// fiscal.ts — FEAT-1972079890 round-4: single source of truth for fiscal formulas
//
// Extracted from engine.ts, consistency.ts, debugjson.ts to eliminate formula
// duplication and prevent checker drift. All coefficients are PLACEHOLDER
// (balance-number regime) — Aaron's row-by-row approval pending.

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

/** Coefficients for fiscal calculations (PLACEHOLDER, balance-number regime). */
export const FISCAL_COEFFICIENTS = {
  /** Council tax: effective rate per citizen (as % of tax rate). */
  councilTaxEffectiveRate: 2,
  /** Business tax: fraction of catalogue rate per commercial zone. */
  businessTaxFraction: 0.4,
  /** Wages: cost per citizen per tick. */
  wagesPerCitizen: 0.5,
} as const;
