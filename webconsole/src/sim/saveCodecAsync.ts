// saveCodecAsync.ts — BUG-798: off-main-thread wrapper around saveCodec.ts's
// LZ compression step.
//
// WHY: on Aaron's 9.48M-citizen capture (E:\gotmp\dbg13.json, 38,251
// buildings, ~14MB savepoint), saveCodec.encode()'s compressToUTF16 call ran
// synchronously on the main thread for ~2.4s. Autosave fires every 30s
// (AUTOSAVE_INTERVAL_MS, store.tsx), so that was a felt 2.4s input freeze
// twice a minute, and it also inflated the engine-lag badge (which measures
// main-thread responsiveness). JSON.stringify measured ~45ms on the same
// capture and stays synchronous — only the LZ step below moves off-thread.
//
// DESIGN (option (a) from the brief, preferred): a tiny dedicated Worker
// (saveCodecWorker.ts) that does nothing but call saveCodec.encode() on the
// string it is given and post the result back — the SAME encode() the
// synchronous fallback below calls, so there is exactly one compression
// implementation (GR#21), just two places it can run.
//
// One worker, lazily constructed on first use and reused for the page's
// lifetime (mirrors simWorker.ts/store.tsx's single-purpose-worker
// convention). Unlike simWorkerOffloadController.ts's tick-offload worker,
// a compression job has no "at most one outstanding" requirement — the
// worker's mailbox can hold more than one pending encode — but a STALE
// result must never reach storage once a newer persist has started; see
// `nextPersistGeneration`/`isCurrentPersistGeneration` below, used by
// replay.ts's `persistSavepointWithReasonAsync`.
//
// FALLBACK (options (b)/(c) from the brief): synchronous `encode()` — used
// when `Worker` is unavailable (SSR / node --test / jsdom without a Worker
// polyfill), when the worker fails to construct, or when it errors at
// runtime. Never blocks a save on a capability the environment doesn't
// have. A capture-before-wipe (GR#27) or any OTHER synchronous call site
// this bug was told not to touch keeps calling saveCodec.encode() directly
// and never routes through here at all.
import { encode } from './saveCodec.ts';
import { recordError } from './backend.ts';
import type { SaveCodecWorkerRequest, SaveCodecWorkerReply } from './saveCodecWorker.ts';

// ---------------------------------------------------------------------------
// Stale-result discard (the "a save in flight must not be superseded by a
// newer one silently" requirement), WITH PRIORITY (opus-round-bug798
// REJECT, finding A): an explicit save (the player clicked Save/Save As —
// a call they are actively waiting on) must NEVER read as superseded merely
// because an autosave happened to start while its encode was in flight.
// Aaron's dogfood city hit this on ~8% of manual saves: autosave fires every
// 30s, and a manual save's own off-thread encode can easily still be
// running when the next autosave tick lands. Under a single shared counter,
// that autosave — which the player never asked for and which persists to
// the SAME slot — would win the race and make Save/Save As silently report
// success while writing nothing (or writing to the wrong slot's freshness
// baseline), a data-loss-shaped false positive worse than a loud failure.
//
// State:
//   - `latestGeneration` — bumped by EVERY persist call, either kind. An
//     AUTOSAVE reads current only if its own id is still the overall latest
//     AND it was never marked doomed (below) — so any later call of EITHER
//     kind correctly supersedes an already-in-flight autosave (the round's
//     own example: "an autosave in flight is the one discarded").
//   - `latestExplicitGeneration` — bumped ONLY by explicit saves. An
//     EXPLICIT save reads current only if its own id is still the latest
//     EXPLICIT id — so an autosave can never supersede it, regardless of
//     start order. Only a LATER EXPLICIT save (e.g. Save then Save As in
//     quick succession) can supersede an explicit one; see
//     `persistSavepointWithReasonAsync`'s doc comment and store.tsx's
//     saveGame/saveGameAs for the "retry once with the freshest state"
//     handling that requires (returning `{ok:true}` on a superseded
//     EXPLICIT save would silently no-op a DIFFERENT storage target —
//     Save and Save As write to different slots).
//   - `explicitInFlightCount` / `doomedAutosaveGenerations` — the round's
//     LITERAL bug shape was "an autosave fires DURING a manual save's
//     encode": explicit starts, THEN autosave starts, and the autosave's
//     compression happens to finish FIRST. Under `latestGeneration` alone
//     that autosave would read as current (nothing later exists yet) and
//     WRITE, even though the whole point of priority is that the player's
//     explicit save should not be racing an unattended autosave for the
//     same slot at all. Any autosave MINTED while at least one explicit
//     call is still outstanding is marked doomed at birth — it will read as
//     superseded when it resolves, however the two round trips interleave.
//     `settlePersistGeneration` (called once per persist, after the
//     generation check) retires the bookkeeping so it never leaks past one
//     persist's lifetime.
//
// `encodeOffMainThread` itself does not consult any of this — the caller
// (replay.ts) captures its own generation before awaiting and checks it
// after, so this module stays a pure compression utility with no opinion on
// WHICH caller's request matters.
// ---------------------------------------------------------------------------
export type PersistKind = 'autosave' | 'explicit';

