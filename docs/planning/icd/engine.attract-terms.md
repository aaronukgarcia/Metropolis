# ICD: Attract Terms — Real Signal Wiring (Safety / LeisureFit / Environment / ServiceCoverage / JobAvailability)

**Status:** DRAFT — authored against `docs/planning/icd/TEMPLATE.md` for FEAT-167 ("Wire real attractiveness terms + populate appealProfile"). Covers the five `engine.attract` §11 terms that are today a flat `baselineOneTermValue = 50.0` constant pushed every month by `compose.go`'s `applyMigration` (`internal/engine/compose/compose.go` lines ~68, 973-996, ASM-243/BUG-058). Does NOT cover `appealProfile` population on the 337 buildings or the tourism visit-appeal portfolio — those are separate FEAT-167 work items with their own ICDs/AC files.

---

## 1. Identity

- **GUID:** `fae40226-71d0-4836-be99-854c7b41eb4a` — `feat.compositionroot`'s own registered `code.json` module GUID (`path: internal/engine/compose/`). Standing in as this integration's identity GUID: no single source module (crime/leisure/refuse) owns the whole integration — the thing that ties five independent term reads into one `AttractAPI.SetTermInputs` push is compose's own `applyMigration`, exactly analogous to why `feat.compositionroot` already owns the registered edges to `engine.attract`/`engine.market`/`engine.finance`/etc. Mirrors the FEAT-169 ICD's stand-in pattern (`docs/planning/icd/engine.citizens-coldpass.md` §1).
- **Name:** `attract.terms.realsignal`
- **Owning module (mkey):** `feat.compositionroot`
- **code.json edge ref(s):**
  - **Already registered (reused, no action needed):** `feat.compositionroot → engine.citizens` (edge guid `35078084-e4a5-4c34-b9ee-6b0352b76fe3`, `feat.compositionroot`'s outbound group `ccb71e33-e413-4de2-afc2-4a453beec322`) — already carries `TotalPopulation`, reused here as the Safety/Environment driver.
  - **NOT YET REGISTERED — pending GR#25 registration before this ICD's Inputs/Outputs may be built against them:**
    - `feat.compositionroot → engine.crime` (target inbound guid `ec4eb047-5101-496f-b9c4-760e21846954`, crime module guid `67dd9b63-91fc-4ce1-8332-ec52009c9f3f`) — verified absent: `feat.compositionroot`'s outbound `calls` array (`code.json`) has no `engine.crime` entry today.
    - `feat.compositionroot → engine.leisure` (target inbound guid `d7d01c42-212d-48f4-b9b9-984bf33bd1ce`, leisure module guid `47447aee-f985-470a-9e8f-b25dbb571743`) — verified absent.
    - `feat.compositionroot → engine.refuse` (target inbound guid `877d2b29-6055-4ec4-9be2-b60b2d53fcf3`, refuse module guid `93cadd89-ef03-4b29-b1b8-1b35c44788b7`) — verified absent.
  - **ServiceCoverage/JobAvailability need no new edge** — both remain a placeholder (§3/§4 below); no call is made into `engine.services`/`engine.firms`/`engine.market` for them by this ICD.

---

## 2. Purpose

Today (2026-08-18) five of `engine.attract`'s seven §11 terms are a hardcoded `baselineOneTermValue = 50.0` constant, so migration cannot respond to anything the player builds or that changes in the city (crime, leisure supply, refuse pollution, service coverage, job market) — only `HousingAffordability` and `Reputation` are real (`api.go`'s own doc comment, ASM-243/BUG-058). This integration wires three of the five terms (Safety, LeisureFit, Environment) to real, already-built module signals driven by a real, already-compose-owned dynamic quantity (population), so the city visibly gets less/more attractive as it changes. The remaining two (ServiceCoverage, JobAvailability) have **no usable real signal today** in any built module (discovery below) and are honestly left as documented placeholders with tripwires rather than backed by invented numbers. This is FEAT-167's core "migration responds to gameplay" enabler for the Baseline One milestone.

---

## 3. Inputs

### Discovery — per-term signal audit

