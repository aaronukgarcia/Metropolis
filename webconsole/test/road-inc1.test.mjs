// road-inc1.test.mjs — FEAT-1972079907 inc1: road types + capacity + fittingTier,
// deterministic auto-connect router, upgrade-on-connect, blocked-notice, conservation.
//
// Determinism is the crux (GR#21): the router uses a level-synchronous BFS with
// strict (x,y) tie-breaking and a fixed direction ordinal — no Date/Math.random.
//
// RED proof (scratch cp/mv, NEVER git): inject order-dependence into the router
// (e.g. shuffle the frontier before the goal scan) and the "router determinism"
// test below goes RED; restoring roadConnect.ts returns it GREEN. Demonstrated at
// build time via `cp roadConnect.ts roadConnect.bak; <edit>; <run RED>; mv back`.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { planConnector } from '../src/sim/roadConnect.ts';
import {
  SPECS,
  fittingTier,
  roadTierOf,
  ROAD_TIER_SPECS,
  ROAD_TIER_CAPACITY,
  MAP_W,
  MAP_H,
} from '../src/sim/data.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

const key = (x, y) => `${x},${y}`;

// Build a clean board (no starter city) with an explicit building list.
function board(buildings) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null };
}

// ===================== fittingTier =====================

test('fittingTier: a small building maps to tier 1', () => {
  assert.equal(fittingTier(SPECS.res_hut), 1, 'res_hut (1×1, 8 residents) → tier 1');
  assert.equal(fittingTier(SPECS.com_shop), 1, 'com_shop (1×1) → tier 1');
});

test('fittingTier: large / industry / mega buildings map to a HIGHER tier', () => {
  const industry = fittingTier(SPECS.ind_heavy); // 3×3 heavy industry
  const mega = fittingTier(SPECS.land_space); // 5×5 mega landmark
  assert.ok(industry > 1, `heavy industry → tier ${industry} (> 1)`);
  assert.ok(mega > industry, `mega landmark → tier ${mega} (> industry ${industry})`);
  assert.equal(mega, 5, 'mega facility tops the ladder at tier 5');
  // Ordering sanity across the housing ladder.
  assert.ok(fittingTier(SPECS.res_highrise) >= fittingTier(SPECS.res_hut));
});

test('fittingTier: deterministic — identical spec yields identical tier', () => {
  for (const id of ['res_hut', 'ind_heavy', 'land_space', 'off_tower', 'com_mall']) {
    assert.equal(fittingTier(SPECS[id]), fittingTier(SPECS[id]), `${id} stable`);
  }
});

test('road ladder: the five tiers carry roadTier + capacity, ordered ascending', () => {
  const ladder = [1, 2, 3, 4, 5].map((t) => SPECS[ROAD_TIER_SPECS[t]]);
  for (let i = 0; i < ladder.length; i++) {
    assert.equal(roadTierOf(ladder[i]), i + 1, `${ladder[i].id} is tier ${i + 1}`);
    assert.equal(ladder[i].capacity, ROAD_TIER_CAPACITY[i + 1], `${ladder[i].id} capacity`);
    if (i > 0) assert.ok(ladder[i].capacity > ladder[i - 1].capacity, 'capacity increases with tier');
  }
});

// ===================== router determinism =====================

test('router determinism: a TIE between two equal-cost routes resolves identically every run', () => {
  // Building at (5,5). Two roads equidistant (path length 2): (2,5) reached going
  // left, (5,2) reached going up. The strict (x,y) tie-break MUST pick the goal
  // (3,5) (x=3) over (5,3) (x=5), so the connector is the LEFT route — every run.
  const mk = () => ({
    occupied: new Set([key(5, 5), key(2, 5), key(5, 2)]),
    roads: new Set([key(2, 5), key(5, 2)]),
    bx: 5,
    by: 5,
    bw: 1,
    bh: 1,
    mapW: MAP_W,
    mapH: MAP_H,
  });

  const r1 = planConnector(mk());
  const r2 = planConnector(mk());

  assert.equal(r1.blocked, false);
  assert.equal(r1.connected, false);
  // Exact, tie-broken path — proves the ordering is fixed, not incidental.
  assert.deepEqual(
    r1.path,
    [
      { x: 4, y: 5 },
      { x: 3, y: 5 },
    ],
    'the lowest-(x,y) goal wins the tie (left route)'
  );
  assert.deepEqual(r1.junctions, [{ x: 2, y: 5 }], 'junction is the joined road tile');
  assert.deepEqual(r1, r2, 'two independent runs produce identical plans');
});

