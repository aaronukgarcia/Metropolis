// Package census implements F6, the Demographics/Census screen (FEAT-209):
// three independent population splines (age band, sex, highest education
// tier) rendered as §13-F6's ASCII population pyramid + companion charts,
// the §45 blue/white-collar workforce split, the eight city-KPI tile row
// with Enter-to-source drill-in (UI-SPEC §4), citizen cradle-to-grave bio
// drill-in, and the education→crime linkage report. All of it is sourced
// entirely from an int.protocol view subscription against harness.stub
// (pre-FEAT-208) and, unchanged, a real engine later (the same SF-4
// pattern ui.screen.finance/ui.screen.services already establish).
//
// Module key: ui.screen.census (see code.json)
// Spec refs:  §13-F6 (docs/METROPOLIS-MASTER-v2.1.md line 253); §45 Firms
//
//	(line 625, the blue/white-collar F6 workforce view); §46
//	Multinational Attraction/FDI (lines 627-640, the specialist-
//	university-to-industry precedent a citizen bio drill-in surfaces);
//	UI-SPEC §4 (lines 760-765, the drill-through rule); UI-SPEC §5
//	(lines 767-777, the perf budget table); docs/planning/acceptance/
//	ui.screen.census.md (this package's own criteria, AC-1..AC-16 +
//	BLOCKED-1..BLOCKED-3); docs/planning/acceptance/feat.citycensus.md
//	(FEAT-133, the engine-side ACs this file mirrors 1:1 into a render
//	surface: AC-17 blue/white-collar, AC-18 splines, AC-19 KPIs, AC-20
//	drill-in).
//
// # View subscription (AC-2 field traceability)
//
// This screen subscribes to exactly one view: "f6.census" (wire.go), the
// aggregate feed engine.census's view fields push. Every displayed
// figure's exact source field:
//
//	Age-band spline (AC-3)             <- f6.census.ageBands (CensusAPI.AgeBandSeries)
//	Sex spline (AC-3)                  <- f6.census.sexSeries (CensusAPI.SexSeries)
//	Education-tier spline (AC-3)       <- f6.census.educationTiers (CensusAPI.EducationTierSeries)
//	Blue/white-collar graph (AC-4)     <- f6.census.blueWhiteCollar.blue/.white (CensusAPI.BlueWhiteCollar)
//	City KPI tiles (AC-5)              <- f6.census.kpis[].key/value (CensusAPI's eight KPIKey* accessors)
//	KPI drill-in source (AC-6)         <- f6.census.kpiSources[].key/entityIds/lineValue/unavailable/reason (CensusAPI.Source)
//	Citizen bio (AC-7)                 <- f6.census.selectedBio (CensusAPI.CitizenBio)
//	Education->crime linkage (AC-8)    <- f6.census.educationCrimeLinkage (CensusAPI.EducationCrimeLinkage)
//
// # AC-1: protocol-only consumption
//
// This package never imports internal/engine (GR#20 depguard-enforced,
// .golangci.yml's ui-must-not-import-engine rule) — every field above
// arrives as protocol.Delta.Patch's raw JSON, decoded against this
// package's own wire-schema copy (wire.go), never an internal/engine
// type. This screen consumes the registered ui.screen.census -> engine.census
// code.json outbound edge exclusively through int.protocol; the graph
// edge exists (GR#25), the code still crosses it only via a view
// subscription, matching every other F-screen.
//
// # AC-2: the "stub cannot fake" differential check
//
// sf_test.go drives two wire patches differing in exactly one figure and
// asserts (a) that figure's rendered pane differs and (b) an untouched
// pane's rendered output is byte-identical between the two runs — this
// package's own instance of the shared SF-3-style differential shape,
// repeated per figure (AC-3's independence pair, AC-4, AC-5).
//
// # AC-6/AC-7: KPI drill-in and citizen bio, chained
//
// Enter on a KPI tile (AC-5) resolves via Screen.KPISource(key), sourced
// from f6.census's kpiSources field — the entity IDs for a population-
// derived KPI (homeless/out-of-work) or the ledger LineValue for an
// aggregate KPI. Drilling from a KPI's entity list into one citizen
// (AC-7) opens Screen.SelectedBio(), sourced from f6.census's selectedBio
// field. Both selections also have an outbound seam
// (Screen.SelectKPI/Screen.SelectCitizen) riding a protocol.DebugPayload
// command with a fixed Op string ("census.select-kpi"/
// "census.select-citizen" — the ui.screen.services/ui.screen.trade
// ASM-1193 precedent: int.protocol's sealed v1 command vocabulary has no
// dedicated Kind for either and this screen may not edit
// internal/protocol); the resolved data itself still arrives back through
// the next f6.census patch, not the command's own return value (mirrors
// services.Screen.SetFunding's fire-and-await-patch shape).
//
// # AC-9: KPI tiles and bio facets registered into ui.dash
//
// DrillTargets (render.go) returns one dash.DrillTarget per KPI tile
// (AC-5) and one per citizen-bio facet (AC-7) — the canonical
// (ViewName, EntityID) shape ui.screen.services'/ui.screen.build's own
// DrillTargets already establish — for a caller with ui.dash's (MOD-038)
// registration API to register. This package implements no navigation,
// dead-end detection, or graph storage itself.
//
// # AC-10: population pyramid — reused widgets, and an honesty note (GR#3)
//
// RenderAgeBandPyramid (render.go) composes the pyramid from
// widgets.Heatmap (an existing ui.widgets primitive) — no new widget type
// is invented for §13-F6's "ASCII population pyramid" phrase.
// engine.census exposes AgeBandSeries and SexSeries as two INDEPENDENT
// arrays (internal/engine/census/stats.go) — there is no joint age×sex
// table anywhere in engine.census today, so the pyramid's two mirrored
// sides both show the SAME per-band population count; it is not a
// per-band male/female split (that data does not exist — inventing one
// would violate GR#3). RenderSexSeries renders the sex totals separately,
// sourced independently (AC-3's independence guarantee holds: mutating
// only the education-tier fixture leaves both the pyramid and the sex
// series byte-identical).
//
// # AC-11/AC-12: error handling
//
// ApplyDelta decodes via decodeWirePatch (wire.go): malformed JSON, an
// unrecognised schemaVersion, or an oversized payload is logged via
// MET-V700 (GR#7) and dropped — the screen's data keeps its last-known-
// good state, never applied, never panics. A Delta for an unbound
// (unknown/stale) SubscriptionID is logged via MET-V701 and dropped. A
// KPI/bio query the engine rejects (mirroring engine.census's
// MET-G2701/MET-G2702) arrives on the wire with Unavailable=true and is
// logged via MET-V703 (KPI)/MET-V704 (bio) and rendered as an explicit
// "unavailable" pane (RenderKPISource/RenderCitizenBio), never a
// silently-rendered zero the player could mistake for a real empty
// figure (mirrors engine.census.md AC-21's "no zero-value bio/KPI"
// guarantee, carried through to the render layer).
//
// # AC-13: copy-guard sweep
//
// Every exported *Screen method that reads or writes a receiver field is
// checkNotCopied-guarded (copyguard.go), mirroring
// internal/ui/core/copyguard.go, internal/protocol/subscription.go, and
// ui.screen.services'/ui.screen.finance's identical precedent — a method
// called on a struct-copied value returns MET-V702 rather than operating
// on an independently-zeroed, aliasing copy.
//
// # AC-14/AC-15: determinism and race safety
//
// Every render function in this package is a pure function of its
// arguments — no wall-clock call anywhere in this package's non-test
// source (determinism_test.go mechanically greps for the literal call).
// Every Screen method locks mu, so ApplyDelta (delta-applying goroutine)
// and the accessor/Render*/command calls (render/input goroutine) may run
// concurrently; `go test ./internal/ui/screens/census/... -race -count=2`
// is part of this package's own verification.
//
// # Blocked ACs (engine gap — ASM-1161's no-live-source class)
//
// BLOCKED-1 (personality/leisure-taste): no field exists anywhere in
// engine.census (verified: grep -c "Personality\|LeisureTaste"
// internal/engine/census/*.go returns 0) — no personality/leisure-taste
// pane exists in this package. Tripwire: tripwire_test.go's
// TestTripwire_PersonalityLeisureTasteStillAbsent fails loudly the moment
// either field lands.
//
// BLOCKED-2 (finer sector/skill breakdown): only the 2-way blue/white
// split (AC-4) is exposed; no SectorSeries/SkillSeries accessor exists
// (verified: grep -c "func (c \*CensusAPI) SectorSeries\|func (c
// \*CensusAPI) SkillSeries" internal/engine/census/*.go returns 0) — this
// screen renders only the blue/white binary. Tripwire: tripwire_test.go's
// TestTripwire_SectorSkillSeriesStillAbsent fails loudly the moment either
// accessor lands.
//
// BLOCKED-3 (non-citizen bio completeness): inherited from
// feat.citycensus.md's own open escalation (ES-3) — the owning modules
// that would populate car/house/chopper GUIDs into the census's tracked
// set are outside this file's scope. No mechanical tripwire exists for
// this one (mirrors ui.screen.services' SVC-3: no stable, already-agreed
// detection point to probe — see tripwire_test.go's own note on why).
//
// # Cross-references
//
// docs/planning/acceptance/feat.citycensus.md's AC-17 (blue/white
// collar), AC-18 (splines), AC-19 (KPIs), AC-20 (drill-in) are the
// engine-side contract this screen renders rather than reinvents.
//
// Gating Notes & Architecture Seams (ASM-1482):
//   - SelectKPI/SelectCitizen's command-rejection surfacing
//     (ApplyResult/SelectionRejectedReason) is fully designed, implemented,
//     and verified in unit tests. Actual routing of command results to
//     screen sub-receivers is currently unwired in the wider frame pending
//     the core routing-seam implementation (ASM-1482) — do not invent
//     custom/ad-hoc wiring (mirrors internal/ui/screens/services/doc.go's
//     identical SVC-8 gating note).
package census
