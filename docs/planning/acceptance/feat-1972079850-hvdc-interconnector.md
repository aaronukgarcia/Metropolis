# FEAT-1972079850: HVDC Interconnector — Power Import/Export to France

**Feature:** Webconsole HVDC converter station — coastal building enabling power import from and export to the French market, with fiscal flow seam via lastFlows.

**Mkey:** FEAT-1972079850

## Overview

Adds a new power infrastructure type: a converter station that links the city's power grid to an external market (France). It:
1. **Buys power** (import) when domestic capacity is insufficient or import is cheaper than generation.
2. **Sells power** (export) when domestic demand is low and export price is profitable.
3. **Appears in fiscal flows** as 'Power Import' (outflow) and 'Power Export' (inflow) so the Sankey shows money flowing to/from France.
4. **Contributes to power capacity** like other power buildings (via powerStats integration).
5. **Coastal constraint** (PLACEHOLDER: must be placed within a range of water/harbour tiles, or flagged as a constraint for future engine work).

Integration with `powerStats()`, `brownoutOf()`, and `lastFlows` keeps the brownout/conservation logic coherent. HVDC can coexist with super grid (FEAT-1972079849) with no ordering dependency.

---

## Design Decisions Flagged for Lead

### DD1: HVDC Building Spec & Footprint
**What is the physical structure and game footprint?**

**Proposed (PLACEHOLDER):**
- New spec in SPECS: `hvdc_converter`
  - **Footprint:** PLACEHOLDER — 50×50 m (larger than pylon, indicates industrial significance)
  - **Cost:** PLACEHOLDER — £150,000 (3× super pylon, 16× local pylon; high capital investment)
  - **Upkeep:** PLACEHOLDER — £500/tick (complex machinery)
  - **Power capacity:** PLACEHOLDER — 200 MW (import/export rate limit, distinct from super pylon; see AC-4)
  - **Kind:** `'power'` (participates in upkeep bucket and powerStats)
  - **Unlock:** PLACEHOLDER level (higher than super grid; requires technology/infrastructure prerequisites)
  - **Coastal constraint:** PLACEHOLDER — must be placed orthogonally adjacent to a water tile (wat_clean, wat_waste, or landing-stage kind). Flagged as DESIGN-DECISION; enforcement is mechanical in placement validation.
  - **Dims:** `{ x: 50, y: 50, z: 40 }` (mid-height; transformer/substation scale)

**Flag for Aaron:** Confirm spec cost, upkeep, capacity (200 MW), unlock level. Should coastal constraint be mandatory (mechanical block) or soft (warning only)? IFA/ElecLink precedent is coastal France ↔ Kent; is the game's location Folkestone (coastal) always, or can player build inland? If inland-possible, should HVDC placement auto-fail or warn?

---

### DD2: Import & Export Pricing Model — Aaron Economic Ruling
**At what price does the city import/export power?**

