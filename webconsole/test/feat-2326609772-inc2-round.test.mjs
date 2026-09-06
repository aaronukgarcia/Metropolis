// feat-2326609772-inc2-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND pins for
// FEAT-2326609772 inc2 (attacker: opus-round-feat772-inc2, not the author).
//
// Why this file exists: the round found that inc2's own AC-3 sum-invariant
// test is VACUOUS with respect to the rounding rule it claims to pin. Its
// fixture happens to produce a class usage that divides EXACTLY by the number
// of connected stations, so replacing the "floor-per-station, remainder on the
// LAST station" rule with a plain floor everywhere (which loses up to n-1 of
// commuter flow) left the whole inc2 suite GREEN (mutant E, survived 11/11).
// The pins below re-state the same invariants over fixtures whose remainder is
// PROVEN non-zero, and each asserts its own non-degeneracy precondition so the
// pin cannot quietly go vacuous again if the balance constants move.
//
// The round also established that the per-station ×3 Ashford weight is
// arithmetically INERT inside stationUtilisationOf (Ashford is the only
// weight-3 spec AND the only spec routed to 'hs1', so every class is
// weight-homogeneous and w/Σw collapses to 1/n): setting every weight to 1
// changed no output at all. The ×3 is observable only at the CLASS level, in
// lineUsageOf's hs1-vs-rail split — R3 pins stationUtilisationOf's numbers to
// exactly that ratio, so a change to the SSOT weight reds here too.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { lineSegmentsOf, lineUsageOf, stationUtilisationOf } from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}
const run = (spec, id0, x0, y, n) =>
  Array.from({ length: n }, (_, i) => ({ id: id0 + i, spec, x: x0 + i, y, builtTick: 0 }));
const roadNear = (id, x, y) => ({ id, spec: 'rd_aroad', x: x + 1, y, builtTick: 0 });

test('R1 — station sum invariant survives a NON-DIVISIBLE remainder (mutant E: floor-everywhere loses flow)', () => {
  const buildings = [
    ...run('rail', 1, 0, 20, 4),
    ...run('hs1', 10, 0, 30, 4),
    roadNear(30, 0, 0), { id: 31, spec: 'station_ashford', x: 0, y: 0, builtTick: 0 },
    roadNear(32, 0, 2), { id: 33, spec: 'station_ashford', x: 0, y: 2, builtTick: 0 },
    roadNear(34, 0, 4), { id: 35, spec: 'station_sanderling', x: 0, y: 4, builtTick: 0 },
    roadNear(36, 0, 6), { id: 37, spec: 'station_sanderling', x: 0, y: 6, builtTick: 0 },
    roadNear(38, 0, 8), { id: 39, spec: 'station_sanderling', x: 0, y: 8, builtTick: 0 },
  ];
  const s = board(buildings, 999999);
  const railCls = lineUsageOf(s).find((u) => u.spec === 'rail');
  assert.ok(railCls, 'rail class must exist');
  const railStats = stationUtilisationOf(s).filter((x) => x.lineSpec === 'rail');
  assert.equal(railStats.length, 3);
  // NON-DEGENERACY GUARD: if this ever divides exactly, the pin below stops
  // testing the rounding rule and must be re-fixtured (that is exactly how the
  // inc2 suite's own sum test went vacuous).
  assert.notEqual(railCls.usage % railStats.length, 0, 'fixture must leave a real remainder');
  const sum = railStats.reduce((a, x) => a + (x.utilisation ?? 0), 0);
  // MUTANT (verified to survive the whole inc2 suite before this pin existed):
  //   const u = Math.floor((cls.usage * sorted[i].weight) / totalWeight);   // no remainder-on-last
  // reds here, short by the remainder.
  assert.equal(sum, railCls.usage, 'connected rail stations must sum EXACTLY to the class usage');
  // The remainder lands on the LAST station by id — deterministic, as documented.
  const byId = [...railStats].sort((a, b) => a.id - b.id);
  assert.equal(byId[0].utilisation, byId[1].utilisation, 'equal-weight stations get equal floors');
  assert.ok(byId[2].utilisation > byId[0].utilisation, 'the last station by id carries the remainder');
});

test('R2 — segment sum invariant survives a NON-DIVISIBLE remainder across unequal rail runs', () => {
  const buildings = [
    ...run('rail', 1, 0, 0, 3),
    ...run('rail', 100, 0, 10, 7),
    ...run('rail', 200, 0, 20, 5),
    roadNear(300, 0, 0), { id: 301, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 },
    roadNear(302, 0, 10), { id: 303, spec: 'station_sanderling', x: 0, y: 10, builtTick: 0 },
  ];
  const s = board(buildings, 999999);
  const cls = lineUsageOf(s).find((u) => u.spec === 'rail');
  assert.ok(cls);
  const segs = lineSegmentsOf(s).filter((x) => x.spec === 'rail');
  assert.equal(segs.length, 3, 'three disconnected rail runs are three segments');
  const floors = segs.map((x) => Math.floor((cls.usage * x.capacity) / cls.capacity));
  // NON-DEGENERACY GUARD: the naive all-floors split must actually LOSE flow,
  // otherwise this pin cannot distinguish the two rounding rules.
  assert.notEqual(floors.reduce((a, b) => a + b, 0), cls.usage, 'fixture must leave a real remainder');
  const sum = segs.reduce((a, x) => a + x.usage, 0);
  // MUTANT: dropping the `isLast ? cls.usage - allocated : floor(...)` rule
  // for a plain floor on every segment reds here.
  assert.equal(sum, cls.usage, 'rail segment usages sum EXACTLY to the class usage');
});

