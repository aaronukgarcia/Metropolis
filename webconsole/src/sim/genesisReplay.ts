// genesisReplay.ts — FEAT-1972079897 inc1: deterministic GENESIS replay core.
//
// Design brief: docs/planning/hard-reset-replay-brief.md.
//
// The savepoint path in replay.ts replays a journal TAIL onto a frozen SimState
// SNAPSHOT — old-rules numbers baked in. That resurrects the old city; it does
// NOT re-derive it under new engine rules. This module is the complementary
// GENESIS replay: start from initialState() on the CURRENT engine and re-apply
// every journaled action in recorded order through the SAME pure reducer, so the
// city is re-derived on today's build (hard-reset + replay from genesis).
//
// Purity: no Date.now / Math.random, no I/O, no React, and the input journal is
// never mutated (reducer is pure; we only read journal.entries in order).
//
// inc1 scope is the HEADLESS CORE only — no UI, no build stamping, no defensive
// per-action skip/crash-catch (that is inc2), and no sparse-log / tick-synthesis
// optimization (that is a LATER increment — see the note on replayFromGenesis).

import type { SimState } from './types.ts';
import type { Journal } from './journal.ts';
import { initialState, reducer, setReplayMode } from './engine.ts';
import { computeRoadConnectivity } from './data.ts';

/**
 * Replay a journal from GENESIS: start at initialState() (NOT a snapshot) and
 * apply every recorded action in order through the pure reducer, returning the
 * final state. Re-derives the city under the CURRENT engine rules.
 *
 * The input journal is treated as read-only — entries are applied in their
 * recorded order and never reordered or mutated.
 *
 * inc1 FIDELITY NOTE: the journal already contains explicit `tick` entries
 * (journal.isStateAffecting classifies `tick` as state-affecting), so applying
 * them reproduces timing exactly. We deliberately do NOT synthesize tick
 * advances here — that keeps genesis replay byte-faithful to the recorded log.
 * The sparse-action-log optimization from §4.1/§4.2 of the brief (store only
 * sparse player actions, synthesize the ticks between them) is a LATER
 * increment and is intentionally out of scope for inc1.
 */
export function replayFromGenesis(journal: Journal): SimState {
  let state = initialState();
  // BUG-460 FIX A: no UI reads happen between actions during a headless replay,
  // so the reducer's per-action roadConnectivity recompute is pure allocation
  // churn here — skip it for the duration of the loop and do ONE final recompute
  // below so the returned state is correct for the live game to resume from.
  // Cleared in finally even on a thrown error (setReplayMode is a shared flag —
  // leaving it set would silently break connectivity freshness for normal play).
  setReplayMode(true);
  try {
    for (const entry of journal.entries) {
      state = reducer(state, entry.action);
    }
  } finally {
    setReplayMode(false);
  }
  return { ...state, roadConnectivity: computeRoadConnectivity(state) };
}

/**
 * FEAT-2326609723 (Play Mode) — a LATCHED (playModeLatched === true) session
 * is a deliberate sandbox deviation (a trillion-unit non-economy injection),
 * not a valid economy run — it must never be treated as a genesis-replay/AB
 * determinism REFERENCE (a "golden" state other runs are compared against).
 * The replay CORE itself still replays a play-mode journal faithfully
 * (determinism is unaffected — the latch and injection are ordinary
 * deterministic state transitions, no rand/time), so this is a guard for
 * CALLERS deciding whether a given end-state is fit to serve as a comparison
 * baseline, not a restriction on replayFromGenesis itself. Pure, no I/O.
 */
export function canUseAsReplayReference(state: SimState): boolean {
  return !state.playModeLatched;
}

/**
 * Deterministic stable serialization: JSON with object keys sorted recursively,
 * so two structurally-equal states compare byte-identical regardless of key
 * insertion order. Used as the determinism-self-test oracle (brief §4.5).
 * Pure — no Date/Math/randomness.
 */
