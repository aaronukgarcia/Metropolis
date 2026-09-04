// consolidator.ts — FEAT-2326609761 inc1, DISCOVERY + SECTION AUDIT half only.
//
// Scope discipline (Aaron's dev-team split, 2026-09-03): this module is the
// READ-ONLY analysis layer for the CONSOLIDATOR "urban regenerator"
// (docs/planning/acceptance/FEAT-2326609761.md). It answers "what does the
// city look like, sector by sector, and what WOULD be worth doing about it" —
// it never demolishes, relocates, spends money, or mutates SimState. The
// mutation half (apply/undo/economics, AC-16..AC-31) is a SEPARATE lane
// landing independently; this file exports nothing that can change state, so
// both lanes can land without colliding on behaviour.
//
// Kept OUT of data.ts/engine.ts deliberately (task brief 2026-09-03): both
// files are hot, heavily contended by other lanes, and every helper this
// module needs (memoOnState, SPECS, capacityAtTier, placementCost,
// canEnterSim, isOnline) is already a stable, exported read-only surface —
// there is no need to touch either file for a pure analysis pass.
//
// Golden Rule #21 (determinism): every function here is a pure fold over
// SimState. No Date.now/performance.now/Math.random/localStorage. No
// `for (const x of someMap) { ...; break; }` — every Map/Set walk here is
// either an order-independent accumulation or a subsequent sort over an
// explicit array before any exit-early logic.
//
// Golden Rule #15 (validators derive from data): SECTION_TILES is DERIVED
// from CONSOLIDATOR_SECTION_METRES / TILE_METRES, never a bare literal `4`.

import type { SimState, ZoneKind } from './types.ts';
import {
  SPECS,
  canEnterSim,
  capacityAtTier,
  memoOnState,
  placementCost,
  computeFailedGates,
  isOnline,
  connectedRoadTileSet,
} from './data.ts';
import type { Spec, Tag } from './data.ts';
import { TICKS_PER_MONTH, CONNECT_EXEMPT_KINDS } from './engine.ts';

// ---------------------------------------------------------------------------
// §1 Vocabulary and derived constants (acceptance doc §3)
// ---------------------------------------------------------------------------

/**
 * Mirrors the tile-grid reference size documented at data.ts:122 ("Tile grid
 * = 50 m"). The acceptance doc's AC-3 calls for promoting that comment to a
 * shared export in data.ts — deliberately NOT done here (see file header):
 * data.ts is contended by other lanes, and this constant is needed by only
 * this read-only module in inc1. If/when the mutation lane (or a later
 * increment) promotes a real `TILE_METRES` export from data.ts, this local
 * constant should be replaced by that import in one line — the VALUE is
 * already the single source of truth (50), just not yet a shared symbol.
 */
export const TILE_METRES = 50;

/**
 * AARON'S RULING (2026-09-03, via the coordinator, on this module's own
 * measured finding): SECTION SIZE IS 800m (16x16 tiles), overriding the
 * original "e.g. 200m x 200m" example in his words (§1). History matters
 * here — this was NOT picked, it was PROVEN wrong at 200m and corrected:
 *
 *   ORIGINAL REASONING (200m/4x4, now superseded): Aaron's own example is
 *   "10 wind turbines" (1x1 footprint) becoming "a more efficient wind
 *   farm" (pow_windfarm, 3x3). A 4x4 section (16 tiles) looked like the
 *   smallest unit that could hold both a turbine group and its successor
 *   without spilling out, and a single 200m section seemed too coarse for
 *   "10 turbines in an area" to stay a precise statement rather than
 *   "60 turbines scattered across the same patch".
 *
 *   WHAT THE REAL DATA SHOWED (the measurement that overturned it): on
 *   Aaron's actual 29,831-building/1,991-pow_wind savepoint, the MAXIMUM
 *   number of same-spec buildings ever co-located inside a real 4x4-tile
 *   section was measured directly at **10** (pow_wind) — every other
 *   consolidatable spec's max co-location was lower still (hea_clinic 7,
 *   fire_post 3). AC-8's derived group-size rule needs **37** co-located
 *   pow_wind turbines for the cheapest real successor rung (pow_wind ->
 *   pow_offshore — `groupSize = floor(capacityOf(B) / capacityOf(A))`, the
 *   round-1-corrected formula, see `groupSizeOf` below; this section's
 *   threshold was originally measured against the PRE-fix ceil value of 38,
 *   corrected here to 37 after that round); the "obvious" rungs (->
 *   pow_windfarm/pow_nuke) are independently refused by the AC-8 rule-4
 *   density test on today's catalogue numbers (the AC-9 finding). Result:
 *   **zero** sections in the whole real city ever qualified for ANY ladder
 *   rung at 4x4 — the section was real-world too small to ever host the
 *   group CEIL-1 requires, regardless of how the ladder itself is tuned.
 *
 *   Re-measuring pow_wind's max co-location at larger section sizes on a
 *   later real savepoint (49,174 buildings/4,755 pow_wind, 2026-09-04;
 *   Chebyshev/raster bucketing at each tile size, independent of this
 *   module's own code), against the corrected >=37 threshold:
 *     4x4  (200m):  max co-located = 10   sections with >=37 = 0
 *     8x8  (400m):  max co-located = 26   sections with >=37 = 0
 *     16x16(800m):  max co-located = 69   sections with >=37 = 30
 *     32x32(1600m): max co-located = 164  sections with >=37 = 48
 *     64x64(3200m): max co-located = 574  sections with >=37 = 19
 *   800m is the SMALLEST section size at which the real city's actual wind
 *   farm clustering crosses the group-size threshold ANY rung needs — the
 *   arithmetic Aaron took as his ruling, and it holds under the corrected
 *   threshold exactly as it did under the original (mistaken) one. (1600m
 *   does better on this one spec, but 800m is the minimum sufficient size,
 *   keeping the audit as fine-grained as the data allows rather than
 *   coarser than it has to be — CEIL-1 locality still means a coarser
 *   section only ever helps grouping, never hurts it, so 800m is
 *   deliberately not rounded up further without a second real-city data
 *   point motivating it.)
 *
 * PLACEHOLDER-balance still applies to the VALUE (directional, per the
 * standing player-felt-numbers regime, and Aaron may retune again once he
 * has played against it) — but it is no longer a bare example figure. It is
 * now a ruling anchored to a real measurement, not a guess.
 */
export const CONSOLIDATOR_SECTION_METRES = 800;

