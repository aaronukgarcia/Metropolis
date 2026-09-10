// trafficDemand.test.mjs — FEAT-2326609795 inc2 "DEMAND FORECAST"
// (docs/planning/acceptance/FEAT-2326609792-inc2.md AC-1..AC-9).
//
// Run with `node tools/test/scoped.mjs webconsole/test/trafficDemand.test.mjs`
// (node --test with type-stripping, so this exercises the exact shipped
// TypeScript modules — same discipline as feat-2326609772-segments-inc2.test.mjs).
//
// Every pin states its own mutant (prove-can-fail, GR#21 discipline).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import {
  demandForecastOf,
  forecastLineUsage,
  forecastSegmentUsage,
  forecastUnattributedOf,
  modeShareOf,
  ladderPointOf,
  KIND_TO_FREIGHT_SECTOR,
  nearestSegmentWeights,
  __resetBfsOpCounterForTest,
  __getBfsOpCounterForTest,
  __resetOffMapSeedsDroppedCounterForTest,
  __getOffMapSeedsDroppedCounterForTest,
} from '../src/sim/trafficDemand.ts';
import { SPECS, lineSegmentIdByTileOf, filledJobsBySector } from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';
import { MAP_W, MAP_H } from '../src/sim/grid.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, '..', '..');
const trafficDemandSrc = readFileSync(
  path.join(repoRoot, 'webconsole', 'src', 'sim', 'trafficDemand.ts'),
  'utf8',
);
const tripGeneration = JSON.parse(
  readFileSync(path.join(repoRoot, 'data', 'traffic', 'trip_generation.json'), 'utf8'),
);
const vehicleClasses = JSON.parse(
  readFileSync(path.join(repoRoot, 'data', 'traffic', 'vehicle_classes.json'), 'utf8'),
);
const scaleLadder = JSON.parse(
  readFileSync(path.join(repoRoot, 'data', 'traffic', 'scale_ladder.json'), 'utf8'),
);

function board(buildings, population = 0) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, population };
}

// No builtTick -> isOnline() returns true immediately (data.ts:849 `if
// (b.builtTick == null) return true;`) -- bypasses construction/road-gate
// checks entirely, matching the sibling segment tests' `roadNear` pattern
// but simpler since this module never reads road connectivity.
function res(id, x, y) {
  return { id, spec: 'res_hut', x, y };
}
function road(id, spec, x, y) {
  return { id, spec, x, y };
}

const RUNG0 = scaleLadder.rungs[0]; // population 100 -- the ladder's own floor
const TRIP_RATE_RUNG0 = RUNG0.tripRatePersonPerDay;
const COMMUTE_LEGS = tripGeneration.workerTripRate.commuteLegsPerWorkerPerDay.value;

test('AC-1: per-tile ACTUAL residents/workers (capacity x occupancy fraction), not raw capacity', () => {
  // res_hut: residents:8 capacity (data.ts res_hut spec), off_suite: jobs:25.
  const s = board([res(1, 0, 0), { id: 2, spec: 'off_suite', x: 5, y: 5 }], 4);
  // onlineResidentsCapacity = 8 (one res_hut) -> occupancy = 4/8 = 0.5 (SUB-100%,
  // per AC-1's false-pass note: a 100%-occupancy fixture cannot distinguish
  // actual-vs-capacity).
  const tiles = demandForecastOf(s);
  const resTile = tiles.find((t) => t.spec === 'res_hut');
  const jobTile = tiles.find((t) => t.spec === 'off_suite');
  assert.equal(resTile.residentsActual, 4, 'residentsActual = 8 capacity x 0.5 occupancy, never raw 8');
  // BUG-849 rework: workerOccupancy = filledJobsBySector(s) sum / totalJobs(s),
  // NOT totalJobs(s)/totalJobs(s) (==1 always, the vacuous equivalent-mutant
  // BUG-849 found). population=4 -> workers = 4*0.55=2.2 -> filled=round(min(2.2,25))=2
  // -> occupancy = 2/25 -> workersActual = 25 * 2/25 = 2.
  const filled = filledJobsBySector(s);
  const filledTotal = filled.primary + filled.secondary + filled.tertiary + filled.public;
  assert.equal(filledTotal, 2, 'sanity: 4 population x 0.55 working-age fraction, rounded');
  assert.equal(jobTile.workersActual, 2, 'workersActual = 25 capacity x (2 filled / 25 capacity), never raw 25 (D2 corrected by BUG-849)');
  const expectedTrips = 4 * TRIP_RATE_RUNG0 + 2 * COMMUTE_LEGS;
  assert.equal(resTile.personTrips, 4 * TRIP_RATE_RUNG0);
  assert.equal(jobTile.personTrips, 2 * COMMUTE_LEGS);
  assert.ok(expectedTrips > 0);
  // MUTANT: replacing the occupancy-fraction multiply with `x 1` (always-full
  // occupancy) would report residentsActual = 8, not 4 -- reds this pin.
});

test('AC-2: modeShareOf reads ONLY ladderPoint.fields modeShare.<id> leaves, sorted-safe, matching rung 0 below the floor', () => {
  const s = board([res(1, 0, 0)], 4); // population 4 < first rung (100) -> clamps to rung 0
  const point = ladderPointOf(s);
  assert.equal(point.population, RUNG0.population, 'below-floor population clamps to the first rung');
  const shares = modeShareOf(point);
  for (const [modeId, value] of Object.entries(RUNG0.modeShare)) {
    assert.equal(shares[modeId], value, `modeShare.${modeId} must come from the ladder rung, not a re-derived density lookup`);
  }
  // AC-2's OWN check (doc, not a value assertion): no second density model.
  assert.doesNotMatch(trafficDemandSrc, /mode_share_by_density/, 'trafficDemand.ts must never import mode_share_by_density.json directly');
  // MUTANT: a fresh mode_share_by_density.json import bypassing the ladder
  // would still pass a value comparison (the tables agree today) but reds
  // this grep-based structural check, per the doc's explicit false-pass note.
});

