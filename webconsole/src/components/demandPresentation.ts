// FEAT-9995385592 (UI overhaul UX-2) — the ONE place the DemandDock's colour
// law, direction phrases and need-vs-have wording live (AC-2.1..2.5).
//
// Presentation only: every number here is read from an existing selector
// (serviceCoverageOf / wasteStatsOf / demandOf / totalJobs / countByKindOnline /
// zoneDemandFixPlan) and formatted; nothing here changes the sim's demand
// maths or touches SimState. GR#3: components and tests import THESE
// thresholds/phrases, never hand-typed copies.
//
// THE COLOUR LAW (each colour means exactly one thing, in every demand
// surface — bar fill, value, row accent, summary strip):
//   red    (action)  — a service is short AND needs you (large shortfall, or a
//                      brownout). Fix lives here.
//   amber  (watch)   — a service is short but small, already being built, or a
//                      bought-in cover is masking it. Fix may live here.
//   green  (healthy) — a service is covered. NEVER used for zone demand and
//                      never for "build more".
//   blue   (build)   — zoning (R/C/I) pressure to build more; an opportunity,
//                      not a fault, always paired with an arrow and words.
//   grey   (idle)    — balanced or oversupplied: nothing to do.
// Colour follows MEANING, not the sign of the -100..100 index.
import { serviceCoverageOf, wasteStatsOf, totalJobs, countByKindOnline, isBrownoutActive, effectiveCleanWaterCoverageOf, effectiveWastewaterCoverageOf, effectiveRefuseCoverageOf, AUTO_BUILD_DEMAND_PERCENT, type DemandFixPlanItem } from '../sim/data';
import { demandOf, WORKFORCE_PARTICIPATION_RATE } from '../sim/engine';
import type { SimState } from '../sim/types';
import { fmtNum, formatPower } from '../sim/utils';
import { DEMAND_FIX_SERVICE_LABELS, ZONE_DEMAND_THRESHOLD, zoneDemandFixPlan, formatMixText, type ZoneKey } from './demandFixUi';

export type DemandTone = 'action' | 'watch' | 'healthy' | 'build' | 'idle';

/** The colour law as data — the dock's CSS classes and the tests both read it. */
export const DEMAND_COLOUR_LAW: Record<DemandTone, { colour: 'red' | 'amber' | 'green' | 'blue' | 'grey'; meaning: string }> = {
  action: { colour: 'red', meaning: 'A service is short and needs you' },
  watch: { colour: 'amber', meaning: 'A service is short, but small or already being handled' },
  healthy: { colour: 'green', meaning: 'A service is covered' },
  build: { colour: 'blue', meaning: 'Zoning pressure: the city would take more of this' },
  idle: { colour: 'grey', meaning: 'Balanced or oversupplied: nothing to do' },
};

/** A service shortfall index at/above this is red; below it (but > 0) amber. */
export const SERVICE_ACTION_INDEX = 30;
/** Zone index at/above this is "high demand" (shares the advisor's threshold). */
export const ZONE_HIGH_INDEX = ZONE_DEMAND_THRESHOLD;
/** Zone index magnitude below this reads "balanced". */
export const ZONE_BALANCED_BAND = 10;

/** Meter end caps + midpoint (AC-2.3), so the direction of travel is stated. */
export const METER_CAPS = {
  zone: { low: 'oversupplied', mid: 'balanced', high: 'build more' },
  service: { low: 'spare', mid: 'covered', high: 'short' },
} as const;

/** Tones that mean "needs your action" (the only rows that may carry Fix). */
export function isActionTone(t: DemandTone): boolean {
  return t === 'action' || t === 'watch';
}

export interface ServiceToneInput {
  value: number;
  alert?: boolean;
  hasFix: boolean;
  inProgress: boolean;
}

