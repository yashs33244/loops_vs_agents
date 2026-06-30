package provider

import (
	"context"
	"errors"
)

// errNotImplemented is the placeholder error returned by skeleton methods that
// must compile and return a value instead of panicking. Retained for any
// callers that test against it; all real impls below are complete.
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

// Compile-time checks that each provider satisfies the interface.
var (
	_ Provider = (*MockProvider)(nil)
	_ Provider = (*ClaudeCLIProvider)(nil)
	_ Provider = (*GeminiAPIProvider)(nil)
)
