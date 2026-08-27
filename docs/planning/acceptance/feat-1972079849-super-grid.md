# FEAT-1972079849: Super Grid — National High-Capacity Transmission Backbone

**Feature:** Webconsole super-grid infrastructure — national high-capacity transmission layer placed above the local grid, connecting major generation to load centres via a backbone network.

**Mkey:** FEAT-1972079849

## Overview

Extends the power infrastructure system from a single local grid (HV pylons) to a dual-layer model:
1. **Local Grid (localGrid)** — existing HV pylon network, short-range, lower capacity
2. **Super Grid (superGrid)** — new trunk transmission backbone, long-range, higher capacity, costlier to build and maintain
3. **Connection model** — super grid connects to local grid via substations/transformers (new intermediate building type)

The super grid raises the city's total power capacity. Integration with `powerStats()` and `serviceCoverageOf()` maintains the brownout/coverage logic. Migration from saves without super grid is graceful (no grid data = no extra capacity).

---

## Design Decisions Flagged for Lead

### DD1: Super-Grid Building Spec & Footprint
**Is the super grid a new pylon variant, or a distinct structure?**

**Proposed (PLACEHOLDER):**
- New spec in SPECS: `super_pylon` (or `supergrid_node`)
  - **Footprint:** PLACEHOLDER — 40×40 m (like pylon: 30×30 m = 1 tile real-world) or 50×50 m (larger, distinct presence)?
  - **Cost:** PLACEHOLDER — £45,000 (5× local pylon cost ~£9k assumed)
  - **Upkeep:** PLACEHOLDER — £350/tick (50× local pylon ~£7 assumed)
  - **Power capacity:** PLACEHOLDER — 250 MW (vs local pylon PLACEHOLDER ~20 MW)
  - **Kind:** `'power'` (so it participates in `powerStats()` and upkeep bucket)
  - **Unlock:** PLACEHOLDER level (gate construction behind progress)
  - **Dims:** `{ x: 40, y: 40, z: 90 }` (taller visual hint of trunk infrastructure)

**Alternative DD1B (substation model):**
- Keep local pylon as-is; introduce NEW spec `substation` (20×20 m, £5k cost, £30 upkeep, 0 MW) as an optional bridge between layers.
  - Super pylon requires a substation adjacent to operate (one-time conversion cost).
  - Adds gameplay depth but increases complexity.

**Flag for Aaron:** Confirm whether super grid is a simpler direct variant or uses a substation intermediary. Approve spec cost/upkeep/MW values under the balance-number regime.

---

### DD2: Super-Grid Coverage Model — Capacity Contribution
**How much does a super-grid pylon add to city power capacity?**