test('AC-3: freight tonnes/day + vehicle-trips trace to freightTonnesPerJobPerDay.manufacturing and vehicle_classes.json capacityTonnes', () => {
  assert.equal(KIND_TO_FREIGHT_SECTOR.industrial, 'manufacturing', 'industrial kind maps to the manufacturing freight sector (doc AC-3 literal example)');
  // ind_heavy: jobs:110, no capacityTiers -> capacityAtTier falls back to sp.jobs = 110.
  const s = board([{ id: 1, spec: 'ind_heavy', x: 0, y: 0 }], 100);
  const tiles = demandForecastOf(s);
  const tile = tiles.find((t) => t.spec === 'ind_heavy');
  const rate = tripGeneration.freightTonnesPerJobPerDay.manufacturing.tonnesPerJobPerDay;
  // BUG-849 rework: population is NO LONGER irrelevant to workerOccupancy --
  // workers = 100*0.55=55 -> filled=round(min(55,110))=55 -> occupancy=55/110=0.5.
  const filled = filledJobsBySector(s);
  const filledTotal = filled.primary + filled.secondary + filled.tertiary + filled.public;
  assert.equal(filledTotal, 55, 'sanity: 100 population x 0.55 working-age fraction');
  const expectedWorkersActual = 110 * (filledTotal / 110);
  const expectedTonnes = expectedWorkersActual * rate;
  assert.equal(tile.freightTonnesPerDay, expectedTonnes);

  const point = ladderPointOf(s);
  const vehicleIds = ['cargo_van', 'rigid_truck', 'articulated_truck'];
  const capacityById = Object.fromEntries(
    vehicleClasses.roadVehicles.filter((v) => vehicleIds.includes(v.id)).map((v) => [v.id, v.capacityTonnes]),
  );
  let weightedSum = 0;
  let totalShare = 0;
  for (const id of vehicleIds) {
    const share = point.fields.find((f) => f.key === `freightTonnesByVehicleClass.${id}`)?.value ?? 0;
    weightedSum += share * capacityById[id];
    totalShare += share;
  }
  const blended = totalShare > 0 ? weightedSum / totalShare : capacityById.rigid_truck;
  assert.equal(tile.freightVehicleTrips, expectedTonnes / blended);
  // MUTANT: hand-typing rigid_truck's capacity as its capacityTonnesRange
  // midpoint instead of reading vehicle_classes.json's canonical
  // capacityTonnes field would diverge from `blended` computed here the
  // moment the fixture's capacityTonnes is edited independently (doc's mutant).
});

test('AC-4: forecastLineUsage runs in parallel with lineUsageOf (never reads .usage as an input) and reports a divergenceRatio', () => {
  const s = board(
    [
      ...[0, 1].map((i) => road(100 + i, 'rd_aroad', i, 0)),
      res(1, 0, 1),
      res(2, 1, 1),
    ],
    8,
  );
  const forecast = forecastLineUsage(s);
  const entry = forecast.get('rd_aroad');
  assert.ok(entry, 'rd_aroad present in the parallel forecast map');
  assert.ok(Number.isFinite(entry.demand) && Number.isFinite(entry.legacyUsage) && Number.isFinite(entry.divergenceRatio));
  assert.equal(entry.divergenceRatio, Math.abs(entry.demand - entry.legacyUsage) / Math.max(1, entry.legacyUsage));
  // Structural anti-circularity check (AC-4's own false-pass note): within
  // forecastLineUsage's body, `u.usage` (the ONLY handle on lineUsageOf's
  // per-class figure) may only feed `legacyUsage`/`divergenceRatio`, never
  // `demand`.
  const fnBody = trafficDemandSrc.slice(
    trafficDemandSrc.indexOf('export const forecastLineUsage'),
    trafficDemandSrc.indexOf('// --- AC-5'),
  );
  const demandAssignLines = fnBody.split('\n').filter((l) => /\bdemand\s*=/.test(l));
  for (const line of demandAssignLines) {
    assert.doesNotMatch(line, /u\.usage/, `demand assignment must never read u.usage (aliasing): "${line.trim()}"`);
  }
  // MUTANT: feeding lineUsageOf(s).usage into forecastLineUsage's own demand
  // computation (aliasing) would make divergenceRatio always 0 -- caught by
  // the structural grep above, not a value assertion (doc's false-pass note).
});

test('AC-5/AC-6: nearest-segment demand attribution DIFFERS by spatial proximity and sums exactly to the class demand (>=3 segments)', () => {
  // Three isolated 2-tile rd_aroad segments, far apart (gaps of 8 tiles) so
  // the flood-fill in lineSegmentIndexOf never merges them.
  const buildings = [
    road(1, 'rd_aroad', 0, 0), road(2, 'rd_aroad', 1, 0), // segment A
    road(3, 'rd_aroad', 10, 0), road(4, 'rd_aroad', 11, 0), // segment B
    road(5, 'rd_aroad', 20, 0), road(6, 'rd_aroad', 21, 0), // segment C
    // Dense residential cluster next to segment A (5 buildings).
    res(10, 0, 1), res(11, 1, 1), res(12, 0, 2), res(13, 1, 2), res(14, 0, 3),
    // One residential building next to segment C.
    res(20, 20, 1),
    // Nothing near segment B.
  ];
  const s = board(buildings, 24); // onlineResidentsCapacity = 6 * 8 = 48 -> occupancy 0.5

  const segIndex = forecastSegmentUsage(s);
  const bySegKey = [...segIndex.values()].filter((e) => e.spec === 'rd_aroad');
  assert.ok(bySegKey.length >= 3, 'three distinct rd_aroad segments present');

  // Locate which segmentId belongs to which physical run via the shared tile->segment lookup.
  const tileToSegment = lineSegmentIdByTileOf(s);
  const segA = tileToSegment.get('0,0');
  const segB = tileToSegment.get('10,0');
  const segC = tileToSegment.get('20,0');
  assert.notEqual(segA, segB);
  assert.notEqual(segB, segC);

  const demandOf = (segmentId) => segIndex.get(segmentId).demand;
  assert.ok(demandOf(segA) > demandOf(segC), 'segment A (5 nearby residents) carries more demand than segment C (1 nearby resident)');
  assert.ok(demandOf(segC) > demandOf(segB) || demandOf(segB) === 0, 'segment B (no nearby residents) carries the least demand');
  // MUTANT: capacity-share attribution (today's lineSegmentIndexOf behaviour)
  // would make demandOf(segA) === demandOf(segC) === demandOf(segB) since all
  // three segments have identical capacity (2 tiles each, same spec) -- reds
  // this differentiation assertion (doc's exact mutant).

  // AC-6 conservation: sum over the class's segments === forecastLineUsage's class demand, exactly.
  const forecast = forecastLineUsage(s);
  const classDemand = forecast.get('rd_aroad').demand;
  const sum = demandOf(segA) + demandOf(segB) + demandOf(segC);
  assert.equal(sum, classDemand, 'segment demand sums EXACTLY to the class demand (floor-with-remainder-on-last)');
  // MUTANT: dropping the remainder correction (plain floor on every segment,
  // no last-segment adjustment) would red this exact-sum assertion by the
  // total rounding loss.
});

test('AC-7: money is untouched -- no budget/treasury/Pounds/Revenue identifier in trafficDemand.ts', () => {
  assert.doesNotMatch(trafficDemandSrc, /budget|treasury|Pounds|Revenue/i);
  // MUTANT: wiring divergenceRatio into a wellbeing/income term would
  // introduce a money-shaped identifier, caught by this grep.
});

test('AC-9: no Date.now/Math.random/localStorage, and no s.citizens read (structural, no such field exists in webconsole SimState)', () => {
  assert.doesNotMatch(trafficDemandSrc, /Date\.now|Math\.random|localStorage/);
  assert.doesNotMatch(trafficDemandSrc, /s\.citizens|\.citizens\b/);
});

