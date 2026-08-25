package world

import "math"

// This file defines the per-cell contract (§2.4, AC-4): the public
// read-only Cell snapshot returned by WorldAPI.CellAt, and the small
// enums it is built from. The AUTHORITATIVE storage is struct-of-arrays
// (grid.go's cellGrid) — Cell exists only as an API return value
// assembled on demand, never stored 36M-times-over itself (see grid.go's
// doc comment and memory_test.go for the actual per-cell byte cost this
// buys).

// SlopeClass is the four documented buildability bands (§2.4), derived
// from the imported heightmap's local gradient (slope.go) at build time.
// It multiplies construction cost and gates which structure kinds
// engine.build permits on a cell (AC-5).
type SlopeClass uint8

const (
	SlopeFlat SlopeClass = iota
	SlopeGentle
	SlopeSteep
	SlopeUnbuildable
)

func (s SlopeClass) String() string {
	switch s {
	case SlopeFlat:
		return "flat"
	case SlopeGentle:
		return "gentle"
	case SlopeSteep:
		return "steep"
	case SlopeUnbuildable:
		return "unbuildable"
	default:
		return "unknown"
	}
}

// CostMultiplier is the construction-cost multiplier engine.build (a
// later-sprint consumer) applies for a cell of this SlopeClass (AC-5).
// Values are a Sprint-3-time placeholder tuned only for "steep costs more
// than flat, unbuildable is not buildable at all" — the real tuning pass
// belongs to engine.build once it exists; see ASM-* in the dispatch
// report for why 1.0/1.4/2.2/+Inf was chosen here rather than left for
// that module to invent independently (a slope class with NO exposed
// multiplier would leave engine.build unable to honour §2.4's "slope
// class multiplies construction cost" without reaching into this
// package's internals, which GR#20 forbids).
func (s SlopeClass) CostMultiplier() float64 {
	switch s {
	case SlopeFlat:
		return 1.0
	case SlopeGentle:
		return 1.4
	case SlopeSteep:
		return 2.2
	case SlopeUnbuildable:
		return math.Inf(1)
	default:
		return math.Inf(1)
	}
}

// Surface is the visible ground cover of a cell (§2.4).
type Surface uint8

const (
	SurfaceGrass Surface = iota
	SurfaceWoodland
	SurfaceWater
	SurfaceShingle
	SurfaceRock
)

func (s Surface) String() string {
	switch s {
	case SurfaceGrass:
		return "grass"
	case SurfaceWoodland:
		return "woodland"
	case SurfaceWater:
		return "water"
	case SurfaceShingle:
		return "shingle"
	case SurfaceRock:
		return "rock"
	default:
		return "unknown"
	}
}

// Zoning is the per-cell land-use designation (§2.4). v1 is deliberately
// the skeleton-era minimum — engine.zoning (MOD-026, a later sprint) owns
// the real 8-way zoning vocabulary and build queue; this package only
// carries the field so engine.build/engine.zoning have somewhere to
// write it via WorldAPI's ownership/zoning command path (GR#20 — this
// package does not itself interpret zoning values beyond storing them).
//
// FEAT-199 (2026-08-23): ZoningOffice and ZoningMining were APPENDED at
// the end of the iota block (never inserted mid-list) so every value
// already persisted or in flight keeps its meaning. compose's KindZone
// write-through maps the data/zoning.json six families onto this enum:
// residential->Residential, commercial->Commercial, office->Office,
// industry->Industrial, farming->Agricultural, mining->Mining.
type Zoning uint8

const (
	ZoningNone Zoning = iota
	ZoningResidential
	ZoningCommercial
	ZoningIndustrial
	ZoningAgricultural
	ZoningOffice
	ZoningMining
)

// MaxZoningDensity bounds the per-cell zoning density level a command may
// carry (FEAT-199): 0 = unzoned, 1..5 = the data-driven ladder
// data/zoning.json declares per zone family. The per-family min/max live
// in that file (GR#15); this constant is only the storage-space ceiling
// ApplyOwnershipCommand hard-rejects above.
const MaxZoningDensity uint8 = 5

// OverlayScratch is the per-cell "flow scratch" (§2.4): small
// contents-independent working values recomputed every relevant tick by
// downstream network modules (traffic, utility coverage, pollution,
// decay). engine.world stores the bytes and exposes them read/write via
// WorldAPI commands; it assigns no meaning to the values themselves —
// that belongs entirely to the consumer modules that write them
// (engine.traffic, engine.consumption's utility networks, etc.), keeping
// this package a passive ledger for this part of the cell (GR#20: this
// package doesn't reach into those modules' logic, and they don't reach
// into this package's internals beyond the WorldAPI command surface).
type OverlayScratch struct {
	Traffic         uint8
	UtilityCoverage uint8
	Pollution       uint8
	Decay           uint8
}

// Cell is the read-only per-cell snapshot WorldAPI.CellAt returns —
// the exact ~30-byte-core field set §2.4/AC-4 documents: elevation,
// slope class, surface, ownership, zoning, structure ref, land value,
// and the per-overlay flow scratch. See grid.go's cellGrid for how this
// is actually stored (packed struct-of-arrays, not one Cell per cell)
// and memory_test.go for the measured real per-cell byte cost.
type Cell struct {
	Elevation    float32 // metres above Ordnance Datum
	Slope        SlopeClass
	Surface      Surface
	Owner        uint32 // 0 = unowned; otherwise an opaque owner/player ID
	Zoning       Zoning
	StructureRef uint32 // 0 = no structure; otherwise an opaque structure ID (engine.build's)
	LandValue    float32
	Overlay      OverlayScratch

	// ZoningDensity is the cell's density level within its Zoning family
	// (FEAT-199): 0 = unzoned/unset, 1..5 = the data-driven ladder
	// data/zoning.json declares per family (MaxZoningDensity is the hard
	// ceiling ApplyOwnershipCommand enforces). Stored as one packed byte
	// in simGrid's SoA; the per-family min/max interpretation is the
	// consumers' (compose/UI), never this package's (GR#20).
	ZoningDensity uint8
}
