// FEAT-2326609728 inc2 — one-click demand fix, UI HALF.
//
// Pure UI-side helpers shared by the MapView advisor prompt and the DemandDock
// per-service "Fix (N)" buttons, so the two surfaces never diverge on wording
// or on which shortfall is "most pressing". Deliberately kept OUT of
// sim/data.ts / sim/engine.ts (inc1's engine core is landed + rounded and is
// not touched by this increment) — this file only reads demandFixPlan()'s
// output and formats it; it never mutates SimState.
import {
  demandFixPlan,
  SPECS,
  AUTO_BUILD_DEMAND_PERCENT,
  AUTO_BUILD_DEMAND_FRACTION,
  placementCost,
  canEnterSim,
  capacityAtTier,
  totalJobs,
  WORKING_AGE_FRACTION,
  type DemandFixPlanItem,
  type Spec,
} from '../sim/data';
import { demandOf, specUnlocked } from '../sim/engine';
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

// ════════════════════════════════════════════════════════════════════════════
// BUG-641 — ZONE demand-fix advisor (shops/housing/industry).
//
// BUG-606 sized the 12 serviceCoverageOf()/wasteStatsOf() COVERAGE services
// (nursery/primary/.../refuse) via demandFixPlan(). The three ZONE demands —
// residential/commercial/industrial, engine.ts's demandOf() — never went
// through that pipeline: they still fall through to MapView.tsx's legacy
// unsized banners ("Citizens want shops — paint Commercial zones."), Aaron's
// literal, thrice-repeated complaint (BUG-641): "'citizens want shops' no
// help — how much what type a clue would be nice is this one hypermarket or
// 50?"
//
// demandOf()'s residential/commercial/industrial numbers are a -100..100
// PRESSURE INDEX (population/tax/job-mix driven), not a (need, have) pair
// like the coverage services — its internal coefficients (shopBase,
// popFactor, tax adjustments, ...) are private to engine.ts and this file's
// surface is UI-only (demandFixUi.ts), so this module intentionally does
// NOT reimplement or re-derive that formula (GR#3 — a second copy would
// silently drift the moment engine.ts's demandOf() is retuned). Instead the
// index itself IS the raw number reported to the player (the same number
// that already drives the >40 threshold below), and the SIZING quantity is
// built from real, already-EXPORTED capacity primitives that share units
// with the chosen spec's own capacity field, so the message's count/cost and
// a future auto-build handler can never disagree (agreement-by-construction,
// same discipline as DemandFixPlanItem):
//   residential (housing) — unitCapacity is RESIDENTS (sp.residents via
//     capacityAtTier). The physical shortfall closed is (jobs - workers):
//     totalJobs(s) minus s.population*WORKING_AGE_FRACTION (data.ts's own
//     SSOT constant — the SAME 0.55 demandOf()'s `workers` term uses) — the
//     number of extra residents needed to staff the jobs already built,
//     which is exactly the pressure resFacing() responds to (more jobs than
//     workers -> housing demand rises).
//   commercial / industrial (shops / industry) — unitCapacity is JOBS
//     (sp.jobs via capacityAtTier; a handful of starter specs — Corner Shop,
//     Retail Park, the three starter farms — carry no `jobs` field at all,
//     mirroring totalJobs()'s own commercial/industrial fallback of 12/18
//     jobs per unit, data.ts ~2553 — never a NEW literal, the SAME one
//     already governing the money path). The physical shortfall closed is
//     (workers - jobs): unemployed workers who need somewhere to work,
//     the flip side of the residential case, closed by adding JOB capacity
//     via the zone's own building kind rather than by re-deriving
//     shopBase/indBase (private to engine.ts).
// Either shortfall is floored at 1 (`Math.max(1, ...)`, the SAME idiom
// rankedProviders() uses for its own `units` floor, data.ts ~3794) so a zone
// that trips the index threshold always yields an actionable, positive
// count — the index and the underlying jobs/workers gap are correlated but
// not algebraically identical, and a zero-sized recommendation would be
// exactly Aaron's original complaint again, just with a number stapled to
// the front of it.
// ════════════════════════════════════════════════════════════════════════════

