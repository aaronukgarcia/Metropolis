// consistency.ts — FEAT-1972079890 round-3: real cross-derivation checks
//
// Cross-derivation layer: compare two INDEPENDENT paths to the same fact.
// (1) FUNDS-VS-FLOWS: funds === lastAdvanceFunds + inflows - outflows
// (2) FLOWS-VS-RECOMPUTE: recompute key flow amounts from placed[] and compare
//     to actual lastFlows entries — catches silent data corruption
// (3) PALETTE-VS-SPEC: placed buildings' colors exist in SPECS

import type { SimState, FlowItem } from './types.ts';
import {
  SPECS,
  TIER_COLORS,
  densityTier,
  PALETTE_FLAT,
  countByKindOnline,
  isOnline,
  totalJobsBySector,
  filledJobsFromCapacityAndPopulation,
  upkeepChargeableOf,
} from './data.ts';
import {
  councilTaxPerTick,
  businessTaxPerTick,
  sectorWagesPerTick,
  applyOutflowPolicies,
  UPKEEP_BUCKET,
  GRID_IMPORT_OUTFLOW_LABEL,
  BAILOUT_STANDING_COST_LABEL,
} from './fiscal.ts';

export interface ConsistencyCheck {
  id: string;
  ok: boolean;
  detail: string;
}

export interface ConsistencyReport {
  checks: ConsistencyCheck[];
  failures: number;
  /**
   * BUG-624: ids of the grace-eligible per-line flows-vs-recompute checks
   * (GRACE_ELIGIBLE_LINE_IDS below) that failed on THIS call's RAW
   * evaluation — i.e. before any grace was applied. Always populated
   * (whether or not the caller asked for grace), so a caller can start
   * threading it through from any point without a throwaway first call.
   * A caller that wants the two-consecutive-failures tolerance passes this
   * set back in as the NEXT call's `priorFailedLineIds` argument — carried
   * by the CALLER, never by SimState (determinism untouched, and
   * runConsistencyChecks stays a pure function of its arguments).
   */
  rawFailedLineIds: string[];
  /**
   * BUG-640 round-2 (independent REJECT round, 2026-09-03): id -> delta
   * (actual - computed) for every id in `rawFailedLineIds`, on THIS call's
   * RAW evaluation. A plain JSON-safe `Record` (not a Map) so it survives
   * `JSON.stringify` unchanged inside debug.json.
   *
   * The round-1 windowed fix (bare occurrence counting — "same id failed N
   * times in the window") had its own false-positive blind spot: a steadily,
   * legitimately growing city (a plausible every-3-tick build cadence) reds
   * 10%+ of refreshes, because TWO INDEPENDENT, genuine online-flip
   * transients (different buildings, different ticks) landing within one
   * window of each other were counted as "the same defect happening twice".
   * The fix is to also require the failures to carry the SAME SIGNATURE
   * (identical delta, sign and magnitude) before a repeat counts toward the
   * threshold — a persisting/recurring real defect reproduces the identical
   * divergence every time it fires, while unrelated transients generically
   * diverge by different amounts (a different building's upkeep constant,
   * a different pending population/job delta at that moment). See
   * `foldGraceHistory`.
   */
  rawFailedSignatures: Record<string, number>;
}

/**
 * BUG-624 (Aaron/Opus round, 2026-09-03): the flows-vs-recompute per-line
 * checks (Wages, Upkeep total) reconstruct their comparison from CURRENT
 * state (`countByKindOnline(s)` / `isOnline(s, b)`), while the "actual" side
 * is the value `advance()` recorded via `computeFlows(s)` EARLIER in the
 * same tick, against the PRE-increment `s.tick`. When a building's
 * construction completes exactly on this tick boundary (`s.tick - builtTick
 * === constructionTicks`), `isOnline` flips false→true between the
 * computeFlows() call and the tick-incremented final state — so the
 * recompute sees one more online building than the actual flow charged for.
 * This is genuine, benign, and self-heals next tick (the recompute and the
 * next tick's actual will agree again) — it is NOT the persistent silent
 * corruption these checks exist to catch (a hand-tampered flow, a building
 * removed without a reflow, a policy pipeline regression), which reproduces
 * on every subsequent check until fixed.
 *
 * The distinguishing signal is exactly that: a genuine defect fails on EVERY
 * consecutive check; a mid-tick online flip fails on exactly one. So these
 * two ids get a two-consecutive-failures grace — opt-in only (see
 * `priorFailedLineIds` on runConsistencyChecks): omitting the argument (as
 * every pre-existing call site and test does) keeps the ORIGINAL
 * instant-fail behaviour byte-for-byte, so BUG-603's tamper regressions and
 * every single-shot corruption test stay exactly as red as before. Only a
 * caller that explicitly threads `rawFailedLineIds` from one call into the
 * next `priorFailedLineIds` argument gets the tolerance, and even then a
 * failure that persists into the SECOND consecutive check is never graced.
 */
export const GRACE_ELIGIBLE_LINE_IDS: ReadonlySet<string> = new Set([
  'flows.wages-matches',
  'flows.upkeep-total-matches',
]);

