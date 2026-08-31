package core

import (
	"errors"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestBUG456_CopiedEngineClock_ValidCorrelationID is the copy-safety
// regression for BUG-456's lazy correlation-ID mint. Clock() now passes
// "" to checkNotCopied so no crypto/rand UUID is minted on the hot
// (never-copied) per-tick path. This test proves the OTHER path — an
// actual struct copy — is unchanged: Clock() on a copy must still return
// ErrEngineCopied (not hang, not touch the lock) AND that error must
// carry a NON-EMPTY, VALID correlation ID. Before the fix the ID was
// minted eagerly; the lazy `if correlationID == "" { mint }` branch must
// actually fire here. If it does not, errs.New replaces the empty ID with
// the visible "MISSING-CORRELATION-ID" placeholder — a traceability
// regression this test catches.
func TestBUG456_CopiedEngineClock_ValidCorrelationID(t *testing.T) {
	orig := NewEngine(WithPoolSize(4))
	cp := e2Copy(orig)

	// Run on the copy off the main goroutine so a hang (SEC-016 class)
	// shows up as a timeout rather than wedging the test binary.
	type res struct {
		err error
	}
	done := make(chan res, 1)
	go func() {
		_, err := cp.Clock()
		done <- res{err: err}
	}()

	var got res
	select {
	case got = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("copied Engine.Clock() did not return within the liveness timeout — it hung (SEC-016 class) instead of being rejected")
	}

	if got.err == nil {
		t.Fatal("copied Engine.Clock() returned nil error, want ErrEngineCopied")
	}

	var e *errs.E
	if !errors.As(got.err, &e) {
		t.Fatalf("copied Engine.Clock() error is not *errs.E: %T (%v)", got.err, got.err)
	}
	if e.Code != ErrEngineCopied {
		t.Fatalf("copied Engine.Clock() error code = %q, want %q", e.Code, ErrEngineCopied)
	}
	if e.CorrelationID == "" {
		t.Fatal("copied Engine.Clock() error has EMPTY correlation ID — lazy mint branch did not fire (BUG-456 regression)")
	}
	if e.CorrelationID == "MISSING-CORRELATION-ID" {
		t.Fatal("copied Engine.Clock() error carries the MISSING-CORRELATION-ID placeholder — lazy mint branch did not fire, errs.New saw \"\" (BUG-456 regression)")
	}
	if !errs.IsValidCorrelationID(e.CorrelationID) {
		t.Fatalf("copied Engine.Clock() error correlation ID %q is not a valid UUIDv4 (BUG-456 regression)", e.CorrelationID)
	}
}

// TestBUG456_CopiedEngineClock_UniqueIDsAcrossCalls confirms the lazy
// mint produces a fresh ID each time the copy path is taken (two copies'
// errors must not share the same ID nor both be empty/placeholder), i.e.
// the mint really runs inside checkNotCopied and is not accidentally a
// shared constant.
func TestBUG456_CopiedEngineClock_UniqueIDsAcrossCalls(t *testing.T) {
	orig := NewEngine(WithPoolSize(2))
	cp := e2Copy(orig)

	id := func() string {
		_, err := cp.Clock()
		if err == nil {
			t.Fatal("copied Engine.Clock() returned nil error, want ErrEngineCopied")
		}
		var e *errs.E
		if !errors.As(err, &e) {
			t.Fatalf("error not *errs.E: %T", err)
		}
		return e.CorrelationID
	}

	a := id()
	b := id()
	if a == "" || b == "" {
		t.Fatalf("empty correlation ID(s): a=%q b=%q", a, b)
	}
	if a == b {
		t.Fatalf("two copy-path Clock() calls minted the SAME correlation ID %q — mint is not per-call", a)
	}
}
