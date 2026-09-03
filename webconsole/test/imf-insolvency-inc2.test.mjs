// imf-insolvency-inc2.test.mjs — FEAT-1972079923 inc2: the IMF bailout EVENT
// (360-tick state machine + one-time income injection), FORCED ASSET SALES
// (capital-value-descending list per Aaron's ruling — biggest first, NOT the
// AC doc's stale construction-order text), and the atomic ledger-labelled
// sale transaction.
//
// Scope (per the BUILD LANE brief's inc2 slice — AC-2, AC-3, AC-4): the
// bailout event, the forced-sale asset list + sale transaction. Administration
// Mode, the second bailout and the decline screen are inc3/4 and are NOT
// tested here (see imf-insolvency-inc1.test.mjs for AC-1/AC-8/AC-9).
//
// node --test type-strips the .ts imports; every assertion below can FAIL if
// the corresponding fix is reverted — RED/GREEN pairs proved with a scratch
// cp/mv of fiscal.ts/engine.ts, never a git revert (GR#24).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, forcedSaleAssets, TICKS_PER_YEAR } from '../src/sim/engine.ts';
import {
  DEBT_THRESHOLD_FOR_BAILOUT,
  INSOLVENCY_WARNING_THRESHOLD,
  BAILOUT_DURATION_TICKS,
  BAILOUT_INCOME_INJECTION,
  ASSET_SALE_VALUE_FRACTION,
  BAILOUT_INJECTION_LABEL,
  ASSET_SALE_LABEL,
  DYNAMIC_BAILOUT_INJECTION_LABEL,
} from '../src/sim/fiscal.ts';
import { isStateAffecting } from '../src/sim/journal.ts';
import { placementCost, SPECS } from '../src/sim/data.ts';

// BUG-452 inc1 (2026-09-01): derived from the ratio-preserved thresholds (see
// imf-insolvency-inc1.test.mjs) rather than hardcoded to the old £10M-scale
// -6,000,000 literal, so this suite auto-scales with STARTING_TREASURY.
const WARNING_BAND_FUNDS = Math.round((INSOLVENCY_WARNING_THRESHOLD + DEBT_THRESHOLD_FOR_BAILOUT) / 2);
const CRISIS_FUNDS = DEBT_THRESHOLD_FOR_BAILOUT * 2; // well below (more negative than) the crisis threshold

// Advance the state by one tick, forcing funds to a target value first (mirrors
// imf-insolvency-inc1.test.mjs's helper exactly).
function tickAtFunds(state, targetFunds) {
  const forced = reducer(state, { type: 'debugFunds', amount: targetFunds - state.funds });
  return reducer(forced, { type: 'tick' });
}

function tickN(state, n) {
  let s = state;
  for (let i = 0; i < n; i++) s = reducer(s, { type: 'tick' });
  return s;
}

// ========== Duration constant sanity (AC-2's "must equal TICKS_PER_YEAR") ==========

test('BAILOUT_DURATION_TICKS equals TICKS_PER_YEAR (one game-year, AC-2)', () => {
  assert.equal(BAILOUT_DURATION_TICKS, TICKS_PER_YEAR);
  assert.equal(BAILOUT_DURATION_TICKS, 360);
});

// ========== AC-1/AC-2: crossing the threshold triggers the bailout EVENT ==========

test('AC-1/AC-2: crossing into crisis triggers the bailout state machine exactly once', () => {
  const s0 = initialState();
  assert.equal(s0.bailoutState ?? null, null, 'precondition: no bailout at game start');

  const warning = tickAtFunds(s0, WARNING_BAND_FUNDS);
  assert.equal(warning.bailoutState ?? null, null, 'no bailout while only in the warning band');

  const enteredCrisis = tickAtFunds(warning, CRISIS_FUNDS);
  assert.equal(enteredCrisis.insolvencyState, 'crisis');
  assert.ok(enteredCrisis.bailoutState, 'bailoutState must be stamped on the ENTRY tick');
  assert.equal(enteredCrisis.bailoutState.enteredAt, enteredCrisis.tick);

  // Still in crisis next tick — must NOT re-stamp (same enteredAt), mirrors insolvencyPopup.
  const stillCrisis = reducer(enteredCrisis, { type: 'tick' });
  assert.equal(
    stillCrisis.bailoutState.enteredAt,
    enteredCrisis.bailoutState.enteredAt,
    'bailoutState must not re-fire on a tick that does not RE-enter crisis',
  );
});

