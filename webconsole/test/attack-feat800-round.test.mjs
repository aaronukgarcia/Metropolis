// attack-feat800-round.test.mjs — FEAT-2326609800 inc7 "TAX, WEAR AND REPAIR",
// independent Destructive round 1 (attacker: opus-round-feat800-inc7).
//
// Every pin below is INDEPENDENT of the implementation it tests: each expected
// value is re-derived from the raw data files (data/traffic.json,
// vehicle_classes.json, road_wear.json, taxation.json, trip_generation.json,
// data/fuel.json) and from the SEGMENT-level flow map, never from the
// city-wide aggregate under test. That is the difference from the builder's
// own trafficWear.test.mjs, whose AC-2/AC-3/AC-4 "hand computations" read the
// aggregate they are checking and therefore survived these mutants:
//   M9  cityVehicleKmByClassOf x2              (fuel duty basis) -> caught by AC2_KM / AC2_DUTY
//   M10 cityVehicleTripsByClassOf = routed sum (VED basis)       -> caught by AC3_TRIP_BASIS
//   M11 segmentKmOf (tiles + 1)                (length basis)    -> caught by AC2_SEGKM
//
// No timing assertions (perf findings are reported on the BOW item, not
// pinned here).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import {
  assignedFlowOf,
  assignedFlowByClassOf,
  cityVehicleKmByClassOf,
  cityVehicleTripsByClassOf,
  fuelLitresDemandedOf,
  vehiclesOwnedByClassOf,
  vedAnnualGbpOf,
  segmentKmOf,
  roadWearStepOf,
  conditionIndexOf,
  repairCostMultiplierOf,
  REPAIR_TRIGGER_CONDITION_INDEX,
} from '../src/sim/trafficAssignment.ts';
import { initialState, computeFlows, reducer, TICKS_PER_YEAR } from '../src/sim/engine.ts';
import { TRAFFIC_RECOMPUTE_TICKS } from '../src/sim/trafficWellbeing.ts';
import { sanitizeRoadWearBySegment, lineSegmentIndexOf, SPECS } from '../src/sim/data.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, '..', '..');
const readJson = (...p) => JSON.parse(readFileSync(path.join(repoRoot, ...p), 'utf8'));
const traffic = readJson('data', 'traffic.json');
const vehicleClasses = readJson('data', 'traffic', 'vehicle_classes.json');
const roadWear = readJson('data', 'traffic', 'road_wear.json');
const taxation = readJson('data', 'traffic', 'taxation.json');
const tripGeneration = readJson('data', 'traffic', 'trip_generation.json');
const fuel = readJson('data', 'fuel.json');

const METRES_PER_TILE = traffic.webconsoleMetresPerTile;
const DUTY_PENCE_PER_LITRE = fuel.duty.ratePencePerLitre;

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
const rd = (id, spec, x, y) => ({ id, spec, x: x + OFFSET, y: y + OFFSET, builtTick: 0 });
const bl = (id, spec, x, y) => ({ id, spec, x: x + OFFSET, y: y + OFFSET });

// A REAL fixture: every spec id is asserted to exist in SPECS below. The
// builder's own mixedFixture() uses 'res_tower', which is NOT a spec (only
// res_tower_nyc / res_tower_sgp are), so that building is silently dropped.
// A multi-tile chain, so at least one routed path traverses >= 2 segments —
// that is what makes the trip-generation vs routed-sum distinction visible.
function chainFixture() {
  const bs = [bl(1, 'res_block', -1, 0)];
  for (let i = 0; i < 6; i++) bs.push(rd(10 + i, 'm20', i, 0));
  for (let i = 0; i < 4; i++) bs.push(rd(30 + i, 'rd_dual', i, 1));
  bs.push(bl(90, 'ind_estate', 3, 2));
  bs.push(bl(91, 'off_suite', 1, 2));
  return board(bs, 200000);
}

test('FIXTURE: every spec id in the attack fixture really exists (the res_tower phantom-building class)', () => {
  for (const b of chainFixture().buildings) {
    assert.ok(SPECS[b.spec], `spec ${b.spec} does not exist in SPECS — a phantom building the engine silently ignores`);
  }
  assert.equal(SPECS['res_tower'], undefined, 'guard: res_tower is not a spec (res_tower_nyc / res_tower_sgp are)');
});

