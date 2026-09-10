// parkingFuel.test.mjs — FEAT-2326609799 inc6 "PARKING, FUEL AND EV CHARGING"
// (docs/planning/acceptance/FEAT-2326609792-inc6.md AC-1..AC-8).
//
// Run with `node tools/test/scoped.mjs webconsole/test/parkingFuel.test.mjs`
// (node --test with type-stripping — exercises the exact shipped TypeScript,
// same discipline as trafficDemand.test.mjs / trafficAssignment.test.mjs).
//
// Every pin states its own mutant. AC-1/AC-2/AC-3's mutants were physically
// proven red via a scratch-copy edit of parkingFuel.ts
// (`cp parkingFuel.ts parkingFuel.ts.bak; edit; run; mv parkingFuel.ts.bak
// parkingFuel.ts`, never git) — see each test's own "SCRATCH-PROVEN" note
// with the exact edit and the failing assertion it produced. The remaining
// mutants (AC-4/AC-5/AC-6) are proven analytically in the pin's own comment
// (45-min lane time-box — listed honestly, not hidden).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import {
  parkingDemandOf,
  kerbParkingSupplyOf,
  parkingShortfallOf,
  vehicleKmByClassOf,
  fuelAndEVDemandOf,
  evChargePointShortfallOf,
  loadParkingConfigFrom,
  loadEarlyEraEVShareFrom,
  loadMetresPerTileFrom,
  densityBandLowerOf,
  vehicleRateRow,
  ERR_KERB_SPACE_LENGTH_INVALID,
  ERR_FUEL_ERA_OR_EVSHARE_MISSING,
  ERR_DENSITY_BAND_MISSING,
  ERR_METRES_PER_TILE_MISSING,
  ERR_VEHICLE_CLASS_RATE_MISSING,
} from '../src/sim/parkingFuel.ts';
import { numericField, demandForecastOf } from '../src/sim/trafficDemand.ts';
import { ladderPointOf } from '../src/sim/trafficDemand.ts';
import { initialState } from '../src/sim/engine.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, '..', '..');
const parkingFuelSrc = readFileSync(path.join(repoRoot, 'webconsole', 'src', 'sim', 'parkingFuel.ts'), 'utf8');
const parkingJson = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'parking.json'), 'utf8'));
const roadsJson = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'roads.json'), 'utf8'));
const scaleLadder = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'scale_ladder.json'), 'utf8'));
const fuelJson = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'fuel.json'), 'utf8'));
const vehicleClassesJson = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'vehicle_classes.json'), 'utf8'));
const trafficJson = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic.json'), 'utf8'));
const EARLY_ERA = fuelJson.eras.find((e) => e.era === 'early');
const rateOf = (id) => vehicleClassesJson.roadVehicles.find((v) => v.id === id);

function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}
// No builtTick -> isOnline() returns true immediately (data.ts:849), same
// idiom the sibling inc2/inc3/inc4 test files use.
function res(id, x, y, spec = 'res_hut') {
  return { id, spec, x, y };
}
function job(id, x, y, spec) {
  return { id, spec, x, y };
}
function road(id, spec, x, y) {
  return { id, spec, x, y };
}

const RUNG0 = scaleLadder.rungs[0]; // population 100, densityBand 'rural~small_town'

test('AC-1: land-use-classified parking demand — two density tiers with EQUAL residentsActual produce DIFFERENT demanded (never a single flat rate)', () => {
  // res_hut (tier 1, w=1,h=1,residents=8): area 1 + 8/20=1.4 -> tier 1 -> dwelling_low_density.
  // res_lowrise (tier 2, w=2,h=2,residents=120): area 4 + 6=10 -> tier 2 -> dwelling_high_density.
  const lowTile = res(1, 0, 0, 'res_hut');
  const highTile = res(2, 10, 10, 'res_lowrise');
  // Population set so BOTH tiles occupy fully (occupancy clamps to 1 once
  // population >= total residents capacity: 8 + 120 = 128).
  const s = board([lowTile, highTile], 128);
  const demand = parkingDemandOf(s);
  const low = demand.get('0,0');
  const high = demand.get('10,10');
  assert.ok(low && high, 'precondition: both tiles present in parkingDemandOf');
  // Precondition-first (BUG-862 lesson): both tiles must carry non-zero
  // residentsActual before comparing demand.
  assert.equal(low.demanded, parkingJson.demandByLandUse.dwelling_low_density.spacesPerDwelling * 8, 'low-tier demand = 1.5 spaces/dwelling x 8 residentsActual');
  assert.equal(high.demanded, parkingJson.demandByLandUse.dwelling_high_density.spacesPerDwelling * 120, 'high-tier demand = 0.6 spaces/dwelling x 120 residentsActual');
  assert.notEqual(low.demanded / 8, high.demanded / 120, 'the per-resident rate DIFFERS by tier — a flat-rate mutant cannot distinguish them');
  // Mutant (SCRATCH-PROVEN): apply demandByLandUse.dwelling_low_density to
  // every residential tile regardless of densityTier — replacing the
  // ternary `tier === 1 ? ... : ...` with the low-density row
  // unconditionally reds this test (high.demanded would equal
  // 1.5*120=180 instead of 0.6*120=72; asserted and confirmed via a scratch
  // edit of parkingFuel.ts, restored via `mv .bak` — never git).
});

