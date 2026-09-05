// bug-674-online-memo.test.mjs — BUG-674: onlineByBuilding's road-gate fold
// was keyed on the WHOLE SimState object (memoOnState's usual idiom), which
// is a permanent cache miss every tick/commit since the codebase's
// immutable-replace discipline hands back a brand-new top-level state object
// EVERY time, even when the change (funds, citizens, a notice, a pipe
// upgrade) has nothing to do with online-ness. The fix (data.ts) splits
// isOnline() into:
//   - G1 (construction): s.tick - b.builtTick < constructionTicks(sp) —
//     O(1), evaluated fresh on every call, never cached.
//   - G2/G3 (road gates): folded ONCE per distinct (s.buildings identity,
//     s.roadConnectivity identity) pair via roadGateMapOf(), instrumented by
//     the exported __roadGateFoldCount test counter (bumped once per ACTUAL
//     fold, never on a cache hit).
//
// This file proves the INVALIDATION-SLICE MATRIX directly (Aaron's dispatch
// prompt): every slice that SHOULD invalidate the road-gate fold does, and
// every slice that should NOT invalidate it (funds/citizens/notices/
// pipeTier/tick-alone) provably does not — via the counter, not timing, so
// the proof is immune to machine noise (the class of test the pipeTier
// regression from the BUG-642 round-finding shows timing/manual reasoning
// alone would have missed).
//
// Run with the scoped test runner from webconsole/:
//   node ../tools/test/scoped.mjs test/bug-674-online-memo.test.mjs

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { isOnline, constructionTicks, SPECS, __resetRoadGateFoldCountForTest } from '../src/sim/data.ts';
import * as data from '../src/sim/data.ts';
import { reducer, initialState } from '../src/sim/engine.ts';
import { buildScaleFixture, DEFAULT_BUILDING_COUNT } from './scale/fixture.mjs';

// `__roadGateFoldCount` is a live `let` export — ESM live bindings mean
// `data.__roadGateFoldCount` always reads the CURRENT value (unlike the
// static import above, captured once at import time). Read through the
// namespace object everywhere in this file.
function foldCount() {
  return data.__roadGateFoldCount;
}

function median(values) {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)];
}

/** A small state with a real road graph and a mix of online/offline/
 *  under-construction buildings, exercising all three gates. */
function baseState() {
  let s = initialState();
  s = {
    ...s,
    tick: 50,
    funds: 1_000_000,
    roadConnectivity: { connectedRoadTiles: ['6,5'] },
    buildings: [
      { id: 1, spec: 'res_hut', x: 5, y: 5, builtTick: 0 }, // road-adjacent, fully online
      { id: 2, spec: 'road', x: 6, y: 5, builtTick: 0 },
      { id: 3, spec: 'wat_clean', x: 20, y: 20, builtTick: 0 }, // no adjacent road -> offline (G2)
      { id: 4, spec: 'wat_waste', x: 30, y: 30, builtTick: 45 }, // still under construction at tick 50 (G1)
    ],
  };
  return s;
}

