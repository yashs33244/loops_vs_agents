#!/usr/bin/env python3
"""Publication-quality loop-vs-graph comparison plots for the outreach-proj bug-fix
experiment. Reads runs/metrics.json (assembled from both arms) and writes high-DPI
SVG + PNG to plots/.

Usage: python3 plot_compare.py
"""
import json
import os

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib import font_manager  # noqa: F401  (ensures font cache is built)

HERE = os.path.dirname(os.path.abspath(__file__))
PLOTS = os.path.join(HERE, "plots")
METRICS = os.path.join(HERE, "runs", "metrics.json")

# ---- house style (clean, no chartjunk) ----
LOOP = "#E8743B"   # warm orange  = loop arm
GRAPH = "#19A979"  # teal green   = graph arm
INK = "#222222"
GRID = "#D9D9D9"

plt.rcParams.update({
    "figure.facecolor": "white",
    "axes.facecolor": "white",
    "axes.edgecolor": INK,
    "axes.labelcolor": INK,
    "text.color": INK,
    "xtick.color": INK,
    "ytick.color": INK,
    "font.size": 11,
    "font.family": "sans-serif",
    "axes.titlesize": 12,
    "axes.titleweight": "bold",
    "axes.spines.top": False,
    "axes.spines.right": False,
    "figure.dpi": 150,
})


def bars(ax, title, loop_v, graph_v, fmt="{:.0f}", unit="", lower_is_better=True):
    vals = [loop_v, graph_v]
    b = ax.bar(["Loop", "Graph"], vals, color=[LOOP, GRAPH], width=0.62, zorder=3)
    ax.set_title(title)
    ax.grid(axis="y", color=GRID, lw=0.8, zorder=0)
    ax.set_axisbelow(True)
    top = max(vals) if max(vals) > 0 else 1
    ax.set_ylim(0, top * 1.22)
    for rect, v in zip(b, vals):
        ax.text(rect.get_x() + rect.get_width() / 2, v + top * 0.03,
                (fmt.format(v) + unit), ha="center", va="bottom", fontweight="bold", fontsize=10.5)
    # annotate the winner when the two differ meaningfully
    if loop_v != graph_v and max(vals) > 0:
        better_graph = (graph_v < loop_v) if lower_is_better else (graph_v > loop_v)
        if loop_v != 0:
            delta = abs(graph_v - loop_v) / loop_v * 100
            direction = "+" if graph_v > loop_v else "-"  # real direction of change
            tag = f"graph {direction}{delta:.0f}%"
            ax.text(0.5, 0.93, tag, transform=ax.transAxes, ha="center",
                    fontsize=9, color=GRAPH if better_graph else LOOP, style="italic")
    ax.tick_params(length=0)


def main():
    if not os.path.exists(METRICS):
        raise SystemExit(f"missing {METRICS} - assemble it from both arms first")
    with open(METRICS) as f:
        m = json.load(f)
    os.makedirs(PLOTS, exist_ok=True)
    L = m["arms"]["loop"]
    G = m["arms"]["graph"]

    fig, ax = plt.subplots(2, 3, figsize=(13, 7.6))
    fig.suptitle("Loop vs Graph on real bugs (outreach-proj backend)",
                 fontsize=16, fontweight="bold", y=0.98)
    fig.text(0.5, 0.935,
             f"task: {m.get('task','')}   |   ground truth: {m.get('ground_truth','')}   |   "
             f"both arms = Claude sub-agents, identical start ({m.get('baseline','')})",
             ha="center", fontsize=9.5, color="#555555")

    bars(ax[0][0], "Agents spawned (orchestration cost)", L["agents"], G["agents"])
    bars(ax[0][1], "Wall-clock time", L["wall_s"], G["wall_s"], fmt="{:.0f}", unit="s")
    bars(ax[0][2], "Tokens used", L["tokens"] / 1000, G["tokens"] / 1000, fmt="{:.1f}", unit="k")
    bars(ax[1][0], "Tool calls", L["tool_uses"], G["tool_uses"])
    bars(ax[1][1], "Max parallelism (|U|)", L["max_parallel"], G["max_parallel"],
         fmt="{:.0f}", lower_is_better=False)
    bars(ax[1][2], "Diff size (lines changed)", L["lines_added"] + L["lines_removed"],
         G["lines_added"] + G["lines_removed"])

    # success ribbon under the title row
    for a, arm, col in ((ax[0][0], L, LOOP), (ax[0][2], G, GRAPH)):
        pass
    fig.text(0.5, 0.0,
             f"Both arms reached ground truth: loop={'PASS' if L['success'] else 'FAIL'}, "
             f"graph={'PASS' if G['success'] else 'FAIL'}"
             f"{'  (graph used a recovery round)' if G.get('recovered') else ''}",
             ha="center", fontsize=10, color=INK)

    fig.tight_layout(rect=[0, 0.03, 1, 0.92])
    for ext in ("svg", "png"):
        fig.savefig(os.path.join(PLOTS, f"loop_vs_graph.{ext}"), bbox_inches="tight")
    plt.close(fig)
    print(f"wrote {PLOTS}/loop_vs_graph.svg and .png")


if __name__ == "__main__":
    main()
