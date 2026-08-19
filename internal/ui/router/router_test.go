package router

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// --- test fixtures -----------------------------------------------------

func newTestTransport() *protocol.InProcTransport {
	return protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer,
		protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer,
		protocol.DefaultDeltaBuffer,
	)
}

// waitForCondition polls cond until it is true or one second elapses,
// mirroring internal/ui/core/harness_test.go's identical helper (this is
// a distinct package, so it is restated rather than imported — ui/core's
// helper is unexported test-only code).
func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met within timeout")
	}
}

// stubResultScreen is a fake ResultReceiver a test can inspect — stands
// in for internal/ui/screens/finance's ApplyResult, which does not yet
// exist as a Go package in this worktree (doc.go).
type stubResultScreen struct {
	mu      sync.Mutex
	applied []protocol.CommandResult
}

func (s *stubResultScreen) ApplyResult(r protocol.CommandResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = append(s.applied, r)
}

func (s *stubResultScreen) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.applied)
}

func (s *stubResultScreen) last() protocol.CommandResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applied[len(s.applied)-1]
}

// stubDeltaScreen is a fake DeltaReceiver a test can inspect.
type stubDeltaScreen struct {
	mu      sync.Mutex
	applied []protocol.Delta
}

func (s *stubDeltaScreen) ApplyDelta(d protocol.Delta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = append(s.applied, d)
}

func (s *stubDeltaScreen) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.applied)
}

func (s *stubDeltaScreen) last() protocol.Delta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applied[len(s.applied)-1]
}

// panicResultReceiver panics on every ApplyResult call -- used to prove
// the router's recover-log-continue policy (doc.go).
type panicResultReceiver struct{}

func (panicResultReceiver) ApplyResult(protocol.CommandResult) {
	panic("panicResultReceiver: deliberate ApplyResult panic for TestReceiverPanic")
}

// panicDeltaReceiver panics on every ApplyDelta call.
type panicDeltaReceiver struct{}

func (panicDeltaReceiver) ApplyDelta(protocol.Delta) {
	panic("panicDeltaReceiver: deliberate ApplyDelta panic for TestReceiverPanic")
}

// panicEventReceiver panics on every ApplyEvent call.
type panicEventReceiver struct{}

func (panicEventReceiver) ApplyEvent(protocol.Event) {
	panic("panicEventReceiver: deliberate ApplyEvent panic for TestReceiverPanic")
}

// recordingEventReceiver appends its own tag to a shared, mutex-guarded
// order slice every time it receives an Event — used by the
// registration-order determinism test to prove dispatch order is fixed by
// registration, not iteration of any unordered structure.
type recordingEventReceiver struct {
	tag    string
	order  *[]string
	mu     *sync.Mutex
	events *[]protocol.Event
}

func (r *recordingEventReceiver) ApplyEvent(e protocol.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.order = append(*r.order, r.tag)
	*r.events = append(*r.events, e)
}

// --- ASM-1482 reachability (the defect this whole seam exists to close) --

