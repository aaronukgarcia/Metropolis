import { createContext, useContext, useEffect, useMemo, useReducer, useState } from 'react';
import type { Dispatch, ReactNode } from 'react';
import type { SimState } from './types';
import { reducer, initialState, SPEED_MS } from './engine';
import type { Action } from './engine';
import { getGlobalTickTracker, recordTickDuration } from './perfhud';
import type { TickTrackerState } from './perfhud';
import { recordAction, emptyJournal, persistJournal, loadJournal, journalTail, type Journal } from './journal';
import { AUTOSAVE_INTERVAL_MS, persistSavepoint, createSavepoint, restoreFromSavepoint } from './replay';

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
  // Initialize state from boot-time restore or fresh start.
  // FEAT-1972079854: Boot-time recovery from persisted savepoint + journal replay.
  const [initializedState, setInitializedState] = useState<SimState | null>(null);
  const [initializedJournal, setInitializedJournal] = useState<Journal | null>(null);
  const [lastSaveIndex, setLastSaveIndex] = useState<number>(0);

  // On mount, attempt restore from localStorage; fall back to fresh state.
  useEffect(() => {
    // Try restore first (FEAT-1972079854: journal recovery on boot).
    const restoreResult = restoreFromSavepoint(window.localStorage);
    if (restoreResult.success && restoreResult.state) {
      // Restore succeeded: use recovered state and load journal.
      setInitializedState(restoreResult.state);
      const loadedJournal = loadJournal(window.localStorage);
      setInitializedJournal(loadedJournal);
      setLastSaveIndex(loadedJournal.entries.length); // Mark this as the checkpoint.
    } else {
      // Restore failed or no savepoint: boot fresh.
      setInitializedState(initialState());
      setInitializedJournal(emptyJournal());
      setLastSaveIndex(0);
    }
  }, []); // Mount once only

  // Wait for initialization to complete before rendering sim.
  if (!initializedState || !initializedJournal) {
    return <SimContext.Provider value={null}>{children}</SimContext.Provider>;
  }

  // Now that state is initialized, set up the reducer + dispatch loop.
  const [state, dispatch] = useReducer(reducer, initializedState);
  const [journal, setJournal] = useState<Journal>(initializedJournal);
  const [autoSaveError, setAutoSaveError] = useState<boolean>(false);

  // Wrap dispatch to:
  // 1. Record state-affecting actions in the journal (FEAT-1972079854: journal recording)
  // 2. Persist journal to localStorage (FEAT-1972079854: journal survival across reload)
  // 3. Measure tick duration (FEAT-1972079856: perf HUD)
  const tickTracker: TickTrackerState | null = import.meta.env.DEV ? getGlobalTickTracker() : null;
  const wrappedDispatch = useMemo(() => {
    return (action: Action) => {
      // Record action in journal if state-affecting.
      setJournal((j) => {
        const updated = recordAction(j, state.tick, action);
        // Persist journal to localStorage immediately after recording.
        persistJournal(window.localStorage, updated);
        return updated;
      });

      // Dispatch the action.
      const timedAction = () => dispatch(action);

      if (tickTracker && action.type === 'tick') {
        const start = performance.now();
        timedAction();
        const duration = performance.now() - start;
        recordTickDuration(tickTracker, duration);
      } else {
        timedAction();
      }
    };
  }, [tickTracker, state.tick]);

  // Autosave timer: every AUTOSAVE_INTERVAL_MS, persist a savepoint.
  // FEAT-1972079854: rolling autosave with fail-safe error handling.
  useEffect(() => {
    const intervalId = setInterval(() => {
      try {
        // Calculate journalTail: entries added since last savepoint.
        const tail = journalTail(journal, lastSaveIndex);
        const savepoint = createSavepoint(state, tail);
        const success = persistSavepoint(window.localStorage, savepoint);
        setAutoSaveError(!success);
        if (success) {
          // Update lastSaveIndex to mark this checkpoint.
          setLastSaveIndex(journal.entries.length);
        }
      } catch (e) {
        // Catch-all for any error during autosave (e.g., localStorage throws).
        setAutoSaveError(true);
      }
    }, AUTOSAVE_INTERVAL_MS);
    return () => clearInterval(intervalId);
  }, [state, journal, lastSaveIndex]);

  useEffect(() => {
    if (state.speed === 0) return;
    const id = setInterval(() => wrappedDispatch({ type: 'tick' }), SPEED_MS[state.speed]);
    return () => clearInterval(id);
  }, [state.speed, wrappedDispatch]);

  const value = useMemo(() => ({ state, dispatch: wrappedDispatch }), [state, wrappedDispatch]);
  // Use autoSaveError for quiet indicator (available for UI to display if desired).
  return (
    <SimContext.Provider value={value}>
      {autoSaveError && (
        <div
          style={{
            position: 'fixed',
            bottom: '8px',
            right: '8px',
            fontSize: '11px',
            color: '#999',
            fontFamily: 'monospace',
            zIndex: 1,
          }}
          title="Autosave failed; your progress may not be recoverable on reload"
        >
          ⚠ save
        </div>
      )}
      {children}
    </SimContext.Provider>
  );
}

export function useSim(): SimContextValue {
  const v = useContext(SimContext);
  if (!v) throw new Error('useSim must be used inside SimProvider');
  return v;
}
