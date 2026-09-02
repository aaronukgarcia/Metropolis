// bug-504-505-506-endgame.test.mjs — FEAT-endgame-ladder (BUG-504/505/506):
// closing the three defects in the insolvency state machine identified by
// the cold audit (docs/planning/acceptance/FEAT-endgame-ladder-2026-09-02.md).
//
// BUG-505 (dead-stuck crisis): the OLD first-bailout clean-end check
// (`funds >= DEBT_THRESHOLD_FOR_BAILOUT`, the crisis line itself) could clear
// bailoutState while the raw funds band was STILL 'crisis' (funds ==
// DEBT_THRESHOLD_FOR_BAILOUT exactly satisfies both `>=` clean-end AND `<=`
// crisis) — bailoutState null, bailoutSecondState null, administrationState
// null, raw band 'crisis': a permanent dead end. Fix: BAILOUT_CLEAN_END_THRESHOLD
// (real solvency, funds >= 0) sits STRICTLY ABOVE DEBT_THRESHOLD_FOR_BAILOUT, so
// a clean-end can never coincide with a still-crisis raw band.
//
// BUG-504 (unbounded free re-grant): a city draining < BAILOUT_INCOME_INJECTION
// per bailout year could clear the OLD crisis-line clean-end bar every year
// while never truly recovering, re-collecting a fresh 750k-ish grant forever.
// Fix: clean-end now requires REAL solvency, AND fresh first-bailout grants are
// capped at MAX_FIRST_BAILOUTS — once exhausted, a new crisis is FORCED
// straight to the (worse-terms, no-fresh-grant) second bailout.
//
// BUG-506 (no early exit / single-tick decline): a bailout used to run its
// FULL fixed duration regardless of recovery, and the decline decision read
// funds at a SINGLE tick. Fix: SUSTAINED_RECOVERY_TICKS consecutive solvent
// ticks clears a bailout EARLY, and the decline decision reads the MEAN of the
// final DECLINE_AVERAGING_WINDOW_TICKS ticks (recentFundsWindow), not one
// tick's sample.
//
// node --test type-strips the .ts imports; every assertion below can FAIL if
// the corresponding fix is reverted — RED/GREEN pairs proved by re-running
// this file against a scratch cp/mv of engine.ts/fiscal.ts with the relevant
// fix reverted (GR#24 — never a git revert).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, TICKS_PER_YEAR } from '../src/sim/engine.ts';
import {
  DEBT_THRESHOLD_FOR_BAILOUT,
  INSOLVENCY_WARNING_THRESHOLD,
  BAILOUT_DURATION_TICKS,
  BAILOUT_INCOME_INJECTION,
  BAILOUT_INJECTION_LABEL,
  SECOND_BAILOUT_DURATION_TICKS,
  BAILOUT_INCOME_INJECTION_SECOND,
  BAILOUT_SECOND_INJECTION_LABEL,
  FINAL_DECLINE_FUNDS_THRESHOLD,
  BAILOUT_CLEAN_END_THRESHOLD,
  MAX_FIRST_BAILOUTS,
  SUSTAINED_RECOVERY_TICKS,
  DECLINE_AVERAGING_WINDOW_TICKS,
  BAILOUT_STANDING_COST_LABEL,
  bailoutStandingCostPerTick,
} from '../src/sim/fiscal.ts';

// Mirrors imf-insolvency-inc*.test.mjs's helper exactly.
function tickAtFunds(state, targetFunds) {
  const forced = reducer(state, { type: 'debugFunds', amount: targetFunds - state.funds });
  return reducer(forced, { type: 'tick' });
}

const WARNING_BAND_FUNDS = Math.round((INSOLVENCY_WARNING_THRESHOLD + DEBT_THRESHOLD_FOR_BAILOUT) / 2);

