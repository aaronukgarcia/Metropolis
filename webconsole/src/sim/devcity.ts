import type { SimState } from './types.ts';
import snapshot from './fixtures/Dev-city1.json' with { type: 'json' };

export const DEVCITY1_NAME = 'Dev-city1';

export function loadDevCity1(): SimState {
  const state = structuredClone(snapshot as unknown as SimState);
  // FEAT-1972079878 inc1: ensure buildingMonitors field exists for backward compatibility
  if (!state.buildingMonitors) {
    state.buildingMonitors = [];
  }
  return state;
}
