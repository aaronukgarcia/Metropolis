package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-208 increment 1, independent round r1 (2026-08-19/20). This file
// started life as an attack-only file ("no production code is touched")
// and drove a REJECT verdict: two of its four tests found real,
// mechanically-reachable defects (F1a: StartSubscriptionPump had no
// single-start guard; F1b: Publish's delivery order could diverge from
// its Seq-assignment order under concurrent Publish calls, empirically
// 3/8 runs). Both are now fixed in production code (commands.go's
// StartSubscriptionPump, subscribe.go's Publish) — this file is kept as
// the PERMANENT regression suite proving those fixes hold, renamed from
// TestAttack_* to TestRegression_*. The other two tests already passed
// against the design as originally built (proving the BUG-283/284 class
// could not recur) and are kept unchanged in substance, renamed only for
// naming consistency with the rest of this file.

// orderedSink records every Delta it receives IN THE ORDER SendDelta was
// actually called, with a wall-clock arrival timestamp, so a test can
// detect out-of-order DELIVERY even when per-subscription Seq assignment
// itself is internally monotonic.
type orderedSink struct {
	mu   sync.Mutex
	recv []protocol.Delta
}

func (o *orderedSink) SendDelta(d protocol.Delta) bool {
	o.mu.Lock()
	o.recv = append(o.recv, d)
	o.mu.Unlock()
	return true
}

func (o *orderedSink) snapshot() []protocol.Delta {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]protocol.Delta, len(o.recv))
	copy(out, o.recv)
	return out
}

// TestRegression_ConcurrentPublish_DeliveryStaysInOrder is the F1b fix's
// acceptance bar (formerly TestAttack_ConcurrentPublishCalls_CanDeliverOutOfOrderSeq,
// which found the defect: delivery order could diverge from Seq
// assignment order because SendDelta ran AFTER pass 3's lock was
// released). subscribe.go's Publish now performs SendDelta INSIDE pass
// 3's s.mu critical section, so two concurrent Publish calls can never
// interleave their assignment and delivery — this test proves that
// holds across 8 independent runs (subtests), each hammering the same
// SubscriptionServer with 60 concurrent Publish calls and an
// artificially-jittered ViewPatchFunc (modelling a real producer that
// does real work), exactly the shape that reproduced the defect 3/8
// runs before the fix. Run under `go test -race` — the whole point is
// proving no data race exists any more, not just no observed reordering.
func TestRegression_ConcurrentPublish_DeliveryStaysInOrder(t *testing.T) {
	for run := 0; run < 8; run++ {
		t.Run(fmt.Sprintf("run%d", run), func(t *testing.T) {
			s := NewSubscriptionServer()
			callN := 0
			var callMu sync.Mutex
			if err := s.RegisterView("regression.view", func() (json.RawMessage, error) {
				callMu.Lock()
				callN++
				n := callN
				callMu.Unlock()
				// Artificial jitter so different goroutines' pass-2 compute
				// times vary, maximising the chance of assignment/send
				// interleaving — modelling a real ViewPatchFunc that does
				// real work (a services iteration, a json.Marshal), not an
				// instant no-op.
				time.Sleep(time.Duration(n%3) * time.Millisecond)
				return json.Marshal(map[string]int{"n": n})
			}); err != nil {
				t.Fatalf("RegisterView: %v", err)
			}
			if _, err := s.Subscribe("regression.view", nil, "", "regression-corr"); err != nil {
				t.Fatalf("Subscribe: %v", err)
			}

			sink := &orderedSink{}

			const cycles = 60
			var wg sync.WaitGroup
			for i := 0; i < cycles; i++ {
				wg.Add(1)
				go func(tick int64) {
					defer wg.Done()
					s.Publish(sink, protocol.Tick(tick))
				}(int64(i))
			}
			wg.Wait()

			got := sink.snapshot()
			if len(got) == 0 {
				t.Fatal("no deltas delivered at all — cannot evaluate ordering")
			}

			// Seq VALUES must still be a set of DISTINCT monotonically
			// increasing integers (no duplicate/skip) — assignment
			// serialization under s.mu.
			seqs := make([]uint64, len(got))
			for i, d := range got {
				seqs[i] = d.Seq
			}
			sortedSeqs := append([]uint64(nil), seqs...)
			sort.Slice(sortedSeqs, func(i, j int) bool { return sortedSeqs[i] < sortedSeqs[j] })
			for i := 1; i < len(sortedSeqs); i++ {
				if sortedSeqs[i] == sortedSeqs[i-1] {
					t.Fatalf("DUPLICATE Seq assigned under concurrent Publish calls: %d appears twice", sortedSeqs[i])
				}
			}

			// DELIVERY order must now match Seq order exactly — the F1b
			// fix's acceptance bar: SendDelta runs inside the same
			// critical section Seq is assigned in, so a later-assigned
			// Seq can never reach the sink before an earlier-assigned
			// one.
			maxSeenSoFar := uint64(0)
			for _, d := range got {
				if d.Seq < maxSeenSoFar {
					t.Fatalf("F1b REGRESSION: delivery order diverged from Seq assignment order — delivered Seq %d after already having delivered a higher Seq (%d). Full delivery sequence: %v", d.Seq, maxSeenSoFar, seqCsv(got))
				}
				if d.Seq > maxSeenSoFar {
					maxSeenSoFar = d.Seq
				}
			}
		})
	}
}

