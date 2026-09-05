// commitqueue.ts — FEAT-1972079885: the client-side snapshot commit queue,
// extracted from backend.ts as a PURE module so its behaviour is unit-testable
// (the storage is injected; no window/localStorage globals here).
//
// ── ASM-453: THE NO-BACKEND CONTRACT ────────────────────────────────────────
// There is no debug backend today. `POST /api/debug/commit` always fails, so
// every "Commit snapshot" lands HERE: an append-only queue of committed
// debug.json frames persisted client-side (localStorage, key QUEUE_KEY),
// surfaced in the DebugTab as "N queued". The queue's job is to survive
// reloads so nothing a session committed is lost before a backend exists.
// When a backend arrives it drains this queue: read `readQueue()`, POST each
// entry (oldest first — array order IS commit order), and remove entries only
// on a 2xx. Until then entries are never removed except to stay under bounds:
// QUEUE_CAP (entry count) and QUEUE_BYTE_BUDGET (serialized bytes), both of
// which drop the OLDEST entries first. Do not change QUEUE_KEY or the
// QueuedCommit shape without a migration — persisted queues in players'
// browsers are written in this exact schema.
//
// ── BUG-607: BYTE BUDGET + CONTENT COMPACTION ───────────────────────────────
// Debug frames scale with city size (buildings.list, road monitors, ...), so
// a 50-ENTRY cap alone does not bound bytes — one real browser showed 8.02MB
// in this single key of a ~5-10MB origin quota, which made every OTHER
// localStorage.setItem in the app (including autosaves) start throwing.
// Two changes, both SCHEMA-COMPATIBLE — readQueue() of an old, pre-BUG-607 fat
// entry still works unchanged, because QueuedCommit.payload is `unknown` and
// its internal shape was never part of the persisted contract:
//   1. `compactPayload()` strips the heaviest, least-needed sections from a
//      debug-json-shaped `payload` before it is queued — mirrors
//      captureBeforeWipe.ts's compactDebugForArchive (buildings.list,
//      sim.roadMonitors / sim.roadConnectivity.connectedRoadTiles, and
//      perfHud all dropped; meta / sim scalars / flows / consistency / errors
//      are KEPT so a future backend drain stays useful). A payload that
//      doesn't look like a DebugJson (missing/wrong-typed buildings/sim)
//      passes through unchanged rather than throwing — this module has no
//      DebugJson import and stays duck-typed against `unknown`.
//   2. After append, `enqueueCommit` evicts OLDEST entries until the
//      serialized queue fits QUEUE_BYTE_BUDGET. A single (already-compacted)
//      entry that alone exceeds the budget is dropped rather than starving
//      out the rest of the queue for one outsized frame — the caller records
//      MET-V854 for this (this module stays pure/throw-free, no recordError
//      import). A QuotaExceededError from the underlying storage.setItem
//      itself degrades further: drop the oldest half of what would have been
//      written and retry ONCE, then give up WITHOUT throwing (the caller
//      records MET-V855) — a queue write must never break the commit button
//      or the sim (GR#1).
// ────────────────────────────────────────────────────────────────────────────

import { safeSetItem } from './safeStorage.ts';

// FEAT-2326609752 (AARON Q100078=B, 2026-09-03): "Debug commits into metro
// MariaDB via a tiny local endpoint" — backend.ts now tries the sink FIRST
// and only falls back to this queue on failure/timeout (the ASM-453
// contract is unchanged: nothing committed is ever lost). When the sink
// becomes reachable again, backend.ts opportunistically DRAINS whatever
// this queue is still holding: read the queue (oldest first — array order
// IS commit order, unchanged from the header note above), POST each entry,
// and persist the queue with that entry removed ONLY after a 2xx response.
// `writeQueue` is the single write path a drain uses so QUEUE_KEY writes
// stay centralized in this module (SSOT) rather than backend.ts reaching
// into localStorage directly.

/** localStorage key holding the persisted queue (ASM-453 schema — stable). */
export const QUEUE_KEY = 'metropolis.debugQueue';

/** Max queued commits kept; overflow drops the OLDEST entries first. */
export const QUEUE_CAP = 50;

