// attack-bug852-retry.test.mjs — independent destructive round on BUG-852's
// RETRY (attacker opus-round-bug852-retry, 2026-09-10; GR#23, attacker is
// never the author).
//
// WHY THIS FILE EXISTS: the retry rewrote nearestSegmentWeights's internals
// to stamp-tagged flat Int32Array buffers indexed by `y * MAP_W + x`. That
// makes the SEED bounds check load-bearing in a way it was not before: an
// off-map seed key is no longer merely an inert string in a Map, its index
// arithmetic ALIASES a real on-map tile (x = -1 on row y wraps into the last
// column of row y-1, since (y * MAP_W) + (-1) === ((y-1) * MAP_W) + (MAP_W-1)).
//
// The retry's own BUG-866 pin asserts only the `offMapSeedsDropped` COUNTER,
// so a mutant that keeps the counter increment and deletes the `continue`
// (i.e. counts the seed and then admits it anyway) SURVIVED the whole suite
// while silently attributing weight to an undefined segment id. Measured
// live this round via the GR#24 scratch-copy method (cp the file to a
// scratchpad .bak, mutate the real file, run, restore from the .bak — never
// a git command):
//   unmutated : weightBySegment { 'seg-real' => 5 }, attributed 1, dropped 1
//   mutant    : weightBySegment { null => 18, 'seg-real' => 5 }, attributed 3, dropped 1
// This test pins the DROP, not just the count, so that mutant reds.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  nearestSegmentWeights,
  forecastSegmentUsage,
  __setBfsStampCounterForTest,
} from '../src/sim/trafficDemand.ts';
import { initialState } from '../src/sim/engine.ts';
import { MAP_W } from '../src/sim/grid.ts';

test('BUG-852 retry attack: an off-map SEED is DROPPED, not merely counted — its aliased on-map tile index attributes nothing', () => {
  // '-1,5' is off-map. Under the flat-index scheme its would-be index is
  // 5 * MAP_W + (-1), which is the SAME slot as the on-map tile
  // (MAP_W - 1, 4) — proven here from MAP_W itself rather than a literal, so
  // the aliasing this pin guards against is stated, not assumed.
  const aliasX = MAP_W - 1;
  const aliasY = 4;
  assert.equal(5 * MAP_W + -1, aliasY * MAP_W + aliasX, 'sanity: the off-map seed really does alias an on-map tile index');

  const offMapSeed = '-1,5';
  const realSeed = '600,300';
  const sourceTileKeys = [offMapSeed, realSeed];
  const tileToSegment = new Map([[realSeed, 'seg-real']]);
  // Demand sitting on the ALIASED tile and its neighbours: if the off-map
  // seed were admitted, its BFS would flood from the aliased tile and
  // attribute all of this to `tileToSegment.get('-1,5')` — which is
  // `undefined`.
  const tileWeight = new Map([
    [`${aliasX},${aliasY}`, 9],
    [`${aliasX},${aliasY + 1}`, 9],
    [`${aliasX - 1},${aliasY}`, 9],
    [realSeed, 5],
  ]);

  const r = nearestSegmentWeights(sourceTileKeys, tileToSegment, tileWeight, 3);

  assert.equal(r.offMapSeedsDropped, 1, 'the off-map seed is counted (BUG-866/BUG-882)');
  assert.equal(
    [...r.weightBySegment.keys()].filter((k) => k === undefined || k === null).length,
    0,
    'no weight may be attributed to an undefined/null segment id — that is the signature of an admitted off-map seed',
  );
  assert.deepEqual(
    [...r.weightBySegment.entries()],
    [['seg-real', 5]],
    'only the real on-map seed attributes anything; the off-map seed contributes nothing via its aliased index',
  );
  assert.equal(r.attributedTileCount, 1, 'the aliased tile and its neighbours are NOT attributed');
  // MUTANT (offmap_seed_counted_but_admitted): delete the `continue;` from
  // nearestSegmentWeights's seed bounds-check block, keeping both counter
  // increments. Reds the deepEqual and the attributedTileCount assertion
  // above (null => 18, attributed 3); the pre-existing BUG-866 pin does not
  // red, because `offMapSeedsDropped` is still 1. Proven live this round.
});

