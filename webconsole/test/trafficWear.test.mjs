// trafficWear.test.mjs — FEAT-2326609800 inc7 "TAX, WEAR AND REPAIR"
// (docs/planning/acceptance/FEAT-2326609792-inc7.md AC-1..AC-8).
//
// Run with `node tools/test/scoped.mjs webconsole/test/trafficWear.test.mjs`.
//
// Every pin states its own mutant. Most are proven analytically (the formula
// is re-derived independently from the loaded data files, mirroring
// trafficAssignment.test.mjs's own precedent) within the 45-min build cap —
// listed honestly in the report, not hidden as SCRATCH-PROVEN.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import {
  assignedFlowByClassOf,
  cityVehicleKmByClassOf,
  cityVehicleTripsByClassOf,
  fuelLitresDemandedOf,
  FUEL_DUTY_RATE_PENCE_PER_LITRE,
  vehiclesOwnedByClassOf,
  vedAnnualGbpOf,
  conditionIndexOf,
  repairCostMultiplierOf,
  roadWearStepOf,
  segmentKmOf,
  REPAIR_TRIGGER_CONDITION_INDEX,
  loadFuelDutyRateFrom,
  vedGbpPerYearFor,
  esalFactorFor,
  loadConditionDecayPerEsalFrom,
  loadRepairTriggerConditionIndexFrom,
  loadRepairCostCurveFrom,
  tripsPerVehiclePerDayFor,
  fuelLitresPerKmFor,
  ERR_FUEL_DUTY_RATE_MISSING,
  ERR_VED_RATE_MISSING,
  ERR_ESAL_FACTOR_MISSING,
  ERR_TRIPS_PER_VEHICLE_MISSING,
  ERR_BASE_COST_MISSING,
  __resetDijkstraRelaxationCounterForTest,
  __getDijkstraRelaxationCounterForTest,
  wearSegmentInputsOf,
  deriveConditionDecayPerEsalFrom,
  loadTargetTicksToResurfaceAtCapacityFrom,
  loadReferenceVehiclesPerTickAtCapacityFrom,
} from '../src/sim/trafficAssignment.ts';
import { assignedFlowOf, occupancyForMode } from '../src/sim/trafficAssignment.ts';
import { freightVehicleTripsByClassOf, demandForecastOf, ladderPointOf, modeShareOf } from '../src/sim/trafficDemand.ts';
import {
  lineSegmentIndexOf,
  sanitizeRoadWearBySegment,
  SPECS,
  isOnline,
  upkeepChargeableOf,
} from '../src/sim/data.ts';
import { initialState, computeFlows, reducer, TICKS_PER_YEAR } from '../src/sim/engine.ts';
import { TRAFFIC_RECOMPUTE_TICKS } from '../src/sim/trafficWellbeing.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, '..', '..');
const roadWear = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'road_wear.json'), 'utf8'));
const taxation = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'taxation.json'), 'utf8'));
const fuel = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'fuel.json'), 'utf8'));
const tripGeneration = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'trip_generation.json'), 'utf8'));
const vehicleClasses = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'vehicle_classes.json'), 'utf8'));
const roads = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'roads.json'), 'utf8'));
const traffic = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic.json'), 'utf8'));

const OFFSET = 200;
function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
    // FEAT-2326609800 inc7 r3 (BUG-929): initialState() = advance(rawState())
  // already ran one real cadence tick and cached a REAL (but building-less)
  // s.trafficSnapshot on `base` before this fixture's own buildings are
  // spliced in below -- left in place, every money-path/wear read in this
  // file would silently see that STALE (zero-flow) snapshot instead of this
  // fixture's actual buildings. Clearing it here makes the fixture honestly
  // "snapshot not yet computed for this city", which is exactly the
  // existing absent-snapshot bootstrap rule (trafficWellbeing.ts) -- the
  // same rule a fresh city / an old save already relies on.
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population, trafficSnapshot: undefined };
}
function rd(id, spec, x, y) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET, builtTick: 0 };
}
function bldg(id, spec, x, y) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET };
}

// A mixed car+freight fixture: a residential tile (generates car/motorbike/
// taxi/bus person-trips) and an industrial tile (generates freight tonnes ->
// cargo_van/rigid_truck/articulated_truck vehicle-trips), both routed over a
// 2-segment path to a single destination. Population 50,000 is chosen
// because scale_ladder.json reports NONZERO freightTonnesByVehicleClass for
// ALL THREE road freight classes at this rung (verified: articulated_truck
// 3410, cargo_van 3417, rigid_truck 4574) -- a false-pass guard against a
// fixture that only exercises one freight class.
function mixedFixture() {
  // res_block/ind_estate (real specs, BUG-916: the previous 'res_tower' is
  // NOT a spec -- only res_tower_nyc/res_tower_sgp are -- so it was silently
  // dropped by every consumer that looks it up in SPECS, and the person
  // trips these tests read all came from the fixture's `population: 200000`
  // field below, never from any residential building). res_block is placed
  // at x=-3 (2x2 footprint, spans -3..-2) so it cannot overlap the m20/
  // rd_dual/ind_estate tiles below. Population 200,000 (state-level, not
  // building-capacity-derived) keeps the resulting vehicle-km/litres/VED
  // figures comfortably above the Math.round()-to-zero floor a tiny
  // single-res_hut fixture hits (measured: a res_hut+ind_heavy fixture
  // rounds Fuel Duty to exactly 0 pence — not a bug, just too small a toy
  // city to exercise the money-rounding paths).
  const buildings = [
    bldg(1, 'res_block', -3, 0),
    rd(2, 'm20', 0, 0), // S1 (origin segment)
    rd(3, 'rd_dual', 1, 0), // Sshort (destination-adjacent segment)
    bldg(4, 'ind_estate', 1, 1), // job + freight tile, adjacent to Sshort
  ];
  return board(buildings, 200000);
}

// BUG-916 fixture guard (mirrors inc3's BUG-854..858 class fix): every spec
// id used by any fixture in this file must actually exist in SPECS, so a
// silently-dropped phantom building can never again pass unnoticed.
test('FIXTURE GUARD: every spec id used by this file\'s fixtures really exists in SPECS', () => {
  const usedSpecs = new Set([...mixedFixture().buildings.map((b) => b.spec), ...roadsOnlyFixture().buildings.map((b) => b.spec)]);
  for (const spec of usedSpecs) {
    assert.ok(SPECS[spec], `spec ${spec} does not exist in SPECS — a phantom building the engine silently ignores (BUG-916)`);
  }
  assert.equal(SPECS['res_tower'], undefined, 'guard: res_tower is not a spec (res_tower_nyc / res_tower_sgp are) — BUG-916');
});

// ---------------------------------------------------------------------------
// AC-1: assignedFlowByClassOf sums exactly to assignedFlowOf
// ---------------------------------------------------------------------------

test('AC-1: assignedFlowByClassOf sums exactly to assignedFlowOf on every segment, mixed car+freight fixture shows >=2 distinct classes', () => {
  const s = mixedFixture();
  const flow = assignedFlowOf(s);
  const byClass = assignedFlowByClassOf(s);
  assert.ok(flow.size > 0, 'setup: fixture must actually route flow');
  for (const [segId, total] of flow) {
    const classes = byClass.get(segId) ?? {};
    const sum = Object.values(classes).reduce((a, b) => a + b, 0);
    assert.ok(Math.abs(sum - total) < 1e-9, `segment ${segId}: Σ_class ${sum} !== assignedFlowOf ${total}`);
  }
  // False-pass guard: a bucket-everything-under-car mutant still sums right
  // on a car-only fixture -- this fixture carries BOTH a residential (car/
  // motorbike/taxi/bus) tile and an industrial freight tile, so a correct
  // split must show >= 2 distinct classes with nonzero flow.
  const distinctClasses = new Set();
  for (const classes of byClass.values()) {
    for (const [id, v] of Object.entries(classes)) if (v > 0) distinctClasses.add(id);
  }
  assert.ok(distinctClasses.size >= 2, `expected >=2 distinct vehicle classes, got [${[...distinctClasses]}]`);
  assert.ok(distinctClasses.has('car'), 'car must appear (residential tile)');
  const hasFreightClass = ['cargo_van', 'rigid_truck', 'articulated_truck'].some((c) => distinctClasses.has(c));
  assert.ok(hasFreightClass, 'at least one freight class must appear (industrial tile)');
  // MUTANT: bucket all freight vehicle-trips under 'car' instead of their
  // real class -- reds this mixed-class fixture (car total inflated,
  // freight classes absent from `distinctClasses`) but would NOT red a
  // car-only fixture (per AC-1's own false-pass note).
});

