/**
 * claude-sync.test.js — unit + subprocess tests for FEAT-069 (tool.syncmsg):
 * unread-message delivery on checkin, per-identity read cursor.
 *
 * Runs against a scratch database (METRO_DB_NAME=metro_test_syncmsg), created
 * and dropped by this suite. The real `metro` database — the one this very
 * session's own Bill/Bob/Ben coordination depends on — is NEVER written to by
 * any DB query in this file. The one unavoidable filesystem side effect
 * (acquire() -> writeIdentityFiles() writes .claude/.identity + a per-window
 * .claude/.identity-<id> file, independent of which DB backs the permit) is
 * guarded: this suite snapshots the shared .claude/.identity file before
 * running and restores it verbatim afterward, and every synthetic session id
 * used below is an obviously-fake, grep-able string (never a real UUID that
 * could collide with a live window's).
 *
 * Covers, per docs/planning/acceptance/tool.syncmsg.md:
 *   AC-1  schema idempotency + cursor seeded with exactly 3 rows
 *   AC-2  message requires an active permit; from_name mandatory, never NULL
 *   AC-3  broadcast (--to omitted) vs directed (--to Bob) to_name switch
 *   AC-4  unknown --to rejected, no partial write, exact reused error string
 *   AC-5  argv parser actually consumes --to's value (no body corruption)
 *   AC-6  --body-file byte-identical read; inline+--body-file mutually excl.
 *   AC-7  checkin delivers unread + advances cursor (DB-verified, not just stdout)
 *   AC-8  offline-at-send-time identity still gets delivery on next checkin,
 *         even from a different window/session id
 *   AC-9  sender's own message never re-surfaces to the sender; recipient still sees it
 *   AC-10 broadcast delivery is independent per identity (separate cursor rows)
 *   AC-11 unread messages surface oldest-first (ascending id)
 *   AC-12 delivery fires on checkin only, never on `renew --auto`
 *   AC-13 header comment documents the new command
 *
 * Also covers FEAT-070 (tool.looparm) Destructive REJECT fix-round regression
 * tests, rounds 1 and 2:
 *   Finding #1 (round 1) / Finding A (round 2, Marrow) — loop-set/loop-clear/
 *     loop-show identity resolution. Round 1 closed the `--session` FLAG
 *     path; round 2 found WINDOW_ID itself (the env var) was still fully
 *     trusted, letting a permit-less attacker who merely knows a victim's
 *     session UUID impersonate them with zero flags. Fixed by authenticating
 *     these three commands ONLY via the server-issued session secret
 *     (`findMineBySessionSecret`) — WINDOW_ID is no longer consulted at all
 *     for this identity boundary.
 *   Finding #2 (round 1) / Finding B (round 2, Marrow) — spec content
 *     sanitization. Round 1's ASCII-only control-character blocklist missed
 *     TAB, Unicode line/paragraph separators (U+2028/U+2029), bidi/RTL
 *     override characters (U+202E etc.), and zero-width characters. Fixed by
 *     switching to a printable-ASCII ALLOWLIST.
 *   Finding #3 (round 1, confirmed clean both rounds) — printLoopArmStatus's
 *     TOCTOU race under the FOR UPDATE lock.
 *
 * Run: node --test claude-sync.test.js
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
const TEST_DB = process.env.METRO_DB_TEST_NAME || 'metro_test_syncmsg';

const DB_HOST = process.env.METRO_DB_HOST || '127.0.0.1';
const DB_PORT = Number(process.env.METRO_DB_PORT || 3306);
const DB_USER = process.env.METRO_DB_USER || 'root';
const DB_PASSWORD = process.env.METRO_DB_PASSWORD || '';

const IDENTITY_FILE = path.join(ROOT, '.claude', '.identity');

let db;
let realBowLikeCheck; // not applicable here, but keep the "never touch real metro" proof explicit below
let identitySnapshot = null;
let identitySnapshotExisted = false;

/** Run claude-sync.js as a real subprocess against the scratch DB — every
 *  test that exercises the CLI surface (argv parsing, exit codes, printed
 *  output) goes through here, never in-process, so AC-5's argv-parser fix is
 *  actually exercised end-to-end. */
function run(args, sessionId) {
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
      CLAUDE_IDENTITY: '', // never let the real operator's preferred slot leak into these
    },
  });
}

function checkin(name, sessionId) {
  return run(['checkin', '--name', name], sessionId);
}

test.before(async () => {
  // Snapshot the shared identity fallback file so this suite's synthetic
  // acquire() calls (unavoidable side effect of exercising `checkin` as a
  // real subprocess) can be undone byte-for-byte afterward.
  try {
    identitySnapshot = fs.readFileSync(IDENTITY_FILE, 'utf8');
    identitySnapshotExisted = true;
  } catch {
    identitySnapshotExisted = false;
  }

  const boot = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD });
  await boot.query(`CREATE DATABASE IF NOT EXISTS \`${TEST_DB}\``);
  await boot.end();

  db = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD, database: TEST_DB });
  const sync = require('./claude-sync.js');
  await sync.ensureSchema(db);
});

test.after(async () => {
  if (db) {
    await db.query(`DROP DATABASE IF EXISTS \`${TEST_DB}\``);
    await db.end();
  }
  // Restore the shared identity fallback file exactly as found.
  try {
    if (identitySnapshotExisted) fs.writeFileSync(IDENTITY_FILE, identitySnapshot, 'utf8');
  } catch { /* best-effort restore — never fail the suite over this */ }
});

