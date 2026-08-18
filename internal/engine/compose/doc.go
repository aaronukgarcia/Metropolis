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
// citizens, build and invariant all on the daily tick (invariant last,
// BUG-268/FEAT-169):
//
//	world -> citizens -> market -> consumption -> finance -> build -> attract
//	         citizens ------------------------------------> build -> invariant  (PhaseDailyTick, every tick)
//
// This order is held in a fixed slice (registrationOrder), never a Go map,
// so registration is deterministic run-over-run (GR#21). The order is what
// determines intra-phase hook order where modules share a phase: market
// before consumption (both PhaseConsumptionShortfall), and citizens before
// build before invariant (all three PhaseDailyTick) so citizens'
// births/deaths and the build queue both advance before that day's
// conservation check observes them. attract is now alone on
// PhasePopulation (see below).
//
// # Phase-kind mapping (the flagged-ambiguous modules)
//
// The fixed phase set is engine.core's contract; this package does not
// add, remove, or reorder phases. The chosen mapping, following the BA's
// proposed table in docs/planning/acceptance/feat.compositionroot.md:
//
//	citizens -> PhaseDailyTick     (daily tick, before build/invariant; MOD-018 real cold pass — FEAT-169)
//	build    -> PhaseDailyTick     (daily tick, alongside citizens/invariant; MOD-026 real hook — BUG-268)
//	attract  -> PhasePopulation    (monthly; MOD-029 real hook)
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
// citizens was originally mapped onto the monthly PhasePopulation slot via
// spawnHook, a flat monthlyBirths=8/month fake with no connection to real
// demographics (compose's only citizens integration before FEAT-169).
// FEAT-169 (Aaron, "childbearing on day one") replaced it with
// coldPassHook, which calls CitizensAPI.AdvanceDayTick — itself already a
// once-per-day-tick call internally to citizens (its own amortised
// 1/30-shards-per-day cold pass covering per-citizen mortality and the new
// FEAT-160 fertility) — so it moved onto the daily PhaseDailyTick slot for
// the same reason build did: a monthly phase call cadence would have been
// 30x out of step with AdvanceDayTick's own daily contract. See
// docs/planning/icd/engine.citizens-coldpass.md (the ICD this integration
// was built against) for the full Update Class / Determinism / Shard Scope
// reasoning, and compose.go's coldPassHook doc comment for the per-tick
// (not month-boundary) peopleDelta fold — the ICD's §4 floated pulling
// VitalEvents at the month boundary, but that would defer the
// conservation credit past the tick the population actually changed on
// (caught for real by the daily invariant check during FEAT-169's build).
// See the "Citizen id namespace map" section below for the
// ErrCitizenIDNamespaceSeam guard — the ICD's §12 open decision 2.
//
// # Citizen id namespace map (FEAT-169, corrected by destructive review)
//
// Three packages independently mint citizen ids from disjoint high-bit
// ranges, by CONVENTION (not a shared allocator):
//
//	[1,                    attract.MigrantIDBase)      compose: seed population + any direct seeding (simState.nextCitizenID)
//	[attract.MigrantIDBase, citizens.FertilityChildIDBase)  engine.attract: admitted-migrant ids (migration.go's migrantIDHighBit)
//	[citizens.FertilityChildIDBase, ...)                engine.citizens: fertility-born child ids (fertility.go's fertilityChildIDBase)
//
// This convention FAILED once already: both attract's migrantIDHighBit and
// citizens' original fertilityChildIDBase independently chose 1<<62, so
// with FEAT-169 wiring both the citizens cold-pass tick and the attract
// migration hook live in the same composition, a duplicate citizen id
// (and a silently aliased citizen — TotalPopulation's row-count view
// cannot detect it) was reachable within months of simulated play. Fixed
// 2026-08-18 (destructive-review REJECT): citizens' base moved to 1<<63,
// and the convention is now defended THREE ways: (1) compose's
// spawnCitizens rejects any mint at or past attract.MigrantIDBase
// (ErrCitizenIDNamespaceSeam), checked on every mint including the Wire-
// time seed population; (2) Wire itself asserts
// citizens.FertilityChildIDBase >= 2*attract.MigrantIDBase before
// constructing anything (ErrIDNamespaceRangesOverlap) — the boundary
// BETWEEN attract's and citizens' ranges, which (1) does not cover; (3)
// engine.citizens independently rejects a LifeEventBirth whose id already
// exists in its own cold or hot store (ErrDuplicateCitizenID), defense in
// depth for the case the convention is violated by some future caller
// neither (1) nor (2) can see. None of the three is a substitute for the
// others: (1)/(2) are compose-side range checks that can never fire once
// the constants are correct; (3) is the only one that would catch an
// actual runtime collision.
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
