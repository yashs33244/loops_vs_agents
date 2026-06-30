package wal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"sgh/engine/plan"
)

// newLogs returns the two Log implementations under test, each as a factory so
// every subtest gets a fresh instance. FileLog factories use t.TempDir().
func logFactories(t *testing.T) map[string]func() Log {
	t.Helper()
	return map[string]func() Log{
		"FileLog": func() Log {
			path := filepath.Join(t.TempDir(), "wal.jsonl")
			fl, err := NewFileLog(path)
			if err != nil {
				t.Fatalf("NewFileLog: %v", err)
			}
			t.Cleanup(func() { _ = fl.Close() })
			return fl
		},
		"MemLog": func() Log {
			return NewMemLog()
		},
	}
}

func mkEntry(runID, nodeID string, seq int, from, to plan.NodeState) Entry {
	return Entry{
		RunID:   runID,
		NodeID:  nodeID,
		Trigger: "test",
		Seq:     seq,
		TS:      "2026-06-30T00:00:00Z",
		From:    from,
		To:      to,
		Payload: json.RawMessage(`{"k":"v"}`),
	}
}

// TestRoundTrip appends a sequence of entries and confirms Replay returns them
// verbatim and in Seq order, for both implementations.
func TestRoundTrip(t *testing.T) {
	for name, factory := range logFactories(t) {
		t.Run(name, func(t *testing.T) {
			log := factory()

			want := []Entry{
				mkEntry("run-1", "n1", 0, plan.StatePending, plan.StateReady),
				mkEntry("run-1", "n1", 1, plan.StateReady, plan.StateRunning),
				mkEntry("run-1", "n2", 2, plan.StatePending, plan.StateReady),
				mkEntry("run-1", "n1", 3, plan.StateRunning, plan.StateExecuted),
			}
			for _, e := range want {
				if err := log.Append(e); err != nil {
					t.Fatalf("Append(%+v): %v", e, err)
				}
			}

			got, err := log.Replay("run-1")
			if err != nil {
				t.Fatalf("Replay: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Replay mismatch\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

// TestFilterByRunID interleaves two runs and confirms Replay returns only the
// requested run, in Seq order.
func TestFilterByRunID(t *testing.T) {
	for name, factory := range logFactories(t) {
		t.Run(name, func(t *testing.T) {
			log := factory()

			// Interleaved write order across two runs.
			appendOrder := []Entry{
				mkEntry("run-a", "a1", 0, plan.StatePending, plan.StateReady),
				mkEntry("run-b", "b1", 0, plan.StatePending, plan.StateReady),
				mkEntry("run-a", "a2", 1, plan.StatePending, plan.StateReady),
				mkEntry("run-b", "b2", 1, plan.StatePending, plan.StateReady),
				mkEntry("run-a", "a1", 2, plan.StateReady, plan.StateRunning),
			}
			for _, e := range appendOrder {
				if err := log.Append(e); err != nil {
					t.Fatalf("Append: %v", err)
				}
			}

			gotA, err := log.Replay("run-a")
			if err != nil {
				t.Fatalf("Replay run-a: %v", err)
			}
			if len(gotA) != 3 {
				t.Fatalf("run-a: got %d entries, want 3: %+v", len(gotA), gotA)
			}
			for _, e := range gotA {
				if e.RunID != "run-a" {
					t.Fatalf("run-a: got entry from run %q: %+v", e.RunID, e)
				}
			}

			gotB, err := log.Replay("run-b")
			if err != nil {
				t.Fatalf("Replay run-b: %v", err)
			}
			if len(gotB) != 2 {
				t.Fatalf("run-b: got %d entries, want 2: %+v", len(gotB), gotB)
			}
			for _, e := range gotB {
				if e.RunID != "run-b" {
					t.Fatalf("run-b: got entry from run %q: %+v", e.RunID, e)
				}
			}

			if got, err := log.Replay("missing"); err != nil || len(got) != 0 {
				t.Fatalf("Replay missing: got %+v err %v, want empty/nil", got, err)
			}
		})
	}
}

// TestReplayOrdersBySeq appends entries out of Seq order and confirms Replay
// sorts them ascending by Seq.
func TestReplayOrdersBySeq(t *testing.T) {
	for name, factory := range logFactories(t) {
		t.Run(name, func(t *testing.T) {
			log := factory()

			// Append in scrambled Seq order.
			for _, seq := range []int{3, 0, 4, 1, 2} {
				if err := log.Append(mkEntry("run-1", "n", seq, plan.StatePending, plan.StateReady)); err != nil {
					t.Fatalf("Append: %v", err)
				}
			}

			got, err := log.Replay("run-1")
			if err != nil {
				t.Fatalf("Replay: %v", err)
			}
			for i, e := range got {
				if e.Seq != i {
					t.Fatalf("entry %d has Seq %d, want %d; full: %+v", i, e.Seq, i, got)
				}
			}
		})
	}
}

// TestTornFinalLine writes a valid log, then appends a partial/garbage final
// line (simulating a crash mid-write) and confirms FileLog.Replay still returns
// the valid entries and skips the torn tail.
func TestTornFinalLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.jsonl")

	fl, err := NewFileLog(path)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	want := []Entry{
		mkEntry("run-1", "n1", 0, plan.StatePending, plan.StateReady),
		mkEntry("run-1", "n2", 1, plan.StateReady, plan.StateRunning),
	}
	for _, e := range want {
		if err := fl.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	// Simulate a crash that left a partial JSON record (no trailing newline).
	if err := fl.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("reopen for torn write: %v", err)
	}
	if _, err := raw.WriteString(`{"RunID":"run-1","NodeID":"n3","Seq":2,"To":"ru`); err != nil {
		t.Fatalf("torn write: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close torn writer: %v", err)
	}

	// A fresh reader over the corrupted file must tolerate the torn tail.
	rl, err := NewFileLog(path)
	if err != nil {
		t.Fatalf("NewFileLog (reopen): %v", err)
	}
	defer rl.Close()

	got, err := rl.Replay("run-1")
	if err != nil {
		t.Fatalf("Replay over torn log: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("torn-line Replay mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

// TestAppendAfterClose confirms FileLog rejects appends after Close and that
// Close is idempotent.
func TestAppendAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.jsonl")
	fl, err := NewFileLog(path)
	if err != nil {
		t.Fatalf("NewFileLog: %v", err)
	}
	if err := fl.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := fl.Close(); err != nil {
		t.Fatalf("second Close should be a no-op, got: %v", err)
	}
	if err := fl.Append(mkEntry("run-1", "n1", 0, plan.StatePending, plan.StateReady)); err == nil {
		t.Fatalf("Append after Close should error")
	}
}

// TestEmptyReplay confirms replaying an empty log yields no entries and no error.
func TestEmptyReplay(t *testing.T) {
	for name, factory := range logFactories(t) {
		t.Run(name, func(t *testing.T) {
			log := factory()
			got, err := log.Replay("run-1")
			if err != nil {
				t.Fatalf("Replay empty: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("Replay empty: got %+v, want none", got)
			}
		})
	}
}