test.beforeEach(async () => {
  // Full clean slate every test: all three ACs about independence (AC-9/10)
  // and the offline-identity AC-8 depend on starting from FREE permits and
  // an empty message/cursor state, not on carefully sequencing session ids
  // across the whole suite.
  await db.query('DELETE FROM sync_messages');
  await db.query('UPDATE sync_read_cursor SET last_read_id=0');
  await db.query(`UPDATE sync_permits SET session_id=NULL, window_id=NULL, acquired_ms=NULL,
    expires_ms=NULL, heartbeat_ms=NULL, boot_id=NULL, released=1`);
  await db.query('DELETE FROM sync_file_claims');
  await db.query('DELETE FROM sync_activity');
  await db.query('DELETE FROM sync_window_map');
  await db.query('DELETE FROM sync_loop_config');
});

// ── AC-1: schema idempotency ────────────────────────────────────────────────

test('AC-1: ensureSchema is idempotent and seeds exactly 3 read-cursor rows', async () => {
  const AC1_DB = `${TEST_DB}_ac1`;
  const boot = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD });
  await boot.query(`DROP DATABASE IF EXISTS \`${AC1_DB}\``);
  // claude-sync.js's connect() has never auto-created the target database
  // (it just opens a connection with `database: <name>` and fails if that
  // database doesn't exist yet, same as the real `metro` DB was created
  // manually once per CLAUDE.md) — only ensureSchema creates TABLES inside an
  // already-existing database. So the throwaway DB itself must be created
  // here first, matching how test.before() bootstraps TEST_DB above; this is
  // not new FEAT-069 behaviour, just this AC's own scratch-DB setup.
  await boot.query(`CREATE DATABASE \`${AC1_DB}\``);
  await boot.end();

  const runInit = () => spawnSync(process.execPath, ['claude-sync.js', 'init'], {
    cwd: ROOT,
    encoding: 'utf8',
    env: { ...process.env, METRO_DB_NAME: AC1_DB, METRO_DB_HOST: DB_HOST, METRO_DB_PORT: String(DB_PORT), METRO_DB_USER: DB_USER, METRO_DB_PASSWORD: DB_PASSWORD },
  });

  const first = runInit();
  assert.equal(first.status, 0, `first init should exit 0: ${first.stderr}`);

  const check = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD, database: AC1_DB });
  const [[row1]] = await check.query('SELECT COUNT(*) AS n FROM sync_read_cursor');
  assert.equal(row1.n, 3, 'sync_read_cursor must have exactly 3 rows immediately after first ensureSchema run');

  const second = runInit();
  assert.equal(second.status, 0, `second init (already-migrated DB) should also exit 0, no duplicate-table error: ${second.stderr}`);
  const [[row2]] = await check.query('SELECT COUNT(*) AS n FROM sync_read_cursor');
  assert.equal(row2.n, 3, 'a second ensureSchema run must not change the row count (false-pass guard: not just "did not crash")');
  const [[permitsRow]] = await check.query('SELECT COUNT(*) AS n FROM sync_permits');
  assert.equal(permitsRow.n, 3, 'sync_permits row count also unchanged by the second run');

  await check.end();
  const drop = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD });
  await drop.query(`DROP DATABASE IF EXISTS \`${AC1_DB}\``);
  await drop.end();
});

// ── AC-2: permit required; from_name mandatory ──────────────────────────────

test('AC-2a: message with no active permit exits non-zero and writes no row', () => {
  const res = run(['message', 'hi'], 'ac2-no-permit-session');
  assert.notEqual(res.status, 0);
});

test('AC-2a: no row written when permit missing (DB-verified)', async () => {
  const [[before]] = await db.query('SELECT COUNT(*) AS n FROM sync_messages');
  run(['message', 'hi'], 'ac2-no-permit-session-2');
  const [[after]] = await db.query('SELECT COUNT(*) AS n FROM sync_messages');
  assert.equal(after.n, before.n, 'message with no permit must not write a sync_messages row');
});

test('AC-2b: sent message records the caller\'s own resolved identity as from_name', async () => {
  const sid = 'ac2-bob-session';
  const ci = checkin('Bob', sid);
  assert.equal(ci.status, 0, `checkin should succeed: ${ci.stderr}`);
  const msg = run(['message', 'hi'], sid);
  assert.equal(msg.status, 0, `message should succeed: ${msg.stderr}`);
  const [rows] = await db.query('SELECT from_name FROM sync_messages ORDER BY id DESC LIMIT 1');
  assert.equal(rows[0].from_name, 'Bob');
});

// ── AC-3: broadcast vs directed ──────────────────────────────────────────────

test('AC-3a: message with no --to writes to_name IS NULL (broadcast)', async () => {
  const sid = 'ac3-broadcast-session';
  assert.equal(checkin('Bill', sid).status, 0);
  assert.equal(run(['message', 'broadcast text'], sid).status, 0);
  const [rows] = await db.query('SELECT to_name FROM sync_messages ORDER BY id DESC LIMIT 1');
  assert.equal(rows[0].to_name, null);
});

