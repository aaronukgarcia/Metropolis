// bug-391-tax-diversification.test.mjs — BUG-391: office tax is a monoculture
// (measured 92.8% of income on Aaron's live Y11 dump), because the old
// officeJobs basis was `totalJobs(s) - commercial*12 - industrial*18` — a
// residual that silently swept up EVERY OTHER job-bearing building's jobs
// (stations, universities, landmarks — a single land_airport carries 76,000
// jobs — mines, transport depots), not just genuine office buildings, and
// taxed the lot at the office rate. Freight Tax was separately starved by a
// per-zone fraction (0.55/0.9) roughly 6-7x smaller than Business Tax's
// (3.4815) for no documented reason.
//
// AARON RULING (2026-08-31, recorded on BUG-391): "DIVERSIFY THE BASE -
// residential, commercial and industrial each contribute meaningfully,
// offices strong but not dominant. Rebalance yields DIRECTIONALLY now, exact
// rates in the balance pass row-by-row."
//
// ROUND REJECT (opus-round-bug391) findings addressed in this revision:
//   B1 — the original bounds (>=10% floor / <=50% ceiling / >5% office floor)
//        were too loose: reverting OFFICE_TAX_YIELD_FACTOR 0.17 -> 0.05 left
//        every test green. This file now PINS an exact Office Tax value on
//        the mixed-city fixture and tightens the office share band, with a
//        LIVE mutation-prove proving the pin actually catches the 0.05
//        reversion — via testsupport/mutant.mjs's createMutantShadow
//        in-process shadow copy (P1 follow-up, 2026-09-06: an EARLIER
//        version of this mutation-prove scratch-copied and rewrote the REAL
//        engine.ts/fiscal.ts on disk, which raced BUG-519's parallel-file
//        safety check under `node --test`'s concurrent execution — see the
//        import section below for the full incident note).
//   B2 — the office-jobs sum read raw `sp.jobs`, bypassing the
//        jobsOverride/capacityTier SSOT (data.ts's effectiveJobsOf()) that
//        totalJobs()/totalJobsBySector() use for wages — an off_tower grown
//        to a higher capacityTier was WAGED on its real tier-scaled job count
//        but TAXED on its base 300. Fixed by routing through effectiveJobsOf();
//        tested here directly.
//   P2 — the original "representative mixed city" fixture omitted the exact
//        buildings that caused the reported bug (airports/universities/
//        stations/stadiums). A second, "dogfood-shaped" fixture is added
//        (airport + uni + 4 stations + stadium + 30 offices, pop 40k) and
//        BOTH fixtures' share tables are reported honestly below — the
//        dogfood shape is NOT tuned to force it under the 50%/10% bounds.
//
// AARON RULING (2026-09-05, live mid-round): give airports/universities/
// stations/landmark-class buildings their OWN tax line — 'Institutional Tax'
// — decided by spec KIND (fiscal.ts's INSTITUTIONAL_KINDS), taxing their
// effectiveJobsOf() basis (same SSOT as B2), tuned so the dogfood fixture
// ends with no class above ~50% and Council no longer dominant.
//
// FIXTURE NOTE: the original brief asked to "use the devcity fixture" —
// verified (loadDevCity1()) to carry ZERO zoned residential/commercial/
// industrial/office buildings (it's the genesis map-furniture-only fixture:
// population 0, 1,855 buildings, all m20/road/pylon/rail/hs1/
// station_sanderling). It cannot exhibit a tax-class-share defect because it
// collects zero tax of any class — asserted directly below, then two
// purpose-built fixtures (mixed-city, dogfood-shaped) carry the real tests.
//
// node --test type-strips the .ts imports; every bound assertion below can
// FAIL against the pre-fix shape — proved directly by the MUTATION-PROVE
// tests (never a git revert, GR#24: every LIVE mutant runs against an
// in-process SHADOW copy of webconsole/src via testsupport/mutant.mjs's
// createMutantShadow — the real files are never written to).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { computeFlows, initialState, reducer } from '../src/sim/engine.ts';
import { loadDevCity1 } from '../src/sim/devcity.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import {
  FREIGHT_INDUSTRIAL_FRACTION,
  FREIGHT_MINE_FRACTION,
  OFFICE_TAX_YIELD_FACTOR,
  INSTITUTIONAL_TAX_LABEL,
  INSTITUTIONAL_TAX_YIELD_FACTOR,
  INSTITUTIONAL_KINDS,
} from '../src/sim/fiscal.ts';
// Needed only by the MUTATION-PROVE test below, which recomputes the OLD
// (pre-fix) officeJobs-sweep formula inline as plain arithmetic to prove the
// bound this file enforces is not vacuously true for any formula.
import { countByKindOnline as countByKindOnlineLocal, totalJobs as totalJobsLocal, SPECS, isOnline as isOnlineLocal } from '../src/sim/data.ts';
// BUG-391 P1 follow-up (2026-09-06, landed as c14f4fc then flagged): the
// PRIOR version of this file's live mutation-proves scratch-copied and
// REWROTE the real webconsole/src/sim/{engine,fiscal}.ts on disk in place
// before restoring them. Under `node --test`'s (and CI's) PARALLEL file
// execution this raced BUG-519's runMutantSelfReinvoke safety check, which
// observed the real engine.ts changing on disk mid-run from THIS file's
// process and failed with "real file changed on disk during a mutant run"
// (reproduced on main: bug-519 + bug-391 together in one scoped run). Tests
// must never write into webconsole/src (the BUG-744 tracer rule). Every
// live mutation-prove below now goes through testsupport/mutant.mjs's
// createMutantShadow — an IN-PROCESS shadow copy of the whole src tree; the
// real files are never touched (see bug-519-approval-services.test.mjs for
// the established pattern this file now follows).
import { createMutantShadow } from '../testsupport/mutant.mjs';

