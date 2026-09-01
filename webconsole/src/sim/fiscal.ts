// fiscal.ts — FEAT-1972079890 round-4: single source of truth for fiscal formulas
//
// Extracted from engine.ts, consistency.ts, debugjson.ts to eliminate formula
// duplication and prevent checker drift. All coefficients are PLACEHOLDER
// (balance-number regime) — Aaron's row-by-row approval pending.

import type { FlowItem, InsolvencyState, PolicyId, ZoneKind } from './types.ts';

/**
 * BUG-452 inc1 (2026-09-01, Aaron's approved GBP-scale anchors): the webconsole
 * starting treasury, moved off the old toy £10,000,000 to a "truly small"
 * £1.5M grant — the midpoint of Aaron's "start truly small, £1-2M" ruling.
 * NAMED so the balance pass can retune this ONE constant and have every
 * ratio-derived insolvency threshold below auto-scale with it (GR#3 SSOT).
 * Referenced by engine.ts's rawState() (funds/fundsAtTickStart/fundsAtTickEnd).
 */
export const STARTING_TREASURY = 1_500_000;

/**
 * BUG-452 inc1 — must equal engine.ts's TICKS_PER_MONTH (30). Kept as a local
 * reference constant (not an import) to avoid a fiscal.ts -> engine.ts
 * module-eval-time cycle (engine.ts already imports FROM fiscal.ts) — mirrors
 * the existing BAILOUT_DURATION_TICKS/TICKS_PER_YEAR pattern below. Asserted
 * equal to engine.ts's TICKS_PER_MONTH by a test.
 */
const TICKS_PER_MONTH_REF = 30;

/**
 * BUG-452 inc1 — real UK-grounded per-citizen monthly anchors (carried forward
 * from money-numbers-real-world.md / Aaron's rulings, already used at full
 * scale on the Go engine side, moneycirc.go:130,155,166-167):
 *   - council tax: £47/month/person (Band D Folkestone & Hythe ÷ ~3 residents/household)
 *   - net wage: £1,512/month/citizen (Kent ONS-grounded)
 * Tick-adjusted below by TICKS_PER_MONTH_REF so the SAME real monthly figure
 * is what actually accrues over a real month of ticks.
 */
export const REAL_COUNCIL_TAX_PER_CAPITA_PER_MONTH = 47;
export const REAL_NET_WAGE_PER_CITIZEN_PER_MONTH = 1512;

/**
 * BUG-452 inc1 — the residential tax-rate value the council-tax anchor above is
 * CALIBRATED against (engine.ts rawState()'s taxRates.residential initial = 9).
 * councilTaxPerTick's taxRate parameter scales proportionally AROUND this
 * default, so the real £47/mo anchor holds exactly at the default rate while
 * the player's tax-rate slider still moves revenue up/down as before.
 */
const DEFAULT_RESIDENTIAL_TAX_RATE = 9;

/**
 * Council tax per tick: population * real-£47/mo-per-citizen (tick-adjusted),
 * scaled by the player's residential tax rate relative to the DEFAULT rate.
 * BUG-452 inc1: rebased from the old directional placeholder (population *
 * taxRate * 2 / 100) onto the real anchor above — no longer a placeholder for
 * the £-per-citizen magnitude, though the tax-RATE lever's exact sensitivity
 * is still an independent gameplay knob (balance-pass pending).
 */
export function councilTaxPerTick(population: number, taxRate: number): number {
  const rateMultiplier = taxRate / DEFAULT_RESIDENTIAL_TAX_RATE;
  return Math.round(population * FISCAL_COEFFICIENTS.councilTaxPerCapitaPerTick * rateMultiplier);
}

/**
 * Business tax per tick: commercial zones * rate.commercial * fraction.
 * PLACEHOLDER (balance-number regime): no real per-zone £ citation exists
 * (money-numbers-real-world.md has none) — BUG-452 inc1 keeps this an
 * INDEPENDENT lever, retuned only so its OUTPUT sits in the same £-per-tick
 * range as the newly-real-anchored council tax above (internal consistency,
 * not a new citation).
 */
export function businessTaxPerTick(commercialZones: number, taxRate: number): number {
  return Math.round(commercialZones * taxRate * FISCAL_COEFFICIENTS.businessTaxFraction);
}

/**
 * Wages per tick: population * real-£1,512/mo-per-citizen (tick-adjusted).
 * BUG-452 inc1: rebased from the old directional placeholder (population *
 * 0.5) onto the real net-wage anchor (money-numbers-real-world.md §4,
 * already used at full scale on the Go side, moneycirc.go:166-167).
 * Simplification carried over unchanged from the pre-existing design: this
 * uses total population as the wage-bearing base (not a separate "employed"
 * subset — the TS sim has no employment-status field), same as the old
 * placeholder did.
 */
