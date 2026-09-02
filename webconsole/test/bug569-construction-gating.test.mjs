// bug569-construction-gating.test.mjs — BUG-569: four contribution sites never
// called the shared isOnline(s, b) predicate (data.ts:460-...), so an
// under-construction building's effect leaked into the economy/HUD one tick
// early:
//
//   1. Tourism income (engine.ts computeFlows) — summed sp.tourism over every
//      building regardless of construction.
//   2. Harbour Freight-Tax boost (engine.ts computeFlows) — existence-only
//      `.some(b => b.spec === 'land_harbour')`, no online check.
//   3. Commuter Revenue (engine.ts computeFlows via stationLinks()) —
//      stationLinks() tests road-adjacency only (the network-wiring
//      question), never construction; the usage site never gated on
//      isOnline either.
//   4. HUD school utilisation (data.ts utilisationOf, 'school' case) — summed
//      every school's `children` capacity ungated, disagreeing with the
//      gated serviceCoverageOf() school rows.
//
// Fix: the SAME one-line `if (!isOnline(s, b)) continue;` guard already used
// at every other contribution site (countByKindOnline, powerStats, sumBy,
// etc. — see data.ts:1607-1616's comment for the idiom).
//
// RED-PROOF (GR#23): each guard below was temporarily reverted in turn
// (scratch cp/mv, never git — GR#24) and the corresponding "ZERO while
// under construction" assertion went RED (non-zero), confirming the test can
// actually fail; then restored to GREEN. See task report for the per-site
// revert/restore transcript.
//
// Run with the scoped test runner (never a full glob):
// node ../tools/test/scoped.mjs test/bug569-construction-gating.test.mjs

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isOnline, computeRoadConnectivity, utilisationOf } from '../src/sim/data.ts';
import { initialState, computeFlows } from '../src/sim/engine.ts';

let _id = 569000;
const B = (spec, x, y, extra = {}) => ({ id: _id++, spec, x, y, ...extra });

// Same harness as bug-520/bug430: road connectivity computed exactly as
// advance() does every tick.
function city(buildings, tick, population = 1000) {
  const s = initialState();
  const st = { ...s, buildings: [...buildings], population, tick };
  st.roadConnectivity = computeRoadConnectivity(st);
  return st;
}

// ─────────────────────────────────────────────────────────────────────────
// 1. Tourism income — lei_cinema (tourism: 10), constructionTicks = 7.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-569: an under-construction cinema contributes ZERO tourism income', () => {
  const road = B('road', 0, 10, { builtTick: 0 });
  const s = city([B('lei_cinema', 1, 10, { builtTick: 195 }), road], 200); // 5 ticks old, needs 7
  const cinema = s.buildings[0];
  assert.equal(isOnline(s, cinema), false, 'setup: cinema must still be under construction');

  const { inflows } = computeFlows(s);
  const tourism = inflows.find((f) => f.label === 'Tourism');
  assert.equal(tourism?.value ?? 0, 0, 'an under-construction cinema must add ZERO tourism income (was the leak)');
});

test('BUG-569: the SAME cinema, once online, contributes NON-ZERO tourism income', () => {
  const road = B('road', 0, 10, { builtTick: 0 });
  const s = city([B('lei_cinema', 1, 10, { builtTick: 0 }), road], 200); // 200 ticks old, well past 7
  const cinema = s.buildings[0];
  assert.equal(isOnline(s, cinema), true, 'setup: cinema must be online');

  const { inflows } = computeFlows(s);
  const tourism = inflows.find((f) => f.label === 'Tourism');
  assert.ok((tourism?.value ?? 0) > 0, 'an online cinema must add non-zero tourism income');
});

// ─────────────────────────────────────────────────────────────────────────
// 2. Harbour Freight-Tax boost — land_harbour, constructionTicks = 46.
// Needs an industrial/mine building present so Freight Tax is non-zero and
// the boost is visible in the ratio.
// ─────────────────────────────────────────────────────────────────────────
function freightTax(s) {
  return computeFlows(s).inflows.find((f) => f.label === 'Freight Tax')?.value ?? 0;
}

// Mine and harbour sit on separate rows, each with its own edge-connected
// road stub directly to their west, so each building's own road-adjacency/
// connectivity is satisfied independently (isOnline gates BOTH the mine and
// the harbour, so both must be genuinely online for the boost to show).
function mineRoad(y) {
  return [B('mine_quarry', 1, y, { builtTick: 0 }), B('road', 0, y, { builtTick: 0 })];
}

test('BUG-569: an under-construction harbour grants NO Freight-Tax boost', () => {
  const [mine, mineRoadTile] = mineRoad(10);
  const harbourRoad = B('road', 0, 20, { builtTick: 0 });
  const harbour = B('land_harbour', 1, 20, { builtTick: 190 }); // 10 ticks old, needs 46
  const withHarbour = city([mine, mineRoadTile, harbour, harbourRoad], 200);
  assert.equal(isOnline(withHarbour, harbour), false, 'setup: harbour must still be under construction');

  const without = city([mine, mineRoadTile], 200);
  assert.equal(
    freightTax(withHarbour),
    freightTax(without),
    'an under-construction harbour must not change Freight Tax (boost must not apply, was the leak)'
  );
});