/** Legacy MapView.tsx advisor threshold (BUG-641: same threshold value the
 *  old unsized "paint Commercial zones" branches used) — exported so the
 *  lead's MapView hookup can share this ONE constant instead of keeping a
 *  second hand-typed `40` that could silently drift from this file's gate. */
export const ZONE_DEMAND_THRESHOLD = 40;

export type ZoneKey = 'residential' | 'commercial' | 'industrial';

/** Static display label per zone key, mirrors DEMAND_FIX_SERVICE_LABELS'
 *  role for the coverage services (ADVISOR PROSE only, not a game-state SSOT). */
export const ZONE_DEMAND_LABELS: Record<ZoneKey, string> = {
  residential: 'Housing',
  commercial: 'Shops',
  industrial: 'Industry',
};

/**
 * One shortfall-clearing build plan for a single OVER-THRESHOLD zone demand
 * (BUG-641). Deliberately a sibling type to DemandFixPlanItem, not a reuse of
 * it: `serviceKey` there is a serviceCoverageOf()/wasteStatsOf() id resolved
 * against DEMAND_FIX_PROVIDERS (data.ts, engine-private machinery this file
 * does not touch); `zone` here is one of demandOf()'s three ZoneDemand keys,
 * a completely different resolution path (SPECS filtered by `kind`, not by
 * DEMAND_FIX_PROVIDERS). Reusing the same interface would make `serviceKey`
 * ambiguous between two disjoint ID spaces.
 */
export interface ZoneDemandFixItem {
  zone: ZoneKey;
  /** demandOf(s)[zone] — the raw -100..100 pressure index (BUG-641: "how
   *  much" — the SAME number gating the >40 threshold below, always > 40
   *  when this item is present). */
  demandIndex: number;
  /** The buildable spec id chosen for this zone (cheapest unlocked total
   *  plan, same rankedProviders()-style scoring as DemandFixPlanItem). */
  specId: string;
  /** Capacity ONE unit of specId contributes (residents for housing, jobs
   *  for shops/industry — see the module doc comment above). */
  unitCapacity: number;
  /** The physical (jobs vs workers) shortfall this plan targets, in the
   *  SAME unit as unitCapacity — always >= 1 (floored, see module doc). */
  shortfall: number;
  /** Units to place to close AUTO_BUILD_DEMAND_FRACTION of `shortfall`
   *  (always > 0 when this item is present). */
  count: number;
  /** count * specId's placementCost() — the CHOSEN plan's total bill. */
  planCost: number;
  /** The next-best scored candidate for the same target, or null when only
   *  one unlocked provider exists for this zone (same contract as
   *  DemandFixPlanItem.alternative). */
  alternative: { specId: string; count: number; planCost: number } | null;
}

/** Per-zone provider predicate + per-unit capacity extractor — the ZONE
 *  analogue of data.ts's (engine-private) DEMAND_FIX_PROVIDERS, built
 *  entirely from this file's own imports (SPECS/capacityAtTier), so it needs
 *  no engine.ts/data.ts edit. The commercial/industrial 12/18 fallback
 *  mirrors totalJobs()'s own fallback for `jobs`-less specs (data.ts ~2553)
 *  — the SAME two literals already governing the money path, not new ones. */
const ZONE_PROVIDERS: Record<ZoneKey, { match: (sp: Spec) => boolean; unitCapacity: (sp: Spec) => number }> = {
  residential: {
    match: (sp) => sp.kind === 'residential',
    unitCapacity: (sp) => capacityAtTier(sp, 0),
  },
  commercial: {
    match: (sp) => sp.kind === 'commercial',
    unitCapacity: (sp) => (sp.jobs ? capacityAtTier(sp, 0) : 12),
  },
  industrial: {
    match: (sp) => sp.kind === 'industrial',
    unitCapacity: (sp) => (sp.jobs ? capacityAtTier(sp, 0) : 18),
  },
};

