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

// ---------------------------------------------------------------------------
// BUG-564 rework (2026-09-04): the banner above was unmounted (6f69e28,
// 2026-09-02) because it misfired in active dev — `runningSha`
// (APP_VERSION_SHA) is frozen at dev-server START while `diskSha` (live git
// HEAD) advances on every commit, so after the FIRST commit of any dev
// session the comparison shows a PERMANENT mismatch that only a dev-server
// RESTART can clear (Reload does nothing — the module the sha comes from
// isn't re-evaluated by a reload) — even though Vite's HMR has been patching
// the module graph in place the whole time and the code actually running is
// perfectly current. A warning nobody can act on is worse than none.
//
// The real signature of the incident this guard exists for (a long-lived dev
// server whose module graph has gone stale) is NOT "sha differs" — sha
// differs on every commit by design in dev — it is "HMR has stopped
// delivering updates", i.e. the dev-server websocket connection is down (see
// hmrLiveness.ts). So:
//   - production build (no HMR at all, isDevServer=false): sha comparison is
//     exactly right and UNCHANGED — this falls straight through to
//     checkStaleBuild above, still pinned by this file's existing tests.
//   - dev server, HMR connected: never stale, no matter what the shas say —
//     HMR is doing its job, so the build-time-frozen sha is not a valid
//     staleness proxy while it's connected.
//   - dev server, HMR genuinely disconnected: this IS the dead-server case —
//     fall back to the sha comparison, which is trustworthy here because no
//     further live patches are coming and only a restart + reload fixes it.

export interface DevAwareStaleInputs {
  runningSha: string | null | undefined;
  diskSha: string | null | undefined;
  /** True under the Vite dev server (import.meta.env.DEV); false for a production build. */
  isDevServer: boolean;
  /**
   * True while the HMR websocket is connected. Meaningless (and ignored) when
   * `isDevServer` is false — a production build has no HMR to lose.
   */
  hmrConnected: boolean;
}

/**
 * Dev-aware wrapper around {@link checkStaleBuild} — use this from the live
 * component. Use `checkStaleBuild` directly only where sha comparison alone
 * is the intended, unconditional check (this file's prod-pinning tests).
 */
export function resolveStaleBuild(inputs: DevAwareStaleInputs): StaleBuildResult {
  const { runningSha, diskSha, isDevServer, hmrConnected } = inputs;
  if (isDevServer && hmrConnected) {
    return { stale: false, runningSha: runningSha ?? '', diskSha: diskSha ?? null };
  }
  return checkStaleBuild(runningSha, diskSha);
}