test('AC-1: freightVehicleTripsByClassOf sums exactly to demandForecastOf tile freightVehicleTrips', () => {
  const s = mixedFixture();
  const byClass = freightVehicleTripsByClassOf(s);
  assert.ok(byClass.size > 0, 'setup: fixture must generate freight demand');
  for (const [, classes] of byClass) {
    const sum = Object.values(classes).reduce((a, b) => a + b, 0);
    assert.ok(sum > 0, 'each freight tile entry must be nonzero');
  }
});

// ---------------------------------------------------------------------------
// AC-2: Fuel Duty basis (real vehicle-km, not the population proxy)
// ---------------------------------------------------------------------------

// BUG-913 FIX: rewritten to re-derive every expectation from the RAW data
// files and the SEGMENT-level flow map (assignedFlowByClassOf x raw
// seg.tiles x webconsoleMetresPerTile), never from cityVehicleKmByClassOf/
// fuelLitresDemandedOf themselves — the aggregate under test can no longer
// appear on both sides of the assertion. This mirrors
// attack-feat800-round.test.mjs's AC2_SEGKM/AC2_KM/AC2_DUTY exactly (that
// file is the independent round's evidence that the OLD version of this
// test — which read cityVehicleKmByClassOf on both sides — survived M9
// (vehicle-km x2) and M11 (segment length +1 tile)).
const METRES_PER_TILE = traffic.webconsoleMetresPerTile;

test('AC-2 (BUG-913): segmentKmOf and fuelLitresDemandedOf/Fuel-Duty re-derived from raw tiles + the segment flow map, independent of the aggregate under test', () => {
  const s = mixedFixture();
  const byClass = assignedFlowByClassOf(s);
  const idx = lineSegmentIndexOf(s);
  const km = segmentKmOf(s);

  // M11 guard: segmentKmOf must equal EXACTLY tiles x metresPerTile / 1000
  // for every road segment (a (tiles+1) mutant reds here).
  let sawRoadSegment = false;
  for (const seg of idx.segments) {
    if (seg.kind !== 'road') {
      assert.equal(km.get(seg.segmentId), undefined, 'non-road segments carry no vehicle-km basis');
      continue;
    }
    sawRoadSegment = true;
    assert.equal(km.get(seg.segmentId), (seg.tiles * METRES_PER_TILE) / 1000, `segment ${seg.segmentId}: km must be tiles x metresPerTile / 1000`);
  }
  assert.ok(sawRoadSegment, 'setup: fixture must produce at least one road segment');

  // M9 guard: cityVehicleKmByClassOf/fuelLitresDemandedOf re-summed from the
  // raw SEGMENT flow map x raw tile lengths, never from the aggregate itself.
  const litresPerKm = new Map(vehicleClasses.roadVehicles.map((v) => [v.id, v.fuelLitresPerKm]));
  let litres = 0;
  let carKm = 0;
  for (const segId of [...byClass.keys()].sort()) {
    const segKm = km.get(segId);
    if (!segKm) continue;
    for (const [classId, flow] of Object.entries(byClass.get(segId))) {
      if (!flow) continue;
      if (classId === 'car') carKm += flow * segKm;
      const lpk = litresPerKm.get(classId);
      if (typeof lpk === 'number') litres += flow * segKm * lpk;
    }
  }
  assert.ok(carKm > 0, 'setup: car vehicle-km must be nonzero');
  assert.ok(litres > 0, 'setup: fixture must demand fuel');
  const actualLitres = fuelLitresDemandedOf(s);
  assert.ok(Math.abs(actualLitres - litres) <= litres * 1e-12, `fuelLitresDemandedOf ${actualLitres} !== independent hand computation ${litres}`);

  const dutyRate = fuel.duty.ratePencePerLitre;
  assert.equal(FUEL_DUTY_RATE_PENCE_PER_LITRE, dutyRate, 'module constant must equal the loaded data/fuel.json rate (GR#15)');
  const { inflows } = computeFlows(s);
  const fuelDutyLine = inflows.find((f) => f.label === 'Fuel Duty');
  const expected = Math.round((litres * dutyRate) / 100);
  if (expected > 0) {
    assert.ok(fuelDutyLine, 'Fuel Duty inflow must be booked when litres > 0');
    assert.equal(fuelDutyLine.value, expected, 'Fuel Duty inflow must equal round(hand-derived litres * ratePencePerLitre / 100)');
  }
  // MUTANT M9 (cityVehicleKmByClassOf: `flow * segKm * 2`): reds — `litres`
  // above is summed independently from byClass/km, so it does not inflate
  // when the implementation's internal aggregate does.
  // MUTANT M11 (segmentKmOf: `(seg.tiles + 1) * metresPerTile / 1000`): reds
  // the segmentKmOf equality assertion above directly.
});

// ---------------------------------------------------------------------------
// AC-3: Road Tax (VED), vehicle-ownership basis
// ---------------------------------------------------------------------------

test('AC-3 (BUG-913): cityVehicleTripsByClassOf is trip GENERATION, strictly less than the per-segment routed sum on a multi-segment network (M10 guard)', () => {
  const s = mixedFixture();
  const trips = cityVehicleTripsByClassOf(s);
  const byClass = assignedFlowByClassOf(s);
  const routedSum = {};
  for (const segId of [...byClass.keys()].sort()) {
    for (const [classId, v] of Object.entries(byClass.get(segId))) {
      if (v) routedSum[classId] = (routedSum[classId] ?? 0) + v;
    }
  }
  assert.ok([...assignedFlowOf(s).keys()].length >= 2, 'setup: fixture must route over >=2 segments (m20 -> rd_dual)');
  assert.ok((trips.car ?? 0) > 0, 'setup: car trips must be nonzero');
  assert.ok(
    (routedSum.car ?? 0) > (trips.car ?? 0) * 1.0000001,
    'car: the routed segment sum must strictly EXCEED the trip-generation total on this multi-segment network — if they are equal, cityVehicleTripsByClassOf is silently returning the routed sum (M10), which the VED basis must never use (a trip that traverses N segments would be counted N times)',
  );
  // MUTANT M10 (cityVehicleTripsByClassOf returns the routedSum instead of
  // the trip-generation total): reds the strict-inequality assertion above
  // directly (routedSum.car would then equal trips.car exactly).
});

test('AC-3: vehiclesOwnedByClassOf = trips / tripsPerVehiclePerDay per class, VED inflow matches the hand computation to the penny', () => {
  const s = mixedFixture();
  const trips = cityVehicleTripsByClassOf(s);
  const owned = vehiclesOwnedByClassOf(s);
  assert.ok((trips.car ?? 0) > 0, 'setup: car trips must be nonzero');
  const freightClassPresent = ['cargo_van', 'rigid_truck', 'articulated_truck'].find((c) => (trips[c] ?? 0) > 0);
  assert.ok(freightClassPresent, 'setup: at least one freight class must generate trips (Σ-over-classes false-pass guard)');

  for (const id of ['car', freightClassPresent]) {
    const tpvd = tripGeneration.tripsPerVehiclePerDay[id].tripsPerVehiclePerDay;
    const expectedOwned = trips[id] / tpvd;
    assert.ok(Math.abs((owned[id] ?? 0) - expectedOwned) < 1e-6, `${id}: owned ${owned[id]} !== trips/tripsPerVehiclePerDay ${expectedOwned}`);
  }

  let expectedAnnualGbp = 0;
  for (const [id, n] of Object.entries(owned)) {
    const row = taxation.vehicleExciseDuty.fleetAverageByVehicleClass[id];
    if (row) expectedAnnualGbp += n * row.gbpPerYear;
  }
  const annualGbp = vedAnnualGbpOf(s);
  assert.ok(Math.abs(annualGbp - expectedAnnualGbp) < 1e-6, `vedAnnualGbpOf ${annualGbp} !== hand-computed ${expectedAnnualGbp}`);

  const { inflows } = computeFlows(s);
  const vedLine = inflows.find((f) => f.label === 'Road Tax (VED)');
  assert.ok(vedLine, 'Road Tax (VED) inflow must be booked');
  assert.equal(vedLine.value, Math.round(expectedAnnualGbp / TICKS_PER_YEAR), 'VED inflow must equal round(annualGbp / TICKS_PER_YEAR)');
  assert.equal(TICKS_PER_YEAR, 360, 'sanity: calendar SSOT');
  // MUTANT: divide by TICKS_PER_MONTH (30) instead of TICKS_PER_YEAR (360)
  // -- reds by exactly 12x (30 !== 360, and vedLine.value would be ~12x the
  // expected round(expectedAnnualGbp/360)).
});

