package stub

import (
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// TestStubEngine_Chaos_UnsubscribeDropsDelayedDeltas is BUG-283's
// regression test: a Delta whose chaos-delay is still counting down when
// Unsubscribe is processed must be DROPPED, never delivered
// (use-after-unsubscribe). deltas.go requires the engine to stop pushing
// the instant Unsubscribe is processed (AC-5).
//
// RED (pre-fix): emitDeltaLocked spawned an independent goroutine per
// delayed delta that slept and then called SendDelta unconditionally,
// never re-checking subscription liveness — so the delayed initial
// snapshot arrived ~MinDelay after Unsubscribe. GREEN (post-fix): the
// per-subscription delivery pump aborts on Unsubscribe and re-checks
// liveness under s.mu before every send, so the queued delta is dropped.
func TestStubEngine_Chaos_UnsubscribeDropsDelayedDeltas(t *testing.T) {
	const delay = 150 * time.Millisecond

	tr, eng := newTestEngine(t, WithChaos(ChaosConfig{
		Seed: 5,
		DelayedDeltas: DelayConfig{
			Enabled:  true,
			MinDelay: delay,
			MaxDelay: delay,
		},
	}))

	sr := send(t, tr, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f1.viewport"})
	if !sr.Accepted {
		t.Fatalf("Subscribe rejected: %#v", sr.Error)
	}

	// The initial snapshot delta is queued but delayed; its SubscriptionID
	// would normally be learned from that (not-yet-arrived) delta, so read
	// it white-box and Unsubscribe well before the delay elapses.
	var subID protocol.SubscriptionID
	deadline := time.Now().Add(testTimeout)
	for {
		if ids := SubIDsForTest(eng); len(ids) == 1 {
			subID = ids[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("subscription never registered")
		}
		time.Sleep(time.Millisecond)
	}

	ur := send(t, tr, protocol.KindUnsubscribe, protocol.UnsubscribePayload{SubscriptionID: subID})
	if !ur.Accepted {
		t.Fatalf("Unsubscribe rejected: %#v", ur.Error)
	}

	// No delta may arrive for the now-dead subscription, even though its
	// delayed initial snapshot was queued before the Unsubscribe.
	select {
	case d := <-tr.Deltas():
		t.Fatalf("received a delayed delta after Unsubscribe (BUG-283 use-after-unsubscribe): %#v", d)
	case <-time.After(delay + 150*time.Millisecond):
		// expected: the queued delayed delta was dropped
	}
}

// TestStubEngine_Chaos_DelayedDeltasDeliverInSeqOrder is BUG-284's
// regression test: delayed deltas for one subscription must arrive in
// strictly increasing Seq order (GR#21 determinism), never reordered.
//
// RED (pre-fix): each delayed delta was sent from its own goroutine after
// an independent random sleep, so a later-Seq delta with a shorter delay
// overtook an earlier one. A burst of 12 delayed deltas has a ~1/12!
// chance of its random delays already being ascending, so pre-fix this
// test reordered essentially every run and SeqTracker.Observe reported
// ok=false. GREEN (post-fix): a single per-subscription pump drains the
// queue sequentially, so delivery order == enqueue order == Seq order.
func TestStubEngine_Chaos_DelayedDeltasDeliverInSeqOrder(t *testing.T) {
	const burst = 12

	tr, _ := newTestEngine(t, WithChaos(ChaosConfig{
		Seed: 20260825,
		DelayedDeltas: DelayConfig{
			Enabled:  true,
			MinDelay: 1 * time.Millisecond,
			MaxDelay: 40 * time.Millisecond,
		},
		BurstDeltas: BurstConfig{
			Enabled: true,
			Size:    burst,
		},
	}))

	sr := send(t, tr, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f1.viewport"})
	if !sr.Accepted {
		t.Fatalf("Subscribe rejected: %#v", sr.Error)
	}

	// The initial snapshot is pushed as `burst` delayed copies with Seq
	// 1..burst. Feed each arrival through a SeqTracker in receipt order: a
	// non-monotonic arrival is exactly the ok=false the protocol tells
	// callers to treat as a bug.
	tracker := protocol.NewSeqTracker()
	var prev uint64
	for i := 0; i < burst; i++ {
		d := recvDelta(t, tr)
		if _, ok := tracker.Observe(d.SubscriptionID, d.Seq); !ok {
			t.Fatalf("delta Seq %d arrived out of Seq order (previous arrival Seq %d): delayed deltas delivered out of order (BUG-284)", d.Seq, prev)
		}
		prev = d.Seq
	}
	if prev != burst {
		t.Fatalf("last arrival Seq = %d, want %d (all %d burst deltas in order)", prev, burst, burst)
	}
}
