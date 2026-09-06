// bug-684-cifix-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND on BUG-684's
// CI fix (attacker "opus-verify-cifix2", 2026-09-06, GR#23: attacker is never
// the author).
//
// SUBJECT: `consolidatorEconomyBaselineOf(s)` (engine.ts) — the new memo that
// replaces the per-pass `cur.buildings.reduce(...)` structural-outflow fold,
// keyed on the pair (s.buildings identity, s.roadConnectivity identity) with a
// `nextTransitionTick` escape hatch for the ONE read-set member the key
// deliberately omits: `s.tick`, via isOnline's G1 construction gate.
//
// THE HOLE THIS FILE PINS: a building finishing construction flips isOnline
// false -> true with NO new buildings array and NO new roadConnectivity object
// (place/demolish/upgrade mint new arrays; the mere PASSAGE OF TIME does not).
// Without `nextTransitionTick` the memo would serve the pre-completion upkeep
// total forever — i.e. the runway floor (funds >= INSOLVENCY_WARNING_THRESHOLD
// + max(netCost, netOutflowPerTick * 900)) would keep judging the city against
// an outflow it no longer has, letting an unsafe merge straight through: the
// EXACT under-read BUG-684's whole re-round exists to close, reintroduced by
// the perf fix rather than by the cold-lastFlows read.
//
// The fixture makes that difference a 10x swing in the floor, so the pass's
// own decision (merge vs `skipped.reason === 'funds floor'`) is the observable
// — no instrumentation counters, no timing:
//   * a pow_hydro (upkeep 277,778/tick) whose construction completes at tick
//     200, in a 5x fire_post section that HAS a real merge opportunity;
//   * BEFORE completion the structural outflow is ~541/tick, so the runway
//     term (0.49M) is dominated by the merge's own netCost (~25M) -> floor
//     ~25M -> funds 100M MERGES;
//   * AFTER completion the outflow is ~278,319/tick -> runway term ~250M ->
//     floor ~250M -> funds 100M must REFUSE with reason 'funds floor'.
// Both states share the SAME frozen `buildings` array object and the SAME
// `roadConnectivity` object, so nothing but `s.tick` differs between them —
// which is precisely the memo key's blind spot.
//
// MUTATION PROOF (run by the attacker before landing this file): deleting the
// `nextTransitionTick` bookkeeping from consolidatorEconomyBaselineOf (cache
// entries recorded/honoured with Infinity) leaves the whole pre-existing
// consolidator estate GREEN and turns ONLY this file's second assertion RED.
//
// FINDING (documented, not asserted as a pass/fail here — see the round note):
// the guard is one-sided. A cache entry folded at tick T is honoured for ANY
// state with `s.tick < nextTransitionTick`, including states whose tick is
// EARLIER than T, where the building was NOT yet online. That direction is not
// reachable from the forward-only reducer, but it DOES make the function's
// answer depend on what else ran earlier in the same JS realm whenever two
// states share a buildings array at different ticks (the ordering-dependence
// demonstrated in the round report). Recorded as a follow-up, not a blocker.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  computeRoadConnectivity,
  constructionTicks,
  isOnline,
  upkeepChargeableOf,
} from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  CONSOLIDATOR_UNLOCK_LEVEL,
  xpForLevel,
  levelOf,
} from '../src/sim/engine.ts';

/** The high-upkeep building whose construction completion is the whole point. */
const BIG_SPEC = 'pow_hydro';
/** Tick at which BIG_SPEC comes online (G1). Chosen strictly between the two
 *  pass ticks below so one pass sees it offline and the other sees it online. */
const COMPLETES_AT = 200;
const BIG_BUILT_TICK = COMPLETES_AT - constructionTicks(SPECS[BIG_SPEC]);

/** ONE shared, frozen buildings array — the memo's primary cache key. Frozen so
 *  a stray in-place mutation (which would silently invalidate the whole premise
 *  of this test) throws instead of passing quietly. */
const BUILDINGS = Object.freeze([
  ...Array.from({ length: 41 }, (_, x) => ({ id: 5000 + x, spec: 'road', x, y: 15, builtTick: -1000 })),
  ...Array.from({ length: 5 }, (_, i) => ({ id: 100 + i, spec: 'fire_post', x: 16 + i, y: 14, builtTick: -1000 })),
  // Merge headroom (the consolidator refuses to exceed the family's own
  // existing successor allowance) — sited far away, already online.
  ...[900, 901, 902, 903].map((id, i) => ({ id, spec: 'fire_station', x: 200 + i * 10, y: 200, builtTick: -1000 })),
  // The building whose completion the memo must notice.
  { id: 777, spec: BIG_SPEC, x: 30, y: 14, builtTick: BIG_BUILT_TICK },
]);

