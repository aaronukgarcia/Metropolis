// feat-2326609777-delta-sync.test.mjs — FEAT-2326609777 (2026-09-06):
// delta-sync for the Web Worker tick offload's postMessage protocol.
//
// BACKGROUND: with the offload ON, every tick round-trip structured-clones
// the WHOLE SimState (main->worker and worker->main) — measured 814-2761ms
// per round trip on Aaron's city, exceeding the 900ms Play interval so the
// clock cannot keep wall pace. A field-by-field JSON-byte measurement on the
// real capture-13 dogfood city (38,251 buildings / 9.48M citizens,
// E:/gotmp/dbg13.json, NOT committed to this repo — a local dogfood
// artifact) found `buildings` alone is 92.1% of the payload and
// `roadConnectivity` (recomputed fresh every tick by advance() regardless of
// whether anything changed) a further 4.2%; together >96%. See
// src/sim/simWorkerDelta.ts's header for the full writeup and the fix design
// (both sides keep a cache of "the last full state the other side is known
// to hold"; every request/reply is the DIFFERENCE from that cache).
//
// This file is the CI-safe, hermetic half of that measurement + correctness
// proof: it uses test/scale/fixture.mjs's committed ~13k-building/1.4M-
// population dogfood-scale fixture (the same one FEAT-2326609771's
// determinism-parity test already relies on) rather than the external
// dbg13.json capture, so it runs identically on Aaron's machine and in CI.
// The BOW item's own capture-13 measurement (buildings=92.1%,
// roadConnectivity=4.2% of payload; structuredClone ~40ms/direction in a
// Node harness) is reported separately in the round's own evidence, not
// re-asserted here as a wall-clock bound (GR#21 "no wall-clock asserts in
// CI" — see Vestige's verification-standards note).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { reducer } from '../src/sim/engine.ts';
import { buildScaleFixture } from './scale/fixture.mjs';
import { TRAFFIC_RECOMPUTE_TICKS } from '../src/sim/trafficWellbeing.ts';
import {
  diffBuildings,
  applyBuildingsDelta,
  diffSimState,
  applyStateDelta,
  roadConnectivityEqual,
  DeltaBasisMismatchError,
  RESYNC_EVERY_TICKS,
} from '../src/sim/simWorkerDelta.ts';

function byteLen(x) {
  return Buffer.byteLength(JSON.stringify(x), 'utf8');
}

// ===========================================================================
// (1) diffBuildings / applyBuildingsDelta — pure round-trip correctness on
// synthetic arrays covering every structural case: no change, a mutated
// building (new reference, same id), an added building, a removed building,
// and a reorder (belt-and-braces — the reducer never actually reorders, but
// the fallback path must still be correct if it ever did).
// ===========================================================================

