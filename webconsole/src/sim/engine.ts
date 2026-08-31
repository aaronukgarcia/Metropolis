// engine.ts — pure (non-React) simulation core.
//
// Extracted from store.tsx so the reducer and its helpers can be unit-tested
// directly: `node --test` type-strips .ts, but NOT the JSX in store.tsx. Every
// piece of game logic lives here; store.tsx keeps only the React wiring and
// re-exports these symbols so existing `'../sim/store'` imports keep working.

import {
  PIPE_TIERS,
  SPECS,
  canEnterSim,
  countByKind,
  fits,
  isOnline,
  occupiedSet,
  placementCost,
  serviceCoverageOf,
  earlyGameFactor,
  brownoutOf,
  BROWNOUT_WELLBEING_K,
  stationLinks,
  totalJobs,
  waterBalanceOf,
  unlockedAtLevel,
  MAP_H,
  MAP_W,
  powerStats,
  isRoadSpec,
  isRailSpec,
  isMotorwayClassSpec,
  roadTierOf,
  fittingTier,
  ROAD_TIER_SPECS,
  ROAD_TIER_CAPACITY,
  computeRoadConnectivity,
  collectionCoverageOf,
  collectionOpexOf,
  landfillTippingOf,
  recyclingRevenueOf,
  compostRevenueOf,
  RAIL_BRIDGE_COST_MULTIPLIER,
  MOTORWAY_JUNCTION_COST,
  residentsCapacity,
} from './data.ts';
import type { Spec, RoadTier } from './data.ts';
import { planConnector } from './roadConnect.ts';
import { planRailBranch, RAIL_BRANCH_BUDGET } from './railConnect.ts';
import type { Building, RoadMonitor, BuildingMonitor } from './types.ts';
import { fmtMoney } from './utils.ts';
import { councilTaxPerTick, businessTaxPerTick, wagesPerTick, gridExportRevenuePerTick, GRID_EXPORT_TARIFF_PER_MW, applyOutflowPolicies, UPKEEP_BUCKET, overdraftInterestPerTick, sanitizeFunds } from './fiscal.ts';
import type {
  FlowItem,
  LedgerEntry,
  LevelUpNotice,
  PolicyId,
  SimState,
  TaxRates,
  Tool,
} from './types.ts';

export const LOAN_PRINCIPAL = 25000;
export const LOAN_TOTAL = 27500;
const MOVE_COST = 25;

/** Milliseconds per tick at each speed (0 = paused). SSOT for the game clock —
 * the store's interval and the debug snapshot's tick-rate both derive from it. */
export const SPEED_MS: Record<SimState['speed'], number> = { 0: 0, 1: 900, 2: 420, 3: 160 };

/** Rolling caps (changelog-cap style): history / ledger keep at most this many
 * entries. Referenced by the debug snapshot so the displayed cap can't drift. */
export const HISTORY_CAP = 240;
export const LEDGER_CAP = 200;

/**
 * Milestone cash-injection rate (FEAT-1972079884): a level-up grants this
 * fraction of current funds. PLACEHOLDER under the balance-number regime —
 * directional only, pending Aaron's row-by-row balance pass.
 */
export const LEVEL_REWARD_RATE = 0.1;

// BUG-394 FIX: Population growth rate constant
// PLACEHOLDER (balance-number regime, pending Aaron's approval):
// Per-tick population growth = (capacity - population) * POPULATION_GROWTH_RATE * growthFactor
// Controls how responsive population is to available housing.
// At 0.05: slow crawl (~100+ ticks to fill 1% surplus) -> freeze-like appearance
// At 0.15: responsive growth (~30 ticks to fill 1% surplus) -> player-visible climbing
// Directional: larger housing surplus drives faster climb toward capacity.
export const POPULATION_GROWTH_RATE = 0.15;

const XP_LEVELS: number[] = (() => {
  const a = [0];
  let step = 50;
  for (let l = 2; l <= 20; l++) {
    a.push(a[a.length - 1] + step);
    step = Math.round(step * 1.5);
  }
  return a;
})();

export const levelOf = (xp: number) => {
  let lv = 1;
  for (let i = 1; i < XP_LEVELS.length; i++) if (xp >= XP_LEVELS[i]) lv = i + 1;
  return lv;
};

export const xpForLevel = (level: number) =>
  XP_LEVELS[Math.max(0, Math.min(level - 1, XP_LEVELS.length - 1))];

export interface ZoneDemand {
  residential: number;
  commercial: number;
  industrial: number;
}

const clampN = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

export function demandOf(s: SimState): ZoneDemand {
  const c = countByKind(s.buildings);
  const t = s.taxRates;
  const avgTax = (t.residential + t.commercial + t.industrial) / 3;
  const jobs = totalJobs(s);
  const workers = s.population * 0.55;
  const base = Math.max(Math.max(jobs, workers), 40);
  const res = ((jobs - workers) / base) * 140 - (avgTax - 10) * 4;
  const popFactor = Math.min(1, s.population / 40);
  const shopBase = Math.max(s.population * 0.22, 12);
  const com = popFactor * (((shopBase - c.commercial * 10) / shopBase) * 130 - (t.commercial - 11) * 3);
  const indBase = Math.max(s.population * 0.18, 9);
  const ind = popFactor * (((indBase - c.industrial * 7) / indBase) * 130 - (t.industrial - 13) * 3);
  return {
    residential: Math.round(clampN(res, -100, 100)),
    commercial: Math.round(clampN(com, -100, 100)),
    industrial: Math.round(clampN(ind, -100, 100)),
  };
}

function starterCity(): SimState['buildings'] {
  const out: SimState['buildings'] = [];
  let id = 1;
  const put = (spec: string, x: number, y: number) => out.push({ id: id++, spec, x, y });

  for (let x = 0; x < MAP_W; x++) {
    put('m20', x, 56);
    put('m20', x, 58);
  }
  for (let y = 57; y <= 63; y++) put('road', 150, y);

  const jx = 150;
  const jy = 67;
  const r = 4;
  const seen = new Set<string>();
  for (let a = 0; a < 360; a += 4) {
    const rad = (a * Math.PI) / 180;
    const x = Math.round(jx + r * Math.cos(rad));
    const y = Math.round(jy + r * Math.sin(rad));
    const k = `${x},${y}`;
    if (!seen.has(k)) {
      seen.add(k);
      put('road', x, y);
    }
  }

  for (let y = jy + r + 1; y <= 96; y++) put('road', 150, y);
  for (let x = 149; x >= 122; x--) put('road', x, 96);

  put('pylon', 164, 72);
  put('pylon', 171, 70);

  for (let x = 0; x < MAP_W; x++) put('rail', x, 84);
  for (let x = 0; x < MAP_W; x++) put('hs1', x, 205);
  put('station_sanderling', 136, 83);
  return out;
}

/**
 * Calculate the next safe building ID: max(existing building ids) + 1.
 * This ensures no collision between scenery, savepoint-restored buildings, and new placements.
 *
 * BUG-413 FIX: scenery starts at id=1 and can reach ~1900+ buildings,
 * but nextId was hardcoded to 100, causing collision. This function computes
 * the safe starting point for new gameplay buildings.
 */
export function nextSafeBuildingId(buildings: SimState['buildings']): number {
  if (buildings.length === 0) return 1;
  let maxId = 0;
  for (const b of buildings) {
    if (b.id > maxId) maxId = b.id;
  }
  return maxId + 1;
}

function rawState(): SimState {
  const buildings = starterCity();
  return {
    tick: 0,
    speed: 1,
    funds: 10000000,
    loanBalance: 0,
    population: 0,
    xp: 30,
    taxRates: { residential: 9, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: false },
    buildings,
    nextId: nextSafeBuildingId(buildings),
    movingId: null,
    tool: { mode: 'select' },
    clipboard: null,
    pipeTier: {},
    history: [],
    ledger: [],
    nextLedgerId: 1,
    lastFlows: { inflows: [], outflows: [] },
    fundsAtTickStart: 10000000,
    fundsAtTickEnd: 10000000,
    pendingRewards: [],
    // Start already "at" the seed level so the opening state grants no reward.
    lastRewardedLevel: levelOf(30),
    notice: null,
    unlockedAll: false,
    roadNotice: null,
    railNotice: null,
    placeNotice: null,
    roadMonitors: [],
    buildingMonitors: [],
  };
}

/**
 * God-mode "Unlock all" price (FEAT-1972079899). PLACEHOLDER under the balance-number
 * regime — a deliberately large cash gate pending Aaron's balance sign-off; not a
 * derived/tuned value. Charged once by the `unlockAll` action to flip s.unlockedAll.
 */
export const UNLOCK_ALL_COST = 5_000_000;

/**
 * Single-source catalogue unlock gate (FEAT-1972079899). A spec is available for
 * placement when the god-mode flag is set OR its unlock level has been reached.
 * Behaviour-preserving when unlockedAll is false: identical to `sp.unlock <= level`.
 */
export function specUnlocked(s: SimState, sp: Spec): boolean {
  return s.unlockedAll || sp.unlock <= levelOf(s.xp);
}

// UPKEEP_BUCKET moved to fiscal.ts (BUG-422 SSOT) so the consistency checker can
// recompute per-bucket upkeep under the same labels the engine records.

/**
 * Regional Grant — a fixed monthly regional stipend. PLACEHOLDER under the
 * balance-number regime (directional only, pending Aaron's row-by-row pass).
 */
export const REGIONAL_GRANT_PER_MONTH = 800;

/**
 * BUG-400 FIX — the Regional Grant is SMOOTHED across the month into a per-tick
 * inflow booked through computeFlows (see below), NOT a lump sum dropped on the
 * single tick the month rolls over. Two reasons:
 *   1. No 1000x spike — incomePerTick / margin / the fiscal trend read
 *      Σ(lastFlows.inflows); a monthly lump made income jump on one tick and
 *      distorted the per-tick view (Bro: +1.4M spike in a large economy). A
 *      smoothed inflow keeps the per-tick income flat.
 *   2. No side channel / no ledger eviction — the grant is now a normal named
 *      inflow visible to Flow / Earnings / history.income (so history reconciles
 *      with funds), and it no longer prepends a recurring "Regional Grant" row
 *      into the 200-cap ledger every 30 ticks, which used to evict real player
 *      events (build / loan / demolish).
 *
 * The floor-difference distribution pays an integer (26 or 27 for 800/30) each
 * tick that sums to EXACTLY REGIONAL_GRANT_PER_MONTH per 30-tick month
 * (telescoping over a full phase cycle), so funds stay integer and conservation
 * is exact. Tick-driven and deterministic (GR#21) — no Date/Math.random.
 */
export function regionalGrantPerTick(tick: number): number {
  const g = REGIONAL_GRANT_PER_MONTH;
  const tpm = TICKS_PER_MONTH;
  // Phase within the month, normalised so negative ticks are still 0..tpm-1.
  const phase = ((Math.trunc(tick) % tpm) + tpm) % tpm;
  return Math.floor((g * (phase + 1)) / tpm) - Math.floor((g * phase) / tpm);
}

