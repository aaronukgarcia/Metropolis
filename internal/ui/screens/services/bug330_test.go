package services

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestBUG330_EmptyRosterRendersNoData pins the BUG-330 services half: an
// empty roster (have=true but zero rows — what f4.services publishes every
// frame) must render an honest "no data" empty state, never a heading with
// zero rows that is indistinguishable from a broken screen.
func TestBUG330_EmptyRosterRendersNoData(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)

	sBuf, sRect := core.NewBuffer(80, 10), core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderSliders(sBuf, sRect, []ServiceSlider{}, "", true, style)
	if !rowContains(renderedText(sBuf, sRect), "no data") {
		t.Error("RenderSliders with have=true and an empty roster did not render \"no data\"")
	}

	cdBuf, cdRect := renderCapacityInto([]CapacityDemand{}, true)
	if !rowContains(renderedText(cdBuf, cdRect), "no data") {
		t.Error("RenderCapacityDemand with have=true and an empty roster did not render \"no data\"")
	}

	rtBuf, rtRect := core.NewBuffer(80, 10), core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderResponseTimes(rtBuf, rtRect, []ResponseTimeStat{}, true, style)
	if !rowContains(renderedText(rtBuf, rtRect), "no data") {
		t.Error("RenderResponseTimes with have=true and an empty roster did not render \"no data\"")
	}

	wlBuf, wlRect := renderWaitingInto([]WaitingList{}, true)
	if !rowContains(renderedText(wlBuf, wlRect), "no data") {
		t.Error("RenderWaitingLists with have=true and an empty roster did not render \"no data\"")
	}

	pieBuf, pieRect := core.NewBuffer(80, 10), core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderPublicServicePie(pieBuf, pieRect, PublicServicePieView{Slices: []PieSlice{}}, true, style)
	if !rowContains(renderedText(pieBuf, pieRect), "no data") {
		t.Error("RenderPublicServicePie with have=true and an empty roster did not render \"no data\"")
	}
}

// TestBUG330_EmptyRosterWirePathReportsHaveTrue proves the distinction the
// fix relies on: an EMPTY roster on the wire (an empty JSON array, exactly
// what f4.services publishes every frame) decodes to have=true with zero
// rows — NOT have=false — so the empty-but-present case is real and must
// be rendered as "no data" rather than blank rows.
func TestBUG330_EmptyRosterWirePathReportsHaveTrue(t *testing.T) {
	s := New("corr-empty-roster")
	empty := []wireServiceSlider{}
	s.BindSubscription("sub-empty")
	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-empty",
		Patch:          mustJSON(t, wirePatch{SchemaVersion: 1, Sliders: &empty}),
	})

	sliders, have := s.Sliders()
	if !have {
		t.Fatal("empty sliders array on the wire reported have=false, want have=true (the roster was present, just empty)")
	}
	if len(sliders) != 0 {
		t.Fatalf("len(sliders) = %d, want 0", len(sliders))
	}
}