/** Colour for a SERVICE row, by meaning. Positive index = shortfall. */
export function serviceTone(i: ServiceToneInput): DemandTone {
  // A brownout alert is real whatever the index: tone 'action' agrees with the
  // 'Brownout' phrase even when the index itself is NaN.
  if (i.alert) return 'action';
  // A non-finite index is UNKNOWN, never healthy: fail to the neutral tone.
  if (!Number.isFinite(i.value)) return 'idle';
  if (i.hasFix) return i.value >= SERVICE_ACTION_INDEX ? 'action' : 'watch';
  if (i.inProgress) return 'watch';
  if (i.value > 0) return i.value >= SERVICE_ACTION_INDEX ? 'action' : 'watch';
  return 'healthy';
}

/** Colour + words for a ZONE (R/C/I) row from its -100..100 pressure index. */
export function zoneTone(value: number): DemandTone {
  if (!Number.isFinite(value)) return 'idle';
  if (value >= ZONE_BALANCED_BAND) return 'build';
  return 'idle';
}

const ZONE_NOUN: Record<ZoneKey, { more: string; none: string }> = {
  residential: { more: 'housing', none: 'homes' },
  commercial: { more: 'shops', none: 'shops' },
  industrial: { more: 'industry', none: 'factories' },
};

/** Inputs the zone index was derived from, so a phrase never states a cause they do not show. */
export interface ZoneCauseInputs {
  jobs: number;
  workers: number;
}

/**
 * The plain direction phrase for a zone index (AC-2.2). Never reads -100 as unmet demand.
 * F3: the residential CAUSE ("too many jobs" / "more workers than jobs") is stated
 * only when `ctx` (jobs vs workers) shows it; otherwise the wording is neutral.
 */
export function zonePhrase(zone: ZoneKey, value: number, ctx?: ZoneCauseInputs): string {
  const n = ZONE_NOUN[zone];
  if (!Number.isFinite(value)) return 'Demand unknown';
  const jobsExceed = !!ctx && ctx.jobs > ctx.workers;
  const workersExceed = !!ctx && ctx.workers > ctx.jobs;
  if (value >= ZONE_HIGH_INDEX) {
    return zone === 'residential'
      ? `High demand — ${jobsExceed ? 'too many jobs for the workforce, ' : ''}zone more housing`
      : `High demand — zone more ${n.more}`;
  }
  if (value >= ZONE_BALANCED_BAND) {
    return zone === 'residential' ? 'Some demand — a little more housing would fill' : `Some demand — room for more ${n.more}`;
  }
  if (value > -ZONE_BALANCED_BAND) return 'Balanced — nothing to build';
  return zone === 'residential'
    ? `Oversupplied — ${workersExceed ? 'more workers than jobs, build jobs not homes' : 'no more homes needed'}`
    : `Oversupplied — no more ${n.none} needed (building more would only sit idle)`;
}

const SERVICE_UNIT: Record<string, string> = {
  nursery: 'places',
  primary: 'places',
  college: 'places',
  gp: 'people covered',
  hosp: 'people covered',
  police: 'people covered',
  fire: 'people covered',
  cleanwater: 'people supplied',
  waste: 'people connected',
  refuse: 'tonnes collected',
  parks: 'park area',
};

function fmtQty(n: number): string {
  if (!Number.isFinite(n)) return '0';
  if (Math.abs(n) >= 10) return fmtNum(n);
  return n.toLocaleString('en-GB', { maximumFractionDigits: 1 });
}

function fmtServiceQty(id: string, n: number): string {
  return id === 'power' ? formatPower(n) : fmtQty(n);
}

export interface ServiceQuantity {
  need: number;
  have: number;
}

/** Real need/have for every service the dock lists — the same rows demandFixPlan reads. */
export function serviceQuantitiesOf(s: SimState): Map<string, ServiceQuantity> {
  const out = new Map<string, ServiceQuantity>();
  for (const c of serviceCoverageOf(s)) out.set(c.id, { need: c.need, have: c.cap });
  const w = wasteStatsOf(s);
  out.set('refuse', { need: w.generated, have: w.capacity });
  return out;
}