export function computeFlows(s: SimState): { inflows: FlowItem[]; outflows: FlowItem[] } {
  const c = countByKind(s.buildings);
  const t = s.taxRates;
  const inflows: FlowItem[] = [
    { label: 'Council Tax', value: councilTaxPerTick(s.population, t.residential) },
    { label: 'Business Tax', value: businessTaxPerTick(c.commercial, t.commercial) },
    { label: 'Freight Tax', value: Math.round(c.industrial * t.industrial * 0.55) },
    // BUG-400: Regional Grant is a proper, smoothed per-tick inflow booked through
    // the SAME flows path as everything else (no side channel, no ledger eviction).
    { label: 'Regional Grant', value: regionalGrantPerTick(s.tick) },
  ];
  // BUG-404 FIX: removed the duplicate tourismDrive Tourism entry here.
  // All tourism income (both policy and building-sourced) is calculated and added
  // once below at the unified tourism calculation site (lines ~227-230).

  const links = stationLinks(s);
  let commuterWeight = 0;
  for (const b of s.buildings) {
    if (!links.connectedIds.has(b.id)) continue;
    const sp = SPECS[b.spec];
    if (sp?.kind !== 'station') continue;
    commuterWeight += sp.id === 'station_ashford' ? 3 : 1;
  }
  if (commuterWeight > 0) {
    inflows.push({
      label: 'Commuter Revenue',
      value: Math.round(s.population * 0.08 * Math.min(commuterWeight, 6)),
    });
  }

  const c2 = countByKind(s.buildings);
  const officeJobs = totalJobs(s) - c2.commercial * 12 - c2.industrial * 18;
  const officeTax = Math.max(0, officeJobs) * t.commercial * 0.05;
  if (officeTax > 0) inflows.push({ label: 'Office Tax', value: Math.round(officeTax) });

  // BUG-404 FIX: unified tourism calculation (SSOT style per GR#3).
  // tourismDrive policy and building tourism both feed into ONE stream.
  // Before: tourismDrive added a separate entry at line 202, then buildings
  // added another "Tourism" entry at line 229, creating duplicates.
  // Now: single source of truth for tourism income.
  let tourism = s.policies.tourismDrive ? Math.round(s.population * 0.12) : 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.tourism) tourism += sp.tourism * Math.min(1, s.population / 300);
  }
  if (tourism > 0) inflows.push({ label: 'Tourism', value: Math.round(tourism) });

  // MOD-049 inc1: Grid Export revenue (power surplus sold to regional grid).
  // exportMW = max(0, capMW - needMW); exportRevenue = exportMW * tariff.
  // Show "Grid Export" line only when revenue > 0 (per brief requirements).
  const pw = powerStats(s);
  const gridExportRevenue = gridExportRevenuePerTick(pw.cap, pw.need, GRID_EXPORT_TARIFF_PER_MW);
  if (gridExportRevenue > 0) {
    inflows.push({ label: 'Grid Export', value: gridExportRevenue });
  }

  const harbourBoost = s.buildings.some((b) => b.spec === 'land_harbour') ? 1.4 : 1;

  const buckets: Record<string, number> = {};
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (!sp || !sp.upkeep) continue;
    const k = UPKEEP_BUCKET[sp.kind];
    if (k) buckets[k] = (buckets[k] ?? 0) + sp.upkeep;
  }
  let outflows: FlowItem[] = Object.entries(buckets)
    .filter(([, v]) => v > 0)
    .map(([label, value]) => ({ label, value }));
  outflows.push({ label: 'Wages', value: wagesPerTick(s.population) });

  const c3 = countByKind(s.buildings);
  const freightIdx = inflows.findIndex((f) => f.label === 'Freight Tax');
  if (freightIdx >= 0) {
    inflows[freightIdx] = {
      label: 'Freight Tax',
      value: Math.round(
        (c3.industrial * t.industrial * 0.55 + c3.mine * t.industrial * 0.9) * harbourBoost
      ),
    };
  }
  // BROWNOUT consequence (BUG-393): while power need exceeds capacity,
  // powered businesses under-produce — commercial/industrial/office income
  // scales down by brownout.incomeFactor. Applied AFTER the harbour-boosted
  // Freight Tax overwrite above so the penalty cannot be clobbered.
  // Deterministic; weight BROWNOUT_INCOME_K is PLACEHOLDER (balance regime).
  const brownout = brownoutOf(s);
  if (brownout.active) {
    const poweredIncome = new Set(['Business Tax', 'Freight Tax', 'Office Tax']);
    for (const fl of inflows) {
      if (poweredIncome.has(fl.label)) fl.value = Math.round(fl.value * brownout.incomeFactor);
    }
  }

  // BUG-403 FIX: Transit Subsidy scaled/capped to avoid unbounded costs.
  // PLACEHOLDER (balance-number regime): scale by tax income instead of population only.
  // Base tax income = sum of tax flows (council, business, freight).
  const baseTaxIncome = inflows
    .filter((f) => ['Council Tax', 'Business Tax', 'Freight Tax'].includes(f.label))
    .reduce((a, f) => a + f.value, 0);
  // PLACEHOLDER: cap transit subsidy at a fraction (50%) of base tax income.
  const maxTransitSubsidy = Math.round(baseTaxIncome * 0.5);
  const transitSubsidyCost = Math.round(s.population * 1.5);
  if (s.policies.transitSubsidy)
    outflows.push({ label: 'Transit Subsidy', value: Math.min(transitSubsidyCost, maxTransitSubsidy) });

  if (s.loanBalance > 0)
    outflows.push({ label: 'Loan Interest', value: Math.round(s.loanBalance * 0.005) });

  // FEAT-1972079906 inc1: refuse COLLECTION OPEX — the rounds cost ∝ tonnage
  // actually collected (collectionOpexOf, tonnes collected × rate). Only emitted
  // when > 0 (no depots / no waste ⇒ nothing collected ⇒ no line), so a city with
  // no refuse infrastructure sees no new outflow. Routed through the flows so it is
  // conservation-safe; excluded from the consistency upkeep reconciliation (it is a
  // tonnage-based operating cost, not a per-building `upkeep` bucket — see
  // consistency.ts, alongside Wages / Road Auto-Scale). Depot FIXED upkeep still
  // flows normally via the Water & Waste bucket.
  const refuseOpex = collectionOpexOf(s);
  if (refuseOpex > 0) outflows.push({ label: 'Refuse Collection', value: refuseOpex });

  // FEAT-1972079906 inc2: refuse PROCESSING economics (all conservation-safe via the
  // flows). Landfill tipping is a tonnage-based OUTFLOW; MRF material + compost are
  // tonnage-based INFLOWS. Each only emitted when > 0 (a city with no processing sees
  // no new lines). EfW power is NOT booked here — it feeds powerStats.cap and is
  // monetised through the existing Grid Export inflow when there is a surplus, so
  // booking it again would double-count. "Waste Disposal" is a tonnage-based operating
  // cost, NOT a per-building `upkeep` bucket, so it is excluded from the consistency
  // upkeep reconciliation alongside Wages / Refuse Collection (see consistency.ts).
  const recyclingRevenue = recyclingRevenueOf(s);
  if (recyclingRevenue > 0) inflows.push({ label: 'Recycling Revenue', value: recyclingRevenue });
  const compostRevenue = compostRevenueOf(s);
  if (compostRevenue > 0) inflows.push({ label: 'Compost Revenue', value: compostRevenue });
  const landfillTipping = landfillTippingOf(s);
  if (landfillTipping > 0) outflows.push({ label: 'Waste Disposal', value: landfillTipping });

  if (s.funds < 0) {
    const other = outflows.reduce((a, o) => a + o.value, 0);
    const overdraftInterest = overdraftInterestPerTick(s.funds, other);
    if (overdraftInterest > 0) outflows.push({ label: 'Overdraft Interest', value: overdraftInterest });
  }

  // BUG-422 (GR#3): post-policy outflow multipliers now live in the shared
  // applyOutflowPolicies helper (fiscal.ts) so the consistency checker applies the
  // IDENTICAL recycling(0.93 on discounted labels) + austerity(0.9 all) pipeline,
  // in the same order with the same rounding, to its recomputed outflows.
  outflows = applyOutflowPolicies(outflows, s.policies);
  return { inflows, outflows };
}

/**
 * Milestone rewards (FEAT-1972079884). If experience has crossed one or more new
 * levels since the last reward, grant the cash injection + build the level-up
 * notice EXACTLY ONCE per level. Idempotent: a no-op unless levelOf(xp) has
 * advanced past lastRewardedLevel, so it is safe to call after any xp change.
 */
/**
 * SINGLE SOURCE OF TRUTH: Compute level-up rewards (one per level crossed).
 * For queuing: multiple calls don't stack; each call drains the old queue and computes fresh.
 * Called exactly once per grant action (tick-time cross, debugXp, place).
 *
 * PLACEHOLDER: Reward base is PRE-TICK (s.funds before flows), consistent with how
 * citizens earned XP during that tick. This ensures the reward % reflects the fund state
 * the citizen earned from, not post-expense states.
 *
 * Returns array of results (one per level crossed), or empty array if no crossing.
 * Each result includes the per-level cash, notice, and the accumulated newLevel.
 */
export interface LevelRewardResult {
  totalReward: number;
  newLevel: number;
  notice: LevelUpNotice;
}

export function computeLevelRewards(s: SimState): LevelRewardResult[] {
  const lv = levelOf(s.xp);
  if (lv <= s.lastRewardedLevel) return [];
  const results: LevelRewardResult[] = [];
  let funds = s.funds;
  for (let L = s.lastRewardedLevel + 1; L <= lv; L++) {
    const cash = Math.max(0, Math.round(funds * LEVEL_REWARD_RATE));
    funds += cash;
    const notice = { level: L, cash, unlocked: unlockedAtLevel(L) };
    results.push({
      totalReward: cash, // Per-level cash, not cumulative
      newLevel: L,
      notice,
    });
  }
  return results;
}

/**
 * Drain and apply pending rewards, updating funds and lastRewardedLevel atomically.
 * Does NOT recompute; takes results verbatim from computeLevelRewards().
 * Called by advance() to apply queued rewards through flows.
 */
export function grantLevelRewards(s: SimState): SimState {
  if (s.pendingRewards.length === 0) return s;
  let funds = s.funds;
  let lastRewardedLevel = s.lastRewardedLevel;
  let notice: LevelUpNotice | null = s.notice;
  for (const lr of s.pendingRewards) {
    funds += lr.totalReward;
    lastRewardedLevel = lr.newLevel;
    notice = lr.notice;
  }
  return { ...s, funds, lastRewardedLevel, notice };
}

// ════════════════════════════════════════════════════════════════════════════
// FEAT-1972079907 inc2 — ONE-YEAR TRAFFIC MONITORING + AUTO-SCALE.
// ════════════════════════════════════════════════════════════════════════════

/**
 * Clock windows for road monitoring. Mirrors the calendar SSOT in utils.gameDate
 * (30-day months, 12 months → a 360-tick year), so a monitored segment is watched
 * for exactly one in-game YEAR of ticks. Tick-driven — NEVER wall-clock (GR#21).
 */
export const TICKS_PER_MONTH = 30;
export const TICKS_PER_YEAR = TICKS_PER_MONTH * 12; // 360

/**
 * Saturation threshold: a monitored segment auto-scales ONE tier when its coarse
 * traffic load reaches this fraction of the tile's current-tier capacity
 * (ROAD_TIER_CAPACITY). ⚠ PLACEHOLDER-balance — directional only, Aaron's pass.
 */
export const ROAD_SATURATION_THRESHOLD = 0.85;

