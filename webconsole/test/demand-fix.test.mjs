// demand-fix.test.mjs — FEAT-2326609728 engine core (one-click demand fix).
//
// demandFixPlan(state) is the PURE planning half; 'resolveDemand' is the bulk-
// place reducer action. Both reuse the existing SSOTs: serviceCoverageOf/
// wasteStatsOf for need/have, findSpot + the single-tile 'place' path for
// mutation — no second placement mechanism, so tests here focus on the NEW
// arithmetic (the ceil(need*1.05-have)/unitCapacity count) and the bulk-place
// contract (affordability cap, determinism), not on re-proving coverage math
// covered elsewhere (consistency.test.mjs).
//
// RED-PROOF (documented per test): each test's assertion is shown to be able
// to fail by describing the mutation that would redden it — the canonical
// proof (breaking the +5%/ceil formula) is run live in the first test below.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { demandFixPlan, SPECS, serviceCoverageOf } from '../src/sim/data.ts';

/** A state with `population` citizens, no service buildings, all specs unlocked,
 *  and a large treasury — guarantees every population-scaled service is in
 *  shortfall (starterCity() has only roads/rail/pylons, per engine.ts). */
function shortfallState(population, fundsOverride = 1_000_000_000) {
  const base = initialState();
  return { ...base, population, unlockedAll: true, funds: fundsOverride, administrationState: null };
}

/**
 * Construction-time / road-activation (isOnline, data.ts) is orthogonal
 * pre-existing behaviour, not part of this feature's arithmetic — a building
 * placed this tick is legitimately still "under construction" for a few
 * ticks. To test the demand-fix COUNT math (not the construction timer) we
 * force every building online by clearing builtTick, which is isOnline()'s
 * documented always-online escape hatch (`b.builtTick == null` => true).
 */
function forceOnline(state) {
  return { ...state, buildings: state.buildings.map((b) => ({ ...b, builtTick: null })) };
}

test('demandFixPlan: clean-water count = ceil((need*1.05 - have) / unitCapacity)', () => {
  // 60,000 chosen deliberately (FEAT-demanddock-overhaul / optimalProvider,
  // re-scored to a TOTAL-PLAN-COST comparison after the independent round's
  // REJECT of the original clears-in-one-only draft): at shortfall 63,000,
  // wat_clean's 4-unit plan (£18.72M) is the cheapest total among wat_tower's
  // 16-unit plan (£43.2M) and wat_reservoir's 2-unit plan (£162M), so
  // wat_clean wins. need/unitCapacity (wat_clean, served 20,000) is EXACTLY 3
  // without headroom (60,000 is an exact multiple of 20,000), so the +5% is
  // the ONLY thing that pushes the ceiling to 4 — a population where headroom
  // is a no-op would let a dropped-1.05 bug hide.
  const s = shortfallState(60_000);
  const plan = demandFixPlan(s);
  const water = plan.find((p) => p.serviceKey === 'cleanwater');
  assert.ok(water, 'clean-water shortfall present at pop 60,000 with zero water buildings');

  // Cross-check against the SAME coverage row demandFixPlan is required to use
  // (GR#3 SSOT — not re-derived), so this test would redden if demandFixPlan
  // silently forked its own need/have numbers.
  const row = serviceCoverageOf(s).find((c) => c.id === 'cleanwater');
  assert.equal(water.need, row.need);
  assert.equal(water.have, row.cap);

  const sp = SPECS[water.specId];
  assert.equal(water.unitCapacity, sp.served, 'unitCapacity is the chosen spec\'s served field');

  const expectedCount = Math.ceil(Math.max(0, water.need * 1.05 - water.have) / water.unitCapacity);
  assert.equal(water.count, expectedCount);
  assert.ok(water.count > 0, 'a real shortfall always yields count > 0');

  // RED-PROOF: the count math can fail. Recompute WITHOUT the 5% headroom (the
  // bug this feature fixes — "just enough" leaves zero margin) and show it is
  // a strictly smaller number whenever need isn't already a multiple of
  // unitCapacity, i.e. the +1.05 term is load-bearing, not decorative.
  const countWithoutHeadroom = Math.ceil(Math.max(0, water.need - water.have) / water.unitCapacity);
  assert.equal(countWithoutHeadroom, 3, 'sanity: without headroom, 60,000/20,000 needs exactly 3 Water Works');
  assert.ok(
    water.count > countWithoutHeadroom,
    `the 5% headroom must push the count above the bare no-headroom figure (got ${water.count} vs ${countWithoutHeadroom})`
  );
});