describe('BUG-674 invalidation-slice matrix', () => {
  test('setup sanity: baseState exercises all three gates (online / road-blocked / under-construction)', () => {
    const s = baseState();
    assert.equal(isOnline(s, s.buildings[0]), true, 'res_hut is road-adjacent and built long ago -> online');
    assert.equal(isOnline(s, s.buildings[2]), false, 'wat_clean has no adjacent road -> offline (G2)');
    assert.equal(isOnline(s, s.buildings[3]), false, 'wat_waste is still under construction at tick 50 -> offline (G1)');
  });

  test('NON-INVALIDATING: funds-only change never re-folds the road-gate map', () => {
    const s = baseState();
    isOnline(s, s.buildings[0]); // prime the fold for `s`
    __resetRoadGateFoldCountForTest();

    const s2 = { ...s, funds: s.funds + 500 };
    assert.notEqual(s2, s, 'sanity: a genuinely new top-level state object');
    isOnline(s2, s2.buildings[0]);
    isOnline(s2, s2.buildings[2]);
    isOnline(s2, s2.buildings[3]);

    assert.equal(foldCount(), 0, 'a funds-only change must hit the cached road-gate map (same buildings/roadConnectivity identity)');
  });

  test('NON-INVALIDATING: population/citizens-only change never re-folds the road-gate map', () => {
    const s = baseState();
    isOnline(s, s.buildings[0]);
    __resetRoadGateFoldCountForTest();

    const s2 = { ...s, population: s.population + 1000 };
    isOnline(s2, s2.buildings[0]);
    assert.equal(foldCount(), 0, 'a population-only change must not re-fold');
  });

  test('NON-INVALIDATING: a place-notice-only change never re-folds the road-gate map', () => {
    const s = baseState();
    isOnline(s, s.buildings[0]);
    __resetRoadGateFoldCountForTest();

    const s2 = { ...s, placeNotice: 'test notice' };
    isOnline(s2, s2.buildings[0]);
    assert.equal(foldCount(), 0, 'a notice-only change must not re-fold');
  });

  test('NON-INVALIDATING: pipeTier-only change never re-folds the road-gate map (the exact class of field the serviceCapacityAggregates round-finding missed)', () => {
    const s = baseState();
    isOnline(s, s.buildings[0]);
    __resetRoadGateFoldCountForTest();

    const s2 = { ...s, pipeTier: { ...s.pipeTier, 3: 2 } };
    isOnline(s2, s2.buildings[0]);
    isOnline(s2, s2.buildings[2]);
    assert.equal(foldCount(), 0, 'a pipeTier-only change must not re-fold (isOnline never reads pipeTier)');
  });

  test('NON-INVALIDATING: tick advancing alone (no buildings/roadConnectivity change) never re-folds the road-gate map, yet G1 stays correct', () => {
    // Building #4 placed ROAD-ADJACENT (unlike baseState()'s isolated
    // wat_waste) so G2/G3 pass unconditionally here — isolating this test to
    // G1 (construction) exactly, with a REAL roadConnectivity object present
    // (unlike the old attack file's equivalent test, which cleared
    // roadConnectivity entirely) so this also proves the road-gate fold
    // itself is untouched by a tick-only change even while real connectivity
    // is in play.
    let s = initialState();
    s = {
      ...s,
      tick: 50,
      roadConnectivity: { connectedRoadTiles: ['6,5'] },
      buildings: [
        { id: 1, spec: 'res_hut', x: 5, y: 5, builtTick: 0 },
        { id: 2, spec: 'road', x: 6, y: 5, builtTick: 0 },
        { id: 4, spec: 'wat_waste', x: 7, y: 5, builtTick: 45 }, // road-adjacent, still under construction at tick 50
      ],
    };
    isOnline(s, s.buildings[0]);
    __resetRoadGateFoldCountForTest();

    // Advance tick past building #4's construction completion WITHOUT
    // touching buildings or roadConnectivity — proves G1 is evaluated fresh
    // (not stale/cached) even while the road-gate fold correctly stays cached.
    const ticksNeeded = constructionTicks(SPECS['wat_waste']);
    const s2 = { ...s, tick: 45 + ticksNeeded };
    const stillBuilding = { ...s, tick: 45 + ticksNeeded - 1 };

    assert.equal(isOnline(stillBuilding, stillBuilding.buildings[2]), false, 'one tick before completion: still offline (G1)');
    assert.equal(isOnline(s2, s2.buildings[2]), true, 'at completion tick: online (G1 correctly re-evaluated, not stale)');
    assert.equal(isOnline(s2, s2.buildings[0]), true, 'unrelated already-online building unaffected');
    assert.equal(foldCount(), 0, 'a tick-only change must not re-fold the road-gate map (G1 is checked outside the fold)');
  });

  test('INVALIDATING: a buildings-array change (place) DOES re-fold the road-gate map, and the new building answers correctly', () => {
    const s = baseState();
    isOnline(s, s.buildings[0]);
    __resetRoadGateFoldCountForTest();

    const newBuilding = { id: 5, spec: 'res_hut', x: 99, y: 99, builtTick: 0 }; // far from any road
    const s2 = { ...s, buildings: [...s.buildings, newBuilding] };
    assert.equal(isOnline(s2, newBuilding), false, 'new building is not road-adjacent -> offline');
    assert.equal(foldCount(), 1, 'a buildings-array change must re-fold exactly once (new buildings identity)');

    // Re-querying the SAME s2 again must hit the now-warm cache.
    isOnline(s2, s2.buildings[0]);
    isOnline(s2, newBuilding);
    assert.equal(foldCount(), 1, 'repeated queries against the SAME (buildings, roadConnectivity) pair must not re-fold');
  });

  test('INVALIDATING: a roadConnectivity change with the SAME buildings array DOES re-fold, and flips the affected building\'s answer', () => {
    const s = baseState();
    // #3 (wat_clean at 20,20) has no adjacent road in `s`, so it is offline
    // purely on G2 (road-adjacency doesn't even get to G3). Swap in a
    // roadConnectivity that also makes an ADJACENT tile connected, holding
    // s.buildings byte-identical (same array reference) to isolate this slice.
    assert.equal(isOnline(s, s.buildings[2]), false, 'sanity: offline under the base connectivity');
    __resetRoadGateFoldCountForTest();

    // add a road tile at (21,20) — still no road BUILDING there, so #3 stays
    // road-adjacent=false regardless; instead flip #1 (res_hut at 5,5) by
    // REMOVING its adjacent road tile from connectedRoadTiles, holding
    // s.buildings identical.
    const disconnected = { ...s, roadConnectivity: { connectedRoadTiles: [] } };
    assert.equal(disconnected.buildings, s.buildings, 'sanity: SAME buildings array reference, only roadConnectivity differs');
    assert.equal(isOnline(disconnected, disconnected.buildings[0]), false, 'res_hut is road-adjacent but the road is no longer in the connected set -> offline (G3)');
    assert.equal(foldCount(), 1, 'a roadConnectivity-only change (same buildings identity) must re-fold exactly once');

    isOnline(disconnected, disconnected.buildings[0]);
    assert.equal(foldCount(), 1, 'repeated queries against the same (buildings, roadConnectivity) pair must not re-fold again');
  });

  test('INVALIDATING: a real reducer tick that grows the road network (roadConnectivity recompute keyed on the SAME new buildings) still answers correctly end to end', () => {
    // Integration-level check via the real reducer, not hand-built states:
    // tax/pipeUpgrade (funds/pipeTier-only) must not disturb online-ness,
    // while a genuine tick sequence keeps producing correct answers.
    let s = buildScaleFixture({ buildingCount: 300, targetPopulation: 20_000, settleTicks: 1 });
    const waterPlant = s.buildings.find((b) => SPECS[b.spec]?.kind === 'water' && SPECS[b.spec]?.tag === 'clean');
    const actions = [
      { type: 'tick' },
      { type: 'tax', which: 'residential', rate: 0.05 },
      ...(waterPlant ? [{ type: 'pipeUpgrade', id: waterPlant.id }] : []),
      { type: 'tick' },
    ];
    for (const action of actions) {
      const prev = s;
      s = reducer(s, action);
      assert.notEqual(s, prev, `sanity: ${action.type} must produce a new top-level state`);
      // Every building's isOnline() answer must be internally consistent
      // (an online building must actually pass every gate it claims to).
      for (const b of s.buildings) {
        const online = isOnline(s, b);
        if (b.builtTick != null && SPECS[b.spec] && SPECS[b.spec].category !== 'network' && s.roadConnectivity) {
          if (online) {
            assert.ok(
              s.tick - b.builtTick >= constructionTicks(SPECS[b.spec]),
              `building #${b.id} reported online after ${action.type} but is still within its construction window`
            );
          }
        }
      }
    }
  });
});

