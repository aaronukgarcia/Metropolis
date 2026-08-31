import type { GameSave } from './gamesave.ts';
import { DEVCITY1_NAME } from './devcity.ts';

export const NAMED_SAVES_INDEX_KEY = 'metropolis.namedSaves';
export const NAMED_SAVE_SLOT_PREFIX = 'metropolis.namedSave.';
export const CURRENT_CITY_NAME_KEY = 'metropolis.currentCityName';
export const NAMED_SAVE_BLOB_CAP = 2;

export interface NamedSaveMeta {
  name: string;
  slug: string;
  savedAt: string;
  tick: number;
  population: number;
  funds: number;
}

export interface NamedSaveStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

export function displayCityName(name: string): string {
  const t = name.trim().slice(0, 40);
  return t || DEVCITY1_NAME;
}

export function cityNameToSlug(name: string): string {
  const s = displayCityName(name)
    .replace(/[^a-zA-Z0-9._-]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return s.slice(0, 40) || 'city';
}

function slotKey(slug: string): string {
  return `${NAMED_SAVE_SLOT_PREFIX}${slug}`;
}

function readIndex(storage: NamedSaveStorage): NamedSaveMeta[] {
  try {
    const raw = storage.getItem(NAMED_SAVES_INDEX_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? (parsed as NamedSaveMeta[]) : [];
  } catch {
    return [];
  }
}

function writeIndex(storage: NamedSaveStorage, index: NamedSaveMeta[]): void {
  storage.setItem(NAMED_SAVES_INDEX_KEY, JSON.stringify(index));
}

export function listNamedSaves(storage: NamedSaveStorage): NamedSaveMeta[] {
  return readIndex(storage);
}

export function getCurrentCityName(storage: NamedSaveStorage): string {
  try {
    return displayCityName(storage.getItem(CURRENT_CITY_NAME_KEY) ?? DEVCITY1_NAME);
  } catch {
    return DEVCITY1_NAME;
  }
}

export function setCurrentCityName(storage: NamedSaveStorage, name: string): void {
  storage.setItem(CURRENT_CITY_NAME_KEY, displayCityName(name));
}

export function writeNamedSave(storage: NamedSaveStorage, save: GameSave): boolean {
  try {
    const name = displayCityName(save.name);
    const slug = cityNameToSlug(name);
    const meta: NamedSaveMeta = {
      name,
      slug,
      savedAt: save.savedAt,
      tick: save.savepoint.snapshot.tick,
      population: save.savepoint.snapshot.population,
      funds: save.savepoint.snapshot.funds,
    };
    storage.setItem(slotKey(slug), JSON.stringify({ ...save, name }));
    const index = readIndex(storage).filter((m) => m.slug !== slug);
    index.unshift(meta);
    const dropped = index.slice(NAMED_SAVE_BLOB_CAP);
    const kept = index.slice(0, NAMED_SAVE_BLOB_CAP);
    for (const d of dropped) {
      try {
        storage.removeItem(slotKey(d.slug));
      } catch {
        /* ignore */
      }
    }
    writeIndex(storage, kept);
    setCurrentCityName(storage, name);
    return true;
  } catch {
    return false;
  }
}

export function readNamedSave(storage: NamedSaveStorage, slug: string): GameSave | null {
  try {
    const raw = storage.getItem(slotKey(slug));
    if (!raw) return null;
    return JSON.parse(raw) as GameSave;
  } catch {
    return null;
  }
}

export function renameNamedSave(storage: NamedSaveStorage, oldSlug: string, newName: string): boolean {
  const save = readNamedSave(storage, oldSlug);
  if (!save) return false;
  const named = { ...save, name: displayCityName(newName) };
  const newSlug = cityNameToSlug(named.name);
  if (!writeNamedSave(storage, named)) return false;
  if (newSlug !== oldSlug) {
    try {
      storage.removeItem(slotKey(oldSlug));
    } catch {
      /* keep the new slot even if the old key lingers */
    }
    const index = readIndex(storage).filter((m) => m.slug !== oldSlug);
    writeIndex(storage, index);
  }
  return true;
}
