// Command spike is the M0 GO/NO-GO probe for Graph Harness (SGH).
//
// It answers the project's load-bearing question BEFORE any engine code:
// can the Claude Code CLI and the Gemini CLI act as clean "completion nodes"?
// Specifically:
//   - strict JSON out:  does `<cli> -p <prompt>` reliably return parseable JSON?
//   - concurrency:       can N calls run in parallel without contaminating each other?
//   - cancellation:      does a context timeout actually kill the process group?
//   - cost/latency:      best-effort wall-clock per call.
//
// Throwaway by design. Stdlib only. Run: go run . [-n 5] [-conc 4] [-providers claude,gemini]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A deliberately trivial, tool-free task: the model only has to emit strict JSON.
// If a CLI can't do this cleanly, it can't be a deterministic node executor.
const prompt = `Respond with ONLY a single-line minified JSON object of the form {"answer": N} where N is an integer, and nothing else - no prose, no markdown, no code fences. Question: what is 6 * 7?`

type provider struct {
	name string
	bin  string
	args func(p string) []string
}

func providers() []provider {
	return []provider{
		{name: "claude", bin: "claude", args: func(p string) []string { return []string{"-p", p} }},
		{name: "gemini", bin: "gemini", args: func(p string) []string { return []string{"-p", p} }},
	}
}

type result struct {
	Provider   string `json:"provider"`
	Index      int    `json:"index"`
	DurationMS int64  `json:"duration_ms"`
	ExitCode   int    `json:"exit_code"`
	JSONOK     bool   `json:"json_ok"`    // got a JSON object with an "answer" field
	JSONClean  bool   `json:"json_clean"` // parsed without needing substring extraction
	Correct    bool   `json:"correct"`    // answer == 42
	Answer     *int   `json:"answer,omitempty"`
	ErrMsg     string `json:"err,omitempty"`
	StdoutHead string `json:"stdout_head,omitempty"`
}

// parseAnswer tries a strict parse first, then a forgiving "first { .. last }"
// extraction (which would mean the model wrapped the JSON in prose/fences).
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

// runCmd runs an isolated subprocess with its own process group so that a
// context-timeout cancellation kills the whole tree, not just the parent.
func runCmd(ctx context.Context, bin string, args []string, dir string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // negative pid = process group
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

func runOne(p provider, idx int, timeout time.Duration) result {
	r := result{Provider: p.name, Index: idx}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	dir, derr := os.MkdirTemp("", "sgh-spike-")
	if derr == nil {
		defer os.RemoveAll(dir)
	}

	start := time.Now()
	stdout, stderr, code, err := runCmd(ctx, p.bin, p.args(prompt), dir)
	r.DurationMS = time.Since(start).Milliseconds()
	r.ExitCode = code
	if err != nil {
		r.ErrMsg = trunc(err.Error()+" | stderr: "+stderr, 200)
	}

	r.StdoutHead = trunc(stdout, 160)
	ans, ok, clean := parseAnswer(stdout)
	r.JSONOK, r.JSONClean, r.Answer = ok, clean, ans
	r.Correct = ans != nil && *ans == 42
	return r
}

func main() {
	n := flag.Int("n", 5, "calls per provider")
	conc := flag.Int("conc", 4, "max concurrent calls")
	timeoutS := flag.Int("timeout", 120, "per-call timeout in seconds")
	only := flag.String("providers", "claude,gemini", "comma-separated providers to test")
	flag.Parse()

	want := map[string]bool{}
	for _, s := range strings.Split(*only, ",") {
		want[strings.TrimSpace(s)] = true
	}

	var active []provider
	for _, p := range providers() {
		if !want[p.name] {
			continue
		}
		if _, err := exec.LookPath(p.bin); err != nil {
			fmt.Printf("SKIP %-7s not on PATH (%v)\n", p.name, err)
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
	fmt.Printf("M0 CLI spike: %d call(s)/provider, conc=%d, timeout=%ds, providers=[%s]\n\n",
		*n, *conc, *timeoutS, strings.Join(names, " "))

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
				fmt.Printf("  %-7s #%d  %6dms  exit=%-2d  %s\n", res.Provider, res.Index, res.DurationMS, res.ExitCode, tag)
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
		fmt.Printf("GO: a CLI reliably returns strict JSON (best=%.0f%%) and cancellation works.\n", best*100)
		fmt.Println("    -> proceed to M0 (data model + contracts) on the CLI-provider path.")
	case best >= 0.5:
		fmt.Printf("CAUTION: JSON reliability is moderate (best=%.0f%%).\n", best*100)
		fmt.Println("    -> add a JSON-extraction + retry wrapper or a structured-output flag, then re-run before committing to CLIs.")
	default:
		fmt.Printf("NO-GO: CLIs do not reliably behave as completion nodes (best=%.0f%%).\n", best*100)
		fmt.Println("    -> switch to raw API-key providers before building the engine.")
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
		var jsonOK, clean, correct, nonzero int
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
		fmt.Printf("%-7s n=%d  json=%d/%d (%.0f%%)  clean=%d  correct=%d  exit!=0:%d  latency[min/med/max]=%d/%d/%dms\n",
			p.name, len(rs), jsonOK, len(rs), rate*100, clean, correct, nonzero,
			durs[0], durs[len(durs)/2], durs[len(durs)-1])
		if sampleErr != "" {
			fmt.Printf("        sample err: %s\n", sampleErr)
		}
		if sampleOut != "" {
			fmt.Printf("        sample non-JSON stdout: %s\n", sampleOut)
		}
	}
	return bestRate
}

// cancelTest fires one long-running call with a short timeout and verifies the
// process is killed promptly (i.e. process-group cancellation works).
func cancelTest(p provider) bool {
	fmt.Println("\n=== CANCELLATION TEST ===")
	long := "Write a detailed 1500-word essay about the history of relational databases."
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	dir, _ := os.MkdirTemp("", "sgh-spike-cancel-")
	defer os.RemoveAll(dir)

	start := time.Now()
	_, _, _, err := runCmd(ctx, p.bin, []string{"-p", long}, dir)
	dur := time.Since(start)
	killed := err != nil && dur < 10*time.Second
	fmt.Printf("provider=%s  elapsed=%dms  terminated=%v\n", p.name, dur.Milliseconds(), err != nil)
	if killed {
		fmt.Println("PASS: process terminated promptly on context timeout (group-kill works).")
	} else {
		fmt.Println("CAUTION: process did not terminate as expected - investigate cancellation/WaitDelay.")
	}
	return killed
}