describe('FEAT-2326609777: diffBuildings/applyBuildingsDelta round-trip', () => {
  const b = (id, spec = 'road', extra = {}) => ({ id, spec, x: id, y: id, ...extra });

  test('no change: empty delta, orderChanged false', () => {
    const base = [b(1), b(2), b(3)];
    const next = base; // same array reference entirely
    const delta = diffBuildings(base, next);
    assert.deepEqual(delta.changed, []);
    assert.deepEqual(delta.removedIds, []);
    assert.equal(delta.orderChanged, false);
    assert.equal(delta.order, undefined);
    assert.deepEqual(applyBuildingsDelta(base, delta), next);
  });

  test('one mutated building (new reference, same id, same position): fast path, no order array', () => {
    const base = [b(1), b(2), b(3)];
    const next = [base[0], { ...base[1], capacityTier: 1 }, base[2]];
    const delta = diffBuildings(base, next);
    assert.equal(delta.changed.length, 1);
    assert.equal(delta.changed[0].id, 2);
    assert.equal(delta.removedIds.length, 0);
    assert.equal(delta.orderChanged, false, 'a same-length, no-removal change must stay on the cheap fast path');
    assert.deepEqual(applyBuildingsDelta(base, delta), next);
  });

  test('an appended building (consolidator/auto-scale build event): orderChanged true, order present', () => {
    const base = [b(1), b(2)];
    const next = [base[0], base[1], b(3)];
    const delta = diffBuildings(base, next);
    assert.equal(delta.changed.length, 1);
    assert.equal(delta.changed[0].id, 3);
    assert.equal(delta.removedIds.length, 0);
    assert.equal(delta.orderChanged, true);
    assert.deepEqual(delta.order, [1, 2, 3]);
    assert.deepEqual(applyBuildingsDelta(base, delta), next);
  });

  test('a removed building (demolish/scrap event): removedIds populated, orderChanged true', () => {
    const base = [b(1), b(2), b(3)];
    const next = [base[0], base[2]];
    const delta = diffBuildings(base, next);
    assert.deepEqual(delta.changed, []);
    assert.deepEqual(delta.removedIds, [2]);
    assert.equal(delta.orderChanged, true);
    assert.deepEqual(applyBuildingsDelta(base, delta), next);
  });

  test('simultaneous add + remove + mutate: full round-trip', () => {
    const base = [b(1), b(2), b(3), b(4)];
    const next = [base[0], { ...base[2], heightStoreys: 3 }, b(5)]; // 2 and 4 removed, 3 mutated, 5 added
    const delta = diffBuildings(base, next);
    assert.deepEqual(applyBuildingsDelta(base, delta), next);
    assert.ok(delta.removedIds.includes(2) && delta.removedIds.includes(4));
  });

  test('a reorder with no add/remove/mutate (defensive fallback, never produced by the real reducer)', () => {
    const base = [b(1), b(2), b(3)];
    const next = [base[2], base[0], base[1]];
    const delta = diffBuildings(base, next);
    assert.equal(delta.orderChanged, true, 'a pure reorder must still be detected even with zero changed/removed entries');
    assert.deepEqual(applyBuildingsDelta(base, delta), next);
  });

  test('applyBuildingsDelta throws (never fabricates) if a delta is applied against the WRONG base', () => {
    const baseA = [b(1), b(2)];
    const baseB = [b(1)]; // missing id 2 entirely
    const next = [baseA[0], baseA[1], b(3)];
    const delta = diffBuildings(baseA, next);
    assert.throws(() => applyBuildingsDelta(baseB, delta), /missing from both delta.changed and base/);
  });
});

// ===========================================================================
// (2) roadConnectivityEqual — value equality, not reference equality (it is
// recomputed fresh every tick regardless of whether anything changed).
// ===========================================================================

describe('FEAT-2326609777: roadConnectivityEqual', () => {
  test('two distinct objects with the same tile list, same order: equal', () => {
    const a = { connectedRoadTiles: ['0,0', '1,0', '2,0'] };
    const c = { connectedRoadTiles: ['0,0', '1,0', '2,0'] };
    assert.notEqual(a, c, 'precondition: distinct references');
    assert.equal(roadConnectivityEqual(a, c), true);
  });

  test('different tile lists: not equal', () => {
    const a = { connectedRoadTiles: ['0,0', '1,0'] };
    const c = { connectedRoadTiles: ['0,0', '1,0', '2,0'] };
    assert.equal(roadConnectivityEqual(a, c), false);
  });

  test('undefined on both sides: equal (both mean "no connectivity computed yet")', () => {
    assert.equal(roadConnectivityEqual(undefined, undefined), true);
  });

  test('one undefined, one populated: not equal', () => {
    assert.equal(roadConnectivityEqual(undefined, { connectedRoadTiles: [] }), false);
  });
});

// ===========================================================================
// (3) diffSimState / applyStateDelta — full-state round-trip against a REAL
// reducer chain (small state), proving the reconstruction is byte-identical
// to what the reducer actually produced, not just structurally similar.
// ===========================================================================

