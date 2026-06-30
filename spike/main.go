// Command spike is the M0 GO/NO-GO probe for Graph Harness (SGH).
//
// It answers the project's load-bearing question BEFORE any engine code:
// can a given backend act as a clean "completion node"?
//   - strict JSON out:  does the call reliably return parseable JSON?
//   - concurrency:       can N calls run in parallel without contaminating each other?
//   - cancellation:      does a context timeout actually abort the call promptly?
//   - cost/latency:      wall-clock per call, and tokens where the backend reports them.
//
// Backends:
//   - claude     : Claude Code CLI   (`claude -p`, subprocess)
//   - gemini-cli : Gemini CLI        (`gemini -p`, subprocess)
//   - gemini-api : Gemini HTTP API   (generateContent; needs GEMINI_API_KEY env)
//
// Throwaway by design. Stdlib only.
// Run: GEMINI_API_KEY=... go run . -providers gemini-api -gemini-model gemini-3.1-flash-lite
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A deliberately trivial, tool-free task: the model only has to emit strict JSON.
// If a backend can't do this cleanly, it can't be a deterministic node executor.
const prompt = `Respond with ONLY a single-line minified JSON object of the form {"answer": N} where N is an integer, and nothing else - no prose, no markdown, no code fences. Question: what is 6 * 7?`

type callOut struct {
	stdout   string
	exitCode int
	tokens   *int // total tokens, when the backend reports them
}

type provider struct {
	name      string
	available func() bool
	call      func(ctx context.Context, prompt string) (callOut, error)
}

type result struct {
	Provider   string `json:"provider"`
	Index      int    `json:"index"`
	DurationMS int64  `json:"duration_ms"`
	ExitCode   int    `json:"exit_code"`
	JSONOK     bool   `json:"json_ok"`
	JSONClean  bool   `json:"json_clean"`
	Correct    bool   `json:"correct"`
	Answer     *int   `json:"answer,omitempty"`
	Tokens     *int   `json:"tokens,omitempty"`
	ErrMsg     string `json:"err,omitempty"`
	StdoutHead string `json:"stdout_head,omitempty"`
}

// --- backends ---------------------------------------------------------------

func cliProvider(name, bin string, mkArgs func(string) []string) provider {
	return provider{
		name:      name,
		available: func() bool { _, e := exec.LookPath(bin); return e == nil },
		call: func(ctx context.Context, p string) (callOut, error) {
			dir, derr := os.MkdirTemp("", "sgh-spike-")
			if derr == nil {
				defer os.RemoveAll(dir)
			}
			out, stderr, code, err := runCmd(ctx, bin, mkArgs(p), dir)
			if err != nil {
				err = fmt.Errorf("%w | stderr: %s", err, strings.TrimSpace(stderr))
			}
			return callOut{stdout: out, exitCode: code}, err
		},
	}
}

