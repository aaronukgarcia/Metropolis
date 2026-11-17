import { test } from 'node:test';
import assert from 'node:assert/strict';
import { listCitiesAll, SAVEPOINT_INDEX_KEY, savepointFingerprint } from '../src/sim/saveStore.ts';
import { decodeSavepointBytes, createSavepoint } from '../src/sim/replay.ts';
import { initialState } from '../src/sim/engine.ts';
const NOW = new Date(Date.UTC(2026, 10, 1, 13, 0));
function fakeIdb(failIndexWrite = false) { const m = new Map(); return { m,
  getItem: async (k) => (m.has(k) ? m.get(k) : null),
  setItem: async (k, v) => { if (failIndexWrite && k === SAVEPOINT_INDEX_KEY) throw new Error('QuotaExceededError'); m.set(k, v); return { ok: true }; },
  removeItem: async (k) => void m.delete(k), listKeys: async (p) => [...m.keys()].filter((k) => k.startsWith(p)).sort(),
  isFullyDegraded: () => false, degradedKeys: () => [] }; }
const emptyLocal = { getItem: () => null, setItem() {}, removeItem() {}, key: () => null, length: 0 };
function sp(tick, when, extra = {}) { const s = { ...initialState(), tick, population: tick * 3 }; return { ...createSavepoint(s, [], when, 'v0.test', null), ...extra }; }
function seed(idb) {
  idb.m.set('metropolis.savepoint.aaa.idbOnly', JSON.stringify(sp(100, new Date(Date.UTC(2026, 10, 1, 12, 1)))));
  idb.m.set('metropolis.savepoint.bbb.idbOnly', JSON.stringify(sp(200, new Date(Date.UTC(2026, 10, 1, 12, 9)), { scratch: true, saveSeq: 7 })));
  idb.m.set('metropolis.savepoint.ccc.idbOnly', JSON.stringify(sp(300, new Date(Date.UTC(2020, 0, 1)))));               // stale autosave
  idb.m.set('metropolis.savepoint.ddd.idbOnly', JSON.stringify({ ...sp(400, new Date(Date.UTC(2026, 10, 1, 12, 5))), savedAt: 5 })); // invalid
  idb.m.set('metropolis.savepoint.eee.0', JSON.stringify(sp(500, new Date(Date.UTC(2026, 10, 1, 12, 7)))));
  idb.m.set('metropolis.savepoint.eee.1', JSON.stringify(sp(501, new Date(Date.UTC(2026, 10, 1, 12, 8)), { saveSeq: 2 })));
}
const cold = async (idb) => { const copy = fakeIdb(); for (const [k, v] of idb.m) if (k !== SAVEPOINT_INDEX_KEY) copy.m.set(k, v); return JSON.stringify(await listCitiesAll(emptyLocal, copy, 'aaa', NOW)); };

test('B1 warm == cold across scratch/saveSeq/stale/invalid/multi-slot', async () => {
  const idb = fakeIdb(); seed(idb);
  const c = await cold(idb);
  await listCitiesAll(emptyLocal, idb, 'aaa', NOW);
  let n = 0; const warm = JSON.stringify(await listCitiesAll(emptyLocal, idb, 'aaa', NOW, (r) => { n++; return decodeSavepointBytes(r); }));
  console.log('warm decodes', n, 'rows', warm);
  assert.equal(warm, c);
});
test('B2 same-length rewrite is re-decoded (fingerprint catches it)', async () => {
  const idb = fakeIdb(); seed(idb);
  await listCitiesAll(emptyLocal, idb, 'aaa', NOW);
  const k = 'metropolis.savepoint.aaa.idbOnly'; const old = idb.m.get(k);
  const neu = old.replace('2026-11-01T12:01', '2026-11-01T12:11'); assert.equal(neu.length, old.length); assert.notEqual(neu, old);
  idb.m.set(k, neu);
  const warm = JSON.stringify(await listCitiesAll(emptyLocal, idb, 'aaa', NOW));
  assert.equal(warm, await cold(idb)); assert.ok(warm.includes('12:11'));
});
test('B3 index write-back quota failure: rows still correct, no throw, next call decodes again', async () => {
  const idb = fakeIdb(true); seed(idb);
  const a = JSON.stringify(await listCitiesAll(emptyLocal, idb, 'aaa', NOW));
  assert.equal(a, await cold(idb)); assert.equal(idb.m.has(SAVEPOINT_INDEX_KEY), false);
});
test('B4 corrupt index variants never change rows', async () => {
  for (const bad of ['{', 'null', '[]', '{"v":2,"entries":{}}', '{"v":1,"entries":[]}', '{"v":1,"entries":{"metropolis.savepoint.aaa.idbOnly":{"fp":1}}}']) {
    const idb = fakeIdb(); seed(idb); idb.m.set(SAVEPOINT_INDEX_KEY, bad);
    assert.equal(JSON.stringify(await listCitiesAll(emptyLocal, idb, 'aaa', NOW)), await cold(idb), bad);
  }
});
test('B5 forged entry with the REAL fingerprint is trusted (index tamper changes listing order only)', async () => {
  const idb = fakeIdb(); seed(idb);
  const k = 'metropolis.savepoint.aaa.idbOnly';
  idb.m.set(SAVEPOINT_INDEX_KEY, JSON.stringify({ v: 1, entries: { [k]: { fp: savepointFingerprint(idb.m.get(k)), savedAt: '2099-01-01T00:00:00.000Z', snapshotTick: 1, scratch: false } } }));
  const rows = await listCitiesAll(emptyLocal, idb, 'aaa', NOW);
  console.log('forged first row', JSON.stringify(rows[0]));
  assert.equal(rows[0].savedAt, '2099-01-01T00:00:00.000Z');
});
