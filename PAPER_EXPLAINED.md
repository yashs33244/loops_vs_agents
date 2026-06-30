# From Agent Loops to Structured Graphs - Explained

> Paper: *From Agent Loops to Structured Graphs: A Scheduler-Theoretic Framework for LLM Agent Execution*
> Author: Hu Wei | arXiv:2604.11378v1 | 13 April 2026 | 50 pages
> PDF: [`paper/2604.11378-structured-graphs.pdf`](paper/2604.11378-structured-graphs.pdf)

This is the "graphs are better than loops for agents" paper. This doc explains it in plain English, with analogies to backend systems you already know (Airflow, schedulers, write-ahead logs, state machines, Raft). Read this first, then skim the PDF for the formal definitions.

---

## 0. TL;DR (read this if nothing else)

Today most AI agents run as a **loop**: the LLM looks at everything it has done so far, decides the single next action, does it, looks again, repeats. This is the ReAct pattern (think -> act -> observe -> repeat).

This paper argues that loop is structurally weak for serious "engineering" tasks, and proposes replacing it with a **static graph (a DAG)** that is planned upfront and then executed by a deterministic scheduler, exactly like Airflow runs a DAG of tasks. The twist is that the nodes are LLM calls (non-deterministic, can hallucinate, retries are not safe to repeat blindly), so the author adds three things classical schedulers do not have:

1. **Contract validation** - check each node's output against a spec before marking it done.
2. **A 3-level recovery protocol** - retry, then patch, then full replan, in that strict order, with budgets.
3. **An immutable plan** - the graph cannot be edited mid-run; changing it means minting a new plan version.

**Crucial fact for you:** this is a *position paper*. There is **no code**. The author even estimates a minimal implementation at 3,300 to 6,500 lines and lists exactly what to build. That gap is your project.

---

## 1. The core idea in one sentence

> An agent loop and a graph executor are both **schedulers**; the only real differences are (a) how many things can run at once and (b) whether a human can predict and audit the scheduling decisions - and for verifiable engineering tasks, the graph wins on both.

---

## 2. Background: what is an "Agent Loop"?

An agent loop is the standard way agents work today (ReAct, AutoGPT, most coding agents). One LLM holds a growing context window and, every turn, decides the next single step.

Example the paper uses: *"Fix a Python auth bug and update the docs."* A loop does it in **11 sequential turns**:

```
Turn 1: decide to search auth files   -> search_code("auth bug")
Turn 2: decide to search utils too     -> search_code("utils")     [sequential, not parallel]
Turn 3: read auth file                  -> read_file("auth.py")
Turn 4: read utils file                 -> read_file("utils.py")
Turn 5: analyze both                     -> analyze(...)            [implicit dep on turns 3-4]
Turn 6: write a fix                      -> write_fix("patch A")
Turn 7: run tests                        -> FAIL: 2 tests failing
Turn 8: try a different fix              -> write_fix("patch B")    [ad-hoc, no protocol]
Turn 9: run tests again                  -> OK
Turn 10: update docs
Turn 11: generate report
```

It works, but notice: the two searches *could* have run in parallel and didn't; the docs update *could* have happened alongside the fix and didn't; and when patch A failed, the decision to "try something else" was an unstructured whim of the LLM.

---

## 3. The 3 structural weaknesses of loops

This is the heart of the paper's complaint:

| # | Weakness | What it means | Backend analogy |
|---|----------|---------------|-----------------|
| 1 | **Implicit dependencies** | "Analyze depends on the two reads" exists only as a memory in the context window. No structural guard stops out-of-order execution. | Like a build with no Makefile - order lives in the operator's head, not the system. |
| 2 | **Unbounded recovery** | When a step fails, the LLM freely decides retry / skip / replan, with no limit. Can loop forever or give up too early. | A retry with no max-attempts and no backoff policy. |
| 3 | **Mutable history** | The plan can be silently rewritten mid-run. Afterwards you cannot reconstruct which plan actually governed which action. | Editing prod state with no audit log / no event sourcing. |

For exploratory or creative tasks these are fine (you *want* flexibility). For **verifiable engineering tasks** - clear dependencies, checkable success - they are liabilities.

---

## 4. The key reframe: everything is a scheduler

This is the paper's main intellectual move, and it is genuinely clean.

Model any agent execution system as a tuple `E = (S, U, P, O, Delta)`:

- **S** = the set of node states (each unit of work and its status)
- **U(s)** = the **ready set**: which units are eligible to run *right now* (all their dependencies are done)
- **P** = the **scheduling policy**: picks which ready unit(s) to dispatch
- **O** = outcome space: `{success, failure, retry, escalate}`
- **Delta** = transition function: update state after an outcome, recompute the ready set

