# Writeup - LaTeX experience report

A self-contained, Overleaf-ready paper documenting the SGH engine and the
loop-versus-graph study.

## Files

| Path | What it is |
|---|---|
| `paper.tex` | The paper source (single-column `article`, compiles with pdflatex or tectonic). |
| `paper.pdf` | The compiled output (7 pages), for quick reading without a LaTeX toolchain. |
| `figures/fig1_crossover.pdf` | Loop vs graph wall-clock + token tax at two task scales. |
| `figures/fig2_parallelism.pdf` | Gantt of the graph arm's parallel fix-nodes (2.1x speedup). |
| `figures/fig3_cost_vs_correctness.pdf` | Gemini cost-lever study (capability-gated). |
| `figures/fig4_engine_dag.pdf` | The engine's own run of the 10-node bug-fix DAG (Graphviz). |
| `figures/fig5_scorecard.pdf` | At-a-glance scorecard tile panel. |

The figures are generated from real run data by `../showcase/make_figures.py`
(figs 1, 2, 3, 5) and `../showcase/engine_dag.dot` (fig 4). Every number in the
paper traces back to a run file in `../experiments/outreach-bugfix/runs/`.

## Compile on Overleaf

1. Create a new Overleaf project and upload `paper.tex` plus the whole `figures/`
   folder (keep the folder name `figures/`).
2. Set the compiler to **pdfLaTeX** (Menu -> Compiler). Recompile.

`\graphicspath` already points at `figures/`, so no path edits are needed.

## Compile locally

```bash
tectonic paper.tex          # single binary, auto-fetches packages
# or: pdflatex paper.tex
```
