// backend.ts — the browser-facing debug backend shim (FEAT-1972079885/86).
// Error capture (window listeners in main.tsx feed recordError) plus the
// snapshot commit path. The commit QUEUE itself is the pure module
// commitqueue.ts (see its ASM-453 contract note: no backend exists yet, so
// every commit queues client-side in localStorage until one arrives); this
// file only binds it to the real window.localStorage and the real network.

import { enqueueCommit, pendingCount, type StorageLike } from './commitqueue.ts';
import type { SimState } from './types.ts';
// The captured-error record shape is owned by debugjson.ts (CapturedError) so the
// debug.json artefact and the live "Errors captured" panel can never drift. This
// is a TYPE-ONLY import (erased at build/run time), so backend stays lightweight
// and debugjson keeps no runtime dependency on this module.
import type { CapturedError } from './debugjson.ts';

// GR#1/GR#7 (FEAT-1972079898/FEAT-1972079916): full-context error capture with
// registry-sourced codes. Every distinct error is trapped with its message,
// source/type, JS stack, React component stack (the "what triggered it"), a
// registry-sourced error code (MET-xxx), a unique correlation id, app version,
// wall-clock timestamp, and a lightweight snapshot of the sim state at crash
// time ("the heap"). Identical repeats collapse into one record with a count +
// first/last timestamps instead of spamming the list.

/** Where a captured error originated. */
export type ErrorSource =
  | 'window-error'
  | 'unhandledrejection'
  | 'render-crash'
  | 'reset-abort'
  | 'app';

/**
 * Best-effort stringify. Never throws — an evil `toString`/valueOf, a Proxy
 * that throws on every trap, or any other unstringifiable value all fall back
 * to a fixed placeholder rather than escaping to the caller.
 */
function safeStringifyAny(v: unknown): string {
  try {
    return String(v);
  } catch {
    return '<unstringifiable thrown value>';
  }
}

/**
 * Best-effort message extraction from a thrown object. Wrapped in a SINGLE
 * try/catch around the whole property-read + fallback chain: a throwing
 * Proxy 'get' trap, or a plain object with an evil `message`/`toString`,
 * surfaces mid-expression and must not escape (BAR-F2).
 */
function safeMessageFromObject(value: unknown): string {
  try {
    const anyVal = value as any;
    const candidate = anyVal.message || anyVal.error || anyVal.reason || String(anyVal);
    return typeof candidate === 'string' ? candidate : safeStringifyAny(candidate);
  } catch {
    return '<unstringifiable thrown value> (object)';
  }
}

/**
 * Normalize any thrown value (Error, string, null, undefined, etc.) to an
 * Error-shaped record with {message, stack} (or fallbacks). This is BUG-442:
 * componentDidCatch and window error handlers can receive non-Error values
 * (strings, undefined, null, objects without .message), and attempting to
 * read .message on non-Error triggers a crash in the crash handler itself.
 *
 * BAR-F2 (round-r1): this function must NEVER throw, even when the thrown
 * value fights back — an evil `toString()`, a Proxy that throws on every
 * property access, etc. Every stringification/property-read is guarded, and
 * an outer try/catch is the absolute last resort.
 */
export function normalizeThrowable(value: unknown): Error & { type?: string } {
  try {
    // Case 1: already an Error — pass through. instanceof itself is wrapped:
    // a Proxy overriding Symbol.hasInstance could throw.
    try {
      if (value instanceof Error) {
        return value;
      }
    } catch {
      // fall through — treat as a non-Error value below
    }

    // Case 2: a string — wrap in an Error to preserve stack context
    if (typeof value === 'string') {
      const err = new Error(value);
      (err as any).type = 'string';
      return err;
    }

    // Case 3: null — special case (typeof null === 'object' in JS!)
    if (value === null) {
      const err = new Error('null');
      (err as any).type = 'null';
      return err;
    }

    // Case 4: undefined — stringified + type tag
    if (value === undefined) {
      const err = new Error('undefined');
      (err as any).type = 'undefined';
      return err;
    }

    // Case 5: an object (dict) without message — try to extract message or stringify
    if (typeof value === 'object') {
      const msg = safeMessageFromObject(value);
      const err = new Error(msg);
      (err as any).type = 'object';
      return err;
    }

    // Case 6: fallback for numbers, booleans, symbols, etc.
    const err = new Error(safeStringifyAny(value));
    (err as any).type = typeof value;
    return err;
  } catch {
    // Absolute last resort: something in the normalization path itself threw.
    // BAR-F2: this function must never throw back to the caller.
    let typeTag = 'unknown';
    try {
      typeTag = typeof value;
    } catch {
      /* typeof cannot throw in practice, but stay defensive */
    }
    const err = new Error(`<unstringifiable thrown value> (${typeTag})`);
    (err as any).type = typeTag;
    return err;
  }
}

