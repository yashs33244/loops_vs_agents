package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sgh/engine/node"
	"sgh/engine/plan"
	"sgh/engine/recovery"
	"sgh/engine/wal"
)

// tsExec is a thread-safe Executor for the CONCURRENT tests. node.MockExecutor
// records every call into an unguarded slice (node.go:102), so dispatching it
// from multiple worker goroutines at once trips the race detector - a property
// of the mock, not of the scheduler's own (mutex-free, single-writer) state.
// tsExec guards the recording with a mutex and delegates behavior to fn, so the
// concurrent tests exercise real parallelism while staying -race clean. The
// strictly sequential tests (linear chain, recovery) use node.MockExecutor
// directly, since their nodes never run concurrently.
type tsExec struct {
	fn    func(n plan.Node, inputs map[string]string) node.Result
	mu    sync.Mutex
	calls []string // node IDs, in completion order under the lock
}

func newTSExec(fn func(n plan.Node, inputs map[string]string) node.Result) *tsExec {
	return &tsExec{fn: fn}
}

func (e *tsExec) Execute(ctx context.Context, n plan.Node, inputs map[string]string) node.Result {
	if err := ctx.Err(); err != nil {
		return node.Result{Retryable: true, Err: err}
	}
	res := e.fn(n, inputs)
	e.mu.Lock()
	e.calls = append(e.calls, n.ID)
	e.mu.Unlock()
	return res
}

func (e *tsExec) callCount(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := 0
	for _, c2 := range e.calls {
		if c2 == id {
			c++
		}
	}
	return c
}

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

// linearPlan builds an N-node chain a0 -> a1 -> ... -> a{n-1}, all all_of. |U|
// stays 1 throughout (the degenerate, no-parallelism case from the paper).
func linearPlan(ids ...string) *plan.Plan {
	p := &plan.Plan{ID: "linear", Version: 1}
	for _, id := range ids {
		p.Nodes = append(p.Nodes, plan.Node{
			ID: id, ActionRef: id, Join: plan.JoinAllOf,
			SideEffect: plan.SideEffectNone, RetryBudget: 2,
		})
	}
	for i := 1; i < len(ids); i++ {
		p.Edges = append(p.Edges, plan.Edge{From: ids[i-1], To: ids[i]})
	}
	return p
}

// bugfixPlan is the paper's motivating 10-node bug-fix DAG (PAPER_EXPLAINED 5.x).
// Two parallel searches, two parallel reads, an all_of analyze, two parallel
// patch alternatives (fix_A, fix_B) plus parallel docs, then run_tests races the
// two patches via an any_of join, and a final report.
//
// NOTE on join placement: in this engine's model (and the validate package) the
// join mode lives on the JOINING node and applies to all its predecessors. So
// the any_of "try patch A or B, take whichever works" race is expressed by
// marking run_tests (which has predecessors {fix_A, fix_B}) as any_of - NOT by
// marking the candidates themselves. A candidate with a single predecessor must
// be all_of (the validator's join-consistency check 3 requires any_of nodes to
// have >= 2 predecessors). This is the validator-consistent form of the fixture
// in plan/types_test.go.
func bugfixPlan() *plan.Plan {
	n := func(id string, join plan.JoinMode, se plan.SideEffectLevel) plan.Node {
		return plan.Node{ID: id, ActionRef: id, Join: join, SideEffect: se, RetryBudget: 2, TimeoutMS: 60000}
	}
	return &plan.Plan{
		ID:      "bugfix-auth",
		Version: 1,
		Nodes: []plan.Node{
			n("search_auth", plan.JoinAllOf, plan.SideEffectRead),
			n("search_utils", plan.JoinAllOf, plan.SideEffectRead),
			n("read_auth", plan.JoinAllOf, plan.SideEffectRead),
			n("read_utils", plan.JoinAllOf, plan.SideEffectRead),
			n("analyze", plan.JoinAllOf, plan.SideEffectNone),
			n("fix_A", plan.JoinAllOf, plan.SideEffectWrite),
			n("fix_B", plan.JoinAllOf, plan.SideEffectWrite),
			n("update_docs", plan.JoinAllOf, plan.SideEffectWrite),
			n("run_tests", plan.JoinAnyOf, plan.SideEffectRead), // races fix_A vs fix_B
			n("report", plan.JoinAllOf, plan.SideEffectNone),
		},
		Edges: []plan.Edge{
			{From: "search_auth", To: "read_auth"},
			{From: "search_utils", To: "read_utils"},
			{From: "read_auth", To: "analyze"},
			{From: "read_utils", To: "analyze"},
			{From: "analyze", To: "fix_A"},
			{From: "analyze", To: "fix_B"},
			{From: "analyze", To: "update_docs"},
			{From: "fix_A", To: "run_tests"},
			{From: "fix_B", To: "run_tests"},
			{From: "run_tests", To: "report"},
			{From: "update_docs", To: "report"},
		},
	}
}

