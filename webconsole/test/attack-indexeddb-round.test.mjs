// attack-indexeddb-round.test.mjs — GR#23 independent destructive round on
// FEAT-2326609778 (IndexedDB saves + Export/Import City). Attacker is NOT the
// author. Findings below are proven with executable tests, not narrative.
//
// SCOPE: this file targets the two structural risks the round judged most
// consequential — (1) the mirror's honesty (is it read back by ANY shipped
// code) and (2) crash-consistency under concurrent mirror calls (the ordering
// contract inside ONE mirrorSaveCheckpoint call is solid — see the author's
// own saveStore.test.mjs — but nothing serializes TWO overlapping calls
// against each other). Everything else in the brief (migration idempotency,
// import validation, degradation loud-once) was verified by direct code
// reading + running the author's existing suite and is reported in the BOW
// verdict note rather than re-proven here, to avoid duplicating coverage that
// already exists and passes.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createSaveStore,
  mirrorKeyFromLocalStorage,
  mirrorSaveCheckpoint,
  memoryKVStore,
} from '../src/sim/saveStore.ts';
import { readAllSavepoints, mostRecentSavepoint, restoreFromSavepoint } from '../src/sim/replay.ts';

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

// ---------------------------------------------------------------------------
// FINDING 1 (structural, HIGH signal, matches the brief's suspicion): the
// mirror is genuinely write-only. Boot (replay.ts) takes a synchronous
// StorageLike and NEVER imports or references saveStore.ts. If localStorage
// is cleared/evicted but the IndexedDB mirror still holds the bytes, boot
// produces a FRESH city — the mirror's data is inert. There is also NO manual
// recovery path: importCity() requires a FILE the player exported earlier: it
// cannot read the IndexedDB mirror either (grep confirms getDefaultSaveStore()
// has exactly 4 call sites in store.tsx, all of them writes: mirrorSaveCheckpoint,
// mirrorNamedSave's mirrorKeyFromLocalStorage calls, mirrorPreWipeArchive, and
// migrateFromLocalStorage). This test proves the boot half of that claim
// directly: clearing localStorage while the durable store still holds a
// savepoint produces an empty restore, with no fallback to the durable store.
// ---------------------------------------------------------------------------
test('FINDING 1: mirror is write-only — clearing localStorage loses the save even though the IndexedDB mirror still has it (no restore path reads it)', async () => {
  const ls = memLocalStorage();
  const durable = createSaveStore(memoryKVStore());

  // Simulate a savepoint that was written+mirrored earlier in the session.
  const savepointJson = JSON.stringify({
    tick: 12345,
    schemaVersion: 1,
    buildVersion: 'v1.2.3',
    savedAt: new Date().toISOString(),
    state: { citizens: 999, treasury: 42 },
  });
  ls.setItem('metropolis.savepoint.0', savepointJson);
  await mirrorKeyFromLocalStorage(durable, ls, 'metropolis.savepoint.0');
  assert.equal(await durable.getItem('metropolis.savepoint.0'), savepointJson, 'sanity: the mirror really has the data');

  // Now simulate what BUG-617/the quota incident actually looks like: the
  // browser evicts/clears localStorage (quota pressure, private-mode
  // teardown, "clear site data") but IndexedDB, with its far larger ceiling,
  // survives.
  ls._map.clear();

  // Boot reads ONLY localStorage (replay.ts's real, unmodified functions —
  // zero diff against main this round, confirmed via git diff).
  const savepoints = readAllSavepoints(ls);
  assert.equal(savepoints.length, 0, 'boot sees zero savepoints once localStorage is cleared');
  assert.equal(mostRecentSavepoint(savepoints), null);
  const restore = restoreFromSavepoint(ls);
  assert.ok(restore.savepoint == null, 'restoreFromSavepoint has nothing to offer — the durable IndexedDB copy is never consulted');

  // And the durable copy is still sitting right there, provably intact and
  // provably unreachable by any boot code path.
  assert.equal(await durable.getItem('metropolis.savepoint.0'), savepointJson, 'the mirror STILL has the save — it is just never read back');
});

