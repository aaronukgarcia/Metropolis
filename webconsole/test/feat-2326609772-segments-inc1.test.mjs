// feat-2326609772-segments-inc1.test.mjs — FEAT-2326609772 inc1: per-segment
// road capacity/utilisation. Read-only decomposition of lineUsageOf()'s
// already-computed per-class usage across contiguous same-class road runs
// (AC-1/AC-2). Scope: rd_aroad/rd_dual/m20 only (doc §8 inc1 slice).
//
// Run with `npm test` (node --test); node's type-stripping imports the real
// TypeScript modules, so these assertions exercise the exact shipped code.
//
// Every pin below states its own mutant (prove-can-fail, GR#21 discipline
// mirrored from rail-inc1.test.mjs).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  lineSegmentsOf,
  lineUsageOf,
  SEGMENT_ROAD_CLASSES,
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

// A run of `n` m20 tiles along the x-axis starting at (x0,y).
function motorwayRun(startId, x0, y, n) {
  const out = [];
  for (let i = 0; i < n; i++) out.push({ id: startId + i, spec: 'm20', x: x0 + i, y, builtTick: 0 });
  return out;
}

// Enough traffic-generating buildings + population to make lineUsageOf produce
// non-zero usage for m20 (feederTrafficWeight/trafficActivity are read-only
// inputs this test does not re-derive — it just needs usage > 0 to exercise
// apportionment; the exact traffic formula is lineUsageOf's own contract,
// pinned by rail-inc1.test.mjs / congestion-teeth.test.mjs already).
function withDemand(buildings, population) {
  return [...buildings, { id: 9000, spec: 'res_hut', x: 0, y: 0, builtTick: 0 }];
}

test('SEGMENT_ROAD_CLASSES scope is exactly rd_aroad/rd_dual/m20 (inc1 slice)', () => {
  assert.deepEqual(
    [...SEGMENT_ROAD_CLASSES].sort(),
    ['m20', 'rd_aroad', 'rd_dual'].sort(),
    'inc1 covers only the classes named in doc §8'
  );
  // MUTANT: adding 'road' (tier-1 lane) to scope would fail this pin.
  assert.equal(SEGMENT_ROAD_CLASSES.has('road'), false, 'tier-1 lane is NOT in inc1 scope');
  assert.equal(SEGMENT_ROAD_CLASSES.has('rail'), false, 'rail is NOT in inc1 scope (inc2)');
});

test('lineSegmentsOf: two physically separate m20 runs of the same class are two segments', () => {
  const buildings = withDemand(
    [...motorwayRun(1, 0, 0, 5), ...motorwayRun(100, 20, 0, 3)],
    50000
  );
  const s = board(buildings, 50000);
  const segs = lineSegmentsOf(s).filter((x) => x.spec === 'm20');
  // MUTANT: a flood-fill bug that treats all same-spec tiles as one run
  // (ignoring adjacency) would collapse this to length 1.
  assert.equal(segs.length, 2, 'two disconnected runs produce two segments');
  const tileCounts = segs.map((x) => x.tiles).sort((a, b) => a - b);
  assert.deepEqual(tileCounts, [3, 5], 'segment tile counts match the two runs');
});

test('lineSegmentsOf: one contiguous m20 run is a single segment, not one-per-tile', () => {
  const buildings = withDemand(motorwayRun(1, 0, 0, 10), 50000);
  const s = board(buildings, 50000);
  const segs = lineSegmentsOf(s).filter((x) => x.spec === 'm20');
  // MUTANT: per-tile aggregation (AC-1's explicitly rejected granularity)
  // would produce 10 segments instead of 1.
  assert.equal(segs.length, 1, 'one contiguous run is one segment');
  assert.equal(segs[0].tiles, 10);
});

test('lineSegmentsOf: capacity is exactly lineCapacityOf(spec) x tiles, no new constant', () => {
  const buildings = withDemand(motorwayRun(1, 0, 0, 7), 50000);
  const s = board(buildings, 50000);
  const seg = lineSegmentsOf(s).find((x) => x.spec === 'm20');
  assert.ok(seg);
  // MUTANT: hardcoding a different per-tile figure instead of reusing
  // lineCapacityOf/ROAD_TIER_CAPACITY would fail this exact-value pin.
  assert.equal(seg.capacity, lineCapacityOf(SPECS.m20) * 7, 'capacity = per-tile x tiles');
  assert.equal(seg.capacity, 2500 * 7);
});

test('lineSegmentsOf: SUM INVARIANT — segment usages for a class sum EXACTLY to the class LineUsage.usage (AC-2)', () => {
  // Two disconnected m20 runs of different lengths so apportionment is non-trivial.
  const buildings = withDemand(
    [...motorwayRun(1, 0, 0, 4), ...motorwayRun(100, 50, 0, 9)],
    250000
  );
  const s = board(buildings, 250000);
  const classUsage = lineUsageOf(s).find((x) => x.spec === 'm20');
  assert.ok(classUsage, 'm20 must have a class-level usage entry');
  const segs = lineSegmentsOf(s).filter((x) => x.spec === 'm20');
  const sum = segs.reduce((a, x) => a + x.usage, 0);
  // MUTANT: independently Math.round()-ing each segment's share (instead of
  // floor-with-remainder-on-last) would drift the sum away from the class
  // total by rounding error — this is the AC-2 invariant test the doc demands.
  assert.equal(sum, classUsage.usage, 'segment usages sum exactly to the class usage');
});