/** "Need 1,200, have 800 places" (AC-2.2). */
export function needHaveText(id: string, q: ServiceQuantity | undefined): string {
  if (!q || q.need <= 0) return 'Nothing needed yet';
  const unit = SERVICE_UNIT[id];
  const tail = unit ? ` ${unit}` : '';
  return `Need ${fmtServiceQty(id, q.need)}, have ${fmtServiceQty(id, q.have)}${tail}`;
}

/**
 * F2: is this service's raw shortfall really being substituted by an import /
 * contract / grid import right now? Reads the SAME effective-coverage SSOT the
 * demand index reads (effective*CoverageOf), so it can never disagree with it.
 * Only water, wastewater, refuse and power can be bought in.
 */
export function isBoughtIn(s: SimState, id: string): boolean {
  const q = serviceQuantitiesOf(s).get(id);
  if (!q || !(q.need > q.have)) return false;
  const raw = q.need > 0 ? q.have / q.need : 1;
  if (id === 'cleanwater') return effectiveCleanWaterCoverageOf(s) > raw;
  if (id === 'waste') return effectiveWastewaterCoverageOf(s) > raw;
  if (id === 'refuse') return effectiveRefuseCoverageOf(s) > raw;
  if (id === 'power') return !isBrownoutActive(s);
  return false;
}

/**
 * True when the Fix placement may differ from the labelled mix because funds
 * are below plan.planCost (the reducer then substitutes a cheaper mix).
 */
export function fixMayFallShort(plan: DemandFixPlanItem, funds: number): boolean {
  return !(plan.planCost <= funds);
}

/**
 * F1 (r4 lead ruling): the Fix button label from the WHOLE plan.mix. With funds
 * >= planCost it is an UPPER BOUND ("Build up to 4 x Nature Reserve + 1 x
 * Botanical Garden"): the reducer also pays connector roads from the same funds,
 * so it may place fewer, but never more per spec nor a spec outside the plan.
 * Below planCost it keeps "(as funds allow)". No render-time dry run.
 */
export function fixLabelOf(plan: DemandFixPlanItem, funds: number): string {
  return fixMayFallShort(plan, funds)
    ? `Build ${formatMixText(plan)} (as funds allow)`
    : `Build up to ${formatMixText(plan)}`;
}

/** Hover title for the Fix button; same mix text and same funds test as the label. */
export function fixTitleOf(plan: DemandFixPlanItem, funds: number): string {
  const note = fixMayFallShort(plan, funds)
    ? ' (funds are short: a cheaper mix may be substituted)'
    : ' (roads may use some of the funds, so fewer may be built)';
  return `Place ${formatMixText(plan)} to fix ${AUTO_BUILD_DEMAND_PERCENT}% of this shortfall${note}`;
}

export interface ServiceRowView {
  id: string;
  tone: DemandTone;
  phrase: string;
  quantity: string;
  fixAllowed: boolean;
}

/** Everything the dock prints for one service row, in words. */
export function serviceRowView(
  id: string,
  value: number,
  alert: boolean | undefined,
  q: ServiceQuantity | undefined,
  hasFix: boolean,
  inProgressUnits: number,
  boughtIn = false,
): ServiceRowView {
  const tone = serviceTone({ value, alert, hasFix, inProgress: inProgressUnits > 0 });
  const noun = DEMAND_FIX_SERVICE_LABELS[id] ?? id;
  let phrase: string;
  if (alert) phrase = `Brownout — ${noun} demand exceeds supply`;
  else if (!Number.isFinite(value)) phrase = `Demand for ${noun} unknown`;
  else if (tone === 'healthy') phrase = `Enough ${noun}`;
  else if (inProgressUnits > 0 && !hasFix) {
    phrase = `Needs more ${noun} — ${inProgressUnits} building${inProgressUnits === 1 ? '' : 's'} on the way`;
  } else if (value <= 0) {
    phrase = boughtIn ? `Needs more ${noun} — shortfall is being bought in` : `Needs more ${noun} — small shortfall`;
  } else phrase = `Needs more ${noun}`;
  return { id, tone, phrase, quantity: needHaveText(id, q), fixAllowed: isActionTone(tone) && hasFix };
}