test('router: a building already touching a road needs NO connector', () => {
  const occupied = new Set([key(4, 5), key(5, 5)]);
  const roads = new Set([key(4, 5)]);
  const plan = planConnector({ occupied, roads, bx: 5, by: 5, bw: 1, bh: 1, mapW: MAP_W, mapH: MAP_H });
  assert.equal(plan.connected, true, 'already connected');
  assert.deepEqual(plan.path, [], 'no connector laid');
});

// ===================== around-buildings (impassable) =====================

test('around-buildings: the router routes AROUND an obstacle (no tile on an occupied cell)', () => {
  // Road at (0,0); building at (4,0); obstacle at (2,0) blocking the straight line.
  const roads = new Set([key(0, 0)]);
  const occupied = new Set([key(0, 0), key(4, 0), key(2, 0)]);
  const plan = planConnector({ occupied, roads, bx: 4, by: 0, bw: 1, bh: 1, mapW: MAP_W, mapH: MAP_H });

  assert.equal(plan.blocked, false, 'a detour route exists');
  assert.equal(plan.connected, false);
  assert.ok(plan.path.length > 0, 'connector laid');
  // No connector tile may land on ANY occupied cell (buildings are impassable).
  for (const p of plan.path) {
    assert.ok(!occupied.has(key(p.x, p.y)), `path cell ${p.x},${p.y} must be empty`);
  }
  // Specifically it must NOT drive through the obstacle at (2,0).
  assert.ok(!plan.path.some((p) => p.x === 2 && p.y === 0), 'must route around the obstacle');
});

// ===================== upgrade-on-connect =====================

test('upgrade-on-connect: a big-footprint building upgrades the tier-1 lane it joins', () => {
  // A tier-1 lane tile at (10,12). A 3×3 heavy-industry building at (10,8)..(12,10),
  // one empty row (y=11) from the lane. fittingTier(ind_heavy) = 4 (rd_dual).
  const lane = { id: 1, spec: 'road', x: 10, y: 12, builtTick: 0 };
  const s = board([lane]);
  const tier = fittingTier(SPECS.ind_heavy);
  const connSpecId = ROAD_TIER_SPECS[tier];
  assert.ok(tier > roadTierOf(SPECS.road), 'precondition: fitting tier outranks the lane');

  const after = reducer(s, { type: 'place', spec: 'ind_heavy', x: 10, y: 8 });

  // The building was placed.
  assert.ok(after.buildings.some((b) => b.spec === 'ind_heavy' && b.x === 10 && b.y === 8), 'building placed');
  // A connector of the fitting tier was laid (at least one tile).
  assert.ok(
    after.buildings.some((b) => b.spec === connSpecId && roadTierOf(SPECS[b.spec]) === tier),
    'connector of the fitting tier laid'
  );
  // The joined lane tile at (10,12) was UPGRADED to the connector tier.
  const junction = after.buildings.find((b) => b.x === 10 && b.y === 12);
  assert.equal(junction.spec, connSpecId, 'the joined lane was upgraded to the connector tier');
  assert.equal(after.roadNotice, null, 'connected — no notice');
});

test('upgrade-on-connect: a road already AT/ABOVE the connector tier is NOT downgraded', () => {
  // A tier-5 motorway tile at (10,12); a small building (fitting tier 1) joins it.
  const mtile = { id: 1, spec: 'm20', x: 10, y: 12, builtTick: 0 };
  const s = board([mtile]);
  const after = reducer(s, { type: 'place', spec: 'res_hut', x: 10, y: 10 });
  const junction = after.buildings.find((b) => b.x === 10 && b.y === 12);
  assert.equal(junction.spec, 'm20', 'the motorway is not downgraded to a lane');
});

// ===================== blocked → notice =====================

