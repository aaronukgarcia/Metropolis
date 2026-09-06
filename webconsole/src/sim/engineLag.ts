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
//      completed). BUG-787 (2026-09-05): in practice this does NOT reliably
//      decay back to 0 in a live session — a single slow tick (GC pause,
//      month boundary, consolidator pass) can leave completed permanently
//      one-plus behind scheduled, and nothing but settle()/reset() ever
//      zeroes it, so it reads as a ONE-WAY RATCHET ("Engine: 966 behind"
//      against a perfectly healthy 112ms median tick). backlog/backlogClass
//      are KEPT as-is (tests + the honest cumulative "slipped since load"
//      popover figure both still want them) but the chip's displayed
//      colour/label now come from achievedRate/demandedRate/rateClass — a
//      SLIDING WINDOW over the last RATE_WINDOW_TICKS completed-tick
//      timestamps (within RATE_WINDOW_MS of one another) — which heals on
//      its own once a slow tick ages out of the window. See RATE_WINDOW_*
//      and classifyRate below.
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

/** Shared green<amber<red ranking, used everywhere two LagClasses need to be
 *  combined into "whichever is worse" (engineLagClassOf's rateClass/
 *  ratioClass combination, and opus-reround-bug787's mean/median rateClass
 *  combination below). */
const CLASS_RANK: Record<LagClass, number> = { green: 0, amber: 1, red: 2 };
function worseClass(a: LagClass, b: LagClass): LagClass {
  return CLASS_RANK[a] >= CLASS_RANK[b] ? a : b;
}

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

// BUG-787 fix (2026-09-05): backlog (ticksScheduled - ticksCompleted) is a
// ONE-WAY RATCHET as a DISPLAYED signal — it only resets on settle()/reset(),
// so a single slow tick (GC pause, month boundary, consolidator pass) that
// leaves completed one tick behind schedule raises it permanently even when
// every subsequent tick is comfortably inside budget (Aaron's 9.5M-citizen
// city: "Engine: 966 behind" against a measured 112ms median tick vs a 160ms
// Turbo interval — the engine was NOT actually behind). The counters
// themselves (ticksScheduled/ticksCompleted, and the derived `backlog`/
// `backlogClass` fields below) are UNCHANGED and still tested directly —
// existing arithmetic tests keep asserting them — but the chip's displayed
// classification/label now come from a SLIDING WINDOW of recent completed-
// tick timestamps (achievedRate/demandedRate/rateClass below), which
// naturally heals once the slow tick rolls out of the window. The old
// cumulative count is not thrown away — it survives as `backlog`/aliased
// `slippedSinceLoad` for the popover's honest "slipped since load: N".
/** Number of most-recent completed-tick timestamps retained for the
 *  windowed achieved-rate calculation (BUG-787). Also effectively bounded to
 *  ~RATE_WINDOW_MS of wall-clock span by the filter in computeAchievedRate —
 *  whichever is tighter wins, so a burst of slow ticks ages out of both the
 *  count cap and the time cap. PINNED at 30 by an explicit test (opus-round-
 *  bug787 F5: this constant was previously unpinned — nothing failed if it
 *  silently shrank from 30 to 3, which combined with MIN_RATE_SAMPLES would
 *  make the floor logic below meaningless). Must stay >= MIN_RATE_SAMPLES. */
export const RATE_WINDOW_TICKS = 30;

/** Wall-clock span (ms) considered "recent" for the achieved-rate window
 *  (BUG-787) — "a time window ~10s" per the brief. opus-round-bug787 F1:
 *  this is now measured relative to the LIVE query time (snapshot()'s own
 *  `nowMs`), never to the newest stored completion — see computeAchievedRate. */
export const RATE_WINDOW_MS = 10_000;

/** opus-round-bug787 F5: floor on how few completion timestamps
 *  computeAchievedRate will settle for, even when the RATE_WINDOW_MS time
 *  cut would otherwise leave fewer. A slow tick-driver interval (e.g. the
 *  "Slow" speed) legitimately produces very few completions inside a plain
 *  10-second window even while perfectly healthy, and a THIN sample (as few
 *  as 1-2 gaps) lets a single hiccup dominate the statistic no matter which
 *  aggregate you compute (median included — median of 1 gap IS that gap).
 *  When at least MIN_RATE_SAMPLES timestamps exist in the tracker's total
 *  history (bounded by RATE_WINDOW_TICKS), computeAchievedRate falls back to
 *  the most recent MIN_RATE_SAMPLES of them regardless of how old some are —
 *  diluting one recent hiccup against a run of older, clean gaps. This does
 *  NOT reintroduce BUG-787's original "frozen forever" defect: a genuinely
 *  dead worker is caught independently by the DEAD_WORKER_SCHEDULED_TICKS
 *  rule below (which looks at scheduled-tick advancement, not at how stale
 *  the rate sample is), so this floor only ever matters while ticks are
 *  still actually completing, just slowly/sparsely. */