**Proposed (PLACEHOLDER & flagged as an Aaron economic ruling):**
- **Import price (outflow):** PLACEHOLDER — £X per MWh (constant, or dynamic based on city demand?).
  - Trigger: automatic when `powerStats().need > powerStats().cap` (brownout).
  - Volume: PLACEHOLDER — up to `hvdc.capacity` MW, but capped to meet unmet need (don't over-import).
  - Formula: `imported_MW = Math.min(hvdc.capacity, Math.max(0, need - cap))`.
  - Cost per tick: `import_cost = imported_MW * import_price_per_mwh / ticks_per_hour`.
  - Ledger entry: creates 'Power Import' outflow in `lastFlows`.
- **Export price (inflow):** PLACEHOLDER — £Y per MWh (must be < generation cost for player to profit, or = import price for arbitrage).
  - Trigger: automatic when `powerStats().cap > powerStats().need` (surplus).
  - Volume: PLACEHOLDER — up to `hvdc.capacity` MW, but capped to available surplus.
  - Formula: `exported_MW = Math.min(hvdc.capacity, Math.max(0, cap - need))`.
  - Revenue per tick: `export_revenue = exported_MW * export_price_per_mwh / ticks_per_hour`.
  - Ledger entry: creates 'Power Export' inflow in `lastFlows`.

**Consequence:** 
- A city under brownout imports at a cost, improving power balance but draining funds.
- A city with surplus capacity exports at a profit, providing a new revenue stream.
- Import/export are **mutually exclusive per tick** (can't both happen simultaneously; depends on need vs cap at that tick).

**Balance considerations (Aaron's domain):**
- If import price = export price (parity), player gains no arbitrage advantage; HVDC is a cost/relief tool.
- If import price > export price (e.g., import £300/MWh, export £80/MWh), city loses money importing and makes little exporting (defensive tool).
- If import price is very high and export very low, HVDC becomes a last-resort brownout relief, not a profit engine.

**Flag for Aaron:** Determine import and export prices (£/MWh), decide if prices are static or dynamic (based on time of year / game events / AI market), confirm volume cap (= hvdc.capacity or a separate max-import limit?), and flag whether import/export are mutually exclusive or can both occur at reduced rates.

---

### DD3: Capacity Contributions & Brownout Interaction
**Does HVDC's "capacity" count toward power supply, or is it separate?**

**Proposed (PLACEHOLDER):**
- **HVDC capacity participates in `powerStats().cap`:** when computing total power supply, include `hvdc.capacity` like any other power building.
  - Example: city with 500 MW local capacity + 200 MW super grid + 200 MW HVDC = 900 MW total.
  - Consequence: a brownout can be solved by building HVDC, reducing need to import (self-relieving via capacity, not just cost).
- **OR: HVDC capacity is separate (NOT in powerStats):** HVDC is purely a fiscal market link, not a power supply contributor.
  - Consequence: HVDC only relieves brownout via import (funds cost), not by adding supply (cap increase).

**Recommended:** HVDC capacity counts toward cap (simpler, more coherent with other power specs). This avoids a special case in brownout logic.

**Alternative DD3B (split model):**
- HVDC physical converter has a small capacity (e.g., 50 MW) that counts toward cap (transmission lines have real capacity).
- Import/export volumes are capped by a separate contract rate (e.g., 200 MW max import/export, independent of converter capacity).
- More realistic but adds complexity.

**Flag for Aaron:** Confirm whether HVDC capacity is included in `powerStats().cap` and whether that improved supply directly reduces brownout, or if HVDC is purely a market tool (fiscal only, not supply-side).

---

### DD4: HVDC Rendering in Power Overlay
**How does Power overlay distinguish HVDC from other power infrastructure?**

**Current state (FEAT-1972079851):**
- POWER_LINES defines hvdc colour: #ff7b72 (red, for long-distance DC link).
- MapView Power overlay renders pylon tiles by their building's POWER_LINES colour.

**Proposed:**
- HVDC converter renders with hvdc red colour (#ff7b72) in Power overlay.
- Distinct from localGrid grey and superGrid amber.
- **Toggle:** Power overlay button shows/hides all three power classes together (or future: separate toggles per class).

**Test:** Place HVDC on coastal tile (100, 50). Open Power overlay. Verify red colour appears and is distinct from local/super grid colours.

**Flag for Aaron:** Confirm red (#ff7b72) is final. Should HVDC render a distinct visual (e.g., "HVDC" label, transformer icon) to reinforce its role as an interconnector, vs a simple colour change?

---

### DD5: Coastal Placement Constraint
**Where can HVDC converter stations be placed?**

**Proposed (PLACEHOLDER enforcement model):**
- **Hard constraint (mechanical block):** HVDC must be orthogonally adjacent to at least one water tile (wat_clean, wat_waste, or future 'sea' tile if added).
  - Validation in `canPlace()`: before allowing placement, scan N/S/E/W neighbours. If any is water kind, allow; else reject with message "HVDC must be coastal".
  - Consequence: HVDC is locked to player's coastline, ensuring game-world coherence (physically realistic).
- **OR: Soft constraint (warning only):** HVDC can be placed inland but shows a warning tooltip. No mechanical block.
  - More flexible for playtesting but allows nonsensical inland placement.

**Recommended:** Hard constraint (mechanical block). IFA/ElecLink are coastal by geography; inland HVDC makes no sense.

**Implementation:** New helper `isCoastal(state: SimState, tile: {x, y}): boolean` checks orthogonal neighbours for water kind.

**Test:**
  1. Tile (100, 50): water to the north. Place HVDC at (100, 50). Verify success.
  2. Tile (200, 200): no water neighbours. Place HVDC. Verify reject with "must be coastal" message.

**Flag for Aaron:** Approve hard mechanical constraint vs soft warning. If game map has no water tiles or is fully inland (not Folkestone simulation), revert to soft warning or remove constraint entirely.

---

### DD6: Coastal Constraint Interaction with Super Grid & Local Grid
**Can HVDC connect inland via super-grid backbone?**

**Proposed (OUT OF SCOPE for webconsole ACs, documented for future):**
- Webconsole does NOT enforce long-distance transmission physics (that is engine scope).
- HVDC can be placed if coastal (local constraint only); whether it "connects" to inland grids is an engine-layer question (beyond GR#25 webconsole boundary).
- **Implication:** HVDC at the coast is always "wired"; player does not need to build transmission lines from coast to interior.
- **Future (engine): HVDC may require super-grid connection or point-to-point linking to function; that is a future design decision.**

**For now:** Accept any coastal HVDC as functional (no engine connectivity check in webconsole).

**Flag for Aaron:** If HVDC should only work when connected to super-grid backbone (future gameplay), that is an engine feature; webconsole ACs do not require it. Document in the engine.power spec.

---

## Acceptance Criteria

### AC-1: HVDC Converter Spec Registration in SPECS Catalogue
**The `hvdc_converter` building type exists and is placeable.**

- **Spec properties:**
  - `id: 'hvdc_converter'`
  - `kind: 'power'`
  - `name: 'HVDC Converter Station'` (or Aaron-approved alias)
  - `blurb: 'High-voltage direct current link to continental Europe. Import power during shortage, export surplus for revenue.'`
  - `w: 50, h: 50` (PLACEHOLDER footprint)
  - `cost: 150000` (PLACEHOLDER in micropounds)
  - `upkeep: 500` (PLACEHOLDER per-tick cost)
  - `color: '#ff7b72'` (red, matches hvdc POWER_LINES colour)
  - `category: 'network'`
  - `unlock: PLACEHOLDER` (level gate, higher than super grid)
  - `mw: 200` (PLACEHOLDER MW capacity, distinct meaning: see AC-4)
  - `dims: { x: 50, y: 50, z: 40 }` (transformer/substation scale)
- **Presence:** `hvdc_converter` entry exists in SPECS in data.ts and is selectable via palette.
- **Test:** Verify `SPECS['hvdc_converter']` and all properties are defined and retrievable.

---

### AC-2: HVDC Coastal Placement Constraint
**HVDC converter can only be placed on tiles orthogonally adjacent to water.**

- **Validation rule:** Before allowing placement at tile (x, y), check all four orthogonal neighbours (x±1, y±0 and x±0, y±1).
- **Water kinds:** A neighbour counts as water if its building's spec has `kind === 'water'` (includes wat_clean, wat_waste, or future 'sea' building type).
- **Rejection:** If no water neighbours found, placement fails with user-facing message: "HVDC converter station must be placed adjacent to water (coastal location required)."
- **Mechanical enforcement:** The `canPlace()` function in engine.ts (or MapView placement handler) performs this check before allowing the placement action.
- **Test:**
  1. Map with water building at (100, 50). Place HVDC at (100, 51) (south of water). Verify success.
  2. Map with no water. Place HVDC at (50, 50). Verify rejection with message.
  3. Place water at (50, 50). Nudge player's cursor near it. Place HVDC. Verify success.

---

### AC-3: Power Import Flow — Automatic When Brownout Active
**When city is in brownout, HVDC automatically imports power and records cost in `lastFlows.outflows`.**

- **Trigger:** Each tick, after `computeFlows()` checks `brownoutOf(s)`:
  - If `brownout.active === true` AND HVDC spec exists on the map (at least one `hvdc_converter` building online):
    - Compute unmet need: `unmet_mw = Math.max(0, powerStats().need - powerStats().cap)`.
    - Import volume: `import_mw = Math.min(hvdc_total_capacity, unmet_mw)` (sum capacity of all online HVDC converters).
- **Cost calculation (PLACEHOLDER formula):**
  - `import_cost = import_mw * import_price_per_mwh / TICKS_PER_HOUR`.
  - `import_price_per_mwh` is PLACEHOLDER (Aaron's ruling).
  - Example: if import price = £300/MWh, HVDC capacity 200 MW, and full import occurs: cost = 200 * 300 / 60 = £1000/tick.
- **Ledger entry:** Add to `lastFlows.outflows`: `{ label: 'Power Import', value: import_cost }` (positive outflow, money leaving city).
- **Conservation:** import_cost is included in totalOut when verifying funds conservation (BUG-406 check).
- **Determinism:** Given same buildings, same brownout, same prices, import cost is always identical.
- **Test:**
  1. City with 500 MW need, 200 MW cap (brownout active). Place 1 HVDC (200 MW capacity).
  2. Compute: unmet = 300 MW, import volume = min(200, 300) = 200 MW.
  3. Verify 'Power Import' appears in lastFlows.outflows with the expected cost.
  4. Save/load. Verify import_cost is stable (deterministic).

---

### AC-4: Power Export Flow — Automatic When Surplus Capacity
**When city has surplus power capacity, HVDC automatically exports and records revenue in `lastFlows.inflows`.**

- **Trigger:** Each tick, after `brownoutOf()`:
  - If `brownout.active === false` (no brownout, cap ≥ need) AND HVDC exists:
    - Compute surplus: `surplus_mw = Math.max(0, powerStats().cap - powerStats().need)`.
    - Export volume: `export_mw = Math.min(hvdc_total_capacity, surplus_mw)` (sum capacity of all online HVDC converters).
    - **Exception:** Do not export if import is already occurring (mutually exclusive: one per tick).
- **Revenue calculation (PLACEHOLDER formula):**
  - `export_revenue = export_mw * export_price_per_mwh / TICKS_PER_HOUR`.
  - `export_price_per_mwh` is PLACEHOLDER (Aaron's ruling; typically < import price).
  - Example: if export price = £80/MWh, export 150 MW: revenue = 150 * 80 / 60 = £200/tick.
- **Ledger entry:** Add to `lastFlows.inflows`: `{ label: 'Power Export', value: export_revenue }` (inflow, money entering city).
- **Conservation:** export_revenue is included in totalIn.
- **Determinism:** Same input, same output.
- **Test:**
  1. City with 500 MW need, 800 MW cap (no brownout, surplus = 300 MW). Place 1 HVDC (200 MW capacity).
  2. Compute: surplus = 300 MW, export volume = 200 MW.
  3. Verify 'Power Export' appears in lastFlows.inflows with expected revenue.
  4. Verify import does NOT appear (mutually exclusive).
  5. Reduce capacity to 400 MW cap (now in brownout). Verify 'Power Export' disappears, 'Power Import' appears.

---

### AC-5: HVDC Capacity Aggregation in `powerStats()`
**HVDC converters' `.mw` field contributes to total power capacity.**

- **Formula:** `powerStats().cap = Σ(SPECS[b.spec].mw for all power buildings including hvdc_converter)`.
- **Consequence:** An HVDC converter (200 MW capacity) raises city's `powerStats().cap` by 200 MW immediately upon placement.
- **Brownout improvement:** If previously in brownout, the added capacity may move city out of brownout in the same tick (or reduce deficitRatio).
- **Test:**
  1. City: 500 MW need, 400 MW cap (brownout: deficitRatio = 0.2). Place HVDC (200 MW).
  2. Verify `powerStats().cap === 600 MW`.
  3. Verify `brownoutOf().active === false` (no longer in brownout).
  4. Verify income penalties are removed (brownout.incomeFactor back to 1.0).

---

### AC-6: HVDC Upkeep Routes Through Fiscal Flows
**HVDC converter upkeep appears in `lastFlows.outflows` under 'Power Grid' bucket.**

- **Mechanism:** `computeFlows()` collects upkeep by kind via UPKEEP_BUCKET.
- **HVDC kind:** `'power'`, so upkeep is bucketed as `'Power Grid'` (same bucket as pylons, super pylons).
- **Test:**
  1. Place 1 local pylon (upkeep ~7) + 1 super pylon (upkeep 350) + 1 HVDC (upkeep 500).
  2. Verify 'Power Grid' outflow = ~857.
  3. Run 10 ticks. Verify stable ~857/tick.
  4. Bulldoze HVDC. Verify outflow drops to ~357.

---

### AC-7: HVDC Rendering in Power Overlay — Red Colour
**HVDC converter renders with hvdc colour (#ff7b72 red) in Power overlay.**

- **Overlay state:** When Power overlay is active (toggle on), HVDC tiles render red; local grid pylons render grey; super-grid render amber.
- **MapView rendering:** Power overlay layer reads building's spec → looks up spec.kind and spec.id → checks POWER_LINES to find colour → applies to hex.
  - For hvdc_converter, match against POWER_LINES entry with `id: 'hvdc'` → colour: '#ff7b72'.
- **Test:**
  1. Place 1 local pylon, 1 super pylon, 1 HVDC on the map.
  2. Open Power overlay. Verify three distinct colours (grey, amber, red).
  3. Close Power overlay. Verify colours disappear.
  4. Re-open. Verify colours reappear and are stable.

---

### AC-8: Import/Export Mutual Exclusivity — One Per Tick
**Each tick, HVDC either imports OR exports power, not both.**

- **Rule:** Compute brownout state once. If active, import; if not, export (if surplus exists). Never both in the same tick.
- **Implementation:**
  - After `brownoutOf()` check, if active: append import flow to outflows.
  - Else if surplus > 0: append export flow to inflows.
  - Both conditions are mutually exclusive in the if-chain.
- **Test:**
  1. Tick T1: brownout active. Verify import flow in outflows, no export flow.
  2. Tick T2: brownout clears (capacity increased). Verify export flow in inflows, no import.
  3. Tick T3: brownout returns. Verify import returns, export gone.

---

### AC-9: Import/Export Capped by HVDC Total Capacity
**Import and export volumes cannot exceed the sum of all online HVDC converters' `.mw` capacity.**

- **Formula:**
  - `hvdc_total_mw = Σ(SPECS[b.spec].mw for all online b where b.spec === 'hvdc_converter')`.
  - `import_volume = min(hvdc_total_mw, unmet_mw)`.
  - `export_volume = min(hvdc_total_mw, surplus_mw)`.
- **Consequence:** If unmet need is 500 MW but HVDC capacity is only 200 MW, import is capped at 200 MW (remaining 300 MW need is unmet; brownout continues but less severe).
- **Test:**
  1. Unmet need: 500 MW. Place 1 HVDC (200 MW capacity). Verify import = 200 MW, cost = 200 * import_price / ticks_per_hour.
  2. Place 2nd HVDC (200 MW each, total 400 MW capacity). Verify import = 400 MW (up to unmet need).
  3. Bulldoze both. Verify import = 0 (no HVDC, no import).

---

### AC-10: HVDC Placement Cost Deducted Immediately
**Placing HVDC converter charges placement cost immediately; structure is online the same tick (no construction delay).**

- **Cost model:** Like other network infrastructure (roads, pylons), HVDC is instant.
- **Test:**
  1. Record funds: F1. Place HVDC (cost 150,000). Verify funds = F1 - 150,000 immediately.
  2. Verify no construction tick counter (pylon is online at tick T, not at tick T+N).

---

### AC-11: Legacy Save Compatibility — No HVDC Data Loss
**Opening a save created before HVDC ships loads successfully; no phantom import/export flows appear.**

- **Migration:** Like super grid, HVDC is spec-based (check SPECS for hvdc_converter buildings).
- **Test:**
  1. Create save pre-HVDC. Load post-HVDC. Verify load succeeds, no error.
  2. Verify no 'Power Import' or 'Power Export' flows in lastFlows (no HVDC placed, so no flows).
  3. Place an HVDC in the loaded save. Verify import/export flows appear on the next brownout/surplus tick.

---

### AC-12: Sankey Fiscal Seam — Import/Export Appear as Distinct Line Items
**'Power Import' and 'Power Export' flow items appear in the Sankey diagram.**

- **Sankey source/sink nodes:**
  - 'Power Export' (if present) appears as an inflow source node (green/done colour).
  - 'Power Import' (if present) appears as an outflow sink node (red/danger colour).
  - Both route through the Treasury (centre node).
- **Conservative check (BUG-406):** Import/export flows are included in the conservation check (fundsAtEnd = fundsAtStart + inflows - outflows).
- **Test:**
  1. City in brownout with HVDC. Open Sankey. Verify 'Power Import' appears as an outflow.
  2. City with surplus + HVDC. Verify 'Power Export' appears as an inflow.
  3. Save game. Verify next load's Sankey is identical (deterministic).

---

### AC-13: Determinism — Same Conditions, Same Flows
**Given fixed HVDC placement, prices, and power statistics, import/export flows are identical across runs.**

- **Invariant:** Same tick, same buildings, same powerStats(), same brownout state → same import_cost or export_revenue.
- **Test:**
  1. Game A: reach tick 100 with specific buildings and HVDC placement.
  2. Game B: identical scenario (same buildings, same seed or replay). Reach tick 100.
  3. Verify `lastFlows.inflows` and `lastFlows.outflows` are byte-identical in both games (including import/export values).

---

### AC-14: Forward Compatibility — HVDC ∩ Super Grid Have No Blocking Dependencies
**Placing HVDC does not require super grid; placing super grid does not require HVDC.**

- **Independence:** Both are power specs in SPECS. Neither has a prerequisite dependency on the other.
- **Test:**
  1. Place HVDC without building any super grid. Verify it functions (imports/exports as needed).
  2. Place super grid without HVDC. Verify capacity improves independently.
  3. Place both. Verify they coexist and both contribute to power capacity and fiscal flows.

---

## Structural Notes

**GR#25 Compliance:** All claims stay within webconsole scope (data.ts, engine.ts, types.ts, MapView.tsx, Sankey.tsx). No engine modifications required. HVDC is a catalogued building participating in existing functions (powerStats, brownoutOf, computeFlows, serviceCoverageOf). Coastal constraint is checked locally in `canPlace()` validation; no engine cross-module dependency.

**POWER_LINES forward-declaration:** The hvdc colour and label in POWER_LINES were already present (FEAT-1972079851). Once `hvdc_converter` spec ships and import/export flows are added to computeFlows, the forward-declaration becomes real.

**Conservation & Sankey:** Import and export flows route through `lastFlows`, so Sankey renders them correctly and conservation checks (BUG-406, AC-12) include them.

**Balance-number regime:** All PLACEHOLDER values (cost, upkeep, capacity, import price, export price, coastal constraint enforcement model) are flagged for Aaron's row-by-row balance pass. The proportionality tier (GR#23) applies: code-bearing commit on import/export logic needs a Destructive verdict.

**Mutual exclusivity note:** AC-8 enforces one import/export per tick. If future design requires simultaneous import and export (e.g., two separate HVDC converters, one importing, one exporting), this AC would need revision. For MVP, simpler is better.
