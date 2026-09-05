// demanddock-overhaul.test.mjs — FEAT-demanddock-overhaul (BUG-571/BUG-572/
// FEAT-2326609735 unified): docs/planning/acceptance/FEAT-demanddock-overhaul-2026-09-02.md
//
// optimalProvider() (data.ts) replaces cheapestProvider() at both call sites:
// unlock-aware (BUG-571 fire) AND value-aware (FEAT-2326609735 "1 dam not 20
// towers"). DemandDock.tsx grows a refuse row (BUG-572 AC-1) and a dynamic
// sort with Health (gp+hosp) pinned top (BUG-572 AC-2/AC-3, Aaron-approved
// a1 reading: both rows pinned, sorted between themselves).
//
// Each test documents its RED-PROOF: what the pre-fix code would have done
// instead, so a revert of the fix reddens the assertion.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, levelOf, xpForLevel } from '../src/sim/engine.ts';
import {
  optimalProvider,
  serviceDemandOf,
  serviceCoverageOf,
  demandFixPlan,
  wasteStatsOf,
  pickAutoSpec,
  SPECS,
  AUTO_BUILD_DEMAND_FRACTION,
} from '../src/sim/data.ts';

/** Mirrors demand-fix.test.mjs's shortfallState(): real population, no service
 *  buildings, everything unlocked, ample funds — guarantees a real shortfall. */
function shortfallState(population, fundsOverride = 1_000_000_000) {
  const base = initialState();
  return { ...base, population, unlockedAll: true, funds: fundsOverride, administrationState: null };
}

/** Mirrors demand-fix.test.mjs's forceOnline(): a freshly-placed building is
 *  legitimately still "under construction" for a few ticks (isOnline(),
 *  data.ts), which is orthogonal to the AUTO_BUILD_DEMAND_FRACTION sizing
 *  arithmetic this file tests — force every building online (isOnline()'s
 *  documented `b.builtTick == null` escape hatch) so a "does this fully clear
 *  the shortfall" check reflects capacity math, not construction timing. */
function forceOnline(state) {
  return { ...state, buildings: state.buildings.map((b) => ({ ...b, builtTick: null })) };
}

/** BUG-685: count placements of EVERY spec in a (possibly mixed) plan, not
 *  just the primary/largest one — a largestFirstFill() mix places several
 *  specs, so a single-specId filter undercounts. Mirrors demand-fix.test.mjs's
 *  identical helper. */
function countMixPlaced(before, after, mix) {
  const ids = new Set(mix.map((m) => m.specId));
  const countOf = (bs) => bs.filter((b) => ids.has(b.spec)).length;
  return countOf(after) - countOf(before);
}

// ---------------------------------------------------------------------------
// AC-7/AC-8 — optimalProvider(): "1 dam not 20 towers".
// ---------------------------------------------------------------------------

test('AC-7: optimalProvider prefers the big affordable unit when its TOTAL plan cost genuinely beats the small unit\'s multi-unit plan', () => {
  // cleanwater shortfall 8,000: wat_tower (served 4,000, £2.7M) needs 2 units
  // -> £5.4M total; wat_clean (served 20,000, £4.68M) clears it in ONE unit
  // -> £4.68M total. wat_clean's WHOLE PLAN is cheaper, not just its
  // per-capacity ratio (the REJECTed draft's comparison, which never checked
  // this against a real multi-unit tower plan).
  // RED-PROOF: the pre-fix cheapestProvider() always returns wat_tower here
  // (lowest absolute sp.cost) — this assertion would fail under that code.
  const s = shortfallState(0, 1_000_000_000);
  const sp = optimalProvider(s, 'cleanwater', s.funds, 8_000);
  assert.ok(sp, 'a candidate must be found');
  assert.equal(sp.id, 'wat_clean', 'the big affordable unit wins because its total plan cost is genuinely lower');

  // Sanity: prove the total-cost comparison the AC requires.
  const towerUnits = Math.ceil(8_000 / SPECS.wat_tower.served);
  const towerTotal = towerUnits * SPECS.wat_tower.cost;
  assert.ok(SPECS.wat_clean.cost < towerTotal, `wat_clean (£${SPECS.wat_clean.cost}) must beat the ${towerUnits}-tower plan (£${towerTotal})`);
});

test('AC-8: optimalProvider falls back to the cheapest unlocked unit when no plan fits the budget wholesale', () => {
  // Same 8,000 shortfall, but budget covers only a single wat_tower (£2.7M) —
  // NEITHER wat_clean's plan (£4.68M) NOR wat_tower's own 2-unit plan (£5.4M)
  // NOR wat_reservoir's (£81M) fits the £3M budget wholesale, so the selector
  // falls back to the cheapest single-unit-AFFORDABLE spec (wat_tower) so
  // resolveDemand's downstream loop can still start placing something, per
  // Q100055 A1 (place as many as affordable).
  const s = shortfallState(0, 3_000_000);
  const sp = optimalProvider(s, 'cleanwater', s.funds, 8_000);
  assert.ok(sp);
  assert.equal(sp.id, 'wat_tower', 'unaffordable big-plan must fall back to the cheapest single-unit-affordable spec');
});

