// replay.ts — FEAT-1972079854: savepoint/restore and deterministic replay.
//
// A savepoint captures: (1) a snapshot of SimState at a known tick, (2) the journal
// tail since that snapshot, (3) a wall-clock saved timestamp. Restoring a savepoint
// replays the journal tail onto the snapshot, re-deriving the original end state
// deterministically. State consistency is verified before and after replay.
//
// Fail-safe: any localStorage error (quota, private mode, corruption) must degrade
// gracefully — never crash the app. Restore returns { success: false } and the app
// boots with a fresh initial state.

import type { SimState } from './types.ts';
import type { Journal, JournalEntry } from './journal.ts';
import type { MapViewState } from './uistate.ts';
import { reducer, initialState as getInitialState, nextSafeBuildingId, computeFlows } from './engine.ts';
import { runConsistencyChecks } from './consistency.ts';
import type { ConsistencyReport, RecomputedFlowsOverride } from './consistency.ts';
import type { ReplayProgress } from './genesisReplay.ts';
import { SPECS } from './data.ts';
import { emptyJournal } from './journal.ts';
import { safeSetItem } from './safeStorage.ts';
import { encode, decode } from './saveCodec.ts';

/**
 * Complete savepoint persisted to localStorage. Includes snapshot, journal tail,
 * and metadata for diagnostics. Stable schema — never change field names without
 * a migration.
 */
export interface Savepoint {
  /** ISO-8601 timestamp when the savepoint was saved (wall-clock). */
  savedAt: string;
  /** Game tick of the snapshot. */
  snapshotTick: number;
  /** Complete SimState snapshot at snapshotTick (before journal tail was replayed). */
  snapshot: SimState;
  /** Journal entries added since the snapshot (replay these to reach current state). */
  journalTail: JournalEntry[];
  /**
   * FEAT-1972079897 inc2 (build stamping, brief §4.3): the build/live version this
   * savepoint was produced under, so boot can detect a cross-build change and offer
   * a rebuild. Optional for backward tolerance — a legacy savepoint written before
   * inc2 has no stamp, and `needsRebuild` treats an absent stamp as "cannot prove a
   * rules change" (no prompt), preserving the pre-inc2 restore path exactly.
   */
  buildVersion?: string;
  /**
   * FEAT-1972079897 inc2 (camera restore, Aaron 2026-08-27): the UI camera at save
   * time (zoom + focus), captured so a post-rebuild resume can re-home the view
   * instead of jumping to the default. Envelope/UI state — never part of SimState,
   * so genesis replay stays deterministic. Optional (absent on legacy savepoints).
   */
  camera?: MapViewState | null;
}

/**
 * Result of a restore attempt. Always returns { success: boolean } so the caller
 * doesn't need try/catch; localStorage errors degrade to { success: false }.
 */
export interface RestoreResult {
  success: boolean;
  /** If success=true, the restored state. Otherwise undefined. */
  state?: SimState;
  /** Diagnostic: why restore failed (empty string on success). */
  reason?: string;
  /** If success=true, the number of journal entries replayed. */
  replayed?: number;
}

/**
 * The localStorage key for savepoints. Follow metropolis.* naming convention.
 * Multiple savepoints rotate through keys: savepoint.0, savepoint.1, savepoint.2
 * (see SAVEPOINT_CAP).
 */
export const SAVEPOINT_KEY_PREFIX = 'metropolis.savepoint';

/**
 * BUG-469 (Aaron ruling, Q100029): a single autosave slot means ANY overwrite
 * (a reload, a second tab, a race) destroys the only autosave with no recovery.
 * A rotating history of 3+ slots means no single bad/stale write can destroy
 * every prior good autosave. PLACEHOLDER tunable per spec — Aaron may retune.
 */
export const SAVEPOINT_CAP = 3;

/** Time in milliseconds between autosaves. PLACEHOLDER per spec; wall-clock timer in UI. */
export const AUTOSAVE_INTERVAL_MS = 30000; // 30 seconds

