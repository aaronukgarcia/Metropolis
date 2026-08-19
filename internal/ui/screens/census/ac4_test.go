package census

// AC-4 (§45 blue/white-collar workforce graph -- emergent, not
// hardcoded).

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func renderBlueWhiteInto(bwc BlueWhiteCollar, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(80, 10)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderBlueWhiteCollar(buf, rect, bwc, have, widgets.DefaultPalette, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

// TestBlueWhiteCollar_DifferentialChangesBars feeds two fixture deltas
// differing only in the blue/white split and asserts the rendered graph's
// two bars move accordingly. Lazy implementation this rejects: a bar pair
// whose proportions are computed from a screen-local constant rather than
// the subscribed field would satisfy "a graph renders two bars" while
// failing this differential check.
func TestBlueWhiteCollar_DifferentialChangesBars(t *testing.T) {
	base := fullPatch()
	mutated := fullPatch()
	mutatedBWC := wireBlueWhiteCollar{Blue: 500, White: 5500}
	mutated.BlueWhiteCollar = &mutatedBWC

	sA := New("corr-bwc-a")
	sA.BindSubscription("sub-bwc-a")
	sA.ApplyDelta(protocolDelta(t, "sub-bwc-a", base))
	sB := New("corr-bwc-b")
	sB.BindSubscription("sub-bwc-b")
	sB.ApplyDelta(protocolDelta(t, "sub-bwc-b", mutated))

	bwcA, _ := sA.BlueWhiteCollarSplit()
	bwcB, _ := sB.BlueWhiteCollarSplit()

	ba, baRect := renderBlueWhiteInto(bwcA, true)
	bb, _ := renderBlueWhiteInto(bwcB, true)
	if bufsEqual(ba, bb, baRect) {
		t.Error("blue/white-collar pane unchanged after mutating the split -- graph may be reading a screen-local constant, not the subscribed field")
	}
	if bwcA.Blue == bwcB.Blue && bwcA.White == bwcB.White {
		t.Error("fixture blue/white split did not actually differ -- test setup bug")
	}
}
