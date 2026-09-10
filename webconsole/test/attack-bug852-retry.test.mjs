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
import { nearestSegmentWeights } from '../src/sim/trafficDemand.ts';
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
