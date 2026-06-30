# Cost levers on the graph: naive vs optimized (gemini-3.1-flash-lite)

**Question:** the pilot showed the graph costs ~3x the tokens. Can the transformer-informed levers
(context scoping, deterministic verify instead of an LLM verify node) cut that - and at what cost to
correctness? See [`../../docs/SCHEDULING_FIRST_PRINCIPLES.md`](../../docs/SCHEDULING_FIRST_PRINCIPLES.md)
for where the levers come from.

**Setup:** fix the 3 single-file hardening items (5 tests) on the real `outreach-proj` code with
`gemini-3.1-flash-lite`, two ways. Correctness judged by `pytest` (objective). Exact token counts from
the API. Each fix node rewrites its file.

- **Naive graph:** every fix node is sent the WHOLE repo context (all 3 files + the test file + an
  overview); plus a final LLM "verify" node reviews the changes. (Mirrors the pilot: agents re-read everything.)
- **Optimized graph:** each fix node gets ONLY its file + its own failing tests + a one-line cheatsheet
  (aggressive context scoping). Verify is `pytest` (free) - no LLM verify node.

## Result: cost down 66%, but correctness fell off a cliff

| Metric | Naive | Optimized | Delta |
|--------|------:|----------:|------:|
| Total tokens | 22,616 | 7,643 | **-66%** |
| Input tokens | 18,935 | 4,137 | -78% |
| LLM calls | 4 | 3 | -1 (dropped the LLM verify) |
| Wall-clock | 33.5s | 25.4s | -24% |
| **Hardening tests fixed** | **5 / 5** | **1 / 5** | **broke correctness** |

![naive vs optimized](plots/gemini_levers.png)

## What it means (the honest reading)

**The cost levers work, but "scope harder" is the wrong mental model.** Two separate lessons:

1. **Deterministic verify is a free, safe win.** Replacing the LLM "verify" node with `pytest` removed a
   whole call (and ~3.5k tokens) with zero downside - verification is exactly what code checks are for.
   This lever should always be on.

2. **Context scoping has a correctness cliff.** Cutting each node down to just-its-file (input tokens
   -78%) made the model cheap but wrong: it had to rewrite a whole file it could only partly see, so it
   dropped or altered things outside its window and regressed the suite (only 1 of 5 net fixed). The
   naive arm "won" on correctness precisely because it over-fed every node the full context.

So the real optimization is **minimal *sufficient* context, not minimal context** - plus a safer edit
shape. Two concrete fixes for the next arm:

- **Targeted edits, not whole-file rewrites.** Ask for a small patch/anchored replacement, so the model
  can't silently drop the parts it wasn't shown.
- **Minimal-sufficient scoping.** File + its tests + the specific cross-file signatures it needs (e.g.
  the repository method, the response shape) - which is more than "just the file" but far less than "the
  whole repo."

This maps back to the OS lens: you want the node's **working set** resident (cache locality), not the
*entire* address space and not *too little*. The sweet spot sits between the two arms here.

## Next

Add a **balanced arm (G'')**: minimal-sufficient context + targeted edits + deterministic verify, and
show it can land 5/5 while staying well below the naive 22.6k tokens. Then carry the two robust,
provider-agnostic levers (deterministic verify, targeted edits) into the realistic Claude experiment.

Artifacts: `gemini_levers.py`, `runs/gemini_levers.json`, `plots/gemini_levers.{png,svg}`, `test_hardening.py` (the task).