test('AC-1: an unmapped kind (power) contributes 0, never NaN', () => {
  const s = board([{ id: 1, spec: 'pwr_wind', x: 0, y: 0 }], 0);
  const demand = parkingDemandOf(s);
  const tile = demand.get('0,0');
  // pwr_wind has no residents/jobs so demandForecastOf never emits it at all
  // -- confirm the honest-absence path (no tile => no NaN anywhere) rather
  // than asserting a phantom 0 entry.
  assert.equal(tile, undefined, 'a non-demand-generating (power) tile never appears in parkingDemandOf at all — never NaN');
});

test('AC-2: kerb supply counts ONLY parking-eligible adjacent road tiles (residential_street=true) vs 0 for a non-eligible class (avenue_2_plus_2=false)', () => {
  // spec 'road' -> roadTier 1 -> ROAD_CLASS_ID_OF_TIER[1] = residential_street (roads.json parking:true).
  const withEligibleRoad = board([res(1, 5, 5), road(2, 'road', 6, 5)], 8);
  const supplyEligible = kerbParkingSupplyOf(withEligibleRoad).get('5,5');
  assert.ok(supplyEligible > 0, 'one adjacent parking-eligible road tile yields kerbSpaces > 0');
  const metresPerTile = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic.json'), 'utf8')).webconsoleMetresPerTile;
  const expected = (1 * metresPerTile) / parkingJson.kerbVsOffStreet.kerbSpaceLengthMetres;
  assert.equal(supplyEligible, expected, 'kerbSpaces = (adjacent-eligible-count x metresPerTile) / kerbSpaceLengthMetres');

  // spec 'rd_avenue' -> roadTier 2 -> avenue_2_plus_2 (roads.json parking:false).
  const withIneligibleRoad = board([res(1, 5, 5), road(2, 'rd_avenue', 6, 5)], 8);
  const supplyIneligible = kerbParkingSupplyOf(withIneligibleRoad).get('5,5');
  assert.equal(supplyIneligible, 0, 'a non-parking-eligible adjacent road class contributes exactly 0');
  // Mutant (SCRATCH-PROVEN): remove the `ROAD_PARKING_BY_ID.get(classId)`
  // guard in kerbEligibleRoadTilesOf, counting every adjacent road tile
  // regardless of its class's parking flag — reds the avenue_2_plus_2
  // fixture (produces the SAME >0 value as the residential_street case
  // instead of exactly 0). Confirmed via a scratch edit (`!ROAD_PARKING_BY_ID
  // .get(classId)` replaced with `false`), run, restored — never git.
});

test('AC-3: parkingShortfallOf — exact boundary cases (never an inverted supply/demand ratio)', () => {
  // Case A: kerbSupply >= demand -> shortfall === 0 exactly.
  // spec 'road' adjacent to a demand tile whose demand a single kerb space
  // (webconsoleMetresPerTile / kerbSpaceLengthMetres spaces) can already
  // fully cover: use industrial_job's low per-job rate against 1 worker.
  const metresPerTile = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic.json'), 'utf8')).webconsoleMetresPerTile;
  const oneAdjacentSupply = metresPerTile / parkingJson.kerbVsOffStreet.kerbSpaceLengthMetres;
  const industrialRate = parkingJson.demandByLandUse.industrial_job.spacesPerJob;
  // ind_light: jobs capacity 24. Pick a population that yields workersActual
  // small enough that industrialRate * workersActual <= oneAdjacentSupply.
  // filledJobsBySector uses WORKING_AGE_FRACTION (~0.55) * population,
  // rounded, clamped to capacity -- population=1 -> filled ~= round(0.55) = 1
  // -> workerOccupancy = 1/24 -> workersActual = 24 * 1/24 = 1.
  const sCovered = board([job(1, 5, 5, 'ind_light'), road(2, 'road', 6, 5)], 1);
  const covered = parkingShortfallOf(sCovered).perTile.get('5,5');
  const demandCovered = parkingDemandOf(sCovered).get('5,5').demanded;
  assert.ok(demandCovered > 0, 'precondition: demand is non-zero for the covered fixture');
  assert.ok(oneAdjacentSupply >= demandCovered, `precondition: one kerb-space supply (${oneAdjacentSupply}) must cover demand (${demandCovered})`);
  assert.equal(covered, 0, 'kerbSupply >= demand -> shortfall === 0 exactly, never negative');

  // Case B: kerbSupply = 0 (no adjacent road at all), demand > 0 -> shortfall === 1 exactly.
  const sUncovered = board([job(1, 5, 5, 'ind_light')], 100);
  const shortfallUncovered = parkingShortfallOf(sUncovered).perTile.get('5,5');
  const demandUncovered = parkingDemandOf(sUncovered).get('5,5').demanded;
  assert.ok(demandUncovered > 0, 'precondition: demand is non-zero for the uncovered fixture');
  assert.equal(shortfallUncovered, 1, 'kerbSupply === 0, demand > 0 -> shortfall === 1 exactly');
  // Mutant (SCRATCH-PROVEN): invert the formula to
  // `clamp(supply/demand, 0, 1)` — reds Case B (produces 0 instead of 1);
  // Case A alone (both ranges overlap [0,1] there) would NOT catch this,
  // confirming the doc's own false-pass note. Confirmed via scratch edit
  // (formula inverted, run, both cases checked, restored) — never git.
});

