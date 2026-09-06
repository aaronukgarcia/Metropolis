// bug-781-idb-large-city.test.tsx — BUG-781 (P1): does Aaron's capture-13
// city actually survive a save/reload today?
//
// CONTEXT: Aaron's dogfood capture 13 (9.48M citizens, 38,251 buildings,
// debug JSON 15.8MB at E:\gotmp\dbg13.json) carries 'Save failed (storage
// quota)' x11 and 'City loaded in memory; session persist failed (quota)'
// x4 in its error ring. Since 2bd94ad (FEAT-2326609780) IndexedDB is the
// PRIMARY savepoint store, with localStorage as a fast, practically
// 5-10MB-quota-constrained mirror. This file proves, against the REAL
// production code (never a reimplementation):
//
//   (1) IDB ROUND TRIP AT SCALE: a savepoint the size of a ~38k-building,
//       ~15MB-serialised city actually persists into and reloads from the
//       durable IndexedDB store byte-identically — buildings array length,
//       content, and a content hash all survive the trip. (fake-indexeddb's
//       backing Map has no size ceiling, matching real IndexedDB's own
//       quota-managed-against-disk behaviour, unlike localStorage's tight
//       per-origin cap — this is exactly the property BUG-781 needs proven,
//       not assumed.)
//   (2) MIRROR-ONLY QUOTA -> WARN, NOT "SAVE FAILED": when ONLY the
//       localStorage mirror fails on quota (the exact capture-13 shape —
//       localStorage cannot hold a ~15MB blob), the recorded message must
//       NOT say the city is not being saved; it must say the durable
//       IndexedDB copy landed, at warn severity, under MET-V884.
//   (3) PRIMARY (IDB) FAILURE STAYS LOUD: when the durable store ALSO fails
//       (both stores wedged — the genuine "the city really is not saved"
//       case), the original loud refusal wording is unchanged.
//
// See idb-primary-boot.test.tsx for the general IDB-primary-boot machinery
// this file re-uses (installJsdom/loadFakeIndexedDBFactory/waitFor idioms).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { JSDOM } from 'jsdom';

async function loadFakeIndexedDBFactory(): Promise<(backing?: Map<string, Map<string, string>>, opts?: any) => any> {
  const specifier = './helpers/fakeIndexedDB.mjs';
  const mod: any = await import(specifier);
  return mod.createFakeIndexedDBFactory;
}

function installJsdom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  return dom;
}

/**
 * A capture-13-shaped buildings array: ~38,251 entries with a synthetic
 * filler field calibrated so the WHOLE savepoint's JSON serialises to
 * roughly 15-16MB, matching the real debug JSON's measured size
 * (E:\gotmp\dbg13.json, 15,825,994 bytes). The filler is deliberately
 * synthetic (this is a save-STORAGE test, not a game-mechanics test) —
 * what matters is the byte volume the save/reload path must move, not the
 * building semantics.
 */
function bigCityBuildings(count: number, fillerLen: number) {
  const filler = 'x'.repeat(fillerLen);
  const buildings: any[] = [];
  const mapW = 624;
  for (let i = 0; i < count; i++) {
    buildings.push({
      id: i + 1,
      spec: i % 7 === 0 ? 'res_tower' : i % 5 === 0 ? 'com_office' : 'res_hut',
      x: i % mapW,
      y: Math.floor(i / mapW),
      builtTick: i,
      capacityTier: i % 4,
      note: filler,
    });
  }
  return buildings;
}

function hashOf(value: unknown): string {
  return createHash('sha256').update(JSON.stringify(value)).digest('hex');
}