// stdPolicy is a generous-enough recovery policy for the happy-path tests.
func stdPolicy() recovery.Policy { return recovery.NewStandardPolicy(3, 1) }

// runWithTimeout runs the scheduler with a context backstop so a hang fails the
// test instead of blocking forever (the tests assert termination per Thm 6.2).
func runWithTimeout(t *testing.T, p *plan.Plan, exec node.Executor, log wal.Log, rec recovery.Policy, opts Options) (Outcome, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if opts.RunID == "" {
		opts.RunID = "run-" + t.Name()
	}
	out, err := Run(ctx, p, exec, log, rec, opts)
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("scheduler hung (deadline exceeded): %v", err)
	}
	return out, err
}

func allExecuted(t *testing.T, out Outcome, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if out.Final[id] != plan.StateExecuted {
			t.Errorf("node %q: want executed, got %q", id, out.Final[id])
		}
	}
}

// ---------------------------------------------------------------------------
// 1. linear chain
// ---------------------------------------------------------------------------

func TestLinearChain(t *testing.T) {
	ids := []string{"a", "b", "c", "d"}
	p := linearPlan(ids...)
	exec := node.NewMockExecutor(nil) // default echo => all succeed
	log := wal.NewMemLog()

	out, err := runWithTimeout(t, p, exec, log, stdPolicy(), Options{MaxParallel: 4})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.Succeeded {
		t.Fatalf("expected success, got %+v", out.Final)
	}
	allExecuted(t, out, ids...)

	// A linear chain has no parallelism: |U| never exceeds 1.
	if out.MaxReady != 1 {
		t.Errorf("linear chain MaxReady = %d, want 1", out.MaxReady)
	}

	// Dispatch order must respect the chain (each node runs after its pred).
	pos := map[string]int{}
	for i, c := range exec.Calls {
		if _, dup := pos[c.NodeID]; dup {
			t.Fatalf("node %q dispatched more than once: %+v", c.NodeID, exec.Calls)
		}
		pos[c.NodeID] = i
	}
	for i := 1; i < len(ids); i++ {
		if pos[ids[i-1]] >= pos[ids[i]] {
			t.Errorf("order violation: %q ran at %d, %q at %d", ids[i-1], pos[ids[i-1]], ids[i], pos[ids[i]])
		}
	}
}

// ---------------------------------------------------------------------------
// 2. bug-fix DAG: parallelism realized, all executed, few rounds
// ---------------------------------------------------------------------------

func TestBugfixDAG_Parallelism(t *testing.T) {
	p := bugfixPlan()
	// Concurrent dispatch -> use the thread-safe executor. Every node succeeds.
	exec := newTSExec(func(n plan.Node, inputs map[string]string) node.Result {
		return node.Result{Output: n.ID}
	})
	log := wal.NewMemLog()

	out, err := runWithTimeout(t, p, exec, log, stdPolicy(), Options{MaxParallel: 8})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.Succeeded {
		t.Fatalf("expected success, got %+v", out.Final)
	}

	// Every node reaches a terminal-success state. fix_A or fix_B may be skipped
	// (any_of), so assert terminal-success rather than executed for those.
	for id, st := range out.Final {
		if !st.IsTerminalSuccess() {
			t.Errorf("node %q ended %q, want terminal-success", id, st)
		}
	}

	// Parallelism is realized: peak ready-set >= 2 (e.g. the two searches, or
	// fix_A/fix_B/update_docs).
	if out.MaxReady < 2 {
		t.Errorf("MaxReady = %d, want >= 2 (parallelism not realized)", out.MaxReady)
	}

	// Exactly one of the any_of patch candidates is the winner; the loser is
	// skipped. Both being executed is allowed (the loser may finish before the
	// winner resolves), but at least one must be executed.
	if out.Final["fix_A"] != plan.StateExecuted && out.Final["fix_B"] != plan.StateExecuted {
		t.Errorf("neither fix_A nor fix_B executed: A=%q B=%q", out.Final["fix_A"], out.Final["fix_B"])
	}
}

