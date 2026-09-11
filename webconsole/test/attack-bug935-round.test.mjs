// attack-bug935-round.test.mjs — INDEPENDENT destructive round r1 for BUG-935
// (attacker opus-round-bug935, 2026-09-11). Verdict: REJECT.
//
// Run: node tools/test/scoped.mjs webconsole/test/attack-bug935-round.test.mjs
//
// Two kinds of pin live here:
//
//   (1) EQUIVALENCE FUZZ (green) — the primitive `nearestSourceForTiles` really
//       is byte-identical to `boundedNearestSourceMapOf(...).get(tileKey)` for
//       every realistic shape (ties incl. string-vs-numeric key order, radius
//       boundaries 0..5, duplicate + off-map sources, map corners, random
//       cities, a dense equidistant cluster). Keep this pin for life: it is
//       what makes the GR#21 determinism/equivalence claim mechanical instead
//       of prose. It also pins the TWO deviations the round found, so a future
//       change to either is visible.
//
//   (2) STALE-CACHE REPRODUCTION (RED on the r1 tree — this is the blocker).
//       `nearestRoadSegmentTileMapOf` caches on `s.buildings`' array identity
//       (BUG-912's perf fix), which was SOUND only because the cached body read
//       nothing but `s.buildings`. The BUG-935 change makes the body read
//       `demandForecastOf(s)` — population/occupancy/isOnline dependent, and it
//       changes every tick WITHOUT the buildings array changing. The cached map
//       therefore covers only the demand tiles that existed the first time that
//       buildings array was seen; every tile that joins the demand set later is
//       a `.get()` miss and its whole vehicle-trip load is dropped into
//       `unrouted: 'no-origin-segment'`. HEAD does not have this failure mode:
//       its map covers every reachable tile, so it is demand-independent.
//       This test must go GREEN before BUG-935 may be committed.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { boundedNearestSourceMapOf, nearestSourceForTiles, demandForecastOf } from '../src/sim/trafficDemand.ts';
import { assignedFlowOf, unroutedDemandOf, loadTrafficConfigFrom } from '../src/sim/trafficAssignment.ts';
import { loadMaxAttributionRadiusFrom } from '../src/sim/emergencyResponse.ts';
import { lineSegmentIndexOf } from '../src/sim/data.ts';
import { MAP_W, MAP_H } from '../src/sim/grid.ts';
import { initialState } from '../src/sim/engine.ts';

// --- fixture helpers (same OFFSET idiom as trafficAssignment.test.mjs) ------
const OFFSET = 200;
function rd(id, spec, x, y) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET, builtTick: 0 };
}
function bldg(id, spec, x, y) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET };
}
function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}

// ---------------------------------------------------------------------------
// (1) EQUIVALENCE FUZZ — nearestSourceForTiles vs the untouched flood
// ---------------------------------------------------------------------------

