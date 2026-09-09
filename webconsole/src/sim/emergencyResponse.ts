// FEAT-2326609797 inc4 "EMERGENCY RESPONSE" — docs/planning/acceptance/FEAT-2326609792-inc4.md
// (AC-1..AC-8, §4 D1-D3, ASM-1510..1513), REWORKED after r1 independent-round REJECT (row 7605,
// BUG-869..873) per the Lead amendments at the bottom of the acceptance doc.
//
// Congested-but-priority-degraded isochrones for the three emergency
// services (ambulance/fire/police), a per-tile response-minutes derivation,
// and a population-weighted coverage statistic against
// data/traffic/emergency_response.json's published targets. Consumes inc3's
// (trafficAssignment.ts) segment graph, free-flow minutes and per-segment
// v/c, and inc2's (trafficDemand.ts) per-tile demand + scale-ladder
// densityBand. AC-7 fiscal/wellbeing boundary: this module is a read-only
// diagnostic layer — no s.budget/treasury/*Pounds/*Revenue/*Cost field, no
// happiness/wellbeing-named identifier (that coupling is inc5's job).
//
// PURE + DETERMINISTIC (GR#21): every exported derivation is memoOnState
// over SimState — no wall-clock read, no PRNG, no browser-storage read.
// Iteration is always over pre-sorted keys/ids (never a bare
// Map-range-with-break).
//
// GR#3 note on the nearest-road-segment attachment (AC-1/AC-3): this module
// reuses the SAME shared primitive `boundedNearestSourceMapOf`
// (trafficDemand.ts, already exported) that trafficAssignment.ts's own
// private `nearestRoadSegmentTileMapOf` composes over. The single-export
// budget from the first build is gone (BUG-872, see below) but this wrapper
// still stays a local re-composition of the shared BFS primitive rather than
// a second export, since it is small and genuinely local plumbing (bbox +
// radius + boundedNearestSourceMapOf), not a table that can silently drift.
//
// BUG-869 (P1, r1 REJECT): the access leg used to be `baseAccessMinutes`
// (data/traffic.json, 15.0) — a COMMUTER walk-access figure that alone
// exceeds every urban target and made coverageShare identically 0 in every
// urban city. That field is no longer read by this module. The access leg
// is now a per-service `turnoutMinutes` (data/traffic/emergency_response.json
// services[].turnoutMinutes — dispatch-to-mobile activation time, sourced),
// loaded via `loadTurnoutMinutesFrom` below.
//
// BUG-872 (P2, r1 REJECT): the narrow-class speed penalty
// (`speedDegradation.narrowClassPenalty`) is REMOVED from this module. It
// was structurally dead code: `SEGMENT_ROAD_CLASSES` (trafficAssignment.ts)
// only ever turns rd_aroad/rd_dual/m20 (tiers 3/4/5) into road SEGMENTS, and
// none of those map to the narrow classes (alley/gravel/residential_street,
// tiers 1/2) — the multiplier could never fire on any currently-segmentable
// road, and no test could ever catch its removal. Speed degradation this
// increment is therefore class-UNIFORM (the vOverC curve alone) until narrow
// classes become segmentable in a future increment. The road-class table
// this would have needed is `ROAD_CLASS_ID_OF_TIER`/`roadClassIdOfSegment`,
// now additively exported from trafficAssignment.ts (GR#3 — the near-verbatim
// local copy the first build carried is deleted); a future increment that
// re-adds the narrow-class penalty imports those rather than re-deriving a
// third copy.

import type { SimState } from './types.ts';
import { SPECS, isOnline, lineSegmentIndexOf, memoOnState, type LineSegment, type Spec } from './data.ts';
import { demandForecastOf, ladderPointOf, boundedNearestSourceMapOf } from './trafficDemand.ts';
import { segmentAdjacencyOf, segmentFreeFlowMinutesOf, segmentDelayOf, weightedPercentile } from './trafficAssignment.ts';