export const MIN_RATE_SAMPLES = 12;

/** opus-round-bug787 F1/F5: number of tick-driver SCHEDULE fires that may
 *  elapse with ZERO completions before the engine is declared dead — "the
 *  worker stopped responding", as opposed to merely slow. This is a TICK-
 *  COUNT threshold, not a time threshold: it fires identically whether the
 *  gap has been 1 minute or 30 minutes of real time, because store.tsx's
 *  own recordTickScheduled() call site is the only thing this tracker knows
 *  is still alive. Chosen small (5) because a healthy engine's completed
 *  count should track scheduled almost 1:1 (see recordTickCompleted's own
 *  doc comment on the main-thread fallback path being synchronous) — 5
 *  consecutive misses is already well past ordinary jitter. */
export const DEAD_WORKER_SCHEDULED_TICKS = 5;

/** achievedRate/demandedRate fraction at/above which the windowed rate reads
 *  green (BUG-787) — originally "green when achieved >= 95% of demanded".
 *
 *  opus-reround-bug787 F2 (round REJECT #2, "the median absorbs up to 49% of
 *  ticks at ANY severity"): the first round's fix (median-only, threshold
 *  left at 95%) was itself exploitable — a median statistic by definition
 *  ignores whichever HALF of the samples is worse, so 45% of ticks taking
 *  30,000ms each with the other 55% healthy still reports the healthy
 *  median (100% -> green) while TRUE throughput is ~1%. The fix (see
 *  classifyRate/computeAchievedRates) is to classify on the WORSE of the
 *  median-based figure AND a proper MEAN-over-span throughput (which cannot
 *  be gamed this way — every sample counts, weighted by the real time it
 *  actually consumed). That reintroduced the original F2 problem at the
 *  THRESHOLD level: a mean-over-span statistic is what read Aaron's real
 *  "one 3x spike in ~20 ticks" scenario as only ~91% of demanded (19 normal
 *  gaps + 1 gap at 3x length = 22 "interval units" of span for 20
 *  completions = 20/22 = 90.9%), which the ORIGINAL 95% floor would flag
 *  amber. Lowering this specific threshold (not the amber floor) to 0.85 is
 *  a placeholder tuned to sit just below that 90.9% figure with a small
 *  margin, while still catching the 45%-of-ticks-slow case outright (that
 *  case's mean-over-span throughput is ~53% at 480ms severity and ~1% at
 *  30,000ms severity — both fail 0.85 by a wide margin; see the pinned
 *  tests). This is Aaron's call to retune further once real dogfood data
 *  exists for what "green" should tolerate. */
export const RATE_GREEN_FRACTION = 0.85;

/** achievedRate/demandedRate fraction at/above which the windowed rate reads
 *  amber rather than red (BUG-787) — "amber to 60%". Below this is red. */
