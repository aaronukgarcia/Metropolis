// fiscal.ts — FEAT-1972079890 round-4: single source of truth for fiscal formulas
//
// Extracted from engine.ts, consistency.ts, debugjson.ts to eliminate formula
// duplication and prevent checker drift. All coefficients are PLACEHOLDER
// (balance-number regime) — Aaron's row-by-row approval pending.

import type { FlowItem, PolicyId, ZoneKind } from './types.ts';

/**
 * Council tax per tick: population * rate.residential * 2 / 100
 * PLACEHOLDER (balance-number regime): 2% effective rate per citizen.
 */
export function councilTaxPerTick(population: number, taxRate: number): number {
  return Math.round((population * taxRate * 2) / 100);
}

/**
 * Business tax per tick: commercial zones * rate.commercial * 0.4
 * PLACEHOLDER (balance-number regime): ~40% of the catalogue rate per zone.
 */
export function businessTaxPerTick(commercialZones: number, taxRate: number): number {
  return Math.round(commercialZones * taxRate * 0.4);
}

/**
 * Wages per tick: population * 0.5
 * PLACEHOLDER (balance-number regime): directional cost per citizen.
 */
export function wagesPerTick(population: number): number {
  return Math.round(population * 0.5);
}

/**
 * Grid Export tariff per MW sold to the regional grid (PLACEHOLDER, balance-number regime).
 * Aaron's row-by-row balance sign-off pending. Suggested ~1.6 from the ~9,920/tick upkeep
 * over ~6,095 MW basis (cost-of-service tariff model).
 */
export const GRID_EXPORT_TARIFF_PER_MW = 1.6;

/**
 * Calculate Grid Export revenue per tick.
 * Returns (capMW - needMW) * tariff if cap > need, else 0.
 * Pure function, deterministic.
 */
export function gridExportRevenuePerTick(capMW: number, needMW: number, tariff: number): number {
  const exportMW = Math.max(0, capMW - needMW);
  return Math.round(exportMW * tariff);
}

/**
 * SINGLE SOURCE OF TRUTH (BUG-422 / GR#3): the ONLY place the engine's post-policy
 * outflow multipliers live, shared by engine.computeFlows and the consistency checker
 * so the checker's recompute matches what the engine actually recorded.
 *
 * Two adjustments, applied in this exact order (matching engine.computeFlows):
 *   1. recycling → the discounted service labels below are multiplied by 0.93 (rounded);
 *   2. austerity → EVERY outflow is multiplied by 0.9 (rounded).
 * Both stack (recycling's rounded result is then austerity-rounded). Pure, order-preserving,
 * returns new FlowItem entries.
 */
export const RECYCLING_DISCOUNT_LABELS: ReadonlySet<string> = new Set<string>([
  'Roads',
  'Power Grid',
  'Water & Waste',
  'Healthcare',
  'Education',
  'Parks',
  'Policing',
]);

export function applyOutflowPolicies(
  outflows: FlowItem[],
  policies: Record<PolicyId, boolean>,
): FlowItem[] {
  let result = outflows;
  if (policies.recycling) {
    result = result.map((o) =>
      RECYCLING_DISCOUNT_LABELS.has(o.label) ? { ...o, value: Math.round(o.value * 0.93) } : o,
    );
  }
  if (policies.austerity) {
    result = result.map((o) => ({ ...o, value: Math.round(o.value * 0.9) }));
  }
  return result;
}

/**
 * SINGLE SOURCE OF TRUTH (BUG-422 / GR#3): maps a building's ZoneKind to the outflow
 * bucket label its upkeep is charged under. Shared by engine.computeFlows (to build the
 * recorded per-bucket outflows) and the consistency checker (to recompute them with the
 * same labels, so per-label policy discounts line up). Moved here from engine.ts.
 */
export const UPKEEP_BUCKET: Partial<Record<ZoneKind, string>> = {
  road: 'Roads',
  pylon: 'Power Grid',
  power: 'Power Grid',
  water: 'Water & Waste',
  health: 'Healthcare',
  school: 'Education',
  police: 'Policing',
  park: 'Parks',
  residential: 'Housing',
  commercial: 'Commerce & Industry',
  office: 'Commerce & Industry',
  industrial: 'Commerce & Industry',
  mine: 'Commerce & Industry',
  station: 'Transport',
  landmark: 'Civic & Landmarks',
  // FEAT-1972079877: placeholder catalogue kinds — without these buckets the
  // new structures' upkeep would silently vanish from the outflows.
  transport: 'Transport',
  fire: 'Fire & Rescue',
  civic: 'Civic & Justice',
  leisure: 'Leisure',
};

/** Coefficients for fiscal calculations (PLACEHOLDER, balance-number regime). */
export const FISCAL_COEFFICIENTS = {
  /** Council tax: effective rate per citizen (as % of tax rate). */
  councilTaxEffectiveRate: 2,
  /** Business tax: fraction of catalogue rate per commercial zone. */
  businessTaxFraction: 0.4,
  /** Wages: cost per citizen per tick. */
  wagesPerCitizen: 0.5,
} as const;

/** PLACEHOLDER (balance-number regime). BUG-438: never apply this to |funds| uncapped. */
export const OVERDRAFT_PER_TICK = 0.004;

export function overdraftInterestPerTick(funds: number, otherOutflowSum: number): number {
  if (!(funds < 0) || !Number.isFinite(funds)) return 0;
  const raw = Math.round(Math.abs(funds) * OVERDRAFT_PER_TICK);
  const cap = Math.max(otherOutflowSum, 1);
  if (!Number.isFinite(raw)) return cap;
  return Math.min(raw, cap);
}

export function sanitizeFunds(n: number): number {
  if (!Number.isFinite(n)) return 0;
  const i = Math.trunc(n);
  return Number.isSafeInteger(i) ? i : 0;
}
