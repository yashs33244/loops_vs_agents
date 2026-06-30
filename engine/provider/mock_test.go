package provider

import (
	"context"
	"errors"
	"testing"
)

func TestMockProvider_Name(t *testing.T) {
	tests := []struct {
		name string
		p    *MockProvider
		want string
	}{
		{"default", &MockProvider{}, "mock"},
		{"override", &MockProvider{ProviderName: "fake"}, "fake"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Name(); got != tt.want {
				t.Errorf("Name() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMockProvider_FixedResponse(t *testing.T) {
	want := Response{Text: `{"answer":42}`, InputTokens: 3, OutputTokens: 5, TotalTokens: 8}
	p := NewMockProvider(want)

	got, err := p.Complete(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got != want {
		t.Errorf("Complete() = %+v, want %+v", got, want)
	}
}

func TestMockProvider_Func(t *testing.T) {
	p := NewMockProviderFunc(func(req Request) (Response, error) {
		return Response{Text: "echo:" + req.Prompt, TotalTokens: len(req.Prompt)}, nil
	})

	got, err := p.Complete(context.Background(), Request{Prompt: "abc"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got.Text != "echo:abc" {
		t.Errorf("Text = %q, want %q", got.Text, "echo:abc")
	}
	if got.TotalTokens != 3 {
		t.Errorf("TotalTokens = %d, want 3", got.TotalTokens)
	}
}

func TestMockProvider_ScriptedError(t *testing.T) {
	sentinel := errors.New("rate limited")
	p := &MockProvider{Err: sentinel}

	_, err := p.Complete(context.Background(), Request{Prompt: "x"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Complete() error = %v, want %v", err, sentinel)
	}
}

func TestMockProvider_DefaultEchoesPrompt(t *testing.T) {
	p := &MockProvider{}
	got, err := p.Complete(context.Background(), Request{Prompt: "ping"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got.Text != "ping" {
		t.Errorf("Text = %q, want %q", got.Text, "ping")
	}
}

func TestMockProvider_RecordsCalls(t *testing.T) {
	p := &MockProvider{}
	reqs := []Request{{Prompt: "a"}, {Prompt: "b", Model: "m"}}
	for _, r := range reqs {
		if _, err := p.Complete(context.Background(), r); err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
	}
	if len(p.Calls) != 2 {
		t.Fatalf("len(Calls) = %d, want 2", len(p.Calls))
	}
	if p.Calls[1].Model != "m" {
		t.Errorf("Calls[1].Model = %q, want %q", p.Calls[1].Model, "m")
	}
}

func TestMockProvider_RespectsCancelledContext(t *testing.T) {
	p := NewMockProvider(Response{Text: "should not appear"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Complete(ctx, Request{Prompt: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Complete() error = %v, want context.Canceled", err)
	}
}

// MockProvider must satisfy the Provider interface (used by other packages).
func TestMockProvider_ImplementsProvider(t *testing.T) {
	var _ Provider = NewMockProvider(Response{})
}