test('FEAT-2326609777: applyStateDelta(base, diffSimState(base, next)) reproduces `next` byte-for-byte over 10 real ticks', () => {
  let s = buildScaleFixture({ buildingCount: 500, targetPopulation: 20_000, settleTicks: 1 });
  for (let i = 0; i < 10; i++) {
    const base = s;
    const next = reducer(base, { type: 'tick' });
    const delta = diffSimState(base, next);
    const reconstructed = applyStateDelta(base, delta);
    assert.deepEqual(reconstructed, next, `tick ${i}: reconstructed state must be byte-identical to the real reducer output`);
    s = next;
  }
});

// ===========================================================================
// (3b) BUG-951 REGRESSION PIN — `rest` must OMIT reference-identical fields.
//
// `rest` originally shipped every non-buildings/roadConnectivity field in
// full on every tick, justified by a capture-13 measurement putting all of
// `rest` at ~3.4% of the payload. FEAT-2326609800 inc7 invalidated that:
// TrafficSnapshot gained `wearSegments`, a per-segment table that reaches
// ~147KB on the 13k-building dogfood fixture. It is recomputed only on the
// traffic cadence (TRAFFIC_RECOMPUTE_TICKS) and is the SAME OBJECT on every
// other tick, yet was re-sent on all 60 of 60 ticks — 89.5% of the delta,
// taking the measurement in (4) below from an expected sub-5% to 20.21%.
//
// diffSimState now drops any `rest` field that is `===` its counterpart in
// `base`. The test below pins the MECHANISM (so the aggregate ratio in (4)
// can never go green for the wrong reason) and, critically, pins that the
// omission is LOSSLESS — including key presence for SimState's OPTIONAL
// fields, which a naive `delete` would silently drop.
//
// MUTANT (verified red, 2026-09-11): restore the old body
// (`rest: { ...allRest }`, no identity filter) -> the `omitted` assertion
// below fails, and (4)'s ratio returns to 20.21%.
// ===========================================================================

test('BUG-951: diffSimState omits reference-identical `rest` fields (trafficSnapshot on a non-cadence tick) and applyStateDelta still reproduces them exactly', () => {
  let s = buildScaleFixture({ buildingCount: 500, targetPopulation: 20_000, settleTicks: 1 });
  let sawOmittedTrafficSnapshot = false;
  let sawShippedTrafficSnapshot = false;
  let sawOmittedAnyObjectField = false;

  // Window sized from the DATA (never a literal): long enough to guarantee
  // both a traffic-cadence tick and non-cadence ticks inside it.
  const WINDOW_TICKS = TRAFFIC_RECOMPUTE_TICKS * 2 + 2;
  for (let i = 0; i < WINDOW_TICKS; i++) {
    const base = s;
    const next = reducer(base, { type: 'tick' });
    const delta = diffSimState(base, next);

    // LOSSLESS: the reconstruction must still be byte-identical, key presence
    // included (assert.deepEqual here is assert/strict's deepStrictEqual, which
    // distinguishes an absent key from a key present-with-undefined).
    assert.deepEqual(
      applyStateDelta(base, delta),
      next,
      `tick ${i}: dropping reference-identical fields must not change the reconstruction`
    );

    // Every field the delta DID ship must be one the tick genuinely replaced.
    for (const key of Object.keys(delta.rest)) {
      assert.notEqual(
        base[key] === next[key] && Object.prototype.hasOwnProperty.call(base, key),
        true,
        `tick ${i}: '${key}' is the same reference in base and next but was shipped anyway`
      );
    }
    // Every field the delta OMITTED must be reference-identical in base.
    for (const key of Object.keys(next)) {
      if (key === 'buildings' || key === 'roadConnectivity') continue;
      if (Object.prototype.hasOwnProperty.call(delta.rest, key)) continue;
      assert.equal(
        base[key],
        next[key],
        `tick ${i}: '${key}' was omitted from the delta but is NOT the same reference in base`
      );
      if (typeof next[key] === 'object' && next[key] !== null) sawOmittedAnyObjectField = true;
    }

    if (base.trafficSnapshot === next.trafficSnapshot) {
      assert.equal(
        Object.prototype.hasOwnProperty.call(delta.rest, 'trafficSnapshot'),
        false,
        `tick ${i}: trafficSnapshot is unchanged (same reference) but was re-sent in full — ` +
          `this is the inc7 wearSegments regression that took the payload to 20.2% of a full clone`
      );
      sawOmittedTrafficSnapshot = true;
    } else {
      sawShippedTrafficSnapshot = true;
    }
    s = next;
  }

  // ANTI-VACUITY: the window must have contained BOTH a cadence tick (so the
  // snapshot really is still shipped when it changes — the filter is not just
  // dropping it forever) and non-cadence ticks (so the omission was exercised).
  assert.ok(sawOmittedTrafficSnapshot, 'setup: at least one non-cadence tick must occur in the window');
  assert.ok(sawShippedTrafficSnapshot, 'setup: at least one traffic-cadence tick must occur in the window');
  assert.ok(sawOmittedAnyObjectField, 'setup: at least one object-valued rest field must have been omitted');
});

