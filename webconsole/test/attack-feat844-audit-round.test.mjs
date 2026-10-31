// attack-feat844-audit-round.test.mjs — independent destructive round r1 on
// FEAT-2326609844 (spatial-partition inc3a, the production self-audit, design
// AC-13). Attacker: opus-round-r1-inc3a-audit. Every test goes through the
// REAL reducer(); the oracle is always the flag-independent totalJobsWholeCity.
//
// Kept in tree because each pins a behaviour the builder's suite left open:
//   R1 StrictMode/double-invoke: the same (state, action) reduced twice queues
//      MET-V970 exactly once (BUG-756/BUG-677 class).
//   R2 the VISIBLE number at the fire tick: totalJobs(final) is whole-city.
//   R3 determinism: two identical poisoned runs fire on the same tick and end
//      in byte-identical state.
//   R4 a non-tick action dispatched AT the boundary tick does not re-audit
//      (kills the "drop the prevTick guard" mutant the builder's suite missed).
//   R5 hydrate across the boundary (load-at-299 then tick, load-at-300) audits
//      once per boundary, never twice.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { initialState, reducer } from '../src/sim/engine.ts';
import { totalJobs, totalJobsWholeCity } from '../src/sim/data.ts';
import * as SP from '../src/sim/sectorPartition.ts';

const {
  SECTOR_AUDIT_INTERVAL_TICKS: N,
  partitionAuditStatus,
  drainPartitionAuditEvents,
  __resetPartitionAuditForTest,
  __resetDirtyTrackingForTest,
  __setPartitionedDerivationsForTest,
  __poisonSectorAggregateForTest,
} = SP;

function city(flagOn) {
  __resetDirtyTrackingForTest();
  __resetPartitionAuditForTest();
  __setPartitionedDerivationsForTest(flagOn);
  let s = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
  for (let x = 0; x < 40; x++) s = reducer(s, { type: 'place', spec: 'road', x, y: 10 });
  for (let x = 0; x < 5; x++) s = reducer(s, { type: 'place', spec: 'com_market', x: x * 7, y: 11 });
  return s;
}
function ticks(s, n) {
  for (let t = 0; t < n; t++) s = reducer(s, { type: 'tick' });
  return s;
}
function jobsSectorOf(s) {
  for (const [key, agg] of SP.sectorIndexOf(s)) if (agg.jobs > 0) return key;
  throw new Error('no jobs sector');
}
function cleanup() {
  __setPartitionedDerivationsForTest(false);
  __resetPartitionAuditForTest();
  __resetDirtyTrackingForTest();
}
function to299(flagOn = true) {
  let s = city(flagOn);
  return ticks(s, N - 1 - s.tick);
}

test('R1 StrictMode double-invoke: same (state, tick) reduced twice queues MET-V970 once', () => {
  try {
    const s = to299();
    __poisonSectorAggregateForTest(s, jobsSectorOf(s), 5);
    const a = reducer(s, { type: 'tick' }); // React dev: first (discarded) invocation
    const b = reducer(s, { type: 'tick' }); // React dev: second (kept) invocation
    assert.equal(a.tick, N);
    assert.equal(b.tick, N);
    const ev = drainPartitionAuditEvents();
    assert.equal(ev.length, 1, 'exactly one MET-V970 event for one logical tick');
    assert.equal(partitionAuditStatus().mismatches, 1);
    // the kept state is computed on the whole-city path
    assert.equal(totalJobs(b), totalJobsWholeCity(b));
  } finally {
    cleanup();
  }
});

test('R2 the visible totalJobs on the fire-tick state is the whole-city value, not the stale partitioned one', () => {
  try {
    let s = to299();
    __poisonSectorAggregateForTest(s, jobsSectorOf(s), 9);
    s = reducer(s, { type: 'tick' });
    assert.equal(partitionAuditStatus().mismatches, 1);
    assert.equal(totalJobs(s), totalJobsWholeCity(s), 'UI-read selector on the fire-tick state must not serve the stale fold');
  } finally {
    cleanup();
  }
});

test('R3 determinism: two identical poisoned runs fire on the same tick and end byte-identical', () => {
  const run = () => {
    let s = to299();
    __poisonSectorAggregateForTest(s, jobsSectorOf(s), 3);
    s = ticks(s, 2 * N + 7);
    const st = partitionAuditStatus();
    const out = { fire: st.events.map((e) => e.tick), json: JSON.stringify(s) };
    cleanup();
    return out;
  };
  const r1 = run();
  const r2 = run();
  assert.deepEqual(r1.fire, [N]);
  assert.deepEqual(r2.fire, r1.fire);
  assert.equal(r2.json, r1.json);
});

test('R4 a non-tick action dispatched at the boundary tick does not re-run the audit', () => {
  try {
    let s = to299();
    s = reducer(s, { type: 'tick' });
    assert.equal(partitionAuditStatus().runs, 1);
    s = reducer(s, { type: 'speed', speed: 2 });
    s = reducer(s, { type: 'tool', tool: 'select' });
    assert.equal(s.tick, N);
    assert.equal(partitionAuditStatus().runs, 1, 'same tick, non-tick actions: no extra whole-city fold');
  } finally {
    cleanup();
  }
});

// r2 (opus-round-r2-inc3a-audit): the worker relay, DELTA path + no-loss paths.
let workerSink = null;
let workerSelf = null;
async function worker(sink) {
  workerSink = sink;
  if (!workerSelf) {
    globalThis.self = { postMessage: (m) => workerSink.push(structuredClone(m)) };
    await import('../src/sim/simWorker.ts');
    workerSelf = globalThis.self;
  }
  return workerSelf;
}

