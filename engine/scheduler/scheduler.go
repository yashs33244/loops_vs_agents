package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"sgh/engine/node"
	"sgh/engine/plan"
	"sgh/engine/recovery"
	"sgh/engine/validate"
	"sgh/engine/wal"
)

// Options configures one scheduler run.
type Options struct {
	MaxParallel int     // worker pool cap
	RatePerSec  float64 // token-bucket throttle for provider calls (0 = unlimited)
	RunID       string
}

// Outcome is the terminal report of a scheduler run.
type Outcome struct {
	Final     map[string]plan.NodeState // node id -> terminal state
	Rounds    int                       // scheduling rounds
	MaxReady  int                       // peak |U| observed (the parallelism number)
	Succeeded bool                      // plan-level contract satisfied
}

// event is the message a worker goroutine sends back to the single-writer loop
// when it finishes running a node (decision D2). It is the ONLY way node
// outcomes re-enter the loop, so the loop alone ever mutates node state.
type event struct {
	nodeID string
	result node.Result
}

// nodeRuntime is the loop-private mutable state for one node. Only the loop
// goroutine ever reads or writes these fields, so no mutex is needed (D2).
type nodeRuntime struct {
	n       plan.Node
	state   plan.NodeState
	attempt recovery.Attempt
	cancel  context.CancelFunc // set while running; used for any_of sibling cancellation
	output  string             // last successful output, fed to successors as input
}

