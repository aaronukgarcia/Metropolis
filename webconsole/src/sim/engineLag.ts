// engineLag.ts — BUG-618 (P1): the ENGINE LAG GAUGE Aaron has been asking
// for for days: "how far lagging the backend engine is from the UI."
//
// CONTEXT (what was wrongly built instead, do not repeat): FEAT-1972079938's
// Construction Queue tab shows buildings under construction (a different,
// legitimate feature) and QueueDepthHud.tsx's worker line, in the DEFAULT
// flag-off mode (metropolis.webworker unset — Aaron plays flag-off), shows a
// literal "worker off — ticks run on the main thread" string sourced from
// perfhud.ts's getGlobalTickTracker(), which is DEV-ONLY
// (`import.meta.env?.DEV` — see perfhud.ts). In Aaron's own (non-dev,
// production-style) session that reads as a permanently useless message —
// exactly the failure this gauge exists to fix. This tracker is therefore
// ALWAYS ACTIVE, gated on nothing: no import.meta.env.DEV check anywhere in
// this file, and no dependency on the webworker flag for whether it records
// (only the UI's popover optionally reads the worker's own queue depth
// separately, additively, when that flag is on).
//
// PURE TELEMETRY, same discipline as queueDepth.ts / workerQueueDepth.ts:
// this module NEVER reads or mutates SimState, the journal, or anything
// determinism-relevant (GR#21). It is fed by explicit calls from store.tsx's
// tick-driver interval / worker.onmessage (the two places a tick is
// scheduled or actually applied) and by the gauge component's own
// requestAnimationFrame heartbeat (the stall detector). performance.now()
// values are supplied BY THE CALLER (never read internally except in the
// convenience default-arg wrappers below) so the arithmetic here is testable
// with fabricated timestamps and in plain Node, mirroring queueDepth.ts's
// framework-free convention. Kept entirely out of debugjson.ts/
// captureBeforeWipe.ts's capture path — GR#27's fail-closed wipe-capture
// guard forbids clock calls during capture, and this module is never
// imported by either.
//
// Three signals, three plain counters/values:
//   1. TICK BACKLOG — ticksScheduled (one per tick-driver interval fire that
//      WANTS a tick) vs ticksCompleted (one per tick actually applied,
//      worker-hydrate or main-thread reducer). backlog = max(0, scheduled -
//      completed); naturally decays to 0 as completions catch up to
//      schedule fires — no separate decay timer needed, it is the honest
//      arithmetic difference of two monotonic counters.
//   2. TICK COST RATIO — lastTickMs (duration of the most recently APPLIED
//      or observed tick, either path) divided by intervalMs (the
//      tick-driver's own SPEED_MS[state.speed]). <=1 green, (1,3] amber,
//      >3 red (RATIO_AMBER/RATIO_RED below).
//   3. STALL DETECTOR — recordFrameGap(gapMs, nowMs) called by the gauge
//      component's rAF loop; gapMs > STALL_THRESHOLD_MS (500ms) means the
//      main thread was blocked between two consecutive animation frames.
//      Retroactive by construction (nothing can observe a stall WHILE it is
//      happening — the thread is blocked) — the component reports it the
//      moment the next frame finally paints. recentStall(nowMs) returns the
//      magnitude for STALL_DISPLAY_MS after it was recorded, then reverts to
//      null; worstStallMs never resets (session high-water mark, matching
//      queueDepth.ts's HWM convention) until reset()/resetAll().
export type LagClass = 'green' | 'amber' | 'red';

/** Tick-cost ratio thresholds (lastTickMs / intervalMs). */
export const RATIO_AMBER = 1;
export const RATIO_RED = 3;

/** Backlog-count thresholds (ticksScheduled - ticksCompleted, clamped >=0).
 *  BACKLOG_AMBER (1) is the count at which the chip first leaves green (any
 *  backlog at all is worth flagging); BACKLOG_RED (3) is the amber ceiling —
 *  classifyBacklog below reads <=0 green, <=BACKLOG_RED amber, >BACKLOG_RED
 *  red, the same 3-band shape as classifyRatio. */
