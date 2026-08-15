package spiral

import (
	"fmt"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// CellRef identifies one world cell: a tile coordinate plus a cell-local
// coordinate (world.TileCoord + world.CellLocal). It is the identity this
// package keys every decay record and command against, consumed from
// engine.world's public types through the registered edge (GR#20) — this
// package never re-derives a cell identity of its own.
type CellRef struct {
	Tile  world.TileCoord
	Local world.CellLocal
}

// key returns the comparable map-key form of c (both world.TileCoord and
// world.CellLocal are comparable structs, so the pair is a valid map key —
// the same pattern engine.build's cellKey uses).
func (c CellRef) key() cellKey { return cellKey{tile: c.Tile, local: c.Local} }

// String renders a compact, deterministic cell identity for logs/tests.
func (c CellRef) String() string {
	return fmt.Sprintf("tile(%d,%d)@(%d,%d)", c.Tile.X, c.Tile.Y, c.Local.Row, c.Local.Col)
}

// cellKey is the comparable internal key form of CellRef.
type cellKey struct {
	tile  world.TileCoord
	local world.CellLocal
}

// Stage is one Detroit-spiral stage (AC-2). Stages are DERIVED from live,
// externally-owned inputs by [DecayAPI.EvaluateStage] — never advanced by an
// internal counter, a wall clock, or a hardcoded stage-advance sequence.
// Each transition predicate takes a real external value (attractiveness
// score, net migration, tax delta, the fiscal distress signal) as its
// argument, so reversing that value halts or reverses the stage progression
// — the "no scripted loss" property §12 requires.
type Stage int

const (
	// StageStable is the pre-shock baseline: no shock recorded, no decline
	// in progress.
	StageStable Stage = iota
	// StageShock: a shock (e.g. major employer closure) has been recorded.
	StageShock
	// StageEmigrationOnset: the attractiveness score has fallen below the
	// data-sourced emigration threshold (spiral.json).
	StageEmigrationOnset
	// StageTaxBaseDecline: tax receipts declined month-over-month (the
	// fiscal signal is a negative tax delta).
	StageTaxBaseDecline
	// StageServiceCutsDebt: the fiscal distress signal (debt/service cuts)
	// is active.
	StageServiceCutsDebt
	// StageAttractivenessDecline: the attractiveness score fell since the
	// previous month (the derivative turned negative).
	StageAttractivenessDecline
	// StageAbandonmentOnset: net migration is negative (emigration) and
	// cells are being abandoned.
	StageAbandonmentOnset
	// StageBlightSpread: abandoned, decayed cells are spreading blight to
	// their neighbours cell-by-cell.
	StageBlightSpread
)

// String returns the stage's canonical name (log/debug use — never the
// GR#1 user-visible error text).
func (s Stage) String() string {
	switch s {
	case StageShock:
		return "shock"
	case StageEmigrationOnset:
		return "emigration-onset"
	case StageTaxBaseDecline:
		return "tax-base-decline"
	case StageServiceCutsDebt:
		return "service-cuts-debt"
	case StageAttractivenessDecline:
		return "attractiveness-decline"
	case StageAbandonmentOnset:
		return "abandonment-onset"
	case StageBlightSpread:
		return "blight-spread"
	default:
		return "stable"
	}
}

// DeathVerdict names a death condition, or none (AC-6/AC-7).
type DeathVerdict int

const (
	// DeathNone: no death condition is active.
	DeathNone DeathVerdict = iota
	// DeathInsolvency: engine.finance's game-over signal has fired (AC-6).
	DeathInsolvency
	// DeathGhostCity: population below 10% of a historic peak that
	// exceeded 50,000 (AC-7), with a qualifying warning on record (AC-15).
	DeathGhostCity
)

// String returns the verdict's canonical name.
func (v DeathVerdict) String() string {
	switch v {
	case DeathInsolvency:
		return "insolvency"
	case DeathGhostCity:
		return "ghost-city"
	default:
		return "none"
	}
}

// EventKind classifies one ordered event in the history log (AC-9's ordered
// sequence, and AC-8's epilogue data source).
type EventKind int

const (
	// EventShock records a shock firing.
	EventShock EventKind = iota
	// EventStageTransition records a stage change (rising or falling).
	EventStageTransition
	// EventBlightSpread records one cell becoming blighted (frontier step).
	EventBlightSpread
	// EventAbandonment records cells being abandoned this month.
	EventAbandonment
	// EventDeath records a death-condition verdict.
	EventDeath
)

// Event is one ordered entry in the spiral history log. It is the unit of
// AC-9's "ordered sequence of stage-transition events" — the reproducible
// outcome is the event sequence plus the final state hash, never a single
// scalar.
type Event struct {
	Month int64
	Kind  EventKind
	Stage Stage        // EventStageTransition: the stage moved to
	Cell  *CellRef     // EventBlightSpread: which cell became blighted
	Count int          // EventAbandonment: how many cells were abandoned
	Death DeathVerdict // EventDeath: which death condition fired
}

// String renders a deterministic, hashable one-line form of the event —
// used by both the epilogue and the AC-9 state hash, so the two can never
// disagree about what happened.
func (e Event) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "month=%d ", e.Month)
	switch e.Kind {
	case EventShock:
		b.WriteString("shock")
	case EventStageTransition:
		fmt.Fprintf(&b, "stage=%s", e.Stage)
	case EventBlightSpread:
		fmt.Fprintf(&b, "blight=%s", e.Cell)
	case EventAbandonment:
		fmt.Fprintf(&b, "abandonment=%d", e.Count)
	case EventDeath:
		fmt.Fprintf(&b, "death=%s", e.Death)
	default:
		b.WriteString("unknown")
	}
	return b.String()
}

// HistoryEntry is one month's recorded history — the epilogue's data source
// (AC-8) and the population curve provider's backing store (AC-15).
type HistoryEntry struct {
	Month          int64
	Stage          Stage
	Population     int64
	Attractiveness float64
	NetMigration   float64
	TaxDelta       int64
	Death          DeathVerdict
}

// DecayState is one abandoned cell's decay record (AC-3). The three §12
// effects — neighbour land-value drag, hazard/fire/crime pressure, and
// demolition cost — are three SEPARATE, independently queryable fields, each
// a pure function of severity (and age, for demolition cost) via
// [DecayAPI.LandValueDrag]/[DecayAPI.HazardPressure]/[DecayAPI.DemolitionCost].
// They are recomputed as a unit whenever severity or age changes; a test can
// raise severity and observe exactly one effect move, independent of the
// other two.
type DecayState struct {
	Cell        CellRef
	AbandonedAt int64 // simulation month the cell was abandoned
	Age         int64 // months since abandonment
	Severity    int   // 0..maxSeverity (spiral.json blight.maxSeverity)

	// The three AC-3 effects (computed from severity/age, independently
	// queryable — never a single merged "blight penalty"):
	LandValueDrag  int64 // micro-pounds of drag imposed on adjacent cells
	HazardPressure int   // 0..100 hazard/fire/crime pressure
	DemolitionCost int64 // micro-pounds to demolish this cell
}

// SpiralMetric is one subscribed spiral-metrics update (AC-1's subscribable
// metrics surface). Published to subscribers on every AdvanceMonth.
type SpiralMetric struct {
	Month        int64
	Stage        Stage
	Population   int64
	DecayedCells int
}
