// FEAT-2326609795 inc2 "DEMAND FORECAST" — docs/planning/acceptance/FEAT-2326609792-inc2.md
// (AC-1..AC-9, §4 D1 parallel-with-divergence-indicator, D2 workers=totalJobs).
//
// This module is the FIRST real consumer of inc1's scale ladder
// (scaleLadderData.ts / scaleLadder.ts) and inc0's per-tile trip-generation
// tables (data/traffic/trip_generation.json, data/traffic/vehicle_classes.json).
// It turns "how many residents/workers are actually on this tile" into
// person-trips/day and freight vehicle-trips/day, splits person-trips by
// mode using the CITY's current scale-ladder rung (never a second density
// model, GR#3), and re-aggregates into a per-line-class demand figure that
// runs IN PARALLEL to lineUsageOf's existing capacity-share usage (D1 — no
// consumer switch this increment) plus a per-segment demand split using
// real spatial (nearest-segment) attribution instead of lineSegmentIndexOf's
// current capacity-share split (AC-5, the actual fix for Q100163).
//
// PURE + DETERMINISTIC (GR#21): every export here is memoOnState over
// SimState only — no wall-clock read, no PRNG, no browser storage read (AC-9
// — actual counts are derived from capacity times a city-wide occupancy
// fraction, never a per-citizen-array walk; the webconsole SimState has no
// citizen-array field at all, so this is true by construction here).
//
// Money (AC-7): this file never touches a fiscal SimState field or a
// currency-shaped output. forecastLineUsage/forecastSegmentUsage are
// read-only diagnostic numbers this increment — see D1.

import type { SimState } from './types.ts';
import {
  SPECS,
  isOnline,
  capacityAtTier,
  onlineResidentsCapacity,
  totalJobs,
  filledJobsBySector,
  lineUsageOf,
  lineSegmentIndexOf,
  memoOnState,
  type LineUsage,
} from './data.ts';
import { scaleLadder } from './scaleLadderData.ts';
import { ladderAt, type LadderPoint } from './scaleLadder.ts';
import { MAP_W, MAP_H } from './grid.ts';
// AC-1/AC-3 (GR#15): the worker commute-leg rate and freight tonnes/job/day
// rates are read from trip_generation.json at load time — never hand-typed
// literals — mirroring scaleLadderData.ts's static-import shape (the doc's
// explicit instruction, §2/AC-1).
import rawTripGeneration from './traffic-data/trip_generation.json' with { type: 'json' };
import rawVehicleClasses from './traffic-data/vehicle_classes.json' with { type: 'json' };
// BUG-847 rework: data/traffic.json's maxAttributionRadiusTiles bounds the
// nearestSegmentWeights BFS radius (GR#15, no literal in TS) — see that
// file's _maxAttributionRadiusTilesSource disclosure. NOTE: this is the
// engine defaults file at data/traffic.json, NOT the data/traffic/ inc0
// research-table directory imported above.
import rawTrafficConfig from './traffic-data/traffic.json' with { type: 'json' };

// --- Registry error codes (GR#7) -------------------------------------------
// Minted via `node tools/plan/add-error.js add MET-Vnnn --mkey ui.webconsole
// ...` (data/errors.json), block V900-V903 claimed on FEAT-2326609795;
// V912-V919 claimed for the BUG-847/BUG-850 rework (V904-V911 landed
// elsewhere between the original build and this rework, so the rework block
// is non-contiguous with the original — both blocks are reserved to
// ui.webconsole, see `node tools/plan/add-error.js check`).
export const ERR_SECTOR_UNMAPPED = 'MET-V900'; // DemandForecastSectorUnmapped
export const ERR_VEHICLE_CLASS_MISSING = 'MET-V901'; // DemandForecastVehicleClassMissing
export const ERR_LADDER_FIELD_MISSING = 'MET-V902'; // DemandForecastLadderFieldMissing
export const ERR_TRAFFIC_CONFIG_MISSING = 'MET-V903'; // DemandForecastTrafficConfigMissing (BUG-847)
export const ERR_POPULATION_INVALID = 'MET-V912'; // DemandForecastPopulationInvalid (BUG-850)

function registryError(code: string, message: string): Error {
  return new Error(`${code}: ${message}`);
}

// BUG-850: one-shot (per distinct key, per page load) console log for a
// freight-sector gap discovered inside demandForecastOf's render-path loop
// — honest-absence (zero freight for that tile) instead of a render-path
// throw, but still surfaced via the registry code rather than silently
// swallowed (GR#17: a monitoring/log path, not a thrown error, for a
// derivation that runs every render).
const loggedSectorGaps = new Set<string>();
function logSectorGapOnce(key: string, message: string): void {
  if (loggedSectorGaps.has(key)) return;
  loggedSectorGaps.add(key);
  // eslint-disable-next-line no-console
  console.error(`${ERR_SECTOR_UNMAPPED}: ${message}`);
}

// --- trip_generation.json / vehicle_classes.json typed views ---------------
// These JSON files are inc0 RESEARCH tables with no dedicated loader/
// validator of their own (unlike scale_ladder.json's loadScaleLadder) — this
// module does its own minimal shape checks at module-load time (below),
// fail-loud via the registry codes above rather than a silent `undefined`
// arithmetic NaN downstream.
interface TripGenerationTable {
  workerTripRate: { commuteLegsPerWorkerPerDay: { value: number } };
  freightTonnesPerJobPerDay: Record<string, { tonnesPerJobPerDay: number }>;
}
interface VehicleClassesTable {
  roadVehicles: Array<{ id: string; capacityTonnes?: number }>;
}
const tripGeneration = rawTripGeneration as unknown as TripGenerationTable;
const vehicleClasses = rawVehicleClasses as unknown as VehicleClassesTable;