// ===========================================================================
// BUG-896 round (attacker opus-round-bug896, 2026-09-10; GR#23, the attacker
// is never the author) - independent pins on the bfsStampCounter wrap guard.
//
// All three tests below force the guard to fire FIRST (a throwaway call with
// the counter pushed past the ceiling), so both scratch arrays are provably
// all-zero - byte-identical to a fresh process - before the fixture runs.
// That prologue/epilogue shape is deliberate and load-bearing: simply
// setting the counter back to 0 does NOT restore the module, it leaves
// exactly the stale small stamps the guard exists to erase (measured this
// round: { P: 334, Q: 324 }/329 degrades to { P: 206 }/103).
const GUARD_TRIP = 2_000_000_001; // > BFS_STAMP_COUNTER_SAFE_CEILING (2e9), which is module-private
function resetBfsScratchViaGuard() {
  __setBfsStampCounterForTest(GUARD_TRIP);
  // A one-tile, radius-0 call in a far corner: fires the guard (both arrays
  // .fill(0), counter back to 0) and then stamps exactly one tile.
  nearestSegmentWeights(['5,300'], new Map([['5,300', 'Z']]), new Map(), 0);
}
function sortedPairs(m) {
  return [...m.entries()].sort((a, b) => (a[0] < b[0] ? -1 : 1));
}

test('BUG-896 attack: the wrap guard holds at ceiling-1 / ceiling / ceiling+1 AND at the raw Int32 boundary, with the LARGEST radius a call can ever use', () => {
  // MAX_ATTRIBUTION_RADIUS_TILES is 250, so a single call mints at most
  // 1 + 250 = 251 stamps. The author's own pin uses radius 25; this one uses
  // the worst case, which is the only radius that can prove the ceiling
  // leaves enough headroom for a call STARTING just under it never to cross
  // 2^31-1 mid-call (2_000_000_000 + 251 << 2_147_483_647).
  const keys = ['100,100', '140,100'];
  const seg = new Map([['100,100', 'A'], ['140,100', 'B']]);
  const weight = new Map();
  for (let x = 60; x < 200; x++) for (let dy = -3; dy <= 3; dy++) weight.set(`${x},${100 + dy}`, 1 + ((x * 7 + dy) % 5));
  const radius = 250;

  const run = (startCounter) => {
    resetBfsScratchViaGuard();
    __setBfsStampCounterForTest(startCounter);
    const r = nearestSegmentWeights(keys, seg, weight, radius);
    return { w: sortedPairs(r.weightBySegment), n: r.attributedTileCount };
  };

  const base = run(0);
  assert.deepEqual(base.w, [['A', 1282], ['B', 1658]], 'baseline weights (counter at 0) - literal, so a silent change to the BFS itself also reds this pin');
  assert.equal(base.n, 980, 'baseline attributedTileCount');
  for (const start of [1_999_999_999, 2_000_000_000, 2_000_000_001, 2_147_483_396, 2_147_483_630, 2_147_483_646, 2_147_483_647]) {
    const r = run(start);
    assert.deepEqual(r.w, base.w, `weights must be identical with the counter starting at ${start}`);
    assert.equal(r.n, base.n, `attributedTileCount must be identical with the counter starting at ${start}`);
  }
  // MUTANTS proven RED live this round (GR#24 scratch-copy method - cp the
  // file to a scratchpad .bak, mutate the real file, run, restore from the
  // .bak; never a git command): (1) the whole guard block deleted; (2) the
  // ceiling raised to 2_147_483_647 so it fires only after the wrap; (3)
  // `bfsStampCounter = 0` dropped from the reset. `>` -> `>=` is an
  // EQUIVALENT mutant (it merely resets one call earlier) and is
  // deliberately NOT claimed as covered.
  resetBfsScratchViaGuard();
});

