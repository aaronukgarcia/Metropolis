// FEAT-2326609796 inc3 "ASSIGNMENT + CONGESTION" — docs/planning/acceptance/FEAT-2326609792-inc3.md
// (AC-1..AC-9, §4 D1-D3, ASM-1501..1504, lead amendment overriding AC-2's
// tile size to the webconsole's own 50m grid).
//
// This module routes inc2's (trafficDemand.ts) per-tile person/freight demand
// over the real line-segment graph (lineSegmentIndexOf, data.ts) to the
// nearest job-bearing destination, loads every traversed segment, and applies
// a single-pass BPR volume-delay curve to get a v/c ratio, a travel-time
// penalty, a commute-time distribution, and a segment-granularity gridlock
// signal. AC-8 fiscal boundary: this module is a read-only diagnostic layer
// that never touches ANY currency-shaped SimState field or the class-level
// congestion/income coupling those other modules own; it is not wired to
// any consumer yet (inc4/inc5's job, §6 out of scope).
//
// PURE + DETERMINISTIC (GR#21): every exported derivation is memoOnState over
// SimState (+ a plain prevTicks record for gridlockedSegmentsOf, mirroring
// the class-level congestion tracker's own non-memoised shape) — no
// wall-clock read, no PRNG, no browser-storage read. Iteration is always
// over pre-sorted keys (never a bare Map-range-with-break).

import type { SimState } from './types.ts';
import {
  SPECS,
  isOnline,
  lineSegmentIndexOf,
  memoOnState,
  CONGESTION_CONSTANTS,
  sanitizeRoadWearBySegment,
  ROAD_CLASS_ID_OF_TIER,
  type LineSegment,
} from './data.ts';
import {
  demandForecastOf,
  ladderPointOf,
  modeShareOf,
  nearestSourceForTiles,
  freightVehicleTripsByClassOf,
  type VehicleClassId,
} from './trafficDemand.ts';
export type { VehicleClassId } from './trafficDemand.ts';

// data files this module reads (GR#15 — every constant below is sourced,
// never hand-typed). This module owns ONLY the webconsoleMetresPerTile field
// inside traffic.json and the BUG-843 motorway beta-override removal inside
// link_capacity.json — every other field here is read-only.
import rawTraffic from './traffic-data/traffic.json' with { type: 'json' };
import rawLinkCapacity from './traffic-data/link_capacity.json' with { type: 'json' };
import rawRoads from './traffic-data/roads.json' with { type: 'json' };
import rawVehicleClasses from './traffic-data/vehicle_classes.json' with { type: 'json' };
// FEAT-2326609800 inc7 (AC-2/AC-3/AC-4): fuel duty rate, VED fleet-average
// rates, and ESAL wear/repair-cost curve — all read-only from this module's
// point of view (owned by data/fuel.json / data/traffic/taxation.json /
// data/traffic/road_wear.json respectively).
import rawFuel from './traffic-data/fuel.json' with { type: 'json' };
import rawTaxation from './traffic-data/taxation.json' with { type: 'json' };
import rawRoadWear from './traffic-data/road_wear.json' with { type: 'json' };
import rawTripGeneration from './traffic-data/trip_generation.json' with { type: 'json' };

// --- Registry error codes (GR#7) --------------------------------------------
// Claimed via `node tools/plan/add-error.js claim-range ui.webconsole --size 6`
// (V906-V911), added V906-V910 (data/errors.json); V911 stays reserved.
export const ERR_ROAD_CLASS_UNMAPPED = 'MET-V906'; // TrafficAssignmentRoadClassUnmapped
export const ERR_SPEED_LIMIT_MISSING = 'MET-V907'; // TrafficAssignmentSpeedLimitMissing
export const ERR_LINK_CAPACITY_MISSING = 'MET-V908'; // TrafficAssignmentLinkCapacityMissing
export const ERR_METRES_PER_TILE_MISSING = 'MET-V909'; // TrafficAssignmentMetresPerTileMissing
export const ERR_BPR_PARAM_INVALID = 'MET-V910'; // TrafficAssignmentBprParamInvalid
export const ERR_PEAK_HOUR_FACTOR_MISSING = 'MET-V911'; // TrafficAssignmentPeakHourFactorMissing (BUG-854)
export const ERR_MAX_ATTRIBUTION_RADIUS_MISSING = 'MET-V881'; // TrafficAssignmentMaxAttributionRadiusMissing (BUG-864, GR#15)
export const ERR_METRES_PER_MILE_MISSING = 'MET-V882'; // TrafficAssignmentMetresPerMileMissing (BUG-864, GR#15)
// FEAT-2326609800 inc7 (AC-2/AC-3/AC-4, GR#7) — claimed via
// `node tools/plan/add-error.js claim-range ui.webconsole --size 5` (the
// brief pre-assigned V935-V939, but that block was NOT actually reserved in
// data/errors.json at dispatch time — claim-range's lowest-free scan granted
// V930-V934 instead; see the BOW comment/report for the discrepancy).
export const ERR_TRIPS_PER_VEHICLE_MISSING = 'MET-V940'; // TrafficWearTripsPerVehicleMissing
export const ERR_FUEL_DUTY_RATE_MISSING = 'MET-V941'; // TrafficWearFuelDutyRateMissing
export const ERR_VED_RATE_MISSING = 'MET-V942'; // TrafficWearVedRateMissing
export const ERR_ESAL_FACTOR_MISSING = 'MET-V943'; // TrafficWearEsalFactorMissing (also covers other road_wear.json wearToRepairCost field gaps)
export const ERR_BASE_COST_MISSING = 'MET-V944'; // TrafficWearBaseCostMissing

function registryError(code: string, message: string): Error {
  return new Error(`${code}: ${message}`);
}
// BUG-914(a): exported so engine.ts can throw ERR_BASE_COST_MISSING
// (MET-V944) fail-closed for an unknown road class, instead of the `?? 0`
// silent-free-repair fallback that made MET-V944 registered-but-dead.
export { registryError };

// --- data/traffic.json typed view -------------------------------------------

interface TrafficConfig {
  baseCommuteHours: number;
  baseAccessMinutes: number;
  baseCommuteMinutes: number;
  bprAlpha: number;
  bprBeta: number;
  webconsoleMetresPerTile: number;
  /** BUG-847 (sibling inc2 rework, 2026-09-09): bounds any multi-source BFS
   * attribution radius so cost is bounded by data, never by the city's own
   * occupied bounding-box diameter (the old, unbounded shape measured 5.12M
   * BFS ops from one outpost building). BUG-864/GR#15 fix: fail-closed if the
   * field is missing/invalid — a silent `?? 250` fallback let a stale/older
   * traffic.json silently swap in an unsourced literal; the field is required
   * exactly like webconsoleMetresPerTile above, not merely preferred. */
  maxAttributionRadiusTiles: number;
  /** GR#15 leftover (BUG-864 r2): 1 international mile in metres — a pure
   * unit-conversion constant, moved off a hand-typed TS literal into data so
   * AC-2's free-flow-minutes formula never hand-types a conversion figure. */
  metresPerMile: number;
}

/**
 * BUG-865: pure loader taking the raw JSON as an ARGUMENT (the same
 * "pass the cfg as an argument" idiom BUG-861 already used successfully for
 * segmentFreeFlowMinutesFor) so every fail-closed field read here is
 * directly testable with a scratch `raw` object -- no module-load-time
 * side effect to work around. `loadTrafficConfig()` below is a thin wrapper
 * over the real data/traffic.json import; production behaviour is
 * unchanged.
 */
export function loadTrafficConfigFrom(raw: unknown): TrafficConfig {
  const j = raw as Record<string, unknown>;
  const metresPerTile = j.webconsoleMetresPerTile;
  if (typeof metresPerTile !== 'number' || !Number.isFinite(metresPerTile) || metresPerTile <= 0) {
    throw registryError(
      ERR_METRES_PER_TILE_MISSING,
      'data/traffic.json is missing a numeric webconsoleMetresPerTile field',
    );
  }
  const maxRadiusRaw = j.maxAttributionRadiusTiles;
  if (typeof maxRadiusRaw !== 'number' || !Number.isFinite(maxRadiusRaw) || maxRadiusRaw <= 0) {
    throw registryError(
      ERR_MAX_ATTRIBUTION_RADIUS_MISSING,
      'data/traffic.json is missing a positive numeric maxAttributionRadiusTiles field',
    );
  }
  // BUG-968 (r3 rework): floor to an integer so nearestSourceForTiles' radius
  // domain always agrees with boundedNearestSourceMapOf's inclusive
  // `dist < radius` layer semantics — see trafficDemand.ts's
  // loadMaxAttributionRadiusTiles for the full rationale.
  const maxRadius = Math.floor(maxRadiusRaw);
  const metresPerMile = j.metresPerMile;
  if (typeof metresPerMile !== 'number' || !Number.isFinite(metresPerMile) || metresPerMile <= 0) {
    throw registryError(
      ERR_METRES_PER_MILE_MISSING,
      'data/traffic.json is missing a positive numeric metresPerMile field',
    );
  }
  return {
    baseCommuteHours: j.baseCommuteHours as number,
    baseAccessMinutes: j.baseAccessMinutes as number,
    baseCommuteMinutes: j.baseCommuteMinutes as number,
    bprAlpha: j.bprAlpha as number,
    bprBeta: j.bprBeta as number,
    webconsoleMetresPerTile: metresPerTile,
    maxAttributionRadiusTiles: maxRadius,
    metresPerMile,
  };
}
function loadTrafficConfig(): TrafficConfig {
  return loadTrafficConfigFrom(rawTraffic);
}
const TRAFFIC = loadTrafficConfig();

// --- data/traffic/link_capacity.json typed view ------------------------------

interface RoadClassCapacityRow {
  roadClassId: string;
  capacityPcuPerLanePerHour: number;
}
interface RailClassRow {
  id: string;
  trainsPerHourMax: number;
}
interface LinkCapacityTable {
  roadClasses: RoadClassCapacityRow[];
  bprCurve: { perClassOverrides: Record<string, { alpha?: number; beta?: number }> };
  railClasses: RailClassRow[];
}
const LINK_CAPACITY = rawLinkCapacity as unknown as LinkCapacityTable;
const roadCapacityById = new Map<string, RoadClassCapacityRow>(
  LINK_CAPACITY.roadClasses.map((r) => [r.roadClassId, r]),
);
const railById = new Map<string, RailClassRow>(LINK_CAPACITY.railClasses.map((r) => [r.id, r]));

// --- data/roads.json typed view ---------------------------------------------

interface RoadClassRow {
  id: string;
  lanes: number;
  speedLimit: number;
}
const ROADS_TABLE = rawRoads as unknown as {
  classes: RoadClassRow[];
  maintenance: { conditionDecayPerMonth: number };
};
const ROADS = ROADS_TABLE.classes;
const roadRowById = new Map<string, RoadClassRow>(ROADS.map((r) => [r.id, r]));