/**
 * DERIVED, never a literal `16` (GR#15). At the current ruling value this is
 * exactly 800/50 = 16 (a 16x16-tile / 256-tile section).
 *
 * Section count at 16x16: ceil(440/16) x ceil(260/16) = 28 x 17 = 476
 * sections (down from 7,150 at 4x4) — note 440/16=27.5 and 260/16=16.25, so
 * BOTH axes have a partial final section (the map does not divide evenly by
 * 16); `sectionOriginOf`'s existing clip-to-map-edge logic (`Math.min(
 * SECTION_TILES, MAP_W - x0)` / same for h) already handles this without any
 * change — the exhaustive tile-coverage test re-proves it at the new grid
 * size. For a 29,831-building city that is ~62.7 buildings/section on
 * average (up from ~4.2 at 4x4) — coarser, as intended: the whole point of
 * the ruling is that inc1's locality constraint (CEIL-1: a group must lie
 * wholly inside one section) needs a section large enough to actually
 * contain the group sizes AC-8's derived rule produces on today's catalogue,
 * not a section sized to an intuition about "how big does a patch of city
 * feel". A single 800m x 800m section is still far too coarse to be the
 * unit of "the whole map" (that IS the whole map, see AC-6/ruling 7's
 * monthly-twelfth cadence below) and far too fine to mean "the whole tile"
 * ruling 7 refers to for month 12 — those are different granularities
 * entirely, which is why the monthly ROTATION (§4 below) partitions
 * SECTIONS, not tiles, into twelfths (and does so completely independently
 * of SECTION_TILES's value — re-proven by test at the new 476-section grid).
 */
export const SECTION_TILES = Math.round(CONSOLIDATOR_SECTION_METRES / TILE_METRES);

/** Local mirror of data.ts's MAP_W/MAP_H (data.ts:43-44) — read-only constants, safe to duplicate the VALUE reference without importing the whole data module surface twice. */
export const MAP_W = 440;
export const MAP_H = 260;

/** Section grid dimensions, derived from the map size and SECTION_TILES (never hand-computed). Edge sections are partial (clipped to the map boundary). */
export const SECTIONS_X = Math.ceil(MAP_W / SECTION_TILES);
export const SECTIONS_Y = Math.ceil(MAP_H / SECTION_TILES);
export const TOTAL_SECTIONS = SECTIONS_X * SECTIONS_Y;

/**
 * Deterministic, integer section key for a tile — raster order, ascending in
 * both x and y. A building spanning a section boundary is owned by the
 * section containing its ORIGIN tile (b.x, b.y) — ASM-1493's adopted
 * convention, mirroring how occupiedSet/fits already treat origins.
 */
export function sectionKeyOf(x: number, y: number): number {
  const sx = Math.floor(x / SECTION_TILES);
  const sy = Math.floor(y / SECTION_TILES);
  return sy * SECTIONS_X + sx;
}

/** The origin tile and clipped size of a section, from its key. Inverse of sectionKeyOf's bucketing. */
export function sectionOriginOf(key: number): { x0: number; y0: number; w: number; h: number } {
  const sx = key % SECTIONS_X;
  const sy = Math.floor(key / SECTIONS_X);
  const x0 = sx * SECTION_TILES;
  const y0 = sy * SECTION_TILES;
  const w = Math.min(SECTION_TILES, MAP_W - x0);
  const h = Math.min(SECTION_TILES, MAP_H - y0);
  return { x0, y0, w, h };
}

// ---------------------------------------------------------------------------
// §2 The density ladder — families, successor rule (acceptance doc §C)
// ---------------------------------------------------------------------------

export type CapacityField =
  | 'residents'
  | 'jobs'
  | 'children'
  | 'served'
  | 'mw'
  | 'wasteCapacity'
  | 'processCapacity'
  | 'tourism'
  | 'capacity';

const CAPACITY_FIELD_ORDER: CapacityField[] = [
  'residents',
  'jobs',
  'children',
  'served',
  'mw',
  'wasteCapacity',
  'processCapacity',
  'tourism',
  'capacity',
];

/** First present capacity field on a spec, in the AC-7 order. Null for specs with no capacity field at all (civic, shops, factories, most parks — AC-11). */
/**
 * AC-20 protected classes are excluded HERE, at the root of the whole
 * derivation — capacityFieldOf is the single gate every downstream function
 * (familyKeyOf, the ladder, the audit's capacityByFamily bucketing, the
 * opportunity finder) reads through, so excluding a kind here excludes it
 * everywhere at once (GR#3). Without this a road spec's `capacity` field
 * (vehicles/tick, NOT a consolidation capacity at all) got silently treated
 * as one of AC-7's nine capacity fields and the ladder started proposing
 * "5 A-Roads -> 1 Motorway Junction" — exactly the road-graph collision
 * AC-20 exists to prevent (FEAT-1972079928 already owns that graph
 * autonomously). `landmark` is excluded too (AC-20's second protected class,
 * one-off set pieces).
 */
const CONSOLIDATION_EXEMPT_KINDS: ReadonlySet<ZoneKind> = new Set<ZoneKind>([
  ...CONNECT_EXEMPT_KINDS,
  'landmark',
]);

export function capacityFieldOf(sp: Spec): CapacityField | null {
  if (CONSOLIDATION_EXEMPT_KINDS.has(sp.kind)) return null;
  for (const field of CAPACITY_FIELD_ORDER) {
    const v = (sp as unknown as Record<string, unknown>)[field];
    if (typeof v === 'number' && v !== 0) return field;
  }
  return null;
}

/** Tier-0 (base, unscaled) capacity of a spec — capacityAtTier already falls back to residents/jobs; for the other capacity fields we read the field directly since capacityAtTier only special-cases residents/jobs today. */
export function capacityOf(sp: Spec): number {
  return buildingCapacityOf(sp, 0);
}

/**
 * Capacity of a SPECIFIC building instance at its current auto-scale tier.
 * `data.ts`'s own `capacityAtTier` only falls back to `residents ?? jobs` for
 * a spec with no `capacityTiers` array — every OTHER capacity field (mw,
 * served, wasteCapacity, processCapacity, tourism, capacity) is invisible to
 * it. Every consolidator call site needs the AC-7 field-aware base value for
 * those specs (e.g. pow_wind's `mw: 8` has no capacityTiers at all today),
 * so this wraps capacityAtTier and falls through to the field-aware read
 * capacityFieldOf/direct-field lookup provides for the untiered case. Tiered
 * specs (capacityTiers present) are unaffected — capacityAtTier already
 * handles those correctly and this defers to it unchanged.
 */