test('AC2_SEGKM: segmentKmOf is exactly tiles x data/traffic.json webconsoleMetresPerTile / 1000, per road segment', () => {
  const s = chainFixture();
  const km = segmentKmOf(s);
  const idx = lineSegmentIndexOf(s);
  assert.ok(km.size > 0, 'setup: fixture must produce road segments');
  for (const seg of idx.segments) {
    if (seg.kind !== 'road') {
      assert.equal(km.get(seg.segmentId), undefined, 'non-road segments carry no vehicle-km basis');
      continue;
    }
    const expected = (seg.tiles * METRES_PER_TILE) / 1000;
    assert.equal(km.get(seg.segmentId), expected, `segment ${seg.segmentId}: km must be tiles x metresPerTile / 1000`);
  }
  // MUTANT M11: (seg.tiles + 1) x metresPerTile / 1000 — reds here. The
  // builder's suite survives it (AC-4 re-derives wear from segmentKmOf
  // itself, so an inflated length cancels out of both sides).
});

test('AC2_KM: cityVehicleKmByClassOf re-derived independently from the SEGMENT flow map x raw tile lengths', () => {
  const s = chainFixture();
  const byClass = assignedFlowByClassOf(s);
  const idx = lineSegmentIndexOf(s);
  const tilesOf = new Map(idx.segments.map((sg) => [sg.segmentId, sg.kind === 'road' ? sg.tiles : null]));
  const expected = {};
  for (const segId of [...byClass.keys()].sort()) {
    const tiles = tilesOf.get(segId);
    if (!tiles) continue;
    const segKm = (tiles * METRES_PER_TILE) / 1000;
    for (const [classId, flow] of Object.entries(byClass.get(segId))) {
      if (!flow) continue;
      expected[classId] = (expected[classId] ?? 0) + flow * segKm;
    }
  }
  const actual = cityVehicleKmByClassOf(s);
  assert.ok(Object.keys(expected).length >= 2, 'setup: at least 2 vehicle classes must carry km');
  assert.deepEqual(Object.keys(actual).sort(), Object.keys(expected).sort(), 'class key set must match');
  for (const [classId, v] of Object.entries(expected)) {
    assert.ok(Math.abs(actual[classId] - v) <= Math.abs(v) * 1e-12, `${classId}: aggregate disagrees with the independently derived vehicle-km`);
  }
  // MUTANT M9: `flow * segKm * 2` inside cityVehicleKmByClassOf — reds here.
});

test('AC2_DUTY: Fuel Duty inflow equals the fully hand-derived litres x duty rate / 100, and bus is excluded', () => {
  const s = chainFixture();
  const byClass = assignedFlowByClassOf(s);
  const idx = lineSegmentIndexOf(s);
  const tilesOf = new Map(idx.segments.map((sg) => [sg.segmentId, sg.kind === 'road' ? sg.tiles : null]));
  const litresPerKm = new Map(vehicleClasses.roadVehicles.map((v) => [v.id, v.fuelLitresPerKm]));
  let litres = 0;
  let busKm = 0;
  for (const segId of [...byClass.keys()].sort()) {
    const tiles = tilesOf.get(segId);
    if (!tiles) continue;
    const segKm = (tiles * METRES_PER_TILE) / 1000;
    for (const [classId, flow] of Object.entries(byClass.get(segId))) {
      if (!flow) continue;
      const lpk = litresPerKm.get(classId);
      if (typeof lpk !== 'number') {
        if (classId === 'bus') busKm += flow * segKm;
        continue;
      }
      litres += flow * segKm * lpk;
    }
  }
  assert.ok(litres > 0, 'setup: fixture must demand fuel');
  assert.ok(Math.abs(fuelLitresDemandedOf(s) - litres) <= litres * 1e-12, 'fuelLitresDemandedOf must equal the independent hand computation');
  const expectedDuty = Math.round((litres * DUTY_PENCE_PER_LITRE) / 100);
  const line = computeFlows(s).inflows.find((f) => f.label === 'Fuel Duty');
  if (expectedDuty > 0) {
    assert.ok(line, 'Fuel Duty inflow must be booked when litres > 0');
    assert.equal(line.value, expectedDuty, 'Fuel Duty must equal round(hand-derived litres x ratePencePerLitre / 100)');
  }
  // DISCLOSED GAP, pinned so it cannot regress silently: bus carries real
  // routed vehicle-km but pays NO fuel duty (vehicle_classes.json has no bus
  // fuelLitresPerKm row).
  assert.ok(busKm > 0, 'setup: this fixture does carry bus vehicle-km');
  assert.equal(litresPerKm.get('bus'), undefined, 'bus fuel duty is an acknowledged data gap (ASM), not an implementation choice');
});

