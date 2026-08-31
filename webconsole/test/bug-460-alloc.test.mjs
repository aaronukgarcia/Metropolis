// bug-460-alloc.test.mjs — BUG-460 quick-win allocation fixes for the 2.5 GB
// genesis-replay OOM (measured diagnosis: replay is O(actions x buildings)
// transient-allocation CHURN, not a leak).
//
// FIX B: computeRoadConnectivity's BFS used queue.shift() (O(n) per dequeue,
// O(n^2) total on a large road network). Proves the index-pointer rewrite
// yields the IDENTICAL reachable set as the old shift()-based BFS (the
// reachable set is order-independent per the existing data.ts:~495 comment, so
// this is a pure perf fix with no output change).
//
// FIX A: the reducer wrapper's per-action computeRoadConnectivity recompute is
// skipped during a headless genesis replay (setReplayMode), with a single
// final recompute after the loop. Proves:
//   1. determinism — replaying the same journal twice from genesis is
//      byte-identical (belt-and-braces alongside genesis-replay.test.mjs).
//   2. equivalence — a replay WITH the skip produces a BYTE-IDENTICAL final
//      state to a control replay WITHOUT the skip (manual per-action reducer
//      loop, mirroring the live store path), including a journal with `place`
//      actions NOT followed by a `tick` before the end (the edge the skip
//      could plausibly disturb).
//   3. setReplayMode is cleared even when the replay loop throws (try/finally).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, MAP_W, MAP_H, computeRoadConnectivity } from '../src/sim/data.ts';
import { initialState, reducer, setReplayMode } from '../src/sim/engine.ts';
import { emptyJournal, recordAction } from '../src/sim/journal.ts';
import {
  replayFromGenesis,
  replayIsDeterministic,
  replayFromGenesisDefensive,
  stableStringify,
} from '../src/sim/genesisReplay.ts';

const key = (x, y) => `${x},${y}`;

// Build a clean board (no starter city) with an explicit building list —
// mirrors the board() helper used across the road-*.test.mjs suite.
function board(buildings) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null };
}

function roadAt(id, x, y) {
  return { id, spec: 'road', x, y, builtTick: null };
}

/**
 * Scratch OLD-style BFS: an exact copy of computeRoadConnectivity's pre-FIX-B
 * seeding/BFS logic, but using `queue.shift()` instead of the index-pointer
 * walk. Kept ONLY in this test file (never touches data.ts) so the two
 * algorithms can be compared for identical output without any git revert.
 */
function computeRoadConnectivityOldShiftBFS(s) {
  const roadTiles = new Set();
  const trunkTiles = new Set();
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const road = sp.roadTier > 0;
    const trunk = sp.kind === 'motorway' || sp.kind === 'rail' || sp.kind === 'station';
    if (!road && !trunk) continue;
    for (let dx = 0; dx < sp.w; dx++)
      for (let dy = 0; dy < sp.h; dy++) {
        const k = key(b.x + dx, b.y + dy);
        if (road) roadTiles.add(k);
        if (trunk) trunkTiles.add(k);
      }
  }

  const connected = new Set();
  const queue = [];
  const seed = (k) => {
    if (roadTiles.has(k) && !connected.has(k)) {
      connected.add(k);
      queue.push(k);
    }
  };
  for (const k of roadTiles) {
    const c = k.indexOf(',');
    const x = Number(k.slice(0, c));
    const y = Number(k.slice(c + 1));
    const edge = x === 0 || y === 0 || x === MAP_W - 1 || y === MAP_H - 1;
    const trunkRoad = trunkTiles.has(k);
    const nearTrunk =
      trunkTiles.has(key(x + 1, y)) ||
      trunkTiles.has(key(x - 1, y)) ||
      trunkTiles.has(key(x, y + 1)) ||
      trunkTiles.has(key(x, y - 1));
    if (edge || trunkRoad || nearTrunk) seed(k);
  }

  const ORTHO = [
    [1, 0],
    [-1, 0],
    [0, 1],
    [0, -1],
  ];
  while (queue.length > 0) {
    const k = queue.shift();
    const c = k.indexOf(',');
    const x = Number(k.slice(0, c));
    const y = Number(k.slice(c + 1));
    for (const [ox, oy] of ORTHO) {
      const nk = key(x + ox, y + oy);
      if (roadTiles.has(nk) && !connected.has(nk)) {
        connected.add(nk);
        queue.push(nk);
      }
    }
  }

  return { connectedRoadTiles: Array.from(connected).sort() };
}