test('lineSegmentsOf: apportionment is by capacity share (bigger run gets proportionally more usage)', () => {
  const buildings = withDemand(
    [...motorwayRun(1, 0, 0, 2), ...motorwayRun(100, 50, 0, 8)],
    300000
  );
  const s = board(buildings, 300000);
  const segs = lineSegmentsOf(s).filter((x) => x.spec === 'm20').sort((a, b) => a.tiles - b.tiles);
  const [small, big] = segs;
  assert.equal(small.tiles, 2);
  assert.equal(big.tiles, 8);
  // MUTANT: an equal-split (usage / segment count) instead of capacity-share
  // apportionment would give both segments equal usage regardless of size.
  assert.ok(big.usage >= small.usage * 3, 'the 8-tile run carries a proportionally larger share than the 2-tile run');
});

test('lineSegmentsOf: saturation/headroom/overCapacity are derived consistently from usage/capacity', () => {
  const buildings = withDemand(motorwayRun(1, 0, 0, 20), 5); // tiny population -> near-zero traffic -> low usage
  const s = board(buildings, 5);
  const seg = lineSegmentsOf(s).find((x) => x.spec === 'm20');
  assert.ok(seg);
  const expectedSat = seg.capacity > 0 ? Math.min(1, Math.max(0, seg.usage / seg.capacity)) : 0;
  // MUTANT: computing saturation from the CLASS capacity instead of the
  // segment's own capacity would desync this from usage/capacity.
  assert.equal(seg.saturation, expectedSat);
  assert.equal(seg.headroom, seg.capacity - seg.usage);
  assert.equal(seg.overCapacity, seg.headroom < 0);
});

test('lineSegmentsOf: segmentId is stable across independent re-derivation of the SAME tiles', () => {
  const buildings = withDemand(motorwayRun(1, 0, 0, 6), 50000);
  const s1 = board(buildings, 50000);
  // A structurally-different state object with the SAME m20 tiles (different
  // building array identity / order / unrelated field) must yield the SAME
  // segmentId — AC-8's forward-compat requirement (graph-position-derived,
  // not array-index-derived).
  const shuffled = [...buildings].reverse();
  const s2 = board(shuffled, 50000);
  const id1 = lineSegmentsOf(s1).find((x) => x.spec === 'm20').segmentId;
  const id2 = lineSegmentsOf(s2).find((x) => x.spec === 'm20').segmentId;
  // MUTANT: keying segmentId off array index/insertion order instead of the
  // sorted tile chain would make this test flake/fail under reordering.
  assert.equal(id1, id2, 'segmentId is derived from tile positions, not array order');
});

test('lineSegmentsOf: segmentId differs for two genuinely different segments of the same class', () => {
  const buildings = withDemand(
    [...motorwayRun(1, 0, 0, 5), ...motorwayRun(100, 90, 90, 5)],
    50000
  );
  const s = board(buildings, 50000);
  const segs = lineSegmentsOf(s).filter((x) => x.spec === 'm20');
  assert.equal(segs.length, 2);
  // MUTANT: a hash collision / constant-key bug would make both segments
  // report the same segmentId.
  assert.notEqual(segs[0].segmentId, segs[1].segmentId);
});

test('lineSegmentsOf: determinism — two independent runs on byte-identical state produce byte-identical output', () => {
  const buildings = withDemand(
    [...motorwayRun(1, 0, 0, 5), ...motorwayRun(100, 20, 3, 3), ...motorwayRun(200, 40, 7, 9)],
    500000
  );
  const s1 = board(buildings, 500000);
  const s2 = board(JSON.parse(JSON.stringify(buildings)), 500000);
  const out1 = JSON.stringify(lineSegmentsOf(s1));
  const out2 = JSON.stringify(lineSegmentsOf(s2));
  // MUTANT: any Date.now/Math.random/map-iteration-with-early-break creeping
  // into the derivation would break this byte-identical pin.
  assert.equal(out1, out2, 'lineSegmentsOf is a pure deterministic function of state');
});

test('purity pin — no Date.now / localStorage / Math.random in the segment-derivation source', async () => {
  const fs = await import('node:fs');
  const path = await import('node:url');
  const dataPath = new URL('../src/sim/data.ts', import.meta.url);
  const src = fs.readFileSync(dataPath, 'utf8');
  // Scope the scan to the FEAT-2326609772 inc1 block only (the file has many
  // other functions; this test is about the code THIS item adds).
  const startMarker = 'FEAT-2326609772 inc1 — PER-SEGMENT ROAD CAPACITY';
  const endMarker = 'FEAT-congestion-teeth-2026-09-02 (Q100057 A1';
  const start = src.indexOf(startMarker);
  const end = src.indexOf(endMarker, start);
  assert.ok(start >= 0 && end > start, 'could not locate the inc1 block markers in data.ts');
  const block = src.slice(start, end);
  // MUTANT: introducing Date.now()/localStorage/Math.random anywhere in this
  // block would fail this scan (BUG-602/BUG-642 class).
  assert.ok(!/Date\.now\(/.test(block), 'no Date.now in the segment-derivation block');
  assert.ok(!/localStorage/.test(block), 'no localStorage in the segment-derivation block');
  assert.ok(!/Math\.random\(/.test(block), 'no Math.random in the segment-derivation block');
});
