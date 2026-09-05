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
import { SPECS, stampJobsGrandfather, stampJobsGrandfatherForce, needsJobsGrandfather } from './data.ts';
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
  /**
   * FEAT-2326609780 round 3 (the structural fix — adjudicated, not
   * optional): a MONOTONIC PER-LINEAGE counter, incremented once per persist
   * ATTEMPT (success or failure — see store.tsx's `nextSaveSeq()`) and
   * carried through every mirror (inc1 mirrorSaveCheckpoint,
   * mirrorSavepointDirect). Replaces `snapshotTick`+`savedAt` as the PRIMARY
   * freshness ordering between two savepoints of the SAME lineage
   * (localStorage vs. its IndexedDB mirror/rescue copy):
   *   - `snapshotTick` sits FLAT while the player places/demolishes without
   *     a tick advancing (only `{type:'tick'}` actions move it), so two
   *     savepoints minutes apart can tie on tick.
   *   - Building COUNT is not monotonic either (round 2's ATTACK G,
   *     adjudicated): bulldoze, forced asset sales (FEAT-1972079923 inc2),
   *     and the default-on consolidator's scrap-and-rebuild passes
   *     (FEAT-2326609761) all legitimately SHRINK a city that is still
   *     unambiguously the NEWER one — a building-count safety net refused a
   *     genuine rescue forever.
   *   - `savedAt` (wall-clock) is the round-2 R2-F1 trap: a `finish()`
   *     self-heal stamped with `new Date()` at equal tick always beat a
   *     genuinely-newer rescue that merely happened to be written a little
   *     earlier in wall-clock time but represents MORE of the lineage's
   *     real persist history.
   * `saveSeq` sits FLAT for none of these — it counts persist events, not
   * ticks, buildings, or wall-clock time, so it rises across every one of
   * the shapes above.
   *
   * Optional for backward tolerance: a savepoint written before this field
   * existed has no stamp. Per the adjudicated ruling, an absent `saveSeq` is
   * treated as `0` on BOTH sides of a comparison; if that leaves the two
   * sides tied (both absent, or an honest tie at a shared value), the
   * comparison falls back to the pre-round-3 `snapshotTick`+`savedAt` rule —
   * documented, not silent, and only ever a TIE-BREAK now, never primary.
   */
  saveSeq?: number;
  /**
   * P0 RCA fix (Aaron, 2026-09-04, item 1): copied automatically from
   * `snapshot.lineageId` by `createSavepoint` — see `SimState.lineageId`'s
   * own doc comment (types.ts) for the full mechanism. Absent = the
   * reserved `LEGACY_LINEAGE_ID` lineage (a save written before this field
   * existed) — `savepointKey` maps both the same way, so this is backward
   * tolerant with zero storage migration required.
   */
  lineageId?: string;
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
 * P0 RCA fix: the reserved lineage id for a save with no `lineageId` stamp
 * (every save written before this fix). Deliberately maps to the SAME
 * unnamespaced keys (`metropolis.savepoint.<slot>`) every save already used
 * — see `savepointKey`'s own comment — so an existing player's next boot
 * reads exactly what it does today, zero storage migration required.
 */
export const LEGACY_LINEAGE_ID = 'legacy';

/** The localStorage key holding which lineage id is "current" (the one boot should restore). Absent = LEGACY_LINEAGE_ID (item 5's default). */
export const CURRENT_LINEAGE_KEY = 'metropolis.currentLineage';

/** Read the current-lineage pointer, defaulting to the reserved legacy lineage (P0 fix item 5). Never throws. */
export function readCurrentLineageId(storage: StorageLike): string {
  try {
    const raw = storage.getItem(CURRENT_LINEAGE_KEY);
    return raw && raw.length > 0 ? raw : LEGACY_LINEAGE_ID;
  } catch {
    return LEGACY_LINEAGE_ID;
  }
}

/** Set the current-lineage pointer. Never throws. */
export function writeCurrentLineageId(storage: StorageLike, lineageId: string): void {
  try {
    storage.setItem(CURRENT_LINEAGE_KEY, lineageId);
  } catch {
    /* best-effort — a failure here just means the NEXT boot falls back to LEGACY_LINEAGE_ID */
  }
}