/**
 * FEAT-1972079878 inc1 — Building auto-scale utilization threshold. A monitored
 * building scales when utilization (residents/capacity or jobs/capacity) reaches
 * this fraction. ⚠ PLACEHOLDER-balance — directional only, Aaron's pass.
 */
export const BUILDING_UTILIZATION_THRESHOLD = 0.85;

/**
 * FEAT-1972079878 inc1 — Cost multiplier for building capacity tier upgrade.
 * Delta-cost per tier = originalPlacementCost × this fraction. ⚠ PLACEHOLDER-balance.
 */
export const BUILDING_AUTO_SCALE_COST_FRACTION = 0.15;

/**
 * Coarse per-segment traffic-load weights (⚠ PLACEHOLDER-balance). A monitored
 * segment's demand is the FEEDING building's population + jobs + freight, each
 * weighted, then ramped by city activity (population vs ROAD_TRAFFIC_ACTIVITY_REF).
 * This reuses the per-building commuter-weight idiom from stationLinks / the
 * Commuter Revenue term in computeFlows — a categorical weight per building, not a
 * vehicle sim (brief §4: "a demand-vs-capacity scalar"). `residents` is the
 * per-building population proxy (true per-building occupancy isn't tracked).
 */
export const ROAD_TRAFFIC_POP_WEIGHT = 1; // per resident of the feeding building
export const ROAD_TRAFFIC_JOB_WEIGHT = 1; // per job
export const ROAD_TRAFFIC_FREIGHT_WEIGHT = 1; // extra per industrial/mine job (freight)
/** City population at which traffic activity ramps to 1.0. ⚠ PLACEHOLDER-balance. */
export const ROAD_TRAFFIC_ACTIVITY_REF = 500;

/** Per-building traffic contribution — population + jobs + freight. Pure.
 *  Exported (read-only) for FEAT-1972079902 rail-inc1 line-usage reuse (GR#3 SSOT):
 *  data.ts's lineUsageOf carries road/m20 flow through THIS exact weight, so the
 *  rail panel's road numbers can never disagree with road inc2's monitor load. */
export function feederTrafficWeight(sp: Spec): number {
  const pop = sp.residents ?? 0;
  const jobs = sp.jobs ?? 0;
  const freight = sp.kind === 'industrial' || sp.kind === 'mine' ? jobs : 0;
  return (
    pop * ROAD_TRAFFIC_POP_WEIGHT +
    jobs * ROAD_TRAFFIC_JOB_WEIGHT +
    freight * ROAD_TRAFFIC_FREIGHT_WEIGHT
  );
}

/**
 * City-wide traffic activity 0..1 — ramps as the city fills toward
 * ROAD_TRAFFIC_ACTIVITY_REF. Pure function of s.population, so a segment's load
 * grows over its monitoring year exactly as the city grows. Deterministic.
 * Exported (read-only) for FEAT-1972079902 rail-inc1 line-usage reuse. */
export function trafficActivity(s: SimState): number {
  if (s.population <= 0) return 0;
  return Math.min(1, s.population / ROAD_TRAFFIC_ACTIVITY_REF);
}

export interface RoadScaleResult {
  /** buildings with any saturated monitored road tile bumped one tier (spec swap). */
  buildings: Building[];
  /** monitors still inside their window (expired ones dropped). */
  monitors: RoadMonitor[];
  /** total upgrade spend to charge through the ledger this pass. */
  cost: number;
  /** number of segments scaled this pass. */
  upgraded: number;
}

/**
 * Evaluate the road monitors at a monthly aggregate boundary (pure + deterministic).
 *  1. Drop monitors whose window has closed (tick past `until`).
 *  2. Process the survivors in strict (x,y) order (NEVER map-iteration order).
 *  3. For each: read the CURRENT road tile there and its feeding source building;
 *     the coarse load = feederTrafficWeight(source) × city activity.
 *  4. If load ≥ threshold × tier-capacity and the tile is below the ladder max,
 *     upgrade it ONE tier (brief §6 Q4: one tier per saturation event), booking the
 *     placement-cost DELTA (mirrors inc1's upgrade-on-connect charge).
 * Each tile scales at most once per pass; the cost is charged through the tick's
 * flows so conservation holds and replay reproduces it.
 */
export function evaluateRoadMonitors(s: SimState, tick: number): RoadScaleResult {
  // (1) expire — keep only monitors still inside their year-long window.
  // Tolerate a state that predates the field (older snapshots / bespoke test states).
  const active = (s.roadMonitors ?? []).filter((m) => tick <= m.until);
  // (2) strict (x,y) order — deterministic, order-independent upgrades.
  active.sort((a, b) => a.x - b.x || a.y - b.y);

  const byId = new Map<number, Building>();
  const roadAt = new Map<string, Building>();
  for (const b of s.buildings) {
    byId.set(b.id, b);
    const sp = SPECS[b.spec];
    if (sp && isRoadSpec(sp)) roadAt.set(`${b.x},${b.y}`, b);
  }
  const activity = trafficActivity(s);

  const upgradeSpecById = new Map<number, string>();
  let cost = 0;
  let upgraded = 0;
  for (const m of active) {
    const road = roadAt.get(`${m.x},${m.y}`);
    if (!road) continue; // tile is no longer a road (bulldozed) — nothing to scale.
    if (upgradeSpecById.has(road.id)) continue; // already scaled this pass.
    const curSpec = SPECS[road.spec];
    const tier = roadTierOf(curSpec);
    if (tier < 1 || tier >= 5) continue; // not a ladder road, or already at the max.
    const src = byId.get(m.source);
    const srcSpec = src ? SPECS[src.spec] : undefined;
    const load = srcSpec ? feederTrafficWeight(srcSpec) * activity : 0;
    const capacity = ROAD_TIER_CAPACITY[tier as RoadTier];
    if (load < ROAD_SATURATION_THRESHOLD * capacity) continue; // below saturation.
    const nextSpecId = ROAD_TIER_SPECS[(tier + 1) as RoadTier];
    const nextSpec = SPECS[nextSpecId];
    upgradeSpecById.set(road.id, nextSpecId);
    cost += Math.max(0, placementCost(nextSpec) - placementCost(curSpec));
    upgraded++;
  }

  const buildings =
    upgradeSpecById.size === 0
      ? s.buildings
      : s.buildings.map((b) =>
          upgradeSpecById.has(b.id) ? { ...b, spec: upgradeSpecById.get(b.id)! } : b
        );

  return { buildings, monitors: active, cost, upgraded };
}

interface BuildingScaleResult {
  /** buildings with upgraded capacityTiers */
  buildings: Building[];
  /** monitors still inside their window (expired ones dropped) */
  monitors: BuildingMonitor[];
  /** total upgrade cost charged this pass */
  cost: number;
  /** number of buildings scaled this pass */
  upgraded: number;
}

/**
 * FEAT-1972079878 inc1 (AC-7): Evaluate building monitors at a monthly boundary (pure + deterministic).
 * 1. Drop monitors whose window has closed (tick past `until`).
 * 2. Process survivors in strict buildingId order (NEVER map-iteration order).
 * 3. For each: if building is online AND utilization ≥ 0.85 threshold, upgrade
 *    capacityTier by 1, booking delta-cost (BUILDING_AUTO_SCALE_COST_FRACTION × base cost).
 * Mirrors evaluateRoadMonitors pattern; each building scales at most once per pass.
 */
export function evaluateBuildingMonitors(s: SimState, tick: number): BuildingScaleResult {
  // (1) expire — keep only monitors still inside their 1-year window
  const active = (s.buildingMonitors ?? []).filter((m) => tick <= m.until);
  // (2) strict buildingId order — deterministic, order-independent upgrades
  active.sort((a, b) => a.buildingId - b.buildingId);

  const byId = new Map<number, Building>();
  for (const b of s.buildings) {
    byId.set(b.id, b);
  }

  const tierUpgradeById = new Map<number, number>();
  let cost = 0;
  let upgraded = 0;

  for (const m of active) {
    const building = byId.get(m.buildingId);
    if (!building) continue; // building bulldozed — skip

    // Skip offline buildings (AC-11). Uses the SAME (pre-increment) `s` that every
    // other isOnline() call site in advance() uses this tick (computeFlows, the
    // population-growth capacity target below) — keeping the online view
    // consistent within one tick, matching the road-monitor pattern.
    if (!isOnline(s, building)) continue;

    if (tierUpgradeById.has(building.id)) continue; // already scaled this pass

    const sp = SPECS[building.spec];
    if (!sp || !sp.capacityTiers) continue; // spec missing or not scalable

    const currentTier = building.capacityTier ?? 0;
    if (currentTier >= sp.capacityTiers.length - 1) continue; // already at max tier

    // Compute utilization based on monitor type (residents or jobs)
    let utilization = 0;
    if (m.type === 'residents') {
      const totalCap = residentsCapacity(s);
      utilization = totalCap > 0 ? Math.min(1, s.population / totalCap) : 0;
    } else {
      // jobs type
      const totalCap = totalJobs(s);
      utilization = totalCap > 0 ? Math.min(1, s.population / totalCap) : 0; // jobs utilization ~ population-based proxy
    }

    if (utilization < BUILDING_UTILIZATION_THRESHOLD) continue; // below threshold

    // Upgrade tier
    tierUpgradeById.set(building.id, currentTier + 1);
    // AC-10 (acceptance doc worked example): "Place res_estate (placement cost
    // ~45k); trigger auto-scale -> cost ~6.75k charged" — 45k is sp.cost, the
    // catalogue cost, NOT placementCost(sp) (which is £0 for every zone-category
    // estate). Upgrading a capacity tier is genuine construction spend even
    // though the ORIGINAL zoning was free, so the upgrade fraction is charged
    // against the raw catalogue cost, not the zoning-discounted placement cost.
    const upgradeCost = Math.round(sp.cost * BUILDING_AUTO_SCALE_COST_FRACTION);
    cost += upgradeCost;
    upgraded++;
  }

  const buildings =
    tierUpgradeById.size === 0
      ? s.buildings
      : s.buildings.map((b) =>
          tierUpgradeById.has(b.id) ? { ...b, capacityTier: tierUpgradeById.get(b.id)! } : b
        );

  return { buildings, monitors: active, cost, upgraded };
}

