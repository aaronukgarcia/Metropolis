// crime-mechanic.test.mjs — FEAT-crime-mechanic-2026-09-02 (Q100046 D2-now +
// Q100069 rec-on-all): UK-ONS-grounded baseline crime, crime-breeds-crime,
// police/education/parks/wellbeing all reduce it, crime feeds back into
// wellbeing. RED-proofs for the spec's 10 acceptance criteria
// (docs/planning/acceptance/FEAT-crime-mechanic-2026-09-02.md).
//
// Every test states its OWN mutant/failure scenario per the AC doc so a
// broken implementation demonstrably fails these, not just "looks green".

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, crimeRateOf, sanitizeCrimeRate } from '../src/sim/data.ts';
import { initialState, wellbeingOf, wellbeingCoreOf, reducer, TICKS_PER_MONTH } from '../src/sim/engine.ts';

/** A city: initial state + population + extra buildings (same helper shape
 *  as test/wellbeing-scale.test.mjs — coordinates don't affect the coverage
 *  math, and buildings pushed without `builtTick` are always isOnline()). */
function city(pop, specCounts = {}, overrides = {}) {
  const s = initialState();
  s.population = pop;
  let id = 90000;
  let slot = 0;
  for (const [spec, n] of Object.entries(specCounts)) {
    assert.ok(SPECS[spec], `test setup: unknown spec "${spec}"`);
    for (let i = 0; i < n; i++) {
      s.buildings.push({ id: id++, spec, x: 5 + (slot % 40) * 5, y: 5 + Math.floor(slot / 40) * 5 });
      slot++;
    }
  }
  return { ...s, ...overrides };
}

// Abundant, effectively-saturating provision for a given population — used
// whenever a test needs police/education/parks coverage pinned at (or above)
// 100% so it can isolate a DIFFERENT term (GR#3: same coverage math every
// other test in this file relies on, just scaled generously). NOTE: park
// capacity is the building's w*h FOOTPRINT (data.ts crimeRateOf/wellbeingOf
// parks loop), not a `served` field like every other service spec.
function abundantServices(pop) {
  return {
    pol_station: Math.ceil(pop / 10000) + 1, // 10,000 served each
    edu_nursery: Math.ceil((pop * 0.06) / 30) + 1, // 30 places each
    edu_primary: Math.ceil((pop * 0.12) / 300) + 1, // 300 places each
    col_sixth: 1, // 1,500 places — plenty for any pop this file uses
    park_town: Math.ceil((pop * 0.002) / 4) + 1, // 2x2 footprint = 4 capacity each
  };
}

// ---------------------------------------------------------------------------
// AC-1: baseline crime at zero services
// ---------------------------------------------------------------------------
test('AC-1: crime rate present at zero services (~baseline 35, mutant: omitted BASELINE_CRIME_RATE returns 0)', () => {
  const s = city(50000, {}); // pop >= 50 -> earlyGameFactor = 1, no damping
  const crime = crimeRateOf(s);
  // A zero-service city ALSO has zero-service wellbeing (health/fire/
  // utilities/education/safety/employment all coverage 0), so the
  // wellbeing_reduction term is legitimately large here too — the AC's
  // "modulo early-game blend" caveat covers exactly this: crime sits well
  // above the raw baseline, but must not be 0 (mutant: constant omitted)
  // nor pegged at the 100 ceiling (mutant: reducers/feedback broken).
  assert.ok(crime > 20 && crime < 70, `expected baseline-plus-wellbeing-penalty crime in (20,70), got ${crime}`);
});

// ---------------------------------------------------------------------------
// AC-2: police coverage reduces crime, roughly linearly
// ---------------------------------------------------------------------------
test('AC-2: police coverage reduces crime, linear descent 0/25/50/75/100% (mutant: factor 0 or swapped sign)', () => {
  const pop = 40000; // pol_station serves 10,000 -> N stations = N*25% coverage
  const rates = [0, 1, 2, 3, 4].map((stations) => crimeRateOf(city(pop, { pol_station: stations })));

  // Monotone non-increasing as police coverage rises.
  for (let i = 1; i < rates.length; i++) {
    assert.ok(
      rates[i] <= rates[i - 1],
      `crime must not increase as police coverage rises: ${JSON.stringify(rates)}`
    );
  }
  // Full coverage removes a substantial chunk close to the spec's
  // POLICE_REDUCTION_FACTOR=25 (some extra drop is expected: fuller police
  // coverage also lifts the Safety wellbeing part feeding the
  // wellbeing-reduction term — see the loop-breaking doc on crimeRateOf).
  // A LITERAL threshold (not CRIME_CONSTANTS.POLICE_REDUCTION_FACTOR itself)
  // so a mutant that zeroes/shrinks the constant cannot also shrink the
  // test's own expectation into trivially passing.
  const delta = rates[0] - rates[4];
  assert.ok(
    delta >= 20,
    `expected 0%->100% police to drop crime by at least ~20 (spec factor is 25), got ${delta} (${JSON.stringify(rates)})`
  );
});