func seqCsv(ds []protocol.Delta) []uint64 {
	out := make([]uint64, len(ds))
	for i, d := range ds {
		out[i] = d.Seq
	}
	return out
}

// TestRegression_UnsubscribeDuringPublishPass2_NeverReceivesInFlightDelta
// (formerly TestAttack_UnsubscribeDuringPass2_NeverReceivesTheInFlightPublish)
// attacks the BUG-283 class directly at its most dangerous timing
// window: Publish pass 2 (off s.mu, potentially slow real work) is
// deliberately blocked via a channel until the test signals it to
// proceed, giving Unsubscribe a guaranteed window to run BETWEEN
// Publish's pass 1 snapshot and pass 3 re-lock. This proves the design's
// own claim (§4: "a subscription pass 1 saw but Unsubscribe removed
// before this lock re-acquires is simply absent from s.subs here")
// empirically, under a real race window rather than by code inspection
// alone. Unchanged in substance from the independent round's original
// attack — it already passed against the pre-F1/F2 code, proving this
// specific defect class was never present; renamed only for consistency
// with this file's other regression tests.
func TestRegression_UnsubscribeDuringPublishPass2_NeverReceivesInFlightDelta(t *testing.T) {
	s := NewSubscriptionServer()
	proceed := make(chan struct{})
	entered := make(chan struct{})
	if err := s.RegisterView("regression.slow", func() (json.RawMessage, error) {
		close(entered)
		<-proceed // block pass 2 here until the test unblocks it
		return json.Marshal(map[string]int{"ok": 1})
	}); err != nil {
		t.Fatalf("RegisterView: %v", err)
	}
	id, err := s.Subscribe("regression.slow", nil, "", "regression-unsub")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	sink := &orderedSink{}
	publishDone := make(chan struct{})
	go func() {
		s.Publish(sink, protocol.Tick(1))
		close(publishDone)
	}()

	<-entered // Publish is now inside pass 2, holding no lock.
	if err := s.Unsubscribe(id, "regression-unsub-cancel"); err != nil {
		t.Fatalf("Unsubscribe while Publish is mid-pass-2: %v", err)
	}
	close(proceed) // let Publish's pass 2 finish and enter pass 3.

	select {
	case <-publishDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Publish never completed after Unsubscribe raced its pass 2")
	}

	got := sink.snapshot()
	if len(got) != 0 {
		t.Fatalf("BUG-283-CLASS REGRESSION: %d delta(s) delivered for a subscription Unsubscribed while its Publish cycle was mid-flight (pass 2) — pass 3's live-check should have dropped it: %+v", len(got), got)
	}

	// The SubscriptionServer itself must be clean afterward: a second,
	// fresh Publish cycle must produce nothing for the dead ID either.
	sink2 := &orderedSink{}
	s.Publish(sink2, protocol.Tick(2))
	if len(sink2.snapshot()) != 0 {
		t.Fatalf("stale subscription still being published to on a LATER cycle: %+v", sink2.snapshot())
	}
}

