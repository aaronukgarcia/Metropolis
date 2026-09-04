// debugjson.ts â€” FEAT-1972079886: the FULL-STATE debug.json capture.
//
// Aaron's requirement: the debug screen captures EVERYTHING about the running
// game â€” every UI tab's status â€” written into debug.json. This module is the
// single serializer for that file. It is PURE (no React, no Date.now, no
// localStorage): everything comes in through its two arguments, so node --test
// exercises the exact shipped logic and the same inputs always yield the same
// JSON (determinism is asserted by test/debugjson.test.mjs).
//
// RAW NUMBERS ONLY. Unlike snapshot.ts (the human-facing display frame), no
// value in this file goes through fmtMoney/fmtNum â€” debug.json is a machine
// artefact; the DISPLAY stays formatted elsewhere. The tests assert no
// Â£-prefixed figure ever appears in the serialized output.
//
// COVERAGE GUARANTEE. SIMSTATE_COVERAGE below maps EVERY top-level SimState
// key to the JSON path where it is represented. It is typed
// Record<keyof SimState, string>, so adding a SimState field without deciding
// where it lands in debug.json is a compile error â€” and the completeness test
// walks the runtime keys of a real state object against this map and resolves
// each path in the built JSON, so a forgotten serialization goes RED even if
// the type is widened. A future tab that adds sim state cannot silently skip
// debug.json.

import type {
  ArrivalsByMode,
  BailoutOrigin,
  Building,
  BuildingMonitor,
  Clipboard,
  ConsolidatorMode,
  ConsolidatorSliders,
  DeclineState,
  DemographicFlow,
  FlowItem,
  InsolvencyState,
  LedgerEntry,
  LevelUpNotice,
  MilestoneNotice,
  MonthlyArrivalsByMode,
  MonthlyDemographics,
  RoadMonitor,
  SimState,
  TaxRates,
  TickRecord,
  Tool,
  ZoneKind,
} from './types.ts';
import {
  FAMILIES,
  MAP_H,
  MAP_W,
  MILESTONES,
  PHYSICAL_ENTITIES,
  PIPE_TIERS,
  POLICIES,
  SPECS,
  UNIT_REGISTRY,
  blockOccupancy,
  countByKind,
  densityTier,
  isOnline,
  plantEffServed,
  powerStats,
  residentsCapacity,
  onlineResidentsCapacity,
  underConstructionResidents,
  offlineResidentsByReason,
  serviceDemandOf,
  totalJobs,
  utilisationOf,
  waterBalanceOf,
  waterDemandOf,
  waterPipeInfo,
  wasteStatsOf,
  collectionOpexOf,
  processingMixOf,
  efwPowerOf,
} from './data.ts';
import type { PipeTierAgg } from './data.ts';
import type { ConsolidationPass } from './consolidator.ts';
import { sanitizeCrimeRate, sanitizeCongestionTicksBySpec, sanitizeClaimedMilestones } from './data.ts';
import {
  HISTORY_CAP,
  LEDGER_CAP,
  SPEED_MS,
  approvalOf,
  demandOf,
  levelOf,
  wellbeingOf,
  xpForLevel,
  CONSOLIDATOR_ENABLED_DEFAULT,
  CONSOLIDATOR_MODE_DEFAULT,
  CONSOLIDATOR_SECTION_METRES_DEFAULT,
  CONSOLIDATOR_SLIDERS_DEFAULT,
} from './engine.ts';
import { SNAPSHOT_REFRESH_MS } from './throttle.ts';
import type { MapUiState } from './uistate.ts';
import { runConsistencyChecks } from './consistency.ts';
import {
  businessTaxPerTick,
  councilTaxPerTick,
  GRID_IMPORT_ENABLED_DEFAULT,
  UPKEEP_BUCKET,
} from './fiscal.ts';
import { getPerformanceSnapshot } from './perfhud.ts';
import { getLiveVersion } from './liveVersionRef.ts';

/** Bumped when the serialized shape changes incompatibly. */
export const DEBUG_JSON_FORMAT = 'metropolis-debug/1';

/** A lightweight, bounded snapshot of the sim state at crash time ("the heap"). */
export interface CapturedStateSummary {
  tick: number;
  funds: number;
  population: number;
  speed: number;
  policies: Record<string, boolean>;
}

/**
 * One captured error (GR#1, FEAT-1972079898): full context, not a bare line.
 * Produced by backend.recordError() and surfaced both here (debug.json `errors`)
 * and in the live "Errors captured" panel. Identical repeats are collapsed into
 * one record via `count` + `firstAt`/`lastAt` (so e.g. the "useSim must be used
 * inside SimProvider" spam becomes a single row).
 */
export interface CapturedError {
  /** Unique per distinct error — the correlation id. */
  correlationId: number;
  /** Where it came from. */
  type: 'window-error' | 'unhandledrejection' | 'render-crash' | 'reset-abort' | 'app';
  msg: string;
  /** JS stack (error.stack), when available. */
  stack?: string;
  /** React component tree that triggered a render crash (errorInfo.componentStack). */
  componentStack?: string;
  /** Optional action/context label. */
  action?: string;
  /** Epoch-ms of the first occurrence (its stack/heap are preserved). */
  firstAt: number;
  /** Epoch-ms of the most recent occurrence. */
  lastAt: number;
  /** Occurrences collapsed into this record (>= 1). */
  count: number;
  /** Bounded sim-state snapshot at first-capture time ("the heap"). */
  stateSummary: CapturedStateSummary | null;
  /** BAR-F5 (round-r1, FEAT-1972079916): true when this row is a cascade error
   * (a failed cleanup/sibling crash following a first render error) rather
   * than the root cause. */
  cascade?: boolean;
  /** BAR-F5: correlation id of the FIRST error this cascade followed. */
  firstCorrelationId?: number;
}

/** Everything the pure builder needs that is NOT sim state. */
export interface DebugUiInput {
  /** BUILD-TIME git-derived app version (versionRaw from sim/version â€” passed
   * in, not imported, so this module stays node-resolvable without the
   * generated version file). This is the FROZEN last-full-build value; it is
   * emitted as meta.buildVersion. meta.appVersion prefers the LIVE version (the
   * badge/HMR value from liveVersionRef) and only falls back to this when no
   * live poll has happened yet. See BUG-424. */
  appVersion: string;
  /** Epoch-ms of the frozen 15 s frame this JSON belongs to. */
  frameAtMs: number;
  /** Last-published map camera / selection (uistate.ts). */
  map: MapUiState;
  /** Captured errors (empty array until FEAT-1972079885 feeds it). */
  errors: CapturedError[];
}

