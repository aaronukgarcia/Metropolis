import type { Dims, SimState, ZoneKind } from './types.ts';
import { formatPower } from './utils.ts';
// FEAT-1972079877: isPlaceable defers real specs to the existing unlock gate.
// specUnlocked lives in engine.ts, which itself imports from data.ts — this is a
// function-only (call-time) cyclic import: neither module uses the other at
// module-eval time, so ESM live bindings resolve it safely.
import { specUnlocked } from './engine.ts';
// FEAT-1972079902 rail-inc1: road/m20 line-usage reuses road inc2's per-building
// traffic weight + city activity ramp (GR#3 SSOT) — call-time (cyclic-safe) imports,
// same pattern as specUnlocked above. Neither is used at module-eval time.
import { feederTrafficWeight, trafficActivity } from './engine.ts';

export const MAP_W = 440;
export const MAP_H = 260;

export const ROW_BAND = 10;

export function yLabel(y: number): string {
  return String.fromCharCode(65 + Math.max(0, Math.min(Math.floor(y / ROW_BAND)), 25));
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
   * FEAT-1972079877 placeholder catalogue: true marks a planned-but-unbuilt type
   * shown GREYED-OUT / "coming soon" in the build catalogue as a roadmap preview.
   * A placeholder is NEVER placeable (see isPlaceable) and carries zero sim stats
   * (cost/upkeep 0, no served/jobs/mw/residents), so it can never enter the running
   * sim. Real specs never set this flag.
   */
  placeholder?: boolean;
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

export function pipeTierOf(s: SimState, id: number): number {
  return s.pipeTier[id] ?? 0;
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

export function constructionTicks(sp: Spec): number {
  return Math.max(3, Math.round(sp.cost / 1500));
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
  if (score >= 24) return 5;
  if (score >= 14) return 4;
  if (score >= 7) return 3;
  if (score >= 3) return 2;
  return 1;
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

export function isOnline(s: SimState, b: SimState['buildings'][number]): boolean {
  if (b.builtTick == null) return true;
  const sp = SPECS[b.spec];
  // G1 (construction time) — unchanged.
  if (s.tick - b.builtTick < constructionTicks(sp)) return false;
  // FEAT-1972079891 inc1 — ROAD ACTIVATION GATES (G2 road-adjacent, G3 road-connected).
  // A non-infrastructure building only operates if it sits beside a road tile that
  // reaches the connected road network (map edges + trunk m20/hs1/rail/stations).
  // Infrastructure (category 'network' — road/motorway/rail/station/pylon) IS the
  // network, so it is exempt. Gates are pure functions of SimState (GR#21): same
  // state → same online set; no Date/Math.random.
  //
  // DD4 GRACE (migration rule): a building carrying `graceTick` keeps its prior
  // online status while `s.tick < graceTick`. On save-load, pre-existing buildings
  // are stamped with a graceTick (see applyActivationGrace / restoreFromSavepoint)
  // so loading a legacy save does NOT instant-blackout unconnected buildings —
  // they get GRACE_TICKS ticks to be connected before the gate bites.
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
    if (b.graceTick == null || s.tick >= b.graceTick) {
      if (!isRoadAdjacent(s, b)) return false;
      if (!isRoadConnected(s, b)) return false;
    }
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

/** Grace window (ticks) granted to pre-existing buildings on save-load (DD4
 *  Option A). PLACEHOLDER-balance (directional only, Aaron's pass). */
export const GRACE_TICKS = 30;

// Per-state memo of the drivable-road tile set, keyed on the buildings array
// reference (immutable per tick), so isOnline's road gates stay ~O(footprint)
// instead of O(buildings) each. Pure: a function of buildings only.
const roadTileSetCache = new WeakMap<object, Set<string>>();
function roadTileSetOf(s: SimState): Set<string> {
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
 */
export function computeRoadConnectivity(s: SimState): { connectedRoadTiles: string[] } {
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
  while (queue.length > 0) {
    const k = queue.shift()!;
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
  for (let dx = 0; dx < sp.w; dx++)
    for (let dy = 0; dy < sp.h; dy++) {
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
  for (let dx = 0; dx < sp.w; dx++)
    for (let dy = 0; dy < sp.h; dy++) {
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
  if (b.graceTick != null && s.tick < b.graceTick) return out; // within grace — gates skipped.
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

export function waterCaps(s: SimState): { clean: number; waste: number } {
  let clean = 0;
  let waste = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind !== 'water') continue;
    const eff = plantEffServed(s, b);
    if (sp.tag === 'clean') clean += eff;
    if (sp.tag === 'waste') waste += eff;
  }
  return { clean, waste };
}

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
      let places = 0;
      for (const o of s.buildings) {
        const os = SPECS[o.spec];
        if (os?.children) places += os.children;
      }
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
      // Aggregate health capacity from GP + hospital
      const gp = sumBy(s, (sp) => sp.id === 'hea_clinic', (sp) => sp.served ?? 0);
      const hosp = sumBy(s, (sp) => sp.id === 'hea_hospital' || sp.id === 'hea_teaching', (sp) => sp.served ?? 0);
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
      const cap = sumBy(s, (sp) => sp.kind === 'police', (sp) => sp.served ?? 0);
      if (cap <= 0) return null;
      return {
        ratio: ratio(s.population, cap),
        basis: 'citywide police coverage',
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
    case 'fire':
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
 * Placement gate (FEAT-1972079877): the SINGLE predicate for "can the player place
 * this spec right now" in the UI. A placeholder is NEVER placeable — regardless of
 * city level, unlock, or the god-mode unlockedAll flag (via canEnterSim). Any real
 * (non-placeholder) spec defers to the existing unlock gate (specUnlocked), so
 * real-spec behaviour is unchanged.
 */
export function isPlaceable(s: SimState, sp: Spec): boolean {
  if (!canEnterSim(sp)) return false;
  return specUnlocked(s, sp);
}

export const SPECS: Record<string, Spec> = {
  m20: P('m20', 'motorway', 'M20 Motorway', '', 1, 1, 0, 0, '#1d5fa8', 'network', 99, { roadTier: 5, capacity: 2500 }),
  rail: P('rail', 'rail', 'Rail Line', '', 1, 1, 0, 0, '#8a6d3b', 'network', 99),
  station_sanderling: P('station_sanderling', 'station', 'Sanderling Station', '', 1, 1, 0, 15, '#d0a83c', 'network', 99),
  station_ashford: P('station_ashford', 'station', 'Ashford International', 'HS1 international gateway · 60,000 served · x3 commuter weight', 4, 2, 80000, 220, '#e0559f', 'network', 5, { served: 60000 }),
  hs1: P('hs1', 'rail', 'HS1 High-Speed Line', '', 1, 1, 0, 0, '#c2477e', 'network', 99),
  pylon: P('pylon', 'pylon', 'HV Pylon', '', 1, 1, 0, 5, '#9aa4ae', 'network', 99),

  road: P('road', 'road', 'Road', '', 1, 1, 40, 3, '#4a525c', 'network', 1, { roadTier: 1, capacity: 100 }),

  res_hut: P('res_hut', 'residential', 'Small Holding', '8 residents', 1, 1, 220, 1, '#4c9aff', 'zones', 1, { residents: 8 }),
  res_block: P('res_block', 'residential', 'Estate Block', '60 residents', 2, 2, 1600, 6, '#4c9aff', 'zones', 2, { residents: 60 }),

  com_shop: P('com_shop', 'commercial', 'Corner Shop', 'Local trade', 1, 1, 320, 2, '#e3b341', 'zones', 1),
  com_retail: P('com_retail', 'commercial', 'Retail Park', 'Shopping quarter', 3, 2, 4200, 18, '#e3b341', 'zones', 2),

  farm_wheat: P('farm_wheat', 'industrial', 'Wheat Farm', 'Arable · golden crop', 2, 2, 800, 4, '#d9b13b', 'zones', 1),
  farm_cattle: P('farm_cattle', 'industrial', 'Cattle Pasture', 'Dairy herd', 3, 3, 1400, 6, '#7da24f', 'zones', 1),
  farm_orchard: P('farm_orchard', 'industrial', 'Orchard', 'Fruit · blossom crop', 2, 2, 1000, 5, '#97c15c', 'zones', 1),
  ind_factory: P('ind_factory', 'industrial', 'Factory', 'Goods + freight jobs', 2, 2, 2400, 14, '#a371f7', 'zones', 2, { tag: 'pollution' }),

  park: P('park', 'park', 'Park', 'Green space', 1, 1, 150, 10, '#3fb950', 'zones', 1),

  pow_wind: P('pow_wind', 'power', 'Wind Turbine', '8 MW · clean', 1, 1, 1400, 8, '#7fb2e5', 'services', 2, { mw: 8 }),
  pow_coal: P('pow_coal', 'power', 'Coal Plant', '80 MW · polluting', 2, 2, 6500, 90, '#f0883e', 'services', 3, { mw: 80, tag: 'pollution' }),
  // FEAT-1972079901 realistic power costs: nuclear is the priciest generator to
  // BUILD (Aaron's ask — nuclear VERY expensive up front). Capex raised 150k→560k
  // (~£500/MW, above renewables/gas per-MW) and opex 600→1400. `mw`/footprint/tag
  // UNCHANGED — cost-only retune. ⚠ PLACEHOLDER-balance — Aaron's row-by-row pass.
  pow_nuke: P('pow_nuke', 'power', 'Nuclear Plant', 'Twin AGR · 1,120 MW · Dungeness-scale', 13, 13, 560000, 1400, '#e05d38', 'services', 5, { mw: 1120, tag: 'pollution' }),

  wat_clean: P('wat_clean', 'water', 'Water Works', 'Clean water for 20,000', 2, 2, 2600, 38, '#39c5cf', 'services', 3, { tag: 'clean', served: 20000 }),
  wat_waste: P('wat_waste', 'water', 'Waste-Water Plant', 'Treats sewage for 20,000', 2, 2, 3400, 44, '#6b8f71', 'services', 3, { tag: 'waste', served: 20000 }),

  hea_clinic: P('hea_clinic', 'health', 'Clinic', 'GPs for 5,000', 1, 1, 1800, 26, '#ff7b72', 'services', 2, { served: 5000 }),
  hea_hospital: P('hea_hospital', 'health', 'General Hospital', 'Serves 40,000', 2, 2, 16000, 210, '#d95f57', 'services', 4, { served: 40000 }),
  pol_station: P('pol_station', 'police', 'Police Station', 'Covers 10,000', 2, 1, 2600, 34, '#6e7bd9', 'services', 3, { served: 10000 }),

  edu_nursery: P('edu_nursery', 'school', 'Kindergarten', '30 places · ages 0–4', 1, 1, 1200, 22, '#ffd166', 'services', 2, { children: 30, stage: 'nursery' }),
  edu_primary: P('edu_primary', 'school', 'Primary School', '300 places · ages 5–11', 2, 2, 5200, 70, '#f2c14e', 'services', 3, { children: 300, stage: 'primary' }),
  edu_city: P('edu_city', 'school', 'City School', '2,000 places · ages 5–15', 3, 2, 32000, 320, '#e3a92f', 'services', 4, { children: 2000, stage: 'city' }),
  col_sixth: P('col_sixth', 'school', 'College', '1,500 places · ages 16–19', 2, 2, 18000, 190, '#b58fd8', 'services', 4, { children: 1500, stage: 'tertiary' }),
  uni: P('uni', 'school', 'University', '6,000 students', 3, 3, 75000, 520, '#a371f7', 'services', 5, { children: 6000, stage: 'tertiary' }),

  off_suite: P('off_suite', 'office', 'Office Suite', '25 office jobs', 1, 1, 900, 5, '#43aa8b', 'zones', 2, { jobs: 25 }),
  off_tower: P('off_tower', 'office', 'Office Tower', '300 office jobs', 2, 3, 22000, 120, '#43aa8b', 'zones', 4, { jobs: 300 }),

  mine_quarry: P('mine_quarry', 'mine', 'Quarry', 'Materials + freight jobs', 2, 2, 3200, 20, '#b08d55', 'zones', 3, { tag: 'pollution', jobs: 30 }),
  mine_deep: P('mine_deep', 'mine', 'Deep Mine', 'Heavy freight output', 3, 3, 15000, 80, '#9c6f3f', 'zones', 5, { tag: 'pollution', jobs: 90 }),

  land_stadium: P('land_stadium', 'landmark', 'Regional Stadium', 'Tourism magnet + approval', 3, 2, 24000, 260, '#d0a83c', 'services', 5, { tourism: 60 }),
  land_airport: P('land_airport', 'landmark', 'International Airport', 'Heathrow-scale · 1,227 ha · twin 3.9 km runways', 70, 70, 450000, 3000, '#5eb3d6', 'services', 6, { tourism: 140 }),
  land_harbour: P('land_harbour', 'landmark', 'Deep-Water Harbour', 'Freight income x1.4', 3, 3, 38000, 300, '#5e8bb0', 'services', 7, {}),

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
  bus_stop: P('bus_stop', 'transport', 'Bus Stop', 'Local hopper services', 1, 1, 300, 4, '#5ea0c8', 'services', 2),
  bus_depot: P('bus_depot', 'transport', 'Bus Depot', 'Runs 20 local routes', 2, 2, 4500, 40, '#5ea0c8', 'services', 4, { jobs: 20 }),
  car_park: P('car_park', 'transport', 'Multi-storey Car Park', 'Park & ride commuters', 2, 2, 6000, 30, '#7f93a8', 'services', 5),
  bus_station: P('bus_station', 'transport', 'Bus Station', 'Regional coach interchange', 2, 2, 9000, 70, '#5ea0c8', 'services', 6, { served: 12000 }),
  tram_depot: P('tram_depot', 'transport', 'Tram Depot', 'Street tram network hub', 2, 2, 14000, 110, '#4d8fb8', 'services', 8, { jobs: 35 }),
  ferry_pier: P('ferry_pier', 'transport', 'Ferry Pier', 'Cross-channel foot ferry', 1, 2, 11000, 90, '#4a9dae', 'services', 9, { tourism: 15 }),
  metro_station: P('metro_station', 'transport', 'Metro Station', 'Underground rapid transit', 2, 2, 26000, 180, '#3d7ea6', 'services', 12, { served: 30000 }),
  grand_terminus: P('grand_terminus', 'transport', 'Grand Terminus', 'Victorian rail cathedral', 3, 2, 60000, 320, '#d0a83c', 'services', 14, { served: 80000, jobs: 60 }),

  // ---- Housing tiers ----
  res_terrace: P('res_terrace', 'residential', 'Terrace Row', '30 residents · Victorian brick', 2, 1, 900, 3, '#4c9aff', 'zones', 3, { residents: 30 }),
  res_lowrise: P('res_lowrise', 'residential', 'Low-rise Flats', '120 residents', 2, 2, 3200, 10, '#4c9aff', 'zones', 4, { residents: 120 }),
  res_midrise: P('res_midrise', 'residential', 'Mid-rise Flats', '280 residents', 2, 2, 7800, 22, '#3d84e6', 'zones', 6, { residents: 280 }),
  res_highrise: P('res_highrise', 'residential', 'High-rise Tower', '600 residents', 2, 2, 21000, 60, '#3d84e6', 'zones', 9, { residents: 600 }),
  res_penthouse: P('res_penthouse', 'residential', 'Penthouse Tower', '350 wealthy residents', 2, 2, 45000, 90, '#6ab0ff', 'zones', 13, { residents: 350 }),

  // ---- Retail tiers ----
  com_market: P('com_market', 'commercial', 'Market Hall', 'Covered traders market', 2, 2, 2200, 10, '#e3b341', 'zones', 3, { jobs: 25 }),
  com_super: P('com_super', 'commercial', 'Supermarket', 'Weekly shop anchor', 2, 2, 5200, 24, '#e3b341', 'zones', 4, { jobs: 40 }),
  com_mall: P('com_mall', 'commercial', 'Shopping Mall', 'Regional retail destination', 3, 3, 30000, 160, '#d9a52e', 'zones', 8, { jobs: 220 }),

  // ---- Industry tiers ----
  ind_light: P('ind_light', 'industrial', 'Light Industrial Units', 'Workshops + trades', 2, 2, 1800, 10, '#a371f7', 'zones', 3, { jobs: 24 }),
  ind_warehouse: P('ind_warehouse', 'industrial', 'Warehouse', 'Storage + distribution', 2, 2, 3600, 16, '#9a6ee0', 'zones', 5, { jobs: 18 }),
  ind_heavy: P('ind_heavy', 'industrial', 'Heavy Industry Estate', 'Big plant · heavy freight', 3, 3, 16000, 90, '#8957d9', 'zones', 7, { tag: 'pollution', jobs: 110 }),
  ind_cement: P('ind_cement', 'industrial', 'Cement Works', 'Construction materials', 2, 2, 12000, 70, '#8957d9', 'zones', 9, { tag: 'pollution', jobs: 45 }),
  ind_logistics: P('ind_logistics', 'industrial', 'Automated Logistics Hub', 'Robotic freight sorting', 3, 3, 48000, 210, '#b58fd8', 'zones', 15, { jobs: 60 }),

  // ---- Offices ----
  off_data: P('off_data', 'office', 'Data Centre', '90 tech jobs · heavy power draw', 2, 2, 34000, 240, '#2f8f74', 'zones', 12, { jobs: 90 }),

  // ---- Parks tiers ----
  park_playground: P('park_playground', 'park', 'Playground', 'Swings + climbing frame', 1, 1, 400, 6, '#3fb950', 'zones', 2),
  park_town: P('park_town', 'park', 'Town Park', 'Bandstand + boating lake', 2, 2, 2400, 30, '#3fb950', 'zones', 4),
  park_botanical: P('park_botanical', 'park', 'Botanical Garden', 'Glasshouses + collections', 2, 2, 9000, 80, '#2f9e44', 'zones', 8, { tourism: 20 }),
  park_nature: P('park_nature', 'park', 'Nature Reserve', 'Wetland + wildlife', 3, 3, 6000, 40, '#2f9e44', 'zones', 12),

  // ---- Leisure ----
  lei_leisure: P('lei_leisure', 'leisure', 'Leisure Centre', 'Pool + courts for 8,000', 2, 2, 7000, 85, '#e07be0', 'services', 4, { served: 8000 }),
  lei_cinema: P('lei_cinema', 'leisure', 'Cinema', 'Eight-screen multiplex', 2, 1, 5500, 45, '#e07be0', 'services', 5, { tourism: 10 }),
  lei_theatre: P('lei_theatre', 'leisure', 'Theatre', 'Rep company + touring shows', 2, 2, 12000, 95, '#c95fc9', 'services', 7, { tourism: 18 }),
  lei_museum: P('lei_museum', 'leisure', 'Museum', 'County collection', 2, 2, 15000, 110, '#c95fc9', 'services', 9, { tourism: 25 }),
  lei_arena: P('lei_arena', 'leisure', 'Arena', '12,000-seat events bowl', 3, 3, 55000, 380, '#b34fb3', 'services', 11, { tourism: 70 }),
  lei_themepark: P('lei_themepark', 'leisure', 'Theme Park', 'Coasters + day-trippers', 4, 4, 120000, 700, '#b34fb3', 'services', 16, { tourism: 160 }),

  // ---- Power additions ----
  pow_substation: P('pow_substation', 'power', 'Substation', 'Grid step-down node', 1, 1, 1200, 12, '#9aa4ae', 'services', 3),
  pow_solar: P('pow_solar', 'power', 'Solar Farm', '25 MW · clean', 3, 3, 9000, 30, '#f6c744', 'services', 6, { mw: 25 }),
  pow_windfarm: P('pow_windfarm', 'power', 'Onshore Wind Farm', '60 MW · clean', 3, 3, 18000, 60, '#7fb2e5', 'services', 7, { mw: 60 }),
  pow_ccgt: P('pow_ccgt', 'power', 'CCGT Gas Plant', '420 MW · fast response', 3, 3, 42000, 260, '#f0883e', 'services', 8, { mw: 420, tag: 'pollution' }),
  pow_offshore: P('pow_offshore', 'power', 'Offshore Wind Array', '300 MW · clean', 3, 3, 90000, 240, '#5b8fc9', 'services', 12, { mw: 300 }),
  // FEAT-1972079901 realistic power costs: experimental mega-plant, dearest per-MW
  // capex of all generators (400k→520k, ~£650/MW; opex 900→1500). `mw` UNCHANGED.
  pow_fusion: P('pow_fusion', 'power', 'Fusion Pilot Plant', '800 MW · experimental', 4, 4, 520000, 1500, '#ff9f43', 'services', 19, { mw: 800 }),

  // FEAT-1972079901 — FIVE GORGES DAM (GRADUATED from a roadmap placeholder to a
  // real, placeable mega-hydro GENERATOR). A single huge hydroelectric station:
  // 5,000 MW dwarfs the 1,120 MW Nuclear Plant (~4.5×). It is a power generator, so
  // its `mw` correctly adds to powerStats.cap like any plant. Deliberately carries
  // NO water `tag`/`served` and NO `residents` — a power spec only, so it can never
  // leak clean/waste-water or residential capacity (the waste_depot/estate lesson).
  // Hydro is clean, so (like pow_wind/pow_solar) it takes no `pollution` tag.
  // Huge 8×8 footprint + late unlock (16). ⚠ every figure PLACEHOLDER-balance —
  // directional only, pending Aaron's row-by-row pass.
  pow_hydro: P('pow_hydro', 'power', 'Five Gorges Dam', 'Mega hydroelectric dam · 5,000 MW · dwarfs a nuclear plant', 8, 8, 600000, 900, '#5b8fc9', 'services', 16, { mw: 5000 }),

  // ---- Water & waste additions ----
  wat_tower: P('wat_tower', 'water', 'Water Tower', 'Pressure head for 4,000', 1, 1, 1500, 14, '#39c5cf', 'services', 2, { tag: 'clean', served: 4000 }),
  wat_reservoir: P('wat_reservoir', 'water', 'Reservoir', 'Valley dam · serves 60,000', 4, 4, 45000, 150, '#2ba7b1', 'services', 9, { tag: 'clean', served: 60000 }),
  wat_sewage_regional: P('wat_sewage_regional', 'water', 'Regional Sewage Works', 'Treats waste for 60,000', 3, 3, 38000, 170, '#6b8f71', 'services', 11, { tag: 'waste', served: 60000 }),

  // FEAT-1972079906 inc1: the Refuse Depot GRADUATES from a "coming soon" roadmap
  // placeholder to a real, placeable collection depot — runs the city's rounds.
  // `wasteCapacity` (tonnes/tick collected) drives collectionCoverageOf(); it has
  // NO water tag, so it never counts as clean/waste-water capacity. ⚠ cost /
  // upkeep / wasteCapacity are PLACEHOLDER-balance — Aaron's row-by-row pass.
  waste_depot: P('waste_depot', 'water', 'Refuse Depot', 'Collects refuse on city rounds · 50 t/tick', 2, 2, 3000, 40, '#6b8f71', 'services', 4, { wasteCapacity: 50 }),

  // ---- Education additions ----
  edu_tech: P('edu_tech', 'school', 'Technical College', '2,200 places · trades + T-levels', 2, 2, 24000, 210, '#b58fd8', 'services', 6, { children: 2200, stage: 'tertiary' }),

  // ---- Health additions ----
  hea_ambulance: P('hea_ambulance', 'health', 'Ambulance Station', 'Six-crew emergency cover', 1, 1, 3800, 55, '#ff7b72', 'services', 5, { served: 15000 }),
  hea_eldercare: P('hea_eldercare', 'health', 'Elder-care Home', '90 assisted-living places', 2, 2, 8500, 95, '#d95f57', 'services', 7, { served: 90 }),
  hea_teaching: P('hea_teaching', 'health', 'Teaching Hospital', 'Serves 120,000 + trains doctors', 3, 3, 85000, 650, '#c24f47', 'services', 10, { served: 120000 }),

  // ---- Police & justice ----
  pol_hq: P('pol_hq', 'police', 'Divisional HQ', 'Commands 60,000 coverage', 2, 2, 15000, 160, '#6e7bd9', 'services', 9, { served: 60000 }),
  civ_courthouse: P('civ_courthouse', 'civic', 'Courthouse', 'Magistrates + crown courts', 2, 2, 12000, 130, '#8a94a8', 'services', 8),
  civ_prison: P('civ_prison', 'civic', 'Prison', 'Category B · 800 places', 3, 2, 26000, 240, '#707a8c', 'services', 10),
  // FEAT-1972079870 — the ADX supermax.
  civ_adx: P('civ_adx', 'civic', 'ADX Supermax', 'Maximum-security prison · escape-proof', 3, 3, 90000, 520, '#565e6e', 'services', 17),

  // ---- Fire & rescue ----
  fire_post: P('fire_post', 'fire', 'Volunteer Fire Post', 'Retained crew · covers 4,000', 1, 1, 1000, 16, '#f65b56', 'services', 2, { served: 4000 }),
  fire_station: P('fire_station', 'fire', 'Fire Station', 'Two pumps · covers 20,000', 2, 1, 4800, 70, '#f65b56', 'services', 4, { served: 20000 }),
  fire_hq: P('fire_hq', 'fire', 'Regional Fire HQ', 'Command + specialist appliances', 2, 2, 18000, 180, '#d94a45', 'services', 11, { served: 80000 }),

  // ---- Civic ----
  civ_library: P('civ_library', 'civic', 'Library', 'Lending + study space', 1, 1, 3000, 40, '#8a94a8', 'services', 5),
  civ_townhall: P('civ_townhall', 'civic', 'Town Hall', 'Local governance seat', 2, 2, 9000, 90, '#8a94a8', 'services', 6),
  civ_cityhall: P('civ_cityhall', 'civic', 'City Hall', 'Metropolitan administration', 2, 2, 30000, 220, '#707a8c', 'services', 12),

  // ---- Landmark additions ----
  land_cathedral: P('land_cathedral', 'landmark', 'Cathedral', 'Gothic spire · pilgrimage draw', 2, 2, 40000, 150, '#d0a83c', 'services', 11, { tourism: 45 }),
  land_eye: P('land_eye', 'landmark', 'The Folkestone Eye', 'Coastal observation wheel', 1, 1, 28000, 130, '#5eb3d6', 'services', 13, { tourism: 55 }),
  land_tunnel: P('land_tunnel', 'landmark', 'Channel Tunnel Portal', 'Continental rail gateway', 3, 3, 250000, 1200, '#c2477e', 'services', 18, { tourism: 80 }),
  land_space: P('land_space', 'landmark', 'Space Launch Complex', 'Kent spaceport · mega-project', 5, 5, 600000, 2500, '#ff9f43', 'services', 20, { tourism: 200 }),
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
  rd_avenue: P('rd_avenue', 'road', 'Avenue', 'Tree-lined urban avenue · tier 2', 1, 1, 90, 6, '#4a525c', 'network', 3, { roadTier: 2, capacity: 250 }),
  rd_aroad: P('rd_aroad', 'road', 'A-Road', 'Arterial trunk road · tier 3', 1, 1, 180, 10, '#454c56', 'network', 4, { roadTier: 3, capacity: 500 }),
  rd_dual: P('rd_dual', 'road', 'Dual Carriageway', 'High-capacity dual road · tier 4', 1, 1, 320, 16, '#3f4650', 'network', 5, { roadTier: 4, capacity: 1000 }),
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
  res_estate: P('res_estate', 'residential', 'Housing Estate', 'Master-planned housing estate · ≈ 12 low-rise blocks', 5, 5, 45000, 130, '#4c9aff', 'zones', 10, { residents: 1500 }),
  off_businesspark: P('off_businesspark', 'office', 'Business Park', 'Landscaped out-of-town office park · ≈ 4 towers', 5, 5, 85000, 420, '#43aa8b', 'zones', 12, { jobs: 1200 }),
  ind_estate: P('ind_estate', 'industrial', 'Industrial Estate', 'Heavy industrial estate · ≈ 18 factories · ICI-Wilton scale', 6, 6, 180000, 900, '#a371f7', 'zones', 11, { tag: 'pollution', jobs: 2000 }),

  // ---- Retail ----
  // FEAT-1972079900 inc1 — the RETAIL estate (out-of-town shopping / retail park).
  // Graduated from a placeholder into a real, placeable estate-scale retail object
  // carrying the AGGREGATE retail jobs of an out-of-town superstore. Same DATA-only
  // treatment as the other estates above (no mw, no water tag). PLACEHOLDER-balance.
  com_hypermarket: P('com_hypermarket', 'commercial', 'Hypermarket', 'Out-of-town retail estate · ≈ 20 shops', 5, 5, 90000, 480, '#e3b341', 'zones', 10, { jobs: 800 }),
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
  waste_landfill: P('waste_landfill', 'water', 'Landfill', 'Buries residual refuse · cheap, finite · 300 t/tick', 3, 3, 5000, 30, '#5f7f66', 'services', 5, { processCapacity: 300 }),
  waste_incinerator: P('waste_incinerator', 'water', 'Energy-from-Waste', 'Burns residual for grid power · 60 t/tick', 3, 3, 42000, 180, '#6b8f71', 'services', 9, { processCapacity: 60 }),
  waste_recycling: P('waste_recycling', 'water', 'Recycling Centre', 'MRF recovers materials for sale · 40 t/tick', 2, 2, 8000, 70, '#5f9e6a', 'services', 6, { processCapacity: 40 }),
  waste_compost: P('waste_compost', 'water', 'Composting Site', 'Turns organics into compost · 30 t/tick', 2, 2, 3500, 30, '#6b9e6b', 'services', 5, { processCapacity: 30 }),

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
  // ═══════════════ end FEAT-1972079877 roadmap placeholders ═══════════════
};

for (const [id, d] of Object.entries(DIMS)) {
  const sp = SPECS[id];
  if (sp) sp.dims = d;
}

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
  { title: 'Housing', items: ['res_hut', 'res_block', 'res_terrace', 'res_lowrise', 'res_midrise', 'res_highrise', 'res_penthouse', 'res_estate'] },
  { title: 'Retail', items: ['com_shop', 'com_retail', 'com_market', 'com_super', 'com_mall', 'com_discounter', 'com_hypermarket', 'com_darkstore'] },
  { title: 'Industry & Farms', items: ['farm_wheat', 'farm_cattle', 'farm_orchard', 'ind_factory', 'ind_light', 'ind_warehouse', 'ind_heavy', 'ind_cement', 'ind_logistics', 'farm_dairy', 'farm_abattoir', 'ind_estate', 'harbour_fishing', 'ind_parcelhub', 'ind_chemworks', 'ind_fulfilment', 'ind_refinery'] },
  { title: 'Offices', items: ['off_suite', 'off_tower', 'off_data', 'off_businesspark'] },
  { title: 'Mining', items: ['mine_quarry', 'mine_deep', 'mine_chalk', 'mine_clay', 'mine_coal'] },
  { title: 'Parks', items: ['park', 'park_playground', 'park_town', 'park_botanical', 'park_nature'] },
  { title: 'Leisure', items: ['lei_leisure', 'lei_cinema', 'lei_theatre', 'lei_museum', 'lei_arena', 'lei_themepark', 'lei_gym', 'lei_sportsground', 'lei_stables'] },
  { title: 'Power', items: ['pow_wind', 'pow_coal', 'pow_substation', 'pow_nuke', 'pow_solar', 'pow_windfarm', 'pow_ccgt', 'pow_offshore', 'pow_hydro', 'pow_fusion', 'pow_hvdc', 'pow_reprocess'] },
  { title: 'Water & Waste', items: ['wat_tower', 'wat_clean', 'wat_waste', 'wat_reservoir', 'wat_sewage_regional', 'waste_depot', 'waste_compost', 'waste_recycling', 'waste_landfill', 'waste_incinerator'] },
  { title: 'Health', items: ['hea_clinic', 'hea_hospital', 'hea_ambulance', 'hea_eldercare', 'hea_teaching', 'death_cemetery', 'death_crematorium', 'air_heliport'] },
  { title: 'Police & Justice', items: ['pol_station', 'civ_courthouse', 'pol_hq', 'civ_prison', 'civ_adx', 'air_police_helibase'] },
  { title: 'Fire & Rescue', items: ['fire_post', 'fire_station', 'fire_hq', 'air_fire_helibase'] },
  { title: 'Education', items: ['edu_nursery', 'edu_primary', 'edu_city', 'col_sixth', 'uni', 'edu_tech'] },
  { title: 'Civic', items: ['civ_library', 'civ_townhall', 'civ_cityhall'] },
  { title: 'Landmarks', items: ['land_stadium', 'land_airport', 'land_harbour', 'land_cathedral', 'land_eye', 'land_tunnel', 'land_space', 'land_ferryterminal', 'land_containerport', 'land_gigafactory', 'land_semifab', 'land_cern'] },
];

export const PALETTE_FLAT: string[] = PALETTE.flatMap((g) => g.items);

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

export function residentsCapacity(s: SimState): number {
  let cap = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind === 'residential') cap += sp.residents ?? 8;
  }
  return cap;
}

export function onlineResidentsCapacity(s: SimState): number {
  let cap = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.kind === 'residential') cap += sp.residents ?? 8;
  }
  return cap;
}

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

export function stationLinks(s: SimState): StationLinkInfo {
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
}

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
export function lineUsageOf(s: SimState): LineUsage[] {
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
}

function sumBy(s: SimState, f: (sp: Spec) => boolean, g: (sp: Spec) => number): number {
  let t = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp && f(sp)) t += g(sp);
  }
  return t;
}

export function powerStats(s: SimState): { need: number; cap: number } {
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
  // need/cap; no Date/Math.random. The DD4 grace period is handled inside
  // isOnline (a within-grace building reads online → still counts), so it needs
  // no special-case here.
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
    if (sp.kind === 'power') staticMw += sp.mw ?? 0;
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

export function totalJobs(s: SimState): number {
  let jobs = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    if (sp.jobs) jobs += sp.jobs;
    else if (sp.kind === 'commercial') jobs += 12;
    else if (sp.kind === 'industrial') jobs += 18;
  }
  return jobs;
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
 * for GP/hospital/police/water) is a PLACEHOLDER — directional only, pending
 * Aaron's row-by-row balance pass.
 */
export function serviceCoverageOf(s: SimState): ServiceCoverage[] {
  const pop = s.population;
  const nursery = sumBy(s, (sp) => sp.stage === 'nursery', (sp) => sp.children ?? 0);
  const primary = sumBy(s, (sp) => sp.stage === 'primary' || sp.stage === 'city', (sp) => sp.children ?? 0);
  const tertiary = sumBy(s, (sp) => sp.stage === 'tertiary', (sp) => sp.children ?? 0);
  const gp = sumBy(s, (sp) => sp.id === 'hea_clinic', (sp) => sp.served ?? 0);
  const hosp = sumBy(s, (sp) => sp.id === 'hea_hospital' || sp.id === 'hea_teaching', (sp) => sp.served ?? 0);
  // BUG-399 spec-drift fix: count police coverage by KIND, not a hardcoded id.
  // Newer police buildings (e.g. pol_hq "Divisional HQ", served 60,000) pay
  // upkeep and are unambiguously police coverage in the same served-population
  // unit; keying on id === 'pol_station' left them invisible to the meter, so a
  // fully-policed city read a pegged +100 shortfall. 'police' has exactly one
  // capability (population coverage via `served`), so kind is the right key.
  const police = sumBy(s, (sp) => sp.kind === 'police', (sp) => sp.served ?? 0);
  let clean = 0;
  let waste = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp?.kind !== 'water') continue;
    const eff = plantEffServed(s, b);
    if (sp.tag === 'clean') clean += eff;
    if (sp.tag === 'waste') waste += eff;
  }
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
    row('cleanwater', 'Clean water', pop, clean, 'wat_clean'),
    row('waste', 'Sewage', pop, waste, 'wat_waste'),
    row('power', `Power (${formatPower(pw.cap)}/${formatPower(pw.need)})`, pw.need, pw.cap, pw.need - pw.cap > 60 ? 'pow_coal' : 'pow_wind'),
  ];
}

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