function baseState(tick, funds) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: BUILDINGS,
    population: 0,
    funds,
    tick,
    consolidatorEnabled: true,
    consolidatorLayoutEnabled: false,
    consolidatorLog: [],
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    consolidatorMode: 'monthly-twelfth',
    nextId: 9000,
  };
}

// ONE connectivity object shared by both states — the memo's secondary cache
// key. computeRoadConnectivity is itself memoised on s.buildings, so the real
// reducer would hand back this same object anyway; pinning it explicitly makes
// the "only s.tick differs" premise airtight.
const CONNECTIVITY = computeRoadConnectivity(baseState(0, 1));
const st = (tick, funds) => ({ ...baseState(tick, funds), roadConnectivity: CONNECTIVITY });
const lastPass = (s) => (s.consolidatorLog ?? [])[0] ?? null;

// Pass ticks: both are month boundaries (TICKS_PER_MONTH = 30) landing on the
// SAME monthly-twelfth section scope (month 1 and month 13 -> twelfth 1), so
// the fire_post section is in scope for both and the ONLY difference between
// the two passes is whether the hydro plant has finished building.
const TICK_BEFORE_COMPLETION = 30;
const TICK_AFTER_COMPLETION = 390;
const FUNDS = 100_000_000;

describe('BUG-684 CI fix: consolidatorEconomyBaselineOf must not serve a pre-completion upkeep total after a building comes online', () => {
  test('setup: the two states differ ONLY in tick, and the hydro plant flips offline -> online between them', () => {
    const before = st(TICK_BEFORE_COMPLETION - 1, FUNDS);
    const after = st(TICK_AFTER_COMPLETION - 1, FUNDS);
    assert.equal(before.buildings, after.buildings, 'same buildings array identity (the memo key)');
    assert.equal(before.roadConnectivity, after.roadConnectivity, 'same roadConnectivity identity (the memo key)');

    const big = BUILDINGS.find((b) => b.id === 777);
    const upkeepOf = (s) =>
      s.buildings.reduce((sum, b) => {
        if (!isOnline(s, b)) return sum;
        const sp = SPECS[b.spec];
        return !sp || !sp.upkeep ? sum : sum + upkeepChargeableOf(b, sp);
      }, 0);
    const upBefore = upkeepOf(st(TICK_BEFORE_COMPLETION, FUNDS));
    const upAfter = upkeepOf(st(TICK_AFTER_COMPLETION, FUNDS));
    assert.equal(isOnline(st(TICK_BEFORE_COMPLETION, FUNDS), big), false, 'hydro still under construction at tick 30');
    assert.equal(isOnline(st(TICK_AFTER_COMPLETION, FUNDS), big), true, 'hydro online at tick 390');
    assert.ok(
      upAfter > upBefore * 100,
      `structural upkeep jumps by orders of magnitude across completion (${upBefore} -> ${upAfter})`,
    );
  });

  // ORDER MATTERS: this pass runs FIRST so it populates the memo with the
  // PRE-completion total. The next test then proves the memo notices the
  // completion rather than serving that total back.
  test('before completion: the merge is affordable and goes through (baseline outflow ~541/tick)', () => {
    const pass = lastPass(reducer(st(TICK_BEFORE_COMPLETION - 1, FUNDS), { type: 'tick' }));
    assert.ok(pass, 'a consolidator pass ran');
    assert.equal(
      pass.transactions.length,
      1,
      `funds ${FUNDS} clear the netCost-sized floor while the hydro plant is still building ` +
        `(skips: ${JSON.stringify(pass.skipped?.map((k) => k.reason) ?? [])})`,
    );
  });

  test('after completion: the SAME buildings array at a later tick must refuse — the memo re-folds at nextTransitionTick', () => {
    const pass = lastPass(reducer(st(TICK_AFTER_COMPLETION - 1, FUNDS), { type: 'tick' }));
    assert.ok(pass, 'a consolidator pass ran');
    // RED-PROOF: with the nextTransitionTick invalidation removed from
    // consolidatorEconomyBaselineOf, the previous test's cached 541/tick total
    // is served here and this merge is (wrongly) allowed.
    assert.equal(
      pass.transactions.length,
      0,
      'the now-online 277,778/tick hydro plant must push the runway floor above 100M and block the merge',
    );
    assert.deepEqual(
      pass.skipped.map((k) => k.reason),
      ['funds floor'],
      'and the refusal must be the runway/funds floor, not some unrelated skip',
    );
  });
});
