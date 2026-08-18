// Package compose is the composition root (FEAT-082, module key
// feat.compositionroot, GUID fae40226-71d0-4836-be99-854c7b41eb4a) — the
// ONE place in the codebase permitted to import engine.core AND every
// wired module. It exists to close the gap every runnable path carried
// before it landed: `cmd/metropolis`, `-headless`, `harness/replay` and
// `harness/synth` all performed ZERO RegisterPhaseHook calls, so the only
// production hook caller was engine.invariant.Wire, which nothing invoked
// (ASM-001, "headless == live" — see engine.headless.md AC-9).
//
// # What this package is, and is NOT
//
// This is the COMPOSITION concern, distinct from the orchestrator
// mechanism it wires against. engine.core (M0-ENG §1.1-1.3) defines how a
// phase pipeline runs and what RegisterPhaseHook/PhaseHook guarantee; this
// package defines which real modules register, in what order, driven
// headlessly. It does not reimplement the phase pipeline, the invariant
// hook, or the registry — it composes the existing core.Engine, the
// existing invariant.WireDaily, and the existing world/citizens/market/
// consumption/build/attract APIs (plus a coarse finance stub PhaseHook),
// M0-ENG §2's module-stubbing discipline.
//
// GR#20 (Contract-First, Stub-Forever): the composition root is the only
// package that imports engine.core together with all the wired modules.
// Nothing imports it except the two runnable tops — cmd/metropolis
// (interactive boot) and internal/harness/headless (headless driver).
//
// # Registration order (the composition contract)
//
// The baseline-one module set is registered in this fixed order, with
// build and invariant both on the daily tick (invariant last, BUG-268):
//
//	world -> citizens -> market -> consumption -> finance -> build -> attract
//	                                                          build -> invariant  (PhaseDailyTick, every tick)
//
// This order is held in a fixed slice (registrationOrder), never a Go map,
// so registration is deterministic run-over-run (GR#21). The order is what
// determines intra-phase hook order where two modules share a phase:
// market before consumption (both PhaseConsumptionShortfall), citizens
// before attract (both PhasePopulation), and build before invariant (both
// PhaseDailyTick) so the build queue advances before that day's
// conservation check observes it.
//
// # Phase-kind mapping (the two flagged-ambiguous modules)
//
// The fixed phase set is engine.core's contract; this package does not
// add, remove, or reorder phases. The chosen mapping, following the BA's
// proposed table in docs/planning/acceptance/feat.compositionroot.md:
//
//	build  -> PhaseDailyTick       (daily tick, alongside invariant; MOD-026 real hook — BUG-268)
//	attract -> PhasePopulation     (after citizens; MOD-029 real hook)
//
// build was originally mapped onto the monthly PhaseLandValueDecay slot
// (a scheduling decision the spec does not pin, the same class
// engine.invariant.md AC-7 flagged as ASM-080). BUG-268 (Aaron,
// 2026-08-18): BuildAPI.Tick elapses exactly one simulation DAY of lead
// time per call, so registering it against a phase that only fires once
// per simulation MONTH made every lead time run 30x too slow (a 45-day
// dwelling took 45 months). Moved onto the daily PhaseDailyTick slot —
// the only daily phase this package's fixed phase set offers — so the
// queue advances once per sim-day, matching data/buildings.json's own
// day-denominated documentation.
//
// # STUB-FOR-BASELINE policy
//
// Baseline one (FEAT-083) wires the must-have spine and leaves the rest
// coarse. world/citizens/market/consumption/build/attract/invariant are
// REAL: world is the terrain/ownership store, citizens the cold citizen
// store, market the price registry, consumption the utility-network draw
// (MOD-021's SolveDailyTick against coarse single-source networks), build
// the build-queue advance (MOD-026's BuildAPI.Tick) plus the Buy/Zone/
// Build/Demolish command surface (routed through engine.core's
// GameplayCommandHandler seam), attract the attractiveness-driven
// migration step (MOD-029's ApplyMigration — g(A − A_world), never a
// hardcoded +N), invariant the conservation checker. finance remains a
// STUB PhaseHook with coarse, directional behaviour sufficient to keep
// the loop alive: a budget-closing wage/tax transfer that moves the money
// stock. roads/traffic/logistics/services/wellbeing/unlocks/education/
// crime are DEFERRED ENTIRELY — no stub hooks are registered for them (a
// stub that occupies a phase slot and does nothing is dead weight, not a
// composition).
//
// # Gameplay command seam (engine.core GameplayCommandHandler)
//
// engine.core's HandleCommand does not itself adjudicate the four
// gameplay-intent kinds (Buy/Zone/Build/Demolish); it delegates them to an
// injected GameplayCommandHandler, deny-by-default when unset. This
// package injects the one handler (simState.handleGameplay) that maps those
// kinds onto engine.world.PurchaseTile and engine.build's Submit*Command
// surfaces — the same single-wiring-path discipline (AC-1/GR#20) as the
// phase hooks, so no runnable path routes gameplay intent around compose.
//
// # Error sub-range
//
// This package owns MET-G800-G899 (registered in data/errors.json's
// ranges.reserved table) — see errors.go. The E layer was exhausted by the
// eleven earlier engine modules and the code format widened to four digits
// on 2026-08-14 (BUG-234); G800-G899 was the next free engine block after
// attract's G700-G799.
package compose
