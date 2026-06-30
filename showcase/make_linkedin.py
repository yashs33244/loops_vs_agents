#!/usr/bin/env python3
"""
Square (1:1) LinkedIn carousel cards for the SGH project.

1500x1500 px each, same visual identity as the paper figures. Every number is
real (from experiments/outreach-bugfix/runs/). Narrative lives in titles and a
consistent footer; the plot area carries only data + value labels (no floating
arrows), so nothing overlaps.
"""
import json, os
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import FancyBboxPatch
import numpy as np

HERE = os.path.dirname(os.path.abspath(__file__))
RUNS = os.path.join(HERE, "..", "experiments", "outreach-bugfix", "runs")
OUT  = os.path.join(HERE, "linkedin")
os.makedirs(OUT, exist_ok=True)

INK="#1A1D23"; SUBINK="#5A6472"; GRID="#E6E9EE"; PANEL="#F7F8FA"
LOOP="#D1495B"; GRAPH="#2E86AB"; WIN="#2A9D8F"; WARN="#E9A12E"; PAPER="#FBFBFD"
HANDLE="github.com/yashs33244/loops_vs_agents"

plt.rcParams.update({
    "font.family":"Helvetica","axes.edgecolor":SUBINK,"axes.linewidth":1.0,
    "text.color":INK,"xtick.color":INK,"ytick.color":INK,
    "savefig.dpi":200,"figure.dpi":110,
})

def load(n):
    with open(os.path.join(RUNS,n)) as f: return json.load(f)
pilot=load("metrics.json")["arms"]; hard=load("claude_arms.json"); gem=load("gemini_levers.json")["arms"]

def new_card(bg="white"):
    fig=plt.figure(figsize=(7.5,7.5))
    fig.patch.set_facecolor(bg)
    return fig

def header(fig, kicker, accent):
    # top accent rule + kicker
    fig.add_artist(plt.Line2D([0.07,0.30],[0.945,0.945], color=accent, lw=5,
                              solid_capstyle="round", transform=fig.transFigure))
    fig.text(0.07,0.905,kicker,fontsize=13,fontweight="bold",color=accent,
             transform=fig.transFigure)

def footer(fig, dark=False):
    c = "#C7CDD6" if dark else SUBINK
    fig.text(0.07,0.045,HANDLE,fontsize=12.5,color=c,fontweight="bold")
    fig.text(0.93,0.045,"Yash Singh",fontsize=12.5,color=c,ha="right")

def save(fig,name,bg="white"):
    fig.savefig(os.path.join(OUT,name),facecolor=bg)
    plt.close(fig); print("wrote",name)

def style(ax):
    for s in ("top","right"): ax.spines[s].set_visible(False)
    ax.set_axisbelow(True); ax.grid(axis="y",color=GRID,lw=1); ax.tick_params(length=0)

# ---------- Card 1: hook (dark) ----------
def card1():
    fig=new_card(INK)
    fig.add_artist(plt.Line2D([0.09,0.32],[0.93,0.93],color=GRAPH,lw=6,
                   solid_capstyle="round",transform=fig.transFigure))
    fig.text(0.09,0.885,"OPEN-SOURCE  PROJECT",fontsize=13.5,fontweight="bold",color=GRAPH)
    fig.text(0.09,0.70,"A 2026 paper said AI\nagents should run as\ngraphs, not loops.",
             fontsize=33,fontweight="bold",color="white",linespacing=1.25,va="top")
    fig.text(0.09,0.40,"It shipped zero code.",fontsize=26,color="#AEB6C2",va="top")
    fig.text(0.09,0.31,"So I built the engine,\nthen tested the claim.",
             fontsize=29,fontweight="bold",color=WIN,linespacing=1.25,va="top")
    footer(fig,dark=True)
    save(fig,"card1_hook.png",INK)

# ---------- Card 2: the crossover ----------
def card2():
    fig=new_card(); header(fig,"THE RESULT",GRAPH)
    fig.text(0.07,0.855,"Graphs beat loops,\nbut only at scale",fontsize=30,
             fontweight="bold",va="top",linespacing=1.15)
    ax=fig.add_axes([0.13,0.20,0.80,0.46]); style(ax)
    x=np.arange(2); w=0.36
    loop=[pilot["loop"]["wall_s"],hard["loop"]["wall_s"]]
    graph=[pilot["graph"]["wall_s"],hard["graph"]["wall_parallel_s"]]
    b1=ax.bar(x-w/2,loop,w,color=LOOP,label="Loop (ReAct)")
    b2=ax.bar(x+w/2,graph,w,color=GRAPH,label="Graph (SGH)")
    ax.set_xticks(x); ax.set_xticklabels(["Small task\n(2 bugs)","Large task\n(7 issues)"],fontsize=13)
    ax.set_ylim(0,195); ax.set_ylabel("Wall-clock (s)",fontsize=13)
    for b in list(b1)+list(b2):
        ax.annotate(f"{b.get_height():.0f}s",(b.get_x()+b.get_width()/2,b.get_height()+3),
                    ha="center",fontsize=12.5,fontweight="bold")
    ax.legend(frameon=False,fontsize=12.5,loc="upper left",ncol=1)
    fig.text(0.07,0.115,"23% faster on the larger task. Same model, objective test oracle.",
             fontsize=14,color=SUBINK)
    footer(fig); save(fig,"card2_crossover.png")

