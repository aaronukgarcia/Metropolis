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
import { decode } from './saveCodec.ts';
// BUG-704 round REJECT (P2/P3): the reserved legacy-lineage id, imported
// (not re-literalled as `'legacy'`) so this module's own lineage-scoping and
// its overflow-key derivation can never silently drift from replay.ts's
// definition — the SAME reason `savepointKey` is imported by store.tsx
// rather than hand-re-derived there.
import { LEGACY_LINEAGE_ID, savepointKey, isUsableSavepointFreshness } from './replay.ts';

/** IndexedDB database name + version for the save estate. */
export const SAVE_STORE_DB_NAME = 'metropolis-saves';
export const SAVE_STORE_DB_VERSION = 1;
export const SAVE_STORE_OBJECT_STORE = 'kv';

/** Flag key (inside the store itself) marking the one-time localStorage migration done. */
export const SAVE_STORE_MIGRATED_KEY = 'metropolis.saveStore.migrated';

/**
 * FEAT-2326609780 inc2: IDB-only overflow slot for a savepoint whose
 * localStorage write FAILED (the quota-wedge shape this increment exists to
 * fix). `mirrorSaveCheckpoint`/`mirrorKeyFromLocalStorage` only ever COPY
 * bytes that are already sitting in localStorage — on a failed write,
 * localStorage still holds the OLD (stale) savepoint, so mirroring from it
 * would just re-copy stale data and never let the durable store get ahead.
 * This key instead receives the JUST-COMPUTED savepoint bytes DIRECTLY (never
 * read back from localStorage), so the durable store can advance even when
 * every localStorage slot is wedged. Deliberately outside the
 * `metropolis.savepoint.0..SAVEPOINT_CAP-1` rotation — it is never written to
 * or read from localStorage, only IndexedDB.
 */
export const SAVEPOINT_OVERFLOW_KEY = 'metropolis.savepoint.idbOnly';

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
  const key = code + '\u0000' + msg;
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
// FEAT-2326609780 round 2 (F3/F4, independent round REJECT — ATTACK C/E):
// BUG-469-STYLE OVERWRITE PROTECTION FOR THE DURABLE LAYER.
//
// `persistSavepoint` (replay.ts) refuses to overwrite an occupied localStorage
// slot with an OLDER savepoint. Every write path into IndexedDB's savepoint
// keys (the rotation slots AND the overflow slot) did a bare `setItem` with
// NO such check — in the wedge, the overflow slot is the ONLY surviving copy
// of the advanced city, so ATTACK C proved a later FAILED persist of an
// OLDER savepoint (loading an older named save while still wedged, say)
// silently destroyed it; ATTACK E proved the inc1 migration/mirror path
// mirrors localStorage's STALE bytes over a rotation slot that IndexedDB
// already held something fresher in, unconditionally, mid-mount. Every write
// to a savepoint-shaped IndexedDB key now goes through the same guard.
// ---------------------------------------------------------------------------

/**
 * Matches a rotation-slot OR overflow key, legacy (unnamespaced) or
 * lineage-namespaced (P0 RCA fix, item 2): `metropolis.savepoint.0`,
 * `metropolis.savepoint.idbOnly`, `metropolis.savepoint.<lineageId>.0`,
 * `metropolis.savepoint.<lineageId>.idbOnly`.
 */
const SAVEPOINT_SLOT_KEY_RE = /^metropolis\.savepoint\.([A-Za-z0-9_-]+\.)?(\d+|idbOnly)$/;

/** True for any IndexedDB key this module treats as "savepoint-shaped" and therefore freshness-guarded — the numbered rotation slots plus the overflow slot, legacy or lineage-namespaced. */
function isSavepointGuardedKey(key: string): boolean {
  return SAVEPOINT_SLOT_KEY_RE.test(key) || key === SAVEPOINT_OVERFLOW_KEY;
}

/**
 * Parse just enough of a possibly-`saveCodec.encode()`-compressed savepoint
 * blob to compare freshness. Returns `null` on anything not shaped like a
 * valid savepoint (corrupt JSON, missing/non-finite `snapshotTick`,
 * missing/empty `savedAt`) — fail-OPEN toward "cannot prove this is a
 * regression, so do not block the write" is the safe default here: refusing
 * an incoming write we cannot evaluate would risk permanently wedging the
 * durable store on an unreadable value.
 */