/** AC-1 GR#15: the 2.0 commute-legs-per-worker-per-day figure, read from
 * trip_generation.json at module-load time — never hand-typed into this file. */
const COMMUTE_LEGS_PER_WORKER_PER_DAY: number =
  tripGeneration.workerTripRate.commuteLegsPerWorkerPerDay.value;

/**
 * KIND -> trip_generation.json freight-sector id (AC-3). NOTE (ASM, see BOW
 * comment / report): KIND_TO_WAGE_SECTOR (fiscal.ts) maps a building kind to
 * a WageSector (primary/secondary/tertiary/public) — a DIFFERENT taxonomy
 * from trip_generation.json's freightTonnesPerJobPerDay sectors
 * (construction/manufacturing/food/logistics_retail/services_office). The
 * doc's AC-3 text ("cite the exact mapping KIND_TO_WAGE_SECTOR uses") does
 * not resolve cleanly onto trip_generation.json's own taxonomy, so this
 * module defines its OWN kind->freight-sector map, following the doc's one
 * concrete literal example (industrial -> manufacturing) and reasoned
 * placements for the rest; everything not obviously bulk-freight-generating
 * falls to services_office (0.02 t/job/day — negligible bulk freight, matching
 * that sector's own row comment).
 */
export const KIND_TO_FREIGHT_SECTOR: Readonly<Record<string, string>> = Object.freeze({
  mine: 'construction',
  industrial: 'manufacturing',
  commercial: 'logistics_retail',
  office: 'services_office',
  transport: 'logistics_retail',
  station: 'logistics_retail',
  school: 'services_office',
  health: 'services_office',
  police: 'services_office',
  fire: 'services_office',
  civic: 'services_office',
  power: 'services_office',
  water: 'services_office',
  pylon: 'services_office',
  road: 'services_office',
  landmark: 'services_office',
});

/** Road-going freight vehicle ids in scope for the blended capacity figure
 * (AC-3 — freight_train is rail, out of scope for a ROAD vehicle-trip count). */
const ROAD_FREIGHT_VEHICLE_IDS: readonly string[] = ['cargo_van', 'rigid_truck', 'articulated_truck'];

/** capacityTonnes per road freight vehicle id, validated once at module-load
 * time (fail-loud, GR#7) — mirrors scaleLadderData.ts's "validate at import
 * time, never a silent partial load" idiom. */
function loadVehicleCapacities(): Readonly<Record<string, number>> {
  const out: Record<string, number> = {};
  for (const v of vehicleClasses.roadVehicles) {
    if (!ROAD_FREIGHT_VEHICLE_IDS.includes(v.id)) continue;
    if (typeof v.capacityTonnes !== 'number') {
      throw registryError(
        ERR_VEHICLE_CLASS_MISSING,
        `vehicle_classes.json roadVehicles "${v.id}" has no numeric capacityTonnes`,
      );
    }
    out[v.id] = v.capacityTonnes;
  }
  for (const id of ROAD_FREIGHT_VEHICLE_IDS) {
    if (!(id in out)) {
      throw registryError(
        ERR_VEHICLE_CLASS_MISSING,
        `vehicle_classes.json roadVehicles is missing required freight class "${id}"`,
      );
    }
  }
  return Object.freeze(out);
}
const VEHICLE_CAPACITY_TONNES = loadVehicleCapacities();

// --- data/traffic.json: maxAttributionRadiusTiles (BUG-847, GR#15) ---------

interface TrafficConfigTable {
  maxAttributionRadiusTiles?: unknown;
}
const trafficConfig = rawTrafficConfig as unknown as TrafficConfigTable;

/** BUG-847: bounds nearestSegmentWeights's BFS radius so cost is a function
 * of the MAP (MAP_W x MAP_H) and this figure, never of the city's own
 * occupied bounding-box diameter — validated once at module-load time,
 * fail-loud (GR#7), never a silent `undefined` -> NaN radius downstream. */
function loadMaxAttributionRadiusTiles(): number {
  const v = trafficConfig.maxAttributionRadiusTiles;
  if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
    throw registryError(
      ERR_TRAFFIC_CONFIG_MISSING,
      `data/traffic.json maxAttributionRadiusTiles must be a positive finite number, got ${JSON.stringify(v)}`,
    );
  }
  return v;
}
const MAX_ATTRIBUTION_RADIUS_TILES = loadMaxAttributionRadiusTiles();

// --- Scale-ladder access -----------------------------------------------------

/**
 * AC-2/§3: the city's current ladder point, ONE call per tick (not per
 * tile — GR#21/O(rungs) cost). Below the ladder's first rung (population 0
 * at genesis, or any population under the lowest rung), use the FIRST rung
 * rather than throw — a brand-new city must still forecast demand.
 * ABOVE the last rung (98,000,000) is NEVER clamped: ladderAt's own
 * ERR_OUT_OF_RANGE (MET-V899) surfaces unmodified, per the task brief's
 * explicit "NEVER extrapolate above 98M" instruction.
 *
 * BUG-850 rework: a non-finite (NaN/Infinity/-Infinity) `s.population`
 * previously produced a SILENT NaN ladder rung (Math.max(NaN, floor) is
 * NaN, and ladderAt's own range comparisons are both false for NaN, so
 * neither branch throws) — every downstream demand number then became NaN
 * with no error anywhere. Fail loud instead: a non-finite population is a
 * registry error, never a NaN rung. A negative-but-finite population still
 * clamps up to the floor rung (defensible, matches the documented
 * below-floor clamp — BUG-850 confirmed -5 clamping to rung 100 is fine).
 */