/**
 * BAR-F1 (round-r1): build an Error that carries a real registry-sourced
 * error code (MET-xxxx) as a `.code` property, so the record path (backend +
 * ErrorBoundary) can use the NAMED code instead of falling back to the
 * per-channel generic code.
 */
export function codedError(code: string, message: string): Error & { code: string } {
  const err = new Error(message) as Error & { code: string };
  err.code = code;
  return err;
}

/**
 * Lightweight, BOUNDED summary of the sim state at crash time — the "heap".
 * Deliberately NOT the full 2 MB debug JSON: just the headline numbers plus the
 * (small) policy flag record, snapshotted by value so it cannot pin or mutate
 * with the live state.
 */
export interface StateSummary {
  tick: number;
  funds: number;
  population: number;
  speed: number;
  policies: Record<string, boolean>;
}

/** Optional structured context a caller can attach to an error. */
export interface RecordErrorMeta {
  type?: ErrorSource;
  /** JS stack (error.stack). */
  stack?: string;
  /** React component tree at the crash (errorInfo.componentStack). */
  componentStack?: string;
  /** Optional action/context label (e.g. the reducer action that aborted). */
  action?: string;
  /** Registry-sourced error code (MET-xxxx). If not provided, a generic trap-path code is assigned. */
  code?: string;
  /** BAR-F5 (round-r1): true when this is a cascade error (a failed cleanup/
   * sibling crash following a first render error), never the root cause. */
  cascade?: boolean;
  /** BAR-F5: correlation id of the FIRST error this cascade followed, linking
   * the cascade row back to the root-cause row in the ring/debug.json. */
  firstCorrelationId?: number;
}

/** One captured, de-duplicated error record. Same shape as debugjson.CapturedError. */
export type ErrorRecord = CapturedError & {
  code?: string;
  appVersion?: string;
  timestamp?: number;
}

/** Internal store row: the public record plus the dedup key (never serialized). */
type StoredError = ErrorRecord & { dedupeKey: string };

/** Cap for the persistent error ring in localStorage (roughly 100 entries). */
export const ERROR_RING_CAP = 100;

/** localStorage key holding the persistent error ring. */
const ERROR_RING_STORAGE_KEY = 'metropolis.errorRing';

// FEAT-1972079916 BAR-F3: real app version, WITHOUT the `require('./version.ts')`
// hack (which never actually worked under ESM and silently fell to 'unknown'
// every time). version.ts's OWN import (`from '../generated/version'`, no
// extension) only resolves under a bundler/tsx resolver, not under a plain
// `node --test` run of the .mjs suite — so backend.ts, which the plain-node
// suite loads directly, must not statically import version.ts itself (that
// would break test/*.test.mjs at module-load time for every test in the file,
// not just the version ones). This mirrors the EXISTING liveVersionRef.ts
// pattern in this codebase (BUG-424): a module-level ref that imports NOTHING,
// set by a real static import one layer up (main.tsx, which real bundlers/tsx
// resolve fine) via setAppVersion(). 'unknown' is reserved for the genuinely
// absent case (setAppVersion never called yet), never hit on the normal path
// once main.tsx has wired it.
let liveAppVersion: string | undefined;

/** Called once from main.tsx with the real, build-time git-derived version. */
export function setAppVersion(v: string): void {
  if (typeof v === 'string' && v.length > 0) liveAppVersion = v;
}

function getAppVersion(): string {
  return liveAppVersion || 'unknown';
}

const errorLog: StoredError[] = [];
let nextCorrelationId = 1;

// The "heap" ref: the store updates this with the latest sim state so recordError
// can attach a snapshot at crash time WITHOUT the sim/state layer knowing anything
// about error capture. Bounded — only the headline summary is retained by value.
let lastKnownState: StateSummary | null = null;