test('AC-3b: message --to Bob writes to_name = Bob', async () => {
  const sid = 'ac3-directed-session';
  assert.equal(checkin('Bill', sid).status, 0);
  assert.equal(run(['message', 'directed text', '--to', 'Bob'], sid).status, 0);
  const [rows] = await db.query('SELECT to_name FROM sync_messages ORDER BY id DESC LIMIT 1');
  assert.equal(rows[0].to_name, 'Bob');
});

// ── AC-4: unknown --to rejected ──────────────────────────────────────────────

test('AC-4: unknown --to target rejected, no partial write, exact reused string', async () => {
  const sid = 'ac4-session';
  assert.equal(checkin('Bill', sid).status, 0);
  const [[before]] = await db.query('SELECT COUNT(*) AS n FROM sync_messages');
  const res = run(['message', 'hi', '--to', 'Nobody'], sid);
  assert.notEqual(res.status, 0);
  assert.match(res.stderr, /Unknown slot name "Nobody"\. Valid: Bill, Bob, Ben/);
  const [[after]] = await db.query('SELECT COUNT(*) AS n FROM sync_messages');
  assert.equal(after.n, before.n);
});

// ── AC-5: argv parser actually consumes --to's value ────────────────────────

test('AC-5: --to consumes its value; body is not corrupted with the target name', async () => {
  const sid = 'ac5-session';
  assert.equal(checkin('Bill', sid).status, 0);
  assert.equal(run(['message', 'hi', '--to', 'Bob'], sid).status, 0);
  const [rows] = await db.query('SELECT body, to_name FROM sync_messages ORDER BY id DESC LIMIT 1');
  assert.equal(rows[0].body, 'hi', 'body must be exactly "hi", not "hi Bob" or similar concatenation');
  assert.equal(rows[0].to_name, 'Bob');
});

// ── AC-6: --body-file ─────────────────────────────────────────────────────────

test('AC-6a: --body-file reads the body byte-identically, including shell-special chars', async () => {
  const sid = 'ac6-session';
  assert.equal(checkin('Bill', sid).status, 0);
  const tricky = 'line with a `backtick`, a $(subshell) and an embedded "double quote" — verbatim.';
  const tmpFile = path.join(os.tmpdir(), `claude-sync-test-body-${Date.now()}.txt`);
  fs.writeFileSync(tmpFile, tricky, 'utf8');
  try {
    const res = run(['message', '--to', 'Bob', '--body-file', tmpFile], sid);
    assert.equal(res.status, 0, `message --body-file should succeed: ${res.stderr}`);
    const [rows] = await db.query('SELECT body FROM sync_messages ORDER BY id DESC LIMIT 1');
    assert.equal(rows[0].body, tricky, 'stored body must be byte-identical to the file content');
  } finally {
    fs.unlinkSync(tmpFile);
  }
});

test('AC-6b: inline text + --body-file together is rejected, no row written', async () => {
  const tmpFile = path.join(os.tmpdir(), `claude-sync-test-body2-${Date.now()}.txt`);
  fs.writeFileSync(tmpFile, 'file body', 'utf8');
  try {
    const [[before]] = await db.query('SELECT COUNT(*) AS n FROM sync_messages');
    const res = run(['message', 'inline text', '--to', 'Bob', '--body-file', tmpFile], 'ac6b-session');
    assert.notEqual(res.status, 0);
    const [[after]] = await db.query('SELECT COUNT(*) AS n FROM sync_messages');
    assert.equal(after.n, before.n);
  } finally {
    fs.unlinkSync(tmpFile);
  }
});

// ── AC-7: checkin delivers + advances cursor ────────────────────────────────

test('AC-7: checkin surfaces unread messages and advances the cursor (DB-verified)', async () => {
  const [result] = await db.query(
    "INSERT INTO sync_messages (from_name, to_name, body) VALUES ('Bill', 'Bob', 'hello Bob')"
  );
  const msgId = result.insertId;
  const res = checkin('Bob', 'ac7-session');
  assert.equal(res.status, 0, `checkin should succeed: ${res.stderr}`);
  assert.match(res.stdout, /hello Bob/);
  const [rows] = await db.query('SELECT last_read_id FROM sync_read_cursor WHERE name="Bob"');
  assert.ok(Number(rows[0].last_read_id) >= msgId, 'cursor must have advanced to at least the delivered message id');
});

test('AC-7 false-pass guard: a second checkin does not re-print the already-delivered message', async () => {
  await db.query("INSERT INTO sync_messages (from_name, to_name, body) VALUES ('Bill', 'Bob', 'once only please')");
  const sid = 'ac7b-session';
  const first = checkin('Bob', sid);
  assert.match(first.stdout, /once only please/);
  const second = checkin('Bob', sid); // renew-of-self path
  assert.doesNotMatch(second.stdout, /once only please/);
});

// ── AC-8: offline identity, delivered to a different window later ──────────

test('AC-8: message to an offline identity persists and delivers on a later checkin from a different window', async () => {
  // No active Ben permit exists (beforeEach reset everyone to FREE).
  const sid1 = 'ac8-sender-session';
  assert.equal(checkin('Bill', sid1).status, 0);
  assert.equal(run(['message', 'hi Ben, are you there?', '--to', 'Ben'], sid1).status, 0);

  // Deliver via a DIFFERENT, never-before-seen window/session id.
  const sid2 = 'ac8-different-window-session';
  const res = checkin('Ben', sid2);
  assert.equal(res.status, 0, `checkin should succeed: ${res.stderr}`);
  assert.match(res.stdout, /hi Ben, are you there\?/);
});