function mulberry32(a) {
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Compare NEW against the OLD flood for every query key; collect divergences. */
function collectDivergences(sources, queries, radius, label, out) {
  const ref = boundedNearestSourceMapOf([...sources], radius);
  const got = nearestSourceForTiles(queries, sources, radius);
  for (const q of queries) {
    if (ref.get(q) !== got.get(q)) out.push(`${label} q=${q} r=${radius} OLD=${ref.get(q)} NEW=${got.get(q)}`);
  }
  const asked = new Set(queries);
  for (const k of got.keys()) if (!asked.has(k)) out.push(`${label} invented unasked key ${k}`);
}

test('BUG-935 r1 pin: nearestSourceForTiles === boundedNearestSourceMapOf(...).get(tile) on every realistic shape', () => {
  const d = [];
  // string-vs-numeric tie-break disagreement: "100,5" sorts BEFORE "20,5" as a
  // STRING, after it numerically. The flood's tie-break is the string compare
  // (`src < existing` on tileKeys, seeds sorted with a default `.sort()`), so a
  // numeric-order rewrite of either side reds here.
  collectDivergences(['100,5', '20,5'], ['60,5'], 250, 'tiebreak-string', d);
  // a query tile that IS a source (the radius-0 self-map the fixer's own
  // mid-round correction was about — the flood's seed step runs even when its
  // while-loop body never does).
  collectDivergences(['9,5', '10,5'], ['9,5'], 250, 'tiebreak-self', d);
  // radius boundary: the flood runs `while (dist < radius)`, i.e. INCLUSIVE of
  // exactly `radius`. An off-by-one on either side reds at r=3 for q=13,10.
  for (const r of [0, 1, 2, 3, 4, 5]) {
    collectDivergences(['10,10'], ['13,10', '10,13', '12,12', '10,10'], r, `radius-${r}`, d);
  }
  // duplicate + off-map sources (BUG-864 seed bounds-check parity)
  collectDivergences(['10,10', '10,10', '-1,5', `${MAP_W},5`, '5,-1'], ['12,10', '0,0'], 250, 'dup-offmap-src', d);
  // map corners/edges — the flood clips at the edge, Manhattan does not, so
  // this is where a detour would show up if the grid were not open.
  collectDivergences(
    ['0,0', `${MAP_W - 1},${MAP_H - 1}`, `0,${MAP_H - 1}`, `${MAP_W - 1},0`],
    ['1,1', '5,300', '600,300', `${MAP_W - 1},0`],
    250,
    'corners',
    d,
  );
  // random cities: 6 trials x 120 sources x 200 queries across 5 radii
  const rnd = mulberry32(12345);
  for (let trial = 0; trial < 6; trial++) {
    const srcs = new Set();
    for (let i = 0; i < 120; i++) srcs.add(`${Math.floor(rnd() * MAP_W)},${Math.floor(rnd() * MAP_H)}`);
    const qs = [];
    for (let i = 0; i < 200; i++) qs.push(`${Math.floor(rnd() * MAP_W)},${Math.floor(rnd() * MAP_H)}`);
    collectDivergences([...srcs], qs, [0, 1, 5, 30, 250][trial % 5], `rand-${trial}`, d);
  }
  // dense cluster: 529 query tiles over a 10x10 source lattice at 6 radii —
  // thousands of exact distance ties, the hardest case for the tie-break claim.
  const clusterSrcs = [];
  for (let x = 100; x < 110; x++) for (let y = 100; y < 110; y++) if ((x + y) % 3 === 0) clusterSrcs.push(`${x},${y}`);
  const clusterQs = [];
  for (let x = 95; x < 118; x++) for (let y = 95; y < 118; y++) clusterQs.push(`${x},${y}`);
  for (const r of [0, 1, 2, 3, 7, 20]) collectDivergences(clusterSrcs, clusterQs, r, `cluster-r${r}`, d);

  assert.deepEqual(d, [], `OLD-vs-NEW divergences:\n${d.join('\n')}`);
});

test('BUG-935 r2 rework closes BUG-959: nearestSourceForTiles now matches the flood exactly on negative radius and off-map query tiles (was: two documented/undocumented deviations on the r1 tree)', () => {
  // Deviation A (was documented in nearestSourceForTiles' own r1 comment,
  // closed by BUG-959): a NEGATIVE radius. The flood still seeds every
  // on-map source to itself at distance 0 even though its expansion loop
  // never runs; the r2 fix now matches that exactly instead of returning an
  // empty map.
  assert.equal(boundedNearestSourceMapOf(['10,10'], -1).get('10,10'), '10,10');
  assert.equal(nearestSourceForTiles(['10,10'], ['10,10'], -1).get('10,10'), '10,10');

  // Deviation B (was NOT documented on the r1 tree, closed by BUG-959): the
  // r1 function bounds-checked SOURCE keys but not QUERY keys, so an
  // off-map query tile got a fabricated answer where the flood had none
  // (the flood's `visited` map can only ever contain tiles it actually
  // reached). The r2 fix bounds-checks the query tile too.
  assert.equal(boundedNearestSourceMapOf(['10,10'], 250).get('-1,10'), undefined);
  assert.equal(nearestSourceForTiles(['-1,10'], ['10,10'], 250).get('-1,10'), undefined);
});

// ---------------------------------------------------------------------------
// (2) BLOCKER REPRODUCTION — stale buildings-keyed cache (RED on the r1 tree)
// ---------------------------------------------------------------------------

test('BUG-935 r1 BLOCKER: a demand tile that joins the forecast without a construction event is dropped as "no-origin-segment" — the nearest-segment map is cached on s.buildings but its query set is now demand-dependent', () => {
  const buildings = [
    rd(1, 'rd_aroad', 0, 0),
    rd(2, 'rd_aroad', 1, 0),
    rd(3, 'rd_aroad', 2, 0),
    bldg(4, 'res_hut', 0, 1),
    bldg(5, 'off_suite', 2, 1),
  ];

  // Tick N: population 0 -> demandForecastOf() is EMPTY (no resident/worker
  // occupancy => personTrips 0 for every tile => every tile filtered out).
  // This warms nearestRoadSegmentTileMapByBuildings with an EMPTY query set.
  const cold = board(buildings, 0);
  assert.equal(demandForecastOf(cold).length, 0, 'fixture guard: the cold tick must have zero demand tiles');
  assignedFlowOf(cold);

  // Tick N+1: SAME buildings array (the player built nothing — BUG-912's own
  // comment: "s is a fresh object every tick but s.buildings usually is not"),
  // population has grown. Two tiles now carry demand.
  const hot = board(buildings, 50000);
  assert.equal(demandForecastOf(hot).length, 2, 'fixture guard: the hot tick must have two demand tiles');

  // Control: byte-identical state content, but a FRESH buildings array, so the
  // cache misses and the map is computed against the hot demand set. This is
  // what HEAD produces on BOTH states (HEAD's map is demand-independent: it
  // covers every reachable tile).
  const control = board(buildings.map((b) => ({ ...b })), 50000);

  // HEAD-semantics cross-check: the flood the change replaced DOES contain an
  // origin for the res_hut tile, regardless of any demand set.
  const idx = lineSegmentIndexOf(hot);
  const roadTileKeys = [...idx.tileToSegment]
    .filter(([, segId]) => idx.segmentById.get(segId)?.kind === 'road')
    .map(([tileKey]) => tileKey);
  assert.ok(
    boundedNearestSourceMapOf(roadTileKeys, 250).get(`${OFFSET},${OFFSET + 1}`),
    'HEAD-semantics guard: the full flood always has an origin segment for the demand tile',
  );

  assert.deepEqual(
    unroutedDemandOf(hot),
    unroutedDemandOf(control),
    'a cache-warmed tick must route exactly like a cold one — same state content, same result',
  );
  assert.deepEqual(
    [...assignedFlowOf(hot)].sort(),
    [...assignedFlowOf(control)].sort(),
    'assigned flow must not depend on which earlier state warmed the buildings-keyed cache',
  );
});

// ===========================================================================
// ROUND 2 (attacker opus-reround-bug935, 2026-09-11). Verdict: REJECT.
// The r1 P1 (BUG-958) IS fixed, and the fix is byte-identical to HEAD
// (63b8174) across every fixture this round measured — but the ops-fallback
// threshold the r2 rework introduced is set ABOVE the real crossover, so a
// mid/large city takes the O(queries x sources) branch and the whole traffic
// snapshot becomes 2.5-4.5x SLOWER THAN HEAD. Blocker: BUG-967 (P1).
// ===========================================================================

import { readFileSync as readFileSyncR2 } from 'node:fs';
import pathR2 from 'node:path';
import { fileURLToPath as fileURLToPathR2 } from 'node:url';
const repoRootR2 = pathR2.resolve(pathR2.dirname(fileURLToPathR2(import.meta.url)), '..', '..');
const trafficR2 = JSON.parse(readFileSyncR2(pathR2.join(repoRootR2, 'data', 'traffic.json'), 'utf8'));

// ---------------------------------------------------------------------------
// (3) GREEN — multi-tile buildings + a reused buildings array (BUG-958 fix)
// ---------------------------------------------------------------------------

test('BUG-935 r2 pin: a city of MULTI-TILE buildings (2x2 / 2x1 / 2x3 / 4x4 footprints) plus all three emergency services routes identically on a REUSED buildings array and on a fresh one, at every population', () => {
  // Every consumer of the two nearest-segment maps queries an ANCHOR tile
  // (emergencyResponse `${b.x},${b.y}`, assignmentOf `${t.x},${t.y}` where
  // demandForecastOf pushes b.x,b.y straight off the building), never a
  // non-anchor footprint tile — so `s.buildings.map(anchor)` really is a
  // superset of the queried set even for a 4x4 building. This pin is the
  // mechanical form of that argument, and of BUG-958's cache invariant.
  const B = [];
  let id = 0;
  const OFF2 = 200;
  for (let x = 0; x < 20; x++) B.push({ id: ++id, spec: 'rd_aroad', x: OFF2 + x, y: OFF2 + 10, builtTick: 0 });
  for (let y = 0; y < 20; y++) B.push({ id: ++id, spec: 'rd_aroad', x: OFF2 + 10, y: OFF2 + y, builtTick: 0 });
  B.push({ id: ++id, spec: 'res_block', x: OFF2 + 2, y: OFF2 + 8 });        // 2x2
  B.push({ id: ++id, spec: 'res_highrise', x: OFF2 + 5, y: OFF2 + 11 });    // 2x2
  B.push({ id: ++id, spec: 'off_tower', x: OFF2 + 12, y: OFF2 + 8 });       // 2x3
  B.push({ id: ++id, spec: 'lei_themepark', x: OFF2 + 14, y: OFF2 + 12 });  // 4x4
  B.push({ id: ++id, spec: 'hea_hospital', x: OFF2 + 8, y: OFF2 + 12 });    // 2x2
  B.push({ id: ++id, spec: 'hea_ambulance', x: OFF2 + 9, y: OFF2 + 9 });    // ambulance station
  B.push({ id: ++id, spec: 'fire_station', x: OFF2 + 11, y: OFF2 + 14 });   // 2x1 fire station
  B.push({ id: ++id, spec: 'pol_station', x: OFF2 + 3, y: OFF2 + 11 });     // 2x1 police station
  B.push({ id: ++id, spec: 'ind_heavy', x: OFF2 + 16, y: OFF2 + 9 });       // 3x3 freight

  // Warm the buildings-identity cache with the SMALLEST possible demand set.
  assignedFlowOf(board(B, 0));
  for (const pop of [5000, 50000, 400000]) {
    const hot = board(B, pop); // reused array — cache hit
    const control = board(B.map((b) => ({ ...b })), pop); // fresh array — cache miss
    assert.deepEqual(
      [...assignedFlowOf(hot)].sort(),
      [...assignedFlowOf(control)].sort(),
      `assigned flow diverged at pop ${pop}`,
    );
    assert.deepEqual(unroutedDemandOf(hot), unroutedDemandOf(control), `unrouted demand diverged at pop ${pop}`);
  }
  // Non-vacuity guard: this fixture must actually route something.
  const live = board(B, 400000);
  assert.ok(demandForecastOf(live).length > 0, 'fixture guard: the hot state must have demand tiles');
  assert.ok(assignedFlowOf(live).size > 0, 'fixture guard: the hot state must assign flow to at least one segment');
});

// ---------------------------------------------------------------------------
// (4) BLOCKER — BUG-967 (P1): the ops-fallback threshold does not engage where
//     the crossover actually is. RED until the threshold is re-derived.
// ---------------------------------------------------------------------------

test('BUG-967 (r2 BLOCKER): nearestSourceForTilesOpsFallbackThreshold must be low enough that a realistic 20,000-building grid city takes the FLOOD branch — measured, the primitive branch costs 3.08x the flood at that size', () => {
  // Measured this round (direct A/B, one process, same inputs, radius 250):
  //   200x100 grid city, roads every 5th row AND column
  //     -> 20,000 query tiles x 7,200 road source tiles = 144,000,000 ops
  //     -> nearestSourceForTiles 417ms vs boundedNearestSourceMapOf 135ms (3.08x)
  //     -> full computeTrafficSnapshot: HEAD 389-481ms vs lane 1,200-1,770ms
  //   120x75 grid city -> 9,000 x 3,240 = 29,160,000 ops -> 95ms vs 55ms (1.72x)
  // i.e. the flood already wins BELOW 30M ops for a dense road network — an
  // order of magnitude under the 225M-400M crossover the rework recorded.
  // This pin is timing-FREE on purpose (no wall-clock assertion in CI, per
  // metropolis-verification-standards): it compares the shipped threshold
  // against the ops figure the shipped code computes for that city.
  const QUERY_TILES_20K = 20000;
  const ROAD_SOURCE_TILES_20K = 7200;
  const ops20k = QUERY_TILES_20K * ROAD_SOURCE_TILES_20K;
  assert.equal(ops20k, 144000000, 'arithmetic guard for the measured fixture');
  assert.ok(
    trafficR2.nearestSourceForTilesOpsFallbackThreshold < ops20k,
    `threshold ${trafficR2.nearestSourceForTilesOpsFallbackThreshold} must be below ${ops20k} so a 20,000-building city falls back to the flood instead of paying 3.08x its cost`,
  );
});

// ===========================================================================
// ROUND 3 REWORK (2026-09-11), closing BUG-967. r3 re-measured the primitive
// vs the flood on the SAME dense-grid shape (roads on a 1-in-5 row/column
// lattice) at more points between the round's 29.16M ("still faster") and
// 144M ("3.08x slower") readings, and found the real crossover sits between
// 29.16M and 70.7M — an order of magnitude under the r2 rework's 150,000,000
// figure. nearestSourceForTilesOpsFallbackThreshold ships at 20,000,000 (real
// margin below even the 29.16M favourable point; see data/traffic.json's own
// _nearestSourceForTilesOpsFallbackThresholdSource note for the full table).
// This is the LEAD's r3 bar (BUG-967 amendment): "a pin asserts the shipped
// value is <= 30,000,000" — timing-free, on purpose (metropolis-
// verification-standards: no wall-clock bound in CI).
// ===========================================================================

test('BUG-967 r3 FIX: nearestSourceForTilesOpsFallbackThreshold ships at <= 30,000,000 (the lead\'s r3 bar) and still below the 20,000-building/144,000,000-op crossover the round measured', () => {
  const v = trafficR2.nearestSourceForTilesOpsFallbackThreshold;
  assert.ok(
    typeof v === 'number' && Number.isFinite(v) && v > 0,
    `nearestSourceForTilesOpsFallbackThreshold must be a positive finite number, got ${JSON.stringify(v)}`,
  );
  assert.ok(v <= 30000000, `shipped threshold ${v} must be <= 30,000,000 per the r3 lead amendment`);
  // Re-assert against the round's OWN adversarial fixtures (belt + braces —
  // this is the same computation the r2 test above already pins, kept
  // explicit here so a future edit to either test still catches a
  // regression on both real measured city sizes from this round's evidence).
  assert.ok(v < 29160000, `shipped threshold ${v} must also clear the round's own 9,000-building/29,160,000-op reading (measured 0.89x-1.72x depending on run — too close to the crossover to trust as "still faster")`);
});

// ---------------------------------------------------------------------------
// (5) GREEN — BUG-968 (P3): two radius-domain values where the r2 primitive
//     still does NOT match the flood, and where the function's OWN two
//     branches disagree with each other. Documented, pinned, visible.
// ---------------------------------------------------------------------------

test('BUG-968 (r2 P3): a FRACTIONAL or +Infinity radius still diverges from boundedNearestSourceMapOf — the r2 comment\'s "matches the flood EXACTLY" claim holds for negative/NaN only', () => {
  // Fractional: the flood runs `while (dist < radius)`, so at radius 2.5 it
  // expands layer 2 and ADMITS tiles at distance 3; the primitive's
  // `d > radius` excludes them. Reachable by DATA, not only in theory —
  // neither maxAttributionRadiusTiles loader requires an integer, so
  // data/traffic.json: 250.5 makes this live for any city whose bounding-box
  // diameter exceeds it.
  assert.equal(boundedNearestSourceMapOf(['10,10'], 2.5).get('13,10'), '10,10');
  assert.equal(nearestSourceForTiles(['13,10'], ['10,10'], 2.5).get('13,10'), undefined);
  // +Infinity: `Number.isFinite(Infinity)` is false, so the r2 rework
  // collapses it to effectiveRadius 0 (seeds only) — but the flood's
  // `dist < Infinity` is ALWAYS true, i.e. an unbounded flood reaching every
  // tile. Opposite ends of the domain, same bucket.
  assert.equal(boundedNearestSourceMapOf(['10,10'], Infinity).get('500,300'), '10,10');
  assert.equal(nearestSourceForTiles(['500,300'], ['10,10'], Infinity).get('500,300'), undefined);
});

test('BUG-968 (r2 P3): the ops-fallback branch and the primitive branch of nearestSourceForTiles answer the SAME fractional-radius question differently — "only the cost model changes" does not hold on this edge', () => {
  const threshold = trafficR2.nearestSourceForTilesOpsFallbackThreshold;
  // Small input -> primitive branch.
  assert.equal(nearestSourceForTiles(['13,10'], ['10,10'], 2.5).get('13,10'), undefined);
  // Same question, padded past the ops threshold so the SAME call takes the
  // flood-fallback branch instead — and now answers '10,10'.
  const sources = ['10,10'];
  for (let i = 1; i < 40; i++) sources.push(`${500 + i},300`); // far away, never nearest
  const queries = ['13,10'];
  const need = Math.ceil(threshold / sources.length) + 1;
  for (let i = 0; i < need; i++) queries.push(`${i % MAP_W},${(300 + i) % MAP_H}`);
  assert.ok(queries.length * sources.length > threshold, 'guard: this input must cross the ops threshold');
  assert.equal(nearestSourceForTiles(queries, sources, 2.5).get('13,10'), '10,10');
});

// ===========================================================================
// ROUND 3 REWORK (2026-09-11), closing BUG-968 at the LOADER (lead's r3
// ruling): "the radius loader coerces to an integer with Math.floor and
// rejects non-finite via the existing registry error". The two exported pure
// loaders (loadTrafficConfigFrom in trafficAssignment.ts, loadMaxAttribution
// RadiusFrom in emergencyResponse.ts) now floor a fractional
// maxAttributionRadiusTiles to an integer, so both branches of
// nearestSourceForTiles agree by construction once the radius reaches them
// (the primitive/flood divergence on a raw fractional input, pinned above by
// the r2 round's own tests, stays true of the PRIMITIVE in isolation — that
// pin is deliberately kept as documentation of why the loader-side floor is
// the real fix, not a primitive rewrite). +Infinity was already rejected by
// `Number.isFinite` before this rework; pinned here explicitly per the r3
// ruling so a future relaxation of that check is caught.
// ===========================================================================

test('BUG-968 r3 FIX: loadTrafficConfigFrom floors a fractional maxAttributionRadiusTiles to an integer (2.5 -> 2)', () => {
  const raw = {
    baseCommuteHours: 5,
    baseAccessMinutes: 15,
    baseCommuteMinutes: 30,
    bprAlpha: 0.15,
    bprBeta: 4,
    webconsoleMetresPerTile: 50,
    maxAttributionRadiusTiles: 2.5,
    metresPerMile: 1609.34,
  };
  const cfg = loadTrafficConfigFrom(raw);
  assert.equal(cfg.maxAttributionRadiusTiles, 2, 'a fractional radius must floor to an integer, not pass through unchanged');
});

test('BUG-968 r3 FIX: loadTrafficConfigFrom rejects a non-finite (+Infinity/NaN) maxAttributionRadiusTiles with the existing registry error (unchanged behaviour, pinned explicitly per the r3 ruling)', () => {
  const base = {
    baseCommuteHours: 5,
    baseAccessMinutes: 15,
    baseCommuteMinutes: 30,
    bprAlpha: 0.15,
    bprBeta: 4,
    webconsoleMetresPerTile: 50,
    metresPerMile: 1609.34,
  };
  assert.throws(() => loadTrafficConfigFrom({ ...base, maxAttributionRadiusTiles: Infinity }), /MET-/);
  assert.throws(() => loadTrafficConfigFrom({ ...base, maxAttributionRadiusTiles: NaN }), /MET-/);
});

test('BUG-968 r3 FIX: loadMaxAttributionRadiusFrom (emergencyResponse.ts) floors a fractional radius to an integer (2.5 -> 2) and rejects +Infinity/NaN', () => {
  assert.equal(loadMaxAttributionRadiusFrom({ maxAttributionRadiusTiles: 2.5 }), 2);
  assert.throws(() => loadMaxAttributionRadiusFrom({ maxAttributionRadiusTiles: Infinity }), /MET-/);
  assert.throws(() => loadMaxAttributionRadiusFrom({ maxAttributionRadiusTiles: NaN }), /MET-/);
});

test('BUG-968 r3 FIX: both real loaders now agree with each other AND with nearestSourceForTiles on a fractional shipped radius (once floored, primitive === flood)', () => {
  const cfg = loadTrafficConfigFrom({
    baseCommuteHours: 5,
    baseAccessMinutes: 15,
    baseCommuteMinutes: 30,
    bprAlpha: 0.15,
    bprBeta: 4,
    webconsoleMetresPerTile: 50,
    maxAttributionRadiusTiles: 3.9,
    metresPerMile: 1609.34,
  });
  const emergencyRadius = loadMaxAttributionRadiusFrom({ maxAttributionRadiusTiles: 3.9 });
  assert.equal(cfg.maxAttributionRadiusTiles, emergencyRadius, 'both loaders must floor identically');
  assert.equal(cfg.maxAttributionRadiusTiles, 3);
  // With the floored integer radius, primitive and flood now agree exactly
  // at the boundary that used to diverge (radius 3 admits distance 3 in
  // both — the flood's `dist < radius` INCLUSIVE semantics for an integer,
  // and the primitive's `d > radius` exclusion, agree on every integer d).
  assert.equal(
    nearestSourceForTiles(['13,10'], ['10,10'], cfg.maxAttributionRadiusTiles).get('13,10'),
    boundedNearestSourceMapOf(['10,10'], cfg.maxAttributionRadiusTiles).get('13,10'),
  );
});

// ===========================================================================
// ROUND 3 (opus-round3-bug935) — independent pins appended after the r3
// rework. Measured on this machine, HEAD 63b8174 restored into a scratch tree
// outside the repo and A/B'd against the lane tree in one process.
// ===========================================================================

test('BUG-967 r3 round pin: the shipped ops threshold sits at or below this round\'s OWN measured crossover (20,250,000 ops, primitive 0.95x the flood; at 29,160,000 ops it is 1.06x, slower)', () => {
  // Direct primitive-vs-flood A/B on the r2/r3 adversarial dense-grid shape
  // (roads on a 1-in-5 row/column lattice), radius 250, 7 reps, warm, lowest
  // third dropped, 0 result divergences at every point:
  //   3,294,225 ops  primitive  29.2ms  flood 327.6ms  0.09x
  //   6,426,225 ops  primitive  69.5ms  flood 285.7ms  0.24x
  //  20,250,000 ops  primitive 350.1ms  flood 366.8ms  0.95x   <- still faster
  //  29,160,000 ops  primitive 374.3ms  flood 353.2ms  1.06x   <- already slower
  // and at each shape's REAL radius (min(bboxDiameter, 250)) the ordering is
  // the same (20,250,000 ops at radius 173: 219.0ms vs 233.7ms, 0.94x).
  // So the crossover on this machine is ~20-29M ops and 20,000,000 ships at
  // or just under it — no city size can take the primitive branch and be
  // slower than HEAD's flood. This is the timing-FREE form of that finding.
  const v = trafficR2.nearestSourceForTilesOpsFallbackThreshold;
  assert.ok(
    v <= 20250000,
    `shipped threshold ${v} must be <= 20,250,000 — the highest ops point at which opus-round3-bug935 measured the primitive still beating the flood (0.95x); above it the flood wins and the fallback must already have engaged`,
  );
});

test('BUG-969 r3 round pin: the New-Game perf fixture shape (initialState() + ten huts, population untouched) has ZERO demand tiles, so any wall-clock bound on it cannot detect a nearest-source regression — a population is what makes the bug live', () => {
  // Measured, same fixture, median of 5, fresh state per rep:
  //   population 0 (as the author fixture ships): HEAD first advance() 7.1ms,
  //     LANE 9.0ms — i.e. the <= 25ms assertion passes on HEAD TOO.
  //   population 50,000 on the IDENTICAL fixture: computeTrafficSnapshot
  //     HEAD 598.7ms vs LANE 33.9ms (17.7x, the honest win).
  // This pin is the timing-free mechanical statement of why: with no
  // population there is no demand tile, so neither nearest-source map is
  // ever queried and neither branch of the fix is reached.
  const base = initialState();
  assert.ok(base.buildings.length > 1000, `fixture guard: initialState() must carry its infra network, got ${base.buildings.length}`);
  let maxId = 0;
  for (const b of base.buildings) if (b.id > maxId) maxId = b.id;
  const withHuts = [...base.buildings];
  for (let i = 0; i < 10; i++) withHuts.push({ id: ++maxId, spec: 'res_hut', x: 5 + (i % 5), y: 5 + Math.floor(i / 5) });
  const asShipped = { ...base, unlockedAll: true, buildings: withHuts, nextId: maxId + 1 };
  assert.equal(base.population, 0, 'initialState() ships population 0 — the premise of this pin');
  assert.equal(
    demandForecastOf(asShipped).length,
    0,
    'the population-0 New Game fixture must have NO demand tiles — if this ever becomes nonzero the fixture has changed and its wall-clock bound must be re-derived',
  );
  const withPop = { ...asShipped, buildings: [...withHuts], population: 50000 };
  assert.ok(
    demandForecastOf(withPop).length > 0,
    'the SAME fixture with a population must have demand tiles — that is the state in which BUG-935 is reproducible (HEAD 598.7ms vs lane 33.9ms, measured)',
  );
});
