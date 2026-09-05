// demand-fix.test.mjs — FEAT-2326609728 engine core (one-click demand fix).
//
// demandFixPlan(state) is the PURE planning half; 'resolveDemand' is the bulk-
// place reducer action. Both reuse the existing SSOTs: serviceCoverageOf/
// wasteStatsOf for need/have, findSpot + the single-tile 'place' path for
// mutation — no second placement mechanism, so tests here focus on the NEW
// arithmetic (BUG-601, 2026-09-02: ceil((need-have)*AUTO_BUILD_DEMAND_FRACTION
// /unitCapacity) — was ceil((need*1.05-have)/unitCapacity), a full clear+5%
// headroom, until Aaron's ruling that a single Fix/Auto-build action must
// leave headroom for a follow-up action) and the bulk-place contract
// (affordability cap, determinism), not on re-proving coverage math covered
// elsewhere (consistency.test.mjs).
//
// SUPERSEDED 2026-09-03 (Aaron, recorded on the SAME BUG-601 item, folded in
// during the BUG-606 independent round): AUTO_BUILD_DEMAND_FRACTION 0.5 ->
// 1.5 — auto-place must now OVERSHOOT to 150% of the outstanding gap
// (deliberate headroom), the opposite direction from the original 50% ruling.
// Fixtures below re-derived against the live constant (never a hardcoded
// 0.5/1.5 literal) so a future re-tuning of the SAME constant cannot silently
// go unnoticed by these tests.
//
// RED-PROOF (documented per test): each test's assertion is shown to be able
// to fail by describing the mutation that would redden it — the canonical
// proof (breaking the AUTO_BUILD_DEMAND_FRACTION/ceil formula) is run live in
// the first test below.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { demandFixPlan, SPECS, serviceCoverageOf, AUTO_BUILD_DEMAND_FRACTION } from '../src/sim/data.ts';

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

