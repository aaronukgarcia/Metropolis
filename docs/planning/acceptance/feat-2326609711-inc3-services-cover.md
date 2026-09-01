# FEAT-2326609711 inc3: Services Buy-In (Fire, Police, Health)

**Mkey:** FEAT-2326609711  
**Epic:** External provision buy-in vs local funding (Baseline One—watchable deterministic year)

**Relates to:** MOD-051 (fire), MOD-052 (police), MOD-048 (health), FEAT-1972079890 (fiscal SSOT), FEAT-2326609711 inc1 + inc2 (external cover pattern)

**GR#25 scope:** webconsole-internal `fiscal.ts` / `engine.ts` / `types.ts` / `data.ts` / `RightDock.tsx`. No new code.json edges. Extends inc1/inc2 external-cover mechanic to three emergency services: fire, police, and health.

---

## Design Ruling (Aaron, 2026-09-01)

Extends inc1 and inc2's external-cover pattern to **three emergency services**: fire rescue, policing, and health (GP + hospital combined). New cities default to **external cover enabled** for all three. When local capacity falls short of demand, the city buys the difference at external tariffs (per person served/treated, per tick) that are **strictly higher** than the amortised local cost. Player can toggle each service independently. When off, the legacy shortage behaviour (no service, penalty via wellbeing/demand calculations) applies unchanged. External tariffs are charged as finance **outflow lines** labelled exactly "Contracted Fire Cover", "Contracted Policing", "Contracted Health" and persisted in saves and deterministic replays.

---

## Placeholder Constants (balance-number regime)

All tariffs are **named constants** sourced from `fiscal.ts` for Aaron's row-by-row approval. Tests reference exports, never hardcoded.

| Constant | Value (placeholder) | Usage | Notes |
|----------|---------------------|-------|-------|
| `FIRE_COVER_TARIFF_PER_PERSON_PER_TICK` | 0.12 | External fire cover tariff per person served per tick | Must be > local-cheapest fire station amortised cost/person/tick |
| `POLICE_COVER_TARIFF_PER_PERSON_PER_TICK` | 0.15 | External police cover tariff per person served per tick | Must be > local-cheapest police station amortised cost/person/tick |
| `HEALTH_COVER_TARIFF_PER_PERSON_PER_TICK` | 0.18 | External health cover tariff per person served per tick (unified GP + hospital) | Must be > local-cheapest health facility amortised cost/person/tick |
| `EXTERNAL_COVER_DEFAULTS` (extended) | `{ fire: true, police: true, health: true, ... }` | New cities start with all covers on | Mechanical; affects genesis replay and new-game flow only |

**Local cheapest-plant amortised cost:** derived at test-time from data.ts SPECS (fire station with lowest `cost ÷ served ÷ lifespan`; police station; health facility combining GP clinic and hospital by served population). Tariff invariant checks assert each tariff > local_cheapest_amortised_£/person/tick.

---

## Acceptance Criteria

### AC-1 (new city defaults to all external covers on for fire/police/health)

**Scenario:** Genesis replay. Check: after initial state, state carries `externalCover` field with fire, police, health **enabled**:
- `state.externalCover.fire === true`
- `state.externalCover.police === true`
- `state.externalCover.health === true`

On tick 1, when fire/police/health need > capacity, no shortage applies; instead, outflow "Contracted Fire Cover"/"Contracted Policing"/"Contracted Health" appears in `lastFlows.outflows`. 

**Mutation:** set all defaults to `false`; new city has external covers off. Import lines do not appear. Test goes red.

**False-pass:** flag exists but is never consulted in outflow-computation paths.

---

### AC-2 (fire cover outflow = (needPersons − capPersons) × tariff when enabled)

**Scenario:** Fire system. At tick N:
- Population needing fire cover: 100,000 people
- Fire service capacity (sum of `served` from all fire stations): 60,000 people
- External cover is enabled
- `FIRE_COVER_TARIFF_PER_PERSON_PER_TICK` = 0.12

Check: `lastFlows.outflows` contains entry:
- `label === 'Contracted Fire Cover'` (exact label)
- `value === Math.round((100000 − 60000) × 0.12)` = Math.round(4800) = 4800 (the shortfall 40,000 people × 0.12)

If capacity >= need (no shortage), line is absent (not zero-value).

**Mutation:** tariff calculation uses `needPersons * 0.06` (cheaper). Value becomes 6000 instead of 4800. Test goes red.