export const BACKLOG_AMBER = 1;
export const BACKLOG_RED = 3;

/** A gap between two consecutive animation frames longer than this (ms)
 *  means the main thread was blocked — "the thread was blocked" per the
 *  brief's stall-detector spec. ~500ms per the brief. */
export const STALL_THRESHOLD_MS = 500;

/** How long (ms) a detected stall stays reported by recentStall() before
 *  reverting to null — "show 'stalled X.Xs' for a few seconds" per the brief. */
export const STALL_DISPLAY_MS = 4000;

/** Sanity ceiling for a single recorded tick duration — guards against a
 *  clock-skew/measurement artifact polluting lastTickMs (mirrors
 *  perfhud.ts's recordTickDuration ms<10000 filter, widened slightly since a
 *  genuinely stalled tick is exactly what this gauge exists to surface). */
const MAX_SANE_TICK_MS = 60_000;

export interface EngineLagSnapshot {
  ticksScheduled: number;
  ticksCompleted: number;
  /** max(0, ticksScheduled - ticksCompleted). 0 when the engine is caught up. */
  backlog: number;
  /** Duration (ms) of the most recently observed tick, or null before any
   *  tick has ever been timed. */
  lastTickMs: number | null;
  /** The tick-driver's current interval length (ms), or null before
   *  store.tsx has ever reported one (e.g. before first mount / speed=0). */
  intervalMs: number | null;
  /** lastTickMs / intervalMs, or null when either input is missing. */
  ratio: number | null;
  /** Classification of `ratio` (green when ratio is null — "no data yet"
   *  reads as fine, never as a false alarm). */
  ratioClass: LagClass;
  /** Classification of `backlog` alone (independent of ratio — the chip
   *  shows the worse of the two, see engineLagClassOf below). */
  backlogClass: LagClass;
  /** Largest single frame-gap ever observed this session (ms), 0 if none. */
  worstStallMs: number;
  /** Magnitude (ms) of the most recent stall IF it was recorded within the
   *  last STALL_DISPLAY_MS of `nowMs` passed to snapshot(); otherwise null.
   *  This is what makes a stall's report time-limited rather than sticky. */
  recentStallMs: number | null;
}

/** The chip's overall status is the WORSE of backlogClass and ratioClass,
 *  with an active recentStall always winning (a stall is definitionally the
 *  worst thing this gauge can report — the thread was blocked outright). */
export function engineLagClassOf(snap: EngineLagSnapshot): LagClass {
  if (snap.recentStallMs !== null) return 'red';
  const rank: Record<LagClass, number> = { green: 0, amber: 1, red: 2 };
  return rank[snap.backlogClass] >= rank[snap.ratioClass] ? snap.backlogClass : snap.ratioClass;
}

function classifyRatio(ratio: number | null): LagClass {
  if (ratio === null) return 'green';
  if (ratio <= RATIO_AMBER) return 'green';
  if (ratio <= RATIO_RED) return 'amber';
  return 'red';
}

// F2 fix (independent round REJECT, 2026-09-03): the previous implementation
// used BACKLOG_AMBER (1) as the amber/red boundary instead of BACKLOG_RED
// (3), so a backlog of 2 read RED instead of AMBER — the exact same 3-band
// shape as classifyRatio above (<= a low bound is green, <= a high bound is
// amber, above it is red) was NOT what the code actually did. BACKLOG_AMBER
// stays exported/documented as the count at which the chip first leaves
// green (1 — any backlog at all is worth flagging amber), but the
// green/amber/red SPLIT below is <=0 / <=BACKLOG_RED / >BACKLOG_RED, matching
// the constants' documented semantics (BACKLOG_RED = "the amber ceiling
// before red").
function classifyBacklog(backlog: number): LagClass {
  if (backlog <= 0) return 'green';
  if (backlog <= BACKLOG_RED) return 'amber';
  return 'red';
}

