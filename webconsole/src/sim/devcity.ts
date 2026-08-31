import type { SimState } from './types.ts';
import snapshot from './fixtures/Dev-city1.json' with { type: 'json' };

export const DEVCITY1_NAME = 'Dev-city1';

export function loadDevCity1(): SimState {
  return structuredClone(snapshot as SimState);
}
