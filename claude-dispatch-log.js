/**
 * claude-dispatch-log.js — pure logic for FEAT-076 (BOW mkey: tool.agentlog).
 *
 * Spec: docs/planning/acceptance/tool.agentlog.md. Mechanical agent
 * dispatch/stop logging + hourly utilisation report + under-utilisation nag,
 * approved by Aaron 2026-08-13 (target lanes = 12 per dev-team-process v1.5
 * caps; enforcement = measure + mechanical nag, no hard gate in v1).
 *
 * This module holds every piece of logic that does NOT need to be a hook
 * script: building the two event-row shapes, inserting them, sweeping the
 * event log into a concurrency timeline, bucketing that into an hourly
 * utilisation table, resolving the configured target lane count, rendering
 * the table, and deciding whether to nag. Every exported function is either
 * pure (no DB) or takes an already-open `conn` and does exactly one query —
 * none of it opens or closes a connection itself, so the whole file is
 * unit-testable with a mock `{ query() }` stub and zero DB (AC-24).
 *
 * `sync_dispatch_events` itself is created ONLY by claude-sync.js's
 * ensureSchema() (DDL lives there, verbatim from the AC file) — this module
 * assumes the table exists and lets ER_NO_SUCH_TABLE / connection errors
 * propagate to its caller, which is always a hook wrapping the call in its
 * own try/catch (fail-open at the CALL SITE, not buried in here — GR#1).
 *
 * Append-only by usage (AC-2): nothing in this file issues UPDATE or DELETE
 * against sync_dispatch_events.
 */

'use strict';

// Unmatched-dispatch cap (AC-13): a dispatch with no stop row within this
// window is treated as having ended at +capMs, so one crashed/forgotten
// agent can't inflate "running" forever. GR#15: this is the one documented
// default — nothing else in this file hardcodes a lane/time constant.
const DEFAULT_CAP_MS = 2 * 60 * 60 * 1000; // 2 hours

// GR#15: the only hardcoded target-lane constant in the whole feature —
// resolveTargetLanes() below always states in its return value whether this
// default was actually used, or a real project_meta override was found.
const DEFAULT_TARGET_LANES = 12;

const HOUR_MS = 60 * 60 * 1000;

/** Truncate a value to `max` chars for a VARCHAR column; null passes through. */
function truncate(value, max) {
  if (value === undefined || value === null) return null;
  const s = String(value);
  return s.length > max ? s.slice(0, max) : s;
}

/** Coerce a row's `ts` (Date, ISO string, or epoch ms number) to epoch ms. */
function toMs(ts) {
  if (ts instanceof Date) return ts.getTime();
  if (typeof ts === 'number') return ts;
  const parsed = Date.parse(ts);
  return Number.isFinite(parsed) ? parsed : Date.now();
}

/**
 * Build a `dispatch` row for sync_dispatch_events (AC-3). `bowCodes` may be
 * an array (comma-joined here) or an already-joined string — the guard hands
 * this its own already-extracted `codes` array, never re-derives them.
 */
function buildDispatchEvent({ sessionId, name, subagentType, description, bowCodes, promptChars } = {}) {
  const joinedCodes = Array.isArray(bowCodes) ? bowCodes.join(',') : bowCodes;
  return {
    event: 'dispatch',
    session_id: truncate(sessionId, 36),
    name: truncate(name, 16),
    subagent_type: truncate(subagentType, 64),
    description: truncate(description, 255),
    bow_codes: truncate(joinedCodes, 255),
    prompt_chars: Number.isFinite(promptChars) ? promptChars : null,
  };
}

/**
 * Build a `stop` row for sync_dispatch_events (AC-10). `name` is deliberately
 * NOT part of the stop event's identity — the stop hook doesn't reliably know
 * which identity dispatched the agent (that lived in the guard's own
 * resolveIdentity() call, a separate process); concurrency accounting
 * (sweepConcurrency) keys purely on `session_id`, so this is not a loss.
 */
function buildStopEvent({ sessionId, subagentType, description } = {}) {
  return {
    event: 'stop',
    session_id: truncate(sessionId, 36),
    name: null,
    subagent_type: truncate(subagentType, 64),
    description: truncate(description, 255),
    bow_codes: null,
    prompt_chars: null,
  };
}