// ---------------------------------------------------------------------------
// Independent-round follow-up — the attacker's proven damage cases, re-run as
// permanent regressions against the total-plan-cost rewrite.
// ---------------------------------------------------------------------------

test('attacker case: cleanwater shortfall 20,000 must NOT pick the £81M reservoir when 6 towers (£16.2M) or 1 wat_clean is cheaper', () => {
  const s = shortfallState(0, 1_000_000_000);
  const sp = optimalProvider(s, 'cleanwater', s.funds, 20_000);
  assert.ok(sp);
  assert.notEqual(sp.id, 'wat_reservoir', 'RED-PROOF: the REJECTed draft picked wat_reservoir here — its £81.0M plan is 5x a 6-tower £16.2M plan');
  // Confirm the winner really is the cheapest total plan among the three.
  const totals = ['wat_tower', 'wat_clean', 'wat_reservoir'].map((id) => {
    const cap = SPECS[id].served;
    const units = Math.ceil(20_000 / cap);
    return { id, total: units * SPECS[id].cost };
  });
  const cheapest = totals.reduce((a, b) => (b.total < a.total ? b : a));
  assert.equal(sp.id, cheapest.id, `optimalProvider must pick the genuinely cheapest total plan (${JSON.stringify(totals)})`);
});

test('attacker case: the capacity+1 cliff is gone — shortfall 20,001 picks the 2x wat_clean class of answer, never a 17x reservoir jump', () => {
  const s = shortfallState(0, 1_000_000_000);
  const sp = optimalProvider(s, 'cleanwater', s.funds, 20_001);
  assert.ok(sp);
  // RED-PROOF: the REJECTed draft's "clears in one" gate excludes wat_clean
  // the instant shortfall exceeds its 20,000 capacity by even 1 — for ONE
  // extra person of shortfall it jumped straight to wat_reservoir (£81.0M),
  // a 17x cost cliff over 2x wat_clean (£9.36M).
  const watCleanDoubleTotal = 2 * SPECS.wat_clean.cost;
  const reservoirTotal = SPECS.wat_reservoir.cost; // 1 unit clears 20,001
  assert.ok(watCleanDoubleTotal < reservoirTotal, 'precondition: 2x wat_clean must genuinely beat 1x reservoir in total cost');
  assert.equal(sp.id, 'wat_clean', 'must pick the 2-unit wat_clean plan, not fall off the capacity cliff into wat_reservoir');
});

test('attacker case: strictly-dominated pick eliminated — shortfall 2,000 (1 unit either way) must pick the cheaper wat_tower, not wat_clean', () => {
  const s = shortfallState(0, 1_000_000_000);
  const sp = optimalProvider(s, 'cleanwater', s.funds, 2_000);
  assert.ok(sp);
  // RED-PROOF: the REJECTed draft's cost-per-capacity-among-clears-in-one
  // ranking preferred wat_clean (better £/capacity) even though BOTH specs
  // clear this shortfall in exactly 1 unit — wat_tower is strictly cheaper
  // for the SAME unit count (£2.70M vs £4.68M), a dominated pick.
  assert.equal(Math.ceil(2_000 / SPECS.wat_tower.served), 1, 'precondition: wat_tower also clears 2,000 in one unit');
  assert.equal(sp.id, 'wat_tower', 'equal unit count must go to the cheaper spec, never the pricier one');
});

