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
// maxCacheEntries bounds the live layout cache (BUG-321). Once the Engine is
// hoisted so it lives across frames (BUG-316), a plain unbounded map turns
// per-frame churn into unbounded retention: SankeyTopology.Hash folds the
// float64 money amounts, so a live budget whose figures move every tick mints
// a fresh key every tick forever, and adding buffer height to layoutKey
// (BUG-342) multiplies that by the number of distinct terminal heights a
// resize drag passes through. Measured on the hoisted-but-unbounded engine:
// ~1.2 GB/hour retained at the 10 Hz UI tick, never released.
//
// Why 64 rather than a round number: the only live consumer is the finance
// screen, which draws at most ONE Sankey diagram per frame. 64 therefore
// retains the most-recently-rendered 64 distinct (topology, width, height,
// palette) tuples — comfortably a whole resize drag's worth of recent
// geometries plus several seconds of a ticking budget — while capping
// worst-case retention at 64 entries. An entry is a full glyph snapshot; an
// 80x50 region is roughly 64 KB of core.Cell, so 64 entries bound the cache
// at about 4 MB worst case, two orders of magnitude below the unbounded leak.
// A resize storm thrashes the cache, which is correct: resizing is transient,
// and thrashing costs re-layout, not memory.
const maxCacheEntries = 64

type Engine struct {
	mu    sync.Mutex
	cache map[uint64]cacheEntry

	// accessSeq is a monotonic counter bumped once per Render call under
	// e.mu. Each cache entry records the accessSeq of its most recent
	// touch (insert or hit) in cacheEntry.lastAccess, and eviction removes
	// the entry with the smallest lastAccess — a least-recently-used policy.
	// Because accessSeq is unique per access, no two live entries ever share
	// a lastAccess, so the LRU victim is uniquely determined regardless of
	// Go's randomised map-iteration order (GR#21: deterministic eviction,
	// no ties to break nondeterministically).
	accessSeq uint64

	// hits/misses/evictions back CacheStats (BUG-321). Evictions are
	// surfaced deliberately: a silently-evicted entry that then misses looks
	// identical to a cache that never worked — which is exactly the class of
	// defect BUG-316 was — so the eviction count makes the difference
	// observable. All three are written only under e.mu.
	hits      uint64
	misses    uint64
	evictions uint64

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

	// lastAccess is the Engine.accessSeq value of this entry's most recent
	// touch (its insert, or its most recent cache hit). It is the LRU
	// recency key; see Engine.accessSeq for why uniqueness makes eviction
	// deterministic (GR#21).
	lastAccess uint64
}

// CacheStats is an observable snapshot of the layout cache (BUG-321).
// Entries is the current live entry count (bounded by maxCacheEntries);
// Hits/Misses/Evictions are monotonic totals since construction. Evictions
// is exposed so a "served from cache unless evicted" miss is distinguishable
// from a cache that never worked at all.
type CacheStats struct {
	Entries   int
	Hits      uint64
	Misses    uint64
	Evictions uint64
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
// the buffer width, the buffer height, and the semantic palette are layout
// inputs too — RenderSankey reads buf.Size() to derive bandMax, and both
// RenderSankey and RenderNetwork read opts.Palette for glyph colour — so a
// change in any of the three must never be served a stale cached layout. A
// nil buf contributes width 0 and height 0 (its render is the zero Result
// regardless of width or height).
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
	e.accessSeq++
	seq := e.accessSeq
	if ent, ok := e.cache[hash]; ok {
		// Touch the entry so a hit refreshes its LRU recency (write the
		// value back — cacheEntry is stored by value).
		ent.lastAccess = seq
		e.cache[hash] = ent
		e.hits++
		blit(buf, ent.res.Region, ent.glyphs)
		return cloneResult(ent.res), ent.err
	}
	e.misses++
	res, err := render(buf)
	// Bound the cache (BUG-321): evict the least-recently-used entry before
	// inserting at the cap, so the map never exceeds maxCacheEntries. The
	// hoisted engine (BUG-316) lives for the whole session, so without this
	// the map would grow without limit.
	if len(e.cache) >= maxCacheEntries {
		if evictLRU(e.cache) {
			e.evictions++
		}
	}
	e.cache[hash] = cacheEntry{res: res, err: err, glyphs: snapshot(buf, res.Region), lastAccess: seq}
	return cloneResult(res), err
}

