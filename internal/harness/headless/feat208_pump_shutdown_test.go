package headless

import (
	"testing"
	"time"
)

// TestRegression_JoinPumpDone_TimesOutRatherThanHangs is R3's
// (independent round r2/r3, FEAT-208 increment 1) bounded-join proof at
// the headless package's own shutdown-closure layer (mirrors
// cmd/metropolis's identical
// TestRegression_Shutdown_PumpDoneTimeout_ProceedsRatherThanHangs):
// joinPumpDone must return once pumpShutdownJoinTimeout elapses, even
// when the pump goroutine's done channel never closes (e.g. a DeltaSink
// that blocks indefinitely or reenters Publish — both documented-
// prohibited on engine/core.DeltaSink, neither mechanically
// preventable). pumpShutdownJoinTimeout is temporarily lowered (it is a
// package-level var specifically so this test can do that — see its own
// doc comment) so this proof runs in milliseconds; restored via a
// scratch save/restore, never left mutated for other tests.
func TestRegression_JoinPumpDone_TimesOutRatherThanHangs(t *testing.T) {
	original := pumpShutdownJoinTimeout
	pumpShutdownJoinTimeout = 100 * time.Millisecond
	defer func() { pumpShutdownJoinTimeout = original }()

	// Deliberately never closed — models the R3 hazard (a permanently-
	// stuck pump goroutine).
	pumpDone := make(chan struct{})

	returned := make(chan struct{})
	start := time.Now()
	go func() {
		joinPumpDone(pumpDone, "regression-join-pumpdone-timeout")
		close(returned)
	}()

	select {
	case <-returned:
		elapsed := time.Since(start)
		if elapsed < pumpShutdownJoinTimeout {
			t.Fatalf("joinPumpDone returned in %v, want >= the (lowered) timeout %v — it should have waited for the timeout before giving up", elapsed, pumpShutdownJoinTimeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("R3 REGRESSION: joinPumpDone did not return even after 2s with a permanently-open pumpDone channel and a lowered 100ms timeout — the bounded join is not actually bounded")
	}
}

// TestRegression_JoinPumpDone_ReturnsPromptlyWhenPumpDoneCloses is the
// non-regression half: joinPumpDone must NOT wait for the full timeout
// when pumpDone closes promptly (the ordinary, non-hung case) — proving
// the timeout is a ceiling, not an unconditional delay.
func TestRegression_JoinPumpDone_ReturnsPromptlyWhenPumpDoneCloses(t *testing.T) {
	original := pumpShutdownJoinTimeout
	pumpShutdownJoinTimeout = 5 * time.Second // generous, so this test's own assertion (< timeout) is meaningful
	defer func() { pumpShutdownJoinTimeout = original }()

	pumpDone := make(chan struct{})
	close(pumpDone)

	returned := make(chan struct{})
	start := time.Now()
	go func() {
		joinPumpDone(pumpDone, "regression-join-pumpdone-prompt")
		close(returned)
	}()

	select {
	case <-returned:
		elapsed := time.Since(start)
		if elapsed >= pumpShutdownJoinTimeout {
			t.Fatalf("joinPumpDone took %v (>= the timeout %v) to return even though pumpDone was already closed — it should return immediately", elapsed, pumpShutdownJoinTimeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("joinPumpDone never returned despite pumpDone already being closed")
	}
}