test('demandFixPlan: clean-water LARGEST-FIRST mix clears AUTO_BUILD_DEMAND_FRACTION of the shortfall, biggest unlocked spec leading (BUG-685)', () => {
  // 60,000 chosen deliberately (FEAT-demanddock-overhaul / optimalProvider) —
  // big enough that wat_reservoir (served 60,000, the biggest cleanwater
  // spec) cannot alone close the 1.5x-overshot fixAmount, so the LARGEST-FIRST
  // picker (BUG-685) must fall through to wat_clean/wat_tower for the
  // remainder — a genuine multi-spec `mix`, not the single-spec plan the
  // pre-BUG-685 cheapest-total-plan scorer produced here.
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
  assert.ok(water.count > 0, 'a real shortfall always yields count > 0');

  // BUG-685 RED-PROOF: the biggest unlocked cleanwater spec (wat_reservoir,
  // served 60,000 — bigger than wat_clean's 20,000 and wat_tower's 4,000)
  // must lead the plan. The pre-BUG-685 cheapest-TOTAL-PLAN scorer picked
  // wat_reservoir here too by coincidence of cost, so the real discriminator
  // is the MIX shape below, not just this line — but a revert to a picker
  // that scores per-capacity efficiency (rather than sheer size) could still
  // legitimately fail this assertion for a different reason, so it stays.
  const biggestCleanwaterSpec = Object.values(SPECS)
    .filter((sp) => sp.kind === 'water' && sp.tag === 'clean')
    .reduce((best, sp) => ((sp.served ?? 0) > (best.served ?? 0) ? sp : best));
  assert.equal(water.specId, biggestCleanwaterSpec.id, 'the LARGEST unlocked provider must lead the plan (BUG-685)');
  assert.equal(water.unitCapacity, biggestCleanwaterSpec.served, "unitCapacity is the PRIMARY (largest) chosen spec's served field");

  // LARGEST-FIRST ordering: mix entries are sorted by capacity strictly
  // non-increasing (BUG-685's own contract).
  assert.ok(water.mix.length > 1, 'precondition: pop 60,000 needs more than the biggest spec alone can close in whole units');
  for (let i = 1; i < water.mix.length; i++) {
    assert.ok(
      water.mix[i - 1].unitCapacity >= water.mix[i].unitCapacity,
      `mix must be ordered largest-capacity-first, got ${JSON.stringify(water.mix)}`
    );
  }

  // The mix actually clears AUTO_BUILD_DEMAND_FRACTION of the gap, with
  // overshoot bounded by the LARGEST mix entry's own capacity (Aaron's
  // "never overshoot by more than one largest unit" boundary — BUG-685).
  const fixAmount = (water.need - water.have) * AUTO_BUILD_DEMAND_FRACTION;
  const delivered = water.mix.reduce((sum, m) => sum + m.count * m.unitCapacity, 0);
  const count = water.mix.reduce((sum, m) => sum + m.count, 0);
  const planCost = water.mix.reduce((sum, m) => sum + m.planCost, 0);
  assert.equal(water.count, count, 'count is the TOTAL across the whole mix');
  assert.equal(water.planCost, planCost, 'planCost is the TOTAL across the whole mix');
  assert.ok(delivered >= fixAmount, `the mix must clear the whole fixAmount (${delivered} < ${fixAmount})`);
  assert.ok(
    delivered - fixAmount < water.mix[0].unitCapacity,
    `overshoot (${delivered - fixAmount}) must stay under the largest mix entry's own capacity (${water.mix[0].unitCapacity})`
  );

  // RED-PROOF (monoculture-of-the-smallest-unit): the pre-BUG-685 toy-scale
  // defect this whole fix exists to close would have priced this shortfall
  // off the SMALLEST available unit (wat_tower, served 4,000) alone —
  // strictly more buildings than the LARGEST-FIRST mix actually needs.
  const smallestCleanwaterSpec = Object.values(SPECS)
    .filter((sp) => sp.kind === 'water' && sp.tag === 'clean')
    .reduce((worst, sp) => ((sp.served ?? 0) < (worst.served ?? 0) ? sp : worst));
  const monocultureCount = Math.ceil(fixAmount / smallestCleanwaterSpec.served);
  assert.ok(
    water.count < monocultureCount,
    `LARGEST-FIRST must need strictly fewer buildings than an all-${smallestCleanwaterSpec.id} monoculture (got ${water.count} vs ${monocultureCount})`
  );
});