function parseSavepointFreshnessMeta(raw: string): SavepointFreshnessMeta | null {
  try {
    const parsed = JSON.parse(decode(raw)) as { snapshotTick?: unknown; savedAt?: unknown; saveSeq?: unknown; lineageId?: unknown };
    // BUG-704 re-round 2 (P3 item 1): the SAME acceptance predicate
    // replay.ts's `persistSavepointWithReason` now applies to an occupied
    // local rotation slot — see `isUsableSavepointFreshness`'s own doc
    // comment for why the two readers previously disagreed on this exact
    // point.
    if (isUsableSavepointFreshness(parsed)) {
      return {
        snapshotTick: parsed.snapshotTick as number,
        savedAt: parsed.savedAt as string,
        ...(typeof parsed.saveSeq === 'number' && Number.isFinite(parsed.saveSeq) ? { saveSeq: parsed.saveSeq } : {}),
        // BUG-704 round REJECT (P2): carried through so the cross-store gate
        // can apply BUG-687 item 5 (a foreign-lineage candidate is never a
        // competitor) — see `guardedSavepointSetItem`'s own doc comment.
        ...(typeof parsed.lineageId === 'string' && parsed.lineageId.length > 0 ? { lineageId: parsed.lineageId } : {}),
      };
    }
  } catch {
    /* fall through to null — corrupt/unparsable, never blocks the write */
  }
  return null;
}

/** Parsed freshness metadata for one savepoint candidate (see `parseSavepointFreshnessMeta`). */
type SavepointFreshnessMeta = { snapshotTick: number; savedAt: string; saveSeq?: number; lineageId?: string };

/**
 * Normalize a lineage id for comparison — `undefined` and the reserved
 * legacy lineage are the SAME lineage. A per-module copy of the identical
 * rule store.tsx's own `normalizeLineageId` (`isStrictlyFresherSavepointMeta`)
 * uses — kept local rather than imported to avoid a store.tsx <-> saveStore.ts
 * import cycle (store.tsx already imports FROM saveStore.ts), but pinned to
 * the SAME shared `LEGACY_LINEAGE_ID` constant so the two never disagree on
 * WHICH id is reserved, only on where the four-line function itself lives.
 */
function normalizeLineageId(lineageId: string | undefined): string {
  return lineageId && lineageId !== LEGACY_LINEAGE_ID ? lineageId : LEGACY_LINEAGE_ID;
}

/**
 * Is `a` newer-or-equal to `b`, using the SAME saveSeq-primary /
 * tick+savedAt-fallback rule replay.ts's `isIncomingSavepointNewerOrEqual`
 * and store.tsx's `isStrictlyFresherSavepointMeta` use — kept as one small
 * shared function so every freshness gate in this estate agrees byte-for-byte
 * on ordering.
 *
 * BUG-704 fix: `saveSeq` is primary ONLY when BOTH sides carry a real one —
 * this module's original single-key gate treated an ABSENT `saveSeq` as a
 * literal `0` unconditionally, which is NOT what the other two freshness
 * gates in this estate do (see `isIncomingSavepointNewerOrEqual`'s and
 * `isStrictlyFresherSavepointMeta`'s own doc comments for why: a self-heal
 * ALWAYS stamps a real, positive `saveSeq`, so treating "no saveSeq" as `0`
 * makes every pre-round-3 savepoint lose UNCONDITIONALLY regardless of tick —
 * exactly the bug round 3 fixed in the other two gates). Left uncorrected
 * here, feeding in a baseline that predates `saveSeq` (any savepoint written
 * before round 3) would make this module's cross-store comparison disagree
 * with the very decision `persistSavepointWithReason` just made using the
 * correct rule — the opposite of BUG-704's "one shared gate" goal.
 */
function isMetaNewerOrEqual(a: SavepointFreshnessMeta, b: SavepointFreshnessMeta): boolean {
  const aSeqDefined = Number.isFinite(a.saveSeq);
  const bSeqDefined = Number.isFinite(b.saveSeq);
  if (aSeqDefined && bSeqDefined) {
    const aSeq = a.saveSeq as number;
    const bSeq = b.saveSeq as number;
    if (aSeq !== bSeq) return aSeq > bSeq;
    // tied on a REAL saveSeq — fall through to the tick+savedAt tie-break below
  }
  return a.snapshotTick > b.snapshotTick || (a.snapshotTick === b.snapshotTick && a.savedAt >= b.savedAt);
}

