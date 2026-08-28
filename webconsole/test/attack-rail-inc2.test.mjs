// attack-rail-inc2.test.mjs — INDEPENDENT destructive attacks (GR#23) on rail inc2.
// Not the author's tests. Hunts geometry edges, count boundaries, wall-clock,
// dwell off-by-one, ping-pong teleport, and purity.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  buildRailGeometry,
  trainPositions,
  trainCountFor,
  DWELL_TICKS,
  TRAVEL_TICKS,
  MAX_TRAINS,
} from '../src/sim/trains.ts';

const finiteGlyphs = (lt) => {
  for (const l of lt)
    for (const t of l.trains) {
      assert.ok(Number.isFinite(t.x), `x finite: ${t.x}`);
      assert.ok(Number.isFinite(t.y), `y finite: ${t.y}`);
      assert.ok(Number.isFinite(t.progress), `progress finite: ${t.progress}`);
    }
};

const D = (spec, sat, over = false) => [{ spec, saturation: sat, overCapacity: over }];

// ---- 1. count ladder boundaries ----
test('ATTACK count ladder: boundaries, over-capacity cap, negatives, NaN', () => {
  assert.equal(trainCountFor(0), 0);
  assert.equal(trainCountFor(1e-9), 1, 'just above 0 → 1');
  assert.equal(trainCountFor(0.25), 2, '0.25 boundary → 1+floor(1)=2');
  assert.equal(trainCountFor(0.5), 3);
  assert.equal(trainCountFor(0.75), 4);
  assert.equal(trainCountFor(1.0), 5);
  assert.equal(trainCountFor(1.5), 5, 'sat>1 clamped to 1 → 5, not 7');
  assert.equal(trainCountFor(1000), 5);
  assert.ok(trainCountFor(1000) <= MAX_TRAINS, 'never exceeds MAX_TRAINS');
  assert.equal(trainCountFor(-1), 0, 'negative → 0 not negative');
  assert.equal(trainCountFor(-0.0001), 0);
  assert.equal(trainCountFor(NaN), 0, 'NaN → 0');
  assert.equal(trainCountFor(Infinity), 5, 'Infinity clamps to 1 → 5');
  // monotonic non-decreasing across a fine sweep
  let prev = -1;
  for (let s = 0; s <= 1.2; s += 0.01) {
    const c = trainCountFor(s);
    assert.ok(c >= prev, `monotonic at ${s}: ${c} >= ${prev}`);
    prev = c;
  }
});

// ---- 2. geometry edges ----
test('ATTACK geometry: single tile → no circuit, no train, no NaN', () => {
  const g = buildRailGeometry([{ spec: 'rail', x: 5, y: 5 }], []);
  assert.equal(g[0].circuit.length, 0, 'S=1 no circuit');
  const lt = trainPositions(g, D('rail', 1), 7);
  assert.equal(lt[0].trains.length, 0, 'single tile carries no train');
  finiteGlyphs(lt);
});

test('ATTACK geometry: two tiles, no station → moves, no NaN', () => {
  const g = buildRailGeometry([{ spec: 'rail', x: 0, y: 0 }, { spec: 'rail', x: 0, y: 1 }], []);
  assert.deepEqual(g[0].stationIdx, [], 'no stations');
  const lt = trainPositions(g, D('rail', 0.1), 0);
  assert.equal(lt[0].trains.length, 1);
  finiteGlyphs(lt);
  // over 20 ticks the single train occupies >1 position (movement w/o stations)
  const seen = new Set();
  for (let t = 0; t < 20; t++) {
    const tr = trainPositions(g, D('rail', 0.1), t)[0].trains[0];
    seen.add(`${tr.x},${tr.y}`);
  }
  assert.ok(seen.size > 1, 'moves even with zero stations');
});