/**
 * BUG-640 (Aaron/Opus round, 2026-09-03, following BUG-624): a caller that
 * threads only the SINGLE immediately-preceding check's raw-failure Set (the
 * original BUG-624 "2 consecutive failures" contract) has a structural blind
 * spot — a defect that raw-fails on every OTHER check (alternating parity,
 * e.g. tick 2/4/6/8...) always sees an empty `prior` Set, because the
 * intervening healthy check resets the history to nothing. An independent
 * attack round proved this: 20/20 alternating-tamper ticks graced over 40
 * ticks, zero reds, forever. The two-consecutive rule only ever looks one
 * step back.
 *
 * The fix is a BOUNDED WINDOW, not a single look-back, COMBINED with a
 * SIGNATURE MATCH (round-2 addendum below): a failure is graced only if
 * FEWER than GRACE_MAX_FAILURES_IN_WINDOW occurrences of the SAME id WITH
 * THE SAME DELTA SIGNATURE (see `rawFailedSignatures`) appear in the last
 * GRACE_WINDOW_SIZE checks (this one included). A genuine transient (one
 * isolated failure, or several UNRELATED transients whose deltas differ)
 * still has zero MATCHING occurrences in the window besides itself and
 * stays graced; an alternating or otherwise-recurring defect reproduces the
 * IDENTICAL divergence every time it fires, racks up
 * >= GRACE_MAX_FAILURES_IN_WINDOW matching occurrences within a handful of
 * checks, and reds for real. The window remains a BACKSTOP: even a
 * signature match older than the window has aged out and no longer counts.
 *
 * ROUND-2 FINDING (independent REJECT, 2026-09-03): the round-1 version of
 * this fix counted bare occurrences of an id, with no signature check. A
 * steady, realistic every-3-tick build cadence (faster than the window)
 * reds 10%+ of panel refreshes under that rule, because two INDEPENDENT
 * genuine online-flip transients (different buildings completing
 * construction at different times) land inside one window of each other
 * and get miscounted as "the same defect recurring". Requiring the SAME
 * delta (magnitude + direction), not just the same id, fixes this: two
 * unrelated transients generically diverge by different amounts (a
 * different building's upkeep constant, a different population/job
 * shortfall at that instant), while a real defect (or, rarely, two
 * coincidentally identical-spec transients — an accepted residual edge
 * case, not eliminated by this fix, see the round-2 plausibility test)
 * reproduces the exact same delta each time.
 *
 * ROUND-3 FINDING (independent REJECT re-round, 2026-09-03 — "the mirror
 * failure"): requiring an EXACT delta match to count as a repeat closes the
 * round-2 false-positive hole but opens a false-NEGATIVE one — a genuine
 * defect whose divergence DRIFTS every single check (wrong by a population-
 * linked or cumulative-spend-linked term, so the delta is never bit-identical
 * twice) or produces a NaN (division by zero — `NaN === NaN` is `false` in
 * JS) NEVER accumulates a matching-signature count, so it is graced on
 * literally every check, forever, even though it is failing EVERY SINGLE
 * TIME. This is strictly worse than doing nothing: the original BUG-624
 * two-consecutive rule would at least have caught it on the 2nd check.
 *
 * The round-3 fix does NOT loosen the delta comparison to a tolerance band —
 * a global tolerance wide enough to catch a slow per-tick drift (round-3's
 * repro drifts by ~1e-6 per tick) is mathematically incompatible with also
 * keeping two independent, genuinely-different-by-design deltas apart (the
 * round-2/round-3 suites both rely on exact matching to prove that; see
 * consistency's own test suite's floating-point-edge cases). Instead,
 * `pushGraceable` layers two SIGNATURE-BLIND backstops on top of the
 * signature-matched rule, checked in this order:
 *   1. A non-finite delta (NaN or ±Infinity) is NEVER graceable, full stop —
 *      see the `!Number.isFinite(delta)` branch below.
 *   2. SUSTAINED DIVERGENCE: if this id raw-failed on literally EVERY ONE of
 *      the caller's supplied historical snapshots (`priorDeltas.length >=
 *      GRACE_WINDOW_SIZE - 1`) AND fails again now, that is
 *      `GRACE_WINDOW_SIZE` CONSECUTIVE failures with zero healthy gaps —
 *      forced red regardless of whether the deltas match each other. A
 *      drifting-but-real defect (or a NaN one, belt-and-braces with rule 1)
 *      fires on every check and hits this within one full window; an
 *      isolated or even repeated-but-INTERMITTENT genuine transient never
 *      saturates every single slot and is unaffected.
 * Only once BOTH backstops clear does the ordinary signature-matched
 * tolerance (rule 3, unchanged from round-2) apply.
 *
 * ROUND-4 FINDING (independent REJECT re-round, 2026-09-03 — "the gap-free
 * hole"): the round-3 sustained-divergence backstop is a CONSECUTIVE-RUN
 * test — it only fires when a grace-eligible id fails on literally EVERY
 * ONE of the trailing snapshots, with ZERO gaps. A drifting defect that
 * fails on a periodic cadence with just ONE healthy check every 4th/5th/6th
 * check (duty cycles up to ~83%) never lets the run reach `GRACE_WINDOW_SIZE`
 * and evades BOTH backstops FOREVER, no matter how long it runs — measured
 * 0/120 reds at duty cycles 3/4, 4/5, 5/6 across every gap phase. This is
 * NOT an epsilon/tolerance problem (a perfect signature oracle was proven
 * unable to close it either, since the backstop's gate is raw OCCURRENCE
 * COUNT, an axis signature matching never touches) — it needs a
 * GAP-TOLERANT persistence signal.
 *
 * The round-4 fix REPLACES the round-3 all-or-nothing gap-free gate with a
 * single, LARGER, gap-TOLERANT "K of N" raw-failure-RATE backstop
 * (`GRACE_RATE_WINDOW_SIZE` / `GRACE_RATE_THRESHOLD`): red if this id
 * raw-failed at least `GRACE_RATE_THRESHOLD` times (ANY signature, gaps
 * allowed) within the trailing `GRACE_RATE_WINDOW_SIZE` checks. This is
 * signature- AND gap-blind by design — it cannot be evaded by drifting the
 * delta OR by scattering small healthy gaps through the failure run,
 * because it counts raw occurrences over a long horizon, not consecutive
 * runs over a short one. It is a strict GENERALISATION of the old gap-free
 * gate (which was just the special case K = N over a much smaller N) — it
 * is not layered ALONGSIDE the old gate, it stands in for it, because
 * keeping the old small-N/K=N gate ALSO active reopened the very
 * false-positive it was meant to avoid (see the finding below).
 *
 * ROUND-4 FALSE-POSITIVE FINDING (measured directly against an earlier
 * draft of this fix, before the redesign settled on the numbers below): a
 * healthy, steadily-growing city (one cheap building completing
 * construction per tick, indefinitely) can legitimately raw-fail
 * 'flows.upkeep-total-matches' on a long GAP-FREE run of consecutive,
 * individually-benign, DIFFERENT-signature online-flip transients — keeping
 * ANY small-window gap-free-only gate active (round-3's original
 * `GRACE_WINDOW_SIZE`-based rule) reds this every time. Measured
 * empirically against this codebase's own online-flip mechanics (a steady
 * one-park-per-tick dogfood cadence, `off_suite`-seeded fixture): this
 * benign run self-terminates at a MAXIMUM of ~70 consecutive raw fails
 * (confirmed never resumes across 1000+ further ticks of continued
 * building — construction-completion accounting re-stabilises once the
 * online building count is large enough that the mid-tick online-flip's
 * contribution rounds away). A malicious drifting defect, being a genuine
 * formula bug, NEVER self-terminates — at the weakest evasive duty cycle
 * this round measured (3/4 = 75%), it racks up ~75% of however many checks
 * have elapsed, forever. There is NO window small enough to catch a
 * gap-free run of 6 without ALSO catching a benign run that happens to also
 * be gap-free for a while — the only way to tell "83% duty forever" apart
 * from "100% duty for ~70 checks then never again" using raw counts alone
 * is to wait long enough that the benign case's OWN bounded ceiling has
 * necessarily passed.
 *
 * TUNING RATIONALE (Aaron may retune; both numbers below are measured):
 * `GRACE_RATE_WINDOW_SIZE = 180` / `GRACE_RATE_THRESHOLD = 125` (~69% duty)
 * sits with a comfortable margin on BOTH sides: 125 is ~1.8x the measured
 * benign ceiling (70, ~44% headroom before the benign case could ever reach
 * it), while every evasive duty cycle this round measured (75%/80%/83%)
 * empirically crosses 125 comfortably within the 180-check window (measured
 * first-red between check 148 and check 166 across all three duty cycles
 * and all gap-phase offsets tested — see attack-bug640-round4.test.mjs).
 * The trade-off, explicitly ACCEPTED: at the panel's 15s refresh cadence,
 * up to `GRACE_RATE_WINDOW_SIZE` checks (~45 minutes) before ANY
 * signature-drifting or gapped-evasion defect can red — this is
 * DELIBERATELY slower than round-3's ~90s promise, because round-3's speed
 * came from a gate that could not distinguish the two scenarios above. A
 * non-finite delta (NaN/±Infinity, see below) still reds INSTANTLY,
 * independent of this window, so the most dangerous corruption class is
 * unaffected by the slower rate tier. A fresh session's first
 * `GRACE_RATE_WINDOW_SIZE - 1` refreshes structurally cannot red via this
 * mechanism (the STARTUP WINDOW; see attack-bug640-round4.test.mjs's ATTACK
 * c) — documented and accepted per the same reasoning: a smaller window
 * would reopen the false-positive class this redesign exists to close.
 *
 * SIGNATURE-MATCH WINDOW STAYS SMALL (round-2 unchanged): the ordinary
 * signature-matched tolerance (`GRACE_MAX_FAILURES_IN_WINDOW` occurrences
 * of the exact same delta within `GRACE_WINDOW_SIZE` checks) is evaluated
 * against a SEPARATE, SHORT tail slice of the caller's history — NOT the
 * full long `GRACE_RATE_WINDOW_SIZE`-sized history the rate backstop reads.
 * This separation is load-bearing: naively widening signature-matching to
 * see the same long history the new rate backstop needs reintroduced a
 * round-2-class false positive (measured 9/500 on the round-2 mixed-spec
 * headline fixture — two coincidentally-identical-delta transients, spaced
 * far enough apart to have safely aged out of a 6-check window, no longer
 * aged out of a 180-check one). `foldGraceHistory` therefore derives TWO
 * independent windows from whatever history it is handed: a short tail
 * slice for signature matching, and the full supplied history for the rate
 * count.
 */
