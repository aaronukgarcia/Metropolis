// Package fdi is the shared home of engine.fdi (MOD-059, Multinational
// Attraction & anchor employers) and feat.pharmacampus (FEAT-101, the
// pharma/R&D campus — Groton-Pfizer-class). This file documents the
// feat.pharmacampus half, which lives in this package as a file/type set
// alongside MOD-059's eventual FdiAPI, NOT as a fork or a second package.
//
// # Shared-package, no-separate-inbound-contract arrangement (GR#20)
//
// code.json's feat.pharmacampus entry (path internal/engine/fdi/) has a null
// inbound name/format/pattern because it shares engine.fdi's package and
// surfaces through that module's FdiAPI (GUID 83febe83-2068-4620-9ba4-88117dd89773).
// It registers three outbound calls — engine.fdi, engine.education and
// engine.firms — which are the modules this feature consumes today, plus a
// pending engine.freight trade edge for the campus's exports (AC-6, see
// [TradeEdge]). Every edge is consumed through a local consumer-driven seam
// the composition root adapts to the real API (contract-first, stub-forever):
//   - [EducationEdge] — the education term/demand shape (open cross-module
//     contract ASM-698) adapted to the real *education.EducationAPI (the same
//     seam engine.education itself uses for its TrafficAPI).
//   - [FirmsEdge] — RegisterFirm + RemoveFirm. Both are satisfied directly by
//     the real *firms.FirmsAPI (SEC-159): RegisterFirm registers the anchor as a
//     real firm, and RemoveFirm is the real API's genuine compensating inverse
//     (removes the firm and decrements the founded count with no insolvency
//     semantics), so the anchor is a real firm retrievable and closeable through
//     FirmsAPI's own §32 path (AC-6) and a partial win rolls back through a
//     genuine inverse, never the insolvency path (AC-8, SEC-140).
//   - [TradeEdge] — AddExports, adapted to the real *freight.FreightAPI
//     (Export + Exports ledger) so the campus's exports are a real queryable
//     trade flow, never a pharma-local counter (AC-6). The engine.freight
//     outbound registration is the composition root's to wire when it lands.
//
// # North-star vetting — Q5 is the headline
//
// The design's own words name the long bet: "education + FDI compounding
// into a high-value anchor employer". The bet is two-directional and
// compounding:
//
//   - First leg (AC-3): a city that funds education produces an
//     education-output term (graduates/research) that makes the pharma
//     prospect's bid genuinely better — higher education output wins, or
//     wins on strictly better terms, than an otherwise-identical city; a
//     sufficiently under-educated city loses to the off-map region, so the
//     bet has a real losing side.
//   - Second leg (AC-4): a won campus feeds graduate/research demand back
//     into the education system, scaling with its own employment — so the
//     loop is education → FDI → education, not one-way.
//
// The consequence-sticks (Q3) and snowball (Q4) halves are real too: the
// won campus is a real firm whose closure is the §32 one-employer-town shock
// (AC-6), and it spawns §45 supply-chain firms (AC-5) and exports (AC-6).
//
// # The GR#15 data file (AC-2)
//
// Every tunable figure this feature uses — footprint, output t/day, jobs,
// jobs character, utility draw, exports, capex, opex, wages, supply-chain
// counts, and the bid curve (base quality, education-term rate, competing
// floor, jitter range, graduate-demand rate) — is a placeholder read from
// data/pharmacampus.json at load time. None of it is a Go numeric literal;
// a balance pass edits the data file, never this package. Each numeric entry
// carries a non-empty disclosure field naming it pending Aaron's balance
// pass (the data/market.json convention).
//
// # The manufacture-side split (ASM-488 / ASM-696)
//
// This feature owns the pharma campus as an FDI anchor facility and the
// education↔FDI compounding loop. It does NOT build the pharma
// manufacture-side ChemAPI chain stage — that is feat.commoditymarket's
// (FEAT-052) AC-5, per ASM-488/ASM-696. A developer building FEAT-052 must
// not create a second anchor-firm registration, just as this feature must
// not create a second pharma-output stage.
//
// # Spec refs
//
//   - §46 Multinational Attraction & Anchor Employers (the pharma archetype
//     row: "university, graduates, clean utilities | labs: high-wage white
//     collar, low freight"; the M7+ prospect/competitive-bid/anchor mechanic).
//   - §45 (the supply-chain-firm demand injection the won campus triggers).
//   - §27 (the education lifecycle — the "longest fuse" this loop compounds).
//   - §32 (the coal-mine-scale closure shock the anchor's §32 dependency
//     risk reaches through FirmsAPI).
//   - resources-design-brief.md §6 (the Groton-Pfizer-class pharma/R&D campus
//     archetype — Pfizer's real UK campus was Sandwich, Kent — "a lovely
//     FDI-anchor tie-in").
//
// # Cross-references (not restated)
//
//   - engine.fdi.md (MOD-059) — the prospect/bid/incentive/anchor-registration
//     machinery this feature supplies its education term into (AC-3) and reads
//     the won-anchor outcome from (AC-5/AC-6); its bid-resolution stub is
//     anchor.go here, not a reimplementation.
//   - engine.education.md (MOD-041) — the graduates/research-points output
//     this feature consumes (AC-3) and the graduate/research demand it emits
//     (AC-4); the exact term/demand shape is ASM-698.
//   - feat.facilitypermits.md (FEAT-053) — the permit gate this facility
//     inherits (AC-7), not reimplemented here.
//   - feat.decommission.md (FEAT-054) — the day-one "put back to nature"
//     liability this facility inherits (AC-7), not reimplemented here.
package fdi
