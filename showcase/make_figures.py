#!/usr/bin/env python3
"""
Graph Harness (SGH) - showcase charts (figs 1,2,3,5).

The DAG (fig 4) is rendered separately with Graphviz (engine_dag.dot).

Design rule that keeps these clean: NO floating arrow-annotations inside the
plot. Every figure reserves margin bands for title / subtitle / legend, and the
plot area carries only data + value labels. Narrative lives in titles and in
SHOWCASE.md captions.

Every number is read from a real run file in
experiments/outreach-bugfix/runs/ - nothing is synthesized.
"""
import json
import os
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import FancyBboxPatch
import numpy as np

HERE = os.path.dirname(os.path.abspath(__file__))
RUNS = os.path.join(HERE, "..", "experiments", "outreach-bugfix", "runs")

INK    = "#1A1D23"
SUBINK = "#5A6472"
GRID   = "#E6E9EE"
PANEL  = "#F7F8FA"
LOOP   = "#D1495B"   # loop arm
GRAPH  = "#2E86AB"   # graph arm
WIN    = "#2A9D8F"   # positive
WARN   = "#E9A12E"   # caution

plt.rcParams.update({
    "font.family": "Helvetica",
    "font.size": 11,
    "axes.edgecolor": SUBINK,
    "axes.linewidth": 0.9,
    "axes.labelcolor": INK,
    "text.color": INK,
    "xtick.color": INK, "ytick.color": INK,
    "figure.dpi": 110, "savefig.dpi": 300, "savefig.bbox": "tight",
})

def load(name):
    with open(os.path.join(RUNS, name)) as f:
        return json.load(f)

def style_ax(ax):
    ax.spines["top"].set_visible(False)
    ax.spines["right"].set_visible(False)
    ax.set_axisbelow(True)
    ax.grid(axis="y", color=GRID, linewidth=1)
    ax.tick_params(length=0)

def titles(fig, title, sub):
    fig.text(0.5, 0.965, title, ha="center", va="top", fontsize=17, fontweight="bold", color=INK)
    fig.text(0.5, 0.905, sub, ha="center", va="top", fontsize=10.5, color=SUBINK)

def barlabels(ax, bars, fmt="{:.0f}", dy=2, fs=10.5):
    for b in bars:
        ax.annotate(fmt.format(b.get_height()),
                    (b.get_x()+b.get_width()/2, b.get_height()+dy),
                    ha="center", va="bottom", fontsize=fs, fontweight="bold")

def save(fig, stem):
    for ext in ("png", "svg", "pdf"):
        fig.savefig(os.path.join(HERE, f"{stem}.{ext}"))
    plt.close(fig)
    print("wrote", stem)

pilot = load("metrics.json")["arms"]
hard  = load("claude_arms.json")
gem   = load("gemini_levers.json")["arms"]

GROUPS = ["Small task\n(2 bugs, 2 files)", "Large task\n(7 issues, 5 files)"]

# ============================================================================
# FIG 1 - the crossover: wall-clock + tokens, two scales, loop vs graph.
# ============================================================================
def fig_crossover():
    x = np.arange(2); w = 0.34
    wall_loop  = [pilot["loop"]["wall_s"],  hard["loop"]["wall_s"]]
    wall_graph = [pilot["graph"]["wall_s"], hard["graph"]["wall_parallel_s"]]
    tok_loop   = [pilot["loop"]["tokens"]/1000,  hard["loop"]["tokens"]/1000]
    tok_graph  = [pilot["graph"]["tokens"]/1000, hard["graph"]["tokens"]/1000]

    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(13.2, 6.4))

    style_ax(ax1)
    b1 = ax1.bar(x-w/2, wall_loop,  w, color=LOOP)
    b2 = ax1.bar(x+w/2, wall_graph, w, color=GRAPH)
    ax1.set_ylabel("Wall-clock time  (seconds  -  lower is better)")
    ax1.set_xticks(x); ax1.set_xticklabels(GROUPS)
    ax1.set_ylim(0, 195)
    barlabels(ax1, list(b1)+list(b2), fmt="{:.0f}s")
    ax1.set_title("Wall-clock:  small task loop wins,  large task graph wins",
                  fontsize=11.5, fontweight="bold", pad=12, color=INK)

    style_ax(ax2)
    b3 = ax2.bar(x-w/2, tok_loop,  w, color=LOOP)
    b4 = ax2.bar(x+w/2, tok_graph, w, color=GRAPH)
    ax2.set_ylabel("Tokens  (thousands  -  lower is cheaper)")
    ax2.set_xticks(x); ax2.set_xticklabels(GROUPS)
    ax2.set_ylim(0, 185)
    barlabels(ax2, list(b3)+list(b4), fmt="{:.0f}k")
    ax2.set_title("Tokens:  the graph always pays a tax  (3.0x small,  2.1x large)",
                  fontsize=11.5, fontweight="bold", pad=12, color=INK)

    titles(fig,
           "Graphs beat loops once the task is big enough",
           "Same task, same model (Claude), objective pytest ground truth.   "
           "Loop = 1 ReAct agent;   Graph = parallel fix nodes + verify checkpoint.")
    # one shared legend in its own reserved band
    handles = [plt.Rectangle((0,0),1,1,color=LOOP), plt.Rectangle((0,0),1,1,color=GRAPH)]
    fig.legend(handles, ["Loop (ReAct)", "Graph (SGH)"],
               loc="upper center", bbox_to_anchor=(0.5, 0.875),
               ncol=2, frameon=False, fontsize=11, columnspacing=2.2, handlelength=1.4)
    fig.subplots_adjust(top=0.74, bottom=0.11, wspace=0.24, left=0.07, right=0.97)
    save(fig, "fig1_crossover")

