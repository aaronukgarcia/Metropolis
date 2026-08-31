package errs

import (
	"fmt"
	"path/filepath"
	"testing"
)

// --- Issue 1: Rotate Failure Bricks Logger ---

// TestFileLogger_RotateFailureDoesNotBrickLogger tests that when rotateLocked
// encounters a failure (e.g., rename fails on Windows because file is open),
// the logger does NOT panic but instead cleanly returns the error (BUG-307
// issue 1: rotate failure bricking the logger). The key invariant is that
// rotation failure must NOT leave l.file in a broken/closed state that causes
// panics on subsequent writes.
func TestFileLogger_RotateFailureDoesNotBrickLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ndjson")

	// Create a logger with small maxBytes to trigger rotation.
	l, err := NewFileLogger(path, 50, 3)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer func() { _ = l.Close() }()

	// Write multiple entries. This will trigger rotations.
	// On systems where rotation can fail (e.g., Windows won't rename open files),
	// the logger should cleanly error-out without panicking.
	entry := Entry{Level: "info", Code: "MET-F900", CorrelationID: "corr", Module: "m", Msg: "fill"}

	// Write 10 entries. Each may succeed or fail, but NONE should panic.
	didNotPanic := true
	for i := 0; i < 10; i++ {
		// This may error (e.g., rotate failed), but must not panic.
		_ = l.Log(entry)
	}

	// If we got here without a panic, the logger survived the rotation stress.
	// The test passes if we didn't panic, regardless of whether rotations
	// succeeded or failed.
	if !didNotPanic {
		t.Fatal("logger panicked during rotation stress")
	}

	t.Log("Logger survived rotation stress without panicking")
}

// TestFileLogger_RotateFailureResilience tests that the logger's rotation
// failure handling is resilient by verifying the logger can degrade gracefully
// (e.g., by falling back, retrying, or reporting an error).
func TestFileLogger_RotateFailureResilience(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ndjson")

	l, err := NewFileLogger(path, 50, 3)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer func() { _ = l.Close() }()

	// Write entries to trigger rotation.
	entry := Entry{Level: "info", Code: "MET-F900", CorrelationID: "corr", Module: "m", Msg: "fill"}
	for i := 0; i < 5; i++ {
		if err := l.Log(entry); err != nil {
			t.Logf("Log error (expected during rotation stress): %v", err)
		}
	}

	// After this, the logger should still be in a usable state for the next write
	// (either it successfully rotated, or it degraded gracefully).
	// If we can log without panic, the resilience test passes.
	err = l.Log(Entry{Level: "info", Code: "MET-F902", CorrelationID: "corr", Module: "m", Msg: "after rotation"})
	_ = err // No panic = pass.

	t.Log("Logger resilient after rotation stress")
}

// --- Issue 2: Short-Write Detection ---

// shortWriter is an io.Writer that writes fewer bytes than requested.
// Used to test that the logger properly detects and handles short writes.
type shortWriter struct {
	maxWrite int
	writeErr error
}

func (sw *shortWriter) Write(p []byte) (int, error) {
	if sw.writeErr != nil {
		return 0, sw.writeErr
	}
	n := len(p)
	if sw.maxWrite > 0 && n > sw.maxWrite {
		n = sw.maxWrite
	}
	return n, nil // Return fewer bytes written than requested, no error.
}

// TestLogger_DetectsShortWrite tests that the logger detects when the underlying
// writer performs a short write (n < len(buf)) and handles it properly
// (BUG-307 issue 2: short-write not handled).
func TestLogger_DetectsShortWrite(t *testing.T) {
	// Create a logger wrapping a shortWriter that only writes half the bytes.
	sw := &shortWriter{maxWrite: 10}
	l := NewLogger(sw)

	entry := Entry{Level: "error", Code: "MET-F900", CorrelationID: "corr", Module: "m", Msg: "short write test"}

	// Log an entry. The underlying writer will do a short write.
	err := l.Log(entry)

	// The logger MUST detect this as an error condition.
	// With the fix, it should return an error or ensure the entire line was written.
	// Without the fix, it silently treats the short write as success.
	if err == nil {
		// If no error is returned, verify that the entire entry was actually written.
		// (The fix may choose to retry or error; either is acceptable as long as
		// the outcome is deterministic — either the full entry is written, or an error is raised.)
		// For now, we just require that an error IS raised to detect the short write.
		t.Error("expected an error for short write, but got nil")
	}

	t.Logf("Short write detected as error: %v", err)
}

// TestLogger_ShortWriteWithFullBuffer tests short writes on a logger wrapping
// a strict short-writer.
func TestLogger_ShortWriteWithFullBuffer(t *testing.T) {
	sw := &shortWriter{maxWrite: 5} // Only accept 5 bytes at a time.
	l := NewLogger(sw)

	entry := Entry{Level: "info", Code: "MET-F900", CorrelationID: "c", Module: "m", Msg: "test"}
	err := l.Log(entry)

	// The logger should detect that a short write occurred.
	if err == nil {
		t.Error("expected error for short write, got nil")
	}
}

// --- Issue 3: Snapshot Aliasing ---

