// FEAT-2326609728 inc2 — one-click demand fix, UI HALF.
//
// Pure UI-side helpers shared by the MapView advisor prompt and the DemandDock
// per-service "Fix (N)" buttons, so the two surfaces never diverge on wording
// or on which shortfall is "most pressing". Deliberately kept OUT of
// sim/data.ts / sim/engine.ts (inc1's engine core is landed + rounded and is
// not touched by this increment) — this file only reads demandFixPlan()'s
// output and formats it; it never mutates SimState.
import { demandFixPlan, SPECS, AUTO_BUILD_DEMAND_PERCENT, type DemandFixPlanItem } from '../sim/data';
import type { SimState } from '../sim/types';
import { fmtMoney, fmtNum } from '../sim/utils';

/**
 * BUG-587: the old pluralizeBuildingName() applied an English -s/-x/-z/-ch/-sh
 * -> -es rule that mangled 32 of the 203 catalogue names — "Water Works"
 * (already ends in -s) became "Water Workses", "Crossroads" became
 * "Crossroadses", "Versailles" became "Versailleses", "St Peter's" became
 * "St Peter'ses" — on both the MapView advisor prompt and the DemandDock Fix
 * button title. BUG-583 already solved this exact class in engine.ts's
 * formatPlacedCount() (the placeNotice line) by sidestepping pluralisation
 * entirely: "N x <Name>" reads correctly regardless of the name's own
 * grammatical number. This is that same shape, reimplemented here rather
 * than imported from sim/engine.ts — the BUG-583 round explicitly endorsed
 * NOT sharing a formatter across the sim/UI boundary for a function this
 * trivial (GR#3: two tiny formatters producing the SAME output shape are an
 * acceptable, deliberate exception to "no duplication without validation"
 * when the alternative is a component importing from the engine internals).
 * Pure (GR#21): no Date/Math.random.
 */
export function formatBuildingCount(name: string, count: number): string {
  return `${count} x ${name}`;
}

/**
 * Short, static display label per demandFixPlan() serviceKey, for the
 * advisor's "fixes 50% of <service> demand" sentence (BUG-601). Deliberately separate
 * from serviceCoverageOf()'s ServiceCoverage.label (which embeds live MW
 * numbers for 'power' and reads oddly inline in a sentence) and from
 * DemandDock's per-row label (which stays exactly as serviceDemandOf()
 * reports it — SSOT for the row itself, this map is ADVISOR PROSE only).
 */
export const DEMAND_FIX_SERVICE_LABELS: Record<string, string> = {
  nursery: 'nursery places',
  primary: 'school places',
  college: 'college places',
  gp: 'GP coverage',
  hosp: 'hospital coverage',
  police: 'police coverage',
  cleanwater: 'clean water',
  waste: 'sewage',
  power: 'power',
  refuse: 'refuse collection',
  // FEAT-demanddock-overhaul / BUG-571: fire now has a real DEMAND_FIX_PROVIDERS
  // rule (data.ts) so demandFixPlan() can emit a 'fire' entry — without this
  // label the advisor's fallback `?? fix.serviceKey` would render the raw key.
  fire: 'fire cover',
  // BUG-572 follow-up: parks/leisure now has a real serviceCoverageOf() row
  // + DEMAND_FIX_PROVIDERS rule (data.ts) so demandFixPlan() can emit a
  // 'parks' entry — same fallback-key reasoning as fire above.
  parks: 'park space',
};

/**
 * The single most-pressing demandFixPlan() entry, by absolute coverage gap
 * (need - have — the RAW outstanding shortfall, not the 50%-of-gap amount
 * BUG-601's demandFixPlan() actually sizes each action to; this is a pure
 * ranking heuristic for "which service hurts most right now", monotone in
 * the same (need, have) pair regardless of how much of it one action
 * clears), largest first. Deterministic tie-break by serviceKey (GR#21 —
 * never rely on array order alone when two gaps tie). Returns null when
 * nothing is short.
 */
export function worstDemandFix(s: SimState): DemandFixPlanItem | null {
  const plan = demandFixPlan(s);
  if (plan.length === 0) return null;
  const ranked = plan
    .map((item) => ({ item, gap: item.need - item.have }))
    .sort((a, b) => b.gap - a.gap || a.item.serviceKey.localeCompare(b.item.serviceKey));
  return ranked[0].item;
}