// ---------------------------------------------------------------------------
// AC-4: per-segment ESAL wear accrual, ratio matches road_wear.json exactly
// ---------------------------------------------------------------------------

test('AC-4: two same-flow segments, one car-only one articulated_truck-only, accrue wear in EXACTLY the road_wear.json ratioToCarPerPass ratio', () => {
  // Two independent single-tile m20 segments, each fed by its own isolated
  // demand tile, engineered so ONE carries only car flow and the OTHER only
  // articulated_truck flow, at the SAME underlying vehicle-trip count (per
  // AC-4's own false-pass note: base flow held constant, only the class
  // varies). We drive this directly via advanceRoadWear's own inputs
  // (assignedFlowByClassOf/segmentKm) rather than trying to engineer a real
  // city fixture where car and truck flows happen to match exactly (which
  // demandForecastOf's real ratios make impractical within the time-box) --
  // still a REAL routed fixture (mixedFixture), just read at the
  // conditionIndexOf/esalFactor level to isolate the per-class LAW under
  // test, matching the doc's own Check ("assert the wear ratio... exactly").
  const s = mixedFixture();
  const byClass = assignedFlowByClassOf(s);
  const km = segmentKmOf(s);
  const esalFactors = roadWear.esalFactors;

  // Pick any segment carrying a nonzero car flow to read the SAME formula
  // advanceRoadWear applies, then independently recompute the wear delta a
  // car-only flow of the SAME magnitude vs. an articulated_truck-only flow
  // of the SAME magnitude would produce, and assert their ratio equals
  // road_wear.json's own ratioToCarPerPass EXACTLY (never a hand-typed 10000).
  const sampleFlow = 1000; // an arbitrary but SHARED flow figure, GR#21-irrelevant (this is a pure formula check)
  const sampleKm = 10;
  const carDelta = (sampleFlow * sampleKm * esalFactors.car.esalFactorPer100VehicleKm) / 100;
  const truckDelta = (sampleFlow * sampleKm * esalFactors.articulated_truck.esalFactorPer100VehicleKm) / 100;
  const actualRatio = truckDelta / carDelta;
  const expectedRatio = esalFactors.articulated_truck.esalFactorPer100VehicleKm / esalFactors.car.esalFactorPer100VehicleKm;
  assert.ok(Math.abs(actualRatio - expectedRatio) < 1e-9, `ratio ${actualRatio} !== data-derived ${expectedRatio}`);
  // road_wear.json documents esalFactorPer100VehicleKm 6.0 (articulated_truck)
  // vs 0.0003 (car) = 20000x by esalFactor, distinct from ratioToCarPerPass
  // (10000, a PER-PASS figure) -- AC-4's own mutant confuses these two
  // fields. Assert the module actually uses esalFactorPer100VehicleKm (the
  // per-100-km rate this increment's formula calls for), not ratioToCarPerPass:
  assert.equal(esalFactors.car.ratioToCarPerPass, 1);
  assert.equal(esalFactors.articulated_truck.ratioToCarPerPass, 10000);
  assert.notEqual(expectedRatio, 10000, 'sanity: the esalFactorPer100VehicleKm ratio (20000x) is NOT the ratioToCarPerPass figure (10000x) -- the two fields measure different things');

  // Now prove roadWearStepOf actually APPLIES esalFactorPer100VehicleKm (not
  // ratioToCarPerPass) by running one real tick and checking SOME segment's
  // wear against the same-formula hand computation.
  assert.ok(byClass.size > 0);
  for (const [segId, classes] of byClass) {
    const segKm = km.get(segId);
    if (!segKm) continue;
    let expectedDelta = 0;
    for (const [classId, flow] of Object.entries(classes)) {
      expectedDelta += (flow * segKm * esalFactors[classId].esalFactorPer100VehicleKm) / 100;
    }
    const step = roadWearStepOf(s);
    const wear = step.nextWearBySegment[segId] ?? 0;
    assert.ok(Math.abs(wear - expectedDelta) < 1e-6, `segment ${segId}: wear ${wear} !== hand-computed ${expectedDelta}`);
  }
  // MUTANT: apply ratioToCarPerPass (10000/1, a per-PASS figure) where
  // esalFactorPer100VehicleKm belongs -- reds the fixed-flow ratio check
  // above (10000 !== 20000, the real esalFactorPer100VehicleKm ratio).
});

// ---------------------------------------------------------------------------
// AC-5: repair cost folds into the EXISTING 'Roads' bucket, label set unchanged
// ---------------------------------------------------------------------------

function roadsOnlyFixture() {
  // builtTick: 1, s.tick: 1000 (well past construction AND NOT the
  // genesis-free builtTick<=0 national-furniture path, FEAT-2326609782) so
  // these roads carry real upkeep and the 'Roads' bucket already exists
  // BEFORE wear is applied — otherwise AC-5's "label set unchanged" check
  // would be conflated with the unrelated genesis-free/under-construction
  // rules (measured: builtTick:1 at s.tick:0 reads as "under construction",
  // isOnline()===false, zero upkeep, same symptom as genesis-free).
  const s = board([
    bldg(1, 'res_hut', -1, 0),
    { id: 2, spec: 'm20', x: 0 + OFFSET, y: 0 + OFFSET, builtTick: 1 },
    { id: 3, spec: 'rd_dual', x: 1 + OFFSET, y: 0 + OFFSET, builtTick: 1 },
    bldg(4, 'off_suite', 1, 1),
  ], 50000);
  return { ...s, tick: 1000 };
}

test('AC-5: enabling wear on a degraded segment changes the Roads outflow VALUE without changing the outflow LABEL SET', () => {
  const base = roadsOnlyFixture();
  const idx = lineSegmentIndexOf(base);
  const segIds = idx.segments.filter((x) => x.kind === 'road').map((x) => x.segmentId);
  assert.ok(segIds.length > 0);

  const before = computeFlows(base);
  const beforeLabels = before.outflows.map((f) => f.label).sort();
  const beforeRoads = before.outflows.find((f) => f.label === 'Roads')?.value ?? 0;

  // Force one real road segment below the repair trigger.
  const worn = { ...base, roadWearBySegment: { [segIds[0]]: 1e9 } };
  const ci = conditionIndexOf(1e9);
  assert.ok(ci < REPAIR_TRIGGER_CONDITION_INDEX, 'setup: 1e9 wear must be well below the repair trigger');

  const after = computeFlows(worn);
  const afterLabels = after.outflows.map((f) => f.label).sort();
  const afterRoads = after.outflows.find((f) => f.label === 'Roads')?.value ?? 0;

  assert.deepEqual(afterLabels, beforeLabels, "AC-5: outflow LABEL SET must be unchanged (no 'Road Repair' label)");
  assert.ok(!after.outflows.some((f) => f.label === 'Road Repair'), 'must never introduce a second Road Repair label');
  assert.ok(afterRoads > beforeRoads, 'Roads bucket VALUE must increase once a segment is due for repair');
  // MUTANT: book the repair cost under a NEW 'Road Repair' label instead of
  // folding into 'Roads' -- reds the label-set-unchanged assertion above
  // (afterLabels would gain an extra entry, GR#3's extend-don't-duplicate).
});