**False-pass:** outflow present but value wrong (double-counted or using wrong formula).

---

### AC-3 (police cover outflow = (needPersons − capPersons) × tariff when enabled)

**Scenario:** Police system. At tick N:
- Population needing police: 100,000 people
- Police capacity (sum of `served` from all police stations/HQs): 50,000 people
- External cover is enabled
- `POLICE_COVER_TARIFF_PER_PERSON_PER_TICK` = 0.15

Check: `lastFlows.outflows` contains entry:
- `label === 'Contracted Policing'` (exact label)
- `value === Math.round((100000 − 50000) × 0.15)` = Math.round(7500) = 7500 (the shortfall 50,000 people × 0.15)

If capacity >= need (no shortage), line is absent (not zero-value).

**Mutation:** tariff calculation uses `needPersons * 0.08` (cheaper). Value becomes 8000 instead of 7500. Test goes red.

**False-pass:** outflow present but value wrong (double-counted or using wrong formula).

---

### AC-4 (health cover outflow = (needPersons − capPersons) × tariff when enabled; unified GP + hospital)

**Scenario:** Health system (GP clinics + hospitals combined). At tick N:
- Population needing health services: 150,000 people
- Health capacity (sum of `served` from all GP clinics + hospitals): 80,000 people
- External cover is enabled
- `HEALTH_COVER_TARIFF_PER_PERSON_PER_TICK` = 0.18

Check: `lastFlows.outflows` contains entry:
- `label === 'Contracted Health'` (exact label)
- `value === Math.round((150000 − 80000) × 0.18)` = Math.round(12600) = 12600 (the shortfall 70,000 people × 0.18)

If capacity >= need (no shortage), line is absent (not zero-value).

**Mutation:** tariff calculation uses `needPersons * 0.09` (cheaper). Value becomes 13500 instead of 12600. Test goes red.

**False-pass:** outflow present but value wrong (double-counted or using wrong formula).

---

### AC-5 (external cover disabled → no outflow, legacy behaviour applies)

**Scenario:** Each service separately: fire at shortfall (need 100,000, cap 60,000). Cover toggle is **off** (`state.externalCover.fire === false`).

Check:
- `lastFlows.outflows` does **not** contain "Contracted Fire Cover" (absent, not zero-value)
- Fire shortage effects applied via legacy path (demand index in serviceDemandOf increases, wellbeing part tracks coverage ratio, no external cover substitutes)
- Byte-identical to pre-external-cover behaviour for this service

**Mutation (same for police/health):** remove the toggle check; import line appears regardless. Test goes red: double-penalty.

**False-pass:** toggle checked but no regression verification against pre-feature code path.

---

### AC-6 (service coverage integrated into serviceCoverageOf; fire is NEW)

**Scenario:** SEAM CHECK (one-time, not a regression test). Call `serviceCoverageOf(state)` and verify:
- The returned array includes entries with `id === 'fire'`, `id === 'police'`, `id === 'health'`
- Fire: `need = population`, `cap = Σ(spec.served for all fire stations)`, `coverage = cap / need`
- Police: `need = population`, `cap = Σ(spec.served for all police stations/HQs)`, `coverage = cap / need`
- Health: `need = population`, `cap = Σ(spec.served for all GP clinics + hospitals)`, `coverage = cap / need`
- Fire is a NEW service (not present in pre-inc3 code); police and health combine into unified entries per the unified-cap model

**Mutation:** remove fire from the coverage array. Fire shortfall cannot be computed; external cover outflow is undefined. Test goes red.

**False-pass:** fire entry present in the array but with wrong formula (e.g., cap is a facility count instead of population-served sum).

---

### AC-7 (tariff invariant per service)

**Scenario:** Load each service's cheapest local facility (data.ts SPECS). Compute amortised cost:
- Fire: cheapest fire station `cost ÷ served ÷ lifespan_ticks`
- Police: cheapest police station `cost ÷ served ÷ lifespan_ticks`
- Health: cheapest health facility (min of GP clinic and hospital on per-person basis) `cost ÷ served ÷ lifespan_ticks`

Check at test-time:
- `FIRE_COVER_TARIFF_PER_PERSON_PER_TICK > local_fire_cheapest_£/person/tick`
- `POLICE_COVER_TARIFF_PER_PERSON_PER_TICK > local_police_cheapest_£/person/tick`
- `HEALTH_COVER_TARIFF_PER_PERSON_PER_TICK > local_health_cheapest_£/person/tick`

