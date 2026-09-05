// bug-769-insolvency.test.mjs — BUG-769: FinanceAPI.InsolvencyMonths()/
// IsInsolvent() (BUG-759) advance for real in production every month but
// nothing outside a Go test ever read them — no compose consumer beyond
// the wire patch this ticket adds, no protocol delta before this fix, no
// player surface. This file proves the TS half, mirroring
// bug-723-payroll-shortfall.test.mjs's exact structure for the equally
// optional insolvency section:
//
//   1. wire.ts's isFinanceBalanceSheetPatch/decodeFinanceBalanceSheetPatch
//      coerce garbage insolvencyMonths/insolvent fields safely (GR#16).
//   2. newsFeed.ts's observeNews emits exactly ONE entry when insolvency
//      starts and exactly ONE when it clears, is StrictMode-safe, and
//      treats a null source as unknown (never a phantom clear).
//   3. A mutation proof that a broken start-transition detector is caught.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { runMutantSelfReinvoke } from '../testsupport/mutant.mjs';
import {
  isFinanceBalanceSheetPatch,
  decodeFinanceBalanceSheetPatch,
  FINANCE_SCHEMA_VERSION,
} from '../src/sim/wire.ts';
import {
  createNewsFeedTracker,
  createNewsFeedSeq,
  observeNews,
} from '../src/sim/newsFeed.ts';

function emptySources(tick = 0) {
  return { notice: null, milestoneNotice: null, placeNotice: null, tick };
}

// ---------------------------------------------------------------------
// 1. wire.ts decode-boundary garbage safety (GR#16).
// ---------------------------------------------------------------------

test('isFinanceBalanceSheetPatch accepts well-formed insolvencyMonths/insolvent fields', () => {
  const patch = { schemaVersion: FINANCE_SCHEMA_VERSION, insolvencyMonths: 3, insolvent: true };
  assert.equal(isFinanceBalanceSheetPatch(patch), true);
  assert.deepEqual(decodeFinanceBalanceSheetPatch(patch), patch);
});

test('isFinanceBalanceSheetPatch accepts a patch with NEITHER insolvency field ("no data this cycle")', () => {
  const patch = { schemaVersion: FINANCE_SCHEMA_VERSION };
  assert.equal(isFinanceBalanceSheetPatch(patch), true);
});

test('isFinanceBalanceSheetPatch accepts insolvencyMonths/insolvent: null identically to absent', () => {
  const withNull = { schemaVersion: FINANCE_SCHEMA_VERSION, insolvencyMonths: null, insolvent: null };
  const withoutField = { schemaVersion: FINANCE_SCHEMA_VERSION };
  assert.equal(isFinanceBalanceSheetPatch(withNull), true, 'null fields must be accepted, not rejected as garbage');
  assert.deepEqual(decodeFinanceBalanceSheetPatch(withNull), withNull);
  assert.equal(
    isFinanceBalanceSheetPatch(withNull),
    isFinanceBalanceSheetPatch(withoutField),
    'null and absent must decode to the same accept/reject verdict'
  );
});

const garbageInsolvencyMonths = [
  { name: 'a string', value: 'three' },
  { name: 'an array', value: [1, 2, 3] },
  { name: 'a boolean', value: true },
];
for (const { name, value } of garbageInsolvencyMonths) {
  test(`isFinanceBalanceSheetPatch REJECTS the whole patch when insolvencyMonths is ${name}`, () => {
    const patch = { schemaVersion: FINANCE_SCHEMA_VERSION, insolvencyMonths: value, insolvent: false };
    assert.equal(isFinanceBalanceSheetPatch(patch), false);
    assert.equal(decodeFinanceBalanceSheetPatch(patch), null);
  });
}

const garbageInsolvent = [
  { name: 'a string', value: 'true' },
  { name: 'a number', value: 1 },
  { name: 'an object', value: {} },
];
for (const { name, value } of garbageInsolvent) {
  test(`isFinanceBalanceSheetPatch REJECTS the whole patch when insolvent is ${name}`, () => {
    const patch = { schemaVersion: FINANCE_SCHEMA_VERSION, insolvencyMonths: 3, insolvent: value };
    assert.equal(isFinanceBalanceSheetPatch(patch), false);
    assert.equal(decodeFinanceBalanceSheetPatch(patch), null);
  });
}

// ---------------------------------------------------------------------
// 2. newsFeed.ts observer: exactly one start entry, exactly one clear
//    entry, across a 3-month starve, null-source discipline.
// ---------------------------------------------------------------------