export const ladderPointOf: (s: SimState) => LadderPoint = memoOnState((s) => {
  if (!Number.isFinite(s.population)) {
    throw registryError(ERR_POPULATION_INVALID, `population ${s.population} is not finite`);
  }
  const minPopulation = scaleLadder.rungs[0].population;
  const population = Math.max(s.population, minPopulation);
  return ladderAt(scaleLadder, population);
});

function numericField(point: LadderPoint, key: string): number {
  const f = point.fields.find((x) => x.key === key);
  if (!f) {
    throw registryError(
      ERR_LADDER_FIELD_MISSING,
      `scale ladder point at population ${point.population} is missing required numeric field "${key}"`,
    );
  }
  return f.value;
}

function numericFieldOrZero(point: LadderPoint, key: string): number {
  const f = point.fields.find((x) => x.key === key);
  return f ? f.value : 0;
}

/** AC-2 — typed accessor filtering a ladder point's flattened fields down to
 * its modeShare.<modeId> leaves, keyed by mode id. The ONLY path to a mode
 * share in this module — the inc0 density-band table is never imported here,
 * grep-checked by the test suite (AC-2's own Check). */
export function modeShareOf(point: LadderPoint): Record<string, number> {
  const out: Record<string, number> = {};
  const prefix = 'modeShare.';
  for (const f of point.fields) {
    if (f.key.startsWith(prefix)) out[f.key.slice(prefix.length)] = f.value;
  }
  return out;
}

/** AC-3 — blended weighted-average road-freight-vehicle capacity (tonnes),
 * weighted by the CITY's current rung's freightTonnesByVehicleClass shares
 * (itself trip_generation.json-rollup-derived — see scale_ladder.json's
 * provenance block) — no literal weighting hand-typed in TS (GR#15). */
function blendedFreightVehicleCapacity(point: LadderPoint): number {
  let weightedSum = 0;
  let totalShare = 0;
  for (const id of ROAD_FREIGHT_VEHICLE_IDS) {
    const share = numericFieldOrZero(point, `freightTonnesByVehicleClass.${id}`);
    weightedSum += share * VEHICLE_CAPACITY_TONNES[id];
    totalShare += share;
  }
  // Fall back to the rigid_truck class (the mid-weight road freight class)
  // when a rung reports zero freight of every road vehicle class (e.g. the
  // lowest rungs) — division-by-zero guard, never a fabricated non-zero share.
  return totalShare > 0 ? weightedSum / totalShare : VEHICLE_CAPACITY_TONNES['rigid_truck'];
}

// --- AC-1: per-tile actual residents/workers -> person-trips + freight -----

export interface TileDemand {
  x: number;
  y: number;
  spec: string;
  /** Actual (occupancy-scaled) residents on this tile — never raw capacity. */
  residentsActual: number;
  /** Actual (occupancy-scaled) workers on this tile — never raw capacity. */
  workersActual: number;
  /** Person-trips/day generated by this tile (resident all-purpose + worker commute). */
  personTrips: number;
  freightTonnesPerDay: number;
  freightVehicleTrips: number;
}

/**
 * demandForecastOf (AC-1/AC-3) — per demand-generating tile (an online
 * building with `residents` or `jobs`), the ACTUAL person-trip and freight
 * figures. "Actual" because SPECS[...].residents/.jobs are CAPACITY figures
 * (capacityAtTier) — each tile's capacity is scaled by a city-wide occupancy
 * fraction to get an actual count, never raw capacity (AC-1's false-pass
 * note: a sub-100%-occupancy fixture is required to catch a ×1 mutant).
 *
 * D2/AC-1 (BUG-849 rework, 2026-09-09): workersActual's occupancy fraction
 * was ORIGINALLY totalJobs(s)/totalJobs(s) — the same call as both numerator
 * and denominator, identically 1 for every state, a vacuous multiply the
 * independent round proved equivalent-mutant to a bare `x 1` (BUG-849). A
 * real filled-vs-capacity basis DOES exist in this file's own module
 * (data.ts's filledJobsBySector/filledJobsFromCapacityAndPopulation, the
 * F1 money-path fix: filled = round(clamp(min(population *
 * WORKING_AGE_FRACTION, totalCapacity), 0, totalCapacity)) — GR#3, the SAME
 * basis the wage bill already uses, not a second working-age model), so this
 * now reads workerOccupancy = (sum of filledJobsBySector(s)'s four sector
 * fields) / totalJobs(s) — a real ratio that is < 1 whenever the working-age
 * population is below job capacity, and clamps to 1 (never above — jobs
 * cannot be MORE than filled) once population growth outstrips capacity.
 * D2's "no external-commuter deduction" simplification still holds (workers
 * are still assumed city-resident, never MOD-035-split) — only the
 * numerator changed, from a vacuous self-ratio to the real filled-jobs
 * figure the fiscal side already computes.
 *
 * Cost: O(tiles), one ladder call + one filledJobsBySector call (already
 * memoOnState, AC-9) — never walks a per-citizen array.
 */
