BOW code: MOD-019

# Acceptance criteria — engine.invariant (MOD-019)

**BOW code:** MOD-019
**Spec refs:** §14 (`docs/METROPOLIS-MASTER-v2.1.md` line 259: "invariant checker every tick (conservation: people, money, goods must balance — hard assert in dev)"); §19.3/§19 intro (line 337: "nothing ever despawns — vehicle-conservation is an invariant-checker assert. Gridlock is real, visible, and yours to fix."); M0-ENG §6 point 3 (working agreement, Definition of Done: "determinism-relevant modules also add a shard-count invariance test"); code.json `engine.invariant` entry (consumes `engine.core` MOD-012).
**Date:** 2026-08-08
**Status:** draft-ahead
**Package under test:** `internal/engine/invariant/` (path from `node claude-bow.js show MOD-019`)
**Standard gates:** see `README.md` — all apply, package for SG-4/SG-7 is `./internal/engine/invariant/...`.

## User stories

- **US-1.** As the engine, I need a hard assert every tick (in dev builds) that people, money, goods, and vehicles conserve, so that a bug that silently creates or destroys state is caught at the moment it happens, not months of game-time later when the discrepancy has compounded (§14).
- **US-2.** As a release build, I need the same conservation checks to run but produce a registry-sourced logged error instead of a hard crash, so that a live game can surface a real bug to the player/F12 panel without terminating the process (§14; GR#1/#7).
- **US-3.** As the transport system's design (§19), I need vehicle-conservation enforced structurally, so that despawn-masking gridlock — 'Blue''s known failure mode — is impossible even before `engine.traffic` exists to generate vehicles (§19.3).
- **US-4.** As `harness.headless` and CI, I need the invariant suite runnable standalone against a scenario/save, so that H-HEADLESS's "per-phase timing + invariant reports every tick" (M0-ENG §2.3) has real checks to report, and so H-SYNTH's perf runs are validated as not just fast but correct.
- **US-5.** As a future engine module (`engine.market`, `engine.finance`, `engine.traffic`, etc., landing Sprint 4+ per the sprint plan), I need a registration seam for adding new conserved stocks, so that "conservation invariants extended to each new stock" (sprint plan S9 exit gate) is a config/registration change, not a rewrite of this package.

## Scope

The invariant-checker framework (hard assert in dev / registry-logged error in release), the four v1 conserved stocks (people, money, goods, vehicles), its wiring into `engine.core`'s tick pipeline, and its standalone-runnable form for H-HEADLESS/CI.

## Acceptance criteria

### Functional

- **AC-1 (GR#20).** An `Invariant` interface exists with a method that computes/verifies a conservation check against the current world state (or a state snapshot/delta), e.g. `Check(state Snapshot) Violation` (returns nil/zero-value `Violation` when balanced). Check: `go doc ./internal/engine/invariant Invariant` shows this method.
- **AC-2.** A registry of `Invariant` implementations exists, seeded with at least four: people conservation, money conservation, goods conservation, vehicle conservation. Check: `grep -n "type PeopleInvariant\|type MoneyInvariant\|type GoodsInvariant\|type VehicleInvariant" internal/engine/invariant/*.go` (or equivalently named types) all match.
- **AC-3.** People conservation: total citizen count across all fidelity tiers (HOT/WARM/COLD, §5.2) plus tracked emigration/immigration/birth/death deltas for the tick balances to zero unexplained change — a citizen must be traceable to birth, death, or migration, never simply vanish or appear. Check: a passing test constructs a state with an untracked citizen disappearance and asserts a `Violation` is reported (`grep -rn "func Test.*[Pp]eople" internal/engine/invariant/*_test.go`).
- **AC-4.** Money conservation: total money in circulation (citizen wealth + firm/institution balances + city treasury + in-flight transactions) balances against tracked income/expenditure for the tick — this is the invariant the sprint plan's S4 exit gate later relies on running "green over 120 headless months", so it must be extensible to the finance module's ledger without a rewrite. Check: a passing test asserts an untracked money creation/destruction is caught (`grep -rn "func Test.*[Mm]oney" internal/engine/invariant/*_test.go`).
- **AC-5.** Goods conservation: tracked commodity/stock quantities (per §6/§8 JIT logistics, once those modules register their stocks) balance against production, consumption, and in-transit flows for the tick. Check: `grep -n "GoodsInvariant" internal/engine/invariant/*.go` and a passing test covering a synthetic goods-stock scenario (`grep -rn "func Test.*[Gg]oods" internal/engine/invariant/*_test.go`).
- **AC-6 (§19.3).** Vehicle conservation: every vehicle instance is traceable to spawn (trip origin) and despawn (trip completion/parking), and the invariant fails loudly if a vehicle's count changes without a matching spawn/despawn event — making despawn-masking structurally impossible per §19.3, even before `engine.traffic` (Sprint 5) exists to generate real vehicles. Check: a passing test constructs a synthetic vehicle-count mismatch and asserts a `Violation` (`grep -rn "func Test.*[Vv]ehicle" internal/engine/invariant/*_test.go`).
- **AC-7.** The invariant checker runs every tick, wired into `engine.core`'s phase pipeline as a registered consumer (per code.json's outbound edge to `engine.core`), not as an out-of-band script run separately from normal ticking. Check: `grep -n "invariant\." internal/engine/core/*.go` (or the reverse: `engine.core`'s phase list references an `invariant` phase/hook) shows the wiring; if `engine.core` doesn't exist yet at this item's own build time, this AC is satisfied by the package exposing a `RunEveryTick(pipeline PhaseRegistrar)` (or equivalent) registration function ready for `engine.core` to call, with a passing test proving it is invoked once per simulated tick against a fake pipeline.
- **AC-8.** In dev builds (a build tag or debug-mode runtime flag, consistent with M0-ENG §3's "debug is a runtime feature switch, not a build flavour"), a `Violation` triggers a hard assert (panic or `os.Exit` with diagnostic output naming the invariant, the tick, and the imbalance amount). Check: `go doc ./internal/engine/invariant` documents the dev-mode hard-fail behaviour; a passing test (using a test-only override, not an actual process-killing panic in the test binary) asserts the hard-fail path is selected when debug mode is on (`grep -rn "func Test.*[Hh]ardFail\|func Test.*[Dd]evMode" internal/engine/invariant/*_test.go`).
- **AC-9.** In release builds/non-debug mode, a `Violation` instead produces a registry-sourced logged error (GR#1/#7: new `MET-E`-range code(s) added to `data/errors.json`) surfaced to the F12 error log tail's underlying store (`foundation.errors`), never a silent drop. Check: `grep -n "MET-" internal/engine/invariant/*.go` finds registry code references; a passing test asserts the release-mode path logs rather than panics (`grep -rn "func Test.*[Rr]elease" internal/engine/invariant/*_test.go`).
- **AC-10.** A standalone entry point (function or small `cmd/`) runs the full invariant suite against a save/scenario outside the live tick loop, for `harness.headless`'s "invariant reports every tick" output and CI's invariant suite (sprint plan S2 exit gate: "stub-vs-real conformance suite runs" alongside this). Check: `go doc ./internal/engine/invariant RunSuite` (or equivalent) exists and is exercised by a passing test loading a fixture/synthetic save.

### Error handling

- **AC-11 (GR#7).** Every `Violation` carries enough context (invariant name, tick, expected vs. actual value, affected entity IDs where applicable) to construct a registry error via `errs.New` without a second data-gathering pass. Check: `go doc ./internal/engine/invariant Violation` lists these fields.
- **AC-12.** A missing/unregistered stock referenced by a module that hasn't registered its invariant yet (e.g. `engine.market` not yet real) does not crash the suite — the checker skips unregistered stocks and reports which are unchecked, rather than assuming zero and false-flagging. Check: passing test coverage (`grep -rn "func Test.*[Uu]nregistered" internal/engine/invariant/*_test.go`).

### Determinism & safety

- **AC-13 (GR#21).** Running the invariant suite twice against the same tick's state produces identical `Violation` results (no nondeterministic floating-point accumulation order causing a flaky false-positive). Check: a passing test asserts determinism (`grep -rn "func Test.*[Dd]eterminis" internal/engine/invariant/*_test.go`).
- **AC-14 (M0-ENG §6 point 3 DoD; shard-count invariance).** The invariant suite produces identical results run against the same world computed under different shard/worker counts (1 vs. N workers) — this is the specific DoD requirement for determinism-relevant modules. Check: `grep -rn "func Test.*[Ss]hard.*[Ii]nvarian\|func Test.*[Ww]orkerCount" internal/engine/invariant/*_test.go` finds coverage, and it passes.
- **AC-15 (SG-7 scoped; GR#21).** `grep -rn "time.Now\|time.Since" internal/engine/invariant/*.go` (excluding `_test.go`) returns no matches — invariant checks run against simulation tick/state data only, never wall clock.
- **AC-16.** `go test ./internal/engine/invariant/... -race -count=1` passes with no data race when invariants are checked concurrently across the 256-shard worker pool (§1.2/M0-ENG §1) that `engine.core` will eventually drive this from. Check: `grep -n "go func()" internal/engine/invariant/*_test.go` finds at least one concurrency test.

### Documentation

- **AC-17.** `internal/engine/invariant/doc.go` states the module key `engine.invariant`, cites §14 and §19.3, and documents the extensibility contract (how a future module registers a new conserved stock) referenced by US-5. Check: `grep -n "engine.invariant" internal/engine/invariant/doc.go` and `grep -n "§14\|section 14" internal/engine/invariant/doc.go` and `grep -n "§19" internal/engine/invariant/doc.go` all match.
- **AC-18.** Each of the four seeded invariants documents, in a comment, exactly what "balance" means for that stock (the formula/accounting identity being checked), so a later module author knows precisely what their registration must satisfy. Check: `grep -n "balance\|conservation" internal/engine/invariant/people.go internal/engine/invariant/money.go internal/engine/invariant/goods.go internal/engine/invariant/vehicle.go` (or equivalent filenames) each find a doc comment.

## Out of scope

- The actual production/consumption/transaction logic of any stock (finance ledger, JIT logistics, traffic vehicle spawning) — those are their owning modules' (Sprint 4/5+) jobs; this item only checks conservation of whatever numbers those modules report.
- F12 Info Panel rendering of invariant status — that is a UI-layer concern consuming `foundation.errors`'/this package's output.
- Historical/trend analysis of invariant violations over a play session — this item reports per-tick violations, not a dashboard.
- Auto-repair or self-healing of a detected imbalance — a violation is reported/asserted, never silently corrected (that would itself violate GR#1's "never silently drift").

## Escalations

- **Assumption flagged (per BA instructions §3).** This item depends on `MOD-012` (engine.core, Sprint 1). Because `engine.invariant` is seq 270 within Sprint 2 while `engine.core` is a Sprint 1 walking-skeleton deliverable, AC-7's "wired into the pipeline" check should be straightforward by dispatch — but if engine.core's phase-registration API shape differs from the `PhaseRegistrar` placeholder assumed here, the owning BA must refresh AC-7 at dispatch.
- **For Bill.** Goods and money conservation (AC-4/AC-5) are written generically because the concrete ledger/stock types they check don't exist until `engine.finance`/`engine.market`/`engine.logistics` (Sprint 4/6). At Sprint 2 build time this item can only prove the *framework* conserves a synthetic/fake stock correctly — full money/goods conservation over real gameplay is necessarily re-verified when those modules land and register (sprint plan S4 exit gate explicitly re-tests "money conservation invariant green over 120 headless months"). Flagging so Bill's Sprint 2 review doesn't expect full-city conservation proof this early — only the mechanism's correctness.
