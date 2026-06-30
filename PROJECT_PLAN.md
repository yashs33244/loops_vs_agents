# Project Plan - Graph Harness (SGH)

> Building the first open reference implementation of *From Agent Loops to Structured Graphs* (arXiv:2604.11378).
> Companion doc: [`PAPER_EXPLAINED.md`](PAPER_EXPLAINED.md)
> Status: DRAFT plan, pending Gate 2 (eng review) before code.

---

## 0. Locked decisions (2026-06-30)

- **Scope:** FULL research project, M0 through M8.
- **Stack:** **Go engine core + Python eval/analysis** (the systems-flex path).
- **Models:** **Claude via the Claude Code CLI** (`claude -p`) and **Gemini via the Gemini CLI** (`gemini -p`), invoked as subprocesses. No raw API keys yet, so token-cost metrics are best-effort (see section 6).
- **Visibility:** **private** repo for now; polish for public later.

---

## 0.5 Locked architecture decisions (Gate 2 eng review, 2026-06-30)

- **D1 Build boundary:** hand-roll the SGH core (validator, ready-set scheduler, node FSM, recovery, WAL); libraries for primitives only (`errgroup`, `x/time/rate`, `santhosh-tekuri/jsonschema`, `container/heap`).
- **D2 Concurrency:** single-writer event loop (actor). One goroutine owns all node state; workers run nodes and send completion events; the loop recomputes the ready-set, emits the WAL entry, dispatches new work, and cancels `any_of` siblings. Tests run with `go test -race`.
- **D3 Persistence:** JSON-lines append-only WAL + SQLite snapshots every ~100 transitions. Replay = restore newest snapshot, apply WAL tail (seq > snapshot seq); idempotent by monotonic seq; torn final line truncated on crash.
- **D4 Executor sandbox:** side-effect levels `{none,read,write,destructive}`; destructive ops allowlisted and never speculatively parallel-dispatched; tool nodes jailed to a per-run temp dir; CLIs via `exec.Command` arg-slice (no shell), prompt via stdin/temp-file, `context` timeout + process-GROUP kill, pure-completion mode (no nested agent tools).
- **D5 Contracts:** JSON Schema on every node + free deterministic semantic checks (compile/tests/parse) where available; NO LLM-judge validators in v1 (added later as a measured option, reporting p_v).
- **D6 Task set:** 10-15 hand-built tasks (simple/medium/complex tiers) with programmatic success checks + hand-authored reference DAGs; deliberately control the parallel-vs-linear mix to avoid inflating G_graph.

**Code-quality notes:** Go structs are the single source of truth for `Plan`/`Node`/`WALEntry`/contract shapes; emit JSON Schema for the Python harness (DRY across the boundary). Package layout: `engine/{plan,scheduler,node,recovery,wal,provider}`, `cmd/sgh`.

**Perf notes:** start with naive `O(|V|+|E|)` ready-set recompute (optimize to incremental only if profiling shows need); wall-clock is dominated by CLI latency (mock nodes in tests, real CLIs only in M8); accept per-call CLI process-spawn overhead in v1.

**Failure modes - 1 known non-critical gap:** a structurally-valid but semantically-wrong planner DAG, on a node lacking a cheap deterministic check, can pass a shallow contract and propagate silently. Inherent (paper §9.6); measured via G_plan; acceptable for a research prototype. All other failure paths have planned tests + error handling and surface explicitly.

**Parallelization:** Lane A (Go engine), Lane B (Python baselines), Lane C (task authoring) run in parallel worktrees; converge at the eval.

---

## 0.6 Outside-voice revisions (Codex challenge, 2026-06-30)

Codex independently challenged the plan (16 findings, 5 P0). Two cross-model decisions changed the plan; eval rigor was baked in regardless.

- **CLI spike goes first (Tension 1 - adopted).** A new **M0-spike** runs BEFORE any engine code: prove ~20 isolated, concurrent `claude -p` / `gemini -p` calls return strict JSON, with real isolation (clean env/cwd, no inherited creds, no nested agent tools), working cancellation, and best-effort token capture. **GO/NO-GO gate:** if the CLIs cannot be coerced into deterministic completion, stop and switch providers (raw API keys) before building the engine. This is the project's load-bearing assumption and it gets validated day one, not at M6.
- **Public credible core first, full engine as v2 (Tension 2 - adopted).** Reordered (below). Repo flips **public** when the v1 core + write-up ships. The **eval is the differentiator**, not the engine (the paper itself calls SGH "Airflow for LLM nodes").

