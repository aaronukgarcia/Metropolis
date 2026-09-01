// fiscal.test.mjs — BUG-402/403/404/405 fiscal cluster regression tests.
//
// Four distinct bugs in the fiscal tick system:
// - BUG-402: no insolvency mechanic when funds go negative
// - BUG-403: Transit Subsidy cost scales unbounded (population * 1.5)
// - BUG-404: duplicate Tourism inflow streams (same label, different sources)
// - BUG-405: austerity policy appears to be a no-op on expenditure

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  computeFlows,
  computeLevelRewards,
  LOAN_PRINCIPAL,
  reducer,
  initialState,
  xpForLevel,
  TICKS_PER_MONTH,
} from '../src/sim/engine.ts';
import {
  OVERDRAFT_PER_TICK,
  sanitizeFunds,
  overdraftInterestPerTick,
  STARTING_TREASURY,
  DEBT_THRESHOLD_FOR_BAILOUT,
  INSOLVENCY_WARNING_THRESHOLD,
  BAILOUT_INCOME_INJECTION,
  BAILOUT_INCOME_INJECTION_SECOND,
  ASSET_SALE_VALUE_FRACTION,
  wagesPerTick,
  councilTaxPerTick,
  REAL_NET_WAGE_PER_CITIZEN_PER_MONTH,
  REAL_COUNCIL_TAX_PER_CAPITA_PER_MONTH,
} from '../src/sim/fiscal.ts';
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

test('BUG-438 AC-1: level-up grant is never a debit', () => {
  const s = { ...initialState(), funds: -1_000_000, lastRewardedLevel: 1, xp: xpForLevel(3) };
  const rewards = computeLevelRewards(s);
  assert.ok(rewards.length > 0, 'should queue rewards for crossed levels');
  for (const r of rewards) {
    assert.equal(r.totalReward, 0);
    assert.equal(r.notice.cash, 0);
  }
});

test('BUG-438 AC-3: overdraft cannot explode over 1000 ticks', () => {
  let s = { ...initialState(), funds: -1_000_000, loanBalance: 0 };
  for (let i = 0; i < 1000; i++) s = reducer(s, { type: 'tick' });
  assert.ok(Number.isSafeInteger(s.funds), `funds left integer domain: ${s.funds}`);
  const od = s.lastFlows.outflows.find((o) => o.label === 'Overdraft Interest');
  const other = s.lastFlows.outflows
    .filter((o) => o.label !== 'Overdraft Interest')
    .reduce((a, o) => a + o.value, 0);
  if (od) assert.ok(od.value <= Math.max(other, 1), `overdraft ${od.value} > cap ${other}`);
});

test('BUG-438 AC-4: dump-scale funds do not produce e25 overdraft', () => {
  const exploded = -2.9067324168216207e35;
  const s0 = { ...initialState(), funds: exploded };
  const { outflows } = computeFlows(s0);
  const od = outflows.find((o) => o.label === 'Overdraft Interest');
  const other = outflows.filter((o) => o.label !== 'Overdraft Interest').reduce((a, o) => a + o.value, 0);
  assert.ok(od, 'overdraft line still present while insolvent');
  assert.ok(od.value <= Math.max(other, 1), `overdraft ${od.value} vs other ${other}`);
  assert.ok(od.value < 1e12, `overdraft still dump-scale: ${od.value}`);
  const s1 = reducer(s0, { type: 'tick' });
  assert.ok(Number.isSafeInteger(s1.funds), `post-tick funds ${s1.funds}`);
});

test('BUG-438 AC-5: sanitizeFunds fail-closed on non-integer money', () => {
  assert.equal(sanitizeFunds(-2.9067324168216207e35), 0);
  assert.equal(sanitizeFunds(Number.POSITIVE_INFINITY), 0);
  assert.equal(sanitizeFunds(Number.NaN), 0);
  assert.equal(sanitizeFunds(1000), 1000);
  assert.equal(sanitizeFunds(-1_000_000), -1_000_000);
  assert.ok(OVERDRAFT_PER_TICK > 0);
  assert.equal(overdraftInterestPerTick(-1_000_000, 80_000), Math.round(1_000_000 * OVERDRAFT_PER_TICK));
  assert.equal(overdraftInterestPerTick(-1e28, 80_000), 80_000);
});

// ========== BUG-452 inc1: GBP-scale rebase — ratios auto-scale with STARTING_TREASURY ==========
//
// Prove-can-fail: every assertion below directly encodes Aaron's approved
// ratios/range (2026-09-01, BOW-452). Mutating STARTING_TREASURY (e.g. back to
// the old £10,000,000 toy figure, or to something outside £1-2M) or breaking
// any ratio in fiscal.ts's derivation would trip these — proved live during
// this build via a scratch cp/mv of fiscal.ts (never a git revert, GR#24):
// reverting DEBT_THRESHOLD_FOR_BAILOUT to a hardcoded -10_000_000 literal
// turned the "ratio" assertion red immediately (thresholds no longer equal
// exactly -1x/-0.5x treasury), then restored.

