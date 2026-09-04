// consolidator-toggle.test.mjs — FEAT-2326609761 inc1: the consolidator
// enable toggle. ASM-1504 is the risk this file exists to gate: EVERY other
// flag in this codebase (liveEngineFlag.ts, webWorkerFlag.ts,
// debugBuildSpeed.ts) is localStorage-backed, so the natural-but-WRONG thing
// to build here is another localStorage flag — which would make the exact
// same journal rebuild a DIFFERENT city depending on which machine/cache
// loaded it. `consolidatorEnabled` is instead plain journalled SimState,
// flipped only by the `toggleConsolidator` action (engine.ts), mirroring
// `toggleGridImport` exactly.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { emptyJournal, recordAction, isStateAffecting } from '../src/sim/journal.ts';
import { initialState, reducer, CONSOLIDATOR_ENABLED_DEFAULT } from '../src/sim/engine.ts';
import { replayIsDeterministic, replayFromGenesis, stableStringify } from '../src/sim/genesisReplay.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

test('AC-1(a): toggleConsolidator is classified state-affecting in the journal', () => {
  assert.equal(isStateAffecting({ type: 'toggleConsolidator' }), true);
});

test('AC-1: new cities default to consolidatorEnabled === false (CONSOLIDATOR_ENABLED_DEFAULT)', () => {
  assert.equal(CONSOLIDATOR_ENABLED_DEFAULT, false);
  const s = initialState();
  assert.equal(s.consolidatorEnabled ?? CONSOLIDATOR_ENABLED_DEFAULT, false);
});

test('toggleConsolidator flips the flag and nothing else', () => {
  const s0 = initialState();
  const s1 = reducer(s0, { type: 'toggleConsolidator' });
  assert.equal(s1.consolidatorEnabled, true);
  const s2 = reducer(s1, { type: 'toggleConsolidator' });
  assert.equal(s2.consolidatorEnabled, false);
  // Nothing else about the state changed (funds/buildings/tick untouched).
  assert.equal(s1.funds, s0.funds);
  assert.equal(s1.buildings.length, s0.buildings.length);
  assert.equal(s1.tick, s0.tick);
});

test('AC-16 default (GR#16): a legacy state object with NO consolidatorEnabled field reads as false everywhere', () => {
  const s = initialState();
  delete s.consolidatorEnabled; // simulate an old save serialized before this field existed
  assert.equal(s.consolidatorEnabled ?? CONSOLIDATOR_ENABLED_DEFAULT, false);
});

test('ASM-1504: a journal containing a toggle replays to a state with the SAME consolidatorEnabled — byte-identical, twice', () => {
  let journal = emptyJournal();
  let state = initialState();
  const script = [
    { type: 'tick' },
    { type: 'toggleConsolidator' },
    { type: 'tick' },
    { type: 'tick' },
    { type: 'toggleConsolidator' },
    { type: 'toggleConsolidator' },
    { type: 'tick' },
  ];
  for (const action of script) {
    journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  assert.equal(state.consolidatorEnabled, true, 'three toggles from false ends true');
  assert.equal(replayIsDeterministic(journal), true, 'genesis replay must be deterministic with a consolidator toggle in the journal');

  const replayed = replayFromGenesis(journal);
  assert.equal(replayed.consolidatorEnabled, state.consolidatorEnabled, 'replayed state must carry the SAME consolidatorEnabled as the live-driven state');
  assert.equal(stableStringify(replayFromGenesis(journal)), stableStringify(replayFromGenesis(journal)), 'two replays of the same journal are byte-identical');
});

test('ASM-1504: grep proves no consolidator code path reads localStorage', () => {
  for (const rel of ['../src/sim/consolidator.ts', '../src/sim/consolidatorFocus.ts', '../src/components/left/tabs/consolidatorTab.tsx']) {
    const src = fs.readFileSync(path.join(__dirname, rel), 'utf8');
    const codeOnly = src
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .split('\n')
      .map((line) => line.replace(/\/\/.*$/, ''))
      .join('\n');
    assert.ok(!codeOnly.includes('localStorage.'), `${rel} must never read/write localStorage for the consolidator toggle`);
  }
});

test('the toggle round-trips through a savepoint-shaped snapshot object (JSON serialize/deserialize)', () => {
  const s = reducer(initialState(), { type: 'toggleConsolidator' });
  assert.equal(s.consolidatorEnabled, true);
  const roundTripped = JSON.parse(JSON.stringify(s));
  assert.equal(roundTripped.consolidatorEnabled, true, 'consolidatorEnabled must survive a plain JSON round-trip, same as every other SimState field');
});