export const RATE_AMBER_FRACTION = 0.6;

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
  /** max(0, ticksScheduled - ticksCompleted). 0 when the engine is caught up.
   *  BUG-787: this is a CUMULATIVE, one-way-ratchet count (it only zeroes on
   *  settle()/reset()) — kept for the counter-arithmetic tests and exposed
   *  in the popover as `slippedSinceLoad`, but it no longer drives the
   *  chip's displayed colour/label (see rateClass/engineLagClassOf). */
  backlog: number;
  /** The honest "slipped since load: N" popover figure (BUG-787). opus-round-
   *  bug787 F4: this is NO LONGER a plain alias of `backlog` — `backlog`
   *  zeroes on every settle() (pause), so a chip that only ever shows the
   *  live backlog would falsely read "0 slipped" for a session that has
   *  paused ten times after genuinely losing ticks each time. slippedSinceLoad
   *  is current-backlog PLUS a separate accumulator that only settle()
   *  folds into (added, never cleared by settle()) — see the tracker's
   *  `slippedSinceLoadAccum` field. Only reset()/resetAll() (a fresh load)
   *  zero it, matching the field's name. */
  slippedSinceLoad: number;
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
  /** Classification of `backlog` alone. BUG-787: retained for the
   *  counter-arithmetic tests but NO LONGER consulted by engineLagClassOf —
   *  see rateClass, which replaced it as the "am I behind right now" signal. */
  backlogClass: LagClass;
  /** Achieved completed-tick throughput (ticks/sec), MEAN-over-span — total
   *  completions in the chosen sample divided by the real elapsed time they
   *  spanned. This is the figure shown in the chip's "X.X/Y.Y t/s" label
   *  (opus-reround-bug787 F2: "the honest throughput the t/s label
   *  promises") — every sample counts, weighted by how long it actually
   *  took, so unlike a median it cannot be gamed by a large minority of
   *  arbitrarily slow ticks. Null when fewer than 2 completions fall in the
   *  chosen sample (not enough data yet, e.g. right after reset()/settle()). */
  achievedRate: number | null;
  /** opus-reround-bug787 F2: the MEDIAN inter-completion-gap-derived rate
   *  (the first round's fix) — kept as a SEPARATE signal, not shown in the
   *  label, but still folded into `rateClass` (the worse of this and the
   *  mean-based `achievedRate`). It exists to stop a small MINORITY of
   *  extreme outliers (e.g. one real 3x-slow tick) from reading amber/red
   *  when the mean-over-span alone would be briefly dragged down by a
   *  single genuine hiccup — see classifyRate's comment for the full
   *  worse-of-two rationale and RATE_GREEN_FRACTION's comment for why one
   *  statistic alone was not enough in either direction. */
  achievedRateMedian: number | null;
  /** The tick-driver's demanded throughput (1000 / intervalMs), or null
   *  before an interval has been reported. */
  demandedRate: number | null;
  /** Classification of the WORSE of achievedRate (mean-over-span) and
   *  achievedRateMedian vs demandedRate (BUG-787, reworked opus-reround-
   *  bug787 F2): green when a statistic's fraction of demanded is >=
   *  RATE_GREEN_FRACTION, amber to RATE_AMBER_FRACTION, red below — green
   *  when a statistic's inputs are unknown (no false alarm on missing data,
   *  same convention as ratioClass). This, not backlogClass, is what
   *  engineLagClassOf now uses as the "behind" signal. */
  rateClass: LagClass;
  /** opus-round-bug787 F1: scheduled-tick fires since the last completed
   *  tick (or since reset()/settle(), if there has never been one) — the
   *  raw signal `deadWorker` below is derived from. */
  scheduledSinceLastCompletion: number;
  /** opus-round-bug787 F1: true when scheduledSinceLastCompletion >=
   *  DEAD_WORKER_SCHEDULED_TICKS — the tick-driver is still firing but
   *  nothing is completing, e.g. a worker that silently died. This
   *  OVERRIDES rateClass's "no data = green" convention in
   *  engineLagClassOf: a rate window that has decayed to null because
   *  nothing has completed in a while is genuinely ambiguous ("no data yet"
   *  vs "the engine died"), and deadWorker is what disambiguates it to red.
   *  False while paused (settle() zeroes the counters this is derived
   *  from), so pause honesty is preserved automatically. */
  deadWorker: boolean;
  /** Largest single frame-gap ever observed this session (ms), 0 if none. */
  worstStallMs: number;
  /** Magnitude (ms) of the most recent stall IF it was recorded within the
   *  last STALL_DISPLAY_MS of `nowMs` passed to snapshot(); otherwise null.
   *  This is what makes a stall's report time-limited rather than sticky. */
  recentStallMs: number | null;
}

/** The chip's overall status is the WORSE of rateClass and ratioClass, with
 *  an active recentStall always winning (a stall is definitionally the
 *  worst thing this gauge can report — the thread was blocked outright).
 *
 *  BUG-787: this used to combine backlogClass (the one-way-ratchet
 *  cumulative-count classification) with ratioClass. backlogClass is now
 *  replaced by rateClass — a windowed achieved-vs-demanded throughput signal
 *  that actually heals once a slow tick ages out of the window, rather than
 *  reading "behind" forever after a single GC pause / month boundary /
 *  consolidator-pass spike.
 *
 *  opus-round-bug787 F1: a `deadWorker` snapshot (scheduled ticks piling up
 *  with zero completions) now ALSO forces red — this is the case rateClass's
 *  "null achievedRate reads green" convention cannot tell apart from a
 *  harmless startup/reset with no data yet, so it needs its own explicit
 *  override rather than folding into classifyRate. */
