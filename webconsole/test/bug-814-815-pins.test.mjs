// bug-814-815-pins.test.mjs — pins for BUG-814 (stationUtilisationOf honest
// null when a CONNECTED station's class has no LineUsage entry yet) and
// BUG-815 (MapView's Lines overlay must read a memoised segmentById map
// instead of rebuilding `new Map(...)` every draw frame).
//
// Run with `npm test` (node --test); node's type-stripping imports the real
// TypeScript modules, so these assertions exercise the exact shipped code.
//
// Every pin below states its own mutant (prove-can-fail, GR#21 discipline).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  stationUtilisationOf,
  stationLinks,
  lineUsageOf,
  lineSegmentByIdOf,
  lineSegmentsOf,
} from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

// A controlled board: bare (no starter city), explicit building list + population.
function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}

// A road tile adjacent to (x,y) so a station there is road-connected
// (stationLinks connects via an ADJACENT ROAD tile, not the rail tile itself).
function roadNear(id, x, y) {
  return { id, spec: 'rd_aroad', x: x + 1, y, builtTick: 0 };
}

// A run of `n` tiles of `spec` along the x-axis starting at (x0,y).
function lineRun(spec, startId, x0, y, n) {
  const out = [];
  for (let i = 0; i < n; i++) out.push({ id: startId + i, spec, x: x0 + i, y, builtTick: 0 });
  return out;
}

test('BUG-814: a CONNECTED station whose class has NO rail tiles (no LineUsage entry) reports null, not 0', () => {
  // Station with an adjacent road (connected) but NOT ONE rail tile anywhere
  // on the map — lineUsageOf never emits a 'rail' class entry, so the class
  // has literally no usage basis yet.
  const buildings = [roadNear(1, 0, 0), { id: 2, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 }];
  const s = board(buildings, 100000);
  const links = stationLinks(s);
  assert.equal(links.connectedIds.has(2), true, 'fixture station must be road-connected');
  const railClass = lineUsageOf(s).find((x) => x.spec === 'rail');
  assert.equal(railClass, undefined, 'fixture must have no rail LineUsage entry (no rail tiles laid)');
  const stat = stationUtilisationOf(s).find((x) => x.id === 2);
  assert.ok(stat);
  // MUTANT: restoring `utilByStation.get(b.id) ?? 0` would report 0 here —
  // a fabricated number indistinguishable from "connected but genuinely
  // carrying zero commuters". The honest answer is null: no usage basis exists.
  assert.equal(stat.utilisation, null, 'connected station with no rail-class usage basis gets null, not a fabricated 0');
});

test('BUG-814: connected station WITH rail tiles still reports a real number (not null)', () => {
  const buildings = [
    ...lineRun('rail', 100, 0, 5, 3),
    roadNear(1, 0, 0),
    { id: 2, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 },
  ];
  const s = board(buildings, 100000);
  const links = stationLinks(s);
  assert.equal(links.connectedIds.has(2), true, 'fixture station must be road-connected');
  const railClass = lineUsageOf(s).find((x) => x.spec === 'rail');
  assert.ok(railClass, 'fixture must have a rail LineUsage entry (rail tiles laid)');
  const stat = stationUtilisationOf(s).find((x) => x.id === 2);
  assert.ok(stat);
  // MUTANT: a fix that always returns null regardless of usage basis would
  // fail this — connected-with-usage-basis must still produce a number.
  assert.equal(typeof stat.utilisation, 'number', 'connected station WITH a rail usage basis reports a number');
});

test('BUG-814: disconnected station still reports null (existing AC-3 behaviour, unchanged)', () => {
  const buildings = [...lineRun('rail', 1, 5, 5, 3), { id: 201, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 }];
  const s = board(buildings, 100000);
  const links = stationLinks(s);
  assert.equal(links.connectedIds.has(201), false, 'fixture station must be disconnected');
  const stat = stationUtilisationOf(s).find((x) => x.id === 201);
  assert.ok(stat);
  // MUTANT: making the disconnected path fall through to a number would
  // regress the pre-existing AC-3 disconnected->null guarantee.
  assert.equal(stat.utilisation, null, 'disconnected station still reports null');
});

test('BUG-815: lineSegmentByIdOf returns the SAME Map object identity for two calls on the same state', () => {
  const buildings = [...lineRun('m20', 1, 0, 0, 5)];
  const s = board(buildings, 100000);
  const first = lineSegmentByIdOf(s);
  const second = lineSegmentByIdOf(s);
  // MUTANT: rebuilding `new Map(lineSegmentsOf(state).map(...))` per call
  // (the pre-fix behaviour) would produce a NEW Map object every time,
  // failing this identity check.
  assert.equal(first, second, 'same state must return the identical memoised Map object, not a fresh allocation');
  assert.ok(first instanceof Map, 'lineSegmentByIdOf returns a Map');
  // Sanity: content matches lineSegmentsOf's own rows.
  for (const seg of lineSegmentsOf(s)) {
    assert.equal(first.get(seg.segmentId), seg, 'segmentById entries are the SAME segment objects lineSegmentsOf emits');
  }
});

test('BUG-815: MapView draw block contains no per-frame `new Map(` over lineSegmentsOf (source scan)', async () => {
  const fs = await import('node:fs');
  const mapViewPath = new URL('../src/components/MapView.tsx', import.meta.url);
  const src = fs.readFileSync(mapViewPath, 'utf8');
  const startMarker = 'if (showLines) {';
  const start = src.indexOf(startMarker);
  assert.ok(start >= 0, 'could not locate the Lines overlay draw block in MapView.tsx');
  // Scan just this draw block up to its closing brace at the same nesting
  // depth as the opening one (simple brace-depth walk — the block is a
  // single top-level `if` inside the draw effect).
  let depth = 0;
  let end = start;
  for (let i = start; i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}') {
      depth--;
      if (depth === 0) {
        end = i;
        break;
      }
    }
  }
  const block = src.slice(start, end);
  // MUTANT: reverting to `const segmentById = new Map(lineSegmentsOf(state)
  // .map(...))` inside this block would fail this scan.
  assert.ok(
    !/new Map\(\s*lineSegmentsOf/.test(block),
    'the Lines overlay draw block must not rebuild a Map over lineSegmentsOf per frame'
  );
  assert.ok(block.includes('lineSegmentByIdOf'), 'the draw block must read the memoised lineSegmentByIdOf instead');
});
