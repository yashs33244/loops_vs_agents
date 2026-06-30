// Package recovery implements the bounded 3-level failure-recovery protocol the
// scheduler invokes when a node fails:
//
//	local_retry -> local_patch -> request_replan
//
// Escalation is strict (a level is only tried after the previous one is
// exhausted) and budgeted, which guarantees termination. The package also
// classifies errors as retryable (transient: timeout/ratelimit) or structural.
package recovery