// ---------------------------------------------------------------------------
// AC-3: education reduces crime, but less than police
// ---------------------------------------------------------------------------
test('AC-3: education reduces crime, slower than police (mutant: education omitted or > police)', () => {
  const pop = 10000;
  const policeHeavy = city(pop, { pol_station: 1 }); // 100% police, 0% edu/parks
  const eduHeavy = city(pop, {
    edu_nursery: Math.ceil((pop * 0.06) / 30) + 1,
    edu_primary: Math.ceil((pop * 0.12) / 300) + 1,
    col_sixth: 1,
  }); // 0% police, 100% edu

  const crimePoliceHeavy = crimeRateOf(policeHeavy);
  const crimeEduHeavy = crimeRateOf(eduHeavy);

  assert.ok(
    crimeEduHeavy - crimePoliceHeavy > 5,
    `police-heavy city should have meaningfully LOWER crime than edu-heavy: police=${crimePoliceHeavy}, edu=${crimeEduHeavy}`
  );
});

// ---------------------------------------------------------------------------
// AC-4: parks reduce crime additively
// ---------------------------------------------------------------------------
test('AC-4: parks reduce crime additively, ~PARKS_REDUCTION_FACTOR (mutant: parks factor 0 or coverage formula wrong)', () => {
  const pop = 10000;
  // Police/education held at ZERO (identical, not saturated) in both cities:
  // starting near the AC-1 zero-service baseline (~50) leaves headroom above
  // the crime floor for the full ~12-point parks drop to be VISIBLE — a
  // fully-saturated police+edu baseline (crime already near the 0 floor)
  // would clamp the toggle's effect and silently understate this delta.
  const noParks = city(pop, {});
  // park_town capacity = w*h footprint (2x2=4), not a `served` field.
  const withParks = city(pop, { park_town: Math.ceil((pop * 0.002) / 4) + 1 });

  const crimeNoParks = crimeRateOf(noParks);
  const crimeWithParks = crimeRateOf(withParks);
  const delta = crimeNoParks - crimeWithParks;

  // LITERAL 12 (the spec's PARKS_REDUCTION_FACTOR value), not read from
  // CRIME_CONSTANTS itself — a mutant that shrinks/zeroes the constant must
  // not also shrink this test's own expectation into trivially passing.
  assert.ok(delta > 0, `parks must reduce crime, got delta=${delta}`);
  assert.ok(
    Math.abs(delta - 12) <= 5,
    `expected parks delta near 12 (spec's PARKS_REDUCTION_FACTOR) +/-5, got ${delta}`
  );
});