export function stableStringify(value: unknown): string {
  return JSON.stringify(value, replacerSortingKeys(value));
}

function replacerSortingKeys(root: unknown): (key: string, val: unknown) => unknown {
  // JSON.stringify visits nested values; for every plain object we return a new
  // object whose keys are inserted in sorted order, which fixes serialization
  // order deterministically. Arrays keep their (meaningful) order.
  void root;
  return (_key: string, val: unknown): unknown => {
    if (val && typeof val === 'object' && !Array.isArray(val)) {
      const src = val as Record<string, unknown>;
      const sorted: Record<string, unknown> = {};
      for (const k of Object.keys(src).sort()) {
        sorted[k] = src[k];
      }
      return sorted;
    }
    return val;
  };
}

/**
 * Determinism self-test (brief §4.5): replay the SAME journal from genesis twice
 * and confirm the two final states are byte-identical under stable JSON. Proves
 * the replay is deterministic on THIS build — a stray Date.now / Math.random in
 * engine code (GR#21 already bans them) would surface here as a mismatch.
 *
 * Returns true iff the two independent replays are byte-identical.
 */
export function replayIsDeterministic(journal: Journal): boolean {
  const first = replayFromGenesis(journal);
  const second = replayFromGenesis(journal);
  return stableStringify(first) === stableStringify(second);
}

// ---------------------------------------------------------------------------
// inc2 — CROSS-BUILD REBUILD path (brief §4.3-4.5).
//
// inc1 gave us the exact-fidelity genesis core. inc2 adds the machinery for
// replaying a journal captured under an OLD build onto the CURRENT engine, where
// two new truths hold (brief §3):
//   1. deterministic != identical across a rules change — the replayed city is
//      the one your actions produce under the CORRECTED rules, so we report
//      before/after metrics and NEVER claim pixel-identity.
//   2. some past actions may be invalid under new rules — replay must apply each
//      action DEFENSIVELY: skip-and-log an action the new engine rejects rather
//      than aborting the whole rebuild, and catch any hard crash.
// All of the below is PURE (no Date.now / Math.random / I/O): the report envelope
// timestamps and recordError() calls live in the UI/store layer, not here.
// ---------------------------------------------------------------------------

/**
 * Decide whether a persisted save needs a cross-build rebuild. Pure string
 * compare so it is trivially testable and can never itself introduce
 * nondeterminism.
 *
 * Returns true ONLY when both versions are known and DIFFER. When either side is
 * missing (an unstamped legacy save, or the running build is somehow unknown) we
 * return false: we cannot prove a rules change, so we do not nag the player — the
 * normal snapshot-restore path handles it, exactly as before inc2.
 */
export function needsRebuild(
  savedVersion: string | null | undefined,
  currentVersion: string | null | undefined
): boolean {
  if (!savedVersion || !currentVersion) return false;
  return savedVersion !== currentVersion;
}

/**
 * BUG-468: which DIRECTION a cross-build change goes, so the prompt can be honest
 * and the store can avoid forcing an endless rebuild on a REGRESSION.
 *
 * Parses the "v0.3.0.193" badge form (leading "v" optional) into its numeric
 * segments and compares them positionally:
 *   - 'same'       — identical (no prompt needed)
 *   - 'upgrade'    — the saved city is OLDER than the running build (the normal
 *                    forward case: replaying on the newer engine makes sense)
 *   - 'regression' — the saved city is NEWER than the running build (the dogfood
 *                    case Aaron hit: an older bundle is running behind a save
 *                    stamped by a newer live badge — DON'T force a rebuild)
 *   - 'unknown'    — a side is missing or non-numeric; caller falls back to the
 *                    plain "differs" copy.
 * Pure: no I/O, no clock, no randomness.
 */