/**
 * Store hook (minimal touch): record a bounded snapshot of the current sim state
 * so a subsequent recordError() can attach "the heap" at crash time. Called from
 * the SimProvider on every state change. This lives in the error ENVELOPE, not in
 * SimState — no Date.now/Math.random leaks into the reducer.
 */
export function updateLastKnownState(s: SimState): void {
  lastKnownState = {
    tick: s.tick,
    funds: s.funds,
    population: s.population,
    speed: s.speed,
    policies: { ...s.policies },
  };
}

/** Deduplication key: identical message + component stack collapse into one row. */
function dedupeKey(msg: string, componentStack?: string): string {
  return msg + '\u0000' + (componentStack ?? '');
}

/**
 * Trap an error with full context (GR#1/GR#7). Backward-compatible:
 * `recordError(msg)` still works (type defaults to 'app', no stack). Identical
 * repeats (same message + component stack) collapse into the existing record,
 * bumping `count` and `lastAt` while preserving the first occurrence's stack,
 * correlation id, and captured heap. Persists to localStorage ring buffer for
 * cross-reload continuity. Returns the recorded error's details for display.
 */
export function recordError(msg: string, meta?: RecordErrorMeta): { correlationId: number; code?: string; tick?: number } {
  const now = Date.now();
  const componentStack = meta?.componentStack;
  const key = dedupeKey(msg, componentStack);

  const existing = errorLog.find((e) => e.dedupeKey === key);
  if (existing) {
    existing.count += 1;
    existing.lastAt = now;
    persistErrorRing();
    return { correlationId: existing.correlationId, code: existing.code, tick: existing.stateSummary?.tick };
  }

  const correlationId = nextCorrelationId++;
  const stateSummary = lastKnownState ? { ...lastKnownState, policies: { ...lastKnownState.policies } } : null;
  const record: StoredError = {
    correlationId,
    type: meta?.type ?? 'app',
    msg,
    stack: meta?.stack,
    componentStack,
    action: meta?.action,
    code: meta?.code,
    appVersion: meta?.code ? getAppVersion() : undefined, // only set if a code was provided
    timestamp: now,
    firstAt: now,
    lastAt: now,
    count: 1,
    stateSummary,
    cascade: meta?.cascade,
    firstCorrelationId: meta?.firstCorrelationId,
    dedupeKey: key,
  };
  errorLog.unshift(record);
  // BAR-F4: real cap — ERROR_RING_CAP, not a hardcoded literal.
  if (errorLog.length > ERROR_RING_CAP) errorLog.pop();
  persistErrorRing();
  return { correlationId, code: meta?.code, tick: stateSummary?.tick };
}

/**
 * Persist the in-memory error ring to localStorage. If the write fails
 * (QuotaExceeded, private mode, etc.), record the failure as its own
 * registry-coded error so it doesn't silently disappear.
 */
function persistErrorRing(): void {
  try {
    const storage = queueStorage();
    const ring = recentErrors().map((e) => ({
      msg: e.msg,
      type: e.type,
      correlationId: e.correlationId,
      code: e.code,
      appVersion: e.appVersion,
      timestamp: e.timestamp,
      firstAt: e.firstAt,
      lastAt: e.lastAt,
      count: e.count,
      stack: e.stack,
      componentStack: e.componentStack,
      stateSummary: e.stateSummary,
      cascade: e.cascade,
      firstCorrelationId: e.firstCorrelationId,
    }));
    storage.setItem(ERROR_RING_STORAGE_KEY, JSON.stringify(ring));
  } catch (e) {
    // GR#17: a failed log write is itself an error that must surface.
    // We record it in-memory only to avoid infinite recursion.
    const msg = `Error log write failed: ${String(e).slice(0, 100)}`;
    const inMemRecord: StoredError = {
      correlationId: nextCorrelationId++,
      type: 'app',
      msg,
      code: 'MET-V805', // ErrorLogWriteFailed
      appVersion: getAppVersion(),
      timestamp: Date.now(),
      firstAt: Date.now(),
      lastAt: Date.now(),
      count: 1,
      stateSummary: lastKnownState ? { ...lastKnownState, policies: { ...lastKnownState.policies } } : null,
      dedupeKey: msg,
    };
    errorLog.unshift(inMemRecord);
    // BAR-F4: real cap here too — this is a second cap site the round found hardcoded.
    if (errorLog.length > ERROR_RING_CAP) errorLog.pop();
  }
}