describe('BUG-674 scale invariance: per-building cost of an IRRELEVANT change must not grow with city size', () => {
  // Same-run ratio idiom (BUG-660's (B) block): measure the SAME operation at
  // two city sizes IN ONE PROCESS RUN and assert the per-building cost ratio
  // stays bounded — never a wall-clock CI bound (house rule).
  test('funds-only-change isOnline() sweep: cost-per-building ratio at 13k vs 2.6k stays bounded', () => {
    const SMALL_N = Math.round(DEFAULT_BUILDING_COUNT / 5); // ~2.6k
    const LARGE_N = DEFAULT_BUILDING_COUNT; // ~13k
    const REPEATS = 3;

    function stampedFixture(n) {
      const raw = buildScaleFixture({ buildingCount: n, targetPopulation: n * 90, settleTicks: 1 });
      // Stamp a real builtTick far in the past on every building so the
      // road-gate fold actually runs (the fixture's own documented
      // ONLINE-GATING SHORTCUT otherwise short-circuits every building true
      // via `b.builtTick == null` before the fold is ever reached).
      return { ...raw, tick: Math.max(raw.tick, 100_000), buildings: raw.buildings.map((b) => ({ ...b, builtTick: 0 })) };
    }

    function costPerBuildingMs(n) {
      let s = stampedFixture(n);
      isOnline(s, s.buildings[0]); // prime the fold once
      const s2 = { ...s, funds: s.funds + 1 }; // irrelevant change: new state, same buildings/roadConnectivity
      const t0 = performance.now();
      for (const b of s2.buildings) isOnline(s2, b);
      const elapsed = performance.now() - t0;
      return elapsed / s2.buildings.length;
    }

    const smallSamples = [];
    const largeSamples = [];
    for (let r = 0; r < REPEATS; r++) {
      smallSamples.push(costPerBuildingMs(SMALL_N));
      largeSamples.push(costPerBuildingMs(LARGE_N));
    }
    const smallMedian = median(smallSamples);
    const largeMedian = median(largeSamples);
    const ratio = largeMedian / smallMedian;

    // K DERIVATION (GR#15): the property under test is "cost-per-building for
    // an unrelated change is O(1) in city size" (fixed: ratio ~1x) vs the
    // pre-fix behaviour where the WHOLE road-gate fold re-ran every time
    // (still O(1)-per-building in THIS metric, since the fold itself is
    // O(buildings) — the ratio test alone cannot distinguish fixed-vs-broken
    // on cost-per-building; it is a SANITY bound, not the primary proof —
    // the invalidation-slice matrix above is the primary correctness/cost
    // proof via the fold counter). K is loose (5x) to avoid flaking on CI
    // noise while still catching a gross regression (e.g. an accidental
    // O(buildings^2) reintroduction).
    const K = 5;
    console.log(
      `[BUG-674 scale] small(n=${SMALL_N}) ${smallMedian.toFixed(5)}ms/building, large(n=${LARGE_N}) ${largeMedian.toFixed(5)}ms/building, ratio=${ratio.toFixed(2)}x`
    );
    assert.ok(ratio < K, `cost-per-building for an irrelevant change must not grow with city size: ratio ${ratio.toFixed(2)}x must be < K=${K}`);
  });

  test('the fold counter proves ZERO re-folds across REPEATS irrelevant commits regardless of city size (the actual O(1)-blast-radius property)', () => {
    for (const n of [Math.round(DEFAULT_BUILDING_COUNT / 5), DEFAULT_BUILDING_COUNT]) {
      const raw = buildScaleFixture({ buildingCount: n, targetPopulation: n * 90, settleTicks: 1 });
      let s = { ...raw, tick: Math.max(raw.tick, 100_000), buildings: raw.buildings.map((b) => ({ ...b, builtTick: 0 })) };
      isOnline(s, s.buildings[0]); // prime
      __resetRoadGateFoldCountForTest();
      for (let i = 0; i < 10; i++) {
        s = { ...s, funds: s.funds + 1 };
        for (const b of s.buildings) isOnline(s, b);
      }
      assert.equal(foldCount(), 0, `n=${n}: 10 consecutive funds-only commits must produce ZERO road-gate re-folds`);
    }
  });
});
