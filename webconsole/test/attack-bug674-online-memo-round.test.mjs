// attack-bug674-online-memo-round.test.mjs
//
// INDEPENDENT DESTRUCTIVE ROUND (GR#23) against BUG-674 — the split of
// isOnline()'s memo into (a) a FRESH-every-call G1 construction gate and
// (b) a road-gate fold cached on the PAIR (s.buildings identity,
// s.roadConnectivity identity) via roadGateMapOf().
//
// The author's own bug-674-online-memo.test.mjs proves the invalidation
// matrix with the fold counter on HAND-BUILT states. This file attacks the
// thing hand-built states cannot reach: whether the cache's *premise* — that
// s.buildings and s.roadConnectivity are always identity-replaced when their
// meaning changes — actually holds under the REAL reducer, the REAL batch
// placement path (BUG-660's mutated-in-place board), replay, and the
// downstream selectors that consume isOnline through their OWN coarser
// caches (sectionIndexOf is keyed on s.buildings ALONE — if G1's now-fresh
// answer gets frozen inside it, the bug is simply reborn one layer up).
//
// The centrepiece is the ORACLE (A5): after every real action, every
// building's cached isOnline answer is compared against the same answer
// recomputed on a deep-identity-cloned state, which is guaranteed to miss
// every cache. Any in-place mutation anywhere in the engine that poisons the
// road-gate cache shows up here as a disagreement, without the attacker
// having to guess WHICH write site did it.
//
// Run: node ../tools/test/scoped.mjs test/attack-bug674-online-memo-round.test.mjs

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  isOnline,
  constructionTicks,
  SPECS,
  buildingDisplayStates,
  __resetRoadGateFoldCountForTest,
} from '../src/sim/data.ts';
import * as data from '../src/sim/data.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { sectionIndexOf } from '../src/sim/consolidator.ts';
import { emptyJournal, recordAction, isStateAffecting } from '../src/sim/journal.ts';
import { replayFromGenesis, stableStringify } from '../src/sim/genesisReplay.ts';

const foldCount = () => data.__roadGateFoldCount;

/**
 * A state whose buildings array, every Building object inside it, and whose
 * roadConnectivity object are all BRAND NEW identities — so every
 * identity-keyed cache in data.ts (roadGateCache, roadTileSetCache,
 * connectedSetCache, occupiedSetCache, ...) provably MISSES and recomputes
 * from scratch. This is the uncached oracle the cached answers are graded
 * against.
 */
function identityClone(s) {
  return {
    ...s,
    buildings: s.buildings.map((b) => ({ ...b })),
    roadConnectivity: s.roadConnectivity
      ? { ...s.roadConnectivity, connectedRoadTiles: [...s.roadConnectivity.connectedRoadTiles] }
      : s.roadConnectivity,
  };
}

/** Assert every building's CACHED isOnline agrees with the uncached oracle. */
function assertNoStaleness(s, label) {
  const oracle = identityClone(s);
  for (let i = 0; i < s.buildings.length; i++) {
    const cached = isOnline(s, s.buildings[i]);
    const truth = isOnline(oracle, oracle.buildings[i]);
    assert.equal(
      cached,
      truth,
      `${label}: STALE CACHE — building #${s.buildings[i].id} (${s.buildings[i].spec} @ ${s.buildings[i].x},${s.buildings[i].y}) ` +
        `cached isOnline=${cached} but a cache-free recomputation says ${truth}`
    );
  }
}

