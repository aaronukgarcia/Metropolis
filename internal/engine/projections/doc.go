// Package projections is the projections engine + Slow-Fuse Principle
// module (MOD-031), module key engine.projections (GUID
// 56d13d82-76bd-4919-8b73-b796b4e37bf5, see code.json). Spec refs: §13
// (F7 Projections — "every demand/supply curve N years forward ...
// all seasonally aware; the anti-ambush machine"); A5, the Slow-Fuse
// Principle ("any decision whose principal effects land more than 5
// game-years out MUST render its projected consequence in the
// confirmation UI at the moment of decision. Now binding across ALL
// systems — education, planning quality, rehabilitation, BDI, debt —
// not merely the five named examples: slowFuseGate (decisions.go) is
// one shared, decision-type-agnostic function, so a sixth long-fuse
// system a later module adds is gated exactly as strictly as any of
// the five, with zero code change to this package"); UI-SPEC §4
// "Dashboards & the drill-through rule" (the projections-pane idiom:
// history solid, projection dim, threshold lines, decision markers);
// §1.3 ("the player should almost always be able to see trouble
// coming").
//
// # The mechanism (AC-1)
//
// [ProjectionsAPI] is code.json's documented inbound contract:
// registrants call [ProjectionsAPI.RegisterCurveProvider] with a
// [CurveProvider] (systems register curve providers); consumers call
// [ProjectionsAPI.Curve] with a key and a month range (UI subscribes
// to named projections). This package never knows or computes any
// system's actual curve math — that is each registrant's own job
// (Out of scope).
//
// # Confidence semantics (AC-6)
//
// Every [Point] carries a [Confidence]: Computed (derived from a
// registered provider within the current horizon N), Extrapolated (a
// trend continuation beyond N, or a death-warning margin computed
// from a flat/improving trend with no near-term crossing), or
// Unavailable (genuinely undefined — e.g. MarginToGhostCity against a
// city whose historic peak has never exceeded the AC-18 floor). A
// query beyond horizon N is never tagged Computed, regardless of what
// the underlying registered provider itself returns for that month —
// this package re-tags confidence itself rather than trusting a
// provider's own claim, which is exactly what stops a provider that
// keeps extrapolating a flat line past N from being presented with
// the same confidence as a real, modelled consequence (US-5).
//
// # Base horizon (ASM-237)
//
// The forecasting horizon N defaults to six game-years' worth of
// months, loaded from this package's own embedded horizon.json
// (config.go, see that file's baseHorizonMonths field for the exact
// figure) — GR#15: never a bare Go literal. ASM-237 (escalated to
// Aaron, open as of this build) flags that no base horizon is stated
// anywhere in the spec for before any forecasting unlock, and that
// A5's Slow-Fuse Principle (>5-year visibility, no early-game
// exemption) and §13 F7's unlock-gated N are in unresolved tension if
// the intended base N is ever shorter than five-to-six years — this
// package's default is only the smallest whole-year figure that
// clears A5 with one year of margin, not a confirmed balance number.
// [WithHorizonProvider]
// (projections.go) is the seam engine.unlocks plugs a real unlock-
// tier-aware horizon function into later (US-6) without any change
// here.
//
// # A known packaging shortfall (flag for Bill/Aaron)
//
// This build's dispatch brief scoped this junior to
// internal/engine/projections/ ONLY, because data/errors.json was
// claimed by another session this same wave. Two consequences, both
// documented at their source and repeated here for visibility:
//
//   - errors.go: every raised MET- code REUSES a code already
//     registered to another module (season/market/helper/invariant),
//     since no E/G-layer sub-range exists for engine.projections in
//     data/errors.json's ranges.reserved table and this session could
//     not add one. Recommended real allocation: G100-G199 (the next
//     free G-layer sub-range after engine.citizens' G000-G099).
//   - config.go: horizon.json/deathwarnings.json are go:embed'd INSIDE
//     this package rather than living under data/ and loading through
//     internal/foundation/data's shared §24-style Load helper, because
//     creating data/*.json or touching internal/foundation/data/* was
//     outside this build's file-ownership boundary. AC-20's real
//     requirement (data-sourced, never a Go literal, non-empty
//     disclosure field) is genuinely met either way, but this is not
//     yet the house convention every other config file follows.
//
// # BUG-058 (Bill/Ben — real Slow-Fuse wiring is not yet possible)
//
// Per-module wiring of the Slow-Fuse gate for A5's five named example
// systems (education, planning quality, rehabilitation, BDI, debt) is
// each producer module's OWN obligation at its own build time — this
// item cannot complete that wiring alone. BUG-058 (filed 2026-08-11,
// re-confirmed against code.json this build): engine.education and
// engine.finance still have no registered ProjectionsAPI inbound edge
// in code.json today, so those two modules have no legal GR#20 call
// path yet to register curve providers or attach a projected
// consequence at confirmation time. This item's own tests exercise
// slowFuseGate against fake, previously-unknown decision types
// precisely because the real education/debt consumers are not
// buildable yet (Out of scope) — but the S7 exit gate's "Slow-Fuse
// projections render for education/debt-class decisions" clause
// cannot be FULLY satisfied until BUG-058 is fixed and those modules
// land.
//
// # FEAT-068: MarginToInsolvency / MarginToGhostCity / WarningLedger
//
// [ProjectionsAPI.MarginToInsolvency] and [ProjectionsAPI.
// MarginToGhostCity] exist specifically to satisfy FEAT-068's
// six-word requirement ("firing without prior warning = defect"):
// both extrapolate a real registered curve (engine.finance's
// CurveKeyFinanceInsolvencyRisk / engine.spiral's
// CurveKeyGhostCityPopulation — this package never reimplements
// either module's own bookkeeping) toward that module's own documented
// death-condition threshold, and every crossing of AC-20's
// data-sourced warning threshold is recorded in [WarningLedger] (AC-19)
// — queryable independent of whether any UI ever rendered it.
// engine.finance.md AC-29 and engine.spiral.md AC-15 are the two
// consumers of this WarningLedger: each queries it to prove its own
// death-condition trigger path was preceded by a warning, which is
// this module's load-bearing half of FEAT-068's own title ("the
// projections engine warned you") — the death-condition TRIGGER logic
// itself, and the gate that makes each provably wait on this warning,
// are each of those two modules' own job (Out of scope), not this
// package's.
//
// BUG-118 (2026-08-12, done, Destructive-ACCEPTed): the engine.spiral
// <-> engine.projections code.json edge is LIVE — re-confirmed against
// code.json directly this build (engine.spiral's outbound calls
// include engine.projections; engine.projections' inbound consumers
// include engine.spiral). MarginToGhostCity is built against this real
// edge, not an interim fake-provider-only fallback (ASM-471's
// correction, superseding this item's own 2026-08-12 BA pass).
//
// # Determinism (GR#21)
//
// This package never reads the wall clock (grep -rn "time\.Now\|
// time\.Since" internal/engine/projections/*.go, excluding _test.go,
// returns no matches) — every query is a function of registered
// provider state, the current month, and the loaded horizon/
// death-warning config, never wall-clock time. decisionStepsForKey
// (decisions.go) ranges over a Go map but only ever SUMS matching
// deltas, an order-independent operation, so the map's non-
// deterministic iteration order never affects Curve's returned series.
//
// # Perf-CI wiring (AC-13; BUG-034 still open)
//
// Projection recomputation cost is intended to be phase-timed under
// engine.core's PhaseFinance monthly phase (internal/engine/core/
// phase.go) — the closest existing MonthlyPhaseOrder stop to where a
// consuming module would naturally trigger a projection recompute,
// pending a dedicated PhaseProjections stop if that fixed pipeline is
// ever extended — via harness.synth's existing PerfResult/PerfRecord
// machinery (internal/harness/synth/perf.go, results.go), so a
// regression is caught by CompareToBaseline's relative comparison
// against a CI-recorded baseline (baseline.go), never an absolute
// wall-clock assertion inside this package's own tests (none exist
// here — grep -rn "time\.Since|elapsed <|elapsed >|Sleep\(" internal/
// engine/projections/*_test.go returns no matches). BUG-034 is open
// and no CI-measured real-scale baseline has been recorded yet — this
// package is wired to REPORT through harness.synth's relative-
// comparison machinery, not proof of a currently-passing regression
// comparison against a baseline that does not yet exist.
//
// # What this package does NOT do (Out of scope)
//
// The forecasting-horizon unlock mechanism itself (engine.unlocks'
// job); any individual system's actual curve math; the real wiring of
// education/debt decisions through the Slow-Fuse gate (BUG-058, above);
// int.solver's cloud-offload path for "deep projections"; a specific
// tuned value for the base horizon, threshold magnitudes, or any
// curve's numeric shape (M2 Batch-tuning); the death-condition trigger
// logic itself and the gate that makes engine.finance/engine.spiral
// provably wait on this module's warning signal; the player-facing UI
// surface for these warnings (feat.deathwarnings.md's job).
package projections