// SUPERSEDED 2026-09-04 (BUG-685, Aaron's largest-first ruling): this test
// used to prove demandFixPlan() picked the cheaper multi-turbine plan over a
// single big unit — that is EXACTLY the "carpets the map with the smallest
// unit" defect BUG-685 was filed to close (Aaron's real save: 10,033 6MW
// turbines and ZERO nukes/CCGT/offshore for a 51.7GW city). demandFixPlan()'s
// BUILD plan now goes through largestFirstFill(), not optimalProvider()'s
// cheapest-total-plan scorer (still exercised, unchanged, by the
// optimalProvider()-based tests above this one) — the biggest unlocked spec
// wins a one-unit clear regardless of cost.
test('BUG-685: pop 8,000 power picks the BIGGEST unlocked spec that one-shot-clears the shortfall, not a multi-turbine monoculture', () => {
  const s = shortfallState(8_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
  assert.ok(plan, 'a power shortfall must exist at pop 8,000');
  assert.equal(plan.mix.length, 1, 'precondition: the biggest unlocked power spec alone clears this small shortfall in one unit');

  const biggestPowerSpec = Object.values(SPECS)
    .filter((sp) => sp.kind === 'power' && (sp.mw ?? 0) > 0)
    .reduce((best, sp) => ((sp.mw ?? 0) > (best.mw ?? 0) ? sp : best));
  assert.equal(plan.specId, biggestPowerSpec.id, 'BUG-685: the biggest unlocked power spec must win, even though it costs far more than a turbine');
  assert.notEqual(plan.specId, 'pow_wind', 'must never be the smallest (6 MW) turbine when bigger unlocked plants exist');
});

test('AC-12: optimalProvider returns null when nothing is unlocked for the service', () => {
  const base = initialState();
  // level 1: fire_post (unlock 2), fire_station (unlock 4), fire_hq (unlock 11)
  // are all locked; unlockedAll explicitly false so specUnlocked's real check runs.
  const s = { ...base, population: 10_000, unlockedAll: false, xp: 0, funds: 1_000_000_000 };
  assert.equal(levelOf(s.xp), 1, 'precondition: level 1, below every fire tier\'s unlock');
  const sp = optimalProvider(s, 'fire', s.funds, 10_500);
  assert.equal(sp, null, 'no unlocked fire spec exists at level 1 — must return null, not a locked spec');

  // AC-12 (caller-side): serviceCoverageOf's fire row falls back to the
  // pathological-all-locked literal ('fire_post') exactly as documented,
  // never crashes on a null spec id.
  const row = serviceCoverageOf(s).find((c) => c.id === 'fire');
  assert.equal(row.spec, 'fire_post', 'fire row must use the documented fallback literal when nothing is unlocked');
});

// ---------------------------------------------------------------------------
// AC-5/AC-6 — BUG-571 fire unlock-awareness.
// ---------------------------------------------------------------------------

test('AC-5: fire routes through optimalProvider and never recommends a locked tier', () => {
  const base = initialState();
  // Level exactly 2: fire_post (unlock 2) is unlocked; fire_station (unlock 4)
  // and fire_hq (unlock 11) are locked.
  const xpLevel2 = xpForLevel(2);
  const s = { ...base, population: 10_000, unlockedAll: false, xp: xpLevel2, funds: 1_000_000_000 };
  assert.equal(levelOf(s.xp), 2, 'precondition: level 2 unlocks fire_post only');

  const sp = optimalProvider(s, 'fire', s.funds, 10_500);
  assert.ok(sp);
  // RED-PROOF: the pre-fix code hardcoded row('fire', ..., 'fire_station') in
  // serviceCoverageOf() with NO unlock check at all — this would recommend
  // fire_station (locked) regardless of level.
  assert.equal(sp.id, 'fire_post', 'must never recommend fire_station/fire_hq while they are locked');

  const row = serviceCoverageOf(s).find((c) => c.id === 'fire');
  assert.equal(row.spec, 'fire_post', 'the serviceCoverageOf fire row must resolve through the same unlock-aware selector');

  const auto = pickAutoSpec(s);
  if (auto && auto.spec === row.spec) {
    assert.equal(SPECS[auto.spec].unlock, 2, 'pickAutoSpec must never surface a locked auto-build recommendation for fire');
  }
});

// Independent-round follow-up (re-score after the FEAT-2326609735 REJECT):
// confirm the accepted BUG-571 behaviour survives the total-plan-cost rewrite
// of optimalProvider() — these are the two canonical tiers the round verified.
test('BUG-571 regression: level 2 (fire_post only) picks fire_post, sized 2 units', () => {
  // SUPERSEDED 2026-09-03: AUTO_BUILD_DEMAND_FRACTION is now 1.5 (was 0.5) —
  // population re-derived (5,000, not the old 10,000) so the fixAmount
  // (5,000 * 1.5 = 7,500) still needs a clean ceil(7,500/4,000) = 2 units of
  // fire_post (served 4,000).
  const base = initialState();
  const s = { ...base, population: 5_000, unlockedAll: false, xp: xpForLevel(2), funds: 1_000_000_000, administrationState: null };
  assert.equal(levelOf(s.xp), 2, 'precondition: level 2 unlocks fire_post only');
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'fire');
  assert.ok(plan);
  assert.equal(plan.specId, 'fire_post');
  const fixAmount = 5_000 * AUTO_BUILD_DEMAND_FRACTION;
  assert.equal(plan.count, Math.ceil(fixAmount / SPECS.fire_post.served), `fire_post (served ${SPECS.fire_post.served}) needs ${Math.ceil(fixAmount / SPECS.fire_post.served)} units for a ${fixAmount} fixAmount (AUTO_BUILD_DEMAND_FRACTION of the 5,000 shortfall)`);
});

test('BUG-571 regression: level 4 (fire_post + fire_station unlocked) picks fire_station when its TOTAL plan cost is genuinely cheaper', () => {
  // SUPERSEDED 2026-09-03: AUTO_BUILD_DEMAND_FRACTION is now 1.5 (was 0.5) —
  // population re-derived (12,000, not the old 37,800) so fire_post's
  // 5-unit plan (£9.0M) is still genuinely more expensive than fire_station's
  // 1-unit plan (£8.64M) at the NEW fixAmount (12,000 * 1.5 = 18,000) — the
  // comparison the REJECTed draft never made (it only compared per-unit/
  // cost-per-capacity among clears-in-one candidates, or fell back to bare
  // per-unit cost — never a real total-cost comparison across differently
  // sized plans).
  const base = initialState();
  const s = { ...base, population: 12_000, unlockedAll: false, xp: xpForLevel(4), funds: 1_000_000_000, administrationState: null };
  assert.equal(levelOf(s.xp), 4, 'precondition: level 4 unlocks fire_post + fire_station (fire_hq still locked)');
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'fire');
  assert.ok(plan);
  assert.equal(plan.specId, 'fire_station');
  assert.equal(plan.count, 1);

  // Sanity: prove fire_post really would have been the worse TOTAL plan here.
  const fixAmount = 12_000 * AUTO_BUILD_DEMAND_FRACTION; // 18,000
  const postUnits = Math.ceil(fixAmount / SPECS.fire_post.served);
  const postTotal = postUnits * SPECS.fire_post.cost;
  const stationTotal = SPECS.fire_station.cost; // 1 unit
  assert.ok(stationTotal < postTotal, `fire_station's plan (£${stationTotal}) must beat fire_post's ${postUnits}-unit plan (£${postTotal})`);
});

