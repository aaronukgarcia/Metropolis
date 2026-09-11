// FEAT-2326609761 (CONSOLIDATOR mutation lane) — type-only import. Erased at
// compile time (this project type-strips .ts, no runtime import survives),
// so this does NOT create a runtime cycle even though consolidator.ts itself
// imports `type { SimState, ZoneKind }` from THIS file. See consolidator.ts's
// own header note on the engine.ts<->consolidator.ts cycle it had to avoid —
// a type-only edge here is safe by construction, a value edge would not be.
import type { ConsolidationPass } from './consolidator.ts';

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
  /**
   * FEAT-2326609740 (Aaron Q100076, "A PLUS B" up-then-out ladder): the
   * building's current storey count. Starts at the spec's implicit base (1
   * storey) and is incremented by each UP scale step (odd tier index),
   * capped per-spec by HEIGHT_CAP_STOREYS in data.ts. Absent on buildings
   * placed before this feature, or a building that has never scaled UP —
   * ALWAYS read via `building.heightStoreys ?? 1` (GR#16), never trusted bare.
   */
  heightStoreys?: number;
  /**
   * FEAT-2326609740: the building's current footprint width/height in
   * TILES, growing by +1 on each OUT scale step (even tier index). Absent
   * (or equal to the spec's base sp.w/sp.h) means the building has never
   * scaled OUT. ALWAYS read via `building.footprintW ?? sp.w` /
   * `building.footprintH ?? sp.h` (GR#16) — never sp.w/sp.h directly once a
   * building may have scaled, since the spec's base size no longer describes
   * every building of that spec.
   */
  footprintW?: number;
  footprintH?: number;
  /**
   * FEAT-2326609740 (§3.5/§14): true once the building has hit BOTH its
   * height cap (or is height-exempt, e.g. the NPP reactor ladder) AND the
   * end of its capacityTiers ladder, or has exhausted the up-then-out
   * lookahead this pass with no room left to climb. Its buildingMonitor is
   * removed the same pass this flips true — a locked building is DONE
   * auto-scaling for good (re-arming after a demolition frees space is a
   * known future gap, out of scope for this build — see the acceptance doc's
   * interim-assumption #4). Absent/undefined == false (GR#16 default).
   */
  scaleLocked?: boolean;
  /**
   * FEAT-2326609761 (CONSOLIDATOR, AC-21): provenance — did a PLAYER place
   * this building, or did the consolidator (`applyConsolidatorPass`,
   * engine.ts)? GR#16: an OLD save has no field at all, and `undefined` MUST
   * be read as `'player'` — the conservative default (a background process
   * must never be free to assume a pre-existing building is fair game just
   * because a field is missing). Every read site in this build reads
   * `b.placedBy ?? 'player'`, never `b.placedBy === 'auto'` bare.
   */
  placedBy?: 'player' | 'auto';
  /**
   * BUG-652 GRANDFATHERING (2026-09-04, round-mandated after the combined
   * FEAT-2326609763+BUG-652 estate was REJECTED on rollout for retroactively
   * re-pricing buildings a player already owns): when present, this building's
   * effective job count is PINNED to this value regardless of what its spec's
   * `jobs` field says today or ever says in the future — mirrors the
   * `footprintW ?? sp.w` convention exactly (a per-building override that
   * survives spec changes). Stamped ONLY by stampJobsGrandfather() (data.ts)
   * at load time onto a pre-existing building of one of the six BUG-652
   * specs (land_airport/hea_teaching/uni/land_tunnel/land_stadium/
   * station_ashford) found in a save whose `economyEpoch` predates
   * JOBS_GRANDFATHER_ECONOMY_EPOCH — always to 0 today (the pre-BUG-652
   * economy had no jobs field on any of these six specs at all). A building
   * placed AFTER the stamp existed (a fresh 'place' action, any session)
   * NEVER carries this field, so it falls through to the spec's real,
   * researched job count. Read via effectiveJobsOf() (data.ts) — never
   * `sp.jobs` directly for these six specs.
   */
  jobsOverride?: number;
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

/**
 * BUG-394 (2026-09-05 fix, revised same day per the opus-round-bug394 F4
 * finding) — the growth-model's per-tick diagnostic terms, so a future
 * freeze names itself in the debug JSON instead of requiring a repro
 * script. `attractiveness` is the RAW (uncapped) composite
 * jobs/wellbeing/coverage/tax/transit/station score (see engine.ts's
 * `attractivenessOf`) — it can exceed 1 (F2 finding). `marketInflow` is the
 * population-scaled gross interest BEFORE the attractiveness multiplier and
 * caps are applied. `inflowRate` (F4: previously a byte-for-byte duplicate
 * of `attractiveness`, which was a round-reject finding) now carries the
 * REAL final `grossInflow` — the actual number of movers this tick's growth
 * block computed BEFORE the effectiveHeadroom cap (moveIns = min(grossInflow,
 * effectiveHeadroom), see advance()'s growth block). `effectiveHeadroom`/
 * `capacity` are the same values the growth block itself computed this tick.
 */
export interface GrowthDiag {
  attractiveness: number;
  marketInflow: number;
  inflowRate: number;
  effectiveHeadroom: number;
  capacity: number;
  /**
   * G3 (2026-09-06 re-round finding, semantics corrected 2026-09-06
   * re-round-3): true iff the FLAT MAX_INFLOW_SHARE_OF_CAPACITY share is
   * what actually bound this tick's grossInflow — i.e. the progress
   * guarantee's own floor (minProgress) did NOT need to raise the ceiling
   * above the flat share, and the pre-cap number exceeded it. False
   * whenever the guarantee's floor exceeded the flat share (the effective
   * ceiling that tick was minProgress, not the flat cap), even if
   * grossInflow was reduced to reach that higher ceiling.
   */
  inflowCapped: boolean;
}

export interface LedgerEntry {
  id: number;
  tick: number;
  label: string;
  amount: number;
}