export function engineLagClassOf(snap: EngineLagSnapshot): LagClass {
  if (snap.recentStallMs !== null) return 'red';
  if (snap.deadWorker) return 'red';
  return worseClass(snap.rateClass, snap.ratioClass);
}

/** opus-reround-bug787 (round REJECT #2, "amber dot beside Engine: OK"):
 *  the chip's DOT colour, as a pure function of (snapshot, paused) — the
 *  exact same `paused` override the label below must use, extracted here so
 *  TopBar.tsx's component and this file's own unit tests consult IDENTICAL
 *  logic. Paused (state.speed === 0) always reads green UNLESS a stall is
 *  actively being reported (F1 pause-honesty — see the original BUG-618 F1
 *  fix's history in TopBar.tsx). */
export function engineLagChipClassOf(snap: EngineLagSnapshot, paused: boolean): LagClass {
  const stalled = snap.recentStallMs !== null;
  if (stalled) return 'red';
  if (paused) return 'green';
  return engineLagClassOf(snap);
}

/** opus-reround-bug787 (round REJECT #2): the chip's LABEL, as a pure
 *  function of (snapshot, paused). Previously this logic lived duplicated
 *  inline in TopBar.tsx, computing its own `cls`-like checks independently
 *  of the dot's actual colour — which is exactly how "amber dot beside
 *  'Engine: OK'" happened: the label only ever checked `rateClass`, so a
 *  `ratioClass`-driven (or otherwise unanticipated) non-green `cls` fell
 *  through every branch to a hardcoded 'Engine: OK'. The fix is structural,
 *  not just another special case: this function calls
 *  engineLagChipClassOf ITSELF for its final fallback check, so by
 *  construction the label can never say "OK" while the class it was
 *  computed from disagrees — see the pinned property-style test in
 *  engineLag.test.mjs that fuzzes many snapshots to prove exactly that. */
