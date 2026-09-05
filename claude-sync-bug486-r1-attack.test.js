/**
 * claude-sync-bug486-r1-attack.test.js — INDEPENDENT DESTRUCTIVE ROUND 1
 * against the BUG-486 / BUG-253 estate (auto-renew fix in
 * claude-ping-check.js + the GR#17 loud diagnostic in claude-sync.js's
 * cmdRenew).
 *
 * Attacker: opus-round-sync486 (NOT the author — GR#23 independence).
 *
 * SAFETY: every query in this file runs against a per-PID scratch database
 * (`metro_test_bug486attack_<pid>`), created and dropped here. The live
 * `metro` DB — this machine's real Bill/Bev/Bro coordination — is never
 * touched, and every synthetic window id is an obviously fake, grep-able
 * string that can never collide with a real crypto.randomUUID() window id.
 * Session-key files are DB-tagged (`-<TEST_DB>`) exactly as the parent suite
 * does, so cleanup can never delete a live window's untagged key file.
 *
 * The attack surface, per the round brief:
 *   X-1  the BUG-354 boundary: can a secret-less process that knows only a
 *        victim's window id renew/re-mint that permit through ANY path now
 *        that a shipped executable (the hook) reads key files by window id?
 *   X-2  hostile session-key file contents -> shell interpolation into
 *        execSync, and loud-vs-silent degradation (GR#17)
 *   X-3  the GR#17 diagnostic: does it actually fire, is exit-0-no-op now
 *        observably distinct from exit-0-success?
 *   X-4  eviction-vs-autorenew: can the hook resurrect a permit a human
 *        deliberately released (checkout / checkout --force)?
 *   X-5  TTL math: near-expiry, past-expiry-within-RESERVE, past-RESERVE
 *
 * Run: node --test claude-sync-bug486-r1-attack.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');
const mysql = require('mysql2/promise');

const ROOT = __dirname;
const TEST_DB = `metro_test_bug486attack_${process.pid}`;

const DB_HOST = process.env.METRO_DB_HOST || '127.0.0.1';
const DB_PORT = Number(process.env.METRO_DB_PORT || 3306);
const DB_USER = process.env.METRO_DB_USER || 'root';
const DB_PASSWORD = process.env.METRO_DB_PASSWORD || '';

const IDENTITY_FILE = path.join(ROOT, '.claude', '.identity');
const KEY_DIR = path.join(os.homedir(), '.claude', 'session-keys');

let db;
let identitySnapshot = null;
let identitySnapshotExisted = false;

/** Scratch-DB-scoped claude-sync subprocess (mirrors the parent suite's run()). */
function run(args, sessionId, extraEnv = {}) {
  return spawnSync(process.execPath, ['claude-sync.js', ...args], {
    cwd: ROOT,
    encoding: 'utf8',
    env: {
      ...process.env,
      METRO_DB_HOST: DB_HOST,
      METRO_DB_PORT: String(DB_PORT),
      METRO_DB_USER: DB_USER,
      METRO_DB_PASSWORD: DB_PASSWORD,
      METRO_DB_NAME: TEST_DB,
      CLAUDE_CODE_SESSION_ID: sessionId || '',
      CLAUDE_SESSION_ID: '',
      CLAUDE_IDENTITY: '',
      ...extraEnv,
    },
  });
}

function sessionKeyFile(sessionId) {
  return path.join(KEY_DIR, `.session-key-${sessionId}-${TEST_DB}`);
}

function readSessionSecret(sessionId) {
  try { return fs.readFileSync(sessionKeyFile(sessionId), 'utf8').trim(); } catch { return ''; }
}

function checkin(name, sessionId) {
  const args = ['checkin', '--name', name];
  const secret = readSessionSecret(sessionId);
  if (secret) args.push('--session', secret);
  return run(args, sessionId);
}

/** Run the REAL PostToolUse hook exactly as production does: the window id
 *  arrives on stdin, a throwaway CLAUDE_PING_DIR defeats the 2-minute
 *  throttle, and CLAUDE_PING_SYNC_SCRIPT is left unset so the hook uses the
 *  real (fixed) claude-sync.js beside it. */
