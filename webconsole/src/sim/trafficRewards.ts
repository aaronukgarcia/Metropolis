// FEAT-2326609802 "REWARDS" (inc9 of the Realistic Traffic XXL epic) —
// docs/planning/acceptance/FEAT-2326609792-inc9.md, AC-1..AC-8.
//
// Two read-out scores over the real routed road network (GR#3 — reuses
// inc3's segmentDelayOf/assignedFlowOf/roadClassIdOfSegment, data.ts's
// lineSegmentIndexOf/stationLinks/SPECS/isOnline, and inc2's
// ladderPointOf/modeShareOf — no second flood, no second mode split):
//
//  - safeRoadScoreOf(s)/citySafeRoadScoreOf(s) — per-segment and
//    flow-weighted city-wide safety score (AC-1..AC-3), junction-aware with
//    weight renormalization when a segment owns no junction tile.
//  - integratedTransportScoreOf(s) — interchange-adjacency share (honestly
//    scoped to STATION COUNT, not a 400m population catchment — ASM-1528)
//    blended with a Shannon-entropy mode-balance measure over the CITY-WIDE
//    (population-keyed) mode-share vector (AC-4).
//
// LEAD RULING r3 (BUG-938, 2026-09-11): the webconsole's only mode-split
// model (inc2's modeShareOf(ladderPointOf(s))) is keyed purely on city
// population, so an "integration reward" built from it cannot respond to
// what the player actually builds — r2's per-tile-occupancy attempt
// (BUG-927) approximated build-sensitivity but produced a hidden POPULATION
// PENALTY instead (every built city scored LOWER than a bare map, 13/18
// ladder rungs unreachable). integratedTransportScoreOf is therefore an
// EXPORTED DIAGNOSTIC ONLY this increment — safeRoadScoreOf/
// citySafeRoadScoreOf is the ONLY score wired into wellbeing (AC-5) or
// attract (there is no attract multiplier this increment — AC-6 amended,
// see docs/planning/acceptance/FEAT-2326609792-inc9.md). The real
// prerequisite (a per-tile, land-use/access-keyed mode split in inc2's OWN
// demand model) is tracked as FEAT-2326609804; inc9's integration coupling
// returns once that lands.
//
// Both scores are POSITIVE-signed [0,1] and feed ONE wellbeing row
// ('Safe roads', engine.ts's buildServiceWellbeingParts, AC-5). Neither is
// ever referenced inside attractivenessOf (D2 — safety feeds wellbeing
// only, avoiding the double-count risk inc5's D1 explicitly avoided for
// congestion; the integration diagnostic feeds nothing downstream today).
//
// PURE + DETERMINISTIC (GR#21): every exported derivation is memoOnState
// over SimState — no Date.now/Math.random/localStorage, sorted iteration
// throughout, no map-range-with-break. No money (AC-8): nothing here reads
// or writes s.budget/treasury/any *Pounds/*Revenue/*Cost field, and the
// congestion term reads ONLY segmentDelayOf(seg).vOverC (never a
// wear/condition field — inc7/FEAT-2326609800 is open, not landed).

import type { SimState } from './types.ts';
import { SPECS, isOnline, lineSegmentIndexOf, memoOnState, stationLinks, type LineSegment } from './data.ts';
import { segmentDelayOf, assignedFlowOf, roadClassIdOfSegment, loadTrafficConfigFrom } from './trafficAssignment.ts';
import { ladderPointOf, modeShareOf } from './trafficDemand.ts';

import rawRewards from './traffic-data/rewards.json' with { type: 'json' };
import rawRoads from './traffic-data/roads.json' with { type: 'json' };
import rawTraffic from './traffic-data/traffic.json' with { type: 'json' };

// --- Registry error codes (GR#7) --------------------------------------------
// Claimed from the ui.webconsole V950-V959 reservation via
// `node tools/plan/add-error.js add MET-V95x --mkey ui.webconsole ...`.
export const ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING = 'MET-V950'; // RewardsSafeRoadComponentsMissing
export const ERR_REWARDS_ROAD_CLASS_SAFETY_MISSING = 'MET-V951'; // RewardsRoadClassSafetyMissing
export const ERR_REWARDS_JUNCTION_TYPE_SAFETY_MISSING = 'MET-V952'; // RewardsJunctionTypeSafetyMissing
export const ERR_REWARDS_INTEGRATED_TRANSPORT_COMPONENTS_MISSING = 'MET-V953'; // RewardsIntegratedTransportComponentsMissing
export const ERR_REWARDS_MODE_SHARE_VECTOR_EMPTY = 'MET-V954'; // RewardsModeShareVectorEmpty

