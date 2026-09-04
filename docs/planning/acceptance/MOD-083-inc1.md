BOW code: MOD-083

# Acceptance criteria — MOD-083 Death services: graveyards (space+reuse), cremation, hearses, emergency dispensation (increment 1)

**BOW code:** MOD-083
**Mkey:** `engine.deathservices`
**Spec refs:** Part IV structure catalogue (`docs/METROPOLIS-MASTER-v2.1.md` lines 1075-1079: Cemetery M2, 300k, 2k plots, "fills permanently — land pressure"; Crematorium M5+DP, 3M, 12/d; Memorial woodland M6+DP, 1M, 5k); regional-contract table (line 543: "Crematorium/cemetery | £/service"); §5 mortality; §26 Emergency & Care Dispatch; §25 Refuse & waste-health loop (structural analog for collection-round logistics); §9 Seasonality (weather curves driving emergency gates).
**Upstream:** FEAT-087 `feat.deathwave` (P1, open) — produces RealisedDeath{CitizenID, DeathMonth, EmergencyFlag} FIFO stream consumed by this module's intake. FEAT-087 owns mortality smoothing, weather-event detection, and queue ordering; this module owns body triage/disposal.
**Registered edges:** code.json (2026-08-20, commit cec11a0): `engine.deathservices` (seq 605, path `internal/engine/deathservices/`, sprint 9, phase M4) outbound to engine.citizens, feat.deathwave, engine.services, engine.world, engine.build, engine.logistics, foundation.data, foundation.errors (all registered, no contingencies).
**Date:** 2026-09-04 (BA increment spec).
**Status:** **ready for increment 1 dispatch** — module registered, upstream FEAT-087 ready, all graph edges exist, FEAT-088 feature-level spec available for reference.
**Package under test:** `internal/engine/deathservices/` (Go engine module only; UI display deferred to increment 2).
**Standard gates:** see `README.md` — all apply; package for SG-4/SG-7 is `./internal/engine/deathservices/...`.
**Balance-number regime (GR#15):** Spec catalogue *capacities* (2k plots, 12 cremations/day, 5k memorial-woodland places) are transcribed as seed values sourced from data, never hardcoded. Every *player-felt* magnitude the spec does NOT provide — the plot reuse horizon (months), the hearse speed/round time, the cremation per-body cost, the van/truck multi-body count under dispensation, and the dispensation throughput multiplier — is a data-file placeholder with disclosure comment. ACs check *shape/mechanism/direction*, never a pinned figure. All live in `data/deathservices.json` with metadata.

## Scope — increment 1

Engine-side body intake (FEAT-087 death queue → internal body records), graveyard plot allocation with reuse horizon enforcement, cremation as the costed alternative, hearse-based transport with one-body-per-trip throughput (backed by a coarse monthly transport budget), emergency-dispensation mode (multi-body vans/trucks + 24×7) gated on FEAT-087's weather event, and the body-conservation identity (one body, handled exactly once, by exactly one terminal method). UI display (screens, service status bubbles, backlog indicator), memorial-woodland buildings, and regional-contract capacity export are **deferred to increment 2+**.

## Acceptance criteria

### Functional — intake: RealisedDeath → body

- **AC-1 (GR#20 inbound; US-1 from FEAT-088).** A `DeathServicesAPI` interface exists driving body intake directly off FEAT-087's RealisedDeath queue handoff: every death released from the queue produces one body record in the intake pipeline, never duplicating or dropping a citizen. Check: `go doc ./internal/engine/deathservices DeathServicesAPI` shows an intake entrypoint; a passing test streams a fixed RealisedDeath list through intake and asserts the body set has identical cardinality and IDs as the input, with no dropped/duplicated bodies (`grep -rn "func Test.*[Ii]ntake\|func Test.*[Oo]neBodyPerDeath" internal/engine/deathservices/*_test.go`). **No direct exported-field mutation** from other packages (GR#20 contract): `grep -rln "deathservices\.\w\+ = " internal/engine/*/ internal/ui/*/` (excluding `internal/engine/deathservices` itself and `_test.go`) finds none.

### Functional — graveyard plot allocation & reuse

- **AC-2 (§Part IV cemetery line 1075).** A graveyard building (registered through `engine.build`'s catalogue, consuming an `engine.build` outbound edge) has a finite plot capacity sourced from data (spec seed: 2k plots transcribed into `data/deathservices.json` with spec line ref), never hardcoded in Go (GR#15). Each burial consumes exactly one plot. Check: `grep -n "2000\|2 *000\|plots.*capacity" data/deathservices.json` shows the data source; `go doc ./internal/engine/deathservices CemeteryBuild` (or equivalent) shows plot-capacity and occupancy accessors; a passing test buries one body and asserts occupied-plots increments by 1 and available-plots decrements by 1 (`grep -rn "func Test.*[Bb]urial.*[Pp]lot\|func Test.*[Pp]lotConsum" internal/engine/deathservices/*_test.go`).

- **AC-3 (plot reuse mechanism; US-2 from FEAT-088; directional only).** A consumed plot re-enters the allocatable pool only after a configurable reuse horizon has elapsed since burial; the horizon is a data-sourced value (`data/deathservices.json`, disclosure comment naming it a placeholder) determining plot-retirement/recovery. Check: a passing test buries at tick T, asserts the plot rejects reuse for all ticks < T+horizon (as loaded from fixture), and becomes allocatable at/after T+horizon; a second test mutates the fixture's horizon value and asserts the boundary shifts with the data, proving data-driven (not compiled) (`grep -rn "func Test.*[Rr]euseHorizon\|func Test.*[Pp]lot.*[Rr]eusable" internal/engine/deathservices/*_test.go`). **Horizon length is a placeholder; do not invent a specific month count.**

- **AC-4 (§25 "fills permanently — land pressure"; US-2; saturation behavior).** When all plots in a cemetery are occupied and none has reached reuse eligibility, a new burial against that cemetery enters a documented triage path — reject with a typed error *or* auto-route to cremation/emergency-dispensation as a fallback, never silently extend capacity. Check: a passing test fills a cemetery to capacity, attempts a burial, and asserts the burial either (a) returns a typed "no-plot-available" error, or (b) shows a documented fallback route in the triage logic (`grep -rn "func Test.*[Ff]ullCemetery\|func Test.*[Ll]andPressure\|func Test.*[Ss]aturation" internal/engine/deathservices/*_test.go`). **False-pass risk:** a plot allocator that wraps around or extends capacity on overflow would satisfy "burials keep working" while erasing land pressure — the check requires the saturation behavior to be explicit and triage-routed, not silent overflow.

### Functional — cremation (costed alternative)

- **AC-5 (§Part IV crematorium line 1076; line 543 "£/service"; US-3 from FEAT-088).** Cremation disposes a body without consuming a plot, at a per-body cost (placeholder in `data/deathservices.json` per line 543's "£/service" with no figure given), bounded by the crematorium's daily throughput (spec seed: 12/d, transcribed into data with line ref). Check: `go doc ./internal/engine/deathservices CrematoriumBuild` shows throughput-capacity and per-body-cost accessors; a passing test cremates N bodies and asserts (a) zero plots were consumed, (b) the cost is > 0 and > placeholder-minimum (not zero or negative), and (c) attempting to cremate more than the 12/d seed in one day queues the excess rather than exceeding it (`grep -rn "func Test.*[Cc]remat\|func Test.*[Cc]ost.*[Bb]ody" internal/engine/deathservices/*_test.go`). Cost magnitude is a placeholder; AC checks "cost exists and is charged", not a specific £ figure.

- **AC-6 (cost routing; GR#3 single source of truth).** Cremation cost is posted through the registered `engine.services` outbound edge (not a locally-invented ledger), integrating with the service-quality and funding path that the service module owns. Check: `grep -rn "services\." internal/engine/deathservices/*.go` (excluding `_test.go`) shows a real call into `engine.services`' cost/funding surface, not a local duplicate ledger; a passing test cremates a body and asserts the cost appears in `engine.services`' records or a documented service-funding interface, proving cross-module integration (`grep -rn "func Test.*[Cc]remat.*[Cc]ost.*[Ss]ervice\|func Test.*[Cc]ost.*[Pp]ost" internal/engine/deathservices/*_test.go`).

### Functional — hearse throughput (one body per trip)

- **AC-7 (US-4 from FEAT-088; the throughput cap — death surge becomes backlog).** A hearse carries exactly one body per trip. Total disposal throughput is therefore bounded by `(fleet size) × (trips per unit time)`, sourced as a monthly throughput budget from data (placeholder), so a death surge released by FEAT-087's non-smoothed event cannot be cleared faster than that cap and instead accumulates as a queryable unhandled-body backlog. Check: `go doc ./internal/engine/deathservices HearseFleet` (or equivalent) shows a per-trip capacity field = 1; `grep -n "hearse.*budget\|throughput.*month" data/deathservices.json` (or equivalent field name) shows the monthly transport budget as a data source; a passing test releases N deaths where N exceeds the monthly hearse budget and asserts (a) unhandled-body backlog grows exactly by `N − cleared`, and (b) no hearse trip carries > 1 body (`grep -rn "func Test.*[Hh]earse\|func Test.*[Oo]nePerTrip\|func Test.*[Bb]acklog.*[Ss]urge" internal/engine/deathservices/*_test.go`). **False-pass risk:** a hearse whose capacity field reads 1 but scheduler loops to clear the backlog in one tick would satisfy the field while destroying queue-drains behavior — check requires backlog persistence across ticks at the cap.

- **AC-8 (GR#20 outbound edge; hearses consume `engine.logistics` movement type).** Hearse movement is expressed through the registered movement owner `engine.logistics` (the code.json outbound edge `engine.deathservices → engine.logistics`, registered 2026-08-20) rather than a bespoke hearse-only router. A hearse trip is a logistics movement subject to the same congestion/queueing rules as any logistics round. Check: `grep -rn "logistics\." internal/engine/deathservices/*.go` (excluding `_test.go`) shows a live call into `engine.logistics`' exported movement surface, not locally-duplicated routing logic; a passing test asserts a hearse trip is delayed by the same junction saturation that would delay any logistics round, proving shared ownership (`grep -rn "func Test.*[Hh]earse.*[Cc]ongestion\|func Test.*[Mm]ovementShared" internal/engine/deathservices/*_test.go`).

- **AC-9 (coarse month-level transport budget; inc1 scope).** Hearse/crematorium/emergency dispensation throughput in increment 1 is expressed as a *monthly budget* (coarse, aggregated), not per-vehicle routing or sub-tick scheduling. A body in the disposal pipeline is either (a) cleared in the current month, or (b) deferred to the next month backlog, based on available transport/capacity budget. Per-vehicle routing, real-time scheduling, and sub-tick movement cycles are **deferred to increment 2** (`MOD-083-inc2.md`). Check: the throughput tests (AC-7, AC-10, AC-11 below) assert month-level totals, not per-tick/per-vehicle counts.

### Functional — emergency dispensation (weather-driven major death event)

- **AC-10 (US-5 from FEAT-088; the trigger and escape).** Dispensation mode activates exactly when FEAT-087's weather-driven major death event is active (reading `feat.deathwave`'s event signal through the registered inbound edge) and deactivates immediately when the event ends. Dispensation does NOT re-implement weather detection (GR#3); it reads the *same* signal FEAT-087 consumes from `engine.season`. Check: `grep -rn "deathwave\.\|feat.deathwave.*[Ee]vent\|WeatherEvent\|emergencyFlag" internal/engine/deathservices/*.go` (excluding `_test.go`) shows the dispensation gate reads FEAT-087's event state, not a local weather calculator; a passing test activates the event and asserts dispensation becomes active in the same tick, then deactivates and asserts dispensation returns to normal mode (`grep -rn "func Test.*[Dd]ispensation.*[Gg]ate\|func Test.*[Dd]ispensation.*[Ee]vent" internal/engine/deathservices/*_test.go`).

- **AC-11 (US-5; vans/trucks + higher throughput + 24×7; directional only).** While dispensation is active, bodies may be transported in non-hearse vehicles (vans/trucks) carrying **more than one body per trip**, and services run 24×7 (removing normal operating-hours idle), so total disposal throughput exceeds the normal one-body-per-trip hearse-monthly-budget ceiling. The multi-body-per-trip count and the 24×7 throughput multiplier are data-sourced placeholders (`data/deathservices.json`), not hardcoded. Check: `go doc ./internal/engine/deathservices DispensationMode` exists; a passing test with dispensation active asserts (a) a trip carries more than one body (contra AC-7's normal cap), and (b) total disposal throughput over a fixed month exceeds the normal hearse-only budget, bounded by the data-sourced multiplier (`grep -rn "func Test.*[Dd]ispensation.*[Tt]hroughput\|func Test.*[Vv]an.*[Mm]ulti" internal/engine/deathservices/*_test.go`). **Directional-only:** test asserts the *lift exists and is bounded by data*, not a specific van count or multiplier magnitude.

- **AC-12 (US-5; dispensation reverts on event end).** When FEAT-087's weather event ends, dispensation deactivates: multi-body transport is no longer permitted, 24×7 operation ceases, and the normal one-body-per-trip cap + normal throughput budget resume. Check: a passing test drives dispensation on, transports bodies via multi-body trip, then ends the event and asserts (a) a subsequent multi-body trip is rejected with a typed error, and (b) the one-body cap is re-enforced (`grep -rn "func Test.*[Dd]ispensation.*[Rr]evert\|func Test.*[Ee]vent.*[Ee]nd" internal/engine/deathservices/*_test.go`). **False-pass risk:** dispensation that is set but never cleared would satisfy "lifts the cap" on a shallow read while leaving the city in permanent emergency throughput — check requires reversion.

- **AC-13 (AC-12 independent point: wellbeing/approval penalty under dispensation, to be specified by Aaron).** While dispensation is active, a wellbeing and/or approval penalty applies (documenting the cost of mass disposal). The penalty magnitude is a placeholder (`data/deathservices.json` disclosure comment). Check: `grep -n "dispensation.*penalty\|emergency.*wellbeing" data/deathservices.json` shows a placeholder entry; a passing test runs dispensation, measures wellbeing/approval deltas, and asserts both change negatively by an amount ≥ placeholder-minimum (not zero, not inverted to positive) (`grep -rn "func Test.*[Dd]ispensation.*[Pp]enalty\|func Test.*[Ee]mergency.*[Cc]ost" internal/engine/deathservices/*_test.go`). **Magnitude is a placeholder; do not invent a specific wellbeing-point or approval-percent figure.**

### Functional — conservation identity

- **AC-14 (US-6 from FEAT-088; the load-bearing identity).** For every accounting period (tick or month, documented which), the following identity holds exactly:
  ```
  BodiesReleased == BodiesAwaitingHandling 
                  + BodiesEnRoute 
                  + BodiesBuried (occupying plots) 
                  + BodiesCremated 
                  + BodiesHandledByDispensation
  ```
  Each right-hand term is independently sourced (released from FEAT-087 ledger, awaiting from intake backlog, en-route from transport assignment, buried from plot occupancy, cremated from crematorium ledger, dispensation from its own ledger) and the identity is *checked*, never constructed as `BodiesReleased − (others)`. Check: a passing test runs a synthetic death surge, independently sums all right-hand terms from their own accessors each period, and asserts the sum equals `BodiesReleased` exactly (integer bodies) (`grep -rn "func Test.*[Bb]odyConserv\|func Test.*[Bb]ody.*[Ii]dentity\|func Test.*[Cc]onserved" internal/engine/deathservices/*_test.go`). **False-pass risk:** computing any term as `BodiesReleased − (others)` would make the identity tautologically true and hide a body being dropped or double-counted — check requires all six terms independently tracked.

- **AC-15 (handled exactly once — terminal exclusivity).** Burial, cremation, and dispensation are mutually exclusive terminal states: a body is disposed of by exactly one method, exactly once, and a handled body is removed from awaiting/en-route sets and never re-assigned, re-counted, or re-disposed. Check: a passing test buries a body and asserts it no longer appears in awaiting or en-route; a second attempt to dispose the same body returns a typed error; a test disposes one body by cremation and asserts it never appears in buried-plot occupancy (the two methods never co-apply to one body) (`grep -rn "func Test.*[Ee]xactlyOnce\|func Test.*[Dd]oubleDispos\|func Test.*[Tt]erminal" internal/engine/deathservices/*_test.go`).

### Functional — old-save compatibility

- **AC-16 (breaking-change safety).** Saves from prior releases (without `deathservices` state) remain loadable: a restored city with zero graveyard/crematorium/hearse/dispensation state behaves correctly (no nil-pointer panics, no silent corruption). Check: a passing test loads a fixture save predating this module, asserts all death-services accessors return sensible zero-like values (empty body set, zero occupancy, zero disposed), and asserts intake proceeds normally without crashes (`grep -rn "func Test.*[Oo]ld.*[Ss]ave\|func Test.*[Ll]oad.*[Zz]ero" internal/engine/deathservices/*_test.go`).

### Error handling

- **AC-17 (GR#7 error registry).** All error conditions produce registry-sourced codes (new codes claimed via `tools/plan/add-error.js claim-range` against the G-layer on `data/errors.json` before build; no self-minting). Examples of errors to handle with registry codes: unknown body ID (AC-14), unregistered cemetery (AC-2), plot full (AC-4), crematorium full (AC-5), multi-body trip outside dispensation (AC-11), unknown building type. Check: `grep -n "MET-" internal/engine/deathservices/*.go` shows registry code references; a passing test for each error case asserts (a) the returned error matches a registry code, and (b) no side-effect record was created (no phantom body, no plot allocated, no trip scheduled) (`grep -rn "func Test.*[Ee]rror\|func Test.*[Rr]egistry" internal/engine/deathservices/*_test.go`). **GR#7 assertion, explicit:** test asserts error code match AND absence of side effects, not merely that a matching test function exists. **Do not mint error codes in Go; name the errors, then claim range in the registry flow.**

### Determinism & safety

- **AC-18 (GR#21 determinism; byte-identical across worker counts).** Body intake order, plot allocation, hearse/vehicle assignment, and dispensation activation are deterministic functions of `(worldSeed, tick, prior state, commands)` — repeated runs from an identical starting snapshot and command sequence produce byte-identical backlog, plot-occupancy, and disposal-ledger state across worker counts. Tie-breaks on map iteration are documented and deterministic (never random or order-dependent). Check: `grep -n "sort\." internal/engine/deathservices/*.go` shows deterministic ordering on any map feeding assignment; a passing determinism test runs the same command sequence at worker counts 1 vs 14 and asserts byte-identical state (`grep -rn "func Test.*[Dd]eterminis\|func Test.*[Ww]orkerCount" internal/engine/deathservices/*_test.go`). **Never assert wall-clock upper bounds; assert tick/state equality only.**

- **AC-19 (SG-7; no wall-clock reads).** `grep -rn "time.Now\|time.Since" internal/engine/deathservices/*.go` (excluding `_test.go`) returns no matches — reuse horizon, hearse round time, and dispensation 24×7 window are functions of simulation tick/month index only, never wall clock.

- **AC-20 (concurrency; no data races).** `go test ./internal/engine/deathservices/... -race -count=1` passes; at least one concurrency test exists (concurrent intake from death queue while hearses/cremation resolve, within a tick) (`grep -n "go func()" internal/engine/deathservices/*_test.go`).

### Documentation

- **AC-21 (doc.go — module narrative).** `internal/engine/deathservices/doc.go` states the module key `engine.deathservices`, cites Part IV lines 1075-1079, line 543, §5/§26/§25, §9, documents the body-conservation identity (AC-14, all six terms named), the plot-reuse contract (AC-3), the one-body-per-trip cap (AC-7), and the dispensation activation/reversion gate (AC-10/AC-12). Check: `grep -n "engine.deathservices\|§26\|§25" internal/engine/deathservices/doc.go` matches; `grep -n "BodiesReleased\|conservation" internal/engine/deathservices/doc.go` shows the identity in prose.

- **AC-22 (data.deathservices.json metadata).** `data/deathservices.json` carries a `$comment`/`meta` block naming MOD-083, transcribing spec-given capacities (2k plots, 12/d) with line refs, and marking every non-spec value — reuse horizon, transport budget, cremation cost, van/truck multi-body count, dispensation multiplier, wellbeing/approval penalty — as placeholders with disclosure comment and unit of measure. Check: `grep -n "MOD-083\|engine.deathservices" data/deathservices.json` (as a JSON comment/meta field) matches; each placeholder field carries a non-empty disclosure string.

## Out of scope — increment 1

- Mortality and smoothing (FEAT-087 `feat.deathwave`); this module only consumes the released death queue.
- `engine.build`'s own construction/zoning and `buildings.json` catalogue — only consumed here.
- `engine.services` funding/quality/staffing allocation — only charged here.
- Memorial woodland (catalogue line 1077, "park hybrid") — third disposal option, deferred.
- Regional-contract capacity export ("£/service", line 543) — deferred to increment 2+.
- UI screens, status bubbles, backlog indicators, service-quality display — **deferred to increment 2**.
- Real per-vehicle routing, sub-tick movement cycles, per-hearse scheduling — **deferred to increment 2** (AC-9: month-level budget only in increment 1).
- Exact numeric values for reuse horizon, transport budget, cremation cost, van count, dispensation multiplier, penalties — all placeholders, pending Aaron's balance pass.

## Assumptions for Aaron (placeholders requiring guidance)

The following numeric/design placeholders are awaiting Aaron's ruling:

1. **Plot reuse horizon (months).** AC-3 checks the mechanism (eligibility after N months) but does NOT invent a month count. Data file placeholder with disclosure comment.

2. **Hearse/crematorium monthly transport budget (bodies/month).** AC-7 checks the throughput cap (bodies/month, bounded, not per-tick), but magnitude is a data placeholder. Deferred to Aaron's balance pass.

3. **Cremation per-body cost (£).** AC-5 checks the cost is posted and > 0, but magnitude is line-543-unspecified. Data placeholder with disclosure.

4. **Van/truck multi-body count under dispensation (bodies per trip).** AC-11 checks the lift exists and is > 1, but exact count is a placeholder. Data placeholder.

5. **Dispensation throughput multiplier vs normal hearse budget.** AC-11 checks total disposal throughput exceeds normal budget, but the multiple is a placeholder. Data placeholder.

6. **Wellbeing/approval penalty during dispensation (points/percent).** AC-13 checks a penalty is applied and is negative, but magnitude is a placeholder. Data placeholder.

7. **Body backlog capacity ceiling.** If there is a maximum backlog (before emergency dispensation kicks in or the city faces a crisis), that threshold is a placeholder.

8. **Penalty timing**: Does the wellbeing/approval penalty apply continuously during the dispensation event, or once at the end? Design ruling required; AC-13 checks existence, not timing.

## Deliverables checklist

- [ ] `internal/engine/deathservices/` package created with `api.go`, `cemetery.go`, `crematory.go`, `hearse.go`, `dispensation.go`, `conservation.go`, `doc.go`, and `*.go` implementation files.
- [ ] `internal/engine/deathservices/*_test.go` test files covering all 22 ACs (minimum ~12 test functions named in the grep patterns, scaling to ~22 for full coverage).
- [ ] `data/deathservices.json` created with spec-transcribed capacities (2k plots, 12/d, etc.) and placeholder entries for reuse horizon, transport budget, costs, multipliers, penalties, with disclosure comments and units.
- [ ] Error registry codes claimed via `tools/plan/add-error.js claim-range` for engine.deathservices on the G-layer, and new `MET-Gxxxx` references in source files.
- [ ] `go test ./internal/engine/deathservices/... -race -count=1` passes.
- [ ] `go vet ./internal/engine/deathservices/...` passes.
- [ ] `gofmt -l ./internal/engine/deathservices/` shows no unformatted files.
- [ ] BOW reference: commit hash(es) recorded via `node claude-bow.js ref MOD-083 <hash>` after commit.
- [ ] Destructive verdict recorded (independent attacker, verdict recorded on BOW item) before merge.

## Spec-fold amendments (if any)

(None at writing.)

## Escalations

- **For Aaron — pending design ruling on AC-13 wellbeing/approval penalty timing.** Does the penalty apply continuously during dispensation, or once at the end? AC-13 checks existence and direction (negative), not timing.

- **For Aaron — pending balance pass on all eight placeholders** (reuse horizon, transport budget, costs, multipliers, penalties, backlog ceiling). All magnitudes live in `data/deathservices.json` with disclosure; ACs check mechanism and direction, not figures.

- **For Bill/Ben — old-save compatibility (AC-16).** If a prior release had *any* deathservices-like state, fixture construction should use a realistic pre-incident save. If this is the first shipping, a zero-state restore is sufficient.

- **For the junior coder — do not mint error codes.** Name all errors in prose; the registry-claim step via `tools/plan/add-error.js claim-range` assigns codes. Use the claimed `MET-Gxxxx` codes in source once the claim is approved.

