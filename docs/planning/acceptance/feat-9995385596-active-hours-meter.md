# FEAT-9995385596 Acceptance Criteria: in-app active-hours meter

**BOW:** FEAT-9995385596 (P1). **Author:** BA (Sonnet), 2026-11-17. **Status:** draft for lead review.
**Sources:** inc1-cost-forecast.md R8; compute-offload/a2.md 2.2 and 4.1; A2Bev001 v23 (Q100154 ~125 h/month at 2 vCPU/4 GiB on paid hosts, pix unmetered; Q100157 hibernation; Q100161 pix first; Q100163 Spot); cmd/metroserve (CityHost, sessiongate, health); internal/persist.
**Blocks:** FEAT-9995385600 (inc1 on pix). **Related:** FEAT-9995385595 (hibernation), FEAT-9995385597 (waking/draining messages), FEAT-9995385601 (launcher).
**Code home (proposed):** cmd/metroserve/meter.go (plus a host-scoped record in internal/persist). Error codes below are placeholders `E-<NAME>`; the lead pre-assigns real MET- codes.

## Purpose

The only spend guard with no billing lag. Cloud budget alerts arrive hours late; this meter counts active seconds inside metroserve and refuses new wakes once the month's hour budget is spent. Required before any paid host serves play.

## Definitions

- **Active second:** a wall-clock second in which the process has at least one city loaded or building (a CityHost entry exists, ready or not) or at least one live WebSocket connection. Paused sim and open-but-idle tabs are active until hibernation unloads the city (ASM-1586). Seconds with nothing loaded are not active.
- **Host class:** one of `dev`, `pix`, `azure`, `spot-aws`, `spot-gcp` (extensible). `dev` and `pix` are unmetered; the others are metered.
- **Wake:** a CityHost.GetOrCreate that must build a city (cache miss). Connecting to an already-loaded city is not a wake (ASM-1595).
- **Budget:** hours per calendar month for the running host class; sources of truth are the launcher flags, not code constants.
- **State:** `unmetered`, `ok` (<80 percent), `warn` (80 to <100 percent), `exhausted` (>=100 percent), `hard` (>=110 percent), `unknown` (counter unreadable or unwritable).

## A. What counts

- **AC-1** Given a hosted process with no city loaded and no connection, When 60 s of clock time pass, Then the active-seconds counter does not increase.
- **AC-2** Given a city is built (GetOrCreate claims it), When time passes, Then active seconds increase 1 per elapsed second from the instant of the claim, including while the build is still running (wake time is billed time).
- **AC-3** Given three cities are loaded in one process, When 100 s pass, Then the process counter increases by 100, not 300 (per-process, not per-city; ASM-1587).
- **AC-4** Given a loaded city whose sim is paused and whose tab is open and idle, When time passes, Then seconds still count until the Q100157 hibernation unloads the city.
- **AC-5** Given the city is evicted or hibernated and no other city or connection remains, When time passes, Then counting stops within one sample interval (1 s).
- **AC-6** Given a connection exists but its city has been torn down, When time passes, Then seconds still count while the connection is open (either condition suffices).
- **AC-7** Given a sample whose elapsed delta exceeds 60 s (laptop sleep or clock jump), When the meter samples, Then at most 60 s is added (ASM-1597).
- **AC-8** Given the wall clock moves backwards, When the meter samples, Then the delta is treated as 0, the counter never decreases, and the month key is max(stored, now) (never an earlier month).
- **AC-9** Per-tenant active seconds are recorded as an informational breakdown only; the budget decision uses the process total (ASM-1587).

## B. Persistence