function registryError(code: string, message: string): Error {
  return new Error(`${code}: ${message}`);
}

/** Local clamp — same one-line pure helper idiom as trafficWellbeing.ts's
 * own clampN (not worth an import for a single line, no cycle risk). */
function clampN(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}

// --- data/traffic/rewards.json typed view -----------------------------------
// GR#15: every weight/table below is sourced from rewards.json, never
// hand-typed — including the mode count for AC-7's ln(N) divisor, which
// reads the REALISED mode-share vector's own length (modeShareOf(point)),
// never a literal 11.

interface RewardsComponent {
  id?: unknown;
  weight?: unknown;
  byRoadClassId?: unknown;
  byJunctionType?: unknown;
  curve?: unknown;
  speedAnchorKmh?: unknown;
  speedSpanKmh?: unknown;
}

interface SafeRoadConfig {
  roadClassBaseSafetyWeight: number;
  junctionTypeSafetyWeight: number;
  designSpeedPenaltyWeight: number;
  congestionSafetyInteractionWeight: number;
  byRoadClassId: Record<string, number>;
  byJunctionType: Record<string, number>;
  congestionCurve: Array<{ vOverC: number; safetyContribution: number }>;
  // BUG-928: the 30 km/h anchor and 100 km/h span were hand-typed TS
  // literals; both now come from rewards.json's designSpeedPenalty
  // component (source-noted there), read through this fail-closed loader
  // exactly like every other magnitude in this file.
  designSpeedAnchorKmh: number;
  designSpeedSpanKmh: number;
}

interface IntegratedTransportConfig {
  interchangeAdjacencyWeight: number;
  modeShareBalanceWeight: number;
}

export interface RewardsConfig {
  safeRoad: SafeRoadConfig;
  integratedTransport: IntegratedTransportConfig;
}

function componentById(components: RewardsComponent[], id: string, missingCode: string, whichArray: string): RewardsComponent {
  const c = components.find((x) => x.id === id);
  if (!c) {
    throw registryError(missingCode, `data/traffic/rewards.json ${whichArray}.components is missing component "${id}"`);
  }
  return c;
}

function weightOf(c: RewardsComponent, id: string, missingCode: string, whichArray: string): number {
  const w = c.weight;
  if (typeof w !== 'number' || !Number.isFinite(w) || w <= 0) {
    throw registryError(missingCode, `data/traffic/rewards.json ${whichArray} component "${id}" has an invalid weight`);
  }
  return w;
}

// BUG-928: fail-closed reader for the two designSpeedPenalty magnitudes
// (speedAnchorKmh/speedSpanKmh) — same shape as weightOf, missing/NaN/
// negative all rejected, never silently defaulted. speedSpanKmh is also a
// DIVISOR downstream (designSpeedPenalty's (speedKmh-anchor)/span), so a
// zero span is rejected too — never a division-by-zero waiting to happen.
function positiveNumericField(c: RewardsComponent, field: 'speedAnchorKmh' | 'speedSpanKmh', id: string, missingCode: string, whichArray: string): number {
  const v = c[field];
  const floor = field === 'speedSpanKmh' ? 0 : -Infinity; // span must be > floor(0); anchor may legitimately be 0
  if (typeof v !== 'number' || !Number.isFinite(v) || v < 0 || v <= floor) {
    throw registryError(missingCode, `data/traffic/rewards.json ${whichArray} component "${id}" has an invalid ${field}`);
  }
  return v;
}

/**
 * Pure loader taking the raw JSON as an ARGUMENT (same "pass the cfg as an
 * argument" idiom trafficAssignment.ts's loadTrafficConfigFrom uses) so
 * every fail-closed field read here is directly testable with a scratch
 * `raw` object — AC-7's malformed-fixture check.
 */