/**
 * D3 (BUG-606 independent round REJECT r1 AND r2, Aaron 2026-09-03) —
 * "'citizens want shops' no help" round 1 got a sized message, but
 * `fmtNum()` (thousands-separated INTEGER) silently truncated any real
 * sub-1 gap to "0 short" while the row still recommended a genuine purchase
 * — the exact original complaint, reintroduced at small scale. The r1 fix
 * (`gap.toFixed(1)` for any 0<gap<1) was ITSELF still wrong at the low end:
 * the independent round's r2 attack found 0.046 rendering as "0.0 short" —
 * one decimal place is not enough resolution below roughly 0.05, and "0.0"
 * reads exactly as misleadingly as "0" did.
 *
 * FIX: three display bands, never a THIRD row-existence threshold (see
 * below) —
 *   gap <  0.05           -> "<1"                (too small for ANY decimal
 *                                                  rendering to read honestly;
 *                                                  "<1" is itself the honest
 *                                                  answer to "how much?")
 *   0.05 <= gap < 1        -> 2 significant figures via toPrecision(2)
 *                            (0.46 -> "0.46", 0.096 -> "0.096" — never
 *                             rounds a real sub-1 gap down to a bare "0")
 *   gap >= 1               -> fmtNum() (thousands-separated integer, SSOT)
 *
 * This is DISPLAY PRECISION, not suppression: demandFixPlan()'s own
 * `shortfall <= 0` gate remains the ONLY threshold deciding whether a row
 * EXISTS at all (see its doc comment) — the 0.05 boundary here only chooses
 * how a real, already-decided-to-exist gap is WORDED, never whether the row
 * itself is shown. Adding a second row-EXISTENCE threshold here would risk
 * this function and demandFixPlan() disagreeing about whether a row should
 * exist (exactly the class of divergence this whole feature exists to
 * prevent) — that risk does not apply to a pure wording choice.
 */
function fmtShortfall(gap: number): string {
  if (gap > 0 && gap < 0.05) return '<1';
  if (gap > 0 && gap < 1) return gap.toPrecision(2);
  return fmtNum(gap);
}

/**
 * BUG-606 (Aaron, 2026-09-03, twice: "'citizens want shops' no help - how
 * much what type a clue would be nice is this one hypermarket or 50?") — a
 * quantified, SIZED demand-fix message built ENTIRELY from a
 * DemandFixPlanItem's own fields (need/have/count/specId/planCost/
 * alternative), never re-derived independently — so this text and the
 * Fix/Auto-build/Fix-All button that executes the SAME plan object can never
 * disagree (agreement-by-construction). Format (Aaron-proposed, Q100093):
 *   "<label>: <N> short — Fix builds <P>%: <count> x <Name> (<£cost>)
 *    or <count> x <AltName> (<£cost>) — cheapest picked"
 * `<P>` is AUTO_BUILD_DEMAND_PERCENT (data.ts), derived from the SAME
 * AUTO_BUILD_DEMAND_FRACTION demandFixPlan() sizes against — GR#15, never a
 * hand-typed "50%"/"150%" that could drift from the real sizing arithmetic
 * (Aaron's 2026-09-03 superseding ruling moved the fraction 0.5 -> 1.5; this
 * string updated itself with zero code change here). The " or ... — cheapest
 * picked" clause is only appended when a real alternative provider exists (a
 * single-unlocked-provider service, e.g. a new city with only one tier of a
 * facility unlocked, shows just the chosen option). `planCost`/alternative's
 * `planCost` are placementCost()-derived (D1 fix) — a £0 free-zone plan (e.g.
 * parks) renders honestly as "£0", never the catalogue price. Pure
 * formatting: no Date/Math.random (GR#21), no mutation.
 */
export function demandFixMessage(item: DemandFixPlanItem): string {
  const rawLabel = DEMAND_FIX_SERVICE_LABELS[item.serviceKey] ?? item.serviceKey;
  const label = rawLabel.charAt(0).toUpperCase() + rawLabel.slice(1);
  const shortfall = fmtShortfall(item.need - item.have);
  const chosenName = SPECS[item.specId]?.name ?? item.specId;
  const chosen = `${formatBuildingCount(chosenName, item.count)} (${fmtMoney(item.planCost)})`;
  if (!item.alternative) {
    return `${label}: ${shortfall} short — Fix builds ${AUTO_BUILD_DEMAND_PERCENT}%: ${chosen}`;
  }
  const altName = SPECS[item.alternative.specId]?.name ?? item.alternative.specId;
  const alt = `${formatBuildingCount(altName, item.alternative.count)} (${fmtMoney(item.alternative.planCost)})`;
  return `${label}: ${shortfall} short — Fix builds ${AUTO_BUILD_DEMAND_PERCENT}%: ${chosen} or ${alt} — cheapest picked`;
}
