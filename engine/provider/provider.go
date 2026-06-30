package provider

import (
	"context"
	"errors"
)

// errNotImplemented is the placeholder error returned by skeleton methods that
// must compile and return a value instead of panicking.
var errNotImplemented = errors.New("not implemented")

// Request is a single completion request to a provider.
type Request struct {
	Prompt       string
	SystemPrompt string
	Model        string // "" = provider default
	MaxTokens    int
}

// Response is a single completion result plus token accounting.
type Response struct {
	Text         string
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// Provider is an LLM backend: it names itself and turns a Request into a
// Response (paper: the action that executes a node's work).
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
}

// MockProvider is a deterministic, scriptable provider for tests.
//
// STUB: scripting fields/logic are filled in by the implementer.
type MockProvider struct {
	// ProviderName overrides the reported name; defaults to "mock" when empty.
	ProviderName string
}

// Name reports the provider name.
func (m *MockProvider) Name() string {
	if m.ProviderName != "" {
		return m.ProviderName
	}
	return "mock"
}

// Complete returns a scripted response.
//
// STUB: not implemented yet.
func (m *MockProvider) Complete(ctx context.Context, req Request) (Response, error) {
	return Response{}, errNotImplemented
}

// ClaudeCLIProvider executes completions by shelling out to `claude -p`
// (decision D4: exec.CommandContext, arg slice, prompt via stdin, no shell).
//
// STUB: config fields/logic are filled in by the implementer.
type ClaudeCLIProvider struct {
	// Binary is the path to the claude CLI; defaults to "claude" when empty.
	Binary string
}

// Name reports the provider name.
func (c *ClaudeCLIProvider) Name() string { return "claude" }

// Complete runs the CLI for one completion.
//
// STUB: not implemented yet.
func (c *ClaudeCLIProvider) Complete(ctx context.Context, req Request) (Response, error) {
	return Response{}, errNotImplemented
}

// GeminiAPIProvider executes completions via the HTTP generateContent endpoint,
// with the API key read from GEMINI_API_KEY.
//
// STUB: config fields/logic are filled in by the implementer.
type GeminiAPIProvider struct {
	// APIKey is the Gemini API key; when empty the implementer reads
	// GEMINI_API_KEY from the environment.
	APIKey string
}

// Name reports the provider name.
func (g *GeminiAPIProvider) Name() string { return "gemini" }

// Complete calls the HTTP API for one completion.
//
// STUB: not implemented yet.
func (g *GeminiAPIProvider) Complete(ctx context.Context, req Request) (Response, error) {
	return Response{}, errNotImplemented
}

// Compile-time checks that each provider satisfies the interface.
var (
	_ Provider = (*MockProvider)(nil)
	_ Provider = (*ClaudeCLIProvider)(nil)
	_ Provider = (*GeminiAPIProvider)(nil)
)