// data files this module reads (GR#15 — every constant below is sourced,
// never hand-typed, and every fail-closed read is this module's OWN
// validation — it does not trust another module's already-parsed config).
import rawTraffic from './traffic-data/traffic.json' with { type: 'json' };
import rawEmergencyResponse from './traffic-data/emergency_response.json' with { type: 'json' };

// --- Registry error codes (GR#7) --------------------------------------------
// Claimed via `node tools/plan/add-error.js claim-range ui.webconsole --size 6`
// (V924-V929) plus `--size 2` (V886-V887, the radius/densityBand reads
// added during build once the shape of the module became clear).
// BUG-869/871 rework: MET-V927 is REUSED (message updated in data/errors.json
// from "EmergencyBaseAccessMinutesMissing" to "EmergencyTurnoutMinutesMissing")
// rather than minting a new code — the shape of the failure (a module-load-
// time fail-closed read of a positive numeric access-time field) is the same,
// only the SOURCE field moved from data/traffic.json.baseAccessMinutes to
// data/traffic/emergency_response.json services[].turnoutMinutes.
// MET-V926 (EmergencyNarrowClassPenaltyMissing) is now UNUSED — the
// narrow-class penalty this module used to validate is removed (BUG-872);
// the code stays reserved in data/errors.json rather than deleted (no
// current caller, but a future narrow-class-penalty reintroduction can reuse
// it rather than mint a new one).
export const ERR_EMERGENCY_SERVICE_MISSING = 'MET-V924'; // EmergencyServiceMissing
export const ERR_EMERGENCY_SPEED_CURVE_INVALID = 'MET-V925'; // EmergencySpeedCurveInvalid
export const ERR_EMERGENCY_TURNOUT_MINUTES_MISSING = 'MET-V927'; // EmergencyTurnoutMinutesMissing (was EmergencyBaseAccessMinutesMissing, BUG-869)
/** AC-5's honest-absence guard code — NOT thrown on every call (that would
 * fire every tick for a structural, permanent absence). Reserved for the
 * day death_cemetery/death_crematorium stop being PH() placeholders and a
 * real hearse routing path's own guard trips unexpectedly. */
export const ERR_EMERGENCY_HEARSE_SPEC_MISSING = 'MET-V928'; // EmergencyHearseSpecMissing
export const ERR_EMERGENCY_MAX_ATTRIBUTION_RADIUS_MISSING = 'MET-V886'; // EmergencyMaxAttributionRadiusMissing
export const ERR_EMERGENCY_DENSITY_BAND_MISSING = 'MET-V887'; // EmergencyDensityBandMissing

function registryError(code: string, message: string): Error {
  return new Error(`${code}: ${message}`);
}

export type EmergencyService = 'ambulance' | 'fire' | 'police';
const SERVICES: readonly EmergencyService[] = ['ambulance', 'fire', 'police'];

// --- data/traffic.json typed view (this module's OWN fail-closed reads) ---

/** BUG-871 — exported pure loader (missing/zero/negative/NaN/string all
 * fail-closed, GR#15), the same BUG-865 shape `loadEmergencyConfigFrom`
 * already follows. */
export function loadMaxAttributionRadiusFrom(raw: unknown): number {
  const j = raw as Record<string, unknown>;
  const v = j.maxAttributionRadiusTiles;
  if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
    throw registryError(
      ERR_EMERGENCY_MAX_ATTRIBUTION_RADIUS_MISSING,
      'data/traffic.json is missing a positive numeric maxAttributionRadiusTiles field',
    );
  }
  return v;
}
const MAX_ATTRIBUTION_RADIUS_TILES = loadMaxAttributionRadiusFrom(rawTraffic);

/** BUG-869/871 — replaces the removed `loadBaseAccessMinutesFrom`. Reads the
 * per-service `turnoutMinutes` (dispatch-to-mobile activation leg) from
 * `emergency_response.json`'s OWN `services[]` array — never
 * `data/traffic.json`'s `baseAccessMinutes`, which is a physically different
 * quantity (a commuter's front-door walk-access minutes) that alone exceeded
 * every urban target and made coverageShare structurally 0 (BUG-869).
 * Exported pure loader (BUG-871): missing/zero/negative/NaN/string all
 * fail-closed per service, fully testable with a scratch object. */