test('AC3_TRIP_BASIS: VED counts trip GENERATION, never the per-segment routed sum (which double-counts by path length)', () => {
  const s = chainFixture();
  const trips = cityVehicleTripsByClassOf(s);
  const byClass = assignedFlowByClassOf(s);
  const routedSum = {};
  for (const segId of [...byClass.keys()].sort()) {
    for (const [classId, v] of Object.entries(byClass.get(segId))) {
      if (v) routedSum[classId] = (routedSum[classId] ?? 0) + v;
    }
  }
  assert.ok([...assignedFlowOf(s).keys()].length >= 2, 'setup: at least two segments must carry routed flow');
  assert.ok((trips.car ?? 0) > 0, 'setup: car trips must be nonzero');
  assert.ok(
    (routedSum.car ?? 0) > (trips.car ?? 0) * 1.0000001,
    'car: the routed segment sum must EXCEED the trip-generation total on a multi-segment network — if they are equal the VED basis is the routed sum, inflated by average path length',
  );
  // MUTANT M10: cityVehicleTripsByClassOf returns the per-segment routed sum
  // (the exact error the module's own doc-comment warns about) — reds here.
  // The builder's AC-3 test survives it (it re-derives `owned` from the same
  // `trips` it is checking).
  const freight = ['cargo_van', 'rigid_truck', 'articulated_truck'].find((c) => (trips[c] ?? 0) > 0);
  assert.ok(freight, 'setup: a freight class must generate trips');
  const owned = vehiclesOwnedByClassOf(s);
  for (const id of ['car', freight]) {
    const tpvd = tripGeneration.tripsPerVehiclePerDay[id].tripsPerVehiclePerDay;
    assert.ok(Math.abs(owned[id] - trips[id] / tpvd) <= 1e-9 * Math.abs(owned[id]), `${id}: owned must be trips / tripsPerVehiclePerDay`);
  }
  let annual = 0;
  for (const [id, n] of Object.entries(owned)) annual += n * taxation.vehicleExciseDuty.fleetAverageByVehicleClass[id].gbpPerYear;
  assert.ok(Math.abs(vedAnnualGbpOf(s) - annual) <= annual * 1e-12, 'vedAnnualGbpOf must equal the hand-summed fleet-average VED');
  const ved = computeFlows(s).inflows.find((f) => f.label === 'Road Tax (VED)');
  if (Math.round(annual / TICKS_PER_YEAR) > 0) {
    assert.ok(ved, 'Road Tax (VED) inflow must be booked');
    assert.equal(ved.value, Math.round(annual / TICKS_PER_YEAR), 'VED must be round(annual / TICKS_PER_YEAR)');
  }
  assert.equal(taxation.vehicleExciseDuty.fleetAverageByVehicleClass.bus, undefined, 'bus VED is an acknowledged data gap (ASM), not an implementation choice');
});

test('AC1_EXACT: the per-class split is NOT bit-equal to assignedFlowOf — the "sums exactly by construction" claim is one ULP wrong', () => {
  // AC-1's own Check says `Σ_class ... === assignedFlowOf(s).get(seg)` and the
  // module doc claims the identity holds "by construction". It does not: the
  // blended scalar accumulates ONE term per path segment while the per-class
  // map accumulates up to SEVEN, so the two summation orders diverge by
  // floating-point rounding on any segment fed by more than one class. The
  // builder's own AC-1 test hides this behind a 1e-9 absolute tolerance on a
  // single-segment fixture. Magnitude here is ~1e-16 relative (harmless
  // today), but it is pinned as a tolerance, not an equality, so a real
  // divergence is still caught.
  const s = chainFixture();
  const flow = assignedFlowOf(s);
  const byClass = assignedFlowByClassOf(s);
  assert.ok(flow.size > 0);
  let anyInexact = false;
  for (const [segId, total] of flow) {
    const sum = Object.values(byClass.get(segId) ?? {}).reduce((a, b) => a + b, 0);
    if (sum !== total) anyInexact = true;
    assert.ok(
      Math.abs(sum - total) <= Math.abs(total) * 4 * Number.EPSILON,
      `segment ${segId}: the per-class split diverges from assignedFlowOf by more than rounding`,
    );
  }
  assert.ok(anyInexact, 'recorded: at least one segment is NOT bit-equal, so AC-1 Check\'s === is not actually met');
});

