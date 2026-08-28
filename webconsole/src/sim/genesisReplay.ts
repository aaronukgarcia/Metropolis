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
import { initialState, reducer } from './engine.ts';

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
  for (const entry of journal.entries) {
    state = reducer(state, entry.action);
  }
  return state;
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