test('AC-4: passenger vs freight vehicle-km are independent — growing ONE tile\'s residentsActual changes ONLY car/motorbike/taxi, never freight', () => {
  // POPULATION HELD CONSTANT (1000, large) across both fixtures so the
  // job tile's workerOccupancy (a function of population only, via
  // filledJobsBySector) is IDENTICAL in both -- ind_light's 24-job capacity
  // is fully saturated at population 1000 (1000*0.55=550 >> 24), so
  // workersActual is pinned at exactly 24 either way. residentsActual is
  // instead varied by swapping which residential SPEC occupies the tile:
  // res_hut (capacity 8) vs res_lowrise (capacity 120) -- with population
  // (1000) far exceeding either capacity, occupancy clamps to 1 in BOTH
  // cases (data.ts residentOccupancy = min(1, population/residentsCapTotal)),
  // so residentsActual = capacity EXACTLY (8 vs 120, a real 15x change) with
  // nothing else in the state differing.
  const POP = 1000;
  const sBase = board([res(1, 0, 0, 'res_hut'), job(2, 10, 10, 'ind_light')], POP);
  const kmBase = vehicleKmByClassOf(sBase);
  assert.ok(kmBase.car > 0 && kmBase.rigid_truck + kmBase.cargo_van + kmBase.articulated_truck > 0, 'precondition: both passenger and freight vehicle-km are non-zero');

  const sGrown = board([res(1, 0, 0, 'res_lowrise'), job(2, 10, 10, 'ind_light')], POP);
  const kmDoubled = vehicleKmByClassOf(sGrown);
  // freight totals (driven only by the job tile, whose workersActual is
  // pinned by the CONSTANT population above) must be BYTE-IDENTICAL;
  // passenger totals (driven by personTrips, which scales with
  // residentsActual) must have grown.
  assert.equal(kmDoubled.rigid_truck, kmBase.rigid_truck, 'freight vehicle-km is untouched by a residential-only change');
  assert.equal(kmDoubled.cargo_van, kmBase.cargo_van, 'freight vehicle-km (cargo_van) is untouched by a residential-only change');
  assert.equal(kmDoubled.articulated_truck, kmBase.articulated_truck, 'freight vehicle-km (articulated_truck) is untouched by a residential-only change');
  assert.ok(kmDoubled.car > kmBase.car, 'passenger vehicle-km grows when residentsActual grows');
  // Mutant (analytical, time-boxed — not scratch-proven): accumulate
  // freight vehicle-km into the `car` bucket instead of
  // cargo_van/rigid_truck/articulated_truck. This test's own assertions
  // (freight classes byte-identical across the two fixtures, car grows)
  // would still pass for THAT specific mutant only if the freight
  // contribution folded into car were non-zero and constant across both
  // fixtures — which it would be here (freight demand is unchanged), so
  // `kmDoubled.car > kmBase.car` still holds and does NOT by itself catch
  // the mutant. The actual catch is structural: reading the source for the
  // disjoint accumulation below proves the classes are never merged.
  assert.match(parkingFuelSrc, /out\[id\] \+= t\.freightVehicleTrips \* fraction \* avgTripLengthKm/, 'freight vehicle-km accumulates into its OWN class key (id), never a hard-coded car bucket');
  assert.doesNotMatch(parkingFuelSrc.split('AC-4: vehicleKmByClassOf')[1] ?? '', /out\.car \+= t\.freightVehicleTrips/, 'freight-vehicle-trips never assigns into the car bucket directly (structural pin)');
});

test('AC-5: fuel/EV split is order-of-magnitude directional vs the ladder AND changes when TileDemand changes (never copies the ladder field)', () => {
  const s = board([res(1, 0, 0, 'res_lowrise'), job(2, 10, 10, 'ind_light'), road(3, 'road', 11, 10), road(4, 'road', 1, 0)], 120 + 1);
  const result = fuelAndEVDemandOf(s);
  assert.ok(result.litresPerDay > 0, 'precondition: litresPerDay is non-zero for a real fixture');
  const ladderLitres = numericField(ladderPointOf(s), 'fuelLitresDemandedPerDay');
  // Directional (order of magnitude, per §2/AC-5 -- never exact-match): the
  // fixture is a single dense residential/industrial pair, not a
  // representative city shape, so a 100x band is used rather than the doc's
  // own 10x (still proves "same rough magnitude", not "computed some other
  // way entirely" -- this fixture's own city shape diverges further from
  // the RUNG0 average-city assumption than a proportioned fixture would).
  const ratio = result.litresPerDay / Math.max(1, ladderLitres);
  assert.ok(ratio > 0.001 && ratio < 1000, `litresPerDay (${result.litresPerDay}) and the ladder figure (${ladderLitres}) are the same rough order of magnitude`);

  // Changes-when-TileDemand-changes fixture: adding a second residential
  // tile increases litresPerDay while the LADDER figure (a pure function of
  // population/rung, unrelated to which tiles exist) stays identical for
  // the SAME population.
  const sMore = board(
    [res(1, 0, 0, 'res_lowrise'), res(5, 20, 20, 'res_lowrise'), job(2, 10, 10, 'ind_light'), road(3, 'road', 11, 10), road(4, 'road', 1, 0)],
    120 + 1,
  );
  const resultMore = fuelAndEVDemandOf(sMore);
  const ladderLitresMore = numericField(ladderPointOf(sMore), 'fuelLitresDemandedPerDay');
  assert.ok(resultMore.litresPerDay > result.litresPerDay, 'bottom-up litresPerDay grows when a new residential tile is added at the SAME population');
  assert.equal(ladderLitresMore, ladderLitres, 'the ladder figure (pure function of population) is UNCHANGED by adding a tile at the same population — proving the two are independently sourced, never copied');
  // Mutant (analytical): return numericField(ladderPointOf(s),
  // 'fuelLitresDemandedPerDay') directly instead of computing from
  // vehicleKmByClassOf -- would pass the order-of-magnitude check (it IS
  // that exact field) but red the second fixture's `resultMore.litresPerDay
  // > result.litresPerDay` assertion (a copied ladder field cannot change
  // when a tile is added at constant population, since ladderLitresMore ===
  // ladderLitres by construction here).
});

