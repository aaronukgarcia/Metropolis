// bug-439-rebuild-empty-journal.test.mjs — BUG-439 / FEAT-2326609714 AC-6.
//
// BUG-439: rebuild-after-load replayed an EMPTY action journal instead of the
// saved city's full history, so a rebuild triggered after Save -> Load produced
// a blank/initial city rather than reproducing the pre-save one.
//
// Root cause (store.tsx, pre-fix):
//   1. buildCurrentSave() (~line 733-742) always wrote `journal: emptyJournal()`
//      into the GameSave, discarding the real action history at SAVE time —
//      regardless of how much play the live journal actually held.
//   2. applyLoadedSave() (~line 786-802) then discarded whatever the load
//      produced by calling `setJournal(emptyJournal())` and flushing an empty
//      journal to the on-disk journal persister — instead of restoring the
//      loaded save's `journal` field into the live/persisted journal.
//
// Either drop point alone is enough to make replayFromGenesisDefensiveChunked
// (which reads `hotJournalRef.current ?? journal`) replay nothing from genesis.
//
// This test exercises the SAME real functions store.tsx composes
// (buildGameSave -> gameSaveText -> parseGameSave -> [journal restore] ->
// replayFromGenesis) rather than reimplementing them, so it fails red against
// the pre-fix behaviour and passes green against the fixed store.tsx logic.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { initialState, reducer } from '../src/sim/engine.ts';
import { emptyJournal, recordAction } from '../src/sim/journal.ts';
import { buildGameSave, parseGameSave, gameSaveText } from '../src/sim/gamesave.ts';
import { replayFromGenesis } from '../src/sim/genesisReplay.ts';

