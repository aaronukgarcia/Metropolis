package proj

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// renderCurveInto renders one curve into a fresh buffer, returning the
// buffer and the chart rect (label row excluded) for byte-comparison.
func renderCurveInto(c Curve) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(40, 8)
	rect := core.Rect{X: 0, Y: 0, W: 40, H: 8}
	RenderCurve(buf, rect, c, widgets.DefaultPalette)
	return buf, core.Rect{X: 0, Y: 1, W: 40, H: 7}
}

// bufsEqual reports whether two buffers are byte-identical over rect
// (rune and style).
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

// TestSF3_OneCurveProjectionChanges is this package's instance of the
// shared SF-3 shape: two patches differing in exactly one curve's
// projection value must (a) change that curve's rendered chart and (b)
// leave a second, untouched curve's rendered chart byte-identical — so a
// screen hardcoding a value, computing independently of the subscribed
// view, or wiring the wrong field fails it.
func TestSF3_OneCurveProjectionChanges(t *testing.T) {
	patchA := wirePatch{
		SchemaVersion: 1, HorizonMonths: 72,
		Curves: []wireCurve{
			{Key: "water.demand", Status: "available", History: []float64{10, 11}, Projection: []float64{12, 13, 14}},
			{Key: "power.demand", Status: "available", History: []float64{20, 21}, Projection: []float64{22, 23, 24}},
		},
	}
	patchB := wirePatch{
		SchemaVersion: 1, HorizonMonths: 72,
		Curves: []wireCurve{
			{Key: "water.demand", Status: "available", History: []float64{10, 11}, Projection: []float64{50, 60, 70}}, // mutated
			{Key: "power.demand", Status: "available", History: []float64{20, 21}, Projection: []float64{22, 23, 24}},
		},
	}

	sA := New("corr-sf3-a")
	sA.BindSubscription("sub-a")
	sA.ApplyDelta(protocol.Delta{SubscriptionID: "sub-a", Patch: mustJSON(t, patchA)})
	curvesA, _ := sA.Curves()

	sB := New("corr-sf3-b")
	sB.BindSubscription("sub-b")
	sB.ApplyDelta(protocol.Delta{SubscriptionID: "sub-b", Patch: mustJSON(t, patchB)})
	curvesB, _ := sB.Curves()

	waterA, waterRect := renderCurveInto(curvesA[0])
	waterB, _ := renderCurveInto(curvesB[0])
	powerA, powerRect := renderCurveInto(curvesA[1])
	powerB, _ := renderCurveInto(curvesB[1])

	if bufsEqual(waterA, waterB, waterRect) {
		t.Error("water.demand chart unchanged after mutating its projection 12..14 -> 50..70 (a)")
	}
	if !bufsEqual(powerA, powerB, powerRect) {
		t.Error("power.demand chart changed even though its field was untouched between the two runs (b)")
	}
}
