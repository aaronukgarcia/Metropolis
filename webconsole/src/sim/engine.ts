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
  countByKindOnline,
  fits,
  isOnline,
  isRoadAdjacent,
  isRoadConnected,
  memoOnState,
  buildingByIdOf,
  occupiedSet,
  occupiedSetIncremental,
  roadTileSetOf,
  roadTileSetIncremental,
  isRoadOrTrunkSpec,
  placementCost,
  serviceCoverageOf,
  earlyGameFactor,
  brownoutOf,
  isBrownoutActive,
  BROWNOUT_WELLBEING_K,
  stationLinks,
  totalJobs,
  unemploymentOf,
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
  onlineResidentsCapacity,
  totalChildrenCapacity,
  totalServedCapacity,
  heightCapOf,
  footprintOf,
  demandFixPlan,
  orderedDemandFixPlan,
  RESOLVE_DEMAND_ALL_MAX_UNITS,
  createSpotSearchContext,
  noBuildableSiteReason,
  crimeRateOf,
  lineUsageOf,
  advanceCongestionTicks,
  congestionFactorOf,
  sanitizeCongestionTicksBySpec,
  CONGESTION_CONSTANTS,
  MILESTONES,
  MILESTONE_REWARDS,
  sanitizeClaimedMilestones,
  filledJobsBySector,
  surplusInstancesOf,
  remainingAllowance,
  constructionTicks,
  BULLDOZE_REFUND_FRACTION,
  CONSOLIDATOR_SCRAP_FRACTION,
  JOBS_GRANDFATHER_ECONOMY_EPOCH,
  stampJobsGrandfather,
  stampTunnelFootprintGrandfather,
  TUNNEL_FOOTPRINT_GRANDFATHER_EPOCH,
} from './data.ts';
import type { Spec, RoadTier, DemandFixPlanItem } from './data.ts';
import { planConnector } from './roadConnect.ts';
import { planRailBranch, RAIL_BRANCH_BUDGET } from './railConnect.ts';
import type {
  Building,
  RoadMonitor,
  BuildingMonitor,
  DemographicFlow,
  MonthlyDemographics,
  ArrivalsByMode,
  MonthlyArrivalsByMode,
  InsolvencyState,
  BailoutOrigin,
} from './types.ts';
import { fmtMoney } from './utils.ts';
import { councilTaxPerTick, businessTaxPerTick, sectorWagesPerTick, gridExportRevenuePerTick, GRID_EXPORT_TARIFF_PER_MW, gridImportCostPerTick, GRID_IMPORT_TARIFF_PER_MW, GRID_IMPORT_ENABLED_DEFAULT, GRID_IMPORT_OUTFLOW_LABEL, applyOutflowPolicies, UPKEEP_BUCKET, overdraftInterestPerTick, sanitizeFunds, insolvencyStateForFunds, BAILOUT_DURATION_TICKS, ASSET_SALE_VALUE_FRACTION, ASSET_SALE_LABEL, ADMINISTRATION_DURATION_TICKS, ADMINISTRATION_PLACE_BLOCKED_MESSAGE, ADMINISTRATION_POLICY_BLOCKED_MESSAGE, SECOND_BAILOUT_DURATION_TICKS, BAILOUT_INCOME_INJECTION_SECOND, BAILOUT_SECOND_INJECTION_LABEL, FINAL_DECLINE_FUNDS_THRESHOLD, STARTING_TREASURY, BAILOUT_CLEAN_END_THRESHOLD, SUSTAINED_RECOVERY_TICKS, DECLINE_AVERAGING_WINDOW_TICKS, BAILOUT_STANDING_COST_LABEL, bailoutStandingCostPerTick, PLAY_MODE_INJECTION_AMOUNT, PLAY_MODE_INJECTION_LABEL, netOpexBleedPerTick, computeDynamicBailoutOffer, DYNAMIC_BAILOUT_INJECTION_LABEL, INSOLVENCY_WARNING_THRESHOLD } from './fiscal.ts';
// FEAT-2326609761 (CONSOLIDATOR mutation lane) — read-only discovery/opportunity
// functions from the PARALLEL read-only lane's module. Safe one-directional
// import: consolidator.ts is a LEAF (mirrors TICKS_PER_MONTH/CONNECT_EXEMPT_KINDS
// locally rather than importing them from here — see its own header note) so
// this does not form a cycle.
import {
  sectionIndexOf,
  monthlyScopeOf,
  findReconnectionOpportunities,
  findOpportunities,
  capacityOf,
  familyKeyOf,
  sectionOriginOf,
  sectionKeyOf,
  sectionTilesOf,
  SECTIONS_X,
  SECTIONS_Y,
} from './consolidator.ts';
// FEAT-2326609761 inc2 (Aaron's glide-mode ruling, 2026-09-04): the pure day
// cursor. consolidatorGlide.ts is a true leaf (see its own header note) so
// this import creates no cycle even though consolidator.ts already imports
// FROM this file.
import { glideWindowForDay } from './consolidatorGlide.ts';
import type { ConsolidationPass, ConsolidationTransaction, ConsolidationRecord, SectionAudit } from './consolidator.ts';
import type {
  FlowItem,
  LedgerEntry,
  LevelUpNotice,
  MilestoneNotice,
  PolicyId,
  SimState,
  TaxRates,
  Tool,
  ConsolidatorMode,
  ConsolidatorSliders,
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

// FEAT-1972079925 — demographic flow rate constants. ALL PLACEHOLDER under the
// balance-number regime (directional only, pending Aaron's row-by-row pass).
// These replace the bare converge-to-capacity rule (former POPULATION_GROWTH_RATE,
// BUG-394) with real per-tick births/deaths/move-ins/move-outs so a city AT
// capacity shows churn instead of freezing at a single integer forever.

/** Fraction of population born per tick. Slow background growth. */
export const BIRTH_RATE_PER_TICK = 0.0008;
/** Fraction of population dying per tick. Slightly below births so natural
 * increase alone is small and positive — move-flows dominate the trajectory. */
export const DEATH_RATE_PER_TICK = 0.0005;
/**
 * Fraction of EFFECTIVE headroom (see `advance()`) that moves in per tick,
 * scaled by the attractiveness factor (tax/transit/demand/station terms —
 * the same shape the old growthFactor used, renamed). Kept numerically equal
 * to the retired POPULATION_GROWTH_RATE (0.15) so the below-capacity growth
 * trajectory stays close to every already-landed scenario test.
 */
export const MOVE_IN_RATE = 1.2;
/** Base fraction of population moving out per tick, before the wellbeing
 * penalty below is applied. Small — most churn at capacity is backfilled. */
export const MOVE_OUT_BASE_RATE = 0.003;
/**
 * How strongly falling wellbeing raises the move-out rate: effective rate =
 * MOVE_OUT_BASE_RATE * (1 + WELLBEING_MOVEOUT_FACTOR * (100 - wellbeing) / 100).
 * At wellbeing 100 the rate is exactly the base; at wellbeing 0 it is
 * base * (1 + WELLBEING_MOVEOUT_FACTOR).
 */
export const WELLBEING_MOVEOUT_FACTOR = 1.5;
/** Months of demographicHistory retained (bounded ring, mirrors HISTORY_CAP). */
export const DEMOGRAPHIC_HISTORY_CAP = 120;

// ════════════════════════════════════════════════════════════════════════════
// FEAT-1972079926 — ARRIVALS-BY-MODE: split each tick's moveIns (the SSOT
// total established by FEAT-1972079925) across the transport modes that are
// CONNECTED AND ONLINE in the city. Companion to the demographic flows above
// — a conservation-preserving SPLIT of moveIns, never an independent count.
// ════════════════════════════════════════════════════════════════════════════

/**
 * ⚠ BALANCE-NUMBER PLACEHOLDERS (Aaron's blanket rule): directional only,
 * pending the row-by-row balance pass. Renormalised over only the AVAILABLE
 * modes at split time (see splitArrivalsByMode) so an unavailable mode is
 * EXACTLY zero rather than silently under-weighted.
 */
export const MODE_WEIGHT_ROAD = 0.55;
export const MODE_WEIGHT_RAIL_LOW = 0.22;
export const MODE_WEIGHT_RAIL_HS = 0.14;
export const MODE_WEIGHT_SEA = 0.05;
export const MODE_WEIGHT_PLANE = 0.04;

/** Bounded-ring cap for arrivalsByModeHistory. Mirrors DEMOGRAPHIC_HISTORY_CAP. */
export const ARRIVALS_HISTORY_CAP = DEMOGRAPHIC_HISTORY_CAP;

export interface ModeAvailability {
  road: boolean;
  railLow: boolean;
  railHs: boolean;
  sea: boolean;
  plane: boolean;
}

/**
 * FEAT-1972079926 — which arrival modes are CONNECTED AND ONLINE in the city
 * right now, derived from the ACTUAL building roster (registration in
 * `s.buildings`, never catalogue existence — the built-not-wired lesson):
 *   - road: always available (every city has roads).
 *   - railLow: an online station (station_sanderling, kind 'station', NOT
 *     the HS1 gateway) connected to the road network (stationLinks).
 *   - railHs: the Ashford International gateway (station_ashford) online +
 *     connected AND at least one hs1 line tile actually built (a station
 *     with no line laid carries no HS traffic).
 *   - sea: a built + online harbour (land_harbour) or ferry pier (ferry_pier).
 *   - plane: a built + online international airport (land_airport).
 * Pure + deterministic (GR#21): no Date/Math.random, single ordered pass
 * over s.buildings.
 */
export function modeAvailability(s: SimState): ModeAvailability {
  const links = stationLinks(s);
  let railLow = false;
  let railHsStation = false;
  let hs1Built = false;
  let sea = false;
  let plane = false;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    if (sp.id === 'hs1') hs1Built = true;
    if (sp.kind === 'station' && links.connectedIds.has(b.id) && isOnline(s, b)) {
      if (sp.id === 'station_ashford') railHsStation = true;
      else railLow = true;
    }
    if ((sp.id === 'land_harbour' || sp.id === 'ferry_pier') && isOnline(s, b)) sea = true;
    if (sp.id === 'land_airport' && isOnline(s, b)) plane = true;
  }
  return { road: true, railLow, railHs: railHsStation && hs1Built, sea, plane };
}

const ARRIVAL_MODE_ORDER: (keyof ArrivalsByMode)[] = ['road', 'railLow', 'railHs', 'sea', 'plane'];

/**
 * FEAT-1972079926 — split one tick's moveIns across the AVAILABLE transport
 * modes. The named PLACEHOLDER weights above are renormalised over only the
 * modes `modeAvailability` reports available (an unavailable mode gets
 * EXACTLY zero), then allocated as integers via floor + largest-remainder-in-
 * fixed-order so the split always sums back to `moveIns` EXACTLY
 * (conservation — mirrors the hs1/rail integer-exact usage split in
 * data.ts's commuterFlowSplit). Deterministic: the remainder walk uses the
 * FIXED ARRIVAL_MODE_ORDER array, never object-key iteration order.
 */
export function splitArrivalsByMode(s: SimState, moveIns: number): ArrivalsByMode {
  const avail = modeAvailability(s);
  const weights: Record<keyof ArrivalsByMode, number> = {
    road: avail.road ? MODE_WEIGHT_ROAD : 0,
    railLow: avail.railLow ? MODE_WEIGHT_RAIL_LOW : 0,
    railHs: avail.railHs ? MODE_WEIGHT_RAIL_HS : 0,
    sea: avail.sea ? MODE_WEIGHT_SEA : 0,
    plane: avail.plane ? MODE_WEIGHT_PLANE : 0,
  };
  const out: ArrivalsByMode = { road: 0, railLow: 0, railHs: 0, sea: 0, plane: 0 };
  const totalWeight = ARRIVAL_MODE_ORDER.reduce((a, k) => a + weights[k], 0);
  if (moveIns <= 0 || totalWeight <= 0) return out;

  const floors: Record<keyof ArrivalsByMode, number> = { road: 0, railLow: 0, railHs: 0, sea: 0, plane: 0 };
  let allocated = 0;
  for (const k of ARRIVAL_MODE_ORDER) {
    const share = Math.floor((moveIns * weights[k]) / totalWeight);
    floors[k] = share;
    allocated += share;
  }
  let remainder = moveIns - allocated;
  for (const k of ARRIVAL_MODE_ORDER) out[k] = floors[k];
  // Distribute the remainder one-by-one, fixed order, skipping zero-weight
  // (unavailable) modes. `road` always carries positive weight, so this
  // always terminates well within one pass over the 5-entry order.
  let i = 0;
  while (remainder > 0 && i < ARRIVAL_MODE_ORDER.length * moveIns + 5) {
    const k = ARRIVAL_MODE_ORDER[i % ARRIVAL_MODE_ORDER.length];
    if (weights[k] > 0) {
      out[k] += 1;
      remainder -= 1;
    }
    i += 1;
  }
  return out;
}

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
  // BUG-520 (remaining part): an OFFLINE / road-disconnected commercial or
  // industrial building must not accelerate growth demand — countByKindOnline
  // mirrors the powerStats()/sumBy() activation gate.
  const c = countByKindOnline(s);
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
    // BUG-452 inc1 (2026-09-01): STARTING_TREASURY (fiscal.ts) is the SSOT —
    // was a hardcoded £10,000,000 toy figure, now Aaron's real "£1.5M, start
    // truly small" anchor. Retune in ONE place (fiscal.ts).
    funds: STARTING_TREASURY,
    loanBalance: 0,
    population: 0,
    xp: 30,
    taxRates: { residential: 9, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: false },
    // FEAT-2326609711 inc1 (AC-1): new cities default to external power cover
    // ON (GRID_IMPORT_ENABLED_DEFAULT, fiscal.ts — Aaron's Design Ruling).
    gridImportEnabled: GRID_IMPORT_ENABLED_DEFAULT,
    // FEAT-2326609761 inc1 (AC-1, ASM-1504): new cities default to the
    // consolidator OFF (CONSOLIDATOR_ENABLED_DEFAULT, above).
    consolidatorEnabled: CONSOLIDATOR_ENABLED_DEFAULT,
    // BUG-652 GRANDFATHERING (2026-09-04): a brand-new city is always on the
    // current economy epoch — it has no pre-existing buildings from an older
    // economy to migrate, so stampJobsGrandfather() is immediately a no-op
    // for it (see that function's own epoch-already-current early return).
    economyEpoch: JOBS_GRANDFATHER_ECONOMY_EPOCH,
    // Aaron ruling 2026-09-04 (land_tunnel footprint grew bigger): a
    // brand-new city has no pre-existing tunnels to grandfather, so it
    // starts on the current epoch — mirrors economyEpoch immediately above.
    tunnelFootprintEpoch: TUNNEL_FOOTPRINT_GRANDFATHER_EPOCH,
    // FEAT-2326609761 inc2: new cities default to glide mode, the 800m
    // section size, and an even 25/25/25/25 slider split (all defaults above).
    consolidatorMode: CONSOLIDATOR_MODE_DEFAULT,
    consolidatorSectionMetres: CONSOLIDATOR_SECTION_METRES_DEFAULT,
    consolidatorSliders: CONSOLIDATOR_SLIDERS_DEFAULT,
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
    fundsAtTickStart: STARTING_TREASURY,
    fundsAtTickEnd: STARTING_TREASURY,
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
    demographicAccum: { births: 0, deaths: 0, moveIns: 0, moveOuts: 0 },
    demographicHistory: [],
    lastDemographics: { births: 0, deaths: 0, moveIns: 0, moveOuts: 0 },
    arrivalsByModeAccum: { road: 0, railLow: 0, railHs: 0, sea: 0, plane: 0 },
    arrivalsByModeHistory: [],
    lastArrivalsByMode: { road: 0, railLow: 0, railHs: 0, sea: 0, plane: 0 },
    // FEAT-1972079923 inc1: opening treasury is well above both thresholds — solvent.
    insolvencyState: 'solvent',
    // BUG-496: raw funds-band tracked separately from the overlaid insolvencyState above.
    insolvencyRawBand: 'solvent',
    insolvencyPopup: null,
    // FEAT-1972079923 inc2: no bailout active at game start.
    bailoutState: null,
    // FEAT-1972079923 inc3: no administration active at game start.
    administrationState: null,
    // FEAT-1972079923 inc4: no second bailout / decline at game start.
    bailoutSecondState: null,
    declineState: null,
    // FEAT-1972079923 inc4 (AC-11): decline trackers start from the opening state.
    peakPopulation: 0,
    // BUG-452 inc1: derive from the STARTING_TREASURY SSOT so a retune auto-scales
    // (was a leftover £10M toy-scale literal; harmless at genesis since the first
    // advance min()'s it to funds, but it must honour the one-constant promise).
    minFundsEver: STARTING_TREASURY,
    totalSpending: 0,
    // BUG-504 Option A: no first-bailout re-arm used yet.
    firstBailoutCount: 0,
    // BUG-506 (AC-506-1/2): no sustained-recovery streak at game start.
    recoveryStreak: 0,
    // BUG-506 (AC-506-3/4): the rolling funds window starts empty.
    recentFundsWindow: [],
    // FEAT-2326609723: Play Mode's one-way latch — never engaged at game start.
    playModeLatched: false,
    // FEAT-dynamic-bailout: genesis has spent nothing yet — no backfill ever
    // needed for a brand-new game (capexBackfilled starts already true).
    cumulativeCapexSpent: 0,
    capexBackfilled: true,
    // FEAT-dynamic-bailout (Aaron ruling Q100045): the ONE dynamic bailout
    // has not been used at game start.
    dynamicBailoutUsed: false,
    // FEAT-2326609761 (CONSOLIDATOR, AC-25/AC-34): the enable flag defaults
    // via CONSOLIDATOR_ENABLED_DEFAULT above (landed by the read-only lane,
    // 893511b) — a new city never starts with a background actor already
    // demolishing things. The pass log starts empty.
    consolidatorLog: [],
    // F4 FIX: given a REAL default here (not left `undefined`) for the same
    // reason gridImportEnabled/consolidatorEnabled are — advance() writes
    // this key on EVERY tick (`consolidatorUndoConsumed: consolidatorPassLog
    // ? false : s.consolidatorUndoConsumed`), so a genesis state that left it
    // `undefined` would carry an EXPLICIT `undefined`-valued own-property
    // forward from tick 1 onward. JSON.stringify (every save/restore path)
    // silently DROPS undefined-valued properties, so the round-tripped state
    // would then lack this key entirely while the live in-memory state still
    // has it (as `undefined`) — a real regression this build introduced and
    // caught by journal.test.mjs's BUG-603 restore-fidelity deepEqual (an
    // explicit-undefined key and an absent key are NOT the same object to
    // Node's assert.deepStrictEqual). A concrete boolean default closes it.
    consolidatorUndoConsumed: false,
  };
}

/**
 * God-mode "Unlock all" price (FEAT-1972079899). PLACEHOLDER under the balance-number
 * regime — a deliberately large cash gate pending Aaron's balance sign-off; not a
 * derived/tuned value. Charged once by the `unlockAll` action to flip s.unlockedAll.
 * BUG-452 inc1 (2026-09-01): defined as a RATIO of STARTING_TREASURY (fiscal.ts) —
 * 0.5x, preserving the old £5,000,000-of-£10,000,000 relationship — rather than a
 * re-hardcoded absolute, so a future treasury retune keeps this gate affordable-but-
 * costly instead of silently exceeding the entire seed treasury (as a bare £5,000,000
 * literal now would against the rebased £1,500,000 starting funds).
 */
