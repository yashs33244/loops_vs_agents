// Package wal is the append-only write-ahead log that records every node state
// change (decision D3). The log is JSON-lines: one Entry per line, written via
// stdlib encoding/json. Replaying the entries for a run rebuilds the engine's
// state, which is what lets the scheduler recover after a crash.
//
// FileLog is the durable on-disk implementation; MemLog is an in-memory
// implementation for tests. SQLite-backed storage is a v2 upgrade behind the
// same Log interface.
package wal