Utility function `verifyServicesTariffInvariants()` asserts all three and logs cheapest per type.

**Mutation:** flip one invariant; tariff < amortised. Test fails.

**False-pass:** constants defined but never validated.

---

### AC-8 (toggles persist in saves; genesis replay respects state)

**Scenario:** At tick 500, player sets `externalCover.fire = false` and `externalCover.police = true`. City saves. Load save: both toggles must match saved values. Genesis replay: same toggle state at same replay tick.

Check:
- SimState `externalCover` is serialisable (plain boolean fields)
- Toggles round-trip through save/load/replay byte-identically

**Mutation:** remove toggles from save format. Loaded state defaults all covers to true. Test goes red.

**False-pass:** toggled but not serialised; reconstructed from hardcoded default on load.

---

### AC-9 (money conservation: outflows debited exactly once per tick)

**Scenario:** Tick N with fire/police/health shortfall and covers enabled. Check conservation (FEAT-1972079890, fiscal.ts):

```
fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows
```

Specifically:
- Each service cover outflow is included in `Σoutflows` exactly once
- No double-debit (not in array twice, not side-channel)

**Mutation:** debit cover cost to `state.funds` directly **and** add to outflows. Double-counted. Test fails: Σoutflows over-counts.

**False-pass:** debited via flows only, but consistency checker has a buggy recompute.

---

### AC-10 (insolvency path unaffected; covers are normal outflows)

**Scenario:** City solvent, but external cover outflows push funds negative. Check:
- Insolvency fires per fiscal.ts logic (funds <= DEBT_THRESHOLD → 'crisis')
- Covers treated identically to other outflows: summed, trigger insolvency, subject to policy modifiers
- No special exemption for fire/police/health covers

**Mutation:** exempt covers from insolvency logic. City carries negative funds indefinitely. Test goes red.

**False-pass:** insolvency fires for a different reason (wages alone); covers never actually tested in the crisis path.

---

### AC-11 (finance panel shows cover lines when occurring)

**Scenario:** RightDock.tsx Finance/Earnings tab. Shortfall active; cover enabled. Check:
- Row present: label "Contracted Fire Cover"/"Contracted Policing"/"Contracted Health" (exact match, case-sensitive)
- Value: current tick's cover cost from `lastFlows.outflows`
- Row absent when no shortfall (per AC-2/AC-3/AC-4)

**Mutation:** remove cover rows. Player sees no ledger entry; funds decrease unexplained.

**False-pass:** row rendered but value stale (from previous tick).

---

### AC-12 (services panel shows toggles + status)