// ── AC-9: sender suppression ─────────────────────────────────────────────────

test('AC-9: sender never sees their own message on their next checkin; recipient does', async () => {
  const billSid = 'ac9-bill-session';
  assert.equal(checkin('Bill', billSid).status, 0);
  assert.equal(run(['message', 'note', '--to', 'Bob'], billSid).status, 0);

  // Bill checks in again (renew-of-self path) — must NOT see "note".
  const billAgain = checkin('Bill', billSid);
  assert.equal(billAgain.status, 0);
  assert.doesNotMatch(billAgain.stdout, /note/, 'sender must never see their own just-sent message flagged unread');

  // Control case: Bob's checkin DOES see it.
  const bobSid = 'ac9-bob-session';
  const bobCi = checkin('Bob', bobSid);
  assert.match(bobCi.stdout, /note/, 'the actual recipient must still see the message');
});

// ── AC-10: broadcast delivery independent per identity ──────────────────────

test('AC-10: broadcast is delivered independently to each identity (separate cursor rows)', async () => {
  const senderSid = 'ac10-sender-session';
  assert.equal(checkin('Bill', senderSid).status, 0);
  assert.equal(run(['message', 'broadcast to everyone'], senderSid).status, 0);

  const bobCi = checkin('Bob', 'ac10-bob-session');
  assert.match(bobCi.stdout, /broadcast to everyone/);

  // Ben must STILL receive it — one identity's cursor advancing must not
  // mark it read for another (the single-shared-scalar bug class).
  const benCi = checkin('Ben', 'ac10-ben-session');
  assert.match(benCi.stdout, /broadcast to everyone/, "Ben's independent cursor must still be behind the broadcast");
});

// ── AC-11: chronological ordering ────────────────────────────────────────────

test('AC-11: multiple unread messages surface oldest-first', async () => {
  const senderSid = 'ac11-sender-session';
  assert.equal(checkin('Bill', senderSid).status, 0);
  assert.equal(run(['message', 'first', '--to', 'Bob'], senderSid).status, 0);
  assert.equal(run(['message', 'second', '--to', 'Bob'], senderSid).status, 0);
  assert.equal(run(['message', 'third', '--to', 'Bob'], senderSid).status, 0);

  const bobCi = checkin('Bob', 'ac11-bob-session');
  const out = bobCi.stdout;
  const iFirst = out.indexOf('first');
  const iSecond = out.indexOf('second');
  const iThird = out.indexOf('third');
  assert.ok(iFirst >= 0 && iSecond >= 0 && iThird >= 0, 'all three messages must be present in stdout');
  assert.ok(iFirst < iSecond, '"first" must appear before "second"');
  assert.ok(iSecond < iThird, '"second" must appear before "third"');
});

// ── AC-12: checkin-only delivery, never renew --auto ────────────────────────

test('AC-12: renew --auto never surfaces or consumes unread messages', async () => {
  const sid = 'ac12-session';
  assert.equal(checkin('Bob', sid).status, 0); // Bob's permit now active with ~5min remaining

  const senderSid = 'ac12-sender-session';
  assert.equal(checkin('Bill', senderSid).status, 0);
  assert.equal(run(['message', 'mid-session arrival', '--to', 'Bob'], senderSid).status, 0);

  const autoRenew = run(['renew', '--auto'], sid);
  assert.equal(autoRenew.status, 0, `renew --auto should succeed: ${autoRenew.stderr}`);
  assert.equal(autoRenew.stdout.trim(), '', 'renew --auto must stay silent, matching today\'s heartbeat-only behaviour');

  const [rows] = await db.query('SELECT last_read_id FROM sync_read_cursor WHERE name="Bob"');
  assert.equal(Number(rows[0].last_read_id), 0, 'cursor must NOT advance on renew --auto — message still pending');

  // A genuine subsequent checkin (renew-of-self path) DOES deliver it.
  const genuineCheckin = checkin('Bob', sid);
  assert.match(genuineCheckin.stdout, /mid-session arrival/);
});

// ── AC-13: documentation ─────────────────────────────────────────────────────

test('AC-13: header comment documents the new message command', () => {
  const src = fs.readFileSync(path.join(ROOT, 'claude-sync.js'), 'utf8');
  const headerBlock = src.slice(0, src.indexOf('\'use strict\''));
  assert.match(headerBlock, /message\s+"<text>"\s+\[--to <Name>\]\s+\[--body-file <path>\]/,
    'the Commands: header block must document the new message command shape');
  assert.match(headerBlock, /checkin/, 'checkin entry should still be present');
  assert.match(headerBlock, /deliver|unread/i, 'a note near checkin about message delivery should be present');
});

// ── FEAT-070 (tool.looparm) Destructive REJECT fix-round regression tests ──
// Thresher's three findings, reproduced pre-fix in isolation (see the fork's
// report) and closed here as permanent regressions against the real CLI
// surface, exactly as the other tests in this file do (real subprocess,
// scratch DB, never the live `metro` database).

