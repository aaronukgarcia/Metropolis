// consolidatorAudit.ts — CONSOLIDATOR AUDIT TRAIL (Aaron's ruling on
// FEAT-2326609761, via the BOW item's comments, 2026-09-04): "have the
// consolidator build its own audit of what it discovers, and then what it's
// planning — in the square and at the overall big-picture level — and then
// what it implements. Log it all to Maria, and [the lead] keeps an eye on
// its progress to make sure it does not go mad."
//
// This is the CLIENT half of the audit trail: a thin, fire-and-forget POST
// to the same metro MariaDB debug sink (tools/debugsink/server.js) backend.ts
// already talks to, at the NEW /api/consolidator/audit route.
//
// CALL SITE (independent-round finding 1, 2026-09-04, "TAB-MOUNTED-ONLY"):
// the FIRST landing called this from ConsolidatorTab's own React effect,
// which only runs while that tab is the LeftDock's active body — Aaron's
// ruling is "the audit runs while the CONSOLIDATOR is enabled", not "while
// its tab is visible", so switching to any other tab silently produced gaps
// in the trail during ordinary play even though the consolidator stayed on.
// Fixed by moving the actual posting call site to a store-level subscriber
// in store.tsx (an effect gated on state.consolidatorEnabled, living
// alongside the autosave timer / engineLag instrumentation — see that
// effect's own comment), which runs for as long as the toggle is on
// regardless of what the player is looking at. ConsolidatorTab still renders
// its own display (buildFrame et al.) on its own refresh cadence — it just
// no longer generates the audit trail. Nothing in THIS file changed for that
// fix; it was purely a caller relocation.
//
// It mirrors backend.ts's postToSink discipline EXACTLY:
//   - a 1.5s timeout (same DEBUGSINK_TIMEOUT_MS budget as backend.ts);
//   - NEVER throws — every failure (network error, non-2xx, timeout/abort,
//     or anything else) resolves `false`, never rejects;
//   - the sink being down/slow/absent has ZERO effect on the game — this
//     module does no SimState work and blocks nothing.
//
// NO RETRY QUEUE (deliberate, and different from commitqueue.ts): a debug
// snapshot is a unique, irreplaceable artefact (BUG-607's whole reason for
// existing), so backend.ts's commitDebug queues it in localStorage on
// failure and drains it later — losing one would lose real diagnostic data.
// A consolidator-audit ROW is not: it is fully re-derivable from SimState at
// any later point (consolidator.ts's own functions are pure folds over
// state), so a dropped post is a GAP in the oversight trail's time series,
// never a loss of the underlying data. Building a second localStorage-backed
// persistent queue for a strictly lower-stakes, purely-informational
// artefact would be disproportionate machinery for what it protects — DROP-
// ON-FAILURE is the deliberate choice here. (If a future increment wants
// gap-free audit history, `commitqueue.ts`'s enqueue/drain shape is directly
// reusable — nothing here forecloses that, it's just not built until an
// actual need for it shows up.)
//
// THROTTLE: gated by the SIMULATED month, never wall-clock and never
// per-tick (the task's "once per month-boundary audit, never per tick").
// `isAuditDue` compares the caller-supplied twelfth index (derived from
// SimState.tick via consolidator.ts's monthlyScopeOf — see consolidatorTab.tsx)
// against the last twelfth a post for that STAGE actually succeeded on. A
// fast-forwarded game producing many ticks per real second, or a tab
// re-rendering on its own 5s wall-clock refresh, still posts each stage AT
// MOST once per simulated month. The cursor lives in memory only (not
// localStorage) — a page reload simply re-posts the current month once more,
// which the server's ON DUPLICATE KEY UPDATE id = id upsert (id is derived
// from stage+tick) makes an idempotent no-op, never a duplicate row.
//
// NO Date.now IN THIS MODULE'S OWN LOGIC (the capture-path/hot-path rule):
// the throttle decision (isAuditDue) is a pure comparison of two integers
// (twelfth indices), never a clock read. The wall-clock `at` timestamp this
// module sends to the server is supplied BY THE CALLER (consolidatorTab.tsx,
// a UI component already reading Date.now for its own 5s wall-clock refresh
// throttle via throttle.ts's nextRefreshDue) — mirroring backend.ts's own
// commitDebug, which likewise takes its `new Date().toISOString()` at the
// UI call site, not inside a sim/hot path. Nothing in consolidator.ts's pure
// analysis functions is touched by this file.

import type { MonthlyScope } from './consolidator';