/**
 * BUG-947 (LEAD RULING, r2 amendment): the set of road class ids
 * data/roads.json actually defines, exported so a SNAPSHOT-carried
 * roadClassId (trafficWellbeing.ts's sanitizeTrafficSnapshot) can be
 * validated at sanitize time rather than passing through as "any non-empty
 * string" and only failing later, inside engine.ts's advance(), with a
 * game-bricking MET-V944 throw when a stale save names a removed/renamed
 * class. Single source of truth — never restated as a literal list.
 */
export const ROAD_CLASS_IDS: ReadonlySet<string> = new Set(ROADS.map((r) => r.id));

/** FEAT-2326609800 inc7 (AC-5, GR#15) — reused (never restated) as the
 * per-tick repair-cost amortisation basis: roads.json's own age-based decay
 * RATE is the only per-tick-shaped fraction this data set carries, so this
 * increment repurposes it rather than hand-typing a new fraction (the doc's
 * own "flagged ASM if no suitable field exists" escape did not apply — a
 * suitable field DOES exist, just for a different original purpose; the
 * report flags this repurposing honestly for Aaron's balance pass). Divided
 * by TICKS_PER_MONTH at the engine.ts call site (this module has no
 * calendar/tick constant of its own, GR#3 — TICKS_PER_MONTH is engine.ts's).
 *
 * FISCAL BOUNDARY (AC-8, inc3, `trafficAssignment.test.mjs`'s own pin):
 * this module NEVER reads roads.json's currency-shaped class-cost field —
 * that read (and the ERR_BASE_COST_MISSING registry error) lives in
 * engine.ts instead, the ONLY place trafficAssignment.ts's inc3 fiscal-
 * boundary invariant permits a currency figure. This module exposes only
 * the PHYSICAL roadClassId (roadClassIdOfSegment, already exported) a
 * repair event occurred on; engine.ts converts that id to a cost.
 */
export const ROAD_MAINTENANCE_CONDITION_DECAY_PER_MONTH = ROADS_TABLE.maintenance.conditionDecayPerMonth;

// --- FEAT-2326609800 inc7: fuel.json / taxation.json / road_wear.json / trip_generation.json typed views ---

interface FuelTable {
  duty: { ratePencePerLitre: number };
}
const FUEL = rawFuel as unknown as FuelTable;
// BUG-914(b): loadFuelDutyRateFrom(raw) is a pure loader taking the raw
// parsed JSON as an ARGUMENT (the BUG-865 idiom), so the fail-closed branch
// is directly testable with a scratch object — the real data/fuel.json
// import below is the only production caller.
export function loadFuelDutyRateFrom(raw: FuelTable): number {
  const v = raw?.duty?.ratePencePerLitre;
  if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
    throw registryError(ERR_FUEL_DUTY_RATE_MISSING, 'data/fuel.json duty.ratePencePerLitre is missing or not a positive finite number');
  }
  return v;
}
export const FUEL_DUTY_RATE_PENCE_PER_LITRE: number = loadFuelDutyRateFrom(FUEL);

interface TaxationTable {
  vehicleExciseDuty: { fleetAverageByVehicleClass: Record<string, { gbpPerYear: number }> };
}
const TAXATION = rawTaxation as unknown as TaxationTable;
// BUG-914(b): `table` defaults to the real parsed data/traffic/taxation.json
// (production behaviour unchanged) but is overridable so a test can exercise
// the missing/NaN/negative/string branches with a scratch table instead of
// mutating the real file.
export function vedGbpPerYearFor(classId: string, table: TaxationTable = TAXATION): number {
  const row = table.vehicleExciseDuty.fleetAverageByVehicleClass[classId];
  if (!row || typeof row.gbpPerYear !== 'number' || !Number.isFinite(row.gbpPerYear) || row.gbpPerYear <= 0) {
    throw registryError(ERR_VED_RATE_MISSING, `data/traffic/taxation.json fleetAverageByVehicleClass is missing a positive numeric gbpPerYear entry for vehicle class ${classId}`);
  }
  return row.gbpPerYear;
}

interface RoadWearTable {
  esalFactors: Record<string, { esalFactorPer100VehicleKm: number }>;
  wearToRepairCost: {
    conditionDecayPerESAL: { value: number };
    repairTriggerConditionIndex: { value: number };
    repairCostCurve: Array<{ conditionIndex: number; repairCostMultiplier: number }>;
    targetTicksToResurfaceAtCapacity?: { value: number; referenceVehiclesPerTickAtCapacity: number };
  };
}
const ROAD_WEAR = rawRoadWear as unknown as RoadWearTable;
// BUG-914(b): same overridable-table idiom as vedGbpPerYearFor above.
export function esalFactorFor(classId: string, table: RoadWearTable = ROAD_WEAR): number {
  const row = table.esalFactors[classId];
  if (!row || typeof row.esalFactorPer100VehicleKm !== 'number' || !Number.isFinite(row.esalFactorPer100VehicleKm) || row.esalFactorPer100VehicleKm < 0) {
    throw registryError(ERR_ESAL_FACTOR_MISSING, `data/traffic/road_wear.json esalFactors is missing a positive numeric esalFactorPer100VehicleKm entry for vehicle class ${classId}`);
  }
  return row.esalFactorPer100VehicleKm;
}
// BUG-914(b): loadConditionDecayPerEsalFrom/loadRepairTriggerConditionIndexFrom/
// loadRepairCostCurveFrom are pure loaders over a raw RoadWearTable-shaped
// object (BUG-865 idiom) — directly testable with a scratch object.
export function loadConditionDecayPerEsalFrom(raw: RoadWearTable): number {
  const v = raw.wearToRepairCost?.conditionDecayPerESAL?.value;
  if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
    throw registryError(ERR_ESAL_FACTOR_MISSING, 'data/traffic/road_wear.json wearToRepairCost.conditionDecayPerESAL.value is missing or not a positive finite number');
  }
  return v;
}
export function loadRepairTriggerConditionIndexFrom(raw: RoadWearTable): number {
  const v = raw.wearToRepairCost?.repairTriggerConditionIndex?.value;
  if (typeof v !== 'number' || !Number.isFinite(v) || v < 0 || v > 100) {
    throw registryError(ERR_ESAL_FACTOR_MISSING, 'data/traffic/road_wear.json wearToRepairCost.repairTriggerConditionIndex.value is missing or not a finite number in [0,100]');
  }
  return v;
}
export function loadRepairCostCurveFrom(raw: RoadWearTable): Array<{ conditionIndex: number; repairCostMultiplier: number }> {
  const curve = raw.wearToRepairCost?.repairCostCurve;
  if (!Array.isArray(curve) || curve.length < 2) {
    throw registryError(ERR_ESAL_FACTOR_MISSING, 'data/traffic/road_wear.json wearToRepairCost.repairCostCurve is missing or has fewer than 2 anchor points');
  }
  // Defensive sort descending by conditionIndex (the shipped data is already
  // sorted this way, but the interpolation below depends on it structurally).
  return [...curve].sort((a, b) => b.conditionIndex - a.conditionIndex);
}
// BUG-932 (FEAT-2326609800 inc7 r3 lead amendment): the live conditionDecayPerESAL
// constant is now DERIVED from road_wear.json's targetTicksToResurfaceAtCapacity
// (a stated in-game timescale) rather than an independent hand-typed rate --
// loadConditionDecayPerEsalFrom above stays for its OWN missing/NaN/negative/
// string pin coverage of the legacy literal field (backward documentation), but
// no longer feeds the live constant.
export function loadTargetTicksToResurfaceAtCapacityFrom(raw: RoadWearTable): number {
  const v = raw.wearToRepairCost?.targetTicksToResurfaceAtCapacity?.value;
  if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
    throw registryError(ERR_ESAL_FACTOR_MISSING, 'data/traffic/road_wear.json wearToRepairCost.targetTicksToResurfaceAtCapacity.value is missing or not a positive finite number');
  }
  return v;
}
export function loadReferenceVehiclesPerTickAtCapacityFrom(raw: RoadWearTable): number {
  const v = raw.wearToRepairCost?.targetTicksToResurfaceAtCapacity?.referenceVehiclesPerTickAtCapacity;
  if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
    throw registryError(ERR_ESAL_FACTOR_MISSING, 'data/traffic/road_wear.json wearToRepairCost.targetTicksToResurfaceAtCapacity.referenceVehiclesPerTickAtCapacity is missing or not a positive finite number');
  }
  return v;
}
/**
 * BUG-932 — derives conditionDecayPerESAL from a STATED timescale instead of
 * an independent literal: a 1km reference segment carrying
 * referenceVehiclesPerTickAtCapacity car-equivalent vehicles every tick
 * accrues (refVehicles x 1km x car's esalFactorPer100VehicleKm / 100) ESAL
 * per tick; targetTicksToResurfaceAtCapacity ticks of that must exactly
 * consume the (100 - repairTriggerConditionIndex) points between a fresh
 * road and the repair trigger. The reference flow is a fixed placeholder
 * constant (not the scale-ladder's peakHourFactor-derived capacity) because
 * this constant is computed at MODULE LOAD time, before any SimState (and
 * therefore any scale-ladder rung) exists — see the data file's own
 * referenceVehiclesPerTickAtCapacitySource note.
 */
export function deriveConditionDecayPerEsalFrom(raw: RoadWearTable): number {
  const targetTicks = loadTargetTicksToResurfaceAtCapacityFrom(raw);
  const refVehiclesPerTick = loadReferenceVehiclesPerTickAtCapacityFrom(raw);
  const trigger = loadRepairTriggerConditionIndexFrom(raw);
  const carEsalFactor = esalFactorFor('car', raw);
  const REFERENCE_SEGMENT_KM = 1;
  const esalPerTickAtCapacity = (refVehiclesPerTick * REFERENCE_SEGMENT_KM * carEsalFactor) / 100;
  if (esalPerTickAtCapacity <= 0) {
    throw registryError(ERR_ESAL_FACTOR_MISSING, 'data/traffic/road_wear.json derives a non-positive reference ESAL/tick at capacity — check esalFactors.car and targetTicksToResurfaceAtCapacity.referenceVehiclesPerTickAtCapacity');
  }
  return (100 - trigger) / (esalPerTickAtCapacity * targetTicks);
}
const CONDITION_DECAY_PER_ESAL: number = deriveConditionDecayPerEsalFrom(ROAD_WEAR);
export const REPAIR_TRIGGER_CONDITION_INDEX: number = loadRepairTriggerConditionIndexFrom(ROAD_WEAR);
const REPAIR_COST_CURVE: Array<{ conditionIndex: number; repairCostMultiplier: number }> = loadRepairCostCurveFrom(ROAD_WEAR);

