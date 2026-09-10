// FEAT-2326609799 inc6 "PARKING, FUEL AND EV CHARGING" —
// docs/planning/acceptance/FEAT-2326609792-inc6.md (AC-1..AC-8, §4 D1-D3,
// ASM-1524..1526).
//
// This module does for parking/fuel/EV demand exactly what inc2's
// trafficDemand.ts did for trip demand: bottom-up, per-tile figures derived
// from the ACTUAL built city (demandForecastOf's real occupancy), never a
// ladder-rung total redistributed down. scale_ladder.json's own
// parkingSpacesDemanded/fuelLitresDemandedPerDay/evKWhDemandedPerDay are
// population-rung AGGREGATES from a hypothetical average city shape — used
// here only as a directional cross-check in tests (§2), never a
// reconciliation target; forcing the two numbers equal would be a second,
// competing model of the same quantity (GR#3 violation in the other
// direction). evChargePointsNeeded is the one exception (§2/AC-6): no
// bottom-up charge-point throughput table exists, so the ladder IS the data
// source there.
//
// Decision D1 (doc §5): off-street parking supply and EV charge-point
// supply are fixed at the literal 0 this increment — no placeable
// parking-structure/EV-charger spec exists yet. Decision D2: EV share reads
// data/fuel.json's 'early' era as a fixed default (webconsole has no
// era/tick-count concept). Decision D3: fuelAndEVDemandOf's evKWhPerDay is
// a genuine electricity demand not yet wired into powerStats — diagnostic
// export only, per the doc.
//
// AC-7 fiscal/wellbeing boundary: this module is a READ-ONLY diagnostic
// layer — no s.budget/treasury/*Pounds/*Revenue/*Cost field, no
// happiness/wellbeing-named identifier (that coupling is a future
// increment's job, mirroring inc3 AC-8 / inc4 AC-7). No MapView.tsx change.
//
// PURE + DETERMINISTIC (GR#21): every exported derivation is memoOnState
// over SimState — no wall-clock read, no PRNG, no browser-storage read.
// AC-8 cost bound: one O(tiles) pass for parking demand/supply/shortfall,
// one O(tiles) pass for vehicle-km, O(1) lookups for fuel/EV split and
// charge-point shortfall — this module never imports trafficAssignment.ts's
// Dijkstra/adjacency/segment-graph exports (segmentAdjacencyOf,
// assignedFlowOf, tilePathsOf, segmentDelayOf, lineSegmentIndexOf, etc.) —
// it does not need a second segment-graph traversal.

import type { SimState } from './types.ts';
import { SPECS, isOnline, densityTier, memoOnState } from './data.ts';
import { demandForecastOf, ladderPointOf, modeShareOf, numericField, numericFieldOrZero } from './trafficDemand.ts';
import { ROAD_CLASS_ID_OF_TIER, occupancyForMode } from './trafficAssignment.ts';

// data files this module reads (GR#15 — every constant below is sourced,
// never hand-typed; this module owns no field in any of these tables — all
// reads, no writes).
import rawParking from './traffic-data/parking.json' with { type: 'json' };
import rawRoads from './traffic-data/roads.json' with { type: 'json' };
import rawVehicleClasses from './traffic-data/vehicle_classes.json' with { type: 'json' };
import rawTraffic from './traffic-data/traffic.json' with { type: 'json' };
import rawFuel from './traffic-data/fuel.json' with { type: 'json' };

// --- Registry error codes (GR#7) --------------------------------------------
// Claimed via `node tools/plan/add-error.js claim-range ui.webconsole --size 5`
// (V930-V934).
export const ERR_KERB_SPACE_LENGTH_INVALID = 'MET-V930'; // ParkingFuelKerbSpaceLengthInvalid
export const ERR_FUEL_ERA_OR_EVSHARE_MISSING = 'MET-V931'; // ParkingFuelEraOrEVShareMissing
export const ERR_DENSITY_BAND_MISSING = 'MET-V932'; // ParkingFuelDensityBandMissing
export const ERR_METRES_PER_TILE_MISSING = 'MET-V933'; // ParkingFuelMetresPerTileMissing
export const ERR_VEHICLE_CLASS_RATE_MISSING = 'MET-V934'; // ParkingFuelVehicleClassRateMissing