function advance(s: SimState): SimState {
  // TICK-BOUNDARY INVARIANT (Round-6): Record funds at tick start for conservation checking.
  const fundsAtTickStart = s.funds;
  const tick = s.tick + 1;

  // FEAT-1972079907 inc2: MONTHLY road traffic monitoring + auto-scale. Runs on the
  // existing monthly aggregate boundary (no new per-tick hot path). This happens
  // FIRST — before flows/population — so the REST of the tick (upkeep, commuter
  // links, everything) is computed against the upgraded roads. That keeps the tick
  // internally consistent: the consistency checker recomputes upkeep from the FINAL
  // buildings, so recording flows on the same (post-upgrade) buildings makes them
  // agree. The one-off capital cost is charged as a "Road Auto-Scale" outflow below,
  // so conservation holds and genesis-replay reproduces the whole thing.
  let scaledBuildings = s.buildings;
  let roadMonitors = s.roadMonitors;
  let buildingMonitors = s.buildingMonitors;
  let autoScaleCost = 0;
  let autoScaleCount = 0;
  let buildingAutoScaleCost = 0;
  let buildingAutoScaleCount = 0;
  if (tick % TICKS_PER_MONTH === 0) {
    const roadScale = evaluateRoadMonitors(s, tick);
    scaledBuildings = roadScale.buildings;
    roadMonitors = roadScale.monitors;
    autoScaleCost = roadScale.cost;
    autoScaleCount = roadScale.upgraded;

    // FEAT-1972079878 inc1: MONTHLY building demand monitoring + auto-scale.
    // Run AFTER road scale so buildings read upgraded road network. BUG: this
    // MUST evaluate against `scaledBuildings` (the road-scale RESULT), not the
    // original `s` — evaluateBuildingMonitors returns `s.buildings` UNCHANGED
    // whenever no building itself scales that pass, and blindly assigning that
    // back into `scaledBuildings` silently REVERTED every road auto-scale tier
    // upgrade on any tick where no building happened to scale (the road-inc2
    // regression: 'road' never became 'rd_avenue').
    const buildingScale = evaluateBuildingMonitors({ ...s, buildings: scaledBuildings }, tick);
    scaledBuildings = buildingScale.buildings;
    buildingMonitors = buildingScale.monitors;
    buildingAutoScaleCost = buildingScale.cost;
    buildingAutoScaleCount = buildingScale.upgraded;

    // Re-bind s to the post-upgrade buildings AND the filtered monitor list for the
    // remainder of the tick. BUG-440: the monitors must ride along too — the sweep
    // branch below re-reads s.roadMonitors, and without this rebind it clobbers the
    // eval's expiry-dropped list, resurrecting expired monitors on 60-tick boundaries.
    if (scaledBuildings !== s.buildings || roadMonitors !== s.roadMonitors || buildingMonitors !== s.buildingMonitors)
      s = { ...s, buildings: scaledBuildings, roadMonitors, buildingMonitors };
  }

  let orphanConnectCost = 0;
  if (tick % (2 * TICKS_PER_MONTH) === 0) {
    const beforeFunds = s.funds;
    s = sweepOrphanConnects(s);
    orphanConnectCost = beforeFunds - s.funds;
    s = { ...s, funds: beforeFunds };
    scaledBuildings = s.buildings;
    roadMonitors = s.roadMonitors;
  }

  // FEAT-1972079891 inc1 (AC-1/AC-12): recompute the connected road network at the
  // START of the tick, AFTER any monthly road auto-scale, so every gate check this
  // tick (computeFlows → isOnline, capacity, consistency) reads the SAME frame's
  // connectivity graph. Pure/deterministic; no Date/random.
  s = { ...s, roadConnectivity: computeRoadConnectivity(s) };

  let { inflows, outflows } = computeFlows(s);

  // BUG-400: the Regional Grant is no longer injected here as a monthly lump.
  // computeFlows() now books it as a SMOOTHED per-tick inflow (regionalGrantPerTick),
  // so it is already present in `inflows`, is visible to Flow/Earnings/history.income,
  // reconciles with funds, and does not spike incomePerTick on the month boundary.
  // inc2: the auto-scale capital spend is an outflow so it counts for conservation.
  if (autoScaleCost > 0) {
    outflows = [...outflows, { label: 'Road Auto-Scale', value: autoScaleCost }];
  }
  if (buildingAutoScaleCost > 0) {
    outflows = [...outflows, { label: 'Building Auto-Scale', value: buildingAutoScaleCost }];
  }
  if (orphanConnectCost > 0) {
    outflows = [...outflows, { label: 'Road Auto-Connect', value: orphanConnectCost }];
  }

  // Drain pending rewards queue (from debugXp and place actions).
  // Each applies through flows so it's visible in fiscal panel and counts for conservation.
  let nextNotice = s.notice;
  for (const pr of s.pendingRewards) {
    inflows = [...inflows, { label: 'Level Rewards', value: pr.totalReward }];
    nextNotice = pr.notice; // Last notice wins (multiple crossings rare but possible)
  }

  const income = inflows.reduce((a, b) => a + b.value, 0);
  const expense = outflows.reduce((a, b) => a + b.value, 0);
  let funds = s.funds + income - expense;
  let ledger: LedgerEntry[] = s.ledger;
  let nextLedger = s.nextLedgerId;

  // BUG-400: the recurring monthly "Regional Grant" ledger row is GONE. It used to
  // prepend a +800 event every 30 ticks into the 200-cap ledger, which over time
  // evicted every real player event (build/loan/demolish). The grant is now fully
  // visible in the flows/income views, so no per-tick event-log entry is needed and
  // real events survive across arbitrarily many grant cycles.
  // Ledger entry for any auto-scale spend (mirrors the "Road Auto-Scale" outflow).
  if (autoScaleCost > 0) {
    ledger = [
      { id: nextLedger++, tick, label: `Auto-scaled ${autoScaleCount} road segment(s)`, amount: -autoScaleCost },
      ...ledger,
    ].slice(0, LEDGER_CAP);
  }
  // FEAT-1972079878 inc1: Ledger entry for building auto-scale spend.
  if (buildingAutoScaleCost > 0) {
    ledger = [
      { id: nextLedger++, tick, label: `Auto-scaled ${buildingAutoScaleCount} building(s)`, amount: -buildingAutoScaleCost },
      ...ledger,
    ].slice(0, LEDGER_CAP);
  }

  const capacity = (() => {
    let cap = 0;
    for (const b of s.buildings) {
      if (!isOnline(s, b)) continue;
      const sp = SPECS[b.spec];
      if (sp?.kind === 'residential') cap += sp.residents ?? 8;
    }
    return cap;
  })();
  const t = s.taxRates;
  const avgTax = (t.residential + t.commercial + t.industrial) / 3;
  const demand = demandOf(s);
  const links = stationLinks(s);
  let stationWeight = 0;
  for (const b of s.buildings) {
    if (!links.connectedIds.has(b.id)) continue;
    const sp = SPECS[b.spec];
    if (sp?.kind === 'station') stationWeight += sp.id === 'station_ashford' ? 3 : 1;
  }

  let population = s.population;
  if (capacity > population) {
    const growthFactor =
      (1.4 - avgTax / 15) * (s.policies.transitSubsidy ? 1.25 : 1) *
      Math.max(0.3, 0.55 + demand.residential / 200) *
      (1 + 0.15 * Math.min(stationWeight, 6));
    // BUG-394 FIX: use POPULATION_GROWTH_RATE instead of hardcoded 0.05
    population += Math.max(0, Math.ceil((capacity - population) * POPULATION_GROWTH_RATE * growthFactor));
  } else if (population > capacity) {
    population = Math.max(capacity, population - Math.ceil((population - capacity) * 0.1));
  }

  // Compute in-tick level rewards (if any) EXACTLY ONCE to add to flows for conservation tracking.
  // Increment XP first so computeLevelRewards can check the new level.
  // computeLevelRewards now returns array (one per level crossed, or empty).
  const newXp = s.xp + 1;
  const tempState = { ...s, xp: newXp };
  const inTickRewards = computeLevelRewards(tempState);
  for (const lr of inTickRewards) {
    // Record each per-level reward as a separate inflow for fiscal panel visibility.
    inflows = [...inflows, { label: 'Level Rewards', value: lr.totalReward }];
    funds += lr.totalReward; // CRITICAL: Apply reward to funds (BUG-406 R7 fix)
    nextNotice = lr.notice; // Latest level's notice (usually just one in-tick)
  }

  // Update lastRewardedLevel for all rewards (both pending and in-tick).
  let lastRewardedLevel = s.lastRewardedLevel;
  for (const pr of s.pendingRewards) {
    lastRewardedLevel = pr.newLevel;
  }
  for (const lr of inTickRewards) {
    lastRewardedLevel = lr.newLevel;
  }

  // TICK-BOUNDARY INVARIANT: Record funds at tick end for conservation checking.
  const fundsAtTickEnd = funds;

  return {
    ...s,
    tick,
    funds,
    fundsAtTickStart,
    fundsAtTickEnd,
    pendingRewards: [], // Drained
    population,
    xp: newXp,
    // FEAT-1972079907 inc2: buildings may carry monthly auto-scale tier bumps;
    // roadMonitors has expired entries dropped (both == the inputs off a non-monthly tick).
    buildings: scaledBuildings,
    roadMonitors,
    history: [...s.history, { tick, funds, income, expense, population }].slice(-HISTORY_CAP),
    ledger,
    nextLedgerId: nextLedger,
    // BUG-419: record the START-of-tick population that computeFlows() charged
    // population-scaled flows on (s.population, before the growth update above), so
    // consistency checks recompute Wages/Council Tax against the SAME basis the engine
    // used — not the grown end-of-tick population.
    lastFlows: { inflows, outflows, population: s.population },
    lastRewardedLevel,
    notice: nextNotice,
  };
}

/**
 * Kinds that are themselves network infrastructure and never auto-connect (a road
 * does not wire itself to a road; rail/stations/pylons are not road-served here).
 */
export const CONNECT_EXEMPT_KINDS = new Set<Spec['kind']>([
  'road',
  'motorway',
  'rail',
  'station',
  'pylon',
]);

/**
 * FEAT-1972079907 inc1 — AUTO-CONNECT + UPGRADE-ON-CONNECT.
 *
 * After a real building is placed, wire it to the road network:
 *  1. If it already touches a road → no-op (clear notice).
 *  2. Else route a connector of fittingTier(sp) from the building to the NEAREST
 *     existing road via the deterministic grid router (roadConnect.planConnector),
 *     laying each connector cell as a REAL road building (journaled through replay,
 *     conservation-safe: cost charged through the ledger).
 *  3. Upgrade-on-connect: any joined road tile whose tier < the connector's tier is
 *     upgraded (spec swapped) to the connector spec at the junction.
 *  4. If no route within budget (or the connector is unaffordable) → surface a
 *     "no road access" notice; the building STAYS placed (inc1: no relocation).
 *
 * Pure + deterministic (GR#21): all tie-breaking flows from the router; no Date/random.
 * Called with `s` = state AFTER the player's building was inserted.
 */
