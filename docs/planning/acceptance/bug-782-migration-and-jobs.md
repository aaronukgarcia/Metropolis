# BUG-782 - migration and council tax are blind to employment: acceptance criteria and ruling sheet

Author: BA (Sonnet), 2026-11-18. Docs only: nothing was run, no code changed. Item: BUG-782 (P2, opened 2026-09-05 from the browser-console "Capture 13"). Facts below were read from main at 01946d2; each has a file reference. Where something is inferred rather than read, it says so.

## 0. The report in one paragraph

Capture 13: 9.48 million citizens, jobs = 0 (no shops, offices or industry), business tax 0, freight tax 0, council tax about 95 percent of income, and about 47,000 move-ins per tick. The complaint is that migration and council tax do not respond to employment.

## 1. What is true today

### 1A. The TypeScript simulation (webconsole/src/sim) - the one the player's browser runs

Migration (engine.ts, `attractivenessOf` ~853-920 and the growth block ~7440-7600):

- Attractiveness is a product: tax term x transit term x civic term x station term x jobs multiplier. The civic term averages wellbeing and service coverage.
- **A jobs term exists, and it can read zero.** `jobTerm = clamp(jobs / max(workers,1), 0, 2) / 2`, where `workers = population x WORKFORCE_PARTICIPATION_RATE (0.55)`. The multiplier is `0.5 + 0.5 x jobTerm` (engine.ts ~899-905). With jobs = 0 the multiplier is 0.5: **zero jobs halves attractiveness and nothing more.** It was added by the BUG-394 round fix (F4) on 2026-09-05, the same day as Capture 13, so the capture may pre-date it.
- Unemployment also reaches move-out, but only through wellbeing: `wellbeingOf` has a "Jobs/Employment" part = 1 - `unemploymentOf(s)` (engine.ts ~12102; data.ts ~4866), and the move-out rate rises as wellbeing falls (`WELLBEING_MOVEOUT_FACTOR` 1.5, `MOVE_OUT_BASE_RATE` 0.003). It is one part among many, so it is a weak push.
- Inflow: `marketInflow = population x 0.02`, times clamped attractiveness (max 1), times a vacancy boost (1 + 3 x vacancy fraction). On top of that sits a **progress guarantee**: whenever clamped attractiveness exceeds 0.1, gross inflow is floored at `moveOuts + guaranteeFactor x max(1, capacity x 0.001)`. This was added on purpose so a jobless city "grows SLOWLY, never freezes" (engine.ts ~610-628, ~7560-7580). A flat cap of 0.5 percent of capacity per tick bounds the rest (`MAX_INFLOW_SHARE_OF_CAPACITY`, line 590). Move-ins are finally limited by homes available (`effectiveHeadroom`).
- **Capture 13's 47,000 is exactly 0.5 percent of 9.48 million.** So the capture was sitting on the flat cap: inflow was being capped, not limited by anything about jobs.
- Council tax (fiscal.ts:57-60): `round(population x 1.5667 x taxRate / defaultRate)`; the 1.5667 comes from a real 47 pounds per head per month. **It reads population and tax rate only.** No income, no employment, no dwellings. Business tax reads commercial zone count; freight tax reads industrial and mine zones. A city with no workplaces therefore has council tax as nearly all of its income by construction ("95 percent" is simply what is left when the other two are zero).
- Jobs: `totalJobs(s)` (data.ts, with a sector-partition flag branch, FEAT-2326609764, default OFF) sums building job capacity; `unemploymentOf` = max(0, workers - jobs) / workers with `WORKING_AGE_FRACTION` 0.55 (data.ts:4866). Same 0.55 as `WORKFORCE_PARTICIPATION_RATE` (engine.ts ~788) - two constants for one number, a SSOT wart.
- Since 2026-09-05: `git log` over webconsole/src/sim for migration, attract, job, employment, council or tax shows BUG-394 (move-in freeze fix), BUG-391 (diversified tax base and institutional tax - adds tax lines, does not link council tax to employment), BUG-397 (wages and transit fare), BUG-524 (unemployment into wellbeing), and sector-partition work on `totalJobs`. Nothing makes council tax depend on jobs or adds an unemployment cut-off to inflow. FEAT-2326609806 (the attract-multiplier lane the comb says never reached main) is not in `git log --grep`; the one nearby attempt, FEAT-2326609802 inc9, had its attract multiplier REMOVED by lead ruling BUG-938 r3 because it would have been a hidden population bonus.

**Would Capture 13 still happen on main today?**

