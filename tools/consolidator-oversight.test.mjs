// tools/consolidator-oversight.test.mjs — unit tests for the CONSOLIDATOR
// AUDIT TRAIL oversight tripwires (Aaron's ruling on FEAT-2326609761:
// "keeps an eye on its progress to make sure it does not go mad").
//
// BUG-543: this file is named `*.test.mjs` (not merely placed under a
// `test/` directory), which CI's root `node --test` auto-discovers by
// Node's default test-file glob regardless of directory — the SAME
// discovery mechanism tools/plan/add-error.test.js already relies on
// (sibling file, not nested under a test/ dir). This file IS a real test
// suite, so that auto-discovery is correct and no NODE_TEST_CONTEXT guard
// is needed here; the guard's actual target is the TOOL itself
// (consolidator-oversight.mjs) not implicitly running its CLI on import —
// verified below exactly as server.test.js verifies server.js.
//
// Every flag is RED-PROOFED: a constructed pathology trips it, and a
// constructed healthy trail stays quiet — so a mutation that disables a
// check (e.g. inverting a comparison, or dropping the threshold entirely)
// fails at least one of these tests.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  THRESHOLDS,
  flagRunawayDemolition,
  flagOscillation,
  flagSpendRate,
  flagCapacityDownTrend,
  flagReserveLandShrinking,
  runOversight,
} from './consolidator-oversight.mjs';

/** Build a minimal 'implemented' audit entry. */
function impl(id, tick, payload) {
  return { id, stage: 'implemented', at: `2026-09-04T${String(tick).padStart(2, '0')}:00:00.000Z`, payload: { tick, ...payload } };
}

// ---------------------------------------------------------------------------
// §1 Runaway demolition
// ---------------------------------------------------------------------------

test('flagRunawayDemolition: trips on a pass over the ceiling', () => {
  const entries = [impl('A', 1, { demolitions: THRESHOLDS.MAX_DEMOLITIONS_PER_PASS + 1 })];
  const findings = flagRunawayDemolition(entries);
  assert.equal(findings.length, 1);
  assert.equal(findings[0].kind, 'runaway-demolition');
});

test('flagRunawayDemolition: stays quiet exactly AT the ceiling and below it', () => {
  const entries = [
    impl('A', 1, { demolitions: THRESHOLDS.MAX_DEMOLITIONS_PER_PASS }),
    impl('B', 2, { demolitions: 1 }),
  ];
  assert.deepEqual(flagRunawayDemolition(entries), []);
});

test('flagRunawayDemolition: a missing `demolitions` field is skipped, never crashes, never falsely flags', () => {
  const entries = [impl('A', 1, {})];
  assert.deepEqual(flagRunawayDemolition(entries), []);
});

test('flagRunawayDemolition: honours a custom ceiling argument', () => {
  const entries = [impl('A', 1, { demolitions: 5 })];
  assert.equal(flagRunawayDemolition(entries, 4).length, 1);
  assert.equal(flagRunawayDemolition(entries, 10).length, 0);
});

// ---------------------------------------------------------------------------
// §2 Oscillation
// ---------------------------------------------------------------------------

test('flagOscillation: trips when a section goes A->B then B->A within the window', () => {
  const entries = [
    impl('P1', 1, { consolidations: [{ sectionKey: 7, fromSpec: 'pow_wind', toSpec: 'pow_offshore', buildingIds: [1, 2] }] }),
    impl('P2', 2, { consolidations: [{ sectionKey: 7, fromSpec: 'pow_offshore', toSpec: 'pow_wind', buildingIds: [3] }] }),
  ];
  const findings = flagOscillation(entries, 5);
  assert.equal(findings.length, 1);
  assert.equal(findings[0].kind, 'oscillation');
  assert.equal(findings[0].detail.sectionKey, 7);
});

test('flagOscillation: stays quiet on a healthy, monotonic ladder climb (no reversal)', () => {
  const entries = [
    impl('P1', 1, { consolidations: [{ sectionKey: 7, fromSpec: 'pow_wind', toSpec: 'pow_offshore', buildingIds: [1] }] }),
    impl('P2', 2, { consolidations: [{ sectionKey: 9, fromSpec: 'hea_clinic', toSpec: 'hea_hospital', buildingIds: [4] }] }),
  ];
  assert.deepEqual(flagOscillation(entries, 5), []);
});

