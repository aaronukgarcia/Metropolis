// Package mining is the shared home of engine.mining (MOD-046) and
// feat.resourcedeposits (FEAT-049): subsurface resources, extraction
// siting, and the general blight model. This file documents the
// deposit-model half — the deterministic seeded deposit shuffle — which
// lives in this package as a file/type set alongside engine.mining's own
// eventual BlightAPI/siting surface, NOT as a fork or a second package.
//
// # Shared-package, no-separate-inbound-contract arrangement (GR#20)
//
// code.json's feat.resourcedeposits entry (path internal/engine/mining/)
// has NO inbound contract of its own (its inbound name/format/pattern are
// all null) because it shares engine.mining's package and is expected to
// surface through that module's eventual MiningAPI. It DOES register one
// outbound call — engine.world — which is the only module this deposit
// code consumes: every geology/surface read below goes through
// *world.WorldAPI's exported methods (PocketGeology/GeologyBaseline/CellAt/
// TileAt/Prospect), never through world-internal state, and there is no
// mining-local copy of the terrain/geology model (GR#3). The deposit
// query surface (DepositAt) is a plain read over the DepositMap this
// package's Shuffle produces; it is deliberately NOT the geology-gated
// reveal mechanism engine.mining.md's AC-1/AC-2 specifies for siting —
// that gate belongs to engine.mining's own siting/prospecting surface,
// and FEAT-050's survey progression will be the one place that asks
// "what's under this cell" once it exists.
//
// # North-star vetting (feat.resourcedeposits.md, "North-star vetting")
//
// This feature answers none of the five north-star questions on its own:
// it spends no capex, feels no consequence, and snowballs nothing. It is
// necessary infrastructure that lays out what exists in the ground as a
// fixed, never-reshuffled, pre-purchase fact — which is what makes the
// long-bet (Q5) FEAT-050 (survey: "pay to see underground") and the
// consequence-sticking (Q3) and conflicting-demand (Q1) questions of
// FEAT-051 (extraction) real rather than a slot-machine pull. The map is
// deterministic by construction: same worldSeed => bit-identical deposit
// records, forever.
//
// # The GR#15 data file
//
// Every tunable number this shuffle uses — per-type deposit counts (as
// selection weights), size- and density-curve shape parameters, per-type
// depth-band ranges, the chalk/uranium exclusion and coal/gas correlation
// strengths, and the East Kent coalfield "don't be stingy" generosity
// multiplier plus its coverage floor — is sourced from data/deposits.json
// at load time. None of it is a Go numeric literal (GR#15): a balance
// pass edits the data file, never this package.
//
// # Spec refs
//
//   - resources-design-brief.md §2-3 (the resource set, co-location, and
//     the seeded shuffle).
//   - engine.mining.md (MOD-046) — cross-referenced, not restated: this
//     file's deposits are inert data until engine.mining's siting/
//     BlightAPI machinery consumes them; no siting/extraction/blight AC
//     belongs here.
//   - engine.world.md (MOD-017) — the WorldAPI this shuffle consumes.
package mining
