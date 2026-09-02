// FEAT-2326609728 inc2 — one-click demand fix, UI HALF.
//
// Pure UI-side helpers shared by the MapView advisor prompt and the DemandDock
// per-service "Fix (N)" buttons, so the two surfaces never diverge on wording
// or on which shortfall is "most pressing". Deliberately kept OUT of
// sim/data.ts / sim/engine.ts (inc1's engine core is landed + rounded and is
// not touched by this increment) — this file only reads demandFixPlan()'s
// output and formats it; it never mutates SimState.
import { demandFixPlan, type DemandFixPlanItem } from '../sim/data';
import type { SimState } from '../sim/types';

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
 * advisor's "clears <service> demand +5%" sentence. Deliberately separate
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
};

/**
 * The single most-pressing demandFixPlan() entry, by absolute coverage gap
 * (need*1.05 - have — the same quantity demandFixPlan() itself clears),
 * largest first. Deterministic tie-break by serviceKey (GR#21 — never rely on
 * array order alone when two gaps tie). Returns null when nothing is short.
 */
export function worstDemandFix(s: SimState): DemandFixPlanItem | null {
  const plan = demandFixPlan(s);
  if (plan.length === 0) return null;
  const ranked = plan
    .map((item) => ({ item, gap: item.need * 1.05 - item.have }))
    .sort((a, b) => b.gap - a.gap || a.item.serviceKey.localeCompare(b.item.serviceKey));
  return ranked[0].item;
}
