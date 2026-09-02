// staleBuildGuard.ts — pure comparison logic for FEAT-2326609725's dev-server
// staleness guard.
//
// Real incident (2026-09-02): a long-lived vite dev server kept serving a
// module graph that was 45 commits behind disk. The version BADGE looked
// current because it hot-updates from /version.json (see liveVersion.tsx) —
// but that endpoint is a DIFFERENT signal: it tracks the numeric/describe
// string, which is *meant* to advance live without a page reload (that's the
// whole point of the hot-upgrade path). It is therefore USELESS as a staleness
// signal for "is the JS I'm running the JS on disk" — by design it always
// claims to be current.
//
// The guard here compares something that does NOT move for the life of a
// loaded bundle (APP_VERSION_SHA, frozen at build/dev-start in
// src/generated/version.ts) against something that is asked fresh from git at
// request time (the `sha` field vite.config.ts's /version.json middleware
// computes live, never from a cached file — see gen-version.mjs
// getLiveVersionData()). A mismatch means the running JS predates disk HEAD.
//
// Kept dependency-free and framework-free so it can be exercised directly in
// tests without mounting React (see test/stale-build-guard.test.tsx).

export interface StaleBuildResult {
  /** True only on an exact, comparable sha mismatch (disk moved past the running build). */
  stale: boolean;
  /** The sha frozen into the currently-loaded bundle. */
  runningSha: string;
  /** The sha the server reports as HEAD right now, or null if unknown/unreachable. */
  diskSha: string | null;
}

/**
 * A sha is "uncomparable" when it's missing or the fail-safe 'dev' placeholder
 * (git unavailable at build time, or a production build with no dev server).
 * Uncomparable NEVER produces a stale verdict — a false positive here would
 * train users to ignore the banner, which is worse than never showing it.
 */
function isComparable(sha: string | null | undefined): sha is string {
  return typeof sha === 'string' && sha.length > 0 && sha !== 'dev' && sha !== 'unknown';
}

/**
 * Compare the build-time-frozen sha against a live-polled disk sha.
 *
 * @param runningSha  APP_VERSION_SHA from the loaded bundle (frozen; never
 *                     changes for the life of this page load).
 * @param diskSha     The `sha` field from the latest /version.json poll, or
 *                     null/undefined if the poll failed or hasn't landed yet.
 */
export function checkStaleBuild(
  runningSha: string | null | undefined,
  diskSha: string | null | undefined
): StaleBuildResult {
  const running = runningSha ?? '';
  const disk = diskSha ?? null;

  if (!isComparable(running) || !isComparable(disk)) {
    return { stale: false, runningSha: running, diskSha: disk };
  }

  return { stale: running !== disk, runningSha: running, diskSha: disk };
}
