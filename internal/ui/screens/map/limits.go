package mapscreen

import (
	"math"
	"unsafe"
)

// maxGridBudgetBytes (SEC-009) is this screen's allocation ceiling for
// the whole grid slab a single "f1.viewport" full patch may cause
// applyFullLocked to allocate — GR#15: derived from spec data, never a
// hand-picked number.
//
// Derivation: M0-ENG §1.3's memory budget table (docs/METROPOLIS-MASTER-
// v2.1.md, "UI process domain: 150 MB ... UI-SPEC §5, holds views never
// world" — internal/ui/screens/debug/types.go's MemoryBudgetTable
// transcribes the SAME spec table under the SAME citation; both are
// independent transcriptions of one spec source, not one mirroring the
// other, so this package needs no import of ui.screen.debug for it),
// HALVED because render.go's snapshotLocked keeps a full SECOND copy of
// the grid alive concurrently with the live one during every Render call
// (see snapshotLocked's own doc comment) — so worst-case resident grid
// memory is 2x one grid's allocation, and capping a single grid's
// allocation at half the UI process domain budget bounds that worst case
// at the FULL 150 MB budget rather than double-spending it.
const maxGridBudgetBytes = 150_000_000 / 2

// maxGridCells is maxGridBudgetBytes expressed as a cell count, using
// cellData's REAL, compiler-computed size (unsafe.Sizeof — a runtime
// query, not a guessed byte count, GR#15) so this bound self-corrects if
// cellData's fields ever change shape rather than silently drifting from
// reality.
var maxGridCells = maxGridBudgetBytes / int(unsafe.Sizeof(cellData{}))

// maxGridSide bounds EACH of a full patch's Extent.Width/Height
// INDIVIDUALLY, not only their product (SEC-009 requirement 3: "guard
// the multiplication itself" — w*h computed from a wire-supplied w,h can
// overflow int before any comparison against maxGridCells would ever
// catch it, e.g. w=h=3_000_000_000 on a 64-bit int). Set to
// floor(sqrt(maxGridCells)), so w,h <= maxGridSide guarantees w*h <=
// maxGridSide*maxGridSide <= maxGridCells BEFORE the multiplication in
// applyFullLocked ever runs, and that multiplication itself can never
// overflow (both factors bounded well under 1,200 for any of today's
// budget figures, versus int64's ~9.2e18 ceiling).
//
// At today's figures (maxGridBudgetBytes=75,000,000, sizeof(cellData)
// =64), maxGridSide works out to 1082 — roughly 5.4x the real successor
// tile's per-side cell count (data/georef.json startTile.gridCells:
// 200, the 2km/10m-cell tile engine.world/MOD-017 will serve per §2.1)
// and Folkestone-64's 64x64 (internal/engine/stub.FixtureWidth/
// FixtureHeight, this package's actual Sprint 1 fixture) with headroom
// to spare, while remaining many orders of magnitude below anything an
// attacker-supplied Extent could use to OOM this process.
var maxGridSide = int(math.Sqrt(float64(maxGridCells)))

// maxPatchWireBytes (SEC-039 AC-10/AC-12) bounds the raw, on-the-wire
// byte size of a single "f1.viewport" patch's JSON payload — checked in
// decodeWirePatch BEFORE json.Unmarshal ever runs, so an oversized
// patch is rejected at the cheapest possible point (a len(raw)
// comparison on bytes already fully in memory by the time ApplyPatch
// receives them) rather than after paying the full parse/allocation
// cost: the Destructive-1 PoC measured 1.43s and a 198,300,096-byte
// wire payload to decode+iterate a 300,000-entry Cells array hidden
// behind a declared 1x1 Extent, and that cost is dominated by
// json.Unmarshal itself, which completes and returns a fully-populated
// wirePatch BEFORE len(p.Cells) is ever available to check against
// anything — so a check placed after decode (maxGridCells below, AC-11)
// is necessary but not sufficient; this gate is what actually stops the
// expensive step from running at all.
//
// Population (distinct from maxGridBudgetBytes, GR#15 / the SEC-033
// lesson named explicitly in this wave's acceptance criteria):
// maxGridBudgetBytes/maxGridCells above bound the DECODED, packed
// cellData grid slab (~64 bytes/cell via unsafe.Sizeof, computed at
// package init). This constant bounds a DIFFERENT population — the
// WIRE, JSON-encoded form of the same logical patch data, which for the
// same logical cell count is necessarily larger: a wireCell entry's
// JSON shape (`{"x":...,"y":...,"terrain":"...","elevation":...,
// "road":"...","building":"..."}`) carries field-name/quoting overhead
// and human-readable string content that cellData's packed struct never
// does. Reusing maxGridBudgetBytes's number verbatim under a new name
// would repeat SEC-033's mistake one level up (a bound derived from the
// wrong population); this is a distinct, separately-derived number.
//
// Derivation: set to 2x maxGridBudgetBytes (150,000,000 bytes / ~143
// MiB). The multiplier is a documented, generous allowance for the
// wire-vs-packed size gap above, not an independent guess: a full patch
// describing the ENTIRE maxGridCells-sized grid (this file's own
// ceiling) would need to average under 2x cellData's packed per-cell
// size in wire bytes to fit within this budget — comfortably true for
// this project's real terrain/building name lengths today
// (internal/engine/stub/viewport.go's real fixture data includes
// "Folkestone Harbour Arm", 22 characters; data/georef.json's longest
// cited real feature name, "M20 Junction 13 / Castle Hill Interchange",
// is 42) — while still landing meaningfully BELOW the SEC-039 PoC's
// demonstrated 198,300,096-byte attack payload (which used artificially
// inflated 200-byte junk strings per field, specifically to maximise
// wire size while staying under a tiny declared Extent, so it would
// slip past maxGridCells' output-side check), so the exact attack shape
// measured is rejected here, before json.Unmarshal ever runs on it. Not
// a spec-cited figure (no §-numbered wire-payload budget exists in the
// master plan) — logged as an ASM- per v1.7; re-derive if this
// project's real terrain/road/building name content ever grows enough
// to approach this ceiling for a legitimately large full-grid patch.
const maxPatchWireBytes = 2 * maxGridBudgetBytes

// maxUnknownTerrainSeen bounds the number of DISTINCT unrecognised
// terrain surface strings this screen will remember for BUG-334's
// log-once dedupe (logUnknownTerrainOnce). The dedupe exists so a
// 40,000-cell grid of a new, not-yet-taught surface logs once, not once
// per cell; but the map must not grow without bound either (round D5) —
// a hostile or corrupt engine stream could otherwise mint unbounded
// distinct surface strings and grow unknownTerrainSeen forever. The
// cap: the real Terrain 50 class set is small and closed (today five:
// grass, woodland, water, shingle, rock — render.go terrainGlyph), so a
// dozen-ish distinct unknowns already means a genuinely novel class or
// a corrupt stream; 64 distinct surfaces is far past any legitimate
// signal while still bounding the map at a couple hundred bytes. Past
// the cap, logUnknownTerrainOnce stops recording new surfaces entirely —
// they still draw glyphUnknown ('?') but are not logged — so a
// pathological stream can neither grow the map without bound nor trigger
// a per-cell log storm (the 40,000-line case the dedupe exists to
// prevent). GR#15: derived from the renderer's own known-class
// population (terrainGlyph's switch arms), never a hand-picked constant.
const maxUnknownTerrainSeen = 64