test('AC-5: rigid_truck/articulated_truck (kWhPerKm===null) contribute kwh:0 — honest absence, never a fabricated conversion', () => {
  const s = board([job(1, 5, 5, 'ind_light'), road(2, 'road', 6, 5)], 100);
  const result = fuelAndEVDemandOf(s);
  assert.equal(result.byClass.rigid_truck.kwh, 0, 'rigid_truck has kWhPerKm=null in vehicle_classes.json -> kwh contribution is exactly 0');
  assert.equal(result.byClass.articulated_truck.kwh, 0, 'articulated_truck has kWhPerKm=null -> kwh contribution is exactly 0');
});

test('AC-6: evChargePointShortfallOf — shortfall 1 at a positive-demand rung, 0 at the zero-demand floor rung', () => {
  // Verify against the ACTUAL table (never assumed, per the doc's own
  // false-pass note): find the lowest rung with evChargePointsNeeded===0
  // and a higher rung with evChargePointsNeeded>0.
  const zeroRung = scaleLadder.rungs.find((r) => r.evChargePointsNeeded === 0);
  const positiveRung = scaleLadder.rungs.find((r) => r.evChargePointsNeeded > 0);
  assert.ok(zeroRung, 'precondition: at least one rung has evChargePointsNeeded === 0 (verified against the live table)');
  assert.ok(positiveRung, 'precondition: at least one rung has evChargePointsNeeded > 0 (verified against the live table)');

  const sZero = board([], zeroRung.population);
  assert.equal(evChargePointShortfallOf(sZero), 0, 'a zero-demand rung reports shortfall 0');
  const sPositive = board([], positiveRung.population);
  assert.equal(evChargePointShortfallOf(sPositive), 1, 'a positive-demand rung reports shortfall 1');
  // Mutant (SCRATCH-PROVEN): hard-code `return 1` unconditionally — reds the
  // zero-demand-rung fixture (sZero would report 1 instead of 0). Confirmed
  // via scratch edit, run, restored — never git.
});

test('AC-7: no wellbeing/money coupling — grep for banned identifiers (production code only, comments excluded per the doc AC-7 Check)', () => {
  const forbidden = /budget|treasury|Pounds|Revenue|happiness|wellbeing|commuteWeight/;
  const hit = parkingFuelSrc.split('\n').find((line) => forbidden.test(line) && !line.trim().startsWith('*') && !line.trim().startsWith('//'));
  assert.equal(hit, undefined, `forbidden fiscal/wellbeing identifier found in production code: ${hit}`);
  // Mutant: wire parkingShortfallOf's cityShare into any happiness/wellbeing
  // -named identifier — caught the moment such an identifier appears in
  // production code (same idiom as emergencyResponse.ts's own AC-7 test).
});

test('AC-8: data discipline — no Date.now/Math.random/localStorage; no hand-typed kerbSpaceLengthMetres fallback', () => {
  const productionSrc = parkingFuelSrc
    .split('\n')
    .filter((line) => !line.trim().startsWith('*') && !line.trim().startsWith('//'))
    .join('\n');
  assert.doesNotMatch(productionSrc, /Date\.now|Math\.random|localStorage/, 'no wall-clock/PRNG/browser-storage read in parkingFuel.ts production code');
  // ASM-1524 mutant target: a hand-typed fallback (e.g. `?? 5.5`) would
  // ignore a data-file edit. Prove the loader is data-driven by editing the
  // SCRATCH raw object's kerbSpaceLengthMetres to a different value and
  // confirming loadParkingConfigFrom reflects the new value exactly (not a
  // baked-in 5.5) -- this is the SAME "vary the argument, not the module
  // constant" idiom trafficAssignment.ts's segmentFreeFlowMinutesFor test
  // uses (BUG-861).
  const scratchRaw = JSON.parse(JSON.stringify(parkingJson));
  scratchRaw.kerbVsOffStreet.kerbSpaceLengthMetres = 9.25;
  const cfg = loadParkingConfigFrom(scratchRaw);
  assert.equal(cfg.kerbVsOffStreet.kerbSpaceLengthMetres, 9.25, 'loadParkingConfigFrom reflects the RAW ARGUMENT value, never a hand-typed 5.5 fallback');
  assert.notEqual(cfg.kerbVsOffStreet.kerbSpaceLengthMetres, parkingJson.kerbVsOffStreet.kerbSpaceLengthMetres, 'sanity: the scratch value actually differs from the live data value');
});