export function classifyVersionChange(
  savedVersion: string | null | undefined,
  currentVersion: string | null | undefined
): 'same' | 'upgrade' | 'regression' | 'unknown' {
  if (!savedVersion || !currentVersion) return 'unknown';
  if (savedVersion === currentVersion) return 'same';
  const a = parseVersionSegments(savedVersion);
  const b = parseVersionSegments(currentVersion);
  if (!a || !b) return 'unknown';
  const len = Math.max(a.length, b.length);
  for (let i = 0; i < len; i++) {
    const x = a[i] ?? 0;
    const y = b[i] ?? 0;
    if (x < y) return 'upgrade';
    if (x > y) return 'regression';
  }
  return 'same';
}

/** Parse "v0.3.0.193" / "0.3.0.193" into [0,3,0,193]. Non-numeric → null. */
function parseVersionSegments(v: string): number[] | null {
  const trimmed = v.trim().replace(/^v/i, '');
  if (trimmed.length === 0) return null;
  const parts = trimmed.split('.');
  const nums: number[] = [];
  for (const p of parts) {
    if (!/^\d+$/.test(p)) return null;
    nums.push(Number(p));
  }
  return nums.length > 0 ? nums : null;
}

/** One journalled action skipped during a defensive rebuild because it threw. */
export interface SkippedAction {
  /** Position in journal.entries of the skipped action. */
  index: number;
  /** The recorded game tick the action was stamped with. */
  tick: number;
  /** The action's discriminant (e.g. 'place', 'policy') for the report. */
  type: string;
  /** The error message the new engine produced when the action was applied. */
  error: string;
}

/** Result of a defensive genesis rebuild. */
export interface DefensiveReplayResult {
  /** The reconstructed state (best-effort: skipped actions left it unchanged). */
  state: SimState;
  /** Actions the NEW rules rejected — skipped-and-logged, never fatal. */
  skipped: SkippedAction[];
  /**
   * True if replay hit a non-recoverable crash OUTSIDE a single action (e.g.
   * initialState() itself threw). Per-action throws are captured in `skipped`,
   * not here — those are expected under a rules change (brief §3.2).
   */
  crashed: boolean;
  /** Diagnostic message when crashed=true (empty otherwise). */
  crashError: string;
}

/**
 * Injection seam for the defensive replayer, so tests can simulate "this action
 * is invalid under the NEW rules" (a reducer that throws on a marked action)
 * without needing a second real engine build. Defaults to the real pure engine.
 */
export interface ReplayEngine {
  init: () => SimState;
  reduce: (state: SimState, action: Journal['entries'][number]['action']) => SimState;
}

const REAL_ENGINE: ReplayEngine = { init: initialState, reduce: reducer };

/**
 * Defensive genesis replay (brief §3.2, §4.4 step 4). Start from genesis on the
 * CURRENT engine and apply every journalled action in order, but wrap EACH action
 * so a throw (an action the new rules reject) is caught: the action is skipped,
 * the pre-action state is kept, the failure is recorded, and replay continues.
 * A crash OUTSIDE a single action (init throwing) is caught too and flagged.
 *
 * The input journal is never mutated. Purity is preserved: no clock, no random.
 */
