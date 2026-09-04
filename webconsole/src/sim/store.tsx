import { useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { reducer, initialState, SPEED_MS, sanitizeTreasury, nextSafeBuildingId, CONSOLIDATOR_ENABLED_DEFAULT } from './engine';
import type { Action } from './engine';
// CONSOLIDATOR AUDIT TRAIL (see the store-level effect below for the
// TAB-MOUNTED-ONLY fix this import supports).
import {
  monthlyScopeOf,
  strandedCapacityReport,
  topOpportunities,
  currentMonthOpportunities,
  TOTAL_SECTIONS,
} from './consolidator';
import { postConsolidatorAudit, isAuditDue, AUDIT_POLL_MS, AUDIT_TOP_LIMIT } from './consolidatorAudit';
import type { SimState } from './types';
import { getGlobalTickTracker, recordTickDuration } from './perfhud';
import type { TickTrackerState } from './perfhud';
import {
  recordAction,
  emptyJournal,
  loadJournal,
  journalTail,
  createJournalPersister,
  JOURNAL_KEY,
  type Journal,
  type JournalPersister,
  type JournalEntry,
} from './journal';
import {
  AUTOSAVE_INTERVAL_MS,
  persistSavepoint,
  createSavepoint,
  restoreFromSavepoint,
  readAllSavepoints,
  mostRecentSavepoint,
  restampSavepointsBuildVersion,
  prepareRestoreForChunkedTail,
  replayTailChunked,
  checkConsistencyRecoveringStaleFlows,
  SAVEPOINT_CAP,
  SAVEPOINT_KEY_PREFIX,
  LEGACY_LINEAGE_ID,
  readCurrentLineageId,
  writeCurrentLineageId,
  mintLineageId,
  migrateLegacySavepointsInPlace,
  persistSavepointWithReason,
  type Savepoint,
  type StorageLike,
  type SavepointRejectReason,
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
import { attemptWipe, captureOnUnload, captureBeforeWipe, PREWIPE_ARCHIVE_KEY } from './captureBeforeWipe';
// FEAT-2326609778 (Aaron Q100121, 2026-09-04): the async IndexedDB durable
// save layer. localStorage stays the synchronous boot-time fast path
// (unchanged above); this mirrors WRITE results into IndexedDB, best-effort,
// so a save survives beyond localStorage's 5MB quota. See saveStore.ts's
// header for the full architecture + trade-off writeup.
import {
  getDefaultSaveStore,
  migrateFromLocalStorage,
  mirrorKeyFromLocalStorage,
  mirrorSaveCheckpoint,
  mirrorSavepointDirect as mirrorSavepointDirectToStore,
  SAVEPOINT_OVERFLOW_KEY,
} from './saveStore';
import { encode, decode } from './saveCodec';
// FEAT-webworker-sim-offload Stage 1 / Landing 2 (2026-09-02): tick-only
// worker offload — flag, protocol types, and the queue-depth groundwork
// (FEAT-2326609734). See simWorkerProtocol.ts's header for the full design
// and its documented Landing-2-vs-Landing-3 scope tradeoff.
import { webWorkerOffloadEnabled } from './webWorkerFlag';
import type { MainToWorkerMessage, WorkerToMainMessage } from './simWorkerProtocol';
import { getGlobalWorkerQueueTracker } from './workerQueueDepth';
// BUG-618: the engine-lag gauge tracker (always-active, no DEV/flag gating —
// see engineLag.ts's header for why perfhud.ts's DEV-only tick tracker was
// the wrong reuse target). Fed from the two tick-driver instrumentation
// points below (interval fire = scheduled, applied tick = completed/duration).
import { engineLagTracker } from './engineLag';
import {
  initialOffloadControllerState,
  beginTickRequest,
  invalidateInFlight,
  decideTickReply,
  shouldForceSyncTick,
  afterForcedSyncTick,
  clearWorkerBusy,
  deriveHandshakeTimeoutMs,
  type OffloadControllerState,
} from './simWorkerOffloadController';
// FEAT-2326609771 (2026-09-04, default-ON rollout hardening): the fallback-
// reason tracker QueueDepthHud.tsx reads to tell "worker live" apart from
// "worker failed this session, now running sync" — see the module's header.
import { getGlobalWorkerFallbackTracker } from './webWorkerFallbackStatus';
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
  NAMED_SAVE_SLOT_PREFIX,
  NAMED_SAVES_INDEX_KEY,
  CURRENT_CITY_NAME_KEY,
  type NamedSaveCollision,
} from './namedsaves';
import { versionRaw, versionBadgeLabel } from './version';
import { currentMapUi, type MapViewState } from './uistate';
import { persistStashedCamera } from './cameraStash';
import { listRecentOpened, recordRecentOpened } from './recentfiles';
import { RebuildPrompt, type RebuildPhase } from '../components/RebuildPrompt';
import { recordError, updateLastKnownState } from './backend';
import { useBlockingOverlay } from '../components/overlayManager';
import { BLOCKING_OVERLAY_ID, BLOCKING_OVERLAY_RANK } from '../components/overlayLayers';

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

function triggerJsonDownload(filename: string, text: string, mime: string = 'application/json'): void {
  const blob = new Blob([text], { type: mime });
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

/**
 * FEAT-2326609778/Q100131 Export/Import City: like `pickOpenFile`, but
 * without the `.json`-only file-type filter — an exported city is
 * LZ-compressed (saveCodec.ts's `LZv1:` prefix), not valid JSON text, so a
 * strict `.json`/`application/json` picker would reject it in engines that
 * enforce the `accept` filter. `decode()` on the read side tolerates a plain
 * (uncompressed) JSON save too, so importing either an Export or an ordinary
 * File→Save output both work through this one picker.
 */
function pickAnyFile(): Promise<string | null> {
  const w = window as Window & {
    showOpenFilePicker?: (opts: {
      types: { description: string; accept: Record<string, string[]> }[];
      multiple?: boolean;
    }) => Promise<Array<{ getFile: () => Promise<File> }>>;
  };
  if (typeof w.showOpenFilePicker === 'function') {
    return w
      .showOpenFilePicker({
        types: [{ description: 'Metropolis city', accept: { 'application/octet-stream': ['.mcity', '.json'] } }],
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
    input.accept = '.mcity,.json,application/json,application/octet-stream';
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

// ---------------------------------------------------------------------------
// FEAT-2326609778: async IndexedDB mirror helpers. Every call here is
// fire-and-forget from the caller's perspective (`void mirrorXxx(...)`) —
// these NEVER throw and NEVER block the synchronous localStorage write that
// remains authoritative. See saveStore.ts's header for the full writeup.
// ---------------------------------------------------------------------------

/** Mirror the rotating savepoint slots + the full journal, crash-consistency ordered. */
function mirrorSavepointCheckpoint(lineageId?: string): void {
  try {
    void mirrorSaveCheckpoint(getDefaultSaveStore(), window.localStorage, {
      savepointSlots: SAVEPOINT_CAP,
      journalKey: JOURNAL_KEY,
      lineageId,
    });
  } catch {
    /* best-effort — localStorage's own write already succeeded/failed on its own terms */
  }
}

/**
 * FEAT-2326609780 inc2: mirror a savepoint whose localStorage write FAILED
 * straight into the durable store's overflow slot — bypasses the
 * "read-from-localStorage" step every other mirror helper uses, because on a
 * failed write localStorage still holds the OLD savepoint and mirroring from
 * it would be a no-op. `encodedSavepoint` is typically plain
 * `JSON.stringify(savepoint)` (no need to spend the compression cost twice —
 * IndexedDB is not the 5MB-constrained medium); `decode()` at read time
 * treats an uncompressed payload as a no-op passthrough.
 */
function mirrorSavepointDirect(encodedSavepoint: string, lineageId?: string): void {
  try {
    void mirrorSavepointDirectToStore(getDefaultSaveStore(), encodedSavepoint, lineageId);
  } catch {
    /* best-effort */
  }
}

/**
 * FEAT-2326609780 inc2: the single call-site pattern every `persistSavepoint`
 * caller in this file now uses, so the durable IndexedDB mirror ALWAYS
 * advances regardless of whether the synchronous localStorage write
 * succeeded — this is what breaks the quota-wedge cycle (BUG-617-class):
 * once localStorage cannot accept a savepoint, this is the ONLY remaining
 * path that keeps a fresher-than-localStorage copy reachable for the next
 * boot (see the boot-time IDB-freshness effect below).
 *   - success: mirror the bytes localStorage actually holds now (unchanged
 *     inc1 behaviour — savepoint-slots-before-journal crash-consistency
 *     ordering preserved).
 *   - failure: mirror the savepoint that COULD NOT be written, directly,
 *     bypassing localStorage entirely.
 */
function mirrorAfterPersist(persisted: boolean, savepoint: Savepoint): void {
  // P0 RCA fix, item 2: `savepoint.lineageId` (stamped automatically by
  // createSavepoint from the state that produced it) threads straight
  // through to the IDB mirror keys, so two lineages' durable copies can
  // never collide either.
  if (persisted) {
    mirrorSavepointCheckpoint(savepoint.lineageId);
  } else {
    mirrorSavepointDirect(JSON.stringify(savepoint), savepoint.lineageId);
  }
}

/** The shape every savepoint-freshness comparison in this module operates on. */
export interface SavepointFreshnessMeta {
  snapshotTick: number;
  savedAt: string;
  saveSeq?: number;
  /** P0 RCA fix, item 5: the IDB-freshness swap must never adopt a candidate from a DIFFERENT lineage — see `isStrictlyFresherSavepointMeta`'s own comment. */
  lineageId?: string;
}

/**
 * FEAT-2326609780 ROUND 3 (the structural fix, adjudicated — round 2's
 * ATTACK G and R2-F1 both trace to the SAME root cause): is `candidate`
 * STRICTLY fresher than `booted`?
 *
 * PRIMARY ORDER: `saveSeq`, the monotonic per-lineage persist counter (see
 * `Savepoint.saveSeq`'s own doc comment, replay.ts). Round 2 REJECTED the
 * previous tick+savedAt-primary design on two independently-proven grounds:
 *   - ATTACK G: building count is not monotonic (bulldoze, forced asset
 *     sales, the default-on consolidator's scrap-and-rebuild passes all
 *     legitimately SHRINK a city that is still the newer one) — round 2's
 *     OWN safety net (since REMOVED) refused a genuine rescue forever
 *     because it was smaller.
 *   - R2-F1: `snapshotTick` sits FLAT while the player plays without a tick
 *     advancing, so two savepoints minutes apart can tie on tick — and the
 *     `savedAt` tie-break then means "whichever was stamped most recently
 *     in WALL-CLOCK time wins", which is exactly backwards when a
 *     `finish()` self-heal stamps `new Date()` onto a STALE, smaller
 *     lineage a few milliseconds after a genuinely fresher rescue was
 *     already sitting in IndexedDB with an earlier timestamp.
 * `saveSeq` sits flat for neither: it counts PERSIST EVENTS, not ticks,
 * buildings, or wall-clock time, so it strictly rises across every one of
 * the shapes above (see store.tsx's `nextSaveSeq()` — every
 * autosave/save/self-heal/load/rebuild bumps it once, success or failure).
 *
 * FALLBACK (documented, not silent): a `saveSeq` of `undefined` (a
 * pre-round-3 savepoint) is treated as `0` on BOTH sides. When that leaves
 * the two sides TIED (both absent, or an honest equal count), this falls
 * back to the pre-round-3 `snapshotTick`-then-`savedAt` rule — now only ever
 * a tie-break, never primary. Strict `>` throughout (not `persistSavepoint`'s
 * `>=` — see that function's own BUG-469 rule): equal-on-every-field must
 * NOT count as "fresher" here, because the caller's question is "should I
 * hydrate a SECOND time", and hydrating onto byte-identical metadata would
 * be a pointless (and potentially non-idempotent) re-hydrate for zero
 * benefit — a deliberate divergence from `persistSavepoint`, not a bug to
 * reconcile toward it.
 *
 * `null` (no savepoint) is the oldest possible value on either side: a
 * well-formed candidate beats a null `booted` (nothing was loaded — e.g. a
 * fresh dev-city boot), and a null `candidate` never beats anything.
 *
 * Hostile/corrupt metadata (F5/ATTACK D — a hand-edited or pre-inc1 record
 * missing `savedAt`, or a `NaN` tick/seq from a truncated/corrupted mirror
 * write) is handled by ordinary JS comparison semantics: every relational
 * comparison against `NaN` or `undefined` is `false`, so a corrupt value
 * NEVER wins a comparison it takes part in — safe by the shape of the
 * arithmetic itself, not because this function validates its inputs (see
 * `freshestSavepoint` below, which DOES validate, because `NaN`/`undefined`
 * are also never LESS than anything — a corrupt `booted` baseline would
 * otherwise silently let inequality checks alone be gamed).
 */
/** Normalize a lineage id for comparison — `undefined` and the reserved legacy id are the SAME lineage (see `LEGACY_LINEAGE_ID`'s own doc comment). */
function normalizeLineageId(lineageId: string | undefined): string {
  return lineageId && lineageId !== LEGACY_LINEAGE_ID ? lineageId : LEGACY_LINEAGE_ID;
}

export function isStrictlyFresherSavepointMeta(candidate: SavepointFreshnessMeta | null, booted: SavepointFreshnessMeta | null): boolean {
  if (!candidate) return false;
  if (!booted) return true;
  // P0 RCA fix, item 5: the IDB-freshness swap must NEVER adopt a candidate
  // from a DIFFERENT lineage than what booted — that is exactly Aaron's
  // resurrected-old-city mechanism, just relocated to the IndexedDB mirror
  // instead of localStorage's own rotation slots. A foreign-lineage
  // candidate is never a competitor, full stop, regardless of what its
  // tick/savedAt/saveSeq claim.
  if (normalizeLineageId(candidate.lineageId) !== normalizeLineageId(booted.lineageId)) return false;
  const candidateSeqDefined = Number.isFinite(candidate.saveSeq);
  const bootedSeqDefined = Number.isFinite(booted.saveSeq);
  // FEAT-2326609780 round 3 self-fix (caught by re-running THIS ESTATE'S
  // own tests before reporting, and independently by ATTACK A2/E/G on the
  // first round-3 attempt): saveSeq is primary ONLY when BOTH sides carry a
  // real one. Treating an ABSENT saveSeq as a literal `0` (rather than
  // "unknown, fall back") made every pre-round-3 / seq-less savepoint lose
  // UNCONDITIONALLY to any post-round-3 self-heal, because a self-heal
  // ALWAYS stamps a real, positive `saveSeq` via `nextSaveSeq()` — even the
  // routine, always-happens self-heal on a perfectly ordinary boot. That
  // silently defeated the whole structural fix: a genuinely fresher durable
  // rescue with no `saveSeq` (or one whose OWN saveSeq the test/caller never
  // set) always lost to whatever the local self-heal had just stamped,
  // regardless of which one actually represented more history. Comparing by
  // saveSeq is only meaningful when both numbers describe the SAME counting
  // scheme — an absent value on either side means "unknown", not "zero",
  // and the comparison falls back to tick+savedAt instead.
  if (candidateSeqDefined && bootedSeqDefined) {
    const candidateSeq = candidate.saveSeq as number;
    const bootedSeq = booted.saveSeq as number;
    if (candidateSeq !== bootedSeq) return candidateSeq > bootedSeq;
    // Tied on a REAL saveSeq — fall through to the tick+savedAt tie-break below.
  }
  // Either side lacks a saveSeq, or both have the SAME one (a genuine tie):
  // fall back to the pre-round-3 tick+savedAt rule — tie-break only now,
  // never primary, but the only honest comparison available when the
  // primary counting scheme is not comparable on both sides.
  return candidate.snapshotTick > booted.snapshotTick || (candidate.snapshotTick === booted.snapshotTick && candidate.savedAt > booted.savedAt);
}

/**
 * True for a well-formed `{snapshotTick, savedAt}` pair — finite tick,
 * non-empty string savedAt. `saveSeq` is validated separately at the point
 * of use (`isStrictlyFresherSavepointMeta` already treats a non-finite
 * `saveSeq` as absent/0 safely) since it is optional even on a well-formed
 * savepoint. Deliberately NOT a type predicate (`sp is ...`): `Savepoint`'s
 * own declared type already claims `snapshotTick`/`savedAt` are always
 * `number`/`string`, so a predicate narrowing against that same shape makes
 * TypeScript treat the "invalid" branch as unreachable (`never`) at compile
 * time — exactly backwards for a runtime check whose entire purpose is
 * distrusting a value that TRUE JS at runtime does not actually guarantee
 * matches its declared type (a corrupted/hand-edited IndexedDB blob).
 */
function isValidSavepointMeta(sp: Savepoint): boolean {
  const tick: unknown = sp.snapshotTick;
  const savedAt: unknown = sp.savedAt;
  return typeof tick === 'number' && Number.isFinite(tick) && typeof savedAt === 'string' && savedAt.length > 0;
}

/**
 * FEAT-2326609780 inc2: pick the freshest of a list of possibly-null
 * candidate savepoints (by the same saveSeq-primary rule as
 * `isStrictlyFresherSavepointMeta`). Used to reduce the durable store's
 * several possible sources (the mirrored rotation slots PLUS the
 * failure-path overflow slot) down to a single "best IndexedDB has to
 * offer" candidate before comparing it against what booted from localStorage.
 *
 * F5 FIX (independent round REJECT, ATTACK D): the original version seeded
 * `best` with the FIRST non-null candidate UNCONDITIONALLY, before any
 * validation. A candidate with a corrupt (`NaN`) `snapshotTick` then won
 * that unconditional seed, and EVERY subsequent well-formed candidate lost
 * to it — `wellFormed.tick > NaN` and `wellFormed.tick === NaN` are both
 * `false`, so `isStrictlyFresherSavepointMeta` never replaces a corrupt
 * `best` with a good one. A single corrupted IndexedDB slot silently
 * poisoned the whole comparison. Fix: validate every candidate BEFORE it is
 * ever allowed to become `best` (GR#1/#17: report the corrupt slot loudly —
 * a player should not have their durable rescue copy silently ignored
 * without a trace).
 */
export function freshestSavepoint(candidates: Array<Savepoint | null>): Savepoint | null {
  let best: Savepoint | null = null;
  for (const sp of candidates) {
    if (!sp) continue;
    if (!isValidSavepointMeta(sp)) {
      recordError(
        `A durable IndexedDB savepoint slot was ignored during the boot-freshness check: corrupt metadata (snapshotTick=${String(sp.snapshotTick)}, savedAt=${String(sp.savedAt)})`,
        { type: 'app', action: 'load' },
      );
      continue;
    }
    if (!best || isStrictlyFresherSavepointMeta({ snapshotTick: sp.snapshotTick, savedAt: sp.savedAt, saveSeq: sp.saveSeq }, { snapshotTick: best.snapshotTick, savedAt: best.savedAt, saveSeq: best.saveSeq })) {
      best = sp;
    }
  }
  return best;
}

/**
 * FEAT-2326609780 inc2: decode a raw IndexedDB value (either
 * `saveCodec.encode()`-compressed, mirrored verbatim from a successful
 * localStorage write, or plain JSON, written directly by
 * `mirrorSavepointDirect` on a failed one — `decode()` is a no-op passthrough
 * on uncompressed input either way) back into a `Savepoint`. Fail-safe: any
 * parse error degrades to `null`, matching every other savepoint reader in
 * this codebase (a corrupt IDB entry must never crash the boot-freshness
 * check — localStorage's own copy is always the safe fallback).
 */
function decodeSavepointRaw(raw: string | null): Savepoint | null {
  if (raw === null) return null;
  try {
    return JSON.parse(decode(raw)) as Savepoint;
  } catch {
    return null;
  }
}

/**
 * FEAT-2326609780 inc2: build a minimal in-memory `StorageLike` exposing a
 * single savepoint's raw encoded bytes at slot 0 — just enough for
 * `prepareRestoreForChunkedTail` (which only ever reads
 * `metropolis.savepoint.0..SAVEPOINT_CAP-1`) to parse it via the EXACT SAME
 * code path a localStorage-sourced boot uses. This is what "reuses the
 * existing chunked-tail machinery" means for an IndexedDB-sourced savepoint:
 * no bespoke restore logic, just the real function fed a different backing
 * store for one read.
 */
function singleSavepointStorage(rawSlot0: string): StorageLike {
  return {
    getItem: (key: string) => (key === `${SAVEPOINT_KEY_PREFIX}.0` ? rawSlot0 : null),
    setItem: () => {
      /* never written through this shim */
    },
    removeItem: () => {
      /* never written through this shim */
    },
  };
}

/** Mirror one named-save slot plus its index + current-city-name keys. */
function mirrorNamedSave(slug: string): void {
  try {
    const store = getDefaultSaveStore();
    void mirrorKeyFromLocalStorage(store, window.localStorage, `${NAMED_SAVE_SLOT_PREFIX}${slug}`);
    void mirrorKeyFromLocalStorage(store, window.localStorage, NAMED_SAVES_INDEX_KEY);
    void mirrorKeyFromLocalStorage(store, window.localStorage, CURRENT_CITY_NAME_KEY);
  } catch {
    /* best-effort */
  }
}

/** Mirror the GR#27 pre-wipe archive after a successful synchronous capture. */
function mirrorPreWipeArchive(): void {
  try {
    void mirrorKeyFromLocalStorage(getDefaultSaveStore(), window.localStorage, PREWIPE_ARCHIVE_KEY);
  } catch {
    /* best-effort */
  }
}

/**
 * One-time (per browser profile) copy of every existing localStorage save
 * into the durable IndexedDB layer. Called once from a mount effect — never
 * blocks first paint (boot's synchronous localStorage restore already ran in
 * the lazy useState initializer above this component). Never rejects: a
 * migration failure degrades to "this browser keeps using localStorage as
 * its effective durable store, same as before this feature existed" — never
 * a regression, per the layer's own fail-safe contract.
 *
 * FEAT-2326609780 round 2 (F4): returns the migration's own Promise (instead
 * of firing-and-forgetting it) so the boot-time IDB-freshness effect can
 * `await` it before reading IndexedDB — otherwise the freshness read and this
 * migration's mirror writes race with no ordering guarantee, and the
 * attacker's ATTACK E proved that race lets a stale localStorage copy
 * clobber a fresher IndexedDB rotation slot mid-mount.
 */
function runOneTimeSaveMigrationAsync(): Promise<void> {
  try {
    return migrateFromLocalStorage(getDefaultSaveStore(), window.localStorage).then(
      () => undefined,
      () => undefined, // never rejects — matches every other saveStore.ts helper's contract
    );
  } catch {
    return Promise.resolve();
  }
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
    // P0 RCA fix (Aaron, 2026-09-04, item 5 — MIGRATION): stamp any legacy
    // (pre-lineage) savepoint in place (idempotent, cheap — mirrors
    // restampSavepointsBuildVersion's own precedent of running inline in
    // this initializer) and resolve which lineage boot should restore.
    // `readCurrentLineageId` defaults to LEGACY_LINEAGE_ID when the pointer
    // was never written — an EXISTING player's next boot restores from
    // exactly the same (unnamespaced) keys it always has, zero regression.
    migrateLegacySavepointsInPlace(window.localStorage);
    const currentLineageId = readCurrentLineageId(window.localStorage);

    // inc2 (brief §4.3-4.4): if a persisted save was produced under a DIFFERENT
    // build, do NOT silently snapshot-restore — flag a pending rebuild decision so
    // the player is offered Rebuild / Keep / Fresh. We still restore the OLD
    // snapshot underneath so the app is usable behind the prompt; the choice then
    // either replays on the new engine, keeps this, or starts fresh.
    const most = mostRecentSavepoint(readAllSavepoints(window.localStorage, new Date(), currentLineageId));
    const running = currentBuildVersion();
    const crossBuild = !!most && needsRebuild(most.buildVersion, running);

    // BUG-617 (P1, 2026-09-03): a savepoint whose journal tail has grown past
    // LARGE_TAIL_REPLAY_THRESHOLD is the silent-autosave-failure shape (an
    // over-quota autosave — e.g. a BUG-607-class oversized debugQueue — keeps
    // REJECTING every persist, so lastSaveIndex never advances and the tail
    // balloons to thousands of actions needed to grow a stale small snapshot
    // back to the real city). Replaying that tail synchronously HERE (inside
    // this lazy initializer, which MUST return before the very first render)
    // is exactly what wedged the tab for 20+ minutes. Skip the tail replay
    // NOW — boot the fast PRE-TAIL state instantly — and let the effect below
    // (mirroring onRebuild's own chunked-generator pattern) replay it CHUNKED,
    // behind a progress overlay, after the app has already mounted.
    //
    // F2 FIX (independent round REJECT, 2026-09-03, "the shipping paradox"):
    // this branch used to be gated `!crossBuild`, on the theory that the
    // cross-build path "already has its own chunked flow". It does NOT — the
    // cross-build path fell straight through to the plain synchronous
    // `restoreFromSavepoint` below, which is exactly the unchunked,
    // first-render-blocking loop this bug exists to eliminate. Worse: EVERY
    // savepoint in existence at the instant this fix ships is cross-build
    // (its buildVersion is the previous bundle's), so Aaron's wedged city
    // would have taken the `!crossBuild` branch on NO boot until something
    // re-stamped the savepoint — the rescue would never have fired on the
    // boot that actually needed it. Fix: apply the SAME instant pre-tail
    // boot regardless of crossBuild; if it WAS cross-build, remember that
    // (`crossBuildAfter`) so the effect below offers the Rebuild-from-genesis
    // prompt AFTER the chunked tail replay lands the old-engine state — never
    // instead of the instant boot.
    //
    // BUG-669 (P1, 2026-09-04): the ACTION-COUNT threshold above ignored the
    // OTHER factor in tail-replay cost — building count. Aaron's real
    // 49,174-building save had a perfectly healthy 106-action tail (well
    // under the old cut line of 150), so this branch was skipped and the
    // ~31-SECOND inline replay below ran synchronously in front of first
    // paint anyway — the exact wedge this bug exists to eliminate, just
    // reached through the OTHER branch. A cost-aware cut line (tail actions
    // times building count, or a wall-clock probe of the first few actions)
    // was considered and rejected: it adds a second tunable nobody can derive
    // honestly (BUG-669's own note flags this), and it is UNNECESSARY —
    // measured directly (scratch harness, this fix): `prepareRestoreForChunkedTail`
    // (the only part of the chunked path that still runs BEFORE first paint)
    // costs 7.8ms on a genesis-sized city, 112.7ms at ~6,700 buildings, and
    // 864ms at the real 49,174-building/106-action save — i.e. the chunked
    // path's boot-blocking cost is STRICTLY LOWER than the small-tail path's
    // full synchronous replay at every size measured (57.3ms / 401.0ms /
    // 38,851.5ms respectively for the SAME three fixtures on the old inline
    // path), because `prepare` never runs the tail loop at all — the loop
    // moves into the chunked, yielding, post-mount effect regardless of how
    // short it is. So per GR#3 (one path beats two, when one is strictly
    // better): the threshold is DELETED as a live gate. EVERY boot with an
    // existing savepoint takes the SAME instant-pre-tail-boot + chunked-
    // replay path now, whether the tail is 0 actions (the overwhelmingly
    // common case — the chunked generator returns done on its first `next()`
    // call, and React batches the resulting no-op state churn into the SAME
    // paint as the mount, so there is nothing to see) or 100,000.
    // `LARGE_TAIL_REPLAY_THRESHOLD` itself is kept (unused here) purely as a
    // documented "this many actions is representative of a large tail" sizing
    // constant for the bug617/scale-gate test suites, which exercise
    // `prepareRestoreForChunkedTail`/`replayTailChunked` directly and have no
    // dependency on this branch.
    if (most) {
      const prepared = prepareRestoreForChunkedTail(window.localStorage, currentLineageId);
      if (prepared.success && prepared.state && prepared.tail) {
        const loadedJournal = loadJournal(window.localStorage);
        return {
          state: sanitizeTreasury(prepared.state),
          journal: loadedJournal,
          saveIndex: loadedJournal.entries.length,
          pendingRebuild: null,
          pendingTailReplay: {
            tail: prepared.tail,
            camera: prepared.camera ?? null,
            crossBuildAfter: crossBuild ? { savedVersion: most.buildVersion ?? null, currentVersion: running } : null,
            needsJobsGrandfatherCatchUp: prepared.needsJobsGrandfatherCatchUp ?? false,
          },
          // FEAT-2326609780 inc2: the identity of the savepoint that just
          // booted (tick + timestamp only — never the heavy snapshot itself,
          // which would otherwise be retained for the app's whole lifetime
          // via this `[boot]` useState value). Compared against whatever the
          // IDB-freshness effect below finds; a strictly fresher IDB
          // savepoint triggers a post-mount hydrate through the same chunked
          // machinery this branch itself uses.
          bootSavepointMeta: { snapshotTick: most.snapshotTick, savedAt: most.savedAt, saveSeq: most.saveSeq, lineageId: currentLineageId },
        };
      }
      // prepare failed — fall through to the normal path below, which will
      // independently fail the same way and degrade to a fresh/dev-city boot.
    }

    const restoreResult = restoreFromSavepoint(window.localStorage, currentLineageId);
    if (restoreResult.success && restoreResult.state) {
      const loadedJournal = loadJournal(window.localStorage);
      return {
        state: sanitizeTreasury(restoreResult.state),
        journal: loadedJournal,
        saveIndex: loadedJournal.entries.length,
        pendingRebuild: crossBuild
          ? { savedVersion: most?.buildVersion ?? null, currentVersion: running, camera: most?.camera ?? null, kind: 'rebuild' as StandbyKind }
          : null,
        pendingTailReplay: null,
        bootSavepointMeta: most ? { snapshotTick: most.snapshotTick, savedAt: most.savedAt, saveSeq: most.saveSeq, lineageId: currentLineageId } : null,
      };
    }
    // P0 RCA fix, item 1: no savepoint exists for the current lineage at
    // all — a genuinely fresh boot (first ever, or the pointer was reset).
    // Mint a NEW lineage id and make it current immediately, so this
    // session's very first autosave already lands in its own namespaced
    // slots rather than the shared legacy ones (reserved for pre-fix data).
    const fresh =
      typeof process !== 'undefined' && process.env.NODE_TEST_CONTEXT
        ? initialState()
        : loadDevCity1();
    const freshLineageId = fresh.lineageId ?? mintLineageId();
    fresh.lineageId = freshLineageId;
    writeCurrentLineageId(window.localStorage, freshLineageId);
    return {
      state: sanitizeTreasury(fresh),
      journal: emptyJournal(),
      saveIndex: 0,
      pendingRebuild: null,
      pendingTailReplay: null,
      bootSavepointMeta: null,
    };
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
  // FEAT-2326609778: one-time (per browser profile) copy of every existing
  // localStorage save into the durable IndexedDB layer. Runs in an effect
  // (AFTER first paint) so it never touches the synchronous boot-time restore
  // above — the instant-boot contract (BUG-617) is unaffected.
  //
  // FEAT-2326609780 round 2 (F4): the migration's own Promise is captured
  // here (this effect is declared, and therefore fires, BEFORE the
  // IDB-freshness effect below) so that effect can deterministically `await`
  // migration's completion before reading IndexedDB — closing the race
  // ATTACK E proved (a stale localStorage copy mirrored/migrated over a
  // fresher IndexedDB rotation slot AFTER the freshness effect had already
  // read it, or vice versa, with no ordering guarantee either way).
  const migrationPromiseRef = useRef<Promise<void>>(Promise.resolve());
  useEffect(() => {
    migrationPromiseRef.current = runOneTimeSaveMigrationAsync();
  }, []);
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
  // BUG-617: a large savepoint-tail replay deferred out of the boot
  // initializer (see boot's own BUG-617 comment) — non-null means the effect
  // further below (after dispatch/stateRefForDispatch exist) still needs to
  // chunk-replay `tail` onto the already-mounted pre-tail state. `camera` is
  // carried through so the self-healing fresh savepoint this produces keeps
  // the player's view exactly like every other savepoint-writing path does.
  // F2: `crossBuildAfter` is non-null when the ORIGINAL savepoint (before
  // this tail replay) was itself cross-build — the effect defers offering
  // the Rebuild-from-genesis prompt until AFTER the chunked tail replay
  // lands the old-engine state, rather than skipping the instant boot
  // entirely (the "shipping paradox" REJECT finding).
  const [pendingTailReplay, setPendingTailReplay] = useState<{
    tail: JournalEntry[];
    camera: MapViewState | null;
    crossBuildAfter: { savedVersion: string | null; currentVersion: string } | null;
    /** BUG-652 GRANDFATHERING, ROUND r3 FIX (F4) — threaded straight from
     *  prepareRestoreForChunkedTail() through to replayTailChunked() so a
     *  tail-created instance of one of the six BUG-652 specs is grandfathered
     *  too, exactly like the synchronous restoreFromSavepoint() path. */
    needsJobsGrandfatherCatchUp: boolean;
    /** FEAT-2326609780 inc2: normally undefined, and the effect below replays
     *  `tail` onto `stateRefForDispatch.current` (the already-mounted state —
     *  correct for boot's own use of this mechanism, where the mounted state
     *  IS the tail's own pre-replay base). The post-mount IDB-freshness swap
     *  (below) is different: the currently-mounted state is the STALE
     *  localStorage-sourced city, not the IDB savepoint's own base, so that
     *  path supplies its base explicitly here instead. */
    swapBaseState?: SimState;
    /** FEAT-2326609780 round 2 (F2): true ONLY for the post-mount
     *  IDB-freshness swap (never the boot-time large-tail replay of the
     *  SAME lineage). ATTACK B proved the swap replaced STATE from the
     *  IndexedDB lineage while leaving the persisted journal + lastSaveIndex
     *  as localStorage's — so a later Rebuild-from-Genesis / hard-reset-
     *  replay (GR#27/FEAT-1972079897) silently reverted the player, because
     *  replaying the (unchanged, wrong-lineage) journal never reproduces the
     *  swapped-in city. When true, `finish()` below re-bases the journal
     *  (and `lastSaveIndex`) to the swapped lineage ATOMICALLY with the
     *  hydrate, mirroring how `applyLoadedSave` re-bases `journal` from
     *  `save.journal` for a named-save Load. There is no full companion
     *  journal for an anonymous savepoint/overflow-slot lineage (unlike a
     *  named GameSave), so the re-based journal is a SINGLE synthetic
     *  `{type:'hydrate', state: reconciledState}` entry — engine.ts's
     *  'hydrate' reducer case ignores its incoming `state` parameter
     *  entirely (it substitutes `action.state` wholesale), so this is not a
     *  hack: replaying that one entry from ANY starting state reproduces the
     *  swapped-in city exactly, which is the only truthful thing that can be
     *  claimed about a lineage whose pre-snapshot history was never
     *  recorded locally. */
    isIdbSwap?: boolean;
  } | null>(boot.pendingTailReplay);
  // FEAT-2326609720 inc1: registers RebuildPrompt with the app-wide blocking-
  // overlay resolver (overlayManager.tsx) at BLOCKING_OVERLAY_ID.REBUILD_PROMPT
  // priority — the highest of the four known candidates, since the boot-time
  // engine-version decision must win over the decline/insolvency/forced-sale
  // overlays rendered deep inside MapView (a different subtree; the resolver
  // is the only thing spanning both). In practice this is always true when
  // rebuildDecision is set (nothing outranks it), but going through the
  // resolver — rather than relying solely on a higher CSS z-index — means the
  // LOWER-priority overlays are actually unmounted, not just visually
  // covered, satisfying "at most one blocking overlay is ever mounted".
  const isRebuildPromptTop = useBlockingOverlay(
    BLOCKING_OVERLAY_ID.REBUILD_PROMPT,
    BLOCKING_OVERLAY_RANK.rebuildPrompt,
    rebuildDecision != null,
  );

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

  // BUG-669 (P1, 2026-09-04, independent round REJECT): the chunked
  // savepoint-tail replay effect below (`pendingTailReplay`) ends with
  // `dispatch({type:'hydrate', state: finalState})` — a full state
  // REPLACEMENT computed from a snapshot taken when the effect started, with
  // no knowledge of anything dispatched to the live `state` in between.
  // `guardedDispatch` (the ONLY dispatch exposed via useSim()) applied a
  // mid-replay action immediately, so the player saw it land — then watched
  // it silently vanish the instant hydrate landed. Two refs (not state —
  // guardedDispatch must see the CURRENT value at dispatch time, not one
  // captured when its useMemo last ran; same reasoning as `stateRefForDispatch`
  // itself, BUG-434) close this:
  //   - `tailReplayActiveRef`: true for the exact lifetime of the tail-replay
  //     effect (set at its start, cleared the instant its own hydrate/finish
  //     lands or it unmounts) — initialised from `boot.pendingTailReplay`
  //     rather than `false` so an action fired in the vanishingly-small
  //     window between the FIRST commit and this effect's first run is still
  //     caught, not lost to a default of "not replaying yet".
  //   - `tailReplayBufferRef`: every non-tick action dispatched while active,
  //     in dispatch order — drained through the reducer ONTO `finalState`
  //     right before the replay's own hydrate lands (see that effect), so the
  //     player's live view ends up describing the SAME thing the journal
  //     already recorded (wrappedDispatch journals unconditionally,
  //     regardless of replay state) instead of diverging from it (GR#21).
  //   Deliberately NOT gated by the shared `rebuildInProgress` module flag
  //   (genesisReplay.ts) — that flag is ALSO true during `onRebuild`'s
  //   genesis-from-journal replay, which never live-hydrates (it persists to
  //   storage and reloads the whole page via `onResume`), so there is no
  //   discard risk there and nothing would ever drain a buffer filled during
  //   it — buffering on that shared flag would silently strand actions
  //   forever. This pair tracks ONLY the specific mechanism that discards.
  const tailReplayActiveRef = useRef<boolean>(boot.pendingTailReplay !== null);
  const tailReplayBufferRef = useRef<Action[]>([]);

  // FEAT-2326609780 inc2: IDB-PRIMARY BOOT — the freshness check.
  //
  // Boot itself (the lazy useState initializer above) stays perfectly
  // synchronous and localStorage-only, exactly as before this increment —
  // instant first paint, zero regression, unchanged tests. This effect asks
  // a single question: does the durable IndexedDB mirror hold a savepoint
  // STRICTLY FRESHER than whatever the app is currently showing? That is
  // exactly the quota-wedge shape (BUG-617-class) this increment exists to
  // close: every localStorage persist rejected (5MB quota on a 49k-building
  // city), so localStorage's rotation is stuck on a stale savepoint while
  // `mirrorAfterPersist`'s failure-path overflow write kept advancing
  // IndexedDB underneath it. If localStorage is caught up (the overwhelming
  // common case — inc1's mirror only ever lags, never leads, in the healthy
  // path), this is a no-op: no second hydrate, no flicker.
  //
  // FEAT-2326609780 ROUND 2 F1 FIX (independent round REJECT, "built but not
  // wired"): BUG-669 made EVERY savepoint boot set `pendingTailReplay`
  // (even with an empty tail, so the chunked machinery is one uniform path —
  // see that effect's own comment), which means `pendingTailReplay !== null`
  // is true on essentially every mount the instant this effect's first
  // commit runs. The round-1 version bailed out permanently the FIRST time
  // it observed that (a one-shot `idbFreshnessCheckedRef`), so the swap this
  // whole increment exists to deliver NEVER fired for the exact 49k-building
  // wedged city it was built for — proven by the round's ATTACK A2. Fix:
  // this effect is now keyed on `[pendingTailReplay, rebuildDecision]` so it
  // deterministically RE-RUNS the instant the boot-time tail replay's own
  // `finish()` lands (pendingTailReplay -> null) or an unresolved rebuild
  // prompt clears — hooking the actual completion path, never a timer or a
  // one-shot race. `idbSwapAttemptedRef` (not the old `idbFreshnessCheckedRef`)
  // guards against re-triggering once THIS effect has itself INITIATED a
  // swap (its own replay finishing would otherwise re-null pendingTailReplay
  // and cause an infinite re-evaluation loop).
  //
  // `bootSavepointMetaRef` (not the static `boot.bootSavepointMeta`) is the
  // comparison baseline — updated by the tail-replay effect's `finish()` the
  // instant ANY replay (boot's own, or a prior swap) lands, so a
  // freshness re-check after a large-tail replay compares against what the
  // player is NOW looking at, not the pre-replay boot snapshot.
  //
  // F1's regression-safety net: a candidate whose OWN tail is empty is
  // claiming to be a COMPLETE, fully-caught-up city — that claim is cheaply
  // checkable (its building count must not be LOWER than the live city's),
  // unlike a non-empty tail (whose eventual building count cannot be known
  // without running it, exactly like the existing boot-time replay already
  // trusts). ATTACK A proved that a numerically-"fresher" (by tick) IndexedDB
  // candidate can legitimately be a SMALLER city than a tail replay that just
  // finished landing the player's real, larger history — this net refuses
  // that swap rather than regressing the player.
  const idbSwapAttemptedRef = useRef(false);
  const bootSavepointMetaRef = useRef<SavepointFreshnessMeta | null>(boot.bootSavepointMeta);
  // FEAT-2326609780 round 3 (ATTACK A vs ATTACK G, both re-run before this
  // report — see the freshness effect's own header for the full reasoning):
  // when a candidate lacks a comparable `saveSeq` and the comparison falls
  // back to tick+savedAt, `saveSeq` alone cannot protect a JUST-VERIFIED
  // real tail replay from being discarded for a smaller, empty-tail
  // candidate whose tick was merely bumped without genuine growth (ATTACK
  // A) — but the SAME fallback must still let a genuinely smaller-but-newer
  // rescue win when NOTHING was just verified locally (ATTACK G: bulldoze/
  // forced-sale/consolidator shrink a city that is still the newer one).
  // The discriminator is NOT building count in the abstract (banned,
  // correctly, by ATTACK G) — it is whether the LOCAL side's most recent
  // replay actually verified anything: a NON-EMPTY tail was chunk-replayed
  // through the real reducer, action by action, and its resulting building
  // count is trustworthy; an EMPTY tail is a bare re-persist that verified
  // nothing beyond "the snapshot itself parses". This ref remembers the
  // building count from the last replay that verified real content (`null`
  // when the most recent replay had an empty tail, e.g. G's/E's local
  // self-heal) — consulted ONLY in the tick+savedAt fallback branch, never
  // when a real saveSeq comparison already decided the outcome.
  const verifiedFloorBuildingsRef = useRef<number | null>(null);
  // FEAT-2326609780 round 3 (the structural fix — adjudicated): the
  // monotonic per-lineage save counter (see Savepoint.saveSeq's own doc
  // comment). Recovered at boot from whatever the LOCAL (synchronous,
  // localStorage-sourced) boot savepoint already carried — `0` if it had
  // none or predates round 3 — and bumped once per PERSIST ATTEMPT
  // (`nextSaveSeq()`, called by every autosave/save/self-heal/load/rebuild
  // site, success or failure, BEFORE the write is attempted) so it is always
  // comparable against whatever the durable IndexedDB copy of the SAME
  // lineage carries, regardless of tick, building count, or wall-clock time.
  const saveSeqRef = useRef<number>(boot.bootSavepointMeta?.saveSeq ?? 0);
  const nextSaveSeq = (): number => {
    saveSeqRef.current += 1;
    return saveSeqRef.current;
  };
  useEffect(() => {
    // Never race a flow that currently owns the swap surface — re-evaluate
    // the moment it releases (this effect's own dependency array ensures
    // that transition re-runs it; no polling, no timer).
    if (tailReplayActiveRef.current) return;
    if (rebuildDecisionRef.current !== null) return;
    if (idbSwapAttemptedRef.current) return;
    let cancelled = false;
    (async () => {
      // F4: deterministic ordering — never read IndexedDB until the
      // one-time migration's own mirror writes have settled (ATTACK E).
      await migrationPromiseRef.current;
      if (cancelled) return;

      // P0 RCA fix, item 2/5: read ONLY the current lineage's own IDB keys
      // (legacy/undefined resolves to the SAME unnamespaced keys every
      // pre-fix mirror write used — this is what the round-1/round-2
      // attacker fixtures, which never stamp a lineageId, still exercise
      // byte-for-byte). A different lineage's mirrored slots simply live at
      // a different key and are never even read here, let alone compared.
      const currentLineageForIdbRead = bootSavepointMetaRef.current?.lineageId;
      const idbSlotKey = (slot: number) =>
        !currentLineageForIdbRead || currentLineageForIdbRead === LEGACY_LINEAGE_ID
          ? `${SAVEPOINT_KEY_PREFIX}.${slot}`
          : `${SAVEPOINT_KEY_PREFIX}.${currentLineageForIdbRead}.${slot}`;
      const idbOverflowKey =
        !currentLineageForIdbRead || currentLineageForIdbRead === LEGACY_LINEAGE_ID
          ? SAVEPOINT_OVERFLOW_KEY
          : `${SAVEPOINT_KEY_PREFIX}.${currentLineageForIdbRead}.idbOnly`;

      const store = getDefaultSaveStore();
      const rawCandidates = await Promise.all([
        store.getItem(idbSlotKey(0)),
        store.getItem(idbSlotKey(1)),
        store.getItem(idbSlotKey(2)),
        store.getItem(idbOverflowKey),
      ]);
      if (cancelled || tailReplayActiveRef.current || rebuildDecisionRef.current !== null || idbSwapAttemptedRef.current) return;

      const best = freshestSavepoint(rawCandidates.map(decodeSavepointRaw));
      if (!best) return; // IndexedDB has nothing valid (fresh browser, degraded store, migration not yet run) — localStorage's boot stands.
      const bestMeta: SavepointFreshnessMeta = { snapshotTick: best.snapshotTick, savedAt: best.savedAt, saveSeq: best.saveSeq, lineageId: best.lineageId };
      if (!isStrictlyFresherSavepointMeta(bestMeta, bootSavepointMetaRef.current)) {
        return; // localStorage's boot is already current or newer — no-op, no second hydrate.
      }
      // Did a REAL, comparable saveSeq decide this, or did the comparison
      // fall back to tick+savedAt (see isStrictlyFresherSavepointMeta's own
      // doc comment — a fallback happens when either side lacks a saveSeq)?
      // The verified-floor check just below applies ONLY to a fallback
      // decision — a real saveSeq comparison is the authoritative, adjudicated
      // primary signal and is never second-guessed by building count.
      const decidedByRealSeq = Number.isFinite(bestMeta.saveSeq) && Number.isFinite(bootSavepointMetaRef.current?.saveSeq);

      // Reuse prepareRestoreForChunkedTail VERBATIM — the only difference
      // from a localStorage-sourced boot is which bytes slot 0 of the shim
      // storage hands back.
      const rawBest = JSON.stringify(best);
      const prepared = prepareRestoreForChunkedTail(singleSavepointStorage(rawBest));
      if (!prepared.success || !prepared.state || !prepared.tail) return;
      if (cancelled || tailReplayActiveRef.current || rebuildDecisionRef.current !== null || idbSwapAttemptedRef.current) return;

      // FEAT-2326609780 round 3 (ATTACK A vs ATTACK G — see
      // `verifiedFloorBuildingsRef`'s own doc comment above for the full
      // reasoning): a BLANKET building-count safety net was REMOVED
      // (adjudicated) — building count is not a monotonic progress measure
      // (bulldoze/forced-sale/consolidator scrap-and-rebuild all legitimately
      // shrink a city that is still the newer one, ATTACK G). But when the
      // freshness decision above fell back to tick+savedAt (no comparable
      // saveSeq on both sides), an empty-tail candidate claiming to be
      // complete must not be allowed to discard a JUST-VERIFIED real tail
      // replay for something SMALLER (ATTACK A) — tick alone is not
      // trustworthy evidence of genuine progress (it can sit flat OR be
      // bumped without corresponding growth). This check is SCOPED to
      // exactly that gap: it never fires when a real saveSeq comparison
      // already decided the outcome, and never fires when nothing was just
      // verified locally (`verifiedFloorBuildingsRef.current === null`,
      // ATTACK G's/E's shape — an empty-tail self-heal verifies nothing).
      if (!decidedByRealSeq && prepared.tail.length === 0 && verifiedFloorBuildingsRef.current !== null && prepared.state.buildings.length < verifiedFloorBuildingsRef.current) {
        recordError(
          `IndexedDB held a savepoint that compared as "fresher" by tick/savedAt (no comparable saveSeq) but has FEWER buildings ` +
            `(${prepared.state.buildings.length}) than the real tail replay this boot just verified (${verifiedFloorBuildingsRef.current}) - refusing the swap to avoid regressing the player`,
          { type: 'app', action: 'load' },
        );
        return;
      }

      // FEAT-2326609780 round 3 (R2-F1): keep the app's own persist counter
      // consistent with the lineage we are about to adopt, so a LATER
      // persist in THIS session (autosave, self-heal) numbers itself
      // correctly relative to the swapped-in history, not the stale local
      // one this mount originally booted from.
      if (typeof best.saveSeq === 'number' && Number.isFinite(best.saveSeq) && best.saveSeq > saveSeqRef.current) {
        saveSeqRef.current = best.saveSeq;
      }

      const running = currentBuildVersion();
      const crossBuild = needsRebuild(prepared.buildVersion, running);
      idbSwapAttemptedRef.current = true;
      setPendingTailReplay({
        tail: prepared.tail,
        camera: prepared.camera ?? null,
        crossBuildAfter: crossBuild ? { savedVersion: prepared.buildVersion ?? null, currentVersion: running } : null,
        needsJobsGrandfatherCatchUp: prepared.needsJobsGrandfatherCatchUp ?? false,
        swapBaseState: sanitizeTreasury(prepared.state),
        isIdbSwap: true,
      });
    })().catch((e: unknown) => {
      // Fail-safe (GR#1): the freshness check itself never disturbs the
      // already-mounted, already-usable localStorage-sourced city.
      recordError(`IndexedDB boot-freshness check failed: ${e instanceof Error ? e.message : String(e)} - continuing on the localStorage-sourced city`, {
        type: 'app',
        action: 'load',
      });
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberately
    // re-run only when pendingTailReplay/rebuildDecision themselves change
    // (the completion-path hook, F1); every other value read inside is read
    // through a ref specifically so it is never stale without needing to be
    // a dependency (stateRefForDispatch/tailReplayActiveRef/
    // rebuildDecisionRef/idbSwapAttemptedRef/bootSavepointMetaRef/migrationPromiseRef).
  }, [pendingTailReplay, rebuildDecision]);

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
      // B2 fix (independent round REJECT, 2026-09-02): 'reset' wholesale-
      // replaces state (reduceCore's reset case, exempted from the decline
      // freeze alongside 'hydrate' — engine.ts). Any worker tick reply still
      // in flight was computed against the PRE-reset state and must never
      // be allowed to land afterwards — invalidateInFlightWorkerTick tells
      // the offload controller so its requestId no longer matches, and
      // worker.onmessage's decideTickReply drops the eventual reply
      // unconditionally, regardless of what tick number it carries.
      const timedAction = () => {
        if (action.type === 'reset') invalidateInFlightWorkerTick();
        dispatch(action);
      };

      if (action.type === 'tick') {
        // BUG-618: time EVERY main-thread-applied tick (fallback path AND
        // the forced-sync-tick escape — both dispatch {type:'tick'} through
        // here), unconditionally — this is the always-on twin of the
        // DEV-only tickTracker block just below, which stays exactly as it
        // was for PerfHud.tsx's dev overlay.
        const lagStart = performance.now();
        if (tickTracker) {
          const start = performance.now();
          timedAction();
          const duration = performance.now() - start;
          recordTickDuration(tickTracker, duration);
        } else {
          timedAction();
        }
        engineLagTracker.recordTickDuration(performance.now() - lagStart);
        engineLagTracker.recordTickCompleted();
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
          // P0 RCA fix (Aaron, 2026-09-04, item 1): mint a fresh lineage id
          // for the new city HERE (outside the pure reducer — see
          // Action['reset']'s own doc comment on why) and stamp it onto the
          // dispatched action so it is journalled and reproduces identically
          // on replay. The old city's savepoint slots are namespaced under
          // its OWN (different) lineage id and are simply never looked at
          // again — this is the fix for the exact mechanism the RCA proved
          // (a brand-new city's autosave competing with, and losing to, an
          // old city's savepoint occupying the same global slot).
          const freshLineageId = mintLineageId();
          attemptWipe(stateRefForDispatch.current, versionRaw, window.localStorage, () =>
            recordAndDispatch({ ...action, lineageId: freshLineageId }),
          );
          // GR#27's fail-closed gate has ALREADY run above (attemptWipe
          // threw and aborted if the capture failed) — only make the new
          // lineage "current" once the wipe genuinely happened, and give
          // its own persist counter a clean slate (namespacing already
          // makes cross-lineage seq comparison impossible, but starting a
          // brand-new lineage's count at 0 is the honest representation).
          writeCurrentLineageId(window.localStorage, freshLineageId);
          saveSeqRef.current = 0;
          // FEAT-2326609778: mirror the just-written archive entry into the
          // durable layer. GR#27's fail-closed gate has ALREADY run above
          // (attemptWipe threw and aborted if it failed) — this is purely an
          // additional durability copy, never part of the fail-closed decision.
          mirrorPreWipeArchive();
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

  // ---------------------------------------------------------------------
  // FEAT-webworker-sim-offload Stage 1 / Landing 2 (2026-09-02): tick-only
  // Web Worker offload. See simWorkerProtocol.ts's header for the full
  // design and the documented Landing-2-vs-Landing-3 scope tradeoff (full
  // per-tick state snapshot, not a targeted diff — that's Landing 3).
  //
  // AC-8 fallback contract: `webWorkerActiveRef.current` stays false (and
  // every effect below becomes a no-op) whenever the flag is off, `Worker`
  // is unavailable, or construction throws — the tick-driver effect further
  // below then falls straight back to calling wrappedDispatch({type:'tick'})
  // exactly as it did before this feature existed. Same reducer module
  // either way (simWorkerProtocol.ts imports the SAME `reducer` this file
  // imports for the fallback) — GR#21.
  const workerRef = useRef<Worker | null>(null);
  // BUG-618: wall-clock timestamp (performance.now()) of the most recent
  // postMessage to the worker, consumed the instant a reply/error is
  // observed (worker.onmessage below) to compute the worker round-trip
  // duration for the engine-lag gauge. Landing 2's "at most one in flight"
  // design (workerBusy) means a single ref suffices — never more than one
  // request is ever outstanding at a time.
  const workerPostAtRef = useRef<number | null>(null);
  // FEAT-webworker-sim-offload / independent round REJECT follow-up
  // (2026-09-02): ALL request/reply/supersede bookkeeping lives in
  // simWorkerOffloadController.ts's pure, directly-unit-tested state
  // machine (test/simworker-offload.test.mjs covers B2/B3 against it in
  // isolation, no DOM/timers/Worker needed) — this ref just holds ITS
  // current state across renders. store.tsx below is thin glue: call a
  // controller transition, apply its returned state, act on its decision.
  const offloadControllerRef = useRef<OffloadControllerState>(initialOffloadControllerState());
  // FEAT-2326609771 (2026-09-04, default-ON rollout hardening): true from the
  // instant the worker's FIRST EVER reply (of any message type) is observed —
  // i.e. "the handshake is proven, this worker is genuinely alive" — never
  // reset back to false for the life of this worker instance (a LATER
  // runtime crash is `runtime-error`, not `handshake-error`; see
  // worker.onerror below). Read by issueTickRequest to decide whether THIS
  // request still needs a handshake watchdog armed, and by the watchdog
  // itself (belt-and-braces) to no-op if the reply actually won the race.
  const workerHandshakeCompleteRef = useRef(false);
  // The pending handshake-watchdog timer id, or null when none is armed
  // (already handshaked, no request currently in flight, or the worker has
  // already been torn down). At most one is ever armed at a time — Landing
  // 2's "at most one request in flight" design (workerBusy) guarantees
  // issueTickRequest is never called again before this one either resolves
  // (cleared in worker.onmessage) or fires (which itself tears the worker
  // down, so no second request — and therefore no second timer — can follow).
  const workerHandshakeTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  /**
   * B1/B2/"lesser" fix, superseding the FIRST cut's buffer-while-in-flight
   * design entirely (independent round REJECT, 2026-09-02 — see the round's
   * closing note: "reconsider buffering PLACEMENT actions behind a full-
   * state round trip; it delays clicks — the opposite of the goal").
   *
   * OLD design: a non-tick action dispatched while a tick was in flight was
   * BUFFERED (held back, not applied) until the tick's reply landed, then
   * replayed. That reintroduced exactly the "click blocked by sim work" lag
   * this feature exists to remove (a bounded worker-round-trip's worth, not
   * 5 seconds, but still a regression against the stated goal), AND its
   * buffer-draining logic was itself a source of bugs (a buffered 'reset'
   * silently dropped on component teardown = a GR#27 capture-before-wipe
   * no-op; applyLoadedSave's raw hydrate never even routed through the
   * buffer at all, so it wasn't protected by it — the B2 finding).
   *
   * NEW design: a non-tick action is applied to main's CURRENT state
   * IMMEDIATELY, exactly as it would be with the worker offload off — never
   * delayed, never buffered. Instead, this function SUPERSEDES whatever
   * tick request is in flight (via the controller's invalidateInFlight): it
   * is unconditionally discarded when its reply eventually arrives (the
   * controller's requestId can never match again), because that reply was
   * computed from a state this action has now moved past — applying it
   * afterwards would silently revert this action's effect. Per B3, a
   * discarded tick is never journaled and never applied, so nothing
   * inconsistent reaches the journal; the very next tick-driver interval
   * simply issues a FRESH request from the now-current (post-action) state.
   * Net effect under contention: the simulation occasionally reruns/delays
   * ONE tick by a beat, but a placement/bulldoze/etc click is NEVER made to
   * wait on a worker round-trip — the correct tradeoff, since ticks have no
   * wall-clock deadline a player can perceive but click latency is exactly
   * what this whole feature is trying to fix.
   *
   * Determinism/journal-ordering (thought through, per the round's ask):
   * the journal ends up with the non-tick action recorded at the SAME tick
   * number it already carried (`stateRefForDispatch.current.tick`, read by
   * wrappedDispatch's own recordAndDispatch — untouched), followed later by
   * a fresh 'tick' entry once the REISSUED request completes — i.e. exactly
   * the sequence [..., action @ T, tick @ T, ...] that genesis-replay would
   * reproduce if the same action and tick had simply been processed
   * back-to-back on a single thread with the action arriving first. No
   * action is ever lost, double-applied, or journaled out of the order it
   * was actually processed in.
   *
   * Also used before a 'reset' or a loadGame/loadNamed hydrate proceeds —
   * the B2 finding: applyLoadedSave's raw `dispatch({type:'hydrate', ...})`
   * never invalidated the in-flight tracking, so an in-flight tick's reply
   * (a HIGHER, pre-load tick number) passed the old monotonic guard and
   * clobbered a freshly loaded OLDER save — reachable in ordinary play, not
   * a contrived race. Same supersede logic applies: the in-flight request
   * becomes unconditionally moot the instant the state it was computed
   * against is superseded by ANY other change, whether that's one more
   * user action or a full reset/load.
   */
  const invalidateInFlightWorkerTick = () => {
    const before = offloadControllerRef.current;
    offloadControllerRef.current = invalidateInFlight(before);
    if (before.pendingTick) {
      const tracker = getGlobalWorkerQueueTracker();
      // BUG-592 fix: NO tracker.drain() here anymore. Draining at
      // invalidation time made depth() lie ("caught up") while the worker
      // was still actually crunching the superseded computation — see
      // simWorkerOffloadController.ts's `workerBusy` header for the full
      // story. depth() now only drains when the worker is ACTUALLY observed
      // to finish (worker.onmessage/onerror/teardown below, alongside
      // clearWorkerBusy), so a skipped-because-busy interval correctly
      // keeps reading as outstanding backlog, not as caught-up.
      //
      // N1 fix / FEAT-2326609734 AC-7 honesty: report the (now possibly
      // incremented) consecutive-supersede streak so a UI readout can show
      // "behind / catching up" instead of misreading depth()'s post-drain 0
      // as "caught up" — see workerQueueDepth.ts's reportSupersedeStreak.
      tracker.reportSupersedeStreak(offloadControllerRef.current.supersedeStreak);
    }
  };

  // Record a 'tick' journal entry WITHOUT running the local reducer — used
  // only by the worker-offload path below, which hands the actual advance()
  // computation to the worker instead. Mirrors exactly what
  // recordAndDispatch's setJournal call does for every other action; kept as
  // a tiny top-level closure (not memoised) so it can be called from the
  // worker's onmessage handler below without adding it to that effect's
  // dependency array — every reference inside (setJournal, refs) is already
  // stable across renders.
  //
  // B3 fix (independent round REJECT, 2026-09-02): this used to be called at
  // REQUEST time (before postMessage), so a tick request that was later
  // DISCARDED (monotonic-guard rejection, a reset/load invalidating it, a
  // worker error, or teardown mid-flight) still left a 'tick' entry in the
  // journal — a tick genesis-replay would later fold through the reducer
  // even though live play never actually applied that tick's effect to
  // state (GR#21/AC-5: replay must reproduce exactly what happened, no
  // more, no less). It is now called ONLY from worker.onmessage, ONLY on
  // the branch that goes on to actually apply the result via hydrate —
  // never on request, never on a discarded/rejected/errored reply. `tick`
  // is passed explicitly (the PRE-tick number the offload controller
  // captured at request time — decideTickReply's `tickToJournal`) rather
  // than re-read from stateRefForDispatch.current, which may have moved on
  // by apply time.
  const recordTickInJournalOnly = (tick: number) => {
    setJournal((j) => {
      const updated = recordAction(j, tick, { type: 'tick' });
      journalPersisterRef.current?.schedule(updated);
      return updated;
    });
  };

  /**
   * Issue a new tick request to the worker if none is currently in flight.
   * Called ONLY from the tick-driver interval's own scheduled cadence —
   * NOT from worker.onmessage.
   *
   * N2 fix (independent round 3 REJECT, 2026-09-02, superseding the round-2
   * N1 fix): an earlier version of this feature ALSO called this from
   * worker.onmessage on a stale-reply "rebase" (issue the next request
   * immediately rather than waiting for the next interval). That, combined
   * with shouldForceSyncTick's K-consecutive-supersedes escape, formed an
   * INTERVAL-INDEPENDENT tick generator: under continuous sub-round-trip-
   * interval input, issue/invalidate/reissue cycles could complete many
   * times within a single SPEED_MS period, each Kth one forcing a
   * synchronous tick — the round measured ~20x the selected speed at
   * SPEED_MS=1000/16ms round-trip/60Hz drag. Removing the onmessage call
   * site (see worker.onmessage's stale-reply branch below for the full
   * story) restores the invariant that a tick request can ONLY be issued in
   * response to a scheduled interval fire, which bounds the supersede rate
   * — and therefore the forced-tick rate — to at most the interval rate:
   * total tick production can never exceed the selected speed (see
   * test/simworker-offload.test.mjs's N2 ceiling matrix, ratio <= 1.0 in
   * every cell). The K-escape alone still guarantees liveness (the round-2
   * fix direction, confirmed correct by the round): forward progress at
   * least once every K+1 intervals under sustained contention, never a
   * freeze, and — now — never a runaway either.
   */
  /**
   * Returns true when the caller (the tick-driver interval, below) should
   * treat this fire as handled by the worker — either a request was
   * actually posted, or one was already in flight and nothing more needs to
   * happen this tick. Returns false ONLY on a postMessage failure, telling
   * the caller to fall back to the ordinary synchronous tick for THIS
   * interval instead of losing it.
   *
   * BUG-597 hardening (flag-gated path, defence-in-depth — the round-4
   * follow-up round flagged this as a latent stranding, not reachable today
   * since SimState is JSON-serialisable by design so postMessage's
   * DataCloneError class never actually fires, but fails dead-and-silent
   * the moment that stops being true): postMessage used to run with no
   * try/catch AFTER beginTickRequest had already committed workerBusy=true
   * and the tracker had already enqueue()'d — a throw there left both
   * stranded for the rest of the session, since the ONLY caller (the
   * interval below) did `issueTickRequest(); return;` on the worker branch
   * and never fell through to the fallback dispatch. With workerBusy stuck
   * true, beginTickRequest refuses every future request forever — the clock
   * would then only ever advance via the K-supersede forced-sync-tick
   * escape (shouldForceSyncTick), and freeze completely the moment input
   * (which drives supersedes) stops arriving.
   */
  const issueTickRequest = (): boolean => {
    const worker = workerRef.current;
    if (!worker) return false;
    const begun = beginTickRequest(offloadControllerRef.current, stateRefForDispatch.current.tick);
    if (!begun) return true; // already pending — nothing to do, no fallback needed.
    offloadControllerRef.current = begun.state;
    getGlobalWorkerQueueTracker().enqueue();
    const msg: MainToWorkerMessage = {
      type: 'runTick',
      state: stateRefForDispatch.current,
      requestId: begun.requestId,
    };
    try {
      // BUG-618: stamp the post time BEFORE the actual postMessage call so
      // the recorded duration includes the structured-clone cost of handing
      // the ~1.77MB SimState across the thread boundary — that cost is real
      // engine-vs-UI lag, not something to hide from the gauge.
      workerPostAtRef.current = performance.now();
      worker.postMessage(msg);
      // FEAT-2326609771 (default-ON hardening): arm the handshake watchdog
      // ONLY while no reply has EVER landed for this worker instance — once
      // workerHandshakeCompleteRef is true, a stuck/late reply is ordinary
      // backlog (the existing engine-lag/queue-depth signals already cover
      // it), not a "is this worker even alive" question. At most one timer
      // is ever armed at a time (beginTickRequest's workerBusy gate already
      // guarantees at most one request in flight), so there is nothing to
      // clear here before arming — any previous timer was already cleared by
      // whatever settled the previous request (worker.onmessage/onerror, or
      // the timeout firing and tearing the worker down itself).
      if (!workerHandshakeCompleteRef.current) {
        const timeoutMs = deriveHandshakeTimeoutMs(stateRefForDispatch.current.buildings.length);
        // FEAT-2326609771 round follow-up ("HUD honesty during the
        // handshake window"): report the start of this waiting window so
        // QueueDepthHud can show "worker starting (Xs)" instead of the
        // misleading steady-state "N pending" reading while the clock is
        // still frozen on this worker instance's very first reply.
        getGlobalWorkerQueueTracker().reportHandshakeStartAt(Date.now());
        workerHandshakeTimeoutRef.current = setTimeout(() => {
          workerHandshakeTimeoutRef.current = null;
          if (workerHandshakeCompleteRef.current) return; // belt-and-braces: the reply won the race just before this fired.
          const stillCurrent = workerRef.current === worker;
          if (!stillCurrent) return; // this worker was already torn down/replaced by something else.
          // Same shutdown sequence as worker.onerror: terminate before
          // clearing workerRef so no last-second message can race the
          // fallback path, then reset every tracker/controller so nothing
          // is left thinking a request is still outstanding.
          worker.onmessage = null;
          worker.onerror = null;
          worker.terminate();
          workerRef.current = null;
          getGlobalWorkerQueueTracker().reset();
          offloadControllerRef.current = initialOffloadControllerState();
          getGlobalWorkerFallbackTracker().report('handshake-timeout');
          recordError(
            `Web Worker tick offload: first tick reply did not arrive within ${timeoutMs}ms; falling back to synchronous tick.`,
            { type: 'app', action: 'worker-handshake-timeout', code: 'MET-V856' }
          );
          // The abandoned request's tick was never applied and never will
          // be — run it NOW via the ordinary fallback reducer rather than
          // waiting for the next scheduled interval fire, so the timeout
          // costs at most one extra tick's worth of delay, never a silently
          // skipped tick (the "never lose a tick" requirement).
          wrappedDispatch({ type: 'tick' });
        }, timeoutMs);
      }
      return true;
    } catch (err) {
      // Unwind EXACTLY what beginTickRequest + enqueue() just committed,
      // using the controller's own proper transitions rather than
      // hand-rolling a new state shape here: invalidateInFlight clears
      // pendingTick/activeRequestId/activeRequestTick (and bumps
      // supersedeStreak, same accounting as any other request that never
      // got its reply applied — this one never even left the building), and
      // clearWorkerBusy frees the busy flag since the worker never actually
      // started this computation, so there is nothing left outstanding in
      // its mailbox to wait for. Order matches worker.onmessage's own
      // clear-busy-first convention (see its BUG-592 comment).
      offloadControllerRef.current = clearWorkerBusy(invalidateInFlight(offloadControllerRef.current));
      // The tracker's enqueue() above must be un-enqueued too — this
      // request was never actually sent, so it must never read as
      // outstanding backlog.
      getGlobalWorkerQueueTracker().drain();
      // BUG-618: postMessage itself threw — no reply will ever arrive to
      // consume workerPostAtRef, so clear it now rather than leaving a
      // stale timestamp for a LATER successful post's onmessage to
      // mistakenly compute against.
      workerPostAtRef.current = null;
      const detail = err instanceof Error ? err.message : String(err);
      recordError(`Worker tick offload failed to post (${detail}); falling back to synchronous tick.`, {
        type: 'app',
        action: 'worker-postmessage',
      });
      return false;
    }
  };

  // Construct/tear down the worker exactly once per SimProvider lifetime
  // (or never, if disabled/unavailable) — a stable worker instance is
  // required so the tick-driver effect and this effect agree on the SAME
  // `workerRef.current`.
  useEffect(() => {
    if (!webWorkerOffloadEnabled()) return undefined;
    let worker: Worker;
    try {
      worker = new Worker(new URL('./simWorker.ts', import.meta.url), { type: 'module' });
    } catch (err) {
      // AC-8: construction throwing must never crash the app — leave
      // workerRef null so every call site below falls back to main-thread.
      // FEAT-2326609771 (default-ON hardening): default-ON means EVERY
      // browser now exercises this path, not just opt-in testers — a silent
      // fallback with no trace is no longer good enough, so this now also
      // reports the reason (QueueDepthHud.tsx's worker line) and records a
      // registry-sourced error (GR#7), same discipline as the handshake-
      // error and handshake-timeout branches below.
      const detail = err instanceof Error ? err.message : String(err);
      getGlobalWorkerFallbackTracker().report('construct-failed');
      recordError(`Web Worker tick offload: construction failed (${detail}); falling back to synchronous tick.`, {
        type: 'app',
        action: 'worker-construct',
        code: 'MET-V856',
      });
      return undefined;
    }
    const tracker = getGlobalWorkerQueueTracker();
    worker.onmessage = (ev: MessageEvent<WorkerToMainMessage>) => {
      const msg = ev.data;
      // BUG-592/BUG-597 fix: ANY message the worker sends back — regardless
      // of its type — means the worker has actually finished the ONE
      // computation it was given. Clear workerBusy (and drain the tracker)
      // HERE, unconditionally, BEFORE narrowing on msg.type at all — so the
      // NEXT tick-driver interval fire is allowed to post again (see
      // beginTickRequest's workerBusy guard) no matter what kind of reply
      // this turns out to be. Order matters (round-4 follow-up finding): the
      // previous code put the `msg.type !== 'tickResult'` early-return
      // BEFORE this clear, which happened to be unreachable while
      // 'tickResult' was the only WorkerToMainMessage variant, but would
      // silently strand workerBusy=true forever — freezing all future
      // worker ticks — the instant a second message type existed and this
      // handler returned through the guard without ever reaching the clear
      // below it. Previously this slot was freed at INVALIDATION time
      // instead of at actual worker-completion time, letting main post a
      // second (or third, ...) real ~1.77MB SimState clone into the
      // worker's serial, uncancellable mailbox every interval the round
      // trip outlasted — 501 queued / ~887MB measured over a 60s sustained
      // drag at interval=100ms/rt=600ms.
      offloadControllerRef.current = clearWorkerBusy(offloadControllerRef.current);
      tracker.drain();
      tracker.reportSupersedeStreak(offloadControllerRef.current.supersedeStreak);
      // FEAT-2326609771 (default-ON hardening): ANY message at all — the
      // worker just replied for the first time — is proof of life. Clear the
      // handshake watchdog (see issueTickRequest's arm-on-post) and latch
      // workerHandshakeCompleteRef so no FUTURE request ever arms another
      // one; a later crash is `runtime-error` (worker.onerror below), not a
      // handshake failure, precisely because this line already ran.
      if (workerHandshakeTimeoutRef.current !== null) {
        clearTimeout(workerHandshakeTimeoutRef.current);
        workerHandshakeTimeoutRef.current = null;
      }
      workerHandshakeCompleteRef.current = true;
      // HUD honesty: the waiting window this reply just settled is over —
      // clear it so QueueDepthHud stops reading "worker starting" the
      // instant the clock actually starts moving.
      tracker.reportHandshakeStartAt(null);
      // BUG-618: the worker is observed to have finished ITS one outstanding
      // computation right here, unconditionally (same placement rationale as
      // clearWorkerBusy just above) — record the round-trip duration
      // regardless of whether the reply turns out to be stale/discarded
      // below, since the worker genuinely spent that wall-clock time either
      // way and that cost is exactly what the gauge exists to surface.
      if (workerPostAtRef.current !== null) {
        engineLagTracker.recordTickDuration(performance.now() - workerPostAtRef.current);
        workerPostAtRef.current = null;
      }
      if (msg.type !== 'tickResult') return;
      // B2/B3 fix: ALL of "is this reply stale", "should it be applied",
      // and "what tick number (if any) to journal" are decided by the pure
      // controller — see simWorkerOffloadController.ts's decideTickReply
      // header for the full reasoning (requestId-based discard, not a
      // tick-number comparison against whatever `state` happens to be
      // current by the time this runs).
      const { state: nextControllerState, decision } = decideTickReply(
        offloadControllerRef.current,
        { requestId: msg.requestId, resultTick: msg.state.tick },
        stateRefForDispatch.current.tick
      );
      const wasStaleMismatch = nextControllerState === offloadControllerRef.current;
      offloadControllerRef.current = nextControllerState;
      if (wasStaleMismatch) {
        // N2 fix (independent round 3 REJECT, 2026-09-02) / BUG-592: this
        // reply was already invalidated (a superseding action, or a
        // reset/load) — its request/reply bookkeeping was already settled
        // at invalidation time (invalidateInFlightWorkerTick); the drain()
        // and workerBusy-clear above are the ONLY things this arrival still
        // needed to do, since (BUG-592) invalidation no longer drains or
        // frees the worker slot itself — only an actual observed
        // reply/error does.
        //
        // DELIBERATELY DISCARD, do NOT rebase. The round-2 fix immediately
        // re-issued a new request right here — combined with the K-supersede
        // forced-sync-tick escape, that turned into an INTERVAL-INDEPENDENT
        // tick generator: under continuous sub-round-trip-interval input
        // (e.g. a 60Hz drag against a ~16ms worker round-trip), issue/
        // invalidate/reissue cycles could complete many times WITHIN a
        // single SPEED_MS interval period, each Kth one forcing a
        // synchronous tick — decoupling total tick production entirely from
        // the player's selected speed (measured: ~20x the selected speed at
        // SPEED_MS=1000, 16ms round-trip, 60Hz drag). The round's own
        // control proved the opposite failure mode does NOT occur when
        // rebase is removed: with supersedes only ever occurring against
        // requests the INTERVAL itself issued, the supersede rate — and
        // therefore the forced-tick rate — is bounded by the interval rate,
        // so total tick production can never exceed the selected speed
        // (ratio <= 1.0 always; see test/simworker-offload.test.mjs's N2
        // ceiling matrix). The K-escape ALONE still guarantees liveness
        // (progress >= 1 tick per K+1 intervals under sustained contention —
        // slower than selected speed, never faster, and never frozen) — the
        // NEXT scheduled interval fire (not this handler) issues the fresh
        // request, exactly like the AC-8 fallback's own cadence.
        return;
      }
      if (decision.kind === 'apply') {
        // B3 fix: journal the tick HERE, only now that we know the result
        // will actually be applied — never at request time (see
        // recordTickInJournalOnly's header comment).
        recordTickInJournalOnly(decision.tickToJournal);
        // BUG-677: mark this hydrate as a tick delivery so the reducer skips
        // its once-per-load ceremonies (AC-31 over-cap notice re-fired every
        // second without this, undismissably). Re-applied here after the
        // BUG-669 merge (the estate's store.tsx predated the BUG-677 fix).
        dispatch({ type: 'hydrate', state: msg.state, source: 'tick' });
        // BUG-618: this IS an applied tick (bypasses wrappedDispatch's own
        // 'tick' branch entirely — raw `dispatch` above — so it must be
        // counted here, the only place a worker-sourced tick actually lands).
        engineLagTracker.recordTickCompleted();
      }
    };
    worker.onerror = (ev: ErrorEvent) => {
      // Lesser finding (independent round, 2026-09-02): terminate the
      // worker BEFORE clearing workerRef, so no message this dying worker
      // might still emit can race the fallback path that starts running
      // main-thread ticks the instant workerRef.current reads null.
      worker.terminate();
      // FEAT-2326609771 (default-ON hardening): distinguish a HANDSHAKE
      // failure (this worker never once replied — the same class as
      // construction failing or the reply timing out) from a later
      // RUNTIME crash of an already-proven-alive worker. Both still fall
      // back identically (workerRef cleared, tracker/controller reset
      // below); the distinction is purely for the honest HUD line / error
      // record, not for behaviour.
      if (workerHandshakeTimeoutRef.current !== null) {
        clearTimeout(workerHandshakeTimeoutRef.current);
        workerHandshakeTimeoutRef.current = null;
      }
      const reason = workerHandshakeCompleteRef.current ? 'runtime-error' : 'handshake-error';
      getGlobalWorkerFallbackTracker().report(reason);
      const detail = ev && typeof ev.message === 'string' && ev.message.length > 0 ? ev.message : 'unknown worker error';
      recordError(
        `Web Worker tick offload: ${reason === 'handshake-error' ? 'handshake' : 'runtime'} error (${detail}); falling back to synchronous tick.`,
        { type: 'app', action: 'worker-onerror', code: 'MET-V856' }
      );
      // FEAT-2326609771 round follow-up (2026-09-04, "the asymmetry" — the
      // handshake-timeout path already ran the abandoned tick PROACTIVELY,
      // right here-equivalent, the instant it gave up on the worker;
      // onerror used to just clear workerRef and wait for the NEXT
      // scheduled interval fire, silently costing up to one full SPEED_MS
      // interval of extra lag on top of whatever request was already lost —
      // worse, during that gap the engine-lag/queue-depth gauges kept
      // reading the stale in-flight request as outstanding). Capture
      // whether a request was actually in flight BEFORE resetting the
      // controller below (tracker.reset()/initialOffloadControllerState()
      // wipe pendingTick unconditionally) — only THAT case has a genuinely
      // abandoned tick worth running immediately; a worker that crashes
      // with nothing outstanding must not be charged a spurious extra tick.
      const hadPendingTick = offloadControllerRef.current.pendingTick;
      // A worker runtime error disables the offload for the rest of this
      // session (workerRef cleared) — the tick-driver effect's fallback
      // branch then runs the reducer on main exactly as it always has.
      // AC-8: no user-visible error, no loss of save/load/journal function.
      // No journal write for whatever tick was in flight (B3: never
      // journaled until applied, and an errored worker's result is never
      // applied) — recreated fresh, right now, via the forced synchronous
      // tick below when one was actually owed.
      // No buffer to flush either (the buffering design was removed —
      // see invalidateInFlightWorkerTick's header comment): every non-tick
      // action already applied to main state the instant it was dispatched.
      workerRef.current = null;
      // BUG-592: whatever was outstanding (pendingTick and/or workerBusy —
      // an errored/dying worker may have been mid-computation on an
      // already-superseded request) is moot the instant the worker itself
      // is torn down — tracker.reset() unconditionally zeroes both the
      // backlog count and the reported supersede streak, and
      // initialOffloadControllerState() resets workerBusy to false, so
      // there is nothing left to selectively drain first.
      tracker.reset(); // also clears the reported supersedeStreak — a torn-down worker has nothing left to be "behind" on.
      offloadControllerRef.current = initialOffloadControllerState();
      if (hadPendingTick) {
        // Run the abandoned request's tick NOW, through the ordinary
        // main-thread fallback reducer — same call, same reasoning as the
        // handshake-timeout path just above: the crash costs at most one
        // tick's worth of delay, never a silent extra interval on top of
        // the reply that was already lost.
        wrappedDispatch({ type: 'tick' });
      }
    };
    workerRef.current = worker;
    return () => {
      worker.terminate();
      workerRef.current = null;
      // BUG-592: see worker.onerror's comment just above — reset()/
      // initialOffloadControllerState() unconditionally clear any
      // outstanding pendingTick/workerBusy, so no selective drain is needed.
      tracker.reset();
      offloadControllerRef.current = initialOffloadControllerState();
      // FEAT-2326609771: an ordinary unmount/teardown is not a failure — no
      // fallback reason is reported here — but a still-armed handshake
      // watchdog must be cancelled regardless, or it would fire against a
      // torn-down worker (workerRef already null by then) after this
      // SimProvider instance is gone.
      if (workerHandshakeTimeoutRef.current !== null) {
        clearTimeout(workerHandshakeTimeoutRef.current);
        workerHandshakeTimeoutRef.current = null;
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- dispatch is
    // the stable useReducer dispatch; wrappedDispatch's own identity is
    // stabilised (BUG-434) to depend only on tickTracker.
  }, [wrappedDispatch, dispatch]);

  // The dispatch exposed to the whole app via useSim(). A non-tick action is
  // ALWAYS applied immediately (never buffered/delayed — see
  // invalidateInFlightWorkerTick's header comment for the full reasoning);
  // the only extra behaviour versus wrappedDispatch is superseding whatever
  // tick request is currently in flight, so its reply (computed from a
  // state this action is about to move past) is unconditionally discarded
  // rather than silently reverting this action's effect.
  //
  // N1 fix (independent round 2 REJECT, 2026-09-02): "continuous input
  // starves the tick to a dead stop" — the round proved, against the real
  // controller+reducer+journal, that with the ORIGINAL invalidate-on-every-
  // action design, one action per interval fire (a drag-paint's per-
  // pointermove 'place' at up to 60Hz is exactly this) invalidates EVERY
  // tick request before its worker reply can land, forever — 200 interval
  // fires produced 0 applied ticks, with queue-depth silently reading
  // "caught up" the whole time (each supersede drains its own slot). Fixed
  // by shouldForceSyncTick's K-consecutive-supersedes escape: once
  // offloadControllerRef's streak reaches the threshold, the NEXT tick is
  // forced through the EXISTING synchronous main-thread fallback path
  // (wrappedDispatch({type:'tick'}), the same one AC-8 already uses when
  // the worker is unavailable) — this has no worker round-trip at all, so
  // it cannot be starved by input arriving after it has already returned,
  // guaranteeing the clock advances at least once every K actions
  // regardless of input rate or worker latency. Run AFTER the user's own
  // action (never delays it — the whole point of this feature) so a forced
  // catch-up tick reads as "the overdue tick finally landed right after
  // your click", never as added click latency.
  //
  // BUG-669 (P1, 2026-09-04, independent round REJECT): "always applied
  // immediately" above is still true for the LIVE view — but while the
  // chunked savepoint-tail replay is running (`tailReplayActiveRef`, set by
  // the effect below) a non-tick action's immediate application is to a
  // `state` that is about to be wholesale REPLACED by that replay's own
  // `dispatch({type:'hydrate', ...})`, which would otherwise discard it with
  // no error and no recovery until a later reload replays the journal (the
  // journal itself is unaffected — wrappedDispatch journals unconditionally
  // — only the LIVE state diverged). Two responses, chosen per action type:
  //   - 'reset': REJECTED outright (no wrappedDispatch call at all, visible
  //     error instead). GR#27's pre-wipe debug capture is a side-effecting
  //     ceremony entangled with wrappedDispatch's own reset special-case
  //     (capture-then-wipe, capture skipped on failure) — replaying a
  //     captured reset a SECOND time through the buffer-drain's bare
  //     `reducer()` calls (see the tail-replay effect) would either re-run
  //     that fail-closed capture side effect twice or skip it on the
  //     surviving copy, either of which is a GR#27 violation. There is also
  //     no sane MERGE of "wipe to a fresh city" with "the tail replay's
  //     landing city" — one must lose, and losing the player's explicit
  //     Start Over silently is worse than telling them to wait.
  //   - every other non-tick action: applied immediately exactly as before
  //     (the player still sees it land with no input lag) AND pushed onto
  //     `tailReplayBufferRef`, so the tail-replay effect can replay it, in
  //     order, on top of its own `finalState` right before hydrate lands —
  //     surviving instead of being discarded.
  const guardedDispatch = useMemo(() => {
    return (action: Action) => {
      if (action.type !== 'tick' && tailReplayActiveRef.current) {
        if (action.type === 'reset') {
          recordError(
            'Start Over is unavailable while a large save is still loading — wait for the load to finish, then try again.',
            { type: 'app', action: 'reset-during-replay' },
          );
          return;
        }
        tailReplayBufferRef.current.push(action);
      }
      if (workerRef.current && offloadControllerRef.current.pendingTick && action.type !== 'tick') {
        invalidateInFlightWorkerTick();
      }
      wrappedDispatch(action);
      if (workerRef.current && action.type !== 'tick' && shouldForceSyncTick(offloadControllerRef.current)) {
        offloadControllerRef.current = afterForcedSyncTick(offloadControllerRef.current);
        getGlobalWorkerQueueTracker().reportSupersedeStreak(0);
        wrappedDispatch({ type: 'tick' });
      }
    };
  }, [wrappedDispatch]);

  // BUG-617 (P1, 2026-09-03): drive the deferred large-tail replay (see
  // boot's own comment and pendingTailReplay's declaration above) CHUNKED,
  // exactly mirroring onRebuild's own generator-driven pattern below —
  // requestAnimationFrame between chunks so the tab stays responsive and the
  // player sees live "Loading city — N/M actions" progress via the SAME
  // RebuildPrompt overlay `applyLoadedSave` already uses for a plain Load
  // (busyLabel keys off `rebuildDecision.kind === 'load'`, see the provider
  // value's JSX below). Runs exactly once per pendingTailReplay instance
  // (the effect clears it on completion/failure, so it can never re-fire for
  // the same tail) — mount-time only, never re-triggered by ordinary play.
  //
  // F4 FIX (independent round REJECT, 2026-09-03): the original version of
  // this effect abandoned its generator on cleanup with a bare
  // `cancelled = true` — no `gen.return()`, no generation guard, no
  // rebuildInProgress reset. The attacker's A3 pinned the BUG-460-class
  // consequence directly (an abandoned generator that skips its own
  // try/finally can leave module-scoped replay state stuck, and — even
  // without that specific leak after F1's setReplayMode removal — a
  // never-closed generator is a lifecycle bug on general principle, exactly
  // as BUG-460 FIX A already established for onRebuild's own chain below).
  // This now shares onRebuild's `rebuildGenRef` counter (mutual exclusion:
  // the two chunked chains — tail-replay and genesis-rebuild — never race
  // state ownership) and calls `gen.return()` on every exit path.
  //
  // BUG-669 (P1, 2026-09-04, independent round REJECT, fix (c)): this was a
  // plain `useEffect`, which React runs AFTER the browser paints — so the
  // FIRST frame ever painted showed the fully-interactive, overlay-free app
  // (rebuildDecision starts null on this boot path; see boot's own comment),
  // and only a later frame added the "Loading your city…" overlay once this
  // effect got to run. That gap is exactly the window guardedDispatch's
  // buffering above exists to cover, but "cover it" is strictly worse than
  // "close it" — `useLayoutEffect` runs synchronously after DOM mutations
  // but BEFORE the browser paints, so the `setRebuildDecision` call two
  // lines below lands in the SAME commit as the initial pre-tail state,
  // and the overlay is present in the very first painted frame.
  useLayoutEffect(() => {
    if (!pendingTailReplay) return;
    // BUG-669: mark the discard-risk window OPEN for guardedDispatch, for
    // the exact lifetime of this effect (cleared in `finish()` below and in
    // this effect's own cleanup, whichever fires — both are "this replay
    // attempt is over, live `state` is no longer about to be replaced").
    tailReplayActiveRef.current = true;
    let cancelled = false;
    rebuildGenRef.current += 1;
    const myGen = rebuildGenRef.current;
    const running = currentBuildVersion();
    setRebuildDecision({ savedVersion: running, currentVersion: running, camera: pendingTailReplay.camera, kind: 'load' });
    setRebuildPhase('running');
    setRebuildProgress(null);
    setRebuildInProgress(true);

    // FEAT-2326609780 inc2: the IDB-freshness swap supplies its own base (the
    // IDB savepoint's own snapshot, not whatever localStorage-sourced state
    // is currently mounted) via `swapBaseState`; every other caller of this
    // mechanism (boot's own large-tail path) leaves it undefined and gets the
    // existing "replay onto the already-mounted state" behaviour, unchanged.
    const gen = replayTailChunked(
      pendingTailReplay.swapBaseState ?? stateRefForDispatch.current,
      pendingTailReplay.tail,
      pendingTailReplay.needsJobsGrandfatherCatchUp,
    );

    const closeGen = () => {
      try {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any -- the
        // return value is discarded; only the generator-close side effect matters.
        gen.return(undefined as any);
      } catch {
        // Closing an already-finished/closed generator is a no-op; ignore.
      }
    };

    // F2 FIX: on SUCCESS (no msg), if the tail's ORIGINAL savepoint was
    // itself cross-build, hand off to the Rebuild-from-genesis prompt now
    // that the old-engine state has landed — never silently drop the
    // player's pending rebuild decision just because a large tail also
    // needed chunking.
    const finish = (msg?: string) => {
      // BUG-669: the discard-risk window is closed the instant this replay
      // attempt ends, success or failure alike — a failure never hydrated
      // (live `state` was never replaced, so anything buffered is already
      // sitting safely in it via guardedDispatch's own immediate apply), and
      // a success drains the buffer itself just above where `finish()` is
      // called. Either way nothing must remain buffered for a NEXT replay
      // attempt (a fresh `pendingTailReplay`) to inherit by accident.
      tailReplayActiveRef.current = false;
      tailReplayBufferRef.current = [];
      setRebuildInProgress(false);
      setRebuildProgress(null);
      setPendingTailReplay(null);
      if (msg) {
        recordError(msg, { type: 'app', action: 'load' });
        showCaptureError(msg, 'load');
        setRebuildDecision(null);
        setRebuildPhase('prompt');
        return;
      }
      if (pendingTailReplay.crossBuildAfter) {
        setRebuildDecision({
          savedVersion: pendingTailReplay.crossBuildAfter.savedVersion,
          currentVersion: pendingTailReplay.crossBuildAfter.currentVersion,
          camera: pendingTailReplay.camera,
          kind: 'rebuild',
        });
        setRebuildPhase('prompt');
        return;
      }
      setRebuildDecision(null);
      setRebuildPhase('prompt');
    };

    const processChunk = () => {
      // F4: generation guard (mirrors onRebuild's BAR-3 guard exactly) — a
      // newer chain (this effect re-firing on a fresh pendingTailReplay, or
      // onRebuild starting a genesis rebuild) has superseded this one. Abort
      // with no setState/persist and close the generator so it can't leak.
      if (isStaleRebuildChain(myGen, rebuildGenRef.current)) {
        closeGen();
        return;
      }
      if (cancelled) return;
      let chunk: ReturnType<typeof gen.next>;
      try {
        chunk = gen.next();
      } catch (e) {
        // Strict, non-defensive (replayTailChunked's contract): a throw here
        // means real corruption in the tail, not an expected rules
        // divergence — abort exactly like restoreFromSavepoint's own
        // catch-all, leaving the ALREADY-MOUNTED pre-tail state intact (never
        // a blank app) and surfacing the failure like any other load error.
        const detail = e instanceof Error ? e.message : String(e);
        finish(`Load aborted — could not replay recent actions: ${detail}. Your city is still usable from an earlier point.`);
        return;
      }
      if (chunk.done) {
        const result = chunk.value as { state: SimState; replayed: number };
        let finalState = { ...result.state, nextId: nextSafeBuildingId(result.state.buildings) };
        finalState = sanitizeTreasury(finalState);
        const afterReport = checkConsistencyRecoveringStaleFlows(finalState);
        if (afterReport.failures > 0) {
          finish(
            `Load aborted — replayed city failed consistency (${afterReport.failures} failures). Your city is still usable from an earlier point.`,
          );
          return;
        }

        // BUG-669 (P1, 2026-09-04, independent round REJECT): `finalState`
        // above was computed purely from the pre-replay snapshot + the
        // savepoint's own tail — it knows nothing about any action
        // guardedDispatch applied to the LIVE `state` while this replay was
        // running. Drain `tailReplayBufferRef` (in dispatch order) through
        // the SAME reducer right here, on top of `finalState`, BEFORE the
        // hydrate below lands — this is what makes the player's live view
        // (about to become `reconciledState`) agree with the journal, which
        // already recorded every one of these actions unconditionally via
        // wrappedDispatch at the moment each was dispatched (GR#21: no
        // divergence between journal and state). Close the window and take
        // sole ownership of the buffer FIRST (read-then-clear, both
        // synchronous, no `await` between them) so nothing dispatched in the
        // remainder of this same synchronous block could be silently lost
        // between the read and the clear.
        tailReplayActiveRef.current = false;
        const buffered = tailReplayBufferRef.current;
        tailReplayBufferRef.current = [];
        let reconciledState = finalState;
        if (buffered.length > 0) {
          for (const bufferedAction of buffered) {
            reconciledState = reducer(reconciledState, bufferedAction);
          }
          reconciledState = { ...reconciledState, nextId: nextSafeBuildingId(reconciledState.buildings) };
          reconciledState = sanitizeTreasury(reconciledState);
          // Safety net, not a gate the common (empty-buffer) path pays for:
          // reconcileState replays ORDINARY, already-validated player
          // actions (the reducer is fail-closed-as-no-op for a rejected
          // action — engine.ts carries zero `throw` statements — so this is
          // not expected to fail), but two independently-valid deltas (the
          // tail's and the player's) merging into one state is exactly the
          // kind of interaction this project's own consistency gate exists
          // to catch. If it somehow does not check out, fall back to the
          // tail-only `finalState` with a LOUD, non-silent error rather than
          // either landing a state this project's own gate does not trust,
          // or crashing the load entirely over an edge case in actions that
          // already applied safely once.
          const reconciledReport = checkConsistencyRecoveringStaleFlows(reconciledState);
          if (reconciledReport.failures > 0) {
            recordError(
              `${buffered.length} action(s) dispatched while your city was loading could not be safely merged in ` +
                `(${reconciledReport.failures} consistency failures) and were dropped from the loaded city. They remain ` +
                'in the journal, so a Rebuild from Genesis (Config) will recover them.',
              { type: 'app', action: 'load' },
            );
            reconciledState = finalState;
          }
        }

        // FEAT-2326609780 round 2 (F2, ATTACK B) / ROUND 3 (R2-F3 REJECT,
        // ATTACK H): an IDB-freshness swap replaces STATE from a lineage the
        // currently-persisted `journal` knows NOTHING about (it is
        // localStorage's own lineage) — genesis replay / hard-reset-replay
        // (GR#27/FEAT-1972079897) reads back the PERSISTED journal, so
        // leaving it untouched here means that feature silently reverts the
        // player to the pre-swap city (ATTACK B). There is no full companion
        // journal for an anonymous savepoint/overflow-slot lineage (only the
        // snapshot + the tail just replayed onto it) — round 3's OWN first
        // attempt to instead re-base to an EMPTY journal (reasoning: the
        // swapped savepoint already IS the fully-caught-up snapshot) was
        // REJECTED by re-running this estate's own ATTACK B: an empty
        // journal genesis-replays to the bare baseline city, not the
        // swapped-in one — it is NOT an honest lineage representation, it
        // just trades ATTACK H's failure mode for a NEW, permanent one (a
        // Rebuild-from-Genesis after a healthy swap would ALSO now produce a
        // bare city). The single synthetic
        // `{type:'hydrate', state: reconciledState}` entry (engine.ts's
        // 'hydrate' case substitutes `action.state` wholesale, so replaying
        // this one entry from ANY starting point reproduces the swapped-in
        // city exactly) is therefore restored — it is the only honest claim
        // that CAN be made about a lineage whose pre-snapshot history was
        // never recorded on this device — but ATTACK H proved that write can
        // itself be arbitrarily large (it embeds the whole SimState) and
        // `persistJournal` (journal.ts) DESTRUCTIVELY removes the key on any
        // failure when `entries.length <= 200` (always true here). The fix
        // is therefore atomicity around THAT destructive side effect, not
        // avoiding the entry shape: capture the pre-swap journal, attempt
        // the rebase, and on failure IMMEDIATELY re-persist the CAPTURED
        // pre-swap journal (small — it is the player's real, already-
        // journalled history, not a whole-SimState blob, so it fits under
        // the exact same quota that just rejected the rebase) before
        // aborting the whole swap loudly. Hydrate + journal + saveIndex all
        // land, or none do, and the pre-swap journal is never left deleted.
        if (pendingTailReplay.isIdbSwap) {
          const previousJournal = journalRef.current;
          const rebasedJournal: Journal = {
            entries: [{ tick: reconciledState.tick, action: { type: 'hydrate', state: reconciledState } as Action }],
          };
          const rebasedOk = journalPersisterRef.current?.flush(rebasedJournal) ?? false;
          if (!rebasedOk) {
            // `persistJournal`'s own failure branch has ALREADY called
            // `removeItem(JOURNAL_KEY)` by the time `flush` returns `false`
            // — restore the player's real pre-swap history immediately,
            // best-effort, before surfacing the failure. This small payload
            // (the player's own already-journalled actions, not a
            // whole-SimState blob) fits under the same quota that just
            // rejected the oversized rebase in every case this fix has been
            // proven against.
            journalPersisterRef.current?.flush(previousJournal);
            finish(
              'Load aborted — the fresher save found in your browser\'s durable storage could not be safely checkpointed (journal persist failed). Your current city is still active.',
            );
            return;
          }
          setJournal(rebasedJournal);
          setLastSaveIndex(rebasedJournal.entries.length);
        }

        // FEAT-2326609780 round 3: record whether THIS replay verified real
        // content (see `verifiedFloorBuildingsRef`'s own doc comment above)
        // — a non-empty tail means every one of its actions just ran through
        // the real reducer, so `reconciledState`'s building count is
        // trustworthy; an empty tail (a bare re-persist) establishes no floor.
        verifiedFloorBuildingsRef.current = pendingTailReplay.tail.length > 0 ? reconciledState.buildings.length : null;

        invalidateInFlightWorkerTick();
        dispatch({ type: 'hydrate', state: reconciledState });
        // Self-healing (BUG-617): persist a FRESH savepoint with an EMPTY
        // tail now that the replay succeeded — the NEXT boot then has
        // nothing large left to replay, rescuing this city permanently
        // after this one chunked load.
        //
        // F3 FIX (GR#1, independent round REJECT): `persistSavepoint` NEVER
        // throws on quota — it returns `false`. The original version of this
        // code only had a `try/catch` around the call, so a quota failure (or
        // any other non-throwing rejection — e.g. BUG-469's stale-overwrite
        // protection) was swallowed with zero signal: the self-heal silently
        // did not happen, and the player would hit this exact same large-tail
        // wedge again next boot with no idea why. Check the boolean and
        // surface it through the SAME visible path `saveGame()`/the autosave
        // timer already use for a quota failure (`recordError` + the
        // `autoSaveError` "⚠ save" indicator) — never silent.
        try {
          // BUG-635: on the CROSS-BUILD path (crossBuildAfter non-null) the
          // Rebuild-from-genesis prompt fires immediately after this write via
          // `finish()` below but is NOT YET resolved — stamping the healed
          // savepoint with `running` here would make a reload during that
          // unresolved window compute needsRebuild(saved=running, running) ===
          // false, silently dropping the prompt forever and leaving the player
          // on the OLD-ENGINE state believing it is current (the same class
          // BUG-468 already guards on the resolution paths, onKeep/onResume).
          // Carry the ORIGINAL pre-rebuild version instead so an unresolved
          // cross-build decision survives a reload exactly as it did before
          // BUG-617 r2; onKeep/onResume re-stamp to `running` once the player
          // actually resolves the prompt (either choice).
          const healedVersion = pendingTailReplay.crossBuildAfter
            ? pendingTailReplay.crossBuildAfter.savedVersion ?? running
            : running;
          // BUG-669: heal from `reconciledState`, not `finalState` — the
          // self-heal savepoint must describe the SAME city the player is
          // now looking at (finalState plus anything reconciled in above),
          // or the very next boot's "instant" restore would silently regress
          // behind the buffered actions this fix just finished preserving.
          const healed = createSavepoint(reconciledState, [], new Date(), healedVersion, pendingTailReplay.camera, nextSaveSeq());
          // FEAT-2326609780 round 2 (F1) / round 3: keep the IDB-freshness
          // comparison baseline current — a freshness re-check that fires
          // after THIS replay lands (the completion-path hook) must compare
          // against what the player is NOW looking at, not the pre-replay
          // boot snapshot `boot.bootSavepointMeta` was frozen at. Carries
          // `saveSeq` (round 3: the monotonic primary ordering key, not
          // `savedAt` — see this fix's own header comment on ATTACK G/R2-F1).
          // P0 RCA fix: MUST carry lineageId too — an earlier version of
          // this fix omitted it, which silently reset the comparison
          // baseline's lineage to "legacy" after every self-heal, making
          // the freshness effect read the WRONG (legacy) IDB keys and
          // refuse every genuine same-lineage rescue as "foreign lineage".
          bootSavepointMetaRef.current = { snapshotTick: healed.snapshotTick, savedAt: healed.savedAt, saveSeq: healed.saveSeq, lineageId: healed.lineageId };
          const healedOk = persistSavepoint(window.localStorage, healed);
          // FEAT-2326609780 inc2 (P2 follow-up filed under BUG-669): this
          // self-heal write previously mirrored NOTHING — a quota-wedged
          // localStorage healed the LIVE city in memory but never advanced
          // IndexedDB, so the next boot's IDB-freshness check (below) would
          // still see the OLD IDB savepoint and the wedge would never close.
          // Mirror unconditionally, exactly like every other persistSavepoint
          // call site now does — success mirrors the localStorage bytes,
          // failure mirrors `healed` directly into the overflow slot.
          mirrorAfterPersist(healedOk, healed);
          if (!healedOk) {
            recordError(
              'Self-heal save failed after a large-tail load (storage quota). The replayed city is active now, but the large tail may reappear on the next load — clear journal in Config, then Save.',
              { type: 'app', action: 'load' },
            );
            setAutoSaveError(true);
          } else {
            setAutoSaveError(false);
          }
        } catch (e) {
          // Genuine throw (corrupt storage, serialization failure) — still
          // best-effort for the LOAD itself (the player's city is already
          // safely in memory), but must not be silent either.
          const msg = e instanceof Error ? e.message : String(e);
          recordError(`Self-heal save failed after a large-tail load: ${msg}. The replayed city is active now.`, {
            type: 'app',
            action: 'load',
          });
          setAutoSaveError(true);
        }
        finish();
        return;
      }
      setRebuildProgress(chunk.value as ReplayProgress);
      requestAnimationFrame(processChunk);
    };

    processChunk();
    return () => {
      cancelled = true;
      closeGen();
      // BUG-669: defensive mirror of `finish()`'s own reset — this effect's
      // deps array (`[pendingTailReplay]`) means `pendingTailReplay` only
      // ever transitions non-null -> null via `finish()` itself (never to a
      // second non-null value), so in practice this only runs on unmount,
      // after which no further guardedDispatch call is possible anyway. Left
      // in for the same reason `finish()` clears them: nothing should ever
      // be left buffered against a stale `tailReplayActiveRef === true`.
      tailReplayActiveRef.current = false;
      tailReplayBufferRef.current = [];
      // F4: only clear the shared busy flag / bump the generation if this
      // chain still owns it — if something newer (onRebuild, or a fresh
      // pendingTailReplay) already superseded it, that chain owns cleanup.
      if (rebuildGenRef.current === myGen) {
        setRebuildInProgress(false);
        rebuildGenRef.current += 1;
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- intentionally
    // keyed ONLY on pendingTailReplay: dispatch/stateRefForDispatch/etc. are
    // stable across renders, and re-running this on every render would
    // restart the chunked replay from scratch.
  }, [pendingTailReplay]);

  // Autosave timer: every AUTOSAVE_INTERVAL_MS, persist a savepoint.
  // FEAT-1972079854: rolling autosave with fail-safe error handling.
  useEffect(() => {
    const intervalId = setInterval(() => {
      try {
        // Calculate journalTail: entries added since last savepoint.
        const tail = journalTail(journal, lastSaveIndex);
        // inc2: stamp the save with the running build + current camera so a later
        // boot on a new build can detect the change and offer a rebuild.
        const savepoint = createSavepoint(state, tail, new Date(), currentBuildVersion(), currentCamera(), nextSaveSeq());
        const success = persistSavepoint(window.localStorage, savepoint);
        setAutoSaveError(!success);
        if (success) {
          // Update lastSaveIndex to mark this checkpoint.
          setLastSaveIndex(journal.entries.length);
        }
        // FEAT-2326609780 inc2: mirror UNCONDITIONALLY — on success this is
        // the inc1 savepoint-slots-before-journal crash-consistency mirror;
        // on failure (the quota-wedge shape) this instead writes the
        // savepoint that just failed to reach localStorage directly into the
        // durable store's overflow slot, so IndexedDB keeps advancing even
        // while every localStorage slot is wedged.
        mirrorAfterPersist(success, savepoint);
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

  // CONSOLIDATOR AUDIT TRAIL (Aaron's ruling on FEAT-2326609761 + independent-
  // round finding 1, 2026-09-04, "TAB-MOUNTED-ONLY"): the audit trail's
  // 'discovered'/'planned' posting used to live in ConsolidatorTab's own
  // useEffect — LeftDock renders exactly one ActiveBody at a time, so the
  // instant Aaron switched to any other tab the trail silently stopped even
  // though the consolidator itself was still enabled. His ruling is
  // unambiguous: "the audit runs while the CONSOLIDATOR is enabled", not
  // "while its tab is visible". Moved HERE, to the sim lifecycle itself —
  // same shape as the autosave timer above (a wall-clock poll reading the
  // LATEST state via stateRef, never a stale render-time closure) — so the
  // trail runs for as long as the toggle is on, independent of what (if
  // anything) is on screen. ConsolidatorTab keeps its own display refresh
  // unchanged; this is now the ONLY call site that posts.
  //
  // Effect deps are ONLY [state.consolidatorEnabled]: the interval is
  // armed/disarmed exactly when the toggle flips, never re-created on every
  // tick — "zero cost when the toggle is off" means no interval exists at
  // all in that case, matching the map overlay / tab's own off-state
  // contract. While ON, each poll does the CHEAP check first
  // (monthlyScopeOf + isAuditDue, both O(1) — no building scan) and only
  // pays for the real O(buildings) section audit
  // (strandedCapacityReport/topOpportunities/currentMonthOpportunities) when
  // a post is actually due this simulated month. A persistently-unreachable
  // sink therefore costs one O(1) check per poll forever, not a repeated
  // full-city scan — isAuditDue only clears once a post SUCCEEDS
  // (postConsolidatorAudit's own contract), so a down sink keeps retrying
  // the cheap check every poll and only re-pays the expensive analysis once
  // it actually gets through.
  useEffect(() => {
    if (!(state.consolidatorEnabled ?? CONSOLIDATOR_ENABLED_DEFAULT)) return;

    const postAuditIfDue = () => {
      const s = stateRef.current;
      const scope = monthlyScopeOf(s.tick);
      const dueDiscovered = isAuditDue('discovered', scope);
      const duePlanned = isAuditDue('planned', scope);
      if (!dueDiscovered && !duePlanned) return; // cheap early-out: no O(buildings) work this poll

      const report = strandedCapacityReport(s);
      const monthDensityCount = currentMonthOpportunities(s).length;
      const monthTop = topOpportunities(s, scope.sectionKeys, AUDIT_TOP_LIMIT);
      const wholeMapKeys = Array.from({ length: TOTAL_SECTIONS }, (_, i) => i);
      const wholeMapTop = topOpportunities(s, wholeMapKeys, AUDIT_TOP_LIMIT);
      const atIso = new Date().toISOString(); // sim-lifecycle call site, mirrors backend.ts's commitDebug — not inside consolidator.ts's own pure analysis path.

      if (dueDiscovered) {
        void postConsolidatorAudit('discovered', scope, s.tick, {
          tick: s.tick,
          twelfth: scope.twelfth,
          full: scope.full,
          strandedActionableCapacity: report.totalActionableCapacity,
          strandedClusterCount: report.clusterCount,
          strandedReconnectCostLowerBound: report.totalEstimatedReconnectCost,
          strandedUnderConstructionCapacity: report.totalConstructionCapacity,
          wholeMapOpportunityCount: wholeMapTop.length,
          monthDensityOpportunityCount: monthDensityCount,
        }, atIso);
      }

      if (duePlanned) {
        void postConsolidatorAudit('planned', scope, s.tick, {
          tick: s.tick,
          twelfth: scope.twelfth,
          full: scope.full,
          inSquare: {
            sectionsInScope: scope.sectionKeys.length,
            densityOpportunityCount: monthDensityCount,
            top: monthTop,
          },
          wholeMap: {
            totalSections: TOTAL_SECTIONS,
            top: wholeMapTop,
          },
        }, atIso);
      }
    };

    postAuditIfDue(); // fire immediately on enable rather than waiting a full poll interval
    const auditIntervalId = setInterval(postAuditIfDue, AUDIT_POLL_MS);
    return () => clearInterval(auditIntervalId);
  }, [state.consolidatorEnabled]);

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
    if (state.speed === 0) {
      // F1 fix (independent round REJECT, 2026-09-03) — a paused engine
      // cannot be "behind" (nothing is being asked of it): settle() zeroes
      // the scheduled-vs-completed backlog left over from the instant
      // before pause (e.g. a drag-supersede burst right before the player
      // hit Pause) so the chip cannot read "Engine: N behind" forever while
      // paused. This effect re-runs on every state.speed transition (its own
      // dependency array below), so this fires exactly once per entry into
      // pause, not on every render.
      engineLagTracker.settle();
      return;
    }
    // BUG-618: report the interval length the tick-driver is ABOUT to run at
    // — read whenever this effect (re)fires, i.e. whenever state.speed
    // changes (this effect's own dependency array, below).
    engineLagTracker.setIntervalMs(SPEED_MS[state.speed]);
    const id = setInterval(() => {
      if (rebuildInProgress) return;
      // BUG-618: one "wants a tick" fire, counted BEFORE any
      // worker/fallback branching below — a fire that finds the worker
      // already busy (issueTickRequest() no-ops) still counts as scheduled,
      // which is exactly what makes backlog meaningful: it is the count of
      // intervals that wanted a tick but have not yet gotten one applied.
      // Deliberately AFTER the rebuildInProgress guard above: a chunked
      // genesis replay suspends the ordinary tick driver for an unrelated
      // reason (FEAT-1972079917) and is not itself engine-vs-UI lag — a long
      // rebuild must not inflate backlog with fires that were never real
      // ticks the live engine fell behind on.
      engineLagTracker.recordTickScheduled();
      if (workerRef.current) {
        // Stage 1 offload (Landing 2): hand the actual advance()
        // computation to the worker instead of running it here.
        // issueTickRequest() no-ops if one is already in flight — skip this
        // interval fire rather than piling up a request against state that
        // hasn't been hydrated with the last result yet. N2 fix (independent
        // round 3 REJECT, 2026-09-02): this interval is now the ONLY place a
        // tick request is ever issued — see issueTickRequest's own header
        // for why removing the onmessage rebase call site is what bounds
        // total tick production to the selected speed.
        //
        // B3 fix (independent round REJECT, 2026-09-02): the journal write
        // used to happen HERE, at request time — moved to worker.onmessage,
        // and ONLY on the branch that actually applies the result, so a
        // discarded/rejected/errored tick is never journaled as having
        // happened (see recordTickInJournalOnly's header comment).
        //
        // BUG-597: issueTickRequest() now reports whether it actually
        // handled this fire (posted, or already had one in flight) — only
        // return in that case. A postMessage failure returns false, and
        // this interval fire falls through to the fallback dispatch below
        // instead of silently dropping the tick.
        if (issueTickRequest()) return;
      }
      // Fallback path (AC-8): worker disabled/unavailable — exactly
      // today's untouched behaviour, same reducer module.
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
            // F2 fix (P0 lineage round): replayFromGenesis always starts from
            // initialState() with NO lineageId, only ever recovering one from a
            // JOURNALLED reset entry — which JOURNAL_CAP can roll off. Lineage is
            // an identity of the CITY being rebuilt, not of the journal contents,
            // so carry the CURRENT lineage forward explicitly rather than trusting
            // whatever (if anything) the replay happened to reconstruct.
            const rebuildLineageId = readCurrentLineageId(window.localStorage);
            result.state = { ...result.state, lineageId: rebuildLineageId };
            const rebuiltSave = createSavepoint(result.state, [], new Date(), running, decision.camera ?? currentCamera(), nextSaveSeq());
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
      // F3 fix (BUG-468 regression): restampSavepointsBuildVersion gained a
      // lineageId param — without it, a namespaced city's stamp is checked
      // against the LEGACY slots (lineageId undefined), never its own, so the
      // cross-build stamp on the current lineage never clears and the
      // "New build detected" prompt loops forever for that city.
      restampSavepointsBuildVersion(window.localStorage, currentBuildVersion(), readCurrentLineageId(window.localStorage));
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
      // F3 fix (BUG-468 regression): pass the current lineageId, same reason
      // as onKeep above — the stamp lives per-lineage now.
      restampSavepointsBuildVersion(window.localStorage, currentBuildVersion(), readCurrentLineageId(window.localStorage));
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
      mirrorPreWipeArchive();
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
      } else {
        // FEAT-2326609778: mirror the named-save slot into the durable layer.
        mirrorNamedSave(cityNameToSlug(save.name));
      }
    }
  };

  // FEAT-2326609780 round 3: `saveSeq` is optional and left UNSTAMPED for a
  // caller (exportCity) that never persists the result to this device's
  // durable stores — the monotonic counter only advances on an actual
  // persist attempt (saveGame/saveGameAs pass `nextSaveSeq()` explicitly).
  const buildCurrentSave = (name: string, saveSeq?: number) => {
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
      saveSeq,
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
        const savepointToPersist: Savepoint = {
          ...save.savepoint,
          snapshot,
          // journalTail stays [] deliberately: this is the tail-since-snapshot used
          // by the FAST boot-time restoreFromSavepoint() path, and `snapshot` here
          // already IS the fully-replayed end state, so there is no pending tail.
          // This is unrelated to `save.journal` (the FULL history) restored below.
          journalTail: [],
          buildVersion: running,
          // FEAT-2326609780 round 3: this persist bumps the monotonic
          // per-lineage counter too, same as every other persist site.
          saveSeq: nextSaveSeq(),
        };
        // P0 RCA fix, item 1/5 + F1 fix (P0 lineage round): loading a save makes
        // ITS lineage the current one — this MUST happen BEFORE the persist
        // below, not after. persistSavepointWithReason (replay.ts) defaults an
        // absent savepoint.lineageId from whatever `storage` says is CURRENT —
        // if the pointer still named the PREVIOUS lineage at persist time, a
        // legacy (lineageId-less) loaded save would get silently stamped into
        // that previous (still-current) lineage's namespace instead of staying
        // legacy, contaminating the abandoned city's own slots. Normalise
        // absent -> LEGACY_LINEAGE_ID so a legacy save's pointer write is never
        // skipped, or the next boot resurrects the abandoned city instead of
        // the one the player just loaded.
        writeCurrentLineageId(window.localStorage, normalizeLineageId(savepointToPersist.lineageId));
        const persisted = persistSavepoint(window.localStorage, savepointToPersist);
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
        // FEAT-2326609780 inc2: mirror unconditionally (was `if (persisted)`
        // — a quota failure here left IndexedDB holding the PREVIOUS city's
        // savepoint even though the player just loaded a different one).
        mirrorAfterPersist(persisted, savepointToPersist);
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
        // B2 fix (independent round REJECT, 2026-09-02): this raw hydrate
        // wholesale-replaces state with the LOADED save, whose tick number
        // can be lower (an older save) than whatever tick an in-flight
        // worker request was computed against — the old code left
        // pendingTickRef/activeTickRequestIdRef untouched here, so that
        // request's eventual reply (a HIGHER, pre-load tick number) passed
        // the monotonic guard and clobbered the just-loaded city with the
        // stale pre-load one. Reachable in ordinary play (Load Save while a
        // tick happens to be round-tripping the worker), not a contrived
        // race — invalidate BEFORE this hydrate proceeds.
        invalidateInFlightWorkerTick();
        dispatch({ type: 'hydrate', state: snapshot });
        // FEAT-2326609780 round 3 (R2-F4, independent round REJECT): a
        // manual Load is a deliberate, explicit lineage choice by the
        // player — the post-mount IDB-freshness effect (this component,
        // above) must never later swap a DIFFERENT city in over it just
        // because some other durable savepoint happens to compare as
        // "fresher" by the old boot-time baseline. Update the comparison
        // baseline to this load's own identity (so a stale/unrelated
        // candidate cannot masquerade as fresher than what the player just
        // chose) AND close the swap surface for the rest of this mount,
        // exactly as if the freshness effect had already run once.
        bootSavepointMetaRef.current = {
          snapshotTick: savepointToPersist.snapshotTick,
          savedAt: savepointToPersist.savedAt,
          saveSeq: savepointToPersist.saveSeq,
          lineageId: savepointToPersist.lineageId,
        };
        idbSwapAttemptedRef.current = true;
        // Lineage pointer already written above, BEFORE the persist (see
        // comment there for why the ordering matters).
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

  /**
   * P0 RCA fix (Aaron, 2026-09-04, item 4 — "the aggravator inside the P0"):
   * turn a `persistSavepointWithReason` result into the loud, visible error
   * every manual save refusal now surfaces, and describe it honestly by
   * reason — a lineage/ordering conflict is not a quota problem, and telling
   * the player the wrong cause sends them to the wrong fix. Shared by
   * `saveGame`/`saveGameAs` so the two call sites can never drift apart
   * again (they already had: saveGameAs previously ignored its own return
   * value entirely).
   */
  const surfaceSaveRefusal = (reason: SavepointRejectReason | undefined): void => {
    const msg =
      reason === 'stale-overwrite'
        ? 'Save refused: a fresher save already exists for this city (an ordering/lineage conflict). This city is NOT being saved — your recent play is safe only in memory until you try again.'
        : 'Save failed (storage quota). This city is NOT being saved — clear journal in Config, then try again or use Save As.';
    recordError(msg, { type: 'app', action: 'save' });
    setAutoSaveError(true);
  };

  const saveGame = async (): Promise<boolean> => {
    try {
      const save = buildCurrentSave(cityName, nextSaveSeq());
      const result = persistSavepointWithReason(window.localStorage, save.savepoint);
      // FEAT-2326609780 inc2: mirror unconditionally, success or failure, so
      // a quota-failed manual save still advances the durable IndexedDB copy.
      mirrorAfterPersist(result.ok, save.savepoint);
      if (!result.ok) {
        // P0 RCA fix, item 4: a REFUSED save must NOT clear the journal —
        // the player's real, unsaved history is the ONLY record of what
        // happened since the last successful checkpoint; the OLD code
        // cleared it here UNCONDITIONALLY, silently discarding it on every
        // refusal while claiming (via the quiet autosave dot alone) that
        // nothing was wrong.
        surfaceSaveRefusal(result.reason);
        return false;
      }
      // BUG-458: flush — a save is exactly the boundary where losing the
      // journal tail would be unacceptable (it's now folded into the savepoint).
      journalPersisterRef.current?.flush(emptyJournal());
      setLastSaveIndex(journalRef.current.entries.length);
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
      const save = buildCurrentSave(label, nextSaveSeq());
      const savedAsResult = persistSavepointWithReason(window.localStorage, save.savepoint);
      // FEAT-2326609780 inc2: mirror unconditionally (see mirrorAfterPersist).
      mirrorAfterPersist(savedAsResult.ok, save.savepoint);
      if (!savedAsResult.ok) {
        // P0 RCA fix, item 4 — "the aggravator inside the P0": this return
        // value was PREVIOUSLY IGNORED ENTIRELY (store.tsx:2319 in the RCA's
        // own file:line evidence) — Save As cleared the journal and reported
        // `{ok:true}` to the caller regardless of whether the write actually
        // landed, so a refused Save As silently discarded the player's
        // recent history while claiming success. Refuse the SAME way
        // saveGame does: no journal clear, no city-name switch, no file
        // download, a loud error, and an honest `ok:false` returned.
        surfaceSaveRefusal(savedAsResult.reason);
        return { ok: false };
      }
      // BUG-458: flush — Save As is a save boundary, same as saveGame.
      journalPersisterRef.current?.flush(emptyJournal());
      setLastSaveIndex(journalRef.current.entries.length);
      setAutoSaveError(false);
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
    let save: GameSave | null;
    try {
      save = readNamedSave(window.localStorage, slug);
    } catch (e) {
      // BUG-577: readNamedSave now validates structurally and throws the
      // same registry-sourced MET-V850 as parseGameSave (File→Open) on a
      // malformed named save — thread it through recordError/showCaptureError
      // exactly like loadGame does above, instead of letting it reach
      // applyLoadedSave as an uncaught throw.
      const msg = e instanceof Error ? e.message : String(e);
      const code = (e as { code?: string })?.code;
      recordError(`Load refused: ${msg}`, { type: 'app', action: 'load', code });
      showCaptureError(msg, 'load', code);
      return;
    }
    if (!save) {
      recordError(`Load refused: no city named ${slug}`, { type: 'app', action: 'load' });
      return;
    }
    applyLoadedSave(save);
  };

  /**
   * FEAT-2326609778/Q100131 (Aaron, rides along with Q100121's IndexedDB
   * ruling): one-click, no-picker-dialog city backup — downloads the CURRENT
   * city's savepoint+journal, LZ-compressed (saveCodec.ts) exactly like the
   * durable stores already compress their payloads, so the exported file is
   * the same ~5-6x smaller shape as an in-app save rather than the larger
   * plain-JSON `Save As` output. Distinct from `saveGameAs` (which prompts a
   * filename/location via the File System Access API and writes uncompressed,
   * human-diffable JSON) — Export is the fast "grab a backup right now" path.
   */
  const exportCity = async (): Promise<boolean> => {
    try {
      // BUG-458: flush first — an export must reflect the current journal
      // tail, not a stale pre-debounce one.
      journalPersisterRef.current?.flush(journalRef.current);
      const save = buildCurrentSave(cityName);
      const compressed = encode(gameSaveText(save));
      const filename = suggestedSaveName(save.savepoint.snapshot.tick, cityName).replace(/\.json$/, '.mcity');
      triggerJsonDownload(filename, compressed, 'application/octet-stream');
      return true;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      recordError(`Export City failed: ${msg}`, { type: 'app', action: 'save', code: 'MET-V862' });
      return false;
    }
  };

  /**
   * FEAT-2326609778/Q100131: import a city previously produced by
   * `exportCity` (or a plain `Save As` file — `decode()` is a no-op on
   * uncompressed input, so both shapes load through this one path). Validates
   * via the SAME MET-V850 structural-rejection machinery as File→Open/named
   * saves (GR#3 SSOT — gamesave.ts's `parseGameSave`), then hydrates through
   * the normal `applyLoadedSave` path (pre-wipe archive of the outgoing city,
   * rebuild-decision prompt if the import crosses a build boundary, etc.).
   */
  const importCity = async (): Promise<boolean> => {
    let raw: string | null;
    try {
      raw = await pickAnyFile();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      recordError(`Import City failed: ${msg}`, { type: 'app', action: 'load', code: 'MET-V863' });
      showCaptureError(msg, 'load', 'MET-V863');
      return false;
    }
    if (raw == null) return false;
    if (raw.length > 15_000_000) {
      const msg = 'Import refused: file is larger than 15 MB.';
      recordError(msg, { type: 'app', action: 'load', code: 'MET-V863' });
      showCaptureError(msg, 'load', 'MET-V863');
      return false;
    }
    let parsed;
    try {
      parsed = parseGameSave(decode(raw));
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      const code = (e as { code?: string })?.code ?? 'MET-V863';
      recordError(`Import City rejected: ${msg}`, { type: 'app', action: 'load', code });
      showCaptureError(msg, 'load', code);
      return false;
    }
    if (!parsed.ok || !parsed.save) {
      const reason = parsed.reason ?? 'invalid save';
      recordError(`Import City rejected: ${reason}`, { type: 'app', action: 'load', code: 'MET-V863' });
      showCaptureError(reason, 'load', 'MET-V863');
      return false;
    }
    applyLoadedSave(parsed.save);
    return true;
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
      // FEAT-webworker-sim-offload Stage 1: guardedDispatch is
      // behaviour-identical to wrappedDispatch except for the narrow
      // buffer-while-a-tick-is-in-flight window documented above it — see
      // guardedDispatch's own comment.
      dispatch: guardedDispatch,
      cityName,
      listSaves,
      listRecent,
      saveGame,
      saveGameAs,
      loadGame,
      loadNamed,
      renameCity,
      exportCity,
      importCity,
    }),
    [state, guardedDispatch, cityName],
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
      {rebuildDecision && isRebuildPromptTop && (
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