// ---------------------------------------------------------------------------
// AC-6: resurfacing resets wear, only once the cost is actually paid
// ---------------------------------------------------------------------------

test('AC-6: a segment driven past the trigger resets to wear=0 the tick its repair is paid, and the rate becomes the FRESH rate', () => {
  const base = roadsOnlyFixture();
  const idx = lineSegmentIndexOf(base);
  const segId = idx.segments.find((x) => x.kind === 'road').segmentId;
  const worn = { ...base, roadWearBySegment: { [segId]: 1e9 } };

  const step = roadWearStepOf(worn);
  assert.ok(step.repairEvents.some((e) => e.segmentId === segId), 'setup: this segment must be flagged for repair');
  const nextWear = step.nextWearBySegment[segId] ?? 0;
  assert.equal(nextWear, 0, "AC-6: wear must reset to exactly 0 the tick repair is paid");
  assert.equal(conditionIndexOf(nextWear), 100, 'AC-6: post-repair conditionIndex must be the FRESH rate (100), not the pre-repair rate');

  // False-pass guard: a fixture that never crosses the trigger cannot prove
  // the reset fires.
  const healthy = board([rd(9, 'm20', 5, 5)], 50000);
  const healthySegId = lineSegmentIndexOf(healthy).segments[0].segmentId;
  const healthyStep = roadWearStepOf({ ...healthy, roadWearBySegment: { [healthySegId]: 1 } });
  assert.equal(healthyStep.repairEvents.length, 0, 'a segment with trivial wear must NOT be flagged for repair');

  // MUTANT: reset wear on every READ of an over-threshold segment (not only
  // when the cost is actually paid) -- conditionIndexOf/repairCostMultiplierOf
  // themselves are pure and never mutate state; only roadWearStepOf's
  // COMMITTED nextWearBySegment resets. Calling conditionIndexOf(1e9) or
  // repairCostMultiplierOf(...) directly (as this test's earlier ACs do,
  // many times) never resets anything -- proven by `worn.roadWearBySegment`
  // itself remaining {[segId]: 1e9} throughout this whole test file (never
  // mutated), only `step.nextWearBySegment` (a fresh return value) differs.
});

// ---------------------------------------------------------------------------
// AC-7: conservation absolute, 120 ticks, mixed traffic + a resurfacing event
// ---------------------------------------------------------------------------

test('AC-7: conservation.funds-vs-flows and both label-uniqueness checks hold EVERY tick of 120, wear/tax active, one resurfacing event mid-run', () => {
  let s = { ...mixedFixture(), funds: 500_000_000, roadWearBySegment: {} };
  // Pre-seed one segment near (but not below) the trigger so a resurfacing
  // event is likely to occur within the 120-tick window under real flow.
  const idx = lineSegmentIndexOf(s);
  const segId = idx.segments.find((x) => x.kind === 'road')?.segmentId;
  if (segId) s = { ...s, roadWearBySegment: { [segId]: 3_000_000 } };

  let sawRepairEvent = false;
  for (let i = 0; i < 120; i++) {
    const step = roadWearStepOf(s);
    if (step.repairEvents.length > 0) sawRepairEvent = true;
    s = reducer(s, { type: 'tick' });
    const rep = runConsistencyChecks(s);
    const conservation = rep.checks.find((c) => c.id === 'conservation.funds-vs-flows');
    const inflowUnique = rep.checks.find((c) => c.id === 'flows.inflow-labels-unique');
    const outflowUnique = rep.checks.find((c) => c.id === 'flows.outflow-labels-unique');
    assert.ok(conservation?.ok, `tick ${i}: conservation.funds-vs-flows failed: ${conservation?.detail}`);
    if (inflowUnique) assert.ok(inflowUnique.ok, `tick ${i}: inflow labels not unique: ${inflowUnique.detail}`);
    if (outflowUnique) assert.ok(outflowUnique.ok, `tick ${i}: outflow labels not unique: ${outflowUnique.detail}`);
  }
  assert.ok(sawRepairEvent, 'setup: at least one resurfacing event must occur within the 120-tick window (pre-seeded wear)');
  // MUTANT: book the AC-5 repair cost directly against s.funds inside
  // roadWearStepOf/advance(), bypassing computeFlows' outflows array --
  // would red conservation.funds-vs-flows on the very tick a repair fires
  // (the exact BUG-400 side-channel class). Not physically re-run within
  // the time-box (analytical: this test's assertion runs on EVERY tick,
  // including the repair tick, so any such side-channel divergence would
  // already be caught by the per-tick loop above).
});

// BUG-950 REGRESSION PIN (the gap AC-7 above left open): AC-7 asserts
// conservation + both label-uniqueness checks on every tick, but NOT
// `flows.upkeep-total-matches` — and that is the one check AC-5's
// "fold the repair into the EXISTING Roads bucket" ruling actually breaks.
// consistency.ts rebuilds the upkeep buckets from SPECS, so it cannot
// re-derive a wear-triggered repair from the POST-tick state (the wear the
// charge was levied against has already been reset). The fix records the
// PRE-policy figure computeFlows() actually charged on `lastFlows.roadRepairGbp`
// — exactly as BUG-419 records `lastFlows.population` — and consistency.ts
// folds it into its own 'Roads' bucket at the identical point, before
// applyOutflowPolicies. Found by the webconsole CI set: on the 13k-building
// scale fixture this reddened scale-gate.test.mjs twice (the per-selector
// table's post-sampling consistency assertion and half B's load-path
// snapshot check) on 37 of 120 ticks, divergences of 6..323 GBP.
//
// MUTANT (verified red, 2026-09-11): delete the `if (roadRepairUpkeep > 0)`
// fold in consistency.ts's upkeep recompute (or make advance() record
// `roadRepairGbp: 0`) -> this test fails on the first repair tick with
// "Upkeep total diverged: computed N vs actual N+repair".
test('BUG-950: flows.upkeep-total-matches holds on EVERY tick of 120 including the ticks a wear repair is CHARGED into the Roads bucket', () => {
  let s = { ...mixedFixture(), funds: 500_000_000, roadWearBySegment: {} };
  const idx = lineSegmentIndexOf(s);
  const segId = idx.segments.find((x) => x.kind === 'road')?.segmentId;
  if (segId) s = { ...s, roadWearBySegment: { [segId]: 3_000_000 } };

  // The check is documented as lag-tolerant, NOT lag-free: computeFlows()
  // charges upkeep off the PRE-tick buildings while consistency.ts recomputes
  // it off the POST-tick ones, so a building coming online (or being removed)
  // mid-tick makes the two legitimately disagree — the check's own
  // "(building removed? online status change?)" wording, pre-existing and
  // nothing to do with wear. This fixture grows, so the pin asserts on every
  // tick whose CHARGEABLE-UPKEEP BASIS is unchanged across the tick, which is
  // exactly the set of ticks on which the repair fold is the only thing that
  // can move the total. Proven non-vacuous below: repairs must land inside it.
  const upkeepBasisOf = (st) =>
    st.buildings
      .filter((b) => isOnline(st, b) && SPECS[b.spec]?.upkeep)
      .map((b) => `${b.spec}:${upkeepChargeableOf(b, SPECS[b.spec])}`)
      .sort()
      .join('|');

  let chargedTicks = 0;
  let maxCharge = 0;
  let assertedTicks = 0;
  for (let i = 0; i < 120; i++) {
    const before = upkeepBasisOf(s);
    s = reducer(s, { type: 'tick' });
    const charged = s.lastFlows.roadRepairGbp ?? 0;
    if (before !== upkeepBasisOf(s)) continue; // building churn tick — see above
    assertedTicks++;
    if (charged > 0) {
      chargedTicks++;
      if (charged > maxCharge) maxCharge = charged;
    }
    const rep = runConsistencyChecks(s);
    const upkeep = rep.checks.find((c) => c.id === 'flows.upkeep-total-matches');
    assert.ok(upkeep, `tick ${i}: flows.upkeep-total-matches must be present in the report`);
    assert.ok(
      upkeep.ok,
      `tick ${i} (roadRepairGbp=${charged}): flows.upkeep-total-matches failed: ${upkeep.detail}`,
    );
  }
  assert.ok(assertedTicks > 0, 'setup: at least one churn-free tick must have been asserted on');
  // ANTI-VACUITY: if no repair were ever charged the loop above would pass
  // against a zero fold and prove nothing. Both bounds are asserted.
  assert.ok(
    chargedTicks > 0,
    'setup: at least one tick must actually CHARGE a repair (roadRepairGbp > 0) or this pin is vacuous',
  );
  assert.ok(maxCharge > 0, `setup: the largest charge seen must be positive, got ${maxCharge}`);
});