function runHook(stdinSessionId, extraEnv = {}) {
  const throttleDir = fs.mkdtempSync(path.join(os.tmpdir(), 'b486atk-'));
  try {
    return spawnSync(process.execPath, [path.join(ROOT, 'claude-ping-check.js')], {
      cwd: ROOT,
      input: JSON.stringify({ session_id: stdinSessionId, hook_event_name: 'PostToolUse' }),
      encoding: 'utf8',
      env: {
        ...process.env,
        METRO_DB_HOST: DB_HOST, METRO_DB_PORT: String(DB_PORT),
        METRO_DB_USER: DB_USER, METRO_DB_PASSWORD: DB_PASSWORD, METRO_DB_NAME: TEST_DB,
        CLAUDE_IDENTITY: '',
        CLAUDE_PING_DIR: throttleDir,
        CLAUDE_CODE_SESSION_ID: stdinSessionId,
        ...extraEnv,
      },
    });
  } finally {
    try { fs.rmSync(throttleDir, { recursive: true, force: true }); } catch { /* best effort */ }
  }
}

async function permit(name) {
  const [[row]] = await db.query('SELECT * FROM sync_permits WHERE name=?', [name]);
  return row;
}

test.before(async () => {
  try {
    identitySnapshot = fs.readFileSync(IDENTITY_FILE, 'utf8');
    identitySnapshotExisted = true;
  } catch { identitySnapshotExisted = false; }

  const boot = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD });
  await boot.query(`CREATE DATABASE IF NOT EXISTS \`${TEST_DB}\``);
  await boot.end();

  db = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD, database: TEST_DB });
  await require('./claude-sync.js').ensureSchema(db);
});

test.after(async () => {
  if (db) {
    await db.query(`DROP DATABASE IF EXISTS \`${TEST_DB}\``);
    await db.end();
  }
  try {
    if (identitySnapshotExisted) fs.writeFileSync(IDENTITY_FILE, identitySnapshot, 'utf8');
  } catch { /* best-effort */ }
  for (const dir of [KEY_DIR, path.join(ROOT, '.claude')]) {
    try {
      if (!fs.existsSync(dir)) continue;
      for (const f of fs.readdirSync(dir)) {
        if (f.startsWith('.session-key-') && f.endsWith(`-${TEST_DB}`)) {
          try { fs.unlinkSync(path.join(dir, f)); } catch { /* best-effort */ }
        }
      }
    } catch { /* best-effort */ }
  }
});

test.beforeEach(async () => {
  await db.query('DELETE FROM sync_messages');
  await db.query('UPDATE sync_read_cursor SET last_read_id=0');
  await db.query(`UPDATE sync_permits SET session_id=NULL, window_id=NULL, acquired_ms=NULL,
    expires_ms=NULL, heartbeat_ms=NULL, boot_id=NULL, released=1`);
  await db.query('DELETE FROM sync_file_claims');
  await db.query('DELETE FROM sync_activity');
  await db.query('DELETE FROM sync_window_map');
  await db.query('DELETE FROM sync_loop_config');
});

// ── X-2: hostile session-key file contents ───────────────────────────────────
// readOwnSessionSecret's output is interpolated into a SHELL COMMAND STRING
// (`node "<script>" renew --auto --session "<secret>"`) passed to execSync.
// The only thing standing between a tampered key file and arbitrary command
// execution under the operator's account is the UUID-shape regex. Prove it
// holds for every hostile payload, and prove degradation is LOUD (GR#17).

// The canary command must actually RUN on the shell execSync uses here
// (cmd.exe on Windows, /bin/sh on POSIX) — an attack payload that is merely
// syntactically hostile but not executable on the host proves nothing. A
// scratch probe confirmed all three chaining forms below DO create the canary
// once the UUID-shape guard is removed, so these payloads genuinely
// mutation-catch that guard rather than passing vacuously.
const CANARY = 'B486PWNED';
const HOSTING_SHELL_CHAINS = [
  ['cmd.exe double-quote break-out + & chain', `a" & echo PWNED > ${CANARY} & rem "`],
  ['cmd.exe & chain closing the quote', `a" & echo PWNED > ${CANARY} & echo "`],
  ['POSIX double-quote break-out + ; chain', `a"; echo PWNED > ${CANARY}; echo "`],
  ['POSIX && chain', `a" && echo PWNED > ${CANARY} && echo "`],
  ['POSIX pipe into a writer', `a" | tee ${CANARY} > /dev/null; echo "`],
];

