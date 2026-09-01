# FEAT-2326609711 inc2: Utilities Buy-In (Water, Wastewater, Garbage)

**Mkey:** FEAT-2326609711  
**Epic:** External provision buy-in vs local funding (Baseline One—watchable deterministic year)

**Relates to:** MOD-050 (water), MOD-059 (waste), FEAT-1972079890 (fiscal SSOT), FEAT-2326609711 inc1 (power grid import pattern)

**GR#25 scope:** webconsole-internal `fiscal.ts` / `engine.ts` / `types.ts` / `data.ts` / `RightDock.tsx`. No new code.json edges. Extends inc1's Grid Import mechanic to water, wastewater, and garbage.

---

## Design Ruling (Aaron, 2026-09-01)

Extends inc1's external-cover pattern to **three new utilities**: clean water, wastewater treatment, and garbage collection. New cities default to **external cover enabled** for all three. When local capacity falls short of demand, the city buys the difference at external tariffs (per unit served/treated/collected, per tick) that are **strictly higher** than the amortised local cost. Player can toggle each utility independently. When off, the legacy shortage behaviour (no service, no income from that service) applies unchanged. External tariffs are charged as finance **outflow lines** labelled exactly "Water Import", "Waste-Water Contract", "Contracted Refuse" and persisted in saves and deterministic replays.

---

## Placeholder Constants (balance-number regime)

All tariffs are **named constants** sourced from `fiscal.ts` for Aaron's row-by-row approval. Tests reference exports, never hardcoded.

| Constant | Value (placeholder) | Usage | Notes |
|----------|---------------------|-------|-------|
| `WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK` | 0.08 | External clean-water tariff per person served per tick | Must be > local-cheapest water plant amortised cost/person/tick |
| `WASTEWATER_CONTRACT_TARIFF_PER_PERSON_PER_TICK` | 0.06 | External sewage tariff per person treated per tick | Must be > local cheapest plant amortised cost |
| `REFUSE_CONTRACT_TARIFF_PER_TONNE_PER_TICK` | 0.5 | External refuse tariff per tonne collected per tick | Must be > local depot amortised cost/tonne/tick |
| `EXTERNAL_COVER_DEFAULTS` | `{ water: true, wastewater: true, refuse: true }` | New cities start with all covers on | Mechanical; affects genesis replay and new-game flow only |

**Local cheapest-plant amortised cost:** derived at test-time from data.ts SPECS (water plant with lowest `cost ÷ served ÷ lifespan`; waste depot with lowest cost/capacity/lifespan), never hardcoded. Tariff invariant checks assert each tariff > local_cheapest_amortised_£/unit/tick.

---

## Acceptance Criteria

### AC-1 (new city defaults to all external covers on)

**Scenario:** Genesis replay. Check: after initial state, state carries `externalCover` field (or inc1-compatible alias/migration) with each utility (water, wastewater, refuse) **enabled**:
- `state.externalCover.water === true`
- `state.externalCover.wastewater === true`
- `state.externalCover.refuse === true`

On tick 1, when water need > clean capacity, no shortage applies; instead, outflow "Water Import" appears in `lastFlows.outflows`. Same for wastewater/refuse.

**Mutation:** set all defaults to `false`; new city has external cover off. Import lines do not appear. Test goes red.

**False-pass:** flag exists but is never consulted in outflow-computation paths.

---

### AC-2 (water import outflow = (needPersons − capPersons) × tariff when enabled)

**Scenario:** Water system. At tick N:
- Population needing clean water: 500 people
- Clean water capacity (from specs `served` sum): 350 people
- External cover is enabled
- `WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK` = 0.08

Check: `lastFlows.outflows` contains entry:
- `label === 'Water Import'` (exact label)
- `value === Math.round((500 − 350) × 0.08)` = Math.round(12) = 12 (the shortfall 150 people × 0.08)

If capacity >= need (no shortage), line is absent (not zero-value).

**Mutation:** tariff calculation uses `needPersons * 0.04` (cheaper). Value becomes 20 instead of 12. Test goes red.

**False-pass:** outflow present but value wrong (double-counted or using wrong formula).

---

### AC-3 (external cover disabled → no outflow, legacy behaviour applies)

**Scenario:** Water at shortfall (need 500, cap 350 people). Cover toggle is **off** (`state.externalCover.water === false`).

Check:
- `lastFlows.outflows` does **not** contain "Water Import" (absent, not zero-value)
- No water shortage effects applied (legacy: zero-served buildings produce zero, penalty applies via demand-scaling in serviceCoverageOf, engine.ts:2800–2850)
- Byte-identical to pre-Import behaviour for this utility

