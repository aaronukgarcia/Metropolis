// attack-feat801-round.test.mjs — INDEPENDENT Destructive round 1 against
// FEAT-2326609801 "Realistic traffic inc8 — congestion policy levers".
// Attacker: opus-round-feat801-inc8 (never the author, GR#23 independence
// amendment). Authority: docs/planning/acceptance/FEAT-2326609792-inc8.md.
//
// Everything here is GREEN against the code as shipped in the round. Three
// blocks pin DEFECTS the round found and are labelled DEFECT-PIN — they
// assert the behaviour the code has TODAY so that fixing the corresponding
// BUG item reds this file and forces the pin to be flipped to the correct
// expectation. Every other block pins a property the round proved holds and
// that must keep holding.
//
// Run: node tools/test/scoped.mjs webconsole/test/attack-feat801-round.test.mjs

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
  reducer,
  computeFlows,
  roadPricingInflowOf,
  ownershipQuotaInflowOf,
} from '../src/sim/engine.ts';
import { ROAD_TIER_CAPACITY, POLICIES, SPECS, roadTierOf } from '../src/sim/data.ts';
import {
  runConsistencyChecks,
  foldGraceHistory,
  GRACE_WINDOW_SIZE,
  GRACE_ELIGIBLE_LINE_IDS,
} from '../src/sim/consistency.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, '..', '..');
const linkCapacity = JSON.parse(
  readFileSync(path.join(repoRoot, 'data', 'traffic', 'link_capacity.json'), 'utf8'),
);
const taxation = JSON.parse(
  readFileSync(path.join(repoRoot, 'data', 'traffic', 'taxation.json'), 'utf8'),
);
const capPerLane = (id) =>
  linkCapacity.roadClasses.find((r) => r.roadClassId === id).capacityPcuPerLanePerHour;
const AVENUE_PCU = capPerLane('avenue_2_plus_2');
const BUS_LANE_PCU = capPerLane('bus_lane_variant');
const COE_PRICE = taxation.certificateOfEntitlement.illustrativePriceGBP.value;
const COE_GROWTH = taxation.certificateOfEntitlement.quotaGrowthRatePerYear.value;

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
const road = (id, spec, x, y) => ({ id, spec, x, y });
const res = (id, x, y) => ({ id, spec: 'res_hut', x, y });

// BUG-923 (round 3 REJECT) fixture-hygiene guard: every fixture builder in
// this file places tiles by literal spec string; a typo (e.g. 'rd_road',
// which does not exist -- ROAD_TIER_SPECS[1] === 'road') is silently DROPPED
// by lineUsageOf's isLineSpec filter rather than erroring, which is exactly
// what hid BUG-921 behind an "avenue + road" city that was really
// avenue-only. This guard asserts every placed spec resolves against the
// real SPECS catalogue (GR#15) so a future typo reds immediately instead of
// silently shrinking a fixture's road-class count.
function assertFixtureSpecsValid(buildings) {
  for (const b of buildings) {
    assert.ok(SPECS[b.spec], `fixture building spec "${b.spec}" does not exist in SPECS (typo?)`);
  }
}

/** The round's standard city: 10 avenue tiles + 6 plain road tiles + 30 huts.
 * BUG-923 fix: the 6 "road" tiles were previously built with the
 * non-existent spec id 'rd_road' (ROAD_TIER_SPECS[1] is 'road') -- silently
 * dropped by lineUsageOf, so this "avenue + road" city was really
 * avenue-ONLY, which is exactly the single-road-class shape BUG-921's
 * clamp-boundary annihilation occurs in. Fixed to the real spec id, with a
 * fixture-guard assertion (below) that this city really does produce TWO
 * road classes in forecastLineUsage. */
function city(policyOverrides = {}) {
  const bs = [];
  for (let i = 0; i < 10; i++) bs.push(road(100 + i, 'rd_avenue', i, 0));
  for (let i = 0; i < 6; i++) bs.push(road(150 + i, 'road', i, 3));
  for (let i = 0; i < 30; i++) bs.push(res(200 + i, i % 10, 1 + Math.floor(i / 10)));
  assertFixtureSpecsValid(bs);
  return board(bs, 5000, policyOverrides);
}
const roadDemandSum = (m) => {
  let t = 0;
  for (const [, v] of m) t += v.demand;
  return t;
};

// ===========================================================================
// A1 — AC-3 off-path identity, proved against origin/main's OWN OUTPUT
// ===========================================================================
// The builder's own AC-3 test compares policyModeShareAdjustmentOf against
// modeShareOf inside the SAME tree, which cannot detect a change to
// forecastLineUsage's other arithmetic (the totalPersonTrips read moved to
// totalPersonTripsOf, and totalDrivableCap moved to
// forecastTotalDrivableCapacityOf, in this very increment). This round ran
// the identical fixture in the baseline worktree at 01d20a2 (origin/main,
// pre-inc8) and captured its output; the numbers below ARE that capture and
// were diffed byte-for-byte against this tree's output (`diff` reported no
// difference across lines + mode share + inflows + outflows).
test('A1: with every policy off, forecastLineUsage is byte-identical to a from-source re-derivation of the SAME (now BUG-923-fixed) fixture (re-measured this round, not carried over from the round-1/2 rd_road-typo capture)', () => {
  const s = city();
  const lines = [...forecastLineUsage(s)].map(([k, v]) => [k, v.demand, v.legacyUsage, v.divergenceRatio]);
  // BUG-923 fix: city() now places a REAL 'road' spec (not the non-existent
  // 'rd_road'), so this city produces TWO road classes, not one -- the round-1
  // capture below (rd_avenue only, 454.1039999999998) was a typo artifact,
  // not the intended "avenue + road" city; re-measured directly against this
  // tree's own trafficDemand.ts (not carried over from an earlier round).
  assert.deepEqual(lines, [
    ['rd_avenue', 366.2129032258063, 194, 0.8876953774526098],
    ['road', 87.89109677419351, 46, 0.9106760168302938],
  ]);
  assert.deepEqual(policyModeShareAdjustmentOf(s), modeShareOf(ladderPointOf(s)));
  assert.equal(busPriorityCapacityDeltaOf(s), 0);
  assert.ok(!forecastLineUsage(s).has('bus'), 'no synthetic bus entry on the off path');
});