/**
 * The ZONE analogue of data.ts's rankedProviders() (BUG-606 "one hypermarket
 * or 50?" scoring), reimplemented locally against ZONE_PROVIDERS rather than
 * imported — rankedProviders() is keyed on DEMAND_FIX_PROVIDERS'
 * serviceKey/DEMAND_FIX_PROVIDERS rule table (engine-private to data.ts's
 * coverage-service machinery) and does not accept an arbitrary match/
 * unitCapacity pair, so there is no exported seam to call through for a
 * ZoneKind instead of a serviceKey. SAME preference order and tie-break as
 * the original (fits budget wholesale, cheapest plan first; else cheapest
 * single-unit-affordable; else cheapest overall; ties broken by fewer units
 * then id ascending — GR#21 determinism).
 *
 * NON-BLOCKING FOLLOW-UP (independent round, 2026-09-03, GR#3 debt noted,
 * not fixed this round): this function AND the commercial/industrial 12/18
 * no-jobs-field fallback in ZONE_PROVIDERS above are a genuine duplicate of
 * data.ts's rankedProviders()/totalJobs() shapes, kept separate only because
 * data.ts's DEMAND_FIX_PROVIDERS table has no seam for an arbitrary
 * (match, unitCapacity) pair keyed by ZoneKind rather than serviceKey. A
 * shared scorer (rankedProviders() generalised to accept either a
 * serviceKey-rule lookup or a direct rule object) would remove this
 * duplication — filed as a fast-follow once data.ts is unclaimed by a
 * concurrent lane (this agent's file surface for BUG-641 is demandFixUi.ts
 * only).
 */
function rankedZoneProviders(
  s: SimState,
  zone: ZoneKey,
  budget: number,
  shortfallTarget: number,
): { sp: Spec; units: number; planCost: number }[] {
  const rule = ZONE_PROVIDERS[zone];
  const candidates: { sp: Spec; units: number; unitCost: number; planCost: number }[] = [];
  for (const sp of Object.values(SPECS)) {
    if (!canEnterSim(sp) || !specUnlocked(s, sp)) continue;
    if (!rule.match(sp)) continue;
    const unitCapacity = rule.unitCapacity(sp);
    if (unitCapacity <= 0) continue;
    const units = Math.max(1, Math.ceil(shortfallTarget / unitCapacity));
    const unitCost = placementCost(sp);
    candidates.push({ sp, units, unitCost, planCost: units * unitCost });
  }
  if (candidates.length === 0) return [];

  const cmp = (a: (typeof candidates)[number], b: (typeof candidates)[number], key: 'planCost' | 'unitCost'): number => {
    if (a[key] !== b[key]) return a[key] - b[key];
    if (a.units !== b.units) return a.units - b.units;
    return a.sp.id < b.sp.id ? -1 : a.sp.id > b.sp.id ? 1 : 0;
  };
  const fitting = candidates.filter((c) => c.planCost <= budget).sort((a, b) => cmp(a, b, 'planCost'));
  const singleAffordable = candidates.filter((c) => c.unitCost <= budget).sort((a, b) => cmp(a, b, 'unitCost'));
  const rest = [...candidates].sort((a, b) => cmp(a, b, 'unitCost'));

  const seen = new Set<string>();
  const ranked: { sp: Spec; units: number; planCost: number }[] = [];
  for (const tier of [fitting, singleAffordable, rest]) {
    for (const c of tier) {
      if (seen.has(c.sp.id)) continue;
      seen.add(c.sp.id);
      ranked.push({ sp: c.sp, units: c.units, planCost: c.planCost });
    }
  }
  return ranked;
}

/**
 * BUG-641: the sized fix for ONE over-threshold zone demand, or null when
 * that zone is not currently over ZONE_DEMAND_THRESHOLD or has no unlocked
 * provider yet (needs-unlock — same omission contract as demandFixPlan()).
 * Pure (GR#21): no Date/Math.random, no mutation, deterministic tie-breaks.
 */