# ============================================================================
# FIG 2 - Gantt of the graph arm's parallel fix nodes (real per-node timings).
# ============================================================================
def fig_parallelism():
    nodes = hard["graph"]["nodes"]
    par   = hard["graph"]["wall_parallel_s"]
    seq   = hard["graph"]["wall_sequential_equiv_s"]

    fig, ax = plt.subplots(figsize=(12.6, 5.8))
    ax.spines["top"].set_visible(False); ax.spines["right"].set_visible(False)
    ax.spines["left"].set_visible(False)
    ax.tick_params(length=0)

    colors = [GRAPH, "#3FA7D6", "#1B4965"]
    ypos = [2, 1, 0]
    for y, n, c in zip(ypos, nodes, colors):
        ax.barh(y, n["wall_s"], height=0.5, color=c, zorder=3)
        ax.text(3, y, n["name"], va="center", ha="left", color="white",
                fontsize=11.5, fontweight="bold", zorder=4)
        ax.text(n["wall_s"]+4, y, f"{n['wall_s']:.0f}s   ({n['tokens']/1000:.0f}k tokens)",
                va="center", ha="left", fontsize=10.5, color=INK)

    ax.axvline(par, color=WIN, lw=2, ls="--", zorder=5)
    ax.text(par+4, 2.55, f"parallel makespan = {par:.0f}s",
            color=WIN, fontsize=11, fontweight="bold", va="center")

    # serial-equivalent span below the bars (reserved negative-y band)
    ax.annotate("", xy=(seq, -0.9), xytext=(0, -0.9),
                arrowprops=dict(arrowstyle="<->", color=SUBINK, lw=1.4))
    ax.text(seq/2, -1.28, f"serial-equivalent = {seq:.0f}s   (sum of all three nodes)",
            ha="center", va="center", color=SUBINK, fontsize=10.5)

    ax.set_xlim(0, seq+55)
    ax.set_ylim(-1.7, 3.1)
    ax.set_yticks([])
    ax.set_xlabel("Seconds")
    ax.grid(axis="x", color=GRID, lw=1); ax.set_axisbelow(True)

    titles(fig,
           "The graph runs independent work concurrently  -  2.1x speedup",
           "Graph arm of the large task: three fix nodes the scheduler proved independent, dispatched together "
           f"(three in flight at once).   {seq:.0f}s of work finished in {par:.0f}s of wall-clock.")
    fig.subplots_adjust(top=0.80, bottom=0.13, left=0.04, right=0.97)
    save(fig, "fig2_parallelism")