export function serviceDemandOf(
  s: SimState
): { id: string; label: string; value: number; spec: string; alert?: boolean }[] {
  const f = earlyGameFactor(s.population);
  return serviceCoverageOf(s).map((c) => {
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
    const deficit = c.need > 0 && c.coverage < 1;
    const value = deficit
      ? Math.round(Math.min(100, BROWNOUT_INDEX_FLOOR + (1 - c.coverage) * BROWNOUT_INDEX_SLOPE))
      : Math.round(demandIndexOf(c.coverage) * f);
    return { id: c.id, label: c.label, value, spec: c.spec, alert: deficit };
  });
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
 */
export function wasteGeneratedOf(s: SimState): number {
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
}

/**
 * Total refuse COLLECTION capacity this tick, tonnes (Σ online depot wasteCapacity).
 * Only ONLINE depots collect — an under-construction / disconnected depot runs no
 * rounds. Order-independent sum, pure.
 */
export function collectionCapacityOf(s: SimState): number {
  let cap = 0;
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.wasteCapacity) cap += sp.wasteCapacity;
  }
  return cap;
}

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
 */
export function wasteStatsOf(s: SimState): WasteStats {
  const generated = wasteGeneratedOf(s);
  const capacity = collectionCapacityOf(s);
  const coverage = generated > 0 ? Math.min(1, capacity / generated) : 1;
  const collected = Math.min(generated, capacity);
  const uncollected = Math.max(0, generated - collected);
  return { generated, capacity, collected, coverage, uncollected, uncollectedFraction: 1 - coverage };
}

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

