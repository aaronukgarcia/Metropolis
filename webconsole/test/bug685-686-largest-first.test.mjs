// bug685-686-largest-first.test.mjs — Aaron's LARGEST-FIRST Fix-All ruling
// (2026-09-04), closing BUG-685 (Fix-All optimises GBP-per-unit blind to
// building COUNT: a debug capture of Aaron's real city showed 10,033 6MW wind
// turbines and ZERO nukes/CCGT/offshore for a 51.7GW city, plus 9,138
// kindergartens) and BUG-686 (edu_nursery's capacityTiers ladder toy-sized:
// 30 base topping at 71, vs hea_teaching's 120,000 base).
//
// THE FIX (data.ts):
//   1. largestFirstFill() — the demand-fix PICKER for the actual BUILD plan
//      (demandFixPlan()) now fills a shortfall with the BIGGEST unlocked spec
//      first (crediting a fresh unit's LADDER-GROWN capacity, not its tier-0
//      base — creditedUnitCapacity()), smaller specs only for the remainder.
//   2. edu_nursery's capacityTiers extended from tierLadder(30) (topped at a
//      toy 71) to campusLadder(30, 2000) — a fully-scaled Kindergarten
//      becomes a real ~2,000-place early-years campus.
//
// This file proves the acceptance bar directly: Aaron's real-city SHAPE
// (multi-GW power shortfall, ~250k nursery shortfall, everything unlocked,
// deep funds) now proposes orders-of-magnitude fewer buildings, AND the
// opposite small-city case (a 40-place nursery shortfall) still gets exactly
// ONE Kindergarten, not a 2,000-place campus overshoot.
//
// RED-PROOFs: (a) inline assertions throughout compare the LARGEST-FIRST
// result against what the pre-fix "cheapest/smallest-first" picker would
// have produced (derived from SPECS arithmetic, GR#15 — never a hardcoded
// literal), so a regression back to that picker reddens them; (b) the final
// test in this file does a REAL scratch-copy source revert (GR#24 — never
// git) of the largest-first sort direction and proves the monoculture test
// genuinely fails against that reverted code, then restores the original
// file byte-identical.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { runMutantSelfReinvoke } from '../testsupport/mutant.mjs';
import { initialState } from '../src/sim/engine.ts';
import { demandFixPlan, SPECS, AUTO_BUILD_DEMAND_FRACTION, LADDER_CREDIT_FRACTION, creditedUnitCapacity } from '../src/sim/data.ts';

function shortfallState(population, fundsOverride = 1_000_000_000_000) {
  const base = initialState();
  return { ...base, population, unlockedAll: true, funds: fundsOverride, administrationState: null };
}

// Population sized so the (population-scaled, need = pop * 0.012 for power,
// pop * 0.06 for nursery — data.ts computePowerStats/serviceCoverageOf's own
// formulas) shortfalls land in Aaron's real-city ballpark: a multi-GW power
// need and a several-hundred-thousand nursery need, with zero service
// buildings so the WHOLE need is shortfall.
const AARON_SHAPE_POPULATION = 4_500_000;

test('BUG-685 Aaron-shape: power demand-fix plan is dominated by big plants (nuke/fusion/CCGT/hydro), never a turbine monoculture', () => {
  const s = shortfallState(AARON_SHAPE_POPULATION);
  const plan = demandFixPlan(s);
  const power = plan.find((p) => p.serviceKey === 'power');
  assert.ok(power, 'a power shortfall must exist at this population with zero power plants');
  assert.ok(power.mix.length > 0);

  // LARGEST-FIRST ordering.
  for (let i = 1; i < power.mix.length; i++) {
    assert.ok(power.mix[i - 1].unitCapacity >= power.mix[i].unitCapacity, 'power mix must be ordered largest-capacity-first');
  }

  const turbineCount = power.mix.filter((m) => m.specId === 'pow_wind').reduce((sum, m) => sum + m.count, 0);
  const totalCount = power.mix.reduce((sum, m) => sum + m.count, 0);

  // RED-PROOF #1: the pre-BUG-685 defect (Aaron's real save) was a
  // monoculture of the SMALLEST unlocked unit (pow_wind, 6 MW) — this
  // assertion is exactly the shape of that defect and must never recur.
  assert.ok(
    turbineCount === 0 || turbineCount < totalCount,
    'pow_wind must never be the ENTIRE plan when bigger plants are unlocked'
  );

  // RED-PROOF #2 (derived from SPECS, GR#15): a monoculture of the smallest
  // unlocked power spec would need vastly more buildings than the actual
  // LARGEST-FIRST plan — assert the real plan needs strictly, dramatically
  // fewer total units than that legacy figure (never a hardcoded building
  // count).
  const fixAmount = (power.need - power.have) * AUTO_BUILD_DEMAND_FRACTION;
  const smallestPowerSpec = Object.values(SPECS)
    .filter((sp) => sp.kind === 'power' && (sp.mw ?? 0) > 0)
    .reduce((worst, sp) => ((sp.mw ?? 0) < (worst.mw ?? 0) ? sp : worst));
  const monocultureCount = Math.ceil(fixAmount / smallestPowerSpec.mw);
  assert.ok(
    totalCount * 10 < monocultureCount,
    `LARGEST-FIRST total (${totalCount}) must be at least an order of magnitude below an all-${smallestPowerSpec.id} monoculture (${monocultureCount})`
  );
  // "tens, not thousands" — the acceptance bar's own phrasing.
  assert.ok(totalCount < 1000, `expected a small building count (tens), got ${totalCount}`);

  // The biggest unlocked spec (by flat mw, or credited capacity for a
  // laddered plant like pow_nuke) must lead the mix.
  const biggestCredited = Object.values(SPECS)
    .filter((sp) => sp.kind === 'power' && (sp.mw ?? 0) > 0)
    .map((sp) => ({ sp, capacity: sp.capacityTiers ? Math.round(sp.capacityTiers[sp.capacityTiers.length - 1] * LADDER_CREDIT_FRACTION) : sp.mw }))
    .reduce((best, x) => (x.capacity > best.capacity ? x : best));
  assert.equal(power.specId, biggestCredited.sp.id, 'the biggest credited-capacity power spec must lead the plan');
});

