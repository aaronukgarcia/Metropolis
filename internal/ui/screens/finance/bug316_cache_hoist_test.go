package finance

import (
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// BUG-316: the diagrams.Engine backing the §54 Fiscal Circuit panel used to
// be constructed INSIDE the render call, so a fresh, empty cache was built
// and discarded on every frame. ui.diagrams' AC-6 ("an unchanged topology on
// the 10 Hz tick is served from cache") therefore passed in ui.diagrams' own
// unit tests and was false in the running game. These tests assert the
// contract from the OWNER's side, where it was actually broken.
//
// Every assertion here counts work (cache hits, misses, live entries) — never
// wall-clock time, and never testing.AllocsPerRun (unreliable under -race).

const bug316Rows, bug316Cols = 20, 60

func bug316View(scale int64) FiscalCircuitView {
	return FiscalCircuitView{Bands: []SankeyBand{
		{Source: "IncomeTax", Target: "Treasury", Amount: 500_000_000 + scale},
		{Source: "VAT", Target: "Treasury", Amount: 300_000_000},
		{Source: "Budget", Target: "Health", Amount: 400_000_000},
		{Source: "Budget", Target: "Roads", Amount: 200_000_000},
	}}
}

func bug316Rect() core.Rect {
	return core.Rect{X: 0, Y: 0, W: bug316Cols, H: bug316Rows}
}

// renderFrame draws one frame the way a draw func would: a fresh back
// buffer, the screen's own render entry point.
func renderFrame(s *Screen, view FiscalCircuitView, w, h int) *core.Buffer {
	buf := core.NewBuffer(w, h)
	s.RenderSankey(buf, core.Rect{X: 0, Y: 0, W: w, H: h}, view, true, tcell.StyleDefault)
	return buf
}

func bufDump(b *core.Buffer) []rune {
	w, h := b.Size()
	out := make([]rune, 0, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out = append(out, b.Get(x, y).Rune)
		}
	}
	return out
}

// TestBug316UnchangedFramesAreServedFromCache is the fix's purpose test: N
// frames of an unchanged fiscal circuit must cost exactly ONE layout run.
// Pre-fix this is unachievable by construction — a per-frame engine starts
// empty, so every frame is a miss and Hits is permanently 0.
func TestBug316UnchangedFramesAreServedFromCache(t *testing.T) {
	s := New("corr-bug316")
	view := bug316View(0)

	const frames = 10
	for i := 0; i < frames; i++ {
		renderFrame(s, view, bug316Cols, bug316Rows)
	}

	st := s.DiagramCacheStats()
	t.Logf("frames=%d entries=%d hits=%d misses=%d", frames, st.Entries, st.Hits, st.Misses)

	if st.Misses != 1 {
		t.Errorf("misses = %d, want exactly 1 (one layout run for %d identical frames)", st.Misses, frames)
	}
	if st.Hits != frames-1 {
		t.Errorf("hits = %d, want %d (every frame after the first served from cache)", st.Hits, frames-1)
	}
	if st.Entries != 1 {
		t.Errorf("entries = %d, want 1 (one topology rendered)", st.Entries)
	}
}

// TestBug316CachedFrameMatchesFreshRender guards the other half of the fix:
// a cache HIT must put the same glyphs on screen as a fresh render did.
// Hoisting the engine is only safe if the re-blit path is output-identical,
// otherwise the fix trades an allocation for a rendering bug.
func TestBug316CachedFrameMatchesFreshRender(t *testing.T) {
	view := bug316View(0)

	cold := New("corr-cold")
	fresh := renderFrame(cold, view, bug316Cols, bug316Rows)
	if got := cold.DiagramCacheStats(); got.Hits != 0 {
		t.Fatalf("first frame reported %d hits, want 0", got.Hits)
	}

	cached := renderFrame(cold, view, bug316Cols, bug316Rows)
	if got := cold.DiagramCacheStats(); got.Hits != 1 {
		t.Fatalf("second frame reported %d hits, want 1", got.Hits)
	}

	a, b := bufDump(fresh), bufDump(cached)
	if len(a) != len(b) {
		t.Fatalf("buffer size drift: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("cache-hit frame differs from fresh render at cell %d: %q vs %q", i, a[i], b[i])
		}
	}
}

// TestBug316ChangedTopologyRecomputes is AC-6's second half: a changed input
// must NOT be served the previous layout. This is what BUG-319's
// length-prefixed, type-tagged hashing underwrites — the cache key has to
// genuinely separate distinct topologies now that entries survive frames.
func TestBug316ChangedTopologyRecomputes(t *testing.T) {
	s := New("corr-change")

	renderFrame(s, bug316View(0), bug316Cols, bug316Rows)
	renderFrame(s, bug316View(0), bug316Cols, bug316Rows) // hit
	renderFrame(s, bug316View(7_000_000), bug316Cols, bug316Rows)

	st := s.DiagramCacheStats()
	t.Logf("entries=%d hits=%d misses=%d", st.Entries, st.Hits, st.Misses)
	if st.Misses != 2 {
		t.Errorf("misses = %d, want 2 (one per distinct topology)", st.Misses)
	}
	if st.Hits != 1 {
		t.Errorf("hits = %d, want 1", st.Hits)
	}
	if st.Entries != 2 {
		t.Errorf("entries = %d, want 2 (the changed topology must not reuse the old entry)", st.Entries)
	}
}