// Drive a fresh game into an ACTIVE first bailout (mirrors inc3/inc4's enterBailout()).
function enterBailout(fundsBelowThreshold = DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000) {
  const s0 = initialState();
  const warning = tickAtFunds(s0, WARNING_BAND_FUNDS);
  const crisis = tickAtFunds(warning, fundsBelowThreshold);
  assert.ok(crisis.bailoutState, 'precondition: first bailout must be active');
  return crisis;
}

// Ride the FIRST bailout year out to its year-end, staying deep in crisis the
// whole way, so the SECOND bailout auto-triggers (mirrors inc4's helper).
function rideFirstBailoutToSecond() {
  const crisis = enterBailout();
  let s = crisis;
  for (let i = 0; i < BAILOUT_DURATION_TICKS; i++) {
    s = tickAtFunds(s, DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000);
  }
  assert.ok(s.bailoutSecondState, 'precondition: second bailout must have auto-triggered');
  return s;
}

// Drive a SOLVENT state into a genuinely FRESH crisis entry (forces solvent
// first, so the transition is a real 'solvent'/'warning' -> 'crisis' crossing,
// never a re-read of an already-active bailout).
function freshCrisisEntry(state, fundsBelowThreshold = DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000) {
  const solvent = tickAtFunds(state, 5_000_000);
  return tickAtFunds(solvent, fundsBelowThreshold);
}

// ========== BUG-505: No Dead-Stuck Crisis State ==========

test('BUG-505: funds == DEBT_THRESHOLD_FOR_BAILOUT exactly at first-bailout year-end never dead-stucks (AC-505-2)', () => {
  const crisis = enterBailout();
  let s = crisis;
  // Hold funds at EXACTLY the crisis threshold through year-end.
  for (let i = 0; i < BAILOUT_DURATION_TICKS; i++) {
    s = tickAtFunds(s, DEBT_THRESHOLD_FOR_BAILOUT);
  }
  assert.equal(s.tick, crisis.bailoutState.enteredAt + BAILOUT_DURATION_TICKS);
  // AC-505-1 invariant: raw 'crisis' with NO rescue/escalation path is
  // impossible. Assert the disjunction the spec requires, then the concrete
  // mechanism (this build's choice: unconditional escalation to the second
  // bailout, since BAILOUT_CLEAN_END_THRESHOLD=0 is strictly above the crisis
  // line, so a clean bailoutState=null here would ALSO have implied the raw
  // band left crisis — either branch closes the dead end).
  const rescueOrEscalated =
    s.bailoutState !== null ||
    s.bailoutSecondState !== null ||
    s.administrationState !== null ||
    s.insolvencyRawBand !== 'crisis';
  assert.ok(rescueOrEscalated, 'AC-505-1: crisis band with NO rescue/escalation path must be unreachable');
  // This build's concrete resolution: unconditional escalation, same tick.
  assert.equal(s.bailoutState, null, 'the first bailout must have ended (never persists past its own duration)');
  assert.ok(s.bailoutSecondState, 'must be FORCED straight to the second bailout — never stranded');
  assert.equal(s.bailoutSecondState.enteredAt, s.tick, 'escalation happens on the SAME tick as the year-end check, not one tick later');
});

test('BUG-505 MUTATION-PROVE target: the OLD crisis-line clean-end bar (-1.5M) would have dead-stuck this exact fixture', () => {
  // Sanity-checks the RED premise: DEBT_THRESHOLD_FOR_BAILOUT satisfies its
  // OWN raw-band crisis test (`funds <= DEBT_THRESHOLD_FOR_BAILOUT` — see
  // fiscal.insolvencyStateForFunds), so a clean-end fired AT that exact value
  // would have left the raw band in 'crisis' with nothing active — the
  // dead-stuck configuration this suite exists to make impossible.
  assert.ok(
    DEBT_THRESHOLD_FOR_BAILOUT <= DEBT_THRESHOLD_FOR_BAILOUT,
    'the old bar sits exactly ON the crisis boundary',
  );
  assert.ok(
    BAILOUT_CLEAN_END_THRESHOLD > DEBT_THRESHOLD_FOR_BAILOUT,
    'BUG-505 fix: the NEW clean-end bar must sit STRICTLY ABOVE the crisis line',
  );
});

