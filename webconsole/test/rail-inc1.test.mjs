// rail-inc1.test.mjs — FEAT-1972079902 inc1: line capacity + commuter-flow usage
// + saturation. Display/metrics ONLY (trains = inc2, auto-branch router = inc3).
//
// Run with `npm test` (node --test); node's type-stripping imports the real
// TypeScript modules, so these assertions exercise the exact shipped formulas.
//
// Determinism is the crux (GR#21): lineUsageOf is a PURE function of state — no
// Date.now / Math.random, strict (x,y) ordering — so identical states produce
// byte-identical usage, and it is never wired into the tick (inc1 is display-only,
// no economic effect). Every assertion below pins a real numeric value (computed
// by hand) so the test can actually FAIL.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  lineCapacityOf,
  lineUsageOf,
  LINE_CAPACITY,
  LINE_COMMUTER_K,
  LINE_COMMUTER_WEIGHT_CAP,
  ROAD_TIER_CAPACITY,
  stationLinks,
} from '../src/sim/data.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

// Replicate the ECONOMY's "Commuter Revenue" term (engine.computeFlows): a SINGLE
// combined station weight, capped once — round(pop × 0.08 × min(Σ weight, 6)).
// The rail panel's rail+hs1 buckets MUST sum to exactly this (bug: per-bucket caps
// overstated it once combined weight passed the cap).
function economyCommuterTerm(s) {
  const links = stationLinks(s);
  let weight = 0;
  for (const b of s.buildings) {
    if (!links.connectedIds.has(b.id)) continue;
    const sp = SPECS[b.spec];
    if (sp?.kind !== 'station') continue;
    weight += sp.id === 'station_ashford' ? 3 : 1;
  }
  return weight > 0
    ? Math.round(s.population * LINE_COMMUTER_K * Math.min(weight, LINE_COMMUTER_WEIGHT_CAP))
    : 0;
}
const railBucketSum = (s) =>
  lineUsageOf(s)
    .filter((l) => l.kind === 'rail')
    .reduce((a, l) => a + l.usage, 0);

// A controlled board: bare (no starter city), explicit building list + population.
function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}
const lineFor = (s, spec) => {
  const l = lineUsageOf(s).find((x) => x.spec === spec);
  assert.ok(l, `no line-usage entry for "${spec}"`);
  return l;
};

// ===================== 1. capacity map =====================

test('lineCapacityOf: rail/hs1 from LINE_CAPACITY, road/m20 reuse ROAD_TIER_CAPACITY, else 0', () => {
  assert.equal(lineCapacityOf(SPECS.rail), 1200, 'rail per-tile capacity');
  assert.equal(lineCapacityOf(SPECS.hs1), 6000, 'hs1 per-tile capacity');
  assert.equal(lineCapacityOf(SPECS.rail), LINE_CAPACITY.rail, 'rail sourced from LINE_CAPACITY');
  assert.equal(lineCapacityOf(SPECS.hs1), LINE_CAPACITY.hs1, 'hs1 sourced from LINE_CAPACITY');
  // road/m20 are REUSED from ROAD_TIER_CAPACITY (not redefined).
  assert.equal(lineCapacityOf(SPECS.road), ROAD_TIER_CAPACITY[1], 'road reuses tier-1 capacity (100)');
  assert.equal(lineCapacityOf(SPECS.road), 100, 'road capacity is 100');
  assert.equal(lineCapacityOf(SPECS.m20), ROAD_TIER_CAPACITY[5], 'm20 reuses tier-5 capacity (2500)');
  assert.equal(lineCapacityOf(SPECS.m20), 2500, 'm20 capacity is 2500');
  // non-line specs return 0.
  assert.equal(lineCapacityOf(SPECS.res_hut), 0, 'a house is not a line');
  assert.equal(lineCapacityOf(SPECS.park), 0, 'a park is not a line');
  assert.equal(lineCapacityOf(SPECS.station_sanderling), 0, 'a station building is not a line');
  assert.equal(lineCapacityOf(undefined), 0, 'undefined spec → 0');
});

// ===================== 2. usage math (hand-computed) =====================

