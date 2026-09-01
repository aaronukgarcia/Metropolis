// imf-insolvency-inc3.test.mjs — FEAT-1972079923 inc3: ADMINISTRATION MODE
// (the alternative to forced asset sales), the DISCRETIONARY-SPEND HARD BLOCK
// per Aaron's round-2 ruling (2026-08-31, recorded on the BOW item — OVERRIDES
// the BA criteria doc's stale "multiply ALL outflows" text), and the 360-tick
// re-evaluation.
//
// Scope (per the BUILD LANE brief's inc3 slice — AC-5, AC-6, AC-7 ONLY): enter
// administration as the bailout alternative, the discretionary block (place/
// policy — NOT upkeep/interest, which accrue in FULL), and the exactly-360-tick
// duration. The second bailout and decline screen are inc4 and are NOT tested
// here (see imf-insolvency-inc1/inc2.test.mjs for AC-1/AC-2/AC-3/AC-4/AC-8/AC-9).
//
// node --test type-strips the .ts imports; every assertion below can FAIL if
// the corresponding fix is reverted — RED/GREEN pairs proved with a scratch
// cp/mv of fiscal.ts/engine.ts/data.ts, never a git revert (GR#24).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, TICKS_PER_YEAR } from '../src/sim/engine.ts';
import {
  DEBT_THRESHOLD_FOR_BAILOUT,
  ADMINISTRATION_DURATION_TICKS,
  ADMINISTRATION_PLACE_BLOCKED_MESSAGE,
  ADMINISTRATION_POLICY_BLOCKED_MESSAGE,
} from '../src/sim/fiscal.ts';
import { isStateAffecting } from '../src/sim/journal.ts';
import { placementCost, SPECS, pickAutoSpec } from '../src/sim/data.ts';

// Advance the state by one tick, forcing funds to a target value first (mirrors
// imf-insolvency-inc1/inc2.test.mjs's helper exactly).
function tickAtFunds(state, targetFunds) {
  const forced = reducer(state, { type: 'debugFunds', amount: targetFunds - state.funds });
  return reducer(forced, { type: 'tick' });
}

function tickN(state, n) {
  let s = state;
  for (let i = 0; i < n; i++) s = reducer(s, { type: 'tick' });
  return s;
}

// Drive a fresh game into an ACTIVE bailout (bailoutState != null), the
// precondition for the 'enterAdministration' action (AC-5).
function enterBailout(fundsBelowThreshold = DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000) {
  const s0 = initialState();
  const warning = tickAtFunds(s0, -6_000_000);
  const crisis = tickAtFunds(warning, fundsBelowThreshold);
  assert.ok(crisis.bailoutState, 'precondition: bailout must be active');
  return crisis;
}

// FEAT-1972079882: any 'zones'-category structure is free to PLACE (placementCost
// returns 0), so a zone is the genuinely free scenario for AC-6 — 'road' itself is
// NOT free (see inc1's PAID_SPEC), it is 'network'-category and costs 40.
const FREE_SPEC = 'res_hut'; // zones-category — cost 0 via placementCost.
const FREE_COST = placementCost(SPECS[FREE_SPEC]);
const PAID_SPEC = 'road'; // network-category — cost 40 (mirrors inc1's PAID_SPEC).
const PAID_COST = placementCost(SPECS[PAID_SPEC]);

assert.equal(FREE_COST, 0, 'precondition: a zone spec is free (AC-6 scenario 2 basis)');
assert.ok(PAID_COST > 0, 'precondition: a real paid spec exists (AC-6 scenario 1 basis)');

// ========== Duration constant sanity (AC-7's "must equal TICKS_PER_YEAR") ==========

test('ADMINISTRATION_DURATION_TICKS equals TICKS_PER_YEAR (one game-year, AC-7)', () => {
  assert.equal(ADMINISTRATION_DURATION_TICKS, TICKS_PER_YEAR);
  assert.equal(ADMINISTRATION_DURATION_TICKS, 360);
});

// ========== AC-5: entering administration as the alternative to forced sales ==========