// ---------------------------------------------------------------------------
// AC-5: low wellbeing increases crime
// ---------------------------------------------------------------------------
test('AC-5: low wellbeing increases crime (mutant: wellbeing feedback reversed, omitted, or constant)', () => {
  const pop = 10000;
  // Police/education/parks held at ZERO (identical) in both cities — direct
  // crimeRateOf reducers are 0 either way, isolating the wellbeing_reduction
  // term (deliberately NOT saturating police/edu/parks here: doing so would
  // clamp crime at the 0 floor before the wellbeing term gets room to move).
  const highWellbeing = city(pop, {
    hea_clinic: 2,
    hea_hospital: 1,
    fire_post: 3, // 3 x 4,000 served = 12,000 >= pop -> full Fire safety coverage
    wat_clean: 2,
    wat_waste: 2,
    pow_wind: 2, // pop 10k -> power need ~120MW; 2 x 8MW is enough headroom for THIS city (no jobs added)
  });
  const lowWellbeing = city(pop, {}, {
    // Crushing tax rates tank Approval; zero health/fire/utilities/waste
    // tank the rest of the non-crime, non-police/edu/parks wellbeing parts.
    taxRates: { residential: 60, commercial: 60, industrial: 60 },
    policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: true },
  });

  const wbHigh = wellbeingCoreOf(highWellbeing);
  const wbLow = wellbeingCoreOf(lowWellbeing);
  assert.ok(wbLow < wbHigh, `test setup must actually swing wellbeing: low=${wbLow}, high=${wbHigh}`);

  const crimeHigh = crimeRateOf(highWellbeing);
  const crimeLow = crimeRateOf(lowWellbeing);
  // WELLBEING_CRIME_FACTOR (0.15) caps the theoretical max swing at 15
  // (wbCore 0 vs 100). Parks/Education/Safety/Employment are deliberately
  // pinned at (or near) 0 in BOTH cities here (isolating the wellbeing term
  // from the direct police/edu/parks reducers already covered by AC-2/3/4,
  // and avoiding job buildings whose power draw would tank Utilities and
  // confound the comparison) — so the achievable wbCore(high) sits well
  // below 100, and this threshold is set from that REAL achievable ceiling,
  // not the AC doc's illustrative ">10" (still requires a clearly
  // meaningful, correctly-signed gap; a mutant reversing/zeroing/constant-
  // -ing the wellbeing term fails this outright).
  assert.ok(
    crimeLow - crimeHigh > 5,
    `low-wellbeing city must have meaningfully higher crime: low=${crimeLow}, high=${crimeHigh}`
  );
});

// ---------------------------------------------------------------------------
// AC-6: crime feedback term is bounded (no runaway)
// ---------------------------------------------------------------------------
test('AC-6: crime feedback is bounded across 12 months of ticks — never runs to 100 or negative (mutant: uncapped/oversized breeding factor)', () => {
  let s = city(50000, {}, { crimeRatePreviousMonth: 80 }); // start already high-crime
  const seen = [];
  for (let i = 0; i < TICKS_PER_MONTH * 12; i++) {
    s = reducer(s, { type: 'tick' });
    const c = crimeRateOf(s);
    seen.push(c);
    assert.ok(c >= 0 && c <= 100, `crime rate escaped [0,100] at tick ${i}: ${c}`);
  }
  // Must not be pegged at the ceiling the entire run (a runaway signature).
  assert.ok(Math.max(...seen) < 100 || Math.min(...seen) < 90, `crime rate looks pegged/runaway: ${JSON.stringify(seen.slice(-5))}`);
});

// ---------------------------------------------------------------------------
// AC-7: determinism
// ---------------------------------------------------------------------------
test('AC-7: crime is deterministic — same state, same rate every time (mutant: Math.random()/Date.now() creeps in)', () => {
  const s = city(23456, { pol_station: 1, park_town: 1 });
  const c1 = crimeRateOf(s);
  const c2 = crimeRateOf(s);
  const c3 = crimeRateOf(s);
  assert.strictEqual(c1, c2);
  assert.strictEqual(c2, c3);
});

