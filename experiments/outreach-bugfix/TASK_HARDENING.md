# The task: a "decent" production hardening + bugfix PR

The pilot used a tiny 2-bug task, which is the regime where the graph *should* lose (overhead can't
amortize). This is the larger, realistic task for the loop-vs-graph-vs-optimized comparison: a
**production hardening + bugfix pass** on the real `outreach-proj` FastAPI backend.

## Definition (objective ground truth)

Starting from `baseline/` (a copy of the real backend), make the **whole** test suite green:

```
baseline:  7 failed, 18 passed
goal:      0 failed, 25 passed   (and no regressions)
```

Run with: `pytest -q` (in-memory SQLite, mock LLM - no Postgres, no real model needed).

## The seven failures, by independent fix target

The task is deliberately **fan-out friendly**: the fixes live in four different files, so a graph can
run one fix node per file in parallel without merge conflicts.

| # | Requirement | File to change | Failing test(s) |
|---|-------------|----------------|-----------------|
| 1 | **Bugfix:** `finalize_session()` is called with the wrong arity | `app/services/chat_service.py` (+ caller) | `test_finalize_session_accepts_params` |
| 2 | **Bugfix:** chat history is missing its initial assistant message | `app/services/chat_service.py` / model | `test_get_chat_history_has_initial_message` |
| 3 | **Hardening:** validate chat messages (non-empty, max length) -> 422 | `app/schemas/chat.py` | `test_chat_rejects_empty_message`, `test_chat_rejects_oversized_message` |
| 4 | **Security:** POST chat to a campaign you don't own must be 404, not a 200 stream (IDOR) | `app/api/v1/chat.py` | `test_chat_on_unknown_campaign_returns_404` |
| 5 | **Hardening:** paginate `GET /campaigns` (`limit` / `offset`) | `app/api/v1/campaigns.py` | `test_list_campaigns_respects_limit`, `test_list_campaigns_respects_offset` |

Fix targets 1-2 share a file (one node); 3, 4, 5 are separate files. So the natural graph has
**4 parallel fix nodes** joined by a single deterministic verify (`pytest`).

The hardening tests live in `baseline/tests/test_hardening.py` (the `baseline/` tree itself is
gitignored - it is a copy of a separate project; only this spec and the experiment artifacts are tracked).

## Why this task is fair

- **Bigger than the pilot** (4 independent work items, not 1), so the graph's parallelism has something
  to amortize against - this is where the crossover should start to appear.
- **Objective**: pass/fail is `pytest`, not a judgement call.
- **Mixed**: 2 plain bugs + 3 hardening items, like a real PR.
- **Includes a file with two coupled fixes** (chat_service) so it is not a pure embarrassingly-parallel
  setup - some nodes are heavier than others.