export function replayFromGenesisDefensive(
  journal: Journal,
  engine: ReplayEngine = REAL_ENGINE
): DefensiveReplayResult {
  const skipped: SkippedAction[] = [];
  let state: SimState;
  try {
    state = engine.init();
  } catch (e) {
    // Genesis itself could not be constructed — nothing to salvage.
    return {
      state: REAL_ENGINE === engine ? initialState() : (undefined as unknown as SimState),
      skipped,
      crashed: true,
      crashError: errMsg(e),
    };
  }

  // BUG-460 FIX A: see replayFromGenesis above — skip the wrapper's per-action
  // roadConnectivity recompute for the duration of this headless replay loop;
  // cleared in finally (even on a thrown error) and followed by one final
  // recompute so the returned state is correct for the live game to resume from.
  // Only meaningful when `engine` is the REAL reducer — a test-injected engine
  // ignores the module-scoped flag entirely.
  setReplayMode(true);
  try {
    journal.entries.forEach((entry, index) => {
      try {
        state = engine.reduce(state, entry.action);
      } catch (e) {
        // Invalid under the new rules — skip-and-log, keep the prior state, carry on.
        skipped.push({
          index,
          tick: entry.tick,
          type: (entry.action as { type?: string }).type ?? 'unknown',
          error: errMsg(e),
        });
      }
    });
  } finally {
    setReplayMode(false);
  }

  if (engine === REAL_ENGINE) {
    state = { ...state, roadConnectivity: computeRoadConnectivity(state) };
  }

  return { state, skipped, crashed: false, crashError: '' };
}

/** Stable error-to-string that never itself throws. */
function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message;
  try {
    return String(e);
  } catch {
    return 'unknown error';
  }
}

// ---------------------------------------------------------------------------
// FEAT-1972079917 — Chunked replay with progress callback (inc2-plus).
//
// Enables the UI to show live progress during a long genesis replay without
// blocking the event loop. Instead of replaying all actions in a single loop,
// we yield progress after every N actions, allowing requestAnimationFrame to
// run and render updates between chunks.
// ---------------------------------------------------------------------------

/** Progress update from a chunked replay. */
export interface ReplayProgress {
  /** Number of actions applied so far. */
  actionsDone: number;
  /** Total actions in the journal. */
  actionsTotal: number;
  /** Human-readable phase label (e.g. "Replaying roads... 1,240/3,900 actions"). */
  phaseLabel: string;
}

/**
 * Actions per chunk (UPPER BOUND): a UI frame typically runs ~60fps = ~16ms.
 * This is a CEILING on how many actions one chunk may contain — the actual
 * chunk boundary is whichever of this count or CHUNK_TIME_BUDGET_MS (below)
 * is reached FIRST. Tunable per performance.
 */
const ACTIONS_PER_CHUNK = 50;

/**
 * BUG-617 (P1, 2026-09-03, Aaron's live 1.4M-pop/~13,000-building city):
 * ACTIONS_PER_CHUNK alone assumed every action costs roughly the same — true
 * for a small/starter city, catastrophically false at scale. The reducer's
 * per-action cost (dominated by 'tick', which walks every building doing
 * flows/monitors/growth/road-connectivity work) is O(buildings): measured
 * ~0.7ms per 1,000 buildings on this machine, i.e. ~9ms/tick at 13,000
 * buildings and ~17ms/tick at 26,000 — LINEAR in building count, not
 * quadratic, but a fixed ACTIONS_PER_CHUNK=50 batches 50 of those together
 * regardless, so a chunk anywhere in a mature 13k-building city's tail costs
 * ~450-600ms of uninterrupted main-thread time — 10-30x the ~16-50ms a
 * chunk is supposed to cost, and the exact mechanism behind "the tab freezes
 * for minutes" during a hard-reset-replay / cross-build rebuild (the
 * documented dogfood hot-upgrade workflow — a hot-upgraded badge keeps the
 * OLD engine running until a hard reset + genesis replay picks up new
 * logic), which walks the WHOLE journal (placements interleaved with many
 * thousands of ticks) from an empty city up to full scale, so the LATTER
 * portion of any real replay is exactly this worst case.
 *
 * FIX: chunk by a TIME BUDGET as well as the existing action-count ceiling —
 * whichever bound is hit FIRST ends the chunk. A chunk of cheap actions (a
 * small/starter city, or a burst of 'place' calls) still batches up to
 * ACTIONS_PER_CHUNK exactly as before (preserves the existing multi-boundary
 * coverage test at 600 cheap actions / 50-per-chunk = 12 chunks); a chunk of
 * expensive actions (ticks at high building count) now yields far short of
 * 50, bounding wall-clock chunk cost regardless of city size. The time check
 * runs AFTER each individual action (not pre-estimated), so worst-case
 * overshoot is the cost of exactly one action — at 26,000 buildings that is
 * ~17ms, comfortably under the <100ms scale-test bound.
 *
 * PURITY NOTE (GR#21): performance.now() here decides ONLY the chunk
 * boundary (how many actions this yield contains) — it never influences
 * which actions are applied or in what order, so the REPLAYED STATE stays
 * fully deterministic and chunking-invariant, exactly as before (proven by
 * test/chunked-replay.test.mjs's byte-identity assertions and the new
 * test/bug617-chunked-replay-scale.test.mjs timing-vs-correctness split).
 */
