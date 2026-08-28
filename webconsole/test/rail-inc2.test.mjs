// rail-inc2.test.mjs — FEAT-1972079902 inc2: LIVE DETERMINISTIC TRAINS.
//
// Run with `npm test` (node --test). Node's type-stripping imports the real
// TypeScript, so these assertions exercise the exact shipped train maths.
//
// The crux is DETERMINISM (GR#21): trainPositions is a pure function of
// (tick, geometry, demand) — no Date.now / Math.random / performance.now — so a
// given (tick, board, demand) always yields byte-identical glyphs. Trains are
// UI-derived only: no SimState, no economy. Every assertion pins a real value so
// each test can actually FAIL.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  buildRailGeometry,
  trainPositions,
  trainCountFor,
  TRAVEL_TICKS,
  DWELL_TICKS,
} from '../src/sim/trains.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { lineUsageOf } from '../src/sim/data.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

// A straight 4-tile vertical rail line at x=10, y=11..14, with a station tile at
// (11,12) — adjacent to rail point (10,12), so that point is a dwell station.
const RAIL_TILES = [
  { spec: 'rail', x: 10, y: 11 },
  { spec: 'rail', x: 10, y: 12 },
  { spec: 'rail', x: 10, y: 13 },
  { spec: 'rail', x: 10, y: 14 },
];
const STATION_TILES = [{ x: 11, y: 12 }];

// Demand helpers (saturation drives count + colour).
const demand = (spec, saturation, overCapacity = false) => [{ spec, saturation, overCapacity }];

// ===================== 1. determinism =====================

test('determinism: same (geometry, demand, tick) → byte-identical trains, any insertion order', () => {
  const g1 = buildRailGeometry(RAIL_TILES, STATION_TILES);
  const a = JSON.stringify(trainPositions(g1, demand('rail', 0.6), 7));
  const b = JSON.stringify(trainPositions(g1, demand('rail', 0.6), 7));
  assert.equal(a, b, 'two calls with identical inputs are byte-identical');

  // A DIFFERENT insertion order of the SAME board must yield identical geometry
  // (buildRailGeometry sorts), hence identical trains.
  const shuffled = [RAIL_TILES[2], RAIL_TILES[0], RAIL_TILES[3], RAIL_TILES[1]];
  const g2 = buildRailGeometry(shuffled, STATION_TILES);
  assert.equal(JSON.stringify(g1), JSON.stringify(g2), 'geometry independent of insertion order');
  const c = JSON.stringify(trainPositions(g2, demand('rail', 0.6), 7));
  assert.equal(a, c, 'trains independent of insertion order');
});

// ===================== 2. movement (pure function of tick) =====================

test('movement: trains advance across ticks, and re-reading the same tick is identical', () => {
  const g = buildRailGeometry(RAIL_TILES, STATION_TILES);
  // count=1 (saturation 0.1) isolates a single train we can track.
  assert.equal(trainCountFor(0.1), 1, 'precondition: saturation 0.1 → 1 train');

  const posAt = (tick) => {
    const tr = trainPositions(g, demand('rail', 0.1), tick)[0].trains[0];
    return { x: tr.x, y: tr.y };
  };

  // Pure function of tick: same tick twice → identical (this is the no-wallclock
  // property in tick-space; test 5 adds the wall-time delay variant).
  assert.deepEqual(posAt(3), posAt(3), 'same tick reproduces the same position');

  // Actually moves: over a full lap the train visits MORE than one position.
  const seen = new Set();
  for (let t = 0; t < 40; t++) {
    const p = posAt(t);
    seen.add(`${p.x},${p.y}`);
  }
  assert.ok(seen.size > 1, 'the train occupies more than one position over time (it moves)');

  // A concrete step: at tick 0 it starts travelling from the first tile; by the
  // time it has crossed a segment its y has changed.
  const p0 = posAt(0);
  const p1 = posAt(0 + TRAVEL_TICKS);
  assert.notDeepEqual(p0, p1, 'position after one full segment differs from the start');
});

// ===================== 3. stop-at-station =====================

test('stop-at-station: a train holds position at the station tile for the dwell band', () => {
  const g = buildRailGeometry(RAIL_TILES, STATION_TILES);
  // points sorted (x,y): (10,11)=0,(10,12)=1,(10,13)=2,(10,14)=3. Station adj → idx 1.
  assert.deepEqual(g[0].stationIdx, [1], 'the point next to the station is the dwell index');

  const train = (tick) => trainPositions(g, demand('rail', 0.1), tick)[0].trains[0];

  // Circuit [0,1,2,3,2,1], dwell at nodes hitting point 1 (j=1 and j=5).
  // Walk (offset 0): travel[0,2) node0→1; DWELL at point1 for local ∈ [2,5).
  for (const t of [2, 3, 4]) {
    const tr = train(t);
    assert.equal(tr.stoppedAtStation, true, `tick ${t}: stopped at the station`);
    assert.equal(tr.x, 10, `tick ${t}: held at station x`);
    assert.equal(tr.y, 12, `tick ${t}: held at station y (point 1)`);
  }
  // Held for exactly DWELL_TICKS consecutive ticks at that station.
  assert.equal([2, 3, 4].length, DWELL_TICKS, 'dwell band width equals DWELL_TICKS');

  // Immediately before the dwell it is moving, not stopped, at a different tile.
  const before = train(0);
  assert.equal(before.stoppedAtStation, false, 'tick 0: not stopped');
  assert.notEqual(`${before.x},${before.y}`, '10,12', 'tick 0: not yet at the station tile');
});

