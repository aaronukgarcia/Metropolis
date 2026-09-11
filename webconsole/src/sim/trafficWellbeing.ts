// FEAT-2326609798 "UNHAPPINESS COUPLING" — r3 REWORK after re-round REJECT
// (row 7612, BUG-887/888/889/890/891), itself a rework of r2 (row 7607,
// BUG-877/878/879/880). Authority:
// docs/planning/acceptance/FEAT-2326609792-inc5.md, AC-1..AC-6, INCLUDING
// the Lead amendments 1-10 at the bottom of that doc (they override the body
// where they conflict — this file follows the amendments, not the original
// §2 coverage-averaging design).
//
// r3 changes (amendments 6-10, see each function's own doc comment for the
// exact fix):
//  - BUG-888: gridlockWeight moved onto the SAME 0-100 points scale as
//    commuteWeight/emergencyResponseWeight (10, not the old [0,1] fraction
//    0.6 that a Math.round() composite could never actually express).
//  - BUG-887: the WHOLE combined traffic penalty is scaled by the existing
//    earlyGameFactor(population) ramp before subtraction
//    (earlyGameScaledTrafficPenaltyWithConfig) — a fresh, population-0 city
//    loses nothing from any of the three terms.
//  - BUG-889: three surviving mutants closed — the cadence-boundary check
//    is now the single exported isTrafficCadenceTickWithConfig (q); the
//    carried-forward gridlock tick history and the mental-weight loader's
//    fail-closed behaviour are both directly pinned in the test file (d)(g).
//  - BUG-890: the two remaining local blend/part copies in engine.ts
//    (wellbeingOf's Crime part, utilitiesWellbeingUnpenalized) now call the
//    shared data.ts wellbeingPartOf — see that function's own doc comment.
//  - BUG-891 (documented, non-blocking): the cadence tick itself is still a
//    known 15x runtime overhead on tick-heavy tests (crime-mechanic.test.mjs
//    measured 2.9s -> 42.8s) — every 30th tick (or the first advance() on a
//    state without a snapshot) runs a full traffic assignment + isochrone
//    pass inside computeTrafficSnapshot below. This is the INTENDED overhead
//    of the cadence design (correctness over per-tick speed); a follow-up
//    item owns reducing it further (a larger cadence, an incremental/
//    dirty-tile assignment, or moving the assignment off the tick path
//    entirely) — not fixed in this rework.
//
// Shape after r2 (amendments 1+2):
//  - The three heavy traffic derivations (commuteTimeDistributionOf,
//    gridlockedSegmentsOf + the trip-weighted share, emergencyCoverageOf)
//    run ONLY inside computeTrafficSnapshot(), called by engine.ts's
//    advance() on a data-sourced CADENCE (trafficRecomputeTicks, data/
//    traffic.json) or when SimState.trafficSnapshot is absent (BUG-877).
//    Every exported "*WellbeingPartOf"/"*PenaltyOf" function below reads
//    ONLY s.trafficSnapshot — none of them call the heavy derivations
//    directly, so no traffic assignment runs on a non-cadence tick.
//  - The three terms are PENALTY terms in [0,1] (0 = no penalty), combined
//    as a single weighted sum and SUBTRACTED from the wellbeing composite
//    AFTER the existing parts mean (BUG-879) — they no longer participate
//    in the equal-weight average that an added "coverage" row would
//    otherwise raise. They still render as three {label,value} parts (via
//    the SHARED engine.ts `wellbeingPartOf` export, BUG-880 — no more
//    locally duplicated part()/blend()).
//
// PURE + DETERMINISTIC (GR#21): every exported derivation is either a cheap
// pure function of its explicit arguments or memoOnState over SimState — no
// wall-clock read, no PRNG, no browser-storage read. AC-4's no-cycle
// argument: commuteTimeDistributionOf, gridlockedSegmentsOf's ticks snapshot
// and emergencyCoverageOf are pure functions of buildings/population/
// segments (see each module's own doc comment), never of wellbeing —
// mirroring congestionFactorOf's argument verbatim (data.ts:3704-3714). This
// file itself is included in the no-cycle grep (amendment 5): it never reads
// any overall-wellbeing-derived value or the approval score back off state.

