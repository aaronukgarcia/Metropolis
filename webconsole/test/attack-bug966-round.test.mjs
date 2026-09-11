// attack-bug966-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23)
// opus-round-bug966, 2026-09-11. Attacker != author.
//
// BUG-966: after BUG-951's reference-identity filter, diffSimState/
// applyStateDelta could not represent "this top-level key is GONE" (a key
// present in `base`, absent from `next`): `rest` is destructured from `next`
// so it can only ever name keys `next` OWNS, and `{ ...base, ...rest }` can
// only add or overwrite. While `rest` shipped every field in full every
// tick the receiver's stale extra key was corrected by the very next delta;
// with the identity filter it never is, and a superseded (discarded) reply
// leaves the two caches permanently diverged until RESYNC_EVERY_TICKS.
// The fix adds SimStateDelta.removedRestKeys.
//
// These pins attack the FIX, not the bug: the reconstruction must be exact
// for arbitrary key-shape transitions, the new field must be honoured by the
// receiver, and it must not become a delete-anything primitive.
//
// MUTANTS KILLED BY THIS FILE (measured 2026-09-11, scratch copy of
// simWorkerDelta.ts, .bak outside the repo, md5 restored):
//   M1 diffSimState+applyStateDelta reverted to 7a662693 (no removedRestKeys)
//      -> RED (also reds attack-feat777-round's self-heal test)
//   M3 removedRestKeys emitted but never applied -> RED
//   M5 the buildings/roadConnectivity refusal dropped from applyStateDelta
//      -> RED on "a hostile delta cannot delete buildings".
// Equivalent mutant (GREEN, reported not pinned): M4 applying the deletions
// BEFORE the spread instead of after. The two orders differ only when a key
// appears in BOTH `rest` and `removedRestKeys`, which diffSimState never
// produces; no reachable behaviour distinguishes them.
import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { reducer } from '../src/sim/engine.ts';
import { buildScaleFixture } from './scale/fixture.mjs';
import {
  diffSimState,
  applyStateDelta,
  applyBuildingsDelta,
  roadConnectivityEqual,
  RESYNC_EVERY_TICKS,
} from '../src/sim/simWorkerDelta.ts';

/** cfb244c's (pre-BUG-951) receiver, for the backward-compatibility pin.
 *  diffBuildings/applyBuildingsDelta/roadConnectivityEqual are byte-identical
 *  between cfb244c and HEAD (verified by diff: every hunk is at/after the
 *  SimStateDelta interface), so reusing them here is faithful. */
function legacyApplyStateDelta(base, delta) {
  if (base.tick !== delta.baseTick) throw new Error('basis mismatch');
  const buildings = applyBuildingsDelta(base.buildings, delta.buildings);
  const roadConnectivity = delta.roadConnectivity ?? base.roadConnectivity;
  return { ...base, ...delta.rest, buildings, roadConnectivity };
}
/** cfb244c's sender: `rest` = every non-buildings/roadConnectivity field in
 *  full, and no removedRestKeys field at all. */
function legacyDiffSimState(base, next) {
  const { buildings: _b, roadConnectivity: nextRoad, ...allRest } = next;
  const real = diffSimState(base, next);
  return {
    baseTick: base.tick,
    rest: allRest,
    buildings: real.buildings,
    roadConnectivity: roadConnectivityEqual(base.roadConnectivity, nextRoad) ? undefined : nextRoad,
  };
}

const keySet = (o) => Object.keys(o).sort().join('|');
/** Stricter than the JSON.stringify compare the sibling attack file uses:
 *  JSON drops undefined-valued keys, so it cannot see a key-presence defect
 *  on a field whose value is undefined. */
