export const STORAGE_CONFIG_KEY = 'metropolis.storageConfig';

export const PREWIPE_CAP = 10;

const PREWIPE_CAP_MIN = 1;
const PREWIPE_CAP_MAX = 100;

export interface ConfigStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

export interface UsageStorage {
  readonly length: number;
  key(index: number): string | null;
  getItem(key: string): string | null;
}

export interface StorageUsageKey {
  key: string;
  bytes: number;
}

export interface StorageUsage {
  bytes: number;
  keys: StorageUsageKey[];
}

function clampPrewipeCap(n: number): number {
  if (typeof n !== 'number' || !Number.isFinite(n)) return PREWIPE_CAP;
  const f = Math.floor(n);
  if (f < PREWIPE_CAP_MIN) return PREWIPE_CAP_MIN;
  if (f > PREWIPE_CAP_MAX) return PREWIPE_CAP_MAX;
  return f;
}

export function getPrewipeCap(storage?: { getItem(key: string): string | null }): number {
  if (!storage) return PREWIPE_CAP;
  try {
    const raw = storage.getItem(STORAGE_CONFIG_KEY);
    if (!raw) return PREWIPE_CAP;
    const parsed = JSON.parse(raw) as { prewipeCap?: unknown };
    return clampPrewipeCap(parsed?.prewipeCap as number);
  } catch {
    return PREWIPE_CAP;
  }
}

export function setPrewipeCap(storage: ConfigStorage, n: number): void {
  const prewipeCap = clampPrewipeCap(n);
  let prev: Record<string, unknown> = {};
  try {
    const raw = storage.getItem(STORAGE_CONFIG_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as unknown;
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        prev = parsed as Record<string, unknown>;
      }
    }
  } catch {
    prev = {};
  }
  storage.setItem(STORAGE_CONFIG_KEY, JSON.stringify({ ...prev, prewipeCap }));
}

export const TYPICAL_LOCALSTORAGE_QUOTA_BYTES = 5 * 1024 * 1024;

export function fmtStorageBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '0 B';
  if (n < 1024) return `${Math.round(n)} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(2)} MB`;
}

export function localStorageUsage(storage: UsageStorage): StorageUsage {
  const keys: StorageUsageKey[] = [];
  let bytes = 0;
  for (let i = 0; i < storage.length; i++) {
    const key = storage.key(i);
    if (key == null) continue;
    const value = storage.getItem(key) ?? '';
    const b = (key.length + value.length) * 2;
    keys.push({ key, bytes: b });
    bytes += b;
  }
  keys.sort((a, b) => b.bytes - a.bytes);
  return { bytes, keys };
}