test('AC-6: pickAutoSpec() itself never carries a count/plan field (sizing lives in demandFixPlan, not here)', () => {
  // pickAutoSpec() (the per-click Auto-build advisor's spec/label pick)
  // returns a pure {spec,label,serviceKey} triple, never a count/plan — the
  // BUG-571 fix only changed WHICH spec is chosen; BUG-601 (2026-09-02) then
  // changed how DemandDock.tsx's runAuto() USES that pick (routing through
  // demandFixPlan/resolveDemand for a properly-sized, funds-capped batch
  // instead of a single unit) — but pickAutoSpec's own return shape is
  // unaffected by either change. resolveDemand/demandFixPlan (a SEPARATE
  // code path, already count-based) is where sizing lives — see AC-9.
  const s = shortfallState(10_000);
  const auto = pickAutoSpec(s);
  assert.ok(auto, 'precondition: a real shortfall exists');
  assert.equal(typeof auto.spec, 'string');
  assert.equal(typeof auto.label, 'string');
  assert.equal(typeof auto.serviceKey, 'string', 'BUG-601: serviceKey lets the caller look up this service\'s demandFixPlan entry');
  assert.equal(Object.prototype.hasOwnProperty.call(auto, 'count'), false, 'pickAutoSpec must never carry a count/plan field');
});

// ---------------------------------------------------------------------------
// AC-9/AC-10 — fire gets a sized Fix button, respecting affordability.
// ---------------------------------------------------------------------------

test('AC-9: demandFixPlan emits a fire entry sized ceil((need-have)*AUTO_BUILD_DEMAND_FRACTION/unitCapacity) (BUG-601)', () => {
  // RED-PROOF: before this fix, DEMAND_FIX_PROVIDERS had no 'fire' rule, so
  // demandFixPlan() silently skipped fire (cheapestProvider(s,'fire') === null
  // for lack of any rule) — no entry, no Fix button, regardless of shortfall.
  const s = shortfallState(8_000);
  const plan = demandFixPlan(s);
  const fire = plan.find((p) => p.serviceKey === 'fire');
  assert.ok(fire, 'a fire shortfall with an unlocked provider must yield a demandFixPlan entry');

  const row = serviceCoverageOf(s).find((c) => c.id === 'fire');
  // NOTE: ServiceCoverage's field is `cap` (not `have`) — demandFixPlan()
  // maps c.cap -> DemandFixPlanItem.have for exactly this row (data.ts).
  const fixAmount = (row.need - row.cap) * AUTO_BUILD_DEMAND_FRACTION;
  const expectedCount = Math.ceil(fixAmount / fire.unitCapacity);
  assert.equal(fire.count, expectedCount, 'fire count must use the same ceil((need-have)*AUTO_BUILD_DEMAND_FRACTION/unitCapacity) formula every other service uses');
  assert.equal(fire.unitCapacity, SPECS[fire.specId].served);
});

