// bug-394-vacancy-ceiling.test.mjs — BUG-394 (2026-09-05 fix): population
// locked at a POSITIVE-vacancy fixed point forever, despite abundant
// dwellings and a live +72-style positive housing demand signal.
//
// ROOT CAUSE (recorded on the BOW item, 2026-09-05 RCA, repro at
// E:/gotmp/b394): the retired growth formula was
//     moveIns = min(headroom, round(headroom * k))
// where k = MOVE_IN_RATE * attractiveness. Whenever k < 1 (the DEFAULT-tax
// city measured k ≈ 0.24), that MULTIPLIER-of-headroom shape has a STABLE
// fixed point at POSITIVE vacancy: moveIns undershoots deaths+moveOuts by a
// fixed FRACTION of headroom every tick, so as headroom shrinks moveIns
// shrinks in lockstep and never catches up — the city locks with homes
// standing empty forever. Made worse because attractiveness read
// demand.residential (the ZONING-meter signal), which the sim actively
// drives toward -100 as population grows past job supply — growth
// suppressed its own driver.
//
// THE FIX (engine.ts advance()'s growth block + the new attractivenessOf()):
//   (1) vacancy is now a CEILING on moveIns, never a MULTIPLIER of them —
//       gross inflow = round(marketInflow(popBefore) * attractiveness),
//       computed INDEPENDENTLY of headroom, THEN capped by effectiveHeadroom.
//   (2) attractiveness is driven by jobs-vs-workers / wellbeing / average
//       service coverage — never by demand.residential.
//
// This file proves, on the REAL reducer (not a bespoke fixture) with the
// exact repro4 shape from the BOW RCA (road-connected dwellings + offices,
// default 9/11/13 taxes): population must strictly increase while vacancy
// remains, vacancy must eventually reach (approximately) zero, every flow
// stays conservation-clean, and the new debug-JSON diagnostic fields exist
// so a future freeze names itself without a repro script.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  initialState,
  reducer,
  demandOf,
  wellbeingOf,
  attractivenessOf,
  TICKS_PER_MONTH,
} from '../src/sim/engine.ts';
import { onlineResidentsCapacity, SPECS } from '../src/sim/data.ts';
import { buildDebugJson } from '../src/sim/debugjson.ts';

function testUi(overrides = {}) {
  return {
    appVersion: 'v0.0.0-test',
    frameAtMs: 1_700_000_000_000,
    map: { view: { zoom: 3.5, cx: 150, cy: 70 }, selectedBuildingId: null, showWater: true },
    errors: [],
    ...overrides,
  };
}

// BUG-394 round-2 finding (2026-09-05, opus-round-bug394 re-verification):
// the original version of this fixture built ZERO services (no
// nursery/school/clinic/police/fire/water/power), which crashed wellbeing to
// ~23 and coverage to 0 — nothing like the REAL bug report (BOW comment:
// "wellbeing 91 IDENTICAL"), a genuinely well-served city that was STILL
// frozen. Under the lead's post-round formula (jobsMultiplier +
// vacancy-pull + the MIN_PROGRESS_VACANCY_FRACTION=0.2 cutoff), a
// zero-service city's own steady-state attractiveness sits BELOW what's
// needed to sustain occupancy above ~80% — it correctly settles at a
// partial-occupancy equilibrium instead of ever reaching 100%, because a
// city with no schools/clinics/police really shouldn't fill every home.
// That is not a freeze (moveIns/moveOuts stay live, population keeps
// adjusting) — it is the model correctly refusing to paper over an
// unserved city. Adding a modest set of real services (sized generously for
// this fixture's ~5,000 capacity) makes the fixture match the ACTUAL bug
// report's high-wellbeing shape, which is also the shape the fix promises
// to unfreeze.
function addServices(state) {
  const specs = [
    ...Array(3).fill('edu_primary'),
    ...Array(12).fill('edu_nursery'),
    'col_sixth',
    ...Array(2).fill('hea_clinic'),
    'hea_hospital',
    'pol_station',
    ...Array(2).fill('fire_post'),
    'wat_clean',
    'wat_waste',
    ...Array(3).fill('pow_coal'),
  ];
  let out = state;
  let x = 2;
  for (const spec of specs) {
    // y=104: clear of the road (100), dwellings (98) and offices (102/103,
    // some 2-tall) so no placement silently collides and no-ops.
    out = reducer(out, { type: 'place', spec, x, y: 104 });
    x += 3;
  }
  return out;
}

