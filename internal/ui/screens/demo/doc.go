// Package demo implements F6, the Demographics screen: a month-age
// population pyramid, personality/leisure-taste distributions, the §42
// "how your city spends Saturday" hours-by-activity view, per-typology
// housing demand-vs-stock, and the §21 in/out-commuting leak view — all
// sourced entirely from int.protocol view subscriptions against
// harness.stub (Sprint 8) and, unchanged, a real engine later (SF-4).
//
// Module key: ui.screen.demo (see code.json)
// Spec refs:  §13-F6 (docs/METROPOLIS-MASTER-v2.1.md line 251); §42
//
//	Leisure Time & Exploration (line 573); §21 external commuting /
//	dormitory strategy (line 1297); docs/planning/acceptance/
//	ui.screen.demo.md (this package's own criteria, DEMO-1..DEMO-10)
//	and ui.screen.finance.md (the Shared F-Screen Contract, SF-1..SF-10,
//	inherited here — see DEMO-*.md's "Shared contract" section).
//
// # Scope actually built (DEMO-2/DEMO-3 blocked — see below)
//
// This dispatch builds DEMO-1, DEMO-4, DEMO-5, DEMO-6, DEMO-7, DEMO-8,
// DEMO-9, DEMO-10, and SF-1..SF-10. It does NOT build DEMO-2 (§13-F6
// education-pipeline summary) or DEMO-3 (§13-F6 workforce-by-sector/
// skill vs demand): code.json's ui.screen.demo outbound edges list only
// engine.citizens, engine.households, engine.leisure, and
// engine.extcommute — there is no engine.education or engine.firms (or
// equivalent labour-demand) edge registered, and DEMO-2/DEMO-3's own AC
// text explicitly marks them blocked pending that registry gap (see
// ui.screen.demo.md's Escalations section, the "BUG-058-class
// candidate" entry). This package therefore carries NO
// education-pipeline or workforce-by-sector code at all — not a stub
// function, not a TODO widget, nothing to accidentally half-satisfy the
// AC and paper over the gap. When engine.education/engine.firms land
// and the outbound edges are registered, DEMO-2/DEMO-3 are new,
// separate dispatch work against this same package.
//
// # View subscriptions (SF-2 field traceability)
//
// This screen subscribes to four views (wire.go), each a small
// aggregate-figure feed rather than one large combined view, so a
// screen-code change to one domain's rendering never needs to touch
// another's wire schema:
//
//	f6.population (engine.citizens) -- month-age pyramid + personality distribution
//	f6.leisure    (engine.leisure)  -- Saturday hours-by-activity + leisure-taste distribution
//	f6.housing    (engine.households) -- per-typology demand-vs-stock
//	f6.commute    (engine.extcommute) -- in/out commuting-leak figures
//
// Every displayed figure's exact source field (SF-2's binding
// requirement):
//
//	Population pyramid bars (DEMO-1)     <- f6.population.ageMonths[].male/female
//	Personality distribution (DEMO-7)    <- f6.population.personality[].trait/count
//	Saturday hours-by-activity (DEMO-4)  <- f6.leisure.hoursByActivity[].activity/hours
//	Leisure-taste distribution (DEMO-7)  <- f6.leisure.leisureTaste[].taste/weight
//	Typology demand-vs-stock (DEMO-5)    <- f6.housing.typologies[].typology/demand/stock
//	Out-commuting figure (DEMO-6)        <- f6.commute.outCommuters
//	In-commuting figure (DEMO-6)         <- f6.commute.inCommuters
//
// # SF-1: protocol-only consumption
//
// This package never imports internal/engine (GR#20 depguard-enforced,
// .golangci.yml's ui-must-not-import-engine rule) — every view field
// above arrives as protocol.Delta.Patch's raw JSON, decoded against this
// package's own wire-schema copy (wire.go), never an internal/engine
// type. `go list -deps ./internal/ui/screens/demo/...` shows no
// internal/engine import (test files are exempt per the depguard
// config's own comment, mirroring ui.screen.map's convention).
//
// # SF-3: the "stub cannot fake" differential check
//
// Every _test.go file exercising a DEMO-*/SF-* AC that names a figure
// above drives two fixtures/delta sequences differing in exactly that
// figure's field and asserts (a) the bound render output changes
// correspondingly and (b), where the figure is one of several rendered
// side by side, every OTHER figure's render output is byte-identical
// between the two runs — see each figure-specific test file for its own
// per-DEMO instance of this shape: pyramid_test.go's
// TestComputePyramidBars_TracesToFixture (DEMO-1), typology_test.go's
// TestTypologies_SF3_OneTypologyChanges (DEMO-5, full (a)+(b) shape),
// commute_test.go's TestCommute_SF3_OneDirectionChanges (DEMO-6),
// hours_test.go's TestHoursByActivity_SaturdayHoursByActivity (DEMO-4),
// and personality_test.go's TestRenderPersonality_TracesToFixture /
// TestRenderLeisureTaste_TracesToFixture (DEMO-7).
//
// # No dedicated ui.widgets Pyramid primitive (ASM, flagged for Bill)
//
// ui.screen.demo.md's Spec refs line describes MOD-010's Braille pyramid
// primitive as "already built for this exact screen's use." As of this
// dispatch, internal/ui/widgets contains no Pyramid function — only the
// general-purpose BrailleCanvas/BrailleChart primitives (braille.go),
// which are a line-chart, not a population-pyramid, layout. This
// package satisfies DEMO-1's actual check (widgets.Pyramid|Braille
// grep, reuse of the dot-addressable primitive rather than a
// screen-local reimplementation of Braille dot-plane math) by composing
// widgets.NewBrailleCanvas/SetDot/Mask/Rune directly (pyramid.go) — the
// hard part (2x4 sub-cell dot addressing) is genuinely reused from
// ui.widgets; only the pyramid-specific row/bar layout (ComputePyramidBars,
// pyramid.go) is written here, because there is nothing more specific to
// reuse. Logged as an ASM for Bill to confirm whether MOD-010 owes a
// dedicated Pyramid entry point (in which case this package's
// pyramid-layout code becomes migration debt) or whether this
// BrailleCanvas-composition shape IS what the acceptance file's line
// meant by "primitive."
//
// # SF-5/DEMO-8: drill-through, consumed not reimplemented
//
// DrillTargets (render.go) produces the screen's (widget, source) pairs
// -- the pyramid total, one entry per non-retired housing typology, and
// the two distinct commuting-leak figures -- for a caller with the real
// ui.dash (MOD-038) registration API to register. This package
// implements no navigation, dead-end detection, or graph storage itself
// (MOD-038's job); MOD-038 was open at dispatch time per the acceptance
// file's own Escalations note, so DrillTargets' pair list is the
// integration seam a later item wires against MOD-038's landed API.
// DEMO-3's workforce totals are explicitly absent from this list (DEMO-3
// itself is blocked, see above).
//
// # SF-6: alert-jump landing anchor
//
// This screen is not currently a documented landing target for any
// bottom-alert-stack category (no §13 alert text names F6 as its jump
// destination in the reviewed spec sections) -- no jump-anchor is
// exposed. If a future alert category needs to land here, add a named
// anchor at that time rather than speculatively wiring one now.
//
// # SF-7/DEMO-9: error handling
//
// Every ApplyX method (screen.go) decodes via decodeWirePatch
// (wire.go): malformed JSON, an unrecognised schemaVersion, or an
// oversized payload is logged via MET-U500 (GR#7) and dropped -- the
// affected view's data keeps its last-known-good state, never
// panics, never partially applies. ApplyDelta additionally validates
// the delta's SubscriptionID against this screen's own
// BindSubscription bookkeeping: a Delta for an unbound (unknown/stale)
// SubscriptionID is logged via MET-U501 and dropped (screen.go). A
// housing typology retired mid-game (absent from a subsequent full
// f6.housing snapshot) renders "no longer available" (render.go's
// RenderTypologies) rather than its last stale demand/stock numbers.
//
// # SF-8/SF-9: determinism and race safety
//
// Every render function in this package is a pure function of its
// arguments -- no wall-clock call anywhere in this package's non-test
// source (determinism_test.go mechanically greps for the literal call
// this sentence deliberately does not spell out, mirroring
// feat.devmode's TestNoWallClockUsage). Screen's every
// exported method locks mu, so ApplyDelta (delta-applying goroutine)
// and the accessor/Render* calls (render goroutine) may run
// concurrently; `go test ./internal/ui/screens/demo/... -race -count=1`
// is part of this package's own verification (see race_test.go).
package demo
