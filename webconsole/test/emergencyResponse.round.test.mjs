// emergencyResponse.round.test.mjs — pins left behind by the INDEPENDENT
// destructive re-round r2 (attacker opus-reround-feat797-inc4, GR#23) of
// FEAT-2326609797 inc4 "EMERGENCY RESPONSE".
//
// Two pins, both of which the shipped emergencyResponse.test.mjs does NOT
// carry. Every mutant named below was PHYSICALLY RUN against a scratch copy
// of emergencyResponse.ts inside the round's own isolated git worktree and
// observed to red the pin — no claim here is made without that having been
// run.
//
// 1) AC-4 p50Minutes/p90Minutes are POPULATION-weighted, not tile-weighted.
//    The mutant `const weights = rows.map((r) => r.weight)` ->
//    `rows.map(() => 1)` SURVIVED the whole 27-test shipped suite, and is
//    NOT an equivalent mutant: on the fixture below the two readings differ
//    by minutes. This is BUG-870(d)'s own class (coverage counted over
//    TILES, not population) at the percentile site the rework did not
//    revisit.
//
// 2) The tie-break determinism property, pinned on a fixture that ACTUALLY
//    CONTAINS equal-cost routes from two different stations. The shipped
//    suite's BUG-873(1) test reverses BUILDING ids on a straight chain, but
//    segmentId is `spec:fnv1a(sorted tile keys)` (data.ts) — a pure function
//    of tile coordinates — so reversing building ids changes no segment id,
//    no adjacency and no heap ordering, and a straight chain contains no
//    ties at all. That test therefore cannot fail for any tie-break
//    implementation. This one runs a genuine two-source diamond.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { responseMinutesOf, emergencyCoverageOf, emergencyIsochroneOf } from '../src/sim/emergencyResponse.ts';
import { demandForecastOf } from '../src/sim/trafficDemand.ts';
import { weightedPercentile } from '../src/sim/trafficAssignment.ts';
import { initialState } from '../src/sim/engine.ts';

const OFFSET = 300;
const rd = (id, spec, x, y) => ({ id, spec, x: x + OFFSET, y: y + OFFSET, builtTick: 0 });
const bldg = (id, spec, x, y) => ({ id, spec, x: x + OFFSET, y: y + OFFSET });
const k = (x, y) => `${x + OFFSET},${y + OFFSET}`;
function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}

test('ROUND r2: emergencyCoverageOf p50/p90 are POPULATION-weighted -- an equal-weight percentile reads a materially different number on an unequal-weight fixture', () => {
  // One tiny NEAR tile (res_hut) and one huge FAR tile (res_tower_nyc) off a
  // 300-segment chain: population-weighted percentiles are dragged towards
  // the heavy FAR tile, tile-weighted percentiles are not.
  const row = 20;
  const chain = 300;
  const buildings = [bldg(1, 'hea_ambulance', -1, row)];
  let id = 2;
  for (let i = 0; i < chain; i++) buildings.push(rd(id++, i % 2 === 0 ? 'rd_aroad' : 'rd_dual', i, row));
  buildings.push(bldg(50001, 'res_tower_nyc', chain - 1, row)); // far, heavy
  buildings.push(bldg(50002, 'res_hut', 1, row)); // near, light
  const s = board(buildings, 5_000_000);

  const responses = responseMinutesOf(s, 'ambulance');
  const demandTiles = demandForecastOf(s);
  const weightByTile = new Map(demandTiles.map((t) => [`${t.x},${t.y}`, t.residentsActual + t.workersActual]));
  const nearWeight = weightByTile.get(k(1, row)) ?? 0;
  const farWeight = weightByTile.get(k(chain - 1, row)) ?? 0;
  // Fixture preconditions (BUG-862 lesson): both tiles reachable, both
  // carrying real, GENUINELY UNEQUAL population.
  assert.ok(responses.get(k(1, row)) !== undefined, 'precondition: near tile reachable');
  assert.ok(responses.get(k(chain - 1, row)) !== undefined, 'precondition: far tile reachable');
  assert.ok(nearWeight > 0 && farWeight > 0, 'precondition: both tiles carry population');
  assert.notEqual(nearWeight, farWeight, 'precondition: weights must genuinely differ, else weight=1 would be an equivalent mutant here');

  const rows = [...responses.entries()]
    .map(([tileKey, minutes]) => ({ minutes, weight: weightByTile.get(tileKey) ?? 0 }))
    .sort((a, b) => a.minutes - b.minutes);
  const values = rows.map((r) => r.minutes);
  const expectedP50 = weightedPercentile(values, rows.map((r) => r.weight), 0.5);
  const expectedP90 = weightedPercentile(values, rows.map((r) => r.weight), 0.9);
  const tileWeightedP50 = weightedPercentile(values, rows.map(() => 1), 0.5);
  assert.notEqual(
    expectedP50,
    tileWeightedP50,
    'precondition: population-weighted and tile-weighted p50 must differ on this fixture, else the mutant is equivalent and this pin proves nothing',
  );

  const cov = emergencyCoverageOf(s, 'ambulance');
  assert.ok(Math.abs(cov.p50Minutes - expectedP50) < 1e-9, `p50 ${cov.p50Minutes} !== population-weighted ${expectedP50}`);
  assert.ok(Math.abs(cov.p90Minutes - expectedP90) < 1e-9, `p90 ${cov.p90Minutes} !== population-weighted ${expectedP90}`);
  // MUTANT (SCRATCH-PROVEN, r2 round): `const weights = rows.map((r) => r.weight)`
  // -> `rows.map(() => 1)` inside emergencyCoverageOf. It SURVIVES the
  // shipped 27-test suite; against this pin it reds, because the two
  // readings were just proven to differ.
});

test('ROUND r2: equal-cost routes from TWO different stations give the same isochrone under any tie-break -- a real diamond, not an id relabel', () => {
  // Two ambulance stations at opposite ends of a 20-tile corridor with a
  // parallel bypass of identical length: the middle tiles are genuinely
  // equidistant from two DIFFERENT sources, and the bypass gives two
  // equal-cost paths to the same segments. Run twice over two distinct
  // state objects built from the same topology in a DIFFERENT building
  // order (which does change `sortedBuildings` and therefore the order
  // sources are inserted into the Dijkstra's source set).
  function run(reverseOrder) {
    const row = 15;
    const n = 20;
    const list = [];
    let id = 1;
    list.push(bldg(id++, 'hea_ambulance', -1, row));
    list.push(bldg(id++, 'hea_ambulance', n, row));
    for (let i = 0; i < n; i++) list.push(rd(id++, i % 2 === 0 ? 'rd_aroad' : 'rd_dual', i, row));
    for (let i = 5; i < 15; i++) list.push(rd(id++, i % 2 === 0 ? 'rd_aroad' : 'rd_dual', i, row + 1));
    for (let i = 0; i < n; i++) list.push(bldg(id++, 'res_hut', i, row));
    const buildings = reverseOrder ? [...list].reverse() : list;
    const s = board(buildings, 200_000);
    const iso = emergencyIsochroneOf(s, 'ambulance');
    return [...iso.entries()].sort((a, b) => (a[0] < b[0] ? -1 : 1)).map(([key, v]) => `${key}=${v}`).join('\n');
  }
  const a = run(false);
  const b = run(true);
  assert.ok(a.length > 0, 'precondition: the fixture must produce a non-empty isochrone');
  assert.ok(a.split('\n').length >= 10, 'precondition: the fixture must produce many segments, not a degenerate single run');
  assert.equal(a, b, 'two sources with equal-cost routes must converge to identical distances regardless of building/source insertion order');
});
