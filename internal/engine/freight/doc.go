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
package freight