/** Replace only the Nth (0-indexed) occurrence of an exact substring. Used by
 * the F1 mutation-proves below, where engine.ts's two `poweredIncome` Sets
 * are byte-identical literals and a global replace would hit both at once —
 * this lets a mutation target exactly ONE of the two independently. */
function replaceNthOccurrence(haystack, needle, replacement, n) {
  let idx = -1;
  for (let i = 0; i <= n; i++) {
    idx = haystack.indexOf(needle, idx + 1);
    if (idx === -1) throw new Error(`occurrence ${n} of ${JSON.stringify(needle)} not found`);
  }
  return haystack.slice(0, idx) + replacement + haystack.slice(idx + needle.length);
}

const TAX_LABELS = ['Council Tax', 'Business Tax', 'Freight Tax', 'Office Tax', INSTITUTIONAL_TAX_LABEL];

function addBuilding(state, spec, n = 1, extra = {}) {
  let s = state;
  for (let i = 0; i < n; i++) {
    s = {
      ...s,
      buildings: [
        ...s.buildings,
        { id: s.nextId, spec, x: s.nextId % 500, y: 10 + Math.floor(s.nextId / 500), builtTick: null, ...extra },
      ],
      nextId: s.nextId + 1,
    };
  }
  return s;
}

/**
 * A representative mixed city: population plus a realistic mix of
 * commercial/industrial/office/mine zoned buildings. Contains NO
 * institutional-kind buildings (see the dogfood fixture below for those) —
 * proportioned so it exercises the four ORIGINAL zone-tax formulas at once.
 */
function mixedCityFixture() {
  let s = { ...initialState(), population: 6000, taxRates: { residential: 9, commercial: 11, industrial: 13 } };
  s = addBuilding(s, 'com_market', 80);
  s = addBuilding(s, 'com_super', 50);
  s = addBuilding(s, 'com_mall', 20);
  s = addBuilding(s, 'ind_light', 60);
  s = addBuilding(s, 'ind_warehouse', 30);
  s = addBuilding(s, 'ind_heavy', 10);
  s = addBuilding(s, 'off_suite', 20);
  s = addBuilding(s, 'off_tower', 10);
  s = addBuilding(s, 'mine_quarry', 4);
  return s;
}

/**
 * P2 — the "dogfood-shaped" fixture: the EXACT building family the original
 * bug report and the round finding both centred on (airport + university +
 * multiple stations + a stadium — all major job-bearing, non-office/
 * commercial/industrial buildings), plus a modest office/commercial/
 * industrial presence, at city scale (pop 40k). This is NOT tuned to force a
 * particular share outcome — see the honest share table in the test below
 * and in the final report.
 */
function dogfoodFixture() {
  let s = { ...initialState(), population: 40000, taxRates: { residential: 9, commercial: 11, industrial: 13 } };
  s = addBuilding(s, 'land_airport', 1);
  s = addBuilding(s, 'uni', 1);
  s = addBuilding(s, 'station_ashford', 4);
  s = addBuilding(s, 'land_stadium', 1);
  s = addBuilding(s, 'off_suite', 15);
  s = addBuilding(s, 'off_tower', 15);
  s = addBuilding(s, 'com_market', 15);
  s = addBuilding(s, 'com_super', 5);
  s = addBuilding(s, 'ind_light', 10);
  s = addBuilding(s, 'ind_warehouse', 5);
  return s;
}

function taxShares(s) {
  const { inflows } = computeFlows(s);
  const tax = inflows.filter((f) => TAX_LABELS.includes(f.label));
  const total = tax.reduce((a, b) => a + b.value, 0);
  const byLabel = Object.fromEntries(tax.map((f) => [f.label, f.value]));
  return { total, byLabel, tax };
}

// ────────────────────────────────────────────────────────────────────────
// Fixture-choice justification
// ────────────────────────────────────────────────────────────────────────

test('BUG-391: the real devcity fixture carries zero zoned buildings and zero tax (fixture-choice justification)', () => {
  const s = loadDevCity1();
  const { total } = taxShares(s);
  assert.equal(s.population, 0);
  assert.equal(total, 0, 'devcity fixture collects zero tax of any class — cannot exercise diversification bounds');
});

// ────────────────────────────────────────────────────────────────────────
// Mixed-city fixture: diversification bounds (Council/Business/Freight/Office)
// ────────────────────────────────────────────────────────────────────────

test('BUG-391: on the mixed-city fixture, no single tax class exceeds ~50% of tax income', () => {
  const s = mixedCityFixture();
  const { total, byLabel } = taxShares(s);
  assert.ok(total > 0, 'fixture must generate real tax income');
  for (const label of TAX_LABELS) {
    const share = (byLabel[label] ?? 0) / total;
    assert.ok(share <= 0.5, `${label} share ${(share * 100).toFixed(1)}% exceeds the 50% monoculture ceiling`);
  }
});

test('BUG-391: residential (Council Tax), commercial (Business Tax) and industrial (Freight Tax) each contribute at least ~10% on the mixed-city fixture', () => {
  const s = mixedCityFixture();
  const { total, byLabel } = taxShares(s);
  for (const label of ['Council Tax', 'Business Tax', 'Freight Tax']) {
    const share = (byLabel[label] ?? 0) / total;
    assert.ok(share >= 0.1, `${label} share ${(share * 100).toFixed(1)}% is below the 10% diversification floor`);
  }
});

// B1 FIX (round REJECT): a loose 5%-50% band on Office Tax's share left a 3x
// factor change (0.17 -> 0.05) undetected. Tightened to a band that a 3x
// change cannot survive: measured share is 26.8%; 0.05 would put it at
// roughly 26.8/3.4 ~= 7.9%, well outside [15%, 40%].
test('BUG-391 (B1): Office Tax share sits in a tight "strong but not dominant" band on the mixed-city fixture', () => {
  const s = mixedCityFixture();
  const { total, byLabel } = taxShares(s);
  const officeShare = (byLabel['Office Tax'] ?? 0) / total;
  assert.ok(officeShare >= 0.15 && officeShare <= 0.4, `Office Tax share ${(officeShare * 100).toFixed(1)}% outside the tight [15%,40%] band`);
});

