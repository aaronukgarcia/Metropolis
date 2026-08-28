// cameraApply.ts — FEAT-1972079897 inc2 RE-APPLY (the deferred MapView wiring).
//
// inc2's cameraStash.ts captures the UI camera (zoom + focus/pan) into a pure,
// reload-surviving channel; the final RE-APPLY on MapView mount was deferred
// because MapView belonged to another lane. This module is the pure seam of that
// re-apply: given MapView's current view and a camera consumed from the stash,
// it produces the view-state to set. MapView calls it once on mount.
//
// UI/envelope state ONLY — a camera never enters SimState or the journal, so
// genesis replay stays deterministic (GR#21). No Date.now / Math.random. Pure
// data + a plain function, so node --test can exercise it without React.

import type { MapViewState } from './uistate.ts';

/** MapView's camera view-state. Structurally identical to MapViewState. */
export interface CameraView {
  zoom: number;
  cx: number;
  cy: number;
}

/**
 * Given the current view and a stashed camera (already consumed read-once from
 * cameraStash), produce the view-state to apply.
 *
 * - A valid camera (three finite numbers) → a new view homed on it (zoom + pan).
 * - A null/undefined stash (no camera was carried) → the current view unchanged,
 *   so a fresh session keeps its default start view (no jump).
 * - A malformed camera (non-finite / wrong type) → also unchanged; the caller's
 *   consumePersistedCamera already validates, this is defence in depth.
 */
export function applyStashedCameraToView(
  view: CameraView,
  camera: MapViewState | null | undefined
): CameraView {
  if (!camera) return view;
  const { zoom, cx, cy } = camera;
  if (
    typeof zoom !== 'number' ||
    typeof cx !== 'number' ||
    typeof cy !== 'number' ||
    !Number.isFinite(zoom) ||
    !Number.isFinite(cx) ||
    !Number.isFinite(cy)
  ) {
    return view;
  }
  return { zoom, cx, cy };
}