interface TripGenerationTableForWear {
  tripsPerVehiclePerDay: Record<string, { tripsPerVehiclePerDay: number }>;
}
const TRIP_GENERATION_WEAR = rawTripGeneration as unknown as TripGenerationTableForWear;
// BUG-914(b): same overridable-table idiom as vedGbpPerYearFor/esalFactorFor above.
export function tripsPerVehiclePerDayFor(classId: string, table: TripGenerationTableForWear = TRIP_GENERATION_WEAR): number {
  const row = table.tripsPerVehiclePerDay?.[classId];
  if (!row || typeof row.tripsPerVehiclePerDay !== 'number' || !Number.isFinite(row.tripsPerVehiclePerDay) || row.tripsPerVehiclePerDay <= 0) {
    throw registryError(ERR_TRIPS_PER_VEHICLE_MISSING, `data/traffic/trip_generation.json tripsPerVehiclePerDay is missing a positive numeric entry for vehicle class ${classId}`);
  }
  return row.tripsPerVehiclePerDay;
}

/** vehicle_classes.json roadVehicles' fuelLitresPerKm, by class id — only the
 * 6 classes that carry a numeric fuelLitresPerKm row (buses are modelled as
 * PSV subtypes with no roadVehicles fuelLitresPerKm figure — ASM, see report;
 * the Fuel Duty basis below honestly excludes bus rather than fabricate one). */
const fuelLitresPerKmById = new Map<string, number>(
  (rawVehicleClasses as unknown as { roadVehicles: Array<{ id: string; fuelLitresPerKm?: number }> }).roadVehicles
    .filter((v) => typeof v.fuelLitresPerKm === 'number')
    .map((v) => [v.id, v.fuelLitresPerKm as number]),
);
// BUG-914(b): same overridable-table idiom as vedGbpPerYearFor/esalFactorFor.
export function fuelLitresPerKmFor(classId: string, table: ReadonlyMap<string, number> = fuelLitresPerKmById): number {
  const v = table.get(classId);
  if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
    throw registryError(ERR_FUEL_DUTY_RATE_MISSING, `data/traffic/vehicle_classes.json roadVehicles is missing a positive numeric fuelLitresPerKm entry for vehicle class ${classId}`);
  }
  return v;
}

/** Vehicle classes with a real fuelLitresPerKm figure today (excludes bus —
 * see fuelLitresPerKmById's doc). GR#3: schema-shape id list, not a "value",
 * same idiom as ROAD_PERSON_MODE_IDS/ROAD_FREIGHT_VEHICLE_IDS above. */
const FUEL_DUTY_CLASS_IDS: readonly VehicleClassId[] = ['car', 'motorbike', 'taxi', 'cargo_van', 'rigid_truck', 'articulated_truck'];
/** Vehicle classes with a real taxation.json fleetAverageByVehicleClass row
 * today (excludes bus — taxation.json's table has no bus entry, buses use a
 * separate PSV-operator licensing regime in reality, not modelled here). */
const VED_CLASS_IDS: readonly VehicleClassId[] = ['car', 'motorbike', 'taxi', 'cargo_van', 'rigid_truck', 'articulated_truck'];

// --- data/traffic/vehicle_classes.json typed view (A-10, occupancy) --------

interface RoadVehicleRow {
  id: string;
  avgOccupancyPersons: { value: number };
}
interface BusSubtype {
  totalCapacity: number;
  avgLoadFactor: number;
}
const VEHICLE_CLASSES = rawVehicleClasses as unknown as {
  roadVehicles: RoadVehicleRow[];
  busSubtypes: Record<string, BusSubtype>;
};
const roadOccupancyById = new Map<string, number>(
  VEHICLE_CLASSES.roadVehicles.map((v) => [v.id, v.avgOccupancyPersons.value]),
);
/**
 * The 'bus' mode has no roadVehicles row (buses are modelled as PSV
 * subtypes, not a single vehicle_classes.json roadVehicles entry) — occupancy
 * per bus vehicle-trip is derived from the single_deck subtype's own
 * totalCapacity x avgLoadFactor (persons actually riding, the same "actual,
 * not seat capacity" idiom A-10 requires), the representative UK PSV class.
 */
const BUS_OCCUPANCY_PERSONS: number =
  VEHICLE_CLASSES.busSubtypes.single_deck.totalCapacity * VEHICLE_CLASSES.busSubtypes.single_deck.avgLoadFactor;

/** ROAD_PERSON_MODE_IDS (trafficDemand.ts:320) mirrored here (GR#3 — same
 * list, re-declared because trafficDemand.ts does not export it) so this
 * module can convert each mode's SHARE of a tile's personTrips into
 * vehicle-trips via its own occupancy figure. */
const ROAD_PERSON_MODE_IDS: readonly string[] = ['car', 'motorbike', 'taxi', 'bus'];

// Exported additively (FEAT-2326609799 inc6, GR#3 — parkingFuel.ts's AC-4
// vehicle-km derivation needs the SAME occupancy-per-mode conversion
// assignedFlowOf already performs above, not a second copy).
export function occupancyForMode(modeId: string): number {
  if (modeId === 'bus') return BUS_OCCUPANCY_PERSONS;
  return roadOccupancyById.get(modeId) ?? 0;
}

// --- ROAD_CLASS_ID_OF_TIER (§2) ---------------------------------------------

/**
 * Maps the 5 game road-tier spec ids (ROAD_TIER_SPECS, data.ts:427) onto
 * data/roads.json's 11 real-world class ids, per the doc's own two worked
 * examples (tier1 'road'->residential_street, tier2 'rd_avenue'-
 * >avenue_2_plus_2) and cross-checked against AC-2's Check fixture ("a
 * single-tile two_lane segment, speedLimit 40 mph") and AC-4's mutant
 * ("the motorway class, whose alpha 0.12 diverges from the default 0.15") —
 * both only make sense if tier3 'rd_aroad' resolves to 'two_lane' (the only
 * in-scope road tier below dual/motorway that a segment test could exercise,
 * since SEGMENT_ROAD_CLASSES only segments rd_aroad/rd_dual/m20 — tier1/2
 * ('road'/'rd_avenue') never actually get segmented today, so their mapping
 * below is present for completeness/future extension but not exercised at
 * runtime; see the report's "where the doc is wrong" section).
 *
 * MOVED to data.ts (FEAT-1972079910 inc4, GR#3 dedupe): data.ts's new
 * minBendRadiusTilesForTier needed this same tier->class binding and
 * data.ts is the lower layer (this module already imports FROM data.ts, so
 * the reverse import would cycle) — re-exported here unchanged so every
 * existing consumer of this export (parkingFuel.ts, trafficRewards.ts,
 * emergencyResponse.ts's test) keeps working without modification.
 */
// Additively exported (BUG-872, FEAT-2326609797 inc4 rework): emergencyResponse.ts's
// narrow-class-penalty lookup needed the SAME tier->class mapping this module already owns --
// GR#3 forbade the near-verbatim local copy that inc4's first build carried, so this table and
// its lookup function are exported here rather than having a second copy silently diverge.
export { ROAD_CLASS_ID_OF_TIER };

export function roadClassIdOfSegment(seg: LineSegment): string {
  const sp = SPECS[seg.spec];
  const tier = sp?.roadTier;
  const roadClassId = tier != null ? ROAD_CLASS_ID_OF_TIER[tier] : undefined;
  if (!roadClassId) {
    throw registryError(
      ERR_ROAD_CLASS_UNMAPPED,
      `road tier ${String(tier)} (spec ${seg.spec}) has no entry in ROAD_CLASS_ID_OF_TIER`,
    );
  }
  return roadClassId;
}

function roadClassRow(roadClassId: string): RoadClassRow {
  const row = roadRowById.get(roadClassId);
  if (!row) {
    throw registryError(ERR_SPEED_LIMIT_MISSING, `data/roads.json classes has no entry for road class ${roadClassId}`);
  }
  return row;
}

function linkCapacityRow(roadClassId: string): RoadClassCapacityRow {
  const row = roadCapacityById.get(roadClassId);
  if (!row) {
    throw registryError(
      ERR_LINK_CAPACITY_MISSING,
      `data/traffic/link_capacity.json roadClasses has no entry for road class ${roadClassId}`,
    );
  }
  return row;
}

function railClassRow(spec: string): RailClassRow {
  const row = railById.get(spec);
  if (!row) {
    throw registryError(ERR_LINK_CAPACITY_MISSING, `data/traffic/link_capacity.json railClasses has no entry for ${spec}`);
  }
  return row;
}

/** BPR alpha/beta for a road class: link_capacity.json's per-class override
 * where present (per-KEY, not per-block — AC-7's Check requires a future
 * partial override to be honoured), else data/traffic.json's network default.
 * Exported (BUG-863): a live beta override DOES exist today
 * (residential_street 4.5, alley 5.0 — data/traffic/link_capacity.json
 * bprCurve.perClassOverrides) even though no currently-SEGMENTED road class
 * carries one (SEGMENT_ROAD_CLASSES only segments rd_aroad/rd_dual/m20); this
 * export lets the override-VALUE-honoured path be pinned directly against
 * this pure function, with zero data change and zero dependency on which
 * classes happen to be segmentable today. */
export function bprParamsFor(roadClassId: string): { alpha: number; beta: number } {
  const override = LINK_CAPACITY.bprCurve.perClassOverrides[roadClassId];
  const alpha = override?.alpha ?? TRAFFIC.bprAlpha;
  const beta = override?.beta ?? TRAFFIC.bprBeta;
  // MET-V910's registry template ("BPR parameter %s for road class %s is not
  // finite and strictly positive") carries TWO %s slots (which param, which
  // road class) -- BUG-865: the throw below now supplies both, naming the
  // offending parameter, instead of a single-slot message that silently
  // dropped which of alpha/beta actually failed.
  if (!Number.isFinite(alpha) || alpha <= 0) {
    throw registryError(ERR_BPR_PARAM_INVALID, `BPR parameter alpha for road class ${roadClassId} is not finite/positive`);
  }
  if (!Number.isFinite(beta) || beta <= 0) {
    throw registryError(ERR_BPR_PARAM_INVALID, `BPR parameter beta for road class ${roadClassId} is not finite/positive`);
  }
  return { alpha, beta };
}

/**
 * BUG-854 fix: v/c's `v` must be a PEAK-HOUR-per-lane volume (link_capacity
 * json's `bprCurve.formula` states plainly "v = per-lane volume (pcu/h)"),
 * not a daily total. `assignedFlow` (AC-3) is vehicle-trips PER DAY. The
 * data-sourced conversion factor for exactly this is the CURRENT scale-
 * ladder rung's own `peakHourFactor` leaf (data/traffic/scale_ladder.json,
 * documented docs/planning/traffic-assumptions.md A-9 — "share of daily
 * trips occurring in the design/peak hour"), already reachable via the
 * `ladderPointOf` this module imports. The old code divided by
 * `baseCommuteHours` (5.0, an unrelated engine demand-ratio BASELINE, see
 * internal/engine/compose/traffic_wire.go:139) — that read v/c 1.8182x too
 * high (= 1/(5*0.11)) and, with beta=4, the BPR delay TERM 1.8182^4=10.93x
 * too high. ASM-1507 (the old "operating window" reading) is SUPERSEDED by
 * this fix — see the BOW comment on FEAT-2326609796/BUG-854.
 */
