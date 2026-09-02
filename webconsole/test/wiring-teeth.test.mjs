// wiring-teeth.test.mjs — Q100046 A1/C1 (BUG-526 fire, BUG-524 jobs).
//
// (a) Fire stations charged upkeep with NO wellbeing effect (a cost-only
//     sink) before this fix. Proves placing fire coverage raises the new
//     "Fire safety" wellbeing part, and 0 fire coverage reads strictly lower
//     than full fire coverage.
// (b) A jobs shortfall only softly throttled move-in before this fix — zero
//     wellbeing/move-out teeth. Proves a high-unemployment state has BOTH
//     lower wellbeing AND a higher move-out rate than a full-employment
//     state at the same population (the wellbeing→move-out link already in
//     engine.ts, ~1183 — see the NO-DOUBLE-COUNT DECISION comment on the
//     "Jobs/Employment" part in wellbeingOf for why unemployment is not ALSO
//     a second direct term in moveOutRate).
//
// RED-proven manually (GR#23 — scratch cp/mv, not automated here): commenting
// out the `{ label: 'Fire safety', ... }` push in wellbeingOf reddens test
// (a); commenting out the `{ label: 'Jobs/Employment', ... }` push reddens
// test (b)'s wellbeing half — move-out half also reddens because it is fed
// entirely THROUGH wbOverall, which is exactly the point of the no-double-
// count design (there is no separate direct term to leave standing). Both
// reverts were applied, confirmed RED, then restored — see the session report.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, serviceCoverageOf, unemploymentOf, totalJobs } from '../src/sim/data.ts';
import { initialState, wellbeingOf, reducer } from '../src/sim/engine.ts';

/** A city: initial (service-free) starter map + population + extra buildings. */
function city(pop, specCounts = {}) {
  const s = initialState();
  s.population = pop;
  let id = 50000;
  let slot = 0;
  for (const [spec, n] of Object.entries(specCounts)) {
    assert.ok(SPECS[spec], `test setup: unknown spec "${spec}"`);
    for (let i = 0; i < n; i++) {
      // Coordinates are irrelevant to the coverage/wellbeing math.
      s.buildings.push({ id: id++, spec, x: 5 + (slot % 40) * 5, y: 5 + Math.floor(slot / 40) * 5 });
      slot++;
    }
  }
  return s;
}

const partOf = (s, label) => {
  const p = wellbeingOf(s).parts.find((p) => p.label === label);
  assert.ok(p, `no wellbeing part "${label}"`);
  return p.value;
};

// ───────────────────────── FIX 1: fire (BUG-526) ─────────────────────────

test('fire coverage: serviceCoverageOf has a real "fire" row (need = pop, cap = Σ served)', () => {
  // fire_station covers 20,000 (data.ts P() extra { served: 20000 }).
  const s = city(20000, { fire_station: 1 });
  const fire = serviceCoverageOf(s).find((r) => r.id === 'fire');
  assert.ok(fire, 'serviceCoverageOf must carry a fire row');
  assert.equal(fire.need, 20000, 'fire need is population, matching every other served-based service');
  assert.equal(fire.cap, 20000, 'fire cap is Σ served');
  assert.equal(fire.coverage, 1);
});

test('BUG-526: 0 fire coverage reads LOWER Fire-safety wellbeing than full fire coverage', () => {
  const pop = 20000;
  const noCover = city(pop, {});
  const fullCover = city(pop, { fire_station: 1 }); // served 20000 == pop → coverage 1

  const noFireValue = partOf(noCover, 'Fire safety');
  const fullFireValue = partOf(fullCover, 'Fire safety');

  assert.ok(
    fullFireValue > noFireValue,
    `full fire coverage (${fullFireValue}) must read strictly higher than zero coverage (${noFireValue})`
  );
  // Zero coverage must not silently read as "fine" (the pre-fix cost-only-sink
  // symptom: fire stations existed and drew upkeep but never touched wellbeing
  // at all, so a city with NO fire cover looked identical to one fully covered).
  assert.ok(noFireValue < 90, 'zero fire coverage must visibly depress the Fire-safety part');
});

test('BUG-526: placing/removing a fire station moves the overall wellbeing score', () => {
  const pop = 20000;
  // A city already at 100% on every other coverage row so Fire safety is the
  // only lever that can move `overall` between the two builds.
  const fullServices = {
    hea_clinic: 4,     // 20,000 served
    hea_hospital: 4,   // 20,000 served (assume same per-unit as clinic-class; any excess is harmless)
    pol_station: 4,    // 20,000 served (assume 5,000/unit)
    wat_clean: 1,
    wat_waste: 1,
  };
  const without = city(pop, fullServices);
  const withFire = city(pop, { ...fullServices, fire_station: 1 });
  assert.ok(
    wellbeingOf(withFire).overall > wellbeingOf(without).overall,
    'adding fire coverage must raise overall wellbeing, not sit inert as a cost-only sink'
  );
});