// ---------------------------------------------------------------------------
// FINDING 2 (crash-consistency, MEDIUM signal): mirrorSaveCheckpoint's
// documented contract — "savepoints mirror before the journal; a savepoint
// failure skips the journal" — holds within ONE call (author's test proves
// it). But nothing serializes two OVERLAPPING calls against each other. Two
// mirror cycles firing close together (autosave interval tick racing an
// explicit Save, or a savepoint-rotation racing a Save As) can interleave so
// that the OLDER read wins the LAST write, leaving the durable mirror holding
// a stale value even though localStorage (the authoritative copy) has the
// fresh one. Because finding 1 shows the mirror is not read back by anything
// today, this is currently inert too — but it is exactly the kind of bug that
// would surface silently the moment a restore-from-mirror path (the
// documented "natural next increment") gets built, so it is worth fixing
// before that lands rather than after.
// ---------------------------------------------------------------------------
test('FINDING 2: two overlapping mirrorKeyFromLocalStorage calls for the SAME key can resolve out of freshness order, leaving the durable store stale', async () => {
  const ls = memLocalStorage({ 'metropolis.journal': 'STALE-v1' });

  // A durable store whose setItem for 'STALE-v1' is deliberately delayed
  // longer than the one for 'FRESH-v2' — modelling a slow/backed-up
  // IndexedDB transaction queue under real browser load, which is exactly
  // the condition a big autosave (BUG-617-scale city) creates.
  const applied = [];
  const raw = {
    async getItem() {
      return null;
    },
    async setItem(key, value) {
      const delay = value === 'STALE-v1' ? 20 : 0;
      await new Promise((r) => setTimeout(r, delay));
      applied.push(value);
      return { ok: true, quota: false, degraded: false };
    },
    async removeItem() {},
    async listKeys() {
      return [];
    },
  };
  const store = { ...raw, isFullyDegraded: () => false, degradedKeys: () => [] };

  // Call A reads the stale value and starts mirroring it (slow write).
  const callA = mirrorKeyFromLocalStorage(store, ls, 'metropolis.journal');
  // Before A's write lands, the player saves again — localStorage now holds
  // the fresh value, and a second mirror call starts (fast write).
  ls.setItem('metropolis.journal', 'FRESH-v2');
  const callB = mirrorKeyFromLocalStorage(store, ls, 'metropolis.journal');

  await Promise.all([callA, callB]);

  // Both calls individually report success...
  assert.deepEqual(applied.length, 2);
  // ...but the LAST one to physically land wins the key, and it was the
  // STALE read, not the fresh one. The durable mirror now disagrees with
  // localStorage (the authoritative source) for a key that mirrorSaveCheckpoint's
  // own contract says must never trail a savepoint boundary.
  assert.equal(applied[applied.length - 1], 'STALE-v1', 'demonstrates the interleaving: the stale write physically completed last');
  // This assertion is the actual defect surface: today it fails harmlessly
  // (nothing reads the mirror — Finding 1), but it is a real staleness bug
  // in the mirror's own internal consistency, independent of whether
  // anything currently consumes it.
});

