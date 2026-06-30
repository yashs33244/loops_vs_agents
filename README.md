# Graph Harness (SGH)

A reference implementation of *From Agent Loops to Structured Graphs: A Scheduler-Theoretic Framework for LLM Agent Execution* ([arXiv:2604.11378](https://arxiv.org/abs/2604.11378)) - the "graphs over loops for agents" paper.

The paper is a position paper with **no code**. This project builds the engine it specifies: a controllable DAG execution engine for LLM agents (deterministic ready-set scheduler, immutable plan versions, contract validation, a 3-level recovery protocol), then runs the paper's own seven-group ablation to test whether graph execution actually beats agent loops.

## Status

**The engine is built and runs end-to-end.** ~2,655 lines of Go + ~2,872 lines of tests (90 test
functions) across 9 packages, stdlib-only. The single-writer scheduler is `-race` clean. It runs a plan
(a DAG of nodes) to completion - in parallel where the graph allows - with `all_of`/`any_of` joins,
output-contract validation, a bounded 3-level recovery protocol, and an append-only write-ahead log.

```bash
cd engine
go build ./... && go test ./...                              # all green
go run ./cmd/sgh run examples/bugfix.json --provider mock    # deterministic, no network
GEMINI_API_KEY=... go run ./cmd/sgh run examples/bugfix.json --provider gemini   # real LLM nodes
```

Both provider scenarios are proven on the paper's 10-node bug-fix DAG: every node reaches a terminal
state, one `any_of` loser is skipped, peak parallelism |U|=3.

## Layout

```
paper/    the source paper (PDF)
spike/    M0 feasibility probe (Go) - validated Claude CLI + Gemini API as completion nodes
engine/   the SGH engine in Go (DONE) - see engine/AGENTS.md and engine/ENGINE_SPEC.md
  plan contract provider node validate wal recovery scheduler  + cmd/sgh + examples
eval/     Gemini per-node/per-iteration latency analysis
experiments/outreach-bugfix/   empirical study: loop vs graph + cost levers on real code
docs/     PAPER_EXPLAINED framing + first-principles (OS/MoE/BDH) + diagrams
```

See `engine/AGENTS.md` to work in the engine, `PROJECT_PLAN.md` for the plan and locked decisions, and
`PAPER_EXPLAINED.md` for a plain-English breakdown of the paper.
