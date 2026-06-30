#!/usr/bin/env python3
"""Graph Harness eval probe (M-eval, early): run the paper's 10-node bug-fix DAG
with each node as a real gemini-3.1-flash-lite call, repeat for N iterations, and
emit two analysis views:

  - per-iteration  : how a full DAG run behaves run-to-run (total latency/tokens,
                     JSON success).
  - per-node       : which node is slow / expensive (mean latency, mean tokens),
                     plus a node x iteration latency heatmap.

The Gemini API key is read from the GEMINI_API_KEY environment variable and is
never written to disk. Model id from GEMINI_MODEL (default gemini-3.1-flash-lite).

Usage:
    GEMINI_API_KEY=... python3 run_eval.py            # 4 iterations
    GEMINI_API_KEY=... ITERATIONS=6 python3 run_eval.py
"""
import csv
import json
import os
import re
import time
import urllib.error
import urllib.request

import matplotlib

matplotlib.use("Agg")  # headless: save PNGs, no display
import matplotlib.pyplot as plt
import numpy as np

MODEL = os.environ.get("GEMINI_MODEL", "gemini-3.1-flash-lite")
KEY = os.environ.get("GEMINI_API_KEY")
ITERATIONS = int(os.environ.get("ITERATIONS", "3"))
# Free-tier Gemini has a tight per-minute quota. Space calls out and back off on
# 429 so the data is clean rather than rate-limit-skewed.
REQUEST_INTERVAL_S = float(os.environ.get("REQUEST_INTERVAL_S", "5"))
MAX_RETRIES = int(os.environ.get("MAX_RETRIES", "4"))

HERE = os.path.dirname(os.path.abspath(__file__))
PLOTS = os.path.join(HERE, "plots")
DATA = os.path.join(HERE, "data")

# The paper's motivating DAG (section 3.6), in topological order. Each node is a
# self-contained prompt that must return strict JSON, so per-node token/latency
# differences are real (search nodes are cheap; analyze/fix/report are heavier).
NODES = [
    ("search_auth", 'List 3 likely file paths for authentication logic in a Python web app. JSON: {"files": ["..."]}'),
    ("search_utils", 'List 3 likely file paths for shared utility/helper code in a Python web app. JSON: {"files": ["..."]}'),
    ("read_auth", 'In one sentence, summarize what a typical Python auth.py module is responsible for. JSON: {"summary": "..."}'),
    ("read_utils", 'In one sentence, summarize what a typical Python utils.py module contains. JSON: {"summary": "..."}'),
    ("analyze", 'A login fails only after a JWT token refresh. Give the single most likely root cause in one sentence. JSON: {"root_cause": "..."}'),
    ("fix_A", 'Describe, in 2-3 sentences, a code patch that fixes a JWT token-refresh bug by correcting expiry validation. JSON: {"patch": "..."}'),
    ("fix_B", 'Describe, in 2-3 sentences, an ALTERNATIVE code patch that fixes a JWT token-refresh bug by refreshing the signing key. JSON: {"patch": "..."}'),
    ("update_docs", 'Write a one-paragraph changelog entry for fixing a JWT token-refresh authentication bug. JSON: {"changelog": "..."}'),
    ("run_tests", 'Two previously failing auth tests now pass and 18 others pass. Summarize the suite status. JSON: {"passed": N, "failed": N}'),
    ("report", 'Write a two-sentence summary report of diagnosing and fixing a JWT token-refresh auth bug. JSON: {"report": "..."}'),
]
NODE_IDS = [n[0] for n in NODES]

ENDPOINT = "https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?key={key}"


def _retry_delay(raw):
    """Pull Gemini's suggested retryDelay (e.g. \"21s\") out of a 429 body."""
    m = re.search(r'"retryDelay"\s*:\s*"(\d+)s"', raw)
    return float(m.group(1)) if m else None