export function loadRewardsConfigFrom(raw: unknown): RewardsConfig {
  const j = raw as {
    safeRoadScore?: { components?: unknown };
    integratedTransportScore?: { components?: unknown };
  };

  const safeComponents = j?.safeRoadScore?.components;
  if (!Array.isArray(safeComponents) || safeComponents.length === 0) {
    throw registryError(
      ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING,
      'data/traffic/rewards.json safeRoadScore.components is missing or malformed',
    );
  }
  const roadClassBaseSafety = componentById(safeComponents, 'roadClassBaseSafety', ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING, 'safeRoadScore');
  const junctionTypeSafety = componentById(safeComponents, 'junctionTypeSafety', ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING, 'safeRoadScore');
  const designSpeedPenalty = componentById(safeComponents, 'designSpeedPenalty', ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING, 'safeRoadScore');
  const congestionSafetyInteraction = componentById(safeComponents, 'congestionSafetyInteraction', ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING, 'safeRoadScore');

  const byRoadClassId = roadClassBaseSafety.byRoadClassId;
  if (!byRoadClassId || typeof byRoadClassId !== 'object') {
    throw registryError(ERR_REWARDS_ROAD_CLASS_SAFETY_MISSING, 'data/traffic/rewards.json roadClassBaseSafety.byRoadClassId is missing or malformed');
  }
  const byJunctionType = junctionTypeSafety.byJunctionType;
  if (!byJunctionType || typeof byJunctionType !== 'object') {
    throw registryError(ERR_REWARDS_JUNCTION_TYPE_SAFETY_MISSING, 'data/traffic/rewards.json junctionTypeSafety.byJunctionType is missing or malformed');
  }
  const curve = congestionSafetyInteraction.curve;
  if (!Array.isArray(curve) || curve.length < 2) {
    throw registryError(ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING, 'data/traffic/rewards.json congestionSafetyInteraction.curve is missing or too short');
  }
  for (const pt of curve as Array<Record<string, unknown>>) {
    if (typeof pt.vOverC !== 'number' || typeof pt.safetyContribution !== 'number') {
      throw registryError(ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING, 'data/traffic/rewards.json congestionSafetyInteraction.curve has a non-numeric point');
    }
  }

  const integratedComponents = j?.integratedTransportScore?.components;
  if (!Array.isArray(integratedComponents) || integratedComponents.length === 0) {
    throw registryError(
      ERR_REWARDS_INTEGRATED_TRANSPORT_COMPONENTS_MISSING,
      'data/traffic/rewards.json integratedTransportScore.components is missing or malformed',
    );
  }
  const interchangeAdjacency = componentById(integratedComponents, 'interchangeAdjacency', ERR_REWARDS_INTEGRATED_TRANSPORT_COMPONENTS_MISSING, 'integratedTransportScore');
  const modeShareBalance = componentById(integratedComponents, 'modeShareBalance', ERR_REWARDS_INTEGRATED_TRANSPORT_COMPONENTS_MISSING, 'integratedTransportScore');

  return {
    safeRoad: {
      roadClassBaseSafetyWeight: weightOf(roadClassBaseSafety, 'roadClassBaseSafety', ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING, 'safeRoadScore'),
      junctionTypeSafetyWeight: weightOf(junctionTypeSafety, 'junctionTypeSafety', ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING, 'safeRoadScore'),
      designSpeedPenaltyWeight: weightOf(designSpeedPenalty, 'designSpeedPenalty', ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING, 'safeRoadScore'),
      congestionSafetyInteractionWeight: weightOf(congestionSafetyInteraction, 'congestionSafetyInteraction', ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING, 'safeRoadScore'),
      byRoadClassId: byRoadClassId as Record<string, number>,
      byJunctionType: byJunctionType as Record<string, number>,
      congestionCurve: curve as Array<{ vOverC: number; safetyContribution: number }>,
      designSpeedAnchorKmh: positiveNumericField(designSpeedPenalty, 'speedAnchorKmh', 'designSpeedPenalty', ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING, 'safeRoadScore'),
      designSpeedSpanKmh: positiveNumericField(designSpeedPenalty, 'speedSpanKmh', 'designSpeedPenalty', ERR_REWARDS_SAFE_ROAD_COMPONENTS_MISSING, 'safeRoadScore'),
    },
    integratedTransport: {
      interchangeAdjacencyWeight: weightOf(interchangeAdjacency, 'interchangeAdjacency', ERR_REWARDS_INTEGRATED_TRANSPORT_COMPONENTS_MISSING, 'integratedTransportScore'),
      modeShareBalanceWeight: weightOf(modeShareBalance, 'modeShareBalance', ERR_REWARDS_INTEGRATED_TRANSPORT_COMPONENTS_MISSING, 'integratedTransportScore'),
    },
  };
}