function zoneFixFor(s: SimState, zone: ZoneKey): ZoneDemandFixItem | null {
  const index = demandOf(s)[zone];
  // BUG-641 independent round REJECT r1 (2026-09-03) — BLOCKER: a NaN-
  // poisoned population (or any other corruption that makes demandOf()
  // return NaN) MUST be rejected here explicitly via Number.isFinite(),
  // never via a bare `<=` comparison. In JS every comparison against NaN —
  // `<=`, `<`, `>`, `===` — evaluates to false, so `NaN <= ZONE_DEMAND_THRESHOLD`
  // is FALSE and the intended "not over threshold" early-return was silently
  // SKIPPED (not taken), letting execution fall through into the sizing math
  // below and produce a full plan item with demandIndex/shortfall/count/
  // planCost all NaN — rendering as "NaN short — Fix builds 150%: NaN x
  // Corner Shop (£NaN)" to the player, exactly the "garbage numbers shown to
  // the player" class GR#1/GR#15 exist to prevent. Number.isFinite() is the
  // correct guard for "is this usable as a real number", not another
  // comparison that inherits the same NaN blind spot.
  if (!Number.isFinite(index) || index <= ZONE_DEMAND_THRESHOLD) return null;

  const workers = s.population * WORKING_AGE_FRACTION;
  const jobs = totalJobs(s);
  // Residential closes a JOBS-vs-WORKERS gap (more jobs than workers -> build
  // housing); commercial/industrial close the flip side (more workers than
  // jobs -> build job capacity). Floored at 1 (rankedProviders()'s own
  // `units` floor idiom, data.ts ~3794) so an over-threshold index always
  // yields an actionable count even when the coarse jobs/workers proxy
  // happens to read non-positive for this tick.
  const rawGap = zone === 'residential' ? jobs - workers : workers - jobs;
  const shortfall = Math.max(1, Math.round(rawGap));
  // Defensive belt-and-braces (same round, follow-up hardening): `index` is
  // already proven finite by the guard above, and workers/jobs share the
  // SAME s.population/totalJobs() inputs demandOf() itself consumes to
  // produce that finite index — but shortfall is never taken on faith from
  // that alone. A non-finite shortfall here would mean some OTHER
  // corruption slipped past the index guard; fail closed (omit the item)
  // rather than propagate a NaN-poisoned count/planCost downstream.
  if (!Number.isFinite(shortfall)) return null;
  const fixAmount = shortfall * AUTO_BUILD_DEMAND_FRACTION;

  const ranked = rankedZoneProviders(s, zone, s.funds, fixAmount);
  if (ranked.length === 0) return null; // no unlocked provider yet
  const chosen = ranked[0];
  const alt = ranked.find((c) => c.sp.id !== chosen.sp.id) ?? null;

  return {
    zone,
    demandIndex: index,
    specId: chosen.sp.id,
    unitCapacity: ZONE_PROVIDERS[zone].unitCapacity(chosen.sp),
    shortfall,
    count: chosen.units,
    planCost: chosen.planCost,
    alternative: alt ? { specId: alt.sp.id, count: alt.units, planCost: alt.planCost } : null,
  };
}

/**
 * BUG-641: every currently over-threshold zone demand, sized (residential,
 * commercial, industrial — fixed order, GR#21 determinism independent of any
 * object-key iteration order). Pure (GR#21).
 */
export function zoneDemandFixPlan(s: SimState): ZoneDemandFixItem[] {
  const zones: ZoneKey[] = ['residential', 'commercial', 'industrial'];
  const out: ZoneDemandFixItem[] = [];
  for (const zone of zones) {
    const fix = zoneFixFor(s, zone);
    if (fix) out.push(fix);
  }
  return out;
}

/**
 * BUG-641: the single most-pressing zoneDemandFixPlan() entry, by raw
 * demandIndex (the SAME monotone-ranking heuristic worstDemandFix() applies
 * to coverage services' (need-have) gap — here the "gap" IS the index
 * itself, since that is the only shortfall measure common to all three
 * zones). Deterministic tie-break by zone key (GR#21). Returns null when no
 * zone is over threshold.
 */
export function zoneDemandFix(s: SimState): ZoneDemandFixItem | null {
  const plan = zoneDemandFixPlan(s);
  if (plan.length === 0) return null;
  const ranked = [...plan].sort((a, b) => b.demandIndex - a.demandIndex || a.zone.localeCompare(b.zone));
  return ranked[0];
}