let latestGeneration = 0;
let latestExplicitGeneration = 0;
let explicitInFlightCount = 0;
const doomedAutosaveGenerations = new Set<number>();

/**
 * Mint a new persist generation id for a call of the given kind. Every
 * caller MUST eventually pair this with exactly one `settlePersistGeneration`
 * call for the same (gen, kind) — `persistSavepointWithReasonAsync` is the
 * only caller and does so unconditionally, success or supersede.
 */
export function nextPersistGeneration(kind: PersistKind): number {
  latestGeneration += 1;
  const gen = latestGeneration;
  if (kind === 'explicit') {
    latestExplicitGeneration = gen;
    explicitInFlightCount += 1;
  } else if (explicitInFlightCount > 0) {
    // An explicit save is currently outstanding — this autosave is doomed
    // regardless of which round trip finishes first (see header comment).
    doomedAutosaveGenerations.add(gen);
  }
  return gen;
}

/**
 * Is `gen` (minted for a call of `kind`) still current? Autosave loses to
 * ANY later call (either kind) OR to having been minted while an explicit
 * save was outstanding; explicit loses ONLY to a later explicit call — see
 * the header comment above for why the two are asymmetric. Pure query — does
 * NOT retire the bookkeeping; call `settlePersistGeneration` for that.
 */
export function isCurrentPersistGeneration(gen: number, kind: PersistKind): boolean {
  if (kind === 'explicit') return gen === latestExplicitGeneration;
  if (doomedAutosaveGenerations.has(gen)) return false;
  return gen === latestGeneration;
}

/**
 * Retire the bookkeeping `nextPersistGeneration` created for (gen, kind),
 * once its persist attempt has fully resolved (success OR superseded) —
 * called exactly once per persist by `persistSavepointWithReasonAsync`,
 * regardless of outcome. An explicit call releases its slot in
 * `explicitInFlightCount` (so a LATER autosave is no longer doomed purely
 * because of a save that has already finished); an autosave call clears its
 * own doomed-flag entry (bounded memory — never accumulates across a
 * session).
 */
export function settlePersistGeneration(gen: number, kind: PersistKind): void {
  if (kind === 'explicit') {
    if (explicitInFlightCount > 0) explicitInFlightCount -= 1;
  } else {
    doomedAutosaveGenerations.delete(gen);
  }
}

/**
 * BUG-798 round REJECT finding D / BUG-687 shape: fence the reset (Start
 * Over / new-city) boundary so an in-flight persist from the OLD city —
 * autosave or explicit, does not matter which — can never resolve into a
 * write that lands in (or gets ambient-stamped into, via
 * `persistSavepointWithReason`'s absent-lineageId default reading whatever
 * `metropolis.currentLineage` says AT WRITE TIME) the NEW city's lineage.
 * Unlike the priority rule above, this is unconditional: it invalidates
 * EVERY outstanding request, explicit included, because a reset is a
 * deliberate replace-the-city boundary, not a competing save — there is no
 * "the newer one wins" here, only "nothing from the old city may land after
 * this point". Call this synchronously as the very first step of the reset
 * dispatch path (store.tsx), before the wipe/lineage-mint themselves.
 */
