BOW code: FEAT-087

# Acceptance criteria — feat.deathwave (FEAT-087)

**BOW code:** FEAT-087
**Mkey:** `feat.deathwave`
**Spec refs:** §5.1 (`docs/METROPOLIS-MASTER-v2.1.md` line 168: "deaths are per-person monthly hazard draws"); §5.2 (line 178: cold-pass mortality hazard `h(age, healthBand, healthcareAccess)` on a Gompertz-Makeham curve); §9 Seasonality (line 228: minor winter health wave, summer water stress); §10 (line 232: "deaths are continuous, so is deathcare demand"); §H Health & Deathcare (lines 1064-1079: cemetery "fills permanently — land pressure", crematorium 12/d, hearses); §14/§19 (nothing despawns — the death queue must conserve people, not create or destroy them). **Existing machinery this item extends/consumes:** `engine.citizens` AC-11 (the Gompertz-Makeham `MortalityHazard`/`MortalityDeath` — `internal/engine/citizens/mortality.go`); `engine.season` (`SeasonAPI` month-index curves — `internal/engine/season/`). **Downstream consumer:** FEAT-088 `feat.deathservices` (graveyards, cremation, hearses at one body per trip, emergency dispensation). code.json: `engine.citizens` (GUID `99e0d1f5-0214-4b06-bcde-caba0b1e44ad`) and `engine.season` (GUID `5a348a85-15ba-46d9-aa61-0605f12785f1`).
**Date:** 2026-08-15 (BA first pass)
**Status:** ready — extends `engine.citizens` (which is `open`); consumes `engine.season` (which is `open`); handoff target FEAT-088 is `open`. This item's criteria are written ahead of those dependencies per the draft-ahead convention used by `feat.disasters.md`/`feat.weathermode.md`.
**Package under test:** `internal/engine/citizens/` (path assumption — FEAT-087 "extends engine.citizens (mortality)"; confirm at dispatch whether the smoothing/queue lands in `internal/engine/citizens/` or a sibling `internal/engine/mortality/` package, see Escalations).
**Standard gates:** see `README.md` — all apply, package for SG-4/SG-7 is `./internal/engine/citizens/...`.

## User stories

- **US-1.** As the mortality model, I need deaths to be *realised* through a bounded monthly death queue rather than the moment `MortalityDeath` returns true, so a same-birth-month cohort whose Gompertz hazard crosses the aging cliff together produces a smooth ~N-deaths/month tail across a window of months, never a single-month population cliff.
- **US-2.** As the balance pipeline, I need the monthly death budget to be a data-file-sourced parameter (deaths per month, or an equivalent rate), so retuning "how smooth is smooth" is a data edit under GR#15, never a recompiled constant — and its only spec'd property is directional: ~N deaths/month is smooth, not a 2% population cliff.
- **US-3.** As the weather/season surface, I need a *declared weather emergency* (drought, winter) to suspend the smoothing budget, so a genuine adverse-weather event can produce a major, non-smoothed death event — the one legitimate cliff — rather than being silently absorbed into the smooth queue and never seen.
- **US-4.** As FEAT-088 (death services), I need the realised death queue to be a queryable, deterministically-ordered handoff surface (who died, in what month, under emergency or not), so funeral throughput (hearses, one body per trip) can consume it — and so its own throughput can gate how fast the queue drains, rather than the mortality path hardcoding a funeral rate.
- **US-5.** As `engine.invariant`, I need the death queue to conserve people — a selected-but-not-yet-realised citizen still counts as alive until realised, and over any window total realised deaths equals total selected deaths — so smoothing defers death but never silently despawns, duplicates, or resurrects a citizen (§14/§19).
- **US-6.** As GR#21, I need the queue ordering and the emergency declaration to be pure functions of `(worldSeed, month)` — byte-identical across worker counts and fidelity mixes — because the queue is a city-wide structure fed by many shards' cold passes, and its order must never depend on which shard finished first.

## Scope

The death-queue smoothing mechanism (select from the existing Gompertz-Makeham hazard, enqueue, realise at a bounded monthly rate), the data-file monthly death budget, the weather-emergency suspension (drought/winter permitting a non-smoothed major death event), and the ordered handoff surface FEAT-088's death services drain. It extends `engine.citizens`' existing `MortalityHazard`/`MortalityDeath` (which it does **not** re-derive) and consumes `engine.season`'s weather surface for the emergency declaration.

## Acceptance criteria

### Functional — the smoothing mechanism