**Scenario:** A Services panel exists in UI (Resources or separate modal, consistent with inc1 PowerTab location per Aaron's design ruling: RightDock, Fire/Police/Health subsection). Check:
- Toggle for each service: "Use external fire cover", "Use external police cover", "Use external health cover"
- Current state matches `state.externalCover.[fire/police/health]`
- Clicking toggles sets opposite value; next tick respects new state

**Mutation:** remove toggles. Player has no way to disable; hardcoded on.

**False-pass:** toggle present but clicking does not dispatch action.

---

### AC-13 (toggle state persists across UI interactions; not volatile)

**Scenario:** Player toggles fire cover off (tick 100). Closes panel, reopens. Check: toggle still off.

**Mutation:** stored in React local useState instead of sim state. Closing panel clears local state; toggle resets to default.

**False-pass:** persists within a single session; not persisted to saves (caught by AC-8).

---

### AC-14 (determinism: same shortage → same cover cost)

**Scenario:** Replay a save, passing through various shortfall states for each service. Check:
- Every tick N where service demand > capacity, computed cover cost matches original exactly
- No Math.random(), wall-clock, non-deterministic inputs
- Byte-identical `lastFlows.outflows` array

Mechanically: covers computed purely from state (demand, capacity, tariff constant) via `computeFlows()`, never from cache or wall-clock.

**Mutation:** introduce `Math.random() * 0.1` into tariff. Replay shows different costs for same shortage. Test goes red.

**False-pass:** tariff deterministic, but demand/capacity computed non-deterministically (caught by existing demand-panel tests, but must verify cover path is clean).

---

### AC-15 (existing-city loads default new fields explicitly; no silent economy change)

**Scenario:** Load a pre-existing save (no `externalCover.fire/police/health` fields, only inc1/inc2 water fields). Check:
- Replayed save receives `externalCover` with fire/police/health defaults applied: `{ ..., fire: true, police: true, health: true }` (NEW cities default on; old cities loaded with defaults on to match player expectation — no breakage)
- Legacy shortage behaviour at tick 0: if old save was short of fire/police/health, it remains short until next tick (no retroactive cover applied to the loaded-state instant)
- Document the replay-divergence rule: an undefined field on an old savepoint must NOT silently change its economy

**Mutation:** remove default-assignment for undefined fire/police/health fields. Old saves fail to load or crash when checking toggles.

**False-pass:** old saves load but toggles are undefined; comparisons crash mid-tick.

---

### AC-16 (wellbeing parts: safety (police) and healthcare already exist; fire is NEW)

**Scenario:** INTEGRATION CHECK. Call `wellbeingOf(state)` and verify wellbeing parts array includes:
- `{ label: 'Safety', value: part(ratio('police')) }` — existing police part, now supplied via external cover toggle  
- `{ label: 'Healthcare', value: part(ratio('gp')) }` — existing GP part, supplies health cover shortfall calculation
- `{ label: 'Hospital care', value: part(ratio('hosp')) }` — existing hospital part, supplies health cover shortfall calculation
- NEW: `{ label: 'Fire & Rescue', value: part(ratio('fire')) }` — new fire part, fires when fire shortfall is active (no external cover)

When a service's external cover is enabled, its corresponding wellbeing part REMAINS unchanged (the shortfall is substituted, not the penalty). When off, the part reflects the shortfall via `demandIndexOf(coverage)`.

**Mutation:** remove fire from wellbeing parts. No wellbeing impact from fire shortfall; a city on fire has no penalty.

**False-pass:** fire part present but its value is hard-wired (always 100) instead of tracking coverage ratio.

---

### AC-17 (SSOT: one shared predicate gates income penalty, wellbeing penalty, and demand display)

**Scenario:** BUG-393 inc1 r1 REJECT lesson: the half-wired defect gated income by external cover, but wellbeingOf and the DemandDock banner kept applying the legacy collapse. All three consumers must read ONE shared predicate, never recompute locally.

**SEAM CHECK (implementation requirement, not a regression test):** Create a SINGLE SOURCE OF TRUTH predicate function (e.g., `serviceShortfallActive(state, serviceId)`) in `data.ts` that returns true iff the service IS short (external cover OFF and need > capacity) and false iff the service is NOT short (external cover ON or no shortfall). Then verify that every code path reading shortfall uses THIS function, not a locally-recomputed condition:

- `computeFlows()` in `engine.ts` (~line 540+): gates Fire/Police/Health cover outflow. Must call `serviceShortfallActive(s, 'fire/police/health')` before pushing to outflows; never recompute `s.externalCover.fire || (need > cap)` inline.
- `wellbeingOf()` in `engine.ts` (~line 3370+): computes Fire/Police/Health wellbeing parts. Must call `serviceShortfallActive(s, serviceId)` to check whether to apply penalty via `demandIndexOf(coverage)`; never recompute inline.
- `serviceDemandOf()` in `data.ts` (~line 2044+): computes demand index per service. Must call `serviceShortfallActive()` to gate the demand value; never recompute.
- Any banner/status display in `webconsole/src/components/` (RightDock.tsx Finance/Services panels, DemandDock.tsx if services-demand row exists, etc.): must display shortfall status ONLY if `serviceShortfallActive()` is true; never show a banner that says "shortfall active" while external cover is ON.

**Check:** Write a regression test harness:
1. Set external cover for a service to ON, create a shortage (need > cap).
2. Call the predicate: `serviceShortfallActive(state, serviceId)` returns **false**.
3. Verify that ALL of the following return "no shortage" / "no penalty":
   - `computeFlows()` does NOT include a cover outflow for that service (per AC-2/AC-3/AC-4).
   - `wellbeingOf(state).parts.find(p => p.label === '[Service name]').value` is computed as if coverage >= 1 (per AC-16, no penalty applied).
   - `serviceDemandOf(state).find(d => d.id === '[serviceId]').value` is <= 0 (per data.ts AC-5, no demand shown).
4. Toggle external cover OFF (same shortage persists).
5. Call the predicate: `serviceShortfallActive(state, serviceId)` returns **true**.
6. Verify that ALL of the following return "shortage active" / "penalty applies":
   - (Not tested here; AC-2/AC-3/AC-4 and AC-16 test these independently; this AC just verifies the predicate gates all of them identically.)

**Mutation #1 (income limb gate only):** Delete the predicate call from `wellbeingOf()` and hardcode the wellbeing penalty to always apply based on raw coverage (ignoring external cover). Result: `wellbeingOf` shows penalty even though `computeFlows` gates the outflow (the inc1 r1 defect). Test goes red: predicate not SSOT.

**Mutation #2 (banner reads raw condition):** Add a banner in RightDock that says "Fire shortage!" if `serviceCoverageOf().find(s => s.id === 'fire').coverage < 1`, not using the predicate. Toggle external cover ON with a shortage active. Result: banner still shows "Fire shortage!" while cover is ON (the inc1 r1 defect). Test goes red: banner is not gated by the predicate.

**False-pass:** predicate exists but is only called by one consumer (e.g., `computeFlows` only), while `wellbeingOf` recomputes locally. The test might pass the outflow AC but fail the wellbeing AC if run separately (AC-16 would catch this), but AC-17 must verify they all agree ON THE SAME PREDICATE.

---

## Files Expected to Change

- `webconsole/src/sim/fiscal.ts` — add tariff constants (`FIRE_COVER_TARIFF_PER_PERSON_PER_TICK`, etc.) and `verifyServicesTariffInvariants()` function for tests
- `webconsole/src/sim/engine.ts` — in `computeFlows()` after utilities/water block (line ~540+), add Fire/Police/Health cover blocks: check `state.externalCover.[fire/police/health]` and demand vs capacity, compute shortfall, conditionally push cover outflows
- `webconsole/src/sim/types.ts` — add `fire?: boolean`, `police?: boolean`, `health?: boolean` fields to `externalCover` struct (optional for backward tolerance)
- `webconsole/src/sim/data.ts` — in `serviceCoverageOf()`, add three new rows: fire (need = pop, cap = Σ fire.served), police (now via all police stations/HQs by `kind`, already done in inc2 but confirm), health (unified: need = pop, cap = Σ gp.served + Σ hosp.served). Add fire to `wellbeingOf()` parts array. **NEW:** Create `serviceShortfallActive(state, serviceId)` predicate function (SSOT for all shortfall checks per AC-17).
- `webconsole/src/components/right/RightDock.tsx` — Finance panel displays cover lines when present; Services panel (or Resources sub-tab) shows three toggles + status readouts (covered persons, shortfall if any)
- `webconsole/src/sim/store.tsx` or action layer — add action handlers to toggle each cover and dispatch tick

---

## Out of Scope (inc4+)

- Wind turbine sizing / affordable first local investment (FEAT-2326609711 inc4; see BUG-477)
- Capacity ceiling scaling with city size (Aaron design: inc5)
- Quality/satisfaction penalty on external cover (Aaron design: inc6)
- Education buy-in (inc3b, separate increment)
- Go engine mirror (inc5)
- SaveLoad detailed implementation (assumes SimState serialisation)
- Genesis Replay action journaling (assumes journal preserves toggle state)
- UI/UX design (layout, colours, accessibility — AC covers data only)
- Tariff tuning / balance pass (Aaron row-by-row approval pending)

---

## Open Questions for Aaron

1. **Health unification:** Should health external cover be a single unified toggle/outflow covering both GP + hospital (current design), or two separate toggles (one per facility type)? (Interim: unified, matching the way coverage is computed as `gp + hosp served`.)

2. **Fire integration into wellbeingOf:** Should fire shortfall apply a wellbeing penalty symmetrically to police/health, or remain display-only for now (since fire is NEW to the coverage system)? (Interim: add fire part to wellbeing array, following the same coverage→penalty formula as police/health.)

3. **Per-service defaults on old saves:** When loading a pre-existing save without fire/police/health `externalCover` fields, should all three default to on (matching new-game expectations, safest for player progression), or should they default to off (preserving original legacy-shortage behaviour)? **(ANSWERED 2026-09-01: all on; document as a replay-divergence exception, matching inc2's water/waste/refuse precedent.)**

4. **Tariff formula:** Should the cover tariff be a fixed constant per person/tick (current design, AC-2/AC-3/AC-4), or should it scale with city wealth / population / demand (more complex, deferred to balance pass)? **(ANSWERED 2026-09-01: fixed constant per-person from the catalogue, matching inc1/inc2 design.)**

### Post-inc3 Future Increments (Sequenced After FEAT-083 Baseline One)

The following Q100035 sub-questions define future health and emergency-response system increments, registered as BOW items and sequenced after the Baseline One spine per the northstar:

- **Q100035-1 Health-Chain System** (MOD-034 + new FEAT): Full health-chain spanning doctor access (local GP), ambulance services, hospital facilities, medical-stock warehousing, buildings-online dependency (people, power, water), health decline without attention → death or move-away, and air-ambulance sentiment bonus. Defined in BOW item as future P2 feature, sequenced post-FEAT-083.

- **Q100035-2 Emergency-Response SLA Mechanics** (MOD-040 + new FEAT): Per-service response time vs national-benchmark SLA (England fire ~7–10 min primary, police ~15 min urban, ambulance Category 1 ~7 min mean / Category 2 ~18 min mean per NHS England — PLACEHOLDER, needs verification against real UK standards before balance pass), traffic-dependent travel time computation, and SLA-miss consequences (unhappiness, buildings burn down, sickness/death). Defined in BOW item as future P2 feature, sequenced post-FEAT-083.

---

## Increments

### Inc1: Grid Import (power only) — COMMITTED
- 12 ACs covering power grid import pattern

### Inc2: Utilities Buy-In (water, wastewater, garbage) — COMMITTED
- 13 ACs covering three-utility pattern with unified `externalCover` struct

### Inc3: Services Buy-In (fire, police, health) — this document
- AC-1: new city defaults to all covers on for fire/police/health
- AC-2: fire cover outflow formula (need − cap) × tariff
- AC-3: police cover outflow formula (need − cap) × tariff
- AC-4: health cover outflow formula (need − cap) × tariff (unified GP + hospital)
- AC-5: covers disabled → legacy behaviour applies
- AC-6: services integrated into serviceCoverageOf(); fire is NEW
- AC-7: tariff invariant verification per service
- AC-8: toggles persist in saves + genesis replay
- AC-9: money conservation (outflows debited once)
- AC-10: insolvency path unaffected
- AC-11: finance panel shows cover lines
- AC-12: services panel shows toggles + status
- AC-13: toggle state persists across UI interactions
- AC-14: determinism (same shortage → same cost)
- AC-15: existing-city loads defaults explicitly (no silent economy change)
- AC-16: wellbeing parts integration; fire is NEW
- AC-17: SSOT predicate gates income penalty, wellbeing penalty, and demand display (BUG-393 inc1 r1 lesson)

**Deliverables:**
- fiscal.ts: `FIRE_COVER_TARIFF_PER_PERSON_PER_TICK`, `POLICE_COVER_TARIFF_PER_PERSON_PER_TICK`, `HEALTH_COVER_TARIFF_PER_PERSON_PER_TICK`, `verifyServicesTariffInvariants()`
- engine.ts: `computeFlows()` Fire/Police/Health cover blocks; conditional on `state.externalCover.[service]` and shortfall
- types.ts: `fire/police/health` boolean fields on `externalCover` struct
- data.ts: `serviceCoverageOf()` fire/police/health rows; `wellbeingOf()` fire part
- RightDock.tsx: Finance panel cover lines; Services panel toggles + readouts
- store reducer: toggle action handlers per service

**Gate:** `npm test` green (AC-1 through AC-17 unit/mount/integration tests); manual dogfood: place fire/police/health stations, create a shortfall, verify cover lines appear in finance panel, toggle covers off and verify demand-index penalties + wellbeing penalty + banner all return together (via SSOT predicate), toggle on and verify all three revert together.

---

## GR#25 Compliance Check

Running spec-lint before writing ACs:
- Baseline count (inc1/inc2 combined): 591 findings
- After inc3 addition: (to be measured after AC write-complete)

No new cross-module dependencies introduced. All references (fiscal.ts, engine.ts, types.ts, data.ts, RightDock.tsx, store.tsx) are webconsole-internal. Fire is a NEW service integration within the existing coverage system, not a new module edge.

---

**End of AC. Output file: `E:/git/Metropolis/docs/planning/acceptance/feat-2326609711-inc3-services-cover.md`**

**Lines: ~440** | **ACs: 17** | **GR#25: graph-internal only, webconsole-sim only, fire/police/health integrated into existing data.ts/engine.ts coverage paths. AC-17 SSOT enforcement is the BUG-393 inc1 r1 REJECT lesson applied to inc3.**