export const GRACE_WINDOW_SIZE = 6;
export const GRACE_MAX_FAILURES_IN_WINDOW = 2;
export const GRACE_RATE_WINDOW_SIZE = 180;
export const GRACE_RATE_THRESHOLD = 125;

/**
 * BUG-640 round-2/round-4: fold a rolling queue of PAST raw-failure
 * SIGNATURE snapshots (oldest first — the caller owns trimming the queue to
 * `GRACE_RATE_WINDOW_SIZE - 1` entries so the round-4 rate backstop has
 * enough history to ever fire; this function only folds what it is given)
 * into:
 *   - the PUBLIC per-id compacted list of past deltas, across the FULL
 *     supplied history (unchanged public contract since round-2 — `.get(id)`
 *     skips snapshots where the id was absent, so callers doing their own
 *     signature-match arithmetic — or tests introspecting the fold — see
 *     exactly the occurrences and nothing else). NOT what `pushGraceable`
 *     itself uses for signature matching any more (see the side channel
 *     below) — kept for backward compatibility with every existing caller/
 *     test built against this shape.
 *   - a PRIVATE, non-enumerable side channel (keyed by `GRACE_SIDE_CHANNEL`
 *     on the returned Map instance, invisible to `.get()`/`.has()`/
 *     iteration — attaching it as an own property rather than changing what
 *     `.get()` returns keeps every existing caller/test unaffected) carrying
 *     the two window-scoped signals `pushGraceable` actually consumes:
 *     `recentMatchDeltasById` (compacted deltas within just the trailing
 *     `GRACE_WINDOW_SIZE - 1` snapshots — the SHORT window the round-2
 *     signature match tolerance must stay scoped to) and `rateCountById`
 *     (total raw-occurrence COUNT, any signature, within the trailing
 *     `GRACE_RATE_WINDOW_SIZE - 1` snapshots — the LONG window the round-4
 *     rate backstop needs).
 * Callers (DebugTab today) keep a plain array of the last
 * `GRACE_RATE_WINDOW_SIZE - 1` `rawFailedSignatures` snapshots (plain
 * `Record<string, number>` objects — JSON-safe) and pass the folded result
 * in as `runConsistencyChecks`'s `priorFailedLineIds` argument — a
 * `Map<string, number[]>` opts into the windowed rule. Non-eligible ids are
 * dropped (the map only ever needs to answer questions about
 * GRACE_ELIGIBLE_LINE_IDS members).
 */
export const GRACE_SIDE_CHANNEL: unique symbol = Symbol('BUG-640 r4 grace side channel');

export interface GraceSideChannel {
  /** id -> compacted deltas within just the trailing GRACE_WINDOW_SIZE - 1 snapshots (short window; signature-match input). */
  recentMatchDeltasById: ReadonlyMap<string, number[]>;
  /** id -> total occurrence count (any signature) within the trailing GRACE_RATE_WINDOW_SIZE - 1 snapshots (long window; rate-backstop input). */
  rateCountById: ReadonlyMap<string, number>;
}

export function foldGraceHistory(
  pastRawFailedSignatureSnapshots: ReadonlyArray<Readonly<Record<string, number>>>
): ReadonlyMap<string, number[]> {
  const deltasById = new Map<string, number[]>();
  for (const snapshot of pastRawFailedSignatureSnapshots) {
    for (const id of Object.keys(snapshot)) {
      if (!GRACE_ELIGIBLE_LINE_IDS.has(id)) continue;
      const delta = snapshot[id];
      const existing = deltasById.get(id);
      if (existing) existing.push(delta);
      else deltasById.set(id, [delta]);
    }
  }

  // BUG-640 round-4: derive the two window-scoped side-channel signals.
  // Collect every grace-eligible id that appears ANYWHERE in the supplied
  // history first, so ids with zero recent occurrences never appear in the
  // side maps (keeps `?? []` / `?? 0` fallbacks at the call site meaningful).
  const n = pastRawFailedSignatureSnapshots.length;
  const idsSeen = new Set<string>();
  for (const snapshot of pastRawFailedSignatureSnapshots) {
    for (const id of Object.keys(snapshot)) {
      if (GRACE_ELIGIBLE_LINE_IDS.has(id)) idsSeen.add(id);
    }
  }
  const recentMatchDeltasById = new Map<string, number[]>();
  const rateCountById = new Map<string, number>();
  // SHORT window (signature match, round-2 scope — unchanged from before
  // round-4 introduced the long window): only the trailing
  // GRACE_WINDOW_SIZE - 1 snapshots, compacted (gaps skipped, matching the
  // public `.get()` semantics but scoped to just this tail).
  const matchTailStart = Math.max(0, n - (GRACE_WINDOW_SIZE - 1));
  // LONG window (rate backstop, round-4 scope): the full trailing
  // GRACE_RATE_WINDOW_SIZE - 1 snapshots.
  const rateTailStart = Math.max(0, n - (GRACE_RATE_WINDOW_SIZE - 1));
  for (const id of idsSeen) {
    const recentDeltas: number[] = [];
    for (let i = matchTailStart; i < n; i++) {
      const snap = pastRawFailedSignatureSnapshots[i];
      if (Object.prototype.hasOwnProperty.call(snap, id)) recentDeltas.push(snap[id]);
    }
    recentMatchDeltasById.set(id, recentDeltas);

    let rateCount = 0;
    for (let i = rateTailStart; i < n; i++) {
      if (Object.prototype.hasOwnProperty.call(pastRawFailedSignatureSnapshots[i], id)) rateCount++;
    }
    rateCountById.set(id, rateCount);
  }
  const sideChannel: GraceSideChannel = { recentMatchDeltasById, rateCountById };
  Object.defineProperty(deltasById, GRACE_SIDE_CHANNEL, {
    value: sideChannel,
    enumerable: false,
    configurable: true,
  });

  return deltasById;
}