export function fencePersistGeneration(): void {
  latestGeneration += 1;
  latestExplicitGeneration = latestGeneration;
}

// ---------------------------------------------------------------------------
// opus-reround-bug798 P2 finding 2: serialise the EXPLICIT storage WRITE
// step through a single promise chain.
//
// The generation check above (isCurrentPersistGeneration) decides WHICH
// explicit call is allowed to write, but that decision and the actual write
// (persistSavepointWithReason's synchronous read-modify-write against
// `storage`) are two different moments — nothing before this fix stopped
// two "currently winning" writes (an original call and a later retry of a
// DIFFERENT explicit call, each legitimately the latest EXPLICIT generation
// at the instant IT checked) from being issued back-to-back without a
// well-defined order relative to each other, or a slower-but-still-current
// write landing after a newer one has already written and clobbering it.
// Every explicit write now goes through `runSerializedExplicitWrite`: writes
// execute one at a time, strictly in ENQUEUE order, and a write whose
// generation is lower than the highest generation that has ALREADY written
// is skipped outright rather than clobbering newer, already-persisted data.
// ---------------------------------------------------------------------------
let explicitWriteChain: Promise<void> = Promise.resolve();
let lastWrittenExplicitGeneration = 0;

/**
 * Run `write` (a synchronous storage write) for explicit generation `gen`,
 * serialised against every other explicit write via a single chain. Returns
 * `write()`'s result, or `undefined` if a HIGHER generation has already
 * written since this one was enqueued — in which case `write` is never
 * even called, so an older generation can never overwrite a newer one's
 * already-landed bytes. Never throws for a `write` that throws — the
 * rejection propagates to THIS call's own await, but the chain itself keeps
 * running for whatever is queued after it.
 */
export function runSerializedExplicitWrite<T>(gen: number, write: () => T): Promise<T | undefined> {
  const scheduled = explicitWriteChain.then((): T | undefined => {
    if (gen < lastWrittenExplicitGeneration) {
      // A later generation already wrote while this one was queued/
      // encoding — never overwrite it with older data.
      return undefined;
    }
    const result = write();
    if (gen > lastWrittenExplicitGeneration) lastWrittenExplicitGeneration = gen;
    return result;
  });
  // Keep the chain alive regardless of whether `write` (or the generation
  // check) throws — the NEXT queued write must still get its turn. The
  // caller still observes any rejection via `scheduled` itself.
  explicitWriteChain = scheduled.then(
    () => undefined,
    () => undefined
  );
  return scheduled;
}

/** Test-only: reset the write-serialization chain/high-water mark. */
export function resetExplicitWriteSerializationForTests(): void {
  explicitWriteChain = Promise.resolve();
  lastWrittenExplicitGeneration = 0;
}

// ---------------------------------------------------------------------------
// Worker lifecycle.
// ---------------------------------------------------------------------------
let worker: Worker | null = null;
/** Sticky once true: construction failed, or the worker errored at runtime —
 *  never retried this session (matches AC-8's "one construction attempt per
 *  lifetime" posture; a worker that has already misbehaved is not worth
 *  re-trying on every subsequent save). */
let workerBroken = false;
let nextRequestId = 0;
const pending = new Map<number, { json: string; resolve: (text: string) => void }>();

/** Test-only counters (never read by production code) so tests can assert
 *  WHICH path a given encode actually took without any wall-clock timing —
 *  see BUG-757's "no absolute wall-clock asserts" precedent. */
export const __saveCodecAsyncProbe = {
  workerEncodeCount: 0,
  syncFallbackCount: 0,
};

/**
 * Resolve every currently-pending request via the synchronous fallback and
 * clear the map. Called when the worker construction fails, or its
 * `onerror` fires — a broken worker can never deliver its promised replies,
 * so every request still waiting on one must be rescued rather than hang
 * forever.
 */
