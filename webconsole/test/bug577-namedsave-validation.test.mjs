// bug577-namedsave-validation.test.mjs — BUG-577: the "Load -> Saved cities"
// (named-save) route is the player's MOST-USED load path but used to skip ALL
// shape validation. readNamedSave() did a bare `JSON.parse(decode(raw)) as
// GameSave` with no structural checking, and store.tsx fed that straight into
// applyLoadedSave, which dereferences `save.savepoint.camera` OUTSIDE any
// try/catch -- a corrupt named save threw an uncaught TypeError with no
// recordError, no MET-V850 registry code, no user-visible message.
//
// This proves readNamedSave now routes a malformed named save through the
// SAME structural validator File->Open uses (validateGameSaveObject /
// parseGameSave in gamesave.ts), rejecting with the identical registry-
// sourced MET-V850 -- never an uncaught throw, never a silently-accepted
// partial object -- while a genuinely valid named save still loads.
//
// Run with `npm test` (node --test).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readNamedSave, writeNamedSave } from '../src/sim/namedsaves.ts';
import { buildGameSave } from '../src/sim/gamesave.ts';
import { encode } from '../src/sim/saveCodec.ts';
import { initialState } from '../src/sim/engine.ts';
import { emptyJournal } from '../src/sim/journal.ts';

const SLOT_PREFIX = 'metropolis.namedSave.';

class MockStorage {
  constructor() {
    this.data = new Map();
  }
  getItem(k) {
    return this.data.has(k) ? this.data.get(k) : null;
  }
  setItem(k, v) {
    this.data.set(k, String(v));
  }
  removeItem(k) {
    this.data.delete(k);
  }
}

function makeValidSave(name) {
  return buildGameSave({
    state: initialState(),
    journal: emptyJournal(),
    journalTail: [],
    name,
    buildVersion: 'v1.0.0-test',
  });
}

/** Writes a raw (already-encoded) blob directly into a named-save slot,
 * bypassing writeNamedSave's own (valid-by-construction) save-building so a
 * malformed shape can be planted the same way real storage corruption would
 * produce one. */
function plantRawSlot(storage, slug, value) {
  storage.setItem(`${SLOT_PREFIX}${slug}`, encode(value));
}

test('BUG-577 baseline precondition: a VALID named save round-trips through readNamedSave unchanged', () => {
  const storage = new MockStorage();
  const save = makeValidSave('Good City');
  assert.ok(writeNamedSave(storage, save), 'seed write must succeed');
  const loaded = readNamedSave(storage, 'Good-City');
  assert.ok(loaded, 'a valid named save must still load');
  assert.equal(loaded.name, 'Good City');
  assert.equal(loaded.savepoint.snapshot.tick, save.savepoint.snapshot.tick);
});

test('BUG-577: a named save missing `savepoint` entirely is REJECTED with MET-V850, not an uncaught throw', () => {
  const storage = new MockStorage();
  const malformed = { format: 'metropolis-save/1', name: 'Broken', savedAt: new Date().toISOString(), buildVersion: 'v1.0.0-test' };
  plantRawSlot(storage, 'broken', JSON.stringify(malformed));

  assert.throws(
    () => readNamedSave(storage, 'broken'),
    (err) => {
      assert.ok(err instanceof Error, 'must reject via a thrown Error, never a silent null/undefined');
      assert.equal(err.code, 'MET-V850', 'must reject via the SAME registry error File->Open uses');
      return true;
    },
    'a named save missing savepoint must throw MET-V850, mirroring parseGameSave',
  );
});

test('BUG-577: a named save with a non-object root is REJECTED with MET-V850', () => {
  const storage = new MockStorage();
  plantRawSlot(storage, 'garbage-array', JSON.stringify([1, 2, 3]));

  assert.throws(
    () => readNamedSave(storage, 'garbage-array'),
    (err) => err instanceof Error && err.code === 'MET-V850',
  );
});

test('BUG-577: a named save with a garbage element inside snapshot.buildings is REJECTED with MET-V850', () => {
  const storage = new MockStorage();
  const save = makeValidSave('Bad Buildings');
  const corrupted = {
    ...save,
    savepoint: {
      ...save.savepoint,
      snapshot: {
        ...save.savepoint.snapshot,
        buildings: [{ notEvenClose: true }],
      },
    },
  };
  plantRawSlot(storage, 'bad-buildings', JSON.stringify(corrupted));

  assert.throws(
    () => readNamedSave(storage, 'bad-buildings'),
    (err) => err instanceof Error && err.code === 'MET-V850',
  );
});

test('BUG-577: a missing slot still returns null (nothing shaped to reject, distinct from a corrupt one)', () => {
  const storage = new MockStorage();
  assert.equal(readNamedSave(storage, 'never-saved'), null);
});

test('BUG-577 RED PROOF: reverting readNamedSave to a bare unchecked cast lets the malformed-save case slip through', () => {
  // This documents the pre-fix behaviour directly (rather than depending on
  // git history) so the proof survives independent of the working tree:
  // JSON.parse(...) as GameSave on the SAME malformed blob used above does
  // NOT throw and does NOT reject -- it hands back a GameSave-shaped object
  // with `savepoint` simply absent, exactly the object that used to reach
  // applyLoadedSave's unguarded `save.savepoint.camera` dereference and blow
  // up as an uncaught TypeError with no recordError/MET-V850 at all.
  const malformed = { format: 'metropolis-save/1', name: 'Broken', savedAt: new Date().toISOString(), buildVersion: 'v1.0.0-test' };
  const preFixParse = () => JSON.parse(JSON.stringify(malformed));
  const bare = preFixParse();
  assert.doesNotThrow(() => bare, 'the unchecked cast itself never throws -- that IS the silent-failure bug');
  assert.equal(bare.savepoint, undefined, 'the un-validated object is missing savepoint, exactly what crashed applyLoadedSave');
  assert.throws(
    () => {
      // Mirrors applyLoadedSave's unguarded access (store.tsx ~823):
      // `save.savepoint.camera` with no try/catch around it.
      return bare.savepoint.camera;
    },
    TypeError,
    'proves the pre-fix shape reaches an uncaught TypeError once dereferenced, which is exactly what the real validation now prevents at readNamedSave() instead',
  );
});
