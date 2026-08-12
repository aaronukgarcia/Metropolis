package world

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is engine.world's storage layer: the §2.3 2x2km tile grid
// over the ~60x60km expansion extent, and each tile's per-cell state
// held as struct-of-arrays (SoA) rather than one Cell struct repeated
// 36M times. Cell (types.go) is only ever assembled on demand as an API
// return value — see memory_test.go for the measured saving this buys.
//
// Two SoA groups per tile, matching AC-10's "unowned tiles are terrain +
// price only, not fully simulated" requirement structurally rather than
// by convention:
//
//   - terrainGrid: elevation/slope/surface. Present for EVERY tile in the
//     expansion extent the moment it is first queried (terrain exists
//     independent of ownership — §2.3: "unowned tiles exist as terrain +
//     price").
//   - simGrid: ownership/zoning/structureRef/landValue/overlay scratch.
//     Allocated ONLY when a tile is purchased (PurchaseTile) — this is
//     what "not fully simulated" means in byte terms: an unowned tile's
//     simGrid pointer is nil, and no overlay-scratch writes are possible
//     against it (ApplyOwnershipCommand and every other mutation command
//     reject a nil-simGrid target with ErrTileNotOwned).
const (
	CellSizeM      = 10                            // §2.4: 10m/cell
	TileSizeCells  = 200                           // §2.1/§2.3: 200x200 cells per 2km tile
	TileSizeM      = TileSizeCells * CellSizeM     // 2000
	ExpansionSizeM = 60000                         // §2.3: ~60x60km Kent extent
	TilesPerSide   = ExpansionSizeM / TileSizeM    // 30
	CellsPerTile   = TileSizeCells * TileSizeCells // 40000
	TotalTiles     = TilesPerSide * TilesPerSide   // 900
	TotalCells     = TotalTiles * CellsPerTile     // 36,000,000
)

// TileCoord identifies one 2x2km purchasable tile in the 30x30 expansion
// grid, rooted at data/georef.json's expansion.swEasting/swNorthing
// (590000, 110000). X grows east, Y grows north; both in [0, TilesPerSide).
type TileCoord struct {
	X, Y int
}

// InExtent reports whether c falls inside the 30x30 expansion grid.
func (c TileCoord) InExtent() bool {
	return c.X >= 0 && c.X < TilesPerSide && c.Y >= 0 && c.Y < TilesPerSide
}

// terrainGrid is the terrain SoA present for every tile: elevation
// (metres AOD), slope class, surface — 6 bytes/cell, packed (no
// inter-field padding: each slice is homogeneously typed).
type terrainGrid struct {
	elevation []float32
	slope     []SlopeClass
	surface   []Surface
}

// simGrid is the full-simulation SoA, allocated only for owned tiles:
// owner/zoning/structureRef/landValue/overlay scratch — 17 bytes/cell.
type simGrid struct {
	owner        []uint32
	zoning       []Zoning
	structureRef []uint32
	landValue    []float32
	traffic      []uint8
	utility      []uint8
	pollution    []uint8
	decay        []uint8
}

func newSimGrid() *simGrid {
	return &simGrid{
		owner:        make([]uint32, CellsPerTile),
		zoning:       make([]Zoning, CellsPerTile),
		structureRef: make([]uint32, CellsPerTile),
		landValue:    make([]float32, CellsPerTile),
		traffic:      make([]uint8, CellsPerTile),
		utility:      make([]uint8, CellsPerTile),
		pollution:    make([]uint8, CellsPerTile),
		decay:        make([]uint8, CellsPerTile),
	}
}

// tile is one 2x2km tile's full state: its terrain (always present),
// its simGrid (present iff owned), and the §32 geology region it belongs
// to (geology.go — coarser-grained than the 10m cell, per-tile here).
type tile struct {
	coord      TileCoord
	terrain    terrainGrid
	sim        *simGrid // nil until PurchaseTile succeeds
	owned      bool
	ownerID    uint32
	geology    geologyRegion
	prospected bool
	onLand     bool // AC-12: computed against the coastline model at generation time
}

func localIndex(localX, localY int) int {
	return localY*TileSizeCells + localX
}