function registryError(code: string, message: string): Error {
  return new Error(`${code}: ${message}`);
}

// --- data/traffic/parking.json typed view -----------------------------------

interface ParkingDemandRow {
  spacesPerDwelling?: number;
  spacesPerJob?: number;
  spacesPerTripEnd?: number;
}
interface DensityBandSplit {
  kerbShare: number;
  offStreetShare: number;
}
interface ParkingConfig {
  demandByLandUse: Record<string, ParkingDemandRow>;
  kerbVsOffStreet: {
    kerbSpaceLengthMetres: number;
    byDensityBand: Record<string, DensityBandSplit>;
  };
}

/**
 * BUG-865-style pure loader taking the raw JSON as an argument (same idiom
 * trafficAssignment.ts's loadTrafficConfigFrom uses) so the fail-closed
 * kerbSpaceLengthMetres read is directly testable with a scratch object.
 * ASM-1524/AC-8: kerbSpaceLengthMetres is a NEW parking.json field this
 * increment added — no hand-typed fallback (e.g. `?? 5.5`) is permitted; a
 * missing/non-positive/non-finite value is a registry error.
 */
export function loadParkingConfigFrom(raw: unknown): ParkingConfig {
  const j = raw as {
    demandByLandUse: Record<string, ParkingDemandRow>;
    kerbVsOffStreet: { kerbSpaceLengthMetres?: unknown; byDensityBand: Record<string, DensityBandSplit> };
  };
  const kerbSpaceLengthMetres = j.kerbVsOffStreet?.kerbSpaceLengthMetres;
  if (typeof kerbSpaceLengthMetres !== 'number' || !Number.isFinite(kerbSpaceLengthMetres) || kerbSpaceLengthMetres <= 0) {
    throw registryError(
      ERR_KERB_SPACE_LENGTH_INVALID,
      `data/traffic/parking.json kerbVsOffStreet.kerbSpaceLengthMetres must be a positive finite number, got ${JSON.stringify(kerbSpaceLengthMetres)}`,
    );
  }
  return {
    demandByLandUse: j.demandByLandUse,
    kerbVsOffStreet: { kerbSpaceLengthMetres, byDensityBand: j.kerbVsOffStreet.byDensityBand },
  };
}
const PARKING = loadParkingConfigFrom(rawParking);

// --- data/roads.json typed view (kerb-eligibility flag only) ---------------

interface RoadClassRow {
  id: string;
  parking: boolean;
}
const ROAD_PARKING_BY_ID = new Map<string, boolean>(
  (rawRoads as { classes: RoadClassRow[] }).classes.map((r) => [r.id, r.parking]),
);

// --- data/traffic/vehicle_classes.json typed view (fuel/kWh per km) --------

interface RoadVehicleRow {
  id: string;
  fuelLitresPerKm: number;
  kWhPerKm: number | null;
}
const VEHICLE_RATE_BY_ID = new Map<string, RoadVehicleRow>(
  (rawVehicleClasses as { roadVehicles: RoadVehicleRow[] }).roadVehicles.map((v) => [v.id, v]),
);

/**
 * BUG-900 rework: exported for the same reason as loadMetresPerTileFrom —
 * MET-V934's fail-closed branch (S9's fabricated fallback survivor) was
 * unreachable from any test while this stayed module-private.
 */
export function vehicleRateRow(id: string): RoadVehicleRow {
  const row = VEHICLE_RATE_BY_ID.get(id);
  if (!row) {
    throw registryError(
      ERR_VEHICLE_CLASS_RATE_MISSING,
      `data/traffic/vehicle_classes.json roadVehicles has no entry for vehicle class "${id}"`,
    );
  }
  return row;
}

// --- data/traffic.json typed view (metres per tile) -------------------------
// This module does its OWN fail-closed read (never trusts another module's
// already-parsed config, mirroring trafficAssignment.ts/trafficDemand.ts's
// own independent validation of the same file).

/**
 * BUG-900 rework (inc3 r3 BUG-865 class fix, repeated here): exported so the
 * MET-V933 fail-closed branch is directly testable with a scratch raw
 * object, the same idiom loadParkingConfigFrom/loadEarlyEraEVShareFrom
 * already use — this function was previously module-private and therefore
 * unreachable from any test, which is exactly how a fabricated `return 50`
 * fallback survived round 1 (S8).
 */