export interface DebugJsonBuilding {
  id: number;
  spec: string;
  x: number;
  y: number;
  builtTick: number | null;
  online: boolean;
  /** Density/level tier 1..3 for zone blocks, null for network/services. */
  tier: 1 | 2 | 3 | null;
  /** Occupancy estimate 0..1 (3 dp), null where not applicable. */
  occ: number | null;
  /** Per-building utilisation: ratio 0..1 (3 dp) + basis formula name, null where not applicable. */
  util: { ratio: number; basis: string } | null;
  /** FEAT-1972079910 inc3 (AC-7): original rail line for rd_railbridge tiles. */
  bridgeOver?: string;
  /** FEAT-2326609740 §8: storey count (defaults to 1 for pre-feature saves). */
  heightStoreys: number;
  /** FEAT-2326609740 §8: true once both height cap and ladder end are hit. */
  scaleLocked: boolean;
  /** FEAT-2326609740 §8: current footprint tiles (defaults to the spec's base w/h). */
  footprintW: number;
  footprintH: number;
}

export interface ConsistencyReportJson {
  checks: Array<{ id: string; ok: boolean; detail: string }>;
  failures: number;
  /**
   * BUG-624: ids of the grace-eligible per-line flows checks
   * (consistency.ts's GRACE_ELIGIBLE_LINE_IDS) that failed on THIS build's
   * RAW evaluation, before any grace was applied. A caller that wants the
   * two-consecutive-failures tolerance on a LIVE, repeatedly-refreshed panel
   * (see buildDebugJson's `priorFailedLineIds` parameter) holds this array
   * from one refresh and feeds it back in as the next refresh's
   * `priorFailedLineIds` — held by the CALLER (a component ref / module-level
   * variable), never by SimState, so determinism is untouched and
   * buildDebugJson stays a pure function of its arguments. A one-shot caller
   * that never threads this (capture-before-wipe, every existing test) simply
   * never sees grace applied — see buildDebugJson's doc comment.
   *
   * BUG-640: this array itself is unchanged — it's still the current
   * refresh's raw fails. A caller wanting the windowed tolerance (closing
   * the alternating-parity blind spot) keeps a rolling queue of these arrays
   * across refreshes and folds them via consistency.ts's `foldGraceHistory`
   * into the `priorFailedLineIds` Map for the NEXT call — see DebugTab.
   */
  rawFailedLineIds: string[];
  /**
   * BUG-640 round-2: id -> delta (actual - computed) for every id in
   * `rawFailedLineIds`, on THIS build's RAW evaluation — see
   * consistency.ts's `ConsistencyReport.rawFailedSignatures` doc. A caller
   * wanting the windowed + signature-matched tolerance keeps a rolling
   * queue of THESE objects (not `rawFailedLineIds`) across refreshes and
   * folds them via `foldGraceHistory` — see DebugTab.
   */
  rawFailedSignatures: Record<string, number>;
}

