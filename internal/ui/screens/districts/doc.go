// Package districts implements F8, the Districts & Policies screen
// (FEAT-022): district drawing/naming, a policy library browser, impact
// preview, inline conflict warnings, and a per-district tax-settings
// panel.
//
// Module key: ui.screen.districts (see code.json)
// Spec refs:  §13-F8 (docs/METROPOLIS-MASTER-v2.1.md line 253); §52
//
//	Policies v2 & Named Districts (lines 666-673); UI-SPEC §4 drill-through
//	rule (line 761); docs/planning/acceptance/ui.screen.districts.md
//	(this package's own criteria, AC-1..AC-13).
//
// # Provenance -- why most of this package renders "unavailable"
//
// code.json registers TWO outbound edges for ui.screen.districts:
// engine.policies and engine.tax. Only engine.tax is merged to main
// (internal/engine/tax/, MOD-052) -- engine.policies exists ONLY as commit
// 4451493 on the lane/bob worktree branch (REJECT-state, per the dispatch
// brief; never merged, never accepted). GR#25 forbids building against an
// unregistered dependency; building against a REGISTERED but
// unmerged/rejected dependency's exact field shapes is no safer -- rejected
// code is exactly the code most likely to change shape before it lands.
// So: every AC whose data comes from engine.policies (AC-2, AC-3, AC-4,
// AC-5, AC-8) was DECLARED BLOCKED here with a tripwire, following the F4
// precedent. THE TRIPWIRE FIRED 2026-08-20 when engine.policies landed on
// main (lane/bob sweep): those five ACs are now PENDING BUILD under
// FEAT-210 against the real, accepted PoliciesAPI shape -- still not
// stubbed ahead here, still rendering RenderBlockedFeature until FEAT-210
// executes. Original rationale preserved below, following the F4
// SVC-3/SVC-6 precedent (internal/ui/screens/services/doc.go) exactly --
// no wire schema, no forward-compat stub is invented for a PoliciesAPI
// shape this package has not seen accepted. Only AC-1 (protocol purity,
// structural), AC-6 (engine.tax is live), the whole-view half of AC-7,
// AC-9 (partial, over the one live command), AC-10, AC-11, AC-12 and AC-13
// are built.
//
// # View subscription
//
// "f8.districts" (wire.go) -- this dispatch's own naming choice
// (code.json's inbound.name is null; F8 is undispatched, the same
// convention every not-yet-built F-screen shows). Carries two optional
// sub-surfaces: Districts (AC-2's roster -- always absent in practice,
// nothing populates it) and TaxSettings (AC-6, live).
//
// # AC-1: protocol purity (GR#20)
//
// This package never imports internal/engine -- .golangci.yml's depguard
// ui-must-not-import-engine rule blocks it at lint time; sf1_test.go's
// TestNoEngineImport makes the same guarantee mechanically in `go test`
// (mirrors every built F-screen's own sf1_test.go).
//
// # AC-2: district drawing/naming -- PENDING BUILD (FEAT-210; engine.policies landed 2026-08-20)
//
// US-1's cell-paint-and-name flow issues a CreateDistrict command against
// engine.policies' registered edge. Not built: engine.policies is not on
// main (see Provenance above). Screen.Districts()/District exist purely as
// a forward-compat accessor/type pair (types.go/screen.go) and always
// report have=false today -- nothing sends the wire field. Flagged for
// Bill: AC-2 cannot be closed until engine.policies lands accepted on
// main.
//
// # AC-3: policy library browser -- PENDING BUILD (FEAT-210; engine.policies landed 2026-08-20)
//
// US-2's categorised browser sources PoliciesAPI's library query. Not
// built: no PoliciesAPI, no wire schema for it (inventing one against
// rejected code would itself be a false-pass risk this AC's own text
// warns against, applied one layer up). RenderBlockedFeature (render.go)
// renders the honest "unavailable -- engine.policies not yet merged to
// main" state for this pane.
//
// # AC-4: impact preview (confidence-honest rendering) -- PENDING BUILD (FEAT-210; engine.policies landed 2026-08-20)
//
// US-3's PreviewImpact rendering (Computed solid / Extrapolated dim, per
// engine.policies.md AC-4/AC-5 and UI-SPEC §4's history-vs-projection
// idiom) has no live payload to render. RenderBlockedFeature covers this
// pane too, rather than a preview chart that would (per this AC's own
// "Lazy implementation this rejects" clause) either render nothing
// honestly labelled or invent fake confidence data.
//
// # AC-5: conflict warnings -- PENDING BUILD (FEAT-210; engine.policies landed 2026-08-20)
//
// US-4's inline conflict warning sources PoliciesAPI's conflictsWith data
// (engine.policies.md AC-11). No live source; RenderBlockedFeature covers
// this pane.
//
// # AC-6: per-district tax settings (live, engine.tax)
//
// RenderTaxSettings draws the panel, scoped to Screen.SelectedDistrict()
// (US-5); Screen.SetDistrictMultiplier issues the player action as a
// protocol.DebugPayload command with the fixed Op string
// "districts.set-tax-multiplier" (ASM-1193's districts.<verb> convention,
// mirroring ui.screen.services' "services.set-funding" seam -- int.
// protocol's sealed v1 command vocabulary has no SetDistrictMultiplier
// Kind and this screen may not edit internal/protocol). Every field this
// screen renders/validates mirrors internal/engine/tax/tax.go's
// SetDistrictMultiplier/InstrumentInfo exactly (Multiplier stacks with
// Rate; RateMax is the identical SEC-098 effective-rate cap the engine
// enforces) -- never a screen-invented rule. The displayed value only
// updates from an applied Delta, never from a locally-mutated value held
// before the engine confirms it (AC-6's explicit "not from a
// locally-mutated value" requirement) -- SetDistrictMultiplier never
// writes s.taxSettings itself.
//
// # AC-7: drill-through -- whole-view built, row-level BLOCKED (ASM-275)
//
// FinanceJumpTarget (render.go) returns a dash.DrillTarget naming F2
// Finance's real, registered view ("f2.finance") for a district's
// aggregate tax-revenue figure -- the buildable "whole-view aggregate
// figure... Enter opens the relevant whole source view" half. It does NOT
// filter F2 to the district (F2 has no per-district scoping dimension on
// its own registered outbound edges or wire schema) -- named honestly
// rather than fabricated. Row-level drill-through (a specific incidence
// entry) requires int.protocol's structured sub-entity DrillTarget
// addressing scheme, which ASM-275 (already open, filed by another BA)
// states does not exist yet -- NOT built here, exactly as this item's own
// AC-7 text and "Out of scope" section require; no check for it exists in
// this package's tests.
//
// # AC-8: scope-resolution consumption (ResolveScope) -- PENDING BUILD (FEAT-210; engine.policies landed 2026-08-20)
//
// F8's district cell-highlight rendering would source
// engine.policies.ResolveScope. Not built: no engine.policies on main.
//
// # AC-9: command rejection surfacing (partial -- the one live command)
//
// A SetDistrictMultiplier the engine rejects surfaces via ApplyResult/
// TaxRejectedReason (mirrors services.Screen's fundingRejectedReason /
// finance's loanRejectedReason pattern exactly) rather than silently
// no-op'ing. This is necessarily partial: AC-9's other named failure modes
// (invalid cell selection for district creation, an unknown scope target)
// belong to AC-2/AC-8's BLOCKED commands and have no live path to exercise
// today. A locally-invalid request (non-finite/negative multiplier, or an
// empty district/instrument ID) is refused loudly via MET-V603 before
// ever reaching the engine, never silently dropped.
//
// Gating Notes & Architecture Seams (BUG-058/ASM-1482, mirrors finance/
// services doc.go's identical FIN-8/SVC-8 note):
//   - AC-9's ApplyResult routing is fully designed, implemented, and
//     verified in unit tests. However, actual routing of command results
//     to screen sub-receivers is currently unwired in the wider frame
//     pending the core routing-seam implementation (ASM-1482) -- do not
//     invent custom/ad-hoc wiring.
//
// # AC-10: stale/unknown subscription handling
//
// A Delta for an unbound (unknown/stale) SubscriptionID is logged via
// MET-V601 and dropped, matching ui.screen.map's AC-8 precedent this AC
// cites. A malformed patch (bad JSON, wrong schemaVersion, oversized) is
// logged via MET-V600 and dropped -- the screen's data keeps its
// last-known-good state, never partially applied, never panics.
//
// # AC-11/AC-12: determinism and race safety
//
// Every render function in this package is a pure function of its
// arguments -- no wall-clock call anywhere in this package's non-test
// source (determinism_test.go mechanically greps for the literal call, per
// AC-11's own grep check). Screen's every exported method locks mu, so
// ApplyDelta (delta-applying goroutine) and the accessor/Render*/command
// calls (render/input goroutine) may run concurrently; `go test
// ./internal/ui/screens/districts/... -race -count=2` is this package's
// own verification (AC-12).
//
// # AC-13: this doc
//
// States the module key, cites §13-F8 and §52, documents the
// "f8.districts" view-subscription name and the
// "districts.set-tax-multiplier" command this screen depends on, states
// explicitly that row-level drill-through is BLOCKED pending ASM-275
// (AC-7) -- and additionally, beyond AC-13's own minimum, that AC-2/3/4/5/8
// are wholesale PENDING BUILD under FEAT-210 (engine.policies landed
// 2026-08-20; Provenance, above) -- and cross-references engine.policies.md's
// PreviewDrift/confidence-tag conventions this screen would render, not
// reinvent, once that lands.
//
// # Scope actually built vs. out of scope
//
// This dispatch builds AC-1, AC-6, AC-7 (whole-view half only), AC-9
// (partial), AC-10, AC-11, AC-12, AC-13. AC-2, AC-3, AC-4, AC-5, AC-8 and
// AC-7's row-level half await FEAT-210 / ASM-275 (see above) -- their wire/
// drill plumbing is deliberately NOT stubbed against engine.policies'
// unreviewed shape; wiring them requires this package to be revisited once
// engine.policies lands, not merely flipped on. Out of scope (per
// ui.screen.districts.md): the policy/tax/district engine mechanics
// themselves (engine.policies, engine.tax); row-level drill-through
// (ASM-275); the Fiscal Circuit Sankey (ui.screen.finance's domain); the
// dashboards/layout-editor/drill-through framework (MOD-038);
// enactment-cost debiting UX (engine.policies AC-19, blocked on BUG-058).
package districts
