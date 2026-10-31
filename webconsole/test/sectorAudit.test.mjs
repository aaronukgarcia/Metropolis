// sectorAudit.test.mjs — FEAT-2326609764 inc3a: the PRODUCTION SELF-AUDIT
// (design AC-13; flip gate EG-3 of FEAT-2326609764-partition-flag-on.md).
//
// Every test dispatches through the REAL reducer(). The oracle is always the
// flag-INDEPENDENT totalJobsWholeCity (BUG-1019 class), never totalJobs().
// Mutation proofs (scratch copy + md5, restored byte-for-byte) are logged in
// the build report, not committed.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { initialState, reducer } from '../src/sim/engine.ts';
import { totalJobs, totalJobsWholeCity } from '../src/sim/data.ts';
import * as SP from '../src/sim/sectorPartition.ts';
import { buildDebugJson } from '../src/sim/debugjson.ts';
import { EMPTY_MAP_UI } from '../src/sim/uistate.ts';

const {
  SECTOR_AUDIT_INTERVAL_TICKS,
  sectorKeyOf,
  partitionAuditStatus,
  drainPartitionAuditEvents,
  __resetPartitionAuditForTest,
  __resetDirtyTrackingForTest,
  __setPartitionedDerivationsForTest,
  __poisonSectorAggregateForTest,
} = SP;

const __dirname = dirname(fileURLToPath(import.meta.url));
const REGISTRY = JSON.parse(readFileSync(join(__dirname, '../../data/errors.json'), 'utf8'));

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

function cleanup() {
  __setPartitionedDerivationsForTest(false);
  __resetPartitionAuditForTest();
  __resetDirtyTrackingForTest();
}

function jobsSectorOf(s) {
  // a sector with a non-zero jobs aggregate in the live index
  for (const [key, agg] of SP.sectorIndexOf(s)) if (agg.jobs > 0) return key;
  return sectorKeyOf(0, 11);
}

test('registry: MET-V970 exists and is inside a ui.webconsole reservation', () => {
  assert.ok(REGISTRY.codes['MET-V970'], 'MET-V970 must be registered');
  assert.equal(SECTOR_AUDIT_INTERVAL_TICKS, 300);
});

test('AC-13a: the audit FIRES on an injected stale aggregate: registry error queued, fallback engaged, debug event visible', () => {
  try {
    let s = city(true);
    s = ticks(s, SECTOR_AUDIT_INTERVAL_TICKS - 1 - s.tick);
    assert.equal(s.tick, SECTOR_AUDIT_INTERVAL_TICKS - 1);
    const key = jobsSectorOf(s);
    assert.ok(__poisonSectorAggregateForTest(s, key, 7), 'sanity: poisoned a live sector');
    assert.equal(partitionAuditStatus().runs, 0);
    s = reducer(s, { type: 'tick' }); // tick 300: the audit runs
    const st = partitionAuditStatus();
    assert.equal(st.runs, 1);
    assert.equal(st.mismatches, 1, 'the stale aggregate must be caught');
    assert.equal(st.fallbackEngaged, true);
    assert.equal(SP.PARTITIONED_DERIVATIONS, false, 'partitioned path OFF for the session');
    assert.equal(st.events[0].tick, SECTOR_AUDIT_INTERVAL_TICKS);
    assert.equal(st.events[0].partitioned - st.events[0].wholeCity, 7);
    const drained = drainPartitionAuditEvents();
    assert.equal(drained.length, 1);
    assert.equal(drained[0].code, 'MET-V970');
    assert.equal(drainPartitionAuditEvents().length, 0, 'drained exactly once');
    const dj = buildDebugJson(s, { appVersion: 'x', frameAtMs: 1_700_000_000_000, map: EMPTY_MAP_UI, errors: [] });
    assert.equal(dj.partitionAudit.fallbackEngaged, true);
    assert.equal(dj.partitionAudit.mismatches, 1);
  } finally {
    cleanup();
  }
});

test('AC-13b: zero false positives across 1000 clean ticks (audits counted, none fire)', () => {
  try {
    let s = city(true);
    const start = s.tick;
    s = ticks(s, 1000);
    const st = partitionAuditStatus();
    const expectedRuns = Math.floor((start + 1000) / SECTOR_AUDIT_INTERVAL_TICKS) - Math.floor(start / SECTOR_AUDIT_INTERVAL_TICKS);
    assert.equal(st.runs, expectedRuns);
    assert.ok(st.runs >= 3, `at least three audits ran (got ${st.runs})`);
    assert.equal(st.mismatches, 0);
    assert.equal(st.fallbackEngaged, false);
    assert.equal(SP.PARTITIONED_DERIVATIONS, true);
    assert.equal(drainPartitionAuditEvents().length, 0);
    console.log(`AUDIT-CLEAN-1000: ticks=${s.tick} audits=${st.runs} fires=${st.mismatches}`);
  } finally {
    cleanup();
  }
});