function buildingCapacityOf(sp: Spec, tier: number): number {
  if (sp.capacityTiers && sp.capacityTiers.length > 0) return capacityAtTier(sp, tier);
  const field = capacityFieldOf(sp);
  if (field == null) return 0;
  const v = (sp as unknown as Record<string, unknown>)[field];
  return typeof v === 'number' ? v : 0;
}

export function tilesOf(sp: Spec): number {
  return sp.w * sp.h;
}

export function tileDensityOf(sp: Spec): number {
  const tiles = tilesOf(sp);
  return tiles > 0 ? capacityOf(sp) / tiles : 0;
}

/** The `tag`/`stage` components are load-bearing (AC-7): wat_clean vs wat_waste, edu_nursery vs edu_primary must never share a family despite matching kind+capacityField. */
export function familyKeyOf(sp: Spec): string {
  return `${sp.kind}|${capacityFieldOf(sp) ?? ''}|${sp.tag ?? ''}|${sp.stage ?? ''}`;
}

/** PLACEHOLDER-balance (Aaron's R2/AC-8 rule 3): a successor must replace a GROUP, never a pair. */
export const CONSOLIDATOR_MIN_GROUP = 4;

/**
 * AC-8's successor rule, stated once, derived from data. Spec B is a
 * consolidation successor of spec A iff all five (data-derivable) rules
 * hold. Rule 6 (specUnlocked / remainingAllowance) is deliberately OMITTED
 * here — specUnlocked needs a SimState (fine, passed in) but
 * remainingAllowance (AC-28, maxPerCity) is a mutation-lane field that does
 * not exist on Spec yet; this discovery-only module reports what the DATA
 * supports, and unlock/cap gating is re-applied by the (separate) apply lane
 * before anything is actually built.
 */
/**
 * ASM-1497 (BA finding, acceptance doc §7): hea_eldercare's `served: 90` is
 * BEDS, not population, so despite matching kind+capacityField it must never
 * share a consolidation family with hea_clinic/hea_hospital/hea_teaching —
 * doing so would silently delete real health capacity behind a unit
 * mismatch. Excluded here by the explicit, commented exception the BA doc
 * recommended for inc1, pending the balance-pass fix (a distinct capacity
 * field for eldercare beds).
 */
const CONSOLIDATION_EXEMPT_SPEC_IDS: ReadonlySet<string> = new Set(['hea_eldercare']);

export function isConsolidationSuccessor(a: Spec, b: Spec): boolean {
  if (a.id === b.id) return false;
  if (CONSOLIDATION_EXEMPT_SPEC_IDS.has(a.id) || CONSOLIDATION_EXEMPT_SPEC_IDS.has(b.id)) return false;
  if (familyKeyOf(a) !== familyKeyOf(b)) return false;
  if (!canEnterSim(a) || !canEnterSim(b)) return false;
  const capA = capacityOf(a);
  const capB = capacityOf(b);
  if (capA <= 0 || capB <= 0) return false;
  if (capB < CONSOLIDATOR_MIN_GROUP * capA) return false;
  if (tileDensityOf(b) < tileDensityOf(a)) return false;
  const bPollutes = b.tag === ('pollution' as Tag);
  const aPollutes = a.tag === ('pollution' as Tag);
  if (bPollutes && !aPollutes) return false;
  return true;
}

/**
 * The group size for a given (A, B) successor pair — the number of
 * A-instances whose combined capacity B replaces. Derived, never chosen
 * (AC-8).
 *
 * FLOOR, not ceil (round-1 destructive REJECT, finding 1, 2026-09-04): the
 * group must be the LARGEST n such that n * capacityOf(A) <= capacityOf(B)
 * — the biggest group the successor can fully absorb — never the smallest n
 * such that n * capacityOf(A) >= capacityOf(B). Ceiling picks the latter,
 * which for any non-exact ratio makes the group's combined capacity EXCEED
 * the successor's, i.e. consolidating would DELETE real capacity — the
 * exact opposite of AC-8 rule 4 ("capacity never falls", isConsolidationSuccessor
 * above). Measured against the real catalogue: this hit 48 of 87 generated
 * rungs (55%) before the fix — e.g. pow_wind(8MW) -> pow_offshore(300MW) at
 * ceil(300/8)=38 turbines is a real -4MW loss for a real GBP133.8m spend;
 * the correct floor(300/8)=37 leaves a +4MW gain. isConsolidationSuccessor's
 * rule 3 (`capacityOf(B) >= CONSOLIDATOR_MIN_GROUP * capacityOf(A)`) already
 * guarantees `floor(capB/capA) >= CONSOLIDATOR_MIN_GROUP` for every rung
 * that reaches this function, so the AC-8-rule-3 "replace a group, not a
 * pair" contract holds under floor exactly as it did (incorrectly) under
 * ceil. See `attack-consolidator-inc1-round.test.mjs`'s table-driven proof
 * that the FULL generated ladder satisfies groupSize * capacityOf(A) <=
 * capacityOf(B) for every rung under this formula.
 */
export function groupSizeOf(a: Spec, b: Spec): number {
  const capA = capacityOf(a);
  if (capA <= 0) return Infinity;
  return Math.floor(capacityOf(b) / capA);
}

export interface LadderEntry {
  from: string;
  to: string;
  groupSize: number;
}

/**
 * AC-9: the whole ladder derived from SPECS, never hand-listed. Sorted
 * deterministically (from id asc, then to id asc) so the output is stable
 * regardless of object key iteration order in SPECS.
 */
export function consolidationLadder(): LadderEntry[] {
  const specs = Object.values(SPECS);
  const entries: LadderEntry[] = [];
  for (const a of specs) {
    if (capacityFieldOf(a) == null) continue;
    for (const b of specs) {
      if (capacityFieldOf(b) == null) continue;
      if (isConsolidationSuccessor(a, b)) {
        entries.push({ from: a.id, to: b.id, groupSize: groupSizeOf(a, b) });
      }
    }
  }
  entries.sort((x, y) => (x.from < y.from ? -1 : x.from > y.from ? 1 : x.to < y.to ? -1 : x.to > y.to ? 1 : 0));
  return entries;
}

// ---------------------------------------------------------------------------
// §3 Discovery + section audit (acceptance doc §B)
// ---------------------------------------------------------------------------

