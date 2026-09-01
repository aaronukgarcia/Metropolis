# FEAT-2326609711 inc1: Power Grid Import

**Mkey:** FEAT-2326609711  
**Epic:** External provision buy-in vs local funding (Baseline One—watchable deterministic year)

**Relates to:** MOD-049 (power grid), FEAT-1972079890 (fiscal SSOT), BUG-393 (brownout), BUG-452 (money scale)

**GR#25 scope:** webconsole-internal `fiscal.ts` / `engine.ts` / `data.ts` / `RightDock.tsx` + `types.ts` state shape. No new `code.json` edges required. Mirrors the existing Grid Export mechanic (fiscal.ts:93-103, engine.ts:506-513).

---

## Design Ruling (Aaron, 2026-09-01)

A new city defaults to **external power cover enabled**: when local power capacity falls short of demand, the city buys the difference from a regional grid at an external tariff (per MW, per tick) that is **strictly higher** than the amortised cost of the cheapest local plant. The player can toggle external cover **off** to force local self-sufficiency; when off, the legacy shortage behaviour (brownout income penalty, BUG-393) applies unchanged. The external tariff is charged as a finance **outflow line** labelled exactly "Grid Import" and is persisted in saves and deterministic replays.

---

## Placeholder Constants (balance-number regime)

All player-felt tariffs and coverage defaults are **named constants** sourced from `fiscal.ts` for Aaron's row-by-row balance approval. Tests reference the exports, never hardcoded numbers.

| Constant | Value (placeholder) | Usage | Notes |
|----------|---------------------|-------|-------|
| `GRID_IMPORT_TARIFF_PER_MW` | 2.5 | External grid tariff per MW per tick | Must be > `GRID_EXPORT_TARIFF_PER_MW` (1.6); exact value TBD by balance pass |
| `GRID_IMPORT_ENABLED_DEFAULT` | `true` | New cities start with external cover on | Mechanical; affects genesis replay and new-game flow only |
| `GRID_EXPORT_TARIFF_PER_MW` (existing) | 1.6 | Mirror: ensures import tariff > export to justify local investment | No change; referenced for invariant check |

**Local cheapest-plant amortised cost:** derived at test-time from `webconsole/src/sim/data.ts` SPECS (the power plant with the lowest `cost ÷ mw ÷ lifespan` ratio), never hardcoded. The invariant check asserts `GRID_IMPORT_TARIFF_PER_MW > local_cheapest_amortised_£/MW/tick`.

---

## Acceptance Criteria

### AC-1 (new city defaults to grid import enabled)

**Scenario:** Genesis replay (new game, no actions yet). Check: after the initial state is laid down (tick 0), state carries a flag (or field) recording grid-import external cover is **on**:
- `state.gridImportEnabled === true` (or equivalent state field name)
- On tick 1, when power need > capacity, no shortfall applies; instead, an outflow "Grid Import" appears in `lastFlows.outflows`

**Mutation:** set the default to `false`; a new city has external cover off. Import line does not appear even when need > cap. This test turns red.