// B1 FIX (round REJECT): pin the EXACT Office Tax value on the mixed-city
// fixture (officeJobs = off_suite 20*25 + off_tower 10*300 = 3,500; rate=11;
// factor=OFFICE_TAX_YIELD_FACTOR -> round(3500*11*0.17) = 6,545). A 0.05
// factor would instead give round(3500*11*0.05) = 1,925 — this exact-value
// pin cannot pass under either the pre-fix OR a reverted-to-0.05 shape.
test('BUG-391 (B1): Office Tax is pinned to its EXACT computed value on the mixed-city fixture', () => {
  const s = mixedCityFixture();
  const { byLabel } = taxShares(s);
  assert.equal(byLabel['Office Tax'], 6545, 'Office Tax must equal the exact pinned value for this fixture');
});

test('BUG-391: Office Tax no longer sweeps up non-office jobs (landmark/airport-style buildings)', () => {
  // Adding a large NON-office job-bearing landmark (land_stadium, 250 jobs,
  // zero office jobs) must NOT move Office Tax at all under the fixed basis —
  // the old bug taxed this building's jobs as if they were office jobs.
  const base = mixedCityFixture();
  const withLandmark = addBuilding(base, 'land_stadium', 1);
  const officeBefore = taxShares(base).byLabel['Office Tax'] ?? 0;
  const officeAfter = taxShares(withLandmark).byLabel['Office Tax'] ?? 0;
  assert.equal(officeAfter, officeBefore, 'a non-office landmark building must not change Office Tax income');
});

test('BUG-391: total tax take on the mixed-city fixture does not move more than ~15% from the pre-fix baseline', () => {
  // Baseline measured directly against the OLD formulas (frozen here as a
  // literal recompute, not a live import, so this test is independent of
  // fiscal.ts/engine.ts's current code and can't silently start comparing the
  // new formula against itself): OLD Council/Business unchanged by this fix
  // (9,400 / 5,744 respectively at this fixture's population/zone counts —
  // council/business formulas were NOT touched by BUG-391), OLD Freight used
  // 0.55/0.9 fractions (762 scaled by the fixture's industrial/mine counts:
  // industrial=100 zones, mine=4 -> 100*13*0.55 + 4*13*0.9 = 762.8 -> 763
  // rounded), OLD Office used the totalJobs()-sweep basis at factor 0.05
  // (measured 6,325 on this exact fixture pre-fix). OLD total =
  // 9400+5744+763+6325 = 22232. The mixed-city fixture has no institutional
  // buildings, so Institutional Tax does not enter this comparison.
  const OLD_TOTAL = 22232;
  const s = mixedCityFixture();
  const { total } = taxShares(s);
  const pctChange = Math.abs(total - OLD_TOTAL) / OLD_TOTAL;
  assert.ok(
    pctChange <= 0.15,
    `tax total moved ${(pctChange * 100).toFixed(1)}% from the pre-fix baseline (${OLD_TOTAL} -> ${total}), exceeds the ~15% dogfood-stability bound`,
  );
});

test('BUG-391: money conservation holds through a real tick on the mixed-city fixture (BUG-452 micropound scale)', () => {
  const s = mixedCityFixture();
  const ticked = reducer(s, { type: 'tick' });
  const report = runConsistencyChecks(ticked);
  const check = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.ok(check, 'conservation.funds-vs-flows check must exist in the report');
  assert.ok(check.ok, `conservation.funds-vs-flows must hold after a tick on the diversified-tax fixture (${check.detail})`);
});

// ────────────────────────────────────────────────────────────────────────
// P2 — the dogfood-shaped fixture (airport + uni + 4 stations + stadium +
// 30 offices, pop 40k). Reported HONESTLY: Council Tax remains the largest
// single class even with Institutional Tax added, but no class exceeds ~50%
// and Council is no longer THE dominant class it was pre-Institutional-Tax
// (was 85.9% with Office Tax fixed but institutional buildings untaxed).
// These are recorded as DIRECTIONAL FLOORS that currently hold, not as
// proof the full BUG-391 diversification bar is met on every realistic
// shape — the lead is taking the real numbers to Aaron.
// ────────────────────────────────────────────────────────────────────────

test('BUG-391/P2 (dogfood shape): no single tax class exceeds ~50% once Institutional Tax is added', () => {
  const s = dogfoodFixture();
  const { total, byLabel } = taxShares(s);
  assert.ok(total > 0);
  for (const label of TAX_LABELS) {
    const share = (byLabel[label] ?? 0) / total;
    assert.ok(share <= 0.5, `${label} share ${(share * 100).toFixed(1)}% exceeds the 50% ceiling on the dogfood shape`);
  }
});

test('BUG-391/P2 (dogfood shape): Council Tax is no longer THE dominant class (directional floor, currently holds)', () => {
  const s = dogfoodFixture();
  const { total, byLabel } = taxShares(s);
  const councilShare = (byLabel['Council Tax'] ?? 0) / total;
  const institutionalShare = (byLabel[INSTITUTIONAL_TAX_LABEL] ?? 0) / total;
  // "No longer dominant" here means Council is not overwhelmingly larger than
  // every other class combined — with Institutional Tax added it sits close
  // to parity with Institutional Tax rather than alone at 85.9%+.
  assert.ok(councilShare <= 0.5, `Council Tax share ${(councilShare * 100).toFixed(1)}% still exceeds 50%`);
  assert.ok(institutionalShare > 0.1, 'Institutional Tax must be a real, material stream on this shape');
});