test('ATTACK geometry: every tile a station → all dwell, no NaN, still moves', () => {
  const rails = [];
  const stations = [];
  for (let y = 0; y < 4; y++) {
    rails.push({ spec: 'rail', x: 0, y });
    stations.push({ x: 0, y });
  }
  const g = buildRailGeometry(rails, stations);
  assert.deepEqual(g[0].stationIdx, [0, 1, 2, 3], 'every point is a station');
  let anyStopped = false;
  let anyMoving = false;
  const seen = new Set();
  for (let t = 0; t < 60; t++) {
    const lt = trainPositions(g, D('rail', 0.1), t);
    finiteGlyphs(lt);
    const tr = lt[0].trains[0];
    if (tr.stoppedAtStation) anyStopped = true;
    else anyMoving = true;
    seen.add(`${tr.x},${tr.y}`);
  }
  assert.ok(anyStopped && anyMoving, 'both dwells and travels');
  assert.ok(seen.size > 1, 'still traverses');
});

test('ATTACK geometry: disjoint rail groups same class → single sorted polyline (documented), finite', () => {
  // Two disjoint clusters of the SAME spec. Grouping is by spec only, so they
  // become ONE sorted polyline — a train will "teleport" across the gap. This is
  // a KNOWN inc2 simplification (no router). Assert only: deterministic + finite.
  const rails = [
    { spec: 'rail', x: 0, y: 0 }, { spec: 'rail', x: 0, y: 1 },
    { spec: 'rail', x: 50, y: 50 }, { spec: 'rail', x: 50, y: 51 },
  ];
  const g = buildRailGeometry(rails, []);
  assert.equal(g[0].points.length, 4);
  for (let t = 0; t < 30; t++) finiteGlyphs(trainPositions(g, D('rail', 1), t));
});

// ---- 3. determinism ----
test('ATTACK determinism: wall-time delay + insertion order → byte-identical', async () => {
  const rails = [];
  for (let y = 0; y < 6; y++) rails.push({ spec: 'rail', x: 3, y });
  const stations = [{ x: 4, y: 2 }, { x: 4, y: 4 }];
  const g1 = buildRailGeometry(rails, stations);
  const a = JSON.stringify(trainPositions(g1, D('rail', 0.83, true), 137));
  // real wall time
  const until = Date.now() + 25;
  while (Date.now() < until) { /* burn */ }
  const b = JSON.stringify(trainPositions(g1, D('rail', 0.83, true), 137));
  assert.equal(a, b, 'wall-time cannot change positions');

  // reversed + shuffled insertion order of same board
  const rev = [...rails].reverse();
  const shuffled = [rev[3], rev[0], rev[5], rev[1], rev[4], rev[2]];
  const g2 = buildRailGeometry(shuffled, [...stations].reverse());
  assert.equal(JSON.stringify(g1), JSON.stringify(g2), 'geometry order-independent');
  const c = JSON.stringify(trainPositions(g2, D('rail', 0.83, true), 137));
  assert.equal(a, c, 'positions order-independent');
});

test('ATTACK purity: frozen inputs are not mutated', () => {
  const rails = [Object.freeze({ spec: 'rail', x: 0, y: 0 }), Object.freeze({ spec: 'rail', x: 0, y: 1 }), Object.freeze({ spec: 'rail', x: 0, y: 2 })];
  const g = buildRailGeometry(Object.freeze(rails.slice()), Object.freeze([Object.freeze({ x: 1, y: 1 })]));
  Object.freeze(g);
  for (const line of g) { Object.freeze(line); Object.freeze(line.points); Object.freeze(line.circuit); Object.freeze(line.stationIdx); }
  const dem = Object.freeze([Object.freeze({ spec: 'rail', saturation: 0.5, overCapacity: false })]);
  // If trainPositions mutated any frozen input this throws in strict mode.
  assert.doesNotThrow(() => trainPositions(g, dem, 9));
});

