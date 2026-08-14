// Package consumption is the resource-consumption module (MOD-021): the
// §17 coefficient-driven demand model and the four §17/§2.2 utility
// networks (water, wastewater, electricity, gas).
//
// Module key: engine.consumption (see code.json)
// GUID:        1d4d4bf6-67c7-46c8-8158-089bf3ce1e3c
// Spec ref:    §17 Resource Consumption Model, in full (§17.1 per-person
// daily baseline; §17.2 per-user coefficients by building class; the
// coefficient-driven doctrine "the catalogue never hard-codes utility
// numbers"; wastewater ≈95%-of-water rule; the four networks with
// sources/pipes-wires/storage/losses; the aquifer sustainable-yield
// ceiling; the all-electric-strategy viability); §2.2 Off-map connections
// (Sellindge grid tranche, gas pipeline/LNG, dormant sea/port; "built in
// road corridors for free, cross-country at cost").
//
// # The consumptionRef resolution contract (data.catalogue)
//
// data/buildings.json (FEAT-010) carries a per-entry "consumptionRef" —
// a key into data/consumption.json's "classes" map — and never a raw
// utility number (§17). This package is the module that RESOLVES that
// seam: [UtilityAPI.ClassCoefficients] and [UtilityAPI.ClassDemand] turn a
// consumptionRef into the §17.2 coefficient row and a demand figure
// (coefficient × occupancy/throughput). A consumptionRef that does not
// resolve fails loudly with MET-G301 at reference-resolution time — the
// catalogue never falls back to a silent zero-demand default (AC-13).
//
// # The four networks (AC-6)
//
// Water, wastewater, electricity, and gas are modelled as four
// INDEPENDENTLY-solvable [Network]s, each carrying sources, edges
// (pipes/wires), storage nodes, and a loss factor over edge distance. A
// "solve" is a conserved allocation: for any tick,
//
//	delivered + shortfall == demand
//
// exactly — losses are subtracted from what sources produce BEFORE
// delivery, never invented or dropped silently (see [Network.Solve]).
// Each network is solved independently; the per-network entry point is
// [UtilityAPI.SolveDailyTick].
//
// # Seasonal modifiers (AC-11)
//
// Base coefficients stay pure. engine.season's SeasonAPI curve functions
// (PowerDemandMultiplier, WaterDemandMultiplier, GasDemandMultiplier) are
// applied as a SEPARATE multiplicative layer on top of coefficient-driven
// base demand (§17 "before seasonal modifiers (§9)"). This package never
// re-implements a seasonal curve.
//
// # The all-electric strategy (AC-10)
//
// A city may skip the gas network entirely (§17: "Gas network is optional
// strategy"). When [DemandOptions.GasNetworkPresent] is false, gas demand
// (heating/cooking) reroutes to electricity demand — §17.1's
// "electric-heated homes shift this to E" — with a 1:1 energy shift
// (no efficiency factor is stated in the spec; see the DemandOptions doc).
//
// # Determinism (GR#21)
//
// This package never reads the wall clock: demand, seasonal multipliers,
// and network solves are driven entirely by simulation tick/month (AC-16).
// The solve's allocation order is a documented, deterministic priority
// order (consumers served in sorted EntityRef order), never
// Go-map-iteration-dependent (AC-15).
package consumption