import type { SimState } from './types.ts';
import { memoOnState, wellbeingPartOf, earlyGameFactor, SPECS } from './data.ts';
import { commuteTimeDistributionOf, gridlockedSegmentsOf, tilePathsOf, tileVehicleTripsOf, fuelLitresDemandedOf, vedAnnualGbpOf, wearSegmentInputsOf } from './trafficAssignment.ts';
import type { WearSegmentInput } from './trafficAssignment.ts';
import { emergencyCoverageOf } from './emergencyResponse.ts';
// BUG-892 fix (r4) — the emergency-response penalty must only apply once the
// player can actually build an ambulance station (ASM-1518 amendment: "a
// city that cannot yet build a station is not punished for lacking one").
// specUnlocked lives in engine.ts, which itself imports THIS module
// (computeTrafficSnapshot etc.) — this is the SAME function-only (call-time)
// cyclic import data.ts already uses for specUnlocked (see data.ts's own
// header comment: "neither module uses the other at module-eval time, so
// ESM live bindings resolve it safely"). specUnlocked is called only inside
// trafficPenaltyOf/emergencyWellbeingPartOf below (memoOnState callbacks,
// invoked per-tick at runtime, never at either module's top-level
// module-eval time), so the cycle is safe by the same argument.
import { specUnlocked } from './engine.ts';

/** Local clamp — trafficWellbeing.ts's own [lo,hi] bound, no cycle risk (a
 * one-line pure helper, not worth adding an import for). */
function clampN(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}

// --- Registry error codes (GR#7) --------------------------------------------
// V921-V923 claimed by r1. MET-V924 is ALREADY OWNED by emergencyResponse.ts
// (EmergencyServiceEntryMissing) -- this rework claims a FRESH code via
// `node tools/plan/add-error.js claim-range ui.webconsole --size 1` (BUG-877's
// new cadence field) rather than colliding with it (see
// metropolis-parallel-lane-error-code-collision.md).
export const ERR_WELLBEING_TRAFFIC_DATA_MISSING = 'MET-V921'; // WellbeingTrafficDataMissing
export const ERR_WELLBEING_COMMUTE_ANCHOR_INVALID = 'MET-V922'; // WellbeingCommuteAnchorInvalid
export const ERR_WELLBEING_GRIDLOCK_WEIGHT_MISSING = 'MET-V923'; // WellbeingGridlockWeightMissing
export const ERR_TRAFFIC_RECOMPUTE_TICKS_INVALID = 'MET-V883'; // TrafficRecomputeTicksInvalid

function registryError(code: string, message: string): Error {
  return new Error(`${code}: ${message}`);
}

// --- data/wellbeing.json mental-block typed view ----------------------------
// GR#15: every constant below is sourced from the mirror, never hand-typed.
// BUG-878 fix: every function that consumes these values takes the config as
// an EXPLICIT argument (never a closed-over module constant read implicitly)
// so a test can pass a scratch config and prove the value is actually used,
// not hardcoded.

export interface MentalWellbeingConfig {
  /**
   * BUG-894 fix (r4) — this is the webconsole's OWN commute-PENALTY weight,
   * sourced from wellbeing.json's mental.trafficCommutePenaltyWeight, which
   * is a SEPARATE field from mental.commuteWeight (the pre-existing Go
   * engine's own mental-wellbeing driver weight, on a different scale and
   * left untouched). The two fields no longer share a name or a value, so a
   * balance-pass edit to one can never silently retune the other (the r3
   * regression this bug closed).
   */
  commuteWeight: number;
  commuteThresholdMinutes: number;
  commuteStressAtThreshold: number;
  commuteStressAt100Minutes: number;
  gridlockWeight: number;
  emergencyResponseWeight: number;
  /** BUG-895 fix (r4) — cap on medianCommuteMinutes before it can reach
   * debug.json / the player-visible snapshot; sourced from
   * mental.commuteMinutesClampMax (e.g. 1440 minutes, one day). */
  commuteMinutesClampMax: number;
}

function finiteNumber(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null;
}

/**
 * Pure loader over a raw argument, directly testable with a scratch object —
 * no module-load-time side effect to work around (trafficAssignment.ts's
 * loadTrafficConfigFrom precedent).
 */
