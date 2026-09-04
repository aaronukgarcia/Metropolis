// FEAT-webworker-sim-offload Stage 1 / Landing 2 — offload controller
// (2026-09-02, independent round REJECT follow-up).
//
// The FIRST cut of this feature embedded the "should this tick request be
// issued / superseded / applied / journaled" decisions directly inside
// store.tsx's useEffect/useRef closures. The round could not get a fast,
// deterministic regression test onto B2 (load-race) or B3 (phantom journal
// tick) without either driving real timers through a real React mount or
// re-deriving the logic by hand in the test — exactly the "design for
// testability" trap the original brief warned about for the tick-handler
// itself (simWorkerProtocol.ts's runTick). This module does the same thing
// for the REQUEST/REPLY bookkeeping: every state transition is a pure
// function over a small plain-object state, so test/simworker-offload.test.mjs
// can drive B2/B3 scenarios directly, in microseconds, with no DOM/timers/
// Worker involved. store.tsx is now a thin glue layer over this module —
// see its "Stage 1 offload" section.
export interface OffloadControllerState {
  /** True while exactly one tick request is outstanding (Landing 2: at most
   *  one in flight by design — see store.tsx). */
  pendingTick: boolean;
  /** The currently outstanding request's id, or null. A reply is applied
   *  ONLY if its requestId matches this — see decideTickReply. */
  activeRequestId: number | null;
  /** The PRE-tick tick number captured when the active request was issued —
   *  the number a journal entry for that tick must carry, if-and-only-if
   *  the reply is later applied (B3). */
  activeRequestTick: number | null;
  /** Monotonically increasing — never reused, so a reply for a superseded
   *  request can never accidentally match a later one. */
  nextRequestId: number;
  /**
   * N1 fix (independent round 2 REJECT, 2026-09-02) — count of CONSECUTIVE
   * times an in-flight request was superseded (invalidateInFlight actually
   * cancelling a pending request) without a single tick being APPLIED in
   * between. Reset to 0 the instant a tick is applied (decideTickReply's
   * 'apply' branch) or a forced sync tick runs (afterForcedSyncTick).
   *
   * This is the load-bearing liveness counter: shouldForceSyncTick() below
   * reads it to decide when to stop trusting the worker round-trip
   * entirely and force the next tick through main thread synchronously.
   * See shouldForceSyncTick's header for the full story (the round's N1
   * finding: continuous input — e.g. a drag-paint emitting a 'place' every
   * pointermove — could invalidate every single tick request before its
   * worker reply ever landed, starving the sim clock to a dead stop, with
   * queue-depth silently reading "caught up" the whole time because ticks
   * were discarded, not queued).
   */
  supersedeStreak: number;
  /**
   * BUG-592 fix (Web Worker round 4 follow-up, 2026-09-02) — true from the
   * instant a tick request is actually POSTED to the (serial, uncancellable)
   * worker until its reply/error/teardown is actually observed. Deliberately
   * tracked SEPARATELY from `pendingTick`: `pendingTick` is the controller's
   * own "do I still care about this request's eventual reply" bookkeeping
   * and is cleared the moment a supersede occurs (invalidateInFlight) —
   * correctly, from the JOURNAL/APPLY point of view, since the superseded
   * reply must never be applied. But invalidateInFlight does NOT, and
   * cannot, cancel the actual computation already running inside the
   * worker — the worker has no cancellation channel, and Landing 2's
   * postMessage payload is a full ~1.77MB SimState clone. With the OLD
   * design, beginTickRequest's gate read ONLY `pendingTick`, so the very
   * next tick-driver interval fire (post-supersede) was free to
   * postMessage a SECOND real computation while the FIRST was still
   * running — and under sustained input with round-trip > interval, every
   * subsequent interval fire did the same, piling up an unbounded backlog
   * in the worker's actual (invisible to this controller) mailbox: 501
   * queued / ~887MB measured over a 60s drag at interval=100ms/rt=600ms. A
   * correctness non-issue (stale replies are still discarded by requestId,
   * replay is still byte-identical) but a straightforward tab-OOM memory
   * hazard. `workerBusy` closes that gap: beginTickRequest now refuses to
   * issue while it is true, so at most ONE computation can EVER be
   * outstanding in the worker's mailbox at a time, regardless of how many
   * times invalidateInFlight fires in between. Cleared ONLY by
   * clearWorkerBusy — called by the caller (store.tsx's worker.onmessage,
   * for BOTH a matched and a stale/superseded reply, and worker.onerror /
   * teardown) the instant the worker is actually observed to be done with
   * that one computation, never merely because the controller stopped
   * caring about its result.
   */
  workerBusy: boolean;
}