/** Kinds counted as "job-bearing" for the commute/adjacency facts — mirrors AC-13's commute term definition. */
const JOB_BEARING_KINDS: ReadonlySet<ZoneKind> = new Set(['office', 'industrial', 'commercial']);

export interface AdjacencyFacts {
  /** This section has >=1 residential building AND >=1 pollution-tagged building. */
  homesNearNoisyIndustry: boolean;
  /** This section has >=1 school building, and NEITHER this section nor any of its 8 neighbours has a park. */
  schoolWithoutNearbyPark: boolean;
  /** This section has >=1 residential building, and NEITHER this section nor any of its 8 neighbours has a job-bearing (office/industrial/commercial) building. */
  homesFarFromJobs: boolean;
}

/**
 * Aaron's mid-build ruling (2026-09-03, from his live city's Housing tab
 * reading "20,000 not on road network"): STRANDED capacity — residential
 * capacity that is built and paid for but contributes NOTHING because
 * computeIsOnline's activation gates hold it offline — is the single
 * highest-value finding this audit can report, ranked above density
 * consolidation. Reuses data.ts's OWN classification (computeFailedGates /
 * offlineResidentsByReason's bucketing convention) — never re-derives the
 * gate logic (GR#3 SSOT) — split into THREE causes because they need three
 * different remedies:
 *   - construction:   still building; self-resolves, no action needed.
 *   - roadAdjacentFail:  built, but no adjacent road tile at all — needs a
 *     literal new road tile reaching it (or relocation).
 *   - roadConnectedFail: adjacent to a road, but that road is not part of
 *     the connected network — needs a SPUR joining it to the connected
 *     graph (cheaper than roadAdjacentFail: no need to reach the building
 *     itself, only to bridge the gap to the existing connected network).
 */
export interface StrandedBreakdown {
  constructionCount: number;
  constructionCapacity: number;
  roadAdjacentFailCount: number;
  roadAdjacentFailCapacity: number;
  roadConnectedFailCount: number;
  roadConnectedFailCapacity: number;
}

function emptyStrandedBreakdown(): StrandedBreakdown {
  return {
    constructionCount: 0,
    constructionCapacity: 0,
    roadAdjacentFailCount: 0,
    roadAdjacentFailCapacity: 0,
    roadConnectedFailCount: 0,
    roadConnectedFailCapacity: 0,
  };
}

/** Total stranded (offline, non-construction) capacity in a breakdown — the actionable slice, excluding the self-resolving construction bucket. */
export function actionableStranded(b: StrandedBreakdown): number {
  return b.roadAdjacentFailCapacity + b.roadConnectedFailCapacity;
}

export interface SectionAudit {
  key: number;
  x0: number;
  y0: number;
  w: number;
  h: number;
  buildingIds: number[];
  countByKind: Partial<Record<ZoneKind, number>>;
  countBySpec: Record<string, number>;
  /** buildingIds bucketed by spec id, ascending — lets a caller (e.g. findOpportunities) pick "the N lowest-id buildings of spec X in this section" without an O(buildings) scan over s.buildings. */
  buildingIdsBySpec: Record<string, number[]>;
  capacityByFamily: Record<string, number>;
  tilesUsed: number;
  tilesFree: number;
  nuisanceCount: number;
  /** true iff this section has >=1 residential building. Hoisted per-section flag, used by the second (O(sections)) adjacency pass — never recomputed per building. */
  hasResidents: boolean;
  hasSchool: boolean;
  hasPark: boolean;
  hasJobs: boolean;
  /** true iff this section has a road/motorway-kind building whose origin tile sits in the CONNECTED road network — used by the reconnection-remedy estimate's ring search (never a nested O(buildings) lookup). */
  hasConnectedRoad: boolean;
  adjacency: AdjacencyFacts;
  stranded: StrandedBreakdown;
}

interface RawSectionAggregate {
  key: number;
  buildingIds: number[];
  countByKind: Partial<Record<ZoneKind, number>>;
  countBySpec: Record<string, number>;
  buildingIdsBySpec: Record<string, number[]>;
  capacityByFamily: Record<string, number>;
  tilesUsed: number;
  nuisanceCount: number;
  hasResidents: boolean;
  hasSchool: boolean;
  hasPark: boolean;
  hasJobs: boolean;
  hasConnectedRoad: boolean;
  stranded: StrandedBreakdown;
}

/**
 * AC-4: discovery is a single O(buildings) bucketing fold, once per pass,
 * memoised. Walks s.buildings EXACTLY ONCE, bucketing each building into
 * sectionKeyOf(b.x, b.y). The only O(buildings)-CLASS helpers touched inside
 * this loop are `isOnline` and `computeFailedGates` — both are per-BUILDING
 * checks (O(1) amortised: isOnline is memoOnState-backed, computeFailedGates
 * only runs for the offline minority, exactly BUG-645's own
 * residentialConstructionSummary/offlineResidentsByReason precedent) and
 * neither re-walks s.buildings itself, so calling them once per building
 * here keeps the whole fold O(buildings), not O(buildings^2) (BUG-642's
 * lesson). `connectedRoadTileSet(s)` is hoisted ONCE before the loop
 * (memoOnState-backed, keyed on `s.roadConnectivity`) and only queried
 * (`.has(...)`) inside it — the AC-37 "hoist the aggregate, read it
 * everywhere" idiom.
 *
 * Sections with zero buildings are absent from the returned map — the index
 * is O(occupied sections), not O(TOTAL_SECTIONS).
 */
