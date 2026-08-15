// Package ticker implements F9, the Ticker & History screen: a rolling
// ticker of atomic events, a monthly bulletin front page (read-on-pause),
// an annual review, and a searchable full-history archive that doubles as
// the epilogue's data source — all sourced entirely from int.protocol
// view subscriptions against engine.news (§29) and, unchanged, a real
// engine later (SF-4).
//
// Module key: ui.screen.ticker (see code.json)
// Spec refs:  §13-F9 (docs/METROPOLIS-MASTER-v2.1.md line 256); §29 The
// News System (lines 426-433); §20 Roads & Auto-Naming (lines 360-372,
// cited by §29 for real names); UI-SPEC §2 (the "ticker scroll" motion,
// line 742); UI-SPEC §3 (the "/" name search + n/N stepping, line 753);
// int.protocol (INT-001); ui.keys (MOD-011, reused per ASM-254). Own
// acceptance criteria: docs/planning/acceptance/ui.screen.ticker.md
// (TIK-1..TIK-8) and ui.screen.finance.md (the Shared F-Screen Contract,
// SF-1..SF-10, inherited here).
//
// # View subscriptions (SF-2 field traceability)
//
// This screen subscribes to four views (wire.go), one per §29 news layer,
// each a small feed rather than one large combined view:
//
//	f9.ticker   (engine.news) -- rolling atomic events   (§29.1, TIK-1)
//	f9.bulletin (engine.news) -- monthly front page      (§29.2, TIK-2)
//	f9.annual   (engine.news) -- year in numbers + biggest story (§29.3, TIK-3)
//	f9.archive  (engine.news) -- searchable history / epilogue source (§29.4, TIK-4/TIK-6)
//
// Every displayed figure's exact source field (SF-2's binding
// requirement):
//
//	Ticker headline (TIK-1)            <- f9.ticker.events[].name/text (eventId is the drill-through reference)
//	Bulletin story row (TIK-2)         <- f9.bulletin.stories[].name/text (salience/rank carry the editor's order)
//	Annual "year in numbers" (TIK-3)   <- f9.annual.numbers[].label/value
//	Annual biggest story (TIK-3)       <- f9.annual.biggestStory.name/text
//	Archive entry (TIK-4)              <- f9.archive.stories[].name/text
//
// # SF-1: protocol-only consumption
//
// This package never imports internal/engine (GR#20 depguard-enforced,
// .golangci.yml's ui-must-not-import-engine rule) — every field above
// arrives as protocol.Delta.Patch's raw JSON, decoded against this
// package's own wire-schema copy (wire.go), never an internal/engine
// type. `go list -deps ./internal/ui/screens/ticker/...` shows no
// internal/engine import (test files are exempt per the depguard config's
// own comment, mirroring ui.screen.map/ui.screen.demo's convention).
//
// # SF-3: the "stub cannot fake" differential check
//
// harness.stub serves no f9.* news view (its scriptFor returns a generic
// "no scripted content" patch for any view that isn't f1.viewport), and
// engine.news (MOD-043) is unbuilt at dispatch time, so there is no
// engine-side fixture to drive. The SF-3 differential shape is therefore
// exercised the same way ui.screen.demo exercises it: each figure's test
// drives two delta sequences differing in exactly that figure's field and
// asserts (a) the bound render output changes correspondingly and (b)
// every OTHER figure's render output is byte-identical between the two
// runs (render_test.go's TestRender_SF3_SingleFieldMutation). See the
// ASM logged for this dispatch for the "no stub view to drive" note.
//
// # SF-5/TIK-5: drill-through, consumed not reimplemented
//
// TIK-5 restates the drill-through rule (SF-5) for news content as a
// structural property of the data model: every rendered story carries its
// originating engine.news event ID (Story.EventID), and a story arriving
// without one is rejected at the patch boundary (MET-U703), never
// rendered — so "no hallucinated news" is enforced, not promised.
// DrillTargets (render.go) additionally produces the source identities —
// one canonical dash.DrillTarget (ViewName, EntityID) per rendered story
// — for a caller with ui.dash's (MOD-038) registration API to attach to
// tiles; this package implements no navigation, dead-end detection, or
// graph storage (MOD-038's job). It consumes dash.DrillTarget directly
// (GR#3: no bespoke parallel type), with ViewName drillViewNewsEvent
// ("news.event") and EntityID the story's event ID; the name is
// forward-looking and reconciled against MOD-043's landed engine.news
// view names when that module ships.
//
// # SF-6: alert-jump landing anchor
//
// This screen is not currently a documented landing target for any
// bottom-alert-stack category (no §13 alert text names F9 as its jump
// destination in the reviewed spec sections) — no jump-anchor is exposed.
// If a future alert category needs to land here, add a named anchor at
// that time rather than speculatively wiring one now.
//
// # SF-7/TIK-7/TIK-8: error handling
//
// Every applyX method (screen.go) decodes via decodeWirePatch (wire.go):
// malformed JSON, an unrecognised schemaVersion, or an oversized payload
// is logged via MET-U700 (GR#7) and dropped — the affected view's data
// keeps its last-known-good state, never panics, never partially applies.
// The one exception is f9.archive: because it carries the city's whole
// atomic-event history (spec 29.4), an oversized archive patch is not a
// transient malformed drop but a permanent freeze of the last-known-good
// archive, so applyArchive logs MET-U705 and sets a distinct
// archiveStalled state (surfaced by ArchiveStalled() and a render banner)
// instead of freezing silently (SEC-072/GR#17).
// ApplyDelta additionally validates the delta's SubscriptionID against
// this screen's BindSubscription bookkeeping: a Delta for an unbound
// (unknown/stale) SubscriptionID is logged via MET-U701 and dropped
// (screen.go). TIK-7: before the first f9.ticker/f9.bulletin/f9.annual
// patch arrives, the render path shows an explicit "no news feed" state
// rather than an empty scroll that looks broken. TIK-8: the archive pane
// distinguishes "still loading" (no f9.archive patch yet) from "no
// matches" (a search ran and matched zero) — see render.go's RenderArchive.
//
// # SF-8/SF-9: determinism and race safety
//
// Every render function is a pure function of its arguments, and the
// ticker scroll is a deterministic function of (scroll step, event count)
// (scroll.go) — no wall-clock call anywhere in this package's non-test
// source (determinism_test.go mechanically greps for the literal call,
// mirroring ui.screen.demo's TestNoWallClockUsage). Screen's every
// exported method locks mu, so ApplyDelta (delta-applying goroutine) and
// the accessor/render/search calls (render goroutine) may run
// concurrently; `go test ./internal/ui/screens/ticker/... -race -count=1`
// is part of this package's own verification (race_test.go).
//
// # Scope (this dispatch, FEAT-020)
//
// News generation and salience scoring are engine.news's (MOD-043) job —
// this screen only renders and searches that module's output (see
// ui.screen.ticker.md's "Out of scope"). The engine.news module and the
// f9.* wire schemas it will emit are unbuilt at dispatch time; this
// package therefore defines the f9.* view schemas it consumes (wire.go),
// to be reconciled against engine.news's landed output via a drift test
// when that module ships (see the ASM logged for this dispatch). The
// optional LLM soft-layer (§29) is out of v1 scope — this screen renders
// whatever prose engine.news supplies, it never calls an LLM.
package ticker
