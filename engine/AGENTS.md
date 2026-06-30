# AGENTS.md - Graph Harness engine (for coding agents)

This is a Go implementation of the SGH execution engine from arXiv:2604.11378
("graphs over loops for agents"). Plain-English background: [`../PAPER_EXPLAINED.md`](../PAPER_EXPLAINED.md).
The authoritative build contract is [`ENGINE_SPEC.md`](ENGINE_SPEC.md) - read it before changing anything.

**What it does:** given a *plan* (a DAG of nodes), it schedules and runs the nodes - in parallel where the
graph allows - validates each node's output, recovers from failures with a bounded protocol, logs every
state change, and runs to a terminal state. Nodes can be a mock, a tool, or an LLM call (Claude CLI or
Gemini API).

## Quick start

```bash
go build ./... && go test ./...                 # all green; scheduler tests run with -race
go run ./cmd/sgh run examples/bugfix.json --provider mock     # deterministic, no network
GEMINI_API_KEY=... go run ./cmd/sgh run examples/bugfix.json --provider gemini   # real LLM nodes
go run ./cmd/sgh run examples/bugfix.json --provider claude   # nodes via `claude -p`
```

The CLI prints the transition trace, the final state of every node, and `peak parallelism |U|`.

## Package map

| Package | Responsibility |
|---------|----------------|
| `plan` | the immutable data model: `Plan`, `Node`, `Edge`, `JoinMode` (all_of/any_of), `SideEffectLevel`, the 10-state `NodeState` lifecycle, `Contract`. |
| `validate` | the 5 structural DAG checks (Appendix A.2) + Kahn topological sort. |
| `contract` | per-node output validation (required keys + types). |
| `provider` | LLM backends behind one interface: `MockProvider`, `ClaudeCLIProvider` (`claude -p`), `GeminiAPIProvider` (HTTP, `GEMINI_API_KEY`). |
| `node` | the `Executor` interface + `MockExecutor`, `LLMExecutor` (provider+contract), `ToolExecutor`. |
| `wal` | append-only JSON-lines write-ahead log: `FileLog`, `MemLog`. Replay rebuilds state. |
| `recovery` | the 3-level escalation policy: retry -> patch -> replan -> fail (bounded, always terminates). |
| `scheduler` | the heart: the single-writer event loop. |
| `cmd/sgh` | the CLI. |

## The one rule you must not break

**The scheduler is a single-writer event loop.** Exactly one goroutine owns all mutable node state
(`map[string]*nodeRuntime`). Worker goroutines only run a node and send a completion event back over a
channel; the loop applies it, writes the WAL entry, recomputes the ready-set, and dispatches. Do **not**
add mutexes or shared writes to node state, and do **not** mutate state from a worker. Scheduler tests
must keep passing under `go test -race ./scheduler/`.

## How to extend

- **Add an LLM/tool backend:** implement `provider.Provider` (two methods: `Name()`, `Complete(ctx, Request) (Response, error)`). Drop it into `cmd/sgh`'s `buildExecutor`. Nothing else changes.
- **Add a node kind:** implement `node.Executor` (`Execute(ctx, plan.Node, inputs) Result`). `LLMExecutor` is the reference (builds a prompt, calls a provider, checks the contract).
- **Author a plan:** write JSON matching `plan.Plan` (see `examples/bugfix.json`). It must pass
  `validate.Check` - run the CLI on it, which validates before executing. Use `join: "any_of"` (>= 2
  predecessors) for "try A or B, take whichever finishes"; `all_of` (the default) waits for all.

## Conventions

- **stdlib only** in v1 (no external Go modules). Hand-roll primitives; keep the build offline-safe.
- The interfaces in `ENGINE_SPEC.md` are locked - other packages depend on them verbatim. If one is
  genuinely wrong, change the spec and all callers together, not one side.
- Match the existing style: doc comments tying code to the paper, table-driven tests, `go vet` clean.