export const UNLOCK_ALL_COST = Math.round(STARTING_TREASURY * 0.5);

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
  // BUG-520 (remaining part): Business/Freight/Office Tax must count only
  // ONLINE buildings — a road-disconnected commercial/industrial/office/mine
  // building already pays zero upkeep (isOnline gate below), so it must also
  // pay zero tax. Mirrors the powerStats()/sumBy() gate exactly (c/c2/c3 below
  // all switch to the online-gated count for the same reason).
  const c = countByKindOnline(s);
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
    // BUG-569: stationLinks() only tests road-adjacency (the network-wiring
    // question); it never tests construction, so an under-construction
    // station shouldn't be excluded from `links.connectedIds` — but it must
    // still be excluded here, same as every other contribution site, before
    // it can earn Commuter Revenue.
    if (!isOnline(s, b)) continue;
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

  const c2 = countByKindOnline(s);
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
    // BUG-569: an under-construction / disconnected attraction shouldn't be
    // drawing tourists yet — mirror the isOnline gate used everywhere else.
    if (!isOnline(s, b)) continue;
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

  // FEAT-2326609711 inc1: Grid Import (external power cover). importedMW =
  // max(0, need - cap); import cost = importedMW * tariff. Only booked when
  // the toggle is ON (default GRID_IMPORT_ENABLED_DEFAULT — `?? ` fallback so
  // a legacy state predating this field reads as ON, AC-1) AND a real
  // shortage exists (mirrors Grid Export's "only when > 0" idiom, AC-2) — a
  // covered shortage is NOT a brownout (see the brownout-gating just below,
  // AC-1/AC-3): buying the shortfall in is the entire point of the toggle, so
  // the legacy income penalty is skipped while import covers it. Pushed to
  // `outflows` further down, once that array exists (AC-6: exactly once).
  const gridImportOn = s.gridImportEnabled ?? GRID_IMPORT_ENABLED_DEFAULT;
  const gridImportCost = gridImportOn
    ? gridImportCostPerTick(pw.cap, pw.need, GRID_IMPORT_TARIFF_PER_MW)
    : 0;

  // BUG-569: an under-construction harbour shouldn't grant the Freight Tax
  // boost yet — existence alone isn't enough, mirror the isOnline gate.
  const harbourBoost = s.buildings.some((b) => b.spec === 'land_harbour' && isOnline(s, b))
    ? 1.4
    : 1;

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
  // FEAT-wage-stage1 (Q100067/Q100086, 2026-09-03): the flat population-based
  // wagesPerTick() is REPLACED here by fiscal.ts's per-sector decomposition.
  // F1 FIX (independent round REJECT): the FIRST cut of this wiring fed
  // sectorWagesPerTick() raw job CAPACITY (totalJobsBySector(), vacancy-
  // inclusive) — a population-0 city with one big office tower was charged a
  // real wage for its empty desks. The correct basis is data.ts's
  // filledJobsBySector(), which caps capacity at the actual workforce
  // (population * WORKING_AGE_FRACTION) before apportioning by sector — only
  // jobs actually FILLED by a person get paid (see data.ts's own doc comment
  // for the full workers/filled/apportionment rule). The OUTFLOW LINE LABEL
  // and shape are kept IDENTICAL ('Wages', one aggregate value) — the doc's
  // Stage 1 does not call for a per-sector ledger split, so consistency.ts's
  // flows-vs-recompute checks and every existing fixture keep working
  // against one 'Wages' number, just now sourced differently. wagesPerTick()
  // itself is untouched (byte-identical signature/body) and stays exported
  // from fiscal.ts for its own tests / any grandfathered caller — this WAS
  // its only production call site, and that call site has moved.
  outflows.push({ label: 'Wages', value: sectorWagesPerTick(filledJobsBySector(s)).totalPerTick });
  // FEAT-2326609711 inc1 (AC-2/AC-6): the Grid Import outflow, computed above
  // — pushed exactly once, here, never a second side-channel debit.
  if (gridImportOn && gridImportCost > 0) {
    outflows.push({ label: GRID_IMPORT_OUTFLOW_LABEL, value: gridImportCost });
  }

  const c3 = countByKindOnline(s);
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
  //
  // FEAT-2326609711 inc1 (AC-1/AC-3, Design Ruling): while Grid Import is
  // ENABLED, a power shortfall is bought in from the regional grid instead of
  // browning out — the two penalty mechanisms are mutually exclusive so they
  // can never double-charge the same shortage (AC-3's false-pass warning).
  // Toggle OFF restores the legacy behaviour byte-for-byte (AC-3/AC-12): the
  // brownout branch below is untouched, gated only by the new toggle check.
  //
  // r2 fix (SSOT): gated on data.ts's isBrownoutActive(s) — the ONE place
  // the physical deficit and the toggle are combined — instead of a local
  // `brownout.active && !gridImportOn` recompute, so this can never drift
  // from the wellbeing/UI consumers of the same predicate (GR#3).
  const brownout = brownoutOf(s);
  if (isBrownoutActive(s)) {
    const poweredIncome = new Set(['Business Tax', 'Freight Tax', 'Office Tax']);
    for (const fl of inflows) {
      if (poweredIncome.has(fl.label)) fl.value = Math.round(fl.value * brownout.incomeFactor);
    }
  }

  // FEAT-congestion-teeth-2026-09-02 (AC-9, Q100057 A1 / Q100071 rec-on-all —
  // the spec's optional income drag is INCLUDED). Applied AFTER the brownout
  // block above (spec's explicit sequencing note) so the two penalties are
  // independently visible and never double-charge a single root cause: a
  // brownout-throttled Business Tax can ALSO be congestion-throttled, each by
  // its own factor, same as compounding any two independent multipliers.
  //
  // FORMULA CORRECTION (documented, not a silent deviation): the spec's prose
  // literally states `incomeFactor = 1.0 - congestionFactor * K`, but its own
  // congestionFactor convention (1.0 = no congestion, 0.0 = fully penalized —
  // see congestionFactorOf's doc, data.ts) makes that formula charge an
  // UNCONGESTED city the full 10% and a FULLY congested one nothing, the
  // exact inverse of the spec's own stated intent ("a fully congested city
  // loses ~10% of business income", Q6). Implemented to match the STATED
  // INTENT: the PENALTY scales with (1 - congestionFactor), i.e. with how far
  // below 1.0 the factor has dropped, so a fully congested sustained network
  // (congestionFactor -> 0) loses up to CONGESTION_INCOME_K and an
  // uncongested one (congestionFactor === 1, AC-4) loses nothing.
  const congestionFactor = congestionFactorOf(s);
  if (congestionFactor < 1) {
    const congestionIncomeFactor = 1 - (1 - congestionFactor) * CONGESTION_CONSTANTS.CONGESTION_INCOME_K;
    const poweredIncome = new Set(['Business Tax', 'Freight Tax', 'Office Tax']);
    for (const fl of inflows) {
      if (poweredIncome.has(fl.label)) fl.value = Math.round(fl.value * congestionIncomeFactor);
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
 * FEAT-milestone-cash-rewards-2026-09-02 (Q100047b ruling B1). Result of a
 * single newly-met milestone: the cash to queue plus the notice to show.
 * Mirrors LevelRewardResult's shape.
 */
export interface MilestoneRewardResult {
  totalReward: number;
  milestoneId: string;
  notice: MilestoneNotice;
}

/**
 * SINGLE SOURCE OF TRUTH: which of data.ts's MILESTONES are met by `s` but not
 * yet in `s.claimedMilestones` (already sanitized by the caller — GR#16). Pure
 * + deterministic (GR#21): iterates MILESTONES in catalogue order, no Date/
 * random. Returns one MilestoneRewardResult per newly-met milestone (usually
 * 0 or 1 per tick, but a savepoint/replay boundary can legitimately surface
 * several at once — e.g. an old save loaded already past several thresholds).
 * Called by engine.ts's advance() against the fully-assembled next-tick state
 * (population/history/funds all finalized), so m5 "Solvent City"'s
 * s.history.slice(-60) read sees THIS tick's own history entry.
 */
export function computeMilestoneRewards(s: SimState, claimedMilestones: string[]): MilestoneRewardResult[] {
  const results: MilestoneRewardResult[] = [];
  for (const m of MILESTONES) {
    if (claimedMilestones.includes(m.id)) continue;
    if (!m.test(s)) continue;
    const cash = Math.max(0, MILESTONE_REWARDS[m.id] ?? 0);
    results.push({
      totalReward: cash,
      milestoneId: m.id,
      notice: { id: m.id, label: m.label, cash },
    });
  }
  return results;
}

/**
 * BUG-600 (GR#16 type-safe storage boundaries / GR#3 single source of truth —
 * one shared helper for BOTH reward pending-queues, not two near-copies):
 * a hand-edited or legacy savepoint can hand back ANYTHING for
 * s.pendingRewards / s.pendingMilestoneRewards — not an array at all, an
 * array of junk objects, or a well-shaped element whose totalReward is
 * NaN/negative/non-integer. Left untreated, a non-array throws a TypeError
 * the moment advance()'s `for...of` drain loops touch it, and a NaN
 * totalReward flows straight into `funds`, silently breaking the tick-
 * boundary conservation invariant with no error surfaced (GR#1/#17 — this is
 * exactly the "silent failure" class those rules exist to close).
 *
 * The independent round's five corruptions — non-array, junk elements, a NaN
 * totalReward, a 1000-element flood, and dupes of already-paid ids — produced
 * IDENTICAL outcomes against both queues, so this one function closes both:
 * non-array collapses to `[]`, each element is validated in full via the
 * caller-supplied `isValid` predicate (which also re-checks totalReward, so a
 * partially-valid element can never slip through on a shared check alone),
 * junk elements are dropped rather than crashing the drain, and the queue is
 * capped so a flood can never grow the ledger/history/state unboundedly.
 */
const MAX_PENDING_REWARD_QUEUE = 200; // a real queue drains every tick and never legitimately holds more than a handful of entries

function sanitizePendingRewards<T extends { totalReward: number }>(
  v: unknown,
  isValid: (item: unknown) => item is T,
): T[] {
  if (!Array.isArray(v)) return [];
  const out: T[] = [];
  for (const item of v) {
    if (out.length >= MAX_PENDING_REWARD_QUEUE) break;
    if (isValid(item)) out.push(item);
  }
  return out;
}

/** Shared numeric gate for BOTH queues' totalReward field (BUG-600). */
function isValidRewardAmount(n: unknown): n is number {
  return typeof n === 'number' && Number.isFinite(n) && Number.isSafeInteger(n) && n >= 0;
}

function isPlainRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

/** BUG-600: validates one s.pendingRewards element (LevelRewardResult shape). */
function isValidLevelReward(item: unknown): item is LevelRewardResult {
  if (!isPlainRecord(item)) return false;
  if (!isValidRewardAmount(item.totalReward)) return false;
  if (typeof item.newLevel !== 'number' || !Number.isFinite(item.newLevel)) return false;
  const notice = item.notice;
  if (!isPlainRecord(notice)) return false;
  return (
    typeof notice.level === 'number' && Number.isFinite(notice.level) &&
    typeof notice.cash === 'number' && Number.isFinite(notice.cash) &&
    Array.isArray(notice.unlocked) && notice.unlocked.every((u) => typeof u === 'string')
  );
}

/** BUG-600: validates one s.pendingMilestoneRewards element (MilestoneRewardResult shape). */
function isValidMilestoneReward(item: unknown): item is MilestoneRewardResult {
  if (!isPlainRecord(item)) return false;
  if (!isValidRewardAmount(item.totalReward)) return false;
  if (typeof item.milestoneId !== 'string' || item.milestoneId.length === 0) return false;
  const notice = item.notice;
  if (!isPlainRecord(notice)) return false;
  return (
    typeof notice.id === 'string' &&
    typeof notice.label === 'string' &&
    typeof notice.cash === 'number' && Number.isFinite(notice.cash)
  );
}

/**
 * Drain and apply pending rewards, updating funds and lastRewardedLevel atomically.
 * Does NOT recompute; takes results verbatim from computeLevelRewards().
 * Called by advance() to apply queued rewards through flows.
 */
export function grantLevelRewards(s: SimState): SimState {
  // BUG-600: sanitize before touching .length/for...of — see sanitizePendingRewards doc above.
  const pendingRewards = sanitizePendingRewards(s.pendingRewards, isValidLevelReward);
  if (pendingRewards.length === 0) return s;
  let funds = s.funds;
  let lastRewardedLevel = s.lastRewardedLevel;
  let notice: LevelUpNotice | null = s.notice;
  for (const lr of pendingRewards) {
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
 * FEAT-2326609761 inc1 (AC-1/AC-34, ASM-1504): new cities (and every old
 * save with no `consolidatorEnabled` field) start with the consolidator OFF.
 * SSOT for the default — `SimState.consolidatorEnabled`'s doc comment
 * (types.ts), `rawState()`'s initializer, and the `toggleConsolidator`
 * reducer case all reference this ONE named constant rather than a bare
 * `false` literal repeated across files (GR#15). Lives here (not
 * consolidator.ts) to avoid a circular import — consolidator.ts already
 * imports TICKS_PER_MONTH/CONNECT_EXEMPT_KINDS FROM engine.ts, so engine.ts
 * must not import back from consolidator.ts.
 */
export const CONSOLIDATOR_ENABLED_DEFAULT = false;

/**
 * FEAT-2326609761 inc2 (Aaron's ruling, 2026-09-04): "GLIDE IS THE DEFAULT
 * MODE. When the consolidator is enabled it glides unless the player
 * switches mode." Lives here rather than consolidator.ts for the same
 * import-cycle reason as CONSOLIDATOR_ENABLED_DEFAULT immediately above —
 * consolidator.ts already imports FROM engine.ts, so engine.ts must not
 * import back.
 */
export const CONSOLIDATOR_MODE_DEFAULT: ConsolidatorMode = 'glide';

/**
 * FEAT-2326609761 inc2 (Aaron's ruling, 2026-09-03): "let's build a
 * consolidator tab where the player can enable it from say level 10" — a
 * progression-gated unlock, same idiom as every catalogue spec's own
 * `unlock` field (data.ts) and SpecialistsTab's `level < sp.unlock` check
 * (buildZoningTabs.tsx). A pure predicate so the tab, ConfigMenu, and any
 * future consumer share ONE gate rather than three copies of `>= 10`.
 */
export const CONSOLIDATOR_UNLOCK_LEVEL = 10;

export function consolidatorUnlockedAtLevel(level: number): boolean {
  return level >= CONSOLIDATOR_UNLOCK_LEVEL;
}

/**
 * FEAT-2326609761 inc2 (Aaron's ruling, 2026-09-03 addendum): "the
 * consolidator RUNS with the 800m default; the player can adjust the
 * section size larger or smaller." Duplicates consolidator.ts's
 * CONSOLIDATOR_SECTION_METRES VALUE (both must stay 800 — the values are the
 * single source of truth, not the symbol; mirrors this file's MAP_W/MAP_H
 * duplication precedent in consolidator.ts's own header) rather than
 * importing it, for the same import-cycle reason as CONSOLIDATOR_ENABLED_DEFAULT.
 * MIN/MAX bound the player's slider to the same real-measurement range
 * consolidator.ts's own doc comment table already reports for
 * CONSOLIDATOR_SECTION_METRES (200m..3200m, 4x4..64x64 tiles) — every size in
 * that table was actually measured against Aaron's real savepoint, so these
 * are not invented round numbers.
 */
export const CONSOLIDATOR_SECTION_METRES_DEFAULT = 800;
export const CONSOLIDATOR_SECTION_METRES_MIN = 200;
export const CONSOLIDATOR_SECTION_METRES_MAX = 3200;

/** Clamps a player-supplied section size (metres) into the sanctioned range. Never silently accepts an out-of-range value beyond clamping it — the reducer case below applies this to every 'setConsolidatorSectionMetres' dispatch. */
export function clampConsolidatorSectionMetres(metres: number): number {
  if (!Number.isFinite(metres)) return CONSOLIDATOR_SECTION_METRES_DEFAULT;
  return Math.min(CONSOLIDATOR_SECTION_METRES_MAX, Math.max(CONSOLIDATOR_SECTION_METRES_MIN, Math.round(metres)));
}

/**
 * FEAT-2326609761 inc2 (Aaron's ruling, 2026-09-03): "a simple set of
 * sliders which add up to 100%... a kept mixture" — the neutral default is
 * an even split across the four non-dwelling employment kinds.
 */
export const CONSOLIDATOR_SLIDERS_DEFAULT: ConsolidatorSliders = {
  office: 25,
  mining: 25,
  farming: 25,
  factory: 25,
};

/**
 * SSOT for "the sliders must sum to exactly 100 percent" (Aaron's ruling).
 * Every component is required to be a finite, non-negative number; the sum
 * is checked with a small epsilon to tolerate float accumulation from UI
 * arithmetic while still refusing a genuinely wrong mix (99 or 101). The
 * reducer (setConsolidatorSliders case, below) calls this and REFUSES the
 * action (returns state unchanged) rather than clamping/normalising a bad
 * dispatch — a silently "corrected" mix would misrepresent the player's own
 * chosen intent (types.ts's doc comment on SimState.consolidatorSliders).
 */
export function validateConsolidatorSliders(sliders: ConsolidatorSliders): boolean {
  const values = [sliders.office, sliders.mining, sliders.farming, sliders.factory];
  if (values.some((v) => typeof v !== 'number' || !Number.isFinite(v) || v < 0)) return false;
  const sum = values.reduce((a, b) => a + b, 0);
  return Math.abs(sum - 100) < 1e-6;
}

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
 * BUG-466: the aggregate utilization gating (residents/capacity or jobs/capacity,
 * identical for every monitored building) meant EVERY monitored non-maxed building
 * upgraded in the SAME monthly pass once the city crossed BUILDING_UTILIZATION_THRESHOLD
 * — e.g. 2000 buildings × ~£6,750 = £13.5M in one month, recurring every time population
 * regrew into the ceiling (a treadmill). This caps how many buildings may be queued for
 * upgrade in a single evaluateBuildingMonitors() pass, so the per-month charge is bounded
 * (MAX × ~£6,750, not count-of-city × ~£6,750) and remaining eligible buildings roll over
 * to later passes. The cap is applied over the EXISTING deterministic strict-buildingId
 * order, so the same buildings are picked every replay of the same input.
 * ⚠ PLACEHOLDER-balance — directional only, Aaron's pass.
 */
export const MAX_AUTO_SCALE_UPGRADES_PER_PASS = 25;

/**
 * BUG-466: minimum ticks a building must wait after auto-scaling before it is
 * eligible to auto-scale again. Without this, the treadmill re-fires on the SAME
 * buildings every time population regrows into the utilization ceiling. Paired with
 * MAX_AUTO_SCALE_UPGRADES_PER_PASS above. ⚠ PLACEHOLDER-balance — directional only,
 * Aaron's pass.
 */
export const AUTO_SCALE_COOLDOWN_TICKS = 2 * TICKS_PER_MONTH;

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
 * 3. For each: if building is online, not in cooldown (BUG-466), AND utilization ≥ 0.85
 *    threshold, upgrade capacityTier by 1, booking delta-cost
 *    (BUILDING_AUTO_SCALE_COST_FRACTION × base cost) — up to
 *    MAX_AUTO_SCALE_UPGRADES_PER_PASS per pass (BUG-466 rate limit).
 * Mirrors evaluateRoadMonitors pattern; each building scales at most once per pass.
 */
/** All tiles a w×h building at (x,y) occupies, as "x,y" keys. */
function buildingTileKeys(x: number, y: number, w: number, h: number): Set<string> {
  const out = new Set<string>();
  for (let i = 0; i < w; i++) for (let j = 0; j < h; j++) out.add(`${x + i},${y + j}`);
  return out;
}

/**
 * Same contract as data.ts's `fits()`, but checks against a SHARED (mutable,
 * cross-building) occupied set while excluding the building's OWN current
 * tiles (`selfTiles`) — the growth-specific "does this rect collide with
 * anything ELSE" check attemptScaleStep needs. F1 (independent round REJECT,
 * 2026-09-03): plain `fits(occupiedSet(s, id), ...)` only ever sees tiles
 * from the PRE-PASS `s` — it cannot see another building's OUT step from
 * EARLIER in the SAME monthly pass, so two neighbours can both claim the
 * same tile. Callers thread one `shared` set through the whole pass and
 * grow it after every successful OUT step (see evaluateBuildingMonitors).
 */
function fitsAgainstShared(
  shared: Set<string>,
  selfTiles: Set<string>,
  w: number,
  h: number,
  x: number,
  y: number
): boolean {
  for (let i = 0; i < w; i++)
    for (let j = 0; j < h; j++) {
      const key = `${x + i},${y + j}`;
      if (selfTiles.has(key)) continue; // the building's own current footprint — never a clash with itself
      if (shared.has(key)) return false;
    }
  return true;
}

/**
 * FEAT-2326609740 (Aaron Q100076, "A PLUS B" up-then-out): try to advance a
 * building EXACTLY ONE ladder index (`currentTier + 1`) this pass — never
 * more (F3, independent round REJECT, 2026-09-03: an earlier draft "skipped
 * forward" across ladder indices when the natural type was blocked, granting
 * TWO tiers of capacity for one charge). Each index has a "natural" mutation
 * type by alternating parity (odd = UP/height, even = OUT/footprint — §3.2);
 * a power-ladder spec (the NPP reactor ladder, Q100089=B) is height-EXEMPT,
 * so its natural type is always OUT. When the natural type is structurally
 * blocked, the SAME index's ALTERNATE type is tried instead (never a
 * different index) — this is the "up-only fallback" / height-cap fallback
 * from §3.5/§12/§13, just anchored to one index rather than hopping ahead.
 *
 * F4 (independent round REJECT, 2026-09-03): a `fits()` failure (the OUT
 * mutation) is ALWAYS transient — a neighbouring demolition can free the
 * tile later — so it NEVER locks the building, even when the alternate (UP)
 * is ALSO blocked by a permanent height cap. `locked` is therefore only ever
 * true when a successful step lands on the ladder's last index (nothing
 * left to climb, structurally, independent of height/footprint headroom).
 */
function attemptScaleStep(
  shared: Set<string>,
  building: Building,
  sp: Spec
): {
  advanced: boolean;
  locked: boolean;
  newTier: number;
  newHeight: number;
  newW: number;
  newH: number;
} {
  const tiers = sp.capacityTiers!;
  const isPowerLadder = sp.kind === 'power';
  const heightCap = isPowerLadder ? Infinity : heightCapOf(sp);
  const currentTier = building.capacityTier ?? 0;
  const height = building.heightStoreys ?? 1;
  // FOLLOW-UP (r3 round note (a), non-blocking): this re-implements
  // footprintOf(building, sp) inline rather than calling the SSOT — harmless
  // today (identical logic) but a future footprintOf change could drift from
  // this copy. Not fixed here (out of scope for the F5s-only re-round).
  const w = building.footprintW ?? sp.w;
  const h = building.footprintH ?? sp.h;
  // Caller (evaluateBuildingMonitors) already handles "already at the top of
  // the ladder" before calling this, so `candidate` is always a valid index.
  const candidate = currentTier + 1;
  const selfTiles = buildingTileKeys(building.x, building.y, w, h);

  const tryUp = (): { w: number; h: number; height: number } | null => {
    if (isPowerLadder) return null; // height-exempt — UP is never a valid mutation for a reactor ladder
    if (height >= heightCap) return null; // PERMANENT block — height can never decrease
    return { w, h, height: height + 1 };
  };
  const tryOut = (): { w: number; h: number; height: number } | null => {
    // Width-first, then height — deterministic, order-independent tiebreak (GR#21).
    if (building.x + w + 1 <= MAP_W && fitsAgainstShared(shared, selfTiles, w + 1, h, building.x, building.y)) {
      return { w: w + 1, h, height };
    }
    if (building.y + h + 1 <= MAP_H && fitsAgainstShared(shared, selfTiles, w, h + 1, building.x, building.y)) {
      return { w, h: h + 1, height };
    }
    return null; // TRANSIENT — a demolition could free space later; never locks (F4)
  };

  const naturalWantsUp = !isPowerLadder && candidate % 2 === 1;
  const natural = naturalWantsUp ? tryUp() : tryOut();
  const result = natural ?? (naturalWantsUp ? tryOut() : tryUp());

  if (!result) {
    // Neither this index's natural nor alternate mutation succeeded this
    // pass. Never locks here — a permanent height-cap block only rules out
    // the UP half; the OUT half staying blocked is always a `fits()`
    // failure (transient, F4). The monitor stays active and retries later.
    return { advanced: false, locked: false, newTier: currentTier, newHeight: height, newW: w, newH: h };
  }
  // Landing on the ladder's last index locks the building immediately —
  // structurally nothing is left to climb, independent of height/footprint.
  const locked = candidate >= tiers.length - 1;
  return { advanced: true, locked, newTier: candidate, newHeight: result.height, newW: result.w, newH: result.h };
}

export function evaluateBuildingMonitors(s: SimState, tick: number): BuildingScaleResult {
  // (1) expire — keep only monitors still inside their 1-year window
  const active = (s.buildingMonitors ?? []).filter((m) => tick <= m.until);
  // (2) strict buildingId order — deterministic, order-independent upgrades
  active.sort((a, b) => a.buildingId - b.buildingId);

  const byId = new Map<number, Building>();
  for (const b of s.buildings) {
    byId.set(b.id, b);
  }

  interface StepResult {
    tier: number;
    height: number;
    w: number;
    h: number;
    locked: boolean;
  }
  const stepById = new Map<number, StepResult>();
  const lockOnlyIds = new Set<number>(); // buildings locked with NO tier change (already-maxed / failed-permanently)
  const removeMonitorIds = new Set<number>();
  let cost = 0;
  let upgraded = 0;

  // F1 (independent round REJECT, 2026-09-03): ONE shared occupied-tile set,
  // cloned from occupiedSet(s) (never mutate the cached Set it returns) and
  // grown after every successful OUT step BEFORE the next monitor in this
  // SAME pass is evaluated — otherwise two buildings scaling out in the same
  // monthly pass can both see the pre-pass board and claim the same tile.
  const sharedOccupied = new Set(occupiedSet(s));

  // BUG-467 perf: residentsCapacity(s) and totalJobs(s) are each O(buildings)
  // (they sum over every building). Computing them INSIDE the per-monitor loop
  // below made the pass O(buildings^2) — ~36s of scripting per placement at
  // ~9,886 buildings (the residentsCapacity self-time bomb in the profile).
  // They do not change during this pass (tier upgrades are collected in
  // stepById and applied only AFTER the loop, so `s` is constant here),
  // so hoist them to a single O(n) computation each. FEAT-2326609740 adds
  // 'children'/'served' monitor types — their aggregates are hoisted the
  // same way for the same reason.
  const residentsCapForPass = residentsCapacity(s);
  const jobsCapForPass = totalJobs(s);
  const childrenCapForPass = totalChildrenCapacity(s);
  const servedCapForPass = totalServedCapacity(s);
  const powerStatsForPass = powerStats(s);

  for (const m of active) {
    const building = byId.get(m.buildingId);
    if (!building) continue; // building bulldozed — skip

    // Skip offline buildings (AC-11). Uses the SAME (pre-increment) `s` that every
    // other isOnline() call site in advance() uses this tick (computeFlows, the
    // population-growth capacity target below) — keeping the online view
    // consistent within one tick, matching the road-monitor pattern.
    if (!isOnline(s, building)) continue;

    if (stepById.has(building.id) || lockOnlyIds.has(building.id)) continue; // already resolved this pass

    // BUG-466: rate-limit — cap the number of buildings queued for upgrade THIS
    // pass so a saturated city can't lump-charge every monitored building in one
    // month. `active` is already in strict, stable buildingId order (sorted
    // above), so the cap always selects the SAME buildings across replays of the
    // same input (determinism, GR#21).
    if (stepById.size >= MAX_AUTO_SCALE_UPGRADES_PER_PASS) break;

    // BUG-466: per-building cooldown — a building that just auto-scaled cannot
    // auto-scale again until AUTO_SCALE_COOLDOWN_TICKS have passed. Without this,
    // as soon as population regrows into the capacity ceiling, utilization climbs
    // back over threshold and the SAME buildings re-upgrade every cycle (the
    // treadmill). Buildings with no `lastAutoScaleTick` (older saves/snapshots
    // predating this field) are treated as never having scaled, i.e. never in
    // cooldown — backward compatible.
    const lastAutoScaleTick = building.lastAutoScaleTick ?? -Infinity;
    if (tick - lastAutoScaleTick < AUTO_SCALE_COOLDOWN_TICKS) continue;

    const sp = SPECS[building.spec];
    if (!sp || !sp.capacityTiers) continue; // spec missing or not scalable

    const currentTier = building.capacityTier ?? 0;
    if (currentTier >= sp.capacityTiers.length - 1) {
      // FEAT-2326609740 §3.5/§14: already at the top of the ladder — lock it
      // and drop the monitor instead of leaving it inert until its window
      // expires (the old behaviour).
      lockOnlyIds.add(building.id);
      removeMonitorIds.add(building.id);
      continue;
    }

    // Compute utilization based on monitor type. 'residents'/'jobs' are the
    // original AC-7 basis; 'children'/'served'/'mw' (FEAT-2326609740 §11)
    // follow the SAME population-based-proxy style the original 'jobs' type
    // already used (comment below, unchanged) — directional, ⚠ placeholder.
    let utilization = 0;
    if (m.type === 'residents') {
      const totalCap = residentsCapForPass; // BUG-467: hoisted (was residentsCapacity(s) per-iteration = O(n^2))
      utilization = totalCap > 0 ? Math.min(1, s.population / totalCap) : 0;
    } else if (m.type === 'children') {
      const totalCap = childrenCapForPass;
      utilization = totalCap > 0 ? Math.min(1, s.population / totalCap) : 0; // population-based proxy, same style as 'jobs'
    } else if (m.type === 'served') {
      const totalCap = servedCapForPass;
      utilization = totalCap > 0 ? Math.min(1, s.population / totalCap) : 0; // population-based proxy, same style as 'jobs'
    } else if (m.type === 'mw') {
      const pw = powerStatsForPass;
      utilization = pw.cap > 0 ? Math.min(1, pw.need / pw.cap) : 0; // real need/cap ratio — power already tracks true demand
    } else {
      // jobs type
      const totalCap = jobsCapForPass; // BUG-467: hoisted (was totalJobs(s) per-iteration = O(n^2))
      utilization = totalCap > 0 ? Math.min(1, s.population / totalCap) : 0; // jobs utilization ~ population-based proxy
    }

    if (utilization < BUILDING_UTILIZATION_THRESHOLD) continue; // below threshold

    const step = attemptScaleStep(sharedOccupied, building, sp);
    if (!step.advanced) {
      if (step.locked) {
        lockOnlyIds.add(building.id);
        removeMonitorIds.add(building.id);
      }
      // Not advanced and not locked: a transient fits()-failure — no charge,
      // no tier change, monitor stays active for a future pass (§3.5).
      continue;
    }

    stepById.set(building.id, { tier: step.newTier, height: step.newHeight, w: step.newW, h: step.newH, locked: step.locked });
    if (step.locked) removeMonitorIds.add(building.id);
    // F1: if this step grew the footprint, claim the new tiles in the SHARED
    // set immediately — before the next monitor in this pass is evaluated —
    // so a later building in the same pass can never grow into a tile this
    // one just claimed (see attemptScaleStep's header comment).
    // FOLLOW-UP (r3 round note (a), non-blocking): another inline
    // footprintOf(building, sp) re-implementation — see the same note above
    // attemptScaleStep's `w`/`h`. Not fixed here (out of scope this round).
    const grewOut = step.newW !== (building.footprintW ?? sp.w) || step.newH !== (building.footprintH ?? sp.h);
    if (grewOut) {
      for (const t of buildingTileKeys(building.x, building.y, step.newW, step.newH)) sharedOccupied.add(t);
    }
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
    stepById.size === 0 && lockOnlyIds.size === 0
      ? s.buildings
      : s.buildings.map((b) => {
          const step = stepById.get(b.id);
          if (step) {
            return {
              ...b,
              capacityTier: step.tier,
              heightStoreys: step.height,
              footprintW: step.w,
              footprintH: step.h,
              lastAutoScaleTick: tick,
              scaleLocked: step.locked,
            };
          }
          if (lockOnlyIds.has(b.id)) return { ...b, scaleLocked: true };
          return b;
        });

  const monitors = removeMonitorIds.size === 0 ? active : active.filter((m) => !removeMonitorIds.has(m.buildingId));

  return { buildings, monitors, cost, upgraded };
}

// ════════════════════════════════════════════════════════════════════════════
// FEAT-2326609761 (CONSOLIDATOR) — MUTATION LANE. Consumes the READ-ONLY
// discovery/opportunity functions from consolidator.ts (a separate, parallel
// lane's module — see that file's own header for the scope split) and does
// the part that actually changes Aaron's map: demolish, build, lay a
// reconnecting road spur, charge/refund through the ledger, log it, and
// support Undo. Lives in engine.ts (not consolidator.ts) because every
// primitive it needs to MUTATE state — autoConnect, fits, occupiedSet,
// footprintOf, logEvent, computeRoadConnectivity, INSOLVENCY_WARNING_THRESHOLD
// — already lives here natively; consolidator.ts stays a pure read-only leaf.
//
// Aaron's rulings this section implements (BOW FEAT-2326609761, 2026-09-03):
//   R1 fully automatic — applyConsolidatorPass runs unconditionally at the
//     monthly boundary once consolidatorEnabled; no propose/approve step.
//   R2 pay-build/get-scrap — CONSOLIDATOR_SCRAP_FRACTION (data.ts, derived
//     from BULLDOZE_REFUND_FRACTION, GR#15).
//   Ruling 7 stranded-capacity — reconnection opportunities are always
//     applied BEFORE density-consolidation opportunities in a pass (see the
//     ordering in applyConsolidatorPass below): "recovering 20,000 existing
//     dwellings beats building new ones and costs a fraction".
// ════════════════════════════════════════════════════════════════════════════

/** PLACEHOLDER-balance (mirrors MAX_AUTO_SCALE_UPGRADES_PER_PASS's role, engine.ts:966): the number of TRANSACTIONS (not sections/candidates) a single pass may apply. Candidates beyond this are recorded in `skipped` with reason 'action budget' (AC-38), never silently dropped. */
const CONSOLIDATOR_MAX_TRANSACTIONS_PER_PASS = 4;

/** PLACEHOLDER-balance: no single successor may end a transaction carrying more than this share of the city's online capacity in its own family (CEIL-3) — the ceiling that stops "one XXL nuke for the whole city". */
const CONSOLIDATOR_MAX_FAMILY_SHARE = 0.5;

/**
 * Ring-buffer cap for SimState.consolidatorLog, mirroring LEDGER_CAP's role
 * for the ledger. WIDENED 20 -> 32 for FEAT-2326609761 inc2 (Aaron's ask:
 * "either widen the ring or document [that glide-mode undo only covers the
 * last 20 daily windows]"). JUDGEMENT: widen, for a low cost. Glide mode can
 * push up to one entry per game DAY (30/month) plus the month-12 whole-map
 * pass on top (31 possible pushes in that one month) where the legacy
 * monthly-twelfth cadence pushed at most 1/month — a 20-deep ring would
 * already be exhausted (and start silently dropping the OLDEST entries,
 * which is fine for display/audit history but would mean "undo" — which
 * ALWAYS targets only `consolidatorLog[0]`, the single most recent pass, see
 * undoLastConsolidatorPass below — stays correct regardless of ring depth;
 * only the LOG's visible history depth is affected) partway through a single
 * glide sweep of a busy month. 32 covers a full 30-day month's worth of
 * daily entries PLUS the month-12 whole-map entry with 1 to spare, so a
 * player reviewing "what did the consolidator do this month" in the tab
 * never sees a month-boundary gap. Cost is negligible: each ConsolidationPass
 * entry holds at most CONSOLIDATOR_MAX_TRANSACTIONS_PER_PASS (4) small
 * transaction records, so 32 entries is a few hundred small objects, not a
 * memory concern (LEDGER_CAP is 200 for comparison). Single-level undo
 * itself is UNAFFECTED by this change either way (see the doc comment above)
 * — this only widens how much PAST history is visible/kept.
 */
export const CONSOLIDATOR_LOG_CAP = 32;

/**
 * FEAT-2326609761 inc2 (Aaron's glide-mode ruling, 2026-09-04): which FIXED
 * audit-grid sections (consolidator.ts's sectionKeyOf/sectionIndexOf — the
 * proven, 800m-ruling-derived discovery grain, UNCHANGED by this increment)
 * today's glide window overlaps. Deliberately does NOT make sectionIndexOf's
 * own grid player-adjustable — the window's raw pixel/tile position and
 * on-screen box size scale with the player's chosen section size
 * (consolidatorSectionMetres), but the underlying discovery/opportunity
 * grain the consolidator actually reasons about stays the ladder-derived
 * 800m default; see consolidatorGlide.ts's own file header for why this
 * split is safe (a continuous SLIDING window is a genuinely different
 * concept from the fixed section PARTITION, and re-deriving sectionIndexOf's
 * whole grid dynamically would be a much larger, separately-scoped change).
 * A window this size (>= 1 fixed section wide/tall) overlaps at most 4 fixed
 * sections (when straddling both a column and a row boundary simultaneously)
 * — deduped via a Set, so applyConsolidatorPass never processes the same
 * fixed section twice in one day.
 */
function sectionKeysForGlideWindow(s: SimState, tick: number): number[] {
  const win = glideWindowForDay(tick, sectionTilesOf(s));
  const keys = new Set<number>([
    sectionKeyOf(win.x0, win.y0),
    sectionKeyOf(win.x0 + win.w - 1, win.y0),
    sectionKeyOf(win.x0, win.y0 + win.h - 1),
    sectionKeyOf(win.x0 + win.w - 1, win.y0 + win.h - 1),
  ]);
  return Array.from(keys);
}

function toConsolidationRecord(b: Building, placedByDefault: 'player' | 'auto'): ConsolidationRecord {
  return {
    id: b.id,
    spec: b.spec,
    x: b.x,
    y: b.y,
    ...(b.capacityTier != null ? { capacityTier: b.capacityTier } : {}),
    ...(b.builtTick != null ? { builtTick: b.builtTick } : {}),
    placedBy: b.placedBy ?? placedByDefault,
  };
}

function recordToBuilding(r: ConsolidationRecord): Building {
  return {
    id: r.id,
    spec: r.spec,
    x: r.x,
    y: r.y,
    ...(r.capacityTier != null ? { capacityTier: r.capacityTier } : {}),
    ...(r.builtTick != null ? { builtTick: r.builtTick } : {}),
    ...(r.placedBy != null ? { placedBy: r.placedBy } : {}),
  };
}

/**
 * The city-wide online capacity for one consolidation family (CEIL-3's
 * denominator) — an O(sections) fold over the ALREADY-BUILT sectionIndexOf
 * index (AC-37: never a fresh O(buildings) walk here; sectionIndexOf's own
 * single fold already aggregated capacityByFamily per section).
 */
function cityFamilyCapacity(index: Map<number, SectionAudit>, familyKey: string): number {
  let total = 0;
  for (const audit of index.values()) {
    total += audit.capacityByFamily[familyKey] ?? 0;
  }
  return total;
}

/**
 * The 8-neighbour section keys of `key`, bounds-checked against the SAME
 * SECTIONS_X/SECTIONS_Y grid consolidator.ts's own sectionIndexOf pass-2
 * neighbour walk uses (imported, not re-derived — GR#3).
 */
function sectionNeighbourKeysOf(key: number): number[] {
  const sx = key % SECTIONS_X;
  const sy = Math.floor(key / SECTIONS_X);
  const out: number[] = [];
  for (let dy = -1; dy <= 1; dy++) {
    for (let dx = -1; dx <= 1; dx++) {
      if (dx === 0 && dy === 0) continue;
      const nx = sx + dx;
      const ny = sy + dy;
      if (nx < 0 || ny < 0 || nx >= SECTIONS_X || ny >= SECTIONS_Y) continue;
      out.push(ny * SECTIONS_X + nx);
    }
  }
  return out;
}

/**
 * One consolidator pass — the monthly-twelfth rotation's own monthly
 * cadence (AC-6/ruling 7, AC-12's one-transaction-per-section, AC-17's
 * atomic validate-then-mutate) OR, per FEAT-2326609761 inc2's GLIDE MODE
 * (Aaron's 2026-09-04 ruling, DEFAULT mode), one daily call scoped to the
 * glide window's fixed-grid sections via `sectionKeysOverride`. Called from
 * advance() — monthly-twelfth callers omit `sectionKeysOverride` and get the
 * EXACT pre-inc2 behaviour (monthlyScopeOf(tick).sectionKeys); the glide
 * caller passes the small (1-4) fixed-section-key set the day's glide window
 * overlaps (consolidator.ts's sectionKeysForGlideWindow) instead. Pure
 * function of (s, tick, sectionKeysOverride) — no Date/Math.random/
 * localStorage (GR#21); every ordering decision reads consolidator.ts's own
 * deterministic sorts.
 *
 * PERF (FEAT-2326609761 inc2, the whole reason glide mode exists): the
 * legacy monthly cadence's own disclosed ~1.5-2.1s stall came overwhelmingly
 * from sectionIndexOf's full O(buildings) audit fold running fresh on every
 * call — `memoOnState`, keyed on the whole SimState object, MISSED every
 * single tick (a new object every tick) even though `s.buildings` itself is
 * usually unchanged day-to-day. consolidator.ts's sectionIndexOf now caches
 * on `s.buildings`' own identity (tick-validity-checked against the G1
 * construction gate — see its doc comment), and findOpportunities' internal
 * id lookup is the same shared, buildings-identity-cached `buildingByIdOf`
 * (data.ts) — so a glide day that finds nothing to commit (the common case,
 * since only 1-4 sections are in scope) pays O(1) for both, not O(buildings)
 * — see the FEAT-2326609761 inc2 build report for the measured number on
 * Aaron's real 49k save.
 *
 * Returns the resulting state (buildings/funds/nextId/cumulativeCapexSpent/
 * roadConnectivity already reflect every applied transaction) plus a log
 * entry (or null if the pass found nothing to do and nothing to report).
 * The CALLER (advance()) is responsible for re-booking the funds delta
 * through the normal inflow/outflow + one-ledger-row path (mirrors how
 * autoScaleCost/orphanConnectCost are handled) rather than trusting this
 * function's own `funds` field for the tick's conservation bookkeeping —
 * see advance()'s consolidator block below for why.
 */
function applyConsolidatorPass(
  s: SimState,
  tick: number,
  sectionKeysOverride?: readonly number[],
): { state: SimState; passLog: ConsolidationPass | null } {
  const sectionKeys = sectionKeysOverride ?? monthlyScopeOf(tick).sectionKeys;
  const sliders = s.consolidatorSliders ?? CONSOLIDATOR_SLIDERS_DEFAULT;
  const reconnectOpps = findReconnectionOpportunities(s, sectionKeys);
  const consolidateOpps = findOpportunities(s, sectionKeys, sliders);

  let cur = s;
  // FEAT-2326609761 inc2 (glide-mode perf): seeded from the shared,
  // buildings-identity-cached lookup (data.ts's buildingByIdOf) — a fresh,
  // independently-mutable COPY (this Map is `.set`/`.delete`d as
  // transactions commit below, so it must never be the SAME object the
  // cache hands out to other callers), but the copy itself is a cache HIT
  // when `findOpportunities`/`findReconnectionOpportunities` already built
  // the same lookup moments earlier in THIS pass (`cur === s` here, before
  // any commit) rather than a second independent O(buildings) fold.
  const byId = new Map(buildingByIdOf(cur.buildings));
  const transactions: ConsolidationTransaction[] = [];
  const skipped: Array<{ sectionKey: number; reason: string }> = [];
  const sectionsDone = new Set<number>();

  const overBudget = () => transactions.length >= CONSOLIDATOR_MAX_TRANSACTIONS_PER_PASS;

  // F5 FIX (independent round finding, perf): hoisted ONCE for the whole
  // reconnect phase rather than re-derived via sectionIndexOf(cur) inside
  // the loop below (a full O(buildings) rebuild every time `cur` changes,
  // i.e. once per committed reconnect transaction) — mirrors the density
  // phase's `index` below. `cur === s` here (no commits have happened yet),
  // so this is a cache HIT against findReconnectionOpportunities'/
  // findOpportunities' own internal sectionIndexOf(s) call, not a fresh
  // walk. Section membership (`buildingIds`) can go stale only for a LATER
  // candidate's NEIGHBOUR section after an EARLIER commit in an adjacent
  // section within the SAME pass (accepted per the round's own "hoist
  // across transactions" instruction) — the actual "is it online right now"
  // read is always live against `cur`, never cached.
  const reconnectIndex = sectionIndexOf(cur);

  // F5 FIX (independent round finding, perf): `occupied`/`roads` hoisted for
  // the WHOLE reconnect phase, not re-cloned via `new Set(occupiedSet(cur))`
  // once per OPPORTUNITY. At scale (49,174 buildings) `occupiedSet(cur)` is
  // a Set proportional to occupied TILES, not buildings — cloning it for
  // every one of up to a few dozen reconnect candidates (even ones that end
  // up doing nothing) was the dominant unaccounted cost the round measured
  // (+2,544ms/pass). Re-seeded only when `cur` actually changes (a
  // committed transaction), mirroring the density phase's `runningOccupied`.
  let reconnectOccupied = new Set(occupiedSet(cur));
  let reconnectRoads = new Set(roadTileSetOf(cur));

  // ---- Ruling 7: reconnection ranks ABOVE density consolidation ----------
  for (const opp of reconnectOpps) {
    if (overBudget()) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'action budget' });
      continue;
    }
    if (sectionsDone.has(opp.sectionKey)) continue; // AC-12: one transaction/section/pass
    if (cur.administrationState) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'administration' });
      continue;
    }

    const audit = reconnectIndex.get(opp.sectionKey);
    if (!audit) continue;
    // F5 FIX (independent round finding, perf): the candidate residential
    // ids THIS section holds, bounded to this one section's own
    // buildingIds (AC-37) — WITHOUT an isOnline(cur, b) pre-filter. isOnline
    // is backed by onlineByBuilding(cur), a memoOnState fold that computes
    // EVERY building's status in one O(buildings) pass the first time
    // anything calls it for `cur` — a completely SEPARATE cache from
    // sectionIndexOf's own (already-paid-for) classification, so this
    // filter used to force a SECOND full O(buildings) walk purely to
    // re-confirm what `reconnectOpps` already proved true for the section
    // as a whole (findReconnectionOpportunities only returns sections with
    // actionable stranded capacity > 0). autoConnect() itself is the
    // correct, CHEAP (O(footprint), via planConnector's own geometry) place
    // to discover "already connected" — its `if (plan.connected) return
    // unchanged` branch makes calling it on an already-online building a
    // safe, near-free no-op, so skipping the pre-filter costs nothing but a
    // few wasted calls in the rare case another transaction already fixed
    // this same section's buildings earlier in this pass.
    const strandedIds = audit.buildingIds.filter((id) => {
      const b = byId.get(id);
      return !!b && SPECS[b.spec]?.kind === 'residential';
    });
    if (strandedIds.length === 0) continue; // section holds no residential candidate at all

    const preFunds = cur.funds;
    let attempt = cur;
    let anyConnected = false;
    const addedRecords: ConsolidationRecord[] = [];
    // Rollback list for the SHARED reconnectOccupied/reconnectRoads sets
    // (see their declaration above): since they are now mutated IN PLACE
    // across the whole reconnect phase (not cloned fresh per opportunity),
    // a candidate that ends up REJECTED after partially laying tiles must
    // undo exactly those additions, or the next opportunity would see
    // phantom occupied/road tiles that were never actually committed to
    // `cur`.
    const tentativeKeys: string[] = [];
    // BUG-642-class fix: autoConnect(), given no prebuiltBoard, rebuilds its
    // OWN occupied/road tile Sets from scratch (a full O(buildings) walk) on
    // EVERY call — and this loop can call it once per stranded building in
    // the section. The hoisted `reconnectOccupied`/`reconnectRoads` (shared
    // across the WHOLE reconnect phase, not re-cloned per opportunity — see
    // their declaration above) are threaded straight through and mutated
    // in place as tiles are added, mirroring how the 'place' reducer case
    // and sweepOrphanConnects already avoid this cost.
    for (const id of strandedIds) {
      const b = byId.get(id)!;
      const sp = SPECS[b.spec];
      if (!sp) continue;
      const beforeAttemptFunds = attempt.funds;
      const lengthBefore = attempt.buildings.length;
      attempt = autoConnect(attempt, b, sp, { notice: false }, { occupied: reconnectOccupied, roads: reconnectRoads });
      if (attempt.buildings.length > lengthBefore) {
        // autoConnect only ever APPENDS new tiles at the tail (never
        // reorders/removes) — the tail slice is exactly what this call
        // added, with no O(buildings) search needed to find it.
        for (const nb of attempt.buildings.slice(lengthBefore)) {
          addedRecords.push(toConsolidationRecord(nb, 'auto'));
          const nsp = SPECS[nb.spec];
          if (nsp) {
            for (let dx = 0; dx < nsp.w; dx++) {
              for (let dy = 0; dy < nsp.h; dy++) {
                const key = `${nb.x + dx},${nb.y + dy}`;
                reconnectOccupied.add(key);
                reconnectRoads.add(key); // every appended tile here is a road/connector spec
                tentativeKeys.push(key);
              }
            }
          }
        }
      }
      if (attempt.funds !== beforeAttemptFunds || attempt.buildings.length !== lengthBefore) {
        anyConnected = true;
      }
      // AC-23: never spend a background process through the insolvency floor.
      if (attempt.funds < INSOLVENCY_WARNING_THRESHOLD) break;
    }

    const spend = preFunds - attempt.funds;
    if (!anyConnected || spend <= 0) {
      // Nothing actually laid (e.g. planConnector found no route) —
      // tentativeKeys is empty in this path (no tiles were ever added), so
      // there is nothing to roll back.
      continue; // not a chargeable transaction.
    }
    if (attempt.funds < INSOLVENCY_WARNING_THRESHOLD) {
      for (const key of tentativeKeys) {
        reconnectOccupied.delete(key);
        reconnectRoads.delete(key);
      }
      skipped.push({ sectionKey: opp.sectionKey, reason: 'insufficient funds' });
      continue; // revert — `cur` is untouched since we mutated only the local `attempt`.
    }

    cur = attempt;
    // Rebuild byId only with the delta (cheap — bounded to this section's work).
    for (const rec of addedRecords) byId.set(rec.id, recordToBuilding(rec));
    sectionsDone.add(opp.sectionKey);
    transactions.push({
      sectionKey: opp.sectionKey,
      kind: 'reconnect',
      removed: [],
      added: addedRecords,
      buildCost: spend,
      scrapRecovered: 0,
      netCost: spend,
    });
  }

  // BUG-642-class fix: autoConnect (called per stranded building above) can
  // lay new road tiles WITHOUT this function ever recomputing
  // cur.roadConnectivity — unlike the reducer's 'place' path, this pass runs
  // INSIDE advance(), bypassing the reducer() wrapper that normally does that
  // recompute. Without this, every isOnline/sectionIndexOf call in the
  // density phase below would read a STALE connectivity graph (missing any
  // spur just laid), corrupting the AC-19 stranding check and the CEIL-3
  // family-capacity audit alike. ONE recompute for the whole reconnect
  // phase, not per-candidate.
  if (transactions.some((t) => t.kind === 'reconnect')) {
    cur = { ...cur, roadConnectivity: computeRoadConnectivity(cur) };
  }

  // ---- Density consolidation (the ladder) --------------------------------
  // `index`/`occupiedBoard` are HOISTED across candidates and only
  // refreshed when `cur` actually changes (a transaction committed) — the
  // BUG-642 idiom applied to this loop. Before this fix, `occupiedSet` was
  // rebuilt from scratch (`occupiedSet({...cur, buildings: filtered})`, a
  // FRESH array reference every candidate, guaranteeing a cache miss) once
  // per candidate opportunity, and `computeRoadConnectivity` — an O(road
  // tiles) BFS — ran unconditionally per candidate that reached the site
  // search, together adding ~1.3s to a single boundary tick on Aaron's
  // 29,831-building save (median 543ms -> 1,849ms, measured). Both are now
  // O(1) amortised per candidate in the common case.
  const index = sectionIndexOf(cur);
  // F5 FIX (independent round finding, perf): both hoisted ONCE for the
  // whole density phase and maintained INCREMENTALLY as transactions commit,
  // instead of being re-derived from `cur` (whose reference changes on every
  // commit, forcing a fresh O(buildings) rebuild each time) after every one
  // of up to CONSOLIDATOR_MAX_TRANSACTIONS_PER_PASS commits. See the
  // per-commit update sites below.
  const familyCapacityDelta = new Map<string, number>();
  const runningOccupied = new Set(occupiedSet(cur));
  for (const opp of consolidateOpps) {
    if (overBudget()) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'action budget' });
      continue;
    }
    if (sectionsDone.has(opp.sectionKey)) continue;
    if (cur.administrationState) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'administration' });
      continue;
    }

    const fromSpec = SPECS[opp.fromSpec];
    const toSpec = SPECS[opp.toSpec];
    if (!fromSpec || !toSpec) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'no successor' });
      continue;
    }
    // AC-8 rule 6 / Q100105 (Aaron: "obey the unlock ladder" — a background
    // regenerator must never hand the player something they have not
    // unlocked). Deliberately re-checked HERE, not trusted from the
    // read-only opportunity list — consolidator.ts's isConsolidationSuccessor
    // omits this on purpose (its own doc comment: "unlock/cap gating is
    // re-applied by the (separate) apply lane before anything is actually
    // built" — this IS that apply lane).
    if (!specUnlocked(cur, toSpec)) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'not unlocked' });
      continue;
    }
    // CEIL-4 / FEAT-2326609761 inc1a (maxPerCity, landed on main after this
    // lane was branched — Aaron: "limit Five Gorges Dams to just one",
    // "unplaceable... by the consolidator alike"). `fromSpec !== toSpec`
    // always holds here (AC-8 rule 1: a.id !== b.id in isConsolidationSuccessor),
    // so demolishing the group never changes toSpec's own count — a plain
    // remainingAllowance(cur, toSpec) read is correct and sufficient; no
    // batch-smuggling risk (unlike stampRegion/placeMany) because this loop
    // applies at most one successor of a given spec per section per pass.
    if (remainingAllowance(cur, toSpec) <= 0) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'one per city' });
      continue;
    }

    const group = opp.buildingIds.map((id) => byId.get(id)).filter((b): b is Building => !!b);
    if (group.length < opp.groupCount) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'group changed' });
      continue;
    }

    // AC-20 protected classes: under construction, or an auto-scale cooldown
    // in effect (do not fight the auto-scaler).
    let protectedHit: string | null = null;
    for (const b of group) {
      if (cur.tick - (b.builtTick ?? 0) < constructionTicks(fromSpec)) {
        protectedHit = 'under construction';
        break;
      }
      if (b.lastAutoScaleTick != null && cur.tick - b.lastAutoScaleTick < AUTO_SCALE_COOLDOWN_TICKS) {
        protectedHit = 'auto-scale cooldown';
        break;
      }
    }
    if (protectedHit) {
      skipped.push({ sectionKey: opp.sectionKey, reason: protectedHit });
      continue;
    }

    // CEIL-3: family-share ceiling, derived from the CURRENT audited state,
    // compared AFTER the transaction — family total with the GROUP's own
    // capacity removed and the successor's added back — not the raw BEFORE
    // total, which would double-count the group as if it still existed
    // ALONGSIDE its own successor. A city whose ENTIRE family capacity lives
    // in the group being replaced correctly still fails this check (the
    // successor then legitimately holds 100% of the family — exactly the
    // single-point-of-failure CEIL-3 exists to prevent) — the fix versus the
    // naive before-total comparison only removes the double count, it does
    // not exempt the sole-provider case, which is a genuine, intended block.
    const familyKey = familyKeyOf(toSpec);
    const familyTotalBefore = cityFamilyCapacity(index, familyKey) + (familyCapacityDelta.get(familyKey) ?? 0);
    const successorCapacity = capacityOf(toSpec);
    const groupCapacityApprox = capacityOf(fromSpec) * opp.groupCount;
    const familyTotalAfter = Math.max(0, familyTotalBefore - groupCapacityApprox) + successorCapacity;
    if (successorCapacity > CONSOLIDATOR_MAX_FAMILY_SHARE * familyTotalAfter) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'family share ceiling' });
      continue;
    }

    // Site the successor on the freed tiles (ASM-1495): the lowest (y, x)
    // origin inside the section where it fits, after the group is removed.
    // BUG-642 idiom: derive the after-demolish occupied set INCREMENTALLY
    // from occupiedSet(cur) (memoOnState-cached and shared across every
    // candidate sharing this `cur` — a cache HIT here unless a transaction
    // just committed) by deleting only the group's own footprint tiles
    // (bounded by group size, typically single digits), instead of
    // rebuilding the whole board via occupiedSet({...cur, buildings:
    // filtered}) — a FRESH buildings array reference on every call, which
    // guarantees a cache MISS and a full O(buildings) rebuild each time.
    const groupIds = new Set(group.map((b) => b.id));
    const afterDemolishOccupied = new Set(runningOccupied);
    for (const b of group) {
      const bs = SPECS[b.spec];
      if (!bs) continue;
      const { w, h } = footprintOf(b, bs);
      for (let dx = 0; dx < w; dx++) {
        for (let dy = 0; dy < h; dy++) afterDemolishOccupied.delete(`${b.x + dx},${b.y + dy}`);
      }
    }
    const origin = sectionOriginOf(opp.sectionKey);
    let site: { x: number; y: number } | null = null;
    for (let y = origin.y0; y <= origin.y0 + origin.h - toSpec.h && !site; y++) {
      for (let x = origin.x0; x <= origin.x0 + origin.w - toSpec.w && !site; x++) {
        if (fits(afterDemolishOccupied, toSpec.w, toSpec.h, x, y)) site = { x, y };
      }
    }
    if (!site) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'no site' });
      continue;
    }

    const buildCost = placementCost(toSpec);
    const scrapRecovered = group.reduce(
      (sum, b) => sum + Math.round(placementCost(SPECS[b.spec]) * CONSOLIDATOR_SCRAP_FRACTION),
      0,
    );
    const netCost = buildCost - scrapRecovered;
    if (cur.funds < netCost) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'insufficient funds' });
      continue;
    }
    if (cur.funds - netCost < INSOLVENCY_WARNING_THRESHOLD) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'insufficient funds' });
      continue;
    }

    // AC-19 before-snapshot: this section + its 8 neighbours' buildings
    // (bounded — never the whole city) is the neighbourhood whose online
    // status must not regress.
    const neighbourhoodIds: number[] = [];
    const neighbourAudits = [index.get(opp.sectionKey), ...sectionNeighbourKeysOf(opp.sectionKey).map((k) => index.get(k))];
    for (const a of neighbourAudits) {
      if (a) neighbourhoodIds.push(...a.buildingIds);
    }
    const onlineBefore = new Map<number, boolean>();
    for (const id of neighbourhoodIds) {
      const b = byId.get(id);
      if (b) onlineBefore.set(id, isOnline(cur, b));
    }

    const successorBuilding: Building = {
      id: cur.nextId,
      spec: toSpec.id,
      x: site.x,
      y: site.y,
      builtTick: cur.tick,
      placedBy: 'auto',
    };
    // The O(buildings) filter is deferred to HERE — only candidates that
    // survive every cheaper gate (unlock/cap/protection/family-share/site/
    // funds) above ever pay it, not every candidate the ladder considers.
    const afterDemolishBuildings = cur.buildings.filter((b) => !groupIds.has(b.id));
    const beforeConnectLen = afterDemolishBuildings.length; // successorBuilding's own index, below
    const preAutoConnectFunds = cur.funds;
    let attempt: SimState = {
      ...cur,
      funds: cur.funds - netCost,
      cumulativeCapexSpent: (cur.cumulativeCapexSpent ?? 0) + buildCost,
      nextId: cur.nextId + 1,
      buildings: [...afterDemolishBuildings, successorBuilding],
    };
    // Same prebuiltBoard discipline as 'place' (engine.ts) and the reconnect
    // loop above: `afterDemolishOccupied` already reflects the group's
    // removal (computed above, incrementally); adding the successor's own
    // footprint is O(successor tiles), not O(buildings). Roads are
    // untouched by this transaction's demolish+build (the group is never a
    // road spec — AC-20), so roadTileSetOf(cur) (memoOnState-cached) is
    // reused as-is.
    const occupiedForConnect = new Set(afterDemolishOccupied);
    for (let dx = 0; dx < toSpec.w; dx++) {
      for (let dy = 0; dy < toSpec.h; dy++) occupiedForConnect.add(`${site.x + dx},${site.y + dy}`);
    }
    // F2 FIX, part A (independent round finding, "the city-breaker" root
    // cause): autoConnect's OWN affordability gate is `if (s.funds <
    // totalCost) return unaffordable, state UNCHANGED` — a narrow, purely
    // LOCAL check with no idea a background pass may still have headroom
    // down to the city-wide insolvency floor. That gate exists to protect a
    // PLAYER's direct 'place' action (never spend what you do not have,
    // full stop); the consolidator's own gates already allow spending DOWN
    // TO the floor (ASM-1501), so a connector that would have succeeded
    // within that floor must not be refused by autoConnect's stricter
    // per-call view. Fix: temporarily grant it the FULL remaining headroom
    // to the floor (never touching autoConnect's own logic, a shared
    // function 'place' also calls, whose player-facing non-negative
    // guarantee stays intact for every OTHER caller), let it plan and lay
    // the connector against that, then reconcile the REAL spend against the
    // true funds baseline below — this can never let the transaction commit
    // for more than the floor allows, because the reconciled total is
    // provably >= INSOLVENCY_WARNING_THRESHOLD by construction (the boost
    // itself is capped there).
    const fundsBeforeConnect = attempt.funds; // cur.funds - netCost, captured before autoConnect touches it
    const floorHeadroom = fundsBeforeConnect - INSOLVENCY_WARNING_THRESHOLD; // always >= 0: the cheap pre-filter above already proved cur.funds >= netCost
    attempt = autoConnect(
      { ...attempt, funds: floorHeadroom },
      successorBuilding,
      toSpec,
      { notice: false },
      { occupied: occupiedForConnect, roads: roadTileSetOf(cur) },
    );
    const connectSpend = floorHeadroom - attempt.funds; // whatever autoConnect actually spent, 0 if it refused/was already connected
    attempt = { ...attempt, funds: fundsBeforeConnect - connectSpend };

    // F1 FIX (independent round finding): bill the REAL spend, not just
    // placementCost(successor). autoConnect may have laid a connector (or
    // upgraded a junction) INSIDE this same transaction, and its cost was
    // silently written off — advance() reverts funds to preFunds and
    // re-books only sum(txn.buildCost), so a connector autoConnect paid for
    // out of `attempt.funds` never survives that revert. Mirror the
    // reconnect loop's own (already-correct) idiom above: measure the
    // ACTUAL funds delta across the whole transaction, including autoConnect
    // — that delta, not the pre-computed estimate, is what actually left the
    // treasury and what `buildCost`/`netCost` must report.
    const actualTotalSpend = preAutoConnectFunds - attempt.funds; // netCost + any connector/upgrade cost
    const realBuildCost = actualTotalSpend + scrapRecovered;
    const realNetCost = actualTotalSpend;

    // BUG-642 idiom (mirrors case 'place'/roadTopologyMayHaveChanged): the
    // demolished group can NEVER be a road/trunk spec (AC-20 excludes
    // CONNECT_EXEMPT_KINDS from the ladder entirely), so the ONLY way this
    // transaction can have touched the road graph is if autoConnect laid
    // extra connector/junction tiles beyond the successor itself. Skipping
    // the O(road tiles) BFS in the (common) already-adjacent case is the
    // other half of the fix for the 1.3s-per-pass regression measured on
    // Aaron's real save.
    const roadTopologyMayHaveChanged = attempt.buildings.length !== beforeConnectLen + 1;
    const attemptConn: SimState = roadTopologyMayHaveChanged
      ? { ...attempt, roadConnectivity: computeRoadConnectivity(attempt) }
      : { ...attempt, roadConnectivity: cur.roadConnectivity };

    // F2 FIX, part B — THE CITY-BREAKER (independent round finding): the
    // successor's OWN online status was never checked on EITHER branch.
    // `neighbourhoodIds` is built from the PRE-transaction audit, so a
    // brand-new building (which never existed in that audit) is never in
    // `onlineBefore` and the `wasOnline`-gated loop below always skips it via
    // its own `if (!wasOnline) continue`. When the connector was unaffordable
    // (autoConnect's `if (s.funds < totalCost)` branch returns state
    // UNCHANGED), `roadTopologyMayHaveChanged` is false too, so the OLD code
    // suppressed the AC-19 recheck entirely on BOTH paths — a background
    // process could demolish five working buildings and leave the successor
    // permanently offline with no skip recorded. Fixed by checking the
    // successor explicitly and unconditionally (never gated on
    // roadTopologyMayHaveChanged — a still-unconnected successor is exactly
    // the case where topology did NOT change): reject the WHOLE transaction,
    // discarding `attempt`, if it would not come online. The
    // roadTopologyMayHaveChanged skip-recheck optimisation itself remains
    // sound for SURVIVING buildings (a group can never contain a road/
    // trunk/landmark spec, so their own online status cannot be disturbed by
    // this demolish+build) — this check is what was missing to cover the
    // NEW building the optimisation never accounted for.
    //
    // isRoadAdjacent/isRoadConnected, NOT bare isOnline: computeIsOnline's G1
    // (construction-time) gate ALWAYS fails for a building whose builtTick
    // equals the current tick (0 ticks elapsed < any constructionTicks > 0),
    // so a bare isOnline() check would reject EVERY successor unconditionally
    // regardless of road status — this is normal ("just built, not finished
    // yet"), not the defect F2 describes. The road gates (G2/G3) are exactly
    // what F2's own attack test checks directly, and neither depends on
    // elapsed time, so checking them alone correctly answers "will this
    // building ever come online once construction completes".
    if (!isRoadAdjacent(attemptConn, successorBuilding) || !isRoadConnected(attemptConn, successorBuilding)) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'successor would be offline' });
      continue; // discard `attempt` — `cur` is untouched, nothing was ever committed.
    }

    // THE dominant cost in the original 1.3s-per-pass regression (measured,
    // not guessed): isOnline() is backed by onlineByBuilding() (data.ts), a
    // memoOnState fold that computes EVERY building's online status in ONE
    // O(buildings) pass the FIRST time any isOnline() call sees a given
    // state object, then answers O(1) from a Map for that SAME object
    // afterward. `attemptConn` is a brand-new object on every candidate
    // (`{...attempt, roadConnectivity: ...}` always allocates), so the first
    // isOnline(attemptConn, ...) call on this object (the successor check
    // above, or this neighbourhood check) ALWAYS cache-misses and re-walks
    // every building — bounded here to candidates that reach this point
    // (not every candidate the ladder considers). Fix: isOnline's answer for
    // a SURVIVING building (not in the demolished group) can only differ
    // between `cur` and `attemptConn` if roadConnectivity itself changed —
    // construction time, spec and coordinates are identical for every
    // surviving building, and isOnline reads nothing else. When
    // `!roadTopologyMayHaveChanged` (the common case — the successor sits
    // where the group stood, already road-adjacent), nothing isOnline reads
    // for a surviving building changed, so the whole neighbourhood provably
    // cannot have been newly stranded — skip the check entirely (the
    // successor's OWN status is still checked above, unconditionally).
    let strands = false;
    if (roadTopologyMayHaveChanged) {
      // O(neighbourhoodIds) lookup instead of rebuilding a full id->building
      // map from attemptConn.buildings (another BUG-642-class O(buildings)
      // trap) — every id in `neighbourhoodIds` is either unchanged from
      // `cur` (still in `byId`), the demolished group (excluded above), or
      // the successor itself; a brand-new connector tile can never appear
      // in `neighbourhoodIds` (built from the PRE-transaction audit).
      const lookupInAttempt = (id: number): Building | undefined =>
        id === successorBuilding.id ? successorBuilding : byId.get(id);
      for (const id of neighbourhoodIds) {
        if (groupIds.has(id)) continue; // demolished on purpose, not a stranding regression
        const wasOnline = onlineBefore.get(id);
        if (!wasOnline) continue;
        const nb = lookupInAttempt(id);
        if (!nb || !isOnline(attemptConn, nb)) {
          strands = true;
          break;
        }
      }
    }
    if (strands) {
      skipped.push({ sectionKey: opp.sectionKey, reason: 'would strand' });
      continue; // discard `attempt` — `cur` is untouched.
    }

    cur = attemptConn;
    byId.set(successorBuilding.id, successorBuilding);
    for (const id of groupIds) byId.delete(id);
    // F5 FIX (independent round finding): DO NOT recompute sectionIndexOf
    // after every committed transaction — measured +2,544ms/pass on Aaron's
    // real 49k-building save (60x the 40ms budget), dominated by exactly
    // this per-commit O(buildings) rebuild repeated up to
    // CONSOLIDATOR_MAX_TRANSACTIONS_PER_PASS times. `index` stays bound to
    // its pre-loop snapshot for the rest of the density phase; CEIL-3's
    // family-capacity read is kept EXACTLY correct (not just "close enough")
    // via `familyCapacityDelta` below, applied on every read. The one
    // remaining imprecision — a later candidate's NEIGHBOURHOOD buildingIds
    // list not reflecting an EARLIER commit in an ADJACENT section — is
    // accepted deliberately (per the round's own "hoist across transactions"
    // instruction): each transaction locks its own section via
    // `sectionsDone`, so this can only affect the rare case of two
    // transactions in adjacent sections in the SAME pass, and only the
    // stranding candidate LIST (isOnline itself is still read fresh off
    // `cur`, never cached), never money or determinism.
    familyCapacityDelta.set(familyKey, (familyCapacityDelta.get(familyKey) ?? 0) - groupCapacityApprox + successorCapacity);
    // F5 FIX: maintain the running occupied-tile board incrementally instead
    // of letting the NEXT candidate's occupiedSet(cur) cache-miss (cur just
    // changed) and pay a full O(buildings) rebuild — the other repeated
    // per-commit cost the round measured.
    for (const b of group) {
      const bs = SPECS[b.spec];
      if (!bs) continue;
      const { w, h } = footprintOf(b, bs);
      for (let dx = 0; dx < w; dx++) for (let dy = 0; dy < h; dy++) runningOccupied.delete(`${b.x + dx},${b.y + dy}`);
    }
    for (const nb of attempt.buildings.slice(beforeConnectLen)) {
      const nsp = SPECS[nb.spec];
      if (!nsp) continue;
      for (let dx = 0; dx < nsp.w; dx++) for (let dy = 0; dy < nsp.h; dy++) runningOccupied.add(`${nb.x + dx},${nb.y + dy}`);
    }
    sectionsDone.add(opp.sectionKey);
    // F3 FIX (independent round finding, "undo corrupts"): record EVERY
    // building this transaction added — the successor AND any connector/
    // junction-upgrade tiles autoConnect laid — not just the successor.
    // `attempt.buildings.slice(beforeConnectLen)` is exactly that set:
    // autoConnect only ever APPENDS new tiles at the tail (never reorders),
    // so the successor (at index `beforeConnectLen`, unmoved) plus every
    // tile appended after it is precisely what this transaction created.
    // Without this, Undo restored the demolished group by ORIGINAL
    // coordinate while leaving the connector's road tiles in place — two
    // buildings on the same tile, an invariant `fits`/occupiedSet/bulldoze
    // all assume can never happen.
    const addedThisTxn = attempt.buildings.slice(beforeConnectLen).map((b) => toConsolidationRecord(b, 'auto'));
    transactions.push({
      sectionKey: opp.sectionKey,
      kind: 'consolidate',
      removed: group.map((b) => toConsolidationRecord(b, 'player')),
      added: addedThisTxn,
      buildCost: realBuildCost,
      scrapRecovered,
      netCost: realNetCost,
    });
  }

  if (transactions.length === 0 && skipped.length === 0) {
    return { state: cur, passLog: null };
  }
  const priorId = (s.consolidatorLog ?? [])[0]?.id ?? 0;
  return {
    state: cur,
    passLog: { id: priorId + 1, tick, transactions, skipped },
  };
}