export const sectionIndexOf: (s: SimState) => Map<number, SectionAudit> = memoOnState((s: SimState) => {
  const raw = new Map<number, RawSectionAggregate>();
  const connectedRoads = connectedRoadTileSet(s); // hoisted once (AC-37) — memoOnState-backed already.

  // Pass 1 — single O(buildings) fold, order-independent (a Map keyed by
  // sectionKeyOf, never iterated mid-fold with an early exit).
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue; // GR#16: an unknown spec id (corrupt/old save) is skipped, never thrown into arithmetic.
    const key = sectionKeyOf(b.x, b.y);
    let agg = raw.get(key);
    if (!agg) {
      agg = {
        key,
        buildingIds: [],
        countByKind: {},
        countBySpec: {},
        buildingIdsBySpec: {},
        capacityByFamily: {},
        tilesUsed: 0,
        nuisanceCount: 0,
        hasResidents: false,
        hasSchool: false,
        hasPark: false,
        hasJobs: false,
        hasConnectedRoad: false,
        stranded: emptyStrandedBreakdown(),
      };
      raw.set(key, agg);
    }
    agg.buildingIds.push(b.id);
    agg.countByKind[sp.kind] = (agg.countByKind[sp.kind] ?? 0) + 1;
    agg.countBySpec[sp.id] = (agg.countBySpec[sp.id] ?? 0) + 1;
    (agg.buildingIdsBySpec[sp.id] ??= []).push(b.id);
    agg.tilesUsed += tilesOf(sp);
    if (sp.tag === ('pollution' as Tag)) agg.nuisanceCount += 1;
    const field = capacityFieldOf(sp);
    if (field != null) {
      const fam = familyKeyOf(sp);
      const cap = buildingCapacityOf(sp, b.capacityTier ?? 0);
      agg.capacityByFamily[fam] = (agg.capacityByFamily[fam] ?? 0) + cap;
    }
    if (sp.kind === 'residential') agg.hasResidents = true;
    if (sp.kind === 'school') agg.hasSchool = true;
    if (sp.kind === 'park') agg.hasPark = true;
    if (JOB_BEARING_KINDS.has(sp.kind)) agg.hasJobs = true;
    if ((sp.kind === 'road' || sp.kind === 'motorway') && connectedRoads.has(`${b.x},${b.y}`)) {
      agg.hasConnectedRoad = true;
    }

    // Aaron's stranded-capacity ruling: reuse the SAME gate classification
    // offlineResidentsByReason uses (computeFailedGates), just kept as THREE
    // buckets instead of that selector's two, and attributed per SECTION
    // instead of city-wide. Residential only, matching the "20,000 dwellings"
    // reading and the existing offlineResidentsByReason/
    // residentialConstructionSummary convention (`sp.residents ?? 8`).
    if (sp.kind === 'residential' && !isOnline(s, b)) {
      const capacity = buildingCapacityOf(sp, b.capacityTier ?? 0) || sp.residents || 8;
      const gates = computeFailedGates(s, b);
      if (gates.some((g) => g.gate === 'construction')) {
        agg.stranded.constructionCount += 1;
        agg.stranded.constructionCapacity += capacity;
      } else if (gates.some((g) => g.gate === 'road-adjacent')) {
        agg.stranded.roadAdjacentFailCount += 1;
        agg.stranded.roadAdjacentFailCapacity += capacity;
      } else if (gates.some((g) => g.gate === 'road-connected')) {
        agg.stranded.roadConnectedFailCount += 1;
        agg.stranded.roadConnectedFailCapacity += capacity;
      }
      // else: offline for a reason outside the AC-5 gate set (e.g. connectivity
      // not yet computed on a bespoke state) — left unattributed rather than
      // guessed at (GR#16: never invent a classification the source doesn't report).
    }
  }

  // Deterministic key order for everything downstream — ascending numeric,
  // never object/Map iteration order relied upon for meaning.
  const keys = Array.from(raw.keys()).sort((a, c) => a - c);

  // Pass 2 — O(sections), NOT O(buildings): the 8-neighbour adjacency facts.
  // Neighbour lookup is a plain key arithmetic + Map.get, never a nested
  // building loop (AC-37's "nothing nested" rule).
  const result = new Map<number, SectionAudit>();
  for (const key of keys) {
    const agg = raw.get(key);
    if (!agg) continue; // unreachable (keys derived from raw's own keys), kept for GR#16 defensiveness.
    const sx = key % SECTIONS_X;
    const sy = Math.floor(key / SECTIONS_X);
    let neighbourHasPark = agg.hasPark;
    let neighbourHasJobs = agg.hasJobs;
    for (let dy = -1; dy <= 1; dy++) {
      for (let dx = -1; dx <= 1; dx++) {
        if (dx === 0 && dy === 0) continue;
        const nx = sx + dx;
        const ny = sy + dy;
        if (nx < 0 || ny < 0 || nx >= SECTIONS_X || ny >= SECTIONS_Y) continue;
        const nb = raw.get(ny * SECTIONS_X + nx);
        if (!nb) continue;
        if (nb.hasPark) neighbourHasPark = true;
        if (nb.hasJobs) neighbourHasJobs = true;
      }
    }
    const { x0, y0, w, h } = sectionOriginOf(key);
    const adjacency: AdjacencyFacts = {
      homesNearNoisyIndustry: agg.hasResidents && agg.nuisanceCount > 0,
      schoolWithoutNearbyPark: agg.hasSchool && !neighbourHasPark,
      homesFarFromJobs: agg.hasResidents && !neighbourHasJobs,
    };
    result.set(key, {
      key,
      x0,
      y0,
      w,
      h,
      buildingIds: agg.buildingIds.slice().sort((a, c) => a - c),
      countByKind: agg.countByKind,
      countBySpec: agg.countBySpec,
      buildingIdsBySpec: Object.fromEntries(
        Object.entries(agg.buildingIdsBySpec).map(([spec, ids]) => [spec, ids.slice().sort((a, c) => a - c)]),
      ),
      capacityByFamily: agg.capacityByFamily,
      tilesUsed: agg.tilesUsed,
      tilesFree: Math.max(0, w * h - agg.tilesUsed),
      nuisanceCount: agg.nuisanceCount,
      hasResidents: agg.hasResidents,
      hasConnectedRoad: agg.hasConnectedRoad,
      stranded: agg.stranded,
      hasSchool: agg.hasSchool,
      hasPark: agg.hasPark,
      hasJobs: agg.hasJobs,
      adjacency,
    });
  }
  return result;
});

// ---------------------------------------------------------------------------
// §4 The monthly rotation (Aaron's ruling 7, BOW FEAT-2326609761 2026-09-03)
// ---------------------------------------------------------------------------
//
// "one twelfth of the land per month, so each month consolidates a different
// twelfth of the map, and on month 12 the WHOLE tile is considered in its
// entirety as a single big-picture pass."
//
// Derived from sim state (the tick), never a clock (GR#21): TWELFTHS is a
// deterministic partition of section keys by `key % 12`, computed once and
// memoised per section-grid shape (it depends only on SECTIONS_X/Y, which
// are compile-time constants here, so a plain module-level lazy build is
// sufficient — no SimState dependency, hence no memoOnState wrapper needed).

const TWELFTHS_COUNT = 12;

let _twelfthsCache: number[][] | null = null;