// evictLRU removes the single entry with the smallest lastAccess from cache
// and reports whether one was removed. It is a free function over the map (not
// an *Engine method) so it holds no lock of its own — the caller (Render) owns
// e.mu for the whole critical section and has already passed the SEC-020 copy
// guard, so there is no aliased Engine state for evictLRU to re-validate.
// Because lastAccess values are unique (Engine.accessSeq is monotonic and
// assigned once per access), the minimum is unique and the victim is fully
// determined regardless of Go's randomised map-iteration order (GR#21). O(n)
// at n = maxCacheEntries (64) is negligible.
func evictLRU(cache map[uint64]cacheEntry) bool {
	var victim uint64
	var victimSeq uint64
	first := true
	for k, ent := range cache {
		if first || ent.lastAccess < victimSeq {
			victim, victimSeq, first = k, ent.lastAccess, false
		}
	}
	if first {
		return false
	}
	delete(cache, victim)
	return true
}

// Stats returns an observable snapshot of the cache (BUG-321). A struct-copied
// Engine is rejected (SEC-020) before e.mu is touched, exactly as Render is.
func (e *Engine) Stats() (CacheStats, error) {
	if err := e.checkNotCopied(); err != nil {
		return CacheStats{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return CacheStats{
		Entries:   len(e.cache),
		Hits:      e.hits,
		Misses:    e.misses,
		Evictions: e.evictions,
	}, nil
}

// cloneResult returns a Result whose Hits slice is a fresh copy, never the
// cache entry's own backing array (BUG-318). Result is returned by value on
// every path (a cache hit and a fresh render alike), but Hits is a slice —
// without this clone a caller that sorts hits in place, or writes to a Hit,
// permanently poisons the cached entry for every future cache hit on the
// same key (SEC-077 round: a mutated Hit reported SourceID "POISONED"
// instead of "sa0", misrouting drill-through, US-4/AC-5).
//
// A shallow copy is enough here: Hit's fields (core.Rect, SourceID) are both
// plain value types with no nested slice, map, or pointer, and Result.Region
// is a value-type core.Rect too — so Hits is the only field on either type
// that can alias shared state. Cloning it on every return (not just on a
// cache hit) also protects the FIRST caller of a freshly rendered topology:
// e.cache[hash] stores the same res the first caller receives, so without
// this, caller #1 mutating its Result would poison the entry before caller
// #2 ever sees a cache hit.
func cloneResult(res Result) Result {
	res.Hits = append([]Hit(nil), res.Hits...)
	return res
}

// Chain renders topo through the cache, keyed on layoutKey (topology hash +
// buffer width + buffer height + palette). A buffer width, buffer height,
// or palette change therefore never reuses a stale layout (SEC-065,
// SEC-077).
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
// buffer width + buffer height + palette). A buffer width, buffer height,
// or palette change therefore never reuses a stale layout (SEC-065,
// SEC-077).
func (e *Engine) Network(buf *core.Buffer, topo NetworkTopology, opts Options) (Result, error) {
	if err := e.checkNotCopied(); err != nil {
		return Result{}, err
	}
	return e.Render(buf, layoutKey(topo.Hash(), buf, opts), func(b *core.Buffer) (Result, error) {
		return RenderNetwork(b, topo, opts)
	})
}

// Sankey renders topo through the cache, keyed on layoutKey (topology hash +
// buffer width + buffer height + palette). A buffer width, buffer height,
// or palette change therefore never reuses a stale layout (SEC-065,
// SEC-077).
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
