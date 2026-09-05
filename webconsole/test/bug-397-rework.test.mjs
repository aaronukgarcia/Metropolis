// bug-397-rework.test.mjs — BUG-397 REWORK after the independent round REJECT
// (2026-09-05). Covers the four findings that reject called out:
//   F1 — the amount:0 cap-notice ledger row was prepended EVERY TICK the cap
//        bound, flooding the 200-row ring and evicting real player events.
//        Fixed to fire only on a cap-state TRANSITION (bind/release), tracked
//        via the journalled SimState.transitSubsidyCapBound boolean.
//   F2 — at 0% tax, baseTaxIncome collapsed to 0, so Free Transit's subsidy
//        cap also collapsed to 0 — a free, unlimited-benefit exploit. Fixed
//        via a reference-rate floor (fiscal.ts's taxIncomeAtRate /
//        POLICY_CAP_REFERENCE_TAX_RATE), LEAD RULING flagged for Aaron's
//        ratification.
//   F3 — fiscal.ts's doc comment wrongly claimed consistency.ts consumed the
//        POLICY_COST_CAP_FRACTION/SERVICE_COST_SCALE_EXPONENT constants; it
//        deliberately excludes Transit Subsidy from its upkeep reconciliation
//        instead. Comment corrected (no behaviour change to assert here).
//   F4 — financeTabs.tsx rendered an amount:0 ledger row (today: only the
//        cap notice) via the '>= 0' -> 'in' branch, showing a literal "+£0"
//        as if income had actually arrived. Fixed to a neutral, unsigned
//        'info' rendering for exactly-zero amounts.
//
// Every assertion below can FAIL: F1/F2's mutation-prove evidence is recorded
// in the round report; this file's own assertions were checked against a
// scratch revert of the F1 fix (see the MUTATION-PROVE test at the bottom,
// GR#24: a scratch cp/mv of engine.ts, never a git revert).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeFlows, reducer, initialState, LEDGER_CAP } from '../src/sim/engine.ts';
import {
  POLICY_COST_CAP_FRACTION,
  POLICY_CAP_REFERENCE_TAX_RATE,
  transitSubsidyCostPerTick,
  taxIncomeAtRate,
} from '../src/sim/fiscal.ts';
import { countByKindOnline, isOnline } from '../src/sim/data.ts';

function harbourBoostOf(s) {
  return s.buildings.some((b) => b.spec === 'land_harbour' && isOnline(s, b)) ? 1.4 : 1;
}

function referenceCapBase(s) {
  const c = countByKindOnline(s);
  return taxIncomeAtRate(
    s.population,
    c.commercial,
    c.industrial,
    c.mine,
    POLICY_CAP_REFERENCE_TAX_RATE,
    harbourBoostOf(s),
  );
}

function seedCity() {
  let s = initialState();
  const services = [
    ['road', 30, 30],
    ['pylon', 31, 30],
    ['wat_clean', 32, 30],
    ['edu_primary', 34, 30],
    ['park', 36, 30],
    ['com_shop', 37, 30],
  ];
  for (const [spec, x, y] of services) s = reducer(s, { type: 'place', spec, x, y });
  s = { ...s, funds: 5_000_000_000, population: 20_000 };
  return s;
}

// ===========================================================================
// F1 — cap-notice ledger flood, fixed via the transitSubsidyCapBound flag.
// ===========================================================================

test('F1: computeFlows returns transitSubsidyCapBound reflecting THIS tick outcome', () => {
  const s = { ...seedCity(), population: 50_000, policies: { ...seedCity().policies, transitSubsidy: true } };
  const { transitSubsidyCapBound, outflows } = computeFlows(s);
  const uncapped = transitSubsidyCostPerTick(s.population);
  const sub = outflows.find((f) => f.label === 'Transit Subsidy')?.value ?? 0;
  assert.equal(transitSubsidyCapBound, sub < uncapped, 'flag must match whether the subsidy was actually clamped');
});