export interface ZoneRowView {
  zone: ZoneKey;
  tone: DemandTone;
  phrase: string;
  /** What the sim actually computes for this zone, in plain numbers. */
  quantity: string;
  /** Hover/expand: the model inputs behind the index (AC-2.3). */
  inputs: string;
}

export function zoneRowViews(s: SimState): Record<ZoneKey, ZoneRowView> {
  const d = demandOf(s);
  const jobs = totalJobs(s);
  const workers = s.population * WORKFORCE_PARTICIPATION_RATE;
  const online = countByKindOnline(s);
  const fixes = new Map(zoneDemandFixPlan(s).map((f) => [f.zone, f]));
  const mk = (zone: ZoneKey, value: number): ZoneRowView => {
    const fix = fixes.get(zone);
    const ctx = { jobs, workers };
    let quantity: string;
    let inputs: string;
    if (zone === 'residential') {
      quantity = `${fmtNum(jobs)} jobs for ${fmtNum(workers)} workers`;
      inputs = `Housing demand compares jobs (${fmtNum(jobs)}) with the workforce (${fmtNum(workers)}), nudged by the average tax rate.`;
    } else {
      const built = zone === 'commercial' ? online.commercial : online.industrial;
      const what = zone === 'commercial' ? 'shop' : 'industrial';
      quantity = `${fmtNum(built)} ${what} building${built === 1 ? '' : 's'} running`;
      inputs = `${zone === 'commercial' ? 'Shop' : 'Industry'} demand compares the ${fmtNum(built)} running ${what} buildings with what the population of ${fmtNum(s.population)} supports (the sim does not publish a target count), nudged by tax.`;
    }
    if (fix) quantity += ` — short by ${fmtNum(fix.shortfall)} ${zone === 'residential' ? 'residents' : 'jobs'}`;
    return { zone, tone: zoneTone(value), phrase: zonePhrase(zone, value, ctx), quantity, inputs };
  };
  return {
    residential: mk('residential', d.residential),
    commercial: mk('commercial', d.commercial),
    industrial: mk('industrial', d.industrial),
  };
}

/** Bar fill arrow/label so the direction is never colour-only. */
export function zoneArrow(value: number): string {
  if (!Number.isFinite(value)) return '? unknown';
  if (value >= ZONE_BALANCED_BAND) return '▲ build more';
  if (value > -ZONE_BALANCED_BAND) return '● balanced';
  return '▼ oversupplied';
}

export interface DemandSummary {
  actionCount: number;
  /** F4: the WORST row tone (action > watch), 'healthy' when nothing needs action. */
  tone: 'action' | 'watch' | 'healthy' | 'unknown';
  text: string;
  /** Rows whose demand is not a finite number (unknown, never healthy). */
  unknownCount: number;
}

/** One-line collapsed summary (AC-2.5): worst item + count of the others. */
export function demandSummary(
  rows: { label: string; tone: DemandTone; value: number; phrase: string }[],
): DemandSummary {
  const acts = rows
    .filter((r) => isActionTone(r.tone))
    .sort((a, b) => {
      if ((a.tone === 'action') !== (b.tone === 'action')) return a.tone === 'action' ? -1 : 1;
      return b.value - a.value || (a.label < b.label ? -1 : a.label > b.label ? 1 : 0);
    });
  const unknownCount = rows.filter((r) => !Number.isFinite(r.value)).length;
  if (acts.length === 0) {
    if (unknownCount > 0) return { actionCount: 0, tone: 'unknown', unknownCount, text: `Some demand unknown (${unknownCount} row${unknownCount === 1 ? '' : 's'})` };
    return { actionCount: 0, tone: 'healthy', unknownCount, text: 'All services covered' };
  }
  const rest = acts.length - 1;
  return { actionCount: acts.length, unknownCount, tone: acts[0].tone === 'action' ? 'action' : 'watch', text: `${acts[0].label}: ${acts[0].phrase}${rest > 0 ? ` (+${rest} more)` : ''}` };
}
