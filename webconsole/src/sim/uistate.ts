// uistate.ts — FEAT-1972079886: published map/UI state for the debug capture.
//
// The map view (zoom/center), the selected building and the water-layer toggle
// live as MapView component-local state — the DebugTab (a sibling in the React
// tree) cannot reach them through props without threading state through App.
// Instead MapView PUBLISHES its current view here on every change and the
// debug capture reads it when a frame is taken. This is deliberately a dumb
// last-write-wins mailbox, not a store: nothing subscribes, nothing re-renders
// off it — it exists solely so debug.json can report what the player is
// looking at. Pure data, no React, so node --test can exercise it.

export interface MapViewState {
  zoom: number;
  cx: number;
  cy: number;
}

export interface MapUiState {
  /** Current camera, or null before the map has mounted / published. */
  view: MapViewState | null;
  /** Building id selected on the map, or null when nothing is selected. */
  selectedBuildingId: number | null;
  /** Water-network overlay toggle. */
  showWater: boolean;
}

export const EMPTY_MAP_UI: MapUiState = {
  view: null,
  selectedBuildingId: null,
  showWater: false,
};

let current: MapUiState = EMPTY_MAP_UI;

/** MapView calls this whenever view / selection / layer toggles change. */
export function publishMapUi(u: MapUiState): void {
  current = u;
}

/** Snapshot of the last published map UI state (EMPTY_MAP_UI before mount). */
export function currentMapUi(): MapUiState {
  return current;
}
