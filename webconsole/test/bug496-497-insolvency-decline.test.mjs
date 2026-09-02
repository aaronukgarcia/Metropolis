// bug496-497-insolvency-decline.test.mjs — BUG-496 (bailout popup re-fires every
// tick) + BUG-497 (decline hard-stop presents as a hang).
//
// BUG-496 root cause (engine.ts ~1429-1436 + ~1275-1292): the one-shot popup-stamp
// condition compared the RAW funds-band (insolvencyStateForFunds output) against
// `s.insolvencyState`, which is the EXPOSED/OVERLAID value ('decline' >
// 'administration' > 'bailout_second' take precedence over the raw 'crisis' band).
// While an overlay was active with funds still under the crisis threshold, the
// stored value was never literally 'crisis', so "transitioned into crisis"
// evaluated true EVERY TICK -> the popup was re-stamped every tick -> "I
// understand" (dismissInsolvencyPopup) was a one-tick reprieve. Fix: persist the
// raw band separately (`insolvencyRawBand`) and compare raw-to-raw.
//
// BUG-497 (1) root cause: the popup was never cleared when declineState was set,
// so the aria-modal InsolvencyPopup and the aria-modal DeclineScreen could both be
// mounted simultaneously — CSS stacking (not sim state) then decided which the
// player actually saw. Fix: force-clear insolvencyPopup on the tick declineState
// is set.
//
// node --test type-strips the .ts imports; every assertion below can FAIL if the
// corresponding fix is reverted — RED/GREEN proved with a scratch cp/mv of
// engine.ts (never a git revert, per GR#24) reverting the popup-stamp condition
// to the old raw-vs-overlay comparison form. See the build report for the
// RED run's failure output.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import {
  DEBT_THRESHOLD_FOR_BAILOUT,
  INSOLVENCY_WARNING_THRESHOLD,
  BAILOUT_DURATION_TICKS,
  ADMINISTRATION_DURATION_TICKS,
  SECOND_BAILOUT_DURATION_TICKS,
  FINAL_DECLINE_FUNDS_THRESHOLD,
} from '../src/sim/fiscal.ts';

// Mirrors imf-insolvency-inc*.test.mjs's helper exactly.
function tickAtFunds(state, targetFunds) {
  const forced = reducer(state, { type: 'debugFunds', amount: targetFunds - state.funds });
  return reducer(forced, { type: 'tick' });
}

const WARNING_BAND_FUNDS = Math.round((INSOLVENCY_WARNING_THRESHOLD + DEBT_THRESHOLD_FOR_BAILOUT) / 2);
const DEEP_CRISIS_FUNDS = DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000;

// Drive a fresh game into an ACTIVE first bailout (mirrors inc3/inc4's enterBailout()).
function enterBailout() {
  const s0 = initialState();
  const warning = tickAtFunds(s0, WARNING_BAND_FUNDS);
  const crisis = tickAtFunds(warning, DEEP_CRISIS_FUNDS);
  assert.ok(crisis.bailoutState, 'precondition: first bailout must be active');
  assert.ok(crisis.insolvencyPopup, 'precondition: popup must be stamped on the genuine crisis entry');
  return crisis;
}

// Ride into ADMINISTRATION mode (user-initiated), still deep in crisis funds —
// the exact scenario BUG-496 was filed against: an overlay active while the RAW
// band is still 'crisis'.
function enterAdministration() {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  assert.ok(admin.administrationState, 'precondition: administration must be active');
  assert.equal(admin.insolvencyState, 'administration', 'precondition: EXPOSED state overlays to administration');
  return admin;
}

// ========== BUG-496: popup stamps ONCE, even under an active overlay ==========

test('BUG-496: popup stamps exactly ONCE across 15 subsequent in-crisis ticks under an active BAILOUT overlay', () => {
  const crisis = enterBailout();
  const enteredAt = crisis.insolvencyPopup.enteredAt;
  let s = crisis;
  for (let i = 0; i < 15; i++) {
    s = tickAtFunds(s, DEEP_CRISIS_FUNDS); // stay deep in crisis funds the whole time
    assert.ok(s.insolvencyPopup, `tick ${i}: popup must still be present (not cleared)`);
    assert.equal(
      s.insolvencyPopup.enteredAt,
      enteredAt,
      `tick ${i}: popup must NOT be re-stamped — enteredAt must stay at the original crossing tick`,
    );
  }
});