export function loadMentalWellbeingConfigFrom(raw: unknown): MentalWellbeingConfig {
  if (typeof raw !== 'object' || raw === null) {
    throw registryError(ERR_WELLBEING_TRAFFIC_DATA_MISSING, 'wellbeing.json has no top-level object');
  }
  const mental = (raw as Record<string, unknown>).mental;
  if (typeof mental !== 'object' || mental === null) {
    throw registryError(ERR_WELLBEING_TRAFFIC_DATA_MISSING, 'wellbeing.json has no mental block');
  }
  const m = mental as Record<string, unknown>;

  // BUG-894 fix (r4): the webconsole's OWN commute-penalty weight comes from
  // mental.trafficCommutePenaltyWeight, NOT mental.commuteWeight (that field
  // is the Go engine's own mental-wellbeing driver weight, a different scale,
  // left untouched -- see MentalWellbeingConfig's own doc comment).
  const commuteWeight = finiteNumber(m.trafficCommutePenaltyWeight);
  if (commuteWeight === null || commuteWeight < 0) {
    throw registryError(ERR_WELLBEING_COMMUTE_ANCHOR_INVALID, 'trafficCommutePenaltyWeight');
  }
  const threshold = finiteNumber(m.commuteThresholdMinutes);
  if (threshold === null || threshold <= 0) {
    throw registryError(ERR_WELLBEING_COMMUTE_ANCHOR_INVALID, 'commuteThresholdMinutes');
  }
  const stressAtThreshold = finiteNumber(m.commuteStressAtThreshold);
  if (stressAtThreshold === null || stressAtThreshold < 0) {
    throw registryError(ERR_WELLBEING_COMMUTE_ANCHOR_INVALID, 'commuteStressAtThreshold');
  }
  const stressAt100 = finiteNumber(m.commuteStressAt100Minutes);
  if (stressAt100 === null || stressAt100 <= 0) {
    throw registryError(ERR_WELLBEING_COMMUTE_ANCHOR_INVALID, 'commuteStressAt100Minutes');
  }
  const gridlockWeight = finiteNumber(m.gridlockWeight);
  if (gridlockWeight === null || gridlockWeight < 0) {
    throw registryError(ERR_WELLBEING_GRIDLOCK_WEIGHT_MISSING, 'gridlockWeight');
  }
  const emergencyResponseWeight = finiteNumber(m.emergencyResponseWeight);
  if (emergencyResponseWeight === null || emergencyResponseWeight < 0) {
    throw registryError(ERR_WELLBEING_GRIDLOCK_WEIGHT_MISSING, 'emergencyResponseWeight');
  }
  // BUG-895 fix (r4): fail-closed on commuteMinutesClampMax the same way as
  // every other anchor -- missing/NaN/negative/zero all reject.
  const commuteMinutesClampMax = finiteNumber(m.commuteMinutesClampMax);
  if (commuteMinutesClampMax === null || commuteMinutesClampMax <= 0) {
    throw registryError(ERR_WELLBEING_COMMUTE_ANCHOR_INVALID, 'commuteMinutesClampMax');
  }

  return {
    commuteWeight,
    commuteThresholdMinutes: threshold,
    commuteStressAtThreshold: stressAtThreshold,
    commuteStressAt100Minutes: stressAt100,
    gridlockWeight,
    emergencyResponseWeight,
    commuteMinutesClampMax,
  };
}

// data/wellbeing.json mirror this module reads (ASM-1519, BUG-860 mechanism).
import rawWellbeing from './traffic-data/wellbeing.json' with { type: 'json' };
function loadMentalWellbeingConfig(): MentalWellbeingConfig {
  return loadMentalWellbeingConfigFrom(rawWellbeing);
}
const MENTAL = loadMentalWellbeingConfig();

// --- data/traffic.json trafficRecomputeTicks (BUG-877 cadence) -------------
// Loaded independently of trafficAssignment.ts's own TrafficConfig loader
// (that file is upstream/lead-owned this cycle) — same mirror, same
// fail-closed GR#15 shape, scoped to the one field this module needs.

import rawTraffic from './traffic-data/traffic.json' with { type: 'json' };

export function loadTrafficRecomputeTicksFrom(raw: unknown): number {
  if (typeof raw !== 'object' || raw === null) {
    throw registryError(ERR_TRAFFIC_RECOMPUTE_TICKS_INVALID, 'traffic.json has no top-level object');
  }
  const v = (raw as Record<string, unknown>).trafficRecomputeTicks;
  if (typeof v !== 'number' || !Number.isFinite(v) || v < 1 || Math.floor(v) !== v) {
    throw registryError(ERR_TRAFFIC_RECOMPUTE_TICKS_INVALID, 'trafficRecomputeTicks must be a positive integer');
  }
  return v;
}

