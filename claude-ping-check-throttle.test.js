/**
 * claude-ping-check-throttle.test.js — closes the remaining ASM-724 gap flagged
 * by the tool.pingcheck.md AC-14 amendment (2026-08-27): claude-ping-check.test.js
 * covers BUG-343's wake-recovery banner (describeRenewFailure + SPAWN), but
 * AC-15..AC-19 (fast-path no-spawn, slow-path write-before-renew, window-id
 * precedence, per-window throttle isolation) were still RED — no test named
 * "fast path"/"CHECK_INTERVAL_MS"/"per-window throttle"/"write-before-renew"
 * existed anywhere in the repo.
 *
 * All coverage here is SPAWN-based against the real hook, using the SAME
 * CLAUDE_PING_SYNC_SCRIPT / CLAUDE_PING_DIR override points the hook file
 * already exposes for claude-ping-check.test.js — no tool-file changes needed,
 * no live metro DB touched, no wall-clock assertion (fast/slow path is decided
 * by a PRE-SEEDED throttle-file timestamp, never by actually sleeping).
 *
 * Covers docs/planning/acceptance/tool.pingcheck.md:
 *   AC-15 — fast path: fresh throttle file -> exit 0 AND no subprocess spawn.
 *   AC-16 — slow path: stale throttle -> stub IS invoked with
 *           env.CLAUDE_CODE_SESSION_ID set to the resolved window id.
 *   AC-17 — window-id precedence: payload session_id beats the env fallback;
 *           invalid/missing payload falls back to the env var.
 *   AC-18 — per-window throttle isolation: two distinct window ids produce two
 *           distinct throttle files, and a fresh window's fast-path decision
 *           is never influenced by another window's timestamp.
 *   AC-19 — write-before-renew: a FAILING stub still updates the throttle
 *           timestamp, so an immediate second call takes the fast path
 *           (no second spawn) even though the renewal itself failed.
 *
 * Run: node --test claude-ping-check-throttle.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const HOOK = path.join(__dirname, 'claude-ping-check.js');
const CHECK_INTERVAL_MS = 2 * 60 * 1000; // must match claude-ping-check.js's own constant

/** Write a throwaway stub claude-sync.js that records every invocation (argv +
 *  env.CLAUDE_CODE_SESSION_ID) to a sentinel log file, then behaves per `mode`. */
function writeStub(logPath, mode = 'ok') {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ping-throttle-stub-'));
  const p = path.join(dir, 'stub.js');
  const body = `
    const fs = require('fs');
    fs.appendFileSync(${JSON.stringify(logPath)}, JSON.stringify({
      argv: process.argv.slice(2),
      sessionEnv: process.env.CLAUDE_CODE_SESSION_ID || '',
    }) + '\\n');
    ${mode === 'ok'
      ? `process.exit(0);`
      : `console.error('[claude-sync] stub-forced failure'); process.exit(1);`}
  `;
  fs.writeFileSync(p, body, 'utf8');
  return p;
}

/** Run the real hook once with a chosen throttle dir + stub + payload. */
function runHook({ pingDir, stub, sessionId, payload }) {
  const input = payload === undefined
    ? JSON.stringify({ session_id: sessionId, hook_event_name: 'PostToolUse' })
    : payload;
  return spawnSync(process.execPath, [HOOK], {
    input,
    encoding: 'utf8',
    env: {
      ...process.env,
      CLAUDE_PING_DIR: pingDir,
      CLAUDE_PING_SYNC_SCRIPT: stub,
      CLAUDE_CODE_SESSION_ID: sessionId,
    },
    timeout: 5000,
  });
}

function freshTmpDir(prefix) {
  return fs.mkdtempSync(path.join(os.tmpdir(), prefix));
}

function readInvocations(logPath) {
  if (!fs.existsSync(logPath)) return [];
  return fs.readFileSync(logPath, 'utf8').trim().split('\n').filter(Boolean).map(l => JSON.parse(l));
}

// ── AC-15: fast path — no spawn ──────────────────────────────────────────────

