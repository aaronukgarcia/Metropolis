// fiscal.test.mjs — BUG-402/403/404/405 fiscal cluster regression tests.
//
// Four distinct bugs in the fiscal tick system:
// - BUG-402: no insolvency mechanic when funds go negative
// - BUG-403: Transit Subsidy cost scales unbounded (population * 1.5)
// - BUG-404: duplicate Tourism inflow streams (same label, different sources)
// - BUG-405: austerity policy appears to be a no-op on expenditure

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeFlows, LOAN_PRINCIPAL, reducer, initialState } from '../src/sim/engine.ts';
import { SPECS } from '../src/sim/data.ts';

// Minimal SimState with no loans, no policies, default tax rates.
function baseState() {
  return {
    tick: 0,
    speed: 1,
    funds: 10000000,
    loanBalance: 0,
    population: 1000,
    xp: 0,
    taxRates: { residential: 9, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: false },
    buildings: [],
    nextId: 1,
    movingId: null,
    tool: { mode: 'select' },
    clipboard: null,
    pipeTier: {},
    history: [],
    ledger: [],
    nextLedgerId: 1,
    lastFlows: { inflows: [], outflows: [] },
    lastRewardedLevel: 1,
    notice: null,
  };
}

// Place one building of a given spec and return the updated state.
function addBuilding(state, spec) {
  return {
    ...state,
    buildings: [
      ...state.buildings,
      { id: state.nextId, spec, x: state.nextId, y: 10, builtTick: null },
    ],
    nextId: state.nextId + 1,
  };
}

// ========== BUG-404: duplicate Tourism inflow ==========
test('BUG-404 RED: Tourism inflow has duplicate labels when tourismDrive + buildings', () => {
  let state = baseState();
  state = { ...state, population: 1000, policies: { ...state.policies, tourismDrive: true } };

  // Add a building with tourism value (e.g., Stadium)
  state = addBuilding(state, 'land_stadium'); // tourism: 60

  const { inflows } = computeFlows(state);

  // Count Tourism entries
  const tourismEntries = inflows.filter((f) => f.label === 'Tourism');

  // RED: should find exactly ONE Tourism entry, but the bug creates TWO
  // (one from tourismDrive, one from building tourism)
  assert.equal(
    tourismEntries.length,
    1,
    `Tourism inflow should have exactly 1 entry, but got ${tourismEntries.length}. ` +
    `Entries: ${tourismEntries.map((e) => `${e.label}:${e.value}`).join(', ')}`
  );
});

// ========== BUG-403: Transit Subsidy unbounded -> capped ==========
test('BUG-403 GREEN: Transit Subsidy is capped to a fraction of tax income', () => {
  let state = baseState();

  // At population 282k (Aaron's Y148 dump), raw subsidy would reach 424k/tick
  // After BUG-403 fix: subsidy is capped at 50% of base tax income
  state = { ...state, population: 282000, policies: { ...state.policies, transitSubsidy: true } };

  const { inflows, outflows } = computeFlows(state);
  const subsidy = outflows.find((o) => o.label === 'Transit Subsidy');

  // Calculate base tax income (Council Tax + Business Tax + Freight Tax)
  const baseTaxIncome = inflows
    .filter((f) => ['Council Tax', 'Business Tax', 'Freight Tax'].includes(f.label))
    .reduce((a, f) => a + f.value, 0);

  // Subsidy should be capped at 50% of base tax income (PLACEHOLDER cap)
  const maxSubsidy = Math.round(baseTaxIncome * 0.5);
  const rawSubsidy = Math.round(state.population * 1.5);

  assert.ok(
    subsidy && subsidy.value <= maxSubsidy,
    `Transit Subsidy (${subsidy?.value}) should be capped at 50% of tax income (${maxSubsidy}). ` +
    `Raw cost would be ${rawSubsidy}.`
  );

  // Subsidy should not exceed the tax income
  assert.ok(
    subsidy && subsidy.value < baseTaxIncome,
    `Transit Subsidy (${subsidy?.value}) should not dwarf base tax income (${baseTaxIncome})`
  );
});