/**
 * BUG-469 (Aaron ruling, Q100029): autosaves auto-purge after ~1 month (dev
 * placeholder). Named saves are a completely separate storage mechanism
 * (namedsaves.ts) and are NEVER touched by this retention window.
 */
export const AUTOSAVE_RETENTION_MS = 30 * 24 * 60 * 60 * 1000; // ~1 month

/**
 * The subset of the Web Storage API the replay module needs — injectable for tests.
 */
export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/**
 * Generate the localStorage key for the Nth savepoint slot (0-based).
 */
function savepointKey(slot: number): string {
  return `${SAVEPOINT_KEY_PREFIX}.${slot}`;
}

/**
 * Read a single savepoint slot. Fail-safe: a missing key, corrupt JSON, or a
 * parse error all degrade to `null` — never throws.
 */
function readSlot(storage: StorageLike, slot: number): Savepoint | null {
  try {
    const raw = storage.getItem(savepointKey(slot));
    if (!raw) return null;
    // FEAT-1972079935: decode() is a no-op on a legacy uncompressed value
    // (no LZv1: prefix), so this reads both old and new savepoints.
    return JSON.parse(decode(raw)) as Savepoint;
  } catch {
    return null;
  }
}

/**
 * BUG-469 (Aaron ruling, Q100029): autosaves older than AUTOSAVE_RETENTION_MS
 * are auto-purged (dev placeholder ~1 month). An unparsable `savedAt` is
 * treated as "not stale" — never purge on a timestamp we can't trust.
 */
function isStaleAutosave(sp: Savepoint, nowMs: number): boolean {
  const savedMs = Date.parse(sp?.savedAt as unknown as string);
  if (Number.isNaN(savedMs)) return false;
  return nowMs - savedMs > AUTOSAVE_RETENTION_MS;
}

/**
 * Read all savepoints from localStorage, in order (slot 0, 1, 2).
 * Fail-safe: corrupt JSON or missing keys degrade to an empty list.
 *
 * BUG-469: also filters out any autosave older than AUTOSAVE_RETENTION_MS —
 * purge-on-read, so a stale slot never gets restored even if a purge-on-write
 * hasn't happened yet (e.g. the app has not autosaved since the retention
 * window passed). `now` is injectable for deterministic tests.
 */
export function readAllSavepoints(storage: StorageLike, now: Date = new Date()): Savepoint[] {
  const nowMs = now.getTime();
  const savepoints: Savepoint[] = [];
  for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
    const sp = readSlot(storage, slot);
    if (!sp) continue;
    if (isStaleAutosave(sp, nowMs)) continue;
    savepoints.push(sp);
  }
  return savepoints;
}

/**
 * Find the most recent savepoint by savedAt timestamp.
 */
export function mostRecentSavepoint(savepoints: Savepoint[]): Savepoint | null {
  if (savepoints.length === 0) return null;
  return savepoints.reduce((best, sp) => (sp.savedAt > best.savedAt ? sp : best));
}

/**
 * Persist a new savepoint, rotating slots so a bounded HISTORY of the newest
 * SAVEPOINT_CAP autosaves is kept (BUG-469) — never a single overwritten slot.
 *
 * Target-slot choice (no separate "next slot" cursor — GR#3, the slots
 * themselves are the only source of truth): prefer an empty slot; if all
 * SAVEPOINT_CAP slots are occupied, overwrite the OLDEST occupied one. That
 * is exactly a rotating history — the newest SAVEPOINT_CAP autosaves always
 * survive, the single oldest one rolls off.
 *
 * Overwrite protection (BUG-469): before overwriting an occupied slot, the
 * incoming savepoint must be strictly newer (by snapshotTick, then by
 * savedAt) than what is already there. A stale/older writer — a backgrounded
 * tab whose timer fires after a fresher autosave already landed, a slow
 * reload racing a live tab — is REJECTED (returns false) rather than
 * clobbering a fresher save. The prior good savepoint is left untouched.
 *
 * BUG-469: also purges any autosave older than AUTOSAVE_RETENTION_MS before
 * picking a target slot, so long-idle dev installs don't accumulate
 * autosaves forever (named saves are a separate mechanism and are never
 * touched here).
 *
 * Fail-safe: localStorage errors are caught and logged silently; the app
 * continues without the savepoint. Returns whether the save succeeded (true
 * = persisted, false = failed OR protected-against-stale-overwrite). The
 * caller should display a quiet indicator on failure (FEAT-1972079854 spec,
 * AC-7 / GR#1).
 */
