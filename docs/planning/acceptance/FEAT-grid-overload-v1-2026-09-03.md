# FEAT: Grid Overload v1 — Power Line Capacity Saturation & Brownout Consequence

**Ruling:** Q100090 (Aaron approved).  
**Rationale:** FEAT-2326609741 (Grid transmission capacity) — the power grid currently permits unlimited generation supply as long as local capacity exists, with no saturation feedback on the wires themselves. Baseline One needs power lines to OVERLOAD like roads do, so a big plant (e.g. 800 MW nuclear) without adequate transmission capacity to carry it to demand causes grid saturation and brownout even though generation capacity is plentiful.

---

## Problem Statement

Today:
- Power generation (diesel gensets, nuclear, solar, wind, etc.) and demand (consumption, services) are balanced in `brownoutOf()` as `cap >= need` on the aggregated total — a generator's output either flows to demand or triggers a deficit brownout.
- **But:** transmission capacity (pylon/cable tiles and their MW/tile specs) do not constrain the flow. A 1,000 MW nuclear plant connected via a single 10-MW cable tile will deliver its full 1,000 MW as long as local storage/generation capacity exists, with zero saturation check on the wire carrying it.
- Aaron's verdict: power lines must have per-tile MW capacity (like road tier-capacity for traffic) and must impose a **real brownout consequence** when saturated, forcing the player to build adequate transmission or face an overload-triggered shortage even with ample generation.

---

## Solution Design

### 1. Define Power Line Classes & Capacity Registry

Power lines are modelled as two line classes (mirroring road/motorway tiers but for power networks), each with MW/tile specs:

- **Local Grid (distribution):** low-voltage distribution lines (e.g. underground cables, local feeders) — lower cost, lower capacity per tile. ⚠️ PLACEHOLDER MW/tile `= 20 MW` per local-distribution line tile.
- **Super-Grid (transmission):** high-voltage trunk lines (pylons, transmission cables, interconnects) — higher cost, higher capacity per tile. ⚠️ PLACEHOLDER MW/tile `= 100 MW` per super-grid transmission line tile.

**Data source:** Power line specs (pylon/cable variants) must be registered in `data/buildings.json` with:
- A `powerLineClass` field naming "local" or "super" (spec-level classification, not runtime).
- A `lineCapacityMW` field giving MW/tile (spec-level constant).
- Consumption/cost/appeal/catalogue fields matching the road-line pattern.

