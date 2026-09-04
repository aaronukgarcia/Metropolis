// saveStore.ts — FEAT-2326609778: async IndexedDB durable storage layer.
//
// Aaron ruled Q100121 "Yes, IndexedDB now" (2026-09-04): localStorage's 5MB
// quota already nearly ate Aaron's data once (the 8MB debugQueue incident,
// BUG-617), and the 100m-shape city's spatial-partition spec puts a real
// savepoint at ~3MB — riding the quota edge well before density work lands.
// IndexedDB has no practical browser-imposed size ceiling (it is quota-managed
// against available disk, typically hundreds of MB to several GB), so it is
// the correct home for anything that can grow: savepoints, named saves, the
// journal, and the pre-wipe archive (GR#27).
//
// ARCHITECTURE (the honest trade-off, spelled out — Aaron should be aware of
// this shape, not just the happy path):
//
//   Boot-time restore (store.tsx's lazy useState initializer) MUST be
//   perfectly synchronous — it runs before the very first render, and
//   IndexedDB has no synchronous read API in any browser. Moving boot itself
//   onto IndexedDB would mean either a loading-screen phase (a real UX change,
//   not attempted this increment) or an unsafe synchronous-looking wrapper
//   that isn't actually safe. So: localStorage REMAINS the synchronous
//   boot-time fast-path exactly as today (replay.ts/journal.ts/namedsaves.ts
//   are UNCHANGED, and the BUG-617 instant-boot contract is preserved
//   byte-for-byte — same tests, same behaviour).
//
//   Every WRITE path (autosave, savepoint rotation, named saves, journal
//   flush, the GR#27 pre-wipe archive) keeps writing to localStorage exactly
//   as before (so the fast-path boot always has a same-session, same-tab
//   answer instantly) and ADDITIONALLY mirrors the resulting bytes into this
//   IndexedDB layer, asynchronously, best-effort, never blocking the UI
//   thread and never able to make a write "fail" that would have succeeded
//   on localStorage alone. This is what buys the actual quota headroom: once
//   a save's localStorage copy is evicted or was too big to write in the
//   first place (a hostile quota condition), the IndexedDB mirror is very
//   likely to still hold a copy (its ceiling is far higher), the pre-wipe
//   archive keeps accumulating instead of losing history, and named
//   saves/exports remain recoverable.
//
//   A genuinely IndexedDB-PRIMARY boot (reading through this layer with a
//   loading screen so a save can safely exceed 5MB by itself) is the natural
//   next increment and needs Aaron's ruling on the UX change — flagged here,
//   not silently built, because it is a bigger behavioural change than "add a
//   durable mirror".
//
// DEGRADATION (GR#1/#17): IndexedDB unavailable (older browser, private mode
// in some engines, blocked by policy), or a specific write failing (quota,
// corruption), degrades to an IN-MEMORY session store — lost on reload, but
// the game keeps running and a loud, registry-sourced error is recorded
// (MET-V858 unavailable / MET-V859 write failed) so the player is TOLD their
// save is not durable rather than silently losing it. Never throws out to a
// caller — every method here resolves, never rejects.

import { isQuotaError } from './safeStorage.ts';
import { recordError } from './backend.ts';

/** IndexedDB database name + version for the save estate. */
export const SAVE_STORE_DB_NAME = 'metropolis-saves';
export const SAVE_STORE_DB_VERSION = 1;
export const SAVE_STORE_OBJECT_STORE = 'kv';

/** Flag key (inside the store itself) marking the one-time localStorage migration done. */
export const SAVE_STORE_MIGRATED_KEY = 'metropolis.saveStore.migrated';

/**
 * localStorage key prefixes/exact-keys this layer migrates + mirrors.
 * Deliberately excludes debug/flag keys (metropolis.debug*, metropolis.flag.*,
 * metropolis.errorRing, metropolis.storageConfig, metropolis.webWorkerFlag,
 * etc.) — those stay exactly where they are, per the brief.
 */
