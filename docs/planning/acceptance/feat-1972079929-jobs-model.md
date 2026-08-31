# FEAT-1972079929: Detailed employment + jobs model (ONS-grounded wages, families, essential-jobs fill)

**Mkey:** FEAT-1972079929

**Relates:** FEAT-225 (pay/ keystone — occupation matrix, per-citizen wages, M1, LIBOR/NOI debt ceiling; this feature may BE its realisation or EXTEND it — Architect to reconcile), FEAT-1972079927 (wage distribution stops being equal-split), engine.citizens EmploymentState, engine.staffing, engine.finance.

**GR#25:** Depends on FEAT-225 A1 (occupation registry `data/occupations.json` already landed per BOW comments 2026-08-22); this feature wires the desirability/essential-jobs logic and the config page (no new `code.json` edge beyond what FEAT-225 registered).

**Date:** 2026-08-31 (BA criteria ahead of build).

**Status:** `draft-ahead` (long-range). Scope: detailed employment mechanics — job-family taxonomy, wage spread from UK ONS, desirability scaling with pay, essential-jobs-must-fill constraint, citizens assigned to nearby workplaces, unfilled-jobs signal, config page for editable wages + global YoY inflation + per-job override, deterministic assignment seeded from state, wage conservation through the ledger.

**Balance regime (GR#15, binding):** every player-felt number (wage bands per family, inflation %, desirability weights, essential-jobs thresholds) is a **data placeholder pending Aaron's balance pass** — none is a hardcoded literal in Go. Checks are mechanical (does assignment happen, is it bounded, does wage flow conserve, is it deterministic), never pinned to a specific magnitude.

---

## Relationship to FEAT-225 (GR#3 — read first; no silent duplication)

FEAT-225 `engine.finance` AC-37..42 landed per-citizen wage crediting as the keystone: it replaced the aggregate `PostWages(total Money)` stub with real per-citizen `Wealth` credits via `finance->citizens` edge. FEAT-225 A1 (occupation registry, `data/occupations.json`, 16 roles with R_occ multipliers) landed in a073dbf.

**This feature — FEAT-1972079929 — is the realisation layer on top of that foundation.** It extends A1's occupation registry with:
- Job-family grouping (healthcare: nurse/doctor/carer; transport: bus driver/HGV; sanitation: garbage collector; etc.)
- Desirability logic (higher wage → more citizens want the job, all else equal)
- Essential-jobs constraint (some jobs MUST be filled even at lower wages, preventing a city where everyone chases top earners and garbage goes uncollected)
- Job assignment algorithm (citizens fill jobs at nearby workplaces, respecting both desirability and essential-jobs capacity)
- Unfilled-jobs signal (vacancy count, per job family, visible to the player)
- Config page (editable wages, global YoY inflation slider, per-job override)
- Determinism (assignment driven by state/seed, reproducible)
- Conservation (wages paid = citizen wealth credited, exact through the ledger)

**Open reconciliation (Architect only):** The BOW item states "this may BE [FEAT-225's] realisation or extend it" — the BA cannot assume. If FEAT-225's A1/A2/A3 landed occupation registry + building-completion wiring + per-citizen wage crediting, **this feature's acceptance scope assumes those are done and this feature adds the jobs-model layer on top.** If FEAT-225 is stalled, this feature's criteria may need re-scoping.

---

## Evidence (why this is P1)

Aaron 2026-08-31: "build a highly detailed jobs model NOW (replaces inc1's equal-split wages)… realistic wage spread pulled from the UK ONS (national statistics office) — real occupation wage data; ALL job types mapped as FAMILIES OF JOBS… Desirability scales with pay BUT the city NEEDS essential lower-paid roles filled (garbage collectors, bus drivers, nurses) so the model must fill essential jobs, not just let everyone chase top wages… employed earn their occupation wage, unemployed don't (unlocks the unfilled-jobs signal)."

**Northstar waypoint 1/4 — the model is fundamental to making Baseline One watchable.** Without job families, wage spread, and essential-jobs fill, every citizen's wage is identical (today's equal-split stub), the unfilled-jobs signal is meaningless (no job goes unfilled at any wage), and the player cannot see the city's labour market under stress.

---

## Design

### 1. Job-Family Taxonomy

- **Source:** UK ONS (Office for National Statistics) occupation wage data, real annual salaries. The occupation registry (`data/occupations.json`, landed by FEAT-225 A1) carries 16 role codes (e.g. `nurse`, `doctor`, `garbage_collector`, `bus_driver`, `teacher`, `software_engineer`, etc.), each with a base wage multiplier `R_occ` and a **family affiliation** (a string key mapping to one of 8 families: healthcare, transport, sanitation, education, retail/commercial, industrial/manufacturing, tech/r&d, civic/safety).