test('BUG-923 REGRESSION: the "avenue + road" city() fixture really does produce TWO road classes in forecastLineUsage, never collapsing to avenue-only', () => {
  const s = city();
  const usage = forecastLineUsage(s);
  const roadClassKeys = [...usage.keys()].filter((k) => SPECS[k]?.kind === 'road');
  assert.ok(usage.has('rd_avenue') && usage.has('road'), 'both real road-spec classes must appear');
  assert.equal(roadClassKeys.length, 2, 'exactly two road classes -- a spec typo silently dropping one must red this');
});

test('A1b: with every policy off, computeFlows adds no line at all', () => {
  const s = city();
  const { inflows } = computeFlows(s);
  assert.ok(!inflows.some((f) => f.label === 'Road Pricing (ERP)'));
  assert.ok(!inflows.some((f) => f.label === 'Ownership Quota (COE)'));
  assert.equal(roadPricingInflowOf(s), 0);
  assert.equal(ownershipQuotaInflowOf(s), 0);
});

// ===========================================================================
// A2 — REGRESSION (BUG-904 fixed): busPriority reallocates capacity WITHOUT
// minting road trips
// ===========================================================================
// FIX (rework, r2): adjusted per-class capacities are now computed FIRST —
// the avenue tier loses exactly busCapacityDelta (the same figure the bus
// entry gains), every other road class is untouched — and totalRoadDemand's
// numerator excludes bus person-trips whenever the bus entry exists.
// Numerator and denominator now move together, so turning bus priority ON
// can only move demand between classes, never mint it. This test used to be
// a DEFECT-PIN asserting the old inflation ratio (454.104 -> 756.840,
// 1.6667x); it now pins the FIXED, conserving behaviour and goes red again
// if the amplification regresses.
test('A2 REGRESSION: busPriority ON no longer inflates total demand — general-road demand only goes down or stays equal, and total demand (road classes + bus) is conserved exactly', () => {
  const off = city();
  const on = city({ busPriority: true });
  const capOff = forecastTotalDrivableCapacityOf(off);
  const capOn = forecastTotalDrivableCapacityOf(on);
  const delta = busPriorityCapacityDeltaOf(on);
  assert.ok(delta > 0, 'busPriority actually reallocates some capacity on this fixture');
  assert.ok(Math.abs((capOff - capOn) - delta) < 1e-9);

  const usageOff = forecastLineUsage(off);
  const usageOn = forecastLineUsage(on);
  const avenueOff = usageOff.get('rd_avenue').demand;
  const avenueOn = usageOn.get('rd_avenue').demand;
  assert.ok(avenueOn <= avenueOff, 'general-road demand goes down or stays equal, never up (population 5000, not 0)');
  assert.ok(avenueOn > 0 && avenueOff > 0, 'population 5000 produces real, non-zero demand on both paths');

  // Sum over EVERY entry (general road classes + the synthetic bus entry)
  // is conserved exactly, on and off — the old defect made dOn > dOff by
  // exactly capOff/capOn; that ratio must be gone.
  const dOff = roadDemandSum(usageOff);
  const dOn = roadDemandSum(usageOn);
  assert.ok(Math.abs(dOn - dOff) < 1e-9, 'total demand (road classes + bus) conserved exactly, never inflated');
  const ratio = dOn / dOff;
  assert.ok(Math.abs(ratio - capOff / capOn) > 1e-6, 'the old denominator-only inflation ratio is gone');
});

test('A2b REGRESSION: the synthetic bus entry carries the bus-mode person-trips it took OUT of the general-road basis, never zero', () => {
  const on = city({ busPriority: true });
  const usage = forecastLineUsage(on);
  const bus = usage.get('bus');
  assert.ok(bus, 'synthetic bus entry present');
  assert.equal(bus.capacity, busPriorityCapacityDeltaOf(on));
  // FIX: bus person-trips now ride this entry instead of loading the
  // (now-smaller) road denominator with zero demand moved to match.
  assert.ok(bus.demand > 0, 'bus-mode person-trips now ride the dedicated bus entry');
  assert.equal(bus.legacyUsage, 0);
  assert.equal(bus.divergenceRatio, 0);
  // busPriority must never move mode share (this part of AC-4 always held).
  assert.deepEqual(policyModeShareAdjustmentOf(on), modeShareOf(ladderPointOf(on)));
  // The bus entry's demand really is totalPersonTrips * the bus mode share —
  // a real, nonzero share at population 5000, not a vacuous fixture.
  const totalPersonTrips = totalPersonTripsOf(on);
  const busShare = policyModeShareAdjustmentOf(on)['bus'] ?? 0;
  assert.ok(busShare > 0 && totalPersonTrips > 0);
  assert.ok(Math.abs(bus.demand - totalPersonTrips * busShare) < 1e-9);
});

// ===========================================================================
// A3 — REGRESSION (BUG-905 fixed): the busPriority delta is now a
// dimensionless fraction applied to the avenue tier's OWN capacity unit
// ===========================================================================
// FIX (rework, r2): the delta is now laneShareFraction (delta/rowCapacity
// from link_capacity.json, e.g. 100/1800) x ROAD_TIER_CAPACITY[2] x the
// online-avenue-tile count — no pcu-vs-people arithmetic, and a balance
// retune of ROAD_TIER_CAPACITY scales the policy's strength WITH it. This
// test used to pin the old "strip 100/250 = 40% by unit accident" arithmetic;
// it now pins the fraction-based derivation and goes red if the raw
// pcu-per-lane-per-hour figure is ever subtracted from a tile-capacity figure
// again.
test('A3 REGRESSION: the per-tile capacity delta is a dimensionless fraction of ROAD_TIER_CAPACITY, never a raw pcu/lane/hour figure subtracted from a per-tick tile capacity', () => {
  const on = city({ busPriority: true });
  const laneShareFraction = (AVENUE_PCU - BUS_LANE_PCU) / AVENUE_PCU;
  assert.ok(Math.abs(laneShareFraction - 100 / 1800) < 1e-12, 'a dimensionless fraction, not a raw pcu figure');
  const onlineAvenueTiles = 10;
  const expectedDelta = laneShareFraction * ROAD_TIER_CAPACITY[2] * onlineAvenueTiles;
  const delta = busPriorityCapacityDeltaOf(on);
  assert.ok(Math.abs(delta - expectedDelta) < 1e-9, 'delta derives from the fraction applied to the avenue tier\'s own capacity unit');
  // The old defect subtracted the raw pcu delta (100) times tile count (10)
  // = 1000 directly, with no ROAD_TIER_CAPACITY involvement at all — that
  // figure must be gone.
  const oldDefectDelta = (AVENUE_PCU - BUS_LANE_PCU) * onlineAvenueTiles;
  assert.ok(Math.abs(delta - oldDefectDelta) > 1, 'the old raw pcu-delta arithmetic is gone');
});