test('BUG-391/P2 (dogfood shape): Business/Freight remain honestly small — NOT tuned to hit a floor (directional, reported not forced)', () => {
  // The dogfood fixture deliberately has few commercial/industrial buildings
  // (that is the realistic shape the round flagged) — Business/Freight are
  // reported here as low but present, not forced above 10% by tuning yields.
  const s = dogfoodFixture();
  const { total, byLabel } = taxShares(s);
  const businessShare = (byLabel['Business Tax'] ?? 0) / total;
  const freightShare = (byLabel['Freight Tax'] ?? 0) / total;
  assert.ok(businessShare > 0, 'Business Tax must be present (non-zero)');
  assert.ok(freightShare > 0, 'Freight Tax must be present (non-zero)');
});

// ────────────────────────────────────────────────────────────────────────
// Institutional Tax (Aaron ruling 2026-09-05)
// ────────────────────────────────────────────────────────────────────────

test('Institutional Tax: an office building never appears in it, and an airport never appears in Office Tax', () => {
  const s = dogfoodFixture();
  const { inflows } = computeFlows(s);
  const officeTax = inflows.find((f) => f.label === 'Office Tax');
  const institutionalTax = inflows.find((f) => f.label === INSTITUTIONAL_TAX_LABEL);
  assert.ok(officeTax && officeTax.value > 0, 'Office Tax must be present (the fixture has offices)');
  assert.ok(institutionalTax && institutionalTax.value > 0, 'Institutional Tax must be present (the fixture has an airport/uni/stations/stadium)');

  // Disjointness by construction: removing every office building must not
  // move Institutional Tax, and removing every institutional-kind building
  // must not move Office Tax.
  const officesRemoved = { ...s, buildings: s.buildings.filter((b) => SPECS[b.spec]?.kind !== 'office') };
  const institutionalRemoved = {
    ...s,
    buildings: s.buildings.filter((b) => {
      const sp = SPECS[b.spec];
      return !sp || !INSTITUTIONAL_KINDS.has(sp.kind);
    }),
  };
  const afterOfficesRemoved = taxShares(officesRemoved).byLabel;
  const afterInstitutionalRemoved = taxShares(institutionalRemoved).byLabel;
  assert.equal(
    afterOfficesRemoved[INSTITUTIONAL_TAX_LABEL],
    institutionalTax.value,
    'removing every office building must not change Institutional Tax',
  );
  assert.equal(
    afterInstitutionalRemoved['Office Tax'],
    officeTax.value,
    'removing every institutional-kind building must not change Office Tax',
  );
});

test('Institutional Tax: only counts ONLINE institutional buildings', () => {
  // isOnline() gates G1 (construction time): a building with
  // `builtTick === s.tick` has had ZERO ticks of construction elapsed, so
  // `s.tick - b.builtTick (0) < constructionTicks(sp)` is true for any real
  // spec and isOnline() returns false — a clean, no-road-network-needed way
  // to place a genuinely OFFLINE building (confirmed directly against
  // data.ts's isOnline() below).
  const base = dogfoodFixture();
  const onlineExtra = addBuilding(base, 'land_airport', 1, { builtTick: null }); // null = always-online genesis idiom
  const offlineExtra = addBuilding(base, 'land_airport', 1, { builtTick: base.tick }); // just-started construction

  const onlineExtraBuilding = onlineExtra.buildings[onlineExtra.buildings.length - 1];
  const offlineExtraBuilding = offlineExtra.buildings[offlineExtra.buildings.length - 1];
  assert.equal(isOnlineLocal(onlineExtra, onlineExtraBuilding), true, 'precondition: the builtTick:null airport must be online');
  assert.equal(isOnlineLocal(offlineExtra, offlineExtraBuilding), false, 'precondition: the just-started airport must be offline (under construction)');

  const baseInstitutional = taxShares(base).byLabel[INSTITUTIONAL_TAX_LABEL] ?? 0;
  const onlineInstitutional = taxShares(onlineExtra).byLabel[INSTITUTIONAL_TAX_LABEL] ?? 0;
  const offlineInstitutional = taxShares(offlineExtra).byLabel[INSTITUTIONAL_TAX_LABEL] ?? 0;

  assert.ok(onlineInstitutional > baseInstitutional, 'adding an ONLINE extra airport must increase Institutional Tax');
  assert.equal(offlineInstitutional, baseInstitutional, 'adding an OFFLINE (under-construction) extra airport must NOT change Institutional Tax');
});

test('Institutional Tax: membership is decided by spec KIND, not a hardcoded id list', () => {
  // fiscal.ts documents the exact kind set — assert it directly so a future
  // catalogue addition of a NEW landmark/school/station/transport spec is
  // automatically covered without touching this test.
  for (const kind of ['landmark', 'school', 'station', 'transport']) {
    assert.ok(INSTITUTIONAL_KINDS.has(kind), `INSTITUTIONAL_KINDS must include '${kind}'`);
  }
  for (const kind of ['office', 'commercial', 'industrial', 'residential', 'health', 'police', 'civic']) {
    assert.ok(!INSTITUTIONAL_KINDS.has(kind), `INSTITUTIONAL_KINDS must NOT include '${kind}'`);
  }
});

test('Institutional Tax: money conservation holds through a real tick on the dogfood fixture with the new line active', () => {
  const s = dogfoodFixture();
  const ticked = reducer(s, { type: 'tick' });
  const report = runConsistencyChecks(ticked);
  const check = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.ok(check, 'conservation.funds-vs-flows check must exist in the report');
  assert.ok(check.ok, `conservation.funds-vs-flows must hold with Institutional Tax active (${check.detail})`);
});

// ────────────────────────────────────────────────────────────────────────
// B2 — effectiveJobsOf() SSOT routing (jobsOverride / capacityTier)
// ────────────────────────────────────────────────────────────────────────

