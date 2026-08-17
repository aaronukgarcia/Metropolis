// Package chemicals is the shared home of engine.chemicals (MOD-063) and its
// two sibling features — feat.refinery (FEAT-102) and feat.commoditymarket
// (FEAT-052). engine.chemicals owns the §50 oils/rubber/plastics chain
// machinery (ChemAPI); feat.refinery owns the facility the player builds (the
// refinery + petrochemical works catalogue enrichment, the make-vs-buy
// decision, and the crude→chain→fuel→risk wiring); feat.commoditymarket owns
// the "Billingham/Wilton-class integrated chemical works" and "crackers" as
// ChemAPI chain stages. Each feature is a file/type set in this package, not a
// fork or a second package (GR#20).
//
// # Shared-package, no-separate-inbound-contract arrangement (GR#20)
//
// code.json's feat.refinery entry (path internal/engine/chemicals/) has NO
// inbound contract of its own (its inbound name/format/pattern are all null)
// because it shares engine.chemicals' package and surfaces through that
// module's ChemAPI. Its outbound call set is registered: engine.chemicals
// (ChemAPI) and engine.fuel (FuelAPI). The crude/tanker (engine.freight),
// hazmat-fire (engine.dispatch) and blight-registration (engine.mining) edges
// are engine.chemicals' OWN outbound edges — available through MOD-063 without
// feat.refinery re-registering them — and are consumed here through
// dependency-inversion seams (see refinery.go's FreightAPI/DispatchAPI and the
// permit/decommission seams below), never through a direct import of those
// modules.
//
// # feat.refinery — the facility + make-vs-buy wiring (FEAT-102)
//
// An oil refinery + integrated chemical works as a DISTINCT modelled facility:
// crude by tanker to tank farm to refinery (fuel + feedstock) to petrochemical
// works to plastics, carrying top blight class and the hazmat-fire category,
// with make-vs-buy at its largest scale as the headline decision — no refinery
// means importing refined product at margin and skipping the chain.
//
// Spec refs: §50 (Oils, Rubber, Plastics & the Chemical Network — crude lands
// by tanker at the port, the refinery is a major build with fuel + feedstock
// outputs, top blight class + §26 hazmat fire category, petrochemical works →
// plastics → rubber → tyre plant; "No refinery? Import refined product at
// margin and skip the chain — the make-vs-buy doctrine at its largest scale");
// §49 (Vehicles, Fuel & the EV Transition — the refinery is the fuel system's
// upstream supply: tanker truck → forecourts → fuel duty); §26 (Emergency &
// Care Dispatch — the unified dispatch engine the hazmat-fire category feeds);
// and resources-design-brief.md §6 (the "Billingham/Wilton-class integrated
// chemical works" and "crackers" archetypes; "sell ore cheap now, or invest in
// the works and sell refined dear later — a north-star long bet").
//
// # North-star vetting (feat.refinery.md, "North-star vetting")
//
// Q5 (the long bet) is the headline, and §50's own words: "sell raw now or
// invest in the works and sell refined dear later" is the make-vs-buy doctrine
// at its largest scale. Building the refinery is a huge, permit-gated,
// blight-heavy, decommission-liability-carrying capex bet against an
// always-available import-at-margin alternative. AC-3 requires that bet to
// have both directions reachable from the data — never a strictly-dominant
// option. The two directions are: BUILD (capex + permit + top blight +
// decommission liability, cheaper per unit at scale) and IMPORT (no capex, but
// the margin paid per unit forever — always available, and rational at small
// scale). The bidirectionality is asserted by a test that compares the data
// file's own figures at high and low throughput, never a pinned winner.
//
// # The GR#15 data file
//
// Every tunable figure — footprint, throughput, jobs, utility draw, capex,
// opex, amortisation horizon, the chain input/output pair, the hazmat-fire
// period/severity, and the import-at-margin unit cost — is sourced from
// data/refinery.json at load time (file path: data/refinery.json). None of it
// is a Go numeric literal: a balance pass edits the data file, never this
// package (GR#15, AC-2). The import-at-margin unit cost itself is ASM-321's
// figure (MOD-063's data, engine.chemicals.md AC-4), MIRRORED in
// data/refinery.json as a stub placeholder until MOD-063's data file lands and
// CONSUMED through the ChemAPI import surface — feat.refinery does NOT own or
// re-specify it (ASM-703).
//
// # ASM scope split (ASM-702/ASM-703)
//
// engine.chemicals.md's five-stage chain machinery, the chemical/fuel pipeline
// network, the leak-event risk, and the import-at-margin economics are
// MOD-063's, consumed-not-re-specified: this feature registers its two stages
// (refinery, petrochemical works) against ChemAPI (AC-5) and reuses the import
// path (AC-3), but authors none of that machinery. engine.fuel.md's fleet
// composition, duty erosion, EV transition and forecourt coverage are
// MOD-062's — this feature only feeds the fuel-supply surface (AC-6).
// engine.freight.md's port/berth/crane/customs accounting is MOD-047's — this
// feature only consumes crude landings as freight tonnage (AC-4). The margin
// figure is ASM-321's (ASM-703).
//
// # Money and type safety (AC-11)
//
// Monetary and tonnage/job-count arithmetic uses int64 micro-pounds at the
// scale engine.finance's Money type documents (1 GBP = 1,000,000 micro-pounds,
// M0-ENG §1.2), and the project's saturating helpers (foundation/num). The
// engine.chemicals→engine.finance edge is not registered in code.json (GR#20),
// so this package does NOT import engine.finance for its Money type — it uses
// plain int64 micro-pounds, the same choice the freight package's
// feat.containerport makes for its cost figure. No float32/float64 touches a
// monetary or tonnage field.
//
// # The blight-class half of AC-7 is BLOCKED (BUG-058)
//
// The refinery's top-blight "max" class stays single-sourced in
// data/buildings.json (GR#3 — this feature does not re-declare it). Its
// registration as a blighting object against engine.mining's BlightAPI is the
// same unregistered engine.chemicals→engine.mining edge that engine.chemicals.md
// AC-13 documents as blocked pending BUG-058. Until that edge lands, the blight
// half of AC-7 is documented-not-built; the hazmat-fire dispatch edge
// (engine.chemicals→engine.dispatch) IS registered and is buildable now, so
// this feature wires the hazmat-fire category through the DispatchAPI seam
// (AC-7). This feature builds no refinery-local blight-radius or fire-spread
// model (ASM-706).
//
// # Permit & decommission inheritance (AC-8)
//
// The refinery build is permit-gated through feat.facilitypermits (FEAT-053,
// "permits for ANY large facility") and carries a day-one "put back to nature"
// decommission liability through feat.decommission (FEAT-054, §7). Both are
// consumed through the PermitAuthority/DecommissionRegistrar seams — neither
// the three-route permit gate nor the liability ledger is reimplemented here,
// and no permit-state or liability-provision field lives on this feature's
// struct.
//
// # Determinism (AC-10)
//
// Facility-profile derivation, the make-vs-buy comparison, crude throughput,
// chain-stage tonnage flow, the fuel feed and the hazmat-fire risk are all
// pure functions of (worldSeed, tick, loaded data, commands) — never the wall
// clock and never a shared/global RNG source. The hazmat-fire decision draws
// from foundation/det's counter-based Stream keyed by (worldSeed, tick).
//
// # Cross-references (not restated)
//
//   - engine.chemicals.md (MOD-063) — the five-stage chain, ChemAPI stage
//     machinery, make-vs-buy import path, pipeline, and leak risk; consumed.
//   - engine.fuel.md (MOD-062) — the fuel-supply surface this feature feeds.
//   - engine.freight.md (MOD-047) — the port/tanker tonnage the crude supply
//     consumes as ordinary freight.
//   - feat.facilitypermits.md (FEAT-053) and feat.decommission.md (FEAT-054) —
//     the permit gate and decommission liability this feature inherits.
//   - feat.commoditymarket.md (FEAT-052) — the sibling feature that owns the
//     integrated-works "crackers" chain stage; this feature owns the facility
//     the player builds and must not re-author that stage.
//
// # Error range
//
// feat.refinery raises MET-G2600..MET-G2606, registered in data/errors.json
// with the reserved range G2600-G2699 (owner: feat.refinery).
package chemicals