const HOSTILE_KEY_CONTENTS = [
  ...HOSTING_SHELL_CHAINS,
  ['backtick command substitution', `\`echo PWNED > ${CANARY}\``],
  ['dollar-paren command substitution', `$(echo PWNED > ${CANARY})`],
  ['cmd.exe caret escape + chain', `aaaa^&echo PWNED > ${CANARY}`],
  ['cmd.exe percent expansion', '%CD%%CD%%CD%%CD%%CD%%CD%%CD%%CD%%CD%%CD%%CD%%CD%'],
  ['newline-separated second command', `aaaa\necho PWNED > ${CANARY}`],
  ['CR-injected second command', `aaaa\recho PWNED > ${CANARY}`],
  ['empty file', ''],
  ['whitespace only', '   \t  \n '],
  ['BOM + valid uuid', '﻿3f2504e0-4f89-11d3-9a0c-0305e82c3301'],
  ['UTF-16LE encoded uuid (mojibake)', Buffer.from('3f2504e0-4f89-11d3-9a0c-0305e82c3301', 'utf16le').toString('binary')],
  ['NUL byte injected mid-uuid', '3f2504e0-4f89-11d3- 9a0c-0305e82c33'],
  ['overlong (10KB of hex)', 'a'.repeat(10240)],
  ['37 hex chars (one over)', 'a'.repeat(37)],
  ['35 hex chars (one under)', 'a'.repeat(35)],
  ['uppercase non-hex letters', 'ZZZZZZZZ-ZZZZ-ZZZZ-ZZZZ-ZZZZZZZZZZZZ'],
  ['leading dash (argv confusion attempt)', '-name Bev --session xxxxxxxxxxxxxxxx'],
];

for (const [label, payload] of HOSTILE_KEY_CONTENTS) {
  test(`X-2 (${label}): a tampered session-key file causes NO shell execution and NO silent success`, async () => {
    const sid = `b486atk-hostile-${label.replace(/[^a-z0-9]+/gi, '-').toLowerCase()}`;
    fs.mkdirSync(KEY_DIR, { recursive: true });
    fs.writeFileSync(sessionKeyFile(sid), payload, 'binary');

    // The hook runs with cwd: __dirname (= ROOT), so a relative redirect in an
    // injected command lands here.
    const canary = path.join(ROOT, CANARY);
    try { fs.unlinkSync(canary); } catch { /* absent */ }

    const res = runHook(sid);

    assert.equal(res.status, 0, 'the hook must stay fail-open for the tool call');
    assert.equal(fs.existsSync(canary), false,
      `SHELL INJECTION: the tampered key file executed a command (canary ${canary} created)`);

    // GR#17: this window has no permit and a broken key file. The outcome must
    // be observable, never total silence.
    const combined = `${res.stdout}${res.stderr}`;
    assert.match(combined, /renew --auto|WAKE RECOVERY|claude-sync/,
      `GR#17: a hook run that resolves nothing must say so. stdout=${JSON.stringify(res.stdout)} stderr=${JSON.stringify(res.stderr)}`);

    try { fs.unlinkSync(canary); } catch { /* nothing to clean */ }
  });
}

test('X-2b: readOwnSessionSecret never throws and never returns non-file data', () => {
  const { readOwnSessionSecret } = require('./claude-ping-check.js');
  assert.equal(readOwnSessionSecret(''), '', 'empty window id must short-circuit to empty');
  assert.equal(readOwnSessionSecret(null), '', 'null window id must short-circuit to empty');
  assert.equal(readOwnSessionSecret(undefined), '', 'undefined window id must short-circuit to empty');
  // A window id containing path traversal must not be able to read an
  // arbitrary file as "the secret" (it is joined into a filename, not a path).
  const traversal = readOwnSessionSecret('../../../../etc/passwd');
  assert.equal(typeof traversal, 'string');
  assert.ok(!/root:/.test(traversal), 'a traversal window id must not surface arbitrary file contents');
});

// ── X-3: the GR#17 diagnostic is real, and exit-0-no-op is distinguishable ───

test('X-3a: `renew --auto` with NO secret prints the loud no-secret diagnostic (exit 0, non-silent)', () => {
  const res = run(['renew', '--auto'], 'b486atk-x3a-window');
  assert.equal(res.status, 0, 'must fail open');
  assert.notEqual(res.stdout.trim(), '', 'GR#17: must not be silent');
  assert.match(res.stdout, /no --session secret presented/,
    'the no-secret branch must name its own cause so it is greppable from hook output');
});

test('X-3b: `renew --auto` with a WELL-FORMED but unknown secret prints the loud stale-secret diagnostic', () => {
  const res = run(['renew', '--auto', '--session', '3f2504e0-4f89-11d3-9a0c-0305e82c3301'], 'b486atk-x3b-window');
  assert.equal(res.status, 0);
  assert.match(res.stdout, /matched no live\/stale permit/,
    'a presented-but-dead secret must be reported differently from no secret at all');
});

