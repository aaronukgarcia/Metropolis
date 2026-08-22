package districts

// AC-11 (GR#21 determinism): rendering is a pure function of (view-model
// state, selected district, selected policy, active preview payload) --
// identical inputs render identically across repeated calls; no
// time.Now()-driven content. None of this package's production code calls
// the wall clock directly.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// TestNoWallClockUsage mechanically encodes AC-11's own grep check ("grep
// -rn time.Now internal/ui/screens/districts/*.go, excluding _test.go,
// returns no matches") as a real test, mirroring ui.screen.services'
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
			t.Errorf("%s calls time.Now() directly -- this package must never read the wall clock (AC-11/GR#21)", name)
		}
	}
}

// TestRender_IdenticalInputsRenderIdentically is AC-11's positive check:
// calling every Render* function twice with the same inputs produces
// byte-identical buffers.
func TestRender_IdenticalInputsRenderIdentically(t *testing.T) {
	s := New("corr-det")
	s.BindSubscription("sub-det")
	s.SetSelectedDistrict("harbour")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-det", Patch: mustJSON(t, fullPatch())})

	settings, _ := s.TaxSettings()

	render := func() *core.Buffer {
		buf := core.NewBuffer(90, 30)
		RenderTaxSettings(buf, core.Rect{X: 0, Y: 0, W: 90, H: 10}, settings, s.SelectedDistrict(), s.TaxRejectedReason(), true, testStyle)
		RenderBlockedFeature(buf, core.Rect{X: 0, Y: 10, W: 90, H: 5}, "POLICY LIBRARY", testStyle)
		RenderBlockedFeature(buf, core.Rect{X: 0, Y: 15, W: 90, H: 5}, "IMPACT PREVIEW", testStyle)
		RenderBlockedFeature(buf, core.Rect{X: 0, Y: 20, W: 90, H: 5}, "CONFLICT WARNINGS", testStyle)
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
