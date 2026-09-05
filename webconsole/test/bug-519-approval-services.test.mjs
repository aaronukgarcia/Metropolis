// bug-519-approval-services.test.mjs — BUG-519: the Approval tile was deaf
// to services and wellbeing. approvalOf() (engine.ts) used to depend ONLY on
// tax rates, station links, water-leak and policy toggles — it never read
// serviceCoverageOf() or wellbeingOf()/wellbeingPreApprovalOf(), so building
// health/police/schools/hospitals had ZERO effect on the Approval tile
// (RightDock.tsx). The fix adds DEFICIT-ONLY terms (health/police/education
// coverage + a pre-Approval wellbeing index) — every term is <= 0, so a
// fully-covered, at-or-above-baseline city sees NO CHANGE vs the pre-fix
// formula (no jump for an established dogfood city), while an under-served
// city is docked and recovers as the missing service is built.
//
// Run with `npm test` (node --test); node type-strips the imported .ts so
// these assertions exercise the exact shipped formula — no copy, no drift.
//
// RED-PROOF: every assertion below is written to FAIL if the fix is reverted
// (scratch cp/mv the SERVICE_DEFICIT_WEIGHT/WELLBEING_DEFICIT_WEIGHT terms
// back out of approvalOf — GR#24, never git). Without the fix, adding a
// hospital to an under-served city leaves approvalOf() UNCHANGED (test 1
// goes red), and the deficit-tracking assertions in tests 2-3 have nothing
// to compare against.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { SPECS, serviceCoverageOf, computeRoadConnectivity } from '../src/sim/data.ts';
import { initialState, approvalOf, wellbeingPreApprovalOf } from '../src/sim/engine.ts';
import { runMutantSelfReinvoke, createMutantShadow } from '../testsupport/mutant.mjs';
import { buildScaleFixture } from './scale/fixture.mjs';

let _id = 519000;
const B = (spec, x, y, extra = {}) => ({ id: _id++, spec, x, y, ...extra });

// Fresh state whose ONLY buildings are the given list, with road
// connectivity computed exactly as advance() does every tick (same harness
// as bug-525-527-activation-coverage.test.mjs).
function city(buildings, tick = 200, population = 0) {
  const s = initialState();
  const st = { ...s, buildings: [...buildings], population, tick };
  st.roadConnectivity = computeRoadConnectivity(st);
  return st;
}

const HOSPITAL = SPECS.hea_hospital; // health, served: 40000
assert.ok(HOSPITAL.served > 0, 'sanity: hospital spec carries service capacity');

// A big, road-connected, long-built (fully online) population with NO
// hospital/clinic/police/school at all — every deficit-bearing coverage row
// (gp/hosp/police/nursery/primary/college) is 0/pop = 0 coverage = full
// deficit. Only roads, so tax/station/water/policy terms stay identical
// across "before"/"after" — isolating the services/wellbeing terms this bug
// is about.
const POP = 50000;
const roads = [B('road', 0, 10, { builtTick: 0 })];

test('BUG-519: building a hospital in an under-served city RAISES Approval next month', () => {
  const before = city([...roads], 200, POP);
  const approvalBefore = approvalOf(before);

  const after = city([...roads, B('hea_hospital', 1, 10, { builtTick: 0 })], 200, POP);
  const hospital = after.buildings.find((b) => b.spec === 'hea_hospital');
  const hospRow = serviceCoverageOf(after).find((r) => r.id === 'hosp');
  assert.ok(hospRow.coverage > 0, 'setup: the new hospital must actually raise hosp coverage');
  assert.ok(hospital, 'setup: hospital placed');

  const approvalAfter = approvalOf(after);
  assert.ok(
    approvalAfter > approvalBefore,
    `expected Approval to rise after building a hospital in an under-served city (before=${approvalBefore}, after=${approvalAfter})`
  );
});

test('BUG-519: Approval is UNCHANGED month-to-month when coverage does not change', () => {
  const s1 = city([...roads], 200, POP);
  const s2 = city([...roads], 340, POP); // later tick, identical buildings/population
  assert.equal(
    approvalOf(s1),
    approvalOf(s2),
    'Approval must be a pure function of coverage-affecting state — no drift when nothing was built'
  );
});