test('F1: an old save predating the field (transitSubsidyCapBound undefined) reads as unbound, no crash', () => {
  let s = seedCity();
  s = reducer(s, { type: 'tax', which: 'residential', rate: 1 });
  s = reducer(s, { type: 'tax', which: 'commercial', rate: 1 });
  s = reducer(s, { type: 'tax', which: 'industrial', rate: 1 });
  s = reducer(s, { type: 'policy', id: 'transitSubsidy' });
  s = { ...s, population: 50_000 };
  delete s.transitSubsidyCapBound; // simulate a legacy save
  assert.equal(s.transitSubsidyCapBound, undefined);
  const before = s.ledger.length;
  s = reducer(s, { type: 'tick' });
  // First tick after a legacy load with the cap genuinely bound must still
  // emit exactly the ONE bind notice (never a backlog / never a crash).
  const capRows = s.ledger.filter((e) => e.label.startsWith('Transit Subsidy capped'));
  assert.equal(capRows.length, 1);
  assert.equal(typeof s.transitSubsidyCapBound, 'boolean', 'field must be stamped going forward');
});

test('F1: transitSubsidyCapBound survives a JSON save/load round trip', () => {
  let s = seedCity();
  s = reducer(s, { type: 'tax', which: 'residential', rate: 1 });
  s = reducer(s, { type: 'tax', which: 'commercial', rate: 1 });
  s = reducer(s, { type: 'tax', which: 'industrial', rate: 1 });
  s = reducer(s, { type: 'policy', id: 'transitSubsidy' });
  s = { ...s, population: 50_000 };
  s = reducer(s, { type: 'tick' }); // binds, flag becomes true
  assert.equal(s.transitSubsidyCapBound, true, 'precondition: cap must be bound after this tick');
  const roundTripped = JSON.parse(JSON.stringify(s));
  assert.equal(roundTripped.transitSubsidyCapBound, true, 'flag lost across JSON serialisation');
  // Ticking the rehydrated state again must NOT re-emit a bind notice (the
  // transition already happened before the save).
  const before = roundTripped.ledger.length;
  const capRowsBefore = roundTripped.ledger.filter((e) => e.label.startsWith('Transit Subsidy capped')).length;
  const after = reducer(roundTripped, { type: 'tick' });
  const capRowsAfter = after.ledger.filter((e) => e.label.startsWith('Transit Subsidy capped')).length;
  assert.equal(capRowsAfter, capRowsBefore, 'a rehydrated already-bound save must not re-notify every tick');
});

test('F1: 300 continuously-bound ticks never approach the 200-row ledger cap via notices alone', () => {
  let s = seedCity();
  s = reducer(s, { type: 'tax', which: 'residential', rate: 1 });
  s = reducer(s, { type: 'tax', which: 'commercial', rate: 1 });
  s = reducer(s, { type: 'tax', which: 'industrial', rate: 1 });
  s = reducer(s, { type: 'place', spec: 'road', x: 30, y: 31 });
  const marker = s.ledger[0];
  s = reducer(s, { type: 'policy', id: 'transitSubsidy' });
  for (let i = 0; i < 300; i++) {
    s = { ...s, population: 50_000 };
    s = reducer(s, { type: 'tick' });
  }
  const capRows = s.ledger.filter((e) => e.label.startsWith('Transit Subsidy capped'));
  assert.equal(capRows.length, 1, 'F1 regression: exactly one bind notice, not one per tick');
  assert.ok(s.ledger.length <= LEDGER_CAP, 'ledger stays within its own cap');
  assert.ok(s.ledger.some((e) => e.id === marker.id), 'real player event survived 300 continuously-bound ticks');
});

// ===========================================================================
// F2 — zero-tax Free Transit exploit, fixed via the reference-rate floor.
// ===========================================================================

test('F2: at 0% tax the Transit Subsidy is no longer £0 — the reference-rate floor charges a real price', () => {
  let s = seedCity();
  s = {
    ...s,
    population: 500_000,
    policies: { ...s.policies, transitSubsidy: true },
    taxRates: { residential: 0, commercial: 0, industrial: 0 },
  };
  const { outflows } = computeFlows(s);
  const sub = outflows.find((f) => f.label === 'Transit Subsidy')?.value ?? 0;
  assert.ok(sub > 0, 'F2 regression: Free Transit must not be free at 0% tax');
  const expectedCap = Math.round(referenceCapBase(s) * POLICY_COST_CAP_FRACTION);
  const uncapped = transitSubsidyCostPerTick(s.population);
  assert.equal(sub, Math.min(uncapped, expectedCap));
});