export function persistSavepoint(
  storage: StorageLike,
  savepoint: Savepoint,
  now: Date = new Date()
): boolean {
  try {
    for (let slot = SAVEPOINT_CAP; slot < 8; slot++) {
      try {
        storage.removeItem(savepointKey(slot));
      } catch {
        /* leftover slots from older caps */
      }
    }

    const nowMs = now.getTime();
    const slots: Array<{ slot: number; sp: Savepoint | null }> = [];
    for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
      let sp = readSlot(storage, slot);
      // BUG-469: purge-on-write — drop autosaves older than the retention
      // window so a slot they occupy is free for rotation again.
      if (sp && isStaleAutosave(sp, nowMs)) {
        try {
          storage.removeItem(savepointKey(slot));
        } catch {
          /* ignore — worst case the stale slot lingers, filtered on read */
        }
        sp = null;
      }
      slots.push({ slot, sp });
    }

    const emptySlot = slots.find((s) => s.sp === null);
    const target = emptySlot ?? slots.reduce((oldest, s) => (s.sp!.savedAt < oldest.sp!.savedAt ? s : oldest));

    if (target.sp) {
      const incomingTick = savepoint.snapshotTick;
      const existingTick = target.sp.snapshotTick;
      const incomingIsNewer =
        incomingTick > existingTick || (incomingTick === existingTick && savepoint.savedAt >= target.sp.savedAt);
      if (!incomingIsNewer) {
        // BUG-469 overwrite protection: reject the stale write outright.
        // The prior (fresher) savepoint in this slot is left intact.
        return false;
      }
    }

    // BUG-457: route through the shared quota-safe helper instead of a bare
    // setItem — the outer try/catch still covers readSlot/removeItem.
    // FEAT-1972079935: encode() compresses the (large) serialized savepoint
    // before it hits localStorage — smaller payload, same quota-safe path.
    const result = safeSetItem(storage, savepointKey(target.slot), encode(JSON.stringify(savepoint)));
    return result.ok;
  } catch {
    return false;
  }
}

/**
 * BUG-468: re-stamp every persisted savepoint's `buildVersion` to the running app
 * version, so a cross-build mismatch clears after ONE rebuild-prompt resolution and
 * the "New build detected" prompt does NOT recur on the next load.
 *
 * The infinite-loop root cause: the prompt fires when the persisted savepoint's
 * buildVersion differs from the running build. If a resolution path (Keep, or a
 * rebuild+resume) leaves the OLD buildVersion in storage, the very next boot
 * re-detects saved≠running and re-prompts forever. This rewrites the stamp in place
 * so the mismatch is gone after a single resolution.
 *
 * Fail-safe: any storage error degrades to `false` (nothing written) rather than
 * throwing. Returns true if at least one savepoint slot was re-stamped.
 */
export function restampSavepointsBuildVersion(
  storage: StorageLike,
  runningVersion: string
): boolean {
  if (!runningVersion) return false;
  let wrote = false;
  // Cover the live slots boot actually reads (0..SAVEPOINT_CAP), which includes
  // the rolling autosave savepoint.
  for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
    try {
      const raw = storage.getItem(savepointKey(slot));
      if (!raw) continue;
      const sp = JSON.parse(decode(raw)) as Savepoint;
      if (!sp || typeof sp !== 'object') continue;
      if (sp.buildVersion === runningVersion) continue; // already current
      sp.buildVersion = runningVersion;
      const result = safeSetItem(storage, savepointKey(slot), encode(JSON.stringify(sp)));
      wrote = wrote || result.ok;
    } catch {
      // Corrupt slot / quota — skip; never throw out of a resolution handler.
      continue;
    }
  }
  return wrote;
}