def call_gemini(prompt, timeout=60, max_retries=MAX_RETRIES):
    """Return (latency_ms, total_tokens, json_ok, error). Retries on HTTP 429
    with the server-suggested delay (or exponential backoff). Reported latency
    is the successful attempt only, never the backoff sleeps."""
    url = ENDPOINT.format(model=MODEL, key=KEY)
    body = json.dumps(
        {
            "contents": [{"parts": [{"text": prompt}]}],
            "generationConfig": {"responseMimeType": "application/json"},
        }
    ).encode()
    last_err = None
    for attempt in range(max_retries + 1):
        req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"}, method="POST")
        t0 = time.perf_counter()
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                data = json.loads(resp.read())
            dur = (time.perf_counter() - t0) * 1000.0
            text = ""
            cands = data.get("candidates", [])
            if cands:
                parts = cands[0].get("content", {}).get("parts", [])
                if parts:
                    text = parts[0].get("text", "")
            tokens = data.get("usageMetadata", {}).get("totalTokenCount", 0)
            try:
                json.loads(text)
                json_ok = True
            except Exception:
                json_ok = False
            return dur, tokens, json_ok, None
        except urllib.error.HTTPError as e:
            raw = ""
            try:
                raw = e.read().decode("utf-8", "replace")
            except Exception:
                pass
            last_err = f"http {e.code}"
            if e.code == 429 and attempt < max_retries:
                delay = _retry_delay(raw) or (20.0 + 15.0 * attempt)
                print(f"       429 rate-limited; backoff {delay:.0f}s (retry {attempt + 1}/{max_retries})")
                time.sleep(delay)
                continue
            return (time.perf_counter() - t0) * 1000.0, 0, False, last_err
        except Exception as e:  # noqa: BLE001 - spike-level robustness
            return (time.perf_counter() - t0) * 1000.0, 0, False, str(e)[:80]
    return 0.0, 0, False, last_err or "exhausted retries"


