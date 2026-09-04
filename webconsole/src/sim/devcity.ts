import type { SimState } from './types.ts';
import snapshot from './fixtures/Dev-city1.json' with { type: 'json' };
import { mintLineageId } from './replay.ts';

export const DEVCITY1_NAME = 'Dev-city1';

export function loadDevCity1(): SimState {
  const state = structuredClone(snapshot as unknown as SimState);
  // FEAT-1972079878 inc1: ensure buildingMonitors field exists for backward compatibility
  if (!state.buildingMonitors) {
    state.buildingMonitors = [];
  }
  // FEAT-1972079925: ensure demographic-flow fields exist for backward compatibility
  // (the dev-city fixture predates this feature).
  if (!state.demographicAccum) {
    state.demographicAccum = { births: 0, deaths: 0, moveIns: 0, moveOuts: 0 };
  }
  if (!state.demographicHistory) {
    state.demographicHistory = [];
  }
  if (!state.lastDemographics) {
    state.lastDemographics = { births: 0, deaths: 0, moveIns: 0, moveOuts: 0 };
  }
  // FEAT-1972079926: ensure arrivals-by-mode fields exist for backward
  // compatibility (the dev-city fixture predates this feature).
  if (!state.arrivalsByModeAccum) {
    state.arrivalsByModeAccum = { road: 0, railLow: 0, railHs: 0, sea: 0, plane: 0 };
  }
  if (!state.arrivalsByModeHistory) {
    state.arrivalsByModeHistory = [];
  }
  if (!state.lastArrivalsByMode) {
    state.lastArrivalsByMode = { road: 0, railLow: 0, railHs: 0, sea: 0, plane: 0 };
  }
  // P0 RCA fix, item 1: this is a genesis point (boot's own "no save found"
  // fallback calls it directly, never through the pure reducer), so minting
  // a fresh lineage id here is safe and required.
  state.lineageId = mintLineageId();
  return state;
}