export const demandForecastOf: (s: SimState) => TileDemand[] = memoOnState((s) => {
  const point = ladderPointOf(s);
  const tripRate = numericField(point, 'tripRatePersonPerDay');

  const residentsCapTotal = onlineResidentsCapacity(s);
  const jobsCapTotal = totalJobs(s);
  const residentOccupancy = residentsCapTotal > 0 ? s.population / residentsCapTotal : 0;
  // BUG-849 rework: real filled/capacity ratio (see doc comment above), not
  // the old totalJobs(s)/totalJobs(s) vacuous self-ratio.
  const filled = filledJobsBySector(s);
  const filledJobsTotal = filled.primary + filled.secondary + filled.tertiary + filled.public;
  const workerOccupancy = jobsCapTotal > 0 ? filledJobsTotal / jobsCapTotal : 0;

  const blendedCapacity = blendedFreightVehicleCapacity(point);

  const out: TileDemand[] = [];
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const hasResidents = sp.residents != null;
    const hasJobs = sp.jobs != null;
    if (!hasResidents && !hasJobs) continue;

    const cap = capacityAtTier(sp, b.capacityTier ?? 0);
    const residentsActual = hasResidents ? cap * residentOccupancy : 0;
    const workersActual = hasJobs ? cap * workerOccupancy : 0;
    const personTrips = residentsActual * tripRate + workersActual * COMMUTE_LEGS_PER_WORKER_PER_DAY;

    // BUG-850 rework: demandForecastOf is a memoOnState derivation called
    // during RENDER (not a load-time/startup path) — an unmapped kind or a
    // missing trip_generation.json sector row used to THROW from inside this
    // loop, crashing the render the first time a new job-bearing kind is
    // added to SPECS without a KIND_TO_FREIGHT_SECTOR entry. Neither gap is
    // reachable today (KIND_TO_FREIGHT_SECTOR is attacker-verified total
    // over every job-bearing kind, trip_generation.json's sectors are
    // module-load-validated below), but the FIRST time either drifts this
    // must degrade honestly — zero freight for that tile plus a one-shot
    // registry log (never per-tile-per-tick spam) — not crash the screen.
    let freightTonnesPerDay = 0;
    if (hasJobs && workersActual > 0) {
      const sector = KIND_TO_FREIGHT_SECTOR[sp.kind];
      const rate = sector ? tripGeneration.freightTonnesPerJobPerDay[sector]?.tonnesPerJobPerDay : undefined;
      if (!sector) {
        logSectorGapOnce(sp.kind, `building kind "${sp.kind}" (spec ${sp.id}) has no freight-sector mapping in KIND_TO_FREIGHT_SECTOR`);
      } else if (typeof rate !== 'number') {
        logSectorGapOnce(sector, `trip_generation.json freightTonnesPerJobPerDay is missing sector "${sector}"`);
      } else {
        freightTonnesPerDay = workersActual * rate;
      }
    }
    const freightVehicleTrips = blendedCapacity > 0 ? freightTonnesPerDay / blendedCapacity : 0;

    if (personTrips === 0 && freightTonnesPerDay === 0) continue;
    out.push({
      x: b.x,
      y: b.y,
      spec: b.spec,
      residentsActual,
      workersActual,
      personTrips,
      freightTonnesPerDay,
      freightVehicleTrips,
    });
  }
  // Deterministic order (GR#21): sorted by (x,y,spec), no map-range-with-break.
  out.sort((a, b) => a.x - b.x || a.y - b.y || (a.spec < b.spec ? -1 : a.spec > b.spec ? 1 : 0));
  return out;
});

// --- AC-4: per-line-class demand, PARALLEL to lineUsageOf (D1) -------------

export interface ForecastLineUsage {
  /** Forecast demand for this class, independently derived from tile demand. */
  demand: number;
  /** lineUsageOf's existing usage figure for the SAME spec — comparison target ONLY. */
  legacyUsage: number;
  /** |demand - legacyUsage| / max(1, legacyUsage) — the D1 divergence indicator. */
  divergenceRatio: number;
}

/** Person-trip mode ids that use the ROAD network (GR#15: read from the
 * ladder's own modeShare keys, this list just selects WHICH keys are
 * road-using — walk/bicycle are non-vehicular and excluded). */
const ROAD_PERSON_MODE_IDS: readonly string[] = ['car', 'motorbike', 'taxi', 'bus'];

/**
 * forecastLineUsage (AC-4, D1) — per-line-class demand computed INDEPENDENTLY
 * from tile demand (never reads lineUsageOf(s)'s own `.usage` as an input —
 * only as the `legacyUsage` comparison target, per the doc's anti-circularity
 * note). Road classes split the combined road-using person-trip + freight-
 * vehicle-trip demand by CAPACITY SHARE across present road-tier specs — the
 * SAME apportionment idiom lineUsageOf's own road split already uses (GR#3:
 * one apportionment rule, applied to a different total). rail/hs1 read the
 * ladder's own heavy_rail/hs_rail mode-share fractions of total person-trips.
 */