// FEAT-070 Destructive REJECT round 2, finding A: loop-set/clear/show now
// authenticate ONLY via the DB-issued session secret (findMineBySessionSecret),
// never via WINDOW_ID. `loopSet` below therefore takes the REAL session id
// (captured from a prior checkin's own stdout), not the window/env id.
function loopSet(sessionSecret, spec) {
  return run(['loop-set', '--session', sessionSecret, spec], '');
}
function loopShow(sessionSecret) {
  return run(['loop-show', '--session', sessionSecret], '');
}
function loopClear(sessionSecret) {
  return run(['loop-clear', '--session', sessionSecret], '');
}
/** Extract the "Session: <uuid>" line a real checkin prints, i.e. the
 *  server-issued secret findMineBySessionSecret now requires. */
function captureSessionSecret(checkinResult) {
  const m = checkinResult.stdout.match(/^Session: (\S+)$/m);
  assert.ok(m, `checkin stdout must contain a "Session: <uuid>" line: ${checkinResult.stdout}`);
  return m[1];
}
function loopSetViaSession(sessionId, spec) {
  // No CLAUDE_CODE_SESSION_ID for this call — only --session is supplied,
  // simulating an attacker who holds no permit of their own and knows only
  // a WINDOW_ID-shaped value (a session UUID learned some other way, NOT
  // the DB-issued session secret).
  return run(['loop-set', '--session', sessionId, spec], '');
}
function loopClearViaSession(sessionId) {
  return run(['loop-clear', '--session', sessionId], '');
}
function loopShowViaSession(sessionId) {
  return run(['loop-show', '--session', sessionId], '');
}

// ── Finding #1 (round 1): cross-identity compromise via --session (AC-12) ──

test('Finding #1: loop-set from a window with NO permit of its own, using only another identity\'s WINDOW_ID-shaped --session, is REJECTED', async () => {
  const victimSid = 'f1-victim-session';
  assert.equal(checkin('Bill', victimSid).status, 0);

  const before = (await db.query('SELECT spec FROM sync_loop_config WHERE name="Bill"'))[0];
  assert.equal(before.length, 0, 'no loop configured on Bill yet');

  // Attacker: no CLAUDE_CODE_SESSION_ID of their own, only Bill's WINDOW_ID
  // value (NOT the real DB-issued session secret) passed via --session.
  const attack = loopSetViaSession(victimSid, '99m /attacker-planted-loop');
  assert.notEqual(attack.status, 0, 'loop-set via bare --session of a non-secret value (no owning window) must be rejected');
  assert.match(attack.stderr, /No active permit/);

  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Bill"');
  assert.equal(rows.length, 0, 'attacker must not have been able to plant a loop config on Bill\'s row');
});

test('Finding #1: loop-clear and loop-show are equally rejected via bare (non-secret) --session', async () => {
  const victimCi = checkin('Bill', 'f1-victim-session-2');
  assert.equal(victimCi.status, 0);
  const victimSecret = captureSessionSecret(victimCi);
  assert.equal(loopSet(victimSecret, '15m /legit').status, 0);

  // Attacker passes the WINDOW_ID value (session UUID), not the real secret.
  const clearAttack = loopClearViaSession('f1-victim-session-2');
  assert.notEqual(clearAttack.status, 0, 'loop-clear via bare --session must be rejected');
  const showAttack = loopShowViaSession('f1-victim-session-2');
  assert.notEqual(showAttack.status, 0, 'loop-show via bare --session must be rejected');

  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Bill"');
  assert.equal(rows.length, 1, 'the victim\'s loop config must be untouched by the rejected clear attempt');
  assert.equal(rows[0].spec, '15m /legit');
});

test('Finding #1 false-pass guard: a window presenting its OWN real session secret can still loop-set/show/clear its own row', async () => {
  const sid = 'f1-legit-session';
  const ci = checkin('Bob', sid);
  assert.equal(ci.status, 0);
  const secret = captureSessionSecret(ci);

  const set = loopSet(secret, '20m /oversight-sweep');
  assert.equal(set.status, 0, `legit loop-set should succeed: ${set.stderr}`);

  const show = loopShow(secret);
  assert.equal(show.status, 0, `legit loop-show should succeed: ${show.stderr}`);
  assert.match(show.stdout, /20m \/oversight-sweep/);

  const clear = loopClear(secret);
  assert.equal(clear.status, 0, `legit loop-clear should succeed: ${clear.stderr}`);

  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Bob"');
  assert.equal(rows.length, 0, 'own-secret clear must actually remove the row');
});

// ── Finding A (round 2, Marrow): WINDOW_ID itself is not proof of identity ──
// Round 1 closed the --session FLAG path but left the WINDOW_ID branch of
// findMine fully trusted. An attacker holding NO permit at all who merely
// sets CLAUDE_CODE_SESSION_ID to a victim's real window/session UUID (in
// their OWN separate process) used to pass loop-set/loop-clear/loop-show
// with ZERO flags. loop-set/clear/show must now authenticate ONLY via the
// server-issued session secret (findMineBySessionSecret) — WINDOW_ID is
// never consulted for these three commands at all.

