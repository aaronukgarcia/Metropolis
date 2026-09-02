// namedsaves.test.mjs — BUG-445/AC-5: a named-save slug collision between two
// DIFFERENT cities must never silently destroy the existing one. Two distinct
// display names can normalize to the SAME slug via cityNameToSlug() (punctuation
// is stripped), and a plain Save-As/rename previously called writeNamedSave()
// unconditionally, clobbering whatever city already lived there with no trace.
//
// Run with `npm test` (node --test).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { writeNamedSave, readNamedSave, cityNameToSlug, checkNamedSaveCollision } from '../src/sim/namedsaves.ts';
import { buildGameSave } from '../src/sim/gamesave.ts';
import { initialState } from '../src/sim/engine.ts';
import { emptyJournal } from '../src/sim/journal.ts';

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

function makeSave(name) {
  return buildGameSave({
    state: initialState(),
    journal: emptyJournal(),
    journalTail: [],
    name,
    buildVersion: 'v1.0.0-test',
  });
}

test('BUG-445 fixture precondition: two distinct city names can normalize to the SAME slug', () => {
  const slugA = cityNameToSlug('My City!');
  const slugB = cityNameToSlug('My City?');
  assert.equal(slugA, slugB, 'cityNameToSlug must strip punctuation identically for this scenario to be reachable');
});

test('BUG-445/AC-5: checkNamedSaveCollision detects a DIFFERENT city already occupying the slug', () => {
  const storage = new MockStorage();
  assert.ok(writeNamedSave(storage, makeSave('My City!')), 'seed write must succeed');
  const collision = checkNamedSaveCollision(storage, 'My City?');
  assert.ok(collision, 'a same-slug different-name save must be reported as a collision');
  assert.equal(collision.slug, cityNameToSlug('My City!'));
  assert.equal(collision.existingName, 'My City!');
});

test('BUG-445/AC-5: re-saving the SAME city onto its own existing slug is NEVER reported as a collision', () => {
  const storage = new MockStorage();
  assert.ok(writeNamedSave(storage, makeSave('Metropolis Prime')));
  assert.equal(
    checkNamedSaveCollision(storage, 'Metropolis Prime'),
    null,
    'a same-city re-save must proceed with no confirmation prompt',
  );
  // Whitespace-only edits normalize to the exact same slug -- still no collision.
  assert.equal(checkNamedSaveCollision(storage, '  Metropolis Prime  '), null);
});

test('BUG-445 empty-slot precondition: a free slug is never a collision', () => {
  const storage = new MockStorage();
  assert.equal(checkNamedSaveCollision(storage, 'Nobody Here Yet'), null);
});

test('BUG-445 RED PROOF: writeNamedSave alone (the pre-fix call path, ungated) silently destroys a different city at the same slug', () => {
  const storage = new MockStorage();
  writeNamedSave(storage, makeSave('My City!'));
  assert.equal(readNamedSave(storage, cityNameToSlug('My City!')).name, 'My City!');
  // This is exactly what saveGameAs()/renameNamedSave() did before BUG-445: call
  // writeNamedSave directly with no collision check at all.
  writeNamedSave(storage, makeSave('My City?'));
  const after = readNamedSave(storage, cityNameToSlug('My City!'));
  assert.equal(
    after.name,
    'My City?',
    'documents the underlying hazard: writeNamedSave() itself has no notion of "different city" -- ' +
      'the CALLER (store.tsx) must gate every write through checkNamedSaveCollision before calling it',
  );
});

test('BUG-445/AC-5: the store-level collision-confirm contract -- blocked without confirmation, proceeds once confirmed, same-city never blocks', () => {
  const storage = new MockStorage();
  writeNamedSave(storage, makeSave('My City!'));

  // Mirrors saveGameAs()'s gating logic in store.tsx exactly: check first,
  // refuse the write entirely (never touch storage) unless confirmed.
  function saveAsGated(name, confirmedOverwrite = false) {
    const collision = checkNamedSaveCollision(storage, name);
    if (collision && !confirmedOverwrite) return { ok: false, collision };
    return { ok: writeNamedSave(storage, makeSave(name)) };
  }

  const blocked = saveAsGated('My City?');
  assert.equal(blocked.ok, false, 'a collision without confirmation must be refused');
  assert.ok(blocked.collision);
  assert.equal(
    readNamedSave(storage, cityNameToSlug('My City!')).name,
    'My City!',
    'a blocked write must not touch storage at all -- the existing city survives untouched',
  );

  const confirmed = saveAsGated('My City?', true);
  assert.equal(confirmed.ok, true, 'once confirmed, the overwrite proceeds');
  assert.equal(readNamedSave(storage, cityNameToSlug('My City!')).name, 'My City?');

  const resave = saveAsGated('My City?');
  assert.equal(resave.ok, true, 'a same-city re-save onto its own slug proceeds without any confirmation');
});