test('R3 — AC-3 "same weight as the class-level split": hs1-vs-rail station totals hold the 3·nAshford : nOther ratio', () => {
  // 2 Ashford (weight 3 -> hs1) + 3 ordinary (weight 1 -> rail): class weights 6 : 3.
  const buildings = [
    ...run('rail', 1, 0, 20, 4),
    ...run('hs1', 10, 0, 30, 4),
    roadNear(30, 0, 0), { id: 31, spec: 'station_ashford', x: 0, y: 0, builtTick: 0 },
    roadNear(32, 0, 2), { id: 33, spec: 'station_ashford', x: 0, y: 2, builtTick: 0 },
    roadNear(34, 0, 4), { id: 35, spec: 'station_sanderling', x: 0, y: 4, builtTick: 0 },
    roadNear(36, 0, 6), { id: 37, spec: 'station_sanderling', x: 0, y: 6, builtTick: 0 },
    roadNear(38, 0, 8), { id: 39, spec: 'station_sanderling', x: 0, y: 8, builtTick: 0 },
  ];
  const s = board(buildings, 999999);
  const stats = stationUtilisationOf(s);
  const hsTotal = stats.filter((x) => x.lineSpec === 'hs1').reduce((a, x) => a + (x.utilisation ?? 0), 0);
  const railTotal = stats.filter((x) => x.lineSpec === 'rail').reduce((a, x) => a + (x.utilisation ?? 0), 0);
  assert.ok(hsTotal > 0 && railTotal > 0);
  // hs1 weight 2×3 = 6, rail weight 3×1 = 3 → hs1 total is exactly double.
  // MUTANT: changing the SSOT ×3 in lineUsageOf's rail path (hsWeight += 3)
  // to any other multiplier reds here — this is the ONLY place inc2's own
  // suite can observe the weight, since w/Σw is 1/n inside every class.
  assert.equal(hsTotal, 2 * railTotal, 'hs1 station total : rail station total === 6 : 3');
  // And both halves still tie back to lineUsageOf, not to a second model (GR#3).
  const lu = lineUsageOf(s);
  assert.equal(hsTotal, lu.find((u) => u.spec === 'hs1').usage);
  assert.equal(railTotal, lu.find((u) => u.spec === 'rail').usage);
});

test('R4 — determinism: 20 shuffled building orders give byte-identical lineSegmentsOf and stationUtilisationOf', () => {
  const buildings = [
    ...run('rail', 1, 0, 0, 5), ...run('rail', 20, 0, 3, 4), ...run('hs1', 40, 5, 7, 6),
    ...run('m20', 60, 0, 9, 5), ...run('rd_dual', 80, 0, 11, 3), ...run('rd_aroad', 90, 0, 13, 7),
    roadNear(200, 0, 0), { id: 201, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 },
    roadNear(202, 5, 7), { id: 203, spec: 'station_ashford', x: 5, y: 7, builtTick: 0 },
    roadNear(204, 0, 3), { id: 205, spec: 'station_sanderling', x: 0, y: 3, builtTick: 0 },
    roadNear(206, 9, 9), { id: 207, spec: 'station_ashford', x: 9, y: 9, builtTick: 0 },
  ];
  let seed = 12345;
  const rnd = () => { seed = (seed * 1103515245 + 12345) >>> 0; return seed / 4294967296; };
  let segRef = null;
  let statRef = null;
  for (let k = 0; k < 20; k++) {
    const shuffled = buildings.slice();
    for (let i = shuffled.length - 1; i > 0; i--) {
      const j = Math.floor(rnd() * (i + 1));
      [shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]];
    }
    const s = board(JSON.parse(JSON.stringify(shuffled)), 850000);
    const segJ = JSON.stringify(lineSegmentsOf(s));
    const statJ = JSON.stringify(stationUtilisationOf(s));
    if (segRef === null) { segRef = segJ; statRef = statJ; }
    // MUTANT (measured): determinism here is DOUBLY guarded — the flood-fill
    // start-key sort (`keysSorted`) and the per-run chain sort
    // (`runKeys.sort()`) each normalise segmentId on their own, so removing
    // either ALONE leaves this green (verified: both single mutants survive
    // 4/4). Removing BOTH reds this pin, and so would keying segmentId off an
    // array index (AC-8's explicit prohibition).
    assert.equal(segJ, segRef, `lineSegmentsOf diverged on shuffle ${k}`);
    assert.equal(statJ, statRef, `stationUtilisationOf diverged on shuffle ${k}`);
  }
  assert.ok(JSON.parse(segRef).length >= 5, 'fixture must actually produce multiple segments');
});
