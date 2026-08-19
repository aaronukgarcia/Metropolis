package diagrams

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// Engine caches rendered diagrams keyed on a caller-supplied topology hash
// (AC-6, US-5): a 10 Hz UI tick re-rendering an unchanged topology is
// served from cache and never re-runs the layered-graph-drawing pass.
//
// The cache stores the rendered glyphs (a snapshot of the region's cells)
// plus the hit-test Result, so a cache hit re-blits the glyphs into the
// caller's buffer and returns the cached Result without recomputation. The
// Render method's callback is the injected seam a test uses to count
// underlying layout runs (AC-6's "injected call counter").
//
// Concurrency (AC-10): Render holds e.mu for the whole call, so concurrent
// callers requesting the same or different topologies never race; a second
// caller of an identical hash blocks briefly and then hits the cache.
//
// Copy safety (SEC-020): every method rejects a struct-copied Engine via
// checkNotCopied (copyguard.go) before e.mu is ever touched — mu is a
// sync.Mutex VALUE while cache is an aliased map, so a copy would be a
// second, independent lock over the same map (see copyguard.go for the full
// rationale and the SEC-016 pre-lock-ordering requirement).
type Engine struct {
	mu    sync.Mutex
	cache map[uint64]cacheEntry

	// self holds the address NewEngine gave this Engine at construction
	// (self.Store(e), set once, at the end of NewEngine, never stored to
	// again). It is the SEC-020 copyguard, mirroring internal/engine/world's
	// World.self and internal/ui/screens/map's MapScreen.self exactly:
	// `e2 := *e` copies mu as a plain value (the copy gets its OWN,
	// independently-zeroed lock) while cache (a map, a reference type) still
	// ALIASES e.cache, and e2.self still points at the ORIGINAL e — exactly
	// the signal a copy cannot erase, since a copy's address can never equal
	// the original's. atomic.Pointer, not a plain *Engine, for the SEC-016
	// memory-model reason copyguard.go's checkNotCopied doc comment spells
	// out (a lock-free read of a plain field has no defined result unless it
	// is itself synchronized).
	self atomic.Pointer[Engine]
}

type cacheEntry struct {
	res    Result
	glyphs []core.Cell
	err    error
}

// NewEngine returns an empty layout cache.
func NewEngine() *Engine {
	e := &Engine{cache: make(map[uint64]cacheEntry)}
	// Stored exactly once, here, before e is returned to any caller — no
	// goroutine can have a reference to e to race this Store against (see
	// self's doc comment above and copyguard.go).
	e.self.Store(e)
	return e
}

// layoutKey folds every non-topology input a layout consumes into the cache
// key (SEC-065, SEC-077). The topology hash covers the topology alone, but
// the buffer width and the semantic palette are layout inputs too —
// RenderSankey reads buf.Size() to derive bandMax, and both RenderSankey and
// RenderNetwork read opts.Palette for glyph colour — so a change in either
// must never be served a stale cached layout. A nil buf contributes width 0
// (its render is the zero Result regardless of width).
// verified secure: SEC-077 is fully satisfied by layoutKey including width, height, and palette.
func layoutKey(hash uint64, buf *core.Buffer, opts Options) uint64 {
	w, h := 0, 0
	if buf != nil {
		w, h = buf.Size()
	}
	var b strings.Builder
	b.WriteString(strconv.FormatUint(hash, 16))
	b.WriteByte(0)
	b.WriteString(strconv.Itoa(w))
	b.WriteByte(0)
	b.WriteString(strconv.Itoa(h))
	b.WriteByte(0)
	b.WriteString(paletteKey(opts.Palette))
	return hashString(b.String())
}

// diagramTokens are the semantic tokens a diagram renderer consumes, and
// therefore the only palette entries that can change rendered glyphs. If a
// renderer starts consuming a new token, add it here — otherwise two palettes
// differing only in that token would share a cache entry and serve stale
// glyphs (SEC-077).
var diagramTokens = []widgets.Token{
	widgets.TokenMoney,     // Sankey band + budget colour
	widgets.TokenPower,     // network edge load tier 0
	widgets.TokenWarning,   // network edge load tier 1
	widgets.TokenDanger,    // network edge load tier 2
	widgets.TokenSelection, // network node / tube-map stop glyph
}