/**
 * Total ONLINE processing throughput for one processor spec, tonnes/tick (Σ of that
 * spec's `processCapacity` over online buildings). Only ONLINE processors process —
 * an under-construction / disconnected plant takes nothing. Order-independent, pure.
 */
function processCapacityOf(s: SimState, specId: string): number {
  let cap = 0;
  for (const b of s.buildings) {
    if (b.spec !== specId) continue;
    if (!isOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.processCapacity) cap += sp.processCapacity;
  }
  return cap;
}

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
 */
export function processingMixOf(s: SimState): ProcessingMix {
  const collected = wasteStatsOf(s).collected;
  const efwCapacity = processCapacityOf(s, 'waste_incinerator');
  const mrfCapacity = processCapacityOf(s, 'waste_recycling');
  const compostCapacity = processCapacityOf(s, 'waste_compost');
  const landfillCapacity = processCapacityOf(s, 'waste_landfill');
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
}

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

export function occupiedSet(s: SimState, ignoreId?: number): Set<string> {
  const set = new Set<string>();
  for (const b of s.buildings) {
    if (b.id === ignoreId) continue;
    const sp = SPECS[b.spec];
    if (!sp) continue;
    for (let dx = 0; dx < sp.w; dx++)
      for (let dy = 0; dy < sp.h; dy++) set.add(`${b.x + dx},${b.y + dy}`);
  }
  return set;
}