// TestReachability_CommandResultRoutedToOwner is the ASM-1482 closer: a
// command's CorrelationID is registered against a stub finance-like
// screen, the "engine side" sends the matching CommandResult, and the
// screen's ApplyResult must be invoked with that exact result — proving
// the "correct but unreachable" method is now reached.
//
// Mutation proof: deleting the `entry.receiver.ApplyResult(res)` call in
// handleResult (router.go) makes this test fail with "ApplyResult was
// never called" — verified by hand during this build (temporarily
// commented the call, ran `go test ./internal/ui/router/... -run
// TestReachability`, observed the failure, restored the source from a
// scratch copy per GR#24).
func TestReachability_CommandResultRoutedToOwner(t *testing.T) {
	tr := newTestTransport()
	r := New(tr, WithCorrelationID("corr-router-test-1"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	finance := &stubResultScreen{}
	corrID := protocol.CorrelationID("corr-buy-land-1")
	r.RegisterResultHandler(corrID, finance)

	tr.SendResult(protocol.CommandResult{CorrelationID: corrID, Tick: 10, Accepted: true})

	waitForCondition(t, func() bool { return finance.count() == 1 })
	got := finance.last()
	if got.CorrelationID != corrID || got.Tick != 10 || !got.Accepted {
		t.Fatalf("ApplyResult got %+v, want CorrelationID=%q Tick=10 Accepted=true", got, corrID)
	}
}

// --- Correlation-match --------------------------------------------------

// TestCorrelationMatch_WrongCorrelationNeverDelivered asserts a
// CommandResult is routed ONLY to the screen whose registered
// CorrelationID matches it — a wrong-screen delivery (or any delivery at
// all for an unregistered correlation) fails this test.
func TestCorrelationMatch_WrongCorrelationNeverDelivered(t *testing.T) {
	tr := newTestTransport()
	r := New(tr, WithCorrelationID("corr-router-test-2"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	owner := &stubResultScreen{}
	other := &stubResultScreen{}
	r.RegisterResultHandler("corr-mine", owner)
	r.RegisterResultHandler("corr-other-owner", other)

	// A result for a THIRD, never-registered correlation ID must reach
	// neither receiver, and must be counted as a routing-table miss.
	tr.SendResult(protocol.CommandResult{CorrelationID: "corr-unregistered", Tick: 1, Accepted: true})
	// The genuinely-owned result must reach only its owner.
	tr.SendResult(protocol.CommandResult{CorrelationID: "corr-mine", Tick: 2, Accepted: true})

	waitForCondition(t, func() bool { return owner.count() == 1 })
	time.Sleep(20 * time.Millisecond) // let any wrong-delivery race land

	if other.count() != 0 {
		t.Fatalf("other.count() = %d, want 0 (wrong-correlation delivery)", other.count())
	}
	if owner.count() != 1 {
		t.Fatalf("owner.count() = %d, want 1", owner.count())
	}
	if owner.last().CorrelationID != "corr-mine" {
		t.Fatalf("owner received %+v, want CorrelationID=corr-mine", owner.last())
	}
	if r.RouteMissCount() != 1 {
		t.Fatalf("RouteMissCount() = %d, want 1 (the unregistered correlation)", r.RouteMissCount())
	}
}

// TestBindSubscription_DeltaRoutedOnlyToBoundScreen mirrors the
// correlation-match test for Deltas: a Delta is routed only to the
// screen bound to its SubscriptionID.
func TestBindSubscription_DeltaRoutedOnlyToBoundScreen(t *testing.T) {
	tr := newTestTransport()
	r := New(tr, WithCorrelationID("corr-router-test-3"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	mine := &stubDeltaScreen{}
	other := &stubDeltaScreen{}
	r.BindSubscription("sub-mine", mine)
	r.BindSubscription("sub-other", other)

	tr.SendDelta(protocol.Delta{SubscriptionID: "sub-mine", Tick: 1, Seq: 1, Patch: json.RawMessage(`{"a":1}`)})

	waitForCondition(t, func() bool { return mine.count() == 1 })
	time.Sleep(20 * time.Millisecond)

	if other.count() != 0 {
		t.Fatalf("other.count() = %d, want 0", other.count())
	}
	if string(mine.last().Patch) != `{"a":1}` {
		t.Fatalf("mine received patch %s, want {\"a\":1}", mine.last().Patch)
	}
}

// --- Registration-order determinism -------------------------------------

// TestRegistrationOrder_Deterministic asserts a fixed batch of Events
// dispatches to every matching registered route in REGISTRATION ORDER,
// byte-identical (well, string-slice-identical) across repeated runs —
// never a map-range order (GR#21/§7).
//
// Mutation proof: rewriting handleEvent's `for _, rt := range routes`
// loop to instead range over a map[string]eventRoute keyed by prefix
// makes this test flaky/fail across repeated runs (map iteration order is
// randomised per Go's spec) — verified by hand during this build
// (temporarily swapped the slice for an equivalent map, ran this test
// with `-count=20`, observed order mismatches, restored the source from a
// scratch copy per GR#24).
func TestRegistrationOrder_Deterministic(t *testing.T) {
	run := func() []string {
		tr := newTestTransport()
		r := New(tr, WithCorrelationID("corr-router-order"))

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = r.Run(ctx); close(done) }()
		defer func() { cancel(); <-done }()

		var order []string
		var events []protocol.Event
		var mu sync.Mutex

		// All three prefixes match Kind "ticker.crisis.gridlock" — order
		// of registration is C, A, B (deliberately not alphabetical) so a
		// map-range implementation's randomised order would be caught.
		r.RegisterEventRoute("ticker.crisis", &recordingEventReceiver{tag: "C", order: &order, mu: &mu, events: &events})
		r.RegisterEventRoute("ticker", &recordingEventReceiver{tag: "A", order: &order, mu: &mu, events: &events})
		r.RegisterEventRoute("ticker.crisis.gridlock", &recordingEventReceiver{tag: "B", order: &order, mu: &mu, events: &events})

		tr.SendEvent(protocol.Event{Kind: "ticker.crisis.gridlock", Tick: 1, Severity: protocol.SeverityWarning})

		waitForCondition(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(order) == 3
		})

		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(order))
		copy(out, order)
		return out
	}

	want := []string{"C", "A", "B"}
	for i := 0; i < 5; i++ {
		got := run()
		if len(got) != len(want) {
			t.Fatalf("run %d: order = %v, want %v", i, got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("run %d: order = %v, want %v (registration order)", i, got, want)
			}
		}
	}
}

// --- Drop semantics -------------------------------------------------------

// TestDropSemantics_EvictOldestLeavesNewest fills a 1-buffer Delta
// channel past capacity before the router starts draining, and asserts
// the transport's evict-oldest policy (transport.go) means only the
// NEWEST queued delta is ever delivered — never the stale first one.
//
// Mutation proof: temporarily changing trySendEvictOldest's eviction
// target from the oldest to a no-op (i.e. dropping the NEWEST instead)
// makes this test fail with "delivered patch was the stale one" —
// verified by hand (scratch-copy edit + restore per GR#24).
func TestDropSemantics_EvictOldestLeavesNewest(t *testing.T) {
	tr := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer,
		protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer,
		1, // deltaBuf = 1: the second SendDelta must evict the first
	)
	r := New(tr, WithCorrelationID("corr-router-drop"))

	sub := protocol.SubscriptionID("sub-drop")
	// Both sent BEFORE Run starts draining, so the second necessarily
	// evicts the first under the 1-slot buffer.
	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 1, Seq: 1, Patch: json.RawMessage(`{"stale":true}`)})
	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 2, Seq: 2, Patch: json.RawMessage(`{"stale":false}`)})

	screen := &stubDeltaScreen{}
	r.BindSubscription(sub, screen)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	waitForCondition(t, func() bool { return screen.count() >= 1 })
	time.Sleep(20 * time.Millisecond)

	if screen.count() != 1 {
		t.Fatalf("screen.count() = %d, want exactly 1 (the surviving newest delta)", screen.count())
	}
	if string(screen.last().Patch) != `{"stale":false}` {
		t.Fatalf("delivered patch = %s, want the NEWEST delta's patch ({\"stale\":false}) -- evict-oldest violated", screen.last().Patch)
	}
}

