package diagrams

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestSEC065_SankeyBandWidthTracksBuffer renders the same Sankey topology
// through the Engine cache at two buffer widths and asserts each buffer's
// band width is computed from its own width (SEC-065). Against the unfixed
// cache key (topo.Hash() only), the second render hits the first buffer's
// entry and returns the narrow buffer's band width for the wide buffer.
func TestSEC065_SankeyBandWidthTracksBuffer(t *testing.T) {
	topo := SankeyTopology{
		Sources: []SankeyFlow{{ID: "s1", Name: "tax", Amount: 100}},
		Sinks:   []SankeyFlow{{ID: "k1", Name: "roads", Amount: 100}},
	}
	e := NewEngine()
	// labelW = 6 ("budget"), so bandMax = width - 7: 13 for 20-wide, 73 for
	// 80-wide.
	narrow := core.NewBuffer(20, 8)
	wide := core.NewBuffer(80, 8)

	rn, err := e.Sankey(narrow, topo, Options{})
	if err != nil {
		t.Fatalf("narrow render: %v", err)
	}
	rw, err := e.Sankey(wide, topo, Options{}) // identical topology — must not hit narrow's entry
	if err != nil {
		t.Fatalf("wide render: %v", err)
	}

	if got := rn.Hits[0].Rect.W; got != 13 {
		t.Fatalf("narrow buffer band width = %d, want 13", got)
	}
	if got := rw.Hits[0].Rect.W; got != 73 {
		t.Fatalf("wide buffer band width = %d, want 73 — the cache key omitted buffer width and served the narrow layout", got)
	}
}

// TestSEC077_NetworkCacheKeyIncludesPalette renders the same network topology
// through the Engine cache under two palettes whose TokenPower colour differs
// and asserts the buffers differ (SEC-077). Against the unfixed cache key,
// the second render is served the first palette's glyphs and the two buffers
// are byte-identical.
func TestSEC077_NetworkCacheKeyIncludesPalette(t *testing.T) {
	net := NetworkTopology{
		Mode: NetworkGrid,
		Nodes: []NetworkNode{
			{ID: "A", Label: "a", X: 0, Y: 0},
			{ID: "B", Label: "b", X: 5, Y: 0},
		},
		Edges: []NetworkEdge{{ID: "e1", From: "A", To: "B", Load: 0.1}},
	}
	e := NewEngine()
	b1 := core.NewBuffer(20, 5)
	b2 := core.NewBuffer(20, 5)

	if _, err := e.Network(b1, net, Options{Palette: widgets.DefaultPalette}); err != nil {
		t.Fatalf("default-palette render: %v", err)
	}
	if _, err := e.Network(b2, net, Options{Palette: widgets.ColourblindPalette}); err != nil {
		t.Fatalf("colourblind-palette render: %v", err)
	}
	if bufferEqual(b1, b2) {
		t.Fatal("cache key omitted palette: the same topology under two palettes produced byte-identical buffers (stale glyphs)")
	}
}

// TestSEC067_OutOfRangeCoordinatesRejected asserts an oversized network grid
// coordinate is rejected with a registry error and a zero Result before any
// traversal (SEC-067), covering both rejection paths: a coordinate magnitude
// beyond maxCoord, and a node span that exceeds the buffer.
func TestSEC067_OutOfRangeCoordinatesRejected(t *testing.T) {
	cases := []struct {
		name   string
		x, y   int
		bufW   int
		bufH   int
		reason string
	}{
		{"magnitude beyond maxCoord", 20_000_000, 0, 10, 5, "magnitude"},
		{"span exceeds buffer", 1000, 0, 10, 5, "span"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			topo := NetworkTopology{
				Mode: NetworkGrid,
				Nodes: []NetworkNode{
					{ID: "A", Label: "a", X: 0, Y: 0},
					{ID: "B", Label: "b", X: tc.x, Y: tc.y},
				},
				Edges: []NetworkEdge{{ID: "e1", From: "A", To: "B", Load: 0.5}},
			}
			buf := core.NewBuffer(tc.bufW, tc.bufH)
			res, err := RenderNetwork(buf, topo, Options{})
			if err == nil {
				t.Fatalf("out-of-range %s was accepted (region %+v) instead of rejected", tc.reason, res.Region)
			}
			if !errors.Is(err, &errs.E{Code: "MET-U901"}) {
				t.Errorf("error is not MET-U901: %v", err)
			}
			if res.Region != (core.Rect{}) || len(res.Hits) != 0 {
				t.Errorf("rejected render must return a zero Result (not traversed), got %+v", res)
			}
		})
	}
}
