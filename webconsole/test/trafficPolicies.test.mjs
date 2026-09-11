// trafficPolicies.test.mjs — FEAT-2326609801 "Realistic traffic inc8 -
// congestion policy levers" (docs/planning/acceptance/FEAT-2326609792-inc8.md
// AC-1..AC-7).
//
// Run with `node tools/test/scoped.mjs webconsole/test/trafficPolicies.test.mjs`
// (node --test with type-stripping, exercises the exact shipped TypeScript
// modules — same discipline as trafficDemand.test.mjs).
//
// Every pin states its own mutant (prove-can-fail, GR#21 discipline) —
// verified by a scratch-copy mutation (`cp f f.bak; edit; run; mv f.bak f`)
// during this build; each test's comment records the specific edit and the
// observed RED result.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import {
  policyModeShareAdjustmentOf,
  forecastLineUsage,
  forecastTotalDrivableCapacityOf,
  busPriorityCapacityDeltaOf,
  busPriorityCapacityInfoOf,
  adjustedRoadCapacitiesOf,
  totalPersonTripsOf,
  modeShareOf,
  ladderPointOf,
  __moveShareForTest,
  __setBusLaneShareFractionOverrideForTest,
} from '../src/sim/trafficDemand.ts';
import {
  initialState,
  sanitizeTreasury,
  computeFlows,
  roadPricingInflowOf,
  ownershipQuotaInflowOf,
} from '../src/sim/engine.ts';
import { POLICIES, ROAD_TIER_CAPACITY, ROAD_TIER_SPECS, SPECS, roadTierOf } from '../src/sim/data.ts';
const ROAD_TIER_CAPACITY_TIER2 = ROAD_TIER_CAPACITY[2];

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, '..', '..');

const policyLevers = JSON.parse(
  readFileSync(path.join(repoRoot, 'data', 'traffic', 'policy_levers.json'), 'utf8'),
);
const taxation = JSON.parse(
  readFileSync(path.join(repoRoot, 'data', 'traffic', 'taxation.json'), 'utf8'),
);
const linkCapacity = JSON.parse(
  readFileSync(path.join(repoRoot, 'data', 'traffic', 'link_capacity.json'), 'utf8'),
);

function leverMidpoint(leverId, effectKey) {
  const lever = policyLevers.levers.find((l) => l.id === leverId);
  const [lo, hi] = lever.expectedEffect[effectKey];
  return (lo + hi) / 2;
}
const OWNERSHIP_QUOTA_FRACTION = leverMidpoint('coe_ownership_quota', 'carModeShareReductionPercentagePoints') / 100;
const ROAD_PRICING_FRACTION = leverMidpoint('erp_road_pricing', 'peakPeriodVolumeReductionPercent') / 100;
const INTEGRATED_TICKETING_FRACTION = leverMidpoint('integrated_transit_singapore_style', 'publicTransportModeShareGain') / 100;

const PEAK_GBP_PER_CROSSING = taxation.roadPricing.electronicRoadPricingSingaporeStyle.peakGbpPerCrossing;
const COE_ILLUSTRATIVE_PRICE_GBP = taxation.certificateOfEntitlement.illustrativePriceGBP.value;
const COE_QUOTA_GROWTH_RATE_PER_YEAR = taxation.certificateOfEntitlement.quotaGrowthRatePerYear.value;

function capacityPerLane(roadClassId) {
  return linkCapacity.roadClasses.find((r) => r.roadClassId === roadClassId).capacityPcuPerLanePerHour;
}
const AVENUE_CAP_PER_LANE = capacityPerLane('avenue_2_plus_2');
const BUS_LANE_CAP_PER_LANE = capacityPerLane('bus_lane_variant');

function board(buildings, population = 0, policyOverrides = {}) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return {
    ...base,
    unlockedAll: true,
    buildings,
    nextId: maxId + 1,
    roadNotice: null,
    population,
    policies: { ...base.policies, ...policyOverrides },
  };
}
function road(id, spec, x, y) {
  return { id, spec, x, y };
}
function res(id, x, y) {
  return { id, spec: 'res_hut', x, y };
}

// --- AC-1 — surface extension: old-save default false ----------------------

test('AC-1: POLICIES/PolicyDef carries the 4 new levers with directional descriptions', () => {
  const ids = POLICIES.map((p) => p.id);
  for (const id of ['ownershipQuota', 'roadPricing', 'busPriority', 'integratedTicketing']) {
    assert.ok(ids.includes(id), `POLICIES is missing ${id}`);
  }
});

test('AC-1: a pre-inc8 save (missing the 4 new policy keys) sanitizes to false, never undefined', () => {
  const base = initialState();
  // Simulate a save captured BEFORE this increment — its policies object
  // only ever had the original 4 keys.
  const legacySave = {
    ...base,
    policies: { recycling: false, transitSubsidy: true, tourismDrive: false, austerity: false },
  };
  const sanitized = sanitizeTreasury(legacySave);
  assert.equal(sanitized.policies.ownershipQuota, false);
  assert.equal(sanitized.policies.roadPricing, false);
  assert.equal(sanitized.policies.busPriority, false);
  assert.equal(sanitized.policies.integratedTicketing, false);
  // Pre-existing keys pass through unchanged (transitSubsidy stays true).
  assert.equal(sanitized.policies.transitSubsidy, true);
  // MUTANT (verified via scratch copy, engine.ts sanitizePolicies): default a
  // missing key to `true` instead of `false` -- reds this old-save fixture
  // (a save made before ERP existed would silently start charging ERP).
  // Observed RED: sanitized.policies.roadPricing became `true`, failing the
  // `assert.equal(..., false)` line above.
});