export interface DebugJson {
  meta: {
    format: string;
    /** Freshest LIVE version (matches the badge). Diverges from buildVersion
     * during a long HMR session where the badge advanced but version.ts did
     * not — that divergence is itself diagnostic (BUG-424). Falls back to
     * buildVersion before the first live poll. */
    appVersion: string;
    /** The FROZEN build-time versionRaw (last full build). */
    buildVersion: string;
    generatedAt: string;
    generatedAtMs: number;
    tick: number;
    speed: SimState['speed'];
    tickMs: number;
  };
  sim: {
    tick: number;
    speed: SimState['speed'];
    funds: number;
    loanBalance: number;
    population: number;
    xp: number;
    level: number;
    xpToNext: number;
    taxRates: TaxRates;
    policies: SimState['policies'];
    approval: number;
    wellbeing: { overall: number; parts: { label: string; value: number }[] };
    nextId: number;
    nextLedgerId: number;
    lastRewardedLevel: number;
    notice: LevelUpNotice | null;
    unlockedAll: boolean;
    roadNotice: string | null;
    railNotice: string | null;
    placeNotice: string | null;
    /** FEAT-1972079923 inc1 (AC-1) — the derived insolvency band. */
    insolvencyState: InsolvencyState;
    /** BUG-496 — the RAW funds band (unoverlaid), driving one-shot popup-entry detection. */
    insolvencyRawBand: InsolvencyState;
    /** FEAT-1972079923 inc1 (AC-8) — the one-shot bailout-entry popup, or null. */
    insolvencyPopup: { state: InsolvencyState; enteredAt: number } | null;
    /** FEAT-1972079923 inc2 (AC-2) — the IMF bailout event state machine, or null. */
    bailoutState: { enteredAt: number } | null;
    /** FEAT-1972079923 inc3 (AC-5, AC-7) — the Administration Mode state machine, or null. */
    administrationState: { enteredAt: number; origin?: BailoutOrigin } | null;
    /** FEAT-1972079923 inc4 (AC-10) — the SECOND IMF bailout event state machine, or null. */
    bailoutSecondState: { enteredAt: number } | null;
    /** FEAT-1972079923 inc4 (AC-11) — the FINAL decline (hard game-over) state, or null. */
    declineState: DeclineState | null;
    /** FEAT-1972079923 inc4 (AC-11) — running peak population ever observed. */
    peakPopulation: number;
    /** FEAT-1972079923 inc4 (AC-11) — running minimum funds ever observed. */
    minFundsEver: number;
    /** FEAT-1972079923 inc4 (AC-11) — running total of all outflows since game start. */
    totalSpending: number;
    roadMonitors: RoadMonitor[];
    /** FEAT-1972079878 inc1 — building auto-scale demand monitors. */
    buildingMonitors: BuildingMonitor[];
    /** FEAT-1972079891 inc1 — connected road network (sorted "x,y" tiles). */
    roadConnectivity: { connectedRoadTiles: string[] };
    conservation: { tickStart: number; tickEnd: number };
    pendingRewards: Array<{ totalReward: number; newLevel: number; notice: LevelUpNotice }>;
    /** FEAT-2326609711 inc1 (AC-1) — external power cover toggle. */
    gridImportEnabled: boolean;
    /** FEAT-2326609761 inc1 (AC-1, ASM-1504) — consolidator enable toggle. */
    consolidatorEnabled: boolean;
    /** FEAT-2326609761 inc2 (Aaron's glide-mode/slider rulings, 2026-09-03/04). */
    consolidatorMode: ConsolidatorMode;
    consolidatorSectionMetres: number;
    consolidatorSliders: ConsolidatorSliders;
    /** BUG-504 Option A — how many FRESH first bailouts used so far (capped). */
    firstBailoutCount: number;
    /** BUG-506 (AC-506-1/2) — consecutive-tick sustained-recovery counter. */
    recoveryStreak: number;
    /** BUG-506 (AC-506-3/4) — rolling window of the last N ticks' funds. */
    recentFundsWindow: number[];
    /** FEAT-2326609723 (Play Mode) — the one-way sandbox latch. */
    playModeLatched: boolean;
    /** FEAT-dynamic-bailout — running total of every placementCost charged since genesis. */
    cumulativeCapexSpent: number;
    /** FEAT-dynamic-bailout — whether the old-save CAPEX backfill has run (once-only guard). */
    capexBackfilled: boolean;
    /** FEAT-dynamic-bailout — the ONE-WAY once-only dynamic bailout latch. */
    dynamicBailoutUsed: boolean;
    /** FEAT-crime-mechanic-2026-09-02 — last month-boundary-snapshotted crime rate. */
    crimeRatePreviousMonth: number;
    /** FEAT-congestion-teeth-2026-09-02 (AC-1) — per road-line-spec sustained-congestion tick counters. */
    congestionTicksBySpec: Record<string, number>;
    /** FEAT-milestone-cash-rewards-2026-09-02 (Q100047b) — ids of data.ts MILESTONES already paid out. */
    claimedMilestones: string[];
    /** FEAT-milestone-cash-rewards-2026-09-02 — milestone rewards claimed but not yet paid (drains next tick). */
    pendingMilestoneRewards: Array<{ totalReward: number; milestoneId: string; notice: MilestoneNotice }>;
    /** FEAT-milestone-cash-rewards-2026-09-02 — active milestone-reward notification banner, or null. */
    milestoneNotice: MilestoneNotice | null;
    /** FEAT-2326609761 (CONSOLIDATOR, AC-25) — newest-first pass log, capped at CONSOLIDATOR_LOG_CAP. */
    consolidatorLog: ConsolidationPass[];
    /** FEAT-2326609761 (CONSOLIDATOR, AC-26/ASM-1502, F4 fix) — single-level undo consumed-flag. */
    consolidatorUndoConsumed: boolean;
    /** BUG-652 GRANDFATHERING (2026-09-04) — economy schema-version counter; see SimState.economyEpoch's own doc comment. */
    economyEpoch: number;
  };
  flows: {
    inflows: FlowItem[];
    outflows: FlowItem[];
    incomePerTick: number;
    expensePerTick: number;
    netPerTick: number;
  };
  /**
   * FEAT-1972079925 — demographic flows: the LAST tick's births/deaths/
   * move-ins/move-outs, the running this-month-so-far accumulator, and the
   * bounded monthly history ring backing the population Sankey.
   */
  demographics: {
    lastTick: DemographicFlow;
    accumThisMonth: DemographicFlow;
    monthlyHistory: MonthlyDemographics[];
  };
  /**
   * FEAT-1972079926 — arrivals-by-mode: the LAST tick's split of moveIns
   * across transport modes, the running this-month-so-far accumulator, and
   * the bounded monthly history ring backing the arrivals-by-mode Sankey.
   */
  arrivalsByMode: {
    lastTick: ArrivalsByMode;
    accumThisMonth: ArrivalsByMode;
    monthlyHistory: MonthlyArrivalsByMode[];
  };
  demand: {
    zones: { residential: number; commercial: number; industrial: number };
    services: { id: string; label: string; value: number; spec: string }[];
    power: { needMw: number; capMw: number };
  };
  fiscal: {
    overview: {
      treasury: number;
      netPerTick: number;
      incomePerTick: number;
      expensePerTick: number;
      loanBalance: number;
      /** net/income fraction, null when income is 0. */
      margin: number | null;
      structures: number;
    };
    flowShares: {
      inflows: { label: string; value: number; share: number | null }[];
      outflows: { label: string; value: number; share: number | null }[];
    };
    ledger: { count: number; cap: number; entries: LedgerEntry[] };
    trend: {
      count: number;
      cap: number;
      history: TickRecord[];
      /** LeftDock TrendSummary over the last 72 ticks; null with <2 samples. */
      summary: {
        window: number;
        avgNetPerTick: number;
        fundsGrowth: number;
        popGrowth: number;
      } | null;
    };
  };
  info: {
    status: {
      approval: number;
      wellbeingOverall: number;
      wellbeingParts: { label: string; value: number }[];
      population: number;
      /** BUG-417: ONLINE residential capacity — what population can actually
       *  fill (offline / under-construction dwellings excluded). Honest headline. */
      housingCapacity: number;
      /** BUG-417: capacity still under construction (gross − online). */
      housingCapacityUnderConstruction: number;
      /** BUG-394: offline residential capacity built but stranded OFF the road
       *  network (G2/G3) — the actionable slice of housingCapacityUnderConstruction
       *  that needs the player to connect roads, distinct from genuinely-building. */
      housingCapacityDisconnected: number;
      /** BUG-417: gross residential capacity incl. offline dwellings (residentsCapacity). */
      housingCapacityGross: number;
      jobs: number;
      /** BUG-629: renamed from `solvent` — this is net >= 0 for THIS TICK's
       *  cashflow only, not the exposed insolvency band (sim.insolvencyState,
       *  a multi-tick/administration/bailout state machine). The old name read
       *  as a direct contradiction beside insolvencyState when a city was
       *  mid-crisis but had a single positive-net tick (or vice versa) —
       *  "solvent: false" next to "insolvencyState: solvent" looked like a
       *  bug report on its own. Additive rename: nothing parsed the old debug
       *  export field back in, so no migration is needed. */
      cashPositiveThisTick: boolean;
      netPerTick: number;
      structuresByFamily: { kind: ZoneKind; label: string; count: number; upkeep: number }[];
    };
    rates: {
      taxRates: TaxRates;
      avgRate: number;
      yieldsPerTick: TaxRates;
      basis: { citizens: number; commercialZones: number; industrialPlants: number };
    };
    units: {
      registry: typeof UNIT_REGISTRY;
      physicalEntities: typeof PHYSICAL_ENTITIES;
    };
    water: {
      cleanCapacity: number;
      wasteCapacity: number;
      /** Clean-water demand this tick (people), from waterDemandOf (SSOT). */
      cleanDemand: number;
      /** Waste-water demand this tick (people), from waterDemandOf (SSOT). */
      wasteDemand: number;
      ratio: number;
      leak: boolean;
      /** Per-tier pipe aggregate keyed by pipe-tier index (FEAT-1972079896):
       *  diameter label, mult, plant count, Σ effServed carried, and diameter
       *  headroom (tierUtil) + at-widest-diameter flag. See waterPipeInfo() for
       *  why this is NOT absolute flow saturation (no per-diameter throughput
       *  ceiling exists in the data — a PLACEHOLDER pending Aaron). */
      pipeTiers: Record<number, PipeTierAgg>;
      plants: {
        id: number;
        spec: string;
        name: string;
        x: number;
        y: number;
        pipeTier: number;
        pipeLabel: string;
        effServed: number;
        /** mult / widest-tier mult (0..1): pipe diameter headroom. */
        tierUtil: number;
        /** Pipe is on the widest tier — cannot be upgraded further. */
        atCeiling: boolean;
      }[];
    };
    /**
     * FEAT-1972079906 refuse — GENERATION + COLLECTION (inc1) and PROCESSING MIX
     * + DIVERSION KPI (inc2). All DERIVED (no SimState field), so this is a pure
     * read-out surfaced for the monitorable debug snapshot and the diversion KPI.
     */
    waste: {
      generated: number;
      collected: number;
      coverage: number;
      uncollected: number;
      collectionOpex: number;
      /** Tonnes routed to each processor this tick; landfill = the remainder. */
      efw: number;
      mrf: number;
      compost: number;
      landfill: number;
      /** efw + mrf + compost. */
      diverted: number;
      /** 1 − landfill share = diverted/collected — the total-recycling KPI (0..1). */
      diversionRate: number;
      /** MW the EfW plants add to grid capacity (throughput × MW-per-tonne). */
      efwPowerMw: number;
    };
    earnings: {
      rows: {
        type: string;
        basisCount: number;
        basisUnit: string;
        grossPerTick: number;
        eachPerTick: number | null;
      }[];
      totalInPerTick: number;
      totalOutPerTick: number;
      margin: number | null;
    };
    milestones: {
      achieved: string[];
      all: { id: string; label: string; detail: string; met: boolean }[];
    };
    experience: {
      level: number;
      xp: number;
      xpToNext: number;
      ladder: { level: number; unlocks: string[] }[];
    };
    specialists: {
      id: string;
      name: string;
      unlockLevel: number;
      locked: boolean;
      count: number;
    }[];
    policy: { id: string; label: string; on: boolean }[];
  };
  map: {
    tool: Tool;
    movingId: number | null;
    selectedBuildingId: number | null;
    view: { zoom: number; cx: number; cy: number } | null;
    showWater: boolean;
    clipboard: Clipboard | null;
    grid: { w: number; h: number; tileMetres: number };
  };
  buildings: {
    count: number;
    byKind: Partial<Record<ZoneKind, number>>;
    list: DebugJsonBuilding[];
  };
  errors: CapturedError[];
  consistency: ConsistencyReportJson;
  perfHud: {
    note: string;
    fps: { avgFps: number; p95Fps: number; worstFps: number } | null;
    tick: { avgMs: number; p95Ms: number; worstMs: number } | null;
    memoryMB: number | null;
    networkCalls: number;
    networkKB: number;
  } | null;
  snapshotFrame: {
    takenAtMs: number;
    takenAt: string;
    refreshPeriodMs: number;
  };
}