/**
 * BUG-641 (Aaron, thrice: "'citizens want shops' no help ... is this one
 * hypermarket or 50?") — the zone-demand analogue of demandFixMessage(),
 * built ENTIRELY from a ZoneDemandFixItem's own fields so the message and a
 * future click handler acting on the SAME object can never disagree
 * (agreement-by-construction). Format mirrors demandFixMessage() exactly:
 *   "<Zone>: demand <index> — Fix builds <P>%: <count> x <Name> (<£cost>)
 *    or <count> x <AltName> (<£cost>) — cheapest picked"
 * `<P>` is AUTO_BUILD_DEMAND_PERCENT (data.ts), the SAME constant
 * demandFixMessage() uses — one sizing convention across coverage AND zone
 * demand. Pure formatting (GR#21): no Date/Math.random, no mutation.
 */
export function zoneDemandMessage(item: ZoneDemandFixItem): string {
  const label = ZONE_DEMAND_LABELS[item.zone];
  const chosenName = SPECS[item.specId]?.name ?? item.specId;
  const chosen = `${formatBuildingCount(chosenName, item.count)} (${fmtMoney(item.planCost)})`;
  // `shortfall` (jobs/residents, the SAME unit as unitCapacity/count) is used
  // for the "<N> short" clause rather than the raw -100..100 `demandIndex` —
  // it reads naturally as a "short" quantity exactly like demandFixMessage()'s
  // (need-have) shortfall, and shares fmtShortfall()'s display bands so a
  // real sub-1 gap can never silently read as "0 short" here either (the
  // original BUG-606 defect class). `demandIndex` stays on the item for any
  // caller that wants the raw -100..100 pressure number directly (e.g. a
  // secondary "(demand index N)" suffix at wiring time).
  const shortfallText = fmtShortfall(item.shortfall);
  if (!item.alternative) {
    return `${label}: ${shortfallText} short — Fix builds ${AUTO_BUILD_DEMAND_PERCENT}%: ${chosen}`;
  }
  const altName = SPECS[item.alternative.specId]?.name ?? item.alternative.specId;
  const alt = `${formatBuildingCount(altName, item.alternative.count)} (${fmtMoney(item.alternative.planCost)})`;
  return `${label}: ${shortfallText} short — Fix builds ${AUTO_BUILD_DEMAND_PERCENT}%: ${chosen} or ${alt} — cheapest picked`;
}

/**
 * BUG-641: the single worst outstanding demand-fix across BOTH the coverage
 * services (worstDemandFix()) and the three zone demands (zoneDemandFix()),
 * so the MapView advisor hookup calls exactly ONE function instead of
 * chaining two branches itself. Ranking basis necessarily differs per side
 * (coverage: absolute (need-have) gap in service-specific units; zone: the
 * -100..100 pressure index) — there is no common unit to compare them on
 * directly, so this picks by RAW GAP MAGNITUDE (Math.abs of each side's own
 * ranking number) as the best available cross-domain "which hurts worse"
 * proxy, coverage services winning ties (they were the original BUG-606
 * priority surface; a zone entry only displaces one when its index gap is
 * STRICTLY larger). Deterministic (GR#21): both inputs are themselves
 * deterministic, and the tie-break is fixed.
 *
 * NON-BLOCKING FINDING (independent round, 2026-09-03, confirmed live with
 * concrete numbers by attack-bug641-zone-demand.test.mjs — not fixed this
 * round, a normalisation bug will be filed): the two "gap" numbers compared
 * above are NOT the same unit — a coverage gap is typically in the
 * tens-of-thousands (people/served/tonnes), while a zone index is
 * mathematically capped at 100. In the common case (any real coverage
 * shortfall) coverage wins simply because its gap dwarfs 100, which reads as
 * "right" but is really an accident of scale, not a real comparison; at very
 * small populations (e.g. ~45) a barely-over-threshold zone index (41) CAN
 * legitimately outrank a genuine but small coverage shortfall purely because
 * the units are incomparable. A real fix needs a shared normalisation (e.g.
 * both sides expressed as a fraction of their own "how bad can this get"
 * ceiling) before this comparison is anything more than magnitude-shaped.
 */
export function worstAnyDemandFix(s: SimState): DemandFixPlanItem | ZoneDemandFixItem | null {
  const coverage = worstDemandFix(s);
  const zone = zoneDemandFix(s);
  if (!coverage) return zone;
  if (!zone) return coverage;
  const coverageGap = Math.abs(coverage.need - coverage.have);
  const zoneGap = Math.abs(zone.demandIndex);
  return zoneGap > coverageGap ? zone : coverage;
}