test('AC-9: exported forecast functions take (s: SimState) only -- arity 1, cannot scale with citizen count by construction', () => {
  assert.equal(demandForecastOf.length, 1);
  assert.equal(forecastLineUsage.length, 1);
  assert.equal(forecastSegmentUsage.length, 1);
  // MUTANT: a signature threading a citizens array (or anything beyond s)
  // through these exports would change .length -- reds this pin.
});

// RENAMED (rework, per BUG-847 round report): this only proves MEMOISATION
// -- calling the same memoOnState export 10 times on the SAME state object
// is byte-identical by definition of the memo cache, it says nothing about
// order-independence of the underlying computation. Kept because
// memoisation itself is a real invariant (AC-9), but the actual
// order-independence claim is proven by the shuffle-determinism test below,
// adopted as a PERMANENT pin from the attacker's round (opus-round-feat795-inc2).
test('AC-9 (memoisation only, NOT order-independence): 10 reruns on the SAME state object are byte-identical (JSON)', () => {
  const s = board(
    [
      road(1, 'rd_aroad', 0, 0), road(2, 'rd_aroad', 1, 0),
      { id: 3, spec: 'off_suite', x: 5, y: 5 },
      res(4, 0, 1),
    ],
    50,
  );
  const first = JSON.stringify({
    d: demandForecastOf(s),
    l: [...forecastLineUsage(s).entries()],
    seg: [...forecastSegmentUsage(s).entries()],
  });
  for (let i = 0; i < 10; i++) {
    const again = JSON.stringify({
      d: demandForecastOf(s),
      l: [...forecastLineUsage(s).entries()],
      seg: [...forecastSegmentUsage(s).entries()],
    });
    assert.equal(again, first, `rerun ${i} diverged from the first run`);
  }
});

// ADOPTED PERMANENTLY from the attacker's round (opus-round-feat795-inc2,
// zz-round-props.test.mjs "ORDER INDEPENDENCE") -- proves GR#21
// order-independence for real: a FRESH state object (`reseed`, not the same
// `s`) with the SAME buildings in a DIFFERENT array order must produce
// byte-identical output. This is the test the renamed one above cannot be.
function shuffle(a, seed) {
  const r = [...a];
  let x = seed;
  for (let i = r.length - 1; i > 0; i--) {
    x = (x * 1103515245 + 12345) % 2147483648;
    const j = x % (i + 1);
    [r[i], r[j]] = [r[j], r[i]];
  }
  return r;
}
function snapshot(s) {
  return JSON.stringify({
    d: demandForecastOf(s),
    l: [...forecastLineUsage(s).entries()],
    seg: [...forecastSegmentUsage(s).entries()],
  });
}
test('AC-9: ORDER INDEPENDENCE -- 3 seeded shuffles of s.buildings produce byte-identical output on a fresh state', () => {
  const s = board(
    [
      road(1, 'rd_aroad', 0, 0), road(2, 'rd_aroad', 1, 0), road(3, 'rd_aroad', 10, 0), road(4, 'rd_aroad', 11, 0),
      res(5, 0, 1), res(6, 1, 1), { id: 7, spec: 'off_suite', x: 5, y: 5 }, res(8, 20, 1),
    ],
    8000,
  );
  const base = snapshot(s);
  for (const seed of [1, 7, 99]) {
    const s2 = { ...s, buildings: shuffle(s.buildings, seed) };
    assert.equal(snapshot(s2), base, `shuffle seed ${seed} changed the output (GR#21 order-dependence)`);
  }
  // MUTANT (e2_unsorted_sources / e_unsorted_map_iteration, attacker round):
  // dropping the `.sort()` on sourceTileKeys or the frontier-key iteration
  // inside nearestSegmentWeights makes the result depend on Map/array
  // insertion order, which depends on s.buildings' array order -- reds
  // this pin the moment two shuffles disagree.
});

test('AC-1/AC-9: NO free lunch -- population above the ladder max (98,000,000) surfaces the registry error, never extrapolates', () => {
  const s = board([res(1, 0, 0)], 99_000_000);
  assert.throws(() => ladderPointOf(s), /MET-V899/);
});

// --- BUG-847 rework: BFS cost bounded by the map + data-sourced radius,   --
// --- never by the city's own occupied bounding-box diameter.              --

test('BUG-847: forecastSegmentUsage BFS op count does NOT keep growing with city bbox diameter once past the data-sourced radius cap (sparse outpost, along one axis so MAP_H never clips it first)', () => {
  // Outpost on the X AXIS (y stays 0..5, well inside MAP_H=368) so the
  // measurement isolates the RADIUS cap, not an incidental MAP_H clip --
  // MAP_W=624 gives plenty of room to grow past MAX_ATTRIBUTION_RADIUS_TILES
  // (250) while staying safely on-map.
  function sparse(D) {
    const bs = [];
    let id = 1;
    bs.push(road(id++, 'rd_aroad', 0, 0));
    bs.push(road(id++, 'rd_aroad', 1, 0));
    for (let i = 0; i < 20; i++) bs.push(res(id++, 2 + (i % 5), 1 + Math.floor(i / 5)));
    bs.push(res(id++, D, 0)); // one far-flung outpost, same row
    return board(bs, 50);
  }
  const ops = {};
  for (const D of [50, 100, 200, 300, 600]) {
    const s = sparse(D);
    __resetBfsOpCounterForTest();
    forecastSegmentUsage(s);
    ops[D] = __getBfsOpCounterForTest();
  }
  // Pre-fix behaviour (BUG-847 measured): ops kept growing (quadrupling on a
  // 2D-diameter fixture) with no ceiling as D grew, because radius ===
  // the bbox diameter with no cap. Post-fix: once D exceeds
  // MAX_ATTRIBUTION_RADIUS_TILES (250), ops must plateau -- 300 and 600 (both
  // past the cap) must be equal (the BFS exhausts its radius budget, not the
  // map).
  assert.equal(ops[300], ops[600], `BFS ops must plateau once past the radius cap, independent of how much further the outpost sits: ops=${JSON.stringify(ops)}`);
  assert.ok(ops[200] > ops[100], 'sanity: ops still grow with distance BELOW the cap');
  // MUTANT (bug847_no_radius_cap): reverting forecastSegmentUsage's `const
  // radius = Math.min(bboxRadius, MAX_ATTRIBUTION_RADIUS_TILES)` to `const
  // radius = bboxRadius` makes ops[600] > ops[300] (still growing with
  // distance) -- reds the plateau assertion.
});