/**
 * Where every top-level SimState key surfaces in the built DebugJson, as a
 * dot-path. Typed exhaustively over keyof SimState â€” see the coverage
 * guarantee in the file header. Paths are resolved at test time against a
 * real built object, so a stale path here also fails.
 *
 * NOTE: `perfHud` is NOT a SimState field; it is a UI-layer performance
 * metric snapshot (wall-clock, non-deterministic) collected separately and
 * included in the output for completeness. It does not affect the SIMSTATE
 * coverage contract.
 */
export const SIMSTATE_COVERAGE: Record<keyof SimState, string> = {
  tick: 'meta.tick',
  speed: 'meta.speed',
  funds: 'sim.funds',
  loanBalance: 'sim.loanBalance',
  population: 'sim.population',
  xp: 'sim.xp',
  taxRates: 'sim.taxRates',
  policies: 'sim.policies',
  buildings: 'buildings.list',
  nextId: 'sim.nextId',
  movingId: 'map.movingId',
  tool: 'map.tool',
  clipboard: 'map.clipboard',
  pipeTier: 'info.water.pipeTiers',
  history: 'fiscal.trend.history',
  ledger: 'fiscal.ledger.entries',
  nextLedgerId: 'sim.nextLedgerId',
  lastFlows: 'flows',
  fundsAtTickStart: 'sim.conservation.tickStart',
  fundsAtTickEnd: 'sim.conservation.tickEnd',
  pendingRewards: 'sim.pendingRewards',
  lastRewardedLevel: 'sim.lastRewardedLevel',
  notice: 'sim.notice',
  unlockedAll: 'sim.unlockedAll',
  roadNotice: 'sim.roadNotice',
  railNotice: 'sim.railNotice',
  placeNotice: 'sim.placeNotice',
  insolvencyState: 'sim.insolvencyState',
  insolvencyRawBand: 'sim.insolvencyRawBand',
  insolvencyPopup: 'sim.insolvencyPopup',
  bailoutState: 'sim.bailoutState',
  administrationState: 'sim.administrationState',
  bailoutSecondState: 'sim.bailoutSecondState',
  declineState: 'sim.declineState',
  peakPopulation: 'sim.peakPopulation',
  minFundsEver: 'sim.minFundsEver',
  totalSpending: 'sim.totalSpending',
  roadMonitors: 'sim.roadMonitors',
  buildingMonitors: 'sim.buildingMonitors',
  roadConnectivity: 'sim.roadConnectivity',
  lastDemographics: 'demographics.lastTick',
  demographicAccum: 'demographics.accumThisMonth',
  demographicHistory: 'demographics.monthlyHistory',
  lastArrivalsByMode: 'arrivalsByMode.lastTick',
  arrivalsByModeAccum: 'arrivalsByMode.accumThisMonth',
  arrivalsByModeHistory: 'arrivalsByMode.monthlyHistory',
  // FEAT-2326609711 inc1 (AC-1).
  gridImportEnabled: 'sim.gridImportEnabled',
  // FEAT-2326609761 inc1 (AC-1, ASM-1504).
  consolidatorEnabled: 'sim.consolidatorEnabled',
  // FEAT-2326609761 inc2 (Aaron's glide-mode/slider rulings, 2026-09-03/04).
  consolidatorMode: 'sim.consolidatorMode',
  consolidatorSectionMetres: 'sim.consolidatorSectionMetres',
  consolidatorSliders: 'sim.consolidatorSliders',
  // BUG-504 Option A / BUG-506 / FEAT-2326609723 (FEAT-endgame-ladder).
  firstBailoutCount: 'sim.firstBailoutCount',
  recoveryStreak: 'sim.recoveryStreak',
  recentFundsWindow: 'sim.recentFundsWindow',
  playModeLatched: 'sim.playModeLatched',
  // FEAT-dynamic-bailout.
  cumulativeCapexSpent: 'sim.cumulativeCapexSpent',
  capexBackfilled: 'sim.capexBackfilled',
  dynamicBailoutUsed: 'sim.dynamicBailoutUsed',
  // FEAT-crime-mechanic-2026-09-02.
  crimeRatePreviousMonth: 'sim.crimeRatePreviousMonth',
  // FEAT-congestion-teeth-2026-09-02 (AC-1).
  congestionTicksBySpec: 'sim.congestionTicksBySpec',
  // FEAT-milestone-cash-rewards-2026-09-02 (Q100047b ruling B1).
  claimedMilestones: 'sim.claimedMilestones',
  pendingMilestoneRewards: 'sim.pendingMilestoneRewards',
  milestoneNotice: 'sim.milestoneNotice',
  // FEAT-2326609761 (CONSOLIDATOR mutation lane).
  consolidatorLog: 'sim.consolidatorLog',
  consolidatorUndoConsumed: 'sim.consolidatorUndoConsumed',
  // BUG-652 GRANDFATHERING (2026-09-04).
  economyEpoch: 'sim.economyEpoch',
  // Aaron ruling 2026-09-04 (land_tunnel footprint grandfather).
  tunnelFootprintEpoch: 'sim.tunnelFootprintEpoch',
};