function peakHourFactorOf(point: LadderPointLike): number {
  const f = point.fields.find((x) => x.key === 'peakHourFactor');
  if (!f || !Number.isFinite(f.value) || f.value <= 0) {
    throw registryError(
      ERR_PEAK_HOUR_FACTOR_MISSING,
      `scale ladder point at population ${point.population} is missing a numeric, positive peakHourFactor field`,
    );
  }
  return f.value;
}
type LadderPointLike = { population: number; fields: Array<{ key: string; value: number }> };

// --- AC-1: segmentAdjacencyOf -----------------------------------------------

/**
 * AC-1 — deterministic segment-to-segment adjacency graph. ONE pass over
 * `lineSegmentIndexOf(s).tileToSegment`'s entries in sorted "x,y" key order
 * (GR#21), adding a same-kind edge for every cross-segment tile adjacency.
 * Symmetric by construction: both directions are added within the same
 * iteration that discovers the pair.
 */
export const segmentAdjacencyOf: (s: SimState) => Map<string, Set<string>> = memoOnState((s) => {
  const idx = lineSegmentIndexOf(s);
  const adjacency = new Map<string, Set<string>>();
  const ensure = (id: string): Set<string> => {
    let set = adjacency.get(id);
    if (!set) {
      set = new Set();
      adjacency.set(id, set);
    }
    return set;
  };
  const sortedKeys = [...idx.tileToSegment.keys()].sort();
  for (const key of sortedKeys) {
    const segId = idx.tileToSegment.get(key)!;
    const seg = idx.segmentById.get(segId);
    if (!seg) continue;
    const comma = key.indexOf(',');
    const x = Number(key.slice(0, comma));
    const y = Number(key.slice(comma + 1));
    const neighbours = [`${x + 1},${y}`, `${x - 1},${y}`, `${x},${y + 1}`, `${x},${y - 1}`];
    for (const nk of neighbours) {
      const nSegId = idx.tileToSegment.get(nk);
      if (!nSegId || nSegId === segId) continue;
      const nSeg = idx.segmentById.get(nSegId);
      if (!nSeg || nSeg.kind !== seg.kind) continue;
      ensure(segId).add(nSegId);
      ensure(nSegId).add(segId);
    }
  }
  return adjacency;
});

// --- AC-2: segmentFreeFlowMinutesOf -----------------------------------------

/**
 * AC-2 — free-flow minutes per segment. Road segments:
 * `tiles * cfg.webconsoleMetresPerTile / (speedLimitMph * cfg.metresPerMile / 60)`.
 * Rail/hs1 segments use the class's own trainsPerHourMax-implied headway as a
 * coarse free-flow proxy (doc §2: "documented separately, not blocking this
 * AC") — NOT a road speed.
 *
 * BUG-861 fix: the metres-per-tile figure is threaded through as an explicit
 * `cfg` ARGUMENT rather than closing over the module-level `TRAFFIC` constant
 * directly. A `seg.tiles * 50` mutant (restating the CURRENT data value as a
 * literal) is behaviourally indistinguishable from the correct
 * `seg.tiles * TRAFFIC.webconsoleMetresPerTile` as long as the test only ever
 * exercises the module's single fixed TRAFFIC config — the prior rework's
 * "structural pin" greps caught neither the consumer line nor a value-only
 * numeric assertion (both pass whether the number came from data.json or a
 * hand-typed literal that happens to currently equal it). Varying `cfg` at
 * the CALL SITE is the only shape a literal cannot fake: a hand-typed `* 50`
 * inside the function body ignores `cfg` entirely, so calling this function
 * with a scratch cfg whose `webconsoleMetresPerTile` is NOT 50 immediately
 * diverges from the expected scaled value.
 */
export function segmentFreeFlowMinutesFor(
  s: SimState,
  cfg: Pick<TrafficConfig, 'webconsoleMetresPerTile' | 'metresPerMile'>,
): Map<string, number> {
  const idx = lineSegmentIndexOf(s);
  const out = new Map<string, number>();
  for (const seg of idx.segments) {
    const metres = seg.tiles * cfg.webconsoleMetresPerTile;
    if (seg.kind === 'road') {
      const roadClassId = roadClassIdOfSegment(seg);
      const { speedLimit } = roadClassRow(roadClassId);
      const metresPerMinute = (speedLimit * cfg.metresPerMile) / 60;
      out.set(seg.segmentId, metres / metresPerMinute);
    } else {
      const rail = railClassRow(seg.spec);
      // Headway-implied minutes-per-train-slot proxy (60 / trainsPerHourMax),
      // a coarse stand-in for a true block-signalling free-flow time.
      out.set(seg.segmentId, 60 / rail.trainsPerHourMax);
    }
  }
  return out;
}

export const segmentFreeFlowMinutesOf: (s: SimState) => Map<string, number> = memoOnState((s) =>
  segmentFreeFlowMinutesFor(s, TRAFFIC),
);

// --- nearest-road-segment tile map + job-adjacent destination set ----------
// BUG-857/GR#3 fix: this used to reimplement inc2's nearest-segment BFS
// locally WITHOUT a MAP_W/MAP_H clamp (measured 136,874 of 320,883 visited
// tiles off-map on a 46-building sparse city, 391ms — worse than a
// 25,600-building grid city). It now calls trafficDemand.ts's exported
// `boundedNearestSourceMapOf` (the same map-bounded, radius-bounded,
// deterministic-tie-break primitive `nearestSegmentWeights` uses
// internally) and maps each reached tile to its owning road segment via
// `tileToSegment` here — no second, unbounded BFS implementation anywhere
// in this codebase.

// BUG-912 perf fix: this function's body reads ONLY `s.buildings` (via
// lineSegmentIndexOf(s), itself buildings-derived, and the bbox loop below)
// — nothing else from SimState. Keying its cache on `s` (memoOnState) meant
// it recomputed the EXPENSIVE bounded nearest-source BFS (boundedNearestSourceMapOf,
// over every map tile within radius of the road network) from scratch on
// EVERY tick, even the overwhelming majority where no building was placed
// or demolished — because `s` is a fresh object every tick but `s.buildings`
// usually is not. Rekeying on `s.buildings`' own array identity (the SAME
// idiom data.ts's buildingByIdOf already uses, safe for the same reason:
// this codebase's whole update discipline is immutable replace, never
// in-place mutation, so "same buildings array reference" really does mean
// "same set of Buildings, unchanged") turns the common no-construction tick
// into an O(1) cache hit instead of a full BFS. Measured: this was THE
// dominant cost of BUG-912's regression (a CPU profile on the attacker's
// 4,900-building/70x70 fixture showed ~43% of all tick time inside
// boundedNearestSourceMapOf via this call site alone) — inc7 is the FIRST
// feature to route trafficAssignment.ts's Dijkstra/BFS pipeline through the
// money/tick hot path every tick (previously only the Lines overlay read
// it, on demand); this fix does not change WHAT is computed, only HOW OFTEN.
const nearestRoadSegmentTileMapByBuildings = new WeakMap<SimState['buildings'], Map<string, string>>();
const nearestRoadSegmentTileMapOf: (s: SimState) => Map<string, string> = (s) => {
  const cached = nearestRoadSegmentTileMapByBuildings.get(s.buildings);
  if (cached) return cached;
  const value = computeNearestRoadSegmentTileMap(s);
  nearestRoadSegmentTileMapByBuildings.set(s.buildings, value);
  return value;
};
function computeNearestRoadSegmentTileMap(s: SimState): Map<string, string> {
  const idx = lineSegmentIndexOf(s);
  const roadTileKeys: string[] = [];
  for (const [tileKey, segId] of idx.tileToSegment) {
    const seg = idx.segmentById.get(segId);
    if (seg && seg.kind === 'road') roadTileKeys.push(tileKey);
  }

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
  const bboxDiameter = Number.isFinite(minX) ? maxX - minX + (maxY - minY) : 0;
  // BUG-847 class: bound by data (maxAttributionRadiusTiles), never by the
  // raw occupied bounding-box diameter alone — same fix the sibling inc2
  // rework applied to nearestSegmentWeights.
  const radius = Math.min(bboxDiameter, TRAFFIC.maxAttributionRadiusTiles);

  // BUG-935 perf fix, BUG-958 rework (P1 from r1's round): this Map's real
  // consumer (assignmentOf below) only ever queries `.get(tileKey)` for
  // demandForecastOf(s)'s tiles, so the ORIGINAL fix queried exactly that
  // set — but this function is cached on `s.buildings`' ARRAY IDENTITY
  // (BUG-912 above), a cache whose whole soundness rests on its stated
  // precondition: "this function's body reads ONLY s.buildings". Querying
  // demandForecastOf(s) broke that precondition — demandForecastOf depends
  // on population/occupancy/isOnline/the ladder, ALL of which change every
  // tick without `s.buildings` changing, so the SAME buildings array can be
  // cached against an empty demand set on a cold tick and silently serve
  // that stale, too-small map forever after (r1's attacker reproduction:
  // 100% of a tick's routed flow lost, decided only by which earlier state
  // happened to warm the cache).
  //
  // Fix: derive the query set from `s.buildings` alone instead — every
  // demandForecastOf(s) tile's (x,y) comes straight from a building `b`
  // (`out.push({ x: b.x, y: b.y, ... })` in trafficDemand.ts), so "every
  // building's own tile" is a SUPERSET of the true demand tiles that is
  // still buildings-derived (and therefore cache-safe) and still
  // city-proportional, never map-sized — it only ever grows past the exact
  // demand set by the handful of buildings with no resident/job capacity or
  // zero trips that tick, not by the pre-shipped infrastructure network.
  const queryTileKeys = s.buildings.map((b) => `${b.x},${b.y}`);
  const nearestSourceTile = nearestSourceForTiles(queryTileKeys, roadTileKeys, radius);
  const result = new Map<string, string>(); // tileKey -> owning nearest road segmentId
  for (const [tileKey, sourceTileKey] of nearestSourceTile) {
    result.set(tileKey, idx.tileToSegment.get(sourceTileKey)!);
  }
  return result;
}

