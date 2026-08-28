# FEAT-1972079878: Webconsole Density and Scale

**Feature:** Expand density/capacity options for housing, offices, and farms; add auto-scaling mechanic (~10%/yr) for buildings.

**Mkey:** FEAT-1972079878

## Overview

Extends the building catalogue and auto-scaling system to provide:
1. **More housing-density options** — new estate-scale residential specs (small, medium, large densities) beyond the single `res_estate` to create varied neighborhoods
2. **Denser office variants** — office specs with higher capacity than `off_businesspark` for downtown/hi-density office districts
3. **Bigger farms** — larger farm operations (estate-scale) combining multiple agricultural land types
4. **Auto-scale-out mechanic** — buildings (estates, offices, farms, workplaces) gradually increase residents/jobs capacity by ~10% per year when online and carrying demand, similar to road auto-scaling (FEAT-1972079907 inc2), deterministically replayable via monitors, respecting activation gates

---

## Scope: What Already Exists (Out of This Feature's Scope)

**FEAT-1972079900 inc1 ("density LOD inc1," commit 40336b6) delivered:**
- Four placeable estate-scale specs:
  - `res_estate` (Housing Estate, 5×5, 1500 residents)
  - `off_businesspark` (Business Park, 5×5, 1200 jobs)
  - `ind_estate` (Industrial Estate, 6×6, 2000 jobs)
  - `com_hypermarket` (Hypermarket, 5×5, 800 jobs)
- Render-coarsening (auto-merge clusters at zoom) and up-density variants deferred to inc2
- **Note:** FEAT-1972079878 does NOT re-deliver the estates in inc1; it builds ON them by adding variants and the auto-scale mechanic

---

## Design Decisions Flagged for Lead

### DD1: Housing Estate Variants — Density Tier Strategy
**How many housing estate variants, and what capacity range?**
- **Option A (CHOSEN UNLESS OVERRULED):** Three variants alongside `res_estate` (tier-2 medium):
  - `res_estate_compact` (tier-1 low): 4×4 footprint, ~900 residents (apartment blocks in dense urban cores)
  - `res_estate` (tier-2 medium): 5×5 footprint, ~1500 residents (master-planned suburb — existing)
  - `res_estate_sprawl` (tier-3 high): 6×6 footprint, ~2500 residents (low-rise suburban sprawl)
- **Option B:** Five variants spanning tier-1 to tier-3 with finer gradation (more choice, more code)
- **Option C:** Single adaptive spec; capacity tuned per-city based on unlock level or player choice (system complexity, no fixed catalogue entry)
- **Consequence:** Option A gives players three distinct aesthetic/economic choices; Option B gives more control; Option C requires runtime state tracking not currently in the Building type.
- **Flag for Aaron:** Confirm variant count and resident targets (Option A or other).

---

### DD2: Office Variants — High-Density Office Specs
**How many office variants and capacity distribution?**
- **Option A (CHOSEN UNLESS OVERRULED):** One high-density variant alongside `off_businesspark`:
  - `off_businesspark` (tier-2): 5×5 footprint, ~1200 jobs (landscaped parks, light office)
  - `off_towers_downtown` (tier-3): 5×5 footprint, ~2000 jobs (dense downtown office towers, PLACEHOLDER cost ×1.5 of businesspark)
- **Option B:** Three variants (small suite / medium park / large towers) each with distinct footprint and cost
- **Option C:** Keep single spec; variant is a player choice via upgrade mechanic (instead of placement choice)
- **Consequence:** Option A is minimal and directional; Option B is feature-complete but more specs to balance; Option C defers choice to runtime.
- **Flag for Aaron:** Confirm office variant strategy.

---

