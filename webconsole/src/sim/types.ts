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
  /**
   * FEAT-1972079878 inc1 (AC-4): auto-scale tier for building capacity growth.
   * 0-indexed, starting at 0 = original placement capacity.
   * Incremented by auto-scale at monthly boundaries; persists across saves/loads.
   * Absent on buildings placed before this feature; treated as tier 0.
   */
  capacityTier?: number;
  /**
   * BUG-466: tick at which this building was last auto-scaled (capacityTier bumped
   * by evaluateBuildingMonitors). Used to enforce AUTO_SCALE_COOLDOWN_TICKS so the
   * same building can't re-upgrade every monthly pass once population regrows into
   * the capacity ceiling (the treadmill that caused the £1.6M/tick drain).
   * Absent on buildings that have never auto-scaled, or on saves/snapshots from
   * before this field existed — treated as never in cooldown (backward compatible).
   */
  lastAutoScaleTick?: number;
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

/**
 * FEAT-1972079925 — one tick's (or one month's, when aggregated) demographic
 * flows: births, deaths, move-ins, move-outs. All non-negative integers,
 * state-derived and deterministic (GR#21: no Date/random).
 */
export interface DemographicFlow {
  births: number;
  deaths: number;
  moveIns: number;
  moveOuts: number;
}

/**
 * FEAT-1972079925 — one closed month's aggregated demographic flows, plus the
 * population and tick at the moment the month closed. Recorded into
 * SimState.demographicHistory (a bounded ring) so the population Sankey and
 * trend views read REAL recorded flows, never a fabricated split (GR#15).
 */
export interface MonthlyDemographics extends DemographicFlow {
  tick: number;
  population: number;
}

/**
 * FEAT-1972079926 — one tick's (or one month's, when aggregated) move-ins
 * split across transport arrival modes. Sums back EXACTLY to that tick's/
 * month's moveIns total (DemographicFlow.moveIns is the SSOT — this is a
 * conservation-preserving SPLIT of it, never an independent count). All
 * non-negative integers, state-derived and deterministic (GR#21).
 */
export interface ArrivalsByMode {
  road: number;
  railLow: number;
  railHs: number;
  sea: number;
  plane: number;
}

/**
 * FEAT-1972079926 — one closed month's aggregated arrivals-by-mode split,
 * plus the tick at which the month closed. Recorded into
 * SimState.arrivalsByModeHistory (a bounded ring), parallel to
 * MonthlyDemographics, backing the arrivals-by-mode Sankey.
 */