// Run executes p with the single-writer event loop (decision D2): it dispatches
// newly-ready nodes via exec (throttled per opts), applies completion events,
// logs each transition to log, and consults rec on failure for bounded 3-level
// recovery, until every node is terminal (Theorem 6.2).
//
// Design (single-writer loop):
//   - ONE goroutine (this function's body) owns every nodeRuntime: its
//     plan.NodeState and its recovery.Attempt counters. No other goroutine
//     touches them, so node state needs no mutex and the loop is race-free.
//   - Worker goroutines only call exec.Execute and send an `event` back on a
//     channel. The loop applies the event, writes the WAL transition, recomputes
//     the ready-set, and dispatches newly-ready nodes.
//   - Concurrency is bounded by opts.MaxParallel; provider dispatch is throttled
//     by a hand-rolled token bucket when opts.RatePerSec > 0.
func Run(ctx context.Context, p *plan.Plan, exec node.Executor, log wal.Log, rec recovery.Policy, opts Options) (Outcome, error) {
	if p == nil {
		return Outcome{}, fmt.Errorf("scheduler: nil plan")
	}
	// Validate field-level and structural well-formedness before running so we
	// never schedule a cyclic or malformed graph (the loop assumes a DAG).
	if err := p.BasicValidate(); err != nil {
		return Outcome{}, fmt.Errorf("scheduler: invalid plan: %w", err)
	}
	if probs := validate.Check(p); len(probs) > 0 {
		return Outcome{}, fmt.Errorf("scheduler: plan failed validation: %v", probs[0])
	}

	maxParallel := opts.MaxParallel
	if maxParallel <= 0 {
		maxParallel = len(p.Nodes) // sane default: as wide as the graph could ever be
	}

	// ---- loop-private state (owned solely by this goroutine) ----------------

	rt := make(map[string]*nodeRuntime, len(p.Nodes))
	for _, n := range p.Nodes {
		rt[n.ID] = &nodeRuntime{
			n:       n,
			state:   plan.StatePending,
			attempt: recovery.Attempt{NodeID: n.ID},
		}
	}

	// Predecessor / successor sets from p.Edges (paper E). Used for readiness.
	preds := make(map[string][]string, len(p.Nodes))
	succs := make(map[string][]string, len(p.Nodes))
	for _, n := range p.Nodes {
		preds[n.ID] = nil
		succs[n.ID] = nil
	}
	for _, e := range p.Edges {
		preds[e.To] = append(preds[e.To], e.From)
		succs[e.From] = append(succs[e.From], e.To)
	}

	// WAL sequence is monotonic across the whole run. transition() is the single
	// chokepoint for every state change: it mutates the runtime state and appends
	// exactly one WAL entry, so the log is a faithful, replayable transition trace.
	var seq int
	transition := func(id string, to plan.NodeState, trigger string, payload json.RawMessage) {
		n := rt[id]
		from := n.state
		n.state = to
		e := wal.Entry{
			RunID:   opts.RunID,
			NodeID:  id,
			Trigger: trigger,
			Seq:     seq,
			TS:      time.Now().UTC().Format(time.RFC3339Nano),
			From:    from,
			To:      to,
			Payload: payload,
		}
		seq++
		// MemLog/FileLog Append never returns a meaningful error in practice; we
		// keep going on error rather than aborting a run mid-flight (the WAL is a
		// trace, not a gate). The single-writer loop is the only Append caller.
		_ = log.Append(e)
	}

	// Entry nodes (no predecessors) start ready (paper: U_0).
	for _, n := range p.Nodes {
		if len(preds[n.ID]) == 0 {
			transition(n.ID, plan.StateReady, "deps_met", nil)
		}
	}

	// ---- readiness helpers --------------------------------------------------

	// depsSatisfied reports whether node id's join condition is met, given the
	// current terminal states of its predecessors (paper Def 7.1 / 7.2).
	depsSatisfied := func(id string) bool {
		ps := preds[id]
		if len(ps) == 0 {
			return true // entry node
		}
		switch rt[id].n.Join {
		case plan.JoinAnyOf:
			// any_of: ready as soon as ANY predecessor is `executed`.
			for _, p := range ps {
				if rt[p].state == plan.StateExecuted {
					return true
				}
			}
			return false
		default: // all_of
			// all_of: ready when ALL predecessors are terminal-success
			// (executed or skipped).
			for _, p := range ps {
				if !rt[p].state.IsTerminalSuccess() {
					return false
				}
			}
			return true
		}
	}

	// inputsFor gathers the successful outputs of node id's predecessors, keyed by
	// predecessor id, to pass to the executor (paper C_exec inputs).
	inputsFor := func(id string) map[string]string {
		ps := preds[id]
		if len(ps) == 0 {
			return nil
		}
		in := make(map[string]string, len(ps))
		for _, p := range ps {
			if rt[p].state == plan.StateExecuted {
				in[p] = rt[p].output
			}
		}
		return in
	}

	// promotePending re-scans pending nodes and promotes any whose deps are now
	// satisfied to `ready`. Called after every terminal transition.
	promotePending := func() {
		for _, n := range p.Nodes {
			if rt[n.ID].state == plan.StatePending && depsSatisfied(n.ID) {
				transition(n.ID, plan.StateReady, "deps_met", nil)
			}
		}
	}

	// skipLosers handles any_of resolution (paper Def 7.2): when winner becomes
	// `executed`, every other not-yet-terminal predecessor of a shared any_of
	// joiner is skipped (and cancelled if running). We skip a predecessor q only
	// if EVERY any_of joiner it feeds has already been satisfied by a sibling, so
	// we never skip work another live join still needs.
	skipLosers := func(winner string) {
		for _, joiner := range succs[winner] {
			if rt[joiner].n.Join != plan.JoinAnyOf {
				continue
			}
			// This joiner is now satisfied by `winner`. Skip its other candidates.
			for _, cand := range preds[joiner] {
				if cand == winner {
					continue
				}
				q := rt[cand]
				if q.state.IsTerminal() {
					continue
				}
				// Only skip cand if it is not still needed by some OTHER join that
				// is not yet satisfied.
				if stillNeeded(cand, succs, preds, rt) {
					continue
				}
				if q.cancel != nil {
					q.cancel() // cancel in-flight context if running
				}
				transition(cand, plan.StateSkipped, "skip", nil)
			}
		}
	}

	// ---- worker plumbing ----------------------------------------------------

	events := make(chan event)
	var inFlight int // number of running workers (owned by the loop)
	var bucket *tokenBucket
	if opts.RatePerSec > 0 {
		bucket = newTokenBucket(opts.RatePerSec, maxParallel)
		defer bucket.stop()
	}

	// dispatch starts a worker for node id. The loop sets state->running and the
	// per-node cancel func BEFORE launching, so any_of cancellation can reach an
	// in-flight node. The worker only reads immutable inputs and sends an event.
	dispatch := func(id string) {
		n := rt[id]
		wctx, cancel := context.WithCancel(ctx)
		if n.n.TimeoutMS > 0 {
			wctx, cancel = context.WithTimeout(ctx, time.Duration(n.n.TimeoutMS)*time.Millisecond)
		}
		n.cancel = cancel
		inputs := inputsFor(id)
		nodeCopy := n.n // value copy: worker never touches loop state
		transition(id, plan.StateRunning, "dispatch", nil)
		inFlight++
		go func() {
			if bucket != nil {
				bucket.take(wctx)
			}
			res := exec.Execute(wctx, nodeCopy, inputs)
			select {
			case events <- event{nodeID: id, result: res}:
			case <-ctx.Done():
			}
		}()
	}

	// schedule dispatches as many ready nodes as the parallelism rules allow.
	// Decision D4: a destructive node must run ALONE - it is only dispatched when
	// nothing is in flight, and while it runs nothing else is dispatched.
	schedule := func() {
		// If a destructive node is currently running, hold everything.
		for _, n := range p.Nodes {
			if rt[n.ID].state == plan.StateRunning && rt[n.ID].n.SideEffect == plan.SideEffectDestructive {
				return
			}
		}
		for _, n := range p.Nodes {
			if inFlight >= maxParallel {
				return
			}
			id := n.ID
			if rt[id].state != plan.StateReady {
				continue
			}
			if rt[id].n.SideEffect == plan.SideEffectDestructive {
				// Destructive node: only dispatch alone. Wait until the in-flight
				// set is empty, then dispatch it and stop (others wait their turn).
				if inFlight == 0 {
					dispatch(id)
					return
				}
				continue
			}
			dispatch(id)
		}
	}

	// readyCount returns |U| - the current ready-set size (the parallelism metric).
	readyCount := func() int {
		c := 0
		for _, n := range p.Nodes {
			if rt[n.ID].state == plan.StateReady {
				c++
			}
		}
		return c
	}

	allTerminal := func() bool {
		for _, n := range p.Nodes {
			if !rt[n.ID].state.IsTerminal() {
				return false
			}
		}
		return true
	}

	// ---- the event loop -----------------------------------------------------

	var rounds, maxReady int

	// A "round" is one scheduling iteration: observe |U|, dispatch, then block on
	// the next event. We seed the metric with the initial ready-set.
	observe := func() {
		if c := readyCount(); c > maxReady {
			maxReady = c
		}
	}

	observe()
	rounds++
	schedule()

	for !allTerminal() {
		// If nothing is in flight and nothing is ready, but not all nodes are
		// terminal, the run is stuck (e.g. a failed node starved its successors).
		// Drain by marking the remaining reachable-but-unrunnable nodes blocked is
		// out of scope for v1; instead we simply stop - the failure already ended
		// the run unsuccessfully and there is nothing left to wait on.
		if inFlight == 0 {
			if readyCount() == 0 {
				break // no work in flight and nothing ready: terminal stall
			}
			// Ready nodes exist but none could be dispatched (e.g. a destructive
			// node waiting, or rate-limit). Try again.
			schedule()
			if inFlight == 0 {
				break
			}
		}

		select {
		case <-ctx.Done():
			// Context cancelled: stop dispatching, drain in-flight workers, return.
			drainAndFinalize(events, &inFlight, rt, transition)
			return finalize(p, rt, rounds, maxReady), ctx.Err()

		case ev := <-events:
			inFlight--
			n := rt[ev.nodeID]
			if n.cancel != nil {
				n.cancel()
				n.cancel = nil
			}

			// A node may have been skipped (any_of loser) while its worker was
			// in flight; if it is already terminal, ignore the late event.
			if n.state.IsTerminal() {
				rounds++
				observe()
				schedule()
				continue
			}

			applyResult(ev, n, rec, transition)

			// After applying the outcome, recompute readiness and any_of skips.
			if n.state == plan.StateExecuted {
				n.output = ev.result.Output
				skipLosers(ev.nodeID)
			}
			promotePending()

			rounds++
			observe()
			schedule()
		}
	}

	return finalize(p, rt, rounds, maxReady), nil
}

