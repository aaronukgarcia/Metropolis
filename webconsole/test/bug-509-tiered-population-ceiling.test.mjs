// bug-509-tiered-population-ceiling.test.mjs — BUG-509: residential population
// ceiling ignores capacityTier.
//
// THE BUG: advance()'s `capacity` IIFE (src/sim/engine.ts, the population-growth
// ceiling just above the growth-model block) summed the FLAT per-spec base
// (`sp.residents ?? 8`) for every online residential building, never reading
// `b.capacityTier`. The canonical tiered capacity helpers — `residentsCapacity`
// and `onlineResidentsCapacity` (src/sim/data.ts) — correctly sum
// `capacityAtTier(sp, b.capacityTier ?? 0)`, and the jobs side (`totalJobs`)
// already does the tiered read. `evaluateBuildingMonitors` (engine.ts) bumps
// `building.capacityTier` AND charges the player (Building Auto-Scale outflow)
// based on utilization computed against the TIERED capacity — but the
// population ceiling the growth model converges toward never reflected that
// higher tier, so the auto-scale spend bought nothing: the city could never
// actually grow into the capacity it just paid to unlock.
//
// THE FIX: the ceiling now calls `onlineResidentsCapacity(s)` (residential-only,
// isOnline-gated, tier-aware) instead of the inline flat sum.
//
// RED proof (BUG-739: a private webconsole/test/helpers/mutant.mjs shadow
// copy of webconsole/src + webconsole/test, never the real shared
// engine.ts): the third test below textually swaps the fixed
// `const capacity = onlineResidentsCapacity(s);` line back to the original
// flat-sum IIFE inside that shadow copy, re-runs the deterministic ceiling
// test (second test below) as a child process against the shadow's reverted
// source, and asserts it FAILS.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { runMutantSelfReinvoke } from '../testsupport/mutant.mjs';
import { SPECS, onlineResidentsCapacity, residentsCapacity } from '../src/sim/data.ts';
import { initialState, reducer } from '../src/sim/engine.ts';

// res_estate: flat base (tier 0) = 1500 residents; tier 2 (capacityTiers[2]) = 1815.
const SP = SPECS['res_estate'];
const FLAT_BASE = SP.residents;
const TIER = 2;
const TIERED_CAP = SP.capacityTiers[TIER];

/**
 * Places a real, road-connected res_estate building (via the ordinary 'place'
 * reducer action, so it goes through the SAME road-connectivity computation
 * advance() itself runs every tick — no synthetic `roadConnectivity: undefined`
 * bypass, which only survives until advance()'s own recompute at the top of
 * the tick), then fast-forwards its construction and bumps capacityTier
 * directly (simulating a completed Building Auto-Scale upgrade).
 */
function cityWithUpgradedResEstate(population) {
  let s = initialState();
  s = { ...s, unlockedAll: true, funds: 100_000_000 };
  s = reducer(s, { type: 'place', spec: 'res_estate', x: 5, y: 5 });
  const placed = s.buildings.find((b) => b.spec === 'res_estate');
  assert.ok(placed, 'precondition: res_estate placement succeeded');

  s = {
    ...s,
    population,
    // Strip the auto-registered building monitor so evaluateBuildingMonitors
    // cannot keep bumping capacityTier further during this test's ticks —
    // isolates the population-ceiling logic under test from the (separately
    // tested, BUG-466) auto-scale progression itself: this test wants a FIXED
    // tiered capacity to converge toward, not a moving target.
    buildingMonitors: [],
    buildings: s.buildings.map((b) =>
      b.id === placed.id
        ? { ...b, capacityTier: TIER, builtTick: s.tick - 10_000 } // long-finished construction
        : b
    ),
  };
  return { s, buildingId: placed.id };
}

test('BUG-509: residentsCapacity/onlineResidentsCapacity are tier-aware and strictly exceed the flat base at a non-zero tier', () => {
  assert.equal(FLAT_BASE, 1500, 'precondition: res_estate tier-0 base is 1500');
  assert.ok(TIERED_CAP > FLAT_BASE, 'precondition: tier 2 capacity (1815) exceeds the flat base (1500)');

  const { s } = cityWithUpgradedResEstate(0);

  assert.equal(onlineResidentsCapacity(s), TIERED_CAP, 'the online, tier-aware ceiling reads the upgraded tier');
  assert.equal(residentsCapacity(s), TIERED_CAP, 'the gross tier-aware capacity also reads the upgraded tier');
  assert.notEqual(
    onlineResidentsCapacity(s),
    FLAT_BASE,
    'tier-aware capacity must differ from (and exceed) the flat per-spec base once capacityTier > 0'
  );
});

