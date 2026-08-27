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

/** Coefficients for fiscal calculations (PLACEHOLDER, balance-number regime). */
export const FISCAL_COEFFICIENTS = {
  /** Council tax: effective rate per citizen (as % of tax rate). */
  councilTaxEffectiveRate: 2,
  /** Business tax: fraction of catalogue rate per commercial zone. */
  businessTaxFraction: 0.4,
  /** Wages: cost per citizen per tick. */
  wagesPerCitizen: 0.5,
} as const;