test('AC-10: resolveDemand for fire respects affordability — places only what funds allow, never goes negative', () => {
  // SUPERSEDED 2026-09-03: AUTO_BUILD_DEMAND_FRACTION is now 1.5 (was 0.5) —
  // population re-derived (60,000, not the old 100,000) so the cheapest
  // TOTAL plan is still a multi-unit fire_post plan (not a single fire_hq
  // unit) AND the spec choice stays STABLE at half the real spend (halving
  // the budget must not flip the winning spec, or the "placed count of the
  // ORIGINAL spec" assertion below would be vacuously 0 — re-verified live
  // at this population during development).
  const s = shortfallState(60_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'fire');
  assert.ok(plan && plan.count >= 2, 'need a fire plan needing 2+ units to prove a partial affordability cap');

  // Determine the REAL total cost of completing the whole plan by running once
  // with ample funds — road-adjacency may add connector costs beyond a bare
  // sp.cost per unit as placements spread out, so don't assume a uniform
  // per-unit price (that assumption is what made an earlier draft of this test
  // flaky under a large batch).
  const full = reducer(s, { type: 'resolveDemand', serviceKey: 'fire' });
  const totalSpent = s.funds - full.funds;
  assert.ok(totalSpent > 0, 'precondition: the full-funds run must actually spend money');
  const limitedFunds = Math.floor(totalSpent / 2);
  assert.ok(limitedFunds > 0);
  const limited = { ...s, funds: limitedFunds };

  // BUG-685: recompute the plan against `limited` — largestFirstFill() is
  // unlock/allowance-aware but not itself budget-gated (BUG-601's own
  // convention), so the mix SHAPE is unaffected by the lower budget; only
  // how much of it gets placed is capped downstream.
  const limitedPlan = demandFixPlan(limited).find((p) => p.serviceKey === 'fire');
  assert.ok(limitedPlan, 'a fire shortfall (and a provider) must still exist at the reduced budget');

  const result = reducer(limited, { type: 'resolveDemand', serviceKey: 'fire' });
  const placedCount = countMixPlaced(limited.buildings, result.buildings, limitedPlan.mix);
  assert.ok(placedCount > 0 && placedCount < limitedPlan.count, `expected a strictly partial fire placement, got ${placedCount} of ${limitedPlan.count}`);
  assert.ok(result.funds >= 0, 'funds never went negative from the bulk fire placement');
  assert.ok(result.placeNotice && /insufficient funds/i.test(result.placeNotice));
  assert.ok(
    result.placeNotice.includes(String(placedCount)) && result.placeNotice.includes(String(limitedPlan.count)),
    'placeNotice reports both placed count and wanted count'
  );
});

// ---------------------------------------------------------------------------
// AC-11 — conservation: unrelated services' need/cap/spec are unaffected.
// ---------------------------------------------------------------------------

test('AC-11: non-fire, non-refuse service rows keep their literal recommended spec (provider-selection change is isolated)', () => {
  const s = shortfallState(8_000);
  const rows = serviceCoverageOf(s);
  // These rows still use the literal specs they always have — optimalProvider
  // is wired ONLY into the fire row (and demandFixPlan's generic loop, whose
  // cheapest-vs-value choice is exercised by AC-7/AC-8 above); nursery/primary/
  // college/gp/hosp/police/cleanwater/waste/power's serviceCoverageOf() row
  // literals are untouched by this feature.
  assert.equal(rows.find((r) => r.id === 'gp').spec, 'hea_clinic');
  assert.equal(rows.find((r) => r.id === 'hosp').spec, 'hea_hospital');
  assert.equal(rows.find((r) => r.id === 'police').spec, 'pol_station');
  assert.equal(rows.find((r) => r.id === 'cleanwater').spec, 'wat_clean');
  assert.equal(rows.find((r) => r.id === 'waste').spec, 'wat_waste');
});

// ---------------------------------------------------------------------------
// AC-1 — BUG-572: refuse row folded into serviceDemandOf().
// ---------------------------------------------------------------------------

test('AC-1: serviceDemandOf includes a refuse row when the collection depot is in shortfall', () => {
  const base = initialState();
  const s = {
    ...base,
    unlockedAll: true,
    funds: 1_000_000_000,
    // A real population is required: serviceDemandOf() damps every row
    // (including refuse) by earlyGameFactor(population) — at population 0 the
    // row would exist but read value 0, which is indistinguishable from "no
    // row" for this assertion's purpose. Population itself contributes no
    // waste (wasteGeneratedOf sums building-derived tonnage only), so this
    // does not affect the refuse shortfall precondition below.
    population: 5_000,
    buildings: [...base.buildings, { id: 90001, spec: 'ind_factory', x: 10, y: 10 }],
  };
  const waste = wasteStatsOf(s);
  assert.ok(waste.generated > waste.capacity, 'precondition: a real refuse shortfall (generated > capacity)');

  // RED-PROOF: pre-fix serviceDemandOf() only ever mapped serviceCoverageOf()'s
  // rows, which never included refuse — this find() would return undefined.
  const refuseRow = serviceDemandOf(s).find((r) => r.id === 'refuse');
  assert.ok(refuseRow, 'serviceDemandOf must include a refuse entry when a collection shortfall exists');
  assert.equal(refuseRow.label, 'Refuse');
  assert.ok(refuseRow.value > 0, 'a real shortfall must show a positive demand value');
  assert.ok(SPECS[refuseRow.spec], 'the refuse row must reference a real, buildable spec');
});

// ---------------------------------------------------------------------------
// AC-2/AC-3/AC-4 — DemandDock sort: dynamic, Health (gp+hosp) pinned top,
// deterministic. Tested via serviceDemandOf() + the SAME comparator
// DemandDock.tsx applies (kept identical here so a divergence in the
// component's actual comparator is caught by demand-fix-ui.test.tsx's live
// DOM mount, while this test exercises the comparator's LOGIC against a
// crafted fixture where the naive "just sort by value" reading would fail).
// ---------------------------------------------------------------------------

function sortServiceRows(rows) {
  return [...rows].sort((a, b) => {
    const aHealth = a.id === 'gp' || a.id === 'hosp';
    const bHealth = b.id === 'gp' || b.id === 'hosp';
    if (aHealth !== bHealth) return aHealth ? -1 : 1;
    if (b.value !== a.value) return b.value - a.value;
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });
}