test('R6 worker DELTA reply carries the queued audit report (MET-V970) exactly once', async () => {
  const { diffSimState } = await import('../src/sim/simWorkerDelta.ts');
  const posted = [];
  try {
    const w = await worker(posted);
    const s = to299();
    w.onmessage({ data: { type: 'runTick', state: s, requestId: 1 } }); // seeds the worker cache (tick 300 audit runs clean)
    assert.ok(posted[0].partitionAudit, 'full reply on the audited tick carries the report');
    assert.equal(posted[0].partitionAudit.events.length, 0);
    const base = posted[0].state;
    // A mismatch in this module instance (worker-side audit) queues MET-V970 and marks a report due.
    const p = ticks(base, 299);
    __poisonSectorAggregateForTest(p, jobsSectorOf(p), 4);
    reducer(p, { type: 'tick' });
    assert.equal(partitionAuditStatus().mismatches, 1);
    const delta = diffSimState(base, base);
    w.onmessage({ data: { type: 'runTickDelta', requestId: 2, delta } });
    const r = posted[1];
    assert.equal(r.type, 'tickResultDelta');
    assert.ok(r.partitionAudit, 'the DELTA reply carries the report');
    assert.deepEqual(r.partitionAudit.events.map((e) => e.code), ['MET-V970']);
    assert.equal(r.partitionAudit.status.fallbackEngaged, true);
    w.onmessage({ data: { type: 'runTickDelta', requestId: 3, delta: diffSimState(base, base) } });
    // reply 3 is a basisMismatch (cache moved on) or a delta: either way the event must not be re-sent
    for (const m of posted.slice(2)) assert.ok(!m.partitionAudit || m.partitionAudit.events.length === 0, 'no double relay');
  } finally {
    cleanup();
  }
});

test('R7 a basisMismatch reply does not consume the report: the next tick reply still carries the event', async () => {
  const { diffSimState } = await import('../src/sim/simWorkerDelta.ts');
  const posted = [];
  try {
    const w = await worker(posted);
    const s = to299();
    w.onmessage({ data: { type: 'runTick', state: s, requestId: 1 } });
    const base = posted[0].state;
    const p = ticks(base, 299);
    __poisonSectorAggregateForTest(p, jobsSectorOf(p), 6);
    reducer(p, { type: 'tick' }); // MET-V970 queued worker-side
    const stale = diffSimState(s, s); // baseTick 299 != worker cache 300 -> basisMismatch
    w.onmessage({ data: { type: 'runTickDelta', requestId: 2, delta: stale } });
    assert.equal(posted[1].type, 'basisMismatch');
    assert.equal(posted[1].partitionAudit, undefined);
    w.onmessage({ data: { type: 'runTick', state: base, requestId: 3 } });
    assert.deepEqual(posted[2].partitionAudit?.events.map((e) => e.code), ['MET-V970'], 'event survives to the next reply');
  } finally {
    cleanup();
  }
});

test('R8 store.tsx relays the report BEFORE any discard/stale return of a tick reply', async () => {
  const { readFile } = await import('node:fs/promises');
  const src = await readFile(new URL('../src/sim/store.tsx', import.meta.url), 'utf8');
  const start = src.indexOf("if (msg.type !== 'tickResult' && msg.type !== 'tickResultDelta') return;");
  assert.ok(start > 0);
  const relay = src.indexOf('applyWorkerPartitionAuditReport(msg.partitionAudit)', start);
  assert.ok(relay > start, 'relay present after the type gate');
  const between = src.slice(start + 80, relay);
  assert.ok(!/\breturn\b/.test(between.replace(/\/\/.*$/gm, '')), 'no return between the type gate and the relay');
});

test('R9 CLEAN double-invoke of the same tick counts one run (the idempotence line itself, not the fallback gate)', () => {
  try {
    const s = to299();
    reducer(s, { type: 'tick' });
    reducer(s, { type: 'tick' }); // StrictMode dev re-invoke; flag still ON (clean audit)
    assert.equal(SP.PARTITIONED_DERIVATIONS, true);
    assert.equal(partitionAuditStatus().runs, 1);
    assert.equal(partitionAuditStatus().mismatches, 0);
  } finally {
    cleanup();
  }
});

test('R5 hydrate across the boundary audits once per boundary (load at 299 then tick; load at 300 twice)', () => {
  try {
    const s299 = to299();
    const s300 = reducer(s299, { type: 'tick' });
    __resetPartitionAuditForTest();
    // a different city at tick 10 loads the tick-299 save, then ticks
    let cur = reducer(city(true), { type: 'hydrate', state: s299 });
    assert.equal(partitionAuditStatus().runs, 0, 'load at 299: no audit');
    cur = reducer(cur, { type: 'tick' });
    assert.equal(cur.tick, N);
    assert.equal(partitionAuditStatus().runs, 1, 'tick 299 -> 300 after load: one audit');
    __resetPartitionAuditForTest();
    let c2 = reducer(city(true), { type: 'hydrate', state: s300 });
    assert.equal(partitionAuditStatus().runs, 1, 'load landing exactly on 300 audits once');
    c2 = reducer(c2, { type: 'hydrate', state: s300 });
    assert.equal(partitionAuditStatus().runs, 1, 're-hydrate at the same tick: no second audit');
    assert.equal(partitionAuditStatus().mismatches, 0);
  } finally {
    cleanup();
  }
});