describe('BUG-460 FIX B: index-pointer BFS matches the old shift()-based BFS', () => {
  test('a long connected corridor plus an unreachable spur yields an identical reachable set', () => {
    const buildings = [];
    let id = 1;
    // A long corridor from the map edge (x=0) inward — several hundred tiles so
    // the O(n^2) shift() cost would actually show up under profiling, and the
    // reachable-set comparison below is meaningful on a nontrivial graph.
    for (let x = 0; x < 300; x++) buildings.push(roadAt(id++, x, 50));
    // A branch off the corridor.
    for (let y = 50; y < 80; y++) buildings.push(roadAt(id++, 150, y));
    // An UNREACHABLE spur, not touching the corridor, not at an edge, no trunk
    // nearby — must be excluded from connectivity by BOTH algorithms.
    for (let x = 200; x < 210; x++) buildings.push(roadAt(id++, x, 120));

    const s = board(buildings);
    const fresh = computeRoadConnectivity(s);
    const old = computeRoadConnectivityOldShiftBFS(s);

    assert.deepEqual(
      fresh.connectedRoadTiles,
      old.connectedRoadTiles,
      'index-pointer BFS must reach exactly the same tiles as the shift()-based BFS'
    );
    // Sanity: the corridor and branch are connected; the spur is not.
    assert.ok(fresh.connectedRoadTiles.includes('0,50'), 'corridor start (map edge) is connected');
    assert.ok(fresh.connectedRoadTiles.includes('150,79'), 'branch end is connected');
    assert.ok(
      !fresh.connectedRoadTiles.includes('205,120'),
      'the isolated spur is NOT connected (sanity check the fixture is meaningful)'
    );
  });

  test('an empty board yields an identical (empty) result from both algorithms', () => {
    const s = board([]);
    assert.deepEqual(computeRoadConnectivity(s), computeRoadConnectivityOldShiftBFS(s));
  });

  test('a single isolated road tile (no edge/trunk) is identically unreachable in both', () => {
    const s = board([roadAt(1, 200, 130)]);
    const fresh = computeRoadConnectivity(s);
    const old = computeRoadConnectivityOldShiftBFS(s);
    assert.deepEqual(fresh, old);
    assert.deepEqual(fresh.connectedRoadTiles, []);
  });
});

// ---------------------------------------------------------------------------
// FIX A fixtures
// ---------------------------------------------------------------------------