function fallbackAllPending(): void {
  for (const { json, resolve } of pending.values()) {
    __saveCodecAsyncProbe.syncFallbackCount += 1;
    resolve(encode(json));
  }
  pending.clear();
}

function getOrCreateWorker(): Worker | null {
  if (workerBroken) return null;
  if (worker) return worker;
  try {
    worker = new Worker(new URL('./saveCodecWorker.ts', import.meta.url), { type: 'module' });
  } catch (err) {
    workerBroken = true;
    const detail = err instanceof Error ? err.message : String(err);
    recordError(`Save compression worker failed to start (${detail}); compressing this save on the main thread instead.`, {
      type: 'app',
      action: 'save-compress',
      code: 'MET-V888',
    });
    return null;
  }
  worker.onmessage = (ev: MessageEvent<SaveCodecWorkerReply>) => {
    const { requestId, encoded } = ev.data;
    const req = pending.get(requestId);
    if (!req) return; // already resolved via fallback (e.g. a prior onerror) — ignore.
    pending.delete(requestId);
    __saveCodecAsyncProbe.workerEncodeCount += 1;
    req.resolve(encoded);
  };
  worker.onerror = (ev: ErrorEvent) => {
    workerBroken = true;
    const detail = ev?.message || 'unknown worker error';
    recordError(`Save compression worker failed while compressing this save (${detail}); falling back to synchronous compression for this save.`, {
      type: 'app',
      action: 'save-compress',
      code: 'MET-V889',
    });
    fallbackAllPending();
  };
  return worker;
}

/**
 * Compress `json` (a JSON.stringify'd savepoint) off the main thread when
 * possible, falling back to the synchronous `saveCodec.encode()` when a
 * Worker is unavailable or has already proven broken this session. Never
 * throws and never rejects — a worker-side failure degrades to the
 * synchronous result, exactly like `encode()`'s own internal fail-safe.
 *
 * Does not itself know about "stale request" semantics — see the
 * generation helpers above; the caller decides whether this result is still
 * wanted by the time it resolves.
 */
export function encodeOffMainThread(json: string): Promise<string> {
  if (typeof Worker === 'undefined') {
    __saveCodecAsyncProbe.syncFallbackCount += 1;
    return Promise.resolve(encode(json));
  }
  const w = getOrCreateWorker();
  if (!w) {
    __saveCodecAsyncProbe.syncFallbackCount += 1;
    return Promise.resolve(encode(json));
  }
  return new Promise<string>((resolve) => {
    const requestId = nextRequestId++;
    pending.set(requestId, { json, resolve });
    try {
      const msg: SaveCodecWorkerRequest = { requestId, json };
      w.postMessage(msg);
    } catch (err) {
      // postMessage itself threw (e.g. a hostile environment) — rescue this
      // one request synchronously rather than leaving it pending forever.
      pending.delete(requestId);
      const detail = err instanceof Error ? err.message : String(err);
      recordError(`Save compression worker failed to receive this save (${detail}); compressing it on the main thread instead.`, {
        type: 'app',
        action: 'save-compress',
        code: 'MET-V889',
      });
      __saveCodecAsyncProbe.syncFallbackCount += 1;
      resolve(encode(json));
    }
  });
}

/**
 * Test-only: tear down every module-level piece of state (worker instance,
 * broken flag, pending map, request/generation counters, probe counts) so
 * each test file gets a clean slate regardless of run order. Never called
 * by production code.
 */
export function resetSaveCodecAsyncForTests(): void {
  if (worker) {
    try {
      worker.terminate();
    } catch {
      /* best-effort teardown */
    }
  }
  worker = null;
  workerBroken = false;
  nextRequestId = 0;
  pending.clear();
  latestGeneration = 0;
  latestExplicitGeneration = 0;
  explicitInFlightCount = 0;
  doomedAutosaveGenerations.clear();
  explicitWriteChain = Promise.resolve();
  lastWrittenExplicitGeneration = 0;
  __saveCodecAsyncProbe.workerEncodeCount = 0;
  __saveCodecAsyncProbe.syncFallbackCount = 0;
}
