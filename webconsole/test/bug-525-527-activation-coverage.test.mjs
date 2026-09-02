// bug-525-527-activation-coverage.test.mjs — BUG-525 (totalJobs) + BUG-527
// (sumBy) same-root-cause fix: the activation-consistency class (BUG-430/431
// family). Two aggregation functions in data.ts iterated `s.buildings`
// WITHOUT an `isOnline(s, b)` gate, unlike powerStats() (data.ts ~1918:
// `if (!isOnline(s,b)) continue;`) and wasteGeneratedOf() (~2208). An
// OFFLINE / road-disconnected / under-construction building contributed
// FULL capacity while (correctly) drawing zero upkeep — free coverage/jobs
// from a non-functioning building.
//
//   BUG-527: sumBy() backs GP/hospital/police/school coverage in BOTH
//            serviceCoverageOf() and utilisationOf().
//   BUG-525: totalJobs() sums jobs for every job building, while its sibling
//            onlineResidentsCapacity() IS gated.
//
// Run with `npm test` (node --test); node type-strips the imported .ts so
// these assertions exercise the exact shipped aggregation — no copy, drift.
//
// Every RED-proof assertion is written to FAIL if the corresponding gate is
// dropped: temporarily strip `if (!isOnline(s, b)) continue;` from sumBy /
// totalJobs (scratch cp/mv, NEVER git — GR#24) and the offline-contributes-
// zero assertions below go RED (an offline building would count its full
// served/jobs capacity). See report for the captured RED output.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  serviceCoverageOf,
  utilisationOf,
  totalJobs,
  isOnline,
  computeRoadConnectivity,
  constructionTicks,
} from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

let _id = 525000;
const B = (spec, x, y, extra = {}) => ({ id: _id++, spec, x, y, ...extra });

// A fresh state whose ONLY buildings are the given list, with road
// connectivity computed exactly as advance() does every tick. Fresh arrays
// each call so the per-array / per-connectivity WeakMap memos in data.ts
// never go stale (same harness as bug430-power-gate.test.mjs).
function city(buildings, tick = 20, population = 0) {
  const s = initialState();
  const st = { ...s, buildings: [...buildings], population, tick };
  st.roadConnectivity = computeRoadConnectivity(st);
  return st;
}

const CLINIC = SPECS.hea_clinic; // health, served: 5000
const CLINIC_SERVED = CLINIC.served;
const OFFICE = SPECS.off_suite; // office, jobs: 25
const OFFICE_JOBS = OFFICE.jobs;

// ─────────────────────────────────────────────────────────────────────────
// BUG-527: sumBy() / serviceCoverageOf() — GP coverage.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-527: an ONLINE clinic contributes its full served capacity to GP coverage', () => {
  // Road-connected placement (edge road at (0,10) beside the clinic at
  // (1,10)), built long ago (past its construction window) => fully online.
  const roads = [B('road', 0, 10, { builtTick: 0 })];
  const s = city([B('hea_clinic', 1, 10, { builtTick: 0 }), ...roads], 200, 1000);
  const clinic = s.buildings[0];
  assert.equal(isOnline(s, clinic), true, 'setup: connected + built-out clinic must be online');

  const gpRow = serviceCoverageOf(s).find((r) => r.id === 'gp');
  assert.ok(gpRow, 'sanity: the gp coverage row exists');
  assert.equal(gpRow.cap, CLINIC_SERVED, 'an online clinic must contribute its full served capacity');
  assert.ok(CLINIC_SERVED > 0, 'sanity: the clinic actually has service capacity to contribute');
});

test('BUG-527: an OFFLINE (road-disconnected) clinic contributes ZERO to GP coverage', () => {
  // DISCONNECTED: one road stub at (1,10) touches the clinic but the stub
  // itself is interior — not a map edge, not near a trunk — so it never
  // joins the connected network. Clinic is road-adjacent but NOT road-
  // connected (same disconnection pattern as bug430's test 1, offset one
  // tile in from the edge so the stub itself isn't the edge seed).
  const s = city([B('hea_clinic', 2, 10, { builtTick: 0 }), B('road', 1, 10, { builtTick: 0 })], 200, 1000);
  const clinic = s.buildings[0];
  assert.equal(isOnline(s, clinic), false, 'setup: disconnected clinic must be offline');

  const gpRow = serviceCoverageOf(s).find((r) => r.id === 'gp');
  assert.equal(gpRow.cap, 0, 'an OFFLINE clinic must contribute ZERO to GP coverage (was the bug)');
});