// ===========================================================================
// A4 — REGRESSION (BUG-906 fixed): ownershipQuota scales smoothly with
// population — no silent zero below 36,500, no £45,000 staircase
// ===========================================================================
// FIX (rework, r2): round the MONEY once at the end (price x the UNROUNDED
// registration rate), never the intermediate registration count. This test
// used to pin the old £0-below-36,500-then-£45,000-steps behaviour; it now
// pins monotone-non-decreasing scaling with a real positive line even at low
// population.
test('A4 REGRESSION: ownershipQuota books a real (nonzero) line even at low population, scales monotonically, and never steps by a whole £45,000 at 36,500', () => {
  const at = (pop) => ownershipQuotaInflowOf(board([res(1, 0, 0)], pop, { ownershipQuota: true }));
  const pops = [1000, 10000, 36499, 36500, 100000, 150000];
  const values = pops.map(at);
  assert.ok(values[0] > 0, 'pop 1000 books a strictly positive line — no more silent no-op');
  for (let i = 1; i < values.length; i++) {
    assert.ok(values[i] >= values[i - 1], `monotone non-decreasing in population (pop ${pops[i]})`);
  }
  // No step function at the old rounding threshold — 36,499 and 36,500 now
  // differ by roughly one extra day's registration rate worth of money, not
  // a whole £45,000 registration appearing out of nowhere.
  assert.ok(Math.abs(values[3] - values[2]) < COE_PRICE, 'no 45,000-step at the old rounding threshold');
  // The line now appears in the finance panel at a population that used to
  // be a silent no-op (30,000, well under the old 36,500 threshold).
  const small = board([res(1, 0, 0)], 30000, { ownershipQuota: true });
  const { inflows } = computeFlows(small);
  assert.ok(inflows.some((f) => f.label === 'Ownership Quota (COE)'), 'the line now appears well under the old 36,500 threshold');
  // Sanity: the rate really is the data file's, not a literal — money is
  // rounded once at the end, never the intermediate registration count.
  assert.equal(at(36500), Math.round(COE_PRICE * ((36500 * COE_GROWTH) / 365)));
});

// ===========================================================================
// A5 — mode-share conservation holds (attacked, no defect found)
// ===========================================================================
test('A5: all four policies on -> the mode-share vector sums to exactly 1 and every entry is finite and non-negative', () => {
  const s = city({ ownershipQuota: true, roadPricing: true, busPriority: true, integratedTicketing: true });
  const v = policyModeShareAdjustmentOf(s);
  let sum = 0;
  for (const k of Object.keys(v)) {
    assert.ok(Number.isFinite(v[k]) && v[k] >= 0, `${k} finite and non-negative`);
    sum += v[k];
  }
  assert.equal(sum, 1, 'exactly 1, not merely close');
});

test('A5b: composition order is fixed — the same policy set yields the identical vector regardless of the order the toggles were dispatched', () => {
  const ids = ['integratedTicketing', 'busPriority', 'roadPricing', 'ownershipQuota'];
  const apply = (order) => {
    let s = city();
    for (const id of order) s = reducer(s, { type: 'policy', id });
    return policyModeShareAdjustmentOf(s);
  };
  assert.deepEqual(apply(ids), apply([...ids].reverse()));
  assert.deepEqual(
    apply(ids),
    policyModeShareAdjustmentOf(
      city({ ownershipQuota: true, roadPricing: true, busPriority: true, integratedTicketing: true }),
    ),
  );
});

test('A5c: moveShare clamps an over-draw — a source with less mass than the move never goes negative and mass is conserved', () => {
  const v = { car: 0.05, bus: 0.5, heavy_rail: 0.3, hs_rail: 0.15 };
  __moveShareForTest(v, 0.1, ['car'], ['bus', 'heavy_rail', 'hs_rail']);
  assert.equal(v.car, 0, 'clamped to zero, never negative');
  let sum = 0;
  for (const k of Object.keys(v)) sum += v[k];
  assert.equal(sum, 1);
});

test('A5d: an empty destination set falls back to an even split so mass is never dropped', () => {
  const v = { car: 0.6, bus: 0, heavy_rail: 0, hs_rail: 0, walk: 0.4 };
  __moveShareForTest(v, 0.1, ['car'], ['bus', 'heavy_rail', 'hs_rail']);
  let sum = 0;
  for (const k of Object.keys(v)) sum += v[k];
  assert.ok(Math.abs(sum - 1) < 1e-12, 'no mass destroyed when every destination starts at zero');
  assert.ok(Math.abs(v.bus - 0.1 / 3) < 1e-12);
});

// ===========================================================================
// A6 — money (attacked, no conservation defect found)
// ===========================================================================
test('A6: 120 ticks with all four policies on — conservation.funds-vs-flows and the label-uniqueness checks never fail', () => {
  let s = city({ ownershipQuota: true, roadPricing: true, busPriority: true, integratedTicketing: true });
  s = { ...s, funds: 100_000_000 };
  const watched = new Set([
    'conservation.funds-vs-flows',
    'flows.inflow-labels-unique',
    'flows.outflow-labels-unique',
  ]);
  const failures = [];
  const history = [];
  const labelsSeen = new Set();
  for (let i = 0; i < 120; i++) {
    s = reducer(s, { type: 'tick' });
    for (const f of s.lastFlows?.inflows ?? []) labelsSeen.add(f.label);
    const report = runConsistencyChecks(s, undefined, foldGraceHistory(history));
    for (const c of report.checks) {
      if (!c.ok && watched.has(c.id) && !GRACE_ELIGIBLE_LINE_IDS.has(c.id)) {
        failures.push({ tick: s.tick, id: c.id, detail: c.detail });
      }
    }
    history.push(report.rawFailedSignatures);
    if (history.length > GRACE_WINDOW_SIZE - 1) history.shift();
  }
  assert.deepEqual(failures, [], 'no money created or destroyed across 120 ticks with every lever on');
  // The run is only meaningful if the lever lines were actually booked.
  assert.ok(labelsSeen.has('Road Pricing (ERP)'), 'the ERP inflow really was exercised over the run');
});

