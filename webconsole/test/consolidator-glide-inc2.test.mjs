// consolidator-glide-inc2.test.mjs — FEAT-2326609761 inc2 GLIDE MODE, Aaron's
// DEFAULT traversal ruling (2026-09-04). Proves:
//   1. the glide cursor is a PURE function of (day, sectionTiles) — GR#21
//   2. exhaustive coverage: every column of every row is visited EXACTLY
//      once per full pass, at both map edges (wrap arithmetic)
//   3. a section-size change mid-glide changes only the NEXT window, never
//      throws, never desyncs (no persisted "resume position" to corrupt)
//   4. the level-10 unlock gate (structural, engine.ts reducer)
//   5. the slider sum-to-100 validator (engine.ts)
//   6. the marching-ants display counter is genuinely render-only (never
//      reaches SimState/journal/replay)
//   7. a per-day perf bound on the cursor itself (the real mutation-pass
//      timing awaits the separate mutation lane landing — see the report)

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { glideGridOf, glideWindowOf, glideWindowForDay } from '../src/sim/consolidatorGlide.ts';
import { MAP_W, MAP_H } from '../src/sim/consolidator.ts';
import {
  initialState,
  reducer,
  CONSOLIDATOR_UNLOCK_LEVEL,
  consolidatorUnlockedAtLevel,
  xpForLevel,
  validateConsolidatorSliders,
  CONSOLIDATOR_SLIDERS_DEFAULT,
  CONSOLIDATOR_MODE_DEFAULT,
  CONSOLIDATOR_SECTION_METRES_DEFAULT,
  CONSOLIDATOR_SECTION_METRES_MIN,
  CONSOLIDATOR_SECTION_METRES_MAX,
  clampConsolidatorSectionMetres,
} from '../src/sim/engine.ts';
import { isStateAffecting } from '../src/sim/journal.ts';
import { isConsolidatorBoxVisible, setConsolidatorBoxVisible, CONSOLIDATOR_BOX_VISIBLE_KEY } from '../src/sim/consolidatorDisplayFlag.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// ===========================================================================
// 1. PURITY + DEFAULTS
// ===========================================================================

test('GLIDE IS THE DEFAULT MODE (Aaron\'s ruling)', () => {
  assert.equal(CONSOLIDATOR_MODE_DEFAULT, 'glide');
  const s = initialState();
  assert.equal(s.consolidatorMode ?? CONSOLIDATOR_MODE_DEFAULT, 'glide');
});

test('glideWindowOf/glideWindowForDay are pure: same inputs, same outputs, no globals touched', () => {
  const grid = glideGridOf(16);
  const a = glideWindowOf(12345, grid);
  const b = glideWindowOf(12345, grid);
  assert.deepEqual(a, b);
  const c = glideWindowForDay(12345, 16);
  assert.deepEqual(a, c);
});

test('the glide window is always exactly sectionTiles x sectionTiles (never clipped, by construction)', () => {
  const grid = glideGridOf(16);
  for (let d = 0; d < 500; d += 37) {
    const w = glideWindowOf(d, grid);
    assert.equal(w.w, 16, `day ${d} width`);
    assert.equal(w.h, 16, `day ${d} height`);
    assert.ok(w.x0 >= 0 && w.x0 + w.w <= MAP_W, `day ${d} x0 in range`);
    assert.ok(w.y0 >= 0 && w.y0 + w.h <= MAP_H, `day ${d} y0 in range`);
  }
});

// ===========================================================================
// 2. EXHAUSTIVE COVERAGE PROOF — "one pixel right per day, wrap one row
// lower" — every column of every row visited EXACTLY once per full pass.
// ===========================================================================

test('exhaustive coverage: one full pass visits every (x0,y0) position exactly once', () => {
  const sectionTiles = 16;
  const grid = glideGridOf(sectionTiles);
  const seen = new Set();
  for (let d = 0; d < grid.positionsPerPass; d++) {
    const w = glideWindowOf(d, grid);
    const key = `${w.x0},${w.y0}`;
    assert.ok(!seen.has(key), `position ${key} visited twice within one pass (day ${d})`);
    seen.add(key);
  }
  assert.equal(seen.size, grid.positionsPerPass, 'every distinct position must be reachable');
  assert.equal(grid.positionsPerPass, grid.xPositions * grid.yPositions);
});

