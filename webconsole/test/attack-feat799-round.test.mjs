// attack-feat799-round.test.mjs — FEAT-2326609799 inc6 independent Destructive
// round 1 (attacker opus-round-feat799-inc6; GR#23: the attacker is never the
// author). Every pin below closes a mutant that SURVIVED the builder's own
// parkingFuel.test.mjs — each was physically applied to
// webconsole/src/sim/parkingFuel.ts by scratch copy (backup written OUTSIDE
// the repo, edit, run, restore — never a git command, GR#24) and the builder's
// suite stayed GREEN (exit 0, RESULT: PASS). These assertions red every one.
//
// Survivors closed here:
//   S1  AC-1 kerbSpaces/offStreetSpaces forced to 0     (density-band split unasserted)
//   S2  AC-3 cityShare forced to 0                      (city-wide figure unasserted)
//   S3  AC-5 evKWhPerDay forced to 0                    (top-level EV output unasserted)
//   S4  AC-5 (1 - evShare) dropped from the litres term (EV split unasserted)
//   S5  AC-5 cargo_van reading carEVShare not vanEVShare(per-class mapping unasserted)
//   S10 AC-8 determinism: a module-level counter injected into cityShare
//       survived all 10 reruns of the builder's determinism test, because
//       memoOnState (data.ts:3782) is a WeakMap keyed on the SimState IDENTITY
//       — calls 2..10 on the SAME object return the SAME cached value, so that
//       test measures memoisation, not determinism. The pin below builds THREE
//       DISTINCT-BUT-EQUAL states instead, which defeats the memo entirely.

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
} from '../src/sim/parkingFuel.ts';
import { initialState } from '../src/sim/engine.ts';
// ROUND 2 addition: the real per-tile demand forecast, used to hand-compute
// office/commercial/industrial parking demand from the tile's ACTUAL workers.
import { demandForecastOf } from '../src/sim/trafficDemand.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, '..', '..');
const parkingJson = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'parking.json'), 'utf8'));
const fuelJson = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'fuel.json'), 'utf8'));
const vehicleJson = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'vehicle_classes.json'), 'utf8'));
const EARLY = fuelJson.eras.find((e) => e.era === 'early');
const rateOf = (id) => vehicleJson.roadVehicles.find((v) => v.id === id);

function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}

// --- S1: AC-1's density-band kerb/off-street split is really applied --------
test('attack S1: parkingDemandOf splits demanded into kerb/off-street by the CURRENT rung density band (a zeroed split survives the author suite)', () => {
  const s = board([{ id: 1, spec: 'res_lowrise', x: 10, y: 10 }], 120);
  const tile = parkingDemandOf(s).get('10,10');
  assert.ok(tile && tile.demanded > 0, 'precondition: the fixture tile carries non-zero parking demand');
  // Rung 0's densityBand is 'rural~small_town' -> lower component 'rural'.
  const split = parkingJson.kerbVsOffStreet.byDensityBand.rural;
  assert.ok(split.kerbShare > 0 && split.offStreetShare > 0, 'precondition: the rural band split is non-zero on both sides');
  assert.equal(tile.kerbSpaces, tile.demanded * split.kerbShare, 'kerbSpaces = demanded x the band kerbShare — never 0, never the off-street share');
  assert.equal(tile.offStreetSpaces, tile.demanded * split.offStreetShare, 'offStreetSpaces = demanded x the band offStreetShare');
  assert.notEqual(tile.kerbSpaces, tile.offStreetSpaces, 'the two shares differ in this band — a swapped or zeroed split cannot pass both equalities');
  assert.ok(
    Math.abs(tile.kerbSpaces + tile.offStreetSpaces - tile.demanded) < 1e-9,
    'the split is exhaustive: kerbSpaces + offStreetSpaces === demanded',
  );
});

