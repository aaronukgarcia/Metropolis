// FEAT-webworker-sim-offload Stage 1 / FEAT-2326609734 queue-depth groundwork
// (2026-09-02) — a tiny, framework-free counter tracking the worker's
// backlog: actions/ticks posted to the worker but not yet acknowledged.
// Deliberately pure/side-effect-free (no React, no Worker, no timers) so it
// is independently unit-testable (test/simworker-offload.test.mjs: "enqueue
// N without processing shows N, drains to 0") and reusable by store.tsx's
// real tick-driver wiring without either side needing to know about the
// other's plumbing.
export interface QueueDepthTracker {
  /** Record one more posted-but-unacknowledged item. */
  enqueue(): void;
  /** Record one item's acknowledgement (a worker reply/patch arrived).
   *  Never goes below 0 — an unexpected extra ack (e.g. a stale/duplicate
   *  reply after a resync) is clamped rather than producing a negative
   *  backlog, which would misreport the UI as "ahead" instead of "caught
   *  up". */
  drain(): void;
  /** Current backlog: 0 means no request is currently posted-and-unacked.
   *  NOTE: 0 here does NOT by itself mean "caught up" — see supersedeStreak
   *  below (N1 fix). A tick that gets superseded/discarded drains this
   *  counter (its slot is no longer outstanding) even though the SIM CLOCK
   *  made no progress; depth() alone cannot distinguish "genuinely idle"
   *  from "churning through discarded ticks under sustained contention". */
  depth(): number;
  /** Reset to 0 — used when the worker is torn down/recreated (e.g. a
   *  reset/load-save invalidates every outstanding request). */
  reset(): void;
  /**
   * N1 fix / FEAT-2326609734 AC-7 honesty gap (independent round 2 REJECT,
   * 2026-09-02) — report the offload controller's CURRENT consecutive
   * supersede streak (simWorkerOffloadController.ts's supersedeStreak).
   * Before this, depth() alone drove the UI readout, and depth() reads 0
   * both when the sim is genuinely caught up AND when every tick is being
   * discarded via supersede (each supersede drains its own enqueued slot
   * immediately) — the exact "queue-depth silently reads caught up while
   * the clock is stalled" defect the round's attacker flagged twice. Call
   * this every time the controller's supersedeStreak changes (store.tsx);
   * a UI readout should treat a nonzero streak as "behind / catching up",
   * never as "caught up", regardless of what depth() reads.
   */
  reportSupersedeStreak(streak: number): void;
  /** The most recently reported consecutive-supersede streak. 0 whenever
   *  the worker offload is disabled/unavailable (nothing ever reports a
   *  nonzero streak) or the sim is genuinely progressing normally. */
  supersedeStreak(): number;
}

export function createQueueDepthTracker(): QueueDepthTracker {
  let count = 0;
  let streak = 0;
  return {
    enqueue() {
      count += 1;
    },
    drain() {
      count = Math.max(0, count - 1);
    },
    depth() {
      return count;
    },
    reset() {
      count = 0;
      streak = 0;
    },
    reportSupersedeStreak(next) {
      streak = next;
    },
    supersedeStreak() {
      return streak;
    },
  };
}

// ---------- global singleton (mirrors perfhud.ts's getGlobalTickTracker) ----
//
// store.tsx (the worker owner) writes to this via enqueue()/drain(); a UI
// readout (PerfHud's existing HUD slot — FEAT-2326609734) reads depth()
// without needing SimContext plumbing, same idiom as the existing
// getGlobalTickTracker()/globalFpsTracker pair in perfhud.ts. A module-level
// singleton is safe here for the same reason it is there: one running game
// per page, one worker, one backlog counter.
let globalWorkerQueueTracker: QueueDepthTracker | null = null;

/** Lazily-created global backlog tracker, shared across store.tsx (writer)
 *  and any UI readout (reader). Zero-lag / worker-disabled state simply
 *  never calls enqueue(), so depth() correctly reads 0 forever — no worker
 *  in play, "0" is the honest and correct AC-7 zero-lag answer. */
export function getGlobalWorkerQueueTracker(): QueueDepthTracker {
  if (!globalWorkerQueueTracker) {
    globalWorkerQueueTracker = createQueueDepthTracker();
  }
  return globalWorkerQueueTracker;
}