export const TRAFFIC_RECOMPUTE_TICKS: number = loadTrafficRecomputeTicksFrom(rawTraffic);

/**
 * BUG-889(q) fix — the cadence-boundary predicate as a PURE function taking
 * `ticksPerCadence` as an explicit argument, so a test can drive it with a
 * SCRATCH cadence (e.g. 7) and prove a hardcoded-30 implementation reds: at
 * tick=7/ticksPerCadence=7 this must return true (cadence boundary), while
 * a hardcoded `tick % 30 === 0` implementation would return false. engine.ts's
 * advance() calls this with the real TRAFFIC_RECOMPUTE_TICKS constant — this
 * export is the ONLY place the modulo check is written (single source, no
 * second inline copy in engine.ts).
 */
export function isTrafficCadenceTickWithConfig(tick: number, hasSnapshot: boolean, ticksPerCadence: number): boolean {
  return tick % ticksPerCadence === 0 || !hasSnapshot;
}

// --- BUG-877: the traffic snapshot (cadence-refreshed, never per-tick) -----

export interface TrafficSnapshot {
  tick: number;
  medianCommuteMinutes: number;
  /** trip-weighted share of routed person-trips crossing >=1 gridlocked segment, [0,1]. */
  gridlockShare: number;
  /** ambulance coverageShare, or null for honest zero-station absence (ASM-1518). */
  coverageShare: number | null;
  /**
   * FEAT-2326609800 inc7 r3 (BUG-929) — the money-path inputs that used to
   * be recomputed from the live traffic assignment EVERY tick now refresh
   * only on this same cadence. Optional: a pre-inc7 snapshot (or a sanitized
   * field that failed validation) simply lacks these — engine.ts's own
   * fuelLitresDemandedFor/vedAnnualGbpFor/roadWearStepOf fall back to a
   * fresh bootstrap compute when absent, exactly like this snapshot's other
   * three fields do when the WHOLE snapshot is absent.
   */
  fuelLitresDemanded?: number;
  /** AC-3 — total annual VED (GBP/year, not yet divided by TICKS_PER_YEAR). */
  vedAnnualGbp?: number;
  /** AC-4/AC-5 — per-segment wear-accrual inputs, see WearSegmentInput's own doc. */
  wearSegments?: Record<string, WearSegmentInput>;
}

/**
 * GR#16: coerce an untrusted/legacy value into a well-formed TrafficSnapshot
 * or `undefined` (absent — triggers a fresh compute on the next advance(),
 * exactly like a state that never had the field at all).
 */
export function sanitizeTrafficSnapshot(v: unknown): TrafficSnapshot | undefined {
  if (typeof v !== 'object' || v === null) return undefined;
  const o = v as Record<string, unknown>;
  const tick = finiteNumber(o.tick);
  const medianCommuteMinutes = finiteNumber(o.medianCommuteMinutes);
  const gridlockShare = finiteNumber(o.gridlockShare);
  if (tick === null || medianCommuteMinutes === null || gridlockShare === null) return undefined;
  let coverageShare: number | null;
  if (o.coverageShare === null) {
    coverageShare = null;
  } else {
    const cs = finiteNumber(o.coverageShare);
    if (cs === null) return undefined;
    coverageShare = Math.max(0, Math.min(1, cs));
  }
  const out: TrafficSnapshot = {
    tick: Math.max(0, Math.floor(tick)),
    // BUG-895 fix (r4): medianCommuteMinutes clamps to the data-sourced
    // mental.commuteMinutesClampMax (both shares already clamped to [0,1]
    // above) -- an oversized value (e.g. an unrouted/degenerate 1e9) never
    // persists to debug.json.
    medianCommuteMinutes: Math.max(0, Math.min(MENTAL.commuteMinutesClampMax, medianCommuteMinutes)),
    gridlockShare: Math.max(0, Math.min(1, gridlockShare)),
    coverageShare,
  };

  // FEAT-2326609800 inc7 r3 (BUG-929, GR#16): the three new cadence-cached
  // money-path fields are each independently optional -- a bad/missing
  // field is simply DROPPED (engine.ts's own bootstrap fallback then
  // recomputes it fresh), never enough on its own to invalidate the
  // required core fields above (which is what would send the WHOLE
  // snapshot to `undefined`, forcing a redundant assignment on cadence
  // fields that were actually fine).
  const fuelLitresDemanded = finiteNumber(o.fuelLitresDemanded);
  if (fuelLitresDemanded !== null && fuelLitresDemanded >= 0) out.fuelLitresDemanded = fuelLitresDemanded;
  const vedAnnualGbp = finiteNumber(o.vedAnnualGbp);
  if (vedAnnualGbp !== null && vedAnnualGbp >= 0) out.vedAnnualGbp = vedAnnualGbp;
  if (typeof o.wearSegments === 'object' && o.wearSegments !== null && !Array.isArray(o.wearSegments)) {
    const wearSegments: Record<string, WearSegmentInput> = {};
    for (const [segId, raw] of Object.entries(o.wearSegments as Record<string, unknown>)) {
      if (typeof raw !== 'object' || raw === null) continue;
      const r = raw as Record<string, unknown>;
      const roadClassId = typeof r.roadClassId === 'string' && r.roadClassId.length > 0 ? r.roadClassId : null;
      const deltaEsalPerTick = finiteNumber(r.deltaEsalPerTick);
      if (roadClassId === null || deltaEsalPerTick === null || deltaEsalPerTick < 0) continue;
      wearSegments[segId] = { roadClassId, deltaEsalPerTick };
    }
    out.wearSegments = wearSegments;
  }
  return out;
}

