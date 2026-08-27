// liveVersion.tsx — a version signal that updates HOT while the game runs.
//
// Problem (Aaron, 2026-08-27): a commit regenerated src/generated/version.ts,
// which is INSIDE Vite's module graph, so Vite did a full page reload and the
// running sim reset to the start point mid-game. Unacceptable during play.
//
// Fix: decouple the live version from the module graph entirely. The post-commit
// hook regenerates `webconsole/version.live.json` (a plain root file that nothing
// imports and Vite does NOT watch for HMR — see vite.config.ts), and the app
// POLLS `/version.json` (served by a dev-server middleware that reads that file
// fresh per request). A new commit therefore changes NO module the running app
// depends on: no reload, no reset — the game keeps ticking, and the badge simply
// ticks up with a quiet "upgraded" toast. If a future change genuinely breaks
// render, the ErrorBoundary catches it (that's the acceptable failure mode).
//
// The build-time static import (./version) is the initial value; the poll only
// ever moves it FORWARD to whatever the latest commit wrote.

import { useEffect, useRef, useState } from 'react';
import { versionNumeric, versionRaw } from './version';
import { setLiveVersion } from './liveVersionRef';

/** How often to check for a newer build. Cheap: a tiny JSON, cache-busted. */
const POLL_MS = 3000;
/** How long the "upgraded" toast lingers before auto-dismissing. */
const TOAST_MS = 8000;

/** Mirror of versionBadgeLabel()'s formatting, applied to a live numeric. */
function labelFor(numeric: string, raw: string): string {
  if (numeric && numeric !== '0.0.0.0') return `v${numeric}`;
  if (!raw || raw === 'dev' || raw === 'unknown') return 'dev';
  return raw;
}

export interface LiveVersion {
  /** Badge form, e.g. "v0.3.0.66". */
  label: string;
  /** Full git-describe string for the tooltip / About. */
  raw: string;
  /** The 1.2.3.4 numeric. */
  numeric: string;
  /** Set to the new label for a few seconds after an upgrade is detected; else null. */
  upgradedTo: string | null;
}

/**
 * Track the live version, moving forward as new commits land. Never resets the
 * page or the sim — it only reads a static JSON and updates local state.
 */
export function useLiveVersion(): LiveVersion {
  const [numeric, setNumeric] = useState<string>(versionNumeric);
  const [raw, setRaw] = useState<string>(versionRaw);
  const [upgradedTo, setUpgradedTo] = useState<string | null>(null);
  // Highest numeric we've already surfaced, so we toast once per upgrade.
  const seen = useRef<string>(versionNumeric);
  const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    let alive = true;
    const check = async () => {
      try {
        const res = await fetch(`/version.json?ts=${Date.now()}`, { cache: 'no-store' });
        if (!alive || !res.ok) return;
        const data = await res.json();
        const n = typeof data?.numericVersion === 'string' ? data.numericVersion : null;
        if (!n || n === seen.current) return;
        // A newer build exists while we're running. Surface it hot.
        seen.current = n;
        const r = typeof data?.version === 'string' ? data.version : versionRaw;
        const label = labelFor(n, r);
        // BUG-424: publish the freshest live version to the synchronous ref so
        // buildDebugJson stamps meta.appVersion to match the badge, not the
        // frozen build-time versionRaw.
        setLiveVersion(label);
        setNumeric(n);
        setRaw(r);
        setUpgradedTo(label);
        if (toastTimer.current) clearTimeout(toastTimer.current);
        toastTimer.current = setTimeout(() => {
          if (alive) setUpgradedTo(null);
        }, TOAST_MS);
      } catch {
        // No /version.json (production build, or middleware absent) — ignore.
      }
    };
    const id = setInterval(check, POLL_MS);
    check();
    return () => {
      alive = false;
      clearInterval(id);
      if (toastTimer.current) clearTimeout(toastTimer.current);
    };
  }, []);

  return { label: labelFor(numeric, raw), raw, numeric, upgradedTo };
}

/**
 * A quiet, dismissible toast that appears for a few seconds when the running
 * build is upgraded underneath the player. Purely informational — the game
 * keeps running; this just tells them the code is now newer.
 */
export function VersionUpgradeToast() {
  const { upgradedTo } = useLiveVersion();
  if (!upgradedTo) return null;
  return (
    <div
      role="status"
      style={{
        position: 'fixed',
        bottom: '12px',
        left: '50%',
        transform: 'translateX(-50%)',
        background: 'var(--panel, #1b1f27)',
        color: 'var(--text, #e6e6e6)',
        border: '1px solid var(--accent, #4c8bf5)',
        borderRadius: '8px',
        padding: '8px 14px',
        fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
        fontSize: '12px',
        boxShadow: '0 4px 16px rgba(0,0,0,0.4)',
        zIndex: 9999,
        pointerEvents: 'none',
      }}
    >
      ⬆ Upgraded to {upgradedTo} — running hot, your city kept playing
    </div>
  );
}