/**
 * Same server, same port, new route — see tools/debugsink/server.js's
 * /api/consolidator/audit (POST + GET). Absolute 127.0.0.1 URL for the same
 * reason backend.ts's DEBUGSINK_URL is absolute: works identically under
 * `vite dev`/`vite preview`/any static host, no proxy needed (loopback-only,
 * CORS-open sink).
 */
const AUDIT_URL = 'http://127.0.0.1:8642/api/consolidator/audit';

/** Same generous-but-bounded timeout budget as backend.ts's DEBUGSINK_TIMEOUT_MS. */
const AUDIT_TIMEOUT_MS = 1500;

/**
 * Wall-clock poll cadence for the store-level audit subscriber (store.tsx —
 * see its own comment for the TAB-MOUNTED-ONLY fix this constant supports,
 * independent-round finding 1, 2026-09-04). Same cadence ConsolidatorTab
 * used for its own display refresh before this moved — a poll, never a
 * per-tick check; the ACTUAL post still only happens once per simulated
 * month (isAuditDue), this just bounds how often the cheap "is it due yet"
 * check itself runs.
 */
export const AUDIT_POLL_MS = 5000;

/** Same top-N cap ConsolidatorTab's own display used (TOP_LIMIT) — kept here so the store subscriber and the tab agree without either importing the other. */
export const AUDIT_TOP_LIMIT = 20;

export type ConsolidatorAuditStage = 'discovered' | 'planned' | 'implemented';

/**
 * Last twelfth (0..11) a post for this stage actually SUCCEEDED on. Absent
 * key = never posted this session. In-memory only — see file header for why
 * that's fine (idempotent upsert on reload).
 */
const lastPostedTwelfth: Partial<Record<ConsolidatorAuditStage, number>> = {};

/**
 * The throttle gate: true iff `stage` has not already been successfully
 * posted for this exact simulated month (twelfth index). Exported so the
 * throttle itself is independently testable without a network call.
 */
export function isAuditDue(stage: ConsolidatorAuditStage, scope: Pick<MonthlyScope, 'twelfth'>): boolean {
  return lastPostedTwelfth[stage] !== scope.twelfth;
}

/** Test-only seam: clear the in-memory throttle cursor for every stage. */
export function resetConsolidatorAuditThrottle(): void {
  (Object.keys(lastPostedTwelfth) as ConsolidatorAuditStage[]).forEach((k) => {
    delete lastPostedTwelfth[k];
  });
}

/**
 * POST one raw audit event. Mirrors backend.ts's postToSink line for line:
 * AbortController + timeout, `res.ok` is the only success signal, every
 * failure mode (network error, non-2xx, timeout/abort, a throwing `fetch`
 * itself) is caught and reported as `false` — this function cannot reject.
 */
async function postAuditEvent(
  id: string,
  stage: ConsolidatorAuditStage,
  at: string,
  payload: unknown,
): Promise<boolean> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), AUDIT_TIMEOUT_MS);
  try {
    const res = await fetch(AUDIT_URL, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id, stage, at, payload }),
      signal: controller.signal,
    });
    return res.ok;
  } catch {
    return false;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Fire-and-forget, throttled audit post — the seam every audit-producing
 * call site uses (today: ConsolidatorTab's read-only 'discovered'/'planned'
 * posts; once the mutation lane lands, its ConsolidationPass will call this
 * same function with stage: 'implemented' after each pass actually runs).
 *
 * `tick` + `scope` identify the pass this event belongs to and drive both
 * the throttle gate and the posted event's id (`AUD-<STAGE>-<tick>`, stable
 * and deterministic — a retried post for the SAME pass upserts idempotently
 * server-side rather than minting a new row). `atIso` is the caller-supplied
 * wall-clock timestamp (see file header: never read here).
 *
 * Never throws (GR#1): an unexpected error anywhere in this function's own
 * body — not just inside postAuditEvent, which already can't throw — still
 * resolves `false` rather than escaping to the caller. The caller is free to
 * fire this without awaiting (`void postConsolidatorAudit(...)`) exactly as
 * a telemetry call should be used.
 */
export async function postConsolidatorAudit(
  stage: ConsolidatorAuditStage,
  scope: Pick<MonthlyScope, 'twelfth'>,
  tick: number,
  payload: unknown,
  atIso: string,
): Promise<boolean> {
  try {
    if (!isAuditDue(stage, scope)) return false; // throttled, not a failure — already posted this simulated month
    const id = `AUD-${stage.slice(0, 4).toUpperCase()}-${tick}`;
    const ok = await postAuditEvent(id, stage, atIso, payload);
    if (ok) lastPostedTwelfth[stage] = scope.twelfth;
    return ok;
  } catch {
    return false;
  }
}