export function loadTurnoutMinutesFrom(raw: unknown): Record<EmergencyService, number> {
  const j = raw as Record<string, unknown>;
  const servicesRaw = j.services;
  if (!Array.isArray(servicesRaw)) {
    throw registryError(ERR_EMERGENCY_TURNOUT_MINUTES_MISSING, 'emergency_response.json services must be an array');
  }
  const out = {} as Record<EmergencyService, number>;
  for (const row of servicesRaw as Array<Record<string, unknown>>) {
    const id = row.service;
    if (typeof id !== 'string' || !(SERVICES as readonly string[]).includes(id)) continue;
    const v = row.turnoutMinutes;
    if (typeof v === 'number' && Number.isFinite(v) && v > 0) {
      out[id as EmergencyService] = v;
    }
  }
  for (const service of SERVICES) {
    if (out[service] == null) {
      throw registryError(
        ERR_EMERGENCY_TURNOUT_MINUTES_MISSING,
        `emergency_response.json services[] has no positive numeric turnoutMinutes for service ${service}`,
      );
    }
  }
  return out;
}
const TURNOUT_MINUTES: Record<EmergencyService, number> = loadTurnoutMinutesFrom(rawEmergencyResponse);

// --- data/traffic/emergency_response.json typed view -----------------------

interface SpeedCurveAnchor {
  vOverC: number;
  speedFactor: number;
}
interface EmergencyConfig {
  targetMinutesUrban: Record<EmergencyService, number>;
  targetMinutesRural: Record<EmergencyService, number>;
  curve: SpeedCurveAnchor[];
}

/** BUG-865-style pure loader (trafficAssignment.ts precedent): raw JSON as
 * an argument, directly testable with a scratch object, no module-load-time
 * side effect to work around. */
export function loadEmergencyConfigFrom(raw: unknown): EmergencyConfig {
  const j = raw as Record<string, unknown>;
  const servicesRaw = j.services;
  if (!Array.isArray(servicesRaw)) {
    throw registryError(ERR_EMERGENCY_SERVICE_MISSING, 'emergency_response.json services must be an array');
  }
  const targetMinutesUrban = {} as Record<EmergencyService, number>;
  const targetMinutesRural = {} as Record<EmergencyService, number>;
  for (const row of servicesRaw as Array<Record<string, unknown>>) {
    const id = row.service;
    if (typeof id !== 'string' || !(SERVICES as readonly string[]).includes(id)) continue;
    const urban = row.targetMinutesUrban;
    const rural = row.targetMinutesRural;
    if (typeof urban === 'number' && Number.isFinite(urban) && urban > 0) {
      targetMinutesUrban[id as EmergencyService] = urban;
    }
    if (typeof rural === 'number' && Number.isFinite(rural) && rural > 0) {
      targetMinutesRural[id as EmergencyService] = rural;
    }
  }
  for (const service of SERVICES) {
    if (targetMinutesUrban[service] == null || targetMinutesRural[service] == null) {
      throw registryError(
        ERR_EMERGENCY_SERVICE_MISSING,
        `emergency_response.json services[] has no entry for service ${service}`,
      );
    }
  }

  const speedDegradation = j.speedDegradation as Record<string, unknown> | undefined;
  const curveRaw = speedDegradation?.curve;
  if (!Array.isArray(curveRaw) || curveRaw.length < 2) {
    throw registryError(
      ERR_EMERGENCY_SPEED_CURVE_INVALID,
      'emergency_response.json speedDegradation.curve is missing or has fewer than 2 anchors',
    );
  }
  const curve: SpeedCurveAnchor[] = [];
  for (const anchor of curveRaw as Array<Record<string, unknown>>) {
    const vOverC = anchor.vOverC;
    const speedFactor = anchor.speedFactor;
    if (
      typeof vOverC !== 'number' ||
      !Number.isFinite(vOverC) ||
      vOverC < 0 ||
      typeof speedFactor !== 'number' ||
      !Number.isFinite(speedFactor) ||
      speedFactor <= 0
    ) {
      throw registryError(
        ERR_EMERGENCY_SPEED_CURVE_INVALID,
        'emergency_response.json speedDegradation.curve has a non-finite or out-of-range anchor',
      );
    }
    curve.push({ vOverC, speedFactor });
  }
  for (let i = 1; i < curve.length; i++) {
    if (curve[i].vOverC <= curve[i - 1].vOverC) {
      throw registryError(
        ERR_EMERGENCY_SPEED_CURVE_INVALID,
        'emergency_response.json speedDegradation.curve is not strictly sorted by vOverC',
      );
    }
  }

  // BUG-872: speedDegradation.narrowClassPenalty is deliberately NOT read or
  // validated here any more — it was structurally dead code (see the module
  // header). The data file may still carry it for a future increment; this
  // loader simply no longer consumes it.

  return {
    targetMinutesUrban,
    targetMinutesRural,
    curve,
  };
}