export const forecastLineUsage: (s: SimState) => Map<string, ForecastLineUsage> = memoOnState((s) => {
  const point = ladderPointOf(s);
  const shares = modeShareOf(point);
  const demandTiles = demandForecastOf(s);

  let totalPersonTrips = 0;
  let totalFreightVehicleTrips = 0;
  for (const t of demandTiles) {
    totalPersonTrips += t.personTrips;
    totalFreightVehicleTrips += t.freightVehicleTrips;
  }

  let roadPersonDemand = 0;
  for (const id of ROAD_PERSON_MODE_IDS) roadPersonDemand += totalPersonTrips * (shares[id] ?? 0);
  const railDemand = totalPersonTrips * (shares['heavy_rail'] ?? 0);
  const hsDemand = totalPersonTrips * (shares['hs_rail'] ?? 0);
  const totalRoadDemand = roadPersonDemand + totalFreightVehicleTrips;

  const legacy = new Map<string, LineUsage>();
  for (const u of lineUsageOf(s)) legacy.set(u.spec, u);

  let totalDrivableCap = 0;
  for (const u of legacy.values()) if (u.kind === 'road') totalDrivableCap += u.capacity;

  const out = new Map<string, ForecastLineUsage>();
  for (const [spec, u] of legacy) {
    let demand: number;
    if (u.kind === 'road') {
      demand = totalDrivableCap > 0 ? (totalRoadDemand * u.capacity) / totalDrivableCap : 0;
    } else if (spec === 'hs1') {
      demand = hsDemand;
    } else if (spec === 'rail') {
      demand = railDemand;
    } else {
      demand = 0;
    }
    const divergenceRatio = Math.abs(demand - u.usage) / Math.max(1, u.usage);
    out.set(spec, { demand, legacyUsage: u.usage, divergenceRatio });
  }
  return out;
});

// --- AC-5/AC-6: per-segment demand, nearest-segment attribution ------------

export interface ForecastSegmentUsage {
  segmentId: string;
  spec: string;
  demand: number;
}

function accumulate(m: Map<string, number>, key: string, v: number): void {
  m.set(key, (m.get(key) ?? 0) + v);
}

// BUG-847 test-only instrumentation: counts every neighbour-candidate
// examined by nearestSegmentWeights across whatever BFS work is actually
// executed (memoOnState cache hits do zero further work and are NOT
// counted again). Reset before a measurement to avoid cross-call
// accumulation. Cost is a single integer increment per candidate already
// being visited — negligible relative to the BFS work itself, always on
// (not gated behind a debug flag) so a scale/sparse regression is provable
// in the shipped module, not just an attacker's patched copy.
let bfsOpCounter = 0;
export function __resetBfsOpCounterForTest(): void {
  bfsOpCounter = 0;
}
export function __getBfsOpCounterForTest(): number {
  return bfsOpCounter;
}

/**
 * BUG-864(1) test-only instrumentation: counts SEED keys dropped by
 * `boundedNearestSourceMapOf` because they resolved outside
 * [0,MAP_W) x [0,MAP_H) — the SAME bounds check the neighbour-expansion loop
 * already applied, now also applied at seeding time (the pre-fix code
 * admitted a seed key verbatim into `visited` with no bounds check at all;
 * not production-reachable today since grid.ts clamps every placement, but
 * the same defect CLASS the expansion-loop bound was added to close). Always
 * on, like `bfsOpCounter`, never gated behind a debug flag.
 */
let offMapSeedsDroppedCounter = 0;
export function __resetOffMapSeedsDroppedCounterForTest(): void {
  offMapSeedsDroppedCounter = 0;
}
export function __getOffMapSeedsDroppedCounterForTest(): number {
  return offMapSeedsDroppedCounter;
}

/**
 * Multi-source Manhattan BFS from `sourceTileKeys` (one line class's own
 * tiles) outward over the map, bounded by BOTH `radius` and the map's own
 * extent (MAP_W x MAP_H — BUG-847: the pre-fix version had no bounds check
 * at all and flooded the empty off-map plane in every direction). Returns,
 * per segmentId, the sum of `tileWeight` for every demand tile whose
 * NEAREST tile of this source set resolves (via `tileToSegment`) to that
 * segment — ties at equal distance broken by sorted "x,y" source key
 * (GR#21, no map-range-with-break: every layer's frontier is processed in
 * fully sorted order, first assignment wins, never re-visited). Also
 * returns `attributedTileCount`, the number of DISTINCT weighted demand
 * tiles this BFS actually reached (BUG-847: lets the caller compute an
 * honest unattributed count/weight rather than silently dropping demand
 * beyond the radius or map edge).
 *
 * BUG-847 fix shape: `radius` (passed in) is now `Math.min(<city bbox
 * diameter>, MAX_ATTRIBUTION_RADIUS_TILES)` — the data-sourced cap (GR#15,
 * data/traffic.json) means cost is bounded independent of how far a single
 * outpost building sits from the rest of the city, and the per-step
 * neighbour bounds check below means cost is bounded independent of the
 * radius exceeding the map's own extent. Combined: worst case is
 * O(min(radius, MAP_W+MAP_H)^2 + segments), never O(bbox diameter^2).
 *
 * Design note (per the rework brief): kept as ONE BFS PER LINE CLASS rather
 * than a single combined BFS over all classes with per-class nearest,
 * because (a) each class's own segment tiles are a DIFFERENT source set, so
 * a combined BFS would need a per-tile array of "nearest source per class"
 * anyway — same total work, more state; (b) the radius cap already bounds
 * per-class cost to the map size regardless of class count, so the
 * per-class approach's total cost (O(classes x mapTiles) worst case) does
 * NOT grow with city bbox diameter, which is what BUG-847 actually measured
 * as unshippable — the class-count factor is small and constant (11 line
 * classes) where the diameter factor was unbounded. A combined pass remains
 * a legitimate future optimisation if the per-class factor ever matters.
 *
 * BUG-866 fix: this function is the sibling `boundedNearestSourceMapOf`
 * (below) was copied FROM — BUG-864(1) added a seed bounds-check to the new
 * export but left this, the original, still seeding `visited` from
 * `sortedSources` with no bounds check at all. The identical guard is
 * applied here: an off-map seed key is dropped (never entered into
 * `visited`), counted on both the shared test counter (parity with
 * `boundedNearestSourceMapOf`) and the function's own `offMapSeedsDropped`
 * return field (additive — every existing caller/consumer of the return
 * shape is unaffected since it only adds a field, never removes one).
 * Exported additively (was module-private) so BUG-866's test can drive an
 * off-map seed directly rather than only through real building placement,
 * which grid.ts always clamps on-map today.
 */