const jobAdjacentRoadSegmentsOf: (s: SimState) => Set<string> = memoOnState((s) => {
  const idx = lineSegmentIndexOf(s);
  const jobTiles = new Set<string>();
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp && sp.jobs != null && isOnline(s, b)) jobTiles.add(`${b.x},${b.y}`);
  }
  const result = new Set<string>();
  const sortedTileKeys = [...idx.tileToSegment.keys()].sort();
  for (const key of sortedTileKeys) {
    const segId = idx.tileToSegment.get(key)!;
    const seg = idx.segmentById.get(segId);
    if (!seg || seg.kind !== 'road') continue;
    const comma = key.indexOf(',');
    const x = Number(key.slice(0, comma));
    const y = Number(key.slice(comma + 1));
    for (const nk of [`${x + 1},${y}`, `${x - 1},${y}`, `${x},${y + 1}`, `${x},${y - 1}`]) {
      if (jobTiles.has(nk)) {
        result.add(segId);
        break;
      }
    }
  }
  return result;
});

// --- Dijkstra (deterministic tie-break: lower segmentId wins) --------------

interface HeapItem {
  dist: number;
  segId: string;
}
class MinHeap {
  private items: HeapItem[] = [];
  get size(): number {
    return this.items.length;
  }
  push(item: HeapItem): void {
    this.items.push(item);
    this.bubbleUp(this.items.length - 1);
  }
  pop(): HeapItem | undefined {
    const top = this.items[0];
    const last = this.items.pop();
    if (this.items.length > 0 && last !== undefined) {
      this.items[0] = last;
      this.bubbleDown(0);
    }
    return top;
  }
  private less(a: HeapItem, b: HeapItem): boolean {
    return a.dist < b.dist || (a.dist === b.dist && a.segId < b.segId);
  }
  private bubbleUp(i: number): void {
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (this.less(this.items[i], this.items[p])) {
        [this.items[i], this.items[p]] = [this.items[p], this.items[i]];
        i = p;
      } else break;
    }
  }
  private bubbleDown(i: number): void {
    const n = this.items.length;
    for (;;) {
      let smallest = i;
      const l = 2 * i + 1;
      const r = 2 * i + 2;
      if (l < n && this.less(this.items[l], this.items[smallest])) smallest = l;
      if (r < n && this.less(this.items[r], this.items[smallest])) smallest = r;
      if (smallest !== i) {
        [this.items[i], this.items[smallest]] = [this.items[smallest], this.items[i]];
        i = smallest;
      } else break;
    }
  }
}

/** AC-9 structural pin support: a test-only relaxation counter. Incremented
 * on every neighbour relaxation across every Dijkstra run since the last
 * reset — production code pays only the cost of one integer increment, never
 * gated behind a flag (so the counted behaviour IS the shipped behaviour). */
let dijkstraRelaxationCount = 0;
export function __resetDijkstraRelaxationCounterForTest(): void {
  dijkstraRelaxationCount = 0;
}
export function __getDijkstraRelaxationCounterForTest(): number {
  return dijkstraRelaxationCount;
}

/**
 * One-shot Dijkstra from `originSegId` over `adjacency`, edge weight =
 * the NEIGHBOUR segment's own free-flow minutes (AC-2's t0 — doc §2: "edge
 * weight = the destination segment's FREE-FLOW time"), stopping at the first
 * member of `destSet` popped off the heap (Dijkstra explores in non-
 * decreasing distance order, so the first destination popped is nearest).
 * Deterministic tie-break: lower segmentId wins at equal distance.
 */
function dijkstraPath(
  originSegId: string,
  destSet: ReadonlySet<string>,
  adjacency: ReadonlyMap<string, Set<string>>,
  freeFlow: ReadonlyMap<string, number>,
): string[] | null {
  const dist = new Map<string, number>([[originSegId, 0]]);
  const prev = new Map<string, string>();
  const visited = new Set<string>();
  const heap = new MinHeap();
  heap.push({ dist: 0, segId: originSegId });

  if (destSet.has(originSegId)) return [originSegId];

  while (heap.size > 0) {
    const top = heap.pop()!;
    if (visited.has(top.segId)) continue;
    visited.add(top.segId);
    if (destSet.has(top.segId)) {
      const path: string[] = [top.segId];
      let cur = top.segId;
      while (prev.has(cur)) {
        cur = prev.get(cur)!;
        path.push(cur);
      }
      path.reverse();
      return path;
    }
    const neighbours = adjacency.get(top.segId);
    if (!neighbours) continue;
    for (const n of [...neighbours].sort()) {
      dijkstraRelaxationCount++;
      if (visited.has(n)) continue;
      const w = freeFlow.get(n) ?? 0;
      const nd = top.dist + w;
      const known = dist.get(n);
      if (known === undefined || nd < known) {
        dist.set(n, nd);
        prev.set(n, top.segId);
        heap.push({ dist: nd, segId: n });
      }
    }
  }
  return null;
}

// --- AC-3: assignedFlowOf ----------------------------------------------------

export interface UnroutedDemand {
  x: number;
  y: number;
  vehicleTrips: number;
  reason: 'no-origin-segment' | 'no-destination' | 'no-path';
}

interface AssignmentResult {
  assignedFlow: Map<string, number>;
  // FEAT-2326609800 inc7 (AC-1) — the SAME accumulation, ADDITIVELY tagged
  // per vehicle-class BEFORE the accumulate() call (never after — tagging
  // after would need to re-derive each class's share of the already-summed
  // total, a second, divergence-prone model, GR#3). Sums to assignedFlow by
  // construction: every contribution to `assignedFlow` below has an exactly
  // matching contribution pushed into `assignedFlowByClass` in the SAME
  // iteration, never a separate pass.
  assignedFlowByClass: Map<string, Partial<Record<VehicleClassId, number>>>;
  unrouted: UnroutedDemand[];
  tilePaths: Map<string, string[]>; // "x,y" -> ordered segmentId path
  tileVehicleTrips: Map<string, number>; // "x,y" -> routed vehicle-trips (for commute weighting)
}

/**
 * AC-3 — single ONE-PASS assignment (§2/AC-9: no equilibrium loop). Every
 * demandForecastOf(s) tile's road-mode person-trips convert to vehicle-trips
 * via vehicle_classes.json's avgOccupancyPersons (A-10, per mode share —
 * never seat capacity), plus freightVehicleTrips (already vehicle-shaped,
 * inc2). Each tile's vehicle-trips attach to their nearest ROAD segment
 * (origin) and route via Dijkstra (AC-2 free-flow weights) to the nearest
 * segment adjacent to a job-bearing tile (destination, D2's single rule).
 * Unroutable demand (no origin segment reachable, no destination exists, or
 * no path in the graph) is reported in `unrouted`, never silently dropped.
 */
const assignmentOf: (s: SimState) => AssignmentResult = memoOnState((s) => {
  const adjacency = segmentAdjacencyOf(s);
  const freeFlow = segmentFreeFlowMinutesOf(s);
  const nearestSeg = nearestRoadSegmentTileMapOf(s);
  const destSet = jobAdjacentRoadSegmentsOf(s);
  const demandTiles = demandForecastOf(s);
  const shares = modeShareOf(ladderPointOf(s));
  const freightByClassByTile = freightVehicleTripsByClassOf(s); // FEAT-2326609800 inc7 AC-1

  const assignedFlow = new Map<string, number>();
  const assignedFlowByClass = new Map<string, Partial<Record<VehicleClassId, number>>>();
  const unrouted: UnroutedDemand[] = [];
  const tilePaths = new Map<string, string[]>();
  const tileVehicleTrips = new Map<string, number>();
  const pathCache = new Map<string, string[] | null>(); // originSegId -> path (per this call)

  const accumulate = (m: Map<string, number>, key: string, v: number): void => {
    m.set(key, (m.get(key) ?? 0) + v);
  };
  const accumulateClass = (segId: string, classId: VehicleClassId, v: number): void => {
    let row = assignedFlowByClass.get(segId);
    if (!row) {
      row = {};
      assignedFlowByClass.set(segId, row);
    }
    row[classId] = (row[classId] ?? 0) + v;
  };

  for (const t of demandTiles) {
    // FEAT-2326609800 inc7 (AC-1): tag each mode's/class's vehicle-trips
    // contribution BEFORE summing into the blended roadVehicleTrips scalar —
    // the per-class byTileClass map below is kept in exact lock-step with
    // roadVehicleTrips (every term added to one is added to the other), so
    // Σ_class byTileClass[class] === roadVehicleTrips by construction.
    let roadVehicleTrips = 0;
    const byTileClass: Partial<Record<VehicleClassId, number>> = {};
    for (const modeId of ROAD_PERSON_MODE_IDS) {
      const share = shares[modeId] ?? 0;
      if (share <= 0) continue;
      const occ = occupancyForMode(modeId);
      if (occ > 0) {
        const v = (t.personTrips * share) / occ;
        roadVehicleTrips += v;
        if (v > 0) {
          const classId = modeId as VehicleClassId;
          byTileClass[classId] = (byTileClass[classId] ?? 0) + v;
        }
      }
    }
    const freightByClass = freightByClassByTile.get(`${t.x},${t.y}`);
    if (freightByClass) {
      for (const [classId, v] of Object.entries(freightByClass)) {
        if (!v) continue;
        byTileClass[classId as VehicleClassId] = (byTileClass[classId as VehicleClassId] ?? 0) + v;
      }
    }
    roadVehicleTrips += t.freightVehicleTrips;
    if (roadVehicleTrips <= 0) continue;

    const tileKey = `${t.x},${t.y}`;
    const originSegId = nearestSeg.get(tileKey);
    if (!originSegId) {
      unrouted.push({ x: t.x, y: t.y, vehicleTrips: roadVehicleTrips, reason: 'no-origin-segment' });
      continue;
    }
    if (destSet.size === 0) {
      unrouted.push({ x: t.x, y: t.y, vehicleTrips: roadVehicleTrips, reason: 'no-destination' });
      continue;
    }
    let path = pathCache.get(originSegId);
    if (path === undefined) {
      path = dijkstraPath(originSegId, destSet, adjacency, freeFlow);
      pathCache.set(originSegId, path);
    }
    if (!path) {
      unrouted.push({ x: t.x, y: t.y, vehicleTrips: roadVehicleTrips, reason: 'no-path' });
      continue;
    }
    // BUG-912 perf fix: Object.entries(byTileClass) allocated a fresh array
    // of [key,value] pairs for EVERY segment of EVERY tile's path — with
    // ~4,900 demand tiles and multi-segment paths that was millions of
    // short-lived allocations per tick (measured 7.8x-10.1x reducer cost).
    // Hoist it to ONCE PER TILE, before the per-segment loop, since
    // byTileClass itself does not change while walking the path.
    const byTileClassEntries = Object.entries(byTileClass) as Array<[VehicleClassId, number]>;
    for (const segId of path) {
      accumulate(assignedFlow, segId, roadVehicleTrips);
      for (const [classId, v] of byTileClassEntries) {
        if (v) accumulateClass(segId, classId, v);
      }
    }
    tilePaths.set(tileKey, path);
    tileVehicleTrips.set(tileKey, roadVehicleTrips);
  }
  return { assignedFlow, assignedFlowByClass, unrouted, tilePaths, tileVehicleTrips };
});

