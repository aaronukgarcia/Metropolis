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
// loaded. That can only be fixed by an actual reload of a rebuilt bundle, so
// the banner tells the user to restart the dev server and hard-reload.
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
 * .test.tsx) without needing to mock fetch/useEffect timing.
 */
export function StaleBuildBannerView({ result }: { result: StaleBuildResult }) {
  if (!result.stale) return null;
  return (
    <div className="stale-build-banner" role="alert">
      ⚠ STALE BUILD — the running code is behind disk (running {result.runningSha}, disk{' '}
      {result.diskSha}). Restart the dev server (Ctrl+C then npm run dev) and hard-reload
      (Ctrl+Shift+R).
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
