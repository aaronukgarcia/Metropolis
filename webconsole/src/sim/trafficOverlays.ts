// trafficOverlays.ts — FEAT-2326609805 inc10: read-only overlay tint decision
// logic + Transport screen aggregate metrics.
//
// GR#25 DEVIATION NOTE (re-verified against THIS worktree's HEAD, not the
// doc's stale f5ae80e stamp — the doc itself flags this as still-live inc8
// territory, "re-verify at dispatch time"). Three real mismatches found:
//
//  1. demandForecastOf(s)'s TileDemand rows carry NO `modeShareEstimate`
//     field (grepped: trafficDemand.ts:693-705). Mode share is exposed only
//     CITY-WIDE via `modeShareOf(ladderPointOf(s))` (a Record<mode,share>),
//     optionally policy-adjusted via `policyModeShareAdjustmentOf(s)`
//     (trafficDemand.ts:432). There is no per-tile mode split. This module
//     therefore applies the SAME city-wide dominant-mode/alpha to every
//     demand tile with personTrips >= minPersonTrips — an honest
//     city-uniform approximation, not a fabricated per-tile split. The doc's
//     "two adjacent tiles, opposite dominance" fixture is consequently
//     tested at the pure-function level with two SYNTHETIC share vectors
//     (proving the decision function itself discriminates), not with two
//     tiles carrying different real per-tile data (no such data exists).
//
//  2. `fuelAndEVDemandOf(s)` returns `{litresPerDay, evKWhPerDay, byClass}`
//     (parkingFuel.ts:482-490) — a DEMAND total, with NO capacity/shortfall
//     field and no per-tile map (the doc's claimed
//     `{tiles: Map<tileKey,...>}` shape does not exist on HEAD). The only
//     registered shortfall signal for fuel/EV is the binary, city-wide
//     `evChargePointShortfallOf(s)` (parkingFuel.ts:521, ladder-demand-only,
//     supply pinned at the literal 0 per Decision D1/ASM-1525 — no
//     placeable-charger capacity exists yet). This module uses that binary
//     flag as the fuel/EV overlay's presence signal (applied uniformly to
//     every demand tile when set), and reports the two raw demand totals
//     (litres/day, kWh/day) on the Transport screen as INFORMATIONAL
//     read-outs alongside the binary state — never a fabricated
//     demand-vs-capacity percentage that has no real capacity term behind
//     it (GR#1: no invented data).
//
//  3. `parkingShortfallOf(s)` returns `{cityShare: number, perTile: Map<
//     string, number>}` (parkingFuel.ts:374-402) where `perTile` values are
//     ALREADY a bounded [0,1] shortfall FRACTION per tile (not a raw
//     vehicle count as the doc assumed) and `cityShare` is the
//     population-weighted city average of that same fraction. The overlay
//     below reads `perTile` directly (fraction > 0 => shortfall present,
//     exactly the doc's "demand > supply" semantics, just pre-computed as a
//     fraction rather than a raw difference); the Transport screen's
//     "Parking shortfall" row reports `cityShare` as a percentage (the real
//     available aggregate), not a hand-summed vehicle count that would
//     require re-deriving parkingDemandOf/kerbParkingSupplyOf directly —
//     two symbols NOT in the doc's approved GR#25 symbol list.
//
// AC-7 note: the four policy "effects" are NOT uniformly available as
// isolated per-policy numbers. `busPriorityCapacityInfoOf(s)` (inc8) gives
// bus priority its own real capacity-delta figure. The three demand-shaping
// policies (ownershipQuota/roadPricing/integratedTicketing) are only
// exposed as one COMBINED post-adjustment vector via
// `policyModeShareAdjustmentOf(s)` (trafficDemand.ts:432) — there is no
// exported per-policy-isolated share delta. This module reports the SAME
// combined car-share-shift figure against every one of those three policies
// that is currently ON (an honest attribution of a shared, real, measured
// number — never a fabricated split into three independent numbers).
//
// GR#21: every function here is a pure, deterministic function of its
// arguments (no Date.now/Math.random/localStorage, no engine call). GR#15:
// every band edge / alpha comes from data/traffic/overlays.json (mirrored
// in-tree, loaded once at module scope, fail-closed via MET-V945).

