// Command sgh is the Graph Harness CLI. It loads a plan (a DAG of nodes),
// validates it, runs it through the single-writer scheduler with a chosen
// provider, and prints the transition trace plus the final node states and the
// peak parallelism observed.
//
// Usage:
//
//	sgh run <plan.json> [--provider mock|claude|gemini] [--max-parallel N] [--rate R] [--timeout D]
//
// Providers:
//
//	mock   - every node "succeeds" with no I/O (deterministic; default)
//	claude - each node is a `claude -p` call (Claude Code CLI must be on PATH)
//	gemini - each node is a Gemini API call (GEMINI_API_KEY must be set)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"sgh/engine/node"
	"sgh/engine/plan"
	"sgh/engine/provider"
	"sgh/engine/recovery"
	"sgh/engine/scheduler"
	"sgh/engine/validate"
	"sgh/engine/wal"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sgh:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("missing subcommand")
	}
	switch args[0] {
	case "run":
		return runCmd(args[1:])
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	providerName := fs.String("provider", "mock", "provider backend: mock|claude|gemini")
	maxParallel := fs.Int("max-parallel", 0, "worker pool cap (0 = number of nodes)")
	rate := fs.Float64("rate", 0, "provider calls per second (0 = unlimited)")
	timeout := fs.Duration("timeout", 5*time.Minute, "overall run timeout")
	// Accept the plan path either before or after the flags. Go's flag package
	// stops at the first non-flag arg, so if the path comes first we peel it off
	// before parsing the remaining flags.
	var planPath string
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		planPath, flagArgs = args[0], args[1:]
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if planPath == "" {
		planPath = fs.Arg(0)
	}
	if planPath == "" {
		return fmt.Errorf("run: missing <plan.json>")
	}

	data, err := os.ReadFile(planPath)
	if err != nil {
		return fmt.Errorf("read plan: %w", err)
	}
	p, err := plan.FromJSON(data)
	if err != nil {
		return err
	}

	// Validate: field-level, then the 5 structural DAG checks.
	if err := p.BasicValidate(); err != nil {
		return fmt.Errorf("invalid plan: %w", err)
	}
	if errs := validate.Check(p); len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "plan failed validation:")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %v\n", e)
		}
		return fmt.Errorf("%d validation error(s)", len(errs))
	}

	exec, err := buildExecutor(*providerName)
	if err != nil {
		return err
	}

	log := wal.NewMemLog()
	pol := recovery.NewStandardPolicy(2, 1)
	runID := fmt.Sprintf("%s-%s", p.ID, *providerName)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	fmt.Printf("Plan %q v%d  -  %d nodes, %d edges, provider=%s\n\n", p.ID, p.Version, len(p.Nodes), len(p.Edges), *providerName)

	out, err := scheduler.Run(ctx, p, exec, log, pol, scheduler.Options{
		MaxParallel: *maxParallel,
		RatePerSec:  *rate,
		RunID:       runID,
	})
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}

	entries, _ := log.Replay(runID)
	fmt.Printf("Transition trace (%d events):\n", len(entries))
	for _, e := range entries {
		fmt.Printf("  #%-3d %-13s %-16s -> %-16s %s\n", e.Seq, e.NodeID, e.From, e.To, e.Trigger)
	}

	fmt.Println("\nFinal states:")
	ids := make([]string, 0, len(out.Final))
	for id := range out.Final {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Printf("  %-13s %s\n", id, out.Final[id])
	}

	fmt.Printf("\nrounds=%d   peak parallelism |U|=%d   succeeded=%v\n", out.Rounds, out.MaxReady, out.Succeeded)
	if !out.Succeeded {
		return fmt.Errorf("run did not succeed (a node ended failed/cancelled)")
	}
	return nil
}

// buildExecutor wires the chosen provider into a node executor.
func buildExecutor(name string) (node.Executor, error) {
	switch name {
	case "mock":
		// Every node succeeds with empty-JSON output; no network, deterministic.
		return node.NewMockExecutorFunc(func(n plan.Node, inputs map[string]string) node.Result {
			return node.Result{Output: "{}"}
		}), nil
	case "claude":
		return node.NewLLMExecutor(provider.NewClaudeCLIProvider("claude")), nil
	case "gemini":
		gp, err := provider.NewGeminiAPIProvider()
		if err != nil {
			return nil, fmt.Errorf("gemini provider: %w", err)
		}
		return node.NewLLMExecutor(gp), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (use mock|claude|gemini)", name)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sgh run <plan.json> [--provider mock|claude|gemini] [--max-parallel N] [--rate R] [--timeout D]")
}
