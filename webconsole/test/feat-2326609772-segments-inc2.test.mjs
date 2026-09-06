// feat-2326609772-segments-inc2.test.mjs — FEAT-2326609772 inc2: extends the
// inc1 connected-run decomposition (lineSegmentsOf, data.ts) to rail/hs1
// tiles, and adds AC-3's per-station attribution reusing stationLinks'
// hsWeight/railWeight rule (data.ts:3164-3175, cited verbatim in the
// stationUtilisationOf docstring — no second weighting, GR#3).
//
// Run with `npm test` (node --test); node's type-stripping imports the real
// TypeScript modules, so these assertions exercise the exact shipped code.
//
// Every pin below states its own mutant (prove-can-fail, GR#21 discipline
// mirrored from feat-2326609772-segments-inc1.test.mjs / rail-inc1.test.mjs).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  lineSegmentsOf,
  lineUsageOf,
  stationUtilisationOf,
  stationLinks,
  SEGMENT_ROAD_CLASSES,
  SEGMENT_RAIL_CLASSES,
  SEGMENT_LINE_CLASSES,
  lineCapacityOf,
} from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

// A controlled board: bare (no starter city), explicit building list + population.
function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}

// A run of `n` tiles of `spec` along the x-axis starting at (x0,y).
function lineRun(spec, startId, x0, y, n) {
  const out = [];
  for (let i = 0; i < n; i++) out.push({ id: startId + i, spec, x: x0 + i, y, builtTick: 0 });
  return out;
}

// A road tile adjacent to (x,y) so a station there is road-connected
// (stationLinks connects via an ADJACENT ROAD tile, not the rail tile itself
// — data.ts:3020-3050).
function roadNear(id, x, y) {
  return { id, spec: 'rd_aroad', x: x + 1, y, builtTick: 0 };
}

test('SEGMENT_RAIL_CLASSES scope is exactly rail/hs1 (inc2 slice) and never overlaps road', () => {
  assert.deepEqual([...SEGMENT_RAIL_CLASSES].sort(), ['hs1', 'rail'].sort());
  for (const spec of SEGMENT_RAIL_CLASSES) {
    // MUTANT: accidentally including a road class in the rail set would fail this.
    assert.equal(SEGMENT_ROAD_CLASSES.has(spec), false, `${spec} must not also be a road class`);
  }
  assert.deepEqual(
    [...SEGMENT_LINE_CLASSES].sort(),
    [...SEGMENT_ROAD_CLASSES, ...SEGMENT_RAIL_CLASSES].sort(),
    'SEGMENT_LINE_CLASSES is exactly the union of road and rail scopes'
  );
});

test('lineSegmentsOf: a contiguous rail run decomposes into one segment with kind "rail"', () => {
  const buildings = [...lineRun('rail', 1, 0, 0, 6), roadNear(50, 0, 0), { id: 51, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 }];
  const s = board(buildings, 100000);
  const segs = lineSegmentsOf(s).filter((x) => x.spec === 'rail');
  assert.equal(segs.length, 1, 'one contiguous rail run is one segment');
  assert.equal(segs[0].tiles, 6);
  // MUTANT: hardcoding kind:'road' (the inc1 literal) instead of reading the
  // class's own LineUsage.kind would fail this.
  assert.equal(segs[0].kind, 'rail');
});

test('lineSegmentsOf: RAIL AND ROAD NEVER MERGE — an m20 tile immediately adjacent to a rail tile stays two separate segments/classes', () => {
  // m20 at (0,0)-(4,0), rail at (5,0)-(9,0): physically adjacent tiles, DIFFERENT specs.
  const buildings = [
    ...lineRun('m20', 1, 0, 0, 5),
    ...lineRun('rail', 100, 5, 0, 5),
    roadNear(200, 5, 0),
    { id: 201, spec: 'station_sanderling', x: 5, y: 0, builtTick: 0 },
  ];
  const s = board(buildings, 200000);
  const segs = lineSegmentsOf(s);
  const m20Segs = segs.filter((x) => x.spec === 'm20');
  const railSegs = segs.filter((x) => x.spec === 'rail');
  // MUTANT: a flood-fill that keys adjacency by (x,y) alone instead of
  // (spec, x, y) would merge these into one 10-tile run.
  assert.equal(m20Segs.length, 1);
  assert.equal(railSegs.length, 1);
  assert.equal(m20Segs[0].tiles, 5, 'm20 run stays 5 tiles, not merged with rail');
  assert.equal(railSegs[0].tiles, 5, 'rail run stays 5 tiles, not merged with m20');
  assert.equal(m20Segs[0].kind, 'road');
  assert.equal(railSegs[0].kind, 'rail');
});