function autoConnect(
  s: SimState,
  placed: Building,
  sp: Spec,
  opts?: { notice?: boolean; onUnaffordable?: () => void },
): SimState {
  const notice = opts?.notice !== false;
  if (CONNECT_EXEMPT_KINDS.has(sp.kind) || isRoadSpec(sp)) {
    return notice ? { ...s, roadNotice: null } : s;
  }

  // Board sets from the CURRENT buildings (includes the just-placed building).
  const occupied = new Set<string>();
  const roads = new Set<string>();
  for (const b of s.buildings) {
    const bs = SPECS[b.spec];
    if (!bs) continue;
    const road = isRoadSpec(bs);
    for (let dx = 0; dx < bs.w; dx++)
      for (let dy = 0; dy < bs.h; dy++) {
        const k = `${b.x + dx},${b.y + dy}`;
        occupied.add(k);
        if (road) roads.add(k);
      }
  }

  const plan = planConnector({
    occupied,
    roads,
    bx: placed.x,
    by: placed.y,
    bw: sp.w,
    bh: sp.h,
    mapW: MAP_W,
    mapH: MAP_H,
  });

  if (plan.connected) return notice ? { ...s, roadNotice: null } : s;
  if (plan.blocked) return notice ? { ...s, roadNotice: 'no road access' } : s;

  const tier = fittingTier(sp);
  const connSpecId = ROAD_TIER_SPECS[tier];
  const connSpec = SPECS[connSpecId];
  // Connectors honour the SSOT insertion guard exactly like a manual place.
  if (!canEnterSim(connSpec)) return notice ? { ...s, roadNotice: 'no road access' } : s;

  const tileCost = placementCost(connSpec);
  const connectorCost = tileCost * plan.path.length;

  // Upgrade-on-connect: junction road tiles below the connector tier jump up.
  const junctionKeys = new Set(plan.junctions.map((p) => `${p.x},${p.y}`));
  const upgradeIds: number[] = [];
  let upgradeCost = 0;
  for (const b of s.buildings) {
    const bs = SPECS[b.spec];
    if (!bs || !isRoadSpec(bs) || roadTierOf(bs) >= tier) continue;
    let hit = false;
    for (let dx = 0; dx < bs.w && !hit; dx++)
      for (let dy = 0; dy < bs.h && !hit; dy++)
        if (junctionKeys.has(`${b.x + dx},${b.y + dy}`)) hit = true;
    if (hit) {
      upgradeIds.push(b.id);
      upgradeCost += Math.max(0, tileCost - placementCost(bs));
    }
  }

  const totalCost = connectorCost + upgradeCost;
  // Never fail the placement (inc1): if the connector is unaffordable, keep the
  // building and surface the notice instead of laying a partial network.
  if (s.funds < totalCost) {
    opts?.onUnaffordable?.();
    return notice ? { ...s, roadNotice: 'no road access' } : s;
  }

  // Lay connector tiles as real road buildings.
  let nextId = s.nextId;
  const newTiles: Building[] = plan.path.map((p) => ({
    id: nextId++,
    spec: connSpecId,
    x: p.x,
    y: p.y,
    builtTick: s.tick,
  }));

  const upgradeSet = new Set(upgradeIds);
  const buildings = s.buildings
    .map((b) => (upgradeSet.has(b.id) ? { ...b, spec: connSpecId } : b))
    .concat(newTiles);

  // Ledger: connector spend, then any upgrade spend (mirrors how `place` logs).
  let ledger = s.ledger;
  let nextLedgerId = s.nextLedgerId;
  if (plan.path.length > 0) {
    ledger = [
      { id: nextLedgerId++, tick: s.tick, label: `Connector ${connSpec.name} (${plan.path.length})`, amount: -connectorCost },
      ...ledger,
    ].slice(0, LEDGER_CAP);
  }
  if (upgradeIds.length > 0) {
    ledger = [
      { id: nextLedgerId++, tick: s.tick, label: `Upgraded ${upgradeIds.length} road tile(s) to ${connSpec.name}`, amount: -upgradeCost },
      ...ledger,
    ].slice(0, LEDGER_CAP);
  }

  // FEAT-1972079907 inc2: register the connector tiles + the joined road tile(s)
  // for one-year traffic monitoring. `placed` is the feeding source; the whole
  // connector chain bears that building's demand, so both the connector and the
  // road it joined scale together as traffic grows. Dedup by (x,y) — a re-connect
  // over the same tile refreshes (extends) the window. Deterministic (x,y) order.
  const until = s.tick + TICKS_PER_YEAR;
  const monitorByCell = new Map<string, RoadMonitor>();
  for (const m of s.roadMonitors ?? []) monitorByCell.set(`${m.x},${m.y}`, m);
  const registerMonitor = (x: number, y: number) => {
    const k = `${x},${y}`;
    const existing = monitorByCell.get(k);
    monitorByCell.set(k, {
      x,
      y,
      source: placed.id,
      until: existing ? Math.max(existing.until, until) : until,
    });
  };
  for (const p of plan.path) registerMonitor(p.x, p.y);
  for (const p of plan.junctions) registerMonitor(p.x, p.y);
  const roadMonitors = Array.from(monitorByCell.values()).sort(
    (a, b) => a.x - b.x || a.y - b.y
  );

  return {
    ...s,
    funds: s.funds - totalCost,
    nextId,
    buildings,
    ledger,
    nextLedgerId,
    roadNotice: null,
    roadMonitors,
  };
}

function sweepOrphanConnects(s: SimState): SimState {
  const ids = s.buildings.map((b) => b.id).sort((a, b) => a - b);
  for (const id of ids) {
    const placed = s.buildings.find((b) => b.id === id);
    if (!placed) continue;
    const sp = SPECS[placed.spec];
    if (!sp) continue;
    let unaffordable = false;
    const next = autoConnect(s, placed, sp, {
      notice: false,
      onUnaffordable: () => {
        unaffordable = true;
      },
    });
    if (unaffordable) break;
    s = next;
  }
  return s;
}

// ════════════════════════════════════════════════════════════════════════════
// FEAT-1972079902 inc3 — AUTO-BRANCH-LINING ON GATEWAY PLACEMENT.
// ════════════════════════════════════════════════════════════════════════════

/**
 * Gateway specs that trigger auto-branch-lining (brief §3.4, resolved default (b)):
 * Ashford International (the HS1 station) and the International Airport. Placing
 * either lays a deterministic branch to the nearest slow-rail line AND the nearest
 * HS1 line. No new "Heathrow"-style spec — `land_airport` is the airport (data.ts).
 */
export const GATEWAY_SPEC_IDS = new Set<string>(['station_ashford', 'land_airport']);

export function isGatewaySpec(sp: Spec | undefined): boolean {
  return !!sp && GATEWAY_SPEC_IDS.has(sp.id);
}

/**
 * The two target line classes a gateway branches to, in FIXED deterministic order:
 * the slow 'rail' line first, then the 'hs1' high-speed line. Each branch is laid as
 * tiles of the SAME spec as the line it joins (a 'rail' branch is 'rail' tiles; an
 * 'hs1' branch is 'hs1' tiles), so the branch matches the target line's class. Both
 * specs are real (never placeholder) so they pass the canEnterSim insertion guard.
 *
 * SINGLE-TRACK (inc3 decision, brief §3.4 step 2): a branch is one deterministic
 * grid path of line tiles — a single track, matching the rail_branch catalogue
 * blurb ("single-track branch railway"). Double-track is a cheap follow-up (lay a
 * parallel offset path) but is NOT attempted here; inc3 ships the single path.
 */
const GATEWAY_BRANCH_TARGET_SPECS = ['rail', 'hs1'] as const;

/** Rail-branch notice shown when a target line exists but no route reaches it. */
export const NO_RAIL_ROUTE_NOTICE = 'no rail route';

/**
 * FEAT-1972079902 inc3 — auto-branch a placed GATEWAY to the rail network.
 *
 * After a gateway (Ashford International / International Airport) is placed, lay a
 * deterministic bidirectional branch line from the gateway to (1) the nearest slow
 * 'rail' line tile and (2) the nearest 'hs1' line tile, routing AROUND existing
 * buildings via the deterministic grid router (railConnect.planRailBranch — the same
 * BFS idiom as road inc1's planConnector). For each target line:
 *   - line absent from the map → skip silently (nothing to connect to);
 *   - gateway already touches the line → nothing to lay;
 *   - route blocked within budget → lay NOTHING for that branch, surface the
 *     "no rail route" notice (never partial-lay a dead-end, never bulldoze);
 *   - otherwise → lay each branch cell as a REAL line tile (journaled through the
 *     gateway `place` action via replay; conservation-safe: cost charged through the
 *     ledger, exactly like road inc1's connector).
 *
 * The rail branch is laid FIRST and its tiles become impassable for the HS1 branch,
 * so the two branches deterministically route around each other. Pure + deterministic
 * (GR#21): all tie-breaking flows from the router; no Date/random.
 *
 * Called with `s` = state AFTER the gateway building was inserted (and after
 * autoConnect). A non-gateway placement is a no-op that just clears railNotice.
 */
function autoBranchRail(s: SimState, placed: Building, sp: Spec): SimState {
  if (!isGatewaySpec(sp)) return { ...s, railNotice: null };

  // Board sets from the CURRENT buildings (includes the just-placed gateway).
  // `occupied` is every building footprint (impassable — route around them).
  // `lineTiles[specId]` is the set of tiles belonging to each target line class.
  // AC-7 FIX: rd_railbridge tiles with bridgeOver='rail'/'hs1' count as valid targets
  // for branch routing (they are part of the line they cross).
  const occupied = new Set<string>();
  const lineTiles: Record<string, Set<string>> = {};
  for (const id of GATEWAY_BRANCH_TARGET_SPECS) lineTiles[id] = new Set<string>();
  for (const b of s.buildings) {
    const bs = SPECS[b.spec];
    if (!bs) continue;
    for (let dx = 0; dx < bs.w; dx++)
      for (let dy = 0; dy < bs.h; dy++) {
        const k = `${b.x + dx},${b.y + dy}`;
        occupied.add(k);
        // Standard line tiles
        const lineSet = lineTiles[b.spec]; // non-undefined only for 'rail' / 'hs1'
        if (lineSet) lineSet.add(k);
        // AC-7: bridge tiles targeting their original line
        if (b.spec === 'rd_railbridge' && (b as any).bridgeOver) {
          const bridgeLineSet = lineTiles[(b as any).bridgeOver];
          if (bridgeLineSet) bridgeLineSet.add(k);
        }
      }
  }

  let funds = s.funds;
  let nextId = s.nextId;
  let ledger = s.ledger;
  let nextLedgerId = s.nextLedgerId;
  const newTiles: Building[] = [];
  let blockedAny = false;

  for (const lineSpecId of GATEWAY_BRANCH_TARGET_SPECS) {
    const targets = lineTiles[lineSpecId];
    if (targets.size === 0) continue; // line absent — nothing to connect to (skip silently).

    const plan = planRailBranch({
      occupied,
      targets,
      bx: placed.x,
      by: placed.y,
      bw: sp.w,
      bh: sp.h,
      mapW: MAP_W,
      mapH: MAP_H,
      budget: RAIL_BRANCH_BUDGET,
    });

    if (plan.connected) continue; // gateway already touches this line — nothing to lay.
    if (plan.blocked) {
      blockedAny = true; // walled off within budget — lay nothing for this branch.
      continue;
    }

    const lineSpec = SPECS[lineSpecId];
    // Honour the SSOT insertion guard exactly like a manual place (defence in depth).
    if (!canEnterSim(lineSpec)) {
      blockedAny = true;
      continue;
    }
    // Branch tiles cost placementCost of the line spec, charged through the ledger —
    // the EXACT idiom road inc1 uses for its connector. Today 'rail'/'hs1' are free
    // to lay (placementCost 0), so a branch spends £0; a future per-tile branch cost
    // is a PLACEHOLDER balance number that would flow through here unchanged.
    const tileCost = placementCost(lineSpec);
    const branchCost = tileCost * plan.path.length;
    // Never fail the placement: an unaffordable branch lays nothing + surfaces the
    // notice (mirrors road inc1). With tileCost 0 this can't trigger today.
    if (funds < branchCost) {
      blockedAny = true;
      continue;
    }

    for (const p of plan.path) {
      const tile: Building = { id: nextId++, spec: lineSpecId, x: p.x, y: p.y, builtTick: s.tick };
      newTiles.push(tile);
      // The freshly-laid branch tiles are impassable for the NEXT branch, so the
      // rail branch and the HS1 branch deterministically route around each other.
      occupied.add(`${p.x},${p.y}`);
    }
    funds -= branchCost;
    ledger = [
      {
        id: nextLedgerId++,
        tick: s.tick,
        label: `Rail branch to ${lineSpec.name} (${plan.path.length})`,
        amount: -branchCost,
      },
      ...ledger,
    ].slice(0, LEDGER_CAP);
  }

  return {
    ...s,
    funds,
    nextId,
    buildings: newTiles.length > 0 ? [...s.buildings, ...newTiles] : s.buildings,
    ledger,
    nextLedgerId,
    railNotice: blockedAny ? NO_RAIL_ROUTE_NOTICE : null,
  };
}