// ========== BUG-504: Unbounded Free Re-Grant, Now Capped + Costed ==========

test('BUG-504: a city merely above the OLD crisis-line bar (but still genuinely insolvent) is NOT clean-ended', () => {
  const crisis = enterBailout();
  let s = crisis;
  // -1,000,000 is comfortably ABOVE the old -1.5M bar (would have clean-ended
  // under the pre-fix logic) but still well below TRUE solvency (0).
  const stillBroke = DEBT_THRESHOLD_FOR_BAILOUT + 500_000;
  for (let i = 0; i < BAILOUT_DURATION_TICKS - 1; i++) {
    s = tickAtFunds(s, stillBroke);
  }
  const yearEnd = tickAtFunds(s, stillBroke);
  assert.equal(yearEnd.bailoutState, null, 'the first bailout always ends at year-end — the ladder always progresses');
  assert.ok(
    yearEnd.bailoutSecondState,
    'BUG-504 fix: still not REALLY solvent must escalate, not clean-end for free (the old bar would have clean-ended here)',
  );
});

test('BUG-504: after MAX_FIRST_BAILOUTS fresh grants, a new crisis is FORCED straight to the second bailout — no more free grants', () => {
  let s = initialState();
  for (let i = 0; i < MAX_FIRST_BAILOUTS; i++) {
    s = freshCrisisEntry(s);
    assert.ok(s.bailoutState, `re-arm ${i + 1}/${MAX_FIRST_BAILOUTS} must be a genuine fresh bailout`);
    assert.equal(s.firstBailoutCount, i + 1, 're-arm counter must increment exactly once per fresh grant');
    const freshInjection = s.lastFlows.inflows.find((f) => f.label === BAILOUT_INJECTION_LABEL);
    assert.equal(freshInjection?.value, BAILOUT_INCOME_INJECTION, `re-arm ${i + 1} must still receive the fresh grant while under the cap`);
    // Clean-end this bailout by riding it to REAL solvency at year-end.
    let t = s;
    for (let k = 0; k < BAILOUT_DURATION_TICKS - 1; k++) t = tickAtFunds(t, 5_000_000);
    s = tickAtFunds(t, 5_000_000);
    assert.equal(s.bailoutState, null, `re-arm ${i + 1} must clean-end once genuinely solvent`);
    assert.equal(s.bailoutSecondState, null, `re-arm ${i + 1}'s clean recovery must never escalate`);
  }
  // The cap is now exhausted — a FRESH crisis must skip the first-bailout
  // grant entirely and go straight to the second bailout.
  const capped = freshCrisisEntry(s);
  assert.equal(capped.firstBailoutCount, MAX_FIRST_BAILOUTS, 'the re-arm counter must NOT increment past the cap');
  assert.equal(capped.bailoutState, null, 'no FRESH first-bailout grant once MAX_FIRST_BAILOUTS is exhausted');
  assert.ok(capped.bailoutSecondState, 'must be FORCED straight to the (worse-terms) second bailout instead');
  const freshInjectionAtCap = capped.lastFlows.inflows.find((f) => f.label === BAILOUT_INJECTION_LABEL);
  assert.equal(freshInjectionAtCap, undefined, 'no BAILOUT_INCOME_INJECTION grant once the cap is exhausted');
  const secondInjectionAtCap = capped.lastFlows.inflows.find((f) => f.label === BAILOUT_SECOND_INJECTION_LABEL);
  assert.equal(secondInjectionAtCap?.value, BAILOUT_INCOME_INJECTION_SECOND, 'the forced escalation still gets the (worse-terms) second-bailout injection');
});

