// bug1181-savepoint-index.test.mjs - BUG-1181: listCitiesAll must not fully decode every IndexedDB
// savepoint on each open. A metadata-only index (fingerprint-validated) serves unchanged saves with
// ZERO decodes; changed/missing/malformed entries fall back to one decode and an upsert; the rows
// listed are identical with and without the index.
import { test, describe } from 'node:test';
import assert from 'node:assert/strict';

import { listCitiesAll, SAVEPOINT_INDEX_KEY, savepointFingerprint } from '../src/sim/saveStore.ts';
import { decodeSavepointBytes, createSavepoint } from '../src/sim/replay.ts';
import { initialState } from '../src/sim/engine.ts';

const NOW = new Date(Date.UTC(2026, 10, 1, 13, 0));

function fakeIdb() {
  const m = new Map();
  const writes = [];
  return {
    m, writes,
    getItem: async (k) => (m.has(k) ? m.get(k) : null),
    setItem: async (k, v) => { m.set(k, v); writes.push(k); return { ok: true, quota: false, degraded: false }; },
    removeItem: async (k) => void m.delete(k),
    listKeys: async (p) => [...m.keys()].filter((k) => k.startsWith(p)).sort(),
    isFullyDegraded: () => false,
    degradedKeys: () => [],
  };
}
const emptyLocal = { getItem: () => null, setItem() {}, removeItem() {}, key: () => null, length: 0 };

function put(idb, lineage, tick, minute, extra = {}) {
  const s = { ...initialState(), tick };
  const sp = { ...createSavepoint(s, [], new Date(Date.UTC(2026, 10, 1, 12, minute)), 'v0.test', null), ...extra };
  idb.m.set(`metropolis.savepoint.${lineage}.idbOnly`, JSON.stringify(sp));
  return sp;
}
function counter() {
  const c = { n: 0, fn: (raw) => { c.n++; return decodeSavepointBytes(raw); } };
  return c;
}
const strip = (rows) => JSON.stringify(rows);

describe('BUG-1181 savepoint metadata index', () => {
  test('1. first listing decodes each save once and writes the index once; second decodes none', async () => {
    const idb = fakeIdb();
    put(idb, 'aaa', 100, 1); put(idb, 'bbb', 200, 2); put(idb, 'ccc', 300, 3);
    const c1 = counter();
    const rows1 = await listCitiesAll(emptyLocal, idb, 'aaa', NOW, c1.fn);
    assert.equal(c1.n, 3);
    assert.equal(idb.writes.filter((k) => k === SAVEPOINT_INDEX_KEY).length, 1);
    const c2 = counter();
    const rows2 = await listCitiesAll(emptyLocal, idb, 'aaa', NOW, c2.fn);
    assert.equal(c2.n, 0, 'warm index: zero full decodes');
    assert.equal(idb.writes.filter((k) => k === SAVEPOINT_INDEX_KEY).length, 1, 'no rewrite when unchanged');
    assert.equal(rows1.length, 3);
    assert.equal(strip(rows2), strip(rows1), 'rows identical with and without the index');
  });

  test('2. a changed save re-decodes exactly that one and the row reflects the new tick', async () => {
    const idb = fakeIdb();
    put(idb, 'aaa', 100, 1); put(idb, 'bbb', 200, 2);
    await listCitiesAll(emptyLocal, idb, null, NOW, counter().fn);
    put(idb, 'bbb', 999, 5);
    const c = counter();
    const rows = await listCitiesAll(emptyLocal, idb, null, NOW, c.fn);
    assert.equal(c.n, 1);
    assert.equal(rows.find((r) => r.lineageId === 'bbb').snapshotTick, 999);
  });

  test('3. a deleted save is pruned from the index; a corrupt index is ignored and rebuilt', async () => {
    const idb = fakeIdb();
    put(idb, 'aaa', 100, 1); put(idb, 'bbb', 200, 2);
    await listCitiesAll(emptyLocal, idb, null, NOW, counter().fn);
    idb.m.delete('metropolis.savepoint.bbb.idbOnly');
    await listCitiesAll(emptyLocal, idb, null, NOW, counter().fn);
    assert.deepEqual(Object.keys(JSON.parse(idb.m.get(SAVEPOINT_INDEX_KEY)).entries), ['metropolis.savepoint.aaa.idbOnly']);
    idb.m.set(SAVEPOINT_INDEX_KEY, '{not json');
    const c = counter();
    const rows = await listCitiesAll(emptyLocal, idb, null, NOW, c.fn);
    assert.equal(c.n, 1);
    assert.equal(rows.length, 1);
    assert.equal(JSON.parse(idb.m.get(SAVEPOINT_INDEX_KEY)).v, 1);
  });

  test('4. a tampered index entry (wrong fingerprint) is not trusted', async () => {
    const idb = fakeIdb();
    put(idb, 'aaa', 100, 1);
    await listCitiesAll(emptyLocal, idb, null, NOW, counter().fn);
    const ix = JSON.parse(idb.m.get(SAVEPOINT_INDEX_KEY));
    const k = 'metropolis.savepoint.aaa.idbOnly';
    ix.entries[k].snapshotTick = 123456; ix.entries[k].fp = '0:0';
    idb.m.set(SAVEPOINT_INDEX_KEY, JSON.stringify(ix));
    const c = counter();
    const rows = await listCitiesAll(emptyLocal, idb, null, NOW, c.fn);
    assert.equal(c.n, 1);
    assert.equal(rows[0].snapshotTick, 100);
  });

  test('5. an undecodable save is not indexed and not listed; index key never mistaken for a savepoint', async () => {
    const idb = fakeIdb();
    put(idb, 'aaa', 100, 1);
    idb.m.set('metropolis.savepoint.zzz.idbOnly', 'garbage');
    const rows = await listCitiesAll(emptyLocal, idb, null, NOW, (raw) => { try { return decodeSavepointBytes(raw); } catch { return null; } });
    assert.deepEqual(rows.map((r) => r.lineageId), ['aaa']);
    assert.equal(Object.keys(JSON.parse(idb.m.get(SAVEPOINT_INDEX_KEY)).entries).length, 1);
    assert.ok(!SAVEPOINT_INDEX_KEY.startsWith('metropolis.savepoint.'));
  });

  test('6. scratch flag and building count survive the index round trip', async () => {
    const idb = fakeIdb();
    put(idb, 'aaa', 100, 1, { scratch: true });
    const cold = await listCitiesAll(emptyLocal, idb, null, NOW, counter().fn);
    const warm = await listCitiesAll(emptyLocal, idb, null, NOW, counter().fn);
    assert.equal(warm[0].scratch, true);
    assert.equal(typeof warm[0].buildingCount, 'number');
    assert.equal(strip(warm), strip(cold));
    assert.equal(typeof savepointFingerprint('x'), 'string');
  });
});