test('AC-5: enterAdministration is unavailable outside an active bailout (no button, no state)', () => {
  const s0 = initialState();
  assert.equal(s0.bailoutState ?? null, null, 'precondition: solvent, no bailout');
  const after = reducer(s0, { type: 'enterAdministration' });
  assert.equal(after, s0, 'must be a true no-op with no bailout active');
});

test('AC-5: enterAdministration during an active bailout stamps administrationState + clears bailoutState', () => {
  const crisis = enterBailout();
  const before = crisis.tick;
  const after = reducer(crisis, { type: 'enterAdministration' });

  assert.ok(after.administrationState, 'administrationState must be stamped');
  assert.equal(after.administrationState.enteredAt, before, 'entered at the CURRENT tick');
  assert.equal(after.bailoutState, null, 'FORCED ASSET SALES panel closes — bailoutState must clear');
  assert.equal(after.insolvencyState, 'administration', 'exposed insolvencyState reads administration');
});

test('AC-5: the city remains playable in administration — clock ticks, population can move', () => {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  const oneTickLater = reducer(admin, { type: 'tick' });
  assert.equal(oneTickLater.tick, admin.tick + 1, 'the clock must still advance');
  assert.equal(oneTickLater.administrationState.enteredAt, admin.administrationState.enteredAt, 'still the same administration window');
});

test('AC-5 MUTATION-PROVE target: entering administration twice does not re-stamp (idempotent, same enteredAt)', () => {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  const laterTick = tickN(admin, 5);
  const reenter = reducer(laterTick, { type: 'enterAdministration' });
  assert.equal(
    reenter.administrationState.enteredAt,
    admin.administrationState.enteredAt,
    'a second enterAdministration while already active must NOT reset the entry tick',
  );
});

test('enterAdministration is state-affecting — must be journaled for replay', () => {
  assert.equal(isStateAffecting({ type: 'enterAdministration' }), true);
});

// ========== AC-6 (Aaron's ruling): DISCRETIONARY-SPEND HARD BLOCK, NOT a multiplier ==========

test('AC-6: a PAID place() is blocked outright in administration, even with funds on hand', () => {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  // Give the city plenty of cash — the block must fire regardless of affordability.
  const flush = { ...admin, funds: PAID_COST * 1000 };
  const before = flush.buildings.length;
  const after = reducer(flush, { type: 'place', spec: PAID_SPEC, x: 3, y: 3 });

  assert.equal(after.buildings.length, before, 'the paid building must NOT be placed');
  assert.equal(after.placeNotice, ADMINISTRATION_PLACE_BLOCKED_MESSAGE, 'feedback must name Administration Mode, not a generic insufficient-funds message');
});

test('AC-6: a FREE (£0) place() still succeeds in administration', () => {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  const before = admin.buildings.length;
  // Pick empty coordinates unlikely to be occupied by the starter city.
  const after = reducer(admin, { type: 'place', spec: FREE_SPEC, x: 45, y: 45 });
  assert.equal(after.buildings.length, before + 1, 'a free placement must proceed under administration');
  assert.equal(after.placeNotice, null, 'no admin-blocked notice for a free placement');
});

test('AC-6 MUTATION-PROVE target: removing the administration check lets a paid building place while broke+admin', () => {
  // This test documents the exact behaviour the guard prevents: without the
  // `cost > 0 && state.administrationState` check, `place()` would fall through
  // to the ordinary funds check and SUCCEED once funds are flushed positive —
  // proving the discretionary block is a REAL gate, not a no-op.
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  // Flush funds to a comfortably POSITIVE balance (not just +cost*100 on top of
  // the deeply-negative crisis funds) so the mutation-prove placement's own
  // affordability gate cannot explain a failure either way.
  const flush = { ...admin, funds: PAID_COST * 1000 };
  const withoutAdminOverride = { ...flush, administrationState: null };
  // Empty tile away from the starter city (mirrors the free-place test's x:45,y:45).
  const wouldSucceed = reducer(withoutAdminOverride, { type: 'place', spec: PAID_SPEC, x: 47, y: 47 });
  assert.equal(
    wouldSucceed.buildings.length,
    withoutAdminOverride.buildings.length + 1,
    'precondition: with administrationState cleared, the SAME placement succeeds — proving the block above is what stops it',
  );
});