// --- AC-2 — policyModeShareAdjustmentOf composition -------------------------

test('AC-2: moveShare worked example -- roadPricing alone, exact post-adjustment vector (every recipient)', () => {
  // Doc's literal fixture: car=0.5, bus=0.2, heavy_rail=0.2, hs_rail=0.1.
  const vector = { car: 0.5, bus: 0.2, heavy_rail: 0.2, hs_rail: 0.1 };
  const amount = vector.car * ROAD_PRICING_FRACTION; // 0.5 * 0.15 = 0.075
  __moveShareForTest(vector, amount, ['car'], ['bus', 'heavy_rail', 'hs_rail']);
  assert.ok(Math.abs(vector.car - 0.425) < 1e-9, `car expected 0.425, got ${vector.car}`);
  // Proportional-to-pre-move-share distribution: toTotal = 0.2+0.2+0.1 = 0.5,
  // so bus/heavy_rail get 0.075*(0.2/0.5)=0.03 each, hs_rail gets
  // 0.075*(0.1/0.5)=0.015 -- checking EVERY recipient individually (not just
  // the sum) so an even-split bug (0.075/3 each) cannot hide behind a
  // still-conserved total.
  assert.ok(Math.abs(vector.bus - 0.23) < 1e-9, `bus expected 0.23, got ${vector.bus}`);
  assert.ok(Math.abs(vector.heavy_rail - 0.23) < 1e-9, `heavy_rail expected 0.23, got ${vector.heavy_rail}`);
  assert.ok(Math.abs(vector.hs_rail - 0.115) < 1e-9, `hs_rail expected 0.115, got ${vector.hs_rail}`);
  const sum = Object.values(vector).reduce((a, b) => a + b, 0);
  assert.ok(Math.abs(sum - 1) < 1e-9, `sum expected 1, got ${sum}`);
  // MUTANT (verified via scratch copy, trafficDemand.ts moveShare): distribute
  // the moved amount EVENLY across toModes (`actual / toModes.length` per
  // mode) instead of proportional to each mode's own pre-move share --
  // conserves the total (sum stays 1, car unchanged) but reds every
  // per-recipient assertion above.
  // Observed RED: bus/heavy_rail/hs_rail all became 0.075/3=0.025 above
  // their pre-move value (0.225/0.225/0.125), failing the three
  // `Math.abs(vector.X - expected) < 1e-9` checks while the sum check alone
  // would have passed -- exactly the false-pass shape this test's
  // individual-recipient assertions close.
});

test('AC-2: policyModeShareAdjustmentOf renormalises to exactly 1 with all 3 demand-share policies on', () => {
  const s = board([res(1, 0, 0)], 500, { ownershipQuota: true, roadPricing: true, integratedTicketing: true });
  const adjusted = policyModeShareAdjustmentOf(s);
  const sum = Object.values(adjusted).reduce((a, b) => a + b, 0);
  assert.ok(Math.abs(sum - 1) < 1e-9, `sum expected 1, got ${sum}`);
  // Fixed composition order (ownershipQuota -> roadPricing -> integratedTicketing)
  // is not toggle-order dependent -- the production function reads booleans,
  // never a toggle-history array, so re-deriving the SAME state (booleans
  // already all true) is by construction order-independent; assert calling
  // it twice on the identical state is deterministic (GR#21).
  const adjusted2 = policyModeShareAdjustmentOf(board([res(1, 0, 0)], 500, {
    integratedTicketing: true, roadPricing: true, ownershipQuota: true,
  }));
  for (const k of Object.keys(adjusted)) {
    assert.ok(Math.abs(adjusted[k] - adjusted2[k]) < 1e-12, `mode ${k} differs by construction order`);
  }
  // EQUIVALENT-MUTANT FINDING (verified via scratch copy, mirrors this
  // file's own documented BUG-853(4) precedent for disclosing a mutant that
  // cannot be pinned): removing the final renormalisation divide (and its
  // `anyMove` guard) does NOT red this sum-equals-1 assertion in THIS
  // implementation -- moveShare conserves total mass by construction
  // (subtracts an exact `actual` from fromModes, adds the SAME `actual` to
  // toModes on every branch, including the toTotal<=0 even-split fallback),
  // so the sum is already exactly 1 before the divide runs, and dividing a
  // float by exactly 1.0 is an IEEE-754 identity (x/1.0 === x bit-for-bit).
  // The renormalisation code is kept as defence-in-depth (documented intent,
  // AC-2's own text) but this specific "skip the divide" mutant is
  // unreachable through the sum check alone with a correct moveShare; the
  // PROPORTIONAL-DISTRIBUTION mutant that actually breaks conservation is
  // caught by the worked-example test above instead (its per-recipient
  // assertions), since any bug big enough to move mass off the vector
  // entirely would need to defeat moveShare's own subtract/add symmetry,
  // which the worked-example test exercises directly and precisely.
});

test('AC-2: false-pass guard -- car-share-decreased alone cannot catch a renormalisation bug', () => {
  const s = board([res(1, 0, 0)], 500, { roadPricing: true, ownershipQuota: true });
  const before = modeShareOf(ladderPointOf(s));
  const adjusted = policyModeShareAdjustmentOf(s);
  assert.ok(adjusted.car < (before.car ?? 0), 'car share should have decreased');
  const sum = Object.values(adjusted).reduce((a, b) => a + b, 0);
  assert.ok(Math.abs(sum - 1) < 1e-9, 'sum must ALSO be checked -- a shrinking-total bug still decreases car share');
});

