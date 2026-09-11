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

// r4 LEAD RULING (port amendment, after the r3 ACCEPT failed at port against
// BUG-951/BUG-966's delta protocol — a null-prototype map clones to PLAIN
// through structuredClone, breaking attack-bug950-951-round.test.mjs's
// clone-side deepStrictEqual pin by prototype alone): every sanitizer-built
// map is a PLAIN object again, built via Object.fromEntries over validated
// entries (own-data-property semantics, never bracket assignment). `plain`
// (renamed from r3's `nullProto`) is now a trivial same-shape shallow copy —
// kept as a named helper so every call site below reads as "the expected
// map shape", not a bare object literal.
const plain = (obj) => ({ ...obj });

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
  for (const bad of [undefined, null, 'x', 42, [1, 2]]) assert.deepEqual(sanitizeRoadWearBySegment(bad), plain({}));
  assert.deepEqual(sanitizeRoadWearBySegment({ a: NaN, b: Infinity, c: -Infinity, d: -5, e: '7', f: 0 }), plain({}));
  assert.deepEqual(sanitizeRoadWearBySegment({ g: 2.5 }), plain({ g: 2.5 }));
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

// BUG-941 FIX (flipped from the R3 DEFECT pin): an all-invalid-but-PRESENT
// wearSegments map is now DROPPED (treated as absent) by the sanitizer, not
// sanitized to a present `{}` -- so engine.ts's bootstrap fallback fires and
// roadWearStepOf recomputes wearSegmentInputsOf(s) fresh instead of
// roadWearStepFromSnapshot's orphan-prune (BUG-917(b)) running over a
// corrupt empty valid-set and free-wiping every accumulated wear entry.
test('R3_EMPTY_WEARSEGMENTS_FREE_WIPE (FIXED, BUG-941): an all-invalid cadence wear map is dropped (absent), never sanitized to a trusted empty map', async () => {
  const tw = await import('../src/sim/trafficWellbeing.ts');
  const sanitized = tw.sanitizeTrafficSnapshot({
    tick: 5,
    medianCommuteMinutes: 10,
    gridlockShare: 0.1,
    coverageShare: 0.5,
    wearSegments: { a: { roadClassId: '', deltaEsalPerTick: NaN } },
  });
  assert.equal(sanitized.wearSegments, undefined, 'BUG-941: an all-invalid wearSegments must be DROPPED (absent), not sanitized to a trusted present {}');
  // A genuinely empty raw map (the honest all-roads-demolished cadence
  // reading) must still sanitize PRESENT so the orphan-prune keeps firing --
  // this is the BUG-917(b) case this fix must not regress.
  const genuinelyEmpty = tw.sanitizeTrafficSnapshot({
    tick: 5,
    medianCommuteMinutes: 10,
    gridlockShare: 0.1,
    coverageShare: 0.5,
    wearSegments: {},
  });
  assert.deepEqual(genuinelyEmpty.wearSegments, plain({}), 'a genuinely empty {} (no roads this cadence window) must stay present, not be treated as absent');
  // MUTANT: restore the old unconditional `out.wearSegments = wearSegments`
  // assignment (drop the `if (!sawInvalidEntry)` guard) -- reds the first
  // assertion (sanitized.wearSegments would be {} again, not undefined).
});

// ---------------------------------------------------------------------------
// BUG-941 round 1 (opus-round-bug941, independent) — the fix's poison rule is
// keyed on per-ENTRY validation, and there is exactly one raw key that never
// reached the entry loop's accumulator: '__proto__'. `wearSegments[segId] =
// {...}` with segId '__proto__' did NOT create an own property — it invoked
// Object.prototype's __proto__ SETTER and re-parented the accumulator — so
// `sawInvalidEntry` stayed FALSE, the field was assigned as a TRUSTED
// present-and-empty map (Object.keys length 0), and roadWearStepFromSnapshot's
// orphan-prune free-wiped EVERY accumulated wear entry with ZERO repairEvents:
// the exact BUG-941 defect, unchanged, one key away.
//
// FIXED (BUG-946, r2): the accumulator became Object.create(null) (no
// inherited __proto__ setter to hijack) PLUS a name-ban list rejecting
// '__proto__'/'constructor'/'prototype' as invalid entries. BUG-961 (r3
// LEAD RULING) found the name-ban half of that fix was the wrong shape —
// it covered the names r2 thought of but nothing else ('toString' etc, see
// the BUG-961 test above) — and replaced it with null-prototype structural
// safety. r4 LEAD RULING (after the r3 ACCEPT failed at PORT: a
// null-prototype map clones to PLAIN through structuredClone, breaking the
// delta-protocol's clone-side pin) moved the structural fix one level
// down again: the accumulator is a PLAIN object (Object.prototype, stable
// across structuredClone/JSON/spread) built via Object.fromEntries over
// validated [segId, value] entries — Object.fromEntries uses
// CreateDataProperty internally, an OWN property even for the key
// "__proto__", never the inherited setter that a bracket assignment
// (`acc[segId] = value`) would invoke on a plain target. A '__proto__' key
// with otherwise-VALID data is not banned by name — it is validated exactly
// like any other segId and, being valid, is preserved as ordinary data.
// This test asserts the CURRENT (r4) contract: no re-parenting, no
// Object.prototype leak, EITHER WAY.
test('BUG-946 REGRESSION: a `__proto__` key never re-parents the accumulator, whether it poisons or (post-r3/r4) survives as ordinary data', async () => {
  const ta = await import('../src/sim/trafficAssignment.ts');
  const tw = await import('../src/sim/trafficWellbeing.ts');
  const base = { tick: 5, medianCommuteMinutes: 10, gridlockShare: 0.1, coverageShare: 0.5 };
  // JSON.parse (the real save/debug-json route) produces an OWN '__proto__' key.
  const raw = JSON.parse('{"__proto__":{"roadClassId":"motorway","deltaEsalPerTick":1}}');
  const sanitized = tw.sanitizeTrafficSnapshot({ ...base, wearSegments: raw });
  // r3/r4: a VALID entry under '__proto__' is ordinary data now, not poison —
  // the original BUG-946 concern (the accumulator's OWN prototype getting
  // hijacked) is proven by the assertion below regardless of this outcome.
  assert.notEqual(sanitized.wearSegments, undefined, 'BUG-946/BUG-961: a VALID __proto__ entry must survive as ordinary data (r3 supersedes r2\'s name-ban)');
  assert.deepEqual(sanitized.wearSegments, plain({ ['__proto__']: { roadClassId: 'motorway', deltaEsalPerTick: 1 } }), 'the __proto__-keyed entry itself must be preserved unpoisoned');
  // Prove the accumulator was never re-parented (this is the assertion that
  // caught the ORIGINAL bug: MUTANT — restore bracket assignment
  // (`wearSegmentEntries[segId] = value` style, or building the map with
  // `acc[segId] = value` instead of Object.fromEntries) and this goes RED
  // because roadClassId leaks through the live global Object.prototype
  // instead of landing as an own '__proto__' key).
  assert.equal(({}).roadClassId, undefined, 'global Object.prototype must never be touched');
  assert.equal(Object.getPrototypeOf(sanitized.wearSegments), Object.prototype, 'r4: the accumulator is an ordinary PLAIN object, never re-parented to the raw entry object and never null-prototype');
  const step = ta.roadWearStepFromSnapshot({ seg1: 5e9, seg2: 3 }, tw.sanitizeTrafficSnapshot({ ...base, wearSegments: {} }).wearSegments);
  assert.deepEqual(step.repairEvents, [], 'a genuinely empty cadence map still books nothing (unaffected control case)');
});