test('AC-15: fresh throttle file (just pinged) takes the fast path — exit 0, stub NEVER invoked', () => {
  const pingDir = freshTmpDir('ping-fast-');
  const logPath = path.join(freshTmpDir('ping-fast-log-'), 'invocations.log');
  const stub = writeStub(logPath, 'ok');
  const windowId = 'win-fast-1';

  // Pre-seed the throttle file as "just pinged" (now - 1s), well inside the
  // 2-minute CHECK_INTERVAL_MS window, so the fast path must trigger.
  fs.writeFileSync(path.join(pingDir, `.last-ping-${windowId}`), String(Date.now() - 1000), 'utf8');

  const r = runHook({ pingDir, stub, sessionId: windowId });

  assert.equal(r.status, 0, `fast path must still exit 0, got ${r.status}`);
  assert.deepEqual(readInvocations(logPath), [], 'the stub must NEVER be invoked on the fast path (no spawn)');
});

// ── AC-16: slow path — stub invoked with correct session env ─────────────────

test('AC-16: stale throttle file (elapsed interval) takes the slow path — stub IS invoked with the resolved window id', () => {
  const pingDir = freshTmpDir('ping-slow-');
  const logPath = path.join(freshTmpDir('ping-slow-log-'), 'invocations.log');
  const stub = writeStub(logPath, 'ok');
  const windowId = 'win-slow-1';

  // Pre-seed the throttle file as stale: well beyond CHECK_INTERVAL_MS ago.
  fs.writeFileSync(
    path.join(pingDir, `.last-ping-${windowId}`),
    String(Date.now() - (CHECK_INTERVAL_MS + 60000)),
    'utf8'
  );

  const r = runHook({ pingDir, stub, sessionId: windowId });

  assert.equal(r.status, 0);
  const invocations = readInvocations(logPath);
  assert.equal(invocations.length, 1, 'the slow path must invoke the stub exactly once');
  assert.equal(invocations[0].argv.join(' '), 'renew --auto', 'must call renew --auto');
  assert.equal(invocations[0].sessionEnv, windowId, 'env.CLAUDE_CODE_SESSION_ID must carry the resolved window id');
});

test('AC-16 (negative control): a throttle file with NO prior entry (first run) also takes the slow path', () => {
  const pingDir = freshTmpDir('ping-firstrun-');
  const logPath = path.join(freshTmpDir('ping-firstrun-log-'), 'invocations.log');
  const stub = writeStub(logPath, 'ok');
  const windowId = 'win-firstrun-1';
  // No throttle file written at all — lastPingMs defaults to 0.

  const r = runHook({ pingDir, stub, sessionId: windowId });

  assert.equal(r.status, 0);
  assert.equal(readInvocations(logPath).length, 1, 'a never-pinged window must renew on its first tool use');
});

// ── AC-17: window-id precedence — payload beats env fallback ────────────────

test('AC-17: a valid stdin payload session_id is used, even when it differs from the env fallback', () => {
  const pingDir = freshTmpDir('ping-precedence-');
  const logPath = path.join(freshTmpDir('ping-precedence-log-'), 'invocations.log');
  const stub = writeStub(logPath, 'ok');

  // Stale throttle keyed to the PAYLOAD id only — if the hook wrongly used the
  // env fallback ('win-env-B') it would find no throttle file for THAT id and
  // still take the slow path, masking the bug; assert the *throttle file that
  // gets written* is keyed to the payload id instead, which only the correct
  // precedence produces.
  const r = spawnSync(process.execPath, [HOOK], {
    input: JSON.stringify({ session_id: 'win-payload-A', hook_event_name: 'PostToolUse' }),
    encoding: 'utf8',
    env: {
      ...process.env,
      CLAUDE_PING_DIR: pingDir,
      CLAUDE_PING_SYNC_SCRIPT: stub,
      CLAUDE_CODE_SESSION_ID: 'win-env-B', // deliberately different — must lose
    },
    timeout: 5000,
  });

  assert.equal(r.status, 0);
  assert.ok(fs.existsSync(path.join(pingDir, '.last-ping-win-payload-A')),
    'the throttle file must be keyed to the PAYLOAD session id, not the env fallback');
  assert.ok(!fs.existsSync(path.join(pingDir, '.last-ping-win-env-B')),
    'the env-fallback id must NOT be used when a valid payload id is present');
  const invocations = readInvocations(logPath);
  assert.equal(invocations[0].sessionEnv, 'win-payload-A',
    'renew --auto must be invoked for the payload-resolved window, not the env one');
});

