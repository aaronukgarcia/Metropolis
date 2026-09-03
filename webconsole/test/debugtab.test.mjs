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
  writeQueue,
  pendingCount,
  enqueueCommit,
  compactPayload,
} from '../src/sim/commitqueue.ts';
import { errorListModel, commitDebug } from '../src/sim/backend.ts';
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

// ---------- FEAT-2326609752 (AARON Q100078=B, 2026-09-03): commitDebug sink-first ----------
//
// This sandbox's global `localStorage` exists but its setItem/getItem throw
// ("localStorage.setItem is not a function" under this Node build) — see the
// same note in test/error-trapping.test.mjs. Install a WORKING in-memory
// localStorage stub for the duration of each test here so the fallback-queue
// path (and the drain path, which reads/writes the same key) behaves exactly
// as it does in a real browser instead of silently degrading through
// safeSetItem's catch on every call.
function withFakeLocalStorage(fn) {
  return async (...args) => {
    const originalDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
    const mem = new Map();
    Object.defineProperty(globalThis, 'localStorage', {
      value: {
        getItem: (k) => (mem.has(k) ? mem.get(k) : null),
        setItem: (k, v) => void mem.set(k, String(v)),
      },
      configurable: true,
    });
    try {
      await fn(mem, ...args);
    } finally {
      if (originalDescriptor) {
        Object.defineProperty(globalThis, 'localStorage', originalDescriptor);
      } else {
        delete globalThis.localStorage;
      }
    }
  };
}

/** Install a fake global.fetch for the duration of the wrapped test. */
function withFakeFetch(impl, fn) {
  return async (...args) => {
    const originalDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'fetch');
    const calls = [];
    Object.defineProperty(globalThis, 'fetch', {
      value: async (url, init) => {
        calls.push({ url, init });
        return impl(url, init);
      },
      configurable: true,
      writable: true,
    });
    try {
      await fn(calls, ...args);
    } finally {
      if (originalDescriptor) {
        Object.defineProperty(globalThis, 'fetch', originalDescriptor);
      } else {
        delete globalThis.fetch;
      }
    }
  };
}

test(
  'commitDebug: a 2xx from the sink is reported directly, never touches the local queue',
  withFakeLocalStorage(
    withFakeFetch(
      async () => ({ ok: true, status: 200 }),
      async (calls, mem) => {
        const result = await commitDebug({ meta: { appVersion: '9.9.9' }, hello: 'world' });
        assert.equal(result.ok, true);
        assert.equal(result.queued, false);
        assert.match(result.message, /metro MariaDB debug sink/);
        assert.equal(calls.length, 1, 'exactly one POST attempt (no queue write, so no drain either)');
        assert.equal(calls[0].url, 'http://127.0.0.1:8642/api/debug/commit');
        assert.equal(calls[0].init.method, 'POST');
        const sent = JSON.parse(calls[0].init.body);
        assert.equal(sent.id, result.id);
        assert.deepEqual(sent.payload, { meta: { appVersion: '9.9.9' }, hello: 'world' });
        assert.equal(mem.has(QUEUE_KEY), false, 'the local queue must never be written to on a live sink success');
      }
    )
  )
);

test(
  'commitDebug: a non-2xx from the sink falls back to the local queue unchanged (ASM-453)',
  withFakeLocalStorage(
    withFakeFetch(
      async () => ({ ok: false, status: 503 }),
      async (calls, mem) => {
        const result = await commitDebug({ x: 1 });
        assert.equal(result.ok, true);
        assert.equal(result.queued, true, 'sink failure must fall back to the existing queue path');
        assert.match(result.message, /queued locally/);
        const q = JSON.parse(mem.get(QUEUE_KEY));
        assert.equal(q.length, 1);
        assert.equal(q[0].id, result.id);
      }
    )
  )
);

test(
  'commitDebug: fetch throws -> queued locally, and the returned promise never rejects',
  withFakeLocalStorage(
    withFakeFetch(
      async () => {
        throw new Error('ECONNREFUSED');
      },
      async (calls, mem) => {
        const result = await commitDebug({ y: 2 });
        assert.equal(result.ok, true);
        assert.equal(result.queued, true);
        const q = JSON.parse(mem.get(QUEUE_KEY));
        assert.equal(q.length, 1);
        assert.equal(q[0].id, result.id);
      }
    )
  )
);

test(
  'commitDebug: a slow/never-responding sink times out (~1.5s) and falls back to the queue',
  withFakeLocalStorage(
    withFakeFetch(
      (url, init) =>
        new Promise((resolve, reject) => {
          // Never resolves on our own — only reacts to the AbortController's
          // signal, exactly like a real fetch would under a real timeout.
          init.signal.addEventListener('abort', () => {
            const err = new Error('The operation was aborted');
            err.name = 'AbortError';
            reject(err);
          });
        }),
      async (calls, mem) => {
        const start = Date.now();
        const result = await commitDebug({ z: 3 });
        const elapsedMs = Date.now() - start;
        assert.equal(result.queued, true, 'a timeout must fall back to the queue, not hang the commit');
        assert.ok(elapsedMs < 5000, `timeout must be short (bounded ~1.5s), took ${elapsedMs}ms`);
        const q = JSON.parse(mem.get(QUEUE_KEY));
        assert.equal(q.length, 1);
      }
    )
  )
);

