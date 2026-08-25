import type { Dims, SimState, ZoneKind } from './types';

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
};

for (const [id, d] of Object.entries(DIMS)) {
  const sp = SPECS[id];
  if (sp) sp.dims = d;
}

export const PALETTE: { title: string; items: string[] }[] = [
  { title: 'Network', items: ['road'] },
  { title: 'Zones', items: ['res_hut', 'res_block', 'com_shop', 'com_retail', 'farm_wheat', 'farm_cattle', 'farm_orchard', 'ind_factory', 'park'] },
  { title: 'Offices', items: ['off_suite', 'off_tower'] },
  { title: 'Mining', items: ['mine_quarry', 'mine_deep'] },
  { title: 'Power', items: ['pow_wind', 'pow_coal', 'pow_nuke'] },
  { title: 'Water', items: ['wat_clean', 'wat_waste'] },
  { title: 'Health & Police', items: ['hea_clinic', 'hea_hospital', 'pol_station'] },
  { title: 'Education', items: ['edu_nursery', 'edu_primary', 'edu_city', 'col_sixth', 'uni'] },
  { title: 'Landmarks', items: ['land_stadium', 'land_airport', 'land_harbour'] },
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

export function serviceDemandOf(
  s: SimState
): { id: string; label: string; value: number; spec: string }[] {
  const pop = s.population;
  const f = Math.min(1, pop / 50);
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

  const mk = (need: number, cap: number) =>
    Math.round(Math.max(-100, Math.min(100, ((cap - need) / Math.max(need, 10)) * 100)) * f);

  return [
    { id: 'nursery', label: 'Nursery (0–4)', value: mk(pop * 0.06, nursery), spec: 'edu_nursery' },
    { id: 'primary', label: 'School (5–15)', value: mk(pop * 0.12, primary), spec: pop * 0.12 > 1200 ? 'edu_city' : 'edu_primary' },
    { id: 'college', label: 'College (16–19)', value: mk(pop * 0.05, tertiary), spec: pop * 0.05 > 3000 ? 'uni' : 'col_sixth' },
    { id: 'gp', label: 'GP clinics', value: mk(pop / 800, gp), spec: 'hea_clinic' },
    { id: 'hosp', label: 'Hospital', value: mk(pop / 40000, hosp), spec: 'hea_hospital' },
    { id: 'police', label: 'Police', value: mk(pop / 10000, police), spec: 'pol_station' },
    { id: 'cleanwater', label: 'Clean water', value: mk(pop, clean), spec: 'wat_clean' },
    { id: 'waste', label: 'Sewage', value: mk(pop, waste), spec: 'wat_waste' },
    { id: 'power', label: `Power (${pw.cap}/${pw.need} MW)`, value: mk(pw.need, pw.cap), spec: pw.need - pw.cap > 60 ? 'pow_coal' : 'pow_wind' },
  ];
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
  { id: 'transitSubsidy', label: 'Free Transit', description: '+25% growth rate and +8 approval; costs ¤1.5 per resident per tick' },
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
  { unit: 'credit (¤)', dimension: 'currency', note: 'All fiscal flows; integers only in the engine' },
  { unit: 'person', dimension: 'population', note: 'Persistent individual citizens' },
  { unit: 'MW', dimension: 'power', note: 'Plant capacity vs grid draw' },
  { unit: 'kL/day', dimension: 'water', note: 'Works throughput' },
  { unit: 'tick', dimension: 'time', note: 'One in-game day; two-layer clock base' },
  { unit: 'tile', dimension: 'length/area', note: '50 m map grid cell' },
];