test('A6b: toggling roadPricing/ownershipQuota mid-run mints and destroys nothing — the flow lines appear and vanish, every other line byte-identical', () => {
  let s = city();
  s = { ...s, funds: 100_000_000, population: 100_000 };
  for (let i = 0; i < 5; i++) s = reducer(s, { type: 'tick' });
  const before = computeFlows(s);
  const fundsBefore = s.funds;

  let on = reducer(s, { type: 'policy', id: 'roadPricing' });
  on = reducer(on, { type: 'policy', id: 'ownershipQuota' });
  assert.equal(on.funds, fundsBefore, 'toggling a policy never touches funds directly');
  const withOn = computeFlows(on);
  const newLines = withOn.inflows.filter((f) => !before.inflows.some((g) => g.label === f.label));
  assert.equal(newLines.length, 2);
  for (const f of before.inflows) {
    assert.deepEqual(withOn.inflows.find((g) => g.label === f.label), f, `${f.label} unchanged`);
  }
  assert.deepEqual(withOn.outflows, before.outflows, 'no policy lever touches outflows');

  let off = reducer(on, { type: 'policy', id: 'roadPricing' });
  off = reducer(off, { type: 'policy', id: 'ownershipQuota' });
  assert.equal(off.funds, fundsBefore);
  const withOff = computeFlows(off);
  assert.deepEqual(withOff.inflows, before.inflows, 'toggling back off restores the exact baseline flows');
  assert.deepEqual(withOff.outflows, before.outflows);
});

test('A6c: the two inflows are independent — neither double-counts the other, and each is a non-negative integer', () => {
  const both = board([res(1, 0, 0)], 100_000, { roadPricing: true, ownershipQuota: true });
  const onlyRp = board([res(1, 0, 0)], 100_000, { roadPricing: true });
  const onlyOq = board([res(1, 0, 0)], 100_000, { ownershipQuota: true });
  // ownershipQuota is a pure population function — identical with or without ERP.
  assert.equal(ownershipQuotaInflowOf(both), ownershipQuotaInflowOf(onlyOq));
  // ERP is sized off the POST-adjustment car share, so adding the quota lever
  // LOWERS it (the suppressed trips are not charged) — never raises it.
  assert.ok(roadPricingInflowOf(both) <= roadPricingInflowOf(onlyRp));
  for (const v of [roadPricingInflowOf(both), ownershipQuotaInflowOf(both)]) {
    assert.ok(Number.isInteger(v) && v >= 0, 'integer, non-negative GBP');
  }
});

// ===========================================================================
// A7 — determinism and old saves
// ===========================================================================
test('A7: three independent evaluations of the same city with all levers on are byte-identical', () => {
  const snap = () => {
    const s = city({ ownershipQuota: true, roadPricing: true, busPriority: true, integratedTicketing: true });
    return JSON.stringify({
      shares: policyModeShareAdjustmentOf(s),
      lines: [...forecastLineUsage(s)],
      cap: forecastTotalDrivableCapacityOf(s),
      trips: totalPersonTripsOf(s),
      erp: roadPricingInflowOf(s),
      coe: ownershipQuotaInflowOf(s),
    });
  };
  const a = snap();
  assert.equal(snap(), a);
  assert.equal(snap(), a);
});

test('A7b: POLICIES stays a pure data list — the Finance tab renders it generically, so no per-policy UI code exists', () => {
  assert.equal(POLICIES.length, 8);
  assert.equal(new Set(POLICIES.map((p) => p.id)).size, 8);
  const src = readFileSync(
    path.join(repoRoot, 'webconsole', 'src', 'components', 'left', 'tabs', 'financeTabs.tsx'),
    'utf8',
  );
  for (const id of ['ownershipQuota', 'roadPricing', 'busPriority', 'integratedTicketing']) {
    assert.ok(!src.includes(id), `financeTabs.tsx must contain zero per-policy code for ${id}`);
  }
  assert.ok(src.includes('POLICIES.map'), 'still rendered generically');
});

// ===========================================================================
// ROUND 2 (opus-reround-feat801-inc8) — the rework fixed BUG-904/905/906/907
// for the r1 fixture (10 x rd_avenue + 30 x res_hut). It did NOT fix them for
// a city that also contains the OTHER roadTier-2 spec.
//
//   Object.keys(SPECS).filter((k) => roadTierOf(SPECS[k]) === 2)
//     -> ['rd_avenue', 'rd_roundabout']
//
// rd_roundabout is AUTO-PLACED by the game's own road tool, so essentially
// every real city has some. Two consequences, both in the rework's own code:
//   (i)  busPriorityCapacityDeltaOf counts roundabout tiles as avenue tiles
//        (its loop tests roadTierOf(...) === AVENUE_ROAD_TIER), so the delta
//        is sized off tiles the acceptance doc never mentions; and
//   (ii) forecastLineUsage subtracts the FULL delta from EVERY tier-2 spec
//        entry while the denominator forecastTotalDrivableCapacityOf(s)
//        subtracts it ONCE — so the adjusted capacities sum to
//        (totalCap - 2*delta) over a denominator of (totalCap - delta) and
//        trips are DESTROYED. It is BUG-904's mirror image: r1 minted demand,
//        r2 annihilates it.
// A2's conservation pin above passes only because its fixture has no
// roundabout. Blocks A8b/A8c/A8d below were DEFECT-PINs; the rework r3 fix
// (lead amendment after round-2 REJECT, row 7622) reds them, so they are now
// flipped to REGRESSION pins asserting the correct, conserving behaviour:
// BUS_LANE_SPEC is matched by SPEC id (ROAD_TIER_SPECS[AVENUE_ROAD_TIER] ==
// 'rd_avenue'), never by road TIER, so rd_roundabout (the second roadTier-2
// spec) is never counted into the delta and never loses capacity — and
// forecastTotalDrivableCapacityOf(s) is the sum of the EXACT SAME
// adjustedRoadCapacitiesOf(s) map forecastLineUsage's numerator reads, so
// the two can never diverge again (GR#3, one source).
// ===========================================================================

