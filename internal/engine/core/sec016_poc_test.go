//go:build !race

package core

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is deliberately excluded from -race builds (Go's race
// detector auto-defines the `race` build tag, so `!race` above means
// "never compiled into a -race binary"). That is not a way of avoiding
// scrutiny — it is required by what this test demonstrates: SEC-016's
// attack is 2000 concurrent struct copies of Engine racing against a
// concurrent Lock/Unlock loop on the original, which is BY DESIGN an
// unsynchronized read of memory that another goroutine is concurrently
// writing (the whole premise: catch mu mid-lock). The race detector
// correctly flags that raw copy as a data race every time, which is
// expected and matches Destructive-2's own methodology: SEC-016 was
// reproduced and is verified here "without -race (production shape)"
// specifically because -race's instrumentation changes scheduling and
// is not the code path this finding is about (the vulnerability is a
// silent hang in an uninstrumented production binary, not something
// -race would ever have caught in the first place — see sec003/sec014's
// PoC tests for where -race IS the right tool, and this file for where
// it is not). `go test ./... -race -count=1` therefore never sees this
// file; it is exercised by a plain `go test ./internal/engine/core/ -run
// TestSEC016` run instead.

// TestSEC016_CopyDuringLock_RejectedNotHung reproduces SEC-016
// (Destructive-2, round 2, P1) against the FIXED engine and asserts the
// attack is now refused promptly rather than hanging forever.
//
// The attack: even after SEC-014's fix, checkNotCopied ran AFTER
// e.mu.Lock() in both RegisterPhaseHook and seal(). If a struct copy
// (e2Copy, the same mechanism sec014_poc_test.go uses) is taken at a
// moment when the ORIGINAL's mu happens to be locked, the COPY's mu
// bytes come away byte-identical to "currently locked" — but nobody
// will ever call Unlock() with the COPY's address, only the original's.
// The copy's own next Lock() call then blocks forever: no crash, no
// error, no correlation ID, just a goroutine parked in
// runtime_SemacquireMutex permanently — arguably worse than SEC-014's
// crash, because a crash is loud and this is silent.
//
// This was verified live, run against the code exactly as it stood
// before Engine.self became an atomic.Pointer[Engine] and the identity
// check moved ahead of mu.Lock(): driving a tight lock/unlock loop on
// the original while concurrently taking 2000 struct copies and
// immediately calling RegisterPhaseHook on each reliably left several
// goroutines wedged, and `go test -timeout 4s` dumped stacks confirming
// the exact mechanism:
//
//	internal/sync.runtime_SemacquireMutex(...)
//	internal/sync.(*Mutex).lockSlow(0xc001818880)
//	internal/sync.(*Mutex).Lock(...)
//	...Engine.RegisterPhaseHook(0xc001818880, ...)
//
// The fix (self became atomic.Pointer[Engine], loaded lock-free at the
// very top of RegisterPhaseHook/seal() — see engine.go) rejects a
// struct-copied Engine with ErrEngineCopied BEFORE its mu is ever
// touched, so a copy whose mu bytes are in a bad state is never
// acquired at all. Against the fixed engine, the same 2000-copy attack
// completes promptly (observed: ~0.01s across ten repeated runs, vs.
// this test's 15s bound) with every copy call resolving to
// ErrEngineCopied.
func TestSEC016_CopyDuringLock_RejectedNotHung(t *testing.T) {
	e := NewEngine(WithPoolSize(4))

	stopLocker := make(chan struct{})
	var lockerWG sync.WaitGroup
	lockerWG.Add(1)
	go func() {
		defer lockerWG.Done()
		for {
			select {
			case <-stopLocker:
				return
			default:
				// Briefly locks/unlocks e.mu on every call -- the window
				// SEC-016 needed a copy to land inside.
				_ = e.RegisterPhaseHook(PhaseProduction, noopHook{})
			}
		}
	}()
	defer func() {
		close(stopLocker)
		lockerWG.Wait()
	}()

	const n = 2000
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		e2 := e2Copy(e)
		go func(e2 *Engine) {
			// Pre-fix, this call took e2.mu.Lock() BEFORE any identity
			// check ran, and could hang forever if e2's mu bytes were
			// copied while e's mu was locked. Post-fix, checkNotCopied
			// runs first and needs nothing but an atomic load, so this
			// always returns promptly.
			results <- e2.RegisterPhaseHook(PhaseConsumptionShortfall, noopHook{})
		}(e2)
	}

	// Bounded wait: pre-fix, this bound was the whole point (proving a
	// hang) -- see sec003/sec014's siblings for that shape. Post-fix, we
	// assert every result arrives well within it AND that every result
	// is ErrEngineCopied, not merely "arrived".
	deadline := time.After(15 * time.Second)
	got := 0
	for got < n {
		select {
		case err := <-results:
			got++
			if !errors.Is(err, &errs.E{Code: ErrEngineCopied}) {
				t.Errorf("copy call %d: err = %v, want ErrEngineCopied", got, err)
			}
		case <-deadline:
			t.Fatalf("SEC-016 REGRESSION: only %d/%d copy-during-lock calls completed within 15s — at least one is hung, exactly the pre-fix failure mode", got, n)
		}
	}
}