/**
 * Hydrate the in-memory error ring from localStorage on startup.
 */
function hydrateErrorRing(): void {
  try {
    const storage = queueStorage();
    const raw = storage.getItem(ERROR_RING_STORAGE_KEY);
    if (!raw) return;
    const ring = JSON.parse(raw) as Array<any>;
    if (!Array.isArray(ring)) return;
    // Restore records, newest-first
    for (const entry of ring) {
      const record: StoredError = {
        msg: entry.msg,
        type: entry.type ?? 'app',
        correlationId: entry.correlationId ?? nextCorrelationId++,
        code: entry.code,
        appVersion: entry.appVersion,
        timestamp: entry.timestamp,
        firstAt: entry.firstAt,
        lastAt: entry.lastAt,
        count: entry.count ?? 1,
        stack: entry.stack,
        componentStack: entry.componentStack,
        stateSummary: entry.stateSummary,
        cascade: entry.cascade,
        firstCorrelationId: entry.firstCorrelationId,
        dedupeKey: dedupeKey(entry.msg, entry.componentStack),
      };
      errorLog.push(record);
    }
  } catch {
    // Silently drop corruption; the ring is best-effort for persistence.
  }
}

// Hydrate on module load
hydrateErrorRing();

export function recentErrors(): ErrorRecord[] {
  // Strip the internal dedupe key so the serialized debug.json shape stays clean.
  return errorLog.map(({ dedupeKey: _k, ...rest }) => ({ ...rest }));
}

/**
 * window 'error' handler (extracted so it is unit-testable without a DOM). Traps
 * the message AND the JS stack that the bare listener previously dropped.
 * Normalizes non-Error values to prevent the error handler itself from crashing.
 */
export function reportWindowError(e: { message?: string; error?: unknown | null }): void {
  // Normalize the error payload in case it's not a real Error
  const normalized = e.error ? normalizeThrowable(e.error) : null;
  recordError(e.message || normalized?.message || 'unknown error', {
    type: 'window-error',
    stack: normalized?.stack,
    // BAR-F1: a NAMED code on the thrown value (codedError) wins; MET-V802 is
    // only the generic fallback for this channel.
    code: (normalized as any)?.code ?? 'MET-V802', // WindowError
  });
}

/**
 * window 'unhandledrejection' handler (extracted, DOM-free). Traps the rejection
 * reason's stack, not just its stringification. Normalizes the reason to handle
 * non-Error rejections.
 */
export function reportUnhandledRejection(reason: unknown): void {
  const normalized = normalizeThrowable(reason);
  recordError(`unhandled rejection: ${normalized.message.slice(0, 200)}`, {
    type: 'unhandledrejection',
    stack: normalized.stack,
    code: (normalized as any).code ?? 'MET-V803', // UnhandledRejection
  });
}

/**
 * console.error tap handler. Intercepts console.error calls to record them
 * in the error ring without disrupting normal logging. We only tap the first
 * error parameter to avoid recording the same error twice.
 */
export function tapConsoleError(...args: unknown[]): void {
  if (args.length === 0) return;
  const first = args[0];
  const normalized = normalizeThrowable(first);
  recordError(normalized.message, {
    type: 'app',
    stack: normalized.stack,
    code: (normalized as any).code ?? 'MET-V804', // ConsoleErrorTrap
  });
}

// BAR-F6 (round-r1): "no second root" — the boundary's own diagnostic
// console.error (logging the crash it just recorded) must not re-enter the
// tap and mint a SECOND ring row (a generic MET-V804) for the same incident.
// A module-level suppression flag, toggled only while framework-internal code
// runs its own diagnostic console.error, lets installConsoleTap's wrapper skip
// tapConsoleError for exactly that call without touching the real console.error.
let tapSuppressed = false;

/**
 * Run `fn` with the console-error tap suppressed: the wrapped console.error
 * installed by installConsoleTap still calls the ORIGINAL console.error (so
 * the message still reaches devtools/CI logs), it just skips recording it a
 * second time into the error ring.
 */
