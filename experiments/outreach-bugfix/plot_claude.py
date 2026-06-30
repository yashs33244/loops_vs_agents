#!/usr/bin/env python3
"""Plot the Claude loop-vs-graph result on the hardening task. Reads runs/claude_arms.json.
Run with a matplotlib-enabled python: python3 plot_claude.py"""
import json
import os

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt

HERE = os.path.dirname(os.path.abspath(__file__))
m = json.load(open(os.path.join(HERE, "runs", "claude_arms.json")))
L, G = m["loop"], m["graph"]
arms = ["loop\n(1 agent)", "graph\n(3 parallel agents)"]
x = [0, 1]
BLUE, GREEN, GREY = "#4C78A8", "#059669", "#D1D5DB"

fig, ax = plt.subplots(1, 4, figsize=(18, 5))
fig.suptitle("Claude on the same task: loop vs graph (both reach 25/25)", fontweight="bold")

# tokens (graph costs more)
tok = [L["tokens"], G["tokens"]]
ax[0].bar(x, tok, color=[BLUE, "#EA580C"])
ax[0].set(title="Total tokens (lower = cheaper)", ylabel="tokens")
for i, v in enumerate(tok):
    ax[0].text(i, v, f"{v:,}", ha="center", va="bottom")
ax[0].text(1, G["tokens"], f"  {G['tokens']/L['tokens']:.1f}x", ha="left", va="center", color="#EA580C", fontweight="bold")

# wall-clock (graph wins via parallelism); show sequential-equiv as a ghost bar
wall = [L["wall_s"], G["wall_parallel_s"]]
ax[1].bar(x, wall, color=[BLUE, GREEN])
ax[1].bar([1], [G["wall_sequential_equiv_s"]], color="none", edgecolor=GREY, linestyle="--", hatch="//")
ax[1].set(title="Wall-clock (lower = faster)", ylabel="seconds")
for i, v in enumerate(wall):
    ax[1].text(i, v, f"{v:.0f}s", ha="center", va="bottom")
ax[1].text(1, G["wall_sequential_equiv_s"], f"{G['wall_sequential_equiv_s']:.0f}s if\nrun serially", ha="center", va="bottom", fontsize=8, color="#6B7280")
ax[1].text(1, G["wall_parallel_s"], f"  -{100*(1-G['wall_parallel_s']/L['wall_s']):.0f}%", ha="left", va="center", color=GREEN, fontweight="bold")

# correctness (tie, both correct)
fixed = [25, 25]
ax[2].bar(x, fixed, color=[GREEN, GREEN])
ax[2].set(title="Tests passing (higher = correct)", ylabel="of 25", ylim=(0, 27))
for i, v in enumerate(fixed):
    ax[2].text(i, v, f"{v}/25", ha="center", va="bottom", fontweight="bold")

# diff size (comparable)
dl = [L["diff_lines"], G["diff_lines"]]
ax[3].bar(x, dl, color=[BLUE, BLUE])
ax[3].set(title="Diff size (lines changed)", ylabel="lines")
for i, v in enumerate(dl):
    ax[3].text(i, v, str(v), ha="center", va="bottom")

for a in ax:
    a.grid(axis="y", alpha=0.3)
    a.set_xticks(x)
    a.set_xticklabels(arms)
fig.tight_layout(rect=[0, 0, 1, 0.93])
os.makedirs(os.path.join(HERE, "plots"), exist_ok=True)
fig.savefig(os.path.join(HERE, "plots", "claude_loop_vs_graph.png"), dpi=130)
fig.savefig(os.path.join(HERE, "plots", "claude_loop_vs_graph.svg"))
print("wrote plots/claude_loop_vs_graph.{png,svg}")
