// Package freight is the freight harbour, tonnes accounting and
// production-chain module (MOD-047): the §33 freight port, the five
// production-chain families whose stages register as firms, the four
// storage-site types, the modal-reach bulk caps (§8), the independently
// sourced balance-of-trade breakdown, and the mass-conservation identity
// that keeps every tonne of freight accounted for.
//
// Module key: engine.freight (see code.json)
// GUID:        3ce6d7c8-1aa6-46f4-94d1-07a20cf656e3
// Spec refs:   §33 (The Freight Harbour — Tonnes & Chains); §8 (Logistics &
//
//	Just-in-Time, artery #2 framing — freight shares the road
//	junction-slot mechanism with commuter traffic)
//
// # What this package does
//
//   - [FreightAPI.PortCapacity] — §33's port throughput = berths × crane
//     rate (t/hr) × operating hours, never a flat daily-tonnage constant
//     (AC-2).
//   - [FreightAPI.CustomsCapacity]/[FreightAPI.CustomsSaturation]/
//     [FreightAPI.SmugglingRisk] — customs throughput as a figure SEPARATE
//     from the physical berth/crane throughput, with a smuggling-risk
//     indicator that rises as customs saturation rises (AC-3, §28).
//   - Chain stages loaded from data/freight.json — the five §33 families
//     (construction, steel & machinery, food, consumer goods, energy) as
//     ordered stage sequences, each with input t/day → output t/day, jobs,
//     power/water draw and a blight class, output bounded by input
//     availability (AC-4/AC-5).
//   - The four storage-site types (quayside stacks, silos, tank farm, cold
//     store) with type-matched commodities (AC-6).
//   - Freight movements by road/rail/sea with §33/§8 modal caps (road 25t,
//     rail 1,000t, sea 3kt–40kt), capacity-checked through engine.logistics
//     (AC-7/AC-13).
//   - An independently-sourced balance-of-trade (imports vs exports, t/day
//     and £/day, never one as the other's complement) (AC-9).
//   - The mass-conservation identity (AC-10) below.

// # The mass-conservation identity (AC-10)
//
// For every accounting period (one daily tick — [FreightAPI.AdvanceTick])
// and every tonne-unit commodity the freight system tracks, the identity
//
//	Produced == ConsumedDownstream + Exported + StorageDelta + InTransitDelta
//
// holds exactly, with every term INDEPENDENTLY sourced (never inferred as a
// remainder):
//
//   - Produced           — from each chain stage's own output ledger (AC-5);
//   - ConsumedDownstream — from the NEXT stage's own input ledger (the
//     downstream stage's own draw — AC-10's explicit requirement, the
//     engine.logistics.Draw-ledger stand-in at this stub depth);
//   - Exported           — from the departure ledger's own tracked
//     departures (AC-9's export accessor);
//   - StorageDelta       — closing − opening, from each storage site's own
//     stock accessor (AC-6);
//   - InTransitDelta     — net in-transit change (Ship departures minus
//     arrivals), from the movement ledger (AC-7).
//
// The COMPLETE stock identity additionally carries the Imported inflow:
//
//	Produced + Imported == ConsumedDownstream + Exported + StorageDelta + InTransitDelta
//
// AC-10's identity is the Imported == 0 special case (a closed chain). The
// accounting period is one daily tick; [FreightAPI.ConservationAccount]
// exposes every term, and [FreightAPI.VerifyConservation] is the interim
// local check standing in for the engine.invariant registration below.

// # engine.invariant registration is BLOCKED (AC-11, BUG-058)
//
// code.json's own engine.freight inbound "pattern" field reads
// "chains from data; stages register as firms; tonnes conserved (invariant)"
// — the registry's own text promises invariant registration. But as of this
// build, code.json still shows (a) no engine.freight → engine.invariant
// outbound call edge, and (b) engine.invariant's inbound consumers list
// without engine.freight. Per GR#20 this package consumes siblings ONLY
// through registered interfaces, so it does NOT import engine.invariant in
// production code yet — the registration is deferred until the code.json
// edge lands (BUG-058). A mechanical tripwire (BUG-100) in the acceptance
// doc treats the edge landing as "rewrite AC-11 into a real check"; until
// then [FreightAPI.ConservationAccount]/[FreightAPI.VerifyConservation] are
// the interim proof. Do NOT assume the registration has happened.

