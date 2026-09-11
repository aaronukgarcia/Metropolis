// attack-bug950-951-round.test.mjs — INDEPENDENT DESTRUCTIVE round
// (GR#23, attacker opus-round-bug950-951, 2026-09-11) over the two fixes the
// lead's bounded --webconsole-ci gate turned up on the unpushed inc7/inc9
// tree (63b8174):
//
//   BUG-950 — computeFlows() folds the road-resurfacing charge into the
//     EXISTING 'Roads' upkeep bucket (inc7 AC-5), but consistency.ts's
//     flows.upkeep-total-matches rebuilds those buckets from SPECS on the
//     POST-tick state, where the wear the charge was levied against has
//     already been reset. The fix records the figure on
//     lastFlows.roadRepairGbp (the BUG-419 lastFlows.population precedent).
//   BUG-951 — diffSimState now OMITS any `rest` field that is `===` its
//     base counterpart, which changes the delta protocol's payload shape
//     for every rest-level field at the worker/UI boundary.
//
// These tests pin what the AUTHOR suites do not. They are pure/algorithmic —
// no wall-clock or timing assertion anywhere (GR#21, verification-standards).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { reducer, computeFlows, initialState } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { lineSegmentIndexOf, SPECS, isOnline, upkeepChargeableOf } from '../src/sim/data.ts';
import { diffSimState, applyStateDelta } from '../src/sim/simWorkerDelta.ts';
import { TRAFFIC_RECOMPUTE_TICKS } from '../src/sim/trafficWellbeing.ts';
import { buildScaleFixture } from './scale/fixture.mjs';

// --- fixture (same shape as trafficWear.test.mjs's mixedFixture) ----------
const OFFSET = 200;
const rd = (id, spec, x, y) => ({ id, spec, x: x + OFFSET, y: y + OFFSET, builtTick: 0 });
const bldg = (id, spec, x, y) => ({ id, spec, x: x + OFFSET, y: y + OFFSET });
function board(buildings, population) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population, trafficSnapshot: undefined };
}
function wornCity(funds) {
  let s = board(
    [bldg(1, 'res_block', -3, 0), rd(2, 'm20', 0, 0), rd(3, 'rd_dual', 1, 0), bldg(4, 'ind_estate', 1, 1)],
    200_000,
  );
  s = { ...s, funds, roadWearBySegment: {} };
  const segId = lineSegmentIndexOf(s).segments.find((x) => x.kind === 'road')?.segmentId;
  assert.ok(segId, 'setup: the fixture must contain a segmentable road');
  return { ...s, roadWearBySegment: { [segId]: 3_000_000 } };
}

// =========================================================================
// BUG-950 (1): the figure recorded on lastFlows must be the one the tick's
// OWN single computeFlows(s) call folded into the 'Roads' outflow — not a
// re-derivation, and not a different call's answer. advance() destructures
// it from the one call it already makes (engine.ts is the ONLY non-replay
// computeFlows call site); this asserts the value AND the outflow list it
// came with agree with computeFlows() run on the same pre-tick state.
// =========================================================================
test('BUG-950: lastFlows.roadRepairGbp is the charge that tick own computeFlows() folded into Roads (value and outflow list both agree)', () => {
  let s = wornCity(500_000_000);
  let repairTicks = 0;
  for (let i = 0; i < 40; i++) {
    const pre = s;
    // computeFlows is pure and roadRepairPaymentOf is memoised over `s`, so
    // this re-run on the SAME pre-tick state must reproduce the tick exactly.
    const cf = computeFlows(pre);
    s = reducer(pre, { type: 'tick' });
    assert.equal(
      s.lastFlows.roadRepairGbp ?? 0,
      cf.roadRepairGbp,
      `tick ${i}: recorded roadRepairGbp must equal computeFlows(pre).roadRepairGbp`,
    );
    assert.deepEqual(
      s.lastFlows.outflows,
      cf.outflows,
      `tick ${i}: the recorded outflows must be the ones that charge was folded into`,
    );
    if ((s.lastFlows.roadRepairGbp ?? 0) > 0) repairTicks++;
  }
  // ANTI-VACUITY: a run that never charges a repair proves nothing.
  assert.ok(repairTicks > 0, 'setup: at least one tick must actually charge a road repair');
});

// =========================================================================
// BUG-950 (5): a DEFERRED repair (funds short) must book nothing — the
// recorded figure has to be 0 on exactly the ticks the payment gate refused,
// or consistency.ts would fold in a charge the outflows never carried.
// =========================================================================
test('BUG-950: a repair deferred for lack of funds records roadRepairGbp = 0, and no churn-free tick fails flows.upkeep-total-matches', () => {
  let s = wornCity(0);
  const basisOf = (st) =>
    st.buildings
      .filter((b) => isOnline(st, b) && SPECS[b.spec]?.upkeep)
      .map((b) => `${b.spec}:${upkeepChargeableOf(b, SPECS[b.spec])}`)
      .sort()
      .join('|');
  let deferredTicks = 0;
  let churnFreeTicks = 0;
  for (let i = 0; i < 40; i++) {
    const before = basisOf(s);
    s = reducer(s, { type: 'tick' });
    const charged = s.lastFlows.roadRepairGbp ?? 0;
    if ((s.roadRepairDeferredSegmentIds ?? []).length > 0) {
      deferredTicks++;
      assert.equal(charged, 0, `tick ${i}: a deferred repair must book nothing`);
    }
    if (before !== basisOf(s)) continue; // documented online/removal lag, pre-existing
    churnFreeTicks++;
    const upkeep = runConsistencyChecks(s).checks.find((c) => c.id === 'flows.upkeep-total-matches');
    assert.ok(upkeep, `tick ${i}: flows.upkeep-total-matches must be reported`);
    assert.ok(upkeep.ok, `tick ${i} (roadRepairGbp=${charged}): ${upkeep.detail}`);
  }
  assert.ok(deferredTicks > 0, 'setup: at least one repair must actually have been deferred');
  assert.ok(churnFreeTicks > 0, 'setup: at least one churn-free tick must have been asserted on');
});