// BUG-941 round 1 — a roadClassId that was a valid non-empty STRING but named
// no class in data/roads.json passed the sanitizer untouched, and the very
// next repair that segment was due threw MET-V944 fail-closed from inside
// advance(): a stale save naming a removed/renamed road class bricked the
// tick loop rather than dropping the untrustworthy field and bootstrapping.
//
// FIXED (BUG-947, r2 LEAD RULING): an unresolvable roadClassId is now invalid
// at sanitize time (validated against trafficAssignment.ts's exported
// ROAD_CLASS_IDS, sourced from data/roads.json) and poisons the field,
// converting the game-bricking throw into a graceful bootstrap recompute.
// Flipped from a DEFECT pin to a REGRESSION pin.
test('BUG-947 REGRESSION: an unknown-but-well-formed roadClassId is rejected as an invalid entry, never reaches advance()', async () => {
  const tw = await import('../src/sim/trafficWellbeing.ts');
  const sanitized = tw.sanitizeTrafficSnapshot({
    tick: 5,
    medianCommuteMinutes: 10,
    gridlockShare: 0.1,
    coverageShare: 0.5,
    wearSegments: { a: { roadClassId: 'no_such_class_in_roads_json', deltaEsalPerTick: 1 } },
  });
  assert.equal(
    sanitized.wearSegments,
    undefined,
    'BUG-947: an unknown road class must poison the field (dropped/absent) so engine.ts bootstraps fresh, instead of surviving to throw MET-V944 later',
  );
  // A KNOWN class (real data/roads.json entry) must still pass through — the
  // fix must not reject every roadClassId wholesale.
  const ta = await import('../src/sim/trafficAssignment.ts');
  const [firstKnownClass] = [...ta.ROAD_CLASS_IDS];
  const stillValid = tw.sanitizeTrafficSnapshot({
    tick: 5,
    medianCommuteMinutes: 10,
    gridlockShare: 0.1,
    coverageShare: 0.5,
    wearSegments: { a: { roadClassId: firstKnownClass, deltaEsalPerTick: 1 } },
  });
  assert.deepEqual(stillValid.wearSegments, plain({ a: { roadClassId: firstKnownClass, deltaEsalPerTick: 1 } }), 'a genuinely known road class must still pass through unpoisoned');
});

// ===========================================================================
// BUG-941 ROUND 2 (attacker: opus-reround-bug941) — the r2 fix closes the
// three NAMED unsafe keys and the file-open/named-save load path, but the
// BUG-941 free wipe is still reachable two other ways. Both pins below are
// DEFECT pins: they document the CURRENT (still-broken) behaviour and are
// GREEN as written, ready to flip to REGRESSION pins when fixed.
// ===========================================================================

// BUG-961 (P2, r2 REJECT) -> FIXED (r3 LEAD RULING): the r2 poison rule
// banned exactly '__proto__'/'constructor'/'prototype' BY NAME, but EVERY
// Object.prototype member name ('toString', 'hasOwnProperty', 'valueOf', …)
// is an inherited key on a PLAIN-object map, and none of them were banned —
// so a segment id of 'toString' with a fully VALID roadClassId/
// deltaEsalPerTick used to reach `roadWearStepFromSnapshot`'s bracket reads
// and silently corrupt them: `prevWear[segId] ?? 0` read the INHERITED
// Object.prototype.toString FUNCTION (truthy, so `?? 0` never caught it),
// turning a numeric wear value into a STRING via `function + number`
// coercion, and `segId in wearSegments` (an inherited-key test) could
// wrongly report true for names it never actually held as an own key.
//
// FIX (BUG-961, r3) -> r4 PORT AMENDMENT: r3 made every sanitizer-built map
// a null-prototype object; r4 moved the SAME safety one level down (the
// build discipline, not the target's own prototype) after r3's
// null-prototype maps broke the delta-protocol's structuredClone-based
// clone-side pin. The map is PLAIN again, but every write is
// Object.fromEntries over validated entries (own-data-property semantics —
// CreateDataProperty never invokes an inherited setter, regardless of the
// target's prototype) — so 'toString' becomes a completely ordinary segment
// id either way: a VALID entry under that name passes through unpoisoned
// (it is not corruption, just an unusual but harmless string), and every
// consumer read (`prevWear[segId]`, `wearSegments[segId]`, the orphan-prune
// membership test) uses an own-key guard
// (Object.prototype.hasOwnProperty.call) so a plain-object map can never
// silently misread an inherited name.
test('BUG-961 REGRESSION: an Object.prototype-named segId (toString) is an ordinary entry, never corrupts real accumulated wear, and Object.prototype stays untouched', async () => {
  const tw = await import('../src/sim/trafficWellbeing.ts');
  const ta = await import('../src/sim/trafficAssignment.ts');
  const base = { tick: 5, medianCommuteMinutes: 10, gridlockShare: 0.1, coverageShare: 0.5 };
  const [firstKnownClass] = [...ta.ROAD_CLASS_IDS];
  // The current cadence map carries THREE segments: the two real ones
  // (present with zero flow this window, the honest "exists but idle"
  // reading — never simply absent, per wearSegmentInputsOf's own doc) plus
  // the hazardous 'toString' segId carrying real flow.
  const raw = {
    'seg:real1': { roadClassId: firstKnownClass, deltaEsalPerTick: 0 },
    'seg:real2': { roadClassId: firstKnownClass, deltaEsalPerTick: 0 },
    toString: { roadClassId: firstKnownClass, deltaEsalPerTick: 5 },
  };
  const sanitized = tw.sanitizeTrafficSnapshot({ ...base, wearSegments: raw });
  // (1) present, unpoisoned, null-prototype, and Object.prototype untouched.
  assert.notEqual(sanitized.wearSegments, undefined, 'a VALID entry under an inherited-method name must pass through, never poison the field');
  assert.equal(Object.getPrototypeOf(sanitized.wearSegments), Object.prototype, 'r4: the sanitized map must be an ordinary PLAIN object (Object.prototype), never null-prototype (would break the delta-protocol clone-side pin)');
  assert.equal(typeof ({}).toString, 'function', 'BUG-961: global Object.prototype.toString must remain the native function, never overwritten');
  assert.deepEqual(sanitized.wearSegments.toString, { roadClassId: firstKnownClass, deltaEsalPerTick: 5 }, 'the toString-keyed entry itself must survive as ordinary data, not the inherited function');

  // (2) real accumulated wear on the two real segments (prior wear 5e9 /
  // repair-due, and 3 / not due) is never silently free-wiped or corrupted
  // to a string — 5e9 books a REAL repair event; 3 carries forward as a
  // NUMBER, unchanged.
  const step = ta.roadWearStepFromSnapshot({ 'seg:real1': 5e9, 'seg:real2': 3 }, sanitized.wearSegments);
  assert.equal(step.repairEvents.length, 1, 'BUG-961: exactly one repair fires (seg:real1, past the trigger) — the wipe must book something, not nothing');
  assert.equal(step.repairEvents[0].segmentId, 'seg:real1');
  assert.equal(step.nextWearBySegment['seg:real1'], undefined, 'seg:real1 resets to 0 (self-pruning) BECAUSE it was actually repaired, not because it was free-wiped');
  assert.equal(step.nextWearBySegment['seg:real2'], 3, 'seg:real2 (not yet due for repair) must survive UNCHANGED, never silently dropped');
  assert.equal(typeof step.nextWearBySegment.toString, 'number', 'BUG-961: the toString-keyed entry accrues as a NUMBER (0 + 5), never the inherited-function-coerced STRING');
  assert.equal(step.nextWearBySegment.toString, 5);
  assert.equal(typeof ({}).toString, 'function', 'BUG-961: Object.prototype.toString must still be pristine after the whole step');
});

