import { createContext, useContext, useEffect, useMemo, useReducer } from 'react';
import type { Dispatch, ReactNode } from 'react';
import type { SimState } from './types';
import { reducer, initialState, SPEED_MS } from './engine';
import type { Action } from './engine';
import { getGlobalTickTracker, recordTickDuration } from './perfhud';
import type { TickTrackerState } from './perfhud';

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
  SPEED_MS,
  HISTORY_CAP,
  LEDGER_CAP,
} from './engine';
export type { Action, ZoneDemand } from './engine';

interface SimContextValue {
  state: SimState;
  dispatch: Dispatch<Action>;
}

const SimContext = createContext<SimContextValue | null>(null);

export function SimProvider({ children }: { children: ReactNode }) {
  const [state, dispatch] = useReducer(reducer, undefined, initialState);

  // Wrap dispatch to measure tick duration (FEAT-1972079856: perf HUD).
  // Only in DEV mode and only for 'tick' actions.
  const tickTracker: TickTrackerState | null = import.meta.env.DEV ? getGlobalTickTracker() : null;
  const wrappedDispatch = useMemo(() => {
    if (!tickTracker) return dispatch;
    return (action: Action) => {
      if (action.type === 'tick') {
        const start = performance.now();
        dispatch(action);
        const duration = performance.now() - start;
        recordTickDuration(tickTracker, duration);
      } else {
        dispatch(action);
      }
    };
  }, [tickTracker]);

  useEffect(() => {
    if (state.speed === 0) return;
    const id = setInterval(() => wrappedDispatch({ type: 'tick' }), SPEED_MS[state.speed]);
    return () => clearInterval(id);
  }, [state.speed, wrappedDispatch]);
  const value = useMemo(() => ({ state, dispatch: wrappedDispatch }), [state, wrappedDispatch]);
  return <SimContext.Provider value={value}>{children}</SimContext.Provider>;
}

export function useSim(): SimContextValue {
  const v = useContext(SimContext);
  if (!v) throw new Error('useSim must be used inside SimProvider');
  return v;
}