/** The 12-way partition of every occupied-or-not section key, 0..TOTAL_SECTIONS-1, by key % 12. Every section belongs to EXACTLY one twelfth (a partition, not a sample) — proven exhaustively by the sector-partition test. */
function twelfths(): number[][] {
  if (_twelfthsCache) return _twelfthsCache;
  const buckets: number[][] = Array.from({ length: TWELFTHS_COUNT }, () => []);
  for (let key = 0; key < TOTAL_SECTIONS; key++) {
    buckets[key % TWELFTHS_COUNT].push(key);
  }
  _twelfthsCache = buckets;
  return buckets;
}

/** Which twelfth (0..11) the given tick's month falls into. Month 0 = twelfth 0, ..., month 11 = twelfth 11 = the whole-map month. Wraps every 12 months (`% 12`), so month 12 of year 2 is twelfth 11 again. */
export function twelfthIndexOf(tick: number): number {
  const month = Math.floor(tick / TICKS_PER_MONTH);
  return ((month % TWELFTHS_COUNT) + TWELFTHS_COUNT) % TWELFTHS_COUNT;
}

export interface MonthlyScope {
  /** 0..11 — which twelfth this tick's month primarily owns. */
  twelfth: number;
  /** True iff this is the 12th month in the cycle (twelfth === 11) — the whole-tile big-picture pass. */
  full: boolean;
  /** Section keys in scope for this month: the twelfth's own sections, PLUS every section when `full`. */
  sectionKeys: number[];
}

/**
 * AC-6/ruling 7: the fairness-rotation scope for a given tick. Months 0..10
 * scope to their own twelfth (a strict 1/12 slice of the section grid);
 * month 11 (the 12th month of the cycle) scopes to the WHOLE map — Aaron's
 * "on month 12 the whole tile is considered in its entirety". This is a
 * pure function of `tick` (via TICKS_PER_MONTH), never a clock.
 */
export function monthlyScopeOf(tick: number): MonthlyScope {
  const twelfth = twelfthIndexOf(tick);
  const full = twelfth === TWELFTHS_COUNT - 1;
  const sectionKeys = full
    ? Array.from({ length: TOTAL_SECTIONS }, (_, i) => i)
    : twelfths()[twelfth].slice();
  return { twelfth, full, sectionKeys };
}

// ---------------------------------------------------------------------------
// §5 Consolidation opportunities — named, sized, costed, NEVER applied.
// ---------------------------------------------------------------------------

export interface ConsolidationOpportunity {
  sectionKey: number;
  fromSpec: string;
  toSpec: string;
  /** How many instances of fromSpec this opportunity would consolidate. */
  groupCount: number;
  buildingIds: number[];
  /** placementCost(toSpec) — the build charge, using the same predicate the reducer would use (AC-22: never sp.cost directly). */
  buildCost: number;
  /** sum of placementCost(fromSpec) * CONSOLIDATOR_SCRAP_FRACTION over the group — the scrap that would be recovered. */
  scrapRecovered: number;
  netCost: number;
  /**
   * capacity gained (successor capacity - group's REAL combined capacity,
   * summed per building at its actual current tier). For a group of
   * untiered (tier-0) buildings this is structurally >= 0 under the
   * floor-based groupSizeOf (AC-8 rule 4). NEVER clamped: a group
   * containing an auto-scaled member (capacityTier > 0) can push this
   * negative, and that is reported HONESTLY, not hidden as 0 (round-1
   * destructive finding 1, 2026-09-04 — "can the numbers lie to Aaron").
   */
  capacityGain: number;
  /** buildings removed - 1 (the successor) — Aaron's "fewer, bigger, deliberate" building-count economy, reported for the panel even though the full weighted layoutScore (AC-13) is inc2 scope. */
  buildingCountReduction: number;
}

/** Aaron's R2 placeholder, mirrored here for read-only costing display — the authoritative constant lives with the (separate) apply/economics lane once it exists; this module only ever REPORTS a cost, never charges one. */
export const CONSOLIDATOR_SCRAP_FRACTION = 0.5;

/**
 * Read-only opportunity finder: for every section in scope, for every
 * (from, to) ladder rung, if the section holds >= groupSize instances of
 * `from`, record ONE opportunity consolidating the cheapest-to-find group of
 * that size (the lowest building ids, for determinism). This NEVER mutates
 * state, never charges money, and never places or removes a building — it
 * is purely descriptive, for Aaron to see what the consolidator WOULD act
 * on once the apply half lands (task brief: "named, sized, costed — never
 * acted upon").
 *
 * Deterministic ordering (GR#21): opportunities are returned sorted by
 * (capacityGain desc, sectionKey asc, fromSpec asc, toSpec asc) — a total
 * order with no remaining ties, so two identical states (or the same state
 * with `s.buildings` reordered) yield a byte-identical list.
 */
