// imf-insolvency-inc4.test.mjs — FEAT-1972079923 inc4: the SECOND IMF bailout
// on worse terms (AC-10) + the FINAL DECLINE screen / hard game-over (AC-11).
//
// *** RULING CORRECTION (Aaron, round-2 interview, 2026-08-31, recorded on the
// BOW item) — OVERRIDES the BA criteria doc's stale AC-10 "user-initiated,
// button required" text: the second bailout is AUTO-TRIGGERED, no button, no
// player click. Whether the first bailout year was spent under the plain
// bailoutState or under administrationState, still-broke at that year's
// re-evaluation auto-triggers the second bailout on worse terms. Still broke
// at the SECOND bailout's year-end (either path) is the hard game-over. ***
//
// Scope (per the BUILD LANE brief's inc4 slice — AC-10, AC-11 ONLY): the
// second bailout state machine + the decline screen's stat trackers and hard
// freeze. AC-12 (replay-determinism refactor) is inc5 and is NOT tested here.
//
// node --test type-strips the .ts imports; every assertion below can FAIL if
// the corresponding fix is reverted — RED/GREEN pairs proved with a scratch
// cp/mv of fiscal.ts/engine.ts, never a git revert (GR#24).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, TICKS_PER_YEAR, forcedSaleAssets } from '../src/sim/engine.ts';
import {
  DEBT_THRESHOLD_FOR_BAILOUT,
  INSOLVENCY_WARNING_THRESHOLD,
  BAILOUT_DURATION_TICKS,
  BAILOUT_INCOME_INJECTION,
  ADMINISTRATION_DURATION_TICKS,
  SECOND_BAILOUT_DURATION_TICKS,
  BAILOUT_INCOME_INJECTION_SECOND,
  BAILOUT_SECOND_INJECTION_LABEL,
  FINAL_DECLINE_FUNDS_THRESHOLD,
} from '../src/sim/fiscal.ts';
import { isStateAffecting } from '../src/sim/journal.ts';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

// Advance the state by one tick, forcing funds to a target value first (mirrors
// imf-insolvency-inc1/inc2/inc3.test.mjs's helper exactly).
function tickAtFunds(state, targetFunds) {
  const forced = reducer(state, { type: 'debugFunds', amount: targetFunds - state.funds });
  return reducer(forced, { type: 'tick' });
}

function tickN(state, n) {
  let s = state;
  for (let i = 0; i < n; i++) s = reducer(s, { type: 'tick' });
  return s;
}

// BUG-452 inc1 (2026-09-01): derived from the ratio-preserved thresholds (see
// imf-insolvency-inc1.test.mjs) rather than hardcoded to the old £10M-scale
// -6,000,000 literal, so this suite auto-scales with STARTING_TREASURY.
const WARNING_BAND_FUNDS = Math.round((INSOLVENCY_WARNING_THRESHOLD + DEBT_THRESHOLD_FOR_BAILOUT) / 2);

// Drive a fresh game into an ACTIVE first bailout (mirrors inc3's enterBailout()).
function enterBailout(fundsBelowThreshold = DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000) {
  const s0 = initialState();
  const warning = tickAtFunds(s0, WARNING_BAND_FUNDS);
  const crisis = tickAtFunds(warning, fundsBelowThreshold);
  assert.ok(crisis.bailoutState, 'precondition: first bailout must be active');
  return crisis;
}

// Ride the FIRST bailout year out to its year-end, staying deep in crisis the
// whole way, so the SECOND bailout auto-triggers (AC-10) via the plain
// (non-administration) path.
function rideFirstBailoutToSecond() {
  const crisis = enterBailout();
  let s = crisis;
  for (let i = 0; i < BAILOUT_DURATION_TICKS; i++) {
    s = tickAtFunds(s, DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000);
  }
  assert.ok(s.bailoutSecondState, 'precondition: second bailout must have auto-triggered');
  return s;
}