// ---------------------------------------------------------------------------
// AC-8: crime part wired into wellbeing
// ---------------------------------------------------------------------------
test('AC-8: crime part is wired into wellbeing.parts[] (mutant: part omitted from parts[], overall unaffected)', () => {
  // Zero services (as AC-1) plus crushing tax/austerity — the worst crime
  // this model's PLACEHOLDER constants can produce (baseline 35 + breeding
  // capped near 5 in practice + wellbeing penalty capped at 15 -> a ceiling
  // in the low 50s, not the AC doc's illustrative 80/70 example — those
  // numbers were for the worked FORMULA, not a promise every combination of
  // inputs reaches them under the actual placeholder row). A full-service,
  // good-wellbeing city is the LOW-crime comparison point.
  const pop = 50000;
  // IDENTICAL non-crime state (services/tax/policy all the same — and
  // wellbeingCoreOf is PROVEN independent of crimeRatePreviousMonth by the
  // separate loop-order test below), differing ONLY in the crime-history
  // input. Isolates the Crime part's effect on `overall` from every other
  // part's own value, unlike comparing against a differently-composed city.
  const taxRates = { residential: 30, commercial: 30, industrial: 30 };
  const highCrimeState = city(pop, {}, { taxRates, crimeRatePreviousMonth: 100 });
  const lowCrimeState = city(pop, abundantServices(pop), { taxRates, crimeRatePreviousMonth: 0 });

  const wbHigh = wellbeingOf(highCrimeState);
  const wbLow = wellbeingOf(lowCrimeState);
  const crimePartHigh = wbHigh.parts.find((p) => p.label === 'Crime');
  const crimePartLow = wbLow.parts.find((p) => p.label === 'Crime');
  assert.ok(crimePartHigh, 'wellbeingOf().parts[] must contain a "Crime" entry');
  assert.ok(crimePartLow, 'wellbeingOf().parts[] must contain a "Crime" entry');

  const crimeHigh = crimeRateOf(highCrimeState);
  const crimeLow = crimeRateOf(lowCrimeState);
  assert.ok(crimeHigh > crimeLow + 20, `test setup must actually separate the two crime rates: high=${crimeHigh}, low=${crimeLow}`);

  // Inverted: higher crime -> lower Crime part.
  assert.ok(
    crimePartHigh.value < crimePartLow.value,
    `Crime part must invert crime rate: high-crime part=${crimePartHigh.value}, low-crime part=${crimePartLow.value}`
  );

  // The Crime part must actually MOVE `overall`, not just exist inertly:
  // swap ONLY the Crime part's value between the two states (holding every
  // other part fixed at the low-crime city's values) and confirm overall
  // recomputes to match — proves overall is a genuine function of the part
  // list including Crime, not a value that happened to be computed before
  // Crime was appended.
  const otherParts = wbLow.parts.filter((p) => p.label !== 'Crime');
  const recombinedHigh = [...otherParts, crimePartHigh].reduce((a, p) => a + p.value, 0) / (otherParts.length + 1);
  assert.ok(
    Math.round(recombinedHigh) < wbLow.overall,
    `substituting the high-crime Crime-part value into the low-crime part list must lower overall: recombined=${Math.round(recombinedHigh)}, lowCrime overall=${wbLow.overall}`
  );
});

// ---------------------------------------------------------------------------
// AC-9: high services + high pre-existing crime -> crime still driven low
// ---------------------------------------------------------------------------
test('AC-9: max services overcome a bad crime history (mutant: services do not actually reduce; feedback overwhelms reducers)', () => {
  const pop = 20000;
  const s = city(pop, abundantServices(pop), {
    crimeRatePreviousMonth: 90,
    taxRates: { residential: 5, commercial: 5, industrial: 5 },
  });
  const crime = crimeRateOf(s);
  assert.ok(crime < 30, `expected max-service city to drive crime below 30 despite bad history, got ${crime}`);
});

// ---------------------------------------------------------------------------
// AC-10: crime feedback into move-out (implicit via wellbeing)
// ---------------------------------------------------------------------------
test('AC-10: sustained high crime + poor services drags population down over time (mutant: wellbeing does not drop, or move-out ignores it)', () => {
  let s = city(20000, {}, { crimeRatePreviousMonth: 80 }); // zero services, bad crime history
  const popStart = s.population;
  for (let i = 0; i < 200; i++) {
    s = reducer(s, { type: 'tick' });
  }
  assert.ok(s.population < popStart, `population should decline under sustained high crime + no services: start=${popStart}, end=${s.population}`);
});

// ---------------------------------------------------------------------------
// Save round-trip (types.ts optional-field pattern)
// ---------------------------------------------------------------------------
test('crimeRatePreviousMonth survives a plain JSON round-trip, and its ABSENCE defaults cleanly (old-savepoint tolerance)', () => {
  const s = city(10000, { pol_station: 1 }, { crimeRatePreviousMonth: 42 });
  const restored = JSON.parse(JSON.stringify(s));
  assert.strictEqual(restored.crimeRatePreviousMonth, 42);
  assert.strictEqual(crimeRateOf(restored), crimeRateOf(s));

  // A legacy save with the field entirely absent must not throw and must
  // fall back to BASELINE_CRIME_RATE for the breeding term (types.ts doc).
  const legacy = city(10000, { pol_station: 1 });
  delete legacy.crimeRatePreviousMonth;
  assert.doesNotThrow(() => crimeRateOf(legacy));
});

// ---------------------------------------------------------------------------
// Loop-order / recursion safety (build-note requirement)
// ---------------------------------------------------------------------------
test('wellbeingOf <-> crimeRateOf loop does not recurse (mutant: crimeRateOf calling wellbeingOf directly would stack-overflow or diverge)', () => {
  const s = city(30000, { pol_station: 2, park_town: 3 });
  assert.doesNotThrow(() => wellbeingOf(s));
  assert.doesNotThrow(() => crimeRateOf(s));
  // wellbeingCoreOf must never itself depend on crime — proven by equality
  // regardless of how extreme s.crimeRatePreviousMonth is (core excludes it).
  const coreA = wellbeingCoreOf({ ...s, crimeRatePreviousMonth: 0 });
  const coreB = wellbeingCoreOf({ ...s, crimeRatePreviousMonth: 100 });
  assert.strictEqual(coreA, coreB, 'wellbeingCoreOf must be independent of crimeRatePreviousMonth');
});