test('BUG-847: nearestSegmentWeights (via considerNeighbour) bounds neighbours to [0,MAP_W) x [0,MAP_H) before visiting (structural pin)', () => {
  // A pure op-count test cannot reliably distinguish "map-bounds check
  // present" from "absent" once the radius cap already bounds total BFS
  // layers (removing the bounds check does not, by itself, make the loop
  // run more LAYERS -- it only lets a layer generate a few extra phantom
  // off-map candidate keys per corner tile, a small constant-factor cost
  // difference that a wall-clock-free op-count assertion cannot safely
  // threshold without becoming a wall-clock-shaped flaky test, GR#21). This
  // is therefore a STRUCTURAL check (mirrors AC-2's own doc-endorsed
  // grep-based false-pass guard) that the neighbour-examination path
  // explicitly checks map bounds before ever touching the flat visited
  // arrays.
  //
  // BUG-852 retry: the bounds check moved into the standalone
  // `considerNeighbour` helper (called by nearestSegmentWeights's BFS loop,
  // not inlined) -- scoped to JUST that function's body so this pin cannot
  // false-pass via `boundedNearestSourceMapOf`'s OWN identical-looking bound
  // check a few hundred lines below (that sibling function is untouched by
  // this retry and keeps its own pin elsewhere; scoping to `nearestSegmentWeights('
  // .. 'forecastSegmentUsage' used to silently include it too -- a
  // BUG-883-shaped false-pass this retry closes at the same time).
  const fnBody = trafficDemandSrc.slice(
    trafficDemandSrc.indexOf('function considerNeighbour('),
    trafficDemandSrc.indexOf('function nearestSegmentWeights('),
  );
  assert.match(
    fnBody,
    /if\s*\(nx < 0 \|\| nx >= MAP_W \|\| ny < 0 \|\| ny >= MAP_H\)\s*return;/,
    'considerNeighbour must bounds-check every candidate neighbour against MAP_W/MAP_H before touching the flat visited arrays (BUG-847)',
  );
  // MUTANT (bug847_no_map_bound): dropping this bounds check reds the
  // structural match above; a fixture whose source segment sits at a map
  // CORNER (0,0) would otherwise waste BFS work generating negative-x/-y
  // candidate indices every layer, once per line class, for the life of the
  // BFS -- exactly BUG-847's "floods the empty off-map plane" finding. Live
  // scratch-copy proof: deleting the `if (...) return;` line from
  // considerNeighbour in a scratch copy reds this exact assertion (verified
  // this session, see the BOW comment).
});

test('BUG-847: conservation -- attributed + unattributed weight equals total demand-tile weight exactly, per class', () => {
  const bs = [];
  let id = 1;
  bs.push(road(id++, 'rd_aroad', 0, 0));
  bs.push(road(id++, 'rd_aroad', 1, 0));
  for (let i = 0; i < 10; i++) bs.push(res(id++, 2 + (i % 5), 1 + Math.floor(i / 5)));
  bs.push(res(id++, 260, 260)); // beyond MAX_ATTRIBUTION_RADIUS_TILES (250) from the road
  const s = board(bs, 60);
  forecastSegmentUsage(s); // populates the unattributed side table
  const unattributed = forecastUnattributedOf(s);
  const entry = unattributed.get('rd_aroad');
  assert.ok(entry, 'rd_aroad has an unattributed-demand entry');
  assert.ok(entry.weight > 0, 'the far outpost tile is genuinely unattributed for rd_aroad (beyond the radius cap)');
  assert.ok(entry.tileCount >= 1);
  // MUTANT: if the far outpost's weight were silently dropped instead of
  // reported (e.g. forecastUnattributedOf always returning zero entries),
  // this pin's entry.weight > 0 assertion reds.
});

// --- BUG-848: mode-split value pin at a NON-rung-0 population -------------

test('BUG-848: forecastLineUsage road-class demand traces to the RUNG-10000 modeShare fields, not a hardcoded 0.9 or the rung-0-only 0.775', () => {
  // scale_ladder.json rung population=10000's own road-mode sum (car+
  // motorbike+taxi+bus) is 0.718 -- distinct from BOTH a hardcoded 0.9 AND
  // rung 0's own 0.775 (which a rung-0-only fixture cannot distinguish from
  // a compliant read, per BUG-848's own finding).
  const s = board(
    [road(1, 'rd_aroad', 0, 0), road(2, 'rd_aroad', 1, 0), res(3, 0, 1), res(4, 1, 1)],
    10000,
  );
  const point = ladderPointOf(s);
  assert.equal(point.population, 10000, 'exact rung, not an interpolated point');
  const shares = modeShareOf(point);
  const roadSum = shares.car + shares.motorbike + shares.taxi + shares.bus;
  assert.ok(Math.abs(roadSum - 0.718) < 1e-9, `rung 10000 road-mode sum must be its OWN value (0.718), got ${roadSum}`);
  assert.notEqual(roadSum, 0.9, 'sanity: the banned hardcoded value');
  assert.notEqual(roadSum, 0.775, 'sanity: rung-0-only value, cannot appear at rung 10000');

  const tiles = demandForecastOf(s);
  let totalPersonTrips = 0;
  let totalFreightVehicleTrips = 0;
  for (const t of tiles) {
    totalPersonTrips += t.personTrips;
    totalFreightVehicleTrips += t.freightVehicleTrips;
  }
  const expectedRoadPersonDemand = totalPersonTrips * roadSum;
  const expectedTotalRoadDemand = expectedRoadPersonDemand + totalFreightVehicleTrips;

  const forecast = forecastLineUsage(s);
  const entry = forecast.get('rd_aroad');
  assert.ok(entry, 'rd_aroad present');
  // Only ONE road class present in this fixture -> capacity-share ratio is
  // 1 (totalDrivableCap === this class's own capacity), so demand ===
  // expectedTotalRoadDemand exactly.
  assert.ok(
    Math.abs(entry.demand - expectedTotalRoadDemand) < 1e-6,
    `expected ${expectedTotalRoadDemand}, got ${entry.demand} (roadSum=${roadSum})`,
  );
  // MUTANT (b_hardcoded_mode_share, attacker round): replacing the
  // ROAD_PERSON_MODE_IDS loop with `roadPersonDemand = totalPersonTrips *
  // 0.9` (or 0.775, rung 0's OWN sum) diverges from expectedTotalRoadDemand
  // at this non-rung-0 population -- reds this pin, where a rung-0-only
  // fixture could not (BUG-848's exact finding).
});

// --- BUG-849: workerOccupancy is a REAL filled/capacity ratio, not x1 -----