export function wagesPerTick(population: number): number {
  return Math.round(population * FISCAL_COEFFICIENTS.wagesPerCitizen);
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

/**
 * Coefficients for fiscal calculations. BUG-452 inc1 (2026-09-01): the
 * council-tax and wage coefficients are now REAL-ANCHORED (money-numbers-
 * real-world.md), tick-adjusted by TICKS_PER_MONTH_REF; businessTaxFraction
 * remains an independent PLACEHOLDER (balance-number regime), retuned only so
 * its £-per-tick output sits in the same range as the rebased council tax.
 */
export const FISCAL_COEFFICIENTS = {
  /**
   * Council tax: real £47/mo/person (REAL_COUNCIL_TAX_PER_CAPITA_PER_MONTH)
   * ÷ TICKS_PER_MONTH_REF, applying exactly at DEFAULT_RESIDENTIAL_TAX_RATE —
   * councilTaxPerTick() then scales this by taxRate/DEFAULT_RESIDENTIAL_TAX_RATE.
   */
  councilTaxPerCapitaPerTick: REAL_COUNCIL_TAX_PER_CAPITA_PER_MONTH / TICKS_PER_MONTH_REF, // = 1.5667
  /**
   * Business tax: fraction of catalogue rate per commercial zone. PLACEHOLDER —
   * no real per-zone £ citation exists; retuned from the old 0.4 by the SAME
   * ratio the council-tax-per-citizen figure moved by (1.5667/0.18 ≈ 8.7037)
   * so the two taxes stay in the same order of magnitude at their defaults.
   */
  businessTaxFraction: 3.4815,
  /** Wages: real £1,512/mo/citizen (REAL_NET_WAGE_PER_CITIZEN_PER_MONTH) ÷ TICKS_PER_MONTH_REF. */
  wagesPerCitizen: REAL_NET_WAGE_PER_CITIZEN_PER_MONTH / TICKS_PER_MONTH_REF, // = 50.4
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
 * FEAT-1972079923 inc1 (AC-1) / BUG-452 inc1 (2026-09-01): funds at or below
 * this trip the 'crisis' band, the future IMF bailout entry point. Aaron's
 * approved RATIO (recorded on BUG-452): -1x STARTING_TREASURY — defined as a
 * ratio, not an absolute literal, so retuning STARTING_TREASURY auto-scales
 * this threshold. Named DEBT_THRESHOLD_FOR_BAILOUT (not a generic "insolvency"
 * name) to match the original AC doc so the balance pass finds one SSOT
 * constant, not a second table (GR#3).
 */
export const DEBT_THRESHOLD_FOR_BAILOUT = -STARTING_TREASURY;

/**
 * FEAT-1972079923 inc1 (companion to AC-1) / BUG-452 inc1: funds at or below
 * this (but still above DEBT_THRESHOLD_FOR_BAILOUT) trip the 'warning' band,
 * giving the player advance notice before the crisis threshold is crossed.
 * Aaron's approved RATIO: -0.5x STARTING_TREASURY.
 */
export const INSOLVENCY_WARNING_THRESHOLD = -Math.round(STARTING_TREASURY * 0.5);

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
 * FEAT-1972079923 inc2 / BUG-452 inc1 (2026-09-01, Aaron's "bigger relief"
 * ruling): one-time cash injection credited to the treasury the SAME tick the
 * bailout is entered — 50% of the debt hole (computed against
 * DEBT_THRESHOLD_FOR_BAILOUT's magnitude, i.e. STARTING_TREASURY), a bigger
 * relief than the old flat 2,000,000 (previously 0.2x treasury). This is a
 * legitimate external inflow (like a grant), not manufactured money — booked
 * as a normal labelled inflow (see BAILOUT_INJECTION_LABEL) so conservation
 * (fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows) can trace it
 * exactly like every other inflow.
 */
export const BAILOUT_INCOME_INJECTION = Math.round(Math.abs(DEBT_THRESHOLD_FOR_BAILOUT) * 0.5);

/**
 * FEAT-1972079923 inc2 (AC-4) / BUG-452 inc1: fraction of a building's capital
 * value (placementCost) credited to the treasury on a FORCED asset sale.
 * Aaron's 2026-09-01 ruling: 0.6 -> 0.5, the real distressed-sale rate (a
 * forced sale realises materially less than the original capex — a real
 * fire-sale discount, not merely "less generous than a normal refund").
 */
export const ASSET_SALE_VALUE_FRACTION = 0.5;

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
 * FEAT-1972079923 inc4 (AC-10) / BUG-452 inc1 (2026-09-01): the SECOND
 * bailout's one-time cash injection, credited the SAME tick the second
 * bailout auto-triggers. Aaron's ruling: the second bailout is on WORSE TERMS
 * than the first — 25% of the debt hole (vs. the first bailout's 50%),
 * computed against the SAME DEBT_THRESHOLD_FOR_BAILOUT magnitude so both
 * injections auto-scale together with STARTING_TREASURY (GR#3).
 */
export const BAILOUT_INCOME_INJECTION_SECOND = Math.round(Math.abs(DEBT_THRESHOLD_FOR_BAILOUT) * 0.25);

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