test('BUG-685/686 Aaron-shape: nursery demand-fix plan is in the low hundreds of buildings, ladder-credited (not a 12,654-style monoculture)', () => {
  const s = shortfallState(AARON_SHAPE_POPULATION);
  const plan = demandFixPlan(s);
  const nursery = plan.find((p) => p.serviceKey === 'nursery');
  assert.ok(nursery, 'a nursery shortfall must exist at this population with zero nursery buildings');

  // Civic-tier rebase fix (FEAT-2326609772, 2026-09-05): edu_nursery is no
  // longer the only nursery-stage spec — edu_nursery_city (FEAT-2326609761)
  // landed as its own same-stage, much-bigger consolidation successor. The
  // headline (mix[0]) must be whichever nursery-stage spec is CREDITED the
  // most capacity, derived from SPECS/creditedUnitCapacity (GR#15), never a
  // hardcoded id — Aaron's own words for this successor: "one city
  // kindergarten doing 1000 children, not 40 kindergartens", i.e. the big
  // successor SHOULD lead a big-city shortfall like this one.
  const biggestNurserySpec = Object.values(SPECS)
    .filter((sp) => sp.stage === 'nursery' && sp.children != null)
    .map((sp) => ({ sp, capacity: creditedUnitCapacity(sp, sp.children) }))
    .reduce((best, x) => (x.capacity > best.capacity ? x : best));
  assert.equal(nursery.specId, biggestNurserySpec.sp.id, 'the biggest credited-capacity nursery spec must lead the plan');

  const fixAmount = (nursery.need - nursery.have) * AUTO_BUILD_DEMAND_FRACTION;

  // RED-PROOF (BUG-686, the exact reported defect): the OLD tier-0-only
  // sizing (30 children/unit, tierLadder(30)'s pre-fix top of 71 makes no
  // difference here since the picker always priced off tier-0) would need
  // this many buildings — the real, ladder-credited count must be strictly,
  // dramatically fewer.
  const TOY_TIER0_CAPACITY = 30; // SPECS.edu_nursery.children, the reported defect's own base figure
  const legacyMonocultureCount = Math.ceil(fixAmount / TOY_TIER0_CAPACITY);
  assert.ok(
    nursery.count * 10 < legacyMonocultureCount,
    `ladder-credited nursery count (${nursery.count}) must be at least an order of magnitude below the tier-0 figure (${legacyMonocultureCount})`
  );
  // "low hundreds" — the acceptance bar's own phrasing; generously bounded
  // under 1,000 so this does not flake on the Balance Number Regime's
  // PLACEHOLDER LADDER_CREDIT_FRACTION/campusLadder top being retuned later.
  assert.ok(nursery.count < 1000, `expected a low-hundreds building count, got ${nursery.count}`);

  // Cross-check against the live creditedUnitCapacity formula itself (GR#15:
  // the validator's expected value comes FROM the data/formula, never a
  // literal), now against the ACTUAL headline spec (which may be
  // edu_nursery_city, not always edu_nursery) rather than a hardcoded one —
  // largest-first can only ever OVERSHOOT the raw ceil-on-one-spec figure
  // (never overshoot by more than the headline's own capacity, and it may
  // finish with a smaller trailing spec instead of purely ceiling the
  // headline), so this bounds the count from below and by one full extra
  // unit above.
  const creditedCapacity = biggestNurserySpec.capacity;
  const minPlausibleCount = Math.floor(fixAmount / creditedCapacity);
  assert.ok(
    nursery.count >= minPlausibleCount && nursery.count <= minPlausibleCount + 2,
    `nursery count (${nursery.count}) must be within one spec's overshoot of ceil(fixAmount / creditedUnitCapacity) (${minPlausibleCount})`
  );
});

