package provider

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestClaudeCLIProvider_Name(t *testing.T) {
	c := NewClaudeCLIProvider("")
	if c.Name() != "claude" {
		t.Errorf("Name() = %q, want %q", c.Name(), "claude")
	}
}

func TestClaudeCLIProvider_BinaryDefault(t *testing.T) {
	if got := (&ClaudeCLIProvider{}).binary(); got != "claude" {
		t.Errorf("binary() = %q, want %q", got, "claude")
	}
	if got := (&ClaudeCLIProvider{Binary: "/opt/claude"}).binary(); got != "/opt/claude" {
		t.Errorf("binary() = %q, want %q", got, "/opt/claude")
	}
}

func TestClaudeCLIProvider_BuildArgs(t *testing.T) {
	tests := []struct {
		name     string
		c        *ClaudeCLIProvider
		req      Request
		wantArgs []string
	}{
		{
			name:     "prompt as arg",
			c:        &ClaudeCLIProvider{},
			req:      Request{Prompt: "hello"},
			wantArgs: []string{"-p", "hello"},
		},
		{
			name:     "prompt via stdin omits positional",
			c:        &ClaudeCLIProvider{PromptViaStdin: true},
			req:      Request{Prompt: "hello"},
			wantArgs: []string{"-p"},
		},
		{
			name:     "system prompt appended",
			c:        &ClaudeCLIProvider{},
			req:      Request{Prompt: "hi", SystemPrompt: "be terse"},
			wantArgs: []string{"-p", "hi", "--append-system-prompt", "be terse"},
		},
		{
			name:     "model flag appended",
			c:        &ClaudeCLIProvider{},
			req:      Request{Prompt: "hi", Model: "opus"},
			wantArgs: []string{"-p", "hi", "--model", "opus"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.c.buildArgs(tt.req)
			if len(got) != len(tt.wantArgs) {
				t.Fatalf("buildArgs() = %v, want %v", got, tt.wantArgs)
			}
			for i := range got {
				if got[i] != tt.wantArgs[i] {
					t.Errorf("buildArgs()[%d] = %q, want %q", i, got[i], tt.wantArgs[i])
				}
			}
		})
	}
}

func TestScrubbedEnv(t *testing.T) {
	// scrubbedEnv keeps only PATH and HOME and never leaks arbitrary vars.
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/home/test")
	t.Setenv("SECRET_TOKEN", "should-not-leak")

	env := scrubbedEnv()
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "PATH=/usr/bin:/bin") {
		t.Errorf("scrubbedEnv missing PATH: %v", env)
	}
	if !strings.Contains(joined, "HOME=/home/test") {
		t.Errorf("scrubbedEnv missing HOME: %v", env)
	}
	if strings.Contains(joined, "SECRET_TOKEN") {
		t.Errorf("scrubbedEnv leaked SECRET_TOKEN: %v", env)
	}
	if len(env) != 2 {
		t.Errorf("scrubbedEnv len = %d, want 2 (got %v)", len(env), env)
	}
}

func TestClaudeCLIProvider_ImplementsProvider(t *testing.T) {
	var _ Provider = NewClaudeCLIProvider("")
}

// TestClaudeCLIProvider_Live exercises a real `claude -p` call. It is opt-in:
// it runs only when SGH_LIVE=1 is set and the binary is on PATH, so normal
// `go test` never shells out (and never depends on the binary being present
// or authenticated).
func TestClaudeCLIProvider_Live(t *testing.T) {
	if os.Getenv("SGH_LIVE") != "1" {
		t.Skip("set SGH_LIVE=1 to run the live claude CLI call")
	}
	c := NewClaudeCLIProvider("")
	if !c.Available() {
		t.Skip("claude binary not on PATH; skipping live CLI call")
	}
	resp, err := c.Complete(context.Background(), Request{
		Prompt: `Respond with ONLY {"answer":42} and nothing else.`,
	})
	if err != nil {
		t.Fatalf("live Complete() error = %v", err)
	}
	if resp.Text == "" {
		t.Errorf("live Complete() returned empty text")
	}
	t.Logf("claude live response: %q", resp.Text)
}
