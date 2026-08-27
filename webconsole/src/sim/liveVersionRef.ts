// liveVersionRef.ts — BUG-424: the freshest live version, as a synchronous
// module-level ref.
//
// WHY THIS EXISTS. The badge (useLiveVersion in liveVersion.tsx) polls
// /version.json and moves the displayed build FORWARD as new commits land
// during a long HMR dev session, WITHOUT regenerating src/generated/version.ts
// (the hot-upgrade hook runs gen-version.mjs --live-only). So versionRaw — the
// build-time value baked into version.ts — freezes at the last full build while
// the running code and the badge move ahead. A debug dump that stamped
// versionRaw for appVersion therefore MISREPORTED the running build.
//
// This module mirrors the `lastKnownState` error-trapping pattern: a single
// mutable cell that the UI updates and any synchronous consumer (buildDebugJson)
// can read without React. It imports NOTHING (no version file, no React), so
// pure Node consumers stay resolvable and deterministic — the ref is envelope /
// UI state, never SimState (GR: no non-determinism in the sim path).
//
// Contract:
//   - starts null (no live poll has happened yet)
//   - setLiveVersion(v) is called by useLiveVersion on each SUCCESSFUL poll,
//     with the badge-form label (e.g. "v0.3.0.88")
//   - getLiveVersion() returns the freshest known live version, or null if the
//     app has not polled yet (initial load) — callers fall back to the
//     build-time versionRaw in that case.

/** The freshest live version the badge has seen, or null before any poll. */
let currentLiveVersion: string | null = null;

/** Record the newest live version (badge-form label). Ignores empty values. */
export function setLiveVersion(v: string | null | undefined): void {
  if (typeof v === 'string' && v.length > 0) currentLiveVersion = v;
}

/** The freshest live version, or null if nothing has been polled yet. */
export function getLiveVersion(): string | null {
  return currentLiveVersion;
}

/** Test-only: reset the ref to its fresh-module state. */
export function __resetLiveVersion(): void {
  currentLiveVersion = null;
}