function loadRewardsConfig(): RewardsConfig {
  return loadRewardsConfigFrom(rawRewards);
}
const REWARDS = loadRewardsConfig();

// data/traffic.json's metresPerMile — reused via trafficAssignment.ts's
// already fail-closed-validated loadTrafficConfigFrom (GR#3: don't
// re-validate a field another module already owns validating).
const METRES_PER_MILE = loadTrafficConfigFrom(rawTraffic).metresPerMile;

// --- data/traffic/roads.json typed view (this item's OWN class-row reader —
// trafficAssignment.ts's roadClassRow is module-private, per the doc's GR#25
// dependency note: "this item needs its OWN class-row readers over
// rewards.json, not a reuse of these private helpers"). --------------------
interface RoadClassSpeedRow {
  id: string;
  speedLimit: number;
}
const ROAD_SPEED_ROWS = (rawRoads as { classes: RoadClassSpeedRow[] }).classes;
const speedLimitMphById = new Map<string, number>(ROAD_SPEED_ROWS.map((r) => [r.id, r.speedLimit]));

function speedLimitMphOf(roadClassId: string): number {
  const v = speedLimitMphById.get(roadClassId);
  if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
    throw registryError(ERR_REWARDS_ROAD_CLASS_SAFETY_MISSING, `data/traffic/roads.json classes has no numeric speedLimit for road class ${roadClassId}`);
  }
  return v;
}

function roadClassBaseSafetyOf(roadClassId: string): number {
  const v = REWARDS.safeRoad.byRoadClassId[roadClassId];
  if (typeof v !== 'number' || !Number.isFinite(v)) {
    throw registryError(ERR_REWARDS_ROAD_CLASS_SAFETY_MISSING, `data/traffic/rewards.json byRoadClassId has no entry for road class ${roadClassId}`);
  }
  return v;
}

function junctionTypeSafetyOf(junctionKey: string): number {
  const v = REWARDS.safeRoad.byJunctionType[junctionKey];
  if (typeof v !== 'number' || !Number.isFinite(v)) {
    throw registryError(ERR_REWARDS_JUNCTION_TYPE_SAFETY_MISSING, `data/traffic/rewards.json byJunctionType has no entry for junction key ${junctionKey}`);
  }
  return v;
}

/** Piecewise-linear interpolation over rewards.json's own congestion curve
 * (sorted ascending by vOverC), flat-extrapolated beyond either end —
 * exported for the AC-1 boundary tests (below the first point, above the
 * last point, and one straddled midpoint). */
export function interpolateSafetyCurve(curve: Array<{ vOverC: number; safetyContribution: number }>, vOverC: number): number {
  const pts = [...curve].sort((a, b) => a.vOverC - b.vOverC);
  if (pts.length === 0) return 1;
  if (vOverC <= pts[0].vOverC) return pts[0].safetyContribution;
  const last = pts[pts.length - 1];
  if (vOverC >= last.vOverC) return last.safetyContribution;
  for (let i = 0; i < pts.length - 1; i++) {
    const a = pts[i];
    const b = pts[i + 1];
    if (vOverC >= a.vOverC && vOverC <= b.vOverC) {
      const frac = (vOverC - a.vOverC) / (b.vOverC - a.vOverC);
      return a.safetyContribution + frac * (b.safetyContribution - a.safetyContribution);
    }
  }
  return last.safetyContribution;
}

// --- AC-2: junction spec id -> rewards.json junction key --------------------
// Spec-id-driven (SPECS[b.spec] IS b.spec here, the map key doubles as the
// id — data.ts:2356-2370). 'signalised' is deliberately never a value below:
// no signal-controlled junction spec exists in this game today (ASM-1527).
const JUNCTION_KEY_BY_SPEC_ID: Readonly<Record<string, string>> = Object.freeze({
  rd_junction: 'simple_priority',
  rd_roundabout: 'roundabout',
  rd_mwyjunction: 'grade_separated',
});