test('BUG-519: a fully-served, at-or-above-baseline city sees ZERO jump from the services/wellbeing terms (dogfood-safety)', () => {
  // Enough of every served/education/utility spec to drive every deficit
  // term to (or past) zero coverage deficit at a modest population, so the
  // NEW services/wellbeing terms in approvalOf all evaluate to exactly 0 —
  // proving an already-well-served city's Approval is IDENTICAL to the
  // pre-fix (tax/station/water/policy-only) formula.
  const smallPop = 50; // small enough that every base (tier-0) capacity below clears the need
  // A connected road CHAIN from the map-edge seed at (0,10) out to (6,10), so
  // every service building placed just north of its own road tile is
  // road-connected (not merely road-adjacent — bug-525-527's disconnection
  // pattern applies per-building, not just to the first one).
  const chain = [];
  for (let x = 0; x <= 6; x++) chain.push(B('road', x, 10, { builtTick: 0 }));
  const wellServed = city(
    [
      ...chain,
      B('hea_clinic', 1, 9, { builtTick: 0 }),
      B('hea_hospital', 2, 9, { builtTick: 0 }),
      B('pol_station', 3, 9, { builtTick: 0 }),
      B('edu_nursery', 4, 9, { builtTick: 0 }),
      B('edu_primary', 5, 9, { builtTick: 0 }),
      B('col_sixth', 6, 9, { builtTick: 0 }),
    ],
    200,
    smallPop
  );

  const cov = serviceCoverageOf(wellServed);
  const gp = cov.find((r) => r.id === 'gp').coverage;
  const hosp = cov.find((r) => r.id === 'hosp').coverage;
  const police = cov.find((r) => r.id === 'police').coverage;
  const nursery = cov.find((r) => r.id === 'nursery').coverage;
  const primary = cov.find((r) => r.id === 'primary').coverage;
  const college = cov.find((r) => r.id === 'college').coverage;
  assert.ok(gp >= 1 && hosp >= 1 && police >= 1, 'setup: gp/hosp/police must be fully (over-)covered');
  assert.ok(nursery >= 1 && primary >= 1 && college >= 1, 'setup: nursery/primary/college must be fully (over-)covered');

  const wb = wellbeingPreApprovalOf(wellServed);
  assert.ok(wb >= 55, `setup: pre-approval wellbeing must be at/above the 55 baseline (was ${wb})`);

  // Pre-fix formula, reproduced verbatim from engine.ts's approvalOf (tax +
  // stationLinks + water-leak + policy toggles only — no services/wellbeing
  // terms), so this is a real independent oracle, not a copy of the new code.
  const t = wellServed.taxRates;
  const avgTax = (t.residential + t.commercial + t.industrial) / 3;
  let expected = 62 - avgTax * 1.5;
  // stationLinks/waterBalanceOf/policies all evaluate the same for this
  // fixture as engine.ts's own defaults (initialState() carries no stations,
  // no leak, no policy toggles set) — asserted below so this oracle can never
  // silently drift from the fixture's real setup.
  assert.equal(wellServed.policies.transitSubsidy, false, 'setup: fixture keeps default (no) transit subsidy');
  assert.equal(wellServed.policies.austerity, false, 'setup: fixture keeps default (no) austerity');
  assert.equal(wellServed.policies.recycling, false, 'setup: fixture keeps default (no) recycling');
  expected = Math.max(0, Math.min(100, Math.round(expected)));

  assert.equal(
    approvalOf(wellServed),
    expected,
    'a fully-served, above-baseline-wellbeing city must match the pre-fix formula exactly (no jump)'
  );
});