const EMERGENCY = loadEmergencyConfigFrom(rawEmergencyResponse);

/** Piecewise-linear interpolation on `curve` (sorted ascending by vOverC,
 * enforced by the loader above), clamped to the curve's own endpoints
 * outside its range — per emergency_response.json's own interpolationRule,
 * never a hand-typed fallback constant (AC-8's mutant target). */
export function speedFactorFromCurve(curve: readonly SpeedCurveAnchor[], vOverC: number): number {
  if (vOverC <= curve[0].vOverC) return curve[0].speedFactor;
  const last = curve[curve.length - 1];
  if (vOverC >= last.vOverC) return last.speedFactor;
  for (let i = 0; i < curve.length - 1; i++) {
    const a = curve[i];
    const b = curve[i + 1];
    if (vOverC >= a.vOverC && vOverC <= b.vOverC) {
      const w = (vOverC - a.vOverC) / (b.vOverC - a.vOverC);
      return a.speedFactor + w * (b.speedFactor - a.speedFactor);
    }
  }
  return last.speedFactor;
}

/** AC-1 — emergency-vehicle speed factor for one segment: the curve's own
 * interpolation on the segment's vOverC (absent from segmentDelayOf ⇒ 0,
 * the curve's own vOverC:0 anchor ⇒ speedFactor 1.0 — never a hand-typed
 * `1.0` bypassing the curve). BUG-872: the narrow-class multiplier that used
 * to apply here is REMOVED (structurally dead — see the module header); the
 * `seg` parameter is kept in the signature for now (MapView/tests pass a
 * real LineSegment and a future narrow-class reintroduction needs it again)
 * but is deliberately unused this increment. */
export function speedFactorFor(s: SimState, segId: string, _seg: LineSegment): number {
  const d = segmentDelayOf(s).get(segId);
  const vOverC = d ? d.vOverC : 0;
  return speedFactorFromCurve(EMERGENCY.curve, vOverC);
}

// --- nearest-road-segment attachment (shared primitive re-composition) ----

const nearestRoadSegmentOf: (s: SimState) => Map<string, string> = memoOnState((s) => {
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
  const radius = Math.min(bboxDiameter, MAX_ATTRIBUTION_RADIUS_TILES);
  const nearestSourceTile = boundedNearestSourceMapOf(roadTileKeys, radius);
  const result = new Map<string, string>();
  for (const [tileKey, sourceTileKey] of nearestSourceTile) {
    result.set(tileKey, idx.tileToSegment.get(sourceTileKey)!);
  }
  return result;
});

// --- station discovery (AC-2) -----------------------------------------------

/** AC-2 — spec-kind-driven, never a hand-typed id LIST. Ambulance is the one
 * exception that must check the SPEC ID (not `kind==='health'`, which also
 * matches clinics/hospitals — neither is a dispatch point) — a single id
 * equality, not an array, per the doc's own AC-2 wording. */
function isStationOfService(sp: Spec, service: EmergencyService): boolean {
  if (service === 'ambulance') return sp.kind === 'health' && sp.id === 'hea_ambulance';
  if (service === 'fire') return sp.kind === 'fire';
  return sp.kind === 'police';
}