### DD3: Farm Estate and Scale
**What constitutes a "bigger farm" and how do they relate to existing small farms?**
- **Option A (CHOSEN UNLESS OVERRULED):** One farm estate spec — `farm_estate`:
  - Footprint: 6×6 (larger than individual farms: wheat/cattle/orchard are 2×2..3×3)
  - Capacity: ~1500 jobs aggregate (representing mixed crop + livestock, e.g., dairy + grain + beef)
  - Cost: ~2× a single small farm (placeholder pending Aaron's balance pass)
  - No new "crop type" — it is an AGGREGATE (single building, not a crop-tracking spec)
- **Option B:** Specialize the farm estate by crop (grain estate, beef estate, dairy estate) with distinct capacities and pollution tags
- **Option C:** Keep farms as-is; "bigger" is addressed only by the auto-scale mechanic (gradual capacity growth)
- **Consequence:** Option A is simple (one new spec); Option B is thematic but multiplies catalogue entries; Option C defers to dynamic scaling.
- **Flag for Aaron:** Confirm whether a farm estate is needed, and if so, whether it is mixed-commodity or specialized.

---

### DD4: Auto-Scale Mechanic — Monitor-Based vs. Decay-Based
**What triggers and tracks building capacity growth?**
- **Option A (CHOSEN UNLESS OVERRULED):** Building-demand monitors (parallel to road monitors):
  - Placed on buildings at construction (e.g., when `res_estate` is placed, a ResidentsMonitor logs its id + builtTick).
  - At monthly boundaries, monitors evaluate: if the building is online AND its residents/jobs are at/near capacity (>85% utilization, parallel to road saturation), capacity scales +10% and a cost is charged.
  - Window: monitors expire after 1 year (360 ticks); old buildings can re-qualify for growth (perpetual, but capped by the mechanic's thresholds).
  - Deterministic, order-independent (sorted by building id), replayable via journal.
- **Option B:** Time-decay scaling — every online building +10% capacity each year automatically, no demand check (simpler, but does not respond to city needs).
- **Option C:** Player-driven upgrade — buildings have an "upgrade" action, player pays, capacity +10% (requires UI + interaction, no auto).
- **Consequence:** Option A mirrors the proven road mechanic and responds to actual utilization (dynamic). Option B is automatic but may over-build. Option C is manual but predictable.
- **Flag for Aaron:** Confirm auto-scale driver (demand-based monitor vs. time-based decay vs. player-driven).

---

### DD5: Auto-Scale Cost Model
**How is the capacity increase cost calculated and charged?**
- **Option A (CHOSEN UNLESS OVERRULED):** Delta-cost model (parallel to road auto-scale, FEAT-1972079907 inc2):
  - Cost = `placementCost(nextCapacityTier) - placementCost(currentCapacityTier)`.
  - The spec does not change (e.g., `res_estate` remains `res_estate`), only an internal capacity field increments.
  - Charged through flows as "Building Auto-Scale" outflow (visible in fiscal panel, counts for conservation).
- **Option B:** Percentage-of-original cost — always charge (original cost × 0.1) per upgrade, regardless of tier progression.
- **Option C:** FREE — no charge, capacity grows for operational cost only (no growth tax).
- **Consequence:** Option A requires tracking capacity tiers on the Building struct (new field: `capacityTier`); Option B is simpler but ignores building complexity; Option C is generous but can lead to exponential unchecked growth.
- **Flag for Aaron:** Confirm whether capacity growth is charged, and if so, the cost model.

---

### DD6: Offline Buildings and Auto-Scale
**Do offline buildings (road-disconnected, under construction, etc.) participate in auto-scaling?**
- **Option A (CHOSEN UNLESS OVERRULED):** Auto-scale does NOT apply to offline buildings.
  - At evaluation time, if `isOnline(state, building) === false`, the monitor is skipped; no scaling occurs.
  - Grace period (FEAT-1972079891 DD4 Option A) does NOT protect scaling; a graceful building still scales if it would (but grace is about activation gates, not scaling).
  - Rationale: a disconnected neighborhood cannot grow organically; scale-up requires demand (foot traffic, workers arriving), which requires connectivity.
- **Option B:** Offline buildings scale at a REDUCED rate (e.g., 5%/yr instead of 10%).
- **Option C:** Offline buildings scale the same as online (growth is independent of connectivity).
- **Consequence:** Option A enforces a consistent design (connectivity gates capacity growth). Option B allows slow expansion even isolated. Option C is permissive.
- **Flag for Aaron:** Confirm auto-scale condition on isOnline.

---

## Acceptance Criteria

### AC-1: Housing Estate Variants — Specs Defined
**Three housing estate specs graduated into the real catalogue (or per DD1 choice).**

- **Specs (if Option A):**
  - `res_estate_compact`: 4×4 footprint, ~900 residents, cost ~£32k (placeholder), upkeep ~£90, tier-1
  - `res_estate`: 5×5 footprint, ~1500 residents, cost ~£45k (existing), upkeep ~£130, tier-2
  - `res_estate_sprawl`: 6×6 footprint, ~2500 residents, cost ~£70k (placeholder), upkeep ~£200, tier-3
- **Spec properties (all three):**
  - `kind: 'residential'`, `category: 'zones'`
  - No `mw`, no `served` (housing does not provide services)
  - `residents: N` (spec-specific capacity)
  - `placeholder: false` (real, placeable)
  - Unique ids, palette entries (Housing group)
- **Render:** Density tier colours applied (1=grey, 2=blue, 3=gold) per existing tier logic
- **Test:** Each spec is placeable under unlockedAll; residents capacity matches spec definition; cost/upkeep are non-zero; spec carries no cross-system leaks (mw/served/children absent)

---

### AC-2: Office Variants — Specs Defined
**New high-density office spec(s) graduated (or per DD2 choice).**

- **Spec (if Option A):**
  - `off_towers_downtown`: 5×5 footprint, ~2000 jobs, cost ~£128k (placeholder, ~1.5× businesspark), upkeep ~£630, tier-3
- **Spec properties:**
  - `kind: 'office'`, `category: 'zones'`
  - No `mw`, no `served`
  - `jobs: 2000`
  - `placeholder: false`
  - Palette entry (Offices group)
- **Render:** Tier-3 colour (gold)
- **Test:** Placeable, jobs capacity correct, cost/upkeep non-zero, no cross-system leaks

---

### AC-3: Farm Estate — Spec Defined
**New farm estate spec graduated (or per DD3 decision, may be skipped if Option C chosen).**

- **Spec (if Option A):**
  - `farm_estate`: 6×6 footprint, ~1500 jobs, cost ~£2400 (placeholder), upkeep ~£15, tier-3, no pollution tag (mixed-commodity, assumed balanced)
- **Spec properties:**
  - `kind: 'industrial'`, `category: 'zones'`
  - No `mw`, no `served`
  - `jobs: 1500`
  - `placeholder: false`
  - Palette entry (Industry & Farms group)
- **Render:** Tier-3 colour
- **Rationale:** Represents a large integrated farm operation (crop + livestock processing) at a single LOD
- **Test:** Placeable, jobs capacity correct, cost/upkeep, no cross-system leaks

---

### AC-4: Building Type Extended — Capacity Tier Tracking (If DD5 Option A)
**Building struct gains optional `capacityTier` field for auto-scale state.**

- **Field:** `capacityTier?: number` (0-indexed, starting at 0 = original placement capacity)
- **Initial value:** Undefined (absent on buildings placed before feature release; treated as tier 0)
- **Lifecycle:** Incremented by auto-scale at monthly boundaries; persists across saves/loads
- **Storage:** Serialized in debug JSON and save files (deterministic replay)
- **Rendering:** Does NOT affect visual appearance (the spec's density tier is fixed; capacity tier is economic only)
- **Test:** A building placed with capacityTier absent reads as tier 0; after one successful scale, capacityTier === 1; capacity value follows the tier progression

---

### AC-5: Building Capacity Tiers — Spec Extension
**Every spec that can auto-scale has a `capacityTiers` array defining capacity per tier.**

- **Format:** `capacityTiers: [number, number, ...]` — indexed by tier (0, 1, 2, ...).
  - Example for `res_estate`: `capacityTiers: [1500, 1650, 1815, 1996, 2196, ...]` (each ~10% higher)
- **Tiers defined for:**
  - Housing estates: `res_estate_compact`, `res_estate`, `res_estate_sprawl`
  - Office estates: `off_businesspark`, `off_towers_downtown`
  - Farm estate: `farm_estate`
  - Industrial workplaces: `ind_estate`, `off_businesspark` (if eligible per AA design)
- **Scope:** Do NOT add to non-scalable specs (pylons, roads, services, landmarks)
- **Storage:** In data.ts alongside existing spec fields
- **Access:** Helper function `capacityAtTier(sp: Spec, tier: number): number` returns capacity for tier (or original if tier > array length — capped)
- **Test:** Each capacity array has enough tiers to reach plausible max (e.g., 5–10 tiers); every entry is realistic (monotonic increasing); placement/unlock do not break on missing/stale capacityTiers

---

### AC-6: Building-Demand Monitors — Data Structure
**SimState gains `buildingMonitors` field (parallel to existing `roadMonitors`).**

- **Type:** `BuildingMonitor[]` — array of:
  ```typescript
  {
    buildingId: number;      // Building.id this monitor tracks
    until: number;           // tick when monitor expires (builtTick + 360 for 1-year window)
    type: 'residents' | 'jobs';  // which capacity to scale
  }
  ```
- **Initialization:** Empty array `[]` on game start; monitors added at building placement
- **Scope:** Only created for specs with `capacityTiers` defined
- **Lifecycle:**
  - Placed when building is constructed (at construction completion, `advance()` time builtTick + constructionTicks)
  - Expires when `tick > monitor.until` (auto-dropped at next evaluation)
  - Survives save/load (persisted in debug JSON)
- **Test:** Initial state has empty monitors; after placing a scalable building, a monitor appears; monitor expires correctly after its window

---

### AC-7: Auto-Scale Evaluation — Monthly Monitor Pass
**At every monthly boundary (tick % TICKS_PER_MONTH === 0), monitors are evaluated deterministically.**

- **Process (deterministic, order-independent):**
  1. Drop monitors whose `until < tick` (expired)
  2. Sort survivors by `buildingId` (deterministic order, not map iteration order)
  3. For each monitor:
     - Fetch building by id; if bulldozed, skip
     - Check `isOnline(state, building)` — if false, skip (offline buildings do not scale)
     - Read current capacity = `capacityAtTier(sp, building.capacityTier ?? 0)`
     - Compute utilization:
       - For residents: `util = state.population / totalResidentsCapacity(state)` (rounded to 0..1)
       - For jobs: `util = totalJobsNeeded(state) / totalJobs(state)` (rounded to 0..1)
     - If `util >= 0.85` (threshold, PLACEHOLDER per balance regime), upgrade:
       - `building.capacityTier = (building.capacityTier ?? 0) + 1`
       - Cost = delta-cost (per DD5 Option A; or per chosen DD5 option)
       - Increment upgrade counter
- **Function:** `evaluateBuildingMonitors(s: SimState, tick: number): BuildingScaleResult` (parallel to evaluateRoadMonitors)
  - Returns: `{ buildings, monitors, cost, upgraded }`
- **Determinism:** Same seed state + same tick → same outcome (no Date/random/map-order)
- **Test:** Place scalable building with population/jobs at 85%+ utilization; advance to monthly boundary; verify capacityTier increments and cost is charged; place another building and verify it does not scale (below threshold); advance under 85% and verify no scale occurs

---

### AC-8: Auto-Scale Integration — Tick Flow
**Auto-scale pass is integrated into `advance()` at the same point as road scaling (FEAT-1972079907 inc2).**

- **Timing:** Building monitors evaluated FIRST (before road monitors, or in same pass if both run), AFTER funds/conservation check.
- **Flow integration:**
  - Upgrade cost is added to `outflows: [..., { label: 'Building Auto-Scale', value: cost }]`
  - Ledger entry: `{ label: `Auto-scaled ${upgraded} building(s)`, amount: -cost }`
- **Conservation:** Cost charged through flows so `funds` reconciles; replay reproduces exact cost and upgrades
- **Consequence:** Multiple upgrade rounds on the same building in a year are possible (if placed early and utilization stays high); cost and upgraded count both track total
- **Test:** City with high population/jobs demand triggers building auto-scale on the monthly boundary; outflows include "Building Auto-Scale" line; ledger shows scaled count; funds reconcile; replaying the same state yields byte-identical outcomes

---

### AC-9: Utilization Calculation — SSOT
**Building-demand utilization reads from the same `serviceCoverageOf()` and job totals that economy/demand use (GR#3 SSOT).**

- **Rule:** Do NOT re-derive utilization independently; it MUST consume the same functions:
  - `residentsCapacity(state)` — total housing capacity (sum of online residents)
  - `totalJobs(state)` — total workplace capacity (sum of online jobs)
  - `state.population` — current resident count
  - Job demand derivation (e.g., unemployed + working population)
- **Consequence:** If a building goes offline due to missing services (AC-6 of FEAT-1972079891), it drops out of both utilization numerator AND denominator (capacity), so the ratio stays honest
- **Test:** Place housing with 1000 capacity; city has 850 residents → util = 0.85 → should auto-scale. Demolish half the housing → util = 850 / 500 = 1.7 (capped at 1.0) → should scale. Move a building offline (disconnect road) → it drops from capacity, utilization recalculates; if remaining capacity is high, scaling stops

---

### AC-10: Auto-Scale Cost Model — Tiered Progression (If DD5 Option A)
**Capacity upgrade cost follows a delta-cost model tied to placement cost, mirrors road auto-scale.**

- **Logic:** When a building scales to tier N+1:
  - Original cost: `placementCost(spec)` (from data.ts)
  - Per-tier upgrade cost (PLACEHOLDER): `originalCost × 0.15` (15% of placement cost per tier, directional)
  - Total cost for tier 0→1: `0.15 × originalCost`
  - This is charged through flows as "Building Auto-Scale" outflow
- **Rationale:** Ties growth cost to building scale; bigger buildings cost more to upgrade
- **Test:** Place `res_estate` (placement cost ~45k); trigger auto-scale → cost ~6.75k charged; ledger and outflows both record it; place `ind_estate` (cost ~180k) → upgrade cost ~27k; verify conservative reconciles

---

### AC-11: Offline Buildings and Auto-Scale Interaction
**Offline buildings (road-disconnected, under construction, grace period) do not trigger auto-scale (per DD6 Option A).**

- **Rule:** In `evaluateBuildingMonitors()`, skip any monitor whose building is offline (`isOnline(state, b) === false`)
- **Rationale:** A disconnected building cannot grow; it has no economic activity (zero residents/jobs flow)
- **Consequence:**
  - Grace-period buildings (FEAT-1972079891 DD4 Option A) CAN scale if online (grace does not inhibit scaling)
  - An offline building's monitor is NOT dropped; it is temporarily skipped, and can resume if the building comes online again within the window
- **Test:** Build a housing estate, then immediately cut its road → goes offline. Advance to monthly boundary → no scale (building is offline). Reconnect road → building comes online. Advance another month → should scale if utilization is high (monitor is still active within the 1-year window)

---

### AC-12: Determinism and Replayability
**Building auto-scale is deterministic; same state + same tick → byte-identical outcomes, replayable via journal.**

- **No randomness:** No Date/Math.random() in evaluateBuildingMonitors
- **Order-independent:** Sorted by buildingId, not map iteration order
- **State-only:** Outcome depends on (SimState, tick) alone
- **Journal consistency:** If a build action includes a place(buildingId, spec), and later ticks auto-scale that building, replay reproduces the exact same scaled state
- **Test:** Create a scenario with buildings in high-demand state; save snapshot at tick 0; run to tick 720 (2 months). Replay the same scenario from snapshot in a fresh session; verify buildings have identical capacityTier and funds reconcile bit-for-bit

---

### AC-13: Capacity Change Display — No Visual Asset Impact
**The visual appearance of a building does NOT change when capacity tier increments.**

- **Reason:** Capacity is an economic/flow property; the spec's visual density tier is fixed (determined at placement)
- **Consequence:**
  - A `res_estate` placed today always renders as a tier-2 density block (blue border), even after 10 years of scaling
  - Capacity is only visible in the building inspector/details panel (economy info, not visuals)
- **Implementation:** No new render code; capacity tier is an economic-only field
- **Test:** Auto-scale a building; verify visual appearance is unchanged; inspect panel shows new capacity number

---

### AC-14: Unlock and Availability
**New housing/office/farm estate specs follow the existing unlock gate (specUnlocked in engine.ts).**

- **Unlock levels (PLACEHOLDER — pending Aaron's balance pass):**
  - `res_estate_compact`: unlock level ~8 (early, compact housing fills early-game neighborhoods)
  - `res_estate`: unlock level ~10 (existing)
  - `res_estate_sprawl`: unlock level ~15 (late, sprawl is a post-established luxury)
  - `off_towers_downtown`: unlock level ~14 (late, downtown towers are high-complexity)
  - `farm_estate`: unlock level ~12 (mid-game, large farming)
- **Gate:** `isPlaceable(state, spec)` checks unlock level against state.unlockedUntil; locked specs render greyed-out in catalogue
- **Test:** Early game (unlockedUntil = 5): compact estate not available, regular estate available. Late game (unlockedUntil = 20): all three estates available

---

### AC-15: Balance Numbers — All Placeholders (Regime Per GR#15)
**Every number is a directional placeholder pending Aaron's balance pass:**

- **Housing resident counts:** `res_estate_compact` ~900, `res_estate` ~1500, `res_estate_sprawl` ~2500 — PLACEHOLDERS
- **Office job counts:** `off_businesspark` ~1200, `off_towers_downtown` ~2000 — PLACEHOLDERS
- **Farm jobs:** `farm_estate` ~1500 — PLACEHOLDER
- **Cost/upkeep:** All PLACEHOLDER; anchored to constituent building scales, not absolute
- **Capacity tier progression:** ~10%/year is DIRECTIONAL; actual % pending Aaron
- **Auto-scale threshold:** 0.85 utilization is PLACEHOLDER
- **Upgrade cost:** 15% per tier is PLACEHOLDER
- **Unlock levels:** 8–15 are PLACEHOLDER
- **Testing guidance:** Never hardcode checks like "at 2000 residents, N buildings auto-scale" — those are balance tuning, not acceptance. Test structure: "given utilization ratio U, building upgrade matches threshold rule" (U is a parameter)

---

### AC-16: Migration and Save Compatibility
**Existing save games (pre-feature) remain valid; new specs and auto-scale fields are absent by default.**

- **Loaded old save:** Buildings carry no `capacityTier` (absent field treated as 0); no `buildingMonitors` list
- **New buildings placed after feature:** Auto-scale-eligible specs get a monitor at construction
- **Migration:** No retroactive upgrade for old buildings; their capacityTier stays at 0 unless they satisfy demand conditions AFTER load and auto-scale thereafter (opt-in to growth)
- **Test:** Load a pre-feature save; existing buildings render normally; place a new auto-scale-eligible building; verify monitor appears and scales correctly; old building never scales (or scales only if it comes online and gets a monitor re-issued — designer choice)

---

### AC-17: Cross-System Leak Guards (Parallel to FEAT-1972079900 inc1)
**New estate specs do NOT introduce power, water, or service leaks (GR#7, waste_depot lesson).**

- **Resident estate specs:**
  - NO `mw` (no grid generation leak)
  - NO `served` (no water supply leak)
  - NO `tag` (not pollution, not water)
  - Waste and power/water DRAW still flow through existing gates (powerStats, waterCaps, wasteGeneratedOf)
- **Office/farm estate specs:**
  - NO `mw`
  - NO `served` (workplaces do not supply services)
  - Industrial specs carry `tag: 'pollution'` where appropriate (farm_estate: none, industrial_estate: already set)
- **Test:** Sum of `mw` across all buildings excludes estate specs. Sum of `served` (water supply) excludes estates. Waste generation for an estate = jobs × WASTE_PER_JOB, consistent with any other workplace

---

### AC-18: Unit Consistency
**Capacity/utilization measured in consistent units from code.json registry.**

- **Residents:** Persons (unit: person; from `sp.residents` and `state.population`)
- **Jobs:** Persons (unit: person; from `sp.jobs` and employment-tracking flows)
- **Utilization ratio:** Dimensionless, 0..1 (capacity-bound), capped at 1.0 for over-utilization
- **Upgrade cost:** Micropounds (standard game currency unit)

---

## Non-Acceptance Criteria (Out of Scope)

- **Render-coarsening / LOD:** Merging clusters of estates at zoom; deferred to inc2
- **Player choice of variant at runtime:** Buildings do not have a "choose your density" modal after placement; variant is chosen at placement time
- **Per-building capacity display UI:** The existing building inspector shows capacity; no new panel required
- **Partial auto-scale:** Scaling is binary — either a tier increments or it does not (not a gradual in-progress animation)
- **Negative auto-scale:** Buildings never shrink capacity (one-way ratchet)
- **Retroactive scaling:** Pre-feature buildings do not retroactively get monitors; new scaling applies only to buildings placed after (or explicitly opted into via design choice)
- **Cross-estate scaling interactions:** Estates scale independently; no "cluster of estates triggers a mega-scale" mechanic

---

## Testing Strategy

### Unit Tests
- `densityTier(sp)` returns 1, 2, or 3 consistently for the new estate specs
- `capacityAtTier(sp, tier)` returns correct capacity for each tier (monotonic increasing, realistic)
- `evaluateBuildingMonitors(state, tick)` drops expired monitors, respects offline gate, scales when utilization ≥ threshold
- `isOnline(state, building)` correctly gates offline buildings from auto-scale evaluation (pre-condition for monitor skip)

### Integration Tests
- Place `res_estate_sprawl` with high population density → auto-scale at next monthly boundary
- Place office in low-demand area → no auto-scale (below threshold)
- Place estate, disconnect road → goes offline → monitor still exists but is skipped → reconnect road → resumes scaling if demand high
- Multiple buildings placed early, high demand → verify all scale at correct monthly boundaries with correct costs

### Determinism Tests
- Save scenario with mix of estates at various demand levels; run to tick 720; replay; verify capacityTier and fund reconciliation byte-identical
- No timing-dependent flashing or Date.now() calls in monitor evaluation

### Balance Tuning (NOT required for AC)
- Aaron reviews resident/job/cost/upkeep numbers per spec
- Aaron tunes unlock levels, threshold (0.85 placeholder), tier progression (~10% placeholder), upgrade cost (15% placeholder)
- Measure typical city: count estates by type, measure average scaling event/building/year, confirm feel-good progression

---

## References

- **FEAT-1972079900 inc1 (existing):** `webconsole/src/sim/data.ts` (lines 1114–1123, the four estate specs); `webconsole/test/density-inc1.test.mjs` (estate tests)
- **Road auto-scale pattern (FEAT-1972079907 inc2):** `webconsole/src/sim/engine.ts` (evaluateRoadMonitors, advance() integration); `webconsole/test/road-inc2.test.mjs`
- **Building activation gates (FEAT-1972079891 inc1):** `webconsole/src/sim/engine.ts` (isOnline); `webconsole/src/sim/data.ts` (graceTick migration)
- **Building type:** `webconsole/src/sim/types.ts` (interface Building)
- **Economy/capacity totals:** `webconsole/src/sim/engine.ts` (residentsCapacity, totalJobs, computeFlows)
- **Utilization reading (GR#3 SSOT):** `serviceCoverageOf()`, `powerStats()`, `waterCaps()` in engine.ts

---

## Acceptance Criteria Summary

| AC | Description | Design Decisions |
|---|---|---|
| AC-1 | Housing estate variants defined | DD1 (option choice on count/capacity) |
| AC-2 | Office variants defined | DD2 (option on variants) |
| AC-3 | Farm estate spec defined | DD3 (option on farm estate type) |
| AC-4 | Building.capacityTier field | DD5 Option A (delta-cost tier tracking) |
| AC-5 | Spec.capacityTiers array | DD5 Option A |
| AC-6 | BuildingMonitor data structure | DD4 Option A (monitor-based scaling) |
| AC-7 | Monthly auto-scale evaluation | DD4 Option A, DD6 Option A (offline gate) |
| AC-8 | Tick flow integration | DD4 Option A |
| AC-9 | Utilization SSOT (GR#3) | — |
| AC-10 | Delta-cost model | DD5 Option A |
| AC-11 | Offline building gate | DD6 Option A |
| AC-12 | Determinism / replay | — |
| AC-13 | Visual appearance unchanged | — |
| AC-14 | Unlock gate integration | — |
| AC-15 | Balance number placeholders | GR#15 |
| AC-16 | Save compatibility | — |
| AC-17 | Cross-system leak guards | — |
| AC-18 | Unit consistency | — |

---

## Design Decision Summary

| DD | Decision | Recommendation | Lead Approval |
|----|----------|---|---|
| DD1 | Housing density tiers | Option A (3 variants: compact/medium/sprawl) | Pending |
| DD2 | Office variants | Option A (add tier-3 downtown towers) | Pending |
| DD3 | Farm estate | Option A (6×6, mixed-commodity estate) | Pending |
| DD4 | Auto-scale driver | Option A (monitor-based, demand-triggered) | Pending |
| DD5 | Cost model | Option A (delta-cost tied to placement cost) | Pending |
| DD6 | Offline scaling | Option A (offline buildings do NOT scale) | Pending |