export function nearestSegmentWeights(
  sourceTileKeys: string[],
  tileToSegment: Map<string, string>,
  tileWeight: Map<string, number>,
  radius: number,
): { weightBySegment: Map<string, number>; attributedTileCount: number; offMapSeedsDropped: number } {
  const weightBySegment = new Map<string, number>();
  if (sourceTileKeys.length === 0) return { weightBySegment, attributedTileCount: 0, offMapSeedsDropped: 0 };

  // `radius` is capped by MAX_ATTRIBUTION_RADIUS_TILES by the ONLY caller
  // (forecastSegmentUsage, below) before this private helper ever runs — no
  // second cap re-applied here (GR#3, one rule, one place).
  const visited = new Map<string, string>(); // tileKey -> nearest source tileKey
  const sortedSources = [...sourceTileKeys].sort();
  // BUG-866/BUG-864(1): bounds-check SEED keys exactly like expanded
  // neighbours below — the pre-fix loop admitted a seed key verbatim into
  // `visited` with no bounds check at all.
  let offMapSeedsDropped = 0;
  const onMapSources: string[] = [];
  for (const k of sortedSources) {
    const comma = k.indexOf(',');
    const x = Number(k.slice(0, comma));
    const y = Number(k.slice(comma + 1));
    if (x < 0 || x >= MAP_W || y < 0 || y >= MAP_H) {
      offMapSeedsDropped++;
      offMapSeedsDroppedCounter++;
      continue;
    }
    onMapSources.push(k);
    visited.set(k, k);
  }
  let attributedTileCount = 0;
  for (const k of onMapSources) {
    const w = tileWeight.get(k);
    if (w) {
      accumulate(weightBySegment, tileToSegment.get(k)!, w);
      attributedTileCount++;
    }
  }

  let frontier = onMapSources;
  let dist = 0;
  while (frontier.length > 0 && dist < radius) {
    dist++;
    const next = new Map<string, string>(); // candidate tileKey -> best source seen this layer
    for (const key of frontier) {
      const comma = key.indexOf(',');
      const x = Number(key.slice(0, comma));
      const y = Number(key.slice(comma + 1));
      const src = visited.get(key)!;
      const neighbours: Array<[number, number]> = [
        [x + 1, y],
        [x - 1, y],
        [x, y + 1],
        [x, y - 1],
      ];
      for (const [nx, ny] of neighbours) {
        bfsOpCounter++;
        // BUG-847: never step off-map — the pre-fix version had no such
        // check and flooded the empty off-map plane to `radius` in every
        // direction, once per line class.
        if (nx < 0 || nx >= MAP_W || ny < 0 || ny >= MAP_H) continue;
        const nk = `${nx},${ny}`;
        if (visited.has(nk)) continue;
        const existing = next.get(nk);
        if (!existing || src < existing) next.set(nk, src);
      }
    }
    const nextFrontier: string[] = [];
    for (const nk of [...next.keys()].sort()) {
      const src = next.get(nk)!;
      visited.set(nk, src);
      nextFrontier.push(nk);
      const w = tileWeight.get(nk);
      if (w) {
        accumulate(weightBySegment, tileToSegment.get(src)!, w);
        attributedTileCount++;
      }
    }
    frontier = nextFrontier;
  }
  return { weightBySegment, attributedTileCount, offMapSeedsDropped };
}