// TestBug316ResizeChangesTheKey: buffer width and height are layout inputs
// (SEC-065), so a resize must recompute rather than re-blit a layout sized
// for the old terminal.
func TestBug316ResizeChangesTheKey(t *testing.T) {
	s := New("corr-resize")
	view := bug316View(0)

	renderFrame(s, view, 60, 20)
	renderFrame(s, view, 61, 20)
	renderFrame(s, view, 60, 21)
	renderFrame(s, view, 60, 20) // back to the first size — must hit

	st := s.DiagramCacheStats()
	t.Logf("entries=%d hits=%d misses=%d", st.Entries, st.Hits, st.Misses)
	if st.Entries != 3 {
		t.Errorf("entries = %d, want 3 (one per distinct buffer size)", st.Entries)
	}
	if st.Hits != 1 {
		t.Errorf("hits = %d, want 1 (returning to a seen size re-serves its entry)", st.Hits)
	}
}

// TestBug316CacheIsUnboundedUnderMovingAmounts is a CHARACTERISATION test,
// not an approval. It documents, and pins, a real defect the per-frame
// engine was accidentally masking: ui.diagrams' cache has no cap and no
// eviction, and SankeyTopology.Hash folds the flow amounts, so a live budget
// whose figures move every tick mints one entry per tick FOREVER. Each entry
// carries a full glyph snapshot of the region.
//
// If this test ever fails because Entries is LOWER than ticks, that is good
// news — a bound or an eviction policy has landed — and the test should be
// rewritten to assert that bound. It exists so the growth is a decision
// somebody made, not a surprise found in a memory profile.
func TestBug316CacheIsUnboundedUnderMovingAmounts(t *testing.T) {
	s := New("corr-growth")

	const ticks = 300
	for i := 0; i < ticks; i++ {
		renderFrame(s, bug316View(int64(i)*1_000), bug316Cols, bug316Rows)
	}

	st := s.DiagramCacheStats()
	cellsPerEntry := bug316Cols * bug316Rows
	t.Logf("KNOWN UNBOUNDED GROWTH: ticks=%d entries=%d hits=%d misses=%d "+
		"(~%d cells of glyph snapshot per entry, no cap, no eviction)",
		ticks, st.Entries, st.Hits, st.Misses, cellsPerEntry)

	if st.Entries != ticks {
		t.Errorf("entries = %d, want %d — cache growth behaviour changed; "+
			"if a bound landed, update this test to assert it", st.Entries, ticks)
	}
	if st.Hits != 0 {
		t.Errorf("hits = %d, want 0 — every moving-amount tick is a distinct key", st.Hits)
	}
}

// TestBug316ResizeStormGrowth is the second measured growth path: a drag-
// resize sweeps a continuum of widths, and every width is its own key.
func TestBug316ResizeStormGrowth(t *testing.T) {
	s := New("corr-resize-storm")
	view := bug316View(0)

	const steps = 120
	for i := 0; i < steps; i++ {
		renderFrame(s, view, 40+i, bug316Rows)
	}

	st := s.DiagramCacheStats()
	t.Logf("KNOWN UNBOUNDED GROWTH: resize steps=%d entries=%d hits=%d misses=%d",
		steps, st.Entries, st.Hits, st.Misses)
	if st.Entries != steps {
		t.Errorf("entries = %d, want %d", st.Entries, steps)
	}
}

// TestBug316ConcurrentRendersShareOneEngine exercises the lifetime change
// that -race has to cover: pre-fix each frame owned a private engine, so the
// Engine mutex protected nothing that crossed a goroutine. Post-fix the
// engine is shared, and its mutex is load-bearing. Run under -race.
func TestBug316ConcurrentRendersShareOneEngine(t *testing.T) {
	s := New("corr-concurrent")

	const goroutines, framesEach = 8, 25
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < framesEach; i++ {
				// Half the goroutines redraw one unchanged topology (the
				// pure cache-hit path), half sweep distinct ones (the
				// insert path), so hits and misses interleave.
				scale := int64(0)
				if g%2 == 1 {
					scale = int64(i) * 1_000
				}
				renderFrame(s, bug316View(scale), bug316Cols, bug316Rows)
				s.DiagramCacheStats()
			}
		}(g)
	}
	wg.Wait()

	st := s.DiagramCacheStats()
	t.Logf("entries=%d hits=%d misses=%d total=%d", st.Entries, st.Hits, st.Misses, goroutines*framesEach)
	if st.Hits+st.Misses != uint64(goroutines*framesEach) {
		t.Errorf("hits+misses = %d, want %d — a render was lost or double-counted",
			st.Hits+st.Misses, goroutines*framesEach)
	}
	if st.Hits == 0 {
		t.Error("hits = 0 under concurrent identical frames — the shared cache served nothing")
	}
}

// TestBug316CopiedScreenRendersNothing keeps the new method inside the
// screen's SEC-020 copy-guard contract: a struct-copied Screen must refuse,
// not quietly render through the original's engine.
func TestBug316CopiedScreenRendersNothing(t *testing.T) {
	s := New("corr-copy")
	renderFrame(s, bug316View(0), bug316Cols, bug316Rows)
	before := s.DiagramCacheStats()

	cp := screenCopy(s)
	buf := core.NewBuffer(bug316Cols, bug316Rows)
	cp.RenderSankey(buf, bug316Rect(), bug316View(42), true, tcell.StyleDefault)

	if got := cp.DiagramCacheStats(); got.Entries != 0 || got.Hits != 0 || got.Misses != 0 {
		t.Errorf("copied screen reported live stats %+v, want the zero snapshot", got)
	}
	after := s.DiagramCacheStats()
	if after != before {
		t.Errorf("copied screen mutated the original's cache: %+v -> %+v", before, after)
	}
}