/**
 * BUG-877 fix — the ONLY place the three heavy traffic derivations
 * (commuteTimeDistributionOf, gridlockedSegmentsOf + trip-weighted share,
 * emergencyCoverageOf) are called. Sole caller: engine.ts's advance(), gated
 * behind the cadence check (tick % TRAFFIC_RECOMPUTE_TICKS === 0, or the
 * snapshot is absent). Also advances `gridlockTicksBySegment` in the SAME
 * call (folds the r1 double-call the round flagged) — the sustained-gridlock
 * tick counter now advances once per CADENCE window rather than once per
 * tick, an approximation the Lead amendment explicitly directs.
 */
export function computeTrafficSnapshot(
  s: SimState,
  tick: number,
  prevGridlockTicksBySegment: Record<string, number>
): { snapshot: TrafficSnapshot; gridlockTicksBySegment: Record<string, number> } {
  const { medianMinutes } = commuteTimeDistributionOf(s);
  const { gridlocked, ticks } = gridlockedSegmentsOf(s, prevGridlockTicksBySegment);
  const gridlockedSet = new Set(gridlocked);
  const tilePaths = tilePathsOf(s);
  const tileVehicleTrips = tileVehicleTripsOf(s);

  let gridlockedWeight = 0;
  let totalWeight = 0;
  // Order-independent fold over tilePaths' own keys (GR#21, no
  // map-range-with-break — see metropolis-map-range-break-gotcha.md).
  for (const [tileKey, path] of tilePaths) {
    const weight = tileVehicleTrips.get(tileKey) ?? 0;
    totalWeight += weight;
    if (path.some((segId) => gridlockedSet.has(segId))) gridlockedWeight += weight;
  }
  const gridlockShare = totalWeight > 0 ? gridlockedWeight / totalWeight : 0;
  const coverageShare = emergencyCoverageOf(s, 'ambulance').coverageShare;

  // FEAT-2326609800 inc7 r3 (BUG-929) — the money-path inputs ride the SAME
  // cadence window: computed here (inside the already-cadence-gated call),
  // never again from the per-tick money path (engine.ts's
  // fuelLitresDemandedFor/vedAnnualGbpFor/roadWearStepOf all read these
  // cached fields first, falling back to a fresh compute only when absent).
  const fuelLitresDemanded = fuelLitresDemandedOf(s);
  const vedAnnualGbp = vedAnnualGbpOf(s);
  const wearSegments = wearSegmentInputsOf(s);

  return {
    snapshot: { tick, medianCommuteMinutes: medianMinutes, gridlockShare, coverageShare, fuelLitresDemanded, vedAnnualGbp, wearSegments },
    gridlockTicksBySegment: ticks,
  };
}

// --- commute-time stress curve (pure, config-injected — BUG-878) ----------

