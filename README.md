# Graph Harness (SGH)

A reference implementation of *From Agent Loops to Structured Graphs: A Scheduler-Theoretic Framework for LLM Agent Execution* ([arXiv:2604.11378](https://arxiv.org/abs/2604.11378)) - the "graphs over loops for agents" paper.

The paper is a position paper with **no code**. This project builds the engine it specifies: a controllable DAG execution engine for LLM agents (deterministic ready-set scheduler, immutable plan versions, contract validation, a 3-level recovery protocol), then runs the paper's own seven-group ablation to test whether graph execution actually beats agent loops.

## Status

**M0 - CLI feasibility spike (GO/NO-GO).** Before building the engine, we validate the load-bearing assumption: can the Claude Code CLI and Gemini CLI act as clean "completion nodes" - isolated, concurrent, cancellable, and reliably returning strict JSON?

## Layout

```
paper/   the source paper (PDF)
spike/   M0 throwaway probe (Go, stdlib only) - run: cd spike && go run .
engine/  the SGH engine in Go            (added once the spike passes)
eval/    the seven-group ablation harness (Python; added in v2)
```

See `PROJECT_PLAN.md` for the full plan and locked decisions, and `PAPER_EXPLAINED.md` for a plain-English breakdown of the paper.