/**
 * GR#3/BUG-857 additive export: the SAME bounded multi-source 4-neighbour
 * Manhattan BFS shape as `nearestSegmentWeights` above (identical bounds
 * check, identical sorted-source/sorted-frontier tie-break, identical op
 * counter), but returning the raw `visited` map (tileKey -> nearest source
 * tileKey) instead of a per-segment weight sum — the shape
 * trafficAssignment.ts's nearest-road-segment lookup needs. Added as a
 * SEPARATE function rather than refactoring `nearestSegmentWeights` to call
 * it, because that function's own suite pins its exact source text
 * (structural grep for the bounds-check/sort lines inside
 * `function nearestSegmentWeights(...)` specifically) — an internal
 * refactor would falsely red those structural pins without changing
 * behaviour, so this is a pure ADDITIVE export (GR#3 duplication of BFS
 * MECHANICS is accepted here in exchange for zero risk to the sibling
 * lane's landed, pinned suite; a follow-up could unify both once that
 * suite's structural pins are updated to match).
 *
 * BUG-864(2) cost-shape note: this is already ONE multi-source BFS over the
 * UNION of every source tile passed in (trafficAssignment.ts's own caller
 * passes every road tile city-wide in a single call — never per-cluster,
 * never per-class here). The r2 finding that a 46-building SPARSE
 * corner-scattered fixture (734,688 ops @ radius 250) costs MORE than a
 * 25,600-tile DENSE grid fixture (537,744 ops @ radius 250) is not a
 * per-cluster-BFS defect: a dense grid's sources are already mutually
 * adjacent, so neighbour expansion mostly hits `visited.has` and stops
 * almost immediately, while scattered sources each pay for flooding their
 * own empty surrounding area up to the radius cap. The real, PROVABLE
 * structural bound is independent of source layout: every tile key can enter
 * `frontier` (and therefore get its 4 neighbours examined) AT MOST ONCE per
 * call, because `visited.has(nk)` permanently excludes it from every later
 * layer — so total neighbour-examination ops for one call are bounded by
 * `4 * MAP_W * MAP_H` regardless of how sparse or dense, clustered or
 * scattered, the source set is (empirically confirmed: a radius-400 sparse
 * run measured 918,576 ops against `4 * 624 * 368 = 918,528` — within
 * rounding of the theoretical ceiling). "Sparse must not exceed dense" was
 * the r1/r2 brief's own informal proxy for "cost is bounded"; the map-area
 * bound above is the actual, layout-independent guarantee and is what the
 * structural test below pins.
 */
export function boundedNearestSourceMapOf(sourceTileKeys: string[], radius: number): Map<string, string> {
  const visited = new Map<string, string>(); // tileKey -> nearest source tileKey
  const sortedSources = [...sourceTileKeys].sort();
  // BUG-864(1): bounds-check SEED keys exactly like expanded neighbours below
  // — an off-map seed is dropped and counted, never silently entered into
  // `visited` (the pre-fix version admitted seed keys verbatim, unlike the
  // neighbour-expansion loop, which already had this check).
  for (const k of sortedSources) {
    const comma = k.indexOf(',');
    const x = Number(k.slice(0, comma));
    const y = Number(k.slice(comma + 1));
    if (x < 0 || x >= MAP_W || y < 0 || y >= MAP_H) {
      offMapSeedsDroppedCounter++;
      continue;
    }
    visited.set(k, k);
  }
  const onMapSources = sortedSources.filter((k) => visited.has(k));

  let frontier = onMapSources;
  let dist = 0;
  while (frontier.length > 0 && dist < radius) {
    dist++;
    const next = new Map<string, string>(); // candidate tileKey -> best source seen this layer
    for (const key of frontier) {
      const comma = key.indexOf(',');
      const x = Number(key.slice(0, comma));
      const y = Number(key.slice(comma + 1));
      const src = visited.get(key)!;
      const neighbours: Array<[number, number]> = [
        [x + 1, y],
        [x - 1, y],
        [x, y + 1],
        [x, y - 1],
      ];
      for (const [nx, ny] of neighbours) {
        bfsOpCounter++;
        // BUG-847/BUG-857: never step off-map — an unbounded version floods
        // the empty off-map plane to `radius` in every direction.
        if (nx < 0 || nx >= MAP_W || ny < 0 || ny >= MAP_H) continue;
        const nk = `${nx},${ny}`;
        if (visited.has(nk)) continue;
        const existing = next.get(nk);
        if (!existing || src < existing) next.set(nk, src);
      }
    }
    const nextFrontier: string[] = [];
    for (const nk of [...next.keys()].sort()) {
      const src = next.get(nk)!;
      visited.set(nk, src);
      nextFrontier.push(nk);
    }
    frontier = nextFrontier;
  }
  return visited;
}

/**
 * forecastSegmentUsage (AC-5/AC-6) — each in-scope class's forecast demand
 * (forecastLineUsage) redistributed across that class's OWN segments
 * (lineSegmentIndexOf) by NEAREST-SEGMENT tile-demand attribution, not
 * capacity share (the actual fix for Q100163: two same-class segments now
 * legitimately differ when the demand generating them is spatially
 * uneven). "Weight" per demand tile is its total person+freight trips —
 * sound because the ladder's mode-share fractions are CITY-WIDE constants
 * (uniform per rung), so scaling every tile's weight by the same fraction
 * before nearest-neighbour attribution would produce IDENTICAL relative
 * segment proportions; using the unscaled total trip weight is therefore
 * equivalent and avoids a redundant per-mode BFS pass per class.
 *
 * Search radius (AC-5 "bounded search radius, not a hardcoded constant") is
 * the city's own occupied bounding-box Manhattan diameter — derived from
 * s.buildings, never a literal.
 *
 * Conservation (AC-6): floor-per-segment by weight share, remainder on the
 * LAST segment in sorted segmentId order — the exact stationUtilisationOf
 * idiom (data.ts ~3521). When a class has zero reachable demand weight
 * (honest: no demand tile within radius of any of its tiles), falls back to
 * the existing capacity-share split so the sum-equality invariant still
 * holds exactly even in that degenerate case.
 *
 * BUG-847: `radius` is now capped by MAX_ATTRIBUTION_RADIUS_TILES
 * (data/traffic.json, GR#15) in addition to the bbox diameter, and
 * nearestSegmentWeights never steps off the map — see that function's own
 * doc comment. A demand tile beyond a class's capped radius/map edge is
 * reported as UNATTRIBUTED for that class (see forecastUnattributedOf
 * below) rather than being silently dropped: attributedWeight +
 * unattributedWeight == the city's total demand-tile weight, exactly, for
 * every class (never affects THIS function's own AC-6 sum-to-class-demand
 * invariant, which is unconditional on the remainder-on-last-segment rule
 * regardless of how much weight was actually reachable).
 */