// --- S2: AC-3's cityShare is really the population-weighted mean ------------
test('attack S2: parkingShortfallOf.cityShare is a real weighted mean of perTile, never a hard 0 (a hard 0 survives the author suite)', () => {
  const s = board(
    [
      { id: 1, spec: 'res_lowrise', x: 10, y: 10 }, // no adjacent road -> fully short
      { id: 2, spec: 'ind_light', x: 40, y: 40 },
      { id: 3, spec: 'road', x: 41, y: 40 }, // parking-eligible kerb for the industrial tile
    ],
    1,
  );
  const sf = parkingShortfallOf(s);
  const a = sf.perTile.get('10,10');
  const b = sf.perTile.get('40,40');
  assert.equal(a, 1, 'precondition: the road-less residential tile is fully short');
  assert.equal(b, 0, 'precondition: the road-served industrial tile is fully served');
  const demand = parkingDemandOf(s);
  assert.ok(demand.get('10,10').demanded > 0 && demand.get('40,40').demanded > 0, 'precondition: both tiles generate demand');
  assert.ok(sf.cityShare > 0, 'cityShare is NOT hard-zero when a weighted tile is fully short');
  assert.ok(sf.cityShare < 1, 'cityShare is NOT hard-one when another weighted tile is fully served');
  assert.ok(sf.cityShare >= 0 && sf.cityShare <= 1, 'cityShare stays inside [0,1]');
});

// --- S3/S4/S5: AC-5's EV split is really applied, per class -----------------
test('attack S3/S4/S5: fuelAndEVDemandOf applies the per-class early-era EV share to BOTH the litres and the kWh terms', () => {
  const s = board(
    [
      { id: 1, spec: 'res_lowrise', x: 10, y: 10 },
      { id: 2, spec: 'ind_light', x: 40, y: 40 },
      { id: 3, spec: 'road', x: 41, y: 40 },
    ],
    2000,
  );
  const km = vehicleKmByClassOf(s);
  const f = fuelAndEVDemandOf(s);
  assert.ok(km.car > 0 && km.cargo_van > 0, 'precondition: car and cargo_van vehicle-km are both non-zero');
  assert.ok(EARLY.carEVShare > 0 && EARLY.vanEVShare > 0, 'precondition: the early era has non-zero car and van EV shares');
  assert.notEqual(EARLY.carEVShare, EARLY.vanEVShare, 'precondition: car and van EV shares DIFFER — one flat share cannot satisfy both pins below');

  // S4: the (1 - evShare) non-EV factor is really present on the litres term.
  const carRate = rateOf('car');
  assert.equal(
    f.byClass.car.litres,
    km.car * carRate.fuelLitresPerKm * (1 - EARLY.carEVShare),
    'car litres carry the (1 - carEVShare) non-EV factor',
  );
  // S5: cargo_van reads vanEVShare, never carEVShare.
  const vanRate = rateOf('cargo_van');
  assert.equal(
    f.byClass.cargo_van.litres,
    km.cargo_van * vanRate.fuelLitresPerKm * (1 - EARLY.vanEVShare),
    'cargo_van litres use vanEVShare, never carEVShare',
  );
  assert.equal(
    f.byClass.cargo_van.kwh,
    km.cargo_van * vanRate.kWhPerKm * EARLY.vanEVShare,
    'cargo_van kWh use vanEVShare, never carEVShare',
  );
  // S3: the top-level evKWhPerDay is the real sum, not a hard 0.
  assert.ok(f.evKWhPerDay > 0, 'evKWhPerDay is non-zero when EV-capable classes carry vehicle-km');
  const summedKwh = Object.values(f.byClass).reduce((acc, c) => acc + c.kwh, 0);
  assert.ok(Math.abs(f.evKWhPerDay - summedKwh) < 1e-9, 'evKWhPerDay equals the sum of byClass kWh — never a hard-coded 0');
  const summedLitres = Object.values(f.byClass).reduce((acc, c) => acc + c.litres, 0);
  assert.ok(Math.abs(f.litresPerDay - summedLitres) < 1e-9, 'litresPerDay equals the sum of byClass litres');
});