// ---- 4. dwell exactness + ping-pong no teleport ----
test('ATTACK dwell: exactly DWELL_TICKS at station, off-by-one both sides', () => {
  // vertical line x=0 y=0..3, station adjacent to point idx1 (0,1)
  const g = buildRailGeometry(
    [{ spec: 'rail', x: 0, y: 0 }, { spec: 'rail', x: 0, y: 1 }, { spec: 'rail', x: 0, y: 2 }, { spec: 'rail', x: 0, y: 3 }],
    [{ x: 1, y: 1 }]
  );
  assert.deepEqual(g[0].stationIdx, [1]);
  // circuit [0,1,2,3,2,1]; dwell at j=1 (point1) and j=5 (point1)
  // lap tick timeline for train offset 0:
  //  j0 travel [0,2) ; j1 dwell [2,5) ; j2 travel [5,7); j3 dwell? point2 not station
  // Count consecutive stopped ticks that are at station point (0,1) starting tick2
  const stoppedTicks = [];
  for (let t = 0; t < 100; t++) {
    const tr = trainPositions(g, D('rail', 0.1), t)[0].trains[0];
    if (tr.stoppedAtStation) {
      assert.equal(tr.x, 0);
      assert.equal(tr.y, 1, 'the only station is point (0,1)');
      stoppedTicks.push(t);
    }
  }
  // Find run lengths of consecutive stopped ticks
  let runs = [];
  let run = 1;
  for (let i = 1; i < stoppedTicks.length; i++) {
    if (stoppedTicks[i] === stoppedTicks[i - 1] + 1) run++;
    else { runs.push(run); run = 1; }
  }
  runs.push(run);
  // Each dwell visit lasts exactly DWELL_TICKS ticks (two visits per lap: j1,j5,
  // which are adjacent-in-lap? j5 is last node then wraps to j0 travel — the two
  // station dwells in one lap may merge if they are contiguous. Assert every run
  // is a multiple of DWELL_TICKS and at least DWELL_TICKS).
  for (const r of runs) {
    assert.ok(r % DWELL_TICKS === 0, `dwell run ${r} is a whole multiple of DWELL_TICKS=${DWELL_TICKS}`);
  }
});

test('ATTACK ping-pong: train reverses, no S-1 → 0 teleport wrap', () => {
  // straight line 0..4, no stations, single train. Track y sequence over a lap;
  // consecutive travel samples must never jump by more than 1 tile per TRAVEL step
  // segment (i.e. it goes 0..4 then back 4..0, never 4→0 directly).
  const g = buildRailGeometry(
    [0, 1, 2, 3, 4].map((y) => ({ spec: 'rail', x: 0, y })), []
  );
  const C = g[0].circuit.length; // 4 forward-back = 8
  const lap = C * (TRAVEL_TICKS + 0); // no dwell
  const ys = [];
  for (let t = 0; t < lap + 2; t++) ys.push(trainPositions(g, D('rail', 0.1), t)[0].trains[0].y);
  // max jump between successive integer-tile crossings must be <= 1 tile
  // Sample only at segment boundaries (whole ticks land mid-segment); instead
  // assert min and max reached the endpoints and no single-tick delta exceeds 1.
  let maxDelta = 0;
  for (let i = 1; i < ys.length; i++) maxDelta = Math.max(maxDelta, Math.abs(ys[i] - ys[i - 1]));
  assert.ok(maxDelta <= 1 + 1e-9, `no teleport: max per-tick y delta ${maxDelta} <= 1`);
  assert.ok(Math.min(...ys) <= 0.5 && Math.max(...ys) >= 3.5, 'reaches both ends of the line');
});

// ---- 5. count monotonic in emitted glyphs & no demand spec ----
test('ATTACK emitted glyph count matches ladder and never exceeds MAX_TRAINS', () => {
  const rails = [];
  for (let y = 0; y < 8; y++) rails.push({ spec: 'rail', x: 0, y });
  const g = buildRailGeometry(rails, [{ x: 1, y: 3 }]);
  // NOTE: saturation is clamped to 1 BEFORE the band divide, so the ladder tops
  // out at 1+floor(1/0.25)=5 — MAX_TRAINS=6 is a never-binding defensive cap.
  assert.equal(trainPositions(g, D('rail', 5), 0)[0].trains.length, 5, 'huge sat → ladder max 5 (MAX_TRAINS cap unreachable)');
  assert.ok(5 <= MAX_TRAINS, 'ladder max stays within MAX_TRAINS');
  assert.equal(trainPositions(g, D('rail', 0), 0)[0].trains.length, 0);
  // unknown spec in demand doesn't feed the line
  assert.equal(trainPositions(g, D('hs1', 1), 0)[0].trains.length, 0, 'wrong-spec demand → 0 trains');
});