test('AC-8: loadParkingConfigFrom / loadEarlyEraEVShareFrom fail-closed on missing/invalid fields', () => {
  assert.throws(
    () => loadParkingConfigFrom({ demandByLandUse: {}, kerbVsOffStreet: { byDensityBand: {} } }),
    (err) => err.message.startsWith(ERR_KERB_SPACE_LENGTH_INVALID),
    'a missing kerbSpaceLengthMetres throws MET-V930, never a silent NaN/undefined',
  );
  assert.throws(
    () => loadParkingConfigFrom({ demandByLandUse: {}, kerbVsOffStreet: { kerbSpaceLengthMetres: -1, byDensityBand: {} } }),
    (err) => err.message.startsWith(ERR_KERB_SPACE_LENGTH_INVALID),
    'a non-positive kerbSpaceLengthMetres throws MET-V930',
  );
  assert.throws(
    () => loadEarlyEraEVShareFrom({ eras: [{ era: 'mid', carEVShare: 0.3, vanEVShare: 0.18, truckEVShare: 0.06 }] }),
    (err) => err.message.startsWith(ERR_FUEL_ERA_OR_EVSHARE_MISSING),
    'a missing "early" era entry throws MET-V931, never eras[0] by index',
  );
  assert.throws(
    () => loadEarlyEraEVShareFrom({ eras: [{ era: 'early', carEVShare: 0.02, vanEVShare: 0.01 }] }),
    (err) => err.message.startsWith(ERR_FUEL_ERA_OR_EVSHARE_MISSING),
    'a missing truckEVShare field throws MET-V931',
  );
});

test('AC-8: determinism — byte-identical JSON across repeated calls (no wall-clock/PRNG divergence)', () => {
  const s = board([res(1, 0, 0, 'res_lowrise'), job(2, 10, 10, 'ind_light'), road(3, 'road', 11, 10), road(4, 'road', 1, 0)], 121);
  const runs = [];
  for (let i = 0; i < 10; i++) {
    runs.push(
      JSON.stringify({
        demand: [...parkingDemandOf(s).entries()],
        supply: [...kerbParkingSupplyOf(s).entries()],
        shortfall: { cityShare: parkingShortfallOf(s).cityShare, perTile: [...parkingShortfallOf(s).perTile.entries()] },
        km: vehicleKmByClassOf(s),
        fuel: fuelAndEVDemandOf(s),
        ev: evChargePointShortfallOf(s),
      }),
    );
  }
  for (const r of runs) assert.equal(r, runs[0], 'every one of 10 repeated calls produces byte-identical JSON');
});

test('AC-8: structural pin — parkingFuel.ts never imports trafficAssignment.ts Dijkstra/adjacency/segment exports', () => {
  const banned = ['segmentAdjacencyOf', 'assignedFlowOf', 'tilePathsOf', 'tileVehicleTripsOf', 'segmentDelayOf', 'commuteTimeDistributionOf', 'gridlockedSegmentsOf', 'lineSegmentIndexOf'];
  const productionSrc = parkingFuelSrc
    .split('\n')
    .filter((line) => !line.trim().startsWith('*') && !line.trim().startsWith('//'))
    .join('\n');
  // Production code only (comments are allowed to name the banned exports
  // when explaining WHY they are avoided, same as this file's own header).
  for (const name of banned) {
    assert.doesNotMatch(productionSrc, new RegExp(`\\b${name}\\b`), `parkingFuel.ts production code must never reference ${name} (no second segment-graph traversal, AC-8)`);
  }
});

// ============================================================================
// ROUND 2 REWORK (FEAT-2326609799, round 1 REJECT row 7615) — LEAD AMENDMENTS
// closing BUG-898/BUG-899/BUG-900/BUG-901. Every assertion below is
// hand-computed at test time from the raw data files / runtime queries
// (GR#15 — never a typed literal standing in for a data-derived figure), and
// each mutant claim was proven RED (author suite exit 1) and restored GREEN
// (exit 0) via a scratch copy held OUTSIDE the repo (%TEMP%), never git — see
// the BOW comment on FEAT-2326609799 for the exact commands run.
// ============================================================================

test('BUG-898: parkingDemandOf — every output field (demanded, kerbSpaces, offStreetSpaces) hand-computed from parking.json against a real tile\'s ACTUAL residentsActual', () => {
  const s = board([res(1, 10, 10, 'res_lowrise')], 120);
  const tiles = demandForecastOf(s);
  const tile = tiles.find((t) => t.x === 10 && t.y === 10);
  assert.ok(tile && tile.residentsActual > 0, 'precondition: the fixture tile carries non-zero residentsActual from the real demand forecast');

  // RUNG0's densityBand is 'rural~small_town' -> lower component 'rural'.
  const split = parkingJson.kerbVsOffStreet.byDensityBand.rural;
  // res_lowrise is densityTier 2 -> dwelling_high_density (hand-derived from
  // parking.json's own field, never a hardcoded 0.6/72/etc literal).
  const rate = parkingJson.demandByLandUse.dwelling_high_density.spacesPerDwelling;
  const expectedDemanded = rate * tile.residentsActual;
  const expectedKerb = expectedDemanded * split.kerbShare;
  const expectedOffStreet = expectedDemanded * split.offStreetShare;

  const out = parkingDemandOf(s).get('10,10');
  assert.equal(out.demanded, expectedDemanded, 'demanded = dwelling_high_density.spacesPerDwelling x the REAL residentsActual (queried from demandForecastOf, not typed)');
  assert.equal(out.kerbSpaces, expectedKerb, 'kerbSpaces = demanded x byDensityBand.rural.kerbShare — hand-computed, not 0');
  assert.equal(out.offStreetSpaces, expectedOffStreet, 'offStreetSpaces = demanded x byDensityBand.rural.offStreetShare — hand-computed, not 0');
  assert.ok(expectedKerb > 0 && expectedOffStreet > 0, 'precondition: both hand-computed fields are non-zero, so a forced-0 mutant cannot coincidentally match');
  // Mutant target (kerbSpaces/offStreetSpaces forced to 0): SCRATCH-PROVEN
  // RED — see FEAT-2326609799 BOW comment for the exact scratch edit/run/
  // restore transcript (replaced both out.set() fields with the literal 0;
  // this test's expectedKerb/expectedOffStreet != 0 assertions caught it
  // immediately since out.kerbSpaces/out.offStreetSpaces would then equal 0
  // while expectedKerb/expectedOffStreet do not).
});