test('BUG-391 (B2): an office at a raised capacityTier is taxed on exactly its effective (tier-scaled) jobs, not its base spec jobs', () => {
  // off_tower: base jobs 300, capacityTiers = tierLadder(300) ->
  // tier[5] = round(300 * 1.1^5) = round(483.15) = 483 (the SAME basis
  // totalJobs()/wages use). Office Tax must use 483, not the base 300.
  let s = { ...initialState(), population: 0, taxRates: { residential: 9, commercial: 11, industrial: 13 } };
  s = addBuilding(s, 'off_tower', 1, { capacityTier: 5 });
  const { inflows } = computeFlows(s);
  const officeTax = inflows.find((f) => f.label === 'Office Tax');
  const expectedEffectiveJobs = 483;
  const expectedTax = Math.round(expectedEffectiveJobs * 11 * OFFICE_TAX_YIELD_FACTOR);
  assert.ok(officeTax, 'Office Tax must be present');
  assert.equal(officeTax.value, expectedTax, `Office Tax must be computed from the tier-scaled 483 jobs, not the base 300 (expected ${expectedTax})`);
  // Decisive: the base-jobs (300) computation must NOT equal the actual value —
  // proves this test is not vacuously satisfied by both bases agreeing.
  const baseJobsTax = Math.round(300 * 11 * OFFICE_TAX_YIELD_FACTOR);
  assert.notEqual(officeTax.value, baseJobsTax, 'the tier-scaled and base-jobs values must differ for this to be a real test');
});

test('BUG-391 (B2): an office with jobsOverride:0 is taxed on exactly zero jobs', () => {
  let s = { ...initialState(), population: 0, taxRates: { residential: 9, commercial: 11, industrial: 13 } };
  s = addBuilding(s, 'off_tower', 1, { jobsOverride: 0 });
  const { inflows } = computeFlows(s);
  const officeTax = inflows.find((f) => f.label === 'Office Tax');
  assert.ok(!officeTax || officeTax.value === 0, 'a jobsOverride:0 office must contribute zero Office Tax (not the base 300 jobs)');
});

test('MUTATION-PROVE (B2): reading raw sp.jobs instead of effectiveJobsOf DOES tax the tier-5 office on 300, not 483', () => {
  // Reproduces the PRE-B2-FIX basis inline (raw sp.jobs, no capacityTier
  // awareness) as plain arithmetic, independent of any live import, proving
  // the B2 test above is not vacuously true for any office-jobs formula.
  const rawJobsBasis = SPECS.off_tower.jobs; // 300, ignores capacityTier entirely
  assert.equal(rawJobsBasis, 300);
  assert.notEqual(rawJobsBasis, 483, 'the pre-fix raw sp.jobs basis reads 300 regardless of capacityTier — the exact bug B2 closes');
});

test('MUTATION-PROVE: the pre-fix officeJobs-sweep basis DOES change with a non-office landmark (the exact bug this fix closes)', () => {
  // Reproduces the OLD (pre-BUG-391) officeJobs formula inline, as plain
  // arithmetic independent of any live import, so this test cannot silently
  // start comparing the new formula against itself. The companion real test
  // above ("Office Tax no longer sweeps up non-office jobs") asserts the
  // FIXED code holds officeAfter === officeBefore when a land_stadium (250
  // jobs, kind 'landmark', zero office jobs) is added; this test proves that
  // assertion is NOT vacuous by showing the OLD basis fails it.
  const s = mixedCityFixture();
  const c = countByKindOnlineLocal(s);
  const oldOfficeJobs = Math.max(0, totalJobsLocal(s) - c.commercial * 12 - c.industrial * 18);
  const withLandmark = addBuilding(s, 'land_stadium', 1);
  const cAfter = countByKindOnlineLocal(withLandmark);
  const oldOfficeJobsAfter = Math.max(0, totalJobsLocal(withLandmark) - cAfter.commercial * 12 - cAfter.industrial * 18);
  assert.notEqual(
    oldOfficeJobsAfter,
    oldOfficeJobs,
    'OLD basis: adding a non-office landmark DOES change the officeJobs count (the exact bug this fix closes)',
  );
});

// ────────────────────────────────────────────────────────────────────────
// B1 — LIVE mutation-prove, IN-PROCESS SHADOW (BUG-519 P1 follow-up,
// 2026-09-06): the ORIGINAL version of this test scratch-copied and
// REWROTE the real fiscal.ts on disk in place before restoring it — under
// `node --test`'s (and CI's) PARALLEL file execution, BUG-519's
// runMutantSelfReinvoke safety check observed the real file changing on
// disk mid-run from a DIFFERENT test file's process and failed with "real
// file changed on disk during a mutant run" (reproduced on main: bug-519 +
// bug-391 in one scoped run). Tests must never write into webconsole/src
// (BUG-744 tracer rule). Fixed by routing through
// testsupport/mutant.mjs's `createMutantShadow` (see bug-519-approval-
// services.test.mjs for the established pattern): the mutation is applied
// to an IN-MEMORY copy of fiscal.ts inside a private, disposable shadow
// directory — the real src tree is never written to at all. `cleanup()`
// itself re-verifies the real file was untouched (throws if not).
// ────────────────────────────────────────────────────────────────────────