export const MIGRATED_KEY_PREFIXES = [
  'metropolis.savepoint.', // replay.ts SAVEPOINT_KEY_PREFIX + '.'
  'metropolis.namedSave.', // namedsaves.ts NAMED_SAVE_SLOT_PREFIX
] as const;

export const MIGRATED_EXACT_KEYS = [
  'metropolis.namedSaves', // namedsaves.ts NAMED_SAVES_INDEX_KEY
  'metropolis.currentCityName', // namedsaves.ts CURRENT_CITY_NAME_KEY
  'metropolis.journal', // journal.ts JOURNAL_KEY
  'metropolis.preWipeArchive', // captureBeforeWipe.ts PREWIPE_ARCHIVE_KEY
] as const;

/** Result of a guarded async write — mirrors safeStorage.ts's SafeSetResult shape. */
export interface SaveStoreWriteResult {
  ok: boolean;
  /** true when the failure was specifically a storage-quota condition. */
  quota: boolean;
  error?: string;
  /** true when this write landed in the in-memory fallback, not IndexedDB. */
  degraded: boolean;
}

/** The minimal async key/value contract every backing implementation satisfies. */
export interface AsyncKVStore {
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<SaveStoreWriteResult>;
  removeItem(key: string): Promise<void>;
  /** All keys, optionally filtered to those starting with `prefix`. */
  listKeys(prefix?: string): Promise<string[]>;
}

/**
 * Always-available in-memory implementation. Used both as the degraded
 * fallback AND directly as a fast, dependency-free backing store in tests.
 */
export function memoryKVStore(): AsyncKVStore {
  const map = new Map<string, string>();
  return {
    async getItem(key) {
      return map.has(key) ? (map.get(key) as string) : null;
    },
    async setItem(key, value) {
      map.set(key, value);
      return { ok: true, quota: false, degraded: true };
    },
    async removeItem(key) {
      map.delete(key);
    },
    async listKeys(prefix) {
      const keys = Array.from(map.keys());
      return prefix ? keys.filter((k) => k.startsWith(prefix)) : keys;
    },
  };
}

/**
 * Minimal subset of the DOM IndexedDB types this module needs — declared
 * locally so this file type-checks under a plain `tsc --lib es2020` (no
 * `dom` lib assumption) as well as the real browser bundle; the real
 * browser's IDBFactory/IDBDatabase/etc. structurally satisfy these.
 */
export interface MiniIDBRequest<T> {
  result: T;
  error: unknown;
  onsuccess: (() => void) | null;
  onerror: (() => void) | null;
}
export interface MiniIDBObjectStore {
  get(key: string): MiniIDBRequest<string | undefined>;
  put(value: string, key: string): MiniIDBRequest<unknown>;
  delete(key: string): MiniIDBRequest<unknown>;
  getAllKeys(): MiniIDBRequest<unknown[]>;
}
export interface MiniIDBTransaction {
  objectStore(name: string): MiniIDBObjectStore;
}
export interface MiniIDBDatabase {
  objectStoreNames: { contains(name: string): boolean };
  createObjectStore(name: string): unknown;
  transaction(name: string, mode: 'readonly' | 'readwrite'): MiniIDBTransaction;
  close(): void;
}
export interface MiniIDBOpenRequest extends MiniIDBRequest<MiniIDBDatabase> {
  onupgradeneeded: (() => void) | null;
  onblocked: (() => void) | null;
}
export interface MiniIDBFactory {
  open(name: string, version: number): MiniIDBOpenRequest;
}

