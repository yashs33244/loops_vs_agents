package recovery

import "sgh/engine/node"

// Level is a rung in the strict 3-level escalation ladder. The scheduler tries
// the lowest applicable level first and only climbs when the current level's
// budget is exhausted, which is what guarantees termination.
type Level string

const (
	// LevelLocalRetry re-runs the same node unchanged (transient failures).
	LevelLocalRetry Level = "local_retry"
	// LevelLocalPatch re-runs the node with a corrective patch/prompt.
	LevelLocalPatch Level = "local_patch"
	// LevelReplan gives up locally and requests a new plan.
	LevelReplan Level = "request_replan"
)

// Action is the decision the scheduler must carry out for a failed node.
type Action string

const (
	// ActionRetry => re-dispatch the node (possibly with a patch).
	ActionRetry Action = "retry"
	// ActionReplan => escalate to a replan of the surrounding subgraph.
	ActionReplan Action = "replan"
	// ActionFail => stop escalating; mark the node terminally failed.
	ActionFail Action = "fail"
)

// Attempt is the per-node recovery bookkeeping the policy reasons over: how many
// times each level has already been spent on this node.
type Attempt struct {
	NodeID    string
	Retries   int // local_retry attempts already made
	Patches   int // local_patch attempts already made
	Replanned bool
}

// Decision is what the policy tells the scheduler to do next for a failed node.
type Decision struct {
	Action Action
	Level  Level  // the level chosen for this decision (informational)
	Reason string // human-readable explanation for the WAL/trace
}

// Policy is the recovery strategy the scheduler consults whenever a node fails.
// Decide inspects the node's failure Result and its prior Attempt history and
// returns the next Action, enforcing strict, budgeted escalation.
type Policy interface {
	// Classify reports whether a result is a transient (retryable) failure or a
	// structural one; the scheduler also forwards node.Result.Retryable here.
	Classify(r node.Result) (retryable bool)
	// Decide returns the next recovery action for a failed node.
	Decide(att Attempt, r node.Result) Decision
}

// StandardPolicy is the default strict, budgeted 3-level escalation policy.
//
// STUB: budget fields/logic are filled in by the implementer.
type StandardPolicy struct {
	// MaxRetries caps local_retry attempts (overridden per-node by RetryBudget).
	MaxRetries int
	// MaxPatches caps local_patch attempts before escalating to replan.
	MaxPatches int
}

// Classify reports whether r is a transient failure.
//
// STUB: not implemented yet.
func (p *StandardPolicy) Classify(r node.Result) (retryable bool) {
	panic("not implemented: recovery.StandardPolicy.Classify")
}

// Decide returns the next recovery action.
//
// STUB: not implemented yet.
func (p *StandardPolicy) Decide(att Attempt, r node.Result) Decision {
	panic("not implemented: recovery.StandardPolicy.Decide")
}

// Compile-time check that StandardPolicy satisfies the interface.
var _ Policy = (*StandardPolicy)(nil)