test('X-3c: SUCCESS is observably distinct from the no-op — a real --auto renewal is SILENT', async () => {
  const sid = 'b486atk-x3c-window';
  assert.equal(checkin('Bro', sid).status, 0);
  const secret = readSessionSecret(sid);
  assert.ok(secret, 'precondition: checkin wrote a key file');

  // (i) plenty of TTL left -> heartbeat-only success path
  const plenty = run(['renew', '--auto', '--session', secret], sid);
  assert.equal(plenty.status, 0);
  assert.equal(plenty.stdout.trim(), '', 'a heartbeat-only success must print nothing');

  // (ii) near expiry -> real renewal success path
  const nearExpiry = Date.now() + 60 * 1000;
  await db.query('UPDATE sync_permits SET expires_ms=? WHERE name="Bro"', [nearExpiry]);
  const renewed = run(['renew', '--auto', '--session', secret], sid);
  assert.equal(renewed.status, 0);
  assert.equal(renewed.stdout.trim(), '', 'a genuine --auto renewal must also print nothing');
  assert.ok(Number((await permit('Bro')).expires_ms) > nearExpiry + 60000, 'and must actually have renewed');

  // The invariant the old exit-0-no-op violated: for `renew --auto`,
  // stdout emptiness is now a FAITHFUL success signal.
  const failed = run(['renew', '--auto'], 'b486atk-x3c-other');
  assert.notEqual(failed.stdout.trim(), '',
    'and a resolve-nothing call must break that silence — success and no-op can no longer look identical');
});

// ── X-5: TTL math across every band ──────────────────────────────────────────

test('X-5a: the hook renews an ACTIVE near-expiry permit without changing the secret', async () => {
  const sid = 'b486atk-x5a-window';
  assert.equal(checkin('Bro', sid).status, 0);
  const before = await permit('Bro');
  const nearExpiry = Date.now() + 60 * 1000;
  await db.query('UPDATE sync_permits SET expires_ms=? WHERE name="Bro"', [nearExpiry]);

  const res = runHook(sid);
  assert.equal(res.status, 0);
  const after = await permit('Bro');
  assert.ok(Number(after.expires_ms) > nearExpiry + 60000, 'TTL must be pushed out');
  assert.equal(after.session_id, before.session_id,
    'a plain renewal must NOT re-mint the secret (that would desync every other holder of it)');
  assert.equal(readSessionSecret(sid), before.session_id, 'and the key file must still hold the same secret');
});

test('X-5b: the hook wake-recovers a RESERVED (expired-within-reserve) permit and RE-MINTS the secret', async () => {
  const sid = 'b486atk-x5b-window';
  assert.equal(checkin('Bro', sid).status, 0);
  const before = await permit('Bro');
  await db.query('UPDATE sync_permits SET expires_ms=?, heartbeat_ms=? WHERE name="Bro"',
    [Date.now() - 1000, Date.now() - 1000]);

  const res = runHook(sid);
  assert.equal(res.status, 0);
  const after = await permit('Bro');
  assert.equal(after.released, 0, 'the slot must be live again');
  assert.notEqual(after.session_id, before.session_id, 'wake recovery re-acquires, minting a fresh secret');
  assert.equal(readSessionSecret(sid), after.session_id,
    'and the key file MUST be updated in the same breath, or the window self-locks on its next renew');
  assert.match(res.stdout, /Wake recovery/, 'GR#17: an identity-affecting re-acquire must be announced');
});

test('X-5c: a permit lapsed PAST reserve is silently re-minted by the hook via the window map', async () => {
  const sid = 'b486atk-x5c-window';
  assert.equal(checkin('Bro', sid).status, 0);
  const before = await permit('Bro');
  // Far past RESERVE_MS: slotState -> FREE. Only the hadName/window-map path
  // can act, and it is gated on flags.session — which the hook now presents.
  await db.query('UPDATE sync_permits SET expires_ms=?, heartbeat_ms=? WHERE name="Bro"',
    [Date.now() - 24 * 60 * 60 * 1000, Date.now() - 24 * 60 * 60 * 1000]);

  const res = runHook(sid);
  assert.equal(res.status, 0);
  const after = await permit('Bro');
  assert.equal(after.released, 0, 'documented wake-recovery contract: the same name is reclaimed');
  assert.notEqual(after.session_id, before.session_id);
  assert.match(res.stdout, /Wake recovery/, 'and it is announced, not silent');
});

