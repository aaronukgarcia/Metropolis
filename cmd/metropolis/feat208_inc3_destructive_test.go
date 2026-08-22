package main

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
	"github.com/aaronukgarcia/Metropolis/internal/ui/router"
	servicesscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/services"
)

// FEAT-208 increment 3, GR#23 independent round r1 — REGRESSION suite.
//
// This file WAS the round's destructive attack file (three TestAttack_*
// findings, all CONFIRMED, verdict REJECT). Per the coordinator's
// instruction on the reject verdict, these are kept as this fix's own
// acceptance bar: flipped to TestRegression_ names with INVERTED
// assertions (the finding must no longer reproduce) rather than deleted,
// so a future regression on any of the three findings fails loudly here,
// in the same file, against the same call shapes the original attack
// used.
//
// F-A (TestRegression_DuplicateFundingCommands_BothCorrelationIDsDeliver):
// every SetFunding command previously reused the screen's single, fixed
// s.correlationID (the same value Subscribe uses). Two commands in
// flight at once collapsed onto router.RegisterResultHandler's ONE
// pending-result slot per CorrelationID (its own one-shot contract), so
// the second command's CommandResult became an unrecoverable route miss,
// never reaching ApplyResult. Fix: Screen.SetFunding
// (internal/ui/screens/services/screen.go) now mints a fresh
// protocol.NewCorrelationID() per call and tracks it in
// s.pendingFunding, so two in-flight commands never collide on one
// router slot again.
//
// F-B part 1 (TestRegression_RegisterFundingAdjustKeys_SendFailureSurfacesLocally):
// the `adjust` closure inside RegisterFundingAdjustKeys discarded
// SetFunding's returned error (`_ = s.SetFunding(...)`) — a failed
// send() (e.g. protocol.ErrCommandQueueFull) was indistinguishable from
// success, since no CommandResult can ever arrive for a command that
// never left the client. Fix: SetFunding now records the failure on a
// NEW, separate surface, Screen.FundingLocalFailureReason() (MET-V504,
// GR#7) — deliberately not merged into FundingRejectedReason, which
// stays reserved for a real, authoritative engine rejection.
//
// F-B part 2 (TestRegression_RegisterFundingAdjustKeys_ResyncsOnRejection):
// the adjust closure's own client-side `current` optimistic tracker
// updated unconditionally and was never corrected after a rejected
// command, permanently drifting the client's baseline away from the
// engine's real, authoritative funding level. Fix: `current` is no
// longer a closure-private variable — RegisterFundingAdjustKeys now reads
// Screen.fundingBaseline(serviceID), and ApplyResult reverts
// fundingConfirmed[serviceID] back to the rejected request's own
// priorLevel (compare-before-revert, so a newer, still-outstanding
// request for the same service is never stomped).
//
// MINOR (TestRegression_RegisterFundingAdjustKeys_HonoursCountPrefix):
// Action.Run ignored ActionArgs.Count entirely, silently discarding any
// ui.keys count-prefix ("5 s f +"). Fix: countAdjust (screen.go) reads
// args.Count (defaulting to 1, defended locally) and scales the step by
// it.