Now you can place every system on two axes:

1. **Ready-set cardinality `|U|`** - how many units can run simultaneously.
   - Agent Loop: `|U| <= 1` ("single-ready-unit"). Only one thing ever runs.
   - Graph executor: `|U| >= 1` ("multi-ready-unit"). Independent nodes run in parallel.

2. **Policy explicitness** - is the scheduling decision a deterministic, inspectable function, or an opaque LLM guess?
   - Agent Loop: the "policy" is just the LLM's next-token output. Non-deterministic, not inspectable.
   - Graph Harness: deterministic topological scheduling. Same state -> same decision, every time.

### The scheduler continuum

The paper lines systems up left to right (Figure 2):

```
Naive Loop  ->  Parallel Loop  ->  Planner Loop  ->  Structured Loop  ->  GRAPH HARNESS
|U|=1            |U|>=1               |U|=1              |U|=1                  |U|>=1
non-det         non-det              non-det            semi-det               DETERMINISTIC
```

Key insight that trips people up: **adding a planner to a loop does NOT move you right on the cardinality axis.** A "plan-then-execute" loop still runs one step at a time (`|U|=1`). Only a real graph executor reaches the multi-ready-unit, deterministic corner. That corner is Graph Harness.

---

## 5. The proposal: Graph Harness (SGH)

"Lift the control structure out of the implicit context and into an explicit static DAG."

Three hard commitments define it:

1. **The plan is an immutable DAG** for the life of a plan version.
2. **Planning, execution, and recovery are three separate layers** with clean interfaces.
3. **Recovery follows a strict escalation protocol** (no skipping levels).

These deliberately trade away some flexibility (it explicitly drops "try-all-take-first" parallelism, recursive sub-graphs, and dynamic topology) in exchange for **controllability, verifiability, and auditability**.

### The same bug-fix task in SGH (6 rounds instead of 11 turns)

```
Round 1  ready={search_auth, search_utils}    |U|=2  -> both run in PARALLEL
Round 2  ready={read_auth, read_utils}         |U|=2  -> both in PARALLEL
Round 3  ready={analyze}                        |U|=1  (waits for BOTH reads - enforced structurally)
Round 4  ready={fix_A, fix_B, update_docs}      |U|=3  -> all three in PARALLEL
            fix_A FAILED (transient) -> level-1 retry... but
            fix_B succeeded first -> fix_A is SKIPPED (any_of join: "try either patch")
Round 5  ready={run_tests}                      |U|=1
Round 6  ready={report}                         |U|=1
```

Same task, but parallelism is now a **structural property of the graph**, not a lucky LLM decision. And "try patch A or B, whichever works" is a first-class graph construct (`any_of`), not an ad-hoc reaction to failure.

---

## 6. The four design principles

Derived from a survey of 70 open-source agent projects (60% of which use plain loops).

| Principle | Rule | Sacrifices | Gains |
|-----------|------|-----------|-------|
| **1. Controllability first** | Prefer predictable/verifiable execution over expressive flexibility | "try-all-take-first" parallelism, recursive expansion, rollback | Predictable execution, verifiable traces |
| **2. Stable execution commitment** | Once validated, the plan structure cannot change mid-run | Dynamic plan editing | Auditable plan history, reliable failure attribution |
| **3. Bounded recovery** | Recovery has explicit triggers, bounded scope, strict escalation | Ad-hoc LLM-driven recovery | Deterministic escalation, no infinite failure loops |
| **4. Side-effect classification** | Classify nodes by how dangerous they are (a read vs a DB write) and schedule accordingly | Unrestricted parallel dispatch | Safety - never speculatively run a destructive op |

---

## 7. The formal model (the part you will actually implement)

This is where the paper becomes an engineering spec. Five components:

### 7.1 The execution plan (Definition 5.1)
`Plan = (id, version, V, E, sigma, kappa)` where:
- `V` = nodes, `E` = directed edges (dependencies)
- `sigma` = per-node config (the action, retry policy, side-effect level)
- `kappa` = output contract (what the plan must produce)

**Plan invariant (Def 5.2):** for the life of version `v`, `(V, E)` is immutable. The only way to change structure is to mint version `v+1` via the replan protocol. This is basically **event-sourcing / immutable deploys for agent plans** - you never mutate, you version.

### 7.2 Three-layer separation (Figure 3)
```
Planner Layer   -> produces a validated DAG (can be an LLM, a template, or hybrid)
      | plan
      v
Runtime Layer   -> executes the DAG, never modifies it; maintains node state, computes ready set
      | failure report
      v
Recovery Layer  -> diagnoses failures, picks a recovery action; sends it back to runtime
                   (and can request a full replan from the planner)
```