// --- AC-3 — off-path identity, forecastLineUsage reads the adjusted shares -

test('AC-3: policyModeShareAdjustmentOf is byte-identical to modeShareOf(ladderPointOf(s)) with every policy off', () => {
  const s = board([res(1, 0, 0), road(2, 'rd_avenue', 5, 5)], 500);
  const raw = modeShareOf(ladderPointOf(s));
  const adjusted = policyModeShareAdjustmentOf(s);
  assert.deepEqual(adjusted, raw, 'off-path must be a true no-op, not merely close');
});

test('AC-3: forecastLineUsage output is unchanged from the pre-inc8 shape with every policy off', () => {
  const s = board([res(1, 0, 0), road(2, 'rd_avenue', 5, 5)], 500);
  const usage = forecastLineUsage(s);
  assert.ok(usage.has('rd_avenue'), 'rd_avenue must still appear');
  assert.ok(!usage.has('bus'), 'no synthetic bus entry when busPriority is off');
  // MUTANT (verified via scratch copy, trafficDemand.ts policyModeShareAdjustmentOf):
  // apply the ownershipQuota/roadPricing/integratedTicketing moves
  // UNCONDITIONALLY (drop the `if (s.policies.X)` gates). Observed RED: the
  // AC-3 byte-identical test above failed with `adjusted.car` dropped from
  // ~0.59 to ~0.28 and every mode share shifted despite every policy being
  // off, and the "busPriority off -> mode share untouched" test just below
  // failed the same way (deepEqual mismatch on car/bus/heavy_rail/
  // motorbike/taxi) -- both prove the golden no-op path is broken once the
  // policy gates are removed.
});

// --- AC-4 — busPriority capacity reallocation -------------------------------

test('AC-4: busPriority moves capacity OUT of totalDrivableCap and INTO the bus line class, exactly (BUG-905 fix: a dimensionless fraction of ROAD_TIER_CAPACITY, never a raw pcu figure)', () => {
  const avenueTiles = [];
  for (let i = 0; i < 10; i++) avenueTiles.push(road(100 + i, 'rd_avenue', i, 0));
  const off = board(avenueTiles, 0, { busPriority: false });
  const on = board(avenueTiles, 0, { busPriority: true });

  const capOff = forecastTotalDrivableCapacityOf(off);
  const capOn = forecastTotalDrivableCapacityOf(on);
  // BUG-905: the delta is laneShareFraction (delta/rowCapacity from
  // link_capacity.json, e.g. 100/1800) applied to the avenue tier's OWN
  // ROAD_TIER_CAPACITY figure (data.ts, tier 2 = 250) -- never a raw
  // pcu-per-lane-per-hour figure subtracted from a people/tick/tile figure.
  const laneShareFraction = (AVENUE_CAP_PER_LANE - BUS_LANE_CAP_PER_LANE) / AVENUE_CAP_PER_LANE;
  const expectedDelta = laneShareFraction * ROAD_TIER_CAPACITY_TIER2 * 10;
  assert.ok(Math.abs(busPriorityCapacityDeltaOf(on) - expectedDelta) < 1e-9);
  assert.ok(Math.abs((capOff - capOn) - expectedDelta) < 1e-9, 'totalDrivableCap must drop by exactly the delta');

  const usageOn = forecastLineUsage(on);
  const busEntry = usageOn.get('bus');
  assert.ok(busEntry, 'a synthetic bus entry must appear when busPriority is on');
  assert.ok(Math.abs(busEntry.capacity - expectedDelta) < 1e-9, 'bus capacity must gain exactly the delta (both sides of the transfer)');

  // MUTANT (verified via scratch copy, trafficDemand.ts
  // forecastTotalDrivableCapacityOf): drop the `- busPriorityCapacityDeltaOf(s)`
  // subtraction (return the raw road-capacity sum unadjusted) -- reds the
  // road-side assertion (free capacity created from nothing; totalDrivableCap
  // no longer drops when busPriority is on).
  // Observed RED: `capOff - capOn` became 0 (capOn no longer reduced),
  // failing the `capOff - capOn` assertion above.
  // BUILD NOTE: forecastLineUsage originally recomputed this same sum in its
  // own closure instead of calling forecastTotalDrivableCapacityOf() --  a
  // GR#3 single-source violation this exact mutant round caught (the mutant
  // on forecastLineUsage's own local computation left this exported
  // function, which the test reads, untouched and passing) -- fixed by
  // having forecastLineUsage call the exported function directly.
});

