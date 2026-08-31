// ATTACK 3: Repeat-crossing abuse — place the same path twice
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';

function board(buildings) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1 };
}

test('ATTACK 3 FIXED: repeat-crossing deduped — same path laid twice (one-building-per-tile)', () => {
  let s = board([]);
  const path = [
    { x: 10, y: 10 },
    { x: 11, y: 10 },
    { x: 12, y: 10 },
  ];

  // First commit: place the path
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: path });

  const afterFirst = {
    roads: s.buildings.filter((b) => b.spec === 'road').length,
    junctions: s.buildings.filter((b) => b.spec === 'rd_junction').length,
    total: s.buildings.length,
  };

  console.log(`After first commit: ${afterFirst.roads} roads, ${afterFirst.junctions} junctions`);

  // Second commit: place the exact same path again (overlapping all tiles).
  // NEW BEHAVIOR: same-spec dedup (road over road) → skip, no placement, no junctions.
  // This prevents the old stacking attack (ATTACK 3).
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: path });

  const afterSecond = {
    roads: s.buildings.filter((b) => b.spec === 'road').length,
    junctions: s.buildings.filter((b) => b.spec === 'rd_junction').length,
    total: s.buildings.length,
  };

  console.log(`After second commit: ${afterSecond.roads} roads, ${afterSecond.junctions} junctions`);
  console.log(`Incremental: ${afterSecond.roads - afterFirst.roads} new roads, ${afterSecond.junctions - afterFirst.junctions} new junctions`);

  // FIXED BEHAVIOR: same-spec overlap is deduped.
  // Second commit places nothing (all 3 tiles are same-spec roads).
  assert.equal(
    afterSecond.roads,
    afterFirst.roads,
    `second commit deduped, no new roads`
  );
  assert.equal(
    afterSecond.junctions,
    0,
    `second commit deduped, no junctions created`
  );

  // ONE-BUILDING-PER-TILE INVARIANT: each tile has exactly 1 building
  const counts = {};
  for (const b of s.buildings) {
    const k = `${b.x},${b.y}`;
    counts[k] = (counts[k] ?? 0) + 1;
  }

  console.log(`Building counts per tile (should all be 1):`);
  for (const k of ['10,10', '11,10', '12,10']) {
    console.log(`  (${k}): ${counts[k] ?? 0} buildings`);
    assert.equal(counts[k], 1, `tile ${k} has exactly 1 building (no stacking)`);
  }

  console.log(`✓ ATTACK 3 FIXED: repeat-crossing deduped, one-building-per-tile invariant maintained`);
});
