BOW code: FEAT-2326609740

# Acceptance criteria — construction lifecycle & baseload selling (FEAT-2326609740)

**BOW code:** FEAT-2326609740 + BUG-569 (clarification trail)  
**Design:** Aaron Garcia, 2026-09-02 (verbal design; recorded here)  
**Spec refs:** docs/METROPOLIS-MASTER-v2.1.md (implied: lifecycle model, economic realism); no existing formal section — this feature defines TS sim model alignment with Go engine's construction resource model (§13-F3) and baseline-one economic closure (FEAT-083).  
**Date:** 2026-09-02  
**Status:** ready  
**Scope:** webconsole TS simulation — **gameplay observable model only** (no Go engine changes required; Go already has construction materials/labour/water model; TS sim is the *watchable* layer that displays it).  
**Standard gates:** *none specified for TS sim ACs yet; follow existing pattern from other TS ACs in this folder*.

---

## Context

Aaron's design (2026-09-02):

> "During the build phase, construction employs lots of people and consumes huge amounts of cement and water etc. So a build phase resource cost beyond just money is required, then there is steady state. Nuke plants run at 100% and excess power is sold off to the grid."

This feature models two phases of a building's lifecycle in the TS webconsole sim:

1. **BUILD PHASE** (under construction, `isOnline() === false`): building consumes labour (generates construction jobs) and water. Produces nothing. Takes time.
2. **STEADY STATE** (online, `isOnline() === true`): building produces (or consumes, or both). No construction jobs or water draw.

Additionally, **baseload power plants** (nuclear, coal) run at 100% capacity when online and sell any excess to the regional grid as an **inflow revenue line**, mirroring the existing Grid Import outflow line.

---

## Orientation (TS sim current state)