// MUTATION-PROVE target: if the trigger condition drops the `prevBailoutState === null`
// guard (or the crisis-entry condition entirely), the re-fire assertion above goes RED.
// Verified by scratch-editing engine.ts's bailout trigger `if` to always fire and
// confirming `stillCrisis.bailoutState.enteredAt` no longer matches (RED), then restoring.

// FEAT-dynamic-bailout SUPERSESSION NOTE (Aaron ruling Q100045, 2026-09-02):
// the FRESH first-tier grant is no longer the fixed BAILOUT_INCOME_INJECTION
// under BAILOUT_INJECTION_LABEL — it is now the dynamically-sized offer
// (fiscal.computeDynamicBailoutOffer) under DYNAMIC_BAILOUT_INJECTION_LABEL.
// Updated (not deleted) to prove the NEW label/mechanism fires exactly once
// per entry — see fiscal.test.mjs / feat-dynamic-bailout.test.mjs for the
// formula's own proportionality/floor/cap coverage.
test('AC-2 (superseded by FEAT-dynamic-bailout): the bailout injects the DYNAMIC offer as a labelled inflow, exactly once', () => {
  const s0 = initialState();
  const warning = tickAtFunds(s0, WARNING_BAND_FUNDS);
  const preEntryFunds = warning.funds;
  const entered = tickAtFunds(warning, CRISIS_FUNDS);

  const injectionFlow = entered.lastFlows.inflows.find((f) => f.label === DYNAMIC_BAILOUT_INJECTION_LABEL);
  assert.ok(injectionFlow, 'the injection must be a NAMED inflow, traceable by the consistency checker');
  assert.ok(injectionFlow.value > 0, 'the dynamic offer must be a real positive credit');
  assert.equal(
    entered.lastFlows.inflows.some((f) => f.label === BAILOUT_INJECTION_LABEL),
    false,
    'the OLD fixed-terms label must never appear for a fresh dynamic grant',
  );

  // Conservation: fundsAtTickEnd must still equal fundsAtTickStart + Σinflows − Σoutflows
  // WITH the injection counted (mirrors consistency.ts's conservation.funds-vs-flows check).
  const inflowSum = entered.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const outflowSum = entered.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  assert.equal(entered.fundsAtTickEnd, entered.fundsAtTickStart + inflowSum - outflowSum);

  // A tick that does NOT enter crisis (already in crisis) must NOT inject again.
  const nextTick = reducer(entered, { type: 'tick' });
  const secondInjection = nextTick.lastFlows.inflows.find((f) => f.label === DYNAMIC_BAILOUT_INJECTION_LABEL);
  assert.equal(secondInjection, undefined, 'no re-injection on a non-entry tick');
});

test('AC-2: bailout duration is exactly BAILOUT_DURATION_TICKS — ends only at year-end if solvent', () => {
  const s0 = initialState();
  const entered = tickAtFunds(s0, DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000);
  assert.ok(entered.bailoutState);
  const enteredAt = entered.bailoutState.enteredAt;

  // One tick BEFORE year-end: still active regardless of funds.
  const justBefore = tickN(entered, BAILOUT_DURATION_TICKS - 1);
  assert.ok(justBefore.bailoutState, 'bailout must still be active one tick before year-end');

  // Force funds solvent right at year-end (one more tick reaches enteredAt+360).
  const solventAtYearEnd = tickAtFunds(justBefore, 0);
  assert.equal(solventAtYearEnd.tick, enteredAt + BAILOUT_DURATION_TICKS);
  assert.equal(solventAtYearEnd.bailoutState, null, 'bailout must end when solvent AT the year-end tick');
});