test('Finding A: an attacker who merely knows the victim\'s WINDOW_ID (CLAUDE_CODE_SESSION_ID), with ZERO flags, cannot read the victim\'s loop', async () => {
  const victimSid = 'fA-victim-window-id';
  const victimCi = checkin('Ben', victimSid);
  assert.equal(victimCi.status, 0);
  const realSecret = captureSessionSecret(victimCi);
  assert.equal(loopSet(realSecret, '15m /victim-loop').status, 0);

  // Attacker: separate process, sets CLAUDE_CODE_SESSION_ID to the exact
  // value the victim used as WINDOW_ID — no --session flag at all. This is
  // Marrow's literal repro (round 2 REJECT, finding A).
  const attackShow = run(['loop-show'], victimSid);
  assert.notEqual(attackShow.status, 0, 'loop-show via bare WINDOW_ID spoofing must be rejected');
  assert.doesNotMatch(attackShow.stdout, /victim-loop/, 'the victim\'s spec must never be disclosed to the WINDOW_ID-only attacker');
});

test('Finding A: the same WINDOW_ID-spoofing attacker cannot overwrite or delete the victim\'s loop', async () => {
  const victimSid = 'fA-victim-window-id-2';
  const victimCi = checkin('Ben', victimSid);
  assert.equal(victimCi.status, 0);
  const realSecret = captureSessionSecret(victimCi);
  assert.equal(loopSet(realSecret, '15m /victim-loop').status, 0);

  // Attacker: bare WINDOW_ID spoof, zero flags, no permit of their own.
  const attackOverwrite = run(['loop-set', '1m /evil-payload'], victimSid);
  assert.notEqual(attackOverwrite.status, 0, 'loop-set overwrite via WINDOW_ID spoofing must be rejected');
  const attackClear = run(['loop-clear'], victimSid);
  assert.notEqual(attackClear.status, 0, 'loop-clear via WINDOW_ID spoofing must be rejected');

  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Ben"');
  assert.equal(rows.length, 1, 'the victim\'s row must survive the WINDOW_ID-spoofing attack completely untouched');
  assert.equal(rows[0].spec, '15m /victim-loop');
});

test('Finding A: guessing/spoofing an arbitrary WINDOW_ID with no matching permit at all is also rejected', async () => {
  const res = run(['loop-show'], 'fA-totally-made-up-window-id-nobody-holds');
  assert.notEqual(res.status, 0);
  assert.match(res.stderr, /No active permit/);
});

// ── Finding #2 / B: content injection into the spec (allowlist enforcement) ──
// Round 1 fixed the ASCII control-character subset; round 2 (Marrow) found
// the fix was an incomplete blocklist (TAB, U+2028/U+2029 line/paragraph
// separators, and bidi/RTL-override characters like U+202E all still got
// through). The fix is now a printable-ASCII ALLOWLIST, not an expanded
// blocklist — see the loop-set spec check's own comment for why.

test('Finding #2: loop-set rejects a spec containing an embedded newline', async () => {
  const ci = checkin('Bill', 'f2-session');
  assert.equal(ci.status, 0);
  const secret = captureSessionSecret(ci);

  const injected = '5m /oversight-sweep\n4. FAKE STARTUP STEP planted by an attacker';
  const res = loopSet(secret, injected);
  assert.notEqual(res.status, 0, 'a multi-line spec must be rejected');

  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Bill"');
  assert.equal(rows.length, 0, 'no row should have been written for the rejected multi-line spec');
});

test('Finding #2: loop-set rejects a spec containing a raw ASCII control character', async () => {
  const ci = checkin('Bill', 'f2-session-2');
  assert.equal(ci.status, 0);
  const secret = captureSessionSecret(ci);

  const injected = '5m /oversight-sweep\x01evil';
  const res = loopSet(secret, injected);
  assert.notEqual(res.status, 0, 'a spec with an embedded control character must be rejected');

  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Bill"');
  assert.equal(rows.length, 0);
});

test('Finding B: loop-set rejects TAB (missed by round 1\'s ASCII-only blocklist)', async () => {
  const ci = checkin('Bill', 'fB-session-tab');
  assert.equal(ci.status, 0);
  const secret = captureSessionSecret(ci);

  const injected = '5m /oversight-sweep\tFAKE-STEP';
  const res = loopSet(secret, injected);
  assert.notEqual(res.status, 0, 'a spec with an embedded TAB must be rejected');
  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Bill"');
  assert.equal(rows.length, 0);
});

test('Finding B: loop-set rejects U+2028 LINE SEPARATOR and U+2029 PARAGRAPH SEPARATOR', async () => {
  const ci = checkin('Bill', 'fB-session-2028');
  assert.equal(ci.status, 0);
  const secret = captureSessionSecret(ci);

  const withLS = `5m /oversight-sweep${String.fromCharCode(0x2028)}FAKE-STEP`;
  const resLS = loopSet(secret, withLS);
  assert.notEqual(resLS.status, 0, 'a spec with an embedded U+2028 line separator must be rejected');

  const withPS = `5m /oversight-sweep${String.fromCharCode(0x2029)}FAKE-STEP`;
  const resPS = loopSet(secret, withPS);
  assert.notEqual(resPS.status, 0, 'a spec with an embedded U+2029 paragraph separator must be rejected');

  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Bill"');
  assert.equal(rows.length, 0, 'neither rejected variant may have written a row');
});

