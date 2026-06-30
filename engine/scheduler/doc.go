// Package scheduler is the single-writer event loop that executes a Plan
// (decision D2). Exactly one goroutine owns all node state: worker goroutines
// run nodes and send completion events on a channel; the loop applies each
// event, recomputes the ready-set U, writes a WAL entry, dispatches newly-ready
// nodes (up to MaxParallel, throttled by a hand-rolled token bucket), and
// handles any_of sibling cancellation via context. No mutexes guard node state;
// tests run with -race.
//
// On failure it consults a recovery.Policy for the bounded 3-level escalation,
// and it runs until every node reaches a terminal state.
package scheduler
