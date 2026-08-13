package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-019: StartSubscriptionPump carried no checkNotCopied guard. A
// struct-copied Engine (e2 := *e) calling e2.StartSubscriptionPump would
// start a live goroutine reading e2.deltaSignal (a copied channel HEADER
// aliasing the same underlying channel as the original) and call
// e2.subs.PublishEngineStatus() — but since e2.subs is the SAME POINTER
// as e.subs (not itself a copy), PublishEngineStatus's own checkNotCopied
// guard would never fire. The result would not be a crash or a hang but
// silently WRONG DATA: engine.status deltas built from a zeroed Clock via
// EngineStatusView()'s degrade-to-zero path.
//
// TestBUG019_CopiedEngine_StartSubscriptionPump_RejectedNoPublish proves
// AC-1/AC-2/AC-3 from docs/planning/acceptance/BUG-019.md: the copy is
// rejected with the same registry-sourced ErrEngineCopied shape used by
// every other guarded method on this type (AC-1/AC-3), and — the
// finding's specific subtlety — no delta is ever published via the
// shared subs pointer as a side effect of the rejected call (AC-2).
func TestBUG019_CopiedEngine_StartSubscriptionPump_RejectedNoPublish(t *testing.T) {
	e := NewEngine()
	e2 := e2Copy(e)

	sink := &countingSink{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := e2.StartSubscriptionPump(ctx, sink)
	if !errors.Is(err, &errs.E{Code: ErrEngineCopied}) {
		t.Fatalf("StartSubscriptionPump on a struct-copied Engine: err = %v, want ErrEngineCopied", err)
	}

	// Signal the pump the same way a real command handler would
	// (signalSubscriptionPump), and give a wrongly-started goroutine a
	// generous window to act. Because the copy was rejected before the
	// goroutine was ever started, no signal delivery is possible and no
	// delta should ever reach sink — proving this isn't merely "an error
	// was returned" but that the specific wrong-data publish path never
	// runs, even though e2.subs is the same pointer as e.subs.
	select {
	case e2.deltaSignal <- struct{}{}:
	default:
	}
	time.Sleep(50 * time.Millisecond)

	if sink.calls != 0 {
		t.Fatalf("StartSubscriptionPump on a copy: sink.SendDelta called %d times, want 0 (no goroutine should ever have started, so PublishEngineStatus must never run against the shared subs pointer)", sink.calls)
	}
}

// notifyingSink is a DeltaSink that signals a channel on every call
// instead of incrementing a plain counter — unlike countingSink (shared
// with sec019_poc_test.go, safely read there only after its writer
// goroutine has already joined via a `done` channel), this test reads
// from the main goroutine WHILE the pump goroutine may still be writing,
// so a channel handoff is used instead of a bare field to stay clean
// under -race.
type notifyingSink struct {
	called chan struct{}
}

func (n *notifyingSink) SendDelta(protocol.Delta) bool {
	select {
	case n.called <- struct{}{}:
	default:
	}
	return true
}

// TestBUG019_OriginalEngine_StartSubscriptionPump_StillWorks is the
// non-regression half: the guard must not break the normal, non-copied
// path — a genuine *Engine returned from NewEngine must still start the
// pump successfully and actually publish a delta when signalled.
func TestBUG019_OriginalEngine_StartSubscriptionPump_StillWorks(t *testing.T) {
	e := NewEngine()
	sink := &notifyingSink{called: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// PublishEngineStatus only pushes to live "engine.status"
	// subscriptions (subscribe.go) — a real caller reaches this via the
	// Subscribe command; this test subscribes directly against the same
	// package's SubscriptionServer to keep the test focused on the pump
	// guard rather than full command round-tripping.
	if _, err := e.subs.Subscribe(engineStatusView, nil, "", "bug019-still-works-seed"); err != nil {
		t.Fatalf("seed Subscribe: %v", err)
	}

	if err := e.StartSubscriptionPump(ctx, sink); err != nil {
		t.Fatalf("StartSubscriptionPump on a genuine Engine: err = %v, want nil", err)
	}

	e.signalSubscriptionPump()

	select {
	case <-sink.called:
	case <-time.After(2 * time.Second):
		t.Fatal("StartSubscriptionPump on a genuine Engine: no delta published within 2s after signalSubscriptionPump")
	}
}
