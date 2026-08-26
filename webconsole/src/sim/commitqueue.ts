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
// on a 2xx. Until then entries are never removed, only capped at QUEUE_CAP
// (oldest dropped first) to bound storage. Do not change QUEUE_KEY or the
// QueuedCommit shape without a migration — persisted queues in players'
// browsers are written in this exact schema.
// ────────────────────────────────────────────────────────────────────────────

/** localStorage key holding the persisted queue (ASM-453 schema — stable). */
export const QUEUE_KEY = 'metropolis.debugQueue';

/** Max queued commits kept; overflow drops the OLDEST entries first. */
export const QUEUE_CAP = 50;

/** One committed-but-unsent debug.json frame awaiting a backend (ASM-453). */
export interface QueuedCommit {
  /** Commit id (DBG-...), assigned at commit time. */
  id: string;
  /** ISO-8601 wall-clock time of the commit. */
  at: string;
  /** The committed debug.json frame, exactly as shown on screen (WYSIWYG). */
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
 * Append one commit and persist, enforcing QUEUE_CAP (keeps the NEWEST
 * QUEUE_CAP entries). Returns the queue length after the append.
 * `atIso` is injected so tests are clock-deterministic.
 */
export function enqueueCommit(
  storage: StorageLike,
  id: string,
  payload: unknown,
  atIso: string
): number {
  const q = readQueue(storage);
  q.push({ id, at: atIso, payload });
  const trimmed = q.slice(-QUEUE_CAP);
  storage.setItem(QUEUE_KEY, JSON.stringify(trimmed));
  return trimmed.length;
}