const round3 = (n: number) => Math.round(n * 1000) / 1000;

function share(value: number, total: number): number | null {
  return total > 0 ? round3(value / total) : null;
}

/**
 * Build the complete raw-number debug.json object from sim + UI state.
 *
 * BUG-624 `priorFailedLineIds` (optional, default undefined): this function
 * stays a PURE serializer — no hidden module state, no history of its own.
 * `undefined` (every pre-existing call site: captureBeforeWipe's fail-closed
 * GR#27 capture, every debugjson.test.mjs assertion) means "no history" and
 * reproduces the ORIGINAL instant-fail consistency behaviour byte-for-byte —
 * see consistency.ts's runConsistencyChecks doc for why the capture path in
 * particular must NEVER thread grace: a pre-wipe snapshot exists to record
 * the RAW truth of the city at that instant, not a tolerance-smoothed view.
 * A caller that DOES want the two-consecutive-failures tolerance on a live,
 * repeatedly-refreshed panel (DebugTab) holds the PREVIOUS refresh's
 * `consistency.rawFailedLineIds` itself (a component ref, not SimState) and
 * passes it back in here as `priorFailedLineIds` on the NEXT refresh.
 *
 * BUG-640: `priorFailedLineIds` accepts either shape consistency.ts's
 * `runConsistencyChecks` does — a legacy `ReadonlySet<string>` (single
 * look-back) or the round-2 windowed + signature-matched
 * `ReadonlyMap<string, number[]>` produced by `foldGraceHistory` (fed the
 * caller's rolling queue of past `rawFailedSignatures`, NOT
 * `rawFailedLineIds`). This function is a pure pass-through either way, and
 * degrades safely (never throws) on any other shape — see consistency.ts's
 * GR#16 doc comment.
 */
