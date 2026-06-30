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
// It encodes the paper's escalation invariant (Prop 6.4): you must exhaust
// level i before climbing to level i+1, and you never replan before patches
// are spent nor fail before a replan was attempted. The per-node counters live
// in the Attempt the scheduler passes in (Retries/Patches/Replanned), mirroring
// the paper's recovery_state in {pristine, retried, patched}. The budgets here
// (MaxRetries, MaxPatches) plus the single replan are what make Theorem 6.2
// (Termination) hold: every node reaches a terminal state in bounded steps.
type StandardPolicy struct {
	// MaxRetries caps local_retry attempts (overridden per-node by RetryBudget).
	MaxRetries int
	// MaxPatches caps local_patch attempts before escalating to replan.
	MaxPatches int
}

// NewStandardPolicy builds a StandardPolicy with the given level-1 (retry) and
// level-2 (patch) budgets. Negative budgets are clamped to zero so a caller
// can never accidentally create an infinitely-retrying policy.
func NewStandardPolicy(maxRetries, maxPatches int) *StandardPolicy {
	if maxRetries < 0 {
		maxRetries = 0
	}
	if maxPatches < 0 {
		maxPatches = 0
	}
	return &StandardPolicy{MaxRetries: maxRetries, MaxPatches: maxPatches}
}

// Classify reports whether r is a transient (retryable) failure. Transient
// failures (timeout, rate limit) are worth re-running unchanged at level 1;
// structural failures (contract violation, missing dependency) are not, so the
// scheduler/Decide will skip level 1 for them. The classification is exactly
// node.Result.Retryable, which the executor sets when producing the result.
func (p *StandardPolicy) Classify(r node.Result) (retryable bool) {
	return r.Retryable
}

// Decide returns the next recovery action for a failed node, enforcing strict,
// budgeted escalation (Prop 6.4). The ladder is climbed in order and never
// skips a rung except that a structural (non-retryable) failure has no useful
// level-1 retry, so it enters at level 2 (patch):
//
//	level 1 local_retry  -> only while transient AND Retries < MaxRetries
//	level 2 local_patch  -> while Patches < MaxPatches  (reconfigure + retry)
//	level 3 request_replan -> once, if not already Replanned
//	terminal fail        -> budgets spent and a replan was already attempted
//
// Invariants this guarantees:
//   - We never emit ActionReplan while patch budget remains (level 2 before 3).
//   - We never emit ActionFail unless a replan was already attempted.
//   - Each branch strictly increases the work spent on the node, so repeated
//     calls with the updated Attempt always reach ActionFail (termination).
func (p *StandardPolicy) Decide(att Attempt, r node.Result) Decision {
	// Level 1: local_retry. Only transient failures are retried unchanged, and
	// only while the retry budget remains. Structural failures fall straight
	// through to level 2 because re-running them unchanged cannot help.
	if p.Classify(r) && att.Retries < p.MaxRetries {
		return Decision{
			Action: ActionRetry,
			Level:  LevelLocalRetry,
			Reason: "transient: retry",
		}
	}

	// Level 2: local_patch. Reconfigure the node (corrective patch/prompt) and
	// re-dispatch it. Represented as ActionRetry at LevelLocalPatch so the
	// scheduler re-runs the node, but at the patched level.
	if att.Patches < p.MaxPatches {
		return Decision{
			Action: ActionRetry,
			Level:  LevelLocalPatch,
			Reason: "patch + retry",
		}
	}

	// Level 3: request_replan. Retry and patch budgets are exhausted; ask the
	// planner for a new plan version. Allowed exactly once per node.
	if !att.Replanned {
		return Decision{
			Action: ActionReplan,
			Level:  LevelReplan,
			Reason: "replan",
		}
	}

	// Terminal: every level has been spent (retries + patches exhausted and a
	// replan was already attempted). Stop escalating and fail the node.
	return Decision{
		Action: ActionFail,
		Level:  LevelReplan,
		Reason: "recovery exhausted",
	}
}

// Compile-time check that StandardPolicy satisfies the interface.
var _ Policy = (*StandardPolicy)(nil)