export function engineLagChipLabelOf(snap: EngineLagSnapshot, paused: boolean): string {
  const stalled = snap.recentStallMs !== null;
  if (stalled) return `Engine: stalled ${(snap.recentStallMs! / 1000).toFixed(1)}s`;
  if (paused) return 'Engine: paused';

  // opus-round-bug787 F1: a dead worker (tick-driver still firing, nothing
  // completing) gets its own label ahead of the rate reading, which would
  // otherwise have decayed to null (no recent completions to sample) and
  // read as a false "Engine: OK" per the null-is-green convention.
  const deadWorker = snap.deadWorker;
  if (deadWorker) return `Engine: no ticks for ${snap.scheduledSinceLastCompletion}`;

  // BUG-787: describe the CURRENT state via the windowed achieved-vs-
  // demanded rate, not the one-way-ratchet cumulative backlog.
  const behind = snap.achievedRate !== null && snap.demandedRate !== null && snap.rateClass !== 'green';
  if (behind) {
    const pct = Math.round((snap.achievedRate! / snap.demandedRate!) * 100);
    return `Engine: ${snap.achievedRate!.toFixed(1)}/${snap.demandedRate!.toFixed(1)} t/s (${pct}%)`;
  }

  // FINAL FALLBACK — must consult the SAME class the dot renders (never an
  // independent re-derivation) so this can never claim "OK" while the dot
  // disagrees. Whatever residual signal is driving a non-green class here
  // (typically ratioClass, i.e. lastTickMs/intervalMs) gets its own honest
  // label instead of silently defaulting to "OK".
  if (engineLagChipClassOf(snap, paused) !== 'green') {
    return `Engine: slow tick ${snap.lastTickMs != null ? snap.lastTickMs.toFixed(0) : '?'}ms`;
  }
  return 'Engine: OK';
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

/** BUG-787: classify ONE rate figure (either the mean-over-span or the
 *  median-gap statistic — see computeAchievedRates) against demandedRate.
 *  Null achieved/demanded (no data yet, or demandedRate<=0 which can't
 *  happen once intervalMs is set but guarded anyway) reads green — "no data
 *  yet" is not a false alarm, matching classifyRatio's convention above
 *  (the `deadWorker` override in engineLagClassOf is what catches the
 *  "actually broken, not just quiet" case this convention would otherwise
 *  mask).
 *
 *  HISTORY (both rounds kept here — the exploit chain is the whole point):
 *
 *  Round 1 fix (opus-round-bug787 F2, "Aaron's case reads amber"): the
 *  ORIGINAL achieved rate was a MEAN throughput over the window (ticks /
 *  span-seconds). Aaron's measured real scenario — 112ms median tick, one
 *  480ms spike in ~20 completions, 160ms Turbo interval — read as 181/200 =
 *  90.5%, BELOW the then-95% floor, i.e. amber for a session that was
 *  overwhelmingly healthy. Fix: switch the achieved-rate statistic to the
 *  MEDIAN inter-completion gap, which is self-scaling — with N samples, up
 *  to floor((N-1)/2) outliers of ARBITRARY severity are fully absorbed with
 *  zero effect on the reported rate.
 *
 *  Round 2 REJECT (opus-reround-bug787 F2, "the median absorbs up to 49% of
 *  ticks at ANY severity"): that self-scaling property is exactly the
 *  exploit — a median-ONLY statistic cannot distinguish "1 outlier in 20"
 *  from "45% of ticks taking 30,000ms each", because BOTH have a healthy
 *  MAJORITY and therefore the same median. 45% of ticks at 30,000ms (a
 *  worker doing barely 1% of demanded real work) still reported a
 *  perfectly healthy median gap → green.
 *
 *  Round 2 FIX: classify on the WORSE of BOTH statistics — the median
 *  (immune to a small minority of arbitrarily severe outliers, catches
 *  Aaron's "1 spike in 20" as green) AND the mean-over-span (a true,
 *  ungameable average — every sample counts weighted by its real duration,
 *  so a 45%-slow distribution shows up at its true ~53%/~1% throughput
 *  regardless of how the other 55% behaves). Concretely: Aaron's scenario
 *  reads ~100% median / ~91% mean → worse is ~91% → green under the
 *  lowered RATE_GREEN_FRACTION (0.85, see its own comment for why THAT
 *  threshold moved instead of this function's logic). 45% of ticks at
 *  480ms reads ~100% median / ~53% mean → worse is ~53% → red. 45% of
 *  ticks at 30,000ms reads ~100% median / ~1% mean → worse is ~1% → red.
 *  20 CONSECUTIVE 480ms ticks (no healthy majority for either statistic to
 *  fall back on) reads ~33% on both → red. All pinned as tests. */
function classifyRate(achievedRate: number | null, demandedRate: number | null): LagClass {
  if (achievedRate === null || demandedRate === null || demandedRate <= 0) return 'green';
  const fraction = achievedRate / demandedRate;
  if (fraction >= RATE_GREEN_FRACTION) return 'green';
  if (fraction >= RATE_AMBER_FRACTION) return 'amber';
  return 'red';
}

type Listener = (snapshot: EngineLagSnapshot) => void;

/** Convenience default-arg wrapper (see the file header's testability rule)
 *  — the only place recordTickCompleted reads the clock, and only when a
 *  caller omits the explicit timestamp. */
function defaultNowMs(): number {
  return typeof performance !== 'undefined' ? performance.now() : 0;
}

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
  // BUG-787: FIFO of the last RATE_WINDOW_TICKS recordTickCompleted()
  // timestamps, used ONLY to derive the windowed achievedRate — never read
  // as a source of truth for anything else. Caller-supplied timestamps only
  // (see recordTickCompleted's nowMs param), same testability discipline as
  // the rest of this file.
  private completedTimes: number[] = [];
  // opus-round-bug787 F1: the ticksScheduled VALUE observed at the moment of
  // the last completion (or 0, meaning "never" / "since the last reset()/
  // settle()"). scheduledSinceLastCompletion = ticksScheduled - this. Reset
  // to 0 alongside ticksScheduled by settle()/reset() so pause never reads
  // as a dead worker.
  private scheduledAtLastCompletion = 0;
  // opus-round-bug787 F4: a separate, non-settling accumulator for the
  // "slipped since load" popover figure. settle() folds the CURRENT backlog
  // into this before zeroing ticksScheduled/ticksCompleted, so a session
  // that pauses after genuinely losing ticks keeps an honest running total
  // instead of resetting to 0 on every pause. Only reset()/resetAll() (a
  // fresh load) clear it.
  private slippedSinceLoadAccum = 0;
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
   *  backlog is clamped at 0 rather than allowed to go negative.
   *
   *  BUG-787: `nowMs` is OPTIONAL and defaults to performance.now() — a
   *  convenience default-arg wrapper in the same spirit as the file header's
   *  testability rule (the module never reads the clock internally except in
   *  such wrappers). store.tsx's existing zero-arg call sites are unaffected;
   *  tests that need deterministic windowed-rate behaviour pass an explicit
   *  fabricated timestamp so the sliding-window math never depends on real
   *  wall-clock time. */
  recordTickCompleted(nowMs: number = defaultNowMs()): void {
    this.ticksCompleted++;
    this.completedTimes.push(nowMs);
    if (this.completedTimes.length > RATE_WINDOW_TICKS) this.completedTimes.shift();
    // opus-round-bug787 F1: a real completion just landed, so the
    // dead-worker clock resets — "no ticks since" starts counting fresh
    // from the CURRENT scheduled count.
    this.scheduledAtLastCompletion = this.ticksScheduled;
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
   *  SPEED_MS[state.speed] may have changed (which per that call site's own
   *  comment may be a same-value re-fire, not necessarily an actual change).
   *
   *  opus-round-bug787 F3 (round REJECT, "every speed-up shows a false
   *  4-9s red"): when the interval ACTUALLY CHANGES, the achieved-rate
   *  window must be cleared — its stored timestamps reflect the OLD
   *  cadence, and comparing them against the NEW demandedRate (1000/ms)
   *  produces a spurious low/high ratio until enough new completions at the
   *  new cadence replace the old ones (which, at a slow interval, could take
   *  several real seconds — exactly the "4-9s false red" the round reported
   *  on a speed-up). A same-value call (the common re-fire case) is a
   *  no-op here, same as before — only a genuine change clears history. */
  setIntervalMs(ms: number): void {
    if (ms > 0) {
      if (this.intervalMs !== null && this.intervalMs !== ms) {
        this.completedTimes = [];
      }
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
    // opus-round-bug787 F4: fold the CURRENT backlog into the non-settling
    // "slipped since load" accumulator BEFORE zeroing the counters below —
    // otherwise a session that pauses after genuinely losing ticks would
    // read "0 slipped" in the popover on every pause, which is exactly as
    // dishonest as the chip's old backlog-forever-ratchet problem, just in
    // the opposite direction (under- instead of over-reporting).
    this.slippedSinceLoadAccum += Math.max(0, this.ticksScheduled - this.ticksCompleted);
    this.ticksScheduled = 0;
    this.ticksCompleted = 0;
    // BUG-787: also empty the windowed-rate buffer — a paused engine has no
    // "current achieved rate" to report (there is no demand right now), same
    // pause-honesty principle as zeroing the scheduled/completed counters
    // above. Resuming starts the window fresh rather than judging the first
    // few post-resume ticks against stale pre-pause timestamps.
    this.completedTimes = [];
    // opus-round-bug787 F1: the dead-worker clock resets alongside
    // ticksScheduled — a paused engine is never "dead", it is paused (kept
    // green by the TopBar chip's own pause branch regardless, but this
    // keeps the tracker's own deadWorker field honest too).
    this.scheduledAtLastCompletion = 0;
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
    const { meanRate, medianRate } = this.computeAchievedRates(nowMs);
    const demandedRate = this.intervalMs !== null && this.intervalMs > 0 ? 1000 / this.intervalMs : null;
    const scheduledSinceLastCompletion = Math.max(0, this.ticksScheduled - this.scheduledAtLastCompletion);
    // opus-reround-bug787 F2: classify EACH statistic against demandedRate,
    // then take the WORSE of the two — see classifyRate's own comment for
    // the full two-round exploit history this guards against.
    const rateClass = worseClass(classifyRate(meanRate, demandedRate), classifyRate(medianRate, demandedRate));
    return {
      ticksScheduled: this.ticksScheduled,
      ticksCompleted: this.ticksCompleted,
      backlog,
      slippedSinceLoad: this.slippedSinceLoadAccum + backlog,
      lastTickMs: this.lastTickMs,
      intervalMs: this.intervalMs,
      ratio,
      ratioClass: classifyRatio(ratio),
      backlogClass: classifyBacklog(backlog),
      achievedRate: meanRate,
      achievedRateMedian: medianRate,
      demandedRate,
      rateClass,
      scheduledSinceLastCompletion,
      deadWorker: scheduledSinceLastCompletion >= DEAD_WORKER_SCHEDULED_TICKS,
      worstStallMs: this.worstStallMs,
      recentStallMs: this.recentStall(nowMs),
    };
  }

  /** opus-reround-bug787 F1/F5 (shared by both rate statistics below): pick
   *  the completion-timestamp sample computeAchievedRates works from.
   *
   *  F1 — the time cut is relative to the LIVE query time `nowMs`
   *  (snapshot()'s own parameter), never to completedTimes' own newest
   *  entry. The OLD self-referential window froze the last healthy rate
   *  forever once completions stopped arriving (a dead worker with old
   *  history still on file kept reading its last good rate indefinitely —
   *  "45,000 scheduled / 0 completed after 2h reads Engine: OK"). Now, as
   *  real time passes with no new completions, the in-window sample shrinks
   *  toward empty and both rates decay to null on their own (which reads
   *  green under classifyRate's "no data" convention — the actual "is it
   *  dead" signal is `deadWorker`, computed independently in snapshot()
   *  from the scheduled/completed counters, which is what correctly forces
   *  red for exactly this case; see engineLagClassOf).
   *
   *  F5 — floored at MIN_RATE_SAMPLES: if the live time-window sample is
   *  thinner than that AND the tracker's total history (bounded by
   *  RATE_WINDOW_TICKS) has at least that many entries, fall back to the
   *  most recent MIN_RATE_SAMPLES timestamps regardless of age, so a
   *  legitimately slow cadence (few completions land inside a plain 10s
   *  window) doesn't let one hiccup dominate a too-thin sample. This can
   *  reach slightly outside RATE_WINDOW_MS, which is intentional — the
   *  `deadWorker` signal (not this function) is what must catch an actually
   *  dead engine, so extending the window here to stabilise the statistic
   *  for a merely-slow-but-alive engine is safe. Returns null when fewer
   *  than 2 timestamps are available (can't derive a gap/span from one
   *  point). */
  private selectRateSample(nowMs: number): number[] | null {
    if (this.completedTimes.length < 2) return null;
    const withinWindow = this.completedTimes.filter((t) => nowMs - t <= RATE_WINDOW_MS);
    const sample =
      withinWindow.length >= MIN_RATE_SAMPLES || this.completedTimes.length < MIN_RATE_SAMPLES
        ? withinWindow
        : this.completedTimes.slice(-MIN_RATE_SAMPLES);
    return sample.length >= 2 ? sample : null;
  }

  /** BUG-787, reworked opus-round-bug787 then opus-reround-bug787 (F2):
   *  compute BOTH achieved-rate statistics (ticks/sec) from the SAME sample
   *  (selectRateSample above) — a MEAN-over-span throughput (total
   *  completions in the sample / the real time they spanned — an honest,
   *  ungameable average) and a MEDIAN inter-completion-gap rate (immune to
   *  a small minority of arbitrarily severe outliers). snapshot() classifies
   *  each independently and takes the worse — see classifyRate's comment
   *  for the full exploit history and why BOTH statistics are needed. */
  private computeAchievedRates(nowMs: number): { meanRate: number | null; medianRate: number | null } {
    const sample = this.selectRateSample(nowMs);
    if (sample === null) return { meanRate: null, medianRate: null };

    const spanMs = sample[sample.length - 1] - sample[0];
    const meanRate = spanMs > 0 ? (sample.length - 1) / (spanMs / 1000) : null;

    const gaps: number[] = [];
    for (let i = 1; i < sample.length; i++) gaps.push(sample[i] - sample[i - 1]);
    gaps.sort((a, b) => a - b);
    const mid = Math.floor(gaps.length / 2);
    const medianGapMs = gaps.length % 2 === 0 ? (gaps[mid - 1] + gaps[mid]) / 2 : gaps[mid];
    const medianRate = medianGapMs > 0 ? 1000 / medianGapMs : null;

    return { meanRate, medianRate };
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
    this.completedTimes = []; // BUG-787: the windowed-rate buffer empties too
    this.scheduledAtLastCompletion = 0; // opus-round-bug787 F1
    // opus-round-bug787 F4: reset() is "a fresh load" (mirrors a fresh
    // SimProvider mount per this method's own top-level doc comment) — the
    // ONLY place slippedSinceLoadAccum is allowed to zero, per its name.
    this.slippedSinceLoadAccum = 0;
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