export function buildDebugJson(
  s: SimState,
  ui: DebugUiInput,
  priorFailedLineIds?: ReadonlySet<string> | ReadonlyMap<string, number[]>
): DebugJson {
  const income = s.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const expense = s.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  const net = income - expense;
  const level = levelOf(s.xp);
  const xpToNext = Math.max(0, xpForLevel(level + 1) - s.xp);
  const wb = wellbeingOf(s);
  const approval = approvalOf(s);
  const pw = powerStats(s);
  const zoneDemand = demandOf(s);
  const c = countByKind(s.buildings);
  const bal = waterBalanceOf(s);
  const waterDemand = waterDemandOf(s);
  const pipeInfo = waterPipeInfo(s);
  const generatedAt = new Date(ui.frameAtMs).toISOString();
  const consistency = runConsistencyChecks(s, undefined, priorFailedLineIds);

  // buildings â€” full per-building list + present-kind counts
  const byKind: Partial<Record<ZoneKind, number>> = {};
  for (const [kind, n] of Object.entries(c) as [ZoneKind, number][]) {
    if (n > 0) byKind[kind] = n;
  }
  const list: DebugJsonBuilding[] = s.buildings.map((b: Building) => {
    const sp = SPECS[b.spec];
    const occ = blockOccupancy(s, b);
    const util = utilisationOf(s, b);
    const result: DebugJsonBuilding = {
      id: b.id,
      spec: b.spec,
      x: b.x,
      y: b.y,
      builtTick: b.builtTick ?? null,
      online: sp ? isOnline(s, b) : false,
      tier: sp && sp.category === 'zones' ? densityTier(sp) : null,
      occ: occ == null ? null : round3(occ),
      util: util == null ? null : { ratio: round3(util.ratio), basis: util.basis },
      // FEAT-2326609740 §8 (GR#16 defaults — never crash/silent-zero on an
      // old save that predates these fields).
      heightStoreys: b.heightStoreys ?? 1,
      scaleLocked: b.scaleLocked ?? false,
      footprintW: b.footprintW ?? sp?.w ?? 0,
      footprintH: b.footprintH ?? sp?.h ?? 0,
    };
    // AC-7: include bridgeOver for rail bridges
    if ((b as any).bridgeOver) result.bridgeOver = (b as any).bridgeOver;
    return result;
  });

  // fiscal trend summary â€” mirrors LeftDock's TrendSummary, raw
  const h72 = s.history.slice(-72);
  const trendSummary =
    h72.length < 2
      ? null
      : (() => {
          const avgNet = h72.reduce((a, b) => a + b.income - b.expense, 0) / h72.length;
          const first = h72[0];
          const last = h72[h72.length - 1];
          return {
            window: h72.length,
            avgNetPerTick: round3(avgNet),
            fundsGrowth:
              first.funds !== 0 ? round3((last.funds - first.funds) / Math.abs(first.funds)) : 0,
            popGrowth:
              first.population > 0
                ? round3((last.population - first.population) / first.population)
                : 0,
          };
        })();

  // Status tab structures table (nonzero families, matching the tab).
  //
  // BUG-628: `upkeep` here used to be an independent raw re-sum of sp.upkeep
  // over EVERY building of the family, regardless of online/offline state and
  // with no policy discount applied. The fiscal outflows line for the SAME
  // family (e.g. 'Roads') is booked by engine.computeFlows via UPKEEP_BUCKET
  // (fiscal.ts SSOT) — it (a) skips buildings failing isOnline() and (b) runs
  // through applyOutflowPolicies (recycling/austerity multipliers). Two
  // independent formulas for "what does this family cost" drifted apart —
  // e.g. Roads read 28,192 here against 28,024 on the actual outflow line, a
  // display-only 0.6% gap with no engine bug behind it. Fix: whenever this
  // family's ZoneKind is the ONLY kind charged into its UPKEEP_BUCKET label
  // (i.e. the outflow bucket total is unambiguously this family's own spend),
  // report that ALREADY-COMPUTED, policy-adjusted, online-only figure instead
  // of re-deriving a second number — one SSOT, zero drift. Shared buckets
  // (e.g. 'Commerce & Industry' = commercial+office+industrial+mine,
  // 'Power Grid' = power+pylon, 'Transport' = station+transport) can't be
  // re-attributed to a single family without inventing a split the engine
  // itself never computes, so those keep the raw per-family sum (unchanged
  // behaviour — a distinct, pre-existing "combined bucket" concern, not this
  // bug's asymmetry).
  const bucketOwnerCount: Partial<Record<string, number>> = {};
  for (const label of Object.values(UPKEEP_BUCKET)) {
    bucketOwnerCount[label] = (bucketOwnerCount[label] ?? 0) + 1;
  }
  const outflowByLabel = new Map(s.lastFlows.outflows.map((f) => [f.label, f.value]));
  const structuresByFamily = FAMILIES.flatMap((fam) => {
    let count = 0;
    let upkeep = 0;
    for (const b of s.buildings) {
      const sp = SPECS[b.spec];
      if (sp?.kind === fam.kind) {
        count++;
        upkeep += sp.upkeep;
      }
    }
    if (count === 0) return [];
    const bucketLabel = UPKEEP_BUCKET[fam.kind];
    if (bucketLabel && bucketOwnerCount[bucketLabel] === 1) {
      // Sole owner of this bucket — the outflow line IS this family's spend.
      // Missing from lastFlows.outflows means the bucket totalled 0 (e.g. every
      // instance is offline/under construction), which is correctly 0 upkeep
      // actually being charged, not the raw catalogue sum.
      upkeep = outflowByLabel.get(bucketLabel) ?? 0;
    }
    return [{ kind: fam.kind, label: fam.label, count, upkeep }];
  });

  // Rates tab yields (same formulas as the tab / computeFlows bases)
  const t = s.taxRates;
  const yieldsPerTick: TaxRates = {
    residential: councilTaxPerTick(s.population, t.residential),
    commercial: businessTaxPerTick(c.commercial, t.commercial),
    industrial: Math.round(c.industrial * t.industrial * 0.55),
  };

  // Water tab plants
  const plants = s.buildings
    .filter((b) => SPECS[b.spec]?.kind === 'water')
    .map((b) => {
      const sp = SPECS[b.spec];
      const tier = s.pipeTier[b.id] ?? 0;
      const pu = pipeInfo.plants.find((p) => p.id === b.id);
      return {
        id: b.id,
        spec: b.spec,
        name: sp.name,
        x: b.x,
        y: b.y,
        pipeTier: tier,
        pipeLabel: PIPE_TIERS[tier].label,
        effServed: plantEffServed(s, b),
        tierUtil: round3(pu?.tierUtil ?? 0),
        atCeiling: pu?.atCeiling ?? false,
      };
    });

  // Per-tier pipe aggregate (FEAT-1972079896), rounded for byte-stable JSON.
  const pipeTiers: Record<number, PipeTierAgg> = {};
  for (const [k, v] of Object.entries(pipeInfo.perTier)) {
    pipeTiers[Number(k)] = { ...v, tierUtil: round3(v.tierUtil) };
  }

  // Earnings tab rows (raw mirror of EarningsTab)
  const inflowValue = (label: string) =>
    s.lastFlows.inflows.find((f) => f.label === label)?.value ?? 0;
  const officeCount = s.buildings.filter((b) => SPECS[b.spec]?.kind === 'office').length;
  const earningsRows = [
    { type: 'Residential', basisCount: Math.max(s.population, 1), basisUnit: 'per citizen', grossPerTick: inflowValue('Council Tax') },
    { type: 'Commercial', basisCount: Math.max(c.commercial, 1), basisUnit: 'per zone', grossPerTick: inflowValue('Business Tax') },
    { type: 'Offices', basisCount: Math.max(officeCount, 1), basisUnit: 'per block', grossPerTick: inflowValue('Office Tax') },
    { type: 'Industrial', basisCount: Math.max(c.industrial, 1), basisUnit: 'per plant', grossPerTick: inflowValue('Freight Tax') },
    { type: 'Tourism', basisCount: s.policies.tourismDrive ? Math.max(s.population, 1) : 0, basisUnit: 'policy-driven', grossPerTick: inflowValue('Tourism') },
  ].map((r) => ({
    ...r,
    eachPerTick: r.basisCount > 0 ? round3(r.grossPerTick / r.basisCount) : null,
  }));
  const earningsIn = earningsRows.reduce((a, r) => a + r.grossPerTick, 0);

  // Milestones tab
  const milestonesAll = MILESTONES.map((m) => ({
    id: m.id,
    label: m.label,
    detail: m.detail,
    met: m.test(s),
  }));

  // Experience tab unlock ladder (same filter as XpTab)
  const byUnlock = new Map<number, string[]>();
  for (const sp of Object.values(SPECS)) {
    if (sp.unlock > 20 || sp.category === 'network') continue;
    const arr = byUnlock.get(sp.unlock) ?? [];
    arr.push(sp.name);
    byUnlock.set(sp.unlock, arr);
  }
  const ladder = Array.from({ length: 20 }, (_, i) => ({
    level: i + 1,
    unlocks: byUnlock.get(i + 1) ?? [],
  }));

  // Specialists tab (same filter as SpecialistsTab)
  const specialists = Object.values(SPECS)
    .filter((sp) => sp.kind === 'landmark' || sp.id === 'uni')
    .map((sp) => ({
      id: sp.id,
      name: sp.name,
      unlockLevel: sp.unlock,
      locked: level < sp.unlock,
      count: s.buildings.filter((b) => b.spec === sp.id).length,
    }));

  return {
    meta: {
      format: DEBUG_JSON_FORMAT,
      // BUG-424: appVersion reflects the freshest LIVE version (the badge),
      // falling back to the build-time value only before any live poll.
      // buildVersion always carries the frozen last-full-build versionRaw, so a
      // reader sees both and their divergence is diagnostic.
      appVersion: getLiveVersion() ?? ui.appVersion,
      buildVersion: ui.appVersion,
      generatedAt,
      generatedAtMs: ui.frameAtMs,
      tick: s.tick,
      speed: s.speed,
      tickMs: SPEED_MS[s.speed],
    },
    sim: {
      tick: s.tick,
      speed: s.speed,
      funds: s.funds,
      loanBalance: s.loanBalance,
      population: s.population,
      xp: s.xp,
      level,
      xpToNext,
      taxRates: s.taxRates,
      policies: s.policies,
      approval,
      wellbeing: wb,
      nextId: s.nextId,
      nextLedgerId: s.nextLedgerId,
      lastRewardedLevel: s.lastRewardedLevel,
      notice: s.notice,
      unlockedAll: s.unlockedAll,
      roadNotice: s.roadNotice,
      railNotice: s.railNotice,
      placeNotice: s.placeNotice,
      // FEAT-1972079923 inc1: defaults for a legacy state predating this field
      // (backward tolerance — mirrors roadConnectivity's `?? {...}` a few lines down).
      insolvencyState: s.insolvencyState ?? 'solvent',
      // BUG-496: the raw funds band must survive into debug.json (SIMSTATE_COVERAGE
      // maps it to sim.insolvencyRawBand); legacy states predating it default solvent.
      insolvencyRawBand: s.insolvencyRawBand ?? 'solvent',
      insolvencyPopup: s.insolvencyPopup ?? null,
      // FEAT-1972079923 inc2: backward tolerance for a legacy state predating bailoutState.
      bailoutState: s.bailoutState ?? null,
      // FEAT-1972079923 inc3: backward tolerance for a legacy state predating administrationState.
      administrationState: s.administrationState ?? null,
      // FEAT-1972079923 inc4: backward tolerance for a legacy state predating these fields.
      bailoutSecondState: s.bailoutSecondState ?? null,
      declineState: s.declineState ?? null,
      peakPopulation: s.peakPopulation ?? s.population,
      minFundsEver: s.minFundsEver ?? s.funds,
      totalSpending: s.totalSpending ?? 0,
      roadMonitors: s.roadMonitors,
      // FEAT-1972079878 inc1: building auto-scale demand monitors (parallel to
      // roadMonitors above) — must be serialized for save/load + replay parity
      // and to satisfy the SIMSTATE_COVERAGE completeness guarantee.
      buildingMonitors: s.buildingMonitors,
      // FEAT-1972079891 inc1: connected road network (defaults to empty when a
      // legacy/bespoke state predates the graph — see SimState.roadConnectivity).
      roadConnectivity: s.roadConnectivity ?? { connectedRoadTiles: [] },
      // TICK-BOUNDARY INVARIANT (Round-6): Conservation snapshot for determinism checking
      conservation: {
        tickStart: s.fundsAtTickStart,
        tickEnd: s.fundsAtTickEnd,
      },
      pendingRewards: s.pendingRewards,
      // FEAT-2326609711 inc1 (AC-1): backward tolerance for a legacy state
      // predating this field, mirrors insolvencyState/bailoutState above.
      gridImportEnabled: s.gridImportEnabled ?? GRID_IMPORT_ENABLED_DEFAULT,
      // FEAT-2326609761 inc1 (AC-1, ASM-1504): backward tolerance for a
      // legacy state predating this field, mirrors gridImportEnabled above.
      consolidatorEnabled: s.consolidatorEnabled ?? CONSOLIDATOR_ENABLED_DEFAULT,
      // FEAT-2326609761 inc2 (Aaron's glide-mode/slider rulings, 2026-09-03/04):
      // backward tolerance for a legacy state predating these fields, mirrors
      // consolidatorEnabled immediately above.
      consolidatorMode: s.consolidatorMode ?? CONSOLIDATOR_MODE_DEFAULT,
      consolidatorSectionMetres: s.consolidatorSectionMetres ?? CONSOLIDATOR_SECTION_METRES_DEFAULT,
      consolidatorSliders: s.consolidatorSliders ?? CONSOLIDATOR_SLIDERS_DEFAULT,
      // BUG-504 Option A / BUG-506 / FEAT-2326609723: backward tolerance for a
      // legacy state predating these fields, mirrors the fields above.
      firstBailoutCount: s.firstBailoutCount ?? 0,
      recoveryStreak: s.recoveryStreak ?? 0,
      recentFundsWindow: s.recentFundsWindow ?? [],
      playModeLatched: s.playModeLatched ?? false,
      // FEAT-dynamic-bailout: backward tolerance for a legacy state predating
      // these fields (sanitizeTreasury migrates them properly on the next
      // reducer() pass; this raw debug-snapshot read just needs a safe default).
      cumulativeCapexSpent: s.cumulativeCapexSpent ?? 0,
      capexBackfilled: s.capexBackfilled ?? false,
      dynamicBailoutUsed: s.dynamicBailoutUsed ?? false,
      // FEAT-crime-mechanic-2026-09-02 (round-1 F1, GR#16): sanitizeCrimeRate
      // covers BOTH backward tolerance for a legacy state predating this
      // field AND a corrupt save's non-number value (a raw `?? default`
      // only catches null/undefined — see crimeRateOf's doc comment).
      //
      // BUG-595 ruling (2026-09-02): kept the sanitised display, chose (a)
      // over dropping the wrap to show raw corruption. The "show the
      // corruption, it's a diagnostic tool" argument has real merit in the
      // abstract, but loses here on two counts specific to THIS field: (1)
      // debug.json is auto-verified by SIMSTATE_COVERAGE's completeness walk
      // AND consumed by other tooling (crime-mechanic tests, the reward/
      // conservation checks) that expects every emitted numeric field to be
      // a finite number in-domain — an unsanitised `"abc"` or `NaN` here
      // would silently poison anything downstream that reads debug.json
      // rather than the live SimState (GR#16 is explicit: never trust the
      // TS `number` annotation for stored data, coerce via the safeX()
      // helper at every boundary, and a debug export IS a boundary); and (2)
      // sanitizeCrimeRate does not erase the corruption signal — the value
      // it degrades TO (BASELINE_CRIME_RATE, 35) never coincides with a
      // genuine in-range live reading of exactly 35 by chance across a
      // whole run, and a diagnostician chasing corruption has the live
      // SimState / localStorage dump available for the raw byte-for-byte
      // value; debug.json's job is a SAFE, always-parseable snapshot, not
      // the corruption forensics tool itself. Pinned by
      // debugjson.test.mjs's "BUG-595" test (RED-proof: reverting this to a
      // raw `s.crimeRatePreviousMonth as number` cast turns it red).
      crimeRatePreviousMonth: sanitizeCrimeRate(s.crimeRatePreviousMonth),
      // FEAT-congestion-teeth-2026-09-02 (GR#16): same shape as crimeRatePreviousMonth
      // above — sanitizeCongestionTicksBySpec covers backward tolerance AND a corrupt
      // save's non-object/negative/fractional entries.
      congestionTicksBySpec: sanitizeCongestionTicksBySpec(s.congestionTicksBySpec),
      // FEAT-milestone-cash-rewards-2026-09-02 (GR#16): same shape as
      // crimeRatePreviousMonth/congestionTicksBySpec above — sanitizeClaimedMilestones
      // covers backward tolerance (legacy state -> []) AND a corrupt save's
      // non-array/non-string/unrecognised-id entries.
      claimedMilestones: sanitizeClaimedMilestones(s.claimedMilestones),
      pendingMilestoneRewards: s.pendingMilestoneRewards ?? [],
      milestoneNotice: s.milestoneNotice ?? null,
      // FEAT-2326609761 (CONSOLIDATOR mutation lane, AC-25/AC-34): GR#16
      // default — an old save predating this feature reads an empty pass
      // log (consolidatorEnabled's own default is set above,
      // CONSOLIDATOR_ENABLED_DEFAULT).
      consolidatorLog: s.consolidatorLog ?? [],
      consolidatorUndoConsumed: s.consolidatorUndoConsumed ?? false,
      // BUG-652 GRANDFATHERING (2026-09-04, GR#16): legacy state predating the
      // field reads epoch 0 — see SimState.economyEpoch's own doc comment.
      economyEpoch: s.economyEpoch ?? 0,
    },
    flows: {
      inflows: s.lastFlows.inflows,
      outflows: s.lastFlows.outflows,
      incomePerTick: income,
      expensePerTick: expense,
      netPerTick: net,
    },
    // FEAT-1972079925: demographic flows — defaults cover a legacy/bespoke
    // state predating this feature (backward tolerance, mirrors roadConnectivity).
    demographics: {
      lastTick: s.lastDemographics ?? { births: 0, deaths: 0, moveIns: 0, moveOuts: 0 },
      accumThisMonth: s.demographicAccum ?? { births: 0, deaths: 0, moveIns: 0, moveOuts: 0 },
      monthlyHistory: s.demographicHistory ?? [],
    },
    // FEAT-1972079926: arrivals-by-mode split — defaults cover a legacy/bespoke
    // state predating this feature (backward tolerance, mirrors demographics above).
    arrivalsByMode: {
      lastTick: s.lastArrivalsByMode ?? { road: 0, railLow: 0, railHs: 0, sea: 0, plane: 0 },
      accumThisMonth: s.arrivalsByModeAccum ?? { road: 0, railLow: 0, railHs: 0, sea: 0, plane: 0 },
      monthlyHistory: s.arrivalsByModeHistory ?? [],
    },
    demand: {
      zones: zoneDemand,
      services: serviceDemandOf(s),
      power: { needMw: pw.need, capMw: pw.cap },
    },
    fiscal: {
      overview: {
        treasury: s.funds,
        netPerTick: net,
        incomePerTick: income,
        expensePerTick: expense,
        loanBalance: s.loanBalance,
        margin: share(net, income),
        structures: s.buildings.length,
      },
      flowShares: {
        inflows: s.lastFlows.inflows.map((f) => ({ ...f, share: share(f.value, income) })),
        outflows: s.lastFlows.outflows.map((f) => ({ ...f, share: share(f.value, expense) })),
      },
      ledger: { count: s.ledger.length, cap: LEDGER_CAP, entries: s.ledger },
      trend: { count: s.history.length, cap: HISTORY_CAP, history: s.history, summary: trendSummary },
    },
    info: {
      status: {
        approval,
        wellbeingOverall: wb.overall,
        wellbeingParts: wb.parts,
        population: s.population,
        // BUG-417: honest headline = ONLINE capacity (what population can fill),
        // with the gross total and the under-construction remainder alongside so
        // the debug JSON explains a population pinned below the gross figure.
        housingCapacity: onlineResidentsCapacity(s),
        housingCapacityUnderConstruction: underConstructionResidents(s),
        housingCapacityDisconnected: offlineResidentsByReason(s).disconnected,
        housingCapacityGross: residentsCapacity(s),
        jobs: totalJobs(s),
        cashPositiveThisTick: net >= 0,
        netPerTick: net,
        structuresByFamily,
      },
      rates: {
        taxRates: s.taxRates,
        avgRate: round3((t.residential + t.commercial + t.industrial) / 3),
        yieldsPerTick,
        basis: {
          citizens: s.population,
          commercialZones: c.commercial,
          industrialPlants: c.industrial,
        },
      },
      units: { registry: UNIT_REGISTRY, physicalEntities: PHYSICAL_ENTITIES },
      water: {
        cleanCapacity: bal.clean,
        wasteCapacity: bal.waste,
        cleanDemand: waterDemand.clean,
        wasteDemand: waterDemand.waste,
        ratio: round3(bal.ratio),
        leak: bal.leak,
        pipeTiers,
        plants,
      },
      waste: (() => {
        const ws = wasteStatsOf(s);
        const pm = processingMixOf(s);
        return {
          generated: round3(ws.generated),
          collected: round3(ws.collected),
          coverage: round3(ws.coverage),
          uncollected: round3(ws.uncollected),
          collectionOpex: collectionOpexOf(s),
          efw: round3(pm.efw),
          mrf: round3(pm.mrf),
          compost: round3(pm.compost),
          landfill: round3(pm.landfill),
          diverted: round3(pm.diverted),
          diversionRate: round3(pm.diversionRate),
          efwPowerMw: round3(efwPowerOf(s)),
        };
      })(),
      earnings: {
        rows: earningsRows,
        totalInPerTick: earningsIn,
        totalOutPerTick: expense,
        margin: share(earningsIn - expense, earningsIn),
      },
      milestones: {
        achieved: milestonesAll.filter((m) => m.met).map((m) => m.label),
        all: milestonesAll,
      },
      experience: { level, xp: s.xp, xpToNext, ladder },
      specialists,
      policy: POLICIES.map((p) => ({ id: p.id, label: p.label, on: s.policies[p.id] })),
    },
    map: {
      tool: s.tool,
      movingId: s.movingId,
      selectedBuildingId: ui.map.selectedBuildingId,
      view: ui.map.view,
      showWater: ui.map.showWater,
      clipboard: s.clipboard,
      grid: { w: MAP_W, h: MAP_H, tileMetres: 50 },
    },
    buildings: { count: s.buildings.length, byKind, list },
    errors: ui.errors,
    consistency,
    perfHud: (() => {
      const snap = getPerformanceSnapshot();
      if (!snap) return null;
      return {
        note: 'wall-clock, non-deterministic',
        fps: { avgFps: round3(snap.fps.avgFps), p95Fps: round3(snap.fps.p95Fps), worstFps: round3(snap.fps.worstFps) },
        tick: { avgMs: round3(snap.tick.avgMs), p95Ms: round3(snap.tick.p95Ms), worstMs: round3(snap.tick.worstMs) },
        memoryMB: snap.memoryBytes === null ? null : round3(snap.memoryBytes / 1024 / 1024),
        networkCalls: snap.network.fetchCount,
        networkKB: round3(snap.network.fetchBytes / 1024),
      };
    })(),
    snapshotFrame: {
      takenAtMs: ui.frameAtMs,
      takenAt: generatedAt,
      refreshPeriodMs: SNAPSHOT_REFRESH_MS,
    },
  };
}