type Listener = (snapshot: EngineLagSnapshot) => void;

/**
 * EngineLagTracker: an observable store of engine-vs-UI lag signals. Every
 * mutator is a single, cheap counter/value update — safe to call from a hot
 * path (the tick-driver interval, a worker onmessage handler, a rAF loop)
 * with no allocation beyond the listener fan-out.
 */
export class EngineLagTracker {
  private ticksScheduled = 0;
  private ticksCompleted = 0;
  private lastTickMs: number | null = null;
  private intervalMs: number | null = null;
  private worstStallMs = 0;
  private lastStallMs: number | null = null;
  private lastStallAtMs: number | null = null;
  private listeners = new Set<Listener>();

  /** Call once per tick-driver interval fire that WANTS a tick — i.e. the
   *  top of store.tsx's setInterval callback, before branching on
   *  worker-vs-fallback. */
  recordTickScheduled(): void {
    this.ticksScheduled++;
    this.emit();
  }

  /** Call once per tick ACTUALLY applied to state — the main-thread reducer
   *  path (wrappedDispatch's 'tick' branch) and the worker-hydrate path
   *  (worker.onmessage's decision.kind === 'apply' branch) both call this
   *  exactly once per applied tick. A forced-synchronous escape tick
   *  (guardedDispatch's K-supersede rescue) also flows through the
   *  main-thread reducer path and is counted here too — it is a real tick
   *  applied outside the interval's own schedule, which is exactly why
   *  backlog is clamped at 0 rather than allowed to go negative. */
  recordTickCompleted(): void {
    this.ticksCompleted++;
    this.emit();
  }

  /** Record how long the most recently observed tick took (ms). Sanity-
   *  filtered the same way perfhud.ts's recordTickDuration is: negative or
   *  absurd values (clock skew) are dropped rather than corrupting the
   *  ratio. */
  recordTickDuration(ms: number): void {
    if (ms >= 0 && ms < MAX_SANE_TICK_MS) {
      this.lastTickMs = ms;
      this.emit();
    }
  }

  /** Record the tick-driver's current interval length (ms) — store.tsx
   *  calls this whenever its tick-driver effect (re)fires, i.e. whenever
   *  SPEED_MS[state.speed] may have changed. */
  setIntervalMs(ms: number): void {
    if (ms > 0) {
      this.intervalMs = ms;
      this.emit();
    }
  }

  /**
   * F1 fix (independent round REJECT, 2026-09-03, "the killer — pause
   * honesty") — call the instant the tick-driver enters PAUSE (state.speed
   * === 0). A paused engine cannot be "behind": nothing is being asked of
   * it, so a nonzero backlog left over from the instant before pause (e.g.
   * a drag-supersede burst right before the player hit Pause) must not sit
   * there reading "Engine: N behind" FOREVER while paused — exactly the
   * dishonesty class Aaron's ruling forbids. Zeroes ticksScheduled/
   * ticksCompleted (so backlog reads 0 and resuming counts up cleanly from
   * a matched baseline, not from some stale N-tick deficit) while
   * PRESERVING worstStallMs (session high-water mark) and the last-tick/
   * interval stats (lastTickMs/intervalMs — still meaningful, honest
   * numbers about the last real tick even though none are running now).
   * Idempotent: calling it again while already settled (both counters
   * already 0) is a harmless no-op value-wise.
   */
  settle(): void {
    this.ticksScheduled = 0;
    this.ticksCompleted = 0;
    this.emit();
  }

