// fiscal.ts — FEAT-1972079890 round-4: single source of truth for fiscal formulas
//
// Extracted from engine.ts, consistency.ts, debugjson.ts to eliminate formula
// duplication and prevent checker drift. All coefficients are PLACEHOLDER
// (balance-number regime) — Aaron's row-by-row approval pending.

import type { FlowItem, InsolvencyState, PolicyId, ZoneKind } from './types.ts';

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

/**
 * FEAT-1972079923 inc1 (AC-1) — PLACEHOLDER (balance-number regime): funds at or
 * below this trip the 'crisis' band, the future IMF bailout entry point. Value
 * taken verbatim from the BA criteria's placeholder-constants table
 * (docs/planning/acceptance/feat-1972079923-imf-insolvency.md); Aaron's
 * row-by-row balance approval pending. Named DEBT_THRESHOLD_FOR_BAILOUT (not a
 * generic "insolvency" name) to match the AC doc so the balance pass finds one
 * SSOT constant, not a second table (GR#3).
 */
export const DEBT_THRESHOLD_FOR_BAILOUT = -10_000_000;

/**
 * FEAT-1972079923 inc1 (companion to AC-1, not in the BA doc's placeholder
 * table) — PLACEHOLDER: funds at or below this (but still above
 * DEBT_THRESHOLD_FOR_BAILOUT) trip the 'warning' band, giving the player
 * advance notice before the crisis threshold is crossed so the eventual
 * bailout is not a surprise (task point 3). Balance pass pending.
 */
export const INSOLVENCY_WARNING_THRESHOLD = -5_000_000;

/**
 * FEAT-1972079923 inc1 (AC-1, AC-12) — pure, state-derived insolvency band.
 * No Date/random; deterministic given `funds` alone, so two identical runs
 * produce byte-identical bands and a replay reproduces every band transition.
 */
export function insolvencyStateForFunds(funds: number): InsolvencyState {
  if (funds <= DEBT_THRESHOLD_FOR_BAILOUT) return 'crisis';
  if (funds <= INSOLVENCY_WARNING_THRESHOLD) return 'warning';
  return 'solvent';
}

/**
 * FEAT-1972079923 inc2 (AC-2) — PLACEHOLDER (balance-number regime): the
 * IMF bailout event lasts exactly one game-year. Must equal TICKS_PER_YEAR
 * (engine.ts) — asserted by a test — so the year-end re-evaluation lands on a
 * real calendar boundary, not an arbitrary tick count that drifts from the
 * rest of the sim's yearly cadence.
 */
export const BAILOUT_DURATION_TICKS = 360;

/**
 * FEAT-1972079923 inc2 — PLACEHOLDER (balance-number regime): one-time cash
 * injection credited to the treasury the SAME tick the bailout is entered.
 * Aaron's ruling: this is a legitimate external inflow (like a grant), not
 * manufactured money — booked as a normal labelled inflow (see
 * BAILOUT_INJECTION_LABEL) so conservation (fundsAtTickEnd === fundsAtTickStart
 * + Σinflows − Σoutflows) can trace it exactly like every other inflow.
 */
export const BAILOUT_INCOME_INJECTION = 2_000_000;

/**
 * FEAT-1972079923 inc2 (AC-4) — PLACEHOLDER (balance-number regime): fraction
 * of a building's capital value (placementCost) credited to the treasury on a
 * FORCED asset sale. Mirrors the existing bulldoze-refund pattern (25% of paid
 * cost, engine.ts case 'bulldoze') but at a higher fraction since this is a
 * deliberate sale under bailout conditions, not a demolition refund. Balance
 * pass pending.
 */
export const ASSET_SALE_VALUE_FRACTION = 0.6;

/**
 * FEAT-1972079923 inc2 (AC-4) — SSOT label for the one-time bailout cash
 * injection, so the ledger/lastFlows entry and the conservation/consistency
 * checks always agree on the exact string (GR#3: no duplicated literal).
 */
export const BAILOUT_INJECTION_LABEL = 'IMF Bailout Injection';

/**
 * FEAT-1972079923 inc2 (AC-4) — SSOT label for a forced asset sale's ledger/
 * lastFlows inflow entry (see BAILOUT_INJECTION_LABEL for the rationale).
 */
export const ASSET_SALE_LABEL = 'Forced Asset Sale';

