#!/usr/bin/env python3
"""Gemini lever-experiment: naive graph vs optimized graph, on the REAL outreach-proj code.

It fixes 3 independent single-file hardening items with gemini-3.1-flash-lite, in two modes,
and measures the exact token/latency cost of each mode (correctness judged by pytest):

  NAIVE graph     - each fix node is sent the WHOLE repo context (all target files + the test
                    file + an overview), then an extra LLM "verify" node reviews the changes.
                    Mirrors the pilot's graph arm: every agent re-reads everything.
  OPTIMIZED graph - each fix node is sent ONLY its own file + its own failing test + a tiny
                    cheatsheet (context scoping). Verification is pytest (deterministic, free) -
                    no LLM verify node, and the planner uses the small model (it already is).

Levers isolated: (1) context scoping, (2) deterministic verify vs an LLM verify node.

Key is read from GEMINI_API_KEY (never stored). Run:
    GEMINI_API_KEY=... python3 gemini_levers.py
"""
import json
import os
import re
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
BASELINE = os.path.join(HERE, "baseline")
PLOTS = os.path.join(HERE, "plots")
RUNS = os.path.join(HERE, "runs")
MODEL = os.environ.get("GEMINI_MODEL", "gemini-3.1-flash-lite")
KEY = os.environ.get("GEMINI_API_KEY")
PYTEST = "/opt/homebrew/Caskroom/miniforge/base/envs/outreach/bin/python"
INTERVAL = float(os.environ.get("REQUEST_INTERVAL_S", "5"))

# 3 independent single-file fix targets (the hardening items).
TARGETS = [
    {
        "name": "schema_validation",
        "path": "app/schemas/chat.py",
        "tests": ["test_chat_rejects_empty_message", "test_chat_rejects_oversized_message"],
        "cheatsheet": "Pydantic v2. Add a constraint so ChatMessageRequest.message is non-empty and at most 4000 chars; FastAPI then returns 422 automatically.",
    },
    {
        "name": "chat_idor",
        "path": "app/api/v1/chat.py",
        "tests": ["test_chat_on_unknown_campaign_returns_404"],
        "cheatsheet": "CampaignRepository(db).get_by_id_for_user(campaign_id, user.id) returns None if the campaign is missing or not owned. The GET history endpoint in this same file already does this check - do the same in the POST chat endpoint BEFORE returning the StreamingResponse, raising HTTPException(404) if None.",
    },
    {
        "name": "campaigns_pagination",
        "path": "app/api/v1/campaigns.py",
        "tests": ["test_list_campaigns_respects_limit", "test_list_campaigns_respects_offset"],
        "cheatsheet": "The list endpoint returns {\"campaigns\": [...], \"total\": N}. Add limit and offset query params (fastapi Query, sane defaults/caps). limit caps the page; total stays the full count; offset pages through.",
    },
]

ENDPOINT = "https://generativelanguage.googleapis.com/v1beta/models/{m}:generateContent?key={k}"
FIX_SCHEMA = {"type": "OBJECT", "properties": {"content": {"type": "STRING"}}, "required": ["content"]}
VERIFY_SCHEMA = {"type": "OBJECT", "properties": {"ok": {"type": "BOOLEAN"}, "notes": {"type": "STRING"}}, "required": ["ok", "notes"]}


def gem(prompt, schema, max_retries=4):
    """One Gemini call. Returns (parsed_json, prompt_tokens, output_tokens, total_tokens)."""
    url = ENDPOINT.format(m=MODEL, k=KEY)
    body = json.dumps({
        "contents": [{"parts": [{"text": prompt}]}],
        "generationConfig": {"responseMimeType": "application/json", "responseSchema": schema},
    }).encode()
    for attempt in range(max_retries + 1):
        req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"}, method="POST")
        try:
            with urllib.request.urlopen(req, timeout=90) as r:
                data = json.loads(r.read())
            u = data.get("usageMetadata", {})
            text = data["candidates"][0]["content"]["parts"][0]["text"]
            return (json.loads(text),
                    u.get("promptTokenCount", 0), u.get("candidatesTokenCount", 0), u.get("totalTokenCount", 0))
        except urllib.error.HTTPError as e:
            raw = e.read().decode("utf-8", "replace") if e.fp else ""
            if e.code == 429 and attempt < max_retries:
                m = re.search(r'"retryDelay"\s*:\s*"(\d+)s"', raw)
                delay = float(m.group(1)) if m else 20.0 + 15 * attempt
                print(f"       429; backoff {delay:.0f}s")
                time.sleep(delay)
                continue
            raise RuntimeError(f"http {e.code}: {raw[:160]}")
    raise RuntimeError("retries exhausted")


