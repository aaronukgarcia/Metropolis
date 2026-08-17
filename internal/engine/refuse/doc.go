// Package refuse is the waste-collection module (MOD-039): per-cell typed
// bin stock, collection rounds expressed as engine.logistics movements,
// the overflow→vermin→health/land-value/fire-risk chain, the three §25
// waste streams, the landfill/incinerator/compost disposal lifecycle, and
// the refuse-tonnage mass-conservation identity — reduced to its
// STUB-FOR-BASELINE depth (FEAT-083).
//
// Module key: engine.refuse (see code.json)
// GUID:        93cadd89-ef03-4b29-b1b8-1b35c44788b7
// Spec refs:   §25 (Refuse Collection & the Waste–Health Loop — per-cell
//
//	waste generation into typed bin stock: residential wheelie /
//	commercial trade / industrial skip with differing capacities;
//	scheduled rounds run by real trucks on real roads, auto-optimised,
//	player-overridable, consuming road space and fuel like any freight;
//	missed collections leave bin overflow → vermin index up → local
//	physical health down, land value down, fire risk up, the ticker
//	names the street; streams general→landfill/incinerator, recycling
//	with player-set service level and contamination reducing resale,
//	food→composting→farm input); §31 (Farming & the Biodiversity Engine
//	— the compost→farm-input consumer of the food-waste stream).
//
// # Stub-for-baseline depth (FEAT-083 — read this before replacing it)
//
// This package is a COARSE approximation of the full §25 model:
//
//   - Per-cell bin stock is refuse-owned, keyed by land use (AC-2), with
//     the three streams as three distinct sub-stocks (AC-3).
//   - Collection rounds are real [RefuseAPI] commands whose movement is
//     expressed through engine.logistics' registered [logistics.LogisticsAPI]
//     (Deliverable/Stock/Restock) — never a parallel RefuseTruck type
//     (AC-4). Because engine.logistics is itself at stub depth (no junction
//     slot ledger yet), the "saturated junction → next-day queue" behaviour
//     is exercised through logistics' throughput/shortfall machinery (the
//     stub's analogue of the junction queue), not a refuse-local queue.
//   - The overflow chain (AC-7) is directional: vermin index → wellbeing
//     (via the registered seam) → land value → fire risk, with a
//     street-naming ticker event. Magnitudes are data placeholders.
//
// What is DEFERRED to after Baseline One (and to the real dependency
// modules): the full junction-slot truck scheduler (engine.logistics'
// AC-4), the physical-health driver decomposition itself
// (engine.wellbeing's), and the §17 consumption-coefficient waste rate
// (engine.consumption's edge is not registered yet — the rate is a
// data/refuse.json placeholder, see data/refuse.json's $comment).
//
// # Mass-conservation identity (AC-11)
//
// For every stream, at every tick, the identity
//
//	TonnesGenerated == TonnesCollected + TonnesUncollected + TonnesInTransit + TonnesDisposalBacklog
//
// holds exactly (whole kilograms). The four right-hand terms are computed
// INDEPENDENTLY, each from its own source — TonnesCollected from the
// completed-delivery counter, TonnesUncollected from the bin-stock level +
// overflow state, TonnesInTransit from the round movement ledger (the
// refuse-side view of engine.logistics' movement; at full logistics depth
// this is logistics' own ledger), and TonnesDisposalBacklog from the
// disposal sites' own queues — then the identity is CHECKED, never
// constructed to balance by definition. A bug in any one term therefore
// breaks the identity instead of being absorbed into a tautological
// remainder (the engine.wellbeing.md AC-2/AC-3 discipline).
//
// # engine.invariant registration is NOT wired (AC-12)
//
// Refuse tonnage is NOT yet registered with engine.invariant (MOD-019).
// The engine.refuse→engine.invariant edge is absent from code.json — the
// collaborations gate governs it (declare the edge in
// master-plan-v2.1.json's collaborations field to have generate.js enforce
// it); BUG-058 is closed (c36778b) but only registered the
// engine.refuse↔engine.farming edge, not this one. Do not assume the
// registration already happened: AC-11's local identity test is the interim
// proof of correctness until the edge lands and the package adds an
// invariant.Register call at boot.
//
// # Incineration is not strictly dominant (AC-9)
//
// Routing general waste to an incinerator produces energy output at the
// cost of an airshed-pollution term that has NO landfill-side equivalent,
// so a player choosing incineration over landfill faces a real trade-off,
// not a dominant option. That airshed term is distinct from — and in
// addition to — the overflow-driven PollutionExposure consequence of AC-7,
// which is routed through the registered engine.wellbeing seam, never a
// refuse-owned health number (GR#3 single-source-of-truth).
//
// # Compost output (AC-10)
//
// The food-waste stream's collected tonnage becomes an exported, queryable
// compost output ([RefuseAPI.CompostOutput]) at the data-sourced conversion
// ratio. engine.farming consumes that output through the registered
// engine.refuse↔engine.farming edge (c36778b); the consumption-side
// mechanic belongs to engine.farming, not this package.
package refuse
