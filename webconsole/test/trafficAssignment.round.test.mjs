// trafficAssignment.round.test.mjs — FEAT-2326609796 inc3 r2 REJECT
// (opus-reround-feat796-inc3, row 7602) attacker probes, promoted into the
// suite per the r3 rework brief. Two independent checks:
//
//  1. Hand-physics re-derivation: t0 (free-flow minutes) and assignedFlowOf
//     are re-computed here from the RAW data files (data/traffic.json,
//     data/roads.json, data/traffic/link_capacity.json,
//     data/traffic/vehicle_classes.json) using the same public-data formulas
//     the acceptance doc states, WITHOUT importing any internal constant or
//     helper from trafficAssignment.ts (ROAD_CLASS_ID_OF_TIER,
//     roadClassIdOfSegment, etc. are all module-private) — an attacker who
//     never reads trafficAssignment.ts's source, only its data contracts and
//     the acceptance doc, can still independently reconstruct the expected
//     numbers and catch a formula regression.
//  2. Distributed-grid city-scale sanity bound: a 24x24 alternating
//     rd_aroad/rd_dual grid (checkerboard — every orthogonal neighbour is a
//     different spec, so every tile is its OWN 1-tile segment, densely
//     interconnected via segmentAdjacencyOf) at population 20,000 must never
//     produce an overloaded (v/c >= 1) segment — a basic "a generously
//     provisioned road mesh does not spuriously gridlock" sanity check the
//     small hand fixtures elsewhere in this suite cannot exercise.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import {
  assignedFlowOf,
  segmentFreeFlowMinutesOf,
  segmentDelayOf,
  unroutedDemandOf,
} from '../src/sim/trafficAssignment.ts';
import { SPECS, lineSegmentIndexOf } from '../src/sim/data.ts';
import { demandForecastOf, ladderPointOf, modeShareOf } from '../src/sim/trafficDemand.ts';
import { initialState } from '../src/sim/engine.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, '..', '..');
const trafficConfig = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic.json'), 'utf8'));
const linkCapacity = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'link_capacity.json'), 'utf8'));
const roads = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'roads.json'), 'utf8'));
const vehicleClasses = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'vehicle_classes.json'), 'utf8'));

function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}
// BUG-857 lesson (mirrors trafficAssignment.test.mjs's own OFFSET note):
// fixtures around (0,0) can chain adjacent rows into one physical segment
// via lineSegmentIndexOf's own 4-neighbour flood. OFFSET keeps every
// coordinate map-legal and non-negative.
const OFFSET = 200;
function rd(id, spec, x, y) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET, builtTick: 0 };
}
function bldg(id, spec, x, y) {
  return { id, spec, x: x + OFFSET, y: y + OFFSET };
}
function k(x, y) {
  return `${x + OFFSET},${y + OFFSET}`;
}

// ---------------------------------------------------------------------------
// Probe 1: hand-physics re-derivation (independent of module internals)
// ---------------------------------------------------------------------------