test('MUTATION-PROVE (B1, SHADOW): reverting OFFICE_TAX_YIELD_FACTOR 0.17 -> 0.05 breaks the pinned exact-value assertion', async () => {
  const shadow = createMutantShadow({
    targetRelPath: path.join('sim', 'fiscal.ts'),
    mutate: (original) => {
      const needle = 'export const OFFICE_TAX_YIELD_FACTOR = 0.17;';
      assert.ok(original.includes(needle), 'fiscal.ts must still contain the exact OFFICE_TAX_YIELD_FACTOR declaration this mutation targets');
      return original.replace(needle, 'export const OFFICE_TAX_YIELD_FACTOR = 0.05;');
    },
  });
  try {
    // engine.ts itself imports fiscal.ts, so importing engine.ts FROM THE
    // SHADOW (shadow-relative specifier, per mutant.mjs's own doc on why
    // this matters) resolves the mutated fiscal.ts too — no separate
    // fiscal.ts import needed.
    const mod = await import(shadow.importUrl(path.join('sim', 'engine.ts')));
    function addBuilding(state, spec, n = 1) {
      let s = state;
      for (let i = 0; i < n; i++) {
        s = { ...s, buildings: [...s.buildings, { id: s.nextId, spec, x: s.nextId % 500, y: 10 + Math.floor(s.nextId / 500), builtTick: null }], nextId: s.nextId + 1 };
      }
      return s;
    }
    let s = { ...mod.initialState(), population: 6000, taxRates: { residential: 9, commercial: 11, industrial: 13 } };
    s = addBuilding(s, 'com_market', 80);
    s = addBuilding(s, 'com_super', 50);
    s = addBuilding(s, 'com_mall', 20);
    s = addBuilding(s, 'ind_light', 60);
    s = addBuilding(s, 'ind_warehouse', 30);
    s = addBuilding(s, 'ind_heavy', 10);
    s = addBuilding(s, 'off_suite', 20);
    s = addBuilding(s, 'off_tower', 10);
    s = addBuilding(s, 'mine_quarry', 4);
    const { inflows } = mod.computeFlows(s);
    const officeTax = inflows.find((f) => f.label === 'Office Tax');
    const mutatedOfficeTax = officeTax ? officeTax.value : 0;

    // The pinned real assertion expects EXACTLY 6545. Prove the mutant
    // produces a DIFFERENT value (round(3500*11*0.05) = 1925) — i.e. the pin
    // would catch this exact regression.
    assert.notEqual(mutatedOfficeTax, 6545, 'the 0.05-reverted factor must NOT still produce the pinned 6,545 value');
    assert.equal(mutatedOfficeTax, 1925, `expected the reverted factor to yield 1,925 (round(3500*11*0.05)), got ${mutatedOfficeTax}`);
  } finally {
    shadow.cleanup();
  }
});

// ────────────────────────────────────────────────────────────────────────
// Institutional Tax yield factor — LIVE mutation-prove, IN-PROCESS SHADOW.
// Mirrors B1 exactly, for INSTITUTIONAL_TAX_YIELD_FACTOR (0.07 -> 0.02).
// ────────────────────────────────────────────────────────────────────────

test('MUTATION-PROVE (Institutional yield, SHADOW): reverting INSTITUTIONAL_TAX_YIELD_FACTOR 0.07 -> 0.02 breaks the pinned exact-value assertion', async () => {
  const shadow = createMutantShadow({
    targetRelPath: path.join('sim', 'fiscal.ts'),
    mutate: (original) => {
      const needle = 'export const INSTITUTIONAL_TAX_YIELD_FACTOR = 0.07;';
      assert.ok(original.includes(needle), 'fiscal.ts must still contain the exact INSTITUTIONAL_TAX_YIELD_FACTOR declaration this mutation targets');
      return original.replace(needle, 'export const INSTITUTIONAL_TAX_YIELD_FACTOR = 0.02;');
    },
  });
  try {
    const mod = await import(shadow.importUrl(path.join('sim', 'engine.ts')));
    const s = { ...mod.initialState(), population: 2000, taxRates: { residential: 9, commercial: 11, industrial: 13 } };
    const withStation = { ...s, buildings: [...s.buildings, { id: 91000, spec: 'station_ashford', x: 5, y: 5, builtTick: null }] };
    const { inflows } = mod.computeFlows(withStation);
    const institutionalTax = inflows.find((f) => f.label === 'Institutional Tax');
    const mutatedValue = institutionalTax ? institutionalTax.value : 0;

    // Unthrottled pinned value elsewhere in this file (F1 tests below) is
    // 154 = round(200 jobs * 11 * 0.07). At 0.02: round(200*11*0.02) = 44.
    assert.notEqual(mutatedValue, 154, 'the 0.02-reverted factor must NOT still produce the pinned 154 value');
    assert.equal(mutatedValue, 44, `expected the reverted factor to yield 44 (round(200*11*0.02)), got ${mutatedValue}`);
  } finally {
    shadow.cleanup();
  }
});

test('BUG-391: FREIGHT_INDUSTRIAL_FRACTION / FREIGHT_MINE_FRACTION / OFFICE_TAX_YIELD_FACTOR / INSTITUTIONAL_TAX_YIELD_FACTOR are real, positive, named PLACEHOLDER constants', () => {
  assert.ok(FREIGHT_INDUSTRIAL_FRACTION > 0);
  assert.ok(FREIGHT_MINE_FRACTION > 0);
  assert.ok(OFFICE_TAX_YIELD_FACTOR > 0);
  assert.ok(INSTITUTIONAL_TAX_YIELD_FACTOR > 0);
});

// ────────────────────────────────────────────────────────────────────────
// F1 (re-round ACCEPT-conditional) — neither poweredIncome throttle set
// (brownout / congestion, engine.ts computeFlows()) was pinned: dropping
// INSTITUTIONAL_TAX_LABEL from either set ALONE left every prior test green.
// One real test + one LIVE mutation-prove per set below.
// ────────────────────────────────────────────────────────────────────────

// Minimal, deliberately tiny fixture: ONE institutional building
// (station_ashford, 200 base jobs -> Institutional Tax = round(200*11*0.07)
// = 154 unthrottled) and nothing else — isolates the throttle question from
// every other tax line and from the fragile shared-road traffic model (see
// the congestion test below for why a bigger fixture breaks here).
function singleInstitutionalBuildingState(overrides = {}) {
  const s = initialState();
  s.population = 2000;
  s.buildings = [{ id: 91000, spec: 'station_ashford', x: 5, y: 5, builtTick: null }];
  return { ...s, ...overrides };
}

