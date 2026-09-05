// backend.ts — the browser-facing debug backend shim (FEAT-1972079885/86).
// Error capture (window listeners in main.tsx feed recordError) plus the
// snapshot commit path. The commit QUEUE itself is the pure module
// commitqueue.ts (see its ASM-453 contract note: no backend exists yet, so
// every commit queues client-side in localStorage until one arrives); this
// file only binds it to the real window.localStorage and the real network.

import { enqueueCommit, pendingCount, readQueue, writeQueue, QUEUE_BYTE_BUDGET, type StorageLike } from './commitqueue.ts';
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

// FEAT-2326609752 (AARON Q100078=B, 2026-09-03): the metro MariaDB debug
// sink (tools/debugsink/server.js), a tiny localhost-only Node HTTP server.
// Fixed port, documented alongside the server itself. An absolute
// 127.0.0.1 URL is used (rather than a same-origin path through a vite
// proxy) so the sink works identically whether the app is served by `vite
// dev`, `vite preview`, or any other static host — the sink is CORS-open
// (Access-Control-Allow-Origin: *) precisely because it never leaves the
// loopback interface, so no proxy is required for this to work cleanly.
const DEBUGSINK_URL = 'http://127.0.0.1:8642';

// Short timeout (ASM-453 / this FEAT): the sink is either running on the
// developer's own machine (near-instant) or not running at all (nothing
// listening on 127.0.0.1:8642) — a slow success is not an expected shape,
// so 1.5s is generous headroom before falling back to the always-reliable
// local queue rather than stalling the Commit button.
const DEBUGSINK_TIMEOUT_MS = 1500;

// BUG-703 (Aaron/round F8, 2026-09-04, "a wrong service answering 200 loses
// debug frames behind a green indicator"): `res.ok` alone used to be treated
// as proof the frame was durably sunk into the metro MariaDB debug sink.
// ANY listener on 127.0.0.1:8642 -- a stray dev tool, a misconfigured proxy,
// even a generic "It works!" placeholder page some HTTP server serves by
// default -- answering plain 200 was indistinguishable from the real sink,
// and the frame was destroyed locally on the strength of that alone. The
// real sink (tools/debugsink/server.js) now echoes a small, verifiable JSON
// ack on every successful commit: `{ ok: true, sink: SINK_NAME, id }`. This
// name MUST match tools/debugsink/server.js's own SINK_NAME constant exactly
// -- there is deliberately no shared module between the Go^H^H^HNode sink and
// this browser bundle (loopback-only local tool, no build-time coupling), so
// both sides carry the literal string and the pinned attack test
// (attack-bug691-sink-silence.test.mjs) is what keeps them honest.
const SINK_NAME = 'metropolis-debugsink';

/** The shape this module trusts from a 2xx /api/debug/commit response, after
 * GR#16 safe coercion of the untrusted response body -- never assume the
 * JSON matches this shape just because the HTTP status was 2xx. */
interface SinkAck {
  sink: unknown;
  id: unknown;
}

/** GR#16: coerce an arbitrary parsed-JSON value into the two fields this
 * module actually reads, without ever trusting its declared shape. A
 * non-object body (string/number/array/null) yields two `undefined` fields,
 * which fails ack validation exactly like a missing ack should. */
function coerceAck(raw: unknown): SinkAck {
  if (raw !== null && typeof raw === 'object' && !Array.isArray(raw)) {
    const o = raw as Record<string, unknown>;
    return { sink: o.sink, id: o.id };
  }
  return { sink: undefined, id: undefined };
}

/** Outcome of one POST attempt to the sink. */
interface SinkPostResult {
  /** True ONLY when the response was a 2xx AND carried a verifiable ack
   * (the correct sink identity, echoing the exact frame id sent) -- the
   * frame may be safely discarded locally only when this is true. */
  sunk: boolean;
  /** True when a 2xx response came back but failed ack validation -- BUG-703's
   * "wrong service on the port" case, distinct from no response at all
   * (network error/timeout/non-2xx) so the two get separate registry codes
   * and the player can tell "nothing is listening" from "something else is". */
  invalidAck: boolean;
}

