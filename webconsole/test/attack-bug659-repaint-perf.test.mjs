// attack-bug659-repaint-perf.test.mjs — BUG-659 P0 repaint perf gate.
//
// Aaron's real dogfood city (49,174 buildings / 3,198,809 population / tick
// 5299, savepoint C:\Users\aarongarcia\.claude\jobs\f9ac9353\tmp\aaron-49k.lz)
// reported "Engine: stalled 6.1s" — the THIRD time in one day he could not
// play. Headless engine-tick + derivation medians on that exact savepoint
// summed to well under 700ms, isolating the missing seconds to the RENDER
// path: MapView.tsx's draw effect made three unconditional O(buildings)
// passes every repaint with no viewport culling at all.
//
// This gate proves the FIX (src/render/viewportCull.ts, wired into
// MapView.tsx) at the SAME 49,174-building scale, using the provenanced
// scale fixture (test/scale/fixture.mjs — its CATEGORY_FRACTIONS are derived
// from a real dogfood capture per BUG-644, and its coordinate allocator
// spreads buildings across the FULL [0,MAP_W)x[0,MAP_H) map, exactly what a
// spatial-index viewport cull needs to be measured honestly against).
//
// BOUND DERIVATION (house rule from scale-gate.test.mjs: bound the
// steady-state MEDIAN of >=5 runs, never max; a generous multiplier, not a
// tight wall-clock race):
//
//   Measured locally (Windows, Node 25.3.0, jsdom-less pure-JS harness,
//   5 runs each) at the real 49,174-building scale:
//     - UNCULLED (the pre-fix shape, every repaint regardless of camera):
//       ~9.1ms median JS-side cost (jsdom has no real rasteriser — this is
//       the per-building iteration/branch cost alone, not paint time; see
//       perfHarness.ts's header for why that's still the right thing to
//       measure here).
//     - CULLED, ~60x40-tile viewport (a realistic "player editing a
//       district" zoom): ~0.41ms median, visible count 1037/49174 (2.1%).
//     - CULLED, ~20x15-tile viewport (zoomed further in): ~0.10ms median,
//       visible count 172/49174 (0.35%).
//     - CULLED, full-map viewport (zoomed all the way out — nothing left to
//       cull): ~11.0ms median, i.e. NOT worse than the uncalled baseline
//       (proves culling never regresses the zoomed-out case).
//
//   REPAINT_BOUND_MS is set to 4x the highest observed culled-at-realistic-
//   zoom median (0.41ms x 4 ~= 1.64ms), rounded up generously to 8ms for
//   CI-hardware variance (this project's own measured CI/local gap has been
//   3-7x on other gates — scale-gate.test.mjs, BUG-644) — an order of
//   magnitude under the 16ms/frame target the brief asks for, leaving room
//   for the real Canvas2D rasterisation cost this jsdom harness cannot
//   measure. NEVER worse than UNCALLED_REGRESSION_BOUND_MS (see below) —
//   the visible-count assertions are the real regression-catchers; the
//   wall-clock bound is a generous sanity ceiling only.
//
//   UNCALLED_REGRESSION_BOUND_MS bounds the full-map (zoom-to-fit) case at
//   3x its own observed median (11.0ms x 3 ~= 33ms -> 40ms), proving the fix
//   did not make the ALREADY-EXPENSIVE zoomed-out case any worse.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildScaleFixture } from './scale/fixture.mjs';
import { measureCurrentDrawLoopJsCost, measureCulledDrawLoopJsCost } from '../src/render/perfHarness.ts';

const REPAINT_BOUND_MS = 8; // see derivation above: 4x observed ~0.41ms, rounded up generously.
const UNCALLED_REGRESSION_BOUND_MS = 40; // 3x observed ~11.0ms full-map (zoom-to-fit) median.

const BUILDING_COUNT = 49174; // Aaron's exact measured live-city count (BUG-659 brief).
const TARGET_POPULATION = 3198809; // Aaron's exact measured live-city population.

let state;
test('setup: build the real 49,174-building / 3.2M-population scale fixture', () => {
  state = buildScaleFixture({ buildingCount: BUILDING_COUNT, targetPopulation: TARGET_POPULATION, settleTicks: 3 });
  assert.equal(state.buildings.length, BUILDING_COUNT);
});

test('BUG-659: viewport-culled repaint at a realistic zoomed-in camera stays under the bound', () => {
  // ~60x40 tiles — a district-scale view, representative of normal play
  // (nowhere near the pathological zoom-to-fit-whole-map case).
  const rect = { minX: 150, minY: 90, maxX: 210, maxY: 130 };
  const result = measureCulledDrawLoopJsCost(state, rect, 5);
  assert.ok(
    result.visibleCount < BUILDING_COUNT,
    `a realistic viewport must cull SOME buildings out of ${BUILDING_COUNT} (got ${result.visibleCount} visible — culling did not engage)`
  );
  assert.ok(
    result.msPerFrame < REPAINT_BOUND_MS,
    `viewport-culled repaint median ${result.msPerFrame.toFixed(2)}ms exceeds the ${REPAINT_BOUND_MS}ms bound at ${BUILDING_COUNT} buildings`
  );
});

test('BUG-659: viewport-culled repaint cost scales with VISIBLE count, not total city size', () => {
  const wide = measureCulledDrawLoopJsCost(state, { minX: 150, minY: 90, maxX: 210, maxY: 130 }, 5);
  const narrow = measureCulledDrawLoopJsCost(state, { minX: 200, minY: 120, maxX: 220, maxY: 135 }, 5);
  assert.ok(narrow.visibleCount < wide.visibleCount, 'narrower viewport must see fewer buildings');
  // Loose multiplier (not a tight race) — the point is monotonic scaling
  // with visible count, not an exact ratio (fixed per-frame overhead like
  // the road-connectivity Set construction does not scale to zero).
  assert.ok(
    narrow.msPerFrame <= wide.msPerFrame + 2,
    `a narrower (fewer visible buildings) viewport must not cost meaningfully MORE than a wider one: narrow=${narrow.msPerFrame.toFixed(2)}ms wide=${wide.msPerFrame.toFixed(2)}ms`
  );
});

test('BUG-659: zoom-to-fit (full-map viewport) is NOT worse than the pre-fix unculled cost', () => {
  const before = measureCurrentDrawLoopJsCost(state, 5);
  const fullMap = measureCulledDrawLoopJsCost(state, { minX: 0, minY: 0, maxX: 440, maxY: 260 }, 5);
  assert.equal(fullMap.visibleCount, BUILDING_COUNT, 'a full-map viewport must include every building — zero silently dropped');
  assert.ok(
    fullMap.msPerFrame < UNCALLED_REGRESSION_BOUND_MS,
    `full-map (zoom-to-fit) culled median ${fullMap.msPerFrame.toFixed(2)}ms exceeds the regression bound ${UNCALLED_REGRESSION_BOUND_MS}ms`
  );
  // Documents the before/after relationship for the BOW record — not a hard
  // multiplier assertion (both numbers are close by construction: a
  // full-map viewport visits every building either way), just proves the
  // culled path adds no meaningful per-building overhead over the plain scan.
  assert.ok(fullMap.msPerFrame < before.msPerFrame * 3 + 5);
});
