package main

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/router"
)

// DESTRUCTIVE ATTACK (GR#23 independent round, FEAT-208 increment 2).
// Attacks primeScreenSubscription's own coalescing-forwarding fix (the
// `primed` shared map, boot.go) — the builder's own doc comment admits
// this fix has NO deterministic regression test, only the end-to-end
// TestBootCore_FinanceAndServicesScreens_LiveOverRouter
// (feat208_router_test.go), which relies on real timing and does not
// reliably force the exact interleaving the fix exists for. This file
// builds that deterministic proof by driving primeScreenSubscription
// directly against a real *protocol.InProcTransport whose Result/Delta
// traffic is INJECTED by the test itself (via transport.SendResult/
// SendDelta, the same engine-facing send methods the real engine uses),
// rather than by a real engine/pump — this makes the race fully
// controllable: no timing luck, no flakiness, the exact interleaving
// happens on every run. Attack-only: no production code touched.

// TestRegression_PrimeScreenSubscription_ForwardsCoalescedDeltaForAlreadyPrimedScreen
// is the deterministic reproduction of the scenario boot.go's own
// `primed` doc comment describes: while priming screen 2 (finance),
// a delta for screen 1's (services) ALREADY-BOUND subscription arrives
// on the SAME transport.Deltas() channel BEFORE screen 2's own first
// delta does (modelling signalSubscriptionPump's coalescing: a single
// pump wake republishing every live subscription, not just the
// just-subscribed one). It must reach screen 1's applyDelta exactly
// once, with its Seq unmodified, and must NOT be dropped merely because
// its CorrelationID doesn't match screen 2's own priming handshake.
func TestRegression_PrimeScreenSubscription_ForwardsCoalescedDeltaForAlreadyPrimedScreen(t *testing.T) {
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	defer func() { _ = transport.Close() }()
	rt := router.New(transport)

	const (
		servicesSubID protocol.SubscriptionID = "sub-services-1"
		financeSubID  protocol.SubscriptionID = "sub-finance-1"
		servicesCorr  protocol.CorrelationID  = "corr-services"
		financeCorr   protocol.CorrelationID  = "corr-finance"
	)

	var mu sync.Mutex
	var servicesApplied []protocol.Delta
	var financeApplied []protocol.Delta
	servicesApply := func(d protocol.Delta) {
		mu.Lock()
		servicesApplied = append(servicesApplied, d)
		mu.Unlock()
	}
	financeApply := func(d protocol.Delta) {
		mu.Lock()
		financeApplied = append(financeApplied, d)
		mu.Unlock()
	}
	var servicesBoundID, financeBoundID protocol.SubscriptionID
	servicesBind := func(id protocol.SubscriptionID) { servicesBoundID = id }
	financeBind := func(id protocol.SubscriptionID) { financeBoundID = id }

	primed := make(map[protocol.SubscriptionID]func(protocol.Delta))

	// --- Prime services first (screen 1) ---
	// subscribe() here just injects services' own accept+first-delta,
	// mirroring what a real Subscribe command handled by the real engine
	// would produce, but under full test control.
	servicesSubscribe := func() error {
		go func() {
			transport.SendResult(protocol.CommandResult{CorrelationID: servicesCorr, Accepted: true})
			transport.SendDelta(protocol.Delta{
				SubscriptionID: servicesSubID,
				Tick:           1,
				Seq:            1,
				Patch:          []byte(`{}`),
				CorrelationID:  servicesCorr,
			})
		}()
		return nil
	}
	if err := primeScreenSubscription(transport, rt, primed, string(servicesCorr), "f4.services", servicesSubscribe, servicesBind, servicesApply); err != nil {
		t.Fatalf("priming services: %v", err)
	}
	if servicesBoundID != servicesSubID {
		t.Fatalf("services bound to %q, want %q", servicesBoundID, servicesSubID)
	}
	if len(servicesApplied) != 1 || servicesApplied[0].Seq != 1 {
		t.Fatalf("services applied = %+v, want exactly one delta with Seq=1", servicesApplied)
	}

	// --- Prime finance second (screen 2), but FIRST inject a coalesced
	// republish delta for services' ALREADY-BOUND subscription (Seq=2,
	// no CorrelationID — exactly what a real pump republish looks like:
	// subscribe.go's pendingCorrID is only echoed on a subscription's
	// very first delta, cleared thereafter), THEN finance's own
	// accept+first-delta. This deterministically reproduces the "screen
	// 2 priming observes screen 1's republished delta" scenario — no
	// timing luck: the injection order is fixed by this test, not by a
	// real pump's wake schedule.
	financeSubscribe := func() error {
		go func() {
			// The coalesced republish for services, arriving DURING
			// finance's priming window.
			transport.SendDelta(protocol.Delta{
				SubscriptionID: servicesSubID,
				Tick:           2,
				Seq:            2,
				Patch:          []byte(`{"n":2}`),
				CorrelationID:  "", // republish never carries a CorrelationID
			})
			// finance's own accept+first-delta.
			transport.SendResult(protocol.CommandResult{CorrelationID: financeCorr, Accepted: true})
			transport.SendDelta(protocol.Delta{
				SubscriptionID: financeSubID,
				Tick:           2,
				Seq:            1,
				Patch:          []byte(`{}`),
				CorrelationID:  financeCorr,
			})
		}()
		return nil
	}
	if err := primeScreenSubscription(transport, rt, primed, string(financeCorr), "f2.finance", financeSubscribe, financeBind, financeApply); err != nil {
		t.Fatalf("priming finance: %v", err)
	}
	if financeBoundID != financeSubID {
		t.Fatalf("finance bound to %q, want %q", financeBoundID, financeSubID)
	}

	// --- Assertions: the core of this attack ---
	mu.Lock()
	defer mu.Unlock()

	// finance must have received EXACTLY its own first delta — the
	// coalesced services delta must never be misrouted to finance.
	if len(financeApplied) != 1 || financeApplied[0].Seq != 1 || financeApplied[0].SubscriptionID != financeSubID {
		t.Fatalf("finance applied = %+v, want exactly one delta (Seq=1, own SubscriptionID)", financeApplied)
	}

	// services must have received EXACTLY TWO deltas: its own priming
	// delta (Seq=1) AND the coalesced republish (Seq=2) forwarded via
	// `primed` — no drop, no double-apply, Seq intact (unmodified from
	// what was injected).
	if len(servicesApplied) != 2 {
		t.Fatalf("services applied %d deltas, want 2 (priming delta + forwarded coalesced republish): %+v", len(servicesApplied), servicesApplied)
	}
	if servicesApplied[0].Seq != 1 || servicesApplied[1].Seq != 2 {
		t.Fatalf("services applied Seqs = [%d %d], want [1 2] (monotonic, Seq intact, no reordering)", servicesApplied[0].Seq, servicesApplied[1].Seq)
	}
	if servicesApplied[1].SubscriptionID != servicesSubID {
		t.Fatalf("forwarded coalesced delta SubscriptionID = %q, want %q", servicesApplied[1].SubscriptionID, servicesSubID)
	}
	if string(servicesApplied[1].Patch) != `{"n":2}` {
		t.Fatalf("forwarded coalesced delta Patch = %q, want the exact injected payload (no mutation in transit)", servicesApplied[1].Patch)
	}
}

