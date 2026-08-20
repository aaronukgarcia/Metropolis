package core

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-208 increment 1, independent round r2 (2026-08-20). This file
// started life as an attack-only file targeting the surface F1b's fix
// created: SendDelta ran INSIDE SubscriptionServer.mu's pass-3 critical
// section (subscribe.go's Publish), which was documented as safe because
// DeltaSink.SendDelta was CLAIMED to be non-blocking — but that contract
// lived only on protocol.Transport's doc comment, not on the DeltaSink
// interface Publish actually depended on. Two attacks found real,
// mechanically reachable hazards:
//
//   - a blocking DeltaSink stalled Subscribe/Unsubscribe/RegisterView
//     indefinitely (they all shared s.mu with the SendDelta call F1b
//     moved inside it);
//   - a DeltaSink that called back into the SubscriptionServer from
//     inside SendDelta (a routing/logging wrapper, not an exotic
//     scenario) permanently self-deadlocked the pump goroutine on the
//     non-reentrant s.mu.
//
// R3's fix (the two-mutex split — see subscribe.go's publishMu doc
// comment and Publish's own doc comment) closes both: publishMu now
// serializes an entire Publish cycle end-to-end (preserving F1b's
// ordering guarantee, more strongly than before), while s.mu — the ONLY
// mutex Subscribe/Unsubscribe/RegisterView ever take — is held only
// briefly, NEVER across SendDelta. This file is kept as the PERMANENT
// regression suite proving that: the first two tests are flipped from
// TestAttack_* (proving the hazard existed) to TestRegression_* (proving
// it no longer does); the third test proves subscribe-reentrancy is
// SPECIFICALLY now safe; a fourth, honestly-named test proves the ONE
// remaining prohibition R3 documents (a DeltaSink must never call back
// into Publish itself) still holds exactly as documented — that is
// intended, permanent behaviour, not a defect, so it is asserted as a
// self-deadlock, same as the original attack, just renamed to reflect
// that this is now confirming documented behaviour rather than
// attacking an undocumented one. The restart-one-shot test is kept
// unchanged, per the round's own instruction.

