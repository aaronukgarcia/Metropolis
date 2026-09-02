// ragThresholds.ts — FEAT-2326609720 inc2 (AC-7/AC-8/AC-9/AC-10).
//
// SINGLE named constants object (GR#3 SSOT) for every RAG (red/amber/green)
// threshold the inc2 tab tree colours from. Grounded against
// docs/planning/acceptance/FEAT-2326609720-inc2-tab-tree-and-rag-2026-09-02.md
// section 2 — every numeric bound here is PLACEHOLDER pending Aaron's balance
// pass (per project's balance-number regime), the POINT of AC-8 is that a
// future retune is a single-file edit, never scattered per-component magic
// numbers.
//
// AC-8 REUSE note: two rows reuse an EXISTING shipped convention rather than
// inventing a second number for the same metric:
//   - Wellbeing (row 1/2): TopBar.tsx line ~30 (`wb.overall >= 70 ? done :
//     wb.overall >= 45 ? warn : danger`) and the old RightDock `status` tab
//     (line ~142, identical per-part 70/45 split). TopBar.tsx is OUT OF
//     SCOPE for this increment (AC-4 + the lane's file boundary explicitly
//     forbids touching it), so its literal 70/45 could not be repointed at
//     this constant in the same commit — WELLBEING.GREEN/AMBER below are
//     numerically IDENTICAL to TopBar's literals by construction, and every
//     NEW call site this increment owns (populationTabs.tsx's WellbeingTab)
//     imports this single constant rather than re-typing 70/45 a third time.
//     Flagged for Aaron/the lead: a follow-up should repoint TopBar.tsx at
//     this same object once a lane is allowed to touch that file.
//   - Coverage / line-saturation (rows 4/8): reuses the Water tab's existing
//     leak convention (waterBalanceOf flags `leak` at ratio < 0.8,
//     sim/data.ts) — ragForCoverage() and ragForLineSaturation() both read
//     the SAME RAG_THRESHOLDS.COVERAGE_RATIO.AMBER (0.8) literal; there is no
//     separate LINE_SATURATION constant (GR#3 — one number, two consumers).

export type RagState = 'green' | 'amber' | 'red' | 'stub';

/** ONE named thresholds object — AC-8. Never duplicate these numbers locally. */
export const RAG_THRESHOLDS = {
  /** §2 row 1/2 — wellbeingOf(state).overall and each wb.parts[i].value (0-100).
   *  REUSES TopBar.tsx's shipped 70/45 split (see file-header note above). */
  WELLBEING: { GREEN: 70, AMBER: 45 },
  /** §2 row 3 — approvalOf(state) (0-100). */
  APPROVAL: { GREEN: 55, AMBER: 40 },
  /** §2 row 4/8 — serviceCoverageOf() coverage ratio AND lineUsageOf() saturation
   *  headroom. REUSES the Water tab's waterBalanceOf leak line (< 0.8). */
  COVERAGE_RATIO: { GREEN: 1.0, AMBER: 0.8 },
  /** §2 row 6 — unemploymentOf(state) (0..1). Lower is better (inverted band). */
  UNEMPLOYMENT: { GREEN: 0.07, AMBER: 0.15 },
  /** §2 row 9 — housing headroom = (onlineResidentsCapacity - population) / capacity. */
  HOUSING_HEADROOM: { GREEN: 0.2, AMBER: 0.05 },
} as const;

const GREEN = 'var(--done)';
const AMBER = 'var(--warn)';
const RED = 'var(--danger)';
const GREY = 'var(--muted, #888)';

export function ragColor(state: RagState): string {
  switch (state) {
    case 'green': return GREEN;
    case 'amber': return AMBER;
    case 'red': return RED;
    case 'stub': return GREY;
  }
}

/** §2 row 1/2 — a 0-100 wellbeing-style value (overall or any of the 11 parts). */
export function ragForWellbeing(value: number): RagState {
  if (value >= RAG_THRESHOLDS.WELLBEING.GREEN) return 'green';
  if (value >= RAG_THRESHOLDS.WELLBEING.AMBER) return 'amber';
  return 'red';
}