/**
 * Segment -> junction key for every segment that has an online junction
 * building 4-adjacent to one of its tiles. Junction specs are NOT in
 * SEGMENT_LINE_CLASSES (data.ts:3264-3272), so a junction tile's own key
 * never appears in tileToSegment — this reverse-walks the junction
 * building's own footprint tiles' 4-neighbours against tileToSegment, the
 * SAME adjacency idiom stationLinks (data.ts:3020) already uses for
 * road-adjacency, applied here to segment-adjacency instead (GR#3 shape,
 * not the same function). Deterministic: junction buildings visited in
 * (x,y,id) order, first touch wins per segment (never overwritten).
 */
const junctionKeyBySegmentOf: (s: SimState) => Map<string, string> = memoOnState((s) => {
  const idx = lineSegmentIndexOf(s);
  const out = new Map<string, string>();
  const junctionBuildings = s.buildings
    .filter((b) => JUNCTION_KEY_BY_SPEC_ID[b.spec] !== undefined && isOnline(s, b))
    .slice()
    .sort((a, b) => a.x - b.x || a.y - b.y || a.id - b.id);
  for (const b of junctionBuildings) {
    const key = JUNCTION_KEY_BY_SPEC_ID[b.spec];
    const sp = SPECS[b.spec];
    const w = sp?.w ?? 1;
    const h = sp?.h ?? 1;
    for (let dx = 0; dx < w; dx++) {
      for (let dy = 0; dy < h; dy++) {
        const x = b.x + dx;
        const y = b.y + dy;
        const neighbours = [`${x + 1},${y}`, `${x - 1},${y}`, `${x},${y + 1}`, `${x},${y - 1}`];
        for (const nk of neighbours) {
          const segId = idx.tileToSegment.get(nk);
          if (!segId) continue;
          if (!out.has(segId)) out.set(segId, key);
        }
      }
    }
  }
  return out;
});

// --- AC-1/AC-2/AC-3: safeRoadScoreOf / citySafeRoadScoreOf -------------------

/**
 * AC-1/AC-2 — per-segment safety score in [0,1], 1 = safest. Domain =
 * segmentDelayOf(s)'s own domain restricted to road segments (a zero-flow
 * segment has no vehicles to be safe or unsafe for, honest absence
 * mirrored from inc3 AC-4; rail/hs1 segments carry no road-safety meaning
 * and are excluded). Cost: one O(segments) pass — never O(citizens).
 */
export const safeRoadScoreOf: (s: SimState) => Map<string, number> = memoOnState((s) => {
  const delays = segmentDelayOf(s);
  const idx = lineSegmentIndexOf(s);
  const junctionKeys = junctionKeyBySegmentOf(s);
  const out = new Map<string, number>();
  const segIds = [...delays.keys()].sort();
  const wBase = REWARDS.safeRoad.roadClassBaseSafetyWeight;
  const wJunction = REWARDS.safeRoad.junctionTypeSafetyWeight;
  const wSpeed = REWARDS.safeRoad.designSpeedPenaltyWeight;
  const wCongestion = REWARDS.safeRoad.congestionSafetyInteractionWeight;
  for (const segId of segIds) {
    const seg: LineSegment | undefined = idx.segmentById.get(segId);
    if (!seg || seg.kind !== 'road') continue;
    const delay = delays.get(segId)!;
    const roadClassId = roadClassIdOfSegment(seg);
    const base = roadClassBaseSafetyOf(roadClassId);
    const speedKmh = (speedLimitMphOf(roadClassId) * METRES_PER_MILE) / 1000;
    // BUG-928: anchor (30 km/h) and span (100 km/h) are now data-sourced
    // (rewards.json designSpeedPenalty.speedAnchorKmh/speedSpanKmh), never
    // hand-typed TS literals.
    const designSpeedPenalty = clampN((speedKmh - REWARDS.safeRoad.designSpeedAnchorKmh) / REWARDS.safeRoad.designSpeedSpanKmh, 0, 1);
    const congestion = interpolateSafetyCurve(REWARDS.safeRoad.congestionCurve, delay.vOverC);
    const junctionKey = junctionKeys.get(segId);
    let score: number;
    if (junctionKey !== undefined) {
      const junctionSafety = junctionTypeSafetyOf(junctionKey);
      score = wBase * base + wJunction * junctionSafety + wSpeed * (1 - designSpeedPenalty) + wCongestion * congestion;
    } else {
      const renormDenom = wBase + wSpeed + wCongestion;
      score = (wBase * base + wSpeed * (1 - designSpeedPenalty) + wCongestion * congestion) / renormDenom;
    }
    out.set(segId, clampN(score, 0, 1));
  }
  return out;
});

