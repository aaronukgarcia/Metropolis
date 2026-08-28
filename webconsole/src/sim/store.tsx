import { createContext, useContext, useEffect, useMemo, useReducer, useState } from 'react';
import type { Dispatch, ReactNode } from 'react';
import type { SimState } from './types';
import { reducer, initialState, SPEED_MS } from './engine';
import type { Action } from './engine';
import { getGlobalTickTracker, recordTickDuration } from './perfhud';
import type { TickTrackerState } from './perfhud';
import { recordAction, emptyJournal, persistJournal, loadJournal, journalTail, type Journal } from './journal';
import {
  AUTOSAVE_INTERVAL_MS,
  persistSavepoint,
  createSavepoint,
  restoreFromSavepoint,
  readAllSavepoints,
  mostRecentSavepoint,
} from './replay';
import {
  needsRebuild,
  replayFromGenesisDefensive,
  rebuildReport,
  type RebuildReport,
} from './genesisReplay';
import { attemptWipe } from './captureBeforeWipe';
import { versionRaw, versionBadgeLabel } from './version';
import { getLiveVersion } from './liveVersionRef';
import { currentMapUi, type MapViewState } from './uistate';
import { persistStashedCamera } from './cameraStash';
import { RebuildPrompt, type RebuildPhase } from '../components/RebuildPrompt';
import { recordError, updateLastKnownState } from './backend';

/**
 * FEAT-1972079897 inc2: the build the RUNNING engine represents. Prefer the
 * freshest live/badge version (BUG-424's liveVersionRef, set by useLiveVersion on
 * each poll); before any poll it is null, so fall back to the build-time badge
 * label baked into version.ts. Used both to STAMP a save and to COMPARE at boot,
 * so the two sides are always measured the same way.
 */
function currentBuildVersion(): string {
  return getLiveVersion() ?? versionBadgeLabel();
}

/** The camera the player is currently looking at, or null before the map mounts. */
function currentCamera(): MapViewState | null {
  return currentMapUi().view;
}

