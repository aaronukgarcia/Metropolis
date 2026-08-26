package finance

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// TestScreen_SankeyEngineHoistedAcrossFrames proves BUG-316: the diagrams
// layout engine is hoisted onto the Screen and lives across frames, so a
// second frame rendering the same fiscal-circuit topology at the same geometry
// is served from the persistent cache instead of a rebuilt, throwaway engine.
//
// It can fail: were RenderSankey to construct a fresh engine per call again
// (the pre-fix behaviour), every frame would start with an empty cache and
// Hits would stay 0 forever, so the assertion Hits > 0 would be false.
func TestScreen_SankeyEngineHoistedAcrossFrames(t *testing.T) {
	s := New("corr-hoist")
	sankey := FiscalCircuitView{
		Bands: []SankeyBand{
			{Source: "IncomeTax", Target: "Treasury", Amount: 1_000_000},
			{Source: "Treasury", Target: "Roads", Amount: 600_000},
			{Source: "Treasury", Target: "Schools", Amount: 400_000},
		},
	}
	rect := core.Rect{X: 0, Y: 0, W: 40, H: 14}

	// Frame 1 populates the cache.
	s.RenderSankey(core.NewBuffer(40, 14), rect, sankey, true, tcell.StyleDefault)
	st1, err := s.engine.Stats()
	if err != nil {
		t.Fatalf("stats after frame 1: %v", err)
	}

	// Frame 2: identical topology and geometry -> must be a cache hit.
	s.RenderSankey(core.NewBuffer(40, 14), rect, sankey, true, tcell.StyleDefault)
	st2, err := s.engine.Stats()
	if err != nil {
		t.Fatalf("stats after frame 2: %v", err)
	}

	if st2.Hits <= st1.Hits {
		t.Fatalf("BUG-316: second frame did not hit the cache (hits %d -> %d); "+
			"the engine is not hoisted across frames", st1.Hits, st2.Hits)
	}
}
