package node

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"sgh/engine/contract"
	"sgh/engine/plan"
	"sgh/engine/provider"
)

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

// ---------------------------------------------------------------------------
// MockExecutor
// ---------------------------------------------------------------------------

// MockExecutor is a deterministic, scriptable executor for tests. It never
// performs I/O: the scheduler's tests use it to drive ready-set, join, and
// recovery logic without a real provider or tool backend.
//
// Three scripting hooks, in priority order:
//
//   - Fn:      a func(plan.Node, inputs) Result gives full control per call.
//   - Results: a map keyed by node ID; a node's canned Result is returned.
//   - FailNTimes: a map keyed by node ID; the node fails (retryably, by
//     default) the first N calls, then succeeds. This drives recovery and
//     scheduler tests through transient failures into eventual success.
//
// When more than one hook applies, Fn wins, then FailNTimes (while it still
// has failures owed), then Results, then a default echo. Calls records every
// (node, inputs) pair seen so tests can assert on dispatch order/count.
type MockExecutor struct {
	// Fn, when non-nil, is invoked for every Execute and its result returned
	// verbatim. The most flexible hook.
	Fn func(n plan.Node, inputs map[string]string) Result

	// Results maps node ID -> canned Result, returned once FailNTimes (if any)
	// is exhausted for that node.
	Results map[string]Result

	// FailNTimes maps node ID -> number of leading calls that should fail
	// before the node succeeds. Each call to Execute decrements the remaining
	// count for that node. The injected failure is retryable so recovery's
	// local_retry path can drive the node to success.
	FailNTimes map[string]int

	// FailResult is the Result returned while a node still owes failures under
	// FailNTimes. When zero-valued, a default retryable error is used.
	FailResult Result

	// failCounts tracks how many times each node has been called, so
	// FailNTimes can be honoured without mutating the caller's map.
	failCounts map[string]int

	// Calls records every (node ID, inputs) pair seen, in order.
	Calls []MockCall
}

// MockCall is one recorded invocation of MockExecutor.Execute.
type MockCall struct {
	NodeID string
	Inputs map[string]string
}

// NewMockExecutor returns a MockExecutor that replays the given canned results,
// keyed by node ID. A node missing from the map gets a default echo result.
func NewMockExecutor(results map[string]Result) *MockExecutor {
	return &MockExecutor{Results: results}
}

// NewMockExecutorFunc returns a MockExecutor driven entirely by fn.
func NewMockExecutorFunc(fn func(n plan.Node, inputs map[string]string) Result) *MockExecutor {
	return &MockExecutor{Fn: fn}
}

// defaultFailResult is the retryable failure injected by FailNTimes when no
// explicit FailResult is configured.
var defaultFailResult = Result{
	Retryable: true,
	Err:       errors.New("mock: injected transient failure"),
}

// Execute returns a scripted result. It records the call, then resolves the
// outcome via Fn, FailNTimes, Results, or a default echo (in that order).
func (m *MockExecutor) Execute(ctx context.Context, n plan.Node, inputs map[string]string) Result {
	m.Calls = append(m.Calls, MockCall{NodeID: n.ID, Inputs: inputs})

	// Honour a cancelled context deterministically: a node that was cancelled
	// out from under the executor reports a retryable context error, matching
	// what a real provider would surface.
	if err := ctx.Err(); err != nil {
		return Result{Retryable: true, Err: err}
	}

	if m.Fn != nil {
		return m.Fn(n, inputs)
	}

	// Fail-then-succeed: while this node still owes failures, return the
	// (retryable) failure and consume one.
	if owed, ok := m.FailNTimes[n.ID]; ok && owed > 0 {
		if m.failCounts == nil {
			m.failCounts = make(map[string]int)
		}
		if m.failCounts[n.ID] < owed {
			m.failCounts[n.ID]++
			if m.FailResult != (Result{}) {
				return m.FailResult
			}
			return defaultFailResult
		}
	}

	if m.Results != nil {
		if r, ok := m.Results[n.ID]; ok {
			return r
		}
	}

	// Default: echo the node ID so a bare MockExecutor still produces a
	// non-empty, deterministic output.
	return Result{Output: n.ID}
}

// ---------------------------------------------------------------------------
// LLMExecutor
// ---------------------------------------------------------------------------

// LLMExecutor runs a node by building a prompt from the node's ActionRef and
// its dependency inputs, calling a provider.Provider, and (when the node
// carries a contract) validating the output with contract.Validate before
// reporting success.
//
// Outcome mapping (the scheduler relies on Retryable to choose local_retry vs.
// escalation):
//
//   - success + contract valid -> {Output, Tokens, Retryable:false, Err:nil}
//   - contract violation        -> {Retryable:false, Err: problems} (structural)
//   - transient provider error  -> {Retryable:true,  Err}  (timeout / 429-ish)
//   - other provider error      -> {Retryable:false, Err}  (structural)
type LLMExecutor struct {
	// Provider is the LLM backend used to execute the node.
	Provider provider.Provider
}

// NewLLMExecutor returns an LLMExecutor backed by p.
func NewLLMExecutor(p provider.Provider) *LLMExecutor {
	return &LLMExecutor{Provider: p}
}

