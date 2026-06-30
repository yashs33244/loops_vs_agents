package node

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"sgh/engine/plan"
	"sgh/engine/provider"
)

// ---------------------------------------------------------------------------
// MockExecutor
// ---------------------------------------------------------------------------

func TestMockExecutor_Canned(t *testing.T) {
	want := Result{Output: `{"ok":true}`, Tokens: 7}
	m := NewMockExecutor(map[string]Result{"a": want})

	got := m.Execute(context.Background(), plan.Node{ID: "a"}, nil)
	if got != want {
		t.Fatalf("canned result: got %+v, want %+v", got, want)
	}
}

func TestMockExecutor_DefaultEchoesNodeID(t *testing.T) {
	m := NewMockExecutor(map[string]Result{}) // node "b" not present
	got := m.Execute(context.Background(), plan.Node{ID: "b"}, nil)
	if got.Err != nil {
		t.Fatalf("default: unexpected err %v", got.Err)
	}
	if got.Output != "b" {
		t.Fatalf("default echo: got %q, want %q", got.Output, "b")
	}
}

func TestMockExecutor_Func(t *testing.T) {
	m := NewMockExecutorFunc(func(n plan.Node, inputs map[string]string) Result {
		return Result{Output: n.ActionRef + ":" + inputs["x"]}
	})
	got := m.Execute(context.Background(), plan.Node{ID: "n", ActionRef: "act"}, map[string]string{"x": "v"})
	if got.Output != "act:v" {
		t.Fatalf("func: got %q, want %q", got.Output, "act:v")
	}
}

func TestMockExecutor_FailNTimesThenSucceed(t *testing.T) {
	const fails = 2
	m := &MockExecutor{
		FailNTimes: map[string]int{"flaky": fails},
		Results:    map[string]Result{"flaky": {Output: "recovered"}},
	}
	n := plan.Node{ID: "flaky"}

	// First `fails` calls must be retryable failures.
	for i := 0; i < fails; i++ {
		got := m.Execute(context.Background(), n, nil)
		if got.Err == nil {
			t.Fatalf("call %d: expected failure, got success %+v", i, got)
		}
		if !got.Retryable {
			t.Fatalf("call %d: injected failure must be retryable", i)
		}
	}

	// Next call succeeds with the canned result.
	got := m.Execute(context.Background(), n, nil)
	if got.Err != nil {
		t.Fatalf("after %d fails: expected success, got err %v", fails, got.Err)
	}
	if got.Output != "recovered" {
		t.Fatalf("after recovery: got %q, want %q", got.Output, "recovered")
	}

	if len(m.Calls) != fails+1 {
		t.Fatalf("recorded calls: got %d, want %d", len(m.Calls), fails+1)
	}
}

func TestMockExecutor_FailNTimesCustomResult(t *testing.T) {
	custom := Result{Retryable: false, Err: errors.New("hard fail")}
	m := &MockExecutor{
		FailNTimes: map[string]int{"x": 1},
		FailResult: custom,
		Results:    map[string]Result{"x": {Output: "ok"}},
	}
	got := m.Execute(context.Background(), plan.Node{ID: "x"}, nil)
	if got.Retryable || got.Err == nil {
		t.Fatalf("custom fail result not honoured: %+v", got)
	}
	got = m.Execute(context.Background(), plan.Node{ID: "x"}, nil)
	if got.Output != "ok" {
		t.Fatalf("after custom fail: got %q, want ok", got.Output)
	}
}

func TestMockExecutor_RecordsCalls(t *testing.T) {
	m := NewMockExecutor(nil)
	m.Execute(context.Background(), plan.Node{ID: "a"}, map[string]string{"k": "1"})
	m.Execute(context.Background(), plan.Node{ID: "b"}, nil)
	if len(m.Calls) != 2 {
		t.Fatalf("calls: got %d, want 2", len(m.Calls))
	}
	if m.Calls[0].NodeID != "a" || m.Calls[0].Inputs["k"] != "1" {
		t.Fatalf("call 0 mismatch: %+v", m.Calls[0])
	}
	if m.Calls[1].NodeID != "b" {
		t.Fatalf("call 1 mismatch: %+v", m.Calls[1])
	}
}