test('AC4_WEAR_LAW: one tick of wear equals sum over classes of flow x km x esalFactorPer100VehicleKm / 100, re-derived from raw tiles', () => {
  const s = chainFixture();
  const byClass = assignedFlowByClassOf(s);
  const idx = lineSegmentIndexOf(s);
  const tilesOf = new Map(idx.segments.map((sg) => [sg.segmentId, sg.kind === 'road' ? sg.tiles : null]));
  const step = roadWearStepOf(s);
  let checked = 0;
  for (const segId of [...byClass.keys()].sort()) {
    const tiles = tilesOf.get(segId);
    if (!tiles) continue;
    const segKm = (tiles * METRES_PER_TILE) / 1000;
    let expected = 0;
    for (const [classId, flow] of Object.entries(byClass.get(segId))) {
      if (flow) expected += (flow * segKm * roadWear.esalFactors[classId].esalFactorPer100VehicleKm) / 100;
    }
    if (expected <= 0) continue;
    assert.ok(
      Math.abs((step.nextWearBySegment[segId] ?? 0) - expected) <= expected * 1e-12,
      `segment ${segId}: wear must follow the ESAL-per-100-vehicle-km law`,
    );
    checked++;
  }
  assert.ok(checked > 0, 'setup: at least one segment must accrue wear');
  // The two road_wear.json fields disagree: esalFactorPer100VehicleKm gives
  // articulated_truck/car = 20000, ratioToCarPerPass says 10000 (the figure
  // AC-4's own Check names). This pins the per-100-km field — what the
  // formula actually calls for — and records the discrepancy so a later data
  // edit cannot quietly reconcile it in either direction unnoticed.
  const perKmRatio = roadWear.esalFactors.articulated_truck.esalFactorPer100VehicleKm / roadWear.esalFactors.car.esalFactorPer100VehicleKm;
  assert.equal(perKmRatio, 20000);
  assert.equal(roadWear.esalFactors.articulated_truck.ratioToCarPerPass, 10000);
});

test('AC5_LABELS: a repair folds into the EXISTING Roads outflow and never adds a label; all money stays integral', () => {
  const base = { ...chainFixture(), tick: 1000, funds: 500_000_000 };
  const segId = lineSegmentIndexOf(base).segments.find((x) => x.kind === 'road').segmentId;
  const before = computeFlows(base);
  const worn = { ...base, roadWearBySegment: { [segId]: 1e9 } };
  assert.ok(conditionIndexOf(1e9) < REPAIR_TRIGGER_CONDITION_INDEX, 'setup: must be past the repair trigger');
  const after = computeFlows(worn);
  assert.deepEqual(
    after.outflows.map((f) => f.label).sort(),
    before.outflows.map((f) => f.label).sort(),
    'outflow label set must be unchanged (no second Road Repair line)',
  );
  const roadsBefore = before.outflows.find((f) => f.label === 'Roads')?.value ?? 0;
  const roadsAfter = after.outflows.find((f) => f.label === 'Roads')?.value ?? 0;
  assert.ok(roadsAfter > roadsBefore, 'the repair must actually cost money in the Roads bucket');
  assert.ok(after.outflows.every((f) => Number.isInteger(f.value) && f.value > 0), 'every outflow must be a positive integer');
  assert.ok(after.inflows.every((f) => Number.isInteger(f.value) && f.value >= 0), 'every inflow must be a non-negative integer');
});

test('AC6_RESET: wear resets to 0 the tick a repair event fires; a read never mutates state; a healthy segment is never flagged', () => {
  const base = { ...chainFixture(), tick: 1000 };
  const segId = lineSegmentIndexOf(base).segments.find((x) => x.kind === 'road').segmentId;
  const worn = { ...base, roadWearBySegment: { [segId]: 1e9 } };
  const step = roadWearStepOf(worn);
  assert.ok(step.repairEvents.some((e) => e.segmentId === segId), 'setup: must be flagged for repair');
  assert.equal(step.nextWearBySegment[segId] ?? 0, 0, 'wear resets to exactly 0');
  assert.equal(worn.roadWearBySegment[segId], 1e9, 'PURITY: the input state must never be mutated by a read');
  const healthy = { ...base, roadWearBySegment: { [segId]: 1 } };
  assert.equal(roadWearStepOf(healthy).repairEvents.length, 0, 'a trivially-worn segment must not be flagged');
});