test('BUG-569: the SAME harbour, once online, DOES boost Freight Tax', () => {
  const [mine, mineRoadTile] = mineRoad(10);
  const harbourRoad = B('road', 0, 20, { builtTick: 0 });
  const harbour = B('land_harbour', 1, 20, { builtTick: 0 });
  const withHarbour = city([mine, mineRoadTile, harbour, harbourRoad], 200);
  assert.equal(isOnline(withHarbour, harbour), true, 'setup: harbour must be online');

  const without = city([mine, mineRoadTile], 200);
  assert.ok(
    freightTax(withHarbour) > freightTax(without),
    'an online harbour must boost Freight Tax above the no-harbour baseline'
  );
});

// ─────────────────────────────────────────────────────────────────────────
// 3. Commuter Revenue — station_sanderling (network category, cost 0 so
// constructionTicks floors at 3), placed road-adjacent so stationLinks()
// always counts it as connected; only construction time distinguishes the
// two cases.
// ─────────────────────────────────────────────────────────────────────────
function commuterRevenue(s) {
  return computeFlows(s).inflows.find((f) => f.label === 'Commuter Revenue')?.value ?? 0;
}

test('BUG-569: an under-construction station earns ZERO Commuter Revenue', () => {
  const road = B('road', 0, 10, { builtTick: 0 });
  const s = city([B('station_sanderling', 1, 10, { builtTick: 199 }), road], 200); // 1 tick old, needs 3
  const station = s.buildings[0];
  assert.equal(isOnline(s, station), false, 'setup: station must still be under construction');

  assert.equal(commuterRevenue(s), 0, 'an under-construction station must earn ZERO Commuter Revenue (was the leak)');
});

test('BUG-569: the SAME station, once online, earns NON-ZERO Commuter Revenue', () => {
  const road = B('road', 0, 10, { builtTick: 0 });
  const s = city([B('station_sanderling', 1, 10, { builtTick: 0 }), road], 200);
  const station = s.buildings[0];
  assert.equal(isOnline(s, station), true, 'setup: station must be online');

  assert.ok(commuterRevenue(s) > 0, 'an online station must earn non-zero Commuter Revenue');
});

// ─────────────────────────────────────────────────────────────────────────
// 4. HUD school utilisation — edu_nursery (children: 30), constructionTicks = 3.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-569: an under-construction school does not inflate school utilisation places', () => {
  const road = B('road', 0, 10, { builtTick: 0 });
  const nursery = B('edu_nursery', 1, 10, { builtTick: 199 }); // 1 tick old, needs 3
  const withNursery = city([nursery, road], 200, 50);
  assert.equal(isOnline(withNursery, nursery), false, 'setup: nursery must still be under construction');

  const withoutNursery = city([road], 200, 50);
  const utilWith = utilisationOf(withNursery, nursery);
  const utilWithout = utilisationOf(withoutNursery, road);
  // No online school at all in either state -> both should read the same
  // "no capacity" result (null), proving the under-construction nursery did
  // not contribute to `places`.
  assert.equal(utilWith, null, 'an under-construction school must not register any student places (was the leak)');
  assert.equal(utilWithout, null, 'sanity: no school at all also yields null (no capacity to divide by)');
});

test('BUG-569: the SAME school, once online, DOES register student places', () => {
  const road = B('road', 0, 10, { builtTick: 0 });
  const nursery = B('edu_nursery', 1, 10, { builtTick: 0 });
  const s = city([nursery, road], 200, 50);
  assert.equal(isOnline(s, nursery), true, 'setup: nursery must be online');

  const util = utilisationOf(s, nursery);
  assert.ok(util !== null, 'an online school must register student places (non-null utilisation)');
  assert.ok(util.ratio >= 0, 'sanity: ratio is a valid number');
});

// ─────────────────────────────────────────────────────────────────────────
// Determinism (GR#21) — pure functions of state; no Date/Math.random.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-569: computeFlows/utilisationOf are deterministic across identical states', () => {
  const mk = () =>
    city(
      [
        B('lei_cinema', 1, 10, { builtTick: 0 }),
        B('land_harbour', 5, 10, { builtTick: 0 }),
        B('station_sanderling', 9, 10, { builtTick: 0 }),
        B('edu_nursery', 12, 10, { builtTick: 0 }),
        B('road', 0, 10, { builtTick: 0 }),
      ],
      200
    );
  const a = mk();
  const b = mk();
  assert.deepEqual(computeFlows(a), computeFlows(b), 'identical states must yield identical flows');
  assert.deepEqual(
    utilisationOf(a, a.buildings[3]),
    utilisationOf(b, b.buildings[3]),
    'identical states must yield identical school utilisation'
  );
});