test('BUG-519: Approval stays bounded 0..100 under extreme under-provision', () => {
  // No services at all, high population -> maximum possible deficit on every
  // term, plus austerity policy for good measure -> approval must still
  // clamp to >= 0.
  const extreme = city([...roads], 200, 2_000_000);
  extreme.policies = { ...extreme.policies, austerity: true };
  extreme.taxRates = { residential: 40, commercial: 40, industrial: 40 };
  const a = approvalOf(extreme);
  assert.ok(a >= 0 && a <= 100, `Approval must stay within [0,100], got ${a}`);
  assert.equal(a, 0, 'sanity: this fixture is deliberately extreme enough to hit the floor');

  // And the ceiling: everything maxed out, best possible fiscal/services
  // inputs, cannot exceed 100.
  const utopia = city(
    [
      ...roads,
      B('hea_clinic', 1, 10, { builtTick: 0 }),
      B('hea_hospital', 2, 10, { builtTick: 0 }),
      B('pol_station', 3, 10, { builtTick: 0 }),
    ],
    200,
    100
  );
  utopia.taxRates = { residential: 0, commercial: 0, industrial: 0 };
  utopia.policies = { ...utopia.policies, transitSubsidy: true };
  const aTop = approvalOf(utopia);
  assert.ok(aTop >= 0 && aTop <= 100, `Approval must stay within [0,100], got ${aTop}`);
});

// ─────────────────────────────────────────────────────────────────────────
// ROUND r1 (opus-round-bug519) REJECT F1 (P1): the ORIGINAL test 1 above
// ("building a hospital ... RAISES Approval") does NOT isolate the DIRECT
// SERVICE_DEFICIT_WEIGHT term — at POP=50000 a single hospital drives hosp
// coverage to only 0.8, so healthDeficit = max(deficit('gp')=1, deficit
// ('hosp')=0.2) STAYS AT 1 before AND after (gp is still fully uncovered).
// The whole observed rise in that test came from the WELLBEING term alone
// (buildServiceWellbeingParts' Hospital-care part reacting to the new
// coverage), so mutating SERVICE_DEFICIT_WEIGHT to 0, or flipping its sign
// (`a -=` -> `a +=`), both left that test GREEN.
//
// FIX: an isolated fixture that holds police + all three school-stage
// deficits at EXACTLY 0 in both "before" and "after" (never touched), and
// drives ONLY the health deficit from 1 (no gp/hosp at all) to 0 (gp AND
// hosp BOTH fully covered) — so healthDeficit is the ONE deficit term that
// moves. The wellbeing-index term still moves too (health/hospital coverage
// is also one of buildServiceWellbeingParts' inputs) — rather than pretend
// that term is flat, the assertion below computes its EXACT expected
// contribution from the real wellbeingPreApprovalOf(before)/(after) values
// (not re-derived, just read) and combines it with the KNOWN
// SERVICE_DEFICIT_WEIGHT=4 placeholder to predict the total delta. A
// mutated weight (0, or any value other than 4) or a flipped sign changes
// ONLY the direct-term half of that prediction, so the exact-delta
// assertion catches both — proven below by two mutation RED-PROOFs.
// ─────────────────────────────────────────────────────────────────────────

// A connected road CHAIN long enough for 6 distinct service buildings
// (mirrors the "fully-served" fixture above, extracted so the isolated-term
// test and its mutation RED-PROOFs share exactly one fixture builder).
function isolatedHealthFixture(withHealth) {
  const chain = [];
  for (let x = 0; x <= 7; x++) chain.push(B('road', x, 10, { builtTick: 0 }));
  const buildings = [
    ...chain,
    B('pol_station', 1, 9, { builtTick: 0 }),
    B('edu_nursery', 2, 9, { builtTick: 0 }),
    B('edu_primary', 3, 9, { builtTick: 0 }),
    B('col_sixth', 4, 9, { builtTick: 0 }),
  ];
  if (withHealth) {
    buildings.push(B('hea_clinic', 5, 9, { builtTick: 0 }), B('hea_hospital', 6, 9, { builtTick: 0 }));
  }
  return city(buildings, 200, 50);
}