/**
 * BUG-603 (P1, data-loss class; Aaron ruling Q100079=A, TIGHTENED after an
 * independent REJECT round): `lastFlows` only refreshes on a real tick. A
 * savepoint whose journal tail — or whose baked-in SNAPSHOT — ends on a
 * discretionary, non-tick action (a policy toggle, a tax-rate change, a
 * build/demolish) leaves the PER-LINE `lastFlows` entries (Council Tax,
 * Business Tax, Wages, Upkeep) describing the PRE-action world while every
 * other field (taxRates, policies, buildings) already reflects the action.
 * `runConsistencyChecks`' flows-vs-recompute layer (`flows.*-matches`)
 * always recomputes those specific lines LIVE from current state, so they
 * then diverge from the now-stale stored values and the whole savepoint is
 * rejected as "inconsistent" — a false-positive rejection that loses a save
 * that was never actually broken.
 *
 * THE KEY SPLIT (the first attempt at this fix got this wrong and was
 * REJECTed for it): `lastFlows` + the funds triplet (fundsAtTickStart/End)
 * are the LAST TICK's historical story. A post-tick policy toggle changes
 * NEITHER of them — conservation (consistency.ts #1, fundsAtTickEnd ===
 * fundsAtTickStart + Σinflows − Σoutflows) NEVER legitimately goes stale
 * from a discretionary action, because it is checked against what actually
 * happened at tick time, not against current policy. Only the four PER-LINE
 * checks go stale, because they compare stored history against a LIVE
 * recompute of CURRENT policies/taxRates/buildings.
 *
 * So this NEVER derives or touches fundsAtTickStart/fundsAtTickEnd (an
 * earlier version of this fix back-derived fundsAtTickStart from the new
 * flow sums to keep conservation "true" — that made conservation a
 * tautology against ANY value of fundsAtTickEnd, so a hand-tampered
 * fundsAtTickEnd (+1,000,000 / −500,000) was silently laundered through the
 * retry. Conservation must always see the REAL stored funds triplet, so a
 * tampered one still fails no matter what else is retried).
 *
 * Ruling: on restore, if the raw check fails, retry ONLY the per-line checks
 * against flows recomputed fresh via the exact same SSOT pipeline
 * (`engine.computeFlows`) the next real tick would use — passed to
 * `runConsistencyChecks` as `actualFlowsOverride`, which conservation and
 * every other check (shape validation, palette, lastFlows shape/finite)
 * ignore. A genuinely corrupt save (bad building data, an out-of-range tax
 * rate, a duplicate id, a smuggled placeholder spec, a tampered funds
 * triplet) is untouched by this and still fails on retry — the check keeps
 * its teeth.
 *
 * NOT written into the restored state: the stored `lastFlows` stays as the
 * historical record it always was — the Flows UI is stale only until the
 * next tick, exactly as before this bug existed. Keeping the stored triplet
 * untouched and re-deriving the override fresh on every restore attempt
 * means a SECOND restore of the same (still-unticked) state goes through
 * this identical, non-laundering retry again — never a "fixed" value baked
 * in that could itself drift from a later tampering.
 */
function recomputeFlowsOverride(state: SimState): RecomputedFlowsOverride {
  const { inflows, outflows } = computeFlows(state);
  return { inflows, outflows, population: state.population };
}