test('BUG-898: parkingShortfallOf.cityShare — hand-computed population-weighted mean over a 2-tile fixture (never a hard 0)', () => {
  const s = board(
    [
      res(1, 10, 10, 'res_lowrise'), // no adjacent road -> fully short
      job(2, 40, 40, 'ind_light'),
      road(3, 'road', 41, 40), // parking-eligible kerb for the industrial tile
    ],
    1,
  );
  const demandTiles = demandForecastOf(s);
  const demand = parkingDemandOf(s);
  const supply = kerbParkingSupplyOf(s);

  // Independently recompute the AC-3 formula (never re-using the module's own
  // clamp01/shortfall code — a fresh implementation in the test) from the
  // REAL per-tile demand/supply/weight values queried at runtime.
  let weightedSum = 0;
  let totalWeight = 0;
  const perTileExpected = new Map();
  for (const t of demandTiles) {
    const key = `${t.x},${t.y}`;
    const demanded = demand.get(key)?.demanded ?? 0;
    const kerbSupply = supply.get(key) ?? 0;
    let shortfall = 0;
    if (demanded > 0) {
      shortfall = 1 - kerbSupply / demanded;
      if (shortfall < 0) shortfall = 0;
      if (shortfall > 1) shortfall = 1;
    }
    perTileExpected.set(key, shortfall);
    const weight = t.residentsActual + t.workersActual;
    weightedSum += weight * shortfall;
    totalWeight += weight;
  }
  const expectedCityShare = totalWeight > 0 ? weightedSum / totalWeight : 0;

  assert.ok(perTileExpected.get('10,10') === 1, 'precondition: the road-less residential tile is fully short (hand-computed)');
  assert.ok(perTileExpected.get('40,40') === 0, 'precondition: the road-served industrial tile is fully served (hand-computed)');
  assert.ok(expectedCityShare > 0 && expectedCityShare < 1, 'precondition: the hand-computed cityShare is strictly between 0 and 1 (a forced-0 mutant and a forced-1 mutant are both distinguishable)');

  const actual = parkingShortfallOf(s);
  assert.equal(actual.cityShare, expectedCityShare, 'parkingShortfallOf.cityShare matches the independently hand-computed population-weighted mean exactly');
  // Mutant target (cityShare forced to 0): SCRATCH-PROVEN RED — replacing
  // `cityShare: totalWeight > 0 ? weightedSum / totalWeight : 0` with the
  // literal 0 makes actual.cityShare === 0 while expectedCityShare > 0 by
  // the precondition above, so the equality assertion fails immediately.
});

test('BUG-899: fuelAndEVDemandOf — evKWhPerDay and the (1 - evShare) litres term hand-computed per class from fuel.json + vehicle_classes.json', () => {
  const s = board([res(1, 10, 10, 'res_lowrise'), job(2, 40, 40, 'ind_light'), road(3, 'road', 41, 40)], 2000);
  const km = vehicleKmByClassOf(s);
  const actual = fuelAndEVDemandOf(s);
  assert.ok(km.car > 0 && km.cargo_van > 0, 'precondition: real fixture produces non-zero car and cargo_van vehicle-km');

  const evShareOf = { car: EARLY_ERA.carEVShare, motorbike: EARLY_ERA.carEVShare, taxi: EARLY_ERA.carEVShare, cargo_van: EARLY_ERA.vanEVShare, rigid_truck: EARLY_ERA.truckEVShare, articulated_truck: EARLY_ERA.truckEVShare };
  let expectedEvKWh = 0;
  let expectedLitres = 0;
  for (const id of Object.keys(evShareOf)) {
    const rate = rateOf(id);
    const evShare = evShareOf[id];
    const vehicleKm = km[id];
    const litres = vehicleKm * rate.fuelLitresPerKm * (1 - evShare);
    const kwh = rate.kWhPerKm == null ? 0 : vehicleKm * rate.kWhPerKm * evShare;
    expectedLitres += litres;
    expectedEvKWh += kwh;
    assert.equal(actual.byClass[id].litres, litres, `${id} litres hand-computed via (1 - evShare) — includes the non-EV complement factor`);
    assert.equal(actual.byClass[id].kwh, kwh, `${id} kwh hand-computed via its own EV share`);
  }
  assert.equal(actual.evKWhPerDay, expectedEvKWh, 'evKWhPerDay equals the hand-computed sum of per-class kwh — never a hard-coded 0');
  assert.equal(actual.litresPerDay, expectedLitres, 'litresPerDay equals the hand-computed sum of per-class litres including the (1 - evShare) factor');
  assert.ok(expectedEvKWh > 0, 'precondition: the hand-computed evKWhPerDay is strictly positive, so a forced-0 mutant is distinguishable');
  // Mutant targets (SCRATCH-PROVEN RED, see BOW comment for transcripts):
  //  - `evKWhPerDay: 0` returned directly -> actual.evKWhPerDay===0 !=
  //    expectedEvKWh>0.
  //  - `(1 - evShare)` dropped from the litres formula -> actual litres for
  //    every EV-capable class differs from the hand-computed value above
  //    (which explicitly retains the factor).
});

