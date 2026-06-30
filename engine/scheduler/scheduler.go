package scheduler

import (
	"context"
	"errors"

	"sgh/engine/node"
	"sgh/engine/plan"
	"sgh/engine/recovery"
	"sgh/engine/wal"
)

// errNotImplemented is the placeholder error returned by this skeleton.
var errNotImplemented = errors.New("not implemented")

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

// Run executes p with the single-writer event loop (decision D2): it dispatches
// newly-ready nodes via exec (throttled per opts), applies completion events,
// logs each transition to log, and consults rec on failure for bounded 3-level
// recovery, until every node is terminal.
//
// STUB: not implemented yet.
func Run(ctx context.Context, p *plan.Plan, exec node.Executor, log wal.Log, rec recovery.Policy, opts Options) (Outcome, error) {
	return Outcome{}, errNotImplemented
}
