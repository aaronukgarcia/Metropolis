// perfhud.ts — FEAT-1972079856: performance HUD metrics collection.
//
// Pure, no-React metrics for the debug performance overlay. FPS, frame time,
// memory usage, sim-tick duration, and network activity are collected
// wall-clock-only (never inside SimState) so determinism and consistency
// checks stay untouched. All measurements use performance.now() for precision.
//
// CRITICAL DESIGN CONSTRAINT: This module contains zero SimState mutations
// and zero measurements inside the reducer. The HUD is a UI-layer overlay.

/**
 * Ring buffer for running statistics. Stores fixed-size array of numbers and
 * provides avg, p95, worst. Used for FPS frame deltas and frame times.
 */
export class RingBuffer {
  private values: number[];
  private index = 0;
  private filled = false;

  constructor(capacity: number) {
    this.values = new Array(capacity);
  }

  /** Add a value, overwriting the oldest if full. */
  add(value: number): void {
    this.values[this.index] = value;
    this.index = (this.index + 1) % this.values.length;
    if (this.index === 0) this.filled = true;
  }

  /** Average of all values. */
  average(): number {
    const len = this.isFull() ? this.values.length : this.index;
    if (len === 0) return 0;
    const sum = this.values.slice(0, len).reduce((a, b) => a + b, 0);
    return sum / len;
  }

  /** 95th percentile of all values. */
  p95(): number {
    const len = this.isFull() ? this.values.length : this.index;
    if (len === 0) return 0;
    const sorted = [...this.values.slice(0, len)].sort((a, b) => a - b);
    const idx = Math.ceil(len * 0.95) - 1;
    return sorted[Math.max(0, idx)];
  }

  /** Maximum value observed. */
  worst(): number {
    const len = this.isFull() ? this.values.length : this.index;
    if (len === 0) return 0;
    return Math.max(...this.values.slice(0, len));
  }

  /** True when the ring buffer has been filled once. */
  isFull(): boolean {
    return this.filled;
  }

  /** Clear all values. */
  reset(): void {
    this.values = new Array(this.values.length);
    this.index = 0;
    this.filled = false;
  }
}

/**
 * Network metrics from performance.getEntriesByType('resource').
 * Counts fetch calls (even if the app makes none) and sums total bytes.
 */
export interface NetworkMetrics {
  fetchCount: number;
  fetchBytes: number;
}

/**
 * Collect network metrics from Performance API (Chrome/Edge/Firefox, not Safari).
 * Returns honest zero when no fetch calls exist; never lies about activity.
 */
export function networkMetrics(): NetworkMetrics {
  try {
    if (typeof performance === 'undefined' || !performance.getEntriesByType) {
      return { fetchCount: 0, fetchBytes: 0 };
    }
    const entries = performance.getEntriesByType('resource');
    let fetchCount = 0;
    let fetchBytes = 0;
    for (const entry of entries) {
      if (entry.name.includes('/')) {
        // Rough heuristic: entries with a URL-like name. The Performance API
        // records fetch/XMLHttpRequest by default in most browsers.
        if ('transferSize' in entry && typeof entry.transferSize === 'number') {
          fetchBytes += entry.transferSize;
          fetchCount++;
        }
      }
    }
    return { fetchCount, fetchBytes };
  } catch {
    return { fetchCount: 0, fetchBytes: 0 };
  }
}

/**
 * Memory metrics from performance.memory (Chrome-only).
 * Returns null when unavailable; the HUD displays "n/a" honestly.
 */
export function jsHeapMemory(): number | null {
  try {
    if (
      typeof performance !== 'undefined' &&
      'memory' in performance &&
      typeof (performance as any).memory === 'object'
    ) {
      const mem = (performance as any).memory;
      if ('usedJSHeapSize' in mem && typeof mem.usedJSHeapSize === 'number') {
        return mem.usedJSHeapSize;
      }
    }
  } catch {
    /* feature detect failed */
  }
  return null;
}

/**
 * State of the FPS tracker — ring buffer of frame deltas.
 */
export interface FpsTrackerState {
  lastFrameMs: number | null;
  deltas: RingBuffer;
}

/**
 * Create a new FPS tracker. The ring buffer stores up to 60 frame deltas.
 */
export function createFpsTracker(): FpsTrackerState {
  return {
    lastFrameMs: null,
    deltas: new RingBuffer(60),
  };
}

/**
 * Sample the current frame time (call once per animation frame).
 * Adds the delta to the ring buffer if a previous frame exists.
 *
 * @param state  the tracker state (mutated)
 * @param nowMs  current wall-clock time (performance.now())
 */
export function sampleFrame(state: FpsTrackerState, nowMs: number): void {
  if (state.lastFrameMs !== null) {
    const delta = nowMs - state.lastFrameMs;
    if (delta > 0 && delta < 1000) {
      // Sanity: ignore impossible deltas (clock jumps, very fast loops)
      state.deltas.add(delta);
    }
  }
  state.lastFrameMs = nowMs;
}

/**
 * Snapshot of current FPS metrics. Avg, p95, worst frame times in ms;
 * FPS derived from 1000 / delta (a 16.7 ms frame = ~60 FPS).
 */
