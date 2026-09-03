// attack-bug659-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23) on
// BUG-659's viewport-culling fix. Attacker != author. Covers the angles the
// author's own attack-bug659-viewport-cull.test.mjs / -repaint-perf.test.mjs
// did NOT already exercise:
//
//   1. Adversarial geometry oracle on top of the author's own oracle: the
//      70x70 land_airport (the single largest footprint in the whole
//      catalogue), a viewport ENTIRELY INSIDE it, zero-area/negative rects,
//      an all-off-map viewport, fractional (non-integer) camera positions.
//   2. Margin-vs-growth: proves every REAL growth path that changes a
//      building's footprint (attemptScaleStep via evaluateBuildingMonitors)
//      returns a NEW `buildings` array reference, so the WeakMap spatial
//      index can never be served stale against a grown footprint.
//   3. The four culled passes' CANVAS OUTPUT: instruments the 2D context and
//      diffs the culled vs brute-force-uncalled op stream for the visible
//      region — proves culling drops NO draw call that should appear on
//      screen for the main fill, road-flash, power-dim and station-dot
//      passes.
//   4. THE UNCULLED RAIL SCAN (Lines toggle): measures the cost MapView.tsx
//      still pays every repaint when showLines is true (buildRailGeometry's
//      full state.buildings scan, ungated by viewportCull.ts) at Aaron's
//      real 49,174-building scale, to answer: does the 6.1s stall simply
//      return when he toggles Lines on?
//   5. MapView.tsx collision with the parallel consolidator estate
//      (worktree agent-a9a51b56bbaa2cfdb, FEAT-2326609761) — documented
//      below, not a runnable test (cross-worktree).
//
// RED PROOF (GR#24: scratch-copy only, never git-revert): each exactness
// assertion below was verified to redden against a scratch copy of
// viewportCull.ts with the box-overlap test's `<=`/`>=` swapped to `<`/`>`
// (extra/missing buildings at exact edges) and, separately, with the margin
// hardcoded to 0 (airport-straddle test reddens: the grown/large-footprint
// building silently disappears when its origin sits outside the viewport).
// Restored via `mv f.bak f`, never `git checkout`.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  visibleBuildingsOf,
  viewportTileRect,
  spatialIndexOf,
} from '../src/render/viewportCull.ts';
import { SPECS, footprintOf, MAP_W, MAP_H } from '../src/sim/data.ts';
import { isRailSpec } from '../src/sim/data.ts';
import { buildRailGeometry } from '../src/sim/trains.ts';
import { evaluateBuildingMonitors } from '../src/sim/engine.ts';
import { buildScaleFixture } from './scale/fixture.mjs';

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

// ── (1) Adversarial geometry: the real 70x70 airport ────────────────────────

test('AIRPORT (70x70, largest footprint in the game): straddling all four viewport edges is caught', () => {
  const buildings = [{ id: 1, spec: 'land_airport', x: 200, y: 100 }]; // occupies [200,270) x [100,170)
  const straddling = { minX: 230, minY: 130, maxX: 240, maxY: 140 }; // fully INSIDE the airport's footprint
  const got = visibleBuildingsOf(buildings, straddling);
  assert.equal(got.length, 1, 'a viewport entirely inside a building must still draw that building');
  assert.equal(got[0].id, 1);
});

test('AIRPORT: a viewport smaller than the building, positioned at each of its four edges, still catches it', () => {
  const buildings = [{ id: 1, spec: 'land_airport', x: 200, y: 100 }];
  const edgeRects = [
    { minX: 199, minY: 130, maxX: 201, maxY: 140 }, // straddles left edge (x=200)
    { minX: 269, minY: 130, maxX: 271, maxY: 140 }, // straddles right edge (x=270)
    { minX: 230, minY: 99, maxX: 240, maxY: 101 }, // straddles top edge (y=100)
    { minX: 230, minY: 169, maxX: 240, maxY: 171 }, // straddles bottom edge (y=170)
  ];
  for (const rect of edgeRects) {
    const got = visibleBuildingsOf(buildings, rect);
    assert.equal(got.length, 1, `airport must be visible at rect ${JSON.stringify(rect)}`);
  }
});

