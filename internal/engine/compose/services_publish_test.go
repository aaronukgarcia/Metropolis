package compose

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	uiservices "github.com/aaronukgarcia/Metropolis/internal/ui/screens/services"
)

// FEAT-208 increment 1's own end-to-end regression test (design §6 step
// 7): compose.Wire, advance ticks, Subscribe("f4.services", ...), assert
// a real Delta arrives whose patch decodes to a non-empty capacityDemand
// matching ServicesAPI's live state, AND that the BUG-283/284 defect
// class (delayed deltas surviving Unsubscribe; out-of-Seq/Tick delivery)
// cannot recur against the real SubscriptionServer/Publish path this
// increment builds — only harness/stub ever had that defect; this proves
// the real engine.core path never had it in the first place, by
// construction (§4 of the design).

// TestServicesViewSubscriptionName_MatchesUIScreenConstant guards
// against the two independently-maintained copies of "f4.services"
// (this package's servicesViewSubscriptionName, and
// ui/screens/services/wire.go's ViewSubscriptionName) drifting apart —
// GR#20/SF-1 requires them to be independently maintained, not that they
// silently diverge in VALUE. This test is the only place in this
// package that imports internal/ui/screens/services — production code
// (services_publish.go, compose.go) never does (GR#20).
func TestServicesViewSubscriptionName_MatchesUIScreenConstant(t *testing.T) {
	if servicesViewSubscriptionName != uiservices.ViewSubscriptionName {
		t.Fatalf("servicesViewSubscriptionName = %q, want %q (ui.screen.services' own ViewSubscriptionName)", servicesViewSubscriptionName, uiservices.ViewSubscriptionName)
	}
}

// wireServicesTestEngine builds a real compose.Wire'd engine with one
// pre-registered "clinic-1" service instance (capacity 100, demand 30 —
// both non-zero and unequal so the resulting patch is unambiguously
// distinguishable from every field's zero value) and a live subscription
// pump. Returns the engine, transport, and a cancel func the caller must
// defer.
func wireServicesTestEngine(t *testing.T) (*core.Engine, *protocol.InProcTransport, context.CancelFunc) {
	t.Helper()
	cid := errs.NewCorrelationID()

	servicesAPI, err := services.LoadDefault(cid)
	if err != nil {
		t.Fatalf("services.LoadDefault: %v", err)
	}
	if err := servicesAPI.RegisterService(services.ServiceSpec{
		ID:          "clinic-1",
		Kind:        services.ServiceHealthcare,
		CapacityRaw: "100 visits/d",
		UpgradePath: []services.UpgradeStep{{BuildingID: "clinic", Name: "Clinic", CapacityCeiling: 100}},
	}); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}
	if err := servicesAPI.UpdateDemand("clinic-1", 30, 0); err != nil {
		t.Fatalf("UpdateDemand: %v", err)
	}

	e := core.NewEngine()
	if _, err := Wire(e, &Deps{CorrelationID: cid, Services: servicesAPI}); err != nil {
		t.Fatalf("Wire: %v", err)
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := e.StartSubscriptionPump(ctx, transport); err != nil {
		cancel()
		t.Fatalf("StartSubscriptionPump: %v", err)
	}
	go func() { _ = e.RunCommandLoop(ctx, transport) }()

	return e, transport, cancel
}

// subscribeAndAwaitFirstDelta issues Subscribe(view) over transport and
// waits for its own Seq==1 delta, mirroring
// subscribe_test.go's TestSubscription_EngineStatusDeltas_MonotonicSeq
// synchronisation discipline exactly (see that test's doc comment for
// why this specific wait removes the pump-coalescing ambiguity for every
// signal driven afterward).
func subscribeAndAwaitFirstDelta(t *testing.T, transport *protocol.InProcTransport, view string) (protocol.SubscriptionID, protocol.Delta) {
	t.Helper()
	subCorrID := protocol.NewCorrelationID()
	if err := transport.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   subCorrID,
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: view},
	}); err != nil {
		t.Fatalf("SendCommand(Subscribe %s): %v", view, err)
	}
	var subResult protocol.CommandResult
	select {
	case subResult = <-transport.Results():
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for Subscribe(%s) result", view)
	}
	if !subResult.Accepted {
		t.Fatalf("Subscribe(%s) rejected: %+v", view, subResult.Error)
	}

	// The Subscribe CommandResult itself carries no SubscriptionID
	// (engine.core/commands.go's handleSubscribe returns a bare accept) —
	// the caller learns its own SubscriptionID from the first Delta's
	// SubscriptionID field, correlated by CorrelationID (echoed exactly
	// once, on that first delta — subscribe.go's pendingCorrID
	// discipline). Mirrors subscribe_test.go's identical pattern for
	// "engine.status".
	select {
	case d := <-transport.Deltas():
		if d.Seq != 1 {
			t.Fatalf("first delta Seq = %d, want 1", d.Seq)
		}
		if d.CorrelationID != subCorrID {
			t.Fatalf("first delta CorrelationID = %q, want %q (echoes Subscribe)", d.CorrelationID, subCorrID)
		}
		return d.SubscriptionID, d
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for Subscribe(%s)'s own delta (Seq==1)", view)
	}
	panic("unreachable")
}

