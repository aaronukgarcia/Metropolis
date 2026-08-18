# ICD: Citizens Cold Pass → Composition Root (VitalEvents wiring)

**Status:** DRAFT — first real ICD authored against `docs/planning/icd/TEMPLATE.md` (Increment 4 of the Integration Engine, FEAT-190). Covers FEAT-169: wiring `engine.citizens`' cold pass (`AdvanceDayTick`/`AdvanceMonth`, including mortality and the new FEAT-160 fertility) into the composition root's live tick, so births and deaths actually happen in `cmd/metropolis`, not only in citizens' own unit tests. Pre-staged ahead of the dev build per the Integration Engine proposal's build order (§6: "THEN FEAT-169... as the first two real integrations on the new substrate").

---

## 1. Identity

- **GUID:** `99e0d1f5-0214-4b06-bcde-caba0b1e44ad` — `engine.citizens`' own registered `code.json` module GUID. Standing in as this integration's identity GUID because the dedicated citizens↔compose edge is **not yet registered** in `code.json` (verified: `code.json` has no module entry whose `path` contains `compose` at all today). See §12 Open Decisions — this GUID must be swapped for the real edge GUID once GR#25 registration lands, before the Go wiring is built.
- **Name:** `citizens.coldpass.vitalevents`
- **Owning module (mkey):** `engine.citizens`
- **code.json edge ref(s):** NONE YET. `engine.citizens`' `inbound.consumers` list (checked directly in `code.json`) currently names `engine.attract`, `engine.census`, `engine.coastal`, `engine.crime`, `engine.defence`, `engine.education`, `engine.extcommute`, `engine.firms`, `engine.households`, `engine.leisure`, `engine.services`, `engine.social`, `engine.staffing`, `engine.traffic`, `engine.wellbeing`, `ui.screen.demo`, `ui.screen.map` — no composition-root consumer at all. FEAT-169's own BOW description already flags this: "registering any new citizens↔compose edge goes through master-plan/code.json first" (GR#25). This ICD does not authorize skipping that step; it documents the target shape the eventual edge registration should match.

---

## 2. Purpose

Today (2026-08-18) nothing outside `internal/engine/citizens` calls `AdvanceDayTick`/`AdvanceMonth` — mortality (already built) and fertility (FEAT-160, newly built) both run only inside citizens' own unit tests, never in the live tick `cmd/metropolis` actually drives. `internal/engine/compose/compose.go`'s only existing citizens integration is `spawnHook`, which births citizens purely as a scalar migration/seed count (`spawnCitizens`) with no connection to the cold pass at all. This integration wires `AdvanceDayTick` into the composition root's per-tick phase list and feeds its `VitalEvents` births/deaths totals into the existing `peopleDelta` conservation accumulator, so population responds to natural increase and mortality — not migration admits alone — while the game is actually running. This is Aaron's "childbearing on day one" enabler for the Baseline One milestone (FEAT-083): the loop must RUN, not just compile.

---

## 3. Inputs

| Source module | Shard-state read | Type |
|---|---|---|
| `engine.citizens` (`CitizensAPI`) | The cold-pass amortised daily shard schedule (`ColdPassSchedule(dayTick)`, 1/30th of the 256 cold shards per day-tick) — read and mutated entirely INSIDE `AdvanceDayTick`; compose never reads a shard directly, only calls the exported method (AC-1b: `CitizensAPI` is the only way a consumer reaches citizen state) | `*citizens.CitizensAPI` (opaque handle; `simState.citizens`, already held by compose today for `spawnHook`/`TotalPopulation`/`PopulationHash`) |
| `engine.compose` (`simState`) | The current sim clock (`clock.Month()`) — read only to attribute the correct calendar month to correlation-id/logging context around the tick call, not consumed by `AdvanceDayTick` itself (citizens derives its own month from internal state, `c.month`) | `core.Clock` |
| `engine.compose` (`simState`) | `cid` (the correlation id string) — passed through to every citizens call for GR#1 error-trapping traceability | `string` |

Compose's call into citizens is command-only and parameterless beyond the correlation id: `citizens.AdvanceDayTick(cid) error`. No shard index, no explicit state blob crosses the boundary — this mirrors `spawnHook`'s existing pattern of never reaching into `cold`/`hot` directly.

---

## 4. Outputs

| Effect | Target stock/edge | Type |
|---|---|---|
| Births tallied over the most recently completed calendar month | `simState.peopleDelta` (added, via `num.SatAdd`) — the same `invariant.PeopleInvariant` `TrackedDelta` accumulator `spawnHook.ApplyEffect` already feeds for migration admits | `int` |
| Deaths tallied over the most recently completed calendar month | `simState.peopleDelta` (subtracted, via `num.SatAdd` with a negated delta) | `int` |
| Cold-pass side effects: mortality removals, fertility births into the cold store, education/job/health/satisfaction drift for the scheduled shards this day-tick | `CitizensAPI`'s own cold/hot stores — internal to citizens, already covered by its own `PopulationHash` (AC-17) determinism fingerprint; not a compose-owned stock | n/a |

Source method: `CitizensAPI.VitalEvents(correlationID string) (births, deaths int)` — returns the most recently COMPLETED calendar month's totals; a mid-month call returns the PREVIOUS completed month's figures, never a half-counted in-progress number (`registry.go`'s doc comment on `VitalEvents`). The integration's new per-tick hook must call `AdvanceDayTick` every day-tick and only pull `VitalEvents` (and fold it into `peopleDelta`) at the month boundary (`c.dayTick == DaysPerMonth` internally to citizens; from compose's side, this is best detected by comparing `VitalEvents`' returned totals against the previous tick's, or by exposing a month-boundary signal — an open build-time decision for the dev, not fixed by this ICD, since it does not change any Input/Output type or semantics).

---

## 5. Update Class

**T0 (critical).** The proposal's own T0 definition names population explicitly: "population, money, conservation — small, must run every tick, never queued past one tick" (§3). `AdvanceDayTick` already runs unconditionally once per day-tick inside `registry.go`; the births/deaths delta it produces must land in `peopleDelta` the SAME tick it is computed. Queuing it past the current tick would let `PeopleInvariant`'s Opening/Closing reconciliation observe a population change before the tracked delta accounts for it — a conservation violation, not merely a staleness issue.

---

## 6. Shard Scope

**All shards, internally to citizens; single-call from compose's point of view.** Unlike `spawnHook` (compose's existing citizens integration, which is genuinely single-shard — shard 0 only, since a migrant/seed spawn is a scalar count with no per-shard structure), the cold pass's `AdvanceDayTick` already fans its own internal parallel mortality/education/job pass and sequential fertility pass across `ColdPassSchedule(dayTick)`'s scheduled shards (1/30th of 256 per day-tick) entirely inside `citizens.CitizensAPI` — built and tested (AC-6/AC-7/AC-17) prior to this integration, not re-implemented by it. From compose's perspective this integration is one opaque call per tick with no shard parameter — so if/when expressed as an `integration.Integration[T,M]` (the Increment 1 contract), it is `SingleShard() == true` at the COMPOSE-INTEGRATION layer (one call per tick, not once per shard), while citizens' own internal `det`-shard fan-out remains entirely unaffected and invisible to compose.

---

## 7. Determinism Guarantee

Every stochastic draw inside the cold pass is a counter-based stream keyed `hash(worldSeed, id-or-householdID, month, purpose)`: mortality keys `hash(seed, citizenID, month, "mortality")`; fertility keys `hash(seed, householdID, month, "fertility")` (`fertility.go`'s `CoupleBirth`) — fully determined by the world seed and the sim's own logical month counter, never by wall-clock time or goroutine scheduling order. The parallel mortality/education/job pass merges its per-shard `passTotals` in strict ascending shard order (`registry.go`'s `AdvanceDayTick`: `for _, t := range results { tot = tot.add(t) }` over `runShardsParallel`'s ordered results); the fertility pass then runs strictly SEQUENTIALLY after the parallel pass fully completes, specifically because a couple's eligibility check reads the partner's age/household data across a shard boundary and would otherwise race another shard's concurrent mortality mutation (`applyFertilityLocked`'s doc comment). Compose's own fold of `VitalEvents`' `(births, deaths)` into `peopleDelta` is a single-goroutine, barrier-time operation (mirrors `spawnHook.ApplyEffect`'s existing pattern) with no ordering ambiguity of its own. **No wall-clock time is read anywhere in this integration** (AC-20): the day/month counters are internal sim state (`c.dayTick`/`c.month`), `VitalEvents`' "most recently completed month" semantics are derived purely from those counters, and compose's own tick driver never consults a system clock either.

---

## 8. Error / Registry Codes

`engine.citizens` owns registry range `MOD-018` / `MET-G000`–`MET-G099` (a second "engine" letter-block — the original `E000`–`E999` range was fully exhausted by eleven earlier engine modules before citizens landed; `errors.go`'s range-claim note). Codes this integration can surface:

- **`MET-G008` (`ErrFertilityDataInvalid`)** — malformed `data/fertility.json`; load-time only (`NewCitizensAPI` construction), so it would prevent the composition root from ever constructing citizens in the first place, before this integration's first tick runs.
- **`MET-G009` (`ErrFertilityBirthRejected`)** — a fertility-driven birth's constructed `Citizen` record failed `ValidateCitizen`. Logged loudly (GR#1) and the birth is skipped for that couple that month; `AdvanceDayTick` itself does not error out, so this integration's per-tick call does not need special handling beyond normal error-log surfacing.
- **`MET-G004` (`ErrAPICopied`)** — `AdvanceDayTick`/`VitalEvents` called on a copied `*CitizensAPI` handle (SEC-020 family). Should never occur given compose's existing single-owner `simState.citizens` field, but is a live defensive path every citizens method checks first.
- **`ErrModuleFailed`** (compose's own registry code, already used by `spawnHook.ApplyEffect` for an unexpected `spawnCitizens` failure) — the natural code for this integration's own hook to raise if `AdvanceDayTick` returns an unexpected error, mirroring the existing pattern rather than inventing a new compose-side code.

---

## 9. Resilience Behaviour

In-process, always-connected today — the degenerate case `integration.LocalReconnectHooks` already models (`Authenticate`/`Lookup` are no-ops). `AdvanceDayTick`'s only failure modes today are the copied-handle rejection (`MET-G004`) or a propagated internal citizens error, both deterministic given identical inputs — a bare retry without a code fix would fail identically every time, so the correct "retry" for this integration is "fix the caller bug and re-run the tick," not a logical-backoff loop. Retry policy: NOT WIRED into `integration.Connection`/`Attempt` for this first build — T0's own contract ("never queued past one tick," §3) rules out any remote-dispatch latency this proposal's location-transparent executor exists to absorb, so a `Connection` wrapper adds no value here today. If a future change offloads any part of the cold pass to a `WorkerPool` (unlikely for T0 population work specifically, per the proposal's own §4 guidance that T1 heavy classes go first), it would adopt `integration.DefaultBackoff`/`DefaultMaxRetries` and `Connection.Attempt`/`Reconnect` exactly as Increment 3 defines them, with `Queue == nil` (a T0 command that cannot be enqueued is `ErrT0QueueExhausted` per `queue.go`, never silently spilled to disk). Catch-up: none needed while in-process; a crash mid-tick is recovered by the EXISTING checkpoint/replay path (`recovery.go`), not by any state this integration owns — citizens' own cold store is entirely checkpoint-serializable already (it is the persistent record).

---

## 10. Monitoring Signals

**Status:** derived from whether `AdvanceDayTick`'s per-tick call returns an error (up) vs propagates one (degraded — logged via the registry per GR#1/GR#17). **Throughput:** `VitalEvents`' `(births, deaths)` pair is a natural per-month rate signal — pipe it through `core.WithPhaseObserver`'s existing per-phase timing hook (proposal §7's "monitoring taps existing hooks") alongside `spawnHook`'s existing spawn-count reporting, so a future dashboard (Increment 5, not yet built) can show citizens' cold-pass births/deaths on the same timeline as migration admits. **Queue depth:** not applicable — T0, no queue (see §9). **Peak load:** the day-tick's wall-clock COST (an operational metric, not a determinism input — never read back into any simulation decision) via the existing `PhaseObserver` timing, watched against the BUG-034 1M-citizen perf gate this pass must stay inside.

---

## 11. Required Tests

- **Determinism equivalence:** `internal/engine/citizens/determinism_test.go`'s existing `PopulationHash` shard/worker-count invariance coverage (AC-17) already proves the cold pass itself is deterministic in isolation; this integration additionally needs a compose-level test asserting two identical `driveTicks` runs (same seed) produce byte-identical `peopleDelta` accumulation AND `PopulationHash` after N ticks with births/deaths live in the loop.
- **Resilience/disconnect-catch-up:** N/A for this first build per §9 (no `Connection`/queue wrapping — T0, in-process). If a later revision wraps this integration in `integration.Connection`, `resilience_test.go`'s existing disconnect→retry→catch-up→reconnect determinism-replay pattern applies directly with no new ordering logic needed.
- **Contract-conformance:** a new compose-level test asserting `VitalEvents`' returned `(births, deaths)` for a completed month exactly equals `peopleDelta`'s accumulated change over that same month — the conservation identity this ICD's §4 Outputs table declares — mirroring `invariant/people_test.go`'s existing `PeopleInvariant` Opening/Closing/TrackedDelta identity check.
- **AC-coverage:** the FEAT-169 acceptance criteria file must exist (`docs/planning/acceptance/`, to be authored by the BA per GR#25 alongside the citizens↔compose edge registration — not yet written as of this ICD draft) and pass `tools/plan/spec-lint.js` clean before the Go wiring is built.

---

## 12. Change Control

Additive-only: a later ICD revision may ADD a new Input/Output without a version bump provided no existing field's type or semantics changes; any REMOVAL or semantic change to an existing Input/Output/Update-Class/Determinism guarantee requires a new version appended to the table below, plus a fresh Destructive-verdict round (GR#23) on the affected integration code even if the underlying `engine.citizens`/`engine.compose` code is otherwise untouched.

**Open decisions flagged by this ICD (unresolved — surfaced for Bill/Aaron):**

1. **The citizens↔compose `code.json` edge does not exist yet.** It must be registered (GR#25) before the Go wiring described here is built, and this ICD's §1 GUID must be swapped from `engine.citizens`' own module GUID (the current stand-in) to the real edge GUID once that registration lands.
2. **The child-id namespace seam — RESOLVED 2026-08-18, amended by destructive review.** This section originally (v1) described the seam as two-party: fertility allocating from a `2^62` offset against compose's separate `simState.nextCitizenID` counter. That framing MISSED a real three-party collision: `engine.attract` independently mints admitted-migrant citizen ids from its own high-bit prefix, `migrantIDHighBit` (`migration.go`) — and it, too, was `1<<62`, the SAME value fertility's `fertilityChildIDBase` used. With FEAT-169 wiring both the citizens cold-pass tick and the attract migration hook into the same live composition, a duplicate citizen id (silently aliasing an existing citizen — invisible to `TotalPopulation`'s row-count-based conservation view) was reachable within months of simulated play. An independent destructive-review round caught this and REJECTED the first FEAT-169 build over it.
   **Fix landed:** the id map is now a documented THREE-way disjoint range, by convention (not a shared allocator): compose `[1, attract.MigrantIDBase)`, `engine.attract` migrants `[attract.MigrantIDBase, citizens.FertilityChildIDBase)`, `engine.citizens` fertility children `[citizens.FertilityChildIDBase, ...)` — `fertilityChildIDBase` moved from `1<<62` to `1<<63` (`fertility.go`), and `attract.MigrantIDBase` was newly exported (`migration.go`) so compose could reference it. THREE independent defenses now exist, none a substitute for the others: (a) compose's `spawnCitizens` rejects any mint at or past `attract.MigrantIDBase` (`ErrCitizenIDNamespaceSeam`) — compose's own range guard, checked on every mint including the Wire-time seed population; (b) compose's `Wire` asserts `citizens.FertilityChildIDBase >= 2*attract.MigrantIDBase` at construction time (`ErrIDNamespaceRangesOverlap`) — the boundary BETWEEN attract's and citizens' ranges, which (a) cannot see; (c) `engine.citizens` independently rejects a `LifeEventBirth` whose id already exists in its own cold or hot store (`ErrDuplicateCitizenID`) — defense in depth, and the only one of the three that would catch an ACTUAL runtime collision rather than a range check on constants. Documented identically in `citizens/doc.go`'s "Live-tick wiring" section and `compose/doc.go`'s "Citizen id namespace map" section. Still not a single shared allocator across all three packages — that unification remains a larger refactor, flagged as a follow-up, not done here.

| Version | Date | Change |
|---|---|---|
| v1 | 2026-08-18 | Initial draft, authored against `TEMPLATE.md` (FEAT-190 Increment 4), pre-staging FEAT-169 |
| v1.1 | 2026-08-18 | §12 open decision 2 corrected: the child-id namespace seam is a THREE-party collision (compose/attract/citizens), not two — an independent destructive-review round rejected the first FEAT-169 build over a real `engine.attract`↔`engine.citizens` id-range collision (`migrantIDHighBit` and `fertilityChildIDBase` both `1<<62`) this ICD's original framing did not surface. See the amended item 2 above for the landed fix. |