test('FINDING 2b (regression-shaped): mirrorSaveCheckpoint\'s documented ordering (savepoints-before-journal) is per-call only — a second call started mid-flight is not blocked or queued behind the first', async () => {
  const ls = memLocalStorage({
    'metropolis.savepoint.0': 'sp-a',
    'metropolis.journal': 'journal-a',
  });
  const inFlight = { count: 0, maxConcurrent: 0 };
  const raw = {
    async getItem() {
      return null;
    },
    async setItem(_key, value) {
      inFlight.count++;
      inFlight.maxConcurrent = Math.max(inFlight.maxConcurrent, inFlight.count);
      await new Promise((r) => setTimeout(r, 5));
      inFlight.count--;
      return { ok: true, quota: false, degraded: false };
    },
    async removeItem() {},
    async listKeys() {
      return [];
    },
  };
  const store = { ...raw, isFullyDegraded: () => false, degradedKeys: () => [] };

  // Fire two mirrorSaveCheckpoint calls back-to-back with no await between
  // them — exactly what happens if an autosave interval fires while a manual
  // Save's mirror is still in flight.
  const p1 = mirrorSaveCheckpoint(store, ls, { savepointSlots: 1, journalKey: 'metropolis.journal' });
  ls.setItem('metropolis.savepoint.0', 'sp-b');
  ls.setItem('metropolis.journal', 'journal-b');
  const p2 = mirrorSaveCheckpoint(store, ls, { savepointSlots: 1, journalKey: 'metropolis.journal' });

  await Promise.all([p1, p2]);

  // No serialization: both calls' writes were in flight simultaneously
  // (proves the two mirror cycles are NOT mutually exclusive), which is the
  // structural precondition for Finding 2's staleness race to occur on any
  // checkpoint field, not just a hand-picked single-key repro.
  assert.ok(inFlight.maxConcurrent >= 2, `expected concurrent in-flight writes across the two mirrorSaveCheckpoint calls, got max concurrency ${inFlight.maxConcurrent}`);
});

// ---------------------------------------------------------------------------
// FINDING 3 (confirms an author-disclosed non-finding — verification, not a
// new defect): migrateFromLocalStorage is safe under a StrictMode-style
// double-mount. Both calls race to read SAVE_STORE_MIGRATED_KEY, but the copy
// itself is idempotent (same source, same destination, last-write-wins on the
// SAME bytes) so no data corruption results — only harmless duplicate work.
// ---------------------------------------------------------------------------
test('FINDING 3 (verification): concurrent migrateFromLocalStorage calls (StrictMode double-mount) do not corrupt data, only duplicate work', async () => {
  const { migrateFromLocalStorage } = await import('../src/sim/saveStore.ts');
  const ls = memLocalStorage({ 'metropolis.savepoint.0': 'city-payload' });
  const store = createSaveStore(memoryKVStore());

  const [r1, r2] = await Promise.all([migrateFromLocalStorage(store, ls), migrateFromLocalStorage(store, ls)]);
  // Both may see "not yet migrated" (race on the flag read) and both run —
  // that's the duplicate-work case this test is checking is HARMLESS.
  assert.equal(await store.getItem('metropolis.savepoint.0'), 'city-payload');
  assert.equal(r1.ran || r2.ran, true);
});

// ---------------------------------------------------------------------------
// FINDING 4 (verification): a hostile/junk import never throws uncaught —
// decode() degrades to raw text, JSON.parse inside parseGameSave (unchanged,
// zero diff this round) throws the registry-sourced MET-V850/etc, which the
// author's importCity() catches and reports. Proven directly against the
// real decode() + parseGameSave() pipeline the app actually runs.
// ---------------------------------------------------------------------------
test('FINDING 4 (verification): junk import bytes never throw uncaught through decode()+parseGameSave()', async () => {
  const { decode } = await import('../src/sim/saveCodec.ts');
  const { parseGameSave } = await import('../src/sim/gamesave.ts');

  const junkInputs = [
    'not json at all, just garbage bytes {{{',
    'LZv1:' + 'x'.repeat(500), // malformed compressed payload, looks legit at a glance
    '{"totally": "wrong shape"}',
    '',
  ];
  for (const junk of junkInputs) {
    const decoded = decode(junk); // must never throw
    assert.equal(typeof decoded, 'string');
    let threw = false;
    let coded = false;
    try {
      parseGameSave(decoded);
    } catch (e) {
      threw = true;
      coded = typeof e === 'object' && e !== null && 'code' in e;
    }
    // Either it throws a CODED (registry-sourced) error, or it returns a
    // structured {ok:false} — both are acceptable per gamesave.ts's contract;
    // an UNCAUGHT non-coded throw would not be.
    if (threw) assert.equal(coded, true, `junk input ${JSON.stringify(junk.slice(0, 20))} threw a non-coded error`);
  }
});
