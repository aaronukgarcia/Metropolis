// attack-bug617-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND against the
// BUG-617 chunked boot-restore estate (GR#23: the attacker is not the author).
//
// ROUND HISTORY
//   r1 REJECT — A2 caught a real byte-identity divergence: replayTailChunked
//     wrapped its loop in setReplayMode(true) (mirroring genesisReplay's
//     chunked genesis replay) on BUG-460 FIX A's premise that "nothing reads
//     s.roadConnectivity BETWEEN actions during a replay". BUG-606 (landed the
//     same day, 64105b7) falsified that premise by adding the resolveDemand /
//     resolveDemandAll reducer cases, which derive their build plan through
//     demandFixPlan -> serviceCoverageOf -> isOnline -> the G2/G3 road gates,
//     reading state.roadConnectivity DIRECTLY inside the reducer between
//     actions. A [place road][resolveDemand gp] tail — the ordinary "finish
//     the road, then click Fix" player sequence — made the chunked replay
//     build a clinic the synchronous restoreFromSavepoint loop never built.
//   r2 — setReplayMode was DROPPED from replayTailChunked entirely, so the
//     chunked path now runs the IDENTICAL reducer call restoreFromSavepoint's
//     own loop runs. Byte-identity became tautological rather than argued.
//
// These tests are kept as REGRESSIONS. A2 is the r1 repro (now green); A2b is
// re-pointed (its r1 form was a diagnostic control that compared against the
// replay-mode route which no longer exists) to guard the FIX itself: it fails
// the day anyone reintroduces a connectivity-deferring shortcut into the tail
// path. A5 is r2's new adversarial mixed tail.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, setReplayMode } from '../src/sim/engine.ts';
import { computeRoadConnectivity, MAP_H, isOnline, serviceCoverageOf } from '../src/sim/data.ts';
import { stableStringify } from '../src/sim/genesisReplay.ts';
import { replayTailChunked } from '../src/sim/replay.ts';

/**
 * A city whose ONLY health provider (a clinic) sits beside an isolated road
 * stub one tile short of the map edge — road-ADJACENT but not road-CONNECTED,
 * so the FEAT-1972079891 G3 gate has it offline and GP coverage reads 0.
 * `builtTick` is set (and `tick` pushed well past it) so the gates actually
 * apply — a building with a null `builtTick` is unconditionally online and
 * would mask the whole mechanism.
 */