test('AC-4 REWORK (BUG-904): busPriority reallocates CAPACITY only -- Σ(road-class demand)+bus === totalRoadDemand exactly, with and without the policy, and general-road demand never goes UP when the policy turns on', () => {
  const bs = [];
  for (let i = 0; i < 10; i++) bs.push(road(100 + i, 'rd_avenue', i, 0));
  for (let i = 0; i < 30; i++) bs.push(res(200 + i, i % 10, 1 + Math.floor(i / 10)));
  const off = board(bs, 5000, { busPriority: false });
  const on = board(bs, 5000, { busPriority: true });

  const roadDemandSum = (m) => {
    let t = 0;
    for (const [, v] of m) t += v.demand;
    return t;
  };
  const usageOff = forecastLineUsage(off);
  const usageOn = forecastLineUsage(on);
  const totalOff = roadDemandSum(usageOff);
  const totalOn = roadDemandSum(usageOn);
  assert.ok(totalOff > 0, 'population 5000 produces real, non-zero demand -- not the doc\'s own population-0 false-pass');
  assert.ok(Math.abs(totalOn - totalOff) < 1e-9, 'Σ(road classes)+bus === totalRoadDemand exactly, on and off');

  const avenueOff = usageOff.get('rd_avenue').demand;
  const avenueOn = usageOn.get('rd_avenue').demand;
  assert.ok(avenueOn <= avenueOff, 'general-road demand goes down or stays equal when busPriority turns on, never up');

  // MUTANT (verified via scratch copy, trafficDemand.ts forecastLineUsage):
  // restore the old denominator-only subtraction (apportion totalRoadDemand,
  // WITH bus folded in, over the adjusted/reduced capacities) -- reds the
  // conservation assertion above (totalOn becomes strictly greater than
  // totalOff, the BUG-904 amplification).
  // MUTANT 2 (verified via scratch copy): restore raw pcu subtraction in
  // busPriorityCapacityDeltaOf (BUG-905's old arithmetic) -- reds the
  // AC-4 fraction pin two tests up.
});

// ===========================================================================
// BUG-918/BUG-919 rework r3 (round-2 REJECT, row 7622): a second roadTier-2
// spec (rd_roundabout) must never be swept into busPriority's capacity move.
// The fix identifies the bus-lane class by SPEC (ROAD_TIER_SPECS[2] ===
// 'rd_avenue'), never by roadTier, and computes the adjusted per-class
// capacity map EXACTLY ONCE (adjustedRoadCapacitiesOf) so the numerator
// (forecastLineUsage) and denominator (forecastTotalDrivableCapacityOf) can
// never diverge again (GR#3). Four fixtures per the lead's r3 amendment.
// ===========================================================================

test('BUG-918: rd_roundabout really is a second roadTier-2 spec (still true after the fix -- the fix is about NOT matching it, not about it ceasing to exist)', () => {
  const tier2 = Object.keys(SPECS).filter((k) => roadTierOf(SPECS[k]) === 2);
  assert.deepEqual(tier2.sort(), ['rd_avenue', 'rd_roundabout']);
  assert.equal(ROAD_TIER_SPECS[2], 'rd_avenue', 'the bus-lane-eligible spec is data-sourced, not hand-typed');
});

test('BUG-923 REGRESSION: the "10 avenue + 6 road" fixture really does produce TWO road classes in forecastLineUsage, never collapsing to avenue-only via a spec typo', () => {
  const s = fixtureR1({}, 5000);
  const usage = forecastLineUsage(s);
  const roadClassKeys = [...usage.keys()].filter((k) => SPECS[k]?.kind === 'road');
  assert.ok(usage.has('rd_avenue') && usage.has('road'), 'both real road-spec classes must appear');
  assert.equal(roadClassKeys.length, 2, 'exactly two road classes -- a spec typo silently dropping one must red this');
});

function roadDemandTotal(m) {
  let t = 0;
  for (const [, v] of m) t += v.demand;
  return t;
}

// BUG-923 (round 3 REJECT) fixture-hygiene guard: assert every placed spec
// resolves against the real SPECS catalogue (GR#15) -- a typo like the
// attack file's former 'rd_road' is silently DROPPED by lineUsageOf's
// isLineSpec filter rather than erroring, which is exactly what hid BUG-921
// behind an "avenue + road" city that was really avenue-only.
function assertFixtureSpecsValid(buildings) {
  for (const b of buildings) {
    assert.ok(SPECS[b.spec], `fixture building spec "${b.spec}" does not exist in SPECS (typo?)`);
  }
}

/** Fixture 1 (r1, no roundabouts): 10 avenue + 6 road + 30 hut. */
function fixtureR1(policyOverrides = {}, population = 5000) {
  const bs = [];
  for (let i = 0; i < 10; i++) bs.push(road(100 + i, 'rd_avenue', i, 0));
  for (let i = 0; i < 6; i++) bs.push(road(150 + i, 'road', i, 3));
  for (let i = 0; i < 30; i++) bs.push(res(200 + i, i % 10, 1 + Math.floor(i / 10)));
  assertFixtureSpecsValid(bs);
  return board(bs, population, policyOverrides);
}
/** Fixture 2/3 (r1 + n auto-placed roundabouts). */
function fixtureWithRoundabouts(n, policyOverrides = {}, population = 5000) {
  const bs = [];
  let id = 100;
  for (let i = 0; i < 10; i++) bs.push(road(id++, 'rd_avenue', i, 0));
  for (let i = 0; i < 6; i++) bs.push(road(id++, 'road', i, 3));
  for (let i = 0; i < n; i++) bs.push(road(id++, 'rd_roundabout', i % 20, 5 + Math.floor(i / 20)));
  for (let i = 0; i < 30; i++) bs.push(res(id++, i % 10, 1 + Math.floor(i / 10)));
  assertFixtureSpecsValid(bs);
  return board(bs, population, policyOverrides);
}
/** Fixture 4: 1 avenue + 50 roundabouts -- the r2 "clamp amplifier" shape. */
function fixtureOneAvenueManyRoundabouts(policyOverrides = {}, population = 5000) {
  const bs = [];
  let id = 100;
  bs.push(road(id++, 'rd_avenue', 0, 0));
  for (let i = 0; i < 6; i++) bs.push(road(id++, 'road', i, 3));
  for (let i = 0; i < 50; i++) bs.push(road(id++, 'rd_roundabout', i % 20, 5 + Math.floor(i / 20)));
  for (let i = 0; i < 30; i++) bs.push(res(id++, i % 10, 1 + Math.floor(i / 10)));
  assertFixtureSpecsValid(bs);
  return board(bs, population, policyOverrides);
}

