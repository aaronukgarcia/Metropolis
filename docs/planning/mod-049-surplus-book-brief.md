# MOD-049: Service Capacity Export — Design Brief

**Placement Decision: Implement Power Export Prototype in Webconsole First (with Canonical Go Engine Follow-up)**

---

## Executive Summary

Aaron's dogfood observation (2026-08-27) exposed a concrete gap in the webconsole: a 17,277 MW power plant capacity generating only 6,095 MW demand yields an 11,182 MW surplus (65% waste) with **zero monetization**. The Go engine's MOD-049 (internal/engine/capexport/) is fully built for ten service lines but POWER is deliberately excluded pending BUG-058 (missing engine.capexport ↔ engine.consumption edge). Meanwhile, the webconsole is the playable surface today.

**Recommendation: Build power export in the webconsole first (incremental, immediate value to Aaron's game) while noting MOD-049 remains the canonical long-term home. Risks: prototype surplus mechanics may not survive the transition to the Go engine; power tariff values are placeholders pending Aaron's balance sign-off.**

---

## Phase 1: Power Export (inc1)

### Mechanic

Sell (capMW − needMW) at a per-MW tariff into a new "Grid Export" inflow line.

- **Calculation:** `exportMW = max(0, capMW - needMW)`  
  `exportRevenue = exportMW * tariff` per tick
- **Inflow integration:** Add "Grid Export" to `computeFlows()`'s inflows array (alongside Council Tax, Business Tax, etc.)
- **Conservation:** Must pass the tick-boundary invariant (BUG-406): `fundsAtTickEnd === fundsAtTickStart + Σ(inflows) − Σ(outflows)`
- **Visibility:** Flows.inflows contains the item; Finance dock's fiscal view reflects export revenue in the inflow section
- **Behaviour when capMW ≤ needMW:** Grid Export = 0 or omitted (no export line shown when idle)

### Tariff Model (Placeholder — Pending Aaron's Row-by-Row Sign-Off)

Two candidates:

**Option A: Fixed per-MW rate**  
- Simplest: `tariff = GRID_EXPORT_TARIFF` (e.g. 1.6, 2.0, etc.)
- Pros: Direct, transparent
- Cons: Decoupled from plant economics

**Option B: Fraction of plant upkeep cost**  
- Observational: Power Grid upkeep ~9,920/tick ÷ 6,095 needMW ≈ 1.63/MW (cost basis)
- Model: `tariff = GRID_UPKEEP_COST_BASIS` (derived from actual maintenance)
- Pros: Ties export value to service cost, economies-of-scale natural
- Cons: Tariff moves if upkeep coefficients change

**Recommendation:** Start with Option A (fixed rate) for clarity; Aaron can sweep to Option B post-balance-pass.

### Acceptance Criteria (inc1 — Power Only)

1. **Surplus Detection**  
   Given capMW > needMW, the export MW is calculated as (capMW − needMW)  
   With needMW ≥ capMW, export MW = 0

2. **Revenue Calculation**  
   Export revenue = exportMW × tariff (per tick)  
   Revenue is included in computeFlows().inflows (not omitted when zero)

3. **Flow Ledger & Conservation**  
   Export revenue appears in lastFlows.inflows as "Grid Export"  
   TICK-BOUNDARY INVARIANT holds: tick-end funds = tick-start funds + inflows − outflows (export revenue counted)

4. **Fiscal Display**  
   Finance dock (flows inflow section) shows "Grid Export: NNN" when exportMW > 0  
   Absent or shown as "Grid Export: 0" when exportMW = 0 (design to Aaron)

5. **Projection Curve (F7 integration)**  
   The export revenue projects forward as demand grows  
   If demand curve crosses capacity, export revenue should curve down deterministically

6. **Edge Case: Brownout**  
   Power brownout (need > cap) should not suppress export calculation; the `brownoutOf()` penalty applies to income, not export availability

---

## Phase 2: Other Surplus Streams (inc2)

**Only implement streams the webconsole already models with capacity/demand:**

- **Sewage/Waste Water:** (waste cap − waste need) sold at £/unit; modelled alongside clean water in waterBalanceOf()
- **Clean Water:** (clean cap − clean need) sold at £/unit; same function
- **Hospital Beds:** (health capacity − population) sold at beds/unit
- **School Places:** (school places − student need) sold at places/unit
- **Police:** (police capacity − population) sold at (capacity − coverage)/unit

**Not in scope (webconsole lacks models):**
- Refuse/incineration, toxic waste, university, crematorium, prison, transshipment, mutual aid (no capacity/demand in webconsole yet)

Each stream follows the power inc1 pattern: surplus calculation → tariff → inflow line → fiscal view.

---

## Phase 3: Contracts & Commitments (inc3)

**Stretch goal (likely exceeds webconsole scope):**

- Term: contract duration (months/years)
- Per-unit price: agreed rate (£/MW, £/bed, etc.)
- Commitment: quantity sold is reserved; your own growth that crosses sold capacity triggers a choice:
  - Pay cancellation penalty (remaining term × rate × quantity) to break contract
  - Cut internal service (reduce citizen coverage by the shortfall)
- Visual: Projections screen shows demand curve crossing sold-capacity line at crossing year; game pauses or prompts choice
- Ledger: Penalty postings tagged `trade.export` (matching Go engine's FinanceAPI tagging)

**Status:** Likely requires UI work beyond inc1/inc2; mark as aspirational, not blocking.

---

## Webconsole Implementation Details

### Files That inc1 Touches

| File | Role |
|------|------|
| `webconsole/src/sim/fiscal.ts` | Add new function `gridExportRevenuePerTick(capMW, needMW, tariff)` or inline the calc in `computeFlows()` |
| `webconsole/src/sim/engine.ts` / `computeFlows()` | Add "Grid Export" inflow item; call `powerStats()` and compute revenue; integrate into flows |
| `webconsole/src/sim/data.ts` | Define `GRID_EXPORT_TARIFF_PER_MW` constant (placeholder); update `powerStats()` if needed |
| `webconsole/src/sim/consistency.ts` | Verify tick-boundary invariant includes export revenue; run conservation check |
| `webconsole/src/ui/docks/FlowsDock.tsx` | Display "Grid Export" in inflows list (same as other inflow items) |
| `webconsole/src/ui/screens/finance/FinanceScreen.tsx` | Show Grid Export line in fiscal summary/breakdown view if it exists |

### UI Surface (inc1)

- **Flows dock (left sidebar):**  
  Inflows section → "Grid Export: X" item added below or alongside "Tourism"
- **Finance screen:**  
  Fiscal breakdown shows export revenue as its own line (or folded into "Other" category if space is tight; Aaron's call)
- **Status bar or HUD:**  
  Optional: display active power surplus (e.g. "+2,500 MW spare") so player is aware of the monetizable capacity

### State Fields (inc1)

- Power stats (capMW, needMW) already calculated by `powerStats()`; no new state needed
- Tariff constant in `fiscal.ts` or `data.ts`
- Export revenue computed fresh each tick (stateless, deterministic)

---

## Open Questions for Aaron

1. **Tariff Value:**  
   - Option A (fixed rate): Propose 1.6, 2.0, or another target?
   - Option B (cost-basis): Should tariff track plant upkeep coefficient changes?

2. **Export Cap:**  
   - Is grid export unlimited, or capped by a grid-interconnect limit (e.g. 50% of capacity, or a fixed MW ceiling)?
   - If capped, value as a game lever or pure realism?

3. **Balance Regime:**  
   - Tariff value pending your row-by-row balance pass (FEAT-077 proportionality ruling)?
   - Expected: export revenue should help close the -109k/tick deficit but not completely flip insolvency;  
     data suggests ~18k/tick at 1.6/MW tariff is reasonable directional order of magnitude

4. **Webconsole → Go Engine Handoff:**  
   - Once BUG-058 (capexport ↔ consumption edge) is resolved, MOD-049 inc1 (power export, no contracts) should land in the Go engine proper  
   - Webconsole prototype will likely be superseded; are you comfortable with throwaway mechanics here?

5. **Fiscal Tagging:**  
   - Go engine tags export via FinanceAPI as `trade.export` for future balance-of-trade queries  
   - Webconsole currently has no tagging system; is a future sweep acceptable or should inflow tags be added now?

---

## Risk Assessment

| Risk | Mitigation |
|------|------------|
| **Prototype throwaway** | Webconsole surplus mechanics may not map 1:1 to MOD-049 Go engine; prototype is a teaching tool, not production | Communicate scope clearly; Go engine remains canonical long-term |
| **Tariff uncertainty** | No agreed-upon per-MW rate yet; export revenue swing from 1/MW to 5/MW is 5x variance | Flag as placeholder; Aaron to sign off row-by-row |
| **Conservation regression** | Adding export inflow could break tick-boundary invariant if not integrated correctly | Test harness (BUG-406 checker) will catch this immediately |
| **UI clutter** | Flows dock and Finance screen have limited space; new inflow may overlap existing items | Propose compact format; Aaron reviews mockup before code |

---

## Success Criteria (Delivery of inc1)

- [ ] Power surplus is calculated and displayed (export MW = capMW − needMW, >0 only when capMW > needMW)
- [ ] Export revenue inflow appears in computeFlows() alongside existing inflows
- [ ] "Grid Export: NNN" line visible in Flows dock inflows section
- [ ] Conservation checker passes: fundsAtTickEnd = fundsAtTickStart + exports + other inflows − outflows
- [ ] Tariff value accepted by Aaron; constant applied consistently
- [ ] Acceptance criteria (AC 1–6) all pass
- [ ] Webconsole gameplay reflects the surplus as a revenue stream (no more invisible 11k MW waste)

---

## Timeline Estimate

- **Design review:** Done (this brief)
- **Build (inc1, power only):** ~1–2 sprints (Sonnet junior + Tester); depends on conservation checker complexity and UI dock refactoring
- **Balance pass:** Awaits Aaron's row-by-row tariff sign-off (GR#15 balance-number regime)
- **Go engine MOD-049 power edge:** Blocked on BUG-058 resolution; separate track

---

*Design brief completed 2026-08-27. Awaiting Aaron's decision on placement, tariff model, and balance values.*