/**
 * Mint an opaque, per-city lineage id. Called ONLY from outside the pure
 * reducer (a boot-time fresh-city decision, `freshStart`, `loadDevCity1`) —
 * NEVER from inside `reducer()`/`rawState()` itself, which must stay
 * deterministic (GR#21): the 'reset' reducer case instead receives an
 * already-minted id on the dispatched action (see engine.ts's 'reset' case
 * and store.tsx's Start Over dispatch site), so replaying the SAME recorded
 * action always reproduces the SAME lineage id.
 */
export function mintLineageId(now: Date = new Date()): string {
  return `L${now.getTime().toString(36)}${Math.random().toString(36).slice(2, 10)}`;
}

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
 * Generate the localStorage key for the Nth savepoint slot (0-based),
 * optionally namespaced to a lineage (P0 RCA fix, item 2).
 *
 * `undefined` and `LEGACY_LINEAGE_ID` are DELIBERATELY THE SAME physical key
 * (`metropolis.savepoint.<slot>`, no lineage segment at all) — every save
 * ever written used this exact key, so treating the legacy lineage as
 * "no segment" rather than a literal `.legacy.` segment means an existing
 * player's saves are found at the SAME location with ZERO storage migration.
 * Any OTHER (minted) lineage id gets its own namespaced key
 * (`metropolis.savepoint.<lineageId>.<slot>`), which can never collide with
 * another lineage's slots — this is what makes the P0 fix's overwrite gate
 * (item 3) scoped-by-construction: two different lineages are never even
 * reading/writing the same key, so there is nothing to compare.
 */
function savepointKey(slot: number, lineageId?: string): string {
  if (!lineageId || lineageId === LEGACY_LINEAGE_ID) return `${SAVEPOINT_KEY_PREFIX}.${slot}`;
  return `${SAVEPOINT_KEY_PREFIX}.${lineageId}.${slot}`;
}

/**
 * Read a single savepoint slot. Fail-safe: a missing key, corrupt JSON, or a
 * parse error all degrade to `null` — never throws.
 */