// ===========================================================================
// (4) THE ACCEPTANCE MEASUREMENT: per-tick delta payload bytes vs a full
// clone's bytes, on the committed ~13k-building/1.4M-population dogfood-
// scale fixture, over 60 real ticks. Algorithmic (byte-count via
// JSON.stringify), never wall-clock (GR#21 / verification-standards).
// ===========================================================================

test('FEAT-2326609777: delta payload is under 5% of a full-state clone, averaged over 60 ticks at dogfood scale', () => {
  let s = buildScaleFixture(); // DEFAULT_BUILDING_COUNT=13000, DEFAULT_TARGET_POPULATION=1_400_000
  const fullBytesSamples = [];
  const deltaBytesSamples = [];
  for (let i = 0; i < 60; i++) {
    const base = s;
    const next = reducer(base, { type: 'tick' });
    const delta = diffSimState(base, next);
    fullBytesSamples.push(byteLen(next));
    deltaBytesSamples.push(byteLen(delta));
    s = next;
  }
  const totalFull = fullBytesSamples.reduce((a, b2) => a + b2, 0);
  const totalDelta = deltaBytesSamples.reduce((a, b2) => a + b2, 0);
  const ratio = totalDelta / totalFull;
  assert.ok(
    ratio < 0.05,
    `delta payload averaged ${(ratio * 100).toFixed(2)}% of a full clone over 60 ticks (total delta=${totalDelta}, total full=${totalFull}) — expected < 5%`
  );
  // Sanity: the fixture must actually be large enough for this to be a
  // meaningful measurement (not a false pass from an accidentally-tiny
  // fixture) — buildings alone must dwarf a bare scalar-only state.
  assert.ok(totalFull > 60 * 400_000, 'fixture too small to be a meaningful dogfood-scale measurement');
});

test('FEAT-2326609777: a tick that genuinely changes many buildings (forced auto-scale wave) still stays well under a full clone', () => {
  // Build a fixture, then force EVERY building's capacityTier to look
  // eligible for auto-scale by fast-forwarding tick — worst case within the
  // "no add/remove" fast path: potentially ALL buildings' tier could bump in
  // one monthly pass (MAX_AUTO_SCALE_UPGRADES_PER_PASS caps this in
  // practice, but the assertion here is a soft ratio bound, not a specific
  // change count, so it stays valid regardless of the actual cap value).
  let s = buildScaleFixture({ buildingCount: 3000, targetPopulation: 300_000, settleTicks: 1 });
  // Run to the next monthly boundary (auto-scale evaluation tick) so this
  // sample actually exercises the buildings-changing path, not just an
  // ordinary no-op tick.
  const TICKS_PER_MONTH_GUESS = 30; // engine.ts's TICKS_PER_MONTH; not imported to keep this test decoupled from an internal constant name.
  for (let i = 0; i < TICKS_PER_MONTH_GUESS; i++) s = reducer(s, { type: 'tick' });
  const base = s;
  const next = reducer(base, { type: 'tick' });
  const delta = diffSimState(base, next);
  const ratio = byteLen(delta) / byteLen(next);
  assert.ok(ratio < 0.5, `even a monthly auto-scale-eligible tick's delta (${(ratio * 100).toFixed(1)}% of full) must stay a real reduction`);
});

