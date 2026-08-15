package diagrams

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// buildChainFromMap builds a chain topology by iterating a Go map, so the
// Nodes slice order is randomized per run — the AC-8 "map-iteration-order-
// sensitive input" a hidden range-over-map leak in the caller-facing
// boundary would be caught by.
func buildChainFromMap() ChainTopology {
	m := map[SourceID]ChainNode{
		"A": {ID: "A", Label: "mine"},
		"B": {ID: "B", Label: "smelt"},
		"C": {ID: "C", Label: "factory"},
		"D": {ID: "D", Label: "warehouse"},
		"E": {ID: "E", Label: "port"},
	}
	var nodes []ChainNode
	for _, n := range m {
		nodes = append(nodes, n)
	}
	return ChainTopology{
		Nodes: nodes,
		Edges: []ChainEdge{
			{ID: "e1", From: "A", To: "B", Figure: "12 t/day"},
			{ID: "e2", From: "B", To: "C", Figure: "8 t/day"},
			{ID: "e3", From: "C", To: "D", Figure: "5 t/day"},
			{ID: "e4", From: "D", To: "E", Figure: "3 t/day"},
		},
	}
}

func buildNetworkFromMap() NetworkTopology {
	m := map[SourceID]NetworkNode{
		"n1": {ID: "n1", Label: "gen", X: 0, Y: 0},
		"n2": {ID: "n2", Label: "sub", X: 4, Y: 0},
		"n3": {ID: "n3", Label: "hut", X: 4, Y: 2},
		"n4": {ID: "n4", Label: "mill", X: 0, Y: 2},
	}
	var nodes []NetworkNode
	for _, n := range m {
		nodes = append(nodes, n)
	}
	return NetworkTopology{
		Mode:  NetworkGrid,
		Nodes: nodes,
		Edges: []NetworkEdge{
			{ID: "e1", From: "n1", To: "n2", Load: 0.1},
			{ID: "e2", From: "n2", To: "n3", Load: 0.5},
			{ID: "e3", From: "n3", To: "n4", Load: 0.9},
		},
	}
}

// assertDeterministic renders topo into a fresh buffer count times and
// fails if any run differs from the first (AC-8).
func assertDeterministic(t *testing.T, render func(buf *core.Buffer) (Result, error), w, h int) {
	t.Helper()
	var first *core.Buffer
	var firstHits []Hit
	for i := 0; i < 5; i++ {
		buf := core.NewBuffer(w, h)
		res, err := render(buf)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if i == 0 {
			first = buf
			firstHits = res.Hits
			continue
		}
		if !bufferEqual(first, buf) {
			t.Fatalf("run %d produced different cells than run 0", i)
		}
		if !hitsEqual(firstHits, res.Hits) {
			t.Fatalf("run %d produced a different hit-test mapping than run 0", i)
		}
	}
}

func TestRenderChainDeterministicAcrossMapOrder(t *testing.T) {
	topo := buildChainFromMap()
	assertDeterministic(t, func(buf *core.Buffer) (Result, error) {
		return RenderChain(buf, topo, Options{})
	}, 80, 20)
}

// TestRenderChain_SameTopologyDifferentSliceOrder is the deterministic
// twin of the map-order smoke above: the same semantic topology passed in
// two different node-slice orders must lay out identically. This is what
// actually fails if node placement stops sorting by ID (AC-8's "no
// map-iteration-order-dependent placement"), and it is deterministic
// rather than probable.
func TestRenderChain_SameTopologyDifferentSliceOrder(t *testing.T) {
	edges := []ChainEdge{
		{ID: "e1", From: "A", To: "B", Figure: "1"},
		{ID: "e2", From: "B", To: "C", Figure: "1"},
		{ID: "e3", From: "C", To: "D", Figure: "1"},
		{ID: "e4", From: "D", To: "E", Figure: "1"},
	}
	nodes := []ChainNode{
		{ID: "A", Label: "mine"}, {ID: "B", Label: "smelt"}, {ID: "C", Label: "factory"},
		{ID: "D", Label: "warehouse"}, {ID: "E", Label: "port"},
	}
	rev := []ChainNode{
		{ID: "E", Label: "port"}, {ID: "D", Label: "warehouse"}, {ID: "C", Label: "factory"},
		{ID: "B", Label: "smelt"}, {ID: "A", Label: "mine"},
	}

	b1 := core.NewBuffer(80, 20)
	r1, err1 := RenderChain(b1, ChainTopology{Nodes: nodes, Edges: edges}, Options{})
	if err1 != nil {
		t.Fatal(err1)
	}
	b2 := core.NewBuffer(80, 20)
	r2, err2 := RenderChain(b2, ChainTopology{Nodes: rev, Edges: edges}, Options{})
	if err2 != nil {
		t.Fatal(err2)
	}
	if !bufferEqual(b1, b2) {
		t.Fatal("layout must not depend on node slice order (AC-8)")
	}
	if !hitsEqual(r1.Hits, r2.Hits) {
		t.Fatal("hit-test mapping must not depend on node slice order (AC-8)")
	}
}

func TestRenderNetworkDeterministicAcrossMapOrder(t *testing.T) {
	topo := buildNetworkFromMap()
	assertDeterministic(t, func(buf *core.Buffer) (Result, error) {
		return RenderNetwork(buf, topo, Options{})
	}, 30, 10)
}

func TestRenderSankeyDeterministic(t *testing.T) {
	topo := SankeyTopology{
		Sources: []SankeyFlow{{ID: "s1", Name: "tax", Amount: 100}, {ID: "s2", Name: "grants", Amount: 200}},
		Sinks:   []SankeyFlow{{ID: "k1", Name: "roads", Amount: 150}, {ID: "k2", Name: "schools", Amount: 150}},
	}
	assertDeterministic(t, func(buf *core.Buffer) (Result, error) {
		return RenderSankey(buf, topo, Options{})
	}, 40, 10)
}

func TestHashChangesWithTopology(t *testing.T) {
	a := SankeyTopology{Sources: []SankeyFlow{{ID: "s1", Name: "tax", Amount: 100}}}
	b := a
	b.Sources = append([]SankeyFlow(nil), a.Sources...)
	b.Sources[0].Amount = 101
	if a.Hash() == b.Hash() {
		t.Fatal("changing a flow amount must change the topology hash")
	}
	c := ChainTopology{Nodes: []ChainNode{{ID: "A", Label: "a"}}}
	d := ChainTopology{Nodes: []ChainNode{{ID: "A", Label: "a"}}}
	if c.Hash() != d.Hash() {
		t.Fatal("identical topologies must hash identically")
	}
}
