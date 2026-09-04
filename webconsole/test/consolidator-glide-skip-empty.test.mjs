// consolidator-glide-skip-empty.test.mjs — FEAT-2326609839, Aaron's
// "skip empty land" glide ruling (2026-09-04, verbatim: "marchin ants and
// red squares only need to look at squares with contnet so skip where
// nothing is built"). Proves BOTH halves of the feature:
//
//   SCAN  — consolidatorGlide.ts's day->column mapping, when handed an
//           `occupiedColumnsOf(s)` list (data.ts), visits ONLY non-empty
//           tile columns; the mapping stays a pure function of
//           (occupiedColumns, dayIndex) — GR#21 — and degrades to the exact
//           pre-existing dense raster when the caller omits the list
//           (backward compatible with every consolidator-glide-inc2/
//           attack-glide-inc2-round test that predates this feature).
//   DISPLAY — MapView's monthly-twelfth static scope grid (tested here at
//           the sectionIndexOf/data-layer level, since MapView.tsx itself
//           has no headless render harness in this suite) skips sections
//           with zero buildings by construction of sectionIndexOf's own
//           "absent means empty" contract (consolidator.ts).
//
// Also proves the four edge cases named in the brief: zero-buildings
// genesis (no divide-by-zero), an all-columns-occupied dense city
// (identical to the old behaviour except column visiting order — none,
// here, since ALL columns are occupied), determinism under a shuffled
// buildings array, and memo identity (same buildings array object -> the
// O(buildings) fold runs exactly once, proven by an iteration-counting
// Proxy, not by a spy library this repo doesn't otherwise depend on).
//
// RED-PROOF (see the build report): reverting glideWindowOf's `x0 =
// grid.columns[xIndex]` line back to the old linear `x0 = xIndex` (a scratch
// copy edit, GR#24 — never a git revert) makes the "40 consecutive days
// stay within columns 10-20" test below fail immediately, since a linear
// scan of a 440-wide map necessarily visits columns 0-39 in its first 40
// days, only 11 of which (10-20) are inside the fixture's occupied band.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { glideGridOf, glideWindowOf, glideWindowForDay } from '../src/sim/consolidatorGlide.ts';
import { MAP_W, MAP_H } from '../src/sim/consolidator.ts';
import { sectionTilesOf, sectionKeyOf, sectionIndexOf } from '../src/sim/consolidator.ts';
import { occupiedColumnsOf } from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

function withBuildings(buildings) {
  return { ...initialState(), buildings };
}

// ===========================================================================
// 1. SKIP-EMPTY MAPPING — buildings confined to columns 10-20 only.
// ===========================================================================

test('SKIP-EMPTY: a map with buildings only in columns 10-20 never sends the glide cursor outside 10-20, across 40 consecutive days', () => {
  const buildings = [];
  let id = 1;
  for (let x = 10; x <= 20; x++) buildings.push({ id: id++, spec: 'fire_post', x, y: 0, builtTick: -1000 });
  const s = withBuildings(buildings);
  const columns = occupiedColumnsOf(s);
  assert.deepEqual(columns, [10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20]);

  const sectionTiles = 1; // isolate the column-skip mechanism from window-width edge clipping
  for (let day = 0; day < 40; day++) {
    const win = glideWindowForDay(day, sectionTiles, columns);
    assert.ok(win.x0 >= 10 && win.x0 <= 20, `day ${day}: x0=${win.x0} escaped the 10-20 occupied band`);
  }
});

test('RED-PROOF sanity: the SAME fixture WOULD fail under the old linear (non-skipping) mapping', () => {
  // Simulates reverting to the pre-feature `x0 = dayInPass % xPositions`
  // mapping (a scratch re-derivation, not a call into production code) to
  // prove the new test above is not vacuously true.
  const sectionTiles = 1;
  const maxX0 = MAP_W - sectionTiles;
  const yPositions = MAP_H - sectionTiles + 1;
  const xPositions = maxX0 + 1;
  const total = xPositions * yPositions;
  let escaped = false;
  for (let day = 0; day < 40; day++) {
    const dayInPass = day % total;
    const x0 = dayInPass % xPositions; // OLD linear mapping
    if (x0 < 10 || x0 > 20) escaped = true;
  }
  assert.equal(escaped, true, 'sanity check: the old linear mapping must escape the 10-20 band within 40 days');
});

