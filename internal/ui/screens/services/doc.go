// Package services implements F4, the Services screen (FEAT-016):
// per-service funding sliders, capacity-vs-demand, response-time
// distributions and waiting lists (§26's unified dispatch model), a
// coverage-map jump to F1, and the §54 Public Service Pie allocation
// view. All of it is sourced entirely from an int.protocol view
// subscription against harness.stub (Sprint 8) and, unchanged, a real
// engine later (SF-4).
//
// Module key: ui.screen.services (see code.json)
// Spec refs:  §13-F4 (docs/METROPOLIS-MASTER-v2.1.md line 251); §26
//
//	Emergency & Care Dispatch Model (lines 410-412); §54 The Fiscal
//	Circuit — Public Service Pie (line 684);
//	docs/planning/acceptance/ui.screen.services.md (this package's own
//	criteria, SVC-1..SVC-8) and ui.screen.finance.md (the Shared
//	F-Screen Contract, SF-1..SF-10, inherited here).
//
// # View subscription (SF-2 field traceability)
//
// This screen subscribes to exactly one view: "f4.services" (wire.go),
// the aggregate feed engine.services'/engine.dispatch's view fields will
// push. It carries every figure this screen renders in one patch. Every
// displayed figure's exact source field:
//
//	Funding slider (SVC-1)               <- f4.services.sliders[].id/label/value/min/max/step
//	                                        (UI display domain only — the
//	                                        outbound services.set-funding
//	                                        wire value is this rescaled into
//	                                        the engine's [0,1] funding-level
//	                                        domain, internal/engine/services/
//	                                        api.go:266-292's SetFunding
//	                                        contract; see SVC-1 below)
//	Capacity-vs-demand gauge (SVC-2)    <- f4.services.capacityDemand[].serviceId/label/capacityUnits/demandUnits
//	Coverage-map jump (SVC-3)           <- BLOCKED, see below (no rendered figure of its own; the jump rides the capacity/slider rows' own EntityID)
//	Response-time distribution (SVC-4)  <- f4.services.responseTimes[].serviceId/label/medianSeconds/p90Seconds/sampleCount
//	Waiting-list figure (SVC-5)         <- f4.services.waitingLists[].id/label/currentCount/trendHistory
//	Public Service Pie slice (SVC-6)    <- BLOCKED, see below (f4.services.publicServicePie.slices[] exists in the wire schema for forward compatibility only; nothing populates it today)
//
// # SF-1: protocol-only consumption
//
// This package never imports internal/engine (GR#20 depguard-enforced,
// .golangci.yml's ui-must-not-import-engine rule) — every field above
// arrives as protocol.Delta.Patch's raw JSON, decoded against this
// package's own wire-schema copy (wire.go), never an internal/engine
// type. sf1_test.go's TestNoEngineImport scans this package's non-test
// .go files for the literal internal/engine import path (test files are
// exempt, and this package's own tests build fixtures from its own wire
// structs, so none import it anyway).
//
// # SVC-1: per-service funding sliders
//
// RenderSliders draws the list; Screen.SetFunding issues the player
// action. Because int.protocol's sealed v1 command vocabulary has no
// SetFunding Kind and this screen may not edit internal/protocol, the
// action is carried as a protocol.DebugPayload command with a fixed Op
// string ("services.set-funding") — the ui.screen.trade/ui.screen.menu
// ASM-1193 precedent. Slider Min/Max/Step arrive on the wire from the
// engine rather than being spec-fixed constants here (ASM-250: no spec
// text mandates slider bounds); this range is the slider's UI DISPLAY
// domain only — the slider MAY still display 0-1000 or a percentage. The
// engine's own funding-level domain is [0,1]
// (internal/engine/services/api.go:266-292's ServicesAPI.SetFunding hard-
// rejects level<0 or level>1, never silently clamps — the codebase-wide
// funding-level convention). Screen.SetFunding rescales the raw
// display-domain value into that [0,1] fraction (normalizeFundingLevel,
// screen.go) before it is ever placed on the wire, and rejects locally
// with MET-V503 (never sends) when the rescaled level falls outside
// [0,1] — mirroring the engine's own rule exactly rather than inventing a
// looser, UI-domain-shaped one. TestSetFunding_RescalesValueToEngineDomain
// and TestSetFunding_RejectsAboveEngineDomain (screen_test.go) are this
// package's proof-of-failure pair for the rescale and the rejection.
//
// # SVC-3: coverage-map jump — BLOCKED (tripwire)
//
// SVC-3 asks for an Enter-on-a-service's-coverage-figure jump to F1's
// existing per-service coverage overlay. drill_map.go's
// CoverageJumpTarget(serviceID) produces the canonical dash.DrillTarget
// naming a real, registered view (ui.screen.map's "f1.viewport") — never
// a fabricated non-view, never a second coverage renderer built here
// (the AC's explicit instruction). It does NOT currently resolve:
// ui.screen.map's own AC-3 (the per-service coverage overlay, part of its
// documented "overlay cycle: ownership, land value, zoning, ...") was
// explicitly deferred out of scope at FEAT-005's Sprint 1 dispatch (see
// internal/ui/screens/map/doc.go's "Scope" section) and has not landed —
// ui.screen.map never marks a per-service coverage entity live, so this
// screen's jump target is presently a real-but-unresolvable destination
// through no fault of this dispatch. Flagged for Bill: SVC-3 cannot be
// closed until ui.screen.map's AC-3 lands. See drill_map.go/
// drill_map_test.go for the full note and the drift test proving the
// target name is real, not fabricated.
//
// # SVC-6: Public Service Pie — BLOCKED (tripwire, BUG-058 candidate)
//
// code.json's ui.screen.services outbound calls list only
// engine.services and engine.dispatch — engine.fiscal (§54's likely
// source for the Pie's per-1k-population benchmark ratios) is NOT a
// registered outbound edge for this screen (confirmed at dispatch by
// reading code.json directly; GR#25 forbids building against an
// unregistered dependency). SVC-6 therefore cannot be built against a
// named, SF-2-traceable field. wire.go/types.go define the
// PublicServicePie wire shape and Screen.PublicServicePie() accessor for
// forward compatibility only (mirrors ui.screen.trade's TRD-6
// wireSafety/RenderSafety pattern exactly); Screen.PublicServicePie()
// always reports have=false and RenderPublicServicePie always renders the
// honest "unavailable" state (SF-7) because nothing sends the wire field
// yet. Flagged for Bill per the acceptance file's own BUG-058 candidate
// note — not built against a phantom field.
//
// # SF-3: the "stub cannot fake" differential check
//
// sf3_test.go drives two wire patches differing in exactly one figure
// (a capacity-demand ratio) and asserts (a) that figure's rendered gauge
// differs and (b) an untouched figure's rendered output is byte-identical
// between the two runs — this package's own instance of the shared SF-3
// shape.
//
// # SF-5: drill-through, consumed not reimplemented
//
// DrillTargets (render.go) produces the drill-through source identities —
// one canonical dash.DrillTarget (ViewName, EntityID) per funding slider,
// capacity-demand figure, response-time figure, and waiting list — for a
// caller with ui.dash's (MOD-038) registration API to register. SVC-6's
// Pie slices are deliberately NOT registered (a slice this screen never
// actually has data for is not a drill source); SVC-3's coverage jump is
// registered separately via CoverageJumpTarget, declared BLOCKED above.
// This package implements no navigation, dead-end detection, or graph
// storage itself (MOD-038's job).
//
// # SF-7/SVC-7/SVC-8: error handling
//
// ApplyDelta decodes via decodeWirePatch (wire.go): malformed JSON, an
// unrecognised schemaVersion, or an oversized payload is logged via
// MET-V500 (GR#7) and dropped — the screen's data keeps its last-known-
// good state, never panics, never partially applies. A Delta for an
// unbound (unknown/stale) SubscriptionID is logged via MET-V501 and
// dropped. Any sub-surface absent from the latest patch renders an
// explicit "unavailable" line (Render* functions), never blank and never
// stale (SVC-7): the previous data for that sub-surface is cleared on
// apply. A funding-slider change the engine rejects (for a level that IS
// inside [0,1] but still fails an engine-side rule, e.g. below a hard
// floor, or ErrNotUnlocked) surfaces the reason via
// ApplyResult/FundingRejectedReason (SVC-8), never a silent revert; a
// locally-invalid request — non-finite, or a rawValue that rescales to a
// level outside the engine's [0,1] domain — is refused loudly via
// MET-V503 before ever reaching the engine.
//
// Gating Notes & Architecture Seams (BUG-058 / ASM-1482):
//   - Delta path — UNBLOCKED (FEAT-208 increments 1+2): ASM-1482's core
//     routing seam (internal/ui/router) exists and is wired into the real
//     composition root (cmd/metropolis/boot.go's bootCore): a real
//     *Screen is constructed, primed against a live Subscribe to
//     "f4.services", and bound into router.Router via BindSubscription,
//     so ApplyDelta now receives real protocol.Delta traffic end to end
//     (transport -> engine.core.Publish -> router -> this screen), not
//     just in this package's own unit tests. Only the capacityDemand
//     sub-view is published server-side so far
//     (compose/services_publish.go) — sliders/responseTimes/waitingLists/
//     publicServicePie remain server-side fast-follows (every field is
//     already `omitempty`, so no schema change is needed when they land);
//     ApplyDelta itself already handles all of them.
//   - SVC-8 command rejection surfacing (ApplyResult) — STILL GATED, but
//     the reason changed: router.RegisterResultHandler is a real, wired
//     surface now (unlike before increment 2), but no
//     protocol.Command/CorrelationID-bearing dispatch path reaches
//     ServicesAPI.SetFunding — this screen's own SetFunding
//     (screen.go:352) is never called from anywhere outside this
//     package's own screen.go/tests, and even if it were, the command it
//     issues ("services.set-funding") rides protocol.KindDebug
//     (ASM-1193's DebugPayload seam, screen.go's own opCommand), whose
//     ONLY handler, engine.core's handleDebug (commands.go), is a
//     documented no-op placeholder that just accepts the command
//     ("TODO(feat.debugmode, Debug op dispatch): route DebugPayload.Op to
//     real F12 panel operations... out of scope for engine.core today") —
//     it never inspects Op or dispatches anywhere. Reproduce with:
//     `grep -n "case protocol.Kind" internal/engine/core/commands.go`
//     (shows KindDebug -> handleDebug, the no-op) and
//     `grep -rn "SetFunding" internal/engine/compose/*.go` (zero matches
//     — compose, the ONLY package wired to receive real protocol.Command
//     traffic via GameplayCommandHandler, never calls SetFunding; its own
//     handleGameplay only maps KindBuy/KindZone/KindBuild/KindDemolish
//     onto world/build, never KindDebug). A prior version of this note
//     claimed a plain `grep -rln "\.SetFunding" internal cmd` returned
//     zero matches outside this package — that claim was FALSE as
//     literally written: education/refuse/social each define their own
//     unrelated SetFunding method (internal/engine/education/funding.go,
//     internal/engine/refuse/round.go, internal/engine/social/funding.go)
//     that the grep also matches — those are Go-level module APIs called
//     directly by other engine code, not protocol.Command handlers, so
//     they say nothing about whether a real command reaches
//     ServicesAPI.SetFunding. ApplyResult stays fully designed/
//     implemented/verified in unit tests, unreachable in the real binary
//     for lack of ANY dispatch path from a real Command to SetFunding,
//     not for lack of a routing seam. Do not invent custom/ad-hoc wiring
//     to close this gap; it is F4's own input-wiring
//     scope, not a router or composition-root gap (mirrors
//     internal/ui/screens/finance/doc.go's identical FIN-8 gating note).
//
// # SF-8/SF-9: determinism and race safety
//
// Every render function in this package is a pure function of its
// arguments — no wall-clock call anywhere in this package's non-test
// source (determinism_test.go mechanically greps for the literal call).
// Screen's every exported method locks mu, so ApplyDelta (delta-applying
// goroutine) and the accessor/Render*/command calls (render/input
// goroutine) may run concurrently; `go test ./internal/ui/screens/
// services/... -race -count=2` is part of this package's own
// verification.
//
// # Scope actually built vs. out of scope
//
// This dispatch builds SVC-1, SVC-2, SVC-4, SVC-5, SVC-7, SVC-8, and
// SF-1..SF-10. SVC-3 and SVC-6 are BLOCKED (tripwire, see above) — their
// wire/drill plumbing exists so wiring them later requires no structural
// change here. Out of scope (per ui.screen.services.md): the dispatch
// engine's routing/outcome computation (engine.dispatch); F1's coverage
// overlay rendering itself (ui.screen.map, FEAT-005); the dashboard
// drill-through graph and layout editor (MOD-038). The command kind
// (set-funding) rides the Debug seam pending real protocol Kinds
// (ASM-1193).
package services
