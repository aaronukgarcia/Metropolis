// Package compose is the composition root (FEAT-082, module key
// feat.compositionroot, GUID dbdaf1f1-d096-4bf8-8b3a-964084e93ea9) — the
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
// existing invariant.WireDaily, and the existing world/citizens/market
// APIs, plus four stub PhaseHooks (M0-ENG §2, module-stubbing).
//
// GR#20 (Contract-First, Stub-Forever): the composition root is the only
// package that imports engine.core together with all the wired modules.
// Nothing imports it except the two runnable tops — cmd/metropolis
// (interactive boot) and internal/harness/headless (headless driver).
//
// # Registration order (the composition contract)
//
// The baseline-one module set is registered in this fixed order, invariant
// last and on the daily tick:
//
//	world -> citizens -> market -> consumption -> finance -> build -> attract
//	invariant                                                           (PhaseDailyTick, every tick)
//
// This order is held in a fixed slice (registrationOrder), never a Go map,
// so registration is deterministic run-over-run (GR#21). The order is what
// determines intra-phase hook order where two modules share a phase:
// market before consumption (both PhaseConsumptionShortfall) and citizens
// before attract (both PhasePopulation).
//
// # Phase-kind mapping (the two flagged-ambiguous modules)
//
// The fixed phase set is engine.core's contract; this package does not
// add, remove, or reorder phases. The chosen mapping, following the BA's
// proposed table in docs/planning/acceptance/feat.compositionroot.md:
//
//	build  -> PhaseLandValueDecay  (land-value decay slot; MOD-026 stub)
//	attract -> PhasePopulation     (after citizens; MOD-029 stub)
//
// Both are scheduling decisions the spec does not pin (the same class
// engine.invariant.md AC-7 flagged as ASM-080); recorded here so the lead
// rules once rather than the developer guessing.
//
// # STUB-FOR-BASELINE policy
//
// Baseline one (FEAT-083) wires the must-have spine and leaves the rest
// coarse. world/citizens/market/invariant are REAL (world is the terrain/
// ownership store, citizens the cold citizen store, market the price
// registry, invariant the conservation checker). consumption/finance/
// build/attract are STUB PhaseHooks with coarse, directional behaviour
// sufficient to keep the loop alive: citizens + attract move the people
// stock, finance moves the money stock (a budget-closing wage/tax
// transfer), consumption and build are no-op slots that keep their phase
// position wired for the real modules to fill one at a time. roads/
// traffic/logistics/services/wellbeing/unlocks/education/crime are DEFERRED
// ENTIRELY — no stub hooks are registered for them (a stub that occupies a
// phase slot and does nothing is dead weight, not a composition).
//
// # Error sub-range
//
// This package owns MET-G800-G899 (registered in data/errors.json's
// ranges.reserved table) — see errors.go. The E layer was exhausted by the
// eleven earlier engine modules and the code format widened to four digits
// on 2026-08-14 (BUG-234); G800-G899 was the next free engine block after
// attract's G700-G799.
package compose
