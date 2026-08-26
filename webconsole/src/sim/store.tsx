import { createContext, useContext, useEffect, useMemo, useReducer } from 'react';
import type { Dispatch, ReactNode } from 'react';
import type { SimState } from './types';
import { reducer, initialState } from './engine';
import type { Action } from './engine';

// Pure engine logic lives in engine.ts so it is unit-testable without JSX.
// Re-exported here for backward compatibility with existing `'../sim/store'`
// imports across the app.
export {
  levelOf,
  xpForLevel,
  demandOf,
  computeFlows,
  approvalOf,
  wellbeingOf,
  grantLevelRewards,
  reducer,
  initialState,
  LOAN_PRINCIPAL,
  LOAN_TOTAL,
  LEVEL_REWARD_RATE,
} from './engine';
export type { Action, ZoneDemand } from './engine';

const SPEED_MS: Record<SimState['speed'], number> = { 0: 0, 1: 900, 2: 420, 3: 160 };

interface SimContextValue {
  state: SimState;
  dispatch: Dispatch<Action>;
}

const SimContext = createContext<SimContextValue | null>(null);

export function SimProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, undefined, initialState);
  useEffect(() => {
    if (state.speed === 0) return;
    const id = setInterval(() => dispatch({ type: 'tick' }), SPEED_MS[state.speed]);
    return () => clearInterval(id);
  }, [state.speed]);
  const value = useMemo(() => ({ state, dispatch }), [state]);
  return <SimContext.Provider value={value}>{children}</SimContext.Provider>;
}

export function useSim(): SimContextValue {
  const v = useContext(SimContext);
  if (!v) throw new Error('useSim must be used inside SimProvider');
  return v;
}
