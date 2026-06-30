# Experiment: Loop vs Graph on real bugs (outreach-proj)

A controlled head-to-head testing the paper's hypothesis (graph beats loop for verifiable
engineering tasks) on **real bugs** in a **real project** - no mocks.

## Hypothesis
For a bug-fix task with clear, partly-independent sub-tasks, running the work as a planned
**graph of steps** (parallel where independent, single-threaded at checkpoints) beats running
it as a single **loop**, on: wall-clock time, wasted/redundant steps, and reliability of the fix.

## Arms (same model = Claude, same bugs, same tools, same ground-truth, same budget)
- **Arm L - Loop:** one strong Claude agent (ReAct). Given the bug list + the ground-truth
  command, it fixes bugs one step at a time, deciding each next move itself.
- **Arm G - Graph:** Claude orchestrated as a planned DAG of sub-agents. A planner emits a
  fix-DAG; independent fix-nodes run as parallel sub-agents; analysis/verify nodes are
  single-cardinality (wait for predecessors); recovery is bounded (retry -> re-dispatch).

> Note: Arm G's executor is **Claude's workflow/sub-agent orchestration**, not (yet) our Go
> SGH engine (which is at M0). This tests the paper's *idea* on real agents + real code. The
> Go engine is a separate, later systems experiment. Stated up front so the result is honest.

## Setup (original project never touched, nothing committed)
1. Copy the target module of `outreach-proj` into `experiments/outreach-bugfix/baseline/`.
2. `git init` the copy + one baseline commit (reference point; enables worktrees + diffs).
3. Two worktrees from the baseline: `arms/loop/` (branch `arm-loop`), `arms/graph/`
   (branch `arm-graph`) - identical starting state.
4. Agents work in their worktree; their fixes are **left uncommitted** (captured as diffs).

## Ground truth (objective, no external services)
- TARGET MODULE: _pending recon_
- VERIFICATION COMMAND: _pending recon_ (e.g. `tsc --noEmit`, `npm run build`, `npm test`)
- BASELINE FAILURES: _recorded before any fix; both arms start from the same failing state._
- Success = the verification command goes from failing -> passing, with no new failures.

## Fairness controls (from the eng review + outside-model challenge)
- The loop arm is a **genuinely strong** loop (good prompt, same tools), not a strawman.
- Both arms get the **same** frozen bug set, tools, model, ground-truth command, and budget.
- We report what was held constant and what differed. v1 is an honest **pilot** (small N),
  not a claim to have reproduced the paper's full study.

## Metrics (per arm)
| Metric | How |
|--------|-----|
| ground-truth result | verification command pass/fail + failures fixed / introduced |
| wall-clock | start..finish |
| steps / turns | from the step log |
| parallelism realized | max concurrent nodes (Arm G); always 1 for Arm L |
| tokens | where the runtime reports them |
| recovery actions | retries / re-dispatches / replans |
| diff size | files changed, +/- lines |

## Monitoring
A monitor records every step/node of both arms (timestamp, actor, action, target file,
outcome, retry) into `runs/`. That log is the raw material for the diagrams and plots.

## Artifacts
- `runs/` - per-arm structured step logs + final diffs + ground-truth output.
- `docs/diagrams/*.svg` - publication-grade: the loop structure, the actual DAG, the
  agent/sub-agent layout, and the exact prompts, for each arm.
- `plots/*` - high-quality loop-vs-graph comparison charts.
- `REPORT.md` - the writeup tying results back to the paper's hypothesis.

## Status
- [x] decisions locked (Claude-orchestrated graph now; small controlled pilot)
- [ ] recon (in progress) -> fills in TARGET MODULE, VERIFICATION COMMAND, bug set
- [ ] sandbox + worktrees
- [ ] run Arm L / Arm G with monitor
- [ ] metrics -> diagrams -> plots -> report