// ---------------------------------------------------------------------------
// 3. any_of: the winning candidate executes, the loser is skipped
// ---------------------------------------------------------------------------

func TestAnyOf_LoserSkipped(t *testing.T) {
	p := bugfixPlan()
	log := wal.NewMemLog()

	// Make fix_A win deterministically: fix_B is slow, so by the time it would
	// return, fix_A has already executed and the loop has skipped fix_B (its
	// context is cancelled). The late fix_B event is ignored because fix_B is
	// already terminal (skipped). fix_A and fix_B are the any_of candidates for
	// run_tests, so run_tests still fires off fix_A's success.
	exec := newTSExec(func(n plan.Node, inputs map[string]string) node.Result {
		switch n.ID {
		case "fix_A":
			return node.Result{Output: "patched-by-A"}
		case "fix_B":
			time.Sleep(150 * time.Millisecond)
			return node.Result{Output: "patched-by-B"}
		default:
			return node.Result{Output: n.ID}
		}
	})

	out, err := runWithTimeout(t, p, exec, log, stdPolicy(), Options{MaxParallel: 8})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.Succeeded {
		t.Fatalf("expected success, got %+v", out.Final)
	}
	if out.Final["fix_A"] != plan.StateExecuted {
		t.Errorf("fix_A: want executed, got %q", out.Final["fix_A"])
	}
	if out.Final["fix_B"] != plan.StateSkipped {
		t.Errorf("fix_B: want skipped, got %q", out.Final["fix_B"])
	}
	// run_tests (any_of over fix_A/fix_B) and report must still complete.
	allExecuted(t, out, "run_tests", "report")
}

// ---------------------------------------------------------------------------
// 4. recovery: transient retry succeeds; structural failure terminates
// ---------------------------------------------------------------------------

