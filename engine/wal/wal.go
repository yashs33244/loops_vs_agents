package wal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"

	"sgh/engine/plan"
)

// Entry is one append-only WAL record: a single node state transition plus
// metadata. One Entry is written per line in the JSONL log.
type Entry struct {
	RunID, NodeID, Trigger string
	Seq                    int
	TS                     string
	From, To               plan.NodeState
	Payload                json.RawMessage
}

// Log is the append-only write-ahead log abstraction.
type Log interface {
	Append(e Entry) error
	Replay(runID string) ([]Entry, error)
	Close() error
}

// FileLog is the durable, append-only JSONL implementation of Log. Each Append
// marshals one Entry to a single line and appends it to the file (decision D3).
// Replay reads the whole file back and is tolerant of a torn/partial final line
// left behind by a crash mid-write.
type FileLog struct {
	// Path is the on-disk JSONL file path.
	Path string

	// mu guards the file handle. The scheduler is a single-writer loop, but we
	// still guard the handle so concurrent Append/Replay/Close calls cannot
	// race on the *os.File (and so tests pass under -race).
	mu sync.Mutex
	f  *os.File
}

// NewFileLog opens (or creates) an append-only JSONL log at path. The file is
// opened O_APPEND so every write lands at the end, and created with mode 0o644
// if it does not yet exist.
func NewFileLog(path string) (*FileLog, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("wal: open %q: %w", path, err)
	}
	return &FileLog{Path: path, f: f}, nil
}

// Append writes e as one JSON line (the record followed by a newline). It is
// safe for the single-writer caller: the file handle is guarded by mu.
func (f *FileLog) Append(e Entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("wal: marshal entry: %w", err)
	}
	b = append(b, '\n')

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.f == nil {
		return fmt.Errorf("wal: append to closed log %q", f.Path)
	}
	if _, err := f.f.Write(b); err != nil {
		return fmt.Errorf("wal: write entry: %w", err)
	}
	return nil
}

// Replay reads every line of the log, unmarshals each one, and returns the
// entries whose RunID matches runID, ordered by Seq. A trailing line that fails
// to unmarshal (a torn final write from a crash) is skipped rather than
// surfaced as an error; any non-final line that fails to parse is a real
// corruption and is reported.
func (f *FileLog) Replay(runID string) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	rf, err := os.Open(f.Path)
	if err != nil {
		return nil, fmt.Errorf("wal: open for replay %q: %w", f.Path, err)
	}
	defer rf.Close()

	// Collect raw lines first so we can tell which parse failure is the
	// (tolerable) trailing torn line versus a (fatal) interior corruption.
	var lines [][]byte
	sc := bufio.NewScanner(rf)
	// Allow long payload lines (default token limit is 64 KiB).
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		// Copy: Scanner reuses its buffer between Scan calls.
		line := append([]byte(nil), sc.Bytes()...)
		lines = append(lines, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("wal: scan %q: %w", f.Path, err)
	}

	var out []Entry
	for i, line := range lines {
		if len(line) == 0 {
			continue // skip blank lines
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			// Tolerate only a torn final line (last non-empty record).
			if i == len(lines)-1 {
				break
			}
			return nil, fmt.Errorf("wal: corrupt entry at line %d in %q: %w", i+1, f.Path, err)
		}
		if e.RunID == runID {
			out = append(out, e)
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// Close closes the underlying file handle. It is idempotent: closing an already
// closed log is a no-op.
func (f *FileLog) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.f == nil {
		return nil
	}
	err := f.f.Close()
	f.f = nil
	if err != nil {
		return fmt.Errorf("wal: close %q: %w", f.Path, err)
	}
	return nil
}

// MemLog is an in-memory implementation of Log for tests.
type MemLog struct {
	mu sync.Mutex
	// entries holds appended records in write order.
	entries []Entry
}

// NewMemLog returns an empty in-memory log.
func NewMemLog() *MemLog { return &MemLog{} }

// Append records e in memory.
func (m *MemLog) Append(e Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
	return nil
}

// Replay returns all in-memory entries for runID, ordered by Seq.
func (m *MemLog) Replay(runID string) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Entry
	for _, e := range m.entries {
		if e.RunID == runID {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// Close is a no-op for the in-memory log.
func (m *MemLog) Close() error { return nil }

// Compile-time checks that each log satisfies the interface.
var (
	_ Log = (*FileLog)(nil)
	_ Log = (*MemLog)(nil)
)