// applyResult turns one completion event into the right state transition for the
// node, consulting the recovery policy on failure. It is called only by the loop
// goroutine (single writer), so it freely mutates n's state and attempt counters.
func applyResult(ev event, n *nodeRuntime, rec recovery.Policy, transition func(id string, to plan.NodeState, trigger string, payload json.RawMessage)) {
	r := ev.result
	if r.Err == nil {
		// Success: node is executed.
		transition(ev.nodeID, plan.StateExecuted, "success", nil)
		return
	}

	// Failure. First mark the intermediate failure state for the trace
	// (retryable vs structural), then consult the policy for the next action.
	if rec.Classify(r) {
		transition(ev.nodeID, plan.StateFailedRetry, "fail_retryable", errPayload(r.Err))
	} else {
		transition(ev.nodeID, plan.StateFailedRetry, "fail_structural", errPayload(r.Err))
	}

	dec := rec.Decide(n.attempt, r)
	switch dec.Action {
	case recovery.ActionRetry:
		switch dec.Level {
		case recovery.LevelLocalPatch:
			// Level 2: local_patch. A real patch would tweak the node config; for
			// v1 we just re-run the node unchanged. Increment the patch counter so
			// the policy escalates correctly on the next failure.
			n.attempt.Patches++
			transition(ev.nodeID, plan.StateReady, "patch", reasonPayload(dec.Reason))
		default:
			// Level 1: local_retry. Re-run the node unchanged.
			n.attempt.Retries++
			transition(ev.nodeID, plan.StateReady, "retry", reasonPayload(dec.Reason))
		}

	case recovery.ActionReplan:
		// v1 LIMITATION: there is no planner. A replan request cannot mint a new
		// plan version, so we record the request in the WAL and fail the node,
		// letting the run end unsuccessfully. (Wiring a planner is a v2 upgrade.)
		n.attempt.Replanned = true
		transition(ev.nodeID, plan.StateFailed, "replan_requested", reasonPayload(dec.Reason))

	case recovery.ActionFail:
		transition(ev.nodeID, plan.StateFailed, "fail", reasonPayload(dec.Reason))

	default:
		transition(ev.nodeID, plan.StateFailed, "fail", reasonPayload("unknown recovery action"))
	}
}