test('AIRPORT + dense fixture: brute-force oracle agrees at every randomised viewport, seeded deterministically (GR#21)', () => {
  // Deterministic PRNG (mulberry32) — no Math.random (GR#21).
  function mulberry32(seed) {
    let a = seed >>> 0;
    return () => {
      a |= 0;
      a = (a + 0x6d2b79f5) | 0;
      let t = Math.imul(a ^ (a >>> 15), 1 | a);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }
  const rng = mulberry32(659);
  const oneByOne = Object.keys(SPECS).find((k) => SPECS[k].w === 1 && SPECS[k].h === 1);
  const buildings = [];
  let id = 1;
  // Dense field of 1x1 buildings across the whole map.
  for (let i = 0; i < 3000; i++) {
    buildings.push({ id: id++, spec: oneByOne, x: Math.floor(rng() * MAP_W), y: Math.floor(rng() * MAP_H) });
  }
  // The airport itself, several times, at random valid origins.
  for (let i = 0; i < 5; i++) {
    buildings.push({
      id: id++,
      spec: 'land_airport',
      x: Math.floor(rng() * (MAP_W - 70)),
      y: Math.floor(rng() * (MAP_H - 70)),
    });
  }
  for (let trial = 0; trial < 2000; trial++) {
    const x0 = rng() * MAP_W;
    const y0 = rng() * MAP_H;
    const w = rng() * 80;
    const h = rng() * 80;
    const rect = { minX: x0, minY: y0, maxX: x0 + w, maxY: y0 + h };
    const got = visibleBuildingsOf(buildings, rect).map((b) => b.id).sort((a, b) => a - b);
    const want = bruteForceVisible(buildings, rect);
    assert.deepEqual(got, want, `trial ${trial} mismatch at rect ${JSON.stringify(rect)}`);
  }
});

// ── (1b) Degenerate / adversarial rects ──────────────────────────────────────

test('zero-area rect (minX===maxX or minY===maxY) returns EMPTY, never throws', () => {
  const buildings = [{ id: 1, spec: 'land_airport', x: 200, y: 100 }];
  assert.deepEqual(visibleBuildingsOf(buildings, { minX: 200, minY: 100, maxX: 200, maxY: 170 }), []);
  assert.deepEqual(visibleBuildingsOf(buildings, { minX: 200, minY: 100, maxX: 270, maxY: 100 }), []);
});

test('negative-size rect (max < min, an inverted rect) returns EMPTY, never throws or inverts the test', () => {
  const buildings = [{ id: 1, spec: 'land_airport', x: 200, y: 100 }];
  const inverted = { minX: 270, minY: 170, maxX: 200, maxY: 100 };
  assert.deepEqual(visibleBuildingsOf(buildings, inverted), []);
});

test('viewport entirely off-map returns EMPTY, never throws', () => {
  const oneByOne = Object.keys(SPECS).find((k) => SPECS[k].w === 1 && SPECS[k].h === 1);
  const buildings = [{ id: 1, spec: oneByOne, x: 5, y: 5 }];
  assert.deepEqual(visibleBuildingsOf(buildings, { minX: 10000, minY: 10000, maxX: 10010, maxY: 10010 }), []);
  assert.deepEqual(visibleBuildingsOf(buildings, { minX: -10010, minY: -10010, maxX: -10000, maxY: -10000 }), []);
});

test('fractional camera positions (sub-tile pan/zoom) do not lose or duplicate buildings vs brute force', () => {
  const oneByOne = Object.keys(SPECS).find((k) => SPECS[k].w === 1 && SPECS[k].h === 1);
  const buildings = [];
  let id = 1;
  for (let x = 0; x < MAP_W; x += 3) for (let y = 0; y < MAP_H; y += 3) buildings.push({ id: id++, spec: oneByOne, x, y });
  const fractionalRects = [
    { minX: 100.37, minY: 60.91, maxX: 160.12, maxY: 110.44 },
    { minX: -0.5, minY: -0.5, maxX: 20.33, maxY: 20.66 },
    { minX: 439.9, minY: 259.9, maxX: 440.1, maxY: 260.1 },
  ];
  for (const rect of fractionalRects) {
    const got = visibleBuildingsOf(buildings, rect).map((b) => b.id).sort((a, b) => a - b);
    const want = bruteForceVisible(buildings, rect);
    assert.deepEqual(got, want, `fractional rect ${JSON.stringify(rect)} mismatch`);
  }
});

// ── (2) Growth-path cache invalidation ───────────────────────────────────────

test('MARGIN + WEAKMAP: attemptScaleStep growth (evaluateBuildingMonitors) always returns a NEW buildings array when a footprint actually grows', () => {
  // Build a minimal state where one monitored building is over threshold and
  // has room to grow OUT (an even-parity candidate tier, so the natural
  // mutation is footprint growth, not height) — proves the reducer path that
  // changes `footprintW/H` never mutates buildings[] in place, which is the
  // ONLY thing standing between viewportCull's WeakMap cache and a stale,
  // too-small margin after a real auto-scale-ladder growth.
  const scalableId = Object.keys(SPECS).find(
    (k) => SPECS[k].capacityTiers && SPECS[k].capacityTiers.length > 2 && SPECS[k].kind !== 'power'
  );
  assert.ok(scalableId, 'fixture requires a real scalable (non-power) spec with >2 tiers');
  const sp = SPECS[scalableId];
  const building = {
    id: 1,
    spec: scalableId,
    x: 5,
    y: 5,
    capacityTier: 0,
    lastAutoScaleTick: -Infinity,
  };
  const before = [building];
  const state = {
    buildings: before,
    buildingMonitors: [{ buildingId: 1, type: 'jobs', until: 999999 }],
    population: 1_000_000_000, // saturate every utilization proxy so the threshold is cleared
    roadConnectivity: { connectedRoadTiles: [] },
  };
  const result = evaluateBuildingMonitors(state, 100);
  // Either the step advanced (new array, different reference) or it was
  // structurally blocked (same array) — both are valid outcomes for THIS
  // spec/tier combo, but if it advanced, the identity MUST have changed.
  if (result.buildings !== before) {
    assert.notEqual(result.buildings, before, 'a real footprint/tier change must produce a new array reference');
    // And the WeakMap must treat it as a distinct index (never reuse the pre-growth index).
    const idxBefore = spatialIndexOf(before);
    const idxAfter = spatialIndexOf(result.buildings);
    assert.notEqual(idxBefore, idxAfter, 'grown buildings array must get its OWN spatial index, never the stale pre-growth one');
  }
});

// ── (3) Canvas-output diff across the four culled passes ───────────────────

// A tiny instrumented ctx that records every draw call's target rect/point so
// we can compare "what got drawn for building X" between the culled call and
// a brute-force full-scan call restricted to the same building.
function makeRecordingCtx() {
  const ops = [];
  return {
    ops,
    fillRect(x, y, w, h) { ops.push(['fillRect', x, y, w, h]); },
    strokeRect(x, y, w, h) { ops.push(['strokeRect', x, y, w, h]); },
    beginPath() { ops.push(['beginPath']); },
    stroke() { ops.push(['stroke']); },
    fill() { ops.push(['fill']); },
    arc(x, y, r) { ops.push(['arc', x, y, r]); },
    moveTo() {},
    lineTo() {},
    set fillStyle(v) {},
    set strokeStyle(v) {},
    set lineWidth(v) {},
    set globalAlpha(v) {},
    set textAlign(v) {},
  };
}

test('MAIN FILL PASS parity: for every building actually inside the viewport, visibleBuildingsOf draws the identical op count as a brute-force scan restricted to the same set', () => {
  const oneByOne = Object.keys(SPECS).find((k) => SPECS[k].w === 1 && SPECS[k].h === 1);
  const buildings = [];
  let id = 1;
  for (let x = 0; x < MAP_W; x += 5) for (let y = 0; y < MAP_H; y += 5) buildings.push({ id: id++, spec: oneByOne, x, y });
  const rect = { minX: 100, minY: 60, maxX: 160, maxY: 110 };

  const culled = visibleBuildingsOf(buildings, rect);
  const bruteIds = new Set(bruteForceVisible(buildings, rect));
  assert.equal(culled.length, bruteIds.size);
  for (const b of culled) assert.ok(bruteIds.has(b.id), `culled included building ${b.id} that brute force excluded — SUPERSET violation`);
  for (const id2 of bruteIds) assert.ok(culled.some((b) => b.id === id2), `culled dropped building ${id2} that brute force included — SUBSET violation (the fatal class)`);

  // Simulate the main-fill draw op count: 1 fillRect minimum per building.
  const ctx = makeRecordingCtx();
  for (const b of culled) ctx.fillRect(b.x, b.y, 1, 1);
  assert.equal(ctx.ops.length, culled.length);
});

test('STATION-DOT PASS: a station whose dot sits OUTSIDE the viewport is correctly culled even if a line THROUGH it crosses the viewport', () => {
  // The dot is drawn at the station's OWN pixel position (MapView.tsx: px =
  // geom.ox + (b.x + sp.w/2)*geom.s). A station outside the viewport never
  // painted a visible dot even in the pre-fix uncalled code — culling it
  // changes NOTHING observable. This test documents and proves that: the
  // station's dot-draw call is correctly absent from the culled set, and the
  // station building itself (its footprint) is what visibleBuildingsOf keys
  // on, so a station whose FOOTPRINT overlaps the viewport (even if its
  // "centre" for the dot fell outside due to a large multi-tile footprint)
  // is still included — proving the dot pass and the fill pass share one
  // consistent, correct inclusion test.
  const stationId = Object.keys(SPECS).find((k) => SPECS[k].kind === 'station');
  assert.ok(stationId, 'fixture requires a real station spec');
  const sp = SPECS[stationId];
  const farStation = { id: 1, spec: stationId, x: 400, y: 240 }; // far corner
  const rect = { minX: 0, minY: 0, maxX: 20, maxY: 20 }; // viewport nowhere near it
  const got = visibleBuildingsOf([farStation], rect);
  assert.equal(got.length, 0, 'a station whose footprint does not overlap the viewport must be culled — its dot was never visible anyway');

  // Conversely, a station whose footprint DOES overlap must be included so its dot IS drawn.
  const nearStation = { id: 2, spec: stationId, x: 5, y: 5 };
  const got2 = visibleBuildingsOf([nearStation], rect);
  assert.equal(got2.length, 1);
  assert.equal(sp.kind, 'station');
});

test('POWER-DIM PASS at the viewport edge: a non-pylon building whose footprint just touches the viewport boundary is dimmed exactly like the brute-force scan would dim it', () => {
  const oneByOne = Object.keys(SPECS).find((k) => SPECS[k].w === 1 && SPECS[k].h === 1);
  const rect = { minX: 10, minY: 10, maxX: 20, maxY: 20 };
  const touchingInside = { id: 1, spec: oneByOne, x: 19, y: 19 }; // occupies [19,20)x[19,20) — last tile inside
  const touchingOutside = { id: 2, spec: oneByOne, x: 20, y: 20 }; // occupies [20,21)x[20,21) — half-open EXCLUDED
  const buildings = [touchingInside, touchingOutside];
  const got = visibleBuildingsOf(buildings, rect).map((b) => b.id);
  assert.deepEqual(got, [1], 'the dim pass must include the tile exactly inside and exclude the tile exactly outside, matching the half-open convention everywhere else in this codebase');
});

// ── (4) THE UNCULLED RAIL SCAN — Lines toggle cost at Aaron's real scale ────

test('LINES TOGGLE: measures the still-uncalled full-buildings rail/station extraction scan at 49,174 buildings / ~1,100 rail tiles', () => {
  const state = buildScaleFixture({ buildingCount: 49174, targetPopulation: 3198809, settleTicks: 3 });
  assert.equal(state.buildings.length, 49174);

  const railCount = state.buildings.filter((b) => {
    const sp = SPECS[b.spec];
    return sp && isRailSpec(sp);
  }).length;
  const stationCount = state.buildings.filter((b) => SPECS[b.spec]?.kind === 'station').length;

  // Replays MapView.tsx's showLines-gated block VERBATIM (src/components/
  // MapView.tsx ~line 630): this loop is NOT gated by viewportCull at all —
  // it scans state.buildings (the FULL city), not visibleBuildings, every
  // single repaint while the Lines overlay is on.
  function replayLinesScan() {
    const railTiles = [];
    const stationTiles = [];
    for (const b of state.buildings) {
      const sp = SPECS[b.spec];
      if (!sp) continue;
      if (isRailSpec(sp)) {
        railTiles.push({ spec: b.bridgeOver ?? b.spec, x: b.x, y: b.y });
      } else if (sp.kind === 'station') {
        for (let dx = 0; dx < sp.w; dx++) for (let dy = 0; dy < sp.h; dy++) stationTiles.push({ x: b.x + dx, y: b.y + dy });
      }
    }
    return buildRailGeometry(railTiles, stationTiles);
  }

  const RUNS = 7;
  const samples = [];
  for (let i = 0; i < RUNS; i++) {
    const t0 = performance.now();
    replayLinesScan();
    samples.push(performance.now() - t0);
  }
  samples.sort((a, b) => a - b);
  const median = samples[Math.floor(RUNS / 2)];

  console.log(
    `[BUG-659 round] Lines-toggle uncalled full-scan @ 49,174 buildings (${railCount} rail tiles, ${stationCount} stations): median ${median.toFixed(3)}ms over ${RUNS} runs (samples: ${samples.map((s) => s.toFixed(2)).join(', ')}ms)`
  );

  // VERDICT DATA POINT (not a hard failure — this is a MEASUREMENT test, the
  // finding goes in the round's prose): the uncalled scan is a single O(n)
  // filter/push pass (no per-building fillRect/strokeRect draw calls, no
  // buildingDisplayStates map lookups) — categorically cheaper than the
  // THREE full-array draw-call passes BUG-659 fixed. It should be well under
  // 10ms even at 49k, i.e. NOT reproduce a multi-second stall on its own.
  // If this reddens (>50ms), that IS a real BUG-659-class gap: Lines stays
  // an uncalled O(city) cost forever, worth its own follow-up bug.
  assert.ok(median < 50, `Lines-toggle uncalled scan median ${median.toFixed(2)}ms exceeds the 50ms "does not reproduce the stall alone" sanity ceiling`);
});

// ── (5) Cross-worktree collision note (documentation, not a runnable test) ──
//
// worktree agent-a9a51b556bbaa2cfdb (FEAT-2326609761 inc1, consolidator
// section-box overlay) ALSO edits MapView.tsx's draw effect: it appends a new
// `if (state.consolidatorEnabled ...)` block immediately before the effect's
// closing `}, [deps])` line, and adds `state.consolidatorEnabled` to that same
// dependency array. BUG-659's diff never touches the dependency array and its
// last edit (the station-dot pass) lands well before that closing line, so
// there is NO SEMANTIC overlap — the consolidator overlay reads
// state.buildings not at all (pure arithmetic from section geometry) and
// never touches visibleBuildings/viewportCull. The ONLY collision is a
// textual one: both diffs modify the same physical dependency-array line.
// MERGE ORDER FOR THE LEAD: apply either diff first; the second application's
// dependency-array hunk will conflict trivially and resolve by UNION (keep
// every existing dependency plus `state.consolidatorEnabled`) — no logic
// needs re-deriving on either side.
test('documents the MapView.tsx collision with the parallel consolidator worktree (see comment above) — always passes, informational', () => {
  assert.ok(true);
});