test('AC-2: non-health rows sort by demand value, descending', () => {
  const s = shortfallState(8_000);
  const sorted = sortServiceRows(serviceDemandOf(s));
  const nonHealth = sorted.filter((r) => r.id !== 'gp' && r.id !== 'hosp');
  for (let i = 1; i < nonHealth.length; i++) {
    assert.ok(
      nonHealth[i - 1].value >= nonHealth[i].value,
      `non-health rows must be non-increasing by value: ${nonHealth[i - 1].id}(${nonHealth[i - 1].value}) then ${nonHealth[i].id}(${nonHealth[i].value})`
    );
  }
});

test('AC-3: gp and hosp are pinned above every non-health row, even when a non-health row has a higher value', () => {
  // Craft a fixture where a non-health service (nursery) has a HIGHER demand
  // value than gp/hosp by giving gp/hosp real capacity (low shortfall) while
  // leaving nursery with none (full shortfall) — a naive value-only sort would
  // put nursery above gp/hosp; the health-pinned predicate must not.
  const base = initialState();
  const s = {
    ...base,
    unlockedAll: true,
    funds: 1_000_000_000,
    population: 8_000,
    buildings: [
      ...base.buildings,
      { id: 90002, spec: 'hea_clinic', x: 10, y: 10 },
      { id: 90003, spec: 'hea_hospital', x: 12, y: 10 },
    ],
  };
  const rows = serviceDemandOf(s);
  const gp = rows.find((r) => r.id === 'gp');
  const hosp = rows.find((r) => r.id === 'hosp');
  const nursery = rows.find((r) => r.id === 'nursery');
  assert.ok(nursery.value > gp.value && nursery.value > hosp.value, 'precondition: nursery must out-rank gp/hosp on raw value');

  const sorted = sortServiceRows(rows);
  const nurseryIdx = sorted.findIndex((r) => r.id === 'nursery');
  const gpIdx = sorted.findIndex((r) => r.id === 'gp');
  const hospIdx = sorted.findIndex((r) => r.id === 'hosp');
  assert.ok(gpIdx < nurseryIdx, 'gp must render above nursery despite nursery\'s higher raw value');
  assert.ok(hospIdx < nurseryIdx, 'hosp must render above nursery despite nursery\'s higher raw value');
  // Between themselves, gp/hosp still sort by value (worse-covered leads) —
  // this is option (a1), Aaron-approved: BOTH rows pinned, sorted between
  // themselves, not folded into one meter.
  if (gp.value !== hosp.value) {
    const worseFirst = gp.value >= hosp.value ? gpIdx < hospIdx : hospIdx < gpIdx;
    assert.ok(worseFirst, 'between gp and hosp, the worse-covered (higher value) row must lead');
  }
});

test('AC-4: sort is deterministic — identical input sorts identically every call, with a stable id tiebreak', () => {
  const s = shortfallState(8_000);
  const rowsA = serviceDemandOf(s);
  const rowsB = serviceDemandOf(s);
  assert.deepEqual(rowsA, rowsB, 'serviceDemandOf must be a pure function of state (GR#21)');

  const sortedA = sortServiceRows(rowsA).map((r) => r.id);
  const sortedB = sortServiceRows(rowsB).map((r) => r.id);
  assert.deepEqual(sortedA, sortedB, 'sorting identical rows twice must yield byte-identical order');

  // Tie-break proof: two synthetic rows with equal value must always resolve
  // id-ascending, never flap between runs (no reliance on Array.sort's
  // unspecified-stability edge cases for equal comparator results here — the
  // comparator itself supplies the tiebreak).
  const tied = [
    { id: 'zzz', label: 'Z', value: 42, spec: 'wat_tower' },
    { id: 'aaa', label: 'A', value: 42, spec: 'wat_tower' },
  ];
  const sortedTied1 = sortServiceRows(tied).map((r) => r.id);
  const sortedTied2 = sortServiceRows(tied).map((r) => r.id);
  assert.deepEqual(sortedTied1, ['aaa', 'zzz']);
  assert.deepEqual(sortedTied1, sortedTied2, 'tie-break must be stable across repeated sorts');
});

// ---------------------------------------------------------------------------
// BUG-572 FOLLOW-UP (2026-09-02, Aaron rec-on-all) — parks/leisure was the
// last serviceCoverageOf()-modellable resource with a REAL coverage formula
// (crimeRateOf's Parks reducer, present since FEAT-2326609731) that was still
// never folded into a demand row — the same defect class the refuse row (AC-1
// above) closed. Also: a generic, DATA-DERIVED (GR#15) completeness check so
// a FUTURE resource with a real coverage function cannot silently regress
// this same gap without reddening a test.
// ---------------------------------------------------------------------------