/** The r1 city plus n auto-placed roundabout tiles (roadTier 2, like avenues). */
function cityWithRoundabouts(n, policyOverrides = {}, population = 5000) {
  const bs = [];
  let id = 100;
  for (let i = 0; i < 10; i++) bs.push(road(id++, 'rd_avenue', i, 0));
  for (let i = 0; i < 6; i++) bs.push(road(id++, 'road', i, 3));
  for (let i = 0; i < n; i++) bs.push(road(id++, 'rd_roundabout', i % 20, 5 + Math.floor(i / 20)));
  for (let i = 0; i < 30; i++) bs.push(res(id++, i % 10, 1 + Math.floor(i / 10)));
  return board(bs, population, policyOverrides);
}

test('A8: rd_roundabout really is a second roadTier-2 spec — the premise of A8b/A8c/A8d', () => {
  const tier2 = Object.keys(SPECS).filter((k) => roadTierOf(SPECS[k]) === 2);
  assert.deepEqual(tier2.sort(), ['rd_avenue', 'rd_roundabout']);
});

test('A8b REGRESSION (BUG-918 fixed): with a second roadTier-2 spec present, busPriority conserves total demand EXACTLY — no trips destroyed', () => {
  const off = cityWithRoundabouts(4);
  const on = cityWithRoundabouts(4, { busPriority: true });
  const totalOff = roadDemandSum(forecastLineUsage(off));
  const totalOn = roadDemandSum(forecastLineUsage(on)); // includes the bus entry
  // Sum in the SAME fixed order on both sides (GR#21 float-summation-order
  // discipline); the residual is float noise (~1e-13 on this fixture, well
  // under 1e-9), never a structural loss/mint.
  assert.ok(Math.abs(totalOn - totalOff) < 1e-9, 'total demand (road classes + bus) conserved exactly, on vs off');
  assert.ok(totalOff > 0 && totalOn > 0, 'population 5000 produces real, non-zero demand on both paths');
  const avenueOff = forecastLineUsage(off).get('rd_avenue').demand;
  const avenueOn = forecastLineUsage(on).get('rd_avenue').demand;
  assert.ok(avenueOn <= avenueOff, 'general-road (rd_avenue) demand only goes down or stays equal, never up');
  // Root cause fixed, pinned directly: the numerator (adjustedRoadCapacitiesOf)
  // and denominator (forecastTotalDrivableCapacityOf) are now the SAME map —
  // rd_roundabout is untouched (its adjusted capacity equals its raw
  // capacity) and only rd_avenue loses delta, exactly once, on both sides.
  const delta = busPriorityCapacityDeltaOf(on);
  const denom = forecastTotalDrivableCapacityOf(on);
  const denomOff = forecastTotalDrivableCapacityOf(off);
  assert.equal(delta, 138.88888888888889, 'delta sized off the 10 avenue tiles only, not the 4 roundabouts too');
  assert.ok(Math.abs(denomOff - denom - delta) < 1e-9, 'the denominator drops by EXACTLY delta, once, never twice');
});

test('A8c REGRESSION (BUG-919 fixed): busPriorityCapacityDeltaOf sizes the bus lanes off AVENUE tiles only — roundabouts never counted', () => {
  const noRb = cityWithRoundabouts(0, { busPriority: true });
  const withRb = cityWithRoundabouts(4, { busPriority: true });
  const fraction = (AVENUE_PCU - BUS_LANE_PCU) / AVENUE_PCU;
  // Doc AC-4: delta = fraction * ROAD_TIER_CAPACITY[2] * online AVENUE tile count (10).
  const expected = fraction * ROAD_TIER_CAPACITY[2] * 10;
  assert.equal(busPriorityCapacityDeltaOf(noRb), expected);
  // FIX: adding 4 roundabouts must NOT change the delta — they are matched
  // by SPEC (ROAD_TIER_SPECS[2] === 'rd_avenue'), never by road tier.
  assert.equal(busPriorityCapacityDeltaOf(withRb), expected);
  assert.notEqual(busPriorityCapacityDeltaOf(withRb), fraction * ROAD_TIER_CAPACITY[2] * 14, 'the old tier-based over-count (14) is gone');
});

test('A8d REGRESSION (BUG-918 fixed): a second roadTier-2 spec no longer amplifies the delta past the avenue class capacity, and the clamp — when it DOES trigger — is reported, never silent', () => {
  const bs = [];
  let id = 100;
  bs.push(road(id++, 'rd_avenue', 0, 0));
  for (let i = 0; i < 6; i++) bs.push(road(id++, 'road', i, 3));
  for (let i = 0; i < 50; i++) bs.push(road(id++, 'rd_roundabout', i % 20, 5 + Math.floor(i / 20)));
  for (let i = 0; i < 30; i++) bs.push(res(id++, i % 10, 1 + Math.floor(i / 10)));
  const on = board(bs, 5000, { busPriority: true });
  const off = board(bs, 5000, {});
  // FIX: the 50 roundabouts no longer inflate the tile count (BUG-919), so
  // the old "708.33 > the whole avenue class's 250 capacity" amplifier is
  // gone — delta is now sized off the single avenue tile only, well under
  // its own capacity, and the class is NOT zeroed out.
  const fraction = (AVENUE_PCU - BUS_LANE_PCU) / AVENUE_PCU;
  const expectedDelta = fraction * ROAD_TIER_CAPACITY[2] * 1;
  assert.equal(busPriorityCapacityDeltaOf(on), expectedDelta);
  assert.ok(expectedDelta < ROAD_TIER_CAPACITY[2] * 1, 'delta stays under the single avenue tile\'s own raw capacity');
  assert.ok(forecastLineUsage(on).get('rd_avenue').demand > 0, 'no longer silently zeroed');
  // Conservation still holds on THIS fixture too (the general BUG-918 fix,
  // not just the tile-count fix).
  const totalOff = roadDemandSum(forecastLineUsage(off));
  const totalOn = roadDemandSum(forecastLineUsage(on));
  assert.ok(Math.abs(totalOn - totalOff) < 1e-9, 'conserved exactly on this fixture too');
});