/**
 * AC-1 (§2.1) — piecewise-linear stress curve anchored at (0,0),
 * (commuteThresholdMinutes, commuteStressAtThreshold) and
 * (100, commuteStressAt100Minutes), clamped to [0, commuteStressAt100Minutes]
 * beyond 100 minutes. Config is an EXPLICIT parameter (BUG-878: a hardcoded
 * anchor cannot pass a scratch-mirror-value test that varies this argument).
 */
export function commuteStressWithConfig(medianMinutes: number, cfg: MentalWellbeingConfig): number {
  const { commuteThresholdMinutes: T, commuteStressAtThreshold: S1, commuteStressAt100Minutes: S100 } = cfg;
  const m = Math.max(0, medianMinutes);
  let stress: number;
  if (m <= T) {
    stress = (m / T) * S1;
  } else if (m <= 100) {
    stress = S1 + ((m - T) / (100 - T)) * (S100 - S1);
  } else {
    stress = S100;
  }
  return Math.max(0, Math.min(S100, stress));
}

/** Thin wrapper over commuteStressWithConfig using the real mirrored config. */
export function commuteStressOf(medianMinutes: number): number {
  return commuteStressWithConfig(medianMinutes, MENTAL);
}

// --- BUG-879: penalty terms (each [0,1], 0 = no penalty), config-injected -

/** Normalised commute penalty: stress / commuteStressAt100Minutes, in [0,1]. */
export function commutePenaltyWithConfig(medianMinutes: number, cfg: MentalWellbeingConfig): number {
  const stress = commuteStressWithConfig(medianMinutes, cfg);
  return clampN(stress / cfg.commuteStressAt100Minutes, 0, 1);
}

/** Gridlock penalty is the trip-weighted share itself, already [0,1]. */
export function gridlockPenaltyOf(gridlockShare: number): number {
  return clampN(gridlockShare, 0, 1);
}

/**
 * Emergency penalty: 1 - coverage (null/no-station honest-absence = worst,
 * ASM-1518) -- BUT ONLY once the player can actually build an ambulance
 * station (BUG-892 fix, r4, Lead amendment 1 after round-3 REJECT row 7613:
 * "a city that cannot yet build a station is not punished for lacking
 * one"). `ambulanceUnlocked` defaults to true so every pre-r4 call site
 * (and every test that predates this gate) keeps its exact prior
 * behaviour -- the gate is opt-OUT only via an explicit `false`, never a
 * silent behaviour change for an unaware caller.
 */
export function emergencyPenaltyOf(coverageShare: number | null, ambulanceUnlocked: boolean = true): number {
  if (!ambulanceUnlocked) return 0;
  const coverage = coverageShare === null ? 0 : coverageShare;
  return clampN(1 - coverage, 0, 1);
}

/**
 * BUG-879 (Lead amendment 2) — the single weighted traffic penalty, summed
 * from the three [0,1] penalty terms via their data-sourced weights. Config
 * is explicit (BUG-878) so a scratch-weight test can prove the sum actually
 * reads the mirror. Reads ONLY the snapshot — never calls a heavy traffic
 * derivation (BUG-877).
 */
export function trafficPenaltyWithConfig(
  snapshot: TrafficSnapshot | undefined,
  cfg: MentalWellbeingConfig,
  ambulanceUnlocked: boolean = true
): number {
  const medianCommuteMinutes = snapshot ? snapshot.medianCommuteMinutes : 0;
  const gridlockShare = snapshot ? snapshot.gridlockShare : 0;
  const coverageShare = snapshot ? snapshot.coverageShare : null;
  const commutePenalty = commutePenaltyWithConfig(medianCommuteMinutes, cfg);
  const gridlockPenalty = gridlockPenaltyOf(gridlockShare);
  const emergencyPenalty = emergencyPenaltyOf(coverageShare, ambulanceUnlocked);
  return (
    cfg.commuteWeight * commutePenalty +
    cfg.gridlockWeight * gridlockPenalty +
    cfg.emergencyResponseWeight * emergencyPenalty
  );
}

/**
 * BUG-887 fix (r3, Lead amendment 7) — the RAW weighted penalty above scales
 * with population via the SAME earlyGameFactor(population) ramp every other
 * wellbeing part already uses (data.ts, imported not copied per GR#3): at
 * population 0 the whole traffic penalty is 0 (a fresh city with no ambulance
 * station yet loses nothing), and once earlyGameFactor reaches 1 the raw
 * penalty applies in full, unscaled. Config and population are both explicit
 * arguments (BUG-878 shape) so a scratch config/population pair proves the
 * scaling is genuinely wired, not hardcoded.
 */