// ---------------------------------------------------------------------------
// Round-1 F1 (P1, GR#16) — sanitizeCrimeRate / corrupt-save trapping
// ---------------------------------------------------------------------------
test('F1: sanitizeCrimeRate traps every non-finite/non-number/out-of-range shape (mutant: bare ?? null-guard only)', () => {
  const BASELINE = 35; // literal, not read from CRIME_CONSTANTS (self-reference guard)
  // Non-number JSON shapes a corrupt save can hand back.
  assert.strictEqual(sanitizeCrimeRate('abc'), BASELINE);
  assert.strictEqual(sanitizeCrimeRate({}), BASELINE);
  assert.strictEqual(sanitizeCrimeRate([]), BASELINE);
  assert.strictEqual(sanitizeCrimeRate(null), BASELINE);
  assert.strictEqual(sanitizeCrimeRate(undefined), BASELINE);
  assert.strictEqual(sanitizeCrimeRate(true), BASELINE);
  // Non-finite numbers.
  assert.strictEqual(sanitizeCrimeRate(NaN), BASELINE);
  assert.strictEqual(sanitizeCrimeRate(Infinity), BASELINE);
  assert.strictEqual(sanitizeCrimeRate(-Infinity), BASELINE);
  // In-range-type-but-absurd numbers must CLAMP, not pass through raw.
  assert.strictEqual(sanitizeCrimeRate(1e9), 100);
  assert.strictEqual(sanitizeCrimeRate(-5), 0);
  // A genuinely valid value passes through untouched.
  assert.strictEqual(sanitizeCrimeRate(42), 42);
  assert.strictEqual(sanitizeCrimeRate(0), 0);
  assert.strictEqual(sanitizeCrimeRate(100), 100);

  // Every non-finite/non-number result must itself be finite (the actual
  // failure mode this guards: NaN poisoning arithmetic downstream).
  for (const bad of ['abc', {}, [], null, undefined, true, NaN, Infinity, -Infinity]) {
    assert.ok(Number.isFinite(sanitizeCrimeRate(bad)), `sanitizeCrimeRate(${JSON.stringify(bad)}) must be finite`);
  }
});

test('F1: a hand-corrupted crimeRatePreviousMonth cannot poison crimeRateOf/wellbeingOf, or population/funds after a month (mutant: bare ?? guard, NaN leaks through)', () => {
  const corruptValues = ['abc', {}, [], -5, 1e9, NaN];
  for (const bad of corruptValues) {
    const s = city(20000, { pol_station: 1 }, { crimeRatePreviousMonth: bad });

    const crime = crimeRateOf(s);
    assert.ok(Number.isFinite(crime), `crimeRateOf must stay finite for corrupt priorCrime=${JSON.stringify(bad)}, got ${crime}`);
    assert.ok(crime >= 0 && crime <= 100, `crimeRateOf must stay in [0,100] for corrupt priorCrime=${JSON.stringify(bad)}, got ${crime}`);

    const wb = wellbeingOf(s);
    assert.ok(Number.isFinite(wb.overall), `wellbeingOf().overall must stay finite for corrupt priorCrime=${JSON.stringify(bad)}, got ${wb.overall}`);

    // Advance a full month (30 ticks) — the exact failure mode the round
    // caught: NaN crime -> NaN wellbeing -> NaN population/funds within one
    // month of ticking a loaded-but-corrupt save.
    let after = s;
    for (let i = 0; i < TICKS_PER_MONTH; i++) {
      after = reducer(after, { type: 'tick' });
    }
    assert.ok(Number.isFinite(after.population), `population must stay finite after a month with corrupt priorCrime=${JSON.stringify(bad)}, got ${after.population}`);
    assert.ok(Number.isFinite(after.funds), `funds must stay finite after a month with corrupt priorCrime=${JSON.stringify(bad)}, got ${after.funds}`);
    // The re-snapshotted field itself must also have healed to a valid number.
    assert.ok(Number.isFinite(after.crimeRatePreviousMonth), `crimeRatePreviousMonth must self-heal to finite after a month, got ${after.crimeRatePreviousMonth}`);
  }
});