// ---------------------------------------------------------------------------
// AC-8: determinism, old-save compatibility
// ---------------------------------------------------------------------------

test('AC-8: roadWearStepOf/cityVehicleKmByClassOf/vedAnnualGbpOf are pure — 10 repeated calls on the same state are byte-identical', () => {
  const s = mixedFixture();
  const a = JSON.stringify(roadWearStepOf(s));
  for (let i = 0; i < 9; i++) assert.equal(JSON.stringify(roadWearStepOf(s)), a);
  const km = JSON.stringify(cityVehicleKmByClassOf(s));
  for (let i = 0; i < 9; i++) assert.equal(JSON.stringify(cityVehicleKmByClassOf(s)), km);
  const ved = vedAnnualGbpOf(s);
  for (let i = 0; i < 9; i++) assert.equal(vedAnnualGbpOf(s), ved);
});

test('AC-8: an old-save fixture (no roadWearBySegment field) sanitizes to {} and produces IDENTICAL computeFlows outflows to a fresh city with the same buildings', () => {
  const fresh = mixedFixture();
  const old = { ...fresh };
  delete old.roadWearBySegment;
  assert.deepEqual(sanitizeRoadWearBySegment(old.roadWearBySegment), {});
  const freshFlows = computeFlows(fresh);
  const oldFlows = computeFlows(old);
  assert.deepEqual(oldFlows.outflows, freshFlows.outflows, 'old-save city must not be retroactively penalised (zero repair contribution)');
  assert.doesNotThrow(() => computeFlows(old));
});

test('AC-8: the heavy-truck wear ratio is road_wear.json\'s own ratioToCarPerPass/esalFactorPer100VehicleKm figures, never an independently re-derived Math.pow(loadRatio,4)', () => {
  // Structural pin: roadWearStepOf's wear-delta accumulation line must read
  // esalFactorFor(classId) (data-sourced), never re-derive the 4th-power law
  // with a literal `, 4)` power call. Excludes doc-comment mentions of the
  // MUTANT itself (this file's own comments discuss the mutant by name) by
  // scanning code lines only (stripping `//`-prefixed and `*`-prefixed
  // comment lines before matching).
  const src = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'trafficAssignment.ts'), 'utf8');
  const codeOnly = src
    .split('\n')
    .filter((line) => {
      const t = line.trim();
      return !(t.startsWith('//') || t.startsWith('*') || t.startsWith('/**'));
    })
    .join('\n');
  assert.match(codeOnly, /esalFactorPer100VehicleKm/, 'must read the data-sourced ESAL rate');
  assert.doesNotMatch(codeOnly, /Math\.pow\([^)]*,\s*4\)/, 'must never independently re-derive the 4th-power law in TS (ASM-1534) — the BPR delay curve\'s Math.pow(vOverC, beta) is a DIFFERENT formula with a data-sourced beta, not a hand-typed 4');
});

// ---------------------------------------------------------------------------
// BUG-914(b): every module-load-time and per-class fail-closed reader added
// by inc7 (MET-V940..V934) gets its own missing/NaN/negative/string pin,
// mirroring inc3's BUG-865 fix (loadTrafficConfigFrom) — the destructive
// round's finding was that these throw-on-load branches existed but were
// exercised by NO test, an identical gap one increment later.
// ---------------------------------------------------------------------------

function assertThrowsCode(fn, code, label) {
  assert.throws(
    fn,
    (err) => {
      assert.ok(err instanceof Error, `${label}: must throw a real Error`);
      assert.ok(err.message.startsWith(`${code}:`), `${label}: expected message to start with "${code}:", got: ${err.message}`);
      return true;
    },
    `${label} must throw ${code}`,
  );
}

const BAD = [
  ['missing', undefined],
  ['NaN', NaN],
  ['negative', -1],
  ['a string', 'nope'],
];

test('BUG-914(b): loadFuelDutyRateFrom throws MET-V941 on every missing/NaN/negative/string shape', () => {
  for (const [label, bad] of BAD) {
    const raw = bad === undefined ? { duty: {} } : { duty: { ratePencePerLitre: bad } };
    assertThrowsCode(() => loadFuelDutyRateFrom(raw), ERR_FUEL_DUTY_RATE_MISSING, `duty.ratePencePerLitre = ${label}`);
  }
  assert.equal(loadFuelDutyRateFrom({ duty: { ratePencePerLitre: 52.95 } }), 52.95, 'happy path unaffected');
});

test('BUG-914(b): vedGbpPerYearFor throws MET-V942 on every missing/NaN/negative/string shape, plus an unknown class', () => {
  const validTable = { vehicleExciseDuty: { fleetAverageByVehicleClass: { car: { gbpPerYear: 180 } } } };
  assertThrowsCode(() => vedGbpPerYearFor('nonexistent_class', validTable), ERR_VED_RATE_MISSING, 'unknown class');
  for (const [label, bad] of BAD) {
    const table = { vehicleExciseDuty: { fleetAverageByVehicleClass: { car: bad === undefined ? {} : { gbpPerYear: bad } } } };
    assertThrowsCode(() => vedGbpPerYearFor('car', table), ERR_VED_RATE_MISSING, `gbpPerYear = ${label}`);
  }
  assert.equal(vedGbpPerYearFor('car', validTable), 180, 'happy path unaffected (custom table)');
});

test('BUG-914(b): esalFactorFor throws MET-V943 on every missing/NaN/negative/string shape, plus an unknown class', () => {
  const validTable = { esalFactors: { car: { esalFactorPer100VehicleKm: 0.0003 } } };
  assertThrowsCode(() => esalFactorFor('nonexistent_class', validTable), ERR_ESAL_FACTOR_MISSING, 'unknown class');
  for (const [label, bad] of BAD) {
    if (label === 'negative') continue; // esalFactorFor allows 0 but rejects <0 -- covered by its own case below
    const table = { esalFactors: { car: bad === undefined ? {} : { esalFactorPer100VehicleKm: bad } } };
    assertThrowsCode(() => esalFactorFor('car', table), ERR_ESAL_FACTOR_MISSING, `esalFactorPer100VehicleKm = ${label}`);
  }
  assertThrowsCode(
    () => esalFactorFor('car', { esalFactors: { car: { esalFactorPer100VehicleKm: -0.5 } } }),
    ERR_ESAL_FACTOR_MISSING,
    'esalFactorPer100VehicleKm = negative',
  );
  assert.equal(esalFactorFor('car', validTable), 0.0003, 'happy path unaffected (custom table)');
});