  /** Record one observed gap (ms) between two consecutive animation frames,
   *  at wall-clock `nowMs` (caller-supplied — the component's rAF loop
   *  passes its own performance.now() reading). Gaps at or below
   *  STALL_THRESHOLD_MS are ordinary frame jitter and are ignored; only a
   *  genuine stall updates worstStallMs / the recent-stall report. */
  recordFrameGap(gapMs: number, nowMs: number): void {
    if (gapMs > STALL_THRESHOLD_MS) {
      this.lastStallMs = gapMs;
      this.lastStallAtMs = nowMs;
      if (gapMs > this.worstStallMs) this.worstStallMs = gapMs;
      this.emit();
    }
  }

  /** The magnitude (ms) of the most recent stall if it was recorded within
   *  the last STALL_DISPLAY_MS relative to `nowMs`; otherwise null. Pure
   *  function of (state, nowMs) — no internal clock read, so it is testable
   *  with a fabricated `nowMs`. */
  recentStall(nowMs: number): number | null {
    if (this.lastStallAtMs === null || this.lastStallMs === null) return null;
    if (nowMs - this.lastStallAtMs > STALL_DISPLAY_MS) return null;
    return this.lastStallMs;
  }

  /** Full snapshot at wall-clock `nowMs` (only consulted for the
   *  time-limited recentStallMs field — every other field is a pure read of
   *  internal counters). */
  snapshot(nowMs: number): EngineLagSnapshot {
    const backlog = Math.max(0, this.ticksScheduled - this.ticksCompleted);
    const ratio = this.lastTickMs !== null && this.intervalMs !== null ? this.lastTickMs / this.intervalMs : null;
    return {
      ticksScheduled: this.ticksScheduled,
      ticksCompleted: this.ticksCompleted,
      backlog,
      lastTickMs: this.lastTickMs,
      intervalMs: this.intervalMs,
      ratio,
      ratioClass: classifyRatio(ratio),
      backlogClass: classifyBacklog(backlog),
      worstStallMs: this.worstStallMs,
      recentStallMs: this.recentStall(nowMs),
    };
  }

  /** Reset every counter/value to its initial state — used by tests and by
   *  a fresh SimProvider mount (mirrors queueDepth.ts's resetAll). Does NOT
   *  clear worstStallMs — that is a deliberate session high-water mark, same
   *  convention as queueDepth.ts's HWM (only an explicit resetAll clears it;
   *  use resetAll() below for a true full wipe, e.g. test isolation). */
  reset(): void {
    this.ticksScheduled = 0;
    this.ticksCompleted = 0;
    this.lastTickMs = null;
    this.intervalMs = null;
    this.lastStallMs = null;
    this.lastStallAtMs = null;
    this.emit();
  }

  /** Full wipe INCLUDING the worstStallMs session high-water mark — never
   *  called from app code (that would defeat the point of a session HWM);
   *  exists for test isolation between independent test files/cases. */
  resetAll(): void {
    this.reset();
    this.worstStallMs = 0;
    this.emit();
  }

  /** Subscribe to every mutation. Returns an unsubscribe fn. Fires
   *  immediately with the current snapshot (at the subscribe-time caller-
   *  supplied `nowMs`), matching queueDepth.ts's subscribe convention. */
  subscribe(listener: Listener, nowMs: number): () => void {
    this.listeners.add(listener);
    listener(this.snapshot(nowMs));
    return () => {
      this.listeners.delete(listener);
    };
  }

  private emit(): void {
    if (this.listeners.size === 0) return;
    const nowMs = typeof performance !== 'undefined' ? performance.now() : 0;
    const snap = this.snapshot(nowMs);
    for (const l of this.listeners) l(snap);
  }
}

/** Module-level singleton — the one tracker the whole app shares, mirroring
 *  queueDepth.ts's queueDepthTracker / workerQueueDepth.ts's
 *  getGlobalWorkerQueueTracker convention. store.tsx's tick-driver +
 *  worker.onmessage write to this; the TopBar chip (EngineLagChip) reads it. */
export const engineLagTracker = new EngineLagTracker();