test('BUG-781 (1): a ~15MB, 38,251-building savepoint round-trips byte-identically through the durable IndexedDB store', async () => {
  const dom = installJsdom();
  try {
    const factory = await loadFakeIndexedDBFactory();
    (globalThis as any).indexedDB = factory(new Map());
    const { resetSaveStoreForTests, getDefaultSaveStore } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const buildings = bigCityBuildings(38_251, 300);
    const savepoint = {
      snapshotTick: 12345,
      savedAt: new Date().toISOString(),
      saveSeq: 1,
      lineageId: 'capture-13-repro',
      buildVersion: 'test',
      camera: null,
      journalTail: [],
      snapshot: {
        tick: 12345,
        population: 9_480_000,
        buildings,
      },
    };
    const encoded = JSON.stringify(savepoint);
    const sizeMB = encoded.length / (1024 * 1024);
    assert.ok(sizeMB > 10, `test setup: the synthetic savepoint must actually be in the multi-MB range this bug is about (got ${sizeMB.toFixed(2)}MB)`);

    const store = getDefaultSaveStore();
    const key = 'metropolis.savepoint.capture-13-repro.0';

    const t0 = Date.now();
    const writeResult = await store.setItem(key, encoded);
    const writeMs = Date.now() - t0;
    assert.equal(writeResult.ok, true, `the durable store must accept a ~${sizeMB.toFixed(1)}MB savepoint — IndexedDB (unlike localStorage) has no practical per-origin size ceiling`);
    assert.equal(writeResult.degraded, false, 'must land in real IndexedDB, not the in-memory degraded fallback');

    const t1 = Date.now();
    const readBack = await store.getItem(key);
    const readMs = Date.now() - t1;
    assert.ok(readBack !== null, 'the durable store must return the savepoint it just accepted');

    // BYTE-IDENTICAL, not just "parses similarly": exact string equality
    // proves no truncation, no re-encoding drift, nothing silently dropped.
    assert.equal(readBack, encoded, 'the round-tripped bytes must be EXACTLY what was written — no truncation at this size');

    const restored = JSON.parse(readBack!);
    assert.equal(restored.snapshot.buildings.length, 38_251, 'building count must survive the round trip exactly');
    assert.equal(hashOf(restored.snapshot.buildings), hashOf(buildings), 'the buildings array content hash must match exactly — byte-identical, not just same length');

    // Not a hard perf gate (BUG-781 files a separate P2 for the main-thread
    // cost class if this is large) — just a recorded data point.
    console.log(`[BUG-781] durable write of ${sizeMB.toFixed(2)}MB took ${writeMs}ms, read took ${readMs}ms`);
  } finally {
    dom.window.close();
  }
});

test('BUG-781 (1b): the large savepoint survives a fresh "reload" (new SaveStore instance, same IndexedDB backing map)', async () => {
  const dom = installJsdom();
  try {
    const backing = new Map<string, Map<string, string>>();
    const factory = await loadFakeIndexedDBFactory();
    (globalThis as any).indexedDB = factory(backing);
    const { resetSaveStoreForTests, getDefaultSaveStore } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();

    const buildings = bigCityBuildings(38_251, 300);
    const savepoint = { snapshotTick: 1, savedAt: new Date().toISOString(), saveSeq: 1, snapshot: { buildings } };
    const encoded = JSON.stringify(savepoint);
    const key = 'metropolis.savepoint.reload-repro.0';

    const store1 = getDefaultSaveStore();
    assert.equal((await store1.setItem(key, encoded)).ok, true);

    // "Reload": brand-new SaveStore singleton (resetSaveStoreForTests), same
    // underlying IndexedDB database (real reload behaviour — IndexedDB is
    // on-disk, unlike the in-memory degraded fallback).
    resetSaveStoreForTests();
    (globalThis as any).indexedDB = factory(backing);
    const store2 = getDefaultSaveStore();
    const readBack = await store2.getItem(key);
    assert.equal(readBack, encoded, 'a fresh SaveStore instance reading the SAME IndexedDB database must see the exact bytes a prior instance wrote');
    assert.equal(JSON.parse(readBack!).snapshot.buildings.length, 38_251);
  } finally {
    dom.window.close();
  }
});

