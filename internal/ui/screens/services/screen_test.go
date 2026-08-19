package services

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func newScreenWithData(t *testing.T, sub protocol.SubscriptionID) *Screen {
	t.Helper()
	s := New("corr-" + string(sub))
	s.BindSubscription(sub)
	s.ApplyDelta(protocolDelta(t, sub, fullPatch()))
	return s
}

func TestApplyDelta_PopulatesAllSubSurfaces(t *testing.T) {
	s := newScreenWithData(t, "sub-full")

	if !s.HaveData() {
		t.Fatal("HaveData() = false after a full patch")
	}
	if sliders, have := s.Sliders(); !have || len(sliders) != 2 {
		t.Errorf("Sliders() = %v, %v; want 2 sliders, have=true", sliders, have)
	}
	if cd, have := s.CapacityDemand(); !have || len(cd) != 2 {
		t.Errorf("CapacityDemand() = %v, %v; want 2 entries, have=true", cd, have)
	}
	if rt, have := s.ResponseTimes(); !have || len(rt) != 2 {
		t.Errorf("ResponseTimes() = %v, %v; want 2 entries, have=true", rt, have)
	}
	if wl, have := s.WaitingLists(); !have || len(wl) != 1 {
		t.Errorf("WaitingLists() = %v, %v; want 1 entry, have=true", wl, have)
	}
	// SVC-6 is BLOCKED — fullPatch never populates it, so it must report
	// have=false even after a full patch (proving this is the deliberate
	// BLOCKED state doc.go documents, not a bug in ApplyDelta).
	if _, have := s.PublicServicePie(); have {
		t.Error("PublicServicePie() reported have=true, but SVC-6 is BLOCKED and no fixture ever populates it")
	}
}

func TestApplyDelta_UnboundSubscriptionIsDroppedNotApplied(t *testing.T) {
	s := newScreenWithData(t, "sub-bound")

	// A delta for an unbound subscription must be dropped, not applied.
	bad := fullPatch()
	cd := *bad.CapacityDemand
	cd[0].DemandUnits = 999
	bad.CapacityDemand = &cd
	s.ApplyDelta(protocolDelta(t, "sub-ghost", bad))

	cdOut, _ := s.CapacityDemand()
	if cdOut[0].DemandUnits == 999 {
		t.Error("delta for an unknown subscription was applied (SF-7 violation)")
	}
}