- Council tax blind to employment: **yes, certain** (read directly from fiscal.ts).
- Migration still brings in tens of thousands per tick to a jobless 9.5 million city: **probably yes, medium confidence (about 70 percent).** Reasoning: the jobs multiplier halves attractiveness, but with default taxes the pre-jobs score was already around 0.24 (BUG-394 note), so about 0.12 now, which is still above the 0.1 trigger of the progress guarantee. The guarantee floors gross inflow at roughly move-outs (0.3 percent or more of population, around 28,000 at 9.48M) plus up to 0.1 percent of capacity (about 9,500), landing in the same tens-of-thousands range as 47,000. The exact figure depends on vacancy and wellbeing in that capture, which I cannot reproduce without running it. The design intent in the code comments is that a jobless city still grows; that intent is the thing Aaron must rule on (see section 5).
- The one clear improvement since the capture: the jobs multiplier exists. It is far too weak to be "migration responds to jobs".

### 1B. The Go engine (internal/engine/attract, internal/engine/compose) - the cloud engine

How migration is computed: `ApplyMigration` (attract/migration.go ~204) takes the weighted sum `A = sum(w_i x term_i) + reputation` (api.go ~483), multiplies by a wellbeing modifier, and net migration is `migrationRate x (A - AWorld)`. Weights are set in compose.go:4447 (`baselineOneAttractConfig`): jobs 0.2, housing 0.2, services 0.15, environment 0.1, leisure 0.1, safety 0.1, reputation 0.15. Housing vacancy is a hard capacity limit passed alongside.

What feeds each term today (`applyMigration`, compose.go ~3300):

| Term | Fed from | Real today? |
|------|----------|-------------|
| Safety | engine.crime | real |
| LeisureFit | engine.leisure venues | real |
| Environment | engine.refuse waste backlog | real |
| ServiceCoverage | engine.services `CoverageSummary` (servicesfirms_wire.go:35) | formula real, **signal zero**: no build-to-services bridge, so zero demand and the term reads 100 (maximum) |
| JobAvailability | engine.firms `LabourMarket().VacancyRatePerMille` through a half-saturation curve (servicesfirms_wire.go:59; data/attract_terms.json, half-saturation 100 per mille) | formula real, **signal zero**: no automatic firm founding, so vacancies are 0 and the term reads 0 |
| HousingAffordability | engine.households `HousingAffordability` | real since FEAT-1972079927 |
| HousingVacancy (hard limit) | `housingVacancyFor` (compose_vacancy.go:56): seed homes plus completed dwellings minus population | real since BUG-1214 |
| JunctionThroughput | constant `baselineOneJunctionThroughput` | placeholder |

Policy and tax settings feed nothing into attract (consistent with the Opus review note).

Employment data that exists but is not connected to the jobs term:

- **Citizens `Employment{State, Sector}` and `Workplace`** (citizens/citizen.go ~80-100). Compose marks working-age residents Employed or Unemployed by a **deterministic 75 percent coin-flip per citizen** (`employmentDecision`, moneycirc.go:255; `employmentRateOfWorkingAgeFraction` 0.75), permanently, independent of any job existing. So the Go employment status is also blind to jobs; the unemployment figure used for unemployment support (`unemployedResidentCount`, fiscal_wire.go:111) is the coin-flip, not a jobs-versus-workers figure.
- **Build workplaces:** `BuildAPI.StandingCapacities()` returns, per completed structure, `ResidentCapacity` and `JobCapacity` (build.go:1505-1532). Zone job capacity comes from data/buildings.json `zones`: shop 10, office 40, entertainment 15, farming 6, manufacturing 50, heavy industry 80, mining 30, dwelling 0. `housingVacancyFor` already walks this list and sums `ResidentCapacity`; **nothing sums `JobCapacity`.** This is the smallest link: the data is already one field away.
- **Firms:** `engine.firms` has vacancies and a labour-market aggregate, but the only firm compose ever registers is one auto-placed Builders' Merchant with 5 staff (moneycirc_inc2.go, `buildersMerchantStaffCount`). Shop, office and industry buildings never become firms. data/buildings.json building entries (356) carry free-text capacity and notes and only 8 `staffingNeed` entries (services), so job counts for the building catalogue proper do not exist as data except via the zones table above.
- Full jobs model: FEAT-1972079929 is the deferred stage plan; compose comments call the current single-sector marking a "minimal-scope simplification".

