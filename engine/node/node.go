package node

import (
	"context"
	"errors"

	"sgh/engine/contract"
	"sgh/engine/plan"
	"sgh/engine/provider"
)

// errNotImplemented is the placeholder error returned by skeleton methods.
var errNotImplemented = errors.New("not implemented")

// Result is the outcome of executing one node.
type Result struct {
	Output    string // raw node output (JSON for LLM nodes)
	Tokens    int
	Retryable bool // true => transient (timeout/ratelimit); false => structural
	Err       error
}

// Executor runs one node given its already-satisfied dependency outputs.
type Executor interface {
	// Execute runs one node given its already-satisfied dependency outputs.
	Execute(ctx context.Context, n plan.Node, inputs map[string]string) Result
}

// MockExecutor is a deterministic, scriptable executor for tests, including
// injected failures.
//
// STUB: scripting fields/logic are filled in by the implementer.
type MockExecutor struct{}

// Execute returns a scripted result.
//
// STUB: not implemented yet.
func (m *MockExecutor) Execute(ctx context.Context, n plan.Node, inputs map[string]string) Result {
	return Result{Err: errNotImplemented}
}

// LLMExecutor builds a prompt from the node and its inputs, calls a Provider,
// and validates the output against the node's Contract before reporting
// success.
//
// STUB: prompt-building/validation logic is filled in by the implementer.
type LLMExecutor struct {
	// Provider is the LLM backend used to execute the node.
	Provider provider.Provider
}

// Execute runs the node via the provider and checks its contract.
//
// STUB: not implemented yet. (contract is imported so implementers wire
// contract.Validate here.)
func (l *LLMExecutor) Execute(ctx context.Context, n plan.Node, inputs map[string]string) Result {
	_ = contract.Validate // referenced so the dependency is explicit
	return Result{Err: errNotImplemented}
}

// ToolExecutor performs jailed file/command operations, gated by the node's
// side-effect level (decision D4).
//
// STUB: jail/config fields are filled in by the implementer.
type ToolExecutor struct{}

// Execute runs the node's tool action within its side-effect jail.
//
// STUB: not implemented yet.
func (t *ToolExecutor) Execute(ctx context.Context, n plan.Node, inputs map[string]string) Result {
	return Result{Err: errNotImplemented}
}

// Compile-time checks that each executor satisfies the interface.
var (
	_ Executor = (*MockExecutor)(nil)
	_ Executor = (*LLMExecutor)(nil)
	_ Executor = (*ToolExecutor)(nil)
)