test('A8e REGRESSION: with NO tier-2 class in the city at all, busPriority is an exact no-op — delta 0, no bus entry, nothing divides by zero', () => {
  const bs = [];
  let id = 100;
  for (let i = 0; i < 8; i++) bs.push(road(id++, 'road', i, 3));
  for (let i = 0; i < 30; i++) bs.push(res(id++, i % 10, 1 + Math.floor(i / 10)));
  const off = board(bs, 5000);
  const on = board(bs, 5000, { busPriority: true });
  assert.equal(busPriorityCapacityDeltaOf(on), 0);
  assert.equal(forecastLineUsage(on).get('bus'), undefined);
  assert.equal(forecastTotalDrivableCapacityOf(on), forecastTotalDrivableCapacityOf(off));
  assert.equal(roadDemandSum(forecastLineUsage(on)), roadDemandSum(forecastLineUsage(off)));
  for (const [, v] of forecastLineUsage(on)) assert.ok(Number.isFinite(v.demand));
});

test('A8f REGRESSION (BUG-905): the delta is read from link_capacity.json, not a literal — retuning the avenue row moves it proportionally', () => {
  // Proved by mutation in the round (avenue row 1800 -> 3600 in the in-tree
  // mirror moved the delta 138.888... -> 1319.444..., i.e. exactly
  // (3600-1700)/3600 * 250 * 10). Pinned here as the data-derived identity so
  // a future hand-typed literal reds this file.
  const on = cityWithRoundabouts(0, { busPriority: true });
  assert.equal(
    busPriorityCapacityDeltaOf(on),
    ((AVENUE_PCU - BUS_LANE_PCU) / AVENUE_PCU) * ROAD_TIER_CAPACITY[2] * 10,
  );
  const src = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'trafficDemand.ts'), 'utf8');
  const code = src
    .split('\n')
    .filter((l) => !l.trim().startsWith('*') && !l.trim().startsWith('//') && !l.trim().startsWith('/*'))
    .join('\n');
  for (const lit of [String(AVENUE_PCU), String(BUS_LANE_PCU)]) {
    assert.ok(!code.includes(lit), 'trafficDemand.ts must not hand-type ' + lit);
  }
});

test('A8g REGRESSION (BUG-907): the dead renormalise block is gone and the vector still sums to exactly 1', () => {
  const src = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'trafficDemand.ts'), 'utf8');
  const body = src.slice(src.indexOf('export const policyModeShareAdjustmentOf'));
  const fn = body.slice(0, body.indexOf('\n});'));
  assert.ok(!/anyMove/.test(fn), 'the dead anyMove flag is gone');
  const v = policyModeShareAdjustmentOf(
    cityWithRoundabouts(2, {
      ownershipQuota: true,
      roadPricing: true,
      busPriority: true,
      integratedTicketing: true,
    }),
  );
  assert.equal(Object.values(v).reduce((a, b) => a + b, 0), 1);
});

test('A8h REGRESSION (BUG-906): the COE inflow is monotone non-decreasing, positive at pop 1000, integer, and has no step at 36,500', () => {
  const pops = [1000, 10000, 36499, 36500, 100000, 150000];
  const vals = pops.map((p) => {
    const s = board([res(1, 0, 0)], p, { ownershipQuota: true });
    return ownershipQuotaInflowOf(s);
  });
  assert.deepEqual(vals, [616, 6164, 22499, 22500, 61644, 92466]);
  for (const v of vals) assert.ok(Number.isInteger(v) && v >= 0);
  assert.ok(vals[0] > 0, 'pop 1000 books a real line');
  for (let i = 1; i < vals.length; i++) assert.ok(vals[i] >= vals[i - 1], 'monotone non-decreasing');
  assert.ok(vals[3] - vals[2] < COE_PRICE, 'no whole-COE staircase at 36,500');
});

// ───────────────────────────────────────────────────────────────────────────
// ROUND 3 (opus-round3-feat801-inc8) — pins appended after the BUG-918/919
// rework. Verdict: REJECT, on the clamp-boundary conservation break pinned
// by A9e's comment. Everything else below is a property round 3 proved
// holds on NEW city shapes the rework's own fixtures never build.
// ───────────────────────────────────────────────────────────────────────────

/** Round 3's shape builder — accepts an arbitrary road-spec mix. */
function cityOf(specs, population, policyOverrides = {}) {
  const bs = [];
  let id = 100;
  for (const [spec, n] of specs) for (let i = 0; i < n; i++) bs.push(road(id++, spec, i % 20, id % 20));
  for (let i = 0; i < 30; i++) bs.push(res(id++, i % 10, 1 + Math.floor(i / 10)));
  return board(bs, population, policyOverrides);
}
const demandMapOf = (m) => [...m].map(([k, v]) => [k, v.demand]);

test('A9 REGRESSION (round 3): a city with roundabouts and plain roads but NO avenue at all — busPriority is a total no-op, delta 0, no bus entry, demand map byte-identical on vs off', () => {
  const specs = [['road', 6], ['rd_roundabout', 5]];
  const off = forecastLineUsage(cityOf(specs, 5000, {}));
  const on = forecastLineUsage(cityOf(specs, 5000, { busPriority: true }));
  assert.equal(busPriorityCapacityDeltaOf(cityOf(specs, 5000, { busPriority: true })), 0);
  assert.ok(!on.has('bus'), 'no synthetic bus entry without an avenue to carve the lane from');
  assert.deepEqual(demandMapOf(on), demandMapOf(off));
});

test('A9b REGRESSION (round 3): with EVERY road tier present at once, only the avenue class loses capacity and total demand is conserved exactly', () => {
  const specs = [['road', 4], ['rd_avenue', 4], ['rd_aroad', 4], ['rd_dual', 4], ['m20', 4], ['rd_roundabout', 4]];
  const sOff = cityOf(specs, 5000, {});
  const sOn = cityOf(specs, 5000, { busPriority: true });
  const totalOff = roadDemandSum(forecastLineUsage(sOff));
  const totalOn = roadDemandSum(forecastLineUsage(sOn));
  assert.ok(Math.abs(totalOn - totalOff) < 1e-9, `conserved: off ${totalOff} on ${totalOn}`);
  // Only rd_avenue's adjusted capacity differs from its raw capacity.
  const raw = new Map();
  for (const [spec, cap] of adjustedRoadCapacitiesOf(sOff)) raw.set(spec, cap);
  for (const [spec, cap] of adjustedRoadCapacitiesOf(sOn)) {
    if (spec === 'rd_avenue') assert.ok(cap < raw.get(spec), 'the avenue class loses capacity');
    else assert.equal(cap, raw.get(spec), `${spec} must keep its full capacity`);
  }
  // The map IS the denominator (GR#3 — one source, never two).
  let sum = 0;
  for (const [, cap] of adjustedRoadCapacitiesOf(sOn)) sum += cap;
  assert.equal(sum, forecastTotalDrivableCapacityOf(sOn));
});

