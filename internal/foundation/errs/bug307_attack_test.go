package errs

import (
	"errors"
	"path/filepath"
	"testing"
)

// shortWriter is an io.Writer that reports a successful write of at most
// max bytes, silently dropping the rest — the exact shape of a misbehaving
// Writer that would otherwise pass an unchecked n==len(line) test.
type shortWriter struct{ max int }

func (s *shortWriter) Write(p []byte) (int, error) {
	if len(p) > s.max {
		return s.max, nil
	}
	return len(p), nil
}

// TestBug307_ShortWriteDetected proves Log no longer silently accepts a
// short write: an io.Writer that reports n < len(line) with no error must
// surface as an error, never an under-counted, silently-truncated entry.
func TestBug307_ShortWriteDetected(t *testing.T) {
	l := NewLogger(&shortWriter{max: 3})
	err := l.Log(Entry{Code: "MET-X", Msg: "this line is far longer than three bytes"})
	if err == nil {
		t.Fatal("Log accepted a short write without error")
	}
}

// TestBug307_SnapshotClonesCtx proves snapshot() returns a defensive copy of
// each Entry's Ctx: mutating the returned map must not corrupt the ring's
// stored audit entry.
func TestBug307_SnapshotClonesCtx(t *testing.T) {
	r := newRingBuffer(8)
	r.push(Entry{Code: "MET-A", Ctx: map[string]any{"k": "v"}})

	snap := r.snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(snap))
	}
	snap[0].Ctx["k"] = "mutated"

	again := r.snapshot()
	if again[0].Ctx["k"] != "v" {
		t.Fatalf("snapshot Ctx aliases the ring's live entry: got %q, want %q", again[0].Ctx["k"], "v")
	}
}

// TestBug307_RotateFailureDoesNotBrickLogger proves a rotate failure (the
// rename chain or reopen failing AFTER l.file.Close) recovers by reopening
// the path, so the audit trail stays usable instead of writing to a closed
// file forever.
func TestBug307_RotateFailureDoesNotBrickLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.ndjson")

	l, err := NewFileLogger(path, 1024, 3)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer func() { _ = l.Close() }()

	// Simulate rotateLocked's pre-rename Close: the file is now closed but
	// l.w still points at it — exactly the bricked state a mid-rotate
	// failure used to leave behind.
	if err := l.file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	cause := errors.New("rename blocked by AV lock")
	if got := l.recoverAfterRotateFailure(cause); !errors.Is(got, cause) {
		t.Fatalf("recoverAfterRotateFailure returned %v, want the original cause", got)
	}

	// The logger must be usable again — a subsequent Log writes successfully
	// to the reopened file rather than erroring on the closed one.
	if err := l.Log(Entry{Code: "MET-X", Msg: "after recovery"}); err != nil {
		t.Fatalf("logger bricked after rotate failure: %v", err)
	}
}