/**
 * Run the consistency gate, and ONLY if it fails, retry once passing a fresh
 * `actualFlowsOverride` (recomputeFlowsOverride) so ONLY the four per-line
 * policy-sensitive checks re-evaluate against current-state flows;
 * conservation and every other check keep reading the real, untouched state
 * both times. The returned state is ALWAYS the input state, unmodified —
 * this function only decides which report to trust.
 *
 * A state whose lastFlows was already fresh (the overwhelmingly common
 * case — a tick just ran, nothing discretionary happened after it) never
 * even reaches the retry, so its report is byte-identical to
 * runConsistencyChecks(state) alone — no behaviour change for the common
 * path.
 *
 * A failure the override does NOT fix (a duplicate building id, an
 * out-of-range tax rate, a smuggled placeholder spec, a non-finite funds
 * value, or — the tampered-funds attack this split exists to stop — a
 * fundsAtTickEnd/fundsAtTickStart hand-edited beyond what the REAL stored
 * lastFlows accounts for) still fails on the retry exactly as it did before
 * this fix, because conservation is never handed the override.
 */
export function checkConsistencyRecoveringStaleFlows(state: SimState): ConsistencyReport {
  const report = runConsistencyChecks(state);
  if (report.failures === 0) return report;
  const override = recomputeFlowsOverride(state);
  const retryReport = runConsistencyChecks(state, override);
  return retryReport.failures === 0 ? retryReport : report;
}

/**
 * BUG-617 (P1, 2026-09-03, Aaron's live city hard-wedged 20+ minutes on load):
 * the ORIGINAL root cause was a fixed-size chunk on the cross-build GENESIS
 * replay path (fixed — see genesisReplay.ts's CHUNK_TIME_BUDGET_MS). The
 * REAL-WORLD shape then turned out worse: an 8MB debugQueue blew the
 * localStorage quota (a BUG-607-class silent autosave failure), so
 * `persistSavepoint` kept REJECTING every autosave for hours — `lastSaveIndex`
 * (store.tsx) only advances on a SUCCESSFUL persist, so the on-disk
 * savepoint's `snapshot` stayed a STALE, SMALL city while its `journalTail`
 * (computed fresh every failed attempt against that same stuck
 * `lastSaveIndex`) grew to thousands of actions — hundreds of KB of `place`/
 * `tick` entries needed to grow that small snapshot back up to the real
 * 13,000-building city. `restoreFromSavepoint`'s tail-replay loop below has
 * NO chunking at all (unlike the genesis path) and runs SYNCHRONOUSLY inside
 * store.tsx's `useState` lazy boot initializer — every one of those thousands
 * of `reducer()` calls, each paying real per-building work (flows/monitors/
 * road-connectivity) at whatever building count the tail has grown the city
 * to by that point, blocks the very first render.
 *
 * FIX: `restoreFromSavepoint` ITSELF is UNCHANGED (every existing caller/test
 * keeps its exact synchronous, all-tail-replayed contract — small tails are
 * still the overwhelming common case and stay exactly as fast as before).
 * For the LARGE-tail case, store.tsx instead: (1) calls
 * `prepareRestoreForChunkedTail` to get the pre-tail snapshot state
 * SYNCHRONOUSLY (fast — no tail loop) as the initial boot state, so the app
 * mounts immediately; (2) drives `replayTailChunked` from a `useEffect`
 * (mirroring the existing onRebuild chunked-generator pattern exactly,
 * including its progress/ETA/watchdog/generation-guard machinery and the
 * SAME RebuildPrompt overlay, `kind: 'load'`) to replay the tail with the
 * main thread yielding between chunks; (3) once complete, applies the result
 * via `hydrate` and IMMEDIATELY persists a FRESH savepoint with an EMPTY tail
 * (self-healing — the NEXT boot then has nothing large to replay, rescuing
 * the city permanently after one successful chunked load).
 *
 * `LARGE_TAIL_REPLAY_THRESHOLD` actions is the cut line between "replay
 * inline, synchronously, exactly as always" and "replay chunked, behind a
 * progress overlay" — chosen well above any tail a HEALTHY autosave cadence
 * (AUTOSAVE_INTERVAL_MS = 30s) would ever accumulate, so normal play is
 * COMPLETELY unaffected; it only engages for the pathological
 * silent-autosave-failure shape this bug documents.
 */
