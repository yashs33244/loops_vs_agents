package wal

import (
	"encoding/json"
	"errors"

	"sgh/engine/plan"
)

// errNotImplemented is the placeholder error returned by skeleton methods that
// must compile and return a value instead of panicking.
var errNotImplemented = errors.New("not implemented")

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

// FileLog is the durable, append-only JSONL implementation of Log.
//
// STUB: file handle/config fields are filled in by the implementer.
type FileLog struct {
	// Path is the on-disk JSONL file path.
	Path string
}

// NewFileLog opens (or creates) an append-only JSONL log at path.
//
// STUB: not implemented yet.
func NewFileLog(path string) (*FileLog, error) {
	return nil, errNotImplemented
}

// Append writes e as one JSON line.
//
// STUB: not implemented yet.
func (f *FileLog) Append(e Entry) error { return errNotImplemented }

// Replay reads back all entries for runID in write order.
//
// STUB: not implemented yet.
func (f *FileLog) Replay(runID string) ([]Entry, error) { return nil, errNotImplemented }

// Close flushes and closes the underlying file.
//
// STUB: not implemented yet.
func (f *FileLog) Close() error { return errNotImplemented }

// MemLog is an in-memory implementation of Log for tests.
//
// STUB: backing storage fields are filled in by the implementer.
type MemLog struct {
	// entries holds appended records in write order.
	entries []Entry
}

// NewMemLog returns an empty in-memory log.
func NewMemLog() *MemLog { return &MemLog{} }

// Append records e in memory.
//
// STUB: not implemented yet.
func (m *MemLog) Append(e Entry) error { return errNotImplemented }

// Replay returns all in-memory entries for runID in write order.
//
// STUB: not implemented yet.
func (m *MemLog) Replay(runID string) ([]Entry, error) { return nil, errNotImplemented }

// Close is a no-op for the in-memory log.
//
// STUB: not implemented yet.
func (m *MemLog) Close() error { return errNotImplemented }

// Compile-time checks that each log satisfies the interface.
var (
	_ Log = (*FileLog)(nil)
	_ Log = (*MemLog)(nil)
)