test('demandFixPlan: a dominated multi-unit plan never wins — same unit count must go to the cheaper spec (wat_tower over wat_clean at pop 2,000)', () => {
  // FEAT-demanddock-overhaul re-score: optimalProvider() picks the cheapest
  // TOTAL PLAN cost, not per-capacity efficiency. At pop 2,000 (shortfall
  // 2,100) BOTH wat_tower (served 4,000) and wat_clean (served 20,000) clear
  // the shortfall in exactly 1 unit — with the unit count tied, the cheaper
  // spec must win (wat_tower £2.7M < wat_clean £4.68M), never the pricier one
  // just because it has better cost-per-capacity in the abstract.
  const s = shortfallState(2_000);
  const plan = demandFixPlan(s);
  const water = plan.find((p) => p.serviceKey === 'cleanwater');
  assert.ok(water);
  assert.equal(water.count, 1, 'precondition: both candidate specs clear this shortfall in exactly 1 unit');
  assert.equal(water.specId, 'wat_tower', 'a strictly dominated (same units, higher cost) pick must never win');
});

test('demandFixPlan: no shortfall (population 0) returns an empty plan', () => {
  const s = shortfallState(0);
  const plan = demandFixPlan(s);
  assert.equal(plan.length, 0, 'a genesis city with zero population has zero demand, so nothing to fix');
});

test('demandFixPlan is pure — calling it twice on the same state never mutates it', () => {
  const s = shortfallState(5_000);
  const before = JSON.stringify(s);
  demandFixPlan(s);
  demandFixPlan(s);
  assert.equal(JSON.stringify(s), before, 'demandFixPlan must not mutate its input');
});

test('resolveDemand: dispatching clears the service (capacity >= need*1.05)', () => {
  const s = shortfallState(10_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'cleanwater');
  assert.ok(plan);
  const s1 = reducer(s, { type: 'resolveDemand', serviceKey: 'cleanwater' });
  const s1Online = forceOnline(s1); // see forceOnline() doc — isolates the count math from construction timing

  const row = serviceCoverageOf(s1Online).find((c) => c.id === 'cleanwater');
  assert.ok(row.cap >= row.need * 1.05 - 1e-9, `capacity ${row.cap} should clear need*1.05 = ${row.need * 1.05}`);

  // The plan for this service should now be empty (shortfall resolved).
  const remaining = demandFixPlan(s1Online).find((p) => p.serviceKey === 'cleanwater');
  assert.equal(remaining, undefined, 'no residual clean-water shortfall after resolveDemand');

  // Placed buildings are all the planned spec, and exactly `count` of them.
  const placedCount = s1.buildings.filter((b) => b.spec === plan.specId).length - s.buildings.filter((b) => b.spec === plan.specId).length;
  assert.equal(placedCount, plan.count, 'placed exactly the planned count');
});