**Does Capture 13 reproduce in Go?** The Go engine cannot reach 9.48M by this path and has no council tax per head; the comparable defect is structural: **migration and employment are both blind to built workplaces**. A Go city with homes and zero workplaces: jobs term reads 0 (costs 0.2 of weight, so it is lower than a city with vacancies, but a city with a zero jobs term and good everything else can still have A above AWorld), 75 percent of working-age residents are marked Employed anyway, and moves in are bounded only by homes. Confidence: high on the wiring (read from code), medium on whether net migration is positive in a given seed (depends on `baselineOneAWorld` and `migrationRate`, not read).

## 2. Expected behaviour as testable criteria

Rules for all of these: **directional, not exact numbers.** Every constant introduced is a placeholder marked for Aaron (the project's balance-number regime) and each test compares a city against a same-seed control. Each criterion says Go, TS or Both. Test names are patterns; gate packages are given. Each must be proven able to fail (briefing lesson: the test fails when the fix is reverted).

**AC-1 (Both). A city with no workplaces does not grow without bound.**
Observable: population after N months for a city with homes and zero workplaces, same seed, as the housing stock grows. Expect population to level off at or below a bounded value, or to fall, and never to track housing capacity. Gate: Go `TestBug782_NoWorkplacesCityStaysBounded` in internal/engine/compose; TS `bug-782-jobless-city-bounded.test.mjs` in webconsole sim tests. Catches: the present progress guarantee and flat cap letting a jobless city fill all housing.

**AC-2 (Both). Adding workplaces raises migration against a same-seed control.**
Observable: net migrants over a fixed window in city A (control, homes only) versus city B (identical plus completed workplaces with job capacity). B's net migration is strictly higher, and monotone as more job capacity is added until jobs match the workforce, then no further gain. Gate: Go `TestBug782_WorkplacesRaiseNetMigration`, TS `...workplaces-raise-inflow`. Catches: a job term that is wired but flat.

**AC-3 (Go). The jobs term is fed from built workplaces.**
Observable: `jobAvailabilityTerm` reads 0 with homes only, rises as completed structures with `JobCapacity` are added, falls as population rises past jobs (jobs versus workers, same shape as the TS `jobTerm`). Gate: `TestBug782_JobTermFollowsBuiltJobCapacity` in compose (beside `TestHousingVacancy*`). Catches: the zero-signal reading described in 1B; a fed term that ignores population.

**AC-4 (Go). The jobs term moves with demolition and with save/load.**
Observable: demolishing a workplace lowers the term the next month; a save/load round trip mid-run gives an identical term and identical population hash to an uninterrupted run. Gate: `TestBug782_JobTermSurvivesDemolitionAndLoad`. Catches: a cached job total that goes stale (the BUG-1143 lesson: use `LoadAt`).

**AC-5 (Go). Employment status follows jobs, not a coin-flip.**
Observable: employed count never exceeds job capacity (plus off-map commuters); with zero workplaces no resident of working age is Employed (off-map excepted); unemployed count equals working-age residents minus filled jobs; unemployment support (`unemployedResidentCount`) follows it. Gate: `TestBug782_EmployedNeverExceedsJobs`. Catches: employment independent of jobs. This is the biggest piece; see ruling R5 on whether it ships in the same increment.

**AC-6 (Both). High unemployment stops the pull.**
Observable: with unemployment above the ruled threshold U, net migration from the jobs and housing terms is not positive (other terms may still hold residents; this criterion is about arrivals, not departures). Directional test: a city at unemployment above U with generous housing, services and safety has net migration at or below zero. Gate: Go `TestBug782_HighUnemploymentStopsInflow`, TS counterpart. Catches: positive migration into mass unemployment (Capture 13's behaviour).

**AC-7 (Both). Unemployment pushes people out.**
Observable: at unemployment above U, move-out rate is higher than the same city at low unemployment, by a margin larger than the present wellbeing-only effect (that effect is one of many parts of wellbeing). Gate: `...HighUnemploymentRaisesMoveOut`. Catches: the weak, indirect push in 1A.

**AC-8 (TS). The progress guarantee no longer overrides a jobless city.**
Observable: the guarantee floor (engine.ts ~7560) is not applied when unemployment is above U (or its floor is scaled by the jobs multiplier). Existing BUG-394 tests (no fixed-point freeze at positive vacancy; bug-394-round2-ruling) keep passing, so a city with jobs must still never freeze with empty homes. Gate: the BUG-394 suite plus `bug-782-guarantee-respects-jobs`. Catches: reintroducing the BUG-394 freeze while fixing BUG-782. Both directions must be tested.

**AC-9 (TS). Jobs multiplier strength matches the ruling.**
Observable: jobs multiplier at zero jobs is at or below the ruled floor (today 0.5); pin the constant as a named placeholder with Aaron-pending marker. Gate: `bug-782-jobs-multiplier-floor`. Catches: a silent retune.

**AC-10 (Both). Council tax follows the ruled basis (R3).**
Observable depends on the ruling. If per dwelling or per household: a city with no homes pays none. If tied to employment/income: council tax per tick falls as unemployment rises, same seed; a city with zero jobs yields council tax at or below the ruled share of the employed city's. In every case the P and L statement and the finance screen (BUG-1213 treasury statement, F2) reconcile. Gate: TS `bug-782-council-tax-basis`; Go (the Go engine has no per-head council tax; the equivalent is the fiscal module's household posting) `TestBug782_CouncilTaxBasis` only if R3 chooses to mirror. Catches: income share of one tax at 95 percent on a jobless city staying unchanged.

**AC-11 (Both). A 9.5 million city with no jobs declines or levels off (R4).**
Observable: from a captured or synthetic 9.5M city with zero jobs, population over 24 months does not rise, and monthly net migration is not positive; no module failure and no overflow. Gate: TS scale gate `bug-782-capture13-regression` using a Capture 13-shaped fixture (also feeds BUG-617's lesson: bound the load composition); Go equivalent at 1M and 10M using the existing scale gates. Catches: Capture 13 itself.

**AC-12 (Both). Convergence is not made worse.**
Observable: the engine A/B comparison for population (docs/planning convergence fixtures; `Compare` fails when a field was never evaluated, BUG-1140) on the workplace-less and the workplace-present scenarios stays within the existing declared tolerances, or the tolerances are changed in the same commit with the rule recorded. Gate: the converge fixtures (`go test` converge package, `--webconsole-tsx-all`). Catches: one engine learning the rule while the other keeps the old behaviour.

**AC-13 (Both). Determinism and conservation.**
Observable: same seed twice gives identical populations; the population conservation identity (pop_after = pop_before + births + move-ins - deaths - move-outs) holds on every tick of every test above. Gate: existing detgate plus the identity assertion in each new test. Catches: float or map-order drift in the new term.

Count: 13 criteria (3 Go-only: AC-3, 4, 5; 2 TS-only: AC-8, 9; 8 both).

## 3. Convergence: keep the two engines from drifting further

The cold assessment (docs/planning/assessment.docx, summarised in docs/planning/backlog-comb/cold-assessment-raw.md) recommends not adding mechanics to the TS sim without a convergence plan. The TS sim today has a jobs multiplier and a progress guarantee that the Go engine does not have, and the Go engine has a seven-term weighted score and a household affordability term that TS does not have. Each engine already computes migration by a different formula, so "the same rule" means the same **observable behaviour**, not the same code.

Smallest plan that respects it:

1. **Write the rule once, as data and prose** (a short section in this file's ruling sheet once Aaron rules: unemployment threshold U, jobs weight, council tax basis). Placeholder values live in data files where Go reads them (data/attract_terms.json already holds `jobAvailability`) and in a single TS constants block.
2. **Go first.** Wire built `JobCapacity` into `jobAvailabilityTerm` (AC-3, 4), add the Go AC-1, 2, 6 tests, and add a **converge fixture** scenario "homes only versus homes plus workplaces" recording the population trajectory in both engines. Record which of Go's and TS's trajectories differ and by how much.
3. **Then mirror in TS** the smallest change that makes the new fixture pass within tolerance: AC-8 (guarantee respects jobs) and AC-6/7. The jobs multiplier already exists, so TS is mostly a strengthening and a guard on the guarantee, not a new mechanic.
4. **Council tax is a TS change only** (the Go engine has no per-head council tax). Do it in the same increment as step 3, behind the ruled basis, and add the same-seed finance fixture. If R3 says "leave per head", record it as an accepted limitation and close that half of the item.
5. **No new TS mechanic lands without its converge-fixture row.** AC-12 is the gate.

Implication of a fix in one engine for the other: a Go jobs link alone widens the gap (Go would respond to workplaces, TS responds weakly); a TS-only fix widens it the other way (TS would stop a jobless city, Go still admits it). Either order leaves a window of divergence, which is why steps 2 and 3 should land in consecutive increments with the fixture recorded at step 2.

## 4. Sizes (lane-hours; one lane-hour is one Sonnet developer working for an hour, including its tests; excludes the destructive round)

| Piece | Estimate |
|-------|----------|
| Go jobs link: sum `JobCapacity` from `StandingCapacities`, jobs-versus-workers term, AC-3, 4 | 3-4 |
| Go employment follows jobs (AC-5, the coin-flip replaced; touches fiscal unemployment support, moneycirc tests) | 5-8 (largest; many fixtures assume 75 percent employed) |
| Go AC-1, 2, 6, 7 directional tests, plus load/demolition coverage | 3-4 |
| Converge fixture "workplaces versus none", both engines, recorded | 3-4 |
| TS: guard the progress guarantee, unemployment cut-off and push, jobs multiplier pin (AC-6 to 9) | 4-5 |
| TS: council tax basis (R3) and finance statement reconciliation (AC-10) | 2-4 |
| Capture-13-shaped scale gate (AC-11, TS and Go) | 3-4 |
| Opus rounds (two increments) | 2 rounds |
| Total | roughly 23-33 lane-hours over three increments |

## 5. Rulings Aaron owes (with a recommended answer)

R1. **Should unemployed migrants still arrive, and at what unemployment does the pull stop?**
Recommended: the pull weakens linearly as unemployment rises and stops at **20 percent** (placeholder); below that, arrivals still happen. Rationale: some inflow into mild unemployment is realistic and keeps cities from freezing (the BUG-394 lesson); a hard stop at a high level gives AC-6 its testable edge.

R2. **Should the progress guarantee survive in a jobless city?**
Recommended: **no** above the R1 threshold; it stays for cities that have jobs (keeps the BUG-394 freeze fix). This changes a deliberate design choice recorded in engine.ts ("jobless city grows slowly, never freezes") so it needs your say.

R3. **Is council tax per head, per dwelling, or on income?**
Recommended: **per dwelling that is occupied** in the Go convergence target (fits the households model) and, for TS, keep per head but **scale by the employed share blended with a floor** (for example half the rate paid regardless, half scaled by employment) so a jobless city pays less but not nothing. Simplest acceptable answer if you want minimum change: leave per head and close the council-tax half as "by design" - I rate this the weakest option because Capture 13 shows 95 percent of income from people with no jobs.

R4. **What happens to a 9.48M city with no jobs: emigration, decline, or nothing?**
Recommended: emigration at a bounded rate (move-out rate raised by unemployment, AC-7), so population falls gradually toward the jobs-supported level, not a cliff. No automatic game over.

R5. **Is Go employment-follows-jobs (AC-5) part of this item or a follow-up?**
Recommended: **follow-up item**, same epic. Ship the Go jobs-term link first (AC-3, 4) because it is small; AC-5 changes the population/fiscal fixtures widely and should have its own round.

R6. **Placeholder numbers (all marked "Aaron pending", directional tests only):** unemployment cut-off U (20 percent), jobs weight in Go (keep 0.2), jobs multiplier floor in TS (today 0.5; recommend 0.25 so zero jobs cuts attractiveness to a quarter), job-term half-saturation (keep 100 per mille), the 0.55 workforce fraction (also fix the duplicate constant), the R3 blend (50/50).

R7. **Which engine is "the game" for this fix?** If the TS sim is the player's real game today (it is the browser console) and Go is the 100M target, confirm that the Go-first order in section 3 is acceptable even though the player does not see Go changes until the engines converge. Recommended: yes, Go first, because the cold assessment's objection is to unconverged TS mechanics, not to TS fixes that follow a recorded Go rule.

## 6. Recommended order of work

1. Aaron rules R1 to R4 (R5 to R7 can follow).
2. **Go jobs link** (AC-3, 4, 1, 2, 6) with a converge fixture - small, self-contained, the "next smallest link after housing".
3. **TS guard and cut-off** (AC-6 to 9) mirrored against the fixture.
4. **TS council tax** (AC-10) per R3.
5. **Scale gate** (AC-11) before closing BUG-782.
6. **Go employment follows jobs** (AC-5) as its own item, then close the loop with AC-12.
Each increment: one Opus destructive round, then commit (GGR83). Run `--webconsole-tsx-all` as well as the bounded set for TS changes, and check CI after pushing.

## 7. Open points the BA could not settle from the code

- Whether Capture 13's 47,000 was produced before or after the BUG-394 jobs multiplier landed (the capture time and the commit time of that day are not recorded on the item). It does not change the verdict, since the multiplier only halves attractiveness.
- The exact net migration sign in Go for a homes-only city (needs `baselineOneAWorld` and `baselineOneMigrationRate`, not read).
- FEAT-2326609806 was not located in git history; the comb's claim that its lane never reached main is unverified here.
