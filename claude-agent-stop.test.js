/**
 * claude-agent-stop.test.js — unit tests for claude-agent-stop.js
 * (FEAT-076, BOW mkey: tool.agentlog).
 *
 * The hook's `main()` reads real stdin and calls process.exit(), so it isn't
 * directly unit-testable in-process (same posture as claude-dispatch-guard.js,
 * which only exports its pure helpers). This file exercises the one exported
 * function, `maybeNag`, against a mock `{ query() }` conn stub that dispatches
 * on the SQL text — no live DB, per AC-24. Ready-count / running-lane /
 * buildNag logic itself is already covered by claude-dispatch-log.test.js;
 * these tests are about maybeNag's OWN wiring (which query answers which
 * question, and that a real ready count truly can withhold or trigger a nag).
 *
 * Run: node --test claude-agent-stop.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const path = require('node:path');

const { maybeNag } = require('./claude-agent-stop.js');
const { currentRunning, resolveTargetLanes, buildNag } = require('./claude-dispatch-log.js');

// ── Spawn-based main() gating tests (Tester bounce, 2026-08-13) ────────────
//
// maybeNag() (below) only covers what happens AFTER main() decides to log —
// it never touched main()'s own payload-shape gating (PostToolUse vs the now
// LIVE SubagentStop wiring — see AC-11 fallback note in claude-agent-stop.js).
// These run the real script as a child process (no way to unit-test
// process.exit()-driven control flow in-process) with stdin piped and
// METRO_DB_PORT pointed at a dead port (1 — nothing listens there, so
// connect() fails almost instantly with ECONNREFUSED). That dead port is
// what makes "did this reach connect() or not" observable from OUTSIDE the
// process without a live DB: any path that gates OUT before connect() never
// prints the "cannot connect to metro MariaDB" stderr note; any path that
// gates IN always does. No live DB is used anywhere in this file.
const SCRIPT = path.join(__dirname, 'claude-agent-stop.js');

/** Spawn the real hook script with `payload` piped as JSON stdin. */
function runHook(payload, envOverrides = {}) {
  return spawnSync(process.execPath, [SCRIPT], {
    input: JSON.stringify(payload),
    cwd: __dirname,
    encoding: 'utf8',
    timeout: 8000,
    env: { ...process.env, ...envOverrides },
  });
}

/** Build a mock conn whose query() answers based on which table the SQL
 *  string mentions — mirrors the two real queries maybeNag issues. */
function mockConn({ dispatchRows = [], readyCount = 0, metaRows = [] } = {}) {
  return {
    query: async (sql) => {
      if (/sync_dispatch_events/.test(sql)) return [dispatchRows];
      if (/bow_items/.test(sql)) return [[{ n: readyCount }]];
      if (/project_meta/.test(sql)) return [metaRows];
      throw new Error(`mockConn: unexpected query — ${sql}`);
    },
  };
}

const helpers = { currentRunning, resolveTargetLanes, buildNag };

test('maybeNag returns a nag when measured running is under target and ready work exists', async () => {
  const conn = mockConn({ dispatchRows: [], readyCount: 4, metaRows: [{ meta_value: '12' }] });
  const nag = await maybeNag(conn, helpers);
  assert.ok(nag);
  assert.equal(nag.hookSpecificOutput.hookEventName, 'PostToolUse');
  assert.match(nag.hookSpecificOutput.additionalContext, /UTILISATION: 0\/12 lanes running, 4 ready BOW items/);
});

test('maybeNag returns null when there is no ready work, even if running is 0', async () => {
  const conn = mockConn({ dispatchRows: [], readyCount: 0, metaRows: [{ meta_value: '12' }] });
  const nag = await maybeNag(conn, helpers);
  assert.equal(nag, null);
});

test('maybeNag returns null when measured running already meets/exceeds target', async () => {
  const now = Date.now();
  const dispatchRows = [
    { event: 'dispatch', ts: now - 1000, session_id: 's1', name: 'bill' },
    { event: 'dispatch', ts: now - 1000, session_id: 's2', name: 'bob' },
  ];
  const conn = mockConn({ dispatchRows, readyCount: 10, metaRows: [{ meta_value: '2' }] });
  const nag = await maybeNag(conn, helpers);
  assert.equal(nag, null); // running=2 >= target=2
});

test('maybeNag computes running from the SAME dispatch/stop rows sweepConcurrency uses (real currentRunning wiring, not a stub)', async () => {
  const now = Date.now();
  const dispatchRows = [
    { event: 'dispatch', ts: now - 5000, session_id: 's1', name: 'bill' },
    { event: 'stop', ts: now - 1000, session_id: 's1' }, // already closed — 0 running
  ];
  const conn = mockConn({ dispatchRows, readyCount: 3, metaRows: [] }); // no project_meta row -> default target 12
  const nag = await maybeNag(conn, helpers);
  assert.ok(nag);
  assert.match(nag.hookSpecificOutput.additionalContext, /UTILISATION: 0\/12 lanes running, 3 ready BOW items/);
});