// FEAT-2326609801 inc8 (AC-1/AC-7): four congestion policy levers added to
// the existing boolean-toggle PolicyId surface (extend, never duplicate —
// data/policies.json's separate multiplicative-combination engine is out of
// scope this increment, see the doc's §1 scoping note). `parkAndRide` is
// deliberately NOT a member — AC-7 requires an unknown/unshipped PolicyId to
// be unreachable at the TYPE level, never silently ignored at runtime.
export type PolicyId =
  | 'recycling'
  | 'transitSubsidy'
  | 'tourismDrive'
  | 'austerity'
  | 'ownershipQuota'
  | 'roadPricing'
  | 'busPriority'
  | 'integratedTicketing';

/**
 * FEAT-2326609761 inc2 (Aaron's glide-mode ruling, 2026-09-04): the
 * consolidator's traversal mode. 'glide' is the DEFAULT continuous
 * one-tile-per-day scanline (consolidatorGlide.ts); 'monthly-twelfth' is the
 * pre-existing inc1 rotation (consolidator.ts's monthlyScopeOf), kept
 * selectable as the legacy mode. Both modes share the month-12 whole-tile
 * big-picture pass unconditionally (Aaron's addendum: "the FULL-TILE pass...
 * always still runs regardless of the player's chosen section size" — and,
 * by the same reasoning, regardless of traversal mode).
 */
export type ConsolidatorMode = 'glide' | 'monthly-twelfth';

/**
 * FEAT-2326609761 inc2 (Aaron's slider ruling, 2026-09-03): the four
 * non-dwelling economic-direction sliders, each a WHOLE PERCENTAGE POINT
 * (0..100). Deliberately closed to exactly these four employment kinds —
 * services are excluded by design (Aaron: they "consolidate on their own
 * need-based logic regardless of the slider mix"). validateConsolidatorSliders
 * (engine.ts) is the SSOT for "must sum to exactly 100".
 */
export interface ConsolidatorSliders {
  office: number;
  mining: number;
  farming: number;
  factory: number;
}

/** Level-up notification (FEAT-1972079884): what a crossing unlocked + the cash granted. */
export interface LevelUpNotice {
  level: number;
  /** Cash injection granted for reaching this level (already added to funds). */
  cash: number;
  /** Human-readable names of specs that unlock AT this level. */
  unlocked: string[];
}

/**
 * Milestone-reward notification (FEAT-milestone-cash-rewards-2026-09-02, Q100047b
 * ruling B1 — "an achieved milestone that does nothing reads as broken; small cash
 * + a notice at minimum"). Mirrors LevelUpNotice's shape/consumption pattern but
 * keyed by the milestone's data.ts MilestoneDef.id (a string) rather than a level
 * number, since milestones have no ordering/level concept.
 */
export interface MilestoneNotice {
  id: string;
  /** data.ts MilestoneDef.label, so the banner can say what was achieved. */
  label: string;
  /** Cash injection granted for reaching this milestone (already added to funds). */
  cash: number;
}