function openDb(factory: MiniIDBFactory): Promise<MiniIDBDatabase> {
  return new Promise((resolve, reject) => {
    let req: MiniIDBOpenRequest;
    try {
      req = factory.open(SAVE_STORE_DB_NAME, SAVE_STORE_DB_VERSION);
    } catch (e) {
      reject(e);
      return;
    }
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(SAVE_STORE_OBJECT_STORE)) {
        db.createObjectStore(SAVE_STORE_OBJECT_STORE);
      }
    };
    req.onblocked = () => reject(new Error('IndexedDB open blocked (another tab holds an old version open)'));
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error instanceof Error ? req.error : new Error('IndexedDB open failed'));
  });
}

function runRequest<T>(fn: () => MiniIDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    let req: MiniIDBRequest<T>;
    try {
      req = fn();
    } catch (e) {
      reject(e);
      return;
    }
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error instanceof Error ? req.error : new Error('IndexedDB request failed'));
  });
}

/**
 * Real IndexedDB-backed implementation. Returns null (feature-detect) when no
 * factory is available (very old browser, or a jsdom-style test environment
 * with no indexedDB global) — callers treat null as "fall back to memory".
 */
export function indexedDBKVStore(factory?: MiniIDBFactory | null): AsyncKVStore | null {
  const idbFactory: MiniIDBFactory | null =
    factory !== undefined ? factory : (globalThis as { indexedDB?: MiniIDBFactory }).indexedDB ?? null;
  if (!idbFactory) return null;

  let dbPromise: Promise<MiniIDBDatabase> | null = null;
  const getDb = (): Promise<MiniIDBDatabase> => {
    if (!dbPromise) dbPromise = openDb(idbFactory);
    return dbPromise;
  };

  return {
    async getItem(key) {
      const db = await getDb();
      const v = await runRequest<string | undefined>(() => db.transaction(SAVE_STORE_OBJECT_STORE, 'readonly').objectStore(SAVE_STORE_OBJECT_STORE).get(key));
      return v === undefined ? null : v;
    },
    async setItem(key, value) {
      try {
        const db = await getDb();
        await runRequest(() => db.transaction(SAVE_STORE_OBJECT_STORE, 'readwrite').objectStore(SAVE_STORE_OBJECT_STORE).put(value, key));
        return { ok: true, quota: false, degraded: false };
      } catch (e) {
        return { ok: false, quota: isQuotaError(e), error: e instanceof Error ? e.message : String(e), degraded: false };
      }
    },
    async removeItem(key) {
      const db = await getDb();
      await runRequest(() => db.transaction(SAVE_STORE_OBJECT_STORE, 'readwrite').objectStore(SAVE_STORE_OBJECT_STORE).delete(key));
    },
    async listKeys(prefix) {
      const db = await getDb();
      const keys = await runRequest<unknown[]>(() => db.transaction(SAVE_STORE_OBJECT_STORE, 'readonly').objectStore(SAVE_STORE_OBJECT_STORE).getAllKeys());
      const strKeys = keys.map(String);
      return prefix ? strKeys.filter((k) => k.startsWith(prefix)) : strKeys;
    },
  };
}

/** Bounded, deduplicated-by-code loud-error reporting so a quota storm doesn't spam the ring. */
const reportedOnce = new Set<string>();
function reportOnce(code: string, msg: string): void {
  const key = code + ' ' + msg;
  if (reportedOnce.has(key)) return;
  reportedOnce.add(key);
  recordError(msg, { type: 'app', action: 'saveStore', code });
}

/**
 * The durable layer's public surface: a store that ALWAYS resolves (never
 * rejects), transparently falling back to an in-memory overlay per-key on any
 * IndexedDB failure, while surfacing a loud registry error the first time
 * that happens (GR#1/#17) — the fallback keeps the game running, but the
 * player is told a save is not durable.
 */
export interface SaveStore extends AsyncKVStore {
  /** true once IndexedDB has been confirmed unusable (fully degraded to memory). */
  isFullyDegraded(): boolean;
  /** keys currently served from the in-memory overlay rather than IndexedDB. */
  degradedKeys(): string[];
}