// BUG-962 (P1) — BUG-948's fix routes trafficSnapshot through
// sanitizeTrafficSnapshot inside gamesave.ts's validateGameSaveObject, which
// covers File->Open (parseGameSave) and Load->Saved cities (readNamedSave).
// It does NOT cover the DEFAULT boot path: replay.ts's
// restoreFromSavepoint / prepareRestoreForChunkedTail used to take
// `sp.snapshot` straight out of decodeSavepointBytes (a bare JSON.parse +
// coerceSnapshotBuildings), and store.tsx wrapped that in sanitizeTreasury
// ONLY (store.tsx:921/950/1026/1437) — so the localStorage/IndexedDB
// savepoint every session actually boots from was never sanitized: a
// corrupt trafficSnapshot reached advance() verbatim.
//
// FIX (BUG-962): decodeSavepointBytes — the ONE shared decode boundary
// (BUG-742 round F3) every one of those paths already funnels through — now
// also runs sanitizeTrafficSnapshot (plus sanitizeCongestionTicksBySpec/
// sanitizeRoadWearBySegment for the sibling maps) on `sp.snapshot` right
// after the JSON.parse, before ANY caller (restoreFromSavepoint,
// prepareRestoreForChunkedTail, store.tsx's decodeSavepointRaw hot-swap
// path) ever sees it. gamesave.ts keeps its own separate call at the
// File->Open / Load->Saved-cities boundary (BUG-948) since that path never
// goes through decodeSavepointBytes at all.
//
// NOTE: with BUG-961's null-prototype fix, a hazardous key name
// ('__proto__'/'toString') carrying otherwise-VALID data is no longer
// poison on its own — it is validated exactly like any other segId. So this
// pin uses an INVALID roadClassId (the actual corruption signal) under both
// hazardous names, to isolate "did the decode boundary sanitize at all"
// from the separate (already-covered) BUG-961 key-safety question.
test('BUG-962 REGRESSION: the default savepoint boot path (restoreFromSavepoint) sanitizes trafficSnapshot at the decode boundary', async () => {
  const { createSavepoint, persistSavepointForced, restoreFromSavepoint, prepareRestoreForChunkedTail } = await import('../src/sim/replay.ts');
  const { initialState: init, reducer: red, sanitizeTreasury } = await import('../src/sim/engine.ts');
  class Mem {
    constructor() { this.m = new Map(); }
    getItem(k) { return this.m.has(k) ? this.m.get(k) : null; }
    setItem(k, v) { this.m.set(k, String(v)); }
    removeItem(k) { this.m.delete(k); }
    key(i) { return [...this.m.keys()][i] ?? null; }
    get length() { return this.m.size; }
  }
  const s = { ...init(), roadWearBySegment: { 'seg:real1': 5e9, 'seg:real2': 3 } };
  const corrupt = {
    ...s,
    trafficSnapshot: {
      tick: s.tick, medianCommuteMinutes: 10, gridlockShare: 0.1, coverageShare: 0.5,
      safeRoadScore: 1, integratedTransportScore: 0,
      // INVALID data (empty/unknown roadClassId) under both hazardous names
      // — must poison the whole field regardless of key-ban questions.
      wearSegments: JSON.parse('{"__proto__":{"roadClassId":"","deltaEsalPerTick":1},"toString":{"roadClassId":"no_such_class_in_roads_json","deltaEsalPerTick":2}}'),
    },
  };
  const storage = new Mem();
  persistSavepointForced(storage, createSavepoint(corrupt, [], new Date(), 'test', null));
  const restored = restoreFromSavepoint(storage);
  assert.equal(restored.success, true, 'setup: the savepoint must restore');
  const booted = sanitizeTreasury(restored.state); // exactly what store.tsx does at boot
  // FIXED: the decode boundary already sanitized this before restoreFromSavepoint
  // ever saw it — the corrupt field is dropped (absent), not carried through.
  assert.equal(
    booted.trafficSnapshot.wearSegments,
    undefined,
    'BUG-962: the decode boundary must sanitize trafficSnapshot before restoreFromSavepoint reads it, dropping the corrupt field',
  );
  const prepared = prepareRestoreForChunkedTail(storage);
  assert.equal(prepared.state.trafficSnapshot.wearSegments, undefined, 'BUG-962: the chunked-tail boot variant shares the SAME decode boundary and must be sanitized identically');
  assert.equal(typeof ({}).toString, 'function', 'BUG-962: Object.prototype must never be touched by any of this');
  // One tick on the now-clean boot state must book normally: never throw
  // (a stale unknown-class entry used to survive to MET-V944 inside
  // advance() — BUG-947), and never leave a string-typed wear value.
  assert.doesNotThrow(() => red(booted, { type: 'tick' }), 'BUG-962: a corrupt savepoint must never brick the first tick after boot');
  const after = red(booted, { type: 'tick' });
  for (const [segId, wear] of Object.entries(after.roadWearBySegment)) {
    assert.equal(typeof wear, 'number', `BUG-962: roadWearBySegment.${segId} must stay numeric, never a corrupted string`);
    assert.ok(Number.isFinite(wear), `BUG-962: roadWearBySegment.${segId} must stay finite`);
  }
  // MUTANT: comment out decodeSavepointBytes's three sanitize* calls -- reds
  // both equal(...,undefined) assertions above (the corrupt field survives
  // as a present object with '__proto__'/'toString' keys instead).
});

// ===========================================================================
// BUG-941 ROUND 3 (attacker: opus-round3-bug941) — the r3 fix (null-prototype
// maps + own-key consumer guards + the decodeSavepointBytes boundary) holds.
// The pins below are the three properties nothing else in the tree kills,
// plus one DEFECT pin (green as written) for the one map named in the r3
// LEAD RULING whose LIVE producer was missed.
// ===========================================================================

// R3_PRUNE_OWNKEY_ON_A_PLAIN_MAP — the `segId in wearSegments` ->
// Object.prototype.hasOwnProperty.call change in roadWearStepFromSnapshot's
// BUG-917(b) orphan-prune is load-bearing on a REACHABLE shape: `in` walks
// the prototype chain and would wrongly report TRUE for an
// Object.prototype-named segId the map never held as an own key, leaving a
// stale wear entry un-prunable forever. r4 makes this the ONLY line of
// defence (the map itself is plain both before and after r3/r4, so this was
// never contingent on prototype shape) — still pinned unchanged.
test('R3/R4 (BUG-961): the orphan-prune uses an OWN-key test, so a stale Object.prototype-named wear entry is pruned even when wearSegments arrives PLAIN (the post-structuredClone worker shape)', async () => {
  const ta = await import('../src/sim/trafficAssignment.ts');
  const [known] = [...ta.ROAD_CLASS_IDS];
  // A PLAIN object — exactly what a worker postMessage round trip delivers.
  const wearSegments = { 'seg:a': { roadClassId: known, deltaEsalPerTick: 1 } };
  assert.equal(Object.getPrototypeOf(wearSegments), Object.prototype, 'setup: this pin is specifically about the PLAIN-prototype input shape');
  assert.equal(Object.prototype.hasOwnProperty.call(wearSegments, 'toString'), false, 'setup: toString is NOT an own key');
  assert.equal('toString' in wearSegments, true, "setup: but `in` sees it — that is the whole hazard");
  // 'toString' is an ORPHAN wear entry (its segment no longer exists).
  const step = ta.roadWearStepFromSnapshot({ 'seg:a': 2, toString: 7 }, wearSegments);
  assert.equal(
    Object.prototype.hasOwnProperty.call(step.nextWearBySegment, 'toString'),
    false,
    'BUG-961/BUG-917(b): an orphan wear entry named after an Object.prototype member must be pruned — MUTANT: restore `if (!(segId in wearSegments))` and this entry survives forever',
  );
  assert.equal(step.nextWearBySegment['seg:a'], 3, 'the real segment still accrues normally (control)');
});

// R4 (supersedes r3's R3_NULLPROTO_DOES_NOT_CROSS_THE_WORKER_BOUNDARY) —
// documents WHY r4 walked r3's null-prototype fix back to plain: a
// null-prototype map's structuredClone/JSON/spread re-parents to
// Object.prototype, so a null-proto ORIGINAL and its own clone differ by
// prototype alone — exactly the asymmetry that broke
// attack-bug950-951-round.test.mjs's clone-side deepStrictEqual pin (worker
// state vs the receiver's structuredClone). A PLAIN map has no such
// asymmetry: its clone is prototype-IDENTICAL to the original, not merely
// coincidentally equal in content.
test('R4 (BUG-961 port amendment): a PLAIN sanitizer-built map keeps an IDENTICAL prototype across structuredClone / JSON / spread — no clone-side asymmetry', () => {
  const p = { toString: 1, 'seg:a': 2 };
  assert.equal(Object.getPrototypeOf(p), Object.prototype, 'setup');
  assert.equal(Object.getPrototypeOf(structuredClone(p)), Object.prototype, 'structuredClone (the worker postMessage path) keeps the SAME prototype');
  assert.equal(Object.getPrototypeOf(JSON.parse(JSON.stringify(p))), Object.prototype, 'a JSON save round trip keeps the SAME prototype');
  assert.equal(Object.getPrototypeOf({ ...p }), Object.prototype, 'an object spread keeps the SAME prototype');
  assert.deepEqual(Object.keys(structuredClone(p)), ['toString', 'seg:a'], 'keys and their order survive too');
  assert.deepStrictEqual(structuredClone(p), p, 'BUG-950/951: a plain map is byte-identical (deepStrictEqual, prototype included) to its own structuredClone — exactly the property the delta-protocol clone-side pin requires');
});

