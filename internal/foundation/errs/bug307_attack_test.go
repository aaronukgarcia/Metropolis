package errs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- BUG-307 independent Destructive round (GR#23) ---
// These are permanent regression tests for the three claims in the
// BUG-307 fix (aa978f2): rotate-failure recovery, short-write
// surfacing, and snapshot() Ctx defensive copy. Written by the
// independent attacker (not the author) since the fix commit itself
// carried zero new test coverage.

// TestRegression_RotateFailure_ReopensNotBricked forces the rename in
// rotateLocked to fail (by pre-creating the rotation target path.1 as a
// non-empty directory, which os.Rename refuses to overwrite on every
// platform) and proves the logger recovers: a write AFTER the failed
// rotation must still land on disk, not silently vanish against a
// closed file descriptor.
func TestRegression_RotateFailure_ReopensNotBricked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "engine.ndjson")

	// Block the rotation rename: path -> path.1 fails because path.1
	// already exists as a non-empty directory.
	blockDir := path + ".1"
	if err := os.Mkdir(blockDir, 0o755); err != nil {
		t.Fatalf("setup: mkdir blocker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockDir, "occupied.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: seed blocker dir: %v", err)
	}

	// maxBytes deliberately large: rotation is never triggered by the
	// automatic size check in Log(). This isolates "does the logger
	// recover from a failed rotate" from "does rotation get retried on
	// every subsequent write" (a confound: with a tiny maxBytes, EVERY
	// write after the first would re-attempt — and re-fail — rotation as
	// long as the blocker directory exists, which is a different, noisier
	// claim than the one BUG-307 makes).
	l, err := NewFileLogger(path, 1<<20, 1)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer l.Close()

	// Directly force the exact failure BUG-307 describes: rotateLocked
	// has already Close()'d l.file, and the rename to path.1 then fails
	// (blocked by the pre-created non-empty directory). White-box call,
	// same package, mirroring rotateLocked's own "caller must hold l.mu"
	// contract.
	l.mu.Lock()
	rotateErr := l.rotateLocked()
	l.mu.Unlock()
	if rotateErr == nil {
		t.Fatalf("expected the blocked rename to fail rotateLocked, got nil error")
	}

	// This is the actual attack. Without recoverAfterRotateFailure,
	// l.file/l.w still point at the file Close()'d at the top of
	// rotateLocked — this write would either silently no-op or return
	// "file already closed", and NEVER reach disk. With the fix,
	// rotateLocked reopens path best-effort, so this write must succeed
	// and be readable back from path.
	err2 := l.Log(Entry{Code: "MET-TEST2", Level: "info", Module: "test", Msg: "two"})
	if err2 != nil {
		t.Fatalf("post-rotate-failure write must succeed via reopen, got error: %v", err2)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading recovered log file: %v", err)
	}
	if !strings.Contains(string(data), "MET-TEST2") {
		t.Fatalf("recovered write did not land on disk; file contents: %q", string(data))
	}
}

// shortWriter always writes one byte fewer than requested, with no
// error — the classic silent-truncation shape os.File.Write can produce
// under real disk-full/quota conditions.
type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

// TestRegression_ShortWrite_IsSurfaced proves Log() treats n != len(line)
// as a reportable failure rather than silently accepting a truncated
// NDJSON line (which would corrupt the file for every downstream
// tail/parse consumer).
func TestRegression_ShortWrite_IsSurfaced(t *testing.T) {
	l := NewLogger(shortWriter{})
	err := l.Log(Entry{Code: "MET-TEST3", Level: "info", Module: "test", Msg: "short"})
	if err == nil {
		t.Fatalf("expected a short-write error, got nil")
	}
	if !strings.Contains(err.Error(), "short write") {
		t.Fatalf("expected a short-write-labelled error, got: %v", err)
	}
}

// TestRegression_Snapshot_CtxNotAliased proves ringBuffer.snapshot()
// returns entries whose Ctx map is an independent copy: mutating the
// map obtained from one Recent() call must NOT be visible through a
// second Recent() call (i.e. must not corrupt the live ring slot).
func TestRegression_Snapshot_CtxNotAliased(t *testing.T) {
	ring.push(Entry{
		Code:   "MET-TESTCTX",
		Level:  "info",
		Module: "test",
		Msg:    "ctx-alias-probe",
		Ctx:    map[string]any{"key": "original"},
	})

	first := Recent()
	var got *Entry
	for i := range first {
		if first[i].Code == "MET-TESTCTX" {
			got = &first[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("seeded entry MET-TESTCTX not found in Recent()")
	}

	// Mutate the caller-side copy.
	got.Ctx["key"] = "MUTATED"

	second := Recent()
	for i := range second {
		if second[i].Code == "MET-TESTCTX" {
			if second[i].Ctx["key"] != "original" {
				t.Fatalf("live ring entry was corrupted by a caller mutating a snapshot Ctx map: got %v", second[i].Ctx["key"])
			}
			return
		}
	}
	t.Fatalf("seeded entry MET-TESTCTX not found on second Recent() call")
}
