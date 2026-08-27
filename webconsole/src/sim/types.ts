export type ZoneKind =
  | 'road'
  | 'motorway'
  | 'rail'
  | 'station'
  | 'pylon'
  | 'residential'
  | 'commercial'
  | 'office'
  | 'industrial'
  | 'mine'
  | 'park'
  | 'power'
  | 'water'
  | 'health'
  | 'police'
  | 'school'
  | 'landmark'
  // FEAT-1972079877 placeholder catalogue families:
  | 'transport' // buses / trams / metro / ferries / parking
  | 'fire' // fire & rescue cover
  | 'civic' // governance + justice (town hall, courts, prison, library)
  | 'leisure'; // cinema / theatre / arena / attractions

export interface Building {
  id: number;
  spec: string;
  x: number;
  y: number;
  /** tick placed; structure is under construction until tick - builtTick >= build time */
  builtTick?: number;
}

export type ToolMode = 'select' | 'move' | 'bulldoze' | 'build' | 'clone';

export interface Tool {
  mode: ToolMode;
  spec?: string;
}

/** Physical size in metres (footprint x × y, height z; negative z = depth). */
export interface Dims {
  x: number;
  y: number;
  z: number;
}

export interface FlowItem {
  label: string;
  value: number;
}

export interface TickRecord {
  tick: number;
  funds: number;
  income: number;
  expense: number;
  population: number;
}

export interface LedgerEntry {
  id: number;
  tick: number;
  label: string;
  amount: number;
}

export type PolicyId = 'recycling' | 'transitSubsidy' | 'tourismDrive' | 'austerity';

/** Level-up notification (FEAT-1972079884): what a crossing unlocked + the cash granted. */
export interface LevelUpNotice {
  level: number;
  /** Cash injection granted for reaching this level (already added to funds). */
  cash: number;
  /** Human-readable names of specs that unlock AT this level. */
  unlocked: string[];
}

export interface TaxRates {
  residential: number;
  commercial: number;
  industrial: number;
}

export interface Clipboard {
  w: number;
  h: number;
  items: { spec: string; dx: number; dy: number }[];
}

export interface SimState {
  tick: number;
  speed: 0 | 1 | 2 | 3;
  funds: number;
  loanBalance: number;
  population: number;
  xp: number;
  taxRates: TaxRates;
  policies: Record<PolicyId, boolean>;
  buildings: Building[];
  nextId: number;
  movingId: number | null;
  tool: Tool;
  clipboard: Clipboard | null;
  /** per water-plant building id -> pipe tier index */
  pipeTier: Record<number, number>;
  history: TickRecord[];
  ledger: LedgerEntry[];
  nextLedgerId: number;
  /**
   * Flows recorded by the last advance().
   * BUG-419: `population` records the START-of-tick population the engine actually
   * charged population-scaled flows on (Council Tax, Wages, commuter, tourism, transit
   * subsidy). computeFlows() runs on the incoming state BEFORE the in-tick population
   * growth update, so end-of-tick `s.population` is the WRONG basis for recomputing
   * those flows in consistency checks — the recorded basis is. Optional for backward
   * tolerance: consumers fall back to `s.population` when absent (mirrors BUG-414's
   * "checker recomputes against the same basis the engine used").
   */
  lastFlows: { inflows: FlowItem[]; outflows: FlowItem[]; population?: number };
  /**
   * TICK-BOUNDARY INVARIANT (FEAT-1972079890, BUG-406, Round-6): Conservation is checked
   * using tick snapshots, not working-tree funds. fundsAtTickStart is funds when
   * the last advance() began (before flows). fundsAtTickEnd is funds when advance()
   * returned (after flows + in-tick rewards). The checker verifies:
   * fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows.
   * Between-tick mutations (debugXp, place cost, dev +10M) never affect conservation
   * checks and require no re-baselining tricks.
   */
  fundsAtTickStart: number;
  fundsAtTickEnd: number;
  /**
   * Queue of level-up rewards granted outside the tick (debugXp, place action).
   * Each advance() drains pendingRewards, applying each reward through flows so it
   * appears in lastFlows and counts for conservation. Gameplay: reward cash lands
   * at the next tick (sub-second at normal speed).
   */
  pendingRewards: Array<{ totalReward: number; newLevel: number; notice: LevelUpNotice }>;
  /**
   * Highest experience level already rewarded (FEAT-1972079884). Guarantees the
   * milestone cash injection + notification fire EXACTLY ONCE per level crossing:
   * a reward only triggers while levelOf(xp) > lastRewardedLevel.
   */
  lastRewardedLevel: number;
  /** Active level-up notification banner, or null when dismissed / none pending. */
  notice: LevelUpNotice | null;
  /**
   * God-mode "Unlock all" flag (FEAT-1972079899). When true, every catalogue spec
   * is available for placement regardless of its `unlock` level — the build gate
   * becomes `unlockedAll || sp.unlock <= level`. Set once by the `unlockAll` action
   * after charging UNLOCK_ALL_COST; default false. Journaled + deterministic.
   */
  unlockedAll: boolean;
}
