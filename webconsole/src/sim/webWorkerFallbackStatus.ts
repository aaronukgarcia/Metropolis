// webWorkerFallbackStatus.ts — FEAT-2326609771 (2026-09-04, default-ON
// rollout): a tiny, framework-free tracker recording WHY the worker offload
// fell back to the synchronous main-thread path this session, if it did.
//
// WHY THIS EXISTS: webWorkerFlag.ts's webWorkerOffloadEnabled() answers "is
// the kill switch on" — a pure read of localStorage + the Worker capability.
// It has no way to know that the worker was ALLOWED to run but then actually
// failed at runtime (construction threw, the handshake errored, or the first
// tick never replied within the derived timeout) — the flag stays "enabled"
// the whole session even after store.tsx has torn the worker down and gone
// back to calling the reducer directly. Without a SEPARATE signal, a UI
// readout (QueueDepthHud.tsx) that only asks the flag would keep claiming
// "worker: 0 pending" (a lie — there is no worker any more) instead of
// honestly reporting the fallback, exactly the class of dishonest-readout
// defect BUG-605/N1 already fixed once for the supersede-streak case.
//
// Mirrors queueDepth.ts/workerQueueDepth.ts's pure-tracker + module-level-
// singleton convention: no React, no Worker, no timers — store.tsx (the
// worker owner) reports a reason; QueueDepthHud.tsx polls it at the same 1Hz
// cadence it already uses for the worker backlog reader.
export type WorkerFallbackReason =
  /** `new Worker(...)` itself threw (AC-8's original construction-failure case). */
  | 'construct-failed'
  /** The worker's `onerror` fired before its first ever reply landed — the
   *  handshake itself failed, not a later steady-state crash. */
  | 'handshake-error'
  /** The worker's first tick reply did not arrive within
   *  simWorkerOffloadController.ts's deriveHandshakeTimeoutMs() window. */
  | 'handshake-timeout'
  /** The worker's `onerror` fired AFTER at least one successful reply — a
   *  steady-state runtime crash, not a handshake failure. */
  | 'runtime-error';

export interface WorkerFallbackTracker {
  /** Record that the worker fell back for `reason` — sticky for the rest of
   *  the session (or until reset(), e.g. a fresh SimProvider mount) since a
   *  torn-down worker is never reconstructed mid-session (AC-8's existing
   *  design: one construction attempt per SimProvider lifetime). */
  report(reason: WorkerFallbackReason): void;
  /** The most recently reported reason, or null if the worker never fell
   *  back this session (either it is running fine, or the kill switch was
   *  explicitly off from the start — that case never calls report() at
   *  all, since nothing was ever attempted). */
  reason(): WorkerFallbackReason | null;
  /** Clear back to "no fallback reported" — used by tests and by a fresh
   *  SimProvider mount, mirroring queueDepth.ts's resetAll()/workerQueueDepth
   *  .ts's reset() convention. */
  reset(): void;
}

export function createWorkerFallbackTracker(): WorkerFallbackTracker {
  let current: WorkerFallbackReason | null = null;
  return {
    report(reason) {
      current = reason;
    },
    reason() {
      return current;
    },
    reset() {
      current = null;
    },
  };
}

// ---------- global singleton (mirrors workerQueueDepth.ts's convention) ----
let globalWorkerFallbackTracker: WorkerFallbackTracker | null = null;

/** Lazily-created global fallback-reason tracker, shared across store.tsx
 *  (writer) and QueueDepthHud.tsx (reader). One running game per page, one
 *  worker, one fallback status — same justification as the sibling trackers
 *  in queueDepth.ts/workerQueueDepth.ts. */
export function getGlobalWorkerFallbackTracker(): WorkerFallbackTracker {
  if (!globalWorkerFallbackTracker) {
    globalWorkerFallbackTracker = createWorkerFallbackTracker();
  }
  return globalWorkerFallbackTracker;
}

/** Human-readable label for a fallback reason, for HUD/error-message use —
 *  kept in one place so the wording never drifts between call sites. */
export function describeFallbackReason(reason: WorkerFallbackReason): string {
  switch (reason) {
    case 'construct-failed':
      return 'construction failed';
    case 'handshake-error':
      return 'handshake error';
    case 'handshake-timeout':
      return 'handshake timed out';
    case 'runtime-error':
      return 'runtime error';
  }
}