- **Family definitions (8 base families, extensible):**
  - **Healthcare:** nurse (R=1.0), doctor (R=2.0), carer/assistant (R=0.7)
  - **Transport:** bus driver (R=0.9), HGV driver (R=0.95), taxi driver (R=0.7)
  - **Sanitation:** garbage collector (R=0.8), recycling operator (R=0.75)
  - **Education:** teacher (R=1.0), professor (R=1.5), teaching assistant (R=0.6)
  - **Retail/Commercial:** shop assistant (R=0.5), supermarket cashier (R=0.55), restaurant worker (R=0.5)
  - **Industrial/Manufacturing:** factory worker (R=0.6), supervisor (R=0.9), engineer (R=1.8)
  - **Tech/R&D:** software developer (R=2.5), scientist (R=3.0), IT support (R=1.5)
  - **Civic/Safety:** police officer (R=1.0), firefighter (R=1.0), social worker (R=0.85)

- **Data file:** `data/occupations.json` (or extended from FEAT-225 A1's version) carries a `families` object with family keys, each containing the list of role codes in that family. The file is loaded at startup and used as SSOT for all job logic (GR#3).

### 2. Wage Spread & Desirability Scaling

- **Base wage per citizen:** `S_final = S_base × R_occ × (1 + α G)` (from FEAT-225 pay/ brief), where:
  - `S_base` = global baseline salary (placeholder, e.g. £30,000/year)
  - `R_occ` = occupation multiplier (e.g. nurse = 1.0, doctor = 2.0, garbage collector = 0.8)
  - `G` = Generosity slider (placeholder, range -5…+5; per FEAT-225 D-9, Generosity is input only here — derived Unrest is a separate signal)
  - `α` = scaling factor per point of Generosity (placeholder, e.g. 0.1 = 10% wage swing per point)

- **Desirability:** Citizens prefer higher-wage jobs. Desirability is **not a free parameter**; it is **mechanically derived from wage bands per family**. When a citizen is unemployed and a job opens, the citizen picks the highest-wage available job within commute distance (see Open Q § Commute/Distance Model). If no high-wage job is available, the citizen accepts a medium-wage job; if none, a low-wage essential job (see § Essential-Jobs Constraint).

- **Essential-Jobs Constraint:** Certain job families (sanitation, refuse collection, bus driving) carry a **family-level "essential fill" flag** in `data/occupations.json`. A city with unfilled garbage-collector jobs is unsustainable (refuse piles up → decay → death spiral). The assignment algorithm must **guarantee that essential-jobs families are filled to a minimum threshold (e.g. 80% of posted capacity) before citizens are free to chase top-wage jobs.** This is the "no one wants to be a garbage collector even though the city needs them" constraint in mechanical form.

### 3. Job Assignment Algorithm (Citizens → Nearby Workplaces)

- **Trigger:** When a building is completed (wired by FEAT-225 A2) or a workplace becomes operational, it posts open jobs (e.g., Hospital posts 10 nurse jobs, 2 doctor jobs). When a citizen transitions from unemployed to job-seeking (e.g., newly arrived, or newly unemployed due to workplace closure), the citizen is assigned to a job.

- **Assignment logic:**
  1. Find all **nearby workplaces** within a commute distance (placeholder, e.g. 5 tiles or Euclidean < 1000m; see Open Q). Exclude workplaces with zero capacity.
  2. For each nearby workplace, enumerate unfilled jobs (jobs posted but not yet assigned to a citizen).
  3. **If essential-jobs families in the city are underfilled** (< 80% of capacity), prefer an essential job first, even if lower-wage. Return the highest-wage essential job.
  4. **Else** (essential families are filled), pick the highest-wage job available. Return that job.
  5. **If no jobs nearby,** the citizen remains unemployed. Post an unfilled-jobs signal (see § Unfilled-Jobs Signal).
  6. **Determinism:** The assignment logic iterates workplaces in a sorted order (e.g., by workplace ID, never by Go map iteration order) and uses a seeded RNG if randomness is needed (e.g., tie-breaking between equal-wage jobs). The seed is derived from the citizen ID + the current tick, ensuring the assignment is reproducible (GR#21).

### 4. Employed/Unemployed State & Wage-Gated-on-Employment

- **State:** Each citizen carries an `EmploymentState` enum (0=unemployed, 1=employed, 2=on_leave, 3=retired, 4=student, 5=other) — this is the closed enum from FEAT-198; no widening.

- **Wage crediting:** When a citizen is `employed`, the citizen's wage is credited to `Wealth` each month via `finance.PostWages(citizen, occupation_wage)` (reusing FEAT-225 A3's per-citizen mechanism). When a citizen is `unemployed` or any other state, no wage is credited. **Unemployed citizens have zero household income from employment but may draw from savings/benefits (scope: engine.services, not this feature).**

- **Unemployment signal:** The player sees "Unemployed: X citizens" on the F1 screen. The unfilled-jobs signal is separate (see below).

### 5. Unfilled-Jobs Signal

- **What it measures:** For each job family, the count of open jobs posted by workplaces but not yet assigned to any citizen (e.g., "Healthcare: 15 unfilled nurse jobs, 3 unfilled doctor jobs").

- **Why it matters:** Unfilled garbage-collector jobs → refuse accumulates → decay accelerates → Death Spiral score rises (FEAT-225/FEAT-???, fiscal circuit). The signal is the **leading indicator** the player uses to know they need to adjust wages, add hiring incentives, or reduce job-demanding facilities.

- **Computation:** Query all workplaces, sum their unfilled-jobs counts by family. Recompute at the end of each tick (when jobs are assigned and closed).

- **Display:** F2 macro dashboard shows unfilled-jobs by family (mock line item: "Healthcare vacancies: 18", "Sanitation vacancies: 7"). A **critical threshold** (placeholder, e.g., sanitation > 5 unfilled → visual warning) is data-defined.

### 6. Config Page (Editable Wages, Global YoY Inflation, Per-Job Override)

- **Three-level control hierarchy:**
  1. **Global baseline salary:** `S_base` (e.g., £30,000/year). Slider or text input on the config page. Changes apply to all citizens on the next month boundary.
  2. **Global YoY inflation rate:** `inflation_rate_annual` (placeholder, e.g., 3% / year). Slider: -5% to +10%. Applied each year (month % 12 == 0): `S_base_new = S_base × (1 + inflation_rate_annual)`. This is **not the Generosity slider** (GR: Generosity is FEAT-225 D-9, a separate input); inflation is a **simulation-level knob** the player adjusts to model economic cycles.
  3. **Per-job multiplier override:** Each occupation can have a **user-set multiplier override** (default: unset = use `R_occ` from the registry). E.g., player sets "garbage_collector" to 1.2× (boost the wage 20% above the baseline) to recruit more sanitation workers. Overrides are entered on the same config page and apply on the next month boundary.

- **UI:** A F8 Config screen (or F9, TBD) with:
  - Section 1: "Economy" with `S_base` slider, inflation %-slider, apply/cancel buttons.
  - Section 2: "Occupation Overrides" — a scrollable list of the 16 (or N) occupations, each with a multiplier input (default blank = "use registry"). Save/cancel buttons.
  - Confirmation: "Apply changes starting next month?"

- **Determinism & ledger:** Changes to S_base and inflation apply at month-end (month tick where `month % 12 == 0` for inflation). Override multipliers apply at the start of the next month. All changes are **idempotent** (re-running the same config twice produces the same state) and **logged to the ledger** (a ledger entry "Config adjustment: garbage_collector +20% override" for audit).

### 7. Determinism (State-Derived/Seeded Assignment)

- **Seed source:** The job-assignment RNG seed is `hash(citizen_id || current_tick)` — deterministic, never `time.Now`. The seed is recomputed every tick, so the same citizen re-running a replay of the same tick produces the same assignment.

- **Sorted iteration:** All map iterations (workplaces, jobs, families) use `sort.Slice` or a pre-sorted data structure (never raw `for k, v := range map`). Order is by ID (workplace ID, job family ID, occupation code).

- **Check:** A headless replay test — (a) runs 12 ticks of a snapshot with N citizens and M workplaces, recording job assignments, (b) re-runs the same snapshot again, (c) asserts byte-identical assignments (citizen ID → occupation, wage).

### 8. Conservation (Wages Move Through the Ledger)

- **Ledger entry:** When a citizen is employed and a month advances, the citizen's wage is posted as:
  - **Credit to citizen's Wealth** (via `finance.PostWages(citizen, occupation_wage)`)
  - **Debit from the municipal ledger** (the treasury pays wages, exact `finance.Money` int64 micro-pounds, overflow-safe via GR#16 `num.SatAdd`/`satAddMoney`)

- **Aggregation:** The total wage bill (summed across all employed citizens) is conserved: `Σ(citizen_wage) == total treasury debit`, exact to the micro-pound. **No wage is created or destroyed.**

- **Check:** A conservation test (a) seeds a city snapshot with known wages and occupations, (b) advances one month, (c) asserts `treasury_balance_delta == -Σ(employed_citizen_wages)` and no money is missing. Reuse the existing `engine.finance` conservation test shape (TestMoneyConservationOver120Months).

---

## Acceptance Criteria

### AC-1 (Job-family taxonomy is data-defined, not hardcoded)

**Requirement:** The 8 job families (healthcare, transport, sanitation, education, retail, industrial, tech, civic) and their role memberships are loaded from `data/occupations.json` at startup. No family affiliation or job role is hardcoded in a Go `switch` or `const`. The occupation registry carries a `families: { healthcare: [...roles...], transport: [...], ... }` object.

**Check:** (a) `grep -rn "healthcare\|sanitation\|transport"` over `engine/staffing` or the jobs-model package finds only data-loading code (`json.Unmarshal`, file I/O) and no hardcoded string literal assignment to a family. (b) A test loads `data/occupations.json`, asserts the `families` object has exactly 8 keys (or a data-defined count), and asserts each family contains > 0 role codes. (c) Rename a family in the JSON (e.g., "healthcare" → "medical") and re-run the test; it still passes with the new name. (d) A headless run with a modified JSON carrying 9 families produces output using all 9 families without code recompilation.

**Lazy implementation this rejects:** A Go `const` defining `familyHealthcare = "healthcare"` and wiring that const to the registry (so the string is in code, not in data) fails (a) — the brief requires data-definedness, not parameterization of a hardcoded list.

---

### AC-2 (Wage spread per occupation derives from R_occ multipliers)

**Requirement:** The final wage for a citizen in occupation O is `S_final = S_base × R_occ[O] × (1 + α G)`, where R_occ is loaded from `data/occupations.json` (not hardcoded), S_base is the global baseline (config-adjustable), α is a placeholder constant (data-defined), and G is the Generosity slider. Occupations with higher R_occ have proportionally higher wages.

**Check:** (a) The occupations.json carries an `R_occ` field (or `wage_multiplier`) for each occupation, with placeholder values (e.g., nurse=1.0, doctor=2.0, garbage_collector=0.8). (b) A unit test computes wages for two occupations (nurse, garbage_collector) with known S_base, G, and α, and asserts `wage_doctor / wage_nurse ≈ 2.0` (or the multiplier ratio from JSON) and that both are integer `finance.Money` (no float). (c) Mutate R_occ for garbage_collector in the JSON to 1.5× (boost it), re-run, and assert the wage_garbage_collector increases proportionally. (d) A determinism test re-runs the same wage calculation 10 times and asserts byte-identical results (no floating-point rounding variance).

**Lazy implementation this rejects:** A wage calculation that rounds at an intermediate step (e.g., `S_base * R_occ` in float, then `round()` to `Money`) risks precision loss and fails (d) under float rounding variance — use `finance.mulDiv` or `num.SafeMul` for deterministic integer arithmetic.

---

### AC-3 (Desirability is implicit in wage rankings, not a separate parameter)

**Requirement:** Citizens prefer higher-wage jobs. There is **no separate "desirability score"** tuning how much citizens want a job — desirability is **purely a consequence of wage ordering**. When multiple jobs are available, citizens pick the highest-wage job within commute range. If no high-wage job is available, they accept lower-wage jobs. Essential-jobs families are filled *first* (see AC-5), then top-wage jobs.

**Check:** (a) A unit test sets up a workplace with two open jobs: Job A (garbage_collector, wage £25k/yr) and Job B (nurse, wage £40k/yr), both within commute range of an unemployed citizen. The citizen is assigned to Job B (higher wage). (b) A second test: only Job A (garbage_collector, £25k) is available; the citizen is assigned to Job A (no choice). (c) A third test: the same setup but garbage_collector is flagged as "essential_fill=true" (see AC-5), and the city's sanitation capacity is 0% filled; the citizen is assigned to Job A (essential fill overrides desirability). (d) A search for hardcoded desirability fields (`desirability: Number`, `attractiveness_bonus`) in the jobs-model code finds none outside AC-5's essential-fill logic.

**Lazy implementation this rejects:** A build that adds a separate "job desirability score" column to the occupations table and uses it to randomly assign jobs (ignoring wage ordering) fails (a)/(b) — wage is the only desirability signal.

---

### AC-4 (Job assignment iterates workplaces in sorted order; assignment is deterministic)

**Requirement:** When a citizen seeks a job, the assignment algorithm iterates over nearby workplaces in a **fixed sorted order** (by workplace ID, never by Go map iteration). The assignment decision is **state-derived and seeded** (seed = `hash(citizen_id || tick)`, never `time.Now`). Running the same snapshot twice produces identical assignments.

**Check:** (a) A determinism test: take a city snapshot (known workplaces, citizens, jobs), run 12 ticks recording all job assignments, then reload the snapshot and run 12 ticks again. Assert citizen → occupation assignments are **byte-identical** (same citizen gets the same job on the same tick). (b) `grep -rn "for .* := range"` over the assignment loop finds all map iterations are wrapped in `sort.Slice(..., func(i, j) int { return IDs[i] < IDs[j] })` or use a pre-sorted slice. (c) `grep -rn "time.Now\|time.Since\|rand.Intn"` (outside test code) finds no unsalted RNG calls in the assignment path. (d) A test flips the tick counter by 1 (e.g., tick 100 vs tick 101) and asserts the assignment seed changes, so the same citizen gets a different assignment (or the same one, but deterministically).

**Lazy implementation this rejects:** An assignment that iterates workplaces by raw map iteration (`for wid, workplace := range workplaces`) fails (b) — Go map iteration order is randomized, so the same city snapshot produces different assignments on consecutive runs.

---

### AC-5 (Essential-jobs families are filled to a threshold before citizens chase top-wage jobs)

**Requirement:** Certain job families (sanitation, refuse, public transport) carry a **family-level "essential_fill" flag** in `data/occupations.json` (boolean, default false for most families). When a citizen is job-seeking:
  1. Compute the current fill % for each essential-fill family (filled jobs ÷ posted jobs, per family).
  2. If any essential-fill family is **below a data-defined threshold** (placeholder, e.g., 80% filled), the citizen is directed to the lowest-wage available job **in that underfilled essential family** (even if a higher-wage non-essential job is available).
  3. **Else** (all essential families are ≥ 80% filled), the citizen picks the highest-wage available job, regardless of family.

**Check:** (a) `data/occupations.json` carries an `essential_fill: true` flag on at least 3 families (sanitation, transport, civic_safety). (b) A unit test: sanitation capacity is 10 jobs; 2 are filled (20% fill). A nurse job (wage £40k) and a garbage_collector job (wage £25k) are both available. The citizen is assigned to garbage_collector (essential fill overrides higher wage). (c) Raise sanitation fill to 8/10 (80%); now the same citizen is assigned to nurse (essential is satisfied, so chase top-wage). (d) A monotonicity test: run a city with essential-fill threshold at 80%. Gradually increase sanitation posting without hiring citizens; assert unfilled sanitation jobs rise monotonically (no sudden drop). Then hire 8/10; assert unfilled jobs drop to 2, and new job-seeking citizens are now free to pick non-essential jobs.

**Lazy implementation this rejects:** A build that computes essential-fill **globally** (e.g., "if 80% of all jobs are filled, unlock top-wage chasing") instead of **per-family** fails (b)/(c) — essential jobs must be filled *by family*, not in aggregate.

---

### AC-6 (Unemployed citizens have zero employment wage; employed citizens are creditted their occupation wage)

**Requirement:** `Wealth` crediting is gated on `EmploymentState == employed`. When a citizen is `employed` and a month tick fires, `finance.PostWages(citizen, occupation_wage)` posts the wage exactly. When a citizen is `unemployed` or any other state, no employment wage is posted (the citizen may draw from savings/benefits, but that is engine.services scope, not this feature).

**Check:** (a) A unit test: set two citizens, one `employed` (nurse, £40k/yr) and one `unemployed`. Advance one month and query `citizen[employed].Wealth` and `citizen[unemployed].Wealth`. Assert `Wealth_employed` increased by `£40k/12` (monthly) and `Wealth_unemployed` is unchanged. (b) Change the employed citizen's state to `unemployed`, advance another month, and assert `Wealth` does not increase. (c) Change back to `employed`, advance a month, and assert `Wealth` increases again by `£40k/12`. (d) A conservation test: sum all employed citizens' wages, tick one month, and assert the treasury-debit equals the sum (no wage is created/destroyed, micro-pound exact).

**Lazy implementation this rejects:** A build that credits `Wealth` to all citizens regardless of state (so unemployed citizens get mystery income) fails (a)/(b) — state-gating is load-bearing.

---

### AC-7 (Wage crediting flows through finance.PostWages, conserved to the micro-pound)

**Requirement:** All wage crediting to citizen `Wealth` goes through `engine.finance`'s `PostWages(citizen, wage)` API (registered via FEAT-225 A3's `engine.finance → engine.citizens` edge). The wage is `int64 finance.Money` (micro-pounds). Overflow is impossible (use `num.SatAdd` / `satAddMoney`). The total treasury debit equals the sum of citizen wages, exact.

**Check:** (a) A test seeds a city with N employed citizens, each with a known occupation wage (e.g., 10 nurses at £40k/yr, 5 garbage collectors at £25k/yr). Capture `treasury_balance_before`. Advance one month. Compute `expected_debit = Σ(wages / 12)`. Assert `treasury_balance_after == treasury_balance_before - expected_debit` (to the micro-pound). (b) Reuse the existing `engine.finance` conservation test shape (headless test running 120 months) and extend it to include the jobs-model wage path; assert `TestMoneyConservationOver120Months` still passes. (c) A test that would cause integer overflow (e.g., N = 1,000,000 citizens each earning £1M/yr, total > MAX_INT64) is prevented by the salary+overflow-check flow; the test asserts a `registry-sourced error` is returned, not a wrapped int64.

**Lazy implementation this rejects:** A build that credits wages in float (so rounding variance accumulates over 120 months) or that calls `PostWages` with a float-derived wage fails (a) due to drift in the conservation assertion — use integer arithmetic and `num.Sat*` operations.

---

### AC-8 (Unfilled-jobs signal is computed per family and visible on F2)

**Requirement:** At the end of each tick, compute unfilled-jobs counts by family: for each family, count the jobs posted by workplaces but not yet assigned to any citizen. The signal is queryable via an API (e.g., `UnfilledJobsByFamily() map[string]int`). The F2 macro dashboard displays unfilled jobs by family (mock: "Healthcare: 18 unfilled", "Sanitation: 7 unfilled"). A **critical threshold** (placeholder, e.g., "Sanitation unfilled > 5 → warning") is data-defined.

**Check:** (a) A test: a hospital posts 10 nurse jobs; 7 are filled (7 citizens assigned). Query `UnfilledJobsByFamily()` and assert `healthcare: 3`. (b) Post 5 garbage jobs; none are filled. Assert `sanitation: 5`. (c) Assign a citizen to a sanitation job; assert `sanitation: 4`. (d) On F2, screenshot the macro dashboard and visually confirm the unfilled-jobs line items appear (or, a headless render test asserts the dashboard JSON carries `unfilledJobs: { healthcare: 3, sanitation: 4, ... }`). (e) Check `data/occupations.json` for a `unfilled_critical_threshold` field (or per-family threshold); if sanitation unfilled exceeds it, a UI warning badge appears on F2 (or the unfilled item is highlighted in red).

**Lazy implementation this rejects:** A build that computes unfilled jobs globally (total across all families) instead of per-family fails (a)/(c) — the brief requires family-level granularity so the player can see which sector is understaffed.

---

### AC-9 (Job assignment respects commute distance; nearby workplaces are preferred)

**Requirement:** When a citizen seeks a job, the algorithm first enumerates **nearby workplaces** (within a commute-distance placeholder, e.g., 5 tiles or < 1000m Euclidean distance). Only jobs at nearby workplaces are considered. A citizen **remains unemployed** if no nearby workplaces have available jobs (even if a job exists 20 tiles away). The commute-distance threshold is **data-defined** (not hardcoded in Go).

**Check:** (a) `data/occupations.json` or a sim config carries a `citizen_job_search_radius` field (meters or tile count, placeholder value). (b) A unit test: place a hospital 4 tiles away (within range) and another 10 tiles away (outside range). Both post nurse jobs. An unemployed citizen at the midpoint is assigned to the 4-tile hospital (nearby) only. Assert citizen is employed, not unemployed waiting for the far job. (c) Delete the nearby hospital's jobs; assert the citizen remains unemployed (does not reach the far hospital). (d) Increase `citizen_job_search_radius` in the data, reload, and re-run; now the far hospital is in range, and the citizen is employed.

**Lazy implementation this rejects:** A build that searches all workplaces globally (ignoring distance) fails (b)/(c) — locality is load-bearing (so a player-built hospital in a new district must hire locally, not pull from the other side of the map).

---

### AC-10 (Config page: editable S_base, global YoY inflation %, per-job override; changes apply at month-end)

**Requirement:** An F8 (or F9) Config screen carries three sections:
  1. **Economy:** Sliders for `S_base` (global baseline salary, e.g., £25,000 to £60,000) and `inflation_rate_annual` (e.g., -5% to +10%, placeholder).
  2. **Occupation Overrides:** A scrollable list of all 16 (or N) occupations, each with an optional multiplier field (default: empty = "use registry R_occ"). E.g., player types "1.2" next to garbage_collector to boost it 20%.
  3. **Apply / Cancel** buttons. Confirmation: "Apply changes starting next month?"

**Changes are applied at the **next month boundary** (not immediately). Inflation is applied **once per year** (month % 12 == 0). Overrides are applied at the start of the following month.**

**Check:** (a) Open Config, set S_base to £40,000 (was £30,000). Click Apply. Advance 29 days (not a month). Assert wages are still computed at £30,000 (old value). Advance to the next month tick; assert wages now use £40,000. (b) Set inflation_rate_annual to 5%. Advance 11 months; S_base is unchanged. On month 12 (year boundary), S_base increases by 5%. (c) Set garbage_collector override to 1.5×. Apply. Assert on the next month tick, garbage_collector wage = (new S_base) × 1.5 × R_occ_base × (1 + α G), where R_occ_base is ignored and 1.5 is used instead. (d) Clear the garbage_collector override; assert it reverts to the registry R_occ on the next month tick.

**Lazy implementation this rejects:** A build that applies config changes immediately (same tick) fails (a) — the brief requires month-boundary application so citizens don't see mid-month wage shifts.

---

### AC-11 (Global YoY inflation is distinct from Generosity; Generosity is FEAT-225 D-9 scope)

**Requirement:** This feature introduces **inflation_rate_annual** as a **simulation-level control** the player adjusts to model economic cycles. This is **not** the same as the **Generosity slider** (FEAT-225 D-9). Generosity is a feedback signal from citizen sentiment (destitute citizens generate negative Generosity, per FEAT-225 D-9 language); inflation_rate_annual is a **player lever** for adjusting the baseline cost of living. Both are independent knobs.

**Check:** (a) The config page has two separate controls: "Baseline Salary (S_base)" and "Annual Inflation %", never conflated. (b) A test: set Generosity to -5 (citizens are destitute) and inflation to +5% (player inflates wages to compensate). Advance one year. Assert S_base increased by 5% (inflation applied), and Generosity is still -5 (inflation does not auto-correct sentiment). (c) Confirm in code comments: "Generosity (FEAT-225) is a derived citizen-sentiment signal; inflation (this feature) is a player lever."

**Lazy implementation this rejects:** A build that ties inflation to Generosity (e.g., "higher Generosity → auto inflation") or that merges the two sliders fails (a)/(c) — they are separate concerns.

---

### AC-12 (Job assignment state is seeded and reproducible; state-derived, not timeline-dependent)

**Requirement:** The job-assignment seed is `hash(citizen_id || current_tick)`. The assignment is **state-derived** (same city state + same tick = same assignment), never dependent on `time.Now` or elapsed wall-clock time. A replay of the same city snapshot produces byte-identical job assignments on each tick.

**Check:** (a) A determinism test: checkpoint a city state (citizens, workplaces, jobs, tick count). Run the assignment algorithm for tick T, recording all assignments. Reload the checkpoint and run assignment for tick T again. Assert the outputs are byte-identical. (b) Replay the checkpoint for ticks T, T+1, T+2, … , T+11 (12 ticks). Assert all 12 ticks are deterministic (every replay run produces the same job assignments). (c) A test that advances wall-clock time but resets the in-game tick to 0 produces identical assignments to a normal run starting at tick 0 (no time.Now dependency).

**Lazy implementation this rejects:** An assignment that calls `time.Now` or uses `math/rand` without seeding (or seeds `rand.Seed(time.Now().UnixNano())`) fails (a)/(b) — every consecutive run would produce different assignments.

---

### AC-13 (Wages are conserved and exact through the ledger; no wage is created or destroyed)

**Requirement:** The total citizen wealth increase from employment wages in a month equals the total municipal treasury debit from paying wages, exact to the micro-pound. No wage is created, destroyed, or rounded away.

**Check:** (a) A conservation test runs a city for 12 months with a constant roster of N employed citizens at known occupations. After each month, compute `Σ(citizen_wage / 12)` (the wage bill for that month). Capture `treasury_delta = treasury_balance_before - treasury_balance_after`. Assert `treasury_delta == Σ(citizen_wage / 12)` exactly (to the micro-pound). (b) Extend the existing `TestMoneyConservationOver120Months` test in `engine/finance/conservation_test.go` to include the jobs-model wage path; verify it still passes. (c) A test that would overflow (total wages > MAX_INT64) is rejected with a registry error, not silently wrapped.

**Lazy implementation this rejects:** A build that rounds wages (e.g., to the nearest pound) at intermediate steps and then sums the rounded values fails (a) — accumulated rounding error violates the exact-equality assertion. Use `finance.Money` (int64 micro-pounds) throughout.

---

## Placeholder Constants Table

All player-felt numbers below are **data placeholders pending Aaron's balance pass (GR#15)**. They are loaded from `data/occupations.json`, `data/balance/jobs_model.json`, or a sim-config file, never hardcoded in Go.

| Constant | Placeholder Value | Source | Notes |
|----------|-------------------|--------|-------|
| `S_base` (global baseline salary) | £30,000/year | `data/balance/jobs_model.json` | GR#15 balance-pass placeholder |
| `α` (Generosity scaling factor) | 0.1 (10% swing per point) | `data/balance/jobs_model.json` | per FEAT-225 pay/ brief; GR#15 |
| `inflation_rate_annual` (player-adjustable) | 0% (default) | Config page slider, -5% to +10% | GR#15 balance placeholder |
| `citizen_job_search_radius` (commute distance) | 5 tiles or 1000m | `data/occupations.json` / sim config | Open Q: exact distance metric TBD |
| `essential_fill_threshold` (% of capacity to fill before unlocking top-wage chase) | 80% | `data/occupations.json` or per-family | GR#15; per-family thresholds allowed |
| `unfilled_critical_warning_threshold` (sanitation, transport, civic) | Family-specific, e.g., sanitation > 5 | `data/occupations.json` | UI warning badge trigger |
| Wage multipliers `R_occ` per occupation | nurse=1.0, doctor=2.0, garbage_collector=0.8, etc. | `data/occupations.json` (FEAT-225 A1) | 16 base roles; ONS-grounded |
| Month-end configuration apply timing | Exactly at month tick (month % 12 == 0 for inflation) | Config page logic | GR#21 determinism; no mid-month apply |

---

## Out of scope

- **Engine.services:** Unemployment benefits, welfare payments, savings-drawdown for unemployed citizens. Those belong to `engine.services` (MOD-033), not this feature.
- **Firm-side labour demand:** This feature specifies job posting by buildings (e.g., Hospital posts 10 nurse jobs on completion). **Firm-side dynamics** (a factory reducing jobs if demand falls) are FEAT-1972079927 or belong to `engine.firms`, not this feature.
- **Migration desirability by jobs/wages:** Whether a high-wage city attracts migrants faster is `engine.attract` scope, not this feature.
- **Per-district wage adjusters:** Regional pay scales (London costs more than Folkestone) are out of scope; all citizens earn the same occupation wage regardless of district (simplification for Baseline One).
- **Commute cost on citizen welfare:** Whether a long commute reduces citizen happiness is `engine.citizens` scope; this feature only uses distance to filter job eligibility.
- **Skill-bracket fidelity beyond EmploymentState:** The closed `EmploymentState` enum (0..5) is the only employment-state signal. Skill brackets (engineer vs nurse distinction) flow through wage multipliers, not a separate state variable.

---

## Open Questions (Architect / Aaron ruling required)

### 1. Reconciliation with FEAT-225: Is this feature its realisation or an extension?

The BOW item says "this may BE [FEAT-225's] realisation or extend it — Architect to reconcile." FEAT-225 A1/A2/A3 (occupation registry, building-completion job posting, per-citizen wage crediting) are implied to be done. **This feature assumes that foundation is in place and adds the jobs-model layer on top (desirability, essential-jobs fill, config page, unfilled-jobs signal, determinism).**

If FEAT-225 has stalled or forked in an unexpected direction, this AC scope needs re-scoping.

### 2. Essential-jobs enforcement: Hard constraint or soft incentive?

**AC-5 specifies a hard constraint:** essential-jobs families are filled *before* citizens chase top-wage jobs. If sanitation is 0% filled, every job-seeking citizen in range is assigned to sanitation (even at £25k/yr when a £40k nurse job is available).

**Open:** Is this the right enforcement? Or should essential-jobs be a **soft signal** (unfilled sanitation jobs raise decay/refuse faster, which indirectly makes the city worse, causing the player to respond) rather than a hard assignment override?

**Aaron ruling required:** Hard constraint (as in AC-5) or soft signal?

### 3. Workplace capacity source and commute-distance metric

**AC-9 specifies:** Jobs are posted by workplaces within a **commute-distance threshold** (data-defined, placeholder 5 tiles or 1000m).

**Open:** (a) How many jobs does a building post? Is it a fixed cap (e.g., hospital posts 20 jobs always) or dynamic (e.g., based on building level/upgrades)? (b) Is commute distance Euclidean tiles, Manhattan distance, or road-network distance (pathing)? (c) Who computes and stores the capacity? Is it `engine.staffing` (likely, per FEAT-225 A2) or this feature?

**Aaron ruling required:** Capacity source (game-data, building-level formula, staffing-module input).

### 4. Commute/distance model scope for Baseline One

The feature specifies job-seeking is geographically local (nearby workplaces only). **This is a simplification for Baseline One** — it prevents infinite-range job-matching and keeps the labour market local to neighbourhood districts.

**Open:** For Baseline One, is "local" enough, or does the feature need a **traffic/commute-time model** (e.g., citizen only accepts job if commute < 30 minutes via road network)? Or is "within 5 tiles Euclidean" sufficient for the watchable MVP?

**Aaron ruling required:** Commute model fidelity for Baseline One (fixed radius vs. traffic-aware vs. road-network pathing).

### 5. Generosity slider vs. inflation vs. wage adjustment

FEAT-225 D-9 flags a **Generosity control ambiguity:** "Generosity is BOTH a player slider and a simulation output" — a control that writes to itself has no defined arbitration.

**This feature introduces inflation_rate_annual** as a separate player lever for year-over-year salary adjustments. **Open:** Does the Generosity slider stay as a **simulation-only output** (citizens generate it based on sentiment, never player-controlled), and inflation_rate_annual becomes the **player wage lever**? Or does Generosity remain a player slider too?

**Aaron ruling required:** Clarify Generosity semantics (player input, sim output, or both?).

---

## Increments suggestion

Implement in layers (following the dev-team cascade):

1. **Inc1 — Data-driven job families & wage spread:**
   - Load `data/occupations.json` (extended from FEAT-225 A1) with family affiliation + R_occ multipliers.
   - Implement wage calculation: `S_final = S_base × R_occ × (1 + α G)` as integer `finance.Money`.
   - Unit tests for wage math (AC-2).
   - Determinism test: same wage calculation, repeated runs → identical output (AC-4, AC-12 seed logic).

2. **Inc2 — Desirability + job assignment (locality-aware, highest-wage preference):**
   - Implement `AssignCitizen(citizen) → (occupied_job, err)` that searches nearby workplaces (commute radius) and assigns highest-wage available job.
   - No essential-jobs constraint yet (all families treated equally).
   - Unit tests: citizen picks highest-wage job, respects commute radius (AC-3, AC-4, AC-9).
   - Determinism test: replay same city twice → identical assignments (AC-4, AC-12).

3. **Inc3 — Essential-jobs fill constraint:**
   - Add `essential_fill: bool` flag to family definitions in `data/occupations.json`.
   - Modify `AssignCitizen` to check essential-family fill % before assigning top-wage jobs (AC-5).
   - Unit tests: essential-jobs are filled first, even at lower wage (AC-5).

4. **Inc4 — Unfilled-jobs signal + F2 display:**
   - Compute unfilled-jobs per family at end-of-tick (AC-8).
   - Expose `UnfilledJobsByFamily() map[string]int` API.
   - Wire F2 macro dashboard to display the signal (AC-8).
   - Critical-threshold warning badge (AC-8).

5. **Inc5 — Config page (S_base, inflation, per-job override):**
   - Implement F8 Config screen (mock UI or headless JSON API).
   - Sliders: S_base, inflation_rate_annual.
   - Scrollable list of occupations with override multiplier inputs (AC-10).
   - Month-end apply logic (AC-10).
   - Unit tests: config changes apply at month boundary, not immediately (AC-10).

6. **Inc6 — Conservation + ledger integration:**
   - Wire `finance.PostWages(citizen, occupation_wage)` on month tick for employed citizens (AC-6, AC-7).
   - Extend `TestMoneyConservationOver120Months` to include wage path (AC-13).
   - Overflow guards via `num.SatAdd` / `satAddMoney` (AC-7).

Each increment has a corresponding test suite and must pass GR#23 (Destructive verdict) before merge.

---

## Summary (12 lines)

FEAT-1972079929 builds a detailed employment + jobs model on top of FEAT-225's occupation registry and per-citizen wage crediting. Citizens are assigned to jobs at nearby workplaces based on wage preference, with essential-jobs (sanitation, transport) guaranteed fill before unlocking top-wage chasing. Wage spread is ONS-grounded (nurse=1.0×, doctor=2.0×, garbage collector=0.8× a baseline salary). The player configures global baseline salary, YoY inflation, and per-job overrides on an F8 Config page (changes apply at month-end). Unfilled-jobs signal by family appears on F2 (leading indicator for labour stress). All assignment is deterministic (state-derived, seeded from citizen ID + tick), and wage conservation is exact through the ledger (no money created/destroyed). Open questions: FEAT-225 reconciliation (realisation or extension?), essential-jobs hard constraint vs soft signal, workplace capacity source, commute-distance metric, Generosity slider semantics.