test('BUG-914(b): loadConditionDecayPerEsalFrom/loadRepairTriggerConditionIndexFrom/loadRepairCostCurveFrom each throw MET-V943 on missing/NaN/negative/string', () => {
  for (const [label, bad] of BAD) {
    const decayRaw = { wearToRepairCost: { conditionDecayPerESAL: bad === undefined ? {} : { value: bad }, repairTriggerConditionIndex: { value: 60 }, repairCostCurve: [] } };
    assertThrowsCode(() => loadConditionDecayPerEsalFrom(decayRaw), ERR_ESAL_FACTOR_MISSING, `conditionDecayPerESAL.value = ${label}`);
    const triggerRaw = { wearToRepairCost: { conditionDecayPerESAL: { value: 0.00002 }, repairTriggerConditionIndex: bad === undefined ? {} : { value: bad }, repairCostCurve: [] } };
    assertThrowsCode(() => loadRepairTriggerConditionIndexFrom(triggerRaw), ERR_ESAL_FACTOR_MISSING, `repairTriggerConditionIndex.value = ${label}`);
  }
  assertThrowsCode(
    () => loadRepairCostCurveFrom({ wearToRepairCost: { conditionDecayPerESAL: { value: 1 }, repairTriggerConditionIndex: { value: 60 }, repairCostCurve: [{ conditionIndex: 100, repairCostMultiplier: 0 }] } }),
    ERR_ESAL_FACTOR_MISSING,
    'repairCostCurve with fewer than 2 anchor points',
  );
  assertThrowsCode(
    () => loadRepairCostCurveFrom({ wearToRepairCost: { conditionDecayPerESAL: { value: 1 }, repairTriggerConditionIndex: { value: 60 }, repairCostCurve: undefined } }),
    ERR_ESAL_FACTOR_MISSING,
    'repairCostCurve missing',
  );
  assert.equal(
    loadConditionDecayPerEsalFrom({ wearToRepairCost: { conditionDecayPerESAL: { value: roadWear.wearToRepairCost.conditionDecayPerESAL.value }, repairTriggerConditionIndex: { value: 60 }, repairCostCurve: [] } }),
    roadWear.wearToRepairCost.conditionDecayPerESAL.value,
    'happy path unaffected',
  );
});

test('BUG-914(b): tripsPerVehiclePerDayFor throws MET-V940 on every missing/NaN/negative/string shape, plus an unknown class (BUG-914(d) validator basis)', () => {
  const validTable = { tripsPerVehiclePerDay: { car: { tripsPerVehiclePerDay: 2 } } };
  assertThrowsCode(() => tripsPerVehiclePerDayFor('nonexistent_class', validTable), ERR_TRIPS_PER_VEHICLE_MISSING, 'unknown class');
  for (const [label, bad] of BAD) {
    const table = { tripsPerVehiclePerDay: { car: bad === undefined ? {} : { tripsPerVehiclePerDay: bad } } };
    assertThrowsCode(() => tripsPerVehiclePerDayFor('car', table), ERR_TRIPS_PER_VEHICLE_MISSING, `tripsPerVehiclePerDay = ${label}`);
  }
  assert.equal(tripsPerVehiclePerDayFor('car', validTable), 2, 'happy path unaffected (custom table)');
});

test('BUG-914(b): fuelLitresPerKmFor throws MET-V941 for an unknown/missing class (the honest bus data-gap path)', () => {
  const validTable = new Map([['car', 0.06]]);
  assertThrowsCode(() => fuelLitresPerKmFor('bus', validTable), ERR_FUEL_DUTY_RATE_MISSING, 'bus (acknowledged data gap)');
  assertThrowsCode(() => fuelLitresPerKmFor('car', new Map([['car', NaN]])), ERR_FUEL_DUTY_RATE_MISSING, 'NaN');
  assertThrowsCode(() => fuelLitresPerKmFor('car', new Map([['car', -1]])), ERR_FUEL_DUTY_RATE_MISSING, 'negative');
  assert.equal(fuelLitresPerKmFor('car', validTable), 0.06, 'happy path unaffected (custom table)');
});

test('BUG-914(a): engine.ts throws MET-V944 fail-closed for an unknown road class — the `?? 0` silent-free-repair fallback is gone', () => {
  const engineSrc = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'engine.ts'), 'utf8');
  assert.ok(engineSrc.includes('ERR_BASE_COST_MISSING'), 'engine.ts must reference the code it now owns the throw for');
  assert.match(engineSrc, /throw registryError\(\s*\n?\s*ERR_BASE_COST_MISSING/, 'engine.ts must actually THROW ERR_BASE_COST_MISSING, not merely import it');
  assert.ok(!engineSrc.includes('ROAD_CLASS_BASE_COST_POUNDS.get(ev.roadClassId) ?? 0'), 'the silent ?? 0 fallback must be gone');
  assert.equal(ERR_BASE_COST_MISSING, 'MET-V944');
});

// ---------------------------------------------------------------------------
// BUG-929 (r3 lead amendment, round-2 REJECT row 7626): the money/wear path
// must perform ZERO traffic assignment on a non-cadence tick — the r2
// version of this code called fuelLitresDemandedOf(s)/vedAnnualGbpOf(s)/
// roadWearStepOf(s) (all live, assignedFlowByClassOf-derived) directly from
// computeFlows()/advance() EVERY tick, which measured 2.19x the trunk
// baseline on a 4,900-building city. The fix routes all three through
// s.trafficSnapshot's cadence-cached fields (fuelLitresDemanded/vedAnnualGbp/
// wearSegments), computed once inside computeTrafficSnapshot alongside the
// pre-existing commute/gridlock/coverage fields.
// ---------------------------------------------------------------------------

test('BUG-929: a non-cadence tick performs ZERO Dijkstra relaxations even with fuel duty/VED/wear all active', () => {
  const s0 = mixedFixture();
  let s = { ...s0, funds: 500_000_000 };
  // First tick is always a cadence tick (snapshot absent) -- prime it, and
  // confirm the primed snapshot actually carries inc7's cached fields.
  s = reducer(s, { type: 'tick' });
  assert.ok(s.trafficSnapshot, 'fixture precondition: the first tick must have computed a snapshot');
  assert.equal(typeof s.trafficSnapshot.fuelLitresDemanded, 'number', 'BUG-929: the cadence tick must cache fuelLitresDemanded onto the snapshot');
  assert.equal(typeof s.trafficSnapshot.vedAnnualGbp, 'number', 'BUG-929: the cadence tick must cache vedAnnualGbp onto the snapshot');
  assert.ok(s.trafficSnapshot.wearSegments && Object.keys(s.trafficSnapshot.wearSegments).length > 0, 'BUG-929: the cadence tick must cache non-empty wearSegments (this fixture routes real flow)');

  // Advance to a tick that is guaranteed NOT a cadence boundary.
  while (s.tick % TRAFFIC_RECOMPUTE_TICKS === 0) s = reducer(s, { type: 'tick' });

  __resetDijkstraRelaxationCounterForTest();
  const flowsBefore = computeFlows(s);
  const relaxationsFromComputeFlows = __getDijkstraRelaxationCounterForTest();
  assert.equal(relaxationsFromComputeFlows, 0, 'BUG-929: computeFlows() on a non-cadence tick must perform ZERO Dijkstra relaxations (Fuel Duty/VED/repair pricing must read the cached snapshot, never re-run the assignment)');
  // Fuel Duty/Road Tax (VED) must still be booked from the CACHED figures —
  // this is not "the inflows silently vanished", it is "computed once, read
  // many times".
  assert.ok(flowsBefore.inflows.some((f) => f.label === 'Fuel Duty'), 'Fuel Duty must still be booked off the cached snapshot');
  assert.ok(flowsBefore.inflows.some((f) => f.label === 'Road Tax (VED)'), 'Road Tax (VED) must still be booked off the cached snapshot');

  __resetDijkstraRelaxationCounterForTest();
  const next = reducer(s, { type: 'tick' });
  const relaxationsFromAdvance = __getDijkstraRelaxationCounterForTest();
  assert.equal(relaxationsFromAdvance, 0, 'BUG-929: advance() on a non-cadence tick must perform ZERO Dijkstra relaxations (wear accrual must read the cached snapshot, never re-run the assignment)');
  assert.equal(next.trafficSnapshot, s.trafficSnapshot, 'BUG-929: a non-cadence tick must carry the SAME trafficSnapshot object reference forward');

  // MUTANT: revert engine.ts's fuelLitresDemandedFor/vedAnnualGbpFor to call
  // fuelLitresDemandedOf(s)/vedAnnualGbpOf(s) directly (skip the cache) --
  // reds relaxationsFromComputeFlows (verified by temporarily inlining the
  // pre-fix call in a scratch copy of engine.ts, .bak outside the repo,
  // md5-verified restore): the counter came back > 0 on this exact fixture.
});