test('maybeNag issues exactly the ready-work SELECT shape claude-bow.js\'s `ready` command uses (open/in_progress, no open/in_progress/blocked dependency)', async () => {
  let readySql = null;
  const conn = {
    query: async (sql) => {
      if (/sync_dispatch_events/.test(sql)) return [[]];
      if (/bow_items/.test(sql)) { readySql = sql; return [[{ n: 1 }]]; }
      if (/project_meta/.test(sql)) return [[]];
      throw new Error('unexpected query');
    },
  };
  await maybeNag(conn, helpers);
  assert.ok(readySql, 'expected a bow_items query to have run');
  assert.match(readySql, /status IN \('open','in_progress'\)/);
  assert.match(readySql, /NOT EXISTS/);
  assert.match(readySql, /di\.status IN \('open','in_progress','blocked'\)/);
});

test('maybeNag forwards hookEventName into the emitted nag\'s hookSpecificOutput (regression: this 4th param was dropped in an earlier edit, so the JSON always claimed PostToolUse even under the live SubagentStop wiring)', async () => {
  const conn = mockConn({ dispatchRows: [], readyCount: 2, metaRows: [{ meta_value: '12' }] });
  const nag = await maybeNag(conn, helpers, 'SubagentStop');
  assert.ok(nag);
  assert.equal(nag.hookSpecificOutput.hookEventName, 'SubagentStop');
});

test('maybeNag defaults hookEventName to PostToolUse when the caller does not pass one (back-compat / manual-rewire path)', async () => {
  const conn = mockConn({ dispatchRows: [], readyCount: 2, metaRows: [{ meta_value: '12' }] });
  const nag = await maybeNag(conn, helpers);
  assert.equal(nag.hookSpecificOutput.hookEventName, 'PostToolUse');
});

// ── main() payload-shape gating (spawn-based, no live DB) ──────────────────

test('main(): a non-Agent PostToolUse-shaped payload exits 0 with NO "cannot connect" stderr — gated out before ever reaching connect()', () => {
  const result = runHook({ tool_name: 'Bash', session_id: 'x' }, { METRO_DB_PORT: '1' });
  assert.equal(result.status, 0);
  assert.doesNotMatch(result.stderr, /cannot connect/);
});

test('main(): an Agent PostToolUse-shaped payload with NO tool_input exits 0 with no connect attempt', () => {
  const result = runHook({ tool_name: 'Agent', session_id: 'x' }, { METRO_DB_PORT: '1' });
  assert.equal(result.status, 0);
  assert.doesNotMatch(result.stderr, /cannot connect/);
});

test('main(): an Agent PostToolUse-shaped payload WITH tool_input passes gating and reaches connect() (dead port -> "cannot connect" stderr)', () => {
  const result = runHook(
    { tool_name: 'Agent', session_id: 'x', tool_input: { subagent_type: 'general-purpose', description: 'd' } },
    { METRO_DB_PORT: '1' }
  );
  assert.equal(result.status, 0);
  assert.match(result.stderr, /cannot connect to metro MariaDB/);
});

test('main(): a SubagentStop payload (no tool/tool_input at all) bypasses the tool-shape gate entirely and reaches connect() (AC-11 live wiring)', () => {
  const result = runHook({ hook_event_name: 'SubagentStop', session_id: 'x' }, { METRO_DB_PORT: '1' });
  assert.equal(result.status, 0);
  assert.match(result.stderr, /cannot connect to metro MariaDB/);
});

test('main(): unparseable stdin exits 0 with an "unparsable stdin" stderr note, never a thrown exception', () => {
  const result = spawnSync(process.execPath, [SCRIPT], {
    input: 'not json at all {{{',
    cwd: __dirname,
    encoding: 'utf8',
    timeout: 8000,
    env: { ...process.env, METRO_DB_PORT: '1' },
  });
  assert.equal(result.status, 0);
  assert.match(result.stderr, /unparsable stdin/);
});

test('main(): CLAUDE_DISABLE_DISPATCH_GUARD=1 exits 0 silently, even for an otherwise-gating-passing Agent+tool_input payload', () => {
  const result = runHook(
    { tool_name: 'Agent', session_id: 'x', tool_input: { subagent_type: 'general-purpose', description: 'd' } },
    { METRO_DB_PORT: '1', CLAUDE_DISABLE_DISPATCH_GUARD: '1' }
  );
  assert.equal(result.status, 0);
  assert.equal(result.stderr.trim(), '');
});