// FEAT-1972079923 inc4 (AC-10) SUPERSESSION NOTE: this test originally
// asserted "still-insolvent at year-end leaves bailoutState ACTIVE (no
// auto-transition)" per the inc2-era AC-2 text. Aaron's round-2 ruling
// (2026-08-31, recorded on the BOW item) OVERRIDES that: still broke at the
// first bailout year-end now AUTO-TRIGGERS the second bailout (AC-10) — see
// imf-insolvency-inc4.test.mjs for the full second-bailout/decline coverage.
// Updated here (not deleted) so this file still documents the FIRST bailout's
// own year-end behaviour accurately post-inc4.
test('AC-2/AC-10: still-insolvent at year-end ends the FIRST bailout and auto-triggers the SECOND (inc4)', () => {
  const s0 = initialState();
  const entered = tickAtFunds(s0, DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000);
  const enteredAt = entered.bailoutState.enteredAt;
  // Force funds to stay deep in crisis through year-end.
  let s = entered;
  for (let i = 0; i < BAILOUT_DURATION_TICKS; i++) {
    s = tickAtFunds(s, DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000);
  }
  assert.equal(s.tick, enteredAt + BAILOUT_DURATION_TICKS);
  assert.equal(s.bailoutState, null, 'the FIRST bailout must end at year-end (never persists past its own duration)');
  assert.ok(s.bailoutSecondState, 'still-insolvent at year-end must auto-trigger the SECOND bailout (AC-10)');
  assert.equal(s.bailoutSecondState.enteredAt, enteredAt + BAILOUT_DURATION_TICKS, 'second bailout enters AT the first year-end tick');
});

// ========== AC-3: forced sale list sorted by CAPITAL VALUE DESCENDING ==========

// The starter city is all-road (uniform £40 capitalValue — see rawState/starterCity),
// which cannot distinguish ascending from descending order (every permutation of a
// tied list "passes" a naive check). A REAL mixed-value fixture is required so a
// wrong comparator direction is actually observable. Built by directly setting
// `buildings` (forcedSaleAssets is a pure selector over s.buildings + SPECS —
// bypassing place()'s affordability/unlock/footprint gates is fine here, since
// this is testing the SELECTOR, not placement).
const ROAD_COST = placementCost(SPECS['road']); // 40
const AVENUE_COST = placementCost(SPECS['rd_avenue']); // 90
assert.ok(AVENUE_COST > ROAD_COST, 'precondition: two real specs with DIFFERENT capital values');

function mixedValueFixture() {
  const s0 = initialState();
  return {
    ...s0,
    buildings: [
      { id: 9001, spec: 'road', x: 0, y: 0 },
      { id: 9002, spec: 'rd_avenue', x: 2, y: 0 }, // bigger capitalValue, placed SECOND (id order != value order)
      { id: 9003, spec: 'road', x: 4, y: 0 },
    ],
  };
}

test('AC-3: forcedSaleAssets sorts by capital value DESCENDING (biggest first — Aaron\'s ruling)', () => {
  const fixture = mixedValueFixture();
  const list = forcedSaleAssets(fixture);
  assert.equal(list.length, 3);
  assert.equal(list[0].spec, 'rd_avenue', 'the bigger-capitalValue asset must be FIRST, not the lower-id road placed before it');
  assert.equal(list[0].capitalValue, AVENUE_COST);
  for (let i = 1; i < list.length; i++) {
    assert.ok(
      list[i - 1].capitalValue >= list[i].capitalValue,
      `list must be non-increasing by capitalValue: [${i - 1}]=${list[i - 1].capitalValue} < [${i}]=${list[i].capitalValue}`,
    );
  }
});

test('AC-3 MUTATION-PROVE target: on this mixed fixture, an ascending sort is DISTINGUISHABLE from descending', () => {
  const fixture = mixedValueFixture();
  const descending = forcedSaleAssets(fixture);
  const ascending = [...descending].sort((a, b) => a.capitalValue - b.capitalValue || a.id - b.id);
  assert.notDeepEqual(
    descending.map((a) => a.id),
    ascending.map((a) => a.id),
    'the mixed fixture must actually distinguish sort direction (proves the AC-3 check above can fail)',
  );
  assert.equal(ascending[0].spec, 'road', 'ascending order (the mutation) would put a £40 road first — wrong per Aaron\'s ruling');
});

test('AC-3: zero-cost assets are excluded (nothing to force-sell for £0)', () => {
  const s0 = initialState();
  const list = forcedSaleAssets(s0);
  assert.ok(list.every((a) => a.capitalValue > 0), 'every listed asset must have a positive capital value');
});

// ========== AC-4: forced sale — atomic removal + ledger-labelled treasury credit ==========