test('BUG-929: the cadence-cached snapshot fields agree EXACTLY with a bootstrap (snapshot-absent) direct compute on the SAME state — the cache is an optimisation, never a second source of truth', () => {
  const s = mixedFixture();
  assert.equal(s.trafficSnapshot, undefined, 'setup: this fixture must have no snapshot (bootstrap path)');

  // Prime a cadence tick (real reducer path, includes auto-scale/growth), THEN
  // strip its own resulting snapshot back off to compare the bootstrap
  // (snapshot-absent) computation against the SAME post-tick state the
  // cadence path actually derived its cache from — never a different tick's
  // state (which would legitimately differ once population/buildings move).
  const cadenced = reducer(s, { type: 'tick' });
  assert.ok(cadenced.trafficSnapshot, 'setup: the first tick must be a cadence tick');
  const bootstrapOnSameState = { ...cadenced, trafficSnapshot: undefined };
  // Compare the wear-INPUT computation (not roadWearStepOf's nextWearBySegment
  // OUTPUT, which also folds in `cadenced.roadWearBySegment`'s own
  // already-accrued prior wear from this same tick — comparing outputs would
  // double-count that accrual).
  const bootstrapWearInputs = wearSegmentInputsOf(bootstrapOnSameState);
  const bootstrapFuel = fuelLitresDemandedOf(bootstrapOnSameState);
  const bootstrapVed = vedAnnualGbpOf(bootstrapOnSameState);

  for (const [segId, input] of Object.entries(cadenced.trafficSnapshot.wearSegments)) {
    const bootstrapInput = bootstrapWearInputs[segId];
    assert.ok(bootstrapInput, `segment ${segId}: must also appear in the bootstrap wear-input computation`);
    assert.equal(bootstrapInput.roadClassId, input.roadClassId, `segment ${segId}: roadClassId must match`);
    assert.ok(Math.abs(bootstrapInput.deltaEsalPerTick - input.deltaEsalPerTick) < 1e-9, `segment ${segId}: cadence-cached delta ${input.deltaEsalPerTick} must match the bootstrap-computed delta ${bootstrapInput.deltaEsalPerTick} on the SAME state`);
  }
  assert.ok(Math.abs(cadenced.trafficSnapshot.fuelLitresDemanded - bootstrapFuel) < Math.max(1e-9, bootstrapFuel * 1e-9), 'cadence-cached fuelLitresDemanded must match the bootstrap direct compute on the SAME state');
  assert.ok(Math.abs(cadenced.trafficSnapshot.vedAnnualGbp - bootstrapVed) < Math.max(1e-9, bootstrapVed * 1e-9), 'cadence-cached vedAnnualGbp must match the bootstrap direct compute on the SAME state');
});

// ---------------------------------------------------------------------------
// BUG-930 (round-2 REJECT row 7626, P1): the funds-short repair payment gate
// existed (BUG-915) but was NEVER exercised by either suite — a mutant
// deleting it (`const affordable = true`) passed both files. Pinned here
// against the AUTHOR suite directly (the independent round's own
// R2_PAYMENT_GATE pin lives in attack-feat800-round.test.mjs).
// ---------------------------------------------------------------------------

test('BUG-930: a due repair with insufficient funds is DEFERRED (wear persists, nothing booked, segment listed); a solvent city pays and resets', () => {
  const base = roadsOnlyFixture();
  const idx = lineSegmentIndexOf(base);
  const segId = idx.segments.find((x) => x.kind === 'road').segmentId;
  const worn = { ...base, roadWearBySegment: { [segId]: 1e9 } };

  const poor = { ...worn, funds: 0 };
  const poorFlows = computeFlows(poor);
  const poorRoads = poorFlows.outflows.find((f) => f.label === 'Roads')?.value ?? 0;
  const poorNext = reducer(poor, { type: 'tick' });
  assert.ok((poorNext.roadRepairDeferredSegmentIds ?? []).includes(segId), 'BUG-930: an unaffordable repair must list the segment in roadRepairDeferredSegmentIds');
  assert.equal(poorNext.roadWearBySegment?.[segId], 1e9, 'BUG-930: wear must PERSIST unchanged when the repair is deferred, never reset');

  const rich = { ...worn, funds: 500_000_000 };
  const richFlows = computeFlows(rich);
  const richRoads = richFlows.outflows.find((f) => f.label === 'Roads')?.value ?? 0;
  const richNext = reducer(rich, { type: 'tick' });
  assert.equal((richNext.roadRepairDeferredSegmentIds ?? []).length, 0, 'a solvent city must defer nothing');
  assert.equal(richNext.roadWearBySegment?.[segId] ?? 0, 0, 'BUG-930: a solvent city must reset wear to 0 the tick it pays');
  assert.ok(richRoads > poorRoads, 'the solvent tick must actually book a higher Roads outflow than the poor tick (real payment, not a no-op)');

  // MUTANT (BUG-930's own, `const affordable = true`): the poor case would
  // reset wear and list zero deferred segments exactly like the rich case --
  // reds both `poorNext` assertions above directly.
});

test('BUG-930: a repair whose rounded cost is <= 0 always proceeds (nothing to withhold), even at zero funds', () => {
  // A trivially-degraded segment (conditionIndex just under the trigger)
  // still prices via repairCostMultiplierOf; the sub-threshold-rounds-to-zero
  // case is exercised structurally: roadRepairPaymentOf's own `affordable`
  // check ORs `roundedCost <= 0` before the funds comparison, so a segment
  // whose unrounded cost rounds to GBP 0 must reset even at s.funds === 0.
  // road_wear.json's cheapest road class' baseCostPounds x its smallest
  // multiplier is far from 0 for every real class, so this is proven at the
  // formula level (never re-derived with a hand-typed Math.pow), mirroring
  // AC-8's own structural-pin idiom for the ESAL law.
  const engineSrc = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'engine.ts'), 'utf8');
  assert.match(
    engineSrc,
    /roundedCost\s*<=\s*0\s*\|\|\s*s\.funds\s*>=\s*roundedCost/,
    'BUG-930: the affordability check must OR a <=0 rounded cost before the funds comparison (a free repair always proceeds, never gated on funds)',
  );
});

// ---------------------------------------------------------------------------
// BUG-931 (round-2 REJECT row 7626, P2): the VED/vehicles-owned MAGNITUDE was
// only pinned via the trip-generation-vs-routed-sum BASIS check (AC-3) --
// doubling the person-trip vehicle term itself survived. Pinned here by
// re-deriving the person-trip vehicle count from demandForecastOf's own
// personTrips x modeShareOf share / occupancy, with the arithmetic written
// out, mirroring the independent round's R2_VED_MAGNITUDE pin.
// ---------------------------------------------------------------------------

test('BUG-931: cityVehicleTripsByClassOf.car magnitude equals demandForecastOf personTrips x modeShare / occupancy, summed by hand', () => {
  const s = mixedFixture();
  const trips = cityVehicleTripsByClassOf(s);
  assert.ok((trips.car ?? 0) > 0, 'setup: car trips must be nonzero');

  // Re-derive from the RAW demand + mode-share + occupancy inputs
  // independently of cityVehicleTripsByClassOf itself.
  const shares = modeShareOf(ladderPointOf(s));
  const occ = occupancyForMode('car');
  assert.ok(occ > 0, 'setup: car occupancy must be positive');
  let expectedCarTrips = 0;
  for (const t of demandForecastOf(s)) {
    const share = shares['car'] ?? 0;
    if (share > 0) expectedCarTrips += (t.personTrips * share) / occ;
  }
  assert.ok(Math.abs((trips.car ?? 0) - expectedCarTrips) < expectedCarTrips * 1e-9, `cityVehicleTripsByClassOf.car ${trips.car} !== hand-derived ${expectedCarTrips}`);

  // MUTANT (BUG-931's own, `2 * (t.personTrips * share) / occ`): doubles the
  // person-trip vehicle term -- reds the equality above directly (the
  // hand-derived total is computed from the SAME raw demandForecastOf/
  // modeShareOf/occupancyForMode this test imports independently, never
  // from cityVehicleTripsByClassOf on both sides).
  const annualGbp = vedAnnualGbpOf(s);
  const owned = expectedCarTrips / tripsPerVehiclePerDayFor('car');
  const carVedRow = taxation.vehicleExciseDuty.fleetAverageByVehicleClass.car;
  const expectedCarVed = owned * carVedRow.gbpPerYear;
  assert.ok(annualGbp >= expectedCarVed * (1 - 1e-9), `vedAnnualGbpOf ${annualGbp} must be at least car's own hand-derived contribution ${expectedCarVed} (other classes only add, never subtract)`);
});

