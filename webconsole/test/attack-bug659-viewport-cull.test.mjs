// attack-bug659-viewport-cull.test.mjs — BUG-659 P0: Aaron's live city
// (49,174 buildings / 3.2M population) stalled the map 6.1s per repaint —
// the third time in one day he could not play. The engine tick itself
// (147.8ms median) and every derivation MapView.tsx reads sum to well under
// 700ms on the same real savepoint, so the missing seconds were the RENDER
// path: MapView.tsx's draw effect made three unconditional full-array passes
// over `state.buildings` every repaint (main building loop, disconnected-
// road-flash pass, station-connectivity-dot pass), regardless of camera
// position — see src/render/viewportCull.ts's header for the full analysis.
//
// This file proves the FIX (src/render/viewportCull.ts) meets the
// non-negotiable correctness contract from the brief:
//
//   1. EXACT SET: visibleBuildingsOf returns exactly the buildings whose
//      real footprint intersects the viewport rect — never a superset
//      (proven by an exhaustive brute-force cross-check against every
//      building in a mixed-footprint fixture) and never a subset (the
//      failure mode that would actually clip something the player should
//      see, checked explicitly below).
//   2. GROWN FOOTPRINTS: a building whose ORIGIN sits outside the viewport
//      but whose grown footprint (footprintOf, FEAT-2326609740 auto-scale
//      ladder) overlaps it is still included — the margin used by the
//      spatial index is derived from the real data (GR#15), not a guessed
//      constant.
//   3. EDGE CASES: every map corner/edge, a 1x1 viewport, a viewport that
//      exactly touches a building's edge (must NOT include a building whose
//      footprint ends exactly at the viewport's start — half-open rect
//      convention, matching buildOccupiedSet's own [x,x+w) convention), and
//      a full-map viewport (must return every building, proving culling
//      never under-includes at zoom-to-fit).
//   4. DETERMINISM (GR#21): two independent calls with the same inputs
//      produce byte-identical (same ids, same order) results — no
//      Date.now/Math.random anywhere in the module (grep-checked below).
//
// RED PROOF (GR#24: scratch-copy, never git-revert): a scratch copy of
// viewportCull.ts with the box-overlap test's `<=`/`>=` bounds swapped to
// `<`/`>` (so a building exactly touching the viewport edge is wrongly
// included) was diffed in and the "exact edge, half-open" test below went
// red immediately (extra building present); restored from the scratch copy,
// never via git.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  visibleBuildingsOf,
  viewportTileRect,
  spatialIndexOf,
} from '../src/render/viewportCull.ts';
import { SPECS, footprintOf } from '../src/sim/data.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

