// rebuild-loop-bug468.test.mjs — BUG-468: close the infinite "New build detected"
// rebuild-prompt loop.
//
// Dogfood repro (Aaron 2026-08-31): a saved city stamped v0.3.0.193 while the
// running bundle is v0.3.0.191. The prompt fires (saved≠running). The player
// clicks Rebuild → Resume (or Keep old snapshot) — but the persisted savepoint's
// buildVersion was NEVER re-stamped to the running build, so the very NEXT load
// re-detects saved≠running and re-prompts forever.
//
// ROOT CAUSE + FIX under test (both PURE, so node --test runs them exactly as CI does):
//   1. restampSavepointsBuildVersion(storage, running) rewrites the persisted
//      savepoint's buildVersion to the running build. After ONE resolution the
//      mismatch is gone and needsRebuild() is false on the second load.
//   2. classifyVersionChange() distinguishes an upgrade from a REGRESSION
//      (saved NEWER than running) so the store/UI don't force an endless rebuild.
//
// PROVE-CAN-FAIL: the "second load" assertions read needsRebuild(readback, running).
// Comment out the restamp call (or revert restampSavepointsBuildVersion to a no-op)
// and the readback still holds the OLD buildVersion → needsRebuild stays TRUE →
// "second load does NOT re-prompt" FAILS. Verified out-of-band (cp/mv, never git).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  persistSavepoint,
  readAllSavepoints,
  mostRecentSavepoint,
  createSavepoint,
  restampSavepointsBuildVersion,
} from '../src/sim/replay.ts';
import { needsRebuild, classifyVersionChange } from '../src/sim/genesisReplay.ts';
import { initialState } from '../src/sim/engine.ts';

/** Minimal injectable in-memory storage mirroring the Web Storage subset. */
function memStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
    removeItem: (k) => void m.delete(k),
  };
}

/** Read back the persisted savepoint's buildVersion the way boot does. */
function persistedBuildVersion(storage) {
  return mostRecentSavepoint(readAllSavepoints(storage))?.buildVersion ?? null;
}

describe('BUG-468: restamp closes the rebuild-prompt loop', () => {
  test('upgrade: saved<running — after restamp the second load does NOT re-prompt', () => {
    const storage = memStorage();
    const saved = 'v0.3.0.189';
    const running = 'v0.3.0.191';
    persistSavepoint(storage, createSavepoint(initialState(), [], new Date(), saved, null));

    // First load: mismatch detected → the prompt fires.
    assert.equal(needsRebuild(persistedBuildVersion(storage), running), true, 'first load must prompt');

    // Resolve (Rebuild+Resume / Keep both re-stamp to the running build).
    const wrote = restampSavepointsBuildVersion(storage, running);
    assert.equal(wrote, true, 'a stale savepoint must be re-stamped');
    assert.equal(persistedBuildVersion(storage), running, 'persisted buildVersion is now the running build');

    // Second load: no mismatch → the loop is closed.
    assert.equal(needsRebuild(persistedBuildVersion(storage), running), false, 'second load must NOT re-prompt');
  });

  test('regression: saved>running — resolves without an endless rebuild', () => {
    const storage = memStorage();
    const saved = 'v0.3.0.193'; // NEWER than running — the exact dogfood case
    const running = 'v0.3.0.191';
    persistSavepoint(storage, createSavepoint(initialState(), [], new Date(), saved, null));

    // It is a regression, not an upgrade — the UI must not force a forward rebuild.
    assert.equal(classifyVersionChange(saved, running), 'regression');
    assert.equal(needsRebuild(persistedBuildVersion(storage), running), true, 'first load prompts (keep/rebuild)');

    // Whichever path the player picks, the store re-stamps to running.
    restampSavepointsBuildVersion(storage, running);
    assert.equal(persistedBuildVersion(storage), running);
    assert.equal(needsRebuild(persistedBuildVersion(storage), running), false, 'no re-prompt after resolution');
  });

  test('idempotent: re-stamping an already-current savepoint leaves it unchanged', () => {
    const storage = memStorage();
    const running = 'v0.3.0.191';
    persistSavepoint(storage, createSavepoint(initialState(), [], new Date(), running, null));
    // No stale slot → returns false (nothing to write) and the value stays put.
    assert.equal(restampSavepointsBuildVersion(storage, running), false);
    assert.equal(persistedBuildVersion(storage), running);
  });

  test('fail-safe: an empty store never throws and reports no write', () => {
    const storage = memStorage();
    assert.doesNotThrow(() => restampSavepointsBuildVersion(storage, 'v0.3.0.191'));
    assert.equal(restampSavepointsBuildVersion(storage, 'v0.3.0.191'), false);
  });

  test('an empty running version is a no-op (never blanks the stamp)', () => {
    const storage = memStorage();
    persistSavepoint(storage, createSavepoint(initialState(), [], new Date(), 'v0.3.0.189', null));
    assert.equal(restampSavepointsBuildVersion(storage, ''), false);
    assert.equal(persistedBuildVersion(storage), 'v0.3.0.189', 'stamp untouched when running is empty');
  });
});

describe('BUG-468: classifyVersionChange direction', () => {
  test('same version → same', () => {
    assert.equal(classifyVersionChange('v0.3.0.191', 'v0.3.0.191'), 'same');
  });
  test('saved older than running → upgrade', () => {
    assert.equal(classifyVersionChange('v0.3.0.189', 'v0.3.0.191'), 'upgrade');
    assert.equal(classifyVersionChange('v0.2.9.999', 'v0.3.0.0'), 'upgrade');
  });
  test('saved newer than running → regression', () => {
    assert.equal(classifyVersionChange('v0.3.0.193', 'v0.3.0.191'), 'regression');
    assert.equal(classifyVersionChange('v1.0.0.0', 'v0.9.9.9'), 'regression');
  });
  test('leading "v" is optional and does not affect the compare', () => {
    assert.equal(classifyVersionChange('0.3.0.189', 'v0.3.0.191'), 'upgrade');
  });
  test('missing or non-numeric → unknown (fall back to plain differs copy)', () => {
    assert.equal(classifyVersionChange(null, 'v0.3.0.191'), 'unknown');
    assert.equal(classifyVersionChange('v0.3.0.191', undefined), 'unknown');
    assert.equal(classifyVersionChange('dev', 'v0.3.0.191'), 'unknown');
    assert.equal(classifyVersionChange('v0.3.0.191', 'abc1234'), 'unknown');
  });
});