/**
 * BUG-603 (Aaron ruling Q100079=A, tightened after a REJECT round caught the
 * conservation check being laundered): a hand-tampered `fundsAtTickEnd`/
 * `fundsAtTickStart` must NEVER be waved through by a "policy went stale"
 * recovery path. The split is:
 *   - CONSERVATION (funds-vs-flows, #1 below) is the LAST TICK's historical
 *     story — funds + the stored lastFlows it was computed from. A post-tick
 *     discretionary action (policy toggle, tax change, build) changes NEITHER
 *     of those, so conservation never legitimately goes stale; it must always
 *     read the STORED lastFlows/funds triplet, never a recompute. A tampered
 *     fundsAtTickEnd must keep failing here no matter what else is retried.
 *   - The PER-LINE POLICY-SENSITIVE checks (Council Tax / Business Tax /
 *     Wages / Upkeep total — flows-vs-recompute, #2 below) are the ones that
 *     legitimately go stale after a non-tick action, because they compare
 *     the stored "actual" value against a LIVE recompute of current
 *     policies/taxRates/buildings. `actualFlowsOverride` lets a caller (see
 *     replay.ts's checkConsistencyRecoveringStaleFlows) supply a freshly
 *     recomputed { inflows, outflows, population } to use as the "actual"
 *     side of ONLY these per-line checks on retry — conservation and every
 *     other check (shape validation, palette, lastFlows shape/finite) always
 *     read the real state, override or not.
 */
export interface RecomputedFlowsOverride {
  inflows: FlowItem[];
  outflows: FlowItem[];
  population?: number;
}

/**
 * Run all consistency checks on a SimState. Returns a deterministic report
 * that never mutates the state. `actualFlowsOverride` (BUG-603) is consumed
 * ONLY by the per-line policy-sensitive recompute checks — see the interface
 * doc above; conservation and every other check always use the real `s`.
 */