// --- AC-1: emergencyIsochroneOf ---------------------------------------------

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

/** AC-9-class structural pin support (mirrors trafficAssignment.ts's own
 * counter): total neighbour relaxations across every Dijkstra run since the
 * last reset. */
let relaxationCount = 0;
export function __resetEmergencyRelaxationCounterForTest(): void {
  relaxationCount = 0;
}
export function __getEmergencyRelaxationCounterForTest(): number {
  return relaxationCount;
}

/** Multi-source Dijkstra from every segment id in `sources` (each starting
 * at distance 0) over `adjacency`, edge weight = the NEIGHBOUR segment's own
 * free-flow minutes divided by its emergency speed factor (AC-1). Explores
 * the WHOLE reachable component (no early stop — this produces a full
 * isochrone, not a single shortest path). Deterministic tie-break: lower
 * segmentId wins at equal distance (sorted neighbour iteration + the heap's
 * own segId tie-break). */
function dijkstraIsochrone(
  s: SimState,
  sources: ReadonlySet<string>,
  adjacency: ReadonlyMap<string, Set<string>>,
  freeFlow: ReadonlyMap<string, number>,
  segmentById: ReadonlyMap<string, LineSegment>,
): Map<string, number> {
  const dist = new Map<string, number>();
  const visited = new Set<string>();
  const heap = new MinHeap();
  for (const src of [...sources].sort()) {
    dist.set(src, 0);
    heap.push({ dist: 0, segId: src });
  }
  while (heap.size > 0) {
    const top = heap.pop()!;
    if (visited.has(top.segId)) continue;
    visited.add(top.segId);
    const neighbours = adjacency.get(top.segId);
    if (!neighbours) continue;
    for (const n of [...neighbours].sort()) {
      relaxationCount++;
      if (visited.has(n)) continue;
      const seg = segmentById.get(n);
      if (!seg) continue;
      const factor = speedFactorFor(s, n, seg);
      const w = (freeFlow.get(n) ?? 0) / factor;
      const nd = top.dist + w;
      const known = dist.get(n);
      if (known === undefined || nd < known) {
        dist.set(n, nd);
        heap.push({ dist: nd, segId: n });
      }
    }
  }
  return dist;
}

const emergencyIsochronesOf: (s: SimState) => Record<EmergencyService, Map<string, number>> = memoOnState((s) => {
  const idx = lineSegmentIndexOf(s);
  const adjacency = segmentAdjacencyOf(s);
  const freeFlow = segmentFreeFlowMinutesOf(s);
  const nearestSeg = nearestRoadSegmentOf(s);
  const sortedBuildings = [...s.buildings].sort((a, b) => a.id - b.id);

  const out = {} as Record<EmergencyService, Map<string, number>>;
  for (const service of SERVICES) {
    const sourceSegIds = new Set<string>();
    for (const b of sortedBuildings) {
      if (!isOnline(s, b)) continue;
      const sp = SPECS[b.spec];
      if (!sp || !isStationOfService(sp, service)) continue;
      const segId = nearestSeg.get(`${b.x},${b.y}`);
      if (segId) sourceSegIds.add(segId);
    }
    out[service] = dijkstraIsochrone(s, sourceSegIds, adjacency, freeFlow, idx.segmentById);
  }
  return out;
});

/** AC-1 — one multi-source Dijkstra per service over the congested-but-
 * degraded graph. Sources = every online station-of-that-service's nearest
 * road segment. A service with zero online stations returns an EMPTY map
 * (AC-2 honest absence), never a thrown error or a fabricated Infinity. */
export const emergencyIsochroneOf = (s: SimState, service: EmergencyService): Map<string, number> =>
  emergencyIsochronesOf(s)[service];

// --- AC-3: responseMinutesOf -------------------------------------------------