export function withTapSuppressed<T>(fn: () => T): T {
  const prev = tapSuppressed;
  tapSuppressed = true;
  try {
    return fn();
  } finally {
    tapSuppressed = prev;
  }
}

/**
 * BAR-K4b (round-r1): install the console.error monkey-patch on a real console
 * object. Extracted out of main.tsx so the ACTUAL patch installation is unit
 * testable (against a fake console) rather than only reachable by importing
 * main.tsx, which is impractical in a test harness.
 */
export function installConsoleTap(consoleObj: Pick<Console, 'error'>): void {
  const original = consoleObj.error.bind(consoleObj);
  consoleObj.error = function (...args: unknown[]) {
    original(...args);
    if (!tapSuppressed) {
      tapConsoleError(...args);
    }
  };
}

/** One row of the DebugTab "Errors captured" list — now carries the full context. */
export interface ErrorRow {
  /** Local wall-clock time the error was last seen. */
  time: string;
  msg: string;
  type: ErrorSource;
  /** Registry-sourced error code (MET-xxxx), GR#1 pillar-4 selectable display.
   * Undefined for older ring entries recorded before codes were captured (BUG-513). */
  code?: string;
  correlationId: number;
  /** Occurrences collapsed into this row (>= 1). */
  count: number;
  firstTime: string;
  lastTime: string;
  stack?: string;
  componentStack?: string;
  stateSummary: StateSummary | null;
}

/**
 * Pure presentation model for the "Errors captured" display: `empty` selects
 * the "No errors captured this session." hint; otherwise `rows` render
 * newest-first (recordError unshifts) with their full context so each row can
 * expand its stack, component stack (the trigger), and state summary.
 */
export function errorListModel(errors: readonly ErrorRecord[]): {
  empty: boolean;
  rows: ErrorRow[];
} {
  return {
    empty: errors.length === 0,
    rows: errors.map((e, i) => {
      // Defensive: tolerate a legacy {at, msg} row (pre-FEAT-1972079898 shape)
      // so any historical caller still renders instead of showing "Invalid Date".
      const legacy = e as ErrorRecord & { at?: number };
      const lastAt = e.lastAt ?? legacy.at ?? 0;
      const firstAt = e.firstAt ?? legacy.at ?? lastAt;
      return {
        time: new Date(lastAt).toLocaleTimeString(),
        msg: e.msg,
        type: e.type ?? 'app',
        code: e.code,
        correlationId: e.correlationId ?? i,
        count: e.count ?? 1,
        firstTime: new Date(firstAt).toLocaleTimeString(),
        lastTime: new Date(lastAt).toLocaleTimeString(),
        stack: e.stack,
        componentStack: e.componentStack,
        stateSummary: e.stateSummary ?? null,
      };
    }),
  };
}

export interface CommitResult {
  ok: boolean;
  queued: boolean;
  id: string;
  message: string;
}

// Fail-safe storage binding: in any context without localStorage (or where
// touching it throws) fall back to a volatile in-memory store — the Debug
// tab must keep working, just without cross-reload persistence.
const memoryStore = new Map<string, string>();
const memoryStorage: StorageLike = {
  getItem: (k) => memoryStore.get(k) ?? null,
  setItem: (k, v) => void memoryStore.set(k, v),
};

function queueStorage(): StorageLike {
  try {
    if (typeof localStorage !== 'undefined') return localStorage;
  } catch {
    /* fall through to memory */
  }
  return memoryStorage;
}

export function pendingCommits(): number {
  return pendingCount(queueStorage());
}

export async function commitDebug(payload: unknown): Promise<CommitResult> {
  const id = `DBG-${Date.now().toString(36).toUpperCase()}`;
  try {
    const res = await fetch('/api/debug/commit', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id, at: new Date().toISOString(), payload }),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return { ok: true, queued: false, id, message: `Committed to backend as ${id}` };
  } catch {
    // ASM-453: no backend exists — queue locally and report honestly.
    const n = enqueueCommit(queueStorage(), id, payload, new Date().toISOString());
    return {
      ok: true,
      queued: true,
      id,
      message: `Backend unreachable — queued locally as ${id} (${n} awaiting processing)`,
    };
  }
}
