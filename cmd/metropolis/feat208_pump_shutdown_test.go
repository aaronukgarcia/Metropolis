package main

import (
	"context"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// TestRegression_Shutdown_PumpDoneTimeout_ProceedsRatherThanHangs is R3's
// (independent round r2/r3, FEAT-208 increment 1) bounded-join proof at
// the boot.go layer: if the subscription pump goroutine's done channel
// never closes (e.g. a DeltaSink that blocks indefinitely or reenters
// Publish — both documented-prohibited on engine/core.DeltaSink,
// neither mechanically preventable), shutdown() must still return —
// bounded by pumpShutdownJoinTimeout, not forever — rather than hanging
// process shutdown permanently (exactly what r2's
// TestAttack_ReentrantDeltaSink_DeadlocksThePumpGoroutine warned an
// unbounded join would do). pumpShutdownJoinTimeout is temporarily
// lowered here (it is a package-level var specifically so this test can
// do that — see its own doc comment) so this proof runs in milliseconds
// rather than the real 5s production value; restored via a scratch
// save/restore, never a package-level mutation left behind for other
// tests.
func TestRegression_Shutdown_PumpDoneTimeout_ProceedsRatherThanHangs(t *testing.T) {
	original := pumpShutdownJoinTimeout
	pumpShutdownJoinTimeout = 100 * time.Millisecond
	defer func() { pumpShutdownJoinTimeout = original }()

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())

	w := &skeletonWiring{
		correlationID: "regression-pump-shutdown-timeout",
		transport:     transport,
		ctx:           ctx,
		cancel:        cancel,
		engineDone:    make(chan struct{}),
		// pumpDone deliberately never closed — models the R3 hazard
		// (a permanently-stuck pump goroutine) at the shutdown-join
		// call site itself, without needing to actually construct a
		// real deadlocked pump.
		pumpDone: make(chan struct{}),
	}
	// w.wg (zero value, no Add() calls made) makes w.wg.Wait()
	// (shutdown()'s first blocking step) return immediately — this
	// test isolates the pumpDone join specifically, not
	// RunCommandLoop/ViewsLoop's own (already independently tested)
	// join behaviour.

	shutdownReturned := make(chan struct{})
	start := time.Now()
	go func() {
		w.shutdown()
		close(shutdownReturned)
	}()

	select {
	case <-shutdownReturned:
		elapsed := time.Since(start)
		if elapsed < pumpShutdownJoinTimeout {
			t.Fatalf("shutdown() returned in %v, want >= the (lowered) timeout %v — it should have waited for the timeout before giving up on pumpDone", elapsed, pumpShutdownJoinTimeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("R3 REGRESSION: shutdown() did not return even after 2s with a permanently-open pumpDone channel and a lowered 100ms timeout — the bounded join is not actually bounded")
	}
}