export const LARGE_TAIL_REPLAY_THRESHOLD = 150;

/**
 * Prepare a savepoint restore up to (but NOT including) the tail replay:
 * reads the most recent savepoint, strips any smuggled placeholder building,
 * fixes `nextId`, and runs the BEFORE-replay consistency gate — everything
 * `restoreFromSavepoint` does before its tail loop. Returns the tail
 * UN-REPLAYED so a large one can be replayed CHUNKED by the caller (BUG-617)
 * instead of blocking the synchronous boot path. Fail-safe: any error
 * degrades to `{ success: false }`, exactly like `restoreFromSavepoint`.
 */
export function prepareRestoreForChunkedTail(storage: StorageLike): {
  success: boolean;
  state?: SimState;
  tail?: JournalEntry[];
  buildVersion?: string;
  camera?: MapViewState | null;
  reason?: string;
} {
  try {
    const savepoints = readAllSavepoints(storage);
    if (savepoints.length === 0) {
      return { success: false, reason: 'No savepoint found' };
    }
    const most = mostRecentSavepoint(savepoints);
    if (!most) {
      return { success: false, reason: 'No valid savepoint found' };
    }

    let state = most.snapshot;
    {
      const clean = state.buildings.filter((b) => SPECS[b.spec]?.placeholder !== true);
      if (clean.length !== state.buildings.length) {
        state = { ...state, buildings: clean };
      }
    }
    state = { ...state, nextId: nextSafeBuildingId(state.buildings) };

    const beforeReport = checkConsistencyRecoveringStaleFlows(state);
    if (beforeReport.failures > 0) {
      return {
        success: false,
        reason: `Snapshot failed consistency (${beforeReport.failures} failures)`,
      };
    }

    return {
      success: true,
      state,
      tail: most.journalTail,
      buildVersion: most.buildVersion,
      camera: most.camera ?? null,
    };
  } catch (e) {
    return { success: false, reason: `Restore error: ${String(e)}` };
  }
}

/**
 * BUG-617: chunk boundary tuning for `replayTailChunked` — identical
 * reasoning to genesisReplay.ts's ACTIONS_PER_CHUNK/CHUNK_TIME_BUDGET_MS
 * (a fixed action-count alone under-chunks an expensive tail at high
 * building count; a chunk ends at whichever of the two bounds comes first).
 * Kept as a SEPARATE constant from genesisReplay.ts's (rather than shared)
 * because the two replay paths are independent BUG-617 fixes for two
 * different real-world shapes — tuning one must not silently retune the other.
 */
const TAIL_ACTIONS_PER_CHUNK = 50;
const TAIL_CHUNK_TIME_BUDGET_MS = 40;