// TestDropSemantics_SeqTrackerReportsGap asserts that when a subscription's
// observed Seq stream skips values (the observable symptom of the
// transport's evict-oldest policy having discarded deltas in between),
// the router's SeqTracker detects it and DeltaGapCount reflects it —
// mirroring internal/ui/core's own TestViewsLoop_SeqGapSetsStaleness
// technique of asserting the gap directly off a Seq jump.
//
// Mutation proof: replacing the `if gap > 0 { r.deltaGaps.Add(gap) ...
// }` block in handleDelta with a no-op makes this test fail with
// "DeltaGapCount() = 0, want 3" — verified by hand (scratch-copy edit +
// restore per GR#24).
func TestDropSemantics_SeqTrackerReportsGap(t *testing.T) {
	tr := newTestTransport()
	r := New(tr, WithCorrelationID("corr-router-gap"))
	screen := &stubDeltaScreen{}
	sub := protocol.SubscriptionID("sub-gap")
	r.BindSubscription(sub, screen)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 1, Seq: 1, Patch: json.RawMessage(`{"a":1}`)})
	waitForCondition(t, func() bool { return screen.count() == 1 })

	// Seq jumps from 1 to 5: a gap of 3 -- three deltas the router never
	// observed (evicted by the transport, or never produced).
	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 2, Seq: 5, Patch: json.RawMessage(`{"a":5}`)})
	waitForCondition(t, func() bool { return screen.count() == 2 })

	if got := r.DeltaGapCount(); got != 3 {
		t.Fatalf("DeltaGapCount() = %d, want 3", got)
	}
}