// geminiAPIProvider calls the Generative Language API generateContent endpoint
// with responseMimeType=application/json (native structured output) so JSON is
// not a matter of prompt luck. The key is read from env, never stored.
func geminiAPIProvider(model, key string) provider {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, key)
	return provider{
		name:      "gemini-api",
		available: func() bool { return key != "" },
		call: func(ctx context.Context, p string) (callOut, error) {
			reqBody := map[string]any{
				"contents":         []any{map[string]any{"parts": []any{map[string]any{"text": p}}}},
				"generationConfig": map[string]any{"responseMimeType": "application/json"},
			}
			b, _ := json.Marshal(reqBody)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
			if err != nil {
				return callOut{exitCode: -1}, err
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return callOut{exitCode: -1}, err
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			var gr struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
				UsageMetadata struct {
					TotalTokenCount int `json:"totalTokenCount"`
				} `json:"usageMetadata"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if e := json.Unmarshal(body, &gr); e != nil {
				return callOut{exitCode: resp.StatusCode}, fmt.Errorf("decode: %v (%s)", e, trunc(string(body), 120))
			}
			if gr.Error != nil {
				return callOut{exitCode: resp.StatusCode}, fmt.Errorf("api error: %s", gr.Error.Message)
			}
			if resp.StatusCode != http.StatusOK {
				return callOut{exitCode: resp.StatusCode}, fmt.Errorf("http %d", resp.StatusCode)
			}
			text := ""
			if len(gr.Candidates) > 0 && len(gr.Candidates[0].Content.Parts) > 0 {
				text = gr.Candidates[0].Content.Parts[0].Text
			}
			co := callOut{stdout: text, exitCode: 0}
			if gr.UsageMetadata.TotalTokenCount > 0 {
				t := gr.UsageMetadata.TotalTokenCount
				co.tokens = &t
			}
			return co, nil
		},
	}
}

func buildProviders(geminiModel string) []provider {
	ps := []provider{
		cliProvider("claude", "claude", func(p string) []string { return []string{"-p", p} }),
		cliProvider("gemini-cli", "gemini", func(p string) []string { return []string{"-p", p} }),
	}
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		ps = append(ps, geminiAPIProvider(geminiModel, key))
	}
	return ps
}

// --- subprocess helper ------------------------------------------------------

// runCmd runs an isolated subprocess in its own process group so a context
// timeout kills the whole tree, not just the parent.
func runCmd(ctx context.Context, bin string, args []string, dir string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second

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

// --- parsing ----------------------------------------------------------------

func parseAnswer(out string) (ans *int, ok, clean bool) {
	type payload struct {
		Answer *int `json:"answer"`
	}
	s := strings.TrimSpace(out)
	var p payload
	if json.Unmarshal([]byte(s), &p) == nil && p.Answer != nil {
		return p.Answer, true, true
	}
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i >= 0 && j > i {
		if json.Unmarshal([]byte(s[i:j+1]), &p) == nil && p.Answer != nil {
			return p.Answer, true, false
		}
	}
	return nil, false, false
}

func trunc(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", "\\n")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func runOne(p provider, idx int, timeout time.Duration) result {
	r := result{Provider: p.name, Index: idx}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	co, err := p.call(ctx, prompt)
	r.DurationMS = time.Since(start).Milliseconds()
	r.ExitCode = co.exitCode
	r.Tokens = co.tokens
	if err != nil {
		r.ErrMsg = trunc(err.Error(), 200)
	}
	r.StdoutHead = trunc(co.stdout, 160)
	ans, ok, clean := parseAnswer(co.stdout)
	r.JSONOK, r.JSONClean, r.Answer = ok, clean, ans
	r.Correct = ans != nil && *ans == 42
	return r
}

func main() {
	n := flag.Int("n", 5, "calls per provider")
	conc := flag.Int("conc", 4, "max concurrent calls")
	timeoutS := flag.Int("timeout", 120, "per-call timeout in seconds")
	only := flag.String("providers", "claude,gemini-cli,gemini-api", "comma-separated providers to test")
	geminiModel := flag.String("gemini-model", "gemini-3.1-flash-lite", "Gemini API model id")
	flag.Parse()

	want := map[string]bool{}
	for _, s := range strings.Split(*only, ",") {
		want[strings.TrimSpace(s)] = true
	}

	var active []provider
	for _, p := range buildProviders(*geminiModel) {
		if !want[p.name] {
			continue
		}
		if p.available != nil && !p.available() {
			fmt.Printf("SKIP %-10s unavailable (not on PATH / no key)\n", p.name)
			continue
		}
		active = append(active, p)
	}
	if len(active) == 0 {
		fmt.Println("No providers available. Nothing to test.")
		os.Exit(1)
	}

	names := make([]string, len(active))
	for i, p := range active {
		names[i] = p.name
	}
	fmt.Printf("M0 CLI spike: %d call(s)/provider, conc=%d, timeout=%ds, gemini-model=%s, providers=[%s]\n\n",
		*n, *conc, *timeoutS, *geminiModel, strings.Join(names, " "))

	sem := make(chan struct{}, *conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []result

	for _, p := range active {
		for i := 0; i < *n; i++ {
			wg.Add(1)
			sem <- struct{}{}
			go func(p provider, i int) {
				defer wg.Done()
				defer func() { <-sem }()
				res := runOne(p, i, time.Duration(*timeoutS)*time.Second)
				mu.Lock()
				results = append(results, res)
				mu.Unlock()
				tag := "ok-json"
				if !res.JSONOK {
					tag = "NO-JSON"
				}
				tok := ""
				if res.Tokens != nil {
					tok = fmt.Sprintf("  tok=%d", *res.Tokens)
				}
				fmt.Printf("  %-10s #%d  %6dms  exit=%-3d %s%s\n", res.Provider, res.Index, res.DurationMS, res.ExitCode, tag, tok)
			}(p, i)
		}
	}
	wg.Wait()

	best := report(active, results)
	killed := cancelTest(active[0])

	if b, err := json.MarshalIndent(results, "", "  "); err == nil {
		_ = os.WriteFile("results.json", b, 0o644)
		fmt.Println("\nWrote results.json")
	}

	fmt.Println("\n=== VERDICT ===")
	switch {
	case best >= 0.8 && killed:
		fmt.Printf("GO: a backend reliably returns strict JSON (best=%.0f%%) and cancellation works.\n", best*100)
	case best >= 0.5:
		fmt.Printf("CAUTION: JSON reliability is moderate (best=%.0f%%). Add extraction + retry, then re-run.\n", best*100)
	default:
		fmt.Printf("NO-GO: no backend reliably behaves as a completion node (best=%.0f%%).\n", best*100)
	}
}

func report(active []provider, results []result) (bestRate float64) {
	fmt.Println("\n=== PER-PROVIDER SUMMARY ===")
	for _, p := range active {
		var rs []result
		for _, r := range results {
			if r.Provider == p.name {
				rs = append(rs, r)
			}
		}
		if len(rs) == 0 {
			continue
		}
		var jsonOK, clean, correct, nonzero, tokSum, tokN int
		var durs []int64
		var sampleErr, sampleOut string
		for _, r := range rs {
			if r.JSONOK {
				jsonOK++
			}
			if r.JSONClean {
				clean++
			}
			if r.Correct {
				correct++
			}
			if r.ExitCode != 0 {
				nonzero++
			}
			if r.Tokens != nil {
				tokSum += *r.Tokens
				tokN++
			}
			durs = append(durs, r.DurationMS)
			if r.ErrMsg != "" && sampleErr == "" {
				sampleErr = r.ErrMsg
			}
			if !r.JSONOK && sampleOut == "" && r.StdoutHead != "" {
				sampleOut = r.StdoutHead
			}
		}
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		rate := float64(jsonOK) / float64(len(rs))
		if rate > bestRate {
			bestRate = rate
		}
		tokStr := "n/a"
		if tokN > 0 {
			tokStr = fmt.Sprintf("%d", tokSum/tokN)
		}
		fmt.Printf("%-10s n=%d  json=%d/%d (%.0f%%)  clean=%d  correct=%d  exit!=0:%d  latency[min/med/max]=%d/%d/%dms  avg_tokens=%s\n",
			p.name, len(rs), jsonOK, len(rs), rate*100, clean, correct, nonzero,
			durs[0], durs[len(durs)/2], durs[len(durs)-1], tokStr)
		if sampleErr != "" {
			fmt.Printf("           sample err: %s\n", sampleErr)
		}
		if sampleOut != "" {
			fmt.Printf("           sample non-JSON stdout: %s\n", sampleOut)
		}
	}
	return bestRate
}

// cancelTest fires one long call with a short timeout and verifies it aborts
// promptly (process-group kill for CLIs; request cancellation for HTTP).
func cancelTest(p provider) bool {
	fmt.Println("\n=== CANCELLATION TEST ===")
	longCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 1500*time.Millisecond)
	}
	ctx, cancel := longCtx()
	defer cancel()
	start := time.Now()
	_, err := p.call(ctx, "Write a detailed 4000-word essay about the history of relational databases.")
	dur := time.Since(start)
	aborted := err != nil && dur < 10*time.Second
	fmt.Printf("provider=%s  elapsed=%dms  aborted=%v\n", p.name, dur.Milliseconds(), err != nil)
	if aborted {
		fmt.Println("PASS: call aborted promptly on context timeout.")
	} else {
		fmt.Println("CAUTION: call did not abort as expected - investigate cancellation.")
	}
	return aborted
}