export const forecastSegmentUsage: (s: SimState) => Map<string, ForecastSegmentUsage> = memoOnState((s) => {
  const segIndex = lineSegmentIndexOf(s);
  const classDemand = forecastLineUsage(s);
  const demandTiles = demandForecastOf(s);

  const tileWeight = new Map<string, number>();
  for (const t of demandTiles) {
    accumulate(tileWeight, `${t.x},${t.y}`, t.personTrips + t.freightVehicleTrips);
  }
  let totalDemandWeight = 0;
  for (const w of tileWeight.values()) totalDemandWeight += w;
  const totalDemandTileCount = tileWeight.size;

  let minX = Infinity;
  let maxX = -Infinity;
  let minY = Infinity;
  let maxY = -Infinity;
  for (const b of s.buildings) {
    if (b.x < minX) minX = b.x;
    if (b.x > maxX) maxX = b.x;
    if (b.y < minY) minY = b.y;
    if (b.y > maxY) maxY = b.y;
  }
  const bboxRadius = Number.isFinite(minX) ? maxX - minX + (maxY - minY) : 0;
  // BUG-847: capped by the data-sourced max, never by the bbox diameter
  // alone -- see nearestSegmentWeights's own doc comment.
  const radius = Math.min(bboxRadius, MAX_ATTRIBUTION_RADIUS_TILES);

  const segsBySpec = new Map<string, typeof segIndex.segments>();
  for (const seg of segIndex.segments) {
    let arr = segsBySpec.get(seg.spec);
    if (!arr) {
      arr = [];
      segsBySpec.set(seg.spec, arr);
    }
    arr.push(seg);
  }

  const result = new Map<string, ForecastSegmentUsage>();
  const unattributed = new Map<string, UnattributedDemand>();
  for (const [spec, segs] of segsBySpec) {
    const cls = classDemand.get(spec);
    if (!cls) continue;

    const specTileKeys: string[] = [];
    for (const [tileKey, segId] of segIndex.tileToSegment) {
      if (segId.startsWith(`${spec}:`)) specTileKeys.push(tileKey);
    }
    const { weightBySegment, attributedTileCount } = nearestSegmentWeights(
      specTileKeys,
      segIndex.tileToSegment,
      tileWeight,
      radius,
    );
    let totalWeight = 0;
    for (const w of weightBySegment.values()) totalWeight += w;

    // BUG-847: honest unattributed accounting -- attributedWeight +
    // unattributedWeight == totalDemandWeight exactly (arithmetic residual,
    // never re-derived by a second walk).
    const unattributedWeight = totalDemandWeight - totalWeight;
    const unattributedTileCount = totalDemandTileCount - attributedTileCount;
    unattributed.set(spec, {
      tileCount: Math.max(0, unattributedTileCount),
      weight: Math.max(0, unattributedWeight),
      demand: totalDemandWeight > 0 ? (cls.demand * Math.max(0, unattributedWeight)) / totalDemandWeight : 0,
    });

    let totalCapacity = 0;
    for (const seg of segs) totalCapacity += seg.capacity;

    const sortedSegs = [...segs].sort((a, b) => (a.segmentId < b.segmentId ? -1 : a.segmentId > b.segmentId ? 1 : 0));
    let allocated = 0;
    for (let i = 0; i < sortedSegs.length; i++) {
      const seg = sortedSegs[i];
      const isLast = i === sortedSegs.length - 1;
      let demand: number;
      if (isLast) {
        demand = cls.demand - allocated;
      } else if (totalWeight > 0) {
        const w = weightBySegment.get(seg.segmentId) ?? 0;
        demand = Math.floor((cls.demand * w) / totalWeight);
      } else {
        demand = totalCapacity > 0 ? Math.floor((cls.demand * seg.capacity) / totalCapacity) : 0;
      }
      allocated += demand;
      result.set(seg.segmentId, { segmentId: seg.segmentId, spec, demand });
    }
  }
  unattributedByState.set(s, unattributed);
  return result;
});

/** BUG-847: per-class honest-absence diagnostic — how much of the city's
 * demand-tile weight/tile-count a class's nearestSegmentWeights BFS could
 * NOT reach (beyond MAX_ATTRIBUTION_RADIUS_TILES or the map edge), plus the
 * corresponding slice of that class's forecast `demand` (diagnostic only,
 * never subtracted from the segment split -- AC-6's sum-to-class-demand
 * invariant is unconditional, see forecastSegmentUsage's own comment). */
export interface UnattributedDemand {
  tileCount: number;
  weight: number;
  demand: number;
}

const unattributedByState = new WeakMap<SimState, Map<string, UnattributedDemand>>();

/** Reads the unattributed-demand side table forecastSegmentUsage(s)
 * populates — calling forecastSegmentUsage(s) first (memoOnState, so a
 * repeat call is a cache hit, not a second BFS pass) guarantees the table
 * is populated before this reads it. */
export function forecastUnattributedOf(s: SimState): Map<string, UnattributedDemand> {
  forecastSegmentUsage(s);
  return unattributedByState.get(s) ?? new Map();
}