test('BUG-509: the population growth ceiling inside advance()/tick honours the tiered capacity, not the flat base', () => {
  // Deliberately drive population ABOVE BOTH the flat base (1500) and the
  // tiered capacity (1815), landing in advance()'s deterministic
  // "over-capacity" branch:
  //   population = Math.max(capacity, popBefore - Math.ceil((popBefore - capacity) * 0.1));
  // This branch is a PURE function of `capacity` and `popBefore` only — no
  // jobs/attractiveness/migration confounds — so it isolates exactly which
  // capacity value the ceiling used. Starting at 2000:
  //   flat-sum bug (capacity=1500):   2000 - ceil(500*0.1) = 2000-50 = 1950, then
  //                                   converges DOWN to a floor of 1500.
  //   tier-aware fix (capacity=1815): 2000 - ceil(185*0.1) = 2000-19 = 1981, then
  //                                   converges DOWN to a (higher) floor of 1815.
  const population = 2000;
  assert.ok(population > FLAT_BASE && population > TIERED_CAP, 'precondition: population starts above both candidate ceilings');

  const { s: s0 } = cityWithUpgradedResEstate(population);
  let s = reducer(s0, { type: 'tick' });

  const expectedAfterOneTick = population - Math.ceil((population - TIERED_CAP) * 0.1);
  assert.equal(
    s.population,
    expectedAfterOneTick,
    `after one tick the over-capacity churn must shrink toward the TIERED capacity (${TIERED_CAP}), ` +
      `not the flat base (${FLAT_BASE}) — got ${s.population}`
  );

  // Run enough further ticks for the churn to fully converge, and confirm it
  // settles EXACTLY at the tiered capacity, never overshooting down to the
  // flat base.
  for (let i = 0; i < 200 && s.population > TIERED_CAP; i++) {
    s = reducer(s, { type: 'tick' });
  }
  assert.equal(s.population, TIERED_CAP, 'population converges to the TIERED capacity ceiling, not the flat base');
  assert.ok(TIERED_CAP > FLAT_BASE, 'sanity: the ceiling this test converged to is strictly above the flat base');
});

test('BUG-509: RED proof — reverting the fix (flat sum, no capacityTier read) reproduces the pre-fix over-capacity shrink', () => {
  // BUG-739: mutation now runs against a private webconsole/test/helpers/
  // mutant.mjs shadow copy of BOTH webconsole/src and webconsole/test (this
  // test re-invokes ITSELF as a child, so the shadow needs the test file too)
  // — the real, shared engine.ts is never written to.
  const { failed, output, crashed } = runMutantSelfReinvoke({
    targetRelPath: path.join('sim', 'engine.ts'),
    mutate: (original) => {
      const fixedLine = '  const capacity = onlineResidentsCapacity(s);';
      assert.ok(original.includes(fixedLine), 'precondition: the fixed one-line ceiling is present in engine.ts');
      const buggyIIFE = [
        '  const capacity = (() => {',
        '    let cap = 0;',
        '    for (const b of s.buildings) {',
        '      if (!isOnline(s, b)) continue;',
        '      const sp = SPECS[b.spec];',
        "      if (sp?.kind === 'residential') cap += sp.residents ?? 8;",
        '    }',
        '    return cap;',
        '  })();',
      ].join('\n');
      return original.replace(fixedLine, buggyIIFE);
    },
    testFileAbsPath: fileURLToPath(import.meta.url),
    testNamePattern: 'BUG-509: the population growth ceiling',
  });

  // R2 (BUG-739 round REJECT, 2026-09-05): `failed` alone cannot distinguish
  // "the mutant was detected" from "the child crashed for an unrelated
  // reason" (e.g. a syntax-breaking mutation) — both redden `failed`
  // identically. Require the test runner to have actually REPORTED (crashed
  // === false) AND the output to name the SPECIFIC assertion this exact
  // mutation is expected to trip, not a bare exit-status/generic-word match.
  assert.ok(!crashed, `the re-invoked test must actually RUN (not crash at load time) against the mutant; output:\n${output}`);
  assert.ok(failed, 'the ceiling test must FAIL against the reverted (flat-sum) engine.ts — proves the test can fail');
  assert.match(
    output,
    /over-capacity churn must shrink toward the TIERED capacity/,
    `child test run output must report the SPECIFIC over-capacity-ceiling assertion failing, not just any failure; got:\n${output}`,
  );
});

test('BUG-509: the ceiling change does not disturb funds/conservation (auto-scale charging is independent of the ceiling)', () => {
  // The Building Auto-Scale charge is applied inside evaluateBuildingMonitors,
  // which runs and charges regardless of the population ceiling computed later
  // in advance() — this fix only changes WHERE the ceiling reads capacity from,
  // never anything about money flow. No monitors are registered in this state,
  // so no auto-scale charge should appear purely from the ceiling fix.
  const { s: s0 } = cityWithUpgradedResEstate(1600);
  const fundsBefore = s0.funds;
  const s = reducer(s0, { type: 'tick' });

  assert.ok(Number.isFinite(s.funds) && Number.isFinite(fundsBefore), 'funds remain finite numbers across the tick');
  const autoScaleEntries = s.ledger.filter((l) => /Auto-scaled \d+ building/.test(l.label));
  assert.equal(
    autoScaleEntries.length,
    0,
    'no building auto-scale charge fires from the ceiling fix alone (no monitors registered on this state)'
  );
});
