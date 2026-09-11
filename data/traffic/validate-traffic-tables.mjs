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
  const tripGeneration = loadJson('data/traffic/trip_generation.json');
  const trafficConfig = loadJson('data/traffic.json');

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

  // FEAT-2326609797 inc4 rework (BUG-869): every service in emergency_response.json services[]
  // must carry a positive, finite turnoutMinutes (the dispatch-to-mobile activation leg) --
  // deleting the field, or leaving it non-numeric/non-positive, must RED this check (this is the
  // field loadTurnoutMinutesFrom in emergencyResponse.ts reads fail-closed at module load).
  const emergencyResponse = loadJson('data/traffic/emergency_response.json');
  if (!Array.isArray(emergencyResponse.services)) {
    warn('emergency_response.json services[] is missing or not an array');
  } else {
    for (const row of emergencyResponse.services) {
      const v = row.turnoutMinutes;
      if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
        warn(`emergency_response.json services['${row.service}'].turnoutMinutes must be a positive finite number, got ${JSON.stringify(v)}`);
      }
    }
  }

  // FEAT-2326609800 inc7 rework (BUG-914(d)): every roadVehicles class in
  // vehicle_classes.json must carry a positive, finite
  // trip_generation.json tripsPerVehiclePerDay entry -- deleting the block,
  // or leaving it non-numeric/non-positive, must RED this check (mirrors
  // the emergency_response.json turnoutMinutes precedent above; this is the
  // field tripsPerVehiclePerDayFor in trafficAssignment.ts reads
  // fail-closed, MET-V940).
  {
    const tpvd = tripGeneration.tripsPerVehiclePerDay ?? {};
    for (const v of vehicleClasses.roadVehicles) {
      const row = tpvd[v.id];
      const val = row?.tripsPerVehiclePerDay;
      if (typeof val !== 'number' || !Number.isFinite(val) || val <= 0) {
        warn(`trip_generation.json tripsPerVehiclePerDay['${v.id}'].tripsPerVehiclePerDay must be a positive finite number, got ${JSON.stringify(val)}`);
      }
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

  // FEAT-2326609805 inc10: overlays.json config sanity -- alphas in [0,1],
  // demand bounds ascending, congestion/wear bands ascending. Structural
  // only (the loader itself, trafficOverlays.ts, is the fail-closed gate
  // MET-V945 for missing/non-numeric fields; this validator catches a
  // present-but-nonsensical value the loader's isFinite check would pass).
  {
    const overlays = loadJson('data/traffic/overlays.json');
    const alphaFields = [
      ['demand.paleAlpha', overlays.demand?.paleAlpha],
      ['demand.saturatedAlpha', overlays.demand?.saturatedAlpha],
      ['modeShare.pureAlpha', overlays.modeShare?.pureAlpha],
      ['congestion.redAlpha', overlays.congestion?.redAlpha],
      ['congestion.yellowAlpha', overlays.congestion?.yellowAlpha],
      ['congestion.greenAlpha', overlays.congestion?.greenAlpha],
      ['parking.alpha', overlays.parking?.alpha],
      ['fuelEv.alpha', overlays.fuelEv?.alpha],
      ['wear.freshAlpha', overlays.wear?.freshAlpha],
      ['wear.failedAlpha', overlays.wear?.failedAlpha],
    ];
    for (const [label, v] of alphaFields) {
      if (typeof v !== 'number' || !Number.isFinite(v) || v < 0 || v > 1) {
        warn(`overlays.json ${label} must be a finite number in [0,1], got ${JSON.stringify(v)}`);
      }
    }
    if (!(overlays.demand?.paleTrips < overlays.demand?.saturatedTrips)) {
      warn(`overlays.json demand.paleTrips (${overlays.demand?.paleTrips}) must be < demand.saturatedTrips (${overlays.demand?.saturatedTrips})`);
    }
    if (!(overlays.demand?.paleAlpha < overlays.demand?.saturatedAlpha)) {
      warn(`overlays.json demand.paleAlpha must be < demand.saturatedAlpha`);
    }
    if (!(overlays.congestion?.yellowThreshold < overlays.congestion?.redThreshold)) {
      warn(`overlays.json congestion.yellowThreshold must be < congestion.redThreshold`);
    }
    if (!(overlays.congestion?.greenAlpha < overlays.congestion?.yellowAlpha && overlays.congestion?.yellowAlpha < overlays.congestion?.redAlpha)) {
      warn(`overlays.json congestion alphas must be strictly ascending green < yellow < red`);
    }
    if (!(overlays.wear?.conditionRedBand < overlays.wear?.conditionYellowBand)) {
      warn(`overlays.json wear.conditionRedBand must be < wear.conditionYellowBand`);
    }
    if (!(overlays.wear?.freshAlpha < overlays.wear?.failedAlpha)) {
      warn(`overlays.json wear.freshAlpha must be < wear.failedAlpha`);
    }
  }

  // BUG-968 (r3 rework, FEAT-2326609798 estate): data/traffic.json's
  // maxAttributionRadiusTiles must be a positive, finite INTEGER --
  // trafficDemand.ts/trafficAssignment.ts/emergencyResponse.ts's three
  // loaders each Math.floor it before use so nearestSourceForTiles' radius
  // domain always agrees with boundedNearestSourceMapOf's inclusive
  // `dist < radius` layer semantics (a fractional radius previously admitted
  // a divergence on the flood's final partial layer). This check does not
  // enforce the floor itself (that lives in the three loaders, each with its
  // own unit-testable pure form) -- it enforces that the SHIPPED data value
  // is already an integer, so the floor is a no-op safety net, not a silent
  // corrector of bad data.
  {
    const v = trafficConfig.maxAttributionRadiusTiles;
    if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
      warn(`data/traffic.json maxAttributionRadiusTiles must be a positive finite number, got ${JSON.stringify(v)}`);
    } else if (!Number.isInteger(v)) {
      warn(`data/traffic.json maxAttributionRadiusTiles must be an INTEGER (BUG-968), got ${v}`);
    }
  }

  // FEAT-2326609804 (inc4, AC-1/AC-3/AC-7): mode_split_local.json structural checks.
  // walkAccessRadiusMetres positive finite (AC-2/AC-7 — the TS loader reads this, never a
  // hand-typed literal); landUseDensityMapping values must be real mode_share_by_density.json
  // band ids (GR#3 — the local table reuses that table's anchors, never a second density
  // model); every table row's mode-share vector must use EXACTLY modeIds and sum to 1.0
  // (AC-3's own vector shape, matching mode_share_by_density.json's own bands[].shares check
  // above); every table key must be `${densityBand}_${accessBand}` with a recognised access
  // band suffix (local_access_none/_low/_medium/_high — AC-2's four bands).
  {
    const localSplit = loadJson('data/traffic/mode_split_local.json');
    const radius = localSplit.walkAccessRadiusMetres;
    if (typeof radius !== 'number' || !Number.isFinite(radius) || radius <= 0) {
      warn(`mode_split_local.json walkAccessRadiusMetres must be a positive finite number, got ${JSON.stringify(radius)}`);
    }
    const knownBands = new Set(modeShare.bands.map((b) => b.id));
    const mapping = localSplit.landUseDensityMapping || {};
    for (const [specId, band] of Object.entries(mapping)) {
      if (!knownBands.has(band)) {
        warn(`mode_split_local.json landUseDensityMapping['${specId}'] band '${band}' not found in mode_share_by_density.json bands`);
      }
    }
    // BUG-989 (round 2): densityBandMagnitudeThresholds is the TIER-AWARE
    // runtime source (trafficModeSplit.ts's densityBandOf keys off a
    // building's SCALED capacityAtTier magnitude against this step function,
    // not the static per-spec landUseDensityMapping alone) — structural
    // checks: a non-empty array of {band, minMagnitude}, every band a known
    // mode_share_by_density.json band id, minMagnitude a non-negative finite
    // number, and STRICTLY ascending by minMagnitude (the step function is
    // only well-defined if thresholds never repeat or go backwards).
    const thresholds = Array.isArray(localSplit.densityBandMagnitudeThresholds) ? localSplit.densityBandMagnitudeThresholds : [];
    if (thresholds.length === 0) {
      warn('mode_split_local.json densityBandMagnitudeThresholds must be a non-empty array');
    } else {
      let prevMag = -Infinity;
      const seenBands = new Set();
      for (const t of thresholds) {
        if (!t || typeof t.band !== 'string' || !knownBands.has(t.band)) {
          warn(`mode_split_local.json densityBandMagnitudeThresholds has an entry with an unrecognised band '${t && t.band}'`);
        }
        if (t && seenBands.has(t.band)) warn(`mode_split_local.json densityBandMagnitudeThresholds repeats band '${t.band}'`);
        if (t) seenBands.add(t.band);
        const mag = t && t.minMagnitude;
        if (typeof mag !== 'number' || !Number.isFinite(mag) || mag < 0) {
          warn(`mode_split_local.json densityBandMagnitudeThresholds entry for '${t && t.band}' has an invalid minMagnitude ${JSON.stringify(mag)}`);
        } else if (mag <= prevMag) {
          warn(`mode_split_local.json densityBandMagnitudeThresholds is not strictly ascending (band '${t.band}' minMagnitude ${mag} <= previous ${prevMag})`);
        } else {
          prevMag = mag;
        }
      }
    }
    const localModeIds = Array.isArray(localSplit.modeIds) ? localSplit.modeIds : [];
    const localModeIdSet = new Set(localModeIds);
    const knownAccessBands = new Set(['local_access_none', 'local_access_low', 'local_access_medium', 'local_access_high']);
    for (const [key, vector] of Object.entries(localSplit.table || {})) {
      const accessSuffix = [...knownAccessBands].find((a) => key.endsWith(`_${a}`));
      if (!accessSuffix) {
        warn(`mode_split_local.json table key '${key}' does not end with a recognised local-access band`);
      } else {
        const densityBand = key.slice(0, key.length - accessSuffix.length - 1);
        if (!knownBands.has(densityBand)) {
          warn(`mode_split_local.json table key '${key}' density-band prefix '${densityBand}' not found in mode_share_by_density.json bands`);
        }
      }
      const vectorKeys = Object.keys(vector);
      if (vectorKeys.length !== localModeIdSet.size || !vectorKeys.every((k) => localModeIdSet.has(k))) {
        warn(`mode_split_local.json table['${key}'] keys do not exactly match modeIds`);
      }
      const sum = sumOf(vector);
      // BUG-987 (round 2): tightened from 1e-6 to 1e-9 to match the TS
      // loader's own tightened tolerance and trafficModeSplit.test.mjs's
      // AC-4 assertion tolerance — a row summing to 1.000001 (the old bound's
      // limit) would make AC-4's renormalisation a NON-no-op, silently
      // hiding a real drift under the module's own load-time check.
      if (Math.abs(sum - 1) > 1e-9) warn(`mode_split_local.json table['${key}'] sums to ${sum}`);
    }
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
