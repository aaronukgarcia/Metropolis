package policies

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// PolicyID names one entry in the policy library (data/policies.json's
// "key" field). It is a stable, data-authored identifier — never a
// human-renamed display string.
type PolicyID string

// DistrictID names a city district (AC-8/ASM-285: districts are the scope
// system and an identity mechanic). engine.policies owns the
// district→cell-reference mapping; the name is queryable metadata (AC-12).
type DistrictID string

// RoadID names a road for road-scoped policies. The road's edge set is a
// scope reference owned by this package; the road geometry itself belongs
// to engine.roads (out of scope here).
type RoadID string

// EnactmentID identifies one enacted policy instance. It is assigned
// deterministically (a monotonically increasing counter) at enactment
// (GR#21).
type EnactmentID string

// CellRef is one world cell reference: a tile coordinate plus a tile-local
// cell coordinate. It reuses engine.world's own coordinate types (GR#3)
// rather than declaring a parallel coordinate system.
type CellRef struct {
	Tile  world.TileCoord
	Local world.CellLocal
}

// EdgeRef is one named-road edge: an ordered pair of cell references. It is
// the scope entity a road-scoped policy resolves to (AC-9).
type EdgeRef struct {
	From CellRef
	To   CellRef
}

// ScopeKind is the three-way scope vocabulary §52 names: citywide,
// district, or road-level.
type ScopeKind string

const (
	// ScopeCitywide affects the whole city (all cells and roads).
	ScopeCitywide ScopeKind = "citywide"
	// ScopeDistrict affects one named district's cell set.
	ScopeDistrict ScopeKind = "district"
	// ScopeRoad affects one named road's edge set.
	ScopeRoad ScopeKind = "road"
)

// Scope is the concrete scope target an enactment/resolution acts against.
// For ScopeCitywide, District and Road are empty; for ScopeDistrict only
// District is set; for ScopeRoad only Road is set.
type Scope struct {
	Kind     ScopeKind
	District DistrictID
	Road     RoadID
}

// valid reports whether s is a well-formed scope: a known kind whose
// district/road payload matches the kind (GR#16 — a citywide scope carrying
// a district ID, or a district scope with no district, is hostile input,
// not a value to normalise).
func (s Scope) valid() bool {
	switch s.Kind {
	case ScopeCitywide:
		return s.District == "" && s.Road == ""
	case ScopeDistrict:
		return s.District != "" && s.Road == ""
	case ScopeRoad:
		return s.Road != "" && s.District == ""
	default:
		return false
	}
}

// CoefficientDelta is one (coefficientKey, delta) pair — the atom of a
// policy's mechanism (AC-2). Key is a curve key in engine.projections' own
// namespace (the coefficient being moved); Delta is the move. Tax, when
// non-nil, additionally routes the move through engine.tax — the routing is
// data-declared (a field on the coefficient), never a name-branched
// mechanism dispatch.
type CoefficientDelta struct {
	Key   string
	Delta float64
	Tax   *TaxMove
}

// TaxMove is the data-declared routing of a coefficient move into
// engine.tax (out of scope: engine.policies does not implement tax
// mechanics itself — it calls TaxAPI). Instrument names a tax instrument
// (e.g. "businessRates"); Mode is the only supported tax-move shape,
// "districtMultiplier" (a freeport-style per-district rate multiplier).
type TaxMove struct {
	Instrument string
	Mode       string
}

// taxMoveDistrictMultiplier is the sole supported TaxMove.Mode. Validated
// at load time; unknown modes are a schema failure, never silently ignored.
const taxMoveDistrictMultiplier = "districtMultiplier"

// CostDef is a policy's declared cost contract (§52 "cost/enforcement
// needs", AC-19): a one-off enactment cost and a recurring monthly opex
// (enforcement) line, both in micro-pounds.
type CostDef struct {
	EnactmentMicroPounds   int64
	OpexMonthlyMicroPounds int64
}

// policyDef is one library entry's runtime form: the immutable, data-loaded
// policy definition. Built once at Load and never mutated afterwards.
type policyDef struct {
	ID         PolicyID
	Name       string
	Category   string
	Scope      ScopeKind
	Mechanism  []CoefficientDelta
	Cost       CostDef
	Conflicts  []PolicyID
	Disclosure string
}

// ScopeResolution is the concrete entity set a scope resolves to (AC-9).
type ScopeResolution struct {
	Kind     ScopeKind
	Citywide bool       // true for citywide scope: the whole city
	District DistrictID // set for district scope
	Cells    []CellRef  // the district's cell set (district scope)
	Road     RoadID     // set for road scope
	Edges    []EdgeRef  // the road's edge set (road scope)
}

// CoefficientSeries is one coefficient key's projected curve — the preview
// surface carrying engine.projections' own Point/Confidence types (AC-5),
// never a policies-local re-invention.
type CoefficientSeries struct {
	Key    string
	Points []projections.Point
}

// Preview is PreviewImpact's result: the identical coefficient-delta payload
// the real enactment applies (AC-4), plus the projected curve per key.
type Preview struct {
	PolicyID PolicyID
	Scope    Scope
	Deltas   []CoefficientDelta
	Series   []CoefficientSeries
}

// PreviewDriftEvent is a raised, queryable reckoning that a stored preview
// and the eventual observed outcome diverged beyond tolerance (AC-7/US-3).
type PreviewDriftEvent struct {
	EnactmentID EnactmentID
	PolicyID    PolicyID
	Coefficient string
	Checkpoint  int64
	Previewed   float64
	Actual      float64
	Magnitude   float64
}
