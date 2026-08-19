package services

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func renderCapacityInto(cd []CapacityDemand, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(80, 10)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderCapacityDemand(buf, rect, cd, have, widgets.DefaultPalette, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

func renderWaitingInto(wl []WaitingList, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(80, 10)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderWaitingLists(buf, rect, wl, have, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

func bufsEqual(a, b *core.Buffer, rect core.Rect) bool {
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		for x := rect.X; x < rect.X+rect.W; x++ {
			if a.Get(x, y) != b.Get(x, y) {
				return false
			}
		}
	}
	return true
}

// TestSF3_CapacityDemandChanges is SF-3's differential single-field
// mutation check: two wire patches differ in exactly one figure (the
// police service's demandUnits), and (a) the capacity-vs-demand pane must
// render differently while (b) an untouched pane (waiting lists) must
// render byte-identically between the two runs — proving this screen
// reads the real subscribed field rather than hardcoding a value or
// wiring the wrong one.
func TestSF3_CapacityDemandChanges(t *testing.T) {
	base := fullPatch()
	mutated := fullPatch()

	// Differ in exactly one field: police's demandUnits.
	mutatedCD := []wireCapacityDemand{
		{ServiceID: "police", Label: "Police", CapacityUnits: 100, DemandUnits: 99},
		{ServiceID: "fire", Label: "Fire", CapacityUnits: 60, DemandUnits: 45},
	}
	mutated.CapacityDemand = &mutatedCD

	sA := New("corr-sf3-a")
	sA.BindSubscription("sub-a")
	sA.ApplyDelta(protocol.Delta{SubscriptionID: "sub-a", Patch: mustJSON(t, base)})

	sB := New("corr-sf3-b")
	sB.BindSubscription("sub-b")
	sB.ApplyDelta(protocol.Delta{SubscriptionID: "sub-b", Patch: mustJSON(t, mutated)})

	cdA, _ := sA.CapacityDemand()
	cdB, _ := sB.CapacityDemand()
	wlA, _ := sA.WaitingLists()
	wlB, _ := sB.WaitingLists()

	// Render capacity-vs-demand.
	ca, caRect := renderCapacityInto(cdA, true)
	cb, _ := renderCapacityInto(cdB, true)

	// Render waiting lists.
	wa, waRect := renderWaitingInto(wlA, true)
	wb, _ := renderWaitingInto(wlB, true)

	// a) The change in demandUnits must change the capacity/demand pane.
	if bufsEqual(ca, cb, caRect) {
		t.Error("capacity-vs-demand pane unchanged after mutating police demandUnits from 80 to 99 (a)")
	}

	// b) The untouched waiting-lists view must remain byte-identical.
	if !bufsEqual(wa, wb, waRect) {
		t.Error("waiting-lists pane changed even though its fields were untouched between the two runs (b)")
	}
}