// BUG-973 REGRESSION (was a DEFECT pin as of the r3 ACCEPT) — the r3 LEAD
// RULING named four maps needing the same own-key discipline: wearSegments,
// congestionTicksBySpec, roadWearBySegment and gridlockTicksBySegment.
// gridlockedSegmentsOf (the LIVE producer of gridlockTicksBySegment) was the
// one map the r3 fix missed — a bare `prevGridlockTicks[segId] ?? 0`
// inherited read and a bracket-assigned `{}` accumulator. Fixed in the r4
// port pass alongside the wider plain-object rework: an own-key guard on
// the prev-ticks read, and the accumulator built via Object.fromEntries
// over validated entries (own-data-property semantics), matching every
// other sanitizer/live-producer pair.
test('BUG-973 REGRESSION: gridlockedSegmentsOf — the LIVE producer of gridlockTicksBySegment — now shares the same own-key discipline and PLAIN-object build as the other three maps', async () => {
  const ta = await import('../src/sim/trafficAssignment.ts');
  const d = await import('../src/sim/data.ts');
  const tw = await import('../src/sim/trafficWellbeing.ts');
  // All four maps (sanitize + live-compute directions) are PLAIN
  // (Object.prototype) after r4 — consistent shape everywhere.
  assert.equal(Object.getPrototypeOf(d.sanitizeCongestionTicksBySpec({ a: 1 })), Object.prototype);
  assert.equal(Object.getPrototypeOf(d.sanitizeRoadWearBySegment({ a: 1 })), Object.prototype);
  assert.equal(Object.getPrototypeOf(d.advanceCongestionTicks({}, [])), Object.prototype, 'the LIVE congestion-tick producer stays plain too');
  const base = { tick: 5, medianCommuteMinutes: 10, gridlockShare: 0.1, coverageShare: 0.5 };
  const [known] = [...ta.ROAD_CLASS_IDS];
  assert.equal(Object.getPrototypeOf(tw.sanitizeTrafficSnapshot({ ...base, wearSegments: { 'seg:a': { roadClassId: known, deltaEsalPerTick: 0 } } }).wearSegments), Object.prototype);
  // The fix itself: gridlockedSegmentsOf's `ticks` accumulator is built via
  // Object.fromEntries now (source-level pin — the function needs a full
  // SimState to invoke), and prevGridlockTicks reads go through an own-key
  // guard, never a bare inherited lookup.
  const fn = ta.gridlockedSegmentsOf.toString();
  assert.match(fn, /Object\s*\.\s*fromEntries\s*\(\s*tickEntries\s*\)/, 'BUG-973: gridlockedSegmentsOf now builds its ticks map via Object.fromEntries (own-data-property semantics)');
  assert.match(fn, /Object\s*\.\s*prototype\s*\.\s*hasOwnProperty\s*\.\s*call\s*\(\s*prevGridlockTicks/, 'BUG-973: and reads prevGridlockTicks through an own-key guard now, not a bare inherited lookup');
  assert.doesNotMatch(fn, /const ticks\s*[^=]*=\s*\{\}/, 'MUTANT check: the old bracket-assigned plain accumulator must be gone');
  assert.doesNotMatch(fn, /prevGridlockTicks\[segId\]\s*\?\?\s*0/, 'MUTANT check: the old bare inherited read must be gone');
});

// ===========================================================================
// BUG-941 ROUND 4 (attacker: opus-round4-bug941) — the r4 PORT rework
// (PLAIN maps built with own-data-property semantics + every r3 own-key
// consumer guard kept) verified end to end against the REAL BUG-951/BUG-966
// delta protocol that killed the r3 ACCEPT at port. These pins fix the
// properties nothing else in the tree kills; each names its own mutant.
// ===========================================================================

// Hostile segIds carrying VALID entries — every Object.prototype member name
// plus '__proto__'/'constructor'. Written as raw JSON text on purpose: an
// object literal or a bracket assignment for '__proto__' would invoke the
// inherited setter in the TEST itself and the fixture would silently never
// carry the key at all (measured — the first draft of this pin did exactly
// that). JSON.parse uses CreateDataProperty, so these are real own keys, and
// a save / debug-json blob is exactly this shape.
const R4_HOSTILE = ['__proto__', 'constructor', 'toString', 'hasOwnProperty', 'valueOf', '__defineGetter__', 'isPrototypeOf', 'propertyIsEnumerable'];
const r4q = (k) => JSON.stringify(k);
const r4WearJson = (cls) => '{' + R4_HOSTILE.map((k) => r4q(k) + ':{"roadClassId":"' + cls + '","deltaEsalPerTick":1.5}').join(',') + ',"seg:real1":{"roadClassId":"' + cls + '","deltaEsalPerTick":2}}';
const r4PrevJson = '{' + R4_HOSTILE.map((k) => r4q(k) + ':3').join(',') + ',"seg:real1":5e9,"seg:real2":3}';
const r4Snap = (tick, wear) => ({ tick, medianCommuteMinutes: 10, gridlockShare: 0.1, coverageShare: 0.5, safeRoadScore: 1, integratedTransportScore: 0, wearSegments: wear });
const r4ProtoNames = () => Object.getOwnPropertyNames(Object.prototype).sort().join(',');

// R4_OWN_DATA — the whole point of the r4 ruling: PLAIN objects, but every
// key an OWN DATA property, for names that are inherited members of
// Object.prototype. Proven at the descriptor level (not just `k in m`, which
// an inherited member satisfies for free) and across every transport the
// pipeline actually uses.
test('R4_OWN_DATA (BUG-941 port ruling): hostile segIds with valid entries become OWN DATA properties of a PLAIN map, and stay own data through structuredClone / JSON / spread', async () => {
  const tw = await import('../src/sim/trafficWellbeing.ts');
  const d = await import('../src/sim/data.ts');
  const ta = await import('../src/sim/trafficAssignment.ts');
  const [cls] = [...ta.ROAD_CLASS_IDS];
  const before = r4ProtoNames();
  const san = tw.sanitizeTrafficSnapshot(JSON.parse('{"tick":5,"medianCommuteMinutes":20,"gridlockShare":0.1,"coverageShare":0.5,"wearSegments":' + r4WearJson(cls) + '}'));
  const ws = san.wearSegments;
  assert.notEqual(ws, undefined, 'setup: a VALID entry under a hazardous key is ordinary data post-r3, never poison');
  assert.equal(Object.getPrototypeOf(ws), Object.prototype, 'r4: the map must be PLAIN — a null-prototype map clones to plain and breaks the delta protocol clone-side pin');
  for (const k of [...R4_HOSTILE, 'seg:real1']) {
    const desc = Object.getOwnPropertyDescriptor(ws, k);
    assert.ok(desc, 'r4: ' + k + ' must be an OWN property — MUTANT: replace Object.fromEntries(wearSegmentEntries) with a bracket-assigned {} accumulator and __proto__ re-parents the map instead');
    assert.ok('value' in desc && desc.writable && desc.enumerable && desc.configurable, 'r4: ' + k + ' must be an own DATA property, not an accessor');
    assert.equal(typeof ws[k].deltaEsalPerTick, 'number', 'r4: ' + k + ' must read back as real numeric wear, never an inherited Object.prototype member');
  }
  const keys = Object.getOwnPropertyNames(ws).sort().join(',');
  for (const [name, m] of [['structuredClone', structuredClone(ws)], ['json', JSON.parse(JSON.stringify(ws))], ['spread', { ...ws }]]) {
    assert.equal(Object.getOwnPropertyNames(m).sort().join(','), keys, 'r4: ' + name + ' must preserve every own key');
    assert.equal(Object.getPrototypeOf(m), Object.prototype, 'r4: ' + name + ' must preserve the prototype — the r3 ACCEPT died on exactly this asymmetry');
  }
  assert.deepStrictEqual(structuredClone(ws), ws, 'r4: worker state and the receiver-side structuredClone must be deepStrictEqual (BUG-950/951 clone-side pin)');
  // The other two sanitizers take the identical treatment.
  const numJson = JSON.parse('{' + R4_HOSTILE.map((k) => r4q(k) + ':4').join(',') + '}');
  for (const [name, fn] of [['sanitizeCongestionTicksBySpec', d.sanitizeCongestionTicksBySpec], ['sanitizeRoadWearBySegment', d.sanitizeRoadWearBySegment]]) {
    const o = fn(numJson);
    assert.equal(Object.getPrototypeOf(o), Object.prototype, 'r4: ' + name + ' output must be plain');
    for (const k of R4_HOSTILE) {
      assert.ok(Object.prototype.hasOwnProperty.call(o, k), 'r4: ' + name + ' must make ' + k + ' an OWN key');
      assert.equal(typeof o[k], 'number', 'r4: ' + name + '.' + k + ' must be numeric');
    }
  }
  assert.equal(r4ProtoNames(), before, 'r4: Object.prototype must be byte-identical before and after — no global pollution on any path');
});

// R4_STEP_ACCRUAL — the consumer side. Every own-key guard r3 added is the
// ONLY defence now that the maps are plain again, so this pin drives a real
// roadWearStepFromSnapshot over hostile-key PRIOR wear and hostile-key
// inputs: real accrual (never a silent drop), numeric everywhere (never an
// inherited function coerced to a string), the orphan pruned, the due repair
// booked exactly once.
test('R4_STEP_ACCRUAL (BUG-941/BUG-961): roadWearStepFromSnapshot accrues hostile-key wear as numbers, prunes the orphan, and books the due repair once', async () => {
  const ta = await import('../src/sim/trafficAssignment.ts');
  const tw = await import('../src/sim/trafficWellbeing.ts');
  const [cls] = [...ta.ROAD_CLASS_IDS];
  const before = r4ProtoNames();
  const ws = tw.sanitizeTrafficSnapshot(JSON.parse('{"tick":5,"medianCommuteMinutes":20,"gridlockShare":0.1,"coverageShare":0.5,"wearSegments":' + r4WearJson(cls) + '}')).wearSegments;
  const step = ta.roadWearStepFromSnapshot(JSON.parse(r4PrevJson), ws);
  assert.equal(Object.getPrototypeOf(step.nextWearBySegment), Object.prototype, 'r4: the step output must be a plain object (the Map -> fromEntries conversion)');
  for (const k of Object.getOwnPropertyNames(step.nextWearBySegment)) {
    const v = step.nextWearBySegment[k];
    assert.equal(typeof v, 'number', 'r4/BUG-961: nextWear.' + k + ' must stay numeric — MUTANT: restore the bare prevWear[segId] ?? 0 read and an inherited member is string-concatenated in instead');
    assert.ok(Number.isFinite(v), 'r4: nextWear.' + k + ' must be finite');
  }
  for (const k of R4_HOSTILE) {
    assert.ok(Object.prototype.hasOwnProperty.call(step.nextWearBySegment, k), 'r4: ' + k + ' carried real wear in and must carry accrued wear out — a silent drop IS the BUG-941 free wipe');
    assert.equal(step.nextWearBySegment[k], 3 + 1.5, 'r4: ' + k + ' must accrue prior 3 + delta 1.5 exactly');
  }
  assert.ok(!Object.prototype.hasOwnProperty.call(step.nextWearBySegment, 'seg:real2'), 'r4/BUG-917(b): an orphan (no cadence entry, under trigger) is still pruned');
  const real1 = step.repairEvents.filter((e) => e.segmentId === 'seg:real1');
  assert.equal(real1.length, 1, 'r4: the over-trigger segment books EXACTLY ONE repair event, never two');
  assert.ok(!Object.prototype.hasOwnProperty.call(step.nextWearBySegment, 'seg:real1'), 'r4/AC-6: and its wear resets');
  assert.equal(r4ProtoNames(), before, 'r4: Object.prototype untouched');
});

// R4_BOOT_PATHS — every load boundary at once (BUG-948 + BUG-962 carry-over),
// on the shape r4 actually changed: VALID data under hazardous names. The
// corrupt-data direction stays pinned by the BUG-962 REGRESSION test above;
// this one proves the boundary does not EAT good data either.
test('R4_BOOT_PATHS (BUG-948/BUG-962): restoreFromSavepoint, prepareRestoreForChunkedTail and parseGameSave all preserve hostile-key entries as own data, and the first tick books normally', async () => {
  const { createSavepoint, persistSavepointForced, restoreFromSavepoint, prepareRestoreForChunkedTail } = await import('../src/sim/replay.ts');
  const { buildGameSave, gameSaveText, parseGameSave } = await import('../src/sim/gamesave.ts');
  const { initialState: init, reducer: red, sanitizeTreasury } = await import('../src/sim/engine.ts');
  const ta = await import('../src/sim/trafficAssignment.ts');
  const [cls] = [...ta.ROAD_CLASS_IDS];
  class Mem {
    constructor() { this.m = new Map(); }
    getItem(k) { return this.m.has(k) ? this.m.get(k) : null; }
    setItem(k, v) { this.m.set(k, String(v)); }
    removeItem(k) { this.m.delete(k); }
    key(i) { return [...this.m.keys()][i] ?? null; }
    get length() { return this.m.size; }
  }
  const before = r4ProtoNames();
  const base = init();
  const mk = () => ({ ...base, roadWearBySegment: JSON.parse(r4PrevJson), trafficSnapshot: r4Snap(base.tick, JSON.parse(r4WearJson(cls))) });
  const storage = new Mem();
  persistSavepointForced(storage, createSavepoint(mk(), [], new Date(), 'test', null));
  const restored = restoreFromSavepoint(storage);
  assert.equal(restored.success, true, 'setup: the savepoint must restore');
  const booted = sanitizeTreasury(restored.state);
  const prepared = prepareRestoreForChunkedTail(storage);
  const parsed = parseGameSave(gameSaveText(buildGameSave({ state: mk(), journal: { entries: [] }, journalTail: [], name: 'r4', buildVersion: 'test' })));
  assert.equal(parsed.ok, true, 'setup: the game save must parse');
  for (const [name, snap] of [['restoreFromSavepoint', booted], ['prepareRestoreForChunkedTail', prepared.state], ['parseGameSave', parsed.save.savepoint.snapshot]]) {
    const ws = snap.trafficSnapshot && snap.trafficSnapshot.wearSegments;
    assert.notEqual(ws, undefined, name + ': valid entries under hazardous names must SURVIVE the boundary, not be eaten');
    assert.equal(Object.getPrototypeOf(ws), Object.prototype, name + ': and the map must be plain');
    for (const k of R4_HOSTILE) assert.ok(Object.prototype.hasOwnProperty.call(ws, k), name + ': lost own key ' + k + ' at the load boundary');
  }
  // One real tick on the booted state: never throws, wear stays numeric and
  // finite, and the over-trigger segment resurfaces (money booked, not wiped).
  const after = red(booted, { type: 'tick' });
  for (const k of Object.getOwnPropertyNames(after.roadWearBySegment)) {
    assert.equal(typeof after.roadWearBySegment[k], 'number', 'r4: post-boot roadWearBySegment.' + k + ' must stay numeric');
    assert.ok(Number.isFinite(after.roadWearBySegment[k]), 'r4: post-boot roadWearBySegment.' + k + ' must stay finite');
  }
  assert.ok(!Object.prototype.hasOwnProperty.call(after.roadWearBySegment, 'seg:real1'), 'r4: the over-trigger segment resurfaced on the first post-boot tick');
  assert.ok((after.lastFlows && after.lastFlows.roadRepairGbp) > 0, 'r4: and the resurfacing was BOOKED (roadRepairGbp > 0), never a free wipe');
  assert.equal(r4ProtoNames(), before, 'r4: Object.prototype untouched by any boot path');
});

// R4_DELTA_120 — the exact pin the r3 ACCEPT failed at port. Worker state
// and a receiver that only ever sees structuredClone(diffSimState(...)) must
// stay byte-identical for a long run booted from a savepoint whose wear maps
// carry hostile keys, and every worker state must be deepStrictEqual to its
// own structuredClone (the prototype-symmetry property the null-prototype r3
// fix destroyed).
test('R4_DELTA_120 (BUG-950/BUG-951/BUG-966 port): 120 ticks of worker -> structuredClone(delta) -> receiver stay byte-identical with hostile-key wear maps aboard', async () => {
  const { initialState: init, reducer: red } = await import('../src/sim/engine.ts');
  const { diffSimState, applyStateDelta } = await import('../src/sim/simWorkerDelta.ts');
  const ta = await import('../src/sim/trafficAssignment.ts');
  const [cls] = [...ta.ROAD_CLASS_IDS];
  const before = r4ProtoNames();
  const seed = init();
  let s = { ...seed, roadWearBySegment: JSON.parse(r4PrevJson), trafficSnapshot: r4Snap(seed.tick, JSON.parse(r4WearJson(cls))) };
  let ui = structuredClone(s);
  for (let t = 0; t < 120; t++) {
    const basis = s;
    const next = red(s, { type: 'tick' });
    ui = applyStateDelta(ui, structuredClone(diffSimState(basis, next)));
    s = next;
    assert.equal(JSON.stringify(ui), JSON.stringify(s), 'r4: receiver diverged from the worker at tick ' + t);
    assert.deepStrictEqual(structuredClone(s), s, 'r4: worker state is not deepStrictEqual to its own structuredClone at tick ' + t + ' — MUTANT: rebuild any of these maps with Object.create(null) and this reds by prototype alone, which is exactly how the r3 ACCEPT died at port');
  }
  assert.equal(r4ProtoNames(), before, 'r4: 120 ticks of hostile-key state must never touch Object.prototype');
});

// R4_BUILD_SHAPE — a source-level backstop so the own-data-property build
// cannot be quietly reverted to a bracket-assigned accumulator by a later
// edit that happens not to be covered by a behavioural fixture. Every one of
// these producers is named in the r4 ruling.
test('R4_BUILD_SHAPE: every sanitizer / live producer of these maps finishes with Object.fromEntries and never bracket-assigns the accumulator', async () => {
  const d = await import('../src/sim/data.ts');
  const ta = await import('../src/sim/trafficAssignment.ts');
  const producers = [
    ['sanitizeCongestionTicksBySpec', d.sanitizeCongestionTicksBySpec.toString()],
    ['sanitizeRoadWearBySegment', d.sanitizeRoadWearBySegment.toString()],
    ['advanceCongestionTicks', d.advanceCongestionTicks.toString()],
    ['roadWearStepFromSnapshot', ta.roadWearStepFromSnapshot.toString()],
  ];
  // Strip `//` comments first: each of these functions DOCUMENTS the banned
  // shape in prose (e.g. "bracket assignment (`nextWear[segId] = ...`)"), so
  // a raw source scan reds on the explanation rather than on real code — the
  // exact false positive this pin hit when first written.
  // (CRLF-safe: `.` and `$` both stop at a bare \r, so an anchored per-line
  // strip silently does nothing on this repo's CRLF sources — BUG-port
  // hygiene lesson, use an unanchored class that excludes both terminators.)
  const stripComments = (src) => src.replace(/\/\/[^\n\r]*/g, '');
  for (const [name, raw] of producers) {
    const src = stripComments(raw);
    assert.match(src, /Object\s*\.\s*fromEntries\s*\(/, name + ': must finish via Object.fromEntries (CreateDataProperty — an own key even for "__proto__")');
    assert.doesNotMatch(src, /\b(out|entries|ticks|nextWear)\s*\[\s*(spec|segId|u\.spec|k|key)\s*\]\s*=[^=]/, name + ': must never bracket-assign its accumulator — that invokes the inherited __proto__ setter');
  }
  // advanceCongestionTicks' prev read is an own-key test too (its keys are
  // spec ids today, so this is not reachable from live data — pinned anyway
  // so the shape cannot drift back; a surviving mutant here was a RECORDED
  // GAP of this round).
  assert.match(producers[2][1], /Object\s*\.\s*prototype\s*\.\s*hasOwnProperty\s*\.\s*call\s*\(\s*prevTicks/, 'advanceCongestionTicks: prevTicks must be read through an own-key guard');
});

// R4_BUG974 (FIXED): decodeSavepointBytes now guards each of the four fields
// (trafficSnapshot/congestionTicksBySpec/roadWearBySegment/
// gridlockTicksBySegment) with an own-key check before reassigning the
// sanitized value, so a pre-inc7 savepoint that never carried these keys no
// longer gains them at decode (previously it always did — one of them
// `trafficSnapshot: undefined`, the other three freshly-invented empty `{}`
// maps — which is exactly why save/load/save was never byte-identical for
// such a save; see BUG-974's own comment on the fix). Was pinned as a
// RECORDED GAP in the direction the bug actually behaved; flipped here now
// that the gap is closed.
test('R4_BUG974 (FIXED): decodeSavepointBytes no longer invents own keys on a pre-inc7 savepoint — value-safe, key-order-safe, AND byte-identical', async () => {
  const { createSavepoint, persistSavepointForced, restoreFromSavepoint } = await import('../src/sim/replay.ts');
  const { initialState: init, reducer: red } = await import('../src/sim/engine.ts');
  class Mem {
    constructor() { this.m = new Map(); }
    getItem(k) { return this.m.has(k) ? this.m.get(k) : null; }
    setItem(k, v) { this.m.set(k, String(v)); }
    removeItem(k) { this.m.delete(k); }
    key(i) { return [...this.m.keys()][i] ?? null; }
    get length() { return this.m.size; }
  }
  const legacy = { ...init() };
  for (const f of ['trafficSnapshot', 'congestionTicksBySpec', 'roadWearBySegment', 'gridlockTicksBySegment']) delete legacy[f];
  const beforeKeys = Object.keys(legacy);
  const storage = new Mem();
  persistSavepointForced(storage, createSavepoint(legacy, [], new Date(), 'test', null));
  const restored = restoreFromSavepoint(storage);
  assert.equal(restored.success, true, 'setup: the legacy savepoint must restore');
  const afterKeys = Object.keys(restored.state);
  assert.deepEqual(afterKeys.filter((k) => !beforeKeys.includes(k)), [], 'BUG-974 (fixed): decode must invent NO own keys on a pre-inc7 savepoint');
  assert.equal(Object.prototype.hasOwnProperty.call(restored.state, 'trafficSnapshot'), false, 'BUG-974 (fixed): no fabricated trafficSnapshot own key');
  assert.deepEqual(afterKeys.filter((k) => beforeKeys.includes(k)), beforeKeys, 'BUG-974 is bounded: the ORDER of every pre-existing key is untouched');
  let a = restored.state, b = legacy;
  for (let i = 0; i < 10; i++) { a = red(a, { type: 'tick' }); b = red(b, { type: 'tick' }); }
  const canon = (o) => JSON.stringify(o, Object.keys(o).sort());
  assert.equal(canon(a), canon(b), 'BUG-974 is value-safe: 10 ticks from a decoded legacy savepoint are canonically identical to the same state fed straight to the reducer');
});

// ═══════════════ BUG-974 INDEPENDENT ROUND (opus-round-bug974) ═══════════
// Lasting pins from the independent destructive round on BUG-974 (the ONE
// canonical TRAFFIC_SNAPSHOT_KEY_ORDER + canonicalSnapshot() builder, and
// the present-only hasOwnProperty-guarded decode in replay.ts/gamesave.ts).
// Every pin below was proven to RED against at least one scratch mutant of
// the fix (7/7 mutants red: shuffled order constant, dropped decode guard on
// trafficSnapshot and on roadWearBySegment, compute bypassing
// canonicalSnapshot, sanitize bypassing it, gamesave's unconditional
// spread+assign, and canonicalSnapshot copying keys without the own-key
// guard).

/**
 * The canonical order as a HARD-CODED literal, deliberately NOT read from
 * TRAFFIC_SNAPSHOT_KEY_ORDER: asserting Object.keys() against the constant
 * itself is a tautology that stays green when the constant and
 * canonicalSnapshot are shuffled TOGETHER (measured — a scratch mutant that
 * swapped `tick`/`medianCommuteMinutes` in the constant left the first draft
 * of ROUND_BUG974_D and _E GREEN).
 */
const ROUND_BUG974_EXPECTED_ORDER = [
  'tick',
  'medianCommuteMinutes',
  'p90CommuteMinutes',
  'gridlockShare',
  'coverageShare',
  'coverageShareByService',
  'vOverCBySegment',
  'safeRoadScore',
  'integratedTransportScore',
  'fuelLitresDemanded',
  'vedAnnualGbp',
  'wearSegments',
];

/** Shared fixtures for the BUG-974 round pins. */
async function bug974Fixtures() {
  const { reducer, initialState } = await import('../src/sim/engine.ts');
  const { emptyJournal } = await import('../src/sim/journal.ts');
  const gs = await import('../src/sim/gamesave.ts');
  const tw = await import('../src/sim/trafficWellbeing.ts');
  const rp = await import('../src/sim/replay.ts');
  const text = (state) =>
    gs.gameSaveText(gs.buildGameSave({ state, journal: emptyJournal(), journalTail: [], name: 'n', buildVersion: 'v', now: new Date(0) }));
  const routedCity = (extraTicks) => {
    let s = initialState();
    s = reducer(s, { type: 'debugFunds', amount: 500_000_000 });
    s = reducer(s, { type: 'unlockAll' });
    const roadTiles = [];
    for (let x = 0; x <= 25; x++) roadTiles.push({ x, y: 10 });
    roadTiles.push({ x: 10, y: 11 });
    roadTiles.push({ x: 20, y: 11 });
    s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: roadTiles });
    s = reducer(s, { type: 'place', spec: 'res_estate', x: 10, y: 12 });
    s = reducer(s, { type: 'place', spec: 'com_shop', x: 20, y: 12 });
    for (let i = 0; i < extraTicks; i++) s = reducer(s, { type: 'tick' });
    return s;
  };
  const preInc7 = (state) => {
    const o = { ...state };
    for (const f of ['trafficSnapshot', 'congestionTicksBySpec', 'roadWearBySegment', 'gridlockTicksBySegment']) delete o[f];
    return o;
  };
  return { reducer, initialState, gs, tw, rp, text, routedCity, preInc7 };
}

/**
 * ROUND_BUG974_A — the identity sweep the round was commissioned to run:
 * save -> load -> save -> load -> save must be byte-identical for FOUR
 * different snapshot shapes, not just the one the author's own fixture
 * happened to produce. (c) is the shape the author's tests never built — a
 * snapshot carrying the required fields but NONE of inc7's three optionals,
 * i.e. the exact partial shape the sanitizer emits after poisoning
 * wearSegments (BUG-941) — which is where an order constant that only
 * agreed on the FULL key set would still drift.
 */
test('ROUND_BUG974_A: save/load/save is byte-identical for initial, routed, optional-less and pre-inc7 states', async () => {
  const { initialState, gs, tw, text, routedCity, preInc7 } = await bug974Fixtures();
  const c = routedCity(2 * tw.TRAFFIC_RECOMPUTE_TICKS + 3);
  const snapNoOpt = { ...c.trafficSnapshot };
  for (const f of ['fuelLitresDemanded', 'vedAnnualGbp', 'wearSegments']) delete snapNoOpt[f];
  const cases = {
    a_initial: initialState(),
    b_routed: routedCity(2 * tw.TRAFFIC_RECOMPUTE_TICKS + 3),
    c_noOptional: { ...c, trafficSnapshot: snapNoOpt },
    d_preInc7: preInc7(routedCity(5)),
  };
  for (const [name, s] of Object.entries(cases)) {
    const t1 = text(s);
    const p1 = gs.parseGameSave(t1);
    assert.equal(p1.ok, true, name + ': setup, the save must parse');
    const t2 = text(p1.save.savepoint.snapshot);
    assert.equal(t1, t2, name + ': save/load/save is NOT byte-identical');
    const t3 = text(gs.parseGameSave(t2).save.savepoint.snapshot);
    assert.equal(t2, t3, name + ': a third save/load pass drifted');
  }

  // (e) a genuinely pre-inc7 FILE: the four keys stripped from the persisted
  // JSON itself, not merely from the in-memory SimState -- this is the case
  // gamesave.ts's own present-only ternary exists for, and the one that reds
  // if that ternary regresses to the old unconditional spread+assign.
  const obj = JSON.parse(text(preInc7(routedCity(5))));
  for (const f of ['trafficSnapshot', 'congestionTicksBySpec', 'roadWearBySegment', 'gridlockTicksBySegment']) delete obj.savepoint.snapshot[f];
  const beforeKeys = Object.keys(obj.savepoint.snapshot);
  const decoded = gs.parseGameSave(JSON.stringify(obj)).save.savepoint.snapshot;
  assert.deepEqual(Object.keys(decoded).filter((k) => !beforeKeys.includes(k)), [], 'e_preInc7File: parseGameSave invented own keys on a pre-inc7 save file');
  const e1 = text(decoded);
  const e2 = text(gs.parseGameSave(e1).save.savepoint.snapshot);
  assert.equal(e1, e2, 'e_preInc7File: save/load/save is NOT byte-identical for a pre-inc7 save file');
});

/**
 * ROUND_BUG974_B — sanitizeTrafficSnapshot must be IDEMPOTENT (deep AND
 * byte-wise) over the BUG-941 corruption shapes, must never emit an own key
 * whose value is `undefined` (JSON drops such a key while structuredClone
 * keeps it — that asymmetry is precisely BUG-974's failure mode), and must
 * emit whatever subset of fields it does keep in canonical order.
 */
test('ROUND_BUG974_B: sanitizeTrafficSnapshot is idempotent, canonically ordered, and emits no undefined-valued own keys', async () => {
  const { tw } = await bug974Fixtures();
  const full = {
    tick: 5, medianCommuteMinutes: 40, gridlockShare: 0.2, coverageShare: 0.5,
    safeRoadScore: 0.9, integratedTransportScore: 0.4, p90CommuteMinutes: 55,
    vOverCBySegment: { seg1: 0.8 },
    coverageShareByService: { ambulance: 0.5, fire: 0.6, police: 0.7 },
    fuelLitresDemanded: 100, vedAnnualGbp: 200,
    wearSegments: { seg1: { roadClassId: 'motorway', deltaEsalPerTick: 1 } },
  };
  const shapes = {
    full,
    badWearClass: { ...full, wearSegments: { seg1: { roadClassId: 'nope', deltaEsalPerTick: 1 } } },
    emptyWear: { ...full, wearSegments: {} },
    nullWear: { ...full, wearSegments: null },
    negativeFuel: { ...full, fuelLitresDemanded: -1 },
    nanVed: { ...full, vedAnnualGbp: NaN },
    protoWear: { ...full, wearSegments: JSON.parse('{"__proto__":{"roadClassId":"motorway","deltaEsalPerTick":1}}') },
    nullCoverage: { ...full, coverageShare: null },
    stringTick: { ...full, tick: '5' },
    hugeMedian: { ...full, medianCommuteMinutes: 1e9 },
  };
  for (const [name, v] of Object.entries(shapes)) {
    const s1 = tw.sanitizeTrafficSnapshot(v);
    const s2 = tw.sanitizeTrafficSnapshot(s1);
    assert.deepStrictEqual(s2, s1, name + ': sanitizeTrafficSnapshot is not idempotent (deep)');
    assert.equal(JSON.stringify(s2), JSON.stringify(s1), name + ': sanitizeTrafficSnapshot is not idempotent (bytes)');
    // A shape whose REQUIRED fields fail validation (stringTick) is honestly
    // rejected wholesale -- `undefined`, no object to inspect. That is the
    // BUG-877/GR#16 contract, already pinned elsewhere; the order/undefined-key
    // assertions below only apply to the shapes that survive.
    if (!s1) continue;
    const keys = Object.keys(s1);
    assert.deepEqual(keys, ROUND_BUG974_EXPECTED_ORDER.filter((k) => keys.includes(k)), name + ': surviving keys are not in canonical order');
    for (const k of keys) assert.notEqual(s1[k], undefined, name + ': own key "' + k + '" carries an undefined value');
  }
});

/**
 * ROUND_BUG974_C — the SAVEPOINT path (createSavepoint ->
 * persistSavepointForced -> restoreFromSavepoint -> persist again), which is
 * a different decoder (decodeSavepointBytes) from the gamesave path pinned
 * in _A: the persisted bytes must be identical across the round trip and the
 * restored state's key SET AND ORDER must equal the original's.
 */
test('ROUND_BUG974_C: savepoint persist/restore/persist is byte-identical and invents no keys', async () => {
  const { initialState, tw, rp, routedCity, preInc7 } = await bug974Fixtures();
  class Mem {
    constructor() { this.m = new Map(); }
    getItem(k) { return this.m.has(k) ? this.m.get(k) : null; }
    setItem(k, v) { this.m.set(k, String(v)); }
    removeItem(k) { this.m.delete(k); }
    key(i) { return [...this.m.keys()][i] ?? null; }
    get length() { return this.m.size; }
  }
  const now = new Date();
  const cases = {
    a_initial: initialState(),
    b_routed: routedCity(2 * tw.TRAFFIC_RECOMPUTE_TICKS + 3),
    d_preInc7: preInc7(routedCity(5)),
  };
  for (const [name, s] of Object.entries(cases)) {
    const st1 = new Mem();
    rp.persistSavepointForced(st1, rp.createSavepoint(s, [], now, 'test', null));
    const raw1 = st1.getItem(st1.key(0));
    const r = rp.restoreFromSavepoint(st1);
    assert.equal(r.success, true, name + ': setup, the savepoint must restore');
    const st2 = new Mem();
    rp.persistSavepointForced(st2, rp.createSavepoint(r.state, [], now, 'test', null));
    assert.equal(raw1, st2.getItem(st2.key(0)), name + ': savepoint persist/restore/persist is NOT byte-identical');
    assert.deepEqual(Object.keys(r.state), Object.keys(s), name + ': the restored state key set/order differs from the original');
  }
});

/**
 * ROUND_BUG974_D — the LIVE producer (computeTrafficSnapshot on a real
 * routed city) and the sanitizer must agree on the key set after
 * structuredClone, and the live snapshot must survive the JSON/clone key
 * parity check: an own key valued `undefined` would appear under
 * structuredClone but vanish under JSON.stringify, which is exactly the
 * asymmetry that made save/load/save drift.
 */
test('ROUND_BUG974_D: live producer and sanitizer agree on keys through structuredClone and JSON', async () => {
  const { tw, routedCity } = await bug974Fixtures();
  const produced = routedCity(2 * tw.TRAFFIC_RECOMPUTE_TICKS + 3).trafficSnapshot;
  assert.ok(produced, 'setup: a cadence tick must have populated trafficSnapshot');
  const sanitized = tw.sanitizeTrafficSnapshot(JSON.parse(JSON.stringify(produced)));
  assert.deepEqual(Object.keys(structuredClone(produced)), Object.keys(structuredClone(sanitized)), 'producer and sanitizer clone to different key sets');
  assert.deepEqual(ROUND_BUG974_EXPECTED_ORDER, [...tw.TRAFFIC_SNAPSHOT_KEY_ORDER], 'the exported order constant drifted from the order this round measured');
  assert.deepEqual(Object.keys(produced), ROUND_BUG974_EXPECTED_ORDER, 'the live producer key order is off-canon');
  for (const k of Object.keys(produced)) assert.notEqual(produced[k], undefined, 'the live producer emitted an undefined-valued own key: ' + k);
  assert.deepEqual(Object.keys(JSON.parse(JSON.stringify(produced))), Object.keys(structuredClone(produced)), 'JSON/structuredClone key parity is broken on the live snapshot');
});

/**
 * ROUND_BUG974_E — the THIRD TrafficSnapshot builder the fix did NOT route
 * through canonicalSnapshot: engine.ts's BUG-949 bootstrap
 * `{ ...trafficSnapshot, wearSegments: wearSegmentInputsOf(s) }`. It is
 * order-safe TODAY only because `wearSegments` happens to be LAST in
 * TRAFFIC_SNAPSHOT_KEY_ORDER, so re-appending it lands it back in its
 * canonical slot. This pin makes that accident load-bearing and visible: add
 * an optional field AFTER wearSegments in the order constant without routing
 * this spread through canonicalSnapshot and this test reds (see the P3
 * follow-up filed by the round).
 */
test('ROUND_BUG974_E: the BUG-949 wearSegments bootstrap spread still yields a canonically-ordered snapshot', async () => {
  const { reducer, gs, tw, text, routedCity } = await bug974Fixtures();
  const s = routedCity(2 * tw.TRAFFIC_RECOMPUTE_TICKS + 3);
  const snapNoWear = { ...s.trafficSnapshot };
  delete snapNoWear.wearSegments;
  const bootstrapped = reducer({ ...s, trafficSnapshot: snapNoWear }, { type: 'tick' });
  assert.ok(bootstrapped.trafficSnapshot.wearSegments, 'setup: the bootstrap must have re-populated wearSegments');
  assert.deepEqual(Object.keys(bootstrapped.trafficSnapshot), ROUND_BUG974_EXPECTED_ORDER, 'the BUG-949 bootstrap spread produced a non-canonical key order');
  const t1 = text(bootstrapped);
  const t2 = text(gs.parseGameSave(t1).save.savepoint.snapshot);
  assert.equal(t1, t2, 'a state produced by the BUG-949 bootstrap does not round-trip byte-identically');
});

/**
 * ROUND_BUG974_F (RECORDED GAP, green as written) — BUG-974's fix closes the
 * ABSENT-key half of the class (a pre-inc7 save no longer gains keys) but
 * NOT the PRESENT-but-corrupt half: when the raw save DOES carry a
 * `trafficSnapshot` key whose value is invalid (null / {} / a number / a
 * string), the own-key guard passes, sanitizeTrafficSnapshot returns
 * `undefined`, and the assignment leaves an own key valued `undefined` —
 * which structuredClone preserves and JSON.stringify drops, so the decoded
 * state and its own re-serialisation are not deepStrictEqual. Measured, not
 * theorised. Pinned in the direction it actually behaves so the day the
 * follow-up lands this test fails loudly and is flipped.
 */
test('ROUND_BUG974_F (RECORDED GAP): a present-but-corrupt trafficSnapshot still decodes to an own key valued undefined', async () => {
  const { rp, routedCity } = await bug974Fixtures();
  const s = routedCity(5);
  const plain = JSON.parse(JSON.stringify(s));
  for (const bad of [null, {}, 7, 'x']) {
    const raw = JSON.stringify({
      v: 1, slot: 0, savedAt: new Date().toISOString(), snapshotTick: s.tick,
      buildVersion: 'v', lineageId: 'l', saveSeq: 1,
      snapshot: { ...plain, trafficSnapshot: bad }, journalTail: [],
    });
    const d = rp.decodeSavepointBytes(raw).snapshot;
    const label = JSON.stringify(bad);
    assert.equal(Object.prototype.hasOwnProperty.call(d, 'trafficSnapshot'), true, label + ': the key was present going in, so it stays present');
    assert.equal(d.trafficSnapshot, undefined, label + ': BUG-974 residual — the corrupt value sanitizes to undefined but the own key survives');
    const viaClone = structuredClone(d);
    const viaJson = JSON.parse(JSON.stringify(d));
    assert.equal(Object.prototype.hasOwnProperty.call(viaClone, 'trafficSnapshot'), true, label + ': structuredClone keeps the undefined-valued key');
    assert.equal(Object.prototype.hasOwnProperty.call(viaJson, 'trafficSnapshot'), false, label + ': JSON.stringify drops it — the asymmetry this gap records');
    assert.throws(() => assert.deepStrictEqual(viaClone, viaJson), /deep-equal/, label + ': the clone-side and JSON-side states must currently differ');
  }
});
