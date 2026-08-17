package build

// SF-8 (GR#21 determinism): rendering is a pure function of (view-model
// state, navigation/selection state) — identical inputs render identically
// across repeated calls; no time.Now()-driven content. None of this
// package's production code calls the wall clock directly.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestNoWallClockUsage mechanically encodes SF-8's own grep check
// ("grep -rn time.Now internal/ui/screens/build/*.go, excluding _test.go,
// returns no matches") as a real test, mirroring ui.screen.trade's
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

// protocolDelta builds a protocol.Delta carrying the given wirePatch for a
// subscription ID, for tests.
func protocolDelta(t *testing.T, sub protocol.SubscriptionID, p wirePatch) protocol.Delta {
	t.Helper()
	return protocol.Delta{SubscriptionID: sub, Patch: mustJSON(t, p)}
}

// TestRender_IdenticalInputsRenderIdentically is SF-8's positive check:
// calling every Render* function twice with the same inputs produces
// byte-identical buffers.
func TestRender_IdenticalInputsRenderIdentically(t *testing.T) {
	s := New("corr-det")
	s.BindSubscription("sub-det")
	s.ApplyDelta(protocolDelta(t, "sub-det", fullPatch()))

	zones, _ := s.Zones()
	queue, _ := s.Queue()
	catalogue, _ := s.Catalogue()
	price, havePrice := s.LandPrice()
	dem, haveDem := s.Demolition()

	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	render := func() *core.Buffer {
		buf := core.NewBuffer(90, 20)
		RenderHeader(buf, core.Rect{X: 0, Y: 0, W: 90, H: 1}, s.HaveData(), s.Stale(), tcell.StyleDefault)
		RenderLandPrice(buf, core.Rect{X: 0, Y: 1, W: 90, H: 2}, price, havePrice, style)
		RenderZones(buf, core.Rect{X: 0, Y: 3, W: 90, H: 6}, zones, true, style)
		RenderQueue(buf, core.Rect{X: 0, Y: 9, W: 90, H: 5}, queue, true, style)
		RenderCatalogue(buf, core.Rect{X: 0, Y: 14, W: 90, H: 5}, catalogue, true, style)
		RenderDemolition(buf, core.Rect{X: 0, Y: 19, W: 90, H: 1}, dem, haveDem, style)
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
