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
   *  below (N1 fix). BUG-592 fix (2026-09-02): this counter now drains ONLY
   *  when the worker is ACTUALLY observed to finish a computation (a reply,
   *  an error, or teardown) — a superseded/discarded request does NOT drain
   *  it, because the worker itself is still crunching it (no cancellation
   *  channel exists). Before this fix, a supersede drained the slot
   *  immediately even though the underlying worker computation was still
   *  running, which both misreported an actual outstanding backlog as "0
   *  pending" AND (worse — the actual bug) let the caller post a SECOND
   *  real message on top of the still-running first one, unboundedly, under
   *  sustained input with round-trip > interval. depth() is now an honest
   *  reflection of the worker's real mailbox depth, capped at 1 by
   *  construction (see simWorkerOffloadController.ts's workerBusy). It
   *  still cannot, on its own, distinguish "genuinely idle" from "one real
   *  computation outstanding, discarded ticks notwithstanding" — that is
   *  what supersedeStreak below is for. */
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
  /**
   * FEAT-2326609771 round follow-up (2026-09-04, "HUD honesty during the
   * handshake window") — report the wall-clock start time (epoch ms) of the
   * VERY FIRST tick request ever posted to a newly-constructed worker, or
   * null once that handshake has settled (a reply landed, it timed out, it
   * errored, or the worker was torn down). Distinct from depth()/
   * supersedeStreak(): those describe STEADY-STATE backlog/contention on an
   * already-proven-alive worker, but a fresh worker's first request is a
   * different situation entirely — the sim clock is FROZEN (no tick has
   * ever landed from this worker instance) while the derived handshake
   * timeout runs, which a plain "1 pending" reading does not distinguish
   * from an ordinary one-tick backlog on a healthy worker. A UI readout
   * should treat a non-null value here as "still proving the worker is
   * alive, clock not yet moving" rather than "N pending", regardless of
   * what depth() reads (depth() is 1 either way, by construction).
   */
  reportHandshakeStartAt(atMs: number | null): void;
  /** The most recently reported handshake start time (epoch ms), or null —
   *  see reportHandshakeStartAt's header for the full semantics. */
  handshakeStartAt(): number | null;
}

export function createQueueDepthTracker(): QueueDepthTracker {
  let count = 0;
  let streak = 0;
  let handshakeStartedAt: number | null = null;
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
      handshakeStartedAt = null;
    },
    reportSupersedeStreak(next) {
      streak = next;
    },
    supersedeStreak() {
      return streak;
    },
    reportHandshakeStartAt(atMs) {
      handshakeStartedAt = atMs;
    },
    handshakeStartAt() {
      return handshakeStartedAt;
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
