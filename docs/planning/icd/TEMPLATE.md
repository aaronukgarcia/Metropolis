# ICD: <Integration Name>

> **What this is:** an Interface Control Document — the self-describing contract a BA authors BEFORE a dev builds an integration (Integration Engine proposal, `docs/planning/proposals/integration-engine.md` §2 principle 2, §5, §7, §8 increment 4). A dev builds against this document, not against a conversation. Every section below has a short instruction (in italics) followed by a worked **EXAMPLE** — the EXAMPLE block is the running FEAT-169 case (`engine.citizens.AdvanceDayTick`/`AdvanceMonth` cold-pass births+deaths wiring into `compose.go`'s tick), clearly marked, and is not part of the real ICD you are writing. Delete the EXAMPLE blocks (or leave them for reference — `tools/plan/icd-lint.js` only reads the real section content, not fenced example blocks) and fill in every section for real; `icd-lint.js` fails any ICD with an empty required section, an Identity GUID that isn't registered in `code.json`, an unrecognised update class, an unregistered owning-module mkey, or a Determinism Guarantee section missing the no-wall-clock declaration. This file itself (`TEMPLATE.md`) is skipped by the lint.

---

## 1. Identity

*Instruction: the GUID identifying this integration in `code.json` — either an already-registered edge/module GUID, or (if the edge itself is not yet registered, per GR#25) the owning module's own GUID as a stand-in until the edge is registered. Name it, name the module that owns/builds it, and cite every `code.json` edge reference this integration rides on or will need (GR#25: a new cross-module edge must be registered in `code.json` before this ICD's Inputs/Outputs sections may cite it as live).*

- **GUID:** `<uuid>`
- **Name:** `<short.dotted.integration.name>`
- **Owning module (mkey):** `<engine.foo>`
- **code.json edge ref(s):** `<edge GUIDs this rides on, or "NONE YET — pending GR#25 registration, see Open Decisions">`

**EXAMPLE (FEAT-169, engine.citizens cold-pass → compose tick):**
- **GUID:** `99e0d1f5-0214-4b06-bcde-caba0b1e44ad` *(engine.citizens' own module GUID — the dedicated citizens↔compose edge is NOT yet registered in code.json; this stands in until GR#25 registration lands, per the open decision in §12)*
- **Name:** `citizens.coldpass.vitalevents`
- **Owning module (mkey):** `engine.citizens`
- **code.json edge ref(s):** NONE YET — the composition root (`internal/engine/compose`) has no `code.json` module entry at all today (verified: no `path` containing `compose` is registered); a new outbound edge from `engine.citizens` to whatever key `compose` gets registered under must be added before this integration is built (GR#25), not merely wired in Go.

---

## 2. Purpose

*Instruction: one paragraph — what real, player-visible outcome this integration produces, and why it must exist. No implementation detail here; that lives in Inputs/Outputs.*

**EXAMPLE:** Today (2026-08-18) nothing outside `internal/engine/citizens` calls `AdvanceDayTick`/`AdvanceMonth`, so mortality (existing) and fertility (FEAT-160, new) run only inside citizens' own unit tests — never in the live tick `cmd/metropolis` drives. This integration wires the citizens cold pass into the composition root's tick so births and deaths actually happen while the game runs: population responds to natural increase and mortality on day one, not only to migration admits (the only source of population change compose.go currently drives). This is the "childbearing on day one" enabler Aaron named for Baseline One (FEAT-083).

---

## 3. Inputs

*Instruction: the source module, the exact shard-state it reads, and the Go types involved. Be precise about what is READ, not what is computed — computation belongs in the module being integrated, not in this ICD.*

**EXAMPLE:**
| Source module | Shard-state read | Type |
|---|---|---|
| `engine.citizens` (`CitizensAPI`, unexported `cold [256]*ColdShard`) | The citizens cold-pass amortised daily shard schedule (`ColdPassSchedule(dayTick)`, 1/30th of the 256 shards per day-tick) — read-only from compose's point of view; citizens owns and mutates its own cold store internally | `citizens.CitizensAPI` (opaque; compose only calls its exported methods, never reaches into `cold`/`hot` directly — AC-1b) |
| `engine.compose` (`simState`) | The current sim clock (`clock.Month()`), read to attribute the correct calendar month to the tick call | `core.Clock` |

Compose's own inputs into citizens are **command-only** (`AdvanceDayTick(correlationID)` — no shard-state parameter beyond the correlation id; citizens derives everything else from its own internal clock, mirroring `spawnHook`'s existing pattern of calling `st.citizens.TotalPopulation`/`ApplyLifeEventCommand` without ever touching a cold shard directly).

---

## 4. Outputs

*Instruction: the effects this integration produces, and the target stocks/edges (conservation accumulators, invariants, other modules' state) they land in. Every population/money/resource-moving output MUST identify which invariant accumulator it feeds — an output with no named target is not tracked, and an untracked stock mutation is a GR#... invariant violation waiting to happen.*

**EXAMPLE:**
| Effect | Target stock/edge | Type |
|---|---|---|
| Births this completed month | `simState.peopleDelta` (the `invariant.PeopleInvariant` `TrackedDelta` accumulator — the same role migration admits already play, see `compose.go`'s `spawnHook.ApplyEffect`) | `int` (added via `num.SatAdd`) |
| Deaths this completed month | `simState.peopleDelta` (subtracted) | `int` (subtracted via `num.SatAdd` with a negated delta) |
| Cold-pass side effects (mortality removals, fertility births into the cold store, education/job/health drift) | `CitizensAPI`'s own cold/hot stores — internal to citizens, not a compose-owned stock | n/a (already tracked by `PopulationHash`'s AC-17 fingerprint) |

Source: `CitizensAPI.VitalEvents(correlationID) (births, deaths int)` — returns the most recently COMPLETED calendar month's totals (never a half-counted in-progress figure; see `registry.go`'s doc comment on `VitalEvents`).

---

## 5. Update Class

*Instruction: T0 critical / T1 batchable / T2 coalescible (proposal §3), plus a one-sentence justification grounded in the proposal's own class definitions. Must be exactly one of `T0`, `T1`, `T2` (the literal token, so the lint can verify it) somewhere in this section.*

**EXAMPLE:** **T0** — population is explicitly named in the proposal's T0 definition ("population, money, conservation — small, must run every tick, never queued past one tick," §3). `AdvanceDayTick` already runs once per day-tick unconditionally (`registry.go`); the births/deaths delta it produces must be applied to `peopleDelta` the same tick it is computed — queuing it past the current tick would let `PeopleInvariant`'s Opening/Closing reconciliation observe a population change the tracked delta hasn't caught up to yet, a conservation violation (GR#1/GR#21 adjacent).

---

## 6. Shard Scope

*Instruction: single-shard or all-shards, and why. If single-shard, name which shard and confirm the `SingleShard()`/`SingleShardHook` (BUG-269) contract is honoured — real work on any other shard silently loses that work.*

**EXAMPLE:** **All shards** (not single-shard). Unlike `spawnHook` (compose's existing citizens integration, which is deliberately single-shard — shard 0 only, migrant/seed spawns are a scalar count with no per-shard structure), the cold pass's `AdvanceDayTick` fans its OWN internal parallel mortality pass and sequential fertility pass across `ColdPassSchedule(dayTick)`'s scheduled shards (1/30th of 256 per day-tick) entirely inside `citizens.CitizensAPI` — this is citizens' own shard scope, already built and tested (AC-6/AC-7/AC-17), not something this integration re-implements. From `compose`'s point of view, the integration itself is a single opaque call (`AdvanceDayTick`) with no shard parameter at all — so if/when this is expressed as an `integration.Integration[T,M]` (Increment 1's contract), it is `SingleShard() == true` at the COMPOSE-INTEGRATION layer (one call per tick, not once per shard), while citizens' own internal `det`-shard fan-out remains unaffected and invisible to compose.

---

## 7. Determinism Guarantee

*Instruction: the seed-key derivation (what values feed the seeded stream), the merge order, and an explicit no-wall-clock declaration. The lint requires this section to state that no wall-clock time is read (GR#21) — write it as a plain, unambiguous sentence containing "no wall-clock".*

**EXAMPLE:** Every stochastic draw inside the cold pass is a counter-based stream keyed `hash(worldSeed, id-or-householdID, month, purpose)` (mortality: `hash(seed, citizenID, month, "mortality")`; fertility: `hash(seed, householdID, month, "fertility")`, see `fertility.go`'s `CoupleBirth`) — fully determined by the world seed and the sim's own logical month, never by wall-clock time or goroutine scheduling order. The parallel mortality/education/job pass merges in ascending shard order (`runShardsParallel` + explicit `for _, t := range results` accumulation in ascending index); the fertility pass runs strictly sequentially AFTER the parallel pass completes (documented reason: a couple's eligibility read crosses shard boundaries and would otherwise race another shard's concurrent mortality mutation — `applyFertilityLocked`'s doc comment). **No wall-clock time is read anywhere in this integration** (AC-20) — the day/month counters are internal sim state (`c.dayTick`/`c.month`), and `VitalEvents`'s "most recently completed month" semantics are themselves clock-free (derived from the day-tick/month counters, never `time.Now()`).

---

## 8. Error / Registry Codes

*Instruction: the module's MET-range and the specific codes this integration can surface (GR#7 — every error must be registry-sourced).*

**EXAMPLE:** `engine.citizens` owns range `MOD-018` / `MET-G000`–`MET-G099` (a second "engine" letter-block, opened because the original `E000`–`E999` range was fully exhausted by eleven earlier modules — see `errors.go`'s range-claim note). Codes this integration can surface: `MET-G008` (`ErrFertilityDataInvalid` — malformed `data/fertility.json`, load-time only, would prevent `NewCitizensAPI` from constructing at all, so it fails before this integration's first tick) and `MET-G009` (`ErrFertilityBirthRejected` — a fertility-driven birth failed `ValidateCitizen`; logged loudly and the birth is skipped for that couple that month, never silently dropped per GR#1). `AdvanceDayTick`/`VitalEvents` themselves return `MET-G004` (`ErrAPICopied`) if called on a copied `*CitizensAPI` handle (SEC-020 family) — should never occur through the composition root's ownership pattern, but is a live defensive code path.

---

## 9. Resilience Behaviour

*Instruction: retry policy, catch-up semantics, reconnect/re-authentication expectations — modelled per the Integration Engine proposal's "resilience by design, from day one" (§1 point 5) even for an in-process integration, using the Increment 3 `integration.Connection`/`ReconnectHooks`/`Drainer` primitives (or their local no-op degenerate forms) as the vocabulary.*

**EXAMPLE:** In-process, always-connected today — the degenerate case `integration.LocalReconnectHooks` already models: `Authenticate`/`Lookup` are no-ops, so `Connection.Reconnect` (if this integration is later wrapped in a `Connection`) trivially succeeds every time. Retry policy: `AdvanceDayTick`'s only failure mode today is a copied-handle rejection (`MET-G004`) or a propagated `citizens` internal error — both deterministic given the same inputs, so a bare retry without backoff would just fail identically; the correct "retry" for this integration is therefore "fix the caller bug and re-run the tick," not a logical-backoff loop. If this integration is later location-transparently offloaded (proposal §4 — not planned for T0 population work, since T0 is explicitly "never queued past one tick," which rules out remote dispatch latency), it would use `integration.DefaultBackoff` + `DefaultMaxRetries` and `Connection.Attempt`/`Reconnect` exactly as increment 3 defines them, with `Queue == nil` (no `QueuedTransport` backlog — a T0 command that cannot be enqueued is `ErrT0QueueExhausted`, per `queue.go`, never silently spilled to disk). Catch-up: none needed while T0/in-process; a crash mid-tick is recovered by the existing checkpoint/replay path (`recovery.go`), not by this integration's own state.

---

## 10. Monitoring Signals

*Instruction: status (up/down/degraded), queue depth, throughput, peak load — proposal §1 point 7/§2's "Monitoring" and §7's "taps existing hooks."*

**EXAMPLE:** Status: derived from whether `AdvanceDayTick` returns an error this tick (up) vs a propagated error (degraded — logged via the registry, GR#1/GR#17). Throughput: `VitalEvents`' `(births, deaths)` pair, already a natural per-month rate signal — pipe it through `core.WithPhaseObserver`'s existing per-phase timing hook (proposal §7) alongside `spawnHook`'s existing spawn-count reporting, so the dashboard's integration map (increment 5, not yet built) can show citizens' cold-pass births/deaths on the same timeline as migration admits. Queue depth: not applicable — T0, no queue (see §9). Peak load: the day-tick's wall-clock COST (not a determinism input — purely an operational metric) via the existing `PhaseObserver` timing, watched against the BUG-034 1M-citizen perf gate this pass must stay inside.

---

## 11. Required Tests

*Instruction: name the concrete test(s) — proposal §1 point 8's four required categories. "Will be tested" is not sufficient; name the actual test function/file where possible.*

**EXAMPLE:**
- **Determinism equivalence:** `internal/engine/citizens/determinism_test.go`'s existing `PopulationHash` shard/worker-count invariance coverage (AC-17) already proves the cold pass itself is deterministic; this integration adds a compose-level test that two identical `driveTicks` runs (same seed) produce byte-identical `peopleDelta` accumulation and `PopulationHash` after N ticks with births/deaths live.
- **Resilience/disconnect-catch-up:** N/A today per §9 (no queue/connection — T0 in-process); once/if this integration is wrapped in `integration.Connection`, `resilience_test.go`'s existing disconnect→retry→catch-up→reconnect determinism-replay pattern applies directly.
- **Contract-conformance:** a new compose-level test asserting `VitalEvents`' returned `(births, deaths)` for a completed month exactly equals `peopleDelta`'s accumulated change over that month (the conservation identity this ICD's §4 Outputs table declares), mirroring `invariant/people_test.go`'s existing `PeopleInvariant` Opening/Closing/TrackedDelta identity check pattern.
- **AC-coverage:** the FEAT-169 acceptance criteria file (`docs/planning/acceptance/feat.compose-citizens-coldpass.md` or equivalent, to be authored per GR#25 alongside the edge registration) must be spec-linted clean (`tools/plan/spec-lint.js`) before this integration is built.

---

## 12. Change Control

*Instruction: the additive-only rule and versioning policy for this ICD — how a later change to Inputs/Outputs/Update Class is handled without breaking a dev who already built against an earlier version.*

**EXAMPLE:** Additive-only: a new Input/Output may be ADDED to a later version of this ICD without a version bump if it does not change any existing field's type or semantics; any REMOVAL or type/semantics change to an existing Input/Output/Update-Class/Determinism guarantee requires a new ICD version (append `v2`, `v3`, ... to this file's `## Change Control` history below) and a fresh Destructive-verdict round (GR#23) on the affected integration code, even if the underlying module code is otherwise untouched. Versioning is tracked here, not in the filename — `docs/planning/icd/<name>.md` stays a stable path; history is appended, never overwritten.

**Open decisions flagged by this ICD (not yet resolved — surfaced for Bill/Aaron, per §1 Identity):**
1. The citizens↔compose `code.json` edge does not exist yet; it must be registered (GR#25) before this integration's Go wiring is built, and this ICD's §1 GUID updated to the real edge GUID once that happens.
2. The child-id namespace seam: fertility allocates child ids from a `2^62` offset (`fertilityChildIDBase`, `fertility.go`) while compose allocates migrant/seed ids from its own separate sequential `simState.nextCitizenID` counter — disjoint by construction today, but with no single authoritative allocator or a formally VERIFIED-disjoint contract between the two. Flagged by FEAT-160's own build; this integration inherits the same open question and must not be considered fully closed until Bill/Aaron rule on a single allocator or an explicit verified-disjoint contract.

| Version | Date | Change |
|---|---|---|
| v1 | (fill in) | Initial ICD |