// ROUND r3 FIX (2026-09-04, F2): AffordabilityNotice / SimState.
// affordabilityNotice REMOVED — round r2 (INDEPENDENT DESTRUCTIVE, GR#23)
// found nothing under src/components ever read this field and nothing ever
// dispatched a confirmation, so a tripped gate was a permanent, silent,
// feedback-free no-op, AND the stale notice serialised into every save. The
// placement-affordability confirmation now lives ENTIRELY at the UI dispatch
// site (MapView.tsx calls data.ts's placementAffordability() directly,
// before ever constructing a 'place' action) — no SimState field, nothing
// journaled, nothing to go stale.

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
  /**
   * FEAT-2326609761 inc1 (AC-1, ASM-1504): the CONSOLIDATOR enable toggle.
   * Deliberately SIM STATE, not localStorage — every other feature flag in
   * this codebase (liveEngineFlag.ts, webWorkerFlag.ts, debugBuildSpeed.ts)
   * IS localStorage-backed, which is the trap this field exists to avoid: a
   * localStorage flag does not travel with the journal, so the same journal
   * would rebuild a DIFFERENT city on a different machine or after a cache
   * clear — silently breaking replay determinism (GR#21). Flipped ONLY by
   * the journalled `toggleConsolidator` action (engine.ts), never written
   * directly. Read-only consumers (this module's audit/discovery half) may
   * gate their own display on it once the mutation-lane pass exists; today
   * it also gates whether the map's section-focus overlay is shown.
   * Optional for backward tolerance (mirrors gridImportEnabled): an old save
   * predating this field is treated as `false` (CONSOLIDATOR_ENABLED_DEFAULT,
   * consolidator.ts) by every read site — loading a save must never silently
   * start demolishing the player's city (AC-34).
   */
  consolidatorEnabled?: boolean;
  /**
   * BUG-652 GRANDFATHERING (2026-09-04) — a schema-version counter for
   * economy-affecting migrations, DELIBERATELY SEPARATE from the app's
   * git-describe `buildVersion` string (which is not reliably ORDERABLE —
   * a git-describe string's commit-count suffix is not guaranteed
   * monotonic across branches/dirty flags, so comparing two of them to
   * decide "is this save older" would be an unsound, undocumented parser,
   * exactly the kind of fudge this task was told to avoid). A save/state
   * predating this field's introduction deserializes with it `undefined`;
   * every read site treats that as epoch 0 (mirrors gridImportEnabled's own
   * `?? DEFAULT` backward-tolerance convention immediately above).
   * initialState() always stamps the CURRENT epoch on a brand-new city.
   * stampJobsGrandfather() (data.ts) is the ONLY function that reads this to
   * decide whether a loaded snapshot's pre-existing buildings need their
   * jobs pinned to zero, and bumps it to current once applied.
   */
  economyEpoch?: number;

  /**
   * Aaron ruling 2026-09-04 ("the channel tunnel location needs to be
   * bigger too") — a schema-version counter for the land_tunnel footprint
   * grandfather migration, same idiom as `economyEpoch` immediately above
   * (deliberately a SEPARATE counter — orthogonal migration, no reason to
   * couple its timing to the unrelated jobs-schema one). A save predating
   * this field deserializes with it `undefined`, read as epoch 0 by
   * `stampTunnelFootprintGrandfather` (data.ts), which is the ONLY function
   * that reads/writes it: it stamps every pre-existing land_tunnel with no
   * footprintW/footprintH override to the OLD (pre-grow) footprint, then
   * bumps this to current so the migration never re-fires — critical,
   * because without that guard a LATER hydrate would misread a genuinely
   * NEW tunnel (placed after this fix, correctly reading the bigger spec
   * footprint via footprintOf's `?? sp.w/sp.h` fallback) as "legacy" too,
   * wrongly shrinking it back down. initialState() always stamps the
   * CURRENT epoch on a brand-new city (it has no legacy tunnels to migrate).
   */
  tunnelFootprintEpoch?: number;
  /**
   * P0 RCA fix (Aaron, 2026-09-04): "I created a whole new map city 13 and
   * never once placed any gorges dams... yet I saved and started a new map"
   * — the OLD city was resurrected over the NEW one because savepoint slots
   * were global (`metropolis.savepoint.{0,1,2}`, lineage-blind) and BUG-469's
   * tick-only overwrite gate could never let a brand-new (low-tick) city's
   * autosave land over an old (high-tick) one occupying the same slots.
   * `lineageId` is an OPAQUE per-city identity, minted ONCE at every genesis
   * point (the boot-time fresh-city fallback, `freshStart`, `loadDevCity1`,
   * the 'reset' reducer case via the dispatched action's own `lineageId` —
   * see engine.ts's 'reset' case for why it is NOT minted with
   * Math.random/Date.now INSIDE the reducer, which would break GR#21
   * determinism) and carried unchanged through every subsequent tick,
   * savepoint, journal entry, GameSave, and pre-wipe archive entry for that
   * city's whole lifetime — it is bookkeeping identity, never read by any
   * gameplay computation. `replay.ts` uses it to namespace savepoint slots
   * per-lineage (`metropolis.savepoint.<lineageId>.<slot>`) so two cities can
   * never compete for the same rotation slots or overwrite-protection
   * comparison again. Absent (`undefined`) on a save written before this
   * field existed — treated as the reserved `'legacy'` lineage, which maps
   * to the SAME unnamespaced keys every save already used (zero storage
   * migration, zero behaviour change for an existing player's next boot).
   */
  lineageId?: string;
  /**
   * FEAT-2326609761 inc2 (Aaron's glide-mode ruling, 2026-09-04): which
   * traversal mode the consolidator uses to pick its focus window. 'glide'
   * (the DEFAULT — "glide is the default mode unless the player switches")
   * is the continuous one-tile-per-day scanline (consolidatorGlide.ts);
   * 'monthly-twelfth' is the pre-existing inc1 rotation (consolidator.ts's
   * monthlyScopeOf) kept as a selectable legacy mode. Journalled sim state
   * (ASM-1504) — it changes which part of the map the mutation lane's pass
   * touches on a given day, so replay determinism depends on it exactly
   * like consolidatorEnabled. Optional for backward tolerance: an old save
   * predating this field is treated as CONSOLIDATOR_MODE_DEFAULT ('glide',
   * engine.ts — lives there, not consolidator.ts, for the same import-cycle
   * reason documented on CONSOLIDATOR_ENABLED_DEFAULT) by every read site.
   */
  consolidatorMode?: ConsolidatorMode;
  /**
   * BUG-397 F1 (round REJECT fix, 2026-09-05) — whether the Transit Subsidy
   * outflow was CLAMPED by POLICY_COST_CAP_FRACTION on the LAST tick
   * computeFlows() ran (engine.ts). Existed purely to detect the bind/release
   * TRANSITION so a cap-notice ledger row is written once, not every tick the
   * cap continues to bind — an unconditional per-tick notice is the BUG-400
   * class: an amount:0 row every tick evicts every real player event out of
   * the ledger's 200-row ring within a few hundred ticks on any city where
   * the cap binds continuously. Plain journalled sim-state boolean (mirrors
   * gridImportEnabled/consolidatorEnabled's idiom immediately above/below) so
   * it serialises/journals/replays exactly like every other field — the
   * transition detection must survive save/load and replay, not just live in
   * a single running session. Optional for backward tolerance: an old save
   * predating this field is treated as `false` (not currently bound) by
   * computeFlows()'s `s.transitSubsidyCapBound ?? false` read — worst case a
   * legacy save that resumes mid-cap re-emits one bind notice it may have
   * already shown once before saving, never a flood.
   */
  transitSubsidyCapBound?: boolean;
  /**
   * FEAT-2326609761 inc2 (Aaron's ruling, 2026-09-03: "player can... set the
   * size of the consolidator"): the player-adjustable section/window size in
   * METRES (mirrors CONSOLIDATOR_SECTION_METRES's unit). Defaults to 800m
   * (the inc1 ruling value) but the player may widen or narrow it within
   * CONSOLIDATOR_SECTION_METRES_MIN/MAX (engine.ts, derived from the same
   * real-savepoint measurement that set the 800m default — never a bare new
   * literal). Feeds BOTH the glide window's width (consolidatorGlide.ts)
   * and, for the mutation lane, the audit/opportunity section size. Journalled
   * sim state (ASM-1504) — a size change mid-glide changes which window the
   * NEXT day derives (consolidatorGlide.ts's cursor is pure, no resume-state
   * needed). Optional for backward tolerance: an old save predating this
   * field is treated as CONSOLIDATOR_SECTION_METRES_DEFAULT (engine.ts).
   */
  consolidatorSectionMetres?: number;
  /**
   * FEAT-2326609761 inc2 (Aaron's ruling, 2026-09-03): the economic-direction
   * sliders — "Office / Mining / Farming / Factory, constrained to sum to
   * exactly 100 percent, steering which employment type non-dwelling
   * consolidation converts TOWARD". Services (education/health/power/police/
   * water/waste/etc) are NOT steered by this — they "consolidate on their
   * own need-based logic regardless of the slider mix" (Aaron's words) — so
   * this is deliberately scoped to the four non-dwelling employment kinds
   * only, never a general-purpose weighting the mutation lane could misapply
   * to services. Journalled sim state (ASM-1504): it changes what the
   * consolidator's objective function targets, so replay determinism depends
   * on it. Optional for backward tolerance: an old save predating this field
   * is treated as CONSOLIDATOR_SLIDERS_DEFAULT (engine.ts, an even 25/25/25/
   * 25 split — "a kept mixture" is Aaron's own neutral-default phrase).
   * validateConsolidatorSliders (engine.ts) is the SSOT for "must sum
   * to 100" — the reducer refuses any action that fails it (never silently
   * clamped/normalised, so a bad dispatch is visibly a no-op, not a silent
   * distortion of the player's intended mix).
   */
  consolidatorSliders?: ConsolidatorSliders;
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
   * BUG-394 — the growth model's diagnostic terms from the LAST advance()
   * (mirrors `lastDemographics`). Optional for backward tolerance.
   */
  lastGrowthDiag?: GrowthDiag;
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
   * BUG-496 fix — the RAW funds-derived band (insolvencyStateForFunds output),
   * persisted SEPARATELY from the exposed/overlaid `insolvencyState` above.
   * `insolvencyState` gets overlaid to 'decline'/'administration'/'bailout_second'
   * while the underlying funds band is still 'crisis', so comparing the raw band
   * against the EXPOSED previous value made "transitioned into crisis" evaluate
   * true on every tick a bailout/administration overlay was active — the popup
   * (and the AC-2 bailout injection, which happened to be separately guarded by
   * `bailoutState === null` and so was unaffected) must compare raw-to-raw.
   * Optional for backward tolerance: a legacy state without it is treated as
   * 'solvent' until the next tick recomputes it.
   */
  insolvencyRawBand?: InsolvencyState;
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
  /**
   * BUG-504 Option A (FEAT-endgame-ladder, Aaron ruling Q100045, 2026-09-02) —
   * how many FRESH first bailouts (engine.ts's fresh-trigger branch, NOT the
   * automatic first->second escalation) this playthrough has used, capped at
   * fiscal.MAX_FIRST_BAILOUTS before a fresh crisis is forced straight to the
   * second bailout instead of re-issuing a free grant — closes BUG-504's
   * unbounded yearly re-rescue loop. Optional for backward tolerance: a
   * legacy state without it is treated as 0 (no re-arms used yet).
   */
  firstBailoutCount?: number;
  /**
   * BUG-506 (AC-506-1/2) — consecutive-tick counter of SUSTAINED recovery
   * (funds >= 0) while EITHER bailout is active, reset to 0 the instant funds
   * dip below 0 or no bailout is active. Reaching
   * fiscal.SUSTAINED_RECOVERY_TICKS clears the active bailout EARLY, before
   * its year-end checkpoint. Optional for backward tolerance: a legacy state
   * without it starts the streak at 0.
   */
  recoveryStreak?: number;
  /**
   * BUG-506 (AC-506-3/4) — a rolling window of the last
   * fiscal.DECLINE_AVERAGING_WINDOW_TICKS ticks' sanitized funds, updated
   * EVERY tick regardless of insolvency state. The decline year-end decision
   * reads the MEAN of this window rather than a single-tick sample, so one
   * bad tick in an otherwise-solvent year no longer forces a hard game-over
   * (and one lucky tick in an otherwise-insolvent year no longer buys a
   * reprieve). Optional for backward tolerance: a legacy state without it
   * starts the window empty (the very next tick begins filling it).
   */
  recentFundsWindow?: number[];
  /**
   * FEAT-2326609723 (Play Mode, Aaron ruling Q100045's escape-hatch
   * companion, 2026-09-02) — a ONE-WAY, set-once latch: once true, no action
   * ever sets it back to false (engine.ts's `enterPlayMode` reducer case is
   * the ONLY writer, and it is idempotent once already latched). Engaging
   * Play Mode credits fiscal.PLAY_MODE_INJECTION_AMOUNT (a deliberately
   * implausible "trillion" sandbox sum) as a clearly-labelled inflow and
   * clears every insolvency overlay so the player can keep building. A
   * latched session is EXCLUDED from being used as a genesis-replay/AB
   * determinism REFERENCE (see genesisReplay.ts's canUseAsReplayReference) —
   * it is a deliberate sandbox deviation, not a valid economy run. Optional
   * for backward tolerance: a legacy state without it is treated as `false`
   * (never latched).
   */
  playModeLatched?: boolean;
  /**
   * FEAT-dynamic-bailout (Aaron ruling Q100045, 2026-09-02) — running total of
   * every `placementCost` charged since genesis (or, for a save predating this
   * feature, a one-time backfill proxy — see `capexBackfilled` below). NEVER
   * decremented by refunds/demolitions/forced asset sales (gross spend only —
   * a refund is a separate ledger event, per the spec's §7.1 recommendation:
   * netting would let a demolish/rebuild cycle manipulate the dynamic bailout
   * offer downward right before triggering crisis). Feeds
   * fiscal.computeDynamicBailoutOffer's CAPEX-allowance term. Optional for
   * backward tolerance: a legacy state without it is backfilled once (see
   * `capexBackfilled`) rather than treated as a bare 0, so an old, large,
   * already-mid-crisis save never sees a false "tiny city, tiny offer" cliff.
   */
  cumulativeCapexSpent?: number;
  /**
   * FEAT-dynamic-bailout — set ONCE, the first time a save is sanitized
   * (engine.ts's sanitizeTreasury, which runs at the top of every reducer()
   * call) with `cumulativeCapexSpent` absent. A brand-new game starts with
   * this already `true` (genesis has spent nothing yet — no backfill needed).
   * An old save missing `cumulativeCapexSpent` gets backfilled EXACTLY ONCE
   * from the CURRENT standing asset base (sum of `placementCost` for every
   * building present at load — a reasonable "what it would cost to build
   * what's standing today" proxy, understating true lifetime spend but never
   * overstating it, and never zero for a real city), then this flag flips
   * true so the sum is never repeated on subsequent loads/actions. Optional
   * for backward tolerance: a legacy state without it is treated as `false`
   * (backfill still owed).
   */
  capexBackfilled?: boolean;
  /**
   * FEAT-dynamic-bailout (Aaron ruling Q100045: "this only happens once. then
   * that's it.") — a ONE-WAY, set-once latch: once true, no action ever sets
   * it back to false (mirrors `playModeLatched`'s shape exactly). Replaces
   * `firstBailoutCount`'s re-arm-counting role now that the maximum is
   * strictly one, not `fiscal.MAX_FIRST_BAILOUTS` re-arms — a bool is simpler
   * than a counter once the cap is 1. Set the SAME tick the ONE dynamic
   * bailout offer is credited (engine.ts advance()'s fresh-crisis-trigger
   * branch); once true, a subsequent crisis entry credits NOTHING NEW at the
   * first tier — the (unchanged) worse-terms second-bailout escalation is
   * what fires instead (engine.ts's FEAT-dynamic-bailout scoping note).
   * Migrated on load for a save that predates this feature (see the FEAT-
   * dynamic-bailout spec's §4 migration table, applied once in
   * sanitizeTreasury alongside the capex backfill): a save already mid-
   * bailout, past a first bailout, in/past a second bailout, in
   * administration, or in decline is marked `true` immediately (no-double-dip
   * — they already had their "once"); a save that is genuinely solvent with
   * no bailout history at all starts `false`. Optional for backward
   * tolerance: a legacy state without it is migrated on first sanitize
   * (never read as a bare default without going through that migration).
   */
  dynamicBailoutUsed?: boolean;
  /**
   * FEAT-crime-mechanic-2026-09-02 (Q100046 D2, Q100069 rec-on-all Q4) —
   * the crimeRateOf() result SNAPSHOTTED by engine.ts's advance() at each
   * month boundary (tick % TICKS_PER_MONTH === 0), read back by the NEXT
   * month's crimeRateOf() call as its "crime breeds crime" feedback input.
   * This is a genuine one-month LAG, not same-tick self-reference — the
   * value written this month is computed from the value stored BEFORE the
   * write (see advance()'s crimeRatePreviousMonth assignment, which reads
   * `next` — a state that still carries the OLD field — before overwriting
   * it). Optional for backward tolerance: a legacy state without it is
   * treated as data.ts's BASELINE_CRIME_RATE (a new city starts crime
   * calculations exactly where a fresh genesis state would).
   */
  crimeRatePreviousMonth?: number;
  /**
   * FEAT-congestion-teeth-2026-09-02 (Q100057 A1, Q100071 rec-on-all) —
   * per road/motorway-class line spec id, consecutive ticks that line's
   * saturation (data.ts lineUsageOf) has been >= CONGESTION_PENALTY_THRESHOLD,
   * capped at CONGESTION_SUSTAINED_TICKS (data.ts CONGESTION_CONSTANTS — the
   * counter never needs to exceed the value that already proves "sustained").
   * RESET RULE: a line's counter is hard-reset to 0 the instant its saturation
   * drops below the threshold (no decay/grace window — mirrors BUG-506's
   * recoveryStreak idiom), and specs with a zero count are OMITTED from the
   * map entirely (keeps the record small/deterministic and self-pruning when
   * a line is bulldozed). engine.ts's advance() is the SOLE writer, computed
   * from THIS tick's own lineUsageOf(next) — there is no circular risk
   * because congestion depends only on buildings/population, never on
   * wellbeing (see congestionFactorOf's doc comment, data.ts). Optional for
   * backward tolerance: a legacy state without it is treated as `{}` (no
   * line has ever been sustained — a fresh genesis city starts penalty-free,
   * AC-4).
   */
  congestionTicksBySpec?: Record<string, number>;
  /**
   * FEAT-2326609798 inc5 (AC-2, ASM-1517) — per line-segment id, consecutive
   * ticks that segment's v/c has been >= CONGESTION_PENALTY_THRESHOLD, capped
   * at CONGESTION_SUSTAINED_TICKS (data.ts CONGESTION_CONSTANTS) — the SAME
   * counter shape as `congestionTicksBySpec` above, one grain finer (segment
   * instead of line-class). engine.ts's advance() is the SOLE writer,
   * computed from THIS tick's own trafficAssignment.ts segmentDelayOf(next)
   * via gridlockedSegmentsOf, mirroring congestionTicksBySpec's exact
   * same-tick-write/next-tick-read lag rule — read back ONLY by the NEXT
   * tick's trafficWellbeing.ts gridlockWellbeingPartOf(s), never same-tick.
   * Optional for backward tolerance: a legacy save without this field is
   * treated as `{}` (no segment has ever been gridlocked — an old save loads
   * with a fresh, penalty-free gridlock history, exactly like
   * congestionTicksBySpec's own backward-compat rule).
   */
  gridlockTicksBySegment?: Record<string, number>;
  /**
   * FEAT-2326609798 inc5 r2 (BUG-877 cadence fix, Lead amendment 1 after r1
   * REJECT row 7607) — the three traffic wellbeing inputs (commute median
   * minutes, trip-weighted gridlock share, ambulance coverageShare),
   * refreshed by engine.ts's advance() only on a data-sourced cadence
   * (trafficRecomputeTicks, data/traffic.json) or when this field is absent
   * (fresh state / old save — computed on the very first advance()). The
   * three trafficWellbeing.ts parts read ONLY this snapshot — never the raw
   * traffic-assignment derivations directly — so no per-tick traffic
   * assignment runs (a full assignment measured 7.6ms -> 728ms, 96x, at
   * 20,000 buildings, BUG-877). Optional for backward tolerance: a legacy
   * save without this field is treated as absent (traffic wellbeing parts
   * neutral until the next advance() computes one), exactly like
   * gridlockTicksBySegment's own backward-compat rule above.
   */
  trafficSnapshot?: {
    tick: number;
    medianCommuteMinutes: number;
    gridlockShare: number;
    coverageShare: number | null;
    /**
     * FEAT-2326609800 inc7 r3 (BUG-929 lead amendment) — the money-path
     * inputs that used to be recomputed from the live assignment EVERY tick
     * (the 2.19x reducer-tick regression the r2 round measured) now refresh
     * only on this same traffic cadence, exactly like medianCommuteMinutes/
     * gridlockShare/coverageShare above. computeFlows()/advanceRoadWear()
     * read ONLY these fields — never trafficAssignment.ts's live
     * assignedFlowByClassOf/cityVehicleKmByClassOf/etc. directly — so a
     * non-cadence tick does zero assignment work (pinned by
     * trafficAssignment.ts's exported op counter). Optional for backward
     * tolerance: a legacy save's trafficSnapshot predates inc7 and simply
     * lacks these fields — treated as "absent", triggering one fresh
     * bootstrap compute on the next advance()/computeFlows() call, exactly
     * like a snapshot-less fresh city.
     */
    fuelLitresDemanded?: number;
    /** AC-3 — total annual VED (GBP/year, not yet divided by TICKS_PER_YEAR). */
    vedAnnualGbp?: number;
    /**
     * AC-4/AC-5 wear-increment inputs, one entry per segment currently
     * carrying routed flow: the per-tick ESAL delta this segment accrues
     * (Σ_class flow x segmentKm x esalFactorPer100VehicleKm / 100, computed
     * ONCE per cadence window and reapplied every tick until the next
     * refresh — the same cadence-lag approximation gridlockTicksBySegment
     * already uses) plus the roadClassId a repair event on this segment
     * should be priced against.
     */
    // wearSegments carries EVERY current road segment (delta 0 for a segment
    // with no flow this cadence window), so its own key set doubles as the
    // "which segments still exist" bound for roadWearBySegment orphan
    // pruning — no separate validSegmentIds field needed.
    wearSegments?: Record<string, { roadClassId: string; deltaEsalPerTick: number }>;
  };
  /**
   * FEAT-2326609800 inc7 (AC-4, ASM-1534) — per-segment cumulative ESAL
   * (Equivalent Single-Axle Load) wear, mirroring `congestionTicksBySpec`'s
   * exact shape: engine.ts's `advance()` is the SOLE writer, computed each
   * tick from `trafficAssignment.ts`'s `assignedFlowByClassOf` x segment-km x
   * `road_wear.json` esalFactors (never a hand-typed ratio, GR#15). Optional
   * for backward tolerance: a legacy state without it sanitizes to `{}` (zero
   * wear on every segment — old saves are not retroactively penalised, AC-8).
   * Resets a segment's entry to 0 the tick its repair cost is actually paid
   * (AC-6, a resurfacing event) — never on a mere read of an over-threshold
   * segment.
   */
  roadWearBySegment?: Record<string, number>;
  /**
   * BUG-915 — debug/informational field ONLY (never read by conservation or
   * money logic): segment ids whose repair crossed the trigger THIS tick
   * but was DEFERRED because this tick's starting funds could not cover the
   * rounded total repair cost. Empty array when nothing was due or every
   * due repair was affordable. Optional for backward tolerance: a legacy
   * state without it sanitizes to `[]`.
   */
  roadRepairDeferredSegmentIds?: string[];
  /**
   * FEAT-milestone-cash-rewards-2026-09-02 (Q100047b ruling B1) — ids of
   * data.ts MILESTONES already paid out, so a one-time cash reward + notice
   * fires EXACTLY ONCE per milestone no matter how many times its predicate
   * flips true/false/true again afterward (e.g. m5 "Solvent City" can lose
   * and regain its 60-tick surplus window many times over a game). The id is
   * added to this set the SAME tick the predicate is first observed true
   * (engine.ts's advance(), evaluated against the fully-assembled next
   * state); the cash itself lands one tick later via pendingMilestoneRewards
   * below, mirroring lastRewardedLevel/pendingRewards' claim-now/pay-next-
   * tick split. Optional for backward tolerance: a legacy state without it
   * is treated as `[]` (GR#16 — see data.ts's sanitizeClaimedMilestones,
   * which ALSO drops any id not in the current MILESTONES catalogue so a
   * future catalogue edit can't leave a dangling/unrecognised claim). A
   * fresh save starts with every milestone unclaimed; an OLD save loaded
   * with milestones already met retroactively pays them once on the first
   * tick that observes them met-but-unrewarded (Aaron's steer, 2026-09-02) —
   * simplest and player-friendly, and a fresh save can never double-pay
   * because the claimed set persists across saves/loads.
   */
  claimedMilestones?: string[];
  /**
   * Queue of milestone rewards claimed this tick but not yet paid (mirrors
   * pendingRewards' queue-and-drain-through-inflows idiom exactly). Drained
   * by the NEXT advance() call into inflows + a ledger row, so the cash
   * participates in the tick-boundary conservation invariant
   * (fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows) on the tick
   * it actually lands. Optional for backward tolerance: a legacy state
   * without it is treated as `[]` (nothing queued).
   */
  pendingMilestoneRewards?: Array<{ totalReward: number; milestoneId: string; notice: MilestoneNotice }>;
  /** Active milestone-reward notification banner, or null when dismissed / none pending. */
  milestoneNotice?: MilestoneNotice | null;
  /**
   * FEAT-2326609761 (CONSOLIDATOR, AC-25). Newest-first, capped at
   * CONSOLIDATOR_LOG_CAP (engine.ts) — mirrors `ledger`'s own ring-buffer
   * idiom. `consolidatorUndo` (AC-26) reverses exactly `consolidatorLog[0]`
   * and pops it; single-level by design (ASM-1502 — Aaron said "the last
   * pass", and a multi-level stack would need a full state-history buffer at
   * ~1.77MB/SimState clone, the BUG-592 memory profile this project already
   * ruled out). Optional for backward tolerance: an old save predating this
   * field is read as `[]`.
   */
  consolidatorLog?: ConsolidationPass[];
  /**
   * FEAT-2326609761 (CONSOLIDATOR, AC-26/ASM-1502) — F4 FIX (independent
   * round finding, "undo is NOT single-level"): `consolidatorLog[0]` alone
   * is not enough to enforce "only the last pass is undoable", because
   * POPping the log after a successful undo makes the PREVIOUS pass the new
   * `log[0]` — a second `consolidatorUndo` press would then happily reverse
   * that older pass too, chaining backward through the whole 20-entry ring
   * one press at a time against a map that has long since moved on. This
   * flag is the single-level gate: `consolidatorUndo` refuses (reference
   * identity, never an error) whenever it is `true`; it is set `true` the
   * moment an undo succeeds, and reset to `false` the moment a NEW pass is
   * appended to `consolidatorLog` (a fresh pass earns a fresh, one-time
   * undo). Optional for backward tolerance: an old save predating this field
   * is read as `false` via `state.consolidatorUndoConsumed ?? false` (safe —
   * such a save has no consolidatorLog either, so the flag is moot until a
   * pass actually runs).
   */
  consolidatorUndoConsumed?: boolean;
  /**
   * FEAT-2326609779 (consolidator inc3, AC-8). F5 FIX (independent round
   * finding, MEDIUM — "write-only and grows unboundedly"): keyed by
   * SECTION KEY (stringified), value = the growth-reserve tile "x,y" keys
   * CURRENTLY unclaimed in that section. Every time a layout pass touches a
   * section it REPLACES that section's own entry wholesale from its fresh
   * `FreeSpaceAllocation.reserve` (never merges/accumulates across passes),
   * so the structure is bounded by "sections ever touched x that section's
   * own tile count" at any snapshot, not by how many PASSES have ever run —
   * the previous per-tile-flat-map design merged forever and never pruned a
   * section once written, which is what made it grow unboundedly. A tile
   * dropping out of a section's list the next time that section is
   * revisited (because it got claimed by a tier, per `reservedTilesReused`
   * on the audit) is the AC-8 "reuse" path; a section with zero remaining
   * reserve tiles has its key removed entirely rather than kept as `[]`.
   * Optional for backward tolerance (AC-13): an old save predating inc3, or
   * one with no reserve state yet, is read as `{}` — every read site uses
   * `state.consolidatorReservedTiles ?? {}`, never a bare property access.
   */
  consolidatorReservedTiles?: Record<string, string[]>;
  /**
   * FEAT-2326609779 (consolidator inc3): master on/off for the whole
   * tier-hierarchy layout stage, separate from `consolidatorEnabled` (the
   * inc1/inc2 building-consolidation/reconnect engine, unaffected either
   * way). Defaults to `true` (`?? true` at every read site) since the
   * independent destructive round's adjudication (2026-09-04): the
   * conservation fear that originally justified an `?? false` rollout-safety
   * default was FOUND UNFOUNDED (`advance()` re-derives funds wholly from
   * flow lines computed AFTER this stage runs, so new tiles' upkeep is
   * already booked — see engine.ts's applyConsolidatorPass file header for
   * the full adjudication and the round's A1-A4 proofs). AC-13 still holds:
   * an old save predating this field reads as `true` — the layout stage
   * simply starts running the NEXT time `consolidatorEnabled` is on and a
   * pass fires, exactly the same "opt-in behaviour resumes on the next
   * pass" contract inc1/inc2 fields already use; it never touches or
   * reinterprets any EXISTING pre-inc3 log entry (still read-only, still
   * `tierLayout: undefined`). An explicit `consolidatorLayoutEnabled: false`
   * still turns the whole stage off.
   */
  consolidatorLayoutEnabled?: boolean;
  /**
   * BUG-684 FIX (round-6 F1b): the tier-layout upkeep-affordability gate's
   * anchor — the city's net income per tick, measured ONCE (the first pass
   * the layout stage ever runs, or lazily on the first such pass after an
   * old save without this field loads) and NEVER rewritten by the layout
   * stage's own subsequent spend. See consolidatorLayout.ts's
   * `layoutUpkeepEffectiveFloorOf` doc for the full rationale (recomputing
   * this every pass from `cur.lastFlows` let the floor ratchet down forever
   * on its own damage). `null`/absent = "not yet anchored".
   */
  consolidatorLayoutBaselineNetIncome?: number | null;
  /**
   * BUG-684 FIX (round-6 F1b): running LIFETIME total of recurring upkeep
   * the tier-layout stage has committed since `consolidatorLayoutBaselineNetIncome`
   * was anchored, across every pass — genuinely accumulated (`+=` each
   * pass's own delta at finalize, `-=` on Undo), never reset or overwritten.
   * Absent/undefined (old saves) reads as 0.
   *
   * ROUND-12 REJECT CORRECTION (P1-A, dated 2026-09-05): this doc has
   * described the field as "lifetime" since round 6, but engine.ts actually
   * OVERWROTE it with each pass's own delta rather than accumulating —
   * making the doc false and letting lifetime upkeep growth run unbounded
   * (measured ~1,300/pass forever on a dogfood city). Fixed to match this
   * doc, not the other way round. TWO separate mechanisms now read this
   * field, and neither is "the per-pass gate consumes from it" (that
   * sentence described the round-6-era design, superseded by round 14):
   *   - the per-pass upkeep-affordability floor
   *     (`layoutUpkeepEffectiveFloorOf`) uses a FRESH, income-scaled
   *     per-pass allowance every pass (never this field) — see round 14's
   *     own doc in consolidatorLayout.ts for why a shrinking lifetime pool
   *     there caused permanent starvation;
   *   - the NEW lifetime ceiling (`LAYOUT_LIFETIME_UPKEEP_SHARE_OF_INCOME`,
   *     consolidatorLayout.ts) reads THIS field to cap how much of the
   *     per-pass allowance may still be spent before the city's total
   *     layout-added upkeep would exceed that share of its current tax
   *     income — the missing ceiling half of the two-bound contract.
   */
  consolidatorLayoutCumulativeUpkeepDelta?: number;

  /**
   * FEAT-2326609779 inc4 (THE RED BOX RE-PLAN — Aaron: "the red box needs to
   * defag and reimmagine everything within it and optimise join"). Which box
   * POSITION the currently-running re-plan job belongs to, as
   * `"x0,y0,w,h"` — the glide window's own rectangle. The red box moves one
   * tile per game day, so this changes constantly by design; the cursor below
   * resets to 0 whenever it does.
   *
   * The job's CORRECTNESS never depends on either field: `planBox` is a pure
   * function of (box, contents, ports, ladder, seed) and `buildSteps` omits
   * every step the current contents already satisfy, so the step list SHRINKS
   * as work lands and the head of it is always the next real thing to do.
   * That self-cursoring property is what makes a save/load mid-job identical
   * without persisting the plan itself (which would be a large, redundant,
   * drift-prone blob). These two fields exist for REPORTING — the
   * consolidator log, the debug JSON and the consolidator tab's
   * "Re-plan: N/M steps" line — and for bounding per-tick work. `undefined`
   * on every save predating inc4, read as "no job running" (engine.ts uses
   * `?? null` / `?? 0`, never a bare property access — GR#16).
   */
  consolidatorReplanPlanKey?: string | null;
  /** FEAT-2326609779 inc4: how many re-plan steps have been EXECUTED at `consolidatorReplanPlanKey`'s box position. Display/bounding only — see that field's doc for why correctness never depends on it. */
  consolidatorReplanStepCursor?: number;
  /**
   * FEAT-2326609779 inc4, LEAD RULING "THE BOX DWELLS" (2026-09-06): the tick
   * whose glide window the red box is currently PINNED to, or null/absent when
   * the window is free-running (the pre-dwell behaviour). While set, the
   * engine derives the box from THIS tick rather than the live one, so the box
   * stays put until its re-plan converges — which is the only way the
   * lines-then-civic step order ever reaches its civic steps (measured: 0
   * consolidated civics in 900 ticks before this existed). Released on
   * convergence, on an invariant-failure discard, or after
   * REPLAN_MAX_DWELL_DAYS (MET-V874), so the scanline can never be pinned
   * forever. `undefined` on every save predating inc4 — read with `?? null`
   * (GR#16), never a bare property access.
   */
  consolidatorReplanDwellStartTick?: number | null;
  /**
   * FEAT-2326609779 inc4, ROUND-15 (5): the glide day the window RESUMES from
   * once a dwell ends — one box width past the box that just finished, never
   * the live tick. Without it, a 30-day dwell on a 16-wide box teleported the
   * window 30 days forward on release, so only 1 column in 31 was ever
   * re-planned. `undefined`/null means "no dwell has ended yet"; read with
   * `?? null` (GR#16).
   */
  consolidatorReplanDwellResumeDay?: number | null;
  /**
   * FEAT-2326609779 inc4, ROUND-16 REJECT (A) — THE RE-PLAN'S OWN LIFETIME
   * UPKEEP LINE. The re-plan used to add its `upkeepDelta` to the EXTENDER's
   * `layoutRunningUpkeepDelta`, which finalises into
   * `consolidatorLayoutCumulativeUpkeepDelta` — the extender's LIFETIME
   * ceiling for every section on the whole map. One red box laying a rail row
   * and a motorway column therefore exhausted the extender's lifetime budget
   * and killed it CITY-WIDE: measured by the round at 'layout paused: lifetime
   * upkeep ceiling' on 32 of 32 passes, with scatterFixture's extender
   * transactions falling from 124 (baseline) to 0 (lane).
   *
   * The two stages now keep SEPARATE books with the SAME shape: nothing the
   * re-plan does may move the extender's counters, and vice versa. Cumulative
   * and persisted, exactly like its extender twin (`+=` at finalize, `-=` on
   * Undo), so the ceiling is a real bound and not an overwrite.
   */
  consolidatorReplanCumulativeUpkeepDelta?: number;
  /**
   * FEAT-2326609779 inc4: the GLIDE DAY the red box is pinned to while it
   * dwells — distinct from `consolidatorReplanDwellStartTick`, which is the
   * real TICK the dwell began on.
   *
   * MY REGRESSION, fixed here (round-15 follow-up): I conflated the two. The
   * dwell cap is measured in real ticks (`tick - dwellStartTick`), but I set
   * `dwellStartTick` to the pinned GLIDE DAY, which lags the live tick by
   * however long previous dwells lasted. `tick - dwellStartTick` therefore
   * measured elapsed-time-since-that-glide-day, not the age of this dwell, and
   * sailed past REPLAN_MAX_DWELL_DAYS — the estate's own bound test caught it.
   * Two fields, two meanings, no arithmetic between them.
   */
  consolidatorReplanPinnedDay?: number | null;
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
  /**
   * FEAT-2326609740: extended beyond residents/jobs to cover the new
   * service-spec ladders (§2/§11 of the acceptance doc) — 'children' for
   * schools, 'served' for health + police, 'mw' for the NPP reactor ladder
   * (Q100089=B). Each type reads the matching data.ts aggregate
   * (residentsCapacity/totalJobs/totalChildrenCapacity/totalServedCapacity/
   * powerStats) as its utilization denominator, same population-based-proxy
   * style the original 'jobs' type already used (see evaluateBuildingMonitors).
   */
  type: 'residents' | 'jobs' | 'children' | 'served' | 'mw';
}