export function loadMetresPerTileFrom(raw: unknown): number {
  const v = (raw as { webconsoleMetresPerTile?: unknown }).webconsoleMetresPerTile;
  if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
    throw registryError(
      ERR_METRES_PER_TILE_MISSING,
      `data/traffic.json is missing a positive numeric webconsoleMetresPerTile field, got ${JSON.stringify(v)}`,
    );
  }
  return v;
}
const METRES_PER_TILE = loadMetresPerTileFrom(rawTraffic);

// --- data/fuel.json typed view (early-era EV share, D2/ASM-1526) -----------

interface FuelEra {
  era: string;
  carEVShare: number;
  vanEVShare: number;
  truckEVShare: number;
}

/**
 * Reads the 'early' era BY ID (`.find(e => e.era === 'early')`, never
 * `eras[0]` by index — AC-5's explicit instruction) and validates every EV
 * share field is a finite number in [0,1]. Fail-closed (GR#7): a missing
 * 'early' entry or a missing/invalid share field is a registry error, never
 * a hand-typed fallback.
 */
export function loadEarlyEraEVShareFrom(raw: unknown): { carEVShare: number; vanEVShare: number; truckEVShare: number } {
  const eras = (raw as { eras?: FuelEra[] }).eras ?? [];
  const early = eras.find((e) => e && e.era === 'early');
  if (!early) {
    throw registryError(ERR_FUEL_ERA_OR_EVSHARE_MISSING, "data/fuel.json is missing an eras entry with era 'early'");
  }
  for (const key of ['carEVShare', 'vanEVShare', 'truckEVShare'] as const) {
    const v = early[key];
    if (typeof v !== 'number' || !Number.isFinite(v) || v < 0 || v > 1) {
      throw registryError(
        ERR_FUEL_ERA_OR_EVSHARE_MISSING,
        `data/fuel.json eras 'early' entry has an invalid ${key}, got ${JSON.stringify(v)}`,
      );
    }
  }
  return { carEVShare: early.carEVShare, vanEVShare: early.vanEVShare, truckEVShare: early.truckEVShare };
}
const EARLY_EV_SHARE = loadEarlyEraEVShareFrom(rawFuel);

/** AC-5 — motorbike/taxi ride the 'car' EV share (no separate fuel.json
 * entry exists for them); cargo_van reads vanEVShare; rigid_truck/
 * articulated_truck read truckEVShare. */
const EV_SHARE_BY_CLASS: Readonly<Record<string, number>> = Object.freeze({
  car: EARLY_EV_SHARE.carEVShare,
  motorbike: EARLY_EV_SHARE.carEVShare,
  taxi: EARLY_EV_SHARE.carEVShare,
  cargo_van: EARLY_EV_SHARE.vanEVShare,
  rigid_truck: EARLY_EV_SHARE.truckEVShare,
  articulated_truck: EARLY_EV_SHARE.truckEVShare,
});

// --- density-band lookup (§2 — "lower component of the rung's transition
// string", the SAME reading emergencyResponse.ts's isRuralDensityBand
// established for the boolean-rural case; this module needs the full band
// id, not just a rural/urban boolean, so it is its own small local reader
// rather than a re-export — GR#3's "small local plumbing" exception, same
// shape as that module's own nearest-segment wrapper). ---------------------

interface LadderPointLike {
  nonNumeric: Array<{ key: string; rawValue: unknown }>;
}
/**
 * BUG-900 rework: exported for the same reason as loadMetresPerTileFrom —
 * MET-V932's fail-closed branch (S7's fabricated 'rural' fallback survivor)
 * was unreachable from any test while this stayed module-private.
 */
export function densityBandLowerOf(point: LadderPointLike): string {
  const f = point.nonNumeric.find((x) => x.key === 'densityBand');
  if (!f || typeof f.rawValue !== 'string') {
    throw registryError(
      ERR_DENSITY_BAND_MISSING,
      'the current scale-ladder point is missing a string densityBand non-numeric field',
    );
  }
  return f.rawValue.split('~')[0];
}
function kerbVsOffStreetSplitFor(point: LadderPointLike): DensityBandSplit {
  const band = densityBandLowerOf(point);
  const split = PARKING.kerbVsOffStreet.byDensityBand[band];
  if (!split) {
    throw registryError(
      ERR_DENSITY_BAND_MISSING,
      `data/traffic/parking.json kerbVsOffStreet.byDensityBand has no entry for density band "${band}"`,
    );
  }
  return split;
}