test('BUG-527: an OFFLINE (under-construction) clinic contributes ZERO to utilisationOf health', () => {
  const buildTicks = constructionTicks(CLINIC);
  assert.ok(buildTicks >= 1, 'sanity: the clinic has a real construction window');
  // Road-connected placement so the ONLY gate in play is construction time.
  const roads = [B('road', 0, 10, { builtTick: 0 })];

  // builtTick == tick ⇒ 0 ticks elapsed < buildTicks ⇒ under construction ⇒ offline.
  const underConstruction = city([B('hea_clinic', 1, 10, { builtTick: 20 }), ...roads], 20, 1000);
  const clinicUC = underConstruction.buildings[0];
  assert.equal(isOnline(underConstruction, clinicUC), false, 'setup: still under construction');
  const utilUC = utilisationOf(underConstruction, clinicUC);
  assert.equal(utilUC, null, 'an offline-only city has zero health cap so utilisationOf returns null (no cap>0 branch)');

  // Advance past the construction window ⇒ online ⇒ cap = full served.
  const built = city([B('hea_clinic', 1, 10, { builtTick: 20 }), ...roads], 20 + buildTicks + 1, 1000);
  const clinicBuilt = built.buildings[0];
  assert.equal(isOnline(built, clinicBuilt), true, 'setup: construction complete → online');
  const utilBuilt = utilisationOf(built, clinicBuilt);
  assert.ok(utilBuilt, 'an online clinic must produce a non-null utilisation reading');
  assert.equal(utilBuilt.basis, 'citywide health coverage');
});

// ─────────────────────────────────────────────────────────────────────────
// BUG-525: totalJobs() — jobs reflect an online factory/office, zero offline.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-525: an ONLINE office contributes its full jobs to totalJobs', () => {
  const roads = [B('road', 0, 10, { builtTick: 0 })];
  const s = city([B('off_suite', 1, 10, { builtTick: 0 }), ...roads], 200);
  const office = s.buildings[0];
  assert.equal(isOnline(s, office), true, 'setup: connected + built-out office must be online');
  assert.equal(totalJobs(s), OFFICE_JOBS, 'an online office must contribute its full jobs count');
  assert.ok(OFFICE_JOBS > 0, 'sanity: the office actually carries jobs');
});

test('BUG-525: an OFFLINE (road-disconnected) office contributes ZERO jobs', () => {
  // Road stub at (1,10) is interior — not a map edge, not near a trunk — so
  // it never joins the connected network. Office at (2,10) is road-adjacent
  // but NOT road-connected (same disconnection pattern as bug430's test 1).
  const disconnected = city(
    [B('off_suite', 2, 10, { builtTick: 0 }), B('road', 1, 10, { builtTick: 0 })],
    200
  );
  const disc = disconnected.buildings[0];
  assert.equal(isOnline(disconnected, disc), false, 'setup: disconnected office must be offline');
  assert.equal(totalJobs(disconnected), 0, 'an OFFLINE office must contribute ZERO jobs (was the bug)');
});

test('BUG-525: an OFFLINE (under-construction) factory contributes ZERO jobs; built → full', () => {
  const FACTORY = SPECS.ind_light; // industrial, jobs: 24
  const buildTicks = constructionTicks(FACTORY);
  assert.ok(buildTicks >= 1, 'sanity: the factory has a real construction window');
  const roads = [B('road', 0, 10, { builtTick: 0 })];

  const underConstruction = city([B('ind_light', 1, 10, { builtTick: 20 }), ...roads], 20);
  assert.equal(
    isOnline(underConstruction, underConstruction.buildings[0]),
    false,
    'setup: still under construction'
  );
  assert.equal(totalJobs(underConstruction), 0, 'an under-construction factory must add ZERO jobs');

  const built = city([B('ind_light', 1, 10, { builtTick: 20 }), ...roads], 20 + buildTicks + 1);
  assert.equal(isOnline(built, built.buildings[0]), true, 'setup: construction complete → online');
  assert.equal(totalJobs(built), FACTORY.jobs, 'a completed + connected factory adds its full jobs');
});

// ─────────────────────────────────────────────────────────────────────────
// Determinism (GR#21) — pure functions of state; no Date/Math.random.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-525/527: serviceCoverageOf and totalJobs are deterministic across identical states', () => {
  const mk = () =>
    city(
      [
        B('hea_clinic', 1, 10, { builtTick: 0 }),
        B('off_suite', 5, 10, { builtTick: 0 }),
        B('road', 0, 10, { builtTick: 0 }),
        B('road', 1, 11, { builtTick: 0 }),
      ],
      200,
      1000
    );
  const a = serviceCoverageOf(mk());
  const b = serviceCoverageOf(mk());
  assert.deepEqual(a, b, 'identical states must yield identical coverage rows');
  assert.equal(totalJobs(mk()), totalJobs(mk()), 'identical states must yield identical totalJobs');
  const s = mk();
  assert.deepEqual(serviceCoverageOf(s), serviceCoverageOf(s), 'repeat call on one state is stable');
});