export function initialOffloadControllerState(): OffloadControllerState {
  return {
    pendingTick: false,
    activeRequestId: null,
    activeRequestTick: null,
    nextRequestId: 0,
    supersedeStreak: 0,
    workerBusy: false,
  };
}

/**
 * FEAT-2326609771 (2026-09-04, default-ON rollout hardening) — the HANDSHAKE
 * timeout: how long store.tsx should wait for the worker's FIRST tick reply
 * ever (proof the worker is genuinely alive, not just constructed without
 * throwing) before giving up on it for the rest of the session and falling
 * back to the synchronous main-thread reducer path — the same fallback AC-8
 * already uses for a construction failure or an onerror.
 *
 * DERIVED, not a flat constant, because a flat constant cannot be correct at
 * both ends of this game's own stated scale (Option B: persistent citizens up
 * to 100M at adaptive fidelity). A short fixed timeout (say 2s) would be
 * correct for a fresh city but would misdiagnose a perfectly healthy worker
 * on a large save as "hung" purely because Landing 2's full-SimState-clone
 * protocol (see simWorkerProtocol.ts's scope note) takes longer to clone and
 * fold through the reducer as the building count grows — exactly the
 * scenario this rollout most needs to get right, since default-ON means
 * every dogfood session on a large city now depends on this window. A long
 * fixed timeout (say 30s flat) would be correct at scale but would leave a
 * genuinely-stuck small city sitting silently on a dead worker for far
 * longer than necessary before recovering. Deriving the window from the
 * REQUEST's own building count ties the timeout to the actual payload being
 * cloned/computed, so neither end of the scale is either falsely tripped or
 * left waiting needlessly.
 *
 * FLOOR covers worker construction + module evaluation + JIT warmup on a
 * near-empty city, none of which scales with building count. CEILING is a
 * hard cap (a bound on the bound, matching this codebase's "liveness needs
 * BOTH a floor and a ceiling" lesson from the Web Worker saga, applied here
 * to the timeout itself rather than to tick throughput) — even an
 * astronomically large city must not be allowed to wait indefinitely for a
 * first reply; something has gone wrong well before 30s regardless of scale,
 * and the player deserves the fallback rather than a silent hang.
 */
export const HANDSHAKE_TIMEOUT_FLOOR_MS = 4000;
export const HANDSHAKE_TIMEOUT_PER_BUILDING_MS = 0.4;
export const HANDSHAKE_TIMEOUT_CEILING_MS = 30_000;

/**
 * Derive the handshake timeout (ms) for a request built against a city with
 * `buildingCount` buildings. Pure function of one number so it is trivially
 * unit-testable without a real Worker/timer (test/simworker-offload.test.mjs
 * covers the floor, the per-building slope, the ceiling clamp, and hostile
 * inputs — NaN/negative/non-finite counts — all of which fall back to the
 * floor rather than producing NaN/negative/Infinity timeouts).
 */
export function deriveHandshakeTimeoutMs(buildingCount: number): number {
  const safeCount = Number.isFinite(buildingCount) && buildingCount > 0 ? buildingCount : 0;
  const derived = HANDSHAKE_TIMEOUT_FLOOR_MS + safeCount * HANDSHAKE_TIMEOUT_PER_BUILDING_MS;
  return Math.min(HANDSHAKE_TIMEOUT_CEILING_MS, derived);
}