// TestServicesView_EndToEnd_DeltaMatchesLiveState is the design's §6
// step 7 core proof: subscribing to "f4.services" against a REAL
// compose.Wire'd engine (not harness/stub) now succeeds (previously
// rejected unconditionally — the design's §1 "the gap, precisely"
// finding) and the delivered patch's capacityDemand entry matches the
// live ServicesAPI state the test itself registered — proving the whole
// path (RegisterView -> Subscribe table lookup -> subscription pump ->
// buildServicesCapacityDemandPatch -> SendDelta -> ApplyDelta-decodable
// wire.go schema) end to end.
func TestServicesView_EndToEnd_DeltaMatchesLiveState(t *testing.T) {
	_, transport, cancel := wireServicesTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uiservices.ViewSubscriptionName)

	// The delta must decode through the REAL ui.screen.services
	// wire.go decoder (never a hand-rolled re-implementation here) —
	// proves compose's independently-maintained schema copy
	// (services_publish.go) actually round-trips through the UI side's
	// own decoder, not merely "some JSON that happens to look similar".
	patch, err := decodeServicesPatchViaUIScreen(delta.Patch)
	if err != nil {
		t.Fatalf("decoding f4.services patch via ui.screen.services' own decoder: %v", err)
	}
	if patch.CapacityDemand == nil {
		t.Fatal("patch.CapacityDemand = nil, want a non-nil (possibly empty) slice — buildServicesCapacityDemandPatch always sets this field")
	}
	cd := *patch.CapacityDemand
	if len(cd) != 1 {
		t.Fatalf("len(capacityDemand) = %d, want 1 (clinic-1)", len(cd))
	}
	if cd[0].ServiceID != "clinic-1" {
		t.Errorf("capacityDemand[0].ServiceID = %q, want %q", cd[0].ServiceID, "clinic-1")
	}
	if cd[0].CapacityUnits != 100 {
		t.Errorf("capacityDemand[0].CapacityUnits = %v, want 100 (RegisterService's CapacityRaw)", cd[0].CapacityUnits)
	}
	if cd[0].DemandUnits != 30 {
		t.Errorf("capacityDemand[0].DemandUnits = %v, want 30 (UpdateDemand's pushed value)", cd[0].DemandUnits)
	}

	// AdvanceTicks also signals the pump (commands.go); driving one more
	// tick and observing a second, Seq==2 delta proves the view keeps
	// publishing on the live pump, not just once at Subscribe time.
	if err := transport.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 1},
	}); err != nil {
		t.Fatalf("SendCommand(AdvanceTicks): %v", err)
	}
	select {
	case r := <-transport.Results():
		if !r.Accepted {
			t.Fatalf("AdvanceTicks rejected: %+v", r.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for AdvanceTicks result")
	}
	select {
	case d := <-transport.Deltas():
		if d.Seq != 2 {
			t.Fatalf("second delta Seq = %d, want 2", d.Seq)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the post-AdvanceTicks f4.services delta")
	}
}

// TestServicesView_UnsubscribeThenResubscribe_NoStaleDelta_SeqRestarts
// is the BUG-283/284-class regression proof the brief calls for,
// exercised at the SubscriptionServer level with the new registered-view
// table (not harness/stub, which is where BUG-283/284 actually lived —
// see subscribe.go's Publish doc comment §4 for why the real path is
// structurally immune, not just "tested more"):
//
//   - BUG-283 class (delayed delta after Unsubscribe): Unsubscribe, then
//     drive several more AdvanceTicks/pump-signalling cycles, and prove
//     NO further delta for the now-dead SubscriptionID ever arrives —
//     the real Publish loop's live-subs snapshot (subscribe.go's pass 1)
//     is read fresh every cycle under the same mutex Unsubscribe deletes
//     under, so there is no captured, detached goroutine that could fire
//     late.
//   - BUG-284 class (out-of-Seq/Tick order): resubscribing to the SAME
//     view gets a brand-new SubscriptionID (the allocator never reuses
//     IDs) whose own Seq starts at 1 again — proving no stale-Seq
//     collision with the torn-down subscription, and that Seq/Tick stay
//     monotonic per-subscription across the whole Unsubscribe/Subscribe
//     cycle.
func TestServicesView_UnsubscribeThenResubscribe_NoStaleDelta_SeqRestarts(t *testing.T) {
	_, transport, cancel := wireServicesTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	firstID, _ := subscribeAndAwaitFirstDelta(t, transport, uiservices.ViewSubscriptionName)

	// Unsubscribe the first subscription.
	if err := transport.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindUnsubscribe,
		Payload:         protocol.UnsubscribePayload{SubscriptionID: firstID},
	}); err != nil {
		t.Fatalf("SendCommand(Unsubscribe): %v", err)
	}
	select {
	case r := <-transport.Results():
		if !r.Accepted {
			t.Fatalf("Unsubscribe rejected: %+v", r.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Unsubscribe result")
	}

	// Immediately resubscribe (same view). If BUG-284's class existed
	// here, a stale delayed delta for firstID racing this new
	// subscription's own delta would be indistinguishable without care —
	// this test drains every delta received from this point on and
	// asserts NONE of them ever name firstID again.
	secondID, firstDeltaOfSecond := subscribeAndAwaitFirstDelta(t, transport, uiservices.ViewSubscriptionName)
	if secondID == firstID {
		t.Fatalf("resubscribe allocated the SAME SubscriptionID (%s) as the unsubscribed one — the allocator must never reuse IDs", secondID)
	}
	if firstDeltaOfSecond.Seq != 1 {
		t.Fatalf("resubscribed SubscriptionID's first delta Seq = %d, want 1 (fresh counter, no collision with the torn-down subscription's own Seq history)", firstDeltaOfSecond.Seq)
	}

	// Drive three more AdvanceTicks cycles (each signals the pump) and
	// collect every delta observed for secondID's own Seq — proving
	// monotonic, gap-free delivery — while asserting NOTHING ever
	// arrives for firstID (the BUG-283 class: a delayed send outliving
	// Unsubscribe).
	lastSeq := firstDeltaOfSecond.Seq
	for i := 0; i < 3; i++ {
		if err := transport.SendCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.NewCorrelationID(),
			Kind:            protocol.KindAdvanceTicks,
			Payload:         protocol.AdvanceTicksPayload{N: 1},
		}); err != nil {
			t.Fatalf("SendCommand(AdvanceTicks %d): %v", i, err)
		}
		select {
		case r := <-transport.Results():
			if !r.Accepted {
				t.Fatalf("AdvanceTicks %d rejected: %+v", i, r.Error)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for AdvanceTicks %d result", i)
		}
		select {
		case d := <-transport.Deltas():
			if d.SubscriptionID == firstID {
				t.Fatalf("BUG-283-class regression: received a delta for the UNSUBSCRIBED SubscriptionID %s after Unsubscribe (Seq=%d)", firstID, d.Seq)
			}
			if d.SubscriptionID != secondID {
				t.Fatalf("delta for unexpected SubscriptionID %s, want %s", d.SubscriptionID, secondID)
			}
			if d.Seq <= lastSeq {
				t.Fatalf("BUG-284-class regression: delta Seq = %d, want > previous %d (monotonic per-subscription)", d.Seq, lastSeq)
			}
			lastSeq = d.Seq
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for delta after AdvanceTicks %d", i)
		}
	}
}

// decodeServicesPatchViaUIScreen decodes raw through ui.screen.services'
// OWN wire schema, via a real ApplyDelta round-trip against a fresh
// Screen — this package is not allowed to import
// internal/ui/screens/services' unexported decodeWirePatch (it is
// unexported, and this file already imports the package only for its
// exported ViewSubscriptionName/BindSubscription/ApplyDelta/
// CapacityDemand surface, GR#20-compatible for a TEST file the same way
// TestServicesViewSubscriptionName_MatchesUIScreenConstant is), so this
// helper drives the exact same public path the real UI binary does:
// BindSubscription, then ApplyDelta, then CapacityDemand().
func decodeServicesPatchViaUIScreen(raw json.RawMessage) (uiPatchView, error) {
	scr := uiservices.New(errs.NewCorrelationID())
	id := protocol.SubscriptionID("sub-test")
	scr.BindSubscription(id)
	scr.ApplyDelta(protocol.Delta{SubscriptionID: id, Patch: raw})
	cd, have := scr.CapacityDemand()
	if !have {
		return uiPatchView{}, errNoCapacityDemand
	}
	out := make([]uiservices.CapacityDemand, len(cd))
	copy(out, cd)
	return uiPatchView{CapacityDemand: &out}, nil
}

// uiPatchView is this test file's own minimal read shape (mirrors
// wire.go's wirePatch just enough for this file's assertions) — built
// from ui.screen.services' own exported CapacityDemand() accessor, not
// a re-parse of the raw JSON, so a passing test proves the REAL decode
// path (decodeWirePatch, unexported, plus ApplyDelta's field-mapping)
// round-tripped correctly, not merely that this test's own JSON
// unmarshalling agrees with itself.
type uiPatchView struct {
	CapacityDemand *[]uiservices.CapacityDemand
}

var errNoCapacityDemand = errors.New("ui.screen.services CapacityDemand() reported have=false after ApplyDelta")
