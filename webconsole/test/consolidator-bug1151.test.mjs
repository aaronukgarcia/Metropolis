// consolidator-bug1151.test.mjs — BUG-1151: per-section groups must prefer
// un-scaled members (smallest real capacity first, id tiebreak), mirroring
// the city-wide round-F1 rule. A lowest-id slice let one auto-scaled member
// wedge the group at negative capacityGain forever.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState } from '../src/sim/engine.ts';
import { SPECS } from '../src/sim/data.ts';
import {
  findOpportunities,
  consolidationLadder,
  buildingCapacityOf,
} from '../src/sim/consolidator.ts';

function baseState(buildings) {
  return { ...initialState(), buildings, nextId: 1000, tick: 0 };
}

const FROM = 'res_block';
const rungs = consolidationLadder().filter((r) => r.from === FROM);
// res_estate (groupSize 25) is the largest rung a modest fixture can fill;
// the smaller rungs (midrise 4, penthouse 5, ...) fire in the same section too.
const GROUP = rungs.find((r) => r.to === 'res_estate').groupSize;
const range = (a, n) => Array.from({ length: n }, (_, i) => a + i);

// n res_block in section 0, ids 1..n; ids in `scaled` get capacityTier 9.
function fixture(n, scaled) {
  const out = [];
  for (let i = 1; i <= n; i++) {
    out.push({ id: i, spec: FROM, x: (i - 1) % 16, y: Math.floor((i - 1) / 16), ...(scaled.includes(i) ? { capacityTier: 9 } : {}) });
  }
  return out;
}
const oppsOf = (s) => findOpportunities(s, [0]).filter((o) => o.fromSpec === FROM);

test('sanity: ladder has res_block rungs and tier 9 really is bigger', () => {
  assert.ok(rungs.length >= 1);
  assert.ok(buildingCapacityOf(SPECS[FROM], 9) > buildingCapacityOf(SPECS[FROM], 0));
});

test('RED/GREEN wedge: scaled lowest-id member is excluded when enough tier-0 members exist', () => {
  const s = baseState(fixture(GROUP + 1, [1]));
  const opps = oppsOf(s);
  assert.ok(opps.length > 0);
  for (const o of opps) {
    assert.ok(!o.buildingIds.includes(1), `scaled id 1 must not be in group for ${o.toSpec}`);
    assert.deepEqual(o.buildingIds, range(2, o.groupCount));
  }
  assert.ok(opps.some((o) => o.capacityGain >= 0), 'at least one non-negative-gain opportunity');
});

test('control: no scaled member consolidates the lowest ids as before', () => {
  const opps = oppsOf(baseState(fixture(GROUP + 1, [])));
  assert.ok(opps.length > 0);
  for (const o of opps) {
    assert.deepEqual(o.buildingIds, range(1, o.groupCount));
    assert.ok(o.capacityGain >= 0);
  }
});

test('determinism: same state twice, and shuffled building order, give identical lists', () => {
  const b = fixture(GROUP + 3, [1, 4]);
  const a1 = JSON.stringify(oppsOf(baseState(b)));
  const a2 = JSON.stringify(oppsOf(baseState(b)));
  const shuffled = b.slice().reverse();
  const a3 = JSON.stringify(oppsOf(baseState(shuffled)));
  assert.equal(a1, a2);
  assert.equal(a1, a3);
});

test('equal-capacity ties break by id (stable, never flap)', () => {
  // GROUP+2 tier-0 members: first GROUP ids win, ids ascending.
  const opps = oppsOf(baseState(fixture(GROUP + 2, [])));
  for (const o of opps) assert.deepEqual(o.buildingIds, range(1, o.groupCount));
});

test('honest skip: when too many members are scaled the negative gain is still reported, never clamped', () => {
  // Exactly GROUP members, one scaled: no alternative exists.
  const opps = oppsOf(baseState(fixture(GROUP, [1]))).filter((o) => o.groupCount === GROUP);
  assert.ok(opps.length > 0);
  for (const o of opps) {
    assert.ok(o.buildingIds.includes(1));
    assert.ok(o.capacityGain < 0, `expected honest negative gain, got ${o.capacityGain}`);
  }
});

test('apply-gate agreement: opportunity ids are ascending real buildings of fromSpec, matching what the apply step re-reads', () => {
  const s = baseState(fixture(GROUP + 2, [1, 2]));
  const byId = new Map(s.buildings.map((b) => [b.id, b]));
  for (const o of oppsOf(s)) {
    assert.equal(o.buildingIds.length, o.groupCount);
    assert.deepEqual(o.buildingIds, o.buildingIds.slice().sort((a, b) => a - b));
    const real = o.buildingIds.reduce((sum, id) => sum + buildingCapacityOf(SPECS[FROM], byId.get(id).capacityTier ?? 0), 0);
    // apply-time recompute equals the finder's reported gain
    assert.equal(o.capacityGain, buildingCapacityOf(SPECS[o.toSpec], 0) - real);
  }
});