export interface MonthlyArrivalsByMode extends ArrivalsByMode {
  tick: number;
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
  /**
   * FEAT-2326609711 inc1 (AC-1, AC-5, AC-9/AC-10) — external power cover
   * toggle. When true, a power shortfall (powerStats.need > .cap) is bought
   * in from the regional grid at GRID_IMPORT_TARIFF_PER_MW (fiscal.ts) —
   * booked as a "Grid Import" outflow (computeFlows) instead of the legacy
   * BUG-393 brownout income penalty. When false, the legacy brownout path
   * applies unchanged. Defaults to GRID_IMPORT_ENABLED_DEFAULT (true) for a
   * new city. Plain sim-state boolean (not React-local, not policies-keyed)
   * so it serialises/journals/replays exactly like every other field.
   * Optional for backward tolerance (mirrors roadNotice/demographicAccum etc.):
   * a legacy state predating this field is treated as GRID_IMPORT_ENABLED_DEFAULT
   * by every read site (never a silent `false`/off fallback).
   */
  gridImportEnabled?: boolean;
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
  /**
   * FEAT-1972079878 inc1 (AC-6) — one-year building capacity monitors. When a
   * building with scalable capacity is placed, a monitor is created and tracked
   * for one in-game year (TICKS_PER_YEAR). On each monthly boundary the engine
   * evaluates each monitor's building for auto-scale eligibility (online + utilization
   * ≥ 0.85), and if eligible, increments its capacityTier, charging the delta-cost
   * through flows. Fully serialisable (plain numbers) so it round-trips through save
   * and genesis-replay; deterministic, tick-driven — NO wall-clock.
   */
  buildingMonitors: BuildingMonitor[];
  /**
   * FEAT-1972079925 — running accumulator of this-month-so-far demographic
   * flows, flushed into `demographicHistory` and reset to zero at every
   * TICKS_PER_MONTH boundary (mirrors the roadMonitors/buildingMonitors
   * monthly-aggregate pattern). Optional for backward tolerance: a legacy
   * state without it starts accumulating from zero (see devcity.ts).
   */
  demographicAccum?: DemographicFlow;
  /**
   * FEAT-1972079925 — bounded ring of closed-month demographic aggregates
   * (births/deaths/moveIns/moveOuts + population), newest last, capped at
   * DEMOGRAPHIC_HISTORY_CAP months. Backs the population Sankey + trend
   * views. Optional for backward tolerance: a legacy state without it has an
   * empty history (honest empty state, not a fabricated one — GR#15).
   */
  demographicHistory?: MonthlyDemographics[];
  /**
   * FEAT-1972079925 — the four demographic flows computed by the LAST
   * advance() (mirrors `lastFlows` for fiscal flows). Lets tests and any
   * per-tick UI read the immediate churn without waiting for a month to
   * close. Optional for backward tolerance.
   */
  lastDemographics?: DemographicFlow;
  /**
   * FEAT-1972079926 — running accumulator of this-month-so-far arrivals-by-
   * mode split, flushed into `arrivalsByModeHistory` and reset to zero at
   * every TICKS_PER_MONTH boundary (mirrors `demographicAccum`). Optional
   * for backward tolerance: a legacy state without it starts accumulating
   * from zero (see devcity.ts).
   */
  arrivalsByModeAccum?: ArrivalsByMode;
  /**
   * FEAT-1972079926 — bounded ring of closed-month arrivals-by-mode
   * aggregates (mirrors `demographicHistory`). Backs the arrivals-by-mode
   * Sankey. Optional for backward tolerance: a legacy state without it has
   * an empty history (honest empty state, not a fabricated one — GR#15).
   */
  arrivalsByModeHistory?: MonthlyArrivalsByMode[];
  /**
   * FEAT-1972079926 — the arrivals-by-mode split computed by the LAST
   * advance() (mirrors `lastDemographics`). Optional for backward tolerance.
   */
  lastArrivalsByMode?: ArrivalsByMode;
  /**
   * FEAT-1972079923 inc1 (AC-1) — the insolvency band derived from `funds` every
   * tick via fiscal.insolvencyStateForFunds (pure, state-derived, no wall-clock).
   * 'solvent' above the warning threshold, 'warning' between the warning and
   * crisis thresholds (advance notice before the bailout flow), 'crisis' at or
   * below DEBT_THRESHOLD_FOR_BAILOUT (the future IMF bailout entry point — the
   * bailout EVENT itself, forced sales, administration and the decline screen
   * are inc2-4; inc1 only detects and surfaces the band). Optional for backward
   * tolerance: a legacy state without it is treated as 'solvent' until the next tick.
   */
  insolvencyState?: InsolvencyState;
  /**
   * FEAT-1972079923 inc1 (AC-8, scenario 1 only) — set ONCE, on the tick the
   * band transitions into 'crisis' from a non-crisis band, so the MapView popup
   * states the conditions exactly once per entry rather than every tick. Cleared
   * by the `dismissInsolvencyPopup` action (UI-only, not journaled — mirrors
   * `dismissNotice`). null when no popup is pending. Optional for backward tolerance.
   */
  insolvencyPopup?: { state: InsolvencyState; enteredAt: number } | null;
  /**
   * FEAT-1972079923 inc2 (AC-2) — the IMF BAILOUT EVENT state machine. Set
   * ONCE, on the same tick insolvencyPopup is stamped (band transitions INTO
   * 'crisis' from a non-crisis band, and no bailout is already active), and
   * cleared at the year-end re-evaluation (tick >= enteredAt +
   * BAILOUT_DURATION_TICKS) IF funds have recovered above
   * DEBT_THRESHOLD_FOR_BAILOUT by then — otherwise it stays active (no
   * transition; Administration Mode / second bailout are inc3/4 scope).
   * `enteredAt` is a tick number, never Date.now() (GR#21 determinism).
   * Optional for backward tolerance: a legacy state without it is treated as
   * no-bailout-active.
   */
  bailoutState?: { enteredAt: number } | null;
  /**
   * FEAT-1972079923 inc3 (AC-5, AC-6, AC-7) — ADMINISTRATION MODE state machine.
   * Set ONCE, by the user-initiated `enterAdministration` action (available only
   * while `bailoutState` is active — the alternative to forced asset sales), and
   * cleared exactly ADMINISTRATION_DURATION_TICKS later at the year-end
   * re-evaluation (AC-7), regardless of whether solvency was restored (recovery
   * reverts `insolvencyState` to the funds band; still-broke reverts it to
   * 'crisis' with no auto-transition to a second bailout — inc4 scope).
   * Entering administration also clears `bailoutState` (closes the FORCED ASSET
   * SALES panel, per AC-5). `enteredAt` is a tick number, never Date.now()
   * (GR#21 determinism). Optional for backward tolerance: a legacy state
   * without it is treated as no-administration-active.
   */
  administrationState?: { enteredAt: number; origin?: BailoutOrigin } | null;
  /**
   * FEAT-1972079923 inc4 (AC-10) — the SECOND IMF BAILOUT EVENT state machine.
   * AUTO-TRIGGERED (Aaron's round-2 ruling, 2026-08-31, OVERRIDES the BA
   * criteria doc's stale "user-initiated" text) at the year-end re-evaluation
   * of the FIRST bailout year — whether that year was spent under the plain
   * bailoutState or under administrationState — if the treasury is still at
   * or below DEBT_THRESHOLD_FOR_BAILOUT. Cleared at its OWN year-end
   * re-evaluation (tick >= enteredAt + SECOND_BAILOUT_DURATION_TICKS):
   * recovered (funds >= FINAL_DECLINE_FUNDS_THRESHOLD) reverts to the funds
   * band; still broke transitions to `declineState` (AC-11, hard game-over) —
   * no third bailout is ever offered. `enteredAt` is a tick number, never
   * Date.now() (GR#21 determinism). Optional for backward tolerance: a legacy
   * state without it is treated as no-second-bailout-active.
   */
  bailoutSecondState?: { enteredAt: number } | null;
  /**
   * FEAT-1972079923 inc4 (AC-11) — the FINAL DECLINE state: hard game-over.
   * Set ONCE, at the second bailout's year-end re-evaluation if funds are
   * still below FINAL_DECLINE_FUNDS_THRESHOLD. Once set, `advance()` short-
   * circuits to a no-op (the clock STOPS — no further ticks change state)
   * until an action is taken (Start Over / Load Save, both routed through the
   * GR#27 capture-before-wipe path). The stats below are captured AT the
   * decline tick from trackers maintained every tick since game start
   * (peakPopulation/minFundsEver/totalSpending), never fabricated defaults
   * (GR#15) and never recomputed after the freeze. Optional for backward
   * tolerance: a legacy state without it is treated as not-in-decline.
   */
  declineState?: DeclineState | null;
  /**
   * FEAT-1972079923 inc4 (AC-11) — running maximum of `population` ever
   * observed, updated every tick regardless of insolvency state, so the
   * decline screen's "Peak population" stat is a real computed value, not a
   * default (GR#15). Optional for backward tolerance: a legacy state without
   * it starts tracking from the current population.
   */
  peakPopulation?: number;
  /**
   * FEAT-1972079923 inc4 (AC-11) — running minimum of `fundsAtTickEnd` ever
   * observed, updated every tick, backing the decline screen's "Min funds
   * reached" stat. Optional for backward tolerance: a legacy state without it
   * starts tracking from the current funds.
   */
  minFundsEver?: number;
  /**
   * FEAT-1972079923 inc4 (AC-11) — running sum of every tick's outflows
   * (`expense`) since game start, backing the decline screen's "Total
   * spending" stat. Deliberately excludes inflows (bailout injections, asset
   * sales, taxes) — this is spend only. Optional for backward tolerance: a
   * legacy state without it starts accumulating from zero.
   */
  totalSpending?: number;
}

