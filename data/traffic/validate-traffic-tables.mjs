#!/usr/bin/env node
// FEAT-2326609792 inc0 — validator for data/traffic/*.json.
// NOT a test file (deliberately kept out of any test/ dir per the CLAUDE.md node-test-discovery
// gotcha — CI's root `node --test` auto-discovers *.mjs under a test/ dir; this lives beside the
// data it checks and is a no-op unless invoked directly: `node data/traffic/validate-traffic-tables.mjs`).
//
// r2 rework (BUG-820, independent round opus-round-feat792-inc0): 14/26 mutants survived the
// original validator. This pass adds: rung-count + exact-population-sequence-vs-meta agreement,
// a recursive finite/non-negative/type walk over every rung leaf (schema-driven off rung 0, GR#15
// -- never a hardcoded leaf list), the four sum identities (tripsByMode/busSubtypeTrips/freight
// sector+class/workersInCity), five non-decreasing-across-rungs checks, and a peakHourFactor
// range check. BUG-823's provenance-is-entirely-non-numeric rule is enforced here too.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(__dirname, '..', '..');

function loadJson(relPath) {
  const p = path.join(ROOT, relPath);
  return JSON.parse(fs.readFileSync(p, 'utf8'));
}

/** Walks `node` against `schemaNode` (rung 0, the schema-of-record — GR#15: the expected shape is
 * DERIVED from the data file, never a hardcoded literal list of leaf names). At every path where
 * schemaNode is a number, `node` at the same path MUST also be a finite, non-negative number — a
 * string/null/negative/non-finite value there is RED. `provenance` subtrees are walked with the
 * inverse rule (BUG-823): every leaf under `provenance` must be a STRING, never numeric, at ANY
 * depth, regardless of what rung 0's provenance looks like (rung 0 itself must obey this too). */
function walkNumericLeaves(node, schemaNode, pathSoFar, rungLabel, warn, inProvenance) {
  if (schemaNode !== null && typeof schemaNode === 'object') {
    if (Array.isArray(schemaNode)) {
      if (!Array.isArray(node)) {
        warn(`${rungLabel} ${pathSoFar}: expected an array (schema from rung 0), got ${typeof node}`);
        return;
      }
      for (let i = 0; i < schemaNode.length; i++) {
        walkNumericLeaves(node[i], schemaNode[i], `${pathSoFar}[${i}]`, rungLabel, warn, inProvenance);
      }
      return;
    }
    if (node === null || typeof node !== 'object' || Array.isArray(node)) {
      warn(`${rungLabel} ${pathSoFar}: expected an object (schema from rung 0), got ${node === null ? 'null' : typeof node}`);
      return;
    }
    const nextInProvenance = inProvenance || pathSoFar.endsWith('.provenance') || pathSoFar === 'provenance';
    for (const key of Object.keys(schemaNode)) {
      walkNumericLeaves(node[key], schemaNode[key], pathSoFar ? `${pathSoFar}.${key}` : key, rungLabel, warn, nextInProvenance);
    }
    return;
  }

  // Leaf. BUG-823: anything under a rung's provenance subtree must be a string, never numeric,
  // regardless of what schemaNode (rung 0) looks like there.
  if (inProvenance) {
    if (typeof node === 'number') {
      warn(`${rungLabel} ${pathSoFar}: provenance leaf is NUMERIC (${node}) — every provenance field must be a string (BUG-823, avoids silent log-linear interpolation of e.g. a year)`);
    }
    return;
  }

  if (typeof schemaNode === 'number') {
    // A number on rung 0 at this path — the same path on every rung must also be a finite,
    // non-negative number (a string/null/negative/non-finite value here is RED).
    if (typeof node !== 'number') {
      warn(`${rungLabel} ${pathSoFar}: expected a number (rung 0's schema has a number here), got ${node === null ? 'null' : typeof node} (${JSON.stringify(node)})`);
      return;
    }
    if (!Number.isFinite(node)) {
      warn(`${rungLabel} ${pathSoFar}: numeric leaf is not finite (${node})`);
      return;
    }
    if (node < 0) {
      warn(`${rungLabel} ${pathSoFar}: numeric leaf is negative (${node})`);
    }
    return;
  }

  // A NON-number on rung 0 at this path (e.g. densityBand, a string) — the same path on every
  // rung must stay non-numeric too. A number where a string was is RED (BUG-820 mutant (g)).
  if (typeof schemaNode === 'string' && typeof node === 'number') {
    warn(`${rungLabel} ${pathSoFar}: expected a string (rung 0's schema has a string here), got a number (${node})`);
  }
}

function sumOf(obj) {
  return Object.values(obj).reduce((a, x) => a + x, 0);
}