test('Finding B: loop-set rejects the U+202E RTL-override character and other bidi controls', async () => {
  const ci = checkin('Bill', 'fB-session-bidi');
  assert.equal(ci.status, 0);
  const secret = captureSessionSecret(ci);

  const withRLO = `5m /oversight-sweep${String.fromCharCode(0x202E)}evil-reversed`;
  const res = loopSet(secret, withRLO);
  assert.notEqual(res.status, 0, 'a spec with an embedded U+202E RTL override must be rejected');

  const withEmbed = `5m /oversight-sweep${String.fromCharCode(0x2066)}evil`;
  const res2 = loopSet(secret, withEmbed);
  assert.notEqual(res2.status, 0, 'a spec with an embedded U+2066 bidi isolate must be rejected');

  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Bill"');
  assert.equal(rows.length, 0);
});

test('Finding B: loop-set rejects zero-width characters (U+200B, U+FEFF)', async () => {
  const ci = checkin('Bill', 'fB-session-zerowidth');
  assert.equal(ci.status, 0);
  const secret = captureSessionSecret(ci);

  const withZWSP = `5m/oversight${String.fromCharCode(0x200B)}sweep`;
  const res = loopSet(secret, withZWSP);
  assert.notEqual(res.status, 0, 'a spec with an embedded zero-width space must be rejected');

  // U+FEFF at a string BOUNDARY is stripped by JS's own String.trim() (it is
  // explicitly part of ECMAScript's WhiteSpace production) before the
  // allowlist check ever runs — that is expected trim() behaviour, not a
  // bypass, since loop-set already trims the whole spec first. Embed it
  // mid-string, which is also the more realistic spoofing position, to
  // actually exercise the allowlist check itself.
  const withBOM = `5m /oversight${String.fromCharCode(0xFEFF)}-sweep`;
  const res2 = loopSet(secret, withBOM);
  assert.notEqual(res2.status, 0, 'a spec with an embedded BOM/zero-width-no-break-space must be rejected');

  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Bill"');
  assert.equal(rows.length, 0);
});

test('Finding #2/B false-pass guard: a normal printable-ASCII single-line spec is still accepted verbatim', async () => {
  const ci = checkin('Bill', 'f2-session-3');
  assert.equal(ci.status, 0);
  const secret = captureSessionSecret(ci);

  const res = loopSet(secret, '15m /oversight-sweep');
  assert.equal(res.status, 0, `a plain single-line spec must still be accepted: ${res.stderr}`);

  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Bill"');
  assert.equal(rows.length, 1);
  assert.equal(rows[0].spec, '15m /oversight-sweep');
});

// ── Finding #3: AC-9 "exactly once" race on printLoopArmStatus ─────────────

test('Finding #3: printLoopArmStatus arms under a locked transaction — the update behind a printed MANDATORY line always persists', async () => {
  const sid = 'f3-session';
  const ci = checkin('Bill', sid);
  assert.equal(ci.status, 0);
  assert.equal(loopSet(captureSessionSecret(ci), '5m /oversight-sweep').status, 0);
  await db.query('UPDATE sync_loop_config SET armed_count=0, last_armed_ms=NULL WHERE name="Bill"');

  const sync = require('./claude-sync.js');
  // Fresh connection matching the CLI's own (checkin's own db.commit() has
  // already happened by the time printLoopArmStatus runs — see printSuccess).
  const raceDb = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD, database: TEST_DB });
  const clearDb = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD, database: TEST_DB });

  const origQuery = raceDb.query.bind(raceDb);
  let sawSelect = false;
  let updateAffectedRows = null;
  raceDb.query = async (sql, params) => {
    if (typeof sql === 'string' && sql.includes('FROM sync_loop_config WHERE name=') && !sawSelect) {
      sawSelect = true;
      const result = await origQuery(sql, params);
      // Fire a concurrent loop-clear-equivalent DELETE into the exact gap
      // between the SELECT and the UPDATE. Not awaited: post-fix this is
      // expected to block behind the row lock until printLoopArmStatus commits.
      global.__f3ClearPromise = clearDb.query('DELETE FROM sync_loop_config WHERE name="Bill"');
      return result;
    }
    if (typeof sql === 'string' && sql.startsWith('UPDATE sync_loop_config SET last_armed_ms')) {
      const [res] = await origQuery(sql, params);
      updateAffectedRows = res.affectedRows;
      return [res];
    }
    return origQuery(sql, params);
  };

  const logs = [];
  const origLog = console.log;
  console.log = (...a) => logs.push(a.join(' '));
  try {
    await sync.printLoopArmStatus(raceDb, 'Bill');
  } finally {
    console.log = origLog;
  }
  await global.__f3ClearPromise;

  const printedMandatory = logs.join('\n').includes('MANDATORY: invoke');
  assert.ok(printedMandatory, 'the arm decision should have printed MANDATORY (row was present when the transaction started)');
  // The regression this test guards: a printed MANDATORY line must never be
  // backed by an UPDATE that silently touched 0 rows (that mismatch is
  // exactly Thresher's pre-fix finding #3 — reproduced in the fix-round fork
  // against the pre-fix file with affectedRows===0 for this exact scenario).
  assert.equal(updateAffectedRows, 1,
    'the UPDATE behind a printed MANDATORY line must have actually persisted (concurrent clear must be serialized to AFTER commit, not interleaved)');

  await raceDb.end();
  await clearDb.end();
});

