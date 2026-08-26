// throttle.ts — FEAT-1972079880: debug-snapshot refresh throttle.
//
// Pure and timer-free so it is unit-testable without fake timers: the caller
// owns the clock and simply asks "is a refresh due, and if not, how long?".
// The DebugTab keeps its rendered snapshot frozen until `due` flips true, so
// the JSON text is a stable, selectable frame for a full period.

/** Refresh period for the debug snapshot: once per 15 SECONDS (Aaron's spec). */
export const SNAPSHOT_REFRESH_MS = 15_000;

export interface RefreshDue {
  /** True when a new snapshot frame should be taken now. */
  due: boolean;
  /** Milliseconds until the next refresh is due (0 when due now). */
  remainingMs: number;
}

/**
 * Decide whether a snapshot refresh is due.
 *
 * @param lastMs  epoch-ms of the last taken frame, or null when none exists yet
 * @param nowMs   the current clock reading (epoch-ms)
 * @param periodMs refresh period; defaults to SNAPSHOT_REFRESH_MS
 *
 * Fail-open on bad clocks: a missing/non-finite `lastMs`, a non-finite `nowMs`,
 * a non-positive/non-finite period, or a backwards clock jump (now < last) all
 * report `due` — a stale-but-refreshing panel beats one frozen forever.
 */
export function nextRefreshDue(
  lastMs: number | null,
  nowMs: number,
  periodMs: number = SNAPSHOT_REFRESH_MS
): RefreshDue {
  if (!Number.isFinite(periodMs) || periodMs <= 0) return { due: true, remainingMs: 0 };
  if (lastMs == null || !Number.isFinite(lastMs) || !Number.isFinite(nowMs)) {
    return { due: true, remainingMs: 0 };
  }
  const elapsed = nowMs - lastMs;
  if (elapsed < 0) return { due: true, remainingMs: 0 }; // clock went backwards — self-heal
  if (elapsed >= periodMs) return { due: true, remainingMs: 0 };
  return { due: false, remainingMs: Math.ceil(periodMs - elapsed) };
}