const responseMinutesAllOf: (s: SimState) => Record<EmergencyService, Map<string, number>> = memoOnState((s) => {
  const nearestSeg = nearestRoadSegmentOf(s);
  const demandTiles = demandForecastOf(s);
  const out = {} as Record<EmergencyService, Map<string, number>>;
  for (const service of SERVICES) {
    const iso = emergencyIsochroneOf(s, service);
    const m = new Map<string, number>();
    for (const t of demandTiles) {
      const tileKey = `${t.x},${t.y}`;
      const segId = nearestSeg.get(tileKey);
      if (!segId) continue; // honest absence: no origin segment for this tile
      const minutes = iso.get(segId);
      if (minutes === undefined) continue; // honest absence: unreachable / no stations
      m.set(tileKey, minutes + TURNOUT_MINUTES[service]);
    }
    out[service] = m;
  }
  return out;
});

/** AC-3 — per-tile response minutes via nearest-segment attachment (the
 * SAME `boundedNearestSourceMapOf`-composed primitive `assignedFlowOf`
 * uses) + the service's own turnout-minutes activation leg (BUG-869 —
 * NEVER `data/traffic.json`'s `baseAccessMinutes`, a different, larger
 * commuter figure). A tile whose nearest segment never appears in the
 * isochrone (disconnected component, or zero stations per AC-2) is OMITTED
 * from the returned map — never `Infinity`, never a fabricated sentinel. */
export const responseMinutesOf = (s: SimState, service: EmergencyService): Map<string, number> =>
  responseMinutesAllOf(s)[service];

// --- AC-4: emergencyCoverageOf ----------------------------------------------

/** BUG-873, Lead amendment 3: reported THREE ways pending Aaron's ruling on
 * the denominator, so nothing is hidden. `coverageShare` = covered ÷
 * RESPONDED population (AC-4's original text, unreachable/no-station
 * population excluded from both sides). `strandedPopulation` = demand
 * population on tiles that never got a `responseMinutesOf` entry at all
 * (disconnected graph component, or the service has zero online stations —
 * AC-3's honest-absence set). `coverageShareOfAll` = covered ÷ (responded +
 * stranded) — the harsher, whole-city denominator; a city that strands half
 * its map shows a materially lower `coverageShareOfAll` than `coverageShare`
 * even though nothing "improved". Both share fields are `null` only when
 * their OWN denominator is zero (never a division-by-zero NaN). */
export interface EmergencyCoverage {
  coverageShare: number | null;
  strandedPopulation: number;
  coverageShareOfAll: number | null;
  p50Minutes: number;
  p90Minutes: number;
}

/** AC-4 — is the CURRENT scale-ladder point's densityBand rural (the LOWER
 * component of the "lower~upper" transition string every rung carries)? A
 * densityBand like `'rural~small_town'` is treated as rural for target
 * SELECTION purposes (the city is still predominantly in the rural band at
 * that rung) — no rung in data/traffic/scale_ladder.json is ever the bare
 * string `'rural'` (every rung's value is a `<band>~<band>` transition
 * pair), so a literal `=== 'rural'` equality (as the acceptance doc's prose
 * shorthand reads) can never be true; this is flagged in the report's
 * "where the doc is wrong" section. Fail-closed if densityBand is absent or
 * not a string (GR#15). */
export function isRuralDensityBand(point: { nonNumeric: Array<{ key: string; rawValue: unknown }> }): boolean {
  const f = point.nonNumeric.find((x) => x.key === 'densityBand');
  if (!f || typeof f.rawValue !== 'string') {
    throw registryError(
      ERR_EMERGENCY_DENSITY_BAND_MISSING,
      'the current scale-ladder point is missing a string densityBand non-numeric field',
    );
  }
  return f.rawValue.split('~')[0] === 'rural';
}

/** AC-4 — population-weighted coverage vs emergency_response.json targets
 * (band-selected: rural target where the city's densityBand is rural, else
 * urban), plus city-wide p50/p90 minutes via the SAME weighted-percentile
 * helper `commuteTimeDistributionOf` (inc3 AC-5) uses. A city with zero
 * routable demand reports `coverageShare: null` (honest absence), never a
 * division-by-zero NaN. */
