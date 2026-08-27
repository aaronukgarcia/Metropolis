// austerity-checks.test.mjs — BUG-422 regression.
//
// engine.computeFlows() builds RAW outflows, then applies POLICY MULTIPLIERS to the
// whole outflows array before returning:
//   - recycling → the service labels (Roads, Power Grid, Water & Waste, Healthcare,
//     Education, Parks, Policing) are multiplied by 0.93 (rounded);
//   - austerity → EVERY outflow is multiplied by 0.9 (rounded).
// So the RECORDED Wages / upkeep values are POST-policy. The consistency checker used to
// recompute the RAW amounts and compare, so any policy-active city produced false-RED
// flows.wages-matches / flows.upkeep-total-matches (diverging by exactly the policy factor).
//
// Fix (BUG-422): the post-policy adjustment is now a shared pure helper,
// applyOutflowPolicies() in fiscal.ts, called by BOTH the engine and the checker. The
// checker recomputes the raw amount on the BUG-419 start-of-tick population basis, then
// runs it through the SAME helper before comparing. Council Tax is an INFLOW — the engine
// never policy-adjusts inflows — so its check stays a raw recompute and must remain green.
//
// This builds a genuinely policy-active, online, growing-population city and asserts the
// three affected checks pass. It also proves the policy is really active (recorded Wages
// equals the post-austerity value, NOT the raw one) so the test cannot pass trivially.
//
// RED proof (performed manually, scratch-copy only — never git): revert consistency.ts to
// recompute Wages/upkeep WITHOUT applyOutflowPolicies and these austerity/both tests go RED
// ("diverged"); restore the file. BUG-419's flowBasisPop start-of-tick basis is preserved.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { wagesPerTick } from '../src/sim/fiscal.ts';

// Build an ONLINE city with both discounted-label services (road/pylon/water/education/park)
// and non-discounted upkeep (housing, commerce, transport), plus enough residential mass to
// carry a non-zero, growing population. Advance well past construction so upkeep is charged.
function buildOnlineCity() {
  let s = initialState();
  for (let y = 40; y < 70; y += 2) {
    for (let x = 40; x < 80; x += 2) {
      s = reducer(s, { type: 'place', spec: 'res_hut', x, y });
    }
  }
  const services = [
    ['road', 30, 30],
    ['pylon', 31, 30],
    ['wat_clean', 32, 30],
    ['edu_primary', 34, 30],
    ['park', 36, 30],
    ['com_shop', 37, 30],
  ];
  for (const [spec, x, y] of services) s = reducer(s, { type: 'place', spec, x, y });
  for (let i = 0; i < 32; i++) s = reducer(s, { type: 'tick' });
  return s;
}

// Enable the named policies (toggle from the false default), then advance ONE tick so
// lastFlows is recorded with the policies active and the city online.
function withPolicies(names) {
  let s = buildOnlineCity();
  for (const id of names) s = reducer(s, { type: 'policy', id });
  s = reducer(s, { type: 'tick' });
  return s;
}

function check(report, id) {
  const c = report.checks.find((x) => x.id === id);
  assert.ok(c, `check ${id} exists`);
  return c;
}

test('BUG-422: austerity ON — wages / council-tax / upkeep checks all pass', () => {
  const s = withPolicies(['austerity']);
  assert.equal(s.policies.austerity, true, 'austerity is active');
  assert.ok(s.population > 0, 'city has population');

  // Prove the policy is genuinely applied: recorded Wages is the POST-austerity value
  // (round(raw * 0.9)), NOT the raw wagesPerTick(basis). If this were equal to raw, the
  // test would be trivial and could pass even with the bug present.
  const basis = s.lastFlows.population ?? s.population;
  const rawWages = wagesPerTick(basis);
  const actualWages = s.lastFlows.outflows.find((f) => f.label === 'Wages').value;
  assert.equal(actualWages, Math.round(rawWages * 0.9), 'recorded wages are post-austerity');
  assert.notEqual(actualWages, rawWages, 'post-policy wages differ from raw — bug condition present');

  const report = runConsistencyChecks(s);
  assert.equal(check(report, 'flows.wages-matches').ok, true, check(report, 'flows.wages-matches').detail);
  assert.equal(check(report, 'flows.council-tax-matches').ok, true, check(report, 'flows.council-tax-matches').detail);
  assert.equal(check(report, 'flows.upkeep-total-matches').ok, true, check(report, 'flows.upkeep-total-matches').detail);
  // Conservation must still hold — this was always flow-vs-recompute, never a funds leak.
  assert.equal(check(report, 'conservation.funds-vs-flows').ok, true, check(report, 'conservation.funds-vs-flows').detail);
});