function clamp01(v: number): number {
  if (v < 0) return 0;
  if (v > 1) return 1;
  return v;
}

// --- AC-1: parkingDemandOf ---------------------------------------------------

export interface ParkingDemand {
  demanded: number;
  kerbSpaces: number;
  offStreetSpaces: number;
}

/**
 * AC-1 — per demand-generating tile (demandForecastOf's own set), classify
 * `SPECS[spec].kind` into a parking.json demandByLandUse rate and apply it
 * to the tile's ACTUAL residents/workers (never raw capacity — inherited
 * from demandForecastOf). residential uses densityTier(sp): tier 1 ->
 * dwelling_low_density, tier 2/3 -> dwelling_high_density, applied to
 * residentsActual. office/commercial(retail)/industrial use their own
 * per-job/per-trip-end rate, applied to workersActual. Any other kind (e.g.
 * power, park, school, health — not enumerated in the doc's §2 mapping)
 * contributes 0, never NaN or a fallback rate. Split into kerb/off-street
 * shares via kerbVsOffStreet.byDensityBand[the CURRENT rung's density band].
 * O(tiles), one ladder call (AC-8).
 */
export const parkingDemandOf: (s: SimState) => Map<string, ParkingDemand> = memoOnState((s) => {
  const point = ladderPointOf(s);
  const split = kerbVsOffStreetSplitFor(point);
  const demandTiles = demandForecastOf(s);
  const out = new Map<string, ParkingDemand>();
  for (const t of demandTiles) {
    const sp = SPECS[t.spec];
    let demanded = 0;
    if (sp) {
      switch (sp.kind) {
        case 'residential': {
          const tier = densityTier(sp);
          const row = tier === 1 ? PARKING.demandByLandUse.dwelling_low_density : PARKING.demandByLandUse.dwelling_high_density;
          demanded = (row?.spacesPerDwelling ?? 0) * t.residentsActual;
          break;
        }
        case 'office':
          demanded = (PARKING.demandByLandUse.office_job?.spacesPerJob ?? 0) * t.workersActual;
          break;
        case 'commercial':
          demanded = (PARKING.demandByLandUse.retail_job?.spacesPerTripEnd ?? 0) * t.workersActual;
          break;
        case 'industrial':
          demanded = (PARKING.demandByLandUse.industrial_job?.spacesPerJob ?? 0) * t.workersActual;
          break;
        default:
          demanded = 0;
      }
    }
    out.set(`${t.x},${t.y}`, {
      demanded,
      kerbSpaces: demanded * split.kerbShare,
      offStreetSpaces: demanded * split.offStreetShare,
    });
  }
  return out;
});

// --- AC-2: kerbParkingSupplyOf -----------------------------------------------

const NEIGHBOUR_OFFSETS: ReadonlyArray<readonly [number, number]> = [
  [1, 0],
  [-1, 0],
  [0, 1],
  [0, -1],
];

/** Every tile occupied by a drivable road building whose data/roads.json
 * class has `parking === true` (BUG-843-class fix precedent: never count a
 * road tile regardless of its class's parking flag). Private, memoOnState —
 * not exported; AC-2's own export composes over it. */
const kerbEligibleRoadTilesOf: (s: SimState) => Set<string> = memoOnState((s) => {
  const out = new Set<string>();
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp || sp.roadTier == null || !isOnline(s, b)) continue;
    const classId = ROAD_CLASS_ID_OF_TIER[sp.roadTier];
    if (!classId || !ROAD_PARKING_BY_ID.get(classId)) continue;
    for (let dx = 0; dx < sp.w; dx++) {
      for (let dy = 0; dy < sp.h; dy++) out.add(`${b.x + dx},${b.y + dy}`);
    }
  }
  return out;
});

