// wages-reflow.test.mjs — BUG-419 regression.
//
// The engine charges population-scaled flows (Wages, Council Tax, ...) in
// computeFlows() on the INCOMING start-of-tick population, BEFORE the in-tick
// population growth update. The consistency checker used to recompute Wages against
// the GROWN end-of-tick population (s.population), so any growing tick diverged
// (recomputed > actual). Real live divergence: computed 212579 vs actual 191043 at
// pop 425157.
//
// Fix (checker-side, mirrors BUG-414): advance() records the basis it charged flows on
// in `lastFlows.population`, and the checker recomputes Wages/Council Tax against THAT.
// This test builds a state that genuinely grows population WITHIN a tick and asserts the
// wages/council-tax checks pass — while conservation still holds (money is not leaking;
// this was always a flow-vs-state timing mismatch, never a fund error).
//
// RED proof: revert the checker basis in consistency.ts back to `wagesPerTick(s.population)`
// (via scratch copy, never git) and this test goes RED — the check reports "diverged".
//
// BUG-wage-drift-2026-09 (Wage Stage 1, commit 4a8e9ed): this fixture had population but
// ZERO job buildings, so wagesPerTick(population) (the OLD flat-wage SSOT) no longer matches
// what the engine actually pays — 'Wages' is now sectorWagesPerTick(filledJobsBySector(s))
// .totalPerTick, paid on FILLED jobs, and a jobless city legitimately pays £0 regardless of
// population. Fix: (a) splice in a large-capacity job building (off_towers_downtown, 2,000
// jobs — comfortably above this fixture's few-hundred population so population, not job
// capacity, stays the binding constraint and filled jobs genuinely scale with the start-pop
// vs end-pop bases this test is built to distinguish) with no builtTick (isOnline()'s
// documented "no builtTick -> always online" convention, same as austerity-checks.test.mjs
// and every congestion-teeth.test.mjs city() fixture — deterministic, no dependency on road
// layout/funds/XP), and (b) recompute the RAW expectation via the NEW SSOT —
// sectorWagesPerTick(filledJobsFromCapacityAndPopulation(totalJobsBySector(s), basis))
// .totalPerTick — preserving this test's actual intent: the recorded wage is charged on the
// START-of-tick basis, not the grown end-of-tick one.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { councilTaxPerTick, sectorWagesPerTick } from '../src/sim/fiscal.ts';
import { totalJobsBySector, filledJobsFromCapacityAndPopulation } from '../src/sim/data.ts';

// Raw (pre-policy) wages on a given population basis — the SAME SSOT formula engine.ts's
// computeFlows and consistency.ts's recompute both funnel through.
function rawWagesAt(s, basis) {
  return sectorWagesPerTick(filledJobsFromCapacityAndPopulation(totalJobsBySector(s), basis))
    .totalPerTick;
}

// Build a state with ample residential capacity AND a large-capacity job building (so
// filled jobs scale with population, not job-capacity), then advance until we hit a tick
// that grows population from a NON-ZERO base (start-of-tick pop > 0), which is exactly the
// condition under which start-of-tick and end-of-tick population differ.
function advanceToGrowingTick() {
  let s = initialState();
  for (let y = 40; y < 70; y += 2) {
    for (let x = 40; x < 80; x += 2) {
      s = reducer(s, { type: 'place', spec: 'res_hut', x, y });
    }
  }
  s = {
    ...s,
    buildings: [...s.buildings, { id: s.nextId, spec: 'off_towers_downtown', x: 300, y: 200 }],
    nextId: s.nextId + 1,
  };
  for (let i = 0; i < 40; i++) {
    const startPop = s.population;
    s = reducer(s, { type: 'tick' });
    const endPop = s.population;
    if (endPop > startPop && startPop > 0) {
      return { s, startPop, endPop };
    }
  }
  return null;
}

test('BUG-419: wages check passes on a tick that grows population (start-pop basis)', () => {
  const grown = advanceToGrowingTick();
  assert.ok(grown, 'reached a tick that grows population from a non-zero base');
  const { s, startPop, endPop } = grown;

  // Guard: this test is only meaningful if the two bases genuinely differ.
  assert.ok(endPop > startPop, `population grew within the tick (${startPop} -> ${endPop})`);
  assert.equal(s.lastFlows.population, startPop, 'lastFlows records the start-of-tick basis');

  // The engine charged wages on the start-of-tick workforce, so the recorded flow equals
  // rawWagesAt(startPop), NOT rawWagesAt(endPop).
  const actualWages = s.lastFlows.outflows.find((f) => f.label === 'Wages').value;
  assert.ok(actualWages > 0, 'test setup: fixture must pay nonzero raw wages (job capacity filled)');
  assert.equal(actualWages, rawWagesAt(s, startPop), 'recorded wages match the start-of-tick basis');
  assert.notEqual(
    rawWagesAt(s, endPop),
    actualWages,
    'end-of-tick basis (the old buggy recompute) genuinely differs — proves the test exercises the bug'
  );

  const report = runConsistencyChecks(s);

  const wages = report.checks.find((c) => c.id === 'flows.wages-matches');
  assert.ok(wages, 'wages check exists');
  assert.equal(wages.ok, true, `flows.wages-matches must pass on a growing tick: ${wages.detail}`);

  // Council Tax is charged on the same start-of-tick basis and must likewise agree.
  const council = report.checks.find((c) => c.id === 'flows.council-tax-matches');
  assert.equal(council.ok, true, `flows.council-tax-matches must pass on a growing tick: ${council.detail}`);
  const actualCouncil = s.lastFlows.inflows.find((f) => f.label === 'Council Tax')?.value ?? 0;
  assert.equal(actualCouncil, councilTaxPerTick(startPop, s.taxRates.residential),
    'recorded council tax matches the start-of-tick basis');

  // Money is NOT leaking — the divergence was always flow-vs-state timing, not funds.
  const conservation = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.equal(conservation.ok, true, `conservation must still hold: ${conservation.detail}`);
});