export function createSaveStore(idb: AsyncKVStore | null): SaveStore {
  const mem = memoryKVStore();
  const degraded = new Set<string>();
  let fullyDegraded = idb === null;
  if (fullyDegraded) {
    reportOnce('MET-V858', 'IndexedDB is unavailable (no indexedDB global) — saves are running in-memory only and will NOT survive a reload');
  }

  const markDegraded = (key: string, reason: string, quota: boolean): void => {
    degraded.add(key);
    fullyDegraded = true;
    reportOnce(
      quota ? 'MET-V859' : 'MET-V858',
      quota
        ? `Durable save write failed for ${SAVE_STORE_OBJECT_STORE}/${key} (${reason}) - falling back to in-memory storage for this session`
        : `IndexedDB is unavailable (${reason}) - saves are running in-memory only and will NOT survive a reload`,
    );
  };

  return {
    async getItem(key) {
      if (idb && !degraded.has(key)) {
        try {
          const v = await idb.getItem(key);
          if (v !== null) return v;
        } catch (e) {
          markDegraded(key, e instanceof Error ? e.message : String(e), isQuotaError(e));
        }
      }
      return mem.getItem(key);
    },
    async setItem(key, value) {
      if (idb && !degraded.has(key)) {
        try {
          const r = await idb.setItem(key, value);
          if (r.ok) return r;
          markDegraded(key, r.error ?? 'unknown write failure', r.quota);
        } catch (e) {
          markDegraded(key, e instanceof Error ? e.message : String(e), isQuotaError(e));
        }
      }
      const memResult = await mem.setItem(key, value);
      return { ...memResult, degraded: true };
    },
    async removeItem(key) {
      if (idb) {
        try {
          await idb.removeItem(key);
        } catch {
          /* best-effort — the memory overlay removal below still applies */
        }
      }
      await mem.removeItem(key);
    },
    async listKeys(prefix) {
      const seen = new Set<string>();
      if (idb) {
        try {
          for (const k of await idb.listKeys(prefix)) seen.add(k);
        } catch {
          /* fall through to memory-only view */
        }
      }
      for (const k of await mem.listKeys(prefix)) seen.add(k);
      return Array.from(seen);
    },
    isFullyDegraded() {
      return fullyDegraded;
    },
    degradedKeys() {
      return Array.from(degraded);
    },
  };
}

let defaultStore: SaveStore | null = null;

/** Lazily-constructed process-wide singleton backing the mirror helpers below. */
export function getDefaultSaveStore(): SaveStore {
  if (!defaultStore) defaultStore = createSaveStore(indexedDBKVStore());
  return defaultStore;
}

/** Test-only: reset the singleton and the reportOnce dedup set between test files. */
export function resetSaveStoreForTests(): void {
  defaultStore = null;
  reportedOnce.clear();
}

// ---------------------------------------------------------------------------
// Migration — one-time, copy-in, NEVER delete the localStorage originals.
// ---------------------------------------------------------------------------

export interface MigrationStorage {
  getItem(key: string): string | null;
  length?: number;
  key?(index: number): string | null;
}

export interface MigrationResult {
  ran: boolean;
  keysCopied: string[];
  failures: string[];
}

/**
 * One-time copy of every existing localStorage save-estate key into the
 * durable store. Idempotent (checks + sets SAVE_STORE_MIGRATED_KEY in the
 * store itself) and additive-only: `localStorage` is read via `getItem` only
 * — this function never calls `removeItem` on it, matching Aaron's explicit
 * "belt and braces" instruction. A per-key copy failure is reported (loud,
 * MET-V860, warn-severity) and skipped — one bad key never aborts the rest of
 * the migration.
 */