/**
 * FEAT-1972079923 inc4 (AC-10) — which bailout year an active
 * `administrationState` was entered FROM. Needed because entering
 * Administration clears BOTH `bailoutState` and `bailoutSecondState`
 * immediately (AC-5's "closes the FORCED ASSET SALES panel" behaviour,
 * extended to the second bailout in inc4) — without recording the origin,
 * the year-end re-evaluation could not tell whether "still broke" should
 * auto-trigger the second bailout (origin 'bailout') or the final decline
 * screen (origin 'bailout_second'). Optional on the state object itself for
 * backward tolerance: a legacy administrationState predating inc4 has no
 * `origin` and is treated as 'bailout' (the only origin that existed then).
 */
export type BailoutOrigin = 'bailout' | 'bailout_second';

/**
 * FEAT-1972079923 inc4 (AC-11) — decline statistics, computed ONCE at the
 * tick declineState is set, from trackers maintained every tick since game
 * start. Never zero/placeholder unless the game genuinely never had
 * population or spending (GR#15: honest, not fabricated).
 */
export interface DeclineState {
  /** Tick the decline screen was triggered (hard game-over). */
  enteredAt: number;
  /** Highest population ever reached during play. */
  peakPopulation: number;
  /** Population at the moment of decline. */
  finalPopulation: number;
  /** Lowest (most negative) funds value ever reached during play. */
  minFundsEver: number;
  /** Sum of every tick's outflows since game start. */
  totalSpending: number;
}