/**
 * AC-26: reverses exactly `consolidatorLog[0]` and pops it. Idempotent on an
 * empty log (returns `state` by reference identity — never an error).
 *
 * UNDO MODEL (task brief's "specify and be explicit about what Undo can NOT
 * restore"): this is the "store the pre-pass building set" strategy, in the
 * specific form of storing each transaction's `removed`/`added` records
 * (equivalent to storing the whole pre-pass set for the sections touched,
 * but far smaller — O(transactions × group size) instead of O(all buildings
 * in the affected sections)). What it CAN restore exactly: every demolished
 * building's original id/spec/x/y/capacityTier/builtTick/placedBy (so
 * `nextId` never moves and `buildings.ids-unique` still holds), and every
 * pound spent/recovered (funds + cumulativeCapexSpent, reversed exactly).
 * What it CANNOT restore:
 *   (1) a monitor (buildingMonitors/roadMonitors) that expired or was
 *       dropped on the demolished/added buildings during the pass — monitors
 *       are NOT captured in ConsolidationRecord (task scope: this build's
 *       file surface is consolidator.ts/engine.ts/types.ts, and monitor
 *       state is a THIRD structure with its own lifecycle the acceptance
 *       doc never asked Undo to reconstruct). A restored building starts
 *       with no active monitor, same as a legacy building predating one.
 *   (2) a reconnect transaction's road spur, if removing it would strand a
 *       building that came online BECAUSE of it (checked below) — the spur
 *       is kept and the pass log's undo is honestly partial for that one
 *       transaction, per the acceptance doc's own instruction ("otherwise
 *       the connector stays and the log entry says so").
 *   (3) anything OUTSIDE this one pass — Undo is single-level (ASM-1502,
 *       "the last pass"); a second Undo without a new pass in between is a
 *       no-op (the log is already empty of that entry).
 */
function undoLastConsolidatorPass(state: SimState): SimState {
  const log = state.consolidatorLog ?? [];
  const last = log[0];
  // F4 FIX (independent round finding): single-level, enforced by the
  // consolidatorUndoConsumed flag (see its doc comment, types.ts) — popping
  // the log alone is not single-level, since a SECOND press would then find
  // the PREVIOUS pass sitting at the new log[0] and cheerfully reverse that
  // one too.
  if (!last || (state.consolidatorUndoConsumed ?? false)) return state; // idempotent — AC-26

  const byId = new Map(state.buildings.map((b) => [b.id, b]));
  const addedIds = new Set<number>();
  for (const txn of last.transactions) for (const a of txn.added) addedIds.add(a.id);

  const removedRestored: Building[] = [];
  for (const txn of last.transactions) for (const r of txn.removed) removedRestored.push(recordToBuilding(r));

  const fullyUndoneBuildings = state.buildings.filter((b) => !addedIds.has(b.id)).concat(removedRestored);
  const fullyUndoneState: SimState = {
    ...state,
    buildings: fullyUndoneBuildings,
    roadConnectivity: computeRoadConnectivity({ ...state, buildings: fullyUndoneBuildings }),
  };
  const fullyUndoneById = new Map(fullyUndoneBuildings.map((b) => [b.id, b]));

  // Per-reconnect-transaction partial-undo check (AC-26's last bullet).
  const keptAddedRecords = new Map<number, ConsolidationRecord>();
  let anyPartial = false;
  for (const txn of last.transactions) {
    if (txn.kind !== 'reconnect' || txn.added.length === 0) continue;
    const audit = sectionIndexOf(state).get(txn.sectionKey);
    if (!audit) continue;
    let strands = false;
    for (const id of audit.buildingIds) {
      if (addedIds.has(id)) continue; // the spur's own tiles
      const before = byId.get(id);
      const after = fullyUndoneById.get(id);
      if (!before || !after) continue;
      if (isOnline(state, before) && !isOnline(fullyUndoneState, after)) {
        strands = true;
        break;
      }
    }
    if (strands) {
      anyPartial = true;
      for (const a of txn.added) keptAddedRecords.set(a.id, a);
    }
  }

  let finalBuildings = fullyUndoneBuildings;
  if (keptAddedRecords.size > 0) {
    finalBuildings = finalBuildings.concat(Array.from(keptAddedRecords.values()).map(recordToBuilding));
  }

  let reversedNetCost = 0;
  let reversedBuildCost = 0;
  for (const txn of last.transactions) {
    const kept = txn.kind === 'reconnect' && txn.added.some((a) => keptAddedRecords.has(a.id));
    if (kept) continue; // the asset survives — its spend is not refunded.
    reversedNetCost += txn.netCost;
    reversedBuildCost += txn.buildCost;
  }

  const label = `Consolidation Undo (${last.transactions.length} site${last.transactions.length === 1 ? '' : 's'})${anyPartial ? ' — 1+ spur kept (would strand)' : ''}`;

  // AC-26: "ids are preserved in the record, so nextId never moves". Roll
  // nextId back by exactly the count of FULLY-removed added records (a kept
  // reconnect spur's ids stay consumed — that asset still exists). Floor-
  // guarded against the highest id actually surviving in `finalBuildings` so
  // this can never collide with an id some OTHER action minted in between
  // the pass and this Undo (the common "undo right after the pass" case
  // restores nextId exactly; a less common "undo much later" case degrades
  // safely to "as low as it can go without a collision" rather than ever
  // creating one).
  const removedAddedCount = last.transactions.reduce(
    (sum, t) => sum + (t.kind === 'reconnect' && t.added.some((a) => keptAddedRecords.has(a.id)) ? 0 : t.added.length),
    0,
  );
  const maxSurvivingId = finalBuildings.reduce((m, b) => Math.max(m, b.id), 0);
  const nextId = Math.max(state.nextId - removedAddedCount, maxSurvivingId + 1);

  return {
    ...state,
    buildings: finalBuildings,
    nextId,
    roadConnectivity: computeRoadConnectivity({ ...state, buildings: finalBuildings }),
    funds: state.funds + reversedNetCost,
    cumulativeCapexSpent: Math.max(0, (state.cumulativeCapexSpent ?? 0) - reversedBuildCost),
    consolidatorLog: log.slice(1),
    // F4 FIX: this reversal is now CONSUMED — a second consolidatorUndo
    // press is a no-op until a new pass runs (see the flag's doc comment).
    consolidatorUndoConsumed: true,
    ...(reversedNetCost !== 0 ? logEvent(state, label, reversedNetCost) : {}),
  };
}