**Determinism (GR#21):** Line classes are a static fact of the spec, derived at load-time, never computed per tick. No early breaks in iteration.

### 2. Flow Apportionment: Generation → Transmission → Demand

Power flow is deterministically apportioned across line classes by their **capacity share**, exactly as `lineUsageOf()` apportions traffic by road capacity:

- **Total generation:** the sum of all online generation (plants that are ONLINE, not under-construction, not abandoned).
- **Total demand:** the sum of consumption and service demand (same `powerStats()` need basis that `brownoutOf()` uses).
- **Line capacity by class:** sum of MW capacity across all "super" tiles + sum across all "local" tiles.
- **Flow apportionment:** if generation > demand (surplus, gridExportOn may capture it) or generation < demand (shortage), the deficit (or surplus) is apportioned **proportionally to each line class's capacity share**, not spread evenly.
  - E.g.: Super-grid 500 MW capacity, Local-grid 100 MW capacity, total 600 MW. A 300 MW shortfall apportions as: Super-grid carries (500/600) × 300 = 250 MW shortage pressure, Local-grid carries (100/600) × 300 = 50 MW shortage pressure.
  - **Order-independence:** line specs are iterated in spec-id order (same deterministic sort as `lineUsageOf`), and the sum is order-independent; no early breaks.

### 3. Saturation & Sustained-Overload Counter

**Saturation ratio per line class:**
- For each line class (local/super), saturation = actual flow / total capacity for that class.
- Saturation ∈ [0, 1] by construction (clamped).

**Sustained-overload counter:**
- Each line class tracks a sustained-overload tick counter (separate from the road congestion counter) — e.g., `powerLineOverloadTicksByClass: { "local": 0, "super": 45 }`.
- **Hard reset rule (like Q100077):** if a line class drops below OVERLOAD_PENALTY_THRESHOLD this tick, its counter resets to 0 immediately (no hysteresis).
- **Increment:** if saturation ≥ OVERLOAD_PENALTY_THRESHOLD, the counter increments (capped at OVERLOAD_SUSTAINED_TICKS, like congestion).

**Constants (PLACEHOLDER balance):**
- `OVERLOAD_PENALTY_THRESHOLD` = ⚠️ PLACEHOLDER `0.80` (bites when lines are quite saturated, close to max; can tune downward if desired).
- `OVERLOAD_SUSTAINED_TICKS` = ⚠️ PLACEHOLDER `60` (same as congestion — 2 game-months for feel).

### 4. Overload-Brownout Consequence Through isBrownoutActive SSOT

**Key rule (AC-17 lesson from FEAT-2326609711):** there is ONE `isBrownoutActive()` predicate that gates ALL brownout consequences (income penalty, wellbeing Utilities collapse, the brownout banner). Grid overload must compose with the existing deficit brownout, not conflict.

**Two independent brownout triggers:**

1. **Deficit brownout (existing):** `brownoutOf(s).active && !gridImportOn` — generation < demand even with imports disabled.
2. **Overload brownout (new):** any line class sustained-overload is active — some transmission class has been saturated for ≥ OVERLOAD_SUSTAINED_TICKS.

**Updated isBrownoutActive() composition:**
```
isBrownoutActive(s) := (brownoutOf(s).active && !gridImportOn) OR (ANY line class overloadBrownoutActive(s))
```

**Consequence:** once isBrownoutActive() returns true for ANY reason, the city-wide brownout consequences apply:
- Income penalty via `incomeFactor = 1.0 - (deficitRatio * BROWNOUT_INCOME_K)` (existing deficit logic).
- Wellbeing Utilities part collapses (existing deficit logic).
- DemandDock brownout banner + escalated alerts (existing UI logic).

**Critical insight:** the penalty is the SAME whether the brownout is caused by a shortage or by transmission overload. The player sees "brownout active" and must diagnose why (F3 power panel) — if it's overload despite ample generation, the solution is to build transmission; if it's deficit, the solution is to generate more or import more.

### 5. Zero-Penalty When Under Threshold

**When all line classes have saturation < OVERLOAD_PENALTY_THRESHOLD:**
- All overload counters = 0.
- No line class overload-brownout is active.
- `isBrownoutActive()` depends ONLY on deficit brownout (existing).

**Assumption:** a healthy-margin city with 70% transmission saturation has no overload issue; only sustained extremes (80%+) trigger consequences.

---

## Acceptance Criteria

### AC-1: Line Class Specs with MW Capacity Are Registered
**Given:** Pylon/cable building specs exist in `data/buildings.json` (e.g. "pylon_standard", "cable_underground_hv").  
**When:** Each spec is loaded.  
**Then:** It carries a `powerLineClass` field (value "local" or "super") and a `lineCapacityMW` field (a positive number, e.g. 20 or 100), matching the house spec-registration pattern.  
**Failing scenario:** A pylon spec has no `powerLineClass`, or `lineCapacityMW` is absent, NaN, or negative.

### AC-2: Power Flow Apportionment by Line Class Capacity Share Is Deterministic
**Given:** A city with super-grid (500 MW total capacity) and local-grid (100 MW total capacity) tiles; generation = 300 MW, demand = 250 MW.  
**When:** The monthly tick advances.  
**Then:** Flow is deterministically apportioned by capacity share (super-grid carries 250/300×(500/600) MW toward the generation, local-grid the remainder), with byte-identical results across replays (spec-id-sorted iteration, no early breaks, order-independent aggregate).  
**Failing scenario:** Two identical saves compute different flow apportionments, or iteration order affects the distribution.

### AC-3: Saturation Ratio Per Line Class is Computed & Bounded [0, 1]
**Given:** A line class with 100 MW capacity and 95 MW flow.  
**When:** `powerLineSaturationOf()` is queried (or debugjson is rendered).  
**Then:** Saturation = min(1, max(0, 95/100)) = 0.95, clamped to [0, 1].  
**Failing scenario:** Saturation = 1.5 (exceeds bound), or Saturation < 0 (negative is impossible).

### AC-4: Sustained-Overload Counter Increments When Saturation >= Threshold
**Given:** A line class reaches saturation 0.85 (≥ OVERLOAD_PENALTY_THRESHOLD 0.80).  
**When:** The tick advances with sustained saturation.  
**Then:** Its overload counter increments by 1 each tick (capped at OVERLOAD_SUSTAINED_TICKS = 60).  
**Failing scenario:** Counter stays 0 while saturation is 0.85, or counter skips ahead multiple ticks in a single advance.

### AC-5: Sustained-Overload Counter Hard-Resets When Saturation Drops Below Threshold
**Given:** A line class has overload counter = 45 (sustained for 45 ticks).  
**When:** Its saturation drops below OVERLOAD_PENALTY_THRESHOLD (e.g. 0.75) this tick.  
**Then:** Its counter resets to 0 immediately (no hysteresis, no gradual decay).  
**Failing scenario:** Counter decays or stays > 0 after saturation recovers.

### AC-6: Line Class Overload-Brownout Active Flag
**Given:** A line class counter ≥ OVERLOAD_SUSTAINED_TICKS (60).  
**When:** The query `isLineClassOverloaded(s, "super")` is evaluated.  
**Then:** Returns true.  
**Failing scenario:** Returns true when counter < 60, or returns false when counter ≥ 60.

### AC-7: isBrownoutActive() Composes Deficit & Overload Brownouts via OR
**Given:** A city with ample generation (no deficit brownout) but super-grid overload-brownout active.  
**When:** `isBrownoutActive(s)` is queried.  
**Then:** Returns true (because overload-brownout is active).  
**Failing scenario:** Returns false (ignores overload), or only checks deficit.

### AC-8: Brownout Income Penalty Applies to Overload Brownout
**Given:** A city in overload brownout (no deficit, just transmission overload).  
**When:** `computeFlows()` is called.  
**Then:** Business/Freight/Office income is penalized via `incomeFactor = 1.0 - (overloadSeverity * BROWNOUT_INCOME_K)`, where overloadSeverity is Aaron's metric for overload intensity.  
**Failing scenario:** Income remains unchanged during overload brownout, or penalty is different from deficit brownout penalty.

### AC-9: Wellbeing Utilities Part Collapses During Overload Brownout
**Given:** A city in overload brownout.  
**When:** `wellbeingOf()` is computed.  
**Then:** The Utilities (power) wellbeing part drops (or collapses, per AC-17 lesson) as if a deficit brownout is active — no separate penalty computation, same SSOT via `isBrownoutActive()`.  
**Failing scenario:** Utilities part is unchanged, or computed separately from brownout state.

### AC-10: Brownout Banner & Alerts Visible During Overload Brownout
**Given:** A city in transmission overload (visible in debugjson or F3 power panel).  
**When:** The DemandDock panel or main HUD renders.  
**Then:** A brownout banner (⚠️ BROWNOUT) is shown, and power-row alerts escalate (red, not yellow).  
**Failing scenario:** Banner is absent, or alerts stay yellow.

### AC-11: Debugjson Carries Power Line Class State
**Given:** A city with pylons/cables.  
**When:** `debugjson()` is rendered.  
**Then:** It includes a `powerGridState` section with:
  - Per-class capacity (MW), flow (MW), saturation, overload-counter (ticks), and overload-brownout-active flag.
  - Example: `{ "super": { "capacityMW": 500, "flowMW": 420, "saturation": 0.84, "overloadTicks": 35, "overloadBrownoutActive": false }, "local": { … } }`.  
**Failing scenario:** Debugjson has no power-line state, or it's incomplete (missing saturation, counters, or class breakdown).

### AC-12: Old-Save Defaults for Power Line Overload State
**Given:** A save file from before this feature (no `powerLineOverloadTicksByClass` field in the serialized state).  
**When:** The save is loaded.  
**Then:** Missing `powerLineOverloadTicksByClass` defaults to `{}` (no sustained overloads), and the first tick recomputes counters from the current transmission saturation.  
**Failing scenario:** Loading an old save crashes (no default), or reads garbage values from an uninitialized field.

### AC-13: Conservation — Overload Doesn't Invent or Destroy Money
**Given:** A city in transmission overload brownout incurring an income penalty.  
**When:** The ledger is audited (engine.invariant's GoodsInvariant for money).  
**Then:** No new money is created or destroyed; the penalty is a redistribution (income reduced, deficit widening), and the conservation equation `money-in + prior-balance = money-out + current-balance` holds exactly.  
**Failing scenario:** Money appears or vanishes, or the ledger's `delivered + shortfall != demand` for power.

### AC-14: Determinism — Overload State is Deterministic Across Replays
**Given:** Two identical save states (same buildings, generation, demand, transmission tiles, tick count with overload history).  
**When:** Both are advanced one tick.  
**Then:** Both compute byte-identical overload counters, saturation, and isBrownoutActive() result. Replayed a third time with a line tile added, the new saturation and counters are deterministically reproducible.  
**Failing scenario:** Two runs of the same state compute different overload values, or the state varies across replays.

### AC-15: Auto-Scale Interaction — Transmission Capacity Can Auto-Widen (Flagged for Aaron)
**Given:** A super-grid line class is overload-brownout-active (sustained 60+ ticks).  
**When:** This feature ships.  
**Then:** ⚠️ **PROPOSAL (Aaron's call):** the engine runs a **monitor** similar to road auto-widening (e.g. `evaluateTransmissionMonitors()` mirroring `evaluateRoadMonitors()`) that auto-upgrades the saturated line class to a higher-capacity tier (e.g. local → super, or super → super-premium) if the overload has been sustained, subject to space (no upgrade if all tiles are exhausted) and spend approval (auto-widen applies the same budget-guard as roads). The **monitor shape:** per line class, if `overloadBrownoutActive && cash > auto-cost`, queue an auto-upgrade (or auto-double the capacity with a cost multiplier); tentatively YES, same as roads, but Aaron's final call on whether transmission auto-scales at all or players must manually build more.  
**Failing scenario:** A city at transmission overload for 60+ ticks does nothing automatically; the player must manually build new transmission tiles to relief (mirrors the old road-widening manual era).

### AC-16: Super-Grid vs Local-Grid Distinction & Capacity Trade-Off
**Given:** A city with both local-grid (20 MW/tile) and super-grid (100 MW/tile) lines available for build.  
**When:** The player designs their transmission layout.  
**Then:** The two classes are visually distinct (different glyphs/colours on the map), have different costs (super-grid more expensive per-tile or -cost, reflecting real infrastructure), and the city must route generation to demand via a mix of both classes. A large plant without super-grid reach stays bottlenecked; a well-connected spine (super) with local-grid distribution is resilient. This distinction is the **game design meat** — transmission scarcity forces topology choices.  
**Failing scenario:** Only one line class is buildable, or both have identical capacity/cost (no choice/trade-off).

---

## Open Questions for Aaron

1. **Overload-penalty threshold:** At what saturation should overload consequences begin to bite?
   - **Candidates:** 0.75 (early pain), 0.80 (moderate), 0.90 (crisis-only).
   - **BA recommendation:** 0.80 — high enough that a well-designed network at 75% is safe, but low enough that trying to squeeze generation through thin transmission is quickly felt.

2. **Sustained window length (power lines):** How many ticks for transmission overload to be "sustained" (same as road congestion)?
   - **Candidates:** 30 ticks (noisy), 60 ticks (medium), 120 ticks (glacial).
   - **BA recommendation:** 60 ticks — consistent with road congestion, matching the 2-month feel.

3. **Overload severity metric for income penalty:** When a line class is overload-brownout-active, what is the `overloadSeverity` value fed to the income penalty?
   - **Candidates:** 
     - (A) Identical to deficit-brownout severity (deficitRatio), so both causes apply the same penalty.
     - (B) Independent metric based on transmission saturation excess (e.g. max(0, saturation - threshold) / (1 - threshold)), so an 90%-saturated line costs differently than a 50%-deficit.
     - (C) Hybrid: max(deficit, overload) — whichever is worse.
   - **BA recommendation:** (A) — identical to deficit, so the player sees one brownout penalty magnitude; Aaron can tune BROWNOUT_INCOME_K to set overall severity. Separate metrics risk confusing the UX.

4. **Two line classes or more?** Should there be exactly two (local + super), or should we design for Nfuture extensibility (e.g. distribution, transmission, ultra-high-voltage)?
   - **BA recommendation:** Two for Baseline One (local 20 MW/tile, super 100 MW/tile); future expansions (e.g. HVDC links, overseas interconnect) can add classes later without breakage.

5. **Local-grid vs super-grid MW/tile PLACEHOLDER values:** 20 and 100 are order-of-magnitude guesses. What real ranges do you want?
   - **Candidates:** Local 10–30 MW/tile, Super 80–150 MW/tile.
   - **BA recommendation:** 20/100 as a round 5× ratio, easy to explain ("transmission is 5× more powerful"); Aaron's balance pass can retuning.

6. **Auto-scale for transmission (AC-15):** Should transmission lines auto-upgrade when overloaded (YES, mirrors roads) or should the player manually expand transmission (NO, forces intentional planning)?
   - **BA recommendation:** YES, same monitor as roads — overload-brown-out is a strong enough signal that auto-widening is fair. But if Aaron wants transmission to be a "resource scarcity" puzzle, say NO and players must plan ahead or live with brownout.

7. **Grid import cover interaction (FEAT-2326609711 inc1 ruling):** If Grid Import is ON, does it suppress transmission overload brownout, or only deficit brownout?
   - **Proposal:** Grid import covers **deficit only** — if generation is ample but transmission is overloaded, the import cannot bypass the overload (the wires are the constraint, not the supply). So transmission overload-brownout persists even with imports ON.
   - **Alternatively:** Grid import has a **component for transmission overflow** — a "transmission lease" line item that reroutes power via interconnects at a premium cost, suppressing overload brownout if paid.
   - **BA recommendation:** Deficit only (simpler, forces transmission planning as distinct from generation). Aaron's call if the second interpretation fits the inc1 balance vision.

8. **Conservation proof for power routing:** The proof that generation→transmission→demand conserves power (no invented/destroyed MW-hours) should mirror the existing `powerStats()` conservation check. Should this be a standalone test or folded into engine.invariant?
   - **BA recommendation:** Standalone test in the power-routing code first; once stable, roll into engine.invariant's `GoodsInvariant` if Aaron wants it in the determinism gate.

---

## Implementation Notes

### Files to modify (TS dogfood layer — same as congestion-teeth):
- **webconsole/src/sim/data.ts:** 
  - Add `OVERLOAD_CONSTANTS` (PENALTY_THRESHOLD, SUSTAINED_TICKS) alongside CONGESTION_CONSTANTS.
  - Add `PowerLineState` interface (per-class capacity/saturation/overload-counter/active flag).
  - Extend `SimState` with `powerLineOverloadTicksByClass: Record<string, number>` (like `congestionTicksBySpec`).
  - Add `powerLineStatesOf(s: SimState)` function (mirrors `congestionLinesOf`, iterates pylon/cable specs, computes per-class saturation/overload-flag).
  - Add `advancePowerLineOverloadTicks()` (mirrors `advanceCongestionTicks`).
  - Add `powerLineOverloadBrownoutActive(s: SimState): boolean` (checks if ANY line class is overload-active).
  - Update `sanitizeSimState()` to coerce `powerLineOverloadTicksByClass` (GR#16 type-safe storage boundary).

- **webconsole/src/sim/engine.ts:**
  - Update `advance()` to call `advancePowerLineOverloadTicks()` (before or after `advanceCongestionTicks`, no dependency order).
  - Update `isBrownoutActive()` (or the caller of `brownoutOf()`) to compose: `(existingDeficitLogic) OR powerLineOverloadBrownoutActive(s)`.
  - Update `computeFlows()` to feed `isBrownoutActive()` (the composition) into `incomeFactor`, so overload-brownout triggers income penalty.
  - Update `wellbeingOf()` — Utilities part should already key off `isBrownoutActive()` (verify existing code), so no change needed if it already reads the SSOT.

- **webconsole/src/sim/debugjson.ts:**
  - Add `powerGridState` section to the JSON output, including per-class capacity/flow/saturation/overload-counter/active flag.

### Data files:
- **data/buildings.json:** Add pylon/cable specs (or update existing ones) with `powerLineClass` and `lineCapacityMW` fields.
  - Example entry:
    ```json
    {
      "id": "pylon_transmission_400kv",
      "name": "400 kV Transmission Pylon",
      "powerLineClass": "super",
      "lineCapacityMW": 100,
      "costPerTile": 150000,
      "consumption": null,
      "catalogueSection": "E",
      "appealProfile": ["negative"],
      "notes": "High-capacity long-distance transmission; visual impact"
    }
    ```

### Constants to define (balance placeholders):
- `OVERLOAD_PENALTY_THRESHOLD` (default ~0.80)
- `OVERLOAD_SUSTAINED_TICKS` (default ~60)
- (Income penalty uses existing `BROWNOUT_INCOME_K` — no new constant)

### No Go-engine changes required for inc1:
Baseline One targets the TS dogfood sim. Go engine convergence (inc2 or later) can mirror the same logic.

---

## Overlap & Sequencing Notes

- **FEAT-2326609741 (Grid transmission capacity — super vs local):** This AC fulfills the super/local distinction at the design level; implementation of the actual specs is a separate task (data-loading + catalogue registration).
- **Brownout income penalty (existing):** Already present in `brownoutOf()` and `computeFlows()` (line 603); overload-brownout reuses the same penalty via the `isBrownoutActive()` composition, no duplicate logic.
- **Congestion teeth (FEAT-congestion-teeth-2026-09-02):** Independent; both are independent line-saturation mechanics (roads vs power). They can coexist without conflict; a city can be both congested and overloaded.
- **Auto-widening (existing road monitors):** If AC-15 proposes transmission auto-scaling, it mirrors the road pattern; no code conflict, just a new monitor function in the same family.

---

## Acceptance Test Shape (Reference)

A passing test suite should include:

1. **Unit: Power line spec loading** — pylon/cable specs carry `powerLineClass` + `lineCapacityMW`.
2. **Unit: Flow apportionment** — given capacities and flow, saturation ∈ [0,1], deterministic across sorts.
3. **Unit: Overload counter advance** — saturation >= threshold increments counter; saturation < threshold resets to 0.
4. **Unit: isBrownoutActive composition** — deficit OR overload returns true when either is active.
5. **Integration: Old-save load** — missing `powerLineOverloadTicksByClass` defaults to `{}`, first advance recomputes.
6. **Integration: Debugjson coverage** — `powerGridState` section is present and populated.
7. **Integration: Determinism** — replays of identical states produce byte-identical overload state and `isBrownoutActive()` result.
8. **Integration: Conservation** — overload brownout income penalty is a redistribution, not money creation.

---

**House style:** Mirrors `docs/planning/acceptance/FEAT-congestion-teeth-2026-09-02.md` (Given/When/Then + AC numbering, open questions, implementation notes, overlap notes, balanced placeholder values).

---

## Status

**Ready for Aaron's verdict on open questions (1-8) before implementation dispatch.**