// Ride the SECOND bailout out to its own year-end, staying below the decline
// threshold the whole way, so the FINAL DECLINE screen fires (AC-11) via the
// plain (non-administration) path.
function rideSecondBailoutToDecline() {
  let s = rideFirstBailoutToSecond();
  for (let i = 0; i < SECOND_BAILOUT_DURATION_TICKS; i++) {
    s = tickAtFunds(s, FINAL_DECLINE_FUNDS_THRESHOLD - 5_000_000);
  }
  assert.ok(s.declineState, 'precondition: decline must have triggered');
  return s;
}

// ========== Duration/injection constant sanity ==========

test('SECOND_BAILOUT_DURATION_TICKS equals TICKS_PER_YEAR (one game-year, AC-10)', () => {
  assert.equal(SECOND_BAILOUT_DURATION_TICKS, TICKS_PER_YEAR);
  assert.equal(SECOND_BAILOUT_DURATION_TICKS, 360);
});

test('AC-10: the second bailout is genuinely WORSE TERMS — a SMALLER injection than the first', () => {
  assert.ok(
    BAILOUT_INCOME_INJECTION_SECOND < BAILOUT_INCOME_INJECTION,
    'the second bailout injection must be strictly less generous than the first',
  );
  assert.ok(BAILOUT_INCOME_INJECTION_SECOND > 0, 'still a real positive injection, just a worse one');
});

// ========== AC-10: the second bailout is AUTO-TRIGGERED (no button) ==========

test('AC-10: still-broke at the FIRST bailout year-end AUTO-TRIGGERS the second bailout, no user action', () => {
  const s = rideFirstBailoutToSecond();
  assert.equal(s.bailoutState, null, 'the first bailout must have ended');
  assert.ok(s.bailoutSecondState, 'the second bailout must be active WITHOUT any button click');
  assert.equal(
    s.bailoutSecondState.enteredAt,
    s.tick,
    'entered at exactly the first year-end tick (the tick this test drove to)',
  );
  assert.equal(s.insolvencyState, 'bailout_second', 'exposed state reads the new bailout_second overlay');
});

test('AC-10 MUTATION-PROVE target: a SOLVENT first-year-end must NOT trigger the second bailout', () => {
  const crisis = enterBailout();
  const enteredAt = crisis.bailoutState.enteredAt;
  const justBefore = tickN(crisis, BAILOUT_DURATION_TICKS - 1);
  const solventAtYearEnd = tickAtFunds(justBefore, 0); // recovers exactly at year-end.
  assert.equal(solventAtYearEnd.tick, enteredAt + BAILOUT_DURATION_TICKS);
  assert.equal(solventAtYearEnd.bailoutState, null, 'first bailout ends cleanly');
  assert.equal(solventAtYearEnd.bailoutSecondState, null, 'a RECOVERED city must never see the second bailout');
  assert.equal(solventAtYearEnd.insolvencyState, 'solvent');
});

test('AC-10: the second bailout injects BAILOUT_INCOME_INJECTION_SECOND as a NAMED, traceable inflow', () => {
  const s = rideFirstBailoutToSecond();
  const injectionFlow = s.lastFlows.inflows.find((f) => f.label === BAILOUT_SECOND_INJECTION_LABEL);
  assert.ok(injectionFlow, 'the second injection must be a NAMED inflow, distinct from the first bailout\'s label');
  assert.equal(injectionFlow.value, BAILOUT_INCOME_INJECTION_SECOND);

  // Conservation: fundsAtTickEnd must still equal fundsAtTickStart + Σinflows − Σoutflows
  // WITH the second injection counted.
  const inflowSum = s.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const outflowSum = s.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  assert.equal(s.fundsAtTickEnd, s.fundsAtTickStart + inflowSum - outflowSum);
});