const CHUNK_TIME_BUDGET_MS = 40;

/**
 * Chunked defensive genesis replay: yields progress after each chunk of actions
 * so the UI can display live progress without blocking. The replay loop runs
 * via a generator — calling onProgress(result) lets the UI render updates
 * between chunks.
 *
 * Like replayFromGenesisDefensive, this is PURE: no clock, no random.
 * The chunking does NOT change the final state — the same actions are applied
 * in the same order, just yielded in smaller pieces.
 *
 * Usage:
 *   const gen = replayFromGenesisDefensiveChunked(journal);
 *   for (const progress of gen) {
 *     onProgress(progress);  // update UI
 *   }
 *   const result = gen.return(value);  // final DefensiveReplayResult
 *
 * Because generators are stateful, each call to this function creates a fresh
 * generator with its own replay state — safe to use concurrently (if needed).
 */
export function* replayFromGenesisDefensiveChunked(
  journal: Journal,
  engine: ReplayEngine = REAL_ENGINE
): Generator<ReplayProgress, DefensiveReplayResult, void> {
  const skipped: SkippedAction[] = [];
  let state: SimState;
  try {
    state = engine.init();
  } catch (e) {
    // Genesis itself could not be constructed — nothing to salvage.
    return {
      state: REAL_ENGINE === engine ? initialState() : (undefined as unknown as SimState),
      skipped,
      crashed: true,
      crashError: errMsg(e),
    };
  }

  const entries = journal.entries;
  const total = entries.length;

  // BUG-460 FIX A: see replayFromGenesis above — skip the wrapper's per-action
  // roadConnectivity recompute for the duration of this headless replay
  // (cleared in finally below, even on a thrown error, and followed by one
  // final recompute so the returned state is correct for live play to resume
  // from). Only meaningful when `engine` is the REAL reducer.
  setReplayMode(true);
  try {
    // BUG-617: yield after ACTIONS_PER_CHUNK actions OR CHUNK_TIME_BUDGET_MS of
    // wall-clock, whichever comes first — see the constants' header comments.
    let i = 0;
    while (i < total) {
      const chunkClockStart = performance.now();
      let processedInChunk = 0;
      while (i < total && processedInChunk < ACTIONS_PER_CHUNK) {
        const entry = entries[i];
        try {
          state = engine.reduce(state, entry.action);
        } catch (e) {
          // Invalid under the new rules — skip-and-log, keep the prior state, carry on.
          skipped.push({
            index: i,
            tick: entry.tick,
            type: (entry.action as { type?: string }).type ?? 'unknown',
            error: errMsg(e),
          });
        }
        i++;
        processedInChunk++;
        // BUG-617: time-budget early exit — checked AFTER every single action so
        // an expensive action (a 'tick' at high building count) can never be
        // followed by another before this chunk yields. Never breaks with zero
        // actions processed (processedInChunk >= 1 is guaranteed by the outer
        // while condition + this being checked only after the reduce above), so
        // forward progress is always made even if one action alone exceeds the
        // budget.
        if (performance.now() - chunkClockStart >= CHUNK_TIME_BUDGET_MS) break;
      }
      const chunkEnd = i;

      // After each chunk, derive a human-readable phase label from the action
      // type of the action we just applied (if any in this chunk).
      let phaseLabel = 'Replaying actions';
      if (chunkEnd > 0) {
        const lastAction = entries[chunkEnd - 1].action as { type?: string };
        const actionType = lastAction.type ?? 'unknown';
        // Capitalize and pluralize common action types.
        const capitalize = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);
        const plural = {
          place: 'Placing buildings',
          bulldoze: 'Bulldozing',
          tax: 'Adjusting taxes',
          tick: 'Advancing ticks',
          policy: 'Adjusting policies',
          road: 'Building roads',
          rail: 'Building rail',
          water: 'Building water',
          reset: 'Resetting',
        }[actionType] || `Replaying ${capitalize(actionType)}s`;
        phaseLabel = `${plural}... ${chunkEnd.toLocaleString()}/${total.toLocaleString()} actions`;
      }

      yield {
        actionsDone: chunkEnd,
        actionsTotal: total,
        phaseLabel,
      };
    }
  } finally {
    setReplayMode(false);
  }

  if (engine === REAL_ENGINE) {
    state = { ...state, roadConnectivity: computeRoadConnectivity(state) };
  }

  return { state, skipped, crashed: false, crashError: '' };
}