/**
 * Chunked, STRICT (non-defensive) replay of a savepoint's journal tail onto
 * an already-prepared snapshot state. Unlike genesisReplay's chunked
 * replayer, an action that THROWS is NOT skipped-and-logged — it propagates
 * out of the generator, exactly matching `restoreFromSavepoint`'s existing
 * fail-fast contract (a tail is same-build history, not a cross-build
 * rules-change replay, so an action failing here is real corruption, not an
 * expected rules divergence). Callers should wrap `gen.next()` calls in
 * try/catch and treat a thrown error as a restore failure.
 *
 * Purity note (mirrors genesisReplay.ts's CHUNK_TIME_BUDGET_MS comment):
 * performance.now() decides ONLY the chunk boundary, never which actions are
 * applied or in what order — the resulting STATE is chunking-invariant.
 *
 * F1 FIX (independent round REJECT, 2026-09-03): an earlier version of this
 * function wrapped the loop in `setReplayMode(true)` and did a single
 * roadConnectivity recompute at the end (mirroring genesisReplay.ts's chunked
 * genesis replay), on the premise that "nothing reads `state.roadConnectivity`
 * BETWEEN actions during a replay". That premise is FALSE for a savepoint
 * TAIL: BUG-606 added the `resolveDemand`/`resolveDemandAll` reducer cases,
 * which derive their build plan from `demandFixPlan(state)` ->
 * `serviceCoverageOf(state)` -> `isOnline(state, b)`, which reads
 * `state.roadConnectivity` DIRECTLY, INSIDE the reducer, between actions. The
 * independent round's A2 attack proved the divergence directly: a
 * `[place road][resolveDemand gp]` tail — the ordinary "finish the road, then
 * click Fix" player sequence — makes the CHUNKED replay see a STALE
 * (pre-road) connectivity graph and build an extra clinic the SYNCHRONOUS
 * `restoreFromSavepoint` loop never would. `setReplayMode` was DROPPED
 * entirely rather than patched at the resolveDemand call sites (which would
 * touch engine.ts) — measured cost of a plain per-action recompute at
 * 13,000-building scale is only ~2-6ms/action on top of the already-dominant
 * monthly-boundary sweep cost (see scale-gate.test.mjs's LOAD_CHUNK_BOUND_MS
 * derivation), i.e. affordable, and this now runs the IDENTICAL reducer path
 * `restoreFromSavepoint`'s own unchunked loop runs — byte-identity is
 * TAUTOLOGICAL (same function, same inputs, same order), not an argued
 * property. (genesisReplay.ts's chunked GENESIS replay has the SAME stale
 * premise, pre-existing and outside this fix's diff — flagged as a separate
 * follow-up finding, not fixed here.)
 */
export function* replayTailChunked(
  initialTailState: SimState,
  tail: JournalEntry[]
): Generator<ReplayProgress, { state: SimState; replayed: number }, void> {
  let state = initialTailState;
  const total = tail.length;
  let i = 0;
  while (i < total) {
    const chunkClockStart = performance.now();
    let processedInChunk = 0;
    while (i < total && processedInChunk < TAIL_ACTIONS_PER_CHUNK) {
      state = reducer(state, tail[i].action);
      i++;
      processedInChunk++;
      if (performance.now() - chunkClockStart >= TAIL_CHUNK_TIME_BUDGET_MS) break;
    }
    yield {
      actionsDone: i,
      actionsTotal: total,
      phaseLabel: `Replaying your recent actions... ${i.toLocaleString()}/${total.toLocaleString()} actions`,
    };
  }
  return { state, replayed: total };
}

/**
 * Restore from the most recent savepoint. Replays the journal tail to reconstruct
 * the end state, verifies consistency, and returns the restored state. Always fail-safe:
 * any error (missing savepoint, corrupt JSON, consistency check failure) returns
 * { success: false } and the app boots fresh.
 */
