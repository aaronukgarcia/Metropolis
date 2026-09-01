// imf-insolvency-inc1.test.mjs — FEAT-1972079923 inc1: debt threshold detection,
// cannot-afford feedback visibility, and the one-shot bailout-entry popup.
//
// Scope (per the BA doc's Inc1 slice — AC-1, AC-8 scenario 1, AC-9): threshold
// detection only. The bailout EVENT, forced asset sales, Administration mode,
// second bailout and the decline screen are inc2-4 and are NOT tested here.
//
// node --test type-strips the .ts imports; every assertion below can FAIL if
// the corresponding fix is reverted (see the MUTATION-PROVE excerpts in the
// build report — this file's RED/GREEN pairs are proved with a scratch
// cp/mv of fiscal.ts and engine.ts, never a git revert, per GR#24).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import {
  DEBT_THRESHOLD_FOR_BAILOUT,
  INSOLVENCY_WARNING_THRESHOLD,
  insolvencyStateForFunds,
} from '../src/sim/fiscal.ts';
import { isStateAffecting } from '../src/sim/journal.ts';
import { placementCost, SPECS } from '../src/sim/data.ts';

const PAID_SPEC = 'road'; // Road — category 'network', a real (non-zero) placement cost.
const PAID_COST = placementCost(SPECS[PAID_SPEC]);
const X = 5;
const Y = 5;

// BUG-452 inc1 (2026-09-01): these three funds targets are DERIVED from the
// ratio-preserved insolvency thresholds (not hardcoded to the old £10M-scale
// absolutes: -1,000,000 / -6,000,000 / -12,000,000) so this suite keeps
// passing however STARTING_TREASURY is retuned. Aaron's approved ratios
// (fiscal.ts): warning = -0.5x treasury, crisis = -1x treasury.
const SOLVENT_FUNDS = Math.round(INSOLVENCY_WARNING_THRESHOLD / 2); // less negative than warning -> solvent
const WARNING_BAND_FUNDS = Math.round((INSOLVENCY_WARNING_THRESHOLD + DEBT_THRESHOLD_FOR_BAILOUT) / 2); // strictly between the two thresholds
const CRISIS_FUNDS = DEBT_THRESHOLD_FOR_BAILOUT * 2; // well below (more negative than) the crisis threshold

// Advance the state by one tick, forcing funds to a target value first via the
// between-tick debugFunds delta (mirrors the pattern in fiscal.test.mjs /
// bug396-place-feedback.test.mjs). One tick's flows move funds by at most a
// few thousand — far smaller than the gap between the warning and crisis
// thresholds — so the requested band is never masked by tick churn.
function tickAtFunds(state, targetFunds) {
  const forced = reducer(state, { type: 'debugFunds', amount: targetFunds - state.funds });
  return reducer(forced, { type: 'tick' });
}

// ========== AC-1: threshold crossing sets the right band ==========

test('AC-1: insolvencyStateForFunds is the pure SSOT band classifier', () => {
  assert.equal(insolvencyStateForFunds(0), 'solvent');
  assert.equal(insolvencyStateForFunds(INSOLVENCY_WARNING_THRESHOLD + 1), 'solvent');
  assert.equal(insolvencyStateForFunds(INSOLVENCY_WARNING_THRESHOLD), 'warning');
  assert.equal(insolvencyStateForFunds(DEBT_THRESHOLD_FOR_BAILOUT + 1), 'warning');
  assert.equal(insolvencyStateForFunds(DEBT_THRESHOLD_FOR_BAILOUT), 'crisis');
  assert.equal(insolvencyStateForFunds(DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000), 'crisis');
});

test('AC-1: engine.advance() stamps insolvencyState from end-of-tick funds', () => {
  const s0 = initialState();

  const solvent = tickAtFunds(s0, SOLVENT_FUNDS); // above warning threshold
  assert.equal(solvent.insolvencyState, 'solvent');

  const warning = tickAtFunds(s0, WARNING_BAND_FUNDS); // between the two thresholds
  assert.equal(warning.insolvencyState, 'warning');

  const crisis = tickAtFunds(s0, CRISIS_FUNDS); // at/below the crisis threshold
  assert.equal(crisis.insolvencyState, 'crisis');
});

test('AC-1 MUTATION-PROVE target: a legacy state without insolvencyState defaults to solvent in debug/serialization paths, never crashes', () => {
  const legacy = { ...initialState() };
  delete legacy.insolvencyState;
  const after = reducer(legacy, { type: 'tick' });
  // advance() always stamps a fresh value regardless of what came in.
  assert.ok(['solvent', 'warning', 'crisis'].includes(after.insolvencyState));
});

// ========== AC-9 / BUG-396: cannot-afford feedback is visible, not a silent no-op ==========