test('resolveDemand: affordability cap places only what is affordable and reports the shortfall (no negative funds)', () => {
  // 60,000 gives a plan.count safely >= 2 (wat_clean x4, the cheapest total
  // plan at this shortfall — see the headroom test above), unlike a smaller
  // population where the "1 dam not 20 towers" scoring would plan exactly 1
  // (large) unit.
  const s = shortfallState(60_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'cleanwater');
  assert.ok(plan && plan.count >= 2, 'need a plan needing 2+ units to prove a partial cap');

  // Determine the REAL total cost of completing the whole plan by running once
  // with ample funds — as placements spread out across the map, road-adjacency
  // may add connector costs beyond a bare sp.cost per unit, so don't assume a
  // uniform per-unit price (a naive `sp.cost * N` funds budget can straddle a
  // connector-cost boundary and place one fewer/more unit than expected).
  const full = reducer(s, { type: 'resolveDemand', serviceKey: 'cleanwater' });
  const totalSpent = s.funds - full.funds;
  assert.ok(totalSpent > 0, 'precondition: the full-funds run must actually spend money');
  const limitedFunds = Math.floor(totalSpent / 2); // never zero, never all
  assert.ok(limitedFunds > 0);
  const limited = { ...s, funds: limitedFunds };

  // optimalProvider() is budget-sensitive (a whole plan must fit the budget
  // to win outright, else it falls back to the cheapest single-unit-affordable
  // spec) — at this LOWER budget the plan may legitimately pick a DIFFERENT,
  // cheaper spec than the ample-funds run did. Recompute the plan against the
  // actual `limited` state rather than assuming continuity with `plan`.
  const limitedPlan = demandFixPlan(limited).find((p) => p.serviceKey === 'cleanwater');
  assert.ok(limitedPlan, 'a clean-water shortfall (and a provider) must still exist at the reduced budget');

  const result = reducer(limited, { type: 'resolveDemand', serviceKey: 'cleanwater' });

  const placedCount = result.buildings.filter((b) => b.spec === limitedPlan.specId).length -
    limited.buildings.filter((b) => b.spec === limitedPlan.specId).length;
  assert.ok(placedCount > 0 && placedCount < limitedPlan.count, `expected a strictly partial placement, got ${placedCount} of ${limitedPlan.count}`);
  assert.ok(result.funds >= 0, 'funds never went negative from the bulk placement');
  assert.ok(
    result.placeNotice && /insufficient funds/i.test(result.placeNotice),
    `placeNotice must report the shortfall, got: ${result.placeNotice}`
  );
  assert.ok(
    result.placeNotice.includes(String(placedCount)) && result.placeNotice.includes(String(limitedPlan.count)),
    'placeNotice reports both placed count and wanted count'
  );
});

test('resolveDemand: determinism — same state dispatched twice yields identical plans and placements', () => {
  const s = shortfallState(10_000);
  const planA = demandFixPlan(s);
  const planB = demandFixPlan(s);
  assert.deepEqual(planA, planB, 'demandFixPlan(state) is a pure function of state');

  const r1 = reducer(s, { type: 'resolveDemand', serviceKey: 'cleanwater' });
  const r2 = reducer(s, { type: 'resolveDemand', serviceKey: 'cleanwater' });
  // Compare the resulting building layouts and funds, ignoring nothing —
  // a Date.now/Math.random leak would show up as a coordinate or id mismatch.
  assert.deepEqual(r1.buildings, r2.buildings, 'identical placements from identical input (GR#21)');
  assert.equal(r1.funds, r2.funds);
  assert.equal(r1.placeNotice, r2.placeNotice);
});

test('resolveDemand: unknown/absent serviceKey is a no-op', () => {
  const s = shortfallState(0); // no shortfalls at all
  const result = reducer(s, { type: 'resolveDemand', serviceKey: 'cleanwater' });
  assert.deepEqual(result, s, 'no plan for the service => state unchanged');

  const result2 = reducer(s, { type: 'resolveDemand', serviceKey: 'not-a-real-service' });
  assert.deepEqual(result2, s, 'nonexistent service key => state unchanged');
});

test('demandFixPlan: refuse (collection depot) shortfall uses wasteStatsOf, not serviceCoverageOf', () => {
  // Refuse generation scales with residential/commercial/industrial JOBS/capacity,
  // not raw population, so seed some job-bearing buildings via a direct state
  // build rather than population alone (population alone drives zero waste).
  const base = initialState();
  const s = {
    ...base,
    unlockedAll: true,
    funds: 1_000_000_000,
    buildings: [
      ...base.buildings,
      // builtTick omitted (undefined): isOnline() treats a building with no
      // builtTick as always-online (bypasses construction/road-gate timing —
      // see data.ts isOnline), so this factory unconditionally generates
      // waste regardless of road adjacency in this synthetic state.
      { id: 90001, spec: 'ind_factory', x: 10, y: 10 },
    ],
  };
  const plan = demandFixPlan(s);
  const refuse = plan.find((p) => p.serviceKey === 'refuse');
  assert.ok(refuse, 'an online factory with zero refuse depots must show a refuse shortfall');
  assert.equal(SPECS[refuse.specId].wasteCapacity, refuse.unitCapacity);
  assert.ok(refuse.count > 0);
});