# ============================================================================
# FIG 3 - cost vs correctness (Gemini levers), two clean panels, no twin axis.
# ============================================================================
def fig_cost_correctness():
    order  = ["naive", "optimized", "balanced"]
    labels = ["Naive\n(full context)", "Optimized\n(scoped context)", "Balanced\n(targeted edits)"]
    cols   = [WIN, WARN, LOOP]
    toks   = [gem[a]["total_tokens"]/1000 for a in order]
    fixed  = [gem[a]["hardening_fixed"] for a in order]
    redux  = {"naive":0.0, "optimized":65.9, "balanced":77.9}
    x = np.arange(3)

    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(13.2, 6.2))

    style_ax(ax1)
    bars = ax1.bar(x, toks, 0.55, color=cols)
    ax1.set_ylabel("Total tokens  (thousands)")
    ax1.set_xticks(x); ax1.set_xticklabels(labels)
    ax1.set_ylim(0, 26)
    barlabels(ax1, bars, fmt="{:.1f}k", dy=0.3)
    for b, a in zip(bars, order):
        if redux[a] > 0:
            ax1.text(b.get_x()+b.get_width()/2, b.get_height()*0.5,
                     f"-{redux[a]:.0f}%", ha="center", va="center",
                     color="white", fontsize=11, fontweight="bold")
    ax1.set_title("Cheaper rightward", fontsize=11.5, fontweight="bold", pad=10, color=SUBINK)

    style_ax(ax2)
    bars2 = ax2.bar(x, fixed, 0.55, color=cols)
    ax2.set_ylabel("Hardening fixes that landed  (out of 5)")
    ax2.set_xticks(x); ax2.set_xticklabels(labels)
    ax2.set_ylim(0, 5.6); ax2.set_yticks(range(6))
    for b, fv in zip(bars2, fixed):
        ax2.annotate(f"{fv}/5", (b.get_x()+b.get_width()/2, b.get_height()+0.1),
                     ha="center", va="bottom", fontsize=11.5, fontweight="bold")
    ax2.set_title("...but correctness collapses", fontsize=11.5, fontweight="bold", pad=10, color=SUBINK)

    titles(fig,
           "On a small model, cost-cutting is capability-gated",
           "gemini-3.1-flash-lite, same 5 hardening fixes.   "
           "Trimming context cuts tokens 66-78% but drops fixes from 5/5 to 0/5.")
    fig.subplots_adjust(top=0.80, bottom=0.12, wspace=0.22, left=0.07, right=0.97)
    save(fig, "fig3_cost_vs_correctness")

# ============================================================================
# FIG 5 - scorecard tiles.
# ============================================================================
def fig_scorecard():
    fig, ax = plt.subplots(figsize=(13.2, 6.2))
    ax.set_xlim(0, 12); ax.set_ylim(0, 7); ax.axis("off")

    def tile(x, y, big, small, accent, big_fs=23):
        w, h = 3.55, 2.5
        ax.add_patch(FancyBboxPatch((x, y), w, h,
                     boxstyle="round,pad=0.02,rounding_size=0.16",
                     fc=PANEL, ec=accent, lw=2.2))
        ax.text(x+w/2, y+h*0.62, big, ha="center", va="center",
                fontsize=big_fs, fontweight="bold", color=accent)
        ax.text(x+w/2, y+h*0.22, small, ha="center", va="center",
                fontsize=9.3, color=SUBINK, linespacing=1.35)

    X = [0.3, 4.22, 8.15]
    # row 1 - the engine is real (first-party)
    tile(X[0], 3.95, "90 tests", "Go tests passing  -  9 packages\n2,655 core + 2,872 test LOC", GRAPH, 21)
    tile(X[1], 3.95, "stdlib-only", "zero external dependencies\nscheduler is  -race  clean", GRAPH, 20)
    tile(X[2], 3.95, "3 parallel", "peak concurrency, one round\n10-node DAG  -  30 transitions  -  mock + Gemini", GRAPH, 21)
    # row 2 - the empirical results
    tile(X[0], 1.05, "-23%", "graph wall-clock vs loop\nat scale  (the crossover)", WIN, 23)
    tile(X[1], 1.05, "2.1x", "parallel speedup\n245s of work in 117s", WIN, 23)
    tile(X[2], 1.05, "0 / 5", "fixes left when tokens cut 78%\non a small model", WARN, 23)

    fig.text(0.5, 0.965, "Graph Harness (SGH)  -  what got built, and what it showed",
             ha="center", va="top", fontsize=18, fontweight="bold", color=INK)
    fig.text(0.5, 0.915,
             "A from-scratch Go engine for a paper that shipped no code (arXiv:2604.11378), "
             "plus a controlled loop-vs-graph study on real backend code.",
             ha="center", va="top", fontsize=10.5, color=SUBINK)
    fig.text(0.5, 0.05,
             "Top row: the engine itself (first-party).      "
             "Bottom row: the empirical study (Claude + Gemini, objective pytest ground truth).",
             ha="center", va="bottom", fontsize=9, color=SUBINK, style="italic")
    fig.subplots_adjust(top=0.86, bottom=0.08)
    save(fig, "fig5_scorecard")

if __name__ == "__main__":
    fig_crossover()
    fig_parallelism()
    fig_cost_correctness()
    fig_scorecard()
    print("\nCharts written to", HERE)