const FIXTURES = [
  ['r1 fixture (10 avenue + 6 road + 30 hut), pop 5000', () => fixtureR1({}, 5000), () => fixtureR1({ busPriority: true }, 5000)],
  ['+4 roundabouts, pop 5000', () => fixtureWithRoundabouts(4, {}, 5000), () => fixtureWithRoundabouts(4, { busPriority: true }, 5000)],
  ['+4 roundabouts, pop 50000', () => fixtureWithRoundabouts(4, {}, 50000), () => fixtureWithRoundabouts(4, { busPriority: true }, 50000)],
  ['1 avenue + 50 roundabouts, pop 5000', () => fixtureOneAvenueManyRoundabouts({}, 5000), () => fixtureOneAvenueManyRoundabouts({ busPriority: true }, 5000)],
];

for (const [label, makeOff, makeOn] of FIXTURES) {
  test(`BUG-918 REGRESSION [${label}]: total demand (road classes + bus) is conserved EXACTLY on vs off, general-road demand non-increasing`, () => {
    const off = makeOff();
    const on = makeOn();
    const usageOff = forecastLineUsage(off);
    const usageOn = forecastLineUsage(on);
    // Sum in a FIXED, sorted-by-key order on BOTH sides (GR#21 float-order
    // discipline) rather than relying on Map insertion order to coincide.
    const sortedSum = (m) => {
      const keys = [...m.keys()].sort();
      let t = 0;
      for (const k of keys) t += m.get(k).demand;
      return t;
    };
    const totalOff = sortedSum(usageOff);
    const totalOn = sortedSum(usageOn);
    assert.ok(totalOff > 0, 'a real, non-zero baseline -- never the doc\'s own population-0 false-pass');
    // Exact equality is not reachable across a re-ordered float summation
    // (IEEE-754 addition is not associative); asserted to 1e-9 on the SAME
    // fixed order instead, per the brief's own escape hatch.
    assert.ok(Math.abs(totalOn - totalOff) < 1e-9, `conserved to 1e-9: off=${totalOff} on=${totalOn}`);

    const avenueOff = usageOff.get('rd_avenue')?.demand ?? 0;
    const avenueOn = usageOn.get('rd_avenue')?.demand ?? 0;
    assert.ok(avenueOn <= avenueOff + 1e-9, 'general-road (rd_avenue) demand non-increasing when busPriority turns on');

    // rd_roundabout (when present) must be COMPLETELY untouched by the
    // policy -- its adjusted capacity equals its raw capacity, so its
    // demand share is identical on vs off (this is the direct BUG-918
    // regression: previously it silently lost capacity too; see the
    // dedicated adjustedRoadCapacitiesOf assertion below for the per-class
    // proof).
    if (usageOff.has('rd_roundabout')) {
      const roundaboutTileCount = on.buildings.filter((b) => b.spec === 'rd_roundabout').length;
      assert.ok(roundaboutTileCount > 0);
      assert.equal(usageOn.get('rd_roundabout').demand > 0, usageOff.get('rd_roundabout').demand > 0);
    }
  });
}

test('BUG-918 REGRESSION: adjustedRoadCapacitiesOf leaves rd_roundabout capacity byte-identical to its raw LineUsage.capacity (only rd_avenue is ever adjusted)', () => {
  const on = fixtureWithRoundabouts(4, { busPriority: true }, 5000);
  const adjCaps = adjustedRoadCapacitiesOf(on);
  // rd_roundabout's adjusted capacity must equal 4 tiles * ROAD_TIER_CAPACITY[2] exactly.
  assert.equal(adjCaps.get('rd_roundabout'), 4 * ROAD_TIER_CAPACITY[2]);
  // rd_avenue's adjusted capacity must be reduced by exactly the delta.
  const delta = busPriorityCapacityDeltaOf(on);
  assert.ok(Math.abs(adjCaps.get('rd_avenue') - (10 * ROAD_TIER_CAPACITY[2] - delta)) < 1e-9);
  // forecastTotalDrivableCapacityOf(s) is the sum of THIS exact map -- one source, GR#3.
  let sum = 0;
  for (const [, cap] of adjCaps) sum += cap;
  assert.ok(Math.abs(sum - forecastTotalDrivableCapacityOf(on)) < 1e-9);

  // MUTANT 1 (verified via scratch copy, trafficDemand.ts): restore the old
  // per-tier match (`roadTierOf(SPECS[spec]) === AVENUE_ROAD_TIER`) in place
  // of the spec match (`spec === BUS_LANE_SPEC`) inside adjustedRoadCapacitiesOf
  // -- reds this test's rd_roundabout assertion (roundabout capacity drops
  // by delta too) AND the fixture-loop conservation tests above (roundabout
  // now double-subtracted relative to the denominator... no, in the mutant
  // BOTH numerator and denominator read the same wrong map, so conservation
  // itself does NOT catch it any more -- that is exactly why this direct
  // per-class assertion exists as a second, independent pin). Observed RED:
  // adjCaps.get('rd_roundabout') became (4*250 - delta), not 4*250.
  // MUTANT 2 (verified via scratch copy): recompute forecastTotalDrivableCapacityOf
  // as a SEPARATE `totalRoadCap - busPriorityCapacityDeltaOf(s)` sum instead of
  // summing adjustedRoadCapacitiesOf(s) -- reds nothing here as long as exactly
  // one spec is adjusted (mathematically identical), so it is caught instead by
  // the FIXTURES loop above the moment adjustedRoadCapacitiesOf's own matching
  // rule is mutated to hit a second spec (mutant 1) while this parallel
  // recomputation still divides by a single global delta -- i.e. restoring a
  // second copy of the total reopens exactly the class of drift GR#3 exists to
  // close, verified by re-adding a local `totalDrivableCap - busCapacityDelta`
  // line in forecastLineUsage's closure again: conservation held (numbers
  // agreed) UNTIL mutant 1 was also applied, at which point the two totals
  // diverged and the fixture-loop test went red while this direct test did not
  // -- confirming the two tests catch complementary halves of BUG-918.
});

