package main

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-208 increment 2 test infrastructure: bootCore's returned wiring
// now hands the transport's Results()/Deltas()/Events() channels to
// router.Router.Run as their ONE reader (w.router field's own doc
// comment, boot.go) — a test that reads w.transport.Results() directly
// after bootCore returns races Router.Run for delivery and will
// typically lose (ui/router/doc.go's own "two readers split delivery
// non-deterministically" warning, now demonstrated the other direction:
// a second reader OUTSIDE router, not a second router). Every test that
// needs to observe a CommandResult against a booted skeletonWiring must
// therefore register itself with w.router the same way a real screen
// would (router.RegisterResultHandler), never read w.transport.Results()
// directly. This file is that shared test-only adapter.

// resultReceiverFunc adapts a bare func(protocol.CommandResult) into
// router.ResultReceiver (an ApplyResult method) — the test-side mirror of
// boot.go's own deltaReceiverFunc production adapter.
type resultReceiverFunc func(protocol.CommandResult)

func (f resultReceiverFunc) ApplyResult(r protocol.CommandResult) { f(r) }

// awaitRouterResult registers correlationID with w.router and returns a
// buffered channel that receives exactly the one CommandResult router
// routes for it (RegisterResultHandler's own "one CommandResult per
// registered CorrelationID, then consumed" contract) — the test-side
// replacement for a raw `<-w.transport.Results()` read.
func awaitRouterResult(w *skeletonWiring, correlationID protocol.CorrelationID) <-chan protocol.CommandResult {
	ch := make(chan protocol.CommandResult, 1)
	w.router.RegisterResultHandler(correlationID, resultReceiverFunc(func(r protocol.CommandResult) { ch <- r }))
	return ch
}

// sendAndAwaitResult sends cmd over w.transport, having already
// registered cmd's own CorrelationID with w.router (so router — the
// transport's sole reader post-boot — routes the resulting CommandResult
// back to this call rather than it being an unrecoverable routing-table
// hit nothing observes), and returns the result or fails the test on
// timeout/send error.
//
// BUG-510: the wait used to be bounded by a fixed 2s wall-clock timeout —
// the same class of defect as awaitStatus's old 5s bound (see that
// function's doc comment, bug324_chrome_topbar_test.go): on a loaded or
// `-race`-instrumented runner the router/pump goroutines that deliver the
// CommandResult can legitimately take longer than 2s to be scheduled,
// with nothing actually wrong. t.Context() ties the wait to the test's
// REAL deadline (its own -timeout, or cleanup) instead of a guessed
// constant, so a genuinely undelivered result still fails the test — just
// at the deadline that actually governs the test, not one picked to be
// "probably enough."
func sendAndAwaitResult(t *testing.T, w *skeletonWiring, cmd protocol.Command) protocol.CommandResult {
	t.Helper()
	ch := awaitRouterResult(w, cmd.CorrelationID)
	if err := w.transport.SendCommand(cmd); err != nil {
		t.Fatalf("SendCommand(%s): %v", cmd.Kind, err)
	}
	select {
	case res := <-ch:
		return res
	case <-t.Context().Done():
		t.Fatalf("timed out waiting for a %s result (via router.RegisterResultHandler) before the test's own deadline", cmd.Kind)
		return protocol.CommandResult{}
	}
}