test('BUG-781 (2): mirror-only quota (localStorage full, IndexedDB fine) -> a warn-severity MET-V884 message saying the city IS saved, never "Save failed"', async () => {
  const dom = installJsdom();
  try {
    const factory = await loadFakeIndexedDBFactory();
    (globalThis as any).indexedDB = factory(new Map());
    const { resetSaveStoreForTests } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();
    const { recentErrors } = await import('../src/sim/backend.ts');
    const { mirrorAfterPersist } = await import('../src/sim/store.tsx');

    const errorsBefore = recentErrors().length;
    const savepoint: any = { snapshotTick: 1, savedAt: new Date().toISOString(), saveSeq: 1, lineageId: 'mirror-only-quota' };
    // `persisted:false, reason:'storage-error'` is exactly what
    // `persistSavepointWithReason` returns when localStorage's OWN
    // `setItem` throws QuotaExceededError — the capture-13 shape.
    const result = await mirrorAfterPersist(false, savepoint, 'storage-error');
    assert.equal(result.ok, true, 'the durable (IndexedDB) leg must succeed when only localStorage is wedged');

    const added = recentErrors().slice(0, Math.max(0, recentErrors().length - errorsBefore));
    const durableSaved = added.filter((e: any) => e.code === 'MET-V884');
    assert.equal(durableSaved.length, 1, `exactly one MET-V884 durable-rescue message must be recorded: ${JSON.stringify(added.map((e: any) => e.msg))}`);
    assert.match(durableSaved[0].msg, /saved/i);
    assert.doesNotMatch(durableSaved[0].msg, /NOT being saved/i);
    assert.equal(durableSaved[0].type, 'app');

    const falseFailures = added.filter((e: any) => /Save failed/i.test(e.msg) || /NOT being saved/i.test(e.msg));
    assert.equal(falseFailures.length, 0, `no message may claim the city was not saved when the durable copy holds it: ${JSON.stringify(falseFailures.map((e: any) => e.msg))}`);
  } finally {
    dom.window.close();
  }
});

test('BUG-781 (3): primary (durable/IndexedDB) failure keeps the loud refusal — the durable store getting the exception path recorded, never silently swallowed as a warn', async () => {
  const dom = installJsdom();
  try {
    const factory = await loadFakeIndexedDBFactory();
    // Every write to the durable store fails (a huge count — this savepoint
    // write plus the freshness-gate's own preceding read/writes never
    // succeed either).
    (globalThis as any).indexedDB = factory(new Map(), { failNextWrites: { count: 1000, error: () => { const e: any = new Error('QuotaExceededError: disk full'); e.name = 'QuotaExceededError'; return e; } } });
    const { resetSaveStoreForTests } = await import('../src/sim/saveStore.ts');
    resetSaveStoreForTests();
    const { recentErrors } = await import('../src/sim/backend.ts');
    const { mirrorAfterPersist } = await import('../src/sim/store.tsx');

    const errorsBefore = recentErrors().length;
    const savepoint: any = { snapshotTick: 1, savedAt: new Date().toISOString(), saveSeq: 1, lineageId: 'primary-failure' };
    const result = await mirrorAfterPersist(false, savepoint, 'storage-error');
    // BUG-781 finding: `createSaveStore` (saveStore.ts) NEVER lets a write
    // reject — a failing IndexedDB `put` transparently degrades to an
    // in-memory overlay and still resolves `ok:true`. `mirrorAfterPersist`
    // must see through that: `result.degraded` means the write is NOT
    // reload-durable, so `result.ok` here must be false even though the
    // underlying store call itself technically "succeeded".
    assert.equal(result.ok, false, 'when the durable store degrades to memory-only, the outcome must NOT be reported as a durable success');
    assert.equal(result.degraded, true, 'the degraded flag must say WHY: IndexedDB itself failed, not an ordinary freshness refusal');

    const added = recentErrors().slice(0, Math.max(0, recentErrors().length - errorsBefore));
    // The genuine "IndexedDB is failing" case is ALREADY reported loudly by
    // `createSaveStore` itself (MET-V859, saveStore.ts) the moment the write
    // degrades — this is the loud refusal BUG-781 says must stay for a
    // genuine primary failure. `mirrorAfterPersist` must not ALSO claim a
    // durable save landed on top of that.
    const durableFailureWarnings = added.filter((e: any) => e.code === 'MET-V859');
    assert.ok(durableFailureWarnings.length > 0, `MET-V859 (durable save write failed, falling back to in-memory) must still fire loudly: ${JSON.stringify(added.map((e: any) => e.msg))}`);
    // And no MET-V884 "saved durably" message may appear — nothing actually
    // reached durable storage; only the ephemeral in-memory overlay did.
    const wronglyClaimedSaved = added.filter((e: any) => e.code === 'MET-V884');
    assert.equal(wronglyClaimedSaved.length, 0, 'must never claim a durable-rescue save when the durable write itself only degraded to memory');
  } finally {
    dom.window.close();
  }
});
