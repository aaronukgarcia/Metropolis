// queueDepth.ts — FEAT-1972079938: the Queue Depth HUD's tracker.
//
// PURE TELEMETRY. This module tracks, per engine/target key, how many
// "asks" (protocol commands, subscribe/unsubscribe requests — anything
// dispatched over protocolClient.ts's wire and awaiting a settle) are
// currently in flight. It NEVER reads or mutates SimState, the journal, or
// anything determinism-relevant (GR#21) — it only counts increments the
// caller reports. The point (Aaron, FEAT-1972079936 compute-offload epic):
// once multiple engines sit behind the protocol adapter, per-engine queue
// depth is the first legible bottleneck signal ("which engine are we
// waiting on most"), so this is designed to key by an arbitrary engine
// name from day one even though only one target (the protocol adapter)
// exists today.
//
// Model: increment(engine) on dispatch, decrement(engine) on settle
// (resolve OR reject — a rejected ask still frees its slot, no leak).
// depth is clamped at 0 (a decrement without a matching increment, e.g. a
// stale call after reset(), never goes negative). The high-water mark
// (HWM) is the max depth ever observed for that engine THIS SESSION; it
// only moves up until reset() zeroes both depth and HWM for that engine
// (or resetAll() for every engine).
//
// Kept deliberately framework-free (no React) so it is testable in plain
// Node, mirroring protocolClient.ts/backend.ts's own convention. React
// wiring lives in components/right/QueueDepthHud.tsx.

/** Snapshot of one engine's queue state at a point in time. */
export interface QueueDepthEntry {
  engine: string;
  depth: number;
  highWaterMark: number;
}

/** Snapshot of every tracked engine plus the aggregate total. */
export interface QueueDepthSnapshot {
  entries: QueueDepthEntry[];
  total: number;
  totalHighWaterMark: number;
}

type Listener = (snapshot: QueueDepthSnapshot) => void;

/**
 * QueueDepthTracker: an observable store of in-flight ask counts keyed by
 * engine/target name. increment()/decrement() are the only mutators a
 * caller needs to instrument a request path; subscribe() drives the HUD.
 */
export class QueueDepthTracker {
  private depths = new Map<string, number>();
  private hwm = new Map<string, number>();
  private listeners = new Set<Listener>();

  /** Record one ask dispatched to `engine`. Call exactly once per ask, at send time. */
  increment(engine: string): void {
    const next = (this.depths.get(engine) ?? 0) + 1;
    this.depths.set(engine, next);
    if (next > (this.hwm.get(engine) ?? 0)) this.hwm.set(engine, next);
    this.emit();
  }

  /**
   * Record one ask settling (resolved OR rejected) for `engine`. Clamped at
   * 0 — a decrement with no matching prior increment (e.g. called again
   * after reset()) never drives depth negative.
   */
  decrement(engine: string): void {
    const prev = this.depths.get(engine) ?? 0;
    const next = Math.max(0, prev - 1);
    this.depths.set(engine, next);
    this.emit();
  }

  /** Current depth for one engine (0 if never seen). */
  depthOf(engine: string): number {
    return this.depths.get(engine) ?? 0;
  }

  /** High-water mark for one engine (0 if never seen or since last reset). */
  highWaterMarkOf(engine: string): number {
    return this.hwm.get(engine) ?? 0;
  }

  /** Reset one engine's depth AND high-water mark to 0. Depth for an
   * engine mid-flight (nonzero depth) is also zeroed — this is an explicit
   * "start counting again" operation, not a graceful drain. */
  reset(engine: string): void {
    this.depths.delete(engine);
    this.hwm.delete(engine);
    this.emit();
  }

  /** Reset every tracked engine at once (the HUD's "reset" control). */
  resetAll(): void {
    this.depths.clear();
    this.hwm.clear();
    this.emit();
  }

  /** Full snapshot: one row per engine ever seen (even at depth 0, so a
   * momentarily-idle engine doesn't vanish from the HUD), plus totals. */
  snapshot(): QueueDepthSnapshot {
    const engines = new Set<string>([...this.depths.keys(), ...this.hwm.keys()]);
    const entries: QueueDepthEntry[] = [...engines].sort().map((engine) => ({
      engine,
      depth: this.depthOf(engine),
      highWaterMark: this.highWaterMarkOf(engine),
    }));
    const total = entries.reduce((a, e) => a + e.depth, 0);
    const totalHighWaterMark = entries.reduce((a, e) => a + e.highWaterMark, 0);
    return { entries, total, totalHighWaterMark };
  }

  /** Subscribe to every increment/decrement/reset. Returns an unsubscribe fn.
   * Fires the listener immediately with the current snapshot on subscribe,
   * matching the store.tsx subscribe convention elsewhere in this codebase. */
  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    listener(this.snapshot());
    return () => {
      this.listeners.delete(listener);
    };
  }

  private emit(): void {
    const snap = this.snapshot();
    for (const l of this.listeners) l(snap);
  }
}

/** The default target key for today's single (protocol adapter) engine.
 * Multiple real engines slot in later by keying on their own names —
 * nothing here assumes a single key, this is just what protocolClient.ts
 * uses until a second engine exists. */
export const PROTOCOL_ENGINE_KEY = 'protocol';

/** Module-level singleton — the one tracker the whole app shares, mirroring
 * backend.ts's module-level errorLog convention. protocolClient.ts
 * instruments against this instance; QueueDepthHud.tsx subscribes to it. */
export const queueDepthTracker = new QueueDepthTracker();

/**
 * Wrap a Promise so the tracker sees exactly one increment (at call time)
 * and exactly one decrement (on settle, resolve OR reject — no leak on a
 * rejected ask). Pure passthrough otherwise: the wrapped Promise's
 * resolution/rejection value is untouched. Convenience for instrumenting
 * a request path without hand-writing .then/.catch bookkeeping at every
 * call site.
 */
export function trackAsk<T>(engine: string, promise: Promise<T>, tracker: QueueDepthTracker = queueDepthTracker): Promise<T> {
  tracker.increment(engine);
  return promise.then(
    (v) => {
      tracker.decrement(engine);
      return v;
    },
    (e) => {
      tracker.decrement(engine);
      throw e;
    }
  );
}