import rawOverlays from './traffic-data/overlays.json' with { type: 'json' };
// FEAT-2326609805 inc10 r2 (BUG-956 fix, lead amendment): scoreBandOf's
// band edges must come from the SAME registered RAG table the rest of the
// UI's 0-100 score bands use (ragThresholds.ts's WELLBEING split), never a
// second hand-typed 70/50 pair (GR#3). This is a one-off reverse import
// (sim/ -> components/) explicitly directed by the lead ruling — the
// alternative (moving RAG_THRESHOLDS into sim/) is a bigger blast-radius
// change to a file eleven OTHER call sites already depend on, out of scope
// for this rework.
import { ragForWellbeing } from '../components/ragThresholds.ts';

interface OverlayConfig {
  demand: { paleTrips: number; saturatedTrips: number; paleAlpha: number; saturatedAlpha: number };
  modeShare: { minPersonTrips: number; pureAlpha: number };
  congestion: {
    redThreshold: number;
    yellowThreshold: number;
    redAlpha: number;
    yellowAlpha: number;
    greenAlpha: number;
  };
  parking: { alpha: number };
  fuelEv: { alpha: number };
  wear: { freshAlpha: number; failedAlpha: number; conditionYellowBand: number; conditionRedBand: number };
  colors: { ok: string; hot: string; yellow: string; car: string; transit: string; walk: string };
}

function registryError(code: string, message: string): Error {
  return new Error(`${code}: ${message}`);
}

const ERR_OVERLAY_CONFIG_INVALID = 'MET-V945'; // TrafficOverlayConfigInvalid

function num(path: string, v: unknown): number {
  if (typeof v !== 'number' || !Number.isFinite(v)) {
    throw registryError(ERR_OVERLAY_CONFIG_INVALID, path);
  }
  return v;
}

function loadConfig(raw: unknown): OverlayConfig {
  const r = raw as Record<string, any>;
  const demand = r.demand ?? {};
  const modeShare = r.modeShare ?? {};
  const congestion = r.congestion ?? {};
  const parking = r.parking ?? {};
  const fuelEv = r.fuelEv ?? {};
  const wear = r.wear ?? {};
  const colors = r.colors ?? {};
  for (const k of ['ok', 'hot', 'yellow', 'car', 'transit', 'walk']) {
    if (typeof colors[k] !== 'string' || colors[k].length === 0) {
      throw registryError(ERR_OVERLAY_CONFIG_INVALID, `colors.${k}`);
    }
  }
  return {
    demand: {
      paleTrips: num('demand.paleTrips', demand.paleTrips),
      saturatedTrips: num('demand.saturatedTrips', demand.saturatedTrips),
      paleAlpha: num('demand.paleAlpha', demand.paleAlpha),
      saturatedAlpha: num('demand.saturatedAlpha', demand.saturatedAlpha),
    },
    modeShare: {
      minPersonTrips: num('modeShare.minPersonTrips', modeShare.minPersonTrips),
      pureAlpha: num('modeShare.pureAlpha', modeShare.pureAlpha),
    },
    congestion: {
      redThreshold: num('congestion.redThreshold', congestion.redThreshold),
      yellowThreshold: num('congestion.yellowThreshold', congestion.yellowThreshold),
      redAlpha: num('congestion.redAlpha', congestion.redAlpha),
      yellowAlpha: num('congestion.yellowAlpha', congestion.yellowAlpha),
      greenAlpha: num('congestion.greenAlpha', congestion.greenAlpha),
    },
    parking: { alpha: num('parking.alpha', parking.alpha) },
    fuelEv: { alpha: num('fuelEv.alpha', fuelEv.alpha) },
    wear: {
      freshAlpha: num('wear.freshAlpha', wear.freshAlpha),
      failedAlpha: num('wear.failedAlpha', wear.failedAlpha),
      conditionYellowBand: num('wear.conditionYellowBand', wear.conditionYellowBand),
      conditionRedBand: num('wear.conditionRedBand', wear.conditionRedBand),
    },
    colors: {
      ok: colors.ok,
      hot: colors.hot,
      yellow: colors.yellow,
      car: colors.car,
      transit: colors.transit,
      walk: colors.walk,
    },
  };
}

export const OVERLAY_CONFIG: OverlayConfig = loadConfig(rawOverlays);

function clamp01(x: number): number {
  return x < 0 ? 0 : x > 1 ? 1 : x;
}

export interface Tint {
  color: string;
  alpha: number;
}

