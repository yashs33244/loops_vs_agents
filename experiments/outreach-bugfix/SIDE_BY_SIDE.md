# Side by side: loop vs graph, and cheap-model vs strong-model

Three experiments on the **same real codebase** (`outreach-proj`), each judged by `pytest` (objective).
This is the answer to "the graph looked too expensive - can we do better, and when is it worth it?"

## The three experiments

1. **Pilot** (tiny: 2 bugs, [`REPORT.md`](REPORT.md)) - Claude loop vs graph.
2. **Hardening task** (bigger: 7 failures across 4-5 files, [`TASK_HARDENING.md`](TASK_HARDENING.md)) - Claude loop vs graph (this doc).
3. **Cost levers** (same hardening task, cheap model, [`GEMINI_LEVERS_REPORT.md`](GEMINI_LEVERS_REPORT.md)) - gemini-3.1-flash-lite, 3 ways.

## Result 1: on a big-enough task, the graph's parallelism finally pays off

Claude, hardening task, both arms reached **25/25**:

| Metric | Loop (1 agent) | Graph (3 parallel agents) | Verdict |
|--------|---------------:|--------------------------:|---------|
| Correctness | 25/25 | 25/25 | tie |
| Tokens | 48,364 | 103,453 | loop (graph **2.1x**) |
| Wall-clock | 152.6s | **116.8s** | **graph (-23%)** |
| (graph if run serially) | - | 245.2s | - |
| Tool calls | 22 | 29 | loop |
| Diff size | 46 lines | 53 lines | ~tie |

![Claude loop vs graph](plots/claude_loop_vs_graph.png)

**The crossover.** In the tiny pilot the graph was slower AND pricier - overhead with nothing to amortize.
On this bigger task the graph is still pricier (2.1x tokens, the orchestration tax of each node re-reading
its own context) but now **finishes faster** (117s vs 153s), because three nodes ran at once and the
slowest (117s) beat the loop's serial 153s. That is exactly the prediction from the pilot: the graph wins
on wall-clock once a task has enough independent work. The token cost never goes away; the time cost flips.

## Result 2: the token cost CAN be cut, but only if the model is capable enough

Same hardening task, cheap model (gemini-3.1-flash-lite), 3 ways of running the graph:

| Arm | Tokens | Correct |
|-----|-------:|--------:|
| full context + whole-file rewrite | 22,442 | **5/5** |
| scoped context, whole-file rewrite | 7,655 (-66%) | 1/5 |
| scoped context, targeted edits | 4,955 (-78%) | 0/5 |

With a weak model, every attempt to cut cost broke correctness; only the expensive full-context arm was
reliable. (A diagnostic showed the cheap approaches work *in isolation* - they are high-variance, not
broken.) The lesson: **context is what buys correctness for a weak model**, so the cost-cutting levers are
gated by capability.

## The synthesis (what this means for the project)

- **Graph vs loop is a trade, and the trade changes with task size.** Small task: loop wins on both.
  Big task: graph costs more tokens but wins wall-clock. The right design is **dual-path** - route trivial
  tasks to a loop, parallel-heavy tasks to the graph.
- **The graph's token overhead is the orchestration tax** (nodes re-reading context), not model
  inefficiency - which is why context engineering is the lever, and why it is risky.
- **Cutting that cost is capability-gated.** Claude stays 25/25 on this task; flash-lite cannot cut context
  without breaking. "Use a strong model, harnessed well" is literally measurable here.
- **One lever is free and safe at every scale: deterministic verify** (run `pytest`, don't ask an LLM).

## Honest caveats

- N = 1 per arm; LLM output is non-deterministic (run-to-run variance is real - see the Gemini diagnostic).
- The graph's "planner" step was provided, not separately measured, so the graph's token cost here is
  execution-only (it would be a bit higher with a planner call) - this slightly favors the graph.
- Sub-agent wall-clock includes scheduling/queue noise. These are directional signals, not a benchmark.

## Artifacts
- `runs/claude_arms.json`, `plots/claude_loop_vs_graph.{png,svg}`, `plot_claude.py`
- `runs/gemini_levers.json`, `plots/gemini_levers.{png,svg}`, `gemini_levers.py`
- `runs/metrics.json` + `plots/loop_vs_graph.{png,svg}` (the pilot)