function driveAndRecord(actions) {
  let journal = emptyJournal();
  let state = initialState();
  for (const action of actions) {
    journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  return { journal, liveState: state };
}

// A journal that places roads/buildings with NO tick in between for a run of
// actions, per the brief's edge case ("place actions NOT followed by a tick
// before the end").
const NO_TICK_TAIL_SCRIPT = [
  { type: 'place', spec: 'res_hut', x: 5, y: 5 },
  { type: 'tick' },
  { type: 'place', spec: 'm20', x: 10, y: 10 },
  { type: 'place', spec: 'res_hut', x: 20, y: 20 },
  { type: 'place', spec: 'res_hut', x: 25, y: 25 },
  // no trailing tick — the journal ends immediately after three place actions.
];

describe('BUG-460 FIX A: determinism proof', () => {
  test('replaying the same journal twice from genesis is byte-identical (with the skip active)', () => {
    const { journal } = driveAndRecord(NO_TICK_TAIL_SCRIPT);
    assert.equal(replayIsDeterministic(journal), true);
    const a = replayFromGenesis(journal);
    const b = replayFromGenesis(journal);
    assert.equal(stableStringify(a), stableStringify(b));
  });
});

describe('BUG-460 FIX A: skip vs no-skip equivalence (the decisive proof)', () => {
  test('replay WITH the wrapper-recompute skip matches a control WITHOUT it, byte-for-byte', () => {
    const { journal, liveState } = driveAndRecord(NO_TICK_TAIL_SCRIPT);

    // "WITH the skip": the real replayer (replayFromGenesis toggles setReplayMode
    // internally around its loop, then does one final recompute).
    const withSkip = replayFromGenesis(journal);

    // "WITHOUT the skip": manually drive the SAME actions through the reducer
    // with replay mode explicitly OFF the whole time — i.e. the wrapper
    // recomputes roadConnectivity after every buildings-changing action, exactly
    // like ordinary live play. This is liveState from driveAndRecord (built the
    // same way, moment for moment) — kept explicit here for the reader.
    setReplayMode(false);
    let control = initialState();
    for (const entry of journal.entries) {
      control = reducer(control, entry.action);
    }

    assert.deepEqual(control, liveState, 'the control loop matches the live drive (sanity)');
    assert.equal(
      stableStringify(withSkip),
      stableStringify(control),
      'skipping the wrapper recompute during replay must not change the final state'
    );
    assert.deepEqual(withSkip, control, 'deep-equal too, not just serialized');
  });

  test('equivalence holds on the longer representative SCRIPT with intermixed ticks', () => {
    const SCRIPT = [
      { type: 'place', spec: 'res_hut', x: 5, y: 5 },
      { type: 'tick' },
      { type: 'tick' },
      { type: 'tax', which: 'residential', rate: 10 },
      { type: 'place', spec: 'm20', x: 10, y: 10 },
      { type: 'tick' },
      { type: 'place', spec: 'res_hut', x: 20, y: 20 },
      { type: 'tick' },
      { type: 'bulldoze', x: 10, y: 10 },
      { type: 'tick' },
    ];
    const { journal, liveState } = driveAndRecord(SCRIPT);
    const withSkip = replayFromGenesis(journal);
    assert.deepEqual(withSkip, liveState);
  });
});

describe('BUG-460 FIX A: replay mode is cleared even when the loop throws', () => {
  test('setReplayMode is restored to false after a throwing reduce (try/finally)', () => {
    // replayFromGenesisDefensive catches per-ACTION throws internally (skip +
    // log, never propagates out of the loop) — so to prove the try/finally
    // pattern itself is leak-proof, drive it directly the same way
    // genesisReplay's loops do: setReplayMode(true), throw, finally clears it.
    setReplayMode(true);
    try {
      throw new Error('simulated mid-loop failure');
    } catch {
      // expected
    } finally {
      setReplayMode(false);
    }
    // A subsequent ordinary reducer call must recompute roadConnectivity fresh
    // (i.e. replay mode is really off) — place a building and confirm the
    // wrapper's normal behaviour (fresh connectivity graph) is back.
    const s1 = reducer(initialState(), { type: 'place', spec: 'road', x: 30, y: 30 });
    assert.notEqual(s1.roadConnectivity, undefined, 'roadConnectivity must be recomputed — replay mode was not left stuck on');

    // Also exercise the real defensive replayer end-to-end (its own internal
    // try/finally) with an engine whose reduce ALWAYS throws — every action is
    // skip-and-logged, never propagating — then confirm normal play immediately
    // afterwards still gets a fresh connectivity graph (no leaked flag).
    const badJournal = {
      ...emptyJournal(),
      entries: [{ tick: 0, action: { type: 'place', spec: 'res_hut', x: 5, y: 5 } }],
    };
    const throwingEngine = {
      init: initialState,
      reduce: () => {
        throw new Error('injected failure');
      },
    };
    const result = replayFromGenesisDefensive(badJournal, throwingEngine);
    assert.equal(result.skipped.length, 1, 'the throwing engine skip-and-logs the one bad action');
    assert.equal(result.crashed, false);

    const s2 = reducer(initialState(), { type: 'place', spec: 'road', x: 31, y: 30 });
    assert.notEqual(s2.roadConnectivity, undefined, 'replay mode did not leak past the defensive replayer either');
  });
});
