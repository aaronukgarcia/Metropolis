// bug662-college-capacity-tier.test.mjs — BUG-662: College children capacity
// undercounted via a capacityAtTier fallback gap, suppressing school
// auto-scale (Aaron's real save: 571,390 -> 656,890 once fixed).
//
// THE BUG: serviceCapacityAggregates() (src/sim/data.ts, feeds
// serviceCoverageOf()'s nursery/primary/college/gp/hosp/police rows and
// therefore demandFixPlan()) summed the FLAT per-spec base
// (`sp.children ?? 0` / `sp.served ?? 0`) for every online building of the
// relevant stage/kind, never reading `b.capacityTier`. edu_tech (Technical
// College, stage 'tertiary') carries its OWN capacityTiers ladder — a
// building auto-scaled by evaluateBuildingMonitors' 'children' monitor (which
// DOES bump capacityTier and DOES charge the player, engine.ts) never had
// that higher real capacity reflected in the citywide 'college' coverage row:
// the DemandDock/auto-build system kept comparing demand against the
// building's ORIGINAL tier-0 capacity forever, so an already-auto-scaled
// college's headroom was invisible to it (demandFixPlan would go on
// recommending/auto-building MORE college capacity that was not actually
// needed — the auto-scale event's effect was suppressed from the demand
// signal that is supposed to react to it). The SAME gap applied to every
// other stage/kind fed by this aggregate: nursery, primary, gp, hosp, police.
//
// THE FIX: childrenAtTier()/servedAtTier() (src/sim/data.ts, defined just
// after capacityAtTier()) route every read through capacityAtTier() when the
// spec carries a ladder, falling back to the flat field only for specs with
// none (col_sixth/uni carry no ladder — untouched). serviceCapacityAggregates,
// totalChildrenCapacity and totalServedCapacity now all go through these two
// helpers, so a building's CURRENT tier is always what gets counted.
//
// GR#15: every expected number below is derived from capacityAtTier() itself
// (the canonical oracle already used by totalChildrenCapacity/BUG-509's
// residential precedent), never a hand-typed literal.
//
// RED proof (BUG-739: a private webconsole/test/helpers/mutant.mjs shadow
// copy of webconsole/src + webconsole/test, never the real shared data.ts):
// the last test textually reverts serviceCapacityAggregates' two
// children/served accumulator lines back to their pre-fix flat form inside
// that shadow copy, re-runs this file's first test as a child process
// against the shadow's reverted source, and asserts it FAILS.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { runMutantSelfReinvoke } from '../testsupport/mutant.mjs';
import { SPECS, capacityAtTier, serviceCoverageOf, demandFixPlan } from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

// edu_tech (Technical College) is the concrete "College" repro case: stage
// 'tertiary' (feeds the 'college' coverage row) AND carries its own
// capacityTiers ladder (unlike col_sixth/uni, which have none and were
// already handled by the pre-existing children-fallback guard).
const SP = SPECS['edu_tech'];
const TIER = 4;
const FLAT_BASE = SP.children;
const TIERED_CAP = capacityAtTier(SP, TIER);

function coverageOfService(s, svcId) {
  const row = serviceCoverageOf(s).find((c) => c.id === svcId);
  assert.ok(row, `no coverage row for service "${svcId}"`);
  return row;
}

/** A city with one auto-scaled edu_tech building (capacityTier = TIER,
 *  always-online: no builtTick means computeIsOnline()'s construction/road
 *  gates never apply, matching the other tier-focused fixtures in this
 *  suite). */
function cityWithUpgradedEduTech(population) {
  const s = initialState();
  return {
    ...s,
    population,
    funds: 100_000_000,
    unlockedAll: true,
    buildingMonitors: [],
    buildings: [{ id: 90001, spec: 'edu_tech', x: 5, y: 5, capacityTier: TIER }],
  };
}

test('BUG-662 precondition: edu_tech tier 4 capacity strictly exceeds its flat tier-0 base', () => {
  assert.ok(Array.isArray(SP.capacityTiers) && SP.capacityTiers.length > TIER, 'edu_tech has a ladder reaching tier 4');
  assert.ok(TIERED_CAP > FLAT_BASE, `tier ${TIER} capacity (${TIERED_CAP}) must exceed the flat base (${FLAT_BASE})`);
});

test("BUG-662: serviceCoverageOf's 'college' row reads the auto-scaled building's CURRENT tier, not its tier-0 base", () => {
  const s = cityWithUpgradedEduTech(0);
  const college = coverageOfService(s, 'college');
  assert.equal(college.cap, TIERED_CAP, "the college coverage row's cap must be the tiered capacity");
  assert.notEqual(college.cap, FLAT_BASE, 'the coverage row must not be pinned at the flat tier-0 base once capacityTier > 0');
});