test('AC7_CONSERVATION: funds-vs-flows and label uniqueness hold every tick of 120 with tax + wear + a repair active', () => {
  let s = { ...chainFixture(), tick: 1000, funds: 500_000_000, roadWearBySegment: {} };
  const segId = lineSegmentIndexOf(s).segments.find((x) => x.kind === 'road').segmentId;
  s = { ...s, roadWearBySegment: { [segId]: 3_000_000 } };
  let sawRepair = false;
  for (let i = 0; i < 120; i++) {
    if (roadWearStepOf(s).repairEvents.length > 0) sawRepair = true;
    s = reducer(s, { type: 'tick' });
    const rep = runConsistencyChecks(s);
    for (const id of ['conservation.funds-vs-flows', 'flows.inflow-labels-unique', 'flows.outflow-labels-unique']) {
      const c = rep.checks.find((x) => x.id === id);
      if (c) assert.ok(c.ok, `tick ${i}: ${id} failed: ${c.detail}`);
    }
    const labels = s.lastFlows.inflows.map((f) => f.label);
    assert.equal(new Set(labels).size, labels.length, `tick ${i}: duplicate inflow label`);
  }
  assert.ok(sawRepair, 'setup: a resurfacing event must occur inside the window');
});

test('AC8_DETERMINISM: three independent 120-tick runs are byte-identical in wear, funds and flows', () => {
  const runs = [];
  for (let r = 0; r < 3; r++) {
    let s = { ...chainFixture(), tick: 1000, funds: 500_000_000, roadWearBySegment: {} };
    for (let i = 0; i < 120; i++) s = reducer(s, { type: 'tick' });
    runs.push(JSON.stringify({ w: s.roadWearBySegment, f: s.funds, fl: s.lastFlows }));
  }
  assert.equal(runs[0], runs[1]);
  assert.equal(runs[0], runs[2]);
});

test('GR16_SANITIZER: hostile stored wear is coerced, and the two coercers are pinned in the direction they actually fail', () => {
  for (const bad of [undefined, null, 'x', 42, [1, 2]]) assert.deepEqual(sanitizeRoadWearBySegment(bad), {});
  assert.deepEqual(sanitizeRoadWearBySegment({ a: NaN, b: Infinity, c: -Infinity, d: -5, e: '7', f: 0 }), {});
  assert.deepEqual(sanitizeRoadWearBySegment({ g: 2.5 }), { g: 2.5 });
  // Recorded fail-open behaviour of the two coercers. Both are reachable only
  // through the sanitizer today, which is why these are pins and not failures
  // — but a future caller that skips the sanitizer inherits them.
  assert.equal(conditionIndexOf(NaN), 100, 'NaN wear reads as a pristine road, not a failed one');
  assert.equal(conditionIndexOf(Infinity), 100, 'Infinite wear reads as a pristine road — fail-open');
  assert.equal(repairCostMultiplierOf(NaN), 6, 'NaN condition prices at the maximum 6.0x multiplier — fail-open into a charge');
});

test('STATE_GROWTH (FIXED, BUG-917(b)): wear entries for segments that no longer exist are pruned within one cadence window, not carried forward forever', () => {
  // Round-1 found this UNBOUNDED (orphan entries survived demolition
  // forever); the r2 rework's roadWearStepOf dropped any nextWear key whose
  // segment was absent from the CURRENT lineSegmentIndexOf on EVERY tick.
  // r3 (BUG-929) moves the segment-existence check onto the cadence-cached
  // wearSegments map (a live lineSegmentIndexOf call on every tick was
  // exactly the per-tick assignment-pipeline cost BUG-929 removed) — a
  // demolished segment's wear entry now survives at most one traffic-cadence
  // window (TRAFFIC_RECOMPUTE_TICKS, data/traffic.json, currently 30) rather
  // than being dropped the same tick, but is STILL bounded, never forever.
  // This test now runs enough ticks past demolition to guarantee crossing a
  // cadence boundary (2x the cadence length, regardless of the tick 1000
  // fixture's phase within it) before asserting the prune.
  let s = { ...chainFixture(), tick: 1000, funds: 500_000_000 };
  for (let i = 0; i < 3; i++) s = reducer(s, { type: 'tick' });
  const worn = Object.keys(s.roadWearBySegment ?? {}).sort();
  assert.ok(worn.length > 0, 'setup: some segments must have accrued wear');
  let t = { ...s, buildings: s.buildings.filter((b) => b.spec !== 'm20' && b.spec !== 'rd_dual') };
  for (let i = 0; i < 2 * TRAFFIC_RECOMPUTE_TICKS; i++) t = reducer(t, { type: 'tick' });
  assert.deepEqual(
    Object.keys(t.roadWearBySegment ?? {}).sort(),
    [],
    'orphan wear entries for demolished road segments must be pruned within one cadence window, not accumulate forever',
  );
});