/** Insert one already-built row. The only write path onto the table (AC-2). */
async function insertEvent(conn, row) {
  return conn.query(
    `INSERT INTO sync_dispatch_events
       (event, session_id, name, subagent_type, description, bow_codes, prompt_chars)
     VALUES (?, ?, ?, ?, ?, ?, ?)`,
    [row.event, row.session_id, row.name, row.subagent_type, row.description, row.bow_codes, row.prompt_chars]
  );
}

/**
 * Sweep the raw event log (`rows`: objects with at least
 * { event: 'dispatch'|'stop', ts, session_id, name }) into a per-session
 * concurrency accounting.
 *
 * AC-12: matching is done PER SESSION by a simple FIFO queue of "open"
 * dispatches — a stop pops the oldest open dispatch for that session. This
 * makes the resulting concurrency COUNT exact for two overlapping
 * same-session agents regardless of which specific dispatch a given stop is
 * "really" closing: the queue-depth (and therefore every derived interval's
 * contribution to the running total at any instant) is invariant under which
 * of several simultaneously-open dispatches a stop is popped against — only
 * the COUNT of open dispatches at each instant matters, never their identity.
 *
 * AC-13: a dispatch left open at the end of its session's event list (no
 * stop ever arrived) is closed synthetically at `open.ts + capMs` and counted
 * in the returned `unmatchedCount`. A stop with nothing open to match against
 * (an "orphan stop") is simply dropped — it can never drive anything
 * negative because it never contributes a delta at all.
 *
 * Returns `{ intervals, timeline, unmatchedCount }`:
 *   - intervals: [{ start, end, name, session_id, matched }] — one per
 *     dispatch, `matched` false for the capMs-synthetic-close case.
 *   - timeline: intervals flattened to sorted {ts, delta, name} points,
 *     ready for a running-total scan (see currentRunning below).
 */
function sweepConcurrency(rows, { capMs = DEFAULT_CAP_MS } = {}) {
  const bySession = new Map();
  for (const r of rows || []) {
    const sid = r.session_id || 'unknown';
    if (!bySession.has(sid)) bySession.set(sid, []);
    bySession.get(sid).push(r);
  }

  const intervals = [];
  let unmatchedCount = 0;

  for (const evs of bySession.values()) {
    const sorted = [...evs].sort((a, b) => toMs(a.ts) - toMs(b.ts));
    const openQueue = [];
    for (const e of sorted) {
      const ts = toMs(e.ts);
      if (e.event === 'dispatch') {
        openQueue.push({ ts, name: e.name, session_id: e.session_id });
      } else if (e.event === 'stop') {
        if (openQueue.length) {
          const open = openQueue.shift();
          intervals.push({ start: open.ts, end: ts, name: open.name, session_id: open.session_id, matched: true });
        }
        // else: orphan stop — no open dispatch to close, dropped (AC-13).
      }
    }
    // Whatever is still open never got a stop — synthetic capMs close (AC-13).
    for (const open of openQueue) {
      unmatchedCount++;
      intervals.push({
        start: open.ts,
        end: open.ts + capMs,
        name: open.name,
        session_id: open.session_id,
        matched: false,
      });
    }
  }

  const timeline = [];
  for (const iv of intervals) {
    timeline.push({ ts: iv.start, delta: 1, name: iv.name });
    timeline.push({ ts: iv.end, delta: -1, name: iv.name });
  }
  timeline.sort((a, b) => a.ts - b.ts);

  return { intervals, timeline, unmatchedCount };
}

/**
 * Running lane count at `now` (default: current time), floored at 0, plus a
 * per-identity breakdown. Used by the stop hook's nag computation.
 */
function currentRunning(rows, { capMs = DEFAULT_CAP_MS, now = Date.now() } = {}) {
  const { timeline } = sweepConcurrency(rows, { capMs });
  let running = 0;
  const byName = {};
  for (const t of timeline) {
    if (t.ts > now) continue;
    running += t.delta;
    const key = t.name || 'unknown';
    byName[key] = (byName[key] || 0) + t.delta;
  }
  running = Math.max(0, running);
  for (const k of Object.keys(byName)) byName[k] = Math.max(0, byName[k]);
  return { running, byName };
}

