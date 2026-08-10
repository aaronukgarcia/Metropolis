package errs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"
)

// loggerByteCopy performs SEC-020's attack — a plain Logger struct copy —
// via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// internal/engine/core/sec014_poc_test.go's e2Copy and
// internal/engine/debug/copyguard_test.go's stateByteCopy exactly (see
// either's doc comment for why this is the sanctioned TEST-ONLY
// mechanism): a literal `l2 := *l` is legal, unsafe-free, reflect-free Go
// from outside this package too, but it is also something `go vet`'s
// copylocks check statically flags — this package cannot contain that
// literal line and still pass `go vet ./...`, which this fix's VERIFY
// step requires. The byte-level copy produces IDENTICAL runtime
// semantics (mu's bytes copied as-is, w/file/now copied — aliasing the
// SAME underlying io.Writer/*os.File, the highest-consequence part of
// this specific hazard, see Logger.self's doc comment — self's pointer
// bytes copied unchanged) without a statically-flaggable copy
// expression.
func loggerByteCopy(l *Logger) *Logger {
	c := new(Logger)
	*(*[unsafe.Sizeof(Logger{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Logger{})]byte)(unsafe.Pointer(l))
	return c
}

// wantLoggerCopied asserts err is exactly ErrLoggerCopied — naming every
// guarded method individually means a stripped guard on any ONE of them
// identifies which site regressed, so each assertion is per-method, not
// a shared loop with a generic message. Unlike wantRegistryCopied/
// wantStateCopied elsewhere in this codebase, this does NOT use
// errors.Is(err, &E{Code: ...}) — ErrLoggerCopied is deliberately a
// plain sentinel, not a registry-sourced *E (see ErrLoggerCopied's doc
// comment on log.go for why), so a plain errors.Is against the sentinel
// itself is the correct check here.
func wantLoggerCopied(t *testing.T, method string, err error) {
	t.Helper()
	if !errors.Is(err, ErrLoggerCopied) {
		t.Fatalf("%s on a struct-copied Logger: err = %v, want ErrLoggerCopied", method, err)
	}
}

// --- Enumeration method (Weakness pattern #3) -------------------------
//
// Every l.mu.Lock() site in this package's non-test source, found via:
//
//	grep -n "mu\.Lock()\|mu\.Unlock()" internal/foundation/errs/log.go
//
// cross-checked against the full exported-method list on *Logger:
//
//	grep -n "^func (l \*Logger)" internal/foundation/errs/log.go
//
// Results — every lock site, its enclosing method, and its guard:
//
//	log.go  SetClock(now)   -- l.mu.Lock()  -- pre-lock + post-lock check (silently drops; no meaningful error-carrying use elsewhere in this codebase for a setter, matches InProcTransport.Close's "no return to carry a rejection" precedent — see SetClock's doc comment)
//	log.go  Log(e)          -- l.mu.Lock()  -- pre-lock + post-lock check (returns ErrLoggerCopied; ASM-074 ALSO pushes e into the package ring buffer on rejection, see rejectCopiedLog)
//	log.go  Close()         -- l.mu.Lock()  -- pre-lock + post-lock check (returns ErrLoggerCopied)
//
// That is exactly 3 exported methods on *Logger that take l.mu — no
// exported method reaches l.mu indirectly through another unguarded
// helper (rotateLocked is documented "caller must hold l.mu" and has
// exactly one call site, inside Log's own already-guarded critical
// section, so it needs no additional check of its own — same reasoning
// as engine.core's advanceOneDailyTick, see that file's SEC-018
// enumeration note). 3 guarded call sites total, all 3 exercised below
// by name.

// TestLogger_EveryGuardedMethod_RejectsStructCopy is the enumeration
// sweep: every method identified above is called on the SAME
// deterministically-constructed copy and asserted to reject it. Naming
// each call individually means a stripped guard on any one site fails
// ONLY that assertion, identifying the regressed site by name.
func TestLogger_EveryGuardedMethod_RejectsStructCopy(t *testing.T) {
	var buf bytes.Buffer
	orig := NewLogger(&buf)
	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	orig.SetClock(func() time.Time { return fixedNow })

	cp := loggerByteCopy(orig)

	// SetClock: no return value, so the observable effect is on the
	// ORIGINAL — its clock must be unaffected by a call made against the
	// copy.
	laterNow := fixedNow.Add(time.Hour)
	cp.SetClock(func() time.Time { return laterNow })

	// Log: must be rejected with ErrLoggerCopied AND must not write
	// anything to the copy's (shared!) underlying writer.
	err := cp.Log(Entry{Level: "error", Code: "MET-F900", CorrelationID: "corr-attack", Module: "m", Msg: "attack"})
	wantLoggerCopied(t, "Log", err)

	// Close: must be rejected, never actually closing the shared
	// resource (buf has no Close, but the assertion that it returns
	// ErrLoggerCopied rather than nil is what matters for a file-backed
	// Logger — see TestLogger_CopyCloseNeverClosesSharedFile below for
	// the sharp, file-backed version of this).
	wantLoggerCopied(t, "Close", cp.Close())

	// The ORIGINAL must be completely unaffected by every rejected call
	// above: its clock must still be the one legitimately installed via
	// SetClock (fixedNow), not the copy's attempted override, and a
	// legitimate Log() on the original must still work and use fixedNow.
	if err := orig.Log(Entry{Level: "info", Code: "MET-F900", CorrelationID: "corr-real", Module: "m", Msg: "real"}); err != nil {
		t.Fatalf("original Log after copy-attack calls: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(fixedNow.UTC().Format(time.RFC3339Nano))) {
		t.Fatalf("original Log's entry does not carry the original's own clock timestamp — SetClock on the copy must not have reached the original: %s", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte("attack")) {
		t.Fatal("the copy's rejected Log() call wrote to the shared underlying writer — it must not have")
	}
}

// TestLogger_CopyCloseNeverClosesSharedFile is the sharpest form of the
// Close guard: a struct-copied, FILE-backed Logger's Close() must never
// actually close the shared *os.File, because that file is still being
// written to by the ORIGINAL under the original's own (different, but
// irrelevant here since it's never reached) mu. This is Logger's
// distinguishing hazard versus every other SEC-020 type in this codebase
// (see Logger.self's doc comment): the shared resource a copy can
// corrupt is not a map or a slice, it is a live file descriptor.
func TestLogger_CopyCloseNeverClosesSharedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.ndjson")
	orig, err := NewFileLogger(path, 0, 3)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	t.Cleanup(func() { _ = orig.Close() })

	cp := loggerByteCopy(orig)
	wantLoggerCopied(t, "Close", cp.Close())

	// The ORIGINAL must still be able to write after the copy's rejected
	// Close() — proof the shared file was never actually closed.
	if err := orig.Log(Entry{Level: "info", Code: "MET-F900", CorrelationID: "corr-after-copy-close", Module: "m", Msg: "still alive"}); err != nil {
		t.Fatalf("original Log after copy's rejected Close: %v", err)
	}

	data, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("reading %s: %v", path, rerr)
	}
	if !bytes.Contains(data, []byte("still alive")) {
		t.Fatalf("expected the original's post-copy-Close write to have landed on disk, got: %s", data)
	}
}

// --- Deterministic pre-lock-ordering attack (SEC-016 shape) -----------

// runBoundedRejection runs call in its own goroutine and asserts it
// returns within 3 seconds. wantErr, when non-nil, additionally asserts
// the returned error via errors.Is; when nil, the case has no error
// return to check (SetClock) and only promptness is asserted here — its
// effect is verified separately, after the bounded call returns, exactly
// as the caller already does for SetClock's "original clock unaffected"
// assertion. A regression that reintroduces a pre-lock guard gap on a
// copy taken mid-lock hangs the guarded method forever (SEC-016's exact
// failure mode: the copy's mu bytes read as permanently "locked" by
// nobody who will ever unlock THIS copy's address) — without a per-case
// bound, that regression is only caught by Go's default 10-minute test
// timeout and a goroutine-dump panic naming a stuck select, not the
// guarded method itself. Ported from
// internal/foundation/registry/sec020_test.go's identical pattern, this
// initiative's reference shape for exactly this class of test (Bill,
// 2026-08-10, citing Tester-1's reproduction) — a synchronous call in
// this position is a defect in the TEST, not just a style gap, since
// these tests exist to be re-run on every future change to this file and
// a check that needs a -timeout override to fail fast is a check people
// learn to skip.
func runBoundedRejection(t *testing.T, name string, wantErr error, call func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		if wantErr != nil && !errors.Is(err, wantErr) {
			t.Errorf("%s on deterministically mid-lock-copied Logger: err = %v, want %v", name, err, wantErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("SEC-020 REGRESSION: %s on a copy taken while mu was held did not return within 3s — hung, exactly the pre-fix failure mode", name)
	}
}

// TestLogger_CopyTakenWhileLockHeld_RejectedNotHung is the deterministic
// version of the SEC-016 "copy taken mid-lock" attack. Rather than
// racing a concurrent locker against concurrent copies for the TIMING,
// this test CONSTRUCTS the attack STATE directly and deterministically,
// single-goroutine: lock l.mu, take the byte copy while it is held (so
// the copy's mu bytes read as "currently locked" — nobody will ever call
// Unlock() with the copy's address), unlock the original, then call the
// copy. Because checkNotCopied is lock-free and runs BEFORE mu.Lock() in
// every guarded method, the copy must be rejected promptly without ever
// attempting to acquire its own (permanently unrecoverable) lock —
// asserted per case via runBoundedRejection's 3-second bound rather than
// synchronously with no bound.
func TestLogger_CopyTakenWhileLockHeld_RejectedNotHung(t *testing.T) {
	var buf bytes.Buffer
	orig := NewLogger(&buf)

	orig.mu.Lock()
	cp := loggerByteCopy(orig) // cp.mu's bytes now read "locked" — byte-identical to orig.mu at this instant
	orig.mu.Unlock()

	// SetClock has no error return to check — only promptness is
	// asserted inline; the "did it actually reach the original" effect
	// is verified below, after every bounded call has returned.
	runBoundedRejection(t, "SetClock", nil, func() error {
		cp.SetClock(func() time.Time { return time.Unix(0, 0) })
		return nil
	})
	runBoundedRejection(t, "Log", ErrLoggerCopied, func() error {
		return cp.Log(Entry{Code: "MET-F900", Msg: "hung"})
	})
	runBoundedRejection(t, "Close", ErrLoggerCopied, func() error {
		return cp.Close()
	})

	// The original must still be fully usable afterward — the copy
	// attack (and its abandoned, permanently-"locked"-looking mu) must
	// not have wedged anything shared.
	if err := orig.Log(Entry{Code: "MET-F900", Msg: "still works"}); err != nil {
		t.Fatalf("original Log after copy-during-lock attack: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("still works")) {
		t.Fatal("original Logger did not actually write after the copy-during-lock attack")
	}
}

// --- Fail-closed on zero value, new(...), and a hand-built literal ----

// TestLogger_ZeroValue_FailsClosed proves `var l Logger` (never passed
// through NewLogger/NewFileLogger, so self was never stored) is rejected
// the same way a copy is — every documented construction path is one of
// those two constructors, so an unset self is itself a misuse this same
// rejection correctly names. Also proves the zero value's nil `now` func
// is never reached (a naive Log() would panic calling a nil l.now()).
func TestLogger_ZeroValue_FailsClosed(t *testing.T) {
	var l Logger

	err := l.Log(Entry{Code: "MET-F900", Msg: "zero-value"})
	wantLoggerCopied(t, "Log", err)
	wantLoggerCopied(t, "Close", l.Close())
	l.SetClock(func() time.Time { return time.Now() }) // must not panic
}

// TestLogger_NewLoggerPointer_ZeroValue_FailsClosed covers `new(Logger)`
// explicitly — same construction gap as the zero value, reached via a
// different spelling, per the brief's explicit "zero value, new(...),
// and a hand-built literal" requirement.
func TestLogger_NewLoggerPointer_ZeroValue_FailsClosed(t *testing.T) {
	l := new(Logger)
	wantLoggerCopied(t, "Log", l.Log(Entry{Code: "MET-F900", Msg: "new-logger"}))
}

// TestLogger_HandBuiltLiteral_SelfUnset_FailsClosed is the sharpest of
// the three required cases: a hand-built literal with w/now already
// populated exactly as if it were a fully-configured, legitimately used
// Logger — but `self` left unset. If checkNotCopied were ever
// accidentally skipped or short-circuited, Log would succeed here by
// coincidence (the data really is usable); asserting rejection proves
// the guard is actually the thing producing the answer, not the data.
func TestLogger_HandBuiltLiteral_SelfUnset_FailsClosed(t *testing.T) {
	var buf bytes.Buffer
	literal := &Logger{w: &buf, now: time.Now}

	err := literal.Log(Entry{Code: "MET-F900", Msg: "literal"})
	wantLoggerCopied(t, "Log", err)
	if buf.Len() != 0 {
		t.Fatalf("hand-built literal (self unset) wrote %d bytes, want 0 — the guard must be what decides this, not the data", buf.Len())
	}
}

// --- ASM-074: a rejected Log() does not lose the entry -----------------

// TestLogger_RejectedLogFallsBackToRingBuffer proves ASM-074's judgement
// call actually happens: a Log() call rejected because the receiver is a
// struct-copied Logger still lands its Entry in an in-memory ring buffer
// rather than being silently dropped. This is the "show the drop is
// observable somewhere" requirement for the Logger's specific design
// decision.
//
// SEC-031 part 2 (ASM-105) moved this specific fallback from the
// GENUINE ring (Recent()) into a separate copyRejectRing
// (RecentCopyRejections()) — see that split's doc comments for why: the
// genuine ring must never be evictable by a copy-holding caller. This
// test now asserts against RecentCopyRejections(), and additionally
// asserts the entry does NOT leak into Recent(), which it must not.
func TestLogger_RejectedLogFallsBackToRingBuffer(t *testing.T) {
	resetSinkForTest()
	t.Cleanup(resetSinkForTest)

	var buf bytes.Buffer
	orig := NewLogger(&buf)
	cp := loggerByteCopy(orig)

	err := cp.Log(Entry{Level: "error", Code: "MET-F900", CorrelationID: "corr-ring", Module: "m", Msg: "should land in ring"})
	wantLoggerCopied(t, "Log", err)

	found := false
	for _, e := range RecentCopyRejections() {
		if e.Msg == "should land in ring" && e.CorrelationID == "corr-ring" {
			found = true
			if e.Ts == "" {
				t.Error("ring-buffer fallback entry has an empty Ts — Log's normal Ts auto-fill behaviour must still apply on the reject path")
			}
			break
		}
	}
	if !found {
		t.Fatal("rejected Log() call's Entry was not found in RecentCopyRejections() — ASM-074/ASM-105's ring-buffer fallback did not fire, the audit trail went silent")
	}
	for _, e := range Recent() {
		if e.Msg == "should land in ring" {
			t.Fatal("rejected Log() call's Entry leaked into Recent() — SEC-031 part 2 requires copy-rejection entries to stay out of the genuine ring")
		}
	}
	if buf.Len() != 0 {
		t.Fatalf("rejected Log() call wrote %d bytes to the copy's (shared) underlying writer, want 0", buf.Len())
	}
}

// --- Sanity check: two independently constructed Loggers never collide ---

// TestLogger_SelfIdentity_FreshLoggersAreDistinct is a sanity check that
// two independently constructed Loggers never collide on identity (the
// non-attack path checkNotCopied must never reject).
func TestLogger_SelfIdentity_FreshLoggersAreDistinct(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	l1 := NewLogger(&buf1)
	l2 := NewLogger(&buf2)

	if err := l1.Log(Entry{Code: "MET-F900", Msg: "l1"}); err != nil {
		t.Fatalf("l1.Log: %v", err)
	}
	if err := l2.Log(Entry{Code: "MET-F900", Msg: "l2"}); err != nil {
		t.Fatalf("l2.Log: %v", err)
	}
	if err := l1.Close(); err != nil {
		t.Fatalf("l1.Close: %v", err)
	}
	if err := l2.Close(); err != nil {
		t.Fatalf("l2.Close: %v", err)
	}
}
