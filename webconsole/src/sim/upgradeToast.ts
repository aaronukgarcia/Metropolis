// upgradeToast.ts — BAR-1 (BUG-435 defect 3, round r1 REJECT follow-up).
//
// Extracted from liveVersion.tsx as a plain .ts (no JSX) module so the
// message-selection logic is testable under plain `node --test` without a
// JSX/tsx transform. liveVersion.tsx re-exports these for its consumers.

/**
 * Which path produced an "upgraded" toast, so the copy can tell the truth.
 * 'hot' — the sim genuinely kept ticking through the swap.
 * 'drain' — the version arrived DURING a rebuild, was queued, and only
 * applied once the rebuild finished (ticks were suppressed meanwhile).
 */
export type UpgradeSource = 'hot' | 'drain';

/**
 * Pick the truthful toast copy for the given upgrade source. The 'hot' path
 * may honestly claim the city kept playing; the 'drain' path (queued during a
 * rebuild, applied after) must not — ticks were suppressed for the duration
 * of the rebuild. Pure so it is unit-testable without mounting the poller.
 */
export function toastMessageFor(label: string, source: UpgradeSource): string {
  if (source === 'drain') {
    return `⬆ Upgraded to ${label} — applied after your city was rebuilt`;
  }
  return `⬆ Upgraded to ${label} — running hot, your city kept playing`;
}
