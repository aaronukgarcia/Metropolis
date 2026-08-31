// debugBuildSpeed.ts — FEAT-159: DEBUG-ONLY fast-build override.
//
// A developer watching the city evolve does not want to wait real construction
// lead-times (dwellings, mega-facilities). This module provides a debug-gated,
// per-CLASS lead-time scale that multiplies each building type's effective
// construction time DOWN (dwellings ~1/10th, mega/utility "weeks not years").
//
// Design mirrors liveEngineFlag.ts exactly: a localStorage-keyed flag read by a
// pure, never-throws function, importable by a plain `node --test` .mjs file
// with no JSX/React in the module graph. The scale is the SMALLEST correct seam
// (a flag + a pure factor helper); constructionTicks() in data.ts is the single
// place lead-time is derived, so applying the scale there flows to the build
// gate (isOnline G1), the "under construction — N ticks remaining" display, and
// the catalogue's "N ticks to build" hint with no other call-site edits.
//
// ⚠ DETERMINISM: when the flag is OFF (the default), scaleConstructionTicks
// returns its input BYTE-FOR-BYTE unchanged — normal play and replay are never
// affected. The override only reads/uses the factor when explicitly enabled,
// which is a developer-machine localStorage state, never a shipped default.
//
// ⚠ BALANCE-NUMBER REGIME (Aaron's blanket rule): every factor below is a
// PLACEHOLDER — directional only, pending Aaron's row-by-row tuning. Do not
// balance gameplay against these numbers. They exist so the dev can WATCH.

import type { ZoneKind } from './types.ts';

/** localStorage key gating the DEBUG fast-build override. Absent or any value
 * other than '1' means disabled (real lead-times, the safe default). */
export const FAST_BUILD_FLAG_KEY = 'metropolis.debugFastBuild';

/**
 * Per-CLASS (ZoneKind) lead-time scale factors. Effective construction ticks =
 * round(base × factor), floored at 1 tick (never zero/negative). Only consulted
 * when the debug flag is ON.
 *
 * PLACEHOLDER-balance — directional only:
 *   • dwellings (residential) ~1/10th so a neighbourhood fills while you watch;
 *   • mega / utility (power, water) ~1/20th — "weeks not years";
 *   • civic mega-facilities (health/police/school/fire/civic/leisure/landmark)
 *     ~1/20th for the same reason;
 *   • jobs land (commercial/office/industrial/mine) ~1/10th.
 * Network infrastructure (road/motorway/rail/station/pylon) already builds in a
 * few ticks; it falls through to DEFAULT_FAST_BUILD_FACTOR.
 */
export const FAST_BUILD_CLASS_FACTORS: Partial<Record<ZoneKind, number>> = {
  // dwellings — much faster
  residential: 0.1, // PLACEHOLDER
  // mega / utility — weeks not years
  power: 0.05, // PLACEHOLDER
  water: 0.05, // PLACEHOLDER
  // civic mega-facilities — weeks not years
  health: 0.05, // PLACEHOLDER
  police: 0.05, // PLACEHOLDER
  school: 0.05, // PLACEHOLDER
  fire: 0.05, // PLACEHOLDER
  civic: 0.05, // PLACEHOLDER
  leisure: 0.05, // PLACEHOLDER
  landmark: 0.05, // PLACEHOLDER
  // jobs land — much faster
  commercial: 0.1, // PLACEHOLDER
  office: 0.1, // PLACEHOLDER
  industrial: 0.1, // PLACEHOLDER
  mine: 0.1, // PLACEHOLDER
};

/** Fallback factor for any ZoneKind not named above (e.g. network tiles, parks,
 * placeholder catalogue families). PLACEHOLDER-balance. */
export const DEFAULT_FAST_BUILD_FACTOR = 0.1;

/** Reads the debug flag from the given storage-like object (defaults to
 * window.localStorage) — a plain function so it's testable without a real
 * browser localStorage, and never throws (private-mode / disabled storage
 * degrades to "disabled" rather than crashing the sim). */
export function isFastBuildEnabled(storage?: {
  getItem(key: string): string | null;
}): boolean {
  const s =
    storage ?? (typeof localStorage !== 'undefined' ? localStorage : undefined);
  if (!s) return false;
  try {
    return s.getItem(FAST_BUILD_FLAG_KEY) === '1';
  } catch {
    return false;
  }
}

/**
 * Scale a base construction lead-time by the debug per-class factor.
 *
 * When the override is OFF, returns `baseTicks` unchanged (byte-for-byte) — this
 * is the determinism guarantee. When ON, returns
 * `max(1, round(baseTicks × factor))` so the result is never zero or negative
 * (floor-at-1-tick).
 *
 * `enabled` may be forced (tests, or a caller that already read the flag);
 * otherwise it is read from `storage` / window.localStorage. Pure given its
 * inputs — no Date/Math.random.
 */
export function scaleConstructionTicks(
  baseTicks: number,
  kind: ZoneKind,
  opts?: {
    enabled?: boolean;
    storage?: { getItem(key: string): string | null };
  },
): number {
  const enabled = opts?.enabled ?? isFastBuildEnabled(opts?.storage);
  if (!enabled) return baseTicks;
  const factor = FAST_BUILD_CLASS_FACTORS[kind] ?? DEFAULT_FAST_BUILD_FACTOR;
  return Math.max(1, Math.round(baseTicks * factor));
}
