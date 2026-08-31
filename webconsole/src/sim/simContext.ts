import { createContext, useContext, type Context, type Dispatch } from 'react';
import type { SimState } from './types.ts';
import type { Action } from './engine.ts';
import type { NamedSaveMeta } from './namedsaves.ts';
import type { RecentOpened } from './recentfiles.ts';

export interface SimContextValue {
  state: SimState;
  dispatch: Dispatch<Action>;
  cityName: string;
  listSaves: () => NamedSaveMeta[];
  listRecent: () => RecentOpened[];
  saveGame: () => Promise<boolean>;
  saveGameAs: (name?: string) => Promise<void>;
  loadGame: () => Promise<void>;
  loadNamed: (slug: string) => Promise<void>;
  renameCity: (name: string) => boolean;
}

const g = globalThis as unknown as { __metroSimContext?: Context<SimContextValue | null> };
export const SimContext: Context<SimContextValue | null> =
  g.__metroSimContext ?? createContext<SimContextValue | null>(null);
g.__metroSimContext = SimContext;

export function useSim(): SimContextValue {
  const v = useContext(SimContext);
  if (!v) throw new Error('useSim must be used inside SimProvider');
  return v;
}