export function restoreFromSavepoint(storage: StorageLike): RestoreResult {
  try {
    const savepoints = readAllSavepoints(storage);
    if (savepoints.length === 0) {
      return { success: false, reason: 'No savepoint found' };
    }

    const most = mostRecentSavepoint(savepoints);
    if (!most) {
      return { success: false, reason: 'No valid savepoint found' };
    }

    // Start from the snapshot state.
    let state = most.snapshot;

    // FEAT-1972079877 — close the savepoint-restore path: a snapshot's buildings[]
    // is used VERBATIM (only the journal TAIL replays through the guarded reducer),
    // so a crafted/hand-edited savepoint could smuggle a placeholder ("coming soon"
    // roadmap type) building straight into the sim. Drop any placeholder-spec
    // building here (drop-and-continue, so a legit savepoint still loads) BEFORE the
    // consistency gate below — which independently flags placeholder buildings as a
    // failure (the universal catch), so even without this filter the restore would
    // be rejected rather than admit one.
    {
      const clean = state.buildings.filter((b) => SPECS[b.spec]?.placeholder !== true);
      if (clean.length !== state.buildings.length) {
        state = { ...state, buildings: clean };
      }
    }

    // BUG-413 FIX: Recalculate nextId to ensure it's > max existing building id.
    // This prevents collision when new buildings are placed after restore.
    // The saved nextId may be stale if journal replayed actions added buildings.
    state = { ...state, nextId: nextSafeBuildingId(state.buildings) };

    // Verify consistency BEFORE replay (snapshot should already be consistent).
    // BUG-603: a baked-in-snapshot autosave (a policy toggle etc. happened,
    // then autosave fired with no further journal tail) leaves this snapshot's
    // lastFlows stale relative to its own taxRates/policies/buildings — retry
    // with a recompute before failing (see checkConsistencyRecoveringStaleFlows).
    const beforeReport = checkConsistencyRecoveringStaleFlows(state);
    if (beforeReport.failures > 0) {
      return {
        success: false,
        reason: `Snapshot failed consistency (${beforeReport.failures} failures)`,
      };
    }

    // Replay the journal tail to reconstruct the final state.
    let replayed = 0;
    for (const entry of most.journalTail) {
      state = reducer(state, entry.action);
      replayed++;
    }

    // BUG-413 FIX: After replay, recalculate nextId again to ensure it accounts
    // for any buildings added during the replay.
    state = { ...state, nextId: nextSafeBuildingId(state.buildings) };

    // Verify consistency AFTER replay (should still be consistent after deterministic replay).
    // BUG-603: the journal tail can itself end on a non-tick action (e.g. tick,
    // then a policy toggle, then autosave) — retry with a recompute before
    // failing, exactly like the pre-replay gate above.
    const afterReport = checkConsistencyRecoveringStaleFlows(state);
    if (afterReport.failures > 0) {
      return {
        success: false,
        reason: `Replay failed consistency (${afterReport.failures} failures) after ${replayed} actions`,
      };
    }

    return { success: true, state, replayed };
  } catch (e) {
    // Catch-all for JSON.parse errors, missing fields, type mismatches, etc.
    return { success: false, reason: `Restore error: ${String(e)}` };
  }
}

/**
 * Create a new savepoint from current state and journalTail.
 *
 * The savepoint captures:
 * - snapshot: the full SimState at this moment (self-contained)
 * - journalTail: actions added AFTER this savepoint (for incremental recovery)
 *
 * DESIGN: The snapshot is a complete state. For autosave, we save every 30s and
 * the journalTail represents a small delta since the last savepoint. On restore,
 * we load the snapshot + replay its tail to reconstruct any actions taken after
 * the snapshot was saved.
 *
 * CALLER RESPONSIBILITY: Calculate the tail before calling this function.
 * Typically: tail = journal.entries.slice(lastSaveIndex) where lastSaveIndex
 * is tracked since the previous savepoint. Pass the tail here; this function
 * does NOT calculate it.
 *
 * Call this whenever the app wants to persist a recovery point (typically every
 * AUTOSAVE_INTERVAL_MS via a wall-clock timer in the React layer).
 */
export function createSavepoint(
  state: SimState,
  journalTail: JournalEntry[],
  now: Date = new Date(),
  buildVersion?: string,
  camera?: MapViewState | null
): Savepoint {
  return {
    savedAt: now.toISOString(),
    snapshotTick: state.tick,
    snapshot: state,
    journalTail, // Real tail: actions recorded since last snapshot
    // inc2: stamp the build the save was produced under + the current camera.
    // Both are optional; omitted fields simply don't serialize, so a legacy
    // reader (and needsRebuild) sees `undefined` and keeps old behaviour.
    ...(buildVersion ? { buildVersion } : {}),
    ...(camera ? { camera } : {}),
  };
}

/**
 * Initialise a fresh game session. Returns the initial state and an empty journal.
 * Used on app boot if no savepoint is available.
 */
export function freshStart(): { state: SimState; journal: Journal } {
  return {
    state: getInitialState(),
    journal: emptyJournal(),
  };
}