test('BUG-849: workerOccupancy uses filledJobsBySector, not totalJobs(s)/totalJobs(s) (proven NOT identically 1 below full employment)', () => {
  // A large job capacity (ind_heavy: 110 jobs) with a SMALL population, so
  // WORKING_AGE_FRACTION * population is well below job capacity ->
  // filledJobsBySector's sum is LESS than totalJobs(s) -> occupancy < 1.
  const s = board([{ id: 1, spec: 'ind_heavy', x: 0, y: 0 }], 50); // 50 * 0.55 = 27.5 workers << 110 jobs
  const tiles = demandForecastOf(s);
  const tile = tiles.find((t) => t.spec === 'ind_heavy');
  const filled = filledJobsBySector(s);
  const filledTotal = filled.primary + filled.secondary + filled.tertiary + filled.public;
  assert.ok(filledTotal < 110, 'sanity: this fixture is genuinely below full job capacity');
  const expectedWorkersActual = 110 * (filledTotal / 110);
  assert.ok(
    Math.abs(tile.workersActual - expectedWorkersActual) < 1e-6,
    `workersActual must equal capacity x (filled/capacity), got ${tile.workersActual} expected ${expectedWorkersActual}`,
  );
  assert.ok(tile.workersActual < 110, 'workersActual must be BELOW raw capacity when jobs are not fully filled');
  // MUTANT (a2_worker_occupancy_x1 class, and the pre-fix
  // totalJobs(s)/totalJobs(s) shape): either `cap * 1` or the vacuous
  // self-ratio reports workersActual === 110 (raw capacity) regardless of
  // how few workers exist -- reds the `< 110` assertion above.
});

// --- BUG-850: population NaN is honest-absence (registry error), never a -
// --- silent NaN rung; freight-sector gap is honest-absence, never a throw -

test('BUG-850: non-finite population throws the registry error, never a silent NaN ladder rung', () => {
  const s = board([res(1, 0, 0)], NaN);
  assert.throws(() => ladderPointOf(s), /MET-V912/, 'NaN population must fail loud with the registry code, never a silent NaN rung');
  const sInf = board([res(1, 0, 0)], Infinity);
  assert.throws(() => ladderPointOf(sInf), /MET-V912/);
  // Negative-but-finite still clamps up to the floor rung (unchanged,
  // documented behaviour) -- must NOT throw.
  const sNeg = board([res(1, 0, 0)], -5);
  assert.doesNotThrow(() => ladderPointOf(sNeg));
  assert.equal(ladderPointOf(sNeg).population, 100, 'negative population clamps to the floor rung');
  // MUTANT: removing the Number.isFinite guard reproduces BUG-850's silent
  // NaN rung -- ladderPointOf(s).population would itself be NaN and this
  // test's assert.throws would red (no throw occurs).
});