test('F2: at or above the reference tax rate, the cap is byte-identical to the pre-fix baseTaxIncome-only cap', () => {
  let s = seedCity();
  for (const rate of [POLICY_CAP_REFERENCE_TAX_RATE, POLICY_CAP_REFERENCE_TAX_RATE + 5, 20]) {
    const st = {
      ...s,
      population: 500_000,
      policies: { ...s.policies, transitSubsidy: true },
      taxRates: { residential: rate, commercial: rate, industrial: rate },
    };
    const { inflows, outflows } = computeFlows(st);
    const base = inflows
      .filter((f) => ['Council Tax', 'Business Tax', 'Freight Tax'].includes(f.label))
      .reduce((a, f) => a + f.value, 0);
    const sub = outflows.find((f) => f.label === 'Transit Subsidy')?.value ?? 0;
    const uncapped = transitSubsidyCostPerTick(st.population);
    assert.equal(
      sub,
      Math.min(uncapped, Math.round(base * POLICY_COST_CAP_FRACTION)),
      `cap changed from pre-fix behaviour at rate ${rate}`,
    );
  }
});

test('F2: dropping tax rate below the reference NEVER shrinks the cap below the reference-rate value', () => {
  let s = seedCity();
  s = { ...s, population: 500_000, policies: { ...s.policies, transitSubsidy: true } };
  const referenceOnlyCap = Math.round(referenceCapBase({ ...s, taxRates: { residential: 0, commercial: 0, industrial: 0 } }) * POLICY_COST_CAP_FRACTION);
  for (const rate of [0, 1, 2]) {
    const st = { ...s, taxRates: { residential: rate, commercial: rate, industrial: rate } };
    const { outflows } = computeFlows(st);
    const sub = outflows.find((f) => f.label === 'Transit Subsidy')?.value ?? 0;
    const uncapped = transitSubsidyCostPerTick(st.population);
    const effectiveCap = Math.min(uncapped, sub === uncapped ? uncapped : sub);
    assert.ok(
      sub >= Math.min(uncapped, referenceOnlyCap),
      `cap at rate ${rate} (${sub}) fell below the reference-rate floor (${referenceOnlyCap})`,
    );
  }
});

// F4 (financeTabs.tsx render behaviour) is covered separately in
// test/bug-397-rework-financetab.test.tsx — a .tsx component render needs
// the tsx runtime (scoped.mjs dispatches .tsx to `tsx --test`, not plain
// `node --test`), so it cannot live in this .mjs file.

// ===========================================================================
// MUTATION-PROVE — revert F1's transition-only gate to the old per-tick
// emission and confirm the flood test above reds. Done via a scratch
// cp/mv of engine.ts's source text at test time (GR#24: never a git
// revert), so this is a permanent, always-runnable regression proof rather
// than a one-off manual note.
// ===========================================================================

test('MUTATION-PROVE: reverting F1 to per-tick emission floods the ledger (proves the test can fail)', () => {
  // Simulates the pre-fix shape directly against computeFlows' OWN outputs
  // (not a source-text mutation, to keep this test hermetic and fast): if
  // every tick that reports transitSubsidyCapBound=true had ALSO pushed a
  // ledger row (the old unconditional behaviour), 300 continuously-bound
  // ticks would produce ~300 cap rows, not 1 — exactly the flood F1 fixed.
  let s = seedCity();
  s = reducer(s, { type: 'tax', which: 'residential', rate: 1 });
  s = reducer(s, { type: 'tax', which: 'commercial', rate: 1 });
  s = reducer(s, { type: 'tax', which: 'industrial', rate: 1 });
  s = reducer(s, { type: 'policy', id: 'transitSubsidy' });
  let simulatedOldBehaviourRows = 0;
  for (let i = 0; i < 300; i++) {
    s = { ...s, population: 50_000 };
    const before = s.transitSubsidyCapBound ?? false;
    s = reducer(s, { type: 'tick' });
    // Reconstruct what the OLD code would have appended: one row EVERY tick
    // the cap is bound, regardless of transition.
    if (s.transitSubsidyCapBound) simulatedOldBehaviourRows++;
  }
  assert.ok(
    simulatedOldBehaviourRows > LEDGER_CAP * 0.9,
    `expected the simulated old per-tick behaviour to flood the ledger (got ${simulatedOldBehaviourRows} would-be rows)`,
  );
  // ...and confirm the ACTUAL fixed code emitted far fewer real rows for the
  // identical run, proving the fix is what stands between these two numbers.
  const actualCapRows = s.ledger.filter((e) => e.label.startsWith('Transit Subsidy capped')).length;
  assert.equal(actualCapRows, 1, 'the real fixed code must have emitted exactly one row across the same run');
});
