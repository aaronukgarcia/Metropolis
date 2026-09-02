// bug-503-sellasset-conservation.test.mjs — BUG-503 (P1): sellAsset desyncs
// the tick-boundary conservation snapshot.
//
// sellAsset is the ONE between-tick action that extends lastFlows.inflows
// (AC-4 of FEAT-1972079923 inc2 requires this — the sale must be a NAMED,
// traceable inflow, not just a ledger row). Every OTHER between-tick money
// mutation (debugFunds, place, the bulldoze refund) deliberately leaves
// lastFlows untouched so the tick-boundary snapshot equation
//   fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows
// (consistency.ts's 'conservation.funds-vs-flows' check) stays balanced
// between ticks. Before the fix, sellAsset grew Σinflows by saleValue
// without moving fundsAtTickEnd, so the check falsely reported a
// −saleValue violation for the whole window until the next tick().
//
// node --test type-strips the .ts imports; every assertion below can FAIL if
// the fix (engine.ts sellAsset bumping fundsAtTickEnd by saleValue) is
// reverted — proved with a scratch cp/mv of engine.ts, never a git revert
// (GR#24).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, forcedSaleAssets } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { DEBT_THRESHOLD_FOR_BAILOUT } from '../src/sim/fiscal.ts';

// Force funds to targetFunds, then tick — mirrors imf-insolvency-inc2.test.mjs's helper.
function tickAtFunds(state, targetFunds) {
  const forced = reducer(state, { type: 'debugFunds', amount: targetFunds - state.funds });
  return reducer(forced, { type: 'tick' });
}

function enterBailout() {
  const s0 = initialState();
  // Two-step drop (warning band, then crisis) mirrors the AC-1/AC-2 entry
  // sequence used throughout imf-insolvency-inc2.test.mjs.
  const warning = tickAtFunds(s0, Math.round(DEBT_THRESHOLD_FOR_BAILOUT * 0.6));
  return tickAtFunds(warning, DEBT_THRESHOLD_FOR_BAILOUT * 2);
}

test('BUG-503: consistency is GREEN immediately after a between-tick sellAsset, and the sale still happened', () => {
  const entered = enterBailout();
  assert.ok(entered.bailoutState, 'precondition: must actually be in a bailout to force-sell');

  // Sanity: conservation holds right after tick-entry, before any sale.
  const preReport = runConsistencyChecks(entered);
  const preCheck = preReport.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.ok(preCheck.ok, `precondition: conservation must be green before the sale (${preCheck.detail})`);

  const target = forcedSaleAssets(entered)[0];
  assert.ok(target, 'precondition: at least one sellable asset');
  const afterSale = reducer(entered, { type: 'sellAsset', id: target.id });

  // The sale must actually have happened (funds up by exactly saleValue,
  // building gone) — a fix must not make the sale vanish from the ledger.
  assert.equal(afterSale.funds, entered.funds + target.saleValue, 'funds must increase by exactly the sale value');
  assert.ok(!afterSale.buildings.some((b) => b.id === target.id), 'the sold building must be gone');

  // BUG-503 core assertion: consistency must stay GREEN between ticks, with
  // no funds-vs-flows violation, immediately after the sale.
  const report = runConsistencyChecks(afterSale);
  const check = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.ok(
    check.ok,
    `conservation.funds-vs-flows must hold immediately after a between-tick sellAsset (${check.detail})`,
  );

  // And it must remain green after one more tick (the next tick's own
  // recompute of both snapshots must not re-introduce a discrepancy).
  const nextTick = reducer(afterSale, { type: 'tick' });
  const nextReport = runConsistencyChecks(nextTick);
  const nextCheck = nextReport.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.ok(nextCheck.ok, `conservation.funds-vs-flows must remain green after the following tick (${nextCheck.detail})`);
});

// MUTATION-PROVE target: reverting engine.ts's sellAsset fix (dropping the
// `fundsAtTickEnd: state.fundsAtTickEnd + saleValue` line) reproduces the
// original bug — Σinflows grows by saleValue but fundsAtTickEnd does not,
// so conservation.funds-vs-flows reports exactly a −saleValue violation.
// This is asserted directly here (not just "must be green" above) so the
// test can't accidentally pass for the wrong reason.
test('BUG-503 MUTATION-PROVE: without the fundsAtTickEnd fix, the delta is exactly -saleValue', () => {
  const entered = enterBailout();
  const target = forcedSaleAssets(entered)[0];
  const afterSale = reducer(entered, { type: 'sellAsset', id: target.id });

  // Simulate the pre-fix state by manually undoing just the fundsAtTickEnd
  // bump, leaving lastFlows (and everything else) exactly as the real
  // reducer produced it — this is what the code did before BUG-503 was fixed.
  const preFixSimulated = { ...afterSale, fundsAtTickEnd: afterSale.fundsAtTickEnd - target.saleValue };
  const report = runConsistencyChecks(preFixSimulated);
  const check = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.equal(check.ok, false, 'the pre-fix shape must trip the conservation check (proves the check can fail)');
  assert.match(check.detail, /delta: -?\d+/);
  const deltaMatch = check.detail.match(/delta: (-?\d+)/);
  assert.equal(Number(deltaMatch[1]), -target.saleValue, 'the reported delta must be exactly -saleValue');
});