export function runConsistencyChecks(
  s: SimState,
  actualFlowsOverride?: RecomputedFlowsOverride,
  /**
   * BUG-624/BUG-640 opt-in grace argument. `undefined` (the default, and
   * every existing call site) means "no history known" — the ORIGINAL
   * instant-fail behaviour applies to every check, including the two
   * grace-eligible ids.
   *
   * Two accepted shapes, versioned by type (BUG-640):
   *  - `ReadonlySet<string>` — the ORIGINAL BUG-624 contract, preserved
   *    byte-for-byte for backward compatibility: a grace-eligible id that
   *    raw-fails now but is NOT in this set is tolerated for exactly one
   *    consecutive check. This is the "look back exactly one check" rule
   *    that BUG-640 proved has an alternating-parity blind spot.
   *  - `ReadonlyMap<string, number[]>` — the BUG-640 round-2 windowed +
   *    SIGNATURE-MATCHED contract (see `foldGraceHistory`/
   *    `GRACE_WINDOW_SIZE`/`GRACE_MAX_FAILURES_IN_WINDOW`): the value is the
   *    list of past DELTAS (actual - computed) that id raw-failed with,
   *    within the caller's trailing window. Graced only while the count of
   *    entries matching THIS failure's exact delta stays below tolerance —
   *    a recurring/alternating defect reproduces the identical divergence
   *    and racks up enough MATCHING occurrences to red for real, while
   *    unrelated genuine transients (which generically diverge by different
   *    amounts) do not accumulate against each other.
   *  - Anything else (an array, a plain object, `null`, or any other junk a
   *    corrupted/legacy caller might pass — GR#16, never trust a runtime
   *    value's shape from its declared type alone) degrades SAFELY to "no
   *    grace" for that check, exactly as if `undefined` had been passed.
   *    This function never throws on account of this argument's shape.
   */
  priorFailedLineIds?: ReadonlySet<string> | ReadonlyMap<string, number[]>
): ConsistencyReport {
  const checks: ConsistencyCheck[] = [];
  const rawFailedLineIds: string[] = [];
  const rawFailedSignatures: Record<string, number> = {};
  // BUG-624/BUG-640: push a check that may be grace-eligible. `rawOk` is the
  // true, ungraced comparison result — always recorded into
  // rawFailedLineIds/rawFailedSignatures when false, regardless of whether
  // grace ends up applying, so the caller's NEXT call has an accurate
  // history to compare against. `delta` (actual - computed) is the
  // round-2 SIGNATURE used to distinguish a recurring defect from
  // independent, unrelated transients that happen to share an id.
  const pushGraceable = (
    id: string,
    rawOk: boolean,
    delta: number,
    okDetail: string,
    failDetail: string
  ) => {
    if (rawOk) {
      checks.push({ id, ok: true, detail: okDetail });
      return;
    }
    rawFailedLineIds.push(id);
    rawFailedSignatures[id] = delta;
    let graceable = false;
    let graceNote = '';
    // GR#16: never trust the declared TS type of a runtime argument — only
    // dispatch into the Map/Set-specific logic once `instanceof` has proven
    // the shape. Anything else (array, plain object, null, ...) falls
    // through untouched and stays graceable=false (safe "no grace" default,
    // never a thrown TypeError from a duck-typed `.has`/`.get`).
    if (priorFailedLineIds !== undefined && GRACE_ELIGIBLE_LINE_IDS.has(id)) {
      if (priorFailedLineIds instanceof Map) {
        // BUG-640 round-2/round-4: pull BOTH window-scoped signals from
        // foldGraceHistory's side channel — NOT the public `.get(id)` (which
        // reflects the FULL supplied history and would, if used directly for
        // signature matching, widen that match window to whatever length the
        // round-4 rate backstop needs — a real regression this round
        // measured directly: 9/500 false positives on the round-2 mixed-spec
        // headline fixture once the caller started supplying long history).
        // A Map not produced by foldGraceHistory (e.g. a hand-built Map in a
        // test) simply has no side channel, so these default to empty/0 — a
        // safe "no positional history known" fallback, never a crash.
        const sideChannel = (priorFailedLineIds as ReadonlyMap<string, number[]> & {
          [GRACE_SIDE_CHANNEL]?: GraceSideChannel;
        })[GRACE_SIDE_CHANNEL];
        const recentDeltas: number[] = sideChannel?.recentMatchDeltasById.get(id) ?? priorFailedLineIds.get(id) ?? [];
        const rateCount = sideChannel?.rateCountById.get(id) ?? 0;
        if (!Number.isFinite(delta)) {
          // BUG-640 round-3 (h1) — NON-FINITE DELTA IS NEVER GRACEABLE: NaN
          // famously never equals itself (`NaN === NaN` is `false`), so exact
          // signature matching can NEVER accumulate a repeat for a
          // NaN-producing defect (e.g. a divide-by-zero corrupting Wages) —
          // it would otherwise be masked FOREVER, which is exactly backwards:
          // a non-finite value is close to the most dangerous corruption
          // class these checks exist to catch, not the least. (Infinity is
          // fine either way — `Infinity === Infinity` is `true`, so it
          // already accumulates correctly under exact matching; this branch
          // still short-circuits it to instant-red, which is only stricter,
          // never a regression.) Independent of the rate backstop below and
          // of match history — a single non-finite occurrence is never
          // tolerated.
          graceable = false;
          graceNote = `[BUG-640 r3: non-finite delta signature (${delta}) — never graceable]`;
        } else if (rateCount + 1 >= GRACE_RATE_THRESHOLD) {
          // BUG-640 round-4 (b) — GAP-TOLERANT RATE BACKSTOP, un-graced and
          // signature+gap-BLIND: `rateCount` is the total raw occurrence
          // count of this id (any delta, gaps allowed) within the trailing
          // `GRACE_RATE_WINDOW_SIZE - 1` supplied snapshots. A defect that
          // fails on a periodic cadence with occasional healthy gaps (e.g. 5
          // of every 6 checks) never produces a gap-free run, and — if it
          // also drifts — never repeats a signature either, but it DOES keep
          // accumulating raw occurrences over time; this backstop catches it
          // once the count crosses GRACE_RATE_THRESHOLD, independent of both
          // signature and gap position. This REPLACES round-3's small-window
          // gap-free-only gate (see GRACE_RATE_WINDOW_SIZE's doc comment for
          // why keeping both active reopened a false-positive class, and for
          // the measured tuning rationale behind 180/125).
          graceable = false;
          graceNote = `[BUG-640 r4: sustained rate — raw-failed ${rateCount + 1}/${GRACE_RATE_WINDOW_SIZE} of the last checks regardless of signature or gaps, forced red]`;
        } else {
          // BUG-640 round-2 signature-matched tolerance, SCOPED TO THE SHORT
          // WINDOW ONLY (`recentDeltas`, the trailing GRACE_WINDOW_SIZE - 1
          // snapshots — see foldGraceHistory's side channel): grace only
          // while the count of PRIOR occurrences carrying the SAME delta
          // (this failure included) stays below tolerance.
          const matchingCount = recentDeltas.filter((d: number) => d === delta).length;
          graceable = matchingCount + 1 < GRACE_MAX_FAILURES_IN_WINDOW;
          graceNote = `[BUG-640 grace: ${matchingCount + 1}/${GRACE_MAX_FAILURES_IN_WINDOW} matching-signature occurrences in the last ${GRACE_WINDOW_SIZE} checks, tolerated]`;
        }
      } else if (priorFailedLineIds instanceof Set) {
        // Legacy BUG-624 contract: a Set means "did it fail on the single
        // immediately-preceding check" — preserved byte-for-byte, no
        // signature matching (the original contract never had one).
        graceable = !priorFailedLineIds.has(id);
        graceNote = `[BUG-624 grace: 1st consecutive divergence, tolerated pending re-check next tick]`;
      }
      // else: unrecognized shape — degrade silently to no-grace (graceable
      // stays false), never throws. See the doc comment above.
    }
    checks.push({
      id,
      ok: graceable,
      detail: graceable ? `${failDetail} ${graceNote}` : failDetail,
    });
  };

  // ===== SHAPE VALIDATION LAYER (existing) =====

  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) {
      checks.push({
        id: `colour.${b.id}.undefined-spec`,
        ok: false,
        detail: `${b.id}: spec "${b.spec}" undefined`,
      });
      continue;
    }
    if (!sp.color || typeof sp.color !== 'string') {
      checks.push({
        id: `colour.${b.id}.no-color`,
        ok: false,
        detail: `${b.id}: no color`,
      });
      continue;
    }
    checks.push({
      id: `colour.${b.id}.defined`,
      ok: true,
      detail: `${b.id}: ok`,
    });
  }

  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp || sp.category !== 'zones') continue;
    let tier: 1 | 2 | 3;
    try {
      tier = densityTier(sp);
    } catch (e) {
      checks.push({
        id: `tier.${b.id}.compute-error`,
        ok: false,
        detail: `${b.id}: tier compute failed`,
      });
      continue;
    }
    if (![1, 2, 3].includes(tier)) {
      checks.push({
        id: `tier.${b.id}.invalid-value`,
        ok: false,
        detail: `${b.id}: tier ${tier} invalid`,
      });
      continue;
    }
    if (!(tier in TIER_COLORS)) {
      checks.push({
        id: `tier.${b.id}.undefined-color`,
        ok: false,
        detail: `tier ${tier}: no color`,
      });
      continue;
    }
    checks.push({
      id: `tier.${b.id}.valid`,
      ok: true,
      detail: `${b.id}: t${tier}`,
    });
  }

  for (const b of s.buildings) {
    const found = b.spec in SPECS;
    checks.push({
      id: `spec.${b.id}.exists`,
      ok: found,
      detail: found ? `${b.id}: ok` : `${b.id}: ${b.spec} missing`,
    });
  }

  // ===== UNIVERSAL PLACEHOLDER CATCH (FEAT-1972079877) =====
  // A placeholder ("coming soon" roadmap type) is a valid SPECS entry — it has a
  // colour, dims, a family — so every shape check above PASSES for it. But a
  // placeholder must NEVER exist as a real building in the running sim: it is not
  // functional and was never meant to be placeable. This is the SINGLE
  // AUTHORITATIVE net that catches a placeholder building arriving via ANY path
  // (a crafted savepoint restore, a future unguarded insertion site, a hand-edited
  // debug-JSON load) — the reducer guards (place/stampRegion) and the restore
  // filter are defence-in-depth in FRONT of this catch-all. Any placeholder
  // building is an invalid state and fails consistency.
  //
  // ONE aggregate check (not one-per-building): the report is embedded in
  // debug.json, so a per-building entry would bloat it linearly at city scale
  // (the SIZE GUARD test). This fails if ANY building is a placeholder.
  const placeholderBuildings = s.buildings.filter((b) => SPECS[b.spec]?.placeholder === true);
  checks.push({
    id: 'placeholder.none-in-sim',
    ok: placeholderBuildings.length === 0,
    detail:
      placeholderBuildings.length === 0
        ? 'no placeholder buildings in sim'
        : `${placeholderBuildings.length} placeholder building(s) must not exist: ${placeholderBuildings
            .slice(0, 5)
            .map((b) => `#${b.id}(${b.spec})`)
            .join(', ')}${placeholderBuildings.length > 5 ? ', …' : ''}`,
  });

  const inflowLabels = new Set<string>();
  let inflowDupCount = 0;
  for (const f of s.lastFlows.inflows) {
    if (inflowLabels.has(f.label)) {
      inflowDupCount++;
    } else {
      inflowLabels.add(f.label);
    }
  }
  checks.push({
    id: 'flows.inflow-labels-unique',
    ok: inflowDupCount === 0,
    detail:
      inflowDupCount === 0
        ? `${s.lastFlows.inflows.length} inflow labels all unique`
        : `${inflowDupCount} duplicate inflow labels detected`,
  });

  const outflowLabels = new Set<string>();
  let outflowDupCount = 0;
  for (const f of s.lastFlows.outflows) {
    if (outflowLabels.has(f.label)) {
      outflowDupCount++;
    } else {
      outflowLabels.add(f.label);
    }
  }
  checks.push({
    id: 'flows.outflow-labels-unique',
    ok: outflowDupCount === 0,
    detail:
      outflowDupCount === 0
        ? `${s.lastFlows.outflows.length} outflow labels all unique`
        : `${outflowDupCount} duplicate outflow labels detected`,
  });

  for (let i = 0; i < s.lastFlows.inflows.length; i++) {
    const f = s.lastFlows.inflows[i];
    const ok = typeof f.value === 'number' && isFinite(f.value);
    checks.push({
      id: `flows.inflow[${i}].finite`,
      ok,
      detail: ok ? `in[${i}]: ok` : `in[${i}]: ${f.value}`,
    });
  }

  for (let i = 0; i < s.lastFlows.outflows.length; i++) {
    const f = s.lastFlows.outflows[i];
    const ok = typeof f.value === 'number' && isFinite(f.value);
    checks.push({
      id: `flows.outflow[${i}].finite`,
      ok,
      detail: ok ? `out[${i}]: ok` : `out[${i}]: ${f.value}`,
    });
  }

  const popOk = typeof s.population === 'number' && isFinite(s.population) && s.population >= 0;
  checks.push({
    id: 'sim.population.valid',
    ok: popOk,
    detail: popOk ? `Population = ${s.population}` : `Population invalid: ${s.population}`,
  });

  const fundsOk = typeof s.funds === 'number' && isFinite(s.funds);
  checks.push({
    id: 'sim.funds.valid',
    ok: fundsOk,
    detail: fundsOk ? `Funds = ${s.funds}` : `Funds invalid: ${s.funds}`,
  });

  const loanOk = typeof s.loanBalance === 'number' && isFinite(s.loanBalance) && s.loanBalance >= 0;
  checks.push({
    id: 'sim.loanBalance.valid',
    ok: loanOk,
    detail: loanOk ? `Loan balance = ${s.loanBalance}` : `Loan balance invalid: ${s.loanBalance}`,
  });

  const xpOk = typeof s.xp === 'number' && isFinite(s.xp) && s.xp >= 0;
  checks.push({
    id: 'sim.xp.valid',
    ok: xpOk,
    detail: xpOk ? `XP = ${s.xp}` : `XP invalid: ${s.xp}`,
  });

  const tickOk = typeof s.tick === 'number' && isFinite(s.tick) && s.tick >= 0 && Math.floor(s.tick) === s.tick;
  checks.push({
    id: 'sim.tick.valid',
    ok: tickOk,
    detail: tickOk ? `Tick = ${s.tick}` : `Tick invalid: ${s.tick}`,
  });

  const speedOk = s.speed === 0 || s.speed === 1 || s.speed === 2 || s.speed === 3;
  checks.push({
    id: 'sim.speed.valid',
    ok: speedOk,
    detail: speedOk ? `Speed = ${s.speed}` : `Speed invalid: ${s.speed}`,
  });

  for (let i = 0; i < s.ledger.length; i++) {
    const e = s.ledger[i];
    const tickOk = typeof e.tick === 'number' && isFinite(e.tick) && e.tick >= 0;
    const amountOk = typeof e.amount === 'number' && isFinite(e.amount);
    const labelOk = typeof e.label === 'string' && e.label.length > 0;
    const ok = tickOk && amountOk && labelOk;
    checks.push({
      id: `ledger[${i}].valid`,
      ok,
      detail: ok ? `led[${i}]: ok` : `led[${i}]: bad`,
    });
  }

  for (const b of s.buildings) {
    const xOk = typeof b.x === 'number' && isFinite(b.x) && b.x >= 0;
    const yOk = typeof b.y === 'number' && isFinite(b.y) && b.y >= 0;
    const ok = xOk && yOk;
    checks.push({
      id: `building.${b.id}.position`,
      ok,
      detail: ok ? `${b.id}: ok` : `${b.id}: bad pos`,
    });
  }

  const seenIds = new Set<number>();
  let dupIdCount = 0;
  for (const b of s.buildings) {
    if (seenIds.has(b.id)) {
      dupIdCount++;
    } else {
      seenIds.add(b.id);
    }
  }
  checks.push({
    id: 'buildings.ids-unique',
    ok: dupIdCount === 0,
    detail:
      dupIdCount === 0
        ? `${s.buildings.length} building IDs all unique`
        : `${dupIdCount} duplicate building IDs detected`,
  });

  const taxes = [
    { name: 'residential', val: s.taxRates.residential },
    { name: 'commercial', val: s.taxRates.commercial },
    { name: 'industrial', val: s.taxRates.industrial },
  ];
  for (const t of taxes) {
    const ok = typeof t.val === 'number' && isFinite(t.val) && t.val >= 0 && t.val <= 100;
    checks.push({
      id: `taxRates.${t.name}`,
      ok,
      detail: ok ? `${t.name} tax = ${t.val}%` : `${t.name} tax invalid: ${t.val}`,
    });
  }

  // ===== CROSS-DERIVATION LAYER =====

  // ===== 1. FUNDS-VS-FLOWS CONSERVATION (TICK-BOUNDARY INVARIANT, Round-6) =====
  // Conservation is checked using tick snapshots funcsAtTickStart/End, not working-tree
  // funds. This ensures between-tick mutations (debugXp, place, dev +10M) never
  // false-positive the check. We verify:
  // fundsAtTickEnd === funcsAtTickStart + Σinflows − Σoutflows
  const inflowSum = s.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const outflowSum = s.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  const expectedFundsAtEnd = s.fundsAtTickStart + inflowSum - outflowSum;
  const conservationOk = s.fundsAtTickEnd === expectedFundsAtEnd;
  checks.push({
    id: 'conservation.funds-vs-flows',
    ok: conservationOk,
    detail: conservationOk
      ? `fundsAtTickEnd ${s.fundsAtTickEnd} = start ${s.fundsAtTickStart} + flows ${inflowSum - outflowSum}`
      : `fundsAtTickEnd ${s.fundsAtTickEnd} ≠ expected ${expectedFundsAtEnd} (delta: ${s.fundsAtTickEnd - expectedFundsAtEnd})`,
  });

  // ===== 2. FLOWS-VS-RECOMPUTE: Cross-check key flows against recomputed values =====
  // Path A (actual): what's recorded in lastFlows — OR, on a BUG-603 retry, the
  // caller-supplied actualFlowsOverride recomputed fresh against the CURRENT
  // state (see runConsistencyChecks' doc comment). Conservation above never
  // uses this override; only the per-line checks below do.
  // Path B (derived): what we'd compute now from placed[] + policies

  // Recompute fiscal flows using shared formulas (fiscal.ts) for single source of truth.
  // BUG-520 (remaining part): this cross-check MUST use the same online-gated
  // count computeFlows() now uses for Business Tax, or a road-disconnected
  // commercial building would make this recompute diverge from the (correct)
  // actual flow and falsely redden the consistency gate.
  const c = countByKindOnline(s);
  const t = s.taxRates;
  const actualFlows = actualFlowsOverride ?? s.lastFlows;
  // BUG-419: recompute against the START-of-tick population the engine actually charged
  // (recorded in lastFlows.population), not the grown end-of-tick s.population. Fall back
  // to s.population for states recorded before this field existed.
  const flowBasisPop = actualFlowsOverride?.population ?? actualFlows.population ?? s.population;
  const recomputedCouncilTax = councilTaxPerTick(flowBasisPop, t.residential);
  const actualCouncilTax = actualFlows.inflows.find((f) => f.label === 'Council Tax')?.value ?? 0;
  const councilTaxOk = recomputedCouncilTax === actualCouncilTax;
  checks.push({
    id: 'flows.council-tax-matches',
    ok: councilTaxOk,
    detail: councilTaxOk
      ? `Council Tax: computed ${recomputedCouncilTax} = actual ${actualCouncilTax}`
      : `Council Tax diverged: computed ${recomputedCouncilTax} vs actual ${actualCouncilTax} (building removal? policy change?)`,
  });

  // Recompute Business Tax using shared formula (fiscal.ts).
  // NOTE: Business Tax can be reduced by brownout, so we allow some tolerance.
  // If it's LOWER than recomputed, that's OK (brownout). If HIGHER, that's a bug.
  const recomputedBusinessTax = businessTaxPerTick(c.commercial, t.commercial);
  const actualBusinessTax = actualFlows.inflows.find((f) => f.label === 'Business Tax')?.value ?? 0;
  const businessTaxOk = actualBusinessTax <= recomputedBusinessTax;
  checks.push({
    id: 'flows.business-tax-matches',
    ok: businessTaxOk,
    detail: businessTaxOk
      ? `Business Tax: actual ${actualBusinessTax} <= computed ${recomputedBusinessTax} (or brownout)`
      : `Business Tax: actual ${actualBusinessTax} > computed ${recomputedBusinessTax} (impossible without new businesses)`,
  });

  // Recompute Wages using shared formula (fiscal.ts).
  // FEAT-wage-stage1 (Q100067/Q100086, 2026-09-03): the engine now sources the
  // 'Wages' outflow from sectorWagesPerTick(filledJobsBySector(s)) — a
  // BUILDINGS-AND-POPULATION-derived figure (F1 FIX: filled jobs, capped at
  // the workforce, not raw vacancy-inclusive capacity), not the old
  // population-only wagesPerTick().
  //
  // SCALE-GATE FIX (independent round, 2026-09-03 — a real BUG-419-class
  // timing bug the 13k-building fixture's own consistency check caught):
  // BUILDINGS don't drift within a tick (matching Business Tax's existing
  // `c = countByKindOnline(s)` current-state recompute above), but
  // POPULATION DOES — computeFlows() inside advance() runs against the
  // START-of-tick population; migration/growth for THIS tick is applied
  // LATER, so by the time this check runs, `s.population` already reads the
  // GROWN end-of-tick figure. Recomputing the workforce cap against CURRENT
  // s.population (as a first cut of this fix did) overestimates it and
  // false-reds the check the same way BUG-419 originally did for the old
  // pure-population formula. Fixed by reusing the SAME flowBasisPop
  // (start-of-tick population, captured on lastFlows.population) already
  // established above for Council Tax, via filledJobsFromCapacityAndPopulation()
  // — capacity comes from CURRENT buildings (totalJobsBySector(s), safe,
  // buildings don't drift), the WORKFORCE CAP comes from flowBasisPop (the
  // historical snapshot the engine actually charged wages against).
  // BUG-422: the engine applies POLICY MULTIPLIERS to outflows AFTER building the raw
  // amount (austerity ×0.9; Wages is not in the recycling 0.93 set). Recompute the raw
  // wage, then run it through the SAME shared policy pipeline the engine used so the
  // comparison is post-policy vs post-policy.
  const recomputedWages = applyOutflowPolicies(
    [
      {
        label: 'Wages',
        value: sectorWagesPerTick(
          filledJobsFromCapacityAndPopulation(totalJobsBySector(s), flowBasisPop),
        ).totalPerTick,
      },
    ],
    s.policies,
  )[0].value;
  const actualWages = actualFlows.outflows.find((f) => f.label === 'Wages')?.value ?? 0;
  const wagesOk = recomputedWages === actualWages;
  pushGraceable(
    'flows.wages-matches',
    wagesOk,
    actualWages - recomputedWages,
    `Wages: computed ${recomputedWages} = actual ${actualWages}`,
    // F4 FIX (independent round, 2026-09-03): the old "(pop change without
    // reflow?)" wording dated from when Wages was purely population-based
    // (BUG-419). Wages is now buildings-AND-population derived (filled
    // jobs), so the honest divergence causes are a building change
    // (placed/demolished/came online or offline) OR a population change,
    // read against a stale (un-recomputed) actual flow.
    `Wages diverged: computed ${recomputedWages} vs actual ${actualWages} (building or population change without reflow?)`,
  );

  // Recompute total upkeep: sum of ONLINE building upkeeps only.
  // LIMITATION: This check verifies total upkeep magnitude but NOT per-bucket mapping
  // (e.g., roads upkeep stays within 'Roads' bucket, power stays in 'Power Grid', etc.).
  // A full per-bucket check would require tracking which flow label covers which UPKEEP_BUCKET.
  // BUG-414 FIX: exclude offline/under-construction buildings to match engine.computeFlows behavior.
  // BUG-422: recompute upkeep PER BUCKET LABEL (matching engine.computeFlows), then run
  // the buckets through the SAME shared policy pipeline. Per-label matters: the recycling
  // policy discounts only the service labels (Roads, Power Grid, ...) — a flat sum ×0.93
  // would be wrong. austerity then ×0.9 every bucket. Summing the post-policy per-bucket
  // values reproduces the aggregate the engine actually recorded.
  const upkeepBuckets: Record<string, number> = {};
  for (const b of s.buildings) {
    if (!isOnline(s, b)) continue; // Skip buildings still under construction
    const sp = SPECS[b.spec];
    if (!sp || !sp.upkeep) continue;
    // FEAT-2326609782 GENESIS FREE (2026-09-04): mirrors engine.computeFlows'
    // SAME upkeepChargeableOf() call exactly (data.ts SSOT) — genesis m20/rail
    // map furniture (builtTick<=0) pays no upkeep, so the recomputed total
    // here must exclude it too or this check would false-positive diverge.
    const upkeep = upkeepChargeableOf(b, sp);
    if (!upkeep) continue;
    const k = UPKEEP_BUCKET[sp.kind];
    if (k) upkeepBuckets[k] = (upkeepBuckets[k] ?? 0) + upkeep;
  }
  const upkeepEntries = Object.entries(upkeepBuckets)
    .filter(([, v]) => v > 0)
    .map(([label, value]) => ({ label, value }));
  const recomputedUpkeep = applyOutflowPolicies(upkeepEntries, s.policies).reduce(
    (a, e) => a + e.value,
    0,
  );
  // Sum actual upkeep from all outflow buckets (Roads, Power, Healthcare, etc.).
  // Excludes: Wages, Loan Interest, Overdraft Interest, Transit Subsidy (non-upkeep flows).
  let actualUpkeep = 0;
  for (const flow of actualFlows.outflows) {
    // FEAT-1972079907 inc2: 'Road Auto-Scale' is a monthly one-off capital spend
    // (road tier upgrades), NOT recurring building upkeep — exclude it from the
    // upkeep-total reconciliation exactly like Wages/interest/subsidy.
    if (
      flow.label !== 'Wages' &&
      flow.label !== 'Loan Interest' &&
      flow.label !== 'Overdraft Interest' &&
      flow.label !== 'Transit Subsidy' &&
      flow.label !== 'Road Auto-Scale' &&
      // FEAT-1972079878 inc1: 'Building Auto-Scale' is likewise a monthly one-off
      // capital spend (building capacity-tier upgrades), NOT recurring per-building
      // `upkeep` — exclude it from the upkeep-total reconciliation exactly like
      // Road Auto-Scale, or every scaled building double-counts its upgrade spend
      // as phantom recurring upkeep and the reconciliation goes red.
      flow.label !== 'Building Auto-Scale' &&
      flow.label !== 'Road Auto-Connect' &&
      // FEAT-1972079906 inc1: 'Refuse Collection' is a tonnage-based operating cost
      // (collected tonnes × rate), NOT recurring per-building `upkeep` — exclude it
      // from the upkeep-total reconciliation exactly like Wages / Road Auto-Scale.
      // (The depot's FIXED upkeep IS a normal Water & Waste bucket and stays in.)
      flow.label !== 'Refuse Collection' &&
      // FEAT-1972079906 inc2: 'Waste Disposal' is landfill TIPPING (landfilled tonnes ×
      // rate) — a tonnage-based operating cost, NOT recurring per-building `upkeep` —
      // exclude it from the upkeep-total reconciliation exactly like Refuse Collection.
      // (The processors' FIXED upkeep IS a normal Water & Waste bucket and stays in.)
      flow.label !== 'Waste Disposal' &&
      // FEAT-2326609711 inc1: 'Grid Import' is an external-tariff outflow keyed off
      // the power SHORTFALL (importedMW * tariff) — not a per-building `upkeep`
      // bucket — exclude it from the upkeep-total reconciliation exactly like
      // Refuse Collection / Waste Disposal (both other tonnage/shortfall-based
      // operating costs). Without this, any tick with an active Grid Import
      // outflow would falsely diverge this check (upkeepBuckets never includes it).
      flow.label !== GRID_IMPORT_OUTFLOW_LABEL &&
      // BUG-504 Option A: 'Bailout Standing Cost' is a credit-rating/interest
      // surcharge keyed off active-bailout state, NOT per-building `upkeep` —
      // exclude it from the upkeep-total reconciliation exactly like
      // Overdraft Interest / Wages.
      flow.label !== BAILOUT_STANDING_COST_LABEL
    ) {
      actualUpkeep += flow.value;
    }
  }
  const upkeepOk = recomputedUpkeep === actualUpkeep;
  pushGraceable(
    'flows.upkeep-total-matches',
    upkeepOk,
    actualUpkeep - recomputedUpkeep,
    `Upkeep total: computed ${recomputedUpkeep} = actual ${actualUpkeep} (per-bucket mapping NOT verified)`,
    `Upkeep total diverged: computed ${recomputedUpkeep} vs actual ${actualUpkeep} (building removed? online status change?)`,
  );

  // ===== 3. PALETTE CROSS-CHECK =====
  const specsUsedColors = new Set<string>();
  for (const sp of Object.values(SPECS)) {
    if (sp.color) specsUsedColors.add(sp.color);
  }
  let paletteFailure = false;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (sp && sp.color && !specsUsedColors.has(sp.color)) {
      paletteFailure = true;
      break;
    }
  }
  checks.push({
    id: 'palette.building-colors-defined',
    ok: !paletteFailure,
    detail: !paletteFailure
      ? `all ${s.buildings.length} buildings use defined colors`
      : `some buildings use undefined colors`,
  });

  const paletteOk =
    Array.isArray(PALETTE_FLAT) &&
    PALETTE_FLAT.length > 0 &&
    PALETTE_FLAT.every((id) => typeof id === 'string' && id in SPECS);
  checks.push({
    id: 'palette.flat-valid',
    ok: paletteOk,
    detail: paletteOk ? `PALETTE_FLAT has ${PALETTE_FLAT.length} entries` : `PALETTE_FLAT invalid`,
  });

  const failures = checks.filter((c) => !c.ok).length;
  return { checks, failures, rawFailedLineIds, rawFailedSignatures };
}