test('lineUsageOf: rail commuter flow = population × K × min(weight, cap)', () => {
  // pop 1000; one connected Sanderling (weight 1, ×1); 3 rail tiles.
  // road tile at (11,10) makes the station "connected" (adjacent to a road).
  const s = board(
    [
      { id: 1, spec: 'station_sanderling', x: 10, y: 10, builtTick: 0 },
      { id: 2, spec: 'road', x: 11, y: 10, builtTick: 0 },
      { id: 3, spec: 'rail', x: 10, y: 11, builtTick: 0 },
      { id: 4, spec: 'rail', x: 10, y: 12, builtTick: 0 },
      { id: 5, spec: 'rail', x: 10, y: 13, builtTick: 0 },
    ],
    1000
  );
  // Hand computation: railWeight = 1, hsWeight = 0.
  const expectedUsage = Math.round(1000 * LINE_COMMUTER_K * Math.min(1, LINE_COMMUTER_WEIGHT_CAP));
  assert.equal(expectedUsage, 80, 'sanity: 1000 × 0.08 × 1 = 80');
  const rail = lineFor(s, 'rail');
  assert.equal(rail.tiles, 3, '3 rail tiles');
  assert.equal(rail.capacity, 3600, '3 tiles × 1200/tile = 3600');
  assert.equal(rail.usage, 80, 'commuter flow carried = 80');
  assert.equal(rail.overCapacity, false, '80 << 3600 → headroom, not over capacity');
  assert.equal(rail.headroom, 3520, 'headroom = 3600 − 80');
});

test('lineUsageOf: Ashford (HS1 gateway, ×3) feeds hs1 not rail', () => {
  const s = board(
    [
      { id: 1, spec: 'station_ashford', x: 10, y: 10, builtTick: 0 }, // 4×2
      { id: 2, spec: 'road', x: 9, y: 10, builtTick: 0 }, // adjacent → connected
      { id: 3, spec: 'hs1', x: 10, y: 13, builtTick: 0 },
      { id: 4, spec: 'hs1', x: 11, y: 13, builtTick: 0 },
    ],
    2000
  );
  // hsWeight = 3, railWeight = 0. hs1 usage = round(2000 × 0.08 × 3) = 480.
  const hs1 = lineFor(s, 'hs1');
  assert.equal(hs1.usage, 480, '2000 × 0.08 × 3 = 480');
  assert.equal(hs1.capacity, 12000, '2 tiles × 6000/tile');
  // rail has no tiles here, so no rail entry at all.
  assert.equal(lineUsageOf(s).some((l) => l.spec === 'rail'), false, 'no rail line built → no rail entry');
});

test('lineUsageOf: road/m20 traffic demand shared by capacity (road inc2 idiom)', () => {
  // pop 500 → activity 1.0. One res_block (60 residents) feeds the traffic total.
  // 2 road tiles (cap 100 each = 200) + 1 m20 tile (cap 2500). totalDrivableCap 2700.
  const s = board(
    [
      { id: 1, spec: 'res_block', x: 5, y: 5, builtTick: 0 }, // 2×2, 60 residents
      { id: 2, spec: 'road', x: 5, y: 8, builtTick: 0 },
      { id: 3, spec: 'road', x: 6, y: 8, builtTick: 0 },
      { id: 4, spec: 'm20', x: 10, y: 10, builtTick: 0 },
    ],
    500
  );
  // totalFeeder = 60 (only the res_block carries residents); totalTraffic = 60 × 1.
  // road usage = round(60 × 200/2700) = 4; m20 usage = round(60 × 2500/2700) = 56.
  const road = lineFor(s, 'road');
  const m20 = lineFor(s, 'm20');
  assert.equal(road.usage, 4, 'round(60 × 200/2700) = 4');
  assert.equal(m20.usage, 56, 'round(60 × 2500/2700) = 56');
  assert.equal(road.capacity, 200, '2 road tiles × 100');
  assert.equal(m20.capacity, 2500, '1 m20 tile × 2500');
});

// ============ 2b. economy parity — rail+hs1 buckets == the economy term ============
// REGRESSION GUARD (independent-round finding): capping each bucket separately
// overstated flow once combined weight > 6. This asserts EXACT parity with the
// single-cap economy term. RED against the old per-bucket code, GREEN after the fix
// (proven at build time via a scratch cp/mv revert to per-bucket — NEVER git).