- **AC-10** Given the meter has counted seconds, When the process restarts, Then the counter resumes from the persisted value for the same month and host class, never from zero.
- **AC-11** The counter is persisted as one host-scoped record at the persistence root (and the R2 store when inc2 lands), not under any city key and not in process memory (ASM-1588). It carries: schema version, month key, per-host-class seconds buckets, sequence number, last-flush time, last-seen budget, clean-shutdown marker, checksum.
- **AC-12** Given the host moves (pix to Spot to Azure) over the same persistence root or R2 store, When the new host boots, Then it reads the same record; seconds are added to the bucket of the new host's class and the budget check reads only that bucket (ASM-1589).
- **AC-13** Writes are atomic (temp file plus rename, or the store's conditional put); a reader never sees a half-written record. A torn temp file left by a crash is ignored.
- **AC-14** The record is flushed every 30 s while active, on SIGTERM (inside the 20 s snapshot budget, Q100163, and counted as part of it), and on hibernation. The flush happens on the meter's own goroutine and must never block a tick or the /health handler (ASM-1592).
- **AC-15** Given an unclean exit (clean-shutdown marker absent at next boot), When the process boots, Then it adds one flush interval (30 s) to the current-class bucket as a pessimistic top-up before checking the budget.
- **AC-16** The meter writes only while this process holds the persistence-root single-writer lock; without it the state is `unknown` and the meter does not write (ASM-1598).
- **AC-17** Given a flush fails, When it keeps failing for 5 minutes (10 consecutive attempts), Then on a metered class the state becomes `unknown`, new wakes are refused with `E-METER-WRITE`, and /health says so; one success clears it.

## C. Month and time zone

- **AC-18** The month is the UTC calendar month `YYYY-MM` (ASM-1590). Given UTC midnight on the 1st, When the next sample runs, Then the record rolls: the previous month's totals are kept as history (last 12 months, no silent deletion) and the new month starts at zero for every bucket.
- **AC-19** Rollover happens only through the month-key comparison. A missing record never acts as a reset (see AC-30).
- **AC-20** Given a city stays loaded across the month boundary, When rollover happens, Then it keeps running and its seconds count into the new month from the boundary on. Seconds before the boundary stay in the old month.
- **AC-21** Given the process is down across a month boundary, When it boots, Then it opens the new month at zero and writes the history entry for the old one.

## D. Budget configuration

- **AC-22** Flags (and matching env vars the launcher can set): `-host-class` and `-active-hours-budget` (hours, may be fractional). Defaults: host-class `dev`, no budget, unmetered. Current behaviour of every existing invocation is byte-identical (ASM-1596).
- **AC-23** Given a metered class with no budget, or a budget <= 0, or a non-numeric budget, When the process boots, Then it refuses to start with `E-METER-CONFIG`; it never starts unmetered by accident.
- **AC-24** Given a metered class with persistence off (no `-persist-dir`) or in the legacy single-city path, When the process boots, Then it refuses to start with `E-METER-CONFIG` (a counter with nowhere to persist is no guard).
- **AC-25** Given `-host-class pix` (or `dev`), When the process runs, Then seconds are still counted and shown in /health (informational) but no state beyond `unmetered` is reached and no wake is ever refused.
- **AC-26** The budget is read at boot only; changing it needs a restart. The last-seen budget is recorded; if it differs from the previous record, one log line states old and new values (an operator raising the budget leaves a trace).
- **AC-27** The launcher (FEAT-9995385601) is the only place that knows per-class defaults (for example azure 125 h at 2 vCPU/4 GiB; Spot values from its own forecast). metroserve holds no hard-coded hour budgets; unit tests use explicit values.

## E. Thresholds and behaviour

- **AC-28** At >=80 percent of budget the state is `warn`: /health shows it and a single log line is written when crossing; nothing is refused.
- **AC-29** At >=100 percent the state is `exhausted`: every wake (GetOrCreate cache miss, including the default-city pre-create at boot) is refused with `E-METER-BUDGET`. The refusal travels as a registry error through the resolver (the BUG-1196 resolverRefusal passthrough), reaches the client as a clean error and not as a generic MET-P030 wrap, and leaves no half-built city, no CityHost entry and no session-gate claim behind.
- **AC-30** Given state `exhausted`, When a client connects to a city that is already loaded, Then the connection is allowed (ASM-1595). Live sessions are never killed at 100 percent and a tick in progress is never interrupted.
- **AC-31** Given state `exhausted`, When a live session goes idle, Then normal Q100157 hibernation applies and the city snapshots and unloads as usual; after that no new wake is possible until next month or a restart with a higher budget.
- **AC-32** At >=110 percent the state is `hard`: the host asks every loaded city to snapshot at its next tick boundary, sends `server.draining` (FEAT-9995385597) with reason `budget`, then hibernates it (ASM-1594). No tick is aborted mid-way; a snapshot failure is logged and retried, never skipped silently.
- **AC-33** Given the 100 percent line is crossed during a wake already in flight, When the build is past the claim, Then that wake completes (not rolled back); the next wake is refused.
- **AC-34** Percent is computed from integer seconds as `used / (budgetHours * 3600)`; thresholds compare with `>=` on integers, no float accumulation.
- **AC-35** The bounds are named constants (80, 100, 110, 30 s flush, 60 s delta cap), single definition (GR#3).

## F. Hibernation interaction

- **AC-36** Seconds between hibernation unload and the next wake are not counted; the wake itself (build and rehydrate) is counted from the claim (AC-2).
- **AC-37** Hibernation snapshot time before unload counts (the city is still loaded).
- **AC-38** The meter works with hibernation absent: until FEAT-9995385595 lands, the 100 and 110 percent rules still hold (a 110 percent hard stop uses its own snapshot-and-unload path, not the idle path).

## G. Tamper, loss, rollback (fail closed)

- **AC-39** Given a metered class and a persistence root that holds any city data, When the counter record is missing, unreadable, checksum-invalid, schema-unknown or has a lower sequence than the last value this process saw, Then state is `unknown`, every wake is refused with `E-METER-UNKNOWN`, loaded cities continue, and /health reports the reason. The meter never re-initialises to zero on its own (ASM-1593).
- **AC-40** Given a metered class and a completely empty persistence root (first boot ever), When the process boots, Then it creates the record at zero. This is the only path that creates a record without an operator.
- **AC-41** Operator recovery: a boot flag `-meter-seed-hours N` (with explicit `-meter-ack-unknown`) writes a fresh record with N hours used in the current month and logs a loud line with the previous reason. The seed is refused if the record is present and valid.
- **AC-42** Given an unmetered class, When the record is missing or corrupt, Then it is re-created at zero with a log line; no refusal.
- **AC-43** Out of scope: an operator with write access to the root who edits the counter and recomputes the checksum. The checksum detects accident and rollback by a restored old backup (lower sequence), not malice (ASM-1593).

## H. Observability (/health)

- **AC-44** /health gains an always-present `activeHours` object: `hostClass`, `metered` (bool), `month`, `usedSeconds`, `budgetSeconds` (null when unmetered), `percent` (integer, null when unmetered), `state`, `refusedWakes` (count since boot), `lastFlushUnix`, `unknownReason` (empty when none). `status` stays `"ok"` (liveness is unchanged; the meter state is its own field).
- **AC-45** Reading it takes atomic loads only; /health still never blocks on the store or the meter goroutine.
- **AC-46** The in-app badge shows `warn` and `exhausted` from the same state. How the client reads it (poll /health, or a field on the handshake or server messages) is a client-side item that FEAT-9995385597 or the web console owns; this feature guarantees only the /health field (open question Q-3).
- **AC-47** Each refusal emits one log line with the wake's tenant, city, state, used and budget seconds, and increments `refusedWakes`.

## I. GR#21 and determinism

- **AC-48** The meter uses wall-clock time and never reaches any engine, composition or snapshot (GR#21, ASM-1591). A test asserts that no package under internal/engine imports the meter and that no meter value is written into a snapshot, journal record or state hash.
- **AC-49** Engine determinism gates (detgate digests) are unchanged with the meter on, off, exhausted, or unknown.

## J. Tests (fake clock seam)

- **AC-50** The meter takes an injected clock interface (`Now()`) and a manual sample trigger; tests never sleep and never read the real clock. The production wiring is the only place `time.Now` is bound.
- **AC-51** Table tests cover AC-1 to AC-9 (active definition, delta cap, backwards clock), including a fake-clock jump across UTC midnight on the 31st/1st and on a leap day.
- **AC-52** Persistence tests use a real DiskStore in a temp dir: restart resumes (AC-10), unclean-exit top-up (AC-15), torn temp ignored (AC-13), record deleted on a non-empty root (AC-39), lower-sequence restore (AC-39), empty-root first boot (AC-40), seed flag (AC-41), flush failure injection (AC-17).
- **AC-53** Threshold tests drive a fake clock to exactly 79.99, 80, 99.99, 100, 109.99 and 110 percent of a small budget (for example 1 h) and assert the state and wake result at each; they include a 16-way concurrent wake burst at 100 percent that all refuse with no leaked CityHost entry or session claim (compose with BUG-1196 gate).
- **AC-54** Each regression test is proven able to fail (mutation: remove the check, test goes red). No wall-clock bounds in CI.
- **AC-55** Boot-config tests cover AC-22 to AC-25, including that a metered class never silently runs unmetered.

## K. Registry, docs, GR#25

- **AC-56** New errors (registered by the lead through tools/plan/add-error.js; codes pre-assigned): `E-METER-BUDGET`, `E-METER-UNKNOWN`, `E-METER-WRITE`, `E-METER-CONFIG`. Every refusal and boot failure uses a registry code, none ad hoc (GGR80).
- **AC-57** code.json/master plan: meter registered under the metroserve host module with its edge to internal/persist; the edge lint and spec-lint pass.
- **AC-58** The cost forecast (inc1-cost-forecast.md R8) and README row are updated to link this file when the feature is done.

## Unstated assumptions logged

ASM-1586 active definition; ASM-1587 per process; ASM-1588 host-level record; ASM-1589 per-class buckets; ASM-1590 UTC month; ASM-1591 wall clock/GR#21; ASM-1592 30 s flush; ASM-1593 fail closed on missing counter; ASM-1594 110 percent hard stop; ASM-1595 loaded-city connect allowed; ASM-1596 defaults and explicit paid class; ASM-1597 60 s delta cap; ASM-1598 single-writer lock.

## Open questions for Aaron

- **Q-1** Hard stop: is a forced graceful hibernate at 110 percent acceptable, or should live sessions run on past 100 percent until idle? (At 125 h, 110 percent is 12.5 h beyond budget.)
- **Q-2** Should an up-but-empty replica (min replicas 1, everything hibernated) count? Azure bills it at a lower idle rate. Current draft says no, per "hibernated time is not active".
- **Q-3** Who owns the client badge and how does it read the state: poll /health, or a field in the handshake/server messages?
- **Q-4** Should a reconnect to an already-loaded city be allowed at 100 percent (draft: yes)?
- **Q-5** Should there be a dated operator override to raise the budget mid-month without restart (draft: restart only)?
- **Q-6** Is "pix unmetered" strictly unmetered, or should pix also carry a generous advisory budget so a runaway never goes unnoticed?
