// saveStore.test.mjs — FEAT-2326609778: IndexedDB durable save-estate layer.
//
// Run with `npm test` (node --test).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  memoryKVStore,
  indexedDBKVStore,
  createSaveStore,
  migrateFromLocalStorage,
  mirrorKeyFromLocalStorage,
  mirrorSaveCheckpoint,
  resetSaveStoreForTests,
  SAVE_STORE_MIGRATED_KEY,
} from '../src/sim/saveStore.ts';
import { createFakeIndexedDBFactory } from './helpers/fakeIndexedDB.mjs';

function memLocalStorage(initial = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem(k) {
      return map.has(k) ? map.get(k) : null;
    },
    setItem(k, v) {
      map.set(k, String(v));
    },
    removeItem(k) {
      map.delete(k);
    },
    get length() {
      return map.size;
    },
    key(i) {
      return Array.from(map.keys())[i] ?? null;
    },
    _map: map,
  };
}

test('memoryKVStore: basic get/set/remove/listKeys round-trip', async () => {
  const store = memoryKVStore();
  assert.equal(await store.getItem('a'), null);
  const r = await store.setItem('a', 'hello');
  assert.equal(r.ok, true);
  assert.equal(await store.getItem('a'), 'hello');
  await store.setItem('metropolis.savepoint.0', 'sp0');
  assert.deepEqual((await store.listKeys('metropolis.savepoint.')).sort(), ['metropolis.savepoint.0']);
  await store.removeItem('a');
  assert.equal(await store.getItem('a'), null);
});

test('indexedDBKVStore against the fake IndexedDB: round-trip through the REAL production adapter code', async () => {
  const backing = new Map();
  const factory1 = createFakeIndexedDBFactory(backing);
  const idb1 = indexedDBKVStore(factory1);
  await idb1.setItem('metropolis.savepoint.0', 'payload-v1');
  assert.equal(await idb1.getItem('metropolis.savepoint.0'), 'payload-v1');

  // Simulate "close the tab, reopen it": a fresh factory instance pointed at
  // the SAME backing map must see the persisted data.
  const factory2 = createFakeIndexedDBFactory(backing);
  const idb2 = indexedDBKVStore(factory2);
  assert.equal(await idb2.getItem('metropolis.savepoint.0'), 'payload-v1');

  const keys = await idb2.listKeys();
  assert.ok(keys.includes('metropolis.savepoint.0'));
  await idb2.removeItem('metropolis.savepoint.0');
  assert.equal(await idb2.getItem('metropolis.savepoint.0'), null);
});

test('indexedDBKVStore returns null when no IndexedDB factory is present (feature detect)', () => {
  assert.equal(indexedDBKVStore(null), null);
});

test('createSaveStore(null): fully degraded from the start, in-memory writes still succeed, loud error recorded', async () => {
  resetSaveStoreForTests();
  const { recentErrors } = await import('../src/sim/backend.ts');
  const beforeCount = recentErrors().length;
  const store = createSaveStore(null);
  assert.equal(store.isFullyDegraded(), true);
  const r = await store.setItem('x', 'y');
  assert.equal(r.ok, true);
  assert.equal(r.degraded, true);
  assert.equal(await store.getItem('x'), 'y');
  const after = recentErrors();
  assert.ok(after.length > beforeCount, 'a loud registry error must be recorded when IndexedDB is unavailable');
  assert.ok(after.some((e) => e.code === 'MET-V858'), 'the recorded error must carry MET-V858');
});

test('createSaveStore: a write failure on the real IndexedDB path degrades that key to memory, keeps other keys on IndexedDB, and the game keeps running', async () => {
  const backing = new Map();
  const factory = createFakeIndexedDBFactory(backing, { failNextWrites: { count: 1, error: () => Object.assign(new Error('QuotaExceededError'), { name: 'QuotaExceededError', code: 22 }) } });
  const idb = indexedDBKVStore(factory);
  const store = createSaveStore(idb);

  // First write fails (simulated quota) -> degrades to memory, still resolves ok.
  const r1 = await store.setItem('metropolis.savepoint.0', 'big-city');
  assert.equal(r1.ok, true);
  assert.equal(r1.degraded, true);
  assert.equal(await store.getItem('metropolis.savepoint.0'), 'big-city');
  assert.deepEqual(store.degradedKeys(), ['metropolis.savepoint.0']);

  // A DIFFERENT key still goes to IndexedDB (the fake only fails exactly one
  // write) — proving degradation is per-key, not global.
  const r2 = await store.setItem('metropolis.savepoint.1', 'other-city');
  assert.equal(r2.ok, true);
  assert.equal(r2.degraded, false);
  assert.equal(backing.get('metropolis-saves').get('metropolis.savepoint.1'), 'other-city');
  // The degraded key was never written to the real backing map.
  assert.equal(backing.get('metropolis-saves').has('metropolis.savepoint.0'), false);
});