/**
 * Module-level flag: true while a rebuild is actively running. Used by liveVersion.tsx
 * to suppress hot-swap during a rebuild (BUG-435).
 */
export let rebuildInProgress = false;

/**
 * Subscribers notified when rebuildInProgress changes. Used for reactive updates.
 */
const rebuildInProgressListeners: Set<(inProgress: boolean) => void> = new Set();

/**
 * Subscribe to changes in rebuildInProgress. Callback is called immediately with
 * the current value, then again whenever the flag changes.
 */
export function subscribeToRebuildProgress(callback: (inProgress: boolean) => void): () => void {
  callback(rebuildInProgress);
  rebuildInProgressListeners.add(callback);
  return () => {
    rebuildInProgressListeners.delete(callback);
  };
}

/**
 * Set the rebuild flag and notify listeners. Called by store.tsx to indicate
 * when a rebuild is starting and finishing.
 */
export function setRebuildInProgress(inProgress: boolean): void {
  if (rebuildInProgress === inProgress) return;
  rebuildInProgress = inProgress;
  rebuildInProgressListeners.forEach((cb) => cb(inProgress));
}

/** The headline before/after numbers a rebuild report compares. */
export interface RebuildMetrics {
  tick: number;
  population: number;
  funds: number;
  buildings: number;
}

/** Extract the report metrics from a SimState. Pure, tolerant of a null state. */
export function metricsOf(state: SimState | null | undefined): RebuildMetrics {
  return {
    tick: state?.tick ?? 0,
    population: state?.population ?? 0,
    funds: state?.funds ?? 0,
    buildings: state?.buildings?.length ?? 0,
  };
}

/**
 * A rebuild report (brief §3.1, §4.4 step 5): the OLD snapshot's numbers vs the
 * freshly-replayed city's numbers, plus any actions skipped as invalid under the
 * new rules.
 *
 * CRITICAL HONESTY CONTRACT: this report DELIBERATELY does not assert, imply, or
 * verify pixel-identity. `identical` is a plain informational flag ("all headline
 * deltas happen to be zero"), not a success criterion — a rules-change build is
 * EXPECTED to yield a different (corrected) city, and that is the desirable
 * outcome, not a failure (brief §3). Consumers must present divergence as normal.
 */
export interface RebuildReport {
  before: RebuildMetrics;
  after: RebuildMetrics;
  /** after − before for each metric (signed; divergence is expected, not an error). */
  deltas: RebuildMetrics;
  /** Actions skipped as invalid under the new rules. */
  skipped: SkippedAction[];
  /**
   * Informational only: true when every headline delta is zero. NOT a claim of
   * full-state identity and NOT a pass/fail gate — see the contract above.
   */
  identical: boolean;
}