def read(path):
    with open(path, encoding="utf-8") as f:
        return f.read()


def copy_baseline():
    dst = tempfile.mkdtemp(prefix="sgh-arm-")
    shutil.copytree(BASELINE, dst, dirs_exist_ok=True,
                    ignore=shutil.ignore_patterns(".venv", "venv", "__pycache__", ".pytest_cache",
                                                  ".git", "node_modules", "*.pyc"))
    return dst


def run_pytest(workdir):
    p = subprocess.run([PYTEST, "-m", "pytest", "-q"], cwd=workdir, capture_output=True, text=True, timeout=240)
    out = p.stdout + p.stderr
    failed = int((re.search(r"(\d+) failed", out) or [0, 0])[1]) if "failed" in out else 0
    passed = int((re.search(r"(\d+) passed", out) or [0, 0])[1]) if "passed" in out else 0
    return failed, passed


def fix_node(workdir, target, mode, shared_block):
    """Run one fix node; write the corrected file; return token tuple."""
    path = target["path"]
    file_text = read(os.path.join(workdir, path))
    test_text = read(os.path.join(workdir, "tests", "test_hardening.py"))
    if mode == "naive":
        ctx = (f"{shared_block}\n\n"
               f"Now fix the file `{path}` so its hardening tests pass. "
               f"Return the COMPLETE corrected file content (not a diff).\n\n"
               f"=== {path} ===\n{file_text}")
    else:  # optimized: scoped
        only_tests = "\n\n".join(
            blk for blk in re.split(r"\n\n\n+", test_text)
            for t in target["tests"] if f"def {t}(" in blk
        ) or test_text
        ctx = (f"Fix the file `{path}` so these tests pass. Return the COMPLETE corrected file "
               f"content (not a diff).\n\nNotes: {target['cheatsheet']}\n\n"
               f"=== {path} ===\n{file_text}\n\n=== the tests to satisfy ===\n{only_tests}")
    res, pin, pout, ptot = gem(ctx, FIX_SCHEMA)
    with open(os.path.join(workdir, path), "w", encoding="utf-8") as f:
        f.write(res["content"])
    time.sleep(INTERVAL)
    return pin, pout, ptot


def run_arm(mode):
    print(f"\n=== ARM: {mode} graph ===")
    workdir = copy_baseline()
    f0, p0 = run_pytest(workdir)
    print(f"  start: {f0} failed, {p0} passed")

    # shared "re-read everything" block (only the naive arm pays this, per node)
    overview = "Repo: a FastAPI backend (outreach-proj). Target files for this hardening PR:\n"
    for t in TARGETS:
        overview += f"\n=== {t['path']} ===\n{read(os.path.join(workdir, t['path']))}\n"
    overview += f"\n=== tests/test_hardening.py ===\n{read(os.path.join(workdir, 'tests', 'test_hardening.py'))}\n"

    calls = []  # (label, pin, pout, ptot)
    t_start = time.time()
    for t in TARGETS:
        pin, pout, ptot = fix_node(workdir, t, mode, overview)
        calls.append((f"fix:{t['name']}", pin, pout, ptot))
        print(f"  fix {t['name']:<22} in={pin:<6} out={pout:<5} total={ptot}")

    if mode == "naive":  # extra LLM verify node (overhead the optimized arm skips)
        changed = "\n".join(f"--- {t['path']} ---\n{read(os.path.join(workdir, t['path']))}" for t in TARGETS)
        res, pin, pout, ptot = gem(
            f"You are a code reviewer. Here are the edited files. Reply ok=true if they look correct "
            f"and complete for a hardening PR (input validation, IDOR 404, pagination), else ok=false.\n\n{changed}",
            VERIFY_SCHEMA)
        calls.append(("verify:llm", pin, pout, ptot))
        print(f"  verify (LLM)           in={pin:<6} out={pout:<5} total={ptot}  -> ok={res.get('ok')}")
        time.sleep(INTERVAL)

    wall = time.time() - t_start
    f1, p1 = run_pytest(workdir)  # deterministic ground truth for BOTH arms
    print(f"  end:   {f1} failed, {p1} passed   (verify = {'pytest' if mode=='optimized' else 'LLM node + pytest check'})")
    shutil.rmtree(workdir, ignore_errors=True)

    return {
        "mode": mode,
        "llm_calls": len(calls),
        "input_tokens": sum(c[1] for c in calls),
        "output_tokens": sum(c[2] for c in calls),
        "total_tokens": sum(c[3] for c in calls),
        "wall_s": round(wall, 1),
        "pytest_start": f"{f0}f/{p0}p",
        "pytest_end": f"{f1}f/{p1}p",
        "hardening_fixed": max(0, f0 - f1),
        "per_call": [{"label": c[0], "in": c[1], "out": c[2], "total": c[3]} for c in calls],
    }