export function fits(set: Set<string>, w: number, h: number, x: number, y: number): boolean {
  for (let i = 0; i < w; i++) for (let j = 0; j < h; j++) if (set.has(`${x + i},${y + j}`)) return false;
  return true;
}

const cheb = (ax: number, ay: number, bx: number, by: number) =>
  Math.max(Math.abs(ax - bx), Math.abs(ay - by));

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

export function findSpot(s: SimState, specId: string): { x: number; y: number } | null {
  const sp = SPECS[specId];
  if (!sp) return null;
  const occ = occupiedSet(s);
  const hc = housingCentroid(s);

  // Pre-extract only the few buildings that matter for scoring.
  const tagged: Record<Tag, { cx: number; cy: number }[]> = { pollution: [], clean: [], waste: [] };
  const resList: { cx: number; cy: number }[] = [];
  for (const b of s.buildings) {
    const bs = SPECS[b.spec];
    if (!bs) continue;
    if (bs.tag) tagged[bs.tag].push({ cx: b.x + bs.w / 2, cy: b.y + bs.h / 2 });
    if (bs.kind === 'residential') resList.push({ cx: b.x + bs.w / 2, cy: b.y + bs.h / 2 });
  }
  const distTo = (list: { cx: number; cy: number }[], x: number, y: number): number => {
    let min = Infinity;
    for (const p of list) {
      const d = cheb(x, y, p.cx, p.cy);
      if (d < min) min = d;
    }
    return min;
  };

  let best: { x: number; y: number; score: number } | null = null;
  const WIN = 90;
  const xa = Math.max(2, Math.floor(hc.x - WIN / 2));
  const ya = Math.max(2, Math.floor(hc.y - WIN / 2));
  const xb = Math.min(MAP_W - sp.w - 2, xa + WIN);
  const yb = Math.min(MAP_H - sp.h - 2, ya + WIN);

  for (let y = ya; y <= yb; y += 2) {
    for (let x = xa; x <= xb; x += 2) {
      if (!fits(occ, sp.w, sp.h, x, y)) continue;
      const cx = x + sp.w / 2;
      const cy = y + sp.h / 2;
      let score = -cheb(x, y, hc.x, hc.y) / 4;
      const poll = distTo(tagged.pollution, cx, cy);
      const waste = distTo(tagged.waste, cx, cy);
      const clean = distTo(tagged.clean, cx, cy);
      const resNear = distTo(resList, cx, cy);

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

      if (!best || score > best.score) best = { x, y, score };
    }
  }
  return best ? { x: best.x, y: best.y } : null;
}