function craftDisconnectedClinicCity(col = 200) {
  const base = initialState();
  const nextId = base.nextId;
  const buildings = [
    ...base.buildings,
    { id: nextId, spec: 'road', x: col, y: MAP_H - 2 },
    { id: nextId + 1, spec: 'hea_clinic', x: col, y: MAP_H - 3, builtTick: 0 },
  ];
  const s = {
    ...base,
    buildings,
    nextId: nextId + 2,
    tick: 500,
    population: 10_000,
    funds: 900_000_000_000,
    unlockedAll: true,
  };
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

/** The player's real, journalled two-action sequence: finish the road to the
 * map edge (which brings the clinic online), then click Fix (N) / Fix All —
 * both between two ticks, exactly as a paused or slow-speed session records. */
const CONNECT_THEN_FIX = (col = 200) => [
  { type: 'place', spec: 'road', x: col, y: MAP_H - 1 },
  { type: 'resolveDemand', serviceKey: 'gp' },
];

function unchunked(state, actions) {
  let s = state;
  for (const a of actions) s = reducer(s, a);
  return s;
}

function chunked(state, actions) {
  const gen = replayTailChunked(state, actions.map((a) => ({ tick: state.tick, action: a })));
  let n;
  do {
    n = gen.next();
  } while (!n.done);
  return n.value.state;
}

describe('ATTACK BUG-617: chunked tail replay vs the synchronous restore loop', () => {
  test('A0 (setup sanity): the crafted clinic really is gated off by road connectivity', () => {
    const s = craftDisconnectedClinicCity();
    const clinic = s.buildings[s.buildings.length - 1];
    assert.equal(isOnline(s, clinic), false, 'clinic must start OFFLINE (road stub not connected)');
    assert.equal(serviceCoverageOf(s).find((c) => c.id === 'gp').cap, 0, 'offline clinic must contribute no GP capacity');

    const connected = reducer(s, CONNECT_THEN_FIX()[0]);
    const clinic2 = connected.buildings.find((b) => b.id === clinic.id);
    assert.equal(isOnline(connected, clinic2), true, 'clinic must be ONLINE once the road reaches the edge');
  });

  test('A1 (control): chunk boundaries are byte-identity-safe on a connectivity-free tail', () => {
    const start = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    const actions = [];
    for (let i = 0; i < 200; i++) {
      const x = 2 + (i % 100) * 3;
      const y = 2 + Math.floor(i / 100) * 3;
      actions.push({ type: 'place', spec: 'road', x, y: y + 1 });
      actions.push({ type: 'place', spec: 'res_hut', x, y });
      if (i % 20 === 0) actions.push({ type: 'tick' });
    }
    assert.equal(
      stableStringify(chunked(start, actions)),
      stableStringify(unchunked(start, actions)),
      'a connectivity-free tail must replay byte-identically through the chunked generator'
    );
  });

  test('A2 (r1 REJECT repro): a road-then-Fix tail replays byte-identically through the chunked generator', () => {
    const s = craftDisconnectedClinicCity();
    const actions = CONNECT_THEN_FIX();

    const sync = unchunked(s, actions);
    const chunk = chunked(s, actions);

    assert.equal(
      stableStringify(chunk),
      stableStringify(sync),
      `chunked tail replay must be BYTE-IDENTICAL to restoreFromSavepoint's synchronous loop.\n` +
        `  synchronous : ${sync.buildings.length} buildings, funds ${sync.funds}\n` +
        `  chunked     : ${chunk.buildings.length} buildings, funds ${chunk.funds}\n` +
        `  (r1: the chunked path replayed with setReplayMode(true), so 'resolveDemand' saw a\n` +
        `  STALE roadConnectivity, believed the now-connected clinic was still offline, and\n` +
        `  built a clinic the real session never built.)`
    );
  });

  test('A2b (fix guard): the connectivity-deferring shortcut must NOT come back to the tail path', () => {
    // r1's A2b was a diagnostic control proving setReplayMode caused the
    // divergence; that route no longer exists, so it is re-pointed here into
    // a guard on the FIX. This pins the two facts that make A2 meaningful:
    //   (a) replay mode genuinely changes the outcome of this tail, so A2 is
    //       not passing merely because the tail is insensitive; and
    //   (b) replayTailChunked does NOT take that route.
    const s = craftDisconnectedClinicCity(205);
    const actions = CONNECT_THEN_FIX(205);

    setReplayMode(true);
    let deferred = s;
    try {
      for (const a of actions) deferred = reducer(deferred, a);
    } finally {
      setReplayMode(false);
    }
    deferred = { ...deferred, roadConnectivity: computeRoadConnectivity(deferred) };

    assert.notEqual(
      stableStringify(deferred),
      stableStringify(unchunked(s, actions)),
      'sanity: deferring connectivity DOES change this tail — if this ever passes, A2 has lost its teeth'
    );
    assert.equal(
      stableStringify(chunked(s, actions)),
      stableStringify(unchunked(s, actions)),
      'replayTailChunked must run the plain reducer path, never a connectivity-deferring shortcut'
    );
  });

  test('A3 (r1 REJECT repro): an abandoned chunked-tail generator leaves no module-scoped replay state behind', () => {
    const s = craftDisconnectedClinicCity(210);
    const actions = [];
    for (let i = 0; i < 400; i++) actions.push({ type: 'place', spec: 'road', x: 5 + (i % 200), y: 5 });
    const gen = replayTailChunked(s, actions.map((a) => ({ tick: s.tick, action: a })));
    gen.next(); // one chunk, then abandon exactly as the effect's cleanup did in r1

    try {
      const before = craftDisconnectedClinicCity(220);
      const after = reducer(before, { type: 'place', spec: 'road', x: 220, y: MAP_H - 1 });
      const clinic = after.buildings.find((b) => b.spec === 'hea_clinic');
      assert.equal(
        isOnline(after, clinic),
        true,
        'after an abandoned chunked replay, ordinary play must still refresh roadConnectivity'
      );
    } finally {
      setReplayMode(false); // do not contaminate the rest of the process
    }
  });

  test('A4: a corrupt action mid-tail propagates out of the strict generator (does not silently skip)', () => {
    const s = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    const actions = [
      { type: 'place', spec: 'road', x: 5, y: 5 },
      null, // corruption: a journal entry whose action failed to deserialize
      { type: 'place', spec: 'road', x: 6, y: 5 },
    ];
    const gen = replayTailChunked(s, actions.map((a) => ({ tick: s.tick, action: a })));
    assert.throws(
      () => {
        let n;
        do {
          n = gen.next();
        } while (!n.done);
      },
      /.*/,
      'the strict tail generator must fail loudly on a corrupt action, never skip it'
    );
    setReplayMode(false);
  });

  test('A5 (r2): an adversarial mixed tail — resolveDemandAll + policy toggles + places, straddling chunk boundaries', () => {
    // Every action class the real journal carries, deliberately arranged so
    // the connectivity-sensitive actions land at DIFFERENT offsets relative to
    // the 50-action chunk ceiling (before a boundary, on it, after it), and so
    // several of them run while the road graph is mid-change.
    const s = craftDisconnectedClinicCity(230);
    const actions = [];

    // 47 cheap places — the next connectivity-sensitive action therefore lands
    // at index 48/49/50, i.e. straddling the TAIL_ACTIONS_PER_CHUNK boundary.
    for (let i = 0; i < 47; i++) actions.push({ type: 'place', spec: 'road', x: 10 + i, y: 40 });
    actions.push({ type: 'place', spec: 'road', x: 230, y: MAP_H - 1 }); // brings the clinic online
    actions.push({ type: 'resolveDemandAll' }); // reads connectivity, on the boundary
    actions.push({ type: 'policy', key: 'water_meter', value: true });
    actions.push({ type: 'tick' });
    actions.push({ type: 'resolveDemand', serviceKey: 'gp' });

    // A second wave that changes the road graph again, then re-derives.
    for (let i = 0; i < 60; i++) actions.push({ type: 'place', spec: 'road', x: 10 + i, y: 41 });
    actions.push({ type: 'bulldoze', x: 230, y: MAP_H - 1 }); // takes the clinic back OFFLINE
    actions.push({ type: 'resolveDemandAll' });
    actions.push({ type: 'tick' });
    actions.push({ type: 'policy', key: 'water_meter', value: false });
    actions.push({ type: 'resolveDemandAll' });
    for (let i = 0; i < 55; i++) actions.push({ type: 'place', spec: 'res_hut', x: 10 + i, y: 43 });
    actions.push({ type: 'tick' });
    actions.push({ type: 'resolveDemandAll' });

    const sync = unchunked(s, actions);
    const chunk = chunked(s, actions);
    assert.equal(
      stableStringify(chunk),
      stableStringify(sync),
      `mixed adversarial tail (${actions.length} actions) must replay byte-identically.\n` +
        `  synchronous : ${sync.buildings.length} buildings, funds ${sync.funds}\n` +
        `  chunked     : ${chunk.buildings.length} buildings, funds ${chunk.funds}`
    );
    // Teeth check: the tail must actually have DONE something, or byte-identity
    // is vacuous.
    assert.notEqual(stableStringify(sync), stableStringify(s), 'the adversarial tail must change the city');
  });
});