test('BUG-496: popup stamps exactly ONCE across 15 subsequent in-crisis ticks under an active ADMINISTRATION overlay', () => {
  const admin = enterAdministration();
  const enteredAt = admin.insolvencyPopup.enteredAt;
  let s = admin;
  for (let i = 0; i < 15; i++) {
    s = tickAtFunds(s, DEEP_CRISIS_FUNDS);
    assert.equal(s.insolvencyState, 'administration', `tick ${i}: precondition — overlay must stay active`);
    assert.ok(s.insolvencyPopup, `tick ${i}: popup must still be present`);
    assert.equal(
      s.insolvencyPopup.enteredAt,
      enteredAt,
      `tick ${i}: popup must NOT be re-stamped while the administration overlay hides the raw 'crisis' band`,
    );
  }
});

test('BUG-496: dismiss stays dismissed across subsequent in-crisis ticks under an active overlay (the actual player complaint)', () => {
  const admin = enterAdministration();
  const dismissed = reducer(admin, { type: 'dismissInsolvencyPopup' });
  assert.equal(dismissed.insolvencyPopup, null, 'precondition: dismiss must clear the popup');
  let s = dismissed;
  for (let i = 0; i < 15; i++) {
    s = tickAtFunds(s, DEEP_CRISIS_FUNDS);
    assert.equal(
      s.insolvencyPopup,
      null,
      `tick ${i}: dismiss must be permanent for this crisis episode — the every-tick re-stamp bug reintroduces the popup here`,
    );
  }
});

test('BUG-496: a GENUINE re-entry (solvent -> crisis again, later) DOES re-stamp the popup', () => {
  const admin = enterAdministration();
  const dismissed = reducer(admin, { type: 'dismissInsolvencyPopup' });
  // Ride administration out to its year-end WITH FUNDS RECOVERED — reverts to 'solvent'.
  // BUG-504 Option A (2026-09-02): the administration-covered first-bailout
  // year-end "still broke" test now uses BAILOUT_CLEAN_END_THRESHOLD (real
  // solvency, funds >= 0), not the old crisis-line bar — forcing funds to
  // EXACTLY 0 is a razor's edge that one tick's own upkeep/interest can tip
  // back under 0 (a genuine, intended behavior change, not a test weakening).
  // Use a comfortably positive margin, matching the sibling AC-506 fixtures.
  let s = dismissed;
  for (let i = 0; i < ADMINISTRATION_DURATION_TICKS - 1; i++) {
    s = tickAtFunds(s, 5_000_000);
  }
  const recovered = tickAtFunds(s, 5_000_000);
  assert.equal(recovered.administrationState, null, 'precondition: administration must have ended');
  assert.equal(recovered.insolvencyState, 'solvent', 'precondition: must have genuinely recovered to solvent');
  assert.equal(recovered.insolvencyPopup, null, 'precondition: no stale popup once solvent');

  // Now genuinely re-enter crisis.
  const reCrisis = tickAtFunds(recovered, DEEP_CRISIS_FUNDS);
  assert.ok(reCrisis.insolvencyPopup, 'a genuine solvent -> crisis re-entry MUST re-stamp the popup');
  assert.equal(reCrisis.insolvencyPopup.enteredAt, reCrisis.tick, 'stamped at exactly this re-entry tick');
});

test('BUG-496 MUTATION-PROVE target: without the raw-band fix, a fresh crisis entry from solvent still stamps (sanity: the fix does not disable stamping altogether)', () => {
  const s0 = initialState();
  const crisis = tickAtFunds(s0, DEEP_CRISIS_FUNDS);
  assert.ok(crisis.insolvencyPopup, 'a genuine first crossing into crisis must still stamp');
  assert.equal(crisis.insolvencyPopup.enteredAt, crisis.tick);
});