export async function migrateFromLocalStorage(store: SaveStore, localStorage: MigrationStorage): Promise<MigrationResult> {
  const already = await store.getItem(SAVE_STORE_MIGRATED_KEY);
  if (already === '1') return { ran: false, keysCopied: [], failures: [] };

  const candidateKeys: string[] = [];
  // Exact keys are cheap to probe directly.
  for (const k of MIGRATED_EXACT_KEYS) candidateKeys.push(k);
  // Prefixed keys (savepoint slots, named-save slots) need enumeration —
  // localStorage's `key(i)`/`length` iteration when available; fixed-slot
  // prefixes are also probed directly as a fallback for storage mocks that
  // don't implement the iteration API (the existing test doubles in this
  // codebase — see replay.ts's StorageLike — commonly don't).
  if (typeof localStorage.length === 'number' && typeof localStorage.key === 'function') {
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i);
      if (k && MIGRATED_KEY_PREFIXES.some((p) => k.startsWith(p))) candidateKeys.push(k);
    }
  } else {
    for (let slot = 0; slot < 8; slot++) candidateKeys.push(`metropolis.savepoint.${slot}`);
  }

  const keysCopied: string[] = [];
  const failures: string[] = [];
  for (const key of Array.from(new Set(candidateKeys))) {
    try {
      const value = localStorage.getItem(key);
      if (value === null) continue;
      const result = await store.setItem(key, value);
      if (result.ok) {
        keysCopied.push(key);
      } else {
        failures.push(key);
        reportOnce('MET-V860', `Migrating existing localStorage save ${key} into IndexedDB failed (${result.error ?? 'unknown'}) - the original localStorage copy was left untouched`);
      }
    } catch (e) {
      failures.push(key);
      reportOnce('MET-V860', `Migrating existing localStorage save ${key} into IndexedDB failed (${e instanceof Error ? e.message : String(e)}) - the original localStorage copy was left untouched`);
    }
  }

  await store.setItem(SAVE_STORE_MIGRATED_KEY, '1');
  return { ran: true, keysCopied, failures };
}

// ---------------------------------------------------------------------------
// Write-through mirroring — the localStorage write remains authoritative and
// synchronous (unchanged call sites); these helpers copy the RESULTING bytes
// into the durable store, best-effort, fire-and-forget-safe (never throws).
// ---------------------------------------------------------------------------

/** Mirror a single localStorage key's current value into the durable store. Never throws. */
export async function mirrorKeyFromLocalStorage(store: SaveStore, localStorage: MigrationStorage, key: string): Promise<boolean> {
  try {
    const value = localStorage.getItem(key);
    if (value === null) {
      await store.removeItem(key);
      return true;
    }
    const result = await store.setItem(key, value);
    return result.ok;
  } catch {
    return false;
  }
}

/**
 * Crash-consistency contract (the brief's requirement: "the journal on disk
 * is never behind the last SAVEPOINT"): mirror every savepoint slot key
 * FIRST and await completion; only once every savepoint mirror has resolved
 * do we mirror the journal key. If a savepoint mirror fails, the journal
 * mirror is skipped entirely for that call — so the durable store never ends
 * up holding a journal that has moved past a savepoint boundary the durable
 * store itself doesn't have. (localStorage itself is unaffected either way —
 * this function only governs the ORDER of the best-effort IndexedDB mirror,
 * never localStorage's own already-correct synchronous contract.)
 */
export async function mirrorSaveCheckpoint(
  store: SaveStore,
  localStorage: MigrationStorage,
  opts: { savepointSlots: number; journalKey: string },
): Promise<{ savepointsOk: boolean; journalOk: boolean }> {
  let savepointsOk = true;
  for (let slot = 0; slot < opts.savepointSlots; slot++) {
    const ok = await mirrorKeyFromLocalStorage(store, localStorage, `metropolis.savepoint.${slot}`);
    savepointsOk = savepointsOk && ok;
  }
  if (!savepointsOk) return { savepointsOk, journalOk: false };
  const journalOk = await mirrorKeyFromLocalStorage(store, localStorage, opts.journalKey);
  return { savepointsOk, journalOk };
}