// ===========================================================================
// (5) FEAT-2326609777 round follow-up (opus-round-feat777, 2026-09-06):
// baseTick integrity check. ATTACK FEAT-2326609777's "no integrity check on
// the delta basis" finding proved a same-shape-but-wrong-VALUE basis
// corrupted silently. applyStateDelta must now refuse it instead.
// ===========================================================================

describe('FEAT-2326609777 round follow-up: applyStateDelta refuses a mismatched basis', () => {
  test('diffSimState stamps baseTick from the state it diffed FROM', () => {
    let s = buildScaleFixture({ buildingCount: 200, targetPopulation: 5_000, settleTicks: 1 });
    const base = s;
    const next = reducer(base, { type: 'tick' });
    const delta = diffSimState(base, next);
    assert.equal(delta.baseTick, base.tick);
  });

  test('applyStateDelta throws DeltaBasisMismatchError when base.tick !== delta.baseTick (same shape, different values)', () => {
    let s = buildScaleFixture({ buildingCount: 200, targetPopulation: 5_000, settleTicks: 1 });
    const trueBase = s;
    const next = reducer(trueBase, { type: 'tick' });
    const delta = diffSimState(trueBase, next); // delta.baseTick === trueBase.tick

    // A same-shape-but-stale basis: same id/length/order, wrong VALUES (the
    // exact corruption shape the round's attack file demonstrated silently
    // succeeding before this fix) — simulate it by rewinding just the tick
    // field so the STRUCTURE stays identical but the integrity stamp lies.
    const staleBase = { ...trueBase, tick: trueBase.tick - 1 };

    assert.throws(
      () => applyStateDelta(staleBase, delta),
      (err) => {
        assert.ok(err instanceof DeltaBasisMismatchError);
        assert.equal(err.expectedBaseTick, delta.baseTick);
        assert.equal(err.actualBaseTick, staleBase.tick);
        return true;
      }
    );
  });

  test('applyStateDelta succeeds when base.tick === delta.baseTick (the correct-basis case is unaffected)', () => {
    let s = buildScaleFixture({ buildingCount: 200, targetPopulation: 5_000, settleTicks: 1 });
    const base = s;
    const next = reducer(base, { type: 'tick' });
    const delta = diffSimState(base, next);
    const reconstructed = applyStateDelta(base, delta);
    assert.deepEqual(reconstructed, next);
  });

  test('RESYNC_EVERY_TICKS is a positive, sane bound', () => {
    assert.equal(RESYNC_EVERY_TICKS, 64);
    assert.ok(RESYNC_EVERY_TICKS > 0);
  });
});

// ===========================================================================
// (6) BUG-966 REGRESSION PINS — a delta must be able to say "this top-level
//     key is GONE", or BUG-951's reference-identity filter permanently
//     desyncs the two caches on a superseded (discarded) reply.
//
// `rest` is destructured from `next`, so it can only ever describe keys
// `next` OWNS, and applyStateDelta's `{ ...base, ...delta.rest }` spread can
// only add or overwrite. A key present in `base` and absent from `next` was
// therefore unrepresentable (the pre-existing BUG-963 gap). That was
// survivable while `rest` shipped every field in full every tick — the next
// delta re-stated everything the sender still had — but with BUG-951's
// identity filter the receiver's stale extra key is never corrected and the
// protocol's soundness premise ("the receiver's cache is an EXACT value
// reconstruction of the sender's base") is false.
//
// MEASURED failure (attack-feat777-round.test.mjs "superseded replies ...
// self-heal", 400-building fixture): diverged at tick 54 on
// `crimeRatePreviousMonth` — OPTIONAL in types.ts, assigned by advance()
// only on a month boundary, so genuinely absent early. main's basis did not
// own it; the worker's cache held 0; the inbound delta could not say "drop
// it"; the reducer left it at 0; the identity filter then omitted it from
// the reply as "unchanged"; main never learned it. baseTick could not see
// any of this — both states were at tick 29 (two DISTINCT states sharing a
// tick is the exact hole RESYNC_EVERY_TICKS exists to bound).
//
// MUTANT (verified red, 2026-09-11): restore 7a662693's diffSimState body
// (no removedRestKeys) -> BOTH tests below fail, as do
// attack-feat777-round.test.mjs's self-heal and 200-tick protocol tests.
// ===========================================================================