test('AC-10: the second bailout does not re-inject or re-stamp on a subsequent tick (one-shot)', () => {
  const s = rideFirstBailoutToSecond();
  const enteredAt = s.bailoutSecondState.enteredAt;
  const next = tickAtFunds(s, DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000);
  assert.equal(next.bailoutSecondState.enteredAt, enteredAt, 'must not re-stamp while still in the same window');
  const secondInjectionAgain = next.lastFlows.inflows.find((f) => f.label === BAILOUT_SECOND_INJECTION_LABEL);
  assert.equal(secondInjectionAgain, undefined, 'no re-injection on a non-entry tick');
});

test('AC-10: still-broke at year-end of an ADMINISTRATION-covered FIRST bailout year also auto-triggers the second', () => {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  assert.equal(admin.administrationState.origin, 'bailout', 'origin must be stamped as the FIRST bailout');
  let s = admin;
  for (let i = 0; i < ADMINISTRATION_DURATION_TICKS; i++) {
    s = tickAtFunds(s, DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000);
  }
  assert.equal(s.administrationState, null, 'administration must end at year-end');
  assert.ok(s.bailoutSecondState, 'second bailout must auto-trigger from the administration path too');
  assert.equal(s.insolvencyState, 'bailout_second');
});

test('AC-10 MUTATION-PROVE target: a RECOVERED administration-covered first year does not trigger the second bailout', () => {
  const crisis = enterBailout();
  const admin = reducer(crisis, { type: 'enterAdministration' });
  const enteredAt = admin.administrationState.enteredAt;
  const justBefore = tickN(admin, ADMINISTRATION_DURATION_TICKS - 1);
  const solventAtYearEnd = tickAtFunds(justBefore, 0);
  assert.equal(solventAtYearEnd.tick, enteredAt + ADMINISTRATION_DURATION_TICKS);
  assert.equal(solventAtYearEnd.administrationState, null);
  assert.equal(solventAtYearEnd.bailoutSecondState, null, 'recovery must never trigger a second bailout');
  assert.equal(solventAtYearEnd.insolvencyState, 'solvent');
});

test('enterAdministration is state-affecting (mirrors inc3) — still true when reused for the second bailout', () => {
  assert.equal(isStateAffecting({ type: 'enterAdministration' }), true);
});

// ========== AC-10: forced sales + Administration REAPPEAR for the second bailout ==========

test('AC-10: forcedSaleAssets / sellAsset work identically during the second bailout (reused, not reimplemented)', () => {
  const s = rideFirstBailoutToSecond();
  const assets = forcedSaleAssets(s);
  assert.ok(assets.length > 0, 'precondition: sellable assets exist');
  const target = assets[0];
  const after = reducer(s, { type: 'sellAsset', id: target.id });
  assert.equal(after.buildings.length, s.buildings.length - 1);
  assert.equal(after.funds, s.funds + target.saleValue);
  // Still in the second bailout — selling assets doesn't itself end it.
  assert.ok(after.bailoutSecondState, 'second bailout remains active after a sale');
});

test('AC-10: Administration Mode is available AGAIN during the second bailout, stamped with origin bailout_second', () => {
  const s = rideFirstBailoutToSecond();
  const admin = reducer(s, { type: 'enterAdministration' });
  assert.ok(admin.administrationState, 'administration must be enterable during the second bailout');
  assert.equal(admin.administrationState.origin, 'bailout_second', 'origin must record THIS is the second bailout year');
  assert.equal(admin.bailoutSecondState, null, 'entering administration closes the second-bailout FORCED ASSET SALES panel');
  assert.equal(admin.insolvencyState, 'administration');
});

test('AC-10 MUTATION-PROVE target: without an active second bailout, enterAdministration is still a true no-op (mirrors inc3 AC-5)', () => {
  const s0 = initialState();
  assert.equal(s0.bailoutState ?? null, null);
  assert.equal(s0.bailoutSecondState ?? null, null);
  const after = reducer(s0, { type: 'enterAdministration' });
  assert.equal(after, s0, 'no bailout of either kind active — must be a true no-op');
});