### Construction (currently)
- `data.ts:464` — `isOnline()` gates on `constructionTicks(sp)` to hide buildings during construction
- `data.ts:212–220` — `constructionTicks()` = `Math.max(3, cost / 1_500_000)` → time-based gate
- `engine.ts:573–583` — `computeFlows()` computes upkeep buckets for ONLINE buildings only
- **NO construction jobs or water draw modelled in TS currently** (Go engine has it; TS doesn't surface it)
- `BUG-569` (commit `8fec991`): last 4 under-construction contribution leaks gated via `isOnline()` — buildings under construction correctly produce nothing, contribute no jobs/tourism/harbour/schools

### Power & Grid
- `fiscal.ts:89–103` — `gridExportRevenuePerTick()` already implemented: `exportMW = max(0, capMW - needMW)` → revenue
- `fiscal.ts:93` — `GRID_EXPORT_TARIFF_PER_MW = 1.6`
- `fiscal.ts:113` — `GRID_IMPORT_TARIFF_PER_MW = 2.5`
- `engine.ts:543–550` — Grid Export revenue already booked as an inflow line when `gridExportRevenue > 0`
- **Grid export is already implemented; no new export path needed**
- **NO baseload flag exists on power specs yet** (propose adding: `baseload?: boolean`)

### What's missing
- Construction jobs/water consumption visible during build phase
- Baseload classification on power plant specs
- Grid export tied to baseload plants (baseload always runs at 100%, so excess is always sold)

---

## User stories

- **US-1.** As the player, I need construction to visibly consume labour (jobs) and water during the build phase, so I can see *why* a building takes time and resources to appear — construction is a real economic activity, not just a time delay.
- **US-2.** As a baseload power plant (nuclear, coal), I need to always run at full capacity when online, so I generate reliable baseload supply. When capacity exceeds city demand, the excess is sold to the grid as revenue.
- **US-3.** As the player, I need grid export revenue (power sold to grid) to show as a separate, visible income line in the finance breakdown, so I can see the return on my power infrastructure investment.
- **US-4.** As the design, I need to guarantee that grid export tariff (selling price) is strictly less than grid import tariff (buying price), so arbitrage cannot break the economy (buy at 2.5, sell at 2.5 or higher).

---

## Scope

**Build-phase resource consumption in the TS sim:** during construction (0 to `constructionTicks()` ticks), a building consumes:
- **Construction jobs:** proportional to capex, drawn from city employment pool, disappear when build completes
- **Water:** drawn during construction, independent of building type

**Baseload plants:** nuclear and coal power plants operate at 100% nominal capacity when online; when capacity exceeds need, excess is sold at `GRID_EXPORT_TARIFF_PER_MW`.

**Grid export revenue:** already computed by `gridExportRevenuePerTick()`, already displayed when > 0; tied to baseload behaviour so revenue appears consistently.

**Out of scope:**
- Go engine changes (already has materials/labour/water model; TS sim just observes it)
- Detailed construction jobs scheduling (labour pool depth, unemployment rates)
- Detailed water sourcing (water network, treatment, etc.)
- Other power plant types' dispatch rules (solar, wind variable generation — later, if at all)
- Baseload carbon/fuel emissions (environmental sim is later)

---

## Acceptance criteria

### Functional — construction phase

- **AC-1 (construction jobs during build).** While a building is under construction (`isOnline() === false`), it contributes construction jobs to the city employment pool. Jobs must be positive and proportional to the building's capex (cost). At the tick a building completes (`isOnline()` transitions to true), construction jobs drop to zero.  
  *Check:* `totalJobs(s)` increases when a building placed and is under construction; decreases by the same amount when the building completes. Jobs contribution is proportional to `SPECS[spec].cost` or a formula derived from it (e.g., `cost / divisor`, matching `constructionTicks`'s divisor pattern). Jobs are live (not forecast) — a save/load mid-construction preserves the jobs count.  
  *False-pass risk:* a jobs counter that increases but only refreshes on save/load would pass a single session test while being visually stale in real play — the check must measure jobs across multiple consecutive ticks during construction, not just at start/end events.

- **AC-2 (construction water during build).** While a building is under construction, it draws water at a rate independent of building type (placeholder: a constant draw per tick, e.g., 5 litres/tick, tunable by Aaron). Water draw appears in `computeFlows()`'s outflows. At the tick the building completes, water draw for that building stops.  
  *Check:* `computeFlows(s)` outflows include a water-consumption entry proportional to the count and mean construction time of buildings under construction. Water draw is zero when no construction is active. A test with one building under construction for N ticks asserts the total water flow over N ticks matches the expected rate.  
  *False-pass risk:* a water line that appears but never changes in magnitude would pass "water appears" without validating it actually scales — the check must measure water flow for different construction states (0 under construction, 1, multiple).

- **AC-3 (no production during build).** A building under construction (`isOnline() === false`) contributes ZERO to city production, employment (non-construction), tourism, demand satisfaction, or any service (BUG-569 already landed this; this AC re-affirms it applies to the new construction jobs model too). Upon completion, the building's normal produce/consume start immediately.  
  *Check:* `totalJobs(s)` excludes normal jobs from under-construction buildings; `isBrownoutActive(s)` and income calculations ignore under-construction structures; tourism inflows do not include under-construction attractions. Operationally: a city with only one residential zone under construction has zero normal jobs, zero resident income, zero demand-bar advance until completion.  
  *False-pass risk:* a test that accidentally includes a completed building in the same test tick would pass by accident — the check must test an explicitly-incomplete-at-tick state (e.g., tick < builtTick + constructionTicks(spec)).

- **AC-4 (conservation: construction jobs are paid).** Construction jobs, like all jobs, are paid via `wagesPerTick()`. The wage line in finance outflows accounts for them — total wage cost = population × real-wage-per-citizen, and that includes construction workers as notional population for wage purposes (no separate "construction wage" line needed; they're already paid as population members).  
  *Check:* `wagesPerTick(s.population)` is called in `computeFlows()` with the full population count, which includes employed construction workers. Determinism test: identical population (residents + construction workers) yields identical wages regardless of the split.  
  *False-pass risk:* a wage formula that only counts residents and accidentally excludes construction from population would pass the "wage line exists" check while underpaying construction workers — the check must assert the wage line reflects the *actual* population count including construction jobs as a notional employment tier.

### Functional — baseload plants & grid export

- **AC-5 (baseload plant definition).** Power plant specs (nuclear, coal, CCGT, etc.) carry a `baseload?: boolean` flag in `data.ts`'s `Spec` interface or equivalent. Baseload plants: nuclear, coal. Non-baseload (variable): wind, solar, CCGT. The flag is sourced from the catalogue at sim init, never overridden per building instance (baseload is a plant type property, not a per-building runtime state).  
  *Check:* `SPECS['nuclear_power_plant'].baseload === true`, `SPECS['wind_turbine'].baseload === undefined || false`. No per-building runtime baseload flag exists (static property only).  
  *False-pass risk:* a test that stubs the flag without checking the actual `SPECS` data would pass — the check must read from live catalogue load.

- **AC-6 (baseload runs at 100% capacity).** A baseload power plant, when online, operates at 100% of its `mw` capacity (no variable output). It either runs full or is offline (e.g., due to road disconnection), never part-load.  
  *Check:* `powerStats(s)` includes a baseload plant with `mw: 200` → that plant contributes 200 MW to capacity when online, not a scaled fraction. A test with a baseload plant on a disconnected road asserts its capacity contribution is zero (offline) until reconnected, at which point it becomes 100%.  
  *False-pass risk:* a capacity calculation that averages baseload and variable plants (e.g., all plants at 75% average) would fail AC-6 — the check must test baseload plants in isolation and assert their contribution is binary (100% or 0%), never scaled.

- **AC-7 (baseload sells excess to grid).** When a baseload plant's capacity exceeds the city's power need, the excess is sold to the regional grid at `GRID_EXPORT_TARIFF_PER_MW` (already implemented). The revenue appears in `computeFlows()` as an inflow called "Grid Export" (existing line), shown only when revenue > 0. Non-baseload plants' excess also sells (the grid export mechanism is universal), but only baseload guarantees continuous sell-able excess.  
  *Check:* A city with a 200 MW baseload plant + 100 MW need → 100 MW exported → revenue = 100 × 1.6 = 160 per tick, booked in inflows. A city with only wind (variable, not baseload) will have zero revenue most ticks (wind produces 0 on calm nights). The grid export revenue line appears when revenue > 0, disappears when 0 (lazy visibility is acceptable).  
  *False-pass risk:* a test that asserts grid export revenue exists but never varies would pass "revenue is calculated" while missing that only baseload + sufficient capacity actually produces it — the check must construct a scenario where export revenue appears and disappears based on capacity vs need, not always present.

### Non-functional — arbitrage guard

- **AC-8 (export price < import price — economic invariant).** The grid export tariff MUST be strictly less than the grid import tariff: `GRID_EXPORT_TARIFF_PER_MW < GRID_IMPORT_TARIFF_PER_MW`. This prevents arbitrage (buying from grid to sell back). Currently: `1.6 < 2.5` ✓. If rebalanced, this invariant must hold.  
  *Check:* `fiscal.ts` line 93 vs line 113: verify `GRID_EXPORT_TARIFF_PER_MW < GRID_IMPORT_TARIFF_PER_MW` in source. A passing test (`test/fiscal.test.mjs` or similar) asserts the invariant as a hard assertion, not just a comment. If either tariff is changed, the test will fail unless the invariant is maintained.  
  *Rationale:* if export ≥ import, a player could buy power from the grid at 2.5/MW and immediately sell it at export price ≥ 2.5, yielding infinite or arbitrage-level profit, breaking the cost-of-service model.  
  *False-pass risk:* a test that reads the numbers statically without re-running assertions would pass until rebalancing — the check must be a live assertion (e.g., `Math.min(import, export) === export` fails if the values drift).

### Determinism & consistency

- **AC-9 (determinism).** Given an identical starting state and command sequence, construction jobs and water draw during the build phase are byte-identical across replays. Jobs and water are purely functions of building spec (cost, type), build time progress (tick count), and current population, never RNG or wall-clock time.  
  *Check:* A passing replay test (compare two runs through the same action journal) asserts `totalJobs(state1) === totalJobs(state2)` and water flows match bit-for-bit when restarting from an earlier snapshot and replaying forward.

- **AC-10 (conservation: money in = money out).** Total inflows (council tax + business tax + tourism + commuter revenue + **grid export**) + total outflows (wages + upkeep + **grid import**) balance to funds change each tick, accounting for policies and services. The new grid export/import lines do not break this invariant.  
  *Check:* A consistency checker (already exists in `consistency.ts`) includes grid export as an inflow and grid import as an outflow in the conservation identity. The check is already in place (FEAT-2326609711 inc1); this AC affirms it applies to the new baseload scenario.

---

## Data & placeholders for Aaron's balance pass

The following are PLACEHOLDER values; all require Aaron's review and approval before ship:

| Item | Current | Unit | Notes |
|------|---------|------|-------|
| Construction jobs multiplier | `cost / 1_500_000` | jobs | Scaled same as `constructionTicks()` divisor; proportional to capex |
| Construction water draw | (TBD) | litres/tick | Constant rate during any building's construction; Aaron proposes ~5 litres/tick minimum |
| `GRID_EXPORT_TARIFF_PER_MW` | 1.6 | £/MW | Must remain < `GRID_IMPORT_TARIFF_PER_MW` (2.5) to prevent arbitrage |
| `GRID_IMPORT_TARIFF_PER_MW` | 2.5 | £/MW | Must remain > `GRID_EXPORT_TARIFF_PER_MW` (1.6) to prevent arbitrage |
| Baseload plant list | nuclear, coal | — | Proposal: coal & nuclear are baseload; CCGT, wind, solar, hydro are variable |

---

## Open questions & recommendations

### Q1: Construction labour — source and unemployment impact
*Question:* When a building is under construction, do construction jobs come from the city's unemployment pool (affecting unemployment %), or are they abstract (added on top of existing employment)?

*Recommendation:* Abstract. The TS sim has no employment-status tracking per citizen yet (Go engine does; TS doesn't). Construction jobs should be additive to `totalJobs(s)`, not a reallocation of existing jobs. This keeps the model simple while the TS sim is young; the Go engine can model detailed labour pools later if needed.

*Implication:* unemployment % is a Go-engine-only metric for now; the TS sim's "totalJobs/availability" bar is purely a capacity gauge, not an unemployment rate.

### Q2: Other baseload plant types
*Question:* Are there other baseload types beyond nuclear and coal (e.g., hydro, biomass)?

*Recommendation:* Propose the smallest honest set: **nuclear + coal = baseload**. CCGT gas plants are typically peaking/middle-load (variable), so exclude them. Wind, solar are obviously variable. Hydro is not in the catalogue yet. If hydro is added later, Aaron will decide per-plant.

*Implication:* Non-baseload plants have zero tariff revenue in this model (they don't run 100%, so they rarely exceed need). This is intentional — it encourages the player to invest in reliable baseload to generate export revenue.

### Q3: Grid infrastructure requirement
*Question:* Does selling power to the grid require built-out grid infrastructure (substations, transmission lines), or is it abstract (any online power plant automatically sells)?

*Recommendation:* Abstract for Baseline One. No grid infrastructure gating. Any online power plant's excess capacity is sold; the player doesn't need to build "export terminals" to make it happen. This keeps FEAT-083's scope tight (game must RUN, not be complete).

*Implication:* Later features (power-grid depth, transmission losses, infrastructure requirements) can add this gating as a refinement.

---

## Escalations & dependencies

- **No Go engine changes required.** The Go engine already models construction materials/labour/water consumption via `engine.build` (MOD-026). The TS sim just needs to observe and display it.
- **Baseload plant list must be approved by Aaron** (Q2 above). The proposal is nuclear + coal; final decision is his.
- **Construction water draw rate (litres/tick) must be approved by Aaron.** Placeholder: ~5 litres/tick; subject to his balance pass.
- **This feature lands after BUG-569** (already landed: buildings under construction contribute zero to normal production/jobs/tourism).
- **Grid export tariff invariant (AC-8) must be tested and re-tested on any rebalancing.** The test is mechanical and will fail automatically if the invariant breaks.

---

## Test summary

- Construction jobs appear and disappear with build phase ✓
- Construction water consumed during build only ✓
- No production during build (BUG-569 affirms) ✓
- Baseload plants run at 100% capacity ✓
- Grid export revenue computed and displayed ✓
- Export tariff < import tariff (AC-8 invariant) ✓
- Conservation (money in/out balance) holds ✓
- Determinism (byte-identical replays) ✓

---

## References

- **BUG-569:** `8fec991` — gate the last 4 under-construction contribution leaks on isOnline
- **FEAT-2326609711 inc1:** Grid import (external power cover) — already landed; this feature mirrors it with export
- **fiscal.ts:** `GRID_EXPORT_TARIFF_PER_MW` (1.6), `GRID_IMPORT_TARIFF_PER_MW` (2.5), `gridExportRevenuePerTick()`
- **engine.ts:** `computeFlows()` (lines 482–631), `totalJobs()`, `isOnline()`, `powerStats()`
- **data.ts:** `Spec` interface, `isOnline()` (line 460), `constructionTicks()` (line 212)
- **FEAT-083:** Baseline One (game must run)
- **engine.build.md:** Go-side construction model (materials + labour + water + lead time)