function assertExact(actual, expected, msg) {
  assert.equal(keySet(actual), keySet(expected), `${msg} [key SET]`);
  assert.deepStrictEqual(actual, expected, `${msg} [value]`);
}
function mulberry(a) {
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// `crimeRatePreviousMonth` is the field the BUG-966 RCA actually caught the
// divergence on: OPTIONAL in types.ts, written by advance() only on a month
// boundary, so genuinely absent from a young city's state.
const OPTIONAL_KEY = 'crimeRatePreviousMonth';

describe('BUG-966 round: applyStateDelta is an EXACT inverse of diffSimState for arbitrary key shapes', () => {
  test('property-based: 300 (base, next) pairs with injected deletions / additions / undefined-assignments round-trip exactly', () => {
    const seed = buildScaleFixture({ buildingCount: 150, targetPopulation: 4_000, settleTicks: 1 });
    const chain = [seed];
    let s = seed;
    for (let i = 0; i < 20; i++) {
      s = reducer(s, { type: 'tick' });
      if (i === 5) s = reducer(s, { type: 'debugFunds', amount: 1_000_000_00 });
      if (i === 9) s = reducer(s, { type: 'toggleConsolidator' });
      if (i === 14) s = reducer(s, { type: 'place', spec: 'road', x: 380 + i, y: 380 });
      if (i === 18) s = reducer(s, { type: 'tax', which: 'residential', rate: 0.11 });
      chain.push(s);
    }
    // `tick` is excluded from the injection pool only because applyStateDelta's
    // basis check reads it; buildings/roadConnectivity have their own deltas.
    const pool = Object.keys(seed).filter((k) => k !== 'buildings' && k !== 'roadConnectivity' && k !== 'tick');
    const rnd = mulberry(20260911);
    let deletions = 0;
    let additions = 0;
    let undefs = 0;
    for (let iter = 0; iter < 300; iter++) {
      const base = chain[Math.floor(rnd() * chain.length)];
      const next = { ...structuredClone(chain[Math.floor(rnd() * chain.length)]), tick: base.tick };
      for (let d = Math.floor(rnd() * 4); d > 0; d--) {
        const k = pool[Math.floor(rnd() * pool.length)];
        if (Object.prototype.hasOwnProperty.call(next, k)) {
          delete next[k];
          deletions++;
        }
      }
      for (let a = Math.floor(rnd() * 3); a > 0; a--) {
        next['roundOnlyKey' + Math.floor(rnd() * 5)] = { v: Math.floor(rnd() * 1000) };
        additions++;
      }
      if (rnd() < 0.3) {
        next[pool[Math.floor(rnd() * pool.length)]] = undefined;
        undefs++;
      }
      // structuredClone on BOTH sides: the real protocol crosses postMessage.
      const out = applyStateDelta(structuredClone(base), structuredClone(diffSimState(base, next)));
      assertExact(out, next, `iter ${iter}`);
    }
    // Non-vacuous: the injector must really have injected all three shapes.
    assert.ok(deletions > 50, `deletions injected: ${deletions}`);
    assert.ok(additions > 50, `additions injected: ${additions}`);
    assert.ok(undefs > 20, `undefined-assignments injected: ${undefs}`);
  });

  test('all 8 absent / undefined / value transitions on a REAL optional field preserve key presence', () => {
    const s = buildScaleFixture({ buildingCount: 50, targetPopulation: 1_000, settleTicks: 1 });
    const absent = { ...s };
    delete absent[OPTIONAL_KEY];
    const undef = { ...s, [OPTIONAL_KEY]: undefined };
    const val = { ...s, [OPTIONAL_KEY]: 7 };
    const cases = [
      ['absent->undefined', absent, undef],
      ['undefined->absent', undef, absent],
      ['absent->value', absent, val],
      ['value->absent', val, absent],
      ['undefined->value', undef, val],
      ['value->undefined', val, undef],
      ['absent->absent', absent, absent],
      ['undefined->undefined', undef, undef],
    ];
    for (const [name, b, n] of cases) {
      const out = applyStateDelta(structuredClone(b), structuredClone(diffSimState(b, n)));
      assert.equal(
        Object.prototype.hasOwnProperty.call(out, OPTIONAL_KEY),
        Object.prototype.hasOwnProperty.call(n, OPTIONAL_KEY),
        `${name}: key PRESENCE not preserved`
      );
      assertExact(out, n, name);
    }
  });

  test('a tick that drops nothing does not pay for the field at all (the BUG-951 payload win is untouched)', () => {
    const s = buildScaleFixture({ buildingCount: 150, targetPopulation: 4_000, settleTicks: 1 });
    const delta = diffSimState(s, reducer(s, { type: 'tick' }));
    assert.equal(delta.removedRestKeys, undefined, 'removedRestKeys must be OMITTED, not an empty array');
    assert.equal(
      Object.prototype.hasOwnProperty.call(delta, 'removedRestKeys'),
      false,
      'an empty array would still cost wire bytes on every tick'
    );
  });
});

describe('BUG-966 round: removedRestKeys is not a delete-anything primitive', () => {
  test('a hostile delta cannot delete buildings or roadConnectivity', () => {
    const s = buildScaleFixture({ buildingCount: 60, targetPopulation: 1_500, settleTicks: 1 });
    const delta = diffSimState(s, reducer(s, { type: 'tick' }));
    delta.removedRestKeys = ['buildings', 'roadConnectivity'];
    const out = applyStateDelta(s, delta);
    assert.ok(Object.prototype.hasOwnProperty.call(out, 'buildings'), 'buildings must survive a hostile removal');
    assert.ok(Array.isArray(out.buildings) && out.buildings.length > 0);
    assert.ok(
      Object.prototype.hasOwnProperty.call(out, 'roadConnectivity'),
      'roadConnectivity must survive a hostile removal'
    );
    assert.notEqual(out.roadConnectivity, undefined);
    // And diffSimState never NAMES them, even when it legitimately could:
    // both are handled by their own delta fields.
    const stripped = { ...structuredClone(s) };
    delete stripped.roadConnectivity;
    assert.equal(
      (diffSimState(s, { ...stripped, roadConnectivity: s.roadConnectivity }).removedRestKeys ?? []).includes(
        'roadConnectivity'
      ),
      false
    );
  });

  test('removedRestKeys naming __proto__/constructor does not corrupt the reconstructed object or the global prototype', () => {
    const s = buildScaleFixture({ buildingCount: 60, targetPopulation: 1_500, settleTicks: 1 });
    const delta = diffSimState(s, reducer(s, { type: 'tick' }));
    delta.removedRestKeys = ['__proto__', 'constructor'];
    const out = applyStateDelta(s, delta);
    assert.equal(Object.getPrototypeOf(out), Object.prototype);
    assert.equal({}.constructor, Object);
    assert.equal(typeof out.tick, 'number');
  });
});

describe('BUG-966 round: protocol compatibility across the two shapes', () => {
  test('a NEW delta fed to the OLD (cfb244c) receiver does not throw; it merely misses the deletion', () => {
    const s = buildScaleFixture({ buildingCount: 60, targetPopulation: 1_500, settleTicks: 1 });
    const base = { ...s, [OPTIONAL_KEY]: 0 };
    const next = { ...s };
    delete next[OPTIONAL_KEY];
    const wire = structuredClone(diffSimState(base, next));
    assert.deepStrictEqual(wire.removedRestKeys, [OPTIONAL_KEY]);
    const legacy = legacyApplyStateDelta(structuredClone(base), wire);
    assert.equal(
      Object.prototype.hasOwnProperty.call(legacy, OPTIONAL_KEY),
      true,
      'documented: an old receiver keeps the stale key — bounded by RESYNC_EVERY_TICKS'
    );
    assert.ok(RESYNC_EVERY_TICKS > 0 && Number.isFinite(RESYNC_EVERY_TICKS));
    const current = applyStateDelta(structuredClone(base), wire);
    assert.equal(Object.prototype.hasOwnProperty.call(current, OPTIONAL_KEY), false);
  });

  test('an OLD delta (full rest, no removedRestKeys field) fed to the NEW receiver reconstructs exactly', () => {
    const s = buildScaleFixture({ buildingCount: 60, targetPopulation: 1_500, settleTicks: 1 });
    const n = reducer(s, { type: 'tick' });
    const wire = structuredClone(legacyDiffSimState(s, n));
    assert.equal(wire.removedRestKeys, undefined);
    assertExact(applyStateDelta(structuredClone(s), wire), n, 'old delta -> new receiver');
  });
});

describe('BUG-966 round: superseded replies, discard patterns beyond alternating', () => {
  // A faithful transcription of the production protocol (store.tsx's
  // issueTickRequest + worker.onmessage, simWorker.ts's onmessage), with a
  // real structuredClone at every boundary crossing and a STRICT exactness
  // comparison (key set + deepStrictEqual) of main's belief about the worker
  // cache against the worker's ACTUAL cache on every single tick.
  function makeProtocol(initialMain) {
    return {
      main: initialMain,
      workerKnown: null,
      basis: null,
      workerCache: null,
      ticksWithRemovedKeys: 0,
      removedKeyNames: new Set(),
      tick(apply = true) {
        const current = this.main;
        this.basis = current;
        let reply;
        if (this.workerKnown) {
          const delta = diffSimState(this.workerKnown, current);
          if (delta.removedRestKeys) {
            this.ticksWithRemovedKeys++;
            for (const k of delta.removedRestKeys) this.removedKeyNames.add(k);
          }
          const preTick = applyStateDelta(this.workerCache, structuredClone(delta));
          const nextState = reducer(preTick, { type: 'tick' });
          const outDelta = diffSimState(preTick, nextState);
          if (outDelta.removedRestKeys) {
            this.ticksWithRemovedKeys++;
            for (const k of outDelta.removedRestKeys) this.removedKeyNames.add(k);
          }
          this.workerCache = nextState;
          reply = { type: 'tickResultDelta', delta: structuredClone(outDelta) };
        } else {
          const wire = structuredClone(current);
          const nextState = reducer(wire, { type: 'tick' });
          this.workerCache = nextState;
          reply = { type: 'tickResult', state: structuredClone(nextState), deltaCapable: true };
        }
        const resultState =
          reply.type === 'tickResult' ? reply.state : applyStateDelta(this.basis, reply.delta);
        this.workerKnown = resultState;
        if (apply) this.main = reducer(this.main, { type: 'hydrate', state: resultState, source: 'tick' });
      },
      act(action) {
        this.main = reducer(this.main, action);
      },
    };
  }

  const patterns = [
    ['bursts of 5 discarded', (i) => Math.floor(i / 5) % 2 === 0],
    ['pseudo-random 30% discarded', null],
    [
      'discarded ON the RESYNC_EVERY_TICKS boundary tick',
      (i) => !(i % RESYNC_EVERY_TICKS === 0 || i % RESYNC_EVERY_TICKS === RESYNC_EVERY_TICKS - 1),
    ],
    ['every reply discarded but the first', (i) => i === 0],
  ];
  for (const [name, wantApply] of patterns) {
    test(`${name}: the two caches stay EXACT for 140 ticks`, () => {
      const p = makeProtocol(buildScaleFixture({ buildingCount: 400, targetPopulation: 20_000, settleTicks: 1 }));
      const rnd = mulberry(4242);
      let discards = 0;
      for (let i = 0; i < 140; i++) {
        const applyIt = wantApply ? wantApply(i) : rnd() >= 0.3;
        if (!applyIt) discards++;
        p.tick(applyIt);
        if (i % 7 === 3) p.act({ type: 'place', spec: 'road', x: 300 + i, y: 300 });
        if (i === 40) p.act({ type: 'debugFunds', amount: 5_000_000_00 });
        if (i === 70) p.act({ type: 'tax', which: 'residential', rate: 0.13 });
        assertExact(p.workerKnown, p.workerCache, `tick ${i} (${applyIt ? 'applied' : 'DISCARDED'})`);
      }
      assert.ok(discards > 0, 'the pattern must actually discard something');
    });
  }

  test('the pseudo-random discard pattern genuinely EXERCISES removedRestKeys at runtime (not a dead field)', () => {
    const p = makeProtocol(buildScaleFixture({ buildingCount: 400, targetPopulation: 20_000, settleTicks: 1 }));
    const rnd = mulberry(4242);
    for (let i = 0; i < 140; i++) {
      p.tick(rnd() >= 0.3);
      if (i % 7 === 3) p.act({ type: 'place', spec: 'road', x: 300 + i, y: 300 });
    }
    // Measured 2026-09-11: 1 tick, naming crimeRatePreviousMonth — the exact
    // field and mechanism the BUG-966 RCA identified. If this ever reaches 0
    // the discard pattern has stopped reproducing the rewind, and the pins
    // above would be testing a path the protocol no longer takes.
    assert.ok(p.ticksWithRemovedKeys > 0, 'no delta ever carried removedRestKeys — the harness has gone vacuous');
    assert.ok(p.removedKeyNames.has(OPTIONAL_KEY), `expected ${OPTIONAL_KEY}; saw ${[...p.removedKeyNames]}`);
  });
});