// --- AC-1.1: demand overlay --------------------------------------------------

/** Per-tile trip-count tint. Absent (null) at 0 trips (honest-absence); the
 * alpha ramps linearly from `paleAlpha` at `paleTrips` to `saturatedAlpha`
 * at `saturatedTrips` and clamps flat thereafter (AC-1's saturation-cap
 * check: 400 trips must equal 200 trips' alpha exactly). */
export function demandTintOf(trips: number): Tint | null {
  if (!(trips > 0)) return null;
  const { paleTrips, saturatedTrips, paleAlpha, saturatedAlpha } = OVERLAY_CONFIG.demand;
  const frac = clamp01((trips - paleTrips) / (saturatedTrips - paleTrips));
  const alpha = paleAlpha + (saturatedAlpha - paleAlpha) * frac;
  return { color: OVERLAY_CONFIG.colors.hot, alpha };
}

// --- AC-1.2: mode-share overlay ----------------------------------------------

/** Dominant-mode tint for a mode-share vector (city-wide, per the GR#25
 * deviation note above). `personTrips` gates the doc's <50-person-trips
 * honest-absence rule. Alpha is `pureAlpha * dominantShare` — a 100%-car
 * tile paints at full `pureAlpha`; a 50/50 split paints at half. Uses the
 * MAX share (never a mean, AC-1's mutant) to pick the dominant mode. */
export function modeShareTintOf(shares: Record<string, number>, personTrips: number): Tint | null {
  const { minPersonTrips, pureAlpha } = OVERLAY_CONFIG.modeShare;
  if (personTrips < minPersonTrips) return null;
  let dominantMode: string | null = null;
  let dominantShare = -Infinity;
  for (const [mode, share] of Object.entries(shares)) {
    if (share > dominantShare) {
      dominantShare = share;
      dominantMode = mode;
    }
  }
  if (dominantMode === null || !(dominantShare > 0)) return null;
  const color =
    dominantMode === 'car'
      ? OVERLAY_CONFIG.colors.car
      : dominantMode === 'walk'
        ? OVERLAY_CONFIG.colors.walk
        : OVERLAY_CONFIG.colors.transit; // every non-car/non-walk mode (bus/metro/rail/tram/...) reads as "transit"
  return { color, alpha: pureAlpha * clamp01(dominantShare) };
}

// --- AC-1.3: congestion overlay ----------------------------------------------

export type CongestionBand = 'red' | 'yellow' | 'green';

/** Three-band v/c tint. `undefined` (no segmentDelayOf entry, zero flow) is
 * honest absence, per inc3 AC-4. The boundary at `redThreshold` (0.8) is
 * INCLUSIVE on the yellow side (a segment sitting exactly at 0.8 reads
 * yellow, not red) — the doc's own worked boundary fixture is at 0.65,
 * strictly inside the yellow band, so this boundary convention does not
 * affect any Check in the acceptance doc. */
export function congestionTintOf(vOverC: number | undefined): (Tint & { band: CongestionBand }) | null {
  if (vOverC === undefined || !Number.isFinite(vOverC)) return null;
  const { redThreshold, yellowThreshold, redAlpha, yellowAlpha, greenAlpha, } = OVERLAY_CONFIG.congestion;
  const { hot, yellow, ok } = OVERLAY_CONFIG.colors;
  if (vOverC > redThreshold) return { band: 'red', color: hot, alpha: redAlpha };
  if (vOverC >= yellowThreshold) return { band: 'yellow', color: yellow, alpha: yellowAlpha };
  return { band: 'green', color: ok, alpha: greenAlpha };
}

// --- AC-1.4: parking shortfall overlay ---------------------------------------

/** `shortfallFraction` is `parkingShortfallOf(s).perTile`'s own [0,1] value
 * (already `demand > supply` as a bounded fraction — GR#25 deviation note
 * #3). Absent (null) at 0 or below (supply >= demand); flat red otherwise
 * (AC-1's mutant: never paints green for a negative/zero value). */
export function parkingTintOf(shortfallFraction: number): Tint | null {
  if (!(shortfallFraction > 0)) return null;
  return { color: OVERLAY_CONFIG.colors.hot, alpha: OVERLAY_CONFIG.parking.alpha };
}

// --- AC-1.5: fuel/EV shortfall overlay ---------------------------------------