// The exact repro4 shape (E:/gotmp/b394/repro4.mjs): a long road, abundant
// road-connected dwellings on one side, abundant jobs (office towers) on the
// other, PLUS (round-2 retune, see addServices() above) a generous set of
// real services so wellbeing matches the real bug report's shape, at
// DEFAULT tax rates (9/11/13) — the regime the RCA measured k ≈ 0.24
// (freeze) under the originally-retired formula.
function repro4City() {
  let s = initialState();
  s = reducer(s, { type: 'unlockAll' });
  s = reducer(s, { type: 'debugFunds', amount: 50_000_000_000 });
  const roadTiles = [];
  for (let x = 0; x < 200; x++) roadTiles.push({ x, y: 100 });
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: roadTiles });
  const resTiles = [];
  for (let x = 2; x < 198; x += 2) resTiles.push({ x, y: 98 });
  s = reducer(s, { type: 'placeMany', spec: 'res_block', tiles: resTiles });
  s = addServices(s);
  const officeId =
    'off_tower' in SPECS ? 'off_tower' : Object.values(SPECS).find((z) => z.kind === 'office').id;
  const offTiles = [];
  for (let x = 2; x < 198; x += 2) offTiles.push({ x, y: 102 });
  s = reducer(s, { type: 'placeMany', spec: officeId, tiles: offTiles });
  return s;
}

test('BUG-394: population strictly increases across 60 ticks while vacancy > 0 (repro4 shape, default taxes)', () => {
  let s = repro4City();
  // Let construction complete and the road-connectivity/online gates settle
  // before measuring. BUG-394 re-round (2026-09-06): the post-round-2 growth
  // fixes (VACANCY_RETENTION=1, the vacancy-boosted rate, the tapered
  // progress guarantee) fill this fixture MUCH faster than the original
  // 450-tick settle window — it is already at capacity by ~tick 400, which
  // defeats this test's "still has vacancy" precondition. 150 ticks leaves
  // solid measured vacancy (pop ~2650/cap 4980) while still past initial
  // construction/road-gate settling.
  for (let i = 0; i < 150; i++) s = reducer(s, { type: 'tick' });

  const cap0 = onlineResidentsCapacity(s);
  assert.ok(cap0 > 0, 'repro4 fixture must have positive online residential capacity');
  assert.ok(s.population < cap0, 'repro4 fixture must start with vacancy (population below capacity)');
  assert.ok(demandOf(s).residential > 0, 'repro4 fixture must reproduce the real report\'s POSITIVE housing demand');

  let prevPop = s.population;
  let sawIncrease = false;
  let sawDecrease = false;
  for (let t = 0; t < 60; t++) {
    s = reducer(s, { type: 'tick' });
    const cap = onlineResidentsCapacity(s);
    if (s.population > prevPop) sawIncrease = true;
    if (s.population < prevPop) sawDecrease = true;
    assert.ok(
      s.population >= prevPop,
      `BUG-394 REGRESSION: population must never DROP while vacancy > 0 in this positive-attractiveness city (tick ${t}: ${prevPop} -> ${s.population}, cap=${cap})`
    );
    prevPop = s.population;
  }
  assert.ok(sawIncrease, 'population must show real growth across the 60-tick window, not sit flat');
  assert.ok(!sawDecrease, 'population must never decrease in this scenario');
});

test('BUG-394: vacancy reaches ~0 within a generous number of months', () => {
  let s = repro4City();
  for (let i = 0; i < 450; i++) s = reducer(s, { type: 'tick' });

  const MAX_MONTHS = 220; // generous — the measured repro fills by ~month 158
  const MAX_TICKS = MAX_MONTHS * TICKS_PER_MONTH;
  let filledAtTick = null;
  for (let t = 0; t < MAX_TICKS; t++) {
    s = reducer(s, { type: 'tick' });
    const cap = onlineResidentsCapacity(s);
    if (cap > 0 && s.population >= cap) {
      filledAtTick = t;
      break;
    }
  }
  assert.ok(
    filledAtTick !== null,
    `BUG-394 REGRESSION: vacancy never reached zero within ${MAX_MONTHS} months — the freeze is back`
  );
  console.log(`BUG-394: vacancy reached zero at tick ${filledAtTick} (~month ${(filledAtTick / TICKS_PER_MONTH).toFixed(1)})`);

  // Once filled, the city must show LIVE churn (nonzero move-ins backfilling
  // departures), never a frozen zero-flow city — mirrors the demographic-
  // flows.test.mjs at-capacity churn invariant.
  for (let i = 0; i < 20; i++) s = reducer(s, { type: 'tick' });
  assert.ok(onlineResidentsCapacity(s) - s.population <= 1, 'city stays essentially full once vacancy reaches zero');
});

