import type { Building, Dims, SimState, ZoneKind } from './types.ts';
import { formatPower, fmtMoney } from './utils.ts';
// FEAT-1972079877: isPlaceable defers real specs to the existing unlock gate.
// specUnlocked lives in engine.ts, which itself imports from data.ts — this is a
// function-only (call-time) cyclic import: neither module uses the other at
// module-eval time, so ESM live bindings resolve it safely.
import { specUnlocked } from './engine.ts';
// FEAT-1972079902 rail-inc1: road/m20 line-usage reuses road inc2's per-building
// traffic weight + city activity ramp (GR#3 SSOT) — call-time (cyclic-safe) imports,
// same pattern as specUnlocked above. Neither is used at module-eval time.
import { feederTrafficWeight, trafficActivity } from './engine.ts';
// FEAT-crime-mechanic-2026-09-02: crimeRateOf's wellbeing-feedback input reads
// engine.ts's wellbeingCoreOf (the wellbeing computation MINUS the Crime part
// itself) — same call-time cyclic-import pattern as specUnlocked above.
// wellbeingCoreOf never calls crimeRateOf, so this one-way edge plus the
// month-lagged crimeRatePreviousMonth field (types.ts) together keep the
// crime<->wellbeing feedback loop acyclic within a single tick (see the long
// comment on crimeRateOf below for the full argument).
import { wellbeingCoreOf } from './engine.ts';
// FEAT-2326609772 (largest-first rebase fix, 2026-09-05): largestFirstFill's
// right-sized-one-shot rule (see its doc comment) needs to know whether two
// candidates are the SAME consolidator family (e.g. edu_nursery next to its
// own civic-tier successor edu_nursery_city) — familyKeyOf lives in
// consolidator.ts, which itself imports SPECS/canEnterSim/etc from data.ts,
// so this is the SAME function-only (call-time) cyclic import pattern as
// specUnlocked above: familyKeyOf is a plain function declaration, never
// invoked at either module's eval time, so the live ESM binding resolves
// safely.
import { familyKeyOf } from './consolidator.ts';
// FEAT-159: DEBUG-ONLY per-class fast-build override (off by default).
import { scaleConstructionTicks } from './debugBuildSpeed.ts';
// FEAT-2326609711 inc1: fiscal.ts is a leaf module (imports only ./types.ts),
// so importing GRID_IMPORT_ENABLED_DEFAULT here creates no cycle.
import { GRID_IMPORT_ENABLED_DEFAULT, STARTING_TREASURY } from './fiscal.ts';
// FEAT-wage-stage1 (Q100067/Q100086, 2026-09-03): same leaf-module guarantee
// as the import above — fiscal.ts's KIND_TO_WAGE_SECTOR/SectorJobs are pulled
// in here (not the other way around) so totalJobsBySector() below can bucket
// jobs by sector using fiscal.ts's SSOT kind->sector classification, with no
// eval-time cycle risk.
import {
  KIND_TO_WAGE_SECTOR,
  type SectorJobs,
  ZERO_SECTOR_JOBS,
  allocateFilledJobs,
  sectorWagesPerTick,
} from './fiscal.ts';
// BUG-511: registry-sourced (GR#7) fail-loud guard for the residential
// no-`residents` trap below (assertResidentialSpecsHaveResidents). backend.ts
// is a leaf for this purpose too — it imports only commitqueue.ts and a
// type-only debugjson.ts, never data.ts — so this import is call-time/
// module-eval safe and introduces no cycle.
import { codedError, recordError } from './backend.ts';

export const MAP_W = 440;
export const MAP_H = 260;

export const ROW_BAND = 10;

// BUG-657 (Aaron, 2026-09-03: "on the left edge of the map it's just got a Z on
// each row, what's that for?") — a misplaced closing parenthesis made the clamp
// a no-op and then a FLOOR: `Math.min(band)` with one argument returns `band`
// unchanged, and `Math.max(0, band, 25)` is then >= 25 for every input, so every
// row label rendered 'Z' (65 + 25) regardless of y. The gutter was uniform Z's,
// and because coordLabel() builds on this, EVERY coordinate shown to the player
// — the inspector's "grid Z,374", place notices, the debug JSON — carried the
// same dead letter. Intended: clamp the band into 0..25 (A..Z), i.e. the 25
// belongs inside the min. ROW_BAND is 10 and MAP_H is 260, so bands run 0..25
// and the clamp is exactly saturating at the last row.
export function yLabel(y: number): string {
  return String.fromCharCode(65 + Math.max(0, Math.min(Math.floor(y / ROW_BAND), 25)));
}

export function coordLabel(x: number, y: number): string {
  return `${yLabel(y)},${x + 1}`;
}

export type Tag = 'pollution' | 'clean' | 'waste';

export interface Spec {
  id: string;
  kind: ZoneKind;
  name: string;
  blurb: string;
  w: number;
  h: number;
  cost: number;
  upkeep: number;
  color: string;
  category: 'network' | 'zones' | 'services';
  unlock: number;
  tag?: Tag;
  residents?: number;
  children?: number;
  stage?: 'nursery' | 'primary' | 'city' | 'tertiary';
  served?: number;
  mw?: number;
  jobs?: number;
  /**
   * FEAT-2326609761 civic-tier round F1 (2026-09-05, GR#15 data-driven
   * discriminator): distinguishes health specs that share `kind: 'health'`
   * and capacity field `served` but must NEVER be treated as consolidation
   * successors of each other. 'local' = geographically-distributed primary/
   * emergency coverage (hea_clinic, hea_ambulance) — Aaron's own words:
   * "clinics/ambulances are local coverage, never merge them into one
   * hospital". 'regional' = hospital-tier escalation (hea_hospital,
   * hea_teaching) — Aaron's own consolidation example ("one teaching
   * hospital replaces many hospitals") stays valid because both sit in
   * 'regional'. consolidator.ts's familyKeyOf folds this in as a fifth
   * key segment so a 'local' spec can never share a family with a
   * 'regional' one even though kind/capacityField/tag/stage all match.
   * Present ONLY on health-kind specs today; undefined (and therefore a
   * no-op '' segment) everywhere else.
   */
  careTier?: 'local' | 'regional';
  tourism?: number;
  dims?: Dims;
  /**
   * FEAT-1972079907 inc1 — road tier ladder (1..5) + per-tile vehicle capacity.
   * Present ONLY on drivable road specs (kind 'road' / 'motorway'). `roadTier`
   * orders the ladder (1 Lane → 5 Motorway); `capacity` is vehicles/tick.
   * ⚠ PLACEHOLDER-balance (Aaron's sign-off pending) — directional only.
   */
  roadTier?: 1 | 2 | 3 | 4 | 5;
  capacity?: number;
  /**
   * FEAT-1972079906 inc1 — refuse COLLECTION capacity, tonnes/tick a single depot
   * can collect on its rounds. Present ONLY on collection depots (waste_depot).
   * Drives collectionCoverageOf() the same way `served` drives water coverage.
   * ⚠ PLACEHOLDER-balance (Aaron's sign-off pending) — directional only.
   */
  wasteCapacity?: number;
  /**
   * FEAT-1972079906 inc2 — refuse PROCESSING throughput, tonnes/tick a single
   * processor (landfill / EfW / MRF / compost) can take of the COLLECTED tonnage.
   * Present ONLY on processing specs (waste_landfill / waste_incinerator /
   * waste_recycling / waste_compost). Drives processingMixOf() the same way
   * `wasteCapacity` drives collection coverage.
   * ⚠ PLACEHOLDER-balance (Aaron's sign-off pending) — directional only.
   */
  processCapacity?: number;
  /**
   * FEAT-1972079878 inc1 — auto-scale capacity tiers. Array of capacity values
   * for each tier (0-indexed), e.g. [1500, 1650, 1815, ...] for ~10% per tier.
   * Present ONLY on specs with scalable residents or jobs (estates, offices, farms).
   * If a building reaches tier N, its effective capacity = capacityTiers[N] (or
   * original if tier > array length — capped). Supports tier-based delta-cost model.
   * ⚠ PLACEHOLDER-balance — values are directional pending Aaron's pass.
   */
  capacityTiers?: number[];
  /**
   * FEAT-1972079877 placeholder catalogue: true marks a planned-but-unbuilt type
   * shown GREYED-OUT / "coming soon" in the build catalogue as a roadmap preview.
   * A placeholder is NEVER placeable (see isPlaceable) and carries zero sim stats
   * (cost/upkeep 0, no served/jobs/mw/residents), so it can never enter the running
   * sim. Real specs never set this flag.
   */
  placeholder?: boolean;
  /**
   * FEAT-2326609761 AC-28 — unique-building HARD CAP (Aaron: "limit the number
   * of Five Gorges Dams to just one"). When set, at most `maxPerCity` buildings
   * of this spec may exist in `s.buildings` at once, counting under-construction
   * instances too (so two cannot be started in the same tick). `undefined`/
   * absent means unlimited — the overwhelming majority of specs. Derived-from-
   * state enforcement lives in `remainingAllowance()`; this field itself is the
   * ONLY thing a unique building needs (GR#15 — no future `if (spec === 'x')`).
   */
  maxPerCity?: number;
}

/** Real-world reference sizes (metres). Tile grid = 50 m. */
const DIMS: Record<string, Dims> = {
  road: { x: 50, y: 50, z: 0 },
  m20: { x: 50, y: 60, z: 0 },
  rail: { x: 50, y: 50, z: 0 },
  station_sanderling: { x: 50, y: 50, z: 14 },
  pylon: { x: 30, y: 30, z: 50 },
  res_hut: { x: 12, y: 12, z: 8 },
  res_block: { x: 90, y: 90, z: 18 },
  com_shop: { x: 16, y: 16, z: 7 },
  com_retail: { x: 150, y: 95, z: 12 },
  ind_farm: { x: 95, y: 95, z: 12 },
  ind_factory: { x: 90, y: 90, z: 16 },
  park: { x: 50, y: 50, z: 15 },
  pow_wind: { x: 10, y: 10, z: 150 },
  pow_coal: { x: 280, y: 280, z: 200 },
  // BUG-648 (2026-09-03): footprint-shrink draft REVERTED after an independent
  // round proved it silently road-disconnects an already-placed nuke (see the
  // SPECS entry below) — 13x13 kept, DIMS unchanged.
  pow_nuke: { x: 630, y: 630, z: 60 },
  // FEAT-1972079901 — Five Gorges Dam: 8×8 tiles = 400 m span, ~181 m crest height.
  pow_hydro: { x: 400, y: 400, z: 181 },
  wat_clean: { x: 85, y: 85, z: 12 },
  wat_waste: { x: 85, y: 85, z: 12 },
  hea_clinic: { x: 40, y: 25, z: 11 },
  hea_hospital: { x: 120, y: 90, z: 28 },
  pol_station: { x: 90, y: 45, z: 12 },
  edu_nursery: { x: 25, y: 20, z: 6 },
  edu_primary: { x: 95, y: 95, z: 11 },
  edu_city: { x: 145, y: 90, z: 14 },
  col_sixth: { x: 95, y: 95, z: 18 },
  uni: { x: 145, y: 145, z: 32 },
  off_suite: { x: 20, y: 20, z: 25 },
  off_tower: { x: 48, y: 48, z: 120 },
  mine_quarry: { x: 95, y: 95, z: -25 },
  mine_deep: { x: 145, y: 145, z: -60 },
  land_stadium: { x: 190, y: 140, z: 42 },
  land_airport: { x: 3500, y: 3500, z: 65 },
  land_harbour: { x: 650, y: 650, z: 15 },
};

/** Ambient physical entities — a person occupies a 1 m² footprint, 2 m tall. */
export const PHYSICAL_ENTITIES = [
  { id: 'person', label: 'Citizen', x: 1, y: 1, z: 2 },
  { id: 'car', label: 'Car', x: 4.5, y: 1.8, z: 1.5 },
  { id: 'hgv', label: 'HGV lorry', x: 16.5, y: 2.55, z: 4 },
  { id: 'bus', label: 'Bus', x: 12, y: 2.55, z: 3.5 },
  { id: 'train', label: 'Train carriage', x: 23, y: 2.7, z: 4 },
];

/** Abstraction / discharge pipe tiers: capacity multiplier and upgrade cost. */
export const PIPE_TIERS = [
  { label: 'Ø300 mm', mult: 1, upgradeCost: 0 },
  { label: 'Ø500 mm', mult: 1.8, upgradeCost: 8000 },
  { label: 'Ø800 mm', mult: 2.6, upgradeCost: 16000 },
];

// COLD AUDIT (BUG-657 class, GR#16 type-safe storage boundary): the reducer's
// 'pipeUpgrade' case never lets a tier exceed PIPE_TIERS.length - 1, but this
// is ALSO the read path for whatever a save file hands back, and the
// `Record<number, number>` annotation on SimState.pipeTier is a compile-time
// promise only — a hand-edited or corrupted save can carry any
// JSON-representable value (a negative tier, a stray 99, a non-integer).
// Consumers index PIPE_TIERS[pipeTierOf(...)].mult directly, so an
// out-of-range tier would read `.mult` off `undefined` and throw. Clamped here
// at the single read SSOT, exactly as sanitizeCrimeRate and
// sanitizeCongestionTicksBySpec clamp their own corrupt-save inputs below.
export function pipeTierOf(s: SimState, id: number): number {
  const raw = s.pipeTier[id] ?? 0;
  if (!Number.isFinite(raw)) return 0;
  return Math.max(0, Math.min(PIPE_TIERS.length - 1, Math.floor(raw)));
}

/**
 * Power line / infrastructure classes for the power overlay.
 * Each class represents a different tier of power infrastructure.
 *
 * FORWARD-DECLARATION HONESTY (FEAT-1972079851):
 * - localGrid: real, placeable today (HV Pylon structures)
 * - superGrid: declared but not yet built (feature FEAT-1972079849)
 * - hvdc: declared but not yet built (feature FEAT-1972079850)
 *
 * The overlay renders ONLY power infrastructure that exists in state.
 * If only local-grid pylons are placed, only local-grid colour appears;
 * super-grid and HVDC colours only render when those features ship and
 * buildings of those kinds exist on the map.
 */
export interface PowerLineClass {
  id: 'localGrid' | 'superGrid' | 'hvdc';
  label: string;
  /** PLACEHOLDER: colour awaits palette curation. */
  color: string;
}

export const POWER_LINES: PowerLineClass[] = [
  {
    id: 'localGrid',
    label: 'Local Grid',
    color: '#9aa4ae', // PLACEHOLDER: matches pylon colour for now
  },
  {
    id: 'superGrid',
    label: 'Super Grid',
    color: '#e3b341', // PLACEHOLDER: bold amber for high-capacity trunk
  },
  {
    id: 'hvdc',
    label: 'HVDC Interconnector',
    color: '#ff7b72', // PLACEHOLDER: red for long-distance DC link
  },
];

/**
 * BUG-452 inc1 (2026-09-01): the cost-per-tick divisor for constructionTicks(),
 * bumped by the SAME ~1000x the catalogue's 'zones'/'services' categories were
 * rescaled by (see CIVIC_SCALE_FACTOR/ZONES_SCALE_FACTOR below), so build-time
 * pacing stays roughly where it was pre-rebase instead of exploding into
 * millennia once costs read in the hundreds of millions (a straight £-per-1500
 * divisor against a £1.12bn nuclear plant would be ~750,000 ticks — over 2,000
 * game-years). Named + derived so a future catalogue retune can reason about
 * it explicitly rather than rediscovering the coupling.
 */
const CONSTRUCTION_COST_DIVISOR = 1_500_000;

export function constructionTicks(sp: Spec): number {
  const base = Math.max(3, Math.round(sp.cost / CONSTRUCTION_COST_DIVISOR));
  // FEAT-159: DEBUG-ONLY per-class fast-build override. When the debug flag is
  // OFF (the default) scaleConstructionTicks returns `base` byte-for-byte, so
  // normal play and replay are unchanged. When ON it scales the lead-time down
  // by the building's ZoneKind factor (floored at 1 tick) so a developer can
  // watch the city evolve fast. See debugBuildSpeed.ts.
  return scaleConstructionTicks(base, sp.kind);
}

/**
 * Money actually charged to PLACE a spec (FEAT-1972079882).
 * Zoning is free: any 'zones'-category structure (residential / commercial /
 * farm / industrial / park / office / mining zones) costs £0 to place. Network
 * and service structures keep their catalogue cost.
 *
 * NOTE: this deliberately does NOT touch `sp.cost`, so build TIME
 * (constructionTicks, derived from sp.cost) is unchanged and still shown, and
 * demand/refund maths keep a sensible nominal value to work from.
 */
export function placementCost(sp: Spec): number {
  return sp.category === 'zones' ? 0 : sp.cost;
}

/** True when placing this spec is free (a zone). */
export function isFreeZone(sp: Spec): boolean {
  return sp.category === 'zones';
}

/**
 * FEAT-2326609782 GENESIS FREE (2026-09-04, Aaron's ruling on the £3.3M/month
 * genesis-upkeep STOP question): the pre-existing national m20/rail map
 * furniture seeded by starterCity() (engine.ts) — the trunk motorway rows and
 * the rail row, none of which ever set `builtTick`, so `b.builtTick` reads
 * `undefined` for every one of them — pays NO upkeep. The player never built
 * it and never chose to take on its running cost. Any m20/rail tile the
 * PLAYER or autoConnect lays (builtTick > 0, since tick 0 is genesis's own
 * advance() and 'place'/autoConnect always stamp the CURRENT tick at
 * placement) pays full upkeep exactly like before this predicate existed.
 *
 * `builtTick<=0` (not strictly `=== undefined`) so a hand-built test fixture
 * or a hypothetical FUTURE genesis addition that explicitly stamps `0` is
 * ALSO treated as map furniture — the ruling's own wording ("tiles with
 * builtTick<=0") is the general rule, not a spec-detail of how starterCity()
 * happens to omit the field today.
 *
 * Deliberately scoped to exactly the two specs the ruling names (m20, rail) —
 * this is NOT a general "map furniture" concept (the codebase has none yet).
 * Every other genesis spec (road/pylon/station_sanderling/hs1) already had
 * £0 upkeep before this ruling and is untouched by it.
 *
 * Build COST is completely unaffected by this predicate: placementCost() is
 * only ever charged at 'place' action time, and genesis tiles never go
 * through 'place' — they are pre-seeded directly into starterCity()'s
 * buildings array. This function governs upkeep ONLY.
 *
 * SSOT (GR#3): the ONE seam both engine.computeFlows and consistency.ts's
 * upkeep recompute read through (mirroring the existing UPKEEP_BUCKET
 * pattern one function up) — a duplicated inline `builtTick<=0` check in two
 * places would risk exactly the BUG-628-class drift this codebase has
 * already been bitten by once (raw vs policy-adjusted upkeep readouts).
 */
const GENESIS_FREE_UPKEEP_SPECS = new Set(['m20', 'rail']);
export function upkeepChargeableOf(b: Building, sp: Spec): number {
  if (GENESIS_FREE_UPKEEP_SPECS.has(sp.id) && (b.builtTick ?? 0) <= 0) return 0;
  return sp.upkeep;
}

/**
 * FEAT-2326609761 (CONSOLIDATOR, Aaron's R2 ruling + Q100102): the player's
 * own bulldozer refunds 25% of what was actually paid (engine.ts's `bulldoze`
 * case, `Math.round(placementCost(def) * BULLDOZE_REFUND_FRACTION)`) — this
 * is the SSOT constant that fraction now derives from (was three separate
 * `0.25` literals in engine.ts, a GR#15 violation). Deliberately homed in
 * data.ts, NOT engine.ts: engine.ts's mutation-lane consolidator pass needs
 * to import the CONSOLIDATOR_SCRAP_FRACTION derived from this below, and
 * consolidator.ts (the read-only discovery module) is a LEAF that must never
 * import engine.ts (doing so would create an engine.ts <-> consolidator.ts
 * circular import once engine.ts imports consolidator.ts's pure functions —
 * see consolidator.ts's own header note on the same cycle). data.ts is the
 * one shared ancestor both already depend on.
 */
export const BULLDOZE_REFUND_FRACTION = 0.25;

/**
 * FEAT-2326609761 R2 (Aaron, 2026-09-03): "50 percent, deliberately DOUBLE
 * the player's own 25 percent bulldozer refund — the game rewards
 * consolidating rather than merely permitting it." GR#15: derived from
 * BULLDOZE_REFUND_FRACTION, never a second hand-typed 0.5. Q100102 in the BA
 * doc recommended keeping these as separate constants (never unify them,
 * that would silently double every hand-demolition refund in the game) —
 * this constant IS that separate constant, just expressed as a multiple of
 * the bulldozer's own rate so the "double" relationship is provable in code,
 * not merely asserted in a comment.
 */
export const CONSOLIDATOR_SCRAP_FRACTION = 2 * BULLDOZE_REFUND_FRACTION;

/**
 * Density / level tier of a block (FEAT-1972079882), 1..3, drawn as the block's
 * border colour. Deterministic from the spec's footprint + capacity — there is
 * no per-building level in sim state yet, so tier is a stable property of the
 * structure type (a bigger, higher-capacity building = a denser tier).
 *   tier 1 = low density (grey), 2 = medium (blue), 3 = high (gold)
 */
export function densityTier(sp: Spec): 1 | 2 | 3 {
  const area = sp.w * sp.h;
  const cap = sp.residents ?? sp.jobs ?? sp.children ?? 0;
  const score = area + cap / 20;
  if (score >= 12) return 3;
  if (score >= 4) return 2;
  return 1;
}

// ════════════════════════════════════════════════════════════════════════════
// FEAT-1972079907 inc1 — ROAD TIER LADDER + FITTING RULE.
// ⚠ BALANCE-NUMBER REGIME (Aaron's blanket rule): every capacity below and every
// fittingTier threshold is a PLACEHOLDER — directional only, pending Aaron's
// row-by-row balance pass. Do not tune gameplay against these numbers.
// ════════════════════════════════════════════════════════════════════════════

export type RoadTier = 1 | 2 | 3 | 4 | 5;

/** Spec id laid for each tier (auto-connect connector + upgrade target). */
export const ROAD_TIER_SPECS: Record<RoadTier, string> = {
  1: 'road', // Lane
  2: 'rd_avenue', // Avenue
  3: 'rd_aroad', // A-Road
  4: 'rd_dual', // Dual Carriageway
  5: 'm20', // Motorway
};

/** Per-tile vehicle capacity (vehicles/tick) by tier. PLACEHOLDER-balance. */
export const ROAD_TIER_CAPACITY: Record<RoadTier, number> = {
  1: 100,
  2: 250,
  3: 500,
  4: 1000,
  5: 2500,
};

/**
 * FEAT-1972079910 inc3 (AC-7): Cost multiplier for rail bridge (grade-separated
 * crossing where a dual+ road crosses a rail tile). Bridge cost = road cost × this.
 * PLACEHOLDER-balance (Aaron's balance pass pending).
 */
export const RAIL_BRIDGE_COST_MULTIPLIER = 4;

/**
 * FEAT-1972079910 inc3 (AC-8): Flat cost for motorway junction (where a road
 * crosses the highest-tier motorway-class road). BUG-452 inc1 (2026-09-01):
 * rescaled as a "flat premium" over the rebased rd_dual tier (~5x) rather
 * than the road catalogue's general 300x tile-scale multiplier — a literal
 * 300x on the old £250,000 would land near £75M, which would swallow the
 * ENTIRE STARTING_TREASURY (fiscal.ts) on a single junction and break every
 * existing reducer test that converts a junction at genesis funds (e.g.
 * road-path-action.test.mjs's AC-8b). Still a genuine premium over a plain
 * tier-4 dual-carriageway tile — PLACEHOLDER-balance, Aaron's pass pending.
 */
export const MOTORWAY_JUNCTION_COST = 480000;

/**
 * Road tier of a spec, or 0 when it is not a drivable road. Reads `roadTier`
 * (set on the road specs); non-road specs return 0.
 */
export function roadTierOf(sp: Spec | undefined): number {
  return sp?.roadTier ?? 0;
}

/** True when this spec is a drivable road tile (participates in the network). */
export function isRoadSpec(sp: Spec | undefined): boolean {
  return roadTierOf(sp) > 0;
}

/**
 * True when this spec participates in the ROAD-CONNECTIVITY graph at all —
 * either as a drivable road tile or as one of the trunk seed kinds
 * (motorway/rail/station) computeRoadConnectivity treats as always-connected
 * seeds (data.ts computeRoadConnectivity, ~line 546). Placing/removing a
 * building of any OTHER kind can never change the connectivity graph, which
 * is the basis for BUG b2d31bc7 FIX 2's reducer-wrapper recompute gate
 * (engine.ts reducer()).
 */
export function isRoadOrTrunkSpec(sp: Spec | undefined): boolean {
  if (!sp) return false;
  return isRoadSpec(sp) || sp.kind === 'motorway' || sp.kind === 'rail' || sp.kind === 'station';
}

/**
 * FEAT-1972079878 inc1 (AC-5): get the capacity at a given tier for a spec.
 * If the spec has capacityTiers and tier < array length, returns capacityTiers[tier].
 * If tier >= array length, caps at the last tier. Supports auto-scale tier progression:
 * tier 0 = original placement, tier N = after N scale events.
 */
export function capacityAtTier(sp: Spec | undefined, tier: number): number {
  if (!sp) return 0;
  // BUG-448 AC-3: clamp non-finite/undefined tiers to 0 to prevent NaN indexing
  if (!Number.isFinite(tier)) tier = 0;
  const tiers = sp.capacityTiers;
  if (tiers && tiers.length > 0) {
    if (tier >= tiers.length) {
      return tiers[tiers.length - 1]; // Cap at last tier
    }
    return tiers[Math.max(0, tier)]; // Clamp to 0 if negative
  }
  // No capacityTiers defined, fallback to base capacity (residents or jobs).
  // BUG-511: for a 'residential' spec this can only fall through to `?? 0`
  // if `residents` is missing, which assertResidentialSpecsHaveResidents()
  // (above, run at catalogue-load time against SPECS) makes impossible to
  // ship silently -- a spec missing it throws MET-V852 on import instead.
  return sp.residents ?? sp.jobs ?? 0;
}

/**
 * BUG-742 round F3 (GR#16 safeX idiom, GR#3 SSOT): the ONE shared
 * storage-boundary coercion for a building's `capacityTier`, used by EVERY
 * reader that turns persisted bytes into building data — gamesave.ts's
 * File-Open/named-save validation AND replay.ts's/store.tsx's savepoint
 * decode paths (readSlot, restampSavepointsBuildVersion,
 * migrateLegacySavepointsInPlace, decodeSavepointRaw). The round's F3 finding:
 * the coercion originally sat ONLY on gamesave.ts's validateBuildingElement,
 * so the DEFAULT boot path (autosave/savepoint restore, which never goes
 * through gamesave.ts at all) admitted a poisoned tier (e.g. 8.7) verbatim,
 * turning citywide capacity aggregates to NaN and never self-healing.
 *
 * Clamps a `typeof number` capacityTier to a safe integer in
 * `[0, ladderLength - 1]` (ladderLength 0 -- no capacityTiers ladder
 * registered for this spec, or the spec is unknown -- clamps to 0, the only
 * defined tier). A wrong-TYPE value (string/bool/etc) or `undefined` is left
 * untouched -- rejecting an entirely wrong type is a structural-validation
 * concern for the caller (gamesave.ts's rejectSave), not this coercion's job.
 *
 * NEVER mutates `b` -- returns the SAME reference when nothing needs to
 * change, or a NEW shallow-cloned object when it does (round finding E1): a
 * caller may hand in a building object that is ALSO referenced by its own
 * live SimState (buildGameSave does not deep-clone state.buildings), so
 * mutating in place would corrupt the live game state as a side effect of
 * merely validating/loading a save built FROM it.
 */
export function coerceBuildingCapacityTier(b: Record<string, unknown>, index: number): Record<string, unknown> {
  if (b.capacityTier === undefined || typeof b.capacityTier !== 'number') return b;
  const raw = b.capacityTier;
  const sp = typeof b.spec === 'string' ? SPECS[b.spec] : undefined;
  const maxTier = sp?.capacityTiers && sp.capacityTiers.length > 0 ? sp.capacityTiers.length - 1 : 0;
  let coerced = Number.isFinite(raw) ? Math.trunc(raw) : 0;
  if (coerced < 0) coerced = 0;
  if (coerced > maxTier) coerced = maxTier;
  // NaN !== NaN, so a non-finite raw always reports here too.
  if (coerced === raw) return b;
  recordError(
    `Building[${index}]${typeof b.spec === 'string' ? ` (${b.spec})` : ''} had a fractional/non-finite/out-of-range capacityTier (${String(raw)}) -- coerced to ${coerced}`,
    { type: 'app', action: 'storageBoundary', code: 'MET-V865' },
  );
  return { ...b, capacityTier: coerced };
}

/**
 * Coerce every building in a decoded snapshot's `buildings` array via
 * coerceBuildingCapacityTier -- the shared entry point every storage reader
 * (gamesave.ts, replay.ts, store.tsx) calls on its OWN freshly-decoded copy
 * (round F3: "operate on the decoded copy, never the caller's live object
 * graph"). NEVER mutates `buildings` -- returns the SAME array reference
 * when nothing changed (so a caller doing `snap.buildings =
 * coerceSnapshotBuildings(snap.buildings)` never manufactures needless
 * churn for an already-clean save/savepoint), or a brand NEW array when at
 * least one element needed coercion. A non-object array element (garbage
 * that a structural validator elsewhere will reject) is passed through
 * untouched -- this function's only job is the ONE known-shape field.
 */
export function coerceSnapshotBuildings<T>(buildings: readonly T[]): readonly T[] {
  let changed = false;
  const out = buildings.map((b, i) => {
    if (!b || typeof b !== 'object' || Array.isArray(b)) return b;
    const coerced = coerceBuildingCapacityTier(b as Record<string, unknown>, i) as unknown as T;
    if (coerced !== b) changed = true;
    return coerced;
  });
  return changed ? out : buildings;
}

/**
 * BUG-662 — tier-aware read of a school building's `children` capacity.
 * capacityAtTier()'s no-capacityTiers fallback only ever special-cases
 * residents/jobs (see the comment immediately above) — it has never covered
 * children or served. A school-kind spec WITH its own capacityTiers ladder
 * (edu_nursery/edu_primary/edu_city/edu_tech) is correctly scaled by
 * capacityAtTier(); one WITHOUT a ladder (col_sixth/uni — the "College"
 * class this bug is named for) would silently read capacityAtTier()'s bare
 * `residents ?? jobs ?? 0` fallback instead of its own `children` field,
 * undercounting it to 0 (uni: to its `jobs` field, an unrelated number).
 * Every children-capacity aggregate in this file (totalChildrenCapacity,
 * serviceCapacityAggregates) MUST go through this helper, never a bare
 * capacityAtTier(sp, tier) or sp.children read alone, so a scaled-up
 * capacityTiers spec is counted at its CURRENT tier while a flat spec keeps
 * reading its own field.
 */
function childrenAtTier(sp: Spec, tier: number): number {
  return sp.capacityTiers && sp.capacityTiers.length > 0 ? capacityAtTier(sp, tier) : (sp.children ?? 0);
}

/**
 * BUG-662-class — the same tier-aware guard as childrenAtTier() above, for
 * the `served` capacity field (health/police specs). Every current
 * served-bearing spec (hea_clinic/hea_hospital/hea_teaching/pol_station)
 * happens to carry its own capacityTiers ladder today, so a bare
 * capacityAtTier(sp, tier) call has been silently correct for them — but
 * that is a data-shape coincidence, not a guarantee (fire_post/fire_station/
 * fire_hq are served-bearing specs with NO ladder, and would fall through
 * capacityAtTier's bare `residents ?? jobs ?? 0` to 0 if ever routed through
 * it unguarded). Routing every served-capacity aggregate through this helper
 * closes that latent gap the same way childrenAtTier() closes it for schools.
 */
function servedAtTier(sp: Spec, tier: number): number {
  return sp.capacityTiers && sp.capacityTiers.length > 0 ? capacityAtTier(sp, tier) : (sp.served ?? 0);
}

/**
 * BUG-685/686 (Aaron ruling, 2026-09-04, "wind turbines can not produce the
 * power thats needed and 20,000 kindergartens is nuts") — the capacity
 * credited to a FRESH placement of `sp` for the demand-fix PICKER
 * (largestFirstFill(), below), as distinct from childrenAtTier()/
 * servedAtTier() above (BUG-662), which correctly read an EXISTING
 * building's CURRENT capacityTier. A brand-new unit is placed at tier 0 and
 * only grows toward its ladder's top tier over its lifetime, one step at a
 * time, via evaluateBuildingMonitors' auto-scale pass (engine.ts) — pricing
 * every future placement off tier-0 forever is exactly the toy-monoculture
 * defect BUG-686 named (edu_nursery: 30 children at tier 0 vs a ladder built
 * to reach a real early-years-campus scale, so a 253k shortfall priced off
 * 30 proposed 12,654 buildings).
 *
 * Credited capacity = LADDER_CREDIT_FRACTION of the ladder's TOP tier — a
 * documented, derived (never hand-picked-per-spec) mid-lifetime estimate:
 * never the untouched tier-0 base (undercounts every laddered spec's real
 * eventual capacity, the BUG-686 defect), and never the fully-matured top
 * tier either (assumes every fresh placement instantly auto-scales all the
 * way, which the per-tier BUILDING_AUTO_SCALE_COST_FRACTION charge means it
 * has not yet paid for). ⚠ PLACEHOLDER-balance (Balance Number Regime) — the
 * 50% figure is directional, pending Aaron's row-by-row pass; it is applied
 * as ONE constant across every laddered spec (GR#3 SSOT), never re-derived
 * per call site. Specs with no capacityTiers ladder (col_sixth/uni-class,
 * waste_depot) fall straight through to `base`, unchanged — same fallback
 * shape childrenAtTier()/servedAtTier() already use.
 */
export const LADDER_CREDIT_FRACTION = 0.5;

export function creditedUnitCapacity(sp: Spec, base: number): number {
  const tiers = sp.capacityTiers;
  if (!tiers || tiers.length === 0) return base;
  const top = tiers[tiers.length - 1];
  return Math.max(base, Math.round(top * LADDER_CREDIT_FRACTION));
}

/**
 * FEAT-1972079910 inc3 (AC-7): True when this spec is a rail tile that carries
 * train traffic. Includes both native rail lines ('rail', 'hs1') and rd_railbridge
 * (grade-separated crossing: rail runs through it). Used to identify all tiles
 * that participate in rail connectivity and train routing (trains.ts, railConnect.ts).
 */
export function isRailSpec(sp: Spec | undefined): boolean {
  return sp?.kind === 'rail' || sp?.id === 'rd_railbridge';
}

/**
 * FEAT-1972079910 inc3 (AC-8): True when this spec is a motorway-class road
 * (the highest tier). Identifies specs whose roadTier equals the maximum tier.
 * Used to detect motorway junctions in placeRoadPath. Currently only m20 is tier 5.
 */
export function isMotorwayClassSpec(sp: Spec | undefined): boolean {
  return (sp?.roadTier ?? 0) === 5;
}

/**
 * FITTING RULE (FEAT-1972079907 inc1): the MINIMUM road tier a building's
 * connector should be, from its footprint (w×h), kind and throughput
 * (jobs / residents / served / children / mw). Small footprint & low traffic →
 * tier 1; large footprint, industry, mega-facility → higher. Pure + deterministic.
 * ⚠ PLACEHOLDER-balance thresholds — Aaron's sign-off pending.
 */
export function fittingTier(sp: Spec): RoadTier {
  const area = sp.w * sp.h;
  // Throughput proxy — the largest traffic-driving stat the building carries.
  const cap = Math.max(
    sp.jobs ?? 0,
    sp.residents ?? 0,
    Math.round((sp.served ?? 0) / 1000),
    sp.children ? Math.round(sp.children / 20) : 0,
    sp.mw ?? 0
  );
  // Industry / mining generate freight → need heavier roads.
  const heavy = sp.kind === 'industrial' || sp.kind === 'mine';
  let score = area + cap * 0.05 + (heavy ? 6 : 0);
  // Landmarks (airport, stadium, spaceport) are city arterials.
  if (sp.kind === 'landmark') score += 8;
  let tier: RoadTier;
  if (score >= 24) tier = 5;
  else if (score >= 14) tier = 4;
  else if (score >= 7) tier = 3;
  else if (score >= 3) tier = 2;
  else tier = 1;
  // Aaron ruling 2026-09-04 ("when i lay housing down why is an auto
  // motorway laid down to support it?", filed P1): a dense residential
  // tower's `residents` count feeds the SAME `cap` throughput proxy as a
  // factory's jobs/freight, so a 400+ resident tower crossed the >=24
  // tier-5 (m20 motorway) threshold on `residents` alone. ⚠ PLACEHOLDER-
  // balance regime (directional only, pending Aaron's row-by-row pass):
  // residential never needs more than an A-road (tier 3) frontage,
  // whatever its density — clamp AFTER the score ladder above so every
  // other kind (industrial/landmark/etc.) is completely untouched.
  if (sp.kind === 'residential') tier = Math.min(tier, 3) as RoadTier;
  return tier;
}

/** Border colours for the three density tiers (documented in the map legend). */
export const TIER_COLORS: Record<1 | 2 | 3, string> = {
  1: '#9099a6', // grey  — low density
  2: '#4c9aff', // blue  — medium density
  3: '#e3b341', // gold  — high density
};

/**
 * Per-block occupancy fraction 0..1 for fill shading (FEAT-1972079882), or null
 * when the block should render fully filled (services / network / parks).
 *
 * PLACEHOLDER: true per-building occupancy is not tracked in sim state, so this
 * derives a stable city-wide estimate and applies it to every block of the kind:
 *   residential            -> population / residential capacity
 *   commercial/office/     -> workers (population*0.55) / total jobs
 *     industrial/mine
 * Both are clamped to 0..1. Same-kind blocks therefore share one occupancy —
 * a reasonable directional placeholder until per-building tenancy exists.
 */
export function blockOccupancy(s: SimState, b: SimState['buildings'][number]): number | null {
  const sp = SPECS[b.spec];
  if (!sp) return null;
  const frac = (have: number, cap: number) =>
    cap > 0 ? Math.max(0, Math.min(1, have / cap)) : 0;
  switch (sp.kind) {
    case 'residential':
      return frac(s.population, residentsCapacity(s));
    case 'commercial':
    case 'office':
    case 'industrial':
    case 'mine':
      return frac(s.population * 0.55, totalJobs(s));
    default:
      return null;
  }
}

/**
 * Names of specs whose unlock level is EXACTLY `level` (FEAT-1972079884), used to
 * tell the player what a level-up just made available. The 99 sentinel (always
 * placeable seed infrastructure) is excluded.
 */
export function unlockedAtLevel(level: number): string[] {
  const names: string[] = [];
  for (const sp of Object.values(SPECS)) {
    // BUG-390: exclude 'network' category items to match XpTab (which hides them
    // from the unlock ladder). Keeps station_ashford etc. out of level-up notices.
    // FEAT-1972079877: exclude placeholders — a "coming soon" type is not actually
    // made available by a level-up, so announcing it as unlocked would mislead.
    if (sp.unlock === level && sp.unlock !== 99 && sp.category !== 'network' && !sp.placeholder) {
      names.push(sp.name);
    }
  }
  return names;
}

// BUG-642 (part 2) — isOnline() is invoked from roughly a dozen separate
// O(buildings) passes per tick (computeFlows' waste family, powerStats,
// serviceCoverageOf, buildingDisplayStates, demandFixPlan, ...), and every
// call re-walked the building's footprint with string-keyed road lookups.
// CPU profile on Aaron's 29,831-building city: isOnline + isRoadAdjacent +
// isRoadConnected = 64% of ALL per-tick CPU. This memo evaluates the gates
// ONCE per state object for every building in s.buildings and answers each
// subsequent query with a Map lookup. Keyed on the building OBJECT (not its
// id) so a caller passing a building that is not in s.buildings (a placement
// candidate, a ghost) falls through to the direct computation below —
// semantics are byte-identical to the unmemoised function for every input.
//
// BUG-674 — the BUG-642 memo above was keyed on the WHOLE state object `s`
// (memoOnState's usual WeakMap<SimState, T> idiom), which is safe but too
// coarse here: `s` is a brand-new top-level object EVERY tick/commit (the
// codebase's immutable-replace discipline), even on a glide day whose only
// change is funds, citizens, or a notice — none of which computeIsOnline
// reads. So the O(buildings) fold re-ran on every single commit regardless
// of relevance ("whole-engine blast radius" per the item). FIX: split the
// two gates by their ACTUAL read-set instead of hand-guessing one composite
// key for the whole function (the shape the 2026-09-02 independent round
// REJECTED for serviceCapacityAggregates — see memoOnState's own doc comment
// above — because a hand-picked field list silently missed pipeTier):
//
//   G1 (construction, below) reads ONLY `s.tick` and `b.builtTick` — an O(1)
//   subtraction, cheap enough to evaluate FRESH on every isOnline() call
//   rather than fold into any memo, so it can never go stale and never needs
//   to appear in an invalidation key.
//
//   G2/G3 (road gates, roadGateMapOf below) go through isRoadAdjacent/
//   isRoadConnected, which are THEMSELVES memoised (roadTileSetOf keyed on
//   s.buildings, connectedRoadTileSet keyed on s.roadConnectivity — see their
//   own doc comments a few hundred lines up) and, by direct inspection, read
//   NOTHING else: no pipeTier, no funds, no citizens. So caching the road-
//   gate fold on the pair (s.buildings identity, s.roadConnectivity identity)
//   is exact, not a guess — it is provably the complete read-set of the two
//   functions being folded, the same standard the rejected serviceCapacity
//   attempt failed to meet. A change to funds/citizens/notices/pipeTier
//   leaves both identities untouched -> cache hit -> zero-cost. A change to
//   buildings (place/demolish/grow) or a roadConnectivity recompute (which
//   itself only fires when buildings changed — computeRoadConnectivity is
//   ALSO keyed on s.buildings) correctly invalidates.
const roadGateCache = new WeakMap<SimState['buildings'], WeakMap<object, Map<object, boolean>>>();
// Stable stand-in key for the "connectivity not yet computed" bespoke/legacy
// case (WeakMap keys must be objects) — safe because computeRoadGates' answer
// when s.roadConnectivity is absent never depends on which absent state it
// was (the gate is unconditionally skipped), only on s.buildings.
const NO_ROAD_CONNECTIVITY: object = {};

// TEST INSTRUMENTATION (BUG-674): incremented once per ACTUAL O(buildings)
// fold — i.e. once per distinct (buildings, roadConnectivity) pair ever seen,
// never on a cache hit. O(1) overhead (one counter bump per fold, not per
// building), always on — this is how the attack round proves a claimed
// "unrelated change never recomputes" without timing noise. See
// bug-674-online-memo.test.mjs.
export let __roadGateFoldCount = 0;
export function __resetRoadGateFoldCountForTest(): void {
  __roadGateFoldCount = 0;
}

function roadGateMapOf(s: SimState): Map<object, boolean> {
  let byConnectivity = roadGateCache.get(s.buildings);
  if (!byConnectivity) {
    byConnectivity = new WeakMap<object, Map<object, boolean>>();
    roadGateCache.set(s.buildings, byConnectivity);
  }
  const connKey: object = s.roadConnectivity ?? NO_ROAD_CONNECTIVITY;
  let map = byConnectivity.get(connKey);
  if (!map) {
    __roadGateFoldCount++;
    map = new Map<object, boolean>();
    for (const b of s.buildings) map.set(b, computeRoadGates(s, b));
    byConnectivity.set(connKey, map);
  }
  return map;
}

export function isOnline(s: SimState, b: SimState['buildings'][number]): boolean {
  if (b.builtTick == null) return true;
  const sp = SPECS[b.spec];
  // G1 (construction time) — evaluated fresh every call (see BUG-674 comment
  // above): O(1), and its only inputs (s.tick, b.builtTick) are exactly the
  // two things the road-gate cache below deliberately does NOT key on.
  if (s.tick - b.builtTick < constructionTicks(sp)) return false;
  const known = roadGateMapOf(s).get(b);
  return known === undefined ? computeRoadGates(s, b) : known;
}

function computeRoadGates(s: SimState, b: SimState['buildings'][number]): boolean {
  const sp = SPECS[b.spec];
  // FEAT-1972079891 inc1 — ROAD ACTIVATION GATES (G2 road-adjacent, G3 road-connected).
  // A non-infrastructure building only operates if it sits beside a road tile that
  // reaches the connected road network (map edges + trunk m20/hs1/rail/stations).
  // Infrastructure (category 'network' — road/motorway/rail/station/pylon) IS the
  // network, so it is exempt. Gates are pure functions of SimState (GR#21): same
  // state → same online set; no Date/Math.random.
  //
  // DD4 (Aaron, 2026-08-28, Option C): ALL buildings re-evaluate activation gates
  // IMMEDIATELY on save-load and at all times. No grace period, no legacy exemption.
  //
  // BACKWARD TOLERANCE: the road gates only apply once `s.roadConnectivity` has
  // been computed (advance() computes it at the START of every tick and the
  // reducer keeps it fresh — AC-12). A bespoke/legacy state that never went
  // through advance()/reducer has no connectivity graph, so the gate is skipped
  // (treated as pass) rather than failing closed on an unknowable network.
  //
  // DEFERRED (pending Aaron): G4 water / G5 power / G6 workers. As specified those
  // gates test the CITY-WIDE serviceCoverageOf().coverage >= 1.0 as a PER-BUILDING
  // gate — which takes the WHOLE city offline the instant coverage dips to 0.99, a
  // mass-blackout cliff. That per-building-vs-global design question is open; inc1
  // ships the road mechanic without the cliff. Do NOT add G4/G5/G6 here.
  if (sp && sp.category !== 'network' && s.roadConnectivity) {
    if (!isRoadAdjacent(s, b)) return false;
    if (!isRoadConnected(s, b)) return false;
  }
  return true;
}

// ════════════════════════════════════════════════════════════════════════════
// FEAT-1972079891 inc1 — ROAD CONNECTIVITY + PER-BUILDING ROAD GATES.
//
// STORAGE/SERIALISATION DECISION (AC-1): a Set<string> is NOT JSON-serialisable,
// so SimState stores `roadConnectivity.connectedRoadTiles` as a SORTED string[]
// (keyed "x,y"). That round-trips cleanly through save/replay/debug.json and
// compares byte-identically under genesis-replay's stableStringify. Use sites
// build a Set on demand via connectedRoadTileSet() (memoised per state object).
// ════════════════════════════════════════════════════════════════════════════


// Per-state memo of the drivable-road tile set, keyed on the buildings array
// reference (immutable per tick), so isOnline's road gates stay ~O(footprint)
// instead of O(buildings) each. Pure: a function of buildings only.
const roadTileSetCache = new WeakMap<object, Set<string>>();
export function roadTileSetOf(s: SimState): Set<string> {
  const cached = roadTileSetCache.get(s.buildings);
  if (cached) return cached;
  const set = new Set<string>();
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp || !isRoadSpec(sp)) continue;
    for (let dx = 0; dx < sp.w; dx++)
      for (let dy = 0; dy < sp.h; dy++) set.add(`${b.x + dx},${b.y + dy}`);
  }
  roadTileSetCache.set(s.buildings, set);
  return set;
}

/**
 * BUG-646 (Aaron, 2026-09-03, cap 250 -> 2000): the 'place' reducer case
 * (engine.ts) computes `roadTileSetOf(placedState)` right after appending
 * ONE building — placedState.buildings is ALWAYS `[...state.buildings,
 * placedBuilding]` there, a plain append, so a full from-scratch rebuild
 * (roadTileSetOf's cache misses every time because the array reference is
 * new) is wasted work at city scale: measured 1,586ms for 250 units at
 * Aaron's real 29,831-building savepoint (~6.3ms/unit) — small next to
 * occupiedSetIncremental()'s sibling win below, but free to take at the same
 * call site. `oldState.buildings` must be a strict prefix of `newBuildings`
 * (true for every 'place'-case caller, which only ever appends) — this is
 * NOT a general-purpose replacement for roadTileSetOf(), only a fast path
 * for the append-in-place shape that reducer case guarantees.
 */
export function roadTileSetIncremental(oldState: SimState, newBuildings: SimState['buildings']): Set<string> {
  const cached = roadTileSetCache.get(newBuildings);
  if (cached) return cached;
  const base = roadTileSetOf(oldState);
  let set = base;
  for (let i = oldState.buildings.length; i < newBuildings.length; i++) {
    const b = newBuildings[i];
    const sp = SPECS[b.spec];
    if (!sp || !isRoadSpec(sp)) continue;
    if (set === base) set = new Set(base); // copy-on-write: only clone once a road tile actually needs adding
    for (let dx = 0; dx < sp.w; dx++)
      for (let dy = 0; dy < sp.h; dy++) set.add(`${b.x + dx},${b.y + dy}`);
  }
  roadTileSetCache.set(newBuildings, set);
  return set;
}

// Per-state memo of the connected-road tile Set, keyed on the roadConnectivity
// object reference (replaced whenever connectivity is recomputed).
const connectedSetCache = new WeakMap<object, Set<string>>();
/** The connected road tiles as a Set (built from the stored sorted string[]). */
export function connectedRoadTileSet(s: SimState): Set<string> {
  const rc = s.roadConnectivity;
  if (!rc) return new Set();
  const cached = connectedSetCache.get(rc);
  if (cached) return cached;
  const set = new Set(rc.connectedRoadTiles);
  connectedSetCache.set(rc, set);
  return set;
}

const ORTHO: readonly [number, number][] = [
  [1, 0],
  [-1, 0],
  [0, 1],
  [0, -1],
];

/**
 * AC-1 — deterministic flood-fill of the connected road network (DD1 Option A).
 * A drivable-road tile is CONNECTED when reachable, via orthogonal road-to-road
 * adjacency, from any SEED: a map-edge road tile, a motorway (m20 trunk) tile, or
 * a road tile orthogonally touching a trunk tile (m20/hs1/rail/station). Returns a
 * SORTED string[] (JSON-safe; see the storage note above). Pure/deterministic —
 * the result is a full reachability set (order-independent) and the output is
 * sorted, so it can never depend on buildings[] iteration order (no map-range-break
 * nondeterminism). No Date/Math.random.
 *
 * BUG-643 — memoised keyed on s.buildings (a WeakMap<object, T>, NOT memoOnState's
 * WeakMap<SimState, T>), the SAME idiom roadTileSetOf() above already uses: this
 * function reads ONLY s.buildings (grepped — no other field), so caching on the
 * buildings array reference is both sufficient and safe, and it is the right key
 * (not `s`) because callers repeatedly build `{ ...s, roadConnectivity: <this> }`
 * from the SAME buildings array — keying on the whole state object would be a
 * permanent cache miss on the very call this exists to speed up. FINDING: today's
 * engine.ts/genesisReplay.ts call sites already gate this behind a
 * `buildings !== previous.buildings` check (the `roadTopologyMayHaveChanged`
 * flag, engine.ts ~3085) before invoking it, so within the current codebase this
 * mainly guards against a FUTURE or indirect duplicate call on an unchanged
 * buildings array (e.g. a debug/consistency pass) rather than removing an
 * observed redundant call today — kept because engine.ts is contended for this
 * commit and the guard is free, safe, and exactly the precedent this file
 * already established.
 */
const roadConnectivityCache = new WeakMap<object, { connectedRoadTiles: string[] }>();
export function computeRoadConnectivity(s: SimState): { connectedRoadTiles: string[] } {
  const cached = roadConnectivityCache.get(s.buildings);
  if (cached) return cached;
  const result = computeRoadConnectivityUncached(s);
  roadConnectivityCache.set(s.buildings, result);
  return result;
}

function computeRoadConnectivityUncached(s: SimState): { connectedRoadTiles: string[] } {
  const roadTiles = new Set<string>();
  const trunkTiles = new Set<string>();
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const road = isRoadSpec(sp);
    const trunk = sp.kind === 'motorway' || sp.kind === 'rail' || sp.kind === 'station';
    if (!road && !trunk) continue;
    for (let dx = 0; dx < sp.w; dx++)
      for (let dy = 0; dy < sp.h; dy++) {
        const k = `${b.x + dx},${b.y + dy}`;
        if (road) roadTiles.add(k);
        if (trunk) trunkTiles.add(k);
      }
  }

  // Seed = road tiles at a map edge, m20 trunk roads, or roads touching a trunk tile.
  const connected = new Set<string>();
  const queue: string[] = [];
  const seed = (k: string) => {
    if (roadTiles.has(k) && !connected.has(k)) {
      connected.add(k);
      queue.push(k);
    }
  };
  for (const k of roadTiles) {
    const c = k.indexOf(',');
    const x = Number(k.slice(0, c));
    const y = Number(k.slice(c + 1));
    const edge = x === 0 || y === 0 || x === MAP_W - 1 || y === MAP_H - 1;
    const trunkRoad = trunkTiles.has(k); // m20 is both a road AND a trunk
    const nearTrunk =
      trunkTiles.has(`${x + 1},${y}`) ||
      trunkTiles.has(`${x - 1},${y}`) ||
      trunkTiles.has(`${x},${y + 1}`) ||
      trunkTiles.has(`${x},${y - 1}`);
    if (edge || trunkRoad || nearTrunk) seed(k);
  }

  // BFS over road-to-road orthogonal adjacency. The reachable SET is
  // order-independent, so seeding order does not affect the result.
  // BUG-460 FIX B: an index pointer instead of queue.shift() avoids O(n) per-dequeue
  // (Array.shift() re-indexes the whole remaining array), which made this O(n^2) on
  // large road networks. The traversal order and reachable set are unchanged.
  let head = 0;
  while (head < queue.length) {
    const k = queue[head++];
    const c = k.indexOf(',');
    const x = Number(k.slice(0, c));
    const y = Number(k.slice(c + 1));
    for (const [ox, oy] of ORTHO) {
      const nk = `${x + ox},${y + oy}`;
      if (roadTiles.has(nk) && !connected.has(nk)) {
        connected.add(nk);
        queue.push(nk);
      }
    }
  }

  return { connectedRoadTiles: Array.from(connected).sort() };
}

/**
 * AC-2 — a building is "road-side" iff any footprint tile has an orthogonal
 * neighbour that is a drivable-road tile. Infrastructure (category 'network') is
 * exempt: it IS the network. Pure/deterministic.
 */
export function isRoadAdjacent(s: SimState, b: SimState['buildings'][number]): boolean {
  const sp = SPECS[b.spec];
  if (!sp) return false;
  if (sp.category === 'network') return true;
  const roads = roadTileSetOf(s);
  // F5 (independent round REJECT, 2026-09-03): walk the building's OWN
  // current footprint (footprintOf), not the spec's base w/h — a grown
  // building's new edge might be the one actually adjacent to a road.
  const { w, h } = footprintOf(b, sp);
  for (let dx = 0; dx < w; dx++)
    for (let dy = 0; dy < h; dy++) {
      const x = b.x + dx;
      const y = b.y + dy;
      for (const [ox, oy] of ORTHO) {
        if (roads.has(`${x + ox},${y + oy}`)) return true;
      }
    }
  return false;
}

/**
 * AC-3 — a building is "road-connected" iff it is road-adjacent AND that adjacent
 * road tile is in the connected network (roadConnectivity.connectedRoadTiles).
 * Infrastructure is exempt. Pure/deterministic.
 */
export function isRoadConnected(s: SimState, b: SimState['buildings'][number]): boolean {
  const sp = SPECS[b.spec];
  if (!sp) return false;
  if (sp.category === 'network') return true;
  const roads = roadTileSetOf(s);
  const connected = connectedRoadTileSet(s);
  // F5 twin of isRoadAdjacent's fix above — same reasoning, same fix.
  const { w, h } = footprintOf(b, sp);
  for (let dx = 0; dx < w; dx++)
    for (let dy = 0; dy < h; dy++) {
      const x = b.x + dx;
      const y = b.y + dy;
      for (const [ox, oy] of ORTHO) {
        const nk = `${x + ox},${y + oy}`;
        if (roads.has(nk) && connected.has(nk)) return true;
      }
    }
  return false;
}

/** One failed activation gate, for the AC-5 WHY tooltip. inc1 = road gates only. */
export interface FailedGate {
  gate: 'construction' | 'road-adjacent' | 'road-connected';
  reason: string;
}

/**
 * AC-5 — the road-gate failure reasons for an OFFLINE building (empty when it is
 * online, infrastructure, or connectivity has not been computed). Ordered:
 * construction first, then road-adjacency, then road-connectivity. Pure.
 */
export function computeFailedGates(s: SimState, b: SimState['buildings'][number]): FailedGate[] {
  const out: FailedGate[] = [];
  const sp = SPECS[b.spec];
  if (!sp) return out;
  if (b.builtTick != null && s.tick - b.builtTick < constructionTicks(sp)) {
    const remaining = constructionTicks(sp) - (s.tick - b.builtTick);
    out.push({ gate: 'construction', reason: `Under construction — ${remaining} ticks remaining` });
    return out; // still building: the road gates aren't meaningful yet.
  }
  if (sp.category === 'network' || !s.roadConnectivity) return out;
  if (!isRoadAdjacent(s, b)) {
    out.push({ gate: 'road-adjacent', reason: 'Not road-side — move adjacent to a road' });
  } else if (!isRoadConnected(s, b)) {
    out.push({
      gate: 'road-connected',
      reason: 'Road not connected — connect to the main road network',
    });
  }
  return out;
}

export function plantEffServed(s: SimState, b: SimState['buildings'][number]): number {
  const sp = SPECS[b.spec];
  return Math.round((sp?.served ?? 0) * PIPE_TIERS[pipeTierOf(s, b.id)].mult);
}

// BUG-642 — memoised per state object (memoOnState, hoisted function
// declaration below). utilisationOf()'s 'water' case calls this once PER
// WATER PLANT from buildingDisplayStates()' per-tick pass, and every call
// re-walked the whole buildings array: O(water plants x buildings) per tick.
// Measured on Aaron's 29,659-building city (488 water plants): 5,423ms of a
// 5,820ms buildingDisplayStates pass — the ~5s main-thread freeze every tick
// that made the game unplayable. Same read-set as before (buildings,
// roadConnectivity/tick via isOnline, pipeTier via plantEffServed), so the
// state-identity key is the correct, field-list-free cache key.
export const waterCaps: (s: SimState) => { clean: number; waste: number } = memoOnState((s) => {
  // BUG-534 — same activation gate as serviceCoverageOf()'s inline water sum
  // and powerStats(): an offline plant serves nobody.
  let clean = 0;
  let waste = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.kind !== 'water') continue;
    const eff = plantEffServed(s, b);
    if (sp.tag === 'clean') clean += eff;
    if (sp.tag === 'waste') waste += eff;
  }
  return { clean, waste };
});

export function waterBalanceOf(s: SimState): {
  clean: number;
  waste: number;
  ratio: number;
  leak: boolean;
} {
  const { clean, waste } = waterCaps(s);
  const ratio = clean > 0 ? waste / clean : 1;
  return { clean, waste, ratio, leak: clean > 0 && ratio < 0.8 };
}

/**
 * Clean- and waste-water DEMAND for this tick (people needing supply / sewage
 * treatment), a READ-OUT only (FEAT-1972079896). It deliberately reuses the ONE
 * existing demand model — the `need` of the 'cleanwater' and 'waste' rows of
 * serviceCoverageOf() (population-driven, GR#3 SSOT) — so the water panel can
 * never disagree with the coverage meters. No new demand model is introduced;
 * pairing this with waterCaps()/waterBalanceOf() makes headroom/shortfall
 * (capacity − demand) directly visible without touching any water mechanic.
 */
export function waterDemandOf(s: SimState): { clean: number; waste: number } {
  const cov = serviceCoverageOf(s);
  const clean = cov.find((c) => c.id === 'cleanwater')?.need ?? 0;
  const waste = cov.find((c) => c.id === 'waste')?.need ?? 0;
  return { clean, waste };
}

/** Per-tier pipe aggregate for the water panel (FEAT-1972079896). */
export interface PipeTierAgg {
  /** Diameter label, e.g. "Ø300 mm". */
  label: string;
  /** Capacity multiplier applied to a plant's base `served`. */
  mult: number;
  upgradeCost: number;
  /** Plants currently fitted with this diameter of main. */
  plants: number;
  /** Σ effServed of the plants on this tier (people served through it). */
  effServedTotal: number;
  /** mult / widest-tier mult (0..1): how far this diameter is toward the widest
   *  available main. See waterPipeInfo() for why this is diameter headroom, not
   *  absolute flow saturation. */
  tierUtil: number;
  /** This is the widest diameter — a pipe here cannot be upgraded further. */
  atCeiling: boolean;
}

/** Per-plant pipe utilisation entry (FEAT-1972079896). */
export interface PipePlantUtil {
  id: number;
  tier: number;
  effServed: number;
  tierUtil: number;
  atCeiling: boolean;
}

/**
 * Pipe utilisation READ-OUT (FEAT-1972079896) — "which pipes are at capacity".
 * Uses ONLY data that already exists; invents no balance numbers.
 *
 * ⚠ WHAT IS *NOT* DEFINED IN THE DATA — the honest gap: there is NO absolute
 * per-diameter throughput ceiling (e.g. "a Ø300 mm main carries at most N
 * people"). PIPE_TIERS models a pipe as a per-plant MULTIPLIER on that plant's
 * base `served` (plantEffServed = served × mult), so the same Ø300 tier
 * legitimately carries 4,000 (Water Tower) up to 60,000 (Reservoir): there is
 * no single Ø300 number to divide effServed by, and inventing one would
 * contradict the model. A true flow-vs-pipe-max utilisation would require a NEW
 * `maxThroughput` field (absolute people/tick) on each PIPE_TIERS entry — a
 * balance number that is a PLACEHOLDER pending Aaron's sign-off.
 *   TODO(FEAT-1972079896, Aaron balance pass): add PIPE_TIERS[].maxThroughput
 *   and switch tierUtil to effServed / maxThroughput once signed off.
 *
 * What IS honest and delivered here (no invented numbers):
 *   - tierUtil  = mult / widest-tier mult (0..1): diameter headroom. 1.0 means
 *                 the pipe is already on the widest main.
 *   - atCeiling = the pipe is on the widest tier and cannot be upgraded — if the
 *                 network is short, the answer is another plant, not a wider pipe.
 * Network-level shortfall (demand > capacity) is available separately via
 * waterDemandOf() vs waterCaps() and is the primary "at capacity" signal.
 */
export function waterPipeInfo(s: SimState): {
  maxTier: number;
  perTier: Record<number, PipeTierAgg>;
  plants: PipePlantUtil[];
} {
  const maxTier = PIPE_TIERS.length - 1;
  const maxMult = PIPE_TIERS[maxTier]?.mult ?? 1;
  const perTier: Record<number, PipeTierAgg> = {};
  const plants: PipePlantUtil[] = [];
  for (const b of s.buildings) {
    // BUG-534 — an offline plant carries no live pipe flow; exclude it from
    // the pipe-utilisation panel, mirroring waterCaps()/serviceCoverageOf().
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.kind !== 'water') continue;
    const tier = pipeTierOf(s, b.id);
    const t = PIPE_TIERS[tier];
    const eff = plantEffServed(s, b);
    const tierUtil = maxMult > 0 ? t.mult / maxMult : 0;
    const atCeiling = tier >= maxTier;
    plants.push({ id: b.id, tier, effServed: eff, tierUtil, atCeiling });
    const agg =
      perTier[tier] ??
      (perTier[tier] = {
        label: t.label,
        mult: t.mult,
        upgradeCost: t.upgradeCost,
        plants: 0,
        effServedTotal: 0,
        tierUtil,
        atCeiling,
      });
    agg.plants += 1;
    agg.effServedTotal += eff;
  }
  return { maxTier, perTier, plants };
}

/**
 * Per-building utilisation: fraction of capacity in actual use (0..1), with a
 * basis name describing the formula applied. Returns null when the sim has no
 * per-building signal for this kind (GR#15 honest absence).
 *
 * CRITICAL DESIGN NOTE (2026-08-27): The sim models capacity as CITY-WIDE aggregates,
 * not per-building state. Residential occupancy, power demand, school places, and job
 * counts are all derived from population + building specs, then assigned city-wide.
 * This module assigns the city-wide ratio to every building of its kind (residential
 * blocks all show the same occupancy %, power plants all show the same draw %, etc).
 * This is a PLACEHOLDER pending per-building capacity tracking in the engine (not
 * yet implemented). The basis string admits this: "citywide occupancy", etc., not
 * "occupancy vs capacity" (which would imply per-building derivation).
 *
 * Derivation (SSOT):
 *   residential = citywide occupancy (population / residents-capacity aggregate)
 *   workplaces (commercial/office/industrial/mine) = citywide workers vs jobs
 *   service buildings (health/police/school/water) = citywide coverage (via serviceCoverageOf)
 *   power = citywide MW draw vs capacity
 *   others (parks, landmarks, leisure, fire, civic, transport) = documented null-basis (no per-building signal)
 */
export interface Utilisation {
  ratio: number; // 0..1, clamped
  basis: string; // formula name for display; "citywide *" admits aggregate assignment
}

export function utilisationOf(s: SimState, b: SimState['buildings'][number]): Utilisation | null {
  const sp = SPECS[b.spec];
  if (!sp) return null;

  const ratio = (have: number, cap: number): number =>
    cap > 0 ? Math.min(1, Math.max(0, have / cap)) : 0;

  switch (sp.kind) {
    case 'residential': {
      const cap = residentsCapacity(s);
      if (cap <= 0) return null;
      return {
        ratio: ratio(s.population, cap),
        basis: 'citywide occupancy',
      };
    }
    case 'power': {
      const pw = powerStats(s);
      if (pw.cap <= 0) return null;
      return {
        ratio: ratio(pw.need, pw.cap),
        basis: 'citywide MW draw',
      };
    }
    case 'school': {
      // BUG-630: was an UNMEMOIZED O(buildings) scan run fresh on every
      // utilisationOf() call for every school building — i.e. O(buildings x
      // school-buildings) per rebuild. serviceCapacityAggregates() computes
      // this exact sum (see schoolPlaces field) in one memoised city-wide
      // pass; reuse it instead of re-deriving. BUG-569's isOnline gate is
      // preserved (serviceCapacityAggregates' loop applies the same gate).
      const { schoolPlaces: places } = serviceCapacityAggregates(s);
      if (places <= 0) return null;
      return {
        ratio: ratio(s.population * 0.18, places),
        basis: 'citywide student places',
      };
    }
    case 'water': {
      const { clean } = waterCaps(s);
      if (clean <= 0) return null;
      return {
        ratio: ratio(s.population, clean),
        basis: 'citywide clean water usage',
      };
    }
    case 'health': {
      // Aggregate health capacity from GP + hospital.
      // FEAT-webworker-sim-offload Stage 0 (2026-09-02): this case is invoked
      // PER BUILDING from MapView's render loop (see file header on
      // serviceCapacityAggregates) — reusing the memoised aggregate instead of
      // two fresh sumBy() full-building-list passes turns what used to be an
      // O(buildings²) render cost (at 68K population, unusable) into O(buildings)
      // (first call per state computes, every subsequent building hits cache).
      const { gp, hosp } = serviceCapacityAggregates(s);
      const cap = gp + hosp;
      if (cap <= 0) return null;
      return {
        ratio: ratio(s.population, cap),
        basis: 'citywide health coverage',
      };
    }
    case 'police': {
      // BUG-399 spec-drift fix: match serviceCoverageOf — count by KIND so a
      // Divisional HQ (pol_hq) also registers police coverage in this panel.
      const { police: cap } = serviceCapacityAggregates(s);
      if (cap <= 0) return null;
      return {
        ratio: ratio(s.population, cap),
        basis: 'citywide police coverage',
      };
    }
    case 'fire': {
      // BUG-526 (Q100046 A1) — mirrors the police case above; fire coverage
      // is now a real served-population basis (serviceCoverageOf, GR#3 SSOT).
      const { fire: cap } = serviceCapacityAggregates(s);
      if (cap <= 0) return null;
      return {
        ratio: ratio(s.population, cap),
        basis: 'citywide fire coverage',
      };
    }
    case 'commercial':
    case 'office':
    case 'industrial':
    case 'mine': {
      const jobs = totalJobs(s);
      if (jobs <= 0) return null;
      return {
        ratio: ratio(s.population * 0.55, jobs),
        basis: 'citywide workers vs jobs',
      };
    }
    // Honest null-basis kinds with no per-building capacity model:
    case 'park':
    case 'landmark':
    case 'leisure':
    case 'civic':
    case 'transport':
    case 'road':
    case 'motorway':
    case 'rail':
    case 'station':
    case 'pylon':
      return null;
    default:
      return null;
  }
}

/**
 * FEAT-2326609740 (BUG-590 close-out): generate a 10-tier ~10%-compound
 * capacityTiers ladder from a base capacity, matching the growth shape of
 * the hand-written res_estate/off_businesspark/ind_estate family ladders
 * above (GR#3 SSOT — one growth-curve generator instead of re-deriving the
 * arithmetic per spec). tier[0] is always exactly `base` (unchanged
 * placement capacity); tier[i] = round(base * 1.1^i) for i = 1..n-1.
 */
function tierLadder(base: number, n = 10): number[] {
  const out: number[] = [];
  for (let i = 0; i < n; i++) out.push(Math.round(base * Math.pow(1.1, i)));
  return out;
}

/**
 * FEAT-2326609740 (Aaron Q100089=B): the NPP reactor ladder. Each tier adds
 * one more reactor unit to the twin-AGR base (Dungeness-scale, 2 reactors /
 * 1,120 MW -> 560 MW/reactor), so tier[i] = base + i * (base/2) — i.e.
 * 2, 3, 4, ... reactors. ⚠ PLACEHOLDER-balance, directional only (per-reactor
 * MW is a straight-line extrapolation of the existing twin-AGR blurb, not a
 * researched figure).
 */
function reactorLadder(base: number, n = 6): number[] {
  const perReactor = base / 2;
  const out: number[] = [];
  for (let i = 0; i < n; i++) out.push(Math.round(base + i * perReactor));
  return out;
}

/**
 * BUG-686 (Aaron ruling, 2026-09-04, "20,000 kindergartens is nuts"): a
 * "campus" ladder for a spec whose top tier represents genuinely much bigger
 * FACILITY (extra classroom blocks/wings added over time), not the ~10%-per-
 * tier efficiency-only growth tierLadder() models. edu_nursery's old
 * tierLadder(30) topped out at 71 — a toy figure next to hea_teaching's
 * 120,000 base — so a 10%-compound curve cannot reach a realistic large
 * early-years-campus scale in 10 tiers without breaking the "tier[0] ==
 * base" contract every other ladder shares. campusLadder keeps that SAME
 * geometric shape (tier[0] == base exactly, tier[i] = round(base * r^i)) but
 * SOLVES the ratio `r` so the LAST tier lands exactly on `top`, instead of
 * fixing `r` at 10% — the reactorLadder() precedent for "this one spec needs
 * its own growth curve, not the generic one" (GR#3: one generator per growth
 * SHAPE, reused wherever that shape applies, rather than a bespoke literal
 * array). ⚠ PLACEHOLDER-balance (Balance Number Regime) — `top` is
 * directional (Aaron's row-by-row pass may retune), not a researched figure.
 */
function campusLadder(base: number, top: number, n = 10): number[] {
  const ratio = Math.pow(top / base, 1 / (n - 1));
  const out: number[] = [];
  for (let i = 0; i < n; i++) out.push(Math.round(base * Math.pow(ratio, i)));
  return out;
}

/**
 * FEAT-2326609740 §2/Aaron Q100083 (2026-09-03): "height-limit table APPROVED
 * as placeholder" — per-spec storey caps enforced structurally by
 * evaluateBuildingMonitors (an UP tier step that would exceed the cap is
 * refused, see engine.ts). ⚠ PLACEHOLDER-balance — directional only, a future
 * Aaron balance pass may retune individual values; the CATEGORY shape
 * (huts/terraces 3, res_block/lowrise 8, mid/high/estate towers 12-30, NYC 60,
 * SGP 40, schools 4, clinics/eldercare 6, hospitals/teaching 12, offices
 * 12-40, factories 3, civic 8) is what Aaron actually approved — individual
 * spec entries below are this repo's placement of each real spec into that
 * approved shape. The NPP reactor ladder (pow_nuke) is height-EXEMPT — see
 * the isPowerLadder branch in evaluateBuildingMonitors — so it carries no
 * entry here.
 */
export const HEIGHT_CAP_STOREYS: Record<string, number> = {
  res_hut: 3,
  res_terrace: 3,
  res_block: 8,
  res_lowrise: 8,
  res_midrise: 15,
  res_highrise: 30,
  res_penthouse: 30,
  res_estate_compact: 15,
  res_estate: 20,
  res_estate_sprawl: 12,
  res_tower_nyc: 60,
  res_tower_sgp: 40,
  edu_nursery: 4,
  edu_nursery_city: 4,
  edu_primary: 4,
  edu_city: 4,
  edu_tech: 6,
  hea_clinic: 6,
  hea_hospital: 12,
  hea_ambulance: 4,
  hea_eldercare: 6,
  hea_teaching: 12,
  pol_station: 4,
  pol_hq: 8,
  off_suite: 12,
  off_tower: 40,
  off_data: 40,
  off_businesspark: 20,
  off_towers_downtown: 40,
  // FEAT-2326609763 — capped at the existing office ceiling (off_tower/
  // off_data/off_towers_downtown = 40): these are wider (9x9), not taller,
  // than the existing towers, so no new height band is needed.
  off_tower_canary: 40,
  off_tower_marina: 40,
  ind_estate: 3,
  farm_estate: 3,
};

/** Height cap for a spec, or Infinity if the spec carries no cap entry
 *  (e.g. a scalable spec HEIGHT_CAP_STOREYS hasn't caught up with yet — an
 *  honest "no limit recorded" rather than a silent 0-storey lock, GR#15). */
export function heightCapOf(sp: Spec | undefined): number {
  if (!sp) return Infinity;
  const cap = HEIGHT_CAP_STOREYS[sp.id];
  return typeof cap === 'number' ? cap : Infinity;
}

const P = (
  id: string,
  kind: ZoneKind,
  name: string,
  blurb: string,
  w: number,
  h: number,
  cost: number,
  upkeep: number,
  color: string,
  category: Spec['category'],
  unlock: number,
  extra: Partial<Spec> = {}
): Spec => ({ id, kind, name, blurb, w, h, cost, upkeep, color, category, unlock, ...extra });

/**
 * PH — placeholder-spec builder (FEAT-1972079877 / FEAT-1972079901 Five Gorges Dam).
 * A thin wrapper over P that hard-codes SAFE ZERO STATS: cost 0, upkeep 0, and NO
 * served/jobs/mw/residents/tourism/tag. It sets `placeholder: true`, which the
 * catalogue renders greyed-out "coming soon" and which isPlaceable() rejects
 * unconditionally — so a placeholder can never be selected, placed, or enter the
 * running sim. These are roadmap previews only; do not tune gameplay against them.
 */
const PH = (
  id: string,
  kind: ZoneKind,
  name: string,
  blurb: string,
  w: number,
  h: number,
  color: string,
  category: Spec['category'],
  unlock: number
): Spec => P(id, kind, name, blurb, w, h, 0, 0, color, category, unlock, { placeholder: true });

/**
 * SINGLE SOURCE OF TRUTH (FEAT-1972079877) for "may this spec EVER become a real
 * building in the running sim". State-independent: a placeholder ("coming soon"
 * roadmap type) can NEVER enter buildings[] via ANY reducer path (place,
 * stampRegion clone-stamp, replay, genesis-replay, debug console). Every
 * building-INSERTION site in engine.ts MUST gate on this — it is the one guard
 * that keeps placeholders out of the sim. Written as a type predicate so callers
 * get `sp` narrowed to a defined Spec after the check.
 */
export function canEnterSim(sp: Spec | undefined): sp is Spec {
  return !!sp && !sp.placeholder;
}

/**
 * FEAT-2326609761 AC-28 — per-spec building count, memoised ONCE per state as a
 * single O(buildings) fold (BUG-642: never re-walk buildings[] per spec, e.g.
 * inside a ~150-spec palette render loop). `countOfSpec` counts buildings
 * REGARDLESS of construction status — a building under construction still
 * occupies its slot, so two dams cannot both be "in progress" at once.
 */
const countBySpecId: (s: SimState) => Record<string, number> = memoOnState((s) => {
  const counts: Record<string, number> = {};
  for (const b of s.buildings) {
    counts[b.spec] = (counts[b.spec] ?? 0) + 1;
  }
  return counts;
});

export function countOfSpec(s: SimState, specId: string): number {
  return countBySpecId(s)[specId] ?? 0;
}

/**
 * FEAT-2326609761 AC-28 — the unique-building allowance, DERIVED from state,
 * never stored (so it can never desync, GR#3): a spec with no `maxPerCity`
 * has infinite allowance; otherwise it is the cap minus how many already
 * exist (clamped at 0, never negative — AC-31 old-save-over-cap safety).
 * This is the ONE function every placement path below calls (AC-29).
 */
export function remainingAllowance(s: SimState, sp: Spec): number {
  if (sp.maxPerCity == null) return Infinity;
  return Math.max(0, sp.maxPerCity - countOfSpec(s, sp.id));
}

/**
 * Aaron ruling 2026-09-04 ("just purge off the extra five gorges dam ...
 * there is only one permitted just delete the others") SUPERSEDES the old
 * FEAT-2326609761 AC-31 ruling ("None are removed" on an over-cap load) —
 * see engine.ts's 'hydrate' reducer case for the load-ceremony call site.
 *
 * Pure selector, GR#21 deterministic: for a `maxPerCity`-capped spec that
 * currently exceeds its cap, returns the SURPLUS instances to purge — i.e.
 * every instance EXCEPT the `maxPerCity` oldest ones (lowest `builtTick`,
 * ties broken by lowest `id` so the choice never depends on array/object
 * iteration order, wall-clock, or Math.random). Returns `[]` (by reference,
 * cheap to call speculatively) when the spec is not over its cap — this is
 * also what makes the load-ceremony purge idempotent: after the first purge
 * the count is exactly `maxPerCity`, so every subsequent call returns [].
 */
export function surplusInstancesOf(s: SimState, sp: Spec): Building[] {
  if (sp.maxPerCity == null) return [];
  const all = s.buildings.filter((b) => b.spec === sp.id);
  if (all.length <= sp.maxPerCity) return [];
  const oldestFirst = [...all].sort(
    (a, b) => (a.builtTick ?? 0) - (b.builtTick ?? 0) || a.id - b.id,
  );
  return oldestFirst.slice(sp.maxPerCity);
}

/**
 * Placement gate (FEAT-1972079877): the SINGLE predicate for "can the player place
 * this spec right now" in the UI. A placeholder is NEVER placeable — regardless of
 * city level, unlock, or the god-mode unlockedAll flag (via canEnterSim). Any real
 * (non-placeholder) spec defers to the existing unlock gate (specUnlocked), so
 * real-spec behaviour is unchanged.
 * FEAT-2326609761 AC-29: also gated on remainingAllowance — an at-cap unique
 * building (e.g. a second Five Gorges Dam) is never placeable, greying its
 * palette entry via this single predicate.
 */
export function isPlaceable(s: SimState, sp: Spec): boolean {
  if (!canEnterSim(sp)) return false;
  if (!specUnlocked(s, sp)) return false;
  return remainingAllowance(s, sp) > 0;
}

/**
 * FEAT-1972079860 AC-1: Sort palette items available-first, locked by unlock
 * level, then placeholder. Pure, deterministic sort.
 *
 * Sort order:
 * 1. Available real specs (isPlaceable && !placeholder) — first
 * 2. Locked real specs (!specUnlocked && !placeholder) — by unlock level ascending
 * 3. At-cap real specs (FEAT-2326609761 AC-30: unlocked, but remainingAllowance
 *    exhausted — e.g. the Five Gorges Dam once built) — after locked, still
 *    before placeholders, because it IS built, not merely unavailable-forever.
 * 4. Placeholder specs (placeholder === true) — last
 *
 * Within each tier, original order is preserved (stable sort).
 */
export function sortPaletteItems(state: SimState, items: string[]): string[] {
  return items.slice().sort((aId, bId) => {
    const a = SPECS[aId];
    const b = SPECS[bId];
    if (!a || !b) return 0; // Missing specs maintain relative order

    const aPlaceholder = a.placeholder === true;
    const bPlaceholder = b.placeholder === true;
    const aUnlocked = !aPlaceholder && canEnterSim(a) && specUnlocked(state, a);
    const bUnlocked = !bPlaceholder && canEnterSim(b) && specUnlocked(state, b);
    const aAtCap = aUnlocked && remainingAllowance(state, a) <= 0;
    const bAtCap = bUnlocked && remainingAllowance(state, b) <= 0;
    const aPlaceable = aUnlocked && !aAtCap;
    const bPlaceable = bUnlocked && !bAtCap;

    // Tier 1: available real specs
    // Tier 2: locked real specs (never unlocked)
    // Tier 3: at-cap real specs (unlocked, but the unique-building cap is hit)
    // Tier 4: placeholder specs
    const aTier = aPlaceholder ? 4 : aAtCap ? 3 : aPlaceable ? 1 : 2;
    const bTier = bPlaceholder ? 4 : bAtCap ? 3 : bPlaceable ? 1 : 2;

    // Sort by tier first
    if (aTier !== bTier) return aTier - bTier;

    // Within tier 2 (locked real specs), sort by unlock level ascending
    if (aTier === 2) {
      return a.unlock - b.unlock;
    }

    // Within tiers 1, 3 and 4, maintain original order (stable sort via slice)
    return 0;
  });
}

/**
 * BUG-452 inc1 (2026-09-01, Aaron's approved GBP-scale anchors): the full
 * `cost`/`upkeep` catalogue below was rebased off toy £40-£560,000 figures
 * onto realistic UK capex, PRESERVING the cheap->expensive spread (the whole
 * point — not flattened to one number). Category-by-category basis:
 *   - 'power' generators (mw field): cost = mw × a per-MW rate WITHIN the
 *     anchor's £0.75-1.5M/MW range, upkeep = 2%/year of that cost. The rate
 *     is DIFFERENTIATED by generator type (not one flat £/MW for everyone),
 *     preserving Aaron's FEAT-1972079901 ruling ("nuclear VERY expensive up
 *     front... above renewables/gas per-MW"): fusion £1.6M (experimental
 *     premium, priciest) > nuclear £1.4M > hydro £1.0M (mega-tier, still
 *     cheaper per-MW at huge scale) > coal £0.85M > gas CCGT £0.8M (mid
 *     fossil tier) > offshore wind £0.75M > onshore wind £0.7M > solar
 *     £0.65M > small wind turbine £0.6M (small renewables cheapest).
 *     megapower-inc1.test.mjs pins this ordering.
 *   - 'services' category (non-power): cost × 1800 — calibrated so
 *     edu_primary lands at ~£9.36M, inside the anchor's £8-11M primary-school
 *     range — upkeep recomputed as 2%/year of the new cost.
 *   - 'network' (roads/junctions/stations): hand-tuned, NOT a blanket
 *     multiplier — several existing reducer tests place buildings at genesis
 *     funds (now STARTING_TREASURY = £1.5M, fiscal.ts) or a fixed "ample"
 *     fixture, so MOTORWAY_JUNCTION_COST/rd_dual/station_ashford etc. are
 *     capped well under those fixtures rather than pushed to the full
 *     multi-million real-world figure a literal per-km anchor would imply.
 *   - 'zones' (residential/commercial/industrial/office/mine/park/farm):
 *     cost × 1000 — COSMETIC ONLY. placementCost() returns £0 for every
 *     'zones' spec regardless of `sp.cost` (zoning is free to place), so this
 *     bump has NO fiscal affordability effect; it exists only to keep
 *     constructionTicks() build-time pacing unchanged (its cost-per-tick
 *     divisor was bumped by the same 1000x, see CONSTRUCTION_COST_DIVISOR
 *     above) — upkeep (which DOES matter fiscally) is left UNCHANGED.
 * All figures remain PLACEHOLDER-balance (Aaron's row-by-row pass pending) —
 * this rebase fixes the ORDER OF MAGNITUDE, not the final tuned numbers.
 */
/**
 * FEAT-2326609782 (2026-09-04, Aaron's ruling): m20/rail STOP being £0 map
 * furniture — both had sat at cost:0/upkeep:0 since genesis, which meant (a)
 * inc3's rail/motorway layout tiers laid FREE (AC-9 partial) and (b)
 * autoConnect's m20 connector showed capex £0 even on Aaron's own city
 * (id-2098 report, BUG-682's "the zero-cost level-99 profile smells like a
 * placeholder leaking into the choice"). PLACEHOLDER-tier per the balance
 * regime (row-by-row tuning pending) — this fixes the ORDER OF MAGNITUDE,
 * derived from real UK per-km costs rebased onto this game's 50m tile:
 *   - m20 (motorway): UK real-world ≈ £30M/km → £30M × 0.05km/tile =
 *     £1,500,000/tile build cost. Upkeep ≈ £3,000/tile/month, i.e.
 *     3000 / 30 ticks-per-month (engine.ts TICKS_PER_MONTH) = £100/tile/tick
 *     (Spec.upkeep is PER-TICK — see rail/road siblings below and
 *     fiscal.ts's councilTaxPerCapitaPerTick for the same /30 convention).
 *   - rail: UK real-world ≈ £15M/km → £15M × 0.05km/tile = £750,000/tile
 *     build cost. Upkeep ≈ £1,500/tile/month = 1500/30 = £50/tile/tick.
 * Both specs also NEED a UPKEEP_BUCKET entry (fiscal.ts) now that upkeep is
 * nonzero — 'motorway' -> 'Roads' (m20 shares the family the other road tiers
 * already bucket into) and 'rail' -> 'Transport' (matches station/transport);
 * without those two entries this upkeep would silently vanish from the
 * outflows instead of booking (see fiscal.ts's own "placeholder catalogue
 * kinds" comment for the same class of bug, closed there for a different set
 * of kinds).
 */
export const SPECS: Record<string, Spec> = {
  m20: P('m20', 'motorway', 'M20 Motorway', '', 1, 1, 1500000, 100, '#1d5fa8', 'network', 99, { roadTier: 5, capacity: 2500 }),
  rail: P('rail', 'rail', 'Rail Line', '', 1, 1, 750000, 50, '#8a6d3b', 'network', 99),
  station_sanderling: P('station_sanderling', 'station', 'Sanderling Station', '', 1, 1, 0, 15, '#d0a83c', 'network', 99),
  // BUG-652: real Ashford International directly employs roughly 150-250
  // people across station operations, retail, and HS1/Southeastern staff — a
  // major but not huge mainline interchange, well below a full rail-freight
  // yard or depot's headcount. 200 jobs, kind 'station' (KIND_TO_WAGE_SECTOR
  // already maps station -> public, no fiscal.ts change needed). Flat, not
  // capacityTiers-scaled (station_ashford carries no ladder) — see
  // jobsAtTier()'s doc comment above totalJobs() for why `served`+`jobs`
  // together on one spec with no capacityTiers must stay flat.
  station_ashford: P('station_ashford', 'station', 'Ashford International', 'HS1 international gateway · 60,000 served · x3 commuter weight', 4, 2, 150000000, 8333, '#e0559f', 'network', 5, { served: 60000, jobs: 200 }),
  hs1: P('hs1', 'rail', 'HS1 High-Speed Line', '', 1, 1, 0, 0, '#c2477e', 'network', 99),
  pylon: P('pylon', 'pylon', 'HV Pylon', '', 1, 1, 0, 5, '#9aa4ae', 'network', 99),

  road: P('road', 'road', 'Road', '', 1, 1, 12000, 1, '#4a525c', 'network', 1, { roadTier: 1, capacity: 100 }),

  // FEAT-2326609740 (BUG-590): capacityTiers added to close the spec-coverage
  // gap — these were the 7 base residential specs a player builds early game
  // that previously had NO auto-scale ladder at all (evaluateBuildingMonitors
  // hard-skipped them), the exact stall Aaron's tick-1196 capture hit.
  res_hut: P('res_hut', 'residential', 'Small Holding', '8 residents', 1, 1, 220000, 1, '#4c9aff', 'zones', 1, { residents: 8, capacityTiers: tierLadder(8) }),
  res_block: P('res_block', 'residential', 'Estate Block', '60 residents', 2, 2, 1600000, 6, '#4c9aff', 'zones', 2, { residents: 60, capacityTiers: tierLadder(60) }),

  com_shop: P('com_shop', 'commercial', 'Corner Shop', 'Local trade', 1, 1, 320000, 2, '#e3b341', 'zones', 1),
  com_retail: P('com_retail', 'commercial', 'Retail Park', 'Shopping quarter', 3, 2, 4200000, 18, '#e3b341', 'zones', 2),

  farm_wheat: P('farm_wheat', 'industrial', 'Wheat Farm', 'Arable · golden crop', 2, 2, 800000, 4, '#d9b13b', 'zones', 1),
  farm_cattle: P('farm_cattle', 'industrial', 'Cattle Pasture', 'Dairy herd', 3, 3, 1400000, 6, '#7da24f', 'zones', 1),
  farm_orchard: P('farm_orchard', 'industrial', 'Orchard', 'Fruit · blossom crop', 2, 2, 1000000, 5, '#97c15c', 'zones', 1),
  ind_factory: P('ind_factory', 'industrial', 'Factory', 'Goods + freight jobs', 2, 2, 2400000, 14, '#a371f7', 'zones', 2, { tag: 'pollution' }),

  park: P('park', 'park', 'Park', 'Green space', 1, 1, 150000, 10, '#3fb950', 'zones', 1),

  // BUG-648 (2026-09-03, Aaron: "make it realistic — so what do we have in the
  // real world"). Before this fix pow_wind (8 MW / 1 tile = 8.00 MW/tile)
  // out-densified pow_windfarm (60 MW / 9 tiles = 6.67) and pow_nuke (1,120 MW
  // / 169 tiles = 6.63) — a single turbine beating a nuclear plant on power
  // density, backwards vs reality and fatal to the CONSOLIDATOR's data-driven
  // density ladder (FEAT-2326609761 Q100100/AC-8: "10 wind turbines -> 1 wind
  // farm", "10 nuke plants -> 1 XXL nuke" must fall out of the catalogue, not
  // be hand-listed).
  //
  // TILE_METRES = 50 (this file's DIMS comment, "Tile grid = 50 m", promoted
  // for this derivation — FEAT-2326609761 AC-3 will export it as a real
  // constant when the consolidator lands) => 1 tile = 50m x 50m = 2,500 m² =
  // 0.0025 km². A LITERAL real-area/TILE_METRES conversion is unusable here:
  // NREL's onshore wind density study (docs.nrel.gov/docs/fy09osti/45834.pdf,
  // updated national analysis ~4.3 MW/km² over 106.7 GW of US plants) implies
  // ~1 km² = ~400 TILES of exclusive land per single modern turbine — absurd
  // for a 1-tile placeable object, and real onshore/offshore wind farms and
  // nuclear exclusion zones are similarly whole-km²-scale. COMPRESSION RULE
  // (applied UNIFORMLY, not fudged per spec): EVERY footprint (w x h tiles) in
  // this catalogue, pow_nuke INCLUDED, is HELD AT its prior balance-pass value
  // (BUG-452/FEAT-1972079901) — realism is carried by `mw` alone, sourced
  // from a named real reference plant per technology class.
  //
  // ROUND-2 CORRECTION (2026-09-04, independent destructive round REJECT):
  // the FIRST draft of this fix also shrunk pow_nuke's footprint 13x13->5x4 to
  // fix its density ranking against pow_ccgt. An independent attacker proved
  // that "a shrink is safe" claim FALSE for road-adjacency (it is only true
  // for tile-overlap): replayed against Aaron's real building record and real
  // road network, his one already-placed pow_nuke (id 3331, x:33,y:141) is
  // road-adjacent under the old 13x13 footprint but NOT under the shrunk 5x4
  // one (the real road touches the wide footprint's old outer edge, which is
  // now 8+ tiles away) — so isOnline flips false and 1,120 MW of his grid
  // capacity silently vanishes on load, with no notice. footprintOf's
  // "shrink can't overlap a neighbour" safety property says nothing about
  // road-adjacency, which is a DIFFERENT gate over the SAME footprint. The
  // footprint shrink is REVERTED — pow_nuke stays 13x13 — per the round's
  // recommendation (one line, zero risk): pow_nuke and pow_hydro are now BOTH
  // documented FOOTPRINT-REALISM EXCEPTIONS to the density ladder (see the
  // dedicated pow_nuke comment below) rather than being forced to out-density
  // gas per tile; the CONSOLIDATOR's "10 nuke plants -> 1 XXL nuke" example
  // is intended to fall out of COUNT-reduction (fewer, bigger reactors via
  // pow_nuke's own capacityTiers ladder), not out of pow_nuke beating pow_ccgt
  // on MW/tile.
  //
  // Sources (WebSearch, 2026-09-03):
  //  - Turbine: RENA/GWEC Global Status Report 2025 — "the average turbine
  //    delivered to market in 2024 had a capacity of 5.5 MW"; DOE "Wind
  //    Turbines: the Bigger the Better" — "modern onshore turbines reach
  //    capacities of 3-5 MW"; Siemens Gamesa's SG 6.6-170 shows production
  //    onshore models reaching ~6-6.6 MW. We use 6 MW — the TOP of the
  //    realistic modern range, not the middle — DELIBERATELY: see the
  //    savepoint blast-radius note immediately below.
  //  - CCGT: Whitegate Power Station, Ireland — 445 MW on an 11 ha (0.11 km²)
  //    site (Wikipedia); Coryton Power Station, UK — 732 MW on 5.2 ha. The
  //    existing 420 MW figure already sits in this real range and is left
  //    UNCHANGED (it was never the part of the catalogue Aaron flagged).
  //  - Nuclear: Dungeness B (the plant this spec's own flavour text already
  //    names) nets ~1,110 MWe (Wikipedia) — near-identical to the existing
  //    1,120 MW figure, so `mw` is LEFT UNCHANGED; Hinkley Point C's real
  //    165-acre (0.668 km²) OPERATIONAL site (gov.uk/Wikipedia) is the same
  //    order of magnitude as pow_nuke's UNCHANGED 13x13 = 650m x 650m =
  //    0.42 km² footprint — a real nuclear exclusion zone genuinely IS
  //    large, so keeping the wide footprint is the honest choice, not the
  //    shrunk one; see the dedicated pow_nuke comment below.
  //  - Hydro (special case, per Aaron's brief): researched (this spec is a
  //    Three Gorges pastiche — DIMS z:181 already cites the real dam's 181 m
  //    height — whose actual nameplate is 22,500 MW, Wikipedia) but the mw
  //    figure was DELIBERATELY LEFT UNCHANGED at 5,000 — see the dedicated
  //    pow_hydro comment below for why (its density was never part of
  //    Aaron's named defect, and Aaron's real savepoint already carries 23
  //    placed dams, so a literal Three-Gorges mw would silently hand that
  //    city a six-figure MW windfall — exactly what this bug's brief
  //    forbids). It already dwarfs pow_nuke's density (78.1 MW/tile vs the
  //    unchanged 6.63) with zero edit needed.
  //
  //  SAVEPOINT BLAST-RADIUS CHECK (constraint (2) of this bug's brief — "do
  //  not silently change the total power available to an existing city"):
  //  Aaron's real fresh savepoint (49,174 buildings, per the round-2 attack)
  //  is DOMINATED by pow_wind turbines (4,535 online) — a direct symptom of
  //  the very defect this bug fixes (turbines being the densest/cheapest-
  //  per-MW option). The round measured the TRUE before/after online-gated
  //  delta at -14.57% (bigger than this file's own -3.0% gross-nameplate
  //  estimate, both because the city grew and — pre-revert — because the
  //  footprint shrink itself was silently taking the one placed nuke
  //  offline). Even at -14.57%, capacity (59,745 MW) still clears need
  //  (39,534 MW) with a healthy 51% margin — NO brownout flip on that save.
  //  6 MW instead of a naive 4 MW halving (still a genuine, cited
  //  modern-turbine figure, not a fudge) was chosen specifically to keep
  //  that margin healthy while still fixing the named ordering defect (6.0 <
  //  pow_windfarm's unchanged 6.67 MW/tile — turbine still the least dense
  //  rung). This is a mitigation, not a full guarantee against brownout on
  //  every possible save: a complete fix would be a per-instance
  //  "grandfathered mw" migration (mirroring the existing
  //  `b.footprintW ?? sp.w` pattern already used for auto-scale ladder
  //  growth) so pre-existing buildings keep their placement-time nameplate
  //  while new construction reads the realistic catalogue value — left as a
  //  follow-up BOW item rather than expanding this fix's scope from a
  //  catalogue rebalance into a save-schema migration.
  //
  // cost/upkeep are RECOMPUTED (not left stale) via the SAME documented
  // £/MW rate x 2%/year-over-360-ticks formula the BUG-452 header above uses
  // (rate unchanged per technology, so megapower-inc1.test.mjs's cost/mw
  // ratio-ordering assertions are untouched by this mw-only-formula change).
  //
  // BUG-477 (2026-09-05) ⚠ PLACEHOLDER-balance, pending Aaron's row-by-row —
  // the catalogue coherence pass found pow_wind sitting at 600k/MW, BELOW
  // Aaron's own 08-31 UK-realism anchor band (0.75-1.5M/MW). Raised to
  // 1.2M/MW — the SAME onshore-wind anchor this file's BUG-648 sourcing
  // block above already cites (RENA/GWEC 2025 + DOE) — which also removes
  // wind's status as simultaneously the cheapest-per-MW AND (post-BUG-648)
  // one of the least-dense generators, a combination BUG-685/686's
  // largest-first provisioning was over-selecting on cost alone. upkeep
  // RECOMPUTED via the SAME unchanged 2%/year-over-360-ticks rate
  // (7,200,000 * 0.02 / 360 = 400). Does NOT resolve the deeper BUG-477
  // finding (fiscal.ts's GRID_EXPORT/IMPORT tariffs sit ~2 orders of
  // magnitude below ANY plant's amortised £/MW/tick under a realistic
  // capex anchor — see verifyGridTariffInvariant's doc comment) — that
  // needs a cross-module ruling, not a catalogue nudge.
  pow_wind: P('pow_wind', 'power', 'Wind Turbine', '6 MW · clean', 1, 1, 7200000, 400, '#7fb2e5', 'services', 2, { mw: 6 }),
  pow_coal: P('pow_coal', 'power', 'Coal Plant', '80 MW · polluting', 2, 2, 68000000, 3778, '#f0883e', 'services', 3, { mw: 80, tag: 'pollution' }),
  // FEAT-1972079901 realistic power costs: nuclear is the priciest generator to
  // BUILD (Aaron's ask — nuclear VERY expensive up front). Capex raised 150k→560k
  // (~£500/MW, above renewables/gas per-MW) and opex 600→1400. `mw`/footprint/tag
  // UNCHANGED — cost-only retune. ⚠ PLACEHOLDER-balance — Aaron's row-by-row pass.
  // Q100089=B: the NPP becomes a capacityTiers ladder too — tiers = reactor
  // count (reactorLadder), MW scales per tier. Height-EXEMPT (see
  // evaluateBuildingMonitors' isPowerLadder branch) — every tier is an OUT
  // (footprint growth) step, since "another reactor" is a new physical unit,
  // never a taller one.
  // BUG-648 (2026-09-03) — `mw`/cost/upkeep/footprint ALL LEFT UNCHANGED at
  // 1,120 MW / 13x13. 1,120 MW was already a realistic figure (real
  // Dungeness B, the plant this spec's own flavour text names, nets
  // ~1,110 MWe — Wikipedia — a near match), and a FIRST DRAFT of this fix
  // shrunk the footprint to 13x13->5x4 to out-density pow_ccgt (which would
  // have needed mw bumped past the point where megapower-inc1.test.mjs's
  // `pow_hydro.mw >= pow_nuke.mw * 3` forces a matching pow_hydro bump,
  // cascading a six-figure MW windfall onto Aaron's 23 already-placed dams —
  // see the pow_hydro comment below).
  //
  // ROUND-2 CORRECTION (2026-09-04): that footprint shrink was REVERTED after
  // an independent destructive round proved it a live regression, not a safe
  // change — see the dedicated block above pow_wind for the full finding
  // (Aaron's real placed pow_nuke, id 3331, silently road-disconnects under
  // the shrunk 5x4 footprint even though the SAME real road network keeps it
  // online under the original 13x13). pow_nuke is therefore now a documented
  // FOOTPRINT-REALISM EXCEPTION, like pow_hydro below: its density (1,120 /
  // 169 = 6.63 MW/tile) legitimately sits BELOW pow_ccgt's 46.7, and that is
  // the honest answer — a real nuclear exclusion zone genuinely is large
  // (13x13 tiles = 650m x 650m = 0.42 km², the same order of magnitude as
  // Hinkley Point C's real 165-acre/0.668 km² operational site), so a
  // compact gas plant legitimately IS denser per tile than a nuclear one.
  // The CONSOLIDATOR's "10 nuke plants -> 1 XXL nuke" example (Aaron's own
  // words, FEAT-2326609761) is handled by COUNT-reduction via pow_nuke's own
  // capacityTiers reactor ladder (fewer, bigger reactor complexes), not by
  // this spec out-densifying gas per tile — see bug648-power-density.test.mjs
  // for the regression pinning pow_nuke's footprint at 13x13 forever.
  //
  // BUG-477 (2026-09-05) coherence finding, PROPOSED — NOT landed here.
  // pow_nuke sits at 1.4M/MW — INSIDE Aaron's 08-31 0.75-1.5M conventional
  // band, when his own ruling on that same date explicitly asked for
  // nuclear/fusion to carry "a prestige premium ABOVE that band" (real UK
  // nuclear capex is nowhere near this cheap either — Hinkley Point C
  // prices out around £9-14M/MW). A directionally-correct fix is roughly
  // ~3M/MW (cost 1,568,000,000 -> 3,360,000,000; upkeep 186,667, same
  // unchanged 2%/year-over-360-ticks rate). TRIED AND REVERTED in this pass
  // — the measured blast radius was materially larger than the "one
  // hardcoded literal" case pow_offshore/pow_windfarm carry, because
  // raising `cost` also raises constructionTicks() (cost /
  // CONSTRUCTION_COST_DIVISOR), pushing the real build-out from 1,045 to
  // 2,240 ticks:
  //   1. megapower-inc1.test.mjs "nuclear £/MW must exceed offshore £/MW"
  //      — only reddens if pow_offshore is ALSO bumped (coupled change).
  //   2. q100092-construction-display.test.tsx — its NUKE_TICKS literal
  //      (1045-tick-derived) desyncs from the real building's now-2240-tick
  //      construction gate; halfway/completion assertions fail (confirmed
  //      via `node ../tools/test/scoped.mjs
  //      test/q100092-construction-display.test.tsx`).
  //   3. attack-autoscale-ladder.test.mjs "NPP: pow_nuke is height-exempt…"
  //      — its fixture reactor (builtTick -2000) falls OFFLINE under the
  //      new 2240-tick requirement, failing the "sanity: reactor is online"
  //      precondition before the real assertion under test even runs.
  // Recommend a dedicated follow-up BOW item covering pow_nuke AND
  // pow_fusion (see below — coupled) together, sized against this full
  // list plus a fresh full-suite construction-timing sweep (any OTHER
  // fixture with a builtTick offset sized to the OLD nuke/fusion
  // construction time is equally at risk and was not exhaustively found in
  // this pass's time budget), not folded into the same commit as the
  // lower-risk wind/windfarm rescale this pass DID land.
  pow_nuke: P('pow_nuke', 'power', 'Nuclear Plant', 'Twin AGR · 1,120 MW · Dungeness-scale', 13, 13, 1568000000, 87111, '#e05d38', 'services', 5, { mw: 1120, tag: 'pollution', capacityTiers: reactorLadder(1120) }),

  wat_clean: P('wat_clean', 'water', 'Water Works', 'Clean water for 20,000', 2, 2, 4680000, 260, '#39c5cf', 'services', 3, { tag: 'clean', served: 20000 }),
  wat_waste: P('wat_waste', 'water', 'Waste-Water Plant', 'Treats sewage for 20,000', 2, 2, 6120000, 340, '#6b8f71', 'services', 3, { tag: 'waste', served: 20000 }),

  // FEAT-2326609740 §2 service-spec set: capacityTiers + monitor coverage
  // extended to health/police/school/office alongside residential above.
  hea_clinic: P('hea_clinic', 'health', 'Clinic', 'GPs for 5,000', 1, 1, 3240000, 180, '#ff7b72', 'services', 2, { served: 5000, capacityTiers: tierLadder(5000), careTier: 'local' }),
  hea_hospital: P('hea_hospital', 'health', 'General Hospital', 'Serves 40,000', 2, 2, 28800000, 1600, '#d95f57', 'services', 4, { served: 40000, capacityTiers: tierLadder(40000), careTier: 'regional' }),
  pol_station: P('pol_station', 'police', 'Police Station', 'Covers 10,000', 2, 1, 4680000, 260, '#6e7bd9', 'services', 3, { served: 10000, capacityTiers: tierLadder(10000) }),

  // BUG-686: capacityTiers extended from tierLadder(30) (topped at a toy 71)
  // to campusLadder(30, 2000) — a fully-expanded Kindergarten becomes a real
  // early-years campus (≈2,000 places), not a slightly-bigger single hut. See
  // campusLadder()'s own doc comment for why this spec needs its own curve.
  edu_nursery: P('edu_nursery', 'school', 'Kindergarten', '30 places · ages 0–4', 1, 1, 2160000, 120, '#ffd166', 'services', 2, { children: 30, stage: 'nursery', capacityTiers: campusLadder(30, 2000) }),
  // FEAT-2326609761 (Aaron, 2026-09-05, dogfood gap): "ONE city kindergarten
  // doing 1000 children, not 40 kindergartens" — his own consolidation
  // example, and the catalogue had NO same-stage successor for edu_nursery
  // at all (a nursery could only ever consolidate into more nurseries).
  // Same `stage: 'nursery'` as edu_nursery so familyKeyOf() (kind|
  // capacityField|tag|stage) matches — edu_primary/edu_city are a DIFFERENT
  // stage ('primary'/'city') and must never be a nursery successor (AC-7).
  // 1000/30 = 33.3x edu_nursery's capacity, comfortably clears AC-8 rule 3's
  // CONSOLIDATOR_MIN_GROUP(4)x floor; footprint 3x3=9 tiles gives density
  // 111.1 children/tile vs edu_nursery's 30/tile, clearing rule 4 (never
  // lose density) by a wide margin so a future catalogue retune has room.
  // ⚠ PLACEHOLDER-balance (directional only, Aaron balance-reviews):
  // cost/upkeep interpolated on a per-child basis between edu_nursery
  // (£72,000/child, £4/child upkeep) and edu_city (£28,800/child, £1.6/child
  // upkeep) — City Kindergarten sits at £40,000/child (£40.0M) and
  // £2.2/child upkeep (£2,200), i.e. cheaper per-place than a nursery but
  // dearer than a full City School, consistent with the existing economies-
  // of-scale shape (see tierLadder/reactorLadder's own doc comments for the
  // precedent of a placeholder growth curve pending Aaron's numbers pass).
  // Unlock level 4 matches edu_city — a late-game consolidation target, not
  // an early-game build.
  edu_nursery_city: P('edu_nursery_city', 'school', 'City Kindergarten', '1,000 places · ages 0–4', 3, 3, 40000000, 2200, '#ffb703', 'services', 4, { children: 1000, stage: 'nursery', capacityTiers: tierLadder(1000) }),
  edu_primary: P('edu_primary', 'school', 'Primary School', '300 places · ages 5–11', 2, 2, 9360000, 520, '#f2c14e', 'services', 3, { children: 300, stage: 'primary', capacityTiers: tierLadder(300) }),
  edu_city: P('edu_city', 'school', 'City School', '2,000 places · ages 5–15', 3, 2, 57600000, 3200, '#e3a92f', 'services', 4, { children: 2000, stage: 'city', capacityTiers: tierLadder(2000) }),
  col_sixth: P('col_sixth', 'school', 'College', '1,500 places · ages 16–19', 2, 2, 32400000, 1800, '#b58fd8', 'services', 4, { children: 1500, stage: 'tertiary' }),
  // BUG-652: UK higher-education staff-to-student ratios run roughly 1:8-1:10
  // once academic, admin, estates, and research staff are all counted
  // (UniversitiesUK sector workforce data) — applying ~1:9 to uni's 6,000
  // students gives ~650 jobs. kind 'school' -> KIND_TO_WAGE_SECTOR already
  // maps school -> public, no fiscal.ts change needed. Flat, not
  // capacityTiers-scaled (uni carries no ladder) — see jobsAtTier()'s doc
  // comment above totalJobs(); totalChildrenCapacity() was also patched
  // (above) so this new `jobs` field can never be misread as uni's children
  // capacity.
  uni: P('uni', 'school', 'University', '6,000 students', 3, 3, 135000000, 7500, '#a371f7', 'services', 5, { children: 6000, stage: 'tertiary', jobs: 650 }),

  off_suite: P('off_suite', 'office', 'Office Suite', '25 office jobs', 1, 1, 900000, 5, '#43aa8b', 'zones', 2, { jobs: 25, capacityTiers: tierLadder(25) }),
  off_tower: P('off_tower', 'office', 'Office Tower', '300 office jobs', 2, 3, 22000000, 120, '#43aa8b', 'zones', 4, { jobs: 300, capacityTiers: tierLadder(300) }),

  mine_quarry: P('mine_quarry', 'mine', 'Quarry', 'Materials + freight jobs', 2, 2, 3200000, 20, '#b08d55', 'zones', 3, { tag: 'pollution', jobs: 30 }),
  mine_deep: P('mine_deep', 'mine', 'Deep Mine', 'Heavy freight output', 3, 3, 15000000, 80, '#9c6f3f', 'zones', 5, { tag: 'pollution', jobs: 90 }),

  // ════════════════════════════════════════════════════════════════════════
  // BUG-652 — the whole `landmark` kind carried ZERO jobs before this fix,
  // despite land_airport/land_stadium/land_tunnel all modelling real,
  // heavily-staffed infrastructure. fiscal.ts's KIND_TO_WAGE_SECTOR had NO
  // 'landmark' entry at all (a GR#3-class inconsistency in its own right —
  // documented loudly on that mapping's own comment, fiscal.ts) — a landmark
  // spec carrying `jobs` with no sector to bucket into would count toward
  // totalJobs()/employment but contribute ZERO wage outflow, i.e. jobs with
  // no wage bill. FIXED by adding `landmark: 'tertiary'` to
  // KIND_TO_WAGE_SECTOR (fiscal.ts) — airports/stadiums/tunnel operations
  // are overwhelmingly private/commercial-sector employment in the real
  // world, the same bucket commercial/office jobs already land in.
  // ════════════════════════════════════════════════════════════════════════
  // Real Regional Stadium staffing: a mid-size (30-40k capacity) stadium's
  // permanent (non-matchday-casual) staff — ops, grounds, security,
  // hospitality management — typically runs 200-300 FTE; matchday casual
  // staff (often 1,000+) are deliberately excluded, since `jobs` here models
  // steady employment, not event-day casual labour. 250 jobs.
  land_stadium: P('land_stadium', 'landmark', 'Regional Stadium', 'Tourism magnet + approval', 3, 2, 43200000, 2400, '#d0a83c', 'services', 5, { tourism: 60, jobs: 250 }),
  // Real Heathrow directly employs ~76,000 people (Heathrow Airport Ltd
  // employment reporting) across its ~1,227 ha site — this spec's own blurb
  // already cites that exact 1,227 ha figure, and 70x70 tiles x 50m/tile
  // (DIMS convention) = 3,500m x 3,500m = 12,250,000 sqm = 1,225 ha, an
  // almost exact real-footprint match. Using the literal 76,000 figure
  // directly (no further scaling needed) gives 76,000 / 4,900 tiles =
  // 15.5 jobs/tile — comfortably BELOW off_towers_downtown's existing
  // 80 jobs/tile ceiling, so this does not introduce a new density outlier;
  // it simply stops a genuinely enormous real employer from counting zero.
  land_airport: P('land_airport', 'landmark', 'International Airport', 'Heathrow-scale · 1,227 ha · twin 3.9 km runways', 70, 70, 810000000, 45000, '#5eb3d6', 'services', 6, { tourism: 140, jobs: 76000 }),
  land_harbour: P('land_harbour', 'landmark', 'Deep-Water Harbour', 'Freight income x1.4', 3, 3, 68400000, 3800, '#5e8bb0', 'services', 7, {}),

  // ════════════════════════════════════════════════════════════════════════
  // FEAT-1972079877 — PLACEHOLDER OBJECT CATALOGUE.
  // Curated from the Go engine's data/buildings.json (356 entries) + the
  // master plan's module families so the palette looks populated NOW; real
  // mechanics wire in later. Entries below only participate in the generic
  // sim paths (cost/upkeep/jobs/served/mw/residents/tourism).
  //
  // ⚠ BALANCE-NUMBER REGIME (Aaron's blanket rule): every cost / upkeep /
  // capacity figure in this block is a PLACEHOLDER — directional only,
  // pending Aaron's row-by-row balance pass. Do not tune gameplay against
  // these numbers.
  // ════════════════════════════════════════════════════════════════════════

  // ---- Transport (buses / trams / metro / ferries / parking) ----
  bus_stop: P('bus_stop', 'transport', 'Bus Stop', 'Local hopper services', 1, 1, 540000, 30, '#5ea0c8', 'services', 2),
  bus_depot: P('bus_depot', 'transport', 'Bus Depot', 'Runs 20 local routes', 2, 2, 8100000, 450, '#5ea0c8', 'services', 4, { jobs: 20 }),
  car_park: P('car_park', 'transport', 'Multi-storey Car Park', 'Park & ride commuters', 2, 2, 10800000, 600, '#7f93a8', 'services', 5),
  bus_station: P('bus_station', 'transport', 'Bus Station', 'Regional coach interchange', 2, 2, 16200000, 900, '#5ea0c8', 'services', 6, { served: 12000 }),
  tram_depot: P('tram_depot', 'transport', 'Tram Depot', 'Street tram network hub', 2, 2, 25200000, 1400, '#4d8fb8', 'services', 8, { jobs: 35 }),
  ferry_pier: P('ferry_pier', 'transport', 'Ferry Pier', 'Cross-channel foot ferry', 1, 2, 19800000, 1100, '#4a9dae', 'services', 9, { tourism: 15 }),
  metro_station: P('metro_station', 'transport', 'Metro Station', 'Underground rapid transit', 2, 2, 46800000, 2600, '#3d7ea6', 'services', 12, { served: 30000 }),
  grand_terminus: P('grand_terminus', 'transport', 'Grand Terminus', 'Victorian rail cathedral', 3, 2, 108000000, 6000, '#d0a83c', 'services', 14, { served: 80000, jobs: 60 }),

  // ---- Housing tiers ----
  // FEAT-2326609740 (BUG-590): capacityTiers ladders added — these are the
  // remaining 5 of the 7 originally-unscalable base residential specs.
  res_terrace: P('res_terrace', 'residential', 'Terrace Row', '30 residents · Victorian brick', 2, 1, 900000, 3, '#4c9aff', 'zones', 3, { residents: 30, capacityTiers: tierLadder(30) }),
  res_lowrise: P('res_lowrise', 'residential', 'Low-rise Flats', '120 residents', 2, 2, 3200000, 10, '#4c9aff', 'zones', 4, { residents: 120, capacityTiers: tierLadder(120) }),
  res_midrise: P('res_midrise', 'residential', 'Mid-rise Flats', '280 residents', 2, 2, 7800000, 22, '#3d84e6', 'zones', 6, { residents: 280, capacityTiers: tierLadder(280) }),
  res_highrise: P('res_highrise', 'residential', 'High-rise Tower', '600 residents', 2, 2, 21000000, 60, '#3d84e6', 'zones', 9, { residents: 600, capacityTiers: tierLadder(600) }),
  res_penthouse: P('res_penthouse', 'residential', 'Penthouse Tower', '350 wealthy residents', 2, 2, 45000000, 90, '#6ab0ff', 'zones', 13, { residents: 350, capacityTiers: tierLadder(350) }),

  // ---- Retail tiers ----
  com_market: P('com_market', 'commercial', 'Market Hall', 'Covered traders market', 2, 2, 2200000, 10, '#e3b341', 'zones', 3, { jobs: 25 }),
  com_super: P('com_super', 'commercial', 'Supermarket', 'Weekly shop anchor', 2, 2, 5200000, 24, '#e3b341', 'zones', 4, { jobs: 40 }),
  com_mall: P('com_mall', 'commercial', 'Shopping Mall', 'Regional retail destination', 3, 3, 30000000, 160, '#d9a52e', 'zones', 8, { jobs: 220 }),

  // ---- Industry tiers ----
  ind_light: P('ind_light', 'industrial', 'Light Industrial Units', 'Workshops + trades', 2, 2, 1800000, 10, '#a371f7', 'zones', 3, { jobs: 24 }),
  ind_warehouse: P('ind_warehouse', 'industrial', 'Warehouse', 'Storage + distribution', 2, 2, 3600000, 16, '#9a6ee0', 'zones', 5, { jobs: 18 }),
  ind_heavy: P('ind_heavy', 'industrial', 'Heavy Industry Estate', 'Big plant · heavy freight', 3, 3, 16000000, 90, '#8957d9', 'zones', 7, { tag: 'pollution', jobs: 110 }),
  ind_cement: P('ind_cement', 'industrial', 'Cement Works', 'Construction materials', 2, 2, 12000000, 70, '#8957d9', 'zones', 9, { tag: 'pollution', jobs: 45 }),
  ind_logistics: P('ind_logistics', 'industrial', 'Automated Logistics Hub', 'Robotic freight sorting', 3, 3, 48000000, 210, '#b58fd8', 'zones', 15, { jobs: 60 }),

  // ---- Offices ----
  off_data: P('off_data', 'office', 'Data Centre', '90 tech jobs · heavy power draw', 2, 2, 34000000, 240, '#2f8f74', 'zones', 12, { jobs: 90, capacityTiers: tierLadder(90) }),

  // ---- Parks tiers ----
  park_playground: P('park_playground', 'park', 'Playground', 'Swings + climbing frame', 1, 1, 400000, 6, '#3fb950', 'zones', 2),
  park_town: P('park_town', 'park', 'Town Park', 'Bandstand + boating lake', 2, 2, 2400000, 30, '#3fb950', 'zones', 4),
  park_botanical: P('park_botanical', 'park', 'Botanical Garden', 'Glasshouses + collections', 2, 2, 9000000, 80, '#2f9e44', 'zones', 8, { tourism: 20 }),
  park_nature: P('park_nature', 'park', 'Nature Reserve', 'Wetland + wildlife', 3, 3, 6000000, 40, '#2f9e44', 'zones', 12),

  // ---- Leisure ----
  lei_leisure: P('lei_leisure', 'leisure', 'Leisure Centre', 'Pool + courts for 8,000', 2, 2, 12600000, 700, '#e07be0', 'services', 4, { served: 8000 }),
  lei_cinema: P('lei_cinema', 'leisure', 'Cinema', 'Eight-screen multiplex', 2, 1, 9900000, 550, '#e07be0', 'services', 5, { tourism: 10 }),
  lei_theatre: P('lei_theatre', 'leisure', 'Theatre', 'Rep company + touring shows', 2, 2, 21600000, 1200, '#c95fc9', 'services', 7, { tourism: 18 }),
  lei_museum: P('lei_museum', 'leisure', 'Museum', 'County collection', 2, 2, 27000000, 1500, '#c95fc9', 'services', 9, { tourism: 25 }),
  lei_arena: P('lei_arena', 'leisure', 'Arena', '12,000-seat events bowl', 3, 3, 99000000, 5500, '#b34fb3', 'services', 11, { tourism: 70 }),
  lei_themepark: P('lei_themepark', 'leisure', 'Theme Park', 'Coasters + day-trippers', 4, 4, 216000000, 12000, '#b34fb3', 'services', 16, { tourism: 160 }),

  // ---- Power additions ----
  pow_substation: P('pow_substation', 'power', 'Substation', 'Grid step-down node', 1, 1, 2160000, 120, '#9aa4ae', 'services', 3),
  pow_solar: P('pow_solar', 'power', 'Solar Farm', '25 MW · clean', 3, 3, 16250000, 903, '#f6c744', 'services', 6, { mw: 25 }),
  // BUG-477 (2026-09-05) ⚠ PLACEHOLDER-balance — same onshore-wind anchor
  // (1.2M/MW) as pow_wind above, for the same reason (was 700k/MW, below
  // the 0.75-1.5M band): cost 42,000,000 -> 72,000,000, upkeep recomputed
  // (72,000,000 * 0.02 / 360 = 4,000).
  pow_windfarm: P('pow_windfarm', 'power', 'Onshore Wind Farm', '60 MW · clean', 3, 3, 72000000, 4000, '#7fb2e5', 'services', 7, { mw: 60 }),
  pow_ccgt: P('pow_ccgt', 'power', 'CCGT Gas Plant', '420 MW · fast response', 3, 3, 336000000, 18667, '#f0883e', 'services', 8, { mw: 420, tag: 'pollution' }),
  // BUG-477 (2026-09-05) coherence finding, PROPOSED — NOT landed here.
  // Currently 750k/MW, near-parity with onshore wind/wind-farm's NEW
  // 1.2M/MW anchor (was near-parity with their OLD 600-700k/MW too). Real
  // UK offshore wind capex runs roughly 1.5-2x onshore (marine
  // foundations/cabling), so near-parity is incoherent either way — a
  // directionally-correct fix is ~2.0M/MW (cost 225,000,000 ->
  // 600,000,000; upkeep 33,333). NOT LANDED because it was tried and
  // measurably cascades: at 2.0M/MW, offshore's £/MW (2.0M) overtakes
  // pow_nuke's UNCHANGED 1.4M/MW, reddening megapower-inc1.test.mjs's
  // "nuclear £/MW must exceed offshore" invariant — fixing that in turn
  // requires bumping pow_nuke/pow_fusion, which cascades further (see the
  // pow_nuke comment below for the full chain: construction-tick timing
  // shifts redden attack-autoscale-ladder.test.mjs's NPP online-sanity
  // check and consolidator-mutation.test.mjs's fusion-consolidation
  // fixture, on top of attack-consolidator-inc1-round.test.mjs:132's
  // hardcoded 225000000 literal). Offshore's rescale is coupled to nuke's
  // — recommend one dedicated follow-up BOW item that lands both together,
  // round-tested against the FULL blast-radius list above, rather than
  // three uncoordinated partial passes.
  pow_offshore: P('pow_offshore', 'power', 'Offshore Wind Array', '300 MW · clean', 3, 3, 225000000, 12500, '#5b8fc9', 'services', 12, { mw: 300 }),
  // FEAT-1972079901 realistic power costs: experimental mega-plant, dearest per-MW
  // capex of all generators (400k→520k, ~£650/MW; opex 900→1500). `mw` UNCHANGED.
  // BUG-477 (2026-09-05) coherence finding, PROPOSED — NOT landed here.
  // Currently 1.6M/MW, only marginally above pow_nuke's own (also-flagged)
  // 1.4M/MW; an experimental first-of-kind fusion pilot should carry a
  // clearer premium over commercial fission (~3.5M/MW: cost 1,280,000,000
  // -> 2,800,000,000; upkeep 155,556). No hardcoded-literal fixture
  // references this spec's exact cost, BUT TRIED-AND-REVERTED alongside
  // pow_nuke above: raising this spec's cost pushes its
  // constructionTicks() from 853 to 1,867 ticks, which desyncs
  // consolidator-mutation.test.mjs's "control: consolidates pow_fusion
  // into a Five Gorges Dam" fixture (its 7-turbine pow_fusion group is
  // placed at builtTick:-1000 — under the old 853-tick requirement they
  // are long since online; under 1,867 they are STILL under construction,
  // so the consolidation pass never sees them as eligible source buildings
  // and the dam never gets built — "the dam was built": 0 !== 1). Coupled
  // to pow_nuke's deferral above — land together, one follow-up BOW item.
  pow_fusion: P('pow_fusion', 'power', 'Fusion Pilot Plant', '800 MW · experimental', 4, 4, 1280000000, 71111, '#ff9f43', 'services', 19, { mw: 800 }),

  // FEAT-1972079901 — FIVE GORGES DAM (GRADUATED from a roadmap placeholder to a
  // real, placeable mega-hydro GENERATOR). A single huge hydroelectric station
  // dwarfs the Nuclear Plant. It is a power generator, so
  // its `mw` correctly adds to powerStats.cap like any plant. Deliberately carries
  // NO water `tag`/`served` and NO `residents` — a power spec only, so it can never
  // leak clean/waste-water or residential capacity (the waste_depot/estate lesson).
  // Hydro is clean, so (like pow_wind/pow_solar) it takes no `pollution` tag.
  // Huge 8×8 footprint + late unlock (16). ⚠ every figure PLACEHOLDER-balance —
  // directional only, pending Aaron's row-by-row pass.
  // FEAT-2326609761 AC-28 (Aaron R4): "limit the number of Five Gorges Dams to
  // just one" — HARD CAP of 1, enforced at every placement path (AC-29) and
  // in the palette (AC-30). See remainingAllowance() below. (Aaron's real
  // pre-cap savepoint carries 23 placed dams — AC-31 grandfathers them: they
  // survive on load, a 24th is refused.)
  // BUG-648 (2026-09-03) — EVALUATED AND DELIBERATELY LEFT UNCHANGED. Aaron's
  // bug report named pow_wind (8.00 MW/tile), pow_windfarm (6.67) and pow_nuke
  // (6.63) as the backwards trio; pow_hydro's density (5,000/64 = 78.1
  // MW/tile) was NOT flagged — it was already the densest spec in the
  // catalogue and already dwarfs pow_nuke's UNCHANGED figure (78.1 vs 6.63 —
  // pow_nuke's footprint-shrink draft that would have raised this to 56.0
  // was reverted, see the pow_nuke comment above; pow_nuke and pow_hydro are
  // now BOTH documented footprint-realism exceptions to the density ladder),
  // so the "special case" ladder position this bug asks for already held
  // with NO edit here. A real-world pass was still researched
  // (this spec is a Three Gorges Dam pastiche — the DIMS z:181 already cites
  // the real dam's 181 m crest height — whose actual nameplate is 22,500 MW,
  // Wikipedia) but that change was REJECTED: bumping mw 5,000->22,500 would
  // silently hand his 23-dam city ~+402,500 MW of grid capacity and a
  // matching 4.5x jump in dam upkeep alone — exactly the "silently change
  // the total power available to an existing city" this bug's brief forbids,
  // for a spec that was never broken. Left at 5,000 MW pending a dedicated
  // balance-pass BOW item now that the one-dam cap (maxPerCity above) exists
  // to bound a future bump's blast radius.
  // Aaron ruling 2026-09-04: "rename it to the three gorges dam there is
  // only one permitted" — display NAME ONLY. The spec id stays `pow_hydro`
  // (save/journal compatibility, GR#3 — nothing keys off the display string)
  // and every real-world balance figure below is unchanged.
  pow_hydro: P('pow_hydro', 'power', 'Three Gorges Dam', 'Mega hydroelectric dam · 5,000 MW · dwarfs a nuclear plant', 8, 8, 5000000000, 277778, '#5b8fc9', 'services', 16, { mw: 5000, maxPerCity: 1 }),

  // ---- Water & waste additions ----
  wat_tower: P('wat_tower', 'water', 'Water Tower', 'Pressure head for 4,000', 1, 1, 2700000, 150, '#39c5cf', 'services', 2, { tag: 'clean', served: 4000 }),
  wat_reservoir: P('wat_reservoir', 'water', 'Reservoir', 'Valley dam · serves 60,000', 4, 4, 81000000, 4500, '#2ba7b1', 'services', 9, { tag: 'clean', served: 60000 }),
  wat_sewage_regional: P('wat_sewage_regional', 'water', 'Regional Sewage Works', 'Treats waste for 60,000', 3, 3, 68400000, 3800, '#6b8f71', 'services', 11, { tag: 'waste', served: 60000 }),

  // FEAT-1972079906 inc1: the Refuse Depot GRADUATES from a "coming soon" roadmap
  // placeholder to a real, placeable collection depot — runs the city's rounds.
  // `wasteCapacity` (tonnes/tick collected) drives collectionCoverageOf(); it has
  // NO water tag, so it never counts as clean/waste-water capacity. ⚠ cost /
  // upkeep / wasteCapacity are PLACEHOLDER-balance — Aaron's row-by-row pass.
  waste_depot: P('waste_depot', 'water', 'Refuse Depot', 'Collects refuse on city rounds · 50 t/tick', 2, 2, 5400000, 300, '#6b8f71', 'services', 4, { wasteCapacity: 50 }),

  // ---- Education additions ----
  edu_tech: P('edu_tech', 'school', 'Technical College', '2,200 places · trades + T-levels', 2, 2, 43200000, 2400, '#b58fd8', 'services', 6, { children: 2200, stage: 'tertiary', capacityTiers: tierLadder(2200) }),

  // ---- Health additions ----
  hea_ambulance: P('hea_ambulance', 'health', 'Ambulance Station', 'Six-crew emergency cover', 1, 1, 6840000, 380, '#ff7b72', 'services', 5, { served: 15000, capacityTiers: tierLadder(15000), careTier: 'local' }),
  hea_eldercare: P('hea_eldercare', 'health', 'Elder-care Home', '90 assisted-living places', 2, 2, 15300000, 850, '#d95f57', 'services', 7, { served: 90, capacityTiers: tierLadder(90) }),
  // BUG-652: real large NHS teaching hospital trusts (e.g. Cambridge
  // University Hospitals/Addenbrooke's, ~12,000 staff for a ~1M-population
  // regional catchment — a ~1:83 staff-to-served ratio) employ far more per
  // capita than a district general hospital, reflecting teaching/research
  // roles on top of clinical care. The original ~1:83 ratio was applied to
  // hea_teaching's THEN served=120,000 to derive ~1,450 jobs; `jobs` is kept
  // FLAT (grandfathered) rather than re-derived from `served` every time the
  // latter is balance-tuned — see the FEAT-2326609761 note directly below
  // for why `served` itself has since moved. kind 'health' ->
  // KIND_TO_WAGE_SECTOR already maps health -> public, no fiscal.ts change
  // needed. FLAT, not scaled by the existing capacityTiers ladder — that
  // ladder is sized for `served`, NOT jobs; see jobsAtTier()'s doc comment
  // above totalJobs() for why a spec with jobs + an unrelated capacityTiers
  // ladder must keep jobs flat (blindly scaling would overstate this spec's
  // job count by reading its served-tier value as its job count).
  //
  // FEAT-2326609761 (Aaron, 2026-09-05, dogfood gap): "ONE teaching hospital
  // replaces many [hospitals]" — his own consolidation example — was refused
  // by AC-8 rule 3 on the ORIGINAL 120,000-served catalogue value: hea_
  // hospital's `served: 40,000` only reaches hea_teaching's old 120,000 at a
  // 3x ratio, short of CONSOLIDATOR_MIN_GROUP(4)x. `served` raised
  // 120,000 -> 200,000 (5x hea_hospital, clearing the 4x floor with
  // headroom) so the ladder rung Aaron actually asked for now exists in
  // data, per the design's own preferred remedy (raise the successor's
  // capacity rather than weaken CONSOLIDATOR_MIN_GROUP globally, which would
  // have widened every OTHER family's group-size floor too — a much larger
  // blast radius for a single hand-picked example). cost/upkeep scaled by
  // the same 5/3 ratio to keep £-per-served roughly constant with the rest
  // of the health family (153.0M -> 255.0M; 8,500 -> 14,200) — `jobs: 1450`
  // is UNCHANGED (grandfathered per the paragraph above: it is staff
  // headcount, not served-capacity, and BUG-652 already established it must
  // never be re-derived from a `served`/capacityTiers change).
  // ⚠ PLACEHOLDER-balance (directional only, Aaron balance-reviews) — both
  // this bump and the ORIGINAL 120,000/153.0M/8,500 figures it replaces were
  // always placeholder catalogue numbers, never a researched real-hospital
  // figure.
  hea_teaching: P('hea_teaching', 'health', 'Teaching Hospital', 'Serves 200,000 + trains doctors', 3, 3, 255000000, 14200, '#c24f47', 'services', 10, { served: 200000, capacityTiers: tierLadder(200000), jobs: 1450, careTier: 'regional' }),

  // ---- Police & justice ----
  pol_hq: P('pol_hq', 'police', 'Divisional HQ', 'Commands 60,000 coverage', 2, 2, 27000000, 1500, '#6e7bd9', 'services', 9, { served: 60000, capacityTiers: tierLadder(60000) }),
  civ_courthouse: P('civ_courthouse', 'civic', 'Courthouse', 'Magistrates + crown courts', 2, 2, 21600000, 1200, '#8a94a8', 'services', 8),
  civ_prison: P('civ_prison', 'civic', 'Prison', 'Category B · 800 places', 3, 2, 46800000, 2600, '#707a8c', 'services', 10),
  // FEAT-1972079870 — the ADX supermax.
  civ_adx: P('civ_adx', 'civic', 'ADX Supermax', 'Maximum-security prison · escape-proof', 3, 3, 162000000, 9000, '#565e6e', 'services', 17),

  // ---- Fire & rescue ----
  fire_post: P('fire_post', 'fire', 'Volunteer Fire Post', 'Retained crew · covers 4,000', 1, 1, 1800000, 100, '#f65b56', 'services', 2, { served: 4000 }),
  fire_station: P('fire_station', 'fire', 'Fire Station', 'Two pumps · covers 20,000', 2, 1, 8640000, 480, '#f65b56', 'services', 4, { served: 20000 }),
  fire_hq: P('fire_hq', 'fire', 'Regional Fire HQ', 'Command + specialist appliances', 2, 2, 32400000, 1800, '#d94a45', 'services', 11, { served: 80000 }),

  // ---- Civic ----
  civ_library: P('civ_library', 'civic', 'Library', 'Lending + study space', 1, 1, 5400000, 300, '#8a94a8', 'services', 5),
  civ_townhall: P('civ_townhall', 'civic', 'Town Hall', 'Local governance seat', 2, 2, 16200000, 900, '#8a94a8', 'services', 6),
  civ_cityhall: P('civ_cityhall', 'civic', 'City Hall', 'Metropolitan administration', 2, 2, 54000000, 3000, '#707a8c', 'services', 12),

  // ---- Landmark additions ----
  land_cathedral: P('land_cathedral', 'landmark', 'Cathedral', 'Gothic spire · pilgrimage draw', 2, 2, 72000000, 4000, '#d0a83c', 'services', 11, { tourism: 45 }),
  land_eye: P('land_eye', 'landmark', 'The Folkestone Eye', 'Coastal observation wheel', 1, 1, 50400000, 2800, '#5eb3d6', 'services', 13, { tourism: 55 }),
  // BUG-652: Eurotunnel (Getlink) reports ~3,000-4,000 direct staff across
  // both the UK and French terminals combined — a single portal (this spec
  // models ONE side of the crossing) is roughly half that: ~1,800 jobs
  // (rail control, freight terminal operations, security, customs). kind
  // 'landmark' -> KIND_TO_WAGE_SECTOR 'landmark' entry added above
  // (land_stadium's comment block) so this now buckets into 'tertiary'
  // instead of contributing to totalJobs() with no wage bill.
  // Aaron ruling 2026-09-04: "the channel tunnel location needs to be
  // bigger too" — grown from 3x3 (9 tiles) to 6x4 (24 tiles, ~2.7x area).
  // ⚠ PLACEHOLDER-tier (GR#15): the real Cheriton terminal is a genuinely
  // huge site (~150 ha of portal/marshalling yard), so 3x3 was always a
  // gross understatement; picked the next sensible step up alongside this
  // catalogue's other big landmarks (land_space is 5x5 at a higher cost/
  // tier) rather than jumping to land_airport's 70x70 real-world scale,
  // which would dwarf everything else on the palette. Directional only,
  // pending Aaron's row-by-row balance pass. EXISTING placed tunnels are
  // grandfathered at their OLD 3x3 footprint via the per-building
  // footprintW/footprintH override (see the hydrate load ceremony in
  // engine.ts) so no already-built portal is retroactively made to overlap
  // a neighbour; only NEW placements use this bigger footprint.
  land_tunnel: P('land_tunnel', 'landmark', 'Channel Tunnel Portal', 'Continental rail gateway', 6, 4, 450000000, 25000, '#c2477e', 'services', 18, { tourism: 80, jobs: 1800 }),
  land_space: P('land_space', 'landmark', 'Space Launch Complex', 'Kent spaceport · mega-project', 5, 5, 1080000000, 60000, '#ff9f43', 'services', 20, { tourism: 200 }),
  // ═══════════════════ end FEAT-1972079877 placeholder block ═══════════════

  // ════════════════════════════════════════════════════════════════════════
  // FEAT-1972079877 — GREYED-OUT "COMING SOON" ROADMAP PLACEHOLDERS.
  // Planned-but-unbuilt types the player can SEE (greyed / disabled) so the
  // catalogue previews the roadmap. Built via PH(): placeholder:true + ZERO
  // sim stats. NEVER placeable (isPlaceable rejects them), so they never enter
  // the running sim. Grouped into their existing catalogue families in PALETTE.
  // Includes FEAT-1972079901 Five Gorges Dam (pow_hydro).
  // ════════════════════════════════════════════════════════════════════════

  // ---- Network (roads / rail lines) ----
  // FEAT-1972079907 inc1: rd_avenue/rd_aroad/rd_dual GRADUATE from placeholders to
  // real placeable road tiers — real cost/upkeep + roadTier/capacity, placeholder
  // flag removed. ⚠ PLACEHOLDER-balance cost/upkeep/capacity — Aaron's sign-off.
  rd_avenue: P('rd_avenue', 'road', 'Avenue', 'Tree-lined urban avenue · tier 2', 1, 1, 27000, 2, '#4a525c', 'network', 3, { roadTier: 2, capacity: 250 }),
  rd_aroad: P('rd_aroad', 'road', 'A-Road', 'Arterial trunk road · tier 3', 1, 1, 54000, 3, '#454c56', 'network', 4, { roadTier: 3, capacity: 500 }),
  rd_dual: P('rd_dual', 'road', 'Dual Carriageway', 'High-capacity dual road · tier 4', 1, 1, 96000, 5, '#3f4650', 'network', 5, { roadTier: 4, capacity: 1000 }),

  // FEAT-1972079910 inc2: auto-junction specs — placed at tile where new road crosses existing.
  // AC-6: below avenue tier → plain crossroads; avenue+ → roundabout (tier = max of the two roads).
  // unlock: 99 (seed infrastructure) because junctions are auto-placed, never manually selected.
  // ⚠ PLACEHOLDER-balance cost/upkeep — directional, pending Aaron's approval.
  rd_junction: P('rd_junction', 'road', 'Crossroads', 'Plain crossing · auto-placed', 1, 1, 4500, 0, '#5a626d', 'network', 99, { roadTier: 1, capacity: 100 }),
  rd_roundabout: P('rd_roundabout', 'road', 'Roundabout', 'Traffic circle · auto-placed', 1, 1, 15000, 1, '#515961', 'network', 99, { roadTier: 2, capacity: 250 }),

  // FEAT-1972079910 inc3 (AC-7): rail bridge — auto-placed grade-separated crossing
  // where a dual+ road crosses a rail tile. Spec has kind:'rail' so it participates
  // in rail connectivity (isRailSpec includes it). Cost is computed per-placement as
  // road_cost × RAIL_BRIDGE_COST_MULTIPLIER. unlock: 99 (auto-placed, never manual).
  // ⚠ PLACEHOLDER-balance cost/upkeep — directional, pending Aaron's approval.
  rd_railbridge: P('rd_railbridge', 'rail', 'Rail Bridge', 'Grade-separated rail crossing · auto-placed', 1, 1, 0, 0, '#8a6d3b', 'network', 99, { roadTier: 4, capacity: 1000 }),

  // FEAT-1972079910 inc3 (AC-8): motorway junction — auto-placed where a road
  // crosses the highest-tier motorway-class spec (m20). Both roads are continuous
  // through the junction. unlock: 99 (auto-placed, never manual).
  // ⚠ PLACEHOLDER-balance cost/upkeep — directional, pending Aaron's approval.
  rd_mwyjunction: P('rd_mwyjunction', 'road', 'Motorway Junction', 'Grade-separated motorway crossing · auto-placed', 1, 1, 480000, 0, '#1d5fa8', 'network', 99, { roadTier: 5, capacity: 2500 }),

  rail_branch: PH('rail_branch', 'rail', 'Branch Line', 'Planned — single-track branch railway', 1, 1, '#8a6d3b', 'network', 6),

  // ---- Transport ----
  trans_parkride: PH('trans_parkride', 'transport', 'Park & Ride', 'Planned — edge-of-town commuter interchange', 2, 2, '#5ea0c8', 'services', 7),
  trans_interchange: PH('trans_interchange', 'transport', 'Interchange Hub', 'Planned — multi-modal transport interchange', 2, 2, '#4d8fb8', 'services', 10),
  rail_freightyard: PH('rail_freightyard', 'transport', 'Freight Yard', 'Planned — rail freight marshalling yard', 3, 2, '#7f6a3b', 'services', 8),
  ev_charging_hub: PH('ev_charging_hub', 'transport', 'EV Charging Hub', 'Planned — rapid EV charging plaza', 1, 1, '#3d7ea6', 'services', 6),

  // ---- Housing / Offices / Industry estates ----
  // FEAT-1972079900 inc1 — ESTATE-SCALE placeable specs (object density / LOD inc1).
  // Each GRADUATES from a "coming soon" placeholder into a real, placeable object
  // standing for a WHOLE estate: one big building carrying the AGGREGATE jobs /
  // residents / upkeep / footprint of the ~N constituent buildings it represents
  // (the placeable-tier reading of the brief — ICI-Wilton / out-of-town-retail /
  // business-park / housing-estate scale). They flow through placement, road
  // activation, economy and waste EXACTLY like any building — keyed off
  // kind / category / jobs / residents — so NO sim LOGIC changed; they are pure DATA.
  //   • Deliberately NO `mw`: power DRAW is count-based (powerStats counts one
  //     industrial/office building), and `mw` on a NON-power spec would be summed
  //     as grid GENERATION — a cross-system leak (the waste_depot lesson).
  //   • Deliberately NO water `tag` / `served`: a housing estate must HOUSE
  //     residents, never supply water capacity.
  //   • The retail estate is com_hypermarket (an out-of-town superstore), graduated
  //     below in the Retail group.
  //   • Render-coarsening (auto-merge at zoom) + up-density variants are inc2 — NOT built here.
  // ⚠ every footprint / job / resident / cost / upkeep figure is PLACEHOLDER-balance
  // — directional only, anchored to the constituent specs, pending Aaron's row-by-row pass.
  // FEAT-1972079878 inc1: housing estates now have three density tiers (compact/medium/sprawl)
  // with auto-scale support via capacityTiers arrays. Capacity progression ~10%/tier.
  res_estate_compact: P('res_estate_compact', 'residential', 'Compact Housing', 'Apartment blocks in dense urban cores · 4×4', 4, 4, 32000000, 90, '#4c9aff', 'zones', 8, { residents: 900, capacityTiers: [900, 990, 1089, 1197, 1317, 1449, 1594, 1753, 1929, 2121] }),
  res_estate: P('res_estate', 'residential', 'Housing Estate', 'Master-planned housing estate · ≈ 12 low-rise blocks', 5, 5, 45000000, 130, '#4c9aff', 'zones', 10, { residents: 1500, capacityTiers: [1500, 1650, 1815, 1996, 2196, 2416, 2657, 2923, 3215, 3536] }),
  res_estate_sprawl: P('res_estate_sprawl', 'residential', 'Sprawl Housing', 'Low-rise suburban sprawl · 6×6', 6, 6, 70000000, 200, '#4c9aff', 'zones', 15, { residents: 2500, capacityTiers: [2500, 2750, 3025, 3327, 3660, 4026, 4429, 4872, 5359, 5894] }),

  // FEAT-2326609750 — ULTRA-DENSE MEGA-TOWER residential specs (Aaron's ask). These
  // graduate the res_estate_* family (above) one more rung: instead of one big
  // building standing for ~12-20 constituent blocks, these stand for a WHOLE
  // dense city quarter — a NYC-style residential superblock and a Singapore-style
  // mega-estate precinct. Same estate-scale DATA-only treatment as res_estate_*:
  // no `mw` (power draw stays count-based), no water `tag`/`served` (they HOUSE
  // residents, never supply water capacity) — pure `residents` + `capacityTiers`
  // so they flow through placement / road activation / economy / waste / school /
  // health exactly like every other residential spec (GR#15 — zero extra wiring).
  //
  // Footprint honesty (Aaron's FEAT-2326609740 ruling: footprint growth = shape
  // growth, not density growing forever on a fixed footprint): res_highrise (2×2,
  // 600 residents) is the densest EXISTING single building at 150 residents/tile.
  // These two are deliberately given honest LARGER footprints rather than just
  // cramming more residents into a highrise-sized box — 7×7 (49 tiles) and 9×9
  // (81 tiles), both well past every current residential footprint (res_estate_
  // sprawl's 6×6 is the previous largest), reflecting that a 10k/20k-resident
  // structure is a whole superblock/precinct, not one more tower.
  //
  // Ratio derivation (GR#15 — extrapolated from the existing family, not invented):
  //   res_estate:        cost/resident ≈ £30,000, upkeep/resident ≈ 0.0867/tick
  //   res_estate_sprawl: cost/resident ≈ £28,000, upkeep/resident ≈ 0.08/tick
  //   res_tower_nyc  extrapolates res_estate's ratio:        £30,000/resident, 0.087/tick
  //   res_tower_sgp  extrapolates res_estate_sprawl's ratio: £28,000/resident, 0.08/tick
  //     (Singapore mass-housing read as the MORE cost-efficient-at-scale precinct,
  //     matching the trend already in the family: bigger estate = lower £/resident)
  // Unlock gated well above the whole Housing family's current ceiling
  // (res_estate_sprawl = 15) — these are the capstone of residential density —
  // in the same 16-20 band as other capstone mega-projects (pow_hydro 16,
  // land_tunnel 18, land_space 20).
  // ⚠ BALANCE-NUMBER PLACEHOLDER — every cost/upkeep/residents figure here is
  // directional only, pending Aaron's row-by-row balance pass (house convention).
  res_tower_nyc: P('res_tower_nyc', 'residential', 'NYC-style Superblock', 'Dense Manhattan-style residential superblock · ≈ 10,000 residents · 7×7', 7, 7, 300000000, 870, '#3d84e6', 'zones', 16, { residents: 10000, capacityTiers: [10000, 11000, 12100, 13310, 14641, 16105, 17716, 19487, 21436, 23579] }),
  res_tower_sgp: P('res_tower_sgp', 'residential', 'Singapore-style Mega-Estate', 'Ultra-dense Singapore-style HDB mega-precinct · ≈ 20,000 residents · 9×9', 9, 9, 560000000, 1600, '#3d84e6', 'zones', 18, { residents: 20000, capacityTiers: [20000, 22000, 24200, 26620, 29282, 32210, 35431, 38974, 42872, 47159] }),
  off_businesspark: P('off_businesspark', 'office', 'Business Park', 'Landscaped out-of-town office park · ≈ 4 towers', 5, 5, 85000000, 420, '#43aa8b', 'zones', 12, { jobs: 1200, capacityTiers: [1200, 1320, 1452, 1597, 1757, 1933, 2126, 2339, 2573, 2830] }),
  off_towers_downtown: P('off_towers_downtown', 'office', 'Downtown Towers', 'Dense downtown office towers', 5, 5, 128000000, 630, '#43aa8b', 'zones', 14, { jobs: 2000, capacityTiers: [2000, 2200, 2420, 2662, 2928, 3221, 3543, 3897, 4287, 4716] }),

  // FEAT-2326609763 — ULTRA-DENSE EMPLOYMENT TOWERS (Aaron's ask, closing the
  // measured 3x jobs-vs-housing density gap: res_tower_sgp houses 247
  // residents/tile while off_towers_downtown, the densest job spec, tops out
  // at only 80 jobs/tile — a player can house people they can never employ,
  // and BUG-652's own numbers show 3M full employment needing 20.3% of the
  // entire 440x260 map at that ceiling). Mirrors res_tower_nyc/res_tower_sgp's
  // shape EXACTLY: same office `kind` (so KIND_TO_WAGE_SECTOR routes them into
  // 'tertiary' with zero extra wiring, same as every other office spec), same
  // 'zones' category (free to place, catalogue cost shown for build-time only
  // — placementCost()), same capacityTiers-via-tierLadder() auto-scale wiring,
  // same HEIGHT_CAP_STOREYS entry, same PALETTE-family placement.
  //
  // Footprint (Aaron's brief: BOTH 9x9, matching res_tower_sgp's ceiling
  // rather than the 7x7/9x9 split the residential pair uses) — a whole
  // financial-district block, honestly larger than every existing office
  // footprint (off_businesspark/off_towers_downtown top out at 5x5).
  //
  // Ratio derivation (GR#15 — extrapolated from the existing office family,
  // not invented, mirroring res_tower_nyc/res_tower_sgp's OWN derivation
  // exactly: the smaller/first tower takes the pricier existing family's
  // ratio, the bigger/second tower takes the cheaper one — "bigger estate =
  // lower £/unit" is already the trend the residential pair established):
  //   off_businesspark:      cost/job = £70,833.33, upkeep/job = 0.35/tick
  //   off_towers_downtown:   cost/job = £64,000.00, upkeep/job = 0.315/tick
  //   off_tower_canary (10,000 jobs) extrapolates off_businesspark's ratio:
  //     10,000 x £70,833.33 = £708,333,333 cost, 10,000 x 0.35 = 3,500 upkeep.
  //   off_tower_marina (20,000 jobs) extrapolates off_towers_downtown's
  //     (cheaper) ratio: 20,000 x £64,000 = £1,280,000,000 cost,
  //     20,000 x 0.315 = 6,300 upkeep.
  //
  // Real-world sanity check (directional only, PLACEHOLDER-balance per this
  // catalogue's blanket rule — the £ figures are the game's stylised economy,
  // not a literal real construction cost, exactly like res_tower_sgp
  // abstracting decades of actual HDB investment into one placeable price):
  //   - Canary Wharf (London): ~120,000 people work across the estate today,
  //     but the "Canary Wharf-scale financial district" ask here targets the
  //     ~10,000-job CORE tower cluster scale (One Canada Square + immediate
  //     neighbours), not the whole 97-acre estate — matching the ask's stated
  //     10,000 figure.
  //   - Marina Bay Financial Centre (Singapore): the MBFC/Marina Bay
  //     downtown core is estimated at ~20,000+ jobs across its office towers,
  //     matching the ask's stated 20,000 figure.
  //   Both land at 9x9 = 81 tiles = 202,500 sqm (50m/tile, DIMS convention) —
  //   a genuine skyscraper-cluster footprint, not a single building, mirroring
  //   res_tower_sgp's "whole precinct in one placeable" abstraction.
  //
  // Jobs/tile (the density figure this feature exists to fix):
  //   off_towers_downtown (previous ceiling): 2,000 / 25 tiles = 80/tile
  //   off_tower_canary:                       10,000 / 81 tiles ≈ 123.5/tile
  //   off_tower_marina:                       20,000 / 81 tiles ≈ 246.9/tile
  //   res_tower_sgp (housing ceiling, for comparison): 20,000 / 81 = 247/tile
  //   — off_tower_marina now matches the housing ceiling's density almost
  //   exactly (246.9 vs 247 residents/tile), closing the 3x asymmetry this
  //   feature was filed to fix.
  //
  // Unlock gated above the whole Offices family's current ceiling
  // (off_towers_downtown = 14), in the same capstone band the residential
  // towers use (16-20) — smaller/first tower unlocks before the bigger/
  // second, exactly like res_tower_nyc(16) before res_tower_sgp(18).
  // ⚠ BALANCE-NUMBER PLACEHOLDER — every cost/upkeep/jobs figure here is
  // directional only, pending Aaron's row-by-row balance pass (house
  // convention, same disclaimer as res_tower_nyc/res_tower_sgp above).
  off_tower_canary: P('off_tower_canary', 'office', 'Canary Wharf Financial Towers', 'Canary Wharf-scale financial district · ≈ 10,000 jobs · 9×9', 9, 9, 708333333, 3500, '#1f6f57', 'zones', 17, { jobs: 10000, capacityTiers: tierLadder(10000) }),
  off_tower_marina: P('off_tower_marina', 'office', 'Marina Bay Financial Hub', 'Marina Bay-scale financial hub · ≈ 20,000 jobs · 9×9', 9, 9, 1280000000, 6300, '#1f6f57', 'zones', 19, { jobs: 20000, capacityTiers: tierLadder(20000) }),

  ind_estate: P('ind_estate', 'industrial', 'Industrial Estate', 'Heavy industrial estate · ≈ 18 factories · ICI-Wilton scale', 6, 6, 180000000, 900, '#a371f7', 'zones', 11, { tag: 'pollution', jobs: 2000, capacityTiers: [2000, 2200, 2420, 2662, 2928, 3221, 3543, 3897, 4287, 4716] }),
  farm_estate: P('farm_estate', 'industrial', 'Farm Estate', 'Large integrated farm · mixed crop + livestock', 6, 6, 2400000, 15, '#7da24f', 'zones', 12, { jobs: 1500, capacityTiers: [1500, 1650, 1815, 1996, 2196, 2416, 2657, 2923, 3215, 3536] }),

  // ---- Retail ----
  // FEAT-1972079900 inc1 — the RETAIL estate (out-of-town shopping / retail park).
  // Graduated from a placeholder into a real, placeable estate-scale retail object
  // carrying the AGGREGATE retail jobs of an out-of-town superstore. Same DATA-only
  // treatment as the other estates above (no mw, no water tag). PLACEHOLDER-balance.
  com_hypermarket: P('com_hypermarket', 'commercial', 'Hypermarket', 'Out-of-town retail estate · ≈ 20 shops', 5, 5, 90000000, 480, '#e3b341', 'zones', 10, { jobs: 800 }),
  com_discounter: PH('com_discounter', 'commercial', 'Discount Store', 'Planned — value discount retailer', 2, 2, '#d9a52e', 'zones', 5),
  com_darkstore: PH('com_darkstore', 'commercial', 'Dark Store', 'Planned — online-only fulfilment store', 2, 2, '#c99a2a', 'zones', 9),

  // ---- Industry & Farms ----
  ind_chemworks: PH('ind_chemworks', 'industrial', 'Chemical Works', 'Planned — heavy chemical plant', 3, 3, '#8957d9', 'zones', 10),
  ind_refinery: PH('ind_refinery', 'industrial', 'Oil Refinery', 'Planned — petroleum refinery complex', 4, 4, '#7d4fc9', 'zones', 13),
  ind_fulfilment: PH('ind_fulfilment', 'industrial', 'Fulfilment Centre', 'Planned — automated fulfilment warehouse', 3, 3, '#9a6ee0', 'zones', 11),
  ind_parcelhub: PH('ind_parcelhub', 'industrial', 'Parcel Hub', 'Planned — parcel sortation hub', 2, 2, '#9a6ee0', 'zones', 9),
  farm_dairy: PH('farm_dairy', 'industrial', 'Dairy', 'Planned — dairy processing farm', 2, 2, '#7da24f', 'zones', 3),
  farm_abattoir: PH('farm_abattoir', 'industrial', 'Abattoir', 'Planned — livestock abattoir', 2, 2, '#8a6d3b', 'zones', 5),
  harbour_fishing: PH('harbour_fishing', 'industrial', 'Fishing Harbour', 'Planned — coastal fishing harbour', 3, 2, '#5e8bb0', 'zones', 8),

  // ---- Mining ----
  mine_chalk: PH('mine_chalk', 'mine', 'Chalk Pit', 'Planned — chalk extraction pit', 2, 2, '#b08d55', 'zones', 4),
  mine_clay: PH('mine_clay', 'mine', 'Clay Pit', 'Planned — clay extraction pit', 2, 2, '#a37f4a', 'zones', 5),
  mine_coal: PH('mine_coal', 'mine', 'Deep Coal Mine', 'Planned — deep-shaft coal mine', 3, 3, '#9c6f3f', 'zones', 7),

  // ---- Leisure ----
  lei_gym: PH('lei_gym', 'leisure', 'Gym', 'Planned — fitness and gym centre', 1, 1, '#e07be0', 'services', 3),
  lei_sportsground: PH('lei_sportsground', 'leisure', 'Sports Ground', 'Planned — playing fields and pavilion', 2, 2, '#d06fd0', 'services', 4),
  lei_stables: PH('lei_stables', 'leisure', 'Stables', 'Planned — riding stables and paddock', 2, 2, '#c95fc9', 'services', 6),

  // ---- Power ----
  // FEAT-1972079901: pow_hydro (Five Gorges Dam) GRADUATED to a real placeable
  // mega-hydro generator above (in the Power additions block). pow_hvdc (a
  // TRANSMISSION link, not a generator) and pow_reprocess (fuel reprocessing, not
  // a generator) stay placeholders — giving either an `mw` would be a false grid
  // generation leak, so they are intentionally NOT graduated here.
  pow_hvdc: PH('pow_hvdc', 'power', 'HVDC Interconnector', 'Planned — long-distance DC power link', 2, 2, '#ff7b72', 'services', 14),
  pow_reprocess: PH('pow_reprocess', 'power', 'THORP Reprocessing Plant', 'Planned — nuclear fuel reprocessing plant', 4, 4, '#e05d38', 'services', 18),

  // ---- Water & Waste ----
  // FEAT-1972079906 inc1: waste_depot GRADUATED (refuse COLLECTION).
  // FEAT-1972079906 inc2: the four PROCESSING specs GRADUATE from roadmap
  // placeholders to real placeable buildings, each carrying a `processCapacity`
  // (tonnes/tick of collected refuse it can take) that drives processingMixOf().
  // No `tag` (they are not clean/waste-WATER plants). The EfW plant carries NO
  // static `mw`: its grid contribution is THROUGHPUT-based (efwPowerOf), so an
  // idle incinerator produces no power. ⚠ cost / upkeep / processCapacity are all
  // PLACEHOLDER-balance — Aaron's row-by-row pass.
  waste_landfill: P('waste_landfill', 'water', 'Landfill', 'Buries residual refuse · cheap, finite · 300 t/tick', 3, 3, 9000000, 500, '#5f7f66', 'services', 5, { processCapacity: 300 }),
  waste_incinerator: P('waste_incinerator', 'water', 'Energy-from-Waste', 'Burns residual for grid power · 60 t/tick', 3, 3, 75600000, 4200, '#6b8f71', 'services', 9, { processCapacity: 60 }),
  waste_recycling: P('waste_recycling', 'water', 'Recycling Centre', 'MRF recovers materials for sale · 40 t/tick', 2, 2, 14400000, 800, '#5f9e6a', 'services', 6, { processCapacity: 40 }),
  waste_compost: P('waste_compost', 'water', 'Composting Site', 'Turns organics into compost · 30 t/tick', 2, 2, 6300000, 350, '#6b9e6b', 'services', 5, { processCapacity: 30 }),

  // ---- Health & Deathcare ----
  death_cemetery: PH('death_cemetery', 'health', 'Cemetery', 'Planned — municipal cemetery', 3, 3, '#8a94a8', 'services', 4),
  death_crematorium: PH('death_crematorium', 'health', 'Crematorium', 'Planned — crematorium and gardens', 2, 2, '#8a94a8', 'services', 7),
  air_heliport: PH('air_heliport', 'health', 'Air-Ambulance Pad', 'Planned — air-ambulance helipad', 1, 1, '#ff7b72', 'services', 8),

  // ---- Fire & Rescue ----
  air_fire_helibase: PH('air_fire_helibase', 'fire', 'Fire Heli Base', 'Planned — aerial firefighting base', 2, 2, '#f65b56', 'services', 10),

  // ---- Police & Justice ----
  air_police_helibase: PH('air_police_helibase', 'police', 'Police Heli Base', 'Planned — police air-support base', 2, 2, '#6e7bd9', 'services', 10),

  // ---- Landmarks ----
  land_containerport: PH('land_containerport', 'landmark', 'Container Port', 'Planned — deep-water container port', 4, 4, '#5e8bb0', 'services', 12),
  land_ferryterminal: PH('land_ferryterminal', 'landmark', 'Ferry Terminal', 'Planned — cross-channel ferry terminal', 3, 3, '#4a9dae', 'services', 10),
  land_cern: PH('land_cern', 'landmark', 'Particle Accelerator', 'Planned — underground particle accelerator ring', 5, 5, '#a371f7', 'services', 19),
  land_gigafactory: PH('land_gigafactory', 'landmark', 'Gigafactory', 'Planned — battery gigafactory', 5, 5, '#43aa8b', 'services', 15),
  land_semifab: PH('land_semifab', 'landmark', 'Semiconductor Fab', 'Planned — semiconductor fabrication plant', 4, 4, '#2f8f74', 'services', 16),

  tour_greatwall: PH('tour_greatwall', 'landmark', 'Great Wall', 'Planned — world attraction', 5, 5, '#d0a83c', 'services', 20),
  tour_colosseum: PH('tour_colosseum', 'landmark', 'Colosseum', 'Planned — world attraction', 3, 3, '#d0a83c', 'services', 20),
  tour_tajmahal: PH('tour_tajmahal', 'landmark', 'Taj Mahal', 'Planned — world attraction', 3, 3, '#d0a83c', 'services', 20),
  tour_machupicchu: PH('tour_machupicchu', 'landmark', 'Machu Picchu', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_petra: PH('tour_petra', 'landmark', 'Petra', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_giza: PH('tour_giza', 'landmark', 'Great Pyramid', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_eiffel: PH('tour_eiffel', 'landmark', 'Eiffel Tower', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_liberty: PH('tour_liberty', 'landmark', 'Statue of Liberty', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_grandcanyon: PH('tour_grandcanyon', 'landmark', 'Grand Canyon rim', 'Planned — world attraction', 5, 5, '#d0a83c', 'services', 20),
  tour_niagara: PH('tour_niagara', 'landmark', 'Niagara Falls', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_angkor: PH('tour_angkor', 'landmark', 'Angkor Wat', 'Planned — world attraction', 5, 5, '#d0a83c', 'services', 20),
  tour_stonehenge: PH('tour_stonehenge', 'landmark', 'Stonehenge', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_acropolis: PH('tour_acropolis', 'landmark', 'Acropolis', 'Planned — world attraction', 3, 3, '#d0a83c', 'services', 20),
  tour_redeemer: PH('tour_redeemer', 'landmark', 'Christ the Redeemer', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_sagrada: PH('tour_sagrada', 'landmark', 'Sagrada Família', 'Planned — world attraction', 3, 3, '#d0a83c', 'services', 20),
  tour_forbidden: PH('tour_forbidden', 'landmark', 'Forbidden City', 'Planned — world attraction', 5, 5, '#d0a83c', 'services', 20),
  tour_stpeters: PH('tour_stpeters', 'landmark', 'St Peter\'s', 'Planned — world attraction', 3, 3, '#d0a83c', 'services', 20),
  tour_alhambra: PH('tour_alhambra', 'landmark', 'Alhambra', 'Planned — world attraction', 3, 3, '#d0a83c', 'services', 20),
  tour_chichenitza: PH('tour_chichenitza', 'landmark', 'Chichén Itzá', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_fuji: PH('tour_fuji', 'landmark', 'Mount Fuji station', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_opera: PH('tour_opera', 'landmark', 'Sydney Opera House', 'Planned — world attraction', 3, 3, '#d0a83c', 'services', 20),
  tour_goldengate: PH('tour_goldengate', 'landmark', 'Golden Gate', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_louvre: PH('tour_louvre', 'landmark', 'Louvre', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_santorini: PH('tour_santorini', 'landmark', 'Caldera town', 'Planned — world attraction', 3, 3, '#d0a83c', 'services', 20),
  tour_venice: PH('tour_venice', 'landmark', 'Canal quarter', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_neuschwanstein: PH('tour_neuschwanstein', 'landmark', 'Neuschwanstein', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_burj: PH('tour_burj', 'landmark', 'Supertall observatory', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_iguazu: PH('tour_iguazu', 'landmark', 'Iguazú Falls', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_banff: PH('tour_banff', 'landmark', 'Alpine lake park', 'Planned — world attraction', 5, 5, '#d0a83c', 'services', 20),
  tour_aurora: PH('tour_aurora', 'landmark', 'Aurora lodge', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_reef: PH('tour_reef', 'landmark', 'Reef visitor centre', 'Planned — world attraction', 3, 3, '#d0a83c', 'services', 20),
  tour_yellowstone: PH('tour_yellowstone', 'landmark', 'Geyser park', 'Planned — world attraction', 5, 5, '#d0a83c', 'services', 20),
  tour_serengeti: PH('tour_serengeti', 'landmark', 'Safari lodge', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_fushimi: PH('tour_fushimi', 'landmark', 'Fushimi Inari', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_prague: PH('tour_prague', 'landmark', 'Castle quarter', 'Planned — world attraction', 3, 3, '#d0a83c', 'services', 20),
  tour_dubrovnik: PH('tour_dubrovnik', 'landmark', 'Walled city', 'Planned — world attraction', 3, 3, '#d0a83c', 'services', 20),
  tour_cappadocia: PH('tour_cappadocia', 'landmark', 'Cappadocia', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_moai: PH('tour_moai', 'landmark', 'Moai field', 'Planned — world attraction', 3, 3, '#d0a83c', 'services', 20),
  tour_uluru: PH('tour_uluru', 'landmark', 'Uluru', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_tablemountain: PH('tour_tablemountain', 'landmark', 'Table Mountain', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_hallstatt: PH('tour_hallstatt', 'landmark', 'Lakeside village', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_antelope: PH('tour_antelope', 'landmark', 'Slot canyon', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_halong: PH('tour_halong', 'landmark', 'Ha Long Bay', 'Planned — world attraction', 5, 5, '#d0a83c', 'services', 20),
  tour_zhangjiajie: PH('tour_zhangjiajie', 'landmark', 'Pillar peaks', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_matterhorn: PH('tour_matterhorn', 'landmark', 'Matterhorn', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_towerlondon: PH('tour_towerlondon', 'landmark', 'Tower of London', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_versailles: PH('tour_versailles', 'landmark', 'Versailles', 'Planned — world attraction', 4, 4, '#d0a83c', 'services', 20),
  tour_montstmichel: PH('tour_montstmichel', 'landmark', 'Mont-Saint-Michel', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_giantscauseway: PH('tour_giantscauseway', 'landmark', 'Giant\'s Causeway', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),
  tour_edinburgh: PH('tour_edinburgh', 'landmark', 'Edinburgh Castle', 'Planned — world attraction', 2, 2, '#d0a83c', 'services', 20),

  stay_bb: PH('stay_bb', 'leisure', 'B&B / guesthouse', 'Planned — visitor beds', 1, 1, '#e07be0', 'services', 20),
  stay_hotel: PH('stay_hotel', 'leisure', 'City hotel', 'Planned — visitor beds', 2, 2, '#e07be0', 'services', 20),
  stay_luxury: PH('stay_luxury', 'leisure', 'Luxury hotel', 'Planned — visitor beds', 3, 2, '#e07be0', 'services', 20),
  stay_resort: PH('stay_resort', 'leisure', 'Resort campus', 'Planned — visitor beds', 4, 4, '#e07be0', 'services', 20),
  stay_caravan: PH('stay_caravan', 'leisure', 'Caravan & camping', 'Planned — visitor beds', 3, 3, '#e07be0', 'services', 20),
  // ═══════════════ end FEAT-1972079877 roadmap placeholders ═══════════════
};

for (const [id, d] of Object.entries(DIMS)) {
  const sp = SPECS[id];
  if (sp) sp.dims = d;
}

/**
 * BUG-511 (P3): capacityAtTier's no-capacityTiers fallback is
 * `sp.residents ?? sp.jobs ?? 0` — for a residential spec that omits both
 * `capacityTiers` AND `residents`, that resolves to a SILENT 0, which would
 * freeze the population ceiling for every building of that type (see BUG-509
 * for what a wrong ceiling value does to city growth) with no error, no log,
 * nothing — a classic silent-zero-capacity trap. Every one of today's 10
 * residential specs (res_hut .. res_estate_sprawl) defines `residents`, so
 * the trap is harmless TODAY, but nothing stops a FUTURE residential spec
 * from being added without it.
 *
 * GR#15 (validators derive from data, never a hardcoded expected count) +
 * GR#7 (registry-sourced errors only) fix: rather than inventing a magic
 * non-zero fallback number that could silently change city behaviour the
 * moment a future spec fails to override it, this asserts the INVARIANT
 * itself at catalogue-load time — every 'residential' spec MUST declare
 * `residents` — and throws the registry error MET-V852
 * (ResidentialSpecMissingResidents) the instant that invariant is violated.
 * A missing field becomes a loud crash on load, never a quiet 0.
 *
 * Exported so tests can call it directly against a synthetic catalogue
 * (rather than only observing the module-load-time throw below, which would
 * require a subprocess to exercise a second time per test file).
 */
export function assertResidentialSpecsHaveResidents(specs: Record<string, Spec>): void {
  for (const [id, sp] of Object.entries(specs)) {
    if (sp.kind === 'residential' && !sp.placeholder && sp.residents == null) {
      throw codedError(
        'MET-V852',
        `Residential zone spec '${id}' has no 'residents' field -- capacityAtTier would silently fall back to 0, freezing the population ceiling for this building type.`
      );
    }
  }
}

// Run the guard now, at catalogue-load time, against the real SPECS table —
// a future residential spec added without `residents` fails to even IMPORT
// this module, rather than shipping a silent-zero capacity trap.
assertResidentialSpecsHaveResidents(SPECS);

// FEAT-1972079877: the old 9-family palette is regrouped so each family shows a
// realistic, populated count. Ordering within a family is by unlock level, so
// the tree doubles as a preview of the level ladder. Every id here MUST exist
// in SPECS and appear in exactly ONE family (BUG-385 class — enforced by
// test/catalogue.test.mjs).
//
// FEAT-1972079877 roadmap placeholders are appended to the END of their existing
// family's item list, so they render greyed-out "coming soon" beneath the real,
// placeable types. Every placeholder appears in exactly ONE family (the same
// BUG-385 uniqueness rule catalogue.test.mjs enforces for real specs).
export const PALETTE: { title: string; items: string[] }[] = [
  { title: 'Network', items: ['road', 'rd_avenue', 'rd_aroad', 'rd_dual', 'rail_branch'] },
  { title: 'Transport', items: ['bus_stop', 'bus_depot', 'car_park', 'station_ashford', 'bus_station', 'tram_depot', 'ferry_pier', 'metro_station', 'grand_terminus', 'ev_charging_hub', 'trans_parkride', 'rail_freightyard', 'trans_interchange'] },
  { title: 'Housing', items: ['res_hut', 'res_block', 'res_terrace', 'res_lowrise', 'res_midrise', 'res_highrise', 'res_penthouse', 'res_estate_compact', 'res_estate', 'res_estate_sprawl', 'res_tower_nyc', 'res_tower_sgp'] },
  { title: 'Retail', items: ['com_shop', 'com_retail', 'com_market', 'com_super', 'com_mall', 'com_discounter', 'com_hypermarket', 'com_darkstore'] },
  { title: 'Industry & Farms', items: ['farm_wheat', 'farm_cattle', 'farm_orchard', 'ind_factory', 'ind_light', 'ind_warehouse', 'ind_heavy', 'ind_cement', 'ind_logistics', 'farm_dairy', 'farm_abattoir', 'farm_estate', 'ind_estate', 'harbour_fishing', 'ind_parcelhub', 'ind_chemworks', 'ind_fulfilment', 'ind_refinery'] },
  { title: 'Offices', items: ['off_suite', 'off_tower', 'off_data', 'off_businesspark', 'off_towers_downtown', 'off_tower_canary', 'off_tower_marina'] },
  { title: 'Mining', items: ['mine_quarry', 'mine_deep', 'mine_chalk', 'mine_clay', 'mine_coal'] },
  { title: 'Parks', items: ['park', 'park_playground', 'park_town', 'park_botanical', 'park_nature'] },
  { title: 'Leisure', items: ['lei_leisure', 'lei_cinema', 'lei_theatre', 'lei_museum', 'lei_arena', 'lei_themepark', 'lei_gym', 'lei_sportsground', 'lei_stables'] },
  { title: 'Power', items: ['pow_wind', 'pow_coal', 'pow_substation', 'pow_nuke', 'pow_solar', 'pow_windfarm', 'pow_ccgt', 'pow_offshore', 'pow_hydro', 'pow_fusion', 'pow_hvdc', 'pow_reprocess'] },
  { title: 'Water & Waste', items: ['wat_tower', 'wat_clean', 'wat_waste', 'wat_reservoir', 'wat_sewage_regional', 'waste_depot', 'waste_compost', 'waste_recycling', 'waste_landfill', 'waste_incinerator'] },
  { title: 'Health', items: ['hea_clinic', 'hea_hospital', 'hea_ambulance', 'hea_eldercare', 'hea_teaching', 'death_cemetery', 'death_crematorium', 'air_heliport'] },
  { title: 'Police & Justice', items: ['pol_station', 'civ_courthouse', 'pol_hq', 'civ_prison', 'civ_adx', 'air_police_helibase'] },
  { title: 'Fire & Rescue', items: ['fire_post', 'fire_station', 'fire_hq', 'air_fire_helibase'] },
  { title: 'Education', items: ['edu_nursery', 'edu_nursery_city', 'edu_primary', 'edu_city', 'col_sixth', 'uni', 'edu_tech'] },
  { title: 'Civic', items: ['civ_library', 'civ_townhall', 'civ_cityhall'] },
  { title: 'Landmarks', items: ['land_stadium', 'land_airport', 'land_harbour', 'land_cathedral', 'land_eye', 'land_tunnel', 'land_space', 'land_ferryterminal', 'land_containerport', 'land_gigafactory', 'land_semifab', 'land_cern'] },
  { title: 'Tourism', items: ['tour_greatwall', 'tour_colosseum', 'tour_tajmahal', 'tour_machupicchu', 'tour_petra', 'tour_giza', 'tour_eiffel', 'tour_liberty', 'tour_grandcanyon', 'tour_niagara', 'tour_angkor', 'tour_stonehenge', 'tour_acropolis', 'tour_redeemer', 'tour_sagrada', 'tour_forbidden', 'tour_stpeters', 'tour_alhambra', 'tour_chichenitza', 'tour_fuji', 'tour_opera', 'tour_goldengate', 'tour_louvre', 'tour_santorini', 'tour_venice', 'tour_neuschwanstein', 'tour_burj', 'tour_iguazu', 'tour_banff', 'tour_aurora', 'tour_reef', 'tour_yellowstone', 'tour_serengeti', 'tour_fushimi', 'tour_prague', 'tour_dubrovnik', 'tour_cappadocia', 'tour_moai', 'tour_uluru', 'tour_tablemountain', 'tour_hallstatt', 'tour_antelope', 'tour_halong', 'tour_zhangjiajie', 'tour_matterhorn', 'tour_towerlondon', 'tour_versailles', 'tour_montstmichel', 'tour_giantscauseway', 'tour_edinburgh'] },
  { title: 'Stay', items: ['stay_bb', 'stay_hotel', 'stay_luxury', 'stay_resort', 'stay_caravan'] },
];

export const PALETTE_FLAT: string[] = PALETTE.flatMap((g) => g.items);

// FEAT-2326609748: domain groupings for the build-palette information strip
// at the bottom of the screen (BottomBar.tsx's "tree-fams" column). Aaron
// (2026-09-02): "power / water / waste are all utilities, we need one for
// education, and one for health, one for industry etc" — instead of one flat
// list of PALETTE.title families.
//
// GR#15 (validators/data derive from data): this is a PURE LOOKUP keyed off
// PALETTE's existing `title` field (itself derived from each spec's `kind`
// via the family groupings above) — it does NOT re-list individual spec ids,
// so a new spec added to an existing family is picked up automatically with
// no change here. Only a brand-new FAMILY (a new PALETTE title) needs a line
// added to this map; PALETTE_DOMAIN_COMPLETE (below) proves at test time that
// every family is covered so a missed one fails loudly instead of silently
// vanishing from the UI.
export const PALETTE_DOMAIN: Record<string, string> = {
  Power: 'Utilities',
  'Water & Waste': 'Utilities',
  Education: 'Education',
  Health: 'Health',
  'Industry & Farms': 'Industry & Economy',
  Mining: 'Industry & Economy',
  Offices: 'Industry & Economy',
  Retail: 'Industry & Economy',
  'Police & Justice': 'Safety',
  'Fire & Rescue': 'Safety',
  Housing: 'Housing',
  Network: 'Transport',
  Transport: 'Transport',
  Parks: 'Leisure & Tourism',
  Leisure: 'Leisure & Tourism',
  Landmarks: 'Leisure & Tourism',
  Tourism: 'Leisure & Tourism',
  Stay: 'Leisure & Tourism',
  Civic: 'Civic',
};

// Display order for domain group headers. A family whose title is missing
// from PALETTE_DOMAIN falls into 'General' (see domainOfFamily below) rather
// than being dropped from the UI — 'General' is always last.
export const PALETTE_DOMAIN_ORDER: string[] = [
  'Utilities',
  'Education',
  'Health',
  'Industry & Economy',
  'Safety',
  'Housing',
  'Transport',
  'Leisure & Tourism',
  'Civic',
  'General',
];

const UNMAPPED_DOMAIN = 'General';

/** Domain for one PALETTE family title — 'General' for anything unmapped, so a
 * new family can never silently disappear from the grouped UI (it just lands
 * in the catch-all bucket until PALETTE_DOMAIN is updated). */
export function domainOfFamily(title: string): string {
  return PALETTE_DOMAIN[title] ?? UNMAPPED_DOMAIN;
}

/** PALETTE regrouped by domain, in PALETTE_DOMAIN_ORDER, each entry keeping
 * its families in their original PALETTE order. Domains with no families
 * present (e.g. 'General' when every family is mapped) are omitted. */
export function paletteByDomain(): { domain: string; families: typeof PALETTE }[] {
  const byDomain = new Map<string, typeof PALETTE>();
  for (const fam of PALETTE) {
    const d = domainOfFamily(fam.title);
    const list = byDomain.get(d) ?? [];
    list.push(fam);
    byDomain.set(d, list);
  }
  return PALETTE_DOMAIN_ORDER.filter((d) => byDomain.has(d)).map((d) => ({ domain: d, families: byDomain.get(d)! }));
}

export const FAMILIES: { kind: ZoneKind; label: string; color: string }[] = [
  { kind: 'road', label: 'Roads', color: '#4a525c' },
  { kind: 'residential', label: 'Housing', color: '#4c9aff' },
  { kind: 'commercial', label: 'Commercial', color: '#e3b341' },
  { kind: 'office', label: 'Offices', color: '#43aa8b' },
  { kind: 'industrial', label: 'Industry & Farms', color: '#a371f7' },
  { kind: 'mine', label: 'Mining', color: '#b08d55' },
  { kind: 'park', label: 'Parks', color: '#3fb950' },
  { kind: 'power', label: 'Power', color: '#f0883e' },
  { kind: 'water', label: 'Water & Waste', color: '#39c5cf' },
  { kind: 'health', label: 'Health', color: '#ff7b72' },
  { kind: 'police', label: 'Police', color: '#6e7bd9' },
  { kind: 'school', label: 'Education', color: '#ffd166' },
  { kind: 'landmark', label: 'Landmarks', color: '#d0a83c' },
  // FEAT-1972079877 placeholder catalogue families:
  { kind: 'transport', label: 'Transport', color: '#5ea0c8' },
  { kind: 'fire', label: 'Fire & Rescue', color: '#f65b56' },
  { kind: 'civic', label: 'Civic & Justice', color: '#8a94a8' },
  { kind: 'leisure', label: 'Leisure', color: '#e07be0' },
];

const ZERO_COUNTS: Record<ZoneKind, number> = {
  road: 0,
  motorway: 0,
  rail: 0,
  station: 0,
  pylon: 0,
  residential: 0,
  commercial: 0,
  office: 0,
  industrial: 0,
  mine: 0,
  park: 0,
  power: 0,
  water: 0,
  health: 0,
  police: 0,
  school: 0,
  landmark: 0,
  transport: 0,
  fire: 0,
  civic: 0,
  leisure: 0,
};

export function countByKind(buildings: SimState['buildings']): Record<ZoneKind, number> {
  const c = { ...ZERO_COUNTS };
  for (const b of buildings) {
    const sp = SPECS[b.spec];
    if (sp) c[sp.kind]++;
  }
  return c;
}

// BUG-520 (remaining part) — an OFFLINE / road-disconnected building already
// contributes zero power/waste/upkeep (powerStats/sumBy/wasteGeneratedOf); it
// must ALSO contribute zero to business/freight/office TAX (computeFlows) and
// zero to growth DEMAND (demandOf) — the exact same activation gate, applied
// to the per-kind counts those two paths derive from. plain countByKind()
// stays ungated on purpose: debug/save "byKind" totals (debugjson.ts,
// snapshot.ts) and the build-advisor/milestone UI legitimately want the count
// of everything PLACED, online or not. Mirror the powerStats()/sumBy() gate
// exactly: `if (!isOnline(s, b)) continue;` inside the building loop.
// Order-independent fold, no map-range-with-break (GR#21).
// FEAT-webworker-sim-offload Stage 0 (2026-09-02): memoised (memoOnState,
// defined below — a hoisted function declaration, safe to reference from a
// textually-earlier call site) because computeFlows (engine.ts) calls this
// 3x per invocation on the SAME unchanged state (once for Business/Freight
// Tax, once for the Office Tax split, once for the harbour-boosted Freight
// Tax recompute) — one real O(buildings) pass instead of three.
export const countByKindOnline: (s: SimState) => Record<ZoneKind, number> = memoOnState((s) => {
  const c = { ...ZERO_COUNTS };
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp) c[sp.kind]++;
  }
  return c;
});

/**
 * FEAT-1972079878 inc1 (AC-5): total residents capacity, including auto-scaled tiers.
 * For each residential building, capacity = capacityAtTier(sp, building.capacityTier ?? 0).
 * Respects ongoing capacity upgrades triggered by auto-scale.
 */
// BUG-622: memoised — MapView's draw pass calls blockOccupancy/utilisationOf
// per building, each of which called this full-city scan fresh, turning the
// draw loop O(n^2) (~14s per redraw at 13k buildings, measured). Pure
// derivation of s, so memoOnState is exact.
export const residentsCapacity: (s: SimState) => number = memoOnState((s) => {
  let cap = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind === 'residential') cap += capacityAtTier(sp, b.capacityTier ?? 0);
  }
  return cap;
});

/**
 * FEAT-1972079878 inc1 (AC-5): online residents capacity, including auto-scaled tiers.
 * Same as residentsCapacity, but excludes offline buildings (per isOnline gate).
 */
// BUG-622: memoised — same per-building-caller O(n^2) class as residentsCapacity.
export const onlineResidentsCapacity: (s: SimState) => number = memoOnState((s) => {
  let cap = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.kind === 'residential') cap += capacityAtTier(sp, b.capacityTier ?? 0);
  }
  return cap;
});

/**
 * BUG-645 — count AND capacity of RESIDENTIAL buildings withheld ONLY
 * because they are still under construction (the G1 gate in
 * computeIsOnline/computeFailedGates), so the TopBar at-capacity indicator
 * can name the concrete relief already queued: "N homes under construction
 * adding M capacity when they finish". Reuses computeFailedGates() — the SAME
 * classification offlineResidentsByReason() below partitions into its
 * 'construction' bucket — so this NEVER drifts from that already-shipped
 * number (GR#3 SSOT): for any state, this function's `capacity` is exactly
 * offlineResidentsByReason(s).construction, just paired with a building
 * COUNT that selector does not track.
 *
 * Pure derivation of s (GR#21): no Date/Math.random. memoOnState (the
 * BUG-642/BUG-622 idiom this file already uses throughout) because TopBar
 * reads this every render on Aaron's 29,831-building city — an unmemoised
 * per-building scan here would repeat the full O(buildings) walk every tick
 * exactly the class of cost BUG-642/BUG-622 exist to prevent. The inner
 * computeFailedGates() call only runs for buildings isOnline() has already
 * gated OFFLINE (a small minority — 56 of 29,831 on Aaron's city), so the
 * memoised pass stays cheap.
 */
export const residentialConstructionSummary: (s: SimState) => { count: number; capacity: number } = memoOnState(
  (s) => {
    let count = 0;
    let capacity = 0;
    for (const b of s.buildings) {
      const sp = SPECS[b.spec];
      if (sp?.kind !== 'residential') continue;
      if (isOnline(s, b)) continue;
      const gates = computeFailedGates(s, b);
      if (gates.some((g) => g.gate === 'construction')) {
        count++;
        capacity += sp.residents ?? 8;
      }
    }
    return { count, capacity };
  }
);

/**
 * BUG-417 — housing capacity that is NOT yet live: residents that will come
 * online once currently offline / under-construction dwellings finish. Pure
 * derivation (GR#21): gross residential capacity minus the online-gated figure
 * engine growth can actually fill. Kept as a display-layer split so the "Housing
 * cap" readout can show the honest online headline plus a "+N under
 * construction" breakdown WITHOUT changing residentsCapacity (other callers may
 * want the gross total). Never negative — online ⊆ gross by construction.
 */
export function underConstructionResidents(s: SimState): number {
  return Math.max(0, residentsCapacity(s) - onlineResidentsCapacity(s));
}

/**
 * BUG-394 — split the OFFLINE residential capacity (the "+N" from
 * underConstructionResidents) by its ROOT CAUSE, so the display can distinguish
 * the self-resolving reason from the actionable one:
 *
 *   • construction  — the dwelling is genuinely still being built (G1). Fills
 *                     itself once construction finishes; the player need do
 *                     nothing.
 *   • disconnected  — the dwelling is built but sits off the road network
 *                     (G2 not road-adjacent / G3 not road-connected). It will
 *                     NEVER house anyone until the player connects roads. This
 *                     is the legible signal for the BUG-394 pop-freeze: a city
 *                     that looks full but is pinned at online capacity because
 *                     densely-placed dwellings were never wired to a road.
 *
 * Attribution reuses the committed activation gate (computeFailedGates) — it does
 * NOT reimplement or change the gate. computeFailedGates reports construction OR a
 * road gate but never both (it returns early while a building is still under
 * construction, because the road gates aren't meaningful yet). So the
 * both-gates-fail tie-break is resolved by the gate itself: while a dwelling is
 * still building it is 'construction'; only once built does a road failure
 * surface. If any failed gate is a road gate we count 'disconnected' (the
 * more-actionable cause); otherwise 'construction'.
 *
 * Pure derivation (GR#21): a function of SimState only, no Date/Math.random. The
 * two buckets partition exactly the offline residential capacity, so
 * `construction + disconnected === underConstructionResidents(s)` — every offline
 * resident is attributed once, none lost or double-counted.
 */
export function offlineResidentsByReason(s: SimState): {
  construction: number;
  disconnected: number;
} {
  let construction = 0;
  let disconnected = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind !== 'residential') continue;
    if (isOnline(s, b)) continue;
    const residents = sp.residents ?? 8;
    const gates = computeFailedGates(s, b);
    const road = gates.some((g) => g.gate === 'road-adjacent' || g.gate === 'road-connected');
    if (road) disconnected += residents;
    else construction += residents;
  }
  return { construction, disconnected };
}

export interface StationLinkInfo {
  total: number;
  connectedIds: Set<number>;
}

// BUG-643 — memoOnState. stationLinks() is called from FOUR separate sites in
// engine.ts (approvalOf, wellbeingOf, computeFlows, and the resolveDemand path
// — grepped, none of them cache their own result) plus lineUsageOf() below in
// this file, each re-walking the ENTIRE buildings array twice (once to collect
// roads, once to scan stations) on every call. Profiled at 228ms self time on
// Aaron's 29,831-building city. Pure function of s.buildings only, so the
// state-identity key is sound by the same reasoning as every other memo in
// this file — and because engine.ts is not edited by this fix, every existing
// call site benefits automatically the moment this export starts returning a
// cached answer for an unchanged state object.
export const stationLinks: (s: SimState) => StationLinkInfo = memoOnState((s) => {
  const roads = new Set<string>();
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind === 'road') roads.add(`${b.x},${b.y}`);
  }
  const connectedIds = new Set<number>();
  let total = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp || sp.kind !== 'station') continue;
    total++;
    let linked = false;
    for (let dx = 0; dx < sp.w && !linked; dx++) {
      for (let dy = 0; dy < sp.h && !linked; dy++) {
        const x = b.x + dx;
        const y = b.y + dy;
        if (
          roads.has(`${x + 1},${y}`) ||
          roads.has(`${x - 1},${y}`) ||
          roads.has(`${x},${y + 1}`) ||
          roads.has(`${x},${y - 1}`)
        ) {
          linked = true;
        }
      }
    }
    if (linked) connectedIds.add(b.id);
  }
  return { total, connectedIds };
});

// ════════════════════════════════════════════════════════════════════════════
// FEAT-1972079902 — RAIL NETWORK inc1: LINE CAPACITY + COMMUTER-FLOW USAGE +
// SATURATION. Display/metrics ONLY — nothing here is wired into the tick, mutates
// state, or costs money (trains = inc2, the auto-branch router = inc3). Every
// function below is PURE and DETERMINISTIC (GR#21): no Date.now / Math.random, no
// map-iteration-with-break, strict (x,y) ordering — identical states produce
// byte-identical usage. Usage is a DERIVED read-out (never stored in SimState),
// so it cannot drift or break the consistency checker / genesis-replay.
//
// ⚠ BALANCE-NUMBER REGIME (Aaron's blanket rule): the rail/hs1 per-tile
// throughput figures and the commuter-flow coefficients below are PLACEHOLDERS —
// directional only, pending Aaron's row-by-row balance pass. road/m20 capacity is
// NOT redefined here — it is REUSED from ROAD_TIER_CAPACITY.
// ════════════════════════════════════════════════════════════════════════════

/**
 * Per-tile commuter throughput for the RAIL line classes (people/tick a single
 * tile of the line can carry). road/m20 are deliberately absent — they reuse
 * ROAD_TIER_CAPACITY via lineCapacityOf. ⚠ PLACEHOLDER-balance.
 */
export const LINE_CAPACITY: Record<string, number> = {
  rail: 1200, // "Rail Line" — slow/regional commuter throughput per tile
  hs1: 6000, // "HS1 High-Speed Line" — high-capacity per tile
};

/**
 * Commuter-flow coefficients (rail classes). These deliberately MIRROR the
 * existing "Commuter Revenue" term in engine.computeFlows (population × 0.08 ×
 * min(commuterWeight, 6)) so the rail-usage read-out and the commuter income can
 * never tell different stories. ⚠ PLACEHOLDER-balance (inherited magnitudes).
 */
export const LINE_COMMUTER_K = 0.08; // commuter trips per citizen per unit station weight
export const LINE_COMMUTER_WEIGHT_CAP = 6; // matches min(commuterWeight, 6) in computeFlows

/**
 * Per-tile throughput capacity of a LINE spec (people or vehicles per tick):
 *   - road / m20 (and every drivable road tier): REUSES ROAD_TIER_CAPACITY.
 *   - rail / hs1: from LINE_CAPACITY.
 *   - anything else (a house, a park, a station building): 0 (not a line).
 * Pure function of the spec. ⚠ capacities are PLACEHOLDER-balance.
 */
export function lineCapacityOf(sp: Spec | undefined): number {
  if (!sp) return 0;
  const tier = roadTierOf(sp);
  if (tier > 0) return ROAD_TIER_CAPACITY[tier as RoadTier]; // road / m20 / avenue…
  return LINE_CAPACITY[sp.id] ?? 0; // rail / hs1
}

/** True when this spec is a network LINE that carries flow (road or rail). */
export function isLineSpec(sp: Spec | undefined): boolean {
  return lineCapacityOf(sp) > 0;
}

/**
 * Per-line saturation read-out (FEAT-1972079902 inc1). One entry per line CLASS
 * (spec id) that has at least one tile on the map, with the commuter/traffic flow
 * it carries, its total capacity, and the saturation ratio 0..1.
 *
 * SIGN / COLOUR CONVENTION (matches the BUG-425 water headroom split): headroom =
 * capacity − usage. headroom < 0 (overCapacity) is a SHORTFALL → the danger/`neg`
 * colour; headroom ≥ 0 is surplus → the `pos` colour. No new colour language is
 * introduced — callers reuse the existing pos/neg (—done/—danger) tokens.
 */
export interface LineUsage {
  /** Line spec id: 'rail' | 'hs1' | 'road' | 'm20' (or another road tier). */
  spec: string;
  /** Coarse family for rendering: 'rail' (commuter flow) or 'road' (traffic). */
  kind: 'rail' | 'road';
  /** Human label (spec.name). */
  name: string;
  /** Tiles of this line class on the map. */
  tiles: number;
  /** Total throughput capacity = per-tile capacity × tiles. */
  capacity: number;
  /** Flow the line carries this state (people/vehicles per tick). */
  usage: number;
  /** usage / capacity, clamped to [0,1]. */
  saturation: number;
  /** capacity − usage. Negative ⇒ the line is over capacity (shortfall). */
  headroom: number;
  /** headroom < 0 — drives the BUG-425 surplus-vs-shortfall colour split. */
  overCapacity: boolean;
}

/**
 * Compute per-line-class usage/capacity/saturation. PURE + DETERMINISTIC.
 *
 * Rail classes (rail, hs1): commuter flow routed from the CONNECTED stations —
 * Ashford International (the HS1 gateway, ×3 weight) feeds `hs1`, every other
 * connected station (×1) feeds `rail`. Flow = population × LINE_COMMUTER_K ×
 * min(weight, LINE_COMMUTER_WEIGHT_CAP), the same shape as Commuter Revenue.
 *
 * Road classes (road, m20, …): the city's coarse traffic demand
 * (Σ feederTrafficWeight × trafficActivity — road inc2's exact idiom) shared
 * across the drivable classes in proportion to each class's capacity.
 */
// BUG-602 (integration-soak perf cliff, 2026-09-02): memoised on state
// identity — advance()'s wellbeing chain, computeFlows, the congestion
// counter, debugjson and the UI all pull line usage against the SAME state
// object within one tick/render; the station-graph + per-building traffic
// scan is the chain's heaviest leaf. Pure function of s, so memoOnState is
// exact (same discipline as powerStats/serviceCapacityAggregates above).
export const lineUsageOf: (s: SimState) => LineUsage[] = memoOnState((s) => {
  // Tile count per present line class (order-independent aggregate).
  const tilesBySpec = new Map<string, number>();
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!isLineSpec(sp)) continue;
    tilesBySpec.set(b.spec, (tilesBySpec.get(b.spec) ?? 0) + 1);
  }

  // ---- rail commuter flow: weight connected stations by class ----
  const links = stationLinks(s);
  // Strict (x,y) order for GR#21 hygiene (addition commutes, but no ordering
  // ambiguity is left to chance and there is no early break).
  const stations = s.buildings
    .filter((b) => SPECS[b.spec]?.kind === 'station' && links.connectedIds.has(b.id))
    .sort((a, b) => a.x - b.x || a.y - b.y);
  let railWeight = 0;
  let hsWeight = 0;
  for (const b of stations) {
    if (b.spec === 'station_ashford') hsWeight += 3;
    else railWeight += 1;
  }
  // Compute ONE combined commuter scalar — the EXACT economy term from
  // engine.computeFlows: round(pop × K × min(w_rail + w_hs, cap)) — then apportion
  // it across the hs1/rail buckets by their RAW weight share, so the two buckets
  // SUM EXACTLY to the economy's Commuter Revenue basis (invariant restored: the
  // panel can never claim more flow than the economy credits). Capping each bucket
  // separately would overstate flow once the combined weight passes the cap, since
  // min(a,cap)+min(b,cap) ≥ min(a+b,cap). Integer-exact: hs1 = floor(share), rail
  // takes the remainder (deterministic), so hs1 + rail === combined with no drift.
  const totalWeight = railWeight + hsWeight;
  const combined = Math.round(
    s.population * LINE_COMMUTER_K * Math.min(totalWeight, LINE_COMMUTER_WEIGHT_CAP)
  );
  const hs1Usage = totalWeight > 0 ? Math.floor((combined * hsWeight) / totalWeight) : 0;
  const railUsage = combined - hs1Usage;
  const railUsageBySpec: Record<string, number> = {
    rail: railUsage,
    hs1: hs1Usage,
  };

  // ---- road/motorway traffic flow (road inc2 idiom, read-only) ----
  const activity = trafficActivity(s);
  let totalFeeder = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp) totalFeeder += feederTrafficWeight(sp);
  }
  const totalTraffic = Math.round(totalFeeder * activity);
  let totalDrivableCap = 0;
  for (const [spec, tiles] of tilesBySpec) {
    const sp = SPECS[spec];
    if (roadTierOf(sp) > 0) totalDrivableCap += lineCapacityOf(sp) * tiles;
  }

  const out: LineUsage[] = [];
  for (const [spec, tiles] of tilesBySpec) {
    const sp = SPECS[spec]!;
    const capacity = lineCapacityOf(sp) * tiles;
    const isRoad = roadTierOf(sp) > 0;
    const usage = isRoad
      ? totalDrivableCap > 0
        ? Math.round((totalTraffic * capacity) / totalDrivableCap)
        : 0
      : railUsageBySpec[spec] ?? 0;
    const saturation = capacity > 0 ? Math.min(1, Math.max(0, usage / capacity)) : 0;
    const headroom = capacity - usage;
    out.push({
      spec,
      kind: isRoad ? 'road' : 'rail',
      name: sp.name,
      tiles,
      capacity,
      usage,
      saturation,
      headroom,
      overCapacity: headroom < 0,
    });
  }
  // Deterministic, spec-id-sorted output.
  out.sort((a, b) => (a.spec < b.spec ? -1 : a.spec > b.spec ? 1 : 0));
  return out;
});

/**
 * FEAT-congestion-teeth-2026-09-02 (Q100057 A1 "congestion must have felt
 * consequences", Q100071 rec-on-all — every BA recommendation in the spec
 * taken as written) — PLACEHOLDER balance constants, grouped in one named
 * object per the house pattern (CRIME_CONSTANTS mirror) so Aaron's future
 * balance-pass row replaces all three in a single commit.
 *
 * Values below are the spec's own BA recommendations (Open Questions 2/3/6):
 *   - CONGESTION_PENALTY_THRESHOLD = 0.75 (bites before the ~0.80 auto-widen
 *     trigger, so the player feels congestion BEFORE auto-scale, not after)
 *   - CONGESTION_SUSTAINED_TICKS = 60 (~2 game-months; long enough that a
 *     temporary spike doesn't sting — see the burst-tolerance proof)
 *   - CONGESTION_INCOME_K = 0.10 (a fully congested line-set costs ~10% of
 *     the powered-income basis, AC-9)
 * Aggregation across sustained lines is AVERAGE (spec Q5) and the per-line
 * penalty ramp is LINEAR (spec Q4) — see congestionLinesOf/congestionFactorOf.
 */
export const CONGESTION_CONSTANTS = {
  /** Saturation ratio (usage/capacity) above which a line starts accruing sustained-congestion ticks. */
  CONGESTION_PENALTY_THRESHOLD: 0.75,
  /** Consecutive ticks a line must stay >= threshold before it counts as "sustained" (isSustained/AC-1). */
  CONGESTION_SUSTAINED_TICKS: 60,
  /** AC-9 income-drag coefficient: a fully-penalized (congestionFactor=0) sustained network costs this fraction of powered income. */
  CONGESTION_INCOME_K: 0.1,
} as const;

/**
 * FEAT-congestion-teeth-2026-09-02 (GR#16) — Type-Safe Storage Boundary
 * sanitiser for `s.congestionTicksBySpec`, same shape as sanitizeCrimeRate:
 * a corrupt save can hand back ANY JSON-representable value for this field
 * (a string, an array, an object with non-numeric values, NaN-bearing
 * entries, negative/fractional counts). Non-object collapses to `{}` (the
 * field's own documented old-save default — types.ts); each entry is
 * independently sanitised to a non-negative integer, capped at
 * CONGESTION_SUSTAINED_TICKS (a corrupt `1e9` count can never fabricate a
 * false "more sustained than sustained" reading — the flag only ever needs
 * >= the ticks constant to fire).
 */
export function sanitizeCongestionTicksBySpec(v: unknown): Record<string, number> {
  const out: Record<string, number> = {};
  if (typeof v !== 'object' || v === null || Array.isArray(v)) return out;
  for (const [spec, raw] of Object.entries(v as Record<string, unknown>)) {
    const n = typeof raw === 'number' && Number.isFinite(raw) ? raw : 0;
    const clamped = Math.max(0, Math.min(CONGESTION_CONSTANTS.CONGESTION_SUSTAINED_TICKS, Math.floor(n)));
    if (clamped > 0) out[spec] = clamped; // zero entries omitted — self-pruning (see types.ts doc)
  }
  return out;
}

/**
 * FEAT-congestion-teeth-2026-09-02 (AC-1) — advance the per-line sustained-
 * congestion tick counters ONE tick, given the PRIOR counters and THIS
 * tick's lineUsageOf() rows. PURE + DETERMINISTIC (GR#21): no Date/random;
 * folds over `usages`, which is already spec-id-sorted by lineUsageOf, so
 * the resulting object's insertion order (irrelevant for a Record, but kept
 * for hygiene) is itself deterministic. Only road/motorway lines (kind ===
 * 'road') accrue congestion — rail commuter flow is a separate mechanic
 * (spec §1, "road class line"). Sole caller: engine.ts's advance().
 */
export function advanceCongestionTicks(
  prevTicks: Record<string, number>,
  usages: LineUsage[]
): Record<string, number> {
  const { CONGESTION_PENALTY_THRESHOLD, CONGESTION_SUSTAINED_TICKS } = CONGESTION_CONSTANTS;
  const out: Record<string, number> = {};
  for (const u of usages) {
    if (u.kind !== 'road') continue;
    const prev = prevTicks[u.spec] ?? 0;
    const next =
      u.saturation >= CONGESTION_PENALTY_THRESHOLD
        ? Math.min(prev + 1, CONGESTION_SUSTAINED_TICKS)
        : 0; // RESET RULE (types.ts doc): hard reset the instant saturation drops below threshold.
    if (next > 0) out[u.spec] = next;
  }
  return out;
}

/**
 * FEAT-congestion-teeth-2026-09-02 (AC-1/AC-6/AC-8) — one road/motorway
 * LineUsage row extended with its sustained-congestion state. `congestionFactor`
 * is the spec's linear damping ramp ∈ [0,1]: 1.0 while NOT sustained (AC-4 —
 * a short burst under the sustained window imposes nothing) or while
 * saturation is still <= threshold; ramps linearly down to 0.0 as saturation
 * climbs from threshold to full (1.0) ONLY once the line is sustained.
 * Bounded by construction (AC-6): both terms of the ramp are pre-clamped to
 * [0,1] before the subtraction, and the final Math.max(0, …) forbids the
 * ramp from going negative past full saturation.
 */
export interface CongestionLine extends LineUsage {
  /** Consecutive ticks >= CONGESTION_PENALTY_THRESHOLD, capped at CONGESTION_SUSTAINED_TICKS. */
  sustainedTicks: number;
  /** True once sustainedTicks >= CONGESTION_SUSTAINED_TICKS (AC-1). */
  isSustained: boolean;
  /** LINEAR damping factor; 1.0 = no penalty, 0.0 = fully penalized. Only < 1.0 while isSustained. */
  congestionFactor: number;
}

// BUG-602: memoised — see lineUsageOf. Pure derivation of s.
export const congestionLinesOf: (s: SimState) => CongestionLine[] = memoOnState((s) => {
  const ticks = sanitizeCongestionTicksBySpec(s.congestionTicksBySpec);
  const { CONGESTION_PENALTY_THRESHOLD, CONGESTION_SUSTAINED_TICKS } = CONGESTION_CONSTANTS;
  const out: CongestionLine[] = [];
  // lineUsageOf() already returns a spec-id-sorted, order-independent array
  // (GR#21) — no map-range-with-break, no early exit.
  for (const u of lineUsageOf(s)) {
    if (u.kind !== 'road') continue;
    const sustainedTicks = ticks[u.spec] ?? 0;
    const isSustained = sustainedTicks >= CONGESTION_SUSTAINED_TICKS;
    const sat = Math.max(0, Math.min(1, u.saturation));
    const congestionFactor = !isSustained
      ? 1
      : Math.max(0, 1 - Math.max(0, sat - CONGESTION_PENALTY_THRESHOLD) / (1 - CONGESTION_PENALTY_THRESHOLD));
    out.push({ ...u, sustainedTicks, isSustained, congestionFactor });
  }
  return out;
});

/**
 * FEAT-congestion-teeth-2026-09-02 (AC-2/AC-3/AC-4/AC-9) — the city-wide
 * congestion factor wellbeing/income consume: AVERAGE of the congestionFactor
 * of every SUSTAINED road/motorway line (spec Q5, BA rec "AVERAGE… if no
 * lines are sustained, factor = 1.0" — AC-4's zero-penalty case). Non-
 * sustained lines are excluded from the average entirely (they are already
 * pegged at 1.0 and including them would just dilute the signal toward 1.0
 * without changing the "uncongested = exactly 1.0" invariant AC-4 needs).
 * PURE + DETERMINISTIC: order-independent reduce over an already-sorted
 * array, no early break.
 */
export function congestionFactorOf(s: SimState): number {
  const sustained = congestionLinesOf(s).filter((l) => l.isSustained);
  if (sustained.length === 0) return 1; // AC-4: nothing sustained -> zero penalty.
  const sum = sustained.reduce((a, l) => a + l.congestionFactor, 0);
  return sum / sustained.length;
}

// BUG-527 — sumBy backs GP/hospital/police/school coverage in BOTH
// serviceCoverageOf and utilisationOf. Before this fix it iterated every
// building regardless of activation state, so an OFFLINE / road-disconnected
// / still-under-construction police station, clinic, hospital, or school
// contributed its full `served`/`children` capacity to the coverage meters
// while (correctly) drawing zero upkeep — free coverage from a
// non-functioning building. Mirror the powerStats() / wasteGeneratedOf()
// gate exactly: `if (!isOnline(s, b)) continue;` inside the building loop,
// same as those two functions (data.ts ~1918, ~2208). Order-independent
// fold, no map-range-with-break (GR#21).
//
// FEAT-webworker-sim-offload Stage 0 (2026-09-02): the old per-predicate
// `sumBy()` helper that lived here (called once per service kind — 7 times
// from serviceCoverageOf, plus 3 more from utilisationOf) was removed once
// every call site was folded into the single-pass serviceCapacityAggregates()
// below — see that function's docblock for the full before/after story. Its
// isOnline-gated-full-building-list-per-predicate SHAPE is preserved exactly
// in the new aggregate's single loop, just no longer duplicated per call.

// FEAT-webworker-sim-offload Stage 0 (2026-09-02) — memoisation for the
// O(buildings) selectors that get called MULTIPLE times per unchanged
// SimState within one derivation pass: powerStats (utilisationOf/brownoutOf/
// serviceCoverageOf/computeFlows/debugjson/snapshot), countByKindOnline
// (computeFlows calls it 3x today), and the sumBy-driven service-capacity sums
// (serviceCoverageOf's 7 separate full-building-list passes, PLUS utilisationOf
// re-running the SAME sumBy predicates per-building from MapView's render loop
// — an O(buildings²) amplification at 68K population).
//
// INDEPENDENT ROUND FINDING (2026-09-02, REJECT on the first cut): the first
// version of this memo keyed on a hand-picked TRIPLE — (buildings,
// roadConnectivity, tick) — reasoned from isOnline()'s own read-set. That
// list was INCOMPLETE: serviceCapacityAggregates() also reads pipeTier
// (via plantEffServed -> pipeTierOf for water plants), so a 'pipeUpgrade'
// dispatch — which changes ONLY pipeTier, leaving buildings/roadConnectivity/
// tick byte-identical — produced a cache HIT against the stale pre-upgrade
// capacity. Proven: read serviceCoverageOf, dispatch pipeUpgrade, read again
// -> cleanwater cap silently unchanged (stale) while waterCaps(s) on the SAME
// state reported the new number, and wellbeingOf(s) (pure by contract)
// returned different values cold vs. after a prior read — a live GR#3
// violation (two panels disagree) and a live GR#21 violation (a "pure"
// function became call-history-dependent). Hand-enumerating a memoised
// function's read-set is exactly the kind of list that silently rots as the
// function grows — the SAME class of gap that produced the miss.
//
// FIX: key the cache on the STATE OBJECT ITSELF (a WeakMap<SimState, T>,
// the same idiom occupiedSetCache above already uses, just keyed on `s`
// instead of `s.buildings`) rather than on a hand-picked field list. This is
// structurally immune to "forgot a field" by construction, given the
// reducer's existing immutable-update discipline: every reduceCore() case
// that changes ANY field returns a brand-new top-level object via `{...state,
// ...}` (verified: no `state.<field> =` or `.buildings[i] =`/`.push(`
// in-place mutation exists anywhere in engine.ts — grepped clean), and every
// no-op case returns the SAME `state` reference unchanged (a correct,
// trivial cache hit). So: same object -> guaranteed nothing relevant changed
// -> safe to reuse; different object -> guaranteed something MAY have
// changed -> safe (if slightly conservative) to recompute. No per-function
// read-set bookkeeping, no way to add a new field this memo silently misses.
// WeakMap entries are GC'd once a superseded state is no longer referenced
// anywhere (journal/redux history included) — no unbounded-growth leak risk,
// same reasoning as occupiedSetCache's.
export function memoOnState<T>(compute: (s: SimState) => T): (s: SimState) => T {
  const cache = new WeakMap<SimState, T>();
  return (s: SimState): T => {
    if (cache.has(s)) return cache.get(s) as T;
    const value = compute(s);
    cache.set(s, value);
    return value;
  };
}

/**
 * FEAT-2326609761 inc2 (glide-mode perf): a shared id->Building lookup,
 * memoised on the `buildings` ARRAY's own identity (not the whole SimState —
 * mirrors viewportCull.ts's spatialIndexOf idiom, and unlike memoOnState is
 * SAFE to key this way because a Building object's id/spec/x/y/capacityTier
 * never mutate in place — this codebase's whole update discipline is
 * immutable replace, never in-place mutation, so "same buildings array
 * reference" really does mean "same set of Building objects, unchanged").
 * Several call sites across engine.ts/consolidator.ts used to rebuild an
 * `id -> Building` Map via a fresh O(buildings) fold on EVERY call — cheap
 * once a month (the pre-inc2 monthly consolidator cadence) but a real cost
 * if a hot path calls it once per GAME DAY (glide mode) instead. Reusing one
 * cached Map when `buildings` hasn't changed since the last call (the common
 * case on a day where the consolidator's glide pass finds nothing to do)
 * turns that into an O(1) WeakMap hit.
 */
export function buildingByIdOf(buildings: SimState['buildings']): Map<number, SimState['buildings'][number]> {
  const cached = buildingByIdCache.get(buildings);
  if (cached) return cached;
  const map = new Map<number, SimState['buildings'][number]>();
  for (const b of buildings) map.set(b.id, b);
  buildingByIdCache.set(buildings, map);
  return map;
}
const buildingByIdCache = new WeakMap<SimState['buildings'], Map<number, SimState['buildings'][number]>>();

/**
 * Service-capacity aggregate for the served-population/children services —
 * SINGLE pass over s.buildings computing every sum serviceCoverageOf() and
 * utilisationOf()'s health/police/fire cases used to each gather via their
 * OWN separate sumBy() call (7 full-building-list passes -> 1). Memoised
 * (memoOnState) so utilisationOf's per-building MapView render loop hits the
 * cache on every building after the first, instead of re-walking the whole
 * buildings array once per rendered building.
 */
interface ServiceCapacityAggregates {
  nursery: number;
  primary: number;
  tertiary: number;
  gp: number;
  hosp: number;
  police: number;
  fire: number;
  clean: number;
  waste: number;
  // BUG-630: total online school "places" — mirrors utilisationOf()'s 'school'
  // case exactly (`if (os?.children) places += os.children`, no kind/stage
  // filter, same isOnline gate), gathered here as an EXTRA accumulator in the
  // same single pass rather than folded into nursery/primary/tertiary above,
  // so it stays byte-identical to the original per-call loop even if a future
  // children-bearing spec ever ships without one of those three stage tags.
  schoolPlaces: number;
}

const serviceCapacityAggregates: (s: SimState) => ServiceCapacityAggregates = memoOnState((s) => {
  let nursery = 0;
  let primary = 0;
  let tertiary = 0;
  let gp = 0;
  let hosp = 0;
  let police = 0;
  let fire = 0;
  let clean = 0;
  let waste = 0;
  let schoolPlaces = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const tier = b.capacityTier ?? 0;
    // BUG-662: was a bare `sp.children ?? 0` / `sp.served ?? 0` read, which
    // ignores building.capacityTier entirely — an auto-scaled tiered building
    // (e.g. a scaled-up edu_tech Technical College, stage 'tertiary') stayed
    // pinned at its tier-0 base capacity in this aggregate forever, even
    // though capacityAtTier() (and the per-building BuildingMonitor that
    // charged the player for the upgrade) already knows the real, larger
    // number. childrenAtTier/servedAtTier (defined above capacityAtTier)
    // route every read through the same tier-aware guard totalChildrenCapacity
    // already used for the `tertiary` figure's counterpart aggregate.
    if (sp.stage === 'nursery') nursery += childrenAtTier(sp, tier);
    else if (sp.stage === 'primary' || sp.stage === 'city') primary += childrenAtTier(sp, tier);
    else if (sp.stage === 'tertiary') tertiary += childrenAtTier(sp, tier);
    if (sp.id === 'hea_clinic') gp += servedAtTier(sp, tier);
    else if (sp.id === 'hea_hospital' || sp.id === 'hea_teaching') hosp += servedAtTier(sp, tier);
    if (sp.kind === 'police') police += servedAtTier(sp, tier);
    else if (sp.kind === 'fire') fire += servedAtTier(sp, tier);
    else if (sp.kind === 'water') {
      const eff = plantEffServed(s, b);
      if (sp.tag === 'clean') clean += eff;
      if (sp.tag === 'waste') waste += eff;
    }
    if (sp.children) schoolPlaces += childrenAtTier(sp, tier);
  }
  return { nursery, primary, tertiary, gp, hosp, police, fire, clean, waste, schoolPlaces };
});

/**
 * BUG-630 — one memoised per-building display-state derivation, keyed by
 * building id.
 *
 * PROBLEM: MapView's draw loop (and other per-building render code) called
 * isOnline() + blockOccupancy() + utilisationOf() + densityTier() FRESH for
 * every building on EVERY redraw — not just on a sim tick, but on every
 * camera pan/zoom too, since the draw effect re-runs off `view`/`geom` as
 * well as `state`. BUG-622 memoised the CITYWIDE AGGREGATES those four
 * formulas read (residentsCapacity/totalJobs/powerStats/
 * serviceCapacityAggregates, all memoOnState above), which fixed the O(n^2)
 * blow-up, but the four formulas themselves — the switch statements, the
 * SPECS[b.spec] lookup, the road-adjacency footprint walk — still re-ran per
 * building on every redraw, measured at ~165ms for one pass at 13k buildings
 * (GPU spike Phase 0 profiling).
 *
 * FIX: compute all four for every building in a SINGLE pass, ONCE per state
 * identity (memoOnState — the house idiom used throughout this file, e.g.
 * serviceCapacityAggregates/totalJobs/powerStats above). A redraw triggered
 * by camera movement alone (same `state` object, no tick advanced) then pays
 * only a Map.get() per building — O(buildings) cheap lookups instead of
 * O(buildings) formula re-derivations.
 *
 * CORRECTNESS CONTRACT (the parity test in
 * test/attack-bug630-display-state.test.mjs proves this): every entry's four
 * fields are produced by calling the SAME SSOT functions
 * (isOnline/blockOccupancy/utilisationOf/densityTier) a caller would invoke
 * directly for that building — this is a caching wrapper around the existing
 * formulas, never a reimplementation of them. A building whose spec cannot be
 * resolved (SPECS[b.spec] undefined) is omitted from the map, mirroring every
 * existing per-building call site's own `if (!sp) continue` guard (MapView's
 * draw loop, debugjson.ts, consistency.ts).
 */
export interface BuildingDisplayState {
  online: boolean;
  occupancy: number | null;
  utilisation: Utilisation | null;
  tier: 1 | 2 | 3;
}

export const buildingDisplayStates: (s: SimState) => ReadonlyMap<number, BuildingDisplayState> =
  memoOnState((s) => {
    const out = new Map<number, BuildingDisplayState>();
    for (const b of s.buildings) {
      const sp = SPECS[b.spec];
      if (!sp) continue;
      out.set(b.id, {
        online: isOnline(s, b),
        occupancy: blockOccupancy(s, b),
        utilisation: utilisationOf(s, b),
        tier: densityTier(sp),
      });
    }
    return out;
  });

export const powerStats: (s: SimState) => { need: number; cap: number } = memoOnState(
  (s) => computePowerStats(s)
);

function computePowerStats(s: SimState): { need: number; cap: number } {
  // BUG-430 — a power plant only feeds the grid while it is ONLINE. Mirror the
  // building-activation gate (onlineResidentsCapacity / wasteGeneratedOf): a
  // road-disconnected or still-under-construction plant (incl. the Five Gorges
  // Dam) generates ZERO, exactly as an offline building draws/works/houses zero.
  // BUG-431 — the DEMAND side is the exact mirror: an OFFLINE consumer draws
  // ZERO. Before BUG-431 the per-building consumer term used countByKind(all
  // buildings), so a road-disconnected or still-under-construction industry /
  // office / mine added power DEMAND it could never draw — inconsistent with the
  // activation gate that already zeroes an offline building's jobs/draw and with
  // BUG-430's online-gated cap. Both the plant sum and the consumer counts are
  // gathered in ONE online-gated pass here: countByKind() cannot be reused for
  // the consumer term because it exposes only the Spec, not the building instance
  // isOnline() needs. Order-independent / pure (GR#21): same state → same
  // need/cap; no Date/Math.random. DD4=C (Aaron, 2026-08-28): there is no grace
  // period — isOnline evaluates gates immediately, so no special-case here.
  //
  // The population term stays UNGATED: s.population already reflects only
  // online-housed residents (onlineResidentsCapacity governs how many residents
  // the engine seats), so it never counts offline dwellings — gating it would
  // double-remove them.
  let staticMw = 0;
  let industrial = 0;
  let office = 0;
  let mine = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (!sp) continue;
    // FEAT-2326609740 (Q100089=B): a power plant with a reactor-count
    // capacityTiers ladder (pow_nuke) reports its TIER's MW, not the base
    // catalogue mw — same capacityAtTier honouring residentsCapacity/
    // totalJobs already give every other tiered spec. A plant with no
    // capacityTiers (every other generator, today) is unchanged.
    if (sp.kind === 'power') staticMw += sp.capacityTiers ? capacityAtTier(sp, b.capacityTier ?? 0) : sp.mw ?? 0;
    else if (sp.kind === 'industrial') industrial++;
    else if (sp.kind === 'office') office++;
    else if (sp.kind === 'mine') mine++;
  }
  return {
    need: Math.round(s.population * 0.012 + industrial * 6 + office * 4 + mine * 8),
    // FEAT-1972079906 inc2: Energy-from-Waste plants add THROUGHPUT-based MW to grid
    // capacity (efwPowerOf) — it feeds the same surplus/Grid-Export path as any power
    // plant. Cannot double-count: the EfW spec carries no `mw`, so it is absent from the
    // power-plant sum above; a city with no EfW throughput adds exactly 0. efwPowerOf is
    // already online-gated (its throughput comes from online processors via
    // processCapacityOf), so no double-gating is needed on the EfW term.
    cap: staticMw + efwPowerOf(s),
  };
}

// ---------- brownout (BUG-393) ----------
//
// A power DEFICIT (need > cap) is qualitatively worse than unmet demand
// growth: the city is literally browning out. Before BUG-393 a 4% deficit
// (10,592 MW need vs 10,185 cap) read as a ±4 wiggle on the linear demand
// index and had ZERO consequence anywhere in the sim.
//
// ALL constants below are PLACEHOLDER weights under the balance-number
// regime — directional only, flagged for Aaron's row-by-row balance pass.

/** PLACEHOLDER: any deficit floors the power demand index here. */
export const BROWNOUT_INDEX_FLOOR = 50;
/** PLACEHOLDER: index points per unit deficitRatio above the floor
 * (a 4% deficit reads +60; a 20% deficit pegs the index at +100). */
export const BROWNOUT_INDEX_SLOPE = 250;
/** PLACEHOLDER: fraction of powered-business income lost at a total (100%)
 * deficit — a 4% deficit costs ~2.4% of commercial/industrial/office income. */
export const BROWNOUT_INCOME_K = 0.6;
/** PLACEHOLDER: utilities-wellbeing collapse rate vs deficitRatio — a 50%
 * deficit multiplies the utilities part by (1 - 0.5*1.5) = 0.25. */
export const BROWNOUT_WELLBEING_K = 1.5;

export interface Brownout {
  /** true while power need exceeds capacity. */
  active: boolean;
  /** 1 - cap/need while active, else 0. Pure function of state — deterministic.
   *  Identical to 1 - coverage for the 'power' row of serviceCoverageOf,
   *  because that row's coverage is cap/need (BUG-392 shared source). */
  deficitRatio: number;
  /** Multiplier applied to commercial/industrial/office income (<= 1). */
  incomeFactor: number;
}

/** Single source of truth for the brownout state (GR#3): the demand index,
 * the income penalty, the wellbeing penalty, and the UI warning all derive
 * from this one deterministic computation. */
export function brownoutOf(s: SimState): Brownout {
  const pw = powerStats(s);
  if (pw.need <= 0 || pw.cap >= pw.need) {
    return { active: false, deficitRatio: 0, incomeFactor: 1 };
  }
  const deficitRatio = 1 - pw.cap / pw.need;
  return {
    active: true,
    deficitRatio,
    incomeFactor: Math.max(0, 1 - deficitRatio * BROWNOUT_INCOME_K),
  };
}

/**
 * FEAT-2326609711 inc1 fix (r2, closing the r1 HALF-WIRED DEFECT) — SINGLE
 * SOURCE OF TRUTH for whether a power deficit's CONSEQUENCES (income
 * penalty, wellbeing Utilities collapse, the DemandDock brownout banner +
 * power-row alert escalation) should apply THIS tick.
 *
 * brownoutOf() reports the raw PHYSICAL fact — local capacity < demand —
 * and stays deliberately toggle-BLIND: several callers (incl. the
 * destructive-round attack suite, attack-grid-import.test.mjs) rely on it
 * reflecting powerStats alone, e.g. to sanity-check that a real deficit
 * exists before asserting the toggle suppresses its consequences. Aaron's
 * ruling (2026-09-01, inc1 is price-premium-only): while Grid Import cover
 * is ON, a shortfall is bought in from the regional grid and is NOT a
 * brownout of any kind — no income penalty, no wellbeing/utilities
 * collapse, no BROWNOUT banner. This function is the ONE place that
 * combines the physical fact with the toggle to answer "does the brownout
 * bite this tick" — every consumer of a brownout CONSEQUENCE must read it,
 * never recompute `!(s.gridImportEnabled ?? GRID_IMPORT_ENABLED_DEFAULT)`
 * locally (GR#3 single source of truth).
 */
export function isBrownoutActive(s: SimState): boolean {
  const gridImportOn = s.gridImportEnabled ?? GRID_IMPORT_ENABLED_DEFAULT;
  return brownoutOf(s).active && !gridImportOn;
}

/**
 * BUG-652 — jobs contributed by ONE building at its current tier, safe for a
 * spec that carries `jobs` ALONGSIDE another capacity field (residents/
 * children/served). capacityAtTier()'s array/fallback assumes a spec has
 * exactly ONE scaling capacity metric (the same assumption
 * evaluateBuildingMonitors' monitorType priority documents — engine.ts,
 * residents > children > served > jobs, picks the ONE metric a spec's single
 * capacityTiers ladder belongs to). BUG-652 gives jobs to hea_teaching
 * (which already carries a capacityTiers ladder sized for its `served`
 * figure — 200,000 as of FEAT-2326609761's balance bump, was 120,000) and
 * uni/station_ashford (which carry children/served with no ladder at all) —
 * blindly reading capacityAtTier(sp, tier) for jobs on hea_teaching would
 * silently return its SERVED tier value (200,000+) as the job count, an
 * overstatement of two further orders, and on uni/station_ashford would
 * depend on capacityAtTier's bare fallback ordering (residents ?? jobs ??
 * 0), which happens to work only by accident (see grand_terminus, the one
 * pre-existing served+jobs+no-capacityTiers spec).
 *
 * FIX: a spec with jobs AND any other capacity field keeps jobs FLAT (the
 * catalogue's bare `sp.jobs`, no auto-scale growth) — never routed through
 * capacityAtTier(), whether or not a capacityTiers ladder is present. A spec
 * whose ONLY capacity dimension is jobs (every pre-existing commercial/
 * office/industrial/mine/transport job spec) is UNCHANGED: same
 * capacityAtTier() scaling as before this fix (GR#3 — no behaviour change
 * to the live money path for any spec that isn't part of this bug).
 */
/**
 * BUG-652 GRANDFATHERING (2026-09-04, round-mandated after the combined
 * FEAT-2326609763+BUG-652 estate was REJECTED for retroactively re-pricing
 * buildings a player already owns): if `b.jobsOverride` is present (stamped
 * by stampJobsGrandfather() below, ONLY onto a pre-existing building of one
 * of the six BUG-652 specs found in a save that predates
 * JOBS_GRANDFATHER_ECONOMY_EPOCH), it WINS unconditionally over anything
 * `sp.jobs`/capacityAtTier would otherwise say — mirrors the
 * `b.footprintW ?? sp.w` per-building-override convention exactly (types.ts,
 * Building.jobsOverride's own doc comment). A building placed after the
 * stamp existed never carries this field, so it falls through to the
 * ordinary jobsAtTier() logic below and reads its spec's real job count.
 */
function effectiveJobsOf(sp: Spec, b: { jobsOverride?: number; capacityTier?: number }): number {
  if (b.jobsOverride != null) return b.jobsOverride;
  return jobsAtTier(sp, b.capacityTier ?? 0);
}

function jobsAtTier(sp: Spec, tier: number): number {
  if (!sp.jobs) return 0;
  const jobsSharesSpecWithOtherCapacity = sp.residents != null || sp.children != null || sp.served != null;
  return jobsSharesSpecWithOtherCapacity ? sp.jobs : capacityAtTier(sp, tier);
}

/**
 * BUG-652 GRANDFATHERING — the six specs this bug gave real job counts to.
 * The ONLY specs stampJobsGrandfather() will ever touch. SSOT list so the
 * migration, its tests, and any future audit read the same six ids.
 */
export const JOBS_GRANDFATHERED_SPECS: readonly string[] = Object.freeze([
  'land_airport',
  'hea_teaching',
  'uni',
  'land_tunnel',
  'land_stadium',
  'station_ashford',
]);

/**
 * BUG-652 GRANDFATHERING — the economy-schema epoch this migration belongs
 * to. See SimState.economyEpoch's own doc comment (types.ts) for why this is
 * a plain incrementing counter, deliberately NOT a comparison against the
 * app's git-describe buildVersion string (unsound to order).
 */
export const JOBS_GRANDFATHER_ECONOMY_EPOCH = 1;

/**
 * BUG-652 GRANDFATHERING — true when `state` still needs the migration (its
 * `economyEpoch` predates JOBS_GRANDFATHER_ECONOMY_EPOCH). Exposed so a
 * two-phase restore (prepareRestoreForChunkedTail + replayTailChunked,
 * replay.ts) can capture the decision from the RAW pre-tail snapshot and
 * carry it forward explicitly — re-deriving it from `state.economyEpoch`
 * AFTER an early partial stamp has already bumped the epoch would silently
 * read "already current" and skip the tail (round r2 F4's exact failure
 * mode, reproduced one level up if this check were re-run post-bump instead
 * of the captured boolean being threaded through).
 */
export function needsJobsGrandfather(state: SimState): boolean {
  return (state.economyEpoch ?? 0) < JOBS_GRANDFATHER_ECONOMY_EPOCH;
}

/**
 * BUG-652 GRANDFATHERING — the raw stamping pass: every building in `state`
 * whose spec is one of JOBS_GRANDFATHERED_SPECS and that does not already
 * carry `jobsOverride` is stamped `jobsOverride: 0` (the pre-BUG-652 economy
 * had no jobs field on any of these six specs at all, so zero is the ONLY
 * historically-accurate figure, never invented). Does NOT touch
 * `economyEpoch` and does NOT check it either — UNCONDITIONAL, idempotent
 * (a building that already carries jobsOverride is left alone), and safe to
 * call more than once on the same or an already-migrated state. This is the
 * primitive `stampJobsGrandfather()` (below) composes for the common
 * single-shot case; a caller that must apply the stamp a SECOND time after
 * `economyEpoch` has already been bumped by an earlier partial pass (the
 * chunked-tail restore path's tail-replay catch-up, replay.ts's
 * replayTailChunked) calls this directly instead.
 */
export function stampJobsGrandfatherForce(state: SimState): SimState {
  let changed = false;
  const buildings = state.buildings.map((b) => {
    if (b.jobsOverride != null) return b;
    if (!JOBS_GRANDFATHERED_SPECS.includes(b.spec)) return b;
    changed = true;
    return { ...b, jobsOverride: 0 };
  });
  return changed ? { ...state, buildings } : state;
}

/**
 * BUG-652 GRANDFATHERING — pure, idempotent, single-shot migration: a no-op
 * (returns `state` BY REFERENCE) once `needsJobsGrandfather(state)` is
 * false; otherwise applies stampJobsGrandfatherForce() and bumps
 * `economyEpoch` to current in the SAME step.
 *
 * ROUND r3 FIX (F4, coordinator's ruling, 2026-09-04): call sites MUST run
 * this AFTER a save's full journal (snapshot + tail) has finished replaying,
 * never before — if the snapshot's epoch was old, the entire snapshot+tail
 * predates this build's first load by construction (the tail was appended
 * during old-build play, exactly why BUG-617/LARGE_TAIL_REPLAY_THRESHOLD
 * exist: a tail is not a same-session guarantee), so a POST-tail stamp
 * grandfathers a tail-created building of one of the six specs too,
 * deterministically, with no per-action provenance needed. Stamping BEFORE
 * the tail (the r2 defect) bumps the epoch first and permanently closes the
 * fence in front of the tail's own buildings — see replay.ts's
 * restoreFromSavepoint (moved to run after its synchronous tail loop) and
 * prepareRestoreForChunkedTail/replayTailChunked (the two-phase chunked
 * path, which threads needsJobsGrandfather()'s captured boolean through to
 * a stampJobsGrandfatherForce() catch-up pass after the chunked tail
 * completes, since its own early snapshot-stamp already bumped the epoch).
 */
export function stampJobsGrandfather(state: SimState): SimState {
  if (!needsJobsGrandfather(state)) return state;
  return { ...stampJobsGrandfatherForce(state), economyEpoch: JOBS_GRANDFATHER_ECONOMY_EPOCH };
}

/**
 * Aaron ruling 2026-09-04 ("the channel tunnel location needs to be bigger
 * too") — land_tunnel's footprint grew from 3x3 to today's (bigger) spec
 * value. This is the OLD footprint every pre-existing tunnel was actually
 * placed and road-connected against; see `tunnelFootprintEpoch`'s own doc
 * comment (types.ts) for why the epoch guard is load-bearing, not optional.
 */
export const LAND_TUNNEL_LEGACY_FOOTPRINT: Readonly<{ w: number; h: number }> = { w: 3, h: 3 };

/** Current schema version for the land_tunnel footprint migration — bump this (and add a new legacy-dims constant) the next time land_tunnel's base footprint changes. */
export const TUNNEL_FOOTPRINT_GRANDFATHER_EPOCH = 1;

/**
 * Pure, idempotent, single-shot migration (same idiom as
 * stampJobsGrandfather immediately above, deliberately a SEPARATE epoch —
 * see tunnelFootprintEpoch's doc comment for why): a no-op (returns `state`
 * BY REFERENCE) once already at the current epoch; otherwise stamps every
 * EXISTING land_tunnel that carries no per-building footprint override with
 * the OLD 3x3 dims (via the SAME footprintW/footprintH override
 * `footprintOf()` already reads for the auto-scale ladder estates — GR#3,
 * no parallel mechanism) and bumps the epoch. A NEW tunnel placed after
 * this fix carries no override either, but is never touched because by the
 * time it exists the epoch is already current — footprintOf's `?? sp.w/
 * sp.h` fallback then correctly reads the NEW (bigger) spec dims for it.
 */
export function stampTunnelFootprintGrandfather(state: SimState): SimState {
  if ((state.tunnelFootprintEpoch ?? 0) >= TUNNEL_FOOTPRINT_GRANDFATHER_EPOCH) return state;
  const buildings = state.buildings.map((b) =>
    b.spec === 'land_tunnel' && b.footprintW == null && b.footprintH == null
      ? { ...b, footprintW: LAND_TUNNEL_LEGACY_FOOTPRINT.w, footprintH: LAND_TUNNEL_LEGACY_FOOTPRINT.h }
      : b,
  );
  return { ...state, buildings, tunnelFootprintEpoch: TUNNEL_FOOTPRINT_GRANDFATHER_EPOCH };
}

/**
 * FEAT-1972079878 inc1: total jobs capacity, including auto-scaled tiers.
 * For buildings with capacityTiers, uses capacityAtTier(sp, building.capacityTier ?? 0).
 * For others, uses sp.jobs or default commercial/industrial job counts.
 * BUG-652: routed through jobsAtTier() (above) so a spec that also carries
 * residents/children/served (hea_teaching, uni, station_ashford) can never
 * have its job count misread as that OTHER field's tier value.
 */
// BUG-525 — before this fix totalJobs() summed EVERY job building
// regardless of activation state, while onlineResidentsCapacity() (data.ts
// ~1596) IS gated — an inconsistency that let an offline / road-disconnected
// / under-construction factory, office, or shop contribute full job
// capacity (feeding employment ratios, commuter/office splits, and the
// jobs-capacity pass in engine.ts) while correctly paying zero upkeep.
// Mirror the powerStats() gate exactly: `if (!isOnline(s, b)) continue;`
// inside the building loop (data.ts ~1918). Order-independent fold (GR#21).
// BUG-622: memoised — utilisationOf calls this per building from MapView's
// draw pass (the measured O(n^2) / ~14s-per-redraw class at 13k buildings).
// FEAT-2326609763: off_tower_canary/off_tower_marina need NOTHING extra
// here — they carry `jobs` as their ONLY capacity field (no residents/
// children/served alongside it), the exact shape off_businesspark/
// off_towers_downtown already have, so effectiveJobsOf()'s ordinary
// jobsAtTier() path reads their tier-grown job count correctly
// (10,000->23,579 / 20,000->47,159 across the ladder). BUG-652 (landed with
// this merge): specs carrying jobs ALONGSIDE another capacity field
// (hea_teaching/uni/station_ashford) keep jobs FLAT via jobsAtTier(), and a
// grandfathered pre-epoch building's jobsOverride wins via effectiveJobsOf()
// — see those functions' own doc comments above.
export const totalJobs: (s: SimState) => number = memoOnState((s) => {
  let jobs = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (!sp) continue;
    if (sp.jobs) jobs += effectiveJobsOf(sp, b);
    else if (sp.kind === 'commercial') jobs += 12;
    else if (sp.kind === 'industrial') jobs += 18;
  }
  return jobs;
});

/**
 * FEAT-2326609740 §11 — total 'children' capacity across every online school
 * building, tier-aware (same shape as residentsCapacity/totalJobs above:
 * capacityAtTier honours a scaled building's current tier, GR#3 one growth
 * rule reused everywhere). Feeds evaluateBuildingMonitors' 'children' monitor
 * utilization denominator.
 *
 * BUG-652: uni now carries a `jobs` field alongside `children` with no
 * capacityTiers ladder of its own — capacityAtTier()'s bare fallback
 * (`sp.residents ?? sp.jobs ?? 0`) would silently read uni's JOB count as
 * its CHILDREN capacity the instant `jobs` went truthy. Read `children`
 * directly when there is no ladder to scale (same guard idiom the power-kind
 * `mw` read already uses just above, data.ts ~2725 `sp.capacityTiers ?
 * capacityAtTier(...) : sp.mw ?? 0`); a spec WITH capacityTiers (every
 * school spec that actually auto-scales — edu_nursery/edu_primary/edu_city/
 * edu_tech) keeps the exact prior capacityAtTier() behaviour, unchanged.
 * Byproduct fix: col_sixth/uni were previously silently undercounted at 0
 * children capacity via the same bare-fallback gap (children was never in
 * capacityAtTier's no-tiers fallback at all) — now correctly counted.
 */
export const totalChildrenCapacity: (s: SimState) => number = memoOnState((s) => {
  let cap = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.kind !== 'school') continue;
    cap += childrenAtTier(sp, b.capacityTier ?? 0);
  }
  return cap;
});

/**
 * FEAT-2326609740 §11 — total 'served' capacity across every online health +
 * police building, tier-aware. Feeds evaluateBuildingMonitors' 'served'
 * monitor utilization denominator (health and police share one monitor type
 * because both use `served` as their capacity field — same field, same
 * aggregate, per Aaron's Q100083 service-spec scope).
 */
export const totalServedCapacity: (s: SimState) => number = memoOnState((s) => {
  let cap = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.kind === 'health' || sp?.kind === 'police') cap += servedAtTier(sp, b.capacityTier ?? 0);
  }
  return cap;
});

/**
 * BUG-397 (Aaron's ruling, 2026-08-31, shape 1): total population 'served' by
 * every online transit-family building (kind 'transport' — bus_station/
 * metro_station/grand_terminus/etc — AND kind 'station' — station_ashford —
 * both bucket into the SAME 'Transport' UPKEEP_BUCKET label, fiscal.ts). Mirrors
 * totalServedCapacity's exact shape (isOnline gate, servedAtTier tier-aware
 * read) one section above, just over the transit kinds instead of
 * health/police. Feeds engine.ts's computeFlows() Transit Fare Revenue line:
 * riders = min(population, transitServedCapacity(s)) — a building with no
 * `served` field (e.g. bus_stop) contributes 0 capacity, same fail-soft
 * posture as the rest of this file's served-based aggregates.
 */
export const transitServedCapacity: (s: SimState) => number = memoOnState((s) => {
  let cap = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.kind === 'transport' || sp?.kind === 'station') cap += servedAtTier(sp, b.capacityTier ?? 0);
  }
  return cap;
});

/**
 * FEAT-wage-stage1 (Q100067/Q100086, 2026-09-03) — the SAME building-jobs
 * basis as totalJobs() immediately above (identical loop shape, identical
 * BUG-525 isOnline gate, identical sp.jobs/commercial/industrial-fallback
 * job-count rule — GR#3, no re-derivation of the counting rule), but
 * bucketed by wage sector via fiscal.ts's KIND_TO_WAGE_SECTOR SSOT map
 * instead of summed to one grand total. Feeds engine.ts's computeFlows() so
 * the wage outflow line can be computed from fiscal.ts's sectorWagesPerTick()
 * instead of the old flat population-based wagesPerTick().
 *
 * A job-bearing building whose kind has no KIND_TO_WAGE_SECTOR entry
 * contributes to neither bucket NOR the grand total mismatch: every kind
 * that can carry `sp.jobs` in today's live catalogue (commercial, office,
 * industrial, mine, transport) IS mapped — asserted by a test
 * (KIND_TO_WAGE_SECTOR coverage test in wage-sector-bands.test.mjs) — so
 * `Object.values(totalJobsBySector(s)).reduce(sum) === totalJobs(s)` holds
 * for the live catalogue; an unmapped future kind would silently underrepresent
 * only the wage total, never crash, matching this codebase's fail-soft
 * posture for cosmetic/derived aggregates (a hard-fail here would take down
 * the whole tick over a wage line).
 *
 * Memoised (memoOnState) — mirrors countByKindOnline/serviceCapacityAggregates
 * immediately below/above: this walks the FULL buildings array exactly like
 * totalJobs(), and computeFlows() calling it every tick at city scale (the
 * scale-gate's 13k-building bound) must not double the per-tick building-walk
 * cost that totalJobs() itself already pays once.
 *
 * F5 HARDENING (independent round, 2026-09-03): memoOnState caches this
 * object by STATE REFERENCE and hands the SAME object back to every caller
 * on the money path (engine.ts computeFlows / consistency.ts recompute) —
 * without freezing, one careless caller mutating its copy (`result.tertiary
 * += x`) would corrupt every OTHER caller's cached read for that same tick,
 * silently, with no error. Object.freeze() at the return boundary makes a
 * mutation attempt throw (this module runs under ES-module strict mode)
 * instead of silently corrupting the shared cache — a mutation test in
 * wage-sector-bands.test.mjs proves this.
 */
export const totalJobsBySector: (s: SimState) => SectorJobs = memoOnState((s) => {
  const bySector: SectorJobs = { ...ZERO_SECTOR_JOBS };
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (!sp) continue;
    let jobs = 0;
    // BUG-652: jobsAtTier() (see totalJobs() above) — never capacityAtTier()
    // directly — so hea_teaching/uni/station_ashford's job count can't be
    // misread as their served/children tier value.
    if (sp.jobs) jobs = effectiveJobsOf(sp, b);
    else if (sp.kind === 'commercial') jobs = 12;
    else if (sp.kind === 'industrial') jobs = 18;
    if (jobs <= 0) continue;
    const sector = KIND_TO_WAGE_SECTOR[sp.kind];
    if (!sector) continue;
    bySector[sector] += jobs;
  }
  return Object.freeze(bySector);
});

/**
 * F1 FIX (independent round REJECT, 2026-09-03, blocking money defect): the
 * ORIGINAL Stage-1 wiring fed sectorWagesPerTick() raw job CAPACITY
 * (totalJobsBySector() above, vacancy-inclusive) — a population-0 city with
 * one off_towers_downtown (2,000 vacant job slots) was charged £113,333/tick,
 * and the 13k-building scale fixture (3.0M job-slot capacity against a
 * 1.42M-pop city) paid 2.49x the old flat formula. Jobs that exist only as
 * unfilled VACANCIES were being paid a wage as if a person occupied them.
 *
 * filledJobsBySector() is the CORRECTED basis engine.ts/consistency.ts must
 * use instead of totalJobsBySector() for anything money-facing:
 *   workers = population * WORKING_AGE_FRACTION (this file's own SSOT,
 *             already unemploymentOf()'s basis — GR#3, no second working-age
 *             constant)
 *   filled  = round(clamp(min(workers, totalCapacity), 0, totalCapacity))
 *             — the ONE integer-rounding point (you can't pay half a worker);
 *             everything downstream is exact apportionment, never re-rounded.
 *   result  = allocateFilledJobs(filled, capacity) — fiscal.ts's deterministic
 *             largest-remainder apportionment (see its own doc comment for
 *             the full rule + the three edge cases: empty city -> £0 all
 *             round; jobs > workers -> pays exactly `workers`, capacity-
 *             weighted; workers > jobs -> pays exactly `totalCapacity`,
 *             i.e. every sector's own capacity verbatim, zero remainder).
 *
 * Memoised (memoOnState) exactly like totalJobsBySector() — this is a THIN
 * wrapper around the already-memoised capacity walk (no second buildings-array
 * pass), so it adds a fixed O(4) apportionment cost per tick, not another
 * O(buildings) walk, keeping the scale-gate bound intact. Frozen at the
 * return boundary for the same F5 shared-cache-corruption reason as
 * totalJobsBySector() (allocateFilledJobs already returns a fresh object, but
 * freezing here too keeps the invariant "every SectorJobs this module hands
 * out on the money path is immutable" uniform and cheap to audit).
 *
 * SCALE-GATE FIX (independent round, 2026-09-03 — found by the 13k-building
 * fixture's flows.wages-matches check, a real BUG-419-class timing bug, NOT a
 * new invention): computeFlows(s) inside advance() runs against the
 * START-of-tick `s.population` (migration/growth for THIS tick applies
 * LATER in advance(), producing a NEW `population` local — see BUG-419's own
 * comment trail in engine.ts/consistency.ts), and that start-of-tick figure
 * is snapshotted onto `lastFlows.population`. consistency.ts's Wages
 * recompute must cap workers against that SAME snapshotted population, not
 * whatever `s.population` happens to read at CHECK time (which, after even
 * one settled tick, is already the GROWN end-of-tick figure) — otherwise the
 * recompute silently overestimates the workforce and floods the check red on
 * every real city. `filledJobsFromCapacityAndPopulation()` below is the
 * UNMEMOISED, population-parameterised core so consistency.ts can pass the
 * correct historical basis in; `filledJobsBySector()` is the memoised
 * per-tick convenience wrapper that always uses `s.population` (correct for
 * engine.ts's OWN call site, which calls computeFlows/filledJobsBySector on
 * exactly the same start-of-tick `s` that later gets snapshotted).
 */
export function filledJobsFromCapacityAndPopulation(
  capacity: SectorJobs,
  population: number,
): SectorJobs {
  const totalCapacity =
    capacity.primary + capacity.secondary + capacity.tertiary + capacity.public;
  const workers = population * WORKING_AGE_FRACTION;
  const filled = Math.max(0, Math.round(Math.min(workers, totalCapacity)));
  return Object.freeze(allocateFilledJobs(filled, capacity));
}

export const filledJobsBySector: (s: SimState) => SectorJobs = memoOnState((s) =>
  filledJobsFromCapacityAndPopulation(totalJobsBySector(s), s.population),
);

/**
 * BUG-524 (Q100046 C1) — unemployment / jobs-deficit measure, the SSOT
 * (GR#3) consumed by BOTH wellbeingOf's new "Jobs/Employment" part and,
 * indirectly, move-out (engine.ts feeds unemployment into wellbeing only —
 * see the no-double-count note at engine.ts wellbeingOf). Mirrors the
 * `serviceCoverageOf` 'commercial'/'office'/'industrial'/'mine' basis
 * (workers = population * 0.55, PLACEHOLDER working-age fraction per the
 * audit) but expressed as a 0..1 unemployment RATE rather than a coverage
 * ratio, since "jobs > workers" (full employment plus vacancies) must clamp
 * to 0% unemployment, not a negative rate.
 *
 * unemployment = max(0, workers - jobs) / workers. No workers (population 0)
 * ⇒ 0 (nobody is unemployed in an empty city — avoids a 0/0 NaN).
 * Pure / order-independent (GR#21): jobs comes from totalJobs() which is
 * already isOnline-gated (BUG-525), so an offline factory contributes no
 * jobs here either.
 */
export const WORKING_AGE_FRACTION = 0.55; // PLACEHOLDER-balance, matches serviceCoverageOf's jobs basis.

export function unemploymentOf(s: SimState): number {
  const workers = s.population * WORKING_AGE_FRACTION;
  if (workers <= 0) return 0;
  const jobs = totalJobs(s);
  return Math.max(0, workers - jobs) / workers;
}

// ---------- per-service coverage: SINGLE SOURCE OF TRUTH (BUG-392, GR#3) ----
//
// Before BUG-392 the demand meters (here) and the wellbeing breakdown
// (engine.ts wellbeingOf) each computed their own coverage with DIFFERENT
// formulas AND mismatched units — demand compared facility COUNTS against
// population SERVED (e.g. need = pop/800 clinics vs cap = 5,000 people per
// clinic), so one clinic pegged every meter at ±100 while wellbeing's clamp
// read the same mismatch as "great". Both systems now consume the ratios
// produced by serviceCoverageOf() and can never contradict each other again.

export interface ServiceCoverage {
  id: string;
  label: string;
  /** Requirement, in the SAME unit as `cap`: people for served-based services
   *  (GP/hospital/police/water/sewage), school places for education, MW for
   *  power. Never mix a facility count with a population. */
  need: number;
  /** Installed capacity in the same unit as `need`. */
  cap: number;
  /** cap / need, unclamped (may exceed 1 on oversupply); defined as 1 when
   *  need is 0 — nothing required means fully covered. */
  coverage: number;
  /** Palette spec the auto-builder should place to raise this coverage. */
  spec: string;
}

/**
 * ⚠ BALANCE-NUMBER REGIME (Aaron's blanket rule): every `need` rate below
 * (0.06 / 0.12 / 0.05 of population for school places; whole-population reach
 * for GP/hospital/police/fire/water) is a PLACEHOLDER — directional only,
 * pending Aaron's row-by-row balance pass.
 */
export function serviceCoverageOf(s: SimState): ServiceCoverage[] {
  const pop = s.population;
  // FEAT-webworker-sim-offload Stage 0 (2026-09-02): the nursery/primary/
  // tertiary/gp/hosp/police/fire/clean/waste sums used to be 7 separate
  // sumBy() calls plus an inline clean/waste loop — 8 full O(buildings)
  // passes per serviceCoverageOf() call. serviceCapacityAggregates() folds
  // all of them into ONE pass (memoised per buildings/roadConnectivity/tick
  // triple), with identical predicates/gates (BUG-399/BUG-526/BUG-534) to
  // what each sumBy() call did individually — see its definition above for
  // the shared isOnline gate this preserves exactly.
  const agg = serviceCapacityAggregates(s);
  const { nursery, primary, tertiary, gp, hosp, police, fire, clean, waste } = agg;
  const pw = powerStats(s);

  const row = (id: string, label: string, need: number, cap: number, spec: string): ServiceCoverage =>
    ({ id, label, need, cap, coverage: need <= 0 ? 1 : cap / need, spec });

  return [
    row('nursery', 'Nursery (0–4)', pop * 0.06, nursery, 'edu_nursery'),
    row('primary', 'School (5–15)', pop * 0.12, primary, pop * 0.12 > 1200 ? 'edu_city' : 'edu_primary'),
    row('college', 'College (16–19)', pop * 0.05, tertiary, pop * 0.05 > 3000 ? 'uni' : 'col_sixth'),
    // Served-based services: need = whole population, cap = Σ spec.served.
    // (The pre-BUG-392 need was a facility count — pop/800 etc. — compared
    // against population served: a ~5,000× unit mismatch that pegged meters.)
    row('gp', 'GP clinics', pop, gp, 'hea_clinic'),
    row('hosp', 'Hospital', pop, hosp, 'hea_hospital'),
    row('police', 'Police', pop, police, 'pol_station'),
    // BUG-571: recommended spec is now unlock-aware via optimalProvider() —
    // no more hardcoded 'fire_station' (which stays locked at low city
    // levels). Fallback literal only for the pathological all-locked case
    // (mirrors pickAutoSpec/DemandDock's existing "needs unlock" null-handling).
    row('fire', 'Fire cover', pop, fire, optimalProvider(s, 'fire', s.funds, pop - fire)?.id ?? 'fire_post'),
    row('cleanwater', 'Clean water', pop, clean, 'wat_clean'),
    row('waste', 'Sewage', pop, waste, 'wat_waste'),
    row('power', `Power (${formatPower(pw.cap)}/${formatPower(pw.need)})`, pw.need, pw.cap, pw.need - pw.cap > 60 ? 'pow_coal' : 'pow_wind'),
    // BUG-572 follow-up (Aaron: "cross check ALL resources are listed"):
    // Parks & leisure already has a REAL coverage formula in this file
    // (crimeRateOf's Parks reducer, above) and a second copy in engine.ts's
    // wellbeingOf() — genuinely modelled, exactly the class of gap BUG-572's
    // refuse row closed, but never folded into a serviceCoverageOf() row so
    // it never reached the DemandDock. need = pop*0.002 WITHOUT crimeRateOf's
    // `Math.max(1, …)` floor deliberately: every other row here reads need=0
    // at population 0 (row()'s own `need<=0 ⇒ coverage=1` guard), and that
    // floor exists only to keep crimeRateOf's/wellbeingOf's OWN division
    // defined at pop 0 — reusing it here would manufacture a permanent
    // "needs 1 unit of park" demand in an empty city that no other resource
    // has. Deliberately NOT refactored to share crimeRateOf's/wellbeingOf's
    // exact computation: crimeRateOf is independently round-verified and
    // wellbeingOf lives in engine.ts (another lane's surface) — same capacity
    // predicate (sp.kind==='park', Σ w×h), different need floor, by design.
    row('parks', 'Parks & leisure', pop * 0.002, parksCapacityOf(s), optimalProvider(s, 'parks', s.funds, pop * 0.002 - parksCapacityOf(s))?.id ?? 'park'),
  ];
}

/**
 * Parks footprint capacity (Σ w×h over every 'park'-kind building), the SAME
 * sum crimeRateOf's Parks reducer and engine.ts's wellbeingOf() Parks &
 * leisure part each compute inline — extracted here only so the new
 * serviceCoverageOf() 'parks' row (above) doesn't hand-roll a FOURTH copy of
 * this loop. Deliberately NOT online-gated: matches the pre-existing
 * crimeRateOf/wellbeingOf behaviour exactly, so calling this from a new site
 * cannot silently change what those two already-verified computations do.
 * Pure/deterministic (GR#21): unconditional forward scan, no early break.
 *
 * BUG-643 — memoOnState. Called TWICE in the same serviceCoverageOf() 'parks'
 * row expression (once for `cap`, once again inside the optimalProvider shortfall
 * argument) plus again from crimeRateOf/engine.ts's wellbeingOf's own Parks &
 * leisure term every tick — a hidden O(buildings) fan-out with no memoisation.
 * Exported (previously module-private) only so this file's own identity-test
 * suite can compare it directly against a re-implemented oracle; no other
 * behaviour change.
 */
export const parksCapacityOf: (s: SimState) => number = memoOnState((s) => {
  let capacity = 0;
  // FOLLOW-UP (r3 round note (b), non-blocking): sp.w*sp.h, not
  // footprintOf(b, sp) — harmless ONLY because no park spec carries a
  // capacityTiers ladder today (parks never grow via this feature). Would
  // need fixing the day a park spec joins the ladder; not fixed here (out
  // of scope for the F5s-only re-round).
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind === 'park') capacity += sp.w * sp.h;
  }
  return capacity;
});

/**
 * Demand index from a coverage ratio: a monotone, bounded map of
 * (1 - coverage). Positive = shortfall (demand), negative = surplus.
 *   coverage 1.0 → 0, 0.8 → +20, 0.5 → +50, 0 → +100, ≥2 → -100.
 * It only approaches +100 as coverage approaches 0 — 80% coverage reads +20,
 * never a pegged +100 (the BUG-392 saturation).
 * ⚠ BALANCE-NUMBER PLACEHOLDER: the linear 100·(1-coverage) curve and the
 * ±100 clamp are directional only, pending Aaron's balance pass.
 */
export const demandIndexOf = (coverage: number): number =>
  Math.round(Math.max(-100, Math.min(100, 100 * (1 - coverage))));

/** Early-game damping so a near-empty map doesn't scream demand.
 *  ⚠ BALANCE-NUMBER PLACEHOLDER (pop/50 ramp), pending Aaron's balance pass. */
export const earlyGameFactor = (pop: number): number => Math.min(1, pop / 50);

/**
 * FEAT-crime-mechanic-2026-09-02 (Q100046 D2-now + Q100069 rec-on-all) —
 * PLACEHOLDER balance constants, grouped in one named object per the spec so
 * Aaron's future balance-pass row replaces all seven in a single commit.
 * BASELINE_CRIME_RATE is UK ONS-grounded (mid-size English urban area,
 * 2024-25 Crime Survey average); every reduction/feedback constant below is
 * directional-only pending playtest data.
 */
export const CRIME_CONSTANTS = {
  /** Ambient crime a city with ZERO services still has (crimes/100k/month equivalent). */
  BASELINE_CRIME_RATE: 35,
  /** "Crime breeds crime": each point of prior-month crime adds this fraction next month. */
  CRIME_BREEDS_CRIME_FACTOR: 0.05,
  /**
   * Hard cap on the breeding term alone. PLACEHOLDER (Aaron's balance pass) —
   * deliberately set BELOW the term's own natural ceiling (FACTOR * 100 = 5,
   * since priorCrime is itself clamped to [0,100] by sanitizeCrimeRate) so
   * the cap actually BINDS and is testable (BUG-round-1 F3: the original 30
   * placeholder was unreachable dead code — the term can never exceed 5, so
   * a cap of 30 did nothing and no test could distinguish "capped" from
   * "uncapped"). Binds whenever priorCrime > CRIME_BREEDS_CRIME_CAP /
   * CRIME_BREEDS_CRIME_FACTOR = 60. Boundedness ALSO comes independently
   * from the final [0,100] clamp on the whole crime formula (AC-6) — this
   * cap is a SEPARATE, tighter guard on the breeding term specifically, not
   * the only thing standing between the model and runaway.
   */
  CRIME_BREEDS_CRIME_CAP: 3,
  /** Points of crime eliminated at 100% police coverage, scaling linearly to 0 at 0%. */
  POLICE_REDUCTION_FACTOR: 25,
  /** Points of crime eliminated at 100% education coverage (avg of nursery/primary/college). */
  EDUCATION_REDUCTION_FACTOR: 15,
  /** Points of crime eliminated at 100% parks coverage. */
  PARKS_REDUCTION_FACTOR: 12,
  /** Extra crime points added per point of wellbeing BELOW 100 (i.e. (100-wb) * this). */
  WELLBEING_CRIME_FACTOR: 0.15,
} as const;

/**
 * FEAT-crime-mechanic-2026-09-02 round-1 F1 (P1, GR#16) — Type-Safe Storage
 * Boundary sanitiser for `s.crimeRatePreviousMonth`, same shape as fiscal.ts's
 * `sanitizeFunds`: a corrupt save can hand back ANY JSON-representable value
 * for a `number`-typed field (a string, an object, NaN survives one lossy
 * round-trip as `null`, `1e9`, etc.) — TypeScript's `number` annotation is
 * a compile-time promise only, never a runtime guarantee for data loaded from
 * outside the program. Without this guard a corrupt value flows straight into
 * arithmetic: `"abc" * 0.05` is `NaN`, which then poisons `crimeRateOf`'s
 * return, `wellbeingOf().overall`, and — after ONE month of `advance()` —
 * `population`/`funds` (both computed from wellbeing-derived rates). Mirrors
 * `sanitizeFunds`'s contract: non-finite/non-number collapses to a safe
 * default (BASELINE_CRIME_RATE, this field's own documented old-save
 * default — types.ts) rather than 0, and an in-range-but-absurd value (a
 * corrupt `1e9`) is clamped into [0,100] like any other crime rate.
 */
export function sanitizeCrimeRate(n: unknown): number {
  if (typeof n !== 'number' || !Number.isFinite(n)) return CRIME_CONSTANTS.BASELINE_CRIME_RATE;
  return Math.max(0, Math.min(100, n));
}

/**
 * FEAT-crime-mechanic-2026-09-02 — crime rate, 0-100 clamped, representing
 * crime incidents per 100k population equivalent. A PURE, order-independent
 * derived read-out (GR#21): no Math.random(), no Date.now(); the parks loop
 * below is an unconditional forward scan over s.buildings (no early break —
 * the map-range-with-break trap), and serviceCoverageOf() already returns a
 * stable, insertion-order-independent array (GR#3 SSOT — same coverage rows
 * wellbeingOf()/serviceDemandOf() consume).
 *
 * LOOP-BREAKING DESIGN (build-note requirement; both breakers are required —
 * removing either one reintroduces a cycle):
 *
 * 1. "Crime breeds crime" reads `s.crimeRatePreviousMonth`, a value
 *    engine.ts's advance() snapshots ONLY at month boundaries (tick %
 *    TICKS_PER_MONTH === 0), taken from the tick's OWN freshly-computed
 *    crimeRateOf() result — but written into the RETURNED state's field for
 *    NEXT month, never read back within the same tick. So the breeding term
 *    is always a genuine one-month LAG (self-reinforcement across time), not
 *    same-tick self-reference — it cannot diverge in a single evaluation,
 *    and the explicit `CRIME_BREEDS_CRIME_CAP` bounds it further even across
 *    ticks (AC-6).
 * 2. The wellbeing-feedback input reads engine.ts's `wellbeingCoreOf(s)` —
 *    the wellbeing computation with every part EXCEPT Crime — never
 *    `wellbeingOf(s)` (which itself calls crimeRateOf() to build the Crime
 *    part). wellbeingCoreOf() contains no call back into crimeRateOf(), so
 *    the call graph is strictly one-way: wellbeingOf -> crimeRateOf ->
 *    wellbeingCoreOf, with wellbeingCoreOf as a leaf. No recursion is
 *    possible even though "crime lowers wellbeing" and "low wellbeing raises
 *    crime" are both modelled truths.
 */
// BUG-602: memoised — wellbeingOf, the month-boundary snapshot, debugjson and
// the UI all evaluate crime against the same state object; the body walks the
// full building list and recomputes wellbeingCoreOf. Pure derivation of s.
export const crimeRateOf: (s: SimState) => number = memoOnState((s) => {
  const {
    BASELINE_CRIME_RATE,
    CRIME_BREEDS_CRIME_FACTOR,
    CRIME_BREEDS_CRIME_CAP,
    POLICE_REDUCTION_FACTOR,
    EDUCATION_REDUCTION_FACTOR,
    PARKS_REDUCTION_FACTOR,
    WELLBEING_CRIME_FACTOR,
  } = CRIME_CONSTANTS;

  const pop = s.population;
  // Early-game damping — same earlyGameFactor ramp demand/wellbeing use
  // (GR#3), so a near-empty genesis city starts with damped baseline crime
  // rather than the full adult-city ambient rate.
  const f = earlyGameFactor(pop);
  const baseline = Math.round(BASELINE_CRIME_RATE * f);

  // F1 (GR#16): sanitize at the boundary, not just null/undefined-guard —
  // a corrupt save's crimeRatePreviousMonth may be any JSON value.
  const priorCrime = sanitizeCrimeRate(s.crimeRatePreviousMonth);
  const breedingTerm = Math.min(priorCrime * CRIME_BREEDS_CRIME_FACTOR, CRIME_BREEDS_CRIME_CAP);

  const covById = new Map(serviceCoverageOf(s).map((r) => [r.id, r.coverage]));
  const policeCov = Math.min(1, covById.get('police') ?? 1);
  const eduCov =
    (Math.min(1, covById.get('nursery') ?? 1) +
      Math.min(1, covById.get('primary') ?? 1) +
      Math.min(1, covById.get('college') ?? 1)) /
    3;

  // Parks coverage — same capacity/need formula wellbeingOf()'s Parks &
  // leisure part and the serviceCoverageOf() 'parks' row (BUG-572 follow-up,
  // below) use (GR#3 SSOT). parksCapacityOf() is the shared Σw×h sum; the
  // `Math.max(1, …)` need floor stays LOCAL to this function deliberately —
  // it exists only so THIS division is defined at pop 0, and the demand row
  // intentionally does NOT apply it (see that row's comment).
  const parksCapacity = parksCapacityOf(s);
  const parksNeed = Math.max(1, pop * 0.002);
  const parksCov = Math.min(1, parksCapacity / parksNeed);

  const policeReduction = policeCov * POLICE_REDUCTION_FACTOR;
  const eduReduction = eduCov * EDUCATION_REDUCTION_FACTOR;
  const parksReduction = parksCov * PARKS_REDUCTION_FACTOR;

  // See loop-breaking design note above: wellbeingCoreOf, never wellbeingOf.
  const wbCore = wellbeingCoreOf(s);
  const wellbeingReduction = Math.max(0, (100 - wbCore) * WELLBEING_CRIME_FACTOR);

  const crime =
    baseline + breedingTerm - policeReduction - eduReduction - parksReduction + wellbeingReduction;

  return Math.round(Math.max(0, Math.min(100, crime)));
});

export function serviceDemandOf(
  s: SimState
): { id: string; label: string; value: number; spec: string; alert?: boolean }[] {
  const f = earlyGameFactor(s.population);
  const rows = serviceCoverageOf(s).map((c) => {
    if (c.id !== 'power') {
      return { id: c.id, label: c.label, value: Math.round(demandIndexOf(c.coverage) * f), spec: c.spec };
    }
    // BUG-392 × BUG-393 seam. While power has NO deficit (coverage ≥ 1, or
    // nothing needs power) it rides the shared demandIndexOf curve like every
    // other service. A power DEFICIT (need > cap ⇔ coverage < 1) is
    // qualitatively WORSE than an ordinary coverage shortfall — the city is
    // browning out — so the index escalates instead: floored at
    // BROWNOUT_INDEX_FLOOR, climbing by BROWNOUT_INDEX_SLOPE per unit
    // deficitRatio (= 1 - coverage, since the power row's coverage is
    // cap/need — same quantity brownoutOf derives), clamped at 100. The
    // deficit branch deliberately skips the population ramp `f`: a brownout
    // is a brownout however small the town. `alert` drives the DemandDock
    // banner + row highlight. Curve constants are PLACEHOLDER (balance regime).
    //
    // FEAT-2326609711 inc1 fix: routed through isBrownoutActive() (this
    // file's SSOT, GR#3) instead of recomputing the raw coverage<1 test
    // locally — while Grid Import cover is ON a covered shortfall is bought
    // in, not a brownout, so this row must not escalate or raise `alert`
    // ("— BROWNOUT" tooltip) either.
    const deficit = c.need > 0 && c.coverage < 1 && isBrownoutActive(s);
    const value = deficit
      ? Math.round(Math.min(100, BROWNOUT_INDEX_FLOOR + (1 - c.coverage) * BROWNOUT_INDEX_SLOPE))
      : Math.round(demandIndexOf(c.coverage) * f);
    return { id: c.id, label: c.label, value, spec: c.spec, alert: deficit };
  });
  // BUG-572 AC-1: fold the refuse/collection row (wasteStatsOf's twin coverage
  // metric — demandFixPlan() already has a 'refuse' entry, DemandDock never had
  // a row to attach it to) using the SAME demandIndexOf curve every non-power
  // row uses. Spec is unlock-aware via optimalProvider (no hardcoded literal);
  // 'waste_depot' fallback is the only refuse-capable spec today, matching the
  // fire row's pathological-all-locked fallback pattern above.
  const waste = wasteStatsOf(s);
  const refuseRow = {
    id: 'refuse',
    label: 'Refuse',
    value: Math.round(demandIndexOf(waste.coverage) * f),
    spec: optimalProvider(s, 'refuse', s.funds, waste.generated - waste.capacity)?.id ?? 'waste_depot',
  };
  return [...rows, refuseRow];
}

// ════════════════════════════════════════════════════════════════════════════
// FEAT-1972079906 inc1 — GARBAGE / WASTE: GENERATION + COLLECTION COVERAGE +
// WASTE-HEALTH SIGNAL.
//
// Waste generation, collection-depot coverage, and the derived collection OPEX
// basis. ALL functions are PURE + DETERMINISTIC (GR#21): order-independent sums
// over buildings, no Date.now / Math.random, no map-range-with-break. Every value
// is a DERIVED read-out — NOTHING is stored in SimState, so it cannot drift or
// break the consistency checker / genesis-replay (round-trips trivially).
//
// SCOPE (brief §5 inc1): single residual stream, collection only. Processing mix,
// diversion %, recycling/EfW/compost revenue, and the landfill dial are inc2 and
// are deliberately NOT modelled here.
//
// ⚠ BALANCE-NUMBER REGIME (Aaron's blanket rule): every per-resident / per-job
// waste rate, the depot collection capacity (on the spec), and the collection
// OPEX rate below are PLACEHOLDERS — directional only, pending Aaron's row-by-row
// balance pass. Do not tune gameplay against these numbers.
// ════════════════════════════════════════════════════════════════════════════

/** Household refuse per housed resident, tonnes/tick. PLACEHOLDER-balance. */
export const WASTE_PER_RESIDENT = 0.01;
/** Commercial/industrial/office/mine refuse per job, tonnes/tick. PLACEHOLDER-balance. */
export const WASTE_PER_JOB = 0.02;
/** £ charged per tonne of refuse actually COLLECTED (the rounds OPEX). PLACEHOLDER-balance. */
export const COLLECTION_OPEX_PER_TONNE = 12;

/**
 * Effective jobs a workplace spec carries — the SAME rule totalJobs() sums (GR#3
 * SSOT): an explicit `jobs`, else the commercial/industrial category defaults.
 * Offices and mines always carry an explicit `jobs`.
 */
function specJobs(sp: Spec): number {
  if (sp.jobs) return sp.jobs;
  if (sp.kind === 'commercial') return 12;
  if (sp.kind === 'industrial') return 18;
  return 0;
}

/**
 * Waste GENERATED this tick, tonnes (brief §1). Households (residential capacity,
 * the housing-stock proxy for households) + commerce/industry/office/mine jobs.
 * Only ONLINE buildings contribute (isOnline) — an offline building generates no
 * waste, exactly like it earns/consumes nothing. Order-independent sum, pure.
 *
 * NOTE (household basis): inc1 ties household refuse to residential CAPACITY, not
 * live population, so the figure is a per-building online-gated quantity (a needed
 * property — "offline buildings generate no waste"). A live-occupancy refinement
 * (population / capacity) is a later increment. PLACEHOLDER-balance rates.
 *
 * BUG-643 (tier 2 of BUG-642) — memoOnState (WeakMap<SimState, T> keyed on the
 * state object itself, the house idiom — see memoOnState's doc comment). This is
 * the base of the waste-family CHAIN: wasteStatsOf calls this + collectionCapacityOf,
 * processingMixOf calls wasteStatsOf, and computeFlows/DemandDock/demandFixPlan each
 * call several of those PER TICK — before this fix the waste family alone measured
 * 3,176ms INCLUSIVE on Aaron's 29,831-building city. Pure function of
 * (s.buildings, isOnline(s,·)) only, so the state-identity key is sound by the
 * same reasoning as waterCaps'/serviceCapacityAggregates' (every mutating reducer
 * case returns a fresh top-level state object — verified above, unchanged here).
 */
export const wasteGeneratedOf: (s: SimState) => number = memoOnState((s) => {
  let tonnes = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (!sp) continue;
    if (sp.kind === 'residential') {
      tonnes += (sp.residents ?? 8) * WASTE_PER_RESIDENT;
    } else if (
      sp.kind === 'commercial' ||
      sp.kind === 'office' ||
      sp.kind === 'industrial' ||
      sp.kind === 'mine'
    ) {
      tonnes += specJobs(sp) * WASTE_PER_JOB;
    }
  }
  return tonnes;
});

/**
 * Total refuse COLLECTION capacity this tick, tonnes (Σ online depot wasteCapacity).
 * Only ONLINE depots collect — an under-construction / disconnected depot runs no
 * rounds. Order-independent sum, pure.
 *
 * BUG-643 — memoOnState, same reasoning as wasteGeneratedOf() immediately above.
 */
export const collectionCapacityOf: (s: SimState) => number = memoOnState((s) => {
  let cap = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.wasteCapacity) cap += sp.wasteCapacity;
  }
  return cap;
});

/** Derived waste read-out for a state (brief §2). All quantities tonnes/tick. */
export interface WasteStats {
  /** Refuse produced by online buildings. */
  generated: number;
  /** Online depot collection capacity. */
  capacity: number;
  /** Actually collected = min(generated, capacity). */
  collected: number;
  /** capacity / generated clamped to [0,1]; 1 when nothing is generated (nothing
   *  to collect ⇒ fully covered — the water-coverage convention, need 0 ⇒ 1). */
  coverage: number;
  /** generated − collected (tonnes left on the street). */
  uncollected: number;
  /** 1 − coverage: the fraction driving the waste-health penalty. */
  uncollectedFraction: number;
}

/**
 * Collection coverage + uncollected tonnage (brief §2), the twin of waterCaps /
 * serviceCoverageOf. coverage = min(1, capacity/generated); no depots and any
 * generation ⇒ coverage 0 (everything uncollected). Pure/deterministic.
 *
 * BUG-643 — memoOnState. wasteGeneratedOf/collectionCapacityOf are already
 * memoised (O(1) after their own first call this state), so wrapping this too
 * is cheap insurance rather than the main win — but it is called from
 * demandFixPlan, DemandDock's refuse row, AND processingMixOf every tick
 * (several times each), so caching the whole {generated,capacity,collected,...}
 * object avoids re-deriving the trivial arithmetic and — per GR#21's "same
 * reference on a repeat call proves a real cache hit" test — gives every
 * caller within one state THE SAME object reference, matching the waterCaps
 * precedent exactly.
 */
export const wasteStatsOf: (s: SimState) => WasteStats = memoOnState((s) => {
  const generated = wasteGeneratedOf(s);
  const capacity = collectionCapacityOf(s);
  const coverage = generated > 0 ? Math.min(1, capacity / generated) : 1;
  const collected = Math.min(generated, capacity);
  const uncollected = Math.max(0, generated - collected);
  return { generated, capacity, collected, coverage, uncollected, uncollectedFraction: 1 - coverage };
});

/** Collection coverage 0..1 (brief §2) — min(1, depotCapacity/generated). */
export function collectionCoverageOf(s: SimState): number {
  return wasteStatsOf(s).coverage;
}

/**
 * Collection OPEX this tick, £ (brief §2/§4): the rounds cost ∝ tonnage actually
 * COLLECTED. Zero waste (or no depots ⇒ nothing collected) ⇒ zero OPEX. Charged
 * through computeFlows() as the "Refuse Collection" outflow (conservation-safe).
 * Integer £, deterministic. PLACEHOLDER-balance rate.
 */
export function collectionOpexOf(s: SimState): number {
  return Math.round(wasteStatsOf(s).collected * COLLECTION_OPEX_PER_TONNE);
}

// ════════════════════════════════════════════════════════════════════════════
// FEAT-1972079906 inc2 — GARBAGE / WASTE: PROCESSING MIX + TOTAL-RECYCLING ENGINE.
//
// Of the COLLECTED tonnage (inc1's wasteStatsOf(s).collected), how much is routed
// to each processor — landfill / energy-from-waste / MRF-recycling / compost — as a
// deterministic function of the built ONLINE processor capacity. Landfill is the
// FALLBACK sink for whatever the diverting processors (EfW/MRF/compost) cannot take,
// so it always absorbs the remainder even with no landfill building placed. The
// diversion % KPI (§6 Q3 resolved: a KPI, not a scored achievement) = diverted/collected.
//
// Economic hooks are booked through computeFlows() (engine.ts), conservation-safe:
//   • landfill  → "Waste Disposal" OUTFLOW  (tipping cost ∝ landfilled tonnes)
//   • MRF       → "Recycling Revenue" INFLOW (recovered tonnes × recovery rate × rate)
//   • compost   → "Compost Revenue"  INFLOW  (composted tonnes × rate)
//   • EfW power → added to powerStats.cap (efwPowerOf); its economic value is realised
//                 through the EXISTING Grid Export revenue when there is a surplus, so
//                 it is NOT booked as a second inflow (would double-count).
// §6 Q4 resolved: recovered material is REVENUE ONLY for inc2 (real industrial-input
// feedback deferred). All quantities are DERIVED read-outs — nothing is stored in
// SimState, so processing cannot drift or break the consistency checker / replay.
//
// ⚠ BALANCE-NUMBER REGIME: every processor processCapacity (on the spec), the tipping
// cost, recovery rate, revenue rates, and EfW MW-per-tonne are PLACEHOLDERS — directional
// only, pending Aaron's row-by-row balance pass. Do not tune gameplay against them.
// ════════════════════════════════════════════════════════════════════════════

/** £ charged per tonne of refuse sent to LANDFILL (the tipping fee). PLACEHOLDER. */
export const TIPPING_COST_PER_TONNE = 8;
/** Fraction of MRF-processed tonnage recovered as sellable material. PLACEHOLDER. */
export const MRF_RECOVERY_RATE = 0.6;
/** £ earned per tonne of RECOVERED material (revenue only, inc2). PLACEHOLDER. */
export const MATERIAL_REVENUE_PER_TONNE = 45;
/** £ earned per tonne of refuse COMPOSTED. PLACEHOLDER. */
export const COMPOST_REVENUE_PER_TONNE = 15;
/** MW added to the grid per tonne of residual burned in an EfW plant. PLACEHOLDER. */
export const EFW_MW_PER_TONNE = 0.5;

/** Four per-processor online capacity sums, tonnes/tick — see processCapacitiesOf(). */
interface ProcessCapacities {
  efw: number;
  mrf: number;
  compost: number;
  landfill: number;
}

/**
 * Total ONLINE processing throughput per processor spec, tonnes/tick (Σ of each
 * spec's `processCapacity` over online buildings of that spec). Only ONLINE
 * processors process — an under-construction / disconnected plant takes nothing.
 * Order-independent, pure.
 *
 * BUG-643 — this used to be `processCapacityOf(s, specId)`, called FOUR separate
 * times from processingMixOf (once per processor spec), each re-walking the whole
 * buildings array: 4 full O(buildings) passes to answer one processingMixOf()
 * call. Folded into a SINGLE pass over s.buildings computing all four sums at
 * once (same technique as serviceCapacityAggregates' 7-sums-in-one-pass fix
 * above), then memoOnState so repeat processingMixOf() calls on the same state
 * hit the cache entirely. Not exported — processCapacityOf had no external
 * callers or tests (grepped clean), so this is a pure internal refactor with no
 * public-API change.
 */
const processCapacitiesOf: (s: SimState) => ProcessCapacities = memoOnState((s) => {
  let efw = 0;
  let mrf = 0;
  let compost = 0;
  let landfill = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (!sp?.processCapacity) continue;
    if (b.spec === 'waste_incinerator') efw += sp.processCapacity;
    else if (b.spec === 'waste_recycling') mrf += sp.processCapacity;
    else if (b.spec === 'waste_compost') compost += sp.processCapacity;
    else if (b.spec === 'waste_landfill') landfill += sp.processCapacity;
  }
  return { efw, mrf, compost, landfill };
});

/** Derived processing read-out for a state (brief §2/§3). All tonnages tonnes/tick. */
export interface ProcessingMix {
  /** Collected refuse available to process (= wasteStatsOf(s).collected). */
  collected: number;
  /** Online processor capacities, tonnes/tick. */
  efwCapacity: number;
  mrfCapacity: number;
  compostCapacity: number;
  landfillCapacity: number;
  /** Σ of the three DIVERTING capacities (everything that is not landfill). */
  divertCapacity: number;
  /** Tonnes actually routed to each processor this tick. */
  efw: number;
  mrf: number;
  compost: number;
  /** Landfill takes the remainder collected − diverted (the fallback sink). */
  landfill: number;
  /** efw + mrf + compost — everything kept out of landfill. */
  diverted: number;
  /** diverted / collected, 0 when nothing is collected. The total-recycling KPI. */
  diversionRate: number;
}

/**
 * Processing mix of the collected tonnage (brief §2/§3). Diverting processors
 * (EfW/MRF/compost) take up to their combined capacity, split PROPORTIONALLY by
 * capacity (order-independent); landfill absorbs the remainder. Pure/deterministic.
 *
 * BUG-643 — memoOnState. Callers (efwPowerOf/landfillTippingOf/recyclingRevenueOf/
 * compostRevenueOf, plus computeFlows/panels calling this directly) each pull
 * their own field several times per tick from this SAME derivation; before this
 * fix every one of those calls re-ran the full collected/capacities/split/
 * landfill-remainder computation (and, pre-processCapacitiesOf, 4 more full
 * building-array passes underneath it) from scratch.
 */
export const processingMixOf: (s: SimState) => ProcessingMix = memoOnState((s) => {
  const collected = wasteStatsOf(s).collected;
  const { efw: efwCapacity, mrf: mrfCapacity, compost: compostCapacity, landfill: landfillCapacity } =
    processCapacitiesOf(s);
  const divertCapacity = efwCapacity + mrfCapacity + compostCapacity;
  const diverted = Math.min(collected, divertCapacity);
  const share = (cap: number) => (divertCapacity > 0 ? diverted * (cap / divertCapacity) : 0);
  const efw = share(efwCapacity);
  const mrf = share(mrfCapacity);
  const compost = share(compostCapacity);
  // Landfill = remainder, computed as collected − diverted so landfill + diverted
  // is EXACTLY collected (tonnage conservation, no float drift on the total).
  const landfill = collected - diverted;
  const diversionRate = collected > 0 ? diverted / collected : 0;
  return {
    collected,
    efwCapacity,
    mrfCapacity,
    compostCapacity,
    landfillCapacity,
    divertCapacity,
    efw,
    mrf,
    compost,
    landfill,
    diverted,
    diversionRate,
  };
});

/** Diversion rate 0..1 (brief §3) = 1 − landfill share = diverted/collected. The KPI. */
export function diversionRateOf(s: SimState): number {
  return processingMixOf(s).diversionRate;
}

/**
 * EfW grid power this tick, MW (brief §3/§4): residual routed to EfW × MW-per-tonne.
 * Added to powerStats.cap so it feeds the grid surplus / Grid Export exactly like a
 * power plant, WITHOUT a static spec `mw` — so zero throughput ⇒ zero power (an idle
 * incinerator adds nothing). Pure/deterministic; cannot double-count (the EfW spec
 * carries no `mw`, so it never contributes to the power-plant sum in powerStats).
 */
export function efwPowerOf(s: SimState): number {
  return processingMixOf(s).efw * EFW_MW_PER_TONNE;
}

/**
 * Landfill tipping cost this tick, £ (brief §4): the fee ∝ tonnage sent to landfill
 * (the remainder). Zero landfilled ⇒ zero. Charged through computeFlows() as the
 * "Waste Disposal" outflow (conservation-safe). Integer £, deterministic. PLACEHOLDER.
 */
export function landfillTippingOf(s: SimState): number {
  return Math.round(processingMixOf(s).landfill * TIPPING_COST_PER_TONNE);
}

/**
 * MRF material revenue this tick, £ (brief §4): recovered tonnage (MRF throughput ×
 * recovery rate) × price per recovered tonne. Booked as the "Recycling Revenue" inflow.
 * Integer £, deterministic. PLACEHOLDER rates.
 */
export function recyclingRevenueOf(s: SimState): number {
  return Math.round(processingMixOf(s).mrf * MRF_RECOVERY_RATE * MATERIAL_REVENUE_PER_TONNE);
}

/**
 * Compost revenue this tick, £ (brief §4): composted tonnage × price per tonne. Booked
 * as the "Compost Revenue" inflow. Integer £, deterministic. PLACEHOLDER rate.
 */
export function compostRevenueOf(s: SimState): number {
  return Math.round(processingMixOf(s).compost * COMPOST_REVENUE_PER_TONNE);
}

// ---------- placement planner ----------

// Per-state memo of the full-footprint occupied-tile Set, keyed on the
// buildings array reference (immutable per tick) — same idiom as
// roadTileSetOf (data.ts:494 above). BUG b2d31bc7 quick-win FIX 1: this was
// rebuilt from scratch on every occupiedSet() call, and the single-placement
// path calls it once per pointer-move tile-change during drag, making an
// O(buildings) rebuild the dominant cost of the ~15ms reducer(place). Only
// the no-`ignoreId` case (the overwhelmingly common one — plain placement)
// is memoisable this way; the `ignoreId` case (used while dragging an
// EXISTING building to a new spot) still computes fresh below since the
// excluded id varies call to call and isn't part of the cache key.
const occupiedSetCache = new WeakMap<object, Set<string>>();

export function occupiedSet(s: SimState, ignoreId?: number): Set<string> {
  if (ignoreId === undefined) {
    const cached = occupiedSetCache.get(s.buildings);
    if (cached) return cached;
    const set = buildOccupiedSet(s.buildings, undefined);
    occupiedSetCache.set(s.buildings, set);
    return set;
  }
  return buildOccupiedSet(s.buildings, ignoreId);
}

/**
 * FEAT-2326609740 — the SINGLE SOURCE OF TRUTH (GR#3) for a building's REAL
 * current footprint. A building that has scaled OUT occupies MORE tiles than
 * its spec's base w/h — every consumer that reasons about a building's
 * physical extent (occupancy, road adjacency, rendering, hit-testing, the GPU
 * instance builder, relocate validation) MUST read this, never `sp.w`/`sp.h`
 * directly, or it silently disagrees with debug.json and with occupiedSet()
 * (F5, independent round REJECT, 2026-09-03 — "built but not wired": only
 * buildOccupiedSet had been taught the grown footprint; every other consumer
 * still walked the stale spec-only rect).
 */
export function footprintOf(b: { footprintW?: number; footprintH?: number }, sp: Spec): { w: number; h: number } {
  return { w: b.footprintW ?? sp.w, h: b.footprintH ?? sp.h };
}

function buildOccupiedSet(buildings: SimState['buildings'], ignoreId: number | undefined): Set<string> {
  const set = new Set<string>();
  for (const b of buildings) {
    if (b.id === ignoreId) continue;
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const { w, h } = footprintOf(b, sp);
    for (let dx = 0; dx < w; dx++)
      for (let dy = 0; dy < h; dy++) set.add(`${b.x + dx},${b.y + dy}`);
  }
  return set;
}

/**
 * FEAT-2326609839 (Aaron, 2026-09-04, "skip empty land" glide ruling,
 * verbatim: "marchin ants and red squares only need to look at squares with
 * contnet so skip where nothing is built"): a column-occupancy index over
 * the map's MAP_W tile columns — column `x` is occupied iff at least one
 * building's REAL footprint (`footprintOf`, GR#3 — never a grown building's
 * stale spec-only w/h) intersects it. Powers TWO consumers, both of which
 * already import this module: consolidatorGlide.ts's skip-empty-columns
 * cursor (engine.ts's sectionKeysForGlideWindow passes this in so the
 * day->column walk never spends a day gliding over land with nothing built)
 * and MapView.tsx's monthly-twelfth static scope grid (reuses
 * consolidator.ts's sectionIndexOf instead — see that call site — but this
 * is the same idiom, kept here as the shared SSOT column-level index, GR#3).
 *
 * Memoised (occupiedSet's own idiom immediately above): a WeakMap keyed on
 * `s.buildings`' ARRAY identity, one O(buildings) fold per buildings-array
 * change — NEVER per glide-day call, which is the entire point (a glide day
 * runs once per game tick; recomputing this from scratch every tick would
 * reintroduce exactly the O(buildings)-per-day cost the whole glide-mode
 * perf design (consolidator.ts's sectionIndexOf doc comment) exists to
 * avoid).
 *
 * Order-independent by construction (GR#21): the per-building loop only
 * ever SETS bits in a fixed-size boolean array (no accumulation order
 * dependency — painting column 5 twice, in any order, leaves it painted
 * once), and the returned array is built by a single ascending scan over
 * column index 0..MAP_W-1 — so a shuffled `buildings` array (same buildings,
 * different array order/reference) always yields an array that is
 * value-identical to the unshuffled one, even though it is cached under a
 * DIFFERENT WeakMap key (a fresh array reference is, correctly, a fresh
 * cache miss — see the memo-identity test in
 * consolidator-glide-skip-empty.test.mjs for why that's the right contract,
 * not a bug).
 */
const occupiedColumnsCache = new WeakMap<object, number[]>();

export function occupiedColumnsOf(s: SimState): number[] {
  const cached = occupiedColumnsCache.get(s.buildings);
  if (cached) return cached;
  const occupied = new Array<boolean>(MAP_W).fill(false);
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue; // GR#16: an unknown spec id (corrupt/old save) contributes nothing rather than throwing.
    const { w } = footprintOf(b, sp);
    const xStart = Math.max(0, b.x);
    const xEnd = Math.min(MAP_W, b.x + w);
    for (let x = xStart; x < xEnd; x++) occupied[x] = true;
  }
  const columns: number[] = [];
  for (let x = 0; x < MAP_W; x++) if (occupied[x]) columns.push(x);
  occupiedColumnsCache.set(s.buildings, columns);
  return columns;
}

/**
 * BUG-646 (Aaron, 2026-09-03, cap 250 -> 2000) — THE root-cause fix. Profiled
 * against Aaron's real 29,831-building savepoint: the 'place' reducer case
 * (engine.ts) calls `occupiedSet(placedState)` immediately after building
 * `placedState.buildings = [...state.buildings, placedBuilding]` (a single
 * append), to hand autoConnect() a Set that includes the just-placed
 * building. occupiedSet()'s cache is keyed on array IDENTITY (data.ts:3349),
 * so that call was ALWAYS a miss — a full O(buildings) rebuild — EVERY
 * single placement, measured at 5,179ms for 250 units (~20.7ms/unit, the
 * single largest cost in the whole resolveDemandAll hot path, ahead of even
 * the O(buildings) cost findSpot() used to pay before this same bug's
 * createSpotSearchContext() fix above). `state`'s own occupiedSet() IS
 * already cached by this point (the 'place' case's earlier `fits(
 * occupiedSet(state), ...)` guard populated it, and it stays a cache HIT
 * across a resolveDemand / placePlanItem batch loop because each iteration's
 * `state` is the PREVIOUS iteration's `placedState` — same array reference),
 * so cloning that cached Set and adding only the new building's own
 * footprint (O(w*h), a handful of cells) is exact and orders of magnitude
 * cheaper than a full rebuild. `oldState.buildings` must be a strict prefix
 * of `newBuildings` (true for every 'place'-case caller — append-only) —
 * this is NOT a general replacement for occupiedSet(), only a fast path for
 * that exact, guaranteed shape.
 */
export function occupiedSetIncremental(oldState: SimState, newBuildings: SimState['buildings']): Set<string> {
  const cached = occupiedSetCache.get(newBuildings);
  if (cached) return cached;
  const base = occupiedSet(oldState);
  const set = new Set(base);
  for (let i = oldState.buildings.length; i < newBuildings.length; i++) {
    const b = newBuildings[i];
    const sp = SPECS[b.spec];
    if (!sp) continue;
    for (let dx = 0; dx < sp.w; dx++)
      for (let dy = 0; dy < sp.h; dy++) set.add(`${b.x + dx},${b.y + dy}`);
  }
  occupiedSetCache.set(newBuildings, set);
  return set;
}

export function fits(set: Set<string>, w: number, h: number, x: number, y: number): boolean {
  for (let i = 0; i < w; i++) for (let j = 0; j < h; j++) if (set.has(`${x + i},${y + j}`)) return false;
  return true;
}

const cheb = (ax: number, ay: number, bx: number, by: number) =>
  Math.max(Math.abs(ax - bx), Math.abs(ay - by));

// BUG-593: an earlier draft of this fix replaced housingCentroid() with a
// bounding-box centre of ALL buildings ("builtUpCentroid"), reasoning that a
// residential-only centroid was the thing collapsing to a corner. An
// independent destructive round REJECTed that draft with a decisive
// isolation: initialState() ships ~1,855 map-spanning infrastructure
// buildings (motorway/rail/highspeed-rail tiles) whose bounding box already
// spans most of the map on EVERY game, so the all-buildings centroid sits at
// ~the map centre regardless of where the city actually is — on a FRESH game
// at pop 10,000 it placed 16 pow_wind turbines at (217..223,128..134),
// 0/16 road-adjacent, "no road access", instead of HEAD's behaviour of
// placing all 16 next to the actual (small, real) road network. Isolating the
// two changes proved the OLD housingCentroid + ONLY the widen-before-giving-up
// loop below is sufficient for Aaron's original repro (his residential
// centroid was fine — the fixed-size WINDOW was the bug) and restores the
// fresh-game placement behaviour. So this stays housingCentroid(): the
// average position of `kind==='residential'` buildings, falling back to a
// hardcoded (150,78) with zero residential buildings — unchanged from before
// BUG-593, kept because a better city-tracking centre (e.g. excluding
// infrastructure kinds) is a real possible refinement but is NOT required to
// fix the reported bug and was explicitly deferred by the round's ruling.
function housingCentroid(s: SimState): { x: number; y: number } {
  let hx = 0;
  let hy = 0;
  let n = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind !== 'residential') continue;
    hx += b.x;
    hy += b.y;
    n++;
  }
  if (n === 0) return { x: 150, y: 78 };
  return { x: hx / n, y: hy / n };
}

/** The few per-tile scoring inputs findSpotCore() needs — pulled out of
 *  findSpot() (BUG-646) so createSpotSearchContext() below can build them
 *  ONCE per batch and update them incrementally, instead of every caller
 *  paying a fresh O(buildings) scan on every single unit. */
interface SpotScoringContext {
  occ: Set<string>;
  hc: { x: number; y: number };
  tagged: Record<Tag, { cx: number; cy: number }[]>;
  resList: { cx: number; cy: number }[];
}

function buildScoringContext(s: SimState): SpotScoringContext {
  const occ = occupiedSet(s);
  const hc = housingCentroid(s);

  // Pre-extract only the few buildings that matter for scoring.
  // FOLLOW-UP (auto-scale ladder r3 round note (b), non-blocking): these
  // centroids use bs.w/bs.h, not footprintOf(b, bs) — a grown building's
  // scoring centroid is slightly off from its true footprint centre. Harmless
  // in practice (siting-preference heuristic only, never a correctness or
  // overlap gate) and deliberately not fixed here.
  const tagged: Record<Tag, { cx: number; cy: number }[]> = { pollution: [], clean: [], waste: [] };
  const resList: { cx: number; cy: number }[] = [];
  for (const b of s.buildings) {
    const bs = SPECS[b.spec];
    if (!bs) continue;
    if (bs.tag) tagged[bs.tag].push({ cx: b.x + bs.w / 2, cy: b.y + bs.h / 2 });
    if (bs.kind === 'residential') resList.push({ cx: b.x + bs.w / 2, cy: b.y + bs.h / 2 });
  }
  return { occ, hc, tagged, resList };
}

/** Extracted from findSpot() (BUG-646, unchanged scoring rules from before
 *  BUG-593) so both the single-call findSpot() and the batch
 *  createSpotSearchContext() below share the EXACT SAME search/scoring logic
 *  (GR#3 SSOT) — the only difference is where `ctx` comes from (fresh every
 *  call for findSpot(), built once and updated incrementally for a batch). */
function distToList(list: { cx: number; cy: number }[], x: number, y: number): number {
  let min = Infinity;
  for (const p of list) {
    const d = cheb(x, y, p.cx, p.cy);
    if (d < min) min = d;
  }
  return min;
}

/**
 * BUG-593's per-tile scoring rules, extracted to its own function (BUG-646)
 * so BOTH the single-best scanWindow() below AND the ALL-CANDIDATES
 * scanWindowAll() (used by createSpotSearchContextFast()'s candidate cache)
 * score a tile identically (GR#3 SSOT) — only how the RESULT is collected
 * (one best vs every fit, sorted) differs between the two callers.
 */
/** The four SSOT distance inputs scoreTile()'s rules read — split out (BUG-660)
 *  so a batch cache can store them PER CANDIDATE and tighten them
 *  incrementally against only newly-added points, instead of recomputing
 *  distToList() over the WHOLE tagged/resList every time any one point is
 *  added. See createSpotSearchContext()'s occupy() for the incremental use. */
interface TileDistances {
  poll: number;
  waste: number;
  clean: number;
  resNear: number;
}

function tileDistances(tagged: Record<Tag, { cx: number; cy: number }[]>, resList: { cx: number; cy: number }[], cx: number, cy: number): TileDistances {
  return {
    poll: distToList(tagged.pollution, cx, cy),
    waste: distToList(tagged.waste, cx, cy),
    clean: distToList(tagged.clean, cx, cy),
    resNear: distToList(resList, cx, cy),
  };
}

/** BUG-593's per-tile scoring rules, driven by ALREADY-COMPUTED distances
 *  (tileDistances()) — the pure "distances -> score" half of scoreTile(),
 *  split out so an incremental caller can supply freshly-tightened distances
 *  without recomputing them via a full tagged/resList scan (GR#3 SSOT: this
 *  is the ONLY place the scoring rules themselves are written). */
function scoreFromDistances(sp: Spec, hc: { x: number; y: number }, x: number, y: number, d: TileDistances): number {
  let score = -cheb(x, y, hc.x, hc.y) / 4;
  const { poll, waste, clean, resNear } = d;

  if ((sp.stage === 'nursery' || sp.stage === 'primary') && poll < 8) score -= 1000;
  if ((sp.stage === 'city' || sp.stage === 'tertiary') && poll < 6) score -= 800;
  if (sp.stage && waste < 6) score -= 800;
  if (sp.stage && resNear > 14) score -= (resNear - 14) * 10;

  if ((sp.id === 'hea_clinic' || sp.id === 'pol_station') && poll < 5) score -= 600;
  if (sp.id === 'hea_hospital' && poll < 7) score -= 800;

  if (sp.id === 'park') {
    if (resNear > 6) score -= (resNear - 6) * 8;
    else score += 20;
  }

  if (sp.id === 'ind_factory' && resNear < 6) score -= 600;
  if (sp.id === 'ind_farm' && resNear < 4) score -= 200;
  if (sp.tag === 'pollution' && sp.kind === 'power') {
    if (sp.mw && sp.mw >= 600 && resNear < 15) score -= 5000;
    else if (sp.mw && sp.mw >= 80 && resNear < 10) score -= 2000;
    else if (resNear < 3) score -= 400;
  }

  if (sp.tag === 'waste') {
    if (clean < 8) score -= 2000;
    if (resNear < 5) score -= 800;
  }
  if (sp.tag === 'clean' && waste < 8) score -= 2000;

  return score;
}

function scoreTile(sp: Spec, hc: { x: number; y: number }, tagged: Record<Tag, { cx: number; cy: number }[]>, resList: { cx: number; cy: number }[], x: number, y: number): number {
  const cx = x + sp.w / 2;
  const cy = y + sp.h / 2;
  return scoreFromDistances(sp, hc, x, y, tileDistances(tagged, resList, cx, cy));
}

function findSpotCore(specId: string, ctx: SpotScoringContext, winHint?: { win: number }): { x: number; y: number } | null {
  const sp = SPECS[specId];
  if (!sp) return null;
  const { occ, hc, tagged, resList } = ctx;

  // BUG-593 FIX: score every fitting tile in [xa,xb]x[ya,yb] at the given
  // stride, returning the best (or null). Factored out so the widen loop
  // below can call it once per pass without duplicating the scoring rules —
  // the rules themselves are untouched from before BUG-593.
  const scanWindow = (
    xa: number,
    ya: number,
    xb: number,
    yb: number,
    stride: number
  ): { x: number; y: number; score: number } | null => {
    let best: { x: number; y: number; score: number } | null = null;
    for (let y = ya; y <= yb; y += stride) {
      for (let x = xa; x <= xb; x += stride) {
        if (!fits(occ, sp.w, sp.h, x, y)) continue;
        const score = scoreTile(sp, hc, tagged, resList, x, y);
        if (!best || score > best.score) best = { x, y, score };
      }
    }
    return best;
  };

  // BUG-593 FIX: the original code searched ONLY the initial WIN x WIN window
  // and returned null the instant it came up empty, with no notion of "try
  // harder before giving up" — a small window that happens to sit over
  // already-built-out tiles reads as "the whole map is full" even when free
  // land is abundant elsewhere. Now: on an empty pass, DOUBLE the window and
  // scan again, up to the point where the window already covers the entire
  // map. That final, whole-map pass switches to STRIDE 1 (independent-round
  // finding, moderate #4): stride 2 samples only 1 of every 4 tiles, so an
  // empty stride-2 pass over the whole map could still be lying — a genuinely
  // free 1x1 tile at an odd offset would be skipped and "no free area" would
  // be a false claim. Every earlier (non-final) pass keeps stride 2 — it is
  // only a widen-and-retry step, not the last word, so its cost stays cheap;
  // paying stride-1 exactly once, only when the search is about to give up
  // entirely, keeps the message that follows honest without making every
  // successful (usually first-pass) call more expensive. Scan order inside
  // each pass is fixed row-major with a constant stride (GR#21 determinism:
  // no map/set iteration order dependency, pure function of `s` and
  // `specId`), so the same state always yields the same spot, and doubling is
  // a bounded, deterministic number of passes (map is finite).
  // BUG-646 (Aaron, 2026-09-03, cap 250 -> 2000): `winHint`, when supplied,
  // lets a BATCH caller (createSpotSearchContext below) start this widen loop
  // at the window size the PREVIOUS call in the same batch ended up needing,
  // instead of always restarting the doubling from 90 — profiled as a real,
  // additional bottleneck once a big single-spec batch (e.g. 1,340 nurseries
  // in one resolveDemandAll click) exhausts the near-centroid land: every
  // unit was re-paying the SAME sequence of guaranteed-failing small-window
  // passes (90, 180, 360, ...) before reaching the window size that actually
  // has room. This is PROVABLY LOSSLESS, not an approximation: `occ` only
  // ever GROWS within a batch (placements are never undone), so a window
  // size that failed to fit anything on a PREVIOUS call is monotonically
  // guaranteed to still fail on every subsequent call — skipping it changes
  // nothing about which tile is ultimately chosen (scanWindow still picks
  // the single best-scoring tile across the whole window it lands on,
  // identical to what the un-hinted loop would find after walking there the
  // slow way), it only skips widen steps whose outcome is already known.
  // Omitted (undefined) for the single-call findSpot() path — that pays this
  // cost at most once per interactive click, no persistence needed or
  // wanted there.
  let win = winHint ? winHint.win : 90;
  for (;;) {
    const xa = Math.max(2, Math.floor(hc.x - win / 2));
    const ya = Math.max(2, Math.floor(hc.y - win / 2));
    const xb = Math.min(MAP_W - sp.w - 2, xa + win);
    const yb = Math.min(MAP_H - sp.h - 2, ya + win);
    const coversWholeMap = xa <= 2 && ya <= 2 && xb >= MAP_W - sp.w - 2 && yb >= MAP_H - sp.h - 2;

    const best = scanWindow(xa, ya, xb, yb, coversWholeMap ? 1 : 2);
    if (best) {
      if (winHint) winHint.win = win;
      return { x: best.x, y: best.y };
    }
    if (coversWholeMap) {
      if (winHint) winHint.win = win; // already at whole-map — nothing bigger to remember
      return null; // genuinely nothing fits anywhere on the map — proven at stride 1
    }
    win *= 2;
  }
}

/**
 * BUG-646 (Aaron, 2026-09-03, cap 250 -> 2000) — the SECOND real bottleneck
 * found this session, after the O(buildings)-per-unit one findSpotCore()'s
 * doc comment above describes. Even with THAT fix and the winHint widen-loop
 * memo (above), a big single-spec batch in a mature/dense city still paid a
 * full O(window-area) scanWindow() pass on EVERY unit — measured against
 * Aaron's real savepoint: once local land near the housing centroid is
 * mostly built out, several real specs (edu_nursery, pow_wind, wat_clean/
 * wat_waste) settle at window sizes of 180-360 tiles (vs the initial 90),
 * and at that size a SINGLE window pass (~8,000-26,000 candidate tiles x 4
 * distTo() scans each) costs 13-70ms — paid AGAIN for every one of 1,000+
 * units of the same spec in one resolveDemandAll batch, because only ONE
 * tile out of that whole scanned window actually gets consumed per call.
 *
 * THE FIX: scan the window ONCE, scoring and sorting EVERY fitting tile
 * (not just tracking the single best), and serve successive findNext() calls
 * from that precomputed, already-sorted list — collapsing the per-unit cost
 * from O(window-area) to an amortised O(1) pop (plus one O(footprint) fits()
 * re-check per pop, since a later same-batch placement can overlap an
 * earlier candidate's footprint for any spec wider/taller than the stride).
 *
 * CORRECTNESS: a pending candidate's SCORE can shift while cached whenever
 * the spec being placed carries a `tag` or is `kind: 'residential'`
 * (scoreTile()'s only score inputs besides `occ` are `tagged`/`resList`/
 * `hc`, and `occupy()` only appends to those for a tagged/residential spec —
 * see createSpotSearchContext()'s own "never a residential spec" assumption,
 * same idea). BUG-660 FOLLOW-UP (2026-09-04): a tagged spec (wat_clean/
 * wat_waste/a pollution-tagged power plant) used to force a full fresh
 * scanAllCore() rescan+refit of the WHOLE window on every single one of its
 * own placements — CPU-profiled against Aaron's real 49,174-building save as
 * the dominant cost in a resolveDemandAll batch (>80% of samples in `fits`/
 * `distToList`/`scanAllCore`/`scanWindowAll` combined), because `occ` only
 * ever GROWS within a batch, so the true candidate universe for a given
 * window can never gain a tile the original scan missed — createSpotSearchContext()'s
 * occupy() now re-fits + re-scores ONLY the already-cached candidates in
 * place (dropping any that no longer fit) instead of discarding the list and
 * re-walking the whole window — see its own doc comment for the full
 * argument. Still fully correct (identical scores/fits to a from-scratch
 * rescan of the SAME window), just no longer paying for geometry already
 * known.
 */
/** A candidate tile in createSpotSearchContext()'s sorted cache. Carries its
 *  own TileDistances (BUG-660) alongside the derived score, so occupy() can
 *  TIGHTEN just these four numbers against newly-added points instead of
 *  recomputing them from the whole tagged/resList every time. */
interface SpotCandidate extends TileDistances {
  x: number;
  y: number;
  score: number;
}

function scanWindowAll(
  sp: Spec,
  ctx: SpotScoringContext,
  xa: number,
  ya: number,
  xb: number,
  yb: number,
  stride: number
): SpotCandidate[] {
  const { occ, hc, tagged, resList } = ctx;
  const out: SpotCandidate[] = [];
  for (let y = ya; y <= yb; y += stride) {
    for (let x = xa; x <= xb; x += stride) {
      if (!fits(occ, sp.w, sp.h, x, y)) continue;
      const d = tileDistances(tagged, resList, x + sp.w / 2, y + sp.h / 2);
      out.push({ x, y, ...d, score: scoreFromDistances(sp, hc, x, y, d) });
    }
  }
  // Highest score first (pop from the end — O(1) — instead of shift/O(n)).
  out.sort((a, b) => a.score - b.score);
  return out;
}

/** Same widen-and-retry contract as findSpotCore(), but returns the FULL
 *  sorted (ascending — caller pops from the end) candidate list for the
 *  window it settles on, for createSpotSearchContext()'s candidate cache. */
function scanAllCore(specId: string, ctx: SpotScoringContext, winHint: { win: number }): SpotCandidate[] | null {
  const sp = SPECS[specId];
  if (!sp) return null;
  const { hc } = ctx;
  let win = winHint.win;
  for (;;) {
    const xa = Math.max(2, Math.floor(hc.x - win / 2));
    const ya = Math.max(2, Math.floor(hc.y - win / 2));
    const xb = Math.min(MAP_W - sp.w - 2, xa + win);
    const yb = Math.min(MAP_H - sp.h - 2, ya + win);
    const coversWholeMap = xa <= 2 && ya <= 2 && xb >= MAP_W - sp.w - 2 && yb >= MAP_H - sp.h - 2;

    const all = scanWindowAll(sp, ctx, xa, ya, xb, yb, coversWholeMap ? 1 : 2);
    if (all.length > 0) {
      winHint.win = win;
      return all;
    }
    if (coversWholeMap) {
      winHint.win = win;
      return null; // genuinely nothing fits anywhere on the map — proven at stride 1
    }
    win *= 2;
  }
}

export function findSpot(s: SimState, specId: string): { x: number; y: number } | null {
  // FEAT-2326609761 AC-29: an at-cap unique building (e.g. a second Five
  // Gorges Dam) has nowhere to go — never search for a site, so auto-build
  // (placePlanItem) can never place one via findSpot. Guard sits on BOTH
  // search entries (this single-call form and the batch context form below):
  // BUG-646's refactor split the entries after this guard was written, and a
  // guard on only one would let the batch path place a second dam.
  const sp = SPECS[specId];
  if (!sp) return null;
  if (remainingAllowance(s, sp) <= 0) return null;
  return findSpotCore(specId, buildScoringContext(s));
}

/**
 * BUG-646 (Aaron, 2026-09-03: "the autofix looks to have a 250 limit, not
 * enough, make it 2000") — a batch-search context for resolveDemand/
 * resolveDemandAll's placePlanItem() loop.
 *
 * MEASURED ROOT CAUSE (this session, profiled against Aaron's real
 * 29,831-building savepoint with population inflated to force a genuine
 * >8,000-unit shortfall): findSpot(state, specId) was NOT the bottleneck —
 * scanWindow() only touches a bounded ~90x90-tile (stride 2) window. The
 * REAL cost was occupiedSet(state) + the tagged/resList precompute loop
 * above, BOTH O(buildings), paid AFRESH on every single unit because
 * placePlanItem()'s loop calls findSpot(s2, ...) again after EVERY placement
 * and `s2.buildings` is a new array reference each time (occupiedSet's
 * WeakMap cache — data.ts:3349 — is keyed on array identity, so a changed
 * reference is always a cache miss). Measured 68ms/unit at 29,831 buildings,
 * i.e. 17s to place the OLD 250-unit cap and a projected 136s at 2000 units
 * — the exact multi-second freeze class BUG-642 was fixed for elsewhere in
 * this same city, just re-introduced here by a different O(buildings)-per-
 * placement path.
 *
 * THE FIX: build the occ Set + tagged/resList arrays ONCE from the batch's
 * starting state, then update them INCREMENTALLY as each unit (and any
 * auto-connector tiles 'place' appends alongside it) is actually placed —
 * collapsing the per-unit cost from O(buildings) to O(window), the same
 * bound scanWindow() always had. occupy() applies the EXACT SAME tag/
 * residential-kind test the original per-call precompute loop used, so a
 * batch that runs this context to completion computes an identical result
 * to what N fresh findSpot(state, specId) calls would have produced — just
 * without re-scanning `state.buildings` every time.
 *
 * SAFE ONLY because every real caller (DEMAND_FIX_PROVIDERS in this file)
 * places nursery/primary/college/gp/hosp/police/water/power/refuse/fire/
 * parks specs — never a 'residential' kind — so housingCentroid() (which
 * only depends on residential buildings) cannot change mid-batch; it is
 * computed once in buildScoringContext() and never revisited. If a future
 * caller ever batch-places a residential spec through this context, hc
 * would need recomputing per placement too — not needed today and flagged
 * here so nobody re-derives this assumption blind.
 */
export interface SpotSearchContext {
  /** Same contract as findSpot(state, specId) against the CURRENT
   *  incremental occ/tagged/resList — never mutates anything itself. */
  findNext(): { x: number; y: number } | null;
  /** Feed the buildings ACTUALLY added by the last 'place' dispatch
   *  (`next.buildings.slice(beforeLen)` — the target unit AND any
   *  auto-connector tiles) into this context so the next findNext() call
   *  sees them, exactly as a fresh findSpot(state, ...) would. */
  occupy(newBuildings: SimState['buildings']): void;
}

export function createSpotSearchContext(s: SimState, specId: string): SpotSearchContext {
  const base = buildScoringContext(s);
  // Never mutate the shared occupiedSet() cache entry — copy before this
  // context starts marking newly-placed tiles as occupied.
  const ctx: SpotScoringContext = { occ: new Set(base.occ), hc: base.hc, tagged: base.tagged, resList: base.resList };
  const winHint = { win: 90 }; // BUG-646: persists the widen loop's window size across findNext() calls — see findSpotCore()'s doc comment for why this is lossless.
  const sp = SPECS[specId];
  // BUG-646: the sorted-candidate cache (scanAllCore() above) — null means
  // "not built yet or invalidated, recompute on next findNext()". Ascending
  // score order so the best candidate is a cheap pop() off the end.
  let cache: SpotCandidate[] | null = null;
  return {
    findNext: () => {
      if (!sp) return null;
      if (cache === null) {
        cache = scanAllCore(specId, ctx, winHint);
        if (cache === null) return null; // whole map proven to have no fit
      }
      // Pop candidates until one is still actually free — an earlier
      // same-batch placement of a spec wider/taller than the scan stride can
      // overlap a later candidate's footprint even though scores can't have
      // changed (see this cache's correctness note above).
      while (cache.length > 0) {
        const cand = cache.pop()!;
        if (fits(ctx.occ, sp.w, sp.h, cand.x, cand.y)) return { x: cand.x, y: cand.y };
      }
      cache = null; // this window is exhausted — the NEXT findNext() call rescans (and may need to widen)
      return findSpotCore(specId, ctx, winHint); // one direct fallback call so a single-candidate window still resolves without an extra empty round-trip
    },
    occupy: (newBuildings) => {
      // BUG-660 (Aaron, P1, "Fix-All 2000-unit batch still blocks ~66s
      // median" — direct CPU profiling against Aaron's real 49,174-building
      // save identified THIS re-score path, not autoConnect, as the dominant
      // cost: `fits`/`distToList`/`scanAllCore` together were >80% of total
      // samples in a resolveDemandAll batch that includes wat_clean/
      // wat_waste/pollution-tagged power. Every one of those specs carries a
      // `tag`, so the OLD `cache = null` here forced scanAllCore() to re-walk
      // and re-score the ENTIRE window from scratch (an O(window-area) pass,
      // this function's own doc comment already measured at 13-70ms/unit at
      // window sizes 180-360) on literally every unit of a tagged spec.
      //
      // FIRST FIX (kept, still needed): re-fit + re-score only the cache's
      // OWN candidates instead of re-walking the window — see the "THE FIX"
      // paragraph on scanWindowAll()'s doc comment. The independent round's
      // repeated-run harness (not the author's single dev-box anecdote) put
      // that fix alone at PRE 387,329ms (n=1) -> POST a median 19,854ms (n=5)
      // for a single resolveDemandAll at Aaron's real ~49k-building save, and
      // 72,753ms -> a median 7,718ms (n=5) on the smaller ~6k-building
      // fixture, but re-profiling showed `distToList` STILL
      // dominant: each re-score was recomputing every candidate's distance
      // to the ENTIRE (thousands-strong, in a mature city) tagged list from
      // scratch, every single occupy() call.
      //
      // SECOND FIX (this pass): a NEW tagged/residential point can only ever
      // DECREASE distToList()'s min-distance result for any given candidate
      // — it is a min() over a strictly growing list. So instead of
      // recomputing poll/waste/clean/resNear from the WHOLE list, tighten
      // each candidate's ALREADY-CACHED distances (SpotCandidate now carries
      // them — scanWindowAll()/tileDistances()) against only the points
      // ADDED IN THIS CALL (newPoll/newWaste/newClean/newRes below, almost
      // always exactly one tagged point — a demand-fix batch never places a
      // residential spec, so resList realistically never grows here). This
      // is O(cache.length x pointsAddedThisCall) instead of O(cache.length x
      // totalTaggedListSize) — identical RESULT (min() is associative: the
      // min of the old min and the new points equals the min over the whole
      // updated list) at a fraction of the cost when the tagged list is
      // large relative to one batch's own contribution to it.
      const newPoll: { cx: number; cy: number }[] = [];
      const newWaste: { cx: number; cy: number }[] = [];
      const newClean: { cx: number; cy: number }[] = [];
      const newRes: { cx: number; cy: number }[] = [];
      for (const b of newBuildings) {
        const bs = SPECS[b.spec];
        if (!bs) continue;
        for (let dx = 0; dx < bs.w; dx++)
          for (let dy = 0; dy < bs.h; dy++) ctx.occ.add(`${b.x + dx},${b.y + dy}`);
        if (bs.tag) {
          const pt = { cx: b.x + bs.w / 2, cy: b.y + bs.h / 2 };
          ctx.tagged[bs.tag].push(pt);
          if (bs.tag === 'pollution') newPoll.push(pt);
          else if (bs.tag === 'waste') newWaste.push(pt);
          else if (bs.tag === 'clean') newClean.push(pt);
        }
        if (bs.kind === 'residential') {
          const pt = { cx: b.x + bs.w / 2, cy: b.y + bs.h / 2 };
          ctx.resList.push(pt);
          newRes.push(pt);
        }
      }
      const anyNewPoints = newPoll.length > 0 || newWaste.length > 0 || newClean.length > 0 || newRes.length > 0;
      // `occ` only ever GROWS within a batch (placements are never undone —
      // the same invariant findSpotCore()'s winHint memo already relies on),
      // so no tile the ORIGINAL scanAllCore() window pass missed can
      // suddenly start fitting now — the true candidate universe for THIS
      // window is exactly the cache's own tiles, filtered down. A candidate
      // that no longer fits (this unit's own footprint, or any connector
      // tile alongside it, now overlaps it) is dropped here, exactly as
      // findNext()'s own pop-loop fits() re-check would have dropped it
      // lazily one pop at a time — that re-check stays in place as a final
      // safety net, now usually a no-op hit.
      if (anyNewPoints && cache !== null && sp) {
        const tighten = (min: number, pts: { cx: number; cy: number }[], cx: number, cy: number) => {
          let m = min;
          for (const p of pts) {
            const dd = cheb(cx, cy, p.cx, p.cy);
            if (dd < m) m = dd;
          }
          return m;
        };
        const rescored: SpotCandidate[] = [];
        for (const cand of cache) {
          if (!fits(ctx.occ, sp.w, sp.h, cand.x, cand.y)) continue;
          const cx = cand.x + sp.w / 2;
          const cy = cand.y + sp.h / 2;
          const d: TileDistances = {
            poll: newPoll.length ? tighten(cand.poll, newPoll, cx, cy) : cand.poll,
            waste: newWaste.length ? tighten(cand.waste, newWaste, cx, cy) : cand.waste,
            clean: newClean.length ? tighten(cand.clean, newClean, cx, cy) : cand.clean,
            resNear: newRes.length ? tighten(cand.resNear, newRes, cx, cy) : cand.resNear,
          };
          rescored.push({ x: cand.x, y: cand.y, ...d, score: scoreFromDistances(sp, ctx.hc, cand.x, cand.y, d) });
        }
        rescored.sort((a, b) => a.score - b.score); // ascending — findNext() pops from the end
        cache = rescored;
      }
    },
  };
}

/**
 * BUG-593 FIX: a human-readable reason findSpot() found nothing, for the
 * placeNotice text — "no buildable site found" alone reads as "the map is
 * full", which was misleading even before the fix (a tiny search window could
 * fail with 94% of the map free) and stays worth spelling out afterwards
 * (findSpot() now genuinely means "searched the whole map") so a real
 * map-full state is distinguishable at a glance from a config/footprint
 * problem. Pure formatting, no state.
 */
export function noBuildableSiteReason(specId: string): string {
  const sp = SPECS[specId];
  if (!sp) return 'no buildable site found';
  return `no free ${sp.w}x${sp.h} area on the map for ${sp.name}`;
}

export function pickAutoSpec(
  s: SimState
  // BUG-601: `serviceKey` added (the raw serviceDemandOf() id, e.g. 'fire') so
  // a caller can look up this SAME service's demandFixPlan() entry and place
  // the whole 50%-of-shortfall batch through resolveDemand, instead of the
  // single unit this function's own spec/label pair used to imply — see
  // DemandDock.tsx's runAuto(). Never a count/plan field itself (AC-6): this
  // function stays a pure "what/where to recommend" pick.
): { spec: string; label: string; serviceKey: string } | null {
  // Positive value = shortfall (BUG-392 semantics), so the descending sort
  // surfaces the WORST-covered service. (Under the pre-BUG-392 surplus-positive
  // index this same code auto-built the most OVERsupplied service.)
  // ⚠ BALANCE-NUMBER PLACEHOLDER: the >25 trigger threshold is directional
  // only, pending Aaron's balance pass.
  const meters = serviceDemandOf(s).sort((a, b) => b.value - a.value);
  // BUG-396 FIX: never offer a build the player cannot afford. Walk the shortfall
  // meters worst-first (already sorted desc) and return the FIRST under-provided
  // service (value > 25) whose spec exists and whose placement cost is coverable by
  // current funds. A free zone (placementCost 0) is always affordable. If the worst
  // service is unaffordable, this falls through to a cheaper-but-still-short one, and
  // only returns null when nothing short is affordable — so the advisor stops nagging
  // the player to build things they have no money for. Deterministic (sorted, pure).
  for (const m of meters) {
    if (m.value <= 25) break; // sorted desc — everything after is below the trigger.
    const sp = SPECS[m.spec];
    if (!sp) continue;
    const cost = placementCost(sp);
    // FEAT-1972079923 inc3 (AC-6): the advisor must not offer paid buildings
    // while Administration Mode is active — a paid suggestion would just
    // bounce off the `place()` discretionary-spend block, silently confusing
    // the player. A £0 (free zone/road) suggestion is still fine under admin.
    if (s.administrationState && cost > 0) continue;
    if (cost <= s.funds) {
      return { spec: m.spec, label: m.label, serviceKey: m.id };
    }
  }
  return null;
}

// ════════════════════════════════════════════════════════════════════════════
// FEAT-2326609728 — ONE-CLICK DEMAND FIX (engine core).
//
// The advisor's vague "place a Clinic?" becomes "place N <building>s": N clears
// the WHOLE current shortfall for a service plus a 5% headroom buffer, in one
// building type. demandFixPlan() is the PURE planning half (this file); the
// bulk-place mutation is the 'resolveDemand' reducer action (engine.ts), which
// walks the plan and places count units via the SAME single-tile `place` path
// (findSpot + reduceCore 'place') so connectivity/affordability/road-adjacency
// gating and the eventual placement-STYLE setting (victorian/dispersed/optimal,
// a later feature) all plug in for free — this file introduces NO second
// placement mechanism.
//
// SCOPE (BUG-572 follow-up, 2026-09-02): covers every serviceCoverageOf() row
// with a REAL demand/coverage number today — nursery/primary/college/gp/hosp/
// police/fire/cleanwater/waste/power/parks (BUG-571 gave fire a real
// unlock-aware provider via optimalProvider(); this pass adds parks, the last
// serviceCoverageOf() row that still lacked one) — plus refuse (wasteStatsOf()
// generated-vs-capacity, the twin coverage function for the collection-depot
// service). demandFixPlan only ever emits an entry when a real (need, have)
// pair says there IS a shortfall AND a registered provider exists.
// ════════════════════════════════════════════════════════════════════════════

/**
 * ⚠ BALANCE-NUMBER PLACEHOLDER — Aaron ruling BUG-601 (2026-09-02, SUPERSEDED
 * 2026-09-03): a shortfall-clearing action (the Fix (N) button, Fix All, and
 * Auto-build) sizes to THIS fraction of the OUTSTANDING shortfall per action,
 * funds-capped otherwise — never a hand-picked absolute amount. Originally
 * 0.5 (leave real headroom for a follow-up action, never fully resolve in one
 * press); Aaron's 2026-09-03 superseding ruling on the SAME BUG-601 item
 * raised it to 1.5 — auto-place must OVERSHOOT to 150% of the outstanding
 * gap, deliberately building HEADROOM instead of leaving a shortfall, the
 * opposite intent of the original 50% ruling but the SAME mechanism (one
 * named constant, every caller derives its wording/count from it — GR#15,
 * never a hardcoded "50%"/"150%" string). The funds-cap semantics are
 * unchanged either way: this constant sizes the TARGET only; `placePlanItem`'s
 * cost>0 && funds<cost guard still caps what actually gets PLACED. Directional
 * only, pending Aaron's row-by-row balance pass.
 */
export const AUTO_BUILD_DEMAND_FRACTION = 1.5;

/** Display-only percentage form of AUTO_BUILD_DEMAND_FRACTION (GR#15: every
 *  "builds N%" string in the UI derives from the SAME constant above, never a
 *  hand-typed "50%"/"150%" literal that could silently drift from the real
 *  sizing arithmetic). */
export const AUTO_BUILD_DEMAND_PERCENT = Math.round(AUTO_BUILD_DEMAND_FRACTION * 100);

/**
 * BUG-652 follow-up (2026-09-04, round-mandated caveat) — a NEW placement of
 * a mega-employer spec still hits the exact wage cliff grandfathering
 * doesn't (and can't) protect against: the round measured a single new
 * land_airport adding £1,870,000/tick in wages against a mid-game city's
 * ENTIRE £135,967/tick gross inflow (a 13.7x ratio), insolvent by tick 9.
 * PLACEMENT_AFFORDABILITY_WAGE_FRACTION is the threshold, expressed as a
 * FRACTION of the city's own current gross inflow (never an absolute £
 * figure — GR#15, the threshold scales with the city, not a fixed number
 * that reads as generous for a megacity and draconian for a hamlet). A
 * placement whose marginal wage-bill contribution (placementAffordability()
 * below) exceeds this fraction of current gross inflow triggers a
 * confirmation notice instead of silently charging (engine.ts's 'place'
 * reducer case). 0.5 chosen so anything genuinely disproportionate (the
 * round's 13.7x case, or Aaron's own real-save 53.6% wage-bill jump) trips
 * it comfortably, while an ordinary shop/office placement in a going
 * concern (a small fraction of one sector's existing wage bill) does not.
 * ⚠ PLACEHOLDER-balance — directional only, pending Aaron's row-by-row pass
 * (house convention, same disclaimer as AUTO_BUILD_DEMAND_FRACTION above).
 */
export const PLACEMENT_AFFORDABILITY_WAGE_FRACTION = 0.5;

/** One placement's affordability read-out — see placementAffordability()'s own doc comment. */
export interface PlacementAffordability {
  /** This placement's marginal contribution to the city's per-tick wage bill, once online and fully staffed. */
  marginalWagePerTick: number;
  /** The city's current gross (pre-outflow) income, £/tick — the basis the threshold is derived from. */
  grossInflowPerTick: number;
  /** True when marginalWagePerTick exceeds PLACEMENT_AFFORDABILITY_WAGE_FRACTION of grossInflowPerTick. */
  exceedsThreshold: boolean;
  /** Pre-formatted confirmation copy naming the real recurring cost — used verbatim by the UI-side confirm surface (MapView.tsx), never reconstructed there. Empty string when exceedsThreshold is false. */
  message: string;
}

/**
 * ROUND r4 FIX (F5, 2026-09-04, round-mandated): the round proved
 * placementAffordability()'s single-spec shape was called from exactly ONE
 * UI dispatch site (the single-tile build click) and BYPASSED by every
 * batch path — drag-paint (N tiles flush as one 'placeMany'), stampRegion
 * (clone-paste, potentially several DIFFERENT specs at once), and
 * resolveDemand/resolveDemandAll (the advisor's own "Fix"/"Fix All",
 * previously unreachable only by pricing accident). Proven live: 3 Channel
 * Tunnel Portals drag-painted for 180% of gross inflow with zero
 * confirmation, because each tile alone (jobs already placed via 'placeMany'
 * in one shot) was never checked at all.
 *
 * batchPlacementAffordability() is the SSOT this bug's fix builds on:
 * aggregate the marginal wage-bill impact of EVERY spec in a whole batch —
 * a drag-paint run, a clone-paste, or a demand-fix plan's whole build list —
 * against the SAME filledJobsBySector/sectorWagesPerTick SSOT the real
 * 'Wages' outflow line uses (GR#3), never a re-derived formula. Adds every
 * spec's job capacity into the CURRENT capacity-by-sector snapshot ONCE,
 * then compares the resulting (workforce-capped) wage bill against today's —
 * this correctly reflects that every new employer in the SAME batch draws
 * from the SAME finite workforce (the round's own method: a mid-game city's
 * 33,000 available workers absorbed entirely into one 76,000-job airport,
 * filled=33,000 not 76,000 — extended here to N buildings at once).
 *
 * ROUND r2 FIX (F3, carried forward unchanged): each spec's job count is
 * read via jobsAtTier(sp, 0) — the estate's OWN collision-safe SSOT (this
 * file's totalJobs()/totalJobsBySector() use it too) — never
 * capacityAtTier(sp, 0) directly. hea_teaching carries `jobs: 1450`
 * ALONGSIDE a capacityTiers ladder sized for its `served` figure (200,000 as
 * of FEAT-2326609761's balance bump, was 120,000);
 * capacityAtTier(sp,0) blindly returns that ladder's tier-0 value (200,000)
 * regardless of which field it was built for, a huge overstatement.
 *
 * IMPORTANT: this function is PURE and UI-facing ONLY (called from a SHARED
 * UI dispatch seam — see src/components/placementGate.ts — BEFORE any
 * batch-placing action is ever dispatched) — round r3 moved the gate OUT of
 * the 'place' reducer case entirely (r2's REJECT F1/F2: a reducer that can
 * refuse a journalled action breaks replay by construction, and a reducer-
 * side notice field with no UI reader is a silent, unrecoverable dead end).
 * Every reducer stays pure and ALWAYS places once its own funds/unlock/
 * bounds/fits checks pass — round r4 does not touch the reducer layer at
 * all, only widens the UI-side check to cover every batch path. Nothing
 * about this function's result is stored in SimState or journaled.
 *
 * Bootstrap exemption: a city with zero recorded gross inflow yet (the
 * opening ticks of a brand-new game, before any tax has accrued) has no
 * meaningful income baseline to compare against — gating on a £0 threshold
 * would trip on the player's very first office, which is not what this
 * check exists to catch. `exceedsThreshold` is unconditionally false when
 * grossInflowPerTick <= 0.
 *
 * Message shape: a single-spec batch (or a batch of N copies of ONE spec —
 * the drag-paint/demand-fix common case) reads "<name>" or "N x <name>"
 * exactly like the existing single-placement copy; a MIXED-spec batch
 * (stampRegion pasting a multi-type clipboard) reads "N buildings".
 *
 * Pure, deterministic (GR#21): no Date/Math.random, no mutation, order-
 * independent (only spec identity + count matters, never array order).
 */
export function batchPlacementAffordability(s: SimState, specs: Spec[]): PlacementAffordability {
  const grossInflowPerTick = s.lastFlows.inflows.reduce((sum, f) => sum + f.value, 0);
  if (specs.length === 0 || grossInflowPerTick <= 0) {
    return { marginalWagePerTick: 0, grossInflowPerTick, exceedsThreshold: false, message: '' };
  }

  const currentCapacity = totalJobsBySector(s);
  const hypotheticalCapacity: SectorJobs = { ...currentCapacity };
  let anyJobs = false;
  for (const sp of specs) {
    const sector = KIND_TO_WAGE_SECTOR[sp.kind];
    const newJobs = sp.jobs ? jobsAtTier(sp, 0) : sp.kind === 'commercial' ? 12 : sp.kind === 'industrial' ? 18 : 0;
    if (newJobs > 0 && sector) {
      hypotheticalCapacity[sector] += newJobs;
      anyJobs = true;
    }
  }
  if (!anyJobs) {
    return { marginalWagePerTick: 0, grossInflowPerTick, exceedsThreshold: false, message: '' };
  }

  const currentWages = sectorWagesPerTick(filledJobsFromCapacityAndPopulation(currentCapacity, s.population)).totalPerTick;
  const hypotheticalWages = sectorWagesPerTick(filledJobsFromCapacityAndPopulation(hypotheticalCapacity, s.population)).totalPerTick;
  const marginalWagePerTick = hypotheticalWages - currentWages;
  const exceedsThreshold = marginalWagePerTick > grossInflowPerTick * PLACEMENT_AFFORDABILITY_WAGE_FRACTION;

  let subject = 'buildings';
  if (specs.length > 0) {
    const firstId = specs[0].id;
    const uniform = specs.every((sp) => sp.id === firstId);
    subject = uniform ? (specs.length > 1 ? `${specs.length} x ${specs[0].name}` : specs[0].name) : `${specs.length} buildings`;
  }

  return {
    marginalWagePerTick,
    grossInflowPerTick,
    exceedsThreshold,
    message: exceedsThreshold
      ? `${subject} adds ${fmtMoney(marginalWagePerTick)}/tick in wages once staffed — more than ${Math.round(PLACEMENT_AFFORDABILITY_WAGE_FRACTION * 100)}% of your current income (${fmtMoney(grossInflowPerTick)}/tick). Build anyway?`
      : '',
  };
}

/**
 * Single-spec convenience wrapper over batchPlacementAffordability() — kept
 * for callers checking exactly ONE new building (the single-tile build
 * click). See batchPlacementAffordability()'s own doc comment for the full
 * design/round history.
 */
export function placementAffordability(s: SimState, sp: Spec): PlacementAffordability {
  return batchPlacementAffordability(s, [sp]);
}

/**
 * ⚠ BALANCE/PERF-NUMBER (BUG-646, Aaron ruling 2026-09-03: "the autofix looks
 * to have a 250 limit, not enough, make it 2000") — a single 'resolveDemand'/
 * 'resolveDemandAll' dispatch places at most this many building UNITS in
 * total, across however many services it touches. Without a cap, a real
 * dogfood city (Aaron's own, ~1.4M population) can plan tens of thousands of
 * units in one click (measured: pop 3M -> 18,718 planned units, 46.6s
 * synchronous at pop 400k and DNF at pop 3M) — each unit walks a site search
 * plus a full 'place' mutation, so the whole click blocks the tab for the
 * length of the batch, and the JOURNALED action then replays that same cost
 * on every future load/replay forever. Deliberately a FIXED CONSTANT (no
 * clock/RNG) — capping by unit COUNT keeps 'resolveDemand'/'resolveDemandAll'
 * fully deterministic and replay-safe; a wall-clock or elapsed-time cap would
 * NOT be (GR#21, Vestige "never wall-clock bounds").
 *
 * REAL MEASUREMENT (this session, replacing the old "250 is a directional
 * guess" placeholder): profiled directly against Aaron's real 29,831-
 * building savepoint (2026-09-03, tick 3100, pop 1,443,526; the savepoint
 * as-shipped plans ZERO units, so population was inflated to force a
 * genuine shortfall). At the OLD 250 cap this measured ~68ms/unit — 17s to
 * place 250 units, WORSE than the BUG-642 "totally unplayable" 5.3s
 * complaint at a cap 8x smaller than the one Aaron is asking for here — from
 * TWO compounding causes, both fixed this session, both data.ts-only plus
 * swapping the existing 'place'-case call site that needed the incremental
 * variants:
 *   1. occupiedSet(placedState)/roadTileSetOf(placedState) inside the
 *      'place' reducer case (engine.ts) were a fresh O(buildings) rebuild on
 *      EVERY single unit (placedState.buildings is a new array reference
 *      each time and both caches key on array identity). FIXED by
 *      occupiedSetIncremental()/roadTileSetIncremental() (clone the
 *      already-cached prior-state Set + add just the new building's own
 *      footprint) — see their doc comments above. This alone cut the
 *      per-unit cost from ~68ms to ~2.4ms (~28x) at moderate scale.
 *   2. Once local land near the housing centroid is mostly built out (true
 *      of Aaron's real city for several services), findSpot()'s widen loop
 *      settles on a window of 180-360 tiles instead of the initial 90, and
 *      the ORIGINAL algorithm re-scored every candidate in that window from
 *      scratch on EVERY unit even though only one tile actually gets
 *      consumed per call. FIXED by createSpotSearchContext()'s sorted
 *      candidate cache (scanAllCore()/scanWindowAll() above) — scan+sort the
 *      window ONCE, serve successive units from the cache — for every spec
 *      with no `tag` and not `kind:'residential'` (the cache is invalidated,
 *      falling back to a fresh scan, for the few real specs where that
 *      doesn't hold — see the cache's own correctness note).
 *
 * NET RESULT: 2000 units of a well-served spec (no local land pressure —
 * e.g. Aaron's real hea_clinic/wat_clean/wat_waste shortfalls) completes in
 * ~1-2s. But the single biggest remaining shortfall in a realistic scenario
 * (a 1.32x population bump: edu_nursery needing 1,343 units, pow_wind 902)
 * still measured ~32ms/unit end-to-end at cap 2000 (~64s median across 5
 * runs) — DOWN from a projected ~85ms/unit/~94s pre-this-session-fix
 * baseline at the same scenario, but not the sub-second result the first
 * fix alone achieves. Isolated with a direct per-service timing pass: the
 * residual cost is NOT findSpot() (near-zero after fix #2) but
 * autoConnect()'s planConnector() BFS (roadConnect.ts, CONNECT_BUDGET=6000)
 * — as a big batch pushes units progressively further from the existing
 * road network to find free land, each placement's road-connector search
 * walks proportionally more of that budget. This is a PRE-EXISTING
 * mechanism, untouched by this fix, shared by every 'place' dispatch in the
 * game (not introduced by resolveDemand/resolveDemandAll) — amortising or
 * bounding it across a batch would mean touching autoConnect/planConnector
 * itself, real engine.ts core-placement-path surgery this session judged
 * out of scope for a cap-increase bug and too risky given engine.ts's
 * CONTENDED state (another lane holds an uncommitted estate on it) —
 * flagged here as a follow-up (a shared/amortised road-BFS budget across a
 * placePlanItem batch) for whoever picks it up next. 2000 is Aaron's
 * explicit number and is NOT re-derived or capped lower by this session
 * despite that residual worst case: every fix above is a strict, real
 * improvement over pre-session behaviour (never worse), 2000 is far better
 * than the OLD 250 cap's pre-fix ~17s, and the very worst realistic case
 * measured (~64s) is a rare, large single-click batch, not the common path
 * (a routine Fix-All click plans far fewer units and completes quickly).
 */
export const RESOLVE_DEMAND_ALL_MAX_UNITS = 2000;

/**
 * One shortfall-clearing build plan for a single service (BUG-601: sized to
 * AUTO_BUILD_DEMAND_FRACTION of the shortfall, not the whole deficit).
 *   count = ceil((need - have) * AUTO_BUILD_DEMAND_FRACTION / unitCapacity)   (always > 0 when present)
 */
export interface DemandFixPlanItem {
  /** serviceCoverageOf() id, or 'refuse' for the wasteStatsOf() collection service. */
  serviceKey: string;
  /** BUG-685: the PRIMARY (largest-capacity) spec `mix[0]` chose — the
   *  "headline" pick for any caller that only wants one name. */
  specId: string;
  /** Credited capacity (creditedUnitCapacity()) ONE unit of `specId` (mix[0])
   *  contributes — see largestFirstFill()'s own doc comment for why this is
   *  the ladder-grown credit, not a fresh unit's tier-0 base. */
  unitCapacity: number;
  /** Current demand/required quantity for the service. */
  need: number;
  /** Current online capacity/coverage for the service. */
  have: number;
  /** BUG-685: TOTAL units across the whole `mix` needed to close
   *  AUTO_BUILD_DEMAND_FRACTION of the (need-have) gap (BUG-601) — always > 0
   *  when this item is present. Was single-spec before BUG-685; a mixed plan
   *  now sums every mix entry's count. */
  count: number;
  /** BUG-685: TOTAL cost across the whole `mix` (sum of every entry's
   *  planCost) — what a demand notice's real £ figure should show. Was
   *  single-spec (`count * specId's placementCost`) before BUG-685. */
  planCost: number;
  /** BUG-685 (Aaron, 2026-09-04, "largest-first, not toy monocultures"): the
   *  full LARGEST-FIRST build plan from largestFirstFill() — one entry per
   *  spec actually used, ordered largest-credited-capacity-first. Always at
   *  least one entry (mix[0] === the specId/unitCapacity pair above);
   *  placePlanItem() (engine.ts) walks every entry to place the REAL mixed
   *  batch, never just `specId`/`count` alone. */
  mix: MixEntry[];
  /** BUG-606 ("is this one hypermarket or 50?"), re-scoped by BUG-685: when
   *  the mix used more than one spec, this is the NEXT-LARGEST spec ALSO
   *  used in this SAME composed plan (mix[1]) — informational context on
   *  what else this plan is building, not a competing alternative plan.
   *  When the mix used only one spec, this falls back to the old semantics
   *  (rankedProviders()' runner-up: a genuine competing "or M x B instead"
   *  single-spec plan for the SAME fixAmount). Null only when no runner-up
   *  exists either way (a single unlocked provider for this service). */
  alternative: { specId: string; count: number; planCost: number } | null;
}

/**
 * Per-service provider predicate + per-unit capacity extractor. Mirrors the
 * exact grouping serviceCoverageOf()/wasteStatsOf() already sum over (GR#3 SSOT
 * — same predicates, not re-derived), so a provider found here is guaranteed to
 * be counted by the coverage function whose (need, have) drove the plan.
 */
const DEMAND_FIX_PROVIDERS: Record<
  string,
  { match: (sp: Spec) => boolean; unitCapacity: (sp: Spec) => number }
> = {
  nursery: { match: (sp) => sp.stage === 'nursery', unitCapacity: (sp) => sp.children ?? 0 },
  primary: {
    match: (sp) => sp.stage === 'primary' || sp.stage === 'city',
    unitCapacity: (sp) => sp.children ?? 0,
  },
  college: { match: (sp) => sp.stage === 'tertiary', unitCapacity: (sp) => sp.children ?? 0 },
  gp: { match: (sp) => sp.id === 'hea_clinic', unitCapacity: (sp) => sp.served ?? 0 },
  hosp: {
    match: (sp) => sp.id === 'hea_hospital' || sp.id === 'hea_teaching',
    unitCapacity: (sp) => sp.served ?? 0,
  },
  police: { match: (sp) => sp.kind === 'police', unitCapacity: (sp) => sp.served ?? 0 },
  cleanwater: {
    match: (sp) => sp.kind === 'water' && sp.tag === 'clean',
    unitCapacity: (sp) => sp.served ?? 0,
  },
  waste: {
    match: (sp) => sp.kind === 'water' && sp.tag === 'waste',
    unitCapacity: (sp) => sp.served ?? 0,
  },
  power: { match: (sp) => sp.kind === 'power', unitCapacity: (sp) => sp.mw ?? 0 },
  refuse: { match: (sp) => (sp.wasteCapacity ?? 0) > 0, unitCapacity: (sp) => sp.wasteCapacity ?? 0 },
  // BUG-571/FEAT-demanddock-overhaul §4: fire was deliberately excluded pending
  // this follow-up — fire_post/fire_station/fire_hq are already kind:'fire'.
  fire: { match: (sp) => sp.kind === 'fire', unitCapacity: (sp) => sp.served ?? 0 },
  // BUG-572 follow-up: parks/leisure specs (park/park_playground/park_town/
  // park_botanical/park_nature) are all kind:'park'; unit capacity is the
  // SAME footprint measure (w×h) parksCapacityOf() sums, so a placed unit's
  // contribution here always matches what the coverage row will count next
  // tick.
  parks: { match: (sp) => sp.kind === 'park', unitCapacity: (sp) => sp.w * sp.h },
};

/**
 * Unified provider selector (FEAT-demanddock-overhaul §2, re-scored after the
 * independent round's REJECT on the original two-branch draft) — replaces
 * cheapestProvider() at both call sites (serviceCoverageOf's fire row,
 * demandFixPlan). Unlock-aware (BUG-571) AND value-aware (FEAT-2326609735,
 * "1 dam not 20 towers").
 *
 * BUG (the REJECT): the original draft ranked cost-per-capacity ONLY among
 * specs that clear the whole shortfall in a SINGLE unit, and never compared
 * that winner against a cheaper MULTI-unit plan of a smaller spec. Proven
 * broken with real SPECS numbers: cleanwater shortfall 20,000 picked one
 * wat_reservoir (£81.0M) over 6× wat_tower (£16.2M, 5x cheaper); shortfall
 * 20,001 fell off a 17x cliff (wat_clean £4.68M -> wat_reservoir £81.0M for
 * ONE extra person of shortfall, when 2× wat_clean is £9.36M); pop 8,000
 * power picked one Offshore Wind Array (£225M) over 14× pow_wind (£67.2M);
 * and a strictly-dominated case (shortfall 2,000: wat_clean £4.68M over
 * wat_tower £2.70M for the SAME single-unit count).
 *
 * THE FIX: score every candidate by its TOTAL PLAN COST to clear the whole
 * shortfall with THAT spec alone — units = ceil(shortfall/unitCapacity),
 * planCost = units*cost — and pick the minimum. This is the plain "which
 * plan costs least" comparison the original draft skipped; it naturally
 * reproduces Aaron's "1 dam not 20 towers" intent whenever the dam's total
 * cost genuinely beats N towers, and picks the towers otherwise — no
 * capacity-boundary cliff, no dominated pick, because it is always comparing
 * WHOLE PLANS, never a bare per-unit or clears-in-one-only figure.
 *
 * Affordability: prefer the cheapest plan whose planCost fits `budget`
 * (so demandFixPlan's downstream resolveDemand loop can complete the WHOLE
 * plan without hitting its own funds cap mid-batch); if no plan fits
 * wholesale, fall back to the cheapest single-unit-affordable spec (Q100055
 * A1 — resolveDemand still places as many of THAT spec as funds allow, it
 * just needs one to start with); if nothing is even single-unit-affordable,
 * fall back to the globally cheapest spec (never returns null while the
 * candidate set is non-empty — same policy as the prior draft).
 *
 * Tie-break: fewer units first (the "prefer 1 dam" preference expressed
 * CORRECTLY — it only wins genuine ties or real value, never overspends),
 * then id ascending (deterministic, GR#21). Returns null only when the
 * candidate set (step 1, unlocked+enterable) is empty — identical null
 * contract to cheapestProvider(), so no caller-side handling changes.
 */
/** One scored candidate spec for a service's shortfall — see rankedProviders(). */
export interface ProviderOption {
  sp: Spec;
  /** Units of `sp` needed to clear the shortfall this candidate was scored against. */
  units: number;
  /** units * placementCost(sp) — what the player is ACTUALLY charged for the
   *  whole plan (D1 fix, BUG-606 independent round REJECT, 2026-09-03):
   *  previously `units * sp.cost`, the catalogue price, which is £0-blind for
   *  any 'zones'-category spec (parks) — a park plan priced at its FULL
   *  catalogue cost (e.g. £150,000) when placementCost() (the field's own
   *  doc comment, data.ts) says zoning is free. placementCost() is the SAME
   *  function 'place'/placePlanItem() actually charge against, so this total
   *  can never diverge from the real bill again. */
  planCost: number;
}

/**
 * BUG-606 ("is this one hypermarket or 50?" — Aaron, 2026-09-03): the full,
 * deterministically-ordered candidate ranking optimalProvider() picks its
 * winner from. Extracted so demandFixPlan() can expose a concrete SECOND
 * option (the runner-up) alongside the winner, letting a demand notice read
 * "N x A or M x B" instead of a single unexplained pick — a pure ADDITION
 * (optimalProvider()'s own contract/return value is unchanged, see below).
 *
 * Preference order (identical to the pre-existing optimalProvider() logic,
 * now expressed as three concatenated, internally-sorted tiers instead of
 * three independent "best so far" trackers — same winner, same tie-breaks):
 *   1. every candidate whose OWN total plan cost fits `budget` wholesale,
 *      cheapest plan first;
 *   2. every candidate whose single-unit cost is affordable, cheapest first
 *      (covers a plan that doesn't fit wholesale but can still be started);
 *   3. everything else, cheapest single-unit cost first.
 * A candidate that already appeared in an earlier tier is not repeated.
 * Tie-break within a tier: fewer units, then spec id ascending (GR#21).
 */
export function rankedProviders(s: SimState, serviceKey: string, budget: number, shortfall: number): ProviderOption[] {
  const rule = DEMAND_FIX_PROVIDERS[serviceKey];
  if (!rule) return [];

  // D1 fix: unitCost is placementCost(sp) — what 'place'/placePlanItem()
  // actually charge — never sp.cost (the catalogue price, £0-blind for
  // 'zones'-category specs). A £0 unitCost means EVERY unit count "fits"
  // any budget, which also delivers the free-zone tie-break design guard
  // below for free: with planCost tied at £0 across every unlocked park
  // spec, the next comparator key (units ascending) naturally prefers the
  // FEWEST units — i.e. the biggest park spec — over carpeting the map with
  // many small ones (Aaron, 2026-09-03: "a few big parks instead of
  // hundreds of 1x1s"). ⚠ BALANCE-NUMBER NOTE for Aaron's pass: this row is
  // the free-zone selection path — confirm "biggest spec wins" is the
  // intended aesthetic before any future re-score touches this tie-break.
  const candidates: (ProviderOption & { unitCost: number })[] = [];
  for (const sp of Object.values(SPECS)) {
    if (!canEnterSim(sp) || !specUnlocked(s, sp)) continue;
    // FEAT-2326609761 AC-29/CEIL-4: an at-cap unique building (e.g. a second
    // Five Gorges Dam) is never a candidate the demand-fix planner offers —
    // recommending a spec the player cannot act on is the BUG-641 "no help"
    // failure again. Every caller of rankedProviders/optimalProvider
    // (demandFixPlan, serviceCoverageOf's fire/parks/refuse rows) inherits
    // this for free.
    if (remainingAllowance(s, sp) <= 0) continue;
    if (!rule.match(sp)) continue;
    const unitCapacity = rule.unitCapacity(sp);
    if (unitCapacity <= 0) continue;
    const units = Math.max(1, Math.ceil(shortfall / unitCapacity));
    const unitCost = placementCost(sp);
    candidates.push({ sp, units, unitCost, planCost: units * unitCost });
  }
  if (candidates.length === 0) return [];

  const cmp = (a: (typeof candidates)[number], b: (typeof candidates)[number], key: 'planCost' | 'cost'): number => {
    const av = key === 'planCost' ? a.planCost : a.unitCost;
    const bv = key === 'planCost' ? b.planCost : b.unitCost;
    if (av !== bv) return av - bv;
    if (a.units !== b.units) return a.units - b.units;
    return a.sp.id < b.sp.id ? -1 : a.sp.id > b.sp.id ? 1 : 0;
  };

  const fitting = candidates.filter((c) => c.planCost <= budget).sort((a, b) => cmp(a, b, 'planCost'));
  const singleAffordable = candidates.filter((c) => c.unitCost <= budget).sort((a, b) => cmp(a, b, 'cost'));
  const rest = [...candidates].sort((a, b) => cmp(a, b, 'cost'));

  const seen = new Set<string>();
  const ranked: ProviderOption[] = [];
  for (const tier of [fitting, singleAffordable, rest]) {
    for (const c of tier) {
      if (seen.has(c.sp.id)) continue;
      seen.add(c.sp.id);
      ranked.push(c);
    }
  }
  return ranked;
}

export function optimalProvider(s: SimState, serviceKey: string, budget: number, shortfall: number): Spec | null {
  return rankedProviders(s, serviceKey, budget, shortfall)[0]?.sp ?? null;
}

/** One spec's slice of a largestFirstFill() mixed plan — see DemandFixPlanItem.mix. */
export interface MixEntry {
  specId: string;
  /** Credited unit capacity (creditedUnitCapacity()) THIS spec was sized against. */
  unitCapacity: number;
  count: number;
  planCost: number;
}

/**
 * BUG-685 (Aaron ruling, 2026-09-04, "wind turbines can not produce the
 * power thats needed" — a 51.7GW city carpeted with 10,033 6MW turbines and
 * ZERO nukes/CCGT/offshore despite all being unlocked): the demand-fix
 * PICKER. Replaces "pick the single cheapest-total-plan spec" (rankedProviders,
 * still used unchanged for the informational fire/parks/refuse recommended-
 * spec labels in serviceCoverageOf, which show ONE suggested name, not a
 * buildable plan) with Aaron's explicit rule for the thing that actually gets
 * BUILT: fill the shortfall with the BIGGEST unlocked, capacity-available
 * spec first, and only reach for smaller specs to cover what the biggest
 * one(s) cannot (either because they'd overshoot, or because a maxPerCity
 * cap/lock has run out of them).
 *
 * ALGORITHM: candidates for `serviceKey` (same match()/canEnterSim/
 * specUnlocked/remainingAllowance gates rankedProviders() uses — unlock-aware,
 * dam-cap-aware) sorted by CREDITED capacity (creditedUnitCapacity() —
 * BUG-686: a fresh unit's ladder-grown capacity, never its tier-0 base)
 * descending, cost ascending / id ascending tie-break (GR#21 determinism).
 * Walk largest-to-smallest, at each step:
 *   - if THIS spec's capacity alone can clear whatever remains, take exactly
 *     ONE unit of it and stop — a shortfall smaller than the biggest
 *     available unit still gets built as ONE building of the biggest spec
 *     that fits it, never deferred down to a monoculture of the smallest
 *     spec just because the big one "doesn't divide evenly" (a hamlet's
 *     40-child nursery shortfall gets ONE Kindergarten; a 18,000-shortfall
 *     fire gap with fire_station (served 20,000) unlocked gets ONE Fire
 *     Station, never 5x Fire Post);
 *   - otherwise take floor(remaining / capacity) WHOLE units (never round up
 *     here — that would let a huge spec overshoot by nearly its own
 *     capacity for no reason when smaller specs can absorb the remainder)
 *     capped at that spec's remainingAllowance(), and move to the
 *     next-smaller spec with whatever remains, exactly as if the exhausted
 *     capacity had never existed;
 *   - the LAST (smallest) candidate always rounds UP (ceil) if anything
 *     still remains once every other candidate has been tried, since there
 *     is no smaller spec left to hand a remainder to.
 *
 * OVERSHOOT BOUNDARY (Aaron's 150% target, AUTO_BUILD_DEMAND_FRACTION,
 * remains the caller's job — this function just fills whatever `shortfall`
 * it is handed): the only two ways this function itself ever rounds UP are
 * the "one unit clears it" branch and the final-candidate closing ceil —
 * both push the total past `shortfall` by strictly LESS than the capacity of
 * the specific unit used, which (sort order) is always <= mix[0]'s (the
 * largest used spec's) capacity. So the whole plan can never overshoot the
 * requested `shortfall` by as much as one largest-candidate-sized unit,
 * satisfying Aaron's rule.
 *
 * Affordability: by DEFAULT (no `budget` argument — every existing caller
 * that never passed one, and every direct test of this function's pure
 * capacity-based sizing) this stays exactly as documented above and always
 * has: NOT enforced, mirroring rankedProviders() budget=s.funds convention
 * at the call site and BUG-601's own doc comment (the downstream
 * resolveDemand/resolveDemandAll reducer places at most `count` units and
 * stops the moment funds run out) — the whole capacity-sized mix is offered
 * and the reducer's own funds gate does the real capping.
 *
 * BUG-685-MONEY (the round's LEAD DEFECT, Aaron/independent-round
 * 2026-09-04): when a caller DOES pass `budget` (demandFixPlan() does, with
 * s.funds), affordability now gates candidate selection too — the one-shot
 * branch's own doc-comment promise ("take exactly one unit and stop") is
 * only honoured for a candidate that can actually be BOUGHT. A candidate
 * that cannot afford even ONE unit at the current running funds pool
 * contributes NOTHING and is skipped entirely (`remaining` untouched), so
 * the walk falls through to the next-SMALLER candidate exactly like an
 * exhausted maxPerCity/remainingAllowance cap already does (same `continue`
 * path) — never leaving the mix stranded on a single unaffordable pick with
 * no smaller fallback ever offered (the pre-fix defect: a GBP5bn dam alone
 * in the mix against a GBP1bn treasury placed ZERO buildings even though a
 * GBP3.6M wind turbine was unlocked, affordable, and simply never reached).
 * A candidate that CAN afford some but not all of its capacity-sized `units`
 * is clamped down to what it can afford (never above the capacity-derived
 * figure — the overshoot boundary above is untouched), and the walk
 * continues to the next-smaller candidate with whatever of `remaining` is
 * left, same as the existing allowance-cap fall-through. This is orthogonal
 * to the DemandDock recurring-cost CONFIRM dialog (a UI gate on an
 * affordable-but-notable ongoing cost) — that stays exactly as-is.
 *
 * RIGHT-SIZED ONE-SHOT (FEAT-2326609772 rebase fix, 2026-09-05, after the
 * civic-tier consolidator landed edu_nursery_city as a same-stage, much
 * BIGGER successor to edu_nursery): the one-shot branch above ("this spec
 * alone clears the remainder — take exactly one unit and stop") used to mean
 * "the CURRENT (biggest-not-yet-tried) candidate", because a single spec was
 * never both a hamlet-scale AND a city-scale candidate for the same service
 * at once. That stopped being true the moment a service family gained TWO
 * one-shot-capable specs of very different scale (edu_nursery credits 1,000
 * children, edu_nursery_city credits 1,179 — both comfortably clear a
 * 40-child hamlet shortfall), and walking strictly biggest-first meant the
 * £40M City Kindergarten always won a 40-child shortfall the £2.16M
 * Kindergarten was completely capable of clearing — the exact "1,000
 * kindergartens is nuts" over-build BUG-685/686 already rejected, just
 * inverted (now an over-BUY instead of an over-COUNT). Aaron's ruling stays
 * intact for a shortfall genuinely bigger than the small spec's one-shot
 * reach (nothing here changes the floor/ceil walk below, which is exactly
 * how a large shortfall still reaches for edu_nursery_city or bigger), AND
 * for every OTHER service family, which is why this is scoped to the
 * consolidator's OWN family key (familyKeyOf: kind|capacityField|tag|stage|
 * careTier — the exact discriminator that already keeps wat_clean separate
 * from wat_waste and hea_clinic separate from hea_hospital), never applied
 * globally: among untried candidates SHARING THE LEADING CANDIDATE'S FAMILY
 * that could SINGLE-HANDEDLY clear whatever is left (capacity >= remaining),
 * pick the SMALLEST such capacity — the least overshoot — not the biggest;
 * tie-break cheaper unit cost then id ascending (same GR#21 total order as
 * the existing sort), then apply the SAME allowance/affordability clamp as
 * every other pick. A candidate outside the family (e.g. pow_nuke sitting
 * next to pow_hydro — different `tag`, so a different family) can NEVER
 * demote the leader this way.
 *
 * FAMILY MEMBERSHIP ALONE IS NOT ENOUGH, THOUGH (round-2 finding on this
 * same fix): wat_tower/wat_clean/wat_reservoir are ALL one `cleanwater`
 * family (kind:'water'+tag:'clean', no stage) — structurally identical in
 * shape to edu_nursery/edu_nursery_city — yet Aaron's OWN pinned test
 * ("BUG-685 LARGEST-FIRST — the biggest unlocked spec wins a one-unit clear
 * even when it costs more") requires wat_reservoir (£81M, served 60,000) to
 * win a ~3,150-shortfall one-shot over wat_tower (£2.7M, served 4,000) —
 * the water family's OWN hamlet-vs-city moment, ruled the OPPOSITE way. The
 * two cases are distinguished by exactly one derivable number: how far
 * apart the candidates' CREDITED capacities sit. edu_nursery (1,000) and
 * edu_nursery_city (1,179) are barely 18% apart — practically the same rung,
 * so paying 18.5x the price (£40M vs £2.16M) for a sliver more headroom is
 * the "1,000 kindergartens is nuts" defect in reverse. wat_tower (4,000) and
 * wat_reservoir (60,000) are 15x apart — genuinely different INFRASTRUCTURE
 * CLASSES (a tower vs a valley dam), where paying more for the big one is
 * exactly RR-5c's DENSITY-FIRST point. ONE_SHOT_SIBLING_RATIO_BOUND (below)
 * is this derived cutoff: a same-family sibling only demotes the leader when
 * the leader's own credited capacity is within that multiple of the
 * sibling's (1.18x for nursery, comfortably under; 15x/16.7x for
 * water/power, comfortably over) — PLACEHOLDER-balance (directional, pending
 * Aaron's row-by-row pass) like every other constant in this file's Balance
 * Number Regime. Only when NO same-family, ratio-bounded candidate can
 * one-shot it does the walk fall through to the original biggest-first
 * floor/ceil behaviour, unchanged.
 */
/** PLACEHOLDER-balance (see largestFirstFill's "FAMILY MEMBERSHIP ALONE IS
 *  NOT ENOUGH" doc comment above): the credited-capacity ratio under which
 *  two same-family one-shot candidates are treated as near-equal rungs of
 *  the SAME infrastructure (right-size to the smaller/cheaper one) rather
 *  than genuinely different infrastructure classes (biggest-first stands).
 *  2x comfortably separates the nursery case (1.18x apart) from every
 *  currently-accepted biggest-wins case (water 15x, power 16.7x+) — derived
 *  from the catalogue's own numbers, not chosen to hit a specific test. */
export const ONE_SHOT_SIBLING_RATIO_BOUND = 2;
export function largestFirstFill(
  s: SimState,
  serviceKey: string,
  shortfall: number,
  budget: number = Infinity
): MixEntry[] {
  const rule = DEMAND_FIX_PROVIDERS[serviceKey];
  if (!rule || shortfall <= 0) return [];

  const candidates: { sp: Spec; capacity: number; unitCost: number }[] = [];
  for (const sp of Object.values(SPECS)) {
    if (!canEnterSim(sp) || !specUnlocked(s, sp)) continue;
    if (remainingAllowance(s, sp) <= 0) continue;
    if (!rule.match(sp)) continue;
    const capacity = creditedUnitCapacity(sp, rule.unitCapacity(sp));
    if (capacity <= 0) continue;
    candidates.push({ sp, capacity, unitCost: placementCost(sp) });
  }
  if (candidates.length === 0) return [];

  // LARGEST-FIRST: biggest credited capacity leads; ties broken by cheaper
  // unit cost, then id ascending (GR#21 — deterministic, no coincidental
  // Object.values() iteration-order dependence).
  candidates.sort((a, b) => {
    if (a.capacity !== b.capacity) return b.capacity - a.capacity;
    if (a.unitCost !== b.unitCost) return a.unitCost - b.unitCost;
    return a.sp.id < b.sp.id ? -1 : a.sp.id > b.sp.id ? 1 : 0;
  });

  const mix: MixEntry[] = [];
  let remaining = shortfall;
  // BUG-685-MONEY: the running funds pool this whole walk draws against —
  // see the doc comment above. Stays at Infinity (never binds) for every
  // caller that omits `budget`, so the pure capacity-sized behaviour this
  // function has always had is untouched by default.
  let fundsRemaining = budget;
  const used = new Set<string>(); // spec ids already consumed by this walk

  while (remaining > 0) {
    const untried = candidates.filter((c) => !used.has(c.sp.id));
    if (untried.length === 0) break;

    // `untried` stays sorted descending (a filtered copy of `candidates`,
    // whose order Array.prototype.filter preserves), so untried[0] is the
    // biggest-remaining candidate — the walk's default pick, exactly as
    // before.
    let c = untried[0];

    if (c.capacity >= remaining) {
      // ONE-SHOT branch: `c` alone can clear whatever remains.
      //
      // RIGHT-SIZED ONE-SHOT (see doc comment above): before committing to
      // the globally-biggest candidate, check for a SMALLER SAME-FAMILY
      // sibling (consolidator familyKeyOf — e.g. edu_nursery next to its own
      // civic-tier successor edu_nursery_city) that can ALSO one-shot
      // `remaining` AND is a near-equal RUNG of the same infrastructure
      // (credited capacity within ONE_SHOT_SIBLING_RATIO_BOUND of the
      // leader's — see the constant's own doc comment for why this bound is
      // what keeps RR-5c's DENSITY-FIRST ruling intact for water/power,
      // whose family-mates are genuinely different scales of infrastructure,
      // while still fixing edu_nursery/edu_nursery_city, whose credited
      // capacities sit within 18% of each other). A candidate from a
      // DIFFERENT family (e.g. a different power plant entirely), or one far
      // enough below the leader to be a genuinely smaller infrastructure
      // class (e.g. pow_offshore under pow_hydro, wat_tower under
      // wat_reservoir), never demotes the leader.
      const family = familyKeyOf(c.sp);
      const siblingCoverers = untried.filter(
        (x) => familyKeyOf(x.sp) === family && x.capacity >= remaining && c.capacity <= x.capacity * ONE_SHOT_SIBLING_RATIO_BOUND
      );
      siblingCoverers.sort((a, b) => {
        if (a.capacity !== b.capacity) return a.capacity - b.capacity; // smallest coverer first
        if (a.unitCost !== b.unitCost) return a.unitCost - b.unitCost;
        return a.sp.id < b.sp.id ? -1 : a.sp.id > b.sp.id ? 1 : 0;
      });
      c = siblingCoverers[0]; // always includes the original leader, so never empty

      const allowance = remainingAllowance(s, c.sp); // > 0, checked above (Infinity when uncapped)
      const maxAffordable = c.unitCost > 0 ? Math.floor(fundsRemaining / c.unitCost) : Infinity;
      const units = Math.min(1, allowance, maxAffordable);
      used.add(c.sp.id);
      if (units <= 0) continue; // this pick can't actually be bought/allowed — let the next iteration try another
      mix.push({ specId: c.sp.id, unitCapacity: c.capacity, count: units, planCost: units * c.unitCost });
      remaining -= units * c.capacity;
      fundsRemaining -= units * c.unitCost;
      continue;
    }

    // No candidate can one-shot the remainder: the original biggest-first
    // floor/ceil walk, one step at a time.
    const isLast = untried.length === 1;
    const allowance = remainingAllowance(s, c.sp); // > 0, checked above (Infinity when uncapped)
    let units: number;
    if (isLast) {
      units = Math.ceil(remaining / c.capacity); // no smaller spec left — must close the gap
    } else {
      units = Math.floor(remaining / c.capacity); // never overshoot on a spec too small to one-shot it
    }
    // BUG-685-MONEY: clamp to what the running funds pool can actually buy —
    // never MORE than the capacity-derived figure above (the overshoot
    // boundary is untouched), only ever less. A candidate that can't afford
    // even one unit clamps to 0 and falls through to the next-smaller
    // candidate, never silently stranded.
    const maxAffordable = c.unitCost > 0 ? Math.floor(fundsRemaining / c.unitCost) : Infinity;
    units = Math.min(units, allowance, maxAffordable);
    used.add(c.sp.id); // this spec's slot in the walk is spent either way
    if (units <= 0) continue; // capped at 0 allowance/funds, or a floor()==0 remainder — defer to the next-smaller spec
    mix.push({ specId: c.sp.id, unitCapacity: c.capacity, count: units, planCost: units * c.unitCost });
    remaining -= units * c.capacity;
    fundsRemaining -= units * c.unitCost;
  }
  return mix;
}

/**
 * Pure demand-fix plan (FEAT-2326609728): one entry per service currently in
 * shortfall (need > have) that has an unlocked provider. No mutation, no
 * Date/Math.random (GR#21) — a pure function of `s`.
 *
 * BUG-601 (Aaron ruling, 2026-09-02): the plan sizes to
 * AUTO_BUILD_DEMAND_FRACTION (50%) of the OUTSTANDING shortfall, not the
 * whole deficit — a single Fix/Auto-build action deliberately leaves
 * headroom for a follow-up action rather than fully resolving the service in
 * one press. `have` (the row's CURRENT capacity, unaffected by the fraction)
 * still gates whether a row appears at all: any real deficit (need > have)
 * yields an entry, derived straight from the coverage functions (GR#15),
 * never a hand-picked threshold.
 *
 * BUG-685 (2026-09-04): the actual BUILD plan (`mix`) now comes from
 * largestFirstFill() — biggest unlocked spec first, smaller specs only for
 * the remainder — not from rankedProviders()' single-cheapest-plan pick.
 * `specId`/`unitCapacity`/`count`/`planCost` mirror `mix[0]` (the primary,
 * LARGEST spec used) so every existing "headline" reader (a demand notice's
 * "N x <Name>") keeps reading the biggest, most-representative pick; a
 * caller that needs the FULL build (placePlanItem, engine.ts) walks `mix`.
 * `alternative`: when the mix used more than one spec, this is the
 * next-largest spec ALSO used in this SAME composed plan (mix[1]) — no
 * longer a competing single-spec plan (that only existed because the old
 * picker chose exactly one spec for the whole shortfall); when the mix used
 * only one spec, this falls back to rankedProviders()' runner-up (a genuine
 * "or you could use M x B instead" competing pick), preserving that
 * informational case unchanged.
 */
export function demandFixPlan(s: SimState): DemandFixPlanItem[] {
  const waste = wasteStatsOf(s);
  const rows: { serviceKey: string; need: number; have: number }[] = [
    ...serviceCoverageOf(s).map((c) => ({ serviceKey: c.id, need: c.need, have: c.cap })),
    { serviceKey: 'refuse', need: waste.generated, have: waste.capacity },
  ];

  const plan: DemandFixPlanItem[] = [];
  for (const row of rows) {
    if (row.need <= 0) continue;
    const shortfall = row.need - row.have;
    if (shortfall <= 0) continue; // already at/above need — nothing to fix
    const fixAmount = shortfall * AUTO_BUILD_DEMAND_FRACTION;
    // BUG-685-MONEY (Aaron/independent-round 2026-09-04): demandFixPlan()'s
    // own mix/specId/count stay PURE capacity-driven — no `budget` argument
    // here — so the informational "headline" pick (a demand notice's "N x
    // <Name>", the pop-8,000 "biggest unlocked spec wins even though it
    // costs far more than a turbine" contract) never silently downgrades to
    // a cheaper spec just because today's treasury cannot yet afford it.
    // The real fall-through for an UNAFFORDABLE primary pick lives at BUILD
    // time in placePlanItem() (engine.ts) — see largestFirstFill()'s own doc
    // comment for the full defect/fix writeup and why the two layers must
    // stay decoupled.
    const mix = largestFirstFill(s, row.serviceKey, fixAmount);
    if (mix.length === 0) continue; // no unlocked provider yet — omit (needs-unlock)
    const primary = mix[0];
    const count = mix.reduce((sum, m) => sum + m.count, 0);
    const planCost = mix.reduce((sum, m) => sum + m.planCost, 0);
    if (count <= 0) continue;
    let alternative: { specId: string; count: number; planCost: number } | null = null;
    if (mix.length > 1) {
      alternative = { specId: mix[1].specId, count: mix[1].count, planCost: mix[1].planCost };
    } else {
      const ranked = rankedProviders(s, row.serviceKey, s.funds, fixAmount);
      const alt = ranked.find((c) => c.sp.id !== primary.specId) ?? null;
      alternative = alt ? { specId: alt.sp.id, count: alt.units, planCost: alt.planCost } : null;
    }
    plan.push({
      serviceKey: row.serviceKey,
      specId: primary.specId,
      unitCapacity: primary.unitCapacity,
      need: row.need,
      have: row.have,
      count,
      planCost,
      mix,
      alternative,
    });
  }
  return plan;
}

/**
 * BUG-606 fix-all: demandFixPlan() ordered by Aaron's established priority —
 * Health (gp/hosp) pinned together at the top, sorted between themselves by
 * raw outstanding gap (worse-covered leads), then every other service by raw
 * gap descending, id tie-break (GR#21 determinism). SAME comparator shape
 * DemandDock.tsx's `sortedServices` already applies to serviceDemandOf() rows
 * (GR#3 SSOT — reused here against demandFixPlan() items rather than
 * re-deriving a second priority rule), so a "Fix All" build order can never
 * disagree with what the DemandDock row list visually shows as most-pressing.
 * Pure (GR#21): no mutation, no Date/Math.random.
 */
export function orderedDemandFixPlan(s: SimState): DemandFixPlanItem[] {
  const plan = demandFixPlan(s);
  return [...plan].sort((a, b) => {
    const aHealth = a.serviceKey === 'gp' || a.serviceKey === 'hosp';
    const bHealth = b.serviceKey === 'gp' || b.serviceKey === 'hosp';
    if (aHealth !== bHealth) return aHealth ? -1 : 1;
    const gapA = a.need - a.have;
    const gapB = b.need - b.have;
    if (gapB !== gapA) return gapB - gapA;
    return a.serviceKey < b.serviceKey ? -1 : a.serviceKey > b.serviceKey ? 1 : 0;
  });
}

// ---------- milestones / policies / misc ----------

export interface MilestoneDef {
  id: string;
  label: string;
  detail: string;
  test: (s: SimState) => boolean;
}

export const MILESTONES: MilestoneDef[] = [
  { id: 'm1', label: 'First Homes', detail: 'Zone your first residential tiles', test: (s) => countByKind(s.buildings).residential > 0 },
  { id: 'm2', label: 'Village Green', detail: 'Reach 100 citizens', test: (s) => s.population >= 100 },
  { id: 'm3', label: 'Market Town', detail: '8 commercial buildings trading', test: (s) => countByKind(s.buildings).commercial >= 8 },
  { id: 'm4', label: 'Full Services', detail: 'Power, water, health and education online', test: (s) => {
      const c = countByKind(s.buildings);
      return c.power > 0 && c.water > 0 && c.health > 0 && c.school > 0;
    } },
  { id: 'm5', label: 'Solvent City', detail: 'Run a budget surplus for a full 60 ticks', test: (s) => s.tick > 60 && s.history.slice(-60).length === 60 && s.history.slice(-60).every((h) => h.income >= h.expense) },
  { id: 'm6', label: 'Metropolis', detail: 'Reach 1,000 citizens', test: (s) => s.population >= 1000 },
];

/**
 * FEAT-milestone-cash-rewards-2026-09-02 (Q100047b ruling B1) — one-time cash
 * reward paid the first time each MILESTONES entry is observed met (see
 * engine.ts's advance() + sanitizeClaimedMilestones below). Defined as RATIOS
 * of STARTING_TREASURY (fiscal.ts SSOT — mirrors BAILOUT_INCOME_INJECTION/
 * UNLOCK_ALL_COST's "one constant, everything else scales" convention) rather
 * than flat literals, roughly ordered by each milestone's difficulty tier —
 * m1 is a same-tick trivial first-placement, m6 is a genuine late-game goal.
 * ⚠ PLACEHOLDER-balance (Balance Number Regime, Vestige
 * metropolis-balance-number-regime): directional only, pending Aaron's
 * row-by-row balance pass — do not treat these ratios as tuned.
 */
export const MILESTONE_REWARDS: Record<string, number> = {
  m1: Math.round(STARTING_TREASURY * 0.01), // First Homes
  m2: Math.round(STARTING_TREASURY * 0.02), // Village Green
  m3: Math.round(STARTING_TREASURY * 0.05), // Market Town
  m4: Math.round(STARTING_TREASURY * 0.08), // Full Services
  m5: Math.round(STARTING_TREASURY * 0.12), // Solvent City
  m6: Math.round(STARTING_TREASURY * 0.2), // Metropolis
};

/**
 * FEAT-milestone-cash-rewards-2026-09-02 (GR#16 — never trust the TS type,
 * coerce via a sanitizeX() helper). Covers BOTH backward tolerance for a
 * legacy state predating SimState.claimedMilestones (undefined -> []) AND a
 * corrupt save's non-array/non-string/unrecognised-id entries: keeps only
 * strings that name a CURRENT MILESTONES.id (so a future catalogue edit that
 * drops/renames a milestone can't leave a dangling claim around forever),
 * deduplicated. Order is not meaningful (a Set of claims), but the returned
 * array preserves MILESTONES' own catalogue order for determinism (GR#21) —
 * two engines fed the same corrupt/legacy input always produce byte-identical
 * output, never insertion-order-of-the-corrupt-input dependent.
 */
export function sanitizeClaimedMilestones(v: unknown): string[] {
  if (!Array.isArray(v)) return [];
  const validIds = new Set(MILESTONES.map((m) => m.id));
  const claimed = new Set(v.filter((x): x is string => typeof x === 'string' && validIds.has(x)));
  return MILESTONES.map((m) => m.id).filter((id) => claimed.has(id));
}

export interface PolicyDef {
  id: 'recycling' | 'transitSubsidy' | 'tourismDrive' | 'austerity';
  label: string;
  description: string;
}

export const POLICIES: PolicyDef[] = [
  { id: 'recycling', label: 'Recycling Mandate', description: '-7% utility & service upkeep, -2 approval' },
  // BUG-397 (Aaron's ruling, 2026-08-31): the old copy ("costs £1.5 per
  // resident per tick") claimed an uncapped, purely-linear-with-population
  // cost — exactly the guaranteed-late-game-bankruptcy shape the ruling
  // fixed. The real trade-off is now: turning this ON forgoes fare revenue
  // AND pays a subsidy that scales sub-linearly with population and is
  // capped as a fraction of tax income (fiscal.ts's SERVICE_COST_SCALE_EXPONENT
  // / POLICY_COST_CAP_FRACTION) — described directionally, no bare formula
  // in player-facing copy.
  { id: 'transitSubsidy', label: 'Free Transit', description: '+25% growth rate and +8 approval; forgoes fare revenue and pays a capped, scaling subsidy instead' },
  { id: 'tourismDrive', label: 'Tourism Drive', description: 'Adds Tourism income scaling with population' },
  { id: 'austerity', label: 'Austerity Budget', description: '-10% all outflows, -12 approval' },
];

export const UNIT_REGISTRY = [
  { unit: 'pound (£)', dimension: 'currency', note: 'All fiscal flows; integers only in the engine' },
  { unit: 'person', dimension: 'population', note: 'Persistent individual citizens' },
  { unit: 'MW', dimension: 'power', note: 'Plant capacity vs grid draw' },
  { unit: 'kL/day', dimension: 'water', note: 'Works throughput' },
  { unit: 'tick', dimension: 'time', note: 'One in-game day; two-layer clock base' },
  { unit: 'tile', dimension: 'length/area', note: '50 m map grid cell' },
];
