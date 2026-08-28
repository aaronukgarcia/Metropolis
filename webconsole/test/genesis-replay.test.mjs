// genesis-replay.test.mjs — FEAT-1972079897 inc1: GENESIS replay core.
//
// Design brief: docs/planning/hard-reset-replay-brief.md.
//
// Proves the headless genesis replayer (src/sim/genesisReplay.ts):
//   1. Determinism — replaying the same journal from genesis twice is byte-identical.
//   2. Fidelity   — genesis replay reconstructs the EXACT city a live action
//                   sequence produced (deep-equal final SimState).
//   3. Empty journal → initialState() (no crash).
//   4. Placements + ticks reconstruct the buildings and the advanced tick/population.
//   5. RED proof — a mutated (broken) genesis replay makes the fidelity check FAIL,
//                  proving the fidelity assertion can actually go red.
//
// The test Journal is built by MIRRORING the store's recording (store.tsx
// recordAndDispatch): record the action against the CURRENT state.tick, THEN
// dispatch it through the reducer — i.e. recordAction(j, state.tick, action)
// before state = reducer(state, action).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { emptyJournal, recordAction } from '../src/sim/journal.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import {
  replayFromGenesis,
  replayIsDeterministic,
  stableStringify,
} from '../src/sim/genesisReplay.ts';

/**
 * Build a Journal exactly as the store does: for each action, record it against
 * the current state.tick, then advance the live state through the pure reducer.
 * Returns { journal, liveState } so tests can compare replay against the live path.
 */
function driveAndRecord(actions) {
  let journal = emptyJournal();
  let state = initialState();
  for (const action of actions) {
    journal = recordAction(journal, state.tick, action); // mirror store: record with pre-dispatch tick
    state = reducer(state, action);
  }
  return { journal, liveState: state };
}

// A representative game slice: placements, a tax change, ticks, and a bulldoze.
// (res_hut / m20 / road are level-1 specs used by the existing journal tests.)
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

describe('genesisReplay: determinism (self-test)', () => {
  test('replaying the same journal twice is byte-identical', () => {
    const { journal } = driveAndRecord(SCRIPT);
    assert.equal(replayIsDeterministic(journal), true, 'genesis replay must be deterministic');

    // Belt-and-braces: the two replays are byte-identical under stable JSON.
    const a = replayFromGenesis(journal);
    const b = replayFromGenesis(journal);
    assert.equal(stableStringify(a), stableStringify(b), 'two replays serialize identically');
  });

  test('empty journal is deterministic and yields initialState()', () => {
    const journal = emptyJournal();
    assert.equal(replayIsDeterministic(journal), true);
    assert.deepEqual(replayFromGenesis(journal), initialState(), 'empty journal → pristine genesis');
  });
});

describe('genesisReplay: fidelity (reconstructs the exact city)', () => {
  test('genesis replay deep-equals the live action sequence', () => {
    const { journal, liveState } = driveAndRecord(SCRIPT);
    const replayed = replayFromGenesis(journal);
    assert.deepEqual(replayed, liveState, 'genesis replay reconstructs the exact live city');
  });

  test('input journal is not mutated by replay', () => {
    const { journal } = driveAndRecord(SCRIPT);
    const before = stableStringify(journal);
    replayFromGenesis(journal);
    replayFromGenesis(journal);
    assert.equal(stableStringify(journal), before, 'journal is read-only during replay');
  });
});

describe('genesisReplay: empty journal', () => {
  test('does not crash and equals initialState()', () => {
    assert.doesNotThrow(() => replayFromGenesis(emptyJournal()));
    assert.deepEqual(replayFromGenesis(emptyJournal()), initialState());
  });
});

describe('genesisReplay: placements + ticks reconstruct buildings and tick/population', () => {
  test('buildings placed and tick/population advanced are reconstructed', () => {
    // A pure-tick baseline (no placements) to prove population growth is captured.
    const placeThenTicks = [
      // (30,55) is orthogonally adjacent to the seeded M20 (y56 spans all x), so
      // FEAT-1972079907 auto-connect lays NO connector — exactly one building added.
      { type: 'place', spec: 'res_hut', x: 30, y: 55 },
      ...Array.from({ length: 40 }, () => ({ type: 'tick' })),
    ];
    const { journal, liveState } = driveAndRecord(placeThenTicks);
    const replayed = replayFromGenesis(journal);

    // Fidelity across the whole state.
    assert.deepEqual(replayed, liveState);

    // Explicit, human-legible reconstruction checks (not just the deep-equal):
    const genesisBuildings = initialState().buildings.length;
    assert.equal(
      replayed.buildings.length,
      genesisBuildings + 1,
      'the placed res_hut is present after replay'
    );
    assert.ok(
      replayed.buildings.some((b) => b.spec === 'res_hut' && b.x === 30 && b.y === 55),
      'the exact placed building is reconstructed'
    );
    // 40 tick actions were applied; genesis already sits at tick 1 (initialState
    // runs one advance()), so the replayed tick is 1 + 40.
    assert.equal(replayed.tick, initialState().tick + 40, 'tick advanced by the recorded ticks');
    assert.ok(replayed.population > 0, 'population grew from the housing capacity over 40 ticks');
  });
});

describe('genesisReplay: RED proof (fidelity assertion can fail)', () => {
  test('a broken genesis replay (mutated genesis) does NOT match the live city', () => {
    const { journal, liveState } = driveAndRecord(SCRIPT);

    // Scratch-broken replayer: same loop, but starts from a MUTATED genesis
    // (extra funds). If the fidelity check were vacuous this would still pass;
    // it must fail, proving the real fidelity test is meaningful.
    const brokenReplayFromGenesis = (j) => {
      let state = { ...initialState(), funds: initialState().funds + 1 };
      for (const entry of j.entries) {
        state = reducer(state, entry.action);
      }
      return state;
    };

    const broken = brokenReplayFromGenesis(journal);
    assert.notDeepEqual(broken, liveState, 'RED: a mutated genesis diverges from the live city');

    // And the real replayer still matches (GREEN alongside the RED).
    assert.deepEqual(replayFromGenesis(journal), liveState);
  });
});