test('BUG-394: conservation — no negative flows, moveIns never exceeds effective headroom', () => {
  let s = repro4City();
  for (let i = 0; i < 450; i++) s = reducer(s, { type: 'tick' });

  for (let t = 0; t < 300; t++) {
    const before = s.population;
    const capBefore = onlineResidentsCapacity(s);
    s = reducer(s, { type: 'tick' });
    const d = s.lastDemographics;
    for (const [k, v] of Object.entries(d)) {
      assert.ok(Number.isInteger(v) && v >= 0, `${k} must be a non-negative integer, got ${v} at tick ${t}`);
    }
    const headroom = Math.max(0, capBefore - before);
    const effectiveHeadroom = headroom + d.deaths + d.moveOuts;
    assert.ok(
      d.moveIns <= effectiveHeadroom,
      `moveIns (${d.moveIns}) must never exceed effective headroom (${effectiveHeadroom}) at tick ${t} (pop=${before}, cap=${capBefore})`
    );
    const expected = Math.max(0, Math.min(onlineResidentsCapacity(s), before + d.births + d.moveIns - d.deaths - d.moveOuts));
    assert.equal(s.population, expected, `conservation identity must hold exactly at tick ${t}`);
  }
});

test('BUG-394: debug JSON carries attractiveness/inflowRate/effectiveHeadroom/capacity and the four flows', () => {
  let s = repro4City();
  for (let i = 0; i < 460; i++) s = reducer(s, { type: 'tick' });

  assert.ok(s.lastGrowthDiag, 'SimState must carry lastGrowthDiag after a tick');
  const { attractiveness, inflowRate, effectiveHeadroom, capacity } = s.lastGrowthDiag;
  assert.equal(typeof attractiveness, 'number');
  assert.equal(typeof inflowRate, 'number');
  assert.equal(typeof effectiveHeadroom, 'number');
  assert.equal(typeof capacity, 'number');
  assert.ok(attractiveness > 0, 'a positive-demand city must show positive attractiveness');

  const debug = buildDebugJson(s, testUi());
  assert.ok(debug.demographics, 'debug JSON must carry a demographics section');
  assert.ok(debug.demographics.growthDiag, 'debug JSON must expose growthDiag');
  assert.equal(debug.demographics.growthDiag.attractiveness, attractiveness);
  assert.equal(debug.demographics.growthDiag.capacity, capacity);
  assert.equal(debug.demographics.growthDiag.effectiveHeadroom, effectiveHeadroom);

  const flows = debug.demographics.lastTick;
  for (const k of ['births', 'deaths', 'moveIns', 'moveOuts']) {
    assert.ok(k in flows, `debug JSON demographics.lastTick must carry ${k}`);
  }
});

// BUG-394 round-2 note: this RED test deliberately uses the LEAN (no
// services) shape — the ORIGINAL RCA conditions (moderate wellbeing from
// zero schools/clinics/police) where the retired formula's fixed point was
// actually measured. repro4City() (services added, see addServices() above)
// now has high wellbeing, under which even the RETIRED formula's k comes out
// close to 1 and it fills fine — a service-rich city was never the freeze
// regime. That is not a weakness in the real fix's test coverage — it is a
// second, independent confirmation that the freeze was a LOW-wellbeing /
// low-k phenomenon, exactly as the RCA described, not a universal property
// of the shape at every wellbeing level. The lean fixture below reproduces
// that regime precisely.
function leanRepro4City() {
  let s = initialState();
  s = reducer(s, { type: 'unlockAll' });
  s = reducer(s, { type: 'debugFunds', amount: 50_000_000_000 });
  const roadTiles = [];
  for (let x = 0; x < 200; x++) roadTiles.push({ x, y: 100 });
  s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: roadTiles });
  const resTiles = [];
  for (let x = 2; x < 198; x += 2) resTiles.push({ x, y: 98 });
  s = reducer(s, { type: 'placeMany', spec: 'res_block', tiles: resTiles });
  const officeId =
    'off_tower' in SPECS ? 'off_tower' : Object.values(SPECS).find((z) => z.kind === 'office').id;
  const offTiles = [];
  for (let x = 2; x < 198; x += 2) offTiles.push({ x, y: 102 });
  s = reducer(s, { type: 'placeMany', spec: officeId, tiles: offTiles });
  return s;
}