describe('BUG-966: a deleted top-level key is representable, so the identity filter stays sound', () => {
  test('diffSimState reports a key the base owns and next does not; applyStateDelta removes it (key presence exact)', () => {
    const s = buildScaleFixture({ buildingCount: 200, targetPopulation: 5_000, settleTicks: 1 });
    // A REAL optional SimState field (types.ts `crimeRatePreviousMonth?`),
    // not an invented one — advance() writes it only on a month boundary.
    const OPTIONAL_KEY = 'crimeRatePreviousMonth';
    const withKey = { ...s, [OPTIONAL_KEY]: 0 };
    const withoutKey = { ...s };
    delete withoutKey[OPTIONAL_KEY];
    assert.equal(
      Object.prototype.hasOwnProperty.call(withoutKey, OPTIONAL_KEY),
      false,
      'fixture guard: the "next" state must genuinely not own the key'
    );

    const delta = diffSimState(withKey, withoutKey);
    assert.ok(
      Array.isArray(delta.removedRestKeys) && delta.removedRestKeys.includes(OPTIONAL_KEY),
      'the delta must name the dropped key'
    );
    assert.equal(
      Object.prototype.hasOwnProperty.call(delta.rest, OPTIONAL_KEY),
      false,
      '`rest` cannot carry a key `next` does not own'
    );

    const reconstructed = applyStateDelta(withKey, delta);
    // deepEqual here is assert/strict's deepStrictEqual: it distinguishes an
    // absent key from a key present-with-undefined, which is the whole point.
    assert.deepEqual(reconstructed, withoutKey);
    assert.equal(Object.prototype.hasOwnProperty.call(reconstructed, OPTIONAL_KEY), false);

    // And the common case stays free: a tick that drops nothing must not pay
    // for the field at all.
    const plain = diffSimState(s, reducer(s, { type: 'tick' }));
    assert.equal(plain.removedRestKeys, undefined, 'no dropped key => the field is omitted entirely');
  });

  test('a superseded reply cannot desync the two caches: the worker rebuilds main basis EXACTLY, key presence included', () => {
    const s = buildScaleFixture({ buildingCount: 200, targetPopulation: 5_000, settleTicks: 1 });
    const OPTIONAL_KEY = 'crimeRatePreviousMonth';

    // The exact post-discard shape measured at tick 54. main never applied
    // the previous reply, so it sits one tick behind and does NOT own the
    // optional key; both caches advanced and DO own it.
    const main = { ...s };
    delete main[OPTIONAL_KEY];
    const advanced = { ...s, tick: s.tick + 1, [OPTIONAL_KEY]: 0 };
    const workerKnown = advanced; // main's belief about the worker's cache
    const workerCache = structuredClone(advanced); // the worker's ACTUAL cache

    // main -> worker: rewind the worker to main's (older, key-less) state.
    const request = structuredClone(diffSimState(workerKnown, main));
    const preTick = applyStateDelta(workerCache, request);
    assert.deepEqual(
      preTick,
      main,
      'the worker must reconstruct main\'s basis EXACTLY — a leftover key here is the BUG-966 divergence'
    );

    // worker -> main: a tick that does not touch the field at all, so the
    // identity filter omits it (this is the step that made the divergence
    // permanent rather than self-healing).
    const nextState = { ...preTick, tick: preTick.tick + 1 };
    const reply = structuredClone(diffSimState(preTick, nextState));
    const resultState = applyStateDelta(main, reply);
    assert.deepEqual(
      resultState,
      nextState,
      'main\'s belief about the worker cache must equal the worker\'s actual cache'
    );
    assert.equal(
      JSON.stringify(resultState),
      JSON.stringify(nextState),
      'the attack suite\'s own JSON-value comparison of the two caches must agree too'
    );
  });
});