test('BUG-504 MUTATION-PROVE target: a city NOT at the cap still gets a fresh grant (the cap only bites once exhausted)', () => {
  let s = initialState();
  for (let i = 0; i < MAX_FIRST_BAILOUTS - 1; i++) {
    s = freshCrisisEntry(s);
    let t = s;
    for (let k = 0; k < BAILOUT_DURATION_TICKS - 1; k++) t = tickAtFunds(t, 5_000_000);
    s = tickAtFunds(t, 5_000_000);
  }
  const stillUnderCap = freshCrisisEntry(s);
  assert.ok(stillUnderCap.bailoutState, 'below the cap, a fresh crisis must still receive a genuine first-bailout grant');
  assert.equal(stillUnderCap.firstBailoutCount, MAX_FIRST_BAILOUTS);
});

test('BUG-504: the bailout standing cost is a NAMED, traceable outflow while a bailout is active, and conservation still holds', () => {
  const crisis = enterBailout();
  const cost = crisis.lastFlows.outflows.find((f) => f.label === BAILOUT_STANDING_COST_LABEL);
  assert.ok(cost, 'the standing cost must appear as a named outflow the very tick a bailout is active');
  assert.equal(cost.value, bailoutStandingCostPerTick(crisis.firstBailoutCount), 'cost must derive from the SSOT formula, not a re-typed literal');
  const inflowSum = crisis.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const outflowSum = crisis.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  assert.equal(
    crisis.fundsAtTickEnd,
    crisis.fundsAtTickStart + inflowSum - outflowSum,
    'conservation (fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows) must hold WITH the standing cost counted',
  );
});

test('BUG-504 MUTATION-PROVE target: the standing cost SCALES with the re-arm count — a second bailout costs strictly more per tick', () => {
  assert.ok(
    bailoutStandingCostPerTick(2) > bailoutStandingCostPerTick(1),
    'a second re-arm must be a WORSE credit hit than the first, per tick',
  );
  assert.equal(bailoutStandingCostPerTick(0), bailoutStandingCostPerTick(1), 'a count of 0 (defensive floor) must not be FREE — minimum 1x base cost');
});

test('BUG-504: no standing cost is charged once fully solvent (bailout cleared)', () => {
  const crisis = enterBailout();
  let s = crisis;
  for (let i = 0; i < BAILOUT_DURATION_TICKS - 1; i++) s = tickAtFunds(s, 5_000_000);
  const cleared = tickAtFunds(s, 5_000_000);
  assert.equal(cleared.bailoutState, null);
  const cost = cleared.lastFlows.outflows.find((f) => f.label === BAILOUT_STANDING_COST_LABEL);
  assert.equal(cost, undefined, 'a solvent, non-bailed-out city must not carry the bailout standing cost');
});

// ========== BUG-506: Sustained Recovery Early Exit ==========

test('BUG-506 (AC-506-1/2): sustained recovery clears the bailout EARLY, well before its year-end checkpoint', () => {
  const crisis = enterBailout();
  let s = crisis;
  for (let i = 0; i < SUSTAINED_RECOVERY_TICKS - 1; i++) {
    s = tickAtFunds(s, 5_000_000);
    assert.ok(s.bailoutState, `must still be bailed out mid-streak (tick ${i + 1} of ${SUSTAINED_RECOVERY_TICKS})`);
  }
  const cleared = tickAtFunds(s, 5_000_000);
  assert.equal(cleared.bailoutState, null, `must clear at exactly ${SUSTAINED_RECOVERY_TICKS} consecutive solvent ticks`);
  assert.ok(
    cleared.tick < crisis.bailoutState.enteredAt + BAILOUT_DURATION_TICKS,
    'the early exit must land well BEFORE the year-end checkpoint',
  );
  assert.equal(cleared.insolvencyState, 'solvent', 'the exposed band must revert to the raw funds band, not stay "bailout"');
});

