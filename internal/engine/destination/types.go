package destination

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/mining"
	"github.com/aaronukgarcia/Metropolis/internal/engine/parking"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tourism"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// ArchetypeKind identifies one of the two §48 buildable destination
// archetypes (US-1/AC-2): the forest holiday resort and the mega-mall.
// The two values are distinct exported identifiers, never cosmetic
// variants of one generic "destination" kind.
type ArchetypeKind uint8

const (
	// ArchetypeForestResort is the §48 forest holiday resort: a woodland
	// archetype carrying the spec's named job count, minimum footprint, and
	// year-round staying-visitor draw shape.
	ArchetypeForestResort ArchetypeKind = iota

	// ArchetypeMegaMall is the §48 mega-mall: a pit-reclamation retail
	// archetype carrying the spec's named job count, shop-equivalent
	// floorspace minimum, and colossal parking demand.
	ArchetypeMegaMall

	numArchetypes
)

// archetypeKeys is the fixed, deterministic iteration order over the two
// archetypes — never a map range on the tick path (GR#21).
var archetypeKeys = []ArchetypeKind{ArchetypeForestResort, ArchetypeMegaMall}

// String returns the archetype's canonical data-file key (matching
// data/destination.json's "archetypes" keys), never a user-visible error
// text (GR#1).
func (k ArchetypeKind) String() string {
	switch k {
	case ArchetypeForestResort:
		return "forestResort"
	case ArchetypeMegaMall:
		return "megaMall"
	default:
		return "unknown"
	}
}

// Archetype is one archetype's exported, data-loaded characteristic set
// (AC-2). Every field is sourced from data/destination.json (GR#15), never
// a Go literal; the magnitudes named by §48 live only in that file.
type Archetype struct {
	Kind              ArchetypeKind
	Name              string
	Jobs              int64
	MinFootprintHa    float64 // site footprint the archetype requires
	MinShopFloorspace int64   // shop-equivalent floorspace floor (mega-mall)
	YearRoundStaying  bool    // steady year-round staying-visitor draw (resort)
	ParkingSpaces     int64   // parking demand pushed to engine.parking
}

// DestinationID identifies one placed destination (a stable, queryable
// identity — AC-10). Assigned by [DestAPI.Place]; the composition root is
// the authority on placement existence.
type DestinationID uint64

// PlacementRequest is the placement command ([DestAPI.Place]). Construction
// is engine.build's job (out of scope); this module validates and records
// the placement against its registered edges.
type PlacementRequest struct {
	// Kind selects the archetype.
	Kind ArchetypeKind

	// SiteKey is the engine.mining extraction-site key whose reclamation
	// state gates a mega-mall placement (AC-3). Ignored for the resort.
	SiteKey string

	// Tile/Local locate the destination cell.
	Tile  world.TileCoord
	Local world.CellLocal

	// FootprintHa is the site footprint offered for the archetype; rejected
	// when below the archetype's data-driven minimum (AC-10).
	FootprintHa float64

	// Screened requests the mega-mall's viewshed screening wall (AC-4).
	Screened bool

	// District is the parking district the mega-mall's demand is accounted
	// against (AC-9); 0 is the unspecified/citywide default.
	District uint16
}

// Dependency seams (GR#20 contract-first): engine.destination consumes each
// registered outbound dependency through a narrow interface, so the
// composition root wires the real implementation and tests inject fakes.
// The compile-time assertions below prove the concrete implementation
// satisfies each seam AND keep the real engine.{tourism,mining,parking}
// imports alive in this package (the registered edges the ACs name, and the
// exact proof form the grep checks look for).

// TourismDraw is the narrow engine.tourism surface destination consumes
// (AC-1): the decomposed portfolio score. It is never recomputed here —
// single source of truth (GR#3, ASM-326).
type TourismDraw interface {
	PortfolioScore() (float64, error)
}

// MiningBlight is the narrow engine.mining surface destination consumes:
// the reclamation-site read (AC-3) and the general viewshed blight model
// with its bund-occluder screening (AC-4).
type MiningBlight interface {
	SiteInfo(siteKey string, correlationID string) (mining.ExtractionSite, error)
	PlaceBlightingObject(spec mining.BlightingObjectSpec) error
	AddBund(tile world.TileCoord, local world.CellLocal, heightM float64, correlationID string) error
	EffectAt(tile world.TileCoord, local world.CellLocal, year int64, correlationID string) (mining.BlightEffect, error)
}

// ParkingSink is the narrow engine.parking surface destination consumes
// (AC-9): registering a destination's parking facility and its space count.
type ParkingSink interface {
	RegisterFacility(facilityID uint64, tile world.TileCoord, local world.CellLocal, spaces int, instType parking.InstrumentType, district uint16) error
}

// Compile-time assertions: the concrete implementations satisfy the seams.
var (
	_ TourismDraw  = (*tourism.TourismAPI)(nil)
	_ MiningBlight = (*mining.BlightAPI)(nil)
	_ ParkingSink  = (*parking.ParkingAPI)(nil)
)