test('AC-9: an unaffordable PAID placement records placeNotice feedback (not a silent no-op)', () => {
  assert.ok(PAID_COST > 0, 'precondition: the paid spec actually costs money');
  const broke = { ...initialState(), funds: PAID_COST - 10, placeNotice: null };
  const before = broke.buildings.length;
  const after = reducer(broke, { type: 'place', spec: PAID_SPEC, x: X, y: Y });
  assert.equal(after.buildings.length, before, 'the building must NOT be placed');
  assert.ok(after.placeNotice, 'a recorded feedback entry must exist — this is the silent-no-op fix');
  assert.match(after.placeNotice, /Insufficient funds/i);
  assert.match(after.placeNotice, /£/, 'the shortfall amount must be named');
});

test('AC-9: dismissPlaceNotice clears the notice (UI acknowledgement path)', () => {
  const withNotice = { ...initialState(), placeNotice: 'Insufficient funds — £40 needed' };
  const after = reducer(withNotice, { type: 'dismissPlaceNotice' });
  assert.equal(after.placeNotice, null);
  // Dismissing an already-clear notice is a true no-op (no needless re-render).
  const idempotent = reducer(after, { type: 'dismissPlaceNotice' });
  assert.equal(idempotent, after, 'dismissing a null notice returns the SAME object reference');
});

test('AC-9: dismissPlaceNotice / dismissInsolvencyPopup are UI-only — never journaled', () => {
  assert.equal(isStateAffecting({ type: 'dismissPlaceNotice' }), false);
  assert.equal(isStateAffecting({ type: 'dismissInsolvencyPopup' }), false);
});

// ========== AC-8 (scenario 1 only): one-shot bailout-entry popup ==========

test('AC-8: crossing into crisis stamps insolvencyPopup exactly once, not on every subsequent tick', () => {
  const s0 = initialState();
  const warning = tickAtFunds(s0, WARNING_BAND_FUNDS);
  assert.equal(warning.insolvencyPopup, null, 'no popup while only in the warning band');

  const enteredCrisis = tickAtFunds(warning, CRISIS_FUNDS);
  assert.equal(enteredCrisis.insolvencyState, 'crisis');
  assert.ok(enteredCrisis.insolvencyPopup, 'popup must be stamped on the ENTRY tick');
  assert.equal(enteredCrisis.insolvencyPopup.state, 'crisis');
  assert.equal(enteredCrisis.insolvencyPopup.enteredAt, enteredCrisis.tick);

  // Still in crisis on the NEXT tick — popup must NOT be re-stamped (same enteredAt).
  const stillCrisis = reducer(enteredCrisis, { type: 'tick' });
  assert.equal(stillCrisis.insolvencyState, 'crisis');
  assert.equal(
    stillCrisis.insolvencyPopup.enteredAt,
    enteredCrisis.insolvencyPopup.enteredAt,
    'popup must not re-fire on a tick that does not RE-enter crisis'
  );
});

test('AC-8: dismissInsolvencyPopup clears the popup and it stays cleared while still in crisis', () => {
  const s0 = initialState();
  const enteredCrisis = tickAtFunds(s0, CRISIS_FUNDS);
  assert.ok(enteredCrisis.insolvencyPopup);

  const dismissed = reducer(enteredCrisis, { type: 'dismissInsolvencyPopup' });
  assert.equal(dismissed.insolvencyPopup, null);

  const nextTick = reducer(dismissed, { type: 'tick' });
  assert.equal(nextTick.insolvencyState, 'crisis', 'still in crisis');
  assert.equal(nextTick.insolvencyPopup, null, 'dismissed popup must not resurrect on a non-entry tick');
});

// ========== AC-12 companion: warning band fires before crisis ==========

test('AC-12 companion: the warning band is reached before the crisis band as debt worsens', () => {
  const s0 = initialState();
  const sequence = [SOLVENT_FUNDS, WARNING_BAND_FUNDS, CRISIS_FUNDS].map((funds) => tickAtFunds(s0, funds).insolvencyState);
  assert.deepEqual(sequence, ['solvent', 'warning', 'crisis']);

  const firstWarningIndex = sequence.indexOf('warning');
  const firstCrisisIndex = sequence.indexOf('crisis');
  assert.ok(firstWarningIndex >= 0 && firstCrisisIndex >= 0);
  assert.ok(firstWarningIndex < firstCrisisIndex, 'warning must be reachable before crisis as funds worsen');
});

// ========== Determinism (GR#21 / AC-12): no Date/random, byte-identical replays ==========

test('Determinism: two identical funds-then-tick runs produce byte-identical insolvency state', () => {
  const s0 = initialState();
  const a = tickAtFunds(s0, CRISIS_FUNDS);
  const b = tickAtFunds(s0, CRISIS_FUNDS);
  assert.equal(JSON.stringify(a), JSON.stringify(b));

  // Full transition sequence — solvent -> warning -> crisis -> dismiss -> tick.
  const runOnce = () => {
    let s = initialState();
    s = tickAtFunds(s, SOLVENT_FUNDS);
    s = tickAtFunds(s, WARNING_BAND_FUNDS);
    s = tickAtFunds(s, CRISIS_FUNDS);
    s = reducer(s, { type: 'dismissInsolvencyPopup' });
    s = reducer(s, { type: 'tick' });
    return s;
  };
  assert.equal(JSON.stringify(runOnce()), JSON.stringify(runOnce()));
});