// World is the top-level owner of every tile in the expansion extent —
// the storage this package's WorldAPI (worldapi.go) reads and mutates.
// Tiles are generated lazily on first access (tileLocked) rather than
// all 900 up front, so tests and small scenarios never pay the full
// ~828MB (memory_test.go) unless they actually touch the full extent;
// AC-19's memory-budget test forces full generation to measure the
// worst case.
type World struct {
	mu    sync.RWMutex
	tiles map[TileCoord]*tile

	// startCoord is the TileCoord hosting the real, importer-populated
	// start tile (§2.1) — every other tile's terrain is a deterministic
	// synthetic placeholder (terrain_import.go's synthesizeTerrain; see
	// ASM-* in the dispatch report: this build has no real downloaded OS
	// Terrain 50 data for the other ~899 tiles of the 60x60km extent, so
	// only the one real, licensed fixture-derived tile is genuinely
	// "real ground" today — the rest is a placeholder future importer
	// runs must replace tile-by-tile as real data is downloaded).
	startCoord    TileCoord
	startHeight   [][]float32 // the imported, compressed start-tile heightmap (200x200), or nil pre-import
	milestoneTier int

	// self holds the address NewWorld gave this World at construction
	// (self.Store(w), set once, at the end of NewWorld, never stored to
	// again). It is BUG-064's fix, mirroring engine.core's
	// Engine.self/checkNotCopied (SEC-014/SEC-016) EXACTLY (ASM-427; GR#3
	// — don't invent a new pattern): `w2 := *w` is legal, unsafe-free,
	// reflect-free Go — every field of World is unexported, but that
	// does not stop a caller from dereferencing the *World NewWorld
	// returned and copying the struct value. mu is a plain value, so the
	// copy w2 gets its OWN, independently-zeroed mu — but w2.tiles (a
	// map, a reference type) still ALIASES w.tiles, and w2.self still
	// points at the ORIGINAL w (copied by value, unchanged). That is
	// exactly the signal a copy cannot erase: checkNotCopied compares
	// the receiver's own address against self, and a copy's address can
	// never equal the original's.
	//
	// atomic.Pointer[World], not a plain *World, for the same reason
	// SEC-016 forced Engine.self's type: a plain, unsynchronized field
	// read done lock-free, concurrently with a struct copy that touches
	// the whole struct's memory as one operation, has no defined result
	// in the Go memory model unless the read itself is a properly
	// synchronized operation. Store happens exactly once, in NewWorld,
	// before any goroutine can have a reference to w to race against;
	// every subsequent Load is a single lock-free atomic read requiring
	// nothing else — not mu, nothing a copy could have captured mid-lock.
	self atomic.Pointer[World]
}

// NewWorld constructs an empty World. startCoord is the tile that will
// host the real imported start-tile terrain once ImportAndPlaceStartTile
// runs; every other tile falls back to the deterministic synthetic
// terrain model until a real importer run replaces it.
func NewWorld(startCoord TileCoord) *World {
	w := &World{
		tiles:         make(map[TileCoord]*tile),
		startCoord:    startCoord,
		milestoneTier: 1,
	}
	// Stored exactly once, here, before w is returned to any caller — no
	// goroutine can have a reference to w to race this Store against
	// (BUG-064, mirroring engine.core's NewEngine; see self's doc
	// comment above).
	w.self.Store(w)
	return w
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other World value (BUG-064, mirroring engine.core's
// Engine.checkNotCopied exactly — SEC-014/SEC-016 family). Deliberately
// lock-free — a single atomic.Pointer.Load, requiring nothing else, not
// w.mu, not any other field — so it is safe and correct to call BEFORE
// w.mu is ever touched. A nil w.self.Load() (a World constructed as a
// bare `World{}`/`new(World)` rather than via NewWorld, so self was
// never stored) is treated the same as a mismatch and rejected the same
// way — every documented construction path is NewWorld, so an unset
// self is itself a misuse this same error correctly names.
func (w *World) checkNotCopied(correlationID string, ctx map[string]any) error {
	if w.self.Load() != w {
		return errs.New(ErrWorldCopied, correlationID, ctx)
	}
	return nil
}

// ensureTile returns the tile at c, generating its terrain on first
// access. Callers MUST already hold w.mu (write-locked) — this method
// does no locking of its own, deliberately: every *WorldAPI method takes
// w.mu once for its whole body (a single short critical section) and
// calls this as an internal helper, which keeps the locking discipline
// in exactly one place (WorldAPI's methods) instead of every method
// having to reason about a nested lock/unlock inside a helper it calls
// while already locked — sync.Mutex/RWMutex are not reentrant in Go, so
// that nested pattern is a self-deadlock, not a correctness nuance
// (caught by concurrency_test.go's -race run during dispatch; see the
// dispatch report).
//
// BUG-064 (AC-27): astgate's live-tree scan names this exact function as
// an unguarded reachable function for the World candidate type — it
// calls checkNotCopied itself, defence-in-depth on top of every
// *WorldAPI caller's own pre-lock check, exactly mirroring engine.core's
// RegisterPhaseHook/seal double-check pattern (once before mu, once
// again after — see Engine.self's doc comment). Returns an error rather
// than panicking (GR#1: match engine.core's established failure mode,
// never invent a new one).
func (w *World) ensureTile(c TileCoord) (*tile, error) {
	if err := w.checkNotCopied(errs.NewCorrelationID(), map[string]any{"tile": c}); err != nil {
		return nil, err
	}
	if t := w.tiles[c]; t != nil {
		return t, nil
	}
	t := &tile{coord: c}
	if c == w.startCoord && w.startHeight != nil {
		populateTerrainFromHeightmap(t, w.startHeight)
	} else {
		synthesizeTerrain(t, c)
	}
	t.geology = deriveGeology(c)
	t.onLand = classifyLandSea(c)
	w.tiles[c] = t
	return t, nil
}