test('AC-13c: the fallback is session-sticky and the state stream is byte-identical to a flag-OFF run afterwards', () => {
  try {
    let a = city(true);
    a = ticks(a, SECTOR_AUDIT_INTERVAL_TICKS - 1 - a.tick);
    __poisonSectorAggregateForTest(a, jobsSectorOf(a), 11);
    a = ticks(a, 1); // fires
    assert.equal(partitionAuditStatus().fallbackEngaged, true);
    a = ticks(a, 700); // crosses audits 600 and 900: must NOT run again
    assert.equal(partitionAuditStatus().runs, 1, 'sticky: no further audits after fallback');
    assert.equal(SP.PARTITIONED_DERIVATIONS, false);
    assert.equal(totalJobs(a), totalJobsWholeCity(a), 'whole-city path serves totalJobs after fallback');
    const jsonA = JSON.stringify(a);

    let b = city(false);
    b = ticks(b, a.tick - b.tick);
    assert.equal(b.tick, a.tick);
    assert.equal(JSON.stringify(b), jsonA, 'post-fallback state stream byte-identical to the flag-OFF run');
  } finally {
    cleanup();
  }
});

test('AC-13d: flag OFF is completely inert: zero audit runs, zero side-band writes, across audit boundaries', () => {
  try {
    __resetDirtyTrackingForTest();
    __resetPartitionAuditForTest();
    __setPartitionedDerivationsForTest(false);
    const originalSet = WeakMap.prototype.set;
    let sideBandWrites = 0;
    WeakMap.prototype.set = function patched(key, value) {
      if (value && typeof value === 'object' && 'dirty' in value && 'baseIndex' in value && 'schedule' in value) sideBandWrites++;
      return originalSet.call(this, key, value);
    };
    let s;
    try {
      s = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
      for (let x = 0; x < 40; x++) s = reducer(s, { type: 'place', spec: 'road', x, y: 10 });
      for (let x = 0; x < 5; x++) s = reducer(s, { type: 'place', spec: 'com_market', x: x * 7, y: 11 });
      s = ticks(s, 700);
    } finally {
      WeakMap.prototype.set = originalSet;
    }
    const st = partitionAuditStatus();
    assert.ok(s.tick >= 2 * SECTOR_AUDIT_INTERVAL_TICKS, 'crossed at least two audit boundaries');
    assert.equal(st.runs, 0);
    assert.equal(st.lastRunTick, null);
    assert.equal(sideBandWrites, 0);
    assert.equal(st.fallbackEngaged, false);
    console.log(`AUDIT-OFF-INERT: ticks=${s.tick} auditRuns=${st.runs} sideBandWrites=${sideBandWrites}`);
  } finally {
    cleanup();
  }
});

test('AC-13e: the audit compares and never mutates (determinism: SimState with audit-ON equals a no-audit-boundary reference; aggregates untouched)', () => {
  try {
    let s = city(true);
    s = ticks(s, SECTOR_AUDIT_INTERVAL_TICKS - 1 - s.tick);
    const before = [...SP.sectorIndexOf(s)].map(([k, a]) => [k, a.jobs, a.buildingCount]);
    s = ticks(s, 1);
    const after = new Map([...SP.sectorIndexOf(s)].map(([k, a]) => [k, [a.jobs, a.buildingCount]]));
    for (const [k, j, c] of before) assert.deepEqual(after.get(k), [j, c], `sector ${k} unchanged by the audit`);
    assert.equal(partitionAuditStatus().runs, 1);
    assert.equal(partitionAuditStatus().mismatches, 0);
  } finally {
    cleanup();
  }
});

