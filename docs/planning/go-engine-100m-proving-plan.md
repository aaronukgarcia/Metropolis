# Go Engine — 100M Proving Plan

> **Status:** measurement + gap analysis + increment plan. Docs-only, no code.
> **Author:** Bev lane, 2026-09-04.
> **Drives:** FEAT-083 (Baseline One finish line), FEAT-1972079936 (Azure compute offload), FEAT-2326609775 (Azure Phase-4 increments).
> **GR#25:** every module named below already exists in `code.json` / on disk. Nothing here asserts a new cross-module edge; where an increment needs one, it is listed explicitly as a **prerequisite registration**, not as prose.

---

## 0. The bar, restated

Aaron's Q100137 ruling (FEAT-083 BOW comment, 2026-09-04) defines the finish line as four conjunctive conditions:

| # | Condition | Where it is proved |
|---|---|---|
| a | **100,000,000 citizens** — individual records, the Option B promise, not an aggregate | `internal/engine/citizens` cold store |
| b | **Cloud engine runs it, browser views it** | `cmd/metroserve` on Azure; `webconsole/src/sim/protocolClient.ts` |
| c | **At target speed** | Q100100 normal play: 1 s = 1 game day. Q100147 turbo: 6.25 game-days/s |
| d | **Every mechanic live** | the composition root's registered phase hooks |

The engine clock makes (c) arithmetic, not opinion. `internal/engine/core/clock.go:15` — `DailyTicksPerMonth int64 = 30`; `clock.go:130/134/150` — **1 engine tick = 1 game day**, 30 ticks = 1 month, 360 ticks = 1 year.

**Therefore the tick budget is fixed and non-negotiable:**

| Speed | Ruling | Game days / sec | **Wall-clock budget per day-tick** |
|---|---|---|---|
| Normal | Q100100 | 1 | **1000 ms** |
| Turbo | Q100147 (supersedes Q100100 for turbo) | 6.25 | **160 ms** |

Every number in this document is measured against those two figures.

---

## 1. Measured — what the Go engine actually costs today

### 1.1 The existing 1M perf gate does not tick 1M citizens

**This is the most important finding in the document and it invalidates the standing scale safety net.**

`perf-1m-probe` (`.github/workflows/ci.yml:626+`) is live and merge-blocking. It runs `internal/harness/synth/cmd/perfci -preset 1M`. But `runHeadless` (`internal/harness/synth/headless_seam.go:70-76`) constructs the run as:

```go
cfg := headless.Config{
    Seed:          uint64(hdr.WorldSeed),
    Months:        int64(months),
    OutDir:        ..., Report: ..., CorrelationID: ...,
}
```

`headless.Config` (`internal/harness/headless/run.go:54-94`) **has no citizen-count field**. The 1M citizen count reaches `Generate` only — it changes generation cost and nothing else. The engine that is then ticked is the ordinary `compose.Wire` genesis city (seed population order-64, growing by migration/fertility).

So the gate that CI advertises as "the real merge-blocking 1M scale gate" measures **generation of 1M throwaway synthetic records plus 15,000 ticks of a few-thousand-citizen city.** No CI job in this repo has ever ticked a large citizen population. Filed below as the top-ranked gap.

My local run of that exact gate (Windows, this dev box, `-preset 1M -months 500`):

