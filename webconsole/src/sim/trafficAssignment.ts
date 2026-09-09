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
  type LineSegment,
} from './data.ts';
import { demandForecastOf, ladderPointOf, modeShareOf, boundedNearestSourceMapOf } from './trafficDemand.ts';

// data files this module reads (GR#15 — every constant below is sourced,
// never hand-typed). This module owns ONLY the webconsoleMetresPerTile field
// inside traffic.json and the BUG-843 motorway beta-override removal inside
// link_capacity.json — every other field here is read-only.
import rawTraffic from './traffic-data/traffic.json' with { type: 'json' };
import rawLinkCapacity from './traffic-data/link_capacity.json' with { type: 'json' };
import rawRoads from './traffic-data/roads.json' with { type: 'json' };
import rawVehicleClasses from './traffic-data/vehicle_classes.json' with { type: 'json' };

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

function registryError(code: string, message: string): Error {
  return new Error(`${code}: ${message}`);
}

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
  const maxRadius = j.maxAttributionRadiusTiles;
  if (typeof maxRadius !== 'number' || !Number.isFinite(maxRadius) || maxRadius <= 0) {
    throw registryError(
      ERR_MAX_ATTRIBUTION_RADIUS_MISSING,
      'data/traffic.json is missing a positive numeric maxAttributionRadiusTiles field',
    );
  }
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
const ROADS = (rawRoads as { classes: RoadClassRow[] }).classes;
const roadRowById = new Map<string, RoadClassRow>(ROADS.map((r) => [r.id, r]));

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

function occupancyForMode(modeId: string): number {
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
 */
// Additively exported (BUG-872, FEAT-2326609797 inc4 rework): emergencyResponse.ts's
// narrow-class-penalty lookup needed the SAME tier->class mapping this module already owns --
// GR#3 forbade the near-verbatim local copy that inc4's first build carried, so this table and
// its lookup function are exported here rather than having a second copy silently diverge.
export const ROAD_CLASS_ID_OF_TIER: Readonly<Record<number, string>> = Object.freeze({
  1: 'residential_street',
  2: 'avenue_2_plus_2',
  3: 'two_lane',
  4: 'dual_carriageway',
  5: 'motorway',
});

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

const nearestRoadSegmentTileMapOf: (s: SimState) => Map<string, string> = memoOnState((s) => {
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

  const nearestSourceTile = boundedNearestSourceMapOf(roadTileKeys, radius);
  const result = new Map<string, string>(); // tileKey -> owning nearest road segmentId
  for (const [tileKey, sourceTileKey] of nearestSourceTile) {
    result.set(tileKey, idx.tileToSegment.get(sourceTileKey)!);
  }
  return result;
});

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

  const assignedFlow = new Map<string, number>();
  const unrouted: UnroutedDemand[] = [];
  const tilePaths = new Map<string, string[]>();
  const tileVehicleTrips = new Map<string, number>();
  const pathCache = new Map<string, string[] | null>(); // originSegId -> path (per this call)

  const accumulate = (m: Map<string, number>, key: string, v: number): void => {
    m.set(key, (m.get(key) ?? 0) + v);
  };

  for (const t of demandTiles) {
    let roadVehicleTrips = 0;
    for (const modeId of ROAD_PERSON_MODE_IDS) {
      const share = shares[modeId] ?? 0;
      if (share <= 0) continue;
      const occ = occupancyForMode(modeId);
      if (occ > 0) roadVehicleTrips += (t.personTrips * share) / occ;
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
    for (const segId of path) accumulate(assignedFlow, segId, roadVehicleTrips);
    tilePaths.set(tileKey, path);
    tileVehicleTrips.set(tileKey, roadVehicleTrips);
  }
  return { assignedFlow, unrouted, tilePaths, tileVehicleTrips };
});

export const assignedFlowOf: (s: SimState) => Map<string, number> = (s) => assignmentOf(s).assignedFlow;
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
  const ticks: Record<string, number> = {};
  const gridlocked: string[] = [];
  for (const segId of sortedIds) {
    const d = delay.get(segId);
    const vOverC = d ? d.vOverC : 0;
    const prev = prevGridlockTicks[segId] ?? 0;
    const next = vOverC >= CONGESTION_PENALTY_THRESHOLD ? Math.min(prev + 1, CONGESTION_SUSTAINED_TICKS) : 0;
    if (next > 0) ticks[segId] = next;
    if (next >= CONGESTION_SUSTAINED_TICKS) gridlocked.push(segId);
  }
  return { ticks, gridlocked };
}

// --- AC-9: structural scale bound helper (test-only export) ----------------

/** AC-9 — total possible Dijkstra relaxations bound: originTileCount *
 * segmentCount. Test-only export, no production caller. */
export function __structuralDijkstraBoundForTest(s: SimState): number {
  const originTileCount = demandForecastOf(s).length;
  const segmentCount = lineSegmentIndexOf(s).segments.length;
  return originTileCount * segmentCount;
}
