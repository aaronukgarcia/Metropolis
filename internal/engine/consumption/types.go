package consumption

import "math"

// Utility identifies one of the four §17 utility networks. The underlying
// string is also data/market.json's commodity key for water/power/gas
// (wastewater has no Market commodity — it is an internal network that
// routes to treatment, not a traded good), so a utility's Go identity and
// its Market identity coincide for the three billable utilities (AC-20).
type Utility string

const (
	UtilityWater      Utility = "water"
	UtilityWastewater Utility = "wastewater"
	UtilityPower      Utility = "power"
	UtilityGas        Utility = "gas"
)

// allUtilities is the complete, ordered set of the four networks (AC-6).
// Ordered (not a map) so any caller ranging over it gets a deterministic
// order (GR#21).
var allUtilities = []Utility{UtilityWater, UtilityWastewater, UtilityPower, UtilityGas}

// Demand is one entity's physical consumption draw for one daily tick,
// across the utilities §17 names plus solid waste (a Market commodity, not
// one of the four networks — but part of §17.1/§17.2's coefficient rows,
// so it is carried here for completeness and exposed to engine.market-side
// consumers, never networked-solved by this package).
type Demand struct {
	Water      float64 // litres
	Power      float64 // kWh
	Gas        float64 // kWh
	Wastewater float64 // litres — derived: wastewaterFractionOfWater × Water (AC-5)
	Waste      float64 // kg solid waste (§17.1 household waste / §17.2 wasteKg)
}

// DemandOptions carries the per-tick context that shapes a demand figure.
// It is the single place the all-electric strategy (§17) and the
// seasonal-modifier layer (§9/§17) enter the demand model.
type DemandOptions struct {
	// MonthIndex is the absolute month index (0 = world genesis,
	// monotonically increasing — matching engine.core's Clock.Month()). It
	// selects engine.season's seasonal multiplier for the three seasonally
	// modulated utilities (water, power, gas).
	MonthIndex int64

	// GasNetworkPresent reports whether a gas network exists in the world.
	// When false (the all-electric strategy, §17: "Gas network is optional
	// strategy"), gas demand reroutes to electricity demand — §17.1's
	// "electric-heated homes shift this to E" — on a 1:1 energy basis. No
	// efficiency factor is applied because §17 states none (a deliberate
	// v1 placeholder, logged as an assumption; a heat-pump efficiency factor
	// is a candidate M2 Batch tuning improvement, not a Sprint-4 gap).
	GasNetworkPresent bool
}

// Consumer is one demand-bearing entity (household, building, cell) handed
// to a [Network.Solve]. Demand is the scalar draw in the network's own
// unit (litres for water/wastewater, kWh for power/gas).
type Consumer struct {
	EntityRef string
	Demand    float64
}

// DemandEntity is one entity handed to [UtilityAPI.SolveDailyTick]. It
// carries the coefficient reference (or residential population) that
// SolveDailyTick resolves into a [Demand], plus the full per-utility draw.
type DemandEntity struct {
	EntityRef string
	// ClassRef is a data/consumption.json class key (a data/buildings.json
	// "consumptionRef"). When empty, Population drives the §17.1 per-person
	// residential baseline instead.
	ClassRef   string
	Occupancy  float64 // class throughput/occupancy (ignored when ClassRef == "")
	Population float64 // residential per-person baseline (ignored when ClassRef != "")
}

// ConsumerAllocation is one consumer's conserved share of a solve:
// Delivered + Shortfall == Demand for this consumer, by construction.
type ConsumerAllocation struct {
	EntityRef string
	Demand    float64
	Delivered float64
	Shortfall float64
}

// SolveResult is the conserved outcome of solving ONE network for one tick
// (AC-6). The headline invariant — Delivered + ShortfallTotal == Demand —
// holds exactly by construction (ShortfallTotal is computed as
// Demand - Delivered in a single subtraction, never re-derived).
type SolveResult struct {
	Demand         float64 // total demand across consumers
	Produced       float64 // gross source supply (before losses)
	Loss           float64 // total loss over edges (Produced - postLossSupply)
	Delivered      float64 // total delivered (post-loss, capped at demand)
	ShortfallTotal float64 // Demand - Delivered
	PerConsumer    []ConsumerAllocation
}

// DeliveredByCommodity is [UtilityAPI.BilledAmount]'s input: the delivered
// quantities, per utility, whose money value is to be computed against
// engine.market's prices (AC-20).
type DeliveredByCommodity struct {
	Water float64 // litres
	Power float64 // kWh
	Gas   float64 // kWh
}

// BilledAmount is the money value (in micro-pounds, matching M0-ENG §1.2)
// of one entity's consumed utilities for one tick: delivered quantity ×
// engine.market's per-unit price for water, electricity, and gas (AC-20).
// Wastewater has no Market commodity and solid waste/food bill through
// other consumers, so only the three networked, billable utilities appear
// here. This is the INPUT engine.finance's household-spend stage consumes;
// this package never posts to the ledger itself.
type BilledAmount struct {
	WaterMicropounds float64
	PowerMicropounds float64
	GasMicropounds   float64
}

// Total returns the summed micro-pound value across the three billable
// utilities — the per-entity "spend" figure engine.finance's household
// billing stage needs.
func (b BilledAmount) Total() float64 {
	return b.WaterMicropounds + b.PowerMicropounds + b.GasMicropounds
}

// minFloat/maxFloat are tiny float64 helpers kept local to this file.
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// isFinite reports whether x is a finite, non-NaN float64 — the guard this
// package's Solve uses so a malformed demand/supply figure is rejected as
// an error rather than silently propagating NaN/Inf through the conserved
// accounting (GR#1/GR#16).
func isFinite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}
