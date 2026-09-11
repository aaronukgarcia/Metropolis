// attack-feat798-round3.test.mjs — independent Destructive round 3 pins for
// FEAT-2326609798 (realistic traffic inc5, unhappiness coupling), attacker
// opus-round3-feat798-inc5, 2026-09-10.
//
// These are the attack probes from that round that HELD and have lasting
// value: determinism across repeated runs, old-save (absent-field) tolerance,
// and the GR#16 sanitizer's coercion rules. Every assertion consumes an
// EXPORT — no formula is re-typed here (GR#3 / BUG-880).
//
// NOT included (deliberately): the round's two REJECT blockers. The
// bug-519-approval-services 55-baseline failure and the surviving
// engine.ts-call-site gridlock-history mutant are filed as BOW bugs and must
// be pinned by the rework itself, not papered over by an attacker's file.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeRoadConnectivity } from '../src/sim/data.ts';
import { initialState, wellbeingOf, reducer } from '../src/sim/engine.ts';
import { sanitizeTrafficSnapshot, trafficPenaltyOf } from '../src/sim/trafficWellbeing.ts';

let _id = 798300;
const B = (spec, x, y, extra = {}) => ({ id: _id++, spec, x, y, ...extra });

/** Fresh state with the given buildings, road connectivity computed exactly
 *  as advance() does (same harness shape as bug-519-approval-services.mjs). */
function city(buildings, tick, population) {
  const s = initialState();
  const st = { ...s, buildings: [...buildings], population, tick };
  st.roadConnectivity = computeRoadConnectivity(st);
  return st;
}

/** A genuinely ROUTED city: a connected road chain with a residence at one
 *  end and an office at the other, so the traffic assignment has real
 *  origin/destination demand to route (not a vacuous empty board). */
function routedCity() {
  const bs = [];
  for (let x = 0; x <= 8; x++) bs.push(B('road', x, 10, { builtTick: 0 }));
  bs.push(
    B('res_hut', 1, 9, { builtTick: 0 }),
    B('off_suite', 7, 9, { builtTick: 0 }),
    B('hea_hospital', 3, 9, { builtTick: 0 })
  );
  return city(bs, 200, 5000);
}

test('GR#21 determinism: 3 independent runs of 35 ticks on a routed city give byte-identical wellbeing, snapshot, penalty and gridlock history', () => {
  const results = [];
  for (let i = 0; i < 3; i++) {
    let t = routedCity();
    for (let k = 0; k < 35; k++) t = reducer(t, { type: 'tick' });
    results.push(
      JSON.stringify({
        wb: wellbeingOf(t),
        snap: t.trafficSnapshot,
        pen: trafficPenaltyOf(t),
        gl: t.gridlockTicksBySegment,
      })
    );
  }
  // 35 ticks crosses at least one cadence boundary, so this exercises the
  // recompute path, not only the carry-forward path.
  assert.ok(results[0].length > 0, 'precondition: the serialised result must be non-empty');
  assert.equal(results[0], results[1], 'run 1 and run 2 must be byte-identical');
  assert.equal(results[1], results[2], 'run 2 and run 3 must be byte-identical');
});

test('old-save safety: a state with trafficSnapshot AND gridlockTicksBySegment absent advances without throwing or producing NaN', () => {
  const legacy = { ...routedCity() };
  delete legacy.trafficSnapshot;
  delete legacy.gridlockTicksBySegment;
  assert.equal(legacy.trafficSnapshot, undefined, 'precondition: snapshot genuinely absent');
  assert.equal(legacy.gridlockTicksBySegment, undefined, 'precondition: gridlock history genuinely absent');

  let t = legacy;
  for (let k = 0; k < 3; k++) t = reducer(t, { type: 'tick' });

  assert.ok(t.trafficSnapshot, 'an absent snapshot must be computed on the first advance()');
  const wb = wellbeingOf(t);
  assert.ok(Number.isFinite(wb.overall), 'the composite must be finite after a legacy-save load');
  for (const p of wb.parts) {
    assert.ok(Number.isFinite(p.value), `wellbeing part "${p.label}" must be finite after a legacy-save load`);
  }
  assert.ok(Number.isFinite(trafficPenaltyOf(t)), 'the traffic penalty must be finite after a legacy-save load');
});

