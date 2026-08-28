// cameraStash.ts — FEAT-1972079897 inc2 (camera restore, Aaron 2026-08-27).
//
// A rebuild reloads the page onto the new engine (brief §4.4). The camera the
// player was looking at lives in MapView component-local state and is lost across
// that reload. This module is a tiny, PURE, last-write-wins channel that carries
// the captured camera THROUGH the reload so the map can re-home the view instead
// of snapping back to the default start position.
//
// It is UI/envelope state ONLY — a camera never enters SimState or the journal,
// so genesis replay stays deterministic (GR#21). No Date.now / Math.random.
//
// Two layers:
//   - a synchronous module ref (fast, in-memory) for same-tick round-trips, and
//   - a localStorage persist/consume pair so the value survives the rebuild's
//     page reload. `consume*` is read-once: it clears after returning, so a stale
//     camera can never silently override a later manual pan.
//
// RE-APPLY IS DEFERRED: the final one-line consumption on MapView mount lives in
// MapView.tsx, which a parallel lane owns this increment. The capture + carry +
// consume round-trip is complete and tested here; wiring MapView to call
// consumeStashedCamera() on mount is a follow-up for the owning lane.

import type { MapViewState } from './uistate.ts';

/** localStorage key for the camera carried across a rebuild reload. */
export const CAMERA_STASH_KEY = 'metropolis.pendingCamera';

/** Minimal storage surface (injectable for tests), mirroring replay.StorageLike. */
export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

let currentStash: MapViewState | null = null;

/** Validate a parsed value is a real camera (3 finite numbers), else null. */
function coerceCamera(v: unknown): MapViewState | null {
  if (!v || typeof v !== 'object') return null;
  const o = v as Record<string, unknown>;
  const { zoom, cx, cy } = o;
  if (
    typeof zoom === 'number' &&
    typeof cx === 'number' &&
    typeof cy === 'number' &&
    Number.isFinite(zoom) &&
    Number.isFinite(cx) &&
    Number.isFinite(cy)
  ) {
    return { zoom, cx, cy };
  }
  return null;
}

/** Record a camera in the synchronous module ref. Ignores a null/invalid camera. */
export function stashCamera(camera: MapViewState | null | undefined): void {
  const c = coerceCamera(camera);
  if (c) currentStash = c;
}

/** Read-and-clear the synchronous stash (read-once). */
export function consumeStashedCamera(): MapViewState | null {
  const c = currentStash;
  currentStash = null;
  return c;
}

/**
 * Persist a camera so it survives a page reload (the rebuild path). Fail-safe:
 * any storage error (quota, private mode) is swallowed — a lost camera restore is
 * cosmetic and must never block the rebuild.
 */
export function persistStashedCamera(
  storage: StorageLike | null | undefined,
  camera: MapViewState | null | undefined
): boolean {
  const c = coerceCamera(camera);
  if (!storage || !c) return false;
  try {
    storage.setItem(CAMERA_STASH_KEY, JSON.stringify(c));
    return true;
  } catch {
    return false;
  }
}

/**
 * Read-and-clear the persisted camera (read-once across a reload). Fail-safe:
 * missing/corrupt JSON or storage errors degrade to null.
 */
export function consumePersistedCamera(
  storage: StorageLike | null | undefined
): MapViewState | null {
  if (!storage) return null;
  try {
    const raw = storage.getItem(CAMERA_STASH_KEY);
    if (!raw) return null;
    const cam = coerceCamera(JSON.parse(raw));
    try {
      storage.removeItem(CAMERA_STASH_KEY);
    } catch {
      /* best-effort clear */
    }
    return cam;
  } catch {
    return null;
  }
}

/** Test-only: reset the synchronous ref to its fresh-module state. */
export function __resetCameraStash(): void {
  currentStash = null;
}
