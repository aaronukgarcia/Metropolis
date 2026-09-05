// bug-723-payroll-shortfall.test.mjs — BUG-723: FinanceAPI.
// RecordPayrollShortfall/PayrollShortfall (BUG-548) were set/cleared every
// month on the Go side but nothing outside a Go test ever read them — no
// compose consumer, no protocol delta, no player surface. This file proves
// the TS half of the fix:
//
//   1. wire.ts's isFinanceBalanceSheetPatch/decodeFinanceBalanceSheetPatch
//      coerce a garbage `payrollShortfall` section safely (GR#16: never
//      trust the wire's claimed shape) — a malformed section rejects the
//      WHOLE patch rather than partially trusting it.
//   2. newsFeed.ts's observeNews emits exactly ONE entry when a payroll
//      shortfall starts and exactly ONE when it clears across a
//      3-month-starve-then-recover sequence, is StrictMode-double-render
//      safe (re-observing the identical still-active amount does not
//      duplicate), and a mutation proof (testsupport/mutant.mjs) that a
//      broken transition-detector is actually caught by these assertions.

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
import { fmtMoney } from '../src/sim/utils.ts';

function emptySources(tick = 0) {
  return { notice: null, milestoneNotice: null, placeNotice: null, tick };
}

// BUG-452 rebase (2026-09-01): the wire's "amountMicropounds" field is
// STILL NAMED for the pre-rebase 1e-6 GBP/unit scale but the actual scale
// since the rebase is 1,000 units per pound — see
// internal/engine/finance/money.go's MicropoundsPerPound doc comment.
// Tests below use this constant rather than a hand-picked /1,000,000
// division so a future re-rebase only needs updating in one place.
const MICROPOUNDS_PER_POUND = 1_000;

// ---------------------------------------------------------------------
// 1. wire.ts decode-boundary garbage safety (GR#16).
// ---------------------------------------------------------------------

test('isFinanceBalanceSheetPatch accepts a well-formed payrollShortfall section', () => {
  const patch = {
    schemaVersion: FINANCE_SCHEMA_VERSION,
    payrollShortfall: { month: 7, amountMicropounds: 129000000, months: 2 },
  };
  assert.equal(isFinanceBalanceSheetPatch(patch), true);
  assert.deepEqual(decodeFinanceBalanceSheetPatch(patch), patch);
});

test('isFinanceBalanceSheetPatch accepts a patch with NO payrollShortfall section (pre-BUG-723 server / "no data this cycle")', () => {
  const patch = { schemaVersion: FINANCE_SCHEMA_VERSION };
  assert.equal(isFinanceBalanceSheetPatch(patch), true);
});

// BUG-723 round finding F6: the TS type declares `payrollShortfall?:
// FinancePayrollShortfallView | null` — null is a LEGAL value, not
// garbage, and must decode identically to the field being absent
// (undefined), not reject the whole patch.
test('isFinanceBalanceSheetPatch accepts payrollShortfall: null identically to it being absent (F6)', () => {
  const withNull = { schemaVersion: FINANCE_SCHEMA_VERSION, payrollShortfall: null };
  const withoutField = { schemaVersion: FINANCE_SCHEMA_VERSION };
  assert.equal(isFinanceBalanceSheetPatch(withNull), true, 'payrollShortfall: null must be accepted, not rejected as garbage');
  assert.deepEqual(decodeFinanceBalanceSheetPatch(withNull), withNull);
  assert.equal(
    isFinanceBalanceSheetPatch(withNull),
    isFinanceBalanceSheetPatch(withoutField),
    'null and absent must decode to the same accept/reject verdict'
  );
});

const garbagePayrollShortfalls = [
  { name: 'a bare number', value: 42 },
  { name: 'a string', value: 'shortfall' },
  { name: 'an array', value: [1, 2, 3] },
  { name: 'missing amountMicropounds', value: { month: 1, months: 1 } },
  { name: 'string amountMicropounds', value: { month: 1, amountMicropounds: '129000000', months: 1 } },
  { name: 'missing months', value: { month: 1, amountMicropounds: 129000000 } },
  { name: 'NaN-typed months (string)', value: { month: 1, amountMicropounds: 129000000, months: 'two' } },
  { name: 'missing month', value: { amountMicropounds: 129000000, months: 1 } },
];