test('economy parity: rail+hs1 usage sums EXACTLY to the combined Commuter Revenue term (Ashford + 4 ordinary, w=7)', () => {
  // pop 1000; 1 Ashford (w3) + 4 Sanderling (w1 each), each road-connected.
  // Combined weight 7 → capped to 6. Economy credits round(1000×0.08×6) = 480.
  // Old per-bucket code: hs1 round(1000×0.08×min(3,6))=240 + rail round(1000×0.08×min(4,6))=320
  //   = 560 ≠ 480 (RED). Fixed apportioned code: hs1 + rail == 480 (GREEN).
  const s = board(
    [
      { id: 1, spec: 'station_ashford', x: 10, y: 10, builtTick: 0 },
      { id: 2, spec: 'road', x: 9, y: 10, builtTick: 0 },
      { id: 3, spec: 'station_sanderling', x: 20, y: 20, builtTick: 0 },
      { id: 4, spec: 'road', x: 21, y: 20, builtTick: 0 },
      { id: 5, spec: 'station_sanderling', x: 24, y: 20, builtTick: 0 },
      { id: 6, spec: 'road', x: 25, y: 20, builtTick: 0 },
      { id: 7, spec: 'station_sanderling', x: 28, y: 20, builtTick: 0 },
      { id: 8, spec: 'road', x: 29, y: 20, builtTick: 0 },
      { id: 9, spec: 'station_sanderling', x: 32, y: 20, builtTick: 0 },
      { id: 10, spec: 'road', x: 33, y: 20, builtTick: 0 },
      { id: 11, spec: 'rail', x: 20, y: 22, builtTick: 0 },
      { id: 12, spec: 'hs1', x: 10, y: 13, builtTick: 0 },
    ],
    1000
  );
  assert.equal(economyCommuterTerm(s), 480, 'sanity: economy term = round(1000×0.08×min(7,6)) = 480');
  assert.equal(railBucketSum(s), 480, 'rail+hs1 buckets MUST sum to the economy term (not 560)');
  assert.equal(railBucketSum(s), economyCommuterTerm(s), 'exact parity with the economy');
});

test('economy parity: holds for a different mix (2 Ashford + 1 ordinary, w=7) and an under-cap mix', () => {
  // 2 Ashford (w3 each = 6) + 1 Sanderling (w1) = 7 → capped to 6.
  const overCap = board(
    [
      { id: 1, spec: 'station_ashford', x: 10, y: 10, builtTick: 0 },
      { id: 2, spec: 'road', x: 9, y: 10, builtTick: 0 },
      { id: 3, spec: 'station_ashford', x: 30, y: 10, builtTick: 0 },
      { id: 4, spec: 'road', x: 29, y: 10, builtTick: 0 },
      { id: 5, spec: 'station_sanderling', x: 50, y: 10, builtTick: 0 },
      { id: 6, spec: 'road', x: 51, y: 10, builtTick: 0 },
      { id: 7, spec: 'hs1', x: 10, y: 13, builtTick: 0 },
      { id: 8, spec: 'rail', x: 50, y: 12, builtTick: 0 },
    ],
    5000
  );
  assert.equal(railBucketSum(overCap), economyCommuterTerm(overCap), 'exact parity, over-cap mix');
  assert.ok(economyCommuterTerm(overCap) > 0, 'precondition: nonzero economy term');

  // Under-cap mix (combined weight 4 ≤ 6): parity must ALSO hold (fix is general).
  const underCap = board(
    [
      { id: 1, spec: 'station_ashford', x: 10, y: 10, builtTick: 0 }, // w3
      { id: 2, spec: 'road', x: 9, y: 10, builtTick: 0 },
      { id: 3, spec: 'station_sanderling', x: 40, y: 40, builtTick: 0 }, // w1
      { id: 4, spec: 'road', x: 41, y: 40, builtTick: 0 },
      { id: 5, spec: 'hs1', x: 10, y: 13, builtTick: 0 },
      { id: 6, spec: 'rail', x: 40, y: 41, builtTick: 0 },
    ],
    3000
  );
  assert.equal(railBucketSum(underCap), economyCommuterTerm(underCap), 'exact parity, under-cap mix');
});

// ===================== 3. saturation ratio + boundary =====================

