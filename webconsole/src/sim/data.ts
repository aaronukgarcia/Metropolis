import type { Dims, SimState, ZoneKind } from './types.ts';

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
    if (sp.unlock === level && sp.unlock !== 99 && sp.category !== 'network') {
      names.push(sp.name);
    }
  }
  return names;
}

export function isOnline(s: SimState, b: SimState['buildings'][number]): boolean {
  if (b.builtTick == null) return true;
  const sp = SPECS[b.spec];
  return s.tick - b.builtTick >= constructionTicks(sp);
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

export function utilisationOf(s: SimState, b: SimState['buildings'][number]): number | null {
  const sp = SPECS[b.spec];
  if (!sp) return null;
  const pct = (have: number, cap: number) =>
    cap > 0 ? Math.round(Math.min(150, (have / cap) * 100)) : null;
  switch (sp.kind) {
    case 'residential': {
      const cap = residentsCapacity(s);
      return cap > 0 ? pct(s.population, cap) : null;
    }
    case 'power': {
      const pw = powerStats(s);
      return pct(pw.need, pw.cap);
    }
    case 'school': {
      let places = 0;
      for (const o of s.buildings) {
        const os = SPECS[o.spec];
        if (os?.children) places += os.children;
      }
      return pct(s.population * 0.18, places);
    }
    case 'water': {
      const { clean } = waterCaps(s);
      return pct(s.population, clean);
    }
    case 'commercial':
    case 'office':
    case 'industrial':
    case 'mine': {
      const jobs = totalJobs(s);
      return pct(s.population * 0.55, jobs);
    }
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

export const SPECS: Record<string, Spec> = {
  m20: P('m20', 'motorway', 'M20 Motorway', '', 1, 1, 0, 0, '#1d5fa8', 'network', 99),
  rail: P('rail', 'rail', 'Rail Line', '', 1, 1, 0, 0, '#8a6d3b', 'network', 99),
  station_sanderling: P('station_sanderling', 'station', 'Sanderling Station', '', 1, 1, 0, 15, '#d0a83c', 'network', 99),
  station_ashford: P('station_ashford', 'station', 'Ashford International', 'HS1 international gateway · 60,000 served · x3 commuter weight', 4, 2, 80000, 220, '#e0559f', 'network', 5, { served: 60000 }),
  hs1: P('hs1', 'rail', 'HS1 High-Speed Line', '', 1, 1, 0, 0, '#c2477e', 'network', 99),
  pylon: P('pylon', 'pylon', 'HV Pylon', '', 1, 1, 0, 5, '#9aa4ae', 'network', 99),

  road: P('road', 'road', 'Road', '', 1, 1, 40, 3, '#4a525c', 'network', 1),

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
  pow_nuke: P('pow_nuke', 'power', 'Nuclear Plant', 'Twin AGR · 1,120 MW · Dungeness-scale', 13, 13, 150000, 600, '#e05d38', 'services', 5, { mw: 1120, tag: 'pollution' }),

  wat_clean: P('wat_clean', 'water', 'Water Works', 'Clean water for 20,000', 2, 2, 2600, 38, '#39c5cf', 'services', 3, { tag: 'clean', served: 20000 }),
  wat_waste: P('wat_waste', 'water', 'Waste-Water Plant', 'Treats sewage for 20,000', 2, 2, 3400, 44, '#6b8f71', 'services', 3, { tag: 'waste', served: 20000 }),

  hea_clinic: P('hea_clinic', 'health', 'Clinic', 'GPs for 5,000', 1, 1, 1800, 26, '#ff7b72', 'services', 2, { served: 5000 }),
  hea_hospital: P('hea_hospital', 'health', 'General Hospital', 'Serves 40,000', 2, 2, 16000, 210, '#d95f57', 'services', 4, { served: 40000 }),
  pol_station: P('pol_station', 'police', 'Police Station', 'Covers 10,000', 2, 1, 2600, 34, '#6e7bd9', 'services', 3, { served: 10000 }),

  edu_nursery: P('edu_nursery', 'school', 'Kindergarten', '30 places · ages 0–4', 1, 1, 1200, 22, '#ffd166', 'services', 2, { children: 30, stage: 'nursery' }),
  edu_primary: P('edu_primary', 'school', 'Primary School', '300 places · ages 5–11', 2, 2, 5200, 70, '#f2c14e', 'services', 3, { children: 300, stage: 'primary' }),
  edu_city: P('edu_city', 'school', 'City School', '2,000 places · ages 5–16', 3, 2, 32000, 320, '#e3a92f', 'services', 4, { children: 2000, stage: 'city' }),
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
  pow_fusion: P('pow_fusion', 'power', 'Fusion Pilot Plant', '800 MW · experimental', 4, 4, 400000, 900, '#ff9f43', 'services', 19, { mw: 800 }),

  // ---- Water & waste additions ----
  wat_tower: P('wat_tower', 'water', 'Water Tower', 'Pressure head for 4,000', 1, 1, 1500, 14, '#39c5cf', 'services', 2, { tag: 'clean', served: 4000 }),
  wat_reservoir: P('wat_reservoir', 'water', 'Reservoir', 'Valley dam · serves 60,000', 4, 4, 45000, 150, '#2ba7b1', 'services', 9, { tag: 'clean', served: 60000 }),
  wat_sewage_regional: P('wat_sewage_regional', 'water', 'Regional Sewage Works', 'Treats waste for 60,000', 3, 3, 38000, 170, '#6b8f71', 'services', 11, { tag: 'waste', served: 60000 }),

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
export const PALETTE: { title: string; items: string[] }[] = [
  { title: 'Network', items: ['road'] },
  { title: 'Transport', items: ['bus_stop', 'bus_depot', 'car_park', 'station_ashford', 'bus_station', 'tram_depot', 'ferry_pier', 'metro_station', 'grand_terminus'] },
  { title: 'Housing', items: ['res_hut', 'res_block', 'res_terrace', 'res_lowrise', 'res_midrise', 'res_highrise', 'res_penthouse'] },
  { title: 'Retail', items: ['com_shop', 'com_retail', 'com_market', 'com_super', 'com_mall'] },
  { title: 'Industry & Farms', items: ['farm_wheat', 'farm_cattle', 'farm_orchard', 'ind_factory', 'ind_light', 'ind_warehouse', 'ind_heavy', 'ind_cement', 'ind_logistics'] },
  { title: 'Offices', items: ['off_suite', 'off_tower', 'off_data'] },
  { title: 'Mining', items: ['mine_quarry', 'mine_deep'] },
  { title: 'Parks', items: ['park', 'park_playground', 'park_town', 'park_botanical', 'park_nature'] },
  { title: 'Leisure', items: ['lei_leisure', 'lei_cinema', 'lei_theatre', 'lei_museum', 'lei_arena', 'lei_themepark'] },
  { title: 'Power', items: ['pow_wind', 'pow_coal', 'pow_substation', 'pow_nuke', 'pow_solar', 'pow_windfarm', 'pow_ccgt', 'pow_offshore', 'pow_fusion'] },
  { title: 'Water & Waste', items: ['wat_tower', 'wat_clean', 'wat_waste', 'wat_reservoir', 'wat_sewage_regional'] },
  { title: 'Health', items: ['hea_clinic', 'hea_hospital', 'hea_ambulance', 'hea_eldercare', 'hea_teaching'] },
  { title: 'Police & Justice', items: ['pol_station', 'civ_courthouse', 'pol_hq', 'civ_prison', 'civ_adx'] },
  { title: 'Fire & Rescue', items: ['fire_post', 'fire_station', 'fire_hq'] },
  { title: 'Education', items: ['edu_nursery', 'edu_primary', 'edu_city', 'col_sixth', 'uni', 'edu_tech'] },
  { title: 'Civic', items: ['civ_library', 'civ_townhall', 'civ_cityhall'] },
  { title: 'Landmarks', items: ['land_stadium', 'land_airport', 'land_harbour', 'land_cathedral', 'land_eye', 'land_tunnel', 'land_space'] },
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

function sumBy(s: SimState, f: (sp: Spec) => boolean, g: (sp: Spec) => number): number {
  let t = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp && f(sp)) t += g(sp);
  }
  return t;
}

export function powerStats(s: SimState): { need: number; cap: number } {
  const c = countByKind(s.buildings);
  return {
    need: Math.round(s.population * 0.012 + c.industrial * 6 + c.office * 4 + c.mine * 8),
    cap: sumBy(s, (sp) => sp.kind === 'power', (sp) => sp.mw ?? 0),
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
  const hosp = sumBy(s, (sp) => sp.id === 'hea_hospital', (sp) => sp.served ?? 0);
  const police = sumBy(s, (sp) => sp.id === 'pol_station', (sp) => sp.served ?? 0);
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
    row('power', `Power (${pw.cap}/${pw.need} MW)`, pw.need, pw.cap, pw.need - pw.cap > 60 ? 'pow_coal' : 'pow_wind'),
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
  if (meters.length && meters[0].value > 25) {
    return { spec: meters[0].spec, label: meters[0].label };
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
