package build

import "github.com/aaronukgarcia/Metropolis/internal/protocol"

// UnlockState classifies a catalogue entry's unlock badge (BLD-5): the
// resolved unlock decision the engine.unlocks side computed and pushed on
// the view. This screen renders it, never recomputes it (GR#3 — the XP/DP/
// milestone thresholds are engine.unlocks' domain, out of scope here).
type UnlockState int

const (
	// UnlockUnavailable: the view carried no recognisable unlock state for
	// this entry (or the entry's fixture/unlock data is missing at render
	// time) — BLD-8's "unavailable", never a blank row.
	UnlockUnavailable UnlockState = iota
	// UnlockLocked: the entry's unlock gate has not been met.
	UnlockLocked
	// UnlockInProgress: the entry is progressing toward the next tier but
	// not yet unlocked (ASM-258's third badge state).
	UnlockInProgress
	// UnlockUnlocked: the entry is available to build.
	UnlockUnlocked
)

// String renders UnlockState as its badge label (for logs/tests/render).
func (u UnlockState) String() string {
	switch u {
	case UnlockLocked:
		return "locked"
	case UnlockInProgress:
		return "in-progress"
	case UnlockUnlocked:
		return "unlocked"
	default:
		return "unavailable"
	}
}

// BuildOrderStatus is a build order's derived queue state, carried from
// engine.build (the four status strings in its build.go). The screen
// renders the label; it does not derive the state.
type BuildOrderStatus string

const (
	// StatusMaterialsPending: the order's materials bill is not yet fully
	// drawn; it will not complete until it is.
	StatusMaterialsPending BuildOrderStatus = "materials-pending"
	// StatusLabourPending: materials are drawn but labour is not satisfied.
	StatusLabourPending BuildOrderStatus = "labour-pending"
	// StatusInProgress: materials and labour are done; lead time remains.
	StatusInProgress BuildOrderStatus = "in-progress"
	// StatusComplete: the order finished and its zone/structure landed.
	StatusComplete BuildOrderStatus = "complete"
)

// ZoneInfo is one §34 zone type's read-only construction economics, as the
// view carries it (BLD-2). Sourced from "f3.build" zones[]. The zone slug
// (Zone) is an engine-defined string — this package does not enumerate the
// eight types itself (GR#3: the view is the source of which zones exist,
// and ZonePaint validates against it, never against a hardcoded list).
type ZoneInfo struct {
	// Zone is the stable zone slug (e.g. "dwelling", "heavy_industry").
	Zone string
	// Name is the display name (e.g. "Heavy Industry").
	Name string
	// Materials is the construction-materials quantity (tonnes).
	Materials int64
	// Labour is the construction labour requirement (worker-days).
	Labour int64
	// BaseLeadTimeDays is the base construction lead time (simulation days).
	BaseLeadTimeDays int64
}

// BuildOrder is one read-only build-queue entry (BLD-3). Sourced from
// "f3.build" queue[]. Materials/labour/lead-time figures render verbatim;
// this screen simulates none of them.
type BuildOrder struct {
	// ID is the stable order identifier (engine-defined); the drill-through
	// EntityID and the queue row's label.
	ID uint64
	// Cell is the target cell (protocol.CellRef grid coordinates).
	Cell protocol.CellRef
	// Zone is the zone type the order builds.
	Zone string
	// MaterialsBillTotal is the full construction-materials bill (tonnes).
	MaterialsBillTotal int64
	// MaterialsDrawn is the cumulative materials drawn so far.
	MaterialsDrawn int64
	// MaterialsRemaining is the not-yet-drawn remainder.
	MaterialsRemaining int64
	// LabourRemaining is the worker-days not yet satisfied.
	LabourRemaining int64
	// LeadTimeRemaining is the effective lead time left (simulation days).
	LeadTimeRemaining int64
	// Status is the derived queue state.
	Status BuildOrderStatus
}

// CatalogueEntry is one building in the catalogue browser (BLD-5), with
// its resolved unlock badge. Sourced from "f3.build" catalogue[]. The
// cost/capacity/notes text is the spec table's verbatim string (GR#15) —
// this screen renders it, never parses or recomputes it.
type CatalogueEntry struct {
	// ID is the stable building identifier (engine-defined).
	ID string
	// Name is the display name.
	Name string
	// Section is the Part IV/supplement section code (e.g. "R", "E").
	Section string
	// CostRaw is the spec table's cost text, verbatim (may be empty).
	CostRaw string
	// CapacityRaw is the spec table's capacity/density text, verbatim.
	CapacityRaw string
	// Notes is the entry's free-text note, verbatim.
	Notes string
	// Unlock is the resolved unlock badge state (read, never recomputed).
	Unlock UnlockState
}

// LandPriceView is the land-purchase price the view carries for the cell
// currently being considered (BLD-1). Sourced from "f3.build" landPrice.
type LandPriceView struct {
	// Cell is the cell this price applies to.
	Cell protocol.CellRef
	// PriceMicropounds is the purchase price in micro-pounds (M0-ENG §1.2).
	PriceMicropounds int64
}

// DemolitionView is the demolition compensation the view carries for the
// cell currently being considered (BLD-4). Sourced from "f3.build"
// demolition. The figure is the engine's (finance.LandPrice-sourced)
// compensation; this screen surfaces it before the Demolish command.
type DemolitionView struct {
	// Cell is the cell this compensation applies to.
	Cell protocol.CellRef
	// CompensationMicropounds is the compensation in micro-pounds.
	CompensationMicropounds int64
}
