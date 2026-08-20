package main

import (
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// FEAT-208 increment 3's own end-to-end proof: the services.set-funding
// pilot command, all the way from a fed keystroke through
// servicesscreen.RegisterFundingAdjustKeys' registered ui.keys action,
// the real protocol.KindSetFunding command, engine.core's real
// HandleCommand/handleGameplay seam, back through w.router
// (RegisterResultHandler, registered per-command by boot.go's
// sendServicesCommand closure) to Screen.ApplyResult/
// FundingRejectedReason — proving increment 2's own gating note ("no
// protocol.Command/CorrelationID-bearing dispatch path reaches
// ServicesAPI.SetFunding") is now false.
//
// "clinic-1" (feat208PilotServiceID) is NOT a registered service instance
// in a freshly-booted real engine (baseline one wires no automatic
// engine.build -> engine.services registration bridge — see
// feat208PilotServiceID's own doc comment and
// compose/services_publish.go's identical note for the publish side), so
// this drives the REJECTION half of SVC-8 for real: ServicesAPI.SetFunding
// itself rejects with ErrServiceNotRegistered, and that registry-sourced
// reason must reach FundingRejectedReason() — never a silent drop, never
// a panic, never an Accepted result for a service that was never
// registered.

func TestBootCore_FundingAdjustKey_RoundTripsRejectionToApplyResult(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("feat208-inc3-command-path", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	if w.keyGrammar == nil {
		t.Fatal("bootCore did not construct w.keyGrammar (FEAT-208 increment 3's input call site did not take)")
	}

	// Sanity: FundingRejectedReason starts empty (no command issued yet).
	if got := w.servicesScreen.FundingRejectedReason(); got != "" {
		t.Fatalf("FundingRejectedReason() before any command = %q, want empty", got)
	}

	// Feed the registered mnemonic path ("4" "f" "+") one token at a
	// time, exactly as FeedTcellEvent would from real keystrokes (AC-21's
	// "the ONE place a raw tcell key event becomes a dispatch decision" —
	// this test drives Feed directly, the tcell-independent half of that
	// same contract, since no tcell.Screen exists in this test).
	path := append(append([]string{}, feat208PilotFundingKeyPath...), "+")
	var lastStatus keys.FeedStatus
	for _, tok := range path {
		k, ok := keys.ParseKeyToken(tok)
		if !ok {
			t.Fatalf("ParseKeyToken(%q) failed", tok)
		}
		res := w.keyGrammar.Feed(k)
		lastStatus = res.Status
	}
	if lastStatus != keys.Dispatched {
		t.Fatalf("final Feed status = %v, want Dispatched (mnemonic path %v did not complete)", lastStatus, path)
	}

	// The action's Run closure calls Screen.SetFunding synchronously,
	// which calls sendServicesCommand synchronously (registers with
	// router, then transport.SendCommand) — but the CommandResult itself
	// arrives asynchronously via engine.core.RunCommandLoop + router.Run.
	// Poll FundingRejectedReason() with a bounded wall-clock wait (boot-time
	// integration-test synchronization, not engine determinism, mirroring
	// feat208_router_test.go's identical pattern).
	deadline := time.Now().Add(2 * time.Second)
	var reason string
	for time.Now().Before(deadline) {
		reason = w.servicesScreen.FundingRejectedReason()
		if reason != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reason == "" {
		t.Fatal("FundingRejectedReason() still empty after the funding-adjust key fired — the CommandResult never reached ApplyResult")
	}
	t.Logf("FundingRejectedReason() = %q (expected: %s is not a registered service in a fresh baseline-one boot)", reason, feat208PilotServiceID)
}

// TestBootCore_FundingAdjustKey_RejectionCarriesServicesRegistryCode
// proves the rejection reaching FundingRejectedReason() is genuinely
// ServicesAPI.SetFunding's own ErrServiceNotRegistered (MET-G1202) — not
// some other, unrelated envelope/decode failure that would ALSO leave
// FundingRejectedReason non-empty. This is the concrete evidence that the
// engine actually decoded a real protocol.KindSetFunding command and
// routed it into engine.services (via compose.handleGameplay), rather
// than the command silently misrouting or the Kind promotion not having
// taken (a mismatched/undecodable Kind would surface a DIFFERENT
// registry code family — ErrUnhandledCommandKind/ErrInvalidEnvelope, not
// engine.services' own MET-G1202).
func TestBootCore_FundingAdjustKey_RejectionCarriesServicesRegistryCode(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("feat208-inc3-kind-check", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	path := append(append([]string{}, feat208PilotFundingKeyPath...), "-")
	for _, tok := range path {
		k, ok := keys.ParseKeyToken(tok)
		if !ok {
			t.Fatalf("ParseKeyToken(%q) failed", tok)
		}
		if res := w.keyGrammar.Feed(k); res.Status != keys.Pending && res.Status != keys.Dispatched {
			t.Fatalf("Feed(%q) status = %v, want Pending or Dispatched", tok, res.Status)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	var reason string
	for time.Now().Before(deadline) {
		reason = w.servicesScreen.FundingRejectedReason()
		if reason != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reason == "" {
		t.Fatal("FundingRejectedReason() still empty — the decrease action never round-tripped")
	}
	const wantCode = "MET-G1202" // services.ErrServiceNotRegistered
	if !strings.Contains(reason, wantCode) {
		t.Fatalf("FundingRejectedReason() = %q, want it to contain %q (proves engine.services itself rejected it, not an envelope/routing failure)", reason, wantCode)
	}
}