// Mirrors store.tsx's recordAndDispatch: record against the PRE-dispatch tick,
// then advance the live state through the pure reducer (same helper pattern as
// genesis-replay.test.mjs).
function driveAndRecord(actions) {
  let journal = emptyJournal();
  let state = initialState();
  for (const action of actions) {
    journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  return { journal, liveState: state };
}

// A representative session: several placements + ticks, so the journal is
// unambiguously non-empty and the resulting city is unambiguously non-initial.
const SCRIPT = [
  { type: 'place', spec: 'res_hut', x: 5, y: 5 },
  { type: 'tick' },
  { type: 'tick' },
  { type: 'place', spec: 'm20', x: 10, y: 10 },
  { type: 'tick' },
  { type: 'place', spec: 'res_hut', x: 20, y: 20 },
  { type: 'tick' },
  { type: 'tick' },
  { type: 'tick' },
];

describe('BUG-439: rebuild-after-load must replay the FULL saved journal, not an empty one', () => {
  test('RED proof: the pre-fix construction (journal: emptyJournal() at save time) makes rebuild reproduce only initialState()', () => {
    const { journal, liveState } = driveAndRecord(SCRIPT);
    // Sanity: the live city actually diverged from a pristine genesis (otherwise
    // this whole test would be vacuous).
    assert.notDeepEqual(liveState, initialState(), 'the driven script must produce a non-initial city');

    // This is EXACTLY what the pre-fix buildCurrentSave() did: pass emptyJournal()
    // regardless of the real live journal.
    const brokenSave = buildGameSave({
      state: liveState,
      journal: emptyJournal(),
      journalTail: [],
      name: 'Broken Save',
      buildVersion: 'v0.0.0-test',
      now: new Date('2026-09-01T00:00:00.000Z'),
    });
    const roundTripped = parseGameSave(gameSaveText(brokenSave));
    assert.equal(roundTripped.ok, true);

    // The pre-fix applyLoadedSave() then also discarded save.journal and used
    // emptyJournal() as the live/persisted journal going forward. Either way the
    // journal a rebuild reads is empty:
    const journalAfterBrokenLoad = emptyJournal();
    const rebuilt = replayFromGenesis(journalAfterBrokenLoad);

    // RED: with the bug present, "rebuild" reproduces the pristine genesis city,
    // NOT the saved city — population/buildings/tick are all wrong.
    assert.deepEqual(rebuilt, initialState(), 'BUG-439 present: rebuild from an emptied journal is just initialState()');
    assert.notDeepEqual(rebuilt, liveState, 'BUG-439 present: rebuild does NOT reproduce the pre-save city');
  });

  test('GREEN: fixed construction (real journal at save time, restored at load time) makes rebuild reproduce the pre-save city exactly', () => {
    const { journal, liveState } = driveAndRecord(SCRIPT);
    assert.ok(journal.entries.length > 0, 'the driven script journal must be non-empty');

    // Fixed buildCurrentSave(): pass the REAL live journal.
    const goodSave = buildGameSave({
      state: liveState,
      journal,
      journalTail: [],
      name: 'Good Save',
      buildVersion: 'v0.0.0-test',
      now: new Date('2026-09-01T00:00:00.000Z'),
    });

    // Simulate: write to a file (Save As) and load it back fresh (a brand-new
    // parseGameSave, exactly like loadGame()/loadNamed() would produce).
    const roundTripped = parseGameSave(gameSaveText(goodSave));
    assert.equal(roundTripped.ok, true);
    const loaded = roundTripped.save;

    // Fixed applyLoadedSave(): restore save.journal into the live journal state
    // (setJournal(save.journal)) instead of emptyJournal().
    const journalAfterFixedLoad = loaded.journal;
    assert.equal(
      journalAfterFixedLoad.entries.length,
      journal.entries.length,
      'the loaded journal must carry the FULL action history, not a truncated/empty one',
    );

    // A subsequent rebuild (replayFromGenesisDefensiveChunked's core loop is
    // replayFromGenesis) must reproduce the EXACT pre-save city.
    const rebuilt = replayFromGenesis(journalAfterFixedLoad);
    assert.deepEqual(rebuilt, liveState, 'GREEN: rebuild-after-load reproduces the pre-save city exactly (non-empty, correct)');

    // Explicit, human-legible checks alongside the deep-equal (never loop/pop/0).
    assert.ok(rebuilt.buildings.length > initialState().buildings.length, 'rebuilt city has the placed buildings');
    assert.equal(rebuilt.tick, liveState.tick, 'rebuilt tick matches the pre-save tick');
    assert.equal(rebuilt.population, liveState.population, 'rebuilt population matches the pre-save population');
    assert.equal(rebuilt.funds, liveState.funds, 'rebuilt funds match the pre-save funds');
  });

  test('the loaded journal is NOT the same object as the pre-save journal (round-tripped through JSON, still equal)', () => {
    // Guards against a shallow "same reference" false-positive: the fix must
    // survive an ACTUAL JSON.stringify/JSON.parse round trip (a real save file),
    // not merely pass the same in-memory object through.
    const { journal, liveState } = driveAndRecord(SCRIPT);
    const save = buildGameSave({
      state: liveState,
      journal,
      journalTail: [],
      name: 'Round Trip City',
      buildVersion: 'v0.0.0-test',
      now: new Date('2026-09-01T00:00:00.000Z'),
    });
    const text = gameSaveText(save);
    const loaded = parseGameSave(text).save;
    assert.notEqual(loaded.journal, journal, 'sanity: this is a real JSON round trip, not the same object');
    assert.deepEqual(replayFromGenesis(loaded.journal), liveState, 'JSON round-tripped journal still reconstructs the exact city');
  });

  test('wiring proof: store.tsx actually passes the real journal to save/load, not emptyJournal()', () => {
    // BUG-439's actual defect lived in store.tsx's buildCurrentSave/applyLoadedSave
    // closures, which are not exported (they're internal to the SimProvider
    // component) so the black-box tests above can't call them directly. This
    // asserts the source wiring itself, so reverting the store.tsx fix (a scratch
    // `cp store.tsx.bak store.tsx`) reddens THIS test even though the pure
    // gamesave/genesisReplay functions above are untouched by that revert.
    const storePath = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'sim', 'store.tsx');
    const src = fs.readFileSync(storePath, 'utf8');

    const buildCurrentSaveBody = src.match(/const buildCurrentSave = \(name: string\) => \{[\s\S]*?\n  \};/);
    assert.ok(buildCurrentSaveBody, 'buildCurrentSave must exist in store.tsx');
    assert.doesNotMatch(
      buildCurrentSaveBody[0],
      /journal:\s*emptyJournal\(\)/,
      'BUG-439: buildCurrentSave must NOT hardcode journal: emptyJournal() — it must pass the real live journal',
    );
    assert.match(
      buildCurrentSaveBody[0],
      /journal,/,
      'buildCurrentSave must pass the live `journal` state into the GameSave',
    );

    const applyLoadedSaveBody = src.match(/const applyLoadedSave = \(save: GameSave\) => \{[\s\S]*?\n  \};/);
    assert.ok(applyLoadedSaveBody, 'applyLoadedSave must exist in store.tsx');
    assert.doesNotMatch(
      applyLoadedSaveBody[0],
      /setJournal\(emptyJournal\(\)\)/,
      'BUG-439: applyLoadedSave must NOT discard the loaded journal via setJournal(emptyJournal())',
    );
    assert.match(
      applyLoadedSaveBody[0],
      /setJournal\(save\.journal\)/,
      'applyLoadedSave must restore save.journal into the live journal state',
    );
    assert.doesNotMatch(
      applyLoadedSaveBody[0],
      /journalPersisterRef\.current\?\.flush\(emptyJournal\(\)\)/,
      'BUG-439: applyLoadedSave must NOT flush an empty journal to the on-disk journal persister',
    );
    assert.match(
      applyLoadedSaveBody[0],
      /journalPersisterRef\.current\?\.flush\(save\.journal\)/,
      'applyLoadedSave must flush save.journal (the full loaded history) to the on-disk journal persister',
    );
  });
});
