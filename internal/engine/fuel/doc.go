// Package fuel implements the Metropolis vehicles, fuel and EV-transition
// module (BOW MOD-062; module key `engine.fuel`; GUID
// 85cdba62-361d-4ee1-9ae6-500da6676de5; spec §49 "Vehicles, Fuel & the EV
// Transition").
//
// §49 is the mechanical core this package realises: every vehicle-km burns
// something, and both somethings are systems. This package owns the fleet
// composition per milestone era (car/van/truck ICE-vs-EV split, with trucks
// electrifying last), the fuel-demand figures it implies, the hour-of-day EV
// charging load (an evening-peak concentration that stacks with electric
// heating into the §49 winter-grid-crisis), the strategic-reserve sizing, the
// fuel-duty revenue flow (the "fat early tax line" that erodes as EV share
// grows), the forecourt-network coverage that must track city growth, and the
// JIT fragility where a fuel shortage strands the very logistics that fix
// shortages.
//
// # Data-driven placeholders (ASM-307)
//
// The EV-share-by-era curve (data/fuel.json's "eras") and the strategic-reserve
// sizing (data/fuel.json's "strategicReserve.daysOfCover") are DATA-DRIVEN
// PLACEHOLDERS pending Aaron's confirmation — directional figures only, no
// final magnitude decided here (the balance-number regime). Tests assert SHAPE
// (EV share strictly rises across eras; trucks lag cars; a stocked reserve
// mitigates a shortage), never a specific tuned value. Every other balance
// figure (fuel demand, charging base/weights, duty rate, forecourt target,
// tanker throughput) follows the same placeholder convention and is named
// in-file with its source category.
//
// # Blocked mechanics (BUG-058)
//
// Two §49 mechanics are BLOCKED pending BUG-058 because neither outbound edge
// is registered in code.json, and this package does not import the packages
// (GR#20 — no silent routing around an unregistered edge):
//
//  1. Fuel commodity pricing (engine.fuel → engine.market). The per-unit fuel
//     price and the imported-fuel cost booking (the "money leaves via imports"
//     half of the balance-of-trade picture) cannot be booked without the edge.
//     Fuel duty (AC-4) is therefore posted as a function of consumption VOLUME
//     and the duty RATE (a specific per-litre excise — engine.tax's figure),
//     never the commodity price. A second local commodity-price table is
//     deliberately NOT built (GR#3).
//  2. The terraces-can't-charge equity gating (engine.fuel → engine.households).
//     Dwelling-typology data (driveways vs terraced street frontage) lives in
//     engine.households; AC-5's grid-coupling test covers the aggregate
//     evening-peak mechanic but does not gate individual charger availability
//     by dwelling typology until that edge lands.
//
// # Cross-module edges (code.json)
//
// Outbound calls: engine.traffic (fleet composition per era — the fleet
// ICE/EV split traffic's mode-share will consume; engine.traffic is still a
// stub with no fleet-composition consumer surface at this depth, so this
// package exposes [FuelAPI.FleetComposition] as the producer-side query and
// defers the actual traffic call), engine.consumption (charging load into
// UtilityAPI — [FuelAPI.ChargingLoad] returns a [consumption.Demand] the
// composition root sums into the power solve), engine.tax (fuel-duty posting —
// [FuelAPI.PostFuelDuty]), engine.logistics (JIT fuel-gated replenishment —
// [FuelAPI.ReplenishmentDelivery]).
//
// # Determinism
//
// Nothing in this package reads the wall clock (GR#21). Fleet-era transitions,
// charging-load profiles and shortage events are pure functions of the loaded
// data and the injected simulation state (era key, hour of day, tanker
// throughput, strategic reserve) only.
package fuel
