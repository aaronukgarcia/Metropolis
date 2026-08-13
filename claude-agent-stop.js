/**
 * PostToolUse hook — agent stop logging (FEAT-076, BOW mkey: tool.agentlog).
 *
 * Spec: docs/planning/acceptance/tool.agentlog.md. Companion to
 * claude-dispatch-guard.js's dispatch-side logging: where that PreToolUse
 * guard records the START of an Agent dispatch, this PostToolUse hook
 * records its END, so claude-dispatch-log.js's sweepConcurrency() can turn
 * the two into a real running-lane count instead of trusting anyone's
 * memory of what's still in flight.
 *
 * Wired in .claude/settings.json as a PostToolUse entry with matcher
 * "Agent" (AC-9). AC-11's documented fallback: if a live smoke shows
 * PostToolUse does not actually fire for backgrounded Agent dispatches, this
 * exact script is re-wired under a SubagentStop matcher instead — a
 * settings.json-only change. That's why this script never depends on any
 * PostToolUse-only field except as OPTIONAL metadata (tool_result / the
 * PostToolUse-specific envelope fields aren't read at all below; only
 * session_id and tool_input's subagent_type/description are used, and both
 * of those are plain hook-payload fields present under either event).
 *
 * No 4th connect() implementation (GR#3): reuses claude-dispatch-guard.js's
 * exported connect() (host default already fixed to 127.0.0.1 there) —
 * claude-sync.js keeps its own, unrelated connect() for session coordination.
 *
 * FAIL-OPEN, same posture as claude-dispatch-guard.js: PostToolUse hooks
 * can't block anything anyway, but every failure path here still exits 0
 * with a one-line stderr note rather than throwing — a DB outage or an
 * absent sync_dispatch_events table (pre-migration window) must never be
 * visible as an error to the agent that triggered this hook.
 *
 * Deliberate disable: CLAUDE_DISABLE_DISPATCH_GUARD=1 (same single kill
 * switch as the guard — AC-6's "along with the guard" scope covers this hook
 * too).
 *
 * Receives JSON on stdin: { tool_name | tool, session_id, tool_input: {
 *   subagent_type, description, ... }, ... } — this repo's own
 *   claude-dispatch-guard.js established `tool` as the PreToolUse field
 *   name; PostToolUse payload naming isn't otherwise exercised anywhere in
 *   this repo, so both `tool_name` and `tool` are accepted here defensively.
 * Emits on stdout (only when a nag applies): { hookSpecificOutput: {
 *   hookEventName: "PostToolUse", additionalContext: "..." } }
 */

'use strict';

const fs = require('fs');
const { connect } = require('./claude-dispatch-guard.js');

// AC-13's default matches claude-dispatch-log.js's own DEFAULT_CAP_MS — kept
// as a separate require-time constant only for the recent-events query
// window below (must be at least capMs so no still-open dispatch falls out
// of the query before its synthetic close is due).
const LOOKBACK_MS = 24 * 60 * 60 * 1000; // 24h — comfortably >= any capMs

function readStdin() {
  try {
    return fs.readFileSync(0, 'utf8');
  } catch {
    return '';
  }
}

function exitOpen(reason) {
  if (reason) process.stderr.write(`agent-stop: ${reason}\n`);
  process.exit(0);
}