// stillNeeded reports whether candidate cand must keep running because some
// any_of joiner it feeds is not yet satisfied by a sibling (so skipping cand
// would starve that join). Used to make any_of skip conservative.
func stillNeeded(cand string, succs, preds map[string][]string, rt map[string]*nodeRuntime) bool {
	for _, joiner := range succs[cand] {
		if rt[joiner].n.Join != plan.JoinAnyOf {
			// An all_of joiner needs cand to succeed; do not skip it on behalf of
			// some other any_of join.
			if !rt[joiner].state.IsTerminal() {
				return true
			}
			continue
		}
		// any_of joiner: it is satisfied if any predecessor already executed.
		satisfied := false
		for _, p := range preds[joiner] {
			if rt[p].state == plan.StateExecuted {
				satisfied = true
				break
			}
		}
		if !satisfied {
			return true
		}
	}
	return false
}

// drainAndFinalize is called on context cancellation: it stops dispatching,
// waits for all in-flight workers to report (their contexts are cancelled, so
// they return promptly), and records a cancelled transition for any node that
// was still running. The loop goroutine remains the only writer.
func drainAndFinalize(events <-chan event, inFlight *int, rt map[string]*nodeRuntime, transition func(id string, to plan.NodeState, trigger string, payload json.RawMessage)) {
	for *inFlight > 0 {
		ev := <-events
		*inFlight--
		n := rt[ev.nodeID]
		if n.cancel != nil {
			n.cancel()
			n.cancel = nil
		}
		if !n.state.IsTerminal() {
			transition(ev.nodeID, plan.StateCancelled, "cancelled", nil)
		}
	}
}

// finalize builds the Outcome from the final runtime state. Succeeded iff every
// node is terminal and none ended in `failed` (every node executed or skipped).
func finalize(p *plan.Plan, rt map[string]*nodeRuntime, rounds, maxReady int) Outcome {
	final := make(map[string]plan.NodeState, len(p.Nodes))
	succeeded := true
	for _, n := range p.Nodes {
		st := rt[n.ID].state
		final[n.ID] = st
		if !st.IsTerminal() {
			succeeded = false
		}
		if st == plan.StateFailed || st == plan.StateCancelled {
			succeeded = false
		}
	}
	return Outcome{
		Final:     final,
		Rounds:    rounds,
		MaxReady:  maxReady,
		Succeeded: succeeded,
	}
}

// errPayload encodes an error as a small JSON payload for the WAL trace.
func errPayload(err error) json.RawMessage {
	if err == nil {
		return nil
	}
	return reasonPayload(err.Error())
}

// reasonPayload wraps a human-readable reason in a JSON object for the WAL.
func reasonPayload(reason string) json.RawMessage {
	b, err := json.Marshal(struct {
		Reason string `json:"reason"`
	}{Reason: reason})
	if err != nil {
		return nil
	}
	return b
}
