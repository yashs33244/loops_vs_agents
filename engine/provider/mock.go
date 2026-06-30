package provider

import "context"

// MockProvider is a deterministic, scriptable provider for tests. It never does
// I/O, so other packages can exercise the full Provider contract offline.
//
// Three ways to script it, in priority order:
//
//   - Fn:        a func(Request) (Response, error) gives full control per call.
//   - Err:       a fixed error returned (with the zero Response) on every call.
//   - Resp:      a fixed Response returned on every call.
//
// If none are set, Complete echoes the request prompt back as the response
// text, which is enough for executors that only need a non-empty result.
type MockProvider struct {
	// ProviderName overrides the reported name; defaults to "mock" when empty.
	ProviderName string

	// Fn, when non-nil, is called for every Complete and its result returned
	// verbatim. This is the most flexible scripting hook.
	Fn func(req Request) (Response, error)

	// Resp is the fixed response returned when Fn is nil and Err is nil.
	Resp Response

	// Err is the fixed error returned (alongside Resp) when Fn is nil. A non-nil
	// Err lets tests script failure paths deterministically.
	Err error

	// Calls records every Request seen, in order, so tests can assert on what
	// was sent to the provider.
	Calls []Request
}

// NewMockProvider returns a MockProvider that always yields the given Response
// and nil error.
func NewMockProvider(resp Response) *MockProvider {
	return &MockProvider{Resp: resp}
}

// NewMockProviderFunc returns a MockProvider driven by fn, which is invoked for
// every Complete call.
func NewMockProviderFunc(fn func(req Request) (Response, error)) *MockProvider {
	return &MockProvider{Fn: fn}
}

// Name reports the provider name.
func (m *MockProvider) Name() string {
	if m.ProviderName != "" {
		return m.ProviderName
	}
	return "mock"
}

// Complete returns a scripted response and never performs I/O.
func (m *MockProvider) Complete(ctx context.Context, req Request) (Response, error) {
	m.Calls = append(m.Calls, req)

	// Respect a cancelled context deterministically so executors can be tested
	// against cancellation without a real backend.
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}

	if m.Fn != nil {
		return m.Fn(req)
	}
	if m.Err != nil {
		return m.Resp, m.Err
	}
	if m.Resp != (Response{}) {
		return m.Resp, nil
	}
	// Default: echo the prompt back so a bare MockProvider is still useful.
	return Response{Text: req.Prompt}, nil
}