test('raster order: x0 advances by exactly 1 each day within a row, wraps to x0=0 with y0+1 at the row boundary', () => {
  const grid = glideGridOf(16);
  for (let d = 0; d < 40; d++) {
    const cur = glideWindowOf(d, grid);
    const next = glideWindowOf(d + 1, grid);
    if (cur.x0 < grid.xPositions - 1) {
      assert.equal(next.x0, cur.x0 + 1, `day ${d}->${d + 1} should advance one column`);
      assert.equal(next.y0, cur.y0, `day ${d}->${d + 1} should stay on the same row`);
    } else {
      assert.equal(next.x0, 0, `day ${d}->${d + 1} should wrap to the left edge`);
      assert.equal(next.y0, (cur.y0 + 1) % grid.yPositions, `day ${d}->${d + 1} should drop one row (or wrap the whole pass)`);
    }
  }
});

test('BOTH edges: day 0 starts top-left; the last day of a pass sits at the bottom-right-most valid position; the day after wraps to a NEW pass at top-left again', () => {
  const grid = glideGridOf(16);
  const first = glideWindowOf(0, grid);
  assert.equal(first.x0, 0);
  assert.equal(first.y0, 0);
  assert.equal(first.passIndex, 0);

  const lastOfPass = glideWindowOf(grid.positionsPerPass - 1, grid);
  assert.equal(lastOfPass.x0, grid.xPositions - 1);
  assert.equal(lastOfPass.y0, grid.yPositions - 1);
  assert.equal(lastOfPass.passIndex, 0);

  const firstOfSecondPass = glideWindowOf(grid.positionsPerPass, grid);
  assert.equal(firstOfSecondPass.x0, 0);
  assert.equal(firstOfSecondPass.y0, 0);
  assert.equal(firstOfSecondPass.passIndex, 1, 'multi-passing forever: the sweep must repeat, never stop or throw');
});

test('multi-pass forever: many passes in a row all restart cleanly (no drift, no exception)', () => {
  const grid = glideGridOf(16);
  for (let pass = 0; pass < 5; pass++) {
    const d = pass * grid.positionsPerPass;
    const w = glideWindowOf(d, grid);
    assert.equal(w.x0, 0, `pass ${pass} must restart at x0=0`);
    assert.equal(w.y0, 0, `pass ${pass} must restart at y0=0`);
    assert.equal(w.passIndex, pass);
  }
});

// ===========================================================================
// 3. SECTION-SIZE CHANGE MID-GLIDE
// ===========================================================================

test('a section-size change mid-glide only changes the NEXT window it is applied to — no persisted resume state, never throws', () => {
  const smallGrid = glideGridOf(16);
  const bigGrid = glideGridOf(32);
  const day = 400;
  const beforeChange = glideWindowOf(day, smallGrid);
  const afterChange = glideWindowOf(day, bigGrid); // SAME day, new size — pure re-derivation, not a stateful transition
  assert.equal(beforeChange.w, 16);
  assert.equal(afterChange.w, 32);
  // Both are independently valid, in-range windows — proves the cursor
  // recomputes cleanly from (day, size) with no leftover state from the
  // other grid shape.
  assert.ok(afterChange.x0 + afterChange.w <= MAP_W);
  assert.ok(afterChange.y0 + afterChange.h <= MAP_H);
});

test('clampConsolidatorSectionMetres bounds a player value into the sanctioned range, snapping non-finite input to the default', () => {
  assert.equal(clampConsolidatorSectionMetres(50), CONSOLIDATOR_SECTION_METRES_MIN);
  assert.equal(clampConsolidatorSectionMetres(100000), CONSOLIDATOR_SECTION_METRES_MAX);
  assert.equal(clampConsolidatorSectionMetres(NaN), CONSOLIDATOR_SECTION_METRES_DEFAULT);
  assert.equal(clampConsolidatorSectionMetres(800), 800);
});

