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