test('Finding #3 false-pass guard: a single checkin arms the standing loop exactly once', async () => {
  const sid = 'f3-once-session';
  const ci = checkin('Bill', sid);
  assert.equal(ci.status, 0);
  assert.equal(loopSet(captureSessionSecret(ci), '5m /oversight-sweep').status, 0);
  await db.query('UPDATE sync_loop_config SET armed_count=0, last_armed_ms=NULL WHERE name="Bill"');

  // A genuine subsequent checkin (renew-of-self path) triggers printLoopArmStatus.
  const ci2 = checkin('Bill', sid);
  assert.equal(ci2.status, 0);
  const mandatoryCount = (ci2.stdout.match(/MANDATORY: invoke/g) || []).length;
  assert.equal(mandatoryCount, 1, 'exactly one MANDATORY line per checkin call');

  const [rows] = await db.query('SELECT armed_count FROM sync_loop_config WHERE name="Bill"');
  assert.equal(Number(rows[0].armed_count), 1, 'armed_count must advance by exactly 1 for exactly 1 checkin');
});

// ── Culvert Destructive REJECT regression: trailing/ambiguous value-flag ────
// Finding: VALUE_FLAGS parser did `flags[a.slice(2)] = argv[++i]` unconditionally.
// When a value-flag (--to/--body-file/--name/--session) is the LAST argv token,
// argv[++i] runs off the end and evaluates to JS `undefined`, which gets stored
// as the flag's value. cmdMessage's guard `if (flags.to !== undefined)` is then
// FALSE for this exact case, so an explicit --to with no value was silently
// discarded and the message fell through to a BROADCAST instead of erroring.
// Fixed: a missing or flag-shaped next token is now a hard parse error (exit
// non-zero), for --to, --body-file, --name and --session alike.

test('Culvert regression: `message "text" --to` (no value, end of argv) is a hard parse error, NOT a broadcast', async () => {
  const sid = 'culvert-repro-session';
  checkin('Bill', sid);
  const res = run(['message', 'secret for bob only', '--to'], sid);
  assert.notEqual(res.status, 0, 'must exit non-zero, not silently succeed as a broadcast');
  assert.match(res.stderr, /--to requires a value/i);

  // Prove no row was written at all (not even as a broadcast).
  const [rows] = await db.query('SELECT * FROM sync_messages');
  assert.equal(rows.length, 0, 'no message row — directed or broadcast — may be written on a malformed --to');
});

test('Culvert regression: `--body-file` with no value (end of argv) is a hard parse error, no partial write', async () => {
  const sid = 'culvert-repro-bodyfile';
  checkin('Bill', sid);
  const res = run(['message', '--to', 'Bob', '--body-file'], sid);
  assert.notEqual(res.status, 0);
  assert.match(res.stderr, /--body-file requires a value/i);
  const [rows] = await db.query('SELECT * FROM sync_messages');
  assert.equal(rows.length, 0);
});

test('Culvert regression (ambiguous case): `--to --body-file x` — a value-flag immediately followed by another flag — is rejected, not misparsed', async () => {
  const sid = 'culvert-repro-ambiguous';
  checkin('Bill', sid);
  const res = run(['message', 'hi', '--to', '--body-file', 'x'], sid);
  assert.notEqual(res.status, 0, 'must reject rather than silently taking "--body-file" as the --to value');
  assert.match(res.stderr, /--to requires a value/i);
  assert.match(res.stderr, /looks like another flag/i);
  const [rows] = await db.query('SELECT * FROM sync_messages');
  assert.equal(rows.length, 0);
});

test('Culvert regression: same missing-value bug fixed for --name and --session (checkin/renew), not just message flags', async () => {
  // --name with no value: checkin must not silently resolve to undefined/"any" behaviour.
  const resName = run(['checkin', '--name'], 'culvert-name-session');
  assert.notEqual(resName.status, 0, '--name with no value must be a hard parse error');
  assert.match(resName.stderr, /--name requires a value/i);

  // --session with no value: renew must not silently operate on session=undefined.
  const resSession = run(['renew', '--session'], 'culvert-session-session');
  assert.notEqual(resSession.status, 0, '--session with no value must be a hard parse error');
  assert.match(resSession.stderr, /--session requires a value/i);
});

test('Sanity: normal usage is unaffected — `--to Bob` and `--body-file <path>` still parse and work', async () => {
  const sid = 'sanity-normal-usage';
  checkin('Bill', sid);
  assert.equal(run(['message', 'normal directed message', '--to', 'Bob'], sid).status, 0);

  const tmpFile = path.join(os.tmpdir(), `culvert-sanity-${Date.now()}.txt`);
  fs.writeFileSync(tmpFile, 'normal body-file content', 'utf8');
  try {
    const res = run(['message', '--to', 'Bob', '--body-file', tmpFile], sid);
    assert.equal(res.status, 0, `body-file message should still succeed: ${res.stderr}`);
  } finally {
    fs.unlinkSync(tmpFile);
  }

  const [rows] = await db.query('SELECT to_name, body FROM sync_messages ORDER BY id ASC');
  assert.equal(rows.length, 2);
  assert.equal(rows[0].to_name, 'Bob');
  assert.equal(rows[0].body, 'normal directed message');
  assert.equal(rows[1].to_name, 'Bob');
  assert.equal(rows[1].body, 'normal body-file content');
});