function overlapMs(aStart, aEnd, bStart, bEnd) {
  return Math.max(0, Math.min(aEnd, bEnd) - Math.max(aStart, bStart));
}

/** avg (time-weighted concurrency) + peak (max simultaneous) for one set of
 *  intervals restricted to [bucketStart, bucketEnd). */
function bucketWindowStats(intervals, bucketStart, bucketEnd) {
  const dur = bucketEnd - bucketStart;
  if (dur <= 0) return { avg: 0, peak: 0 };

  let weighted = 0;
  const points = new Set([bucketStart, bucketEnd]);
  for (const iv of intervals) {
    weighted += overlapMs(iv.start, iv.end, bucketStart, bucketEnd);
    if (iv.start > bucketStart && iv.start < bucketEnd) points.add(iv.start);
    if (iv.end > bucketStart && iv.end < bucketEnd) points.add(iv.end);
  }
  const sortedPoints = [...points].sort((a, b) => a - b);
  let peak = 0;
  for (let i = 0; i < sortedPoints.length - 1; i++) {
    const mid = (sortedPoints[i] + sortedPoints[i + 1]) / 2;
    let count = 0;
    for (const iv of intervals) if (iv.start <= mid && iv.end > mid) count++;
    if (count > peak) peak = count;
  }
  return { avg: weighted / dur, peak };
}

/**
 * AC-14: bucket the swept event log into per-hour, per-identity rows plus a
 * TEAM rollup. An interval spanning an hour boundary contributes its
 * time-weighted average to EACH hour it overlaps (bucketWindowStats above),
 * not just the hour it started in. Hours with zero events still yield a TEAM
 * zero row — sags must be visible, not skipped (AC-14).
 *
 * Returns a flat array of row objects, ordered oldest-hour-first, TEAM row
 * first within each hour followed by identities alphabetically:
 *   { hourStart, hourLabel, who, disp, done, peak, avg, util }
 * `util` is a fraction (avg / targetLanes), not yet a percentage string —
 * formatUtilTable is the only thing that renders it for display.
 */
function bucketHours(rows, { hours = 12, capMs = DEFAULT_CAP_MS, now = Date.now(), targetLanes = DEFAULT_TARGET_LANES } = {}) {
  const { intervals } = sweepConcurrency(rows, { capMs });

  const endBoundary = Math.ceil(now / HOUR_MS) * HOUR_MS;
  const startBoundary = endBoundary - hours * HOUR_MS;

  const names = [...new Set(intervals.map((iv) => iv.name).filter(Boolean))].sort();

  const out = [];
  for (let bucketStart = startBoundary; bucketStart < endBoundary; bucketStart += HOUR_MS) {
    const bucketEnd = bucketStart + HOUR_MS;
    const hourLabel = new Date(bucketStart).toISOString().slice(0, 13).replace('T', ' ') + ':00';

    // TEAM row: always emitted, even with zero events (AC-14).
    const teamIntervals = intervals.filter((iv) => iv.end > bucketStart && iv.start < bucketEnd);
    const teamDisp = intervals.filter((iv) => iv.start >= bucketStart && iv.start < bucketEnd).length;
    const teamDone = intervals.filter((iv) => iv.matched && iv.end >= bucketStart && iv.end < bucketEnd).length;
    const teamStats = bucketWindowStats(teamIntervals, bucketStart, bucketEnd);
    out.push({
      hourStart: bucketStart,
      hourLabel,
      who: 'TEAM',
      disp: teamDisp,
      done: teamDone,
      peak: teamStats.peak,
      avg: teamStats.avg,
      util: targetLanes > 0 ? teamStats.avg / targetLanes : 0,
    });

    for (const name of names) {
      const nameIntervals = intervals.filter((iv) => iv.name === name && iv.end > bucketStart && iv.start < bucketEnd);
      if (!nameIntervals.length) continue; // no zero-rows for individuals, only TEAM (AC-14)
      const disp = nameIntervals.filter((iv) => iv.start >= bucketStart && iv.start < bucketEnd).length;
      const done = nameIntervals.filter((iv) => iv.matched && iv.end >= bucketStart && iv.end < bucketEnd).length;
      const stats = bucketWindowStats(nameIntervals, bucketStart, bucketEnd);
      out.push({
        hourStart: bucketStart,
        hourLabel,
        who: name,
        disp,
        done,
        peak: stats.peak,
        avg: stats.avg,
        util: targetLanes > 0 ? stats.avg / targetLanes : 0,
      });
    }
  }
  return out;
}

