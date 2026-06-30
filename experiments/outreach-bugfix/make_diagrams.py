#!/usr/bin/env python3
"""Render the ACTUAL structure of each arm (from the run logs) to SVG + PNG via
Graphviz. Data-driven: reads runs/arm_loop.json and runs/arm_graph.json so the
diagrams reflect what really happened.

Outputs: diagrams/loop-arm.{svg,png}, diagrams/graph-arm.{svg,png}
"""
import json
import os
import subprocess

HERE = os.path.dirname(os.path.abspath(__file__))
RUNS = os.path.join(HERE, "runs")
DIAG = os.path.join(HERE, "diagrams")

LOOP = "#E8743B"
GRAPH = "#19A979"
ACTION_FILL = {
    "run_tests": "#CBD5E1",
    "read": "#BFDBFE",
    "edit": "#FDBA74",
    "think": "#FDE68A",
}


def esc(s, n=42):
    s = (s or "").replace('"', "'").replace("\\", "/").replace("&", "and")
    return s if len(s) <= n else s[: n - 1] + "..."


def render(dot_text, name):
    os.makedirs(DIAG, exist_ok=True)
    dot_path = os.path.join(DIAG, name + ".dot")
    with open(dot_path, "w") as f:
        f.write(dot_text)
    for fmt in ("svg", "png"):
        subprocess.run(["dot", f"-T{fmt}", dot_path, "-o", os.path.join(DIAG, f"{name}.{fmt}")], check=True)
    print(f"wrote diagrams/{name}.svg + .png")


def loop_diagram():
    d = json.load(open(os.path.join(RUNS, "arm_loop.json")))
    u = d["_usage"]
    header = (f"LOOP ARM  -  single agent, |U|=1 at every step\\l"
              f"{u['duration_ms']/1000:.0f}s   {u['subagent_tokens']/1000:.1f}k tokens   "
              f"{u['tool_uses']} tool calls   ->  {d['final_pytest']}\\l")
    lines = [
        'digraph loop {',
        '  rankdir=TB; bgcolor="white"; pad=0.3;',
        '  labelloc="t"; fontname="Helvetica-Bold"; fontsize=16;',
        f'  label="{header}";',
        '  node [shape=box style="rounded,filled" fontname="Helvetica" fontsize=11 margin="0.18,0.10" color="#94A3B8"];',
        '  edge [color="#64748B" arrowsize=0.8];',
    ]
    prev = None
    for s in d["steps"]:
        nid = f"s{s['n']}"
        fill = ACTION_FILL.get(s["action"], "#FFFFFF")
        sub = f"\\n{esc(s['file'],34)}" if s.get("file") else ""
        pt = f"\\n[{esc(s['pytest'],28)}]" if s.get("pytest") else ""
        label = f"{s['n']}. {s['action']}{sub}\\n{esc(s['desc'],40)}{pt}"
        lines.append(f'  {nid} [label="{label}" fillcolor="{fill}"];')
        if prev:
            lines.append(f"  {prev} -> {nid};")
        prev = nid
    # bracket showing the whole thing is one agent
    lines.append('  subgraph cluster_agent {')
    lines.append(f'    style="rounded"; color="{LOOP}"; penwidth=2; label="1 agent (ReAct loop)"; fontcolor="{LOOP}"; fontname="Helvetica-Bold";')
    lines.append("    " + " ".join(f"s{s['n']};" for s in d["steps"]))
    lines.append("  }")
    lines.append("}")
    render("\n".join(lines), "loop-arm")


def graph_diagram():
    d = json.load(open(os.path.join(RUNS, "arm_graph.json")))
    u = d["_usage"]
    dag = d["dag"]
    header = (f"GRAPH ARM  -  planned DAG of sub-agents\\l"
              f"{u['duration_ms']/1000:.0f}s   {u['subagent_tokens']/1000:.1f}k tokens   "
              f"{u['agent_count']} agents   ->  {d['verify']['summary']}\\l")
    lines = [
        'digraph graph_arm {',
        '  rankdir=TB; bgcolor="white"; pad=0.3; nodesep=0.6; ranksep=0.7;',
        '  labelloc="t"; fontname="Helvetica-Bold"; fontsize=16;',
        f'  label="{header}";',
        '  node [shape=box style="rounded,filled" fontname="Helvetica" fontsize=11 margin="0.2,0.12"];',
        '  edge [color="#475569" arrowsize=0.9];',
        # planner
        f'  planner [label="PLANNER\\n(read-only)\\nemits fix DAG" fillcolor="#A7F3D0" color="{GRAPH}"];',
    ]
    fix_ids = []
    for n in dag["nodes"]:
        nid = n["id"].replace("-", "_")
        fix_ids.append(nid)
        label = f"FIX: {esc(n['id'],24)}\\n{esc(n['file'],30)}\\n{esc(n['title'],46)}"
        lines.append(f'  {nid} [label="{label}" fillcolor="{GRAPH}" fontcolor="white" color="#0F766E"];')
        lines.append(f"  planner -> {nid};")
    # keep the fix wave on one rank to show parallelism
    lines.append("  {rank=same; " + " ".join(f"{i};" for i in fix_ids) + "}")
    lines.append(f'  verify [label="VERIFY checkpoint\\nall_of join (waits for both)\\nrun pytest -> {esc(d["verify"]["summary"],22)}" fillcolor="#34D399" color="#0F766E"];')
    for i in fix_ids:
        lines.append(f"  {i} -> verify;")
    # |U| annotations down the right side
    lines.append('  node [shape=plaintext style="" fillcolor="white" fontcolor="#64748B" fontsize=10];')
    lines.append('  u1 [label="|U| = 1"]; u2 [label="|U| = 2  (parallel)"]; u3 [label="|U| = 1"];')
    lines.append('  edge [style=invis];')
    lines.append("  u1 -> u2 -> u3;")
    lines.append("  {rank=same; planner; u1}")
    lines.append("  {rank=same; " + fix_ids[0] + "; u2}")
    lines.append("  {rank=same; verify; u3}")
    lines.append("}")
    render("\n".join(lines), "graph-arm")


if __name__ == "__main__":
    loop_diagram()
    graph_diagram()