// ── X-4: eviction-vs-autorenew — the resurrection class ──────────────────────
// cmdCheckout sets released=1 but LEAVES session_id in place, and does NOT
// delete the window's session-key file. cmdRenew's hadName branch matches a
// RELEASED row by presented secret. Pre-fix the hook presented no secret, so
// that branch was unreachable from the hook. It is reachable now.

test('X-4a: RESURRECTION — the hook re-acquires a slot the window deliberately CHECKED OUT', async () => {
  const sid = 'b486atk-x4a-window';
  assert.equal(checkin('Bro', sid).status, 0);
  const secret = readSessionSecret(sid);

  const out = run(['checkout', '--session', secret], sid);
  assert.equal(out.status, 0, `checkout should succeed: ${out.stderr}`);
  const released = await permit('Bro');
  assert.equal(released.released, 1, 'precondition: the slot is released (FREE)');
  assert.equal(released.session_id, secret,
    'NOTE: checkout leaves the old secret on the row — this is what makes the hadName branch matchable');
  assert.equal(readSessionSecret(sid), secret,
    'NOTE: checkout also leaves the window session-key file in place');

  const res = runHook(sid);
  assert.equal(res.status, 0);
  const after = await permit('Bro');

  // This assertion documents the CURRENT behaviour. It is the finding.
  assert.equal(after.released, 0,
    'FINDING X-4a: the ping hook re-acquires a deliberately checked-out slot on the next tool use');
  assert.notEqual(after.session_id, secret, 'with a freshly minted secret');
  assert.match(res.stdout, /Wake recovery/,
    'the resurrection is at least ANNOUNCED on the hook channel (not silent) — this is the mitigating factor');
});

test('X-4b: RESURRECTION — the hook undoes a human operator\'s `checkout --force` eviction', async () => {
  const sid = 'b486atk-x4b-window';
  assert.equal(checkin('Bro', sid).status, 0);
  const secret = readSessionSecret(sid);

  // The sanctioned human-only force-evict (BUG-354 r8 CONTROL path): an
  // operator releases a stuck window's slot, presenting the target's secret.
  const evict = run(['checkout', '--force', 'Bro', '--session', secret], 'b486atk-x4b-operator');
  assert.equal(evict.status, 0, `operator force-evict should succeed: ${evict.stderr}`);
  assert.equal((await permit('Bro')).released, 1, 'precondition: the operator freed the slot');

  // The evicted window is still alive and still running tool calls.
  const res = runHook(sid);
  assert.equal(res.status, 0);
  const after = await permit('Bro');
  assert.equal(after.released, 0,
    'FINDING X-4b: the evicted window\'s own ping hook takes the slot straight back, ' +
    'defeating a human-authorised eviction within one hook interval');
});

test('X-4c: BLAST RADIUS — the resurrection cannot steal a slot a DIFFERENT window has since taken', async () => {
  const victimSid = 'b486atk-x4c-first';
  assert.equal(checkin('Bro', victimSid).status, 0);
  const oldSecret = readSessionSecret(victimSid);
  assert.equal(run(['checkout', '--session', oldSecret], victimSid).status, 0);

  // A different window legitimately takes the freed slot.
  const newSid = 'b486atk-x4c-second';
  assert.equal(checkin('Bro', newSid).status, 0);
  const held = await permit('Bro');
  assert.equal(held.window_id, newSid, 'precondition: the new window holds Bro');

  // The old window's hook fires. It must NOT be able to take the slot back.
  const res = runHook(victimSid);
  assert.equal(res.status, 0, 'hook stays fail-open');
  const after = await permit('Bro');
  assert.equal(after.session_id, held.session_id,
    'the new holder\'s permit must be untouched — resurrection is bounded to a still-FREE slot');
  assert.equal(after.window_id, newSid, 'and the window id must not flip back');
  assert.match(`${res.stdout}${res.stderr}`, /is held|WAKE RECOVERY FAILED/,
    'GR#17: and the losing window must be told loudly that its slot is gone');
});

// ── X-1: the BUG-354 boundary under the new hook ─────────────────────────────

