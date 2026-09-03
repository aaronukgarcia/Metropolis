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
//
// BUG-wage-drift-2026-09 (Wage Stage 1, commit 4a8e9ed): this fixture used to have
// population but ZERO job buildings, so wagesPerTick(population) (the OLD flat-wage SSOT)
// baked a nonzero expectation that no longer matches reality — engine.ts's 'Wages' outflow
// is now sectorWagesPerTick(filledJobsBySector(s)).totalPerTick, paid on FILLED jobs, and a
// jobless city now legitimately pays £0. Fix: (a) add a real job building (off_suite, 25
// office jobs) so wages are genuinely nonzero, and (b) recompute the RAW expectation via the
// NEW SSOT — sectorWagesPerTick(filledJobsFromCapacityAndPopulation(totalJobsBySector(s),
// basis)).totalPerTick — on the SAME flowBasisPop basis the consistency checker itself uses
// (consistency.ts), preserving this test's actual intent: the start-of-tick population basis
// survives austerity/recycling's post-policy multipliers.
//
// The off_suite building is SPLICED directly into s.buildings (no builtTick field) rather
// than routed through the 'place' reducer action like the rest of buildOnlineCity's fixture.
// Verified empirically (2026-09-03): 'place' at this fixture's XP/funds point in the build
// sequence is unreliable for a NEW building (specUnlocked's XP-level gate and the
// autoConnect/orphan-connect funds-and-routing dance both silently no-op the placement
// depending on exactly how much XP/funds the preceding res_hut/service placements consumed
// — orthogonal to what this test is trying to prove). A building with no `builtTick` is a
// documented, pre-existing convention this codebase already treats as complete/online
// unconditionally (data.ts isOnline(): "a bespoke/legacy state ... has no connectivity
// graph, so the gate is skipped" — `if (b.builtTick == null) return true` is the FIRST line
// of isOnline()), the same convention every congestion-teeth.test.mjs city() fixture already
// relies on — so this deterministically guarantees the job building counts, with no
// dependency on road layout, funds, or XP level.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { sectorWagesPerTick } from '../src/sim/fiscal.ts';
import { totalJobsBySector, filledJobsFromCapacityAndPopulation } from '../src/sim/data.ts';

// Raw (pre-policy) wages on a given population basis — the SAME SSOT formula
// engine.ts's computeFlows and consistency.ts's recompute both funnel through
// (sectorWagesPerTick fed FILLED jobs, never raw capacity — fiscal.ts F1).
function rawWagesAt(s, basis) {
  return sectorWagesPerTick(filledJobsFromCapacityAndPopulation(totalJobsBySector(s), basis))
    .totalPerTick;
}

// Build an ONLINE city with both discounted-label services (road/pylon/water/education/park)
// and non-discounted upkeep (housing, commerce, transport), a real job building (off_suite)
// so Wages is genuinely nonzero, plus enough residential mass to carry a non-zero, growing
// population. Advance well past construction so upkeep is charged.
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
  // A real job building (see the fixture-doc comment above for why this is spliced
  // directly rather than routed through 'place').
  s = {
    ...s,
    buildings: [...s.buildings, { id: s.nextId, spec: 'off_suite', x: 300, y: 200 }],
    nextId: s.nextId + 1,
  };
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
  // (round(raw * 0.9)), NOT the raw sectorWagesPerTick(...) basis. If this were equal to
  // raw, the test would be trivial and could pass even with the bug present. Also prove
  // the fixture itself pays real wages (off_suite's 25 jobs) — a jobless city legitimately
  // pays £0 under Wage Stage 1, which would make this whole test vacuous.
  const basis = s.lastFlows.population ?? s.population;
  const rawWages = rawWagesAt(s, basis);
  const actualWages = s.lastFlows.outflows.find((f) => f.label === 'Wages').value;
  assert.ok(rawWages > 0, 'test setup: fixture must pay nonzero raw wages (off_suite jobs filled)');
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
  const rawWages = rawWagesAt(s, basis);
  const actualWages = s.lastFlows.outflows.find((f) => f.label === 'Wages').value;
  assert.ok(rawWages > 0, 'test setup: fixture must pay nonzero raw wages (off_suite jobs filled)');
  assert.equal(actualWages, Math.round(rawWages * 0.9), 'wages post-austerity');
  assert.notEqual(actualWages, rawWages, 'post-policy wages differ from raw — bug condition present');

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