async function main() {
  if (process.env.CLAUDE_DISABLE_DISPATCH_GUARD === '1') exitOpen();

  let payload;
  try {
    payload = JSON.parse(readStdin() || '{}');
  } catch {
    exitOpen('unparsable stdin, nothing logged');
    return;
  }

  // AC-11 fallback wiring (LIVE as of 2026-08-13): PostToolUse fires at
  // LAUNCH for backgrounded Agent dispatches (proven by a stop row landing
  // the instant an agent was dispatched, before it finished), so this script
  // now runs under SubagentStop instead. A SubagentStop payload carries NO
  // tool/tool_input fields — the event itself is the "a subagent finished"
  // signal, so it is never gated on them; they stay optional metadata for
  // the PostToolUse shape (still accepted, for a manual re-wire back).
  const hookEvent = payload.hook_event_name || '';
  if (hookEvent !== 'SubagentStop') {
    const toolName = payload.tool_name || payload.tool || '';
    if (toolName !== 'Agent') exitOpen();
    // AC-10: missing tool_input entirely -> exit 0, no row, no exception.
    if (!payload.tool_input || typeof payload.tool_input !== 'object') exitOpen();
  }

  const toolInput =
    payload.tool_input && typeof payload.tool_input === 'object' ? payload.tool_input : {};
  const sessionId = payload.session_id || process.env.CLAUDE_SESSION_ID || null;
  const subagentType = toolInput.subagent_type;
  const description = toolInput.description;

  let conn;
  try {
    conn = await connect();
  } catch (err) {
    exitOpen(`cannot connect to metro MariaDB, stop event not logged — ${err.message}`);
    return;
  }

  try {
    const { buildStopEvent, insertEvent, currentRunning, resolveTargetLanes, buildNag } =
      require('./claude-dispatch-log.js');

    // DIVISION OF LABOUR (lead ruling, 2026-08-13, after live evidence that a
    // SubagentStop hook's JSON output is delivered back to the DYING subagent
    // — re-waking finished agents with the nag and burning tokens, the exact
    // thing FEAT-076 exists to stop):
    //   SubagentStop  -> insert the stop row. NO nag output (wrong audience).
    //   PostToolUse   -> NO row (it fires at LAUNCH for backgrounded Agent
    //                    dispatches, so a row here double-counts). Nag only —
    //                    its additionalContext is proven to reach the
    //                    dispatching session's context.
    if (hookEvent === 'SubagentStop') {
      try {
        await insertEvent(conn, buildStopEvent({ sessionId, subagentType, description }));
      } catch (err) {
        // ER_NO_SUCH_TABLE: sync_dispatch_events only exists after claude-sync.js's
        // ensureSchema() has run at least once (DDL lives there exclusively, per
        // architecture) -- a stderr note, never a thrown error, per hook.
        process.stderr.write(`agent-stop: stop-event log failed (non-fatal) — ${err.message}\n`);
      }
    } else {
      // AC-21: the nag is entirely best-effort — any error here means "no
      // nag", never a retry or a crash.
      try {
        const nag = await maybeNag(conn, { currentRunning, resolveTargetLanes, buildNag }, 'PostToolUse');
        if (nag) process.stdout.write(JSON.stringify(nag));
      } catch (err) {
        process.stderr.write(`agent-stop: nag computation failed (non-fatal, no nag) — ${err.message}\n`);
      }
    }
  } finally {
    try {
      await conn.end();
    } catch {
      /* closing an already-dead connection must not fail the hook */
    }
  }

  process.exit(0);
}

/**
 * AC-20: measured running lanes + open ready-work count -> buildNag(). Ready
 * count reuses claude-bow.js's `ready` command semantics (open/in_progress
 * items with no open/in_progress/blocked dependency) — see cmdReady in
 * claude-bow.js for the canonical version. Reimplemented here (not
 * imported) deliberately: this hook must never modify claude-bow.js, and
 * the one SELECT is small enough that duplicating it is safer than adding a
 * cross-file coupling to a script this feature doesn't otherwise touch.
 *
 * Returns the nag object (or null) rather than writing to stdout itself —
 * keeps this function pure-enough to unit test against a mock conn without
 * capturing process.stdout; main() does the one, real stdout write.
 * hookEventName flows into the emitted hookSpecificOutput so the JSON names
 * the event it actually ran under (SubagentStop in the live wiring).
 */
async function maybeNag(conn, { currentRunning, resolveTargetLanes, buildNag }, hookEventName = 'PostToolUse') {
  const since = new Date(Date.now() - LOOKBACK_MS);
  const [rows] = await conn.query(
    `SELECT event, ts, session_id, name FROM sync_dispatch_events WHERE ts >= ? ORDER BY ts`,
    [since]
  );
  const { running } = currentRunning(rows, { now: Date.now() });

  const [readyRows] = await conn.query(
    `SELECT COUNT(*) AS n FROM bow_items i
     WHERE i.status IN ('open','in_progress') AND NOT EXISTS (
       SELECT 1 FROM bow_dependencies d
       JOIN bow_items di ON di.guid = d.depends_on_guid
       WHERE d.item_guid = i.guid AND di.status IN ('open','in_progress','blocked'))`
  );
  const readyCount = Number((readyRows[0] || {}).n || 0);

  const { target } = await resolveTargetLanes(conn);
  // Tester finding (2026-08-13): this 4th argument was being dropped, so the
  // emitted JSON claimed PostToolUse under the live SubagentStop wiring —
  // forward it so hookSpecificOutput names the event that actually fired.
  return buildNag(running, target, readyCount, hookEventName);
}

if (require.main === module) {
  main().catch((err) => {
    process.stderr.write(`agent-stop: internal error, exiting open — ${err.message}\n`);
    process.exit(0);
  });
}

module.exports = { maybeNag };