function logEvent(s: SimState, label: string, amount: number): Pick<SimState, 'ledger' | 'nextLedgerId'> {
  return {
    ledger: [{ id: s.nextLedgerId, tick: s.tick, label, amount }, ...s.ledger].slice(0, LEDGER_CAP),
    nextLedgerId: s.nextLedgerId + 1,
  };
}

export type Action =
  | { type: 'tick' }
  | { type: 'speed'; speed: SimState['speed'] }
  | { type: 'tool'; tool: Tool }
  | { type: 'place'; spec: string; x: number; y: number }
  | { type: 'placeRoadPath'; spec: string; tiles: { x: number; y: number }[] }
  | { type: 'bulldoze'; x: number; y: number }
  | { type: 'pickup'; id: number }
  | { type: 'relocate'; x: number; y: number }
  | { type: 'cancelMove' }
  | { type: 'pipeUpgrade'; id: number }
  | { type: 'tax'; which: keyof TaxRates; rate: number }
  | { type: 'policy'; id: PolicyId }
  | { type: 'loan' }
  | { type: 'repay' }
  | { type: 'setClipboard'; clipboard: SimState['clipboard'] }
  | { type: 'stampRegion'; clipboard: SimState['clipboard']; x: number; y: number }
  | { type: 'debugFunds'; amount: number }
  | { type: 'debugXp'; amount: number }
  | { type: 'dismissNotice' }
  | { type: 'unlockAll' }
  | { type: 'reset' }
  | { type: 'hydrate'; state: SimState };