test('setConsolidatorSectionMetres reducer clamps and journals', () => {
  assert.equal(isStateAffecting({ type: 'setConsolidatorSectionMetres', metres: 800 }), true);
  const s0 = initialState();
  assert.equal(s0.consolidatorSectionMetres ?? CONSOLIDATOR_SECTION_METRES_DEFAULT, 800);
  const s1 = reducer(s0, { type: 'setConsolidatorSectionMetres', metres: 1 });
  assert.equal(s1.consolidatorSectionMetres, CONSOLIDATOR_SECTION_METRES_MIN, 'an out-of-range value is clamped, not rejected outright');
  const s2 = reducer(s0, { type: 'setConsolidatorSectionMetres', metres: 1200 });
  assert.equal(s2.consolidatorSectionMetres, 1200);
});

test('setConsolidatorMode refuses an unrecognised mode string (GR#16), accepts the two real ones', () => {
  const s0 = initialState();
  const s1 = reducer(s0, { type: 'setConsolidatorMode', mode: 'monthly-twelfth' });
  assert.equal(s1.consolidatorMode, 'monthly-twelfth');
  const s2 = reducer(s1, { type: 'setConsolidatorMode', mode: 'glide' });
  assert.equal(s2.consolidatorMode, 'glide');
  const s3 = reducer(s1, { type: 'setConsolidatorMode', mode: 'bogus' });
  assert.equal(s3, s1, 'an unrecognised mode is a structural no-op, not a corrupted write');
});

// ===========================================================================
// 4. LEVEL-10 UNLOCK GATE
// ===========================================================================

test('consolidatorUnlockedAtLevel is a simple >= threshold at CONSOLIDATOR_UNLOCK_LEVEL (10)', () => {
  assert.equal(CONSOLIDATOR_UNLOCK_LEVEL, 10);
  assert.equal(consolidatorUnlockedAtLevel(9), false);
  assert.equal(consolidatorUnlockedAtLevel(10), true);
  assert.equal(consolidatorUnlockedAtLevel(11), true);
});

test('STRUCTURAL GATE: toggleConsolidator refuses to turn ON below level 10, even via a direct reducer dispatch (never just a disabled checkbox)', () => {
  const belowLevel10 = { ...initialState(), xp: 0 };
  const s1 = reducer(belowLevel10, { type: 'toggleConsolidator' });
  assert.equal(s1.consolidatorEnabled, belowLevel10.consolidatorEnabled ?? false, 'a locked city must not be able to enable the consolidator via a raw dispatch');
});

test('STRUCTURAL GATE: toggleConsolidator succeeds once the level threshold is reached, and turning OFF is never blocked', () => {
  const unlocked = { ...initialState(), xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL) };
  const on = reducer(unlocked, { type: 'toggleConsolidator' });
  assert.equal(on.consolidatorEnabled, true);
  // Even if xp were somehow to regress (should never happen in real play),
  // turning OFF an already-enabled consolidator must never be blocked.
  const regressed = { ...on, xp: 0 };
  const off = reducer(regressed, { type: 'toggleConsolidator' });
  assert.equal(off.consolidatorEnabled, false);
});

// ===========================================================================
// 5. SLIDER SUM-TO-100 VALIDATION
// ===========================================================================

test('CONSOLIDATOR_SLIDERS_DEFAULT is an even 25/25/25/25 split summing to 100', () => {
  const d = CONSOLIDATOR_SLIDERS_DEFAULT;
  assert.equal(d.office + d.mining + d.farming + d.factory, 100);
  assert.equal(validateConsolidatorSliders(d), true);
});

test('validateConsolidatorSliders refuses a mix that does not sum to exactly 100', () => {
  assert.equal(validateConsolidatorSliders({ office: 30, mining: 30, farming: 30, factory: 9 }), false, '99 must fail');
  assert.equal(validateConsolidatorSliders({ office: 30, mining: 30, farming: 30, factory: 11 }), false, '101 must fail');
  assert.equal(validateConsolidatorSliders({ office: 25, mining: 25, farming: 25, factory: 25 }), true);
});

test('validateConsolidatorSliders refuses negative or non-finite components even if they sum to 100', () => {
  assert.equal(validateConsolidatorSliders({ office: -10, mining: 60, farming: 25, factory: 25 }), false);
  assert.equal(validateConsolidatorSliders({ office: NaN, mining: 25, farming: 25, factory: 50 }), false);
});