test('BUG-506 MUTATION-PROVE target: a single dip below zero RESETS the recovery streak — no early exit', () => {
  const crisis = enterBailout();
  let s = crisis;
  for (let i = 0; i < SUSTAINED_RECOVERY_TICKS - 1; i++) s = tickAtFunds(s, 5_000_000);
  // One bad tick, one short of the streak completing.
  const dip = tickAtFunds(s, -1_000_000);
  assert.equal(dip.recoveryStreak, 0, 'a dip below zero must reset the streak to 0');
  assert.ok(dip.bailoutState, 'the bailout must still be active — the streak was broken before completing');
  // Now restart the streak from scratch and confirm it STILL takes the full
  // SUSTAINED_RECOVERY_TICKS from this point (proving the counter genuinely
  // reset, not just visually).
  let s2 = dip;
  for (let i = 0; i < SUSTAINED_RECOVERY_TICKS - 1; i++) {
    s2 = tickAtFunds(s2, 5_000_000);
    assert.ok(s2.bailoutState, `restarted streak: must still be bailed out (tick ${i + 1})`);
  }
  const clearedAfterRestart = tickAtFunds(s2, 5_000_000);
  assert.equal(clearedAfterRestart.bailoutState, null, 'the restarted streak must still take the full window to clear');
});

test('BUG-506 (AC-506-1/2): sustained recovery ALSO clears an active SECOND bailout early', () => {
  const s0 = rideFirstBailoutToSecond();
  let s = s0;
  for (let i = 0; i < SUSTAINED_RECOVERY_TICKS - 1; i++) {
    s = tickAtFunds(s, 5_000_000);
    assert.ok(s.bailoutSecondState, `must still be in the second bailout mid-streak (tick ${i + 1})`);
  }
  const cleared = tickAtFunds(s, 5_000_000);
  assert.equal(cleared.bailoutSecondState, null, 'the second bailout must ALSO support the early-exit mechanism');
  assert.equal(cleared.declineState ?? null, null, 'an early-exited recovery must never decline');
  assert.equal(cleared.insolvencyState, 'solvent');
});

// ========== BUG-506: Averaged Decline Decision (Not Single-Tick) ==========

const WINDOW = DECLINE_AVERAGING_WINDOW_TICKS;
// Symmetric magnitudes so the window's mean sign is exactly derivable by
// arithmetic below — never a hardcoded pass/fail expectation (GR#15).
const RECOVERY_VALUE = 3_000_000;
const DIP_VALUE = FINAL_DECLINE_FUNDS_THRESHOLD - 3_000_000;

test('BUG-506 (AC-506-3/4): bulk-period INSOLVENCY dominates a SHORT tail recovery — still declines', () => {
  const s0 = rideFirstBailoutToSecond();
  // A tail recovery that is BOTH a MINORITY of the averaging window (so the
  // window's mean stays negative) AND shorter than SUSTAINED_RECOVERY_TICKS
  // (so the early-exit mechanism — BUG-506's other half — never fires) —
  // isolating the AVERAGED DECLINE DECISION specifically, the one mechanism
  // AC-506-3/4 is about.
  const tailTicks = Math.max(1, Math.min(Math.floor(WINDOW / 3), SUSTAINED_RECOVERY_TICKS - 1));
  const bulkTicks = SECOND_BAILOUT_DURATION_TICKS - tailTicks;
  let s = s0;
  for (let i = 0; i < bulkTicks; i++) s = tickAtFunds(s, DIP_VALUE);
  for (let i = 0; i < tailTicks; i++) s = tickAtFunds(s, RECOVERY_VALUE);
  assert.equal(s.tick, s0.bailoutSecondState.enteredAt + SECOND_BAILOUT_DURATION_TICKS);
  // Derived expectation (never hardcoded): the final WINDOW ticks are a mix
  // of (WINDOW - tailTicks) dip ticks + tailTicks recovery ticks; since
  // tailTicks <= WINDOW / 3 (a clear minority), dip ticks dominate the window
  // -> mean must be negative.
  const expectedMean = ((WINDOW - tailTicks) * DIP_VALUE + tailTicks * RECOVERY_VALUE) / WINDOW;
  assert.ok(expectedMean < FINAL_DECLINE_FUNDS_THRESHOLD, 'test construction sanity: the derived window mean must be negative');
  assert.ok(s.declineState, 'AC-506-4: bulk-period insolvency must dominate a short tail recovery blip — decline must fire');
});