// TestRegression_DuplicateFundingCommands_BothCorrelationIDsDeliver
// mirrors the original TestAttack_DuplicateCorrelationID_
// SecondCommandResultSilentlyDropped's double-issue-idempotency shape
// ("same slider keystroke twice fast, before the first result comes
// back") but drives it through Screen.SetFunding itself — the real
// production call path — instead of hand-crafting two router
// registrations under one shared, guessed CorrelationID. Two SetFunding
// calls fire back-to-back (both registrations landing before either
// CommandResult arrives, exactly the true hazard window); their
// CommandResults are then delivered in order — command 1 accepted,
// command 2 genuinely, distinctly REJECTED. Both must now be delivered:
// zero route misses, and the second (later) result's real rejection
// reason must reach FundingRejectedReason(), never silently lost to a
// route miss the way it was pre-fix.
func TestRegression_DuplicateFundingCommands_BothCorrelationIDsDeliver(t *testing.T) {
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	defer func() { _ = transport.Close() }()
	rt := router.New(transport)
	screen := servicesscreen.New("regression-dup-corr")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- rt.Run(ctx) }()

	sl := servicesscreen.ServiceSlider{ID: "clinic-1", Label: "Clinic", Min: 0, Max: 1, Step: 0.05}

	// Mirrors boot.go's sendServicesCommand exactly: register THIS
	// command's own CorrelationID with router BEFORE any result could
	// possibly arrive for it. No real transport delivery is needed here
	// (this test injects CommandResults directly, exactly as the original
	// attack did) — send only needs to perform the registration and
	// capture the CorrelationID SetFunding minted.
	captureAndRegister := func(dst *protocol.CorrelationID) servicesscreen.SendCommandFunc {
		return func(cmd protocol.Command) error {
			*dst = cmd.CorrelationID
			rt.RegisterResultHandler(cmd.CorrelationID, screen)
			return nil
		}
	}

	var corr1, corr2 protocol.CorrelationID
	// Both SetFunding calls (and therefore both router registrations)
	// happen back-to-back, BEFORE either result is sent below — the exact
	// "press the key again before the first result comes back" window the
	// original attack targeted.
	if err := screen.SetFunding(captureAndRegister(&corr1), sl, 0.55); err != nil {
		t.Fatalf("SetFunding (first): %v", err)
	}
	if err := screen.SetFunding(captureAndRegister(&corr2), sl, 0.60); err != nil {
		t.Fatalf("SetFunding (second): %v", err)
	}

	if corr1 == "" || corr2 == "" {
		t.Fatalf("expected two non-empty CorrelationIDs, got %q and %q", corr1, corr2)
	}
	if corr1 == corr2 {
		t.Fatalf("REGRESSION: both SetFunding calls minted the SAME CorrelationID (%q) — this is exactly finding F-A's shape (a fixed, reused ID collapsing two commands onto router's one pending-result slot)", corr1)
	}

	// Command 1's result: accepted.
	transport.SendResult(protocol.CommandResult{CorrelationID: corr1, Accepted: true})
	// Command 2's result: genuinely, distinctly rejected.
	transport.SendResult(protocol.CommandResult{CorrelationID: corr2, Accepted: false,
		Error: &protocol.ErrorRef{Code: "MET-G1203", Display: "rejected: MET-G1203"}})

	deadline := time.Now().Add(2 * time.Second)
	var reason string
	for time.Now().Before(deadline) {
		reason = screen.FundingRejectedReason()
		if reason != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reason == "" {
		t.Fatal("REGRESSION: FundingRejectedReason() still empty — command 2's real rejection never reached ApplyResult (F-A's route-miss shape may have recurred)")
	}
	if !strings.Contains(reason, "MET-G1203") {
		t.Fatalf("FundingRejectedReason() = %q, want it to contain command 2's own MET-G1203 rejection", reason)
	}
	if got := rt.RouteMissCount(); got != 0 {
		t.Fatalf("REGRESSION: router.RouteMissCount() = %d, want 0 — both distinct CorrelationIDs should have delivered cleanly, no route miss", got)
	}

	cancel()
	<-runDone
}