/**
 * POST one commit to the sink with a short timeout. `sunk` is true only when
 * the response is a 2xx carrying a verified ack (BUG-703) -- never throws, so
 * callers never need their own try/catch around this.
 */
async function postToSink(id: string, at: string, payload: unknown): Promise<SinkPostResult> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), DEBUGSINK_TIMEOUT_MS);
  try {
    const res = await fetch(`${DEBUGSINK_URL}/api/debug/commit`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id, at, payload }),
      signal: controller.signal,
    });
    if (!res.ok) return { sunk: false, invalidAck: false };
    let rawAck: unknown = null;
    try {
      rawAck = await res.json();
    } catch {
      rawAck = null; // GR#16: an unparsable body is an invalid ack, not a crash
    }
    const ack = coerceAck(rawAck);
    const valid = ack.sink === SINK_NAME && ack.id === id;
    return { sunk: valid, invalidAck: !valid };
  } catch {
    return { sunk: false, invalidAck: false };
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Opportunistic drain (FEAT-2326609752): on a successful live commit, sweep
 * whatever the localStorage queue is still holding from before the sink was
 * reachable (or from a earlier session run with no sink at all) into the
 * sink too. Oldest-first (array order IS commit order — commitqueue.ts's
 * ASM-453 contract), and an entry is removed from the persisted queue ONLY
 * after its own 2xx — a failure partway through (sink drops mid-drain)
 * leaves the remaining entries queued exactly as before, never lost and
 * never double-counted. Never throws: this runs opportunistically inside
 * an already-successful commit and must not turn that success into a
 * reported failure.
 *
 * REJECT-round fix (F1, 2026-09-03): the FIRST version of this function read
 * the queue ONCE up front and, after every await'd POST, wrote back a
 * filtered copy of that same stale snapshot. `readQueue`/`postToSink` both
 * cross an await boundary, and JS only guarantees atomicity BETWEEN await
 * points — so a commit enqueued (by another commitDebug call, e.g. a second
 * debug-tab commit fired while this drain's POST was still in flight) after
 * the snapshot was taken but before writeQueue ran was silently clobbered:
 * the stale snapshot's filtered copy simply didn't contain it, and the write
 * overwrote the newer on-disk queue with that shorter list. Fixed by never
 * writing from a pre-await snapshot: the LOOP ORDER still comes from a
 * snapshot of ids (drain order must not shift underfoot while iterating),
 * but every actual write is a synchronous read-filter-write of the CURRENT
 * on-disk queue, done immediately after the entry's own POST resolves with
 * no further await in between — single-threaded JS makes that block atomic
 * with respect to any other synchronous code, and the only other writer
 * (another commitDebug/drainQueue call) can only interleave AT an await,
 * never inside this synchronous span. See the regression test
 * "commitDebug: a commit enqueued mid-drain (between an await resolving and
 * the write) survives the drain" in test/debugtab.test.mjs.
 *
 * REJECT-round fix (F2/F3, 2026-09-04): two further findings against this
 * same function.
 *   F2 — the removal step used to be `filter(e => e.id !== entry.id)`, which
 *        deletes EVERY entry sharing that id. Two distinct queued commits
 *        that collided on id (F1) meant POSTing the first successfully wiped
 *        BOTH off disk — the second was never sunk and never queued again,
 *        pure data loss behind an all-clear "0 pending" reading. F1's
 *        collision-free id minting (backend.ts's commitId counter) means
 *        this should no longer happen in practice, but the removal here is
 *        now index-based and removes AT MOST ONE matching entry regardless —
 *        a future id collision (or a legacy persisted queue from before the
 *        F1 fix) can never delete more than the one entry that was actually
 *        just confirmed sunk.
 *   F3 — this function used to be silent on its OWN postToSink failures: a
 *        sink that answers the live commit but dies mid-drain left the
 *        stranded entries queued with NOTHING recorded and (worse)
 *        commitDebug had already cleared the "sink down" indicator before
 *        this ran. Every drain failure now records its own MET-V857 row and
 *        re-stamps the module-level unreachable timestamp; the return value
 *        tells commitDebug whether the drain was fully clean so it can
 *        decide whether clearing the indicator is honest.
 */
async function drainQueue(): Promise<boolean> {
  let allClean = true;
  try {
    const storage = queueStorage();
    // Snapshot for ITERATION ORDER + the (id, at, payload) to re-POST only —
    // never written back directly (see the fix note above).
    const toDrain = readQueue(storage);
    if (toDrain.length === 0) return true;
    for (const entry of toDrain) {
      const result = await postToSink(entry.id, entry.at, entry.payload);
      if (!result.sunk) {
        // F3: a drain failure is a sink-down event too — never silent, and
        // the indicator must reflect it (re-stamped, not left cleared by
        // whatever set it null before this drain started).
        allClean = false;
        sinkLastUnreachableAt = Date.now();
        // BUG-703: a 2xx that fails ack validation is a DIFFERENT failure
        // mode from no response at all (some other service is answering on
        // the port) — distinct registry code so the two are never conflated.
        if (result.invalidAck) {
          recordError(
            `Debug sink at ${DEBUGSINK_URL} answered with a 2xx but not a valid ack (expected sink=${SINK_NAME} id=${entry.id}) -- a different service may be listening on 127.0.0.1:8642; commit remains queued locally instead of being trusted as sunk (drain retry)`,
            { type: 'app', action: `drain ${entry.id}`, code: 'MET-V867' },
          );
        } else {
          recordError(
            `Debug sink unreachable at ${DEBUGSINK_URL} -- commit remains queued locally instead of reaching the metro MariaDB debug sink (drain retry failed)`,
            { type: 'app', action: `drain ${entry.id}`, code: 'MET-V857' },
          );
        }
        continue; // leave this one queued; try the rest in case only this entry is problematic
      }
      // Synchronous read-filter-write, no await inside this block: atomic
      // with respect to any other code that only ever mutates the queue via
      // enqueueCommit/writeQueue (both synchronous). Always derives from the
      // CURRENT on-disk queue, so a commit enqueued while the POST above was
      // in flight is present in `current` and survives untouched.
      const current = readQueue(storage);
      // F2: remove AT MOST ONE entry (the first matching this id), never a
      // blanket filter — a colliding id must not delete a sibling entry that
      // has not itself been confirmed sunk.
      const idx = current.findIndex((e) => e.id === entry.id);
      if (idx !== -1) {
        const next = current.slice();
        next.splice(idx, 1);
        writeQueue(storage, next);
      }
    }
  } catch {
    // Best-effort only — the queue is the source of truth on disk and this
    // function never removes an entry it didn't confirm was sunk.
    allClean = false;
  }
  return allClean;
}

// BUG-691 (Aaron, 2026-09-04, "this should be an error that is trapped"): the
// sink's last-known reachability, tracked at module scope so the Debug tab can
// show an HONEST current status on every mount/refresh — not just the local
// `status` state in debugTab.tsx, which is a one-shot string that resets to
// "no commit this session" the instant the tab remounts or ANOTHER commit
// runs, and which nobody sees at all if the Debug tab isn't open when the
// sink actually goes down. This is the GR#1 "selectable display" pillar for
// sink health: `debugSinkStatus()` lets any caller ask the current state
// without re-attempting a network call.
//
// REJECT-round fix (F4, 2026-09-04): the comment above overclaimed "an HONEST
// current status on every mount/refresh" — module state does NOT survive a
// real page reload (a fresh module instance starts at `null` regardless of
// what the persisted queue holds), so a player reopening a session with the
// sink still down and a non-empty queue used to see a false-healthy chip.
// This is module state ACROSS COMPONENT REMOUNTS within one page load only;
// across a reload it is reseeded below from the one signal that DOES
// survive — the persisted queue itself. A non-empty queue at module-init
// time means the last thing that happened was an unreached sink (nothing
// else leaves entries queued), so seed "unreachable" from that rather than
// from a timestamp we have no way to recover.
let sinkLastUnreachableAt: number | null = pendingCommits() > 0 ? Date.now() : null;

/** BUG-691: current debug-sink reachability, derived from the most recent
 * commitDebug attempt (there is no separate polling — the sink is only ever
 * probed when the player actually commits, so "reachable" here means "the
 * last attempt succeeded", not "definitely up right now").
 *
 * F5 fix (2026-09-04): `sinceMs` used to return the raw absolute epoch
 * timestamp `sinkLastUnreachableAt` under a name ("since-Ms") that reads as
 * an ELAPSED duration — any consumer rendering "down for {sinceMs}ms" would
 * have printed ~1.7e12 (roughly 55,000 years). It is now genuinely elapsed
 * milliseconds since the outage was first noticed. */
export function debugSinkStatus(): { unreachable: boolean; sinceMs: number | null } {
  if (sinkLastUnreachableAt === null) return { unreachable: false, sinceMs: null };
  return { unreachable: true, sinceMs: Date.now() - sinkLastUnreachableAt };
}

// BUG-691 F1 (2026-09-04): the commit id used to be minted as
// `DBG-${Date.now().toString(36)}` alone — 1ms resolution, so two commits
// fired in the same millisecond (trivially reachable on a fast machine, or
// two debug-tab clicks close together) got the IDENTICAL id. That collision
// was the root cause behind two separate defects: the id-bearing MET-V857
// message deduping a second genuine failure into a mere count bump (see the
// message change below), and drainQueue's old id-keyed filter deleting BOTH
// colliding queue entries after sinking only one (F2, commitqueue.ts/this
// file's drainQueue). A monotonic per-module counter suffix makes the id
// collision-free regardless of how many commits land in the same
// millisecond, for the life of this module instance.
let commitIdCounter = 0;
function mintCommitId(): string {
  commitIdCounter += 1;
  return `DBG-${Date.now().toString(36).toUpperCase()}-${commitIdCounter.toString(36).toUpperCase()}`;
}

export async function commitDebug(payload: unknown): Promise<CommitResult> {
  const id = mintCommitId();

  // BUG-691 F6 (2026-09-04): screen for an unserializable payload BEFORE
  // touching the network or the sink-health indicator. The round proved that
  // without this guard, a circular payload's JSON.stringify throw happened
  // deep inside enqueueCommit (reached only after postToSink's own throw was
  // already swallowed as "unreachable") -- so a PAYLOAD defect got blamed on
  // the NETWORK (a healthy sink was marked down), and commitDebug rejected
  // outright despite enqueueCommit's "never throws" contract. This is a
  // defect in what was captured, not in the sink or the queue, so it gets
  // its own registry code and never touches sinkLastUnreachableAt.
  try {
    JSON.stringify(payload);
  } catch (e) {
    const reason = e instanceof Error ? e.message : safeStringifyAny(e);
    recordError(`Debug commit ${id} payload cannot be serialized: ${reason}`, {
      type: 'app',
      action: `commit ${id}`,
      code: 'MET-V864',
    });
    return {
      ok: false,
      queued: false,
      id,
      message: `Debug commit ${id} was NOT sent or queued -- its payload cannot be serialized (${reason}). This is a captured-data defect, not a network/sink problem; the sink status is unaffected.`,
    };
  }

  const at = new Date().toISOString();
  const sinkResult = await postToSink(id, at, payload);
  if (sinkResult.sunk) {
    // BUG-691 F3 (2026-09-04): this used to clear sinkLastUnreachableAt
    // BEFORE awaiting drainQueue -- a sink that answers the live commit but
    // then dies mid-drain left the indicator showing healthy (and recorded
    // nothing) while entries stayed silently stranded on the queue. Only
    // clear once the drain itself confirms every stranded entry was ALSO
    // sunk; drainQueue re-stamps/records its own failures otherwise.
    const drainedClean = await drainQueue();
    if (drainedClean) sinkLastUnreachableAt = null;
    return { ok: true, queued: false, id, message: `Committed to metro MariaDB debug sink as ${id}` };
  }
  // ASM-453: sink unreachable/timed out — queue locally and report honestly.
  // BUG-691: this used to fall straight into the "queue locally" branch below
  // with NOTHING recorded to the registry/error ring — a queued-and-later-
  // drained commit is not data loss, but a downed sink is exactly the kind of
  // silent degradation GR#1/GR#17 exist to catch (Aaron: "the sink died
  // tonight and commits vanished with no registry error or UI signal"). Record
  // it EVERY time the sink is unreachable, regardless of whether the local
  // queue write below succeeds or fails — those are separate, additional
  // failure modes (MET-V854/V855) layered on top of this one.
  //
  // F1 fix: the id is no longer embedded in this MESSAGE — dedupeKey is
  // msg+componentStack, so an id-bearing message forced every distinct
  // outage attempt into its own row (fine on its own) but MASKED an id
  // collision (F1's actual bug) by making two genuinely different failures
  // look identical only when their ids happened to clash. A stable message
  // lets recordError's normal dedup do its job (repeats bump `count`,
  // exactly as designed for a recurring identical-cause failure); the
  // per-commit id is still captured, just in `action` where it cannot affect
  // dedup.
  sinkLastUnreachableAt = Date.now();
  // BUG-703: a 2xx that fails ack validation (some other service answering
  // on 127.0.0.1:8642) is recorded under a DISTINCT code from "nothing
  // answered at all" -- both leave the frame queued and the indicator
  // showing the same warning state, but only the invalid-ack case points at
  // a wrong-service collision rather than a simply-not-running sink.
  if (sinkResult.invalidAck) {
    recordError(
      `Debug sink at ${DEBUGSINK_URL} answered with a 2xx but not a valid ack (expected sink=${SINK_NAME} id=${id}) -- a different service may be listening on 127.0.0.1:8642; commit queued locally instead of being trusted as sunk`,
      { type: 'app', action: `commit ${id}`, code: 'MET-V867' },
    );
  } else {
    recordError(
      `Debug sink unreachable at ${DEBUGSINK_URL} -- commit queued locally instead of reaching the metro MariaDB debug sink`,
      { type: 'app', action: `commit ${id}`, code: 'MET-V857' },
    );
  }
  // BUG-607: enqueueCommit never throws (byte-budget eviction + a
  // quota-degrade retry are internal to commitqueue.ts), but it CAN fail to
  // persist the entry at all — that failure must reach the player and the
  // registry (GR#1/#7), never a silent "queued" lie, and it must never
  // break this button or the sim.
  const outcome = enqueueCommit(queueStorage(), id, payload, at);
  if (!outcome.ok) {
    const code = outcome.droppedOversize ? 'MET-V854' : 'MET-V855';
    const budgetMB = (QUEUE_BYTE_BUDGET / (1024 * 1024)).toFixed(1);
    const msg = outcome.droppedOversize
      ? `Debug commit ${id} dropped: frame exceeds the ${budgetMB}MB queue byte budget even after compaction`
      : `Debug commit ${id} could not be queued locally: localStorage quota exhausted even after evicting the oldest half of the queue`;
    recordError(msg, { type: 'app', action: 'commit', code });
    return {
      ok: false,
      queued: false,
      id,
      message: outcome.droppedOversize
        ? `Backend unreachable and this frame is too large to queue locally (even compacted) — download it instead (Debug tab → Download).`
        : `Backend unreachable and the local queue is full (storage quota) — commit was NOT saved. Free storage (Config → Clear queue / Reclaim storage) and retry, or download it (Debug tab → Download).`,
    };
  }
  return {
    ok: true,
    queued: true,
    id,
    message: `Backend unreachable — queued locally as ${id} (${outcome.length} awaiting processing)`,
  };
}