function advance(s: SimState): SimState {
  // FEAT-1972079923 inc4 (AC-11): the FINAL DECLINE screen is a HARD STOP —
  // once declineState is set, the clock never advances again (no further
  // tick changes ANY field, including tick itself). Same-reference return
  // (not a shallow copy) so callers can cheaply detect "nothing changed".
  if (s.declineState) return s;

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

  // FEAT-2326609761 (CONSOLIDATOR, AC-36): monthly pass, gated on
  // consolidatorEnabled, running right after the auto-scale block above (this
  // tick's road/building auto-scale has already landed, so the consolidator
  // reads the SAME upgraded/connectivity-fresh frame). Deliberately placed
  // AFTER the roadConnectivity recompute just above (not literally "before
  // sweepOrphanConnects" as the acceptance doc's now-superseded line numbers
  // suggested) so the pass's own stranded-capacity detection and AC-19
  // stranding checks read a connectivity graph that is actually current for
  // THIS tick, not last tick's.
  let consolidatorBuildCost = 0;
  let consolidatorScrapRecovered = 0;
  let consolidatorTransactionCount = 0;
  // FEAT-2326609761 inc2: a single day can now produce UP TO TWO log
  // entries (a glide-window pass AND, on the month-12 boundary day only, an
  // additional whole-map pass) — see the loop below. `consolidatorPassLogs`
  // replaces the old single-entry `consolidatorPassLog`, applied to
  // `s.consolidatorLog` in ARRAY order (oldest of the day's entries first,
  // so consolidatorLog[0] after this tick is always the LATEST of the two,
  // matching undoLastConsolidatorPass's "reverse consolidatorLog[0]" contract).
  const consolidatorPassLogs: ConsolidationPass[] = [];
  // Captured BEFORE any pass runs this tick (applyConsolidatorPass computes
  // its own `id` as `priorId + 1` from whatever `s.consolidatorLog` it is
  // handed — see its own `priorId` line — which would hand back the SAME id
  // twice if called twice in one tick without this: `s.consolidatorLog`
  // itself is never updated mid-tick, only once at the very end of
  // advance(), below). Every one of today's passLogs is re-numbered from
  // this single captured value once the loop finishes, so two passes on the
  // SAME day (glide window + month-12 whole-map) always get distinct,
  // correctly-ordered ids.
  const consolidatorPriorLogId = (s.consolidatorLog ?? [])[0]?.id ?? 0;
  if (s.consolidatorEnabled ?? false) {
    const mode = s.consolidatorMode ?? CONSOLIDATOR_MODE_DEFAULT;
    // AC-6/ruling 7's monthly-twelfth rotation already treats month 12 as a
    // whole-map scope (monthlyScopeOf's `full` flag) — reused here, on the
    // SAME single boundary tick the legacy mode would have run its own
    // whole-map pass on, as the trigger for glide mode's "the month-12
    // whole-map pass still runs" ADDITIONAL pass (Aaron's 2026-09-03
    // addendum). Computed once regardless of mode since it's a cheap pure
    // function of tick, not a scan.
    const isMonth12Boundary = tick % TICKS_PER_MONTH === 0 && monthlyScopeOf(tick).full;

    // Each entry is a scope to run a pass against THIS tick, in order.
    // - glide (DEFAULT): one pass every game DAY, scoped to today's glide
    //   window's overlapping fixed section(s) — the structural perf fix for
    //   the disclosed ~1.5-2.1s monthly stall (see applyConsolidatorPass's
    //   own doc comment for the mechanism). PLUS, on the month-12 boundary
    //   day, a SECOND pass with no override (= the full whole-map scope) —
    //   "complements (does not replace) the monthly-twelfth cadence".
    // - monthly-twelfth (legacy): exactly the pre-inc2 behaviour — one pass,
    //   once a month, using monthlyScopeOf's own scope (which is ALREADY the
    //   whole map on month 12) — unaffected by this refactor.
    const passesToRun: (readonly number[] | undefined)[] =
      mode === 'glide'
        ? [sectionKeysForGlideWindow(s, tick), ...(isMonth12Boundary ? [undefined] : [])]
        : tick % TICKS_PER_MONTH === 0
          ? [undefined]
          : [];

    for (const sectionKeysOverride of passesToRun) {
      const preFunds = s.funds;
      const preLedger = s.ledger;
      const preLedgerId = s.nextLedgerId;
      const { state: afterConsolidator, passLog } = applyConsolidatorPass(s, tick, sectionKeysOverride);
      if (passLog) {
        consolidatorPassLogs.push(passLog);
        consolidatorTransactionCount += passLog.transactions.length;
        for (const txn of passLog.transactions) {
          consolidatorBuildCost += txn.buildCost;
          consolidatorScrapRecovered += txn.scrapRecovered;
        }
      }
      // AC-24 (the BUG-400 trap): autoConnect (called internally by the pass
      // for both reconnect transactions and a consolidated successor's own
      // road hookup) writes its OWN per-connector ledger row exactly like a
      // player 'place' would. Stripped here and replaced with exactly ONE
      // aggregate row below, so a background process can never out-populate
      // the 200-cap ledger the way the old recurring Regional Grant row did.
      // funds is reverted too — it is re-derived from the SAME income/expense
      // arithmetic every other monthly aggregate uses (fed via outflows/
      // inflows just below), not trusted bare off the pass's own state.
      s = { ...afterConsolidator, ledger: preLedger, nextLedgerId: preLedgerId, funds: preFunds };
      scaledBuildings = s.buildings;
    }
  }

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
  // FEAT-2326609761 (CONSOLIDATOR, AC-22): gross build/scrap booked as flow
  // lines (never netted here) so conservation.funds-vs-flows holds exactly.
  if (consolidatorBuildCost > 0) {
    outflows = [...outflows, { label: 'Consolidation', value: consolidatorBuildCost }];
  }
  if (consolidatorScrapRecovered > 0) {
    inflows = [...inflows, { label: 'Consolidation Scrap', value: consolidatorScrapRecovered }];
  }

  // Drain pending rewards queue (from debugXp and place actions).
  // Each applies through flows so it's visible in fiscal panel and counts for conservation.
  //
  // BUG-600 (defense-in-depth, GR#16): reducer() already runs sanitizeTreasury()
  // — which sanitizes both reward queues, see there — before ANY action
  // (including 'tick') reaches advance(). This re-sanitizes anyway so a NaN/
  // non-array reaching the funds arithmetic below is structurally impossible
  // here, not merely "prevented upstream today" (advance() has no way to
  // enforce that every future caller routes through reducer() first).
  const safePendingRewards = sanitizePendingRewards(s.pendingRewards, isValidLevelReward);
  let nextNotice = s.notice;
  for (const pr of safePendingRewards) {
    inflows = [...inflows, { label: 'Level Rewards', value: pr.totalReward }];
    nextNotice = pr.notice; // Last notice wins (multiple crossings rare but possible)
  }

  // FEAT-milestone-cash-rewards-2026-09-02 (Q100047b ruling B1): drain the
  // milestone-reward queue exactly like the Level Rewards queue above. The
  // milestone was marked CLAIMED and the reward QUEUED on the tick its
  // predicate was first observed true (see the detection block near the end
  // of this function, evaluated against the fully-assembled next state); it
  // is PAID here, one tick later, as a normal labelled inflow so it counts
  // for the tick-boundary conservation invariant
  // (fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows) on the tick
  // the cash actually lands (mirrors the Level Rewards one-tick-lag design).
  let nextMilestoneNotice: MilestoneNotice | null = s.milestoneNotice ?? null;
  // BUG-600: same defense-in-depth re-sanitization as safePendingRewards above.
  const pendingMilestoneRewards = sanitizePendingRewards(s.pendingMilestoneRewards, isValidMilestoneReward);
  for (const pr of pendingMilestoneRewards) {
    inflows = [...inflows, { label: `Milestone Reward: ${pr.notice.label}`, value: pr.totalReward }];
    nextMilestoneNotice = pr.notice; // Last notice wins (multiple crossings rare but possible)
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
  // FEAT-2326609761 (CONSOLIDATOR, AC-24): AT MOST ONE aggregate ledger row
  // per pass — the per-building detail lives in consolidatorLog (its own cap,
  // pushed into `next` below), never the ledger's 200-row ring. Omitted
  // entirely when netCost is exactly 0 (a pass that only laid a spur with a
  // planner that found £0 route length, or a pass whose scrap exactly offset
  // its build cost) — nothing to report as a money event.
  if (consolidatorTransactionCount > 0) {
    const consolidatorNetCost = consolidatorBuildCost - consolidatorScrapRecovered;
    if (consolidatorNetCost !== 0) {
      ledger = [
        {
          id: nextLedger++,
          tick,
          label: `Consolidation (${consolidatorTransactionCount} site${consolidatorTransactionCount === 1 ? '' : 's'})`,
          amount: -consolidatorNetCost,
        },
        ...ledger,
      ].slice(0, LEDGER_CAP);
    }
  }
  // FEAT-milestone-cash-rewards-2026-09-02: visible ledger row for each
  // milestone reward drained above — a real, positive-amount inflow row
  // (mirrors the Play Mode injection precedent, fiscal.ts), never a silent
  // funds bump. One row per milestone paid this tick (usually 0 or 1).
  for (const pr of pendingMilestoneRewards) {
    ledger = [
      { id: nextLedger++, tick, label: `Milestone Reward: ${pr.notice.label}`, amount: pr.totalReward },
      ...ledger,
    ].slice(0, LEDGER_CAP);
  }

  // BUG-509: use the canonical tiered capacity (residential-only, isOnline-gated,
  // same as evaluateBuildingMonitors' own utilization basis via capacityAtTier)
  // instead of summing the flat per-spec base. The flat sum ignored
  // building.capacityTier entirely, so a Building Auto-Scale upgrade (which DOES
  // raise capacityTier and DOES charge the player, engine.ts evaluateBuildingMonitors)
  // never raised the population ceiling the growth model below converges toward —
  // the auto-scale spend bought nothing. onlineResidentsCapacity(s) mirrors this
  // ceiling's prior semantics exactly (residential kind + isOnline gate) but reads
  // capacityAtTier(sp, b.capacityTier ?? 0) instead of the tier-0 base.
  const capacity = onlineResidentsCapacity(s);
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

  // FEAT-1972079925 — demographic FLOWS replace the bare converge-to-capacity
  // rule (BUG-394's frozen-at-capacity city). `attractiveness` reuses the
  // EXACT shape the retired growthFactor used (tax/transit/demand/station
  // terms, unchanged) so the below-capacity growth trajectory stays close to
  // every already-landed scenario test that exercises population growth.
  const attractiveness =
    (1.4 - avgTax / 15) * (s.policies.transitSubsidy ? 1.25 : 1) *
    Math.max(0.3, 0.55 + demand.residential / 200) *
    (1 + 0.15 * Math.min(stationWeight, 6));

  const popBefore = s.population;
  // Wellbeing read on the START-of-tick state (mirrors BUG-419's basis
  // discipline: population-scaled effects are computed on the SAME frame the
  // rest of this tick charges against, before the growth update below).
  const wbOverall = wellbeingOf(s).overall;

  let births = 0;
  let deaths = 0;
  let moveIns = 0;
  let moveOuts = 0;
  let population = popBefore;

  if (popBefore <= capacity) {
    // ⚠ BALANCE-NUMBER PLACEHOLDERS (BIRTH_RATE_PER_TICK / DEATH_RATE_PER_TICK /
    // MOVE_IN_RATE / MOVE_OUT_BASE_RATE / WELLBEING_MOVEOUT_FACTOR) — directional
    // only, pending Aaron's row-by-row balance pass.
    births = Math.round(popBefore * BIRTH_RATE_PER_TICK);
    deaths = Math.round(popBefore * DEATH_RATE_PER_TICK);
    // Move-out rate rises as wellbeing falls (state-derived, deterministic —
    // GR#21: no Date/random). At wellbeing 100 the rate is exactly the base.
    // BUG-524 (Q100046 C1) — unemployment reaches move-out THROUGH wbOverall
    // (wellbeingOf's new "Jobs/Employment" part, above) rather than as a
    // second direct term here. See the NO-DOUBLE-COUNT DECISION comment on
    // that part in wellbeingOf for the reasoning.
    const moveOutRate =
      MOVE_OUT_BASE_RATE * (1 + (WELLBEING_MOVEOUT_FACTOR * (100 - wbOverall)) / 100);
    moveOuts = Math.round(popBefore * moveOutRate);

    const headroom = Math.max(0, capacity - popBefore);
    // Move-ins are bounded by EFFECTIVE headroom: the raw vacancy plus the
    // space THIS tick's own deaths/move-outs free up. Without the "+deaths+
    // moveOuts" term a city sitting exactly at capacity would compute
    // headroom=0 and freeze move-ins at 0 forever — exactly the BUG-394
    // freeze this feature exists to fix. With it, at-capacity move-ins
    // backfill departures (churn), while below-capacity headroom still
    // dominates (fast fill), and the hard capacity ceiling below still caps
    // the result every tick.
    const effectiveHeadroom = Math.max(0, headroom + deaths + moveOuts);
    moveIns = Math.max(
      0,
      Math.min(effectiveHeadroom, Math.round(effectiveHeadroom * MOVE_IN_RATE * attractiveness))
    );

    population = Math.max(0, Math.min(capacity, popBefore + births + moveIns - deaths - moveOuts));
  } else {
    // Over-capacity (e.g. right after a demolition drops capacity below the
    // current population): retain the pre-existing 10%-per-tick decay-to-
    // capacity behaviour. Recorded as pure move-out churn so the conservation
    // identity (pop_after = pop_before + births + moveIns - deaths - moveOuts)
    // still holds for the history/Sankey consumers.
    population = Math.max(capacity, popBefore - Math.ceil((popBefore - capacity) * 0.1));
    moveOuts = popBefore - population;
  }

  // Per-tick demographic flows (FEAT-1972079925): recorded exactly like
  // lastFlows records fiscal flows, then accumulated into a bounded monthly
  // history ring for the population Sankey + trend views (GR#15: the ring
  // holds only REAL recorded flows, never a fabricated split).
  const demographics: DemographicFlow = { births, deaths, moveIns, moveOuts };
  const accumSoFar: DemographicFlow = s.demographicAccum ?? {
    births: 0,
    deaths: 0,
    moveIns: 0,
    moveOuts: 0,
  };
  const demographicAccum: DemographicFlow = {
    births: accumSoFar.births + births,
    deaths: accumSoFar.deaths + deaths,
    moveIns: accumSoFar.moveIns + moveIns,
    moveOuts: accumSoFar.moveOuts + moveOuts,
  };
  let demographicHistory: MonthlyDemographics[] = s.demographicHistory ?? [];
  let nextDemographicAccum = demographicAccum;
  if (tick % TICKS_PER_MONTH === 0) {
    demographicHistory = [
      ...demographicHistory,
      { tick, population, ...demographicAccum },
    ].slice(-DEMOGRAPHIC_HISTORY_CAP);
    nextDemographicAccum = { births: 0, deaths: 0, moveIns: 0, moveOuts: 0 };
  }

  // FEAT-1972079926 — arrivals-by-mode: split THIS tick's moveIns (the SSOT
  // total computed above, unchanged) across the transport modes connected +
  // online in the city right now. Accumulated into a bounded monthly ring in
  // exact parallel to the demographic accumulator above.
  const arrivalsByMode: ArrivalsByMode = splitArrivalsByMode(s, moveIns);
  const arrivalsAccumSoFar: ArrivalsByMode = s.arrivalsByModeAccum ?? {
    road: 0,
    railLow: 0,
    railHs: 0,
    sea: 0,
    plane: 0,
  };
  const arrivalsByModeAccum: ArrivalsByMode = {
    road: arrivalsAccumSoFar.road + arrivalsByMode.road,
    railLow: arrivalsAccumSoFar.railLow + arrivalsByMode.railLow,
    railHs: arrivalsAccumSoFar.railHs + arrivalsByMode.railHs,
    sea: arrivalsAccumSoFar.sea + arrivalsByMode.sea,
    plane: arrivalsAccumSoFar.plane + arrivalsByMode.plane,
  };
  let arrivalsByModeHistory: MonthlyArrivalsByMode[] = s.arrivalsByModeHistory ?? [];
  let nextArrivalsByModeAccum = arrivalsByModeAccum;
  if (tick % TICKS_PER_MONTH === 0) {
    arrivalsByModeHistory = [
      ...arrivalsByModeHistory,
      { tick, ...arrivalsByModeAccum },
    ].slice(-ARRIVALS_HISTORY_CAP);
    nextArrivalsByModeAccum = { road: 0, railLow: 0, railHs: 0, sea: 0, plane: 0 };
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
  // BUG-600: reuse the already-sanitized safePendingRewards computed above —
  // a raw s.pendingRewards read here would let a junk element's `undefined`
  // newLevel silently corrupt lastRewardedLevel.
  let lastRewardedLevel = s.lastRewardedLevel;
  for (const pr of safePendingRewards) {
    lastRewardedLevel = pr.newLevel;
  }
  for (const lr of inTickRewards) {
    lastRewardedLevel = lr.newLevel;
  }

  // FEAT-1972079923 inc1 (AC-1, AC-12): the insolvency band is PURELY derived from
  // the end-of-tick funds — deterministic, no Date/random, so replay reproduces
  // every band transition at the same tick. Classified BEFORE the inc2 bailout
  // injection below (preInjectionFunds) so the same-tick rescue never masks the
  // read of what actually happened this tick — the band records the crossing,
  // not the bailout's own softening of it.
  const preInjectionFunds = funds;
  const prevInsolvencyState: InsolvencyState = s.insolvencyState ?? 'solvent';
  const insolvencyState = insolvencyStateForFunds(preInjectionFunds);
  // BUG-496: the RAW band from the previous tick, compared like-for-like against
  // the RAW band this tick (both are insolvencyStateForFunds output, never the
  // overlaid/exposed insolvencyState) — see insolvencyRawBand's doc in types.ts.
  const prevInsolvencyRawBand: InsolvencyState = s.insolvencyRawBand ?? 'solvent';

  // FEAT-1972079923 inc2 (AC-1, AC-2, AC-12): the IMF BAILOUT EVENT state
  // machine. Triggered exactly once — the SAME tick and SAME one-shot
  // condition as insolvencyPopup below (band transitions INTO 'crisis'), and
  // guarded by `prevBailoutState === null` so a tick that stays in crisis
  // never re-fires the injection. The one-time BAILOUT_INCOME_INJECTION is
  // booked as a normal labelled inflow (mirrors the Level Rewards pattern
  // immediately above) BEFORE fundsAtTickEnd is captured, so the conservation
  // invariant (fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows)
  // holds exactly like every other in-tick inflow. Year-end re-evaluation
  // (AC-2) is deterministic tick arithmetic only (tick >= enteredAt +
  // BAILOUT_DURATION_TICKS) — no Date/random (GR#21) — so replay reproduces
  // the exact entry AND exit ticks.
  const prevBailoutState = s.bailoutState ?? null;
  let bailoutState = prevBailoutState;
  // FEAT-1972079923 inc4 (AC-10): the SECOND bailout state, read/mutated
  // alongside the first bailout's below — declared here (not lower down)
  // because the plain-bailout year-end branch below may auto-trigger it.
  const prevBailoutSecondState = s.bailoutSecondState ?? null;
  let bailoutSecondState = prevBailoutSecondState;

  // BUG-506 (AC-506-3/4): rolling window of the last DECLINE_AVERAGING_WINDOW_TICKS
  // ticks' funds, updated EVERY tick regardless of insolvency state, so an
  // averaged decline decision at any year-end checkpoint reads the mean of the
  // FINAL window ticks of whatever period just elapsed — the window naturally
  // holds exactly those ticks because checkpoints land on fixed tick
  // arithmetic (BAILOUT_DURATION_TICKS/SECOND_BAILOUT_DURATION_TICKS).
  // Deterministic, no Date/random (GR#21).
  const recentFundsWindow = [...(s.recentFundsWindow ?? []), sanitizeFunds(funds)].slice(
    -DECLINE_AVERAGING_WINDOW_TICKS,
  );
  const meanRecentFunds =
    recentFundsWindow.length > 0
      ? recentFundsWindow.reduce((a, b) => a + b, 0) / recentFundsWindow.length
      : funds;

  // BUG-506 (AC-506-1/2): consecutive-tick counter of SUSTAINED recovery
  // (funds >= 0) while EITHER bailout is active, resetting to 0 the instant
  // funds dip below 0 or no bailout was active last tick. Reaching
  // SUSTAINED_RECOVERY_TICKS triggers an EARLY exit below, ahead of either
  // bailout's own year-end checkpoint.
  const wasInAnyBailout = prevBailoutState !== null || prevBailoutSecondState !== null;
  const recoveryStreak = wasInAnyBailout ? (funds >= 0 ? (s.recoveryStreak ?? 0) + 1 : 0) : 0;

  // BUG-506 (AC-506-1/2): EARLY EXIT — sustained recovery clears the active
  // bailout before its year-end checkpoint. Checked FIRST (before the
  // fresh-trigger / year-end branches below) so a bailout cleared early this
  // tick is never also processed by its own year-end logic the same tick.
  let firstBailoutEarlyExit = false;
  if (prevBailoutState !== null && recoveryStreak >= SUSTAINED_RECOVERY_TICKS) {
    bailoutState = null;
    firstBailoutEarlyExit = true;
  }
  let secondBailoutEarlyExit = false;
  if (
    !firstBailoutEarlyExit &&
    prevBailoutSecondState !== null &&
    recoveryStreak >= SUSTAINED_RECOVERY_TICKS
  ) {
    bailoutSecondState = null;
    secondBailoutEarlyExit = true;
  }

  // BUG-504 Option A: legacy re-arm counter — NO LONGER INCREMENTED by any
  // FRESH bailout trigger (FEAT-dynamic-bailout retires the re-arm; see
  // `dynamicBailoutUsed` below), kept read-only so an OLD save's count is
  // never lost and `bailoutStandingCostPerTick` still reads a stable value
  // (a fresh dynamic bailout never touches it, so it stays at whatever an
  // old save already carries — 0 for a save that never used the old ladder).
  const firstBailoutCountBefore = s.firstBailoutCount ?? 0;
  const firstBailoutCount = firstBailoutCountBefore;

  // FEAT-1972079923 inc3 (AC-5, AC-6, AC-7): ADMINISTRATION MODE overlay.
  // Entry is USER-INITIATED (the `enterAdministration` action, below in
  // reduceCore) — advance() never enters administration on its own; it only
  // handles the AC-7 year-end re-evaluation: exactly ADMINISTRATION_DURATION_TICKS
  // after entry, administration ALWAYS ends (whether or not funds recovered) —
  // deterministic tick arithmetic only (GR#21), never Date.now(). Recovery is
  // reported via the funds band below (solvent/warning). Still-broke reverts
  // to the funds band too, but FEAT-1972079923 inc4 (AC-10/AC-11) ALSO fires
  // the next stage of the endgame depending which bailout year this
  // administration session covered (`origin`, stamped by `enterAdministration`):
  // 'bailout' (first year) → auto-trigger the second bailout (UNCHANGED, see
  // the FEAT-dynamic-bailout scoping note below); 'bailout_second' (second
  // year) → transition to the final decline screen (AC-11). Declared here
  // (ABOVE the fresh-crisis-trigger block below) so both blocks can share the
  // same `declineState`/`administrationState` locals.
  const prevAdministrationState = s.administrationState ?? null;
  let administrationState = prevAdministrationState;
  // FEAT-1972079923 inc4 (AC-11): declineState is read/mutated here (the
  // administration-origin-'bailout_second' branch below may set it) and in the
  // plain bailoutSecondState year-end branch further down.
  const prevDeclineState = s.declineState ?? null;
  let declineState = prevDeclineState;

  // FEAT-dynamic-bailout (Aaron ruling Q100045, docs/planning/acceptance/
  // FEAT-dynamic-bailout-2026-09-02.md) — "this only happens once. then
  // that's it." RETIRES the old MAX_FIRST_BAILOUTS re-arm ladder's FRESH-ENTRY
  // path: `dynamicBailoutUsed` (a bool, not a counter — the max is now
  // strictly one) gates the ONE dynamic offer this playthrough ever gets, in
  // place of the retired `firstBailoutCountBefore < MAX_FIRST_BAILOUTS` test.
  // SCOPING DECISION (lower blast-radius, spec §3's alternative branch (b) —
  // "keep bailoutSecondState as a structurally-different... stage, Aaron's
  // call"): the escalation-to-second-bailout MACHINERY below (both the
  // fresh-crisis-with-offer-already-used branch, and the plain/administration
  // year-end-still-broke branches further down) is left COMPLETELY UNCHANGED
  // from the landed ladder — still the fixed BAILOUT_INCOME_INJECTION_SECOND
  // on worse terms, still leading to decline exactly as before. Only the
  // FIRST-tier grant's SIZE (dynamic, not fixed) and its once-only gate
  // (`dynamicBailoutUsed`, not a re-arm counter) are new. This reuses the
  // whole already-tested endgame-teeth estate (imf-insolvency-inc4/inc5,
  // bug-504-505-506-endgame, bug496-497, play-mode-endgame, bug501) instead
  // of duplicating a second decline path — see the build report's retire-the-
  // ladder note for the full reasoning and the (a)-vs-(b) tradeoff.
  const dynamicBailoutUsedBefore = s.dynamicBailoutUsed ?? false;
  let dynamicBailoutUsed = dynamicBailoutUsedBefore;
  if (
    !firstBailoutEarlyExit &&
    insolvencyState === 'crisis' &&
    prevInsolvencyState !== 'crisis' &&
    prevInsolvencyState !== 'administration' &&
    prevBailoutState === null &&
    prevBailoutSecondState === null
  ) {
    if (!dynamicBailoutUsedBefore) {
      // THE one dynamic offer — sized off THIS city's own cumulative capex
      // spend + its current opex bleed rate (fiscal.computeDynamicBailoutOffer),
      // replacing the retired fixed BAILOUT_INCOME_INJECTION. The bleed
      // reading is taken from THIS tick's own (pre-injection) flows — by
      // construction free of self-distortion, since this tick's own grant
      // hasn't been appended to `inflows` yet, and any PAST tick's injection
      // lives in a PAST tick's flows, never in this tick's `inflows`/`outflows`.
      const bleed = netOpexBleedPerTick({ inflows, outflows });
      const dynamicOffer = computeDynamicBailoutOffer(s.cumulativeCapexSpent ?? 0, bleed);
      bailoutState = { enteredAt: tick };
      dynamicBailoutUsed = true;
      funds += dynamicOffer.offer;
      inflows = [...inflows, { label: DYNAMIC_BAILOUT_INJECTION_LABEL, value: dynamicOffer.offer }];
    } else {
      // FEAT-dynamic-bailout: the ONE dynamic offer is already spent this
      // playthrough — no fresh first-tier grant. Escalates straight to the
      // (unchanged, worse-terms) second bailout, exactly like the retired
      // ladder's "re-arm cap exhausted" branch did — see the scoping note
      // above for why this machinery is deliberately left untouched.
      bailoutSecondState = { enteredAt: tick };
      funds += BAILOUT_INCOME_INJECTION_SECOND;
      inflows = [
        ...inflows,
        { label: BAILOUT_SECOND_INJECTION_LABEL, value: BAILOUT_INCOME_INJECTION_SECOND },
      ];
    }
  } else if (
    !firstBailoutEarlyExit &&
    prevBailoutState !== null &&
    tick >= prevBailoutState.enteredAt + BAILOUT_DURATION_TICKS
  ) {
    if (funds >= BAILOUT_CLEAN_END_THRESHOLD) {
      // BUG-504 Option A / BUG-505: clean-end requires REAL solvency (funds
      // >= 0), not merely climbing back above the OLD crisis-line bar — that
      // old bar let a slow-draining city clear a bailout while still deep in
      // the red, then re-enter crisis and re-collect a fresh grant every
      // year forever (BUG-504). Raising the bar strictly ABOVE the crisis
      // threshold also closes BUG-505's dead-stuck window: a clean-end can
      // never leave the raw funds band in 'crisis' (crisis is funds <=
      // DEBT_THRESHOLD_FOR_BAILOUT, strictly below 0).
      bailoutState = null;
    } else {
      // Still not solvent at the ONE dynamic bailout's year-end — AUTO-
      // TRIGGERS the (unchanged, worse-terms) second bailout, exactly like
      // the landed ladder — see the scoping note above.
      bailoutState = null;
      bailoutSecondState = { enteredAt: tick };
      funds += BAILOUT_INCOME_INJECTION_SECOND;
      inflows = [
        ...inflows,
        { label: BAILOUT_SECOND_INJECTION_LABEL, value: BAILOUT_INCOME_INJECTION_SECOND },
      ];
    }
  }

  if (
    prevAdministrationState !== null &&
    tick >= prevAdministrationState.enteredAt + ADMINISTRATION_DURATION_TICKS
  ) {
    const origin: BailoutOrigin = prevAdministrationState.origin ?? 'bailout';
    administrationState = null;
    // BUG-504 Option A: this "still broke" test now uses the SAME real-
    // solvency bar as the plain (non-administration) first-bailout year-end
    // branch above (BAILOUT_CLEAN_END_THRESHOLD, not the old crisis-line
    // DEBT_THRESHOLD_FOR_BAILOUT) — otherwise Administration Mode would be a
    // silent loophole back into the unbounded-rescue class BUG-504 closed.
    if (origin === 'bailout' && funds < BAILOUT_CLEAN_END_THRESHOLD) {
      // Still broke after an administration-covered ONE dynamic bailout
      // year — auto second bailout (UNCHANGED, see the scoping note above).
      bailoutSecondState = { enteredAt: tick };
      funds += BAILOUT_INCOME_INJECTION_SECOND;
      inflows = [
        ...inflows,
        { label: BAILOUT_SECOND_INJECTION_LABEL, value: BAILOUT_INCOME_INJECTION_SECOND },
      ];
    } else if (origin === 'bailout_second' && meanRecentFunds < FINAL_DECLINE_FUNDS_THRESHOLD) {
      // Still broke after an administration-covered SECOND bailout year — hard
      // game-over. Stats captured NOW from the trackers below (computed just
      // before this return), never fabricated defaults (GR#15).
      declineState = {
        enteredAt: tick,
        peakPopulation: Math.max(s.peakPopulation ?? s.population, population),
        finalPopulation: population,
        minFundsEver: Math.min(s.minFundsEver ?? s.funds, funds),
        totalSpending: (s.totalSpending ?? 0) + expense,
      };
    }
    // origin === 'bailout' with funds recovered, or origin === 'bailout_second'
    // with funds >= FINAL_DECLINE_FUNDS_THRESHOLD: no further transition — the
    // exposed band below reads the recovered funds band directly.
  }

  // FEAT-1972079923 inc4 (AC-10, AC-11): the plain (non-administration) SECOND
  // bailout's OWN year-end re-evaluation — mirrors the first bailout's plain
  // branch above. Guarded so this never fires the same tick bailoutSecondState
  // was just entered (prevBailoutSecondState, not the local var, which may have
  // just been set above).
  if (
    !secondBailoutEarlyExit &&
    declineState === null &&
    prevBailoutSecondState !== null &&
    tick >= prevBailoutSecondState.enteredAt + SECOND_BAILOUT_DURATION_TICKS
  ) {
    // BUG-506 (AC-506-3/4): the decline decision reads the AVERAGED window
    // (meanRecentFunds), not this single tick's funds — a lone bad tick at
    // the very end of an otherwise-solvent year no longer forces game-over,
    // and a lone lucky tick at the end of an otherwise-insolvent year no
    // longer buys a reprieve. See DECLINE_AVERAGING_WINDOW_TICKS.
    if (meanRecentFunds < FINAL_DECLINE_FUNDS_THRESHOLD) {
      bailoutSecondState = null;
      declineState = {
        enteredAt: tick,
        peakPopulation: Math.max(s.peakPopulation ?? s.population, population),
        finalPopulation: population,
        minFundsEver: Math.min(s.minFundsEver ?? s.funds, funds),
        totalSpending: (s.totalSpending ?? 0) + expense,
      };
    } else {
      // Recovered (mean funds >= FINAL_DECLINE_FUNDS_THRESHOLD) — second
      // bailout ends cleanly, no decline, no third bailout ever offered.
      bailoutSecondState = null;
    }
  }

  // BUG-504 Option A: STANDING COST — a felt lifeline, not a free tap.
  // Charged every tick either bailout is ACTIVELY in force (post all of the
  // transitions/early-exits resolved above for THIS tick), scaling with how
  // many first-bailout re-arms have been used so far (a worse credit hit on
  // repeat). Booked as a normal labelled outflow so conservation traces it
  // exactly like the injections above.
  if (bailoutState !== null || bailoutSecondState !== null) {
    const bailoutStandingCost = bailoutStandingCostPerTick(firstBailoutCount);
    funds -= bailoutStandingCost;
    outflows = [...outflows, { label: BAILOUT_STANDING_COST_LABEL, value: bailoutStandingCost }];
  }

  // BUG-504 Option A: the running spend tracker below must count the standing
  // cost outflow just added — `expense` (captured earlier, before this
  // block) would silently under-report a tick with an active bailout.
  // Recomputed once from the FINAL outflows array, after every mutation.
  const expenseFinal = outflows.reduce((a, b) => a + b.value, 0);

  // FEAT-1972079923 inc4 (AC-11): running decline-stat trackers, updated EVERY
  // tick regardless of insolvency state, so the eventual decline screen's
  // stats are real computed values (GR#15), not defaults captured only at the
  // moment of decline. Frozen automatically once declineState is set, because
  // advance() short-circuits before this point on the next call (see the
  // early return at the top of this function).
  const peakPopulation = Math.max(s.peakPopulation ?? s.population, population);
  const minFundsEver = Math.min(s.minFundsEver ?? s.funds, funds);
  const totalSpending = (s.totalSpending ?? 0) + expenseFinal;

  // TICK-BOUNDARY INVARIANT: Record funds at tick end for conservation checking
  // (captured AFTER any bailout injection this tick, so it's a real inflow).
  const fundsAtTickEnd = funds;

  // AC-8 scenario 1: stamp insolvencyPopup ONCE, only on the tick the band
  // transitions INTO 'crisis' from a non-crisis band, so the popup states the
  // conditions exactly once per entry rather than re-appearing every
  // subsequent tick while still in crisis.
  // BUG-496: this MUST compare the RAW band to the PREVIOUS RAW band (both
  // insolvencyStateForFunds output) — comparing the raw band against the
  // EXPOSED previous value (prevInsolvencyState, which reads 'bailout'/
  // 'administration'/'bailout_second' while the raw band is still 'crisis')
  // made "transitioned into crisis" evaluate true on every tick an overlay
  // was active, since the exposed value is never literally 'crisis' then.
  const rawInsolvencyPopup =
    insolvencyState === 'crisis' && prevInsolvencyRawBand !== 'crisis'
      ? { state: insolvencyState, enteredAt: tick }
      : (s.insolvencyPopup ?? null);
  // BUG-497 (1): the popup is moot once the game is over — force-clear it on the
  // very tick declineState is set (declineState is computed above, before this
  // point) so the InsolvencyPopup overlay is never simultaneously mounted with
  // the DeclineScreen overlay; no further tick can undo this because advance()
  // hard-stops the instant declineState is non-null (see the top-of-function guard).
  const insolvencyPopup = declineState !== null ? null : rawInsolvencyPopup;

  // FEAT-1972079923 inc3/inc4 (AC-5, AC-7, AC-10, AC-11): the EXPOSED
  // insolvencyState overlays the pure funds band, highest-precedence overlay
  // first: 'decline' (permanent, AC-11) > 'administration' (AC-5/AC-7) >
  // 'bailout_second' (AC-10, auto-triggered) > the pure funds band itself
  // (which reads 'crisis' while a plain bailoutState is active). `insolvencyState`
  // (the local var above) stays the pure funds-derived band throughout, used
  // only internally for the bailout trigger/popup logic; this is the ONLY
  // place the overlay is applied, so a replay reproduces the exact same
  // exposed value at every tick.
  // FEAT-congestion-teeth-2026-09-02 (AC-1) — advance the per-line sustained-
  // congestion tick counters using THIS tick's OWN traffic snapshot (post
  // auto-scale/growth, so a same-tick road auto-widen or population change
  // is already reflected in the saturation the counter reacts to). Read back
  // ONLY by the NEXT tick's wellbeingOf(s)/computeFlows(s) calls (both read
  // `s`, the state BEFORE this write) — a genuine one-tick LAG, never
  // same-tick self-reference, exactly like BUG-506's recoveryStreak and the
  // crime mechanic's month-lag (data.ts congestionFactorOf's doc comment has
  // the full no-cycle argument: congestion depends only on buildings/
  // population, never on wellbeing).
  const congestionUsages = lineUsageOf({ ...s, buildings: scaledBuildings, population });
  const congestionTicksBySpec = advanceCongestionTicks(
    sanitizeCongestionTicksBySpec(s.congestionTicksBySpec),
    congestionUsages
  );

  const exposedInsolvencyState: InsolvencyState =
    declineState !== null
      ? 'decline'
      : administrationState !== null
        ? 'administration'
        : bailoutSecondState !== null
          ? 'bailout_second'
          : insolvencyState;

  const next: SimState = {
    ...s,
    tick,
    funds,
    fundsAtTickStart,
    fundsAtTickEnd,
    pendingRewards: [], // Drained
    pendingMilestoneRewards: [], // Drained (FEAT-milestone-cash-rewards-2026-09-02)
    population,
    xp: newXp,
    // FEAT-1972079907 inc2: buildings may carry monthly auto-scale tier bumps;
    // roadMonitors has expired entries dropped (both == the inputs off a non-monthly tick).
    buildings: scaledBuildings,
    roadMonitors,
    history: [...s.history, { tick, funds, income, expense: expenseFinal, population }].slice(-HISTORY_CAP),
    // FEAT-1972079925: per-tick demographic flows + the monthly aggregate ring.
    lastDemographics: demographics,
    demographicAccum: nextDemographicAccum,
    demographicHistory,
    // FEAT-1972079926: per-tick arrivals-by-mode split + the monthly aggregate ring.
    lastArrivalsByMode: arrivalsByMode,
    arrivalsByModeAccum: nextArrivalsByModeAccum,
    arrivalsByModeHistory,
    ledger,
    nextLedgerId: nextLedger,
    // FEAT-2326609761 (CONSOLIDATOR, AC-25): newest-first, capped ring —
    // mirrors `ledger`'s own idiom. Unchanged (not even re-referenced) on a
    // tick that ran no pass, or a pass that found nothing to report.
    // FEAT-2326609761 inc2: up to TWO passes can run in one glide-mode day
    // (the glide window + the month-12 whole-map pass) — re-numbered from
    // `consolidatorPriorLogId` (captured before either ran) and reversed so
    // the LAST-run pass (most recent) lands at index 0, exactly like the
    // single-pass case always did.
    consolidatorLog:
      consolidatorPassLogs.length > 0
        ? [
            ...consolidatorPassLogs
              .slice()
              .reverse()
              .map((log, i) => ({ ...log, id: consolidatorPriorLogId + (consolidatorPassLogs.length - i) })),
            ...(s.consolidatorLog ?? []),
          ].slice(0, CONSOLIDATOR_LOG_CAP)
        : s.consolidatorLog,
    // F4 FIX: a NEW pass earns a fresh, one-time undo — reset the
    // single-level consumed-flag whenever a pass is actually appended to the
    // log (never touched on a tick that ran no pass).
    consolidatorUndoConsumed: consolidatorPassLogs.length > 0 ? false : s.consolidatorUndoConsumed,
    // BUG-419: record the START-of-tick population that computeFlows() charged
    // population-scaled flows on (s.population, before the growth update above), so
    // consistency checks recompute Wages/Council Tax against the SAME basis the engine
    // used — not the grown end-of-tick population.
    lastFlows: { inflows, outflows, population: s.population },
    lastRewardedLevel,
    notice: nextNotice,
    milestoneNotice: nextMilestoneNotice,
    insolvencyState: exposedInsolvencyState,
    insolvencyRawBand: insolvencyState,
    insolvencyPopup,
    bailoutState,
    administrationState,
    bailoutSecondState,
    declineState,
    peakPopulation,
    minFundsEver,
    totalSpending,
    // BUG-504 Option A: legacy first-bailout re-arm counter — FEAT-dynamic-
    // bailout RETIRES its re-arm role (no fresh trigger increments it any
    // more; a fresh dynamic grant is gated by `dynamicBailoutUsed`, a bool,
    // not this counter — see fiscal.MAX_FIRST_BAILOUTS's own doc comment).
    // Kept read-only/threaded through so an old save's value survives and
    // `bailoutStandingCostPerTick` still reads a stable number.
    firstBailoutCount,
    // BUG-506 (AC-506-1/2): consecutive-tick sustained-recovery counter.
    recoveryStreak,
    // BUG-506 (AC-506-3/4): rolling window of the last N ticks' funds.
    recentFundsWindow,
    // FEAT-dynamic-bailout: tick-time capital spend from the road/building
    // auto-scale passes counts towards cumulative capex exactly like a
    // player-initiated placement — these ARE real placementCost-derived
    // outflows, just charged by advance() instead of a reducer action.
    //
    // F2 FIX (independent round REJECT, 2026-09-02): `orphanConnectCost` is
    // DELIBERATELY EXCLUDED here — sweepOrphanConnects() (called above, whose
    // result IS `s` by this point) drives every reconnect through the SAME
    // autoConnect() reducer path 'place' uses, and autoConnect() ALREADY
    // writes its own cumulativeCapexSpent increment into the state it
    // returns (engine.ts's autoConnect, `cumulativeCapexSpent: (s.
    // cumulativeCapexSpent ?? 0) + totalCost`). `s.cumulativeCapexSpent`
    // read here is therefore ALREADY the post-sweep total — adding
    // `orphanConnectCost` a second time double-counted every pound the sweep
    // spent (measured: 36,000 real spend -> 72,000 capex). `autoScaleCost`/
    // `buildingAutoScaleCost` are the OPPOSITE case: evaluateRoadMonitors/
    // evaluateBuildingMonitors are pure selectors that never touch
    // cumulativeCapexSpent themselves, so their spend is NOT yet reflected in
    // `s.cumulativeCapexSpent` and MUST be added here exactly once.
    cumulativeCapexSpent: (s.cumulativeCapexSpent ?? 0) + autoScaleCost + buildingAutoScaleCost,
    // FEAT-dynamic-bailout (Aaron ruling Q100045): the ONE-WAY once-only latch
    // — see `dynamicBailoutUsed`'s doc in types.ts. Must be written explicitly
    // here (not left to the `...s` spread above) since it is a genuinely NEW
    // per-tick-computed value, not a passthrough.
    dynamicBailoutUsed,
    // FEAT-congestion-teeth-2026-09-02 (AC-1): this tick's advanced per-line
    // sustained-congestion counters, read by the NEXT tick's wellbeing/income.
    congestionTicksBySpec,
  };

  // FEAT-milestone-cash-rewards-2026-09-02 (Q100047b ruling B1) — detect any
  // MILESTONES newly met using the FULLY-ASSEMBLED `next` state (population/
  // history/funds all finalized this tick, so m5 "Solvent City"'s
  // s.history.slice(-60) read sees this tick's own just-appended history
  // entry). Sanitized EVERY tick (GR#16), not only when something new is
  // found, so a corrupt/legacy claimedMilestones self-heals on the very next
  // tick regardless of milestone state. Newly-met ids are marked claimed
  // IMMEDIATELY (this tick) so an oscillating predicate (m5 in particular —
  // a city can win and lose its 60-tick surplus window repeatedly) can never
  // re-queue a reward once paid; the CASH is queued into
  // pendingMilestoneRewards and paid one tick later by the drain block near
  // the top of this function (same "claim now, pay next tick" split as
  // lastRewardedLevel/pendingRewards). An old save loaded with milestones
  // already met retroactively pays them exactly once, on the first tick that
  // observes them met-but-unrewarded — no special-cased load path needed,
  // this is simply the first evaluation of a freshly-loaded state.
  const sanitizedClaimedMilestones = sanitizeClaimedMilestones(next.claimedMilestones);
  const newlyMetMilestones = computeMilestoneRewards(next, sanitizedClaimedMilestones);
  const patchedNext: SimState =
    newlyMetMilestones.length > 0
      ? {
          ...next,
          claimedMilestones: [...sanitizedClaimedMilestones, ...newlyMetMilestones.map((r) => r.milestoneId)],
          pendingMilestoneRewards: [...next.pendingMilestoneRewards!, ...newlyMetMilestones],
        }
      : { ...next, claimedMilestones: sanitizedClaimedMilestones };

  // FEAT-crime-mechanic-2026-09-02 (Q100069 rec-on-all Q4, "immediate
  // prior-month"): snapshot THIS tick's crime rate for NEXT month's breeding
  // term, but only at month boundaries — `next` still carries the OLD
  // crimeRatePreviousMonth (copied via `...s` above, not yet overwritten), so
  // crimeRateOf(next) reads last month's value and produces the value that
  // becomes new for the month ahead. A non-boundary tick carries the existing
  // field forward unchanged (no per-tick recompute — matches the monthly
  // aggregate idiom every other monthly system here uses, e.g. line 1051).
  if (tick % TICKS_PER_MONTH === 0) {
    return { ...patchedNext, crimeRatePreviousMonth: crimeRateOf(patchedNext) };
  }
  return patchedNext;
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

/**
 * BUG-660: add one building's real footprint (footprintOf — respects a
 * GROWN building, same SSOT autoConnect's own from-scratch board and
 * occupiedSet()/buildOccupiedSet() use) into a Set IN PLACE. The shared,
 * mutating half of the batch-board pattern this bug introduces — the
 * counterpart to occupiedSetIncremental()/roadTileSetIncremental() (data.ts),
 * which instead clone. MUTATES its `set` argument; callers own that choice.
 */
function addFootprintTiles(set: Set<string>, b: Building, sp: Spec): void {
  const { w, h } = footprintOf(b, sp);
  for (let dx = 0; dx < w; dx++)
    for (let dy = 0; dy < h; dy++) set.add(`${b.x + dx},${b.y + dy}`);
}

export function autoConnect(
  s: SimState,
  placed: Building,
  sp: Spec,
  opts?: { notice?: boolean; onUnaffordable?: () => void },
  prebuiltBoard?: { occupied: Set<string>; roads: Set<string> },
): SimState {
  const notice = opts?.notice !== false;
  if (CONNECT_EXEMPT_KINDS.has(sp.kind) || isRoadSpec(sp)) {
    return notice ? { ...s, roadNotice: null } : s;
  }

  // Board sets from the CURRENT buildings (includes the just-placed building).
  // BUG-467 part B: a caller that already maintains a board incrementally in sync
  // with `s` (the orphan-connect sweep) may pass one in via `prebuiltBoard`,
  // skipping this O(n) rebuild-from-ALL-buildings — the per-call cost that made
  // the sweep O(n²) at scale. Single-placement callers (unchanged) still rebuild
  // fresh each time; output is identical either way since the sets' CONTENTS are
  // the same, only how they're assembled differs.
  let occupied: Set<string>;
  let roads: Set<string>;
  if (prebuiltBoard) {
    occupied = prebuiltBoard.occupied;
    roads = prebuiltBoard.roads;
  } else {
    occupied = new Set<string>();
    roads = new Set<string>();
    // Same class as R2-F5a/c (independent round REJECT, 2026-09-03): a
    // GROWN building (FEAT-2326609740) occupies more tiles than its spec's
    // base w/h — use footprintOf so the from-scratch board here agrees with
    // occupiedSet()/debug.json instead of leaking the extra tiles as
    // falsely-free ground a connector could route straight through.
    for (const b of s.buildings) {
      const bs = SPECS[b.spec];
      if (!bs) continue;
      const road = isRoadSpec(bs);
      const { w, h } = footprintOf(b, bs);
      for (let dx = 0; dx < w; dx++)
        for (let dy = 0; dy < h; dy++) {
          const k = `${b.x + dx},${b.y + dy}`;
          occupied.add(k);
          if (road) roads.add(k);
        }
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
    // FEAT-dynamic-bailout: connector + upgrade-on-connect is real capex spend.
    cumulativeCapexSpent: (s.cumulativeCapexSpent ?? 0) + totalCost,
    nextId,
    buildings,
    ledger,
    nextLedgerId,
    roadNotice: null,
    roadMonitors,
  };
}

/**
 * BUG-467 part B: this sweep was O(n²)+ at scale (measured ~110s at ~9,886
 * buildings) from two stacked costs, both eliminated here WITHOUT changing the
 * resulting `SimState` byte-for-byte vs. the prior implementation:
 *
 *  1. `s.buildings.find(b => b.id === id)` per id → O(n) linear scan per
 *     iteration. Fixed with a `Map<id, Building>` built ONCE up front. Safe to
 *     snapshot: the sweep only ever iterates the ids present BEFORE the sweep
 *     started (matching the original `ids` snapshot), and a non-road building's
 *     record (x, y, spec) never mutates mid-sweep — `autoConnect` only ever
 *     mutates ROAD-kind buildings in place (tier upgrade-on-connect), and those
 *     are always skipped early by `autoConnect` itself regardless of the id
 *     order, so the snapshotted `placed` object passed in is always identical
 *     to what a fresh `find` would have returned.
 *
 *  2. `autoConnect` rebuilding the full `occupied`/`roads` tile-string Sets from
 *     ALL buildings on EVERY call. Fixed by building those Sets ONCE before the
 *     loop and threading them through via `autoConnect`'s new optional
 *     `prebuiltBoard` param, kept in sync incrementally as the sweep runs:
 *     tier-upgrades of existing road tiles never change Set membership (a road
 *     tile is a road tile before and after its tier changes), so the ONLY drift
 *     to track is newly laid connector tiles — `autoConnect` always appends
 *     those to the tail of `buildings` (via `.concat(newTiles)`) and bumps
 *     `nextId` by exactly their count, so the tail slice of `next.buildings`
 *     sized `next.nextId - prevNextId` is exactly the new tiles; their footprint
 *     cells are added to both Sets before the next iteration sees them — the
 *     same content a from-scratch rebuild would have produced.
 */
export function sweepOrphanConnects(s: SimState): SimState {
  const ids = s.buildings.map((b) => b.id).sort((a, b) => a - b);
  const byId = new Map<number, Building>();
  for (const b of s.buildings) byId.set(b.id, b);

  const occupied = new Set<string>();
  const roads = new Set<string>();
  // Same class as autoConnect's own from-scratch board above (R2-F5,
  // independent round REJECT, 2026-09-03): use footprintOf so a grown
  // building's extra tiles are counted here too, not just in occupiedSet().
  for (const b of s.buildings) {
    const bs = SPECS[b.spec];
    if (!bs) continue;
    const road = isRoadSpec(bs);
    const { w, h } = footprintOf(b, bs);
    for (let dx = 0; dx < w; dx++)
      for (let dy = 0; dy < h; dy++) {
        const k = `${b.x + dx},${b.y + dy}`;
        occupied.add(k);
        if (road) roads.add(k);
      }
  }

  for (const id of ids) {
    const placed = byId.get(id);
    if (!placed) continue;
    const sp = SPECS[placed.spec];
    if (!sp) continue;
    let unaffordable = false;
    const prevNextId = s.nextId;
    const next = autoConnect(
      s,
      placed,
      sp,
      {
        notice: false,
        onUnaffordable: () => {
          unaffordable = true;
        },
      },
      { occupied, roads },
    );
    if (unaffordable) break;
    if (next.nextId > prevNextId) {
      // New connector tiles were appended at the tail of `buildings` — mirror
      // them into the running board Sets so the NEXT iteration sees exactly
      // what a from-scratch rebuild-from-all-buildings would have seen.
      const added = next.nextId - prevNextId;
      const newTiles = next.buildings.slice(next.buildings.length - added);
      for (const t of newTiles) {
        const ts = SPECS[t.spec];
        if (!ts) continue;
        for (let dx = 0; dx < ts.w; dx++)
          for (let dy = 0; dy < ts.h; dy++) {
            const k = `${t.x + dx},${t.y + dy}`;
            occupied.add(k);
            roads.add(k);
          }
      }
    }
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
  // Same class as autoConnect's board (R2-F5, independent round REJECT,
  // 2026-09-03): use footprintOf so a grown building's extra tiles count as
  // real obstacles the branch router must route around, not free ground.
  for (const b of s.buildings) {
    const bs = SPECS[b.spec];
    if (!bs) continue;
    const { w, h } = footprintOf(b, bs);
    for (let dx = 0; dx < w; dx++)
      for (let dy = 0; dy < h; dy++) {
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
  // FEAT-dynamic-bailout: accumulate real capex spend across every branch laid
  // this call (today £0 per tile, but a future per-tile branch cost must count).
  let branchCapexSpent = 0;

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
    branchCapexSpent += branchCost;
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
    cumulativeCapexSpent: (s.cumulativeCapexSpent ?? 0) + branchCapexSpent,
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

// ════════════════════════════════════════════════════════════════════════════
// FEAT-1972079928 inc1: road re-planning on placement.
//
// Aaron's rulings (BOW comment, 2026-08-31), AUTHORITATIVE over the BA draft's
// softer AC-3/AC-7 wording:
//   (1) DEMOLITION = fully autonomous reroute — Aaron ruled re-plan MAY freely
//       demolish/reroute EXISTING (incl player-placed) ROADS for the optimal
//       layout, no confirmation. IMPLEMENTED (2026-08-31 11:02 pull-forward):
//       beyond reuse/upgrade-in-place, the cascade also identifies existing
//       SUBOPTIMAL through-tiles that this SAME transaction's improvements
//       make provably redundant (bypassed by a surviving alternate path) and
//       demolishes them — see the "demolish/reroute" section of
//       planRoadReplanCascade's doc comment for the full eligibility proof
//       (lower tier, plain through-tile only, no graph fragmentation, no
//       stranded building). A blocked non-road cell is still simply
//       impassable — this pass NEVER demolishes a non-road building.
//   (2) RADIUS = medium trigger zone, but the CASCADE across that zone is
//       computed to completion as ONE atomic transaction before any tile is
//       redrawn/committed — see the "atomic cascade" note on planRoadReplanCascade.
//   (3) HIERARCHY = STRONG preference — the per-tile traversal cost is
//       discounted for higher road tiers, so a route through an existing
//       higher-tier tile can beat a shorter all-local route (AC-3 semantics
//       are superseded by this ruling: hierarchy is a real cost-shaping force,
//       not a tie-breaker only).
//   (4) UPGRADE COST = REPLAN_UPGRADE_COST_FRACTION (90%) of the full new-tier
//       build cost, charged atomically with the rest of the cascade.
//
// Inc1 scope (BA increment split, Aaron-approved, extended by the pull-forward
// ruling above): radius detection (AC-1), deterministic cost function (AC-2),
// strong hierarchy preference (AC-3, strong-variant), atomic all-or-nothing
// funds net of demolition refunds (AC-6), fully autonomous demolish/reroute of
// provably-redundant existing road segments (AC-7 superseded by Aaron's
// 2026-08-31 ruling — the non-destructive default no longer applies; a
// reachable search candidate's own tile is still left alone because it is
// never a plain degree-2 through-tile in this scan, not because of a
// standing non-destructive rule), and clean coexistence with the landed
// auto-junction convert-in-place (AC-8 — the cascade never touches an
// already-converted rd_junction/rd_roundabout/rd_mwyjunction/rd_railbridge
// tile, for upgrades OR demolition). Anti-sprawl minor-into-major limiting
// (AC-4) and motorway junction min-spacing/max-per-segment (AC-5) are
// explicitly deferred to inc2/inc3 (BA's own increment split) — this pass
// does not enforce either.

/** AC-1 placeholder-balance: search radius (tiles) around the new path's bbox. */
export const REPLAN_RADIUS_TILES = 4;

/**
 * AC-2/AC-3 placeholder-balance: how strongly an existing higher road tier
 * discounts the per-tile traversal cost of the re-plan's Dijkstra search.
 * cost(existing tier t) = 1 / (1 + (t-1) × REPLAN_HIERARCHY_WEIGHT) — tier 1
 * costs 1 (no discount), higher tiers cost meaningfully less to reuse, so the
 * search can prefer a longer route through a higher tier over a shorter
 * all-local one (Aaron ruling 3: STRONG preference, not a mere tie-breaker).
 */
export const REPLAN_HIERARCHY_WEIGHT = 3;

/** Aaron ruling (4): upgrade cost fraction of the full new-tier build cost. */
export const REPLAN_UPGRADE_COST_FRACTION = 0.9;

// ────────────────────────────────────────────────────────────────────────────
// FEAT-1972079928 inc2 (AC-5): motorway junction scarcity — minimum spacing.
//
// Aaron's ruling (BOW comment, 2026-08-31 — AUTHORITATIVE over the BA draft's
// softer "spacing OR count" wording): scarcity = MINIMUM SPACING. A new
// motorway junction may only be placed when it is `>= MOTORWAY_JUNCTION_
// MIN_SPACING_TILES` from the NEAREST existing motorway junction. A nearer
// crossing routes via a SLIP to a parallel A-road instead of cutting a direct
// motorway junction (option (a) from the AC-5 doc), or — if no parallel A-road
// is within reach — the crossing is simply suppressed (the connecting road
// does not cross the motorway at that tile at all; no partial state, no cost).
// MOTORWAY_JUNCTION_MAX_PER_SEGMENT is enforced too (belt-and-braces per the
// constants table) but spacing is the ruling's actual mechanism.
// ────────────────────────────────────────────────────────────────────────────

/** AC-5 PLACEHOLDER-balance: min tiles between any two motorway junctions. */
export const MOTORWAY_JUNCTION_MIN_SPACING_TILES = 6;

/** AC-5 PLACEHOLDER-balance: max motorway junctions network-wide (Q5 in the
 * doc asks per-segment vs per-network; the simplest, deterministic, always-
 * computable reading is per-network — see the report's open-question note). */
export const MOTORWAY_JUNCTION_MAX_PER_SEGMENT = 4;

/** AC-5 PLACEHOLDER-balance: how far to search for a parallel A-road (or
 * higher, non-motorway) tile to slip-connect to instead of crossing directly. */
export const MOTORWAY_SLIP_SEARCH_RADIUS_TILES = 4;

/**
 * AC-5: Manhattan-tile distance from (x,y) to the NEAREST existing
 * `rd_mwyjunction` tile in `state.buildings`. Pure function of map state
 * (building positions only) — deterministic, no Date/Math.random, no
 * insertion-order dependence (a min-reduce is order-independent). Returns
 * Infinity when no motorway junction exists yet (nothing to be too close to).
 */
export function nearestMotorwayJunctionDistance(state: SimState, x: number, y: number): number {
  let best = Infinity;
  for (const b of state.buildings) {
    if (b.spec !== 'rd_mwyjunction') continue;
    const d = Math.abs(b.x - x) + Math.abs(b.y - y);
    if (d < best) best = d;
  }
  return best;
}

/** AC-5: total count of existing motorway junctions network-wide. */
export function countMotorwayJunctions(state: SimState): number {
  let n = 0;
  for (const b of state.buildings) if (b.spec === 'rd_mwyjunction') n++;
  return n;
}

/**
 * AC-5: true when a NEW motorway junction at (x,y) would satisfy both the
 * minimum-spacing and max-per-segment(network) rules. Takes the thresholds as
 * PARAMETERS (rather than reading the module consts directly) so a Mutation
 * proof can call this exact function with `minSpacing: 0` / `maxCount:
 * Infinity` — the functional equivalent of zeroing the real consts — without
 * needing to reach into the live ES module bindings (which are read-only from
 * outside the module, same pattern as replanSearch's `radius` parameter).
 */
export function motorwayJunctionSpacingOk(
  state: SimState,
  x: number,
  y: number,
  minSpacing: number,
  maxCount: number
): boolean {
  return nearestMotorwayJunctionDistance(state, x, y) >= minSpacing && countMotorwayJunctions(state) < maxCount;
}

/**
 * AC-5 option (a): search a deterministic raster window (ascending y, then x —
 * never insertion order) around (x,y) for an existing PARALLEL A-road-or-above
 * (roadTier >= 3), non-motorway-class road tile the crossing could slip-
 * connect to instead of cutting a new motorway junction. `roadByTile` is the
 * plain (non-motorway, non-rail) existing-road lookup already built by the
 * `placeRoadPath` case; `excludeTiles` is the tile-set of the path just placed
 * (never slip-target a cell that's part of THIS SAME placement). Returns the
 * FIRST match in raster order, or null when nothing viable is in reach.
 */
export function findMotorwaySlipTarget(
  roadByTile: Map<string, { building: { x: number; y: number }; spec: Spec }>,
  x: number,
  y: number,
  searchRadius: number,
  excludeTiles: Set<string>
): { x: number; y: number } | null {
  for (let dy = -searchRadius; dy <= searchRadius; dy++) {
    for (let dx = -searchRadius; dx <= searchRadius; dx++) {
      if (dx === 0 && dy === 0) continue;
      const cx = x + dx;
      const cy = y + dy;
      const k = `${cx},${cy}`;
      if (excludeTiles.has(k)) continue;
      const found = roadByTile.get(k);
      if (!found) continue;
      if (roadTierOf(found.spec) >= 3) return { x: cx, y: cy };
    }
  }
  return null;
}

/**
 * Auto-placed junction/bridge specs from FEAT-1972079910 inc2/3. The re-plan
 * cascade treats these as ordinary passable road tiles (their roadTier is
 * still meaningful for the hierarchy discount) but NEVER upgrades or
 * re-converts them (AC-8: no double-conversion, no ID churn on landed logic).
 */
const REPLAN_AUTO_JUNCTION_SPECS = new Set([
  'rd_junction',
  'rd_roundabout',
  'rd_mwyjunction',
  'rd_railbridge',
]);

/** One nearby road tile found by replanSearch (AC-1). */
export interface ReplanNearbyRoad {
  x: number;
  y: number;
  buildingId: number;
  spec: string;
  tier: number;
}

/**
 * AC-1: bounding box of `newTiles` expanded by `radius` tiles on every side,
 * clamped to the map bounds. Exported so tests can pin the exact box the
 * cascade searches, independent of the road-tier discount / Dijkstra maths.
 */
export function replanBBox(
  newTiles: { x: number; y: number }[],
  radius: number
): { lo: { x: number; y: number }; hi: { x: number; y: number } } | null {
  if (newTiles.length === 0) return null;
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const t of newTiles) {
    if (t.x < minX) minX = t.x;
    if (t.y < minY) minY = t.y;
    if (t.x > maxX) maxX = t.x;
    if (t.y > maxY) maxY = t.y;
  }
  return {
    lo: { x: Math.max(0, minX - radius), y: Math.max(0, minY - radius) },
    hi: { x: Math.min(MAP_W - 1, maxX + radius), y: Math.min(MAP_H - 1, maxY + radius) },
  };
}

/**
 * AC-1: every road building tile whose footprint falls within `radius` tiles
 * of the bounding box of `newTiles` — EXCLUDING tiles that are themselves part
 * of `newTiles` (the path just placed/converted by this same action). Pure,
 * deterministic (iterates `state.buildings` in its existing, stable order —
 * no Date/Math.random, no reliance on insertion timing beyond that order).
 */
export function replanSearch(
  state: SimState,
  newTiles: { x: number; y: number }[],
  radius: number
): ReplanNearbyRoad[] {
  const box = replanBBox(newTiles, radius);
  if (!box) return [];
  const { lo, hi } = box;
  const newSet = new Set(newTiles.map((t) => `${t.x},${t.y}`));
  const results: ReplanNearbyRoad[] = [];
  for (const b of state.buildings) {
    const sp = SPECS[b.spec];
    if (!sp || !isRoadSpec(sp)) continue;
    for (let dx = 0; dx < sp.w; dx++) {
      for (let dy = 0; dy < sp.h; dy++) {
        const x = b.x + dx;
        const y = b.y + dy;
        if (x < lo.x || x > hi.x || y < lo.y || y > hi.y) continue;
        const k = `${x},${y}`;
        if (newSet.has(k)) continue;
        results.push({ x, y, buildingId: b.id, spec: b.spec, tier: roadTierOf(sp) });
      }
    }
  }
  return results;
}

/** The atomic re-plan cascade computed by planRoadReplanCascade, pre-apply. */
interface ReplanCascade {
  /** Existing road tiles upgraded IN PLACE (id preserved) to `newTier`. */
  conversions: Map<string, { buildingId: number; newSpec: string; cost: number }>;
  /** Brand-new connector tiles placed on empty cells discovered by the search. */
  newTilePlacements: Array<{ x: number; y: number; spec: string; cost: number }>;
  /**
   * FEAT-1972079928 inc1 EXTENSION (Aaron ruling 2026-08-31 11:02 — "pull
   * demolish-reroute into inc1"): existing SUBOPTIMAL road tiles torn down
   * because this SAME cascade proved them redundant — bypassed by a surviving
   * alternate path, so keeping them is pure sprawl. See the demolish/reroute
   * section of planRoadReplanCascade's doc comment for the full eligibility
   * proof (through-tile only, lower tier, no network fragmentation, no
   * stranded building). Refund only (25%, the `bulldoze` convention) — the
   * tile is proven redundant, not rebuilt, so nothing new is laid at its cell.
   */
  demolitions: Map<string, { buildingId: number; refund: number }>;
  /** Net cost: conversions + new tiles, MINUS demolition refunds (Aaron: "net of any demolition refund/cost"). */
  totalCost: number;
}

/**
 * FEAT-1972079928 inc1 (AC-1, AC-2, AC-3 strong-variant, AC-6, AC-7, AC-8):
 * compute the FULL re-plan cascade for the region around `newTiles` — the
 * whole thing is one pure function call that returns a plan object with NO
 * side effects. This is the atomic-cascade-before-redraw mechanism Aaron
 * flagged as the load-bearing correctness AC: the caller (the `placeRoadPath`
 * reducer case) does not touch `buildings`/`funds` until this function has
 * ALREADY finished computing every conversion/placement and their total cost —
 * there is no code path that redraws tile N before tile N+1's cost has been
 * decided, because nothing is written until the whole cascade is known. A
 * reducer call is a single synchronous JS stack frame, so "compute fully, then
 * commit" is structural, not a convention: this function returns a plan, and
 * the caller either applies the WHOLE plan in one state transition or (if
 * unaffordable) applies NONE of it — never a partial application.
 *
 * Algorithm: a deterministic multi-source Dijkstra (no heap — a plain O(V²)
 * "scan for the unvisited min" loop over the (small, radius-bounded) bbox, so
 * ties are broken by a fixed ascending "x,y" string compare, never by
 * insertion order or Math.random) from every `newTiles` cell (distance 0)
 * across the search bbox. Traversal cost per cell:
 *   - a `newTiles` cell itself: 0 (already free, part of this placement).
 *   - an existing road tile (not an auto-junction spec): hierarchy-discounted
 *     reuse cost, see REPLAN_HIERARCHY_WEIGHT — strong preference for tier.
 *   - an existing auto-junction/bridge tile (AC-8): same discount, by its own
 *     roadTier, but flagged non-upgradable (never re-converted).
 *   - an empty, buildable cell: base cost 1 (a brand-new connector tile would
 *     be placed there, at the FULL cost of the new road's tier/spec).
 *   - any other occupied cell (a non-road building): impassable (Infinity) —
 *     inc1 does not demolish non-road buildings to force a route through them.
 *
 * For each `replanSearch` candidate NOT already reachable (dist === Infinity,
 * i.e. boxed in), skip it — a safe, deterministic no-op, never a partial plan.
 * Otherwise walk `prev` back from the candidate to its source, and for every
 * INTERMEDIATE cell (excluding the candidate's own tile — AC-7 non-destructive
 * default — and excluding `newTiles` cells, already free):
 *   - an existing lower-tier, non-auto-junction road tile is upgraded in place
 *     to the new tier, charged REPLAN_UPGRADE_COST_FRACTION × its full cost
 *     (Aaron ruling 4).
 *   - an empty cell gets a brand-new tile at the new tier, full cost.
 * Cells shared by more than one candidate's path are only counted/charged
 * once (a plain Map keyed by cell, built incrementally in one pass).
 */
function planRoadReplanCascade(
  state: SimState,
  newTiles: { x: number; y: number }[],
  newRoadTier: number
): ReplanCascade | null {
  const nearby = replanSearch(state, newTiles, REPLAN_RADIUS_TILES);
  if (nearby.length === 0) return null;

  const box = replanBBox(newTiles, REPLAN_RADIUS_TILES);
  if (!box) return null;
  const { lo, hi } = box;
  const newSet = new Set(newTiles.map((t) => `${t.x},${t.y}`));

  type Cell =
    | { kind: 'road'; tier: number; auto: boolean; buildingId: number; spec: string }
    | { kind: 'blocked' };
  const grid = new Map<string, Cell>();
  // Same class as autoConnect's board (R2-F5, independent round REJECT,
  // 2026-09-03): a grown non-road building's extra tiles must show as
  // 'blocked' here too via footprintOf, not just its spec-base rect (roads
  // themselves never grow via this ladder, so their own tier/auto reads are
  // unaffected — only the 'blocked' branch for non-road buildings matters).
  for (const b of state.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const { w, h } = footprintOf(b, sp);
    for (let dx = 0; dx < w; dx++) {
      for (let dy = 0; dy < h; dy++) {
        const x = b.x + dx;
        const y = b.y + dy;
        if (x < lo.x || x > hi.x || y < lo.y || y > hi.y) continue;
        const k = `${x},${y}`;
        if (isRoadSpec(sp)) {
          grid.set(k, { kind: 'road', tier: roadTierOf(sp), auto: REPLAN_AUTO_JUNCTION_SPECS.has(b.spec), buildingId: b.id, spec: b.spec });
        } else {
          grid.set(k, { kind: 'blocked' });
        }
      }
    }
  }

  const stepCost = (k: string): number => {
    if (newSet.has(k)) return 0;
    const cell = grid.get(k);
    if (!cell) return 1; // empty, buildable — a new connector tile would go here
    if (cell.kind === 'blocked') return Infinity;
    // Existing road reuse: strong hierarchy discount (Aaron ruling 3).
    return 1 / (1 + (cell.tier - 1) * REPLAN_HIERARCHY_WEIGHT);
  };

  const cellsInBox: string[] = [];
  for (let y = lo.y; y <= hi.y; y++) {
    for (let x = lo.x; x <= hi.x; x++) cellsInBox.push(`${x},${y}`);
  }

  const dist = new Map<string, number>();
  const prev = new Map<string, string | null>();
  const visited = new Set<string>();
  for (const t of newTiles) {
    const k = `${t.x},${t.y}`;
    dist.set(k, 0);
    prev.set(k, null);
  }

  for (;;) {
    let bestKey: string | null = null;
    let bestDist = Infinity;
    for (const k of cellsInBox) {
      if (visited.has(k)) continue;
      const d = dist.get(k);
      if (d === undefined) continue;
      if (d < bestDist || (d === bestDist && (bestKey === null || k < bestKey))) {
        bestDist = d;
        bestKey = k;
      }
    }
    if (bestKey === null) break;
    visited.add(bestKey);
    const [bx, by] = bestKey.split(',').map(Number);
    const neighbors: [number, number][] = [[bx + 1, by], [bx - 1, by], [bx, by + 1], [bx, by - 1]];
    for (const [nx, ny] of neighbors) {
      if (nx < lo.x || nx > hi.x || ny < lo.y || ny > hi.y) continue;
      const nk = `${nx},${ny}`;
      if (visited.has(nk)) continue;
      const c = stepCost(nk);
      if (!Number.isFinite(c)) continue;
      const nd = bestDist + c;
      const cur = dist.get(nk);
      if (cur === undefined || nd < cur) {
        dist.set(nk, nd);
        prev.set(nk, bestKey);
      }
    }
  }

  const newTierSpecId = ROAD_TIER_SPECS[newRoadTier as RoadTier];
  const newTierSpec = newTierSpecId ? SPECS[newTierSpecId] : undefined;
  if (!newTierSpec) return null;

  const conversions = new Map<string, { buildingId: number; newSpec: string; cost: number }>();
  const newTilePlacements: Array<{ x: number; y: number; spec: string; cost: number }> = [];
  const plannedCells = new Set<string>();

  for (const cand of nearby) {
    const candKey = `${cand.x},${cand.y}`;
    if (dist.get(candKey) === undefined) continue; // boxed in — skip, no partial plan
    // Walk the path back to its source, collecting intermediate cells only.
    let cur: string | null = candKey;
    const pathCells: string[] = [];
    while (cur !== null) {
      pathCells.push(cur);
      cur = prev.get(cur) ?? null;
    }
    // pathCells[0] is the candidate itself (excluded, AC-7), the last entry is
    // the newTiles source (already free) — everything between is "intermediate".
    for (let i = 1; i < pathCells.length - 1; i++) {
      const k = pathCells[i];
      if (plannedCells.has(k) || newSet.has(k)) continue;
      plannedCells.add(k);
      const cell = grid.get(k);
      if (!cell) {
        // Empty cell: brand-new connector tile at the new path's tier, full cost.
        const [x, y] = k.split(',').map(Number);
        newTilePlacements.push({ x, y, spec: newTierSpecId, cost: placementCost(newTierSpec) });
      } else if (cell.kind === 'road' && !cell.auto && cell.tier < newRoadTier) {
        // Existing lower-tier road: upgrade in place at 90% of the full cost.
        conversions.set(k, {
          buildingId: cell.buildingId,
          newSpec: newTierSpecId,
          cost: Math.round(placementCost(newTierSpec) * REPLAN_UPGRADE_COST_FRACTION),
        });
      }
      // Auto-junction tiles and already-sufficient-tier road tiles: free reuse,
      // no change (AC-8 no-double-conversion; strong-hierarchy preference
      // already made this route cheap enough to be selected).
    }
  }

  // ──────────────────────────────────────────────────────────────────────────
  // FEAT-1972079928 inc1 EXTENSION (Aaron ruling 2026-08-31 11:02): demolish
  // /reroute of EXISTING suboptimal road tiles, still inside this SAME pure,
  // atomic, compute-before-redraw cascade — nothing above has been applied to
  // `state` yet, so this is just a second deterministic pass over the SAME
  // fully-computed plan, not a second transaction.
  //
  // A tile R is eligible for demolition ONLY if ALL of the following hold
  // (every check is a pure function of map state — no Date/Math.random):
  //   1. R is a plain road tile (not `rd_junction`/`rd_roundabout`/
  //      `rd_mwyjunction`/`rd_railbridge` — AC-8 safety: the landed
  //      convert-in-place outputs are never touched by this pass).
  //   2. R is strictly LOWER tier than the road just placed/upgraded here —
  //      "suboptimal" is always relative to what this placement just built.
  //   3. R is a plain THROUGH tile: EXACTLY 2 road-adjacent neighbours in the
  //      POST-cascade grid (this transaction's own upgrades/new tiles already
  //      folded in). A dead-end (0-1 neighbours, e.g. the AC-7 "stranded
  //      candidate" this same function reconnects elsewhere) or a junction/
  //      branch (3+ neighbours) is NEVER a demolition candidate — only a
  //      plain relay segment can ever be "bypassed".
  //   4. REDUNDANCY — the actual "a better alignment exists" proof: with R
  //      excluded, R's two road-neighbours must still reach EACH OTHER via
  //      some OTHER surviving path (a local BFS over the post-cascade grid).
  //      If they can't, R is a bridge in the graph — load-bearing, never
  //      demolished, no exception.
  //   5. NO-STRANDING (safety-critical, mechanically checked, not assumed):
  //      every non-road building whose footprint touches R must keep AT
  //      LEAST ONE OTHER road-adjacent tile once R is gone. If any building
  //      would lose its last adjacent road tile, R is skipped — never
  //      demolished. This is the direct proxy for `isOnline`'s G2 road-
  //      adjacency gate (data.ts) — a demolition this function allows can
  //      never itself flip a building's online gate to false.
  //
  // Candidates are walked in the SAME deterministic `cellsInBox` raster order
  // used by the Dijkstra pass above (ascending "x,y"), and each accepted
  // demolition is removed from the working grid BEFORE the next candidate is
  // evaluated — so a chain of several redundant tiles is proven safe
  // incrementally (never a stale, pre-demolition view that could let two
  // simultaneous removals jointly strand something neither would alone).
  // This bounds the pass to one linear scan — no fixed-point loop, no re-
  // triggering, so a single placement can never thrash.
  const demolitions = new Map<string, { buildingId: number; refund: number }>();

  const postGrid = new Map<string, Cell>(grid);
  for (const [k, conv] of conversions) {
    const existing = postGrid.get(k);
    if (existing && existing.kind === 'road') {
      postGrid.set(k, { kind: 'road', tier: newRoadTier, auto: existing.auto, buildingId: existing.buildingId, spec: conv.newSpec });
    }
  }
  for (const p of newTilePlacements) {
    postGrid.set(`${p.x},${p.y}`, { kind: 'road', tier: newRoadTier, auto: false, buildingId: -1, spec: p.spec });
  }

  const roadNeighbors = (k: string, g: Map<string, Cell>): string[] => {
    const [x, y] = k.split(',').map(Number);
    const around = [`${x + 1},${y}`, `${x - 1},${y}`, `${x},${y + 1}`, `${x},${y - 1}`];
    return around.filter((nk) => {
      const c = g.get(nk);
      return c !== undefined && c.kind === 'road';
    });
  };

  for (const k of cellsInBox) {
    if (newSet.has(k) || plannedCells.has(k) || conversions.has(k)) continue;
    const original = grid.get(k);
    if (!original || original.kind !== 'road' || original.auto) continue; // check 1
    if (original.tier >= newRoadTier) continue; // check 2

    const neighborKeys = roadNeighbors(k, postGrid);
    if (neighborKeys.length !== 2) continue; // check 3

    // Check 4: redundancy — BFS from one neighbour to the other, R walled off.
    const [na, nb] = neighborKeys;
    const seen = new Set<string>([k]);
    seen.add(na);
    const queue = [na];
    let reachable = false;
    while (queue.length > 0) {
      const cur = queue.shift() as string;
      if (cur === nb) { reachable = true; break; }
      for (const nk of roadNeighbors(cur, postGrid)) {
        if (seen.has(nk)) continue;
        seen.add(nk);
        queue.push(nk);
      }
    }
    if (!reachable) continue; // R is load-bearing — never demolish

    // Check 5: no-stranding — every REAL (road-access-requiring) building
    // touching R must keep at least one OTHER road-adjacent tile once R is
    // removed. CONNECT_EXEMPT_KINDS (road/motorway/rail/station/pylon) are
    // infrastructure THEMSELVES — they never need road adjacency (mirrors
    // `isOnline`'s own G2 gate, data.ts), so they never block a demolition.
    const [rx, ry] = k.split(',').map(Number);
    let strandsSomething = false;
    for (const b of state.buildings) {
      const sp = SPECS[b.spec];
      if (!sp || isRoadSpec(sp) || CONNECT_EXEMPT_KINDS.has(sp.kind)) continue;
      // F5s (independent round REJECT, 2026-09-03): a grown building
      // (FEAT-2326609740) occupies more tiles than sp.w/sp.h — this
      // function's OWN blocked-grid above (footprintOf'd) already knows
      // that, so the no-stranding guard here must agree, or a grown
      // building's EXTRA tile (its only real road frontage) is invisible to
      // both the "does it touch R" and "does it keep access" checks, and a
      // road this function allows to demolish silently flips the building's
      // isRoadAdjacent gate false — exactly what the guard's own contract
      // (this function never itself causes an online->offline flip) forbids.
      const { w: fpW, h: fpH } = footprintOf(b, sp);
      let touchesR = false;
      for (let dx = 0; dx < fpW && !touchesR; dx++) {
        for (let dy = 0; dy < fpH && !touchesR; dy++) {
          const bx = b.x + dx, by = b.y + dy;
          // Orthogonal adjacency to R — NOT footprint overlap (a non-road
          // building can never occupy R's own cell, since grid cells are
          // exclusively road-or-blocked; the stranding risk is a building
          // sitting NEXT TO R that relies on R as its road access).
          if ((bx === rx && Math.abs(by - ry) === 1) || (by === ry && Math.abs(bx - rx) === 1)) touchesR = true;
        }
      }
      if (!touchesR) continue;
      let keepsAccess = false;
      for (let dx = 0; dx < fpW && !keepsAccess; dx++) {
        for (let dy = 0; dy < fpH && !keepsAccess; dy++) {
          const bx = b.x + dx, by = b.y + dy;
          const around: [number, number][] = [[bx + 1, by], [bx - 1, by], [bx, by + 1], [bx, by - 1]];
          for (const [ax, ay] of around) {
            if (ax === rx && ay === ry) continue; // R itself no longer counts
            const c = postGrid.get(`${ax},${ay}`);
            if (c && c.kind === 'road') { keepsAccess = true; break; }
          }
        }
      }
      if (!keepsAccess) { strandsSomething = true; break; }
    }
    if (strandsSomething) continue;

    // Eligible: demolish R (25% refund, the `bulldoze` convention elsewhere
    // in this reducer), then remove it from the WORKING grid so later
    // candidates in this same deterministic scan see the up-to-date graph.
    const refundSpec = SPECS[original.spec];
    const refund = refundSpec ? Math.round(placementCost(refundSpec) * BULLDOZE_REFUND_FRACTION) : 0;
    demolitions.set(k, { buildingId: original.buildingId, refund });
    postGrid.delete(k);
  }

  if (conversions.size === 0 && newTilePlacements.length === 0 && demolitions.size === 0) return null;

  let totalCost = 0;
  for (const c of conversions.values()) totalCost += c.cost;
  for (const p of newTilePlacements) totalCost += p.cost;
  for (const d of demolitions.values()) totalCost -= d.refund;

  return { conversions, newTilePlacements, demolitions, totalCost };
}

export type Action =
  | { type: 'tick' }
  | { type: 'speed'; speed: SimState['speed'] }
  | { type: 'tool'; tool: Tool }
  // ROUND r3 FIX (2026-09-04, F1/F2): a 'place' action carries NO
  // affordability-confirmation field. Round r2 (INDEPENDENT DESTRUCTIVE,
  // GR#23) REJECTED an earlier design that gated this reducer case on a
  // `confirmAffordability` flag: every replay path (restoreFromSavepoint's
  // tail loop, replayTailChunked, replayFromGenesis) drives THIS SAME
  // reducer case, so a pre-existing journal's 'place' entries — recorded
  // before the flag existed — silently lost their building on the very next
  // load (F1, BLOCKING), and the resulting notice had no UI reader at all
  // (F2, BLOCKING) — a permanent, feedback-free no-op in the live app. THE
  // FIX: the reducer stays PURE and ALWAYS places once its own funds/unlock/
  // bounds/fits checks pass — exactly as before BUG-652 ever touched this
  // case — because a reducer that can refuse a journalled action breaks
  // replay by construction. The placement-affordability CONFIRMATION now
  // lives entirely at the DISPATCH site (MapView.tsx's build-tool click
  // handler calls data.ts's placementAffordability() BEFORE ever
  // constructing this action) — never in SimState, never journaled.
  | { type: 'place'; spec: string; x: number; y: number }
  // BUG b2d31bc7 FIX 3 — atomic batch placement for drag-painting a run of
  // non-road buildings (mirrors placeRoadPath's atomic-dispatch pattern below
  // for the road case). Tiles are placed in ARRAY ORDER (the drag buffer's
  // insertion order — deterministic, GR#21); a tile that no longer fits (e.g.
  // the drag revisited a cell) is skipped, and placement stops the moment
  // funds run out, same affordability rule as single 'place'.
  | { type: 'placeMany'; spec: string; tiles: { x: number; y: number }[] }
  | { type: 'placeRoadPath'; spec: string; tiles: { x: number; y: number }[] }
  // FEAT-2326609728 — one-click demand fix: bulk-place demandFixPlan(state)'s
  // count for one service, via the existing single-tile 'place' path.
  | { type: 'resolveDemand'; serviceKey: string }
  // BUG-606 fix-all (Aaron, 2026-09-03): runs the SAME per-service fix for
  // EVERY service orderedDemandFixPlan(state) currently lists (Health first,
  // then demand-descending), one journaled action so the DemandDock's "Fix
  // All" button reports ONE aggregate placeNotice ("built X, Y skipped")
  // instead of N per-service dispatches silently overwriting each other's
  // notice — see the reducer case below (a thin loop over the SAME
  // placePlanItem() helper 'resolveDemand' uses, no second placement path).
  | { type: 'resolveDemandAll' }
  | { type: 'bulldoze'; x: number; y: number }
  | { type: 'sellAsset'; id: number }
  | { type: 'enterAdministration' }
  | { type: 'pickup'; id: number }
  | { type: 'relocate'; x: number; y: number }
  | { type: 'cancelMove' }
  | { type: 'pipeUpgrade'; id: number }
  | { type: 'tax'; which: keyof TaxRates; rate: number }
  | { type: 'policy'; id: PolicyId }
  | { type: 'toggleGridImport' }
  // FEAT-2326609761 inc1 (AC-1, ASM-1504): the CONSOLIDATOR enable toggle —
  // journalled sim state, deliberately NOT localStorage (see the field's own
  // doc comment on SimState.consolidatorEnabled, types.ts).
  | { type: 'toggleConsolidator' }
  // FEAT-2326609761 inc2 (Aaron's glide-mode ruling, 2026-09-04): switches
  // between glide (default) and monthly-twelfth traversal.
  | { type: 'setConsolidatorMode'; mode: ConsolidatorMode }
  // FEAT-2326609761 inc2 (Aaron's ruling, 2026-09-03): player-adjustable
  // section/window size in metres — clamped via clampConsolidatorSectionMetres.
  | { type: 'setConsolidatorSectionMetres'; metres: number }
  // FEAT-2326609761 inc2 (Aaron's slider ruling, 2026-09-03): the four
  // non-dwelling direction sliders — refused (no-op) unless they sum to 100
  // (validateConsolidatorSliders).
  | { type: 'setConsolidatorSliders'; sliders: ConsolidatorSliders }
  | { type: 'loan' }
  | { type: 'repay' }
  | { type: 'setClipboard'; clipboard: SimState['clipboard'] }
  | { type: 'stampRegion'; clipboard: SimState['clipboard']; x: number; y: number }
  | { type: 'debugFunds'; amount: number }
  | { type: 'debugXp'; amount: number }
  | { type: 'dismissNotice' }
  | { type: 'dismissMilestoneNotice' }
  | { type: 'dismissPlaceNotice' }
  | { type: 'dismissInsolvencyPopup' }
  | { type: 'unlockAll' }
  | { type: 'reset' }
  | {
      type: 'hydrate';
      state: SimState;
      /**
       * BUG-677: which kind of state hand-off this is. 'load' (the default,
       * so every existing call site and journal entry keeps its meaning) is
       * a savepoint/replay/snapshot restore and runs the once-per-load
       * ceremonies (AC-31 over-cap notice). 'tick' is the web-worker
       * delivering ONE advanced tick (store.tsx's worker.onmessage) — it
       * arrives up to once per second (6.25×/s in turbo), so per-load
       * ceremonies must NOT run: the AC-31 scan re-stamped placeNotice on
       * every tick, making the over-cap notice undismissable, and paid an
       * O(buildings) countOfSpec sweep per capped spec per tick.
       */
      source?: 'load' | 'tick';
    }
  // FEAT-2326609723: Play Mode's one-way sandbox escape hatch, reachable from
  // the Decline screen (and idempotent thereafter — see the reducer case).
  | { type: 'enterPlayMode' }
  // FEAT-2326609761 (CONSOLIDATOR mutation lane, AC-26): reverses exactly
  // the last pass (consolidatorLog[0]), or is a no-op (state returned by
  // reference identity) if the log is empty. Single-level by design
  // (ASM-1502). (The toggle itself, 'toggleConsolidator', is declared once
  // above, above 'loan' — landed by the read-only lane at 893511b.)
  | { type: 'consolidatorUndo' };

// FEAT-1972079891 inc1 (AC-12): the internal reducer. `reducer` (below) wraps it
// to keep roadConnectivity consistent with buildings after every action.
// BUG b2d31bc7 FIX 2 — the reducer wrapper (reducer(), below reduceCore) used
// to recompute computeRoadConnectivity (~1.8ms full BFS) on EVERY action that
// changed `buildings`, even a placement that provably cannot have touched the
// road graph (a residential lot dropped onto an already-connected block, no
// autoConnect connector laid). Conservative default TRUE (recompute, today's
// behaviour) — only the specific case handlers below that can PROVE no road/
// trunk tile was added or removed set this to false for their own action, so
// every action type not explicitly touched here keeps recomputing exactly as
// before (no correctness risk from an un-audited case). Reset to true at the
// top of every reducer() call so a stale false can never leak across actions.
let roadTopologyMayHaveChanged = true;

// BUG-583 FIX: a bare `+ 's'` on a catalogue spec name (`sp.name`) breaks the
// instant a name already ends in 's' — "Water Works" becomes "Water Workss",
// and the same naive suffix mangles any other irregular/plural-looking name
// the catalogue ever gains (data.ts has ~150 spec names, e.g. "ADX Supermax"
// -> "ADX Supermaxs" would be equally wrong under English pluralisation).
// Sidestep English pluralisation entirely rather than special-case every
// name: report the count as "N x <Name>" ("2 x Water Works"), which reads
// correctly for every name in SPECS with the SAME format regardless of
// whether the name is singular, plural, or ends in 's'.
function formatPlacedCount(count: number, specName: string): string {
  return `${count} x ${specName}`;
}

/**
 * D2 (BUG-606 independent round REJECT, Aaron 2026-09-03): the true reason a
 * demandFixPlan() item's placement stopped short — 'resolveDemand' and
 * 'resolveDemandAll' both used to skip straight to noBuildableSiteReason()
 * whenever funds were ample, even when the REAL cause was Administration
 * Mode's discretionary-spend freeze (a player under administration with £1T
 * on hand would see "no free area on the map", or resolveDemandAll's blanket
 * "funds ran out", for a service the map had plenty of room for). Priority
 * mirrors placePlanItem()'s own gate order: administration block (checked
 * FIRST there) > insufficient funds > no buildable site. A £0 (free-zone)
 * spec can never be administration- or funds-blocked (cost>0 guards both),
 * so it always resolves to the real site reason.
 */
function shortfallReason(state: SimState, sp: Spec, specId: string): string {
  const cost = placementCost(sp);
  if (cost > 0 && state.administrationState) return ADMINISTRATION_PLACE_BLOCKED_MESSAGE;
  if (cost > 0 && state.funds < cost) return 'insufficient funds';
  return noBuildableSiteReason(specId);
}

/**
 * Shared bulk-placement core for ONE demandFixPlan() item — extracted from
 * 'resolveDemand' (BUG-606) so the new 'resolveDemandAll' (Fix All) reducer
 * case can loop over MULTIPLE services through the exact same per-tile
 * 'place' path/affordability/road-topology-flag logic, with no second
 * placement mechanism (GR#3 SSOT). Places up to `item.count` units of
 * `item.specId`, stopping the moment funds/administration-mode/findSpot
 * decline — identical contract 'resolveDemand' always had, just factored out.
 * Never mutates `cur`; returns the (possibly unchanged) resulting state.
 */
/**
 * PERF CAP (independent round r2, Aaron 2026-09-03): `unitCap` bounds how
 * many units THIS SINGLE CALL may place, regardless of `item.count` — a real
 * dogfood city can plan tens of thousands of units for one service alone
 * (pop 3M -> 1,000 parks units measured), and each unit walks a full
 * findSpot()/'place' pass, so an uncapped loop is the synchronous-freeze
 * hazard (measured 46.6s at pop 400k, DNF at pop 3M). Defaults to Infinity
 * (no cap) only for callers that have their own reason not to need one; both
 * real call sites ('resolveDemand'/'resolveDemandAll' below) always pass a
 * real number. `cappedByUnitLimit` is true ONLY when the cap itself (not
 * funds/administration/no-site) is what stopped this call short — i.e. every
 * one of the `unitCap` attempted units actually placed — so the caller can
 * report the TRUE reason (GR#1 aggressive error trapping: never blame
 * "insufficient funds" for a perf guard).
 */
/**
 * BUG-660 (Aaron, P1, "Fix-All 2000-unit batch still blocks ~66s median" —
 * the residual left AFTER BUG-646's spot-search fix): placePlanItem()'s loop
 * calls reduceCore({type:'place',...}) once per unit, and the 'place' case
 * hands autoConnect() a per-CALL occupied/road board via
 * occupiedSetIncremental()/roadTileSetIncremental() (data.ts). Those
 * functions are a cache HIT on their BASE Set (occupiedSet(state) /
 * roadTileSetOf(state)) only when the previous iteration's result changed
 * buildings by EXACTLY one append — true when a placement is already
 * road-adjacent, but the instant autoConnect lays a REAL connector (routes
 * around occupied tiles to the nearest road — the "road-BFS" this bug is
 * named for) it appends the connector's tiles too, so `state.buildings`
 * grows by MORE than one element. The next iteration's `fits(occupiedSet(
 * state), ...)` guard (case 'place', below) then sees a brand-new array
 * reference with NO cached entry and pays a full O(buildings) rebuild — and
 * even where the cache does hit, occupiedSetIncremental() itself does an
 * UNCONDITIONAL `new Set(base)` full clone every call (data.ts), unlike its
 * sibling roadTileSetIncremental()'s copy-on-write. Measured directly against
 * Aaron's real 49,174-building save (test/scratch/profile-bug660.mjs, kept
 * out of the shipped test tree): a fresh-clone autoConnect call costs
 * ~10.5ms (of which ~2.7ms is the BFS/board-mutation work itself and the
 * rest is Set-cloning + cache-miss rebuild overhead) — at a 2000-unit batch
 * that waste alone accounts for double-digit seconds, compounding with every
 * unit that is NOT already road-adjacent (the common case away from existing
 * road frontage).
 *
 * THE FIX: placePlanItem() builds ONE mutable board ({occupied, roads} Sets)
 * from `cur` ONCE, up front — exactly mirroring the spotCtx pattern two
 * paragraphs below (BUG-646's own precedent) — and threads it through every
 * reduceCore('place') call in this loop via the new optional `batchBoard`
 * param. The 'place' case (below) then uses THAT shared board directly for
 * both its own `fits()` guard and the board it hands autoConnect(), MUTATING
 * it in place with whatever tiles actually got added (the unit itself, plus
 * any connector/branch tiles autoConnect appends) instead of ever cloning it
 * — collapsing the per-unit board cost from O(buildings) to O(footprint),
 * the same class of fix BUG-646 already applied to findSpot(). This changes
 * ONLY which board object gets read/mutated; the CONTENTS at every step are
 * provably identical to what occupiedSet(state)/roadTileSetOf(state) would
 * have produced from scratch (same tiles added in the same order), so the
 * resulting placements/connector paths are byte-identical to the pre-fix
 * behaviour — see attack-bug660-round.test.mjs's cross-check against the
 * non-batch per-placement path over the same plan. Single-tile 'place',
 * 'placeMany' (drag-paint) and 'stampRegion' never pass batchBoard, so their
 * behaviour and cost are completely unchanged by this fix.
 */
function placePlanItem(
  cur: SimState,
  item: DemandFixPlanItem,
  unitCap: number = Infinity
): { state: SimState; placed: number; roadTopologyChange: boolean; cappedByUnitLimit: boolean } {
  const sp = SPECS[item.specId];
  if (!canEnterSim(sp) || !specUnlocked(cur, sp)) {
    return { state: cur, placed: 0, roadTopologyChange: false, cappedByUnitLimit: false };
  }
  const cost = placementCost(sp);
  let s2 = cur;
  let placed = 0;
  // See BUG-566 FIX note (originally on 'resolveDemand', preserved here
  // verbatim): aggregate the shared roadTopologyMayHaveChanged hazard with OR
  // across every placement in this item's batch, not just the last iteration.
  let anyRoadTopologyChange = isRoadOrTrunkSpec(sp);
  const target = Math.min(item.count, unitCap);
  // BUG-646 (Aaron, 2026-09-03, raising the cap 250 -> 2000): a batched
  // spot-search context (data.ts createSpotSearchContext) replaces the
  // per-unit findSpot(s2, ...) call here — findSpot() itself recomputes
  // occupiedSet(s2)/tagged/resList from s2.buildings on EVERY call, an
  // O(buildings) cost that used to be paid AGAIN for every single unit
  // because s2.buildings is a new array reference after each placement
  // (measured: 68ms/unit at 29,831 buildings, dominated by that rebuild, not
  // by the O(window) scanWindow() search itself). The context instead builds
  // occ/tagged/resList ONCE from `cur` and updates them incrementally via
  // occupy() with exactly the buildings 'place' actually added (the target
  // unit AND any auto-connector tiles), so every subsequent findNext() call
  // sees the true, up-to-date occupied set without re-scanning the whole
  // buildings array — collapsing the per-unit cost to O(window), matching
  // scanWindow()'s own bound. See createSpotSearchContext()'s doc comment
  // (data.ts) for the full measurement and the one assumption (never a
  // residential spec) this depends on.
  const spotCtx = createSpotSearchContext(s2, item.specId);
  // BUG-660: the shared, mutated-in-place connectivity board for THIS whole
  // batch — see this function's own doc comment above. Seeded from `cur`
  // (occupiedSet/roadTileSetOf are themselves memoised, so this is a cache
  // hit whenever `cur` was already touched this tick, and at worst a single
  // O(buildings) rebuild for the WHOLE batch rather than one per unit).
  const batchBoard = { occupied: new Set(occupiedSet(s2)), roads: new Set(roadTileSetOf(s2)) };
  for (let i = 0; i < target; i++) {
    if (cost > 0 && s2.administrationState) break;
    if (cost > 0 && s2.funds < cost) break;
    const spot = spotCtx.findNext();
    if (!spot) break; // findSpot() widened its search to the whole map (BUG-593) and still found nothing
    const beforeLen = s2.buildings.length;
    const next = reduceCore(s2, { type: 'place', spec: item.specId, x: spot.x, y: spot.y }, batchBoard);
    if (next === s2) break; // defensive: 'place' declined for a reason not checked above
    if (next.buildings.length > beforeLen + 1) anyRoadTopologyChange = true;
    spotCtx.occupy(next.buildings.slice(beforeLen)); // sync ctx with what 'place' ACTUALLY added
    s2 = next;
    placed++;
  }
  // Placed EVERY attempted unit (none of the funds/administration/no-site
  // guards fired) AND there is genuinely more of `item.count` left to do ->
  // the cap itself, not any of those other reasons, is what stopped this
  // call. If placed < unitCap, some OTHER guard broke the loop first.
  const cappedByUnitLimit = placed === unitCap && placed < item.count;
  return { state: s2, placed, roadTopologyChange: anyRoadTopologyChange, cappedByUnitLimit };
}

function reduceCore(
  state: SimState,
  action: Action,
  // BUG-660: an optional shared, mutated-in-place connectivity board a batch
  // caller (placePlanItem, see its doc comment) threads through every 'place'
  // dispatch in the SAME batch so the per-unit board cost collapses from
  // O(buildings) to O(footprint). undefined for every other caller (single
  // 'place' click, 'placeMany' drag-paint, 'stampRegion') — those are
  // completely unaffected and keep the exact pre-fix per-call board rebuild.
  batchBoard?: { occupied: Set<string>; roads: Set<string> }
): SimState {
  // FEAT-1972079923 inc4 (AC-11): the FINAL DECLINE screen is a HARD STOP on
  // the whole game, not just the tick clock — once declineState is set, EVERY
  // gameplay-mutating action (place, sellAsset, enterAdministration, policy,
  // relocate, …) is a no-op. Only 'reset' (Start Over, GR#27 capture-before-
  // wipe path) and 'hydrate' (Load Save, same GR#27 path) can move the game
  // past decline — both replace the ENTIRE state, so they are exempted here
  // rather than trying to enumerate every action decline should still allow.
  // FEAT-2326609723: 'enterPlayMode' is the THIRD exemption from the decline
  // freeze (alongside reset/hydrate) — it is specifically the escape hatch
  // OFFERED FROM the Decline screen, so it must be reachable while declineState
  // is set. Its own reducer case below clears declineState as part of engaging
  // the sandbox.
  if (
    state.declineState &&
    action.type !== 'reset' &&
    action.type !== 'hydrate' &&
    action.type !== 'enterPlayMode'
  ) {
    return state;
  }

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
      // FEAT-2326609761 AC-29 (Aaron R4, "unplaceable by hand"): defence in
      // depth mirroring isPlaceable()'s UI gate — a unique building (e.g. the
      // Five Gorges Dam, maxPerCity: 1) refuses a placement past its cap
      // through THIS reducer, not just a disabled palette button.
      if (remainingAllowance(state, sp) <= 0) {
        return { ...state, placeNotice: `One per city — ${sp.name} already built` };
      }
      // Zoning is free (FEAT-1972079882): a zone charges £0, so the funds check
      // and deduction use placementCost, not the catalogue cost.
      const cost = placementCost(sp);
      // FEAT-1972079923 inc3 (AC-6, Aaron's ruling): Administration Mode is a HARD
      // BLOCK on DISCRETIONARY spend — any PAID placement — checked BEFORE the
      // ordinary affordability gate below so a player with enough cash on hand is
      // still refused (this is a spending freeze, not an affordability check). A
      // £0 placement (free zone/road) is NOT discretionary spend and always
      // proceeds, admin or not — mirrors the cost>0 guard immediately below.
      // (Aaron's ruling also freezes "hiring": this engine has no discrete hire
      // action — jobs only arrive via placing job-bearing buildings, which are
      // paid, so this same block covers it; nothing further to gate.)
      if (cost > 0 && state.administrationState) {
        return { ...state, placeNotice: ADMINISTRATION_PLACE_BLOCKED_MESSAGE };
      }
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
      // BUG-660: a batch caller's shared board (see placePlanItem's doc
      // comment) is kept in perfect sync with occupiedSet(state) at every
      // step of the batch, so reading it here instead of occupiedSet(state)
      // is contents-identical — it just avoids the O(buildings) cache-miss
      // rebuild that a batch's own connector-laying triggers on `state`.
      const fitsOcc = batchBoard ? batchBoard.occupied : occupiedSet(state);
      if (!fits(fitsOcc, sp.w, sp.h, action.x, action.y)) return state;
      // ROUND r3 FIX (2026-09-04, F1/F2): NO affordability gate here — see
      // the Action union's 'place' case doc comment above for the full
      // rationale. The reducer always places once it reaches this point.
      const placedBuilding = { id: state.nextId, spec: action.spec, x: action.x, y: action.y, builtTick: state.tick };
      const placedState = {
        ...state,
        funds: state.funds - cost,
        // FEAT-dynamic-bailout: every paid placement is real capex spend, gross
        // (never netted against a later refund/demolition — spec §7.1).
        cumulativeCapexSpent: (state.cumulativeCapexSpent ?? 0) + cost,
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
      // BUG b2d31bc7 FIX 1: build the occupied/road tile sets for placedState ONCE
      // here (memoised — occupiedSet/roadTileSetOf cache on the buildings array
      // ref) and hand them to autoConnect as prebuiltBoard, so the single-
      // placement path no longer pays autoConnect's OWN from-scratch O(n) rebuild
      // (engine.ts:1692-1698) on top of this one — same contents, one pass.
      // BUG-646 FIX (2026-09-03): occupiedSet(placedState)/roadTileSetOf(placedState)
      // used to be a fresh O(buildings) rebuild EVERY placement (placedState.buildings
      // is always a new array reference), measured as the single largest cost in a
      // big-city resolveDemandAll batch (5.2s of a 7.7s/250-unit run at Aaron's real
      // 29,831-building savepoint). placedState.buildings is ALWAYS exactly
      // `[...state.buildings, placedBuilding]` (built two lines above), so
      // occupiedSetIncremental()/roadTileSetIncremental() (data.ts) clone the
      // ALREADY-CACHED occupiedSet(state)/roadTileSetOf(state) — a cache HIT here in
      // any append-only loop (resolveDemand's placePlanItem, drag-placeMany) since
      // `state` is the previous iteration's `placedState` — and add just this one
      // building's own footprint (O(w*h)) instead of re-scanning every building.
      // BUG-660: a batch caller's board is MUTATED IN PLACE with this unit's
      // own footprint (never cloned — see placePlanItem's doc comment above)
      // instead of going through occupiedSetIncremental()/roadTileSetIncremental(),
      // whose unconditional `new Set(base)` clone (data.ts) is the O(buildings)
      // cost this bug fixes. Contents handed to autoConnect are identical
      // either way — only HOW the board is assembled differs.
      let connBoard: { occupied: Set<string>; roads: Set<string> };
      if (batchBoard) {
        addFootprintTiles(batchBoard.occupied, placedBuilding, sp);
        if (isRoadSpec(sp)) addFootprintTiles(batchBoard.roads, placedBuilding, sp);
        connBoard = batchBoard;
      } else {
        connBoard = {
          occupied: occupiedSetIncremental(state, placedState.buildings),
          roads: roadTileSetIncremental(state, placedState.buildings),
        };
      }
      const connected = autoConnect(placedState, placedBuilding, sp, undefined, connBoard);
      // BUG-660: sync the batch board with any tiles autoConnect itself
      // appended (a laid connector or an upgraded junction — see autoConnect's
      // own doc comment: a tier upgrade never changes Set membership, only a
      // NEW connector tile does, and those are always appended at the tail of
      // `buildings`, exactly like sweepOrphanConnects' own board maintenance
      // above). Skipped entirely (no-op loop) on the common already-connected
      // path where `connected.buildings === placedState.buildings`.
      if (batchBoard) {
        for (let i = placedState.buildings.length; i < connected.buildings.length; i++) {
          const tile = connected.buildings[i];
          const tileSp = SPECS[tile.spec];
          if (!tileSp) continue;
          addFootprintTiles(batchBoard.occupied, tile, tileSp);
          addFootprintTiles(batchBoard.roads, tile, tileSp); // connector tiles are always road/trunk specs
        }
      }
      // FEAT-1972079902 inc3: if this is a GATEWAY (Ashford International /
      // International Airport), auto-lay deterministic branch lines to the nearest
      // slow-rail line AND the nearest HS1 line (routing around buildings), or
      // surface a "no rail route" notice. Deterministic; branch tiles journal via
      // the gateway `place` action through replay. Non-gateways just clear railNotice.
      let updated = autoBranchRail(connected, placedBuilding, sp);
      // BUG-660: same board sync for any branch-rail tiles (gateway specs
      // only — demandFixPlan() never targets one today, so this loop is
      // provably a no-op in every current batch caller, kept for correctness
      // if that ever changes rather than assumed away).
      if (batchBoard) {
        for (let i = connected.buildings.length; i < updated.buildings.length; i++) {
          const tile = updated.buildings[i];
          const tileSp = SPECS[tile.spec];
          if (!tileSp) continue;
          addFootprintTiles(batchBoard.occupied, tile, tileSp);
          if (isRoadSpec(tileSp)) addFootprintTiles(batchBoard.roads, tile, tileSp);
        }
      }

      // BUG b2d31bc7 FIX 2: the placed building itself is road/trunk kind, OR
      // autoConnect/autoBranchRail appended extra tiles (a connector or branch
      // rail run — always road/trunk specs) beyond just placedBuilding. If
      // NEITHER happened, the buildings array grew by exactly the one non-road
      // building and the road graph is provably unchanged — safe to skip the
      // reducer wrapper's computeRoadConnectivity recompute for this action.
      if (
        !isRoadOrTrunkSpec(sp) &&
        updated.buildings.length === placedState.buildings.length
      ) {
        roadTopologyMayHaveChanged = false;
      }

      // FEAT-1972079878 inc1 (AC-6): Create a building monitor for scalable specs.
      // Monitor expires after 1 year (TICKS_PER_YEAR). Type is 'residents' or 'jobs'
      // based on the building's primary capacity type.
      if (sp.capacityTiers) {
        // FEAT-2326609740 §11: monitor type follows whichever capacity field
        // the spec actually carries — residential keeps 'residents', schools
        // get the new 'children' type, health/police get 'served', a
        // capacityTiers power plant (the NPP reactor ladder, Q100089=B) gets
        // 'mw', everything else (offices, and any future jobs-carrying spec)
        // keeps 'jobs'. Order matters only in that a spec never carries more
        // than one of these fields (GR#7-adjacent data invariant).
        const monitorType: BuildingMonitor['type'] = sp.residents
          ? 'residents'
          : sp.children !== undefined
            ? 'children'
            : sp.served !== undefined
              ? 'served'
              : sp.kind === 'power'
                ? 'mw'
                : 'jobs';
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

    // BUG b2d31bc7 FIX 3 — atomic batch placement for drag-painting.
    //
    // MapView used to dispatch a separate 'place' action per pointer-move
    // tile-change during a drag (once per tile touched), so a 10-tile drag
    // meant 10 full reducer round-trips + 10 React re-renders + (pre-FIX2) 10
    // computeRoadConnectivity BFS passes — the direct cause of "10 estates,
    // ~3 land" at 68K pop. 'placeMany' collects the whole drag into ONE
    // dispatch: places every tile through the SAME per-tile mutation path
    // 'place' uses (reduceCore recursion, exactly like resolveDemand's bulk-
    // place above), folding each success into `cur` so later tiles in the
    // same drag see the earlier ones (road-adjacency, funds, monitors — all
    // identical to N manual clicks), but the reducer WRAPPER only sees the
    // buildings array change ONCE, so computeRoadConnectivity (FIX 2's gate)
    // runs at most once for the whole drag instead of once per tile.
    //
    // Affordability: stops (does not place further tiles) the instant funds
    // can no longer cover `cost` — mirrors resolveDemand's placeNotice
    // "placed X of Y" report so a funds-starved drag is never a silent
    // partial no-op. A tile that fails its own fits/validity check (e.g. the
    // drag revisited an already-placed cell) is skipped, not fatal to the rest.
    case 'placeMany': {
      const sp = SPECS[action.spec];
      if (!canEnterSim(sp) || !specUnlocked(state, sp)) return state;
      const cost = placementCost(sp);

      let cur = state;
      let placed = 0;
      // Aggregate FIX 2's recompute-gate across every tile in the batch: the
      // whole placeMany only skips the wrapper's connectivity recompute if
      // NOT ONE of its tiles touched the road/trunk graph (own spec is
      // road/trunk, or autoConnect/autoBranchRail appended connector tiles
      // for that placement). Each per-tile reduceCore('place') call below
      // also flips the shared module flag as a side effect — this explicit
      // final assignment (after the loop) is authoritative and overwrites it.
      let anyRoadTopologyChange = isRoadOrTrunkSpec(sp);
      let stoppedAtCap = false;
      for (const tile of action.tiles) {
        if (cost > 0 && cur.administrationState) break;
        if (cost > 0 && cur.funds < cost) break;
        // FEAT-2326609761 AC-29: pre-check the cap BEFORE delegating to 'place'
        // (mirroring the admin/funds guards immediately above), rather than
        // relying on 'place' itself to refuse it. 'place' returns a NEW state
        // object (with a placeNotice) on a cap refusal, not the same
        // reference — delegating into it here would break this loop's
        // `next === cur` decline-detection and wrongly count the tile as
        // placed. Every further tile of the SAME (now-exhausted) spec would
        // refuse identically, so this is a `break`, not a `continue`.
        if (remainingAllowance(cur, sp) <= 0) {
          stoppedAtCap = true;
          break;
        }
        const beforeLen = cur.buildings.length;
        const next = reduceCore(cur, { type: 'place', spec: action.spec, x: tile.x, y: tile.y });
        if (next === cur) continue; // this tile declined (occupied/out of bounds/etc.) — try the rest
        if (next.buildings.length > beforeLen + 1) anyRoadTopologyChange = true;
        cur = next;
        placed++;
      }
      roadTopologyMayHaveChanged = anyRoadTopologyChange;

      if (placed === action.tiles.length) return cur;
      const shortBy = stoppedAtCap
        ? `One per city — ${sp.name} already built`
        : cost > 0 && cur.funds < cost
          ? 'insufficient funds'
          : 'some tiles already occupied';
      return {
        ...cur,
        placeNotice: `Placed ${placed} of ${formatPlacedCount(action.tiles.length, sp.name)} — ${shortBy}`,
      };
    }

    // FEAT-2326609728 (engine core) — ONE-CLICK DEMAND FIX bulk-place.
    //
    // Walks demandFixPlan(state)'s count for `action.serviceKey`, placing ONE
    // unit at a time through the SAME single-tile 'place' path this reducer
    // already runs (findSpot() for the site, reduceCore({type:'place',...}) for
    // the mutation) — no second placement mechanism, so road-adjacency,
    // administration-mode, funds affordability, auto-connect/auto-branch-rail,
    // and building monitors all apply exactly as a manual click would. Each
    // placement is folded into `cur` before the next findSpot() call, so later
    // sites see the just-placed building (deterministic — GR#21, no Date/
    // Math.random, purely a function of the evolving state).
    //
    // Affordability (brief requirement): if funds run out partway, place as
    // many as affordable and STOP — never silently place fewer with no signal
    // (placeNotice reports "placed X of Y") and never let funds go negative
    // from this bulk action (the same cost>0 && funds<cost guard 'place' uses).
    case 'resolveDemand': {
      const plan = demandFixPlan(state).find((p) => p.serviceKey === action.serviceKey);
      if (!plan) return state;
      const sp = SPECS[plan.specId];
      if (!canEnterSim(sp) || !specUnlocked(state, sp)) return state;

      // BUG-566 FIX (independent-round REJECT), preserved via placePlanItem():
      // resolveDemand bulk-places via the SAME recursive reduceCore('place')
      // pattern placeMany uses, so it is exposed to the identical FIX-2
      // hazard — each inner 'place' call flips the shared module-level
      // `roadTopologyMayHaveChanged` flag as a side effect, and without
      // aggregating across the WHOLE batch the wrapper would only see the
      // LAST iteration's verdict (a run where an EARLY building lays a road
      // connector but the FINAL one doesn't would wrongly skip
      // computeRoadConnectivity). placePlanItem() aggregates with OR itself.
      //
      // PERF CAP (independent round r2, 2026-09-03): a SINGLE service can
      // itself exceed RESOLVE_DEMAND_ALL_MAX_UNITS in a big-enough city
      // (measured: parks alone hits 1,000 planned units at pop 3M) — the same
      // per-action unit cap applies here, not only inside 'resolveDemandAll',
      // so one Fix (N) click can never freeze the tab either.
      const result = placePlanItem(state, plan, RESOLVE_DEMAND_ALL_MAX_UNITS);
      roadTopologyMayHaveChanged = result.roadTopologyChange;

      if (result.placed >= plan.count) return result.state; // full shortfall cleared — 'place' already cleared placeNotice
      // D2 FIX: shortfallReason() distinguishes administration-blocked from a
      // real funds shortfall from a real site shortage — see its doc comment.
      // The per-action unit cap is a FOURTH, distinct reason (never blamed on
      // funds/site) — checked first since placePlanItem() only sets it when
      // the cap itself (not funds/admin/site) is what stopped this call.
      const shortBy = result.cappedByUnitLimit
        ? `reached the ${RESOLVE_DEMAND_ALL_MAX_UNITS}-unit per-click build limit — click Fix again for the rest`
        : shortfallReason(result.state, sp, plan.specId);
      return {
        ...result.state,
        placeNotice: `Placed ${result.placed} of ${formatPlacedCount(plan.count, sp.name)} — ${shortBy}`,
      };
    }

    // BUG-606 fix-all (Aaron, 2026-09-03): "next to the word demand ... a
    // fix-all button" — runs the SAME placePlanItem() bulk-place for EVERY
    // service orderedDemandFixPlan(state) lists, in that priority order
    // (Health first, then demand-descending), as ONE journaled action so the
    // result is a single coherent placeNotice ("Fix All: built X, Y skipped")
    // instead of N sequential resolveDemand dispatches silently overwriting
    // each other's notice. Each service's plan is RE-DERIVED against the
    // already-updated `cur` state before it is attempted — funds spent on an
    // earlier, higher-priority service in this same batch are reflected
    // before the next service's affordability check runs, exactly as if the
    // player had clicked every Fix (N) button in this order by hand. No
    // second placement mechanism: placePlanItem() is the SAME helper
    // 'resolveDemand' above uses.
    case 'resolveDemandAll': {
      const order = orderedDemandFixPlan(state);
      if (order.length === 0) return state;

      let cur = state;
      let anyRoadTopologyChange = false;
      const built: string[] = [];
      let skippedCount = 0;
      // D2 FIX: the true, per-item reason each shortfall or skip stopped —
      // never a blanket "funds ran out" when the real cause (this session)
      // was Administration Mode or a real site shortage. Set preserves
      // insertion order (deterministic — GR#21) and collapses repeats so a
      // batch that failed for ONE reason across many services still reads as
      // one reason, not the same sentence N times.
      const reasons = new Set<string>();

      // PERF CAP (independent round r2, Aaron 2026-09-03): a GLOBAL unit
      // budget shared across the WHOLE batch, walked in the SAME Health-first
      // priority order — measured 46.6s synchronous at pop 400k and DNF
      // (18,718 planned units) at pop 3M without this. Deterministic (a fixed
      // constant, never a clock/elapsed-time bound — GR#21): the same journal
      // replays to the same partial result every time. `totalPlanned` is the
      // sum of every service's OWN target count (`item.count`, from the SAME
      // orderedDemandFixPlan(state) snapshot used for priority order) — the
      // honest "Y" a capped notice reports, even though later services in
      // the order are re-derived against `cur` and may end up wanting a
      // slightly different count by the time (if ever) they're reached.
      const totalPlanned = order.reduce((sum, item) => sum + item.count, 0);
      let remainingUnitBudget = RESOLVE_DEMAND_ALL_MAX_UNITS;
      let totalPlaced = 0;
      let hitUnitCap = false;

      for (const item of order) {
        if (remainingUnitBudget <= 0) {
          // The cap was exhausted by an EARLIER, higher-priority service —
          // this and every remaining service in the order is skipped WITHOUT
          // even being attempted (no findSpot()/'place' cost paid for it).
          hitUnitCap = true;
          skippedCount++;
          continue;
        }
        const plan = demandFixPlan(cur).find((p) => p.serviceKey === item.serviceKey);
        if (!plan) continue; // already cleared by an earlier service's side-effect (rare) — nothing left to do
        const sp = SPECS[plan.specId];
        if (!canEnterSim(sp) || !specUnlocked(cur, sp)) {
          skippedCount++;
          reasons.add('not currently unlocked');
          continue;
        }
        const result = placePlanItem(cur, plan, remainingUnitBudget);
        cur = result.state;
        anyRoadTopologyChange = anyRoadTopologyChange || result.roadTopologyChange;
        totalPlaced += result.placed;
        remainingUnitBudget -= result.placed;
        if (result.placed > 0) built.push(formatPlacedCount(result.placed, sp.name));
        if (result.placed < plan.count) {
          skippedCount++;
          if (result.cappedByUnitLimit) {
            hitUnitCap = true;
          } else {
            reasons.add(shortfallReason(cur, sp, plan.specId));
          }
        }
      }
      roadTopologyMayHaveChanged = anyRoadTopologyChange;

      // The unit cap is reported as its OWN honest, actionable reason — never
      // folded into the generic funds/admin/site reasons above, and never
      // silently reported as "insufficient funds" (the exact D2-class defect
      // this whole reason-plumbing exists to prevent).
      if (hitUnitCap) {
        return {
          ...cur,
          placeNotice: `Fix All: built ${totalPlaced} of ${totalPlanned} planned — click Fix All again for the rest`,
        };
      }

      const reasonSummary = [...reasons].join('; ');
      if (built.length === 0) {
        return { ...cur, placeNotice: `Fix All: nothing built — ${reasonSummary || 'insufficient funds'}` };
      }
      const summary = skippedCount > 0
        ? `Fix All: built ${built.join(', ')} — ${skippedCount} service(s) skipped or partial (${reasonSummary})`
        : `Fix All: built ${built.join(', ')}`;
      return { ...cur, placeNotice: summary };
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
          // FEAT-1972079928 AC-5: motorway junction scarcity — minimum spacing.
          if (existingMotorway.spec.id === 'rd_mwyjunction') {
            // Already a junction: this crossing merges into it (AC-5 option
            // (b) + AC-8 no-double-conversion) — no new junction, no extra cost.
            tilesToPlace.push({ x: tile.x, y: tile.y, spec: 'rd_mwyjunction', isConversion: false, cost: 0 });
          } else if (
            !motorwayJunctionSpacingOk(
              state,
              tile.x,
              tile.y,
              MOTORWAY_JUNCTION_MIN_SPACING_TILES,
              MOTORWAY_JUNCTION_MAX_PER_SEGMENT
            )
          ) {
            // AC-5: too close to (or too many) existing motorway junctions.
            // Do NOT place a new direct junction. Option (a): slip-connect to
            // a nearby parallel A-road-or-above instead of crossing directly.
            const slip = findMotorwaySlipTarget(
              existingRoadByTile,
              tile.x,
              tile.y,
              MOTORWAY_SLIP_SEARCH_RADIUS_TILES,
              pathSet
            );
            if (slip) {
              // The parallel road is already on the map and already reachable
              // by the wider re-plan cascade below (AC-2/AC-3 route it in) —
              // nothing to place here; this tile is simply NOT converted, so
              // the crossing is rerouted away from the motorway rather than
              // cutting a scarce new junction.
            }
            // Either way (slip found or not): suppress the crossing — no
            // placement, no conversion, no cost at this tile (mirrors the
            // below-dual/rail rejection elsewhere in this same reducer case).
          } else {
            // Spacing/count OK: any road crossing motorway → convert to
            // rd_mwyjunction at flat cost (FEAT-1972079910 inc3, AC-8).
            conversions.set(k, { buildingId: existingMotorway.building.id, newSpec: 'rd_mwyjunction' });
            tilesToPlace.push({ x: tile.x, y: tile.y, spec: 'rd_mwyjunction', isConversion: true, cost: MOTORWAY_JUNCTION_COST });
          }
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

      // FEAT-1972079923 inc3 (AC-6): same discretionary-spend freeze as `place`
      // above — a PAID road/junction/bridge path is blocked outright in
      // Administration Mode; a £0 (deduped/free-tier) path always proceeds.
      if (totalCost > 0 && state.administrationState) {
        return { ...state, placeNotice: ADMINISTRATION_PLACE_BLOCKED_MESSAGE };
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
      let placedState: SimState = {
        ...state,
        funds: state.funds - totalCost,
        // FEAT-dynamic-bailout: road/junction/bridge path spend is real capex.
        cumulativeCapexSpent: (state.cumulativeCapexSpent ?? 0) + totalCost,
        placeNotice: null,
      };
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

      // FEAT-1972079928 inc1: road re-planning cascade, folded into THIS SAME
      // placeRoadPath action (no new Action/journal type — the whole cascade is
      // a pure function of state, so it replays byte-identically for free).
      // planRoadReplanCascade computes the ENTIRE cascade — every upgrade and
      // every new connector tile across the whole affected region — and
      // returns it as a plan BEFORE any of it is applied (the atomic-cascade-
      // before-redraw guarantee: see the function's doc comment). Only after
      // the full plan (and its total cost) is known do we check affordability
      // and apply it in one state transition; an unaffordable cascade leaves
      // `placedState` completely untouched (AC-6 all-or-nothing — the original
      // placeRoadPath placement still stands, only the RE-PLAN is rolled back).
      const replanNewTiles = tilesToPlace.map((t) => ({ x: t.x, y: t.y }));
      const replanPlan = planRoadReplanCascade(placedState, replanNewTiles, newRoadTier);
      if (replanPlan && replanPlan.totalCost <= placedState.funds) {
        const demolishedIds = new Set(Array.from(replanPlan.demolitions.values()).map((d) => d.buildingId));
        let repState: SimState = {
          ...placedState,
          funds: placedState.funds - replanPlan.totalCost,
          // FEAT-dynamic-bailout: the re-plan cascade's new tiles + upgrades
          // are real capex spend too (demolitions are refunds — never netted
          // out here, spec §7.1 — but the demolition refund itself is applied
          // elsewhere in this cascade and is not part of replanPlan.totalCost).
          cumulativeCapexSpent: (placedState.cumulativeCapexSpent ?? 0) + replanPlan.totalCost,
          buildings: placedState.buildings
            .filter((b) => !demolishedIds.has(b.id))
            .map((b) => {
              const conv = replanPlan.conversions.get(`${b.x},${b.y}`);
              if (!conv || conv.buildingId !== b.id) return b;
              return { ...b, spec: conv.newSpec };
            }),
        };
        for (const nt of replanPlan.newTilePlacements) {
          repState = {
            ...repState,
            nextId: repState.nextId + 1,
            buildings: [...repState.buildings, { id: repState.nextId, spec: nt.spec, x: nt.x, y: nt.y, builtTick: state.tick }],
          };
        }
        const replanLabel = replanPlan.demolitions.size > 0
          ? `Re-planned roads (${replanPlan.newTilePlacements.length} new, ${replanPlan.conversions.size} upgraded, ${replanPlan.demolitions.size} demolished)`
          : `Re-planned roads (${replanPlan.newTilePlacements.length} new, ${replanPlan.conversions.size} upgraded)`;
        placedState = { ...repState, ...logEvent(repState, replanLabel, -replanPlan.totalCost) };
      }
      // replanPlan exists but is unaffordable: skip entirely, no partial spend,
      // no ghost journal entry — placedState is exactly the pre-replan state.

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
      // R2-F5a (independent round REJECT, 2026-09-03): a grown building
      // (FEAT-2326609740) occupies MORE tiles than sp.w/sp.h — hit-testing
      // against the spec-only rect made the extra tiles un-bulldozable dead
      // ground (MapView still finds/dispatches, but the reducer, the
      // AUTHORITATIVE layer, attributed the click to no building and
      // silently no-op'd). Use the building's real footprint (footprintOf).
      const target = state.buildings.find((b) => {
        const sp = SPECS[b.spec];
        if (!sp) return false;
        const { w, h } = footprintOf(b, sp);
        return (
          action.x >= b.x &&
          action.x < b.x + w &&
          action.y >= b.y &&
          action.y < b.y + h
        );
      });
      if (!target) return state;
      const def = SPECS[target.spec];
      // BUG b2d31bc7 FIX 2: bulldoze removes exactly ONE known building — if
      // it isn't road/trunk kind, the road graph provably can't have changed.
      if (!isRoadOrTrunkSpec(def)) {
        roadTopologyMayHaveChanged = false;
      }
      // Refund BULLDOZE_REFUND_FRACTION of what was actually PAID — a free
      // zone refunds nothing, so place-then-bulldoze cannot mint money.
      const refund = Math.round(placementCost(def) * BULLDOZE_REFUND_FRACTION);
      return {
        ...state,
        funds: state.funds + refund,
        buildings: state.buildings.filter((w) => w.id !== target.id),
        ...(state.movingId === target.id ? { movingId: null } : {}),
        ...logEvent(state, `Demolished ${def.name}`, refund),
      };
    }

    // FEAT-1972079923 inc2 (AC-4) — FORCED ASSET SALE: the player sells a
    // building from the bailout panel to raise funds and escape insolvency.
    // Atomic (single reducer transition: building removed + treasury credited
    // in the same returned state, journaled through replay like every other
    // action). The sale value is credited through BOTH the ledger (logEvent,
    // matching bulldoze's pattern) AND lastFlows.inflows — the latter is what
    // AC-4 requires so the consistency checker can trace a funds jump back to
    // a named inflow even for this between-tick action (bulldoze/loan/repay
    // don't extend lastFlows; this one deliberately does, per the AC).
    //
    // BUG-503: because this is the ONE between-tick action that extends
    // lastFlows, it is also the one that must keep the tick-boundary
    // conservation snapshot (fundsAtTickEnd === fundsAtTickStart + Σinflows −
    // Σoutflows, consistency.ts 'conservation.funds-vs-flows') in step:
    // growing Σinflows by saleValue without moving fundsAtTickEnd makes the
    // RHS grow while the LHS stays stale, a false −saleValue violation for
    // the whole window until the next tick() recomputes both snapshots from
    // scratch. bulldoze/debugFunds avoid this by not touching lastFlows at
    // all; sellAsset can't do that (AC-4), so it must bump fundsAtTickEnd by
    // the same saleValue instead — the only field that needs it, since
    // fundsAtTickStart is a start-of-window snapshot this action never moves.
    case 'sellAsset': {
      const target = state.buildings.find((b) => b.id === action.id);
      if (!target) return state;
      const sp = SPECS[target.spec];
      if (!sp) return state;
      const capitalValue = placementCost(sp);
      if (capitalValue <= 0) return state; // nothing to force-sell for £0
      const saleValue = Math.round(capitalValue * ASSET_SALE_VALUE_FRACTION);
      // Merge into any EXISTING 'Forced Asset Sale' inflow this same tick
      // (rather than pushing a second entry) — the player can sell several
      // assets from the panel before the next tick() call resets lastFlows,
      // and a duplicate label would trip consistency.ts's
      // 'flows.inflow-labels-unique' check (GR#3: one label, one entry).
      const existingIdx = state.lastFlows.inflows.findIndex((f) => f.label === ASSET_SALE_LABEL);
      const inflows =
        existingIdx >= 0
          ? state.lastFlows.inflows.map((f, i) =>
              i === existingIdx ? { ...f, value: f.value + saleValue } : f,
            )
          : [...state.lastFlows.inflows, { label: ASSET_SALE_LABEL, value: saleValue }];
      return {
        ...state,
        funds: state.funds + saleValue,
        buildings: state.buildings.filter((w) => w.id !== target.id),
        ...(state.movingId === target.id ? { movingId: null } : {}),
        lastFlows: { ...state.lastFlows, inflows },
        // BUG-503: keep the tick-boundary snapshot in step with the Σinflows
        // bump above (see comment on the case) — fundsAtTickStart is
        // untouched (it's the start-of-window snapshot), only the end moves.
        fundsAtTickEnd: state.fundsAtTickEnd + saleValue,
        ...logEvent(state, `${ASSET_SALE_LABEL}: ${sp.name}`, saleValue),
      };
    }

    // FEAT-1972079923 inc3/inc4 (AC-5, AC-10) — ADMINISTRATION MODE ENTRY: the
    // player's alternative to forced asset sales. Available while EITHER
    // bailout is ACTIVE (inc4: `bailoutSecondState` reuses this same entry
    // point — mirrors the FORCED ASSET SALES panel's own visibility guard) and
    // idempotent if administration is already active (no re-stamp, no double
    // entry). Sets `administrationState` immediately (a between-tick action,
    // like sellAsset) AND the exposed `insolvencyState` to 'administration' in
    // the SAME transition — the next tick's advance() recomputes an IDENTICAL
    // overlay from `administrationState`, so this is not a second code path,
    // just an immediate reflection of what advance() would compute anyway.
    // Clears BOTH `bailoutState` and `bailoutSecondState` so the FORCED ASSET
    // SALES panel closes at once. Stamps `origin` (AC-10) so the year-end
    // re-evaluation in advance() knows whether "still broke" should
    // auto-trigger the second bailout ('bailout') or the final decline screen
    // ('bailout_second') — see advance()'s administration block. The city
    // REMAINS PLAYABLE — nothing here freezes the clock/tick.
    case 'enterAdministration': {
      if ((!state.bailoutState && !state.bailoutSecondState) || state.administrationState) return state;
      const origin: BailoutOrigin = state.bailoutSecondState ? 'bailout_second' : 'bailout';
      return {
        ...state,
        administrationState: { enteredAt: state.tick, origin },
        bailoutState: null,
        bailoutSecondState: null,
        insolvencyState: 'administration',
      };
    }

    case 'pickup':
      return { ...state, movingId: action.id };

    case 'relocate': {
      if (state.movingId == null || state.funds < MOVE_COST) return state;
      const moving = state.buildings.find((b) => b.id === state.movingId);
      if (!moving) return { ...state, movingId: null };
      const sp = SPECS[moving.spec];
      // F2 (independent round REJECT, 2026-09-03): a building that has
      // scaled OUT (FEAT-2326609740) occupies MORE tiles than sp.w/sp.h —
      // validating against the SPEC footprint let a grown building move
      // into a hole too small for its REAL size, or off the map edge, since
      // the smaller spec-only rect could pass a check the true footprint
      // would fail. Always use the building's OWN current footprint here.
      const { w: moveW, h: moveH } = footprintOf(moving, sp);
      if (
        action.x < 0 ||
        action.y < 0 ||
        action.x + moveW > MAP_W ||
        action.y + moveH > MAP_H ||
        !fits(occupiedSet(state, moving.id), moveW, moveH, action.x, action.y)
      )
        return state;
      // FEAT-dynamic-bailout: MOVE_COST is deliberately EXCLUDED from
      // cumulativeCapexSpent — it is a flat UI-convenience repositioning fee
      // for an ALREADY-owned building (no new capital asset is built), not a
      // placementCost-derived capital spend. Counting it would inflate the
      // dynamic bailout's CAPEX-proportional term for spend that built
      // nothing new.
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
      // R2-F5c (independent round REJECT, 2026-09-03): an EXISTING grown
      // building's real footprint (footprintOf) can be bigger than its
      // spec's base rect — flattening against sp.w/sp.h alone missed the
      // extra tiles, so a stamp could land ON TOP of them instead of
      // removing them first (a real, proven overlap).
      const toRemove = new Set<number>();
      for (const b of state.buildings) {
        const sp = SPECS[b.spec];
        if (!sp) continue;
        const { w, h } = footprintOf(b, sp);
        for (let dy = 0; dy < h; dy++) {
          for (let dx = 0; dx < w; dx++) {
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
          if (sp) refundTotal += Math.round(placementCost(sp) * BULLDOZE_REFUND_FRACTION);
          return false;
        }
        return true;
      });

      // FEAT-2326609761 AC-29 ("stampRegion currently gates on canEnterSim
      // only — a real hole"): honour maxPerCity for the WHOLE stamp,
      // all-or-nothing like every other stampRegion gate. Counts start from
      // POST-FLATTEN buildings (newBuildings — the demolitions above already
      // freed any cap slot they vacated), then tally each batch item as it is
      // considered, so a single stamp containing two Five Gorges Dams cannot
      // sneak the second one past a per-item-only check.
      const capCountBySpec = new Map<string, number>();
      for (const b of newBuildings) {
        const sp = SPECS[b.spec];
        if (sp?.maxPerCity != null) {
          capCountBySpec.set(b.spec, (capCountBySpec.get(b.spec) ?? 0) + 1);
        }
      }
      for (const item of action.clipboard.items) {
        const sp = SPECS[item.spec];
        if (sp?.maxPerCity == null) continue;
        const current = capCountBySpec.get(item.spec) ?? 0;
        if (current >= sp.maxPerCity) return state; // whole stamp refused, no partial application
        capCountBySpec.set(item.spec, current + 1);
      }

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
        // FEAT-dynamic-bailout: the GROSS placement spend (totalCost), never
        // netted against the demolition refund (refundTotal) — spec §7.1's
        // gross-only rule, so a demolish/rebuild stamp can't manipulate the
        // dynamic bailout offer downward.
        cumulativeCapexSpent: (state.cumulativeCapexSpent ?? 0) + totalCost,
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
        // FEAT-dynamic-bailout: a pipe-tier upgrade is real capex spend.
        cumulativeCapexSpent: (state.cumulativeCapexSpent ?? 0) + cost,
        pipeTier: { ...state.pipeTier, [action.id]: tier + 1 },
        ...logEvent(state, `${sp.name} pipe upgraded to ${PIPE_TIERS[tier + 1].label}`, -cost),
      };
    }

    case 'tax':
      return { ...state, taxRates: { ...state.taxRates, [action.which]: action.rate } };

    case 'policy': {
      const turningOn = !state.policies[action.id];
      // FEAT-1972079923 inc3 (AC-6): enacting a NEW policy is discretionary
      // spend — blocked in Administration Mode. Turning an ALREADY-ON policy
      // back OFF is not a new spend and stays allowed even under admin.
      if (turningOn && state.administrationState) {
        return { ...state, placeNotice: ADMINISTRATION_POLICY_BLOCKED_MESSAGE };
      }
      return { ...state, policies: { ...state.policies, [action.id]: turningOn } };
    }

    // FEAT-2326609711 inc1 (AC-9/AC-10): toggle external power cover. Plain
    // sim-state mutation (not React local state, AC-10's false-pass) — it
    // journals/replays/serialises exactly like every other reducer action, so
    // it survives panel close/reopen and genesis replay (AC-5) for free.
    case 'toggleGridImport':
      return {
        ...state,
        gridImportEnabled: !(state.gridImportEnabled ?? GRID_IMPORT_ENABLED_DEFAULT),
      };

    // FEAT-2326609761 inc1 (AC-1, AC-2): flips consolidatorEnabled. Mirrors
    // toggleGridImport's shape exactly — plain sim-state mutation, journals/
    // replays/serialises like every other reducer action (journal.ts's
    // isStateAffecting classifies it `true`). This read-only-half build does
    // no discovery/apply work itself when the flag flips; it exists so the
    // separate mutation-lane pass and this module's own map overlay/panel
    // have one shared, deterministic source of truth to gate on.
    case 'toggleConsolidator': {
      const turningOn = !(state.consolidatorEnabled ?? CONSOLIDATOR_ENABLED_DEFAULT);
      // FEAT-2326609761 inc2 (Aaron's ruling, 2026-09-03): TURNING ON is
      // gated at city level 10 (CONSOLIDATOR_UNLOCK_LEVEL) — a progression
      // reward, mirroring every other unlock in this codebase. Turning an
      // already-on consolidator back OFF is never blocked (a level can only
      // ever go up, so this can't currently strand an enabled consolidator
      // behind a regressed gate, but refusing the OFF direction would be an
      // unjustifiable trap regardless). Structural enforcement here, not
      // just a disabled checkbox in the UI, so a stale/replayed dispatch
      // from before level 10 can never silently enable it either.
      if (turningOn && !consolidatorUnlockedAtLevel(levelOf(state.xp))) return state;
      return {
        ...state,
        consolidatorEnabled: turningOn,
      };
    }

    // FEAT-2326609761 inc2 (Aaron's glide-mode ruling, 2026-09-04): plain
    // sim-state mutation, mirrors toggleConsolidator's shape. An unrecognised
    // mode string (a corrupt/future-version dispatch) is refused rather than
    // written, matching GR#16's "never trust an untyped value into state".
    case 'setConsolidatorMode': {
      if (action.mode !== 'glide' && action.mode !== 'monthly-twelfth') return state;
      return { ...state, consolidatorMode: action.mode };
    }

    // FEAT-2326609761 inc2 (Aaron's ruling, 2026-09-03): clamps into the
    // sanctioned range (clampConsolidatorSectionMetres) rather than refusing
    // an out-of-range dispatch outright — a slider drag naturally produces
    // in-range values, and clamping a stray out-of-range one (e.g. a stale
    // action replayed after MIN/MAX retune) is the more forgiving failure
    // mode for a value with no "wrong mix" semantics (unlike the sliders,
    // there is no single "correct" size to protect by refusing).
    case 'setConsolidatorSectionMetres':
      return { ...state, consolidatorSectionMetres: clampConsolidatorSectionMetres(action.metres) };

    // FEAT-2326609761 inc2 (Aaron's slider ruling, 2026-09-03): REFUSED (no
    // state change) unless the four sliders sum to exactly 100 — see
    // validateConsolidatorSliders's doc comment for why this never clamps/
    // normalises instead.
    case 'setConsolidatorSliders': {
      if (!validateConsolidatorSliders(action.sliders)) return state;
      return { ...state, consolidatorSliders: { ...action.sliders } };
    }

    // FEAT-2326609761 (CONSOLIDATOR mutation lane, AC-26): reverses exactly
    // the last pass, or is a no-op (reference identity) if the log is empty.
    case 'consolidatorUndo':
      return undoLastConsolidatorPass(state);

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
      // FOLLOW-UP (r3 round note (c), non-blocking): a clipboard item carries
      // only `{spec, dx, dy}` (see the Action type above) — copying a GROWN
      // building (FEAT-2326609740) and stamping it back places it at its
      // spec's BASE footprint, not the grown one. Lossy (the stamp is
      // smaller than the original), never corrupting (stampRegion's own
      // flatten pass already uses footprintOf for what it clears — see
      // 'stampRegion' above). Not fixed here (out of scope this round).
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

    // FEAT-milestone-cash-rewards-2026-09-02 (Q100047b ruling B1): UI-only
    // dismiss for the milestone-reward banner — mirrors dismissNotice exactly.
    // The reward itself is unaffected (already paid via inflows/ledger by the
    // time the notice is visible); dismissing only clears the banner.
    case 'dismissMilestoneNotice':
      return (state.milestoneNotice ?? null) == null ? state : { ...state, milestoneNotice: null };

    // FEAT-1972079923 inc1 (companion to BUG-396): UI-only dismiss for the
    // cannot-afford placement notice — mirrors dismissNotice. Not journaled
    // (see journal.ts isStateAffecting); a successful place() already clears
    // placeNotice on its own (BUG-396), this just lets the player acknowledge
    // it explicitly without waiting for a new action to supersede it.
    case 'dismissPlaceNotice':
      return state.placeNotice == null ? state : { ...state, placeNotice: null };

    // FEAT-1972079923 inc1 (AC-8): UI-only dismiss for the one-shot bailout-entry
    // popup — mirrors dismissNotice. Not journaled; the popup itself is only ever
    // (re-)set inside advance() on a genuine band transition, so dismissing it here
    // cannot resurrect a stale entry.
    case 'dismissInsolvencyPopup':
      return state.insolvencyPopup == null ? state : { ...state, insolvencyPopup: null };

    case 'unlockAll': {
      // God-mode "Unlock all" (FEAT-1972079899): flip the catalogue gate for a large
      // cash gate. Deterministic + all-or-nothing — with insufficient funds the state
      // is returned untouched (no partial unlock, no partial charge). The funds
      // deduction is a between-tick mutation exactly like debugFunds/place cost, so it
      // never disturbs the tick-boundary conservation invariant; a ledger entry is
      // recorded for UI visibility (mirrors how `place` logs its spend).
      // FEAT-dynamic-bailout: UNLOCK_ALL_COST is deliberately EXCLUDED from
      // cumulativeCapexSpent — it is a meta-game catalogue GATE (buys unlock
      // access, a rules toggle), not placementCost for a built capital asset.
      // Counting it would let a player inflate their dynamic bailout CAPEX
      // term for free by god-moding the catalogue open, without building
      // anything the offer is meant to be proportional to.
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

    case 'hydrate': {
      // BUG-652 GRANDFATHERING (2026-09-04): the universal belt-and-braces
      // catch-all — every path that hands a RESTORED/LOADED snapshot to the
      // live app funnels through this one reducer case EXCEPT the very first
      // (synchronous, pre-render) boot state, which replay.ts's
      // restoreFromSavepoint()/prepareRestoreForChunkedTail() already stamp
      // directly (see their own comments) because it is set via useState's
      // lazy initializer, never dispatched through the reducer. Idempotent
      // (stampJobsGrandfather is a no-op once state.economyEpoch is already
      // current) — hard-reset-replay's result reaches here already at the
      // current epoch (replayFromGenesis starts from initialState()), so this
      // is a safe no-op for that path, exactly as intended (hard-reset-replay
      // deliberately re-derives under CURRENT rules by design).
      // Aaron ruling 2026-09-04 (land_tunnel footprint grew bigger):
      // stampTunnelFootprintGrandfather is unconditional here too (cheap
      // epoch-compare no-op once current), same idiom/placement as
      // stampJobsGrandfather immediately below it.
      const hydrated = stampTunnelFootprintGrandfather(stampJobsGrandfather(sanitizeTreasury(action.state)));
      // BUG-677: a worker-tick hydrate (source === 'tick') is NOT a load —
      // skip the once-per-load AC-31 scan below. Without this gate the scan
      // ran on every applied worker tick (store.tsx delivers each advanced
      // tick as a hydrate), re-stamping placeNotice every second so the
      // over-cap notice could never be dismissed, and burning an
      // O(buildings) countOfSpec sweep per capped spec on the hot path.
      // stampJobsGrandfather above stays unconditional — it early-returns on
      // a current economyEpoch, and a worker tick's state is always current.
      if (action.source === 'tick') return hydrated;
      // Aaron ruling 2026-09-04 ("just purge off the extra five gorges dam
      // ... there is only one permitted just delete the others") SUPERSEDES
      // the old FEAT-2326609761 AC-31 ruling below (previously: "nothing is
      // demolished, no money is clawed back"). An OLD SAVE may carry MORE
      // than maxPerCity of a now-capped spec (Aaron's own save carries 23 ×
      // pow_hydro, placed before the cap existed) — on load, purge every
      // surplus instance of EVERY maxPerCity-capped spec down to the cap.
      //
      // Deterministic (GR#21): surplusInstancesOf (data.ts) is a pure
      // selector — keeps the maxPerCity OLDEST instances (lowest builtTick,
      // ties by lowest id) and returns the rest; SPECS iteration is fixed
      // object insertion order, so which specs are scanned and in what order
      // never varies run to run.
      //
      // Conservation-safe: each removed building credits
      // CONSOLIDATOR_SCRAP_FRACTION (50%) of its placementCost as a clearly
      // labelled inflow ('Surplus <name> decommission scrap', one merged
      // entry per spec) through BOTH the ledger (logEvent) and
      // lastFlows.inflows — mirrors 'sellAsset' (BUG-503): because this
      // extends lastFlows, fundsAtTickEnd must move by the exact same
      // amount in the same transition, or the funds-vs-flows conservation
      // check would read a false violation until the next tick() resets
      // both snapshots from scratch.
      //
      // Idempotent: surplusInstancesOf returns [] once a spec's count is
      // already <= its cap, so a second hydrate of an already-purged state
      // is a pure no-op (no further funds movement, no re-fired notice).
      //
      // Gated on source !== 'tick' by the early return above — this scan is
      // an O(buildings) sweep per capped spec and must never run on the
      // worker-tick hydrate hot path (BUG-677).
      let purged = hydrated.buildings;
      let creditTotal = 0;
      let inflows = hydrated.lastFlows.inflows;
      let ledger = hydrated.ledger;
      let nextLedgerId = hydrated.nextLedgerId;
      const notices: string[] = [];
      for (const sp of Object.values(SPECS)) {
        if (sp.maxPerCity == null) continue;
        const surplus = surplusInstancesOf({ ...hydrated, buildings: purged }, sp);
        if (surplus.length === 0) continue;
        const removeIds = new Set(surplus.map((b) => b.id));
        purged = purged.filter((b) => !removeIds.has(b.id));
        const specCredit = surplus.reduce(
          (sum, b) => sum + Math.round(placementCost(SPECS[b.spec] ?? sp) * CONSOLIDATOR_SCRAP_FRACTION),
          0,
        );
        creditTotal += specCredit;
        const scrapLabel = `Surplus ${sp.name} decommission scrap`;
        inflows = [...inflows, { label: scrapLabel, value: specCredit }];
        // Ledger entry too (mirrors sellAsset/bulldoze) — threaded through
        // a running {ledger, nextLedgerId, tick} cursor since logEvent()
        // takes a whole SimState and multiple capped specs can purge in the
        // same load.
        const ledgerUpdate = logEvent({ ...hydrated, ledger, nextLedgerId }, scrapLabel, specCredit);
        ledger = ledgerUpdate.ledger;
        nextLedgerId = ledgerUpdate.nextLedgerId;
        notices.push(
          `Removed ${surplus.length} surplus ${sp.name} — cap is ${sp.maxPerCity} per city; ${fmtMoney(specCredit)} scrap credited`,
        );
      }
      if (notices.length === 0) return hydrated;
      return {
        ...hydrated,
        buildings: purged,
        funds: hydrated.funds + creditTotal,
        fundsAtTickEnd: hydrated.fundsAtTickEnd + creditTotal,
        lastFlows: { ...hydrated.lastFlows, inflows },
        ledger,
        nextLedgerId,
        placeNotice: notices.join(' '),
      };
    }

    // FEAT-2326609723 (Play Mode) — the ONE-WAY sandbox escape hatch offered
    // from the Decline screen. Idempotent once already latched (no re-
    // injection, no way back — the latch never sets back to false, by
    // construction: this is the ONLY writer of playModeLatched, and it
    // always writes `true`). Credits PLAY_MODE_INJECTION_AMOUNT as a
    // clearly-labelled, non-disguised inflow (booked exactly like a bailout
    // injection) so the conservation invariant (fundsAtTickEnd ===
    // fundsAtTickStart + Σinflows − Σoutflows) still holds exactly even in
    // Play Mode — no bypass flag needed. Clears every insolvency overlay
    // (decline/administration/both bailouts) so the player can keep
    // building; the raw band is forced to 'solvent' immediately (the next
    // tick's advance() would recompute the SAME value anyway from the
    // now-enormous funds, so this is not a second code path). Deterministic:
    // no Date.now()/Math.random() — a fixed, named constant only (GR#21).
    case 'enterPlayMode': {
      if (state.playModeLatched) return state; // irreversible + idempotent — no re-injection.
      const injection = PLAY_MODE_INJECTION_AMOUNT;
      const inflows = [...state.lastFlows.inflows, { label: PLAY_MODE_INJECTION_LABEL, value: injection }];
      return {
        ...state,
        playModeLatched: true,
        funds: state.funds + injection,
        fundsAtTickEnd: state.fundsAtTickEnd + injection,
        insolvencyState: 'solvent',
        insolvencyRawBand: 'solvent',
        insolvencyPopup: null,
        bailoutState: null,
        bailoutSecondState: null,
        administrationState: null,
        declineState: null,
        lastFlows: { ...state.lastFlows, inflows },
        ...logEvent(state, PLAY_MODE_INJECTION_LABEL, injection),
      };
    }
  }
}

/**
 * BUG-460 FIX A — headless genesis replay (webconsole/src/sim/genesisReplay.ts)
 * drives the reducer through up to 50,000 journalled actions with NO UI reads in
 * between. The wrapper's per-action `computeRoadConnectivity` recompute below
 * exists so a UI read between actions always sees a fresh graph, but during a
 * replay nothing reads `s.roadConnectivity` between actions (isOnline/computeFailedGates
 * are only read from advance() (tick), which recomputes connectivity itself at
 * its own start, and from UI query functions the replayer never calls) — so the
 * wrapper's recompute during replay is pure allocation churn: a full Set+array+
 * sort+BFS over the whole board for every `place`/`placeRoadPath` action, which
 * is exactly the O(actions x buildings) allocation blowing the browser's GC
 * budget (2.5 GB, tab death) on a big journal.
 *
 * `setReplayMode(true)` tells the reducer wrapper to skip its recompute; the
 * replayer MUST clear it (try/finally) and then compute connectivity ONCE more
 * on the final state so the returned state is correct for the live game to
 * resume from. `advance()`'s own recompute (engine.ts, inside advance()) is
 * UNTOUCHED — tick-time connectivity used for demand/economy gating always
 * stays fresh, replay mode or not.
 */
let isReplaying = false;

/**
 * Enable/disable BUG-460 FIX A replay mode for the reducer wrapper. Callers
 * MUST clear this in a try/finally around the replay loop (even on a thrown
 * error) — leaving it set would silently stop the wrapper from keeping
 * `roadConnectivity` fresh for ordinary (non-replay) play.
 */
export function setReplayMode(active: boolean): void {
  isReplaying = active;
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
  // FEAT-milestone-cash-rewards-2026-09-02 (GR#16): a corrupt milestoneNotice.cash
  // (negative / non-integer / NaN, e.g. a hand-edited savepoint) is clamped to 0
  // rather than trusted, mirroring the level-up `notice` handling immediately
  // above — this runs at the top of EVERY reducer() call, so it catches a
  // corrupt load before any action (not just a tick) can read the bad value.
  let milestoneNotice = s.milestoneNotice ?? null;
  if (milestoneNotice && (milestoneNotice.cash < 0 || !Number.isSafeInteger(milestoneNotice.cash))) {
    milestoneNotice = { ...milestoneNotice, cash: 0 };
  }
  // FEAT-milestone-cash-rewards-2026-09-02 (GR#16): claimedMilestones is
  // sanitized here too (in addition to advance()'s per-tick self-heal) so a
  // corrupt/legacy value is never read by a non-tick action either.
  const claimedMilestones = sanitizeClaimedMilestones(s.claimedMilestones);
  const claimedMilestonesChanged =
    claimedMilestones.length !== (s.claimedMilestones ?? []).length ||
    claimedMilestones.some((id, i) => id !== (s.claimedMilestones ?? [])[i]);

  // BUG-600 (GR#16 boundary discipline / GR#3 one shared helper for both
  // queues — see sanitizePendingRewards' doc comment above): this is the
  // LOAD boundary — every reducer() call runs sanitizeTreasury() first
  // (including 'hydrate', a savepoint load), so a hand-edited or legacy
  // savepoint's non-array / junk-element / NaN-totalReward pending-reward
  // queue is normalised here BEFORE any action (not just a tick) can read it.
  // Mirrors claimedMilestonesChanged's changed-flag style immediately above
  // (array fields can't use `===` identity the way funds/notice do — a freshly
  // sanitized `[]` is never reference-equal to another `[]`).
  // NOTE: `!Array.isArray(...)` is checked FIRST and unconditionally forces
  // `changed` — a non-array input (string/object/null/undefined) that
  // happens to sanitize down to a same-length `[]` (e.g. length 0) must
  // still be treated as changed, or the identity-preserving early return
  // below would hand back `s` with its ORIGINAL corrupt (non-array) value
  // still sitting in the field, defeating the whole sanitizer.
  const pendingRewards = sanitizePendingRewards(s.pendingRewards, isValidLevelReward);
  const pendingRewardsChanged =
    !Array.isArray(s.pendingRewards) ||
    pendingRewards.length !== s.pendingRewards.length ||
    pendingRewards.some((x, i) => x !== (s.pendingRewards as unknown[])[i]);

  const pendingMilestoneRewards = sanitizePendingRewards(s.pendingMilestoneRewards, isValidMilestoneReward);
  const pendingMilestoneRewardsChanged =
    !Array.isArray(s.pendingMilestoneRewards) ||
    pendingMilestoneRewards.length !== s.pendingMilestoneRewards.length ||
    pendingMilestoneRewards.some((x, i) => x !== (s.pendingMilestoneRewards as unknown[])[i]);

  // FEAT-dynamic-bailout (spec §4 migration table) — runs EXACTLY ONCE per
  // save, guarded by `capexBackfilled` (a brand-new game already starts with
  // it `true` — see rawState() — so this branch only ever fires for a save
  // that predates this feature). No migration path may crash, throw, or
  // silently zero a loaded save's funds (spec §4 closing paragraph) — this is
  // a pure additive/defaulting mapping over already-optional fields, exactly
  // like the notice/milestoneNotice/claimedMilestones tolerance above.
  //
  // F3 FIX (independent round REJECT, 2026-09-02, GR#16 "type-safe storage
  // boundaries" / GR#1 "aggressive error trapping"): the ORIGINAL code below
  // used bare `=== undefined` checks, which pass a hand-edited/corrupt-but-
  // DEFINED value straight through untouched. Two concrete exploits closed:
  //   (a) cumulativeCapexSpent: 'not a number' (a string) is not undefined,
  //       so it skipped the backfill AND was never coerced — every downstream
  //       `(s.cumulativeCapexSpent ?? 0) + cost` charge site then does STRING
  //       CONCATENATION, not addition, silently growing into ever-longer
  //       digit-garbage; computeDynamicBailoutOffer's Number.isFinite guard
  //       then reads it as 0, and the whole CAPEX-proportional formula
  //       silently degrades to the fixed BAILOUT_FLOOR with no error (GR#1/
  //       #17 violation). Fixed by coercing through sanitizeFunds() — the
  //       SAME numeric storage-boundary guard funds/loanBalance already use
  //       above — AFTER the backfill decision (so a corrupt-but-defined value
  //       still correctly SKIPS a re-backfill; it is coerced to a safe
  //       number, not re-derived from the standing asset base).
  //   (b) dynamicBailoutUsed: 0 (or '', null) is FALSY but DEFINED — the old
  //       `=== undefined` migration check passed it straight through as `0`,
  //       so an already-escalated save (bailoutSecondState active) with a
  //       hand-corrupted `0` latch would silently RE-ARM a second dynamic
  //       grant (the exact BUG-504 re-arm class this feature exists to
  //       close). Fixed by keying migration on `typeof !== 'boolean'`
  //       instead — ANY non-boolean value (undefined, 0, '', null, a stray
  //       string) is treated as "needs (re-)deriving from real state signals"
  //       and never trusted at face value; a genuine, properly-typed
  //       `true`/`false` always passes straight through unchanged.
  let capexBackfilled = typeof s.capexBackfilled === 'boolean' ? s.capexBackfilled : false;
  let cumulativeCapexSpentRaw = s.cumulativeCapexSpent;
  if (!capexBackfilled) {
    if (cumulativeCapexSpentRaw === undefined) {
      // BACKFILL PROXY (spec §4): sum placementCost for every building
      // CURRENTLY standing — "what it would cost to build what's standing
      // today". Understates true lifetime spend (ignores demolished/
      // refunded structures) but never overstates it, and is never zero for
      // a real city — closes the false "tiny city, tiny offer" migration
      // cliff the spec calls out.
      let backfill = 0;
      for (const b of s.buildings) {
        const sp = SPECS[b.spec];
        if (sp) backfill += placementCost(sp);
      }
      cumulativeCapexSpentRaw = backfill;
    }
    capexBackfilled = true;
  }
  // GR#16 storage-boundary coercion (F3 fix (a) above) — mirrors funds/
  // loanBalance exactly: never trust the stored TYPE, only ever emit a real,
  // finite, safe-integer number.
  const cumulativeCapexSpent = sanitizeFunds(cumulativeCapexSpentRaw as number);

  let dynamicBailoutUsed = typeof s.dynamicBailoutUsed === 'boolean' ? s.dynamicBailoutUsed : undefined;
  if (dynamicBailoutUsed === undefined) {
    // §4 migration table, no-double-dip: a save that has ALREADY touched any
    // stage of the old fixed-terms ladder (mid a first bailout, a first-
    // bailout re-arm already used, a second bailout, administration, or
    // decline) has already had its ONE dynamic-era "once" — it does not get a
    // fresh dynamic offer on top. A save that is genuinely solvent with no
    // bailout history at all (the common case) starts clean at `false`.
    dynamicBailoutUsed =
      s.declineState != null ||
      s.administrationState != null ||
      s.bailoutSecondState != null ||
      s.bailoutState != null ||
      (s.firstBailoutCount ?? 0) >= 1;
  }

  const capexBackfilledPrev = typeof s.capexBackfilled === 'boolean' ? s.capexBackfilled : false;
  const dynamicBailoutUsedPrev = typeof s.dynamicBailoutUsed === 'boolean' ? s.dynamicBailoutUsed : undefined;
  if (
    funds === s.funds &&
    loanBalance === s.loanBalance &&
    notice === s.notice &&
    milestoneNotice === (s.milestoneNotice ?? null) &&
    !claimedMilestonesChanged &&
    !pendingRewardsChanged &&
    !pendingMilestoneRewardsChanged &&
    capexBackfilled === capexBackfilledPrev &&
    cumulativeCapexSpent === s.cumulativeCapexSpent &&
    dynamicBailoutUsed === dynamicBailoutUsedPrev
  ) {
    return s;
  }
  return {
    ...s,
    funds,
    loanBalance,
    notice,
    milestoneNotice,
    claimedMilestones,
    pendingRewards,
    pendingMilestoneRewards,
    cumulativeCapexSpent,
    capexBackfilled,
    dynamicBailoutUsed,
  };
}

export function reducer(state: SimState, action: Action): SimState {
  const s = sanitizeTreasury(state);
  // BUG b2d31bc7 FIX 2: reset the recompute-gate flag before every action so a
  // prior action's proof can never leak into this one; reduceCore's case
  // handlers (currently 'place' and 'bulldoze') flip it to false only when
  // THEY can prove the road/trunk graph is untouched.
  roadTopologyMayHaveChanged = true;
  const next = reduceCore(s, action);
  if (isReplaying) return next; // BUG-460 FIX A — see setReplayMode doc above.
  if (!next.roadConnectivity) {
    return { ...next, roadConnectivity: computeRoadConnectivity(next) };
  }
  if (next.buildings !== s.buildings && roadTopologyMayHaveChanged) {
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

/**
 * FEAT-crime-mechanic-2026-09-02 — the wellbeing part list MINUS Crime.
 * Extracted verbatim from the pre-crime wellbeingOf() body so crimeRateOf()
 * (data.ts) has a wellbeing-feedback input that can NEVER recurse back into
 * itself: crimeRateOf() calls wellbeingCoreOf(), which builds this list and
 * never calls crimeRateOf() in return. wellbeingOf() below calls this SAME
 * function for its own core parts, then separately calls crimeRateOf() (which
 * internally recomputes this list again via wellbeingCoreOf() — a second,
 * cheap, pure call, not a cycle) to build the Crime part. See crimeRateOf's
 * doc comment (data.ts) for the full loop-breaking argument.
 */
// BUG-602 (integration-soak perf cliff): memoised on state identity. Before
// this, ONE wellbeingOf(s) call built this list TWICE (directly + via
// crimeRateOf→wellbeingCoreOf), and advance()/UI/debugjson each rebuilt it
// again for the same state — the soak measured ~700ms/tick at pop~700 from
// exactly this multiplicity. Pure function of s, so memoOnState is exact.
const buildWellbeingCoreParts: (s: SimState) => { label: string; value: number }[] =
  memoOnState((s) => {
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
  // FOLLOW-UP (r3 round note (b), non-blocking): sp.w*sp.h, not
  // footprintOf(b, sp) — same twin-site class as data.ts's parksCapacityOf,
  // harmless only because no park spec carries a capacityTiers ladder today.
  // Not fixed here (out of scope for the F5s-only re-round).
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
  //
  // FEAT-2326609711 inc1 fix (r2, closing the r1 HALF-WIRED DEFECT): gated
  // on data.ts's isBrownoutActive(s) — the SAME SSOT predicate the income
  // penalty (computeFlows) and the DemandDock banner now read — instead of
  // brownout.active alone. A covered shortfall (Grid Import cover ON) no
  // longer collapses Utilities wellbeing: the price premium is the entire
  // penalty, per Aaron's ruling (no brownout of any kind while covered).
  const brownout = brownoutOf(s);
  const utilitiesValue = isBrownoutActive(s)
    ? Math.max(0, Math.round(part(utilities) * (1 - brownout.deficitRatio * BROWNOUT_WELLBEING_K)))
    : part(utilities);

  // BUG-524 (Q100046 C1) — jobs/unemployment now has TEETH: a "Jobs/
  // Employment" wellbeing part that drops as unemployment rises. unemployment
  // is the SSOT unemploymentOf() (data.ts, GR#3) — same workers=pop*0.55 /
  // totalJobs() basis serviceCoverageOf's commercial/office/industrial/mine
  // rows already use. `part()` expects a coverage-shaped 0..1 (1 = good), so
  // employment coverage = 1 - unemployment (full employment ⇒ 1 ⇒ ~100;
  // 100% unemployment ⇒ 0 ⇒ ~0, subject to the same early-game blend as every
  // other part). ⚠ BALANCE-NUMBER PLACEHOLDER: linear map, pending Aaron's
  // pass.
  //
  // NO-DOUBLE-COUNT DECISION (documented per the brief): unemployment feeds
  // move-out ONLY via this wellbeing part, not as a second direct term in
  // engine.ts's moveOutRate formula (~1183). moveOutRate already reads
  // wbOverall — which now includes this part — so a high-unemployment city
  // already gets a higher move-out rate through the existing wellbeing→
  // move-out link (symmetric with every other wellbeing-driven service
  // effect). Adding a SEPARATE unemployment term to moveOutRate on top of
  // this part would double-count the exact same signal twice in one tick.
  const employment = part(clampN(1 - unemploymentOf(s), 0, 1));

  // FEAT-congestion-teeth-2026-09-02 (AC-2, Q100057 A1 / Q100071 rec-on-all)
  // — Traffic/Commute wellbeing part: sustained road/motorway saturation
  // drags commute-time wellbeing down via congestionFactorOf(s) (data.ts),
  // an AVERAGE-across-sustained-lines ramp already bounded to [0,1] (1.0 =
  // no penalty, AC-4's uncongested case; 0.0 = fully penalized, AC-6). Safe
  // to fold into the SHARED core-parts list (unlike Crime, which needed its
  // own wellbeingCoreOf exclusion) because congestion depends only on
  // buildings/population/its own tick counter — never on wellbeing itself —
  // so there is no cycle risk feeding it into crimeRateOf's wellbeing input
  // too (data.ts congestionFactorOf doc comment has the full argument).
  const congestion = part(congestionFactorOf(s));

  const parts = [
    { label: 'Approval', value: approvalOf(s) },
    { label: 'Parks & leisure', value: parks },
    { label: 'Healthcare', value: part(ratio('gp')) },
    { label: 'Hospital care', value: part(ratio('hosp')) },
    { label: 'Education', value: part(education) },
    { label: 'Safety', value: part(ratio('police')) },
    // BUG-526 (Q100046 A1) — fire stations charged upkeep with no wellbeing
    // effect (cost-only sink) until now; wired to the new serviceCoverageOf
    // 'fire' row (data.ts, GR#3 SSOT) exactly like Safety/police above.
    { label: 'Fire safety', value: part(ratio('fire')) },
    { label: 'Jobs/Employment', value: employment },
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
    { label: 'Traffic/Commute', value: congestion },
  ];
  return parts;
});

/**
 * FEAT-crime-mechanic-2026-09-02 — wellbeing overall WITHOUT the Crime part.
 * The sole consumer is crimeRateOf() (data.ts), which needs a wellbeing
 * signal that provably never depends on crime itself (see that function's
 * doc comment). ⚠ BALANCE-NUMBER PLACEHOLDER: equal part weights, same as
 * wellbeingOf(), pending Aaron's pass.
 */
export function wellbeingCoreOf(s: SimState): number {
  const parts = buildWellbeingCoreParts(s);
  return Math.round(parts.reduce((a, p) => a + p.value, 0) / parts.length);
}

// BUG-602: memoised — advance() consumes this at least twice per tick
// (moveOutRate + snapshot paths) and the UI/debugjson re-derive it per render
// against the same state object. Pure derivation of s.
export const wellbeingOf: (s: SimState) => {
  overall: number;
  parts: { label: string; value: number }[];
} = memoOnState((s) => {
  const coreParts = buildWellbeingCoreParts(s);

  // Same blend/part shaping as buildWellbeingCoreParts uses internally
  // (duplicated here deliberately — see that function's doc comment for why
  // the Crime part cannot be folded into the shared list without reordering
  // the crime<->wellbeing call graph into a cycle).
  const pop = s.population;
  const f = earlyGameFactor(pop);
  const blend = (computed: number) => Math.round(computed * f + 55 * (1 - f));
  const part = (coverage: number) => blend(Math.round(clampN(coverage * 100, 0, 100)));

  // FEAT-crime-mechanic-2026-09-02 (AC-8): Crime as its own wellbeing part,
  // separate from Safety/police — a city can have full police coverage and
  // still suffer high crime (an incomplete defence), or low crime despite
  // low police (a well-integrated community). Invert: high crime (100) ->
  // coverage 0 -> part ~0; low crime (0) -> coverage 1 -> part ~100.
  const crime = crimeRateOf(s);
  const crimePart = part(clampN(1 - crime / 100, 0, 1));

  const parts = [...coreParts, { label: 'Crime', value: crimePart }];
  // ⚠ BALANCE-NUMBER PLACEHOLDER: equal part weights, pending Aaron's pass.
  const overall = Math.round(parts.reduce((a, p) => a + p.value, 0) / parts.length);
  return { overall, parts };
});

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

/**
 * FEAT-1972079923 inc2 (AC-3, task ruling supersedes the AC doc's stale
 * construction-order text) — Aaron's ruling (BOW FEAT-1972079923 comment):
 * the FORCED ASSET SALES list is sorted by CAPITAL VALUE DESCENDING, biggest
 * first ("the stadium goes before the corner shop"). Pure/deterministic: the
 * comparator is capitalValue desc, id asc as a stable tie-break — no
 * Date/random (GR#21), so two identical states always render an identical
 * order (byte-identical replay, AC-12).
 *
 * `capitalValue` is the building's placementCost (what was actually spent to
 * place it — SSOT, matches the bulldoze-refund basis so a sale and a
 * demolition price the same asset identically). Zero-cost assets (free
 * zoning) are excluded — there is nothing to force-sell for £0.
 * `saleValue` is the placeholder amount actually credited on sale (AC-4).
 */
export interface ForcedSaleAsset {
  id: number;
  spec: string;
  name: string;
  x: number;
  y: number;
  capitalValue: number;
  saleValue: number;
}

export function forcedSaleAssets(s: SimState): ForcedSaleAsset[] {
  const assets: ForcedSaleAsset[] = [];
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const capitalValue = placementCost(sp);
    if (capitalValue <= 0) continue;
    assets.push({
      id: b.id,
      spec: b.spec,
      name: sp.name,
      x: b.x,
      y: b.y,
      capitalValue,
      saleValue: Math.round(capitalValue * ASSET_SALE_VALUE_FRACTION),
    });
  }
  return assets.sort((a, b) => b.capitalValue - a.capitalValue || a.id - b.id);
}

export function initialState(): SimState {
  return advance(rawState());
}
