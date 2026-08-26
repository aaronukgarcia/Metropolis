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
  readQueue,
  pendingCount,
  enqueueCommit,
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
  assert.equal(enqueueCommit(s, 'DBG-A', { tick: 1 }, '2026-08-26T10:00:00.000Z'), 1);
  assert.equal(enqueueCommit(s, 'DBG-B', { tick: 2 }, '2026-08-26T10:00:15.000Z'), 2);
  assert.equal(pendingCount(s), 2);
  const q = readQueue(s);
  assert.deepEqual(q[0], { id: 'DBG-A', at: '2026-08-26T10:00:00.000Z', payload: { tick: 1 } });
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
  assert.equal(enqueueCommit(corrupt, 'DBG-R', null, '2026-08-26T13:00:00.000Z'), 1);
  assert.equal(readQueue(corrupt)[0].id, 'DBG-R');
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