// --- S10: a REAL determinism check that memoOnState cannot fake -------------
test('attack S10: three DISTINCT-but-equal SimStates produce byte-identical output (defeats the memoOnState WeakMap the author determinism pin measured)', () => {
  const build = () => {
    const bs = [];
    let id = 1;
    const specs = ['res_lowrise', 'off_tower', 'com_mall', 'ind_light'];
    let x = 0;
    let y = 0;
    for (let i = 0; i < 40; i++) {
      bs.push({ id: id++, spec: specs[i % 4], x, y });
      bs.push({ id: id++, spec: i % 3 === 0 ? 'road' : 'rd_avenue', x: x + 3, y });
      x += 6;
      if (x > 200) {
        x = 0;
        y += 6;
      }
    }
    return board(bs, 5000);
  };
  const snap = (s) =>
    JSON.stringify({
      d: [...parkingDemandOf(s).entries()],
      su: [...kerbParkingSupplyOf(s).entries()],
      sh: { c: parkingShortfallOf(s).cityShare, p: [...parkingShortfallOf(s).perTile.entries()] },
      km: vehicleKmByClassOf(s),
      f: fuelAndEVDemandOf(s),
      ev: evChargePointShortfallOf(s),
    });
  const runs = [snap(build()), snap(build()), snap(build())];
  assert.ok(runs[0].length > 100, 'precondition: the fixture produces a substantial snapshot, not an empty one');
  assert.equal(runs[1], runs[0], 'run 2 on a FRESH equal state is byte-identical to run 1');
  assert.equal(runs[2], runs[0], 'run 3 on a FRESH equal state is byte-identical to run 1');
});

// ============================================================================
// ROUND 2 (attacker opus-reround-feat799-inc6). The four amendments from
// round 1 are all met — every one of round 1's nine survivors (S1..S5,
// S7..S10) is now RED against the AUTHOR'S OWN suite alone. The pins below
// close FOUR NEW survivors found in round 2, each scratch-applied to
// webconsole/src/sim/parkingFuel.ts (backup OUTSIDE the repo, restored,
// md5-verified, never a git command) and each of which survived BOTH
// parkingFuel.test.mjs AND this file's round-1 pins:
//   R2-M2 AC-1 `commercial` reading office_job.spacesPerJob (0.25) instead of
//         retail_job.spacesPerTripEnd (0.35)          — BUG filed, closed here
//   R2-M5 AC-1 `office` reading industrial_job.spacesPerJob (0.5) instead of
//         office_job.spacesPerJob (0.25)              — BUG filed, closed here
//   R2-M3 AC-2 NEIGHBOUR_OFFSETS extended to the 8 diagonal+orthogonal
//         neighbours (the doc says "orthogonally-adjacent")
//   R2-M6 AC-2 the isOnline(s, b) guard dropped from kerbEligibleRoadTilesOf
//         (an unbuilt/offline road still supplying kerb spaces)
// Root cause of all four: no author fixture contains an office or commercial
// building, a diagonal-only road, or an offline road.