def run():
    if not KEY:
        raise SystemExit("GEMINI_API_KEY not set in environment.")
    os.makedirs(PLOTS, exist_ok=True)
    os.makedirs(DATA, exist_ok=True)

    n_nodes = len(NODES)
    lat = np.zeros((n_nodes, ITERATIONS))
    tok = np.zeros((n_nodes, ITERATIONS))
    okm = np.zeros((n_nodes, ITERATIONS))
    rows = []

    print(f"eval: model={MODEL}  iterations={ITERATIONS}  nodes={n_nodes}  total_calls={n_nodes * ITERATIONS}\n")
    for it in range(ITERATIONS):
        for ni, (nid, prompt) in enumerate(NODES):
            dur, tokens, json_ok, err = call_gemini(prompt)
            lat[ni, it] = dur
            tok[ni, it] = tokens
            okm[ni, it] = 1.0 if json_ok else 0.0
            rows.append({
                "iteration": it,
                "node": nid,
                "latency_ms": round(dur, 1),
                "tokens": tokens,
                "json_ok": int(json_ok),
                "error": err or "",
            })
            flag = "ok " if json_ok else "ERR"
            print(f"  it{it} {nid:<12} {dur:7.0f}ms  tok={tokens:<4} {flag}{(' ' + err) if err else ''}")
            time.sleep(REQUEST_INTERVAL_S)  # stay under the free-tier per-minute quota
        print(f"  -- iteration {it}: total={lat[:, it].sum():.0f}ms  tokens={int(tok[:, it].sum())}  json_ok={int(okm[:, it].sum())}/{n_nodes}\n")

    # ---- persist raw data ----
    csv_path = os.path.join(DATA, "results.csv")
    with open(csv_path, "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=["iteration", "node", "latency_ms", "tokens", "json_ok", "error"])
        w.writeheader()
        w.writerows(rows)

    # ---- summary ----
    overall_ok = okm.sum() / okm.size
    print("=== SUMMARY ===")
    print(f"overall JSON success: {okm.sum():.0f}/{okm.size} ({overall_ok * 100:.0f}%)")
    print(f"total tokens: {int(tok.sum())}  total wall (sum of calls): {lat.sum() / 1000:.1f}s")

    make_plots(lat, tok, okm)
    print(f"\nWrote: {csv_path}")
    print(f"Plots: {PLOTS}/per_iteration.png, per_node.png, heatmap_latency.png")


def make_plots(lat, tok, okm):
    n_nodes = lat.shape[0]
    iters = np.arange(lat.shape[1])

    # ---- Figure A: per-iteration ----
    fig, ax = plt.subplots(1, 3, figsize=(16, 4.2))
    fig.suptitle(f"Per-iteration analysis - {MODEL}", fontweight="bold")
    ax[0].bar(iters, lat.sum(axis=0) / 1000.0, color="#4C78A8")
    ax[0].set(title="Total latency per iteration", xlabel="iteration", ylabel="seconds")
    ax[1].bar(iters, tok.sum(axis=0), color="#F58518")
    ax[1].set(title="Total tokens per iteration", xlabel="iteration", ylabel="tokens")
    ax[2].bar(iters, okm.sum(axis=0), color="#54A24B")
    ax[2].axhline(n_nodes, ls="--", color="grey", lw=1)
    ax[2].set(title="JSON-valid nodes per iteration", xlabel="iteration", ylabel=f"nodes (max {n_nodes})", ylim=(0, n_nodes + 0.5))
    for a in ax:
        a.set_xticks(iters)
        a.grid(axis="y", alpha=0.3)
    fig.tight_layout(rect=[0, 0, 1, 0.94])
    fig.savefig(os.path.join(PLOTS, "per_iteration.png"), dpi=130)
    plt.close(fig)

    # ---- Figure B: per-node ----
    means_lat = lat.mean(axis=1)
    std_lat = lat.std(axis=1)
    means_tok = tok.mean(axis=1)
    y = np.arange(n_nodes)
    fig, ax = plt.subplots(1, 2, figsize=(15, 5.5))
    fig.suptitle(f"Per-node analysis (mean over {lat.shape[1]} iterations) - {MODEL}", fontweight="bold")
    ax[0].barh(y, means_lat, xerr=std_lat, color="#4C78A8", capsize=3)
    ax[0].set(title="Mean latency per node (+/- std)", xlabel="ms")
    ax[1].barh(y, means_tok, color="#F58518")
    ax[1].set(title="Mean tokens per node", xlabel="tokens")
    for a in ax:
        a.set_yticks(y)
        a.set_yticklabels(NODE_IDS)
        a.invert_yaxis()  # topo order top-down
        a.grid(axis="x", alpha=0.3)
    fig.tight_layout(rect=[0, 0, 1, 0.94])
    fig.savefig(os.path.join(PLOTS, "per_node.png"), dpi=130)
    plt.close(fig)

    # ---- Figure C: node x iteration latency heatmap ----
    fig, ax = plt.subplots(figsize=(1.2 * lat.shape[1] + 4, 0.5 * n_nodes + 2))
    im = ax.imshow(lat, aspect="auto", cmap="viridis")
    ax.set_yticks(np.arange(n_nodes))
    ax.set_yticklabels(NODE_IDS)
    ax.set_xticks(iters)
    ax.set_xticklabels([f"it{i}" for i in iters])
    ax.set(title=f"Per-node latency (ms) by iteration - {MODEL}", xlabel="iteration", ylabel="node")
    for i in range(n_nodes):
        for j in range(lat.shape[1]):
            ax.text(j, i, f"{lat[i, j]:.0f}", ha="center", va="center", color="white", fontsize=8)
    fig.colorbar(im, ax=ax, label="latency (ms)")
    fig.tight_layout()
    fig.savefig(os.path.join(PLOTS, "heatmap_latency.png"), dpi=130)
    plt.close(fig)


if __name__ == "__main__":
    run()
