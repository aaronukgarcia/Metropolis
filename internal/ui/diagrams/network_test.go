package diagrams

import (
	"errors"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func TestLoadTier_DistinguishesLowAndHighLoad(t *testing.T) {
	if LoadTier(0.1) == LoadTier(0.9) {
		t.Fatalf("LoadTier(0.1)=%d and LoadTier(0.9)=%d must differ (AC-3a)", LoadTier(0.1), LoadTier(0.9))
	}
	for _, tc := range []struct {
		in   float64
		want int
	}{
		{-1, 0}, {0, 0}, {0.1, 0}, {0.4, 1}, {0.5, 1}, {0.7, 2}, {0.9, 2}, {1, 2}, {2, 2},
	} {
		if got := LoadTier(tc.in); got != tc.want {
			t.Errorf("LoadTier(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestRenderNetwork_LoadColoursEdgeDistinctly(t *testing.T) {
	// Two horizontal edges with identical geometry but different loads must
	// render visibly differently (AC-3a): distinct glyph and distinct
	// foreground colour.
	topo := NetworkTopology{
		Mode: NetworkGrid,
		Nodes: []NetworkNode{
			{ID: "A", Label: "a", X: 0, Y: 0},
			{ID: "B", Label: "b", X: 5, Y: 0},
			{ID: "C", Label: "c", X: 0, Y: 1},
			{ID: "D", Label: "d", X: 5, Y: 1},
		},
		Edges: []NetworkEdge{
			{ID: "e1", From: "A", To: "B", Load: 0.1},
			{ID: "e2", From: "C", To: "D", Load: 0.9},
		},
	}
	buf := core.NewBuffer(20, 5)
	_, err := RenderNetwork(buf, topo, Options{Palette: widgets.DefaultPalette})
	if err != nil {
		t.Fatalf("RenderNetwork: %v", err)
	}

	c1 := buf.Get(2, 0) // e1's horizontal run (load 0.1)
	c2 := buf.Get(2, 1) // e2's horizontal run (load 0.9)
	if c1.Rune == c2.Rune {
		t.Errorf("edge glyphs must differ across load tiers, both are %q", c1.Rune)
	}
	fg1, _, _ := c1.Style.Decompose()
	fg2, _, _ := c2.Style.Decompose()
	if fg1 == fg2 {
		t.Errorf("edge colours must differ across load tiers, both are %v", fg1)
	}
}

func TestRenderNetwork_TubeMapStopsInLineOrder(t *testing.T) {
	// Stops with large/negative raw coordinates must render in slice order
	// along the strip, NOT at their raw coordinates (AC-3b).
	topo := NetworkTopology{
		Mode: NetworkTubeMap,
		Nodes: []NetworkNode{
			{ID: "stop1", Label: "central", X: 99, Y: 99},
			{ID: "stop2", Label: "west", X: 7, Y: 7},
			{ID: "stop3", Label: "east", X: -5, Y: -5},
		},
	}
	buf := core.NewBuffer(20, 8)
	res, err := RenderNetwork(buf, topo, Options{})
	if err != nil {
		t.Fatalf("RenderNetwork: %v", err)
	}

	rows := gridRows(buf)
	// Stop i must be at row i*2 (line order), its label immediately right of
	// the '●' stop glyph.
	for i, n := range topo.Nodes {
		y := i * 2
		if got := buf.Get(0, y).Rune; got != '●' {
			t.Errorf("stop %d glyph at row %d = %q, want '●'", i, y, got)
		}
		if !strings.Contains(rows[y], n.Label) {
			t.Errorf("stop %q label %q not on row %d", n.ID, n.Label, y)
		}
	}
	// Every stop round-trips its ID (AC-5).
	gotIDs := hitIDs(res.Hits)
	for _, n := range topo.Nodes {
		if !gotIDs[n.ID] {
			t.Errorf("stop ID %q missing from hit-test output", n.ID)
		}
	}
}

func TestRenderNetwork_DanglingEdgeReturnsRegistryError(t *testing.T) {
	topo := NetworkTopology{
		Mode:  NetworkGrid,
		Nodes: []NetworkNode{{ID: "A", Label: "a", X: 0, Y: 0}},
		Edges: []NetworkEdge{{ID: "e1", From: "A", To: "ghost"}},
	}
	buf := core.NewBuffer(20, 5)
	_, err := RenderNetwork(buf, topo, Options{})
	if err == nil {
		t.Fatal("expected an error for a dangling network edge")
	}
	if !errors.Is(err, &errs.E{Code: "MET-U900"}) {
		t.Errorf("error is not MET-U900: %v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error must name the missing node ID: %v", err)
	}
}

func TestRenderNetwork_ZeroNodesEmptyNoError(t *testing.T) {
	buf := core.NewBuffer(10, 4)
	res, err := RenderNetwork(buf, NetworkTopology{}, Options{})
	if err != nil {
		t.Fatalf("zero nodes should not error: %v", err)
	}
	if len(res.Hits) != 0 || res.Region != (core.Rect{}) {
		t.Errorf("zero nodes should render empty, got %+v", res)
	}
}