// # Chain stages ARE firms (AC-4) — currently a BLOCKED dependency
//
// code.json's "stages register as firms" pattern requires every chain stage
// to register as a real firm through engine.firms (MOD-058), not a
// freight-owned pseudo-firm. engine.firms is still open (no package exists),
// so this package defines the [FirmRegistrar] dependency-inversion seam that
// engine.firms' FirmsAPI will implement when it lands; until then every
// [ChainStage] carries the zero [Firm] and the seam is unset. A stage's
// jobs/staff and premises are the firm-shape fields (AC-5), but the firm
// LIFECYCLE (founding, growth, credit) is engine.firms' own job — never
// reimplemented here (GR#3).

// # Stub-for-baseline depth (FEAT-083 — read before replacing)
//
// This package is built to Baseline One depth, against the CURRENT state of
// its registered dependencies:
//
//   - engine.logistics (MOD-025) is itself a STUB-FOR-BASELINE module: its
//     full junction-slot/truck-movement scheduler is deferred. Freight
//     movements therefore enforce their modal caps locally and resolve
//     capacity through [logistics.LogisticsAPI.Deliverable] (the stub's
//     single deterministic "how much can be delivered" number), rather than
//     contending for a per-junction slot ledger that does not exist yet
//     (AC-7's junction-saturation half is deferred with it).
//   - engine.firms (MOD-058) does not exist yet — see the AC-4 block above.
//   - engine.invariant registration is blocked on BUG-058 — see above.
//
// The "in transit" term covers city-internal Ship movements (port↔estate,
// estate↔estate); export departures are recorded at command time (the
// tonne has left the city's books) and sea/rail export transit is deferred
// to the full logistics movement model.

// # Factory types (feat.factorytypes, FEAT-105)
//
// This package also hosts feat.factorytypes alongside the engine.freight
// module key it shares a package with: the factory-unit catalogue (§33 The
// Freight Harbour; §46 Multinational Attraction / FDI & Anchor Employers;
// §34 Zoning; §17 Resource Consumption Model). It surfaces through
// [FreightAPI.FactoryType]/[FreightAPI.FactoryTypes] with no separate
// inbound contract (ASM-682).
//
// The load-bearing contract (AC-2): each of the eight factory types —
// assembler, steel mill, electronics, chemicals converter, food processing,
// textiles, cement, glass — is a DISTINCT modelled facility with its own
// footprint, input-output pair, jobs, utility draw and blight class, never
// a generic factory row carrying a type string and defaulted fields. Two
// same-category types resolve to two different parameter sets.
//
// The single-source-of-truth boundary (AC-5, ASM-680): a factory type that
// corresponds to a §33 chain stage (steel mill ↔ steelMill, cement ↔
// cementPlant, food processing ↔ flourMill) carries only a stageRef into
// data/freight.json and re-exports that stage's input-output/jobs/power/
// water/blight by reference through one code path — there is no second,
// driftable copy. Footprint (cells) is facility-level and lives in
// data/factorytypes.json for every type, because the chain stages carry no
// footprint. The five types with no chain stage carry their params inline.
// See factorytype.go for the mechanics and the balance-number regime below.
//
// Balance-number regime (ASM-683): every per-type figure — footprint,
// input-output t/day, jobs, utility draw, blight class — is a placeholder
// in data/factorytypes.json, each carrying a disclosure naming it pending
// Aaron's balance pass. No AC is satisfied by a final number; tests check
// shape, direction and distinctness, never a specific magnitude.
//
// Cross-references (not restated here): engine.freight.md AC-4/AC-5 own the
// "stages register as firms" and per-stage input/output/jobs/power-water/
// blight data this catalogue's stageRef re-exports; engine.fdi.md (MOD-059)
// owns the prospect/bid/commitment mechanics whose §46 archetypes map onto
// these per-type params (semiconductor fab → electronics, chemicals complex
// → chemicals converter, steel process plant → steel mill); engine.firms
// (MOD-058) hires a chain-stage firm against the resolved per-type jobs.
// The build-catalogue entries for these structures live in data/buildings.json
// (FEAT-010) and must reference a type key, never duplicate the numbers
// (ASM-694).

// # Determinism (AC-14/AC-15)
//
// Every tick, throughput, storage, movement and trade-rollup computation is
// a pure function of (tick, prior state, commands) — no wall-clock read
// (the wall-clock accessor scan over this package's non-test files returns
// no matches, AC-15), and every map iteration that feeds a result is sorted
// (GR#21). Re-running the same command sequence from identical state yields
// byte-identical output across worker counts.