// TestAttack_PrimeScreenSubscription_TimesOutWhenFirstDeltaNeverArrives
// probes the "what happens if the bounded handshake times out" question:
// a screen whose Subscribe is accepted but which NEVER receives its own
// first Delta (e.g. a registered-but-never-publishing view, or a pump
// that never wakes) must not hang bootCore forever — it must return a
// loud, honest error within feat208PrimeTimeout, never proceed silently
// unbound.
func TestAttack_PrimeScreenSubscription_TimesOutWhenFirstDeltaNeverArrives(t *testing.T) {
	original := feat208PrimeTimeout
	feat208PrimeTimeout = 150 * time.Millisecond
	defer func() { feat208PrimeTimeout = original }()

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	defer func() { _ = transport.Close() }()
	rt := router.New(transport)
	primed := make(map[protocol.SubscriptionID]func(protocol.Delta))

	const corr protocol.CorrelationID = "corr-never-delta"
	subscribe := func() error {
		// Accept arrives, but no Delta EVER follows — models a view that
		// is registered (Subscribe succeeds) but whose first publish
		// cycle never happens within the boot window.
		go func() { transport.SendResult(protocol.CommandResult{CorrelationID: corr, Accepted: true}) }()
		return nil
	}
	applied := 0
	bound := false

	start := time.Now()
	returned := make(chan error, 1)
	go func() {
		returned <- primeScreenSubscription(transport, rt, primed, string(corr), "f9.nevers", subscribe,
			func(protocol.SubscriptionID) { bound = true },
			func(protocol.Delta) { applied++ },
		)
	}()

	select {
	case err := <-returned:
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("R2/inc2 FINDING-CLASS: primeScreenSubscription returned nil (proceeded unbound) despite the first Delta never arriving — boot would silently continue with an unbound/half-primed screen")
		}
		if elapsed < feat208PrimeTimeout {
			t.Fatalf("primeScreenSubscription returned in %v, want >= the (lowered) timeout %v — it should have waited for the bound before giving up", elapsed, feat208PrimeTimeout)
		}
		t.Logf("CONFIRMED: primeScreenSubscription returns a loud error (%v) on timeout rather than hanging or proceeding silently unbound.", err)
	case <-time.After(2 * time.Second):
		t.Fatal("R2/inc2 REGRESSION: primeScreenSubscription hung well past feat208PrimeTimeout instead of returning a timeout error — a screen with no publishing view would hang bootCore forever")
	}

	if bound {
		t.Error("bind() was called despite the handshake timing out — must not partially bind on a failed prime")
	}
	if applied != 0 {
		t.Error("applyDelta() was called despite the handshake timing out")
	}
}

