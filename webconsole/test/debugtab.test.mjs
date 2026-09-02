// debugtab.test.mjs — FEAT-1972079885: fork-reconciliation debug-tab logic.
//
// Covers the three pure modules the DebugTab consumes:
//   commitqueue.ts  — the ASM-453 client-side snapshot commit queue
//   backend.ts      — errorListModel (the "Errors captured" display model)
//   debugactions.ts — the DEV-gated cheat buttons
//
// Run with `npm test` (node --test). Every assertion goes RED under a real
// mutation, e.g. flip enqueueCommit's `slice(-QUEUE_CAP)` to `slice(0,
// QUEUE_CAP)` and the overflow tests fail; make debugActions ignore isDev
// and the gating test fails.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  QUEUE_KEY,
  QUEUE_CAP,
  QUEUE_BYTE_BUDGET,
  readQueue,
  pendingCount,
  enqueueCommit,
  compactPayload,
} from '../src/sim/commitqueue.ts';
import { errorListModel } from '../src/sim/backend.ts';
import {
  debugActions,
  DEBUG_FUNDS_GRANT,
  DEBUG_XP_GRANT,
} from '../src/sim/debugactions.ts';
import { initialState, reducer } from '../src/sim/engine.ts';

/** Minimal in-memory StorageLike — what a browser localStorage does for us. */
function memStorage(seed = {}) {
  const m = new Map(Object.entries(seed));
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
  };
}

// ---------- commit queue (ASM-453) ----------

test('queue: empty storage counts zero', () => {
  const s = memStorage();
  assert.equal(pendingCount(s), 0);
  assert.deepEqual(readQueue(s), []);
});

test('queue: enqueue appends, returns the new length, and preserves the entry', () => {
  const s = memStorage();
  const r1 = enqueueCommit(s, 'DBG-A', { tick: 1 }, '2026-08-26T10:00:00.000Z');
  assert.equal(r1.ok, true);
  assert.equal(r1.length, 1);
  const r2 = enqueueCommit(s, 'DBG-B', { tick: 2 }, '2026-08-26T10:00:15.000Z');
  assert.equal(r2.ok, true);
  assert.equal(r2.length, 2);
  assert.equal(pendingCount(s), 2);
  const q = readQueue(s);
  // BUG-607: every queued payload is compacted (perfHud stamped null; this
  // trivial payload has no buildings/sim to strip further).
  assert.deepEqual(q[0], { id: 'DBG-A', at: '2026-08-26T10:00:00.000Z', payload: { tick: 1, perfHud: null } });
  assert.equal(q[1].id, 'DBG-B', 'array order is commit order (oldest first)');
});

test('queue: persists under QUEUE_KEY as JSON (survives a "reload")', () => {
  const s = memStorage();
  enqueueCommit(s, 'DBG-P', { funds: 9 }, '2026-08-26T11:00:00.000Z');
  const raw = s.getItem(QUEUE_KEY);
  assert.ok(raw, 'queue written to the stable ASM-453 key');
  // A fresh reader over the SAME persisted string (what a reload does) sees it.
  const reloaded = memStorage({ [QUEUE_KEY]: raw });
  assert.equal(pendingCount(reloaded), 1);
  assert.equal(readQueue(reloaded)[0].id, 'DBG-P');
});

test('queue: caps at QUEUE_CAP keeping the NEWEST entries', () => {
  const s = memStorage();
  for (let i = 1; i <= QUEUE_CAP + 5; i++) {
    enqueueCommit(s, `DBG-${i}`, i, '2026-08-26T12:00:00.000Z');
  }
  const q = readQueue(s);
  assert.equal(q.length, QUEUE_CAP, 'never exceeds the cap');
  assert.equal(q[0].id, 'DBG-6', 'oldest 5 dropped');
  assert.equal(q[q.length - 1].id, `DBG-${QUEUE_CAP + 5}`, 'newest kept');
});

test('queue: corrupt or non-array storage degrades to empty, then recovers', () => {
  const corrupt = memStorage({ [QUEUE_KEY]: '{not json' });
  assert.equal(pendingCount(corrupt), 0);
  const wrongShape = memStorage({ [QUEUE_KEY]: '{"a":1}' });
  assert.equal(pendingCount(wrongShape), 0);
  // Enqueue over corruption starts a fresh, valid queue.
  const r = enqueueCommit(corrupt, 'DBG-R', null, '2026-08-26T13:00:00.000Z');
  assert.equal(r.ok, true);
  assert.equal(r.length, 1);
  assert.equal(readQueue(corrupt)[0].id, 'DBG-R');
});

// ---------- BUG-607: byte budget + content compaction ----------