export function findOpportunities(s: SimState, sectionKeys: readonly number[]): ConsolidationOpportunity[] {
  const index = sectionIndexOf(s);
  const ladder = consolidationLadder();
  const opportunities: ConsolidationOpportunity[] = [];

  // Hoisted ONCE (AC-37: a single O(buildings) pass, never re-walked per
  // section/rung) — round-1 destructive finding 1's real fix needs each
  // candidate's ACTUAL current capacity (capacityAtTier via its
  // capacityTier), not the spec's flat tier-0 capacityOf, because an
  // auto-scaled group member's earned capacity is real and must not be
  // silently understated (AC-18's "must not quietly delete auto-scale
  // progress", the same principle applied here to the read-only report).
  const buildingById = new Map<number, SimState['buildings'][number]>();
  for (const b of s.buildings) buildingById.set(b.id, b);

  // Deterministic section order (caller may pass an unsorted twelfth bucket).
  const orderedKeys = sectionKeys.slice().sort((a, b) => a - b);

  for (const key of orderedKeys) {
    const audit = index.get(key);
    if (!audit) continue;
    for (const rung of ladder) {
      const countAvailable = audit.countBySpec[rung.from] ?? 0;
      if (countAvailable < rung.groupSize) continue;
      const fromSpec = SPECS[rung.from];
      const toSpec = SPECS[rung.to];
      if (!fromSpec || !toSpec) continue;

      // Deterministic pick: the lowest-id N buildings of `fromSpec` in this
      // section, read directly from the pre-bucketed index — no scan over
      // s.buildings here (AC-37: nothing O(buildings) inside a loop over
      // sections/rungs).
      const bucket = audit.buildingIdsBySpec[rung.from] ?? [];
      const candidateIds = bucket.slice(0, rung.groupSize);
      if (candidateIds.length < rung.groupSize) continue;

      const buildCost = placementCost(toSpec);
      const scrapRecovered = Math.round(placementCost(fromSpec) * CONSOLIDATOR_SCRAP_FRACTION) * rung.groupSize;

      // REAL group capacity: sum each candidate's ACTUAL capacity at its
      // current tier, not `capacityOf(fromSpec) * groupSize`'s flat
      // tier-0-for-everyone assumption.
      let groupCapacity = 0;
      for (const id of candidateIds) {
        const b = buildingById.get(id);
        groupCapacity += buildingCapacityOf(fromSpec, b?.capacityTier ?? 0);
      }
      // Round-1 destructive finding 1: NEVER clamp this to 0. With the
      // floor-based groupSizeOf above, a group of untiered (tier-0)
      // buildings structurally satisfies groupCapacity <= capacityOf(toSpec)
      // — but a group containing an AUTO-SCALED member (capacityTier > 0)
      // can genuinely exceed it, which is a REAL loss the panel must show,
      // not hide. Reporting the true (possibly negative) number is the
      // whole point of the round's finding: "can the numbers lie to Aaron".
      const capacityGain = capacityOf(toSpec) - groupCapacity;

      opportunities.push({
        sectionKey: key,
        fromSpec: rung.from,
        toSpec: rung.to,
        groupCount: rung.groupSize,
        buildingIds: candidateIds,
        buildCost,
        scrapRecovered,
        netCost: buildCost - scrapRecovered,
        capacityGain,
        buildingCountReduction: rung.groupSize - 1,
      });
    }
  }

  opportunities.sort((a, b) => {
    if (a.capacityGain !== b.capacityGain) return b.capacityGain - a.capacityGain;
    if (a.sectionKey !== b.sectionKey) return a.sectionKey - b.sectionKey;
    if (a.fromSpec !== b.fromSpec) return a.fromSpec < b.fromSpec ? -1 : 1;
    if (a.toSpec !== b.toSpec) return a.toSpec < b.toSpec ? -1 : 1;
    return 0;
  });
  return opportunities;
}

/** Convenience wrapper: opportunities for the CURRENT month's scope (ruling 7's rotation), read only. */
export function currentMonthOpportunities(s: SimState): ConsolidationOpportunity[] {
  const scope = monthlyScopeOf(s.tick);
  return findOpportunities(s, scope.sectionKeys);
}

// ---------------------------------------------------------------------------
// §6 Reconnection opportunities — stranded capacity, Aaron's ruling
// (BOW FEAT-2326609761, 2026-09-03, from his live city's Housing tab reading
// "20,000 not on road network"). Ranked ABOVE density-consolidation
// opportunities: recovering existing paid-for stock is cheaper than building
// new, and a city can be at 99.7% of ONLINE capacity while sitting on tens
// of thousands of stranded units it already paid for (BUG-645).
// ---------------------------------------------------------------------------

/** The cheapest 1x1 road tile spec, used to price a reconnection spur. Read via SPECS/placementCost, never a hand-typed number (GR#15). */
const CHEAPEST_ROAD_SPEC_ID = 'road';

export interface ReconnectionOpportunity {
  kind: 'reconnect';
  sectionKey: number;
  strandedCapacity: number;
  strandedBuildingCount: number;
  cause: 'road-adjacent' | 'road-connected' | 'mixed';
  /**
   * A LOWER BOUND on the spur length in section-grid steps to the nearest
   * section carrying a connected road tile — NOT a real pathfind, and NOT a
   * best-guess midpoint estimate either. It is a straight-line, obstacle-
   * blind Chebyshev distance over the SECTION grid; the real route (which
   * must go around buildings, water, terrain) can only be longer, never
   * shorter. A genuine route is `planConnector`'s job (roadConnect.ts, the
   * mutation-lane/FEAT-1972079928 territory this read-only module
   * deliberately does not duplicate).
   *
   * MEASURED FAILURE MODE (round-1 destructive finding 2, 2026-09-04): a
   * cluster whose only real route detours around a wall of buildings was
   * measured at 526 real (BFS, obstacle-aware) tiles against this field's
   * 16-tile floor — a 32.9x underestimate, with NO caveat surfaced to
   * Aaron. This is why every consumer of this field (the tab included)
   * MUST render it as "at least N sections / £X", never as a bare number —
   * see `estimatedReconnectCost`'s doc for the same rule on the cost.
   */
  approxSpurSections: number | null;
  /**
   * approxSpurSections * SECTION_TILES * placementCost(road) — a LOWER
   * BOUND on the true cost, not an estimate of it. The real reconnection
   * cost can be MANY TIMES higher when the direct route is obstacle-blocked
   * (round-1 measured 32.9x on a constructed worst case: 526 real tiles /
   * £6,312,000 vs this field's 16 tiles / £192,000). Every UI surface that
   * displays this value MUST frame it as "at least £X", never as a plain
   * cost figure that could be mistaken for a quote. null if no connected
   * road was found within the search radius (report honestly rather than
   * guess).
   */
  estimatedReconnectCost: number | null;
  /**
   * Comparison figure: the cost of instead demolishing-and-rebuilding every
   * stranded building elsewhere, at FULL placementCost each (a conservative
   * UPPER bound — it does not net off any bulldoze/scrap refund, so the real
   * relocate cost is <= this). Lets Aaron compare "fix the road" vs "move
   * the houses" at a glance.
   */
  relocateAllCostUpperBound: number;
}

/**
 * Ring search over the SECTION GRID (not tiles, not roadTileSetOf) for the
 * nearest section carrying a connected road tile, from `fromKey`. O(sections)
 * bounded by the search radius, never O(sections x roadTiles) — the AC-37
 * discipline applied to a read-only estimator, not just the audit itself.
 * Returns null if none is found within the map's own diagonal (i.e. truly
 * nowhere on the map has a connected road — an honest "cannot estimate").
 *
 * This is a LOWER BOUND on the real distance, not an estimate of it — see
 * `ReconnectionOpportunity.approxSpurSections`'s doc for the measured 32.9x
 * underestimate on an obstacle-blocked real route (round-1 destructive
 * finding 2). It deliberately does NOT attempt real pathfinding: that is
 * `planConnector`'s (roadConnect.ts) territory, owned by the mutation lane.
 */