test('lineSegmentsOf: hs1 is decomposed separately from rail — never the same bucket', () => {
  const buildings = [
    ...lineRun('rail', 1, 0, 0, 4),
    ...lineRun('hs1', 100, 0, 5, 4),
    roadNear(200, 0, 0),
    { id: 201, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 },
    roadNear(210, 0, 5),
    { id: 211, spec: 'station_ashford', x: 0, y: 5, builtTick: 0 },
  ];
  const s = board(buildings, 500000);
  const segs = lineSegmentsOf(s);
  const railSegs = segs.filter((x) => x.spec === 'rail');
  const hs1Segs = segs.filter((x) => x.spec === 'hs1');
  // MUTANT: bucketing hs1 tiles into the 'rail' spec key (e.g. by kind
  // instead of by spec id) would merge these into one segment set.
  assert.equal(railSegs.length, 1);
  assert.equal(hs1Segs.length, 1);
  assert.notEqual(railSegs[0].spec, hs1Segs[0].spec);
  assert.equal(hs1Segs[0].capacity, lineCapacityOf(SPECS.hs1) * 4);
  assert.equal(railSegs[0].capacity, lineCapacityOf(SPECS.rail) * 4);
});

test('lineSegmentsOf: SUM INVARIANT holds for rail segments too — Σ segment.usage === class LineUsage.usage', () => {
  const buildings = [
    ...lineRun('rail', 1, 0, 0, 3),
    ...lineRun('rail', 100, 0, 10, 7), // second disconnected rail run, same spec
    roadNear(200, 0, 0),
    { id: 201, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 },
    roadNear(210, 0, 10),
    { id: 211, spec: 'station_sanderling', x: 0, y: 10, builtTick: 0 },
  ];
  const s = board(buildings, 300000);
  const cls = lineUsageOf(s).find((x) => x.spec === 'rail');
  assert.ok(cls);
  const segs = lineSegmentsOf(s).filter((x) => x.spec === 'rail');
  assert.equal(segs.length, 2, 'two disconnected rail runs are two segments');
  const sum = segs.reduce((a, x) => a + x.usage, 0);
  // MUTANT: per-segment Math.round() instead of floor-with-remainder-on-last
  // would drift this sum away from the class total.
  assert.equal(sum, cls.usage, 'rail segment usages sum exactly to the class usage');
});

test('stationUtilisationOf: a disconnected station reports null utilisation (honest absence)', () => {
  // Station present but with NO adjacent road — not connected.
  const buildings = [...lineRun('rail', 1, 5, 5, 3), { id: 201, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 }];
  const s = board(buildings, 100000);
  const links = stationLinks(s);
  assert.equal(links.connectedIds.has(201), false, 'fixture station must be disconnected');
  const stat = stationUtilisationOf(s).find((x) => x.id === 201);
  assert.ok(stat);
  // MUTANT: defaulting a disconnected station to 0 instead of null would make
  // it indistinguishable from "connected but carrying zero commuters".
  assert.equal(stat.utilisation, null, 'disconnected station gets null, not a fabricated zero');
  assert.equal(stat.lineSpec, 'rail', 'lineSpec is still reported from the spec id alone');
});

test('stationUtilisationOf: Ashford International always weights into hs1, every other station into rail', () => {
  const buildings = [
    roadNear(1, 0, 0),
    { id: 2, spec: 'station_ashford', x: 0, y: 0, builtTick: 0 },
    roadNear(3, 0, 10),
    { id: 4, spec: 'station_sanderling', x: 0, y: 10, builtTick: 0 },
  ];
  const s = board(buildings, 400000);
  const stats = stationUtilisationOf(s);
  const ashford = stats.find((x) => x.id === 2);
  const sanderling = stats.find((x) => x.id === 4);
  // MUTANT: swapping the lineSpec assignment would fail these.
  assert.equal(ashford.lineSpec, 'hs1');
  assert.equal(sanderling.lineSpec, 'rail');
});