func TestApplyDelta_MalformedPatchKeepsLastKnownGood(t *testing.T) {
	s := newScreenWithData(t, "sub-malformed")
	cdBefore, _ := s.CapacityDemand()

	// Invalid JSON.
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-malformed", Patch: []byte("{not json")})
	// Wrong schema version.
	s.ApplyDelta(protocolDelta(t, "sub-malformed", wirePatch{SchemaVersion: 99}))

	cdAfter, _ := s.CapacityDemand()
	if len(cdAfter) != len(cdBefore) || cdAfter[0].DemandUnits != cdBefore[0].DemandUnits {
		t.Error("malformed patch changed the screen's last-known-good state")
	}
}

func TestApplyDelta_OversizedPatchRejected(t *testing.T) {
	s := newScreenWithData(t, "sub-oversized")
	cdBefore, _ := s.CapacityDemand()

	huge := make([]byte, maxPatchWireBytes+1)
	for i := range huge {
		huge[i] = 'a'
	}
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-oversized", Patch: huge})

	cdAfter, _ := s.CapacityDemand()
	if len(cdAfter) != len(cdBefore) {
		t.Error("oversized patch was not rejected — last-known-good state changed")
	}
}

func TestApplyDelta_AbsentSubSurfaceMarksUnavailable(t *testing.T) {
	s := newScreenWithData(t, "sub-absent")

	// A patch carrying only sliders must mark every other sub-surface
	// unavailable and clear its previously-delivered data (SF-7: no stale
	// data).
	sliders := []wireServiceSlider{{ID: "police", Label: "Police", Value: 100, Min: 0, Max: 1000, Step: 10}}
	s.ApplyDelta(protocolDelta(t, "sub-absent", wirePatch{SchemaVersion: 1, Sliders: &sliders}))

	if _, have := s.CapacityDemand(); have {
		t.Error("CapacityDemand() reported have=true after a patch that omitted it (SF-7: should be unavailable, not stale)")
	}
	if _, have := s.ResponseTimes(); have {
		t.Error("ResponseTimes() reported have=true after a patch that omitted it (SF-7: should be unavailable, not stale)")
	}
	if _, have := s.WaitingLists(); have {
		t.Error("WaitingLists() reported have=true after a patch that omitted it (SF-7: should be unavailable, not stale)")
	}
}

func TestApplyResult_RejectionSurfacedThenClearedOnAccept(t *testing.T) {
	s := New("corr-result")

	// MET-G1203 (engine.services' own ErrInvalidFunding,
	// internal/engine/services/errors.go:52) is used here deliberately —
	// NOT this screen's own local MET-V503 — because this fixture
	// simulates an engine-side rejection (SVC-8's ApplyResult half), and
	// MET-V503 is this screen's own local pre-send validation code
	// (screen.go's SetFunding); reusing it here would mislabel a local
	// code as engine-originated.
	s.ApplyResult(protocol.CommandResult{
		CorrelationID: "corr-result",
		Accepted:      false,
		Error:         &protocol.ErrorRef{Code: "MET-G1203", Display: "funding cannot go below the statutory floor"},
	})
	if got := s.FundingRejectedReason(); got != "funding cannot go below the statutory floor" {
		t.Errorf("FundingRejectedReason() = %q, want the engine's rejection reason (SVC-8: never a silent revert)", got)
	}

	s.ApplyResult(protocol.CommandResult{CorrelationID: "corr-result", Accepted: true})
	if got := s.FundingRejectedReason(); got != "" {
		t.Errorf("FundingRejectedReason() = %q after an accepted result, want empty", got)
	}
}

func TestApplyResult_IgnoresMismatchedCorrelationID(t *testing.T) {
	s := New("corr-mine")
	// MET-G1203, not MET-V503 — see TestApplyResult_RejectionSurfacedThenClearedOnAccept's note.
	s.ApplyResult(protocol.CommandResult{
		CorrelationID: "corr-someone-else",
		Accepted:      false,
		Error:         &protocol.ErrorRef{Code: "MET-G1203", Display: "not for this screen"},
	})
	if got := s.FundingRejectedReason(); got != "" {
		t.Errorf("FundingRejectedReason() = %q, want empty for a result addressed to a different correlation ID", got)
	}
}

func TestSetFunding_RejectsInvalidValuesLocally(t *testing.T) {
	s := New("corr-setfunding")
	noop := func(protocol.Command) error { return nil }
	sl := ServiceSlider{ID: "police", Label: "Police", Min: 0, Max: 1000, Step: 10}

	for _, v := range []float64{-1, negInf(), posInf(), nan()} {
		if err := s.SetFunding(noop, sl, v); err == nil {
			t.Errorf("SetFunding(%v) = nil error, want ErrInvalidFundingRequest", v)
		}
	}
}

func TestSetFunding_SendsFixedOpString(t *testing.T) {
	s := New("corr-setfunding-ok")
	var got protocol.Command
	send := func(cmd protocol.Command) error {
		got = cmd
		return nil
	}
	sl := ServiceSlider{ID: "police", Label: "Police", Min: 0, Max: 1000, Step: 10}
	if err := s.SetFunding(send, sl, 250); err != nil {
		t.Fatalf("SetFunding: %v", err)
	}
	payload, ok := got.Payload.(protocol.DebugPayload)
	if !ok {
		t.Fatalf("Payload type = %T, want protocol.DebugPayload", got.Payload)
	}
	if payload.Op != opSetFunding {
		t.Errorf("Op = %q, want %q (ASM-1193 fixed Op string convention)", payload.Op, opSetFunding)
	}
	if payload.Args["id"] != "police" {
		t.Errorf("Args[id] = %q, want %q", payload.Args["id"], "police")
	}
}

// TestSetFunding_RescalesValueToEngineDomain is the slider-domain
// proof-of-failure half the REJECT r1 finding asked for: it proves the
// wire value SetFunding actually sends is the engine's [0,1] funding-level
// fraction (internal/engine/services/api.go:266-292's SetFunding domain),
// NOT the slider's raw UI display-domain value (250 out of a 0-1000
// display range must wire as "0.25", never "250" — a naive
// pass-value-through implementation that skipped the rescale would fail
// this assertion).
func TestSetFunding_RescalesValueToEngineDomain(t *testing.T) {
	s := New("corr-rescale")
	var got protocol.Command
	send := func(cmd protocol.Command) error {
		got = cmd
		return nil
	}
	sl := ServiceSlider{ID: "police", Label: "Police", Min: 0, Max: 1000, Step: 10}
	if err := s.SetFunding(send, sl, 250); err != nil {
		t.Fatalf("SetFunding: %v", err)
	}
	payload := got.Payload.(protocol.DebugPayload)
	if want := "0.25"; payload.Args["value"] != want {
		t.Errorf("Args[value] = %q, want %q (engine's [0,1] funding-level domain, internal/engine/services/api.go SetFunding — not the raw 250 display-domain value)", payload.Args["value"], want)
	}
}

// TestSetFunding_RejectsAboveEngineDomain is the rejection half of the
// slider-domain proof-of-failure: a rawValue above the slider's own
// display-domain Max rescales to a level > 1, which
// internal/engine/services/api.go:266-292's SetFunding would hard-reject
// — this screen must reject it locally with MET-V503 (and MUST NOT call
// send at all), mirroring the engine's rule rather than forwarding an
// out-of-domain value and hoping the engine catches it.
func TestSetFunding_RejectsAboveEngineDomain(t *testing.T) {
	s := New("corr-reject-domain")
	sendCalled := false
	send := func(protocol.Command) error {
		sendCalled = true
		return nil
	}
	sl := ServiceSlider{ID: "police", Label: "Police", Min: 0, Max: 1000, Step: 10}

	// 1500 out of a 0-1000 display domain rescales to level 1.5 > 1.
	err := s.SetFunding(send, sl, 1500)
	if err == nil {
		t.Fatal("SetFunding(1500) with Max=1000 = nil error, want rejection (level 1.5 exceeds the engine's [0,1] domain)")
	}
	if sendCalled {
		t.Error("SetFunding called send for an above-domain value — must reject locally with MET-V503, never forward an out-of-[0,1] value to the engine")
	}
}

func TestDrillTargets_RegistersDocumentedFigures(t *testing.T) {
	sliders := []ServiceSlider{{ID: "police", Label: "Police"}}
	cd := []CapacityDemand{{ServiceID: "police", Label: "Police"}}
	rt := []ResponseTimeStat{{ServiceID: "fire", Label: "Fire"}}
	wl := []WaitingList{{ID: "hospital-nonurgent", Label: "Hospital"}}

	targets := DrillTargets(sliders, cd, rt, wl)
	if len(targets) != 4 {
		t.Fatalf("DrillTargets produced %d targets, want 4", len(targets))
	}
	for _, tgt := range targets {
		if tgt.ViewName != ViewSubscriptionName {
			t.Errorf("target %q ViewName = %q, want %q", tgt.EntityID, tgt.ViewName, ViewSubscriptionName)
		}
		if !tgt.Valid() {
			t.Errorf("target %q is not valid (a dead end)", tgt.EntityID)
		}
	}
}

func TestRenderSliders_ShowsRejectionReason(t *testing.T) {
	buf := core.NewBuffer(80, 10)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderSliders(buf, rect, nil, "funding cannot go below the statutory floor", true, widgets.DefaultPalette.Style(widgets.TokenMoney))
	if !rowContains(renderedText(buf, rect), "Funding Rejected: funding cannot go below the statutory floor") {
		t.Error("RenderSliders did not render the rejection reason (SVC-8: never a silent revert)")
	}
}

func TestSF7_UnavailableStatesRenderExplicitText(t *testing.T) {
	s := New("corr-unavailable")
	sliders, haveS := s.Sliders()
	cd, haveCD := s.CapacityDemand()
	rt, haveRT := s.ResponseTimes()
	wl, haveWL := s.WaitingLists()
	pie, havePie := s.PublicServicePie()

	if haveS || haveCD || haveRT || haveWL || havePie {
		t.Fatal("a screen with no data yet reported have=true for some sub-surface")
	}

	sBuf, sRect := core.NewBuffer(80, 10), core.Rect{X: 0, Y: 0, W: 80, H: 10}
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	RenderSliders(sBuf, sRect, sliders, "", haveS, style)
	if !rowContains(renderedText(sBuf, sRect), "unavailable") {
		t.Error("RenderSliders with have=false did not render \"unavailable\"")
	}

	cdBuf, cdRect := renderCapacityInto(cd, haveCD)
	if !rowContains(renderedText(cdBuf, cdRect), "unavailable") {
		t.Error("RenderCapacityDemand with have=false did not render \"unavailable\"")
	}

	rtBuf, rtRect := core.NewBuffer(80, 10), core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderResponseTimes(rtBuf, rtRect, rt, haveRT, style)
	if !rowContains(renderedText(rtBuf, rtRect), "unavailable") {
		t.Error("RenderResponseTimes with have=false did not render \"unavailable\"")
	}

	wlBuf, wlRect := renderWaitingInto(wl, haveWL)
	if !rowContains(renderedText(wlBuf, wlRect), "unavailable") {
		t.Error("RenderWaitingLists with have=false did not render \"unavailable\"")
	}

	pieBuf, pieRect := core.NewBuffer(80, 10), core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderPublicServicePie(pieBuf, pieRect, pie, havePie, style)
	if !rowContains(renderedText(pieBuf, pieRect), "unavailable") {
		t.Error("RenderPublicServicePie with have=false did not render \"unavailable\"")
	}
}

func negInf() float64 { return -posInf() }
func posInf() float64 { x := math.MaxFloat64; return x * 2 }
func nan() float64    { z := 0.0; return z / z }