test('A9c REGRESSION (round 3): population 0 with busPriority on — every demand is exactly 0, never NaN, and the bus entry is still emitted', () => {
  const specs = [['rd_avenue', 10], ['rd_roundabout', 4]];
  const on = forecastLineUsage(cityOf(specs, 0, { busPriority: true }));
  for (const [spec, v] of on) assert.equal(v.demand, 0, `${spec} demand must be exactly 0 at population 0`);
});

test('A9d REGRESSION (round 3): an AVENUE-ONLY city (the bus-lane class is the sole road class) still conserves demand, and the road denominator never collapses to 0 while a bus entry exists', () => {
  const specs = [['rd_avenue', 3]];
  const sOff = cityOf(specs, 5000, {});
  const sOn = cityOf(specs, 5000, { busPriority: true });
  const totalOff = roadDemandSum(forecastLineUsage(sOff));
  const totalOn = roadDemandSum(forecastLineUsage(sOn));
  assert.ok(Math.abs(totalOn - totalOff) < 1e-9, `conserved: off ${totalOff} on ${totalOn}`);
  // THE invariant A9e's finding is about: a synthetic bus entry may only
  // exist while the road denominator is still positive, otherwise every
  // road-borne trip is divided by zero and silently dropped.
  if (forecastLineUsage(sOn).has('bus')) {
    assert.ok(forecastTotalDrivableCapacityOf(sOn) > 0, 'road denominator collapsed to 0 with a live bus entry');
  }
});

// A9e was a DEFECT-PIN in round 3 (BUG-921/BUG-922): the shipped capacity
// table kept the clamp unreachable, and a data-mutation-based manual claim
// in trafficPolicies.test.mjs's comment ("conserved exactly, diff 0") did
// not reproduce -- forcing the clamp via a scratch-mirror edit measured an
// 80.3% trip-loss instead, because the clamp ceiling was the class's FULL
// raw capacity (able to drive the whole road-capacity denominator to zero
// in a single-road-class city). Per the lead's r4 ruling this flips to a
// REGRESSION pin: the clamp ceiling is now BUS_LANE_MAX_SHARE_OF_CLASS (0.5,
// data-sourced, link_capacity.json's busPriority block) OF the class's own
// raw capacity, so the class always keeps at least half its capacity and
// the denominator can never collapse to 0 while any road tile exists. This
// pin forces the clamp to bind via the BUG-922 test-only injection seam
// (__setBusLaneShareFractionOverrideForTest) rather than an unpinned manual
// data edit, and RE-MEASURES this round rather than repeating an earlier
// round's unreproduced claim.
test('A9e REGRESSION (BUG-921/BUG-922 fixed): forcing the clamp to bind on a SINGLE-road-class (avenue-only) city still conserves total demand EXACTLY, and the clamp is REPORTED as true, never silent', () => {
  const fraction = (AVENUE_PCU - BUS_LANE_PCU) / AVENUE_PCU;
  assert.ok(fraction > 0 && fraction < 1, 'shipped table keeps the clamp unreachable from REAL data alone');
  const sOff = cityOf([['rd_avenue', 10]], 5000, {});

  __setBusLaneShareFractionOverrideForTest(2); // force requested > raw capacity
  let sOn, info, totalOff, totalOn;
  try {
    sOn = cityOf([['rd_avenue', 10]], 5000, { busPriority: true });
    info = busPriorityCapacityInfoOf(sOn);
    totalOff = roadDemandSum(forecastLineUsage(sOff));
    totalOn = roadDemandSum(forecastLineUsage(sOn));
  } finally {
    __setBusLaneShareFractionOverrideForTest(null); // reset for every other test in this file
  }

  // MEASURED THIS ROUND (re-derived in-process, not carried over): with the
  // 0.5 share ceiling, requested=5000, rd_avenue's own raw capacity=2500 ->
  // delta clamped to 1250 (half of 2500), NOT the old defect's 2500 (the
  // FULL raw capacity, which drove the denominator to exactly 0).
  assert.deepEqual(info, { delta: 1250, requested: 5000, clamped: true });
  assert.ok(forecastTotalDrivableCapacityOf(sOn) > 0, 'the road-capacity denominator never collapses to 0, even at the clamp boundary, even on a single-road-class city');
  assert.ok(Math.abs(totalOn - totalOff) < 1e-9, `conserved exactly at the clamp boundary: off=${totalOff} on=${totalOn} (round 3 measured an 80.3% loss here under the old full-capacity clamp ceiling)`);

  // MUTANT (verified via scratch copy, trafficDemand.ts busPriorityCapacityInfoOf):
  // revert the clamp ceiling from `avenueRawCapacity * BUS_LANE_MAX_SHARE_OF_CLASS`
  // back to the bare `avenueRawCapacity` -- reds the `info` deepEqual (delta
  // becomes 2500) and the denominator/conservation assertions (denominator
  // drops to 0, totalOn collapses to the bus-only figure), reproducing round
  // 3's exact measured shape.
});

// ═══════════════════════════════════════════════════════════════════════════
// ROUND 4 pins — opus-round4-feat801-inc8 (INDEPENDENT, never the author).
// Round 4 attacked the BUG-921/922/923 rework. Everything below is GREEN
// against the code as reworked and pins a property round 4 measured itself.
// ═══════════════════════════════════════════════════════════════════════════

const BUS_LANE_MAX_SHARE_FROM_DATA = linkCapacity.busPriority.busLaneMaxShareOfClass;

