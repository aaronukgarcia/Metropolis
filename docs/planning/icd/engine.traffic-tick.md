# ICD: engine.traffic → Composition Root (daily AdvanceTick wiring)

> Interface Control Document per `docs/planning/icd/TEMPLATE.md`. THIN ICD (FEAT-206): the seams already exist — this documents cadence, ordering and failure modes for wiring `TrafficAPI` into the composition root's live tick. Authored BEFORE Bev builds the wiring.

---

## 1. Identity

- **GUID:** `f048ef78-9446-4fdb-845e-500fda1f2743` *(`engine.traffic`'s own module GUID — the dedicated `feat.compositionroot → engine.traffic` edge is NOT yet registered in code.json; this stands in until GR#25 registration lands, see §12 Open Decision 1)*
- **Name:** `traffic.tick.advancetick`
- **Owning module (mkey):** `engine.traffic`
- **code.json edge ref(s):** NONE YET — `feat.compositionroot` (`code.json` line ~3496, outbound `calls[]`) does not list `engine.traffic` today; the composition root cannot import `internal/engine/traffic` until that edge is registered (GR#25). `engine.traffic`'s own registered inbound/outbound edges are live: inbound `TrafficAPI` (`df65414a-b7cd-4003-b25d-0772bf9049d5`), outbound to `engine.roads` (`6510e884-5a50-4b50-86c1-78a6329b26d0`), `int.solver`, `engine.citizens`, `engine.world`, `engine.invariant`.

---

## 2. Purpose

`engine.traffic` (MOD-023) exposes `TrafficAPI.AdvanceTick`, which resets the daily demand map (`t.demands`) so per-day trip demand does not accumulate monotonically. Today the composition root never constructs a `*TrafficAPI`, so that reset is dead code and the unbounded-demand defect the reset exists to prevent stays live in the running game (FEAT-206's BOW description, from MOD-023 r2). This integration constructs `TrafficAPI` inside `compose.Wire`, registers a daily phase hook that calls `AdvanceTick` once per simulation day at the day boundary, and hands the one composed instance to the demand-side consumers (`engine.shopping`, `engine.dispatch`) through their `SetTraffic` seams so all consumers share the same composed traffic state.

---

## 3. Inputs

| Source module | Shard-state read | Type |
|---|---|---|
| `engine.traffic` (`TrafficAPI`) | The daily demand map (`t.demands map[uint64]int64`) — read and reset entirely inside `AdvanceTick`; compose only calls the exported method, never reaches into `demands`/`links`/`routeCache` directly | `*traffic.TrafficAPI` (opaque handle; constructed in `Wire` via `traffic.New()` + optional `LoadConfig(dir)` + optional `SetRoads(r)`) |
| `engine.compose` (`simState`) | The current sim day boundary — read only to decide the daily-tick phase slot, not consumed by `AdvanceTick` itself (it takes only a correlation id) | `core.PhaseKind` (`PhaseDailyTick`) |
| `engine.compose` (`simState`) | `cid` (correlation id string) — passed through to `AdvanceTick` for GR#1 traceability | `string` |

Compose's call is command-only and parameterless beyond the correlation id: `traffic.AdvanceTick(cid) error`. `New()` returns a usable coarse-approximation `TrafficAPI` with a nil `roads` field (the coarse v/c-multiplier path works without roads; `LinkTravelTime` guards `if t.roads != nil`), so roads construction is **not** a prerequisite of this integration (§12 Open Decision 2).

---

## 4. Outputs

| Effect | Target stock/edge | Type |
|---|---|---|
| Daily demand map reset to empty | `TrafficAPI.demands` — internal to traffic, not a compose-owned stock; no conservation-accumulator effect (demand is not population/money/goods/vehicles) | n/a (the `StockVehicles`/`StockGoods` invariant readings remain registered-zero in baseline one, unchanged) |
| Daily SUE assignment result (only if/when `DailyAssignment` is also driven) | `TrafficAPI` link flows / route cache — internal to traffic; `Converged bool` is a return value, never an error, never silently "converged" when capped (AC-16b) | `traffic.AssignmentResult` |

No `simState` ledger (`peopleDelta`/`moneyDelta`) is touched by this integration. The demand reset's only observable effect is that `traffic`'s own demand multiplier stops growing without bound.

---

## 5. Update Class

**T1** — the daily demand reset is not named in the Integration Engine proposal's critical-tier set (population, money, conservation), and it feeds no conservation accumulator (§4). It is per-day bookkeeping: a one-tick skew in exactly when the reset lands relative to a demand-generating hook degrades the commute-time approximation, but cannot corrupt any invariant. Batchable, not critical.

---

## 6. Shard Scope

**Single-shard** (`SingleShard() == true`, shard 0 only — the BUG-269 contract). `AdvanceTick` is a single map reallocation with no per-shard structure; the hook's `RunShard` returns `(nil, nil)` for every shard except 0 and `ApplyEffect` runs the reset at the barrier, exactly the shape of `coldPassHook`/`buildHook` already in `compose.go`.

---

## 7. Determinism Guarantee

`AdvanceTick` performs no stochastic draw — it replaces the demand map with an empty one, a pure state reset. The `DailyAssignment` path that *fills* the map is deterministic per traffic's own AC-3/AC-16/AC-18 (`solver.Request.Seed` is a fixed literal, iteration order is sorted by link id, convergence is a fixed tolerance/cap from `data/traffic_balance.json`); this integration only *resets* the map at the day boundary and introduces no new ordering. The ordering constraint this ICD adds is a slice order in `registrationOrder`, never a map range. **No wall-clock time is read anywhere in this integration** — the "day" is `core.Engine`'s internal `PhaseDailyTick` counter, and `AdvanceTick` takes only a correlation id.

---

## 8. Error / Registry Codes

`engine.traffic` owns registry range `MET-G4500`–`MET-G4599` (`data/errors.json` G4500 block; codes seen in `api.go`/`sue.go`). Codes this integration can surface:

- **`MET-G4599` (`ErrCopiedValue`)** — a method called on a struct-copied `*TrafficAPI`; cannot occur through compose's single-owner `simState` field, but is the live defensive path `AdvanceTick` checks first.
- **`MET-G4502` (`ErrInvalidInput`)** — `LoadConfig` on a missing/malformed `data/traffic.json`, or `DailyAssignment` wrapping a solver error; `AdvanceTick` itself never returns this.
- **`MET-G4501` (`ErrUnknownCitizen`)** / **`MET-G4503` (`ErrNoRouteFound`)** — query-surface codes; not reachable from the `AdvanceTick` wiring itself.
- **`ErrModuleFailed`** (compose's own registry code) — the natural code for this integration's hook to raise if `AdvanceTick` returns an unexpected error, mirroring `coldPassHook`/`buildHook`'s existing pattern.

---

## 9. Resilience Behaviour

In-process, always-connected — the degenerate `integration.LocalReconnectHooks` case. `AdvanceTick`'s only failure mode is the copied-handle rejection (`MET-G4599`), deterministic given identical inputs, so a bare retry without a code fix fails identically; the correct "retry" is "fix the caller bug and re-run the tick." No queue, no catch-up: a crash mid-day is recovered by the existing checkpoint/replay path (`internal/foundation/integration/recovery.go`), not by any state this integration owns.

---

## 10. Monitoring Signals

**Status:** derived from whether `AdvanceTick` returns an error this tick (up) vs propagates one (degraded — logged via the registry). **Throughput:** the demand-map size immediately before the reset (a one-line probe the hook can record) is the natural "was demand accumulating before the reset" signal — the exact symptom FEAT-206 exists to kill. **Queue depth:** not applicable (no queue). **Peak load:** `core.WithPhaseObserver`'s existing per-phase timing already covers the daily-tick phase cost; no new instrument needed.

---

## 11. Required Tests

- **Ordering / day-boundary:** a compose-level test that drives N day-ticks with a demand-generating hook present, then asserts `AdvanceTick` ran on every day-tick and the demand map is empty at each day boundary (the reset fires after same-day generation, before next-day generation).
- **Unbounded-demand regression (the FEAT-206 defect):** a test that injects demand over many day-ticks and asserts the demand map (or the `demandMultiplier` it drives) does **not** grow monotonically without bound — the original "AdvanceTick is dead code" failure mode.
- **Determinism:** two identical `driveTicks` runs (same seed) produce byte-identical demand reset cadence and identical `DailyAssignment` outputs (where driven).
- **AC-coverage:** the FEAT-206 acceptance criteria file must exist and pass `tools/plan/spec-lint.js` clean before the Go wiring is built (per the template's GR#25 standing rule).

---

## 12. Change Control

Additive-only: a later ICD revision may ADD an Input/Output without a version bump provided no existing field's type or semantics changes; any REMOVAL or semantic change to an existing Input/Output/Update-Class/Determinism guarantee requires a new version appended below plus a fresh Destructive-verdict round (GR#23) on the affected integration code.

**Open decisions flagged by this ICD (unresolved — surfaced for Bill/Aaron):**

1. **The `feat.compositionroot → engine.traffic` code.json edge does not exist.** It must be registered (GR#25) via master-plan before `compose.Wire` imports `internal/engine/traffic`, and this ICD's §1 GUID swapped to the real edge GUID once it lands. This is the same shape FEAT-206's own BOW description names ("register feat.compositionroot->engine.traffic edge in master-plan then regenerate").
2. **Roads construction is out of scope.** `TrafficAPI.SetRoads` is optional for the coarse path (`LinkTravelTime`/`CommuteHours` guard `t.roads != nil`), so this ICD wires traffic without roads; when the road-graph path is enabled, compose must additionally construct `engine.roads` and call `SetRoads` — a separate wiring item, not folded into FEAT-206.
3. **Day-boundary reset semantics are Ben's, not this ICD's.** FEAT-206's BOW description records the build is "blocked until Ben lands the day-boundary reset semantics + BPR guards"; this ICD documents the target ordering (AdvanceTick last among demand-generating hooks in `PhaseDailyTick`, before the invariant) but does not fix traffic's internal reset semantics.
4. **Unregistered traffic↔education and traffic↔leisure edges (flag, not this ICD's build).** `internal/engine/traffic/api.go` imports `internal/engine/education` (for `education.TripDemand`) and `internal/engine/leisure` (for `leisure.TripDemand`/`leisure.Category`) as concrete imports, but `engine.traffic`'s `code.json` `outbound.calls[]` lists only `engine.world`, `int.solver`, `engine.citizens`, `engine.roads`, `engine.invariant` — neither `engine.education` nor `engine.leisure` is registered. That is a live GR#25 gap in the traffic module itself that predates this wiring; it must be closed before the demand-generating halves of those edges are exercised in compose.

| Version | Date | Change |
|---|---|---|
| v1 | 2026-08-19 | Initial ICD (FEAT-206) |