// FEAT-1972079891 inc1 (AC-12): the internal reducer. `reducer` (below) wraps it
// to keep roadConnectivity consistent with buildings after every action.
function reduceCore(state: SimState, action: Action): SimState {
  switch (action.type) {
    case 'tick':
      return advance(state);

    case 'speed':
      return { ...state, speed: action.speed };

    case 'tool':
      return { ...state, tool: action.tool, movingId: null };

    case 'place': {
      const sp = SPECS[action.spec];
      // FEAT-1972079877: canEnterSim is the SSOT guard — rejects unknown specs AND
      // placeholders ("coming soon" roadmap types). The reducer is the AUTHORITATIVE
      // gate, so a placeholder can never enter buildings[] via ANY path (direct
      // dispatch, replay, genesis-replay, debug console), not just the disabled UI
      // button. Mirrors isPlaceable() in data.ts (UI defence-in-depth).
      if (!canEnterSim(sp)) return state;
      if (!specUnlocked(state, sp)) return state;
      // Zoning is free (FEAT-1972079882): a zone charges £0, so the funds check
      // and deduction use placementCost, not the catalogue cost.
      const cost = placementCost(sp);
      // BUG-396 FIX (free-place + silent-fail): only an ACTUAL spend the player
      // cannot cover blocks a placement. A cost-0 placement (a free zone) is always
      // affordable and MUST proceed regardless of funds — even while the treasury is
      // negative — so the `cost > 0` guard keeps insolvency from freezing free zoning.
      // A blocked PAID placement now surfaces a notice (mirrors roadNotice/railNotice)
      // so the player learns why nothing happened instead of a silent no-op.
      // NOTE: this does NOT introduce a funds floor / insolvency halt — funds can still
      // go negative via upkeep; only the up-front paid-placement affordability is gated
      // here. The insolvency STATE (floor / halt-growth / shed) is DEFERRED to Aaron.
      if (cost > 0 && state.funds < cost) {
        return { ...state, placeNotice: `Insufficient funds — ${fmtMoney(cost)} needed` };
      }
      if (
        action.x < 0 ||
        action.y < 0 ||
        action.x + sp.w > MAP_W ||
        action.y + sp.h > MAP_H
      )
        return state;
      if (!fits(occupiedSet(state), sp.w, sp.h, action.x, action.y)) return state;
      const placedBuilding = { id: state.nextId, spec: action.spec, x: action.x, y: action.y, builtTick: state.tick };
      const placedState = {
        ...state,
        funds: state.funds - cost,
        xp: state.xp + 4,
        nextId: state.nextId + 1,
        buildings: [...state.buildings, placedBuilding],
        // BUG-396: a successful placement clears any prior "insufficient funds" notice.
        placeNotice: null,
        ...logEvent(state, `Started ${sp.name}`, -cost),
      };
      // FEAT-1972079907 inc1: auto-wire the building to the road network (lay a
      // fitting connector to the nearest road + upgrade-on-connect), or surface a
      // "no road access" notice. Deterministic; connectors journal via replay.
      const connected = autoConnect(placedState, placedBuilding, sp);
      // FEAT-1972079902 inc3: if this is a GATEWAY (Ashford International /
      // International Airport), auto-lay deterministic branch lines to the nearest
      // slow-rail line AND the nearest HS1 line (routing around buildings), or
      // surface a "no rail route" notice. Deterministic; branch tiles journal via
      // the gateway `place` action through replay. Non-gateways just clear railNotice.
      let updated = autoBranchRail(connected, placedBuilding, sp);

      // FEAT-1972079878 inc1 (AC-6): Create a building monitor for scalable specs.
      // Monitor expires after 1 year (TICKS_PER_YEAR). Type is 'residents' or 'jobs'
      // based on the building's primary capacity type.
      if (sp.capacityTiers) {
        const monitorType: 'residents' | 'jobs' = sp.residents ? 'residents' : 'jobs';
        const newMonitor: BuildingMonitor = {
          buildingId: placedBuilding.id,
          until: state.tick + TICKS_PER_YEAR,
          type: monitorType,
        };
        updated = {
          ...updated,
          buildingMonitors: [...updated.buildingMonitors, newMonitor],
        };
      }

      const rewards = computeLevelRewards(updated);
      if (rewards.length === 0) return updated;
      // Push rewards to pending queue instead of applying immediately.
      // advance() will drain and apply through flows (visible in fiscal panel).
      // Gameplay: reward cash lands at next tick (sub-second at normal speed).
      // CRITICAL: Update lastRewardedLevel NOW so advance() doesn't recompute these rewards
      let lastRewardedLevel = state.lastRewardedLevel;
      for (const r of rewards) {
        lastRewardedLevel = r.newLevel;
      }
      return {
        ...updated,
        pendingRewards: [...state.pendingRewards, ...rewards],
        lastRewardedLevel, // Mark as rewarded now (funds apply later)
        notice: rewards[rewards.length - 1].notice, // Show latest level's notice immediately for UX
      };
    }

    case 'placeRoadPath': {
      // FEAT-1972079910 inc1–2 (AC-3, AC-4, AC-6): atomic all-or-nothing road path placement.
      // Place all tiles in the path, detect junctions where path crosses existing roads,
      // and place junction tiles. If funds cannot cover the total cost (road + junctions),
      // place NOTHING and surface the deficit notice.
      const sp = SPECS[action.spec];
      if (!canEnterSim(sp)) return state;
      if (!specUnlocked(state, sp)) return state;

      // Build maps of existing tile types and their building references.
      // AC-6/7/8: when new road crosses existing road/rail/motorway, convert in place.
      // One-building-per-tile invariant: no stacking, no double upkeep.
      // ⚠ Check MOTORWAY FIRST: m20 is both motorway-class AND a road spec (roadTier 5),
      // so it must be classified as motorway to trigger AC-8 logic, not AC-6 logic.
      const existingRoadByTile = new Map<string, { building: SimState['buildings'][number]; spec: Spec }>();
      const existingRailByTile = new Map<string, { building: SimState['buildings'][number]; spec: Spec }>();
      const existingMotorwayByTile = new Map<string, { building: SimState['buildings'][number]; spec: Spec }>();
      for (const b of state.buildings) {
        const bsp = SPECS[b.spec];
        if (!bsp) continue;
        for (let dx = 0; dx < bsp.w; dx++)
          for (let dy = 0; dy < bsp.h; dy++) {
            const k = `${b.x + dx},${b.y + dy}`;
            if (isMotorwayClassSpec(bsp)) {
              // Check motorway FIRST (before isRoadSpec) since m20 is both
              existingMotorwayByTile.set(k, { building: b, spec: bsp });
            } else if (isRailSpec(bsp)) {
              existingRailByTile.set(k, { building: b, spec: bsp });
            } else if (isRoadSpec(bsp)) {
              existingRoadByTile.set(k, { building: b, spec: bsp });
            }
          }
      }

      // AC-6e: dedup the path to avoid self-intersections (same tile twice in path).
      const pathSet = new Set<string>();
      const dedupPath: typeof action.tiles = [];
      for (const tile of action.tiles) {
        const k = `${tile.x},${tile.y}`;
        if (!pathSet.has(k)) {
          pathSet.add(k);
          dedupPath.push(tile);
        }
      }

      // Identify conversions (existing roads/rail/motorway to convert) and new roads.
      // AC-6: road crossing road → junction
      // AC-7: dual+ road crossing rail → rail bridge (4x cost multiplier)
      // AC-8: road crossing motorway → motorway junction (flat £250k cost)
      // Same-spec overlaps are deduped (no placement, no charge).
      const newRoadTier = roadTierOf(sp);

      // AC-7b validation: reject entire path if below-dual road would cross rail
      // (level crossings not implemented; whole-path occupied rejection).
      if (newRoadTier < 4) {
        for (const tile of dedupPath) {
          const k = `${tile.x},${tile.y}`;
          if (existingRailByTile.has(k)) {
            // Below-dual road crossing rail: reject entire path
            return state; // No placement, no cost, no notice (unchanged behaviour)
          }
        }
      }
      const tilesToPlace: Array<{ x: number; y: number; spec: string; isConversion: boolean; cost: number }> = [];
      const conversions = new Map<string, { buildingId: number; newSpec: string; bridgeOver?: string }>();

      for (const tile of dedupPath) {
        const k = `${tile.x},${tile.y}`;
        const existingRoad = existingRoadByTile.get(k);
        const existingRail = existingRailByTile.get(k);
        const existingMotorway = existingMotorwayByTile.get(k);

        if (existingRoad) {
          // Crossing an existing road: check for dedup or conversion to junction.
          // Dedup: if existing spec is SAME as new road spec, skip entirely (no placement, no cost).
          if (existingRoad.spec.id === action.spec) {
            // Same-spec dedup: road over same road (e.g., repeat-drag)
            tilesToPlace.push({ x: tile.x, y: tile.y, spec: action.spec, isConversion: false, cost: 0 });
          } else {
            // Different specs: convert to junction (DD2 tier rule).
            const existingTier = roadTierOf(existingRoad.spec);
            const junctionTier = Math.max(newRoadTier, existingTier);
            const junctionSpec = junctionTier >= 2 ? 'rd_roundabout' : 'rd_junction';

            // Check if already the right junction type (no conversion needed).
            if (existingRoad.spec.id === junctionSpec) {
              tilesToPlace.push({ x: tile.x, y: tile.y, spec: junctionSpec, isConversion: false, cost: 0 });
            } else {
              // Conversion: will update existing building's spec to junction.
              conversions.set(k, { buildingId: existingRoad.building.id, newSpec: junctionSpec });
              const junctionCost = placementCost(SPECS[junctionSpec] ?? SPECS['rd_junction']);
              tilesToPlace.push({ x: tile.x, y: tile.y, spec: junctionSpec, isConversion: true, cost: junctionCost });
            }
          }
        } else if (existingRail) {
          // FEAT-1972079910 inc3 (AC-7): crossing an existing rail tile.
          // AC-7: dual+ road → convert to rd_railbridge at 4x cost; below-dual → rejected (no placement).
          if (newRoadTier >= 4) {
            // Dual carriageway or above: convert to rail bridge.
            // AC-7 FIX: record the original rail spec so buildRailGeometry can restore line continuity
            conversions.set(k, { buildingId: existingRail.building.id, newSpec: 'rd_railbridge', bridgeOver: existingRail.spec.id });
            // Cost: road cost × 4x multiplier
            const bridgeCost = placementCost(sp) * RAIL_BRIDGE_COST_MULTIPLIER;
            tilesToPlace.push({ x: tile.x, y: tile.y, spec: 'rd_railbridge', isConversion: true, cost: bridgeCost });
          }
          // Below-dual: no placement, no cost (unchanged behaviour — level crossing not implemented).
        } else if (existingMotorway) {
          // FEAT-1972079910 inc3 (AC-8): crossing an existing motorway-class road.
          // Any road crossing motorway → convert to rd_mwyjunction at flat cost.
          conversions.set(k, { buildingId: existingMotorway.building.id, newSpec: 'rd_mwyjunction' });
          tilesToPlace.push({ x: tile.x, y: tile.y, spec: 'rd_mwyjunction', isConversion: true, cost: MOTORWAY_JUNCTION_COST });
        } else {
          // No existing road/rail/motorway: place a new road tile
          tilesToPlace.push({ x: tile.x, y: tile.y, spec: action.spec, isConversion: false, cost: placementCost(sp) });
        }
      }

      // Calculate total cost (conversions charged at junction cost, new roads at road cost).
      let totalCost = 0;
      for (const tile of tilesToPlace) {
        totalCost += tile.cost;
      }

      // All-or-nothing: check affordability before placing anything.
      if (totalCost > 0 && state.funds < totalCost) {
        return { ...state, placeNotice: `Insufficient funds — ${fmtMoney(totalCost)} needed for road path` };
      }

      // Check bounds for all tiles.
      for (const tile of tilesToPlace) {
        if (tile.x < 0 || tile.y < 0 || tile.x >= MAP_W || tile.y >= MAP_H) {
          return state;
        }
      }

      // All checks passed; execute conversions and placements atomically.
      let placedState = { ...state, funds: state.funds - totalCost, placeNotice: null };
      let newPlacementCount = 0;
      let conversionCount = 0;

      // Apply conversions: mutate existing road buildings' specs (preserve id/builtTick).
      placedState = {
        ...placedState,
        buildings: placedState.buildings.map((b) => {
          const found = Array.from(conversions.values()).find((c) => c.buildingId === b.id);
          if (!found) return b;
          conversionCount++;
          // AC-7 FIX: preserve the original rail spec so trains see line continuity
          return { ...b, spec: found.newSpec, ...(found.bridgeOver ? { bridgeOver: found.bridgeOver } : {}) };
        }),
      };

      // Place new road tiles (skip conversions and deduped zero-cost tiles).
      for (const tile of tilesToPlace) {
        const k = `${tile.x},${tile.y}`;
        if (conversions.has(k)) continue; // Skip conversions (already handled above)
        if (tile.cost === 0) continue; // Skip deduped tiles (same-spec overlap, already exists)

        const placedBuilding = {
          id: placedState.nextId,
          spec: tile.spec,
          x: tile.x,
          y: tile.y,
          builtTick: state.tick,
        };
        placedState = {
          ...placedState,
          nextId: placedState.nextId + 1,
          buildings: [...placedState.buildings, placedBuilding],
        };
        newPlacementCount++;
      }

      // Apply ledger event for the total cost.
      const label = conversionCount > 0
        ? `Laid ${newPlacementCount} road tiles + converted ${conversionCount} junctions`
        : `Laid ${newPlacementCount} road tiles`;
      placedState = { ...placedState, ...logEvent(state, label, -totalCost) };

      // Grant XP: 4 per new placement + 1 per conversion (lighter, no rebuilding).
      placedState = { ...placedState, xp: placedState.xp + newPlacementCount * 4 + conversionCount * 1 };

      // Recompute level rewards if any.
      const rewards = computeLevelRewards(placedState);
      if (rewards.length === 0) return placedState;
      let lastRewardedLevel = state.lastRewardedLevel;
      for (const r of rewards) {
        lastRewardedLevel = r.newLevel;
      }
      return {
        ...placedState,
        pendingRewards: [...state.pendingRewards, ...rewards],
        lastRewardedLevel,
        notice: rewards[rewards.length - 1].notice,
      };
    }

    case 'bulldoze': {
      const target = state.buildings.find((b) => {
        const sp = SPECS[b.spec];
        if (!sp) return false;
        return (
          action.x >= b.x &&
          action.x < b.x + sp.w &&
          action.y >= b.y &&
          action.y < b.y + sp.h
        );
      });
      if (!target) return state;
      const def = SPECS[target.spec];
      // Refund 25% of what was actually PAID — a free zone refunds nothing, so
      // place-then-bulldoze cannot mint money.
      const refund = Math.round(placementCost(def) * 0.25);
      return {
        ...state,
        funds: state.funds + refund,
        buildings: state.buildings.filter((w) => w.id !== target.id),
        ...(state.movingId === target.id ? { movingId: null } : {}),
        ...logEvent(state, `Demolished ${def.name}`, refund),
      };
    }

    case 'pickup':
      return { ...state, movingId: action.id };

    case 'relocate': {
      if (state.movingId == null || state.funds < MOVE_COST) return state;
      const moving = state.buildings.find((b) => b.id === state.movingId);
      if (!moving) return { ...state, movingId: null };
      const sp = SPECS[moving.spec];
      if (
        action.x < 0 ||
        action.y < 0 ||
        action.x + sp.w > MAP_W ||
        action.y + sp.h > MAP_H ||
        !fits(occupiedSet(state, moving.id), sp.w, sp.h, action.x, action.y)
      )
        return state;
      return {
        ...state,
        funds: state.funds - MOVE_COST,
        movingId: null,
        buildings: state.buildings.map((b) =>
          b.id === moving.id ? { ...b, x: action.x, y: action.y } : b
        ),
      };
    }

    case 'cancelMove':
      return { ...state, movingId: null };

    case 'stampRegion': {
      // FEAT-1972079853: Clone-stamp tool — deterministically flatten + place a region.
      // The clipboard carries relative offsets (dx, dy) from the region's origin.
      // Stamp at (x, y) means each item lands at (x + item.dx, y + item.dy).
      if (!action.clipboard) return state;

      // Validate that all item footprints fit within bounds.
      // For each item in clipboard, compute its actual world pos and check bounds.
      for (const item of action.clipboard.items) {
        const sp = SPECS[item.spec];
        // FEAT-1972079877: SSOT guard — reject the WHOLE stamp (all-or-nothing,
        // matching the bounds check) if any clipboard item is unknown OR a
        // placeholder. Keeps a clone-stamp (reachable via a crafted debug-JSON
        // clipboard + journaled/replayed stampRegion) from smuggling a
        // "coming soon" type into buildings[].
        if (!canEnterSim(sp)) return state;
        const ax = action.x + item.dx;
        const ay = action.y + item.dy;
        if (ax < 0 || ay < 0 || ax + sp.w > MAP_W || ay + sp.h > MAP_H) {
          return state; // Out of bounds
        }
      }

      // Deterministic flatten: remove ALL buildings whose footprint overlaps the landing zone.
      // To avoid non-determinism, iterate through buildings in a stable order (by id).
      // Collect all cells that will be occupied by stamped items.
      const landingCells = new Set<string>();
      for (const item of action.clipboard.items) {
        const sp = SPECS[item.spec];
        const ax = action.x + item.dx;
        const ay = action.y + item.dy;
        for (let dy = 0; dy < sp.h; dy++) {
          for (let dx = 0; dx < sp.w; dx++) {
            landingCells.add(`${ax + dx},${ay + dy}`);
          }
        }
      }

      // Find all buildings that have ANY cell in the landing zone.
      const toRemove = new Set<number>();
      for (const b of state.buildings) {
        const sp = SPECS[b.spec];
        if (!sp) continue;
        for (let dy = 0; dy < sp.h; dy++) {
          for (let dx = 0; dx < sp.w; dx++) {
            if (landingCells.has(`${b.x + dx},${b.y + dy}`)) {
              toRemove.add(b.id);
              break;
            }
          }
          if (toRemove.has(b.id)) break;
        }
      }

      // Calculate total refund and new buildings array.
      let refundTotal = 0;
      let newBuildings = state.buildings.filter((b) => {
        if (toRemove.has(b.id)) {
          const sp = SPECS[b.spec];
          if (sp) refundTotal += Math.round(placementCost(sp) * 0.25);
          return false;
        }
        return true;
      });

      // Calculate total placement cost for the stamped items.
      let totalCost = 0;
      for (const item of action.clipboard.items) {
        const sp = SPECS[item.spec];
        if (sp) totalCost += placementCost(sp);
      }

      // Check funds.
      const netCost = totalCost - refundTotal;
      if (state.funds < netCost) return state;

      // Place all items.
      let nextId = state.nextId;
      for (const item of action.clipboard.items) {
        const sp = SPECS[item.spec];
        if (!sp) continue;
        const ax = action.x + item.dx;
        const ay = action.y + item.dy;
        newBuildings.push({
          id: nextId++,
          spec: item.spec,
          x: ax,
          y: ay,
          builtTick: state.tick,
        });
      }

      // Compute XP: 4 XP per placed item (same as place action).
      const xpGain = action.clipboard.items.length * 4;

      // Build updated state.
      const updated = {
        ...state,
        funds: state.funds - netCost,
        xp: state.xp + xpGain,
        nextId,
        buildings: newBuildings,
        ...logEvent(
          state,
          `Stamped region (${action.clipboard.items.length} items)`,
          -netCost
        ),
      };

      // Check for level rewards (same as place action).
      const rewards = computeLevelRewards(updated);
      if (rewards.length === 0) return updated;

      let lastRewardedLevel = state.lastRewardedLevel;
      for (const r of rewards) {
        lastRewardedLevel = r.newLevel;
      }
      return {
        ...updated,
        pendingRewards: [...state.pendingRewards, ...rewards],
        lastRewardedLevel,
        notice: rewards[rewards.length - 1].notice,
      };
    }

    case 'pipeUpgrade': {
      const b = state.buildings.find((w) => w.id === action.id);
      if (!b) return state;
      const sp = SPECS[b.spec];
      if (sp?.kind !== 'water') return state;
      const tier = state.pipeTier[action.id] ?? 0;
      if (tier >= PIPE_TIERS.length - 1) return state;
      const cost = PIPE_TIERS[tier + 1].upgradeCost;
      if (state.funds < cost) return state;
      return {
        ...state,
        funds: state.funds - cost,
        pipeTier: { ...state.pipeTier, [action.id]: tier + 1 },
        ...logEvent(state, `${sp.name} pipe upgraded to ${PIPE_TIERS[tier + 1].label}`, -cost),
      };
    }

    case 'tax':
      return { ...state, taxRates: { ...state.taxRates, [action.which]: action.rate } };

    case 'policy':
      return { ...state, policies: { ...state.policies, [action.id]: !state.policies[action.id] } };

    case 'loan': {
      if (state.loanBalance > 0) return state;
      return {
        ...state,
        funds: state.funds + LOAN_PRINCIPAL,
        loanBalance: LOAN_TOTAL,
        ...logEvent(state, `Municipal loan taken (+${LOAN_PRINCIPAL})`, LOAN_PRINCIPAL),
      };
    }

    case 'repay': {
      if (state.loanBalance === 0 || state.funds < state.loanBalance) return state;
      const bal = state.loanBalance;
      return {
        ...state,
        funds: state.funds - bal,
        loanBalance: 0,
        ...logEvent(state, 'Loan repaid in full', -bal),
      };
    }

    case 'setClipboard':
      // FEAT-1972079853: UI-only action — just stores the clipboard for the ghost preview.
      // Does not affect game state deterministically; stampRegion carries the full clipboard.
      return { ...state, clipboard: action.clipboard };

    case 'debugFunds':
      // Tick-boundary invariant (Round-6): between-tick mutations never affect
      // conservation checks (they use funcsAtTickStart/End only, not working-tree funds).
      // No re-baselining needed.
      return { ...state, funds: state.funds + action.amount };

    case 'debugXp': {
      const updated = { ...state, xp: state.xp + action.amount };
      const rewards = computeLevelRewards(updated);
      if (rewards.length === 0) return updated;
      // Push each reward to pending queue instead of applying immediately.
      // advance() will drain and apply through flows (visible in fiscal panel).
      // Gameplay: reward cash lands at next tick (sub-second at normal speed).
      // CRITICAL: Update lastRewardedLevel NOW so advance() doesn't recompute these rewards
      let lastRewardedLevel = state.lastRewardedLevel;
      for (const r of rewards) {
        lastRewardedLevel = r.newLevel;
      }
      return {
        ...updated,
        pendingRewards: [...state.pendingRewards, ...rewards],
        lastRewardedLevel, // Mark as rewarded now (funds apply later)
        notice: rewards[rewards.length - 1].notice, // Show latest level's notice immediately for UX
      };
    }

    case 'dismissNotice':
      return state.notice == null ? state : { ...state, notice: null };

    case 'unlockAll': {
      // God-mode "Unlock all" (FEAT-1972079899): flip the catalogue gate for a large
      // cash gate. Deterministic + all-or-nothing — with insufficient funds the state
      // is returned untouched (no partial unlock, no partial charge). The funds
      // deduction is a between-tick mutation exactly like debugFunds/place cost, so it
      // never disturbs the tick-boundary conservation invariant; a ledger entry is
      // recorded for UI visibility (mirrors how `place` logs its spend).
      if (state.unlockedAll) return state; // idempotent — already unlocked, no re-charge
      if (state.funds < UNLOCK_ALL_COST) return state;
      return {
        ...state,
        funds: state.funds - UNLOCK_ALL_COST,
        unlockedAll: true,
        ...logEvent(state, 'Unlock all (god mode)', -UNLOCK_ALL_COST),
      };
    }

    case 'reset': {
      const s = rawState();
      s.speed = state.speed;
      return advance(s);
    }

    case 'hydrate':
      return sanitizeTreasury(action.state);
  }
}