/**
 * AC-3 — flow-weighted (assignedFlowOf) city-wide mean, never a plain
 * segment average. 0 scored segments (no flow anywhere) => neutral 1.0
 * (nothing unsafe exists yet), never NaN.
 */
export const citySafeRoadScoreOf: (s: SimState) => number = memoOnState((s) => {
  const scores = safeRoadScoreOf(s);
  const flows = assignedFlowOf(s);
  let weightedSum = 0;
  let totalFlow = 0;
  const segIds = [...scores.keys()].sort();
  for (const segId of segIds) {
    const flow = flows.get(segId) ?? 0;
    if (flow <= 0) continue;
    weightedSum += scores.get(segId)! * flow;
    totalFlow += flow;
  }
  return totalFlow > 0 ? weightedSum / totalFlow : 1.0;
});

// --- AC-4: integratedTransportScoreOf ---------------------------------------

/**
 * Honest scope-down (ASM-1528): a station "offers an interchange" here iff
 * it is adjacent (stationLinks' own 4-neighbour footprint walk, reused) to
 * a bus_lane_variant/tram_track_variant-CLASS road segment — never a real
 * 400m walk-catchment population figure, which does not exist anywhere in
 * the webconsole today. roadClassIdOfSegment can throw for an unmapped
 * road tier (never happens for a real segmented spec, all of which map via
 * ROAD_CLASS_ID_OF_TIER); caught defensively here since this is a adjacency
 * SCAN over arbitrary neighbouring segments, not a single already-known-good
 * segment lookup.
 */
function stationTouchesInterchangeClass(b: { x: number; y: number }, sp: { w: number; h: number }, idx: { tileToSegment: Map<string, string>; segmentById: Map<string, LineSegment> }): boolean {
  for (let dx = 0; dx < sp.w; dx++) {
    for (let dy = 0; dy < sp.h; dy++) {
      const x = b.x + dx;
      const y = b.y + dy;
      const neighbours = [`${x + 1},${y}`, `${x - 1},${y}`, `${x},${y + 1}`, `${x},${y - 1}`];
      for (const nk of neighbours) {
        const segId = idx.tileToSegment.get(nk);
        if (!segId) continue;
        const seg = idx.segmentById.get(segId);
        if (!seg || seg.kind !== 'road') continue;
        let cls: string;
        try {
          cls = roadClassIdOfSegment(seg);
        } catch {
          continue;
        }
        if (cls === 'bus_lane_variant' || cls === 'tram_track_variant') return true;
      }
    }
  }
  return false;
}

/**
 * AC-4 — share of ONLINE, road-connected stations (stationLinks(s)) that
 * are ALSO adjacent to an interchange-class segment. 0 connected stations
 * => 0 (no interchange possible), never NaN. Cost: one O(connected
 * stations) pass — never O(citizens).
 */
export const interchangeAdjacencyOf: (s: SimState) => number = memoOnState((s) => {
  const links = stationLinks(s);
  const idx = lineSegmentIndexOf(s);
  const stations = s.buildings
    .filter((b) => SPECS[b.spec]?.kind === 'station' && isOnline(s, b) && links.connectedIds.has(b.id))
    .slice()
    .sort((a, b) => a.id - b.id);
  if (stations.length === 0) return 0;
  let interchange = 0;
  for (const b of stations) {
    const sp = SPECS[b.spec]!;
    if (stationTouchesInterchangeClass(b, sp, idx)) interchange++;
  }
  return interchange / stations.length;
});

/**
 * AC-4/AC-7/BUG-925 — the Shannon-entropy KERNEL alone, over an ARBITRARY
 * mode-share vector, normalised by ln(N) where N is the vector's OWN length
 * (never a hand-typed 11 — AC-7's mutant check). A mode with share=0
 * contributes 0 (standard entropy convention), never NaN/-Infinity.
 * Exported (not inlined into modeShareBalanceOf) so the KERNEL itself can be
 * pinned directly against hand-built synthetic vectors (a 2-mode vector, an
 * N-mode even split) in the test suite — BUG-925 found the previous inline
 * version was only ever exercised by SimState fixtures whose real vector is
 * neither exactly 2-wide nor exactly even, so a wrong-kernel mutant
 * (Simpson's share*(1-share) in place of -share*ln(share)) survived because
 * nothing compared the export's OUTPUT to an independently hand-computed
 * number. modeShareBalanceOf below is this kernel applied to the REALISED
 * trip-weighted vector (or, absent any realised trips, the population-keyed
 * ladder row — see that function's own doc comment for why).
 */