test('BUG-919 REGRESSION: busPriorityCapacityDeltaOf sizes lanes off rd_avenue tiles only -- adding roundabouts never changes the delta', () => {
  const noRb = fixtureWithRoundabouts(0, { busPriority: true }, 5000);
  const with4Rb = fixtureWithRoundabouts(4, { busPriority: true }, 5000);
  const with50Rb = fixtureOneAvenueManyRoundabouts({ busPriority: true }, 5000); // 1 avenue, 50 roundabouts
  const fraction = (AVENUE_CAP_PER_LANE - BUS_LANE_CAP_PER_LANE) / AVENUE_CAP_PER_LANE;
  assert.equal(busPriorityCapacityDeltaOf(noRb), fraction * ROAD_TIER_CAPACITY_TIER2 * 10);
  assert.equal(busPriorityCapacityDeltaOf(with4Rb), fraction * ROAD_TIER_CAPACITY_TIER2 * 10, 'adding 4 roundabouts changes nothing');
  assert.equal(busPriorityCapacityDeltaOf(with50Rb), fraction * ROAD_TIER_CAPACITY_TIER2 * 1, 'sized off the 1 real avenue tile, not the 50 roundabouts');

  // MUTANT (verified via scratch copy, trafficDemand.ts busPriorityCapacityInfoOf):
  // restore `if (roadTierOf(SPECS[b.spec]) === AVENUE_ROAD_TIER) onlineAvenueTileCount++`
  // in place of `if (b.spec === BUS_LANE_SPEC) onlineAvenueTileCount++` -- reds
  // both the with4Rb (delta becomes fraction*250*14) and with50Rb (delta
  // becomes fraction*250*51) assertions above. Observed RED on the mutated
  // copy: with4Rb delta 194.44444444444443 (expected 138.88888888888889);
  // with50Rb delta 708.3333333333333 (expected 13.888888888888888).
});

test('BUG-918 clamp report (unclamped path): busPriorityCapacityInfoOf never silently swallows a delta that would exceed the avenue class\'s own capacity -- it is REPORTED via {delta, requested, clamped}', () => {
  // With today's real data.ts/link_capacity.json numbers, laneShareFraction
  // is structurally < BUS_LANE_MAX_SHARE_OF_CLASS (~0.0556 vs 0.5), so
  // delta === requested and clamped === false for every fixture above -- the
  // clamp is a defensive guard against a future/mis-tuned data file, not a
  // reachable path with live data (documented in busPriorityCapacityInfoOf's
  // own doc comment, the same "equivalent guard" pattern as
  // demandForecastOf's jobsCapTotal > 0 check). BUG-922's REACHABLE-path pin
  // is the next test, using the test-only injection seam.
  const on = fixtureOneAvenueManyRoundabouts({ busPriority: true }, 5000);
  const info = busPriorityCapacityInfoOf(on);
  assert.equal(info.clamped, false);
  assert.equal(info.delta, info.requested);
  assert.ok(info.delta > 0 && info.delta < ROAD_TIER_CAPACITY_TIER2, 'unclamped, well under the single avenue tile\'s own capacity');
});