test('compactPayload: strips buildings.list / road monitors / perfHud, keeps scalars', () => {
  const dj = {
    meta: { appVersion: '1.2.3' },
    sim: {
      tick: 42,
      roadMonitors: [{ x: 1, y: 2 }, { x: 3, y: 4 }],
      roadConnectivity: { connectedRoadTiles: [[0, 0], [0, 1]], other: 'kept' },
    },
    buildings: { count: 2, byKind: { house: 2 }, list: [{ id: 'b1' }, { id: 'b2' }] },
    flows: { income: 100, expense: 40 },
    consistency: { failures: 0, checks: [] },
    errors: [{ msg: 'boom' }],
    perfHud: { fps: 60 },
  };
  const c = compactPayload(dj);
  assert.deepEqual(c.buildings.list, [], 'building list stripped');
  assert.equal(c.buildings.count, 2, 'building count kept');
  assert.deepEqual(c.buildings.byKind, { house: 2 }, 'byKind kept');
  assert.deepEqual(c.sim.roadMonitors, [], 'road monitors stripped');
  assert.deepEqual(c.sim.roadConnectivity.connectedRoadTiles, [], 'connected tiles stripped');
  assert.equal(c.sim.roadConnectivity.other, 'kept', 'other roadConnectivity fields kept');
  assert.equal(c.sim.tick, 42, 'sim scalars kept');
  assert.equal(c.perfHud, null, 'perfHud dropped');
  assert.deepEqual(c.flows, { income: 100, expense: 40 }, 'flows kept for the future backend contract');
  assert.deepEqual(c.consistency, { failures: 0, checks: [] }, 'consistency kept');
  assert.deepEqual(c.errors, [{ msg: 'boom' }], 'errors kept');
  assert.deepEqual(c.meta, { appVersion: '1.2.3' }, 'meta kept');
});

test('compactPayload: a non-debug-json-shaped payload passes through unchanged rather than throwing', () => {
  assert.equal(compactPayload(null), null);
  assert.equal(compactPayload(42), 42);
  assert.deepEqual(compactPayload([1, 2, 3]), [1, 2, 3]);
  assert.deepEqual(compactPayload({ funds: 9 }), { funds: 9, perfHud: null });
});

test('queue: an oversized entry (still too big after compaction) is dropped with droppedOversize, never throws', () => {
  const s = memStorage();
  // Enqueue one normal entry first so we can prove it survives the drop.
  enqueueCommit(s, 'DBG-OK', { tick: 1 }, '2026-08-26T14:00:00.000Z');
  // A payload whose compacted form still exceeds QUEUE_BYTE_BUDGET on its own
  // (buildings.list gets stripped by compactPayload, but this junk lives
  // outside any recognised section, so it survives compaction untouched).
  const huge = { junk: 'x'.repeat(QUEUE_BYTE_BUDGET + 1024) };
  const r = enqueueCommit(s, 'DBG-HUGE', huge, '2026-08-26T14:01:00.000Z');
  assert.equal(r.ok, false);
  assert.equal(r.droppedOversize, true);
  // The pre-existing entry is untouched — dropping the oversize one never
  // evicted anything else, and the queue itself never grew to include it.
  const q = readQueue(s);
  assert.equal(q.length, 1);
  assert.equal(q[0].id, 'DBG-OK');
  assert.equal(r.length, 1);
});

test('queue: byte-budget eviction drops OLDEST entries first, keeping the newest that fit', () => {
  const s = memStorage();
  // Each payload is ~500KB post-JSON so a handful blow QUEUE_BYTE_BUDGET (2MB)
  // well before QUEUE_CAP (50) would ever kick in.
  const chunk = 'a'.repeat(500 * 1024);
  for (let i = 1; i <= 6; i++) {
    enqueueCommit(s, `DBG-${i}`, { chunk, i }, `2026-08-26T15:0${i}:00.000Z`);
  }
  const q = readQueue(s);
  assert.ok(q.length < 6, 'byte budget evicted at least one entry well under the 50-entry cap');
  assert.ok(q.length >= 1, 'at least the newest entry survives');
  assert.equal(q[q.length - 1].id, 'DBG-6', 'newest entry always kept');
  // Oldest-first eviction: whatever remains is a contiguous NEWEST-first suffix.
  const ids = q.map((e) => e.id);
  const expectedSuffix = ['DBG-1', 'DBG-2', 'DBG-3', 'DBG-4', 'DBG-5', 'DBG-6'].slice(6 - ids.length);
  assert.deepEqual(ids, expectedSuffix, 'surviving entries are the newest, in order');
  const raw = s.getItem(QUEUE_KEY);
  assert.ok(
    Buffer.byteLength(raw, 'utf8') <= QUEUE_BYTE_BUDGET,
    'persisted queue fits within QUEUE_BYTE_BUDGET'
  );
});

test('queue: QuotaExceededError on setItem degrades to one retry (oldest half dropped), never throws', () => {
  // A storage fake whose setItem always throws QuotaExceededError, mirroring
  // safeStorage.ts's isQuotaError detection (a plain Error with 'quota' in
  // the message is one of its recognised legacy-engine shapes).
  const store = new Map();
  let calls = 0;
  const quotaStorage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: () => {
      calls += 1;
      const e = new Error('QuotaExceededError: quota exceeded');
      e.name = 'QuotaExceededError';
      throw e;
    },
  };
  assert.doesNotThrow(() => {
    const r = enqueueCommit(quotaStorage, 'DBG-Q', { tick: 1 }, '2026-08-26T16:00:00.000Z');
    assert.equal(r.ok, false, 'gives up cleanly once retry also fails');
    assert.equal(r.length, 0, 'nothing was ever persisted');
  });
  assert.equal(calls, 2, 'exactly one retry after the first quota failure (never more)');
});

