# FEAT-2326609721: Wire Commercial/Industrial Sector into Consumption→Demand Feedback Loop

**Feature Code:** FEAT-2326609721  
**Audit Finding:** S3 FEAT-083 spine audit (2026-09-02)  
**Title:** Commercial and Industrial Revenue Scales with Customer Base; Demand Feeds Back to Attractiveness  
**Status:** Specification (WebConsole-internal mechanic, no new cross-module edges per GR#25)

---

## Intent

The webconsole city-sim's consumption→demand feedback loop today closes **only for residential** zones. Commercial revenue and industrial revenue are per-building constants (independent of population), and the computed `demand.commercial` / `demand.industrial` metrics feed back **only to debug output** (debugjson.ts:533), never affecting any tick quantity.

This spec wires commercial and industrial sectors into the feedback loop by:

1. **Scaling commercial/industrial revenue with the customer base** (total population able to use those services), up to a catchment ceiling — a thriving city with 100k people generates more commercial tax than an empty city with the same zone count.
2. **Feeding demand back into attractiveness calculations** — unmet commercial demand raises the attractiveness/desirability of building more commercial zones (or dampens it if saturated), the same way `demand.residential` already drives residential attractiveness.
3. **Keeping the mechanic deterministic and conservation-safe** — all revenue is booked as labelled inflows through `lastFlows` (following the existing council-tax and business-tax pattern), with no money created from nothing.

---

## Current State

### Commercial Revenue (Population-Independent)

**File:** `webconsole/src/sim/fiscal.ts:70`

```typescript
export function businessTaxPerTick(commercialZones: number, taxRate: number): number {
  return Math.round(commercialZones * taxRate * FISCAL_COEFFICIENTS.businessTaxFraction);
}
```

**Finding:** Revenue is `zones × rate × fraction`. It does **not** depend on `population` — a city at 10 people generates the same per-zone revenue as a city at 100k people.

### Demand Computation (Wired Nowhere)

**File:** `webconsole/src/sim/engine.ts:267-285`

```typescript
export function demandOf(s: SimState): ZoneDemand {
  const c = countByKind(s.buildings);
  const t = s.taxRates;
  const avgTax = (t.residential + t.commercial + t.industrial) / 3;
  const jobs = totalJobs(s);
  const workers = s.population * 0.55;
  const base = Math.max(Math.max(jobs, workers), 40);
  const res = ((jobs - workers) / base) * 140 - (avgTax - 10) * 4;
  const popFactor = Math.min(1, s.population / 40);
  const shopBase = Math.max(s.population * 0.22, 12);
  const com = popFactor * (((shopBase - c.commercial * 10) / shopBase) * 130 - (t.commercial - 11) * 3);
  const indBase = Math.max(s.population * 0.18, 9);
  const ind = popFactor * (((indBase - c.industrial * 7) / indBase) * 130 - (t.industrial - 13) * 3);
  return {
    residential: Math.round(clampN(res, -100, 100)),
    commercial: Math.round(clampN(com, -100, 100)),
    industrial: Math.round(clampN(ind, -100, 100)),
  };
}
```

**Finding:** `demand.commercial` and `demand.industrial` are computed but **never read during a tick** — they appear **only** in:
- `debugjson.ts:533` → `zoneDemand = demandOf(s)` (UI/debug readout only)
- Never in `engine.ts:advanceTick()` flow calculations or attractiveness logic

### Residential Feedback (The Working Loop)

**File:** `webconsole/src/sim/engine.ts:1139-1142`

```typescript
const attractiveness =
  (1.4 - avgTax / 15) * (s.policies.transitSubsidy ? 1.25 : 1) *
  Math.max(0.3, 0.55 + demand.residential / 200) *
  (1 + 0.15 * Math.min(stationWeight, 6));
```

**Finding:** `demand.residential` **is** used here — it drives the third multiplicative term, so `attractiveness` scales up/down with residential demand. This drives `moveIns` (engine.ts:1180), closing the loop: more unmet residential demand → higher attractiveness → more move-ins.

**Commercial and industrial demand do not appear in this calculation.**

---

## Mechanic

### Customer-Base Revenue Model

Commercial and industrial buildings should generate revenue proportional to the population they serve, **up to a catchment ceiling** beyond which additional population brings no benefit (the city is "saturated" — every shopper/factory-recipient is already served).

**Formula concept** (pseudocode, actual values PLACEHOLDER):

```
commercialRevenuePerTick = commercialZones 
  × taxRate 
  × fraction 
  × customerBaseFactor(population, commercialZones)

where customerBaseFactor(pop, zones) = min(1, customerPopPerZone / CATCHMENT_PER_ZONE)
  and customerPopPerZone = max(MINIMUM_POP_BASE, pop × CUSTOMER_SHARE)
  and CATCHMENT_PER_ZONE = PLACEHOLDER, e.g. 5000–10000 people
```

**Example trajectory** (directional, not hardcoded):
- 0 population → revenue ≈ 0 (no customers)
- 5k population, 2 zones → revenue > 0, scales with zone count × taxRate
- 50k population, 2 zones → revenue reaches ceiling as catchment is met
- 50k population, 10 zones → revenue per zone is lower (oversupply, thin margins)

**Same logic for industrial**, keyed to factory/processor workload instead of retail footfall.

### Demand Feedback into Attractiveness

After computing `demand.commercial` and `demand.industrial` (unchanged from today), wire each into the attractiveness calculation **for that sector**:

- **For residential**: continue using `demand.residential` (unchanged, no disturbance)
- **For commercial**: introduce a commercial-attractiveness term that **increases** when `demand.commercial > 0` (unmet demand → players should build more), and **decreases** when `demand.commercial < 0` (oversupply → players should hold back)
- **For industrial**: same pattern

**Sketch** (actual formula PLACEHOLDER):

```typescript
const commercialAttractiveness = baseCommercialDesirability * (1 + demand.commercial / DEMAND_SCALE_FACTOR);
const industrialAttractiveness = baseIndustrialDesirability * (1 + demand.industrial / DEMAND_SCALE_FACTOR);
```

Where `DEMAND_SCALE_FACTOR` is a PLACEHOLDER (e.g., 150–200) that sets the **sensitivity** of attractiveness to demand swings.

### Conservation and Determinism

- All commercial revenue is booked **exactly once per tick** in `computeFlows()` under the label "Business Tax" (or split labels if demand-scaled revenue is broken out)
- Revenue is recorded through `lastFlows.inflows[]`, same as council tax and wages — no side channels, no ledger evictions
- No `Date` or `Math.random()` — deterministic tick arithmetic only, so replay reproduces every tick identically (GR#21)
- The residential loop's formula and terms remain unchanged — this mechanic adds **parallel** commercial/industrial terms, never alters the residential multiplier

---

## Acceptance Criteria

### AC-1: Commercial Revenue Scales Monotonically with Population

**Given** a city with a fixed number of commercial zones (e.g., 5 zones, fixed tax rate),  
**when** population grows from 0 to the catchment ceiling,  
**then** commercial tax revenue per tick increases monotonically (never decreases as population rises), up to the ceiling.

**Testable:** Plot revenue vs. population across a 100-tick window with fixed zones and tax rate; verify the curve is non-decreasing until the ceiling is reached.

---

### AC-2: Commercial Revenue Reaches a Catchment Ceiling

**Given** a city with N commercial zones and a population well above the per-zone catchment base,  
**when** population is further increased (e.g., +20k),  
**then** commercial revenue does **not** increase (the marginal population beyond the ceiling contributes zero revenue).

**Testable:** Fix zones and tax rate; verify revenue is constant from population = 50k to 100k (directional test: the curve flattens).

---

### AC-3: Industrial Revenue Scales Monotonically with Population (by Analogy)

**Given** a city with a fixed number of industrial zones,  
**when** population grows from 0 upward,  
**then** industrial tax revenue per tick increases monotonically, up to an industrial-specific catchment ceiling.

**Testable:** Same pattern as AC-1, applied to industrial tax (label "Freight Tax" or successor).

---

### AC-4: Unmet Commercial Demand Increases Commercial Attractiveness

**Given** a city where `demand.commercial > 0` (unmet demand; fewer commercial zones than the formula suggests the population wants),  
**when** a new commercial zone is considered for placement,  
**then** the attractiveness bonus for that zone is positive (a commercial-demand term is added to the calculation).

**Testable:** Compute the commercial-attractiveness term with demand = +50 vs. demand = 0; verify the former is higher.

---

### AC-5: Oversupply of Commercial Zones Dampens Attractiveness

**Given** a city where `demand.commercial < 0` (oversupply; many commercial zones relative to population),  
**when** another commercial zone is considered,  
**then** the attractiveness bonus is lower (or negative), discouraging further build.

**Testable:** Compute attractiveness with demand = -50 vs. demand = 0; verify the former is lower (directional: overstocking reduces appeal).

---

### AC-6: Industrial Demand Feeds Back into Industrial Attractiveness (by Analogy)

**Given** a city with `demand.industrial > 0` or `< 0`,  
**when** industrial attractiveness is computed,  
**then** the demand term modulates the industrial attractiveness the same way commercial demand modulates commercial attractiveness.

**Testable:** Verify industrial-attractiveness calculation includes an industrial-demand term with the same sign-direction as commercial.

---

### AC-7: Revenue Conservation — All Inflows Booked Through lastFlows

**Given** a tick where commercial and/or industrial revenue is computed,  
**when** the tick completes,  
**then** every revenue unit is present in `lastFlows.inflows[]` under a labelled entry (e.g., "Business Tax" or split "Business Tax (Pop Scaled)" + "Business Tax (Base)"),  
**and** the conservation identity holds: `fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows`.

**Testable:** Run `runConsistencyChecks(s)` (engine.ts) and verify conservation passes; inspect debug.json's `lastFlows` to confirm commercial/industrial revenue is fully booked.

---

### AC-8: Residential Loop Unaffected

**Given** the existing residential-attractiveness formula (engine.ts:1139-1142),  
**when** commercial/industrial demand feedback is added,  
**then** the residential term `Math.max(0.3, 0.55 + demand.residential / 200)` remains **exactly unchanged**,  
**and** scenario tests exercising residential growth (below-capacity growth trajectories) still pass with the same population curves.

**Testable:** Run existing residential-growth scenarios (e.g., a city with no commercial/industrial zones) and verify population curves are byte-identical to the pre-change baseline.

---

### AC-9: Determinism Preserved (Replay Identical)

**Given** a recorded action journal and a starting state,  
**when** the journal is replayed under the new mechanic,  
**then** the final state is **identical** (same population, funds, building layout, tick-by-tick flows).

**Testable:** Record a 500-tick game, then replay it; verify `debug.json` tick-by-tick flows, population, and funds match the original (use SHA256 hashing or byte-compare).

---

### AC-10: No Demand Wiring Outside WebConsole

**Given** the new commercial/industrial demand-feedback mechanic,  
**when** cross-module imports/calls are audited,  
**then** no **new** edges appear in `code.json` — all changes remain within the existing `fiscal.ts` / `engine.ts` / `debugjson.ts` triangle (same modules, same edges as today).

**Testable:** Diff `code.json` before and after the change; verify no new `[from, to]` edge is added for this feature.

---

## Balance Placeholders for Aaron

Every gameplay number below is a **PLACEHOLDER** pending Aaron's row-by-row balance approval. Tests must be **directional** (e.g., "revenue rises monotonically") and **not hardcode** these values.

| Parameter | Current / Old Value | Suggested Placeholder | Controls | Approval Note |
|-----------|----------------------|----------------------|----------|---------------|
| `COMMERCIAL_CUSTOMER_SHARE` | N/A (new) | 0.22 | Fraction of population that is a potential customer for commercial zones. Higher = larger addressable market. | Tied to shopBase formula in demandOf() |
| `COMMERCIAL_CATCHMENT_PER_ZONE` | N/A (new) | PLACEHOLDER: 3000–8000 people/zone | Population beyond which additional people bring zero marginal revenue per zone (saturation point). Affects ceiling height. | Balance pass: tune per player feedback |
| `INDUSTRIAL_CUSTOMER_SHARE` | N/A (new) | 0.18 | Fraction of population feeding industrial demand (similar to shopBase). | Tied to indBase formula |
| `INDUSTRIAL_CATCHMENT_PER_ZONE` | N/A (new) | PLACEHOLDER: 2000–6000 people/zone | Industrial saturation point. Typically lower than commercial (factories serve a broader but sparser customer base). | Balance pass |
| `COMMERCIAL_DEMAND_SCALE_FACTOR` | N/A (new) | PLACEHOLDER: 150–200 | Sensitivity of commercial attractiveness to demand swings. Higher = demand changes move the needle more. | Balance pass: tune responsiveness |
| `INDUSTRIAL_DEMAND_SCALE_FACTOR` | N/A (new) | PLACEHOLDER: 150–200 | Sensitivity of industrial attractiveness to demand swings. | Balance pass |
| `COMMERCIAL_ATTRACTIVENESS_BASE` | N/A (new) | PLACEHOLDER: 1.0 (or scale from existing) | Baseline commercial-sector desirability (e.g., relative to residential). | Design: Aaron to set per city flavor |
| `INDUSTRIAL_ATTRACTIVENESS_BASE` | N/A (new) | PLACEHOLDER: 1.0 (or scale from existing) | Baseline industrial-sector desirability. | Design: Aaron to set |

---

## Required RED Tests

Every AC above must have a **directional RED test** that **can fail** — i.e., the test succeeds if the correct behaviour is implemented, fails if the code is absent or wrong.

### Test Suite: `commercial-industrial-feedback-test.mjs`

1. **Test: Commercial Revenue Monotonic to Catchment (AC-1)**
   - Setup: City with 5 fixed commercial zones, tax rate 11 (default)
   - Sweep: Grow population 0 → 60k, tick to steady-state each step
   - Verify: `commercialTax(pop_i+1) >= commercialTax(pop_i)` for all steps
   - Fail condition: Revenue decreases as population increases

2. **Test: Commercial Revenue Ceiling (AC-2)**
   - Setup: City with 5 commercial zones, tax rate 11
   - Sweep: Population 50k, then 60k, then 70k, 80k, 90k, 100k
   - Verify: Revenue at 60k ≈ revenue at 100k (flat curve after ceiling)
   - Fail condition: Revenue continues growing past the catchment threshold

3. **Test: Industrial Revenue Monotonic (AC-3)**
   - Setup: City with 5 fixed industrial zones, tax rate 13 (default)
   - Sweep: Grow population 0 → 60k
   - Verify: `industrialTax(pop_i+1) >= industrialTax(pop_i)`
   - Fail condition: Revenue decreases

4. **Test: Unmet Commercial Demand Raises Attractiveness (AC-4)**
   - Setup: City with 2 commercial zones, population 30k → `demand.commercial > 0`
   - Compute: `commercialAttractiveness` at this state
   - Setup: City with 10 commercial zones, population 30k → `demand.commercial < 0`
   - Compute: `commercialAttractiveness` at this state
   - Verify: First attractiveness > second attractiveness
   - Fail condition: Demand has no effect on attractiveness

5. **Test: Oversupply Dampens Attractiveness (AC-5)**
   - Setup: City with 10 commercial zones, population 30k → `demand.commercial < 0`
   - Compute: `commercialAttractiveness`
   - Compare: to the baseline (0 demand)
   - Verify: Oversupply attractiveness ≤ baseline
   - Fail condition: Oversupply does not reduce attractiveness

6. **Test: Industrial Demand Modulates Industrial Attractiveness (AC-6)**
   - Setup: Two cities, one with `demand.industrial > 0`, one with `< 0`
   - Verify: Industrial attractiveness follows the same sign-direction as commercial
   - Fail condition: Industrial demand does not affect industrial attractiveness

7. **Test: Conservation (AC-7)**
   - Setup: Arbitrary city state with mixed commercial/industrial
   - Advance 100 ticks
   - Verify: For every tick, `consistencyCheck(s).conservationError === 0`
   - Verify: Every revenue unit is in `lastFlows.inflows` (no hidden debits)
   - Fail condition: Conservation fails or revenue is missing from flows

8. **Test: Residential Growth Curve Unchanged (AC-8)**
   - Setup: Record a 400-tick game with NO commercial/industrial zones (pure residential)
   - Capture: Population at every 10-tick interval
   - Re-run: Same setup, same actions under the new code
   - Verify: Population at every checkpoint is **byte-identical**
   - Fail condition: Population curve differs (residential loop was broken)

9. **Test: Replay Determinism (AC-9)**
   - Setup: Record an action journal (500 ticks, mixed zones, random builds)
   - Capture: `debug.json` snapshots every 50 ticks (funds, population, demand values, lastFlows checksum)
   - Re-run: Replay the journal from genesis
   - Verify: Every snapshot is **byte-identical**
   - Fail condition: Any tick diverges (non-determinism detected)

10. **Test: No New Cross-Module Edges (AC-10)**
    - Setup: Load `code.json` before and after the change
    - Verify: No new edge `[from, to]` is added for this feature
    - Verify: All changes remain in `fiscal.ts` / `engine.ts` / `debugjson.ts`
    - Fail condition: A new cross-module dependency appears

---

## Assumptions for Aaron / Bev

1. **Population as Base**: The customer base is modelled as a simple fraction of total population (`COMMERCIAL_CUSTOMER_SHARE`, `INDUSTRIAL_CUSTOMER_SHARE`), not a separate "employed" or "in-service-range" subset. (The TS webconsole sim has no employment-status field; the Go engine does, but this change is TS-only.)

2. **Linear Scaling to Ceiling**: Revenue scales linearly from 0 to the catchment ceiling, then stays flat. No softening, no smooth asymptote — a step-function-like saturation. (Can be refined in a balance pass if the curve feels artificial.)

3. **Demand as Unmet Desire**: `demand.commercial` and `demand.industrial` continue to measure unmet desire (more shops wanted = positive demand; too many shops = negative). The feedback term simply uses that signal to adjust attractiveness **proportionally** — no hardcoded thresholds, no "if demand > 50 then unlock".

4. **No Gameplay-Logic Wiring**: The commercial/industrial attractiveness boost is **UI/simulation feedback only** — it does NOT unlock buildings, gate zones, or trigger special events. (That is, a player can still place a zone even if attractiveness is low; the feedback just makes it less appealing.)

5. **Tick Timing**: All revenue calculations and demand-feedback updates happen **within the same `advanceTick()` call** — no side effects bleed into the next tick. Determinism and replay rely on this (GR#21).

6. **WebConsole-Only Scope**: This mechanic is **not** wired to the Go backend engine at this time. The Go engine has its own revenue and attractiveness logic (different models, different data); this is a TS webui-sim feature. If the Go engine is later extended with the same mechanic, it will be a separate build item.

7. **No Population Redistribution**: The customer base is global (all population can shop anywhere); the model does not track local catchment or commute friction. A future mechanic might split population by zone/district; this one assumes a uniform pool.

8. **Tax Rate Lever Orthogonal**: The player's tax-rate slider (`taxRate.commercial`, `taxRate.industrial`) continues to work as a **multiplier** on top of the population-scaled base revenue. Changing the tax rate remains a direct lever on income, independent of demand.

---

## Summary

This spec wires the commercial and industrial sectors into the webconsole sim's consumption loop, making city economy more dynamic:

- **Commercial/industrial revenue scales with population**, creating a feedback where a growing city generates more tax revenue per zone (up to a ceiling).
- **Demand metrics now feed back into zone attractiveness**, so players naturally want to build more shops when there's unmet retail demand, and fewer when they're oversupplied.
- **Determinism and conservation preserved** — all revenue flows through `lastFlows`, replay works, residential loop untouched.
- **All numbers are PLACEHOLDER** — balance pass awaits Aaron's row-by-row sign-off; tests are directional and never hardcode magic values.

The implementation lives **entirely within the webconsole fiscal/engine/data modules** — no new cross-module edges, no schema changes to the save format, and no wiring to the Go backend.