func TestMockExecutor_RespectsCancelledContext(t *testing.T) {
	m := NewMockExecutor(map[string]Result{"a": {Output: "should not appear"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := m.Execute(ctx, plan.Node{ID: "a"}, nil)
	if got.Err == nil {
		t.Fatalf("cancelled context: expected error")
	}
	if !got.Retryable {
		t.Fatalf("cancelled context: expected retryable")
	}
}

// ---------------------------------------------------------------------------
// LLMExecutor
// ---------------------------------------------------------------------------

func TestLLMExecutor_SuccessNoContract(t *testing.T) {
	p := provider.NewMockProvider(provider.Response{Text: `{"result":"done"}`, TotalTokens: 42})
	l := NewLLMExecutor(p)

	n := plan.Node{ID: "n", ActionRef: "summarize"}
	got := l.Execute(context.Background(), n, map[string]string{"src": "hello"})

	if got.Err != nil {
		t.Fatalf("unexpected err: %v", got.Err)
	}
	if got.Retryable {
		t.Fatalf("success must not be retryable")
	}
	if got.Output != `{"result":"done"}` {
		t.Fatalf("output: got %q", got.Output)
	}
	if got.Tokens != 42 {
		t.Fatalf("tokens: got %d, want 42", got.Tokens)
	}

	// The prompt should carry the action and the input.
	if len(p.Calls) != 1 {
		t.Fatalf("provider calls: got %d, want 1", len(p.Calls))
	}
	prompt := p.Calls[0].Prompt
	if !strings.Contains(prompt, "summarize") || !strings.Contains(prompt, "hello") {
		t.Fatalf("prompt missing action/input: %q", prompt)
	}
}

func TestLLMExecutor_SuccessWithContractValid(t *testing.T) {
	schema := json.RawMessage(`{"required":["answer"],"types":{"answer":"string"}}`)
	p := provider.NewMockProvider(provider.Response{Text: `{"answer":"yes"}`, TotalTokens: 5})
	l := NewLLMExecutor(p)

	n := plan.Node{ID: "n", ActionRef: "ask", Contract: &plan.Contract{Schema: schema}}
	got := l.Execute(context.Background(), n, nil)

	if got.Err != nil {
		t.Fatalf("valid output should pass contract: %v", got.Err)
	}
	if got.Retryable {
		t.Fatalf("success must not be retryable")
	}
	if got.Output != `{"answer":"yes"}` {
		t.Fatalf("output: got %q", got.Output)
	}

	// The contract schema should be reflected into the prompt as an instruction.
	if !strings.Contains(p.Calls[0].Prompt, "JSON") {
		t.Fatalf("prompt should instruct JSON output: %q", p.Calls[0].Prompt)
	}
}

func TestLLMExecutor_ContractViolationStructural(t *testing.T) {
	// Schema requires "answer":string, but the provider returns a number for it
	// and omits nothing else - a structural violation.
	schema := json.RawMessage(`{"required":["answer"],"types":{"answer":"string"}}`)
	p := provider.NewMockProvider(provider.Response{Text: `{"answer":123}`, TotalTokens: 3})
	l := NewLLMExecutor(p)

	n := plan.Node{ID: "n", ActionRef: "ask", Contract: &plan.Contract{Schema: schema}}
	got := l.Execute(context.Background(), n, nil)

	if got.Err == nil {
		t.Fatalf("contract violation must produce an error")
	}
	if got.Retryable {
		t.Fatalf("contract violation must NOT be retryable (structural)")
	}
	if !strings.Contains(got.Err.Error(), "contract violation") {
		t.Fatalf("err should mention contract violation: %v", got.Err)
	}
	// The raw (invalid) output is still surfaced for diagnostics.
	if got.Output != `{"answer":123}` {
		t.Fatalf("output should be surfaced: got %q", got.Output)
	}
}

func TestLLMExecutor_MissingRequiredKeyStructural(t *testing.T) {
	schema := json.RawMessage(`{"required":["answer"]}`)
	p := provider.NewMockProvider(provider.Response{Text: `{"other":"x"}`})
	l := NewLLMExecutor(p)

	n := plan.Node{ID: "n", ActionRef: "ask", Contract: &plan.Contract{Schema: schema}}
	got := l.Execute(context.Background(), n, nil)
	if got.Err == nil || got.Retryable {
		t.Fatalf("missing required key should be structural error: %+v", got)
	}
}

func TestLLMExecutor_TransientContextDeadline(t *testing.T) {
	p := provider.NewMockProviderFunc(func(req provider.Request) (provider.Response, error) {
		return provider.Response{}, context.DeadlineExceeded
	})
	l := NewLLMExecutor(p)

	got := l.Execute(context.Background(), plan.Node{ID: "n", ActionRef: "x"}, nil)
	if got.Err == nil {
		t.Fatalf("expected error")
	}
	if !got.Retryable {
		t.Fatalf("context deadline must be retryable (transient)")
	}
}

func TestLLMExecutor_TransientRateLimit429(t *testing.T) {
	// Mirrors what GeminiAPIProvider surfaces on a sticky 429: "gemini http 429".
	p := provider.NewMockProviderFunc(func(req provider.Request) (provider.Response, error) {
		return provider.Response{}, errors.New("gemini http 429")
	})
	l := NewLLMExecutor(p)

	got := l.Execute(context.Background(), plan.Node{ID: "n", ActionRef: "x"}, nil)
	if got.Err == nil {
		t.Fatalf("expected error")
	}
	if !got.Retryable {
		t.Fatalf("429-ish error must be retryable (transient): %v", got.Err)
	}
}

func TestLLMExecutor_TransientCancelledContext(t *testing.T) {
	// The MockProvider returns ctx.Err() when the context is already cancelled.
	p := provider.NewMockProvider(provider.Response{Text: "unused"})
	l := NewLLMExecutor(p)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := l.Execute(ctx, plan.Node{ID: "n", ActionRef: "x"}, nil)
	if got.Err == nil || !got.Retryable {
		t.Fatalf("cancelled context should be retryable transient: %+v", got)
	}
}

func TestLLMExecutor_OtherProviderErrorStructural(t *testing.T) {
	p := provider.NewMockProviderFunc(func(req provider.Request) (provider.Response, error) {
		return provider.Response{}, errors.New("400 bad request: malformed model name")
	})
	l := NewLLMExecutor(p)

	got := l.Execute(context.Background(), plan.Node{ID: "n", ActionRef: "x"}, nil)
	if got.Err == nil {
		t.Fatalf("expected error")
	}
	if got.Retryable {
		t.Fatalf("a non-transient provider error must NOT be retryable: %v", got.Err)
	}
}

// ---------------------------------------------------------------------------
// ToolExecutor
// ---------------------------------------------------------------------------

func TestToolExecutor_RegisteredToolRuns(t *testing.T) {
	te := NewToolExecutor(map[string]ToolFunc{
		"echo": func(ctx context.Context, inputs map[string]string) (string, error) {
			return "echoed:" + inputs["v"], nil
		},
	})

	n := plan.Node{ID: "t", ActionRef: "echo", SideEffect: plan.SideEffectRead}
	got := te.Execute(context.Background(), n, map[string]string{"v": "hi"})
	if got.Err != nil {
		t.Fatalf("unexpected err: %v", got.Err)
	}
	if got.Retryable {
		t.Fatalf("tool success must not be retryable")
	}
	if got.Output != "echoed:hi" {
		t.Fatalf("output: got %q, want %q", got.Output, "echoed:hi")
	}
}

func TestToolExecutor_UnknownActionRefStructural(t *testing.T) {
	te := NewToolExecutor(map[string]ToolFunc{
		"known": func(ctx context.Context, inputs map[string]string) (string, error) {
			return "ok", nil
		},
	})
	n := plan.Node{ID: "t", ActionRef: "missing"}
	got := te.Execute(context.Background(), n, nil)
	if got.Err == nil {
		t.Fatalf("unknown action_ref must error")
	}
	if got.Retryable {
		t.Fatalf("unknown action_ref must be structural (not retryable)")
	}
	if !strings.Contains(got.Err.Error(), "unknown action_ref") {
		t.Fatalf("err should mention unknown action_ref: %v", got.Err)
	}
}

func TestToolExecutor_ToolErrorStructural(t *testing.T) {
	sentinel := errors.New("disk full")
	te := NewToolExecutor(map[string]ToolFunc{
		"write": func(ctx context.Context, inputs map[string]string) (string, error) {
			return "", sentinel
		},
	})
	n := plan.Node{ID: "t", ActionRef: "write", SideEffect: plan.SideEffectWrite}
	got := te.Execute(context.Background(), n, nil)
	if !errors.Is(got.Err, sentinel) {
		t.Fatalf("tool error should be surfaced: %v", got.Err)
	}
	if got.Retryable {
		t.Fatalf("tool error is structural by default")
	}
}

func TestToolExecutor_RespectsSideEffectMetadata(t *testing.T) {
	// SideEffect is metadata at this layer: a destructive node still runs; the
	// scheduler (not this executor) enforces scheduling restrictions.
	var ran bool
	te := NewToolExecutor(map[string]ToolFunc{
		"rm": func(ctx context.Context, inputs map[string]string) (string, error) {
			ran = true
			return "deleted", nil
		},
	})
	n := plan.Node{ID: "t", ActionRef: "rm", SideEffect: plan.SideEffectDestructive}
	got := te.Execute(context.Background(), n, nil)
	if !ran || got.Err != nil {
		t.Fatalf("destructive tool should still run: ran=%v err=%v", ran, got.Err)
	}
}

// Interface conformance (mirrors the compile-time checks for documentation).
func TestExecutors_ImplementInterface(t *testing.T) {
	var _ Executor = (*MockExecutor)(nil)
	var _ Executor = (*LLMExecutor)(nil)
	var _ Executor = (*ToolExecutor)(nil)
}