/** City-wide binary presence (GR#25 deviation note #2 — no per-tile fuel/EV
 * capacity data exists). Flat red when the binary flag is truthy, absent
 * otherwise; deliberately takes the RAW evChargePointShortfallOf() flag
 * rather than re-deriving from litres/kWh totals (no capacity term exists
 * to compare those totals against). */
export function fuelEvTintOf(shortfallFlag: number | boolean): Tint | null {
  if (!shortfallFlag) return null;
  return { color: OVERLAY_CONFIG.colors.hot, alpha: OVERLAY_CONFIG.fuelEv.alpha };
}

// --- AC-1.6: road wear overlay -----------------------------------------------

/** `condition` undefined => absent (fresh/unbuilt segment, same visual
 * result as fresh green per the doc's own text). Linear interpolation
 * 1.0 (fresh) -> freshAlpha, 0.0 (failed) -> failedAlpha; NEVER a
 * non-linear warp (AC-1's mutant: condition^2 must NOT match this). */
export function wearTintOf(condition: number | undefined): Tint | null {
  if (condition === undefined || !Number.isFinite(condition)) return null;
  const c = clamp01(condition);
  const { freshAlpha, failedAlpha } = OVERLAY_CONFIG.wear;
  const alpha = freshAlpha + (failedAlpha - freshAlpha) * (1 - c);
  // Colour blends ok -> yellow -> hot across the SAME [0,1] condition axis
  // the alpha uses (never a wall-clock/tick-based colour, GR#21).
  const { ok, yellow, hot } = OVERLAY_CONFIG.colors;
  const color = c >= OVERLAY_CONFIG.wear.conditionYellowBand ? ok : c >= OVERLAY_CONFIG.wear.conditionRedBand ? yellow : hot;
  return { color, alpha };
}

// --- Transport screen aggregate metrics --------------------------------------

/** GR#1/AC-9: Number.isFinite guard with a neutral fallback — NEVER
 * `typeof x === 'number'` (that passes NaN straight through). */
export function finiteOr(v: unknown, fallback: number): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : fallback;
}

/** AC-6 — mean segment condition, clamped [0,1]; a segment-less city (no
 * entries yet) reads 1.0 (all fresh), never 0/NaN. Uses the population
 * mean, never the median (AC-6's mutant: median hides a widespread minor
 * wear problem under one dramatic failure). */
export function averageRoadCondition(roadWearBySegment: Record<string, number> | undefined): number {
  if (!roadWearBySegment) return 1;
  const values = Object.values(roadWearBySegment).filter((v) => typeof v === 'number' && Number.isFinite(v));
  if (values.length === 0) return 1;
  let sum = 0;
  for (const v of values) sum += clamp01(v);
  return clamp01(sum / values.length);
}

export type RoadConditionBand = 'green' | 'yellow' | 'red';

export function roadConditionBandOf(condition: number): RoadConditionBand {
  if (condition >= OVERLAY_CONFIG.wear.conditionYellowBand) return 'green';
  if (condition >= OVERLAY_CONFIG.wear.conditionRedBand) return 'yellow';
  return 'red';
}

export type GridlockBand = 'green' | 'yellow' | 'red';

/** AC-3's gridlock-share bar band — reuses the SAME congestion band edges
 * (0.5/0.8) as the map overlay (GR#3: one threshold table, not a second). */
export function gridlockBandOf(share: number): GridlockBand {
  const { redThreshold, yellowThreshold } = OVERLAY_CONFIG.congestion;
  if (share > redThreshold) return 'red';
  if (share >= yellowThreshold) return 'yellow';
  return 'green';
}

export type ScoreBand = 'green' | 'yellow' | 'red';

/** AC-7 — safe-road score band (0-100 scale). BUG-956 fix: reads
 * RAG_THRESHOLDS.WELLBEING via ragForWellbeing (ragThresholds.ts) — the
 * SAME registered table the wellbeing panel's own 0-100 scores band from —
 * rather than a second hand-typed cut-point pair. If ragThresholds.ts's
 * numbers ever move, this band moves with them automatically; there is no
 * second copy to fall out of sync (GR#3). */
export function scoreBandOf(score0to100: number): ScoreBand {
  const rag = ragForWellbeing(score0to100);
  return rag === 'green' ? 'green' : rag === 'amber' ? 'yellow' : 'red';
}