/**
 * AC-15: read the configured target lane count from project_meta (schema:
 * meta_key/meta_value, already live on this DB — see CLAUDE.md). Absent row
 * OR absent table (a fresh/test DB that never got a value set) both fall
 * back to DEFAULT_TARGET_LANES, and the return value always states which
 * happened, so callers/output can be honest about the source (GR#15).
 */
async function resolveTargetLanes(conn) {
  try {
    const [rows] = await conn.query(
      `SELECT meta_value FROM project_meta WHERE meta_key = 'dispatch_target_lanes'`
    );
    if (rows && rows.length) {
      const v = Number(rows[0].meta_value);
      if (Number.isFinite(v) && v > 0) {
        return { target: v, source: 'project_meta.dispatch_target_lanes' };
      }
    }
  } catch (err) {
    // Missing table is a legitimate "nothing configured yet" state, not an
    // error — anything else is a real DB problem and must propagate so the
    // caller's own fail-open/fail-tolerant wrapper can decide what to do.
    if (err && err.code !== 'ER_NO_SUCH_TABLE') throw err;
  }
  return { target: DEFAULT_TARGET_LANES, source: `default (project_meta.dispatch_target_lanes not set, using ${DEFAULT_TARGET_LANES})` };
}

function pad(value, width) {
  return String(value).padEnd(width);
}

/**
 * AC-16: render bucketHours()' rows as a padded table (printBowSummary
 * style — see claude-bow.js), columns hour/who/disp/done/peak/avg/util%.
 * Golden-string tested: keep column widths/format stable, this is asserted
 * character-for-character by claude-dispatch-log.test.js.
 */
function formatUtilTable(rows) {
  const HOUR_W = 16, WHO_W = 8, DISP_W = 5, DONE_W = 5, PEAK_W = 5, AVG_W = 6, UTIL_W = 6;
  const header = `${pad('HOUR', HOUR_W)}${pad('WHO', WHO_W)}${pad('DISP', DISP_W)}${pad('DONE', DONE_W)}${pad('PEAK', PEAK_W)}${pad('AVG', AVG_W)}${pad('UTIL%', UTIL_W)}`;
  const lines = [header];
  for (const r of rows) {
    lines.push(
      pad(r.hourLabel, HOUR_W) +
      pad(r.who, WHO_W) +
      pad(String(r.disp), DISP_W) +
      pad(String(r.done), DONE_W) +
      pad(String(r.peak), PEAK_W) +
      pad(r.avg.toFixed(1), AVG_W) +
      pad(`${(r.util * 100).toFixed(1)}%`, UTIL_W)
    );
  }
  return lines.join('\n');
}

/**
 * AC-20: decide whether to nag. No nag when running >= target (already
 * saturated) or readyCount <= 0 (nothing to load up with) — exactly the two
 * conditions the AC names. Returns the exact PostToolUse hookSpecificOutput
 * JSON shape, or null when no nag applies.
 */
function buildNag(running, target, readyCount, hookEventName = 'PostToolUse') {
  if (running >= target || readyCount <= 0) return null;
  return {
    hookSpecificOutput: {
      hookEventName,
      additionalContext:
        `UTILISATION: ${running}/${target} lanes running, ${readyCount} ready BOW items — ` +
        `standing order is load up, never hold. Dispatch until saturated or name the blocker.`,
    },
  };
}

module.exports = {
  DEFAULT_CAP_MS,
  DEFAULT_TARGET_LANES,
  buildDispatchEvent,
  buildStopEvent,
  insertEvent,
  sweepConcurrency,
  currentRunning,
  bucketHours,
  resolveTargetLanes,
  formatUtilTable,
  buildNag,
  toMs,
};