/**
 * N1/N2 fix — the forced-synchronous-tick threshold. After this many
 * CONSECUTIVE supersedes with no tick applied in between, the caller
 * (store.tsx's guardedDispatch) must force the next tick through the
 * EXISTING main-thread fallback reducer path (the same one AC-8 already
 * uses when the worker is unavailable) instead of trying the worker again.
 *
 * This is the SOLE liveness mechanism (independent round 3 REJECT,
 * 2026-09-02, firm steer: "drop the immediate rebase entirely"). An earlier
 * cut ALSO issued the next request immediately whenever a stale reply
 * arrived (store.tsx's worker.onmessage), reasoning that this would
 * shorten the "how long can input starve a tick" retry window from "up to
 * one full SPEED_MS interval" to "one worker round-trip". That reasoning
 * was correct in isolation, but COMBINED with this K-escape it formed an
 * INTERVAL-INDEPENDENT tick generator: under continuous sub-round-trip-
 * interval input, issue/invalidate/reissue cycles could complete many
 * times within a single interval period, each Kth one forcing a
 * synchronous tick — the round measured ~20x the selected speed
 * (SPEED_MS=1000, 16ms round-trip, 60Hz drag). The round proved the
 * opposite failure mode does NOT occur with rebase removed entirely
 * (modelled out): the progress ratio never exceeds ~0.33x (slower, never
 * faster) at that same 4-interval-round-trip/action-every-interval
 * scenario. Without rebase, a tick request can ONLY be issued in response
 * to a scheduled interval fire (store.tsx's tick-driver effect) — so the
 * supersede rate, and therefore the forced-tick rate, is bounded by the
 * INTERVAL rate: total tick production can never exceed the selected
 * speed. A forced SYNCHRONOUS tick has no round-trip at all — it cannot be
 * invalidated by input that arrives after it has already returned — so it
 * is the only mechanism that provably terminates regardless of input rate
 * or worker latency, and (now that it is the ONLY re-issue path) the only
 * mechanism that can ever produce a tick outside the interval's own
 * cadence, bounded to at most once per K intervals. K=3 (not 1) so a
 * single isolated supersede — the overwhelmingly common case of one click
 * landing mid-flight — never pays the synchronous cost; only sustained
 * contention does, exactly the case that needs the guarantee.
 */
export const FORCE_SYNC_TICK_SUPERSEDE_THRESHOLD = 3;

/** True once supersedeStreak has reached the threshold — the caller must
 *  force a synchronous tick THIS turn (see FORCE_SYNC_TICK_SUPERSEDE_THRESHOLD). */
export function shouldForceSyncTick(s: OffloadControllerState): boolean {
  return s.supersedeStreak >= FORCE_SYNC_TICK_SUPERSEDE_THRESHOLD;
}

/** Call immediately after actually running the forced synchronous tick —
 *  resets the streak so the NEXT K supersedes must accumulate again before
 *  another forced tick fires. Does not touch pendingTick/activeRequestId:
 *  those were already cleared by the invalidateInFlight call that preceded
 *  the streak check (see store.tsx's guardedDispatch), and the forced tick
 *  itself never touches the worker/controller request bookkeeping at all —
 *  it runs entirely through the ordinary main-thread reducer. */
export function afterForcedSyncTick(s: OffloadControllerState): OffloadControllerState {
  return { ...s, supersedeStreak: 0 };
}

/**
 * The tick-driver wants to issue a new tick request. Returns null if the
 * worker is BUSY (BUG-592 fix: gated on `workerBusy`, not `pendingTick` —
 * see the field's own header comment) — the caller (store.tsx's interval)
 * should skip this fire rather than post a second real message into the
 * worker's serial, uncancellable mailbox while the first is still running
 * (Landing 2's "at most one in flight" design; see simWorkerProtocol.ts for
 * why). Before the fix this checked `pendingTick`, which invalidateInFlight
 * clears on every supersede even though the worker itself is still crunching
 * the superseded request — that mismatch is exactly what let an unbounded
 * backlog of real ~1.77MB SimState clones pile up in the worker's mailbox
 * under sustained input with round-trip > interval.
 */
export function beginTickRequest(
  s: OffloadControllerState,
  currentTick: number
): { state: OffloadControllerState; requestId: number } | null {
  if (s.workerBusy) return null;
  const requestId = s.nextRequestId;
  return {
    state: {
      pendingTick: true,
      activeRequestId: requestId,
      activeRequestTick: currentTick,
      nextRequestId: requestId + 1,
      supersedeStreak: s.supersedeStreak, // preserved — only apply/forced-tick resets it.
      workerBusy: true, // BUG-592: set the instant we're about to postMessage; cleared only by clearWorkerBusy.
    },
    requestId,
  };
}

/**
 * Supersede whatever tick request is currently in flight — called before
 * ANY action that changes `state` out from under that request's basis:
 * a non-tick user action applied immediately (guardedDispatch — see
 * store.tsx), or a full 'reset'/loadGame-hydrate replace (B2). The eventual
 * reply for the superseded request will fail decideTickReply's requestId
 * match and be discarded unconditionally, however OLD or NEW its own tick
 * number is — this is what makes B2 (a stale-but-numerically-higher reply
 * clobbering a freshly loaded OLDER save) structurally impossible rather
 * than merely unlikely: the decision no longer depends on comparing tick
 * numbers against a state that may already have been replaced.
 * No-op (returns the same object) when nothing is in flight.
 *
 * BUG-592: deliberately does NOT touch `workerBusy` — it is carried through
 * unchanged by the `...s` spread. Superseding a request stops the
 * CONTROLLER from caring about its reply, but it does nothing to the actual
 * computation already running inside the worker (no cancellation channel
 * exists) — `workerBusy` stays true until the worker is actually observed
 * to finish (clearWorkerBusy, called from the eventual reply/error), so
 * beginTickRequest continues to refuse a new post until then.
 */
