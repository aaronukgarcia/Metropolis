// liveEngineFlag.ts — FEAT-1972079852 increment 1: the LiveEngineBadge
// feature-flag reader, split out of the .tsx component (mirrors this
// codebase's existing liveVersionRef.ts / liveVersion.tsx split) so it is
// importable by a plain `node --test` .mjs file with no JSX in the module
// graph at all.

/** localStorage key gating the LIVE ENGINE badge. Absent or any value
 * other than '1' means disabled (mock-only, the safe default). */
export const LIVE_ENGINE_FLAG_KEY = 'metropolis.liveEngine';

/** localStorage key for the metroserve WS URL override. */
export const LIVE_ENGINE_URL_KEY = 'metropolis.liveEngineUrl';

/** Default metroserve address (DD1 placeholder, per the acceptance doc's
 * Implementation Notes). */
export const DEFAULT_LIVE_ENGINE_WS_URL = 'ws://localhost:9999/ws';

/** Reads the feature flag from the given storage-like object (defaults to
 * window.localStorage) — a plain function so it's testable without a real
 * browser localStorage, and never throws (private-mode / disabled storage
 * degrades to "disabled" rather than crashing the badge). */
export function isLiveEngineEnabled(storage?: { getItem(key: string): string | null }): boolean {
  const s = storage ?? (typeof localStorage !== 'undefined' ? localStorage : undefined);
  if (!s) return false;
  try {
    return s.getItem(LIVE_ENGINE_FLAG_KEY) === '1';
  } catch {
    return false;
  }
}

/** Resolves the WS URL to connect to, falling back to the default. Never
 * throws. */
export function resolveLiveEngineUrl(storage?: { getItem(key: string): string | null }): string {
  const s = storage ?? (typeof localStorage !== 'undefined' ? localStorage : undefined);
  try {
    return s?.getItem(LIVE_ENGINE_URL_KEY) || DEFAULT_LIVE_ENGINE_WS_URL;
  } catch {
    return DEFAULT_LIVE_ENGINE_WS_URL;
  }
}
