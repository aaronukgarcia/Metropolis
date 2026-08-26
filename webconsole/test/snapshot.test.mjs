// snapshot.test.mjs — FEAT-1972079880: debug-snapshot throttle + enrichment.
//
// Run with `npm test` (node --test). The throttle is a pure clock-in/answer-out
// helper (no timers to fake); the snapshot builder is pure state-in/data-out.
// Each assertion can go RED by mutating the code (e.g. flip `elapsed >= period`
// to `>`, drop the backwards-clock guard, stop formatting funds via fmtMoney).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { nextRefreshDue, SNAPSHOT_REFRESH_MS } from '../src/sim/throttle.ts';
import { buildDebugSnapshot } from '../src/sim/snapshot.ts';
import { initialState, reducer, levelOf, HISTORY_CAP, LEDGER_CAP } from '../src/sim/engine.ts';

// ---------- nextRefreshDue: the 15 s stable-frame contract ----------

test('first call (no frame yet) is always due', () => {
  assert.deepEqual(nextRefreshDue(null, 123456), { due: true, remainingMs: 0 });
});

test('within the window: not due, exact remaining time', () => {
  const r = nextRefreshDue(1000, 8000); // 7 s elapsed of 15 s
  assert.equal(r.due, false);
  assert.equal(r.remainingMs, 8000);
});

test('due exactly at the period boundary, and beyond', () => {
  assert.equal(nextRefreshDue(0, SNAPSHOT_REFRESH_MS).due, true, 'exactly 15 s elapsed → due');
  assert.equal(nextRefreshDue(0, SNAPSHOT_REFRESH_MS - 1).due, false, '1 ms early → NOT due');
  assert.equal(nextRefreshDue(0, SNAPSHOT_REFRESH_MS - 1).remainingMs, 1);
  assert.equal(nextRefreshDue(0, SNAPSHOT_REFRESH_MS * 10).due, true);
});

test('default period is 15 seconds (the Aaron spec)', () => {
  assert.equal(SNAPSHOT_REFRESH_MS, 15000);
});

test('backwards clock jump self-heals (due rather than frozen forever)', () => {
  assert.equal(nextRefreshDue(10_000, 5_000).due, true);
});

test('degenerate inputs fail open (due): NaN clocks, bad periods', () => {
  assert.equal(nextRefreshDue(NaN, 1000).due, true);
  assert.equal(nextRefreshDue(1000, NaN).due, true);
  assert.equal(nextRefreshDue(1000, 2000, 0).due, true);
  assert.equal(nextRefreshDue(1000, 2000, -5).due, true);
  assert.equal(nextRefreshDue(1000, 2000, NaN).due, true);
});

test('custom period is honoured', () => {
  assert.equal(nextRefreshDue(0, 400, 500).due, false);
  assert.equal(nextRefreshDue(0, 400, 500).remainingMs, 100);
  assert.equal(nextRefreshDue(0, 500, 500).due, true);
});

// ---------- buildDebugSnapshot: enrichment ----------

test('snapshot carries clock, entity counts, level and rolling-cap info', () => {
  const s = initialState();
  const snap = buildDebugSnapshot(s);

  assert.equal(snap.clock.tick, s.tick);
  assert.equal(snap.clock.speed, s.speed);
  assert.match(snap.clock.tickRate, /^\d+(\.\d+)?\/s$/, 'tick rate like "1.1/s" at speed 1');

  assert.equal(snap.progress.level, levelOf(s.xp));

  // Building counts: only present kinds listed, and they sum to the total.
  const total = Object.values(snap.entities.buildingsByKind).reduce((a, b) => a + b, 0);
  assert.equal(total, s.buildings.length, 'by-kind counts sum to the building total');
  for (const n of Object.values(snap.entities.buildingsByKind)) {
    assert.ok(n > 0, 'zero-count kinds are omitted');
  }

  // Changelog-cap style "n / cap" strings derive from the engine constants.
  assert.equal(snap.caps.history, `${s.history.length} / ${HISTORY_CAP}`);
  assert.equal(snap.caps.ledger, `${s.ledger.length} / ${LEDGER_CAP}`);
});

test('money fields go through fmtMoney (£ + separators), delta is signed per-minute', () => {
  const s = initialState();
  const snap = buildDebugSnapshot(s);
  assert.match(snap.money.funds, /^-?£[\d,]+$/, `funds formatted, got ${snap.money.funds}`);
  assert.ok(snap.money.funds.includes(','), 'starting funds are in the millions — separators expected');
  assert.match(snap.money.incomePerTick, /^-?£[\d,]+$/);
  assert.match(snap.money.expensePerTick, /^-?£[\d,]+$/);
  assert.match(snap.money.fundsDeltaPerMin, /^[+-]£[\d,]+\/min$/, `signed per-minute delta, got ${snap.money.fundsDeltaPerMin}`);
});

test('paused game (speed 0) reports paused tick rate and delta, not Infinity', () => {
  const s = { ...initialState(), speed: 0 };
  const snap = buildDebugSnapshot(s);
  assert.equal(snap.clock.tickRate, 'paused');
  assert.equal(snap.money.fundsDeltaPerMin, 'paused');
});

test('snapshot reflects sim changes (a placed building shows up in the counts)', () => {
  const s0 = initialState();
  const before = buildDebugSnapshot(s0);
  const s1 = reducer(s0, { type: 'place', spec: 'res_hut', x: 5, y: 5 });
  const after = buildDebugSnapshot(s1);
  assert.equal(
    (after.entities.buildingsByKind.residential ?? 0) - (before.entities.buildingsByKind.residential ?? 0),
    1,
    'placing a residential hut increments the residential count'
  );
});

test('policiesOn lists only enabled policies', () => {
  const s0 = initialState();
  assert.deepEqual(buildDebugSnapshot(s0).policiesOn, [], 'no policies on at start');
  const s1 = reducer(s0, { type: 'policy', id: 'recycling' });
  assert.deepEqual(buildDebugSnapshot(s1).policiesOn, ['recycling']);
});