test('lineUsageOf: saturation clamps to [0,1] and the surplus/shortfall boundary is headroom<0', () => {
  // Over-capacity rail: huge population, 1 rail tile, one connected station (×1).
  // usage = round(1_000_000 × 0.08 × 1) = 80,000 vs capacity 1200 → clamps to 1.
  const over = board(
    [
      { id: 1, spec: 'station_sanderling', x: 10, y: 10, builtTick: 0 },
      { id: 2, spec: 'road', x: 11, y: 10, builtTick: 0 },
      { id: 3, spec: 'rail', x: 10, y: 11, builtTick: 0 },
    ],
    1_000_000
  );
  const rail = lineFor(over, 'rail');
  assert.equal(rail.usage, 80000, 'raw usage exceeds capacity');
  assert.ok(rail.usage > rail.capacity, 'precondition: usage > capacity');
  assert.equal(rail.saturation, 1, 'saturation clamps at 1 (never > 1)');
  assert.equal(rail.overCapacity, true, 'usage > capacity ⇒ overCapacity true');
  assert.ok(rail.headroom < 0, 'headroom is negative when over capacity');

  // Within-capacity rail (scenario 2): saturation in (0,1), NOT over capacity.
  const under = board(
    [
      { id: 1, spec: 'station_sanderling', x: 10, y: 10, builtTick: 0 },
      { id: 2, spec: 'road', x: 11, y: 10, builtTick: 0 },
      { id: 3, spec: 'rail', x: 10, y: 11, builtTick: 0 },
    ],
    1000
  );
  const railU = lineFor(under, 'rail');
  assert.ok(railU.saturation > 0 && railU.saturation < 1, 'partial saturation is strictly inside (0,1)');
  assert.equal(railU.overCapacity, false, 'usage < capacity ⇒ overCapacity false');
  assert.ok(railU.headroom >= 0, 'headroom non-negative within capacity');
  // Boundary property: overCapacity iff usage > capacity, for every line.
  for (const l of [...lineUsageOf(over), ...lineUsageOf(under)]) {
    assert.equal(l.overCapacity, l.usage > l.capacity, `${l.spec}: overCapacity ⇔ usage>capacity`);
  }
});

// ===================== 4. determinism =====================

test('lineUsageOf: identical states → byte-identical usage (independent computations)', () => {
  const mk = () =>
    board(
      [
        { id: 1, spec: 'station_ashford', x: 20, y: 20, builtTick: 0 },
        { id: 2, spec: 'road', x: 19, y: 20, builtTick: 0 },
        { id: 3, spec: 'station_sanderling', x: 40, y: 40, builtTick: 0 },
        { id: 4, spec: 'road', x: 41, y: 40, builtTick: 0 },
        { id: 5, spec: 'rail', x: 40, y: 41, builtTick: 0 },
        { id: 6, spec: 'hs1', x: 20, y: 23, builtTick: 0 },
        { id: 7, spec: 'm20', x: 60, y: 60, builtTick: 0 },
        { id: 8, spec: 'res_block', x: 5, y: 5, builtTick: 0 },
      ],
      3000
    );
  const a = JSON.stringify(lineUsageOf(mk()));
  const b = JSON.stringify(lineUsageOf(mk()));
  assert.equal(a, b, 'two independent states yield byte-identical usage JSON');
  // And the same state computed twice matches too.
  const s = mk();
  assert.equal(JSON.stringify(lineUsageOf(s)), JSON.stringify(lineUsageOf(s)), 'repeatable on one state');
});

// ===================== 5. no economic change (display-only) =====================

test('lineUsageOf: pure read-out — never mutates state, no funds/flow impact vs baseline', () => {
  // (a) calling lineUsageOf must not mutate the state object at all.
  const s0 = initialState();
  const snap = JSON.stringify(s0);
  lineUsageOf(s0);
  lineUsageOf(s0);
  assert.equal(JSON.stringify(s0), snap, 'lineUsageOf did not mutate the sim state');

  // (b) a tick with the rail-usage code present produces the SAME funds/flows as a
  // baseline tick where lineUsageOf is never called (inc1 is display-only).
  const baseline = reducer(initialState(), { type: 'tick' });
  const withReadout = (() => {
    const s = initialState();
    lineUsageOf(s); // exercise the read-out before ticking
    return reducer(s, { type: 'tick' });
  })();
  assert.equal(withReadout.funds, baseline.funds, 'funds unchanged by the rail read-out');
  assert.deepEqual(withReadout.lastFlows, baseline.lastFlows, 'flows unchanged by the rail read-out');
  assert.equal(withReadout.population, baseline.population, 'population unchanged');

  // (c) conservation still holds after the tick.
  const report = runConsistencyChecks(withReadout);
  const conservation = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.ok(conservation, 'conservation check exists');
  assert.equal(conservation.ok, true, 'conservation holds with the rail read-out present');
});