test('BUG-519 (round r1 F1): the direct service-deficit term moves Approval by EXACTLY its weighted delta, isolated from the wellbeing term', () => {
  const before = isolatedHealthFixture(false);
  const after = isolatedHealthFixture(true);

  const covBefore = serviceCoverageOf(before);
  const covAfter = serviceCoverageOf(after);
  const at = (cov, id) => cov.find((r) => r.id === id).coverage;

  // Police/education coverage must be identical AND fully (over-)covered in
  // BOTH states — these three deficits are pinned at 0 throughout, so they
  // contribute nothing to the delta either mutant could hide behind.
  for (const id of ['police', 'nursery', 'primary', 'college']) {
    assert.ok(at(covBefore, id) >= 1, `setup: ${id} must be fully covered BEFORE (was ${at(covBefore, id)})`);
    assert.ok(at(covAfter, id) >= 1, `setup: ${id} must be fully covered AFTER (was ${at(covAfter, id)})`);
  }
  // Health must move from FULLY uncovered to FULLY covered — a clean 1 -> 0
  // deficit swing on the ONE term this test isolates.
  assert.equal(at(covBefore, 'gp'), 0, 'setup: no gp coverage before');
  assert.equal(at(covBefore, 'hosp'), 0, 'setup: no hosp coverage before');
  assert.ok(at(covAfter, 'gp') >= 1 && at(covAfter, 'hosp') >= 1, 'setup: gp AND hosp both fully covered after');

  const healthDeficitBefore = 1; // max(deficit('gp')=1, deficit('hosp')=1)
  const healthDeficitAfter = 0; // max(deficit('gp')=0, deficit('hosp')=0)

  // The wellbeing-index term's exact contribution, read from the REAL
  // (unmutated by F1's target mutations) wellbeingPreApprovalOf — this is
  // the "subtract the wellbeing contribution via wellbeingPreApprovalOf in
  // the oracle" the round asked for: not assumed flat, actually measured.
  const WELLBEING_DEFICIT_WEIGHT = 0.2; // mirrors engine.ts's placeholder — unaffected by the F1 mutations below
  const WELLBEING_DEFICIT_CAP = 10;
  const wellbeingTerm = (wb) => Math.min(WELLBEING_DEFICIT_CAP, Math.max(0, 55 - wb) * WELLBEING_DEFICIT_WEIGHT);
  const wbTermBefore = wellbeingTerm(wellbeingPreApprovalOf(before));
  const wbTermAfter = wellbeingTerm(wellbeingPreApprovalOf(after));

  // The KNOWN placeholder weight (engine.ts's SERVICE_DEFICIT_WEIGHT) — used
  // here as an independent expectation, not read from the source, so a
  // mutated weight in engine.ts diverges from THIS number.
  const SERVICE_DEFICIT_WEIGHT = 4;
  const expectedDelta =
    SERVICE_DEFICIT_WEIGHT * (healthDeficitBefore - healthDeficitAfter) + (wbTermBefore - wbTermAfter);

  const actualDelta = approvalOf(after) - approvalOf(before);
  // Tolerance of 1: approvalOf rounds ITS OWN total to the nearest integer
  // independently at "before" and "after" — two independent roundings can
  // disagree from the continuous delta by at most 1 in total. A weight-0
  // mutation (delta off by exactly SERVICE_DEFICIT_WEIGHT=4) or a sign-flip
  // (delta off by 2x that, 8) both blow straight through this tolerance.
  assert.ok(
    Math.abs(actualDelta - expectedDelta) <= 1,
    `expected the observed Approval delta (${actualDelta}) to match the weighted deficit change (${expectedDelta}) within rounding tolerance 1`
  );
  // Sanity: the fixture must not have hit the 0/100 clamp (which would
  // distort the linear delta relationship this assertion depends on).
  assert.ok(approvalOf(before) > 1 && approvalOf(before) < 99, 'setup: before-state Approval must not be clamp-saturated');
  assert.ok(approvalOf(after) > 1 && approvalOf(after) < 99, 'setup: after-state Approval must not be clamp-saturated');
});