export function shannonModeBalanceOf(shares: Record<string, number>): number {
  const modeIds = Object.keys(shares).sort();
  if (modeIds.length === 0) {
    throw registryError(ERR_REWARDS_MODE_SHARE_VECTOR_EMPTY, 'shannonModeBalanceOf received an empty mode-share vector');
  }
  let entropy = 0;
  for (const id of modeIds) {
    const share = shares[id];
    if (share > 0) entropy += -share * Math.log(share);
  }
  const normalized = entropy / Math.log(modeIds.length);
  return clampN(normalized, 0, 1);
}

/**
 * AC-4/BUG-938 (lead ruling r3, reverting r2's BUG-927 fix) — the CITY-WIDE,
 * POPULATION-KEYED mode-share vector.
 *
 * HISTORY: r1 shipped modeShareOf(ladderPointOf(s)) directly — a pure
 * function of s.population, inert to anything built (round-1 finding
 * BUG-927: a bare city and one with 110 road tiles + 2 road-connected
 * stations at the same population were bit-identical). r2 (BUG-927's fix)
 * tried to make it build-sensitive by re-keying each demandForecastOf(s)
 * tile's OWN residentsActual+workersActual against the scale ladder and
 * trip-weighting the per-tile splits — but round 2 measured (BUG-938) that
 * the real per-tile occupancy scale (max ~76,000 for the largest spec, most
 * residential tiles single digits to low thousands) can only ever select
 * the ladder's BOTTOM ~5 of 18 rungs, so EVERY built city collapsed onto
 * the same narrow, car-heavy band regardless of population or land-use
 * scale — a bare city scored HIGHER (0.70-0.84) than any built city
 * (0.52-0.62) at every population tested. That is a hidden population
 * bonus for NOT building, the exact inversion of "reward integrated
 * transport", not a fix.
 *
 * THE RULING: the webconsole has no per-tile (land-use/access-keyed) mode
 * split anywhere — modeShareOf(ladderPointOf(s)) IS the only mode model
 * that exists, and it is population-keyed BY DESIGN (inc2's own AC).
 * Weighting that SAME city-wide vector by each demandForecastOf(s) tile's
 * personTrips and renormalising mathematically REDUCES to the city-wide
 * vector itself (a trip-weighted sum of N copies of one vector, divided by
 * the sum of the weights, is that vector) — so this function returns the
 * honest, population-keyed city row directly rather than performing that
 * redundant per-tile loop. It is NOT build-sensitive today; that is
 * disclosed here, in the acceptance doc, and in the BOW, not hidden behind
 * a synthetic per-tile computation that only LOOKED build-sensitive.
 * FEAT-2326609804 tracks the real prerequisite (a per-tile mode-split model
 * in inc2's demand code), after which this function can source a genuinely
 * realised vector and inc9's integration coupling can be re-wired.
 *
 * Consequence for consumers: integratedTransportScoreOf (below) stays an
 * EXPORTED DIAGNOSTIC only — see engine.ts, where neither the 'Integrated
 * transport' wellbeing part nor an attract multiplier reads it any more
 * this increment (AC-5/AC-6, doc amended).
 */
export const modeShareBalanceOf: (s: SimState) => number = memoOnState((s) => {
  const cityShares = modeShareOf(ladderPointOf(s));
  return shannonModeBalanceOf(cityShares);
});

/** AC-4 — final score = 0.5*interchangeAdjacency + 0.5*modeShareBalance
 * (rewards.json's OWN weights), bounded [0,1]. */
export const integratedTransportScoreOf: (s: SimState) => number = memoOnState((s) => {
  const interchange = interchangeAdjacencyOf(s);
  const modeBalance = modeShareBalanceOf(s);
  return clampN(
    REWARDS.integratedTransport.interchangeAdjacencyWeight * interchange +
      REWARDS.integratedTransport.modeShareBalanceWeight * modeBalance,
    0,
    1,
  );
});