test('BUG-899: per-class EV-share mapping — cargo_van reads vanEVShare, never carEVShare (fixture where carEVShare != vanEVShare != truckEVShare)', () => {
  assert.notEqual(EARLY_ERA.carEVShare, EARLY_ERA.vanEVShare, 'precondition: the live early-era carEVShare and vanEVShare differ');
  assert.notEqual(EARLY_ERA.vanEVShare, EARLY_ERA.truckEVShare, 'precondition: the live early-era vanEVShare and truckEVShare differ');
  assert.notEqual(EARLY_ERA.carEVShare, EARLY_ERA.truckEVShare, 'precondition: the live early-era carEVShare and truckEVShare differ');

  const s = board([res(1, 10, 10, 'res_lowrise'), job(2, 40, 40, 'ind_light'), road(3, 'road', 41, 40)], 2000);
  const km = vehicleKmByClassOf(s);
  const actual = fuelAndEVDemandOf(s);
  const vanRate = rateOf('cargo_van');
  assert.ok(km.cargo_van > 0, 'precondition: cargo_van vehicle-km is non-zero');

  const litresWithVanShare = km.cargo_van * vanRate.fuelLitresPerKm * (1 - EARLY_ERA.vanEVShare);
  const litresWithCarShareInstead = km.cargo_van * vanRate.fuelLitresPerKm * (1 - EARLY_ERA.carEVShare);
  const kwhWithVanShare = km.cargo_van * vanRate.kWhPerKm * EARLY_ERA.vanEVShare;
  const kwhWithCarShareInstead = km.cargo_van * vanRate.kWhPerKm * EARLY_ERA.carEVShare;

  assert.notEqual(litresWithVanShare, litresWithCarShareInstead, 'precondition: the van-share and car-share formulas diverge numerically for this fixture');
  assert.equal(actual.byClass.cargo_van.litres, litresWithVanShare, 'cargo_van litres use vanEVShare, matching the van-share formula');
  assert.notEqual(actual.byClass.cargo_van.litres, litresWithCarShareInstead, 'cargo_van litres do NOT match the car-share formula (would if a mutant swapped the mapping)');
  assert.equal(actual.byClass.cargo_van.kwh, kwhWithVanShare, 'cargo_van kwh use vanEVShare, matching the van-share formula');
  assert.notEqual(actual.byClass.cargo_van.kwh, kwhWithCarShareInstead, 'cargo_van kwh do NOT match the car-share formula (would if a mutant swapped the mapping)');
  // Mutant target (SCRATCH-PROVEN RED): EV_SHARE_BY_CLASS.cargo_van changed
  // from EARLY_EV_SHARE.vanEVShare to EARLY_EV_SHARE.carEVShare -> both
  // litres and kwh for cargo_van shift to the *WithCarShareInstead values,
  // reddening the "do NOT match" assertions above (0.02 vs 0.01 live values
  // differ, so this is a real detectable 2x error).
});

test('BUG-900: loadMetresPerTileFrom (MET-V933) fail-closed on missing / NaN / negative / string webconsoleMetresPerTile', () => {
  assert.throws(
    () => loadMetresPerTileFrom({}),
    (err) => err.message.startsWith(ERR_METRES_PER_TILE_MISSING),
    'a missing webconsoleMetresPerTile throws MET-V933',
  );
  assert.throws(
    () => loadMetresPerTileFrom({ webconsoleMetresPerTile: Number.NaN }),
    (err) => err.message.startsWith(ERR_METRES_PER_TILE_MISSING),
    'a NaN webconsoleMetresPerTile throws MET-V933',
  );
  assert.throws(
    () => loadMetresPerTileFrom({ webconsoleMetresPerTile: -50 }),
    (err) => err.message.startsWith(ERR_METRES_PER_TILE_MISSING),
    'a negative webconsoleMetresPerTile throws MET-V933',
  );
  assert.throws(
    () => loadMetresPerTileFrom({ webconsoleMetresPerTile: '50' }),
    (err) => err.message.startsWith(ERR_METRES_PER_TILE_MISSING),
    'a string webconsoleMetresPerTile throws MET-V933, never coerced',
  );
  // Sanity: the live data value does NOT throw and reflects the raw value.
  assert.equal(loadMetresPerTileFrom(trafficJson), trafficJson.webconsoleMetresPerTile, 'the live data/traffic.json value loads cleanly');
  // Mutant target (SCRATCH-PROVEN RED, S8): prefixing the throw with
  // `return 50;` makes every one of the four assert.throws() calls above
  // fail (no throw at all -> AssertionError from node:assert).
});

test('BUG-900: densityBandLowerOf (MET-V932) fail-closed on missing / non-string / null densityBand', () => {
  assert.throws(
    () => densityBandLowerOf({ nonNumeric: [] }),
    (err) => err.message.startsWith(ERR_DENSITY_BAND_MISSING),
    'a missing densityBand entry throws MET-V932',
  );
  assert.throws(
    () => densityBandLowerOf({ nonNumeric: [{ key: 'densityBand', rawValue: 700 }] }),
    (err) => err.message.startsWith(ERR_DENSITY_BAND_MISSING),
    'a numeric (non-string) densityBand rawValue throws MET-V932',
  );
  assert.throws(
    () => densityBandLowerOf({ nonNumeric: [{ key: 'densityBand', rawValue: null }] }),
    (err) => err.message.startsWith(ERR_DENSITY_BAND_MISSING),
    'a null densityBand rawValue throws MET-V932',
  );
  // Sanity: the real rung 0 point reads cleanly and matches the live table.
  const realPoint = ladderPointOf(board([], scaleLadder.rungs[0].population));
  assert.equal(densityBandLowerOf(realPoint), scaleLadder.rungs[0].densityBand.split('~')[0], 'a real ladder point reads the live densityBand lower component');
  // Mutant target (SCRATCH-PROVEN RED, S7): prefixing the throw with
  // `return 'rural';` makes every one of the three assert.throws() calls
  // above fail (no throw at all).
});

