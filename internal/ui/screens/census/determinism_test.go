package census

// AC-14 (determinism): rendering is a pure function of (subscribed
// view-model state, selected KPI, selected citizen/bio, selected linkage
// scope) -- identical inputs render identically across repeated calls; no
// time.Now()-driven content beyond the shared widgets pulse primitive.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestNoWallClockUsage mechanically encodes AC-14's own grep check
// ("grep -rn time.Now internal/ui/screens/census/*.go, excluding
// _test.go, returns no matches").
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
			t.Errorf("%s calls time.Now() directly -- this package must never read the wall clock (AC-14/GR#21)", name)
		}
	}
}

// TestRender_IdenticalInputsRenderIdentically is AC-14's positive check:
// calling every Render* function twice with the same inputs produces
// byte-identical buffers.
func TestRender_IdenticalInputsRenderIdentically(t *testing.T) {
	s := newScreenWithData(t, "sub-det")

	bands, _ := s.AgeBandSeries()
	sex, _ := s.SexSeries()
	tiers, _ := s.EducationTierSeries()
	bwc, _ := s.BlueWhiteCollarSplit()
	kpis, _ := s.KPITiles()
	bio, haveBio := s.SelectedBio()
	link, haveLink := s.EducationCrimeLinkageView()
	src, haveSrc := s.KPISource(KPIKeyHomeless)

	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	render := func() *core.Buffer {
		buf := core.NewBuffer(100, 40)
		RenderAgeBandPyramid(buf, core.Rect{X: 0, Y: 0, W: 100, H: 8}, bands, true, widgets.DefaultPalette, style)
		RenderSexSeries(buf, core.Rect{X: 0, Y: 8, W: 100, H: 4}, sex, true, widgets.DefaultPalette, style)
		RenderEducationTierSeries(buf, core.Rect{X: 0, Y: 12, W: 100, H: 10}, tiers, true, style)
		RenderBlueWhiteCollar(buf, core.Rect{X: 0, Y: 22, W: 100, H: 4}, bwc, true, widgets.DefaultPalette, style)
		RenderKPITiles(buf, core.Rect{X: 0, Y: 26, W: 100, H: 9}, kpis, true, style)
		RenderKPISource(buf, core.Rect{X: 0, Y: 35, W: 100, H: 2}, src, haveSrc, style)
		RenderCitizenBio(buf, core.Rect{X: 0, Y: 37, W: 100, H: 1}, bio, haveBio, style)
		RenderEducationCrimeLinkage(buf, core.Rect{X: 0, Y: 38, W: 100, H: 1}, link, haveLink, style)
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
