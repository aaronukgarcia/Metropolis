// Package proj implements F7, the Projections screen: every demand/
// supply curve N years forward — history solid Braille, projection dim
// Braille, confidence bands as dim dots, threshold lines, and queued-
// decision step markers (UI-SPEC §4's projection-pane idiom) — plus the
// §36 contracted-vs-internal demand crossings, the §45 rate outlook, and
// the A5 Slow-Fuse confirmation render target. All of it is sourced
// entirely from an int.protocol view subscription against harness.stub
// (Sprint 8) and, unchanged, a real engine later (SF-4).
//
// Module key: ui.screen.proj (see code.json)
// Spec refs:  §13-F7 (docs/METROPOLIS-MASTER-v2.1.md line 254); A5 Slow-
//
//	Fuse Principle (line 1367); §36 Service Capacity Export (line 548);
//	§45 Firms / rate outlook (line 623); UI-SPEC §4 projection-pane idiom
//	(line 763); docs/planning/acceptance/ui.screen.proj.md (this package's
//	own criteria, PRJ-1..PRJ-6) and ui.screen.finance.md (the Shared
//	F-Screen Contract, SF-1..SF-10, inherited here — see ui.screen.proj.md's
//	"Shared contract" section).
//
// # View subscription (SF-2 field traceability)
//
// This screen subscribes to exactly one view: "f7.projections"
// (wire.go), the aggregate feed engine.projections (MOD-031) will push.
// It carries every figure this screen renders in one patch, because the
// curves all share one horizon and one value model. Every displayed
// figure's exact source field:
//
//	Per-curve history/projection chart (PRJ-1)   <- f7.projections.curves[].history/projection
//	Per-curve confidence bands (PRJ-1)          <- f7.projections.curves[].confidenceUpper/confidenceLower
//	Per-curve threshold lines (PRJ-1)           <- f7.projections.curves[].thresholds[].value/label
//	Per-curve decision markers (PRJ-1)          <- f7.projections.curves[].markers[].monthOffset/label
//	Contract crossing chart (PRJ-3)             <- f7.projections.crossings[].internalDemand/contractedCapacity
//	Rate-outlook curve (PRJ-4)                  <- f7.projections.rateOutlook.history/projection
//	Forecast horizon N (PRJ-2)                  <- f7.projections.horizonMonths
//
// # SF-1: protocol-only consumption
//
// This package never imports internal/engine (GR#20 depguard-enforced,
// .golangci.yml's ui-must-not-import-engine rule) — every field above
// arrives as protocol.Delta.Patch's raw JSON, decoded against this
// package's own wire-schema copy (wire.go), never an internal/engine
// type. `go list -deps ./internal/ui/screens/proj/...` shows no
// internal/engine import (test files are exempt per the depguard config,
// and this package's tests build fixtures from its own wire structs, so
// none import it anyway).
//
// # PRJ-2: horizon N is data-sourced, seasonality is the engine's job
//
// The forecast horizon N is read from the view's horizonMonths field
// (HorizonMonths()), never a hardcoded Go literal — the starting value
// is not spec-fixed (ASM-253) and §13-F7 only says "N grows with unlocked
// forecasting", which is engine.projections'/engine.unlocks' concern, not
// this screen's. Seasonality ("all seasonally aware", §13-F7) is a
// property of the projected series engine.projections computes; this
// screen renders every month the view hands it without downsampling,
// averaging, or linearising it — the seasonal month-to-month structure is
// preserved byte-for-byte in the Braille chart (render.go's RenderCurve).
//
// # PRJ-5 (A5, cross-screen reuse): RenderSlowFuse
//
// RenderSlowFuse (render.go) is this screen's exported projection-
// rendering primitive. Any decision-UI confirmation flow elsewhere in
// the game whose principal effect lands more than 5 game-years (>60
// months, ASM-239) out calls it to render the consequence curve inline in
// its confirmation step — a projection curve, not a bare number. The
// Slow-Fuse *gate* that forces the call (engine.projections' AC-5) is not
// this package's; RenderSlowFuse renders whatever Consequence it is
// given, exactly as the projection-pane idiom renders a curve, so the
// rendering is byte-compatible with the main F7 screen.
//
// # SF-3: the "stub cannot fake" differential check
//
// sf3_test.go drives two wire patches differing in exactly one curve's
// projection value and asserts (a) that curve's rendered chart differs
// and (b) a second, untouched curve's rendered chart is byte-identical
// between the two runs — this package's own instance of the shared SF-3
// shape, so a screen hardcoding a value, computing independently of the
// subscribed view, or wiring the wrong field fails it.
//
// # SF-5: drill-through, consumed not reimplemented
//
// DrillTargets (render.go) produces the drill-through source identities —
// one canonical dash.DrillTarget (ViewName, EntityID) per curve, per
// crossing, and for the rate-outlook figure — for a caller with ui.dash's
// (MOD-038) registration API to register. This package implements no
// navigation, dead-end detection, or graph storage itself (MOD-038's job).
// It consumes dash.DrillTarget directly (GR#3: no bespoke parallel type),
// with ViewName ViewSubscriptionName ("f7.projections" — the one view this
// screen subscribes to, so every target is resolvable) and EntityID the
// sub-entity path within that view ("curve.<key>", "crossing.<key>",
// "rate"), mirroring ui.screen.ticker's already-correct implementation.
//
// # SF-6: alert-jump landing anchor
//
// This screen is not currently a documented landing target for any
// bottom-alert-stack category: §13's alert examples ("Water deficit in 3
// months", "School capacity exceeded next September") are projection-
// flavoured but name no jump destination, and no reviewed spec section
// assigns them to F7. No jump-anchor is exposed; if a future alert
// category needs to land here, add a named anchor at that time (a curve's
// key is the natural anchor) rather than speculatively wiring one now —
// the same posture ui.screen.demo documented for SF-6.
//
// # SF-7/PRJ-6: error handling
//
// ApplyDelta decodes via decodeWirePatch (wire.go): malformed JSON, an
// unrecognised schemaVersion, or an oversized payload is logged via
// MET-V001 (GR#7) and dropped — the screen's data keeps its last-known-
// good state, never panics, never partially applies. A Delta for an
// unbound (unknown/stale) SubscriptionID is logged via MET-V002 and
// dropped. A curve/crossing/rate whose status is not "available" renders
// an explicit "unavailable: <reason>" or "not yet unlocked" line
// (RenderCurve/RenderCrossing/RenderRateOutlook), never a blank or a
// fabricated flat line (PRJ-6).
//
// # SF-8/SF-9: determinism and race safety
//
// Every render function in this package is a pure function of its
// arguments — no wall-clock call anywhere in this package's non-test
// source (determinism_test.go mechanically greps for the literal call,
// mirroring ui.screen.demo's TestNoWallClockUsage). Screen's every
// exported method locks mu, so ApplyDelta (delta-applying goroutine) and
// the accessor/Render* calls (render goroutine) may run concurrently;
// `go test ./internal/ui/screens/proj/... -race -count=1` is part of this
// package's own verification (race_test.go). Confidence-band, threshold,
// and marker placement are pure functions of the view-model — no
// independently-sampled randomness anywhere in the render path (PRJ-1's
// determinism clause).
//
// # Scope actually built vs. out of scope
//
// This dispatch builds PRJ-1..PRJ-6 and SF-1..SF-10. Out of scope (per
// ui.screen.proj.md): projection *computation* (engine.projections);
// the generic confirmation-dialog chrome PRJ-5 plugs into (this package
// only owns the rendering call, not the dialog); service-capacity export
// contract creation/management (ui.screen.trade / ui.screen.services).
// The two BUG-058-candidate missing registry edges named in the criteria
// (engine.capexport for PRJ-3, engine.finance/fiscal for PRJ-4) do not
// block this screen: it consumes the aggregated "f7.projections" view,
// so those producers' data reaches it through engine.projections' single
// registered outbound edge — see the Escalations note in
// ui.screen.proj.md and the ASM logged this dispatch for the rate-outlook
// drill target.
package proj