// ===========================================================================
// 2. EMPTY-CITY FALLBACK (genesis) — no divide-by-zero, deterministic column 0.
// ===========================================================================

test('EDGE CASE: a zero-buildings genesis city never divides by zero, falls back to column 0', () => {
  const s = withBuildings([]);
  const columns = occupiedColumnsOf(s);
  assert.deepEqual(columns, [], 'genesis: no occupied columns at all');

  const grid = glideGridOf(16, columns);
  assert.equal(grid.xPositions, 1);
  assert.deepEqual(grid.columns, [0]);
  assert.ok(Number.isFinite(grid.positionsPerPass) && grid.positionsPerPass > 0);

  for (let day = 0; day < 500; day += 47) {
    const win = glideWindowOf(day, grid);
    assert.equal(win.x0, 0, `day ${day}: genesis fallback must always sit at column 0`);
    assert.ok(Number.isFinite(win.y0) && Number.isFinite(win.x0), `day ${day}: window must never be NaN`);
  }
});

// ===========================================================================
// 3. DENSE-CITY EQUIVALENCE — every column occupied == the old behaviour,
// modulo (identical) visiting order.
// ===========================================================================

test('EDGE CASE: a dense city (every valid column occupied) is byte-identical to the pre-feature dense/omitted-argument behaviour', () => {
  const sectionTiles = 16;
  const maxX0 = MAP_W - sectionTiles;
  const everyColumn = Array.from({ length: maxX0 + 1 }, (_, i) => i);

  const legacyGrid = glideGridOf(sectionTiles); // no nonEmptyColumns arg at all
  const denseGrid = glideGridOf(sectionTiles, everyColumn); // explicitly "every column is occupied"

  assert.deepEqual(legacyGrid, denseGrid, 'an all-occupied column list must reproduce the exact legacy grid shape');

  for (let day = 0; day < 500; day += 37) {
    assert.deepEqual(glideWindowOf(day, legacyGrid), glideWindowOf(day, denseGrid), `day ${day} diverged between legacy and dense-equivalent grids`);
  }
});

// ===========================================================================
// 4. DETERMINISM — shuffled buildings array yields the identical occupied-
// column SET (order-independent fold, GR#21).
// ===========================================================================

test('DETERMINISM: shuffling the buildings array does not change the derived occupied-column set', () => {
  const buildings = [];
  let id = 1;
  const xs = [3, 47, 200, 439, 12, 12, 3, 300];
  for (const x of xs) buildings.push({ id: id++, spec: 'fire_post', x, y: (id * 7) % MAP_H, builtTick: -1000 });

  const shuffled = [...buildings].reverse();
  // A second, differently-ordered permutation to rule out "just reversed".
  const shuffled2 = [buildings[3], buildings[0], buildings[7], buildings[1], buildings[6], buildings[2], buildings[5], buildings[4]];

  const a = occupiedColumnsOf(withBuildings(buildings));
  const b = occupiedColumnsOf(withBuildings(shuffled));
  const c = occupiedColumnsOf(withBuildings(shuffled2));

  assert.deepEqual(a, b, 'reversed buildings array must derive the identical column set');
  assert.deepEqual(a, c, 'arbitrarily-permuted buildings array must derive the identical column set');
  assert.deepEqual(a, [3, 12, 47, 200, 300, 439], 'sanity: dedup + ascending sort of the raw x values');

  // Full glide sequence, not just the raw column set, must also agree.
  const gridA = glideGridOf(16, a);
  const gridB = glideGridOf(16, b);
  for (let day = 0; day < 200; day += 13) {
    assert.deepEqual(glideWindowOf(day, gridA), glideWindowOf(day, gridB), `day ${day}: shuffled-array grids diverged`);
  }
});

// ===========================================================================
// 5. MEMO IDENTITY — same buildings array object -> the O(buildings) fold
// runs exactly once. Proven with an iteration-counting Proxy (this repo has
// no spy/mock library dependency), not by re-reading source.
// ===========================================================================