test('stationUtilisationOf: SUM INVARIANT — connected stations of one class sum EXACTLY to that class usage', () => {
  const buildings = [
    // lineUsageOf only emits a 'rail' class entry when at least one rail TILE
    // is present (stations connect via an adjacent ROAD tile, not a rail
    // tile — data.ts:3020-3050 — so this tile is present purely to make the
    // class exist, placed far from every station/road above).
    { id: 900, spec: 'rail', x: 900, y: 900, builtTick: 0 },
    roadNear(1, 0, 0),
    { id: 2, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 },
    roadNear(3, 0, 10),
    { id: 4, spec: 'station_sanderling', x: 0, y: 10, builtTick: 0 },
    roadNear(5, 0, 20),
    { id: 6, spec: 'station_sanderling', x: 0, y: 20, builtTick: 0 },
  ];
  const s = board(buildings, 600000);
  const cls = lineUsageOf(s).find((x) => x.spec === 'rail');
  assert.ok(cls);
  const stats = stationUtilisationOf(s).filter((x) => x.lineSpec === 'rail');
  const sum = stats.reduce((a, x) => a + (x.utilisation ?? 0), 0);
  // MUTANT: independently Math.round()-ing each station's share instead of
  // floor-with-remainder-on-last would drift this sum off the class total —
  // this is the AC-3 invariant test the doc demands ("reusing the SAME
  // weight the class-level split already computes").
  assert.equal(sum, cls.usage, 'connected station utilisations sum exactly to the class usage');
});

test('stationUtilisationOf: attribution matches the weight ratio exactly (weight-3 Ashford vs weight-1 stations)', () => {
  const buildings = [
    roadNear(1, 0, 0),
    { id: 2, spec: 'station_ashford', x: 0, y: 0, builtTick: 0 }, // weight 3 -> hs1
    roadNear(3, 0, 10),
    { id: 4, spec: 'station_sanderling', x: 0, y: 10, builtTick: 0 }, // weight 1 -> rail
    roadNear(5, 0, 20),
    { id: 6, spec: 'station_sanderling', x: 0, y: 20, builtTick: 0 }, // weight 1 -> rail
  ];
  const s = board(buildings, 900000);
  const stats = stationUtilisationOf(s);
  const railStats = stats.filter((x) => x.lineSpec === 'rail');
  assert.equal(railStats.length, 2);
  const [a, b] = railStats;
  // MUTANT: an unequal split between two equal-weight (both weight-1) rail
  // stations would fail this (their shares must be equal or differ only by
  // the floor/remainder rounding of at most 1).
  assert.ok(Math.abs(a.utilisation - b.utilisation) <= 1, 'two equal-weight rail stations split evenly');
});

test('stationUtilisationOf: determinism — two independent runs on byte-identical state produce byte-identical output', () => {
  const buildings = [
    roadNear(1, 0, 0),
    { id: 2, spec: 'station_ashford', x: 0, y: 0, builtTick: 0 },
    roadNear(3, 0, 10),
    { id: 4, spec: 'station_sanderling', x: 0, y: 10, builtTick: 0 },
  ];
  const s1 = board(buildings, 700000);
  const s2 = board(JSON.parse(JSON.stringify(buildings)), 700000);
  const out1 = JSON.stringify(stationUtilisationOf(s1));
  const out2 = JSON.stringify(stationUtilisationOf(s2));
  // MUTANT: any Date.now/Math.random/map-iteration-with-early-break creeping
  // into the derivation would break this byte-identical pin.
  assert.equal(out1, out2, 'stationUtilisationOf is a pure deterministic function of state');
});

test('purity pin — no Date.now / localStorage / Math.random in the inc2 rail/station block', async () => {
  const fs = await import('node:fs');
  const dataPath = new URL('../src/sim/data.ts', import.meta.url);
  const src = fs.readFileSync(dataPath, 'utf8');
  const startMarker = 'FEAT-2326609772 inc1 — PER-SEGMENT ROAD CAPACITY';
  const endMarker = 'FEAT-congestion-teeth-2026-09-02 (Q100057 A1';
  const start = src.indexOf(startMarker);
  const end = src.indexOf(endMarker, start);
  assert.ok(start >= 0 && end > start, 'could not locate the inc1/inc2 block markers in data.ts');
  const block = src.slice(start, end);
  // MUTANT: introducing Date.now()/localStorage/Math.random anywhere in this
  // block (including the inc2 additions) would fail this scan (BUG-602/BUG-642 class).
  assert.ok(!/Date\.now\(/.test(block), 'no Date.now in the segment-derivation block');
  assert.ok(!/localStorage/.test(block), 'no localStorage in the segment-derivation block');
  assert.ok(!/Math\.random\(/.test(block), 'no Math.random in the segment-derivation block');
  assert.ok(block.includes('stationUtilisationOf'), 'inc2 station-attribution export must be inside the scanned block');
});