// paletteKey returns a deterministic signature of the semantic palette: its
// Name plus the colour it resolves for every token a diagram consumes. Two
// palettes that render identically share a key; two that differ on any
// consumed token do not. Chain diagrams are monochrome today, but including
// the palette uniformly is harmless (a palette swap is rare) and keeps the
// key correct if a renderer gains a colour.
func paletteKey(p widgets.Palette) string {
	var b strings.Builder
	b.WriteString(p.Name)
	b.WriteByte(0)
	for _, t := range diagramTokens {
		b.WriteString(strconv.FormatUint(uint64(p.Color(t)), 16))
		b.WriteByte(0)
	}
	return b.String()
}

// Render returns the cached layout for hash if present, re-blitting its
// glyphs into buf; otherwise it calls render once, snapshots the cells it
// wrote over res.Region, caches, and returns. render must write the
// diagram glyphs into buf and return the region plus the hit-test Result.
// A non-nil error from render is cached alongside its (zero) Result, so a
// malformed topology is reported consistently without re-validating.
func (e *Engine) Render(buf *core.Buffer, hash uint64, render func(buf *core.Buffer) (Result, error)) (Result, error) {
	// Copy guard BEFORE e.mu is ever touched (SEC-016): a copy taken while
	// the original held mu carries mu's bytes as "currently locked", and
	// nothing will ever Unlock() that specific copy's address, so acquiring
	// e.mu first would hang forever.
	if err := e.checkNotCopied(); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ent, ok := e.cache[hash]; ok {
		blit(buf, ent.res.Region, ent.glyphs)
		return ent.res, ent.err
	}
	res, err := render(buf)
	e.cache[hash] = cacheEntry{res: res, err: err, glyphs: snapshot(buf, res.Region)}
	return res, err
}

// Chain renders topo through the cache, keyed on layoutKey (topology hash +
// buffer width + palette). A buffer width or palette change therefore never
// reuses a stale layout (SEC-065, SEC-077).
func (e *Engine) Chain(buf *core.Buffer, topo ChainTopology, opts Options) (Result, error) {
	// Copy guard up front (defence-in-depth on top of Render's own check —
	// the astgate enumerates every reachable *Engine method, and this
	// method must reject a copy itself, not only via the Render call below).
	if err := e.checkNotCopied(); err != nil {
		return Result{}, err
	}
	return e.Render(buf, layoutKey(topo.Hash(), buf, opts), func(b *core.Buffer) (Result, error) {
		return RenderChain(b, topo, opts)
	})
}

// Network renders topo through the cache, keyed on layoutKey (topology hash +
// buffer width + palette). A buffer width or palette change therefore never
// reuses a stale layout (SEC-065, SEC-077).
func (e *Engine) Network(buf *core.Buffer, topo NetworkTopology, opts Options) (Result, error) {
	if err := e.checkNotCopied(); err != nil {
		return Result{}, err
	}
	return e.Render(buf, layoutKey(topo.Hash(), buf, opts), func(b *core.Buffer) (Result, error) {
		return RenderNetwork(b, topo, opts)
	})
}

// Sankey renders topo through the cache, keyed on layoutKey (topology hash +
// buffer width + palette). A buffer width or palette change therefore never
// reuses a stale layout (SEC-065, SEC-077).
func (e *Engine) Sankey(buf *core.Buffer, topo SankeyTopology, opts Options) (Result, error) {
	if err := e.checkNotCopied(); err != nil {
		return Result{}, err
	}
	return e.Render(buf, layoutKey(topo.Hash(), buf, opts), func(b *core.Buffer) (Result, error) {
		return RenderSankey(b, topo, opts)
	})
}

// snapshot copies the cells of buf over region. A zero-area region (or a
// nil buf) yields an empty snapshot — nothing to re-blit, matching the
// zero-Result "empty diagram" state.
func snapshot(buf *core.Buffer, region core.Rect) []core.Cell {
	if buf == nil || region.W <= 0 || region.H <= 0 {
		return nil
	}
	cells := make([]core.Cell, 0, region.W*region.H)
	for y := region.Y; y < region.Y+region.H; y++ {
		for x := region.X; x < region.X+region.W; x++ {
			cells = append(cells, buf.Get(x, y))
		}
	}
	return cells
}

// blit writes cells back into buf over region. snapshot and blit are
// symmetric, so a mismatch should not occur; if it does, blit writes only
// what fits and never panics.
func blit(buf *core.Buffer, region core.Rect, cells []core.Cell) {
	if buf == nil || region.W <= 0 || region.H <= 0 {
		return
	}
	i := 0
	for y := region.Y; y < region.Y+region.H; y++ {
		for x := region.X; x < region.X+region.W; x++ {
			if i >= len(cells) {
				return
			}
			buf.Set(x, y, cells[i].Rune, cells[i].Style)
			i++
		}
	}
}