test('FISCAL_BOUNDARY: trafficAssignment.ts avoids the pinned Pounds token but now carries currency under other names', () => {
  const src = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'trafficAssignment.ts'), 'utf8');
  assert.ok(!src.includes('Pounds'), 'inc3 AC-8 pin still holds (no Pounds token)');
  // Recorded, not asserted away: the module now holds a pence-per-litre duty
  // rate and returns a GBP/year total, so inc3's fiscal boundary is satisfied
  // in letter only. A later increment that tightens the pin must decide about
  // these three names too.
  for (const tok of ['PENCE_PER_LITRE', 'gbpPerYear', 'vedAnnualGbpOf']) {
    assert.ok(src.includes(tok), `${tok} is present — currency crossed the boundary under a different name`);
  }
});

test('REGISTRY (FIXED, BUG-914(a)): MET-V944 is registered AND now has a real throw site in engine.ts — the silent `?? 0` fallback is gone', () => {
  // Round-1 found MET-V944 registered-but-dead (engine.ts silently defaulted
  // an unknown road class's base cost to 0, a free repair). The r2 rework
  // exports registryError from trafficAssignment.ts and engine.ts's new
  // roadRepairPaymentOf now throws ERR_BASE_COST_MISSING fail-closed instead
  // of the `?? 0` fallback. This test now asserts the FIXED state; see the
  // BOW report for the explicit before/after.
  const errors = readJson('data', 'errors.json');
  assert.ok(errors.codes['MET-V944'], 'MET-V944 is registered');
  const engineSrc = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'engine.ts'), 'utf8');
  const taSrc = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'trafficAssignment.ts'), 'utf8');
  assert.ok(engineSrc.includes('ERR_BASE_COST_MISSING'), 'engine.ts must now reference the code it owns the throw for');
  assert.match(engineSrc, /throw registryError\(\s*\n?\s*ERR_BASE_COST_MISSING/, 'engine.ts must actually THROW ERR_BASE_COST_MISSING');
  assert.ok(taSrc.includes("ERR_BASE_COST_MISSING = 'MET-V944'"), 'the code is still declared in trafficAssignment.ts');
  assert.ok(!engineSrc.includes('ROAD_CLASS_BASE_COST_POUNDS.get(ev.roadClassId) ?? 0'), 'the silent ?? 0 fallback must be gone');
});

// ---------------------------------------------------------------------------
// ROUND 2 (attacker: opus-reround-feat800-inc7) — lasting pins added after the
// r2 rework. Both pins below close holes the r2 rework LEFT OPEN, each proven
// by a surviving mutant run from this worktree.
// ---------------------------------------------------------------------------