/** §2 row 3 — approvalOf(state) (0-100). */
export function ragForApproval(value: number): RagState {
  if (value >= RAG_THRESHOLDS.APPROVAL.GREEN) return 'green';
  if (value >= RAG_THRESHOLDS.APPROVAL.AMBER) return 'amber';
  return 'red';
}

/** §2 row 4 — serviceCoverageOf() coverage ratio (1.0 = exactly met). */
export function ragForCoverage(coverage: number): RagState {
  if (coverage >= RAG_THRESHOLDS.COVERAGE_RATIO.GREEN) return 'green';
  if (coverage >= RAG_THRESHOLDS.COVERAGE_RATIO.AMBER) return 'amber';
  return 'red';
}

/**
 * §2 row 5 — AC-9: power RAG MUST be computed from isBrownoutActive(state),
 * NEVER a raw cap<need comparison, or a covered shortfall (Grid Import ON,
 * paying the price premium) would wrongly render RED. Pass the three already-
 * computed facts in (avoids re-deriving powerStats/isBrownoutActive here and
 * risking a second, divergent copy — GR#3).
 */
export function ragForPower(opts: { coverageMet: boolean; brownoutActive: boolean }): RagState {
  if (opts.coverageMet) return 'green';
  // Shortfall exists but isBrownoutActive is false => Grid Import is covering it.
  if (!opts.brownoutActive) return 'amber';
  return 'red';
}

/** §2 row 6 — unemploymentOf(state) (0..1). Lower is better. */
export function ragForUnemployment(rate: number): RagState {
  if (rate < RAG_THRESHOLDS.UNEMPLOYMENT.GREEN) return 'green';
  if (rate <= RAG_THRESHOLDS.UNEMPLOYMENT.AMBER) return 'amber';
  return 'red';
}

/** §2 row 7 — waste collection: binary today per the spec's recommendation
 *  (open question 5, stay binary for inc2). */
export function ragForWasteCollection(hasUncollected: boolean): RagState {
  return hasUncollected ? 'red' : 'green';
}

/** §2 row 8 — lineUsageOf() saturation/overCapacity. Reuses COVERAGE_RATIO.AMBER
 *  (0.8) for the "approaching capacity" line per the spec's stated reuse. */
export function ragForLineSaturation(saturation: number, overCapacity: boolean): RagState {
  if (overCapacity) return 'red';
  if (saturation >= RAG_THRESHOLDS.COVERAGE_RATIO.AMBER) return 'amber';
  return 'green';
}

/** §2 row 9 — housing headroom = (capacity - population) / capacity. */
export function ragForHousingHeadroom(headroomFraction: number): RagState {
  if (headroomFraction >= RAG_THRESHOLDS.HOUSING_HEADROOM.GREEN) return 'green';
  if (headroomFraction >= RAG_THRESHOLDS.HOUSING_HEADROOM.AMBER) return 'amber';
  return 'red';
}

/** §2 row 10 — fiscal net/tick: binary per the spec's recommendation
 *  (open question 5, stay binary for inc2 — Insolvency carries the graduated
 *  signal instead). */
export function ragForFiscalNet(net: number): RagState {
  return net >= 0 ? 'green' : 'red';
}

/** §2 row 11 — state.insolvencyState enum, direct mapping, no numeric threshold. */
export function ragForInsolvency(band: string | undefined): RagState {
  if (!band || band === 'solvent') return 'green';
  if (band === 'warning') return 'amber';
  return 'red'; // crisis / administration / bailout_second / decline
}

/** STUB rows (§2 rows 13-15, AC-10): NEVER a colour, always the 'stub' marker
 *  so a player can never mistake an absent metric for a real GREEN. */
export const STUB_RAG: RagState = 'stub';
export const STUB_LABEL = 'not yet available';