# ---------- Card 3: parallelism / 2.1x ----------
def card3():
    fig=new_card(); header(fig,"WHY IT WINS",GRAPH)
    fig.text(0.07,0.855,"It runs independent\nwork in parallel",fontsize=30,
             fontweight="bold",va="top",linespacing=1.15)
    fig.text(0.93,0.80,"2.1x",fontsize=58,fontweight="bold",color=WIN,ha="right",va="top")
    ax=fig.add_axes([0.07,0.22,0.86,0.40])
    for s in ("top","right","left"): ax.spines[s].set_visible(False)
    ax.tick_params(length=0); ax.grid(axis="x",color=GRID,lw=1); ax.set_axisbelow(True)
    nodes=hard["graph"]["nodes"]; par=hard["graph"]["wall_parallel_s"]; seq=hard["graph"]["wall_sequential_equiv_s"]
    cols=[GRAPH,"#3FA7D6","#1B4965"]; yp=[2,1,0]
    for y,n,c in zip(yp,nodes,cols):
        ax.barh(y,n["wall_s"],height=0.6,color=c)
        ax.text(3,y,n["name"],va="center",color="white",fontsize=12.5,fontweight="bold")
        ax.text(n["wall_s"]+3,y,f"{n['wall_s']:.0f}s",va="center",fontsize=12)
    ax.axvline(par,color=WIN,lw=2,ls="--")
    ax.set_yticks([]); ax.set_xlim(0,seq+15); ax.set_xlabel("Seconds",fontsize=12.5)
    fig.text(0.07,0.135,f"{seq:.0f}s of work, finished in a {par:.0f}s makespan",
             fontsize=14.5,color=INK,fontweight="bold")
    fig.text(0.07,0.105,"(three fix-nodes the scheduler proved independent)",fontsize=13,color=SUBINK)
    footer(fig); save(fig,"card3_parallel.png")

# ---------- Card 4: cost vs correctness ----------
def card4():
    fig=new_card(); header(fig,"THE CATCH",WARN)
    fig.text(0.07,0.855,"But cheaper\nisn't free",fontsize=30,fontweight="bold",va="top",linespacing=1.15)
    order=["naive","optimized","balanced"]; cols=[WIN,WARN,LOOP]
    toks=[gem[a]["total_tokens"]/1000 for a in order]; fixed=[gem[a]["hardening_fixed"] for a in order]
    x=np.arange(3)
    ax1=fig.add_axes([0.10,0.20,0.37,0.46]); style(ax1)
    b=ax1.bar(x,toks,0.6,color=cols)
    ax1.set_xticks(x); ax1.set_xticklabels(["Full","Scoped","Minimal"],fontsize=11.5)
    ax1.set_ylim(0,26); ax1.set_ylabel("Tokens (thousands)",fontsize=12)
    for bb,t in zip(b,toks): ax1.annotate(f"{t:.0f}k",(bb.get_x()+bb.get_width()/2,t+0.4),ha="center",fontsize=11.5,fontweight="bold")
    ax1.set_title("less context, fewer tokens",fontsize=12,color=SUBINK)
    ax2=fig.add_axes([0.57,0.20,0.37,0.46]); style(ax2)
    b2=ax2.bar(x,fixed,0.6,color=cols)
    ax2.set_xticks(x); ax2.set_xticklabels(["Full","Scoped","Minimal"],fontsize=11.5)
    ax2.set_ylim(0,5.6); ax2.set_yticks(range(6)); ax2.set_ylabel("Fixes that landed (of 5)",fontsize=12)
    for bb,fv in zip(b2,fixed): ax2.annotate(f"{fv}/5",(bb.get_x()+bb.get_width()/2,fv+0.12),ha="center",fontsize=12.5,fontweight="bold")
    ax2.set_title("...correctness collapses",fontsize=12,color=SUBINK)
    fig.text(0.07,0.115,"Cut tokens 78% on a small model and correctness fell from 5/5 to 0/5.",
             fontsize=13.5,color=SUBINK)
    footer(fig); save(fig,"card4_cost.png")

# ---------- Card 5: takeaways / CTA (dark) ----------
def card5():
    fig=new_card(INK)
    fig.add_artist(plt.Line2D([0.09,0.32],[0.93,0.93],color=WIN,lw=6,
                   solid_capstyle="round",transform=fig.transFigure))
    fig.text(0.09,0.885,"WHAT I LEARNED",fontsize=13.5,fontweight="bold",color=WIN)
    items=[("Graph vs loop is a task-size call,","not a universal win."),
           ("Parallelism buys wall-clock","at a 2-3x token cost."),
           ("A deterministic verifier is","the one always-safe lever.")]
    y=0.74
    for a,b in items:
        fig.add_artist(plt.Rectangle((0.095,y-0.004),0.022,0.022,color=WIN,
                       transform=fig.transFigure,clip_on=False))
        fig.text(0.140,y,a,fontsize=21,color="white",fontweight="bold",va="baseline")
        fig.text(0.140,y-0.05,b,fontsize=21,color="#AEB6C2",va="baseline")
        y-=0.155
    fig.text(0.09,0.20,"The engine: Go, standard-library-only,",fontsize=15,color="#C7CDD6")
    fig.text(0.09,0.165,"single-writer scheduler, clean under -race.",fontsize=15,color="#C7CDD6")
    fig.text(0.09,0.105,"Code + paper ->  "+HANDLE,fontsize=14.5,color=GRAPH,fontweight="bold")
    save(fig,"card5_takeaways.png",INK)

if __name__=="__main__":
    card1(); card2(); card3(); card4(); card5()
    print("\nLinkedIn cards in",OUT)
