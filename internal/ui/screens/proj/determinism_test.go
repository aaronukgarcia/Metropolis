package proj

// SF-8 (GR#21 determinism): rendering is a pure function of
// (view-model state, navigation/selection state) — identical inputs
// render identically across repeated calls; no time.Now()-driven content.
// None of this package's production code calls the wall clock directly,
// and confidence-band/threshold/marker placement is a pure function of
// the view-model (PRJ-1's determinism clause).

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestNoWallClockUsage mechanically encodes SF-8's own grep check
// ("grep -rn time.Now internal/ui/screens/proj/*.go, excluding _test.go,
// returns no matches") as a real test, mirroring ui.screen.demo's
// TestNoWallClockUsage.
func TestNoWallClockUsage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	needle := []byte("time.Now(")
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" {
			continue
		}
		if len(name) >= len("_test.go") && name[len(name)-len("_test.go"):] == "_test.go" {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if bytes.Contains(b, needle) {
			t.Errorf("%s calls time.Now() directly -- this package must never read the wall clock (SF-8/GR#21)", name)
		}
	}
}

// TestRender_IdenticalInputsRenderIdentically is SF-8's positive check:
// calling every Render* function twice with the same inputs produces
// byte-identical buffers.
func TestRender_IdenticalInputsRenderIdentically(t *testing.T) {
	curve := Curve{
		Key: "water.demand", Label: "Water demand", Status: StatusAvailable,
		History:         []float64{10, 11, 12},
		Projection:      []float64{13, 14, 15},
		ConfidenceUpper: []float64{15.5, 16.5, 17.5},
		ConfidenceLower: []float64{11.5, 12.5, 13.5},
		Thresholds:      []Threshold{{Value: 16, Label: "ceiling"}},
		Markers:         []DecisionMarker{{MonthOffset: 1, Label: "build"}},
	}
	crossing := Crossing{
		Key: "refuse.ashford", Status: StatusAvailable,
		InternalDemand:     []float64{100, 110, 120},
		ContractedCapacity: []float64{115, 115, 115},
		CrossingMonth:      1,
	}
	rate := RateOutlook{Status: StatusAvailable, History: []float64{2.0}, Projection: []float64{2.1, 2.2}}
	consequence := Consequence{Label: "Cut funding", FuseMonths: 72, History: []float64{9, 8}, Projection: []float64{7, 6, 5}}

	render := func() *core.Buffer {
		buf := core.NewBuffer(60, 12)
		RenderHeader(buf, core.Rect{X: 0, Y: 0, W: 60, H: 1}, 72, true, tcell.StyleDefault)
		RenderCurve(buf, core.Rect{X: 0, Y: 1, W: 60, H: 4}, curve, widgets.DefaultPalette)
		RenderCrossing(buf, core.Rect{X: 0, Y: 5, W: 60, H: 3}, crossing, widgets.DefaultPalette)
		RenderRateOutlook(buf, core.Rect{X: 0, Y: 8, W: 60, H: 3}, rate, widgets.DefaultPalette)
		RenderSlowFuse(buf, core.Rect{X: 0, Y: 11, W: 60, H: 1}, consequence, widgets.DefaultPalette)
		return buf
	}

	a := render()
	b := render()
	w, h := a.Size()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if a.Get(x, y) != b.Get(x, y) {
				t.Fatalf("render not deterministic at (%d,%d): %+v vs %+v", x, y, a.Get(x, y), b.Get(x, y))
			}
		}
	}
}