export const assignedFlowOf: (s: SimState) => Map<string, number> = (s) => assignmentOf(s).assignedFlow;
/** FEAT-2326609800 inc7 (AC-1) — additive export of assignmentOf's own
 * per-class tagging (`:606-620` above), same shape as the tilePathsOf/
 * tileVehicleTripsOf inc5 precedent: `Σ_class assignedFlowByClassOf(s).get(seg)[class]`
 * equals `assignedFlowOf(s).get(seg)` for every segment carrying flow, to
 * within ordinary floating-point summation-order rounding (BUG-917(c),
 * corrected 2026-09-11: the two are NOT bit-identical — the blended scalar
 * accumulates one term per path segment while the per-class map accumulates
 * up to seven, so the summation orders differ; measured divergence ~1e-16
 * relative, well inside a `4 * Number.EPSILON` tolerance, harmless in
 * magnitude but not "exact" or "by construction" as this comment previously
 * claimed). */
export const assignedFlowByClassOf: (s: SimState) => Map<string, Partial<Record<VehicleClassId, number>>> = (s) =>
  assignmentOf(s).assignedFlowByClass;
export const unroutedDemandOf: (s: SimState) => UnroutedDemand[] = (s) => assignmentOf(s).unrouted;

// FEAT-2326609798 inc5 (AC-2, ASM-1520) — additive exports of assignmentOf's
// already-computed per-tile path data (`:606-607` above), same shape as the
// weightedPercentile precedent (BUG-857): GR#3 forbids the downstream inc5
// consumer module (gridlock-share derivation) re-deriving what assignmentOf
// already computed. Not re-exported as a new interface — the existing
// internal Map shapes are exposed as-is; no change to assignmentOf's own
// return value.
export const tilePathsOf: (s: SimState) => Map<string, string[]> = (s) => assignmentOf(s).tilePaths;
export const tileVehicleTripsOf: (s: SimState) => Map<string, number> = (s) => assignmentOf(s).tileVehicleTrips;

// --- AC-4: segmentDelayOf ----------------------------------------------------

export interface SegmentDelay {
  t0: number;
  t: number;
  v: number;
  c: number;
  vOverC: number;
}

/**
 * AC-4 — BPR delay + v/c per segment carrying assigned flow > 0 (zero-flow
 * segments are OMITTED — honest absence, never a fabricated vOverC: 0).
 * BUG-854 fix: `v` is assignedFlow (vehicle-trips/DAY) times the CURRENT
 * scale-ladder rung's `peakHourFactor` (see peakHourFactorOf's own doc
 * comment) — never divided by baseCommuteHours (ASM-1507, superseded).
 */
export const segmentDelayOf: (s: SimState) => Map<string, SegmentDelay> = memoOnState((s) => {
  const flow = assignedFlowOf(s);
  const freeFlow = segmentFreeFlowMinutesOf(s);
  const idx = lineSegmentIndexOf(s);
  const peakHourFactor = peakHourFactorOf(ladderPointOf(s));
  const out = new Map<string, SegmentDelay>();
  for (const [segId, assigned] of flow) {
    if (assigned <= 0) continue;
    const seg = idx.segmentById.get(segId);
    if (!seg) continue;
    const t0 = freeFlow.get(segId) ?? 0;
    let c: number;
    let alpha: number;
    let beta: number;
    if (seg.kind === 'road') {
      const roadClassId = roadClassIdOfSegment(seg);
      const lanes = roadClassRow(roadClassId).lanes;
      const perLane = linkCapacityRow(roadClassId).capacityPcuPerLanePerHour;
      c = perLane * lanes;
      const bpr = bprParamsFor(roadClassId);
      alpha = bpr.alpha;
      beta = bpr.beta;
    } else {
      // Rail/hs1: coarse capacity proxy (trainsPerHourMax alone, no per-train
      // passenger figure cross-multiplied here — out of scope §6, "full rail
      // passenger assignment ... not modelled"), network-default BPR params.
      const rail = railClassRow(seg.spec);
      c = rail.trainsPerHourMax;
      alpha = TRAFFIC.bprAlpha;
      beta = TRAFFIC.bprBeta;
    }
    const v = assigned * peakHourFactor;
    const vOverC = c > 0 ? v / c : 0;
    const t = t0 * (1 + alpha * Math.pow(vOverC, beta));
    out.set(segId, { t0, t, v, c, vOverC });
  }
  return out;
});

// --- AC-5: commuteTimeDistributionOf ----------------------------------------

export interface CommuteTimeDistribution {
  medianMinutes: number;
  p90Minutes: number;
}

/**
 * BUG-855 fix — weighted percentile that actually uses `weights`: AC-5
 * requires the commute median/p90 be aggregated across every routed tile
 * "weighted by that tile's personTrips, not by tile count" (a 5-resident
 * tile must count far less than a 50,000-resident tile). The pre-fix
 * version built a `cumFrac` array and never read it — `targetRank` was
 * computed purely from `n` (an unweighted, per-TILE rank), so the weights
 * only ever gated the `totalWeight <= 0` fallback. PROVEN unpinned (BUG-855):
 * mutating every weight to a constant 1 left the AC-5 suite green because
 * its fixture happened to use equal weights.
 *
 * Fix: interpolate on the CUMULATIVE WEIGHT FRACTION. `cumFrac[i]` is the
 * fraction of total weight at or below rank i (0-indexed values already
 * sorted ascending). We want the smallest value v such that the weight
 * mass at/below v is >= p (linear interpolation between the two straddling
 * ranks, mirroring the doc's own "rank = (n-1)*p" linear-interpolation
 * shape but keyed on cumulative WEIGHT position instead of tile count) —
 * for equal weights this collapses exactly to the pre-fix formula (matches
 * AC-5's doc worked example: 9 equal-weight tiles at minutes 1..9 give
 * p90 ~= 8.2), and for unequal weights the heavy tile dominates, per AC-5.
 */
/** FEAT-2326609797 inc4 (BUG-857 precedent): exported additively so
 * emergencyResponse.ts's emergencyCoverageOf can reuse the SAME weighted-
 * percentile formula this module's own commuteTimeDistributionOf (AC-5)
 * already uses for p50/p90, instead of a second, independently-maintained
 * copy (GR#3). This is the ONE additive export inc4's brief allows on this
 * module; behaviour is unchanged for every existing caller. */
export function weightedPercentile(values: number[], weights: number[], p: number): number {
  const n = values.length;
  if (n === 0) return 0;
  if (n === 1) return values[0];
  const totalWeight = weights.reduce((a, b) => a + b, 0);
  if (totalWeight <= 0) return values[Math.floor((n - 1) * p)];
  // Cumulative weight (running sum, INCLUSIVE of rank i, 0-indexed) —
  // `cum[i]` is total weight at ranks [0..i].
  const cum: number[] = [];
  let running = 0;
  for (const w of weights) {
    running += w;
    cum.push(running);
  }
  // Target cumulative-weight position. Generalises the unweighted linear
  // rank formula `(n-1)*p` (0-indexed target rank) to weighted ranks: for
  // EQUAL weights (w_i = totalWeight/n), `target` reduces to exactly
  // `totalWeight * ((n-1)*p + 1) / n`, which selects the identical
  // lo/hi/frac as the unweighted formula and reproduces the doc's own
  // worked example (9 equal-weight tiles, minutes 1..9: p50 -> 5 exactly,
  // p90 -> 8.2 exactly — verified by hand and by the AC-5 pin). For
  // UNEQUAL weights, a heavy-weight tile pulls `target` (and therefore the
  // interpolated rank) toward itself — the AC-5 requirement this function
  // existed to satisfy but the pre-fix `cumFrac`-computed-then-ignored
  // version never delivered (BUG-855).
  const target = (totalWeight * ((n - 1) * p + 1)) / n;
  let lo = -1;
  for (let i = 0; i < n; i++) {
    if (cum[i] < target) lo = i;
    else break;
  }
  const hi = Math.min(n - 1, lo + 1);
  const cumLo = lo >= 0 ? cum[lo] : 0;
  const cumHi = cum[hi];
  const span = cumHi - cumLo;
  const frac = span > 0 ? (target - cumLo) / span : 0;
  const loVal = lo >= 0 ? values[lo] : values[0];
  return loVal + frac * (values[hi] - loVal);
}

/**
 * AC-5 — door-to-door commute minutes per ROUTED demand tile = baseAccess +
 * Sigma segmentDelayOf(t) along the assigned path + baseCommuteMinutes's
 * wait/transfer placeholder component, aggregated to a city-wide median/p90
 * weighted by each tile's personTrips (a dense tile counts more than a
 * sparse one).
 */
export const commuteTimeDistributionOf: (s: SimState) => CommuteTimeDistribution = memoOnState((s) => {
  const { tilePaths } = assignmentOf(s);
  const delay = segmentDelayOf(s);
  const demandTiles = demandForecastOf(s);
  const personTripsByTile = new Map<string, number>();
  for (const t of demandTiles) personTripsByTile.set(`${t.x},${t.y}`, t.personTrips);

  const rows: Array<{ minutes: number; weight: number }> = [];
  const sortedTileKeys = [...tilePaths.keys()].sort();
  for (const tileKey of sortedTileKeys) {
    const path = tilePaths.get(tileKey)!;
    let minutes = TRAFFIC.baseAccessMinutes + TRAFFIC.baseCommuteMinutes;
    for (const segId of path) {
      const d = delay.get(segId);
      if (d) minutes += d.t;
    }
    const weight = personTripsByTile.get(tileKey) ?? 0;
    rows.push({ minutes, weight });
  }
  rows.sort((a, b) => a.minutes - b.minutes);
  const values = rows.map((r) => r.minutes);
  const weights = rows.map((r) => r.weight);
  return {
    medianMinutes: weightedPercentile(values, weights, 0.5),
    p90Minutes: weightedPercentile(values, weights, 0.9),
  };
});

// --- AC-6: gridlockedSegmentsOf ----------------------------------------------

export interface GridlockResult {
  ticks: Record<string, number>;
  gridlocked: string[];
}

/**
 * AC-6 — segment-granularity gridlock, reusing CONGESTION_CONSTANTS
 * (imported, never restated — D3). Mirrors the class-level congestion
 * tracker's own accrual/reset rule exactly, at segment scope instead of
 * class scope. Does NOT write to the class-level ticks field owned by that
 * OTHER, income-coupled tracker — this is a pure function over the caller's
 * OWN prior-ticks record, matching that tracker's own non-memoised
 * signature (AC-6 permits a derived read-out; no new SimState field added).
 */
