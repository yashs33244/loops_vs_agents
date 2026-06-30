// Command sgh is the Graph Harness CLI. It loads a plan, runs it through the
// scheduler with a chosen provider, and prints the per-round ready-set and the
// final node states (spec "Definition of done" #2).
//
// Usage:
//
//	sgh run <plan.json> --provider mock|claude|gemini
//
// STUB: arg parsing is wired; execution is not implemented yet.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sgh:", err)
		os.Exit(1)
	}
}

// run parses args and dispatches to the requested subcommand.
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

// runCmd handles: sgh run <plan.json> --provider <name>
func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	providerName := fs.String("provider", "mock", "provider backend: mock|claude|gemini")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("run: missing <plan.json>")
	}
	planPath := rest[0]

	// STUB: load plan, build provider/executor, call scheduler.Run, print trace.
	fmt.Printf("sgh run: plan=%q provider=%q\n", planPath, *providerName)
	fmt.Println("not implemented")
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sgh run <plan.json> --provider mock|claude|gemini")
}
