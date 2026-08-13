package demo

// SF-8 (GR#21 determinism): rendering is a pure function of
// (view-model state, navigation/selection state) -- identical inputs
// render identically across repeated calls; no time.Now()-driven
// content beyond the shared threshold-pulse primitive (unused by this
// package). None of this package's production code calls the wall
// clock directly.

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
// ("grep -rn time.Now internal/ui/screens/demo/*.go, excluding
// _test.go, returns no matches") as a real test, mirroring
// feat.devmode's TestNoWallClockUsage (internal/ui/screens/devmode/
// determinism_test.go) and this item's own doc.go note.
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
	ages := []AgeBucket{{MonthAge: 0, Male: 10, Female: 8}, {MonthAge: 12, Male: 5, Female: 4}}
	hours := []ActivityHours{{Activity: "Sport", Hours: 3}, {Activity: "Rest", Hours: 5}}
	typologies := []TypologyRow{{Typology: "Terrace", Demand: 10, Stock: 9}}
	commute := CommuteFigures{OutCommuters: 3, InCommuters: 4}
	traits := []TraitBucket{{Trait: "Bold", Count: 5}}
	taste := []TasteBucket{{Taste: "Sport", Weight: 0.5}}
	rect := core.Rect{X: 0, Y: 0, W: 40, H: 6}

	render := func() *core.Buffer {
		buf := core.NewBuffer(40, 6)
		RenderPopulationPyramid(buf, rect, ages, widgets.DefaultPalette)
		RenderHoursByActivity(buf, rect, hours, tcell.StyleDefault)
		RenderTypologies(buf, rect, typologies, tcell.StyleDefault)
		RenderCommuteLeak(buf, rect, commute, tcell.StyleDefault)
		RenderPersonality(buf, rect, traits, tcell.StyleDefault)
		RenderLeisureTaste(buf, rect, taste, tcell.StyleDefault)
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
