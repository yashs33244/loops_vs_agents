# Graph Harness engine - build spec (the contract every builder follows)

Goal: a **complete, runnable** Go implementation of the SGH engine from arXiv:2604.11378. Given a plan
(a DAG of nodes), it schedules and executes nodes - in parallel where the graph allows - validates each
node's output against a contract, recovers from failures with a bounded protocol, logs every state change,
and runs to a terminal state. It works in **both** provider scenarios:

- **CLI scenario:** nodes run by shelling out to a coding agent CLI (`claude -p`). This is how most people
  already use these models.
- **API scenario:** nodes run via an HTTP API with a key (Gemini). Already validated in `../spike/`.

Plus it ships `AGENTS.md` / `CLAUDE.md` so coding agents understand and can drive/extend it.

## Hard rules

- **stdlib-only for v1.** No external Go modules. (Hand-roll the token bucket, the JSON-shape check, the
  WAL. This matches decision D1 "hand-roll the core" and keeps the build offline-safe.) External libs
  (jsonschema, sqlite, x/sync) are a v2 upgrade behind the same interfaces.
- **Go 1.24**, module `sgh/engine` (already initialized; `engine/plan` exists and must not break).
- Verify continuously: `go build ./...`, `go vet ./...`, `go test ./...` (scheduler tests with `-race`).
- Match the style of `engine/plan/types.go`: clear doc comments tying code to the paper, table-driven tests.

## Locked design (from the engineering review - do not relitigate)

- **D2 Concurrency:** single-writer event loop. ONE goroutine owns all node state. Worker goroutines run
  nodes and send completion events on a channel; the loop applies them, recomputes the ready-set, writes
  the WAL entry, dispatches newly-ready nodes, and handles `any_of` sibling cancellation via `context`.
  No mutexes on node state. Tests run with `-race`.
- **D3 Persistence:** append-only **JSON-lines WAL** (stdlib `encoding/json`), one event per line; replay
  rebuilds state. (SQLite is a v2 upgrade.)
- **D4 Sandbox:** nodes carry a side-effect level; destructive nodes are never speculatively parallel-
  dispatched. CLI provider uses `exec.CommandContext` with an arg slice (no shell), prompt via stdin,
  `context` timeout + process-group kill, run in a temp cwd with a scrubbed env. Pure completion only.
- **D5 Contracts:** each node output checked against a JSON shape (v1: required-keys + type check via
  stdlib `encoding/json`; full JSON Schema is v2). No LLM-judge validators.
- **3-level recovery:** `local_retry` -> `local_patch` -> `request_replan`, strict escalation, budgeted.

## Package layout and responsibilities

```
engine/
  plan/        DONE - Plan, Node, Edge, JoinMode, SideEffectLevel, NodeState (+ helpers), Contract
  contract/    output validation: does a node's JSON output satisfy its contract?
  provider/    LLM backends: Provider interface + Mock, ClaudeCLI, GeminiAPI
  node/        Executor interface + Mock / LLM / Tool executors (LLM wraps provider+contract)
  validate/    DAG validation (the 5 checks) + topological order (Kahn)
  wal/         append-only JSONL write-ahead log: Append + Replay (+ in-memory log for tests)
  scheduler/   the single-writer event loop: ready-set, parallel dispatch, joins, token-bucket
  recovery/    3-level escalation protocol + error classification, used by the scheduler
  cmd/sgh/     CLI: `sgh run <plan.json> --provider mock|claude|gemini` -> runs + prints trace
  examples/    example plans (the paper's bug-fix DAG) as JSON
```

## Interfaces (the integration contract - build to these EXACTLY)

```go
// provider/provider.go
type Request struct {
    Prompt       string
    SystemPrompt string
    Model        string // "" = provider default
    MaxTokens    int
}
type Response struct {
    Text         string
    InputTokens  int
    OutputTokens int
    TotalTokens  int
}
type Provider interface {
    Name() string
    Complete(ctx context.Context, req Request) (Response, error)
}
// impls: MockProvider (deterministic, scriptable), ClaudeCLIProvider (exec `claude -p`),
//        GeminiAPIProvider (HTTP generateContent, key from GEMINI_API_KEY env).

// contract/contract.go
// Validate reports whether `output` (a JSON document) satisfies `schema`
// (v1 schema = {"required":["k"...],"types":{"k":"string|number|bool|object|array"}}).
func Validate(output string, schema json.RawMessage) (ok bool, problems []string)

// node/node.go
type Result struct {
    Output     string                 // raw node output (JSON for LLM nodes)
    Tokens     int
    Retryable  bool                   // true => transient (timeout/ratelimit); false => structural
    Err        error
}
type Executor interface {
    // Execute runs one node given its already-satisfied dependency outputs.
    Execute(ctx context.Context, n plan.Node, inputs map[string]string) Result
}
// impls: MockExecutor (deterministic/scriptable failures for tests),
//        LLMExecutor (builds prompt from node+inputs, calls Provider, checks Contract),
//        ToolExecutor (jailed file/cmd ops by side-effect level).

// validate/validate.go
func Check(p *plan.Plan) []error           // the 5 checks (Appendix A.2)
func TopoOrder(p *plan.Plan) ([]string, error)

// wal/wal.go
type Entry struct {
    RunID, NodeID, Trigger string
    Seq                    int
    TS                     string
    From, To               plan.NodeState
    Payload                json.RawMessage
}
type Log interface {
    Append(e Entry) error
    Replay(runID string) ([]Entry, error)
    Close() error
}
// impls: FileLog (append-only JSONL), MemLog (tests).

// scheduler/scheduler.go
type Options struct {
    MaxParallel int           // worker pool cap
    RatePerSec  float64       // token-bucket throttle for provider calls (0 = unlimited)
    RunID       string
}
type Outcome struct {
    Final     map[string]plan.NodeState // node id -> terminal state
    Rounds    int                       // scheduling rounds
    MaxReady  int                       // peak |U| observed (the parallelism number)
    Succeeded bool                      // plan-level contract satisfied
}
func Run(ctx context.Context, p *plan.Plan, exec node.Executor, log wal.Log, rec recovery.Policy, opts Options) (Outcome, error)
```

## Build waves (dependency order)

- **Wave 1 (parallel; depend only on `plan` + the interfaces above):** `contract`, `provider`
  (Mock+ClaudeCLI+GeminiAPI), `wal` (FileLog+MemLog), `validate` (5 checks + topo).
- **Wave 2 (depend on Wave 1):** `node` executors (Mock/LLM/Tool), `recovery` (escalation policy),
  then `scheduler` (single-writer loop using plan+node+wal+recovery+validate).
- **Wave 3:** `cmd/sgh` CLI + `examples/bugfix.json` + end-to-end demo + `AGENTS.md`/`CLAUDE.md`.

## Definition of done

1. `go build ./... && go vet ./... && go test ./...` all green (scheduler tests pass under `-race`).
2. `go run ./cmd/sgh run examples/bugfix.json --provider mock` runs the paper's 10-node DAG to completion,
   printing the per-round ready-set (showing |U|>1 parallelism) and the final states.
3. The same with `--provider gemini` (GEMINI_API_KEY set) or `--provider claude` runs it for real.
4. A failure-injection test shows the 3-level recovery escalates correctly and always terminates.
5. `AGENTS.md` explains the architecture + how to add a node/provider + how to run, for coding agents.