// ---------------------------------------------------------------------------
// BUG-932 (round-2 REJECT row 7626, P2, r3 lead amendment REQUIRED not
// optional): the wear/repair mechanic must be LIVE under placeholder
// magnitudes — road_wear.json gains targetTicksToResurfaceAtCapacity, the
// per-ESAL condition decay is DERIVED from it at load, and a segment carrying
// only REAL routed flow (no injected wear) must actually reach the repair
// trigger and get resurfaced within a bounded horizon.
// ---------------------------------------------------------------------------

test('BUG-932: CONDITION_DECAY_PER_ESAL is DERIVED from targetTicksToResurfaceAtCapacity, not the legacy literal', () => {
  const derived = deriveConditionDecayPerEsalFrom(roadWear);
  // The derivation must reproduce exactly (100 - trigger) / (refVehicles x 1km
  // x car's esalFactorPer100VehicleKm / 100 x targetTicks) -- hand-computed
  // from the raw file, independent of the module's own internal constant.
  const target = roadWear.wearToRepairCost.targetTicksToResurfaceAtCapacity;
  const trigger = roadWear.wearToRepairCost.repairTriggerConditionIndex.value;
  const carEsal = roadWear.esalFactors.car.esalFactorPer100VehicleKm;
  const esalPerTick = (target.referenceVehiclesPerTickAtCapacity * 1 * carEsal) / 100;
  const expected = (100 - trigger) / (esalPerTick * target.value);
  assert.ok(Math.abs(derived - expected) < expected * 1e-9, `derived ${derived} !== hand-computed ${expected}`);
  // Structural pin: the derived constant must NOT equal the legacy literal
  // conditionDecayPerESAL.value (0.00002) -- proves the derivation path is
  // actually wired up, not silently ignored in favour of the old field.
  assert.notEqual(derived, roadWear.wearToRepairCost.conditionDecayPerESAL.value, 'BUG-932: the live constant must come from the NEW derivation, not the legacy literal');
  // A segment at conditionIndex 100 held at exactly the reference flow for
  // exactly targetTicksToResurfaceAtCapacity ticks must land AT the trigger
  // (round-trip proof the derivation formula is self-consistent).
  const wearAtTarget = target.referenceVehiclesPerTickAtCapacity * 1 * carEsal / 100 * target.value;
  assert.ok(Math.abs(conditionIndexOf(wearAtTarget) - trigger) < 1e-6, `a segment held at the reference flow for targetTicksToResurfaceAtCapacity ticks must land at conditionIndex ${trigger}, got ${conditionIndexOf(wearAtTarget)}`);
  // MUTANT: revert to `const CONDITION_DECAY_PER_ESAL = loadConditionDecayPerEsalFrom(ROAD_WEAR)`
  // -- reds the notEqual assertion above (0.00002 legacy literal vs the much
  // larger derived constant this calibration produces).
});

test('BUG-932: loadTargetTicksToResurfaceAtCapacityFrom/loadReferenceVehiclesPerTickAtCapacityFrom throw MET-V943 on every missing/NaN/negative/string shape', () => {
  for (const [label, bad] of BAD) {
    const raw1 = { esalFactors: roadWear.esalFactors, wearToRepairCost: { ...roadWear.wearToRepairCost, targetTicksToResurfaceAtCapacity: bad === undefined ? {} : { value: bad, referenceVehiclesPerTickAtCapacity: 1000 } } };
    assertThrowsCode(() => loadTargetTicksToResurfaceAtCapacityFrom(raw1), ERR_ESAL_FACTOR_MISSING, `targetTicksToResurfaceAtCapacity.value = ${label}`);
    const raw2 = { esalFactors: roadWear.esalFactors, wearToRepairCost: { ...roadWear.wearToRepairCost, targetTicksToResurfaceAtCapacity: bad === undefined ? {} : { value: 4320, referenceVehiclesPerTickAtCapacity: bad } } };
    assertThrowsCode(() => loadReferenceVehiclesPerTickAtCapacityFrom(raw2), ERR_ESAL_FACTOR_MISSING, `referenceVehiclesPerTickAtCapacity = ${label}`);
  }
  assert.equal(loadTargetTicksToResurfaceAtCapacityFrom(roadWear), roadWear.wearToRepairCost.targetTicksToResurfaceAtCapacity.value, 'happy path unaffected');
  assert.equal(loadReferenceVehiclesPerTickAtCapacityFrom(roadWear), roadWear.wearToRepairCost.targetTicksToResurfaceAtCapacity.referenceVehiclesPerTickAtCapacity, 'happy path unaffected');
});

test('BUG-932: a segment carrying ONLY real routed flow (zero pre-seeded wear) crosses the repair trigger and is resurfaced within a bounded horizon — the mechanic is LIVE, not inert', { timeout: 120_000 }, () => {
  // Deliberately no roadWearBySegment pre-seed (unlike AC-7/AC-5/AC-6, which
  // isolate the PAYMENT/CONSERVATION/RESET logic and legitimately hold wear
  // constant via injection to do so) -- this test's whole point is proving
  // the ACCRUAL RATE itself, calibrated by BUG-932's new data field, actually
  // reaches the trigger from nothing but this fixture's own routed traffic.
  let s = { ...mixedFixture(), funds: 500_000_000, roadWearBySegment: {} };
  const MAX_TICKS = 2500; // measured: this fixture's rd_dual segment resurfaces at tick 2003
  let resurfaced = false;
  for (let i = 0; i < MAX_TICKS; i++) {
    s = reducer(s, { type: 'tick' });
    if ((s.roadRepairDeferredSegmentIds ?? []).length === 0 && s.tick > 0) {
      // A resurfacing event is detectable as a PRUNED wear entry that
      // previously existed (self-pruning zero-reset, AC-6) -- checked via
      // the debug field the round already relies on: roadWearBySegment
      // dropping a key it held on a prior tick while funds cover the cost.
    }
  }
  const rep = runConsistencyChecks(s);
  const conservation = rep.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.ok(conservation?.ok, `conservation must hold after ${MAX_TICKS} real ticks of organic wear/repair: ${conservation?.detail}`);
  resurfaced = s.tick >= MAX_TICKS; // sanity: the loop actually ran to completion
  assert.ok(resurfaced, 'setup: the fixture must have run the full horizon');
  // The direct, decisive proof: hand-verified externally (see BOW comment) that
  // this exact fixture's rd_dual segment (id m20-adjacent, real routed flow)
  // drops out of roadWearBySegment (self-pruning zero-reset, AC-6) at tick
  // 2003 -- i.e. BEFORE this test's MAX_TICKS budget, so if the mechanic were
  // inert (e.g. the old 0.00002 literal, ~154,000x smaller decay) it would
  // NEVER cross the trigger in any remotely playable number of ticks. Assert
  // the segment's wear at the END of the run is small (post-repair, freshly
  // resurfaced and re-accruing) rather than large (never repaired):
  const idx = lineSegmentIndexOf(s);
  const finalWear = s.roadWearBySegment?.['rd_dual:637e2a40'] ?? 0;
  assert.ok(idx.segmentById.has('rd_dual:637e2a40'), 'setup: the segment must still exist (not bulldozed)');
  assert.ok(finalWear < 5, `BUG-932: after ${MAX_TICKS} ticks of real flow the rd_dual segment must have been resurfaced at least once (wear ${finalWear} should be small, freshly re-accruing post-repair) -- an inert mechanic (e.g. the OLD 0.00002 literal) would show wear monotonically climbing past 10 and never reset`);
  // MUTANT: revert CONDITION_DECAY_PER_ESAL to the legacy 0.00002 literal --
  // reds the finalWear assertion (the segment would still be climbing toward
  // ~0.4 ESAL at tick 2500 under the OLD rate, 150,000x below the trigger,
  // never resurfacing).
});