// TestRegression_RegisterFundingAdjustKeys_SendFailureSurfacesLocally
// mirrors the original TestAttack_RegisterFundingAdjustKeys_
// SwallowsSendCommandError's exact call shape (a send() that always
// fails, e.g. simulating protocol.ErrCommandQueueFull) but now asserts
// the failure IS surfaced — on FundingLocalFailureReason(), a surface
// distinct from FundingRejectedReason() (which must stay empty: the
// engine never adjudicated a command that never left the client).
func TestRegression_RegisterFundingAdjustKeys_SendFailureSurfacesLocally(t *testing.T) {
	screen := servicesscreen.New("regression-swallowed-send")
	g := keys.NewKeyGrammar(nil, 0, 0, "regression-swallowed-send")

	sendCalls := 0
	failingSend := func(protocol.Command) error {
		sendCalls++
		return errors.New("simulated protocol.ErrCommandQueueFull: transport command queue is full")
	}

	if err := screen.RegisterFundingAdjustKeys(g, failingSend, "clinic-1", 0.05, []string{"s", "f"}); err != nil {
		t.Fatalf("RegisterFundingAdjustKeys: %v", err)
	}

	if got := screen.FundingLocalFailureReason(); got != "" {
		t.Fatalf("FundingLocalFailureReason() before firing = %q, want empty", got)
	}
	if got := screen.FundingRejectedReason(); got != "" {
		t.Fatalf("FundingRejectedReason() before firing = %q, want empty", got)
	}

	for _, tok := range []string{"s", "f", "+"} {
		k, ok := keys.ParseKeyToken(tok)
		if !ok {
			t.Fatalf("ParseKeyToken(%q) failed", tok)
		}
		res := g.Feed(k)
		if tok == "+" && res.Status != keys.Dispatched {
			t.Fatalf("final Feed status = %v, want Dispatched", res.Status)
		}
	}

	if sendCalls != 1 {
		t.Fatalf("failingSend called %d times, want exactly 1 (the action fired but send() failed)", sendCalls)
	}

	local := screen.FundingLocalFailureReason()
	if local == "" {
		t.Fatal("REGRESSION: FundingLocalFailureReason() still empty after a failed send() — the failure is invisible again (F-B part 1)")
	}
	if !strings.Contains(local, "MET-V504") {
		t.Fatalf("FundingLocalFailureReason() = %q, want it to contain the registered local-failure code MET-V504", local)
	}
	if !strings.Contains(local, "ErrCommandQueueFull") {
		t.Fatalf("FundingLocalFailureReason() = %q, want it to carry the underlying send() cause for a selectable display (GR#1)", local)
	}
	// The engine never saw this command — FundingRejectedReason must NOT
	// be where a local, client-side failure gets reported (that would
	// mislabel it as an authoritative engine rejection).
	if got := screen.FundingRejectedReason(); got != "" {
		t.Fatalf("FundingRejectedReason() = %q after a LOCAL send failure, want empty — a client-side failure must never be reported through the engine-rejection surface", got)
	}
}