function readSlot(storage: StorageLike, slot: number, lineageId?: string): Savepoint | null {
  try {
    const raw = storage.getItem(savepointKey(slot, lineageId));
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
export function readAllSavepoints(storage: StorageLike, now: Date = new Date(), lineageId?: string): Savepoint[] {
  const nowMs = now.getTime();
  const savepoints: Savepoint[] = [];
  for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
    const sp = readSlot(storage, slot, lineageId);
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

/** P0 RCA fix, item 3/4: why `persistSavepointWithReason` refused a write. */
export type SavepointRejectReason = 'stale-overwrite' | 'storage-error';

export interface PersistSavepointResult {
  ok: boolean;
  reason?: SavepointRejectReason;
  /**
   * BUG-436 round-4 fix (F-R3-2): per-slot re-stamp failures from a forced
   * write's history walk (see `PersistSavepointOptions.force`) that survived
   * an internal retry. Present ONLY when `ok:true` and the walk was
   * PARTIALLY successful — the primary requested write still landed (that
   * is what `ok:true` reports), but one or more OTHER occupied slots could
   * not be re-stamped below the new lineage-authority ceiling, so a future
   * ordinary (unforced) autosave MAY fall back to the tick comparison
   * against that stale slot and be refused. Never set on the unforced path.
   * Callers that care (the rebuild completion path) must surface this
   * honestly rather than reporting unqualified success — reporting a
   * partial failure beats leaving the player to discover it as a
   * permanently-refused autosave with no signal (RR-1/F-R3-2).
   */
  restampFailures?: Array<{ slot: number; reason: string }>;
}

/**
 * Is `incoming` at least as new as `existing`? PRIMARY: `saveSeq` (the
 * monotonic per-lineage persist counter, FEAT-2326609780 round 3) when BOTH
 * carry one — mirrors store.tsx's `isStrictlyFresherSavepointMeta` and
 * saveStore.ts's `guardedSavepointSetItem` exactly, so all three of this
 * estate's freshness gates agree. Falls back to the original BUG-469 rule
 * (tick, then `savedAt` as the `>=` tie-break) when either side lacks a
 * `saveSeq` — the common case for anything written before round 3, and the
 * exact comparison the RCA's own repro fixtures exercise (they never stamp
 * `saveSeq`), so this fallback is what keeps their assertions unchanged.
 */
function isIncomingSavepointNewerOrEqual(incoming: Savepoint, existing: Savepoint): boolean {
  const incSeq = incoming.saveSeq;
  const exSeq = existing.saveSeq;
  if (Number.isFinite(incSeq) && Number.isFinite(exSeq)) {
    if (incSeq !== exSeq) return (incSeq as number) > (exSeq as number);
    // tied on a REAL saveSeq — fall through to the tick+savedAt tie-break below
  }
  return incoming.snapshotTick > existing.snapshotTick || (incoming.snapshotTick === existing.snapshotTick && incoming.savedAt >= existing.savedAt);
}

/**
 * Persist a new savepoint, rotating slots so a bounded HISTORY of the newest
 * SAVEPOINT_CAP autosaves is kept (BUG-469) — never a single overwritten slot.
 *
 * P0 RCA fix (Aaron, 2026-09-04 — "I created a whole new map city 13...yet I
 * saved and started a new map" resurrected the OLD city): the rotation slots
 * are now namespaced by `savepoint.lineageId` (item 2's `savepointKey`) —
 * `undefined`/`LEGACY_LINEAGE_ID` map to the SAME unnamespaced keys every
 * save has always used (zero migration), any OTHER lineage id gets its own
 * physically separate keyspace. This makes the overwrite gate below (item 3)
 * SCOPED BY CONSTRUCTION: two different lineages' savepoints are never even
 * read from the same key, so a foreign-lineage save can never again be
 * treated as a competitor — closing the exact mechanism the RCA proved
 * (a brand-new low-tick city could never beat an old high-tick one occupying
 * the SAME global slot).
 *
 * Target-slot choice (no separate "next slot" cursor — GR#3, the slots
 * themselves are the only source of truth, WITHIN one lineage's namespace):
 * prefer an empty slot; if all SAVEPOINT_CAP slots are occupied, overwrite
 * the OLDEST occupied one. That is exactly a rotating history — the newest
 * SAVEPOINT_CAP autosaves of THIS lineage always survive, the single oldest
 * one rolls off.
 *
 * Overwrite protection (BUG-469, extended round 3): before overwriting an
 * occupied slot, the incoming savepoint must be at least as new as what is
 * already there by `isIncomingSavepointNewerOrEqual` (saveSeq-primary,
 * tick+savedAt fallback) — now ONLY ever compared against another savepoint
 * of the SAME lineage. A stale/older writer is REJECTED (`ok:false`,
 * `reason:'stale-overwrite'`) rather than clobbering a fresher save of that
 * lineage. The prior good savepoint is left untouched.
 *
 * BUG-469: also purges any autosave older than AUTOSAVE_RETENTION_MS before
 * picking a target slot, so long-idle dev installs don't accumulate
 * autosaves forever (named saves are a separate mechanism and are never
 * touched here).
 *
 * Fail-safe: localStorage errors are caught and reported as
 * `reason:'storage-error'` rather than thrown; the app continues without the
 * savepoint. `persistSavepoint` (below) is the pre-existing boolean-only
 * contract every other caller in this codebase already uses; new call sites
 * that need to distinguish WHY a save was refused (item 4 — a refusal must
 * surface loudly, not the quiet autosave dot) use
 * `persistSavepointWithReason` directly.
 */
export interface PersistSavepointOptions {
  /**
   * BUG-436 round F1/F2 fix (lead ruling): the rebuild boundary is a
   * DELIBERATE replace-the-city event, not a competing autosave — a legacy
   * install's freshness gate falling back to tick comparison (neither side
   * carries a `saveSeq`) refuses a perfectly healthy rebuild whose tick is
   * lower ONLY because journal-cap eviction rolled off the ticks in between
   * (see the class doc comment above and ATTACK 1's F1 repro in
   * attack-bug-436-round.test.mjs). `force: true` skips the
   * `isIncomingSavepointNewerOrEqual` staleness check for an OCCUPIED slot
   * entirely and instead MINTS a coherent `saveSeq` that is guaranteed to
   * supersede every occupied slot of this lineage — never overriding an
   * already-higher explicit `saveSeq` the caller supplied. This is a
   * deliberate lineage-authority stamp, not a silent bypass: the resulting
   * savepoint is still internally consistent with the lineage's own
   * `saveSeq` numbering, so every OTHER freshness comparison in the estate
   * (the IDB boot-swap, the next ordinary autosave) still orders correctly
   * against it. Reserved for the rebuild call site ONLY — every other
   * persist call site (autosave/save/self-heal/load) must keep `force`
   * unset so a genuinely stale write is still rejected.
   */
  force?: boolean;
}

export function persistSavepointWithReason(
  storage: StorageLike,
  savepoint: Savepoint,
  now: Date = new Date(),
  opts?: PersistSavepointOptions
): PersistSavepointResult {
  try {
    // F2 fix (P0 lineage round): a savepoint arriving here with NO lineageId
    // (e.g. a genesis rebuild — replayFromGenesis starts at initialState()
    // and only ever recovers a lineage from a JOURNALLED reset entry, which
    // JOURNAL_CAP can roll off) is NOT necessarily a legacy save — lineage is
    // an identity of the CITY, not of the journal contents. Stamp it to
    // whatever `storage` says is the CURRENT lineage before it is ever keyed,
    // so a rebuild (or any other caller that forgot to carry the id forward)
    // lands in the live lineage's own slots instead of clobbering the
    // legacy keyspace. Only fills a genuine gap — never overrides an
    // explicit lineageId already on the savepoint — and never stamps when
    // the ambient current lineage IS legacy (a real legacy save stays
    // legacy, unchanged from prior behaviour).
    if (!savepoint.lineageId) {
      const ambientLineageId = readCurrentLineageId(storage);
      if (ambientLineageId !== LEGACY_LINEAGE_ID) {
        savepoint.lineageId = ambientLineageId;
      }
    }
    const lineageId = savepoint.lineageId;
    for (let slot = SAVEPOINT_CAP; slot < 8; slot++) {
      try {
        storage.removeItem(savepointKey(slot, lineageId));
      } catch {
        /* leftover slots from older caps */
      }
    }

    const nowMs = now.getTime();
    const slots: Array<{ slot: number; sp: Savepoint | null }> = [];
    for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
      let sp = readSlot(storage, slot, lineageId);
      // BUG-469: purge-on-write — drop autosaves older than the retention
      // window so a slot they occupy is free for rotation again.
      if (sp && isStaleAutosave(sp, nowMs)) {
        try {
          storage.removeItem(savepointKey(slot, lineageId));
        } catch {
          /* ignore — worst case the stale slot lingers, filtered on read */
        }
        sp = null;
      }
      slots.push({ slot, sp });
    }

    const emptySlot = slots.find((s) => s.sp === null);
    const target = emptySlot ?? slots.reduce((oldest, s) => (s.sp!.savedAt < oldest.sp!.savedAt ? s : oldest));

    const restampFailures: Array<{ slot: number; reason: string }> = [];

    if (opts?.force) {
      // Round-4 fix (F-R3-1/F-R3-1b): this block used to live INSIDE
      // `if (target.sp)`, so a FREE or torn target slot (reads as `sp ===
      // null`, see `emptySlot` above) let the unforced write succeed
      // outright and skipped the mint + re-stamp walk entirely — the other
      // occupied slots kept their legacy shape and every subsequent
      // autosave was refused forever (RR-1, one layer down). The walk now
      // runs on `opts?.force` alone, regardless of which slot the write
      // lands in.
      //
      // F1/F2: mint a coherent saveSeq that supersedes every occupied slot
      // of this lineage — the rebuilt city IS the new lineage authority by
      // construction. Never lowers an already-higher explicit saveSeq.
      const maxOccupiedSeq = slots.reduce((m, s) => {
        const seq = s.sp?.saveSeq;
        return Number.isFinite(seq) && (seq as number) > m ? (seq as number) : m;
      }, 0);
      if (!Number.isFinite(savepoint.saveSeq) || (savepoint.saveSeq as number) <= maxOccupiedSeq) {
        savepoint.saveSeq = maxOccupiedSeq + 1;
      }
      // RR-1 (re-round 3, P1): minting a seq for the TARGET slot alone is
      // not enough — the other occupied slots of this lineage can still be
      // legacy entries with NO saveSeq at a HIGH tick, so the next ordinary
      // autosave would fall back to the tick comparison against one of
      // THOSE slots and be refused forever. RE-STAMP the surviving slots'
      // `saveSeq` DOWN below the newly minted ceiling — content untouched,
      // only `saveSeq` moves, oldest-savedAt gets the lowest re-stamped seq.
      const others = slots
        .filter((s) => s.slot !== target.slot && s.sp)
        .sort((a, b) => (a.sp!.savedAt < b.sp!.savedAt ? -1 : a.sp!.savedAt > b.sp!.savedAt ? 1 : 0));
      let ceiling = savepoint.saveSeq as number;
      for (let i = others.length - 1; i >= 0; i--) {
        const o = others[i];
        const sp = o.sp!;
        if (!Number.isFinite(sp.saveSeq) || (sp.saveSeq as number) >= ceiling) {
          const restamped: Savepoint = { ...sp, saveSeq: ceiling - 1 };
          const key = savepointKey(o.slot, lineageId);
          const encoded = encode(JSON.stringify(restamped));
          let res = safeSetItem(storage, key, encoded);
          if (!res.ok) {
            // F-R3-2 fix: a bare `if (res.ok)` here used to swallow the
            // failure completely — no record, no retry — leaving this slot
            // permanently un-restamped and the install in the exact RR-1
            // "autosaves refused forever" shape, SILENTLY. The purge-on-write
            // step earlier in this same call may have just freed slots, and
            // browser quota conditions are frequently transient, so retry
            // ONCE before treating this slot as a genuine failure.
            res = safeSetItem(storage, key, encoded);
          }
          if (res.ok) {
            ceiling = restamped.saveSeq as number;
          } else {
            restampFailures.push({ slot: o.slot, reason: res.error ?? 'storage-error' });
          }
        } else {
          ceiling = sp.saveSeq as number;
        }
      }
    } else if (target.sp && !isIncomingSavepointNewerOrEqual(savepoint, target.sp)) {
      // BUG-469 overwrite protection: reject the stale write outright.
      // The prior (fresher, SAME-lineage) savepoint in this slot is left intact.
      return { ok: false, reason: 'stale-overwrite' };
    }

    // BUG-457: route through the shared quota-safe helper instead of a bare
    // setItem — the outer try/catch still covers readSlot/removeItem.
    // FEAT-1972079935: encode() compresses the (large) serialized savepoint
    // before it hits localStorage — smaller payload, same quota-safe path.
    const result = safeSetItem(storage, savepointKey(target.slot, lineageId), encode(JSON.stringify(savepoint)));
    if (!result.ok) return { ok: false, reason: 'storage-error' };
    return restampFailures.length > 0 ? { ok: true, restampFailures } : { ok: true };
  } catch {
    return { ok: false, reason: 'storage-error' };
  }
}

/**
 * Backward-compatible boolean contract — every pre-existing call site in
 * this codebase uses this. Returns whether the save succeeded (true =
 * persisted, false = failed OR protected-against-stale-overwrite). See
 * `persistSavepointWithReason` for the full doc comment and the reason-
 * carrying variant new call sites (item 4) should prefer.
 */
export function persistSavepoint(
  storage: StorageLike,
  savepoint: Savepoint,
  now: Date = new Date()
): boolean {
  return persistSavepointWithReason(storage, savepoint, now).ok;
}

/**
 * BUG-436 round F1/F2 fix (lead ruling): force-write a savepoint at a
 * deliberate replace-the-city boundary (the rebuild boundary — see
 * `PersistSavepointOptions.force`'s doc comment). Equivalent to
 * `persistSavepointWithReason(storage, savepoint, now, { force: true })`,
 * exposed as its own named entry point so a call site never has to spell
 * out `{ force: true }` inline — the name alone documents that this is the
 * narrow, sanctioned bypass, not a generic option any caller can reach for.
 * Still fails closed on a genuine storage error (`reason: 'storage-error'`)
 * — forcing only skips the STALENESS comparison, never the write itself.
 */
export function persistSavepointForced(
  storage: StorageLike,
  savepoint: Savepoint,
  now: Date = new Date()
): PersistSavepointResult {
  return persistSavepointWithReason(storage, savepoint, now, { force: true });
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
  runningVersion: string,
  lineageId?: string
): boolean {
  if (!runningVersion) return false;
  // F3 fix (P0 lineage round, BUG-468 regression): a caller that does not
  // (or cannot) know the lineage explicitly must still land on the RIGHT
  // keyspace — default to whatever `storage` itself says is current, rather
  // than silently falling back to the (for a namespaced player, empty)
  // legacy slots. `readCurrentLineageId` already normalises an absent
  // pointer to `LEGACY_LINEAGE_ID`, which `savepointKey` treats identically
  // to `undefined`, so this is a no-op for a genuinely legacy player.
  const effectiveLineageId = lineageId ?? readCurrentLineageId(storage);
  let wrote = false;
  // Cover the live slots boot actually reads (0..SAVEPOINT_CAP), which includes
  // the rolling autosave savepoint.
  for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
    try {
      const raw = storage.getItem(savepointKey(slot, effectiveLineageId));
      if (!raw) continue;
      const sp = JSON.parse(decode(raw)) as Savepoint;
      if (!sp || typeof sp !== 'object') continue;
      if (sp.buildVersion === runningVersion) continue; // already current
      sp.buildVersion = runningVersion;
      const result = safeSetItem(storage, savepointKey(slot, effectiveLineageId), encode(JSON.stringify(sp)));
      wrote = wrote || result.ok;
    } catch {
      // Corrupt slot / quota — skip; never throw out of a resolution handler.
      continue;
    }
  }
  return wrote;
}

/**
 * P0 RCA fix, item 5 (MIGRATION): a savepoint written before lineage
 * identity existed has no `lineageId` field, but ALREADY reads/writes as the
 * reserved `LEGACY_LINEAGE_ID` lineage (`savepointKey`'s undefined-maps-to-
 * unnamespaced behaviour) — so this migration is NOT required for correct
 * behaviour, only for HONESTY: it rewrites each legacy slot IN PLACE (same
 * physical key, mirrors `restampSavepointsBuildVersion`'s own idiom exactly)
 * to carry an explicit `lineageId: 'legacy'` stamp, so a future reader never
 * has to guess whether "no field" means "pre-fix" or "a bug". Idempotent
 * (skips a slot that already carries the stamp) and fail-safe (a corrupt
 * slot / quota error is skipped, never thrown).
 */
export function migrateLegacySavepointsInPlace(storage: StorageLike): boolean {
  let wrote = false;
  for (let slot = 0; slot < SAVEPOINT_CAP; slot++) {
    try {
      const raw = storage.getItem(savepointKey(slot)); // unnamespaced = legacy
      if (!raw) continue;
      const sp = JSON.parse(decode(raw)) as Savepoint;
      if (!sp || typeof sp !== 'object') continue;
      if (sp.lineageId === LEGACY_LINEAGE_ID) continue; // already stamped
      sp.lineageId = LEGACY_LINEAGE_ID;
      const result = safeSetItem(storage, savepointKey(slot), encode(JSON.stringify(sp)));
      wrote = wrote || result.ok;
    } catch {
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
export function prepareRestoreForChunkedTail(storage: StorageLike, lineageId?: string): {
  success: boolean;
  state?: SimState;
  tail?: JournalEntry[];
  buildVersion?: string;
  camera?: MapViewState | null;
  reason?: string;
  /**
   * BUG-652 GRANDFATHERING, ROUND r3 FIX (F4, 2026-09-04): true when the RAW
   * snapshot (before the early stamp below ran) predated
   * JOBS_GRANDFATHER_ECONOMY_EPOCH — captured HERE, from the pre-stamp
   * state, because the early stamp below immediately bumps `economyEpoch` to
   * current, so re-deriving this from the (now-current) returned `state`
   * later would always read false and miss the tail entirely (round r2's
   * exact F4 failure mode). The caller MUST thread this through to
   * replayTailChunked() so it can run a stampJobsGrandfatherForce() catch-up
   * pass AFTER the chunked tail finishes — a tail-created building of one of
   * the six BUG-652 specs is otherwise never grandfathered, because it did
   * not exist yet at the point this function's own early stamp ran.
   */
  needsJobsGrandfatherCatchUp?: boolean;
} {
  try {
    const savepoints = readAllSavepoints(storage, new Date(), lineageId);
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

    // BUG-652 GRANDFATHERING: ONE-TIME EARLY migration of this snapshot's
    // PRE-EXISTING buildings — capture the pre-stamp decision FIRST (see the
    // `needsJobsGrandfatherCatchUp` field doc above), then stamp so the
    // snapshot's own buildings are correctly pinned immediately (this half
    // of the fix is unchanged from r2 and was independently verified sound).
    // Idempotent + a no-op once the state is already at the current epoch.
    const needsJobsGrandfatherCatchUp = needsJobsGrandfather(state);
    state = stampJobsGrandfather(state);

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
      needsJobsGrandfatherCatchUp,
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
  tail: JournalEntry[],
  /**
   * BUG-652 GRANDFATHERING, ROUND r3 FIX (F4): pass
   * `prepareRestoreForChunkedTail()`'s own `needsJobsGrandfatherCatchUp`
   * straight through. When true, a stampJobsGrandfatherForce() catch-up pass
   * runs once the tail is fully replayed, so a tail-created instance of one
   * of the six BUG-652 specs — which did not exist yet when
   * prepareRestoreForChunkedTail's own early stamp ran, and therefore could
   * not have been caught by it — is grandfathered too. Uses the FORCE
   * (unconditional) variant, not stampJobsGrandfather(), because
   * `initialTailState.economyEpoch` was ALREADY bumped to current by that
   * early stamp — the epoch-gated public function would see "already
   * current" and skip, silently reproducing the exact bug this fixes.
   */
  needsJobsGrandfatherCatchUp = false
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
  if (needsJobsGrandfatherCatchUp) state = stampJobsGrandfatherForce(state);
  return { state, replayed: total };
}

/**
 * Restore from the most recent savepoint. Replays the journal tail to reconstruct
 * the end state, verifies consistency, and returns the restored state. Always fail-safe:
 * any error (missing savepoint, corrupt JSON, consistency check failure) returns
 * { success: false } and the app boots fresh.
 */
export function restoreFromSavepoint(storage: StorageLike, lineageId?: string): RestoreResult {
  try {
    const savepoints = readAllSavepoints(storage, new Date(), lineageId);
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

    // BUG-652 GRANDFATHERING, ROUND r3 FIX (F4, 2026-09-04): capture whether
    // the RAW snapshot predates the migration BEFORE stamping — the stamp
    // immediately bumps `economyEpoch` to current, so re-deriving this
    // decision afterward (e.g. from the post-tail state) would always read
    // "already current" and silently skip a tail-created building of one of
    // the six BUG-652 specs (round r2's exact F4 finding). Stamp the
    // snapshot's OWN pre-existing buildings now (proven sound in r2 —
    // this half was never the defect) so the BEFORE-replay consistency check
    // below runs against an economically-coherent state; the captured
    // boolean drives a stampJobsGrandfatherForce() CATCH-UP pass after the
    // tail has fully replayed (below), which is the part r2 was missing.
    const needsJobsGrandfatherCatchUp = needsJobsGrandfather(state);
    state = stampJobsGrandfather(state);

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

    // BUG-652 GRANDFATHERING CATCH-UP (F4 fix): a tail-created instance of
    // one of the six BUG-652 specs did not exist yet when the early stamp
    // above ran, so it could not have been caught by it — this pass catches
    // it now that the tail is fully replayed. Uses the FORCE (unconditional)
    // variant, never stampJobsGrandfather() itself, because `state.
    // economyEpoch` was ALREADY bumped to current by the early stamp; the
    // epoch-gated public function would see "already current" and no-op.
    if (needsJobsGrandfatherCatchUp) state = stampJobsGrandfatherForce(state);

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
  camera?: MapViewState | null,
  saveSeq?: number
): Savepoint {
  return {
    savedAt: now.toISOString(),
    snapshotTick: state.tick,
    snapshot: state,
    journalTail, // Real tail: actions recorded since last snapshot
    // inc2: stamp the build the save was produced under + the current camera.
    // round 3: stamp the monotonic per-lineage saveSeq (see the Savepoint
    // field's own doc comment). All three optional; an omitted field simply
    // doesn't serialize, so a legacy reader sees `undefined` and keeps old
    // behaviour (needsRebuild / the saveSeq-absent fallback rule).
    ...(buildVersion ? { buildVersion } : {}),
    ...(camera ? { camera } : {}),
    ...(saveSeq !== undefined ? { saveSeq } : {}),
    // P0 RCA fix, item 1: lineage identity is carried on SimState itself
    // (types.ts's SimState.lineageId) and copied onto every Savepoint
    // AUTOMATICALLY here — no call site anywhere in the app needs to pass
    // it explicitly; it simply rides whatever `state` is being saved.
    ...(state.lineageId ? { lineageId: state.lineageId } : {}),
  };
}

/**
 * Initialise a fresh game session. Returns the initial state and an empty journal.
 * Used on app boot if no savepoint is available.
 */
export function freshStart(): { state: SimState; journal: Journal } {
  // P0 RCA fix, item 1: this is a genesis point, called directly by app code
  // (never by the pure reducer), so minting a lineage id here is safe.
  return {
    state: { ...getInitialState(), lineageId: mintLineageId() },
    journal: emptyJournal(),
  };
}