- **AC-1 (US-1; the cohort cliff is provably killed — the load-bearing AC).** Deaths are not realised inline by `MortalityDeath`. A distinct realisation path exists that enqueues each hazard-selected death and releases at most the monthly death budget per (non-emergency) month. Check: `go doc ./internal/engine/citizens DeathQueue` (or equivalent) shows enqueue/realise separation from `MortalityDeath`; a passing test constructs a large same-`birthMonth` cohort aged so its hazard sits on the steep Gompertz slope (hazard ≫ background, so `MortalityDeath` selects a large fraction of the cohort in one month), runs the death pipeline for that month with smoothing on, and asserts (a) realised deaths in that month ≤ the budget, and (b) the un-realised remainder is retained in the queue, not killed and not lost (`grep -rn "func Test.*[Cc]ohortCliff\|func Test.*[Ss]moothing" internal/engine/citizens/*_test.go`). **False-pass risk (stated because this is the item's whole point):** an implementation that merely averages the death *count* across months for reporting while still removing the full cohort from the living population in one month would show a smooth graph and a cliff underneath. The check requires the living-population delta in the cliff month to be ≤ budget — a genuine 2%-of-population one-month cliff must be structurally impossible while smoothing holds.
- **AC-2 (US-1/US-5; smoothing defers, never destroys — the conservation half of the cliff guarantee).** The queue is complete and conservative: after the cohort cliff's selection wave, running enough subsequent months (with no new selections) empties the queue, and total realised deaths over the whole run equals total selected deaths exactly — no death lost, none duplicated. Check: a passing test drives a cohort through selection → full drain and asserts `totalRealised == totalSelected` and `queueLength == 0` at the end (`grep -rn "func Test.*[Cc]onserve\|func Test.*[Dd]rain" internal/engine/citizens/*_test.go`). This is the check that proves smoothing is *delay*, not a cull or a leak (§14/§19, GR#12).
- **AC-3 (US-5; queued citizens are alive until realised, and selected once).** A citizen selected for death in month `m` (a) still counts in the living population (and any age/aggregate it would normally contribute to) until its month of realisation, and (b) does not draw mortality again in `m+1` — the queue entry is the single, terminal selection event. Check: a passing test selects a citizen, advances a month, and asserts the citizen is still present in the living set, still ages normally, and its death is realised exactly once (`grep -rn "func Test.*[Qq]ueued.*[Aa]live\|func Test.*[Ss]ingleEntry" internal/engine/citizens/*_test.go`).
- **AC-4 (US-6; deterministic FIFO ordering).** Queue realisation order is a documented deterministic total order — FIFO by (selection month, then citizen id) — so two runs with the same seed and command log produce the identical realisation sequence. Check: `go doc ./internal/engine/citizens DeathQueue` documents the order; a passing determinism test runs the same cliff scenario twice and asserts byte-identical realisation sequences (`grep -rn "func Test.*[Dd]eterministicQueue\|func Test.*[Ff]ifo" internal/engine/citizens/*_test.go`).
- **AC-5 (US-2; GR#15 — budget is data, not a constant).** The monthly death budget is loaded from a data file (e.g. `data/mortality.json`; confirm filename at dispatch), not a hardcoded Go literal, and the file carries a `$comment`/`meta` block marking the value a placeholder pending Aaron's balance pass. Check: `grep -n "\$comment\|\"meta\"" data/mortality.json` (or equivalent) matches and states "placeholder pending balance pass"; `grep -nE "[0-9]{2,}" internal/engine/citizens/*.go` (excluding `_test.go`) finds no bare budget literal outside doc-comment prose. No numeric value is pinned by this file — the only spec'd property is directional (~N/month smooth, not a 2% cliff).

### Functional — the weather trigger

- **AC-6 (US-3; emergency suspends smoothing — the suspension itself is mechanical).** When a declared weather emergency is active, the monthly budget is lifted (or replaced by a documented emergency throughput), so the queue realises non-smoothed: a major death event. Check: a passing test loads the queue with many selected deaths (beyond the normal budget), declares an emergency, runs the month, and asserts realised deaths ≫ the normal budget and the queue drains in that month (`grep -rn "func Test.*[Ee]mergency.*[Ss]uspend\|func Test.*[Mm]ajorDeathEvent" internal/engine/citizens/*_test.go`). **False-pass risk:** an implementation that raises the *graph* during an emergency while still drip-releasing the queue at the normal budget would show a "major event" in presentation and none in the population. The check requires the realised-death delta in the emergency month to exceed the normal budget.
- **AC-7 (US-3; the emergency is weather-driven, sourced through the engine.season edge — not a local calendar).** The emergency declaration consumes `engine.season`'s weather surface through its registered call (the `engine.citizens → engine.season` outbound edge), never a mortality-local 12-entry table duplicating seasonality (GR#3). Check: `grep -rn "season\.\(SeasonAPI\|.*Multiplier\|.*Modifier\)" internal/engine/citizens/*.go` (excluding `_test.go`) shows a real call into `engine.season`; a passing test asserts the emergency flag is raised for at least one drought month and one winter month and not raised for at least one mild month, in the documented direction (`grep -rn "func Test.*[Ww]eatherEmergency\|func Test.*[Ee]mergencyDeclaration" internal/engine/citizens/*_test.go`). Direction/shape only — the exact threshold by which a month counts as "drought" or "winter" is **not** invented here (see ASM-579, Escalations); the AC requires the signal to route through `engine.season`, not that this BA supply the cutoff.
- **AC-8 (US-3; scope honesty — suspension of smoothing ≠ inflation of the hazard).** The emergency suspends the *smoothing budget* (the throughput cap) and does not itself modify the underlying Gompertz-Makeham hazard. Any weather-driven *elevation* of mortality is `engine.season`'s `HealthWaveModifier` consumed separately by the hazard path, not a second weather multiplier smuggled into the smoothing layer. Check: a passing test asserts that, for a fixed population whose hazard selections are held constant, the emergency changes *how many* selected deaths are realised this month but not *which citizens* the hazard selected (`grep -rn "func Test.*[Hh]azardUnchanged\|func Test.*[Ss]uspendOnly" internal/engine/citizens/*_test.go`). This keeps the item honest that "adverse weather permits a major death event" is a smoothing-suspension mechanism, not a second mortality model.

### Functional — handoff to death services (FEAT-088)

- **AC-9 (US-4; the ordered handoff surface).** The death queue exposes realised deaths as a queryable, deterministically-ordered stream carrying at minimum `(citizenId, deathMonth, emergencyFlag)` — enough for FEAT-088 to drive funeral throughput (hearses, one body per trip) in order, with no additional query needed by the consumer. Check: `go doc ./internal/engine/citizens RealisedDeath` (or equivalent) shows the three fields; a passing test realises a mixed month (some emergency, some not) and asserts the handoff surface enumerates them in the AC-4 FIFO order with correct per-record flags (`grep -rn "func Test.*[Hh]andoff\|func Test.*[Rr]ealisedDeath" internal/engine/citizens/*_test.go`).
- **AC-10 (US-4; the emergency flag rides the handoff).** A death realised during a declared emergency is tagged as such on the handoff surface, so FEAT-088 can switch to emergency dispensation (vans/trucks, 24×7) without re-deriving the weather state itself. Check: a passing test realises deaths inside and outside an emergency window and asserts the `emergencyFlag` differs correctly (`grep -rn "func Test.*[Ee]mergencyFlag" internal/engine/citizens/*_test.go`).
- **AC-11 (US-4; the boundary — mortality owns the queue, death services own the drain rate).** The death-services drain rate (funeral throughput) is an injected/queried capacity, not a constant inside the mortality path — so FEAT-088's hearse capacity can gate how fast the queue empties, and this package does not hardcode a funeral rate. Check: `go doc ./internal/engine/citizens DeathQueue` shows a drain-capacity input (e.g. `Realise(deathsPerMonth)` or an injected throughput source); `grep -nE "[0-9]{2,}" internal/engine/citizens/*.go` (excluding `_test.go`) finds no bare funeral/hearse-rate literal. (Whether the *smoothing* budget and the *funeral* throughput are one parameter or two is a cross-item question for FEAT-088 — see ASM-580, Escalations — not resolved here.)

### Error handling

- **AC-12 (GR#7).** A malformed or schema-invalid budget data file (missing budget, negative budget, non-integer budget) produces a registry-sourced error (new `MET-E`-range code) at load time, never a silent default-to-1 or unbounded-budget substitution that would silently re-enable the cliff. Check: `grep -n "MET-" internal/engine/citizens/*.go` finds a registry code reference; passing test coverage (`grep -rn "func Test.*[Mm]alformed.*[Bb]udget\|func Test.*[Ii]nvalidBudget" internal/engine/citizens/*_test.go`) — **GR#7 assertion, stated explicitly (BUG-100 convention):** the test asserts the returned error's registry code matches AND that no default budget was silently substituted, not merely that a matching-named test function exists.
- **AC-13 (GR#7).** Requesting realisation of a citizen not in the queue, or a double realisation of the same queued death, returns a registry-sourced error (or is a documented idempotent no-op), never a phantom death or a duplicated corpse. Check: passing test coverage (`grep -rn "func Test.*[Nn]otQueued\|func Test.*[Dd]oubleRealise" internal/engine/citizens/*_test.go`) — **GR#7 assertion:** the test asserts the returned error's registry code matches AND that no extra death record was created.

### Determinism & safety

- **AC-14 (GR#21).** Every stochastic draw feeding the queue (the underlying hazard selection) and every ordering tiebreak uses the counter-based hash stream `hash(worldSeed, id, month, purposeTag)` — no shared/global RNG anywhere in the package. Check: `grep -n "rand.New\|math/rand\"" internal/engine/citizens/*.go` (excluding `_test.go`) finds no shared/global RNG source; the AC-4 FIFO tiebreak keys on `(selectionMonth, id)` and is seed-independent where the spec requires it.
- **AC-15 (US-6; worker-count invariance — the cross-shard determinism risk, stated explicitly).** The death queue is a city-wide structure fed by many shards' cold passes; its realisation sequence must be a pure function of `(worldSeed, command log)`, never of the order shards happened to complete on a given worker count. Check: a passing invariance test runs the same seed + command log at two worker counts (1 vs 14, the M0-ENG §1.2 pattern) and asserts byte-identical realised-death sequences (`grep -rn "func Test.*[Ww]orkerCount\|func Test.*[Ss]hardOrder" internal/engine/citizens/*_test.go`). This is the check that catches a queue whose entry order leaks shard-completion order.
- **AC-16 (SG-7 scoped; GR#21).** `grep -rn "time.Now\|time.Since" internal/engine/citizens/*.go` (excluding `_test.go`) returns no matches — smoothing and emergency declaration are driven by simulation month, never wall clock.
- **AC-17.** `go test ./internal/engine/citizens/... -race -count=1` passes with no data race across the city-wide death queue being written by the per-shard cold pass and read by the realisation/handoff path concurrently. Check: `grep -n "go func()" internal/engine/citizens/*_test.go` finds at least one concurrency test.

### Documentation

- **AC-18.** `internal/engine/citizens/doc.go` states the mkey `feat.deathwave`, cites §5.1/§5.2/§9/§10/§H, and documents in prose: the smoothing mechanism (hazard selects → queue → bounded monthly realisation), the data-file budget, the weather-emergency suspension, the FIFO ordering, and the FEAT-088 handoff surface. Check: `grep -n "feat.deathwave" internal/engine/citizens/doc.go` and `grep -n "§H\|deathcare" internal/engine/citizens/doc.go` both match, and the smoothing/emergency/handoff terms appear.
- **AC-19.** The budget data file's `$comment`/`meta` block cites §5.2/§9/§H, names `engine.season` as the emergency source and FEAT-088 as the handoff target, and states every numeric value is placeholder pending Aaron's balance pass (per the standing balance-number regime, Vestige `metropolis-balance-number-regime`). Check: `grep -n "\$comment\|\"meta\""` on the data file matches; `grep -n "placeholder\|pending.*balance"` matches.

## Cross-check (code.json edges — findings, not changes)

- **`engine.citizens → engine.season` outbound edge must be registered (GR#20).** As of writing, `code.json`'s `engine.citizens` `outbound.calls` lists only `engine.core`, `int.serializer`, `engine.invariant` — no `engine.season`. FEAT-087 makes the mortality path call `SeasonAPI`, which requires (a) adding `engine.season` to `engine.citizens.outbound.calls`, and (b) the mirror registration `engine.citizens` appearing in `engine.season.inbound.consumers` (which currently lists `engine.build`, `engine.cafe`, `engine.consumption`, `engine.education`, `engine.farming`, `engine.projections`, `engine.tourism`, `engine.wellbeing` — not `engine.citizens`). `tools/plan/generate.js` mirrors the two sides, so this is one registration through the master-plan SSOT flow (`/register-guid`). **This BA is flagging the edge, not registering it** (registration is Ben's/Bill's call per the SSOT flow).
- **The "declared weather emergency" surface does not exist yet.** `engine.season`'s `SeasonAPI` exposes pure month-index curves (`HealthWaveModifier`, `WaterDemandMultiplier`, etc.) — there is no `IsWeatherEmergency`/drought function, and "drought" specifically is `feat.disasters`'s aquifer-drought category (FEAT-012), not an `engine.season` curve. So the exact source of the emergency signal (derive a threshold from existing season curves within this feature vs. add a new `engine.season` surface vs. consume `feat.disasters`) is unresolved — logged as ASM-579, and AC-7 is written to require only that the signal routes through the `engine.season` call edge, not that this BA invent the cutoff.

## Out of scope

- `engine.citizens`' own Gompertz-Makeham hazard and `MortalityHazard`/`MortalityDeath` — this item consumes them (AC-1 enqueues their output); it does not re-derive or re-tune the actuarial curve (that is `engine.citizens` AC-11's surface and the balance pass's job).
- `engine.season`'s curve mechanics — this item consumes the weather surface; it does not add curves, thresholds-as-data, or an emergency signal to `engine.season` (if a new surface is needed, that is an `engine.season` extension routed to its owning item).
- `feat.disasters`' aquifer-drought event machinery — a true drought *event* is FEAT-012's; if the emergency declaration must consume it, that is a cross-item dependency, not something this item silently re-implements.
- FEAT-088's death services themselves (graveyards, cremation, hearses, emergency dispensation) — this item only produces the ordered, flagged handoff stream; it does not build the funeral throughput or the emergency-transport rules.
- Exact numeric values for the monthly budget, the smoothing window length, and any emergency threshold — all placeholder, data-file-sourced, pending Aaron's balance pass (GR#15, standing balance-number regime); every AC above checks mechanism/direction/shape, never a pinned figure.

## Escalations

- **For Bill/Aaron — the central mechanism ruling this file commits to.** The BOW title says "bounded monthly death budget" and design decision (1) says "a death queue so ~N die per month". This file reads the budget as a *throughput cap* (realise at most N/month, defer the rest, never lose a death — AC-1/AC-2), so total deaths always equals total hazard-selected deaths and smoothing is pure delay. The alternative reading — a *cull* that silently drops deaths above the budget — is explicitly rejected by AC-2/AC-5 as a GR#12/§14/§19 violation. If Aaron instead wants the budget to also bound *total* mortality (deaths above the cap are simply not modelled), that is a different design ruling this BA cannot make unilaterally; AC-2 would need rewriting.
- **For Ben — the `engine.citizens → engine.season` edge (Cross-check above).** Must be registered through the master-plan SSOT flow before/at dispatch; AC-7's `grep` and the dispatch guard's mkey/codejson checks will both fail until it is.
- **For Bill/Aaron — ASM-579 (weather-emergency source).** `engine.season` has no drought/emergency signal; drought is `feat.disasters`'s aquifer-drought, winter is `HealthWaveModifier`. Three candidate sources (derive-threshold-from-existing-curves within this feature; add a new `engine.season` surface; consume `feat.disasters`) are all plausible and differently scoped. AC-7 requires only the `engine.season` routing, but the owning party must rule on the source before the junior can build the declaration. Logged as ASM-579.
- **For Bill/Aaron — ASM-580 (budget vs funeral throughput).** FEAT-088's description says "funeral throughput gates the death rate", which could mean the smoothing budget should ultimately *be* the death-services throughput rather than an independent data parameter. This file treats them as separable (AC-11 injects the drain rate, AC-5 data-files the budget) and leaves the coupling to the FEAT-088 BA/Bill. Logged as ASM-580.
- **For Bill/Aaron — ASM-581 (queued-citizen semantics).** Whether a selected-but-unrealised citizen still ages, consumes, and appears in aggregates while queued affects GR#3 (single source of truth) and `engine.invariant`'s people-conservation stock. AC-3 commits to "alive until realised, still ages, counts in population" as the conservative default; if Aaron wants the queued to be frozen/non-participating, AC-3 changes. Logged as ASM-581.
- **For Bill (dependency risk, per the draft-ahead pattern).** This item depends on `engine.citizens` (MOD-018, `open`) and `engine.season` (MOD-027, `open`), and hands off to FEAT-088 (`open`). The concrete `SeasonAPI`/`MortalityDeath` shapes are assumed as they exist today (`mortality.go` verified); if they land differently, the owning BA must refresh AC-1/AC-7/AC-9's signatures at dispatch. Recommend stub-first build with Tester re-verification once both land.

## Spec-fold amendments (FEAT-084 SF wave, 2026-08-27)

> Substantive AC amendments folded from BA-6 ASM disposition. Where an entry conflicts with earlier wording in this file, the entry is authoritative.

### ASM-579 — weather-emergency source is SeasonAPI curves; disasters drought BLOCKED (amends AC-7)

The emergency declaration consumes **registered** `feat.deathwave` → `engine.season` (`SeasonAPI` month-index curves). It does **not** consume `engine.citizens` → `engine.season` (that edge is still absent) and does **not** consume `feat.disasters` aquifer-drought (unregistered). `SeasonAPI` has no `IsWeatherEmergency`; this file does not invent one.

Until Aaron picks otherwise, the junior derives the flag locally from existing curves vs data-file thresholds (placeholders, GR#15): winter from `HealthWaveModifier`, drought-shaped months from `WaterDemandMultiplier` — direction/shape only, no cutoff invented here. Aquifer-drought *events* stay `feat.disasters` (AC-4 there) and are out of scope.

- **AC-7 amendment.** Check: `grep -rn "season\.\(SeasonAPI\|HealthWaveModifier\|WaterDemandMultiplier\)" internal/engine/citizens/*.go` (excluding `_test.go`) shows a real `engine.season` call; `grep -rn "engine/events\|feat.disasters\|disasters\." internal/engine/citizens/*.go` (excluding `_test.go`) finds **no** disasters import; a passing test raises the emergency flag for at least one winter-shaped month and one high-water-demand month and not for a mild month, with thresholds loaded from the budget/mortality data file (`grep -rn "func Test.*[Ww]eatherEmergency\|func Test.*[Ee]mergencyDeclaration" internal/engine/citizens/*_test.go`). **Tripwire (disasters BLOCKED):** `node -e "const m=require('./code.json').modules.find(x=>x.key==='feat.deathwave'); process.exit(m.outbound.calls.some(c=>c.key==='feat.disasters')?1:0)"` must exit **0**. **False-pass:** a 12-entry mortality-local calendar, or treating every nonzero `HealthWaveModifier` as emergency with no data threshold. **register-guid:** Architect adds `feat.deathwave` → `feat.disasters` before any aquifer-drought-event prose.

**AARON-DECISION (does not pick a winner):** (a) local threshold vs existing `SeasonAPI` curves (the only graph-legal path today — default until ruled); (b) new `SeasonAPI.IsWeatherEmergency` on `engine.season`'s owning item; (c) consume `feat.disasters` aquifer-drought — **blocked** until the edge is registered. Cutoff magnitudes are balance placeholders, not this BA's numbers.

### ASM-580 — smoothing budget and funeral drain are two inputs (amends AC-11)

The monthly smoothing budget (AC-5, data-filed) and the funeral drain capacity (AC-11, injected) are **independent**. This package does not derive one from the other. Non-emergency realisation in a month is `min(budget, drainCapacity, queued)` — funeral throughput can gate how fast the queue empties without replacing the budget, and the budget can bind even when hearses have spare capacity.

- **AC-11 amendment.** Check: a passing test holds budget fixed, varies injected drain, and asserts realised deaths follow `min(budget, drain, queued)`; a second test holds drain fixed and varies the data-file budget the same way (`grep -rn "func Test.*[Bb]udget.*[Dd]rain\|func Test.*[Mm]in.*[Tt]hroughput" internal/engine/citizens/*_test.go`); `grep -nE "[0-9]{2,}" internal/engine/citizens/*.go` (excluding `_test.go`) still finds no bare funeral/hearse-rate literal. **False-pass:** computing the budget from hearse count (or the drain from the budget) inside this package, so the two knobs are secretly one. FEAT-088 still owns hearse/one-body-per-trip (`feat.deathservices.md` AC-7) — that file is not edited here.

**AARON-DECISION:** whether the two parameters should collapse to a single funeral-throughput budget. Default until ruled: two knobs, realisation is the min.

### ASM-581 — queued citizens stay alive, age, and count (amends AC-3; ruled 2026-08-27)

AC-3's conservative default is load-bearing: a selected-but-unrealised citizen remains in the living population until realisation — still ages, still contributes to this package's population aggregates, and is selected once. Queuing is delay, not a freeze or a silent cull (§14/§19). This file does not add an `engine.consumption` call (unregistered on `engine.citizens` outbound).

- **AC-3 amendment.** Check: a passing test selects a citizen, advances at least one month before realisation, and asserts (a) the citizen is in the living set, (b) age advanced, (c) the citizen still contributes to a population aggregate, (d) death realises exactly once (`grep -rn "func Test.*[Qq]ueued.*[Aa]live\|func Test.*[Ss]ingleEntry" internal/engine/citizens/*_test.go`). **False-pass:** a `queued` flag that still removes the citizen from living aggregates (smooth graph, cliff underneath — the same trap AC-1 names).

Aaron confirmed 2026-08-27: queued death-wave citizens stay alive, age, and count as ordinary residents until the budget realises them — no freeze, no drop-the-queue.

---

## Remaining increments — audit 2026-09-04

This section audits FEAT-087's current state against the acceptance criteria above, maps DONE vs OPEN work, and scopes the remaining increments required to complete this feature.

### Status summary

FEAT-087 has landed **four major increments** across **two days of concentrated build and attack work** (2026-09-01 to 2026-09-03). **The core smoothing mechanism and emergency-suspension surface are COMPLETE and CI-green.** Three critical gaps remain open:

1. **DeathQueue-pending-entries serialization** — save/reload mid-queue (a BUG-483 follow-up, P3)
2. **Weather-driven hazard variation** — multiplier modulation of death rate in adverse months (new AC, GR#15 placeholder-tier)
3. **Population-proportional death budget** — the flat 25/month is structurally wrong at 100M citizens (new AC, GR#15 data-derived, related to BUG-663 perf audit)

The first is a mechanical follow-up to inc3; the second and third are DESIGN RULING decisions Aaron must settle (outlined under "Assumptions for Aaron" below).

### DONE-vs-REMAINING map

Each AC below is marked **LANDED** (with commit hash and test evidence) or **OPEN** (with the remaining work).

#### AC-1 (cohort cliff is provably killed)

**LANDED — inc1.5 wired (c7dcba6), running LIVE.**
- **Evidence:** `coldpass_deathwave_test.go` (inc1.5's live-tick proof suite) proves hazard-selected deaths are Enqueued not removed inline.
- **Test:** `grep -rn "TestCohort\|TestCliff" internal/engine/citizens/deathwave_test.go coldpass_deathwave_test.go` — test suite directly exercises cohort scenarios, verifies queue retention over realisation.
- **Wiring:** `ColdShard.applyMonthly` in coldpass.go now calls `q.Enqueue` on hazard hit, not `removeAt` (was the "built-but-not-wired" gap Bro flagged 2026-09-01).

#### AC-2 (smoothing defers, never destroys)

**LANDED — inc1 core (9dffd52), proven by attack_feat087_inc3_handoff_test.go.**
- **Evidence:** `deathwave_test.go` TestConservationFullDrain; `attack_feat087_inc3_handoff_test.go` proves totalRealised == totalSelected over full drain.
- **Verification:** Opus round 1 (2026-09-01 17:05-17:40) exercised this under the determinism attack; ACCEPT.

#### AC-3 (queued citizens are alive until realised)

**LANDED — inc1.5 settled the semantics (ASM-581 ruling, 2026-08-27).**
- **Evidence:** `coldpass_deathwave_test.go` TestQueuedCitizenStaysAlive; population aggregates (age pyramid, census counts) include queued citizens through the month-end realisation.
- **Verification:** Bev's ruling (doc.go §"Death-queue smoothing"): queued citizens age normally, count in population, dissolve household at REALISATION not selection.

#### AC-4 (deterministic FIFO ordering)

**LANDED — inc1 core + inc3 incremental.**
- **Evidence:** deathwave.go `DeathQueue` struct and `realiseLocked` sort by `(selectionMonth, citizenID)` deterministically; worker-count-invariant test in determinism_test.go.
- **Test:** `grep -n "TestDeterminism\|TestWorkerCount\|TestFifo" internal/engine/citizens/determinism_test.go` — runs same seed at 1 vs 14 workers, asserts byte-identical realised sequence.

#### AC-5 (budget is data, not a constant)

**LANDED — inc1 core.**
- **Evidence:** `data/mortality.json` v1 with `$comment` block; `mortalityconfig.go` LoadMortalityConfig; no bare literal in citizens Go code.
- **Verification:** `grep "25" internal/engine/citizens/*.go` returns 0 (the 25/month lives ONLY in data/mortality.json).
- **Meta block:** cites §5.2/§9/§H, names engine.season as emergency source, FEAT-088 as handoff, states all values placeholder.

#### AC-6 (emergency suspends smoothing)

**LANDED — inc2 (3b4ee30).**
- **Evidence:** `weatheremergency.go` EmergencyRealise wrapper and RealiseDrained (inc3 extension of inc2).
- **Mechanism:** IsWeatherEmergency flag drives budgetFor to swap ordinary → emergency budget.
- **Test:** `weatheremergency_test.go` TestEmergencySuspendsSmoothingBudget; `attack_bug484_emergency_bypass_test.go` verifies min(budget,drain) holds on ordinary path only.

#### AC-7 (emergency is weather-driven via engine.season edge)

**LANDED — inc2 + edge registered.**
- **Evidence:** `weatheremergency.go` IsWeatherEmergency consumes engine.season.SeasonAPI; no disasters import (tripwire enforced).
- **Edge registration:** `code.json` line 3763 lists `feat.deathwave` → `engine.season` in outbound calls. VERIFIED (was missing at AC-7 original write, now present in inc2 commit).
- **Thresholds:** data/mortality.json winterHealthWaveThreshold (0.04) and droughtWaterDemandThreshold (1.2) loaded via MortalityConfig.

#### AC-8 (suspension ≠ inflation of hazard)

**LANDED — inc2 + inc3 differential proof.**
- **Evidence:** `attack_feat087_inc3_handoff_test.go` differential test: RealiseDrained with same (seed, hazard selections, month) but different (budget, emergency) produces same set of selected citizens, different realisation order.
- **Mechanism:** emission via RealiseLocked touches ONLY the realisation loop, never ColdShard.applyMonthly's hazard draw.

#### AC-9 (ordered handoff surface)

**LANDED — inc3 (88f9bce).**
- **Evidence:** `deathwave.go` RealisedDeath struct with (CitizenID, DeathMonth, EmergencyFlag); RealisedDeaths and DeathHandoffSince accessors; inc3 appends to handoff slice in realiseLocked order.
- **Test:** `attack_feat087_inc3_handoff_test.go` TestHandoffSurfaceCarriesThreeFields; verifies FIFO order matches AC-4 sorting.

#### AC-10 (emergency flag on handoff)

**LANDED — inc3 (88f9bce).**
- **Evidence:** RealiseDrained tags each handoff entry with emergencyFlag parameter; weatheremergency_test.go exercises flag correctness.
- **Invariant:** flag is per-entry, immutable at release, consumed by FEAT-088 (feat.deathservices).

#### AC-11 (injected drain capacity, ASM-580 two independent knobs)

**LANDED — inc3 (88f9bce).**
- **Evidence:** `DeathQueue.SetDrainCapacity` wires DrainCapacity interface; RealiseDrained computes `min(budget, drain, queued)` on non-emergency path only (BUG-484 Aaron ruling).
- **Test:** `attack_feat087_inc3_handoff_test.go` TestBudgetAndDrainAreIndependent; fixture tests vary budget and drain orthogonally, verifies min() rule holds.
- **BUG-484 enforcement:** emergency path ignores drain, releases min(emergency budget, queued) alone — hearse fleet cannot flatten a declared major death event.

#### AC-12 (malformed budget data produces registry error)

**LANDED — inc1 core.**
- **Evidence:** `mortalityconfig.go` validate() function; ErrMortalityDataInvalid registry code used on schema violations.
- **Test:** mortality_test.go tests non-positive budget, non-integer budget, missing unit; each asserts error code matches, no default substitution.

#### AC-13 (double-realise or not-queued is an error)

**LANDED — inc1 core.**
- **Evidence:** `deathwave.go` RealiseByID guards against ErrCitizenNotQueued, ErrDoubleRealisation.
- **Test:** `deathwave_test.go` exercises both error paths; no phantom or duplicate death created.

#### AC-14 (no shared RNG)

**LANDED — inc1 core.**
- **Evidence:** All RNG through `foundation/det.NewStream(worldSeed, id, month, purpose)` — no rand.New, no math/rand import.
- **Verification:** `grep -n "rand.New\|math/rand\"" internal/engine/citizens/*.go` returns 0 (excluding _test.go).

#### AC-15 (worker-count invariance)

**LANDED — inc1 + inc3.**
- **Evidence:** determinism_test.go TestWorkerCountInvariance runs same seed at 1 vs 14 workers; asserts RealisedSequence byte-identical.
- **Mechanism:** queue insertion order is Enqueue call order (which leaks shard completion), but FIFO release order is realiseLocked's sort by (selectionMonth, citizenID) — a pure function of queue CONTENTS, never of insertion order.

#### AC-16 (no wall clock)

**LANDED — inc2.**
- **Evidence:** IsWeatherEmergency and RealiseDrained are pure functions of (month int64), never time.Now().
- **Verification:** `grep -n "time.Now\|time.Since" internal/engine/citizens/*.go` returns 0 (excluding _test.go).

#### AC-17 (race-free concurrent access)

**LANDED — inc1 core.**
- **Evidence:** DeathQueue guarded by sync.Mutex across all mutation paths; `go test ./internal/engine/citizens/... -race` green.
- **Test:** concurrency_test.go exercises concurrent Enqueue/Realise; -race reports no data race.

#### AC-18 (documentation in doc.go)

**LANDED — inc2.**
- **Evidence:** `doc.go` §"Death-queue smoothing" (lines 170-246) documents mechanism, inc1/inc1.5/inc2/inc3 scopes, ASM-581 ruling.
- **Verification:** `grep -n "feat.deathwave\|Death-queue smoothing" doc.go` matches; all terms (enqueue, realise, emergency, handoff) present.

#### AC-19 (data file meta block)

**LANDED — inc2.**
- **Evidence:** data/mortality.json meta block (lines 4-10) cites §5.2/§9/§H; names engine.season (emergencySource) and FEAT-088 (handoffTarget); states all params placeholder.
- **Verification:** `grep "\$comment\|meta" data/mortality.json` matches all required fields.

### NEW acceptance criteria for remaining increments

The three remaining gaps require new ACs to gate their completion.

#### NEW AC-20 (DeathQueue-pending serialization)

**Scope:** `internal/engine/compose/` snapshot/restore (handled by int.serializer, not citizens' own responsibility). A DeathQueue.pending[] (the unqueued entries) must be serialized on snapshot and restored on reload, so a mid-queue save+exit+reload does not drop in-flight selections.

**Spec:** A citizen selected-but-not-yet-realised (in DeathQueue.pending) must survive a save+restore cycle byte-identically. Serialization format is int.serializer's concern; this AC requires the fields to be included.

**Test:** A determinism test (or snapshot regression test in compose/) runs month M, enqueues N citizens without realising any, saves, reloads, and asserts RealisedSequence is unchanged when the restored queue is drained.

**Evidence gap:** None — this is a follow-up after inc3. Scheduled as BUG-483 F3 (pending entries serialization, P3).

#### NEW AC-21 (Weather-driven hazard multiplier — placeholder-tier)

**Scope:** When a declared weather emergency is active, the underlying Gompertz-Makeham hazard rate itself is multiplied by a weather-driven factor (e.g., 1.1× in winter, 1.25× in drought), producing a genuine elevation in the number of deaths SELECTED (not merely realised faster from the queue). This is the "minor winter health wave" and "summer water stress" mentioned in §9 — currently consumed only by the emergency-suspension throughput, not the selection step itself.

**Design decision required:** Is this a separate call to SeasonAPI.HealthWaveModifier from the one already consumed by EmergencyRealise (ASM-579), or is it the SAME multiplier applied at TWO points (selection and realisation)? The former is a new AC; the latter is a re-tuning of the existing data-file thresholds.

**Spec:** The hazard multiplier for month M is derived from engine.season curves (direction/shape only, no invented cutoff). E.g., `hazard *= 1 + k * abs(HealthWaveModifier)` for some placeholder k. All magnitudes are data-file placeholders (GR#15).

**Test:** A passing test runs a fixed seed over 12 months, compares total hazard selections in winter vs mild months (holding population constant), and asserts winter selections > mild selections by a measurable factor (direction only, not a pinned magnitude).

**Assumption:** Aaron rules whether this is wanted at all (the current spec §9 says "minor" but does not mandate it in the death path), and if so, whether it is a separate seasonal adjustment or a re-tuning of the emergency-threshold curves.

#### NEW AC-22 (Population-proportional death budget)

**Scope:** The flat 25-deaths/month budget in data/mortality.json is a placeholder derived from a tiny (few-thousand) test city. At the 100M citizens target (BUG-663 scope), the Gompertz-Makeham hazard draws at a rate of ~2.67e-4 selections/citizen/month (measured by the destructive round), implying ~26,700 deaths/month at scale — but the budget is 25/month, starving the realisation loop and creating a backlog that defeats the smoothing purpose (smoothing works only if the budget is close to the natural rate, else queuing just defers everything forever).

**Spec:** The budget must scale with population. GR#15 requires it data-sourced, never a bare Go literal. Propose a formula in data/mortality.json: `monthlyDeathBudget = max(minFloor, populationCount × deathRatePerCapitaPerMonth)`, where:
  - `deathRatePerCapitaPerMonth` is a data-file placeholder (e.g., 2.7e-4, derived from the BUG-663 measurement)
  - `minFloor` is a safeguard against edge cases (e.g., 25) at low population
  - Both are load-time constants (loaded by MortalityConfig), consumed at each monthly realisation step in CitizensAPI.AdvanceDayTick

**Evidence for need:** BUG-663 round measured the Gompertz selections at scale; the arithmetic: 100M × 2.67e-4 / 30 ≈ 890/month, but further shaping by healthBand and healthcare access coverage (mortality.go's hazard function) narrows this — the exact rate is a balance-pass question. Interim assumption: the rate is ~1-2 orders of magnitude higher than 25/month.

**Test:** A passing test with a 1M-citizen fixture computes the budget as `max(floor, 1M × rate)` and verifies it bounds realisation as expected (no queue backlog growth per month in steady state).

**Assumption:** Aaron rules the formula shape (linear in population, or a curve?), the per-capita rate (measured from BUG-663 or a balance-pass derivation?), and the minFloor safeguard (is 25 right, or something else?).

### Assumptions for Aaron

The three remaining increments depend on these design decisions:

1. **Weather-driven hazard multiplier (AC-21):**
   - Is a separate seasonal elevation of the death SELECTION rate wanted, or is the emergency-suspension sufficient?
   - If yes: should it use the same engine.season curves as the emergency declaration (AC-7), or a separate threshold?
   - Magnitude placeholder: what factor (e.g., 1.1× to 1.5×) for winter vs summer?

2. **Population-proportional budget (AC-22):**
   - Formula shape: linear in population, or a curve (e.g., sqrt to represent infrastructure scaling)?
   - Rate derivation: use the BUG-663 measured 2.67e-4 selections/citizen/month, or a balance-pass override?
   - Minimum floor: is 25/month the right safeguard at low population, or something else?

3. **DeathQueue-pending serialization (AC-20):**
   - Not a design decision — a mechanical follow-up. Priority: P3 (after inc3 landed).
   - Owner: int.serializer (compose/) in coordination with citizens' DeathQueue.pending field exposure.

### Edge registration check

- **`feat.deathwave → engine.season` (ASM-579, AC-7):** **VERIFIED REGISTERED** in code.json (inc2 commit). The edge exists and the dispatch guard will pass.
- **`feat.deathwave → feat.disasters` (ASM-579 tripwire):** **DELIBERATELY UNREGISTERED**. Tests assert no disasters import; registering this edge is a later feature (FEAT-012's own requirement to be consumed).

### Test evidence summary

FEAT-087's test suite is comprehensive and landed incrementally:

- **inc1 core:** `deathwave_test.go` (AC-1..5, AC-12..14, AC-16/17 determinism setup)
- **inc1.5 wiring:** `coldpass_deathwave_test.go` (live-tick proof that AC-1/AC-2/AC-3 hold with queue wired into applyMonthly)
- **inc2 weather:** `weatheremergency_test.go` (AC-6/AC-7 emergency flag, thresholds, direction/shape)
- **inc3 handoff + drain:** `attack_feat087_inc3_handoff_test.go` (AC-9/AC-10/AC-11 differential proof under attack; BUG-484 emergency-drain isolation)
- **Cross-cutting:** `determinism_test.go` (AC-15 worker-count invariance), `concurrency_test.go` (AC-17 race-free), `attack_bug484_emergency_bypass_test.go` (ASM-580 min rule verification)

All tests are passing CI-green as of 2026-09-01 17:40 UTC (inc3 Opus ACCEPT, commit 88f9bce).