// blockingSink is a deliberately pathological DeltaSink: its SendDelta
// blocks until told to proceed. This models any future/test DeltaSink
// implementation that is not InProcTransport — nothing in the DeltaSink
// interface signature enforces its documented non-blocking contract
// (commands.go).
type blockingSink struct {
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func newBlockingSink() *blockingSink {
	return &blockingSink{release: make(chan struct{}), entered: make(chan struct{})}
}

func (b *blockingSink) SendDelta(protocol.Delta) bool {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return true
}

// TestRegression_BlockingDeltaSink_SubscribeAndRegisterViewCompletePromptly
// (formerly TestAttack_BlockingDeltaSink_StallsSubscribeUnsubscribeRegisterView,
// which found the hazard: F1b's "hold s.mu across SendDelta" fix let a
// blocking DeltaSink stall every other SubscriptionServer method too).
// R3's two-mutex split moved SendDelta onto publishMu-only, so a
// SendDelta call blocked indefinitely must no longer stall
// Subscribe/RegisterView (both take only s.mu, which Publish holds only
// briefly, in pass 1 and pass 3 — never during SendDelta).
func TestRegression_BlockingDeltaSink_SubscribeAndRegisterViewCompletePromptly(t *testing.T) {
	s := NewSubscriptionServer()
	if err := s.RegisterView("regression.blocking", func() (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}); err != nil {
		t.Fatalf("RegisterView: %v", err)
	}
	if _, err := s.Subscribe("regression.blocking", nil, "", "regression-blocking-corr"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	sink := newBlockingSink()
	publishDone := make(chan struct{})
	go func() {
		s.Publish(sink, protocol.Tick(1))
		close(publishDone)
	}()

	select {
	case <-sink.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("blockingSink.SendDelta was never entered — cannot run the proof")
	}

	// Publish is now blocked INSIDE SendDelta, holding publishMu (never
	// s.mu — R3). RegisterView and Subscribe, which only ever take
	// s.mu, must complete promptly regardless — this is the R3 fix's
	// acceptance bar, proven with a generous-but-bounded deadline so a
	// genuine regression (SendDelta somehow blocking s.mu again) fails
	// loudly rather than hanging the test suite.
	const promptDeadline = 500 * time.Millisecond

	registerDone := make(chan error, 1)
	go func() {
		registerDone <- s.RegisterView("regression.blocking2", func() (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
	}()
	select {
	case err := <-registerDone:
		if err != nil {
			t.Fatalf("RegisterView while a publish is blocked mid-SendDelta: %v", err)
		}
	case <-time.After(promptDeadline):
		t.Fatal("R3 REGRESSION: RegisterView did not complete promptly while a blocking DeltaSink.SendDelta call was in flight — s.mu is apparently held across SendDelta again (the F1b/r1 shape r2 rejected)")
	}

	subDone := make(chan error, 1)
	go func() {
		_, err := s.Subscribe("regression.blocking2", nil, "", "regression-blocking-corr2")
		subDone <- err
	}()
	select {
	case err := <-subDone:
		if err != nil {
			t.Fatalf("Subscribe while a publish is blocked mid-SendDelta: %v", err)
		}
	case <-time.After(promptDeadline):
		t.Fatal("R3 REGRESSION: Subscribe did not complete promptly while a blocking DeltaSink.SendDelta call was in flight")
	}

	// Release the blocked send so the goroutine can finish and the test
	// can clean up deterministically.
	close(sink.release)

	select {
	case <-publishDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish never completed after releasing the blocking sink")
	}
}

// reentrantSubscribeSink is a DeltaSink whose SendDelta calls back into
// Subscribe on the SAME SubscriptionServer, synchronously (e.g. a
// routing/logging wrapper that reacts to a delivered delta by adjusting
// a subscription — not an exotic scenario). Safe under R3: SendDelta
// holds only publishMu at the point of re-entry, and Subscribe only
// ever needs s.mu.
type reentrantSubscribeSink struct {
	s         *SubscriptionServer
	reentered chan struct{}
	resultCh  chan error
}

func (r *reentrantSubscribeSink) SendDelta(protocol.Delta) bool {
	close(r.reentered)
	_, err := r.s.Subscribe("regression.reentrant.fromdelta", nil, "", "regression-reentrant-subscribe")
	r.resultCh <- err
	return true
}

// TestRegression_ReentrantSubscribeFromDeltaSink_DoesNotDeadlock
// (formerly half of TestAttack_ReentrantDeltaSink_DeadlocksThePumpGoroutine,
// which used a DeltaSink that re-entered Subscribe and found it
// permanently deadlocked the pump goroutine under F1b's single-mutex
// shape). R3 makes this specific case — reentering Subscribe/
// Unsubscribe/RegisterView, NOT Publish — safe by construction: SendDelta
// runs under publishMu only, and Subscribe takes only s.mu, a completely
// different mutex the calling goroutine is not holding at that point.
func TestRegression_ReentrantSubscribeFromDeltaSink_DoesNotDeadlock(t *testing.T) {
	s := NewSubscriptionServer()
	if err := s.RegisterView("regression.reentrant", func() (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}); err != nil {
		t.Fatalf("RegisterView: %v", err)
	}
	if err := s.RegisterView("regression.reentrant.fromdelta", func() (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}); err != nil {
		t.Fatalf("RegisterView: %v", err)
	}
	if _, err := s.Subscribe("regression.reentrant", nil, "", "regression-reentrant-seed"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	sink := &reentrantSubscribeSink{s: s, reentered: make(chan struct{}), resultCh: make(chan error, 1)}
	publishReturned := make(chan struct{})
	go func() {
		s.Publish(sink, protocol.Tick(1))
		close(publishReturned)
	}()

	select {
	case <-sink.reentered:
	case <-time.After(2 * time.Second):
		t.Fatal("reentrantSubscribeSink.SendDelta was never entered — cannot run the proof")
	}

	// The reentrant Subscribe call itself must succeed promptly.
	select {
	case err := <-sink.resultCh:
		if err != nil {
			t.Fatalf("Subscribe re-entered from inside SendDelta: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("R3 REGRESSION: Subscribe re-entered from inside SendDelta did not complete — apparently deadlocked (s.mu should be free at this point, not held by the SendDelta caller)")
	}

	// Publish itself must also return (it is not the one being
	// re-entered, so nothing should ever block it here).
	select {
	case <-publishReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("R3 REGRESSION: Publish never returned after a DeltaSink re-entered Subscribe (not Publish) from inside SendDelta")
	}
}

// reentrantPublishSink is a DeltaSink whose SendDelta calls back into
// Publish on the SAME SubscriptionServer — the ONE case R3 leaves
// prohibited by design (see subscribe.go's Publish doc comment and
// commands.go's DeltaSink doc comment): Go's sync.Mutex is not
// reentrant, so re-entering Publish while already holding publishMu on
// this goroutine self-deadlocks permanently. There is no fix for this
// short of a recursive mutex (which Go deliberately does not provide)
// or abandoning the serialization F1b/R3 depend on — it is intended,
// documented, permanent behaviour, not a defect.
type reentrantPublishSink struct {
	s         *SubscriptionServer
	reentered chan struct{}
}

func (r *reentrantPublishSink) SendDelta(protocol.Delta) bool {
	close(r.reentered)
	// Synchronous re-entry into Publish on the SAME SubscriptionServer
	// whose publishMu the caller (this very Publish call's pass 4)
	// already holds on this goroutine — the documented prohibition.
	r.s.Publish(&orderedSink{}, protocol.Tick(2))
	return true
}

// TestDocumented_ReentrantPublishFromDeltaSink_SelfDeadlocks keeps
// proving the SAME behaviour the r2 attack originally found (a
// reentrant DeltaSink deadlocks the pump goroutine) — but renamed
// honestly, because under R3 this is no longer an undocumented hazard:
// it is the one explicitly documented prohibition on DeltaSink
// (commands.go) and on Publish (subscribe.go). This test proves the
// documentation is not aspirational — violating it really does still
// self-deadlock, exactly as warned, using a bounded-timeout race against
// a separate goroutine so the test binary itself never hangs (mirrors
// the original attack's own technique).
func TestDocumented_ReentrantPublishFromDeltaSink_SelfDeadlocks(t *testing.T) {
	s := NewSubscriptionServer()
	if err := s.RegisterView("documented.reentrant.publish", func() (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}); err != nil {
		t.Fatalf("RegisterView: %v", err)
	}
	if _, err := s.Subscribe("documented.reentrant.publish", nil, "", "documented-reentrant-publish-seed"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	sink := &reentrantPublishSink{s: s, reentered: make(chan struct{})}
	publishReturned := make(chan struct{})
	go func() {
		s.Publish(sink, protocol.Tick(1)) // never expected to return — documented prohibition
		close(publishReturned)
	}()

	select {
	case <-sink.reentered:
	case <-time.After(2 * time.Second):
		t.Fatal("reentrantPublishSink.SendDelta was never entered — cannot run the proof")
	}

	select {
	case <-publishReturned:
		t.Fatal("DOCUMENTED-BEHAVIOUR REGRESSION: Publish returned despite a DeltaSink re-entering Publish from inside SendDelta — commands.go's DeltaSink doc comment and subscribe.go's Publish doc comment both document this as a permanent self-deadlock on publishMu; if this now completes, either the documentation is stale or the serialization guarantee F1b/R3 depend on has silently changed shape.")
	case <-time.After(1 * time.Second):
		t.Log("CONFIRMED (documented, intended behaviour): a DeltaSink that calls back into Publish from within SendDelta permanently self-deadlocks the calling goroutine on publishMu, exactly as commands.go's DeltaSink doc comment and subscribe.go's Publish doc comment both warn. This is the ONE prohibition R3's two-mutex split leaves in place — every other form of re-entrancy (Subscribe/Unsubscribe/RegisterView) is proven safe by TestRegression_ReentrantSubscribeFromDeltaSink_DoesNotDeadlock above.")
	}
	// Deliberately no cleanup of the leaked, permanently-blocked goroutine
	// here: it is inherent to what this test demonstrates, exactly as the
	// original r2 attack's own doc comment noted. The process (this test
	// binary) exits normally regardless; Go does not wait for leaked
	// goroutines to finish.
}

// TestAttack_StartSubscriptionPump_NoRestartAfterCleanShutdown is kept
// exactly as the r2 attacker wrote it, per the round's own instruction
// ("keep the restart-one-shot test as-is"): pumpStarted (atomic.Bool,
// engine.go) is never reset anywhere in the diff, so StartSubscriptionPump
// is permanently one-shot per Engine — even AFTER the first pump has
// fully, cleanly exited (ctx cancelled, done channel closed), a second
// call on the SAME Engine still returns ErrSubscriptionPumpAlreadyStarted.
// That is a defensible design choice (simplicity: an Engine's pump
// lifecycle matches its process lifetime in every real caller today),
// but it was UNTESTED before this — F2's own lifecycle test file only
// proved cancel->done-closes, never restart legality — and the doc
// comment's "at most once per Engine" phrase is ambiguous between "at
// most one concurrently" and "at most one, ever" without a test pinning
// down which. This test pins it down: restart-after-clean-shutdown is
// NOT legal today.
func TestAttack_StartSubscriptionPump_NoRestartAfterCleanShutdown(t *testing.T) {
	e := NewEngine()
	sink := &orderedSink{}
	ctx1, cancel1 := context.WithCancel(context.Background())

	done1, err := e.StartSubscriptionPump(ctx1, sink)
	if err != nil {
		t.Fatalf("first StartSubscriptionPump: %v", err)
	}
	cancel1()
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("first pump never exited after cancel1()")
	}

	// The pump is now fully, cleanly stopped. Attempt to restart it.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	done2, err := e.StartSubscriptionPump(ctx2, sink)
	if err == nil {
		t.Fatalf("FINDING: restart after clean shutdown was ACCEPTED (done2=%v) — pumpStarted is apparently reset somewhere; if intentional, this needs its own test and doc update, since F2's lifecycle tests never exercise this path.", done2)
	}
	if !errors.Is(err, &errs.E{Code: ErrSubscriptionPumpAlreadyStarted}) {
		t.Fatalf("restart after clean shutdown: err = %v, want ErrSubscriptionPumpAlreadyStarted", err)
	}
	t.Logf("CONFIRMED (documented behavior, previously untested): StartSubscriptionPump is permanently one-shot per Engine — restart after a clean shutdown is refused with the same ErrSubscriptionPumpAlreadyStarted a concurrent double-start gets (err=%v). No caller in this codebase needs restart today, so this is not itself a defect, but it was unverified by any existing test before this one.", err)
}