test('R2_PAYMENT_GATE (BUG-915): a due repair with insufficient funds is DEFERRED — wear persists, nothing is booked, the segment is listed', () => {
  // MUTANT PROVEN TO SURVIVE BEFORE THIS PIN EXISTED: replacing engine.ts's
  //   const affordable = step.repairEvents.length === 0 || roundedCost <= 0 || s.funds >= roundedCost;
  // with `const affordable = true;` (i.e. deleting BUG-915's payment gate
  // outright) passed BOTH trafficWear.test.mjs AND this file unchanged. The
  // gate was implemented and manually spot-checked, but nothing in either
  // suite exercised the funds-short branch, so it could be deleted silently.
  const base = { ...chainFixture(), tick: 1000 };
  const segId = lineSegmentIndexOf(base).segments.find((x) => x.kind === 'road').segmentId;
  const seeded = 1e9;
  assert.ok(conditionIndexOf(seeded) < REPAIR_TRIGGER_CONDITION_INDEX, 'setup: the seeded wear must be past the repair trigger');

  // (a) funds short -> deferred: wear persists at or above the seeded value,
  //     the segment is named, and no repair money leaves the ledger.
  const broke = { ...base, funds: 0, roadWearBySegment: { [segId]: seeded } };
  const brokeAfter = reducer(broke, { type: 'tick' });
  assert.ok(
    Array.isArray(brokeAfter.roadRepairDeferredSegmentIds) && brokeAfter.roadRepairDeferredSegmentIds.includes(segId),
    'a repair the city cannot afford must be recorded in roadRepairDeferredSegmentIds',
  );
  assert.ok(
    (brokeAfter.roadWearBySegment?.[segId] ?? 0) >= seeded,
    'deferred repair: the wear must PERSIST (not silently reset), so the road stays broken until it is paid for',
  );

  // (b) funds ample -> paid: wear resets and nothing is deferred.
  const rich = { ...base, funds: 500_000_000, roadWearBySegment: { [segId]: seeded } };
  const richAfter = reducer(rich, { type: 'tick' });
  assert.deepEqual(richAfter.roadRepairDeferredSegmentIds ?? [], [], 'an affordable repair must defer nothing');
  assert.ok(
    (richAfter.roadWearBySegment?.[segId] ?? 0) < seeded,
    'a paid repair must reset the segment wear',
  );

  // (c) the two branches must actually DIFFER — without this the pin would
  //     survive a gate that always resurfaces.
  assert.ok(
    (brokeAfter.roadWearBySegment?.[segId] ?? 0) > (richAfter.roadWearBySegment?.[segId] ?? 0),
    'the funds-short and funds-ample outcomes must differ; if they are equal the payment gate is inert',
  );
});

test('R2_VED_MAGNITUDE (BUG-913, RECORDED GAP): the person-trip vehicle basis is still unpinned in magnitude', () => {
  // MUTANT PROVEN TO SURVIVE: doubling the person-trip vehicle term in
  // cityVehicleTripsByClassOf —
  //   const v = (t.personTrips * share) / occ;  ->  const v = 2 * (...) / occ;
  // — passes trafficWear.test.mjs AND this file. Both suites re-derive
  // `owned` and the VED total FROM `trips`, so any scalar error in the
  // person-trip branch propagates through every expectation unnoticed. Only
  // the BASIS (generation vs routed sum, AC3_TRIP_BASIS above) is pinned, not
  // the magnitude. This test records the gap so it cannot be forgotten and
  // pins the one magnitude relation that IS independently checkable today:
  // trips must never exceed the routed segment sum (a trip is loaded onto at
  // least one segment), which a 2x error on a mostly single-segment path set
  // would break.
  const s = chainFixture();
  const trips = cityVehicleTripsByClassOf(s);
  const byClass = assignedFlowByClassOf(s);
  const routedSum = {};
  for (const segId of [...byClass.keys()].sort()) {
    for (const [classId, v] of Object.entries(byClass.get(segId))) {
      if (v) routedSum[classId] = (routedSum[classId] ?? 0) + v;
    }
  }
  assert.ok((trips.car ?? 0) > 0, 'setup: car trips must be nonzero');
  assert.ok(
    (trips.car ?? 0) <= (routedSum.car ?? 0) + 1e-9,
    'car trip generation can never exceed the routed segment sum — every generated trip loads at least one segment',
  );
});

// ===========================================================================
// ROUND 3 (attacker: opus-round3-feat800-inc7) — lasting pins for the r3
// lead amendments. Numbers in the BOW evidence comment, verdict row below.
// ===========================================================================

// BUG-929 (r3 amendment 1) — the money path must do ZERO traffic-assignment
// work on a non-cadence tick. Measured with trafficAssignment.ts's own
// shipped Dijkstra relaxation counter (not a timer): over 40 consecutive
// reducer ticks EXACTLY the cadence ticks may record any relaxation at all.
// MUTANT: reverting engine.ts's fuelLitresDemandedFor/vedAnnualGbpFor to call
// fuelLitresDemandedOf(s)/vedAnnualGbpOf(s) directly, or roadWearStepOf to
// call wearSegmentInputsOf(s) unconditionally, makes EVERY tick relax and
// reds this pin (that regression WAS the r2 2.19x measurement).
test('R3_NO_DIJKSTRA_OFF_CADENCE: only cadence ticks perform any Dijkstra relaxation', async () => {
  const ta = await import('../src/sim/trafficAssignment.ts');
  let s = chainFixture();
  s = reducer(s, { type: 'tick' }); // bootstrap tick (snapshot absent -> one assignment)
  const relaxingTicks = [];
  for (let i = 0; i < 40; i++) {
    ta.__resetDijkstraRelaxationCounterForTest();
    s = reducer(s, { type: 'tick' });
    if (ta.__getDijkstraRelaxationCounterForTest() > 0) relaxingTicks.push(s.tick);
  }
  assert.ok(relaxingTicks.length > 0, 'setup: at least one cadence tick must fall in a 40-tick window');
  for (const t of relaxingTicks) {
    assert.equal(t % TRAFFIC_RECOMPUTE_TICKS, 0, `tick ${t} performed Dijkstra work off-cadence (BUG-929) — relaxing ticks were [${relaxingTicks}]`);
  }
  assert.ok(
    relaxingTicks.length <= Math.ceil(40 / TRAFFIC_RECOMPUTE_TICKS),
    `at most one assignment per cadence window: got ${relaxingTicks.length} relaxing ticks in 40 (BUG-929)`,
  );
});

