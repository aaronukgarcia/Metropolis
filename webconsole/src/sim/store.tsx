import { useEffect, useMemo, useReducer, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { reducer, initialState, SPEED_MS, sanitizeTreasury } from './engine';
import type { Action } from './engine';
import { getGlobalTickTracker, recordTickDuration } from './perfhud';
import type { TickTrackerState } from './perfhud';
import {
  recordAction,
  emptyJournal,
  loadJournal,
  journalTail,
  createJournalPersister,
  type Journal,
  type JournalPersister,
} from './journal';
import {
  AUTOSAVE_INTERVAL_MS,
  persistSavepoint,
  createSavepoint,
  restoreFromSavepoint,
  readAllSavepoints,
  mostRecentSavepoint,
  restampSavepointsBuildVersion,
} from './replay';
import {
  needsRebuild,
  replayFromGenesisDefensiveChunked,
  rebuildReport,
  setRebuildInProgress,
  rebuildInProgress,
  estimateRemainingLabel,
  isStaleRebuildChain,
  type RebuildReport,
  type ReplayProgress,
  type ProgressSample,
} from './genesisReplay';
import { attemptWipe, captureOnUnload, captureBeforeWipe } from './captureBeforeWipe';
import {
  buildGameSave,
  parseGameSave,
  suggestedSaveName,
  gameSaveText,
  type GameSave,
} from './gamesave';
import { loadDevCity1, DEVCITY1_NAME } from './devcity';
import {
  listNamedSaves,
  writeNamedSave,
  readNamedSave,
  renameNamedSave,
  getCurrentCityName,
  setCurrentCityName,
  displayCityName,
  cityNameToSlug,
  checkNamedSaveCollision,
  type NamedSaveCollision,
} from './namedsaves';
import { versionRaw, versionBadgeLabel } from './version';
import { currentMapUi, type MapViewState } from './uistate';
import { persistStashedCamera } from './cameraStash';
import { listRecentOpened, recordRecentOpened } from './recentfiles';
import { RebuildPrompt, type RebuildPhase } from '../components/RebuildPrompt';
import { recordError, updateLastKnownState } from './backend';

/**
 * FEAT-1972079897 inc2: the build the RUNNING engine represents. Used both to
 * STAMP a save and to COMPARE at boot, so the two sides are always measured the
 * same way.
 *
 * BUG-468: this MUST be the stable build-time badge (versionBadgeLabel, baked into
 * version.ts by the actual bundle you are running) and NOT the hot live/badge
 * version (liveVersionRef). The hot value is a display overlay that the /version.json
 * poll can move AHEAD of the running engine (a newer commit's number shown while the
 * OLD bundle is still executing — the dogfood hot-upgrade case). It is also null on a
 * fresh boot (before the first poll), so the boot COMPARE would read the badge while a
 * resolution-time STAMP read the hot value — an asymmetry that could never converge,
 * producing the infinite "New build detected" loop. Reading the same build-time badge
 * on both sides makes stamp == compare, so a mismatch clears after ONE resolution.
 */
function currentBuildVersion(): string {
  return versionBadgeLabel();
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
export { useSim } from './simContext';
export type { SimContextValue } from './simContext';
import { SimContext } from './simContext';

type StandbyKind = 'rebuild' | 'load';

function triggerJsonDownload(filename: string, text: string): void {
  const blob = new Blob([text], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

async function pickSaveFile(suggestedName: string, text: string): Promise<void> {
  const w = window as Window & {
    showSaveFilePicker?: (opts: {
      suggestedName: string;
      types: { description: string; accept: Record<string, string[]> }[];
    }) => Promise<{ createWritable: () => Promise<{ write: (d: string) => Promise<void>; close: () => Promise<void> }> }>;
  };
  if (typeof w.showSaveFilePicker === 'function') {
    try {
      const handle = await w.showSaveFilePicker({
        suggestedName,
        types: [{ description: 'Metropolis save', accept: { 'application/json': ['.json'] } }],
      });
      const writable = await handle.createWritable();
      await writable.write(text);
      await writable.close();
      return;
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
    }
  }
  triggerJsonDownload(suggestedName, text);
}

function pickOpenFile(): Promise<string | null> {
  const w = window as Window & {
    showOpenFilePicker?: (opts: {
      types: { description: string; accept: Record<string, string[]> }[];
      multiple?: boolean;
    }) => Promise<Array<{ getFile: () => Promise<File> }>>;
  };
  if (typeof w.showOpenFilePicker === 'function') {
    return w
      .showOpenFilePicker({
        types: [{ description: 'Metropolis save', accept: { 'application/json': ['.json'] } }],
        multiple: false,
      })
      .then(async ([handle]) => {
        const file = await handle.getFile();
        return file.text();
      })
      .catch((e: unknown) => {
        if (e instanceof DOMException && e.name === 'AbortError') return null;
        throw e;
      });
  }
  return new Promise((resolve) => {
    const input = document.createElement('input');
    input.type = 'file';
    input.accept = 'application/json,.json';
    input.onchange = () => {
      const file = input.files?.[0];
      if (!file) {
        resolve(null);
        return;
      }
      void file.text().then(resolve);
    };
    input.click();
  });
}

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
        state: sanitizeTreasury(restoreResult.state),
        journal: loadedJournal,
        saveIndex: loadedJournal.entries.length,
        pendingRebuild: crossBuild
          ? { savedVersion: most?.buildVersion ?? null, currentVersion: running, camera: most?.camera ?? null, kind: 'rebuild' as StandbyKind }
          : null,
      };
    }
    const fresh =
      typeof process !== 'undefined' && process.env.NODE_TEST_CONTEXT
        ? initialState()
        : loadDevCity1();
    return { state: sanitizeTreasury(fresh), journal: emptyJournal(), saveIndex: 0, pendingRebuild: null };
  });

  const [state, dispatch] = useReducer(reducer, boot.state);
  const [cityName, setCityName] = useState(() => {
    try {
      return getCurrentCityName(window.localStorage);
    } catch {
      return DEVCITY1_NAME;
    }
  });
  const [journal, setJournal] = useState<Journal>(boot.journal);
  const journalRef = useRef(journal);
  useEffect(() => {
    journalRef.current = journal;
  }, [journal]);
  // BUG-458: the coalescing journal persister — created once (lazy ref init,
  // no useEffect indirection so it exists on the very first render). `schedule`
  // is called on every dispatch (cheap: debounced/coalesced); `flush` is called
  // at every boundary where an unpersisted tail would be unacceptable to lose
  // (before a save, before a wipe/capture, on unload/hide).
  const journalPersisterRef = useRef<JournalPersister | null>(null);
  if (journalPersisterRef.current === null) {
    journalPersisterRef.current = createJournalPersister(window.localStorage);
  }
  const [lastSaveIndex, setLastSaveIndex] = useState<number>(boot.saveIndex);
  const lastSaveIndexRef = useRef(lastSaveIndex);
  useEffect(() => {
    lastSaveIndexRef.current = lastSaveIndex;
  }, [lastSaveIndex]);
  const hotJournalRef = useRef<Journal | null>(null);
  const [autoSaveError, setAutoSaveError] = useState<boolean>(false);
  // GR#27 (BUG-420): surfaced when a Start Over / reset was ABORTED because the
  // mandatory pre-wipe debug capture failed. The wipe did not happen; state is intact.
  // BUG-513 GAP 3: this same banner state is also used to surface LOAD failures
  // (a load never wipes anything, so the wording must not claim "Start Over"),
  // so `captureErrorKind` tracks which flow produced the message and
  // `captureErrorCode` carries the registry code (e.g. MET-V850) when one exists.
  const [captureError, setCaptureError] = useState<string | null>(null);
  const [captureErrorKind, setCaptureErrorKind] = useState<'reset' | 'load'>('reset');
  const [captureErrorCode, setCaptureErrorCode] = useState<string | undefined>(undefined);
  // BUG-513 GAP 3: single call site for setting the banner so kind/code never
  // drift out of sync with the message.
  const showCaptureError = (msg: string, kind: 'reset' | 'load', code?: string) => {
    setCaptureError(msg);
    setCaptureErrorKind(kind);
    setCaptureErrorCode(code);
  };
  // inc2: cross-build rebuild prompt state. `rebuildDecision` non-null means a
  // save from a different build is awaiting the player's choice; `rebuildPhase`
  // drives the modal (prompt → running → report); `rebuildReportState` carries
  // the before/after metrics once a rebuild has run.
  const [rebuildDecision, setRebuildDecision] = useState(boot.pendingRebuild);
  const rebuildDecisionRef = useRef(rebuildDecision);
  useEffect(() => {
    rebuildDecisionRef.current = rebuildDecision;
  }, [rebuildDecision]);
  const [rebuildPhase, setRebuildPhase] = useState<RebuildPhase>('prompt');
  const [rebuildReportState, setRebuildReportState] = useState<RebuildReport | null>(null);

  // FEAT-1972079917: progress updates during chunked replay (running phase).
  const [rebuildProgress, setRebuildProgress] = useState<ReplayProgress | null>(null);

  // BUG-435: stall watchdog — if progress doesn't advance for WATCHDOG_MS, we move to stalled phase.
  const [stallInfo, setStallInfo] = useState<{ actionsDone: number; actionsTotal: number; phaseLabel: string } | null>(null);
  const watchdogTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // r1 REJECT follow-up BAR-3: generation counter. Bumped on every fresh
  // onRebuild/onRetry dispatch AND on a watchdog-fired stall, so an old chunked
  // chain (its rAF resuming after the watchdog already declared it stalled)
  // recognizes it has been superseded and aborts with no setState/persist.
  const rebuildGenRef = useRef(0);

  // BAR-2: live ETA — samples of (actionsDone, timestamp) collected during the
  // current running chain, used to derive a "~Xm Ys remaining" label from the
  // REAL observed replay rate (never a canned animation).
  const progressSamplesRef = useRef<ProgressSample[]>([]);
  const [etaLabel, setEtaLabel] = useState<string | null>(null);
  const lastProgressRef = useRef<{ actionsDone: number; timestamp: number } | null>(null);

  // Wrap dispatch to:
  // 1. Record state-affecting actions in the journal (FEAT-1972079854: journal recording)
  // 2. Persist journal to localStorage (FEAT-1972079854: journal survival across reload)
  // 3. Measure tick duration (FEAT-1972079856: perf HUD)
  // Optional chaining: import.meta.env is a Vite build-time replacement and is
  // ABSENT under a bare Node/tsx runtime (e.g. the mount smoke test). `?.` degrades
  // to undefined (→ no tick tracker) there instead of throwing, so the real render
  // path runs under test — without it the mount test could only skip (BUG-412 round).
  const tickTracker: TickTrackerState | null = import.meta.env?.DEV ? getGlobalTickTracker() : null;

  // BUG-434 FIX: stateRef pattern. wrappedDispatch must NOT depend on `state` in its
  // dependency list (which changes on EVERY dispatch, causing wrappedDispatch to be
  // recreated on EVERY render). This causes the tick loop effect to re-run constantly,
  // clearing and recreating the interval, which freezes the game under rapid dispatches
  // at turbo speed. Instead, use stateRef.current to access the current state. This is
  // the same pattern used for stateRef below (lines 217-220) for the beforeunload handler.
  const stateRefForDispatch = useRef(state);
  useEffect(() => {
    stateRefForDispatch.current = state;
  }, [state]);

  const wrappedDispatch = useMemo(() => {
    // Journal-record + dispatch the action (shared by the normal path and the
    // guarded reset path below).
    const recordAndDispatch = (action: Action) => {
      // Record action in journal if state-affecting.
      setJournal((j) => {
        const updated = recordAction(j, stateRefForDispatch.current.tick, action);
        // BUG-458: coalesce — schedule a debounced write instead of a full
        // stringify+setItem on EVERY action (O(n) per action, worse as the
        // journal grows). Boundaries that must not lose the tail call
        // journalPersisterRef.current.flush(...) directly, bypassing this.
        journalPersisterRef.current?.schedule(updated);
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
          // BUG-458: flush any debounced journal write BEFORE the pre-wipe
          // capture/wipe boundary — never let a wipe proceed with a stale
          // on-disk journal tail sitting behind a pending debounce timer.
          journalPersisterRef.current?.flush(journalRef.current);
          attemptWipe(stateRefForDispatch.current, versionRaw, window.localStorage, () => recordAndDispatch(action));
          setCaptureError(null);
        } catch (e) {
          const msg = e instanceof Error ? e.message : String(e);
          recordError(`Start Over aborted — pre-wipe debug capture failed: ${msg}. State left intact.`, { type: 'reset-abort' });
          showCaptureError(msg, 'reset');
        }
        return;
      }

      recordAndDispatch(action);
    };
  }, [tickTracker]);

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

  // BUG-427 / GR#27: track the LATEST state in a ref so the beforeunload handler
  // (registered once) always archives the current city, never a stale closure.
  const stateRef = useRef(state);
  useEffect(() => {
    stateRef.current = state;
  }, [state]);

  // GR#27 CAPTURE BEFORE WIPE — RELOAD boundary (BUG-427). BUG-420's attemptWipe
  // only guards the in-app `reset` reducer action; a page RELOAD / version-restart
  // ALSO wipes the running in-memory sim (boot then restores the savepoint) but
  // never fires that guard. Here we best-effort archive the current state to the
  // same pre-wipe ring on `beforeunload`.
  //
  // FAIL-OPEN by nature: beforeunload cannot be blocked or awaited, so unlike the
  // reset path (fail-CLOSED via attemptWipe, above) we cannot abort the wipe if the
  // capture fails — captureOnUnload does only synchronous localStorage work and
  // swallows every error so the unload is never obstructed. A near-immediate unload
  // right after a reset capturing again is harmless (the ring buffer dedups by cap).
  useEffect(() => {
    // BUG-458: flush the debounced journal write at the unload boundary too —
    // a reload/close must not lose journal entries sitting behind a pending
    // debounce timer (best-effort, same synchronous-only constraint as the
    // state capture below).
    const flushJournalNow = () => {
      journalPersisterRef.current?.flush(journalRef.current);
    };
    const onBeforeUnload = () => {
      flushJournalNow();
      captureOnUnload(() => stateRef.current, versionRaw, window.localStorage);
    };
    // Mobile/backgrounded-tab browsers often never fire beforeunload; the
    // visibilitychange:hidden transition is the reliable "about to be killed"
    // signal there, so flush on it too.
    const onVisibilityChange = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') {
        flushJournalNow();
      }
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', onVisibilityChange);
    }
    return () => {
      window.removeEventListener('beforeunload', onBeforeUnload);
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', onVisibilityChange);
      }
    };
  }, []);

  useEffect(() => {
    if (state.speed === 0) return;
    const id = setInterval(() => {
      if (rebuildInProgress) return;
      wrappedDispatch({ type: 'tick' });
    }, SPEED_MS[state.speed]);
    return () => clearInterval(id);
  }, [state.speed, wrappedDispatch]);

  // FEAT-1972079917 / BUG-435: watchdog timeout (ms) — if chunked replay progress
  // doesn't advance for this long, we treat it as stalled and show a retry UI.
  const WATCHDOG_MS = 10_000;

  // Clean up the stall watchdog timer.
  const clearWatchdog = () => {
    if (watchdogTimerRef.current) {
      clearTimeout(watchdogTimerRef.current);
      watchdogTimerRef.current = null;
    }
  };

  // inc2 rebuild handlers (brief §4.4). FEAT-1972079917: uses chunked replay
  // with progress callback and BUG-435 stall watchdog.
  //
  // r1 REJECT follow-up (BAR-3): every fresh dispatch bumps rebuildGenRef and
  // the chain captures that generation (myGen) at start. processChunk checks
  // isStaleRebuildChain(myGen, rebuildGenRef.current) BEFORE doing anything —
  // a superseded chain (an old rAF resuming after a watchdog stall, or after
  // Retry started a new chain) aborts silently: no setState, no persist.
  const onRebuild = () => {
    const decision = rebuildDecisionRef.current;
    if (!decision) return;
    rebuildGenRef.current += 1;
    const myGen = rebuildGenRef.current;
    setRebuildPhase('running');
    setRebuildProgress(null);
    setStallInfo(null);
    setEtaLabel(null);
    clearWatchdog();
    lastProgressRef.current = null;
    progressSamplesRef.current = [];
    setRebuildInProgress(true);

    try {
      const gen = replayFromGenesisDefensiveChunked(hotJournalRef.current ?? journal);
      let result: ReturnType<typeof replayFromGenesisDefensiveChunked.prototype.return>;

      const processChunk = () => {
        // BAR-3: a newer chain (Retry, or a watchdog stall) has taken over —
        // this chain is stale. Abort with no setState/persist and drop the
        // watchdog it might still be holding.
        if (isStaleRebuildChain(myGen, rebuildGenRef.current)) {
          clearWatchdog();
          // BUG-460 FIX A: abandoning the generator without closing it would leave
          // genesisReplay's module-scoped replay-mode flag stuck ON (its try/finally
          // only runs to completion or on an explicit .return()/.throw()), silently
          // starving every SUBSEQUENT normal reducer call of its roadConnectivity
          // recompute. Close it so the finally fires.
          try {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any -- the
            // return value is discarded; only the generator-close side effect matters.
            gen.return(undefined as any);
          } catch {
            // Closing an already-finished/closed generator is a no-op; ignore.
          }
          return;
        }
        try {
          const chunk = gen.next();
          if (chunk.done) {
            // Replay complete — finalize.
            clearWatchdog();
            result = chunk.value as ReturnType<typeof replayFromGenesisDefensiveChunked.prototype.return>;

            setRebuildInProgress(false);

            if (result.crashed) {
              recordError(`Rebuild crashed during replay: ${result.crashError}. Kept the old snapshot.`, {
                type: 'app',
                action: 'rebuild',
              });
              setRebuildDecision(null);
              setRebuildPhase('prompt');
              return;
            }

            // Report compares the OLD restored snapshot (current `state`) to the replay.
            const report = rebuildReport(state, result.state, result.skipped);
            setRebuildReportState(report);

            // Persist the rebuilt city as a fresh savepoint stamped with the CURRENT
            // build, and carry the camera across the reload, so resuming boots straight
            // into the new-engine city with no re-prompt and no view jump.
            const running = currentBuildVersion();
            const rebuiltSave = createSavepoint(result.state, [], new Date(), running, decision.camera ?? currentCamera());
            persistSavepoint(window.localStorage, rebuiltSave);
            persistStashedCamera(window.localStorage, decision.camera ?? currentCamera());
            // BUG-458: flush (not schedule) — a rebuild is a wipe/replace boundary.
            if (hotJournalRef.current) journalPersisterRef.current?.flush(hotJournalRef.current);

            setRebuildPhase('report');
            return;
          }

          // Progress update.
          const progress = chunk.value as ReplayProgress;
          setRebuildProgress(progress);

          // BAR-2: record this sample and derive a live ETA from the observed
          // actions/sec — never a canned animation. Kept even across repeated
          // actionsDone values below; estimateRemainingLabel tolerates that.
          const now = performance.now();
          progressSamplesRef.current.push({ actionsDone: progress.actionsDone, timestamp: now });
          setEtaLabel(estimateRemainingLabel(progressSamplesRef.current, progress.actionsTotal));

          // BUG-435: stall watchdog. If this progress update is on a different action
          // count than the last one, reset the watchdog timer.
          if (lastProgressRef.current?.actionsDone !== progress.actionsDone) {
            clearWatchdog();
            lastProgressRef.current = { actionsDone: progress.actionsDone, timestamp: now };
            watchdogTimerRef.current = setTimeout(function fireWatchdog() {
              // BAR-5: a backgrounded tab throttles requestAnimationFrame (the
              // replay's own driver) but NOT setTimeout, so a merely-hidden tab
              // looks identical to a genuine stall. Re-arm instead of declaring
              // one when the tab is hidden — rAF will resume and make real
              // progress once it's foregrounded again.
              if (typeof document !== 'undefined' && document.visibilityState === 'hidden') {
                watchdogTimerRef.current = setTimeout(fireWatchdog, WATCHDOG_MS);
                return;
              }
              // No progress for WATCHDOG_MS while visible — genuine stall.
              clearWatchdog();
              setRebuildInProgress(false);
              setStallInfo({
                actionsDone: progress.actionsDone,
                actionsTotal: progress.actionsTotal,
                phaseLabel: progress.phaseLabel,
              });
              setRebuildPhase('stalled');
              // BAR-3: bump the generation so this now-dead chain's already-
              // scheduled rAF resume (if any) sees itself as stale and exits.
              rebuildGenRef.current += 1;
            }, WATCHDOG_MS);
          }

          // Schedule the next chunk on the next frame.
          requestAnimationFrame(processChunk);
        } catch (e) {
          clearWatchdog();
          setRebuildInProgress(false);
          const msg = e instanceof Error ? e.message : String(e);
          recordError(`Rebuild failed: ${msg}. Kept the old snapshot.`, { type: 'app', action: 'rebuild' });
          setRebuildDecision(null);
          setRebuildPhase('prompt');
        }
      };

      processChunk();
    } catch (e) {
      clearWatchdog();
      setRebuildInProgress(false);
      const msg = e instanceof Error ? e.message : String(e);
      recordError(`Rebuild setup failed: ${msg}. Kept the old snapshot.`, { type: 'app', action: 'rebuild' });
      setRebuildDecision(null);
      setRebuildPhase('prompt');
    }
  };

  const onKeep = () => {
    // Keep the old snapshot already restored at boot (pre-inc2 behaviour).
    // BUG-468: re-stamp the persisted savepoint to the running build NOW — do not
    // wait for the next autosave. If the player reloads (or a wipe fires) before an
    // autosave lands, the stale buildVersion would re-trigger the prompt on every
    // subsequent load — the infinite "New build detected" loop. Re-stamping here
    // clears the mismatch after this ONE resolution. Flush the journal so any
    // pending write is on disk before a possible reload.
    try {
      restampSavepointsBuildVersion(window.localStorage, currentBuildVersion());
    } catch {
      /* storage error — the prompt may recur, but never crash the resolution */
    }
    journalPersisterRef.current?.flush(journalRef.current);
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
    // BUG-468: belt-and-braces — re-stamp the persisted savepoint to the running
    // build before the reload so this resume path can NEVER leave a stale
    // buildVersion behind (covers any dismiss-that-continues route as well as the
    // rebuild-report resume). Idempotent when the stamp already matches.
    try {
      restampSavepointsBuildVersion(window.localStorage, currentBuildVersion());
    } catch {
      /* storage error — never block the resume */
    }
    setRebuildDecision(null);
    window.location.reload();
  };

  const onRetry = () => {
    // BUG-435: retry after a stall. Clear the stall state and go back to running.
    setRebuildProgress(null);
    setStallInfo(null);
    clearWatchdog();
    lastProgressRef.current = null;
    onRebuild();
  };

  const captureOutgoingOrDownload = (): boolean => {
    // BUG-458: flush before this capture/wipe boundary (loading a save replaces
    // the current city, i.e. wipes it) — the on-disk journal for the OUTGOING
    // city must be current, not stuck behind a pending debounce.
    journalPersisterRef.current?.flush(journalRef.current);
    try {
      captureBeforeWipe(stateRefForDispatch.current, versionRaw, window.localStorage);
      return true;
    } catch {
      try {
        const outgoing = buildGameSave({
          state: stateRefForDispatch.current,
          journal: journalRef.current,
          journalTail: journalTail(journalRef.current, lastSaveIndexRef.current),
          name: 'pre-wipe',
          buildVersion: currentBuildVersion(),
          camera: currentCamera(),
        });
        triggerJsonDownload(`pre-wipe-${suggestedSaveName(stateRefForDispatch.current.tick)}`, gameSaveText(outgoing));
        return true;
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        recordError(`Load aborted — pre-wipe capture failed: ${msg}. State left intact.`, { type: 'reset-abort' });
        showCaptureError(msg, 'load');
        return false;
      }
    }
  };

  const rememberOpened = (save: GameSave, opts?: { confirmedOverwrite?: boolean }) => {
    const snap = save.savepoint.snapshot;
    // BUG-457: neither of these is allowed to swallow a quota failure silently
    // (GR#1/#17) — both now return a success boolean (routed through the
    // shared safe-setItem helper) instead of throwing, so report honestly
    // through the same error-registry path the rest of the save flow uses.
    // The actual city/save is unaffected either way; only the convenience
    // lists (Recent / named-city slot) may be stale.
    let recentOk = false;
    try {
      recentOk = recordRecentOpened(window.localStorage, {
        name: save.name,
        tick: snap.tick,
        population: snap.population,
        funds: snap.funds,
      });
    } catch {
      recentOk = false;
    }
    if (!recentOk) {
      recordError('Recent-cities list not updated (storage quota). The saved city itself is unaffected.', {
        type: 'app',
        action: 'save',
      });
    }
    // BUG-512: BUG-445 gated the named-save collision check at the Save-As/
    // rename UI vectors only. This is the SAME writeNamedSave call, reached
    // from a plain saveGame()/load's rememberOpened, so it was still ungated —
    // loading or plain-saving onto a name that collides a DIFFERENT existing
    // slot silently clobbered it. Apply the identical BUG-445 pattern here:
    // a same-city re-save (or an already-confirmed overwrite from the
    // saveGameAs/renameCity flows) proceeds; a different-city collision is
    // refused, not written, and reported — never silently overwritten.
    const collision = checkNamedSaveCollision(window.localStorage, save.name);
    let namedOk = false;
    if (collision && !opts?.confirmedOverwrite) {
      recordError(
        `Named-city slot NOT updated: a different city named "${collision.existingName}" already exists at slot "${collision.slug}". Use Save As to confirm overwrite.`,
        { type: 'app', action: 'save', code: 'MET-V851' },
      );
    } else {
      try {
        namedOk = writeNamedSave(window.localStorage, save);
      } catch {
        namedOk = false;
      }
      if (!namedOk) {
        recordError(
          'Named-city slot not updated (storage quota). Use Config → Reclaim storage, then Save As again.',
          { type: 'app', action: 'save' },
        );
      }
    }
  };

  const buildCurrentSave = (name: string) => {
    const s = stateRefForDispatch.current;
    return buildGameSave({
      state: s,
      // BUG-439 FIX: the GameSave's `journal` field is the FULL action history that
      // a later rebuild (replayFromGenesisDefensiveChunked, driven off the live
      // `journal`/hotJournalRef state — see applyLoadedSave below) replays from
      // genesis. Writing `emptyJournal()` here discarded that history at save time,
      // so ANY save (manual, Save As, or the exported file) produced an empty
      // journal on disk — a subsequent rebuild-after-load had nothing to replay and
      // rebuilt a blank/initial city instead of reproducing the saved one.
      // `journal` (the live React state) is captured fresh here because this
      // closure is re-created on every render — not a stale ref.
      journal,
      journalTail: [],
      name: displayCityName(name),
      buildVersion: currentBuildVersion(),
      camera: currentCamera(),
    });
  };

  const finishLoadOverlay = (ok: boolean, msg?: string) => {
    setRebuildInProgress(false);
    if (!ok) {
      if (msg) {
        recordError(msg, { type: 'app', action: 'load' });
        showCaptureError(msg, 'load');
      }
      setRebuildDecision(null);
      setRebuildPhase('prompt');
      setRebuildProgress(null);
      hotJournalRef.current = null;
      return;
    }
    setRebuildDecision(null);
    setRebuildPhase('prompt');
    setRebuildProgress(null);
  };

  const applyLoadedSave = (save: GameSave) => {
    const running = currentBuildVersion();
    const decision = {
      savedVersion: save.buildVersion,
      currentVersion: running,
      camera: save.savepoint.camera ?? null,
      kind: 'load' as StandbyKind,
    };
    rebuildDecisionRef.current = decision;
    setRebuildDecision(decision);
    setRebuildPhase('running');
    setRebuildProgress({ actionsDone: 0, actionsTotal: 4, phaseLabel: 'Loading city…' });
    setRebuildInProgress(true);

    window.setTimeout(() => {
      try {
        setRebuildProgress({ actionsDone: 1, actionsTotal: 4, phaseLabel: 'Archiving current city…' });
        if (!captureOutgoingOrDownload()) {
          finishLoadOverlay(false, 'Load aborted — could not archive the current city.');
          return;
        }
        const snapshot = sanitizeTreasury(save.savepoint.snapshot);
        setRebuildProgress({ actionsDone: 2, actionsTotal: 4, phaseLabel: 'Writing session save…' });
        const persisted = persistSavepoint(window.localStorage, {
          ...save.savepoint,
          snapshot,
          // journalTail stays [] deliberately: this is the tail-since-snapshot used
          // by the FAST boot-time restoreFromSavepoint() path, and `snapshot` here
          // already IS the fully-replayed end state, so there is no pending tail.
          // This is unrelated to `save.journal` (the FULL history) restored below.
          journalTail: [],
          buildVersion: running,
        });
        // BUG-439 FIX: restore the loaded save's FULL journal (save.journal) into
        // the live journal state/on-disk journal file instead of discarding it as
        // emptyJournal(). Without this, a rebuild triggered right after a load (or
        // at a later version-crossing boot) replayed an empty journal from genesis
        // and produced a blank/initial city instead of reproducing the loaded one
        // (replayFromGenesisDefensiveChunked reads hotJournalRef.current ?? journal
        // — both must reflect the loaded history, not an empty one).
        // BUG-458: flush (not schedule) — loading a save resets the journal boundary.
        // (hotJournalRef is deliberately left untouched: setJournal below lands in
        // the same render batch as the phase transition to 'prompt', so `journal`
        // is already correct by the time the Rebuild button is clickable — setting
        // hotJournalRef here would instead go STALE the moment the player keeps
        // playing post-load and a LATER version-crossing rebuild reads it back.)
        journalPersisterRef.current?.flush(save.journal);
        persistStashedCamera(window.localStorage, save.savepoint.camera ?? currentCamera());
        setRebuildProgress({ actionsDone: 3, actionsTotal: 4, phaseLabel: 'Hydrating city…' });
        setCityName(displayCityName(save.name));
        try {
          setCurrentCityName(window.localStorage, save.name);
        } catch {
          /* ignore */
        }
        setJournal(save.journal);
        setLastSaveIndex(save.journal.entries.length);
        dispatch({ type: 'hydrate', state: snapshot });
        if (!persisted) {
          recordError('City loaded in memory; session persist failed (quota). Use Config → Clear journal, then Save.', {
            type: 'app',
            action: 'load',
          });
        }
        rememberOpened(save);
        setRebuildProgress({ actionsDone: 4, actionsTotal: 4, phaseLabel: 'Loaded' });
        finishLoadOverlay(true);
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e);
        finishLoadOverlay(false, `Load failed: ${msg}. Current city left intact.`);
      }
    }, 50);
  };

  const saveGame = async (): Promise<boolean> => {
    try {
      const save = buildCurrentSave(cityName);
      const ok = persistSavepoint(window.localStorage, save.savepoint);
      // BUG-458: flush — a save is exactly the boundary where losing the
      // journal tail would be unacceptable (it's now folded into the savepoint).
      journalPersisterRef.current?.flush(emptyJournal());
      setLastSaveIndex(journalRef.current.entries.length);
      if (!ok) {
        recordError('Save failed (storage quota). Clear journal in Config, then try again or use Save As.', {
          type: 'app',
          action: 'save',
        });
        setAutoSaveError(true);
        return false;
      }
      setAutoSaveError(false);
      rememberOpened(save);
      return true;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      recordError(`Save failed: ${msg}`, { type: 'app', action: 'save' });
      return false;
    }
  };

  const saveGameAs = async (
    name?: string,
    opts?: { confirmedOverwrite?: boolean },
  ): Promise<{ ok: boolean; collision?: NamedSaveCollision }> => {
    try {
      const label = displayCityName(name ?? cityName);
      // BUG-445/AC-5: a Save-As that would silently clobber a DIFFERENT
      // city's slot is refused (not written at all) unless the caller has
      // already obtained the user's explicit confirmation. A re-save onto
      // the same city's own slot never collides (checkNamedSaveCollision
      // returns null for that case) and proceeds exactly as before.
      const collision = checkNamedSaveCollision(window.localStorage, label);
      if (collision && !opts?.confirmedOverwrite) {
        recordError(
          `Save As refused: a different city named "${collision.existingName}" already exists at slot "${collision.slug}". Confirm overwrite to proceed.`,
          { type: 'app', action: 'save', code: 'MET-V851' },
        );
        return { ok: false, collision };
      }
      const save = buildCurrentSave(label);
      persistSavepoint(window.localStorage, save.savepoint);
      // BUG-458: flush — Save As is a save boundary, same as saveGame.
      journalPersisterRef.current?.flush(emptyJournal());
      setLastSaveIndex(journalRef.current.entries.length);
      setCityName(label);
      try {
        setCurrentCityName(window.localStorage, label);
      } catch {
        /* ignore */
      }
      // BUG-512: this call site already resolved any collision above (either
      // there was none, or the caller explicitly confirmed the overwrite) —
      // thread that confirmation through so rememberOpened's own collision
      // gate (guarding the OTHER two, previously-ungated call sites) doesn't
      // re-block a write the user already approved.
      rememberOpened(save, { confirmedOverwrite: true });
      await pickSaveFile(suggestedSaveName(save.savepoint.snapshot.tick, label), gameSaveText(save));
      return { ok: true };
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      recordError(`Save As failed: ${msg}`, { type: 'app', action: 'save' });
      return { ok: false };
    }
  };

  const loadGame = async () => {
    let text: string | null;
    try {
      text = await pickOpenFile();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      recordError(`Load failed: ${msg}`, { type: 'app', action: 'load' });
      showCaptureError(msg, 'load');
      return;
    }
    if (text == null) return;
    if (text.length > 15_000_000) {
      const msg = 'Load refused: file is larger than 15 MB.';
      recordError(msg, { type: 'app', action: 'load' });
      showCaptureError(msg, 'load');
      return;
    }
    let parsed;
    try {
      parsed = parseGameSave(text);
    } catch (e) {
      // BUG-513 GAP 2: parseGameSave rejects via codedError (MET-V850) — thread
      // that code through recordError so it survives into the ring/debug.json
      // instead of being dropped at this boundary (gap-1 already renders it).
      const msg = e instanceof Error ? e.message : String(e);
      const code = (e as { code?: string })?.code;
      recordError(`Load refused: ${msg}`, { type: 'app', action: 'load', code });
      showCaptureError(msg, 'load', code);
      return;
    }
    if (!parsed.ok || !parsed.save) {
      recordError(`Load refused: ${parsed.reason ?? 'invalid save'}`, { type: 'app', action: 'load' });
      showCaptureError(parsed.reason ?? 'invalid save', 'load');
      return;
    }
    applyLoadedSave(parsed.save);
  };

  const loadNamed = async (slug: string) => {
    const save = readNamedSave(window.localStorage, slug);
    if (!save) {
      recordError(`Load refused: no city named ${slug}`, { type: 'app', action: 'load' });
      return;
    }
    applyLoadedSave(save);
  };

  const listSaves = () => {
    try {
      return listNamedSaves(window.localStorage);
    } catch {
      return [];
    }
  };

  const listRecent = () => {
    try {
      return listRecentOpened(window.localStorage);
    } catch {
      return [];
    }
  };

  const renameCity = (
    name: string,
    opts?: { confirmedOverwrite?: boolean },
  ): { ok: boolean; collision?: NamedSaveCollision } => {
    const next = displayCityName(name);
    const oldSlug = cityNameToSlug(cityName);
    const newSlug = cityNameToSlug(next);
    // BUG-445/AC-5: renaming onto a slug already held by a DIFFERENT city is
    // the same silent-destruction hazard as Save As — refuse without
    // confirmation. Renaming within your own existing slug (newSlug ===
    // oldSlug, e.g. a case/whitespace-only edit) is never a collision.
    if (newSlug !== oldSlug) {
      const collision = checkNamedSaveCollision(window.localStorage, next);
      if (collision && !opts?.confirmedOverwrite) {
        recordError(
          `Rename refused: a different city named "${collision.existingName}" already exists at slot "${collision.slug}". Confirm overwrite to proceed.`,
          { type: 'app', action: 'save', code: 'MET-V851' },
        );
        return { ok: false, collision };
      }
    }
    try {
      if (!renameNamedSave(window.localStorage, oldSlug, next)) {
        setCurrentCityName(window.localStorage, next);
      }
    } catch {
      return { ok: false };
    }
    setCityName(next);
    return { ok: true };
  };

  const value = useMemo(
    () => ({
      state,
      dispatch: wrappedDispatch,
      cityName,
      listSaves,
      listRecent,
      saveGame,
      saveGameAs,
      loadGame,
      loadNamed,
      renameCity,
    }),
    [state, wrappedDispatch, cityName],
  );
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
          title={
            captureErrorKind === 'load'
              ? 'The load was refused/aborted. Your current city is unchanged.'
              : 'The reset was aborted because the mandatory pre-wipe debug capture failed. Your city is unchanged.'
          }
          onClick={() => setCaptureError(null)}
        >
          {/* BUG-513 GAP 3: this banner is fired for both Start-Over aborts AND
              load failures/refusals — the wording must not claim "Start Over"
              for a load, and the registry code (when present) must be visible
              here, not just in the Errors panel. */}
          {captureErrorKind === 'load'
            ? `⚠ Load failed${captureErrorCode ? ` [${captureErrorCode}]` : ''} — ${captureError}. Your city is intact.`
            : `⚠ Start Over aborted — could not archive debug snapshot (${captureError}). Your city is intact.`}
        </div>
      )}
      {rebuildDecision && (
        <RebuildPrompt
          phase={rebuildPhase}
          savedVersion={rebuildDecision.savedVersion}
          currentVersion={rebuildDecision.currentVersion}
          report={rebuildReportState}
          progress={rebuildProgress}
          eta={etaLabel}
          stallInfo={stallInfo}
          onRebuild={onRebuild}
          onKeep={onKeep}
          onFresh={onFresh}
          onResume={onResume}
          onRetry={onRetry}
          busyLabel={rebuildDecision.kind === 'load' ? 'Loading your city…' : undefined}
        />
      )}
      {children}
    </SimContext.Provider>
  );
}