// ===================== 4. demand → count =====================

test('demand→count: higher saturation yields more trains; zero demand yields none', () => {
  // Exact ladder anchors (PLACEHOLDER-balance).
  assert.equal(trainCountFor(0), 0, 'saturation 0 → 0 trains');
  assert.equal(trainCountFor(0.1), 1, 'saturation 0.1 → 1');
  assert.equal(trainCountFor(0.5), 3, 'saturation 0.5 → 3');
  assert.equal(trainCountFor(1), 5, 'saturation 1.0 → 5');
  // Monotonic non-decreasing.
  assert.ok(trainCountFor(0.1) < trainCountFor(0.5), 'more demand → more trains');
  assert.ok(trainCountFor(0.5) < trainCountFor(0.9), 'and again higher up the ladder');

  // And trainPositions actually emits that many glyphs.
  const g = buildRailGeometry(RAIL_TILES, STATION_TILES);
  assert.equal(trainPositions(g, demand('rail', 0), 5)[0].trains.length, 0, 'idle line → no trains');
  assert.equal(trainPositions(g, demand('rail', 0.5), 5)[0].trains.length, 3, '0.5 → 3 glyphs');
  assert.equal(trainPositions(g, demand('rail', 1), 5)[0].trains.length, 5, '1.0 → 5 glyphs');

  // A line with no demand entry at all defaults to zero trains.
  assert.equal(trainPositions(g, [], 5)[0].trains.length, 0, 'no demand entry → 0 trains');
});

test('demand→count: the over-capacity colour bucket rides the BUG-425 split', () => {
  const g = buildRailGeometry(RAIL_TILES, STATION_TILES);
  const ok = trainPositions(g, demand('rail', 0.6, false), 5)[0].trains;
  const hot = trainPositions(g, demand('rail', 0.6, true), 5)[0].trains;
  assert.ok(ok.every((t) => t.bucket === 'ok'), 'within capacity → ok (green)');
  assert.ok(hot.every((t) => t.bucket === 'hot'), 'over capacity → hot (red)');
});

// ===================== 5. no wall-clock =====================

test('no-wallclock: positions do not change when only real time passes', () => {
  const g = buildRailGeometry(RAIL_TILES, STATION_TILES);
  const a = JSON.stringify(trainPositions(g, demand('rail', 0.7), 11));
  // Burn real wall-time WITHOUT advancing the sim tick.
  const until = Date.now() + 30;
  while (Date.now() < until) {
    /* busy-wait so measurable wall-time passes between the two identical-tick calls */
  }
  const b = JSON.stringify(trainPositions(g, demand('rail', 0.7), 11));
  assert.equal(a, b, 'identical tick after a real delay → identical trains (no wall-clock leak)');
});

// ===================== 6. no economic change =====================

test('no-economic-change: computing trains never alters funds / flows / conservation', () => {
  // Baseline tick with NO train computation.
  const baseline = reducer(initialState(), { type: 'tick' });

  // Same tick, but exercise the whole train pipeline (geometry + positions) first.
  const withTrains = (() => {
    const s = initialState();
    const railTiles = s.buildings
      .filter((b) => b.spec === 'rail' || b.spec === 'hs1')
      .map((b) => ({ spec: b.spec, x: b.x, y: b.y }));
    const geoms = buildRailGeometry(railTiles, []);
    const railDemand = lineUsageOf(s)
      .filter((l) => l.kind === 'rail')
      .map((l) => ({ spec: l.spec, saturation: l.saturation, overCapacity: l.overCapacity }));
    trainPositions(geoms, railDemand, s.tick); // pure read — must not touch state
    return reducer(s, { type: 'tick' });
  })();

  assert.equal(withTrains.funds, baseline.funds, 'funds unchanged by the train read-out');
  assert.deepEqual(withTrains.lastFlows, baseline.lastFlows, 'flows unchanged');
  assert.equal(withTrains.population, baseline.population, 'population unchanged');

  const report = runConsistencyChecks(withTrains);
  const conservation = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.ok(conservation, 'conservation check exists');
  assert.equal(conservation.ok, true, 'conservation holds with the train read-out present');
});