test('BUG-896 attack: after the guard fires the module is byte-identical to a FRESH PROCESS - BOTH stamp arrays must be wiped, not just finalStamp', () => {
  // The reset restarts the counter at 0, so the very next call mints
  // callStamp 1 and layer stamps 2,3,4... - exactly the values a process's
  // FIRST calls minted. If either array keeps its stale contents those old
  // small stamps alias the new ones: a stale finalStamp === callStamp makes
  // a seed read as already-finalised (dropped, never attributed), and a
  // stale tentStamp === layerStamp makes a neighbour read as
  // already-tentative-this-layer (never pushed onto `touched`, so never
  // finalised and its demand vanishes).
  const keysA = ['100,100', '130,100'];
  const segA = new Map([['100,100', 'A'], ['130,100', 'B']]);
  const wA = new Map();
  for (let x = 90; x < 150; x++) for (let dy = -6; dy <= 6; dy++) wA.set(`${x},${100 + dy}`, 1);
  const keysB = ['110,104', '126,104'];
  const segB = new Map([['110,104', 'P'], ['126,104', 'Q']]);
  const wB = new Map();
  for (let x = 100; x < 140; x++) for (let dy = -5; dy <= 5; dy++) wB.set(`${x},${104 + dy}`, 2);

  resetBfsScratchViaGuard();
  __setBfsStampCounterForTest(0);
  nearestSegmentWeights(keysA, segA, wA, 12); // leaves SMALL stamps (callStamp 1, layers 2..13)
  __setBfsStampCounterForTest(GUARD_TRIP);
  const after = nearestSegmentWeights(keysB, segB, wB, 10); // the guard fires here

  // Truth measured in a DEDICATED fresh node process this round, where the
  // B fixture was the first nearestSegmentWeights call ever made.
  assert.deepEqual(sortedPairs(after.weightBySegment), [['P', 334], ['Q', 324]], 'post-reset result must equal the fresh-process result');
  assert.equal(after.attributedTileCount, 329, 'post-reset attributedTileCount must equal the fresh-process value');
  // MUTANTS proven RED live this round on this exact sequence:
  //   tentStamp.fill(0)  removed -> { P: 276, Q: 180 }, attributed 228
  //   finalStamp.fill(0) removed -> { P: 206 },         attributed 103
  resetBfsScratchViaGuard();
});

test('BUG-896 attack: forecastSegmentUsage is byte-identical across three FRESH states when the counter is pushed past the ceiling before the middle run', () => {
  // The production-level determinism claim: the guard must not make output
  // depend on how many times this module has been called in the current
  // process (the hard-reset genesis-replay hazard the BOW item calls out).
  // forecastSegmentUsage is memoOnState, so every run gets a FRESH state
  // object - re-running against the same object would only read the memo.
  const buildings = [
    { id: 1, spec: 'rd_aroad', x: 10, y: 10 }, { id: 2, spec: 'rd_aroad', x: 11, y: 10 },
    { id: 3, spec: 'rd_aroad', x: 12, y: 10 }, { id: 4, spec: 'rd_aroad', x: 30, y: 10 },
    { id: 5, spec: 'rd_aroad', x: 31, y: 10 }, { id: 6, spec: 'res_hut', x: 10, y: 11 },
    { id: 7, spec: 'res_hut', x: 11, y: 11 }, { id: 8, spec: 'res_hut', x: 30, y: 11 },
    { id: 9, spec: 'off_suite', x: 20, y: 12 },
  ];
  const freshState = () => ({ ...initialState(), unlockedAll: true, buildings, nextId: 100, roadNotice: null, population: 40 });
  const snap = (s) => JSON.stringify([...forecastSegmentUsage(s).entries()]);

  resetBfsScratchViaGuard();
  const run1 = snap(freshState());
  __setBfsStampCounterForTest(GUARD_TRIP); // the middle run straddles the reset
  const run2 = snap(freshState());
  const run3 = snap(freshState()); // the counter is small again - the normal path
  assert.equal(run2, run1, 'run 2 (straddling the wrap-guard reset) must be byte-identical to run 1');
  assert.equal(run3, run1, 'run 3 (immediately after the reset) must be byte-identical to run 1');
  assert.ok(run1.length > 2, 'sanity: the fixture really does produce segment usage, so this is not three empty strings compared');
  resetBfsScratchViaGuard();
});