test('attack R2-M2/R2-M5: AC-1 office and commercial tiles read their OWN parking.json rate (office_job.spacesPerJob / retail_job.spacesPerTripEnd), never a sibling land-use row', () => {
  const officeRate = parkingJson.demandByLandUse.office_job.spacesPerJob;
  const retailRate = parkingJson.demandByLandUse.retail_job.spacesPerTripEnd;
  const industrialRate = parkingJson.demandByLandUse.industrial_job.spacesPerJob;
  // Precondition: the three rates are pairwise distinct in the live data, so
  // a row-swap mutant is numerically distinguishable (not an equivalent mutant).
  assert.notEqual(officeRate, retailRate, 'precondition: office_job and retail_job rates differ in the live parking.json');
  assert.notEqual(officeRate, industrialRate, 'precondition: office_job and industrial_job rates differ');
  assert.notEqual(retailRate, industrialRate, 'precondition: retail_job and industrial_job rates differ');

  // off_tower (kind 'office'), com_mall (kind 'commercial'), ind_light
  // (kind 'industrial') — one demand-generating tile of each, on one board so
  // they share a single ladder rung.
  const s = board(
    [
      { id: 1, spec: 'off_tower', x: 10, y: 10 },
      { id: 2, spec: 'com_mall', x: 40, y: 40 },
      { id: 3, spec: 'ind_light', x: 70, y: 70 },
      { id: 4, spec: 'res_lowrise', x: 12, y: 12 },
    ],
    9000,
  );
  const tiles = demandForecastOf(s);
  const byKey = new Map(tiles.map((t) => [`${t.x},${t.y}`, t]));
  const demand = parkingDemandOf(s);
  for (const key of ['10,10', '40,40', '70,70']) {
    const t = byKey.get(key);
    assert.ok(t && t.workersActual > 0, `precondition: the fixture tile ${key} carries non-zero workersActual from the real demand forecast`);
  }
  // Hand-computed from the raw parking.json rate x the REAL workersActual
  // queried at test time (GR#15 — no typed 0.25/0.35/0.5 literal).
  assert.equal(demand.get('10,10').demanded, officeRate * byKey.get('10,10').workersActual, 'office tile uses office_job.spacesPerJob');
  assert.equal(demand.get('40,40').demanded, retailRate * byKey.get('40,40').workersActual, 'commercial tile uses retail_job.spacesPerTripEnd');
  assert.equal(demand.get('70,70').demanded, industrialRate * byKey.get('70,70').workersActual, 'industrial tile uses industrial_job.spacesPerJob');
  // And explicitly NOT a sibling row (what each survivor mutant produced).
  assert.notEqual(demand.get('10,10').demanded, industrialRate * byKey.get('10,10').workersActual, 'office tile does NOT use the industrial rate (R2-M5)');
  assert.notEqual(demand.get('40,40').demanded, officeRate * byKey.get('40,40').workersActual, 'commercial tile does NOT use the office rate (R2-M2)');
});

test('attack R2-M3: AC-2 kerb supply counts ORTHOGONALLY-adjacent parking-eligible road tiles only — a diagonal-only road contributes exactly 0', () => {
  // A demand tile at (10,10) with a parking-eligible road at (11,11) ONLY —
  // diagonal, never orthogonal. The doc's AC-2 says "orthogonally-adjacent".
  const diagonalOnly = board([{ id: 1, spec: 'res_lowrise', x: 10, y: 10 }, { id: 2, spec: 'road', x: 11, y: 11 }], 120);
  assert.equal(kerbParkingSupplyOf(diagonalOnly).get('10,10'), 0, 'a diagonally-adjacent road supplies NO kerb spaces (an 8-neighbour mutant reports > 0)');
  // Control: the SAME road moved to an orthogonal neighbour does supply kerb
  // spaces — proving the 0 above is a real exclusion, not a dead fixture.
  const orthogonal = board([{ id: 1, spec: 'res_lowrise', x: 10, y: 10 }, { id: 2, spec: 'road', x: 11, y: 10 }], 120);
  assert.ok(kerbParkingSupplyOf(orthogonal).get('10,10') > 0, 'control: the same road orthogonally adjacent DOES supply kerb spaces');
});

test('attack R2-M6: AC-2 kerb supply counts ONLINE roads only — an offline (still-under-construction) road contributes exactly 0', () => {
  // isOnline (data.ts) returns false while s.tick - b.builtTick <
  // constructionTicks(sp); a builtTick in the FUTURE of s.tick is offline for
  // any non-negative construction time.
  const offlineRoad = board(
    [{ id: 1, spec: 'res_lowrise', x: 10, y: 10 }, { id: 2, spec: 'road', x: 11, y: 10, builtTick: 1_000_000 }],
    120,
  );
  assert.equal(kerbParkingSupplyOf(offlineRoad).get('10,10'), 0, 'an offline road supplies NO kerb spaces (dropping the isOnline guard reports > 0)');
  // Control: the identical road with no builtTick (online) does supply.
  const onlineRoad = board([{ id: 1, spec: 'res_lowrise', x: 10, y: 10 }, { id: 2, spec: 'road', x: 11, y: 10 }], 120);
  assert.ok(kerbParkingSupplyOf(onlineRoad).get('10,10') > 0, 'control: the identical ONLINE road DOES supply kerb spaces');
});