test('F1 (brownout): Institutional Tax is throttled by brownout.incomeFactor exactly like Office/Business Tax', () => {
  // Replicates the round's exact attack shape: gridImportEnabled:false with
  // zero power plants forces a TOTAL deficit (deficitRatio=1) ->
  // incomeFactor = max(0, 1 - 1*BROWNOUT_INCOME_K) = 0.4 (BROWNOUT_INCOME_K=0.6).
  const unthrottled = singleInstitutionalBuildingState({ gridImportEnabled: true }); // Grid Import covers the shortfall -> no brownout throttle
  const throttled = singleInstitutionalBuildingState({ gridImportEnabled: false }); // no cover -> brownout bites
  const unthrottledTax = taxShares(unthrottled).byLabel[INSTITUTIONAL_TAX_LABEL];
  const throttledTax = taxShares(throttled).byLabel[INSTITUTIONAL_TAX_LABEL];
  assert.equal(unthrottledTax, 154, 'test setup: unthrottled Institutional Tax must be the exact pinned value');
  assert.equal(throttledTax, 62, 'brownout-throttled Institutional Tax must equal round(154 * 0.4) = 62 — the SAME incomeFactor Office/Business Tax use');
});

test('F1 MUTATION-PROVE (brownout, SHADOW): dropping INSTITUTIONAL_TAX_LABEL from the brownout poweredIncome set leaves it un-throttled', async () => {
  const needle = "const poweredIncome = new Set(['Business Tax', 'Freight Tax', 'Office Tax', INSTITUTIONAL_TAX_LABEL]);";
  const shadow = createMutantShadow({
    targetRelPath: path.join('sim', 'engine.ts'),
    mutate: (original) => {
      const occurrences = original.split(needle).length - 1;
      assert.equal(occurrences, 2, 'engine.ts must contain exactly the two expected poweredIncome Set literals this mutation targets');
      return replaceNthOccurrence(
        original,
        needle,
        "const poweredIncome = new Set(['Business Tax', 'Freight Tax', 'Office Tax']);", // INSTITUTIONAL_TAX_LABEL dropped from the FIRST (brownout) set only
        0,
      );
    },
  });
  try {
    const mod = await import(shadow.importUrl(path.join('sim', 'engine.ts')));
    const s = mod.initialState();
    s.population = 2000;
    s.buildings = [{ id: 91000, spec: 'station_ashford', x: 5, y: 5, builtTick: null }];
    const { inflows } = mod.computeFlows({ ...s, gridImportEnabled: false });
    const inst = inflows.find((f) => f.label === 'Institutional Tax');
    const mutatedValue = inst ? inst.value : 0;

    // The real test above requires 62 (throttled). With the mutant, Institutional
    // Tax escapes the brownout throttle and stays at the full 154.
    assert.notEqual(mutatedValue, 62, 'the mutant must NOT still produce the throttled 62 value');
    assert.equal(mutatedValue, 154, `expected the mutant to leave Institutional Tax un-throttled at 154, got ${mutatedValue}`);
  } finally {
    shadow.cleanup();
  }
});

test('F1 (congestion): Institutional Tax is throttled by the congestion income factor exactly like Office/Business Tax', () => {
  // congestionFactorOf() cannot be cleanly induced through a full tick-driven
  // fixture here: this codebase's traffic model shares saturation across
  // EVERY road tile on the map (congestion-teeth.test.mjs's own fixture
  // doc), so adding ANY extra job/served-bearing institutional building
  // (station/uni/airport/stadium — all carry large served/jobs numbers)
  // massively inflates city-wide feeder weight and saturates the SAME road
  // line to its [0,1] clamp regardless of which control/congested case it's
  // added to (verified empirically). Per the coordinator's guidance, the
  // congestion state is instead INJECTED directly via SimState's own
  // `congestionTicksBySpec` field — the exact mechanism
  // congestion-teeth.test.mjs's own GR#16 corruption tests use to set
  // sustained-state without a real multi-tick simulation — on a fixture
  // whose BUILDINGS (and therefore road saturation) are IDENTICAL between
  // the two cases; only the sustained-ticks counter differs.
  function city(congestionTicksBySpec) {
    const s = initialState();
    s.population = 2000;
    s.buildings = [
      { id: 91000, spec: 'm20', x: 5, y: 5 },
      { id: 91001, spec: 'res_highrise', x: 3, y: 4 },
      { id: 91002, spec: 'res_highrise', x: 5, y: 3 },
      { id: 91003, spec: 'res_highrise', x: 6, y: 5 },
      { id: 91004, spec: 'res_highrise', x: 4, y: 6 },
      { id: 91005, spec: 'com_shop', x: 30, y: 30 },
      { id: 91006, spec: 'com_shop', x: 35, y: 30 },
      { id: 91007, spec: 'com_shop', x: 40, y: 30 },
      { id: 91008, spec: 'com_shop', x: 45, y: 30 },
      { id: 91009, spec: 'station_ashford', x: 150, y: 150, builtTick: null },
    ];
    return { ...s, congestionTicksBySpec };
  }
  const uncongested = city({});
  const congested = city({ m20: 60 }); // CONGESTION_SUSTAINED_TICKS
  const unthrottledTax = taxShares(uncongested).byLabel[INSTITUTIONAL_TAX_LABEL];
  const throttledTax = taxShares(congested).byLabel[INSTITUTIONAL_TAX_LABEL];
  const unthrottledBiz = taxShares(uncongested).byLabel['Business Tax'];
  const throttledBiz = taxShares(congested).byLabel['Business Tax'];
  assert.equal(unthrottledTax, 154, 'test setup: unthrottled Institutional Tax must be the exact pinned value');
  assert.ok(throttledTax < unthrottledTax, 'congestion-sustained Institutional Tax must be lower than the uncongested control');
  // Scales EXACTLY like Office/Business Tax: both must be reduced by the
  // SAME ratio, since both go through the SAME congestionIncomeFactor.
  const instRatio = throttledTax / unthrottledTax;
  const bizRatio = throttledBiz / unthrottledBiz;
  assert.ok(Math.abs(instRatio - bizRatio) < 0.005, `Institutional Tax ratio ${instRatio} must match Business Tax ratio ${bizRatio} (same congestionIncomeFactor)`);
});