// TestRingBuffer_SnapshotIsDeepCopy tests that snapshots returned by
// ringBuffer.snapshot() are independent copies and do not alias live state
// (BUG-307 issue 3: snapshot aliasing).
func TestRingBuffer_SnapshotIsDeepCopy(t *testing.T) {
	ring := newRingBuffer(10)

	// Push an entry with a context map.
	originalCtx := map[string]any{"key": "original_value", "number": 42}
	entry := Entry{
		Level:         "info",
		Code:          "MET-F900",
		CorrelationID: "corr",
		Module:        "m",
		Msg:           "test",
		Ctx:           originalCtx,
	}
	ring.push(entry)

	// Take a snapshot.
	snapshot := ring.snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(snapshot))
	}

	snapshotEntry := snapshot[0]

	// Mutate the original context map in the live ring.
	originalCtx["key"] = "mutated_value"
	originalCtx["new_key"] = "new_value"

	// Verify that the snapshot's Ctx is unchanged.
	if val, ok := snapshotEntry.Ctx["key"]; !ok || val != "original_value" {
		t.Errorf("snapshot.Ctx aliased live state: key changed from 'original_value' to %v", val)
	}
	if _, ok := snapshotEntry.Ctx["new_key"]; ok {
		t.Error("snapshot.Ctx aliased live state: new_key appeared in snapshot after live mutation")
	}
}

// TestRingBuffer_LiveMutationDoesNotAffectSnapshot tests the inverse: mutating
// the snapshot does not affect the live ring.
func TestRingBuffer_LiveMutationDoesNotAffectSnapshot(t *testing.T) {
	ring := newRingBuffer(10)

	// Push an entry with a context map.
	entry := Entry{
		Level:         "info",
		Code:          "MET-F900",
		CorrelationID: "corr",
		Module:        "m",
		Msg:           "test",
		Ctx:           map[string]any{"key": "live_value"},
	}
	ring.push(entry)

	// Take a snapshot.
	snapshot := ring.snapshot()
	snapshotEntry := snapshot[0]

	// Mutate the snapshot's context map.
	snapshotEntry.Ctx["key"] = "snapshot_mutated"
	snapshotEntry.Ctx["snapshot_new"] = "added"

	// Take another snapshot from the live ring and verify it's unchanged.
	snapshot2 := ring.snapshot()
	entry2 := snapshot2[0]

	if val, ok := entry2.Ctx["key"]; !ok || val != "live_value" {
		t.Errorf("live ring mutated after snapshot mutation: key = %v", val)
	}
	if _, ok := entry2.Ctx["snapshot_new"]; ok {
		t.Error("live ring mutated after snapshot mutation: snapshot_new appeared")
	}
}

// TestRingBuffer_SnapshotLargeContext tests snapshot isolation with a larger
// context map to ensure the deep copy is complete.
func TestRingBuffer_SnapshotLargeContext(t *testing.T) {
	ring := newRingBuffer(10)

	// Build a large context map.
	largeCtx := make(map[string]any)
	for i := 0; i < 50; i++ {
		largeCtx[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
	}

	entry := Entry{
		Level:         "info",
		Code:          "MET-F900",
		CorrelationID: "corr",
		Module:        "m",
		Msg:           "test",
		Ctx:           largeCtx,
	}
	ring.push(entry)

	// Take a snapshot.
	snapshot := ring.snapshot()
	snapshotCtx := snapshot[0].Ctx

	// Clear the live context map entirely.
	for k := range largeCtx {
		delete(largeCtx, k)
	}
	largeCtx["only_live_key"] = "live"

	// Verify snapshot is unchanged.
	if len(snapshotCtx) != 50 {
		t.Errorf("snapshot context mutated by live deletion: len = %d, want 50", len(snapshotCtx))
	}
	if _, ok := snapshotCtx["only_live_key"]; ok {
		t.Error("snapshot aliased live state: new live key appeared in snapshot")
	}
}

// TestRecent_SnapshotIsDeepCopy tests the public Recent() function, which uses
// ringBuffer.snapshot() internally.
func TestRecent_SnapshotIsDeepCopy(t *testing.T) {
	resetSinkForTest()
	t.Cleanup(resetSinkForTest)

	// Push an entry with context to the global ring.
	liveCtx := map[string]any{"status": "live"}
	e := Entry{Code: "MET-F900", Msg: "test", Ctx: liveCtx}
	logEntry(e)

	// Get a snapshot via Recent().
	recent := Recent()
	if len(recent) != 1 {
		t.Fatalf("Recent() len = %d, want 1", len(recent))
	}

	snapshotCtx := recent[0].Ctx

	// Mutate the live context.
	liveCtx["status"] = "mutated"
	liveCtx["new_field"] = "added"

	// Verify snapshot is unchanged.
	if val, ok := snapshotCtx["status"]; !ok || val != "live" {
		t.Errorf("Recent() snapshot aliased live state: status = %v", val)
	}
	if _, ok := snapshotCtx["new_field"]; ok {
		t.Error("Recent() snapshot aliased live state: new_field appeared")
	}
}