test('GR#21: viewportCull.ts contains no Date.now/Math.random/localStorage (hot-path determinism)', () => {
  const src = fs.readFileSync(path.join(__dirname, '../src/render/viewportCull.ts'), 'utf8');
  assert.ok(!/Date\.now\(|Math\.random\(|localStorage\./.test(src));
});

// A small, hand-built fixture mixing 1x1, 2x2 (grown via footprintW/H) and a
// deliberately large grown footprint, spread across the whole map so every
// quadrant + edge is exercised. Uses a real single-tile spec ('res_hut') for
// every building so SPECS[b.spec] always resolves; footprintW/H override the
// drawn footprint exactly as the auto-scale ladder does at runtime.
function anySpecId() {
  const id = Object.keys(SPECS).find((k) => SPECS[k].w === 1 && SPECS[k].h === 1);
  assert.ok(id, 'fixture requires a real 1x1 spec in the catalogue');
  return id;
}

function buildMixedFixture() {
  const spec = anySpecId();
  const buildings = [];
  let id = 1;
  // Dense grid across the whole map so every cell of the spatial index has
  // occupants, plus a few buildings with grown (footprintW/H) footprints
  // near cell boundaries — the exact place an off-by-one margin bug would
  // first show up.
  for (let x = 0; x < 440; x += 7) {
    for (let y = 0; y < 260; y += 7) {
      buildings.push({ id: id++, spec, x, y });
    }
  }
  // Grown-footprint buildings straddling cell boundaries (CELL_SIZE=16).
  buildings.push({ id: id++, spec, x: 14, y: 14, footprintW: 6, footprintH: 6 }); // straddles (16,16) cell edge
  buildings.push({ id: id++, spec, x: 0, y: 0, footprintW: 20, footprintH: 20 }); // large, corner-anchored
  buildings.push({ id: id++, spec, x: 438, y: 258, footprintW: 1, footprintH: 1 }); // bottom-right corner, in bounds
  return buildings;
}

function bruteForceVisible(buildings, rect) {
  const out = [];
  for (const b of buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const { w, h } = footprintOf(b, sp);
    const bx1 = b.x + w;
    const by1 = b.y + h;
    if (bx1 <= rect.minX || b.x >= rect.maxX || by1 <= rect.minY || b.y >= rect.maxY) continue;
    out.push(b.id);
  }
  return out.sort((a, b) => a - b);
}

let buildings;
test('setup: build a mixed-footprint fixture across the whole map', () => {
  buildings = buildMixedFixture();
  assert.ok(buildings.length > 1000);
});

test('exact set: matches a brute-force O(n) scan for a viewport in the middle of the map', () => {
  const rect = { minX: 100, minY: 60, maxX: 160, maxY: 110 };
  const got = visibleBuildingsOf(buildings, rect)
    .map((b) => b.id)
    .sort((a, b) => a - b);
  const want = bruteForceVisible(buildings, rect);
  assert.deepEqual(got, want);
  assert.ok(got.length > 0 && got.length < buildings.length, 'sanity: a mid-map viewport neither empties nor drowns');
});

test('exact set: matches brute force at every map corner and edge', () => {
  const rects = [
    { minX: -5, minY: -5, maxX: 20, maxY: 20 }, // top-left corner, partly off-map
    { minX: 420, minY: -5, maxX: 460, maxY: 20 }, // top-right corner, partly off-map
    { minX: -5, minY: 240, maxX: 20, maxY: 280 }, // bottom-left corner, partly off-map
    { minX: 420, minY: 240, maxX: 460, maxY: 280 }, // bottom-right corner, partly off-map
    { minX: 0, minY: 0, maxX: 440, maxY: 260 }, // exactly the whole map
    { minX: 0, minY: 0, maxX: 1, maxY: 1 }, // a single tile
  ];
  for (const rect of rects) {
    const got = visibleBuildingsOf(buildings, rect)
      .map((b) => b.id)
      .sort((a, b) => a - b);
    const want = bruteForceVisible(buildings, rect);
    assert.deepEqual(got, want, `mismatch for rect ${JSON.stringify(rect)}`);
  }
});

test('full-map viewport returns EVERY building (never under-includes at zoom-to-fit)', () => {
  const rect = { minX: 0, minY: 0, maxX: 440, maxY: 260 };
  const got = visibleBuildingsOf(buildings, rect);
  assert.equal(got.length, buildings.length);
});

test('grown footprint straddling a cell boundary is included when the viewport touches only the grown part', () => {
  // Building 'straddler' sits at (14,14) with a 6x6 footprint -> occupies
  // [14,20) x [14,20), straddling the CELL_SIZE=16 boundary. A viewport that
  // only overlaps the far corner of its footprint (18-19, 18-19) must still
  // include it — this is exactly the "origin outside viewport, grown
  // footprint overlaps it" case the margin exists to catch.
  const rect = { minX: 18, minY: 18, maxX: 19, maxY: 19 };
  const got = visibleBuildingsOf(buildings, rect);
  const straddler = buildings.find((b) => b.x === 14 && b.y === 14 && b.footprintW === 6);
  assert.ok(got.some((b) => b.id === straddler.id), 'grown footprint must be caught even though its origin (14,14) is well outside the 1-tile viewport');
});

test('exact edge, half-open rect: a building whose footprint ends exactly at the viewport start is EXCLUDED', () => {
  // A 1x1 building at (10,10) occupies [10,11)x[10,11). A viewport starting
  // exactly at (11,11) must not include it (matches buildOccupiedSet's own
  // [x,x+w) half-open convention throughout data.ts).
  const spec = anySpecId();
  const probe = [{ id: 999001, spec, x: 10, y: 10 }];
  const rect = { minX: 11, minY: 11, maxX: 20, maxY: 20 };
  const got = visibleBuildingsOf(probe, rect);
  assert.equal(got.length, 0);
  // ...but a viewport that includes even one unit of overlap DOES catch it.
  const overlapping = { minX: 10.5, minY: 10.5, maxX: 20, maxY: 20 };
  assert.equal(visibleBuildingsOf(probe, overlapping).length, 1);
});

test('determinism (GR#21): repeated calls with the same inputs are byte-identical', () => {
  const rect = { minX: 50, minY: 50, maxX: 90, maxY: 90 };
  const a = visibleBuildingsOf(buildings, rect).map((b) => b.id);
  const b = visibleBuildingsOf(buildings, rect).map((b) => b.id);
  assert.deepEqual(a, b);
});

test('spatialIndexOf is memoised per buildings-array identity (WeakMap cache hit)', () => {
  const idx1 = spatialIndexOf(buildings);
  const idx2 = spatialIndexOf(buildings);
  assert.equal(idx1, idx2, 'same array reference must reuse the cached index, not rebucket');
  const idx3 = spatialIndexOf([...buildings]);
  assert.notEqual(idx3, idx1, 'a different array reference must NOT share the cache (stale-data hazard)');
});

test('viewportTileRect derives a sane rect from geom/size and pads it', () => {
  const geom = { ox: 0, oy: 0, s: 10 }; // 10px per tile
  const size = { w: 100, h: 50 }; // 10x5 tiles visible
  const rect = viewportTileRect(geom, size, 2);
  assert.equal(rect.minX, -2);
  assert.equal(rect.minY, -2);
  assert.equal(rect.maxX, 12);
  assert.equal(rect.maxY, 7);
});

test('viewportTileRect degrades to an empty rect when geom.s <= 0 (no crash on an unmeasured canvas)', () => {
  const rect = viewportTileRect({ ox: 0, oy: 0, s: 0 }, { w: 100, h: 50 });
  assert.deepEqual(rect, { minX: 0, minY: 0, maxX: 0, maxY: 0 });
});