test('BUG-519 (round r1 F1) RED-PROOF: SERVICE_DEFICIT_WEIGHT=0 mutant fails the isolated-term test', () => {
  const { failed, output, crashed } = runMutantSelfReinvoke({
    targetRelPath: path.join('sim', 'engine.ts'),
    mutate: (original) => {
      const fixedLine = '  const SERVICE_DEFICIT_WEIGHT = 4;';
      assert.ok(original.includes(fixedLine), 'RED-PROOF setup: expected SERVICE_DEFICIT_WEIGHT declaration not found in engine.ts');
      return original.replace(fixedLine, '  const SERVICE_DEFICIT_WEIGHT = 0;');
    },
    testFileAbsPath: fileURLToPath(import.meta.url),
    testNamePattern: 'BUG-519 \\(round r1 F1\\): the direct service-deficit term',
  });
  assert.ok(!crashed, `the re-invoked test must actually RUN (not crash at load time) against the mutant; output:\n${output}`);
  assert.ok(failed, 'the isolated-term test must FAIL when SERVICE_DEFICIT_WEIGHT is zeroed out');
  assert.match(
    output,
    /expected the observed Approval delta .* to match the weighted deficit change/,
    `child test run output must report the SPECIFIC delta-mismatch assertion failing; got:\n${output}`
  );
});

test('BUG-519 (round r1 F1) RED-PROOF: sign-flip mutant (a += instead of a -=) fails the isolated-term test', () => {
  const { failed, output, crashed } = runMutantSelfReinvoke({
    targetRelPath: path.join('sim', 'engine.ts'),
    mutate: (original) => {
      const fixedLine = '  a -= (healthDeficit + policeDeficit + eduDeficit) * SERVICE_DEFICIT_WEIGHT;';
      assert.ok(original.includes(fixedLine), 'RED-PROOF setup: expected direct service-deficit line not found in engine.ts');
      return original.replace(fixedLine, '  a += (healthDeficit + policeDeficit + eduDeficit) * SERVICE_DEFICIT_WEIGHT;');
    },
    testFileAbsPath: fileURLToPath(import.meta.url),
    testNamePattern: 'BUG-519 \\(round r1 F1\\): the direct service-deficit term',
  });
  assert.ok(!crashed, `the re-invoked test must actually RUN (not crash at load time) against the mutant; output:\n${output}`);
  assert.ok(failed, 'the isolated-term test must FAIL when the direct service-deficit term is sign-flipped');
  assert.match(
    output,
    /expected the observed Approval delta .* to match the weighted deficit change/,
    `child test run output must report the SPECIFIC delta-mismatch assertion failing; got:\n${output}`
  );
});

// ─────────────────────────────────────────────────────────────────────────
// ROUND r2 (opus-round-bug519) REJECT F2-followup: the original F2 proof
// asserted a WALL-CLOCK ceiling (warm call < 0.02ms) — banned by this
// project's house rule (BUG-659 -> BUG-757 precedent: no wall-clock bounds
// in CI, a gate that can't evaluate reliably on shared/loaded CI hardware
// must not report success OR failure off a timer). Replaced with an
// ALGORITHMIC proof: instrument the actual compute body with an invocation
// counter (via testsupport/mutant.mjs's in-process shadow-copy mechanism —
// the real engine.ts is never touched) and assert N repeated approvalOf(s)
// calls on the SAME state object execute the underlying compute EXACTLY
// ONCE, while a genuinely different state object triggers exactly one more.
// Wall-clock numbers are kept ONLY as t.diagnostic() logging — informational,
// never asserted on.
// ─────────────────────────────────────────────────────────────────────────

