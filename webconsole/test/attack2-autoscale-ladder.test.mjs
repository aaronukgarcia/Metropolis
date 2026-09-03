// attack2-autoscale-ladder.test.mjs — INDEPENDENT DESTRUCTIVE RE-ROUND (GR#23)
// against FEAT-2326609740 + BUG-590 r2 (auto-scale ladder A+B, up-then-out).
// Attacker is NOT the author. Fresh adversarial probes re-attacking the five
// r1 blocking findings (F1..F5) plus determinism / conservation / perf.
//
// Naming: R2-F<n> = re-attack of r1 finding <n>. NEW-<n> = a finding this
// round raises for the first time.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  heightCapOf,
  capacityAtTier,
  computeRoadConnectivity,
  occupiedSet,
  isRoadAdjacent,
  isOnline,
  MAP_W,
  MAP_H,
} from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  evaluateBuildingMonitors,
  BUILDING_AUTO_SCALE_COST_FRACTION,
} from '../src/sim/engine.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 100_000_000_000,
    tick: 0,
    ...over,
  };
}

function roadRow(y, maxX, minX = 0) {
  const roads = [];
  for (let x = minX; x <= maxX; x++) roads.push({ id: 700000 + y * 1000 + x, spec: 'road', x, y, builtTick: -1000 });
  return roads;
}