test('flagOscillation: a reversal OUTSIDE the window does not trip', () => {
  const entries = [
    impl('P1', 1, { consolidations: [{ sectionKey: 7, fromSpec: 'A', toSpec: 'B', buildingIds: [] }] }),
    impl('P2', 2, { consolidations: [] }),
    impl('P3', 3, { consolidations: [] }),
    impl('P4', 4, { consolidations: [] }),
    impl('P5', 5, { consolidations: [] }),
    impl('P6', 6, { consolidations: [] }),
    impl('P7', 7, { consolidations: [{ sectionKey: 7, fromSpec: 'B', toSpec: 'A', buildingIds: [] }] }),
  ];
  assert.deepEqual(flagOscillation(entries, 5), []);
});

test('flagOscillation: a reversal on a DIFFERENT section does not trip', () => {
  const entries = [
    impl('P1', 1, { consolidations: [{ sectionKey: 7, fromSpec: 'A', toSpec: 'B', buildingIds: [] }] }),
    impl('P2', 2, { consolidations: [{ sectionKey: 8, fromSpec: 'B', toSpec: 'A', buildingIds: [] }] }),
  ];
  assert.deepEqual(flagOscillation(entries, 5), []);
});

test('flagOscillation: a malformed consolidation entry (missing fields) is skipped, never crashes', () => {
  const entries = [
    impl('P1', 1, { consolidations: [{ sectionKey: 7 }, null, { fromSpec: 'A' }] }),
    impl('P2', 2, { consolidations: 'not-an-array' }),
  ];
  assert.doesNotThrow(() => flagOscillation(entries, 5));
  assert.deepEqual(flagOscillation(entries, 5), []);
});

// ---------------------------------------------------------------------------
// §3 Spend rate
// ---------------------------------------------------------------------------

test('flagSpendRate: trips on a pass spending over the ceiling', () => {
  const entries = [impl('A', 1, { spend: THRESHOLDS.MAX_SPEND_PER_PASS + 1 })];
  assert.equal(flagSpendRate(entries).length, 1);
});

test('flagSpendRate: stays quiet at/under the ceiling', () => {
  const entries = [impl('A', 1, { spend: THRESHOLDS.MAX_SPEND_PER_PASS })];
  assert.deepEqual(flagSpendRate(entries), []);
});

test('flagSpendRate: a negative spend (net refund pass) never trips', () => {
  const entries = [impl('A', 1, { spend: -1_000_000 })];
  assert.deepEqual(flagSpendRate(entries), []);
});

// ---------------------------------------------------------------------------
// §4 Capacity trending DOWN
// ---------------------------------------------------------------------------

test('flagCapacityDownTrend: trips on N consecutive falling passes', () => {
  const entries = [
    impl('A', 1, { totalCapacityAfter: 1000 }),
    impl('B', 2, { totalCapacityAfter: 900 }),
    impl('C', 3, { totalCapacityAfter: 800 }),
  ];
  const findings = flagCapacityDownTrend(entries, 3);
  assert.equal(findings.length, 1);
  assert.equal(findings[0].kind, 'capacity-down-trend');
});

test('flagCapacityDownTrend: stays quiet on a rising or flat trend', () => {
  const entries = [
    impl('A', 1, { totalCapacityAfter: 800 }),
    impl('B', 2, { totalCapacityAfter: 900 }),
    impl('C', 3, { totalCapacityAfter: 900 }),
    impl('D', 4, { totalCapacityAfter: 950 }),
  ];
  assert.deepEqual(flagCapacityDownTrend(entries, 3), []);
});

test('flagCapacityDownTrend: stays quiet on a streak SHORTER than the threshold', () => {
  const entries = [
    impl('A', 1, { totalCapacityAfter: 1000 }),
    impl('B', 2, { totalCapacityAfter: 900 }), // only 2 consecutive falls
    impl('C', 3, { totalCapacityAfter: 950 }),
  ];
  assert.deepEqual(flagCapacityDownTrend(entries, 3), []);
});

test('flagCapacityDownTrend: a gap (missing field) does not break OR falsely extend the streak', () => {
  const entries = [
    impl('A', 1, { totalCapacityAfter: 1000 }),
    impl('B', 2, {}), // instrumentation gap — no field at all
    impl('C', 3, { totalCapacityAfter: 900 }),
    impl('D', 4, { totalCapacityAfter: 800 }),
  ];
  // Only entries WITH the field participate: 1000 -> 900 -> 800 is a real
  // 3-pass fall even with a gap in between (GR#16: the gap is invisible to
  // the trend, not falsely healthy and not falsely broken).
  assert.equal(flagCapacityDownTrend(entries, 3).length, 1);
});

// ---------------------------------------------------------------------------
// §5 Reserve land shrinking
// ---------------------------------------------------------------------------

test('flagReserveLandShrinking: trips on N consecutive shrinking passes', () => {
  const entries = [
    impl('A', 1, { reserveLandTilesAfter: 500 }),
    impl('B', 2, { reserveLandTilesAfter: 400 }),
    impl('C', 3, { reserveLandTilesAfter: 300 }),
  ];
  assert.equal(flagReserveLandShrinking(entries, 3).length, 1);
});