export function invalidateInFlight(s: OffloadControllerState): OffloadControllerState {
  if (!s.pendingTick) return s;
  // N1 fix: count this as one more consecutive supersede — this is the
  // ONLY place supersedeStreak increments (a request that was genuinely
  // cancelled mid-flight, not merely a reply that turns out to be stale by
  // the time it arrives — decideTickReply's requestId-mismatch branch does
  // NOT double-count, since the streak was already bumped here at the
  // moment of invalidation).
  return {
    ...s,
    pendingTick: false,
    activeRequestId: null,
    activeRequestTick: null,
    supersedeStreak: s.supersedeStreak + 1,
  };
}

/**
 * BUG-592 fix — call the instant the worker is ACTUALLY observed to be done
 * with its one outstanding computation: a matched reply, a stale/superseded
 * reply (the worker doesn't know it was superseded — it always finishes the
 * tick it was given and posts back), an onerror, or a teardown/terminate.
 * Unconditional and idempotent (clearing an already-clear flag is a no-op
 * value-wise); deliberately separate from decideTickReply so that function's
 * existing requestId-match/discard semantics (and its reference-equality
 * "was this a stale mismatch" convention relied on by store.tsx and the
 * B2/B3 regression tests) stay completely untouched by this fix — this is
 * purely an additional, independent field transition applied on top of
 * whatever decideTickReply already decided.
 */
export function clearWorkerBusy(s: OffloadControllerState): OffloadControllerState {
  if (!s.workerBusy) return s;
  return { ...s, workerBusy: false };
}

export type TickReplyDecision =
  | { kind: 'discard' }
  | { kind: 'apply'; tickToJournal: number };

/**
 * A worker reply has arrived. Decide whether to discard it or apply it, and
 * return the updated controller state either way.
 *
 * B3 fix: `tickToJournal` is returned ONLY on the 'apply' branch — the
 * caller (store.tsx) must journal the tick if and only if this returns
 * 'apply', never at request time. A discarded reply (stale requestId, an
 * onerror/teardown that never delivered a reply at all, or — belt and
 * braces — a reply whose tick doesn't actually advance past the live tick)
 * never produces a journal write, so genesis-replay can never be asked to
 * replay a tick that live play never actually applied.
 *
 * B2 fix: the requestId check (not a tick-number comparison against
 * whatever `state` happens to be current by the time this runs) is the
 * PRIMARY discard criterion — see invalidateInFlight's comment for why a
 * tick-number comparison alone is unsafe once state may have been
 * wholesale-replaced. The tick-number check below is kept as a defensive
 * second guard (never observed to matter given the requestId check, but
 * costs nothing and protects against a future change accidentally reusing
 * an id).
 */
export function decideTickReply(
  s: OffloadControllerState,
  reply: { requestId: number; resultTick: number },
  currentLiveTick: number
): { state: OffloadControllerState; decision: TickReplyDecision } {
  if (reply.requestId !== s.activeRequestId) {
    // Stale/superseded — leave state untouched (whatever superseded this
    // request already updated it correctly; a second write here could race
    // a request issued in the meantime).
    return { state: s, decision: { kind: 'discard' } };
  }
  const tickAtRequest = s.activeRequestTick;
  const cleared: OffloadControllerState = {
    ...s,
    pendingTick: false,
    activeRequestId: null,
    activeRequestTick: null,
  };
  if (reply.resultTick > currentLiveTick && tickAtRequest !== null) {
    // N1 fix: a real, successful tick application resets the supersede
    // streak — forward progress happened, the K-consecutive-supersedes
    // counter starts fresh.
    return {
      state: { ...cleared, supersedeStreak: 0 },
      decision: { kind: 'apply', tickToJournal: tickAtRequest },
    };
  }
  // Belt-and-braces discard (matched requestId, but the reply's own tick
  // doesn't advance past the live tick — should not occur under the
  // invariants documented above; kept as defensive insurance). Does NOT
  // touch supersedeStreak either way: this is not a supersede (nothing
  // invalidated this request — it settled on its own terms) and it is not
  // a successful apply, so leaving the streak exactly as invalidateInFlight
  // last left it is the conservative, correct choice.
  return { state: cleared, decision: { kind: 'discard' } };
}
