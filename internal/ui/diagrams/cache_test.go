package diagrams

import (
	"strconv"
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

// TestBUG321_EvictionIsLRUAndDeterministic fills the cache to its cap with
// distinct keys, then RE-RENDERS the oldest key so a hit refreshes its
// recency, then inserts one new key past the cap. The freshly-refreshed key
// must survive and the now-oldest key must be the eviction victim. Because
// lastAccess values are unique, the victim is fully determined (GR#21) -- so
// this also asserts the eviction is not a coin-flip over map order.
func TestBUG321_EvictionIsLRUAndDeterministic(t *testing.T) {
	topo := func(n int) SankeyTopology {
		return SankeyTopology{Sources: []SankeyFlow{{ID: SourceID(strconv.Itoa(n)), Name: "s", Amount: float64(n)}}}
	}
	e := NewEngine()
	buf := core.NewBuffer(40, 8)
	// Insert exactly maxCacheEntries distinct keys: 0..cap-1.
	for i := 0; i < maxCacheEntries; i++ {
		if _, err := e.Sankey(buf, topo(i), Options{}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	st, _ := e.Stats()
	if st.Entries != maxCacheEntries || st.Evictions != 0 {
		t.Fatalf("after filling to cap: entries=%d evictions=%d, want %d/0", st.Entries, st.Evictions, maxCacheEntries)
	}
	// Refresh key 0 (the oldest) via a cache hit -- it is now the MOST recent.
	if _, err := e.Sankey(buf, topo(0), Options{}); err != nil {
		t.Fatal(err)
	}
	// Insert one new key -> exactly one eviction. Key 1 is now the oldest.
	if _, err := e.Sankey(buf, topo(maxCacheEntries), Options{}); err != nil {
		t.Fatal(err)
	}
	st, _ = e.Stats()
	if st.Entries != maxCacheEntries {
		t.Fatalf("entries=%d, want %d (never exceed the cap)", st.Entries, maxCacheEntries)
	}
	if st.Evictions != 1 {
		t.Fatalf("evictions=%d, want exactly 1", st.Evictions)
	}
	// Key 0 was refreshed, so it must still be a HIT (no recompute).
	hitsBefore := st.Hits
	if _, err := e.Sankey(buf, topo(0), Options{}); err != nil {
		t.Fatal(err)
	}
	st, _ = e.Stats()
	if st.Hits != hitsBefore+1 {
		t.Fatal("BUG-321: the LRU-refreshed key was evicted; eviction is not least-recently-used")
	}
	// Key 1 was the oldest, so it must have been the victim -> now a MISS.
	missBefore := st.Misses
	if _, err := e.Sankey(buf, topo(1), Options{}); err != nil {
		t.Fatal(err)
	}
	st, _ = e.Stats()
	if st.Misses != missBefore+1 {
		t.Fatal("BUG-321: the least-recently-used key was NOT the eviction victim")
	}
}

// TestBUG316_321_342_CachedBoundedNonColliding proves the three fixes hold
// together on ONE engine: the cache LIVES across renders (BUG-316 hit), never
// COLLIDES on buffer height (BUG-342), and stays BOUNDED under many distinct
// renders (BUG-321). Hoisting without the bound leaks; hoisting without the
// key fix collides -- so all three must be asserted on the same live cache.
func TestBUG316_321_342_CachedBoundedNonColliding(t *testing.T) {
	e := NewEngine()

	// (BUG-316) The cache lives: the same topology+geometry twice hits.
	topo := tallSankey(6)
	if _, err := e.Sankey(core.NewBuffer(80, 24), topo, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Sankey(core.NewBuffer(80, 24), topo, Options{}); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.Stats(); st.Hits == 0 {
		t.Fatal("BUG-316: cache did not serve a hit across two identical renders on one engine")
	}

	// (BUG-342) No height collision: a network whose nodes span Y=0..5 renders
	// on a 20x50 buffer, then on a 20x3 buffer. validateGridCoords must reject
	// the short buffer; a shared key would serve the tall (nil-error) entry.
	net := NetworkTopology{
		Mode:  NetworkGrid,
		Nodes: []NetworkNode{{ID: "a", Label: "a", X: 0, Y: 0}, {ID: "b", Label: "b", X: 0, Y: 5}},
	}
	if _, err := e.Network(core.NewBuffer(20, 50), net, Options{}); err != nil {
		t.Fatalf("tall network render should succeed: %v", err)
	}
	if _, err := e.Network(core.NewBuffer(20, 3), net, Options{}); err == nil {
		t.Fatal("BUG-342: 20x3 render returned nil error -- it was served the cached 20x50 layout (height omitted from key)")
	}

	// (BUG-321) Bounded: many distinct geometries leave entries <= cap.
	for i := 0; i < maxCacheEntries*3; i++ {
		if _, err := e.Sankey(core.NewBuffer(30+i, 18), topo, Options{}); err != nil {
			t.Fatalf("bound render %d: %v", i, err)
		}
	}
	st, _ := e.Stats()
	if st.Entries > maxCacheEntries {
		t.Fatalf("BUG-321: entries=%d exceeds cap %d after the combined run", st.Entries, maxCacheEntries)
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