test('ROUND: t0 and assignedFlowOf are independently re-derivable from RAW data files alone (no module-internal helper imported)', () => {
  // Fixture: one origin tile, a 2-segment path (m20 then rd_dual) to one job
  // tile — small enough to hand-verify every number.
  const buildings = [
    bldg(1, 'res_hut', -1, 0),
    rd(2, 'm20', 0, 0),
    rd(3, 'rd_dual', 1, 0),
    bldg(4, 'off_suite', 1, 1),
  ];
  const s = board(buildings, 50000);
  const idx = lineSegmentIndexOf(s);
  const s1 = idx.segmentById.get(idx.tileToSegment.get(k(0, 0))); // m20 -> motorway
  const sshort = idx.segmentById.get(idx.tileToSegment.get(k(1, 0))); // rd_dual -> dual_carriageway

  // --- t0 (free-flow minutes), re-derived purely from data/roads.json +
  // data/traffic.json, using only the doc's own stated formula (AC-2), never
  // trafficAssignment.ts's ROAD_CLASS_ID_OF_TIER (private) -- an attacker
  // reading only the JSON files and the acceptance doc's worked examples
  // must independently pick the same road-class mapping.
  const motorwayRoads = roads.classes.find((c) => c.id === 'motorway');
  const dualRoads = roads.classes.find((c) => c.id === 'dual_carriageway');
  const metresPerTile = trafficConfig.webconsoleMetresPerTile;
  const metresPerMile = trafficConfig.metresPerMile;
  assert.equal(metresPerTile, 50, 'sanity: webconsole tile is 50m per the lead amendment');
  assert.ok(metresPerMile > 1609 && metresPerMile < 1610, 'sanity: metresPerMile is a real mile figure');

  function t0For(speedLimitMph, tiles) {
    const metres = tiles * metresPerTile;
    const metresPerMinute = (speedLimitMph * metresPerMile) / 60;
    return metres / metresPerMinute;
  }
  const expectedT0S1 = t0For(motorwayRoads.speedLimit, 1);
  const expectedT0Sshort = t0For(dualRoads.speedLimit, 1);
  const t0Map = segmentFreeFlowMinutesOf(s);
  assert.ok(Math.abs(t0Map.get(s1.segmentId) - expectedT0S1) < 1e-9, `t0(S1) ${t0Map.get(s1.segmentId)} != ${expectedT0S1}`);
  assert.ok(Math.abs(t0Map.get(sshort.segmentId) - expectedT0Sshort) < 1e-9, `t0(Sshort) ${t0Map.get(sshort.segmentId)} != ${expectedT0Sshort}`);

  // --- assignedFlowOf, re-derived purely from demandForecastOf +
  // modeShareOf + vehicle_classes.json occupancy (A-10), the doc's own
  // stated AC-3 formula -- never reading roadVehicleTrips off any module
  // internal.
  const demand = demandForecastOf(s);
  const shares = modeShareOf(ladderPointOf(s));
  const occupancyById = Object.fromEntries(vehicleClasses.roadVehicles.map((v) => [v.id, v.avgOccupancyPersons.value]));
  const busOccupancy = vehicleClasses.busSubtypes.single_deck.totalCapacity * vehicleClasses.busSubtypes.single_deck.avgLoadFactor;
  function occupancyFor(modeId) {
    return modeId === 'bus' ? busOccupancy : occupancyById[modeId];
  }
  function roadVehicleTripsOf(tile) {
    let trips = 0;
    for (const modeId of ['car', 'motorbike', 'taxi', 'bus']) {
      const share = shares[modeId] ?? 0;
      const occ = occupancyFor(modeId);
      if (share > 0 && occ > 0) trips += (tile.personTrips * share) / occ;
    }
    return trips + tile.freightVehicleTrips;
  }
  const originTile = demand.find((t) => t.x === OFFSET - 1 && t.y === OFFSET);
  const jobTile = demand.find((t) => t.x === OFFSET + 1 && t.y === OFFSET + 1);
  assert.ok(originTile && jobTile, 'both demand tiles must be present');
  const originTrips = roadVehicleTripsOf(originTile);
  const jobTrips = roadVehicleTripsOf(jobTile);

  const flow = assignedFlowOf(s);
  assert.ok(Math.abs(flow.get(s1.segmentId) - originTrips) < 1e-6, `flow(S1) ${flow.get(s1.segmentId)} != ${originTrips}`);
  const expectedSshort = originTrips + jobTrips;
  assert.ok(Math.abs(flow.get(sshort.segmentId) - expectedSshort) < 1e-6, `flow(Sshort) ${flow.get(sshort.segmentId)} != ${expectedSshort}`);

  // MUTANT: any formula drift in either segmentFreeFlowMinutesOf (BUG-861
  // class) or assignedFlowOf's accumulation (mis-attributed tile, double
  // count, wrong mode/occupancy) reds one of the four exact-value pins
  // above -- this is a genuinely independent re-derivation, not a
  // hand-copied constant (every input above comes from the loaded JSON
  // files or demandForecastOf/modeShareOf, both already-shipped inc2
  // exports this module consumes but does not own).
});

// ---------------------------------------------------------------------------
// Probe 2: distributed-grid city-scale sanity bound
// ---------------------------------------------------------------------------

test('ROUND: a 24x24 alternating rd_aroad/rd_dual road mesh at population 20,000 never overloads a segment (v/c max < 1)', () => {
  const N = 24;
  const buildings = [];
  let id = 1;
  for (let x = 0; x < N; x++) {
    for (let y = 0; y < N; y++) {
      // Checkerboard: every orthogonal neighbour is the OTHER spec, so every
      // tile is its own 1-tile segment -- a dense mesh of N*N segments,
      // exactly the "24x24 alternating rd_aroad/rd_dual grid" the r2
      // attacker described.
      const spec = (x + y) % 2 === 0 ? 'rd_aroad' : 'rd_dual';
      buildings.push(rd(id++, spec, x, y));
    }
  }
  // Residential buildings along the west edge (x = -1), job buildings along
  // the east edge (x = N), each adjacent to its own grid row -- spreads
  // demand across N distinct origin/destination pairs instead of
  // concentrating it on one tile (which would trivially overload a single
  // segment regardless of mesh capacity, not a meaningful mesh-level sanity
  // check).
  for (let y = 0; y < N; y++) {
    buildings.push(bldg(id++, 'res_hut', -1, y));
    buildings.push(bldg(id++, 'off_suite', N, y));
  }
  const s = board(buildings, 20000);
  assert.ok(demandForecastOf(s).length > 0, 'grid fixture must generate real demand');
  assert.equal(unroutedDemandOf(s).length, 0, 'every demand tile in a fully-connected mesh must be routable');

  const delay = segmentDelayOf(s);
  let maxVOverC = 0;
  let maxSegId = null;
  for (const [segId, d] of delay) {
    if (d.vOverC > maxVOverC) {
      maxVOverC = d.vOverC;
      maxSegId = segId;
    }
  }
  assert.ok(delay.size > 0, 'the mesh must carry SOME flow (false-pass guard: an empty delay map would trivially pass v/c < 1)');
  assert.ok(maxVOverC < 1, `segment ${maxSegId} is overloaded (v/c=${maxVOverC}) on a well-provisioned distributed mesh at population 20,000`);

  // MUTANT: any v/c basis regression (BUG-854 class: dividing by the wrong
  // operating-window figure, or omitting peakHourFactor) would inflate v
  // city-wide and could tip this sanity bound over 1 even on a spread-out
  // mesh -- a real, if coarse, backstop against ANY systematic v/c
  // overstatement, complementing the exact hand-fixture pins elsewhere in
  // this suite.
});
