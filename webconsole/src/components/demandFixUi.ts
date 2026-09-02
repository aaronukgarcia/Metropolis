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
 * Sane pluralization of a building display name for prompts like
 * "place 10 Water Towers". count === 1 keeps the singular; names ending
 * -s/-x/-z/-ch/-sh take -es; a consonant + -y becomes -ies; everything else
 * takes a plain -s. Covers the small closed set of SPECS names in this game —
 * no i18n library warranted. Pure (GR#21): no Date/Math.random.
 */
export function pluralizeBuildingName(name: string, count: number): string {
  if (count === 1) return name;
  if (/([sxz]|[cs]h)$/i.test(name)) return `${name}es`;
  if (/[^aeiou]y$/i.test(name)) return `${name.slice(0, -1)}ies`;
  return `${name}s`;
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
