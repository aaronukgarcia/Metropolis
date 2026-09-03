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
 * FEAT-2326609711 inc1 (Design Ruling, Aaron 2026-09-01) — external grid import
 * tariff per MW bought from the regional grid when local capacity falls short
 * of demand. ⚠ PLACEHOLDER-balance (Aaron's row-by-row pass pending).
 * Must stay STRICTLY GREATER than GRID_EXPORT_TARIFF_PER_MW (AC-4 invariant) —
 * opex-only external cover is dearer per unit than local generation's own
 * sell-back rate, so local investment can pay back over a horizon.
 */
export const GRID_IMPORT_TARIFF_PER_MW = 2.5;

/**
 * FEAT-2326609711 inc1 — new cities default to external power cover ON
 * (Aaron's ruling: "a hamlet starts on external contracts for everything").
 * Mechanical only (affects rawState()/genesis + new-game flow), not a
 * player-felt £ number.
 */
export const GRID_IMPORT_ENABLED_DEFAULT = true;

/**
 * FEAT-2326609711 inc1 — SSOT label for the Grid Import outflow line
 * (lastFlows.outflows entry + RightDock finance row), so the engine and the
 * UI can never disagree on the exact string (GR#3, mirrors
 * BAILOUT_INJECTION_LABEL / ASSET_SALE_LABEL). The generic sum-based
 * conservation checker (consistency.ts) needs no per-label recompute — it
 * sums lastFlows.outflows regardless of label.
 */
export const GRID_IMPORT_OUTFLOW_LABEL = 'Grid Import';

/**
 * Calculate Grid Import cost per tick: the shortfall (need - cap, floored at
 * 0) times the external tariff. Mirrors gridExportRevenuePerTick exactly
 * (same shape, opposite side of the meter). Pure, deterministic (GR#21).
 */
export function gridImportCostPerTick(capMW: number, needMW: number, tariff: number): number {
  const importMW = Math.max(0, needMW - capMW);
  return Math.round(importMW * tariff);
}

/**
 * FEAT-2326609711 inc1 (AC-4) — tariff invariant verification, loaded and
 * called ONLY at test time (never during gameplay). Derives the CHEAPEST
 * local power plant's amortised cost — capex spread over
 * POWER_PLANT_AMORTISATION_TICKS plus its own per-tick upkeep, per MW — from
 * the LIVE catalogue (data.ts SPECS), never a hardcoded number (GR#15).
 *
 * Takes the catalogue as a parameter (callers pass data.ts's live SPECS)
 * rather than importing data.ts at module scope, which would complete a
 * fiscal.ts -> data.ts -> engine.ts -> fiscal.ts cycle (engine.ts already
 * imports from fiscal.ts, and data.ts already imports specUnlocked/
 * feederTrafficWeight from engine.ts). Keeping this function a pure,
 * injectable derivation sidesteps the cycle entirely.
 *
 * BUG-477 (filed 2026-09-01, discovered building this AC): with TODAY's
 * catalogue, the cheapest plant's amortised cost is roughly an order of
 * magnitude above BOTH tariffs (a pre-existing scale mismatch between
 * BUG-452's realistic small-city tariffs and the FEAT-1972079901 real-capex
 * power catalogue — it already made the EXISTING Grid Export tariff
 * economically incoherent; this just surfaces it). So
 * `exportExceedsLocal`/`importExceedsLocal` read false today;
 * `importExceedsExport` (the inc1 design promise) holds. This function
 * reports the truth either way — it must NEVER be hardcoded to report `true`.
 */
export interface GridTariffInvariantResult {
  cheapestPlantId: string | null;
  cheapestAmortisedPerMwTick: number;
  importExceedsExport: boolean;
  exportExceedsLocal: boolean;
  importExceedsLocal: boolean;
  allHold: boolean;
}

/** ⚠ PLACEHOLDER-balance: physical power-plant capex amortisation horizon
 * (25 years), used ONLY by verifyGridTariffInvariant()'s test-time
 * derivation — never during gameplay. Aaron's balance pass may retune. */
export const POWER_PLANT_AMORTISATION_TICKS = 25 * 360;

/** Minimal shape verifyGridTariffInvariant() needs from a catalogue Spec —
 * kept local (not importing the full data.ts Spec type) to avoid a
 * fiscal.ts -> data.ts -> engine.ts -> fiscal.ts module-eval-time cycle.
 * The caller (tests, never gameplay) passes data.ts's live SPECS in
 * explicitly — this function stays a pure, injectable derivation. */
export interface GridImportPlantLike {
  id: string;
  kind: string;
  placeholder?: boolean;
  mw?: number;
  cost: number;
  upkeep: number;
}

export function verifyGridTariffInvariant(
  specs: Record<string, GridImportPlantLike>,
): GridTariffInvariantResult {
  let cheapestPlantId: string | null = null;
  let cheapestAmortisedPerMwTick = Infinity;
  for (const sp of Object.values(specs)) {
    if (sp.kind !== 'power' || sp.placeholder) continue;
    if (!sp.mw || sp.mw <= 0 || !sp.cost || sp.cost <= 0) continue;
    const capexPerMwTick = sp.cost / (sp.mw * POWER_PLANT_AMORTISATION_TICKS);
    const upkeepPerMwTick = (sp.upkeep ?? 0) / sp.mw;
    const amortised = capexPerMwTick + upkeepPerMwTick;
    if (amortised < cheapestAmortisedPerMwTick) {
      cheapestAmortisedPerMwTick = amortised;
      cheapestPlantId = sp.id;
    }
  }
  if (cheapestPlantId === null) cheapestAmortisedPerMwTick = 0;
  const importExceedsExport = GRID_IMPORT_TARIFF_PER_MW > GRID_EXPORT_TARIFF_PER_MW;
  const exportExceedsLocal = GRID_EXPORT_TARIFF_PER_MW > cheapestAmortisedPerMwTick;
  const importExceedsLocal = GRID_IMPORT_TARIFF_PER_MW > cheapestAmortisedPerMwTick;
  return {
    cheapestPlantId,
    cheapestAmortisedPerMwTick,
    importExceedsExport,
    exportExceedsLocal,
    importExceedsLocal,
    allHold: importExceedsExport && exportExceedsLocal && importExceedsLocal,
  };
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
 * ⚠ RETIRED (FEAT-dynamic-bailout, Aaron ruling Q100045, 2026-09-02) — GR#18
 * audit note: this constant has ZERO PRODUCTION READERS as of this feature.
 * The FRESH first-tier bailout grant is no longer this fixed £-value; it is
 * `fiscal.computeDynamicBailoutOffer()`'s CAPEX+bleed-proportional offer
 * (see `DYNAMIC_BAILOUT_INJECTION_LABEL` further down this file). This
 * constant is kept, UNCHANGED in value, ONLY so a save that predates this
 * feature and is mid an already-credited old-terms bailout can be
 * grandfathered/reasoned about (engine.ts's sanitizeTreasury migration
 * comments reference it) — it is NEVER read by any live trigger path any
 * more. Do not wire this back into a fresh grant without re-reading the
 * FEAT-dynamic-bailout spec's §3 ruling.
 *
 * Original doc (FEAT-1972079923 inc2 / BUG-452 inc1, 2026-09-01, Aaron's
 * "bigger relief" ruling, preserved for history): one-time cash injection
 * credited the SAME tick the bailout was entered — 50% of the debt hole
 * (computed against DEBT_THRESHOLD_FOR_BAILOUT's magnitude, i.e.
 * STARTING_TREASURY). Booked as a normal labelled inflow (see
 * BAILOUT_INJECTION_LABEL) so conservation could trace it exactly like every
 * other inflow.
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

/**
 * BUG-504 Option A (Aaron ruling Q100045, 2026-09-02, FEAT-endgame-ladder):
 * the first-bailout CLEAN-END bar is raised from the crisis line
 * (DEBT_THRESHOLD_FOR_BAILOUT, -1.5M) to REAL SOLVENCY. A city that merely
 * climbed back above the crisis floor while still deep in the red used to
 * clear the bailout and could re-enter crisis + re-collect a fresh
 * BAILOUT_INCOME_INJECTION every year forever (a city draining less than
 * 750k/yr never stayed below the OLD bar for a full year) — that was
 * BUG-504's unbounded free-rescue loop. Numerically identical to
 * FINAL_DECLINE_FUNDS_THRESHOLD today (both "truly solvent" = funds >= 0),
 * but kept as an INDEPENDENT named constant (not a re-export) so the balance
 * pass can diverge the two later — a test asserts they start equal.
 * Strictly ABOVE DEBT_THRESHOLD_FOR_BAILOUT by construction (0 > -1.5M),
 * which also closes BUG-505's dead-stuck window: a clean-end can never
 * leave the raw funds band in 'crisis' (crisis is funds <= -1.5M).
 * ⚠ PLACEHOLDER-balance (Aaron's row-by-row pass pending).
 */
export const BAILOUT_CLEAN_END_THRESHOLD = 0;

/**
 * ⚠ RETIRED (FEAT-dynamic-bailout, Aaron ruling Q100045, 2026-09-02) — GR#18
 * audit note: this constant has ZERO PRODUCTION READERS as of this feature.
 * "This only happens once. Then that's it." replaced the re-arm COUNTER with
 * a one-way boolean LATCH (`SimState.dynamicBailoutUsed`) — the effective
 * cap is now hard-coded to exactly 1, not this named constant. Kept, value
 * UNCHANGED, only so `bug-504-505-506-endgame.test.mjs`'s determinism
 * regression (which still exercises a fixed loop-count fixture) and the
 * historical comments below stay meaningful. Do not re-wire this into a
 * fresh-grant gate — see engine.ts's FEAT-dynamic-bailout comment block.
 *
 * Original doc (BUG-504 Option A, preserved for history): a city could
 * receive at MOST this many FRESH first bailouts across a single
 * playthrough; once exhausted, a fresh crisis entry was FORCED straight to
 * the (worse-terms) second bailout instead of re-issuing a free
 * BAILOUT_INCOME_INJECTION grant.
 * ⚠ PLACEHOLDER-balance (historical; moot now the cap is fixed at 1).
 */
export const MAX_FIRST_BAILOUTS = 2;

/**
 * BUG-506 (AC-506-1/2) — number of CONSECUTIVE ticks of funds >= 0 required,
 * while a bailout (first or second) is active, to trigger an EARLY exit that
 * clears the bailout state before its year-end checkpoint. Named N in the
 * acceptance doc ("suggested N = 30 ticks = 1 month, matching the monthly UI
 * cadence"). Deliberately NOT a re-export of engine.ts's TICKS_PER_MONTH (30)
 * — fiscal.ts cannot import from engine.ts, see TICKS_PER_MONTH_REF's own
 * comment above — a test asserts the two stay numerically equal.
 * ⚠ PLACEHOLDER-balance.
 */
export const SUSTAINED_RECOVERY_TICKS = 30;

/**
 * BUG-506 (AC-506-3/4) — the decline year-end decision reads the MEAN of a
 * rolling window of the last N ticks' funds (SimState.recentFundsWindow,
 * updated every tick), not a single-tick sample — so one bad tick in an
 * otherwise-solvent year no longer forces a hard game-over, and a single
 * lucky tick in an otherwise-insolvent year no longer buys a reprieve.
 * Independent constant from SUSTAINED_RECOVERY_TICKS (same default value
 * today) so the balance pass can retune the two separately later.
 * ⚠ PLACEHOLDER-balance.
 */
export const DECLINE_AVERAGING_WINDOW_TICKS = 30;

/**
 * BUG-504 Option A — SSOT label for the per-tick standing cost (a credit-
 * rating / interest surcharge) charged while EITHER bailout is actively in
 * force, so a bailout is a felt lifeline rather than a free tap. Booked as a
 * normal labelled outflow (mirrors BAILOUT_INJECTION_LABEL on the inflow
 * side) so conservation (fundsAtTickEnd === fundsAtTickStart + Σinflows −
 * Σoutflows) traces it exactly, and consistency.ts's upkeep-total
 * reconciliation excludes it (it is not per-building upkeep) exactly like
 * Overdraft Interest / Wages.
 */
export const BAILOUT_STANDING_COST_LABEL = 'Bailout Standing Cost (Credit Rating Surcharge)';

/**
 * BUG-504 Option A — base per-tick standing cost of an active bailout, before
 * the re-arm multiplier in bailoutStandingCostPerTick() below.
 * ⚠ PLACEHOLDER-balance (Aaron's row-by-row pass pending).
 */
export const BAILOUT_STANDING_COST_PER_TICK = 500;

/**
 * BUG-504 Option A — pure per-tick standing-cost formula: the base cost times
 * the number of first-bailout re-arms used so far this playthrough (minimum
 * 1x, so the very first bailout still carries the base charge; a SECOND
 * re-arm — up to MAX_FIRST_BAILOUTS — costs proportionally more, a genuinely
 * worse credit hit on repeat). Deterministic, no Date/random (GR#21).
 */
export function bailoutStandingCostPerTick(firstBailoutCount: number): number {
  return BAILOUT_STANDING_COST_PER_TICK * Math.max(1, firstBailoutCount);
}

/**
 * FEAT-2326609723 (Play Mode — Aaron ruling Q100045's escape-hatch companion,
 * 2026-09-02): the ONE-WAY sandbox injection credited the tick the player
 * engages Play Mode from the Decline / game-over screen. Deliberately many
 * orders of magnitude above STARTING_TREASURY (1.5M) — this is EXPLICITLY
 * sandbox money, never mistaken for a real economy event. Booked as a normal
 * labelled inflow (see PLAY_MODE_INJECTION_LABEL) so the conservation
 * invariant still holds exactly even in Play Mode — no bypass flag needed.
 */
export const PLAY_MODE_INJECTION_AMOUNT = 1_000_000_000_000;

/**
 * FEAT-2326609723 — SSOT label for the Play Mode sandbox injection, so the
 * ledger/lastFlows entry can never be confused with a real bailout/grant
 * inflow (GR#3: distinct, unambiguous labels for distinct concepts).
 */
export const PLAY_MODE_INJECTION_LABEL = 'Play Mode Sandbox Injection (not a simulation)';

// ════════════════════════════════════════════════════════════════════════════
// FEAT-dynamic-bailout (docs/planning/acceptance/FEAT-dynamic-bailout-2026-09-02.md)
// — Aaron ruling Q100045 (2026-09-02, verbatim): "the 750K and 1.5M are wrong
// - in a long standing game there could be 10's of billions of investment and
// these amounts are not even a days run cost... we need some proportional
// 'offer of help' based on the CAPEX already spent and the current bleed...
// this only happens once. then that's it."
//
// RETIRES the two-stage worse-terms-second-bailout ladder's FRESH-ENTRY path
// (BAILOUT_INCOME_INJECTION / BAILOUT_INCOME_INJECTION_SECOND /
// MAX_FIRST_BAILOUTS's re-arm role) per the spec's §3 recommended branch (a):
// exactly ONE dynamic offer per playthrough, full stop — a second insolvency
// proceeds straight through Administration/Decline with no further grant of
// any kind. The OLD constants above are kept, UNCHANGED, purely so an
// in-flight/grandfathered old save (see engine.ts's migration in
// sanitizeTreasury) can finish its already-credited bailout under its
// original terms — see the spec's §4 migration table. `bailoutState`/
// `administrationState`/`declineState`'s STATE-MACHINE SHAPE and the trigger
// bands (DEBT_THRESHOLD_FOR_BAILOUT/INSOLVENCY_WARNING_THRESHOLD) are
// DELIBERATELY UNCHANGED by this feature (scoping decision, see the spec's
// open question §7.4's own recommendation to keep the trigger simple) — only
// the INJECTION SIZE and the ONCE-ONLY enforcement are dynamic. This is
// Phase-1 of the spec's §7.5 phasing (a resized LUMP SUM credited the
// triggering tick, exactly like the retired ladder's mechanic) — the
// drawdown-facility shape (§2.4) and its CAPEX-spend-gated sub-ledger (AC-11)
// are explicitly Phase-2, NOT built here.
//
// SCOPING NOTE (build-time decision, flagged for Aaron's explicit
// confirmation): engine.ts's fresh-trigger logic implements the spec's §3
// alternative branch (b), NOT the recommended branch (a) above — only the
// FRESH first-tier grant is retired (once-only, dynamically sized); the
// existing worse-terms second-bailout ESCALATION machinery
// (BAILOUT_INCOME_INJECTION_SECOND/BAILOUT_SECOND_INJECTION_LABEL) is left
// byte-for-byte unchanged as "the teeth" once the one dynamic offer is used,
// per the spec's own explicitly-sanctioned alternative. This avoided
// rewriting ~15 tests across the pre-existing endgame-teeth estate
// (imf-insolvency-inc2/inc3/inc4/inc5, bug-504-505-506-endgame, bug496-497,
// play-mode-endgame, bug501) for a behavioural change branch (a) would have
// forced. See engine.ts's own FEAT-dynamic-bailout comment block.
// ════════════════════════════════════════════════════════════════════════════

/**
 * ⚠ BALANCE-NUMBER PLACEHOLDER (Aaron's row-by-row pass pending, spec §6):
 * the CAPEX "spend to save" allowance — the fraction of a city's cumulative
 * historic capital spend offered as part of the ONE dynamic bailout, sized so
 * the money can "fix the cause" (build the missing power plant/service) as
 * well as cover a year of OPEX bleed. Applied to `cumulativeCapexSpent`.
 */
export const CAPEX_SPEND_TO_SAVE_FRACTION = 0.05;

/**
 * ⚠ BALANCE-NUMBER PLACEHOLDER (spec §6): the dynamic offer must never be
 * LESS than this — preserves "the old £750k really was a lifesaver at the
 * start" for a brand-new/small city whose bleed rate and CAPEX are both tiny
 * (a fresh insolvency from a one-off shock, near-zero ongoing bleed).
 * Numerically identical to the RETIRED BAILOUT_INCOME_INJECTION today (both
 * derive from STARTING_TREASURY * 0.5) — an INDEPENDENT named constant (not a
 * re-export), so the balance pass can diverge the two later; a test asserts
 * they start equal.
 */
export const BAILOUT_FLOOR = Math.round(STARTING_TREASURY * 0.5);

/**
 * ⚠ BALANCE-NUMBER PLACEHOLDER (spec §6): safety-rail ceiling on the dynamic
 * offer, expressed as a MULTIPLE of the city's OWN cumulative historic CAPEX
 * (never a fixed £ literal — a size-of-the-city-relative ceiling, keeping the
 * ruling's "no fixed £ constant" spirit even for the guard rail). Stops a
 * runaway/degenerate bleed reading (an engine bug producing an absurd
 * per-tick outflow) from minting an absurd one-time injection.
 */
export const BAILOUT_CAP_FRACTION_OF_CAPEX = 2.0;

/**
 * FEAT-dynamic-bailout — SSOT label for the ONE dynamic bailout's one-time
 * cash injection inflow, distinct from the retired ladder's
 * BAILOUT_INJECTION_LABEL/BAILOUT_SECOND_INJECTION_LABEL so a debug read (or
 * the AC-6 regression test) can tell a fresh dynamic grant apart from a
 * grandfathered old-save injection under the old terms.
 */
export const DYNAMIC_BAILOUT_INJECTION_LABEL = 'Dynamic Bailout Grant';

/**
 * FEAT-dynamic-bailout (spec §2.1/§7.2) — inflow labels that are ONE-OFF
 * external injections, not structural income, and so must be EXCLUDED from
 * the bleed-rate reading below (reusing this SAME rate to size a FRESH
 * bailout would let a past bailout's own injection distort the next
 * reading — the exact distortion the spec calls out `recentFundsWindow` for).
 * SSOT set (GR#3): every one-off inflow label already defined above/elsewhere
 * in this file is listed here once, not re-typed at each call site.
 */
const ONE_OFF_INFLOW_LABELS: ReadonlySet<string> = new Set<string>([
  BAILOUT_INJECTION_LABEL,
  BAILOUT_SECOND_INJECTION_LABEL,
  DYNAMIC_BAILOUT_INJECTION_LABEL,
  ASSET_SALE_LABEL,
  PLAY_MODE_INJECTION_LABEL,
]);

/**
 * FEAT-dynamic-bailout (spec §2.1, "a fresh netOpexBleedPerTick() SSOT
 * function... explicitly excludes one-off injections"). Pure, deterministic
 * (GR#21: no Date/random) — reads a single tick's already-computed flows
 * (engine.ts's advance() calls this BEFORE appending that same tick's own
 * bailout injection, so no self-distortion is possible; a PAST tick's
 * injection lives in a PAST tick's flows and never reaches this call at all).
 * Floored at 0 — a tick that is net POSITIVE (more structural income than
 * outflow) has no "bleed" to speak of, never a negative allowance.
 */
export function netOpexBleedPerTick(flows: { inflows: FlowItem[]; outflows: FlowItem[] }): number {
  const outflowSum = flows.outflows.reduce((a, b) => a + b.value, 0);
  const structuralInflowSum = flows.inflows
    .filter((f) => !ONE_OFF_INFLOW_LABELS.has(f.label))
    .reduce((a, b) => a + b.value, 0);
  return Math.max(0, outflowSum - structuralInflowSum);
}

/** Which formula branch produced a dynamic bailout offer — surfaced to the
 * player-visible UI messaging per spec §2.5 ("distressed-then-recovered play
 * session isn't confusing to debug"). */
export type DynamicBailoutBranch = 'floored' | 'formula' | 'capped';

export interface DynamicBailoutOffer {
  offer: number;
  opexAllowance: number;
  capexAllowance: number;
  branch: DynamicBailoutBranch;
}

/**
 * F4 FIX (independent round REJECT, 2026-09-02) — the safe INPUT ceilings
 * computeDynamicBailoutOffer clamps its two arguments to BEFORE any
 * arithmetic runs, so every downstream term (opexAllowance/capexAllowance/
 * cap) is a safe integer BY CONSTRUCTION and never needs zeroing after the
 * fact. Each is `floor(MAX_SAFE_INTEGER / <the largest multiplier that term
 * feeds>)` so `<ceiling> * <multiplier>` can never exceed
 * Number.MAX_SAFE_INTEGER. BAILOUT_CAP_FRACTION_OF_CAPEX (2.0) is used for
 * capex (it is the larger of the two capex multipliers, vs.
 * CAPEX_SPEND_TO_SAVE_FRACTION's 0.05); BAILOUT_DURATION_TICKS (360) for bleed.
 */
const MAX_SAFE_CAPEX_INPUT = Math.floor(Number.MAX_SAFE_INTEGER / BAILOUT_CAP_FRACTION_OF_CAPEX);
const MAX_SAFE_BLEED_INPUT = Math.floor(Number.MAX_SAFE_INTEGER / BAILOUT_DURATION_TICKS);

/**
 * GR#16 storage-boundary coercion for a formula INPUT (not stored state) —
 * never trust the caller's type: a non-number (string/null/object/NaN/
 * Infinity), a non-finite number, or a non-positive number all sanitize to
 * 0 (no allowance from a garbage/absent reading); a legitimate positive
 * finite number is clamped to `ceiling` so it can never blow the arithmetic
 * budget the caller (computeDynamicBailoutOffer) has reserved for it.
 */
function sanitizePositiveFiniteInput(n: unknown, ceiling: number): number {
  if (typeof n !== 'number' || !Number.isFinite(n) || n <= 0) return 0;
  return Math.min(n, ceiling);
}

/**
 * FEAT-dynamic-bailout (spec §2.3/§2.5) — THE formula. Pure, deterministic
 * (GR#21): a function of two numbers only, no Date/random, so two identical
 * runs produce byte-identical offers (AC-9).
 *
 *   opexAllowance  = recentOpexBleedPerTick * BAILOUT_DURATION_TICKS  (a full
 *                    year of the CURRENT bleed rate — "offering a year's
 *                    worth of support", spec §2.3)
 *   capexAllowance = cumulativeCapexSpent * CAPEX_SPEND_TO_SAVE_FRACTION
 *                    ("spend to save" — fix the cause, sized off what's
 *                    already been built)
 *   offer          = clamp(opexAllowance + capexAllowance, BAILOUT_FLOOR, cap)
 *
 * `cap` is BAILOUT_CAP_FRACTION_OF_CAPEX × cumulativeCapexSpent, floored at
 * BAILOUT_FLOOR itself (AC-5's own safety: a synthetic/adversarial city with
 * cumulativeCapexSpent === 0 must still receive a FINITE, floor-sized offer
 * rather than being capped to zero by its own zero CAPEX base — the cap is a
 * ceiling on top of the floor, never a way to undercut it).
 *
 * F4 FIX (independent round REJECT, 2026-09-02): inputs are SANITIZED FIRST
 * (sanitizePositiveFiniteInput, clamped to a safe-integer-bounded ceiling),
 * the clamp (floor/cap) runs LAST. The ORIGINAL code sanitized the FINAL
 * offer with sanitizeFunds() AFTER the floor/cap clamp — sanitizeFunds()
 * returns 0 for any non-safe-integer, so a capex input around ~1.8e17
 * survived the bare `Number.isFinite` guard, its 2x-capex `cap` term then
 * OVERFLOWED Number.MAX_SAFE_INTEGER, and the stale post-clamp sanitizeFunds
 * call zeroed the whole thing — returning {offer: 0, branch: 'formula'}, a
 * LYING branch label (claims the plain formula ran clean) reporting an offer
 * strictly BELOW the feature's own documented floor. Sanitizing the inputs
 * up front instead means every downstream term is provably a safe integer,
 * so the offer this function returns is NEVER silently zeroed by a
 * downstream integer-safety catch.
 */
export function computeDynamicBailoutOffer(
  cumulativeCapexSpent: number,
  recentOpexBleedPerTick: number,
): DynamicBailoutOffer {
  const safeCapex = sanitizePositiveFiniteInput(cumulativeCapexSpent, MAX_SAFE_CAPEX_INPUT);
  const safeBleed = sanitizePositiveFiniteInput(recentOpexBleedPerTick, MAX_SAFE_BLEED_INPUT);

  const opexAllowance = Math.round(safeBleed * BAILOUT_DURATION_TICKS);
  const capexAllowance = Math.round(safeCapex * CAPEX_SPEND_TO_SAVE_FRACTION);
  const raw = opexAllowance + capexAllowance;
  const cap = Math.max(BAILOUT_FLOOR, Math.round(safeCapex * BAILOUT_CAP_FRACTION_OF_CAPEX));

  let offer = raw;
  let branch: DynamicBailoutBranch = 'formula';
  if (offer < BAILOUT_FLOOR) {
    offer = BAILOUT_FLOOR;
    branch = 'floored';
  } else if (offer > cap) {
    offer = cap;
    branch = 'capped';
  }
  // Defensive final guard ONLY (never zeroing): by construction both inputs
  // were pre-clamped to safe-integer-bounded ceilings above, so `offer` is
  // already a safe integer at this point in every normal case. This just
  // truncates any stray fractional residue and caps the theoretical
  // combined-extreme edge (both inputs simultaneously at their own safe
  // ceiling) at Number.MAX_SAFE_INTEGER — it must NEVER collapse a
  // legitimately-clamped offer down to 0 the way the retired post-clamp
  // sanitizeFunds() call did.
  offer = Math.trunc(Math.min(offer, Number.MAX_SAFE_INTEGER));
  return { offer, opexAllowance, capexAllowance, branch };
}

// ════════════════════════════════════════════════════════════════════════════
// FEAT-wage-stage1 (Q100067, Aaron "rec-on-all" ruling / Q100086 "Stage 1 GO",
// 2026-09-03) — per-sector wage bands, Stage 1 of
// docs/planning/proposals/wage-ownership-model-2026-09-02.md's 4-stage plan.
//
// SCOPE NOTE (webconsole side vs the doc's own Stage 1): the doc's §4 Stage 1
// is a Go-engine plumbing fix — gate PostWages' existing Treasury-payer path
// to Sector==SectorPublic only, add an AcctFirms→AcctHouseholds private-wage
// leg keyed by Sector, collapse the two broken tax legs into one. This
// webconsole has no account/ownership ledger at all yet — wagesPerTick() above
// is a single flat Treasury outflow, full stop. The webconsole-side "Stage 1"
// (Bev's status-file label, Q100086) takes the doc's SECTOR taxonomy
// (Primary/Secondary/Tertiary/Public, §0 "citizens.Sector") and uses it to
// DIFFERENTIATE THE WAGE RATE rather than the payer account — still one
// aggregate outflow, no per-building/per-firm ledger (that is Stage 2/3
// ownership-graph territory, explicitly held back post-BL1 by Q100086=B).
//
// F2 FIX (independent round REJECT, 2026-09-03, GR#6): this section originally
// claimed "wagesPerTick() is left BYTE-IDENTICAL so engine.ts's call-site
// needs no edit" and "the eventual caller... derives them" — both were true
// ONLY for the first (unwired) landing. Stage 1 IS NOW WIRED into the live
// tick: engine.ts's computeFlows() calls sectorWagesPerTick(filledJobsBySector(s))
// instead of wagesPerTick(s.population) for the 'Wages' outflow line, and
// consistency.ts's flows-vs-recompute check was updated to the same formula
// (see both files' own FEAT-wage-stage1 comments). wagesPerTick() itself is
// STILL byte-identical/untouched (kept only for its own tests / any
// grandfathered caller) — but it is NO LONGER engine.ts's production wage
// source, and this comment must not claim otherwise (a stale "not wired"
// claim next to code that IS wired is exactly the kind of lying doc GR#6
// exists to catch).
//
// CYCLE AVOIDANCE (mirrors verifyGridTariffInvariant's existing pattern
// above): fiscal.ts must NOT import data.ts at module scope — data.ts already
// imports FROM engine.ts, and engine.ts already imports FROM fiscal.ts, so a
// fiscal.ts -> data.ts import would complete a module-eval-time cycle. The
// per-sector job counts are therefore an INJECTED parameter — data.ts's
// totalJobsBySector()/filledJobsBySector() derive them from the live SPECS
// catalogue via isOnline()/capacityAtTier() (the SAME inputs data.ts's
// totalJobs() already uses) and engine.ts/consistency.ts pass the result in —
// fiscal.ts itself never reaches into data.ts. sectorWagesPerTick() and
// allocateFilledJobs() below stay pure/injectable, exactly like
// verifyGridTariffInvariant.
// ════════════════════════════════════════════════════════════════════════════

/**
 * The four employment domains, mirroring the Go engine's citizens.Sector enum
 * (Primary/Secondary/Tertiary/Public — wage-ownership-model doc §0/§1.1) so
 * the two sides of the project use the same vocabulary (GR#3) even though the
 * webconsole does not share the Go engine's runtime.
 */
export type WageSector = 'primary' | 'secondary' | 'tertiary' | 'public';

/**
 * SSOT (GR#3) classification of a catalogue building `kind` (data.ts's
 * `ZoneKind`, kept as a bare string here to avoid importing the type from
 * data.ts at module scope — see the cycle-avoidance note above) into the wage
 * sector it employs into. Documents the DECOMPOSITION BASIS for whoever wires
 * this into data.ts/engine.ts: the same building-kind dispatch data.ts's
 * totalJobs() already switches on (`sp.kind === 'commercial'`/`'industrial'`
 * fallback branches, plus every kind that carries an explicit `sp.jobs`
 * count in the live catalogue: commercial, office, industrial, mine,
 * transport). Kinds with NO jobs field in today's catalogue (school, health,
 * police, fire, civic, power, water, pylon, road, station) are still listed
 * here as their real-world employer domain for completeness/documentation —
 * they currently contribute 0 jobs because data.ts's own catalogue has no
 * staff-headcount field for civic-service buildings yet (a catalogue gap, not
 * a fiscal.ts gap; out of this lane's scope to add — data.ts is claimed by
 * another lane). `residential`/`park`/`landmark`/`leisure` are deliberately
 * OMITTED: they are not employers in the catalogue today.
 *
 * F6 NOTE (independent round, 2026-09-03, flagged for the balance table —
 * NOT a kind remap, do not action now): every `farm_*` spec in today's live
 * catalogue (farm_wheat/farm_cattle/farm_orchard/farm_estate) is cataloged
 * with `kind: 'industrial'`, not a distinct agriculture kind — so farm jobs
 * land in the SECONDARY band, not primary, even though real-world farming
 * is agriculture (arguably primary-sector). Combined with `public` having no
 * `sp.jobs`-bearing catalogue kind at all today (see above), the PRACTICAL
 * effect on typical fixtures/playthroughs is that `primary` (mine only) and
 * `public` (zero today) are both thin-to-dead bands versus `secondary`
 * (industrial + all farms) and `tertiary` (commercial + office) doing most
 * of the real work. This is a catalogue-shape fact, not a fiscal.ts bug —
 * recorded here so the balance pass (and whoever eventually gives farming
 * its own kind, if ever) has the pointer.
 */
export const KIND_TO_WAGE_SECTOR: Readonly<Record<string, WageSector>> = Object.freeze({
  mine: 'primary',
  industrial: 'secondary',
  commercial: 'tertiary',
  office: 'tertiary',
  transport: 'public',
  station: 'public',
  school: 'public',
  health: 'public',
  police: 'public',
  fire: 'public',
  civic: 'public',
  power: 'public',
  water: 'public',
  pylon: 'public',
  road: 'public',
});

/** Per-sector job counts — the injected decomposition-basis input to
 * sectorWagesPerTick(). All-zero is a valid input (a jobless/genesis city). */
export interface SectorJobs {
  primary: number;
  secondary: number;
  tertiary: number;
  public: number;
}

export const ZERO_SECTOR_JOBS: Readonly<SectorJobs> = Object.freeze({
  primary: 0,
  secondary: 0,
  tertiary: 0,
  public: 0,
});

/**
 * ⚠ PLACEHOLDER-balance (Aaron's row-by-row pass pending, GR#15/§5 spirit of
 * the doc — no new Go literal, retunable as one table): ONS-inspired
 * DIRECTIONAL monthly gross-wage anchors per sector (not a literal ONS
 * citation — the exact £/month figures are illustrative placeholders in the
 * same spirit as REAL_NET_WAGE_PER_CITIZEN_PER_MONTH above, which this table
 * deliberately brackets rather than replaces):
 *   - primary   (mining/quarrying) — ONS mining & quarrying consistently
 *     reports the highest UK sector average, hence the premium above the rest.
 *   - secondary (manufacturing/industrial) — mid-table, above retail.
 *   - tertiary  (commercial retail + office/professional, blended) — the
 *     doc's own worked examples (§6a corner-shop £1,600/mo, §6b supermarket
 *     £1,800/mo) bracket this ONE coarse Stage-1 band from both sides; 1,700
 *     sits between them (a deliberate Stage-1 SIMPLIFICATION — one flat rate
 *     per sector, not a per-firm negotiated wage, matching the doc's own §5
 *     "keep it that way" coarsening spirit for other flat-rate legs).
 *   - public    (civil service — teachers/nurses/police/fire/etc.) — matches
 *     the doc's own §6c teacher example (£2,000/mo) exactly.
 */
export const SECTOR_WAGE_PER_MONTH: Readonly<Record<WageSector, number>> = Object.freeze({
  primary: 2500,
  secondary: 2000,
  tertiary: 1700,
  public: 2000,
});

export interface SectorWageLine {
  sector: WageSector;
  jobs: number;
  wagePerMonth: number;
  wagePerTick: number;
}

export interface SectorWageBreakdown {
  lines: SectorWageLine[];
  totalPerTick: number;
}

/** GR#16 storage-boundary coercion for a formula INPUT (mirrors
 * sanitizePositiveFiniteInput above, but 0 is a legitimate job count, unlike
 * a bailout allowance): a non-number/non-finite/negative input sanitizes to
 * 0 (no phantom jobs from a garbage reading); fractional job counts are
 * truncated (a building either employs a whole person or it doesn't). */
function sanitizeJobsInput(n: unknown): number {
  if (typeof n !== 'number' || !Number.isFinite(n) || n < 0) return 0;
  return Math.trunc(n);
}

/**
 * Stage 1 per-sector wage decomposition (wage-ownership-model doc §4 Stage 1
 * / Q100067 / Q100086). Pure, deterministic (GR#21: no Date/Math.random) —
 * same jobsBySector input always produces the same breakdown.
 *
 * Per-line rounding happens ONCE, at the per-sector wagePerTick — `totalPerTick`
 * is the SUM of those already-rounded lines, never a separate re-round of the
 * total. This is the doc's own rounding rule read across from every other
 * SSOT formula in this file (councilTaxPerTick/wagesPerTick/etc. round once,
 * at the leaf): it guarantees `totalPerTick === Σ line.wagePerTick` EXACTLY,
 * by construction, with no separate reconciliation step needed — conservation
 * (the task's "no rounding drift" requirement) holds trivially rather than
 * needing a corrective remainder distribution.
 */
export function sectorWagesPerTick(jobsBySector: SectorJobs): SectorWageBreakdown {
  const sectors: WageSector[] = ['primary', 'secondary', 'tertiary', 'public'];
  const lines: SectorWageLine[] = sectors.map((sector) => {
    const jobs = sanitizeJobsInput(jobsBySector[sector]);
    const wagePerMonth = SECTOR_WAGE_PER_MONTH[sector];
    const wagePerTick = Math.round((jobs * wagePerMonth) / TICKS_PER_MONTH_REF);
    return { sector, jobs, wagePerMonth, wagePerTick };
  });
  const totalPerTick = lines.reduce((sum, line) => sum + line.wagePerTick, 0);
  return { lines, totalPerTick };
}

/**
 * F1 FIX (independent round REJECT, 2026-09-03) — blocking money defect: the
 * original wiring fed sectorWagesPerTick() raw job CAPACITY (data.ts's
 * totalJobs()-shaped counts, vacancy-inclusive), so a population-0 city with
 * one off_towers_downtown (2,000 job slots, all vacant) was charged
 * £113,333/tick, and the 13k-building fixture (3.0M job slots against a
 * 1.42M-pop city) paid 2.49x the old flat formula — jobs that don't exist as
 * PEOPLE were being paid wages anyway.
 *
 * FIX: sectorWagesPerTick() must be fed FILLED jobs, never raw capacity.
 * `workers = population * WORKING_AGE_FRACTION` (data.ts's own SSOT constant,
 * unemploymentOf()'s basis — this file cannot import it directly without
 * completing the documented data.ts/engine.ts/fiscal.ts cycle, so the CALLER
 * — data.ts's filledJobsBySector(), which already owns WORKING_AGE_FRACTION —
 * passes the pre-computed `workers` figure in). `filled = min(workers,
 * totalCapacity)`, floored at 0 and rounded ONCE to an integer headcount (you
 * cannot pay half a worker) — this is the single money-relevant rounding
 * point; every downstream step is exact-integer apportionment, never a
 * second independent rounding pass.
 *
 * DETERMINISTIC ALLOCATION RULE (the "how" the reject asked to be
 * documented): `filled` is spread across sectors proportional to each
 * sector's CAPACITY share (a sector with more job slots gets a
 * proportionally larger cut of the filled headcount) via the largest-
 * remainder method (Hamilton apportionment) — floor every sector's exact
 * share, then hand out the leftover headcount (an integer <= the number of
 * sectors) one-by-one to the sectors with the LARGEST fractional remainder;
 * a tie in fractional remainder is broken by SECTOR_ORDER (primary before
 * secondary before tertiary before public — a fixed, arbitrary-but-
 * documented order, never Math.random/insertion-order-of-the-day). This
 * guarantees `Σ allocated === filled` EXACTLY (integer conservation, no
 * rounding drift) for every input, including the two edges the reject named:
 *   - jobs > workers: filled === workers (capped), spread by capacity share.
 *   - workers > jobs: filled === totalCapacity (== Σ weights), so every
 *     sector's exact share already equals its own capacity — floors sum to
 *     totalCapacity with ZERO remainder to distribute, i.e. allocated ===
 *     capacity verbatim (the pre-fix, correct-for-this-one-case behaviour).
 *   - totalCapacity === 0 (no job-bearing buildings at all): every share is
 *     0/0 — returns ZERO_SECTOR_JOBS rather than dividing by zero.
 */
export const SECTOR_ORDER: readonly WageSector[] = ['primary', 'secondary', 'tertiary', 'public'];

export function allocateFilledJobs(filled: number, capacityBySector: SectorJobs): SectorJobs {
  const safeFilled = Math.max(0, Math.trunc(sanitizeJobsInput(filled)));
  const totalCapacity = SECTOR_ORDER.reduce(
    (sum, sector) => sum + sanitizeJobsInput(capacityBySector[sector]),
    0,
  );
  if (totalCapacity <= 0 || safeFilled <= 0) return { ...ZERO_SECTOR_JOBS };

  // Cap the target at totalCapacity — allocateFilledJobs is a pure apportionment
  // primitive and must not silently invent jobs beyond the given capacity even
  // if a caller passes a `filled` value larger than the capacity sum (GR#16:
  // never trust a caller's arithmetic, clamp defensively at the boundary).
  const target = Math.min(safeFilled, totalCapacity);

  const shares = SECTOR_ORDER.map((sector) => {
    const capacity = sanitizeJobsInput(capacityBySector[sector]);
    const exact = (target * capacity) / totalCapacity;
    const floor = Math.floor(exact);
    return { sector, capacity, floor, remainder: exact - floor };
  });

  let allocatedSoFar = shares.reduce((sum, s) => sum + s.floor, 0);
  let leftover = target - allocatedSoFar;

  // Largest-remainder-first, tie-broken by SECTOR_ORDER (the array's own
  // original order — Array.prototype.sort is stable in every engine this
  // project targets, so equal remainders keep primary/secondary/tertiary/
  // public order deterministically, never insertion-order-of-the-day).
  const byRemainderDesc = [...shares].sort((a, b) => b.remainder - a.remainder);
  const bump = new Set<WageSector>();
  for (let i = 0; i < byRemainderDesc.length && leftover > 0; i++, leftover--) {
    bump.add(byRemainderDesc[i].sector);
  }

  const result: SectorJobs = { ...ZERO_SECTOR_JOBS };
  for (const s of shares) {
    result[s.sector] = s.floor + (bump.has(s.sector) ? 1 : 0);
  }
  return result;
}