test('GR#16: sanitizeTrafficSnapshot rejects non-objects and non-finite fields, and clamps negative/oversized shares into the unit interval', () => {
  // Non-object / null -> absent.
  assert.equal(sanitizeTrafficSnapshot(null), undefined, 'null must coerce to absent');
  assert.equal(sanitizeTrafficSnapshot('not-a-snapshot'), undefined, 'a string must coerce to absent');
  assert.equal(sanitizeTrafficSnapshot(undefined), undefined, 'undefined must coerce to absent');

  // Any non-finite numeric field -> absent (never a partially-garbage snapshot).
  const good = { tick: 1, medianCommuteMinutes: 1, gridlockShare: 0, coverageShare: 1 };
  for (const field of ['tick', 'medianCommuteMinutes', 'gridlockShare', 'coverageShare']) {
    for (const bad of [NaN, Infinity, -Infinity]) {
      assert.equal(
        sanitizeTrafficSnapshot({ ...good, [field]: bad }),
        undefined,
        `${field} = ${String(bad)} must coerce the whole snapshot to absent`
      );
    }
  }

  // Negatives floor at 0; a null coverageShare is preserved as the honest
  // no-station absence (NOT coerced to a number).
  // BUG-924 (inc9 r1): the object below now ALSO carries safeRoadScore/
  // integratedTransportScore -- inc9 added these two fields to
  // TrafficSnapshot and this full-object deepEqual pin was not updated with
  // them, so the pin went red the instant inc9 landed (a real regression
  // this attacker pin is meant to catch, not loosen away). The fixture input
  // still omits both fields (mirrors a pre-inc9 save), so the EXPECTED
  // output takes their documented absent-field defaults -- safeRoadScore 1.0
  // (citySafeRoadScoreOf's "nothing routed yet" neutral), integratedTransportScore
  // 0 (integratedTransportScoreOf's "0 connected stations" neutral) -- the
  // SAME two defaults attack-feat802-round.test.mjs's own legacy-save pin
  // asserts directly against sanitizeTrafficSnapshot.
  // FEAT-2326609805 inc10 r2 (BUG-952 fix): the same regression class
  // repeats -- inc10 added THREE more fields (p90CommuteMinutes,
  // vOverCBySegment, coverageShareByService) and this pin must carry their
  // documented absent-field defaults too, or the very next increment's
  // deepEqual failure would (again) look like "the attacker's pin is
  // stale" instead of a real shape regression. Fixture still omits all
  // three (mirrors a pre-inc10 save): p90CommuteMinutes falls back to the
  // (already-floored-to-0) medianCommuteMinutes; vOverCBySegment defaults
  // to {} (no segment map at all -- honest absence, not a fabricated
  // entry); coverageShareByService seeds `ambulance` from the legacy
  // single-service `coverageShare` field (0 here, post-clamp) and leaves
  // `fire`/`police` honestly null (never fabricated) until the next
  // cadence tick.
  assert.deepEqual(
    sanitizeTrafficSnapshot({ tick: -5, medianCommuteMinutes: -3, gridlockShare: -2, coverageShare: -1 }),
    {
      tick: 0,
      medianCommuteMinutes: 0,
      gridlockShare: 0,
      coverageShare: 0,
      safeRoadScore: 1,
      integratedTransportScore: 0,
      p90CommuteMinutes: 0,
      vOverCBySegment: {},
      coverageShareByService: { ambulance: 0, fire: null, police: null },
    },
    'negative fields must floor at 0; absent inc9/inc10 fields must default to their documented neutrals, never be silently dropped from the shape'
  );
  assert.equal(
    sanitizeTrafficSnapshot({ ...good, coverageShare: null }).coverageShare,
    null,
    'a null coverageShare must survive sanitisation as null (ASM-1518 honest absence), not become 0'
  );

  // Oversized shares clamp to 1; the tick is floored to an integer.
  const over = sanitizeTrafficSnapshot({ tick: 12.7, medianCommuteMinutes: 5, gridlockShare: 99, coverageShare: 99 });
  assert.equal(over.gridlockShare, 1, 'an oversized gridlockShare must clamp to 1');
  assert.equal(over.coverageShare, 1, 'an oversized coverageShare must clamp to 1');
  assert.equal(over.tick, 12, 'a fractional tick must floor to an integer');
});