func TestRecovery_TransientRetrySucceeds(t *testing.T) {
	ids := []string{"a", "b", "c"}
	p := linearPlan(ids...)
	log := wal.NewMemLog()

	// "b" fails transiently twice (N=2), then succeeds. Budget (MaxRetries=3) is
	// large enough, so b ends executed.
	exec := &node.MockExecutor{
		FailNTimes: map[string]int{"b": 2},
	}

	out, err := runWithTimeout(t, p, exec, log, recovery.NewStandardPolicy(3, 1), Options{MaxParallel: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.Succeeded {
		t.Fatalf("expected success after retries, got %+v", out.Final)
	}
	allExecuted(t, out, ids...)

	// b was dispatched 3 times: two failures + one success.
	bCalls := 0
	for _, c := range exec.Calls {
		if c.NodeID == "b" {
			bCalls++
		}
	}
	if bCalls != 3 {
		t.Errorf("node b dispatched %d times, want 3 (2 transient fails + 1 success)", bCalls)
	}
}

func TestRecovery_StructuralFailureTerminates(t *testing.T) {
	ids := []string{"a", "b", "c"}
	p := linearPlan(ids...)
	log := wal.NewMemLog()

	// "b" always fails STRUCTURALLY (not retryable). The policy skips level-1
	// retry, spends its single patch, requests a replan (no planner in v1 -> b is
	// failed), and the run terminates unsuccessfully. c never becomes ready.
	structuralErr := errors.New("contract violation: missing key")
	exec := node.NewMockExecutor(map[string]node.Result{
		"b": {Retryable: false, Err: structuralErr},
	})

	out, err := runWithTimeout(t, p, exec, log, recovery.NewStandardPolicy(3, 1), Options{MaxParallel: 2})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if out.Succeeded {
		t.Fatalf("expected failure, got success: %+v", out.Final)
	}
	if out.Final["a"] != plan.StateExecuted {
		t.Errorf("a: want executed, got %q", out.Final["a"])
	}
	if out.Final["b"] != plan.StateFailed {
		t.Errorf("b: want failed, got %q", out.Final["b"])
	}
	// c depends on b, which never succeeded: c never ran, stays pending.
	if out.Final["c"] == plan.StateExecuted {
		t.Errorf("c should not have executed (its dep failed), got %q", out.Final["c"])
	}

	// b was dispatched exactly twice: pristine attempt + one patch re-run, then
	// replan-requested -> failed (structural never gets a level-1 retry).
	bCalls := 0
	for _, c := range exec.Calls {
		if c.NodeID == "b" {
			bCalls++
		}
	}
	if bCalls != 2 {
		t.Errorf("node b dispatched %d times, want 2 (pristine + 1 patch)", bCalls)
	}
}

// TestRecovery_BudgetExhaustionTerminates proves Theorem 6.2: a node that always
// fails transiently still terminates (it cannot retry forever).
func TestRecovery_BudgetExhaustionTerminates(t *testing.T) {
	ids := []string{"a", "b"}
	p := linearPlan(ids...)
	log := wal.NewMemLog()

	// b fails transiently far more times than any budget allows.
	exec := &node.MockExecutor{FailNTimes: map[string]int{"b": 1000}}

	out, err := runWithTimeout(t, p, exec, log, recovery.NewStandardPolicy(2, 1), Options{MaxParallel: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Succeeded {
		t.Fatalf("expected failure, got success: %+v", out.Final)
	}
	if out.Final["b"] != plan.StateFailed {
		t.Errorf("b: want failed after budget exhaustion, got %q", out.Final["b"])
	}

	// b dispatched: 1 pristine + 2 retries + 1 patch = 4 attempts, then replan
	// -> failed. (No infinite loop.)
	bCalls := 0
	for _, c := range exec.Calls {
		if c.NodeID == "b" {
			bCalls++
		}
	}
	if bCalls != 4 {
		t.Errorf("node b dispatched %d times, want 4 (1 pristine + 2 retries + 1 patch)", bCalls)
	}
}

// ---------------------------------------------------------------------------
// 5. WAL: coherent transition trace; replay yields final states
// ---------------------------------------------------------------------------

func TestWAL_CoherentTrace(t *testing.T) {
	ids := []string{"a", "b", "c"}
	p := linearPlan(ids...)
	exec := node.NewMockExecutor(nil)
	log := wal.NewMemLog()

	const runID = "wal-run"
	out, err := runWithTimeout(t, p, exec, log, stdPolicy(), Options{MaxParallel: 4, RunID: runID})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries, err := log.Replay(runID)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("WAL is empty")
	}

	// Seq must be strictly monotonic and From must chain off the previous To for
	// each node (a coherent per-node transition trace).
	lastSeq := -1
	lastTo := map[string]plan.NodeState{}
	replayed := map[string]plan.NodeState{}
	for _, e := range entries {
		if e.RunID != runID {
			t.Errorf("entry has wrong RunID %q", e.RunID)
		}
		if e.Seq <= lastSeq {
			t.Errorf("Seq not monotonic: %d after %d", e.Seq, lastSeq)
		}
		lastSeq = e.Seq
		if prev, ok := lastTo[e.NodeID]; ok && prev != e.From {
			t.Errorf("node %q: From=%q does not chain off previous To=%q", e.NodeID, e.From, prev)
		}
		lastTo[e.NodeID] = e.To
		replayed[e.NodeID] = e.To // last write wins => final state
	}

	// Replaying the WAL must reproduce the Outcome's final states.
	for _, id := range ids {
		if replayed[id] != out.Final[id] {
			t.Errorf("replay mismatch for %q: WAL says %q, Outcome says %q", id, replayed[id], out.Final[id])
		}
		if replayed[id] != plan.StateExecuted {
			t.Errorf("node %q final WAL state = %q, want executed", id, replayed[id])
		}
	}

	// First transition for an entry node must be pending->ready (deps_met).
	first := entries[0]
	if first.From != plan.StatePending || first.To != plan.StateReady {
		t.Errorf("first WAL transition = %q->%q, want pending->ready", first.From, first.To)
	}
}

// TestWAL_RecoveryTrace checks the WAL records the retry path coherently.
func TestWAL_RecoveryTrace(t *testing.T) {
	p := linearPlan("a", "b")
	exec := &node.MockExecutor{FailNTimes: map[string]int{"b": 1}}
	log := wal.NewMemLog()
	const runID = "wal-recovery"

	if _, err := runWithTimeout(t, p, exec, log, recovery.NewStandardPolicy(3, 1), Options{RunID: runID}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	entries, _ := log.Replay(runID)

	// We expect to see b go: ...->running->failed_retryable->ready->running->executed.
	var sawRetry, sawExecuted bool
	for _, e := range entries {
		if e.NodeID != "b" {
			continue
		}
		if e.To == plan.StateFailedRetry {
			sawRetry = true
		}
		if e.To == plan.StateExecuted {
			sawExecuted = true
		}
	}
	if !sawRetry {
		t.Error("WAL missing b's failed_retryable transition")
	}
	if !sawExecuted {
		t.Error("WAL missing b's executed transition")
	}
}

// ---------------------------------------------------------------------------
// extra: side-effect safety (D4) and rate limiting
// ---------------------------------------------------------------------------

// TestDestructiveRunsAlone proves a destructive node never runs concurrently
// with any other node (decision D4).
func TestDestructiveRunsAlone(t *testing.T) {
	// Two independent entry nodes feed a destructive node and a plain node that
	// could in principle run alongside it.
	p := &plan.Plan{
		ID: "destructive", Version: 1,
		Nodes: []plan.Node{
			{ID: "seed", ActionRef: "seed", Join: plan.JoinAllOf, SideEffect: plan.SideEffectNone},
			{ID: "danger", ActionRef: "danger", Join: plan.JoinAllOf, SideEffect: plan.SideEffectDestructive},
			{ID: "other", ActionRef: "other", Join: plan.JoinAllOf, SideEffect: plan.SideEffectNone},
		},
		Edges: []plan.Edge{
			{From: "seed", To: "danger"},
			{From: "seed", To: "other"},
		},
	}
	log := wal.NewMemLog()

	// Track concurrency observed while a worker runs. The executor is invoked
	// from worker goroutines, so the counter uses sync/atomic to stay -race clean.
	violated := make(chan string, 4)
	var inFlight int32
	exec := newTSExec(func(n plan.Node, inputs map[string]string) node.Result {
		cur := atomic.AddInt32(&inFlight, 1)
		defer atomic.AddInt32(&inFlight, -1)
		if n.ID == "danger" && cur != 1 {
			violated <- "danger ran with others in flight"
		}
		time.Sleep(20 * time.Millisecond)
		return node.Result{Output: n.ID}
	})

	out, err := runWithTimeout(t, p, exec, log, stdPolicy(), Options{MaxParallel: 4})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	close(violated)
	if msg, ok := <-violated; ok {
		t.Fatalf("D4 violated: %s", msg)
	}
	allExecuted(t, out, "seed", "danger", "other")
}

// TestRateLimit confirms the token bucket throttles dispatch without hanging.
func TestRateLimit(t *testing.T) {
	// Five independent entry nodes so they are all ready at once and the token
	// bucket actually throttles concurrent dispatch.
	p := &plan.Plan{ID: "rate", Version: 1}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		p.Nodes = append(p.Nodes, plan.Node{ID: id, ActionRef: id, Join: plan.JoinAllOf, SideEffect: plan.SideEffectNone})
	}
	exec := newTSExec(func(n plan.Node, inputs map[string]string) node.Result {
		return node.Result{Output: n.ID}
	})
	log := wal.NewMemLog()

	start := time.Now()
	out, err := runWithTimeout(t, p, exec, log, stdPolicy(), Options{
		MaxParallel: 5,
		RatePerSec:  50, // 5 nodes at 50/s after an initial burst; just verify no hang
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.Succeeded {
		t.Fatalf("expected success, got %+v", out.Final)
	}
	if time.Since(start) > 4*time.Second {
		t.Errorf("rate-limited run took too long: %v", time.Since(start))
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

