package diagrams

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestEngineServesIdenticalTopologyFromCache(t *testing.T) {
	topo := SankeyTopology{
		Sources: []SankeyFlow{{ID: "s1", Name: "tax", Amount: 100}},
		Sinks:   []SankeyFlow{{ID: "k1", Name: "roads", Amount: 100}},
	}
	calls := 0
	e := NewEngine()
	buf := core.NewBuffer(40, 8)

	r1, err1 := e.Render(buf, topo.Hash(), func(b *core.Buffer) (Result, error) {
		calls++
		return RenderSankey(b, topo, Options{})
	})
	if err1 != nil {
		t.Fatalf("first render: %v", err1)
	}
	r2, err2 := e.Render(buf, topo.Hash(), func(b *core.Buffer) (Result, error) {
		calls++
		return RenderSankey(b, topo, Options{})
	})
	if err2 != nil {
		t.Fatalf("second render: %v", err2)
	}
	if calls != 1 {
		t.Fatalf("underlying layout ran %d times for an unchanged topology, want 1 (AC-6)", calls)
	}
	if r1.Region != r2.Region || !hitsEqual(r1.Hits, r2.Hits) {
		t.Fatalf("cached result differs from the first render:\n %+v\n %+v", r1, r2)
	}
}

func TestEngineRecomputesWhenTopologyChanges(t *testing.T) {
	topo := SankeyTopology{
		Sources: []SankeyFlow{{ID: "s1", Name: "tax", Amount: 100}},
		Sinks:   []SankeyFlow{{ID: "k1", Name: "roads", Amount: 100}},
	}
	e := NewEngine()
	buf := core.NewBuffer(40, 8)
	calls := 0
	render := func(t2 SankeyTopology) func(b *core.Buffer) (Result, error) {
		return func(b *core.Buffer) (Result, error) {
			calls++
			return RenderSankey(b, t2, Options{})
		}
	}
	if _, err := e.Render(buf, topo.Hash(), render(topo)); err != nil {
		t.Fatal(err)
	}
	changed := topo
	changed.Sinks = append([]SankeyFlow(nil), topo.Sinks...)
	changed.Sinks[0].Amount = 200
	if changed.Hash() == topo.Hash() {
		t.Fatal("changing an amount must change the topology hash")
	}
	if _, err := e.Render(buf, changed.Hash(), render(changed)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("underlying layout ran %d times, want 2 (recompute after a topology change)", calls)
	}
}

func TestEngineReBlitsGlyphsOnCacheHit(t *testing.T) {
	topo := ChainTopology{
		Nodes: []ChainNode{{ID: "A", Label: "mine"}, {ID: "B", Label: "smelt"}},
		Edges: []ChainEdge{{ID: "e1", From: "A", To: "B", Figure: "9 t/day"}},
	}
	e := NewEngine()
	bufA := core.NewBuffer(50, 10)
	if _, err := e.Chain(bufA, topo, Options{}); err != nil {
		t.Fatal(err)
	}
	bufB := core.NewBuffer(50, 10) // fresh buffer, must be repainted from cache
	if _, err := e.Chain(bufB, topo, Options{}); err != nil {
		t.Fatal(err)
	}
	if !bufferEqual(bufA, bufB) {
		t.Fatal("a cache hit must re-blit the glyphs into the caller's buffer")
	}
}
