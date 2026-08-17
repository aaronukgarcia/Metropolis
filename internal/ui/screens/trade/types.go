package trade

import "github.com/aaronukgarcia/Metropolis/internal/ui/widgets"

// ContractStatus classifies an import contract's lifecycle state (TRD-1).
// The screen renders a cancelled contract as a retired row (like demo's
// retired typology), never deletes it, so the player can still see it
// existed.
type ContractStatus int

const (
	// StatusActive: the contract is in force; its term is counting down.
	StatusActive ContractStatus = iota
	// StatusCancelled: the player cancelled the contract (a cancel past the
	// penalty-free window incurred CancellationPenaltyMicropounds).
	StatusCancelled
)

// String renders ContractStatus for logs/tests.
func (s ContractStatus) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// ImportContract is one import contract (TRD-1): the term, the
// cancellation penalty, and the £/unit price the player holds a position
// on. Sourced from "f5.trade" contracts[].
type ImportContract struct {
	// ID is the stable contract identifier (engine-defined); used for
	// drill-through EntityIDs and the cancel/create command target.
	ID string
	// Commodity is the commodity this contract imports (an engine-defined
	// commodity key, e.g. "grain").
	Commodity string
	// TermMonths is the contract's full term in months.
	TermMonths int
	// MonthsRemaining is the term left to run (0 = expiring this month).
	MonthsRemaining int
	// CancellationPenaltyMicropounds is the penalty charged for cancelling
	// once past the penalty-free window (0 = still penalty-free, or no
	// penalty clause). Micropounds (M0-ENG §1.2), never a float.
	CancellationPenaltyMicropounds int64
	// PricePerUnitMicropounds is the £/unit import price.
	PricePerUnitMicropounds int64
	// Status is the contract's lifecycle state.
	Status ContractStatus
}

// JunctionApproach is one junction approach's queue state (TRD-2 / §33 /
// UI-SPEC §2): a lane of cargo-coded truck glyphs and its wait-time
// figure. Sourced from "f5.trade" junctions[].approaches[].
type JunctionApproach struct {
	// ApproachID names the approach within the junction (engine-defined,
	// e.g. "north").
	ApproachID string
	// Cargo is the cargo class this lane's trucks carry, driving the glyph
	// widgets.QueueLane draws (reused verbatim, not reimplemented).
	Cargo widgets.CargoKind
	// TruckCount is the number of queued trucks (the glyph-run length).
	TruckCount int
	// WaitSeconds is the wait-time figure, rendered as "Ns" by QueueLane.
	WaitSeconds int
}

// JunctionQueue is one junction's live queue view (TRD-2): the signature
// truck-glyph image. Sourced from "f5.trade" junctions[].
type JunctionQueue struct {
	// JunctionID is the stable junction identifier (engine-defined).
	JunctionID string
	// Label is the human-readable junction name; JunctionID shown if empty.
	Label string
	// Approaches are the per-approach queue lanes, in the engine's order.
	Approaches []JunctionApproach
}

// WarehouseCommodity is one commodity's warehouse stock/buffer policy
// state (TRD-3): how much is held, how much slack the player is paying
// to hold, and the current flow. Sourced from "f5.trade" warehouse[].
type WarehouseCommodity struct {
	// Commodity is the commodity key (engine-defined).
	Commodity string
	// StockTonnes is the current held stock.
	StockTonnes int64
	// CapacityTonnes is the warehouse capacity for this commodity.
	CapacityTonnes int64
	// BufferTonnesPerDay is the player-set safety-buffer target in t/day —
	// the only unit the spec fixes for flow figures (ASM-251).
	BufferTonnesPerDay int64
	// FlowTonnesPerDay is the current flow through the warehouse in t/day.
	FlowTonnesPerDay int64
}

// PortState is the port panel's operational state (TRD-4): berths, crane
// rate, and customs throughput. Sourced from "f5.trade" port. The
// unlocked flag is engine.unlocks-sourced data carried on the view — this
// screen reflects it, never implements its own tier-gating logic.
type PortState struct {
	// Unlocked is the unlock state read from the view (engine.unlocks).
	Unlocked bool
	// Berths is the number of berths (0 = port not yet built).
	Berths int64
	// CraneRateTonnesPerHour is the crane rate in t/hr.
	CraneRateTonnesPerHour int64
	// OperatingHoursPerDay is the daily operating hours.
	OperatingHoursPerDay int64
	// CustomsThroughputTonnesPerDay is customs throughput, a figure
	// separate from physical berth/crane throughput (§33/§28).
	CustomsThroughputTonnesPerDay int64
	// SmugglingRisk is the §28 smuggling-risk indicator in [0,1].
	SmugglingRisk float64
}

// TradeFlow is one commodity's or one artery's per-day trade figure
// (TRD-5): t/day and £/day, both sourced from the same ledger entry.
type TradeFlow struct {
	// Key is the commodity or artery identifier (engine-defined).
	Key string
	// TonnesPerDay is the t/day figure.
	TonnesPerDay int64
	// ValuePerDayMicropounds is the £/day figure (micropounds).
	ValuePerDayMicropounds int64
}

// TradeLedgerView is one side (import or export) of the balance of trade,
// broken down by commodity AND by artery (§33). Sourced from "f5.trade"
// balance.imports / balance.exports.
type TradeLedgerView struct {
	// ByCommodity is the per-commodity breakdown, in the engine's order.
	ByCommodity []TradeFlow
	// ByArtery is the per-artery breakdown (road/rail/sea), in the
	// engine's order.
	ByArtery []TradeFlow
}

// BalanceOfTradeView is the import/export breakdown the F5 extension shows
// (TRD-5): two independently-sourced ledgers, never one as the other's
// complement. Sourced from "f5.trade" balance.
type BalanceOfTradeView struct {
	Imports TradeLedgerView
	Exports TradeLedgerView
}

// SafetyCorridor is one corridor's pipeline-vs-truck safety comparison
// (TRD-6 / §50). Sourced from "f5.trade" safety.corridors[]. NOTE: this
// section is forward-compatible only — the chemical/fuel network's data
// is not a registered code.json outbound edge for this screen (BUG-058
// candidate), so a patch carrying safety data is not yet expected; the
// screen renders "unavailable" when the section is absent (SF-7/TRD-8).
type SafetyCorridor struct {
	// Corridor names the corridor (e.g. "port-refinery").
	Corridor string
	// PipelineCapacityTonnesPerDay is the chemical/fuel pipeline grid's
	// capacity for the corridor.
	PipelineCapacityTonnesPerDay int64
	// TruckMovementsPerDay is the truck-movement count the pipeline would
	// otherwise remove from the same corridor.
	TruckMovementsPerDay int64
	// LeakRisk is the pipeline leak-event risk in [0,1].
	LeakRisk float64
}
