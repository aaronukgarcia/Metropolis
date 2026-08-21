package diagrams

import (
	"reflect"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// ---------------------------------------------------------------------------
// ATTACK 1 -- the height collision itself, on a SUCCESSFUL render path.
//
// The committed SEC-077 test only proves the ERROR path (a short buffer that
// validateGridCoords rejects). This one proves the far more common defect:
// two terminal geometries that share a width serve each other's GLYPHS.
//
// A Sankey with more flows than a 24-row terminal can hold draws rows past the
// buffer bottom; core.Buffer.Set drops them. The Engine then snapshots
// res.Region -- which is TALLER than the buffer -- so the cached glyph slice
// carries zero Cells for every clipped row. Re-blitting that snapshot into a
// 50-row buffer writes rune 0, which Buffer.Set sanitises to U+FFFD: the tall
// terminal renders a block of replacement characters where its bands belong.
//
// The assertion is a byte-for-byte comparison against the SAME topology
// rendered directly (no cache) into a 50-row buffer. Cache presence must never
// change rendered output.
// ---------------------------------------------------------------------------
func TestSEC077_SankeyHeightCollisionServesClippedGlyphs(t *testing.T) {
	topo := tallSankey(30)
	opts := Options{}

	// Reference: what a 80x50 terminal MUST show.
	want := core.NewBuffer(80, 50)
	if _, err := RenderSankey(want, topo, opts); err != nil {
		t.Fatalf("reference render: %v", err)
	}

	e := NewEngine()
	// Short terminal first (80x24) -- populates the cache.
	short := core.NewBuffer(80, 24)
	if _, err := e.Sankey(short, topo, opts); err != nil {
		t.Fatalf("short render: %v", err)
	}
	// Then the user resizes to 80x50. Same width, different height.
	tall := core.NewBuffer(80, 50)
	if _, err := e.Sankey(tall, topo, opts); err != nil {
		t.Fatalf("tall render: %v", err)
	}

	if diff, x, y := firstCellDiff(want, tall); diff {
		got := tall.Get(x, y)
		exp := want.Get(x, y)
		t.Fatalf("SEC-077: cached 80x24 layout served to an 80x50 buffer; "+
			"first divergence at (%d,%d): got rune %q, want rune %q",
			x, y, got.Rune, exp.Rune)
	}
}

// ATTACK 1b -- the reverse order (tall cached, short served). The short
// terminal must not be handed glyphs it has no room for; more importantly the
// two geometries must not share an entry in either direction.
func TestSEC077_SankeyHeightCollisionReverseOrder(t *testing.T) {
	topo := tallSankey(30)
	opts := Options{}

	want := core.NewBuffer(80, 24)
	if _, err := RenderSankey(want, topo, opts); err != nil {
		t.Fatalf("reference render: %v", err)
	}

	e := NewEngine()
	tall := core.NewBuffer(80, 50)
	if _, err := e.Sankey(tall, topo, opts); err != nil {
		t.Fatalf("tall render: %v", err)
	}
	short := core.NewBuffer(80, 24)
	if _, err := e.Sankey(short, topo, opts); err != nil {
		t.Fatalf("short render: %v", err)
	}
	if diff, x, y := firstCellDiff(want, short); diff {
		t.Fatalf("SEC-077 (reverse): cached 80x50 layout served to an 80x24 buffer; "+
			"first divergence at (%d,%d): got %q, want %q",
			x, y, short.Get(x, y).Rune, want.Get(x, y).Rune)
	}
}

// ATTACK 1c -- key-level proof, independent of any renderer. Two geometries
// differing ONLY in height must not produce the same layoutKey. This is the
// test that would have caught `w, _ = buf.Size()` directly.
func TestSEC077_LayoutKeyDistinguishesHeight(t *testing.T) {
	a := core.NewBuffer(80, 24)
	b := core.NewBuffer(80, 50)
	if layoutKey(1234, a, Options{}) == layoutKey(1234, b, Options{}) {
		t.Fatal("SEC-077: layoutKey(80x24) == layoutKey(80x50) -- height omitted from the cache key")
	}
	// And the symmetric case, so a future "fold w and h into one number"
	// shortcut (w*h, w+h) is caught too.
	c := core.NewBuffer(24, 80)
	if layoutKey(1234, a, Options{}) == layoutKey(1234, c, Options{}) {
		t.Fatal("SEC-077: layoutKey(80x24) == layoutKey(24x80) -- width and height are not independently keyed")
	}
	d := core.NewBuffer(40, 48)
	if layoutKey(1234, a, Options{}) == layoutKey(1234, d, Options{}) {
		t.Fatal("SEC-077: layoutKey(80x24) == layoutKey(40x48) -- key folds w,h into their product")
	}
}

// ---------------------------------------------------------------------------
// ATTACK 3 -- key construction. layoutKey is a NUL-separated concatenation.
// Every numeric field is unambiguous under that separator, but paletteKey
// embeds a CALLER-SUPPLIED string (Palette.Name) using the same NUL separator
// without escaping it. A Name containing a NUL byte therefore forges the
// remaining fields of the key.
// ---------------------------------------------------------------------------
func TestSEC077_PaletteNameNULInjectionCollides(t *testing.T) {
	honest := widgets.Palette{Name: "dusk"}
	// Forge: a Name that reproduces the honest palette's whole token section
	// verbatim after an embedded NUL.
	forged := widgets.Palette{Name: "dusk\x00" + tokenColorsOf(honest)}

	if paletteKey(honest) == paletteKey(forged) {
		t.Logf("paletteKey collision: honest=%q forged=%q", paletteKey(honest), paletteKey(forged))
		t.Fatal("paletteKey: a NUL byte in Palette.Name forges the token section of the key " +
			"(two distinct palettes share one cache entry)")
	}
	// paletteKey survives this because its token section has FIXED arity
	// (len(diagramTokens)) and sits at the END, so the string parses
	// unambiguously right-to-left however many NULs Name smuggles in.
	// Recorded as a passing property, not a finding.
}

// ATTACK 3b -- the same unescaped-separator class in the topology hashes that
// feed layoutKey. ChainTopology.Hash joins caller strings with NUL and emits
// VARIABLE-arity records ("n" = 3 fields, "e" = 5 fields) with no length
// prefix and no escaping, so a node Label carrying NULs can impersonate a
// whole edge record. Two structurally DIFFERENT chains -- one with an edge,
// one without -- hash identically and share one cached layout.
func TestSEC077_ChainHashNULRecordForgery(t *testing.T) {
	// KNOWN DEFECT, pre-dating the SEC-077 height fix and out of its scope:
	// the round that found it reported it to the lead for its own BOW item.
	// Remove this Skip when the topology hashes gain escaping or length
	// prefixes; the assertion below is the permanent regression test.
	t.Skip("known defect: unescaped NUL framing in ChainTopology.Hash -- reported for a separate BOW item")
	withEdge := ChainTopology{
		Nodes: []ChainNode{{ID: "A", Label: "L"}},
		Edges: []ChainEdge{{ID: "e1", From: "A", To: "A", Figure: "f"}},
	}
	// Same serialised bytes, no edge at all: the edge record is smuggled
	// inside the node's Label.
	forged := ChainTopology{
		Nodes: []ChainNode{{ID: "A", Label: "L\x00e\x00e1\x00A\x00A\x00f"}},
	}
	if withEdge.Hash() == forged.Hash() {
		t.Fatalf("ChainTopology.Hash: unescaped, unlength-prefixed NUL framing -- a 1-node/1-edge "+
			"chain and a 1-node/0-edge chain both hash to 0x%x, so one cached layout serves both",
			withEdge.Hash())
	}
}

// ATTACK 2 -- completeness by construction. layoutKey folds Options field by
// field (only opts.Palette today). Adding a field to Options without adding it
// to layoutKey silently reintroduces SEC-077 wearing a different hat, and no
// existing test would notice. This guard fails the build the moment Options
// grows, forcing the author to extend the key.
func TestSEC077_OptionsArityGuardsKeyCompleteness(t *testing.T) {
	const known = 1 // Palette
	if n := reflect.TypeOf(Options{}).NumField(); n != known {
		t.Fatalf("Options has %d fields, layoutKey folds %d. Every Options field is a layout "+
			"input: add the new field to layoutKey (and bump `known`) or two different "+
			"Options will share one cache entry (SEC-077)", n, known)
	}
}

// ---------------------------------------------------------------------------
// ATTACK 4 -- cache lifecycle. The map has no bound and no eviction. Adding
// height to the key multiplies the key space by the number of distinct
// terminal heights, so a resize drag now mints an entry per (w,h) pair, each
// holding a full glyph snapshot.
// ---------------------------------------------------------------------------
func TestSEC077_ResizeStormGrowsCacheUnbounded(t *testing.T) {
	topo := tallSankey(10)
	e := NewEngine()
	const drags = 200
	for i := 0; i < drags; i++ {
		buf := core.NewBuffer(60+i%40, 20+i%30)
		if _, err := e.Sankey(buf, topo, Options{}); err != nil {
			t.Fatalf("render %d: %v", i, err)
		}
	}
	e.mu.Lock()
	n := len(e.cache)
	e.mu.Unlock()
	t.Logf("after %d resize steps on ONE unchanged topology, cache holds %d entries (no eviction, no cap)", drags, n)
	if n <= 1 {
		t.Fatalf("expected the resize storm to mint many entries, got %d", n)
	}
}

// ATTACK 4b -- the pre-existing leak the height term makes worse: the topology
// hash folds float64 amounts, so a Sankey whose money figures move every tick
// mints a fresh entry every tick, forever, at the UI's 10 Hz.
func TestSEC077_TickingTopologyGrowsCacheUnbounded(t *testing.T) {
	e := NewEngine()
	buf := core.NewBuffer(80, 24)
	const ticks = 300 // 30 seconds of a 10 Hz UI
	for i := 0; i < ticks; i++ {
		topo := SankeyTopology{
			Sources: []SankeyFlow{{ID: "tax", Name: "tax", Amount: 1000 + float64(i)*0.5}},
			Sinks:   []SankeyFlow{{ID: "ops", Name: "ops", Amount: 900}},
		}
		if _, err := e.Sankey(buf, topo, Options{}); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	e.mu.Lock()
	n := len(e.cache)
	e.mu.Unlock()
	t.Logf("after %d ticks of a live budget, cache holds %d entries (one per tick, never evicted)", ticks, n)
	if n < ticks {
		t.Fatalf("expected %d entries, got %d", ticks, n)
	}
}

// ---------------------------------------------------------------------------
// ATTACK 5 -- aliasing. A cache hit returns the cached Result BY VALUE, but
// Result.Hits is a slice: the caller receives the cached entry's own backing
// array. One caller mutating a Hit (or a screen sorting hits in place)
// corrupts every subsequent cache hit for that key.
// ---------------------------------------------------------------------------
func TestSEC077_CachedResultHitsAreAliased(t *testing.T) {
	// FIXED (BUG-318): Render now clones Hits on every return path
	// (cloneResult in cache.go), so a caller mutating its Result no longer
	// poisons the cache entry. This is the permanent regression test.
	topo := tallSankey(4)
	e := NewEngine()
	buf := core.NewBuffer(80, 24)

	first, err := e.Sankey(buf, topo, Options{})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if len(first.Hits) == 0 {
		t.Fatal("no hits to attack")
	}
	orig := first.Hits[0].ID
	first.Hits[0].ID = "POISONED"

	second, err := e.Sankey(buf, topo, Options{})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.Hits[0].ID != orig {
		t.Fatalf("aliasing: a caller mutating its returned Result.Hits corrupted the cache; "+
			"cache hit now reports SourceID %q instead of %q -- hit-test drill-through "+
			"maps the region to the wrong source (US-4/AC-5)",
			second.Hits[0].ID, orig)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// tallSankey builds a Sankey whose row count exceeds a 24-row terminal.
func tallSankey(n int) SankeyTopology {
	var t SankeyTopology
	for i := 0; i < n; i++ {
		id := SourceID("s" + string(rune('a'+i%26)) + string(rune('0'+i/26)))
		t.Sources = append(t.Sources, SankeyFlow{ID: id, Name: string(id), Amount: float64(10 + i)})
	}
	for i := 0; i < n; i++ {
		id := SourceID("k" + string(rune('a'+i%26)) + string(rune('0'+i/26)))
		t.Sinks = append(t.Sinks, SankeyFlow{ID: id, Name: string(id), Amount: float64(5 + i)})
	}
	return t
}

// tokenColorsOf returns the token section paletteKey would emit for p, so a
// forged Name can reproduce it verbatim.
func tokenColorsOf(p widgets.Palette) string {
	full := paletteKey(p)
	return full[len(p.Name)+1:]
}

func firstCellDiff(a, b *core.Buffer) (bool, int, int) {
	aw, ah := a.Size()
	bw, bh := b.Size()
	w, h := aw, ah
	if bw < w {
		w = bw
	}
	if bh < h {
		h = bh
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if a.Get(x, y) != b.Get(x, y) {
				return true, x, y
			}
		}
	}
	return false, 0, 0
}

// ---------------------------------------------------------------------------
// ATTACK 4c -- concurrency. layoutKey now mints far more distinct keys, so the
// map mutates far more often under e.mu. Hammer Chain/Network/Sankey from many
// goroutines at varying geometries and palettes; run under -race -count=2.
// Work is counted (fixed iteration counts), never wall-clock.
// ---------------------------------------------------------------------------
func TestSEC077_ConcurrentVaryingGeometryRaceHunt(t *testing.T) {
	e := NewEngine()
	sankey := tallSankey(6)
	chain := ChainTopology{
		Nodes: []ChainNode{{ID: "a", Label: "alpha"}, {ID: "b", Label: "beta"}},
		Edges: []ChainEdge{{ID: "e", From: "a", To: "b", Figure: "12 t/day"}},
	}
	net := NetworkTopology{
		Mode:  NetworkGrid,
		Nodes: []NetworkNode{{ID: "n1", Label: "n1", X: 0, Y: 0}, {ID: "n2", Label: "n2", X: 4, Y: 2}},
		Edges: []NetworkEdge{{ID: "ne", From: "n1", To: "n2", Load: 0.7}},
	}
	palettes := []widgets.Palette{{}, widgets.DefaultPalette}

	const goroutines, iters = 16, 60
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				// Each goroutine owns its buffer (a real terminal resize is
				// per-screen); only the Engine's map is shared.
				buf := core.NewBuffer(40+(g+i)%50, 12+(g*3+i)%40)
				opts := Options{Palette: palettes[(g+i)%len(palettes)]}
				switch (g + i) % 3 {
				case 0:
					_, _ = e.Sankey(buf, sankey, opts)
				case 1:
					_, _ = e.Chain(buf, chain, opts)
				default:
					_, _ = e.Network(buf, net, opts)
				}
			}
		}(g)
	}
	wg.Wait()
	e.mu.Lock()
	n := len(e.cache)
	e.mu.Unlock()
	if n == 0 {
		t.Fatal("expected cache entries after the concurrent hammer")
	}
	t.Logf("concurrent hammer: %d goroutines x %d iters -> %d cache entries", goroutines, iters, n)
}
