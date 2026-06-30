# LinkedIn post

**Carousel images (in order):** `card1_hook.png` → `card2_crossover.png` → `card3_parallel.png` → `card4_cost.png` → `card5_takeaways.png` (1500x1500, square).

**Links to set first:**
- Google Doc paper: https://docs.google.com/document/d/1ee5HSN4o8sQHyAmq7TmG6cXpMw7EOprR9C70LzxkFWU/edit?usp=sharing  (set sharing to "Anyone with the link -> Viewer")
- Code: https://github.com/yashs33244/loops_vs_agents

---

## Post text

I read a 2026 paper arguing that LLM agents should run as **scheduled graphs**, not reason-act **loops**. Sharp claim. Zero code, zero experiments. So I built it and tested it.

**The engine** (Go, standard-library-only, ~2.6k LOC): an immutable plan DAG, a single-writer scheduler that stays clean under Go's race detector, all-of/any-of joins, output-contract validation, and a bounded recovery protocol. It runs the paper's 10-node bug-fix DAG end-to-end, on a mock backend and on a live model.

**Then I tested the claim** on real backend code, graded by an objective test suite (no LLM judging another LLM):

- Small task: the loop wins. The graph's overhead isn't worth it.
- Larger task: the graph finishes **23% faster** by running independent fixes in parallel (a measured **2.1x** speedup), at a **2-3x token cost**.
- And a trap: on a small model, trimming context to cut that cost dropped correctness from **5/5 to 0/5**. Cheap is capability-gated.

Takeaway: graph-vs-loop is a task-size decision, not a religion, and a deterministic verifier is the one always-safe lever.

Write-up + all the code and run data in the comments.

#LLM #AIengineering #Golang #SoftwareEngineering #Agents

---

## First comment (put links here, not in the post body, for better reach)

Paper (Google Doc): <gdrive link>
Code + run data: github.com/yashs33244/loops_vs_agents