export function pickAutoSpec(
  s: SimState
): { spec: string; label: string } | null {
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
    if (sp && placementCost(sp) <= s.funds) {
      return { spec: m.spec, label: m.label };
    }
  }
  return null;
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

export interface PolicyDef {
  id: 'recycling' | 'transitSubsidy' | 'tourismDrive' | 'austerity';
  label: string;
  description: string;
}

export const POLICIES: PolicyDef[] = [
  { id: 'recycling', label: 'Recycling Mandate', description: '-7% utility & service upkeep, -2 approval' },
  { id: 'transitSubsidy', label: 'Free Transit', description: '+25% growth rate and +8 approval; costs £1.5 per resident per tick' },
  { id: 'tourismDrive', label: 'Tourism Drive', description: 'Adds Tourism income scaling with population' },
  { id: 'austerity', label: 'Austerity Budget', description: '-10% all outflows, -12 approval' },
];

export interface SpecialistDef {
  id: string;
  name: string;
  unlockLevel: number;
  effect: string;
  cost: number;
}

export const SPECIALISTS: SpecialistDef[] = [
  { id: 'stadium', name: 'Regional Stadium', unlockLevel: 5, effect: 'Large tourism income, +6 approval', cost: 24000 },
  { id: 'university', name: 'University Campus', unlockLevel: 5, effect: 'XP gain x1.5, skilled wage premium', cost: 20000 },
  { id: 'airport', name: 'International Airport', unlockLevel: 6, effect: 'Major tourism + freight income', cost: 45000 },
  { id: 'harbour', name: 'Deep-Water Harbour', unlockLevel: 7, effect: 'Industrial output x1.4', cost: 38000 },
];

export const UNIT_REGISTRY = [
  { unit: 'pound (£)', dimension: 'currency', note: 'All fiscal flows; integers only in the engine' },
  { unit: 'person', dimension: 'population', note: 'Persistent individual citizens' },
  { unit: 'MW', dimension: 'power', note: 'Plant capacity vs grid draw' },
  { unit: 'kL/day', dimension: 'water', note: 'Works throughput' },
  { unit: 'tick', dimension: 'time', note: 'One in-game day; two-layer clock base' },
  { unit: 'tile', dimension: 'length/area', note: '50 m map grid cell' },
];
