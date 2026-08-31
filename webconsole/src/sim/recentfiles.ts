import { cityNameToSlug, displayCityName } from './namedsaves.ts';

export const RECENT_OPENED_KEY = 'metropolis.recentOpened';
export const RECENT_OPENED_CAP = 10;

export interface RecentOpened {
  name: string;
  slug: string;
  tick: number;
  population: number;
  funds: number;
  openedAt: string;
}

export interface RecentStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

function readList(storage: RecentStorage): RecentOpened[] {
  try {
    const raw = storage.getItem(RECENT_OPENED_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? (parsed as RecentOpened[]) : [];
  } catch {
    return [];
  }
}

export function listRecentOpened(storage: RecentStorage): RecentOpened[] {
  return readList(storage).slice(0, RECENT_OPENED_CAP);
}

export function recordRecentOpened(
  storage: RecentStorage,
  entry: { name: string; tick: number; population: number; funds: number; slug?: string; openedAt?: string },
): void {
  const name = displayCityName(entry.name);
  const slug = entry.slug ?? cityNameToSlug(name);
  const row: RecentOpened = {
    name,
    slug,
    tick: entry.tick,
    population: entry.population,
    funds: entry.funds,
    openedAt: entry.openedAt ?? new Date().toISOString(),
  };
  const next = [row, ...readList(storage).filter((r) => r.slug !== slug)].slice(0, RECENT_OPENED_CAP);
  storage.setItem(RECENT_OPENED_KEY, JSON.stringify(next));
}