**Mutation:** remove the toggle check; import line appears regardless. Test goes red: double-penalty.

**False-pass:** toggle checked but no regression verification against pre-feature code path.

---

### AC-4 (tariff invariant per utility)

**Scenario:** Load each utility's cheapest local plant (data.ts SPECS, lines 113–114 for water, 60–64 for refuse). Compute amortised cost:
- Water: cheapest water plant `cost ÷ served ÷ lifespan_ticks`
- Waste depot: `cost ÷ wasteCapacity ÷ lifespan_ticks`

Check at test-time:
- `WATER_IMPORT_TARIFF > local_water_cheapest_£/person/tick`
- `WASTEWATER_CONTRACT_TARIFF > local_wwt_cheapest_£/person/tick`
- `REFUSE_CONTRACT_TARIFF > local_depot_cheapest_£/tonne/tick`

Utility function `verifyUtilityTariffInvariants()` asserts all three and logs cheapest per type.

**Mutation:** flip one invariant; tariff < amortised. Test fails.

**False-pass:** constants defined but never validated.

---

### AC-5 (toggles persist in saves; genesis replay respects state)

**Scenario:** At tick 500, player sets `externalCover.refuse = false`. City saves. Load save: `externalCover.refuse` must be `false`. Genesis replay: same toggle state at same replay tick.

Check:
- SimState `externalCover` is serialisable (plain boolean fields)
- Toggles round-trip through save/load/replay byte-identically

**Mutation:** remove toggles from save format. Loaded state defaults all covers to true. Test goes red.

**False-pass:** toggled but not serialised; reconstructed from hardcoded default on load.

---

### AC-6 (money conservation: outflows debited exactly once per tick)

**Scenario:** Tick N with water shortage and cover enabled. Check conservation (FEAT-1972079890, fiscal.ts):

```
fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows
```

Specifically:
- Each utility import is included in `Σoutflows` exactly once
- No double-debit (not in array twice, not side-channel)

**Mutation:** debit import cost to `state.funds` directly **and** add to outflows. Double-counted. Test fails: Σoutflows over-counts.

**False-pass:** debited via flows only, but consistency checker has a buggy recompute.

---

### AC-7 (insolvency path unaffected; imports are normal outflows)

**Scenario:** City solvent, but import outflows push funds negative. Check:
- Insolvency fires per fiscal.ts logic (funds <= DEBT_THRESHOLD → 'crisis')
- Imports treated identically to other outflows: summed, trigger insolvency, subject to policy modifiers
- No special exemption

**Mutation:** exempt imports from insolvency logic. City carries negative funds indefinitely. Test goes red.

**False-pass:** insolvency fires for a different reason (wages alone); imports never actually tested in the crisis path.

---

### AC-8 (finance panel shows import lines when occurring)

**Scenario:** RightDock.tsx Finance/Earnings tab. Shortfall active; cover enabled. Check:
- Row present: label "Water Import"/"Waste-Water Contract"/"Contracted Refuse" (exact match)
- Value: current tick's import cost from `lastFlows.outflows`
- Row absent when no shortfall (per AC-2)

**Mutation:** remove import rows. Player sees no ledger entry; funds decrease unexplained.

**False-pass:** row rendered but value stale (from previous tick).

---

### AC-9 (utilities panel shows toggles + status)

**Scenario:** A Utilities panel exists in UI (Resources or separate modal). Check:
- Toggle for each utility: "Use external water cover", "Use external sewage cover", "Use contracted refuse collection"
- Current state matches `state.externalCover.X`
- Clicking toggles sets opposite value; next tick respects new state

**Mutation:** remove toggles. Player has no way to disable; hardcoded on.

**False-pass:** toggle present but clicking does not dispatch action.

---

### AC-10 (toggle state persists across UI interactions; not volatile)

**Scenario:** Player toggles water cover off (tick 100). Closes panel, reopens. Check: toggle still off.

**Mutation:** stored in React local useState instead of sim state. Closing panel clears local state; toggle resets to default.

**False-pass:** persists within a single session; not persisted to saves (caught by AC-5).

---

### AC-11 (determinism: same shortage → same import cost)

**Scenario:** Replay a save, passing through various shortfall states. Check:
- Every tick N where demand > capacity, computed import cost matches original exactly
- No Math.random(), wall-clock, non-deterministic inputs
- Byte-identical `lastFlows.outflows` array

Mechanically: imports computed purely from state (demand, capacity, tariff constant) via `computeFlows()`, never from cache or wall-clock.