export function gridlockedSegmentsOf(s: SimState, prevGridlockTicks: Record<string, number>): GridlockResult {
  const { CONGESTION_PENALTY_THRESHOLD, CONGESTION_SUSTAINED_TICKS } = CONGESTION_CONSTANTS;
  const delay = segmentDelayOf(s);
  const consideredIds = new Set<string>([...delay.keys(), ...Object.keys(prevGridlockTicks)]);
  const sortedIds = [...consideredIds].sort();
  // BUG-973 (r3 ACCEPT, non-blocking) -> folded into the r4 port: this is the
  // LIVE producer of gridlockTicksBySegment, so it gets the same own-key
  // discipline as the sanitizers -- a bare `prevGridlockTicks[segId]` is an
  // INHERITED read on a plain object (would silently return a stale
  // Object.prototype method for a hazardous segId instead of `undefined`),
  // guarded via hasOwnProperty; the output map is built via
  // Object.fromEntries over validated entries, never bracket assignment
  // (own-data-property semantics, consistent with every other sanitizer/
  // live-producer pair after the r4 ruling).
  const tickEntries: Array<[string, number]> = [];
  const gridlocked: string[] = [];
  for (const segId of sortedIds) {
    const d = delay.get(segId);
    const vOverC = d ? d.vOverC : 0;
    const prev = Object.prototype.hasOwnProperty.call(prevGridlockTicks, segId) ? prevGridlockTicks[segId] : 0;
    const next = vOverC >= CONGESTION_PENALTY_THRESHOLD ? Math.min(prev + 1, CONGESTION_SUSTAINED_TICKS) : 0;
    if (next > 0) tickEntries.push([segId, next]);
    if (next >= CONGESTION_SUSTAINED_TICKS) gridlocked.push(segId);
  }
  return { ticks: Object.fromEntries(tickEntries), gridlocked };
}

// --- AC-9: structural scale bound helper (test-only export) ----------------

/** AC-9 — total possible Dijkstra relaxations bound: originTileCount *
 * segmentCount. Test-only export, no production caller. */
export function __structuralDijkstraBoundForTest(s: SimState): number {
  const originTileCount = demandForecastOf(s).length;
  const segmentCount = lineSegmentIndexOf(s).segments.length;
  return originTileCount * segmentCount;
}

// ═══════════════════════════════════════════════════════════════════════════
// FEAT-2326609800 inc7 "TAX, WEAR AND REPAIR"
// (docs/planning/acceptance/FEAT-2326609792-inc7.md AC-2..AC-8).
// ═══════════════════════════════════════════════════════════════════════════

/**
 * AC-2/AC-4 basis — road-only segment length in km, reusing the EXACT same
 * tile-count x webconsoleMetresPerTile basis segmentFreeFlowMinutesFor
 * already uses for its metres figure (AC-2, inc3) — never re-derived. Rail/
 * hs1 segments are excluded (out of scope for vehicle-km/wear, road-only).
 */
export const segmentKmOf: (s: SimState) => Map<string, number> = memoOnState((s) => {
  const idx = lineSegmentIndexOf(s);
  const out = new Map<string, number>();
  for (const seg of idx.segments) {
    if (seg.kind !== 'road') continue;
    out.set(seg.segmentId, (seg.tiles * TRAFFIC.webconsoleMetresPerTile) / 1000);
  }
  return out;
});

/**
 * AC-2 basis — real per-class vehicle-km this city's routed road network
 * carries today: Σ over every segment carrying that class's flow of
 * (assignedFlowByClassOf x segmentKm). This is the REAL routed figure (not
 * the ladder's population-proxy fuelLitresDemandedPerDay) — the diverging-
 * fixture requirement AC-2's Check calls for.
 */
export const cityVehicleKmByClassOf: (s: SimState) => Partial<Record<VehicleClassId, number>> = memoOnState((s) => {
  const byClass = assignedFlowByClassOf(s);
  const km = segmentKmOf(s);
  const out: Partial<Record<VehicleClassId, number>> = {};
  const sortedSegIds = [...byClass.keys()].sort();
  for (const segId of sortedSegIds) {
    const segKm = km.get(segId);
    if (!segKm) continue;
    const classes = byClass.get(segId)!;
    for (const [classId, flow] of Object.entries(classes)) {
      if (!flow) continue;
      const id = classId as VehicleClassId;
      out[id] = (out[id] ?? 0) + flow * segKm;
    }
  }
  return out;
});

/**
 * AC-2 — Fuel Duty basis: total litres/day demanded across every class that
 * carries a real vehicle_classes.json fuelLitresPerKm figure (excludes bus,
 * see fuelLitresPerKmById's doc — a genuine data gap, not a fabricated
 * figure). ASM-1531: fuel litres taxed at 100% fossil rate this increment,
 * EV share not netted out (inc6's job).
 */
export const fuelLitresDemandedOf: (s: SimState) => number = memoOnState((s) => {
  const km = cityVehicleKmByClassOf(s);
  let total = 0;
  for (const id of FUEL_DUTY_CLASS_IDS) {
    const classKm = km[id] ?? 0;
    if (classKm <= 0) continue;
    total += classKm * fuelLitresPerKmFor(id);
  }
  return total;
});

/**
 * AC-1 (per-class flow prerequisite, city-wide TOTAL not per-segment) — each
 * class's total daily vehicle-trips GENERATED city-wide (person modes via
 * modeShareOf/occupancyForMode, freight via freightVehicleTripsByClassOf),
 * summed over every demand tile. Deliberately NOT derived from
 * assignedFlowByClassOf's per-SEGMENT routed flow — a trip that traverses N
 * segments would otherwise be counted N times, which would silently inflate
 * AC-3's vehicles-owned basis by the average path length. This is the trip-
 * GENERATION total, the correct basis for "how many vehicles of this class
 * does the city's daily activity imply" (D1).
 */
export const cityVehicleTripsByClassOf: (s: SimState) => Partial<Record<VehicleClassId, number>> = memoOnState((s) => {
  const shares = modeShareOf(ladderPointOf(s));
  const freightByTile = freightVehicleTripsByClassOf(s);
  const out: Partial<Record<VehicleClassId, number>> = {};
  for (const t of demandForecastOf(s)) {
    for (const modeId of ROAD_PERSON_MODE_IDS) {
      const share = shares[modeId] ?? 0;
      if (share <= 0) continue;
      const occ = occupancyForMode(modeId);
      if (occ <= 0) continue;
      const v = (t.personTrips * share) / occ;
      if (v > 0) {
        const id = modeId as VehicleClassId;
        out[id] = (out[id] ?? 0) + v;
      }
    }
  }
  const sortedTileKeys = [...freightByTile.keys()].sort();
  for (const tileKey of sortedTileKeys) {
    const byClass = freightByTile.get(tileKey)!;
    for (const [classId, v] of Object.entries(byClass)) {
      if (!v) continue;
      const id = classId as VehicleClassId;
      out[id] = (out[id] ?? 0) + v;
    }
  }
  return out;
});

/**
 * AC-3 (D1) — implied vehicles-owned per class: that class's total daily
 * vehicle-trips ÷ trip_generation.json's new tripsPerVehiclePerDay figure
 * for that class. Only the 6 classes with a real taxation.json
 * fleetAverageByVehicleClass row are computed (excludes bus — see
 * VED_CLASS_IDS's doc).
 */
export const vehiclesOwnedByClassOf: (s: SimState) => Partial<Record<VehicleClassId, number>> = memoOnState((s) => {
  const trips = cityVehicleTripsByClassOf(s);
  const out: Partial<Record<VehicleClassId, number>> = {};
  for (const id of VED_CLASS_IDS) {
    const t = trips[id] ?? 0;
    if (t <= 0) continue;
    out[id] = t / tripsPerVehiclePerDayFor(id);
  }
  return out;
});

/**
 * AC-3 — total annual VED (GBP/year, NOT yet divided by TICKS_PER_YEAR — the
 * calendar conversion is engine.ts's own TICKS_PER_YEAR constant, this
 * module has no calendar constant, GR#3): Σ_class vehiclesOwnedByClassOf x
 * taxation.json fleetAverageByVehicleClass[class].gbpPerYear.
 */
export const vedAnnualGbpOf: (s: SimState) => number = memoOnState((s) => {
  const owned = vehiclesOwnedByClassOf(s);
  let total = 0;
  for (const id of VED_CLASS_IDS) {
    const n = owned[id] ?? 0;
    if (n <= 0) continue;
    total += n * vedGbpPerYearFor(id);
  }
  return total;
});

// --- AC-5: condition index + repair-cost curve ------------------------------

/**
 * AC-5 — conditionIndex from cumulative ESAL wear: road_wear.json's
 * conditionDecayPerESAL points lost per cumulative ESAL unit, clamped to
 * [0,100]. This is the ESAL-driven component ONLY — additive to (never
 * replacing) roads.json's own age-based conditionDecayPerMonth, which no
 * consumer wires yet anywhere in the TS engine (confirmed by inspection,
 * doc §2) and stays out of this increment's scope.
 */
export function conditionIndexOf(wear: number): number {
  const w = Number.isFinite(wear) && wear > 0 ? wear : 0;
  return Math.max(0, Math.min(100, 100 - w * CONDITION_DECAY_PER_ESAL));
}

/**
 * AC-5 — piecewise-linear interpolation of road_wear.json's repairCostCurve
 * (6 anchor points, conditionIndex 100..0 descending). Never re-derived via
 * an independent formula (AC-8's mutant: a Math.pow(loadRatio,4) rebuild
 * would silently drift from this file's own calibrated shape).
 */
export function repairCostMultiplierOf(conditionIndex: number): number {
  const ci = Math.max(0, Math.min(100, conditionIndex));
  for (let i = 0; i < REPAIR_COST_CURVE.length - 1; i++) {
    const hi = REPAIR_COST_CURVE[i];
    const lo = REPAIR_COST_CURVE[i + 1];
    if (ci <= hi.conditionIndex && ci >= lo.conditionIndex) {
      const span = hi.conditionIndex - lo.conditionIndex;
      const frac = span > 0 ? (hi.conditionIndex - ci) / span : 0;
      return hi.repairCostMultiplier + frac * (lo.repairCostMultiplier - hi.repairCostMultiplier);
    }
  }
  return REPAIR_COST_CURVE[REPAIR_COST_CURVE.length - 1].repairCostMultiplier;
}

// --- AC-4/AC-5/AC-6: advanceRoadWear / roadWearStepOf ----------------------

export interface RoadRepairEvent {
  segmentId: string;
  roadClassId: string;
  /** conditionIndex AT THE MOMENT OF REPAIR (before the reset to 0) — the
   * multiplier is priced off the segment's degraded state, per AC-5. */
  conditionIndexBeforeRepair: number;
  multiplier: number;
}