/**
 * ⚠ BALANCE/config placeholder (BUG-607): serialized-queue byte budget. 2MB
 * leaves headroom under a ~5-10MB origin quota so autosaves/journal/named
 * saves/pre-wipe archive keep working alongside a full debug queue. Revisit
 * once real dogfood data shows the actual multi-key quota pressure.
 */
export const QUEUE_BYTE_BUDGET = 2 * 1024 * 1024;

/** One committed-but-unsent debug.json frame awaiting a backend (ASM-453). */
export interface QueuedCommit {
  /** Commit id (DBG-...), assigned at commit time. */
  id: string;
  /** ISO-8601 wall-clock time of the commit. */
  at: string;
  /** The committed debug.json frame, exactly as shown on screen (WYSIWYG) —
   * BUT compacted (BUG-607) before persisting; see compactPayload(). */
  payload: unknown;
}

/** The subset of the Web Storage API the queue needs — injectable for tests. */
export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

/**
 * Read the persisted queue. Fail-safe: missing key, corrupt JSON, or a
 * non-array value all degrade to an EMPTY queue rather than throwing —
 * a broken store must never take the Debug tab down with it.
 */
export function readQueue(storage: StorageLike): QueuedCommit[] {
  try {
    const raw = JSON.parse(storage.getItem(QUEUE_KEY) ?? '[]');
    return Array.isArray(raw) ? (raw as QueuedCommit[]) : [];
  } catch {
    return [];
  }
}

/** Number of commits awaiting a backend — the DebugTab's "N queued" badge. */
export function pendingCount(storage: StorageLike): number {
  return readQueue(storage).length;
}

/**
 * Persist a full queue array directly (FEAT-2326609752 drain path — remove
 * one drained entry and rewrite the rest). Never throws: on a storage
 * failure this returns false and the caller (backend.ts's drain loop)
 * simply stops draining rather than losing track of what is still queued —
 * the ON-DISK queue is only ever what a successful write actually persisted.
 */
export function writeQueue(storage: StorageLike, queue: QueuedCommit[]): boolean {
  return safeSetItem(storage, QUEUE_KEY, JSON.stringify(queue)).ok;
}

/** UTF-8 byte length of a string — the real localStorage quota unit (not .length). */
function byteLength(s: string): number {
  if (typeof TextEncoder !== 'undefined') return new TextEncoder().encode(s).length;
  // Node fallback: TextEncoder has been global since Node 11, this is belt-and-braces.
  return Buffer.byteLength(s, 'utf8');
}

/**
 * BUG-607: strip the heaviest, least-needed sections from a debug-json-shaped
 * payload before it is queued, mirroring captureBeforeWipe.ts's
 * compactDebugForArchive. Duck-typed against `unknown` (this module does not
 * import DebugJson) — any section that isn't present/well-shaped is left
 * alone rather than throwing, so a payload that doesn't match the expected
 * debug.json shape passes through unchanged.
 */
export function compactPayload(payload: unknown): unknown {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return payload;
  const p = payload as Record<string, unknown>;
  const out: Record<string, unknown> = { ...p, perfHud: null };

  const buildings = p.buildings;
  if (buildings && typeof buildings === 'object' && !Array.isArray(buildings)) {
    out.buildings = { ...(buildings as Record<string, unknown>), list: [] };
  }

  const sim = p.sim;
  if (sim && typeof sim === 'object' && !Array.isArray(sim)) {
    const s = sim as Record<string, unknown>;
    const roadConnectivity =
      s.roadConnectivity && typeof s.roadConnectivity === 'object' && !Array.isArray(s.roadConnectivity)
        ? { ...(s.roadConnectivity as Record<string, unknown>), connectedRoadTiles: [] }
        : s.roadConnectivity;
    out.sim = { ...s, roadMonitors: [], roadConnectivity };
  }

  return out;
}

/** Serialize a candidate queue and report whether it fits QUEUE_BYTE_BUDGET. */
function fitsBudget(q: QueuedCommit[], budget: number): boolean {
  return byteLength(JSON.stringify(q)) <= budget;
}

/** Drop OLDEST entries (never the newest) until the queue fits the byte budget. */
function evictToByteBudget(q: QueuedCommit[], budget: number): QueuedCommit[] {
  let out = q;
  while (out.length > 1 && !fitsBudget(out, budget)) {
    out = out.slice(1);
  }
  return out;
}