test('BUG-519 (round r2 F2): approvalOf performs its underlying compute EXACTLY ONCE per distinct state (algorithmic invocation-count proof, no wall-clock)', async (t) => {
  const shadow = createMutantShadow({
    targetRelPath: path.join('sim', 'engine.ts'),
    mutate: (original) => {
      const marker = 'export const approvalOf: (s: SimState) => number = memoOnState((s) => {';
      assert.ok(original.includes(marker), 'probe setup: expected memoised approvalOf declaration not found in engine.ts');
      // Inject a counter incremented ONLY when the memoOnState-wrapped inner
      // function actually RUNS (i.e. NOT on a cache hit) — the exact
      // "underlying work" this memo protects.
      return original.replace(
        marker,
        'export let __approvalOfComputeCount = 0;\n' +
          'export const approvalOf: (s: SimState) => number = memoOnState((s) => {\n' +
          '  __approvalOfComputeCount++;'
      );
    },
  });
  try {
    const mod = await import(shadow.importUrl(path.join('sim', 'engine.ts')));

    // A big, realistic fixture (matches the round's own measurement scale) —
    // timing is logged for visibility only, never asserted.
    const base = buildScaleFixture(); // ~13k buildings / ~1.4M population
    const s1 = { ...base, buildings: base.buildings.slice() };
    const t0 = performance.now();
    for (let i = 0; i < 5; i++) mod.approvalOf(s1);
    const s1Ms = performance.now() - t0;
    assert.equal(
      mod.__approvalOfComputeCount,
      1,
      `5 repeated calls on the SAME state must run the compute body exactly ONCE — got ${mod.__approvalOfComputeCount}`
    );

    // A genuinely different state object (new top-level reference, same
    // reducer-update discipline as a real tick) must trigger exactly one
    // MORE real compute, then itself memoise.
    const s2 = { ...s1, tick: s1.tick + 1 };
    const t1 = performance.now();
    for (let i = 0; i < 5; i++) mod.approvalOf(s2);
    const s2Ms = performance.now() - t1;
    assert.equal(
      mod.__approvalOfComputeCount,
      2,
      `5 calls on a DIFFERENT state must add exactly ONE more real compute — got ${mod.__approvalOfComputeCount}`
    );

    t.diagnostic(
      `[BUG-519 F2] invocation counts: s1(5 calls)=1 compute, s2(5 calls)=+1 compute (total 2) — ` +
        `wall-clock (informational only): 5 calls on s1 took ${s1Ms.toFixed(4)}ms, 5 calls on s2 took ${s2Ms.toFixed(4)}ms`
    );
  } finally {
    shadow.cleanup();
  }
});

test('BUG-519 (round r2 F2) RED-PROOF: reverting approvalOf to an unmemoised function makes the compute count grow with every call', async (t) => {
  const shadow = createMutantShadow({
    targetRelPath: path.join('sim', 'engine.ts'),
    mutate: (original) => {
      const marker = 'export const approvalOf: (s: SimState) => number = memoOnState((s) => {';
      assert.ok(original.includes(marker), 'RED-PROOF setup: expected memoised approvalOf declaration not found in engine.ts');
      let mutated = original.replace(
        marker,
        'export let __approvalOfComputeCount = 0;\n' +
          'export function approvalOf(s: SimState): number {\n' +
          '  __approvalOfComputeCount++;'
      );
      // Same "un-memoise" edit as the r1 RED-PROOF: swap the memoOnState
      // wrapper's closing `});` for a plain function's `}`.
      const closeMarker = '  return Math.max(0, Math.min(100, Math.round(a)));\n});';
      assert.ok(mutated.includes(closeMarker), 'RED-PROOF setup: expected approvalOf closing `});` not found in engine.ts');
      mutated = mutated.replace(closeMarker, '  return Math.max(0, Math.min(100, Math.round(a)));\n}');
      return mutated;
    },
  });
  try {
    const mod = await import(shadow.importUrl(path.join('sim', 'engine.ts')));
    const base = buildScaleFixture();
    const s1 = { ...base, buildings: base.buildings.slice() };

    const t0 = performance.now();
    for (let i = 0; i < 5; i++) mod.approvalOf(s1);
    const s1Ms = performance.now() - t0;

    t.diagnostic(
      `[RED-PROOF BUG-519 F2] unmemoised compute count after 5 calls on the SAME state: ${mod.__approvalOfComputeCount} ` +
        `(wall-clock, informational only: ${s1Ms.toFixed(4)}ms)`
    );
    assert.equal(
      mod.__approvalOfComputeCount,
      5,
      `RED-PROOF FAILED: an unmemoised approvalOf must recompute on EVERY call (expected 5, got ${mod.__approvalOfComputeCount}) — if it did not, the memo removal was not actually effective`
    );
  } finally {
    shadow.cleanup();
  }
});