function main() {
  const errors = [];
  const warn = (m) => errors.push(m);

  const scaleLadder = loadJson('data/traffic/scale_ladder.json');
  const modeShare = loadJson('data/traffic/mode_share_by_density.json');
  const roads = loadJson('data/roads.json');
  const linkCap = loadJson('data/traffic/link_capacity.json');
  const modes = loadJson('data/modes.json');
  const vehicleClasses = loadJson('data/traffic/vehicle_classes.json');

  const rungs = scaleLadder.rungs;

  // BUG-820 (a)+(b): rung count and exact population sequence must equal meta.rungPopulations
  // (GR#15 — the expected sequence is DERIVED from meta.rungPopulations, never a literal 18).
  const expectedPopulations = scaleLadder.meta && Array.isArray(scaleLadder.meta.rungPopulations)
    ? scaleLadder.meta.rungPopulations
    : null;
  if (!expectedPopulations) {
    warn('scale_ladder.json meta.rungPopulations is missing or not an array — cannot validate rung count/sequence');
  } else if (!Array.isArray(rungs) || rungs.length === 0) {
    warn('scale_ladder.json rungs[] is empty (or missing) — expected ' + expectedPopulations.length + ' rungs from meta.rungPopulations');
  } else {
    if (rungs.length !== expectedPopulations.length) {
      warn(`rungs.length (${rungs.length}) does not match meta.rungPopulations.length (${expectedPopulations.length})`);
    }
    const n = Math.min(rungs.length, expectedPopulations.length);
    for (let i = 0; i < n; i++) {
      if (rungs[i].population !== expectedPopulations[i]) {
        warn(`rungs[${i}].population (${rungs[i].population}) does not match meta.rungPopulations[${i}] (${expectedPopulations[i]})`);
      }
    }
  }

  if (Array.isArray(rungs) && rungs.length > 0) {
    // AC: rungs ascending
    for (let i = 1; i < rungs.length; i++) {
      if (!(rungs[i].population > rungs[i - 1].population)) warn(`rungs not strictly ascending at index ${i}`);
      if (!(rungs[i].dailyPersonTrips > rungs[i - 1].dailyPersonTrips)) warn(`dailyPersonTrips not monotone increasing at index ${i}`);
    }

    // AC: mode shares sum to 1.0 +/- 1e-6
    for (const r of rungs) {
      const sum = sumOf(r.modeShare);
      if (Math.abs(sum - 1) > 1e-6) warn(`scale_ladder rung pop=${r.population} modeShare sums to ${sum}`);
    }

    // BUG-820 (c): recursive finite/non-negative/type walk over every rung leaf, schema-driven
    // off rung 0 (GR#15). Also enforces BUG-823's provenance-is-entirely-non-numeric rule.
    const schemaRung = rungs[0];
    for (const r of rungs) {
      const label = `scale_ladder rung pop=${r.population}`;
      for (const key of Object.keys(schemaRung)) {
        walkNumericLeaves(r[key], schemaRung[key], key, label, warn, key === 'provenance');
      }
    }

    // BUG-820 (d): sum identities.
    for (const r of rungs) {
      const label = `scale_ladder rung pop=${r.population}`;
      const modeCount = Object.keys(r.tripsByMode || {}).length;
      const tripsByModeSum = sumOf(r.tripsByMode || {});
      if (Math.abs(tripsByModeSum - r.dailyPersonTrips) > modeCount) {
        warn(`${label}: tripsByMode sums to ${tripsByModeSum}, dailyPersonTrips is ${r.dailyPersonTrips} (residual ${Math.abs(tripsByModeSum - r.dailyPersonTrips)} exceeds tolerance ±${modeCount})`);
      }

      const busSubtypeSum = sumOf(r.busSubtypeTrips || {});
      const busTrips = (r.tripsByMode || {}).bus;
      if (typeof busTrips === 'number' && Math.abs(busSubtypeSum - busTrips) > 4) {
        warn(`${label}: busSubtypeTrips sums to ${busSubtypeSum}, tripsByMode.bus is ${busTrips} (residual ${Math.abs(busSubtypeSum - busTrips)} exceeds tolerance ±4)`);
      }

      const sectorEntries = Object.keys(r.freightTonnesBySector || {}).length;
      const sectorSum = sumOf(r.freightTonnesBySector || {});
      if (Math.abs(sectorSum - r.freightTonnesPerDay) > sectorEntries) {
        warn(`${label}: freightTonnesBySector sums to ${sectorSum}, freightTonnesPerDay is ${r.freightTonnesPerDay} (residual ${Math.abs(sectorSum - r.freightTonnesPerDay)} exceeds tolerance ±${sectorEntries})`);
      }

      const classEntries = Object.keys(r.freightTonnesByVehicleClass || {}).length;
      const classSum = sumOf(r.freightTonnesByVehicleClass || {});
      if (Math.abs(classSum - r.freightTonnesPerDay) > classEntries) {
        warn(`${label}: freightTonnesByVehicleClass sums to ${classSum}, freightTonnesPerDay is ${r.freightTonnesPerDay} (residual ${Math.abs(classSum - r.freightTonnesPerDay)} exceeds tolerance ±${classEntries})`);
      }

      const expectedWorkers = Math.round(r.population * r.workersInCityShare);
      if (Math.abs(expectedWorkers - r.workersInCity) > 1) {
        warn(`${label}: workersInCity (${r.workersInCity}) disagrees with round(population * workersInCityShare) (${expectedWorkers}) by more than ±1`);
      }
    }

    // BUG-820 (e): strictly non-decreasing across rungs for these five fields.
    const nonDecreasingFields = [
      'networkVehicleKmPerDay',
      'parkingSpacesDemanded',
      'evChargePointsNeeded',
      'emergencyIncidentsPerDay',
      'roadWearESALPerDay',
    ];
    for (const field of nonDecreasingFields) {
      let prev = -Infinity;
      for (const r of rungs) {
        const v = r[field];
        if (typeof v === 'number' && v < prev) {
          warn(`${field} not non-decreasing at pop=${r.population} (${v} < ${prev})`);
        }
        if (typeof v === 'number') prev = v;
      }
    }

    // BUG-820 (f): peakHourFactor within [0.08, 0.15].
    for (const r of rungs) {
      const p = r.peakHourFactor;
      if (typeof p !== 'number' || p < 0.08 || p > 0.15) {
        warn(`scale_ladder rung pop=${r.population} peakHourFactor ${p} outside [0.08, 0.15]`);
      }
    }
  }

  // mode_share_by_density.json bands sum to 1.0
  for (const b of modeShare.bands) {
    const sum = sumOf(b.shares);
    if (Math.abs(sum - 1) > 1e-6) warn(`mode_share_by_density band ${b.id} sums to ${sum}`);
  }

  // AC: road class ids referenced in link_capacity.json resolve against data/roads.json
  const roadIds = new Set(roads.classes.map((c) => c.id));
  for (const rc of linkCap.roadClasses) {
    if (!roadIds.has(rc.roadClassId)) warn(`link_capacity.json roadClassId '${rc.roadClassId}' not found in data/roads.json`);
  }
  for (const id of Object.keys(linkCap.bprCurve.perClassOverrides)) {
    if (id === '$comment') continue;
    if (!roadIds.has(id)) warn(`link_capacity.json bprCurve override key '${id}' not found in data/roads.json`);
  }

  // AC-5: car share monotonically falls, rail-family share monotonically rises across rungs
  if (Array.isArray(rungs) && rungs.length > 0) {
    let prevCar = Infinity, prevRail = -Infinity;
    for (const r of rungs) {
      const car = r.modeShare.car;
      const rail = (r.modeShare.metro || 0) + (r.modeShare.heavy_rail || 0) + (r.modeShare.hs_rail || 0) + (r.modeShare.tram || 0);
      if (car > prevCar) warn(`car share not monotone-falling at pop=${r.population} (${car} > ${prevCar})`);
      if (rail < prevRail) warn(`rail-family share not monotone-rising at pop=${r.population} (${rail} < ${prevRail})`);
      prevCar = car; prevRail = rail;
    }
  }

  // AC-6: vehicle_classes.json refinesModeId resolves against data/modes.json mode ids (or null)
  const modeIds = new Set(modes.modes.map((m) => m.id));
  for (const v of vehicleClasses.roadVehicles) {
    if (v.refinesModeId !== null && !modeIds.has(v.refinesModeId)) warn(`vehicle_classes.json roadVehicles['${v.id}'].refinesModeId '${v.refinesModeId}' not found in data/modes.json`);
  }
  for (const [id, v] of Object.entries(vehicleClasses.busSubtypes)) {
    if (id === '$comment') continue;
    if (!modeIds.has(v.refinesModeId)) warn(`vehicle_classes.json busSubtypes['${id}'].refinesModeId '${v.refinesModeId}' not found in data/modes.json`);
    // BUG-821: totalCapacity must equal seated + standing exactly.
    if (typeof v.seatedCapacity === 'number' && typeof v.standingCapacity === 'number' && typeof v.totalCapacity === 'number') {
      if (v.seatedCapacity + v.standingCapacity !== v.totalCapacity) {
        warn(`vehicle_classes.json busSubtypes['${id}']: seatedCapacity (${v.seatedCapacity}) + standingCapacity (${v.standingCapacity}) != totalCapacity (${v.totalCapacity})`);
      }
    }
  }
  for (const v of vehicleClasses.fixedTrack) {
    if (!modeIds.has(v.refinesModeId)) warn(`vehicle_classes.json fixedTrack['${v.id}'].refinesModeId '${v.refinesModeId}' not found in data/modes.json`);
  }

  if (errors.length) {
    console.error(`FAIL: ${errors.length} issue(s):`);
    for (const e of errors) console.error(' -', e);
    process.exitCode = 1;
  } else {
    console.log('OK: data/traffic/*.json pass all structural checks.');
  }
}

// Only run when executed directly, never on import (mirrors the project's NODE_TEST_CONTEXT gotcha
// for tools living under a test/ dir — this file isn't under one, but stays no-op-on-import for safety).
if (import.meta.url === `file://${process.argv[1].replace(/\\/g, '/')}` || process.argv[1] === fileURLToPath(import.meta.url)) {
  main();
}
