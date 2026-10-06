/**
 * claude-ping-check.test.js — regression tests for BUG-343.
 *
 * BUG-343 (silent-failure; GR#1 aggressive error trapping + GR#17 silent-failure
 * detection): claude-sync `renew --auto` wake recovery fails LOUD (console.error +
 * process.exit(1)) when the window's previous slot is retired/parked or held by
 * another window after a laptop sleep/resume. execSync throws on that nonzero exit,
 * and claude-ping-check USED TO swallow the throw in an empty `catch {}` — so the
 * session silently lost its permit with no notice. The fix SURFACES the failure on
 * process.stdout (the channel this PostToolUse hook relays to the session, the same
 * channel the success path uses) and on stderr, while staying fail-open (exit 0).
 *
 * Covers:
 *   - Pure predicate (describeRenewFailure): a nonzero-exit refusal produces the
 *     "WAKE RECOVERY FAILED — permit LOST" banner carrying claude-sync's own stderr
 *     and the session id; a no-status (spawn/timeout) failure produces the
 *     "could not complete" variant instead. Never returns empty.
 *   - SPAWN: the real hook driven end-to-end via stdin against a STUB claude-sync
 *     that exits 1 with a wake-recovery message. Asserts the banner reaches
 *     process.stdout (what the session SEES — the BUG-343 requirement), the hook
 *     stays fail-open (exit 0), and the message is present. This is the RED/GREEN
 *     anchor: under the old empty-catch code the stub's message never reaches
 *     stdout, so this assertion fails RED; with the fix it passes GREEN.
 *   - SPAWN success and throttle paths still behave (surfacing on stdout / silent).
 *
 * Run: node --test claude-ping-check.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const { describeRenewFailure } = require('./claude-ping-check.js');

const HOOK = path.join(__dirname, 'claude-ping-check.js');

// ── Pure predicate: describeRenewFailure ─────────────────────────────────────

test('describeRenewFailure: a nonzero-exit refusal yields the permit-LOST banner with detail + session', () => {
  const err = {
    status: 1,
    stderr: '[claude-sync] Your previous slot "Bill" is held; re-checkin explicitly:',
    stdout: '',
    message: 'Command failed',
  };
  const out = describeRenewFailure(err, 'win-abc');
  assert.match(out, /WAKE RECOVERY FAILED/);
  assert.match(out, /permit LOST/);
  assert.match(out, /win-abc/, 'must name the session that lost its permit');
  assert.match(out, /previous slot "Bill" is held/, "must carry claude-sync's own explanation");
  assert.match(out, /checkin --name/, 'must tell the user how to recover');
});

test('describeRenewFailure: a retired/parked refusal is surfaced too', () => {
  const err = {
    status: 1,
    stderr: '[claude-sync] Your previous slot "Ben" is PARKED and cannot be re-acquired.',
    message: 'Command failed',
  };
  const out = describeRenewFailure(err, '');
  assert.match(out, /WAKE RECOVERY FAILED/);
  assert.match(out, /PARKED and cannot be re-acquired/);
});

test('describeRenewFailure: a no-status failure (timeout/spawn) uses the "could not complete" variant', () => {
  const err = { message: 'spawnSync node ETIMEDOUT', signal: 'SIGTERM' };
  const out = describeRenewFailure(err, 'win-xyz');
  assert.match(out, /could not complete/);
  assert.match(out, /win-xyz/);
  assert.match(out, /ETIMEDOUT/);
  assert.doesNotMatch(out, /WAKE RECOVERY FAILED/, 'no verdict was reached — do not claim a lost permit');
});

test('describeRenewFailure: never returns an empty string, even for a bare error', () => {
  assert.notEqual(describeRenewFailure({}, '').trim(), '');
  assert.notEqual(describeRenewFailure(null, '').trim(), '');
});

// ── SPAWN: real hook, end-to-end via stdin ───────────────────────────────────

/** Run the real hook with a fresh throttle dir and a chosen stub claude-sync. */
function runHook(sessionId, stubPath) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ping-check-test-'));
  try {
    return spawnSync(process.execPath, [HOOK], {
      input: JSON.stringify({ session_id: sessionId, hook_event_name: 'PostToolUse' }),
      encoding: 'utf8',
      env: {
        ...process.env,
        CLAUDE_PING_DIR: dir,             // fresh dir => no throttle, always runs renew
        CLAUDE_PING_SYNC_SCRIPT: stubPath, // stub claude-sync
        CLAUDE_CODE_SESSION_ID: sessionId,
      },
    });
  } finally {
    try { fs.rmSync(dir, { recursive: true, force: true }); } catch { /* best effort */ }
  }
}

/** Write a throwaway stub script and return its path. */
function writeStub(body) {
  const p = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'ping-check-stub-')), 'stub.js');
  fs.writeFileSync(p, body, 'utf8');
  return p;
}

test('SPAWN: wake-recovery failure (exit 1) is SURFACED on stdout — the session SEES it (BUG-343 RED/GREEN)', () => {
  // Stub stands in for claude-sync's wake-recovery refusal: loud to stderr, exit 1.
  const stub = writeStub(
    `console.error('[claude-sync] Your previous slot "Bill" is held; re-checkin explicitly:');\n` +
    `console.error('[claude-sync]   node claude-sync.js checkin --name Bill');\n` +
    `process.exit(1);\n`
  );
  const r = runHook('win-343', stub);

  // Fail-open: the hook must never block the tool call.
  assert.equal(r.status, 0, `hook must stay fail-open (exit 0), got ${r.status}`);

  // The BUG-343 core: the failure must reach the SESSION-VISIBLE channel (stdout).
  // Under the old empty-catch code stdout was empty here — this assertion is the
  // RED that the fix turns GREEN.
  assert.match(r.stdout, /WAKE RECOVERY FAILED/,
    `the permit-lost banner must reach stdout (what the session sees); got stdout=${JSON.stringify(r.stdout)}`);
  assert.match(r.stdout, /permit LOST/);
  assert.match(r.stdout, /win-343/, 'must name the affected session');
  assert.match(r.stdout, /previous slot "Bill" is held/, "must carry claude-sync's own explanation");

  // Also mirrored to stderr.
  assert.match(r.stderr, /WAKE RECOVERY FAILED/);
});

test('SPAWN: a successful renew notice is surfaced on stdout unchanged', () => {
  const stub = writeStub(
    `process.stdout.write('[claude-sync] Wake recovery: re-acquired Bill (permit expired while idle).\\n');\n` +
    `process.exit(0);\n`
  );
  const r = runHook('win-ok', stub);
  assert.equal(r.status, 0);
  assert.match(r.stdout, /Wake recovery: re-acquired Bill/);
});

test('SPAWN: a silent successful renew (no output, exit 0) produces no stdout noise', () => {
  const stub = writeStub(`process.exit(0);\n`);
  const r = runHook('win-quiet', stub);
  assert.equal(r.status, 0);
  assert.equal(r.stdout.trim(), '', 'a silent heartbeat renew must stay silent');
});