test('queue: a QuotaExceededError that recovers on the retry (oldest half dropped) reports success', () => {
  const store = new Map();
  let calls = 0;
  const flakyStorage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => {
      calls += 1;
      if (calls === 1) {
        const e = new Error('QuotaExceededError: quota exceeded');
        e.name = 'QuotaExceededError';
        throw e;
      }
      store.set(k, v);
    },
  };
  // Seed a couple of existing entries so there is something to halve.
  store.set(
    QUEUE_KEY,
    JSON.stringify([
      { id: 'DBG-OLD1', at: '2026-08-26T17:00:00.000Z', payload: 1 },
      { id: 'DBG-OLD2', at: '2026-08-26T17:01:00.000Z', payload: 2 },
    ])
  );
  const r = enqueueCommit(flakyStorage, 'DBG-NEW', { tick: 3 }, '2026-08-26T17:02:00.000Z');
  assert.equal(r.ok, true);
  assert.equal(r.quotaRecovered, true);
  assert.equal(calls, 2, 'first write failed, retry succeeded');
  const q = readQueue(flakyStorage);
  assert.equal(q.length, 2, 'oldest half of the 3-entry candidate dropped');
  assert.equal(q[q.length - 1].id, 'DBG-NEW', 'the newly-enqueued commit survives the retry');
});

test('queue: old, pre-BUG-607 fat entries (uncompacted payload) are still readable', () => {
  // Simulates a queue written by the OLD enqueueCommit, before compaction
  // existed — proves readQueue() never depended on the new shape.
  const fatEntry = {
    id: 'DBG-OLDFAT',
    at: '2026-08-20T09:00:00.000Z',
    payload: { buildings: { count: 1, list: [{ id: 'b1', huge: 'x'.repeat(1000) }] }, sim: { tick: 7 } },
  };
  const s = memStorage({ [QUEUE_KEY]: JSON.stringify([fatEntry]) });
  const q = readQueue(s);
  assert.equal(q.length, 1);
  assert.deepEqual(q[0], fatEntry, 'old uncompacted entry reads back byte-identical');
  assert.equal(pendingCount(s), 1);
});

// ---------- errors-captured display model ----------

test('errors display: empty session renders the empty state', () => {
  const m = errorListModel([]);
  assert.equal(m.empty, true);
  assert.deepEqual(m.rows, []);
});

test('errors display: populated list keeps order, messages, and local times', () => {
  const at1 = Date.UTC(2026, 7, 26, 14, 30, 5);
  const at2 = Date.UTC(2026, 7, 26, 14, 31, 9);
  const m = errorListModel([
    { at: at2, msg: 'boom two' },
    { at: at1, msg: 'boom one' },
  ]);
  assert.equal(m.empty, false);
  assert.equal(m.rows.length, 2);
  assert.equal(m.rows[0].msg, 'boom two', 'newest-first order preserved');
  assert.equal(m.rows[1].msg, 'boom one');
  assert.equal(m.rows[0].time, new Date(at2).toLocaleTimeString());
});

// ---------- DEV-gated cheat buttons ----------

test('dev buttons: production build (isDev=false) renders NO cheats', () => {
  assert.deepEqual(debugActions(false), []);
});

test('dev buttons: dev build exposes exactly the four fork-parity cheats', () => {
  const a = debugActions(true);
  assert.deepEqual(
    a.map((x) => x.id),
    ['funds', 'xp', 'fast', 'reset']
  );
  assert.equal(a.find((x) => x.id === 'funds').label, '+£10,000');
  assert.equal(a.find((x) => x.id === 'xp').label, '+500 XP');
  assert.equal(a.find((x) => x.id === 'reset').danger, true, 'reset is the only danger button');
  assert.ok(a.filter((x) => x.danger).length === 1);
});

test('dev buttons: dispatched payloads actually move the sim', () => {
  const a = Object.fromEntries(debugActions(true).map((x) => [x.id, x.action]));
  assert.deepEqual(a.funds, { type: 'debugFunds', amount: DEBUG_FUNDS_GRANT });
  assert.deepEqual(a.xp, { type: 'debugXp', amount: DEBUG_XP_GRANT });
  assert.deepEqual(a.fast, { type: 'speed', speed: 3 });
  assert.deepEqual(a.reset, { type: 'reset' });

  const s0 = initialState();
  const s1 = reducer(s0, a.funds);
  assert.equal(s1.funds, s0.funds + DEBUG_FUNDS_GRANT, '+£10,000 credits funds');
  const s2 = reducer(s0, a.xp);
  assert.equal(s2.xp, s0.xp + DEBUG_XP_GRANT, '+500 XP credits xp exactly');
  const s3 = reducer(s0, a.fast);
  assert.equal(s3.speed, 3, 'Force fast pins max speed');
});