// =========================================================================
// BUG-951 (THE round's highest-value attack): the author's round-trip pin
// compares against a base that SHARES object references with `next`. The
// REAL protocol's receiver holds a structuredClone of that base. If the
// reducer ever mutated a rest-level field IN PLACE, `base[k] === next[k]`
// would still hold on the SENDER (both names point at the mutated object)
// while the receiver's clone still held the OLD contents — a silently
// dropped change the same-process pin cannot see.
//
// This drives a real reducer chain and reconstructs on an INDEPENDENT CLONE,
// which is exactly the in-place-mutation detector. Verified non-vacuous
// during the round: injecting one `next.ledger.push(...)` in-place mutation
// makes this test fail at the first tick with 'ledger' divergent.
// =========================================================================
test('BUG-951: applying the delta to an INDEPENDENT CLONE of the base reproduces the state exactly (no rest-level field is mutated in place)', () => {
  let s = buildScaleFixture({ buildingCount: 200, targetPopulation: 20_000, settleTicks: 1 });
  let ui = structuredClone(s);
  assert.deepEqual(ui, s, 'setup: the clone must start equal to the sender state');
  const WINDOW_TICKS = TRAFFIC_RECOMPUTE_TICKS + 5;
  let omittedObjectFields = 0;
  for (let i = 0; i < WINDOW_TICKS; i++) {
    const base = s;
    const next = reducer(base, { type: 'tick' });
    const delta = diffSimState(base, next);
    for (const key of Object.keys(next)) {
      if (key === 'buildings' || key === 'roadConnectivity') continue;
      if (Object.prototype.hasOwnProperty.call(delta.rest, key)) continue;
      if (typeof next[key] === 'object' && next[key] !== null) omittedObjectFields++;
    }
    // structuredClone of the delta is what postMessage actually does.
    ui = applyStateDelta(ui, structuredClone(delta));
    assert.deepEqual(ui, next, `tick ${i}: clone-side reconstruction diverged from the sender state`);
    s = next;
  }
  assert.ok(omittedObjectFields > 0, 'setup: at least one object-valued rest field must have been omitted');
});

// =========================================================================
// BUG-951 (2): KEY PRESENCE. The omission rule must never confuse
// "unchanged" with "absent". These are the transitions SimState's optional
// fields can actually make through the reducer (which only ever rebuilds
// with `{ ...s, ... }` and therefore never DELETES a top-level key).
//
// KNOWN, PRE-EXISTING and deliberately not asserted here: a base key that is
// genuinely DELETED in `next` is served from the base by
// `{ ...base, ...delta.rest }` and so survives the round trip. Measured
// identical on the pre-fix baseline (gate-cfb244c) during this round — it is
// a property of applyStateDelta's spread, not of the identity filter, and is
// unreachable while the reducer never deletes a top-level key. See BUG-963.
// =========================================================================
test('BUG-951: key presence survives the identity filter for every transition the reducer can produce', () => {
  const mk = (extra) => ({
    tick: 7,
    buildings: [],
    roadConnectivity: { connectedRoadTiles: [], connectedBuildingIds: [] },
    funds: 10,
    ...extra,
  });
  const roundTrip = (base, next) => applyStateDelta(base, structuredClone(diffSimState(base, next)));
  const shared = { z: 1 };
  const cases = [
    ['absent in base -> present in next', mk({}), mk({ trafficSnapshot: shared })],
    ['present in base -> present-with-undefined in next', mk({ trafficSnapshot: shared }), mk({ trafficSnapshot: undefined })],
    ['present-with-undefined in base -> real value in next', mk({ trafficSnapshot: undefined }), mk({ trafficSnapshot: shared })],
    ['absent in base -> present-with-undefined in next', mk({}), mk({ trafficSnapshot: undefined })],
    ['undefined in both (identity, omitted)', mk({ trafficSnapshot: undefined }), mk({ trafficSnapshot: undefined })],
    ['null in base -> undefined in next', mk({ trafficSnapshot: null }), mk({ trafficSnapshot: undefined })],
    ['same reference carried through while another field changes', mk({ trafficSnapshot: shared }), mk({ trafficSnapshot: shared, funds: 99 })],
    ['value-equal but DISTINCT object must still be shipped', mk({ trafficSnapshot: { z: 1 } }), mk({ trafficSnapshot: { z: 1 } })],
  ];
  for (const [name, base, next] of cases) {
    assert.deepEqual(roundTrip(base, next), next, `key-presence transition failed: ${name}`);
  }
  // The filter must be REFERENCE identity, not value equality: a distinct but
  // value-equal object has to stay on the wire (conservative direction).
  const distinct = diffSimState(mk({ trafficSnapshot: { z: 1 } }), mk({ trafficSnapshot: { z: 1 } }));
  assert.ok(
    Object.prototype.hasOwnProperty.call(distinct.rest, 'trafficSnapshot'),
    'a value-equal but distinct object must still be shipped (reference identity, never deep equality)',
  );
  // ...and a genuinely identical reference must be dropped.
  const identical = diffSimState(mk({ trafficSnapshot: shared }), mk({ trafficSnapshot: shared, funds: 99 }));
  assert.equal(
    Object.prototype.hasOwnProperty.call(identical.rest, 'trafficSnapshot'),
    false,
    'a reference-identical field must be omitted from the delta',
  );
  assert.equal(identical.rest.funds, 99, 'a field the tick really replaced must still be shipped');
});