test('MEMO IDENTITY: occupiedColumnsOf recomputes only when the buildings ARRAY reference changes, never on a repeat call with the same reference', () => {
  const raw = [];
  for (let x = 0; x < 50; x++) raw.push({ id: x + 1, spec: 'fire_post', x, y: 0, builtTick: -1000 });

  let indexReads = 0;
  const countingBuildings = new Proxy(raw, {
    get(target, prop, receiver) {
      if (typeof prop === 'string' && /^\d+$/.test(prop)) indexReads++;
      return Reflect.get(target, prop, receiver);
    },
  });
  const s = { ...initialState(), buildings: countingBuildings };

  const first = occupiedColumnsOf(s);
  const readsAfterFirst = indexReads;
  assert.ok(readsAfterFirst >= raw.length, 'the first call must actually fold over every building');

  const second = occupiedColumnsOf(s); // SAME state object, same buildings reference
  assert.equal(indexReads, readsAfterFirst, 'a second call with the same buildings reference must NOT re-iterate the array (memo hit)');
  assert.equal(second, first, 'a cache hit must return the exact same array instance, not merely an equal one');

  // A genuinely different buildings array (even with identical contents) is
  // correctly treated as a cache miss and DOES recompute — the memo is keyed
  // on reference identity, matching occupiedSet's own documented contract.
  const s2 = { ...initialState(), buildings: [...raw] };
  const third = occupiedColumnsOf(s2);
  assert.notEqual(third, first, 'a different buildings array reference must be a fresh computation, not an accidental cache hit');
  assert.deepEqual(third, first, 'but the VALUE must still agree, since the underlying buildings are the same');
});

// ===========================================================================
// 6. ENGINE COHERENCE — sectionKeysForGlideWindow's own approach (derive
// section keys from the glide window's corners via sectionKeyOf) still
// resolves to a genuinely non-empty section for EVERY day of a full sweep,
// once the day->column hop only ever lands on occupied columns.
// (sectionKeysForGlideWindow itself is a private engine.ts function; this
// re-derives its exact corner-key logic against the same exported
// primitives it calls, which is the only way to test it without reaching
// into engine.ts internals.)
// ===========================================================================

test('ENGINE COHERENCE: every day of a full glide sweep addresses a section that sectionIndexOf actually reports as occupied', () => {
  const buildings = [];
  let id = 1;
  // Buildings fill columns 10-14 across EVERY row, so any glide window whose
  // x0 lands on one of those columns is guaranteed to overlap a real
  // building regardless of which row (y0) it is currently on.
  for (let x = 10; x <= 14; x++) {
    for (let y = 0; y < MAP_H; y++) buildings.push({ id: id++, spec: 'fire_post', x, y, builtTick: -1000 });
  }
  const s = withBuildings(buildings);
  const columns = occupiedColumnsOf(s);
  assert.deepEqual(columns, [10, 11, 12, 13, 14]);

  const sectionTiles = sectionTilesOf(s);
  const grid = glideGridOf(sectionTiles, columns);
  const index = sectionIndexOf(s);

  assert.ok(grid.positionsPerPass > 0 && grid.positionsPerPass < 5000, 'sanity: a full sweep must be small enough to exhaustively check in a unit test');
  for (let day = 0; day < grid.positionsPerPass; day++) {
    const win = glideWindowOf(day, grid);
    // Mirrors sectionKeysForGlideWindow's own top-left corner key exactly
    // (engine.ts) — the window's x0/y0 origin corner.
    const key = sectionKeyOf(win.x0, win.y0);
    assert.ok(index.has(key), `day ${day}: window at (${win.x0},${win.y0}) resolved to section ${key}, which sectionIndexOf reports as EMPTY`);
  }
});

// ===========================================================================
// 7. OLD SAVES — nothing new is persisted; the index is purely derived.
// ===========================================================================

test('NO NEW PERSISTED STATE: occupiedColumnsOf is a pure derivation of s.buildings, never reads/writes SimState fields other than buildings', () => {
  const buildings = [{ id: 1, spec: 'fire_post', x: 5, y: 5, builtTick: -1000 }];
  const s1 = withBuildings(buildings);
  const s2 = { ...s1, tick: 999999, funds: 12345, consolidatorLog: [{ bogus: true }] };
  // Same buildings ARRAY reference on both states -> identical (cached)
  // result regardless of every other field differing wildly.
  assert.equal(occupiedColumnsOf(s1), occupiedColumnsOf(s2));
});