// ========== AC-11: the FINAL DECLINE screen (hard game-over) ==========

test('AC-11: still-broke at the SECOND bailout year-end (plain path) transitions to decline', () => {
  const s = rideSecondBailoutToDecline();
  assert.equal(s.bailoutSecondState, null, 'second bailout must have ended');
  assert.ok(s.declineState, 'declineState must be set');
  assert.equal(s.insolvencyState, 'decline');
  assert.equal(s.declineState.enteredAt, s.tick, 'decline stamped at exactly this tick');
});

test('AC-11 MUTATION-PROVE target: a RECOVERED second bailout year-end does NOT decline — no game-over on solvency', () => {
  const s0 = rideFirstBailoutToSecond();
  const enteredAt = s0.bailoutSecondState.enteredAt;
  const justBefore = tickN(s0, SECOND_BAILOUT_DURATION_TICKS - 1);
  const solventAtYearEnd = tickAtFunds(justBefore, 5_000_000); // comfortably >= FINAL_DECLINE_FUNDS_THRESHOLD.
  assert.equal(solventAtYearEnd.tick, enteredAt + SECOND_BAILOUT_DURATION_TICKS);
  assert.equal(solventAtYearEnd.bailoutSecondState, null, 'second bailout ends cleanly');
  assert.equal(solventAtYearEnd.declineState ?? null, null, 'recovery must never decline the city');
  assert.equal(solventAtYearEnd.insolvencyState, 'solvent');
});

test('AC-11: still-broke at the SECOND bailout year-end via an ADMINISTRATION-covered second year also declines', () => {
  const s0 = rideFirstBailoutToSecond();
  const admin = reducer(s0, { type: 'enterAdministration' });
  assert.equal(admin.administrationState.origin, 'bailout_second');
  let s = admin;
  for (let i = 0; i < ADMINISTRATION_DURATION_TICKS; i++) {
    s = tickAtFunds(s, FINAL_DECLINE_FUNDS_THRESHOLD - 5_000_000);
  }
  assert.equal(s.administrationState, null);
  assert.ok(s.declineState, 'still-broke after an administration-covered SECOND year must decline');
  assert.equal(s.insolvencyState, 'decline');
});

test('AC-11 MUTATION-PROVE target: a RECOVERED administration-covered second year does not decline', () => {
  const s0 = rideFirstBailoutToSecond();
  const admin = reducer(s0, { type: 'enterAdministration' });
  const enteredAt = admin.administrationState.enteredAt;
  const justBefore = tickN(admin, ADMINISTRATION_DURATION_TICKS - 1);
  const solventAtYearEnd = tickAtFunds(justBefore, 5_000_000);
  assert.equal(solventAtYearEnd.tick, enteredAt + ADMINISTRATION_DURATION_TICKS);
  assert.equal(solventAtYearEnd.administrationState, null);
  assert.equal(solventAtYearEnd.declineState ?? null, null);
  assert.equal(solventAtYearEnd.insolvencyState, 'solvent');
});

test('AC-11: NO third bailout is ever offered after decline — bailoutState/bailoutSecondState stay null forever', () => {
  const s = rideSecondBailoutToDecline();
  // Drive funds even deeper negative and tick repeatedly — nothing should reawaken.
  let after = s;
  for (let i = 0; i < 20; i++) {
    after = reducer(after, { type: 'debugFunds', amount: -1_000_000 });
    after = reducer(after, { type: 'tick' });
  }
  assert.equal(after.bailoutState ?? null, null);
  assert.equal(after.bailoutSecondState ?? null, null);
  assert.equal(after.administrationState ?? null, null);
  assert.ok(after.declineState, 'must remain in decline');
  assert.equal(after.insolvencyState, 'decline');
});

// ========== AC-11: the clock STOPS — advance()/tick is a hard freeze ==========