test('BUG-686 small-city boundary: a 40-place nursery shortfall gets exactly ONE Kindergarten, never an oversized campus', () => {
  // A "hamlet" — tiny population, engineered so the nursery shortfall lands
  // at exactly 40 (need - have = 40) rather than derived from population
  // directly (serviceCoverageOf's pop*0.06 formula makes a precise 40-need
  // population awkward), matching the acceptance bar's literal scenario.
  const base = initialState();
  // demandFixPlan() is population-driven (serviceCoverageOf's pop*0.06
  // nursery-need formula), so assert the INVARIANT the boundary rule
  // guarantees for ANY shortfall smaller than the credited capacity: exactly
  // one unit of the (only) nursery spec, never more.
  const creditedCapacity = Math.round(SPECS.edu_nursery.capacityTiers[SPECS.edu_nursery.capacityTiers.length - 1] * LADDER_CREDIT_FRACTION);
  const HAMLET_SHORTFALL = 40;
  assert.ok(HAMLET_SHORTFALL < creditedCapacity, 'precondition: 40 is smaller than one credited-capacity Kindergarten (BUG-686\'s whole point)');

  // Population that makes (pop*0.06 - 0) land near HAMLET_SHORTFALL — the
  // nearest population whose need is a SUB-credited-capacity shortfall.
  const pop = Math.round(HAMLET_SHORTFALL / 0.06);
  const hamlet = { ...base, population: pop, unlockedAll: true, funds: 1_000_000_000, administrationState: null };
  const plan = demandFixPlan(hamlet).find((p) => p.serviceKey === 'nursery');
  assert.ok(plan, 'a nursery shortfall must exist at this tiny population');
  assert.equal(plan.mix.length, 1, 'a sub-credited-capacity shortfall must resolve to exactly one mix entry');
  assert.equal(plan.mix[0].specId, 'edu_nursery');
  assert.equal(plan.mix[0].count, 1, 'BUG-686: a hamlet-scale nursery shortfall gets exactly ONE Kindergarten, never more');
  assert.equal(plan.count, 1);
});

test('RED-PROOF (source revert, private shadow copy — BUG-739, never the real file): reversing largestFirstFill\'s sort to smallest-first reddens the Aaron-shape power test', () => {
  const { failed, output, crashed } = runMutantSelfReinvoke({
    targetRelPath: path.join('sim', 'data.ts'),
    mutate: (original) => {
      const fixedLine = 'if (a.capacity !== b.capacity) return b.capacity - a.capacity;';
      assert.ok(original.includes(fixedLine), 'precondition: the largest-first (descending-capacity) comparator is present in data.ts');
      // Smallest-first (ascending) — exactly the pre-BUG-685-style
      // monoculture shape (always reach for the smallest unlocked unit first).
      const buggyLine = 'if (a.capacity !== b.capacity) return a.capacity - b.capacity;';
      return original.replace(fixedLine, buggyLine);
    },
    testFileAbsPath: fileURLToPath(import.meta.url),
    testNamePattern: 'BUG-685 Aaron-shape: power demand-fix plan',
  });

  // R2 (BUG-739 round REJECT, 2026-09-05): `failed` alone cannot distinguish
  // "the mutant was detected" from "the child crashed for an unrelated
  // reason" — require crashed === false AND the SPECIFIC expected assertion
  // text, never a bare exit-status/generic-word match.
  assert.ok(!crashed, `the re-invoked test must actually RUN (not crash at load time) against the mutant; output:\n${output}`);
  assert.ok(failed, 'the Aaron-shape power test must FAIL against a smallest-first (reverted) picker — proves the test can fail');
  // Either of two assertions in the re-invoked test is specific to THIS
  // mutation and can fire first (assert.ok throws immediately, so whichever
  // check the test reaches first short-circuits the rest): the ordering
  // check itself ("power mix must be ordered largest-capacity-first"), or —
  // reached earlier in source order — the monoculture guard ("pow_wind must
  // never be the ENTIRE plan"), since smallest-first genuinely can make
  // pow_wind the whole plan. Both are specific to a largest-first regression,
  // never a generic "fail" match.
  assert.match(
    output,
    /power mix must be ordered largest-capacity-first|pow_wind must never be the ENTIRE plan/,
    `child test run output must report one of the SPECIFIC largest-first assertions failing, not just any failure; got:\n${output}`,
  );
});