function ringDistanceToConnectedRoad(
  index: Map<number, SectionAudit>,
  fromKey: number,
): number {
  const sx = fromKey % SECTIONS_X;
  const sy = Math.floor(fromKey / SECTIONS_X);
  const maxRadius = Math.max(SECTIONS_X, SECTIONS_Y);
  for (let radius = 0; radius <= maxRadius; radius++) {
    for (let dy = -radius; dy <= radius; dy++) {
      for (let dx = -radius; dx <= radius; dx++) {
        // Only the RING at exactly this radius (Chebyshev) — inner cells were
        // already checked at a smaller radius, so this never rechecks a cell.
        if (Math.max(Math.abs(dx), Math.abs(dy)) !== radius) continue;
        const nx = sx + dx;
        const ny = sy + dy;
        if (nx < 0 || ny < 0 || nx >= SECTIONS_X || ny >= SECTIONS_Y) continue;
        const nb = index.get(ny * SECTIONS_X + nx);
        if (nb?.hasConnectedRoad) return radius;
      }
    }
  }
  return -1; // not found anywhere on the map.
}

/**
 * AC-4-style single pass over the audited sections producing one
 * ReconnectionOpportunity per section carrying actionable stranded capacity
 * (excludes the self-resolving construction bucket). Deterministic ordering:
 * (strandedCapacity desc, sectionKey asc) — a total order.
 */
export function findReconnectionOpportunities(s: SimState, sectionKeys: readonly number[]): ReconnectionOpportunity[] {
  const index = sectionIndexOf(s);
  const roadCost = placementCost(SPECS[CHEAPEST_ROAD_SPEC_ID]);
  const orderedKeys = sectionKeys.slice().sort((a, b) => a - b);
  const out: ReconnectionOpportunity[] = [];

  for (const key of orderedKeys) {
    const audit = index.get(key);
    if (!audit) continue;
    const actionable = actionableStranded(audit.stranded);
    if (actionable <= 0) continue;

    const cause: ReconnectionOpportunity['cause'] =
      audit.stranded.roadAdjacentFailCapacity > 0 && audit.stranded.roadConnectedFailCapacity > 0
        ? 'mixed'
        : audit.stranded.roadAdjacentFailCapacity > 0
          ? 'road-adjacent'
          : 'road-connected';

    const radius = ringDistanceToConnectedRoad(index, key);
    const approxSpurSections = radius >= 0 ? radius : null;
    const estimatedReconnectCost =
      radius >= 0 ? Math.max(1, radius) * SECTION_TILES * roadCost : null;

    const strandedBuildingCount = audit.stranded.roadAdjacentFailCount + audit.stranded.roadConnectedFailCount;
    // Conservative upper bound: full rebuild cost of every stranded building's
    // spec, summed from the section's own capacityByFamily-adjacent counts —
    // approximated here as residential spec cost x count (residential is the
    // only kind this stranded breakdown tracks, per Aaron's ruling).
    const residentialSpecId = Object.keys(audit.buildingIdsBySpec).find((id) => SPECS[id]?.kind === 'residential');
    const perUnitCost = residentialSpecId ? placementCost(SPECS[residentialSpecId]) : 0;
    const relocateAllCostUpperBound = perUnitCost * strandedBuildingCount;

    out.push({
      kind: 'reconnect',
      sectionKey: key,
      strandedCapacity: actionable,
      strandedBuildingCount,
      cause,
      approxSpurSections,
      estimatedReconnectCost,
      relocateAllCostUpperBound,
    });
  }

  out.sort((a, b) => {
    if (a.strandedCapacity !== b.strandedCapacity) return b.strandedCapacity - a.strandedCapacity;
    return a.sectionKey - b.sectionKey;
  });
  return out;
}

export interface StrandedCapacityReport {
  /** Sum of actionable (non-construction) stranded residential capacity, city-wide. Cross-checked against offlineResidentsByReason(s).disconnected (the SSOT) in tests — must match exactly. */
  totalActionableCapacity: number;
  totalConstructionCapacity: number;
  clusterCount: number;
  /** Sum of every cluster's LOWER-BOUND estimatedReconnectCost — itself a lower bound, not a total budget. See ReconnectionOpportunity.estimatedReconnectCost's doc; render as "at least £X", never as a plain total. */
  totalEstimatedReconnectCost: number;
  clusters: ReconnectionOpportunity[];
}

/**
 * The city-wide stranded-capacity summary Aaron asked to see FIRST. Cross-
 * checks its own per-section sum against data.ts's offlineResidentsByReason
 * (the SSOT for the two-bucket citywide figure) — GR#3: this module must
 * never silently drift from the number the Housing tab already shows.
 */
export function strandedCapacityReport(s: SimState): StrandedCapacityReport {
  const index = sectionIndexOf(s);
  const allKeys = Array.from(index.keys());
  const clusters = findReconnectionOpportunities(s, allKeys);
  const totalActionableCapacity = clusters.reduce((sum, c) => sum + c.strandedCapacity, 0);
  const totalConstructionCapacity = Array.from(index.values()).reduce(
    (sum, a) => sum + a.stranded.constructionCapacity,
    0,
  );
  const totalEstimatedReconnectCost = clusters.reduce((sum, c) => sum + (c.estimatedReconnectCost ?? 0), 0);
  return {
    totalActionableCapacity,
    totalConstructionCapacity,
    clusterCount: clusters.length,
    totalEstimatedReconnectCost,
    clusters,
  };
}

export type TopOpportunity =
  | ({ rank: number } & ReconnectionOpportunity)
  | ({ rank: number } & ConsolidationOpportunity & { kind: 'consolidate' });

/**
 * The panel's single ranked list: EVERY reconnection opportunity first
 * (Aaron's ruling — recovering existing stock beats building new), THEN
 * density-consolidation opportunities, each group keeping its own
 * deterministic internal order. Read-only: this never applies anything.
 */
export function topOpportunities(s: SimState, sectionKeys: readonly number[], limit = 20): TopOpportunity[] {
  const reconnect = findReconnectionOpportunities(s, sectionKeys);
  const consolidate = findOpportunities(s, sectionKeys);
  const merged: TopOpportunity[] = [
    ...reconnect.map((o) => ({ ...o, kind: 'reconnect' as const, rank: 0 })),
    ...consolidate.map((o) => ({ ...o, kind: 'consolidate' as const, rank: 0 })),
  ];
  merged.forEach((o, i) => {
    o.rank = i + 1;
  });
  return merged.slice(0, limit);
}