test('BUG-572 follow-up: serviceDemandOf includes a parks row when parks footprint is short of need', () => {
  const s = shortfallState(300_000); // parksNeed = pop*0.002 = 600 footprint units; no parks built.
  // RED-PROOF: pre-fix serviceCoverageOf() never emitted a 'parks' row (it was
  // computed inline inside crimeRateOf and wellbeingOf only) — this find()
  // would return undefined, exactly like the pre-fix refuse row in AC-1.
  const parksRow = serviceDemandOf(s).find((r) => r.id === 'parks');
  assert.ok(parksRow, 'serviceDemandOf must include a parks entry when a footprint shortfall exists');
  assert.equal(parksRow.label, 'Parks & leisure');
  assert.ok(parksRow.value > 0, 'a real shortfall must show a positive demand value');
  assert.ok(SPECS[parksRow.spec], 'the parks row must reference a real, buildable spec');
  assert.equal(SPECS[parksRow.spec].kind, 'park', 'the recommended spec must actually be a park building');
});

test('BUG-572 follow-up: an empty (population 0) city reports zero parks demand — no manufactured baseline shortfall', () => {
  // Guards against reusing crimeRateOf's Math.max(1, pop*0.002) need floor for
  // the demand row: that floor exists only to keep crimeRateOf's OWN division
  // defined at pop 0, and would otherwise invent a permanent "needs 1 unit of
  // park" shortfall on a fresh genesis city that no other resource has.
  const s = initialState();
  assert.equal(s.population, 0, 'precondition: genesis city starts at zero population');
  const parksRow = serviceDemandOf(s).find((r) => r.id === 'parks');
  assert.ok(parksRow, 'the row itself must still exist (need=0 is a valid coverage=1 state, not a missing row)');
  assert.equal(parksRow.value, 0, 'zero population must never manufacture a parks shortfall');
  // Cross-check against demandFixPlan too — it must not suggest a park build
  // on a brand-new empty city either.
  assert.equal(demandFixPlan(s).find((p) => p.serviceKey === 'parks'), undefined);
});

test('BUG-572 follow-up: demandFixPlan sizes a parks fix by the same ceil((need-have)*AUTO_BUILD_DEMAND_FRACTION/unitCapacity) formula every service uses (BUG-601)', () => {
  const s = shortfallState(300_000);
  const plan = demandFixPlan(s);
  const parks = plan.find((p) => p.serviceKey === 'parks');
  assert.ok(parks, 'a parks shortfall with an unlocked provider must yield a demandFixPlan entry');
  const row = serviceCoverageOf(s).find((c) => c.id === 'parks');
  const fixAmount = (row.need - row.cap) * AUTO_BUILD_DEMAND_FRACTION;
  const expectedCount = Math.ceil(fixAmount / parks.unitCapacity);
  assert.equal(parks.count, expectedCount);
  assert.equal(parks.unitCapacity, SPECS[parks.specId].w * SPECS[parks.specId].h);
});

test('BUG-572 completeness (GR#15, data-derived not hand-listed): every serviceCoverageOf() id, plus refuse, appears in serviceDemandOf()', () => {
  // Deliberately derives the expected-id set from serviceCoverageOf() itself
  // (the coverage functions), not a hand-maintained literal array — a NEW
  // coverage row added in the future is automatically required to also reach
  // serviceDemandOf(), or this test reddens without needing an edit.
  const s = shortfallState(50_000);
  const coverageIds = serviceCoverageOf(s).map((r) => r.id);
  const demandIds = new Set(serviceDemandOf(s).map((r) => r.id));
  for (const id of coverageIds) {
    assert.ok(demandIds.has(id), `serviceCoverageOf() row '${id}' must have a matching serviceDemandOf() row`);
  }
  // wasteStatsOf's refuse metric is a twin coverage function outside
  // serviceCoverageOf() (documented above) — still must be present.
  assert.ok(demandIds.has('refuse'), 'the refuse row (wasteStatsOf twin coverage) must be present');
  // Full expected roster, spelled out once as a cross-check against the
  // derived assertions above (documents the current complete set for a
  // human reader; the loop above is what actually guards against drift).
  const expected = [
    'nursery', 'primary', 'college', 'gp', 'hosp', 'police', 'fire',
    'cleanwater', 'waste', 'power', 'parks', 'refuse',
  ];
  assert.deepEqual([...demandIds].sort(), [...expected].sort());
});

// ---------------------------------------------------------------------------
// BUG-601 (Aaron ruling, 2026-09-02) — a Fix/Auto-build action sizes to
// AUTO_BUILD_DEMAND_FRACTION (50%) of the OUTSTANDING shortfall, funds-capped
// otherwise, never the whole deficit in one press. Cross-service proof (gp,
// distinct from demand-fix.test.mjs's cleanwater-focused coverage) that the
// rule applies uniformly, plus the funds-capped case and pickAutoSpec's
// runAuto() routing through the SAME demandFixPlan-sized batch.
// ---------------------------------------------------------------------------