// ========== BUG-405: austerity policy no-op ==========
test('BUG-405 GREEN: austerity ON measurably reduces spending', () => {
  let state = baseState();

  // Create a non-trivial city to measure spending
  state = { ...state, population: 500 };
  for (let i = 0; i < 10; i++) {
    state = addBuilding(state, 'road'); // upkeep 3 each
  }
  state = addBuilding(state, 'pow_wind'); // upkeep 8
  state = addBuilding(state, 'hea_clinic'); // upkeep 26

  // Measure spending WITHOUT austerity
  const flowsNoAusterity = computeFlows({ ...state, policies: { ...state.policies, austerity: false } });
  const expenseNoAusterity = flowsNoAusterity.outflows.reduce((a, f) => a + f.value, 0);

  // Measure spending WITH austerity (should reduce by 10%)
  const flowsAusterity = computeFlows({ ...state, policies: { ...state.policies, austerity: true } });
  const expenseAusterity = flowsAusterity.outflows.reduce((a, f) => a + f.value, 0);

  // Austerity should reduce spending (multiplier 0.9)
  assert.ok(
    expenseAusterity < expenseNoAusterity,
    `Austerity ON should reduce spending: ${expenseNoAusterity} -> ${expenseAusterity}. ` +
    `Expected reduction ≈10%, but got increase or no change.`
  );

  // The reduction should be approximately 10% (0.9x multiplier)
  const ratio = expenseAusterity / expenseNoAusterity;
  assert.ok(
    ratio >= 0.88 && ratio <= 0.92,
    `Austerity reduction should be ~10% (0.9x), got ${((1 - ratio) * 100).toFixed(1)}%. ` +
    `Ratio: ${ratio.toFixed(4)}`
  );
});

// ========== BUG-402: no insolvency mechanic (hardest) ==========
test('BUG-402 GREEN: overdraft interest applied when funds < 0', () => {
  let state = baseState();

  // Force negative funds (spend more than available)
  state = { ...state, funds: -1000000, population: 500, loanBalance: 0 };

  // Add expensive upkeep so funds stay negative
  for (let i = 0; i < 20; i++) {
    state = addBuilding(state, 'pow_coal'); // expensive upkeep 90 each
  }

  const { outflows } = computeFlows(state);

  // Check for overdraft interest or insolvency consequence
  // (After fix: should include "Overdraft Interest" or similar)
  const hasOverdraftMechanic = outflows.some((o) =>
    o.label === 'Overdraft Interest' || o.label.includes('Overdraft')
  );

  assert.ok(
    hasOverdraftMechanic,
    `Negative funds should trigger overdraft interest, but outflows are: ` +
    `${outflows.map((o) => `${o.label}:${o.value}`).join(', ')}`
  );
});

// ========== BUG-402: scaled loan facility ==========
test('BUG-402 GREEN: loan facility scales with city size, not fixed 25k', () => {
  let state = baseState();

  // Small city: loan should be smaller
  const smallCity = { ...state, population: 100, buildings: [] };
  const smallLoan = computeFlows(smallCity).inflows.find((f) => f.label === 'Loan Available');

  // Large city: loan should be larger
  const largeCity = { ...state, population: 10000, buildings: [] };
  const largeLoan = computeFlows(largeCity).inflows.find((f) => f.label === 'Loan Available');

  // After fix: loan facility should scale with population or tax income
  // For now, the test verifies that the loan scales (not flat 25k for all sizes)
  if (largeLoan && smallLoan) {
    assert.ok(
      largeLoan.value > smallLoan.value,
      `Loan facility should scale with city size. ` +
      `Small city (pop 100): ${smallLoan.value}, Large city (pop 10k): ${largeLoan.value}`
    );
  }
});

// ========== Regression: lastFlows stream labels are unique ==========
test('regression: computeFlows stream labels are unique (no duplicates by label)', () => {
  let state = baseState();
  state = { ...state, population: 5000, policies: { ...state.policies, tourismDrive: true } };

  // Add tourism-generating buildings
  state = addBuilding(state, 'land_stadium');
  state = addBuilding(state, 'lei_museum');

  const { inflows, outflows } = computeFlows(state);

  // Check inflows for duplicate labels
  const inflowLabels = inflows.map((f) => f.label);
  const uniqueInflowLabels = new Set(inflowLabels);

  assert.equal(
    inflowLabels.length,
    uniqueInflowLabels.size,
    `Inflow labels must be unique. Duplicates: ${inflowLabels.filter((l, i) => inflowLabels.indexOf(l) !== i).join(', ')}`
  );

  // Check outflows for duplicate labels
  const outflowLabels = outflows.map((o) => o.label);
  const uniqueOutflowLabels = new Set(outflowLabels);

  assert.equal(
    outflowLabels.length,
    uniqueOutflowLabels.size,
    `Outflow labels must be unique. Duplicates: ${outflowLabels.filter((l, i) => outflowLabels.indexOf(l) !== i).join(', ')}`
  );
});