/** Outcome of an enqueueCommit call — the caller (backend.ts) decides how to surface it (GR#1). */
export interface EnqueueOutcome {
  /** Queue length AFTER this call. Unchanged from before the call when ok is false. */
  length: number;
  /** False when the entry could not be persisted at all. Never throws either way. */
  ok: boolean;
  /** True: this entry alone (even compacted) exceeded QUEUE_BYTE_BUDGET and was dropped (MET-V854). */
  droppedOversize?: boolean;
  /** True: storage.setItem hit quota; recovered by evicting the oldest half and retrying once. */
  quotaRecovered?: boolean;
  /** BUG-691 F6: true when `payload` itself could not be JSON.stringify'd (e.g.
   * a circular reference). This module's doc comment promises callers "never
   * throws" -- the round proved that false (the un-guarded
   * `JSON.stringify(entry)` below escaped straight to the caller). Callers
   * (backend.ts) should already screen for this before ever reaching here
   * (a payload defect is not a queue-capacity problem), but this flag keeps
   * the promise true even for a caller that does not. */
  unserializable?: boolean;
}

/** Best-effort JSON.stringify that reports failure instead of throwing
 * (BUG-691 F6: a circular/unserializable value must degrade to `null`, never
 * escape to the caller). */
function safeStringify(v: unknown): { text: string | null; ok: boolean } {
  try {
    return { text: JSON.stringify(v), ok: true };
  } catch {
    return { text: null, ok: false };
  }
}

/**
 * Append one commit and persist, enforcing QUEUE_CAP (entry count) and
 * QUEUE_BYTE_BUDGET (serialized bytes) — both evict the OLDEST entries first.
 * The payload is compacted (BUG-607) before it is measured or stored.
 * `atIso` is injected so tests are clock-deterministic. Never throws — a
 * QuotaExceededError from storage.setItem degrades to one retry (oldest half
 * dropped) then gives up, reporting failure via the returned EnqueueOutcome.
 */
export function enqueueCommit(
  storage: StorageLike,
  id: string,
  payload: unknown,
  atIso: string
): EnqueueOutcome {
  const entry: QueuedCommit = { id, at: atIso, payload: compactPayload(payload) };
  // BUG-691 F6: this used to be a bare `JSON.stringify(entry)` -- a circular
  // (or otherwise unserializable) payload threw straight out of a function
  // whose own doc comment promises "never throws". Guard it explicitly.
  const measured = safeStringify(entry);
  if (!measured.ok) {
    return { length: pendingCount(storage), ok: false, unserializable: true };
  }
  const entryBytes = byteLength(measured.text as string);

  if (entryBytes > QUEUE_BYTE_BUDGET) {
    // Even compacted, this ONE frame alone blows the whole budget — drop it
    // rather than evict every other queued commit to make room for it.
    return { length: pendingCount(storage), ok: false, droppedOversize: true };
  }

  let q = readQueue(storage);
  q.push(entry);
  q = q.slice(-QUEUE_CAP);
  q = evictToByteBudget(q, QUEUE_BYTE_BUDGET);

  const serializedQ = safeStringify(q);
  if (!serializedQ.ok) {
    // Extremely unlikely given the single-entry check above already passed,
    // but stay consistent with the "never throws" contract regardless.
    return { length: pendingCount(storage), ok: false, unserializable: true };
  }
  const first = safeSetItem(storage, QUEUE_KEY, serializedQ.text as string);
  if (first.ok) return { length: q.length, ok: true };

  if (!first.quota) {
    // Not a recoverable quota condition — give up without throwing; storage
    // still holds whatever it held before this call.
    return { length: pendingCount(storage), ok: false };
  }

  // QuotaExceeded: drop the OLDEST half of the candidate queue (keep the
  // newer ceil(n/2), including the just-appended entry) and retry ONCE.
  const halved = q.slice(Math.floor(q.length / 2));
  const serializedHalved = safeStringify(halved);
  if (!serializedHalved.ok) {
    return { length: pendingCount(storage), ok: false, unserializable: true };
  }
  const second = safeSetItem(storage, QUEUE_KEY, serializedHalved.text as string);
  if (second.ok) return { length: halved.length, ok: true, quotaRecovered: true };

  return { length: pendingCount(storage), ok: false };
}