test('BUG-850: an unmapped freight-sector kind is honest-absence (zero freight, one-shot log), never a render-path throw', () => {
  // Monkey-patch a copy of KIND_TO_FREIGHT_SECTOR is not possible (frozen,
  // module-private lookup) -- instead exercise the REAL fail-loud path this
  // module already proves total today (KIND_TO_FREIGHT_SECTOR has no gaps,
  // attacker-verified) by asserting the property that WOULD have to hold if
  // a gap existed: demandForecastOf must never throw for any known SPECS
  // kind that carries jobs, across every currently-mapped kind.
  const jobKinds = Object.values(SPECS)
    .filter((sp) => sp.jobs != null)
    .map((sp) => sp.kind);
  const uniqueKinds = [...new Set(jobKinds)];
  for (const kind of uniqueKinds) {
    assert.ok(kind in KIND_TO_FREIGHT_SECTOR, `job-bearing kind "${kind}" must be in KIND_TO_FREIGHT_SECTOR (BUG-850 honest-absence path exists for when this ever goes stale)`);
  }
  // Direct structural proof the honest-absence path exists at all (never a
  // bare `throw` left in the hot loop for the sector-unmapped case): the
  // freight block must not unconditionally throw when `sector` is falsy.
  assert.doesNotMatch(
    trafficDemandSrc.slice(trafficDemandSrc.indexOf('let freightTonnesPerDay = 0;'), trafficDemandSrc.indexOf('const freightVehicleTrips')),
    /if \(!sector\) \{\s*\n\s*throw registryError/,
    'an unmapped freight sector must not throw from inside the render-path demandForecastOf loop (BUG-850)',
  );
  // MUTANT: reverting to `if (!sector) throw registryError(...)` inside the
  // freight block reds the structural assertion above.
});

// --- BUG-847: sort structural pins (op-count/shuffle value tests cannot   -
// --- reliably distinguish these -- demandForecastOf's own output is       -
// --- ALREADY sorted by (x,y,spec) before forecastSegmentUsage ever builds -
// --- tileWeight/specTileKeys from it, so dropping nearestSegmentWeights's -
// --- OWN internal .sort() calls happens not to change the result on any  -
// --- fixture small enough for a fast test -- a false-pass the doc's own  -
// --- AC-2 false-pass note already anticipates for exactly this reason:   -
// --- a structural check, not a value assertion, is what catches it.      -

test('BUG-847/GR#21: nearestSegmentWeights sorts BOTH the source-tile set and each BFS layer\'s frontier before assigning (no map-range-with-break)', () => {
  // BUG-852 retry: scoped to TILE_COUNT..boundedNearestSourceMapOf's own doc
  // comment -- i.e. the scratch buffers + considerNeighbour +
  // nearestSegmentWeights as one unit -- NOT all the way to
  // forecastSegmentUsage's doc comment, which would silently sweep in
  // boundedNearestSourceMapOf's own (untouched, differently-shaped) sort
  // call and let this pin false-pass even with nearestSegmentWeights's own
  // sort removed (the same BUG-883-shaped scoping bug the bounds-check pin
  // above had).
  const fnBody = trafficDemandSrc.slice(
    trafficDemandSrc.indexOf('const TILE_COUNT = MAP_W * MAP_H;'),
    trafficDemandSrc.indexOf('/**\n * GR#3/BUG-857 additive export'),
  );
  assert.match(fnBody, /const sortedSources = \[\.\.\.sourceTileKeys\]\.sort\(\);/, 'source tile keys must be sorted for deterministic tie-breaking');
  // BUG-852 retry: the per-layer frontier is no longer a `Map<string,string>`
  // sorted by `.keys()` -- it is an array of {idx,key} touched-tile records
  // sorted by `.key` (the SAME "x,y" string, same lexicographic order,
  // proven byte-identical to the pre-retry shape by the parity test below).
  assert.match(
    fnBody,
    /touchedKeyed\.sort\(\(a, b\) => \(a\.key < b\.key \? -1 : a\.key > b\.key \? 1 : 0\)\);/,
    'each BFS layer\'s touched-tile set must be processed in sorted "x,y"-key order',
  );
  // MUTANT (bug847_unsorted_sources / bug847_unsorted_frontier, attacker
  // round): dropping either sort call makes assignment order depend on
  // array/insertion order -- which on THIS module happens to already be
  // sorted (demandForecastOf's own output is pre-sorted by (x,y,spec)) so a
  // value/shuffle test alone cannot catch it; this structural check can.
  // Live scratch-copy proof (this session): removing the `.sort(...)` call
  // on `touchedKeyed` reds this exact assertion.
});

// ---------------------------------------------------------------------------
// BUG-866 — nearestSegmentWeights (the primitive boundedNearestSourceMapOf
// was copied FROM) seeds `visited` from sortedSources with NO bounds check,
// unlike its sibling: BUG-864(1) added the seed guard only to the new export.
// Fixed identically here (off-map seeds dropped, counted, reported via the
// additive `offMapSeedsDropped` return field). nearestSegmentWeights is not
// reachable off-map through real building placement (grid.ts always clamps
// on-map), so this pin drives the primitive directly with a synthetic
// off-map seed key -- the same reason nearestSegmentWeights was made an
// additive export for this fix.
// ---------------------------------------------------------------------------

// --- BUG-851: the freight-sector gap log routes through the registry error --
// --- path (backend.ts's recordError), never a bare console.error (GR#7/    -
// --- GR#17). KIND_TO_FREIGHT_SECTOR is frozen and attacker-verified TOTAL  -
// --- over the live catalogue (BUG-850's own test above), so the gap path   -
// --- is not reachable through real building placement -- a structural      -
// --- source check (the SAME idiom BUG-850's own "never a bare throw" pin   -
// --- above already uses for this identical unreachable-today situation).   -

test('BUG-851: logSectorGapOnce routes through backend.ts recordError (GR#7 registry-sourced), never console.error', () => {
  assert.match(
    trafficDemandSrc,
    /import \{ recordError \} from '\.\/backend\.ts';/,
    'trafficDemand.ts must import recordError from backend.ts (the app-error idiom engine.ts\'s MET-V874 site already uses)',
  );
  const fnBody = trafficDemandSrc.slice(
    trafficDemandSrc.indexOf('function logSectorGapOnce('),
    trafficDemandSrc.indexOf('// --- trip_generation.json'),
  );
  assert.match(
    fnBody,
    /recordError\(message, \{ type: 'app', code: ERR_SECTOR_UNMAPPED, action: 'trafficDemand\.demandForecastOf' \}\);/,
    'logSectorGapOnce must call recordError with the registry code ERR_SECTOR_UNMAPPED (MET-V900), not a bare console.error',
  );
  assert.doesNotMatch(fnBody, /console\.error\(/, 'logSectorGapOnce must never CALL console.error (GR#7/GR#17)');
  // MUTANT (BUG-851's own): reverting logSectorGapOnce's body to
  // `console.error(...)` reds both the recordError-call match and the
  // doesNotMatch console.error-call check above.
});

// --- BUG-853(3): residentOccupancy is clamped to [0,1] -- symmetric with ---
// --- the worker side, which clamps by construction (filledJobsBySector    -
// --- caps `filled` at totalCapacity, data.ts ~4483).                      -

test('BUG-853(3): residentOccupancy clamps to 1 -- a single res_hut (capacity from its own catalogue spec) with population 100,000 reports residentsActual <= capacity, never a >12,000x overcrowded figure', () => {
  // BUG-884 GR#15: the tile's capacity is read from the catalogue spec
  // itself (SPECS['res_hut'].residents), never a hardcoded literal -- a
  // future rebalance of res_hut's capacity can't silently desync this pin.
  const resHutCapacity = SPECS['res_hut'].residents;
  const s = board([res(1, 0, 0)], 100_000);
  const tiles = demandForecastOf(s);
  const resTile = tiles.find((t) => t.spec === 'res_hut');
  assert.ok(
    resTile.residentsActual <= resHutCapacity,
    `residentsActual must never exceed the tile's own capacity (${resHutCapacity}), got ${resTile.residentsActual}`,
  );
  assert.equal(resTile.residentsActual, resHutCapacity, 'clamped occupancy (1.0) x capacity = capacity exactly');
  // MUTANT: dropping the Math.min(1, ...) clamp on residentOccupancy would
  // report residentsActual = resHutCapacity * (100000/resHutCapacity) =
  // 100000 for this single tile -- reds the `<= resHutCapacity` assertion
  // (BUG-853's own measured pre-fix figure).
});

// --- BUG-853(2): workerOccupancy denominator uses totalJobsBySector(s),  --
// --- the SAME basis filledJobsBySector's numerator is capped against,   --
// --- not totalJobs(s) (which counts every job-bearing kind unconditionally,--
// --- including kinds absent from KIND_TO_WAGE_SECTOR).                   -

test('BUG-853(2): workerOccupancy denominator is totalJobsBySector(s) (same basis as the numerator), not totalJobs(s)', () => {
  // Every job-bearing kind in the live catalogue IS in KIND_TO_WAGE_SECTOR
  // today (BUG-652), so totalJobs(s) === sum(totalJobsBySector(s)) for any
  // real fixture -- this test proves the SOURCE reads totalJobsBySector, not
  // that the two bases currently diverge numerically (they don't, yet).
  assert.match(
    trafficDemandSrc,
    /const jobsBySector = totalJobsBySector\(s\);/,
    'workerOccupancy denominator must be built from totalJobsBySector(s), the same capacity basis filledJobsBySector(s) is itself capped against',
  );
  assert.doesNotMatch(
    trafficDemandSrc,
    /const jobsCapTotal = totalJobs\(s\);/,
    'must not read the unconditional totalJobs(s) as the occupancy denominator (BUG-853(2): diverges from the numerator basis the day an unmapped job-bearing kind is added)',
  );
  // MUTANT: reverting to `totalJobs(s)` reds the doesNotMatch assertion
  // above; the day a job-bearing kind without a KIND_TO_WAGE_SECTOR entry is
  // added, that revert would also silently depress workerOccupancy
  // city-wide with no error anywhere (BUG-853's exact finding).
  //
  // BUG-884: this stays a source-text pin, not a behavioural one, by
  // necessity, not convenience. A behavioural pin needs a fixture where
  // totalJobs(s) != sum(totalJobsBySector(s)) -- i.e. a job-bearing
  // `kind` absent from fiscal.ts's KIND_TO_WAGE_SECTOR. Every building
  // fixture here is placed via a real SPECS[...] entry (`b.spec` indexes
  // the live catalogue in data.ts), and KIND_TO_WAGE_SECTOR is frozen and
  // total over every kind the live catalogue uses (BUG-652) -- there is no
  // kind reachable from this test file that is job-bearing yet unmapped.
  // Manufacturing one requires either a new catalogue spec (data.ts) or
  // temporarily un-mapping a kind (fiscal.ts), both production seams this
  // task is not scoped to touch (FILES YOU OWN: trafficDemand.ts +
  // its own two test files). Left as a source-text pin; BUG-881/882/883
  // carry the rest of the BUG-852 backout detail.
});

test('BUG-866: nearestSegmentWeights drops an off-map SEED key (counted, never entered into visited/attribution) -- same bound as boundedNearestSourceMapOf', () => {
  const onMapSeed = '5,5';
  const offMapSeedX = `${MAP_W + 5},5`; // off-map in x
  const offMapSeedY = `5,${MAP_H + 5}`; // off-map in y
  const offMapSeedNeg = '-3,5'; // off-map (negative)

  const tileToSegment = new Map([[onMapSeed, 'seg-a']]);
  const tileWeight = new Map([[onMapSeed, 10]]);

  __resetOffMapSeedsDroppedCounterForTest();
  const result = nearestSegmentWeights(
    [onMapSeed, offMapSeedX, offMapSeedY, offMapSeedNeg],
    tileToSegment,
    tileWeight,
    5,
  );
  const droppedShared = __getOffMapSeedsDroppedCounterForTest();

  assert.equal(result.offMapSeedsDropped, 3, `exactly the 3 off-map seeds must be counted as dropped on the return shape, got ${result.offMapSeedsDropped}`);
  assert.equal(droppedShared, 3, `the shared BUG-864-style test counter must also see the 3 drops (parity with boundedNearestSourceMapOf), got ${droppedShared}`);
  assert.equal(result.attributedTileCount, 1, 'only the on-map seed contributes attribution');
  assert.equal(result.weightBySegment.get('seg-a'), 10, 'the on-map seed\'s weight must still be attributed correctly, unaffected by the dropped off-map seeds');

  // MUTANT (BUG-866's own -- reverting the seed loop to the pre-fix
  // `for (const k of sortedSources) visited.set(k, k);` with no bounds
  // check): all 3 off-map seeds would be admitted into `visited` verbatim,
  // `offMapSeedsDropped` would read 0 instead of 3, and (since none of them
  // carry a tileWeight entry) `attributedTileCount` would happen to still
  // read 1 by coincidence on THIS fixture -- which is exactly why the
  // `offMapSeedsDropped` assertion, not the attribution-count assertion
  // alone, is the one that reliably catches the regression.
});

// ---------------------------------------------------------------------------
// BUG-883/BUG-852 RETRY -- old-vs-new PARITY.
//
// The BUG-852 retry rewrote nearestSegmentWeights's internals from a
// `Map<string,string>` visited/frontier (allocated per call, string keys
// parsed per neighbour) to flat, stamp-tagged Int32Array scratch buffers +
// integer tile indices + an integer source-id tie-break, for wall-clock
// speed (BUG-881: the REJECTED combined-across-classes attempt showed the
// opposite direction -- a per-tile `Map<class,source>` allocation regressed
// wall-clock 49-107% despite a small ops-count win -- this retry keeps the
// per-class call shape and removes the Map/string overhead INSIDE each call
// instead). BUG-883's finding was that the previous attempt's byte-identity
// claim was UNPINNED: nothing in CI proved old-output === new-output, and a
// real, output-CHANGING tie-break mutant survived the whole suite. This
// block is that missing pin: `nearestSegmentWeightsReference` below is a
// verbatim scratch copy of the PRE-RETRY per-class implementation (the one
// at HEAD before this session, `git show f5ae80e:webconsole/src/sim/trafficDemand.ts`),
// decoupled from the module's own bfsOpCounter/offMapSeedsDroppedCounter so
// comparing the two never perturbs the real module's test counters.
function nearestSegmentWeightsReference(sourceTileKeys, tileToSegment, tileWeight, radius) {
  const weightBySegment = new Map();
  if (sourceTileKeys.length === 0) return { weightBySegment, attributedTileCount: 0, offMapSeedsDropped: 0 };
  const visited = new Map(); // tileKey -> nearest source tileKey
  const sortedSources = [...sourceTileKeys].sort();
  let offMapSeedsDropped = 0;
  const onMapSources = [];
  for (const k of sortedSources) {
    const comma = k.indexOf(',');
    const x = Number(k.slice(0, comma));
    const y = Number(k.slice(comma + 1));
    if (x < 0 || x >= MAP_W || y < 0 || y >= MAP_H) {
      offMapSeedsDropped++;
      continue;
    }
    onMapSources.push(k);
    visited.set(k, k);
  }
  let attributedTileCount = 0;
  const accumulate = (m, key, v) => m.set(key, (m.get(key) ?? 0) + v);
  for (const k of onMapSources) {
    const w = tileWeight.get(k);
    if (w) {
      accumulate(weightBySegment, tileToSegment.get(k), w);
      attributedTileCount++;
    }
  }
  let frontier = onMapSources;
  let dist = 0;
  while (frontier.length > 0 && dist < radius) {
    dist++;
    const next = new Map();
    for (const key of frontier) {
      const comma = key.indexOf(',');
      const x = Number(key.slice(0, comma));
      const y = Number(key.slice(comma + 1));
      const src = visited.get(key);
      const neighbours = [[x + 1, y], [x - 1, y], [x, y + 1], [x, y - 1]];
      for (const [nx, ny] of neighbours) {
        if (nx < 0 || nx >= MAP_W || ny < 0 || ny >= MAP_H) continue;
        const nk = `${nx},${ny}`;
        if (visited.has(nk)) continue;
        const existing = next.get(nk);
        if (!existing || src < existing) next.set(nk, src);
      }
    }
    const nextFrontier = [];
    for (const nk of [...next.keys()].sort()) {
      const src = next.get(nk);
      visited.set(nk, src);
      nextFrontier.push(nk);
      const w = tileWeight.get(nk);
      if (w) {
        accumulate(weightBySegment, tileToSegment.get(src), w);
        attributedTileCount++;
      }
    }
    frontier = nextFrontier;
  }
  return { weightBySegment, attributedTileCount, offMapSeedsDropped };
}

function mapToSortedPairs(m) {
  return [...m.entries()].sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
}

function assertParity(name, sourceTileKeys, tileToSegment, tileWeight, radius) {
  const ref = nearestSegmentWeightsReference(sourceTileKeys, tileToSegment, tileWeight, radius);
  const real = nearestSegmentWeights(sourceTileKeys, tileToSegment, tileWeight, radius);
  assert.deepEqual(
    mapToSortedPairs(real.weightBySegment),
    mapToSortedPairs(ref.weightBySegment),
    `${name}: weightBySegment must be byte-identical between the retried and reference implementations`,
  );
  assert.equal(real.attributedTileCount, ref.attributedTileCount, `${name}: attributedTileCount parity`);
  assert.equal(real.offMapSeedsDropped, ref.offMapSeedsDropped, `${name}: offMapSeedsDropped parity`);
}

function gridSourceKeys(x0, y0, w, h) {
  const keys = [];
  for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) keys.push(`${x0 + x},${y0 + y}`);
  return keys;
}
function segMapFor(keys, segId) {
  const m = new Map();
  for (const k of keys) m.set(k, segId);
  return m;
}
// Deterministic scatter of demand weight across a region slightly larger
// than the source footprint, so both algorithms exercise multi-layer BFS
// expansion (not just seed-tile attribution) and genuine equal-distance
// ties between separate source tiles/clusters.
function scatterWeights(x0, y0, w, h, pad, seed) {
  const m = new Map();
  let s = seed;
  for (let y = -pad; y < h + pad; y++) {
    for (let x = -pad; x < w + pad; x++) {
      s = (s * 1103515245 + 12345) % 2147483648;
      if (s % 7 === 0) {
        const tx = x0 + x;
        const ty = y0 + y;
        if (tx >= 0 && ty >= 0 && tx < MAP_W && ty < MAP_H) m.set(`${tx},${ty}`, 1 + (s % 13));
      }
    }
  }
  return m;
}

test('BUG-883: PARITY -- (a) single-class dense grid, retried implementation is byte-identical to the reference', () => {
  const keys = gridSourceKeys(20, 20, 40, 40);
  const seg = segMapFor(keys, 'seg-single');
  const weight = scatterWeights(20, 20, 40, 40, 15, 7);
  assertParity('single-class dense grid', keys, seg, weight, 80);
});

test('BUG-883: PARITY -- (b) 4-class shared footprint (same region, 4 independent per-class calls), each byte-identical', () => {
  for (let cls = 0; cls < 4; cls++) {
    const keys = gridSourceKeys(50, 50, 25, 25);
    const seg = segMapFor(keys, `seg-shared-${cls}`);
    const weight = scatterWeights(50, 50, 25, 25, 10, 11 + cls * 101);
    assertParity(`4-class shared footprint, class ${cls}`, keys, seg, weight, 60);
  }
});

test('BUG-883: PARITY -- (c) disjoint 4-cluster city (ONE class, 4 far-apart source clusters -- real equidistant ties between clusters)', () => {
  const clusters = [
    gridSourceKeys(5, 5, 6, 6),
    gridSourceKeys(200, 5, 6, 6),
    gridSourceKeys(5, 200, 6, 6),
    gridSourceKeys(200, 200, 6, 6),
  ];
  const keys = clusters.flat();
  const seg = new Map();
  clusters.forEach((c, i) => { for (const k of c) seg.set(k, `seg-cluster-${i}`); });
  // Demand scattered across the whole span, including the midpoints between
  // clusters where a real Manhattan-distance TIE between two different
  // clusters' floods is possible.
  const weight = scatterWeights(0, 0, 210, 210, 20, 23);
  assertParity('disjoint 4-cluster city', keys, seg, weight, 130);
});

test('BUG-883: PARITY -- (d) dense grid (10,000 source tiles), byte-identical', () => {
  const keys = gridSourceKeys(0, 0, 100, 100);
  const seg = segMapFor(keys, 'seg-dense');
  const weight = scatterWeights(0, 0, 100, 100, 15, 31);
  assertParity('dense 10,000-tile grid', keys, seg, weight, 130);
});

// BUG-883's own surviving mutant, retargeted at the RETRIED implementation's
// equivalent tie-break line (`else if (srcId < tentSrcId[nIdx])` --
// considerNeighbour's integer-id form of the old `if (!existing || src <
// existing)` string tie-break). Proven RED this session via the scratch-copy
// method GR#24 requires (cp trafficDemand.ts to a scratchpad .bak, mutate
// the real file, run, restore from the .bak -- never a git command): with
// the `else if` branch deleted (first-writer-wins instead of lowest-id-wins)
// the disjoint-4-cluster parity test above reds because a real tile
// equidistant from two different clusters is attributed to whichever
// cluster's frontier happened to reach it in loop-iteration order instead of
// the lowest source id -- see the BOW comment on BUG-883/BUG-852 for the
// live transcript. This test file cannot re-run that mutation itself
// (it would require monkey-patching module-private state), so the
// documented live proof stands in for an in-suite mutant switch, same as
// the sort/bounds structural pins above already do for THEIR mutants.
test('BUG-883: tie-break is lowest-source-id (== lowest source key), NOT whichever frontier arrives first (real divergent fixture)', () => {
  // A NAIVE tie fixture (two sources equidistant on the same row) does NOT
  // discriminate the tie-break mutant: when the tie is reached directly from
  // the seed layer, frontier processing order already equals sorted-source
  // order, so "first writer" and "lowest id" coincide by construction (a
  // false-pass this session found the hard way while building this pin --
  // see the BOW comment). A REAL discriminating fixture needs the tie
  // reached at a DEEPER layer where the intermediate touched-tile keys sort
  // in a DIFFERENT order than the two sources' own keys -- found this
  // session by brute-force search over random equidistant source/tie
  // triples (scratchpad find-tie-mutant.mjs) rather than hand construction.
  // '168,36' < '68,8' lexicographically (STRING comparison, not numeric --
  // '1' < '6' as the first character decides it, even though 168 > 68 as a
  // number) -- '168,36' must win -- but the intermediate frontier tiles
  // approaching the tie point from '68,8' happen to sort EARLIER than those
  // from '168,36' at the layer the two floods actually meet, so a
  // first-writer-wins mutant picks the WRONG (higher-key) source instead.
  const keyLow = '168,36'; // lexicographically smaller -- correct winner
  const keyHigh = '68,8';
  const tie = '104,112'; // Manhattan distance 140 from BOTH sources
  const dist = Math.abs(104 - 68) + Math.abs(112 - 8);
  assert.equal(dist, 140, 'sanity: the tie point really is equidistant from both sources');
  assert.equal(dist, Math.abs(104 - 168) + Math.abs(112 - 36));
  const keys = [keyLow, keyHigh];
  const seg = new Map([[keyLow, 'seg-low'], [keyHigh, 'seg-high']]);
  const weight = new Map([[tie, 7]]);
  const radius = dist + 1;
  const ref = nearestSegmentWeightsReference(keys, seg, weight, radius);
  const real = nearestSegmentWeights(keys, seg, weight, radius);
  assert.equal(ref.weightBySegment.get('seg-low'), 7, 'reference: lowest-key source wins the tie');
  assert.equal(ref.weightBySegment.has('seg-high'), false, 'reference: higher-key source gets nothing');
  assert.equal(real.weightBySegment.get('seg-low'), 7, 'retried implementation: lowest-key source wins the tie (BUG-883 tie-break)');
  assert.equal(real.weightBySegment.has('seg-high'), false, 'retried implementation: higher-key source gets nothing');
  assertParity('deep-layer equidistant tie (brute-force-found divergent fixture)', keys, seg, weight, radius);
  // MUTANT (BUG-883's own, retargeted at considerNeighbour's integer form):
  // deleting the `else if (srcId < tentSrcId[nIdx]) { tentSrcId[nIdx] =
  // srcId; }` branch (leaving first-writer-wins) makes THIS fixture flip to
  // 'seg-high' winning instead of 'seg-low' -- proven RED live this session
  // via the GR#24 scratch-copy method (cp to a .bak, mutate the real file,
  // run, restore from the .bak) -- see the BOW comment on BUG-883/BUG-852.
  // A naive same-row/same-layer tie fixture (tried first, see above) did NOT
  // catch this same mutant -- kept as a comment here, not a test, precisely
  // because it silently coincides with the mutant and would be a false
  // sense of coverage if left in as a pin.
});