/**
 * AC-2 — kerb SUPPLY per demand tile: count of orthogonally-adjacent
 * parking-eligible road tiles, converted to a space count via
 * `(count x webconsoleMetresPerTile) / kerbVsOffStreet.kerbSpaceLengthMetres`
 * (ASM-1524's data-sourced space length — never a hand-typed metre figure).
 * O(tiles) (4 neighbour lookups per demand tile, AC-8).
 */
export const kerbParkingSupplyOf: (s: SimState) => Map<string, number> = memoOnState((s) => {
  const eligible = kerbEligibleRoadTilesOf(s);
  const demandTiles = demandForecastOf(s);
  const out = new Map<string, number>();
  for (const t of demandTiles) {
    let count = 0;
    for (const [dx, dy] of NEIGHBOUR_OFFSETS) {
      if (eligible.has(`${t.x + dx},${t.y + dy}`)) count++;
    }
    out.set(`${t.x},${t.y}`, (count * METRES_PER_TILE) / PARKING.kerbVsOffStreet.kerbSpaceLengthMetres);
  }
  return out;
});

// --- AC-3: parkingShortfallOf -------------------------------------------------

export interface ParkingShortfall {
  cityShare: number;
  perTile: Map<string, number>;
}

/**
 * AC-3 — bounded [0,1] shortfall per tile: `demand > 0 ? clamp(1 -
 * (kerbSupply + 0) / demand, 0, 1) : 0`. The `+ 0` is Decision D1's
 * off-street-supply term, written explicitly (not omitted) — a future
 * placeable-parking increment changes this term, not this one.
 * `cityShare` is the SAME formula population-weighted city-wide (residents
 * + workers weight per tile, mirroring inc4 AC-4's `weightByTile` basis).
 */
export const parkingShortfallOf: (s: SimState) => ParkingShortfall = memoOnState((s) => {
  const demand = parkingDemandOf(s);
  const supply = kerbParkingSupplyOf(s);
  const demandTiles = demandForecastOf(s);
  const perTile = new Map<string, number>();
  let weightedSum = 0;
  let totalWeight = 0;
  for (const t of demandTiles) {
    const key = `${t.x},${t.y}`;
    const demanded = demand.get(key)?.demanded ?? 0;
    const kerbSupply = supply.get(key) ?? 0;
    const offStreetSupply = 0; // Decision D1/ASM-1525 — literal, not omitted.
    const shortfall = demanded > 0 ? clamp01(1 - (kerbSupply + offStreetSupply) / demanded) : 0;
    perTile.set(key, shortfall);
    const weight = t.residentsActual + t.workersActual;
    weightedSum += weight * shortfall;
    totalWeight += weight;
  }
  return { cityShare: totalWeight > 0 ? weightedSum / totalWeight : 0, perTile };
});

// --- AC-4: vehicleKmByClassOf -------------------------------------------------

export interface VehicleKmByClass {
  car: number;
  motorbike: number;
  taxi: number;
  cargo_van: number;
  rigid_truck: number;
  articulated_truck: number;
}

const PASSENGER_CLASS_IDS: readonly (keyof VehicleKmByClass)[] = ['car', 'motorbike', 'taxi'];
const FREIGHT_CLASS_IDS: readonly (keyof VehicleKmByClass)[] = ['cargo_van', 'rigid_truck', 'articulated_truck'];

/**
 * AC-4 — city-wide vehicle-km/day by class, bottom-up from demandForecastOf.
 * Passenger classes: `personTrips x modeShareOf(point)[mode] /
 * occupancyForMode(mode) x avgTripLengthKm` (occupancyForMode reused from
 * trafficAssignment.ts, the SAME conversion assignedFlowOf already performs
 * — GR#3). Freight classes: `freightVehicleTrips x
 * freightTonnesByVehicleClass share / totalShare x avgTripLengthKm` — the
 * same per-class normalisation blendedFreightVehicleCapacity (trafficDemand.ts)
 * uses for its BLENDED capacity figure, applied here per-class instead. When
 * every rung freight-class share is 0 (division-by-zero guard), the fallback
 * mirrors blendedFreightVehicleCapacity's own fallback: 100% rigid_truck,
 * never a fabricated non-zero share for the others. Passenger and freight
 * totals are accumulated into DISJOINT class keys — never merged into one
 * bucket (the doc's AC-4 mutant).
 */