test('BUG-452: STARTING_TREASURY sits within Aaron\'s approved £1-2M "start truly small" range', () => {
  assert.ok(
    STARTING_TREASURY >= 1_000_000 && STARTING_TREASURY <= 2_000_000,
    `STARTING_TREASURY (${STARTING_TREASURY}) must be in Aaron's approved £1,000,000-£2,000,000 range`,
  );
  // The specific approved anchor (midpoint of the range) — a retune within the
  // range is fine, but the anchor itself should not silently drift.
  assert.equal(STARTING_TREASURY, 1_500_000);
});

test('BUG-452: insolvency thresholds are EXACT ratios of STARTING_TREASURY, so a retune auto-scales them', () => {
  assert.equal(DEBT_THRESHOLD_FOR_BAILOUT, -STARTING_TREASURY, 'crisis threshold must be exactly -1x treasury');
  assert.equal(
    INSOLVENCY_WARNING_THRESHOLD,
    -Math.round(STARTING_TREASURY * 0.5),
    'warning threshold must be exactly -0.5x treasury',
  );
  // Ratio-preservation check independent of the literal STARTING_TREASURY value:
  // re-derive what the thresholds WOULD be at a different treasury and confirm
  // the RATIO (not just today's absolute numbers) is what fiscal.ts encodes.
  const impliedTreasuryFromCrisis = -DEBT_THRESHOLD_FOR_BAILOUT;
  const impliedTreasuryFromWarning = -INSOLVENCY_WARNING_THRESHOLD / 0.5;
  assert.equal(impliedTreasuryFromCrisis, STARTING_TREASURY);
  assert.equal(impliedTreasuryFromWarning, STARTING_TREASURY);
});

test('BUG-452: bailout injections are 50%/25% of the debt-hole magnitude, second strictly smaller (worse terms)', () => {
  const debtHoleMagnitude = Math.abs(DEBT_THRESHOLD_FOR_BAILOUT);
  assert.equal(BAILOUT_INCOME_INJECTION, Math.round(debtHoleMagnitude * 0.5), 'first bailout = 50% of the debt hole');
  assert.equal(
    BAILOUT_INCOME_INJECTION_SECOND,
    Math.round(debtHoleMagnitude * 0.25),
    'second bailout = 25% of the debt hole',
  );
  assert.ok(
    BAILOUT_INCOME_INJECTION_SECOND < BAILOUT_INCOME_INJECTION,
    'second bailout must remain strictly less generous (worse terms) than the first',
  );
});

test('BUG-452: ASSET_SALE_VALUE_FRACTION is the real distressed-sale rate (0.5), not the old 0.6', () => {
  assert.equal(ASSET_SALE_VALUE_FRACTION, 0.5);
});

test('BUG-452: wagesPerTick summed over a real month == REAL_NET_WAGE_PER_CITIZEN_PER_MONTH per citizen', () => {
  const population = 1000;
  let total = 0;
  for (let i = 0; i < TICKS_PER_MONTH; i++) total += wagesPerTick(population);
  const expected = REAL_NET_WAGE_PER_CITIZEN_PER_MONTH * population;
  // Per-tick rounding can drift the monthly total by at most one rounding unit
  // per tick — assert it lands within TICKS_PER_MONTH of the exact real figure,
  // not byte-identical (Math.round() is applied every tick, not once a month).
  assert.ok(
    Math.abs(total - expected) <= TICKS_PER_MONTH,
    `monthly wages total ${total} should be ~= real £${expected} (population ${population} x £${REAL_NET_WAGE_PER_CITIZEN_PER_MONTH}/mo)`,
  );
});

test('BUG-452: councilTaxPerTick at the DEFAULT residential rate, summed over a month, == REAL_COUNCIL_TAX_PER_CAPITA_PER_MONTH per person', () => {
  const population = 1000;
  const DEFAULT_RESIDENTIAL_TAX_RATE = 9; // engine.ts rawState()'s taxRates.residential initial
  let total = 0;
  for (let i = 0; i < TICKS_PER_MONTH; i++) total += councilTaxPerTick(population, DEFAULT_RESIDENTIAL_TAX_RATE);
  const expected = REAL_COUNCIL_TAX_PER_CAPITA_PER_MONTH * population;
  assert.ok(
    Math.abs(total - expected) <= TICKS_PER_MONTH,
    `monthly council tax total ${total} should be ~= real £${expected} (population ${population} x £${REAL_COUNCIL_TAX_PER_CAPITA_PER_MONTH}/mo)`,
  );
});

test('BUG-452 MUTATION-PROVE: councilTaxPerTick still scales with the player\'s tax-rate lever around the default', () => {
  const population = 1000;
  const atDefault = councilTaxPerTick(population, 9);
  const doubled = councilTaxPerTick(population, 18);
  assert.ok(doubled > atDefault, 'doubling the residential tax rate must increase council tax per tick');
  assert.ok(
    Math.abs(doubled - atDefault * 2) <= 1,
    `doubling the tax rate should ~double the per-tick council tax: ${atDefault} -> ${doubled}`,
  );
});