export interface FpsMetrics {
  avgFrameMs: number;
  p95FrameMs: number;
  worstFrameMs: number;
  avgFps: number;
  p95Fps: number;
  worstFps: number;
}

/**
 * Snapshot the current FPS metrics from the tracker.
 * Convert frame deltas to FPS using 1000 ms / delta.
 */
export function fpsMetrics(state: FpsTrackerState): FpsMetrics {
  const avg = state.deltas.average();
  const p95 = state.deltas.p95();
  const worst = state.deltas.worst();

  const toFps = (ms: number) => (ms > 0 ? 1000 / ms : 0);

  return {
    avgFrameMs: avg,
    p95FrameMs: p95,
    worstFrameMs: worst,
    avgFps: toFps(avg),
    p95Fps: toFps(p95),
    worstFps: toFps(worst),
  };
}

/**
 * Sim-tick duration tracker. Instruments the reducer to measure how long
 * each call to advance() takes. Stores timing samples in a ring buffer
 * (60 most recent ticks).
 */
export interface TickTrackerState {
  durations: RingBuffer;
}

/**
 * Create a new tick-duration tracker.
 */
export function createTickTracker(): TickTrackerState {
  return {
    durations: new RingBuffer(60),
  };
}

/**
 * Record a tick duration (call this after advance() completes).
 *
 * @param state the tracker state (mutated)
 * @param ms    milliseconds the tick took (use performance.now() before/after)
 */
export function recordTickDuration(state: TickTrackerState, ms: number): void {
  if (ms > 0 && ms < 10000) {
    // Sanity filter: reject clock skew or measurement errors
    state.durations.add(ms);
  }
}

/**
 * Snapshot of tick metrics.
 */
export interface TickMetrics {
  avgMs: number;
  p95Ms: number;
  worstMs: number;
}

/**
 * Snapshot the current tick metrics.
 */
export function tickMetrics(state: TickTrackerState): TickMetrics {
  return {
    avgMs: state.durations.average(),
    p95Ms: state.durations.p95(),
    worstMs: state.durations.worst(),
  };
}

/**
 * All the performance HUD metrics at once.
 */
export interface PerfHudSnapshot {
  fps: FpsMetrics;
  tick: TickMetrics;
  memoryBytes: number | null;
  network: NetworkMetrics;
  snapshotAtMs: number;
}

/**
 * Render throttle: 1 Hz (update every 1000 ms).
 * The UI samples FPS per-frame but renders the HUD only once per second.
 */
export interface RenderThrottle {
  lastRenderMs: number | null;
}

/**
 * Create a new render throttle.
 */
export function createRenderThrottle(): RenderThrottle {
  return { lastRenderMs: null };
}

/**
 * Check if a render is due (1000 ms since last).
 * @param state  throttle state (mutated if returning true)
 * @param nowMs  current wall-clock time
 * @returns true if due to render; updates state.lastRenderMs
 */
export function isRenderDue(state: RenderThrottle, nowMs: number): boolean {
  if (state.lastRenderMs === null) {
    state.lastRenderMs = nowMs;
    return true;
  }
  if (nowMs - state.lastRenderMs >= 1000) {
    state.lastRenderMs = nowMs;
    return true;
  }
  return false;
}

// ============================================================================
// GLOBAL TICK TRACKER (for store.tsx instrumentation)
// ============================================================================

/** Module-level tick tracker, initialized lazily. */
let globalTickTracker: TickTrackerState | null = null;

/**
 * Get the global tick tracker instance (created on first access).
 * Used by store.tsx to measure tick duration.
 * Returns null in production builds.
 */
export function getGlobalTickTracker(): TickTrackerState | null {
  if (import.meta.env.DEV) {
    if (!globalTickTracker) {
      globalTickTracker = createTickTracker();
    }
    return globalTickTracker;
  }
  return null;
}
/**
 * Get a snapshot of current performance metrics from the global tracker.
 * Returns null when tracker is absent (non-DEV or headless test context).
 */
export function getPerformanceSnapshot(): PerfHudSnapshot | null {
  // Both trackers must be live: tick comes from the store instrumentation,
  // fps from the PerfHud component's frame loop. Either absent (non-DEV,
  // headless tests, HUD never opened) → honest null, never fake zeros.
  if (!globalTickTracker || !globalFpsTracker) return null;

  const nowMs = performance.now();
  return {
    fps: fpsMetrics(globalFpsTracker),
    tick: tickMetrics(globalTickTracker),
    memoryBytes: jsHeapMemory(),
    network: networkMetrics(),
    snapshotAtMs: nowMs,
  };
}

/** Module-level FPS tracker (populated by the PerfHud component's loop). */
let globalFpsTracker: FpsTrackerState | null = null;

/**
 * Set the global FPS tracker (called by PerfHud component during init).
 * This allows debugjson to access live FPS metrics without circular imports.
 */
export function setFpsTracker(tracker: FpsTrackerState | null): void {
  globalFpsTracker = tracker;
}

/** Get the global FPS tracker (null when the HUD has never registered one). */
export function getFpsTracker(): FpsTrackerState | null {
  return globalFpsTracker;
}
