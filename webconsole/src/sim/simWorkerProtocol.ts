// FEAT-webworker-sim-offload — Stage 1 / Landing 2 (2026-09-02): tick-only
// Web Worker offload message protocol + the PURE tick-application function
// shared by the real worker, the main-thread fallback path, and tests.
//
// Spec: docs/planning/acceptance/FEAT-webworker-sim-offload-2026-09-02.md
// (§2 Option C / Landing 2, §3 message protocol, §6 staging).
//
// GR#21 DETERMINISM: runTick() below is the ONLY place a tick gets computed,
// and it calls the SAME `reducer` export from engine.ts that the main-thread
// fallback path (store.tsx, worker unavailable/disabled) calls directly —
// there is exactly one reducer module, imported identically by main and by
// the worker bundle (simWorker.ts). No forked/duplicated tick logic exists
// anywhere in this feature. This is the function the determinism test
// (test/simworker-offload.test.mjs) calls directly to prove worker-path and
// main-path ticks are byte-identical, since jsdom/node --test cannot
// construct a real Worker (see simWorker.ts's header for that boundary).
//
// SCOPE NOTE (deliberate, documented tradeoff — spec §2/§3/§6): Landing 2
// sends the FULL current SimState with every tick request and receives the
// FULL resulting SimState back, rather than the byte-optimised targeted
// StatePatch protocol §3 describes for the eventual Option A destination
// (Landing 3/Stage 2). Ticks fire on a fixed interval (SPEED_MS), not per
// animation frame, so the structured-clone cost here is paid once per tick
// interval — nowhere near the reported per-pointer-move placement lag (the
// actual bug §1 describes). This keeps Landing 2's scope to "prove the
// worker lifecycle and message protocol" (its own §6 stated purpose)
// without also solving the harder incremental-diff problem, which needs the
// full reducer (including `place`) resident in the worker to be worth
// building — that is explicitly Landing 3's territory.
import { reducer } from './engine.ts';
import type { SimState } from './types.ts';

/** Main -> worker. */
export type MainToWorkerMessage =
  /** Run exactly one tick against `state` (a full snapshot of main's current
   *  live state at request time — see the scope note above) and reply with
   *  the resulting state. */
  | { type: 'runTick'; state: SimState; requestId: number };

/** Worker -> main. */
export type WorkerToMainMessage =
  /** The tick this `requestId` asked for has finished; `state` is the full
   *  post-tick SimState (advance() already applied, roadConnectivity fresh —
   *  see runTick below). */
  | { type: 'tickResult'; state: SimState; requestId: number };

/**
 * The worker's entire computational job, expressed as a pure function so it
 * is directly unit-testable without a real Worker/postMessage. The REAL
 * worker entry (simWorker.ts) is a thin `self.onmessage` wrapper around this
 * exact function; store.tsx's fallback path (no Worker, or the
 * `metropolis.webworker` flag off) calls `reducer(state, { type: 'tick' })`
 * directly for the SAME effect. Both routes call this one function (or the
 * identical reducer call it wraps), so "which thread ran it" cannot itself
 * be a determinism risk (GR#21) — the reducer has no RNG/Date.now (see
 * engine.ts's own GR#21 comments), so the same input state always produces
 * the same output state regardless of caller.
 */
export function runTick(state: SimState): SimState {
  return reducer(state, { type: 'tick' });
}