// TestRegression_RegisterFundingAdjustKeys_ResyncsOnRejection mirrors the
// original TestAttack_RegisterFundingAdjustKeys_
// OptimisticStateDoesNotResyncOnRejection's exact two-press,
// reject-in-between shape, but now captures the REAL CorrelationID
// SetFunding minted for the first command (rather than guessing a fixed
// one) and asserts the SECOND press re-attempts the SAME level the first
// press did (0.55), proving fundingConfirmed resynced to the rejected
// request's own priorLevel instead of silently drifting forward by a
// full step (0.60, the pre-fix finding).
func TestRegression_RegisterFundingAdjustKeys_ResyncsOnRejection(t *testing.T) {
	screen := servicesscreen.New("regression-optimistic-drift")
	g := keys.NewKeyGrammar(nil, 0, 0, "regression-optimistic-drift")

	var lastSent protocol.SetFundingPayload
	var lastCorrID protocol.CorrelationID
	rejectingSend := func(cmd protocol.Command) error {
		p, ok := cmd.Payload.(protocol.SetFundingPayload)
		if !ok {
			t.Fatalf("unexpected payload type %T", cmd.Payload)
		}
		lastSent = p
		lastCorrID = cmd.CorrelationID
		return nil // the SEND itself succeeds; the engine will reject it later (out of this closure's view)
	}

	if err := screen.RegisterFundingAdjustKeys(g, rejectingSend, "clinic-1", 0.05, []string{"s", "f"}); err != nil {
		t.Fatalf("RegisterFundingAdjustKeys: %v", err)
	}

	fire := func(sign string) {
		for _, tok := range []string{"s", "f", sign} {
			k, ok := keys.ParseKeyToken(tok)
			if !ok {
				t.Fatalf("ParseKeyToken(%q) failed", tok)
			}
			g.Feed(k)
		}
	}

	// Press "+" once: baseline 0.5 -> 0.55, command sent with level=0.55.
	fire("+")
	if lastSent.Level != 0.55 {
		t.Fatalf("first press: sent level = %v, want 0.55", lastSent.Level)
	}
	if lastCorrID == "" {
		t.Fatal("first press: SetFunding sent a command with an empty CorrelationID")
	}

	// The engine REJECTS that first command for real (using the ACTUAL
	// CorrelationID SetFunding minted, captured above — not a guessed
	// fixed ID, which would no longer match anything in pendingFunding
	// after finding F-A's fix).
	screen.ApplyResult(protocol.CommandResult{
		CorrelationID: lastCorrID,
		Accepted:      false,
		Error:         &protocol.ErrorRef{Code: "MET-G1204", Display: "service not yet unlocked"},
	})
	if got := screen.FundingRejectedReason(); got == "" {
		t.Fatal("FundingRejectedReason() empty after a simulated rejection — test setup is wrong")
	}

	// Press "+" again. fundingBaseline("clinic-1") should have resynced
	// to 0.5 (the level confirmed BEFORE the rejected attempt), so this
	// second press should ALSO send 0.55 — re-attempting the same,
	// still-true target — never 0.60 (which would mean the tracker never
	// resynced and kept compounding from the rejected, phantom 0.55).
	fire("+")
	if math.Abs(lastSent.Level-0.6) < 1e-9 {
		t.Fatal("REGRESSION: second press sent level~=0.60 — fundingConfirmed did not resync after the rejection (F-B part 2)")
	}
	if math.Abs(lastSent.Level-0.55) > 1e-9 {
		t.Fatalf("second press: sent level = %v, want ~0.55 (resynced to the pre-rejection confirmed level, then re-advanced by one step)", lastSent.Level)
	}
}

// TestRegression_RegisterFundingAdjustKeys_HonoursCountPrefix is the
// MINOR finding's own proof-of-failure: a count-prefixed dispatch ("5" "s"
// "f" "+" — ui.keys' AC-5 count-prefix, grammar.go) must adjust by
// Count*step in one dispatch, not silently discard Count and adjust by a
// single step regardless (the pre-fix behaviour, since Action.Run's
// ActionArgs parameter was ignored entirely).
func TestRegression_RegisterFundingAdjustKeys_HonoursCountPrefix(t *testing.T) {
	screen := servicesscreen.New("regression-count-prefix")
	g := keys.NewKeyGrammar(nil, 0, 0, "regression-count-prefix")

	var lastSent protocol.SetFundingPayload
	capture := func(cmd protocol.Command) error {
		p, ok := cmd.Payload.(protocol.SetFundingPayload)
		if !ok {
			t.Fatalf("unexpected payload type %T", cmd.Payload)
		}
		lastSent = p
		return nil
	}

	if err := screen.RegisterFundingAdjustKeys(g, capture, "clinic-1", 0.05, []string{"s", "f"}); err != nil {
		t.Fatalf("RegisterFundingAdjustKeys: %v", err)
	}

	// "5" "s" "f" "+" -- a count prefix of 5, then the increase action.
	// Baseline 0.5 + 5*0.05 = 0.75.
	for _, tok := range []string{"5", "s", "f", "+"} {
		k, ok := keys.ParseKeyToken(tok)
		if !ok {
			t.Fatalf("ParseKeyToken(%q) failed", tok)
		}
		res := g.Feed(k)
		if tok == "+" && res.Status != keys.Dispatched {
			t.Fatalf("final Feed status = %v, want Dispatched", res.Status)
		}
	}

	if math.Abs(lastSent.Level-0.75) > 1e-9 {
		t.Fatalf("REGRESSION: sent level = %v, want 0.75 (0.5 baseline + Count(5)*step(0.05) — the count prefix was silently discarded)", lastSent.Level)
	}
}