export interface RoadWearStep {
  /** Next tick's s.roadWearBySegment — self-pruning (zero/repaired entries omitted, mirrors sanitizeRoadWearBySegment's idiom). */
  nextWearBySegment: Record<string, number>;
  /** Every segment repaired (paid) THIS tick — empty on a tick where nothing crosses the trigger. engine.ts folds these into the 'Roads' upkeep bucket exactly once (AC-5). */
  repairEvents: RoadRepairEvent[];
  /** BUG-915: the SANITIZED wear-before-any-decision map, exposed so engine.ts's
   * payment gate can fall back to "wear persists" for a DEFERRED (unaffordable)
   * repair event without re-importing/re-running sanitizeRoadWearBySegment itself
   * (this module is the sole computer of the wear step; engine.ts only decides
   * whether the already-computed reset is actually committed). */
  prevWearBySegment: Record<string, number>;
}

/** One cadence-refreshed wear-accrual input per CURRENT road segment (BUG-929, FEAT-2326609800 inc7 r3). */
export interface WearSegmentInput {
  roadClassId: string;
  /** This segment's ESAL delta for ONE tick at the flow this cadence window observed (0 when the segment carries no flow). */
  deltaEsalPerTick: number;
}

/**
 * BUG-929 (lead amendment r3) — the EXPENSIVE half of the wear/repair
 * mechanic (assignedFlowByClassOf's Dijkstra-derived flow, segmentKmOf,
 * lineSegmentIndexOf), computed ONCE per traffic cadence tick and cached
 * into s.trafficSnapshot.wearSegments by trafficWellbeing.ts's
 * computeTrafficSnapshot — never called from the per-tick money path
 * again. Keyed by EVERY current road segment (delta 0 when the segment
 * carries no flow this cadence window), so its own key set doubles as the
 * "which segments still exist" bound roadWearStepFromSnapshot uses for
 * orphan pruning (BUG-917(b)) without a live lineSegmentIndexOf call.
 */
export const wearSegmentInputsOf: (s: SimState) => Record<string, WearSegmentInput> = memoOnState((s) => {
  const flowByClass = assignedFlowByClassOf(s);
  const km = segmentKmOf(s);
  const idx = lineSegmentIndexOf(s);
  // r4 LEAD RULING (round-trip consistency, PLAIN not null-prototype): a
  // real segment id is geometry-derived and can never collide with an
  // inherited name, but keeping this live-compute counterpart's SHAPE
  // identical to the sanitizer's output means a fresh tick's
  // trafficSnapshot and the SAME snapshot after a save/decode/
  // structuredClone round trip through sanitizeTrafficSnapshot are
  // byte-identical (deepStrictEqual, which compares [[Prototype]]) rather
  // than differing only by accumulator shape — collect entries and finish
  // with Object.fromEntries (own-data-property semantics), no bracket
  // assignment.
  const entries: Array<[string, WearSegmentInput]> = [];
  for (const seg of idx.segments) {
    if (seg.kind !== 'road') continue;
    const roadClassId = roadClassIdOfSegment(seg);
    const classes = flowByClass.get(seg.segmentId);
    const segKm = km.get(seg.segmentId) ?? 0;
    let delta = 0;
    if (classes && segKm > 0) {
      for (const [classId, flow] of Object.entries(classes)) {
        if (!flow) continue;
        delta += (flow * segKm * esalFactorFor(classId)) / 100;
      }
    }
    entries.push([seg.segmentId, { roadClassId, deltaEsalPerTick: delta }]);
  }
  return Object.fromEntries(entries);
});

/**
 * AC-4/AC-5/AC-6 — ONE pure step of the wear/repair state machine, over
 * CACHED cadence inputs (never a live assignment call — BUG-929). Safe to
 * call from BOTH computeFlows (for repairEvents, folded into the 'Roads'
 * bucket THIS tick) and advance() (for nextWearBySegment, written to
 * next.roadWearBySegment): both call sites pass the SAME (prevWearRaw,
 * wearSegments) pair within the same tick, so a caller that memoises on
 * those two references (as engine.ts's roadRepairPaymentOf/roadWearOf do,
 * mirroring the old memoOnState(s) idiom) never disagrees or double-computes
 * (GR#21).
 *
 * Per segment (considering every segment the cadence snapshot carries plus
 * every segment with prior wear, so a segment that stops carrying flow while
 * still degraded is not silently forgotten):
 *   - If the PREVIOUS tick's conditionIndex is already below
 *     repairTriggerConditionIndex (60): a repair fires THIS tick — priced
 *     (in engine.ts, this module's own fiscal boundary forbids reading a
 *     currency-shaped road-class figure, AC-8) off that pre-repair
 *     conditionIndex via repairCostMultiplierOf, wear resets to 0 (AC-6), and NO
 *     further ESAL accrual happens this tick for that segment (it is freshly
 *     resurfaced, condition 100, before this tick's flow could re-degrade
 *     it — matches AC-6's Check: "the FRESH rate, not the pre-repair rate").
 *   - Otherwise: this segment's CACHED deltaEsalPerTick (the cadence
 *     window's own flow x segmentKm x esalFactorPer100VehicleKm/100,
 *     reapplied every tick until the next cadence refresh — the same
 *     cadence-lag approximation gridlockTicksBySegment already uses)
 *     accrues onto the carried-forward wear.
 *
 * AC-6's false-pass guard (never reset on a mere READ): this function is
 * PURE — it never mutates SimState. Only engine.ts's advance() (the sole
 * writer of s.roadWearBySegment, mirroring the sustained-congestion tick
 * counter's own doc) commits
 * `nextWearBySegment` into the next tick's state. A debugjson.ts read-out or
 * a test calling conditionIndexOf/repairCostMultiplierOf directly can never
 * trigger a reset — only advance()'s own commit can.
 */
export function roadWearStepFromSnapshot(
  prevWearRaw: unknown,
  wearSegments: Readonly<Record<string, WearSegmentInput>>,
): RoadWearStep {
  const prevWear = sanitizeRoadWearBySegment(prevWearRaw);
  // r4 LEAD RULING (port amendment): prevWear is a PLAIN object now (r3's
  // null-prototype maps broke the delta-protocol clone-side pin). Both
  // bracket assignment (`nextWear[segId] = ...`) AND Object.assign onto a
  // plain target invoke Object.prototype's inherited `__proto__` SETTER for
  // a segId literally named '__proto__' — r3's fix (Object.assign onto an
  // Object.create(null) target) sidestepped this by giving the target no
  // prototype at all, which is exactly the shape r4 rules out. A Map has no
  // such hazard for ANY key (string or not) regardless of the target's own
  // prototype, so nextWear is built and mutated as a Map throughout this
  // function and converted to a plain object via Object.fromEntries
  // (own-data-property semantics) only once, at the very end.
  const nextWearMap = new Map<string, number>(Object.entries(prevWear));
  const repairEvents: RoadRepairEvent[] = [];

  const consideredSegIds = new Set<string>([...Object.keys(wearSegments), ...Object.keys(prevWear)]);
  const sortedSegIds = [...consideredSegIds].sort(); // deterministic, GR#21 — no map-range-with-break

  for (const segId of sortedSegIds) {
    // BUG-961 (r3 LEAD RULING): own-key test, not a bare `prevWear[segId]`
    // read — a segId literally named 'toString'/'hasOwnProperty'/'valueOf'
    // on a PLAIN accumulator would silently resolve to the inherited
    // Object.prototype method (truthy, so `?? 0` never catches it) instead
    // of the real numeric wear, corrupting the type the rest of this
    // function assumes. prevWear is null-prototype now so this can no
    // longer actually happen, but the own-key test is the correct shape
    // regardless of what future caller hands this function a raw map.
    const prevSegWear = Object.prototype.hasOwnProperty.call(prevWear, segId) ? prevWear[segId] : 0;
    const prevConditionIndex = conditionIndexOf(prevSegWear);
    const input = Object.prototype.hasOwnProperty.call(wearSegments, segId) ? wearSegments[segId] : undefined;

    if (prevConditionIndex < REPAIR_TRIGGER_CONDITION_INDEX) {
      if (input) {
        repairEvents.push({
          segmentId: segId,
          roadClassId: input.roadClassId,
          conditionIndexBeforeRepair: prevConditionIndex,
          multiplier: repairCostMultiplierOf(prevConditionIndex),
        });
      }
      nextWearMap.delete(segId); // AC-6: resurfaced this tick, wear resets to 0 (self-pruning).
      continue;
    }

    if (!input) continue; // segment no longer in the cadence snapshot and not yet due for repair — wear unchanged.
    if (input.deltaEsalPerTick > 0) nextWearMap.set(segId, prevSegWear + input.deltaEsalPerTick);
  }

  // BUG-917(b): orphan-wear growth bound. A segment id is geometry-derived
  // (churns whenever roads are laid/demolished), so a wear entry whose
  // segment no longer exists in the CURRENT cadence snapshot can never
  // again carry flow or reach the repair trigger — it would otherwise
  // survive in the save forever (unbounded growth, proven by
  // attack-feat800-round.test.mjs's STATE_GROWTH case before this fix).
  // Dropping it here is safe and deterministic: wearSegments is refreshed
  // from the same cadence-gated pure computation every replay reproduces
  // identically, so every replay drops the exact same keys on the exact
  // same cadence-boundary tick (a bulldozed segment's wear entry survives
  // at most one cadence window before being dropped, never forever).
  for (const segId of [...nextWearMap.keys()]) {
    // BUG-961: `in` walks the prototype chain (it would have wrongly
    // reported true for e.g. segId 'toString' against a plain
    // `wearSegments` object even with no such own entry) — the own-key test
    // via hasOwnProperty is the own-key-only equivalent and stays correct
    // regardless of wearSegments' own prototype shape.
    if (!Object.prototype.hasOwnProperty.call(wearSegments, segId)) nextWearMap.delete(segId);
  }

  return { nextWearBySegment: Object.fromEntries(nextWearMap), repairEvents, prevWearBySegment: prevWear };
}

/**
 * BUG-929 bootstrap/test-compatibility wrapper — when `s.trafficSnapshot`
 * already carries a cadence-refreshed `wearSegments` map, reads it
 * (zero live assignment work, the money-path contract). When absent (a
 * fresh city, an old save, or a hand-built test fixture that never ran a
 * real advance() tick), computes `wearSegmentInputsOf(s)` fresh exactly
 * once — the SAME bootstrap rule trafficWellbeing.ts's cadence fields
 * already use, so every existing direct caller (trafficWear.test.mjs)
 * keeps its exact prior behaviour on a snapshot-less state.
 */
export const roadWearStepOf: (s: SimState) => RoadWearStep = memoOnState((s) => {
  const wearSegments = s.trafficSnapshot?.wearSegments ?? wearSegmentInputsOf(s);
  return roadWearStepFromSnapshot(s.roadWearBySegment, wearSegments);
});