// Pure engine logic lives in engine.ts so it is unit-testable without JSX.
// Re-exported here for backward compatibility with existing `'../sim/store'`
// imports across the app.
export {
  levelOf,
  xpForLevel,
  specUnlocked,
  UNLOCK_ALL_COST,
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
  // FEAT-1972079854: boot-time recovery from a persisted savepoint + journal,
  // computed SYNCHRONOUSLY in a lazy useState initializer (runs exactly once) so
  // the context value is present on the very first render — there is no null
  // phase — and every hook below is called UNCONDITIONALLY (Rules of Hooks).
  // The prior mount-effect + early-return version rendered a null-context first
  // pass that crashed every consumer with "useSim must be used inside
  // SimProvider" (the whole app went blank), and also called useReducer/useState
  // after a conditional return.
  const [boot] = useState(() => {
    // inc2 (brief §4.3-4.4): if a persisted save was produced under a DIFFERENT
    // build, do NOT silently snapshot-restore — flag a pending rebuild decision so
    // the player is offered Rebuild / Keep / Fresh. We still restore the OLD
    // snapshot underneath so the app is usable behind the prompt; the choice then
    // either replays on the new engine, keeps this, or starts fresh.
    const most = mostRecentSavepoint(readAllSavepoints(window.localStorage));
    const running = currentBuildVersion();
    const crossBuild = !!most && needsRebuild(most.buildVersion, running);

    const restoreResult = restoreFromSavepoint(window.localStorage);
    if (restoreResult.success && restoreResult.state) {
      const loadedJournal = loadJournal(window.localStorage);
      return {
        state: restoreResult.state,
        journal: loadedJournal,
        saveIndex: loadedJournal.entries.length,
        pendingRebuild: crossBuild
          ? { savedVersion: most?.buildVersion ?? null, currentVersion: running, camera: most?.camera ?? null }
          : null,
      };
    }
    return { state: initialState(), journal: emptyJournal(), saveIndex: 0, pendingRebuild: null };
  });

  const [state, dispatch] = useReducer(reducer, boot.state);
  const [journal, setJournal] = useState<Journal>(boot.journal);
  const [lastSaveIndex, setLastSaveIndex] = useState<number>(boot.saveIndex);
  const [autoSaveError, setAutoSaveError] = useState<boolean>(false);
  // GR#27 (BUG-420): surfaced when a Start Over / reset was ABORTED because the
  // mandatory pre-wipe debug capture failed. The wipe did not happen; state is intact.
  const [captureError, setCaptureError] = useState<string | null>(null);
  // inc2: cross-build rebuild prompt state. `rebuildDecision` non-null means a
  // save from a different build is awaiting the player's choice; `rebuildPhase`
  // drives the modal (prompt → running → report); `rebuildReportState` carries
  // the before/after metrics once a rebuild has run.
  const [rebuildDecision, setRebuildDecision] = useState(boot.pendingRebuild);
  const [rebuildPhase, setRebuildPhase] = useState<RebuildPhase>('prompt');
  const [rebuildReportState, setRebuildReportState] = useState<RebuildReport | null>(null);

  // Wrap dispatch to:
  // 1. Record state-affecting actions in the journal (FEAT-1972079854: journal recording)
  // 2. Persist journal to localStorage (FEAT-1972079854: journal survival across reload)
  // 3. Measure tick duration (FEAT-1972079856: perf HUD)
  // Optional chaining: import.meta.env is a Vite build-time replacement and is
  // ABSENT under a bare Node/tsx runtime (e.g. the mount smoke test). `?.` degrades
  // to undefined (→ no tick tracker) there instead of throwing, so the real render
  // path runs under test — without it the mount test could only skip (BUG-412 round).
  const tickTracker: TickTrackerState | null = import.meta.env?.DEV ? getGlobalTickTracker() : null;
  const wrappedDispatch = useMemo(() => {
    // Journal-record + dispatch the action (shared by the normal path and the
    // guarded reset path below).
    const recordAndDispatch = (action: Action) => {
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

    return (action: Action) => {
      // GR#27 CAPTURE BEFORE WIPE (fail-closed): a reset wipes the running
      // SimState, so it may proceed ONLY after the full debug JSON of the
      // current state is archived. attemptWipe captures first and runs the
      // wipe callback only if the capture did not throw; on failure we abort
      // (no journal record, no dispatch — state untouched) and surface an error.
      if (action.type === 'reset') {
        try {
          attemptWipe(state, versionRaw, window.localStorage, () => recordAndDispatch(action));
          setCaptureError(null);
        } catch (e) {
          const msg = e instanceof Error ? e.message : String(e);
          recordError(`Start Over aborted — pre-wipe debug capture failed: ${msg}. State left intact.`, { type: 'reset-abort' });
          setCaptureError(msg);
        }
        return;
      }

      recordAndDispatch(action);
    };
  }, [tickTracker, state]);

  // Autosave timer: every AUTOSAVE_INTERVAL_MS, persist a savepoint.
  // FEAT-1972079854: rolling autosave with fail-safe error handling.
  useEffect(() => {
    const intervalId = setInterval(() => {
      try {
        // Calculate journalTail: entries added since last savepoint.
        const tail = journalTail(journal, lastSaveIndex);
        // inc2: stamp the save with the running build + current camera so a later
        // boot on a new build can detect the change and offer a rebuild.
        const savepoint = createSavepoint(state, tail, new Date(), currentBuildVersion(), currentCamera());
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

  // GR#1 (FEAT-1972079898): feed the error envelope's "heap" ref with a bounded
  // snapshot of the live sim state, so any error trapped by recordError can attach
  // the state at crash time. This lives OUTSIDE SimState — no Date.now/Math.random
  // enters the reducer.
  useEffect(() => {
    updateLastKnownState(state);
  }, [state]);

  useEffect(() => {
    if (state.speed === 0) return;
    const id = setInterval(() => wrappedDispatch({ type: 'tick' }), SPEED_MS[state.speed]);
    return () => clearInterval(id);
  }, [state.speed, wrappedDispatch]);

  // inc2 rebuild handlers (brief §4.4). The genesis replay is a pure, synchronous
  // headless loop (sub-second), so it runs inline; the whole thing is wrapped so a
  // hard crash is caught and recorded (GR#1) and never bricks the boot.
  const onRebuild = () => {
    if (!rebuildDecision) return;
    setRebuildPhase('running');
    try {
      const result = replayFromGenesisDefensive(journal);
      if (result.crashed) {
        recordError(`Rebuild crashed during replay: ${result.crashError}. Kept the old snapshot.`, {
          type: 'app',
          action: 'rebuild',
        });
        setRebuildDecision(null); // fall back to the already-restored old snapshot
        return;
      }
      // Report compares the OLD restored snapshot (current `state`) to the replay.
      const report = rebuildReport(state, result.state, result.skipped);
      setRebuildReportState(report);
      // Persist the rebuilt city as a fresh savepoint stamped with the CURRENT
      // build, and carry the camera across the reload, so resuming boots straight
      // into the new-engine city with no re-prompt and no view jump.
      const running = currentBuildVersion();
      const rebuiltSave = createSavepoint(result.state, [], new Date(), running, rebuildDecision.camera ?? currentCamera());
      persistSavepoint(window.localStorage, rebuiltSave);
      persistStashedCamera(window.localStorage, rebuildDecision.camera ?? currentCamera());
      setRebuildPhase('report');
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      recordError(`Rebuild failed: ${msg}. Kept the old snapshot.`, { type: 'app', action: 'rebuild' });
      setRebuildDecision(null);
    }
  };

  const onKeep = () => {
    // Keep the old snapshot already restored at boot (pre-inc2 behaviour). The next
    // autosave re-stamps it with the current build, so it stops prompting.
    setRebuildDecision(null);
  };

  const onFresh = () => {
    // Discard: reset routes through the guarded capture-before-wipe path.
    setRebuildDecision(null);
    wrappedDispatch({ type: 'reset' });
  };

  const onResume = () => {
    // The rebuilt city is already persisted + stamped current; reload boots into it
    // on the new engine with matching versions (no re-prompt).
    setRebuildDecision(null);
    window.location.reload();
  };

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
      {captureError && (
        <div
          role="alert"
          style={{
            position: 'fixed',
            bottom: '8px',
            left: '50%',
            transform: 'translateX(-50%)',
            maxWidth: '90vw',
            padding: '6px 12px',
            fontSize: '12px',
            color: '#fff',
            background: '#a11',
            borderRadius: '4px',
            fontFamily: 'monospace',
            zIndex: 2,
          }}
          title="The reset was aborted because the mandatory pre-wipe debug capture failed. Your city is unchanged."
          onClick={() => setCaptureError(null)}
        >
          ⚠ Start Over aborted — could not archive debug snapshot ({captureError}). Your city is intact.
        </div>
      )}
      {rebuildDecision && (
        <RebuildPrompt
          phase={rebuildPhase}
          savedVersion={rebuildDecision.savedVersion}
          currentVersion={rebuildDecision.currentVersion}
          report={rebuildReportState}
          onRebuild={onRebuild}
          onKeep={onKeep}
          onFresh={onFresh}
          onResume={onResume}
        />
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