test(
  'commitDebug: a live success drains any pre-existing queued commits, oldest first, 2xx-only removal',
  withFakeLocalStorage(
    withFakeFetch(
      async () => ({ ok: true, status: 200 }),
      async (calls, mem) => {
        // Seed the queue as if two earlier commits had queued locally while
        // the sink was unreachable (commitqueue.ts schema: {id, at, payload}).
        const seeded = [
          { id: 'DBG-OLD-1', at: '2026-09-01T00:00:00.000Z', payload: { n: 1 } },
          { id: 'DBG-OLD-2', at: '2026-09-01T00:00:01.000Z', payload: { n: 2 } },
        ];
        mem.set(QUEUE_KEY, JSON.stringify(seeded));

        const result = await commitDebug({ live: true });
        assert.equal(result.ok, true);
        assert.equal(result.queued, false);

        // The live commit + both drained entries = 3 POSTs, oldest-queued first.
        assert.equal(calls.length, 3);
        const postedIds = calls.map((c) => JSON.parse(c.init.body).id);
        assert.equal(postedIds[0], result.id, 'the live commit posts first');
        assert.deepEqual(postedIds.slice(1), ['DBG-OLD-1', 'DBG-OLD-2'], 'drain is oldest-first');

        // Every drained entry was removed (2xx on all) — queue now empty.
        const finalQueue = JSON.parse(mem.get(QUEUE_KEY));
        assert.deepEqual(finalQueue, []);
      }
    )
  )
);

test(
  'commitDebug: drain only removes entries that actually got a 2xx (partial-failure drain leaves the rest queued)',
  withFakeLocalStorage(
    withFakeFetch(
      // First call (the live commit) succeeds; subsequent drain calls fail.
      (() => {
        let n = 0;
        return async () => {
          n += 1;
          return n === 1 ? { ok: true, status: 200 } : { ok: false, status: 503 };
        };
      })(),
      async (calls, mem) => {
        const seeded = [{ id: 'DBG-STUCK', at: '2026-09-01T00:00:00.000Z', payload: { n: 1 } }];
        mem.set(QUEUE_KEY, JSON.stringify(seeded));

        const result = await commitDebug({ live: true });
        assert.equal(result.ok, true);
        assert.equal(result.queued, false, 'the LIVE commit itself still succeeded');

        // The pre-existing entry failed to drain (503) — it must remain queued.
        const finalQueue = JSON.parse(mem.get(QUEUE_KEY));
        assert.equal(finalQueue.length, 1);
        assert.equal(finalQueue[0].id, 'DBG-STUCK');
      }
    )
  )
);

// F1 REJECT-round fix (2026-09-03, ASM-453 violation): the first version of
// drainQueue read the queue ONCE up front and, after each await'd POST,
// wrote back a filtered copy of that SAME stale snapshot. A commit enqueued
// (by a second, concurrent commitDebug() call) while a drain POST was still
// in flight landed on disk between the snapshot and the write, and the
// stale-snapshot write silently clobbered it. This test reproduces that
// exact race directly (no helper wrappers, for full control over both the
// localStorage mock and the fetch mock's timing): a fetch impl that, WHILE
// its own promise is still pending (i.e. during the await inside
// postToSink/drainQueue), synchronously writes a brand-new commit straight
// into the same localStorage-backed queue — simulating another
// commitDebug() call's enqueueCommit landing in that exact window. The
// concurrently-enqueued entry MUST survive the drain.
test('commitDebug: a commit enqueued mid-drain (during an in-flight POST) survives the drain', async () => {
  const originalLocalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  const originalFetch = Object.getOwnPropertyDescriptor(globalThis, 'fetch');
  const mem = new Map();
  Object.defineProperty(globalThis, 'localStorage', {
    value: {
      getItem: (k) => (mem.has(k) ? mem.get(k) : null),
      setItem: (k, v) => void mem.set(k, String(v)),
    },
    configurable: true,
  });

  // Pre-existing queued entry — this is what the live commit's success will
  // trigger a drain of.
  mem.set(
    QUEUE_KEY,
    JSON.stringify([{ id: 'DBG-OLD-1', at: '2026-09-01T00:00:00.000Z', payload: { n: 1 } }])
  );

  let call = 0;
  Object.defineProperty(globalThis, 'fetch', {
    value: async () => {
      call += 1;
      if (call === 2) {
        // This is the drain's POST for DBG-OLD-1. Before resolving it,
        // simulate a CONCURRENT commit landing in the queue — exactly the
        // race window an unresolved await opens up for any other
        // synchronous code (here: another commitDebug()'s enqueueCommit) to
        // run and mutate the same on-disk queue.
        await new Promise((r) => setTimeout(r, 0));
        const current = JSON.parse(mem.get(QUEUE_KEY));
        current.push({ id: 'DBG-CONCURRENT', at: '2026-09-03T00:00:00.000Z', payload: { concurrent: true } });
        mem.set(QUEUE_KEY, JSON.stringify(current));
      }
      return { ok: true, status: 200 };
    },
    configurable: true,
    writable: true,
  });

  try {
    const result = await commitDebug({ live: true });
    assert.equal(result.ok, true);
    assert.equal(result.queued, false, 'the live commit itself succeeded');

    const finalQueue = JSON.parse(mem.get(QUEUE_KEY));
    assert.deepEqual(
      finalQueue.map((e) => e.id),
      ['DBG-CONCURRENT'],
      'DBG-OLD-1 was successfully drained (gone); DBG-CONCURRENT, enqueued WHILE that drain POST was in flight, must survive — a stale-snapshot write would have silently dropped it'
    );
  } finally {
    if (originalLocalStorage) {
      Object.defineProperty(globalThis, 'localStorage', originalLocalStorage);
    } else {
      delete globalThis.localStorage;
    }
    if (originalFetch) {
      Object.defineProperty(globalThis, 'fetch', originalFetch);
    } else {
      delete globalThis.fetch;
    }
  }
});
