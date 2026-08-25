# ICD: engine.dispatch → Composition Root (daily tick wiring)

> Interface Control Document per `docs/planning/icd/TEMPLATE.md`. THIN ICD STUB (FEAT-192 / Tier D): `engine.dispatch` is registered in `code.json` (MOD-040, seq 610) but has **zero** files on `origin/main` — the only engine-layer module in that state. (A local uncommitted draft `internal/engine/dispatch/{api.go,api_test.go,doc.go}` exists on disk but nothing is committed.) This stub documents the inbound contract, seams/cadence/failure modes, and the GR#25 edge list so the dev builds the module against it.

---

## 1. Identity

- **GUID:** `351b5c05-1b42-4438-b495-c0aaf6855194` *(`engine.dispatch`'s own module GUID — the dedicated `feat.compositionroot → engine.dispatch` edge is NOT yet registered in code.json; this stands in until GR#25 registration lands, §12 OD-1)*
- **Name:** `dispatch.tick.wiring`
- **Owning module (mkey):** `engine.dispatch`
- **code.json edge ref(s):** outbound edges are live — `engine.traffic` (`f048ef78-9446-4fdb-845e-500fda1f2743` / inbound `df65414a-b7cd-4003-b25d-0772bf9049d5`), `engine.services` (`ab443390-0a8a-459a-ae5a-4b8a35308751` / `3e1f24aa-a539-447c-9e63-874fa58f3f9e`), `engine.world` (`4c1cd0b8-7112-4c20-833c-bba4368c6b63` / `2d8855b8-f4f0-43a3-a179-67accca83115`), `engine.projections` (`56d13d82-76bd-4919-8b73-b796b4e37bf5` / `4d6f5619-e6e2-4a10-9f77-71192d1299c4`). Inbound `DispatchAPI` (`ffb9d07e-1f14-4cfa-8573-192efaca1e3f`) already has registered consumers `engine.airunits` (`bba25b12-a2ef-404a-96b4-de6dc739ba49`), `engine.chemicals` (`66c57025-8e56-45f2-a371-7b6aba74c97f`), `ui.screen.services` (`96581f8a-77b4-449e-a592-3a7a3ea149e5`).

---

## 2. Purpose

`engine.dispatch` (MOD-040, §26) is the unified emergency & care dispatch module. Today nothing constructs a `*DispatchAPI` in the live tick, so incidents are never submitted or assigned, response times never degrade with congestion, the shared nursing/staffing pool never shortens hospital wait lists, and the module's fleet-conservation identity — `TotalUnits == Available + EnRoute + OnScene + OutOfService`, exact at every tick — is exercised only inside unit tests, never in the running game. This integration constructs `DispatchAPI` in `compose.Wire`, registers a daily phase hook that advances the incident/unit state, and hands the one composed instance to its four registered dependencies via the `Set*` seams (`SetTraffic`, `SetWorld`, `SetServices`, `SetProjections`) so emergency outcomes respond to live traffic/congestion and capacity.

---

## 3. Inputs

| Source module | Shard-state read | Type |
|---|---|---|
| `engine.traffic` (`TrafficAPI`) | `CommuteMinutes(unitID, cid)` — congestion-adjusted travel time used to pick the nearest available unit (blue-light multiplier applied on top) | `float64` (via `traffic.TrafficAPI`) |
| `engine.services` (`ServicesAPI`) | `StaffingAllocations("nursing")` — the shared staffing pool that shortens `WaitingList` and raises `ElderCareQuality` | `[]services.Allocation` |
| `engine.world` (`WorldAPI`) | `CellAt(...)` — validates incident cells and feeds the fire-spread density term | `world.WorldAPI` |
| `engine.projections` (`ProjectionsAPI`) | `RegisterCurveProvider("engine.dispatch.response_time", ...)` — registers the response-time curve for the projections dashboard | `projections.ProjectionsAPI` |
| `engine.compose` (`simState`) | `cid` (correlation id) passed through to every dispatch call for GR#1 traceability | `string` |

Construction is command-only beyond the correlation id: `dispatch.New()` + optional `LoadConfig(dir)` (reads `data/dispatch.json`) + the four `Set*` seams. The draft `api.go` already mirrors `spawnHook`'s ownership pattern — compose calls exported methods (`SubmitIncident`, `ResolveOutcome`, `WaitingList`, …), never reaches into dispatch's internal `available`/`enRoute`/`onScene`/`outOfService` buckets.

---

## 4. Outputs

| Effect | Target stock/edge | Type |
|---|---|---|
| Incident resolution outcome (`100 - delay`, floored at 0) | `DispatchAPI.incidents` — internal to dispatch; not a sim-level conservation accumulator | `float64` (returned by `ResolveOutcome`) |
| Fire-spread cell loss (`delay/10` + density term) | `DispatchAPI` state; reported, not written to `simState` | `int` (returned by `FireSpread`) |
| Fleet conservation identity | `DispatchAPI` buckets (`totalUnits`/`available`/`enRoute`/`onScene`/`outOfService`) — internal to dispatch; audited by `AuditFleet(unitType)` | `(total, avail, enRoute, onScene, outOfService int)` |
| Response-time curve | `engine.projections` curve `"engine.dispatch.response_time"` (registered in `SetProjections`) | `projections.CurveProviderFunc` |

No `simState` ledger (`peopleDelta`/`moneyDelta`) is touched. The dispatch fleet identity is an internal conservation identity (GR#21-adjacent) but is **not** the sim-level people/money/conservation invariant — §5 classes it accordingly.

---

## 5. Update Class

**T1** — batchable. Emergency response is not in the Integration Engine proposal's critical every-tick tier (population, money, conservation), and it feeds no sim-level conservation accumulator (§4). It is a cadence service whose incident queue absorbs bursts; a one-tick skew in exactly when an incident is assigned degrades the response-time approximation (which is itself a curve on the projections dashboard), but cannot corrupt any invariant. Batchable, not critical, and not coalescible telemetry (a dropped incident is authoritative game state, never latest-wins).

---

## 6. Shard Scope

**Single-shard** (`SingleShard() == true`, shard 0 — the BUG-269 contract). Dispatch state is a single mutex-guarded map-backed fleet + incident set with no per-shard structure; the daily hook's `RunShard` returns `(nil, nil)` for every shard except 0 and applies at the barrier, the same shape as `coldPassHook`/`buildHook` in `compose.go`. The deterministic nearest-unit selection already sorts candidate ids ascending (`sort.Slice`), so shard fan-out is neither needed nor safe here.

---

## 7. Determinism Guarantee

`assignNearestAvailable` gathers candidate units by type and sorts ascending by unit id (deterministic map iteration via `sort.Slice`, AC-15), then picks the minimum travel time — `traffic.CommuteMinutes(unitID, cid)` (deterministic, congestion-derived) multiplied by the blue-light multiplier, or a fixed `5.0` for air-ambulance (road-immune, AC-7). Incident ids and unit ids are sequential counters, not wall-clock. **No wall-clock time is read anywhere in this integration** — "daily" is `core.Engine`'s internal `PhaseDailyTick` counter, and the response-time/waiting-list figures derive from logical sim state (delay, staffing allocations), never `time.Now()`.

---

## 8. Error / Registry Codes

The draft `api.go` claims registry range **`MET-G5000`–`MET-G5099`** (`MET-G5001` `ErrInvalidCell`, `MET-G5002` `ErrUnknownIncident`, `MET-G5003` `ErrInvalidUnitType`, `MET-G5004` `ErrDoubleAssign`, `MET-G5005` `ErrInvalidInput`, `MET-G5099` `ErrCopiedValue`). These are **not yet committed to `data/errors.json`** — the range claim must land there when the module is built (GR#7), and the copyguard `checkNotCopied` path (`MET-G5099`) is the defensive code every `Set*`/method runs first. `compose` raises its own `ErrModuleFailed` if the daily hook returns an unexpected error, mirroring `coldPassHook`/`buildHook`.

---

## 9. Resilience Behaviour

In-process, always-connected — the degenerate `integration.LocalReconnectHooks` case. The only failure modes are the copied-handle rejection (`MET-G5099`, deterministic) or a propagated dependency error (traffic/services/world/projections), both deterministic given identical inputs, so a bare retry fails identically; the correct "retry" is "fix the caller bug and re-run the tick." No queue, no catch-up: a crash mid-day is recovered by the existing checkpoint/replay path (`internal/foundation/integration/recovery.go`), not by any state this integration owns. If dispatch is later location-transparently offloaded (not planned — see §5), it would use the proposal's `DefaultBackoff`/`DefaultMaxRetries` vocabulary unchanged.

---

## 10. Monitoring Signals

**Status:** derived from whether the daily hook returns an error this tick (up) vs propagates one (degraded — logged via the registry, GR#1/GR#17). **Throughput / queue depth:** pending incident count (the natural "is dispatch keeping up with emergencies" signal) and `AuditFleet`'s four-bucket split. **Peak load:** the daily-tick wall-clock cost via the existing `PhaseObserver` timing. The response-time curve already flows to `engine.projections`, so the increment-5 dashboard can show dispatch on the same timeline as traffic.

---

## 11. Required Tests

- **Fleet conservation identity:** a test that mutates the fleet across many ticks and asserts `TotalUnits == Available + EnRoute + OnScene + OutOfService` at every tick (the doc.go identity).
- **Determinism:** two identical `driveTicks` runs (same seed) produce byte-identical incident assignments and identical response-time curves.
- **Contract-conformance:** an incident submitted → assigned → resolved walks the exact Inputs/Outputs contract above, with `AuditFleet` matching the expected bucket movement.
- **AC-coverage:** the MOD-040 acceptance criteria must exist and pass `tools/plan/spec-lint.js` clean before the Go wiring is built (GR#25 standing rule).

---

## 12. Change Control

Additive-only: a later revision may ADD an Input/Output without a version bump provided no existing field's type or semantics changes; any REMOVAL or semantic change to an existing Input/Output/Update-Class/Determinism guarantee requires a new version appended below plus a fresh Destructive-verdict round (GR#23) on the affected code.

**GR#25 edge list (outbound edges this module needs — registered vs new):**

| Edge | Status |
|---|---|
| `engine.dispatch → engine.traffic` | ALREADY in code.json (`df65414a-b7cd-4003-b25d-0772bf9049d5`) |
| `engine.dispatch → engine.services` | ALREADY in code.json (`3e1f24aa-a539-447c-9e63-874fa58f3f9e`) |
| `engine.dispatch → engine.world` | ALREADY in code.json (`2d8855b8-f4f0-43a3-a179-67accca83115`) |
| `engine.dispatch → engine.projections` | ALREADY in code.json (`4d6f5619-e6e2-4a10-9f77-71192d1299c4`) |
| `feat.compositionroot → engine.dispatch` | **NEW** — not in `feat.compositionroot.outbound.calls[]` today (verified); must be registered before `compose.Wire` imports `internal/engine/dispatch` |
| `engine.dispatch → engine.invariant` | **NEW** — the draft `doc.go` flags "the engine.invariant edge remains absent (pending collaborations gate configuration)" |

**Open decisions flagged by this ICD (unresolved — surfaced for Bill/Aaron):**

1. **The `feat.compositionroot → engine.dispatch` edge does not exist** (GR#25). It must be registered via master-plan before `compose.Wire` imports `internal/engine/dispatch`, and this ICD's §1 GUID swapped to the real edge GUID once it lands. (Note: `engine.traffic` was added to `feat.compositionroot`'s outbound since the traffic ICD's OD-1 — dispatch has not been.)
2. **The `engine.dispatch → engine.invariant` edge** is flagged absent in the draft `doc.go`. If the fleet-conservation identity is to be enforced by `engine.invariant`'s accumulator machinery, that outbound edge must be registered first; otherwise the identity stays an internal audit (`AuditFleet`), not a sim-level invariant.
3. **Uncommitted draft code.** `internal/engine/dispatch/{api.go,api_test.go,doc.go}` exist only on the local working tree — not on `origin/main`, and not under any Destructive verdict (GR#23). The module must be committed with a recorded verdict before this wiring is built against it; the draft's `MET-G5000` range must also land in `data/errors.json`.

| Version | Date | Change |
|---|---|---|
| v1 | 2026-08-20 | Initial ICD (FEAT-192 Tier D stub) |