test('BUG-601: ample funds — demandFixPlan sizes a gp fix to exactly ceil((need-have)*AUTO_BUILD_DEMAND_FRACTION/unitCapacity) (SUPERSEDED 2026-09-03: fraction now 1.5, deliberate overshoot)', () => {
  const s = shortfallState(50_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'gp');
  assert.ok(plan, 'a gp shortfall with an unlocked provider must yield a demandFixPlan entry');

  const row = serviceCoverageOf(s).find((c) => c.id === 'gp');
  const fixAmount = (row.need - row.cap) * AUTO_BUILD_DEMAND_FRACTION;
  const expectedCount = Math.ceil(fixAmount / plan.unitCapacity);
  assert.equal(plan.count, expectedCount);
  // Sanity the fraction is genuinely a REAL fraction of the gap, not a no-op
  // — direction depends on AUTO_BUILD_DEMAND_FRACTION's own value (>1
  // overshoots past the gap on purpose since 2026-09-03; <1 stays under it,
  // the original 2026-09-02 ruling) rather than a hardcoded assumption of
  // which ruling is currently live.
  const sizedAmount = plan.count * plan.unitCapacity;
  const gap = row.need - row.cap;
  if (AUTO_BUILD_DEMAND_FRACTION > 1) {
    assert.ok(sizedAmount > gap, 'AUTO_BUILD_DEMAND_FRACTION > 1 must size the batch ABOVE the outstanding gap (deliberate headroom)');
  } else {
    assert.ok(sizedAmount < gap, 'AUTO_BUILD_DEMAND_FRACTION < 1 must size the batch BELOW the outstanding gap (deliberate residual)');
  }

  const result = reducer(s, { type: 'resolveDemand', serviceKey: 'gp' });
  const placedCount = result.buildings.filter((b) => b.spec === plan.specId).length -
    s.buildings.filter((b) => b.spec === plan.specId).length;
  assert.equal(placedCount, plan.count, 'ample funds must place exactly the planned count, no more no less');

  // Whether a residual shortfall remains depends on the SAME fraction sign:
  // fraction > 1 (current ruling) deliberately clears the whole gap PLUS
  // headroom in one ample-funds dispatch — the ORIGINAL <1 ruling instead
  // guaranteed a residual for a follow-up action. Assert whichever is true,
  // once the just-placed buildings are forced online (forceOnline() doc) so
  // this reads capacity math, not construction-timer noise.
  const remaining = demandFixPlan(forceOnline(result)).find((p) => p.serviceKey === 'gp');
  if (AUTO_BUILD_DEMAND_FRACTION > 1) {
    assert.equal(remaining, undefined, 'AUTO_BUILD_DEMAND_FRACTION > 1 must fully clear gp (plus headroom) in one ample-funds dispatch');
  } else {
    assert.ok(remaining, 'AUTO_BUILD_DEMAND_FRACTION < 1 must leave a real gp shortfall after a single ample-funds resolveDemand dispatch');
  }
});

test('BUG-601: funds-capped — a poor city places only what it can afford, still sized off the 50% target, never overspends', () => {
  // 100,000 forces a multi-unit gp plan (hea_clinic served 25,000 or similar
  // — recomputed generically below, no magic number) so a funds cap has room
  // to bite partway through the batch.
  const s = shortfallState(100_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'gp');
  assert.ok(plan && plan.count >= 2, 'need a gp plan needing 2+ units to prove a partial affordability cap');

  const full = reducer(s, { type: 'resolveDemand', serviceKey: 'gp' });
  const totalSpent = s.funds - full.funds;
  assert.ok(totalSpent > 0, 'precondition: the full-funds run must actually spend money');
  const limitedFunds = Math.floor(totalSpent / 2);
  assert.ok(limitedFunds > 0);
  const limited = { ...s, funds: limitedFunds };

  const limitedPlan = demandFixPlan(limited).find((p) => p.serviceKey === 'gp');
  assert.ok(limitedPlan, 'a gp shortfall (and a provider) must still exist at the reduced budget');
  // The plan itself is STILL sized off the 50%-of-shortfall target (funds
  // capping happens in the resolveDemand PLACEMENT loop, never by shrinking
  // the target itself) — this is what distinguishes "funds-capped otherwise"
  // from silently redefining what "50%" means when a player is poor.
  assert.equal(limitedPlan.count, plan.count, 'the funds-limited plan target is unchanged — only how much of it gets PLACED is capped');

  const result = reducer(limited, { type: 'resolveDemand', serviceKey: 'gp' });
  const placedCount = result.buildings.filter((b) => b.spec === limitedPlan.specId).length -
    limited.buildings.filter((b) => b.spec === limitedPlan.specId).length;
  assert.ok(placedCount > 0 && placedCount < limitedPlan.count, `expected a strictly partial gp placement, got ${placedCount} of ${limitedPlan.count}`);
  assert.ok(result.funds >= 0, 'funds never went negative from the bulk gp placement');
});

test('BUG-601: pickAutoSpec exposes serviceKey so the Auto-build click can route through the SAME 50%-sized demandFixPlan batch', () => {
  const s = shortfallState(50_000);
  const auto = pickAutoSpec(s);
  assert.ok(auto, 'precondition: a real shortfall exists');
  const plan = demandFixPlan(s).find((p) => p.serviceKey === auto.serviceKey);
  assert.ok(plan, 'the auto-picked service must have a matching demandFixPlan entry to size the Auto-build click');
  assert.ok(plan.count > 0);
});
