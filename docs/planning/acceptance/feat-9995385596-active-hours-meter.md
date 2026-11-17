# FEAT-9995385596 Acceptance Criteria: in-app active-hours meter

**BOW:** FEAT-9995385596 (P1). **Author:** BA (Sonnet), 2026-11-17. **Status:** reviewed 2026-11-17 (see Independent review at the end); Aaron's Q-1/Q-6 and the lead's Q-2..Q-5 rulings applied in place.
**Sources:** inc1-cost-forecast.md R8; compute-offload/a2.md 2.2 and 4.1; A2Bev001 v23 (Q100154 ~125 h/month at 2 vCPU/4 GiB on paid hosts, pix unmetered; Q100157 hibernation; Q100161 pix first; Q100163 Spot); cmd/metroserve (CityHost, sessiongate, health); internal/persist.
**Blocks:** FEAT-9995385600 (inc1 on pix). **Related:** FEAT-9995385595 (hibernation), FEAT-9995385597 (waking/draining messages), FEAT-9995385601 (launcher).
**Code home (proposed):** cmd/metroserve/meter.go (plus a host-scoped record in internal/persist). Error codes below are placeholders `E-<NAME>`; the lead pre-assigns real MET- codes.

## Purpose

The only spend guard with no billing lag. Cloud budget alerts arrive hours late; this meter counts active seconds inside metroserve and refuses new wakes once the month's hour budget is spent. Required before any paid host serves play.

## Definitions