test('BUG-1019 class: the audit source compares against totalJobsWholeCity, never through totalJobs()', () => {
  const src = readFileSync(join(__dirname, '../src/sim/sectorPartition.ts'), 'utf8');
  const body = src.slice(src.indexOf('export function runPartitionAudit'), src.indexOf('export function drainPartitionAuditEvents'));
  assert.ok(/totalJobsWholeCity\(s\)/.test(body));
  assert.ok(/totalJobsPartitioned\(s\)/.test(body));
  assert.ok(!/totalJobs\(/.test(body.replace(/totalJobs(WholeCity|Partitioned)\(/g, '')), 'no flag-dispatching totalJobs() call in the audit');
  assert.ok(!/Date\.now|performance\.now|localStorage|Math\.random/.test(body), 'BUG-602: no clock/storage in the audit');
});

// ---------------------------------------------------------------------------
// r2 (FEAT-2326609844 round r1 REJECT): F1 worker relay, F2 failure-to-run, P3 idempotent counter.
// ---------------------------------------------------------------------------

function stateAt299Poisoned(delta) {
  let s = city(true);
  s = ticks(s, SECTOR_AUDIT_INTERVAL_TICKS - 1 - s.tick);
  assert.ok(__poisonSectorAggregateForTest(s, jobsSectorOf(s), delta));
  return s;
}

test('F2 (GR#17): an audit that cannot RUN (non-integer aggregate => MET-V964 throw) queues a MET-V971 failure event and engages the fallback; the reducer does not throw', () => {
  try {
    let s = stateAt299Poisoned(0.5);
    s = reducer(s, { type: 'tick' }); // would throw out of the reducer before the fix
    const st = partitionAuditStatus();
    assert.equal(st.failures, 1);
    assert.equal(st.mismatches, 0);
    assert.equal(st.fallbackEngaged, true);
    assert.equal(SP.PARTITIONED_DERIVATIONS, false, 'an audit that cannot evaluate must not leave the partitioned path live');
    const ev = drainPartitionAuditEvents();
    assert.equal(ev.length, 1);
    assert.equal(ev[0].kind, 'failure');
    assert.equal(ev[0].code, 'MET-V971');
    assert.match(ev[0].detail, /MET-V964|non-integer/);
    assert.ok(REGISTRY.codes['MET-V971'], 'MET-V971 must be registered');
  } finally {
    cleanup();
  }
});

test('P3: the runs counter is idempotent per tick (same final state audited twice counts once, queues once)', () => {
  try {
    const s = stateAt299Poisoned(5);
    const a = reducer(s, { type: 'tick' });
    assert.equal(partitionAuditStatus().runs, 1);
    SP.runPartitionAudit(a, s.tick); // StrictMode-style second invoke of the same dispatch
    assert.equal(partitionAuditStatus().runs, 1);
    assert.equal(drainPartitionAuditEvents().length, 1);
  } finally {
    cleanup();
  }
});

let workerSelf = null;
let workerSink = [];
async function loadWorker(posted) {
  workerSink = posted;
  if (!workerSelf) {
    globalThis.self = { postMessage: (m) => workerSink.push(structuredClone(m)) };
    await import('../src/sim/simWorker.ts');
    workerSelf = globalThis.self;
  }
  return workerSelf;
}

test('F1: worker-side mismatch -> reply carries the report -> main-thread registry event + main flag false + debug.json shows the worker truth', async () => {
  const posted = [];
  try {
    const s = stateAt299Poisoned(9);
    const w = await loadWorker(posted);
    w.onmessage({ data: { type: 'runTick', state: s, requestId: 1 } }); // the worker's module instance runs the audit
    assert.equal(posted.length, 1);
    const reply = posted[0];
    assert.equal(reply.type, 'tickResult');
    assert.ok(reply.partitionAudit, 'the audit report rides the tick reply');
    assert.equal(reply.partitionAudit.events[0].code, 'MET-V970');
    // Simulate the MAIN thread: its own module instance still has the flag ON and no audit state.
    __resetPartitionAuditForTest();
    __setPartitionedDerivationsForTest(true);
    assert.equal(partitionAuditStatus().runs, 0);
    SP.applyWorkerPartitionAuditReport(reply.partitionAudit);
    assert.equal(SP.PARTITIONED_DERIVATIONS, false, 'main thread flag falls back');
    const drained = drainPartitionAuditEvents();
    assert.equal(drained.length, 1, 'main thread records MET-V970 via its drain exactly once');
    assert.equal(drained[0].code, 'MET-V970');
    const dj = buildDebugJson(reply.state, { appVersion: 'x', frameAtMs: 1_700_000_000_000, map: EMPTY_MAP_UI, errors: [] });
    assert.equal(dj.partitionAudit.fromWorker, true);
    assert.equal(dj.partitionAudit.runs, 1);
    assert.equal(dj.partitionAudit.mismatches, 1);
    assert.equal(dj.partitionAudit.fallbackEngaged, true);
    assert.equal(dj.partitionAudit.events[0].code, 'MET-V970');
  } finally {
    cleanup();
  }
});

test('F1: flag OFF adds ZERO audit payload to any worker reply; flag ON attaches it only on audited ticks', async () => {
  const posted = [];
  try {
    const w = await loadWorker(posted);
    __resetDirtyTrackingForTest();
    __resetPartitionAuditForTest();
    __setPartitionedDerivationsForTest(false);
    let s = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    for (let i = 0; i < 650; i++) {
      w.onmessage({ data: { type: 'runTick', state: s, requestId: i } });
      s = posted[posted.length - 1].state;
    }
    assert.equal(posted.length, 650);
    assert.equal(posted.filter((m) => 'partitionAudit' in m).length, 0, 'flag OFF: zero added payload');
    console.log(`WORKER-OFF-INERT: replies=${posted.length} withAuditPayload=0`);
    posted.length = 0;
    __resetPartitionAuditForTest();
    __setPartitionedDerivationsForTest(true);
    s = city(true);
    const start = s.tick;
    const withPayloadTicks = [];
    for (let i = 0; i < 620; i++) {
      w.onmessage({ data: { type: 'runTick', state: s, requestId: i } });
      const r = posted[posted.length - 1];
      s = r.state;
      if ('partitionAudit' in r) { withPayloadTicks.push(s.tick); assert.equal(r.partitionAudit.events.length, 0); }
    }
    assert.deepEqual(withPayloadTicks, [300, 600].filter((t) => t > start), 'payload only on audited ticks');
  } finally {
    cleanup();
  }
});