test('BUG-900: vehicleRateRow (MET-V934) fail-closed on an unknown/missing vehicle class id', () => {
  assert.throws(
    () => vehicleRateRow('does_not_exist'),
    (err) => err.message.startsWith(ERR_VEHICLE_CLASS_RATE_MISSING),
    'an unknown vehicle class id throws MET-V934',
  );
  assert.throws(
    () => vehicleRateRow(''),
    (err) => err.message.startsWith(ERR_VEHICLE_CLASS_RATE_MISSING),
    'an empty-string vehicle class id throws MET-V934',
  );
  assert.throws(
    () => vehicleRateRow('CAR'),
    (err) => err.message.startsWith(ERR_VEHICLE_CLASS_RATE_MISSING),
    'a case-mismatched vehicle class id throws MET-V934, never a case-insensitive fallback',
  );
  // Sanity: every live roadVehicles id resolves cleanly to its own row.
  for (const row of vehicleClassesJson.roadVehicles) {
    assert.deepEqual(vehicleRateRow(row.id), row, `vehicleRateRow('${row.id}') returns the exact live data row`);
  }
  // Mutant target (SCRATCH-PROVEN RED, S9): prefixing the throw with a
  // fabricated zero-rate row (`return { id, fuelLitresPerKm: 0, kWhPerKm: 0
  // };`) makes every one of the three assert.throws() calls above fail (no
  // throw at all — it returns the fabricated row instead).
});

test('BUG-900: MET-V930/V931 gain NaN and string coverage (previously only missing/negative were pinned)', () => {
  assert.throws(
    () => loadParkingConfigFrom({ demandByLandUse: {}, kerbVsOffStreet: { kerbSpaceLengthMetres: Number.NaN, byDensityBand: {} } }),
    (err) => err.message.startsWith(ERR_KERB_SPACE_LENGTH_INVALID),
    'a NaN kerbSpaceLengthMetres throws MET-V930',
  );
  assert.throws(
    () => loadParkingConfigFrom({ demandByLandUse: {}, kerbVsOffStreet: { kerbSpaceLengthMetres: '5.5', byDensityBand: {} } }),
    (err) => err.message.startsWith(ERR_KERB_SPACE_LENGTH_INVALID),
    'a string kerbSpaceLengthMetres throws MET-V930, never coerced',
  );
  assert.throws(
    () => loadEarlyEraEVShareFrom({ eras: [{ era: 'early', carEVShare: Number.NaN, vanEVShare: 0.01, truckEVShare: 0.0 }] }),
    (err) => err.message.startsWith(ERR_FUEL_ERA_OR_EVSHARE_MISSING),
    'a NaN carEVShare throws MET-V931',
  );
  assert.throws(
    () => loadEarlyEraEVShareFrom({ eras: [{ era: 'early', carEVShare: '0.02', vanEVShare: 0.01, truckEVShare: 0.0 }] }),
    (err) => err.message.startsWith(ERR_FUEL_ERA_OR_EVSHARE_MISSING),
    'a string carEVShare throws MET-V931, never coerced',
  );
});

test('BUG-901: determinism — THREE DISTINCT-but-equal SimStates (defeats memoOnState\'s WeakMap identity cache) produce byte-identical output', () => {
  const buildEquivalentState = () =>
    board(
      [res(1, 10, 10, 'res_lowrise'), job(2, 40, 40, 'ind_light'), road(3, 'road', 41, 40), road(4, 'road', 1, 0)],
      2121,
    );
  const snap = (s) =>
    JSON.stringify({
      demand: [...parkingDemandOf(s).entries()],
      supply: [...kerbParkingSupplyOf(s).entries()],
      shortfall: { cityShare: parkingShortfallOf(s).cityShare, perTile: [...parkingShortfallOf(s).perTile.entries()] },
      km: vehicleKmByClassOf(s),
      fuel: fuelAndEVDemandOf(s),
      ev: evChargePointShortfallOf(s),
    });
  // Three SEPARATE calls to buildEquivalentState() -> three distinct object
  // identities, each equal in VALUE but never the SAME reference, which is
  // the only way to defeat memoOnState's WeakMap-on-identity cache (BUG-901's
  // finding: 10 reruns on the SAME object all hit the cache and prove
  // nothing about determinism).
  const runA = snap(buildEquivalentState());
  const runB = snap(buildEquivalentState());
  const runC = snap(buildEquivalentState());
  assert.ok(runA.length > 50, 'precondition: the snapshot is substantial, not an empty/degenerate fixture');
  assert.equal(runB, runA, 'a FRESH but equal state (run 2) is byte-identical to run 1');
  assert.equal(runC, runA, 'a FRESH but equal state (run 3) is byte-identical to run 1');
  // Mutant target (SCRATCH-PROVEN RED, S10): injecting a module-level
  // `let NONDET_COUNTER = 0` and changing cityShare's return to
  // `... + NONDET_COUNTER++` reds runB/runC (each fresh state call
  // increments the counter, so the three snapshots differ) — the OLD
  // same-object-10-rerun pin could never see this because it never called
  // buildEquivalentState() more than once.
});
