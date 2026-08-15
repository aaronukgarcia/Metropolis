package diagrams

import (
	"errors"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestRenderChain_RendersEveryNodeAndEdgeWithFigure(t *testing.T) {
	topo := ChainTopology{
		Nodes: []ChainNode{
			{ID: "A", Label: "mine"},
			{ID: "B", Label: "smelt"},
			{ID: "C", Label: "factory"},
			{ID: "D", Label: "warehouse"},
			{ID: "E", Label: "port"},
		},
		Edges: []ChainEdge{
			{ID: "e1", From: "A", To: "B", Figure: "12 t/day"},
			{ID: "e2", From: "B", To: "C", Figure: "8 t/day"},
			{ID: "e3", From: "C", To: "D", Figure: "5 t/day"},
			{ID: "e4", From: "D", To: "E", Figure: "3 t/day"},
		},
	}
	buf := core.NewBuffer(80, 12)
	res, err := RenderChain(buf, topo, Options{})
	if err != nil {
		t.Fatalf("RenderChain: %v", err)
	}

	// Every node has a rendered box (its label appears) and every edge has
	// a rendered arrow carrying its figure (AC-2).
	for _, n := range topo.Nodes {
		if !bufferContains(buf, n.Label) {
			t.Errorf("node %q label %q not rendered", n.ID, n.Label)
		}
	}
	for _, e := range topo.Edges {
		if !bufferContains(buf, e.Figure) {
			t.Errorf("edge %q figure %q not rendered", e.ID, e.Figure)
		}
	}

	// One hit per rendered element (5 nodes + 4 edges) and every ID
	// round-trips unchanged (AC-5).
	if want := len(topo.Nodes) + len(topo.Edges); len(res.Hits) != want {
		t.Fatalf("got %d hits, want %d (one per node+edge)", len(res.Hits), want)
	}
	wantIDs := map[SourceID]bool{"A": true, "B": true, "C": true, "D": true, "E": true, "e1": true, "e2": true, "e3": true, "e4": true}
	gotIDs := hitIDs(res.Hits)
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("hit IDs %v, want exactly %v", gotIDs, wantIDs)
	}
	for id := range wantIDs {
		if !gotIDs[id] {
			t.Errorf("input ID %q missing from hit-test output", id)
		}
	}
}

func TestRenderChain_DanglingEdgeReturnsRegistryError(t *testing.T) {
	topo := ChainTopology{
		Nodes: []ChainNode{{ID: "A", Label: "a"}, {ID: "B", Label: "b"}},
		Edges: []ChainEdge{{ID: "e1", From: "A", To: "missingNode"}},
	}
	buf := core.NewBuffer(40, 10)
	res, err := RenderChain(buf, topo, Options{})
	if err == nil {
		t.Fatal("expected an error for a dangling edge reference")
	}
	if !errors.Is(err, &errs.E{Code: "MET-U900"}) {
		t.Errorf("error is not MET-U900: %v", err)
	}
	if !strings.Contains(err.Error(), "missingNode") {
		t.Errorf("error must name the missing node ID: %v", err)
	}
	// No partial/corrupted layout is returned alongside the error (AC-7).
	if len(res.Hits) != 0 || res.Region != (core.Rect{}) {
		t.Errorf("expected zero Result alongside the error, got %+v", res)
	}
}

func TestRenderChain_ZeroNodesEmptyNoError(t *testing.T) {
	buf := core.NewBuffer(20, 5)
	res, err := RenderChain(buf, ChainTopology{}, Options{})
	if err != nil {
		t.Fatalf("zero nodes should not error: %v", err)
	}
	if len(res.Hits) != 0 || res.Region != (core.Rect{}) {
		t.Errorf("zero nodes should render an empty region with no hits, got %+v", res)
	}
}

func TestRenderChain_IsolatedNodeRendersAlone(t *testing.T) {
	topo := ChainTopology{Nodes: []ChainNode{{ID: "solo", Label: "solo"}}}
	buf := core.NewBuffer(20, 10)
	res, err := RenderChain(buf, topo, Options{})
	if err != nil {
		t.Fatalf("isolated node should not error: %v", err)
	}
	if len(res.Hits) != 1 || res.Hits[0].ID != "solo" {
		t.Fatalf("isolated node should render alone with one hit, got %+v", res.Hits)
	}
	if !bufferContains(buf, "solo") {
		t.Error("isolated node label not rendered")
	}
}

func TestRenderChain_CyclicRendersAllEdgesWithoutPanic(t *testing.T) {
	topo := ChainTopology{
		Nodes: []ChainNode{{ID: "A", Label: "a"}, {ID: "B", Label: "b"}, {ID: "C", Label: "c"}},
		Edges: []ChainEdge{
			{ID: "e1", From: "A", To: "B", Figure: "1"},
			{ID: "e2", From: "B", To: "C", Figure: "1"},
			{ID: "e3", From: "C", To: "A", Figure: "1"},
		},
	}
	buf := core.NewBuffer(60, 12)
	res, err := RenderChain(buf, topo, Options{}) // must not panic
	if err != nil {
		t.Fatalf("cyclic chain should not error: %v", err)
	}
	gotIDs := hitIDs(res.Hits)
	for _, id := range []SourceID{"A", "B", "C", "e1", "e2", "e3"} {
		if !gotIDs[id] {
			t.Errorf("cyclic chain dropped ID %q", id)
		}
	}
}