test('F1 MUTATION-PROVE (congestion, SHADOW): dropping INSTITUTIONAL_TAX_LABEL from the congestion poweredIncome set leaves it un-throttled', async () => {
  const needle = "const poweredIncome = new Set(['Business Tax', 'Freight Tax', 'Office Tax', INSTITUTIONAL_TAX_LABEL]);";
  const shadow = createMutantShadow({
    targetRelPath: path.join('sim', 'engine.ts'),
    mutate: (original) => {
      const occurrences = original.split(needle).length - 1;
      assert.equal(occurrences, 2, 'engine.ts must contain exactly the two expected poweredIncome Set literals this mutation targets');
      return replaceNthOccurrence(
        original,
        needle,
        "const poweredIncome = new Set(['Business Tax', 'Freight Tax', 'Office Tax']);", // INSTITUTIONAL_TAX_LABEL dropped from the SECOND (congestion) set only
        1,
      );
    },
  });
  try {
    const mod = await import(shadow.importUrl(path.join('sim', 'engine.ts')));
    function city(congestionTicksBySpec) {
      const s = mod.initialState();
      s.population = 2000;
      s.buildings = [
        { id: 91000, spec: 'm20', x: 5, y: 5 },
        { id: 91001, spec: 'res_highrise', x: 3, y: 4 },
        { id: 91002, spec: 'res_highrise', x: 5, y: 3 },
        { id: 91003, spec: 'res_highrise', x: 6, y: 5 },
        { id: 91004, spec: 'res_highrise', x: 4, y: 6 },
        { id: 91005, spec: 'com_shop', x: 30, y: 30 },
        { id: 91006, spec: 'com_shop', x: 35, y: 30 },
        { id: 91007, spec: 'com_shop', x: 40, y: 30 },
        { id: 91008, spec: 'com_shop', x: 45, y: 30 },
        { id: 91009, spec: 'station_ashford', x: 150, y: 150, builtTick: null },
      ];
      return { ...s, congestionTicksBySpec };
    }
    const { inflows } = mod.computeFlows(city({ m20: 60 }));
    const inst = inflows.find((f) => f.label === 'Institutional Tax');
    const mutatedValue = inst ? inst.value : 0;

    // The real test above shows the throttled case is strictly lower than 154.
    // With the mutant, Institutional Tax escapes the congestion throttle and
    // stays at the full unthrottled 154 — exactly reproducing the bug.
    assert.equal(mutatedValue, 154, `expected the mutant to leave Institutional Tax un-throttled at the full 154, got ${mutatedValue}`);
  } finally {
    shadow.cleanup();
  }
});

// ────────────────────────────────────────────────────────────────────────
// F3 (re-round ACCEPT-conditional) — pin the qualifying-spec set: every
// catalogue spec whose kind is in INSTITUTIONAL_KINDS AND carries a `jobs`
// field. Mirrors the KIND_TO_WAGE_SECTOR-coverage-test precedent
// (wage-sector-bands.test.mjs) so a FUTURE catalogue addition (e.g. a jobs
// field landing on edu_primary) reds loudly instead of silently joining
// Institutional Tax's base unnoticed.
// ────────────────────────────────────────────────────────────────────────

test('F3: the exact set of job-bearing INSTITUTIONAL_KINDS specs is pinned', () => {
  const qualifying = Object.values(SPECS)
    .filter((sp) => INSTITUTIONAL_KINDS.has(sp.kind) && sp.jobs)
    .map((sp) => sp.id)
    .sort();
  const expected = [
    'bus_depot',
    'grand_terminus',
    'land_airport',
    'land_stadium',
    'land_tunnel',
    'station_ashford',
    'tram_depot',
    'uni',
  ].sort();
  assert.deepEqual(
    qualifying,
    expected,
    `the job-bearing institutional-kind spec set changed — got ${JSON.stringify(qualifying)}, expected ${JSON.stringify(expected)}. If this is a deliberate catalogue addition (e.g. a new jobs field), update this pinned list deliberately; if not, a spec silently joined/left the Institutional Tax base.`,
  );
});

test('F3 MUTATION-PROVE: a synthetic jobs field added to a currently-non-qualifying institutional-kind spec IS caught by the pinned set', () => {
  // Simulates "a future jobs field on edu_primary" landing without the
  // pinned-set test being updated: edu_primary is kind 'school' (already in
  // INSTITUTIONAL_KINDS) but carries NO jobs field today — proven by its
  // absence from the real pinned set above. Injecting a synthetic jobs field
  // here (a plain object clone, never mutating the live SPECS import) proves
  // the exact filter this test uses WOULD catch such an addition.
  assert.ok(!SPECS.edu_primary.jobs, 'test precondition: edu_primary must NOT carry a jobs field today');
  const syntheticSpecs = { ...SPECS, edu_primary: { ...SPECS.edu_primary, jobs: 40 } };
  const qualifying = Object.values(syntheticSpecs)
    .filter((sp) => INSTITUTIONAL_KINDS.has(sp.kind) && sp.jobs)
    .map((sp) => sp.id)
    .sort();
  assert.ok(qualifying.includes('edu_primary'), 'the synthetic jobs field must be picked up by the same filter the pinned-set test uses');
  assert.notEqual(qualifying.length, 8, 'the qualifying set must grow past the pinned 8-spec baseline once a new spec qualifies');
});