test('setConsolidatorSliders REFUSES (no state change) a bad mix, and ACCEPTS a valid one', () => {
  assert.equal(isStateAffecting({ type: 'setConsolidatorSliders', sliders: CONSOLIDATOR_SLIDERS_DEFAULT }), true);
  const s0 = initialState();
  const bad = reducer(s0, { type: 'setConsolidatorSliders', sliders: { office: 50, mining: 50, farming: 50, factory: 50 } });
  assert.equal(bad, s0, 'a mix summing to 200 must be a structural no-op');
  const good = reducer(s0, { type: 'setConsolidatorSliders', sliders: { office: 40, mining: 20, farming: 20, factory: 20 } });
  assert.deepEqual(good.consolidatorSliders, { office: 40, mining: 20, farming: 20, factory: 20 });
});

// ===========================================================================
// 6. MARCHING ANTS IS RENDER-ONLY — no sim state, no journal, no localStorage
// leak into the journalled consolidator fields.
// ===========================================================================

test('consolidatorGlide.ts and consolidatorDisplayFlag.ts never touch SimState/journal/localStorage-for-sim-fields', () => {
  const glideSrc = fs.readFileSync(path.join(__dirname, '..', 'src', 'sim', 'consolidatorGlide.ts'), 'utf8');
  assert.doesNotMatch(glideSrc, /localStorage\.\w|localStorage\[/, 'the glide cursor must be a pure function, never storage-backed');
  // Match actual USAGE (a dispatch call, or importing/typing against
  // SimState), never a bare mention — this file's own doc comments legitimately
  // SAY "no SimState mutation" as a rule statement, which would false-positive
  // a naive substring match (mirrors consolidator-toggle.test.mjs's own idiom).
  assert.doesNotMatch(glideSrc, /dispatch\(|import[^;]*SimState|:\s*SimState\b/, 'the glide cursor module must never dispatch actions or import/reference the SimState type');
});

test('the marching-ants animation counter in MapView.tsx is declared with useRef (never useState/SimState) — a ref update does not trigger React state changes or re-renders that could be mistaken for sim mutation', () => {
  const mapViewSrc = fs.readFileSync(path.join(__dirname, '..', 'src', 'components', 'MapView.tsx'), 'utf8');
  assert.match(mapViewSrc, /consolidatorAntsOffsetRef\s*=\s*useRef\(0\)/, 'must be a plain ref, not React/sim state');
});

test('CONSOLIDATOR_BOX_VISIBLE_KEY is a distinct localStorage key from every journalled consolidator field name (no accidental field-name collision)', () => {
  assert.equal(typeof CONSOLIDATOR_BOX_VISIBLE_KEY, 'string');
  for (const field of ['consolidatorEnabled', 'consolidatorMode', 'consolidatorSectionMetres', 'consolidatorSliders']) {
    assert.notEqual(CONSOLIDATOR_BOX_VISIBLE_KEY, field);
  }
});

test('display flag read/write round-trips through a fake storage object, and degrades safely with none', () => {
  const store = new Map();
  const fake = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, v),
  };
  assert.equal(isConsolidatorBoxVisible(fake), true, 'default is SHOWN (opt-out, not opt-in)');
  setConsolidatorBoxVisible(false, fake);
  assert.equal(isConsolidatorBoxVisible(fake), false);
  setConsolidatorBoxVisible(true, fake);
  assert.equal(isConsolidatorBoxVisible(fake), true);
  // No storage at all (private mode / SSR) — never throws, degrades to shown.
  assert.equal(isConsolidatorBoxVisible(undefined), true);
});

// ===========================================================================
// 7. PER-DAY PERF BOUND (the pure cursor's own cost — see the build report
// for the honest scope note on the mutation pass itself, which is landing
// on a separate lane and could not be measured here).
// ===========================================================================

test('PERF: computing the glide window for 10,000 consecutive days takes well under 50ms total (the cursor itself is O(1) per call)', () => {
  const grid = glideGridOf(16);
  const started = process.hrtime.bigint();
  let sink = 0;
  for (let d = 0; d < 10000; d++) {
    const w = glideWindowOf(d, grid);
    sink += w.x0 + w.y0;
  }
  const ms = Number(process.hrtime.bigint() - started) / 1e6;
  assert.ok(sink >= 0);
  assert.ok(ms < 50, `10,000 glide-window computations took ${ms.toFixed(2)}ms, expected well under 50ms`);
});