function withConnectivity(s) {
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

function tilesOf(b) {
  const sp = SPECS[b.spec];
  const w = b.footprintW ?? sp.w;
  const h = b.footprintH ?? sp.h;
  const out = [];
  for (let dx = 0; dx < w; dx++) for (let dy = 0; dy < h; dy++) out.push(`${b.x + dx},${b.y + dy}`);
  return out;
}

/** Assert no two buildings share a tile. Returns the offending key on failure. */
function assertNoOverlap(buildings, msg) {
  const owner = new Map();
  for (const b of buildings) {
    if (!SPECS[b.spec]) continue;
    for (const k of tilesOf(b)) {
      if (owner.has(k)) {
        assert.fail(
          `${msg}: tile ${k} claimed by BOTH building ${owner.get(k)} and ${b.id}`
        );
      }
      owner.set(k, b.id);
    }
  }
}

function mon(id, type = 'residents') {
  return { buildingId: id, until: 10_000_000, type };
}

// ===========================================================================
// R2-F1 — same-pass footprint overlap
// ===========================================================================

test('R2-F1a: monitors whose grown footprints collide on the SAME free tile in ONE pass never double-claim it', () => {
  // Growth is always +x (width-first) then +y from the origin, so a tile is
  // contended by the building to its LEFT and the building ABOVE it.
  // Contended tile T1=(11,20): A(10,20) grows RIGHT into it; B(11,19) is
  // right-blocked by a static wall so it falls back to DOWN into it.
  // Contended tile T2=(21,20): same shape with C/D.
  // Road row at y=18 plus road columns at x=9 and x=19 keep everyone online.
  const roads = [
    ...roadRow(18, 40),
    { id: 800001, spec: 'road', x: 9, y: 19, builtTick: -1000 },
    { id: 800002, spec: 'road', x: 9, y: 20, builtTick: -1000 },
    { id: 800003, spec: 'road', x: 19, y: 19, builtTick: -1000 },
    { id: 800004, spec: 'road', x: 19, y: 20, builtTick: -1000 },
  ];
  const bs = [
    { id: 1, spec: 'res_hut', x: 10, y: 20, builtTick: -1000, capacityTier: 1, heightStoreys: 3 },
    { id: 2, spec: 'res_hut', x: 11, y: 19, builtTick: -1000, capacityTier: 1, heightStoreys: 3 },
    { id: 3, spec: 'res_hut', x: 20, y: 20, builtTick: -1000, capacityTier: 1, heightStoreys: 3 },
    { id: 4, spec: 'res_hut', x: 21, y: 19, builtTick: -1000, capacityTier: 1, heightStoreys: 3 },
    // static walls forcing B and D right-blocked
    { id: 91, spec: 'res_hut', x: 12, y: 19, builtTick: -1000 },
    { id: 92, spec: 'res_hut', x: 22, y: 19, builtTick: -1000 },
  ];
  const s = withConnectivity(
    mk({ buildings: [...roads, ...bs], population: 5_000_000, tick: 120, buildingMonitors: [mon(1), mon(2), mon(3), mon(4)] })
  );
  const r = evaluateBuildingMonitors(s, 120);
  assert.ok(r.upgraded >= 2, `probe must actually scale (upgraded=${r.upgraded})`);
  assertNoOverlap(r.buildings, 'R2-F1a same-pass contended-tile growth');
});

test('R2-F1c: a sparse 800-building city ticked 300 ticks never self-overlaps', () => {
  // The r1 round proved the overlap was not contrived by ticking a plain
  // sparse city. Same probe, re-run against the r2 fix.
  const buildings = [];
  const monitors = [];
  let id = 1;
  for (let y = 0; y < 120; y += 3) for (let x = 0; x < 60; x++) buildings.push({ id: id++, spec: 'road', x, y, builtTick: -1000 });
  let n = 0;
  for (let y = 1; y < 120 && n < 800; y += 3) {
    for (let x = 0; x < 60 && n < 800; x += 1) {
      const bid = id++;
      buildings.push({ id: bid, spec: 'res_hut', x, y, builtTick: -1000 });
      monitors.push(mon(bid));
      n++;
    }
  }
  assert.equal(n, 800, 'fixture size');
  let s = withConnectivity(mk({ buildings, population: 5_000_000, tick: 0, nextId: id, buildingMonitors: monitors }));
  for (let i = 0; i < 300; i++) {
    s = reducer(s, { type: 'tick' });
    assertNoOverlap(s.buildings, `R2-F1c sparse-city tick ${s.tick}`);
  }
  const grown = s.buildings.filter((b) => (b.footprintW ?? 1) > 1 || (b.footprintH ?? 1) > 1).length;
  assert.ok(grown > 0, `the run must actually have grown OUT (grown=${grown})`);
});

test('R2-F1b: growth never lands on a building the PLAYER placed earlier in the same tick', () => {
  // The player places a building, then the tick that runs the monthly monitor
  // pass must see it. Grow-target tile is taken by the fresh placement.
  const roads = roadRow(18, 60);
  const grower = { id: 1, spec: 'res_hut', x: 20, y: 20, builtTick: -1000, capacityTier: 1, heightStoreys: 3 };
  let s = withConnectivity(
    mk({
      buildings: [...roads, grower],
      population: 5_000_000,
      tick: 0,
      nextId: 500,
      buildingMonitors: [mon(1)],
      tool: { mode: 'build', spec: 'res_hut' },
    })
  );
  // res_hut heightCap is 3 -> UP is permanently blocked, so every step is OUT.
  s = reducer(s, { type: 'place', spec: 'res_hut', x: 21, y: 20 });
  const placed = s.buildings.find((b) => b.x === 21 && b.y === 20 && b.spec === 'res_hut');
  assert.ok(placed, 'sanity: the player placement landed');
  // now run the monitor pass on THAT state
  const r = evaluateBuildingMonitors(withConnectivity(s), 30);
  assertNoOverlap(r.buildings, 'R2-F1b growth over a same-tick player placement');
  const after = r.buildings.find((b) => b.id === 1);
  assert.notEqual(
    `${after.footprintW ?? 1}x${after.footprintH ?? 1}`,
    '2x1',
    'grew RIGHT onto the tile the player just occupied'
  );
});

// ===========================================================================
// R2-F2 — relocate must validate the GROWN footprint
// ===========================================================================

test('R2-F2a: relocating a grown 2x1 into a hole where only its BASE 1x1 fits is refused', () => {
  const roads = roadRow(18, 60);
  const grown = { id: 1, spec: 'res_hut', x: 20, y: 20, builtTick: -1000, capacityTier: 2, footprintW: 2, footprintH: 1 };
  // A 1-wide gap at (30,20): (31,20) is occupied.
  const neighbour = { id: 2, spec: 'res_hut', x: 31, y: 20, builtTick: -1000 };
  let s = withConnectivity(mk({ buildings: [...roads, grown, neighbour], movingId: 1, funds: 1e9 }));
  const after = reducer(s, { type: 'relocate', x: 30, y: 20 });
  const moved = after.buildings.find((b) => b.id === 1);
  assert.equal(moved.x, 20, 'the grown building moved into a hole too small for its real footprint');
  assertNoOverlap(after.buildings, 'R2-F2a relocate');
});

test('R2-F2b: relocating a grown 3x1 to the right map edge is refused (off-map)', () => {
  const roads = roadRow(18, 60);
  const grown = { id: 1, spec: 'res_hut', x: 20, y: 20, builtTick: -1000, capacityTier: 4, footprintW: 3, footprintH: 1 };
  let s = withConnectivity(mk({ buildings: [...roads, grown], movingId: 1, funds: 1e9 }));
  const after = reducer(s, { type: 'relocate', x: MAP_W - 1, y: 20 });
  const moved = after.buildings.find((b) => b.id === 1);
  assert.ok(moved.x + 3 <= MAP_W, `relocated off the map: x=${moved.x} w=3 MAP_W=${MAP_W}`);
});

// ===========================================================================
// R2-F3 — exactly one tier per call, one charge per tier, no skip
// ===========================================================================

test('R2-F3a: 40 consecutive passes charge exactly once per tier with no skip and no double-charge', () => {
  // Open ground so OUT never fails; res_hut heightCap 3 so UP caps early and
  // the alternate path is exercised hard.
  const roads = roadRow(18, 60);
  const b = { id: 1, spec: 'res_hut', x: 25, y: 19, builtTick: -1000 };
  let s = withConnectivity(
    mk({ buildings: [...roads, b], population: 5_000_000, tick: 0, buildingMonitors: [mon(1)] })
  );
  const sp = SPECS.res_hut;
  const perStep = Math.round(sp.cost * BUILDING_AUTO_SCALE_COST_FRACTION);
  const ladderLen = sp.capacityTiers.length;

  let totalCost = 0;
  let steps = 0;
  let prevTier = 0;
  let tick = 0;
  for (let i = 0; i < 40; i++) {
    tick += 30; // clear the cooldown every call
    const r = evaluateBuildingMonitors({ ...s, tick }, tick);
    const after = r.buildings.find((x) => x.id === 1);
    const tier = after.capacityTier ?? 0;
    if (r.upgraded > 0) {
      assert.equal(tier, prevTier + 1, `call ${i}: tier jumped ${prevTier} -> ${tier} (skip-forward / multi-tier)`);
      assert.equal(r.cost, perStep, `call ${i}: charge ${r.cost} != one tier price ${perStep}`);
      totalCost += r.cost;
      steps++;
    } else {
      assert.equal(tier, prevTier, `call ${i}: tier moved ${prevTier} -> ${tier} with upgraded=0 (FREE tier)`);
      assert.equal(r.cost, 0, `call ${i}: charged ${r.cost} without upgrading`);
    }
    prevTier = tier;
    s = withConnectivity({ ...s, tick, buildings: r.buildings, buildingMonitors: r.monitors });
  }
  assert.equal(prevTier, ladderLen - 1, `did not reach the ladder top (tier ${prevTier} of ${ladderLen - 1})`);
  assert.equal(steps, ladderLen - 1, `took ${steps} charged steps to climb ${ladderLen - 1} tiers`);
  assert.equal(totalCost, perStep * (ladderLen - 1), 'total charge != sum of per-tier prices');
});

test('R2-F3b: when BOTH mutation types at an index are blocked the building stays UNLOCKED and uncharged', () => {
  // res_hut heightCap = 3 (PERMANENT once reached). Box it in on all four
  // sides so OUT is TRANSIENTLY blocked too.
  const roads = roadRow(18, 60);
  const b = { id: 1, spec: 'res_hut', x: 25, y: 19, builtTick: -1000, capacityTier: 1, heightStoreys: heightCapOf(SPECS.res_hut) };
  const walls = [
    { id: 11, spec: 'res_hut', x: 26, y: 19, builtTick: -1000 },
    { id: 12, spec: 'res_hut', x: 25, y: 20, builtTick: -1000 },
  ];
  const s = withConnectivity(
    mk({ buildings: [...roads, b, ...walls], population: 5_000_000, tick: 120, buildingMonitors: [mon(1)] })
  );
  const r = evaluateBuildingMonitors(s, 120);
  const after = r.buildings.find((x) => x.id === 1);
  assert.equal(r.upgraded, 0, 'advanced despite both mutation types being blocked');
  assert.equal(r.cost, 0, 'charged for a step that did not happen');
  assert.equal(after.capacityTier ?? 0, 1, 'tier moved');
  assert.ok(!after.scaleLocked, 'LOCKED on a transient fits() failure (F4 class)');
  assert.ok(r.monitors.some((m) => m.buildingId === 1), 'monitor removed on a transient failure');
});

// ===========================================================================
// R2-F4 — locking only on the real last index
// ===========================================================================

test('R2-F4a: boxed-in then FREED resumes scaling (no permanent lock)', () => {
  const roads = roadRow(18, 60);
  const b = { id: 1, spec: 'res_hut', x: 25, y: 19, builtTick: -1000, capacityTier: 1, heightStoreys: heightCapOf(SPECS.res_hut) };
  const walls = [
    { id: 11, spec: 'res_hut', x: 26, y: 19, builtTick: -1000 },
    { id: 12, spec: 'res_hut', x: 25, y: 20, builtTick: -1000 },
  ];
  let s = withConnectivity(
    mk({ buildings: [...roads, b, ...walls], population: 5_000_000, tick: 120, buildingMonitors: [mon(1)] })
  );
  const blocked = evaluateBuildingMonitors(s, 120);
  assert.equal(blocked.upgraded, 0, 'sanity: must be blocked while boxed in');
  // free BOTH blockers
  s = withConnectivity({
    ...s,
    buildings: blocked.buildings.filter((x) => x.id !== 11 && x.id !== 12),
    buildingMonitors: blocked.monitors,
  });
  const freed = evaluateBuildingMonitors({ ...s, tick: 240 }, 240);
  const after = freed.buildings.find((x) => x.id === 1);
  assert.equal(freed.upgraded, 1, 'stayed inert after the blockers were demolished (permanent lock)');
  assert.equal(after.capacityTier, 2, 'tier did not advance after being freed');
});

test('R2-F4b: height-capped-forever AND boxed-forever never sets scaleLocked over 30 passes', () => {
  const roads = roadRow(18, 60);
  const b = { id: 1, spec: 'res_hut', x: 25, y: 19, builtTick: -1000, capacityTier: 1, heightStoreys: heightCapOf(SPECS.res_hut) };
  const walls = [
    { id: 11, spec: 'res_hut', x: 26, y: 19, builtTick: -1000 },
    { id: 12, spec: 'res_hut', x: 25, y: 20, builtTick: -1000 },
  ];
  let s = withConnectivity(
    mk({ buildings: [...roads, b, ...walls], population: 5_000_000, tick: 0, buildingMonitors: [mon(1)] })
  );
  let tick = 0;
  for (let i = 0; i < 30; i++) {
    tick += 30;
    const r = evaluateBuildingMonitors({ ...s, tick }, tick);
    const after = r.buildings.find((x) => x.id === 1);
    assert.ok(!after.scaleLocked, `pass ${i}: locked on a transient block`);
    assert.ok(r.monitors.some((m) => m.buildingId === 1), `pass ${i}: monitor dropped`);
    s = withConnectivity({ ...s, tick, buildings: r.buildings, buildingMonitors: r.monitors });
  }
});

test('R2-F4c: reaching the LAST ladder index does lock (the lock is not simply dead)', () => {
  const sp = SPECS.res_hut;
  const last = sp.capacityTiers.length - 1;
  const roads = roadRow(18, 60);
  const b = { id: 1, spec: 'res_hut', x: 25, y: 19, builtTick: -1000, capacityTier: last - 1, heightStoreys: 1 };
  const s = withConnectivity(
    mk({ buildings: [...roads, b], population: 5_000_000, tick: 120, buildingMonitors: [mon(1)] })
  );
  const r = evaluateBuildingMonitors(s, 120);
  const after = r.buildings.find((x) => x.id === 1);
  assert.equal(after.capacityTier, last, 'did not reach the last index');
  assert.equal(after.scaleLocked, true, 'landing on the last index must lock');
  assert.ok(!r.monitors.some((m) => m.buildingId === 1), 'monitor must be dropped once locked');
});

// ===========================================================================
// R2-F5 — the grown footprint must be visible to EVERY consumer, including
// the REDUCER (the authoritative layer, not just MapView's own predicates).
// ===========================================================================

test('R2-F5a: bulldozing a grown building via one of its EXTRA tiles removes it', () => {
  const roads = roadRow(18, 60);
  const grown = { id: 1, spec: 'res_hut', x: 20, y: 20, builtTick: -1000, capacityTier: 2, footprintW: 2, footprintH: 1 };
  const s = withConnectivity(mk({ buildings: [...roads, grown], funds: 1e9 }));
  assert.ok(occupiedSet(s).has('21,20'), 'sanity: the extra tile is occupied');
  const after = reducer(s, { type: 'bulldoze', x: 21, y: 20 });
  assert.ok(
    !after.buildings.some((b) => b.id === 1),
    'the extra tile is occupied but the REDUCER bulldoze hit test (sp.w/sp.h) attributes it to no building — un-bulldozable dead ground'
  );
});

test('R2-F5b (control): bulldozing a grown building via its BASE tile removes it', () => {
  const roads = roadRow(18, 60);
  const grown = { id: 1, spec: 'res_hut', x: 20, y: 20, builtTick: -1000, capacityTier: 2, footprintW: 2, footprintH: 1 };
  const s = withConnectivity(mk({ buildings: [...roads, grown], funds: 1e9 }));
  const after = reducer(s, { type: 'bulldoze', x: 20, y: 20 });
  assert.ok(!after.buildings.some((b) => b.id === 1), 'base-tile bulldoze must still work');
});

test('R2-F5c: a stampRegion landing on a grown building EXTRA tile flattens it (no overlap)', () => {
  const roads = roadRow(18, 60);
  const grown = { id: 1, spec: 'res_hut', x: 20, y: 20, builtTick: -1000, capacityTier: 2, footprintW: 2, footprintH: 1 };
  const s = withConnectivity(mk({ buildings: [...roads, grown], funds: 1e12, nextId: 900 }));
  const after = reducer(s, {
    type: 'stampRegion',
    x: 21,
    y: 20,
    clipboard: { items: [{ spec: 'res_hut', dx: 0, dy: 0 }] },
  });
  assertNoOverlap(after.buildings, 'R2-F5c stampRegion over a grown extra tile');
});

test('R2-F5d: growing TOWARD a road brings an offline building online', () => {
  // Road row at y=22. Building at (30,20) 1x1 is NOT adjacent (gap at y=21).
  const roads = roadRow(22, 60);
  const b = { id: 1, spec: 'res_hut', x: 30, y: 20, builtTick: -1000 };
  const before = withConnectivity(mk({ buildings: [...roads, b], tick: 500 }));
  assert.equal(isRoadAdjacent(before, b), false, 'sanity: not road-adjacent before growth');
  // Grow DOWN one tile -> occupies (30,20)+(30,21), now orthogonally adjacent to (30,22).
  const grown = { ...b, capacityTier: 2, footprintW: 1, footprintH: 2 };
  const after = withConnectivity(mk({ buildings: [...roads, grown], tick: 500 }));
  assert.equal(isRoadAdjacent(after, grown), true, 'grew toward the road but stayed road-isolated');
  assert.equal(isOnline(after, grown), true, 'grew onto the road frontage but stayed offline');
});

test('R2-F5e: growing AWAY from its only road keeps a building online (base tiles still adjacent)', () => {
  const roads = roadRow(19, 60);
  const b = { id: 1, spec: 'res_hut', x: 30, y: 20, builtTick: -1000 };
  const before = withConnectivity(mk({ buildings: [...roads, b], tick: 500 }));
  assert.equal(isOnline(before, b), true, 'sanity: online before growth');
  const grown = { ...b, capacityTier: 2, footprintW: 1, footprintH: 3 };
  const after = withConnectivity(mk({ buildings: [...roads, grown], tick: 500 }));
  assert.equal(isOnline(after, grown), true, 'growing away from its road knocked it offline');
});

// ===========================================================================
// DETERMINISM (GR#21) — two structurally identical states, 200 ticks
// ===========================================================================

test('DETERMINISM: two identical states produce identical monitor outcomes over 200 ticks', () => {
  const build = () => {
    const roads = roadRow(18, 80);
    const bs = [];
    for (let i = 0; i < 14; i++) bs.push({ id: 100 + i, spec: 'res_hut', x: 3 * i + 2, y: 20, builtTick: -1000 });
    for (let i = 0; i < 6; i++) bs.push({ id: 200 + i, spec: 'edu_nursery', x: 4 * i + 2, y: 24, builtTick: -1000 });
    return withConnectivity(
      mk({
        buildings: [...roads, ...bs],
        population: 400_000,
        tick: 0,
        nextId: 90000,
        buildingMonitors: [
          ...bs.slice(0, 14).map((b) => mon(b.id, 'residents')),
          ...bs.slice(14).map((b) => mon(b.id, 'children')),
        ],
      })
    );
  };
  const run = (s0) => {
    let s = s0;
    const trace = [];
    for (let i = 0; i < 200; i++) {
      s = reducer(s, { type: 'tick' });
      trace.push(
        s.buildings
          .filter((b) => b.capacityTier != null)
          .map((b) => `${b.id}:${b.capacityTier}:${b.heightStoreys ?? 1}:${b.footprintW ?? '-'}x${b.footprintH ?? '-'}:${b.scaleLocked ? 'L' : '.'}`)
          .join('|')
      );
    }
    return trace.join('\n');
  };
  const a = run(build());
  const b = run(build());
  assert.equal(a, b, 'monitor outcomes diverged between two identical runs');
  assert.ok(a.includes(':1:'), 'sanity: the run must actually have scaled something');
});

// ===========================================================================
// CONSERVATION — funds delta attributable to the booked auto-scale outflow
// ===========================================================================

test('CONSERVATION: every auto-scale charge appears in the ledger and no tier is free', () => {
  const roads = roadRow(18, 80);
  const bs = [];
  for (let i = 0; i < 14; i++) bs.push({ id: 100 + i, spec: 'res_hut', x: 3 * i + 2, y: 20, builtTick: -1000 });
  let s = withConnectivity(
    mk({
      buildings: [...roads, ...bs],
      population: 4_000_000,
      tick: 0,
      nextId: 90000,
      buildingMonitors: bs.map((b) => mon(b.id)),
    })
  );
  const perStep = Math.round(SPECS.res_hut.cost * BUILDING_AUTO_SCALE_COST_FRACTION);
  let tierSumPrev = 0;
  let bookedTotal = 0;
  for (let i = 0; i < 300; i++) {
    const next = reducer(s, { type: 'tick' });
    const flow = (next.lastFlows?.outflows ?? []).find((f) => f.label === 'Building Auto-Scale');
    const booked = flow ? flow.value : 0;
    const tierSum = next.buildings.reduce((a, b) => a + (b.capacityTier ?? 0), 0);
    const tiersGained = tierSum - tierSumPrev;
    assert.equal(
      booked,
      tiersGained * perStep,
      `tick ${next.tick}: gained ${tiersGained} tier(s) but booked ${booked} (expected ${tiersGained * perStep})`
    );
    bookedTotal += booked;
    tierSumPrev = tierSum;
    s = next;
  }
  assert.ok(bookedTotal > 0, 'the run must actually have charged something');
});

// ===========================================================================
// PERF — evaluateBuildingMonitors at 13k monitored buildings
// ===========================================================================

test('PERF: evaluateBuildingMonitors at 13,000 monitored buildings stays well inside a smoke bound', () => {
  const buildings = [];
  const monitors = [];
  let id = 1;
  // Road spine rows every 4th row so the buildings are online.
  for (let y = 0; y < 120; y += 4) for (let x = 0; x < 220; x++) buildings.push({ id: id++, spec: 'road', x, y, builtTick: -1000 });
  let placed = 0;
  for (let y = 1; y < 260 && placed < 13000; y += 4) {
    for (let x = 0; x < 220 && placed < 13000; x += 1) {
      const bid = id++;
      buildings.push({ id: bid, spec: 'res_hut', x, y, builtTick: -1000 });
      monitors.push(mon(bid));
      placed++;
    }
  }
  assert.equal(placed, 13000, 'fixture must have 13,000 monitored buildings');
  const s = withConnectivity(mk({ buildings, population: 50_000_000, tick: 600, nextId: id, buildingMonitors: monitors }));
  // warm
  evaluateBuildingMonitors(s, 600);
  const samples = [];
  for (let i = 0; i < 7; i++) {
    const t0 = performance.now();
    evaluateBuildingMonitors(s, 600);
    samples.push(performance.now() - t0);
  }
  samples.sort((a, b) => a - b);
  const median = samples[3];
  // Smoke bound only (BUG-630 doctrine: gate the O(n^2) class, never
  // micro-benchmark). Measured median is reported in the round note.
  assert.ok(median < 3000, `evaluateBuildingMonitors median ${median.toFixed(1)}ms at 13k monitors exceeds the 3000ms smoke bound`);
  console.log(`[PERF] evaluateBuildingMonitors @13k monitors: median ${median.toFixed(1)}ms (samples ${samples.map((x) => x.toFixed(1)).join(', ')})`);
});

// ===========================================================================
// R2-F5s — planRoadReplanCascade's CHECK-5 no-stranding guard must see a
// GROWN building's extra tile, not just its spec-base rect (engine.ts:3193/
// 3205, the SAME function whose blocked-grid ~200 lines above was already
// footprintOf'd — the two halves must agree, or the guard's own contract
// ("a demolition this function allows can never itself flip a building's
// online gate to false") breaks for exactly the buildings BUG-590 put back
// into the ladder: base residential specs sitting against low-tier roads a
// later avenue placement makes redundant).
//
// Fixture: an 8-tile road ring — R=(48,50) is the segment under threat, kept
// "load-bearing-but-redundant" once an A-Road lands nearby. A res_hut GROWN
// to footprintW=1/footprintH=2 at (48,48) occupies (48,48)+(48,49); its
// EXTRA tile (48,49) is the ONLY thing orthogonally touching R. Dispatching
// a real 'placeRoadPath' (rd_aroad) at any of four nearby coordinates
// triggers planRoadReplanCascade; before the fix, CHECK-5 could not see the
// building's real footprint via (48,49) and let R be demolished anyway,
// silently flipping the building's isRoadAdjacent gate true->false. The
// control places an UNGROWN 1x1 res_hut directly AT (48,49) instead — same
// tile-49 dependency on R, via the spec's OWN base rect this time — which
// must stay protected in every case (proving the guard already worked for
// an ungrown building; only the GROWN case was broken).
// ===========================================================================

const RING = [
  { x: 48, y: 50 }, // R — the segment under threat (must survive)
  { x: 49, y: 50 },
  { x: 50, y: 50 },
  { x: 50, y: 51 },
  { x: 50, y: 52 },
  { x: 49, y: 52 },
  { x: 48, y: 52 },
  { x: 48, y: 51 },
];
const DISPATCH_POINTS = [
  { x: 49, y: 54 },
  { x: 50, y: 54 },
  { x: 52, y: 51 },
  { x: 49, y: 53 },
];

function ringRoads() {
  return RING.map((t, i) => ({ id: 600000 + i, spec: 'road', x: t.x, y: t.y, builtTick: -1000 }));
}

function ringFixture(residentBuilding) {
  return withConnectivity(
    mk({
      buildings: [...ringRoads(), residentBuilding],
      unlockedAll: true,
      funds: 1_000_000_000_000,
      tick: 500,
      nextId: 700000,
    })
  );
}

for (const dp of DISPATCH_POINTS) {
  test(`R2-F5s: GROWN res_hut's extra tile protects ring segment R from a placeRoadPath replan at (${dp.x},${dp.y})`, () => {
    const grown = { id: 1, spec: 'res_hut', x: 48, y: 48, builtTick: -1000, capacityTier: 2, footprintW: 1, footprintH: 2 };
    const before = ringFixture(grown);
    assert.equal(isRoadAdjacent(before, grown), true, 'sanity: the grown building starts road-adjacent via its extra tile at (48,49)');

    const after = reducer(before, { type: 'placeRoadPath', spec: 'rd_aroad', tiles: [dp] });

    const ringStillStands = after.buildings.some((b) => b.spec === 'road' && b.x === 48 && b.y === 50);
    assert.ok(ringStillStands, `ring segment R=(48,50) was demolished by the replan cascade at dispatch (${dp.x},${dp.y})`);
    const grownAfter = after.buildings.find((b) => b.id === 1);
    assert.ok(grownAfter, 'the grown building itself must still exist');
    const afterConnectivity = { ...after, roadConnectivity: computeRoadConnectivity(after) };
    assert.equal(
      isRoadAdjacent(afterConnectivity, grownAfter),
      true,
      `the grown building's road-adjacency gate flipped true->false after dispatch (${dp.x},${dp.y}) — R was demolished out from under its only real frontage`
    );
    // NOTE: this deliberately does not assert isOnline() — a real replan
    // cascade dispatched this close to a long hand-built access road can
    // legitimately reroute/restructure tiles FAR from R too (collateral
    // cascade behaviour unrelated to F5s), which can disconnect the fixture
    // from the trunk network for reasons that have nothing to do with this
    // finding. isRoadAdjacent (the actual F5s signal — CHECK-5's own
    // precondition) is the precise, isolated assertion for this defect.
  });

  test(`R2-F5s (control): an UNGROWN 1x1 res_hut directly on (48,49) is protected from the same replan at (${dp.x},${dp.y})`, () => {
    const ungrown = { id: 1, spec: 'res_hut', x: 48, y: 49, builtTick: -1000 };
    const before = ringFixture(ungrown);
    assert.equal(isRoadAdjacent(before, ungrown), true, 'sanity: the control building starts road-adjacent to R');

    const after = reducer(before, { type: 'placeRoadPath', spec: 'rd_aroad', tiles: [dp] });

    const ringStillStands = after.buildings.some((b) => b.spec === 'road' && b.x === 48 && b.y === 50);
    assert.ok(ringStillStands, `CONTROL REGRESSION: ring segment R was demolished even for the ungrown control at dispatch (${dp.x},${dp.y})`);
    const controlAfter = after.buildings.find((b) => b.id === 1);
    assert.ok(controlAfter, 'the control building itself must still exist');
    const afterConnectivity = { ...after, roadConnectivity: computeRoadConnectivity(after) };
    assert.equal(isRoadAdjacent(afterConnectivity, controlAfter), true, `CONTROL REGRESSION: the ungrown building's road-adjacency flipped after dispatch (${dp.x},${dp.y})`);
  });
}