// ─────────────────────── FIX 2: jobs/unemployment (BUG-524) ───────────────

test('unemploymentOf: 0 jobs vs full jobs at the same population', () => {
  const s0 = city(10000, {}); // no jobs at all → workers=5500, jobs=0
  assert.equal(unemploymentOf(s0), 1, 'no jobs anywhere → 100% unemployment');

  // off_tower carries 300 jobs/unit; enough units to exceed workers = 5500.
  const sFull = city(10000, { off_tower: 20 }); // 6000 jobs >= 5500 workers
  assert.equal(totalJobs(sFull) >= 10000 * 0.55, true, 'test setup: jobs must meet or exceed workers');
  assert.equal(unemploymentOf(sFull), 0, 'jobs >= workers → 0% unemployment, never negative');
});

test('BUG-524: high unemployment reads LOWER Jobs/Employment wellbeing than full employment', () => {
  const pop = 10000;
  const noJobs = city(pop, {});
  const fullJobs = city(pop, { off_tower: 20 });

  const employedValue = partOf(fullJobs, 'Jobs/Employment');
  const unemployedValue = partOf(noJobs, 'Jobs/Employment');
  assert.ok(
    employedValue > unemployedValue,
    `full employment (${employedValue}) must read strictly higher than 100% unemployment (${unemployedValue})`
  );
});

// A minimal, fully-controlled base state (same shape as
// demographic-flows.test.mjs's baseState) — deliberately WITHOUT
// roadConnectivity so isOnline()'s road-adjacency gates are skipped,
// letting job/residential buildings here count regardless of map position.
function baseState(overrides = {}) {
  return {
    tick: 0,
    speed: 1,
    funds: 10_000_000,
    loanBalance: 0,
    population: 0,
    xp: 30,
    taxRates: { residential: 9, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: false },
    buildings: [],
    nextId: 1,
    movingId: null,
    tool: { mode: 'select' },
    clipboard: null,
    pipeTier: {},
    history: [],
    ledger: [],
    nextLedgerId: 1,
    lastFlows: { inflows: [], outflows: [] },
    fundsAtTickStart: 10_000_000,
    fundsAtTickEnd: 10_000_000,
    pendingRewards: [],
    lastRewardedLevel: 1,
    notice: null,
    unlockedAll: false,
    roadNotice: null,
    railNotice: null,
    placeNotice: null,
    roadMonitors: [],
    buildingMonitors: [],
    ...overrides,
  };
}

function residentialBuildings(n, spec = 'res_estate') {
  const out = [];
  for (let i = 0; i < n; i++) out.push({ id: i + 1, spec, x: 10 + i, y: 10 });
  return out;
}

function jobBuildings(n, spec = 'off_tower', startId = 100000) {
  const out = [];
  for (let i = 0; i < n; i++) out.push({ id: startId + i, spec, x: 10 + i, y: 40 });
  return out;
}

test('BUG-524: high unemployment produces a HIGHER move-out rate than full employment (via wellbeing, no double-count)', () => {
  // Well below residential capacity so the popBefore <= capacity move-out
  // branch (engine.ts ~1175) is always taken, and a fixed population lets us
  // compare moveOuts directly between the two scenarios at the same tick.
  const residents = residentialBuildings(10, 'res_estate'); // plenty of capacity headroom
  const pop = 8000;

  const unemployedState = baseState({
    population: pop,
    buildings: [...residents], // zero job buildings → 100% unemployment
  });
  const employedState = baseState({
    population: pop,
    buildings: [...residents, ...jobBuildings(30, 'off_tower')], // 9000 jobs >> 4400 workers
  });

  // Sanity: confirm the unemployment gap this test depends on actually exists
  // in the two constructed states before comparing downstream effects.
  assert.ok(unemploymentOf(unemployedState) > unemploymentOf(employedState) + 0.5,
    'test setup must produce a real unemployment gap between the two scenarios');

  const afterUnemployed = reducer(unemployedState, { type: 'tick' });
  const afterEmployed = reducer(employedState, { type: 'tick' });

  const wbUnemployed = wellbeingOf(unemployedState).overall;
  const wbEmployed = wellbeingOf(employedState).overall;
  assert.ok(wbEmployed > wbUnemployed, 'full employment must read higher overall wellbeing than 100% unemployment');

  const moveOutsUnemployed = afterUnemployed.lastDemographics.moveOuts;
  const moveOutsEmployed = afterEmployed.lastDemographics.moveOuts;
  assert.ok(
    moveOutsUnemployed > moveOutsEmployed,
    `100%-unemployment move-outs (${moveOutsUnemployed}) must exceed full-employment move-outs (${moveOutsEmployed})`
  );
});
