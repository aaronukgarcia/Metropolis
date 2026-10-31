// buildingUtility.ts — FEAT-2326609841: per-building utility shares for the
// property card. PRESENTATION ONLY: derives, never invents.
//
// GR#3 / second-model guard. Water and waste-water are CITYWIDE demand models
// (data.ts waterDemandOf -> the 'cleanwater' / 'waste' rows of
// serviceCoverageOf, need = population). There is no stored per-building
// figure. This module is the ONE place that apportions that citywide demand
// to individual buildings, and it does so by largest-remainder apportionment
// over the online housing capacity, so that
//
//     Σ share(b) + unallocated === waterDemandOf(s)   (EXACTLY, clean AND waste)
//
// holds by construction for integer demand (population is an integer count).
// The card and the sim therefore cannot disagree; the identity is a test
// (property-card-utility.test.mjs) and the card renders ONLY what this module
// returns.
//
// TDZ rule: imports FROM data.ts only; data.ts never imports this module.
// Pure / deterministic (GR#21): no clock, no storage, no Math.random, and the
// apportionment tie-break is by building id, so it is independent of the
// order of s.buildings.

import { SPECS, POWER_DRAW_MW_BY_KIND, capacityAtTier, isOnline, memoOnState, waterDemandOf } from './data.ts';
import type { SimState, Building } from './types.ts';

export interface WaterShare {
  /** Clean-water demand attributed to this building (people supplied). */
  clean: number;
  /** Waste-water demand attributed to this building (people sewered). */
  waste: number;
}

export interface UtilityShares {
  /** Per ONLINE housing building id -> its share of the citywide demand. */
  byId: ReadonlyMap<number, WaterShare>;
  /**
   * Demand no online housing could be attributed to (e.g. population with no
   * online dwelling, or a non-integer remainder). Σ byId + unallocated is
   * always the citywide waterDemandOf(s).
   */
  unallocated: WaterShare;
}

/**
 * Largest-remainder apportionment of `total` over `weights` (id -> weight>0).
 * Integer part only; any non-integer residue of `total` is returned as
 * `leftover` so the sum identity never relies on float re-association.
 */
function apportion(
  total: number,
  weights: ReadonlyArray<readonly [number, number]>,
): { shares: Map<number, number>; leftover: number } {
  const shares = new Map<number, number>();
  const safeTotal = Number.isFinite(total) && total > 0 ? total : 0;
  const whole = Math.trunc(safeTotal);
  let weightSum = 0;
  for (const [, w] of weights) weightSum += w;
  if (whole <= 0 || weightSum <= 0) return { shares, leftover: safeTotal };

  const rem: Array<{ id: number; frac: number }> = [];
  let given = 0;
  for (const [id, w] of weights) {
    const exact = whole * w;
    const base = Math.floor(exact / weightSum);
    shares.set(id, base);
    given += base;
    rem.push({ id, frac: exact - base * weightSum });
  }
  // Highest remainder first; ties by lower building id (order-independent).
  rem.sort((a, b) => (b.frac !== a.frac ? b.frac - a.frac : a.id - b.id));
  let left = whole - given;
  for (let i = 0; left > 0 && i < rem.length; i++, left--) {
    shares.set(rem[i].id, (shares.get(rem[i].id) ?? 0) + 1);
  }
  return { shares, leftover: safeTotal - whole };
}

/**
 * Every online housing building's share of the citywide clean/waste demand,
 * computed once per state object (memoOnState — AC-9: no per-render O(N)).
 */
export const utilitySharesOf: (s: SimState) => UtilityShares = memoOnState((s) => {
  const demand = waterDemandOf(s);
  const weights: Array<readonly [number, number]> = [];
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind !== 'residential' || !isOnline(s, b)) continue;
    const w = capacityAtTier(sp, b.capacityTier ?? 0);
    if (w > 0) weights.push([b.id, w]);
  }
  const clean = apportion(demand.clean, weights);
  const waste = apportion(demand.waste, weights);
  const byId = new Map<number, WaterShare>();
  for (const [id] of weights) {
    byId.set(id, { clean: clean.shares.get(id) ?? 0, waste: waste.shares.get(id) ?? 0 });
  }
  // unallocated = demand not placed on any building, derived (never stored).
  let placedClean = 0;
  let placedWaste = 0;
  for (const v of byId.values()) {
    placedClean += v.clean;
    placedWaste += v.waste;
  }
  return {
    byId,
    unallocated: {
      clean: demand.clean - placedClean,
      waste: demand.waste - placedWaste,
    },
  };
});

/**
 * The card's water line for one building: its share if it is online housing,
 * else null ("—": this building draws no water in the citywide model, and the
 * card must not imply a measured zero).
 */
export function buildingWaterShareOf(s: SimState, b: Building): WaterShare | null {
  return utilitySharesOf(s).byId.get(b.id) ?? null;
}

/** Power the card may show for one building. */
export interface PowerFigure {
  kind: 'generation' | 'draw';
  mw: number;
}

/**
 * Power figure from the SAME rules powerStats/the building profile use:
 * a plant reports its tier MW (reactor ladders honoured, identical to
 * computePowerStats); any non-plant spec carrying `mw` reports it as draw.
 * null when the spec has no power figure at all ("—").
 */
export function buildingPowerOf(b: Building): PowerFigure | null {
  const sp = SPECS[b.spec];
  if (!sp) return null;
  if (sp.kind === 'power') {
    const mw = sp.capacityTiers ? capacityAtTier(sp, b.capacityTier ?? 0) : (sp.mw ?? 0);
    return mw > 0 ? { kind: 'generation', mw } : null;
  }
  const kindDraw = (POWER_DRAW_MW_BY_KIND as Record<string, number>)[sp.kind];
  if (kindDraw !== undefined) return { kind: 'draw', mw: kindDraw };
  return (sp.mw ?? 0) > 0 ? { kind: 'draw', mw: sp.mw as number } : null;
}