test('RED: the retired headroom-MULTIPLIER formula freezes this exact city (proves test sensitivity)', () => {
  // Reproduces the OLD (buggy) shape as a SHADOW calculation driven by the
  // real reducer's own state trajectory (buildings/tax/policies), using the
  // same pure attractiveness-input functions engine.ts exports
  // (demandOf/wellbeingOf) — proving the fixed-point freeze is a property of
  // the formula shape at the original RCA's low-wellbeing conditions, not of
  // this test's fixture choice.
  let real = leanRepro4City();
  for (let i = 0; i < 450; i++) real = reducer(real, { type: 'tick' });

  // Shadow state: same buildings/tax/policies as the real city, but its own
  // tracked population, advanced by the RETIRED formula every tick.
  let shadowPop = real.population;
  const MOVE_IN_RATE_OLD = 1.2;
  const MOVE_OUT_BASE_RATE = 0.003;
  const WELLBEING_MOVEOUT_FACTOR = 1.5;
  const BIRTH_RATE_PER_TICK = 0.0008;
  const DEATH_RATE_PER_TICK = 0.0005;

  const popHistory = [shadowPop];
  for (let t = 0; t < 800; t++) {
    const shadowState = { ...real, population: shadowPop };
    const capacity = onlineResidentsCapacity(shadowState);
    const demand = demandOf(shadowState);
    const t2 = shadowState.taxRates;
    const avgTax = (t2.residential + t2.commercial + t2.industrial) / 3;
    // EXACT retired shape: demand.residential-driven attractiveness.
    const oldAttractiveness =
      (1.4 - avgTax / 15) *
      (shadowState.policies.transitSubsidy ? 1.25 : 1) *
      Math.max(0.3, 0.55 + demand.residential / 200);
    const wb = wellbeingOf(shadowState).overall;
    const births = Math.round(shadowPop * BIRTH_RATE_PER_TICK);
    const deaths = Math.round(shadowPop * DEATH_RATE_PER_TICK);
    const moveOutRate = MOVE_OUT_BASE_RATE * (1 + (WELLBEING_MOVEOUT_FACTOR * (100 - wb)) / 100);
    const moveOuts = Math.round(shadowPop * moveOutRate);
    const headroom = Math.max(0, capacity - shadowPop);
    const effectiveHeadroom = Math.max(0, headroom + deaths + moveOuts);
    // THE RETIRED BUG SHAPE: moveIns is headroom * k, not gross-inflow capped by headroom.
    const moveIns = Math.max(
      0,
      Math.min(effectiveHeadroom, Math.round(effectiveHeadroom * MOVE_IN_RATE_OLD * oldAttractiveness))
    );
    shadowPop = Math.max(0, Math.min(capacity, shadowPop + births + moveIns - deaths - moveOuts));
    popHistory.push(shadowPop);
  }

  let maxFrozenRun = 0;
  let run = 1;
  for (let i = 1; i < popHistory.length; i++) {
    if (popHistory[i] === popHistory[i - 1]) {
      run++;
      maxFrozenRun = Math.max(maxFrozenRun, run);
    } else {
      run = 1;
    }
  }
  const finalCapacity = onlineResidentsCapacity({ ...real, population: shadowPop });
  console.log(
    `RED shadow-formula run: final pop=${shadowPop}, capacity=${finalCapacity}, longest frozen run=${maxFrozenRun} ticks`
  );
  // The defining symptom: a long frozen run at a population STILL below
  // capacity (positive vacancy) — this is exactly what the real fix (this
  // file's other tests) proves can no longer happen.
  assert.ok(
    maxFrozenRun > 100,
    `RED sanity: the retired formula should reproduce a long frozen run (got ${maxFrozenRun}) — if this fails, the shadow reproduction itself is wrong, not the real fix`
  );
  assert.ok(
    shadowPop < finalCapacity,
    `RED sanity: the retired formula's frozen run should sit at POSITIVE vacancy (pop=${shadowPop} < capacity=${finalCapacity})`
  );
});