- **Active second:** a wall-clock second in which the process has at least one city loaded or building (a CityHost entry exists, ready or not) or at least one live WebSocket connection. Paused sim and open-but-idle tabs are active until hibernation unloads the city (ASM-1586). Seconds with nothing loaded are not active.
- **Host class:** one of `dev`, `pix`, `azure`, `spot-aws`, `spot-gcp` (extensible). `dev` and `pix` are unmetered (never refuse); `pix` may carry an advisory figure (Aaron Q-6, see AC-25). The others are metered.
- **Wake:** a CityHost.GetOrCreate that must build a city (cache miss). Connecting to an already-loaded city is not a wake (ASM-1595).
- **Budget:** hours per calendar month for the running host class; sources of truth are the launcher flags, not code constants.
- **State:** `unmetered`, `advisory` (unmetered class past its configured advisory figure; never refuses), `ok` (<80 percent), `warn` (80 to <100 percent), `exhausted` (>=100 percent: open cities are saved and hibernated, wakes refused), `unknown` (counter unreadable or unwritable). There is no 110 percent state (Aaron Q-1, 2026-11-17).
- **Up-but-empty process:** a process with no CityHost entry and no connection is not active, even on a paid replica billed at an idle rate (lead ruling Q-2).

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
- **AC-11** The counter is persisted as one host-scoped record at the persistence root (and the R2 store when inc2 lands), not under any city key and not in process memory (ASM-1588). *Review amendment:* `persist.Store` is per-city today (every method takes a `CityKey`), so this needs new host-scoped methods on the interface, for example `GetHostRecord(ctx, name) (payload, seq, error)` and `PutHostRecord(ctx, name, payload, expectedSeq) error` (compare-and-swap on the sequence; a lost CAS is an error, never an overwrite), implemented by MemStore and DiskStore in this feature and carried into the inc2 R2 store's ACs (FEAT-9995385594) as a conditional put (ASM-1618). The record is never written through a fake CityKey. It carries: schema version, month key, per-host-class seconds buckets, sequence number, last-flush time, last-seen budget, clean-shutdown marker, checksum.
- **AC-12** Given the host moves (pix to Spot to Azure) over the same persistence root or R2 store, When the new host boots, Then it reads the same record; seconds are added to the bucket of the new host's class and the budget check reads only that bucket (ASM-1589).
- **AC-13** Writes are atomic (temp file plus rename, or the store's conditional put); a reader never sees a half-written record. A torn temp file left by a crash is ignored.
- **AC-14** The record is flushed every 30 s while active, on SIGTERM (inside the 20 s snapshot budget, Q100163, and counted as part of it), and on hibernation. The flush happens on the meter's own goroutine and must never block a tick or the /health handler (ASM-1592).
- **AC-15** Given an unclean exit (clean-shutdown marker absent at next boot), When the process boots, Then it adds one flush interval (30 s) to the current-class bucket as a pessimistic top-up before checking the budget.
- **AC-16** The meter writes only while this process holds the persistence-root single-writer lock; without it the state is `unknown` and the meter does not write (ASM-1598). *Review amendment:* no such root lock exists on main (DiskStore's locks are in-process per-city mutexes; the only OS claim is the per-city citizen-paging directory). The fence is the inc2 writer epoch (Q100159: conditional write on R2, OS file lock on local disk, ASM-1615). Until that fence exists in the store, a metered class refuses to start with `E-METER-CONFIG` ("metered class needs a fenced store"); `dev` and `pix` run unfenced and stay advisory only (ASM-1619). A test boots a metered class on an unfenced store and asserts the refusal.
- **AC-17** Given a flush fails, When it keeps failing for 5 minutes (10 consecutive attempts), Then on a metered class the state becomes `unknown`, new wakes are refused with `E-METER-WRITE`, and /health says so; one success clears it.

## C. Month and time zone

- **AC-18** The month is the UTC calendar month `YYYY-MM` (ASM-1590). Given UTC midnight on the 1st, When the next sample runs, Then the record rolls: the previous month's totals are kept as history (last 12 months; a month falling out of the window is dropped with one log line naming it and its totals, never silently) and the new month starts at zero for every bucket.
- **AC-19** Rollover happens only through the month-key comparison. A missing record never acts as a reset (see AC-30).
- **AC-20** Given a city stays loaded across the month boundary, When rollover happens, Then it keeps running and its seconds count into the new month from the boundary on. Seconds before the boundary stay in the old month.
- **AC-21** Given the process is down across a month boundary, When it boots, Then it opens the new month at zero and writes the history entry for the old one.

## D. Budget configuration

- **AC-22** Flags (and matching env vars the launcher can set): `-host-class`, `-active-hours-budget` (hours, may be fractional) and `-active-hours-advisory` (hours, unmetered classes only; a metered class given it refuses to start with `E-METER-CONFIG`). When flag and env are both set the flag wins. Defaults: host-class `dev`, no budget, unmetered. Current behaviour of every existing invocation is byte-identical (ASM-1596).
- **AC-23** Given a metered class with no budget, or a budget <= 0, or a non-numeric budget, When the process boots, Then it refuses to start with `E-METER-CONFIG`; it never starts unmetered by accident.
- **AC-24** Given a metered class with persistence off (no `-persist-dir`) or in the legacy single-city path, When the process boots, Then it refuses to start with `E-METER-CONFIG` (a counter with nowhere to persist is no guard).
- **AC-25** (Aaron Q-6: pix ADVISORY) Given `-host-class pix` (or `dev`), When the process runs, Then seconds are counted, persisted and shown in /health, no wake is ever refused and no city is ever stopped by the meter. Given an `-active-hours-advisory` figure, When used seconds reach it (`>=`, integer seconds), Then the state becomes `advisory`, one log line is written at the crossing, and /health shows it; without a figure the state stays `unmetered`. A test drives a fake clock past the figure and asserts state `advisory`, a successful wake afterwards, and exactly one crossing log line.
- **AC-26** (lead Q-5) The budget is read at boot only; changing it needs a restart. The last-seen budget is recorded; if it differs from the previous record, one log line states old and new values (an operator raising the budget leaves a trace).
- **AC-27** The launcher (FEAT-9995385601) is the only place that knows per-class defaults (for example azure 125 h at 2 vCPU/4 GiB; Spot values from its own forecast). metroserve holds no hard-coded hour budgets; unit tests use explicit values.

## E. Thresholds and behaviour

- **AC-28** At >=80 percent of budget the state is `warn`: /health shows it and a single log line is written when crossing; nothing is refused.
- **AC-29** At >=100 percent the state is `exhausted`: every wake (GetOrCreate cache miss) is refused with `E-METER-BUDGET`. At boot in state `exhausted` (or `unknown`) the default-city pre-create is skipped with one log line and the process stays up serving /health and refusing wakes; it does not exit non-zero (main.go returns 1 on a pre-create error today, which would crash-loop the container and keep a paid replica up, ASM-1621). The refusal travels as a registry error through the resolver (the BUG-1196 resolverRefusal passthrough), reaches the client as a clean error and not as a generic MET-P030 wrap, and leaves no half-built city, no CityHost entry and no session-gate claim behind.
- **AC-30** (Aaron Q-1: STOP AT EXACTLY 100 percent; replaces the draft's run-on and 110 percent rules) Given state becomes `exhausted` while cities are loaded, When the crossing sample runs, Then the host immediately, with no grace period, stops every loaded city: at the city's next tick boundary (a tick in progress is never aborted) it sends the graceful notice, takes one durable snapshot attempt within the 20 s budget (Q100163), and unloads the city through the same path as hibernation. The notice is `server.draining` with reason `budget` once FEAT-9995385597 lands; until then a WebSocket close carrying `E-METER-BUDGET` and the text "monthly budget reached". A failed snapshot is logged with `E-METER-STOP-SNAPSHOT` and the city still unloads (journal appends are already durable; a retry loop would keep billing past budget, ASM-1620). Seconds spent stopping are counted; overshoot past 100 percent is bounded by the stop duration only. ASM-1595 (loaded-city connect allowed) is withdrawn.
- **AC-31** (lead Q-4) Given state `exhausted`, When a client connects or reconnects to any city (including one stopped by AC-30 moments earlier), Then the connect is a new wake and is refused with `E-METER-BUDGET`; no new wake is possible until the next UTC month or a restart with a higher budget.
- **AC-32** *Withdrawn by Aaron Q-1* (no 110 percent state). ASM-1594 is superseded by AC-30.
- **AC-33** Given the 100 percent line is crossed during a wake already in flight, When the build is past the claim, Then the build completes (no half-built rollback) and the city is then stopped by AC-30 at its first tick boundary; the next wake is refused.
- **AC-34** The budget is converted once at boot to integer `budgetSeconds = floor(budgetHours * 3600)` (must be >= 1, else `E-METER-CONFIG`). Thresholds compare integers: `used*100 >= budgetSeconds*80` for warn and `used >= budgetSeconds` for exhausted; no float accumulation. /health `percent` is `floor(used*100/budgetSeconds)`.
- **AC-35** The bounds are named constants (80, 100, 30 s flush, 60 s delta cap, 12-month history), single definition (GR#3).

## F. Hibernation interaction

- **AC-36** Seconds between hibernation unload and the next wake are not counted; the wake itself (build and rehydrate) is counted from the claim (AC-2).
- **AC-37** Hibernation snapshot time before unload counts (the city is still loaded).
- **AC-38** The meter works with hibernation absent: until FEAT-9995385595 lands, the AC-30 stop uses CityHost's own snapshot-then-Shutdown path (the existing snapshot driver for the snapshot, `CityHost.Shutdown` for the unload), not the idle evictor. A test with hibernation absent drives the fake clock to 100 percent with one loaded city and asserts: snapshot attempted, CityHost entry gone, session-gate claim released, no leaked goroutine.

## G. Tamper, loss, rollback (fail closed)

- **AC-39** Given a metered class and a persistence root that holds any city data, When the counter record is missing, unreadable, checksum-invalid, schema-unknown or has a lower sequence than the last value this process saw ("holds any city data" means `ListCities` is non-empty for any tenant the host serves; today only `persistTenantID`), Then state is `unknown`, every wake is refused with `E-METER-UNKNOWN`, loaded cities continue, and /health reports the reason. The meter never re-initialises to zero on its own (ASM-1593).
- **AC-40** Given a metered class and a completely empty persistence root (first boot ever), When the process boots, Then it creates the record at zero. This is the only path that creates a record without an operator.
- **AC-41** Operator recovery: a boot flag `-meter-seed-hours N` (with explicit `-meter-ack-unknown`) writes a fresh record with N hours used in the current month and logs a loud line with the previous reason. The seed is refused if the record is present and valid.
- **AC-42** Given an unmetered class, When the record is missing or corrupt, Then it is re-created at zero with a log line; no refusal.
- **AC-43** Out of scope: an operator with write access to the root who edits the counter and recomputes the checksum. The checksum detects accident, not malice (ASM-1593). *Review amendment:* the sequence check detects a rollback only within one process lifetime; a restore of an older valid record before boot is undetectable without an external witness and is out of scope (ASM-1622).

## H. Observability (/health)

- **AC-44** /health gains an always-present `activeHours` object: `hostClass`, `metered` (bool), `month`, `usedSeconds`, `budgetSeconds` (null when unmetered), `percent` (integer, null when unmetered), `state`, `advisorySeconds` (null when none configured), `refusedWakes` (count since boot), `stoppedCities` (AC-30 stops since boot), `lastFlushUnix`, `unknownReason` (empty when none). `state` takes exactly the values in Definitions. `status` stays `"ok"` (liveness is unchanged; the meter state is its own field).
- **AC-45** Reading it takes atomic loads only; /health still never blocks on the store or the meter goroutine.
- **AC-46** (lead Q-3) Out of scope here: this feature ships the /health field only. The in-app badge comes later through FEAT-9995385597's protocol messages; no client code changes in this feature.
- **AC-47** Each refusal emits one log line with the wake's tenant, city, state, used and budget seconds, and increments `refusedWakes`.

## I. GR#21 and determinism

- **AC-48** The meter uses wall-clock time and never reaches any engine, composition or snapshot (GR#21, ASM-1591). A test asserts that no package under internal/engine imports the meter and that no meter value is written into a snapshot, journal record or state hash.
- **AC-49** *Review amendment* (the draft was vacuous: detgate never runs metroserve). Given a city stopped by AC-30 and later woken (fake clock into the next UTC month), When it is rehydrated into a fresh Composition via `LoadAt` and run on, Then its `StateDigest` at every tick equals an uninterrupted run of the same city (BUG-1201 lesson: fresh Composition, never the same instance). The meter adds nothing to engine state.

## J. Tests (fake clock seam)

- **AC-50** The meter takes an injected clock interface (`Now()`) and a manual sample trigger; tests never sleep and never read the real clock. The production wiring is the only place `time.Now` is bound.
- **AC-51** Table tests cover AC-1 to AC-9 (active definition, delta cap, backwards clock), including a fake-clock jump across UTC midnight on the 31st/1st and on a leap day.
- **AC-52** Persistence tests use a real DiskStore in a temp dir: restart resumes (AC-10), unclean-exit top-up (AC-15), torn temp ignored (AC-13), record deleted on a non-empty root (AC-39), lower-sequence restore (AC-39), empty-root first boot (AC-40), seed flag (AC-41), flush failure injection (AC-17).
- **AC-53** Threshold tests drive a fake clock to `budgetSeconds*80/100 - 1`, `budgetSeconds*80/100`, `budgetSeconds - 1` and `budgetSeconds` of a small budget (for example 1 h) and assert the state, the wake result and (at `budgetSeconds`) the AC-30 stop of a loaded city at each; they include a 16-way concurrent wake burst at 100 percent that all refuse with no leaked CityHost entry or session claim (compose with BUG-1196 gate).
- **AC-54** Each regression test is proven able to fail (mutation: remove the check, test goes red). No wall-clock bounds in CI.
- **AC-55** Boot-config tests cover AC-22 to AC-25, including that a metered class never silently runs unmetered.

## K. Registry, docs, GR#25

- **AC-56** New errors (registered by the lead through tools/plan/add-error.js; codes pre-assigned in the dev brief so parallel lanes do not collide): `E-METER-BUDGET`, `E-METER-UNKNOWN`, `E-METER-WRITE`, `E-METER-CONFIG`, `E-METER-STOP-SNAPSHOT` (AC-30 snapshot failure). Every refusal and boot failure uses a registry code, none ad hoc (GGR80).
- **AC-57** code.json/master plan: meter registered under the metroserve host module with its edge to internal/persist; the edge lint and spec-lint pass.
- **AC-58** The cost forecast (inc1-cost-forecast.md R8) and README row are updated to link this file when the feature is done.

## Unstated assumptions logged

ASM-1586 active definition; ASM-1587 per process; ASM-1588 host-level record; ASM-1589 per-class buckets; ASM-1590 UTC month; ASM-1591 wall clock/GR#21; ASM-1592 30 s flush; ASM-1593 fail closed on missing counter; ASM-1594 110 percent hard stop; ASM-1595 loaded-city connect allowed; ASM-1596 defaults and explicit paid class; ASM-1597 60 s delta cap; ASM-1598 single-writer lock.

## Open questions for Aaron (all answered 2026-11-17; kept for the record)

- **Q-1** ANSWERED (Aaron): stop at exactly 100 percent, save and hibernate immediately with a graceful notice; see AC-30. Original: Hard stop: is a forced graceful hibernate at 110 percent acceptable, or should live sessions run on past 100 percent until idle? (At 125 h, 110 percent is 12.5 h beyond budget.)
- **Q-2** ANSWERED (lead): not counted. Original: Should an up-but-empty replica (min replicas 1, everything hibernated) count? Azure bills it at a lower idle rate. Current draft says no, per "hibernated time is not active".
- **Q-3** ANSWERED (lead): /health now, badge later via FEAT-9995385597. Original: Who owns the client badge and how does it read the state: poll /health, or a field in the handshake/server messages?
- **Q-4** ANSWERED (lead): moot; at 100 percent a reconnect is a new wake and is refused (AC-31). Original: Should a reconnect to an already-loaded city be allowed at 100 percent (draft: yes)?
- **Q-5** ANSWERED (lead): restart only (AC-26). Original: Should there be a dated operator override to raise the budget mid-month without restart (draft: restart only)?
- **Q-6** ANSWERED (Aaron): pix ADVISORY, record and warn past a configured figure, never refuse (AC-25). Original: Is "pix unmetered" strictly unmetered, or should pix also carry a generous advisory budget so a runaway never goes unnoticed?

## Independent review 2026-11-17 (opus-review-FEAT-9995385596)

Hostile requirements review by an agent that did not write these ACs. Checked against cmd/metroserve (main.go, cityhost.go, health.go, sessiongate.go), internal/persist (store.go, diskstore.go), A2Bev001 v23..v23.3, compute-offload/README.md and a2.md 2.2.

**Rulings applied.**
- Aaron Q-1, stop at exactly 100 percent: AC-30 rewritten (save and hibernate every open city at the next tick boundary, graceful notice); AC-32 and the `hard` state withdrawn; AC-33, AC-34, AC-35, AC-38 and AC-53 aligned; ASM-1594 and ASM-1595 superseded.
- Aaron Q-6, pix advisory: new `advisory` state, `-active-hours-advisory` flag (AC-22, AC-25), /health `advisorySeconds` (AC-44).
- Lead Q-2 (an empty replica is not counted): Definitions. Lead Q-3 (/health now, badge later): AC-46 made out of scope. Lead Q-4 (a reconnect at 100 percent is a refused wake): AC-31. Lead Q-5 (restart only): AC-26.

**ACs amended in place (17):** AC-11, AC-16, AC-18, AC-22, AC-25, AC-29, AC-30, AC-31, AC-33, AC-34, AC-38, AC-39/AC-43, AC-44, AC-46, AC-49, AC-53, AC-56 (some are ruling-only edits). Code-fact findings behind them:
- AC-11: `persist.Store` is strictly per-city (every method takes a `CityKey`). A host-scoped record is new interface scope (CAS put on a sequence). It must also go into the inc2 R2 store's ACs, FEAT-9995385594, which nobody has told yet.
- AC-16: the "persistence-root single-writer lock" does not exist on main. Metered classes now refuse to start until the inc2 writer-epoch fence exists. pix and dev are unaffected. This matches Q100161, where paid hosts are the later fallback.
- AC-29: main.go exits 1 when the default-city pre-create fails. As drafted, an exhausted meter would crash-loop the container and keep a paid replica alive. Amended so the pre-create is skipped and the process stays up.
- AC-30: snapshot "retried, never skipped" (old AC-32) would bill past budget forever on a failing store. Now one bounded attempt, then unload; the journal is the durability guarantee.
- AC-43: "lower sequence detects a restored backup" is false across a restart. The limitation is now stated.
- AC-49: as drafted it was vacuous, because detgate never runs metroserve. Replaced with a stop-then-wake save/load equivalence via a fresh Composition plus `LoadAt` (BUG-1201).
- AC-34: fractional hours compared as a float percent. Now uses integer `budgetSeconds`.

**ACs rejected:** none outright. AC-32 was withdrawn by ruling.

**New assumptions:** ASM-1618 (host-scoped Store methods), ASM-1619 (metered class needs a fenced store), ASM-1620 (one snapshot attempt then unload), ASM-1621 (exhausted boot skips pre-create, no exit), ASM-1622 (rollback detection is in-process only).

**Dependencies the dev brief must carry:** FEAT-9995385594 (host record in the R2 store, and the writer-epoch fence before any metered class can start); FEAT-9995385597 (the `server.draining` reason `budget`; until then, a close frame); BUG-1197/FEAT-9995385595 (the snapshot-on-stop path, with AC-38's fallback until they land).

**Verdict: READY FOR DEV**, scoped as follows. Everything builds and tests on MemStore and DiskStore with the fake clock now, and pix advisory mode is fully deliverable. Metered-class enablement stays refused by AC-16 until the inc2 fence lands, by design, not as a blocker. Pre-assign the five MET- codes in the brief.