// # Loading, data, and errors (GR#7/GR#15)
//
// [Load] reads data/freight.json (self-contained loader, the engine.mining
// LoadDepositParams pattern) plus data/market.json and data/logistics.json
// through the registered engine.market and engine.logistics edges. Every
// balance figure — port/customs, modal caps, storage capacities, the
// commodity taxonomy and its market/storage mapping, and the per-stage
// rates/jobs/power/water/blight — lives in data/freight.json, never as a Go
// literal in this package (GR#15). Every failure is a registry-sourced
// *errs.E (MET-G9xx, this package's claimed sub-range — see errors.go).

// # feat.containerport (FEAT-099) — the deep-sea terminal shares this package
//
// This package is also the shared home of feat.containerport (key
// feat.containerport, alongside the engine.freight module key this package
// already carries — see code.json). The deep-sea container-terminal surface
// lives here as a file/type set ([ContainerPort], containerport*.go), NOT as
// a fork or a second package, mirroring how feat.megafacilities shares
// engine.mining's package and feat.resourcedeposits does the same. code.json's
// feat.containerport entry (path internal/engine/freight/) has NO inbound
// contract of its own (inbound name/format/pattern all null) because it
// surfaces through this package rather than a separate inbound contract
// (GR#20). Its outbound call set — engine.freight, engine.rail,
// feat.facilitypermits, feat.decommission — is registered in code.json.
//
// The tier relationship (AC-2): the deep-sea terminal is a DISTINCT rung of
// the §33 port ladder ABOVE container_terminal (and cargo_port_small) —
// cargo_port_small → container_terminal → deep_sea_terminal — expressed by
// extending FreightAPI's berths × crane rate × hours capacity model to
// deep-sea scale (40 kt container ships), never by replacing or forking the
// existing port entries. See [ContainerPort.PhysicalCapacity] /
// [ContainerPort.TierPhysicalCapacity], which call FreightAPI's own
// [FreightAPI.PortCapacityFor] rather than reimplementing the formula.
//
// The intermodal reuse commitment (AC-4): the sea↔rail↔road container-transfer
// point is engine.rail's (engine.rail.md AC-3's tonnes-conservation contract),
// consumed through the [RailIntermodal] dependency-inversion seam — never a
// containerport-local parallel transfer ledger. The seam is the same
// consumer-driven shape freight already uses for engine.firms ([FirmRegistrar]);
// engine.rail imports this package and implements the seam (the
// stub-for-baseline stand-in in internal/engine/rail does today). Until the
// seam is wired, [ContainerPort.IntermodalTransfer] rejects every call as an
// unregistered intermodal point.
//
// The customs reuse commitment (AC-5): the deep-sea tier's customs throughput
// is tracked separately from its physical berth/crane throughput, and its
// smuggling-risk indicator rises as customs saturation rises — reusing
// FreightAPI's own customs model ([FreightAPI.CustomsSaturationFor] /
// [FreightAPI.SmugglingRiskFor]) rather than a new smuggling model. The two
// capacities saturate independently.
//
// The balance-number regime (AC-13): every figure in data/containerport.json
// — milestone, cost, berths, crane rate, operating hours, customs throughput,
// the 40 kt ship tonnage, jobs — is a PLACEHOLDER pending Aaron's balance
// pass, each carrying a non-empty disclosure naming it. No AC is satisfied by
// a junior-invented final figure; tests check direction/structure only.
//
// The permit/decommission inheritance (AC-7): building the deep-sea terminal
// is permit-gated through feat.facilitypermits (FEAT-053) and carries a
// day-one decommission liability through feat.decommission (FEAT-054),
// consumed through the [PermitAuthority] / [DecommissionRegistrar] seams —
// neither obligation is reimplemented as a containerport-local permit check or
// liability ledger, and no permit-state or liability-provision field lives on
// [ContainerPort].
//
// Spec refs: §33 (The Freight Harbour — the port ladder + capacity model this
// tier extends), §47 (Rail Industry — the intermodal container crane's
// sea↔rail↔road transfer), §28 (Crime/Policing — the customs house +
// smuggling-interdiction rate this tier's customs capacity feeds), and
// resources-design-brief.md §8/§9 (the "genuine deep-sea container terminal"
// upgrade of the port milestone). Cross-references, not restatements:
// engine.freight.md (the model extended), engine.rail.md (the intermodal
// contract consumed), feat.megafacilities.md (FEAT-055 — owns the
// expert-workforce GATE for the same terminal; this feature owns the
// mechanics), feat.facilitypermits.md (FEAT-053) and feat.decommission.md
// (FEAT-054) (the inherited obligations).
package freight