// TestRegression_SubscribeDuringPublishPass2_NoDuplicateOrOutOfOrderSeq
// (formerly TestAttack_SubscribeDuringPass2_NoDuplicateOrOutOfOrderSeq)
// attacks the other half of the same window: a brand-new Subscribe call
// arriving while a Publish cycle is between pass 1 and pass 3 (i.e.
// after the snapshot was taken, before targets are assigned) must not
// observe any Seq collision or duplicate delivery once it gets its own
// first publish cycle. Unchanged in substance; renamed only for
// consistency with this file's other regression tests.
func TestRegression_SubscribeDuringPublishPass2_NoDuplicateOrOutOfOrderSeq(t *testing.T) {
	s := NewSubscriptionServer()
	proceed := make(chan struct{})
	entered := make(chan struct{})
	callCount := 0
	var mu sync.Mutex
	if err := s.RegisterView("regression.slow2", func() (json.RawMessage, error) {
		mu.Lock()
		callCount++
		first := callCount == 1
		mu.Unlock()
		if first {
			close(entered)
			<-proceed
		}
		return json.Marshal(map[string]int{"ok": 1})
	}); err != nil {
		t.Fatalf("RegisterView: %v", err)
	}
	existingID, err := s.Subscribe("regression.slow2", nil, "", "existing")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	sink := &orderedSink{}
	publishDone := make(chan struct{})
	go func() {
		s.Publish(sink, protocol.Tick(1))
		close(publishDone)
	}()

	<-entered
	newID, err := s.Subscribe("regression.slow2", nil, "", "new-during-publish")
	if err != nil {
		t.Fatalf("Subscribe while Publish is mid-pass-2: %v", err)
	}
	if newID == existingID {
		t.Fatalf("new Subscribe during an in-flight Publish reused the existing SubscriptionID")
	}
	close(proceed)

	select {
	case <-publishDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Publish never completed")
	}

	// The new subscriber must NOT have received a delta from the cycle
	// that started before it existed (pass 1 already snapshotted the
	// target list without it).
	for _, d := range sink.snapshot() {
		if d.SubscriptionID == newID {
			t.Fatalf("new subscriber (registered mid-Publish) received a delta from a cycle that started before it subscribed: %+v", d)
		}
	}

	// Its own first publish cycle must start Seq at 1, cleanly.
	sink2 := &orderedSink{}
	s.Publish(sink2, protocol.Tick(2))
	found := false
	for _, d := range sink2.snapshot() {
		if d.SubscriptionID == newID {
			found = true
			if d.Seq != 1 {
				t.Fatalf("new subscriber's own first delta Seq = %d, want 1", d.Seq)
			}
		}
	}
	if !found {
		t.Fatal("new subscriber never received its own first delta on the following cycle")
	}
}

// TestRegression_StartSubscriptionPump_SecondCallRejected is the F1a
// fix's acceptance bar (formerly
// TestAttack_StartSubscriptionPump_CalledTwice_NoGuard, which found the
// defect: a second call started a second, concurrently-running pump
// goroutine with no error and no guard). StartSubscriptionPump now
// CompareAndSwaps a started flag before ever starting the goroutine — a
// second call on the same Engine must be rejected with
// ErrSubscriptionPumpAlreadyStarted, start no goroutine, and leave the
// FIRST pump's done channel (and the engine's live delta stream)
// completely unaffected.
func TestRegression_StartSubscriptionPump_SecondCallRejected(t *testing.T) {
	e := NewEngine()
	sink := &orderedSink{}
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	done1, err := e.StartSubscriptionPump(ctx1, sink)
	if err != nil {
		t.Fatalf("first StartSubscriptionPump: %v", err)
	}
	if done1 == nil {
		t.Fatal("first StartSubscriptionPump returned a nil done channel")
	}

	done2, err := e.StartSubscriptionPump(ctx2, sink)
	if err == nil {
		t.Fatal("second StartSubscriptionPump on the same Engine: accepted, want ErrSubscriptionPumpAlreadyStarted")
	}
	if !errors.Is(err, &errs.E{Code: ErrSubscriptionPumpAlreadyStarted}) {
		t.Errorf("second StartSubscriptionPump: err = %v, want ErrSubscriptionPumpAlreadyStarted", err)
	}
	if done2 != nil {
		t.Errorf("second StartSubscriptionPump returned a non-nil done channel %v, want nil (no goroutine should have started)", done2)
	}

	// The FIRST pump must still be live and functional: subscribing and
	// signalling must still produce a delta.
	if err := e.subs.RegisterView("regression.pumpstillworks", func() (json.RawMessage, error) {
		return json.RawMessage(`{"ok":1}`), nil
	}); err != nil {
		t.Fatalf("RegisterView: %v", err)
	}
	if _, err := e.subs.Subscribe("regression.pumpstillworks", nil, "", "regression-pump-still-works"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	e.signalSubscriptionPump()

	deadline := time.After(2 * time.Second)
	for len(sink.snapshot()) == 0 {
		select {
		case <-deadline:
			t.Fatal("the first (only) pump never delivered a delta after the rejected second start attempt — the first pump may have been left in a broken state")
		case <-time.After(5 * time.Millisecond):
		}
	}

	// cancel1 must cleanly stop the (single, real) pump goroutine —
	// done1 must close within a bounded time (F2's leak-proving contract,
	// exercised again more directly by TestRegression_StartSubscriptionPump_DoneChannel_ClosesOnCancel
	// in feat208_pump_lifecycle_test.go).
	cancel1()
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("done1 never closed after cancel1() — the pump goroutine leaked")
	}
}