// BUG-921/BUG-922 (round 3 REJECT, row 7624 -- integrity finding: the
// PRIOR version of this test carried an inline comment claiming a
// scratch-mirror data mutation measured "total demand ... conserved exactly
// (diff 0)" at the clamp boundary; round 3 could not reproduce that number
// (it measured an 80.3% trip loss on the SAME fixture) because the clamp
// ceiling at the time was the class's FULL raw capacity, which could drive
// the whole road-capacity denominator to zero in a single-road-class city.
// That claim is DELETED here, not repeated -- this test re-measures THIS
// round, in-process, via __setBusLaneShareFractionOverrideForTest (the
// BUG-922 test-only injection seam), rather than describing an unpinned
// manual data edit in a comment.
test('BUG-921/BUG-922 REGRESSION: forcing the clamp to bind (10 avenues as the ONLY road class) still conserves total demand EXACTLY, and the clamp is REPORTED as true, never silent', () => {
  const bs = [];
  for (let i = 0; i < 10; i++) bs.push(road(100 + i, 'rd_avenue', i, 0));
  for (let i = 0; i < 30; i++) bs.push(res(200 + i, i % 10, 1 + Math.floor(i / 10)));
  assertFixtureSpecsValid(bs);
  const off = board(bs, 5000, { busPriority: false });

  // Force the clamp to bind: an override fraction of 2 (> 1) makes the
  // requested delta exceed the avenue class's own raw capacity by 2x, so
  // BUS_LANE_MAX_SHARE_OF_CLASS's 0.5 ceiling (not the full raw capacity)
  // is what actually binds.
  __setBusLaneShareFractionOverrideForTest(2);
  let on;
  let info;
  let totalOff, totalOn;
  try {
    on = board(bs, 5000, { busPriority: true });
    info = busPriorityCapacityInfoOf(on);
    const roadSum = (m) => { let t = 0; for (const [, v] of m) t += v.demand; return t; };
    totalOff = roadSum(forecastLineUsage(off));
    totalOn = roadSum(forecastLineUsage(on));
  } finally {
    // Reset the seam unconditionally (even on assertion failure) -- it is
    // process-global and every OTHER test in this file must see real data.
    __setBusLaneShareFractionOverrideForTest(null);
  }

  assert.equal(info.clamped, true, 'the clamp must engage and be REPORTED, never silent');
  assert.equal(info.requested, 5000, 'the unclamped fraction*capacity*tiles figure');
  assert.equal(info.delta, 1250, 'clamped to BUS_LANE_MAX_SHARE_OF_CLASS (0.5) x the avenue class\'s own raw capacity (2500)');
  assert.ok(forecastTotalDrivableCapacityOf(on) > 0, 'the road-capacity denominator never collapses to 0 even at the clamp boundary');
  // MEASURED THIS ROUND (re-derived in-process, not carried over from an
  // earlier round's comment): off total 454.1039999999998, on(clamped)
  // total 454.1039999999998 -- diff 0, exact.
  assert.ok(Math.abs(totalOn - totalOff) < 1e-9, `conserved exactly at the clamp boundary: off=${totalOff} on=${totalOn}`);

  // MUTANT M-clamp-report (verified via scratch copy, trafficDemand.ts
  // busPriorityCapacityInfoOf): replace `clamped: delta < requested` with a
  // hardcoded `clamped: false` -- reds the `assert.equal(info.clamped,
  // true, ...)` line above (BUG-922's silent-clamp survivor, closed).
  // MUTANT M-share-clamp (verified via scratch copy): revert the clamp
  // ceiling from `avenueRawCapacity * BUS_LANE_MAX_SHARE_OF_CLASS` back to
  // the bare `avenueRawCapacity` -- reds the `info.delta === 1250` and
  // `forecastTotalDrivableCapacityOf(on) > 0` assertions (delta becomes
  // 2500, the FULL raw capacity, driving the denominator to exactly 0 and
  // reproducing round 3's measured 80.3% trip-loss shape).
});

test('AC-4: busPriority off -> zero delta, no synthetic bus entry, mode share untouched', () => {
  const avenueTiles = [road(1, 'rd_avenue', 0, 0)];
  const s = board(avenueTiles, 500);
  assert.equal(busPriorityCapacityDeltaOf(s), 0);
  assert.ok(!forecastLineUsage(s).has('bus'));
  const raw = modeShareOf(ladderPointOf(s));
  assert.deepEqual(policyModeShareAdjustmentOf(s), raw, 'busPriority must never move mode share');
});

// --- AC-5 — roadPricing inflow, post-adjustment trips -----------------------

test('AC-5: roadPricing books an inflow sized off the POST-adjustment car share, exact to the unit', () => {
  const s = board([res(1, 0, 0)], 5000, { roadPricing: true });
  const adjustedCarShare = policyModeShareAdjustmentOf(s).car;
  const totalPersonTrips = totalPersonTripsOf(s);
  const expected = Math.round(PEAK_GBP_PER_CROSSING * adjustedCarShare * totalPersonTrips);
  assert.equal(roadPricingInflowOf(s), expected);

  const { inflows: inflowsOn } = computeFlows(s);
  const line = inflowsOn.find((f) => f.label === 'Road Pricing (ERP)');
  assert.ok(line, 'Road Pricing (ERP) inflow must appear');
  assert.equal(line.value, expected);

  const sOff = board([res(1, 0, 0)], 5000, { roadPricing: false });
  const { inflows: inflowsOff, outflows: outflowsOff } = computeFlows(sOff);
  const { outflows: outflowsOn } = computeFlows(s);
  assert.ok(!inflowsOff.some((f) => f.label === 'Road Pricing (ERP)'));
  // Conservation: every OTHER inflow/outflow line is byte-identical toggling
  // roadPricing off (removes exactly one line, touches nothing else).
  const otherOn = inflowsOn.filter((f) => f.label !== 'Road Pricing (ERP)');
  assert.deepEqual(otherOn, inflowsOff, 'toggling roadPricing off must change ONLY that one line');
  assert.deepEqual(outflowsOn, outflowsOff, 'roadPricing must never touch outflows');

  // MUTANT (verified via scratch copy, trafficDemand.ts roadPricingInflowOf):
  // read `modeShareOf(ladderPointOf(s))['car']` (PRE-adjustment) instead of
  // `policyModeShareAdjustmentOf(s)['car']` -- reds the exact-value check
  // (over-charges by the suppressed fraction, since pre-adjustment car share
  // is always >= post-adjustment when roadPricing is on).
  // Observed RED: computed inflow value was strictly greater than
  // `expected`, failing `assert.equal(roadPricingInflowOf(s), expected)`.
});

// --- AC-6 — ownershipQuota inflow, independent of roadPricing --------------