**False-pass:** internal flag exists but is never consulted in the flow-computation path; Grid Import outflow appears unconditionally regardless of the toggle state (AC-2 and AC-5 catch this if they run first, but this AC must verify the flag's existence).

---

### AC-2 (grid import outflow = importedMW * tariff when enabled and shortage exists)

**Scenario:** Power system is online. At tick N:
- Capacity (from powerStats): 50 MW
- Need (from powerStats): 70 MW
- Grid import is enabled
- `GRID_IMPORT_TARIFF_PER_MW` = 2.5

Check: `lastFlows.outflows` contains an entry:
- `label === 'Grid Import'` (exact label, case-sensitive)
- `value === Math.round((70 - 50) * 2.5)` = Math.round(50) = 50 (the shortfall 20 MW @ 2.5 per MW)

If capacity >= need (no shortage), the "Grid Import" line **does not appear** (not a zero-value entry, but absent entirely — consistent with Grid Export behaviour, fiscal.ts:511-513).

**Mutation:** change the tariff calculation to `importedMW * 1.0` (cheaper rate). The value becomes 20 instead of 50. Test goes red: the outflow is undercharged.

**False-pass:** outflow is present but value is wrong (e.g., `Math.round(needMW * tariff)` instead of `(needMW - capMW) * tariff`, which double-charges). Consistency checks in AC-7 catch this if run second, but this AC must verify the formula's correctness directly.

---

### AC-3 (grid import disabled → no outflow, legacy shortage applies)

**Scenario:** Power system at shortfall (need 70 MW, cap 50 MW). Grid import toggle is **off** (`state.gridImportEnabled === false`).

Check:
- `lastFlows.outflows` does **not** contain a "Grid Import" entry (import line is absent, not present with zero value)
- Brownout (BUG-393) applies unchanged: `brownoutOf(state)` returns an active brownout, and income flows (Business Tax, Freight Tax, Office Tax) are multiplied by `brownout.incomeFactor` as before (fiscal.ts doesn't change; the legacy path is unmodified)
- Byte-identical to pre-Grid-Import behaviour: the outflow budget and income penalties must match a run with Grid Import feature entirely absent

**Mutation:** delete the grid-import toggle check; import line appears regardless of state. Brownout still fires (double-penalty to income). Test goes red: two distinct penalty mechanisms fire simultaneously.

**False-pass:** toggle is checked but brownout logic is skipped/disabled when toggle is off; the two paths do not coexist as documented.

---

### AC-4 (tariff invariant: import > export > amortised local)

**Scenario:** Load the power plant catalogue and compute the cheapest local plant's amortised cost: `cost_per_plant ÷ (mw_per_plant * lifespan_in_ticks)`. Let this be `L` (£/MW/tick).

Check at test-time (not during gameplay):
- `GRID_IMPORT_TARIFF_PER_MW > GRID_EXPORT_TARIFF_PER_MW` → import is more expensive than export revenue rate
- `GRID_EXPORT_TARIFF_PER_MW > L` → export price exceeds amortised cost of the cheapest local plant (the payback hurdle)
- Thus: `GRID_IMPORT_TARIFF_PER_MW > L`, proving local investment pays back over time if import tariff is high enough

Mechanically: a utility function `verifyGridTariffInvariant()` loaded at test-time asserts these three inequalities and reports which plant is cheapest (log: "power_wind is the cheapest at £X/MW/tick").

**Mutation:** flip the invariant check to `GRID_IMPORT_TARIFF_PER_MW < GRID_EXPORT_TARIFF_PER_MW` (import cheaper than export, nonsensical). The test utility fails its assertion.

**False-pass:** constants are defined but never validated; test-suite runs without the invariant check and accepts an incoherent tariff regime.

---

### AC-5 (toggle persists in saves; genesis replay respects toggle state)

**Scenario:**
1. City is at tick 500 with `gridImportEnabled = false`. Player saves.
2. Load the save: state.gridImportEnabled must be `false` (toggle value is serialised).
3. Genesis replay: load an action journal from a run where the toggle was set to `false` at tick 50. Replay the journal: at tick 50 on replay, the toggle is again `false`; import outflow does not appear.

Check:
- SaveLoad (not yet implemented in this increment; see Out of Scope): SimState is a serialisable value, so `gridImportEnabled` is serialised as a boolean field.
- Genesis Replay (FEAT-1972079897): the toggle state is journaled (if it ever changes) or defaults per `GRID_IMPORT_ENABLED_DEFAULT`; replaying the journal reproduces the toggle state at every replay tick.
- After replay, `state.gridImportEnabled` matches the original run's value at the same tick.

**Mutation:** remove the toggle field from the save format; deserialized state defaults `gridImportEnabled` to `true` even if the original was `false`. On replay, the toggle is lost. Test goes red: toggle does not persist.

**False-pass:** toggle is in state but is not serialised; it is reconstructed from a hardcoded default rather than a saved value, so a save → load → toggle-check fails.

---

### AC-6 (money conservation: import outflow debited exactly once per tick)

**Scenario:** Tick N with power shortage and import enabled. Check conservation (FEAT-1972079890, fiscal.ts):

Let:
- `fundsAtTickStart` = state at tick N before flows
- `inflows` = all recorded inflows (tax, tourism, etc.) at tick N
- `outflows` = all recorded outflows (upkeep, wages, Grid Import, etc.) at tick N
- `fundsAtTickEnd` = state after all flows are applied

The invariant must hold:
```
fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows
```

Specifically:
- The "Grid Import" outflow is included in `Σoutflows` exactly once
- No double-debit (it does not appear twice in the outflows array)
- No side-channel debit (the import cost is not charged twice—once as outflow, once as a separate funds mutation)

**Mutation:** debit the import cost to `state.funds` directly during power-stats computation, **and also** add it to `lastFlows.outflows`. The outflow is counted twice. Conservation invariant fails: `Σoutflows` over-counts, and the checker detects a larger outflow sum than funds decrease.

**False-pass:** import is debited via flows only, but consistency code has a buggy recompute that double-counts or skips it, so the checker passes despite the code being correct (a false-pass of the checker, but AC must verify the engine code is right, not just that a checker agrees with itself).

---

### AC-7 (insolvency path unaffected; import is a normal outflow)

**Scenario:** City is solvent, but Grid Import outflow (along with other costs) pushes funds into negative territory in a single tick. Check:

- The insolvency transition fires per the existing fiscal.ts logic (funds <= `DEBT_THRESHOLD_FOR_BAILOUT` → 'crisis' state in `insolvencyStateForFunds`)
- The Grid Import outflow is treated identically to any other outflow: it is summed into the total, triggers insolvency if it tips the balance, and is subject to `applyOutflowPolicies` (recycling/austerity discounts, if any)
- No special exemption: import is not excluded from the insolvency calculation, nor does it bypass policy modifiers

**Mutation:** exempt Grid Import from insolvency triggers (e.g., `if (label === 'Grid Import') return state` when checking whether insolvency fires). City can carry negative funds indefinitely if the shortfall is large. Insolvency logic is broken.

**False-pass:** insolvency fires correctly, but for a different reason (e.g., wages alone tipped it); the import cost is not actually tested in the path that triggers crisis.

---

### AC-8 (finance panel row: "Grid Import" displays current MW + cost per tick)

**Scenario:** RightDock.tsx Finance/Earnings tab is open. Power shortage is active; import is enabled. Check:

The finance panel renders (or the ledger view shows) a row:
- **Label:** "Grid Import" (exact string match)
- **Gross/tick:** `fmtMoney(gridImportOutflowValue)` where `gridImportOutflowValue` is the value recorded in `lastFlows.outflows` from AC-2 (e.g., £50)
- Row is displayed **only when import is occurring** (capacity < need; if no shortage, row is absent, per AC-2)

**Related:** if a power-panel component exists (displaying MW, capacity, need), it must also show:
- **Imported MW:** `max(0, needMW - capMW)` when import is on (e.g., "20 MW imported")
- **Imported MW:** 0 when import is off

Check: a screenshot test or a mount test verifies the label and value are rendered.

**Mutation:** remove the "Grid Import" row from the panel. Player sees no ledger entry for import costs; the funds decrease is unexplained.

**False-pass:** row is rendered but value is stale (from a previous tick, not the current tick's `lastFlows`); the player sees an import cost from tick N−1 while playing tick N.

---

### AC-9 (power panel shows grid import toggle + status)

**Scenario:** A Power or Resources panel exists in the UI (either on RightDock or a separate modal). Check:

- A toggle switch / checkbox / button is present: "Use external power cover" (label TBD)
- Current state is displayed: toggle is ON or OFF, matching `state.gridImportEnabled`
- Clicking the toggle calls an action to set `gridImportEnabled` to the opposite value
- On the next tick, the new toggle state is respected: if toggled off, no import outflow appears

**Mutation:** remove the toggle from the UI. Player has no way to disable import; it is hardcoded on.

**False-pass:** toggle is present but clicking it does not dispatch an action; the toggle's UI state does not persist to the simulation state.

---

### AC-10 (toggle persists across UI interactions; state is not volatile)

**Scenario:** Player toggles external cover off (tick 100). Player clicks elsewhere, closes panels, reopens the power panel. Check: the toggle is **still off** (state.gridImportEnabled remains false).

**Mutation:** toggle is stored in a React local state (useState) instead of the Redux/sim state. Closing a panel clears the local state; reopening the panel resets the toggle to the default.

**False-pass:** toggle appears to persist across a single session, but is not persisted to saved games (caught by AC-5, not AC-10).

---

### AC-11 (determinism: same power shortage → same import cost)

**Scenario:** Replay a save from tick 50 to tick 150, passing through various power shortage states. Check:

- Every tick N where `powerStats.need > powerStats.cap`, the computed import outflow matches the original run's value exactly
- No `Math.random()`, wall-clock, or non-deterministic inputs are used in import calculation
- Two identical action sequences → identical import costs at every replay tick
- Byte-identical `lastFlows.outflows` array (including "Grid Import" entry values and ordering)

Mechanically: import is computed **purely from state** (power stats, tariff constant) via `computeFlows()`, never from a cache or a wall-clock timer.

**Mutation:** introduce `Math.random() * 0.1` into the tariff calculation (`importedMW * tariff * (1 + Math.random() * 0.1)`). Replay shows different costs for the same shortage. Test goes red.

**False-pass:** tariff is deterministic, but `powerStats.need` or `powerStats.cap` is computed non-deterministically (caught by existing power-panel AC-12 in other features, but AC-11 here must verify the import path is clean).

---

### AC-12 (regression: existing tests still pass; brownout income penalty is unchanged when import is off)

**Scenario:** A pre-existing test (e.g., from BUG-393, FEAT-1972079906) verifies that a power shortage without external cover applies an income penalty to powered businesses. Re-run that test with the new Grid Import code in place:

Check: the test still passes with **identical results** (same income multiplier, same brownout factor). The test fixture explicitly sets `gridImportEnabled = false` or uses the toggle-disable action before verifying brownout. No regression: the legacy path is byte-identical.

**Mutation:** remove or weaken the brownout logic. Income penalty no longer applies (or applies inconsistently). Test goes red: regression detected.

**False-pass:** test passes because it was already broken (e.g., expected value was wrong before Grid Import was added); adding Grid Import doesn't change the test's outcome but only by accident.

---

## Files Expected to Change

- `webconsole/src/sim/fiscal.ts` — add `GRID_IMPORT_TARIFF_PER_MW` (2.5, PLACEHOLDER) and `GRID_IMPORT_ENABLED_DEFAULT` constants; add tariff-invariant validation function for tests
- `webconsole/src/sim/engine.ts` — in `computeFlows()`, after Grid Export block (line 506–513), add Grid Import block: check `state.gridImportEnabled` and power shortage, compute `importedMW = Math.max(0, need - cap)`, conditionally push `{ label: 'Grid Import', value: Math.round(importedMW * GRID_IMPORT_TARIFF_PER_MW) }` to inflows/outflows
- `webconsole/src/sim/types.ts` — add `gridImportEnabled: boolean` field to `SimState` interface
- `webconsole/src/sim/data.ts` — no changes (powerStats is already exported and deterministic)
- `webconsole/src/components/right/RightDock.tsx` — in Finance/Earnings tab, ensure "Grid Import" line is displayed when present in `lastFlows.outflows`; add a Power panel (or extend an existing one) to show imported MW and the toggle switch
- `webconsole/src/sim/store.tsx` or action-dispatch layer — add action handler to toggle `gridImportEnabled` and dispatch a new tick immediately so the toggle takes effect on the next frame

---

## Out of Scope (inc2+)

- Water/waste-water/garbage import (FEAT-2326609711 inc2)
- Fire/police/health external coverage (FEAT-2326609711 inc3)
- Wind turbine resizing / affordable first local investment (FEAT-2326609711 inc4)
- Go engine mirror (FEAT-2326609711 inc5)
- UI/UX design of power panel (layout, colors, accessibility — AC covers data, not design)
- SaveLoad persistence test (assumes saves preserve `gridImportEnabled`; FEAT-1972079897 implementation TBD)
- Genesis Replay detailed action journaling (assumes journal carries toggle state; mechanics TBD with FEAT-1972079897)
- Tariff tuning / balance pass (Aaron row-by-row approval pending)

---

## Open Questions for Aaron

1. **Toggle persistence in saves:** Should the toggle state be journaled as an action (like placing a building) or stored as a passive state field? (Interim: stored as passive state field, serialised with SimState.)

2. **Toggle UX location:** Should the toggle be in the Power panel (component TBD), in the Policies tab alongside other city-wide toggles, or in a separate Utilities/Coverage panel? (Interim: a Power panel or extension to an existing resource panel.)

3. **Default ON vs. OFF:** The brief specifies new cities start with external cover **on**. Is this the intended first-time-player experience? (Interim: yes, default on; toggle off is available for challenge runs.)

4. **Tariff formula:** Should the import tariff be a fixed constant per MW/tick (current design, AC-2), or should it scale with city wealth / population / demand (more complex, deferred to balance pass)?

5. **Quality/satisfaction penalty:** Does buying external power carry a player-felt quality penalty (slower response, citizen satisfaction drop) in addition to the price premium, or is the higher cost alone the punishment? (Interim: price only; quality effects deferred to future increments.)

---

## Increments

### Inc1: Grid Import (power only) — this document

- AC-1: new city defaults to external cover on
- AC-2: import outflow formula (need − cap) × tariff
- AC-3: toggle off → legacy brownout (regression test)
- AC-4: tariff invariant verification
- AC-5: toggle persists in saves + genesis replay
- AC-6: money conservation (outflow debited once)
- AC-7: insolvency path unaffected
- AC-8: finance panel shows import line
- AC-9: power panel shows toggle + status
- AC-10: toggle state persists across UI interactions
- AC-11: determinism (same shortage → same cost)
- AC-12: regression (brownout unchanged when import off)

**Deliverables:**
- fiscal.ts: `GRID_IMPORT_TARIFF_PER_MW`, `GRID_IMPORT_ENABLED_DEFAULT`, `verifyGridTariffInvariant()`
- engine.ts: `computeFlows()` Grid Import block; conditional on `state.gridImportEnabled` and power shortage
- types.ts: `gridImportEnabled: boolean` field on SimState
- RightDock.tsx: Finance panel "Grid Import" row; Power panel toggle + MW readout
- store reducer: toggle action handler

**Gate:** `npm test` green (AC-1 through AC-12 unit/mount/integration tests); manual dogfood: place a power plant, create a shortage, verify import line appears in finance panel, toggle import off and verify brownout returns, toggle on and verify import returns.

---

**End of AC. Report when done. File path: `E:/git/Metropolis/docs/planning/acceptance/feat-2326609711-inc1-grid-import.md`**