export const emergencyCoverageOf: (s: SimState, service: EmergencyService) => EmergencyCoverage = (() => {
  const cache: (s: SimState) => Record<EmergencyService, EmergencyCoverage> = memoOnState((s) => {
    const point = ladderPointOf(s);
    const rural = isRuralDensityBand(point);
    const demandTiles = demandForecastOf(s);
    const weightByTile = new Map<string, number>();
    for (const t of demandTiles) weightByTile.set(`${t.x},${t.y}`, t.residentsActual + t.workersActual);
    // BUG-873(3): total demand weight across EVERY demand tile, reachable or
    // not -- the whole-city denominator coverageShareOfAll needs.
    let allDemandWeight = 0;
    for (const w of weightByTile.values()) allDemandWeight += w;

    const out = {} as Record<EmergencyService, EmergencyCoverage>;
    for (const service of SERVICES) {
      const target = rural ? EMERGENCY.targetMinutesRural[service] : EMERGENCY.targetMinutesUrban[service];
      const responses = responseMinutesOf(s, service);
      let coveredWeight = 0;
      let totalWeight = 0;
      const rows: Array<{ minutes: number; weight: number }> = [];
      const sortedTileKeys = [...responses.keys()].sort();
      for (const tileKey of sortedTileKeys) {
        const minutes = responses.get(tileKey)!;
        const weight = weightByTile.get(tileKey) ?? 0;
        totalWeight += weight;
        if (minutes <= target) coveredWeight += weight;
        rows.push({ minutes, weight });
      }
      rows.sort((a, b) => a.minutes - b.minutes);
      const values = rows.map((r) => r.minutes);
      const weights = rows.map((r) => r.weight);
      const strandedPopulation = allDemandWeight - totalWeight;
      out[service] = {
        coverageShare: totalWeight > 0 ? coveredWeight / totalWeight : null,
        strandedPopulation,
        coverageShareOfAll: allDemandWeight > 0 ? coveredWeight / allDemandWeight : null,
        p50Minutes: weightedPercentile(values, weights, 0.5),
        p90Minutes: weightedPercentile(values, weights, 0.9),
      };
    }
    return out;
  });
  return (s, service) => cache(s)[service];
})();

/** AC-6 overlay support: the current city's target minutes for `service`
 * (band-selected exactly as emergencyCoverageOf's AC-4 selection — same
 * `isRuralDensityBand` call, never a second density read). Exported so
 * MapView's tint can colour a tile relative to its own target without
 * duplicating the urban/rural selection rule. */
const emergencyTargetMinutesAllOf: (s: SimState) => Record<EmergencyService, number> = memoOnState((s) => {
  const rural = isRuralDensityBand(ladderPointOf(s));
  const out = {} as Record<EmergencyService, number>;
  for (const service of SERVICES) {
    out[service] = rural ? EMERGENCY.targetMinutesRural[service] : EMERGENCY.targetMinutesUrban[service];
  }
  return out;
});
export const emergencyTargetMinutesOf = (s: SimState, service: EmergencyService): number =>
  emergencyTargetMinutesAllOf(s)[service];

// --- AC-5: hearseIsochroneOf -------------------------------------------------

/** AC-5 — honest-absence contract this increment (GR#25, no speculative
 * mechanic). Returns `null` UNCONDITIONALLY: `death_cemetery`/
 * `death_crematorium` are `PH()` placeholders that `canEnterSim` rejects
 * unconditionally (data.ts), and MOD-083's hearse transport mechanic does
 * not exist anywhere in the webconsole. This is a permanent, structural
 * scope boundary — not a "no cemetery placed yet" check — so it must stay
 * `null` even if a caller forces a death_cemetery/death_crematorium
 * building directly into `s.buildings` (bypassing canEnterSim), simulating
 * a future world where the placeholder ships. ERR_EMERGENCY_HEARSE_SPEC_
 * MISSING (MET-V928) is reserved for the day a real hearse routing path's
 * own guard trips unexpectedly — it is never thrown by this function today. */
export function hearseIsochroneOf(_s: SimState): Map<string, number> | null {
  return null;
}
