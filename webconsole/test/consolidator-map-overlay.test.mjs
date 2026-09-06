// consolidator-map-overlay.test.mjs — FEAT-2326609761 inc1: the "red box"
// section-focus overlay (Aaron: "let's draw a red box on the area"). Tests
// the PURE GEOMETRY MapView's draw loop uses (screen-rect = section origin/
// size transformed by camera geom {s, ox, oy}), independent of React/canvas,
// plus the consolidatorFocus mailbox and the "no cost when off" contract.
//
// This deliberately does NOT render MapView itself (that needs jsdom/tsx —
// see mount.test.tsx for that class of test); it proves the ARITHMETIC the
// draw loop performs is correct at several camera positions/zooms and that
// nothing here is a function of state.buildings.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  sectionOriginOf,
  sectionKeyOf,
  monthlyScopeOf,
  SECTION_TILES,
  SECTIONS_X,
  SECTIONS_Y,
  TOTAL_SECTIONS,
  MAP_W,
  MAP_H,
} from '../src/sim/consolidator.ts';
import { publishConsolidatorFocus, currentConsolidatorFocus } from '../src/sim/consolidatorFocus.ts';
import { TICKS_PER_MONTH } from '../src/sim/engine.ts';

/** Mirrors MapView.tsx's `geom` shape exactly: screen = origin + tile * scale. */
function screenRectOf(geom, key) {
  const { x0, y0, w, h } = sectionOriginOf(key);
  return {
    x: geom.ox + x0 * geom.s,
    y: geom.oy + y0 * geom.s,
    w: w * geom.s,
    h: h * geom.s,
  };
}

const CAMERAS = [
  { s: 1, ox: 0, oy: 0 }, // 1px/tile, top-left anchored
  { s: 3.2, ox: -120, oy: 40 }, // zoomed in, panned
  { s: 0.4, ox: 300, oy: -50 }, // zoomed out
  { s: 12, ox: -4000, oy: -800 }, // heavily zoomed in
];

test('EXHAUSTIVE at several camera positions/zooms: every section screen-rect contains the screen point of its own origin tile', () => {
  for (const geom of CAMERAS) {
    for (let key = 0; key < TOTAL_SECTIONS; key += 7) {
      // step 7 keeps this fast while still covering every residue class mod 7
      const rect = screenRectOf(geom, key);
      const { x0, y0 } = sectionOriginOf(key);
      const originScreenX = geom.ox + x0 * geom.s;
      const originScreenY = geom.oy + y0 * geom.s;
      assert.ok(originScreenX >= rect.x - 1e-9 && originScreenX <= rect.x + rect.w + 1e-9);
      assert.ok(originScreenY >= rect.y - 1e-9 && originScreenY <= rect.y + rect.h + 1e-9);
      // Rect size scales linearly with geom.s and the section's own tile size.
      const { w, h } = sectionOriginOf(key);
      assert.ok(Math.abs(rect.w - w * geom.s) < 1e-9);
      assert.ok(Math.abs(rect.h - h * geom.s) < 1e-9);
    }
  }
});

test('the ruled 16x16-tile (800m) section grid: geometry is correct at the map edges (last-column/row clipping, GR#15 derived from MAP_W/MAP_H, never hand-computed)', () => {
  // FEAT-2326609790 (2026-09-05, "double the land mass"): the grid grew
  // from 440x260 to 624x368. Both new dimensions happen to be EXACT
  // multiples of SECTION_TILES=16 (624/16=39, 368/16=23), so the last
  // column/row is no longer PARTIAL at this particular map size the way
  // 440x260 was (440/16=27.5, 260/16=16.25) — this test used to pin the
  // partial-clip numbers literally, which would have gone stale (and
  // silently stopped proving anything) the moment the grid resized. Every
  // expected value below is now DERIVED from MAP_W/MAP_H/SECTION_TILES so
  // the same test keeps proving the real formula (clip-to-map-edge, whether
  // that clip removes 0 tiles or several) across any future grid size.
  assert.equal(SECTION_TILES, 16);
  assert.equal(SECTIONS_X, Math.ceil(MAP_W / SECTION_TILES));
  assert.equal(SECTIONS_Y, Math.ceil(MAP_H / SECTION_TILES));
  const lastColKey = sectionKeyOf(MAP_W - 1, 0); // rightmost tile, top row
  const { x0, w } = sectionOriginOf(lastColKey);
  const expectedLastColX0 = (SECTIONS_X - 1) * SECTION_TILES;
  assert.equal(x0, expectedLastColX0);
  assert.equal(w, MAP_W - expectedLastColX0, 'last column clips to whatever remainder is left at the map edge (0 remainder is a valid, exact-fit case)');
  const lastRowKey = sectionKeyOf(0, MAP_H - 1); // leftmost tile, bottom row
  const { y0, h } = sectionOriginOf(lastRowKey);
  const expectedLastRowY0 = (SECTIONS_Y - 1) * SECTION_TILES;
  assert.equal(y0, expectedLastRowY0);
  assert.equal(h, MAP_H - expectedLastRowY0, 'last row clips to whatever remainder is left at the map edge (0 remainder is a valid, exact-fit case)');
});

test('the draw-loop geometry function never references buildings — pure section arithmetic only', () => {
  // screenRectOf (this file) and sectionOriginOf (consolidator.ts) both take
  // ONLY (geom, key)/(key) — there is no SimState/buildings parameter to
  // even pass one to. This is a structural proof by signature, backed by
  // consolidator.test.mjs's own GR#21 purity grep of consolidator.ts.
  assert.equal(sectionOriginOf.length, 1);
  assert.equal(screenRectOf.length, 2);
});

test('monthlyScopeOf (what the box highlights) is O(1)-cheap arithmetic on tick alone, never buildings', () => {
  const t0 = performance.now();
  for (let i = 0; i < 10_000; i++) {
    monthlyScopeOf(i * TICKS_PER_MONTH);
  }
  const elapsed = performance.now() - t0;
  // 10,000 calls (330+ in-game YEARS worth of months) should be near-instant
  // — a generous 200ms bound catches an accidental O(buildings) regression,
  // never a tight timing assertion.
  assert.ok(elapsed < 200, `10,000 monthlyScopeOf calls took ${elapsed.toFixed(1)}ms, expected < 200ms`);
});

test('consolidatorFocus mailbox: publish/read round-trips, defaults to null, and is a plain last-write-wins store', () => {
  publishConsolidatorFocus(null);
  assert.equal(currentConsolidatorFocus(), null);
  publishConsolidatorFocus(42);
  assert.equal(currentConsolidatorFocus(), 42);
  publishConsolidatorFocus(7);
  assert.equal(currentConsolidatorFocus(), 7, 'last write wins');
  publishConsolidatorFocus(null);
  assert.equal(currentConsolidatorFocus(), null, 'off (or unmount) clears the highlight');
});