/**
 * FEAT-1972079923 inc1 (AC-1) — the insolvency band. Placeholder two-band model
 * for inc1 (detection + feedback only); inc2-4 build the actual bailout/
 * administration/second-bailout/decline state machine on top of the 'crisis'
 * band entry point ruled by Aaron (BOW FEAT-1972079923 comment, 2026-08-31).
 * 'administration' (inc3, AC-5/AC-6/AC-7) is an OVERLAY on top of the pure
 * funds-derived band: while `administrationState` is active, the exposed
 * `insolvencyState` reads 'administration' regardless of the underlying funds
 * band, reverting to the true funds band (solvent/warning/crisis) the tick
 * administration ends (AC-7). insolvencyStateForFunds() itself never returns
 * 'administration' — it is a pure funds→band classifier; the overlay is
 * applied in engine.advance().
 *
 * 'bailout_second' (inc4, AC-10) and 'decline' (inc4, AC-11) are further
 * overlays, same pattern: 'bailout_second' while `bailoutSecondState` is
 * active (auto-triggered — no user click — at the first bailout year's
 * still-broke re-evaluation), 'decline' once `declineState` is set (hard
 * game-over, permanent — advance() freezes the clock the instant it is set,
 * so no state ever transitions OUT of 'decline'). Overlay precedence, highest
 * first: decline > administration > bailout_second > the pure funds band
 * (which itself reads 'crisis' while a plain `bailoutState` is active).
 */
export type InsolvencyState = 'solvent' | 'warning' | 'crisis' | 'administration' | 'bailout_second' | 'decline';

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

/**
 * FEAT-1972079878 inc1 (AC-6): a single monitored building tracked for auto-scale.
 * All-number, JSON-round-trippable. `buildingId` identifies the building being monitored.
 * `until` is the tick the monitoring window closes (builtTick + TICKS_PER_YEAR).
 * `type` indicates whether to scale residents or jobs capacity.
 */
export interface BuildingMonitor {
  buildingId: number;
  until: number;
  type: 'residents' | 'jobs';
}