test('AC-11: the clock stops dead — a tick after decline changes NOTHING (same tick, same funds, same reference)', () => {
  const s = rideSecondBailoutToDecline();
  const ticked = reducer(s, { type: 'tick' });
  assert.equal(ticked, s, 'advance() must return the SAME reference once declineState is set — a true no-op');
  assert.equal(ticked.tick, s.tick);
  assert.equal(ticked.funds, s.funds);
});

test('AC-11 MUTATION-PROVE target: removing the decline freeze would let tick keep mutating state (documented via the pre-freeze fixture)', () => {
  // Prove the freeze is load-bearing: the SAME state with declineState cleared
  // DOES advance on a tick (proving something in advance() actually changes
  // per-tick when not frozen — i.e. the freeze guard is not vacuously true).
  const s = rideSecondBailoutToDecline();
  const withoutFreeze = { ...s, declineState: null };
  const ticked = reducer(withoutFreeze, { type: 'tick' });
  assert.equal(ticked.tick, withoutFreeze.tick + 1, 'without declineState, tick must still advance normally');
  assert.notEqual(ticked, withoutFreeze, 'without declineState, tick must NOT be a no-op — proving the freeze above is real');
});

test('AC-11: gameplay-mutating actions (place, sellAsset, policy, enterAdministration) are ALSO frozen after decline, not just tick', () => {
  const s = rideSecondBailoutToDecline();
  const afterPlace = reducer(s, { type: 'place', spec: 'res_hut', x: 60, y: 60 });
  assert.equal(afterPlace, s, 'place() must no-op once declined');
  const afterPolicy = reducer(s, { type: 'policy', id: 'austerity' });
  assert.equal(afterPolicy, s, 'policy() must no-op once declined');
  const afterAdmin = reducer(s, { type: 'enterAdministration' });
  assert.equal(afterAdmin, s, 'enterAdministration must no-op once declined (no fourth chance)');
  if (s.buildings.length > 0) {
    const afterSell = reducer(s, { type: 'sellAsset', id: s.buildings[0].id });
    assert.equal(afterSell, s, 'sellAsset must no-op once declined');
  }
});

test('AC-11 MUTATION-PROVE target: without the reduceCore freeze guard, a gameplay action WOULD mutate a declined city', () => {
  const s = rideSecondBailoutToDecline();
  const withoutFreeze = { ...s, declineState: null };
  const before = withoutFreeze.buildings.length;
  const after = reducer(withoutFreeze, { type: 'place', spec: 'res_hut', x: 61, y: 61 });
  assert.equal(after.buildings.length, before + 1, 'without declineState, place() must still work normally — proving the freeze above is what stops it');
});

test('AC-11: reset (Start Over) and hydrate (Load Save) are EXEMPT from the decline freeze — the only two ways out', () => {
  const s = rideSecondBailoutToDecline();
  const afterReset = reducer(s, { type: 'reset' });
  assert.notEqual(afterReset, s, 'reset must NOT be frozen — it is the GR#27-guarded Start Over path');
  assert.equal(afterReset.declineState ?? null, null, 'a fresh game starts with no decline state');
  assert.equal(afterReset.tick, initialState().tick, 'reset produces the same fresh-game tick as initialState()');

  const fresh = initialState();
  const afterHydrate = reducer(s, { type: 'hydrate', state: fresh });
  assert.notEqual(afterHydrate, s, 'hydrate must NOT be frozen — it is the GR#27-guarded Load Save path');
  assert.deepEqual(afterHydrate.declineState ?? null, fresh.declineState ?? null);
});

// ========== AC-11: decline stats are REAL computed values, never fabricated defaults (GR#15) ==========

test('AC-11: totalSpending is a real running sum of outflows — strictly positive over a multi-year run', () => {
  const s = rideSecondBailoutToDecline();
  assert.ok(
    s.declineState.totalSpending > 0,
    'a city that has run for two full bailout years must have accumulated real upkeep spending, not a placeholder zero',
  );
});

