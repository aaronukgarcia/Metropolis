import type { GameSave } from './gamesave.ts';
import { DEVCITY1_NAME } from './devcity.ts';
import { safeSetItem } from './safeStorage.ts';
import { encode, decode } from './saveCodec.ts';

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

function writeIndex(storage: NamedSaveStorage, index: NamedSaveMeta[]): boolean {
  return safeSetItem(storage, NAMED_SAVES_INDEX_KEY, JSON.stringify(index)).ok;
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
  // BUG-457: routed through the quota-safe helper — never throws even under
  // quota; callers already treat this as best-effort.
  safeSetItem(storage, CURRENT_CITY_NAME_KEY, displayCityName(name));
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
    // BUG-457: the big write (the whole save blob) — if this alone blows quota,
    // report failure BEFORE touching the index, so the index never points at a
    // slot that was never actually written.
    // FEAT-1972079935: this is the single biggest localStorage payload in the
    // app (a full named city save) — compress it before it hits setItem. The
    // small index/current-city-name keys stay plain JSON, not worth compressing.
    const slotResult = safeSetItem(storage, slotKey(slug), encode(JSON.stringify({ ...save, name })));
    if (!slotResult.ok) return false;
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
    if (!writeIndex(storage, kept)) return false;
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
    // FEAT-1972079935: decode() is a no-op on a legacy uncompressed value.
    return JSON.parse(decode(raw)) as GameSave;
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