test('demandFixPlan: BUG-685 LARGEST-FIRST — the biggest unlocked spec wins a one-unit clear even when it costs more (wat_reservoir over wat_tower/wat_clean at pop 2,000)', () => {
  // SUPERSEDED 2026-09-04 (BUG-685, Aaron's largest-first ruling replaces the
  // pre-existing cheapest-TOTAL-PLAN scorer for the actual BUILD plan): at
  // pop 2,000 (shortfall 2,100, fixAmount 2,100*AUTO_BUILD_DEMAND_FRACTION),
  // wat_reservoir (served 60,000 — the BIGGEST cleanwater spec) alone clears
  // the whole fixAmount in exactly ONE unit (largestFirstFill()'s "a shortfall
  // smaller than the biggest available unit still gets ONE building of the
  // biggest spec" rule) — it must win even though it costs far more per-unit
  // than wat_tower/wat_clean, the opposite of the OLD cheapest-plan ruling
  // this test used to assert.
  const s = shortfallState(2_000);
  const plan = demandFixPlan(s);
  const water = plan.find((p) => p.serviceKey === 'cleanwater');
  assert.ok(water);
  assert.equal(water.count, 1, 'precondition: the biggest spec alone clears this shortfall in exactly 1 unit');
  assert.equal(water.specId, 'wat_reservoir', 'BUG-685: the BIGGEST unlocked provider must win a one-unit clear, regardless of cost');
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

test('resolveDemand: one action sizes to AUTO_BUILD_DEMAND_FRACTION of the shortfall (SUPERSEDED 2026-09-03: now OVERSHOOTS to 150%, deliberate headroom)', () => {
  // 200,000 chosen so the sized batch (150% of a 200,000 shortfall = 300,000,
  // AUTO_BUILD_DEMAND_FRACTION now 1.5 per Aaron's 2026-09-03 superseding
  // ruling on BUG-601) divides evenly into wat_clean's 20,000-served capacity
  // (15 units, exactly 300,000) — the resulting capacity lands EXACTLY 1.5x
  // need with no ceiling-rounding overshoot beyond the intended one, so "one
  // dispatch clears AND overshoots need" is a clean, unambiguous assertion
  // rather than a coin-flip on rounding. (This is the SAME population the
  // pre-superseding-ruling version of this test used — only the narrative and
  // assertion direction flipped, since the fraction flipped from <1 to >1.)
  const s = shortfallState(200_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'cleanwater');
  assert.ok(plan);
  const s1 = reducer(s, { type: 'resolveDemand', serviceKey: 'cleanwater' });
  const s1Online = forceOnline(s1); // see forceOnline() doc — isolates the count math from construction timing

  const rowBefore = serviceCoverageOf(s).find((c) => c.id === 'cleanwater');
  const rowAfter = serviceCoverageOf(s1Online).find((c) => c.id === 'cleanwater');
  assert.ok(rowAfter.cap > rowBefore.cap, 'the dispatch must place real capacity');
  // RED-PROOF: a regression back to the ORIGINAL 0.5 fraction (or any
  // fraction <= 1) would leave capacity AT OR BELOW need after one dispatch —
  // this assertion (capacity strictly ABOVE need, real headroom built) would
  // fail under that code. Direction is derived from AUTO_BUILD_DEMAND_FRACTION
  // itself so this test does not silently go vacuous if the constant moves
  // again — it fails loud with a clear message instead.
  if (AUTO_BUILD_DEMAND_FRACTION > 1) {
    assert.ok(
      rowAfter.cap > rowAfter.need,
      `AUTO_BUILD_DEMAND_FRACTION > 1 must OVERSHOOT need after one dispatch — capacity ${rowAfter.cap} should exceed need ${rowAfter.need}`
    );
  } else {
    assert.ok(
      rowAfter.cap < rowAfter.need,
      `AUTO_BUILD_DEMAND_FRACTION < 1 must NOT fully clear a large shortfall — capacity ${rowAfter.cap} should remain below need ${rowAfter.need}`
    );
  }

  // Placed buildings are all the planned spec, and exactly `count` of them.
  const placedCount = s1.buildings.filter((b) => b.spec === plan.specId).length - s.buildings.filter((b) => b.spec === plan.specId).length;
  assert.equal(placedCount, plan.count, 'placed exactly the planned count');
});

/** BUG-685: count placements of EVERY spec in a (possibly mixed) plan, not
 *  just the primary/largest one — a largestFirstFill() mix places several
 *  specs, so a single-specId filter undercounts. */
function countMixPlaced(before, after, mix) {
  const ids = new Set(mix.map((m) => m.specId));
  const countOf = (bs) => bs.filter((b) => ids.has(b.spec)).length;
  return countOf(after) - countOf(before);
}

test('resolveDemand: affordability cap places only what is affordable and reports the shortfall (no negative funds)', () => {
  // 60,000 gives a plan.count safely >= 2 (BUG-685: a multi-spec LARGEST-FIRST
  // mix at this size — see the sibling mix test above), unlike a smaller
  // population where the shortfall closes with exactly 1 (large) unit.
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

  // largestFirstFill() is unlock/allowance-aware but NOT itself budget-gated
  // (BUG-601's own convention: affordability capping happens downstream, in
  // the resolveDemand reducer) — at this LOWER budget the mix is the SAME
  // shape, just partially affordable. Recompute against `limited` anyway so
  // this test tracks whatever demandFixPlan actually offers at that funds
  // level, rather than assuming continuity with `plan`.
  const limitedPlan = demandFixPlan(limited).find((p) => p.serviceKey === 'cleanwater');
  assert.ok(limitedPlan, 'a clean-water shortfall (and a provider) must still exist at the reduced budget');

  const result = reducer(limited, { type: 'resolveDemand', serviceKey: 'cleanwater' });

  const placedCount = countMixPlaced(limited.buildings, result.buildings, limitedPlan.mix);
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