test('blocked: a building walled off from any road is placed, no connector, notice surfaced', () => {
  // res_hut at (5,5), all four orthogonal neighbours walled by buildings, road far away.
  const walls = [
    { id: 1, spec: 'com_shop', x: 4, y: 5, builtTick: 0 },
    { id: 2, spec: 'com_shop', x: 6, y: 5, builtTick: 0 },
    { id: 3, spec: 'com_shop', x: 5, y: 4, builtTick: 0 },
    { id: 4, spec: 'com_shop', x: 5, y: 6, builtTick: 0 },
    { id: 5, spec: 'road', x: 100, y: 100, builtTick: 0 },
  ];
  const s = board(walls);
  const before = s.buildings.length;
  const after = reducer(s, { type: 'place', spec: 'res_hut', x: 5, y: 5 });

  assert.ok(after.buildings.some((b) => b.spec === 'res_hut' && b.x === 5 && b.y === 5), 'building STILL placed');
  assert.equal(after.buildings.length, before + 1, 'exactly one building added — NO connector laid');
  assert.equal(after.roadNotice, 'no road access', 'a "no road access" notice is surfaced');
  assert.equal(after.funds, s.funds, 'zone is free and no connector charged');
});

test('blocked: a board with NO roads at all surfaces the notice', () => {
  const s = board([{ id: 1, spec: 'res_hut', x: 200, y: 120, builtTick: 0 }]);
  const after = reducer(s, { type: 'place', spec: 'off_tower', x: 100, y: 60 });
  assert.equal(after.roadNotice, 'no road access', 'no roads anywhere → notice');
});

// ===================== conservation (connector charged through the ledger) =====================

test('conservation: place-with-auto-connect + a tick keeps conservation', () => {
  // Real board (starter city). res_hut near the M20 lays a connector; the cost
  // flows through the ledger; the tick-boundary conservation invariant holds.
  const s0 = initialState();
  const before = s0.buildings.length;
  const s1 = reducer(s0, { type: 'place', spec: 'res_hut', x: 50, y: 50 });
  assert.ok(s1.buildings.length > before + 1, 'a connector was laid (more than one building added)');
  assert.equal(s1.roadNotice, null, 'connected to the M20');

  const s2 = reducer(s1, { type: 'tick' });
  const report = runConsistencyChecks(s2);
  const check = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.equal(check.ok, true, 'conservation holds after place-with-auto-connect + tick');
});

test('conservation: the connector spend is journaled to the ledger', () => {
  const s0 = initialState();
  const s1 = reducer(s0, { type: 'place', spec: 'res_hut', x: 50, y: 50 });
  const connectorEntry = s1.ledger.find((e) => e.label.startsWith('Connector'));
  assert.ok(connectorEntry, 'a "Connector …" ledger entry was recorded');
  assert.ok(connectorEntry.amount <= 0, 'the connector is a spend (non-positive amount)');
});

// ===================== placeholders unaffected =====================

test('placeholders unaffected: a placeholder cannot be placed, so auto-connect never runs', () => {
  const s = board([{ id: 1, spec: 'road', x: 10, y: 10, builtTick: 0 }]);
  // rail_branch is still a "coming soon" placeholder (canEnterSim === false).
  assert.equal(SPECS.rail_branch.placeholder, true, 'precondition: rail_branch is a placeholder');
  const after = reducer(s, { type: 'place', spec: 'rail_branch', x: 12, y: 10 });
  assert.deepEqual(after, s, 'reducer returns state untouched — no building, no connector, no notice');
});

// ===================== determinism through the reducer =====================

test('reducer determinism: identical placement on identical boards → identical states', () => {
  const mk = () => board([{ id: 1, spec: 'road', x: 0, y: 5, builtTick: 0 }]);
  const a = reducer(mk(), { type: 'place', spec: 'ind_heavy', x: 5, y: 5 });
  const b = reducer(mk(), { type: 'place', spec: 'ind_heavy', x: 5, y: 5 });
  assert.deepEqual(a.buildings, b.buildings, 'connector + upgrades reproduce exactly');
  assert.equal(a.funds, b.funds, 'identical spend');
});