test('flagReserveLandShrinking: stays quiet on a healthy trail (constant reserve land)', () => {
  const entries = [
    impl('A', 1, { reserveLandTilesAfter: 500 }),
    impl('B', 2, { reserveLandTilesAfter: 500 }),
    impl('C', 3, { reserveLandTilesAfter: 500 }),
  ];
  assert.deepEqual(flagReserveLandShrinking(entries, 3), []);
});

// ---------------------------------------------------------------------------
// §6 Aggregate runner — a fully healthy trail stays quiet across ALL flags
// ---------------------------------------------------------------------------

test('runOversight: a fully healthy trail (discovered + planned + a clean implemented pass) reports healthy with zero findings', () => {
  const entries = [
    { id: 'D1', stage: 'discovered', at: '2026-09-04T01:00:00.000Z', payload: { tick: 1 } },
    { id: 'P1', stage: 'planned', at: '2026-09-04T01:00:00.000Z', payload: { tick: 1 } },
    impl('I1', 1, {
      demolitions: 2,
      builds: 1,
      spend: 100_000,
      totalCapacityAfter: 1000,
      reserveLandTilesAfter: 500,
      consolidations: [{ sectionKey: 3, fromSpec: 'pow_wind', toSpec: 'pow_offshore', buildingIds: [1, 2] }],
    }),
    impl('I2', 2, {
      demolitions: 3,
      builds: 1,
      spend: 150_000,
      totalCapacityAfter: 1100,
      reserveLandTilesAfter: 490,
      consolidations: [{ sectionKey: 9, fromSpec: 'hea_clinic', toSpec: 'hea_hospital', buildingIds: [5] }],
    }),
  ];
  const report = runOversight(entries);
  assert.equal(report.healthy, true);
  assert.deepEqual(report.findings, []);
  assert.equal(report.totalEntries, 4);
  assert.equal(report.discoveredCount, 1);
  assert.equal(report.plannedCount, 1);
  assert.equal(report.implementedCount, 2);
});

test('runOversight: a constructed oscillation trail is caught end-to-end', () => {
  const entries = [
    impl('I1', 1, { consolidations: [{ sectionKey: 7, fromSpec: 'A', toSpec: 'B', buildingIds: [] }] }),
    impl('I2', 2, { consolidations: [{ sectionKey: 7, fromSpec: 'B', toSpec: 'A', buildingIds: [] }] }),
  ];
  const report = runOversight(entries);
  assert.equal(report.healthy, false);
  assert.ok(report.findings.some((f) => f.kind === 'oscillation'));
});

test('runOversight: a constructed capacity-down trend trail is caught end-to-end', () => {
  const entries = [
    impl('I1', 1, { totalCapacityAfter: 1000 }),
    impl('I2', 2, { totalCapacityAfter: 900 }),
    impl('I3', 3, { totalCapacityAfter: 800 }),
  ];
  const report = runOversight(entries);
  assert.equal(report.healthy, false);
  assert.ok(report.findings.some((f) => f.kind === 'capacity-down-trend'));
});

test('runOversight: ignores non-implemented stages for the tripwires but counts them in the summary', () => {
  const entries = [
    { id: 'D1', stage: 'discovered', at: '2026-09-04T00:00:00.000Z', payload: { demolitions: 99999 } }, // would trip if mis-scoped
    { id: 'P1', stage: 'planned', at: '2026-09-04T00:00:00.000Z', payload: { spend: 999999999 } }, // would trip if mis-scoped
  ];
  const report = runOversight(entries);
  assert.equal(report.healthy, true, 'discovered/planned payloads must never feed the implemented-only tripwires');
  assert.equal(report.totalEntries, 2);
  assert.equal(report.implementedCount, 0);
});

// ---------------------------------------------------------------------------
// §7 CLI guard: importing this module must never perform a network call or
// run the CLI as a side effect (BUG-543 discipline, mirrored from
// tools/debugsink/server.js's require.main guard).
// ---------------------------------------------------------------------------

test('importing consolidator-oversight.mjs does not invoke the CLI (no implicit network call)', async () => {
  // If import had a side effect of calling main(), it would either hang on a
  // real fetch or throw synchronously during import — this test having
  // already gotten this far (the module-level imports above all resolved)
  // is itself the proof; re-import defensively to double check no top-level
  // await/fetch was triggered.
  const mod = await import('./consolidator-oversight.mjs?cachebust=1');
  assert.equal(typeof mod.runOversight, 'function');
  assert.equal(typeof mod.fetchAuditEntries, 'function');
});