Plus **context separation (Def 5.3)**: two disjoint contexts.
- `C_exec` (execution context): inputs, artifacts, runtime state - visible to nodes while running.
- `C_diag` (diagnostic context): failure history, prior plan versions - visible ONLY to recovery/planner.

The rule `C_exec ∩ C_diag = empty` prevents a subtle bug: failure history leaking into execution and corrupting later reasoning. (Think: don't let your error logs become accidental input to business logic.)

### 7.3 Node state machine (Definition 6.1, Figure 4)
Each node walks a finite state machine:

```
states = {pending, ready, running, waiting_human, blocked,
          executed, failed_retryable, failed, cancelled, skipped}
terminal = {executed, failed, cancelled, skipped}   (absorbing - once here, never leave)
```

Key transitions: `running -> failed_retryable` on transient errors (timeout, rate limit - worth retrying); `running -> failed` on structural errors (missing dependency, invalid plan - retrying won't help). The full transition table is Appendix A.1 (Table 15) - basically your implementation checklist.

The paper proves two things about this FSM:
- **Theorem 6.2 (Termination):** with finite timeouts and retry budgets, every node reaches a terminal state in bounded time, with probability 1. No hangs.
- **Theorem 6.3 (Conditional soundness):** even when every node "passes," overall correctness is only as good as the *validators*. `Pr[all correct] >= product of p_v` where `p_v` is each validator's reliability. This is the "validation gap" - LLM-based validation has `p_v < 1`, so it bounds your trust. Honest and important.

### 7.4 Three-level recovery protocol (Definition 6.3, Table 10)

| Level | Action | Trigger | Scope |
|-------|--------|---------|-------|
| 1 | `local_retry` | transient error (network, timeout) | this node only, plan unchanged |
| 2 | `local_patch` | contract violation, auth error | reconfigure this node, plan unchanged |
| 3 | `request_replan` | missing dependency, invalid plan structure | mint a whole new plan version |

**Escalation invariant (Prop 6.4):** you must exhaust level `i` before level `i+1`. Enforced mechanically by a per-node counter `recovery_state in {pristine, retried, patched}`: `attempt_patch` is rejected unless state >= retried; `request_replan` is rejected unless all failed nodes are >= patched. This is what kills the "infinite replan" pathology. (Very much like a circuit breaker with staged escalation.)

### 7.5 Join semantics (Section 7)
How a node decides its predecessors are "done enough" to start:
- **`all_of`** (Def 7.1): wait for *every* predecessor to succeed. Normal dependency join.
- **`any_of`** (Def 7.2): dispatch all candidates; the first to succeed satisfies the join; remaining siblings are **skipped**. This is "try patch A and B, take whichever works."
- **`first_of` is deliberately EXCLUDED** (Def 7.3): true speculative "race them and cancel the losers" is left out because cancelling mid-flight LLM work needs compensation/rollback protocols (what if you already sent an email?). Only ~8% of tasks truly need it. Workaround: run all with `all_of`, then a downstream node picks the best.

---

## 8. How they propose to prove it works (the experiment design)

The paper does **not** run experiments - it designs them (Section 8). The clever part is the **seven-group ablation**, where each group adds exactly one feature:

```
G0 SOTA Loop      (strong prompted loop, the real-world baseline)
G1 Naive Loop
G2 Planner Loop        (G2 - G1 = "planning gain")
G3 Structured Loop     (G3 - G2 = "scaffold gain")
G4 GH-Core             (G4 - G3 = "graph gain"  <- the headline number)
G5 GH + Patch          (G5 - G4 = "patch gain")
G6 GH + Replan         (G6 - G5 = "replan gain")
```

So the total improvement decomposes additively:
`G_total = G_plan + G_scaffold + G_graph + G_patch + G_replan`

This lets you answer "is the benefit from the *graph structure* or just from *better planning*?" - which is the question everyone hand-waves. Controlled variables: same 50 tasks (stratified simple/medium/complex), same model, same tools, same timeout, same token budget, 10 runs each for variance.

**This experiment design is a gift to you** - it is a ready-made evaluation plan, and "test it with different data models" maps directly onto swapping the LLM in this rig and re-measuring the gains.

---

## 9. What the paper does NOT do = your opportunity

The author is unusually explicit about this. Quoting the structure of Section 9.10 and Limitation 6:

> "engineering details (concurrent scheduling, distributed logging, fault-tolerant persistence) ... are the subject of ongoing work."

Their own estimate of a minimal build (Limitation 6):

| Component | Est. LOC | What it is |
|-----------|---------:|------------|
| DAG validation engine | 1,000-2,000 | cycle detection, reachability, join consistency (Appendix A.2) |
| Concurrent scheduler with rate limiting | 800-1,500 | topological scheduling, incremental ready-set updates, token-bucket |
| State persistence layer | 500-1,000 | write-ahead log + periodic snapshots for crash recovery |
| Recovery engine | 600-1,200 | level 1/2/3 escalation, budget enforcement |
| Contract validation framework | 400-800 | JSON-schema validation, pass/fail semantics |
| **Total** | **~3,300-6,500** | (excluding tests, docs, LLM integration) |

They also name the distributed-systems pieces for a real deployment (Section 9.10): incremental ready-set recomputation (`O(|E_new|)` instead of `O(|V|+|E|)` per cycle), token-bucket rate limiting for LLM quotas, append-only WAL with snapshots (cites Raft/Ongaro), heartbeat-based crash detection, idempotent nodes via unique request IDs, and Raft/Paxos for leader election in multi-worker setups.

**Read that list again.** That is a backend engineer's project spec. None of it is ML research; it is scheduling, persistence, and fault-tolerance - your strengths - with LLM calls as the "non-deterministic task" wrinkle.

---

## 10. When SGH is the WRONG tool (so you scope honestly)

The paper is careful here (Sections 9.7, 10):
- **Bad for exploratory tasks** ("research this and write a survey") - you cannot draw the DAG upfront.
- **Bad for dynamic-goal tasks** ("investigate the outage and fix whatever's broken") - the plan depends on what you find.
- **Bad for creative tasks** ("write a story, then revise") - structure emerges from the content.
- **Degenerates on linear tasks:** if the planner produces a straight chain (no parallelism), `|U|` stays 1 and SGH loses most of its advantage. Its value is bounded by **planner quality** - a bad planner = a bad DAG.
- **Fixed-overhead hypothesis (H4):** on trivial 1-3 step tasks, SGH's bookkeeping overhead may cost more than it saves. Motivates a "dual-path" design: simple tasks -> lightweight loop, complex tasks -> full graph.

---

## 11. Mental map to things you already know

| Paper concept | You already know this as |
|---------------|--------------------------|
| Static DAG, topological scheduling | Airflow / Luigi / Prefect DAGs |
| Immutable plan versions | Immutable deploys, event sourcing |
| Write-ahead log + snapshots | Postgres WAL, Raft log |
| Ready-set, incremental recompute | Dirty-tracking / dependency invalidation |
| Token-bucket rate limiting | API gateway rate limiter |
| Three-level escalation + budgets | Circuit breaker + staged retry policy |
| Node state machine | Any workflow/job FSM |
| Heartbeats + idempotency keys | Distributed job queue (Sidekiq/Temporal-style) |
| `any_of` join | "first writer wins" / quorum-of-1 |
| Contract validation | Schema validation at an API boundary |

The honest summary: **Graph Harness is Airflow for non-deterministic LLM nodes, with a formal recovery protocol bolted on.** If you have ever reasoned about a job scheduler, you already understand 70% of this paper.

---

## 12. Glossary of symbols

| Symbol | Meaning |
|--------|---------|
| `E = (S, U, P, O, Delta)` | execution system tuple |
| `U(s)`, `|U(s)|` | ready set at state `s`, and its size (cardinality) |
| `P` | scheduling policy (function if deterministic, relation if not) |
| `Plan = (id, version, V, E, sigma, kappa)` | the execution plan |
| `sigma: V -> NodeConfig` | per-node configuration |
| `kappa`, `kappa_v` | plan-level / per-node output contract |
| `Sigma`, `Sigma_term` | node state set / terminal states |
| `R` | recovery action set (level 1/2/3) |
| `C_exec`, `C_diag` | execution context vs diagnostic context |
| `G_plan, G_scaffold, G_graph, G_patch, G_replan` | the five attributable gains |

---

## 13. Bottom line

- The "graphs over loops" claim is **real and well-argued**, but it is a **design proposal with zero implementation and zero empirical results**. The author says so repeatedly and even hands you the build spec and the experiment plan.
- It is squarely a **backend / distributed-systems project** wearing an AI hat. The LLM is just the non-deterministic task runner; the meat is scheduling, persistence, recovery, and contracts.
- "Test with different data models" fits perfectly: build the engine, run the seven-group ablation, and swap LLMs to see how `G_graph` and `G_plan` move.

When you have read this, come back and we will resume the grilling (Gate 1), then the engineering review (Gate 2), and only then write code.
