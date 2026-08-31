import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS } from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  TICKS_PER_MONTH,
  CONNECT_EXEMPT_KINDS,
} from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

function board(buildings, extra = {}) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return {
    ...base,
    unlockedAll: true,
    funds: 10_000_000,
    buildings,
    nextId: maxId + 1,
    roadNotice: null,
    ledger: [],
    ...extra,
  };
}

function tickTo(s, n) {
  while (s.tick < n) s = reducer(s, { type: 'tick' });
  return s;
}

function roadCount(s) {
  return s.buildings.filter((b) => {
    const sp = SPECS[b.spec];
    return sp && (sp.kind === 'road' || sp.kind === 'motorway');
  }).length;
}

const PERIOD = 2 * TICKS_PER_MONTH;

test('AC-1 cadence: orphan sweep fires iff tick % (2*TICKS_PER_MONTH) === 0', () => {
  let s = board([
    { id: 1, spec: 'res_hut', x: 10, y: 10 },
    { id: 2, spec: 'road', x: 10, y: 14 },
  ]);
  const roads0 = roadCount(s);
  s = tickTo(s, TICKS_PER_MONTH);
  assert.equal(s.tick, TICKS_PER_MONTH);
  assert.equal(roadCount(s), roads0, 'month boundary is not a sweep');
  s = tickTo(s, PERIOD);
  assert.equal(s.tick, PERIOD);
  assert.ok(roadCount(s) > roads0, 'bi-monthly tick lays connectors');
});

test('AC-2 universe: exempt kinds are not swept', () => {
  let s = board([
    { id: 1, spec: 'res_hut', x: 8, y: 8 },
    { id: 2, spec: 'road', x: 8, y: 12 },
    { id: 3, spec: 'rail', x: 20, y: 20 },
    { id: 4, spec: 'pylon', x: 22, y: 20 },
  ]);
  assert.ok(CONNECT_EXEMPT_KINDS.has('rail'));
  assert.ok(CONNECT_EXEMPT_KINDS.has('pylon'));
  s = tickTo(s, PERIOD);
  const rails = s.buildings.filter((b) => b.spec === 'rail');
  const pylons = s.buildings.filter((b) => b.spec === 'pylon');
  assert.equal(rails.length, 1);
  assert.equal(pylons.length, 1);
  assert.ok(roadCount(s) > 1, 'hut got a connector');
});

test('AC-5 charge: conservation holds on sweep tick', () => {
  let s = board([
    { id: 1, spec: 'res_hut', x: 10, y: 10 },
    { id: 2, spec: 'road', x: 10, y: 14 },
  ]);
  s = tickTo(s, PERIOD);
  const flow = s.lastFlows.outflows.find((o) => o.label === 'Road Auto-Connect');
  assert.ok(flow && flow.value > 0, 'sweep books Road Auto-Connect outflow');
  assert.equal(s.fundsAtTickEnd, s.fundsAtTickStart + s.lastFlows.inflows.reduce((a, f) => a + f.value, 0) - s.lastFlows.outflows.reduce((a, f) => a + f.value, 0));
  const cons = runConsistencyChecks(s);
  assert.equal(cons.failures, 0, cons.checks.filter((c) => !c.ok).map((c) => c.id).join(','));
});

test('AC-7 unaffordable: stop with no partial tiles', () => {
  let s = board(
    [
      { id: 1, spec: 'res_hut', x: 10, y: 10 },
      { id: 2, spec: 'res_hut', x: 30, y: 10 },
      { id: 3, spec: 'road', x: 10, y: 14 },
      { id: 4, spec: 'road', x: 30, y: 14 },
    ],
    { funds: 0 },
  );
  const before = s.buildings.length;
  s = tickTo(s, PERIOD - 1);
  s = { ...s, funds: 0 };
  s = reducer(s, { type: 'tick' });
  assert.equal(s.buildings.length, before, 'zero funds → no connectors');
  assert.ok(!s.lastFlows.outflows.some((o) => o.label === 'Road Auto-Connect'));
});

test('AC-10 already-connected: no-op, no extra tiles', () => {
  let s = board([
    { id: 1, spec: 'res_hut', x: 10, y: 10 },
    { id: 2, spec: 'road', x: 10, y: 11 },
  ]);
  const n0 = s.buildings.length;
  const funds0 = s.funds;
  s = tickTo(s, PERIOD);
  assert.equal(s.buildings.length, n0);
  assert.ok(!s.lastFlows.outflows.some((o) => o.label === 'Road Auto-Connect'));
  assert.equal(s.fundsAtTickEnd - s.fundsAtTickStart, s.lastFlows.inflows.reduce((a, f) => a + f.value, 0) - s.lastFlows.outflows.reduce((a, f) => a + f.value, 0));
  void funds0;
});
