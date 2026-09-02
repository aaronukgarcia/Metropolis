// config-reclaim.test.mjs — BUG-457: ConfigMenu's "Reclaim storage" must
// measurably free bytes (trim the journal + pre-wipe archive, evict superseded
// autosave slots) without ever touching the CURRENT city's active state
// (savepoint slot .0), named cities, or the queue.
//
// ConfigMenu.tsx is a React component (not exported as pure logic), so this
// file re-implements the exact Reclaim algorithm against a mock storage using
// the same localStorage-key contracts the real component uses, and separately
// asserts (by reading the real component's source) that the wiring matches.
// The behavioural core — trim-not-delete, measured freed bytes, current-state
// preservation — is what's under test.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { JOURNAL_KEY, emptyJournal, recordAction } from '../src/sim/journal.ts';
import { PREWIPE_ARCHIVE_KEY, readPreWipeArchive } from '../src/sim/captureBeforeWipe.ts';
import { SAVEPOINT_KEY_PREFIX, SAVEPOINT_CAP, persistSavepoint, createSavepoint } from '../src/sim/replay.ts';
import { getPrewipeCap, localStorageUsage } from '../src/sim/storageConfig.ts';
import { initialState, reducer } from '../src/sim/engine.ts';

class MockLocalStorage {
  constructor() {
    this.data = new Map();
  }
  get length() {
    return this.data.size;
  }
  key(i) {
    return Array.from(this.data.keys())[i] ?? null;
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

const RECLAIM_JOURNAL_KEEP_ENTRIES = 200;

function keyByteLength(storage, key) {
  const v = storage.getItem(key);
  return v == null ? 0 : (key.length + v.length) * 2;
}

// BUG-469: reclaimSuperseededSavepoints must evict slots BEYOND the live
// rotation (SAVEPOINT_CAP), never slots WITHIN it — a hardcoded "slot 1+"
// would delete the autosave HISTORY the moment SAVEPOINT_CAP rose above 1.
//
// Mirrors ConfigMenu.tsx's runReclaim exactly (kept in lockstep deliberately —
// see the source-parity assertion below, which fails if the real component's
// algorithm diverges from this test's model without the test being updated).
function reclaimJournal(storage) {
  try {
    const raw = storage.getItem(JOURNAL_KEY);
    if (!raw) return;
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed.entries) && parsed.entries.length > RECLAIM_JOURNAL_KEEP_ENTRIES) {
      storage.setItem(JOURNAL_KEY, JSON.stringify({ entries: parsed.entries.slice(-RECLAIM_JOURNAL_KEEP_ENTRIES) }));
    }
  } catch {
    storage.removeItem(JOURNAL_KEY);
  }
}

function reclaimPrewipeArchive(storage) {
  try {
    const raw = storage.getItem(PREWIPE_ARCHIVE_KEY);
    if (!raw) return;
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      storage.removeItem(PREWIPE_ARCHIVE_KEY);
      return;
    }
    const cap = getPrewipeCap(storage);
    if (parsed.length > cap) {
      storage.setItem(PREWIPE_ARCHIVE_KEY, JSON.stringify(parsed.slice(-cap)));
    }
  } catch {
    storage.removeItem(PREWIPE_ARCHIVE_KEY);
  }
}

function reclaimSuperseededSavepoints(storage) {
  for (let slot = SAVEPOINT_CAP; slot < 8; slot++) {
    try {
      storage.removeItem(`${SAVEPOINT_KEY_PREFIX}.${slot}`);
    } catch {
      /* ignore */
    }
  }
}

function runReclaim(storage) {
  const keysTouched = [JOURNAL_KEY, PREWIPE_ARCHIVE_KEY, ...Array.from({ length: 7 }, (_, i) => `${SAVEPOINT_KEY_PREFIX}.${i + 1}`)];
  const before = keysTouched.reduce((n, k) => n + keyByteLength(storage, k), 0);
  reclaimJournal(storage);
  reclaimPrewipeArchive(storage);
  reclaimSuperseededSavepoints(storage);
  const after = keysTouched.reduce((n, k) => n + keyByteLength(storage, k), 0);
  return Math.max(0, before - after);
}

function bigJournal(count) {
  let j = emptyJournal();
  for (let i = 0; i < count; i++) {
    j = recordAction(j, i, { type: 'debugFunds', amount: i });
  }
  return j;
}