/**
 * FEAT-1972079891 inc1 (AC-12) — the public reducer. Delegates to reduceCore, then
 * keeps `roadConnectivity` consistent with the resulting buildings so the road
 * activation gates re-evaluate in the SAME tick a road is placed/removed (no delay
 * tick), and so a state always carries a connectivity graph matching its buildings.
 * Recomputes ONLY when buildings changed or the graph is missing — a pure,
 * deterministic function of buildings (no Date/random). A plain `tick` already set
 * the graph in advance() with the same buildings ref, so it is not recomputed.
 */
export function sanitizeTreasury(s: SimState): SimState {
  const funds = sanitizeFunds(s.funds);
  const loanBalance = sanitizeFunds(s.loanBalance);
  let notice = s.notice;
  if (notice && (notice.cash < 0 || !Number.isSafeInteger(notice.cash))) {
    notice = { ...notice, cash: 0 };
  }
  if (funds === s.funds && loanBalance === s.loanBalance && notice === s.notice) return s;
  return { ...s, funds, loanBalance, notice };
}

export function reducer(state: SimState, action: Action): SimState {
  const s = sanitizeTreasury(state);
  const next = reduceCore(s, action);
  if (next.buildings !== s.buildings || !next.roadConnectivity) {
    return { ...next, roadConnectivity: computeRoadConnectivity(next) };
  }
  return next;
}

export function approvalOf(s: SimState): number {
  const t = s.taxRates;
  const avgTax = (t.residential + t.commercial + t.industrial) / 3;
  let a = 62 - avgTax * 1.5;
  a += Math.min(6, 3 * stationLinks(s).connectedIds.size);
  if (waterBalanceOf(s).leak) a -= 5;
  if (s.policies.transitSubsidy) a += 8;
  if (s.policies.austerity) a -= 12;
  if (s.policies.recycling) a -= 2;
  return Math.max(0, Math.min(100, Math.round(a)));
}

export function wellbeingOf(s: SimState): {
  overall: number;
  parts: { label: string; value: number }[];
} {
  const pop = s.population;
  // Early-game blend toward a 55 baseline while pop < 50 — same ramp as the
  // demand meters' earlyGameFactor so the two systems damp identically.
  // ⚠ BALANCE-NUMBER PLACEHOLDER (55 baseline, pop/50 ramp) — Aaron's pass.
  const f = earlyGameFactor(pop);
  const blend = (computed: number) => Math.round(computed * f + 55 * (1 - f));

  // BUG-392: every service part consumes the SAME per-service coverage ratios
  // as the demand meters (data.ts serviceCoverageOf — single source of truth,
  // GR#3). Before this fix wellbeingOf re-derived coverage with its own
  // formula and its own (mismatched) units, so demand could peg at +100 while
  // wellbeing read 91. Now a service part is high iff its demand index is low:
  // at full damping, part ≈ 100 - demand for any under-covered service.
  const covById = new Map(serviceCoverageOf(s).map((r) => [r.id, r.coverage]));
  const ratio = (id: string): number => Math.min(1, covById.get(id) ?? 1);
  // ⚠ BALANCE-NUMBER PLACEHOLDER: linear coverage→score map, 0–100 clamp
  // (the old +20% over-provision bonus is dropped), pending Aaron's pass.
  const part = (coverage: number) => blend(Math.round(clampN(coverage * 100, 0, 100)));

  // ⚠ BALANCE-NUMBER PLACEHOLDERS: parks formula and the equal (1/3) education
  // stage weights below, pending Aaron's pass.
  // BUG-415 FIX: parks coverage formula now matches the serviceCoverageOf pattern.
  // Parks capacity = footprint sum (w × h per building). Parks need is pop-based.
  // Coverage = capacity / need; wellbeing = part(coverage). This prevents underflow
  // at high population (the old formula yielded 0 when pop > park_capacity * 70/0.5).
  let parksCapacity = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind === 'park') parksCapacity += sp.w * sp.h;
  }
  // Parks need = pop * 0.002 (equivalent to "1 park per 500 people" expectation).
  // For a city with 1M people, that's ~2000 parks needed. Coverage = capacity / need.
  const parksNeed = Math.max(1, pop * 0.002);
  const parksCoverage = parksCapacity / parksNeed;
  const parks = part(Math.min(1, parksCoverage));
  const education = (ratio('nursery') + ratio('primary') + ratio('college')) / 3;
  // BUG-392 base: utilities coverage = min(power, clean water) — the weakest
  // grid utility bounds the part.
  const utilities = Math.min(ratio('power'), ratio('cleanwater'));
  // BUG-393 seam, ON TOP of the coverage base: while a power DEFICIT is
  // active, rolling outages hurt everything the grid touches beyond the mere
  // MW shortfall the coverage ratio already expresses — so the blended part
  // is additionally multiplied by (1 - deficitRatio·BROWNOUT_WELLBEING_K).
  // brownoutOf's deficitRatio equals 1 - the power row's coverage (same
  // powerStats source, GR#3), so the two penalties can never disagree about
  // whether a deficit exists. No deficit → factor 1 → pure BUG-392 value.
  // The multiplier applies AFTER blend, so a brownout can drag the part below
  // the early-game 55 baseline — a brownout bites however small the town.
  // Deterministic; BROWNOUT_WELLBEING_K is PLACEHOLDER (balance regime).
  const brownout = brownoutOf(s);
  const utilitiesValue = brownout.active
    ? Math.max(0, Math.round(part(utilities) * (1 - brownout.deficitRatio * BROWNOUT_WELLBEING_K)))
    : part(utilities);

  const parts = [
    { label: 'Approval', value: approvalOf(s) },
    { label: 'Parks & leisure', value: parks },
    { label: 'Healthcare', value: part(ratio('gp')) },
    { label: 'Hospital care', value: part(ratio('hosp')) },
    { label: 'Education', value: part(education) },
    { label: 'Safety', value: part(ratio('police')) },
    { label: 'Utilities', value: utilitiesValue },
    { label: 'Sewage', value: part(ratio('waste')) },
    // FEAT-1972079906 inc1 — WASTE-HEALTH penalty. Uncollected refuse hurts
    // wellbeing: the part tracks collection coverage (part(1) ⇒ ~100 = no penalty
    // when fully collected; part(0) ⇒ ~0 when nothing is collected), so a higher
    // uncollected fraction drags overall wellbeing down monotonically. When a city
    // generates NO waste (no online residents/jobs) coverage is defined as 1, so
    // the part is neutral and adds no penalty. (A disease/health track is inc2.)
    // ⚠ BALANCE-NUMBER PLACEHOLDER: reuses the shared coverage→part map.
    { label: 'Refuse', value: part(collectionCoverageOf(s)) },
  ];
  // ⚠ BALANCE-NUMBER PLACEHOLDER: equal part weights, pending Aaron's pass.
  const overall = Math.round(parts.reduce((a, p) => a + p.value, 0) / parts.length);
  return { overall, parts };
}

/**
 * Helper for BUG-393 testing: calculate the Utilities wellbeing part value
 * WITHOUT the brownout penalty (i.e., what it would be if there was no deficit).
 * Used to verify that the brownout multiplier actually reduces the part.
 */
export function utilitiesWellbeingUnpenalized(s: SimState): number {
  const pop = s.population;
  const f = earlyGameFactor(pop);
  const blend = (computed: number) => Math.round(computed * f + 55 * (1 - f));
  const covById = new Map(serviceCoverageOf(s).map((r) => [r.id, r.coverage]));
  const ratio = (id: string): number => Math.min(1, covById.get(id) ?? 1);
  const part = (coverage: number) => blend(Math.round(clampN(coverage * 100, 0, 100)));
  const utilities = Math.min(ratio('power'), ratio('cleanwater'));
  return part(utilities);
}

export function initialState(): SimState {
  return advance(rawState());
}