// R3 money-on-a-stale-snapshot: the cadence lag is ACCEPTED as the design's
// (documented) behaviour, but it must be BOUNDED and self-correcting. After
// every road is demolished the city may keep booking Fuel Duty from the stale
// snapshot, but it MUST stop within one cadence window, must never go
// negative, and the wear entries for the vanished segments must be gone.
// MUTANT: a snapshot that is never refreshed (cadence gate always false)
// books phantom fuel duty forever and reds this pin.
test('R3_STALE_MONEY_BOUNDED: demolishing every road stops Fuel Duty within one cadence window, never negative', () => {
  let s = chainFixture();
  for (let i = 0; i < 2; i++) s = reducer(s, { type: 'tick' });
  const dutyOf = (st) => {
    const e = computeFlows(st).inflows.find((i) => i.label === 'Fuel Duty');
    return e ? e.value : 0;
  };
  assert.ok(dutyOf(s) > 0, 'setup: the fixture must book Fuel Duty while its roads exist');
  let t = { ...s, buildings: s.buildings.filter((b) => b.spec !== 'm20' && b.spec !== 'rd_dual') };
  let phantomTicks = 0;
  for (let i = 0; i < TRAFFIC_RECOMPUTE_TICKS * 2 + 2; i++) {
    const d = dutyOf(t);
    assert.ok(d >= 0, `Fuel Duty went negative (${d}) at tick ${t.tick} after demolition`);
    if (d > 0) phantomTicks++;
    t = reducer(t, { type: 'tick' });
  }
  assert.ok(
    phantomTicks <= TRAFFIC_RECOMPUTE_TICKS,
    `phantom Fuel Duty from demolished roads must stop within one cadence window (${TRAFFIC_RECOMPUTE_TICKS}); lasted ${phantomTicks} ticks`,
  );
  assert.equal(dutyOf(t), 0, 'after two cadence windows with no roads, Fuel Duty must be exactly 0');
  assert.equal(Object.keys(t.roadWearBySegment ?? {}).length, 0, 'wear entries for demolished segments must be pruned');
});

// R3 finding (recorded, not a regression gate): an all-invalid-but-PRESENT
// wearSegments map sanitizes to `{}`, which is "present", so the bootstrap
// fallback does NOT fire and roadWearStepFromSnapshot orphan-prunes EVERY
// accumulated wear entry with ZERO repairEvents — a corrupt save silently
// resurfaces the whole network for free. Pinned so the behaviour is at least
// documented and a future fix (bootstrap on an EMPTY map, or repair-before-
// prune) has a test to flip.
test('R3_EMPTY_WEARSEGMENTS_FREE_WIPE: an empty cadence wear map drops all accumulated wear with no repair event', async () => {
  const ta = await import('../src/sim/trafficAssignment.ts');
  const tw = await import('../src/sim/trafficWellbeing.ts');
  const sanitized = tw.sanitizeTrafficSnapshot({
    tick: 5,
    medianCommuteMinutes: 10,
    gridlockShare: 0.1,
    coverageShare: 0.5,
    wearSegments: { a: { roadClassId: '', deltaEsalPerTick: NaN } },
  });
  assert.deepEqual(sanitized.wearSegments, {}, 'an all-invalid wearSegments sanitizes to a PRESENT empty map (not dropped)');
  const step = ta.roadWearStepFromSnapshot({ seg1: 5e9, seg2: 3 }, sanitized.wearSegments);
  assert.deepEqual(step.repairEvents, [], 'documented: no repair event is raised for the wiped segments');
  assert.deepEqual(step.nextWearBySegment, {}, 'documented: all accumulated wear is dropped with nothing booked');
});