for (const { name, value } of garbagePayrollShortfalls) {
  test(`isFinanceBalanceSheetPatch REJECTS the whole patch when payrollShortfall is ${name}`, () => {
    const patch = { schemaVersion: FINANCE_SCHEMA_VERSION, payrollShortfall: value };
    assert.equal(
      isFinanceBalanceSheetPatch(patch),
      false,
      `a garbage payrollShortfall (${name}) must reject the whole patch, not be silently coerced downstream`
    );
    assert.equal(decodeFinanceBalanceSheetPatch(patch), null);
  });
}

// ---------------------------------------------------------------------
// 2. newsFeed.ts observer: exactly one start entry, exactly one clear
//    entry, across a 3-month starve.
// ---------------------------------------------------------------------

test('observeNews emits exactly one entry when a payroll shortfall starts, none while it merely persists, and exactly one when it clears', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  let ring = [];

  // Clean months: no entry.
  for (let month = 1; month <= 2; month++) {
    ring = observeNews({ ...emptySources(month), payrollShortfall: { amountMicropounds: 0, months: 0 } }, tracker, ring, seq);
  }
  assert.equal(ring.length, 0, 'clean months must not append any payrollShortfall entry');

  // Starved month 1: exactly one START entry.
  ring = observeNews({ ...emptySources(3), payrollShortfall: { amountMicropounds: 129_000, months: 1 } }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'shortfall activation must append exactly one entry');
  assert.equal(ring[0].source, 'payrollShortfall');
  assert.equal(ring[0].severity, 'warning');
  // BUG-723 round finding F4: assert.match(/£129/) would ALSO match
  // "£129,000,000" (a 1000x-too-large figure from dividing by the wrong
  // scale) — a passing regex proves nothing about the actual magnitude.
  // Assert the FULL rendered string instead: 129,000 micropounds at the
  // real 1,000-per-pound scale (BUG-452) is exactly £129.
  const expectedPounds = fmtMoney(129_000 / MICROPOUNDS_PER_POUND);
  assert.equal(expectedPounds, '£129', 'test precondition: fmtMoney(129) renders as £129');
  assert.equal(
    ring[0].text,
    `Payroll shortfall: ${expectedPounds}. Treasury covering the gap.`,
    'full string match — a regex like /£129/ would also pass for a magnitude 1000x too large (£129,000)'
  );

  // Starved month 2: the amount and months figure both CHANGE (a real
  // month-to-month recompute), but this must NOT append a second entry
  // — only the start/clear TRANSITIONS get an entry, not every ongoing
  // month.
  ring = observeNews({ ...emptySources(4), payrollShortfall: { amountMicropounds: 141_000, months: 2 } }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'a persisting shortfall (even with a different amount) must not append a second entry');

  // Starved month 3 observed TWICE with the identical object shape but a
  // different reference (React 18 StrictMode's double render-invocation) —
  // still no duplicate.
  const month3 = { amountMicropounds: 141_000, months: 3 };
  ring = observeNews({ ...emptySources(5), payrollShortfall: month3 }, tracker, ring, seq);
  ring = observeNews({ ...emptySources(5), payrollShortfall: { ...month3 } }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'StrictMode double-render-safe: re-observing an ongoing shortfall never duplicates');

  // Recovery: exactly one CLEAR entry, and never again on subsequent
  // clean months.
  ring = observeNews({ ...emptySources(6), payrollShortfall: { amountMicropounds: 0, months: 0 } }, tracker, ring, seq);
  assert.equal(ring.length, 2, 'recovery must append exactly one clear entry');
  assert.equal(ring[0].source, 'payrollShortfall');
  assert.equal(ring[0].severity, 'success');
  assert.match(ring[0].text, /recovered/i);

  ring = observeNews({ ...emptySources(7), payrollShortfall: { amountMicropounds: 0, months: 0 } }, tracker, ring, seq);
  assert.equal(ring.length, 2, 'a subsequent clean month must not append yet another clear entry');
});

