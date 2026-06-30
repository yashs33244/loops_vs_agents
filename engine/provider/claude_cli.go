package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ClaudeCLIProvider executes completions by shelling out to `claude -p`
// (decision D4: exec.CommandContext with an arg slice, no shell, a scrubbed
// minimal env, a process-group kill on context cancellation, run in a throwaway
// cwd). The CLI reports no token usage, so the returned token counts are zero.
type ClaudeCLIProvider struct {
	// Binary is the path to the claude CLI; defaults to "claude" when empty.
	Binary string

	// PromptViaStdin, when true, feeds the prompt on stdin (`claude -p`) instead
	// of as a positional argument (`claude -p <prompt>`). Default false passes
	// the prompt as an argument, matching the proven spike invocation.
	PromptViaStdin bool

	// WaitDelay bounds how long Wait blocks after the context is cancelled
	// before the process group is force-killed. Defaults to 5s when zero.
	WaitDelay time.Duration
}

// NewClaudeCLIProvider returns a ClaudeCLIProvider using the given binary path
// (empty means "claude" on PATH).
func NewClaudeCLIProvider(binary string) *ClaudeCLIProvider {
	return &ClaudeCLIProvider{Binary: binary}
}

// Name reports the provider name.
func (c *ClaudeCLIProvider) Name() string { return "claude" }

// binary returns the configured binary or the default.
func (c *ClaudeCLIProvider) binary() string {
	if c.Binary != "" {
		return c.Binary
	}
	return "claude"
}

// Available reports whether the claude CLI can be found on PATH (or at the
// configured Binary path). Tests gate live calls behind this.
func (c *ClaudeCLIProvider) Available() bool {
	_, err := exec.LookPath(c.binary())
	return err == nil
}

// buildArgs assembles the argument slice for one request. The prompt is either
// a positional argument after -p, or omitted (sent on stdin) when
// PromptViaStdin is set. A non-empty system prompt is passed via
// --append-system-prompt.
func (c *ClaudeCLIProvider) buildArgs(req Request) []string {
	args := []string{"-p"}
	if !c.PromptViaStdin {
		args = append(args, req.Prompt)
	}
	if req.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.SystemPrompt)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	return args
}

// scrubbedEnv returns a minimal environment that keeps only PATH and HOME so the
// CLI can still locate its binary and its auth/config under the home dir, while
// not leaking the parent process's full environment into the subprocess
// (decision D4: scrubbed env).
func scrubbedEnv() []string {
	var env []string
	if p := os.Getenv("PATH"); p != "" {
		env = append(env, "PATH="+p)
	}
	if h := os.Getenv("HOME"); h != "" {
		env = append(env, "HOME="+h)
	}
	return env
}

// Complete runs the CLI for one completion. Text is the trimmed stdout; token
// counts are zero because the CLI reports none.
func (c *ClaudeCLIProvider) Complete(ctx context.Context, req Request) (Response, error) {
	dir, derr := os.MkdirTemp("", "sgh-claude-")
	if derr == nil {
		defer os.RemoveAll(dir)
	}

	var stdin string
	if c.PromptViaStdin {
		stdin = req.Prompt
	}

	out, stderr, _, err := c.runCmd(ctx, c.buildArgs(req), dir, stdin)
	if err != nil {
		return Response{}, fmt.Errorf("claude cli: %w | stderr: %s", err, strings.TrimSpace(stderr))
	}
	return Response{Text: strings.TrimSpace(out)}, nil
}

// runCmd runs an isolated subprocess in its own process group so a context
// timeout kills the whole tree, not just the parent. This mirrors the proven
// spike helper (../spike/main.go).
func (c *ClaudeCLIProvider) runCmd(ctx context.Context, args []string, dir, stdin string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, c.binary(), args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = scrubbedEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			// Negative pid signals the whole process group.
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	wd := c.WaitDelay
	if wd == 0 {
		wd = 5 * time.Second
	}
	cmd.WaitDelay = wd

	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	runErr := cmd.Run()

	exitCode = 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return out.String(), errb.String(), exitCode, runErr
}