/**
 * `store.setItem`, but for a savepoint-shaped key: refuses (returns
 * `ok:false`, the value already there is left untouched) when a valid
 * EXISTING value — the durable store's own copy at `key`, OR any of
 * `extraExistingRaw` — is present and the incoming one is not newer-or-equal
 * to the NEWEST of all of them.
 *
 * BUG-704 fix: this used to compare `value` ONLY against `key`'s own prior
 * contents. That is exactly right for an ordinary rotation-slot mirror (the
 * incoming bytes there always CAME FROM localStorage, so there is nothing
 * else to compare against), but it is the wrong, too-narrow gate for
 * `mirrorSavepointDirect`'s failure-path write into the OVERFLOW slot: that
 * savepoint was JUST refused by localStorage's own freshness gate
 * (`persistSavepointWithReason`'s `stale-overwrite`) as older than what a
 * rotation slot of the SAME lineage already holds — but the overflow key
 * itself had never seen a competitor, so the guard let it through
 * unopposed. The two durable-ish stores (localStorage and the IndexedDB
 * mirror) then disagreed about which savepoint of this lineage was newest,
 * and the stale one could resurrect an old city on a later boot (the exact
 * F4 finding, `attack-bug-436-round.test.mjs`). `extraExistingRaw` lets a
 * caller feed in the OTHER current copies of this lineage's savepoint (the
 * localStorage rotation slots that made the staleness call in the first
 * place) so this is a single ordering gate shared by BOTH stores, not two
 * gates that can silently disagree.
 *
 * Only ever refuses when BOTH the (newest) existing candidate and the
 * incoming value parse as well-formed savepoints — an unreadable candidate
 * (never expected in practice, but never trusted blindly either) never
 * blocks a write, matching this module's fail-open-toward-availability
 * posture everywhere else (GR#1: a durable-layer bug must never be able to
 * brick the save path entirely).
 *
 * BUG-704 round REJECT (P2): `extraExistingRaw` candidates whose OWN stamped
 * `lineageId` differs from the incoming savepoint's lineage are IGNORED
 * entirely — never allowed to become `newestExisting`, never allowed to
 * refuse the write. This is the SAME rule store.tsx's
 * `isStrictlyFresherSavepointMeta` already applies (BUG-687 item 5: "a
 * foreign-lineage candidate is never a competitor, full stop, regardless of
 * what its tick/savedAt/saveSeq claim") — without it, a long-lived city's
 * high tick/saveSeq baseline could wrongly refuse a brand-new, unrelated
 * city's very first savepoint merely because a caller happened to pass in
 * cross-lineage baselines. The target key's OWN prior contents are NOT
 * filtered this way — a namespaced key never physically holds another
 * lineage's savepoint by construction (see `overflowKeyForLineage`), so
 * there is nothing to filter there.
 */