/**
 * FEAT-1972079923 inc3 (AC-7) — PLACEHOLDER (balance-number regime):
 * Administration Mode lasts exactly one game-year, mirroring
 * BAILOUT_DURATION_TICKS. Deliberately a SEPARATE named constant (not a reuse
 * of BAILOUT_DURATION_TICKS) so the balance pass can retune the two durations
 * independently later — a test asserts they are currently equal (both must
 * equal TICKS_PER_YEAR today) so a drift is caught, not silently allowed.
 */
export const ADMINISTRATION_DURATION_TICKS = 360;

/**
 * FEAT-1972079923 inc3 (AC-6) — Aaron's round-2 ruling (2026-08-31, recorded on
 * the BOW item) OVERRIDES the BA criteria doc's stale "multiply ALL outflows by
 * a spending multiplier" text. Administration Mode is a HARD BLOCK on
 * DISCRETIONARY spend only:
 *   - BLOCKED: placing/paying for new buildings, enacting new policies, hiring.
 *   - ACCRUES IN FULL (never reduced): overdraft interest + existing upkeep of
 *     already-built infrastructure (mandatory obligations) — computeFlows()/
 *     applyOutflowPolicies() are UNTOUCHED by administration, deliberately.
 * No ADMINISTRATION_SPENDING_MULTIPLIER constant exists (the inc1/inc2 build
 * never declared one) — this SSOT message constant is the discretionary-block
 * feedback text instead, reusing the inc1 placeNotice feedback path (BUG-396).
 */
export const ADMINISTRATION_PLACE_BLOCKED_MESSAGE =
  'Cannot place under Administration Mode — spending is frozen to mandatory obligations only';

/**
 * FEAT-1972079923 inc3 (AC-6) — companion message for a blocked NEW policy
 * enactment while in Administration Mode (see ADMINISTRATION_PLACE_BLOCKED_MESSAGE).
 * Turning an ALREADY-ON policy back OFF is not a new discretionary spend, so
 * that direction is left unblocked.
 */
export const ADMINISTRATION_POLICY_BLOCKED_MESSAGE =
  'Cannot enact new policy under Administration Mode — spending is frozen to mandatory obligations only';

/**
 * FEAT-1972079923 inc4 (AC-10) — PLACEHOLDER (balance-number regime): the
 * SECOND IMF bailout event lasts exactly one game-year, mirroring
 * BAILOUT_DURATION_TICKS/ADMINISTRATION_DURATION_TICKS. A separate named
 * constant (not a reuse) so the balance pass can retune independently later —
 * a test asserts all three currently equal TICKS_PER_YEAR.
 */
export const SECOND_BAILOUT_DURATION_TICKS = 360;

/**
 * FEAT-1972079923 inc4 (AC-10) — PLACEHOLDER (balance-number regime): the
 * SECOND bailout's one-time cash injection, credited the SAME tick the second
 * bailout auto-triggers. Aaron's round-2 ruling (2026-08-31): the second
 * bailout is on WORSE TERMS than the first — this value is deliberately LOWER
 * than BAILOUT_INCOME_INJECTION so the balance pass finds one obvious knob per
 * bailout, not a duplicated literal (GR#3).
 */
export const BAILOUT_INCOME_INJECTION_SECOND = 1_000_000;

/**
 * FEAT-1972079923 inc4 (AC-10) — SSOT label for the second bailout's one-time
 * cash injection inflow, mirroring BAILOUT_INJECTION_LABEL (see its rationale).
 * A distinct label (not a reuse of BAILOUT_INJECTION_LABEL) so a consistency
 * check / debug read can tell which bailout year actually injected the funds.
 */
export const BAILOUT_SECOND_INJECTION_LABEL = 'IMF Second Bailout Injection (Worse Terms)';

/**
 * FEAT-1972079923 inc4 (AC-11) — Aaron's round-2 ruling (2026-08-31, recorded
 * on the BOW item): the FINAL decline screen (hard game-over, no third
 * bailout) fires at the second bailout's year-end re-evaluation if funds are
 * still below this threshold. Named as its own SSOT constant (not a reuse of
 * DEBT_THRESHOLD_FOR_BAILOUT, which is a much deeper -10,000,000 floor) per
 * the AC-11 text ("funds < 0 still") so the balance pass can retune the
 * decline bar independently of the bailout-entry bar.
 */
export const FINAL_DECLINE_FUNDS_THRESHOLD = 0;