// TestRegression_PrimeScreenSubscription_ResultsChannelNotEatenForUnrelatedCorrelationID
// is the fix for the finding this test's own name used to describe as an
// attack: primeScreenSubscription drains transport.Results() directly
// during its own window, and a CommandResult whose CorrelationID does NOT
// match the priming call's own `want` used to be silently discarded
// (`continue`) — a real Go channel receive that could never be delivered
// to a router.RegisterResultHandler registered for it afterward. Not
// exploitable by TODAY's bootCore (no other command is in flight during
// priming — only the two screens' own Subscribes, and mapScreen's
// Subscribe happens strictly after both primes complete), but a real,
// previously-unenforced load-bearing assumption of the priming window
// ("bootCore is the transport's only reader during priming" — boot.go's
// own doc comment).
//
// Per the independent round's fix: rather than silently discarding a
// foreign CommandResult, primeScreenSubscription now fails boot loudly —
// a third concurrent command during priming is a programming error, not
// a droppable event. This test proves the loud-failure behaviour: a
// foreign CorrelationID's CommandResult arriving during the priming
// window now makes primeScreenSubscription return a non-nil,
// MET-E900-wrapped (codeBootFailure) error naming the foreign
// CorrelationID and the constraint it violated, rather than silently
// swallowing it and proceeding.
func TestRegression_PrimeScreenSubscription_ResultsChannelNotEatenForUnrelatedCorrelationID(t *testing.T) {
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	defer func() { _ = transport.Close() }()
	rt := router.New(transport)
	primed := make(map[protocol.SubscriptionID]func(protocol.Delta))

	const (
		primingCorr   protocol.CorrelationID = "corr-priming"
		unrelatedCorr protocol.CorrelationID = "corr-unrelated-inflight-command"
	)

	subscribe := func() error {
		go func() {
			// An UNRELATED command's result arrives during this
			// handshake's own window — models e.g. a devmode/debug
			// command issued concurrently at boot, or (in a future
			// increment) any other in-flight command whose result races
			// the priming read. This must now make priming fail loudly
			// rather than silently consume and discard it.
			transport.SendResult(protocol.CommandResult{CorrelationID: unrelatedCorr, Accepted: true})
			transport.SendResult(protocol.CommandResult{CorrelationID: primingCorr, Accepted: true})
			transport.SendDelta(protocol.Delta{
				SubscriptionID: "sub-eaten-test",
				Tick:           1,
				Seq:            1,
				Patch:          []byte(`{}`),
				CorrelationID:  primingCorr,
			})
		}()
		return nil
	}
	err := primeScreenSubscription(transport, rt, primed, string(primingCorr), "f9.eatentest", subscribe,
		func(protocol.SubscriptionID) {}, func(protocol.Delta) {},
	)
	if err == nil {
		t.Fatal("REGRESSION: primeScreenSubscription returned nil despite observing a CommandResult for a foreign, unrelated CorrelationID during its priming window — it must fail loudly (a third concurrent command at boot is a programming error, not a droppable event), never silently discard and proceed")
	}

	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("priming error %v is not a registry-sourced *errs.E", err)
	}
	if e.Code != codeBootFailure {
		t.Errorf("error code = %q, want %q (MET-E900)", e.Code, codeBootFailure)
	}
	if got, want := e.Ctx["foreignCorrelationID"], string(unrelatedCorr); got != want {
		t.Errorf("error context foreignCorrelationID = %v, want %q", got, want)
	}

	// Register a router result handler for the unrelated CorrelationID
	// the SAME way a real caller would (this models what would happen if
	// a real caller registered for it just after bootCore returns, then
	// started router.Run) — it must never fire: bootCore has already
	// failed and aborted before router.Run is ever started (boot.go
	// cancels/waits/closes the transport on a priming error), so nothing
	// downstream ever gets the chance to observe it either way. This is
	// the honest replacement for the old test's "prove the result is
	// unobservable" assertion — now it is unobservable because boot
	// failed loudly, not because it was silently eaten while boot
	// proceeded.
	ch := make(chan protocol.CommandResult, 1)
	rt.RegisterResultHandler(unrelatedCorr, resultReceiverFunc(func(r protocol.CommandResult) { ch <- r }))
	select {
	case r := <-ch:
		t.Fatalf("did not expect the unrelated result to be delivered to a router handler — got %+v", r)
	case <-time.After(200 * time.Millisecond):
		// Expected: nothing further ever reads this transport in this
		// test (router.Run was never started), so this timeout merely
		// confirms no stray delivery happens — the real proof above is
		// err's loud failure, not this channel's silence.
	}
}