function driveAndRecord(actions, seed) {
  let journal = emptyJournal();
  let state = seed ? seed(initialState()) : initialState();
  for (const action of actions) {
    if (isStateAffecting(action)) journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  return { journal, liveState: state };
}

// ════════════════════════════════════════════════════════════════════════
// A1 — REAL REDUCER INVALIDATION. Not hand-built `{...s, roadConnectivity:
// {...}}` states: the actual place/bulldoze pipeline, where roadConnectivity
// is recomputed by computeRoadConnectivity (itself memoised on s.buildings,
// so the (buildings, connectivity) PAIR collapses to buildings in this path
// — the exact place a two-level key could hide a missing invalidation).
// ════════════════════════════════════════════════════════════════════════
describe('BUG-674 A1: real reducer place/bulldoze invalidation', () => {
  test('bulldozing the road a building depends on flips it OFFLINE through the real reducer', () => {
    let s = { ...initialState(), funds: 50_000_000 };
    // Lay a road spine out from the map edge so the network is genuinely seeded.
    for (let x = 0; x < 12; x++) s = reducer(s, { type: 'place', spec: 'road', x, y: 10 });
    s = reducer(s, { type: 'place', spec: 'res_hut', x: 6, y: 11 });
    // Age past every construction window (G1 out of the way).
    s = { ...s, tick: s.tick + 5000 };

    const hut = s.buildings.find((b) => b.spec === 'res_hut');
    assert.ok(hut, 'precondition: the hut was placed');
    assert.equal(isOnline(s, hut), true, 'precondition: hut is road-connected and online');
    assertNoStaleness(s, 'after placement');

    // Demolish EVERY road tile — the hut must lose its road adjacency.
    let cur = s;
    for (let x = 0; x < 12; x++) {
      // bulldoze may also remove auto-laid connectors; loop until (x,10) is clear.
      cur = reducer(cur, { type: 'bulldoze', x, y: 10 });
    }
    // Bulldoze any remaining ordinary road tile (auto-laid connectors
    // included). The m20 motorway seed is deliberately left alone — it is not
    // adjacent to the hut, so it cannot re-satisfy G2.
    for (let guard = 0; guard < 500; guard++) {
      const road = cur.buildings.find((b) => SPECS[b.spec] && SPECS[b.spec].kind === 'road');
      if (!road) break;
      const next = reducer(cur, { type: 'bulldoze', x: road.x, y: road.y });
      if (next === cur) break;
      cur = next;
    }
    const roadsLeft = cur.buildings.filter((b) => SPECS[b.spec] && SPECS[b.spec].kind === 'road');
    assert.equal(roadsLeft.length, 0, 'precondition: every road tile is gone');

    const hutAfter = cur.buildings.find((b) => b.id === hut.id);
    assert.ok(hutAfter, 'the hut itself survived the road demolition');
    assert.equal(
      isOnline(cur, hutAfter),
      false,
      'STALE: the hut still reports ONLINE after every road it depended on was bulldozed'
    );
    assertNoStaleness(cur, 'after road demolition');
  });

  test('re-laying the road flips the SAME building back ONLINE (offline -> online direction)', () => {
    let s = { ...initialState(), funds: 50_000_000 };
    for (let x = 0; x < 12; x++) s = reducer(s, { type: 'place', spec: 'road', x, y: 10 });
    s = reducer(s, { type: 'place', spec: 'res_hut', x: 6, y: 11 });
    s = { ...s, tick: s.tick + 5000 };
    const hut = s.buildings.find((b) => b.spec === 'res_hut');
    assert.ok(hut, 'precondition: the hut was placed');

    // Strip every road so the hut is genuinely stranded.
    let cur = s;
    for (let guard = 0; guard < 400; guard++) {
      const road = cur.buildings.find((b) => SPECS[b.spec] && SPECS[b.spec].kind === 'road');
      if (!road) break;
      const next = reducer(cur, { type: 'bulldoze', x: road.x, y: road.y });
      if (next === cur) break;
      cur = next;
    }
    let hutNow = cur.buildings.find((b) => b.id === hut.id);
    assert.ok(hutNow, 'the hut survived');
    assert.equal(isOnline(cur, hutNow), false, 'precondition: stranded hut is offline');
    assertNoStaleness(cur, 'stranded hut');

    // Lay a fresh road spine from the map edge back to it.
    for (let x = 0; x <= 6; x++) cur = reducer(cur, { type: 'place', spec: 'road', x, y: 10 });
    cur = { ...cur, tick: cur.tick + 5000 };
    hutNow = cur.buildings.find((b) => b.id === hut.id);
    assert.equal(
      isOnline(cur, hutNow),
      true,
      'STALE: the hut is edge-connected by a freshly laid road again but still reports offline'
    );
    assertNoStaleness(cur, 'after re-laying the road');
  });
});

// ════════════════════════════════════════════════════════════════════════
// A2 — G1 FRESHNESS THROUGH THE DOWNSTREAM SELECTORS ("the bug reborn one
// layer up"). isOnline() is now fresh on tick, but the UI reads it through
// buildingDisplayStates (memoOnState — keyed on the WHOLE state, safe) and
// the consolidator reads it through sectionIndexOf, which is keyed on
// s.buildings ALONE. If sectionIndexOf froze a pre-completion answer, the
// fresh G1 would be invisible where it matters.
// ════════════════════════════════════════════════════════════════════════
describe('BUG-674 A2: G1 freshness survives the downstream isOnline consumers', () => {
  function completionFixture() {
    let s = initialState();
    return {
      ...s,
      tick: 46,
      funds: 10_000_000,
      roadConnectivity: { connectedRoadTiles: ['6,5', '7,5', '8,5'] },
      buildings: [
        { id: 1, spec: 'road', x: 6, y: 5, builtTick: 0 },
        { id: 2, spec: 'road', x: 7, y: 5, builtTick: 0 },
        { id: 3, spec: 'road', x: 8, y: 5, builtTick: 0 },
        // road-adjacent (below the road at 7,5), still under construction at t=46
        { id: 4, spec: 'res_hut', x: 7, y: 6, builtTick: 45 },
      ],
    };
  }

  test('buildingDisplayStates reports the flip on the EXACT completion tick (tick-only change)', () => {
    const s = completionFixture();
    const hut = s.buildings[3];
    const flipAt = 45 + constructionTicks(SPECS['res_hut']);
    assert.ok(flipAt > s.tick, 'precondition: the hut is genuinely mid-construction at the fixture tick');

    const before = { ...s, tick: flipAt - 1 };
    const at = { ...s, tick: flipAt };
    assert.equal(before.buildings, s.buildings, 'sanity: tick-only change, same buildings identity');
    assert.equal(at.buildings, s.buildings, 'sanity: tick-only change, same buildings identity');

    assert.equal(buildingDisplayStates(before).get(hut.id).online, false, 'one tick before completion: display says offline');
    assert.equal(
      buildingDisplayStates(at).get(hut.id).online,
      true,
      'STALE DOWNSTREAM: the UI-facing selector did not see G1 complete on the exact completion tick'
    );
  });

  test('sectionIndexOf (keyed on s.buildings ALONE) does not freeze a pre-completion G1 answer', () => {
    const s = completionFixture();
    const hut = s.buildings[3];
    const flipAt = 45 + constructionTicks(SPECS['res_hut']);

    const before = { ...s, tick: flipAt - 1 };
    const at = { ...s, tick: flipAt };

    // Prime the buildings-keyed section cache at the PRE-completion tick.
    const idxBefore = sectionIndexOf(before);
    const strandedBefore = [...idxBefore.values()].reduce((n, a) => n + a.stranded.constructionCount, 0);
    assert.equal(strandedBefore, 1, 'precondition: the hut is counted as construction-stranded before completion');

    // Same buildings identity, one tick later: the cache MUST expire itself.
    const idxAt = sectionIndexOf(at);
    const strandedAt = [...idxAt.values()].reduce((n, a) => n + a.stranded.constructionCount, 0);
    assert.equal(
      strandedAt,
      0,
      'STALE DOWNSTREAM: sectionIndexOf served a cached pre-completion classification after G1 flipped ' +
        '(buildings identity unchanged) — the BUG-674 fix would be invisible to the consolidator'
    );
    assert.equal(isOnline(at, hut), true, 'and isOnline itself agrees');
  });
});

// ════════════════════════════════════════════════════════════════════════
// A3 — THE SENTINEL KEY. roadGateMapOf substitutes a single module-global
// NO_ROAD_CONNECTIVITY object when s.roadConnectivity is absent. Attack the
// transitions in BOTH directions across an UNCHANGED buildings array — the
// only shape where the two-level key can alias.
// ════════════════════════════════════════════════════════════════════════
describe('BUG-674 A3: the NO_ROAD_CONNECTIVITY sentinel key cannot alias', () => {
  function pair() {
    const buildings = [
      { id: 1, spec: 'res_hut', x: 5, y: 5, builtTick: 0 },
      { id: 2, spec: 'road', x: 6, y: 5, builtTick: 0 },
    ];
    const base = { ...initialState(), tick: 5000, buildings };
    return { buildings, base };
  }

  test('absent -> present -> absent, same buildings array, answers never alias', () => {
    const { buildings, base } = pair();
    const noConn = { ...base, roadConnectivity: undefined };
    const conn = { ...base, roadConnectivity: { connectedRoadTiles: ['6,5'] } };
    const disconn = { ...base, roadConnectivity: { connectedRoadTiles: [] } };
    const noConn2 = { ...base, roadConnectivity: undefined };

    assert.equal(noConn.buildings, conn.buildings, 'sanity: one shared buildings array across all four states');

    // Backward-tolerance: no connectivity graph -> gates skipped -> online.
    assert.equal(isOnline(noConn, buildings[0]), true, 'no connectivity graph: gate skipped (backward tolerance)');
    // Real connectivity including the adjacent road -> online.
    assert.equal(isOnline(conn, buildings[0]), true, 'connected road adjacent -> online');
    // Real connectivity EXCLUDING the adjacent road -> offline (G3).
    assert.equal(isOnline(disconn, buildings[0]), false, 'road present but not in the connected set -> offline (G3)');
    // Back to absent: MUST return to the skipped-gate answer, not the last real one.
    assert.equal(
      isOnline(noConn2, buildings[0]),
      true,
      'ALIAS: a state with NO connectivity graph inherited a cached answer from a state that HAD one'
    );
    // And re-querying the disconnected state must still say offline.
    assert.equal(isOnline(disconn, buildings[0]), false, 'ALIAS: the disconnected answer was clobbered');
  });

  test('null vs undefined roadConnectivity share the sentinel and that is semantically correct', () => {
    const { buildings, base } = pair();
    const undef = { ...base, roadConnectivity: undefined };
    const nul = { ...base, roadConnectivity: null };
    assert.equal(isOnline(undef, buildings[0]), true);
    assert.equal(isOnline(nul, buildings[0]), true);
    // Both are falsy -> computeRoadGates skips the gates identically, so
    // sharing one sentinel key is exact, not a collision.
    assert.equal(isOnline(undef, buildings[1]), isOnline(nul, buildings[1]));
  });
});

// ════════════════════════════════════════════════════════════════════════
// A4 — GHOST BUILDINGS. A placement candidate / clipboard ghost is NOT in
// s.buildings, so it falls through the Map to computeRoadGates. It must (a)
// answer correctly and (b) never be written into the shared fold map.
// ════════════════════════════════════════════════════════════════════════
describe('BUG-674 A4: buildings outside s.buildings', () => {
  test('a ghost answers correctly and does not poison the cached fold', () => {
    const buildings = [
      { id: 1, spec: 'res_hut', x: 5, y: 5, builtTick: 0 },
      { id: 2, spec: 'road', x: 6, y: 5, builtTick: 0 },
    ];
    const s = { ...initialState(), tick: 5000, buildings, roadConnectivity: { connectedRoadTiles: ['6,5'] } };
    isOnline(s, buildings[0]); // prime
    __resetRoadGateFoldCountForTest();

    const ghostConnected = { id: 999, spec: 'res_hut', x: 7, y: 5, builtTick: 0 }; // adjacent to the road
    const ghostStranded = { id: 998, spec: 'res_hut', x: 60, y: 60, builtTick: 0 };
    assert.equal(isOnline(s, ghostConnected), true, 'ghost beside a connected road -> online');
    assert.equal(isOnline(s, ghostStranded), false, 'ghost in the wilderness -> offline');
    assert.equal(foldCount(), 0, 'a ghost query must not trigger a re-fold');

    // The real members are unaffected.
    assert.equal(isOnline(s, buildings[0]), true);
    assert.equal(isOnline(s, buildings[1]), true);
    assert.equal(foldCount(), 0);
  });

  test('a ghost that is still under construction is still gated by G1', () => {
    const buildings = [{ id: 2, spec: 'road', x: 6, y: 5, builtTick: 0 }];
    const s = { ...initialState(), tick: 50, buildings, roadConnectivity: { connectedRoadTiles: ['6,5'] } };
    const ghost = { id: 999, spec: 'res_hut', x: 7, y: 5, builtTick: 49 };
    const need = constructionTicks(SPECS['res_hut']);
    assert.equal(isOnline({ ...s, tick: 49 + need - 1 }, ghost), false, 'ghost mid-construction -> offline');
    assert.equal(isOnline({ ...s, tick: 49 + need }, ghost), true, 'ghost complete -> online');
  });
});

// ════════════════════════════════════════════════════════════════════════
// A5 — THE ORACLE SWEEP. The generic staleness detector: drive real action
// scripts (including the BUG-660 batch path via resolveDemandAll, which
// threads a MUTATED-IN-PLACE board through every place, and auto-connector
// road laying) and after EVERY action compare every cached answer against a
// cache-free recomputation. Catches any in-place mutation that poisons the
// identity-keyed cache without needing to know which write site did it.
// ════════════════════════════════════════════════════════════════════════
describe('BUG-674 A5: cached-vs-uncached oracle across real action scripts', () => {
  const scripts = {
    'place + autoConnect + ticks': (() => {
      const a = [{ type: 'setFunds', amount: 50_000_000 }];
      for (let x = 0; x < 10; x++) a.push({ type: 'place', spec: 'road', x, y: 20 });
      a.push({ type: 'place', spec: 'res_hut', x: 3, y: 21 });
      a.push({ type: 'place', spec: 'res_hut', x: 45, y: 45 }); // far away -> autoConnect lays a connector
      a.push({ type: 'tick' }, { type: 'tick' }, { type: 'tick' });
      a.push({ type: 'bulldoze', x: 3, y: 20 });
      a.push({ type: 'tick' }, { type: 'tick' });
      return a;
    })(),
    'batch placement path (resolveDemandAll)': (() => {
      const a = [{ type: 'setFunds', amount: 500_000_000 }];
      for (let x = 0; x < 20; x++) a.push({ type: 'place', spec: 'road', x, y: 30 });
      for (let i = 0; i < 12; i++) a.push({ type: 'tick' });
      a.push({ type: 'resolveDemandAll' });
      for (let i = 0; i < 8; i++) a.push({ type: 'tick' });
      a.push({ type: 'resolveDemandAll' });
      for (let i = 0; i < 8; i++) a.push({ type: 'tick' });
      return a;
    })(),
  };

  for (const [name, actions] of Object.entries(scripts)) {
    test(`oracle: ${name} — no cached answer ever disagrees with a cache-free recomputation`, () => {
      let s = initialState();
      let i = 0;
      for (const action of actions) {
        let next;
        try {
          next = reducer(s, action);
        } catch {
          // An action this build does not support (e.g. setFunds) — apply the
          // funds directly so the script still exercises what it is meant to.
          next = action.type === 'setFunds' ? { ...s, funds: action.amount } : s;
        }
        if (next === s && action.type === 'setFunds') next = { ...s, funds: action.amount };
        s = next;
        assertNoStaleness(s, `${name} after action ${i} (${action.type})`);
        i++;
      }
      assert.ok(s.buildings.length > 5, `precondition: the script actually built a city (got ${s.buildings.length})`);
    });
  }
});

// ════════════════════════════════════════════════════════════════════════
// A5b — THE SHAPE THE ORACLE ALONE CANNOT SEE. Under the real engine,
// computeRoadConnectivity is ITSELF memoised on s.buildings, so a new
// buildings array almost always brings a new roadConnectivity object too —
// which means the INNER key alone would mask a broken OUTER key, and the
// generic oracle sweep above cannot distinguish the two. The exception is
// engine.ts's non-road placement path ('place' with
// roadTopologyMayHaveChanged false → `{...attempt, roadConnectivity:
// cur.roadConnectivity}`), which deliberately CARRIES THE OLD CONNECTIVITY
// OBJECT ONTO A NEW BUILDINGS ARRAY. That is a real production shape where
// only the buildings half of the key can save you.
// ════════════════════════════════════════════════════════════════════════
describe('BUG-674 A5b: buildings changed while roadConnectivity object identity is CARRIED OVER', () => {
  test('a non-road placement reuses the roadConnectivity object — the new building must still be gated correctly', () => {
    let s = { ...initialState(), funds: 50_000_000 };
    for (let x = 0; x < 10; x++) s = reducer(s, { type: 'place', spec: 'road', x, y: 35 });
    s = { ...s, tick: s.tick + 5000 };
    const connBefore = s.roadConnectivity;
    assert.ok(connBefore, 'precondition: connectivity exists');

    // A non-road placement: the reducer's roadTopologyMayHaveChanged guard
    // carries the SAME roadConnectivity object onto a NEW buildings array.
    const after = reducer(s, { type: 'place', spec: 'res_hut', x: 4, y: 36 });
    assert.notEqual(after.buildings, s.buildings, 'sanity: buildings identity changed');
    if (after.roadConnectivity === connBefore) {
      // The shape we are hunting actually occurred — now prove the answer is
      // computed against the NEW buildings, not served from the old fold.
      const aged = { ...after, tick: after.tick + 5000 };
      const hut = aged.buildings.find((b) => b.spec === 'res_hut' && b.x === 4 && b.y === 36);
      assert.ok(hut, 'precondition: the hut is in the new array');
      assert.equal(
        isOnline(aged, hut),
        true,
        'STALE OUTER KEY: a new building on a carried-over roadConnectivity object was not folded'
      );
      assertNoStaleness(aged, 'carried-over connectivity');
    }

    // Now the sharp version: an EXISTING building whose answer CHANGES
    // because of a buildings-only edit, with roadConnectivity's object
    // identity deliberately carried over. A new building can never expose a
    // broken outer key (it is absent from the map and falls through to a
    // direct computation) — only an existing one whose cached answer is now
    // WRONG can. Removing the road tile beside the hut is exactly that.
    // A connectivity object with the same CONTENT but a fresh identity, so
    // the road-gate map for it starts cold at `withHut` — otherwise the hut
    // would be absent from an already-warm fold and would harmlessly fall
    // through to a direct computation, masking the very thing under test.
    const conn2 = { ...connBefore, connectedRoadTiles: [...connBefore.connectedRoadTiles] };
    const hut2 = { id: 10_000_001, spec: 'res_hut', x: 4, y: 36, builtTick: 0 };
    const withHut = { ...s, buildings: [...s.buildings, hut2], tick: s.tick + 5000, roadConnectivity: conn2 };
    assert.equal(isOnline(withHut, hut2), true, 'precondition: the hut is road-connected and online (this warms the fold)');

    // Same roadConnectivity OBJECT, buildings array with every ordinary road
    // stripped out. Road-adjacency (roadTileSetOf, keyed on buildings) must
    // now fail — so the ONLY thing that can invalidate the fold is the
    // buildings half of the key.
    const noRoads = {
      ...withHut,
      buildings: withHut.buildings.filter((b) => !(SPECS[b.spec] && SPECS[b.spec].kind === 'road')),
      roadConnectivity: conn2,
    };
    assert.equal(noRoads.roadConnectivity, conn2, 'sanity: connectivity object identity carried over unchanged');
    assert.notEqual(noRoads.buildings, withHut.buildings, 'sanity: buildings identity differs');
    const hutInNoRoads = noRoads.buildings.find((b) => b.id === hut2.id);
    assert.equal(
      isOnline(noRoads, hutInNoRoads),
      false,
      'STALE OUTER KEY: every road beside the hut was removed from buildings, but the cached road-gate map ' +
        'was reused because roadConnectivity kept its object identity'
    );
    assertNoStaleness(noRoads, 'carried-over connectivity, roads stripped');
  });
});

// ════════════════════════════════════════════════════════════════════════
// A6 — REPLAY DETERMINISM. The caches must be completely invisible to
// results: a live run and a genesis replay of the same journal must be
// byte-identical, in BOTH cache-warmth orders (replay-first and live-first),
// because a warm cache from the live run is exactly what could leak into a
// replay if the keying were wrong.
// ════════════════════════════════════════════════════════════════════════
describe('BUG-674 A6: the road-gate cache is invisible to replay', () => {
  // NOTE: the seed state must be UNMODIFIED initialState() — any funds/tick
  // tweak applied outside the journal is invisible to replayFromGenesis and
  // would diverge the two runs for a reason that has nothing to do with the
  // memo. Everything below is affordable out of the starting treasury.
  function script() {
    const a = [];
    for (let x = 0; x < 15; x++) a.push({ type: 'place', spec: 'road', x, y: 25 });
    a.push({ type: 'place', spec: 'res_hut', x: 4, y: 26 });
    for (let i = 0; i < 10; i++) a.push({ type: 'tick' });
    a.push({ type: 'bulldoze', x: 4, y: 25 });
    for (let i = 0; i < 6; i++) a.push({ type: 'tick' });
    return a;
  }

  test('live vs replayFromGenesis byte-identical with the memo warm', () => {
    const { journal, liveState } = driveAndRecord(script());
    assert.ok(liveState.buildings.some((b) => b.spec === 'res_hut'), 'precondition: the script actually built something');
    // Warm the caches hard on the live state before replaying.
    for (const b of liveState.buildings) isOnline(liveState, b);
    const replayed = replayFromGenesis(journal);
    for (const b of replayed.buildings) isOnline(replayed, b);
    assert.equal(
      stableStringify({ ...replayed, roadConnectivity: null }),
      stableStringify({ ...liveState, roadConnectivity: null }),
      'live vs replay divergence with the road-gate memo warm'
    );
    // And the ONLINE SET itself must match building-for-building.
    const liveOnline = liveState.buildings.map((b) => `${b.id}:${isOnline(liveState, b)}`).sort();
    const replayOnline = replayed.buildings.map((b) => `${b.id}:${isOnline(replayed, b)}`).sort();
    assert.deepEqual(replayOnline, liveOnline, 'the online SET diverged between live and replay');
  });

  test('replaying the same journal twice yields identical online sets', () => {
    const { journal } = driveAndRecord(script());
    const a = replayFromGenesis(journal);
    const b = replayFromGenesis(journal);
    const onlineOf = (s) => s.buildings.map((x) => `${x.id}:${isOnline(s, x)}`).sort();
    assert.deepEqual(onlineOf(b), onlineOf(a), 'two replays of one journal produced different online sets');
  });
});

// ════════════════════════════════════════════════════════════════════════
// A7 — CACHE-FIRST vs COMPUTE-FIRST EQUIVALENCE. The fold populates the map
// for EVERY building at once; a caller that queries a single building first
// must get the same answer as one that queries the whole city first. Proves
// the fold and the fall-through path (computeRoadGates) agree exactly — the
// semantics claim in the doc comment.
// ════════════════════════════════════════════════════════════════════════
describe('BUG-674 A7: fold path and fall-through path agree exactly', () => {
  test('every building: folded answer === direct answer, across a real city', () => {
    let s = { ...initialState(), funds: 500_000_000 };
    for (let x = 0; x < 25; x++) s = reducer(s, { type: 'place', spec: 'road', x, y: 15 });
    for (let i = 0; i < 6; i++) s = reducer(s, { type: 'place', spec: 'res_hut', x: 2 + i * 3, y: 16 });
    s = reducer(s, { type: 'place', spec: 'wat_clean', x: 70, y: 70 });
    for (let i = 0; i < 12; i++) s = reducer(s, { type: 'tick' });

    // Folded answers (one shared map).
    const folded = s.buildings.map((b) => isOnline(s, b));
    // Direct answers: clone each building object so it is NOT a key in the
    // fold map, forcing the fall-through computeRoadGates path — same state.
    const direct = s.buildings.map((b) => isOnline(s, { ...b }));
    assert.deepEqual(direct, folded, 'the fall-through path disagrees with the folded map for at least one building');
    assert.ok(folded.some((v) => v) && s.buildings.length > 10, 'precondition: a real mixed city');
  });
});