test('migrateFromLocalStorage: copies known keys into the store, never deletes the localStorage originals, and never calls removeItem', async () => {
  const ls = memLocalStorage({
    'metropolis.savepoint.0': '{"tick":5}',
    'metropolis.namedSave.my-city': '{"name":"my-city"}',
    'metropolis.namedSaves': '[{"slug":"my-city"}]',
    'metropolis.currentCityName': 'my-city',
    'metropolis.journal': '{"entries":[]}',
    'metropolis.preWipeArchive': '[]',
    // Deliberately NOT migrated — debug/flag keys stay where they are.
    'metropolis.debugQueue': 'should-not-be-copied',
    'metropolis.flag.webworker': 'true',
  });
  // Hard proof "never delete": removeItem is absent entirely — if
  // migrateFromLocalStorage ever calls it, this throws and the test fails.
  delete ls.removeItem;

  const store = createSaveStore(memoryKVStoreAsIdb());
  const result = await migrateFromLocalStorage(store, ls);

  assert.equal(result.ran, true);
  assert.ok(result.keysCopied.includes('metropolis.savepoint.0'));
  assert.ok(result.keysCopied.includes('metropolis.namedSave.my-city'));
  assert.ok(result.keysCopied.includes('metropolis.namedSaves'));
  assert.ok(result.keysCopied.includes('metropolis.currentCityName'));
  assert.ok(result.keysCopied.includes('metropolis.journal'));
  assert.ok(result.keysCopied.includes('metropolis.preWipeArchive'));
  assert.equal(result.keysCopied.includes('metropolis.debugQueue'), false);
  assert.equal(result.keysCopied.includes('metropolis.flag.webworker'), false);

  assert.equal(await store.getItem('metropolis.savepoint.0'), '{"tick":5}');
  // Originals are byte-identical and still present — migration is copy-in only.
  assert.equal(ls.getItem('metropolis.savepoint.0'), '{"tick":5}');
  assert.equal(ls.getItem('metropolis.debugQueue'), 'should-not-be-copied');

  // Idempotent: a second call is a no-op (flag already set), even if
  // localStorage has since changed — migration runs exactly once.
  ls.setItem('metropolis.savepoint.0', '{"tick":999}');
  const second = await migrateFromLocalStorage(store, ls);
  assert.equal(second.ran, false);
  assert.equal(await store.getItem('metropolis.savepoint.0'), '{"tick":5}');
});

test('migrateFromLocalStorage: a per-key copy failure is skipped, not fatal to the rest of the migration', async () => {
  const ls = memLocalStorage({
    'metropolis.savepoint.0': 'ok-payload',
    'metropolis.journal': 'also-ok',
  });
  let calls = 0;
  const flaky = {
    async getItem() {
      return null;
    },
    async setItem(key) {
      calls++;
      if (key === 'metropolis.savepoint.0') return { ok: false, quota: true, error: 'simulated quota', degraded: false };
      return { ok: true, quota: false, degraded: false };
    },
    async removeItem() {},
    async listKeys() {
      return [];
    },
  };
  const store = { ...flaky, isFullyDegraded: () => false, degradedKeys: () => [] };
  const result = await migrateFromLocalStorage(store, ls);
  assert.equal(result.ran, true);
  assert.ok(result.failures.includes('metropolis.savepoint.0'));
  assert.ok(result.keysCopied.includes('metropolis.journal'));
  assert.ok(calls >= 2);
});

test('mirrorKeyFromLocalStorage: copies present keys, removes absent keys from the mirror, never throws', async () => {
  const ls = memLocalStorage({ 'metropolis.journal': 'entries-payload' });
  const store = createSaveStore(memoryKVStoreAsIdb());
  assert.equal(await mirrorKeyFromLocalStorage(store, ls, 'metropolis.journal'), true);
  assert.equal(await store.getItem('metropolis.journal'), 'entries-payload');

  ls.removeItem('metropolis.journal');
  assert.equal(await mirrorKeyFromLocalStorage(store, ls, 'metropolis.journal'), true);
  assert.equal(await store.getItem('metropolis.journal'), null);
});

test('mirrorSaveCheckpoint: crash-consistency ordering — savepoint slots mirror BEFORE the journal, and a savepoint mirror failure skips the journal mirror entirely', async () => {
  const ls = memLocalStorage({
    'metropolis.savepoint.0': 'sp0',
    'metropolis.savepoint.1': 'sp1',
    'metropolis.journal': 'journal-payload',
  });

  // Case A: everything succeeds — both savepoints AND the journal land in the store.
  const goodStore = createSaveStore(memoryKVStoreAsIdb());
  const goodResult = await mirrorSaveCheckpoint(goodStore, ls, { savepointSlots: 2, journalKey: 'metropolis.journal' });
  assert.equal(goodResult.savepointsOk, true);
  assert.equal(goodResult.journalOk, true);
  assert.equal(await goodStore.getItem('metropolis.journal'), 'journal-payload');

  // Case B: slot 1's mirror write fails (simulated) -> the journal mirror must
  // NEVER be attempted, so the durable store never holds a journal that has
  // moved past a savepoint boundary it doesn't itself have.
  const order = [];
  const flaky = {
    async getItem(key) {
      order.push(`get:${key}`);
      return null;
    },
    async setItem(key, value) {
      order.push(`set:${key}`);
      if (key === 'metropolis.savepoint.1') return { ok: false, quota: true, error: 'simulated', degraded: false };
      return { ok: true, quota: false, degraded: false };
    },
    async removeItem() {},
    async listKeys() {
      return [];
    },
  };
  const flakyStore = { ...flaky, isFullyDegraded: () => false, degradedKeys: () => [] };
  const badResult = await mirrorSaveCheckpoint(flakyStore, ls, { savepointSlots: 2, journalKey: 'metropolis.journal' });
  assert.equal(badResult.savepointsOk, false);
  assert.equal(badResult.journalOk, false);
  assert.equal(order.includes('set:metropolis.journal'), false, 'journal mirror must be skipped when a savepoint mirror fails');
});

function memoryKVStoreAsIdb() {
  return memoryKVStore();
}
