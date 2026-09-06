// feat-2326609772-inc1-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND pins for
// FEAT-2326609772 inc1 (attacker: opus-round-feat772-inc1, GR#23 — not the
// author). These cover the gaps the author's own suite left open:
//
//  1. ADJACENCY IS 4-NEIGHBOUR, NOT 8. The author's suite places every run far
//     apart, so a mutation widening the flood to include the four diagonals
//     passed all 11 of its pins. AC-1's unit is a "contiguous connected run of
//     same-class tiles" reusing the existing 4-adjacent connectivity idiom;
//     two tiles touching only at a corner are NOT one road.
//     MUTANT: adding the 4 diagonal neighbours to the flood queue -> this reds.
//  2. A run must not merge across CLASSES (rd_aroad meeting rd_dual).
//     MUTANT: bucketing by "is an in-scope road" instead of by spec -> reds.
//  3. AC-2 apportionment safety on three unequal runs with a non-dividing
//     usage: sum-exact, no negative usage, no segment over its own capacity.
//     MUTANT: round()-per-segment, or remainder to a non-last segment -> reds.
//  4. GR#21: output is invariant under permutation of s.buildings (the author's
//     pin only reverses the array, and only compares one segmentId).
//     MUTANT: any Map/Set-iteration-order dependence in the flood -> reds.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { lineSegmentsOf, lineUsageOf } from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}
function run(spec, startId, x0, y0, n, horiz = true) {
  const out = [];
  for (let i = 0; i < n; i++)
    out.push({ id: startId + i, spec, x: horiz ? x0 + i : x0, y: horiz ? y0 : y0 + i, builtTick: 0 });
  return out;
}
const demand = [{ id: 9000, spec: 'res_hut', x: 0, y: 60, builtTick: 0 }];

test('AC-1: diagonally-touching tiles are SEPARATE segments (flood is 4-adjacent, not 8)', () => {
  // A staircase: every tile touches the next only at a corner. Under a correct
  // 4-adjacent flood this is 4 one-tile segments; under an 8-adjacent flood it
  // collapses to a single 4-tile segment.
  const bs = [
    { id: 1, spec: 'm20', x: 5, y: 5, builtTick: 0 },
    { id: 2, spec: 'm20', x: 6, y: 6, builtTick: 0 },
    { id: 3, spec: 'm20', x: 7, y: 7, builtTick: 0 },
    { id: 4, spec: 'm20', x: 8, y: 8, builtTick: 0 },
    ...demand,
  ];
  const segs = lineSegmentsOf(board(bs, 50000)).filter((x) => x.spec === 'm20');
  assert.equal(segs.length, 4, 'corner-touching tiles are four separate runs, not one');
  assert.deepEqual(segs.map((x) => x.tiles), [1, 1, 1, 1]);
});

test('AC-1: an L-bend of 4-adjacent tiles IS one run (the flood is not axis-restricted)', () => {
  // Guards the opposite mutation to the one above: narrowing the flood to a
  // single axis would split this L into two runs.
  const bs = [...run('m20', 1, 0, 0, 4), ...run('m20', 100, 3, 1, 3, false), ...demand];
  const segs = lineSegmentsOf(board(bs, 50000)).filter((x) => x.spec === 'm20');
  assert.equal(segs.length, 1, 'an L-bend is a single contiguous run');
  assert.equal(segs[0].tiles, 7);
});

test('AC-1: adjacent tiles of DIFFERENT classes never merge into one segment', () => {
  const bs = [...run('rd_aroad', 1, 0, 0, 4), ...run('rd_dual', 100, 4, 0, 4), ...demand];
  const segs = lineSegmentsOf(board(bs, 50000));
  const aroad = segs.filter((x) => x.spec === 'rd_aroad');
  const dual = segs.filter((x) => x.spec === 'rd_dual');
  assert.equal(aroad.length, 1);
  assert.equal(dual.length, 1);
  assert.equal(aroad[0].tiles, 4, 'the A-road run does not swallow the touching dual carriageway');
  assert.equal(dual[0].tiles, 4);
});

test('AC-2: three unequal runs, non-dividing usage — sum exact, no negative, none over its own capacity', () => {
  const bs = [
    ...run('m20', 1, 0, 0, 3),
    ...run('m20', 100, 20, 0, 7),
    ...run('m20', 200, 40, 0, 11),
    ...demand,
  ];
  const s = board(bs, 123457);
  const cls = lineUsageOf(s).find((x) => x.spec === 'm20');
  const segs = lineSegmentsOf(s).filter((x) => x.spec === 'm20');
  assert.equal(segs.length, 3);
  assert.equal(
    segs.reduce((a, x) => a + x.capacity, 0),
    cls.capacity,
    'segment capacities partition the class capacity exactly (no tile counted twice or lost)'
  );
  assert.equal(
    segs.reduce((a, x) => a + x.usage, 0),
    cls.usage,
    'AC-2 sum invariant across THREE runs with a remainder'
  );
  for (const x of segs) {
    assert.ok(x.usage >= 0, `segment ${x.segmentId} must never carry negative usage`);
    assert.ok(
      x.usage <= x.capacity,
      `segment ${x.segmentId} usage ${x.usage} must not exceed its own capacity ${x.capacity} here`
    );
    assert.equal(x.headroom, x.capacity - x.usage);
    assert.equal(x.overCapacity, x.headroom < 0);
  }
});

test('GR#21: output is byte-identical under permutations of s.buildings', () => {
  const bs = [
    ...run('m20', 1, 0, 0, 5),
    ...run('m20', 100, 20, 3, 3),
    ...run('rd_aroad', 200, 40, 7, 9),
    ...run('rd_dual', 300, 12, 12, 4, false),
    ...demand,
  ];
  const canonical = JSON.stringify(lineSegmentsOf(board(bs, 500000)));
  // Deterministic LCG shuffle — no Math.random in a CI test.
  let seed = 12345;
  const rnd = () => ((seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648);
  for (let k = 0; k < 25; k++) {
    const arr = [...bs];
    for (let i = arr.length - 1; i > 0; i--) {
      const j = Math.floor(rnd() * (i + 1));
      [arr[i], arr[j]] = [arr[j], arr[i]];
    }
    assert.equal(
      JSON.stringify(lineSegmentsOf(board(arr, 500000))),
      canonical,
      `permutation ${k} changed the segment output — buildings-array order leaked into the derivation`
    );
  }
});

test('memo: a NEW state with an extra road tile re-derives (no stale segment)', () => {
  const bs = [...run('m20', 1, 0, 0, 5), ...demand];
  const s1 = board(bs, 50000);
  assert.equal(lineSegmentsOf(s1).find((x) => x.spec === 'm20').tiles, 5);
  const s2 = { ...s1, buildings: [...bs, { id: 500, spec: 'm20', x: 5, y: 0, builtTick: 0 }] };
  assert.equal(
    lineSegmentsOf(s2).find((x) => x.spec === 'm20').tiles,
    6,
    'extending a run on a new state object must invalidate the memo'
  );
});
