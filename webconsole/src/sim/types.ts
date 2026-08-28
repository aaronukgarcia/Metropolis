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
  /**
   * FEAT-1972079910 inc3 (AC-7): if spec='rd_railbridge', the original rail line spec
   * this bridge crosses ('rail' or 'hs1'). Used to restore line continuity in train
   * route geometry (buildRailGeometry groups by spec, so bridge must report the
   * line it belongs to, not the bridge spec). Set during conversion in placeRoadPath,
   * preserved through serialization and replay.
   */
  bridgeOver?: string;
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
  /**
   * FEAT-1972079907 inc1 — transient road auto-connect notice. Set to a short
   * message (e.g. "no road access") when a placed building could not be wired to
   * the road network (no route within budget / unaffordable connector); null
   * otherwise. Cleared/overwritten on the next `place`. Deterministic, sim-state.
   */
  roadNotice: string | null;
  /**
   * FEAT-1972079902 inc3 — transient rail auto-branch notice. Set to a short
   * message (e.g. "no rail route") when a placed GATEWAY (Ashford International /
   * International Airport) could not lay a branch to a rail or HS1 line (no route
   * within budget). null otherwise. Cleared/overwritten on the next `place`.
   * Deterministic, serialisable sim-state — mirrors `roadNotice`.
   */
  railNotice: string | null;
  /**
   * BUG-396 — transient placement-blocked notice. Set to a short message (e.g.
   * "Insufficient funds — £X needed") when a PAID placement is blocked because the
   * player cannot afford it, so the player learns why nothing happened instead of a
   * silent no-op. null otherwise. Cleared on the next successful `place`.
   * A cost-0 (free zone) placement is NEVER blocked and never sets this — a free
   * zone is always affordable, even while the treasury is negative. Deterministic,
   * serialisable sim-state — mirrors `roadNotice` / `railNotice`.
   */
  placeNotice: string | null;
  /**
   * FEAT-1972079907 inc2 — one-year traffic monitors. When auto-connect lays a
   * connector, each connector tile + the joined road tile is registered here and
   * watched for one in-game year (TICKS_PER_YEAR). On each monthly aggregate the
   * engine recomputes each monitored segment's coarse traffic load and auto-scales
   * (upgrades one tier) the ones that saturate, then expires monitors past their
   * window. Fully serialisable (plain numbers) so it round-trips through save and
   * genesis-replay; deterministic, tick-driven — NO wall-clock.
   */
  roadMonitors: RoadMonitor[];
  /**
   * FEAT-1972079891 inc1 — connected road network (AC-1). The set of drivable-road
   * tiles (keyed "x,y") reachable from the map edges / trunk infrastructure, used
   * by the per-building road-activation gates (isRoadConnected). Stored as a SORTED
   * string[] rather than a Set because a Set is not JSON-serialisable — this shape
   * round-trips through save/replay/debug.json and compares byte-identically under
   * genesis-replay's stableStringify. Recomputed at the START of every advance()
   * and kept fresh by the reducer whenever buildings change (AC-12), so it is
   * always consistent with `buildings`. Optional for backward tolerance: a legacy
   * state without it simply has the road gates skipped until it is computed. Use
   * `connectedRoadTileSet(s)` (data.ts) to read it as a Set at use sites.
   */
  roadConnectivity?: { connectedRoadTiles: string[] };
}

/**
 * FEAT-1972079907 inc2 — a single monitored road segment (one road tile).
 * All-number, JSON-round-trippable. `source` is the building id whose auto-connect
 * created this monitor (its spec drives the segment's coarse traffic load). `until`
 * is the tick the monitoring window closes (registeredTick + TICKS_PER_YEAR).
 */
export interface RoadMonitor {
  x: number;
  y: number;
  source: number;
  until: number;
}