test('AC-11 MUTATION-PROVE target: totalSpending tracks the RUNNING sum, not just the final tick\'s expense', () => {
  const s = rideSecondBailoutToDecline();
  // The running total must be larger than any single tick's expense could
  // plausibly be alone — proving it accumulated across many ticks, not just
  // the last one. (starter city upkeep per tick is a few thousand; two years
  // = 720 ticks, so the sum must dwarf any one tick's contribution.)
  const lastTickExpense = s.lastFlows.outflows.reduce((a, o) => a + o.value, 0);
  assert.ok(
    s.declineState.totalSpending > lastTickExpense * 10,
    `totalSpending (${s.declineState.totalSpending}) must be a multi-tick accumulation, not ~= one tick's expense (${lastTickExpense})`,
  );
});

test('AC-11: minFundsEver tracks the RUNNING MINIMUM across the whole play, not just the value at decline', () => {
  // Manufacture a fixture where the historical minimum is DEEPER than the
  // funds value at the moment the deciding tick runs, so a naive
  // "just use current funds" implementation is distinguishable from the real
  // running-min tracker.
  const s0 = rideFirstBailoutToSecond();
  const deeplyNegative = { ...s0, minFundsEver: -999_999_999 };
  let s = deeplyNegative;
  for (let i = 0; i < SECOND_BAILOUT_DURATION_TICKS; i++) {
    // Shallower than the injected historical minimum, but still below the decline threshold.
    s = tickAtFunds(s, FINAL_DECLINE_FUNDS_THRESHOLD - 1_000_000);
  }
  assert.ok(s.declineState, 'precondition: must still decline (funds stay well below threshold)');
  assert.equal(
    s.declineState.minFundsEver,
    -999_999_999,
    'the historical minimum (deeper than anything reached THIS run) must be preserved, proving a running min, not a re-derived current value',
  );
});

test('AC-11 MUTATION-PROVE target: without threading minFundsEver through, the running minimum would be lost', () => {
  // Same fixture as above, but with the injected historical minimum STRIPPED
  // right before the deciding tick — proving the min tracker only "remembers"
  // what was actually threaded through state, not recomputed from history.
  const s0 = rideFirstBailoutToSecond();
  let s = { ...s0, minFundsEver: -999_999_999 };
  for (let i = 0; i < SECOND_BAILOUT_DURATION_TICKS - 1; i++) {
    s = tickAtFunds(s, FINAL_DECLINE_FUNDS_THRESHOLD - 1_000_000);
  }
  // Strip the tracker just before the final, decline-triggering tick.
  const stripped = { ...s, minFundsEver: s.funds };
  const declined = tickAtFunds(stripped, FINAL_DECLINE_FUNDS_THRESHOLD - 1_000_000);
  assert.ok(declined.declineState, 'precondition: must still decline');
  assert.notEqual(
    declined.declineState.minFundsEver,
    -999_999_999,
    'stripping the tracker must lose the deeper historical minimum — proving the field above is genuinely load-bearing, not vacuous',
  );
});

test('AC-11: peakPopulation tracks the RUNNING MAXIMUM, not the population at the moment of decline', () => {
  // Inject a historical peak higher than anything this run will reach, then
  // decline with population back at (or near) its starting value.
  const s0 = rideFirstBailoutToSecond();
  const withPeak = { ...s0, peakPopulation: 123_456, population: 0 };
  let s = withPeak;
  for (let i = 0; i < SECOND_BAILOUT_DURATION_TICKS; i++) {
    s = tickAtFunds(s, FINAL_DECLINE_FUNDS_THRESHOLD - 1_000_000);
  }
  assert.ok(s.declineState, 'precondition: must decline');
  assert.equal(
    s.declineState.peakPopulation,
    123_456,
    'the injected historical peak (never reached again this run) must survive into the decline stats, proving a running max, not a snapshot of the final population',
  );
});