// --- Routing-table miss (never silent, GR#1/GR#17) -----------------------

// TestRouteMiss_UnboundSubscriptionCountsAsMiss asserts a Delta for a
// subscription nobody bound is counted as a routing-table miss rather
// than being silently swallowed.
func TestRouteMiss_UnboundSubscriptionCountsAsMiss(t *testing.T) {
	tr := newTestTransport()
	r := New(tr, WithCorrelationID("corr-router-miss"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	tr.SendDelta(protocol.Delta{SubscriptionID: "sub-nobody-bound", Tick: 1, Seq: 1, Patch: json.RawMessage(`{}`)})

	waitForCondition(t, func() bool { return r.RouteMissCount() == 1 })
}

// --- No-wall-clock: Tick-driven staleness, not time.Now -------------------

// TestNoWallClock_PruningIsTickDrivenNotTimeDriven proves
// pruneStaleLocked's staleness decision is driven entirely by
// protocol.Tick values carried on routed messages, never by how much real
// wall-clock time elapsed: a pending registration survives an actual
// sleep as long as no message carrying an advanced Tick arrives, and is
// pruned only once a Tick advances past the TTL -- regardless of how
// little real time that took.
//
// Mutation proof: replacing pruneStaleLocked's Tick-cutoff comparison
// with a time.Since(...)-based one would prune the registration during
// the sleep below (before any Tick advances), making this test fail with
// "PendingResultCount() = 0, want 1 after sleep, before tick advance" --
// verified by hand (scratch-copy edit + restore per GR#24).
func TestNoWallClock_PruningIsTickDrivenNotTimeDriven(t *testing.T) {
	tr := newTestTransport()
	r := New(tr, WithCorrelationID("corr-router-noclock"), WithPendingResultTTL(2))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	owner := &stubResultScreen{}
	r.RegisterResultHandler("corr-slow", owner)
	if got := r.PendingResultCount(); got != 1 {
		t.Fatalf("PendingResultCount() = %d, want 1 immediately after registering", got)
	}

	// Real wall-clock time passes, but no message carrying an advanced
	// Tick is sent -- currentTick stays at 0, so the TTL=2 window has not
	// been exceeded no matter how long we actually sleep.
	time.Sleep(50 * time.Millisecond)
	if got := r.PendingResultCount(); got != 1 {
		t.Fatalf("PendingResultCount() = %d, want 1 after sleep, before any tick advance (pruning must be tick-driven, not wall-clock-driven)", got)
	}

	// Now advance the Tick past the TTL via an unrelated Delta on a
	// different, harmless subscription -- currentTick becomes 10, well
	// past registeredAt(0)+TTL(2), so the next prune sweep evicts it.
	tr.SendDelta(protocol.Delta{SubscriptionID: "sub-unrelated", Tick: 10, Seq: 1, Patch: json.RawMessage(`{}`)})

	waitForCondition(t, func() bool { return r.PendingResultCount() == 0 })
	if r.RouteMissCount() == 0 {
		t.Fatal("RouteMissCount() = 0, want >0 after a stale-pruned registration (MET-V400 must be raised, never silent)")
	}
	if owner.count() != 0 {
		t.Fatalf("owner.count() = %d, want 0 -- a pruned registration must never later receive a stray ApplyResult", owner.count())
	}
}

// --- Receiver panic recovery (GR#1: recover, log, continue) --------------

// TestReceiverPanic_ResultReceiver_RunSurvivesAndContinuesRouting proves
// (a) Run's goroutine survives a panicking ResultReceiver.ApplyResult
// (never escapes to this test's own goroutine -- if it did, the whole
// `go test` process would crash, not merely fail this one test), (b)
// PanicCount() is incremented, and (c) a LATER, unrelated CommandResult
// still routes normally afterward -- one poisoned message never stops
// routing (T2 lossy-by-design, doc.go).
func TestReceiverPanic_ResultReceiver_RunSurvivesAndContinuesRouting(t *testing.T) {
	tr := newTestTransport()
	r := New(tr, WithCorrelationID("corr-router-panic-result"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	if got := r.PanicCount(); got != 0 {
		t.Fatalf("PanicCount() = %d, want 0 before any panic", got)
	}

	r.RegisterResultHandler("corr-panics", panicResultReceiver{})
	tr.SendResult(protocol.CommandResult{CorrelationID: "corr-panics", Tick: 1, Accepted: true})

	waitForCondition(t, func() bool { return r.PanicCount() == 1 })

	// A LATER, unrelated CommandResult must still be routed normally --
	// proving Run's select loop is still alive and dispatching, not stuck
	// or dead after the panic.
	survivor := &stubResultScreen{}
	r.RegisterResultHandler("corr-survivor", survivor)
	tr.SendResult(protocol.CommandResult{CorrelationID: "corr-survivor", Tick: 2, Accepted: true})

	waitForCondition(t, func() bool { return survivor.count() == 1 })
	if survivor.last().CorrelationID != "corr-survivor" {
		t.Fatalf("survivor received %+v, want CorrelationID=corr-survivor", survivor.last())
	}

	// If the goroutine's panic HAD escaped uncaught, the process would
	// already be dead and this line would never execute -- reaching here
	// at all is itself part of proof (c).
	if got := r.PanicCount(); got != 1 {
		t.Fatalf("PanicCount() = %d, want exactly 1", got)
	}
}

// TestReceiverPanic_DeltaReceiver_SameSubscriptionKeepsBeingAttempted
// proves the same policy for DeltaReceiver: a bound subscription whose
// receiver panics does not get unbound or poison other subscriptions --
// a later Delta for a DIFFERENT, healthy subscription still delivers.
func TestReceiverPanic_DeltaReceiver_SameSubscriptionKeepsBeingAttempted(t *testing.T) {
	tr := newTestTransport()
	r := New(tr, WithCorrelationID("corr-router-panic-delta"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	r.BindSubscription("sub-panics", panicDeltaReceiver{})
	tr.SendDelta(protocol.Delta{SubscriptionID: "sub-panics", Tick: 1, Seq: 1, Patch: json.RawMessage(`{}`)})

	waitForCondition(t, func() bool { return r.PanicCount() == 1 })

	healthy := &stubDeltaScreen{}
	r.BindSubscription("sub-healthy", healthy)
	tr.SendDelta(protocol.Delta{SubscriptionID: "sub-healthy", Tick: 2, Seq: 1, Patch: json.RawMessage(`{"ok":true}`)})

	waitForCondition(t, func() bool { return healthy.count() == 1 })

	if got := r.PanicCount(); got != 1 {
		t.Fatalf("PanicCount() = %d, want exactly 1", got)
	}
}

// TestReceiverPanic_EventReceiver mirrors the above for EventReceiver.
func TestReceiverPanic_EventReceiver(t *testing.T) {
	tr := newTestTransport()
	r := New(tr, WithCorrelationID("corr-router-panic-event"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	r.RegisterEventRoute("crisis", panicEventReceiver{})
	tr.SendEvent(protocol.Event{Kind: "crisis.flood", Tick: 1, Severity: protocol.SeverityCritical})

	waitForCondition(t, func() bool { return r.PanicCount() == 1 })

	var order []string
	var mu sync.Mutex
	var events []protocol.Event
	r.RegisterEventRoute("ticker", &recordingEventReceiver{tag: "ok", order: &order, mu: &mu, events: &events})
	tr.SendEvent(protocol.Event{Kind: "ticker.milestone", Tick: 2, Severity: protocol.SeverityInfo})

	waitForCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) == 1
	})

	if got := r.PanicCount(); got != 1 {
		t.Fatalf("PanicCount() = %d, want exactly 1", got)
	}
}
