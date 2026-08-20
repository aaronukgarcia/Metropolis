package core

import (
	"context"
	"testing"
	"time"
)

// F2 (independent round r1, FEAT-208 increment 1): the subscription
// pump goroutine StartSubscriptionPump starts was never joined at
// shutdown anywhere in the codebase — cmd/metropolis/boot.go's
// shutdown() waited on w.wg (RunCommandLoop + ViewsLoop.Run only) and
// closed the transport with no idea whether the pump goroutine had
// actually exited, and internal/harness/headless's Run() shutdown
// closure had the identical gap. StartSubscriptionPump now returns a
// done channel, closed exactly once when the goroutine actually exits
// (ctx.Done() observed) — boot.go's shutdown() and headless's shutdown
// closure both join it before closing their transport. This file proves
// the done channel itself behaves correctly at the SubscriptionServer/
// Engine level (the leak-proving bar F2 asks for); the two production
// callers' own join call sites are covered by their packages' existing
// integration tests (cmd/metropolis, internal/harness/headless) still
// passing after this change.

// TestRegression_StartSubscriptionPump_DoneChannel_ClosesOnCancel is the
// direct leak-proving test: start the pump, cancel its context, and
// assert the returned done channel closes well before a generous
// timeout — proving the goroutine actually exits rather than leaking
// forever, and that cancellation alone (no further signal) is
// sufficient to stop it.
func TestRegression_StartSubscriptionPump_DoneChannel_ClosesOnCancel(t *testing.T) {
	e := NewEngine()
	sink := &orderedSink{}
	ctx, cancel := context.WithCancel(context.Background())

	done, err := e.StartSubscriptionPump(ctx, sink)
	if err != nil {
		t.Fatalf("StartSubscriptionPump: %v", err)
	}

	// Not yet done — the pump is still running.
	select {
	case <-done:
		t.Fatal("done channel closed before cancel() was ever called")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("LEAK: done channel never closed within 2s of cancel() — the pump goroutine did not exit")
	}

	// Idempotent: reading an already-closed channel again must not
	// block (proves close(doneCh) happened, not merely a coincidental
	// send).
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("done channel is not actually closed (second read blocked)")
	}
}

// TestRegression_StartSubscriptionPump_DoneChannel_SurvivesActiveSignalling
// proves the leak-proving guarantee holds even while the pump is
// actively busy (mid-signal-loop, not idle) — cancel() must still stop
// it and close done promptly, not get lost behind a backlog of pending
// signals.
func TestRegression_StartSubscriptionPump_DoneChannel_SurvivesActiveSignalling(t *testing.T) {
	e := NewEngine()
	sink := &orderedSink{}
	ctx, cancel := context.WithCancel(context.Background())

	done, err := e.StartSubscriptionPump(ctx, sink)
	if err != nil {
		t.Fatalf("StartSubscriptionPump: %v", err)
	}

	stop := make(chan struct{})
	signalDone := make(chan struct{})
	go func() {
		defer close(signalDone)
		for {
			select {
			case <-stop:
				return
			default:
				e.signalSubscriptionPump()
			}
		}
	}()

	time.Sleep(20 * time.Millisecond) // let the signal loop get busy
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(stop)
		<-signalDone
		t.Fatal("LEAK: done channel never closed within 2s of cancel() while under active signalling load")
	}
	close(stop)
	<-signalDone
}
