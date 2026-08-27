// refs.ts — FEAT-1972079903: per-building reference-id label.
//
// The player reports bugs by building number ("re building 44 there's a bug").
// Each building already carries a collision-free `id` (BUG-413) that matches
// `buildings[].id` in the debug JSON, so that id IS the ref. This module is the
// single, pure, deterministic source of the ref text so the map overlay, the
// selected-building panel, and the unit test all agree on the exact string.
//
// UI-only: the "Refs" toggle that gates this is MapView component state and is
// deliberately NOT in SimState/the journal — it never affects the simulation,
// so genesis-replay/determinism are untouched.
//
// Pure data, no React, no Date/Math.random — same building always yields the
// same text, so node --test can exercise it directly.

import type { Building } from './types';

/** The bare ref token for a building, e.g. `#44`. Always the building's id. */
export function buildingRef(b: Pick<Building, 'id'>): string {
  return `#${b.id}`;
}

/**
 * The ref label to show for a building given the Refs toggle. When the toggle
 * is OFF this is the empty string (nothing rendered); when ON it is the bare
 * ref token, e.g. `#44`. Deterministic: identical input → identical output.
 */
export function buildingRefLabel(b: Pick<Building, 'id'>, showRefs: boolean): string {
  return showRefs ? buildingRef(b) : '';
}