// Placeholder token for the buildings list during pretty-printing. Never
// appears in any sim/spec/ledger string, so the single replacement below is
// unambiguous.
const BUILDINGS_MARK = '@@BUILDINGS_LIST@@';

/**
 * Serialize a DebugJson to its canonical pretty text â€” indent 2 everywhere
 * EXCEPT buildings.list, whose entries are rendered one compact line each.
 * That keeps a 7,000-building city's file readable AND bounded: fully
 * indenting each building object would multiply the file several times over
 * for zero information. The JSON itself stays complete â€” nothing is elided.
 */
export function debugJsonText(dj: DebugJson): string {
  const withMark = {
    ...dj,
    buildings: {
      ...dj.buildings,
      list: BUILDINGS_MARK as unknown as DebugJsonBuilding[],
    },
  };
  const pretty = JSON.stringify(withMark, null, 2);
  const rows = dj.buildings.list.map((b) => '      ' + JSON.stringify(b));
  const inline = rows.length === 0 ? '[]' : '[\n' + rows.join(',\n') + '\n    ]';
  const token = JSON.stringify(BUILDINGS_MARK); // the quoted marker in `pretty`
  const i = pretty.indexOf(token);
  // The marker was placed by us two lines up; if it is somehow absent, return
  // the marked text rather than throwing â€” a debug artefact must never crash
  // the debug screen.
  if (i < 0) return pretty;
  return pretty.slice(0, i) + inline + pretty.slice(i + token.length);
}