test('AC-6: upkeep and overdraft interest accrue in FULL under administration (no multiplier, per Aaron\'s ruling)', () => {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  const withAdmin = reducer(admin, { type: 'tick' });
  const withoutAdmin = reducer({ ...admin, administrationState: null }, { type: 'tick' });

  const outflowSum = (s) => s.lastFlows.outflows.reduce((a, o) => a + o.value, 0);
  assert.equal(
    outflowSum(withAdmin),
    outflowSum(withoutAdmin),
    'administration must NOT change the outflow totals — mandatory obligations are untouched (Aaron\'s ruling overrides the stale multiplier text)',
  );
  const interestOf = (s) => s.lastFlows.outflows.find((o) => o.label === 'Overdraft Interest')?.value ?? 0;
  if (interestOf(withoutAdmin) > 0) {
    assert.equal(interestOf(withAdmin), interestOf(withoutAdmin), 'overdraft interest must accrue in full, unreduced');
  }
});

test('AC-6: enacting a NEW policy is blocked in administration; turning an existing one OFF is not', () => {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  assert.equal(admin.policies.austerity, false, 'precondition: austerity starts off');

  const blockedOn = reducer(admin, { type: 'policy', id: 'austerity' });
  assert.equal(blockedOn.policies.austerity, false, 'enacting a new policy must be blocked');
  assert.equal(blockedOn.placeNotice, ADMINISTRATION_POLICY_BLOCKED_MESSAGE);

  // An already-on policy CAN be turned off (not a new discretionary spend).
  const withPolicyOn = { ...admin, policies: { ...admin.policies, austerity: true } };
  const turnedOff = reducer(withPolicyOn, { type: 'policy', id: 'austerity' });
  assert.equal(turnedOff.policies.austerity, false, 'turning OFF an existing policy must still work under administration');
});

// BUG-230 FIX: the previous fixture here was enterBailout()'s starter city —
// population 0, so serviceDemandOf(...) has NOTHING under-provided and
// pickAutoSpec ALWAYS returns null, admin guard present or not (vacuous —
// the attacker deleted the data.ts:2441 guard and both tests below still
// passed). A REAL shortfall requires population >= 5000 with NO nursery/
// school/GP/etc built (still true of the bailout fixture's roads-only
// buildings) — that starves every education/health/police coverage row to
// 0, so serviceDemandOf reports value=100 for 'nursery' (first in
// serviceCoverageOf's row order, so it wins the sort ties) with spec
// 'edu_nursery' (cost 1200 > 0) as a GENUINE paid candidate. Confirmed via
// scratch debug: fixture below yields { spec: 'edu_nursery' } (cost 1200)
// from pickAutoSpec with administrationState cleared, and null with it set.
function shortfallFixture() {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  // Funds comfortably positive so an affordability gate alone cannot explain
  // a null result; population high enough to clear earlyGameFactor's ramp
  // (caps at pop/50, so >=5000 is comfortably past 1.0) with a real
  // education/health/police shortfall (no such buildings exist in the
  // starter/bailout fixture).
  return { ...admin, funds: 999_999_999, population: 5000 };
}

test('AC-6: the advisor (pickAutoSpec) does not offer paid buildings under administration', () => {
  const flush = shortfallFixture();
  assert.ok(flush.administrationState, 'precondition: administration is active');
  // Every candidate row in this fixture (nursery/primary/college/gp/hosp/
  // police/water/power) maps to a PAID spec — there is no free alternative
  // for the advisor to fall back to — so the guard's correct effect here is
  // to skip every one of them and return null (proven NON-vacuous by the
  // MUTATION-PROVE companion below: the SAME fixture, admin cleared, returns
  // a real paid suggestion instead of null).
  const suggestion = pickAutoSpec(flush);
  assert.equal(suggestion, null, 'no candidate here is free, so the advisor must offer NOTHING under administration, not a paid building');
});