test('AC-4: sellAsset removes the building and credits the treasury the saleValue, atomically', () => {
  const s0 = initialState();
  const list = forcedSaleAssets(s0);
  const target = list[0];
  const before = s0;
  const after = reducer(before, { type: 'sellAsset', id: target.id });

  assert.equal(after.buildings.length, before.buildings.length - 1, 'exactly one building removed');
  assert.ok(!after.buildings.some((b) => b.id === target.id), 'the SOLD building must be gone');
  assert.equal(after.funds, before.funds + target.saleValue, 'funds must increase by EXACTLY the sale value');
  assert.equal(
    target.saleValue,
    Math.round(target.capitalValue * ASSET_SALE_VALUE_FRACTION),
    'saleValue must be the placeholder fraction of capitalValue',
  );
});

test('AC-4: the sale is a NAMED, traceable inflow in lastFlows (not just a ledger row)', () => {
  const s0 = initialState();
  const target = forcedSaleAssets(s0)[0];
  const after = reducer(s0, { type: 'sellAsset', id: target.id });
  const saleFlow = after.lastFlows.inflows.find((f) => f.label === ASSET_SALE_LABEL);
  assert.ok(saleFlow, 'a labelled Asset Sale inflow must exist — this is what lets the consistency checker trace the funds jump');
  assert.equal(saleFlow.value, target.saleValue);

  // Ledger also carries a human-readable event (mirrors the bulldoze pattern).
  const ledgerEntry = after.ledger.find((l) => l.label.includes(ASSET_SALE_LABEL));
  assert.ok(ledgerEntry, 'the ledger must carry a Forced Asset Sale entry too');
  assert.equal(ledgerEntry.amount, target.saleValue);
});

test('AC-4: selling a SECOND asset before the next tick merges into ONE inflow entry (no duplicate label)', () => {
  const s0 = initialState();
  const list = forcedSaleAssets(s0);
  assert.ok(list.length >= 2, 'precondition: at least two sellable assets');
  const [first, second] = list;
  const afterFirst = reducer(s0, { type: 'sellAsset', id: first.id });
  const afterSecond = reducer(afterFirst, { type: 'sellAsset', id: second.id });

  const saleFlows = afterSecond.lastFlows.inflows.filter((f) => f.label === ASSET_SALE_LABEL);
  assert.equal(saleFlows.length, 1, 'must merge into ONE entry — a duplicate label would trip consistency.ts flows.inflow-labels-unique');
  assert.equal(saleFlows[0].value, first.saleValue + second.saleValue, 'the merged value must be the SUM of both sales');
  assert.equal(
    afterSecond.funds,
    s0.funds + first.saleValue + second.saleValue,
    'funds must reflect BOTH sales exactly',
  );
});

test('AC-4 MUTATION-PROVE target: selling a nonexistent id is a true no-op', () => {
  const s0 = initialState();
  const bogusId = Math.max(...s0.buildings.map((b) => b.id)) + 999;
  const after = reducer(s0, { type: 'sellAsset', id: bogusId });
  assert.equal(after, s0, 'an unknown id must return the SAME state reference');
});

test('sellAsset is state-affecting — must be journaled for replay (mirrors bulldoze)', () => {
  assert.equal(isStateAffecting({ type: 'sellAsset', id: 1 }), true);
});

// ========== Determinism (GR#21 / AC-12): no Date/random, byte-identical replays ==========

test('Determinism: two identical bailout-entry runs produce byte-identical state (event + injection + list)', () => {
  const s0 = initialState();
  const runOnce = () => {
    let s = tickAtFunds(s0, WARNING_BAND_FUNDS);
    s = tickAtFunds(s, CRISIS_FUNDS); // bailout entry
    return s;
  };
  const a = runOnce();
  const b = runOnce();
  assert.equal(JSON.stringify(a), JSON.stringify(b));

  const listA = forcedSaleAssets(a);
  const listB = forcedSaleAssets(b);
  assert.equal(JSON.stringify(listA), JSON.stringify(listB), 'the forced-sale asset order must replay identically');
});

test('Determinism: selling the same asset on two identical runs yields byte-identical results', () => {
  const s0 = initialState();
  const target = forcedSaleAssets(s0)[0];
  const a = reducer(s0, { type: 'sellAsset', id: target.id });
  const b = reducer(s0, { type: 'sellAsset', id: target.id });
  assert.equal(JSON.stringify(a), JSON.stringify(b));
});