**Mutation:** introduce `Math.random() * 0.1` into tariff. Replay shows different costs for same shortage. Test goes red.

**False-pass:** tariff deterministic, but demand/capacity computed non-deterministically (caught by existing power-panel tests, but must verify import path is clean).

---

### AC-12 (existing-city loads default new fields explicitly; no silent economy change)

**Scenario:** Load a pre-existing save (no `externalCover` field). Check:
- Replayed save receives `externalCover` with defaults applied: `{ water: true, wastewater: true, refuse: true }` (NEW cities default on; old cities loaded with defaults on to match player expectation — no breakage)
- Legacy shortage behaviour at tick 0: if old save was short of water, it remains short until next tick (no retroactive cover applied to the loaded-state instant)
- Document the replay-divergence rule: an undefined field on an old savepoint must NOT silently change its economy. Specify the safe default and a regression AC.

**Mutation:** remove default-assignment for undefined `externalCover`. Old saves fail to load or crash when checking toggles.

**False-pass:** old saves load but toggles are undefined; comparisons crash mid-tick.

---

### AC-13 (regression: existing shortage tests still pass; legacy behaviour unchanged when covers off)

**Scenario:** A pre-existing test (water demand-scaling, refuse coverage) verifies shortage effects with NO external cover available (simulating a city that never had inc2 feature). Re-run test with new import code in place, explicitly setting `externalCover` to `false` for all utilities.

Check: test passes with identical results (same demand scaling, same shortage effects). No regression: legacy path is byte-identical.

**Mutation:** weaken shortage logic when covers off. Shortage effects no longer apply or apply inconsistently. Test goes red: regression detected.

**False-pass:** test passes because it was already broken; adding imports doesn't change outcome by accident.

---

## Files Expected to Change

- `webconsole/src/sim/fiscal.ts` — add tariff constants (`WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK`, etc.) and `verifyUtilityTariffInvariants()` function for tests
- `webconsole/src/sim/engine.ts` — in `computeFlows()` after Grid Export block (line 506–513), add Water/Wastewater/Refuse import blocks: check `state.externalCover.X` and demand vs capacity, compute shortfall, conditionally push import outflows
- `webconsole/src/sim/types.ts` — add `externalCover: { water?: boolean; wastewater?: boolean; refuse?: boolean }` field to `SimState` (optional for backward tolerance, or aliased from inc1's `gridImportEnabled` migration)
- `webconsole/src/sim/data.ts` — no changes (water/waste demand/capacity functions already exported: `waterDemandOf`, `waterBalanceOf`, `collectionCoverageOf`, engine.ts:683–691, 2188–2189)
- `webconsole/src/components/right/RightDock.tsx` — Finance panel displays import lines when present; Utilities panel shows three toggles + status readouts (imported persons/tonnage)
- `webconsole/src/sim/store.tsx` or action layer — add action handlers to toggle each cover and dispatch tick

---

## Out of Scope (inc3+)

- Fire/police/health external coverage (inc3)
- Capacity ceiling balancing (Aaron design call)
- Quality/satisfaction penalty on external cover (deferred)
- Go engine mirror (inc5)
- SaveLoad detailed implementation (assumes SimState serialisation)
- Genesis Replay action journaling (assumes journal preserves toggle state)
- UI/UX design (layout, colours, accessibility — AC covers data only)
- Tariff tuning / balance pass (Aaron row-by-row approval pending)

---

## Open Questions for Aaron

1. **Single `externalCover` field vs. inc1 migration:** Should inc1's `gridImportEnabled: boolean` be aliased to `externalCover.power`, or should inc1 code both fields until a future cleanup? (Interim: separate field in inc1, unified as `externalCover` struct in inc2 with aliasing layer for backward tolerance.)

2. **Per-utility defaults on old saves:** When loading a pre-existing save without `externalCover`, should ALL utilities default to on (matching new-game expectations, safest for player progression), or should they default to off (preserving original legacy-shortage behaviour)? (Interim: all on; document as a replay-divergence exception.)

3. **Demand basis:** Should import computation use `serviceCoverageOf` demand (population-driven) for water/wastewater, or a separate water-system demand (FEAT-1972079896)? (Interim: serviceCoverageOf; same SSOT as water panel.)

---

**End of AC. Report path: `E:/git/Metropolis/docs/planning/acceptance/feat-2326609711-inc2-utilities-buyin.md`**

**Lines: 219 (excluding blank lines and headers)** | **ACs: 13** | **GR#25: graph-internal only, mirrors inc1 pattern**