test('AC-6 MUTATION-PROVE target: without the admin guard, the advisor DOES offer a paid building (same fixture, funds allow it)', () => {
  const withoutAdminOverride = { ...shortfallFixture(), administrationState: null };
  const suggestion = pickAutoSpec(withoutAdminOverride);
  assert.ok(suggestion, 'precondition: the SAME real shortfall must still produce a suggestion once administration is cleared');
  const sp = SPECS[suggestion.spec];
  assert.ok(
    placementCost(sp) > 0,
    'with administrationState cleared, the advisor MUST offer the paid spec it was blocking — proving the guard above is load-bearing, not vacuous',
  );
});

// ========== AC-7: administration lasts exactly ADMINISTRATION_DURATION_TICKS ==========

test('AC-7: administration remains active for the full duration regardless of funds mid-window', () => {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  const enteredAt = admin.administrationState.enteredAt;

  const justBefore = tickN(admin, ADMINISTRATION_DURATION_TICKS - 1);
  assert.ok(justBefore.administrationState, 'administration must still be active one tick before year-end');
  assert.equal(justBefore.administrationState.enteredAt, enteredAt);
});

test('AC-7: administration ENDS at exactly N+ADMINISTRATION_DURATION_TICKS when funds have recovered', () => {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  const enteredAt = admin.administrationState.enteredAt;

  const justBefore = tickN(admin, ADMINISTRATION_DURATION_TICKS - 1);
  const solventAtYearEnd = tickAtFunds(justBefore, 0); // one more tick reaches enteredAt+360, funds solvent.
  assert.equal(solventAtYearEnd.tick, enteredAt + ADMINISTRATION_DURATION_TICKS);
  assert.equal(solventAtYearEnd.administrationState, null, 'administration must end at year-end');
  assert.notEqual(solventAtYearEnd.insolvencyState, 'administration', 'exposed state must revert to the funds band');
  assert.equal(solventAtYearEnd.insolvencyState, 'solvent', 'recovered funds → solvent');
});

test('AC-7 MUTATION-PROVE target: administration ENDS at year-end even while STILL BROKE (no indefinite freeze)', () => {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  const enteredAt = admin.administrationState.enteredAt;

  let s = admin;
  for (let i = 0; i < ADMINISTRATION_DURATION_TICKS; i++) {
    s = tickAtFunds(s, DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000); // stay deep in crisis throughout.
  }
  assert.equal(s.tick, enteredAt + ADMINISTRATION_DURATION_TICKS);
  assert.equal(s.administrationState, null, 'administration must end at the year-end tick regardless of funds (AC-7 "then re-evaluate")');
  assert.equal(s.insolvencyState, 'crisis', 'still-broke reverts to the crisis band, not a fresh administration');
});

test('AC-7 MUTATION-PROVE target: still-broke re-evaluation must NOT auto-re-trigger a fresh bailout injection (inc4 scope)', () => {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });

  let s = admin;
  for (let i = 0; i < ADMINISTRATION_DURATION_TICKS; i++) {
    s = tickAtFunds(s, DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000);
  }
  assert.equal(s.bailoutState, null, 'no auto-transition to a fresh/second bailout at admin year-end — inc4 scope, not inc3');
});

// ========== Determinism (GR#21 / AC-12 companion): no Date/random, byte-identical replays ==========

test('Determinism: two identical enterAdministration + duration runs produce byte-identical state', () => {
  const runOnce = () => {
    const crisis = enterBailout();
    const admin = reducer(crisis, { type: 'enterAdministration' });
    return tickN(admin, 10);
  };
  // enterBailout() re-derives from a fresh initialState() each call — byte-identical inputs.
  const a = runOnce();
  const b = runOnce();
  assert.equal(JSON.stringify(a), JSON.stringify(b));
});

test('Determinism: the full year-end re-evaluation replays to the identical outcome', () => {
  const runOnce = () => {
    const crisis = enterBailout();
    const admin = reducer(crisis, { type: 'enterAdministration' });
    let s = admin;
    for (let i = 0; i < ADMINISTRATION_DURATION_TICKS; i++) {
      s = tickAtFunds(s, DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000);
    }
    return s;
  };
  const a = runOnce();
  const b = runOnce();
  assert.equal(JSON.stringify(a), JSON.stringify(b));
});