export const vehicleKmByClassOf: (s: SimState) => VehicleKmByClass = memoOnState((s) => {
  const point = ladderPointOf(s);
  const shares = modeShareOf(point);
  const avgTripLengthKm = numericField(point, 'avgTripLengthKm');
  const demandTiles = demandForecastOf(s);

  let totalFreightShare = 0;
  const freightShare: Partial<Record<keyof VehicleKmByClass, number>> = {};
  for (const id of FREIGHT_CLASS_IDS) {
    const share = numericFieldOrZero(point, `freightTonnesByVehicleClass.${id}`);
    freightShare[id] = share;
    totalFreightShare += share;
  }

  const out: VehicleKmByClass = {
    car: 0,
    motorbike: 0,
    taxi: 0,
    cargo_van: 0,
    rigid_truck: 0,
    articulated_truck: 0,
  };

  for (const t of demandTiles) {
    if (t.personTrips > 0) {
      for (const id of PASSENGER_CLASS_IDS) {
        const share = shares[id] ?? 0;
        if (share <= 0) continue;
        const occ = occupancyForMode(id);
        if (occ > 0) out[id] += ((t.personTrips * share) / occ) * avgTripLengthKm;
      }
    }
    if (t.freightVehicleTrips > 0) {
      for (const id of FREIGHT_CLASS_IDS) {
        const fraction =
          totalFreightShare > 0 ? (freightShare[id] ?? 0) / totalFreightShare : id === 'rigid_truck' ? 1 : 0;
        out[id] += t.freightVehicleTrips * fraction * avgTripLengthKm;
      }
    }
  }
  return out;
});

// --- AC-5: fuelAndEVDemandOf --------------------------------------------------

export interface FuelAndEVDemand {
  litresPerDay: number;
  evKWhPerDay: number;
  byClass: Record<string, { litres: number; kwh: number }>;
}

const ALL_VEHICLE_CLASS_IDS: readonly (keyof VehicleKmByClass)[] = [...PASSENGER_CLASS_IDS, ...FREIGHT_CLASS_IDS];

/**
 * AC-5 — city-wide fuel litres/EV kWh per day, split from vehicleKmByClassOf
 * via vehicle_classes.json's fuelLitresPerKm/kWhPerKm and the fixed 'early'-
 * era EV share (D2). `kWhPerKm === null` classes (rigid_truck/
 * articulated_truck) contribute `kwh: 0` — honest absence, never a
 * fabricated conversion; `litres` still applies `(1 - evShare)` per the
 * doc's literal formula (today `truckEVShare` is 0 in the 'early' era, so
 * this is a no-op, but the formula is not special-cased on the null kWh
 * figure — the doc's own AC-5 text specifies exactly this).
 */
export const fuelAndEVDemandOf: (s: SimState) => FuelAndEVDemand = memoOnState((s) => {
  const km = vehicleKmByClassOf(s);
  const byClass: Record<string, { litres: number; kwh: number }> = {};
  let litresPerDay = 0;
  let evKWhPerDay = 0;
  for (const id of ALL_VEHICLE_CLASS_IDS) {
    const rate = vehicleRateRow(id);
    const evShare = EV_SHARE_BY_CLASS[id] ?? 0;
    const vehicleKm = km[id];
    const litres = vehicleKm * rate.fuelLitresPerKm * (1 - evShare);
    const kwh = rate.kWhPerKm == null ? 0 : vehicleKm * rate.kWhPerKm * evShare;
    byClass[id] = { litres, kwh };
    litresPerDay += litres;
    evKWhPerDay += kwh;
  }
  return { litresPerDay, evKWhPerDay, byClass };
});

// --- AC-6: evChargePointShortfallOf ------------------------------------------

/**
 * AC-6 — the ladder IS the data source for EV charge-point demand (no
 * bottom-up per-charge-point throughput table exists, per §2). Supply is
 * the literal 0 (Decision D1/ASM-1525) — a future placeable-EV-charger
 * increment's job.
 */
export const evChargePointShortfallOf: (s: SimState) => number = memoOnState((s) => {
  const point = ladderPointOf(s);
  const demand = numericField(point, 'evChargePointsNeeded');
  return demand > 0 ? 1 : 0;
});