/**
 * Build a rebuild report from the old (snapshot) state, the new (replayed) state,
 * and the list of skipped actions. Pure and side-effect free.
 */
export function rebuildReport(
  oldState: SimState | null | undefined,
  newState: SimState | null | undefined,
  skipped: SkippedAction[] = []
): RebuildReport {
  const before = metricsOf(oldState);
  const after = metricsOf(newState);
  const deltas: RebuildMetrics = {
    tick: after.tick - before.tick,
    population: after.population - before.population,
    funds: after.funds - before.funds,
    buildings: after.buildings - before.buildings,
  };
  const identical =
    deltas.tick === 0 &&
    deltas.population === 0 &&
    deltas.funds === 0 &&
    deltas.buildings === 0;
  return { before, after, deltas, skipped, identical };
}

// ---------------------------------------------------------------------------
// FEAT-1972079917 bounce-fix / BUG-435 estate (round r1 REJECT follow-up).
// Pure helpers extracted so the store's state machine decisions (generation
// guard, live ETA) are unit-testable without mounting React.
// ---------------------------------------------------------------------------

/** One progress observation: how many actions were done, and when (ms clock,
 * e.g. performance.now()). Caller supplies the clock — this module stays pure. */
export interface ProgressSample {
  actionsDone: number;
  timestamp: number;
}

/** Never show an ETA off less than this much observed wall-clock — a single
 * chunk's timing is noisy; require a real, held sample window (BAR-2: derive
 * from LIVE actions/sec, never a canned animation). */
const MIN_ETA_SAMPLE_WINDOW_MS = 1000;

/** Format a millisecond duration as a short "1m 10s" / "45s" label. */
function formatDurationShort(ms: number): string {
  const totalSec = Math.max(0, Math.round(ms / 1000));
  const m = Math.floor(totalSec / 60);
  const s = totalSec % 60;
  return m > 0 ? `${m}m ${s}s` : `${s}s`;
}

/**
 * Derive a human "~Xm Ys remaining" label from a history of progress samples,
 * using the observed actions/sec between the earliest and latest sample as the
 * rate. Returns null when there isn't yet enough signal to trust (fewer than 2
 * samples, no time elapsed, an under-window sample set, or no forward
 * progress) — callers should render "estimating…" or nothing in that case.
 *
 * Pure: takes timestamps in, never reads a clock itself.
 */
export function estimateRemainingLabel(
  samples: ProgressSample[],
  actionsTotal: number
): string | null {
  if (samples.length < 2 || actionsTotal <= 0) return null;
  const first = samples[0];
  const last = samples[samples.length - 1];
  const elapsedMs = last.timestamp - first.timestamp;
  const doneDelta = last.actionsDone - first.actionsDone;
  if (elapsedMs < MIN_ETA_SAMPLE_WINDOW_MS || doneDelta <= 0) return null;
  const rate = doneDelta / elapsedMs; // actions per ms
  const remaining = actionsTotal - last.actionsDone;
  if (remaining <= 0) return '~0s remaining';
  const remainingMs = remaining / rate;
  return `~${formatDurationShort(remainingMs)} remaining`;
}

/**
 * Generation guard (the concurrent-replay race, r1 attacker finding): decide
 * whether an in-flight chunked-replay chain has been superseded by a newer one
 * (a fresh onRebuild/onRetry dispatch, or a watchdog-fired stall) and must
 * abort without any further setState/persist. `chainGen` is the generation the
 * chain captured when it started; `currentGen` is the live counter's value at
 * the moment of the check. Pure integer compare, extracted so a test can drive
 * gen-mismatch without mounting the store's React state machine.
 */
export function isStaleRebuildChain(chainGen: number, currentGen: number): boolean {
  return chainGen !== currentGen;
}