**Proposed (PLACEHOLDER):**
- `powerStats()` function aggregates MW like today: `cap = Σ(SPECS[b.spec].mw for all power buildings)`
- Super pylons contribute their `.mw` field (e.g., 250 MW) directly to this sum.
- **No range/distance penalty:** super grid is NOT subject to a distance-decay or connection-range model in webconsole (that is engine scope, outside GR#25 webconsole boundary).
- **Brownout improvement:** increasing super-grid capacity directly raises the city's `powerStats().cap`, reducing deficitRatio and improving income via `brownoutOf()`.

**Consequence:** A city with 50 local pylons (PLACEHOLDER 20 MW each = 1000 MW) + 2 super pylons (250 MW each) has 1500 MW total capacity. Coverage = cap / need improves accordingly.

**Test:** Verify that adding a super pylon increases `powerStats().cap` by the pylon's `.mw` value.

**Flag for Aaron:** Confirm the PLACEHOLDER MW per super pylon. Is 250 MW directional? Is there a diminishing-returns curve or a hard cap on total capacity? (E.g., super grid can only add 500 MW max, then overbuilding super pylons wastes money.)

---

### DD3: Super-Grid Rendering in Power Overlay
**How does the Power overlay distinguish super-grid structures?**

**Current state (FEAT-1972079851):**
- POWER_LINES in data.ts defines three classes: localGrid (#9aa4ae grey), superGrid (#e3b341 amber), hvdc (#ff7b72 red).
- MapView Power overlay renders pylon colour based on building spec.
- Today only localGrid exists; superGrid/hvdc are forward-declared but render nothing (no buildings of those kinds exist).

**Proposed:**
- Once `super_pylon` spec is placed, MapView renders its tile with the superGrid colour (#e3b341 amber) instead of localGrid grey.
- **Tower visual:** super pylon should render as a taller tower graphic (z=90 vs pylon z=50) to reinforce hierarchy.
- **Toggle:** Power overlay button already exists (FEAT-1972079851); clicking it shows/hides all power infrastructure colours.

**Test:** Place a super pylon; open Power overlay; verify amber colour appears and localGrid grey pylon colours are visible alongside (or separately if player toggles only super grid).

**Flag for Aaron:** Confirm the amber colour (#e3b341) is final, or propose an alternative. Is tower height visual distinction sufficient, or add visual marks (e.g., "SG" label on hex)?

---

### DD4: Legacy Save Migration — No Super Grid
**What happens when loading a save created before super-grid ships?**

**Proposed (graceful, no-break):**
- `superGridData` field is optional in SimState.
- On load, if absent: city has zero super-grid buildings (graceful null-check in powerStats).
- New super pylons can be built immediately after load (no "conversion" step).
- **No refund/penalty:** existing cash and power capacity are unaffected.

**Consequence:** Pre-super-grid saves continue to work. After load, player may build super pylons freely (assuming budget allows).

**Test:** Save a game before super-grid ships (pre-commit). Load it after shipping. Verify:
1. Game loads without error.
2. City's `powerStats().cap` does not include phantom super-grid MW.
3. Player can place a super pylon (if funds allow).
4. Placing it increases cap and reduces brownout (if any).

**Flag for Aaron:** Confirm this graceful approach vs alternatives (e.g., "legacy mode" disables super grid, "modernize saves" auto-builds super grid as starting infrastructure).

---

### DD5: Super-Grid Fiscal Seam — Appearance in Sankey
**Does building, operating, and expanding super grid appear as distinct line items in the fiscal Sankey?**

**Proposed:**
- **Placement cost:** charged directly when clicking place (today's model).
- **Upkeep:** routes through `computeFlows()` in engine.ts, bucketed under 'Power Grid' outflow (like today's pylon upkeep).
- **Power income:** No new income line (super grid does not directly generate power; it only transmits). Income continues to flow from generation specs (pow_coal, pow_nuke, pow_wind).
- **No import line (unlike HVDC, AC-3 of FEAT-1972079850):** super grid is domestic backbone, not an interconnector.

**Test:** Verify that after placing a super pylon:
1. Ledger shows a negative entry for placement cost.
2. Sankey's 'Power Grid' outflow increases by the pylon's upkeep.
3. No new inflow appears in lastFlows.

**Flag for Aaron:** Confirm that super grid has no revenue model (only cost). If future designs add "power transmission efficiency bonus" or "export capacity", this seam must be extended.

---

## Acceptance Criteria

### AC-1: Super-Grid Spec Registration in SPECS Catalogue
**The `super_pylon` building type exists as a placeable structure in webconsole.**

- **Spec properties:**
  - `id: 'super_pylon'`
  - `kind: 'power'`
  - `name: 'Super Grid Pylon'` (or Aaron-approved alias)
  - `blurb: 'High-capacity trunk transmission node. Connects major generation to regional load centres.'`
  - `w: 40, h: 40` (PLACEHOLDER footprint)
  - `cost: 45000` (PLACEHOLDER in micropounds, from balance regime)
  - `upkeep: 350` (PLACEHOLDER per-tick cost)
  - `color: '#e3b341'` (amber, matches superGrid POWER_LINES colour)
  - `category: 'network'` (like pylon, not a zone)
  - `unlock: PLACEHOLDER` (level gate)
  - `mw: 250` (PLACEHOLDER MW capacity contribution)
  - `dims: { x: 40, y: 40, z: 90 }` (taller than local pylon)
- **Presence:** `super_pylon` entry appears in `SPECS` object in data.ts and is selectable via the build palette.
- **Test:** Verify that `SPECS['super_pylon']` exists and all properties are defined and retrievable.

---

### AC-2: Power Capacity Aggregation Includes Super Grid
**`powerStats()` computes total power capacity including super-grid contributions.**

- **Function signature (unchanged):** `powerStats(s: SimState): { need: number; cap: number }`
- **Logic (UNCHANGED in formula, CHANGED in input):**
  - `cap = Σ(SPECS[b.spec].mw for all buildings b where SPECS[b.spec].kind === 'power')`
  - This sum now includes both local pylons and super pylons (any spec with kind='power' and mw > 0).
- **Determinism:** Given same buildings set, cap is always the same (pure function of state).
- **Test:**
  1. Map with 10 local pylons (assume 20 MW each) = 200 MW cap.
  2. Verify `powerStats().cap === 200`.
  3. Place 1 super pylon (250 MW). Verify `powerStats().cap === 450`.
  4. Bulldoze super pylon. Verify `powerStats().cap === 200` again.

---

### AC-3: Brownout Coverage Reflects Super-Grid Capacity Increase
**When super grid is placed, `brownoutOf()` and `serviceCoverageOf()` improve accordingly.**

- **Mechanism:**
  - `brownoutOf()` calls `powerStats()` internally.
  - If cap increases (via new super pylon), `deficitRatio = 1 - cap/need` decreases.
  - Income penalty factor `incomeFactor = Math.max(0, 1 - deficitRatio * BROWNOUT_INCOME_K)` improves (closer to 1.0).
- **Consequence:** Commercial/industrial/office income (lines 273–276 in engine.ts) scales up if previously brownout-penalized.
- **Determinism:** Same buildings set → same brownout state.
- **Test:**
  1. Build a city: population 2000, industrial 50, offices 30 → need ~450 MW (PLACEHOLDER formula).
  2. Place only local pylons: total 150 MW cap. Verify `brownoutOf().active === true`, `deficitRatio > 0`, income is penalized.
  3. Place 2 super pylons: cap rises to 650 MW. Verify `brownoutOf().active === false`, `deficitRatio === 0`, income multiplier returns to 1.0.

---

### AC-4: Super-Grid Pylon Renders with Distinct Colour & Height
**Super-grid pylons display visually distinct from local pylons in MapView.**

- **Rendering:**
  - Local pylon: MapView renders grey hex (#9aa4ae, localGrid colour from POWER_LINES).
  - Super pylon: MapView renders amber hex (#e3b341, superGrid colour from POWER_LINES).
  - **Height cue:** Super pylon's z=90 (Dims) is higher than local z=50; tile should render a taller tower graphic (stretch/scale visual asset by z-ratio if sprite-based, or adjust SVG viewBox if drawn procedurally).
- **Power overlay toggle:** Power overlay button shows/hides all power infrastructure colours; both local and super grid colours toggle together.
- **Test:**
  1. Place a local pylon on tile (100, 50). Verify grey colour.
  2. Place a super pylon on tile (102, 50). Verify amber colour.
  3. Toggle Power overlay off/on. Verify both colours show/hide together.
  4. Zoom in on super pylon. Verify visual height is noticeably taller than local pylon.

---

### AC-5: Super-Grid Upkeep Routes Through Fiscal Flows
**Super-pylon maintenance cost appears in `lastFlows.outflows` under 'Power Grid' bucket.**

- **Mechanism:**
  - `computeFlows(s)` already collects upkeep by kind into UPKEEP_BUCKET (engine.ts lines 173–195).
  - Super pylon has `kind: 'power'`, so its upkeep is bucketed as `UPKEEP_BUCKET['power'] = 'Power Grid'`.
  - Outflows list includes a 'Power Grid' entry = Σ upkeep for all online power buildings (local + super pylons).
- **Determinism:** Same online buildings → same upkeep sum.
- **Test:**
  1. Place 1 local pylon (upkeep ~£7) + 1 super pylon (upkeep 350). Verify 'Power Grid' outflow = ~357.
  2. Run 5 ticks. Verify 'Power Grid' outflow remains ~357 (no ramping or decay).
  3. Bulldoze super pylon. Verify outflow drops to ~7.

---

### AC-6: Placement Cost Deducted Immediately (No Building Progress)
**Super-pylon placement is instant (no construction ticks).**

- **Rule:** Unlike residential/service buildings, network infrastructure (roads, pylons, super pylons) is placed instantly.
- **Cost model:** Placement cost is charged immediately when the player clicks place (via `placementCost()` in data.ts).
- **Test:**
  1. Record funds. Place a super pylon (cost 45,000). Verify funds drop by exactly 45,000.
  2. Record tick. Place super pylon. Verify next tick's `advance()` does NOT add construction time (pylon is online immediately).

---

### AC-7: Legacy Save Compatibility — No Super-Grid Data Loss
**Opening a save created before super-grid ships loads successfully with no errors or missing super-grid MW.**

- **Migration:** SimState has no mandatory `superGridData` field; super grid is a pure spec-based model (check SPECS catalogue, count super_pylon buildings).
- **Load test:**
  1. Create a game save in the pre-super-grid codebase.
  2. After super-grid ships, load the save.
  3. Verify game loads without crash or console error.
  4. Verify city's `powerStats().cap` is correct for the loaded buildings (no phantom super-grid MW).
  5. Verify player can build super pylons (if super-grid unlock level is met).
  6. Verify saving and re-loading the modified game works (round-trip).

---

### AC-8: Super-Grid Determinism — Same Buildings, Same Capacity
**Placing super pylons in any order produces identical power stats.**

- **Invariant:** Given a fixed set of placed buildings (local pylons, super pylons, generators, loads), `powerStats()` always returns the same need/cap, regardless of the order in which buildings were placed.
- **Test:**
  1. Game A: place 5 local pylons, then 2 super pylons.
  2. Game B: place 2 super pylons, then 5 local pylons.
  3. At same tick, both games should have identical `powerStats()` output.
  4. Verify brownout state, income penalty, and Sankey are byte-identical.

---

### AC-9: Forward Compatibility — HVDC Can Cross Super-Grid Backbone
**No blocking dependencies: placing HVDC (FEAT-1972079850) does not require super grid; super grid does not block HVDC.**

- **Scope:** Super grid and HVDC are independent; each can exist without the other.
- **Future: Integration path (documented, not enforced):** If a future design requires HVDC to feed INTO the super-grid backbone for onward transmission, AC-9 is the foundation (both exist as power specs in the catalogue).
- **Test:** Verify that placing super grid does not block HVDC placement (and vice versa).

---

## Structural Notes

**GR#25 Compliance:** All AC claims stay within webconsole scope (data.ts, engine.ts, MapView.tsx, types.ts). No engine.power or internal/engine modifications required — super grid is purely a catalogued building type participating in existing power functions (powerStats, serviceCoverageOf, computeFlows). The balance-number regime (Aaron's rules) governs all PLACEHOLDER costs/capacities.

**POWER_LINES forward-declaration:** The superGrid colour and label in POWER_LINES (data.ts) were already present (FEAT-1972079851), forward-declaring this feature. Once `super_pylon` spec ships, that declaration becomes real.

**Conservation & Sankey:** Super-grid upkeep routes through computeFlows like all other power building upkeep. Sankey renders it as part of the 'Power Grid' bucket with no new line items required.
