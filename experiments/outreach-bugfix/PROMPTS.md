# Exact prompts used (both arms)

Both arms use Claude Code sub-agents, the same frozen bug set, the same worktree start,
and the same ground-truth command:
`<baseline>/.venv/bin/python -m pytest -q` (target: `20 passed`).

## Arm L - the loop (one agent, ReAct)

> You are the LOOP arm of a controlled experiment: a single ReAct-style agent fixing real bugs in a FastAPI backend.
>
> WORKING DIRECTORY (work ONLY inside it; never touch anything outside it): `.../arms/loop`
>
> The test suite currently has exactly 2 failing tests. The verification command, run FROM the working directory, is:
> `.../baseline/.venv/bin/python -m pytest -q`
>
> GOAL: make the ENTIRE suite pass (target: "20 passed"). Fix the real bugs in the application code under app/.
>
> HARD RULES: do NOT edit test files; do NOT edit files outside the working directory; do NOT run git; fix the actual application bugs (do not hack the tests); operate as a single iterative agent (inspect -> hypothesize -> edit -> run tests -> repeat).
>
> KEEP A STEP LOG ... return ONLY a strict minified JSON object {arm, success, final_pytest, num_steps, test_runs, files_changed, steps[], summary}.

## Arm G - the graph (planned DAG of sub-agents)

A Workflow orchestrates four sub-agents: 1 planner -> 2 parallel fixers -> 1 verifier
(with one bounded recovery round available, not needed here).

### Planner node (read-only)
> You are the PLANNER for the GRAPH arm. Working directory: `.../arms/graph`. First run the pytest command READ-ONLY to see the failing tests (do NOT edit). Inspect app/ and the failing tests. Produce a fix plan as a DAG: a list of INDEPENDENT fix nodes, each with id, title, primary file, bug description, and depends_on (usually []). Do NOT edit any file. Return JSON.

### Fix node (one per DAG node, run in parallel)
> You are FIX worker "{id}" in the GRAPH arm. Working directory: `.../arms/graph`. Fix ONLY this bug: "{title}" - {bug}. Primary file: {file}. Edit ONLY the application code under app/ for THIS bug. Do NOT edit tests; do NOT touch other bugs' files; do NOT run git. You MAY run only your single relevant test. Return JSON.

### Verify node (all_of join over the fix nodes)
> You are the VERIFY checkpoint for the GRAPH arm. From `.../arms/graph`, run the pytest command and report whether the WHOLE suite passes. Do NOT edit code. Return JSON {success, summary, failures}.

### Recovery node (bounded, only if verify fails)
> Recovery round. The suite is still failing: {summary}. Failures: {failures}. Fix the remaining application bug(s) under app/ (no test edits, no git). Return JSON.

## The DAG the planner produced (Arm G)

```
                 PLANNER (read-only)                 |U|=1
                 /                  \
   FIX chat-history-source      FIX finalize-session-arity     |U|=2 (parallel)
   app/api/v1/chat.py           app/api/v1/campaigns.py
                 \                  /
                 VERIFY (all_of join)                 |U|=1
                 pytest -> 20 passed
```