// ========== BUG-496 sibling check: the bailout INJECTION one-shot is unaffected ==========
// (per the BOW item: guarded separately by prevBailoutState === null, so a second
// bailout cycle after year-end re-evaluation must not re-inject every tick).

test('BUG-496 sibling: the bailout income injection does NOT re-fire every tick while bailoutState stays active', () => {
  const crisis = enterBailout();
  let s = crisis;
  for (let i = 0; i < 10; i++) {
    s = tickAtFunds(s, DEEP_CRISIS_FUNDS);
    const injections = s.lastFlows.inflows.filter((f) => /BAILOUT/i.test(f.label));
    assert.equal(injections.length, 0, `tick ${i}: no re-injection while the same bailout window is still open`);
  }
});

test('BUG-496 sibling: the SECOND bailout auto-trigger cycle also does not re-inject every tick', () => {
  const crisis = enterBailout();
  let s = crisis;
  for (let i = 0; i < BAILOUT_DURATION_TICKS; i++) {
    s = tickAtFunds(s, DEEP_CRISIS_FUNDS);
  }
  assert.ok(s.bailoutSecondState, 'precondition: second bailout must have auto-triggered');
  const enteredAt = s.bailoutSecondState.enteredAt;
  for (let i = 0; i < 10; i++) {
    s = tickAtFunds(s, DEEP_CRISIS_FUNDS);
    assert.equal(s.bailoutSecondState.enteredAt, enteredAt, `tick ${i}: second bailout must not re-stamp`);
    const injections = s.lastFlows.inflows.filter((f) => /BAILOUT/i.test(f.label));
    assert.equal(injections.length, 0, `tick ${i}: no re-injection during the open second-bailout window`);
  }
});

// ========== BUG-497 (1): insolvencyPopup is force-cleared the tick declineState is set ==========

function rideToDecline() {
  let s = enterBailout();
  for (let i = 0; i < BAILOUT_DURATION_TICKS; i++) {
    s = tickAtFunds(s, DEEP_CRISIS_FUNDS);
  }
  assert.ok(s.bailoutSecondState, 'precondition: second bailout must be active');
  for (let i = 0; i < SECOND_BAILOUT_DURATION_TICKS; i++) {
    s = tickAtFunds(s, FINAL_DECLINE_FUNDS_THRESHOLD - 5_000_000);
  }
  assert.ok(s.declineState, 'precondition: must have declined');
  return s;
}

test('BUG-497 (1): entering decline force-clears insolvencyPopup to null', () => {
  const s = rideToDecline();
  assert.equal(s.insolvencyPopup, null, 'the popup must be null once the game is over — it is moot and must not contest DeclineScreen');
});

test('BUG-497 (1) MUTATION-PROVE target: a popup that WAS stamped just before decline would otherwise survive into the decline tick', () => {
  // Prove the clear is load-bearing: manufacture a state ARTIFICIALLY carrying a
  // stamped popup right up to the tick before decline fires, so the only thing
  // preventing it appearing in the final state is the BUG-497(1) force-clear.
  let s = enterBailout();
  for (let i = 0; i < BAILOUT_DURATION_TICKS; i++) {
    s = tickAtFunds(s, DEEP_CRISIS_FUNDS);
  }
  for (let i = 0; i < SECOND_BAILOUT_DURATION_TICKS - 1; i++) {
    s = tickAtFunds(s, FINAL_DECLINE_FUNDS_THRESHOLD - 5_000_000);
  }
  const stampedJustBefore = { ...s, insolvencyPopup: { state: 'crisis', enteredAt: s.tick } };
  assert.ok(stampedJustBefore.insolvencyPopup, 'precondition: popup artificially present the tick before decline');
  const declined = tickAtFunds(stampedJustBefore, FINAL_DECLINE_FUNDS_THRESHOLD - 5_000_000);
  assert.ok(declined.declineState, 'precondition: this tick must be the decline-triggering tick');
  assert.equal(
    declined.insolvencyPopup,
    null,
    'the injected popup must have been force-cleared on the SAME tick declineState was set',
  );
});