test('X-1a: cmdRenew\'s server-side trust boundary is UNCHANGED — a secret-less env-spoofer still gets nothing', async () => {
  const victimSid = 'b486atk-x1a-victim';
  assert.equal(checkin('Bro', victimSid).status, 0);
  const before = await permit('Bro');
  await db.query('UPDATE sync_permits SET expires_ms=? WHERE name="Bro"', [Date.now() + 60 * 1000]);
  const nearExpiry = Number((await permit('Bro')).expires_ms);

  // Attacker calls claude-sync DIRECTLY with the victim's window id in env and
  // no --session — exactly the BUG-354 r4 W-3 / r5 F4 model.
  const attack = run(['renew', '--auto'], victimSid);
  assert.equal(attack.status, 0);
  const after = await permit('Bro');
  assert.equal(after.session_id, before.session_id, 'no re-mint');
  assert.equal(Number(after.expires_ms), nearExpiry, 'and no renewal — the boundary holds server-side');
});

test('X-1b: RESIDUAL — but claude-ping-check.js is now a shipped key-file ORACLE: an attacker-run hook renews the victim\'s permit from the window id alone', async () => {
  const victimSid = 'b486atk-x1b-victim';
  assert.equal(checkin('Bro', victimSid).status, 0);
  const before = await permit('Bro');
  const nearExpiry = Date.now() + 60 * 1000;
  await db.query('UPDATE sync_permits SET expires_ms=? WHERE name="Bro"', [nearExpiry]);

  // The attacker holds NO secret and NO permit. It knows only the victim's
  // window id (leaked in logs/transcripts/error text — the documented BUG-360
  // premise) and runs the hook, feeding that id on stdin. The hook reads the
  // VICTIM's key file for it and presents the VICTIM's secret.
  const res = runHook(victimSid);
  assert.equal(res.status, 0);
  const after = await permit('Bro');

  assert.ok(Number(after.expires_ms) > nearExpiry + 60000,
    'FINDING X-1b: the hook renewed a permit for a caller that presented no credential of its own');
  assert.equal(after.session_id, before.session_id, '(a renewal only — no secret is re-minted here)');

  // Scoping the severity: the capability is NOT new. Any same-user process
  // could already read that key file directly and pass --session itself.
  // The hook is a convenience wrapper over a pre-existing (BUG-360-tracked)
  // same-user file-read capability, not a new privilege.
  assert.equal(fs.readFileSync(sessionKeyFile(victimSid), 'utf8').trim(), before.session_id,
    'PROOF the capability pre-exists the fix: the secret is plainly readable from the key file by any same-user process');
});

test('X-1c: RESIDUAL ESCALATION BOUND — the attacker-run hook CAN wake-recover and re-mint, and prints the new secret to the attacker', async () => {
  const victimSid = 'b486atk-x1c-victim';
  assert.equal(checkin('Bro', victimSid).status, 0);
  const before = await permit('Bro');
  await db.query('UPDATE sync_permits SET expires_ms=?, heartbeat_ms=? WHERE name="Bro"',
    [Date.now() - 1000, Date.now() - 1000]);

  const res = runHook(victimSid);
  assert.equal(res.status, 0);
  const after = await permit('Bro');
  assert.notEqual(after.session_id, before.session_id, 'the row was re-acquired under a NEW secret');
  assert.match(res.stdout, new RegExp(after.session_id),
    'FINDING X-1c: and the fresh secret is printed to whoever ran the hook');
  // Bound: the key file is rewritten with the same new secret, so the genuine
  // window is NOT locked out — both parties converge on the same value, which
  // is precisely the pre-existing same-user shared-readability property.
  assert.equal(readSessionSecret(victimSid), after.session_id,
    'the real window is not locked out — the key file tracks the re-mint');
});

// ── X-6: hook robustness ─────────────────────────────────────────────────────

test('X-6a: a MISSING key file degrades to the old behaviour plus a loud notice, never a crash', async () => {
  const sid = 'b486atk-x6a-never-checked-in';
  assert.equal(fs.existsSync(sessionKeyFile(sid)), false, 'precondition: no key file');
  const res = runHook(sid);
  assert.equal(res.status, 0, 'fail-open');
  assert.match(res.stdout, /no --session secret presented/,
    'GR#17: the exact old failure mode (no secret -> nothing renewed) must now announce itself');
});

test('X-6b: an UNREADABLE claude-sync.js next to the hook does not crash it (require failure is caught)', () => {
  const { readOwnSessionSecret } = require('./claude-ping-check.js');
  // Direct unit proof of the catch: a window id that cannot resolve to a file
  // returns '' rather than throwing, on every shape.
  for (const wid of [' bad', 'x'.repeat(300), 'a/b/c']) {
    assert.doesNotThrow(() => readOwnSessionSecret(wid), `readOwnSessionSecret must not throw on ${JSON.stringify(wid)}`);
  }
});