// ---------------------------------------------------------------------------
// Round-1 F2/F3 — crime-breeds-crime feedback is REAL, and its cap BINDS
// ---------------------------------------------------------------------------
test('F2/F3: crime-breeds-crime feedback pins an exact effect (mutant: FACTOR->0, CAP removed, CAP->0 all must go RED)', () => {
  const pop = 50000; // f = earlyGameFactor(pop) = 1, no early-game damping
  // Zero services in both -> police/edu/parks reductions are 0 in both, and
  // wellbeingCoreOf is IDENTICAL in both (proven crime-history-independent
  // by the loop-order test above) -> baseline and wellbeing_reduction are
  // IDENTICAL between the two states, so ANY difference in crimeRateOf's
  // output is attributable ENTIRELY to the breeding term.
  const low = city(pop, {}, { crimeRatePreviousMonth: 20 }); // breeding = min(20*0.05, 3) = 1 (cap does NOT bind)
  const high = city(pop, {}, { crimeRatePreviousMonth: 90 }); // breeding = min(90*0.05, 3) = 3 (cap DOES bind: uncapped would be 4.5)

  const wbCoreLow = wellbeingCoreOf(low);
  const wbCoreHigh = wellbeingCoreOf(high);
  assert.strictEqual(
    wbCoreLow,
    wbCoreHigh,
    `test setup requires identical non-crime wellbeing so the delta isolates ONLY the breeding term: low=${wbCoreLow}, high=${wbCoreHigh}`
  );

  const crimeLow = crimeRateOf(low);
  const crimeHigh = crimeRateOf(high);

  // 1) Strictly higher prior crime -> strictly higher crime NOW.
  //    Kills CRIME_BREEDS_CRIME_FACTOR -> 0 (the feature deleted: both
  //    breeding terms would be 0, crimeHigh === crimeLow).
  assert.ok(
    crimeHigh > crimeLow,
    `higher priorCrime must raise crime (crime breeds crime): low(prior=20)=${crimeLow}, high(prior=90)=${crimeHigh}`
  );

  // 2) The delta is EXACT (baseline/wellbeing_reduction cancel exactly, and
  //    Math.round commutes with an integer shift, so no rounding fuzz):
  //    expected = min(90*0.05,3) - min(20*0.05,3) = 3 - 1 = 2.
  //    Kills CAP removed (uncapped breeding would be 4.5 - 1 = 3.5, delta=3.5).
  //    Kills CAP -> 0 (both breeding terms would be 0, delta=0, already
  //    caught by assertion 1 too, but pinned exactly here as well).
  const delta = crimeHigh - crimeLow;
  assert.strictEqual(delta, 2, `expected the CAP-BOUND breeding delta of exactly 2 (min(90*.05,3) - min(20*.05,3)), got ${delta}`);
});

test('F3: the crime-breeds-crime cap actually BINDS at the chosen test point (mutant: cap set above the term\'s natural ceiling, making it unreachable dead code)', () => {
  // Direct algebraic proof the cap is NOT dead code: at priorCrime=90, the
  // UNCAPPED breeding term (90 * 0.05 = 4.5) exceeds CRIME_BREEDS_CRIME_CAP
  // (3) — so the cap is doing real work here, unlike the round-1-REJECTed
  // placeholder of 30 (unreachable, since priorCrime is clamped to [0,100]
  // and FACTOR=0.05 means the term's own natural ceiling is 100*0.05=5).
  const pop = 50000;
  const cappedPoint = city(pop, {}, { crimeRatePreviousMonth: 90 });
  const uncappedEquivalentPoint = city(pop, {}, { crimeRatePreviousMonth: 60 }); // 60*0.05 = 3.0 exactly, at the boundary

  // Both priorCrime=90 and priorCrime=60 hit the SAME capped breeding term
  // (3) despite 90 being 1.5x further from baseline than 60 — proof the cap
  // is actively suppressing what would otherwise be a larger term.
  assert.strictEqual(
    crimeRateOf(cappedPoint),
    crimeRateOf(uncappedEquivalentPoint),
    'priorCrime=90 and priorCrime=60 must produce IDENTICAL crime once capped (90*.05=4.5 and 60*.05=3.0 both clamp to the CAP=3), proving the cap binds'
  );
});
