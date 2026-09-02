# FEAT-autoscale-ladder — Residential & Service Auto-Scale Ladder (Up + Out)

**Status:** DRAFT — docs only, no code. Awaiting Aaron's row-by-row approval on height caps (Q100083) and service-spec scope.  
**mkey:** `engine.citybuilder` (placeholder — claim a FEAT- code via `add-error.js`/BOW before build dispatch, per GR#25).  
**Supersedes:** The spec-coverage gap diagnosed in BUG-590 — extends `capacityTiers` ladder from the 3 estate-tier specs to **all 10 residential specs** and **all service specs with scaled capacity** (schools, health, offices, police). Keeps unchanged: the existing auto-scale monitor enrolment at sustained 100% occupancy (BUILDING_UTILIZATION_THRESHOLD = 0.85 in engine.ts), the tier-upgrade cost model (15% of base spec cost), and the determinism guarantees (same journal → same city, GR#21).

## 1. Context

The [BUG-590 diagnosis](https://metropolis.local/bow/BUG-590) revealed a **spec-coverage gap**: only 3 of 10 residential specs (`res_estate_compact`, `res_estate`, `res_estate_sprawl`) carry a `capacityTiers` array in `data.ts`. The 7 base specs a player builds early game (`res_hut`, `res_block`, `res_terrace`, `res_lowrise`, `res_midrise`, `res_highrise`, `res_penthouse`) lack one entirely, so `evaluateBuildingMonitors(engine.ts:1058)` hard-skips them (`if (!sp.capacityTiers) continue`), and the monitor is never created at placement (`engine.ts:3045`). Result: a city at tick 1196 with 13 residential buildings at 100% occupancy and £millions in the treasury never auto-scaled, population capped at 400.

Aaron's ruling ([FEAT-2326609740](https://metropolis.local/bow/FEAT-2326609740), Q100076, 2026-09-02): **"A PLUS B" — scale buildings UP (add floors, height recorded on the building) AND OUT (footprint grows, building occupies more tiles), "up then out, rinse and repeat" like the reference title 'Blue' (GR#22). Realistic per-building height limits required (a school cannot be 10 storeys). If no adjacent space for OUT, scale UP-only to the cap; at both caps, full stop with a land-locked notice.**

This spec extends the `capacityTiers` ladder to close that gap and wire the up-then-out mechanics.

## 2. Specs that gain auto-scale ladders

**Residential (10 specs total — 7 new + 3 already scalable):**

| Spec | Unlock | Base capacity | Status | Notes |
|------|--------|---|---|---|
| res_hut | 1 | 8 | new | height cap 3 storeys |
| res_block | 2 | 60 | new | height cap 8 storeys |
| res_terrace | 3 | 30 | new | height cap 3 storeys |
| res_lowrise | 4 | 120 | new | height cap 8 storeys |
| res_midrise | 6 | 280 | new | height cap 15 storeys |
| res_highrise | 9 | 600 | new | height cap 30 storeys |
| res_penthouse | 13 | 350 | new | height cap 30 storeys |
| res_estate_compact | 8 | 900 | exists | height cap 15 storeys |
| res_estate | 10 | 1500 | exists | height cap 20 storeys |
| res_estate_sprawl | 15 | 2500 | exists | height cap 12 storeys |

**Service specs (representative; full scope pending Aaron's Q100083 approval):**

| Spec | Kind | Unlock | Capacity field | Status | Height cap | Notes |
|------|------|--------|---|---|---|---|
| edu_nursery | school | 2 | children (30) | new | 4 storeys | PLACEHOLDER |
| edu_primary | school | 3 | children (300) | new | 4 storeys | PLACEHOLDER |
| edu_city | school | 4 | children (2000) | new | 4 storeys | PLACEHOLDER |
| edu_tech | school | 6 | children (2200) | new | 6 storeys | PLACEHOLDER |
| hea_clinic | health | 2 | served (5000) | new | 6 storeys | PLACEHOLDER |
| hea_hospital | health | 4 | served (40000) | new | 12 storeys | PLACEHOLDER |
| hea_ambulance | health | 5 | served (15000) | new | 4 storeys | PLACEHOLDER |
| hea_eldercare | health | 7 | served (90) | new | 6 storeys | PLACEHOLDER |
| hea_teaching | health | 10 | served (120000) | new | 12 storeys | PLACEHOLDER |
| pol_station | police | 3 | served (10000) | new | 4 storeys | PLACEHOLDER |
| pol_hq | police | 9 | served (60000) | new | 8 storeys | PLACEHOLDER |
| off_suite | office | 2 | jobs (25) | new | 12 storeys | PLACEHOLDER |
| off_tower | office | 4 | jobs (300) | new | 40 storeys | PLACEHOLDER |
| off_data | office | 12 | jobs (90) | new | 40 storeys | PLACEHOLDER |
| off_businesspark | office | 12 | jobs (1200) | exists | 20 storeys | PLACEHOLDER |
| off_towers_downtown | office | 14 | jobs (2000) | exists | 40 storeys | PLACEHOLDER |

⚠️ **PLACEHOLDER height caps — all marked pending Aaron's Q100083 approval.** Directional only; balance pass required. Service-spec scope under review; conservative set included; full set from `data.ts` enumeration to be confirmed.

## 3. Up-Then-Out alternation semantics

### 3.1 Capacity tiers as a ladder

Each spec in the ladders above gets a `capacityTiers: number[]` array (e.g. `[900, 990, 1089, ...]` for 10% increments in the existing estate specs). A building's effective capacity at tier N is `capacityTiers[N]` (or the last tier if N ≥ array length, GR#15 honest cap).

### 3.2 Height and footprint steps

The ladder alternates between two kinds of scale:

- **ODD tiers (1, 3, 5, ...):** SCALE UP — add a storey (height += 1), record new `heightStoreys` field on the building. Footprint (tile w, h) unchanged.
- **EVEN tiers (2, 4, 6, ...):** SCALE OUT — expand footprint by adding adjacent tiles (width and/or height grow by 1 tile in the model). Height unchanged. Building now occupies more grid cells via the same `occupied[x,y]` machinery placement uses.

**Example:** res_block (2×2 footprint at tier 0, 60 residents).
- Tier 0→1: Add a storey (heightStoreys: 1 → 2). Footprint stays 2×2 (4 tiles).
- Tier 1→2: Expand footprint (2×2 → 3×2, 6 tiles now). Height stays 2 storeys.
- Tier 2→3: Add a storey (heightStoreys: 2 → 3). Footprint stays 3×2.
- Tier 3→4: Expand footprint (3×2 → 3×3, 9 tiles). Height stays 3 storeys.
- And so on, alternating up-then-out per Aaron's ruling.

### 3.3 Height recording and persistence

A new field `heightStoreys: number` is added to the Building struct (`types.ts`):
- At placement (tier 0): `heightStoreys` defaults to the spec's implicit base height (measured from `DIMS` z-coordinate if needed, or `undefined` → 1 storey if no base is explicit).
- After each UP step: `heightStoreys` is incremented. Recorded in the state and persisted through save/load/replay.
- At load-time (GR#16): missing `heightStoreys` on a building from an old save defaults to 1 storey (the implicit base). No crash, silent default.

### 3.4 Footprint growth and tile occupation

When a building scales OUT, the footprint grows. **The building must claim real adjacent tiles** through the same `fits()` and `occupied` machinery that placement uses — no overlap, no road/water clobbering.

**Growth direction (deterministic, GR#21):** When scaling OUT, grow as follows (order-independent fold to ensure determinism):
- Prefer expanding width first (w += 1), then height (h += 1), if both have adjacent space.
- If only width OR only height is available, use that.
- If neither is available (fully land-locked), the OUT step is skipped; the building stays at its current footprint. Go to section 3.5 (up-only fallback).

**Fits check:** The new footprint must pass the same `fits()` checks as initial placement — no out-of-bounds, no overlap with existing buildings, no water/protected-tile clobbering per the map rules. If the OUT step fails its fits check, the entire scale event is skipped for that building this pass (rate-limiting via MAX_AUTO_SCALE_UPGRADES_PER_PASS already in engine.ts).

### 3.5 Up-only fallback when land-locked

If a building reaches a tier where OUT is mandated but the building is **fully surrounded / no adjacent tile available**, the scale event **skips the OUT step** and proceeds directly to the **NEXT UP step** if one exists (i.e., if the next odd tier is within the ladder).

**Example:** res_block at tier 4 (footprint 3×3, 9 tiles) with no adjacent space. The next step (tier 5) is an UP step (add storey). Tier 4→5 proceeds as normal UP (heightStoreys increments). Tier 5→6 would be OUT, but there's still no space; the building is capped at tier 5.

**Land-locked notice:** Once a building reaches a tier where **both** height and footprint are at their maximum (heightStoreys >= height cap from §2, tier >= capacityTiers.length - 1), and the building cannot scale further, a **land-locked flag** is set on the building (`scaleLocked: true`, new boolean field added to Building). The monitor for that building is removed (it will never auto-scale again). When the player clicks on the building, a tooltip/notice appears: *"At maximum capacity and surrounded — cannot expand further."* This notice is cleared when the player demolishes adjacent buildings, freeing space.

## 4. Enrolment (monitor creation at placement)

When a building is placed (reducer 'place' action, engine.ts:3045), if its spec has a `capacityTiers` array, a `BuildingMonitor` is created with:
- `buildingId`: the placed building's ID
- `until`: `state.tick + TICKS_PER_YEAR` (1-year window, same as today)
- `type`: 'residents' if spec.residents, else 'jobs' (same as today)

The monitor fires when utilization >= `BUILDING_UTILIZATION_THRESHOLD` (= 0.85, not 1.0 — the existing constant, cited exactly; see AC-7). This rule is **unchanged**; the ladder extension simply opens it to more specs.

## 5. Capex charges and cumulativeCapexSpent increment

When a building auto-scales (tier advances), the engine charges `Math.round(sp.cost * BUILDING_AUTO_SCALE_COST_FRACTION)` (15% of spec cost, existing constant from engine.ts:846). This is a **one-time capital cost per tier step**, labelled in `lastFlows.outflows` (e.g., "Residential Auto-Scale", "School Expansion").

A new `SimState` field is added: `cumulativeCapexSpent: number`.
- Incremented by `placementCost(sp)` every time a building/tile is placed (at placement, tier upgrade, road/rail/bridge placement, etc.).
- Never decremented (refunds/demolitions are separate ledger events, GR#3 conservation).
- Used by future features (e.g., FEAT-dynamic-bailout) to size proportional help offers.
- Persisted through save/load/replay (DebugJSON coverage, AC-11).
- On old-save load (GR#16): if missing, backfilled once at load time by summing `placementCost` for every building present (a proxy for "what it would cost to build today's city"), never zero for a real city, never re-summed on reload.

## 6. Determinism (same journal → same city, GR#21)

The tier-upgrade order is deterministic: `evaluateBuildingMonitors()` sorts active monitors by `buildingId` in strict order (engine.ts:1007, unchanged). Tier steps are applied in that strict order, so two runs from the same genesis + identical input stream produce identical tier progressions and footprint growths, byte-for-byte, matching every existing determinism guarantee.

Footprint growth direction is order-independent (see 3.4): width-first, then height-first is a tiebreaker, so the same building's OUT step always picks the same direction across replays.

## 7. Old-save compatibility (GR#16 sanitise)

Old saves may lack `heightStoreys`, `scaleLocked`, and `cumulativeCapexSpent` fields:

| Field | Missing on load → default |
|---|---|
| `building.heightStoreys` | `1` (the implicit base, one storey) — no crash, silent. |
| `building.scaleLocked` | `false` (building not yet locked; it will be locked on a future scale step if both caps are hit). |
| `SimState.cumulativeCapexSpent` | Backfilled once at load time: `sum(placementCost(SPECS[b.spec]) for all b in buildings)` — a proxy for historic spend. A `capexBackfilled: boolean` flag ensures this sum runs exactly once per load. Never zero for a real city (at least one building exists). |

No crash, no silent zero, no double-backfill on reload. This mirrors the existing optional-field handling (e.g., `firstBailoutCount?`, `recentFundsWindow?`) in `types.ts`.

## 8. DebugJSON coverage

The new fields are included in the debugJSON export (`debugjson.ts`, reducer 'debug-json' action):
- `building.heightStoreys: number` — serialized on every building
- `building.scaleLocked: boolean` — serialized on every building
- `SimState.cumulativeCapexSpent: number` — serialized once at the state level

A developer dumping the sim's debug state can inspect tier progression, height history, and cumulative capex spend. Conservation checks and before-after audit trails use these fields (AC-9 test).

## 9. Conservation through scale-step charges

Every tick the engine scales a building, the charge is recorded as a labelled `outflows` entry in `lastFlows`:

- Label: *"Residential Auto-Scale"*, *"School Expansion"*, *"Office Complex Growth"*, etc. (per-kind labels, matching the pattern of existing category labels in fiscal.ts).
- Amount: the calculated upgrade cost (15% of spec.cost).
- Conservation check: `fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows` holds exactly, including the new auto-scale outflows (same invariant as every other flow, GR#3).

FAILS if a scale-cost is charged outside the flows ledger or appears in `cumulativeCapexSpent` without a matching outflow (invisible charging = a regression of GR#3's class).

## 10. BUG-590 regression case (13 res buildings, sustained 100% occ)

Aaron's original debug capture: 13 residential buildings of types `res_hut(5)`, `res_block(4)`, `res_terrace(4)` at tick 1196, each at 100% occupancy (occ=1.0) for ~1100 ticks, population capped at 400, ample treasury funds, but none auto-scaled (all stayed tier 1-2).

**Post-spec AC:** Replay the same debug journal from the capture:
- Once the cap spec is extended, place new `res_hut(1)` at genesis (or the first buildable tick) and verify that after ~1100+ ticks at 100% occupancy, the building auto-scales from tier 0 → 1 (capacity 8 → ~9).
- Alternatively, drive a synthetic city (any 13 res buildings of base specs at 100% occ) forward tick-by-tick and confirm at least one auto-scales within the window.
- FAILS if no tier progression occurs after sustained 100% occupancy (proves the spec-coverage gap is NOT closed, reverting to BUG-590).

## 11. Service spec auto-scale (representative test)

As an example (full scope per Aaron's Q100083 approval):

- **Schools (edu_primary):** Place one school. Drive the city to a state where children/school-capacity ratio ≥ 0.85 and maintain it. Verify monitor fires and school auto-scales tier 0→1. Confirm tier cost (15% of spec.cost) is charged.
- **Hospitals (hea_hospital):** Place one hospital. Drive served/served-capacity ratio ≥ 0.85. Verify auto-scale fires.
- **Offices (off_tower):** Place office tower. Drive jobs/jobs-capacity ratio ≥ 0.85. Verify auto-scale fires.

FAILS if any service-spec monitor never fires despite sustained high utilization.

## 12. Footprint growth under land constraint (up-only fallback)

**Given:** A 2×2 building (e.g., res_block) in a corner of a small map or surrounded by other buildings, with zero adjacent empty tiles.

**When:** The building reaches tier 1 (after UP step to heightStoreys=2), and is then monitored for tier 2 (which would be OUT step, expand footprint).

**Then:**
- The OUT step's `fits()` check fails (no adjacent tile available).
- The tier 2 scale is skipped; the building remains at tier 1.
- No charge occurs for the failed step.
- The next time the building is evaluated (next pass, if monitor is still active), it attempts the NEXT tier (tier 3, an UP step). If heightStoreys is still below the height cap, tier 3 proceeds as normal UP.
- Eventually, heightStoreys reaches the cap (e.g., res_block height cap = 8 storeys). If tier index also hits the capacityTiers length - 1, the building is marked `scaleLocked = true` and the monitor is removed.

**Alternative:** If the building reaches heightStoreys = cap AND tier < capacityTiers.length - 1, and the NEXT tier would be OUT (which will fail), then attempt to skip to the NEXT UP tier. If no more UP tiers exist before the ladder ends, mark `scaleLocked = true`.

FAILS if the building remains at tier 1 forever (stuck in a failed-OUT loop) or if `heightStoreys` is not advanced when an UP step succeeds.

## 13. Height cap enforcement

**Given:** A res_block with height cap = 8 storeys, currently at heightStoreys = 7.

**When:** The monitor evaluates and identifies the next tier step as UP (e.g., tier 5→6, assuming odd tier = UP).

**Then:**
- Increment heightStoreys to 8.
- Advance tier to 6.
- Charge the upgrade cost.
- On the NEXT evaluation cycle, the next tier (6→7, an OUT step) is attempted. If it succeeds via footprint growth, footprint expands but heightStoreys stays at 8.
- When the next UP step (7→8) is queued, the engine checks: heightStoreys == cap? If so, skip the UP step and jump to the next OUT step (8→9), or mark the building `scaleLocked = true` if no OUT step exists or it also fails.

FAILS if a building ever exceeds its height cap (heightStoreys > cap) or if an UP step is applied after heightStoreys == cap.

## 14. Monitor lifecycle and land-locked notice

**Given:** A building at heightStoreys = cap AND footprint = maximum possible footprint (fully surrounded, cannot expand).

**When:** The building reaches a state where both caps are hit.

**Then:**
- The monitor is removed from `buildingMonitors`.
- A flag `scaleLocked = true` is set on the building (persisted in state).
- When the player's cursor hovers over or selects the building, the UI displays: *"Fully developed — at maximum height and footprint. Demolish adjacent buildings to expand further."* (or similar).
- The notice clears immediately if the player demolishes adjacent buildings, freeing space (the flag remains false; the monitor can be re-added if scale is desired in a future spec).

FAILS if the notice never appears, the monitor persists after both caps are hit, or the flag is set/unset at the wrong time.

## 15. Determinism — Before/After byte identity

**Given:** Two identical genesis saves + identical deterministic input stream (no Date/random, GR#21).

**When:** Both runs advance to a point where buildings auto-scale.

**Then:** The debug-JSON outputs are byte-identical after stableStringify:
- Same buildings array (same IDs, specs, tiers, heightStoreys, scaleLocked flags).
- Same capacityTiers array (same index progression).
- Same `cumulativeCapexSpent` total.
- Same `lastFlows` (same outflow labels and amounts).

FAILS on any divergence (proves non-determinism in tier progression, footprint growth direction, or cost calculation).

## 16. Conservation — Funds and capex spend alignment

**Given:** A city starting at genesis (funds = STARTING_TREASURY, cumulativeCapexSpent = 0).

**When:** Advance one tick where a building is placed (cost = placementCost(spec)) and another building auto-scales (cost = 15% × spec.cost).

**Then:**
- `cumulativeCapexSpent` increases by the placement cost.
- `lastFlows.outflows` contains both the placement cost (as `"Building Placement"` or similar) and the auto-scale upgrade cost.
- `fundsAtTickEnd = fundsAtTickStart + Σinflows − Σoutflows` (including both costs as outflows).
- No funds are credited/charged outside the flows ledger; no capex is counted outside `cumulativeCapexSpent`.

FAILS if funds and capex are out of sync (a silent charge outside the ledger, or an increment of capex without a matching outflow).

---

## Interim Assumptions (Pending Aaron's Approval)

1. **Height cap values (Q100083):** All height caps in §2 are PLACEHOLDERS pending Aaron's row-by-row approval. Directional only; confirm each spec's cap before wiring.

2. **Service spec scope (Q100083):** The full list of service specs in §2 is conservative. Confirm which service specs (schools, health, police, offices, civic, etc.) should gain auto-scale ladders, or if the feature should be residential-only for v1 and service specs deferred to v2.

3. **Footprint growth direction (deterministic):** When a building scales OUT, the algorithm (width-first, then height-first as a tiebreaker) ensures order-independent, deterministic growth. Confirm this is the desired algorithm; alternative: prefer diagonal neighbors first, or alternate between x and y per tier number.

4. **Land-locked fallback:** When OUT fails due to no adjacent space, the building skips the OUT tier and attempts the next tier (which may be UP). Confirm: (a) is skipping the OUT tier the right behaviour, or should the building be hard-capped immediately? (b) should the notice differentiate between "surrounded and can't expand" vs "at maximum capacity"?

5. **Height field persistence:** `heightStoreys` is a transient property of a building and is persisted in state/save/load. Confirm this is the intended storage model, vs a derived property computed from tier index on-the-fly.

6. **Service spec capacity fields:** Schools use `children`, health uses `served`, offices use `jobs`. Confirm the monitor type (residents/jobs/served) for each service spec, or introduce a third `"children"` monitor type if needed.

7. **cumulativeCapexSpent backfill (old saves):** Missing `cumulativeCapexSpent` is backfilled at load as the sum of `placementCost` for existing buildings. Confirm: (a) is this proxy accurate enough, or should old saves be treated as zero (losing historic context)? (b) should the backfill only happen once per save (via a `capexBackfilled` flag), or re-computed on every load?

---

**Docs-only. No build dispatch until Aaron approves height caps (Q100083) and service-spec scope, per GR#25 (cross-module state addition — `heightStoreys`, `scaleLocked`, `cumulativeCapexSpent` — must have edges registered in code.json before implementation prose is acted on).**

