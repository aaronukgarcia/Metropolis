// StaleBuildBanner.tsx — FEAT-2326609725: loud warning when the running JS
// bundle is behind the code actually on disk.
//
// Real incident (2026-09-02): a long-lived vite dev server kept serving an OLD
// module graph 45 commits stale. The version BADGE hot-updated (see
// TopBar.tsx / liveVersion.tsx) so it LOOKED current, and /version.json also
// reported the stale value at the time — so neither existing signal caught
// the drift. A user hit a bug that was already fixed on disk and lost time
// chasing a ghost.
//
// This component polls the SAME /version.json endpoint, but compares a
// DIFFERENT field than the hot-upgrade path does: the exact-match commit
// `sha`, against APP_VERSION_SHA — a constant frozen into the bundle at
// build/dev-start that (unlike the numeric version) never advances on its
// own. A mismatch means: the server (now computing `sha` LIVE from git HEAD
// on every request — see vite.config.ts) has moved past the JS this page
// loaded.
//
// FEAT-2326609720 inc1 UX fix (BUG, "staleness banner UX", 2026-09-02 — Aaron
// via the lead): the ORIGINAL copy told every reader to "restart the dev
// server (Ctrl+C then npm run dev)" — wrong for the common case, where the
// dev server is live and current and only the loaded PAGE is stale, and dev
// jargon a PLAYER (not running a terminal) cannot act on at all. The fix is a
// COPY/AFFORDANCE change only — checkStaleBuild's fail-silent detection logic
// is untouched (staleBuildGuard.ts, still covered by
// test/stale-build-guard.test.tsx's comparison cases):
//   1. Player-safe headline + a one-click Reload button (location.reload()).
//      The "restart the dev server" instruction survives only as small
//      secondary text / a title tooltip for the rare dead-server case.
//   2. Dismissable (×) — hides the banner for the rest of this page session
//      (component-local state; a genuinely NEW drift after a reload starts
//      fresh, since a reload remounts the whole app) so it does not nag
//      during active development where commits land every few minutes.
//   3. z-index stays on the overlayLayers.ts SSOT scale (Z_INDEX.
//      STALE_BUILD_BANNER) — always-on-top, non-blocking: pointer-events are
//      enabled only on the banner's own two buttons (Reload / ×), never on
//      the wrapper, so it still never blocks a click on the map or controls
//      underneath (I-3).
//
// Fail-silent contract: unreachable endpoint (production build, no dev
// server) or an incomparable 'dev'/'unknown' sha on either side NEVER shows
// the banner (see staleBuildGuard.ts's isComparable) — a false positive here
// would train users to ignore it, which is worse than missing a real one.

import { useEffect, useState } from 'react';
import { APP_VERSION_SHA } from '../generated/version';
import { checkStaleBuild, type StaleBuildResult } from '../sim/staleBuildGuard';

/** How often to poll for drift. Cheap: a tiny JSON, cache-busted server-side. */
const POLL_MS = 25_000;

/**
 * Pure presentational half — exported separately from the polling container
 * so tests can exercise the comparison-to-render logic (test/stale-build-guard
 * .test.tsx) without needing to mock fetch/useEffect timing. Owns its own
 * "dismissed for this session" state (component-local, never persisted) —
 * StaleBuildBanner never unmounts this view between polls, so a dismiss
 * sticks until an actual page reload remounts the tree.
 */
export function StaleBuildBannerView({
  result,
  onReload,
}: {
  result: StaleBuildResult;
  /**
   * Test seam: defaults to a real `window.location.reload()`. jsdom's
   * `location.reload` is a non-configurable, non-writable OWN property (no
   * prototype indirection to stub), so test/stale-build-guard.test.tsx
   * verifies the button-click wiring by injecting a spy here instead of
   * trying to monkeypatch jsdom's Location internals.
   */
  onReload?: () => void;
}) {
  const [dismissed, setDismissed] = useState(false);
  if (!result.stale || dismissed) return null;
  const reload = onReload ?? (() => window.location.reload());
  return (
    <div className="stale-build-banner" role="alert">
      <span className="stale-build-banner-text">
        ⚠ STALE BUILD — 🔄 A newer build is available (running {result.runningSha}, disk{' '}
        {result.diskSha}).
      </span>
      <button
        type="button"
        className="stale-build-banner-reload"
        onClick={reload}
        title="Reload this page to pick up the newer build. If reloading does not help, the dev server itself may need a restart (Ctrl+C then npm run dev)."
      >
        Reload
      </button>
      <button
        type="button"
        className="stale-build-banner-dismiss"
        aria-label="Dismiss for this session"
        title="Dismiss — hides this banner until the next page reload"
        onClick={() => setDismissed(true)}
      >
        ×
      </button>
    </div>
  );
}

export function StaleBuildBanner() {
  const [diskSha, setDiskSha] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const check = async () => {
      try {
        const res = await fetch('/version.json', { cache: 'no-store' });
        if (!alive || !res.ok) return;
        const data = await res.json();
        const sha = typeof data?.sha === 'string' ? data.sha : null;
        if (alive) setDiskSha(sha);
      } catch {
        // Unreachable (production build / no dev server) — fail silent, never
        // flip to a stale sha we can't trust, never show a false warning.
      }
    };
    check();
    const id = setInterval(check, POLL_MS);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  const result = checkStaleBuild(APP_VERSION_SHA, diskSha);
  return <StaleBuildBannerView result={result} />;
}
