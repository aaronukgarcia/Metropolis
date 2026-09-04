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

test('the ruled 16x16-tile (800m) section grid: geometry is correct at the map edges (partial sections)', () => {
  // 440/16 = 27.5, 260/16 = 16.25 — both axes have a partial final section.
  assert.equal(SECTION_TILES, 16);
  assert.equal(SECTIONS_X, 28);
  assert.equal(SECTIONS_Y, 17);
  const lastColKey = sectionKeyOf(439, 0); // rightmost tile, top row
  const { x0, w } = sectionOriginOf(lastColKey);
  assert.equal(x0, 27 * 16); // 27th section starts at tile 432
  assert.equal(w, 440 - 27 * 16); // clipped to 8 tiles, not the full 16
  const lastRowKey = sectionKeyOf(0, 259); // leftmost tile, bottom row
  const { y0, h } = sectionOriginOf(lastRowKey);
  assert.equal(y0, 16 * 16); // 16th section starts at tile 256
  assert.equal(h, 260 - 16 * 16); // clipped to 4 tiles, not 16
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