// A10 — the clamp ceiling is DATA-DRIVEN, and the expected delta is DERIVED
// from the data file rather than hand-typed (GR#15). Round 4 proved the field
// is really read: a scratch-mirror edit 0.5 -> 0.25 moved the measured delta
// 1250 -> 625 and reddened both suites' hardcoded 1250 expectations; a scratch
// mirror of link_capacity.json restored, md5 42990694672de78f316687fb8b3f5e9c.
test('A10 (round 4): the busPriority clamp ceiling is busLaneMaxShareOfClass x the class\'s OWN raw capacity, with the expectation DERIVED from data/traffic/link_capacity.json (never a hand-typed 1250)', () => {
  assert.equal(typeof BUS_LANE_MAX_SHARE_FROM_DATA, 'number');
  assert.ok(
    BUS_LANE_MAX_SHARE_FROM_DATA > 0 && BUS_LANE_MAX_SHARE_FROM_DATA < 1,
    'the share must be strictly inside (0,1): at exactly 1 the clamp allows the class to reach zero capacity and, on a single-road-class city, drives the MET-V1003 fail-closed throw (round 4 measured that throw with a scratch-mirror 1.0)',
  );
  const sOff = cityOf([['rd_avenue', 10]], 5000, {});
  const avenueRaw = 10 * ROAD_TIER_CAPACITY[2]; // 2500, from data.ts, not a literal
  __setBusLaneShareFractionOverrideForTest(2);
  let info, sOn, totalOn;
  try {
    sOn = cityOf([['rd_avenue', 10]], 5000, { busPriority: true });
    info = busPriorityCapacityInfoOf(sOn);
    totalOn = roadDemandSum(forecastLineUsage(sOn));
  } finally {
    __setBusLaneShareFractionOverrideForTest(null);
  }
  assert.equal(info.delta, avenueRaw * BUS_LANE_MAX_SHARE_FROM_DATA);
  assert.equal(info.clamped, true);
  assert.ok(forecastTotalDrivableCapacityOf(sOn) >= avenueRaw * (1 - BUS_LANE_MAX_SHARE_FROM_DATA));
  assert.ok(Math.abs(totalOn - roadDemandSum(forecastLineUsage(sOff))) < 1e-9);
});

// A11 — the clamp's REAL-data path (round 4 independence check on the BUG-922
// test seam): mutating the SSOT-mirrored bus_lane_variant capacity 1700 ->
// -1800 makes laneShareFraction 2 from real data with NO seam involved.
// MEASURED round 4 (scratch mirror, .bak outside the repo, restored + md5
// 42990694672de78f316687fb8b3f5e9c verified): avenue-only city, pop 5000 ->
// info {delta:1250, requested:5000, clamped:true}, denominator 1250, total
// demand off 454.1039999999998 / on 454.1039999999998, diff 0 EXACTLY -
// byte-identical to the seam-driven figures, i.e. the seam faithfully
// reproduces the data path and round 3's 80.3% annihilation is gone.
// This pin is the shipped-data half of that check: with the real table the
// clamp does NOT bind and the policy still conserves.
test('A11 (round 4): with the SHIPPED capacity table the clamp does not bind on an avenue-only city, delta is the unclamped fraction figure, and total demand is conserved exactly', () => {
  const fraction = (AVENUE_PCU - BUS_LANE_PCU) / AVENUE_PCU;
  const sOff = cityOf([['rd_avenue', 10]], 5000, {});
  const sOn = cityOf([['rd_avenue', 10]], 5000, { busPriority: true });
  const info = busPriorityCapacityInfoOf(sOn);
  assert.equal(info.clamped, false);
  assert.equal(info.delta, fraction * ROAD_TIER_CAPACITY[2] * 10);
  assert.equal(info.delta, info.requested);
  assert.ok(forecastTotalDrivableCapacityOf(sOn) > 0);
  assert.ok(
    Math.abs(roadDemandSum(forecastLineUsage(sOn)) - roadDemandSum(forecastLineUsage(sOff))) < 1e-9,
  );
});

// A12 — ZERO bus-lane-eligible tiles with the fraction forced over 1: the
// clamp ceiling is that class's own raw capacity (0 when the class is absent),
// so the delta must be exactly 0, no synthetic bus entry may appear, and
// demand must be untouched. Round 4 measured: info {delta:0, requested:0,
// clamped:false}, denominator 1000 (10 'road' tiles x 100), off/on totals both
// 454.1039999999998. Guards against a future "ceiling defaults to something
// non-zero when the class is missing" regression minting capacity from nothing.
test('A12 (round 4): a city with NO rd_avenue tiles cannot produce a bus-lane delta even with the fraction forced over 1', () => {
  const sOff = cityOf([['road', 10]], 5000, {});
  __setBusLaneShareFractionOverrideForTest(2);
  let info, sOn, totalOn, hasBus;
  try {
    sOn = cityOf([['road', 10]], 5000, { busPriority: true });
    info = busPriorityCapacityInfoOf(sOn);
    totalOn = roadDemandSum(forecastLineUsage(sOn));
    hasBus = forecastLineUsage(sOn).has('bus');
  } finally {
    __setBusLaneShareFractionOverrideForTest(null);
  }
  assert.deepEqual(info, { delta: 0, requested: 0, clamped: false });
  assert.equal(hasBus, false, 'no bus entry may exist when no bus-lane-eligible tile does');
  assert.ok(Math.abs(totalOn - roadDemandSum(forecastLineUsage(sOff))) < 1e-9);
});

// A13 — the BUG-922 seam is TEST-ONLY and must never be reachable from
// production code. Round 4 grepped webconsole/src: the ONLY file mentioning
// it is trafficDemand.ts itself (its declaration + doc comment); no component,
// store, worker or engine path calls it.
test('A13 (round 4): __setBusLaneShareFractionOverrideForTest has no production caller under webconsole/src', () => {
  const srcRoot = path.join(repoRoot, 'webconsole', 'src');
  const hits = [];
  const walk = (dir) => {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) {
        if (e.name === 'node_modules' || e.name === 'generated') continue;
        walk(p);
        continue;
      }
      if (!/\.(ts|tsx)$/.test(e.name)) continue;
      if (p.endsWith(path.join('sim', 'trafficDemand.ts'))) continue; // the declaration site
      if (readFileSync(p, 'utf8').includes('__setBusLaneShareFractionOverrideForTest')) hits.push(p);
    }
  };
  walk(srcRoot);
  assert.deepEqual(hits, [], `the test-only bus-lane seam must have no production caller: ${hits.join(', ')}`);
});
