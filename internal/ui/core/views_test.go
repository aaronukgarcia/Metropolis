package core

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func newTestTransport() *protocol.InProcTransport {
	return protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer,
		protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer,
		protocol.DefaultDeltaBuffer,
	)
}

func TestViewsLoop_AppliesInOrderDeltas(t *testing.T) {
	tr := newTestTransport()
	store := NewViewStore()
	loop := NewViewsLoop(tr, store, "corr-views-1")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { loop.Run(stop); close(done) }()

	sub := protocol.SubscriptionID("sub-1")
	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 1, Seq: 1, Patch: json.RawMessage(`{"a":1}`)})
	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 2, Seq: 2, Patch: json.RawMessage(`{"a":2}`)})

	waitForCondition(t, func() bool {
		vm := store.Front()
		return string(vm.Patches[sub]) == `{"a":2}`
	})

	vm := store.Front()
	if vm.Stale[sub] {
		t.Fatalf("subscription should not be stale after in-order deltas, got %+v", vm.Stale)
	}
	if vm.Tick[sub] != 2 {
		t.Fatalf("Tick[sub] = %d, want 2", vm.Tick[sub])
	}

	close(stop)
	<-done
}

func TestViewsLoop_SeqGapSetsStaleness(t *testing.T) {
	tr := newTestTransport()
	store := NewViewStore()
	loop := NewViewsLoop(tr, store, "corr-views-2")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { loop.Run(stop); close(done) }()

	sub := protocol.SubscriptionID("sub-gap")
	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 1, Seq: 1, Patch: json.RawMessage(`{"a":1}`)})
	// Seq jumps from 1 to 5: a gap of 3.
	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 2, Seq: 5, Patch: json.RawMessage(`{"a":5}`)})

	waitForCondition(t, func() bool {
		return store.Front().Stale[sub]
	})

	if !store.Front().AnyStale() {
		t.Fatal("AnyStale() should report true after a Seq gap")
	}

	// The next in-order delta clears staleness.
	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 3, Seq: 6, Patch: json.RawMessage(`{"a":6}`)})
	waitForCondition(t, func() bool {
		return !store.Front().Stale[sub]
	})

	close(stop)
	<-done
}

func TestViewsLoop_MalformedDeltaDroppedNotCrashed(t *testing.T) {
	tr := newTestTransport()
	store := NewViewStore()
	loop := NewViewsLoop(tr, store, "corr-views-3")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { loop.Run(stop); close(done) }()

	subGood := protocol.SubscriptionID("sub-good")
	subBad := protocol.SubscriptionID("sub-bad")

	tr.SendDelta(protocol.Delta{SubscriptionID: subGood, Tick: 1, Seq: 1, Patch: json.RawMessage(`{"ok":true}`)})
	waitForCondition(t, func() bool { return store.Front().Patches[subGood] != nil })

	// Malformed: not valid JSON.
	tr.SendDelta(protocol.Delta{SubscriptionID: subBad, Tick: 2, Seq: 1, Patch: json.RawMessage(`{not valid json`)})

	// Give the loop a moment to process the malformed delta, then confirm
	// it did not crash the loop (subsequent good deltas still apply) and
	// did not corrupt the other subscription's state.
	tr.SendDelta(protocol.Delta{SubscriptionID: subGood, Tick: 3, Seq: 2, Patch: json.RawMessage(`{"ok":"still true"}`)})
	waitForCondition(t, func() bool {
		return string(store.Front().Patches[subGood]) == `{"ok":"still true"}`
	})

	if _, ok := store.Front().Patches[subBad]; ok {
		t.Fatal("malformed delta's patch must not be stored")
	}

	close(stop)
	<-done
}