**Eval protocol rigor (additive - baked in regardless):**
- **G0 operationally specified** before the comparison: a genuinely *strong* prompted loop with the same tools/budget/timeout as SGH, not a strawman.
- **Baseline equivalence locked:** adjacent groups (esp. G3 vs G4) get identical plan-input, context, tools, budgets, and failure handling - or the gain is not attributable.
- **Task-selection rules written before authoring:** define parallelizable, define success, and **include negative cases where SGH should lose** (linear/trivial tasks) so the result is not rigged.
- **Honest framing:** v1 is a **pilot** (6-8 tasks, the headline G3-vs-G4 contrast), NOT a replication of the paper's 50-task / 10-run protocol. Report variance; do not overclaim. Additivity of gains is indicative, not exact (gains interact).

**Sandbox hardening (Codex #5):** "temp-dir jail" is upgraded to real isolation - scrubbed environment, dedicated cwd, no inherited credentials/config, network only if the node needs it. A working dir is not a sandbox for an agent CLI.

**Reality check (Codex #8):** the paper's own 3,300-6,500 LOC estimate makes full M0-M8 a multi-month effort. The v1 core is the finishable, shippable unit; v2 is open-ended.

### Revised milestone order

**v1 - public credible core (the finishable unit):**
```
M0-spike  CLI feasibility: isolation + strict JSON out + concurrency + cancellation   [GO/NO-GO]
M0        data model + contracts
M1        DAG validator (5 checks)
M2        FSM + sequential runtime (mock nodes)
M3        concurrency + joins (all_of / any_of + sibling-skip)
Mp        eval protocol + task fixtures DEFINED (before more engine)   <- pulled early (Codex #10)
M-prov    one real provider (from the spike) wired as a completion node
M-eval    6-8 tasks + ONE honest G3-vs-G4 comparison + short write-up
          -> flip repo PUBLIC
```

**v2 - deep systems (open-ended extension):**
```
M4   recovery layer (3-level escalation)
M5   persistence: start with a SQLite events table; add the JSONL WAL only if the demo needs it (Codex #11)
M6   planner (task -> DAG) + multi-model sweep (Claude vs Gemini)
M7   HTTP/WebSocket API (only once the engine runs real nodes - Codex #12)
M8   full G0-G6 ablation, scale tasks toward 50, full report
```

Note: D1-D6 architecture decisions still hold. **D3 (WAL) is deferred to v2 and simplified** to "SQLite events table first; JSONL WAL only if needed." API (M7) and the multi-model sweep move to v2.

### M0-spike RESULT (2026-06-30) - GO on Claude; Gemini CLI blocked

Ran via `spike/` (4 concurrent calls/provider + a cancellation test; raw data in `spike/results.json`):

- **Claude CLI: GO.** 4/4 strict JSON, all clean + correct (answer 42), exit 0, ~4.5-7.7s/call. Concurrency clean; a long call was terminated in ~2s by context-timeout process-group kill.
- **Gemini CLI: BLOCKED.** 4/4 failed with `exit 55 - IneligibleTierError: This client is no longer supported for Gemini Code Assist for individuals ... migrate to Antigravity`. The installed Gemini CLI cannot authenticate on the current tier.

**Consequence:** the v1 **Claude-only** core is unblocked - proceed to M0. The **multi-model sweep (v2) needs a working second provider**: re-auth/migrate the Gemini CLI, add a raw Gemini/OpenAI API key, or pick a different second model. Flagged for v2; not a v1 blocker.

---

## 1. The pitch (what this project is)

The paper is a design with **no implementation and no experiments**. We build that implementation: a **controllable DAG-based execution engine for LLM agents**, expose it as a backend service, and then run the paper's own seven-group experiment to test whether "graphs beat loops" actually holds - across multiple models.

**One-line resume framing (for later):** *"Built the first open-source reference implementation of a scheduler-theoretic LLM agent engine: a concurrent DAG runtime with immutable plan versioning, a 3-level recovery protocol, write-ahead-log persistence, and an ablation harness that empirically tested the paper's claims across N models."*

This is ~80% backend/distributed-systems engineering, ~20% AI integration. That ratio matches your profile exactly.

---

## 2. Goals and non-goals

### Goals
- A working SGH engine that runs real DAGs of LLM/tool nodes with parallelism, recovery, and full auditability.
- A clean HTTP/WebSocket API around it (your "API systems and design" strength on display).
- Crash-safe persistence (WAL + snapshots) and replayable traces.
- An evaluation harness that reproduces the seven-group ablation and swaps models in/out.
- A short write-up of results (the "research" credibility layer).

### Non-goals (explicit scope cuts)
- NOT building a production framework to compete with LangGraph. This is a reference + experiment, not a product.
- NOT implementing `first_of` / competitive parallelism (the paper itself excludes it).
- NOT implementing dynamic topology, recursive sub-graphs, or parent-chain rollback (also excluded by the paper).
- NOT distributed multi-worker execution in v1 (single-process async first; multi-worker is a stretch goal).
- NOT building a planner that beats GPT - the planner is allowed to be "good enough"; we measure planner quality, we don't optimize it.

---

## 3. Stack (LOCKED: Go core + Python eval)

Two codebases in one repo, talking over a stable JSON/HTTP boundary.

**Go - the engine** (`/engine`)

| Concern | Choice | Why |
|---------|--------|-----|
| Language | **Go 1.23** | goroutines + channels are a natural fit for ready-set dispatch; static binary; the "systems" signal you want. |
| Concurrency | goroutines + `errgroup` + bounded worker pool | one goroutine per dispatched node; `golang.org/x/sync` caps parallelism. |
| Contracts | JSON Schema (`santhosh-tekuri/jsonschema`) | output-contract validation for non-deterministic node outputs. |
| API | stdlib `net/http` + a WebSocket lib (`nhooyr/websocket`) | REST + live state-transition stream. |
| Persistence | append-only WAL file + SQLite (`modernc.org/sqlite`, cgo-free) for snapshots/index | crash-safe log; replay on restart. |
| Rate limiting | `golang.org/x/time/rate` (token bucket) | per-provider LLM call throttling. |
| Tests | stdlib `testing` + `testify` | FSM and validator are highly unit-testable. |

**Python - the eval harness** (`/eval`)

| Concern | Choice | Why |
|---------|--------|-----|
| Runner | Python 3.12 + `httpx` | drives the Go engine's API; orchestrates G0-G6 runs across tasks/models. |
| Baselines | plain Python | the loop baselines (G0-G3) are simplest to write here. |
| Analysis | pandas + matplotlib | gain-decomposition tables and charts. |

**LLM access:** a Go `Provider` interface with subprocess implementations for what you have today - `ClaudeCodeProvider` (exec `claude -p`) and `GeminiProvider` (exec `gemini -p`). API-key providers (Anthropic/OpenAI over HTTP) can drop in later behind the same interface for cleaner token metrics.

---

## 4. Architecture

```
                           +-----------------------------+
   task (natural lang) ---> |        PLANNER LAYER        |  LLM or template -> DAG
                           |  task -> Plan(V,E,sigma,k)  |
                           +--------------+--------------+
                                          | validated, immutable Plan v1
                                          v
   +--------------------+   +-----------------------------+   +----------------------+
   |  DAG VALIDATOR     |-->|        RUNTIME LAYER        |-->|   PERSISTENCE (WAL)  |
   |  acyclic, reach,   |   |  ready-set U(S), FSM per    |   |  append-only log +   |
   |  joins, contracts  |   |  node, async dispatch,      |   |  snapshots, replay   |
   +--------------------+   |  token-bucket rate limit    |   +----------------------+
                           +--------------+--------------+
                                          | failure report
                                          v
                           +-----------------------------+
                           |       RECOVERY LAYER        |  L1 retry -> L2 patch -> L3 replan
                           |  diagnoser + escalation     |  (strict, budgeted)
                           +-----------------------------+

   Context split:  C_exec (node inputs/artifacts)  ||  C_diag (failure history, plan versions)
                   the two never mix during node execution
```

Each node is a pluggable executor implementing one interface (`async run(inputs) -> output`), so a node can be a **mock** (deterministic, for tests/benchmarks), a **tool call** (file I/O, shell, http), or an **LLM call** (contract-validated output).

---

## 5. Milestones (phased, each ends in something demoable)

Effort is rough ("sessions" = focused half-days), not calendar dates.

| Phase | Name | Deliverable (demo) | Maps to paper | Effort |
|------:|------|--------------------|---------------|:------:|
| **M0** | Data model + contracts | Typed `Plan`, `Node`, `Edge`, side-effect levels, output contracts; load/dump JSON | Def 5.1, sigma, kappa | 1 |
| **M1** | DAG validator | `validate(plan)` enforcing all 5 checks; red/green test suite | Appendix A.2 | 1 |
| **M2** | FSM + sequential runtime | Run a validated DAG to completion with **mock nodes**; print ready-set evolution | Def 6.1, Table 15, U(S) | 2 |
| **M3** | Concurrency + joins | Parallel dispatch (`|U|>1` realized), `all_of`/`any_of` + sibling-skip, token-bucket | Sec 3, Sec 7 | 2 |
| **M4** | Recovery layer | Induced failures escalate L1->L2->L3 correctly; bounded, no infinite loops | Sec 6, Prop 6.4 | 2 |
| **M5** | Persistence + replay | Kill mid-run, restart, resume from WAL; export full trace | Sec 9.10 | 2 |
| **M6** | LLM nodes + planner | Real bug-fix-style task runs end to end on a real model; planner emits DAG | Planner layer | 2-3 |
| **M7** | API service | `POST /runs`, `GET /runs/{id}` (live status + ready-set), trace endpoint, WebSocket stream | (your add) | 2 |
| **M8** | Baselines + eval | G0-G6 implemented; 10-15 task set; gain-decomposition report across >=2 models | Sec 8 | 3-4 |

**Recommended MVP cut (the "it works + it's mine" milestone):** M0-M4 with mock nodes. That alone is a real, controllable, recovering graph executor and a strong demo. M5 (WAL) and M7 (API) are the backend-depth multipliers. M6 + M8 turn it into a research project worth a write-up.

---

## 6. Evaluation plan (the "test with different data models" part)

Reproduce the paper's seven-group ablation (Section 8). Each group adds exactly one feature, so improvements are attributable:

```
G0 SOTA Loop  -> G1 Naive Loop -> G2 Planner Loop -> G3 Structured Loop
   -> G4 GH-Core -> G5 GH+Patch -> G6 GH+Replan

G_plan     = Perf(G2) - Perf(G1)
G_scaffold = Perf(G3) - Perf(G2)
G_graph    = Perf(G4) - Perf(G3)     <- the headline "does the graph help?" number
G_patch    = Perf(G5) - Perf(G4)
G_replan   = Perf(G6) - Perf(G5)
```

- **Tasks:** start with 10-15 hand-built tasks stratified simple / medium / complex (scale toward 50 later). Mix coding, data-transform, and "operational" shapes so some have real parallelism.
- **Metrics:** success rate, wall-clock time, token cost, node count, # recovery actions, # plan versions.
- **The model sweep:** hold everything constant, swap the LLM and re-run. Available now: **Claude (via `claude -p`)** and **Gemini (via `gemini -p`)**, called as subprocesses behind the Go `Provider` interface. Report how `G_graph` and `G_plan` shift between Claude and Gemini - does the weaker/cheaper model benefit more from graph structure? *That contrast is the interesting result.*
- **Token-cost caveat:** CLI wrappers may not return clean token counts. Primary efficiency metrics will be **wall-clock time** and **LLM call count** (always available); token cost is best-effort (parse CLI output if present, else estimate via a tokenizer). Adding raw API keys later yields exact token/cost numbers through the same `Provider` interface.
- **Rigor:** N runs per task per group (paper says 10) for variance; randomize task order; mock-tool mode for clean latency measurement, then validate on real tools.

---

## 7. API surface (draft - your strength, so it should be clean)

```
POST   /v1/plans            { task }            -> { plan_id, version, dag }     # plan only
POST   /v1/runs             { plan_id | task }  -> { run_id }                    # execute
GET    /v1/runs/{id}                            -> { state, ready_set, nodes[] } # live status
GET    /v1/runs/{id}/trace                      -> full WAL trace (audit)
POST   /v1/runs/{id}/cancel
WS     /v1/runs/{id}/stream                     -> live state-transition events
GET    /v1/eval/{job}/report                    -> gain-decomposition results
```

Design notes: runs are async (return immediately, poll or stream); every state transition is an event on the WAL and on the WebSocket; the trace endpoint is the "auditability" selling point made concrete.

---

## 8. Data model sketch (M0)

```
Plan      = (id, version, nodes[], edges[], output_contract)
Node      = (id, action_ref, join_mode in {all_of, any_of},
             side_effect_level in {none, read, write, destructive},
             retry_budget, timeout, output_contract)
Edge      = (from_node, to_node)
NodeState = pending|ready|running|waiting_human|blocked|
            executed|failed_retryable|failed|cancelled|skipped
WALEntry  = (run_id, seq, ts, node_id, from_state, to_state, trigger, payload)
```

Plan versions are immutable: a replan writes a NEW (id, version+1) row; nothing is mutated in place.

---

## 9. Testing strategy

- **Validator + FSM:** pure unit tests, including adversarial bad DAGs (cycles, dangling nodes, empty `any_of` candidate sets).
- **Scheduler:** property tests - "no node runs before all `all_of` predecessors are terminal-success"; "ready-set never un-readies a ready node under `all_of`" (Prop A.1).
- **Recovery:** fault-injection harness (mock nodes that fail transiently / structurally) asserting escalation order and budget bounds, plus a termination test (Theorem 6.2 - everything reaches terminal in bounded time).
- **Persistence:** kill-and-restart tests; trace replay must reproduce final state.
- **Eval:** golden-task regression so baseline numbers don't silently drift.

---

## 10. Risks and mitigations

| Risk | Mitigation |
|------|------------|
| Scope creep (this could become huge) | Hard MVP gate at M4 with mock nodes; everything past it is optional depth. |
| Planner quality dominates results | We *measure* `G_plan` separately (that's the whole point of the ablation); we don't need a great planner, just an honest one. |
| LLM cost during eval | Mock-tool mode + small task set first; cheap models (Haiku/local) for most runs; reserve expensive runs for the final table. |
| "Reinventing Airflow" critique | Lean into the LLM-specific parts (contract validation, 3-level recovery, non-idempotent retry) - that IS the novelty per the paper's own Table 4. |
| Time vs July 2026 grad / job hunt | M0-M4 + M7 (API) is enough for a strong resume bullet even if M8 slips. |

---

## 11. Decisions status

**Resolved (2026-06-30):** scope = full M0-M8; stack = Go core + Python eval; models = Claude Code CLI + Gemini CLI; repo = private for now.

**Remaining - to settle in the eng review (Gate 2):**
1. **Go concurrency model:** worker-pool size, incremental ready-set recomputation, and how `any_of` sibling-skip interacts with in-flight goroutines (cancel via `context`).
2. **WAL format + snapshot cadence:** binary vs JSON-lines log; snapshot every N transitions; exact replay/recovery semantics.
3. **Node executor boundary:** how Go execs the CLIs safely - timeouts, `context` cancellation, and sandboxing of tool nodes that touch the filesystem.
4. **Task set v1:** which 10-15 tasks, and how we encode ground-truth DAGs and success checks for each.
5. **Contract depth:** how much semantic validation vs purely syntactic JSON-Schema checks per node.

---

## 12. Next steps

1. ~~Gate 2: `/plan-eng-review`~~ DONE (2026-06-30) - architecture locked in section 0.5; section 11 remaining items D2-D6 resolved.
2. (Optional) outside-voice independent challenge of the plan.
3. Scaffold M0 (data model + contracts) following the locked decisions.

---

## GSTACK REVIEW REPORT

| Review | Trigger | Why | Runs | Status | Findings |
|--------|---------|-----|------|--------|----------|
| Eng Review | `/plan-eng-review` | Architecture & tests (required) | 1 | CLEAR (PLAN) | 6 decisions resolved (D1-D6), 0 blocking issues, 1 known non-critical failure gap |
| CEO Review | `/plan-ceo-review` | Scope & strategy | 0 | - | optional; personal research project |
| Design Review | `/plan-design-review` | UI/UX gaps | 0 | - | n/a (backend-only) |
| Outside Voice | codex (gpt-5.5) | Blind-spot challenge | 1 | issues_found | 16 findings (5 P0); 2 cross-model decisions adopted (CLI spike first; public core first); eval rigor baked in |

- **CROSS-MODEL:** Codex contradicted two locked decisions (sequencing, scope/visibility); both resolved in Codex's favor after user review. Strong consensus that the CLI-completion assumption is the project's biggest risk.
- **UNRESOLVED:** 0 decisions left open.
- **VERDICT:** ENG CLEARED + outside-voice incorporated. Plan reordered: **M0-spike (GO/NO-GO) is the first action**, then the v1 public credible core, then v2 deep systems.