def plot(naive, opt):
    import matplotlib
    matplotlib.use("Agg")
    import matplotlib.pyplot as plt
    os.makedirs(PLOTS, exist_ok=True)
    fig, ax = plt.subplots(1, 4, figsize=(18, 4.6))
    fig.suptitle(f"Naive vs optimized graph - same task, {MODEL}  (cost is not the only axis)", fontweight="bold")
    arms = ["naive", "optimized"]
    colors = ["#EA580C", "#059669"]

    tot = [naive["total_tokens"], opt["total_tokens"]]
    ax[0].bar(arms, tot, color=colors)
    ax[0].set(title="Total tokens (lower = cheaper)", ylabel="tokens")
    for i, v in enumerate(tot):
        ax[0].text(i, v, f"{v:,}", ha="center", va="bottom")
    if tot[0]:
        ax[0].text(1, tot[1], f"  -{100*(1-tot[1]/tot[0]):.0f}%", ha="left", va="center", color="#059669", fontweight="bold")

    wall = [naive["wall_s"], opt["wall_s"]]
    ax[1].bar(arms, wall, color=colors)
    ax[1].set(title="LLM wall-clock (s)", ylabel="seconds")
    for i, v in enumerate(wall):
        ax[1].text(i, v, f"{v}s", ha="center", va="bottom")

    calls = [naive["llm_calls"], opt["llm_calls"]]
    ax[2].bar(arms, calls, color=colors)
    ax[2].set(title="LLM calls", ylabel="count")
    for i, v in enumerate(calls):
        ax[2].text(i, v, str(v), ha="center", va="bottom")

    fixed = [naive["hardening_fixed"], opt["hardening_fixed"]]
    bars = ax[3].bar(arms, fixed, color=["#EA580C", "#DC2626"])
    ax[3].axhline(5, ls="--", color="grey", lw=1)
    ax[3].set(title="Hardening tests fixed (higher = correct)", ylabel="tests passing (of 5)", ylim=(0, 5.4))
    for i, v in enumerate(fixed):
        ax[3].text(i, v, f"{v}/5", ha="center", va="bottom", fontweight="bold")

    for a in ax:
        a.grid(axis="y", alpha=0.3)
    fig.tight_layout(rect=[0, 0, 1, 0.92])
    fig.savefig(os.path.join(PLOTS, "gemini_levers.png"), dpi=130)
    fig.savefig(os.path.join(PLOTS, "gemini_levers.svg"))
    plt.close(fig)


def main():
    if not KEY:
        raise SystemExit("GEMINI_API_KEY not set")
    os.makedirs(RUNS, exist_ok=True)
    naive = run_arm("naive")
    opt = run_arm("optimized")
    metrics = {"model": MODEL, "naive": naive, "optimized": opt,
               "token_reduction_pct": round(100 * (1 - opt["total_tokens"] / naive["total_tokens"]), 1) if naive["total_tokens"] else None}
    with open(os.path.join(RUNS, "gemini_levers.json"), "w") as f:
        json.dump(metrics, f, indent=2)
    try:
        plot(naive, opt)
        plotted = True
    except Exception as e:  # noqa: BLE001
        plotted = False
        print(f"  (plot skipped: {e} - run plot step with a matplotlib-enabled python)")

    print("\n=== SUMMARY (naive -> optimized) ===")
    print(f"  total tokens : {naive['total_tokens']:,} -> {opt['total_tokens']:,}  ({metrics['token_reduction_pct']}% less)")
    print(f"  input tokens : {naive['input_tokens']:,} -> {opt['input_tokens']:,}")
    print(f"  LLM calls    : {naive['llm_calls']} -> {opt['llm_calls']}")
    print(f"  wall-clock   : {naive['wall_s']}s -> {opt['wall_s']}s")
    print(f"  pytest       : naive {naive['pytest_end']} | optimized {opt['pytest_end']}  (5 hardening tests targeted)")
    print(f"\n  wrote runs/gemini_levers.json, plots/gemini_levers.{{png,svg}}")


if __name__ == "__main__":
    main()