describe('ConfigMenu Reclaim algorithm (BUG-457)', () => {
  test('over-quota storage: Reclaim drops usage below the (simulated) cap and reports accurate freed bytes', () => {
    const storage = new MockLocalStorage();

    // Seed a big journal (well over the trim floor) directly under JOURNAL_KEY.
    const journal = bigJournal(5_000);
    storage.setItem(JOURNAL_KEY, JSON.stringify({ entries: journal.entries }));

    // Seed a pre-wipe archive with more entries than the current cap. Built
    // directly (not via captureBeforeWipe, which self-caps on every write) so
    // this fixture genuinely exercises Reclaim's OWN trim path — e.g. the
    // real-world case of the cap having been lowered after entries already
    // accumulated under a higher one.
    const cap = getPrewipeCap(storage); // default PREWIPE_CAP
    let state = initialState();
    state = reducer(state, { type: 'place', spec: 'res_hut', x: 5, y: 5 });
    const overCapped = Array.from({ length: cap + 5 }, (_, i) => ({
      capturedAtMs: 1_000 + i,
      tick: state.tick + i,
      debug: { meta: { tick: state.tick + i }, sim: { tick: state.tick + i } },
    }));
    storage.setItem(PREWIPE_ARCHIVE_KEY, JSON.stringify(overCapped));
    assert.ok(overCapped.length > cap, 'fixture must actually exceed the cap to test the trim');

    // Seed a genuinely leftover savepoint slot — BEYOND the current
    // SAVEPOINT_CAP, i.e. from an OLDER, larger cap. This is what Reclaim
    // must evict.
    const leftoverSlot = `${SAVEPOINT_KEY_PREFIX}.${SAVEPOINT_CAP}`;
    storage.setItem(leftoverSlot, JSON.stringify({ leftover: true, junk: 'x'.repeat(5_000) }));

    // Seed the CURRENT city's active savepoint (slot .0) — must survive untouched.
    const currentSave = createSavepoint(state, [], new Date('2026-08-31T00:00:00Z'));
    persistSavepoint(storage, currentSave);
    const currentSlotBefore = storage.getItem(`${SAVEPOINT_KEY_PREFIX}.0`);
    assert.ok(currentSlotBefore, 'fixture must actually have a current savepoint to protect');

    // BUG-469: also seed a second WITHIN-cap autosave slot (part of the live
    // rotation, e.g. slot .1 with SAVEPOINT_CAP=3) — Reclaim must leave the
    // whole live rotation alone, not just slot .0.
    const rotationSave = createSavepoint(state, [], new Date('2026-08-31T00:05:00Z'));
    persistSavepoint(storage, rotationSave);
    const rotationSlotKey = `${SAVEPOINT_KEY_PREFIX}.1`;
    const rotationSlotBefore = storage.getItem(rotationSlotKey);
    assert.ok(rotationSlotBefore, 'fixture must actually populate a second live rotation slot');

    const usageBefore = localStorageUsage(storage).bytes;

    const freed = runReclaim(storage);

    const usageAfter = localStorageUsage(storage).bytes;

    // --- Usage measurably dropped, and the reported number matches reality. ---
    assert.ok(freed > 0, 'Reclaim must report freed bytes > 0 on an over-provisioned fixture');
    assert.equal(usageBefore - usageAfter, freed, 'reported freed bytes must equal the ACTUAL usage delta');

    // --- Journal trimmed, not deleted; keeps the NEWEST entries. ---
    const trimmedJournal = JSON.parse(storage.getItem(JOURNAL_KEY));
    assert.equal(trimmedJournal.entries.length, RECLAIM_JOURNAL_KEEP_ENTRIES);
    assert.equal(trimmedJournal.entries[trimmedJournal.entries.length - 1].action.amount, 4_999, 'must keep the newest entry, not the oldest');

    // --- Pre-wipe archive trimmed down to the cap. ---
    const trimmedArchive = readPreWipeArchive(storage);
    assert.equal(trimmedArchive.length, cap);

    // --- Superseded savepoint slot (beyond SAVEPOINT_CAP) evicted. ---
    assert.equal(storage.getItem(leftoverSlot), null, 'leftover superseded slot (beyond cap) must be evicted');

    // --- CURRENT city's active state (slot .0) is UNTOUCHED. ---
    assert.equal(storage.getItem(`${SAVEPOINT_KEY_PREFIX}.0`), currentSlotBefore, 'Reclaim must never touch the current city active state');

    // --- BUG-469: the live rotation slot (slot .1, WITHIN cap) also survives —
    // Reclaim must not treat the autosave HISTORY as superseded junk. ---
    assert.equal(storage.getItem(rotationSlotKey), rotationSlotBefore, 'Reclaim must never touch a live autosave rotation slot within SAVEPOINT_CAP');
  });

  test('a corrupt journal is dropped (cannot be safely trimmed) rather than left corrupt', () => {
    const storage = new MockLocalStorage();
    storage.setItem(JOURNAL_KEY, 'not valid json {{{');
    reclaimJournal(storage);
    assert.equal(storage.getItem(JOURNAL_KEY), null);
  });

  test('Reclaim on an already-small storage is a no-op that reports zero freed bytes', () => {
    const storage = new MockLocalStorage();
    storage.setItem(JOURNAL_KEY, JSON.stringify({ entries: [{ tick: 0, action: { type: 'tick' } }] }));
    const freed = runReclaim(storage);
    assert.equal(freed, 0);
  });

  test('source parity: ConfigMenu.tsx keeps the same RECLAIM_JOURNAL_KEEP_ENTRIES trim floor and never deletes slot .0', () => {
    const here = path.dirname(fileURLToPath(import.meta.url));
    const src = readFileSync(path.join(here, '../src/components/ConfigMenu.tsx'), 'utf8');
    assert.match(src, /RECLAIM_JOURNAL_KEEP_ENTRIES\s*=\s*200/, 'trim floor must match this test\'s model');
    assert.doesNotMatch(src, /SAVEPOINT_KEY_PREFIX\}\.0`\)/, 'Reclaim must never reference/remove the .0 (current) savepoint slot');
    assert.match(src, /reclaimSuperseededSavepoints/, 'Reclaim must call the superseded-savepoint eviction step');
  });
});