test('AC-11: finalPopulation records population AT the decline tick (the current snapshot, distinct from the peak)', () => {
  const s0 = rideFirstBailoutToSecond();
  const withGap = { ...s0, peakPopulation: 500_000, population: 12_345 };
  let s = withGap;
  for (let i = 0; i < SECOND_BAILOUT_DURATION_TICKS; i++) {
    s = tickAtFunds(s, FINAL_DECLINE_FUNDS_THRESHOLD - 1_000_000);
  }
  assert.ok(s.declineState);
  assert.equal(s.declineState.peakPopulation, 500_000, 'peak stays at the injected historical high');
  assert.equal(
    s.declineState.finalPopulation,
    s.population,
    'finalPopulation must equal the population AT the decline tick, not the peak',
  );
  assert.notEqual(
    s.declineState.finalPopulation,
    s.declineState.peakPopulation,
    'peak and final must be DISTINGUISHABLE fields, not the same value copy-pasted',
  );
});

// ========== Determinism (GR#21 / AC-12 companion): no Date/random, byte-identical replays ==========

test('Determinism: two identical rides to the second bailout produce byte-identical state', () => {
  const a = rideFirstBailoutToSecond();
  const b = rideFirstBailoutToSecond();
  assert.equal(JSON.stringify(a), JSON.stringify(b));
});

test('Determinism: two identical rides all the way to decline produce byte-identical state', () => {
  const a = rideSecondBailoutToDecline();
  const b = rideSecondBailoutToDecline();
  assert.equal(JSON.stringify(a), JSON.stringify(b));
});

// ========== UI wiring: DeclineScreen must route BOTH buttons through the GR#27 path ==========
//
// The full store-level "reset aborts when capture fails" contract is already
// proven at the integration level in store-reset-capture.test.tsx (BUG-437) —
// DeclineScreen's Start Over button dispatches the SAME { type: 'reset' }
// action that test drives through a rigged localStorage. Duplicating a full
// jsdom/store mount here would re-test store.tsx, not this increment's new
// code. Instead this proves DeclineScreen's SOURCE does not bypass that
// wiring with some other reset/reload mechanism.
test('AC-11: DeclineScreen source wires Start Over to dispatch(reset) and Load Save to the shared loadGame() (never a raw bypass)', () => {
  const here = path.dirname(fileURLToPath(import.meta.url));
  const src = readFileSync(path.join(here, '..', 'src', 'components', 'MapView.tsx'), 'utf8');
  const start = src.indexOf('function DeclineScreen()');
  assert.ok(start >= 0, 'DeclineScreen component must exist in MapView.tsx');
  const end = src.indexOf('\nfunction Compass()', start);
  const body = src.slice(start, end >= 0 ? end : start + 3000);

  assert.match(body, /dispatch\(\{\s*type:\s*'reset'\s*\}\)/, 'Start Over must dispatch the GR#27-guarded reset action');
  assert.match(body, /loadGame\(\)/, 'Load Save must call the shared GR#27-guarded loadGame()');
  assert.doesNotMatch(body, /localStorage\.(clear|removeItem)/, 'must never bypass GR#27 with a raw storage wipe');
  assert.doesNotMatch(body, /window\.location\.reload/, 'must never bypass GR#27 with a raw page reload');
});

test('MUTATION-PROVE target: the source-scan above can fail — a bypass string is detectable', () => {
  const bypassSnippet = 'function DeclineScreen() {\n  window.location.reload();\n}\nfunction Compass()';
  const start = bypassSnippet.indexOf('function DeclineScreen()');
  const end = bypassSnippet.indexOf('\nfunction Compass()', start);
  const body = bypassSnippet.slice(start, end);
  assert.throws(() => {
    assert.doesNotMatch(body, /window\.location\.reload/);
  }, 'a genuine bypass must trip the doesNotMatch assertion used in the real test above');
});