test('BUG-662: demandFixPlan no longer proposes redundant new college capacity once the auto-scaled college already covers demand', () => {
  // Population sized so demand (pop * 0.05, serviceCoverageOf's 'college'
  // need formula) sits STRICTLY between the flat base and the true tiered
  // capacity: the pre-fix aggregate reports a shortfall (need > FLAT_BASE)
  // even though the real, already-paid-for auto-scale headroom (TIERED_CAP)
  // fully covers it.
  const need = Math.round((FLAT_BASE + TIERED_CAP) / 2);
  const population = Math.ceil(need / 0.05);
  assert.ok(population * 0.05 > FLAT_BASE, 'precondition: demand exceeds the flat base (pre-fix would show a shortfall)');
  assert.ok(population * 0.05 <= TIERED_CAP, 'precondition: demand is fully covered by the real tiered capacity');

  const s = cityWithUpgradedEduTech(population);
  const plan = demandFixPlan(s);
  const collegeEntry = plan.find((p) => p.serviceKey === 'college');
  assert.equal(
    collegeEntry,
    undefined,
    'the already-auto-scaled college fully covers demand — demandFixPlan must not recommend building MORE college capacity'
  );
});

test('BUG-662-class: the same tier-aware guard fixes the sibling services (nursery/primary/gp/hosp/police)', () => {
  const cases = [
    { spec: 'edu_nursery', svc: 'nursery', field: 'children' },
    { spec: 'edu_primary', svc: 'primary', field: 'children' },
    { spec: 'hea_clinic', svc: 'gp', field: 'served' },
    { spec: 'hea_hospital', svc: 'hosp', field: 'served' },
    { spec: 'pol_station', svc: 'police', field: 'served' },
  ];
  for (const { spec, svc, field } of cases) {
    const sp = SPECS[spec];
    assert.ok(Array.isArray(sp.capacityTiers) && sp.capacityTiers.length > 2, `${spec} has a multi-tier ladder`);
    const tier = 2;
    const flatBase = sp[field];
    const tieredCap = capacityAtTier(sp, tier);
    assert.ok(tieredCap > flatBase, `${spec} tier ${tier} capacity must exceed its flat base`);

    const s = {
      ...initialState(),
      population: 0,
      funds: 100_000_000,
      unlockedAll: true,
      buildingMonitors: [],
      buildings: [{ id: 90002, spec, x: 5, y: 5, capacityTier: tier }],
    };
    const row = coverageOfService(s, svc);
    assert.equal(row.cap, tieredCap, `${svc} coverage row must read ${spec}'s current tier, not its flat base`);
  }
});

test('BUG-662: RED proof — reverting the fix (flat sum, no capacityTier read) reproduces the pre-fix undercount', () => {
  // BUG-739: mutation now runs against a private webconsole/test/helpers/
  // mutant.mjs shadow copy of BOTH webconsole/src and webconsole/test (this
  // test re-invokes ITSELF as a child, so the shadow needs the test file too)
  // — the real, shared data.ts is never written to.
  const { failed, output, crashed } = runMutantSelfReinvoke({
    targetRelPath: path.join('sim', 'data.ts'),
    mutate: (original) => {
      const fixedLine = "    else if (sp.stage === 'tertiary') tertiary += childrenAtTier(sp, tier);";
      assert.ok(original.includes(fixedLine), 'precondition: the fixed tier-aware tertiary line is present in data.ts');
      const buggyLine = "    else if (sp.stage === 'tertiary') tertiary += sp.children ?? 0;";
      return original.replace(fixedLine, buggyLine);
    },
    testFileAbsPath: fileURLToPath(import.meta.url),
    testNamePattern: "BUG-662: serviceCoverageOf's 'college' row",
  });

  // R2 (BUG-739 round REJECT, 2026-09-05): `failed` alone cannot distinguish
  // "the mutant was detected" from "the child crashed for an unrelated
  // reason" — require crashed === false AND the SPECIFIC expected assertion
  // text, never a bare exit-status/generic-word match.
  assert.ok(!crashed, `the re-invoked test must actually RUN (not crash at load time) against the mutant; output:\n${output}`);
  assert.ok(failed, "the 'college' coverage test must FAIL against the reverted (flat-sum) data.ts — proves the test can fail");
  assert.match(
    output,
    /the college coverage row's cap must be the tiered capacity/,
    `child test run output must report the SPECIFIC tiered-capacity assertion failing, not just any failure; got:\n${output}`,
  );
});