async function guardedSavepointSetItem(
  store: SaveStore,
  key: string,
  value: string,
  extraExistingRaw: Array<string | null | undefined> = [],
): Promise<SaveStoreWriteResult> {
  const existingRaw = await store.getItem(key);
  const incoming = parseSavepointFreshnessMeta(value);
  const incomingLineage = normalizeLineageId(incoming?.lineageId);

  let newestExisting: SavepointFreshnessMeta | null = null;
  if (existingRaw !== null) {
    const meta = parseSavepointFreshnessMeta(existingRaw);
    if (meta) newestExisting = meta;
  }
  const providedExtras = extraExistingRaw.filter((raw): raw is string => raw !== null && raw !== undefined);
  let unparsableExtras = 0;
  for (const raw of providedExtras) {
    const meta = parseSavepointFreshnessMeta(raw);
    if (!meta) {
      unparsableExtras++;
      continue;
    }
    if (normalizeLineageId(meta.lineageId) !== incomingLineage) continue; // BUG-687 item 5: never a competitor.
    if (!newestExisting || isMetaNewerOrEqual(meta, newestExisting)) newestExisting = meta;
  }
  // BUG-704 re-round 2 (P3 item 1): the caller supplied baseline(s) — it made
  // a staleness judgement using them (the whole reason `extraExistingRaw` was
  // passed) — but under this module's own acceptance domain (now shared with
  // replay.ts's `isUsableSavepointFreshness`) NOT ONE of them parsed, and the
  // target key's own contents gave no usable baseline either. This is a
  // corrupt-data condition, not "nothing to compare against": silently
  // treating it as the latter would fail OPEN exactly when the caller's own
  // judgement cannot be independently confirmed here — reproduced: a stale
  // write landed in the overflow slot unopposed because every occupied
  // rotation slot was simultaneously valid JSON and freshness-unreadable.
  // Recorded (GR#17 — a monitoring/verification gap is itself an error) and
  // refused (fail CLOSED) rather than silently falling back to fail-open.
  if (providedExtras.length > 0 && unparsableExtras === providedExtras.length && newestExisting === null) {
    reportOnce(
      'MET-V859',
      `Durable savepoint freshness gate for ${key} received ${providedExtras.length} baseline(s), none of which parsed as a usable savepoint (valid JSON, but corrupt freshness metadata) - refusing the write rather than risk a stale overwrite that cannot be independently verified`,
    );
    return { ok: false, quota: false, degraded: false, error: 'refused: durable freshness baselines were unreadable (fail-closed, corrupt data)' };
  }
  if (newestExisting && incoming && !isMetaNewerOrEqual(incoming, newestExisting)) {
    return { ok: false, quota: false, degraded: false, error: 'refused: an existing durable savepoint is fresher (BUG-469-style overwrite protection)' };
  }
  return store.setItem(key, value);
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
      // F4: the one-time migration is also a savepoint-slot WRITE — guard it
      // exactly like every other one, so a stale localStorage copy being
      // migrated in cannot clobber a slot IndexedDB already holds something
      // fresher in (ATTACK E's exact shape, reachable at mount time).
      const result = isSavepointGuardedKey(key) ? await guardedSavepointSetItem(store, key, value) : await store.setItem(key, value);
      if (result.ok) {
        keysCopied.push(key);
      } else if (result.error?.startsWith('refused:')) {
        // F4: NOT a migration failure — the durable store already holds a
        // fresher savepoint at this key and the guard correctly kept it.
        // Neither copied nor a reportable failure; nothing was lost.
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

/**
 * Mirror a single localStorage key's current value into the durable store.
 * Never throws. F4 (round 2, ATTACK E): a savepoint-shaped key routes
 * through `guardedSavepointSetItem` — an UNCONDITIONAL mirror of
 * localStorage's bytes would otherwise clobber a rotation slot IndexedDB
 * already holds something fresher in (a `mirrorSaveCheckpoint` call racing
 * this mount's own IDB-freshness read, or simply localStorage genuinely
 * being behind after a prior swap). A "refused as stale" result is treated
 * as success here — the destination already holds the right (fresher) bytes.
 */
export async function mirrorKeyFromLocalStorage(store: SaveStore, localStorage: MigrationStorage, key: string): Promise<boolean> {
  try {
    const value = localStorage.getItem(key);
    if (value === null) {
      await store.removeItem(key);
      return true;
    }
    const result = isSavepointGuardedKey(key) ? await guardedSavepointSetItem(store, key, value) : await store.setItem(key, value);
    return result.ok || !!result.error?.startsWith('refused:');
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
/**
 * FEAT-2326609780 inc2: write a savepoint's bytes STRAIGHT into the durable
 * store's overflow slot, bypassing the "read localStorage first" step every
 * other mirror helper here uses. This is the failure-path counterpart to
 * `mirrorSaveCheckpoint` — call it when `persistSavepoint` returned false, so
 * the durable store still advances past the wedge instead of silently
 * re-mirroring the stale localStorage copy. `encodedSavepoint` may be either
 * `saveCodec.encode()`-compressed or plain JSON — `decode()` at read time is
 * a no-op passthrough on uncompressed input, so callers are free to skip
 * compression here (this is IndexedDB, not the 5MB-constrained medium).
 * Never throws.
 *
 * F3 FIX (round 2, independent round REJECT — ATTACK C): this was a bare
 * `setItem` with NO freshness check at all. In the wedge, the overflow slot
 * is the ONLY surviving copy of the advanced city — a later FAILED persist
 * of an OLDER savepoint (e.g. loading an older named save while still
 * wedged) routed straight here via `mirrorAfterPersist` and silently
 * destroyed the rescue copy. Now routes through the same
 * `guardedSavepointSetItem` BUG-469-style overwrite protection every other
 * savepoint-shaped write in this module uses: refuses (returns `false`,
 * leaves the existing value untouched) when the overflow slot already holds
 * something newer-or-equal.
 */
/**
 * P0 RCA fix, item 2: the overflow key for a given lineage. `undefined`/the
 * reserved legacy lineage id map to the ORIGINAL bare `SAVEPOINT_OVERFLOW_KEY`
 * constant (byte-identical to every pre-fix mirror write and to what the
 * round-1/round-2 attacker fixtures — which never stamp a `lineageId` —
 * still exercise), so this is purely ADDITIVE: only a NAMED (minted)
 * lineage gets its own separate overflow slot.
 */
function overflowKeyForLineage(lineageId?: string): string {
  // BUG-704 round REJECT (P3): compares against the IMPORTED constant, not a
  // re-literalled `'legacy'` — a renamed/changed reserved-id definition in
  // replay.ts must not be able to silently desync this key derivation from
  // it (the same GR#3 concern `savepointKey`'s export addresses for
  // store.tsx).
  return !lineageId || lineageId === LEGACY_LINEAGE_ID ? SAVEPOINT_OVERFLOW_KEY : `metropolis.savepoint.${lineageId}.idbOnly`;
}

/**
 * BUG-704 re-round 2 (P3 item 2): the OLD boolean-only contract collapsed
 * three very different failure shapes — a genuine freshness refusal (this
 * savepoint IS older/corrupt-baseline-blocked), a real storage failure
 * (quota exceeded, the durable store degraded to memory), and an unexpected
 * throw — into a single `false`, and the caller (store.tsx) then reported
 * EVERY one of them as "a fresher save already exists", which is simply
 * false for the storage-error case (GR#1: an inaccurate error message is
 * worse than none — it sends the player to fix the wrong thing). `reason`
 * distinguishes the two buckets a caller can act on differently:
 *   - `'stale'`: `guardedSavepointSetItem` itself refused the write (an
 *     ordinary BUG-469-style overwrite refusal, OR the P3-item-1 fail-closed
 *     refusal when every supplied baseline was corrupt) — recognisable by
 *     its `error` always starting with `'refused:'`.
 *   - `'storage-error'`: anything else — `store.setItem` itself failed
 *     (quota, degraded backing store) or the whole call threw.
 */
export interface MirrorSavepointDirectResult {
  ok: boolean;
  reason?: 'stale' | 'storage-error';
  /** The underlying detail string, when available — surfaced verbatim by the caller so the recorded message is accurate rather than a generic canned string. */
  error?: string;
}

export async function mirrorSavepointDirect(
  store: SaveStore,
  encodedSavepoint: string,
  lineageId?: string,
  /**
   * BUG-704: the OTHER current copies of this lineage's savepoint —
   * typically its localStorage rotation slots' raw bytes, read by the
   * caller AFTER `persistSavepointWithReason` made its own staleness
   * decision — fed into the SAME freshness gate as the overflow key's own
   * prior contents, so a savepoint localStorage just refused as stale
   * cannot land unopposed in the durable store merely because the overflow
   * slot itself was empty or older. Optional/omittable for every call site
   * that has no such context (the memory-only unit-test shape, and any
   * legacy caller) — omitting it degrades to the pre-fix single-key
   * comparison, never a regression, never a thrown error.
   */
  extraExistingRaw: Array<string | null | undefined> = [],
): Promise<MirrorSavepointDirectResult> {
  try {
    const result = await guardedSavepointSetItem(store, overflowKeyForLineage(lineageId), encodedSavepoint, extraExistingRaw);
    if (result.ok) return { ok: true };
    const reason: 'stale' | 'storage-error' = result.error?.startsWith('refused:') ? 'stale' : 'storage-error';
    return { ok: false, reason, error: result.error };
  } catch (e) {
    return { ok: false, reason: 'storage-error', error: e instanceof Error ? e.message : String(e) };
  }
}

export async function mirrorSaveCheckpoint(
  store: SaveStore,
  localStorage: MigrationStorage,
  opts: { savepointSlots: number; journalKey: string; lineageId?: string },
): Promise<{ savepointsOk: boolean; journalOk: boolean }> {
  let savepointsOk = true;
  for (let slot = 0; slot < opts.savepointSlots; slot++) {
    // P0 RCA fix, item 2 / BUG-704 round REJECT (P1/P3): uses replay.ts's
    // EXPORTED `savepointKey` directly (was a hand-rolled re-derivation of
    // the same legacy/undefined -> unnamespaced scheme, with the reserved id
    // re-literalled as `'legacy'`) so the IDB copy of a lineage's rotation
    // slots lives at the SAME relative key shape as its localStorage
    // original, with zero risk of the two derivations silently drifting.
    const key = savepointKey(slot, opts.lineageId);
    const ok = await mirrorKeyFromLocalStorage(store, localStorage, key);
    savepointsOk = savepointsOk && ok;
  }
  if (!savepointsOk) return { savepointsOk, journalOk: false };
  const journalOk = await mirrorKeyFromLocalStorage(store, localStorage, opts.journalKey);
  return { savepointsOk, journalOk };
}