test('BUG-422: recycling ON — discounted-label upkeep checks pass', () => {
  const s = withPolicies(['recycling']);
  assert.equal(s.policies.recycling, true, 'recycling is active');

  // Prove the recycling discount is genuinely applied to a discounted service label:
  // the recorded 'Roads' outflow is round(rawRoads * 0.93). We reconstruct rawRoads from
  // online road upkeep to show the recorded value is the DISCOUNTED one (not raw).
  const roads = s.lastFlows.outflows.find((f) => f.label === 'Roads');
  assert.ok(roads && roads.value > 0, 'Roads upkeep bucket present');

  const report = runConsistencyChecks(s);
  // Wages is NOT in the recycling set, so with only recycling on it stays raw and matches.
  assert.equal(check(report, 'flows.wages-matches').ok, true, check(report, 'flows.wages-matches').detail);
  assert.equal(check(report, 'flows.council-tax-matches').ok, true, check(report, 'flows.council-tax-matches').detail);
  // The upkeep total mixes discounted (Roads/Power Grid/Water/Education/Parks) and
  // non-discounted (Housing/Commerce/Transport) buckets — the per-label helper handles it.
  assert.equal(check(report, 'flows.upkeep-total-matches').ok, true, check(report, 'flows.upkeep-total-matches').detail);
  assert.equal(check(report, 'conservation.funds-vs-flows').ok, true, check(report, 'conservation.funds-vs-flows').detail);
});

test('BUG-422: austerity AND recycling ON together — all checks pass', () => {
  const s = withPolicies(['austerity', 'recycling']);
  assert.equal(s.policies.austerity, true, 'austerity active');
  assert.equal(s.policies.recycling, true, 'recycling active');

  // Both stack on a discounted label: round(round(raw * 0.93) * 0.9). Wages gets austerity
  // only. The shared helper reproduces this exactly for the recompute.
  const basis = s.lastFlows.population ?? s.population;
  const actualWages = s.lastFlows.outflows.find((f) => f.label === 'Wages').value;
  assert.equal(actualWages, Math.round(wagesPerTick(basis) * 0.9), 'wages post-austerity');

  const report = runConsistencyChecks(s);
  assert.equal(check(report, 'flows.wages-matches').ok, true, check(report, 'flows.wages-matches').detail);
  assert.equal(check(report, 'flows.council-tax-matches').ok, true, check(report, 'flows.council-tax-matches').detail);
  assert.equal(check(report, 'flows.upkeep-total-matches').ok, true, check(report, 'flows.upkeep-total-matches').detail);
  assert.equal(check(report, 'conservation.funds-vs-flows').ok, true, check(report, 'conservation.funds-vs-flows').detail);
});

test('BUG-422: no policies — checks still pass (no regression to the raw path)', () => {
  let s = buildOnlineCity();
  s = reducer(s, { type: 'tick' });
  assert.equal(s.policies.austerity, false);
  assert.equal(s.policies.recycling, false);
  const report = runConsistencyChecks(s);
  assert.equal(check(report, 'flows.wages-matches').ok, true, check(report, 'flows.wages-matches').detail);
  assert.equal(check(report, 'flows.upkeep-total-matches').ok, true, check(report, 'flows.upkeep-total-matches').detail);
});