| Field | Measured |
|---|---|
| `GenerationTime` | **29.74 s** (1M synthetic records) |
| `TickTime` | **15.45 s** for 15,000 ticks |
| Derived per-tick | **1.03 ms/tick** — *of a small city, not 1M* |
| `PerMonthTick` | 30.9 ms |
| `PhaseHookCount` | **9** (real hooks; the harness's hardcoded-0 assumption is long stale) |
| `AllocBytes` / `AllocCount` | 643 MB / 5.52M |
| Phase split (500 months) | population 7.65 s, daily-tick 4.59 s, finance 2.66 s, consumption-shortfall 0.09 s, land-value-decay 0.04 s, production 0.004 s, logistics-settlement 0.002 s |

### 1.2 Real per-citizen cost — measured directly

Because no gate measures it, I measured it directly: a throwaway harness (scratch-only, injected via `go build -overlay`, **nothing written into the repo**) that seeds N cold records into a real `citizens.CitizensAPI` via `SeedColdRecords` and drives `AdvanceDayTick` for a full simulated month (30 ticks) after a warm-up tick. Same machine, same build, five scale points.

| Citizens (N) | Resident bytes/citizen | **ms per day-tick** | µs/tick/million | Alloc during ticks | Allocs/tick |
|---:|---:|---:|---:|---:|---:|
| 100,000 | 100.7 | **1.62** | 16,186 | 113 MB / 31 ticks | 210 |
| 300,000 | 86.1 | **3.46** | 11,522 | 358 MB / 31 | 285 |
| 1,000,000 | 87.4 | **17.63** | 17,627 | 1.39 GB / 31 | 311 |
| 3,000,000 | 81.5 | **127.42** | 42,475 | 4.30 GB / 31 | 343 |
| 10,000,000 | 82.9 | **624.35** | 62,435 | 13.20 GB / 31 | 382 |

Seed time scaled cleanly and linearly (33 ms → 3,482 ms), so ingestion is not the problem. **The tick is.**

### 1.3 Memory — the 75 B figure is real but is not the whole number

| Figure | Source | Value |
|---|---|---|
| Cold record field-sum | `citizens.ColdShard.bytesPerCitizen()`, `coldshard.go:331` | **75 B** (verified by running `TestColdShardBytesPerCitizen`) |
| Hot record inline size | `TestPerf10M`, `memory_test.go:18` | **232 B** |
| **Measured live Go heap** at 10M cold | this document's harness | **82.9 B/citizen** |

Two documentation-drift defects found while verifying:

* `internal/engine/citizens/doc.go:30` still says the cold store measures **"~67B ⇒ 100M ≈ 6.7GB"**. The real figure is **75 B** — `Household` and `Partner` were widened `uint32→uint64` by the births-unblock lane on 2026-09-02 (`coldshard.go:295-296`) and the doc was not updated. 100M ⇒ **7.5 GB** field-sum, **8.3 GB** measured live heap.
* `doc.go:21` says the hot record is "~216B"; measured is **232 B**.

Both are BUG candidates (GR#3 / GR#15: the doc is a duplicate of a value that is derivable and is now wrong).

### 1.4 Snapshot / restore / journal — measured design facts

* Cadence: `compose.SnapshotCadenceTicks = 12 * core.DailyTicksPerMonth` = **360 ticks = 1 simulated year** (`snapshot.go:62`); `MaxRetainedSnapshots = 5` (`snapshot.go:73`); the journal is retained **forever** by design (`snapshot.go:37-41`).
* Snapshot is **full-state, every time**. No delta, no dirty-tracking, no COW (`buildSnapshotBytes`, `snapshot.go:228-242`).
* Citizens serialise **one JSON record per citizen** (`participant.go:92-118` `coldStream`, ~25 tagged fields per `coldCitizenWire`), NDJSON + gzip (`internal/foundation/serialize/ndjson.go:21`).
* `zipDir` returns the whole bundle as **one contiguous `[]byte`** (`snapshot.go:736`), and `PutSnapshot`/`GetSnapshot`/`unzipDir` all take `[]byte`. Restore doubles it.
* Journal **fsyncs per record**, with `open`+`MkdirAll`+`ensureCityMeta`+`write`+`Sync`+`Close` on *every* append (`internal/persist/diskstore.go:206-255`). This caps throughput at roughly 100–2,000 commands/sec **regardless of population**.
* Restore reads the entire journal into memory and `splitJournalAtTick` walks all of it (`snapshot.go:688-729`, `persistjournal.go:192-206`) — O(total commands ever).
* `tryRestoreCandidate` (`snapshot.go:575-607`) loads each candidate snapshot into a **throwaway engine** before touching the real one, so a walk-back over k bad snapshots costs k full loads.

---

## 2. Extrapolated to 100,000,000

### 2.1 Tick time — the headline number

The measurements are **not linear**. Marginal cost per million citizens rises from 11.5 ms/M at 300k to 62.4 ms/M at 10M. Two honest fits over the 1M–10M regime, where the curve is best constrained:

* **Power law**, exponent from 1M→10M = **1.55**. 100M ⇒ 624 ms × 10^1.55 = **≈ 22 s per day-tick**.
* **`t = aN + bN²`** fitted to (1M, 17.63) and (10M, 624.35): a = 12.65 ms/M, b = 4.98 ms/M². 100M ⇒ 1,265 + 49,780 = **≈ 51 s per day-tick**.

**Call it 20–50 seconds per day-tick at 100M, citizens alone, before any other module.**

| | Budget | Measured/extrapolated 100M | Over budget by |
|---|---:|---:|---:|
| Normal play (1 day/s) | 1,000 ms | 20,000–51,000 ms | **20–51×** |
| Turbo (6.25 days/s) | 160 ms | 20,000–51,000 ms | **125–319×** |

**If the superlinearity is removed** (see §3), the best marginal rate measured anywhere on the curve is 11.5 ms per million (at 300k). A perfectly linear engine at that rate gives 100M ⇒ **≈ 1.15 s per day-tick single-threaded**. That is still 1.15× over the normal budget and **7.2× over turbo** — so the index fix is *necessary but not sufficient*. The remaining ~7× must come from real shard parallelism (§3.4) on a many-core cloud box.

Simulated-month and simulated-year costs, for planning:

| Scale | Per day-tick | Per game month (30) | Per game year (360) |
|---:|---:|---:|---:|
| 1M (measured) | 17.6 ms | 0.53 s | 6.3 s |
| 10M (measured) | 624 ms | 18.7 s | 3.7 min |
| 100M (extrapolated) | 20–51 s | 10–25 min | **2–5 hours** |
| 100M (linear target) | 1.15 s | 35 s | 7 min |

### 2.2 Memory at 100M

| Component | Basis | 100M |
|---|---|---|
| Cold store, live heap | measured 82.9 B/citizen at 10M | **8.3 GB** |
| Go GC headroom at default `GOGC=100` | heap target ≈ 2× live | **≈ 16.6 GB peak** |
| Hot-elevation cache | 232 B × elevated set; design keeps this bounded | small, but **unbounded today** — see §3.6 |
| Monthly stratified-sample materialisation | `allColdRecordsLocked` builds a `[]ColdRecord` of *every* citizen, once per month (`registry.go:945-953`, called from `registry.go:668`) | **+12 GB transient** |
| Snapshot buffer | `zipDir` one contiguous `[]byte` | **+4–7 GB** (§2.3) |
| Death handoff stream | `handoff` slice never truncated (`deathwave.go:124`, `:564`) | grows without bound |

**Sizing verdict: 32 GiB is the floor for the tick loop; 64 GiB is the number to actually provision** so the monthly sample pass and the snapshot buffer do not collide. `GOMEMLIMIT` must be set explicitly — at 4.3 GB of allocation churn per tick (extrapolated linearly from the measured 426 MB/tick at 10M), default GC behaviour will thrash.

Aaron's "7.5 GB, a cloud box's problem" is the correct order of magnitude for the *records*. It is not the box size. The box size is 4–8× that.

### 2.3 Snapshot / restore at 100M

The wire form is JSON, not the packed columns, so snapshot bytes ≠ 75 B/citizen:

| | 100M |
|---|---|
| Raw NDJSON (`coldCitizenWire`, ~350–450 B/citizen) | **35–45 GB** |
| gzip at a realistic 6–10× on this shape | **4–7 GB per snapshot** |
| × `MaxRetainedSnapshots = 5` | **20–35 GB on disk** |
| Wall clock per snapshot (JSON marshal ~1–3 µs/rec + single-threaded gzip ~50 MB/s + zip-to-RAM + write + fsync) | **≈ 15–30 minutes, tick loop blocked** |
| Cadence today | every 360 ticks = every simulated year |

At 100M the snapshot takes longer than the simulated year it protects. **Snapshotting is a hard blocker at 100M, not a tuning item.**

Journal at 100M: fsync-per-record caps command throughput at 100–2,000/sec independent of population. At normal play that is 1 `AdvanceTicks` command/sec, so it is *not* the bottleneck for the sim itself — but it is a hard cap on player command rate and on any replay-based proving run, and `AppendJournal`'s `MkdirAll`+`open`+`Close` per append is pure waste. FEAT-2326609775 inc2 already plans the batched Blob store; it is blocked on **BUG-480**.

### 2.4 Instance sizing and Azure cost against the £20/month cap

The governing cost ruling is Aaron's **£20/month** (Q100139, FEAT-2326609775 comment, 2026-09-04), superseding the design doc's own £50 recommendation (`docs/planning/azure-cloud-engine-design.md:580`).

**🚨 LOUD FLAG: a 100M city cannot run continuously inside £20/month. Not close.**

| Rung | RAM needed | Azure shape | Est. list cost |
|---|---|---|---|
| 1M | ~90 MB | Container Apps **Consumption**, 0.5 vCPU / 1 GiB, `min-replicas 0` | **≈ £2/mo** — inside cap |
| 10M | ~0.9 GB live, ~2 GB peak | Container Apps **Consumption**, 2 vCPU / 4 GiB | **≈ £8–15/mo** if scaled to zero when idle — at the cap |
| 30M | ~2.5 GB live, ~6 GB peak | Container Apps Consumption ceiling is **4 vCPU / 8 GiB** — this is the last rung that fits it | **≈ £15–25/mo** — at/over the cap |
| **100M** | **32–64 GiB** | **Exceeds Container Apps Consumption entirely.** Needs a Dedicated workload profile (E-series) or a plain VM: `Standard_E8ds_v5` (8 vCPU / 64 GiB) or `Standard_E16ds_v5` (16 vCPU / 128 GiB) | **£360–725/mo at 24/7 PAYG — 18–36× the cap** |

**The way through is that a proving run is not a service.** Aaron's bar is *"a 100,000,000-citizen city runs somewhere Aaron can be shown"* — it does not say "runs forever".

| Strategy | Shape | Cost of ONE 100M proving run |
|---|---|---|
| **Spot VM, provision → prove → tear down** ✅ recommended | `Standard_E16ds_v5` Spot, uksouth, 8-hour window | **≈ £0.80–1.60** |
| PAYG VM, same 8-hour window, deleted after | `Standard_E16ds_v5` PAYG | ≈ £8 |
| Container Apps Dedicated E-profile, scaled to zero between runs | E16 profile | ≈ £8–12 per 8-hour run + profile floor |
| 100M running continuously | any of the above 24/7 | £360–725/mo — **out of budget, do not plan for it** |

**Recommendation to Aaron:** keep the £20/month cap as the *steady-state* cap and treat the 100M finale as a **budgeted one-off spend of ≤£5 per attempt on a Spot instance, torn down on completion**, with the Azure Budget alert and the hard-stop runbook (`az containerapp update --min-replicas 0 --max-replicas 0`) unchanged for the always-on rungs. The steady-state cloud city stays at the 1M/10M rung inside £20.

*(All Azure figures are uksouth list-price estimates from general knowledge, not from a live price query — see ASM-A below. They must be confirmed with `az` before any spend commitment. The ratios, not the absolute pounds, are the load-bearing part: 100M-continuous is more than an order of magnitude over the cap.)*

---

## 3. Superlinearity findings — every one traces to a single missing index

### 3.1 Root cause: `ColdShard.rowOf` is a linear scan

`internal/engine/citizens/coldshard.go:212-219`

```go
func (s *ColdShard) rowOf(id uint64) int {
	for i, v := range s.ids { if v == id { return i } }
	return -1
}
```

`ColdShard` is pure struct-of-arrays with **no id→row map and no sorted index**. Every single-citizen lookup costs **O(shard size) = O(N/256)**. At 100M a shard holds ~390,000 rows, so one lookup is ~390k comparisons. Non-test callers: `registry.go:763, 958, 967, 1085, 1094, 1106`.

### 3.2 The quadratic per-tick path — fertility

`internal/engine/citizens/fertility.go:335-391` `applyFertilityLocked`, called **every day-tick** from `registry.go:800`. Line `:361` does `c.coldRecord(partner)` — a cross-shard `rowOf` scan — **per partnered citizen in the scheduled shards**, and `householdChildCountLocked` (`fertility.go:416-434`) walks household members calling `birthMonthOfLocked` → another scan each. Complexity **O(N² / 7680) per tick**. This is the term my measurements are watching grow: it is what turns 62 µs/M at 10M into 20–50 s at 100M.

### 3.3 The quadratic monthly paths — money circulation and the death drain

* `internal/engine/compose/moneycirc.go` — `markEmploymentAndCount` (`:315-339`), `employedResidentCount` (`:347-359`), `formResidentHouseholds` (`:399-422`), `distributeWagesToResidents` (`:437-457`). Each loops all resident ids calling `CitizenAt` → `rowOf`. **O(N²/256) each, four of them, every month.**
* Worse, `liveResidentIDs` (`compose.go:2198-2237`) enumerates ids by *counter range*, so **dead citizens are never removed from the enumeration** — the walk is O(ever-minted) and every dead id pays a full **failed** (therefore worst-case, full-shard) `rowOf` scan.
* Death drain, `registry.go:761-789`: `for _, id := range realised { row := c.cold[shard].rowOf(id) }` → **O(D × N/256)** per month. At 100M with 100k deaths/month that is ~3.9 × 10¹⁰ comparisons per month.

### 3.4 The parallelism killer — one global mutex, once per citizen

`internal/engine/citizens/coldpass.go:206` calls `dq.IsQueued(id, ...)` for **every citizen in every scheduled shard**, and `IsQueued` (`deathwave.go:204-210`) takes the single `DeathQueue.mu`. That is **N/30 acquisitions of one global mutex per tick**, taken concurrently by all `runShardsParallel` workers. Algorithmically only O(citizens), but it **serialises the shard parallelism the whole cold-pass design exists to provide** — which is precisely the ~7× headroom §2.1 says we need after the index fix.

### 3.5 Full-population materialisation, once per month

`registry.go:1141-1144` `coldParamsLocked` → `allColdRecordsLocked` (`:945-953`) builds a `[]ColdRecord` of every citizen with an unsized `append`, then samples it. O(N) time and O(N × sizeof) allocation → **>12 GB transient at 100M**, monthly.

### 3.6 Unbounded growth

* `DeathQueue.handoff` (`deathwave.go:124`, appended `:564`) is **never truncated**; `RealisedDeaths` (`:405-412`) copies the whole cumulative stream. `DeathHandoffSince` pages it but the backing slice still grows forever.
* `realiseLocked` (`:267-300`) does a full `sort.Slice(q.pending)` on every Realise plus an O(P) copy — fine monthly, but it re-sorts a backlog that budget-limited smoothing can make permanent. A heap is the right structure.
* Per-tick allocation: `hotIDSetLocked` (`registry.go:937-943`) rebuilds a fresh `map[uint64]bool` of the whole hot set **every day-tick** (`registry.go:672`).

### 3.7 What is genuinely well-behaved

Worth recording, because it is the part that works:

* The amortised cold pass itself is **correct O(N/30)** — `ColdPassSchedule` (`coldpass.go:27-39`) advances each of 256 shards exactly once per month.
* No map iteration over citizens on the daily path; `c.hot` iteration is monthly only. Determinism is not at risk (GR#21, the map-range-with-break class).
* traffic / build / invariant hooks are **O(cells)/O(buildings)** — population-independent.
* Seeding/ingestion is linear.
* `PopulationHash` is O(N) with a full sort but is off the tick path — keep it there.

### 3.8 The one fix that collapses most of it

**A `map[uint64]int32` id→row index per `ColdShard`** costs roughly 6–8 B/citizen — comfortably inside the 60–100 B band `ColdShard`'s own doc defends — and collapses §3.1, §3.2, §3.3 from quadratic to linear in one change. Everything else in §3 is a smaller, independent follow-on.

---

## 4. Mechanic completeness — the "every mechanic live" half of the bar

### 4.1 What ticks today: 9 hooks

`internal/engine/compose/compose.go`, `var registrationOrder` (~line 306); `BaselineOneHookCount() == 9`.

| Phase | Cadence | Hooks registered |
|---|---|---|
| `PhaseDailyTick` | daily | `traffic` → `citizens` (cold pass) → `build` → `invariant` |
| `PhaseProduction` | monthly | `world` (**noop — state holder, no tick logic**) |
| `PhaseLogisticsSettlement` | monthly | **— zero hooks —** |
| `PhaseConsumptionShortfall` | monthly | `market` (**noop**), `consumption` |
| `PhasePopulation` | monthly | `attract` |
| `PhaseLandValueDecay` | monthly | **— zero hooks —** |
| `PhaseFinance` | monthly | `finance` |

Driven from inside those hooks without hooks of their own: `crime`, `leisure`, `refuse`, `services`, `firms`, `households`, `season`, `logistics`. Constructed but dormant: `extcommute` (fully seamed, no command routes to it), `unlocks` (constructed only so it can be a `save.Participant`; compose's own comment says "it never ticks").

**So: two of seven phases are empty, two of the nine hooks are noops, and the real per-tick simulation is five modules wide.**

### 4.2 Built, real, tested — and not wired

25 engine packages with substantial implementations and full test suites that **never run in the tick**. Ranked by what blocks "every mechanic live", using the webconsole game as the definition of what a player currently sees:

**Tier 1 — the browser has it, the Go engine has a real module, it is not wired.** These are simultaneously the convergence gap and the feature gap.

| Go module | The TS mechanic it must match | TS source |
|---|---|---|
| `engine.roads` | road network, auto-scale, replan cascade, motorway junction/slip siting | `webconsole/src/sim/engine.ts` |
| `engine.tax` | council-tax / business-tax rates and policy | `webconsole/src/sim/fiscal.ts` |
| `engine.fiscal` | upkeep buckets, welfare / municipal-quality circuit | `fiscal.ts` |
| `engine.maintenance` | per-instance upkeep | `fiscal.ts` |
| `engine.spiral` | insolvency → bailout → administration → final decline | `fiscal.ts`, `engine.ts` |
| `engine.wellbeing` | approval + wellbeing indices | `engine.ts` |
| `engine.policies` | policy toggles / outflow policies | `fiscal.ts` |
| `engine.rail` (**self-declared stub**) | transit lines and trains | `data.ts`, `trains.ts` |
| `engine.fuel` | grid import/export tariffs, plant amortisation | `fiscal.ts` |
| `engine.worklife`, `engine.staffing` | sector wages, filled jobs, unemployment | `fiscal.ts`, `data.ts` |

**Tier 1b — wired but only partially, with a placeholder where the mechanic should be.**
`engine.services` is read for the ServiceCoverage attract term only — no demand push in the tick. `engine.refuse` is wired for `Generate` plus tonnes reads on a single `"citywide"` cell — none of the waste economy (diversion, EfW power, landfill tipping, recycling/compost revenue) that `data.ts` already runs. `engine.consumption` solves three coarse single-source networks with placeholder capacities against `data.ts`'s pipe tiers and brownout model. `engine.households` reports a **Wire-time constant** (`baselineOneHousingVacancy`) instead of real stock.

**Tier 2 — real Go modules with no browser counterpart yet.** They add depth once the loop is complete and are not on the 100M critical path: `census`, `social`, `education`, `projections`, `news`, `mining`, `defence`, `coastal`, `capexport`, `checkpoint`, `accelerator`, `freight`, `airunits`, `comms`, `chemicals`, `shopping`.

**Tier 3 — thin scaffolds:** `fdi`, `tunnels`, `parking`, `cafe`, `farming`, `helper`, `spaceport`, `destination`, `prison`, `airport`, `tourism`.

**No Go module at all** for the density tier / capacity tier / consolidator machinery (`webconsole/src/sim/consolidator.ts`, `data.ts`) — and per Q100114 that machinery is exactly the ~10× people-per-building multiplier the 100M city needs. This is a genuine hole, not a wiring gap.

### 4.3 What is actually proven equivalent

`internal/converge` covers **two domains only**: `FinanceDomain` (`finance_domain.go` — treasury/reserves/debt/netWorth, all `TierExact`) and `ServicesDomain` (`services_domain.go` — fire/education/healthcare capacity/need/coverage). The TS side is a captured JSON fixture, never a live process, and the package has **no non-test callers**.

Population and demographics, roads and traffic, utilities, tax, waste, wellbeing/approval, density, and the insolvency ladder have **no converge domain at all**. Since Aaron's Q100138 ruling is **shadow-first** — the cloud *verifies* the browser city through inc4 and authority flips only when proven — the converge domain list *is* the flip schedule. Two domains is two flips' worth of proof.

---

## 5. Increment plan

Where we are: Azure **inc1** is in flight (uncommitted, in an agent worktree) — `Dockerfile`, `cmd/metroserve/health.go`, `cmd/metroserve/portknock.go`, `.github/workflows/azure-deploy.yml`, `docs/planning/azure-runbook.md`, `tools/azure/smoke.mjs`, error codes MET-P040/P041/P042. Trunk `cmd/metroserve` today serves exactly one route, `/ws` (`main.go:132`), with no health endpoint and no container.

Two tracks run in parallel. Neither blocks the other until the finale.

### Track A — cloud rungs (population, on the real Azure engine)

**inc-A · 1M cloud-hosted, ticking continuously**
Prerequisite: Azure inc1 lands (`/health`, container, smoke test, budget + hard-stop runbook).
Shape: Container Apps Consumption, 0.5 vCPU / 1 GiB, min-replicas 0, max-replicas 1 (mandatory — `wsserver` allows one live WebSocket per Transport).
Seed 1M citizens through `citizens.SeedColdRecords` at genesis.
**Pass bar:**
1. 1,000,000 `TotalPopulation` on the hosted engine, verified over `/ws`.
2. 360 consecutive day-ticks (one simulated year) with **p95 tick < 1000 ms** (measured 17.6 ms for citizens — the headroom is for the other 8 hooks).
3. One snapshot written at tick 360 and a restart restored from it, journal not double-appended.
4. The browser renders it: `metropolis.liveEngine=1` against the Azure URL, population and treasury visibly moving.
5. Round-trip p95 < 100 ms, append p95 < 25 ms (inc1's own bar, re-run at 1M).
6. Cost stays ≤ £5/month.

**inc-B · 10M cloud-hosted**
Shape: Container Apps Consumption, 2 vCPU / 4 GiB.
**Pass bar:** everything in inc-A at 10M, plus:
7. **p95 day-tick < 1000 ms at normal speed** (measured today: 624 ms — passes, with almost no margin).
8. **p95 day-tick < 160 ms at turbo.** Measured today 624 ms ⇒ **FAILS by 3.9×**. This rung is the forcing function for §3.8's index and §3.4's mutex.
9. Snapshot at 10M completes in < 60 s and does not block the tick loop past one tick's budget.
10. Resident memory ≤ 2 GB with `GOMEMLIMIT` set explicitly.
11. Cost stays ≤ £20/month with scale-to-zero when idle.

**inc-C · 30M — the linearity gate** *(new rung, and the one I most want)*
Shape: Container Apps Consumption at its 4 vCPU / 8 GiB ceiling — the last rung that fits the cheap tier.
**Pass bar:**
12. **`t(30M) / t(10M) ≤ 3.3`** — i.e. the measured tick time scales *linearly*, proving §3's quadratic terms are gone. On today's code this ratio would be ~5–6. This is a cheap, decisive, £20-compliant test of the single most important fix, and it must pass before anyone provisions a 64 GiB box.
13. Turbo bar (160 ms) still met at 30M.

**inc-D · 100M — the finale**
Shape: **Spot** `Standard_E16ds_v5` (16 vCPU / 128 GiB), uksouth, provisioned → proven → **torn down**. Budgeted as a one-off ≤£5 per attempt, not as a running service.
**Pass bar:**
14. `TotalPopulation` = 100,000,000 individual citizen records in the Go cold store, read back over `/ws`.
15. Resident memory measured and ≤ 32 GiB with `GOMEMLIMIT` honoured.
16. **p95 day-tick ≤ 1000 ms at normal speed** sustained over one full simulated year (360 ticks).
17. Turbo: p95 ≤ 160 ms, or a recorded, Aaron-ruled turbo derating at 100M.
18. One snapshot written and one restore proven at 100M (this requires §5 Track-B's snapshot work — at today's design it is a 15–30 minute blocking operation).
19. A browser session attaches and renders it.
20. Determinism: two runs at the same seed produce an identical `PopulationHash`.
21. Total spend for the run recorded and ≤ £5.

### Track B — engine work the rungs depend on

Ordered by what unblocks the earliest rung.

1. **`ColdShard` id→row index** (§3.8). Unblocks inc-B's turbo bar and inc-C outright. Single highest-value change in this document.
2. **A real large-population perf gate.** `harness.headless.Config` has no citizen-count field (§1.1). Until it does, CI cannot see any of this. *Prerequisite: none — this is a field on an existing struct in an existing module, plus a `perfci` flag.* Add scale points at 1M and 10M with explicit per-tick ceilings; the existing `perf-1m-probe` job stays but is renamed to what it actually measures (generation cost).
3. **First Go benchmark in the engine.** `grep "func Benchmark" internal/engine/` returns **nothing**. There is no `go test -bench` surface for per-citizen tick cost anywhere. The harness in §1.2 should land as a real benchmark in `internal/engine/citizens`.
4. **Death-queue mutex** (§3.4) — restores shard parallelism, worth the ~7× the linear projection still needs.
5. **Snapshot at scale** — streaming `PutSnapshot` instead of one contiguous `[]byte` (`snapshot.go:736`), and a binary cold-shard encoding instead of one JSON object per citizen (`int.serializer`'s reserved `BinarySerializer`, which `paging.go`'s placeholder gob codec is already waiting on). Blocks inc-D bar 18.
6. **Journal batching / group commit** — FEAT-2326609775 inc2's Blob store. Blocked on **BUG-480**.
7. **Monthly full-population materialisation** (§3.5) — sample from the shards in place instead of building a 12 GB slice.
8. **Unbounded growth** (§3.6) — handoff truncation, heap for the pending queue, reuse the hot-id set.
9. **`PageStore` is dead code.** `internal/engine/citizens/paging.go` implements the disk-backed LRU shard paging that `doc.go` cites as the >20M residency answer, and **nothing outside its own tests references it** — no call site in `compose`, `cmd/`, or anywhere else in `internal/engine`. Classic built-but-not-wired. Either wire it or record that 100M is an all-resident design and size the box accordingly (this document assumes all-resident, which is the conservative reading).

### Track C — mechanic completeness

Runs concurrently and is scheduled against §4.2's ranking, not against the rungs. **`PhaseLogisticsSettlement` and `PhaseLandValueDecay` having zero hooks is the cheapest visible progress** — both phases exist, both have candidate modules already built. Each wiring is a `registrationOrder` entry plus whatever edges `code.json` does not already carry; **any new cross-module edge is a prerequisite registration with the Architect before the acceptance prose is written (GR#25), never inline prose.**

The ranking for "every mechanic live" is §4.2 Tier 1 first (the browser already shows it and a real Go module already exists), then Tier 1b's placeholders, then Tier 2 depth. Density/consolidator has no Go module at all and needs an Architect decision before it can be scheduled.

---

## 6. Assumptions (ASM candidates, with recommendations)

| ID | Assumption | Recommendation |
|---|---|---|
| **ASM-A** | Azure prices in §2.4 are uksouth list-price estimates from general knowledge, not a live price query. | **Verify with `az` before any spend.** The ratios (100M-continuous is 18–36× the cap) are robust to the absolute figures being off by ±30%. |
| **ASM-B** | The 100M extrapolation assumes the 1M→10M curve shape continues. It is fitted from five points on one Windows dev box, not measured at 100M. | Treat 20–51 s/tick as an **order-of-magnitude** figure. inc-C's 30M linearity gate is what turns it into a measurement. |
| **ASM-C** | This document sizes 100M as **all-resident** (no shard paging), because `PageStore` is not wired. | Confirm all-resident is the design intent, or wire `PageStore` and re-size. All-resident is the conservative and simpler reading — **recommend keeping it** and sizing the box. |
| **ASM-D** | The 232 B hot record applies only to *elevated* citizens; the hot set is assumed bounded at 100M. | It is **not bounded in code today**. A hot-set ceiling must exist before inc-D or 100M-hot is 23.2 GB on its own. |
| **ASM-E** | Measurements are single-threaded-equivalent on this dev box. A 16-vCPU cloud box should do better once §3.4's mutex is removed. | The linear projection's remaining ~7× gap is **assumed** recoverable from parallelism. Prove it at inc-C, not at inc-D. |
| **ASM-F** | Snapshot compression ratio 6–10× on the `coldCitizenWire` JSON shape. | Measure it at inc-B (10M) rather than guessing at 100M. |
| **ASM-G** | "Every mechanic live" is read as §4.2 Tier 1 + Tier 1b (what the browser already shows), not all 65 engine packages. | Needs Aaron's ruling — see Q100164. |

---

## 7. Open questions for Aaron

**Q100163 — Does "every mechanic live" mean *every module built*, or *every mechanic the browser currently shows*?**
Today 9 hooks tick and 5 modules do real per-tick work; 25 more engine packages are fully built and tested but never run, and 11 more are scaffolds. Rec: **the browser-parity reading** (§4.2 Tier 1 + Tier 1b) — it is bounded, it is exactly the convergence gap, and it makes "the cloud runs what the browser shows" true. Tier 2 depth follows after 100M.

**Q100164 — Is the 100M proof a *run* or a *service*?**
100M continuous is £360–725/month, 18–36× the £20 cap. A Spot-instance provision→prove→teardown run is ~£1–5 per attempt. Rec: **a run.** Keep £20/month as the steady-state cap for the 1M/10M always-on city, and authorise a separate one-off ≤£5-per-attempt budget for the 100M finale.

**Q100165 — Does the turbo bar (160 ms/tick) apply at 100M, or only at the playable rung?**
Normal play at 100M is a 1000 ms budget and is plausibly reachable. Turbo at 100M is 160 ms and is a much harder target. Rec: **normal speed is the 100M pass bar; turbo is a stretch goal with a recorded derating**, since the 100M city is a proof and Aaron's own play city is at the playable rung.

**Q100166 — At 100M, is a snapshot required, or is journal-replay-from-genesis acceptable for the proving run?**
A 100M snapshot is 4–7 GB and 15–30 blocking minutes at today's design. Rec: **required** — bar 18 stays. A 100M city that cannot be saved is a demo, not a proof, and the streaming/binary snapshot work (Track B item 5) is needed regardless.

**Q100167 — Should the 30M linearity gate (inc-C) be inserted between 10M and 100M?**
It is not in the current inc ladder. Rec: **yes.** It costs nothing extra (it fits the £20 tier), and it is the decisive, cheap test of whether the index fix actually removed the quadratic — before anyone provisions a 64 GiB box to discover it did not.

**Q100168 — Is the currently-required `perf-1m-probe` gate acceptable to keep advertising as a 1M-scale gate?**
It never ticks 1M citizens (§1.1). Rec: **no** — rename it to what it measures (generation cost) in the same commit that adds a real citizen-count-carrying scale gate, so no window exists where CI claims a scale guarantee it does not provide. This is a GR#28-adjacent integrity issue: a green check that certifies nothing.

**Q100169 — Density/consolidator has no Go module at all.**
Per Q100114 it is the ~10× people-per-building multiplier the 100M city needs, and it exists only in `webconsole/src/sim/consolidator.ts`. Rec: **Architect decision needed** on whether the cloud engine gets its own density module (new module registration + edges, GR#25) or whether density stays browser-side and the Go engine remains citizen-only for the proof. The second is cheaper and sufficient for the 100M *citizen* bar as Aaron stated it (fidelity = individual citizens, not buildings).
