// engine.ts — pure (non-React) simulation core.
//
// Extracted from store.tsx so the reducer and its helpers can be unit-tested
// directly: `node --test` type-strips .ts, but NOT the JSX in store.tsx. Every
// piece of game logic lives here; store.tsx keeps only the React wiring and
// re-exports these symbols so existing `'../sim/store'` imports keep working.

import {
  PIPE_TIERS,
  SPECS,
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
} from './data.ts';
import type {
  FlowItem,
  LedgerEntry,
  LevelUpNotice,
  PolicyId,
  SimState,
  TaxRates,
  Tool,
  ZoneKind,
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

function rawState(): SimState {
  return {
    tick: 0,
    speed: 1,
    funds: 10000000,
    loanBalance: 0,
    population: 0,
    xp: 30,
    taxRates: { residential: 9, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: false },
    buildings: starterCity(),
    nextId: 100,
    movingId: null,
    tool: { mode: 'select' },
    clipboard: null,
    pipeTier: {},
    history: [],
    ledger: [],
    nextLedgerId: 1,
    lastFlows: { inflows: [], outflows: [] },
    // Start already "at" the seed level so the opening state grants no reward.
    lastRewardedLevel: levelOf(30),
    notice: null,
  };
}

const UPKEEP_BUCKET: Partial<Record<ZoneKind, string>> = {
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

export function computeFlows(s: SimState): { inflows: FlowItem[]; outflows: FlowItem[] } {
  const c = countByKind(s.buildings);
  const t = s.taxRates;
  const inflows: FlowItem[] = [
    { label: 'Council Tax', value: Math.round((s.population * t.residential * 2) / 100) },
    { label: 'Business Tax', value: Math.round(c.commercial * t.commercial * 0.4) },
    { label: 'Freight Tax', value: Math.round(c.industrial * t.industrial * 0.55) },
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
  outflows.push({ label: 'Wages', value: Math.round(s.population * 0.5) });

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

  // BUG-402 FIX: overdraft interest when funds go negative.
  // PLACEHOLDER (balance-number regime): overdraft rate on negative balance.
  // At 50% annual rate = 0.4% per tick (50% / 125 ticks ≈ 0.4% per tick).
  if (s.funds < 0) {
    const overdraftInterest = Math.round(Math.abs(s.funds) * 0.004);
    outflows.push({ label: 'Overdraft Interest', value: overdraftInterest });
  }

  if (s.policies.recycling) {
    const discounted = new Set(['Roads', 'Power Grid', 'Water & Waste', 'Healthcare', 'Education', 'Parks', 'Policing']);
    outflows = outflows.map((o) =>
      discounted.has(o.label) ? { ...o, value: Math.round(o.value * 0.93) } : o
    );
  }
  if (s.policies.austerity)
    outflows = outflows.map((o) => ({ ...o, value: Math.round(o.value * 0.9) }));
  return { inflows, outflows };
}

/**
 * Milestone rewards (FEAT-1972079884). If experience has crossed one or more new
 * levels since the last reward, grant the cash injection + build the level-up
 * notice EXACTLY ONCE per level. Idempotent: a no-op unless levelOf(xp) has
 * advanced past lastRewardedLevel, so it is safe to call after any xp change.
 */
export function grantLevelRewards(s: SimState): SimState {
  const lv = levelOf(s.xp);
  if (lv <= s.lastRewardedLevel) return s;
  let funds = s.funds;
  let notice: LevelUpNotice | null = s.notice;
  // A single jump may cross several levels (e.g. debugXp); reward each crossed
  // level once, compounding, and surface the most recent as the live notice.
  for (let L = s.lastRewardedLevel + 1; L <= lv; L++) {
    const cash = Math.round(funds * LEVEL_REWARD_RATE);
    funds += cash;
    notice = { level: L, cash, unlocked: unlockedAtLevel(L) };
  }
  return { ...s, funds, lastRewardedLevel: lv, notice };
}

function advance(s: SimState): SimState {
  const { inflows, outflows } = computeFlows(s);
  const income = inflows.reduce((a, b) => a + b.value, 0);
  const expense = outflows.reduce((a, b) => a + b.value, 0);
  let funds = s.funds + income - expense;
  let ledger: LedgerEntry[] = s.ledger;
  let nextLedger = s.nextLedgerId;
  const tick = s.tick + 1;

  if (tick % 30 === 0) {
    funds += 800;
    ledger = [{ id: nextLedger++, tick, label: 'Regional Grant', amount: 800 }, ...ledger].slice(0, LEDGER_CAP);
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
    population += Math.max(0, Math.ceil((capacity - population) * 0.05 * growthFactor));
  } else if (population > capacity) {
    population = Math.max(capacity, population - Math.ceil((population - capacity) * 0.1));
  }

  return grantLevelRewards({
    ...s,
    tick,
    funds,
    population,
    xp: s.xp + 1,
    history: [...s.history, { tick, funds, income, expense, population }].slice(-HISTORY_CAP),
    ledger,
    nextLedgerId: nextLedger,
    lastFlows: { inflows, outflows },
  });
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
  | { type: 'bulldoze'; x: number; y: number }
  | { type: 'pickup'; id: number }
  | { type: 'relocate'; x: number; y: number }
  | { type: 'cancelMove' }
  | { type: 'pipeUpgrade'; id: number }
  | { type: 'tax'; which: keyof TaxRates; rate: number }
  | { type: 'policy'; id: PolicyId }
  | { type: 'loan' }
  | { type: 'repay' }
  | { type: 'debugFunds'; amount: number }
  | { type: 'debugXp'; amount: number }
  | { type: 'dismissNotice' }
  | { type: 'reset' };

export function reducer(state: SimState, action: Action): SimState {
  switch (action.type) {
    case 'tick':
      return advance(state);

    case 'speed':
      return { ...state, speed: action.speed };

    case 'tool':
      return { ...state, tool: action.tool, movingId: null };

    case 'place': {
      const sp = SPECS[action.spec];
      if (!sp) return state;
      if (sp.unlock > levelOf(state.xp)) return state;
      // Zoning is free (FEAT-1972079882): a zone charges £0, so the funds check
      // and deduction use placementCost, not the catalogue cost.
      const cost = placementCost(sp);
      if (state.funds < cost) return state;
      if (
        action.x < 0 ||
        action.y < 0 ||
        action.x + sp.w > MAP_W ||
        action.y + sp.h > MAP_H
      )
        return state;
      if (!fits(occupiedSet(state), sp.w, sp.h, action.x, action.y)) return state;
      return grantLevelRewards({
        ...state,
        funds: state.funds - cost,
        xp: state.xp + 4,
        nextId: state.nextId + 1,
        buildings: [
          ...state.buildings,
          { id: state.nextId, spec: action.spec, x: action.x, y: action.y, builtTick: state.tick },
        ],
        ...logEvent(state, `Started ${sp.name}`, -cost),
      });
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

    case 'debugFunds':
      return { ...state, funds: state.funds + action.amount };

    case 'debugXp':
      return grantLevelRewards({ ...state, xp: state.xp + action.amount });

    case 'dismissNotice':
      return state.notice == null ? state : { ...state, notice: null };

    case 'reset': {
      const s = rawState();
      s.speed = state.speed;
      return advance(s);
    }
  }
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
  const c = countByKind(s.buildings);
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
  const parks = blend(Math.round(clampN(((c.park * 40) / Math.max(pop, 20)) * 70, 0, 100)));
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