export function earlyGameScaledTrafficPenaltyWithConfig(
  snapshot: TrafficSnapshot | undefined,
  cfg: MentalWellbeingConfig,
  population: number,
  ambulanceUnlocked: boolean = true
): number {
  return trafficPenaltyWithConfig(snapshot, cfg, ambulanceUnlocked) * earlyGameFactor(population);
}

/**
 * Thin wrapper over earlyGameScaledTrafficPenaltyWithConfig using the real
 * mirrored config, s.trafficSnapshot, s.population and (BUG-892 fix, r4)
 * s's own ambulance-unlock state via the SAME specUnlocked gate every other
 * unlock check in the codebase uses (never a second copy of the unlock
 * rule).
 */
export const trafficPenaltyOf: (s: SimState) => number = memoOnState((s) =>
  earlyGameScaledTrafficPenaltyWithConfig(s.trafficSnapshot, MENTAL, s.population, specUnlocked(s, SPECS.hea_ambulance))
);

/**
 * The maximum penalty this increment can express — the sum of the three
 * weights (Lead amendment 2: "the maximum achievable penalty equals the sum
 * of the three weights").
 */
export function maxTrafficPenaltyWithConfig(cfg: MentalWellbeingConfig): number {
  return cfg.commuteWeight + cfg.gridlockWeight + cfg.emergencyResponseWeight;
}

// --- display parts (rendered via the SHARED engine.ts part()/blend(), BUG-880) --
// These three still render as {label,value} rows in buildServiceWellbeingParts
// (100 - penalty*100, run through the SAME wellbeingPartOf early-game blend
// every other row uses) but are EXCLUDED from the parts mean that feeds the
// composite — see engine.ts's compositeWithTrafficPenalty. The three labels
// below are the SSOT for "which parts are traffic penalties, excluded from
// the mean" — engine.ts imports this array rather than re-typing the strings.

export const TRAFFIC_PENALTY_PART_LABELS = ['Commute time', 'Gridlock', 'Emergency response'] as const;

export const commuteWellbeingPartOf: (s: SimState) => number = memoOnState((s) => {
  const medianMinutes = s.trafficSnapshot ? s.trafficSnapshot.medianCommuteMinutes : 0;
  const penalty = commutePenaltyWithConfig(medianMinutes, MENTAL);
  return wellbeingPartOf(1 - penalty, s.population);
});

export const gridlockWellbeingPartOf: (s: SimState) => number = memoOnState((s) => {
  const share = s.trafficSnapshot ? s.trafficSnapshot.gridlockShare : 0;
  const penalty = gridlockPenaltyOf(share);
  return wellbeingPartOf(1 - penalty, s.population);
});

export const emergencyWellbeingPartOf: (s: SimState) => number = memoOnState((s) => {
  const coverageShare = s.trafficSnapshot ? s.trafficSnapshot.coverageShare : null;
  // BUG-892 fix (r4): the DISPLAY row uses the SAME ambulance-unlock gate as
  // the penalty it renders -- a locked city shows the neutral (best-case)
  // display value, never a value implying a station it cannot yet build.
  const penalty = emergencyPenaltyOf(coverageShare, specUnlocked(s, SPECS.hea_ambulance));
  return wellbeingPartOf(1 - penalty, s.population);
});

/**
 * BUG-879 (Lead amendment 2) — apply the traffic penalty to a wellbeing
 * composite AFTER its existing parts mean, clamped [0,100]. `parts` must be
 * the FULL parts list including the three traffic-penalty display rows
 * (TRAFFIC_PENALTY_PART_LABELS) — they are excluded from the mean here, not
 * by the caller, so every composite consumer (wellbeingPreApprovalOf,
 * wellbeingCoreOf, wellbeingOf) applies the exact same rule.
 */
export function compositeWithTrafficPenalty(parts: { label: string; value: number }[], s: SimState): number {
  const nonTraffic = parts.filter((p) => !(TRAFFIC_PENALTY_PART_LABELS as readonly string[]).includes(p.label));
  const mean = nonTraffic.length > 0 ? nonTraffic.reduce((a, p) => a + p.value, 0) / nonTraffic.length : 55;
  const penalty = trafficPenaltyOf(s);
  return Math.max(0, Math.min(100, Math.round(mean - penalty)));
}