| Term | Source module | Exact function(s) | Scale as returned | Real dynamic driver available to compose today | Verdict |
|---|---|---|---|---|---|
| **Safety** | `engine.crime` (`CrimeAPI`) | `AdvanceMonth(month, []DistrictInput, SecurityInput) error` then `SafetyTerm(id DistrictID) (float64, error)` | `SafetyTerm` is already `[0,100]`, higher = safer — a monotonic inversion of the district's active-crime stock (`api.go` line 599) | **Yes, partial:** `TotalPopulation` (already compose-owned via `citizens.CitizensAPI`, wired since MOD-018) maps onto `DistrictInput.EligiblePool`, crime's real, data-driven (`data/crime.json`) generation base. No other `DistrictInput` driver (`PatrolCoverage`, `OwnDeprivation`, `YouthUnemployment`, etc.) has a real compose-owned source yet — every module that would supply them (`engine.defence`, `engine.education`, `engine.build` regeneration) is unwired or absent from `simState`. | **WIRE**, with the honestly-scoped caveat in §12 that only the population-driven half of crime's generation model is live; the policing/deprivation half stays at documented-neutral defaults until those modules land (tracked as a follow-up, not invented here). |
| **LeisureFit** | `engine.leisure` (`LeisureAPI`) | `LeisureFitAggregate(d TasteDistribution, correlationID string) (float64, error)` | `[0,1]` (`compute.go`'s `leisureFitOverlap`, doc comment line 148-154: "computed here and PUSHED into engine.attract by a caller... the known BUG-058 gap" — this method was built FOR this exact wiring) | **Yes:** venue supply is compose-owned once `LeisureAPI.RegisterVenue`-equivalent registration exists from `engine.build`'s constructed leisure-zone buildings (a new but small bridge — no other module dependency required for this one method, unlike `LeisureFit`/`Patronage` which need citizens/traffic/wellbeing). `d` (the would-be-migrant taste distribution) is leisure's own already data-loaded `Config.DefaultTaste` (`config.go` line 62, sourced from `data/leisure.json`'s `DefaultPopulationTaste` — GR#15 satisfied, no new data file needed). | **WIRE.** Cleanest of the five: single dependency (a venue-registration bridge from `engine.build`), no upstream driver gap. |
| **Environment** | `engine.refuse` (`RefuseAPI`) | `TonnesUncollected(s Stream) (int64, error)` + `TonnesDisposalBacklog(s Stream) (int64, error)`, summed over the 3 registered `Stream`s (`StreamGeneral`/`StreamRecycling`/`StreamFood`) | `int64` kilograms, city-wide already (no `cellID` parameter — `accounting.go` lines 54-114 sum across every registered cell/site) | **Yes, partial:** same population-driven proxy as Safety — `Generate(cellID, driver)` (`generate.go` line 111) needs a `driver` value per registered cell; compose can register one citywide aggregate cell and drive it from `TotalPopulation`, exactly mirroring the pattern `drawConsumption` already uses for the consumption hook (`compose.go`'s `consumptionHook`). No `engine.wellbeing`/`engine.world` pollution seam wiring is needed for THIS term (only for `wellbeing`'s own `PollutionSource`, a different consumer). | **WIRE**, as a documented composite: `Environment = 100 × (1 − clampUnit(uncollectedTonnes / (uncollectedTonnes + halfSaturationTonnes)))` — the SAME half-saturation-curve shape crime's own `SafetyTerm` already uses (`crime/api.go` line 613), reused for consistency, with `halfSaturationTonnes` a NEW data-driven parameter (GR#15 — see §New data file below). This is a coarse composite (refuse-overflow-derived pollution proxy only — no air-quality/emissions signal exists in any built module), explicitly named as such per the discovery brief's own suggestion. |
| **ServiceCoverage** | `engine.services` (`ServicesAPI`) | `Quality(id ServiceID)`/`Capacity(id ServiceID)` exist, but **no city-wide aggregate and no enumeration method** (`ServiceIDs()` does not exist — every accessor requires a caller-known `ServiceID`) | `Quality` returns a computed float (scale documented in `quality.go`, not re-derived here); irrelevant since no aggregate path exists | **No usable signal today.** `ServicesAPI` is not constructed anywhere outside its own package (`grep` across `internal/`+`cmd/` for `services.Load`/`services.New` outside `internal/engine/services/` returns zero hits) — `compose`'s `simState` holds no `ServicesAPI` field at all. Registering real service instances requires a `engine.build` → `engine.services` bridge via the already-built `ServiceSpecFromBuilding` (`service.go` line 70) that nothing calls yet — a distinct, larger integration outside this ICD's scope. | **REMAIN PLACEHOLDER** at `baselineOneTermValue` (50.0). Tripwire: a follow-up BOW item (build→services registration bridge) must land before `ServiceCoverage` can be honestly wired; do not synthesize a coverage number from unregistered data. |
| **JobAvailability** | `engine.firms` (`FirmsAPI`) and/or `engine.market` (`MarketAPI`) | Searched both packages' full exported surface (`firms.go`, `services.go`, `founding.go`, `credit.go`; `market.go`) | n/a | **No usable signal today, anywhere.** `FirmsAPI` exposes firm lifecycle/credit/founding queries but no vacancy or labour-demand-vs-workforce aggregate; `MarketAPI` is a static price/availability/supply-mode query surface over the nine commodities (§6) with no labour concept at all — "job"/"labour"/"vacancy" appear nowhere in either package's exported API. `engine.citizens` (already compose-wired) likewise has no unemployment/employment-rate aggregate (`grep` across `citizens/*.go` for `employ`/`unemploy`/`vacan`/`labour` in exported method names returns zero). | **REMAIN PLACEHOLDER** at `baselineOneTermValue` (50.0). Tripwire: no module today computes a job-availability signal at all; a real fix needs a NEW aggregate (e.g. `firms.VacancyRate()` or a citizens-side unemployment-rate query) built by `engine.firms`/`engine.citizens` first — flagged as a new BOW item, not invented here. |

**Update cadence (all terms):** `engine.attract` runs monthly (`compose.go`'s `attractHook`, `PhasePopulation`, single-shard, shard 0 only). Every wired term is sampled **once per month**, inside `applyMigration`, immediately before `SetTermInputs`/`ApplyMigration` — never mid-month, matching the existing `HousingAffordability`/`Reputation` cadence and `attractHook`'s own doc comment ("it runs one monthly `AttractAPI.ApplyMigration` step").

**Determinism notes (all terms):** every source call in this table is a pure read of already-deterministic module state (`SafetyTerm`/`LeisureFitAggregate`/`TonnesUncollected` are all `RLock`-guarded accessors over state `AdvanceMonth`/`Generate` already advanced deterministically this same tick) or a pure city-population read (`TotalPopulation`, itself deterministic — AC-17's `PopulationHash`). No new stochastic draw is introduced by this integration; no wall-clock time is read anywhere in the chain.

**New data file (GR#15):** `data/attract_terms.json` — the ONE new balance parameter this integration needs (Environment's half-saturation curve point; LeisureFit and Safety reuse existing data-driven parameters from `data/leisure.json`/`data/crime.json` respectively, and need no new file):
```json
{
  "environment": {
    "pollutionHalfSaturationKg": 50000,
    "comment": "Directional placeholder (GR#15): the total uncollected+backlog waste tonnage (kg) at which the Environment term reads 50/100. Mirrors crime.json's HalfSaturationActiveCrime curve shape. Balance pass pending (Aaron's balance-number regime)."
  }
}
```

---

## 4. Outputs

| Effect | Target stock/edge | Type |
|---|---|---|
| `Safety` term value | `AttractAPI.termInputs.Safety` via `SetTermInputs` (validated `[0,100]` by `validateTermInputs`) | `float64` |
| `LeisureFit` term value | `AttractAPI.termInputs.LeisureFit` via `SetTermInputs` | `float64` |
| `Environment` term value | `AttractAPI.termInputs.Environment` via `SetTermInputs` | `float64` |
| `ServiceCoverage` term value | `AttractAPI.termInputs.ServiceCoverage` — **unchanged**, still `baselineOneTermValue` (50.0) | `float64` |
| `JobAvailability` term value | `AttractAPI.termInputs.JobAvailability` — **unchanged**, still `baselineOneTermValue` (50.0) | `float64` |
| Downstream: composite `A()` score, `G(x)` net-migration response, `peopleDelta`/`netMigration` accumulators | Unchanged existing path (`attractHook.ApplyEffect` → `st.peopleDelta`/`st.netMigration`, the same `invariant.PeopleInvariant` `TrackedDelta` accumulator FEAT-169 already feeds) — this ICD changes only the THREE term VALUES `SetTermInputs` is called with, never the migration-application mechanism itself | n/a (no new accumulator) |

No new conservation-tracked stock is introduced — the terms are dimensionless `[0,100]` inputs into an existing formula, not a resource/money transfer in their own right (§11's terms are never independently conserved quantities).

---

## 5. Update Class

**T1.** None of the five terms are population/money/conservation-critical in the proposal §3 critical-tier sense — they are migration-response INPUTS, not the conservation-tracked population delta itself (that remains `attractHook`'s existing critical-tier monthly fold, unchanged by this ICD). A term value computed one tick late (e.g. `TotalPopulation` sampled at the top of the month rather than mid-month) does not violate any invariant — `AttractAPI.SetTermInputs` explicitly does NOT advance reputation or mutate any conserved stock (`api.go` line 216-218: "It does NOT advance the reputation momentum... changing one term's input changes only that term's accessor output and the composite A(), never the reputation accessor"). Monthly batching (once per `attractHook` tick) is the natural, already-existing cadence — never queued past the SAME month's `applyMigration` call, but not a per-day-tick critical requirement either.

---

## 6. Shard Scope

**Single-shard (shard 0), matching `attractHook`'s existing contract.** `attractHook.RunShard`/`SingleShard() bool { return true }` already restrict `applyMigration` to shard 0 only (`compose.go` line 969) — this integration adds term-value computation INSIDE `applyMigration`, so it inherits the same single-shard contract with no new `SingleShardHook` implementation needed (BUG-269 is already satisfied by the existing hook). `crime.SafetyTerm`/`leisure.LeisureFitAggregate`/`refuse.TonnesUncollected` are themselves whole-city or whole-district aggregate reads with no per-compose-shard structure of their own to preserve.

---

## 7. Determinism Guarantee

Every value folded into `TermInputs` this integration adds is a pure function of already-deterministic module state at the moment `applyMigration` runs: `citizens.TotalPopulation` (AC-17 `PopulationHash`-verified deterministic), `crime.SafetyTerm`/`crime.AdvanceMonth`'s own counter-based draws (keyed `hash(seed, districtID, month, purpose)` per `crime/api.go`'s `detStream`, never wall-clock), `leisure.LeisureFitAggregate` (a pure read over `venues`+the data-loaded `DefaultTaste`, no draw at all), and `refuse.TonnesUncollected`/`TonnesDisposalBacklog` (summed `RLock`-guarded accumulator reads, themselves advanced by `Generate`'s own deterministic accounting, `accounting.go`'s AC-11 mass-conservation identity). The fold order inside `applyMigration` is fixed Go source order (Safety, then LeisureFit, then Environment, then the existing HousingAffordability/Reputation path) — never map iteration, never goroutine-scheduling-dependent. **No wall-clock time is read anywhere in this integration** — every sampled value derives from the sim's own month counter (`clock.Month()`, already passed into `applyMigration`) and each source module's own internal state, never `time.Now()`.

---

## 8. Error / Registry Codes

- **`engine.attract`** owns `MET-G700`–`MET-G7xx` (`errors.go`). This integration can surface `MET-G704` (`ErrInvalidTermInput`) if a computed term value is somehow non-finite or falls outside `[0,100]` — `validateTermInputs` (`api.go` line 177) already guards every `SetTermInputs` call; a bug in this integration's arithmetic (e.g. an unclamped composite) would be caught here, never silently clamped.
- **`engine.crime`** owns `MET-Gxxx` via its own registered range (`crime/errors.go` — not re-enumerated here; see that file). Codes this integration can surface: `ErrUnregisteredDistrict` if `SafetyTerm` is queried before the citywide district is registered/advanced this tick (a caller-ordering bug, not a data problem), `ErrInvalidDistrictInput` if the population-derived `EligiblePool`/other driver value is non-finite or negative (should never occur given `TotalPopulation`'s own `int` contract, but guarded defensively per crime's own `validateDistrictInput`).
- **`engine.leisure`**: `ErrUnknownDistrict` is NOT applicable (`LeisureFitAggregate` uses district 0 / citywide, always valid per `districtKnown`'s own contract). `ErrInvalidTasteDistribution` if `Config.DefaultTaste` were ever zero-sum — should be load-time-rejected already by `cfg.validate`, defensive only.
- **`engine.refuse`**: `ErrUnknownLandUse` if the citywide aggregate cell is queried before `RegisterCell` runs this integration must add — an ordering bug, caught immediately in dev testing, never a silent zero.
- **`feat.compositionroot`** (compose's own range, `MET-G800`–`G899`, `compose/errors.go`): `ErrModuleFailed` (`MET-G801`) is the natural code for this integration's own hook to raise if any of the three new source-module calls returns an unexpected error, mirroring `consumptionHook`/`buildHook`'s existing pattern (`compose.go` lines ~817, ~865) of logging via `errs.New(ErrModuleFailed, ...)` rather than swallowing (GR#1).

---

## 9. Resilience Behaviour

In-process, always-connected — the degenerate case `integration.LocalReconnectHooks` models, identical to the FEAT-169 ICD's §9 reasoning. All three new source calls (`crime.AdvanceMonth`+`SafetyTerm`, `leisure.LeisureFitAggregate`, `refuse.Generate`+`TonnesUncollected`+`TonnesDisposalBacklog`) are deterministic given identical inputs — a bare retry without a code fix fails identically, so "retry" here means "fix the caller bug," not a backoff loop. Retry policy: NOT wired into `integration.Connection` — T1's own "batchable" contract tolerates a one-tick-late value more gracefully than T0, but there is still no remote-dispatch latency this location-transparent executor exists to absorb (in-process only). Catch-up: none needed; a crash mid-`applyMigration` is recovered by the existing checkpoint/replay path, and `crime`/`leisure`/`refuse`'s own internal state is each independently checkpoint-serializable (unaffected by this integration). Degraded mode: if any of the three new source-module constructions (`crime.New`, `leisure.Load`, `refuse.Load`) fails at `Wire` time, `feat.compositionroot`'s existing "fail loudly, zero hooks left behind" contract (its own `inbound.pattern` doc string) applies — never a silent fallback to the flat-50 constant for a module that WAS supposed to construct but didn't; ServiceCoverage/JobAvailability's flat-50 is a deliberate, ALREADY-DOCUMENTED placeholder (§3), not a degraded-mode substitution.

---

## 10. Monitoring Signals

**Status:** derived from whether `applyMigration`'s extended term-sampling block returns an error this tick (up) vs propagates one (degraded, logged per GR#1/GR#17), exactly as `attractHook.ApplyEffect` already does for the existing `SetTermInputs`/`ApplyMigration` calls. **Throughput/liveness evidence:** the three wired term values themselves are the natural "did this respond to gameplay" signal — expose them alongside `netMigration`/`consumptionDelivered` in `Composition`'s existing read-only accessor surface (mirrors FEAT-169's §10 pattern of piping a new observable through `core.WithPhaseObserver`), so a future dashboard can plot Safety/LeisureFit/Environment next to net migration on the same timeline. **Queue depth:** n/a — T1, no queue, in-process. **Peak load:** the three new source-module calls' wall-clock cost (an operational metric only, never fed back into any simulation decision), watched against the BUG-034 1M-citizen perf gate — `crime.AdvanceMonth` in particular iterates every registered district's crime types every month, so its cost must be checked at scale even though baseline-one likely registers only one citywide district.

---

## 11. Required Tests

- **Real-signal-moves-the-term (one per wired term, each must be able to FAIL):**
  - Safety: two otherwise-identical `driveTicks` runs differing only in simulated population growth must diverge in `Safety`'s value (higher population → lower `SafetyTerm`, `crime.json`'s half-saturation curve applied to a larger `EligiblePool`-driven generation) — a test that mutates the population input and asserts the term actually moves, not a test that merely calls the accessor once.
  - LeisureFit: a run with zero registered venues vs a run with several venues in the migrant taste distribution's dominant category must diverge in `LeisureFitAggregate`'s result — mutate venue registration, assert the term moves.
  - Environment: two runs differing only in whether the citywide refuse cell's collection round actually runs (one starved of collection capacity) must diverge in `Environment`'s value — mutate the uncollected-tonnage input, assert the term moves.
- **Determinism equivalence:** a compose-level test asserting two identical-seed `driveTicks` runs produce byte-identical Safety/LeisureFit/Environment term values every month, extending the existing `PopulationHash`-style shard/worker-count invariance pattern (AC-17) to these three new derived values.
- **Migration-responds end-to-end (the specific test the discovery brief names):** worsen a real crime-relevant input (population growth with no offsetting change) → assert `Safety` falls → assert `netMigration`'s monthly delta is measurably lower than an otherwise-identical run without the population growth — a genuine causal chain test, not two independent unit tests asserting unrelated facts.
- **Placeholder-tripwire tests (ServiceCoverage/JobAvailability):** a compose-level test asserting these two terms remain EXACTLY `baselineOneTermValue` regardless of any gameplay mutation — proving the "honest placeholder, not a silently-drifting fake" contract holds, and set up to FAIL loudly (not silently pass) the day a follow-up wires either term for real without updating this test.
- **AC-coverage:** the FEAT-167 acceptance criteria file (`docs/planning/acceptance/feat.attracttermswiring.md` or equivalent — to be authored per GR#25 alongside the three new edge registrations, not yet written as of this ICD) must be `tools/plan/spec-lint.js`-clean before the Go wiring is built.

---

## 12. Change Control

Additive-only: a later revision may ADD ServiceCoverage or JobAvailability wiring (once their respective prerequisite bridges/aggregates land) without a version bump, provided it does not change this version's Safety/LeisureFit/Environment Inputs/Outputs semantics. Any REMOVAL or semantic change to an already-wired term (e.g. changing Environment's composite formula, or Safety's driver mapping) requires a new version appended below, plus a fresh Destructive-verdict round (GR#23) on the affected integration code.

**Open decisions flagged by this ICD (unresolved — surfaced for Bill/Aaron):**

1. **Three new `code.json` edges do not exist yet** (`feat.compositionroot → engine.crime/engine.leisure/engine.refuse`, §1). They must be registered (GR#25) before the Go wiring described here is built.
2. **Safety and Environment are honestly SCOPE-LIMITED, not fully "real."** Both terms are wired against ONE real, compose-owned dynamic driver (population) rather than the full driver set their source modules define (`crime.DistrictInput` has fourteen fields; only `EligiblePool` is population-derived here). This is a deliberate proportionality call (GR#23 tier, "not building NASA code") — it is genuinely better than a flat 50 (it responds to city growth) but is NOT the same as a fully policy-responsive Safety/Environment term. A follow-up BOW item should wire the remaining `DistrictInput`/refuse-driver fields once `engine.defence`/`engine.education`/regeneration-investment sources exist.
3. **ServiceCoverage and JobAvailability remain flat placeholders** with no real signal anywhere in the built codebase (verified by exhaustive grep across `firms`, `market`, `services`, `citizens` exported surfaces). Two new BOW items are needed before either can be honestly wired: (a) a `engine.build → engine.services` registration bridge via the already-built `ServiceSpecFromBuilding`, and (b) a NEW aggregate query (`firms.VacancyRate()` or a citizens-side unemployment-rate query) that does not exist in any module today.
4. **The venue-registration bridge for LeisureFit** (`engine.build`'s constructed leisure-zone buildings → `leisure.LeisureAPI`'s venue registry) is a small new piece of Go this ICD's build must add — it is not itself a registered `code.json` edge change (build→leisure has no existing edge either; `code.json`'s `engine.leisure` outbound calls list `engine.citizens`/`engine.traffic`/`engine.wellbeing`, not `engine.build`) — **this is a FOURTH edge gap** (`engine.build → engine.leisure`, or `feat.compositionroot` mediating both sides without a direct build→leisure edge, whichever the Architect rules) that must also be resolved under GR#25 before build, alongside items 1-3 above.

| Version | Date | Change |
|---|---|---|
| v1 | 2026-08-18 | Initial draft, authored against `TEMPLATE.md`, pre-staging FEAT-167's Safety/LeisureFit/Environment wiring (ServiceCoverage/JobAvailability documented as honest placeholders, not wired). |