// Execute runs the node via the provider and checks its contract.
func (l *LLMExecutor) Execute(ctx context.Context, n plan.Node, inputs map[string]string) Result {
	req := provider.Request{
		Prompt:       buildPrompt(n, inputs),
		SystemPrompt: "You are a node executor in a graph harness. Follow the instructions exactly.",
	}

	resp, err := l.Provider.Complete(ctx, req)
	if err != nil {
		// Transient errors (timeout / cancellation / rate-limit) are retryable;
		// everything else is treated as a structural failure the scheduler must
		// escalate rather than blindly retry.
		return Result{
			Retryable: isTransient(ctx, err),
			Err:       err,
		}
	}

	out := resp.Text
	tokens := resp.TotalTokens

	// Contract gate: a node may only enter `executed` if its output satisfies
	// the contract. A violation is structural (not retryable) - retrying the
	// same prompt will not fix a malformed shape.
	if n.Contract != nil {
		ok, problems := contract.Validate(out, n.Contract.Schema)
		if !ok {
			return Result{
				Output:    out,
				Tokens:    tokens,
				Retryable: false,
				Err:       fmt.Errorf("contract violation: %s", strings.Join(problems, "; ")),
			}
		}
	}

	return Result{Output: out, Tokens: tokens, Retryable: false, Err: nil}
}

// buildPrompt assembles a deterministic prompt from the node's action, its
// dependency inputs, and (when the node has a contract schema) an instruction
// to return JSON matching it. Inputs are emitted in sorted key order so the
// prompt - and thus any caching/snapshotting - is stable.
func buildPrompt(n plan.Node, inputs map[string]string) string {
	var b strings.Builder
	b.WriteString("Action: ")
	b.WriteString(n.ActionRef)
	b.WriteString("\n")

	if len(inputs) > 0 {
		b.WriteString("\nInputs:\n")
		keys := make([]string, 0, len(inputs))
		for k := range inputs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString("- ")
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(inputs[k])
			b.WriteString("\n")
		}
	}

	if n.Contract != nil && len(n.Contract.Schema) > 0 {
		b.WriteString("\nReturn ONLY a JSON object matching this schema:\n")
		b.Write(n.Contract.Schema)
		b.WriteString("\n")
	}

	return b.String()
}

// isTransient classifies a provider error as transient (worth retrying) vs.
// structural. Context deadline/cancellation and rate-limit/429-style errors are
// transient; anything else is structural.
func isTransient(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	// The mock/HTTP providers surface a cancelled context via ctx.Err(); if the
	// context is done, treat the error as transient even if it doesn't unwrap to
	// the sentinel.
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range transientMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// transientMarkers are lowercase substrings whose presence in an error message
// marks it transient. Covers the 429-ish and timeout vocabulary the real
// providers (and tests) emit.
var transientMarkers = []string{
	"429",
	"too many requests",
	"rate limit",
	"ratelimit",
	"timeout",
	"timed out",
	"deadline exceeded",
	"unavailable",
	"try again",
}

// ---------------------------------------------------------------------------
// ToolExecutor
// ---------------------------------------------------------------------------

// ToolFunc is a registered tool implementation: it runs a tool node's work and
// returns its output (the node's Result.Output) or an error.
type ToolFunc func(ctx context.Context, inputs map[string]string) (string, error)

// ToolExecutor runs "tool" nodes by dispatching on the node's ActionRef into a
// registry of named tools. A node's SideEffect level is metadata here: the
// scheduler (decision D4) enforces that destructive nodes are never
// speculatively parallel-dispatched; this executor merely runs the tool.
//
// An unknown ActionRef is a structural (non-retryable) error: the plan refers
// to a tool that was never registered, and retrying cannot fix that. Errors
// returned by a tool are surfaced as non-retryable structural failures.
type ToolExecutor struct {
	// Tools maps ActionRef -> tool implementation.
	Tools map[string]ToolFunc
}

// NewToolExecutor returns a ToolExecutor backed by the given registry, keyed by
// ActionRef.
func NewToolExecutor(tools map[string]ToolFunc) *ToolExecutor {
	return &ToolExecutor{Tools: tools}
}

// Execute dispatches the node to its registered tool by ActionRef.
func (t *ToolExecutor) Execute(ctx context.Context, n plan.Node, inputs map[string]string) Result {
	fn, ok := t.Tools[n.ActionRef]
	if !ok {
		return Result{
			Retryable: false,
			Err:       fmt.Errorf("tool: unknown action_ref %q for node %q", n.ActionRef, n.ID),
		}
	}

	out, err := fn(ctx, inputs)
	if err != nil {
		// A tool failure is structural by default: the scheduler should escalate
		// rather than blindly retry a side-effecting operation. (Transient
		// classification, if ever needed, belongs in the tool itself returning a
		// typed error - kept simple here per spec.)
		return Result{Output: out, Retryable: false, Err: err}
	}

	return Result{Output: out, Retryable: false, Err: nil}
}

// Compile-time checks that each executor satisfies the interface.
var (
	_ Executor = (*MockExecutor)(nil)
	_ Executor = (*LLMExecutor)(nil)
	_ Executor = (*ToolExecutor)(nil)
)