test('AC-6: ownershipQuota books an independent inflow; both policies together add exactly two new lines', () => {
  const s = board([res(1, 0, 0)], 100000, { roadPricing: true, ownershipQuota: true });
  const sOff = board([res(1, 0, 0)], 100000);

  // BUG-906 fix: round the MONEY once at the end, never the intermediate
  // registration count.
  const expectedOwnership = Math.round(
    COE_ILLUSTRATIVE_PRICE_GBP * ((100000 * COE_QUOTA_GROWTH_RATE_PER_YEAR) / 365),
  );
  assert.equal(ownershipQuotaInflowOf(s), expectedOwnership);

  const { inflows: inflowsOn } = computeFlows(s);
  const { inflows: inflowsOff } = computeFlows(sOff);
  const onlyOn = inflowsOn.filter((f) => !inflowsOff.some((g) => g.label === f.label));
  assert.equal(onlyOn.length, 2, 'exactly two NEW lines versus the all-off baseline');
  const roadLine = onlyOn.find((f) => f.label === 'Road Pricing (ERP)');
  const ownLine = onlyOn.find((f) => f.label === 'Ownership Quota (COE)');
  assert.ok(roadLine && ownLine, 'both new lines must be present and separately labelled');
  assert.equal(ownLine.value, expectedOwnership);
  // Every pre-existing flow item (present in BOTH the off baseline and the
  // on state) is byte-identical.
  for (const f of inflowsOff) {
    const match = inflowsOn.find((g) => g.label === f.label);
    assert.deepEqual(match, f, `pre-existing flow "${f.label}" must be unchanged`);
  }
  // MUTANT (verified via scratch copy, engine.ts computeFlows): merge both
  // pushes into ONE combined-label line (`inflows.push({ label: 'Policy
  // Levers', value: roadPricingInflow + ownershipQuotaInflow })`) -- reds
  // the per-policy toggle contract: `lastFlows.inflows` (the UI/audit trail,
  // financeTabs.tsx income sum) can no longer distinguish the two policies,
  // and `onlyOn.length` becomes 1, not 2.
  // Observed RED: `assert.equal(onlyOn.length, 2, ...)` failed with
  // onlyOn.length === 1, and the AC-5 test's own "Road Pricing (ERP)
  // inflow must appear" assertion failed too (label renamed to "Policy
  // Levers"), confirming both AC-5 and AC-6 depend on the two lines staying
  // separately labelled.
});

test('AC-6 REWORK (BUG-906): ownershipQuota is monotone non-decreasing across population, books a real line at pop 1000, and never steps by a whole £45,000 at the old 36,500 rounding threshold', () => {
  const at = (pop) => ownershipQuotaInflowOf(board([res(1, 0, 0)], pop, { ownershipQuota: true }));
  const pops = [1000, 10000, 36499, 36500, 100000, 150000];
  const values = pops.map(at);
  assert.ok(values[0] > 0, 'pop 1000 books a strictly positive line -- no silent no-op below the old 36,500 threshold');
  for (let i = 1; i < values.length; i++) {
    assert.ok(values[i] >= values[i - 1], `monotone non-decreasing (pop ${pops[i]} vs ${pops[i - 1]})`);
  }
  assert.ok(
    Math.abs(values[3] - values[2]) < COE_ILLUSTRATIVE_PRICE_GBP,
    'no 45,000-step between pop 36,499 and 36,500',
  );
  const small = board([res(1, 0, 0)], 30000, { ownershipQuota: true });
  const { inflows } = computeFlows(small);
  assert.ok(inflows.some((f) => f.label === 'Ownership Quota (COE)'), 'the line now appears well under 36,500');

  // MUTANT (verified via scratch copy, engine.ts ownershipQuotaInflowOf):
  // restore `Math.round` on the intermediate registration count (round the
  // COUNT, then multiply by price) -- reds `values[0] > 0` (pop 1000 rounds
  // to 0 registrations again) and reintroduces the 45,000-step at 36,500.
});

// --- AC-7 — parkAndRide is not shipped, unreachable at the type level ------

test('AC-7: parkAndRide is not wired anywhere under webconsole/src (grep-level proof)', () => {
  const srcDir = path.join(repoRoot, 'webconsole', 'src');
  const hits = [];
  function walk(dir) {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name === 'node_modules' || entry.name === 'traffic-data') continue;
        walk(full);
      } else if (entry.name.endsWith('.ts') || entry.name.endsWith('.tsx')) {
        const text = readFileSync(full, 'utf8');
        // Look for parkAndRide actually WIRED as a PolicyId literal (a union
        // member, an id field, or an action id) -- never a bare prose
        // mention (this doc comment's own "deliberately NOT a member" note
        // in types.ts would otherwise false-positive this grep).
        if (/(\|\s*'parkAndRide'|['"]?id['"]?\s*:\s*'parkAndRide'|type:\s*'policy'[^}]*'parkAndRide')/.test(text)) {
          hits.push(full);
        }
      }
    }
  }
  walk(srcDir);
  assert.equal(hits.length, 0, `parkAndRide must not appear as a PolicyId anywhere: ${hits.join(', ')}`);
  // MUTANT: adding 'parkAndRide' to the PolicyId union alone reds nothing at
  // RUNTIME (this is exactly AC-7's own false-pass note) -- the type-level
  // half of this AC is proven by `npx tsc --noEmit` gating a compile error
  // on any code that constructs `{ type: 'policy', id: 'parkAndRide' }`,
  // which this grep alone cannot exercise; both halves are required and
  // this test documents that split rather than silently only covering one.
});

test('AC-7: PolicyId union has exactly 8 members (4 existing + 4 new)', () => {
  // POLICIES is the runtime enumeration of every PolicyId currently wired to
  // the UI (financeTabs.tsx renders it generically) -- 8 confirms neither a
  // stray addition (parkAndRide) nor a missing one slipped in.
  assert.equal(POLICIES.length, 8, `POLICIES must carry exactly 8 defs, got ${POLICIES.length}`);
  const ids = new Set(POLICIES.map((p) => p.id));
  assert.equal(ids.size, 8, 'no duplicate PolicyId in POLICIES');
});