// BUG-723 re-round P1 (opus-reround-bug723 REJECT): null and a real zero
// reading are NOT interchangeable. null means "no live-engine data right
// now" (financeStatusTracker.ts's own doc comment; what LiveEngineBadge
// writes via reset() on unmount/disconnect, and what a fresh mount starts
// at). A real `{amountMicropounds: 0, months: 0}` means the engine is
// CONNECTED and just reported a genuinely clean month. Before this fix,
// both were folded into the identical `psActive=false` reading, so a mid-
// starve DISCONNECT (null) fired the exact same clear-edge as a real
// recovery — the feed announced "recovered" while the city was still
// starving.
test('observeNews treats a null payrollShortfall source (no live data) as UNKNOWN, never itself triggering a start or clear', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  let ring = observeNews(emptySources(1), tracker, [], seq); // payrollShortfall omitted entirely
  assert.equal(ring.length, 0);
  ring = observeNews({ ...emptySources(2), payrollShortfall: null }, tracker, ring, seq);
  assert.equal(ring.length, 0, 'an explicit null source must not fire a phantom clear entry when nothing was ever active');
});

test('BUG-723 P1 RED-PROOF: a disconnect (null) mid-starve must NOT announce recovery, and reconnecting with the SAME shortfall must not duplicate the start', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  let ring = [];

  // Start a real shortfall.
  ring = observeNews({ ...emptySources(1), payrollShortfall: { amountMicropounds: 100_000, months: 1 } }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'precondition: the shortfall must have started');
  assert.equal(ring[0].severity, 'warning');

  // Disconnect: the live-engine feed goes null (exactly what
  // financeStatusTracker.reset() writes on unmount/disconnect). The city
  // is STILL starving — nothing about the underlying FinanceAPI state
  // changed, only the connection did. This must NOT append a "recovered"
  // entry.
  ring = observeNews({ ...emptySources(2), payrollShortfall: null }, tracker, ring, seq);
  assert.equal(
    ring.length,
    1,
    'a disconnect (null) must NOT fire the clear-edge — the pre-fix bug announced "recovered" here while the city was still starving'
  );
  assert.equal(ring[0].severity, 'warning', 'the original warning entry must be unchanged, not replaced by a phantom recovery');

  // Reconnect: the SAME ongoing shortfall reasserts itself (a real,
  // non-null reading again). This must NOT duplicate the start entry —
  // the tracker's active flag was never cleared by the null in between.
  ring = observeNews({ ...emptySources(3), payrollShortfall: { amountMicropounds: 120_000, months: 2 } }, tracker, ring, seq);
  assert.equal(ring.length, 1, 'reconnecting to an ONGOING shortfall must not append a duplicate start entry');

  // A genuinely real zero reading (the engine, now connected, actually
  // reports a clean month) DOES fire exactly one recovered entry.
  ring = observeNews({ ...emptySources(4), payrollShortfall: { amountMicropounds: 0, months: 0 } }, tracker, ring, seq);
  assert.equal(ring.length, 2, 'a real (non-null) zero reading must fire exactly one recovered entry');
  assert.equal(ring[0].severity, 'success');
  assert.match(ring[0].text, /recovered/i);
});

// ---------------------------------------------------------------------
// 3. Mutation proof: a broken start-transition detector is caught.
// ---------------------------------------------------------------------

test('MUTATION: breaking the payrollShortfall start-transition guard is caught by the test above', () => {
  const { failed, output, crashed } = runMutantSelfReinvoke({
    targetRelPath: path.join('sim', 'newsFeed.ts'),
    mutate: (original) => {
      const guard = 'if (psActive && !tracker.payrollShortfallActive) {';
      assert.ok(original.includes(guard), 'precondition: the start-transition guard is present in newsFeed.ts');
      // Force the start branch to never fire — the exact "built but the
      // wiring is inert" shape BUG-723 itself was.
      const buggyGuard = 'if (false) {';
      return original.replace(guard, buggyGuard);
    },
    testFileAbsPath: fileURLToPath(import.meta.url),
    testNamePattern: 'observeNews emits exactly one entry when a payroll shortfall starts',
  });
  assert.equal(crashed, false, `mutant child crashed unexpectedly:\n${output}`);
  assert.equal(failed, true, `mutant escaped — disabling the start-transition guard did not fail the targeted test:\n${output}`);
});