test('AC-17: invalid/unparseable stdin payload falls back to the env session id', () => {
  const pingDir = freshTmpDir('ping-fallback-');
  const logPath = path.join(freshTmpDir('ping-fallback-log-'), 'invocations.log');
  const stub = writeStub(logPath, 'ok');

  const r = runHook({ pingDir, stub, sessionId: 'win-envonly-1', payload: 'not valid json {{{' });

  assert.equal(r.status, 0);
  assert.ok(fs.existsSync(path.join(pingDir, '.last-ping-win-envonly-1')),
    'with unparseable stdin, the env session id must be used as the throttle key');
  assert.equal(readInvocations(logPath)[0].sessionEnv, 'win-envonly-1');
});

test('AC-17: a payload with no session_id field falls back to the env session id', () => {
  const pingDir = freshTmpDir('ping-fallback2-');
  const logPath = path.join(freshTmpDir('ping-fallback2-log-'), 'invocations.log');
  const stub = writeStub(logPath, 'ok');

  const r = runHook({
    pingDir, stub, sessionId: 'win-envonly-2',
    payload: JSON.stringify({ hook_event_name: 'PostToolUse' }), // no session_id key
  });

  assert.equal(r.status, 0);
  assert.ok(fs.existsSync(path.join(pingDir, '.last-ping-win-envonly-2')));
});

// ── AC-18: per-window throttle isolation ─────────────────────────────────────

test('AC-18: two distinct window ids produce two distinct throttle files, each independently timed', () => {
  const pingDir = freshTmpDir('ping-isolation-');
  const logPath = path.join(freshTmpDir('ping-isolation-log-'), 'invocations.log');
  const stub = writeStub(logPath, 'ok');

  // Window A just pinged (fast path expected); window B has never pinged
  // (slow path expected). If the two windows shared one throttle file, A's
  // recent timestamp would incorrectly suppress B's renewal too.
  fs.writeFileSync(path.join(pingDir, '.last-ping-win-A'), String(Date.now() - 1000), 'utf8');

  const rA = runHook({ pingDir, stub, sessionId: 'win-A' });
  const rB = runHook({ pingDir, stub, sessionId: 'win-B' });

  assert.equal(rA.status, 0);
  assert.equal(rB.status, 0);
  const invocations = readInvocations(logPath);
  assert.equal(invocations.length, 1, 'only window B (never pinged) should have triggered a renew');
  assert.equal(invocations[0].sessionEnv, 'win-B');
  assert.ok(fs.existsSync(path.join(pingDir, '.last-ping-win-A')), "window A's own throttle file must still exist, untouched by B's run");
  assert.ok(fs.existsSync(path.join(pingDir, '.last-ping-win-B')), 'window B must get its OWN throttle file');
});

// ── AC-19: write-before-renew — a failing stub still throttles ──────────────

test('AC-19: a FAILING renew still writes the throttle timestamp, so an immediate retry takes the fast path', () => {
  const pingDir = freshTmpDir('ping-writefirst-');
  const logPath = path.join(freshTmpDir('ping-writefirst-log-'), 'invocations.log');
  const stub = writeStub(logPath, 'fail'); // stub exits 1 every time
  const windowId = 'win-writefirst-1';
  // No throttle file — first call is slow-path and the stub will fail.

  const r1 = runHook({ pingDir, stub, sessionId: windowId });
  assert.equal(r1.status, 0, 'hook stays fail-open even though the underlying renew failed');
  assert.equal(readInvocations(logPath).length, 1, 'first call invokes the (failing) stub once');
  assert.ok(fs.existsSync(path.join(pingDir, `.last-ping-${windowId}`)),
    'the throttle timestamp must be written even though renew failed (write-before-renew, AC-5/AC-19)');

  // Second, immediate call: must take the FAST path (no second spawn) despite
  // the first renewal having failed — this is the whole point of writing the
  // timestamp before attempting the renewal, not after a success.
  const r2 = runHook({ pingDir, stub, sessionId: windowId });
  assert.equal(r2.status, 0);
  assert.equal(readInvocations(logPath).length, 1,
    'a second immediate call must NOT re-invoke the stub — write-before-renew must throttle even a failure');
});