test('observeNews emits exactly one entry when insolvency starts, none while it merely persists, and exactly one when it clears', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  let ring = [];

  // Clean months: no entry.
  for (let month = 1; month <= 2; month++) {
    ring = observeNews({ ...emptySources(month), insolvency: { months: 0, insolvent: false } }, tracker, ring, seq);
  }
  assert.equal(ring.length, 0, 'clean months must not append any insolvency entry');

  // Two starved-but-not-yet-insolvent months: still no entry (insolvent
  // only flips true at month 3 in production, matching BUG-759's own
  // 3-consecutive-months contract) — this file drives the wire signal
  // directly rather than re-deriving that threshold.
  ring = observeNews({ ...emptySources(3), insolvency: { months: 1, insolvent: false } }, tracker, ring, seq);
  ring = observeNews({ ...emptySources(4), insolvency: { months: 2, insolvent: false } }, tracker, ring, seq);
  assert.equal(ring.length, 0, 'a rising Months counter with insolvent still false must not append an entry');

  // Month 3: insolvent flips true — exactly one START entry.
  ring = observeNews({ ...emptySources(5), insolvency: { months: 3, insolvent: true } }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'insolvency activation must append exactly one entry');
  assert.equal(ring[0].source, 'insolvency');
  assert.equal(ring[0].severity, 'error');
  assert.match(ring[0].text, /insolvent/i);
  assert.match(ring[0].text, /3/);

  // Month 4: still insolvent, Months keeps climbing — must NOT append a
  // second entry (only the start/clear TRANSITIONS get an entry).
  ring = observeNews({ ...emptySources(6), insolvency: { months: 4, insolvent: true } }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'a persisting insolvency (even with a rising Months figure) must not append a second entry');

  // Observed TWICE with the identical shape but a different reference
  // (React 18 StrictMode's double render-invocation) — still no duplicate.
  const month5 = { months: 5, insolvent: true };
  ring = observeNews({ ...emptySources(7), insolvency: month5 }, tracker, ring, seq);
  ring = observeNews({ ...emptySources(7), insolvency: { ...month5 } }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'StrictMode double-render-safe: re-observing an ongoing insolvency never duplicates');

  // Clear transition: exactly one entry, and never again on subsequent
  // clean readings. Round finding F3 (opus-round-bug769), honest
  // disclosure: FinanceAPI.gameOver LATCHES once set (RecordMonthResult
  // never clears it) while InsolvencyMonths resets on the next met month
  // — so there is NO in-place "engine recovery" from insolvency in a
  // single running session; a real (months:0, insolvent:false) reading
  // on the wire is only reachable via a Load/New Game that resets
  // FinanceAPI's underlying state (finance's resetForLoad participant).
  // This unit test exercises observeNews's pure transition logic against
  // that wire shape directly, independent of how production reaches it.
  ring = observeNews({ ...emptySources(8), insolvency: { months: 0, insolvent: false } }, tracker, ring, seq);
  assert.equal(ring.length, 2, 'the clear transition must append exactly one entry');
  assert.equal(ring[0].source, 'insolvency');
  assert.equal(ring[0].severity, 'success');
  assert.match(ring[0].text, /resolved|recovered/i);

  ring = observeNews({ ...emptySources(9), insolvency: { months: 0, insolvent: false } }, tracker, ring, seq);
  assert.equal(ring.length, 2, 'a subsequent clean month must not append yet another clear entry');
});

// Mirrors bug-723's own P1 re-round finding: null and a real
// insolvent:false reading are NOT interchangeable.
test('observeNews treats a null insolvency source (no live data) as UNKNOWN, never itself triggering a start or clear', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  let ring = observeNews(emptySources(1), tracker, [], seq); // insolvency omitted entirely
  assert.equal(ring.length, 0);
  ring = observeNews({ ...emptySources(2), insolvency: null }, tracker, ring, seq);
  assert.equal(ring.length, 0, 'an explicit null source must not fire a phantom clear entry when nothing was ever active');
});

test('a disconnect (null) mid-insolvency must NOT announce resolution, and reconnecting insolvent must not duplicate the start', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  let ring = [];

  ring = observeNews({ ...emptySources(1), insolvency: { months: 3, insolvent: true } }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'precondition: insolvency must have started');
  assert.equal(ring[0].severity, 'error');

  // Disconnect: the live-engine feed goes null. The city is STILL
  // insolvent — nothing about the underlying FinanceAPI state changed,
  // only the connection did. This must NOT append a "resolved" entry.
  ring = observeNews({ ...emptySources(2), insolvency: null }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'a disconnect (null) must NOT fire the clear-edge');
  assert.equal(ring[0].severity, 'error', 'the original insolvency entry must be unchanged, not replaced by a phantom resolution');

  // Reconnect: still insolvent — must NOT duplicate the start entry.
  ring = observeNews({ ...emptySources(3), insolvency: { months: 4, insolvent: true } }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'reconnecting to an ONGOING insolvency must not append a duplicate start entry');

  // A genuinely real recovered reading DOES fire exactly one resolved entry.
  ring = observeNews({ ...emptySources(4), insolvency: { months: 0, insolvent: false } }, tracker, ring, seq);
  assert.equal(ring.length, 2, 'a real (non-null) recovered reading must fire exactly one resolved entry');
  assert.equal(ring[0].severity, 'success');
});

// ---------------------------------------------------------------------
// 3. Mutation proof: a broken start-transition detector is caught.
// ---------------------------------------------------------------------

test('MUTATION: breaking the insolvency start-transition guard is caught by the test above', () => {
  const { failed, output, crashed } = runMutantSelfReinvoke({
    targetRelPath: path.join('sim', 'newsFeed.ts'),
    mutate: (original) => {
      const guard = 'if (insActive && !tracker.insolvencyActive) {';
      assert.ok(original.includes(guard), 'precondition: the start-transition guard is present in newsFeed.ts');
      const buggyGuard = 'if (false) {';
      return original.replace(guard, buggyGuard);
    },
    testFileAbsPath: fileURLToPath(import.meta.url),
    testNamePattern: 'observeNews emits exactly one entry when insolvency starts',
  });
  assert.equal(crashed, false, `mutant child crashed unexpectedly:\n${output}`);
  assert.equal(failed, true, `mutant escaped — disabling the start-transition guard did not fail the targeted test:\n${output}`);
});