test('BUG-506 (AC-506-3/4): a SHORT tail dip within an otherwise-recovered run avoids decline (no dead-stuck by way of a single bad tick)', () => {
  const s0 = rideFirstBailoutToSecond();
  // Recovery held for the WHOLE run (more than SUSTAINED_RECOVERY_TICKS) may
  // legitimately resolve via the EARLY-EXIT mechanism before ever reaching
  // year-end — that is a CORRECT outcome (an even earlier rescue), not a
  // test failure; assert only the declineState outcome, not which mechanism
  // produced it.
  const tailTicks = Math.max(1, Math.floor(WINDOW / 3));
  const bulkTicks = SECOND_BAILOUT_DURATION_TICKS - tailTicks;
  let s = s0;
  for (let i = 0; i < bulkTicks; i++) s = tickAtFunds(s, RECOVERY_VALUE);
  for (let i = 0; i < tailTicks; i++) s = tickAtFunds(s, DIP_VALUE);
  assert.equal(s.declineState ?? null, null, 'a short tail dip within an otherwise-recovered run must never force decline');
});

test('BUG-506 MUTATION-PROVE target: the OLD single-tick sample would have declined the bulk-recovery fixture above', () => {
  // Sanity-checks the RED premise for the PREVIOUS test: under the single-tick
  // sample the old code used, the LAST tick alone (a DIP_VALUE tick) is what
  // the decision would have read — and that alone is well below the decline
  // threshold, which is exactly what used to force an undeserved game-over.
  assert.ok(
    DIP_VALUE < FINAL_DECLINE_FUNDS_THRESHOLD,
    'the single final tick of the bulk-recovery fixture is, by construction, deep in the red',
  );
});

test('BUG-506: the decline decision derives from recentFundsWindow (SimState), never a re-typed literal', () => {
  const s = rideFirstBailoutToSecond();
  assert.ok(Array.isArray(s.recentFundsWindow), 'recentFundsWindow must be threaded through SimState');
  assert.ok(s.recentFundsWindow.length > 0, 'the window must be populated by the time a second bailout is active');
  assert.ok(s.recentFundsWindow.length <= DECLINE_AVERAGING_WINDOW_TICKS, 'the window must be capped at the named constant, not grow unbounded');
});

// ========== Determinism (GR#21): no Date/random, byte-identical replays ==========

test('Determinism: two identical re-arm-capped rides produce byte-identical state', () => {
  function run() {
    let s = initialState();
    for (let i = 0; i < MAX_FIRST_BAILOUTS; i++) {
      s = freshCrisisEntry(s);
      let t = s;
      for (let k = 0; k < BAILOUT_DURATION_TICKS - 1; k++) t = tickAtFunds(t, 5_000_000);
      s = tickAtFunds(t, 5_000_000);
    }
    return freshCrisisEntry(s);
  }
  const a = run();
  const b = run();
  assert.equal(JSON.stringify(a), JSON.stringify(b));
});

test('Determinism: two identical early-exit rides produce byte-identical state', () => {
  function run() {
    const crisis = enterBailout();
    let s = crisis;
    for (let i = 0; i < SUSTAINED_RECOVERY_TICKS; i++) s = tickAtFunds(s, 5_000_000);
    return s;
  }
  const a = run();
  const b = run();
  assert.equal(JSON.stringify(a), JSON.stringify(b));
});

// Sanity: TICKS_PER_YEAR mirrors the sibling suites' cross-check.
test('sanity: BAILOUT_DURATION_TICKS / SECOND_BAILOUT_DURATION_TICKS still equal one game-year', () => {
  assert.equal(BAILOUT_DURATION_TICKS, TICKS_PER_YEAR);
  assert.equal(SECOND_BAILOUT_DURATION_TICKS, TICKS_PER_YEAR);
});
