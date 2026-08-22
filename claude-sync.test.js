/**
 * claude-sync.test.js — unit + subprocess tests for FEAT-069 (tool.syncmsg):
 * unread-message delivery on checkin, per-identity read cursor.
 *
 * Runs against a scratch database (METRO_DB_NAME=metro_test_syncmsg), created
 * and dropped by this suite. The real `metro` database — the one this very
 * session's own Bill/Bev/Ben coordination depends on — is NEVER written to by
 * any DB query in this file. The one unavoidable filesystem side effect
 * (acquire() -> writeIdentityFiles() writes .claude/.identity + a per-window
 * .claude/.identity-<id> file, independent of which DB backs the permit) is
 * guarded: this suite snapshots the shared .claude/.identity file before
 * running and restores it verbatim afterward, and every synthetic session id
 * used below is an obviously-fake, grep-able string (never a real UUID that
 * could collide with a live window's).
 *
 * Covers, per docs/planning/acceptance/tool.syncmsg.md:
 *   AC-1  schema idempotency + cursor seeded with exactly 4 rows (one per NAMES entry)
 *   AC-2  message requires an active permit; from_name mandatory, never NULL
 *   AC-3  broadcast (--to omitted) vs directed (--to Bev) to_name switch
 *   AC-4  unknown --to rejected, no partial write, exact reused error string
 *   AC-5  argv parser actually consumes --to's value (no body corruption)
 *   AC-6  --body-file byte-identical read; inline+--body-file mutually excl.
 *   AC-7  FEAT-107 delivery split: checkin shows an UNREAD COUNT only (never
 *         message bodies) and does NOT advance the cursor; `read` is the sole
 *         delivering + cursor-advancing path (DB-verified, not just stdout).
 *         Includes an explicit false-pass guard (body absent from checkin
 *         stdout AND a subsequent `read` still delivers it) — see FEAT-107's
 *         root cause: checkin's own stdout is routinely piped/redirected, and
 *         when checkin both delivered AND advanced the cursor, that silently
 *         destroyed nine unread messages on 2026-08-20.
 *   AC-8  offline-at-send-time identity still gets its COUNT on the next
 *         checkin (even from a different window/session id) and its BODY on
 *         the next `read`
 *   AC-9  sender's own message never re-surfaces to the sender (neither as a
 *         checkin count nor a read delivery); recipient still sees it via read
 *   AC-10 broadcast delivery is independent per identity (separate cursor rows)
 *   AC-11 unread messages surface oldest-first (ascending id) via `read`
 *   AC-12 `renew --auto` never surfaces or consumes unread messages; a
 *         genuine (non-auto) checkin only ever shows the count, never the
 *         body — only `read` delivers
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
const TEST_DB = process.env.METRO_DB_TEST_NAME || `metro_test_syncmsg_${process.pid}`;

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

/** BUG-354 r4/r5 F3: the per-window session-key file path for a test window id.
 *  The subprocess's claude-sync writes `.session-key-<windowId>-<METRO_DB_NAME>`
 *  into the PER-USER os.homedir()/.claude/session-keys directory (r5 moved it
 *  out of the shared checkout — a git repo every lane shares). The DB tag keeps
 *  a test run's key files from ever colliding with a live window's (which are
 *  untagged), and makes cleanup safe to glob. */
function sessionKeyFile(sessionId) {
  return path.join(os.homedir(), '.claude', 'session-keys', `.session-key-${sessionId}-${TEST_DB}`);
}

/** BUG-354 r4: read a window's per-window session secret, mirroring the ping /
 *  startup hooks. Returns '' for a fresh window with no key file yet. */
function readSessionSecret(sessionId) {
  try { return fs.readFileSync(sessionKeyFile(sessionId), 'utf8').trim(); } catch { return ''; }
}

function checkin(name, sessionId) {
  // BUG-354 r4: present the per-window session secret explicitly, exactly as
  // the startup hook now does. Without it, a checkin on a HELD row resolves as
  // a fresh acquire (secret-less callers cannot claim any held/reserved row).
  const args = ['checkin', '--name', name];
  const secret = readSessionSecret(sessionId);
  if (secret) args.push('--session', secret);
  return run(args, sessionId);
}

/** AC-12: renew --auto exactly as the PostToolUse ping hook now invokes it —
 *  with the per-window session secret presented explicitly (BUG-354 r4). */
function renewAuto(sessionId) {
  const args = ['renew', '--auto'];
  const secret = readSessionSecret(sessionId);
  if (secret) args.push('--session', secret);
  return run(args, sessionId);
}

/** FEAT-107: `read` is the sole delivering + cursor-advancing command — this
 *  is the helper the AC-7..AC-12 estate uses to actually receive messages. */
function readCmd(sessionId) {
  return run(['read'], sessionId);
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
  // BUG-354 r4/r5 F3: remove this run's per-window session-key files. They are
  // DB-tagged (`-<TEST_DB>`), so only files this test run wrote are touched —
  // live windows' untagged key files are never globbed. r5 moved the files to
  // the per-user os.homedir()/.claude/session-keys dir; clean BOTH the per-user
  // dir and any legacy checkout-path files a pre-r5 run may have left behind.
  const keyDirs = [
    path.join(os.homedir(), '.claude', 'session-keys'),
    path.join(ROOT, '.claude'),
  ];
  for (const dir of keyDirs) {
    try {
      if (!fs.existsSync(dir)) continue;
      for (const f of fs.readdirSync(dir)) {
        if (f.startsWith('.session-key-') && f.endsWith(`-${TEST_DB}`)) {
          try { fs.unlinkSync(path.join(dir, f)); } catch { /* best-effort */ }
        }
      }
    } catch { /* best-effort cleanup — never fail the suite over it */ }
  }
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

test('AC-1: ensureSchema is idempotent and seeds exactly 4 read-cursor rows', async () => {
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
  assert.equal(row1.n, 4, 'sync_read_cursor must have exactly 4 rows (one per NAMES entry) immediately after first ensureSchema run');

  const second = runInit();
  assert.equal(second.status, 0, `second init (already-migrated DB) should also exit 0, no duplicate-table error: ${second.stderr}`);
  const [[row2]] = await check.query('SELECT COUNT(*) AS n FROM sync_read_cursor');
  assert.equal(row2.n, 4, 'a second ensureSchema run must not change the row count (false-pass guard: not just "did not crash")');
  const [[permitsRow]] = await check.query('SELECT COUNT(*) AS n FROM sync_permits');
  assert.equal(permitsRow.n, 4, 'sync_permits row count also unchanged by the second run');

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
  const ci = checkin('Bev', sid);
  assert.equal(ci.status, 0, `checkin should succeed: ${ci.stderr}`);
  const msg = run(['message', 'hi'], sid);
  assert.equal(msg.status, 0, `message should succeed: ${msg.stderr}`);
  const [rows] = await db.query('SELECT from_name FROM sync_messages ORDER BY id DESC LIMIT 1');
  assert.equal(rows[0].from_name, 'Bev');
});

// ── AC-3: broadcast vs directed ──────────────────────────────────────────────

test('AC-3a: message with no --to writes to_name IS NULL (broadcast)', async () => {
  const sid = 'ac3-broadcast-session';
  assert.equal(checkin('Bill', sid).status, 0);
  assert.equal(run(['message', 'broadcast text'], sid).status, 0);
  const [rows] = await db.query('SELECT to_name FROM sync_messages ORDER BY id DESC LIMIT 1');
  assert.equal(rows[0].to_name, null);
});

test('AC-3b: message --to Bev writes to_name = Bev', async () => {
  const sid = 'ac3-directed-session';
  assert.equal(checkin('Bill', sid).status, 0);
  assert.equal(run(['message', 'directed text', '--to', 'Bev'], sid).status, 0);
  const [rows] = await db.query('SELECT to_name FROM sync_messages ORDER BY id DESC LIMIT 1');
  assert.equal(rows[0].to_name, 'Bev');
});

// ── AC-4: unknown --to rejected ──────────────────────────────────────────────

test('AC-4: unknown --to target rejected, no partial write, exact reused string', async () => {
  const sid = 'ac4-session';
  assert.equal(checkin('Bill', sid).status, 0);
  const [[before]] = await db.query('SELECT COUNT(*) AS n FROM sync_messages');
  const res = run(['message', 'hi', '--to', 'Nobody'], sid);
  assert.notEqual(res.status, 0);
  assert.match(res.stderr, /Unknown slot name "Nobody"\. Valid: Bill, Ben, Bev, Bro/);
  const [[after]] = await db.query('SELECT COUNT(*) AS n FROM sync_messages');
  assert.equal(after.n, before.n);
});

// ── AC-5: argv parser actually consumes --to's value ────────────────────────

test('AC-5: --to consumes its value; body is not corrupted with the target name', async () => {
  const sid = 'ac5-session';
  assert.equal(checkin('Bill', sid).status, 0);
  assert.equal(run(['message', 'hi', '--to', 'Bev'], sid).status, 0);
  const [rows] = await db.query('SELECT body, to_name FROM sync_messages ORDER BY id DESC LIMIT 1');
  assert.equal(rows[0].body, 'hi', 'body must be exactly "hi", not "hi Bev" or similar concatenation');
  assert.equal(rows[0].to_name, 'Bev');
});

// ── AC-6: --body-file ─────────────────────────────────────────────────────────

test('AC-6a: --body-file reads the body byte-identically, including shell-special chars', async () => {
  const sid = 'ac6-session';
  assert.equal(checkin('Bill', sid).status, 0);
  const tricky = 'line with a `backtick`, a $(subshell) and an embedded "double quote" — verbatim.';
  const tmpFile = path.join(os.tmpdir(), `claude-sync-test-body-${Date.now()}.txt`);
  fs.writeFileSync(tmpFile, tricky, 'utf8');
  try {
    const res = run(['message', '--to', 'Bev', '--body-file', tmpFile], sid);
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
    const res = run(['message', 'inline text', '--to', 'Bev', '--body-file', tmpFile], 'ac6b-session');
    assert.notEqual(res.status, 0);
    const [[after]] = await db.query('SELECT COUNT(*) AS n FROM sync_messages');
    assert.equal(after.n, before.n);
  } finally {
    fs.unlinkSync(tmpFile);
  }
});

// ── AC-7: FEAT-107 delivery split — checkin shows a COUNT, read delivers ───

test('AC-7: checkin shows an UNREAD COUNT (never the body) and does not advance the cursor', async () => {
  const [result] = await db.query(
    "INSERT INTO sync_messages (from_name, to_name, body) VALUES ('Bill', 'Bev', 'hello Bev')"
  );
  const msgId = result.insertId;
  const res = checkin('Bev', 'ac7-session');
  assert.equal(res.status, 0, `checkin should succeed: ${res.stderr}`);
  assert.match(res.stdout, /UNREAD: 1 message\(s\) - run read to receive them\./, 'checkin must show the count line verbatim');
  assert.doesNotMatch(res.stdout, /hello Bev/, 'checkin must NEVER print the message body — that is read\'s job only (FEAT-107)');
  const [rows] = await db.query('SELECT last_read_id FROM sync_read_cursor WHERE name="Bev"');
  assert.equal(Number(rows[0].last_read_id), 0, 'checkin must NOT advance the cursor — only read does (FEAT-107)');
  assert.ok(msgId > 0); // sanity: the message really was inserted
});

test('AC-7 false-pass guard: body absent from checkin output AND a subsequent read still delivers it', async () => {
  await db.query("INSERT INTO sync_messages (from_name, to_name, body) VALUES ('Bill', 'Bev', 'once only please')");
  const sid = 'ac7b-session';

  // A false pass would be a test that only checks "body absent from checkin"
  // without also proving the message is NOT lost — i.e. it must still be
  // retrievable via read. Both halves are asserted here, in one test, so
  // neither can silently regress without failing this test.
  const ci = checkin('Bev', sid);
  assert.match(ci.stdout, /UNREAD: 1 message\(s\)/, 'checkin must report the count');
  assert.doesNotMatch(ci.stdout, /once only please/, 'checkin must not leak the body');

  const first = readCmd(sid);
  assert.equal(first.status, 0, `read should succeed: ${first.stderr}`);
  assert.match(first.stdout, /once only please/, 'read must deliver the body checkin withheld');

  // Re-reading must not re-print the now-delivered message (cursor advanced).
  const second = readCmd(sid);
  assert.doesNotMatch(second.stdout, /once only please/, 'a second read must not re-deliver an already-read message');
});

// ── AC-8: offline identity, delivered to a different window later ──────────

test('AC-8: message to an offline identity persists — checkin (from a different window) shows the count, read delivers the body', async () => {
  // No active Bro permit exists (beforeEach reset everyone to FREE).
  const sid1 = 'ac8-sender-session';
  assert.equal(checkin('Bill', sid1).status, 0);
  assert.equal(run(['message', 'hi Bro, are you there?', '--to', 'Bro'], sid1).status, 0);

  // Checkin via a DIFFERENT, never-before-seen window/session id — shows the
  // count only.
  const sid2 = 'ac8-different-window-session';
  const res = checkin('Bro', sid2);
  assert.equal(res.status, 0, `checkin should succeed: ${res.stderr}`);
  assert.match(res.stdout, /UNREAD: 1 message\(s\)/);
  assert.doesNotMatch(res.stdout, /hi Bro, are you there\?/, 'checkin must not deliver the body even for an offline-at-send-time identity');

  // read (same window) delivers the body.
  const readRes = readCmd(sid2);
  assert.match(readRes.stdout, /hi Bro, are you there\?/);
});

// ── AC-9: sender suppression ─────────────────────────────────────────────────

test('AC-9: sender never sees their own message (count or body); recipient sees both via checkin count + read body', async () => {
  const billSid = 'ac9-bill-session';
  assert.equal(checkin('Bill', billSid).status, 0);
  assert.equal(run(['message', 'note', '--to', 'Bev'], billSid).status, 0);

  // Bill checks in again (renew-of-self path) — must show NO unread count at
  // all for his own just-sent message.
  const billAgain = checkin('Bill', billSid);
  assert.equal(billAgain.status, 0);
  assert.doesNotMatch(billAgain.stdout, /UNREAD:/, 'sender must never see an unread count for their own just-sent message');
  const billRead = readCmd(billSid);
  assert.doesNotMatch(billRead.stdout, /note/, 'sender must never see their own just-sent message body via read either');

  // Control case: Bev's checkin shows the count, and Bev's read shows the body.
  const bevSid = 'ac9-bev-session';
  const bevCi = checkin('Bev', bevSid);
  assert.match(bevCi.stdout, /UNREAD: 1 message\(s\)/, 'the actual recipient must see the count on checkin');
  const bevRead = readCmd(bevSid);
  assert.match(bevRead.stdout, /note/, 'the actual recipient must see the body via read');
});

// ── AC-10: broadcast delivery independent per identity ──────────────────────

test('AC-10: broadcast is delivered independently to each identity (separate cursor rows), via read', async () => {
  const senderSid = 'ac10-sender-session';
  assert.equal(checkin('Bill', senderSid).status, 0);
  assert.equal(run(['message', 'broadcast to everyone'], senderSid).status, 0);

  const bevSid = 'ac10-bev-session';
  const bevCi = checkin('Bev', bevSid);
  assert.match(bevCi.stdout, /UNREAD: 1 message\(s\)/);
  const bevRead = readCmd(bevSid);
  assert.match(bevRead.stdout, /broadcast to everyone/);

  // Bro must STILL receive it — one identity's cursor advancing must not
  // mark it read for another (the single-shared-scalar bug class).
  const broSid = 'ac10-bro-session';
  const broCi = checkin('Bro', broSid);
  assert.match(broCi.stdout, /UNREAD: 1 message\(s\)/, "Bro's independent cursor must still show the broadcast as unread");
  const broRead = readCmd(broSid);
  assert.match(broRead.stdout, /broadcast to everyone/, "Bro's independent cursor must still deliver the broadcast");
});

// ── AC-11: chronological ordering ────────────────────────────────────────────

test('AC-11: multiple unread messages surface oldest-first, via read', async () => {
  const senderSid = 'ac11-sender-session';
  assert.equal(checkin('Bill', senderSid).status, 0);
  assert.equal(run(['message', 'first', '--to', 'Bev'], senderSid).status, 0);
  assert.equal(run(['message', 'second', '--to', 'Bev'], senderSid).status, 0);
  assert.equal(run(['message', 'third', '--to', 'Bev'], senderSid).status, 0);

  const sid = 'ac11-bev-session';
  const bevCi = checkin('Bev', sid);
  assert.match(bevCi.stdout, /UNREAD: 3 message\(s\)/, 'checkin count must reflect all 3 pending messages');
  assert.doesNotMatch(bevCi.stdout, /first|second|third/, 'checkin must not leak any body text');

  const bevRead = readCmd(sid);
  const out = bevRead.stdout;
  const iFirst = out.indexOf('first');
  const iSecond = out.indexOf('second');
  const iThird = out.indexOf('third');
  assert.ok(iFirst >= 0 && iSecond >= 0 && iThird >= 0, 'all three messages must be present in read stdout');
  assert.ok(iFirst < iSecond, '"first" must appear before "second"');
  assert.ok(iSecond < iThird, '"second" must appear before "third"');
});

// ── AC-12: renew --auto never touches messages; only read delivers ─────────

test('AC-12: renew --auto never surfaces or consumes unread messages; a genuine checkin only shows the count, read delivers', async () => {
  const sid = 'ac12-session';
  assert.equal(checkin('Bev', sid).status, 0); // Bev's permit now active with ~5min remaining

  const senderSid = 'ac12-sender-session';
  assert.equal(checkin('Bill', senderSid).status, 0);
  assert.equal(run(['message', 'mid-session arrival', '--to', 'Bev'], senderSid).status, 0);

  const autoRenew = renewAuto(sid); // --session from the per-window key file, as the ping hook does (BUG-354 r4)
  assert.equal(autoRenew.status, 0, `renew --auto should succeed: ${autoRenew.stderr}`);
  assert.equal(autoRenew.stdout.trim(), '', 'renew --auto must stay silent, matching today\'s heartbeat-only behaviour');

  const [rows] = await db.query('SELECT last_read_id FROM sync_read_cursor WHERE name="Bev"');
  assert.equal(Number(rows[0].last_read_id), 0, 'cursor must NOT advance on renew --auto — message still pending');

  // A genuine subsequent checkin (renew-of-self path, permit still active —
  // NOT wake recovery) shows the count only, per FEAT-107; it must NOT
  // deliver the body and must NOT advance the cursor either.
  const genuineCheckin = checkin('Bev', sid);
  assert.match(genuineCheckin.stdout, /UNREAD: 1 message\(s\)/);
  assert.doesNotMatch(genuineCheckin.stdout, /mid-session arrival/, 'checkin must not deliver the body even on a genuine (non-auto) checkin');
  const [rows2] = await db.query('SELECT last_read_id FROM sync_read_cursor WHERE name="Bev"');
  assert.equal(Number(rows2[0].last_read_id), 0, 'checkin must still not advance the cursor');

  // Only read delivers + advances.
  const genuineRead = readCmd(sid);
  assert.match(genuineRead.stdout, /mid-session arrival/);
  const [rows3] = await db.query('SELECT last_read_id FROM sync_read_cursor WHERE name="Bev"');
  assert.ok(Number(rows3[0].last_read_id) > 0, 'read must advance the cursor');
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
  const ci = checkin('Bev', sid);
  assert.equal(ci.status, 0);
  const secret = captureSessionSecret(ci);

  const set = loopSet(secret, '20m /oversight-sweep');
  assert.equal(set.status, 0, `legit loop-set should succeed: ${set.stderr}`);

  const show = loopShow(secret);
  assert.equal(show.status, 0, `legit loop-show should succeed: ${show.stderr}`);
  assert.match(show.stdout, /20m \/oversight-sweep/);

  const clear = loopClear(secret);
  assert.equal(clear.status, 0, `legit loop-clear should succeed: ${clear.stderr}`);

  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Bev"');
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
  const victimCi = checkin('Bro', victimSid);
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
  const victimCi = checkin('Bro', victimSid);
  assert.equal(victimCi.status, 0);
  const realSecret = captureSessionSecret(victimCi);
  assert.equal(loopSet(realSecret, '15m /victim-loop').status, 0);

  // Attacker: bare WINDOW_ID spoof, zero flags, no permit of their own.
  const attackOverwrite = run(['loop-set', '1m /evil-payload'], victimSid);
  assert.notEqual(attackOverwrite.status, 0, 'loop-set overwrite via WINDOW_ID spoofing must be rejected');
  const attackClear = run(['loop-clear'], victimSid);
  assert.notEqual(attackClear.status, 0, 'loop-clear via WINDOW_ID spoofing must be rejected');

  const [rows] = await db.query('SELECT spec FROM sync_loop_config WHERE name="Bro"');
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
  const res = run(['message', '--to', 'Bev', '--body-file'], sid);
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

test('Sanity: normal usage is unaffected — `--to Bev` and `--body-file <path>` still parse and work', async () => {
  const sid = 'sanity-normal-usage';
  checkin('Bill', sid);
  assert.equal(run(['message', 'normal directed message', '--to', 'Bev'], sid).status, 0);

  const tmpFile = path.join(os.tmpdir(), `culvert-sanity-${Date.now()}.txt`);
  fs.writeFileSync(tmpFile, 'normal body-file content', 'utf8');
  try {
    const res = run(['message', '--to', 'Bev', '--body-file', tmpFile], sid);
    assert.equal(res.status, 0, `body-file message should still succeed: ${res.stderr}`);
  } finally {
    fs.unlinkSync(tmpFile);
  }

  const [rows] = await db.query('SELECT to_name, body FROM sync_messages ORDER BY id ASC');
  assert.equal(rows.length, 2);
  assert.equal(rows[0].to_name, 'Bev');
  assert.equal(rows[0].body, 'normal directed message');
  assert.equal(rows[1].to_name, 'Bev');
  assert.equal(rows[1].body, 'normal body-file content');
});

// ── Retire-Bob checkin fix (Aaron, 2026-08-18/19) ───────────────────────────
// Incident: Bob was retired permanently, but the slot still existed, so a
// woken window with a lapsed/mismatched reservation could land on Bob via
// plain first-free-in-NAMES-order, whose stale role text told it to do
// nothing. Second incident: a lead window's reservation kept getting
// resurrected onto a DIFFERENT identity than the one it actually held, via a
// stale sync_window_map row.  These tests cover: NAMES no longer contains
// Bob, --name Bob / message --to Bob are rejected with the retirement
// message (not the generic "Unknown slot name"), no-name checkin prefers a
// window's own mapped identity over blind first-free, ensureSchema seeds
// only current NAMES, and wake recovery fails loudly instead of
// cross-assigning to a different name.

test('NAMES no longer contains Bob; RETIRED lists exactly Bob', () => {
  const sync = require('./claude-sync.js');
  assert.deepEqual(sync.NAMES, ['Bill', 'Ben', 'Bev', 'Bro'], 'NAMES must be exactly the current four slots (Bro added 2026-08-20, Aaron-directed)');
  assert.ok(!sync.NAMES.includes('Bob'), 'Bob must not be a checkin-able slot any more');
  assert.deepEqual(sync.RETIRED, ['Bob']);
  assert.ok(sync.isRetired('bob'), 'isRetired must be case-insensitive');
  assert.ok(!sync.isRetired('Ben'), 'a live slot must never be reported retired');
});

test('checkin --name Bob is rejected with the retirement message, not a generic error, and no permit is granted', async () => {
  const sid = 'retire-bob-checkin';
  const res = run(['checkin', '--name', 'Bob'], sid);
  assert.notEqual(res.status, 0, 'checkin --name Bob must fail');
  assert.match(res.stderr, /Bob is retired \(Aaron, 2026-08-18\)/, 'must be the specific retirement message');
  assert.match(res.stderr, /Bev=lead.*Bill=RM\/BA\/allocator\+oversight.*Ben=coder/,
    'retirement message must state the current team shape');
  assert.doesNotMatch(res.stdout, /YOU ARE:/, 'no identity may be granted on a retired-name request');

  // False-pass guard: prove no row anywhere in sync_permits was touched by
  // the rejected request (not just that this window got no permit).
  const [rows] = await db.query('SELECT window_id FROM sync_permits WHERE window_id=?', [sid]);
  assert.equal(rows.length, 0, 'a rejected retired-name checkin must not have written this window_id onto any slot');
});

test('CLAUDE_IDENTITY=Bob is equally rejected with the retirement message (not just --name)', () => {
  const res = spawnSync(process.execPath, ['claude-sync.js', 'checkin'], {
    cwd: ROOT,
    encoding: 'utf8',
    env: {
      ...process.env,
      METRO_DB_HOST: DB_HOST, METRO_DB_PORT: String(DB_PORT), METRO_DB_USER: DB_USER, METRO_DB_PASSWORD: DB_PASSWORD,
      METRO_DB_NAME: TEST_DB,
      CLAUDE_CODE_SESSION_ID: 'retire-bob-env-identity',
      CLAUDE_SESSION_ID: '',
      CLAUDE_IDENTITY: 'Bob',
    },
  });
  assert.notEqual(res.status, 0);
  assert.match(res.stderr, /Bob is retired \(Aaron, 2026-08-18\)/);
});

test('message --to Bob is rejected with the retirement message, no row written', async () => {
  const sid = 'retire-bob-message';
  assert.equal(checkin('Bill', sid).status, 0);
  const [[before]] = await db.query('SELECT COUNT(*) AS n FROM sync_messages');
  const res = run(['message', 'hi', '--to', 'Bob'], sid);
  assert.notEqual(res.status, 0);
  assert.match(res.stderr, /Bob is retired \(Aaron, 2026-08-18\)/);
  const [[after]] = await db.query('SELECT COUNT(*) AS n FROM sync_messages');
  assert.equal(after.n, before.n, 'no message row may be written for a retired --to target');
});

test('checkout --force Bob is rejected with the retirement message', () => {
  const res = run(['checkout', '--force', 'Bob'], '');
  assert.notEqual(res.status, 0);
  assert.match(res.stderr, /Bob is retired \(Aaron, 2026-08-18\)/);
});

test('status/read never lists Bob as a slot', async () => {
  const res = run(['status'], '');
  assert.equal(res.status, 0);
  assert.doesNotMatch(res.stdout, /^\s*Bob\s/m, 'Bob must not appear as a slot row in status output');
});

// ── No-name checkin prefers the window-mapped identity over first-free ─────

test('no-name checkin prefers this window\'s own mapped identity over blind first-free-in-NAMES-order', async () => {
  const sid = 'winmap-pref-session';
  // A window that previously acquired a permit holds a per-window session
  // secret (minted by acquire, written to its key file). Its permit then
  // lapses and is released wholesale; the persistent sync_window_map still
  // records Bev as this window's identity. Waking and re-checking-in WITHOUT
  // a name but WITH its secret — exactly how the startup hook presents it
  // (BUG-354 r4) — must land on Bev, not blind first-free-in-NAMES-order Bill.
  assert.equal(checkin('Bev', sid).status, 0);
  await db.query('UPDATE sync_permits SET released=1 WHERE name=?', ['Bev']);
  const secret = readSessionSecret(sid);
  assert.ok(secret, 'precondition: the window holds its per-window session secret');
  const res = run(['checkin', '--session', secret], sid);
  assert.equal(res.status, 0, `checkin should succeed: ${res.stderr}`);
  assert.match(res.stdout, /YOU ARE: Bev/, 'must honour the window-mapped identity, not first-free (Bill)');
});

test('false-pass guard: no-name checkin with NO window map falls back to plain first-free (Bill)', async () => {
  const res = run(['checkin'], 'winmap-nopref-session');
  assert.equal(res.status, 0, `checkin should succeed: ${res.stderr}`);
  assert.match(res.stdout, /YOU ARE: Bill/, 'absent a window map, first-free-in-NAMES-order must still be Bill');
});

test('window-mapped preference is skipped when the mapped slot is unavailable — falls back to first-free', async () => {
  const holderSid = 'winmap-holder-session';
  assert.equal(checkin('Bill', holderSid).status, 0); // Bill now ACTIVE, held by a different window

  const sid = 'winmap-unavailable-session';
  await db.query('INSERT INTO sync_window_map (window_id, name, updated_ms) VALUES (?, ?, ?)',
    [sid, 'Bill', Date.now()]);
  const res = run(['checkin'], sid);
  assert.equal(res.status, 0, `checkin should succeed: ${res.stderr}`);
  assert.match(res.stdout, /YOU ARE: Bev/, 'Bill (mapped but live-held elsewhere) must be skipped, and parked Ben excluded, in favour of the next live free slot (Bev)');
});

test('window-mapped preference never resurrects a retired name (defensive — map should never hold Bob, but code must not trust it blindly)', async () => {
  const sid = 'winmap-retired-session';
  await db.query('INSERT INTO sync_window_map (window_id, name, updated_ms) VALUES (?, ?, ?)',
    [sid, 'Bob', Date.now()]);
  const res = run(['checkin'], sid);
  assert.equal(res.status, 0, `checkin should succeed: ${res.stderr}`);
  assert.doesNotMatch(res.stdout, /YOU ARE: Bob/, 'a Bob-mapped window must never be granted the Bob identity');
  assert.match(res.stdout, /YOU ARE: Bill/, 'must fall back to plain first-free instead');
});

// ── ensureSchema seeding: only current NAMES, Bob never (re)seeded ─────────

test('ensureSchema seeds rows only for current NAMES — Bob is never seeded on a fresh DB', async () => {
  const SEED_DB = `${TEST_DB}_seed`;
  const boot = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD });
  await boot.query(`DROP DATABASE IF EXISTS \`${SEED_DB}\``);
  await boot.query(`CREATE DATABASE \`${SEED_DB}\``);
  await boot.end();

  const check = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD, database: SEED_DB });
  const sync = require('./claude-sync.js');
  await sync.ensureSchema(check);
  const [rows] = await check.query('SELECT name FROM sync_permits ORDER BY name');
  const names = rows.map(r => r.name);
  assert.deepEqual(names.slice().sort(), ['Ben', 'Bev', 'Bill', 'Bro'], 'seeding must create exactly the current NAMES');
  assert.ok(!names.includes('Bob'), 'Bob must never be (re)seeded by ensureSchema on a fresh DB');

  await check.end();
  const drop = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD });
  await drop.query(`DROP DATABASE IF EXISTS \`${SEED_DB}\``);
  await drop.end();
});

test('ensureSchema never deletes a pre-existing stale Bob row (operator handles DB cleanup, never code — GR#24)', async () => {
  await db.query("INSERT IGNORE INTO sync_permits (name) VALUES ('Bob')");
  const sync = require('./claude-sync.js');
  await sync.ensureSchema(db); // every CLI command re-runs this — must stay idempotent and non-destructive
  const [rows] = await db.query("SELECT name FROM sync_permits WHERE name='Bob'");
  assert.equal(rows.length, 1, 'a pre-existing Bob row must survive ensureSchema untouched');
  await db.query("DELETE FROM sync_permits WHERE name='Bob'"); // test-only cleanup, not production behaviour
});

// ── Wake recovery: never cross-assign to a different identity ──────────────

test('wake recovery on a genuinely-unavailable previous slot fails loudly and never adopts a different name', async () => {
  const sidA = 'wakefail-A';
  const sidB = 'wakefail-B';
  // Window A originally held Bev (also seeds sync_window_map: sidA -> Bev).
  assert.equal(checkin('Bev', sidA).status, 0);
  // Simulate A's reservation having lapsed past RESERVE_MS: its Bev row goes
  // back to FREE, but sync_window_map's sidA -> Bev mapping is untouched
  // (that table is explicitly designed to survive slot reassignment).
  await db.query("UPDATE sync_permits SET released=1 WHERE name='Bev'");
  // Window B then legitimately takes the now-free Bev slot for itself.
  assert.equal(checkin('Bev', sidB).status, 0);

  // Window A wakes and calls renew — it holds no active permit of its own
  // any more (Ben's row now belongs to window B). BUG-354 r5: the ping hook
  // presents the window's key-file secret, so the test must too — only then
  // does the hadName path get consulted at all.
  const res = run(['renew', '--session', readSessionSecret(sidA)], sidA);
  assert.notEqual(res.status, 0, 'must fail loudly, never silently succeed under a different name');
  assert.doesNotMatch(res.stdout, /YOU ARE:/, 'no identity may be printed as granted');
  assert.doesNotMatch(res.stdout, /IDENTITY CHANGED/, 'the old silent cross-assign message must be gone');
  assert.match(res.stderr, /Your previous slot "Bev" is held/);
  assert.match(res.stderr, /checkin --name Bev/);

  // False-pass guard: window A must not have been silently granted ANY slot
  // (Bill or Bev), which is exactly what the pre-fix cross-assign did.
  const [rows] = await db.query('SELECT name FROM sync_permits WHERE window_id=?', [sidA]);
  assert.equal(rows.length, 0, 'window A must hold no slot at all after the rejected wake recovery');
});

test('wake recovery false-pass guard: previous slot genuinely FREE still reclaims the SAME name', async () => {
  const sid = 'wakeok-session';
  assert.equal(checkin('Bill', sid).status, 0);
  // Simulate idle-past-reservation expiry: row goes FREE, window_map keeps
  // sid -> Bill.
  await db.query("UPDATE sync_permits SET released=1 WHERE name='Bill'");
  // BUG-354 r5: the ping hook renews WITH the key-file secret; without it the
  // renew is a clean no-op, so the test must present it exactly as the hook does.
  const res = run(['renew', '--session', readSessionSecret(sid)], sid);
  assert.equal(res.status, 0, `wake recovery onto a genuinely-free previous slot must still succeed: ${res.stderr}`);
  assert.match(res.stdout, /re-acquired Bill/);
});

test('wake recovery on a stale window_map entry pointing at retired Bob fails loudly with a re-checkin instruction, never adopts another name', async () => {
  const sid = 'wake-retired-map-session';
  await db.query('INSERT INTO sync_window_map (window_id, name, updated_ms) VALUES (?, ?, ?)',
    [sid, 'Bob', Date.now()]);
  // BUG-354 r5: the map is consulted only under key-file proof — write this
  // window's key file and renew with the secret exactly as the ping hook does.
  const secret = 'wake-retired-map-secret';
  fs.writeFileSync(sessionKeyFile(sid), secret, 'utf8');
  const res = run(['renew', '--session', secret], sid);
  assert.notEqual(res.status, 0);
  assert.match(res.stderr, /Your previous slot "Bob" no longer exists/);
  assert.doesNotMatch(res.stdout, /YOU ARE:/);
  const [rows] = await db.query('SELECT name FROM sync_permits WHERE window_id=?', [sid]);
  assert.equal(rows.length, 0, 'no slot may be silently granted when the mapped previous name is retired');
});

// ── BUG-354: identity is operator-enforceable ─────────────────────────────────
// (Aaron, 2026-08-22: "when I tell you that you're Bev you will be Bev?" —
// the honest answer today is NO.) Three defects, all silent, all exit 0.
//   D1  checkin --name <X> is silently ignored when the window holds a permit.
//   D2  wake recovery / first-free / --any can assign a PARKED slot (Ben).
//   D3  windowed checkin rewrites the shared .identity (last-checkin-wins
//       across windows), so another window's prefix hook reads a file this
//       session does not own.
// These tests must FAIL against today's code (pre-fix) — Bev's acceptance:
// "you must SHOW BOTH TESTS FAILING against today's code before the fix."

test('BUG-354 D1a: --name is authoritative — holding a permit, an explicit different --name SWAPS the slot (operator instruction wins)', async () => {
  const sid = 'bug354-d1a-session';
  assert.equal(checkin('Bill', sid).status, 0);
  const res = checkin('Bev', sid); // operator: "you are Bev" while this window holds Bill
  assert.equal(res.status, 0, `--name Bev must succeed when the target is free: ${res.stderr}`);
  assert.match(res.stdout, /YOU ARE: Bev/, '--name must win over the held Bill permit');
  const [rows] = await db.query('SELECT name, released FROM sync_permits WHERE window_id=?', [sid]);
  assert.deepEqual(rows.map(r => [r.name, r.released]).sort(),
    [['Bev', 0], ['Bill', 1]].sort(),
    'window must hold exactly Bev, with the old Bill slot released');
});

test('BUG-354 D1b: --name fails LOUDLY when the target slot is held live by another window (never silently keeps the old identity)', async () => {
  assert.equal(checkin('Bev', 'bug354-d1b-holder').status, 0); // Bev live-held elsewhere
  const sid = 'bug354-d1b-session';
  assert.equal(checkin('Bill', sid).status, 0); // this window holds Bill
  const res = checkin('Bev', sid);              // operator: "you are Bev" — Bev is taken
  assert.notEqual(res.status, 0, 'must fail loudly, never silently renew Bill and exit 0');
  assert.match(res.stderr, /SLOT IS OCCUPIED/, 'clear non-zero rejection naming the unavailable target');
  const [rows] = await db.query('SELECT name, released FROM sync_permits WHERE window_id=?', [sid]);
  assert.deepEqual(rows.map(r => [r.name, r.released]).sort(),
    [['Bill', 0]].sort(),
    'the window must still hold Bill, unchanged, with no identity swap');
});

test('BUG-354 D2a: wake recovery must never re-acquire a PARKED slot (Ben) via sync_window_map — fails loudly', async () => {
  const sid = 'bug354-d2-wake-session';
  // Window's persistent map says it last held Ben (the exact cross-assign
  // mechanism from the live incident — "landed this window in Ben twice");
  // Ben's permit is FREE (beforeEach reset everyone to released).
  await db.query('INSERT INTO sync_window_map (window_id, name, updated_ms) VALUES (?, ?, ?)',
    [sid, 'Ben', Date.now()]);
  // BUG-354 r5: the map is consulted only under key-file proof — write this
  // window's key file and renew with the secret exactly as the ping hook does.
  const secret = 'bug354-d2a-wake-secret';
  fs.writeFileSync(sessionKeyFile(sid), secret, 'utf8');
  const res = run(['renew', '--session', secret], sid);
  assert.notEqual(res.status, 0, 'must fail loudly — a parked slot must never be (re)assigned');
  assert.doesNotMatch(res.stdout, /re-acquired Ben/);
  const [rows] = await db.query('SELECT name FROM sync_permits WHERE window_id=?', [sid]);
  assert.equal(rows.length, 0, 'no slot may be silently granted by wake recovery onto a parked slot');
});

test('BUG-354 D2b: first-free / --any checkin must skip a PARKED slot (Ben) and take the next live free slot', async () => {
  const sid = 'bug354-d2b-session';
  // Occupy Bill and Bev; Ben is FREE but PARKED; Bro is free. First-free in
  // NAMES order would pick Ben unless parked is excluded.
  assert.equal(checkin('Bill', 'bug354-d2b-w1').status, 0);
  assert.equal(checkin('Bev', 'bug354-d2b-w2').status, 0);
  const res = run(['checkin'], sid); // no --name, no CLAUDE_IDENTITY -> first-free
  assert.equal(res.status, 0, `checkin should succeed via a remaining live slot: ${res.stderr}`);
  assert.match(res.stdout, /YOU ARE: Bro/, 'must skip parked Ben and take the next live free slot');
});

test('BUG-354 D2c: explicit checkin --name Ben is rejected as PARKED with a clear message (isRetired-like treatment)', async () => {
  const sid = 'bug354-d2c-session';
  const res = checkin('Ben', sid);
  assert.notEqual(res.status, 0, 'a parked slot must not be occupiable even by explicit --name');
  assert.match(res.stderr, /parked/i, 'rejection must name the parked state');
  const [rows] = await db.query('SELECT name FROM sync_permits WHERE window_id=?', [sid]);
  assert.equal(rows.length, 0, 'no permit may be granted for a parked slot');
});

test('BUG-354 D3: windowed checkin writes the per-window identity marker and must NOT clobber the shared .identity', async () => {
  const sid = 'bug354-d3-session';
  const perWindow = path.join(ROOT, '.claude', `.identity-${sid}`);
  // Seed a sentinel shared .identity so the no-clobber assertion is
  // unconditional — on a clean tree the shared file may not exist, which made
  // the old "if (before !== null)" assertion vacuous (attacker round Aegis).
  const hadShared = fs.existsSync(IDENTITY_FILE);
  const priorShared = hadShared ? fs.readFileSync(IDENTITY_FILE, 'utf8') : null;
  fs.writeFileSync(IDENTITY_FILE, 'sentinel-bug354-d3', 'utf8');
  try {
    assert.equal(checkin('Bev', sid).status, 0);
    assert.equal(fs.readFileSync(perWindow, 'utf8').trim(), 'bev',
      'per-window identity marker (.identity-<window>) must be written on acquire');
    assert.equal(fs.readFileSync(IDENTITY_FILE, 'utf8'), 'sentinel-bug354-d3',
      'shared .identity must be untouched by a windowed acquire (it is cross-window state)');
  } finally {
    if (hadShared) fs.writeFileSync(IDENTITY_FILE, priorShared, 'utf8');
    else fs.rmSync(IDENTITY_FILE, { force: true });
  }
});

// ── BUG-354 round 2 (attacker Cinder) — F1/F2/F3 in the D1 swap path ────────
// Every test below is written against the BUG-354 acceptance bar from
// bev-to-bill.md ("show both tests failing against today's code before the fix
// lands") — each must FAIL on the pre-fix code and PASS on the fixed code.
//   F1 (HIGH)   — checkin accepts the undocumented --session flag; findMine's
//                 session fallback lets a permit-less attacker who knows a
//                 victim's server-issued secret make the D1 swap release the
//                 VICTIM's permit. Fixed: checkin rejects --session outright
//                 (+ the swap refuses to release a row it does not own).
//   F2 (MEDIUM) — the fresh-acquire path takes a new slot WITHOUT releasing
//                 the window's own stale RESERVED row (idle expiry), leaving
//                 one window holding two live rows. Fixed: releaseOtherRows
//                 ForWindow() runs before every acquire in cmdCheckin.
//   F3 (MED-HI) — hadName includes released=1 rows, so after a D1 swap the
//                 released pre-swap slot (still carrying the window id) shadows
//                 sync_window_map and wake recovery reclaims the WRONG identity.
//                 Fixed: hadName excludes released rows.

test('BUG-354 r4 F1: env-spoofed checkin --name cannot evict the victim via the swap (Warden repro #1)', async () => {
  const victimSid = 'bug354-f1-holder';
  // Victim window holds Bev.
  assert.equal(checkin('Bev', victimSid).status, 0);
  const [before] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(before[0].released, 0, 'precondition: victim holds Bev live');
  // Attacker: no permit of their own, sets CLAUDE_CODE_SESSION_ID to the
  // victim's EXACT value, requests Bill. r2's F1 blocked this by rejecting
  // --session outright; r4 (Warden round 3) removes WINDOW_ID as identity
  // authority, so the swap path must not even resolve the victim's row as
  // "mine" without the server-issued secret. Env-spoofing alone grants zero.
  const res = run(['checkin', '--name', 'Bill'], victimSid);
  const [after] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(after[0].released, 0, 'victim permit must remain live — the swap must not have released it');
  assert.equal(after[0].session_id, before[0].session_id, 'victim secret must be untouched');
});

test('BUG-354 F2: acquiring a new slot must first release any stale non-FREE row still belonging to this window (RESERVED own-slot leak)', async () => {
  const sid = 'bug354-f2-session';
  assert.equal(checkin('Bill', sid).status, 0);
  // Simulate idle expiry: Bill is now expired-but-within-reserve for this window.
  await db.query('UPDATE sync_permits SET expires_ms=?, heartbeat_ms=? WHERE name=?',
    [Date.now() - 1000, Date.now() - 1000, 'Bill']);
  const [b] = await db.query('SELECT expires_ms, window_id, released FROM sync_permits WHERE name=?', ['Bill']);
  assert.equal(b[0].window_id, sid, 'precondition: Bill still belongs to this window');
  assert.equal(b[0].released, 0, 'precondition: Bill is not yet released');
  // The window re-checks-in on a DIFFERENT free slot while Bill is reserved.
  const res = checkin('Bev', sid);
  assert.equal(res.status, 0, `Bev is free and must be acquirable: ${res.stderr}`);
  assert.match(res.stdout, /YOU ARE: Bev/);
  const [rows] = await db.query('SELECT name, released FROM sync_permits WHERE window_id=?', [sid]);
  assert.deepEqual(rows.map(r => [r.name, r.released]).sort(),
    [['Bev', 0], ['Bill', 1]],
    'the stale reserved Bill row must be released, not left occupying the reserve window');
});

test('BUG-354 F3: wake recovery after a D1 swap reclaims the LAST-HELD identity (swap target), never the pre-swap slot left released', async () => {
  const sid = 'bug354-f3-session';
  assert.equal(checkin('Bill', sid).status, 0);
  const swap = checkin('Bev', sid); // D1 swap Bill -> Bev (operator --name)
  assert.equal(swap.status, 0, `swap must succeed: ${swap.stderr}`);
  assert.match(swap.stdout, /YOU ARE: Bev/);
  // Simulate the window going fully idle: its Bev permit lapses and is released
  // wholesale, so neither `mine` nor the stale-RESERVED path fires — but the
  // released Bill row (still carrying this window's id) shadows hadName unless
  // released rows are excluded. sync_window_map still records Bev.
  await db.query('UPDATE sync_permits SET released=1 WHERE name=?', ['Bev']);
  const [map] = await db.query('SELECT name FROM sync_window_map WHERE window_id=?', [sid]);
  assert.equal(map[0].name, 'Bev', 'precondition: persistent map records Bev as the last-held identity');
  // BUG-354 r5: the ping hook renews WITH the key-file secret; without it the
  // renew is a clean no-op. readSessionSecret returns the post-swap secret the
  // acquire wrote into this window's key file.
  const res = run(['renew', '--session', readSessionSecret(sid)], sid);
  assert.equal(res.status, 0, `wake recovery must succeed: ${res.stderr}`);
  assert.match(res.stdout, /re-acquired Bev/, 'wake recovery must reclaim the swap target Bev, never the pre-swap Bill');
  // The window must hold exactly ONE active slot: Bev. (The released Bill row
  // retains its window_id by design — released rows are filtered by hadName,
  // never scrubbed — so assert on the live rows only.)
  const [rows] = await db.query('SELECT name FROM sync_permits WHERE window_id=? AND released=0', [sid]);
  assert.deepEqual(rows.map(r => r.name).sort(), ['Bev'],
    'the window must end holding exactly Bev');
});

test('BUG-354 r4 F1-extended: env-spoofed checkout cannot release the victim\'s permit (Warden repro #2)', async () => {
  const holderSid = 'bug354-f1co-holder';
  assert.equal(checkin('Bev', holderSid).status, 0); // victim window holds Bev
  const [before] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bev']);
  // Attacker: no permit, env set to the victim's exact value, bare checkout.
  // r2's F1-extended guard (mine.row.window_id !== WINDOW_ID) is vacuous when
  // WINDOW_ID is the spoofed env value; r4 resolves checkout identity by the
  // server-issued secret only, so a secret-less attacker must not release it.
  const res = run(['checkout'], holderSid);
  const [after] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(after[0].released, 0, 'victim permit must stay live');
  assert.equal(after[0].session_id, before[0].session_id, 'victim secret must be untouched');
});

test('BUG-354 r4 W-3: env-spoofed renew cannot wake-recover the victim\'s expired RESERVED row (Warden repro #3)', async () => {
  const victimSid = 'bug354-r4-w3-victim';
  assert.equal(checkin('Bev', victimSid).status, 0);
  // Idle expiry: Bev is now expired-but-within-reserve for the victim's window.
  await db.query('UPDATE sync_permits SET expires_ms=?, heartbeat_ms=? WHERE name=?',
    [Date.now() - 1000, Date.now() - 1000, 'Bev']);
  const [before] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bev']);
  assert.ok(before[0].session_id, 'precondition: the reserved row carries the victim\'s server-issued secret');
  // Attacker: env = victim's value, no --session. The r2 code's allowStale
  // window-id match would wake-recover the victim's row and mint a NEW secret
  // under attacker control — full identity theft. r4 must not.
  const res = run(['renew', '--auto'], victimSid);
  const [after] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(after[0].session_id, before[0].session_id,
    'wake recovery must NOT re-mint the victim\'s permit secret for an env-spoofing attacker');
  assert.equal(after[0].released, 0, 'the victim\'s reserved row must remain exactly as it was');
});

test('BUG-354 r4 W-4: env-spoofed --any cannot renew/hijack the victim\'s ACTIVE permit (Warden repro #4)', async () => {
  const victimSid = 'bug354-r4-w4-victim';
  assert.equal(checkin('Bev', victimSid).status, 0);
  const [before] = await db.query('SELECT session_id, expires_ms, heartbeat_ms FROM sync_permits WHERE name=?', ['Bev']);
  // Attacker: env = victim's value, no --session, --any. The r2 code resolves
  // the victim's ACTIVE row as "mine" and silently renews it (the attacker now
  // believes it is Bev and holds her lease). r4 must not touch her row at all.
  const res = run(['checkin', '--any'], victimSid);
  const [after] = await db.query('SELECT session_id, expires_ms, heartbeat_ms FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(after[0].session_id, before[0].session_id, 'victim secret must be untouched');
  assert.equal(Number(after[0].expires_ms), Number(before[0].expires_ms), 'victim permit must not have been renewed');
  assert.equal(Number(after[0].heartbeat_ms), Number(before[0].heartbeat_ms), 'victim heartbeat must not have been touched');
});

test('BUG-354 r4 W-5: checkin with a --session matching NO permit cannot evict anyone (secret must prove ownership)', async () => {
  const victimSid = 'bug354-r4-w5-victim';
  assert.equal(checkin('Bev', victimSid).status, 0);
  const [before] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bev']);
  // Attacker presents a made-up secret (matches no permit) AND the victim's env
  // id. The secret — not the env — is the identity authority, so this must be a
  // plain fresh acquire that leaves the victim untouched.
  const res = run(['checkin', '--name', 'Bill', '--session', 'r4-w5-nonexistent-secret'], victimSid);
  const [after] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(after[0].released, 0, 'victim permit must remain live');
  assert.equal(after[0].session_id, before[0].session_id, 'victim secret must be untouched');
});

test('BUG-354 r5 W-6 (r4 F2): an env-spoofed fresh acquire is REFUSED while a live row claims the window — no attacker mint, no hook-renew redirect', async () => {
  const victimSid = 'bug354-r5-w6-victim';
  assert.equal(checkin('Bev', victimSid).status, 0);
  const keyFile = sessionKeyFile(victimSid);
  const victimKey = fs.readFileSync(keyFile, 'utf8').trim();
  assert.ok(victimKey, 'precondition: the victim\'s per-window key file holds the server secret');
  // Attacker: env = the victim's window id, no --session, claims a FREE slot.
  // r4 let this mint an attacker row beside the victim's live one (the W-6 guard
  // only refused to CLOBBER the key file; deleting the key file first then minted
  // the attacker's secret into the victim's path — r4 REJECT F2). r5 refuses the
  // acquire outright: ONE live row per window id.
  const res = run(['checkin', '--name', 'Bill'], victimSid);
  assert.notEqual(res.status, 0, 'the acquire must be REFUSED — the attacker cannot mint a row beside the victim\'s live one');
  const afterKey = fs.existsSync(keyFile) ? fs.readFileSync(keyFile, 'utf8').trim() : '';
  assert.equal(afterKey, victimKey, 'the victim\'s key file must survive untouched');
  const [bev] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(bev[0].released, 0, 'the victim\'s permit must remain live');
  assert.equal(bev[0].window_id, victimSid, 'the victim\'s window claim must be intact');
  const [bill] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bill']);
  assert.equal(bill[0].released, 1, 'the attacker must have minted NO row');
  assert.equal(bill[0].session_id, null, 'Bill must carry no attacker secret');
});

// BUG-354 r7: the r5 F1 test below was REPLACED by H2. Its scenario — an
// attacker-constructed ghost ACTIVE row under the victim's window, then a gated
// swap — was exactly round 6's H2 ghost-shadowing hole: the NAMES-first claim
// scan saw the ghost (proven by the attacker's own secret) and missed the
// victim's real Bev, so the swap minted a second live row. r7 refuses ANY
// unprovable live row, so the old "swap must run" assertion became the
// vulnerability itself and is inverted below. The r5 F1 session-scoped-release
// semantic survives in code (releaseOtherRowsForWindow still refuses ACTIVE
// rows) but is no longer reachable through this shape — F2 blocks the ghost at
// the door before any release runs.
test('BUG-354 r7 H2: a ghost ACTIVE row under the victim\'s window cannot be leveraged to mint a 2nd live row via the gated swap — ANY unprovable live row refuses, not just the NAMES-first', async () => {
  const sid = 'bug354-r7-h2-window';
  assert.equal(checkin('Bev', sid).status, 0); // victim holds Bev live under this window
  const [victimBefore] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  // Construct the attacker's ghost ACTIVE row under the SAME window id with a
  // secret the attacker controls. Bill sorts before Bev in NAMES order, so a
  // first-only scan sees the ghost as the (provable) claim and hides Bev.
  const bootId = String(Math.round((Date.now() - os.uptime() * 1000) / 10000));
  const attSecret = 'r7-h2-attacker-secret';
  await db.query(
    `UPDATE sync_permits SET session_id=?, window_id=?, acquired_ms=?, expires_ms=?, heartbeat_ms=?, boot_id=?, released=0 WHERE name=?`,
    [attSecret, sid, Date.now(), Date.now() + 300000, Date.now(), bootId, 'Bill']);
  const res = run(['checkin', '--name', 'Bro', '--session', attSecret], sid);
  assert.notEqual(res.status, 0, `the swap must be REFUSED — Bev's live row is unprovable to this caller even though the ghost Bill is provable: ${res.stdout}${res.stderr}`);
  const [bro] = await db.query('SELECT released, session_id FROM sync_permits WHERE name=?', ['Bro']);
  assert.equal(bro[0].released, 1, 'Bro must NOT be minted');
  assert.equal(bro[0].session_id, null, 'Bro must carry no attacker secret');
  const [bev] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(bev[0].released, 0, 'the victim\'s live row must remain live');
  assert.equal(bev[0].session_id, victimBefore[0].session_id, 'the victim\'s secret must be untouched');
  assert.equal(bev[0].window_id, sid, 'the victim\'s window claim must be intact');
  const [bill] = await db.query('SELECT released, session_id, window_id FROM sync_permits WHERE name=?', ['Bill']);
  assert.equal(bill[0].released, 0, 'the refused swap must leave the ghost row untouched');
  assert.equal(bill[0].session_id, attSecret, 'the ghost row must keep its secret');
  assert.equal(bill[0].window_id, sid, 'the ghost row must not have moved windows');
});

test('BUG-354 r7 H1: --force --human-ok (a plain flag with no human proof) cannot mint a ghost row beside a live claim this caller cannot secret-prove', async () => {
  const victimSid = 'bug354-r7-h1-victim';
  const broSid = 'bug354-r7-h1-bro';
  assert.equal(checkin('Bev', victimSid).status, 0); // victim live under victimSid
  assert.equal(checkin('Bro', broSid).status, 0); // Bro ACTIVE under ITS OWN window
  const broBefore = (await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bro']))[0][0];
  // Attacker spoofs the victim's window id and force-evicts Bro with NO secret
  // at all. r6 left the four --force --human-ok acquire branches unguarded, so
  // this minted Bro live beside the victim's Bev. r7 gates every force acquire:
  // the window already carries Bev, which this secret-less caller cannot prove.
  const res = run(['checkin', '--name', 'Bro', '--force', '--human-ok'], victimSid);
  assert.notEqual(res.status, 0, `the force acquire must be REFUSED — no second live row may be minted beside the victim's Bev: ${res.stdout}${res.stderr}`);
  const [broAfter] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bro']);
  assert.equal(broAfter[0].released, 0, 'the real Bro holder must NOT be evicted');
  assert.equal(broAfter[0].session_id, broBefore.session_id, 'the real Bro secret must be untouched');
  assert.equal(broAfter[0].window_id, broSid, 'the real Bro must stay in its own window');
  const [bev] = await db.query('SELECT released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(bev[0].released, 0, 'the victim\'s permit must remain live');
  assert.equal(bev[0].window_id, victimSid, 'the victim\'s window claim must be intact');
});

test('BUG-354 r7 H6: renew wake-recovery from a spoofed window id cannot re-acquire the caller\'s own lapsed row live under the VICTIM\'s window', async () => {
  const victimSid = 'bug354-r7-h6-victim';
  const attSid = 'bug354-r7-h6-attacker';
  assert.equal(checkin('Bev', victimSid).status, 0); // victim live under victimSid
  assert.equal(checkin('Bill', attSid).status, 0); // attacker holds Bill under its own window
  const attSecret = readSessionSecret(attSid);
  assert.ok(attSecret, 'precondition: the attacker\'s Bill key file holds a real server-issued secret');
  // Let the attacker's permit lapse -> Bill becomes RESERVED (its own window's
  // idle-leftover shape, secret known from its key file).
  await db.query('UPDATE sync_permits SET expires_ms=? WHERE name=?', [Date.now() - 60000, 'Bill']);
  // Attacker spoofs the VICTIM's window id and renews with its OWN Bill secret.
  // r6 left the stale-permit wake-recovery acquire unguarded, so this
  // re-acquired Bill live under victimSid, beside the victim's Bev. r7 gates it.
  const res = run(['renew', '--auto', '--session', attSecret], victimSid);
  assert.notEqual(res.status, 0, `wake recovery must be REFUSED — Bill cannot be re-acquired live under the victim's window: ${res.stdout}${res.stderr}`);
  const [bill] = await db.query('SELECT released, session_id, window_id FROM sync_permits WHERE name=?', ['Bill']);
  assert.equal(bill[0].released, 0, 'Bill must stay released=0 (still RESERVED to its own window)');
  assert.equal(bill[0].session_id, attSecret, 'Bill must keep the server-issued secret');
  assert.equal(bill[0].window_id, attSid, 'Bill must NOT have been moved to the victim\'s window');
  const [bev] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(bev[0].released, 0, 'the victim\'s permit must remain live');
  assert.equal(bev[0].window_id, victimSid, 'the victim\'s window claim must be intact');
});

// BUG-354 r8 (r7 REJECT, attacker ac9ff2cc): the F2 gate keys to the AMBIENT
// window, but force-evict/checkout-force target a row BY NAME — so a fresh (or
// no) window id has zero live claims under it, the F2 gate passes vacuously,
// and a no-secret rogue process evicts any identity. r8 requires force surfaces
// to authenticate the TARGET row's server-issued session secret (the identity
// authority). Each P-break is a distinct window-shape the r7 code failed:
//   P1 fresh window + no secret -> checkin --name Bev --force --human-ok
//   P2 fresh window + no secret -> force-evict a third party (Bro)
//   P3 any window + no secret   -> checkout --force Bev
//   P4 own-secret + fresh window -> force-evict the victim's own row
//   P8 fresh window + no secret -> force-override a RESERVED row
//   P11 NO window id + no secret -> force-evict the victim's own row
test('BUG-354 r8 P1: a fresh-window process with NO secret cannot force-evict a live holder (checkin --name X --force --human-ok)', async () => {
  const victimSid = 'bug354-r8-p1-victim';
  const freshSid = 'bug354-r8-p1-fresh';
  assert.equal(checkin('Bev', victimSid).status, 0); // victim live under victimSid
  const [before] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  // Fresh window, NO --session, NO key file — the r7 F2 gate sees no claims
  // under the fresh window and passes vacuously, so the force acquire proceeds.
  const res = run(['checkin', '--name', 'Bev', '--force', '--human-ok'], freshSid);
  assert.notEqual(res.status, 0, `force-evict without the target's secret must be REFUSED: ${res.stdout}${res.stderr}`);
  const [after] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(after[0].released, 0, 'the victim must NOT be evicted');
  assert.equal(after[0].session_id, before[0].session_id, 'the victim\'s secret must be untouched');
  assert.equal(after[0].window_id, victimSid, 'the victim\'s window claim must be intact');
});

test('BUG-354 r8 P2: a fresh-window process with NO secret cannot force-evict a THIRD-PARTY holder by name', async () => {
  const victimSid = 'bug354-r8-p2-victim';
  const broSid = 'bug354-r8-p2-bro';
  const freshSid = 'bug354-r8-p2-fresh';
  assert.equal(checkin('Bev', victimSid).status, 0); // victim live
  assert.equal(checkin('Bro', broSid).status, 0); // third-party Bro live under its OWN window
  const [before] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bro']);
  const res = run(['checkin', '--name', 'Bro', '--force', '--human-ok'], freshSid);
  assert.notEqual(res.status, 0, `force-evict of a third party without the target's secret must be REFUSED: ${res.stdout}${res.stderr}`);
  const [after] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bro']);
  assert.equal(after[0].released, 0, 'Bro must NOT be evicted');
  assert.equal(after[0].session_id, before[0].session_id, 'Bro\'s secret must be untouched');
  assert.equal(after[0].window_id, broSid, 'Bro must stay in its own window');
});

test('BUG-354 r8 P3: checkout --force with NO secret cannot release a live permit', async () => {
  const victimSid = 'bug354-r8-p3-victim';
  assert.equal(checkin('Bev', victimSid).status, 0);
  const [before] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  const res = run(['checkout', '--force', 'Bev'], 'bug354-r8-p3-any');
  assert.notEqual(res.status, 0, `checkout --force without the target's secret must be REFUSED: ${res.stdout}${res.stderr}`);
  const [after] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(after[0].released, 0, 'the victim must NOT be released');
  assert.equal(after[0].session_id, before[0].session_id, 'the victim\'s secret must be untouched');
  assert.equal(after[0].window_id, victimSid, 'the victim\'s window claim must be intact');
});

test('BUG-354 r8 P4: a caller presenting its OWN live secret from a fresh window cannot force-evict the victim\'s row', async () => {
  const victimSid = 'bug354-r8-p4-victim';
  const attackerSid = 'bug354-r8-p4-attacker';
  const freshSid = 'bug354-r8-p4-fresh';
  assert.equal(checkin('Bev', victimSid).status, 0); // victim live
  assert.equal(checkin('Bill', attackerSid).status, 0); // attacker holds Bill under ITS OWN window
  const attSecret = readSessionSecret(attackerSid);
  assert.ok(attSecret, 'precondition: the attacker\'s Bill key file holds a real server-issued secret');
  const [before] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  // Attacker has its OWN live secret but NOT the target's. From a fresh window
  // the F2 gate sees no claims under it, so the r7 swap-force proceeds.
  const res = run(['checkin', '--name', 'Bev', '--force', '--human-ok', '--session', attSecret], freshSid);
  assert.notEqual(res.status, 0, `force-evict with the WRONG (own) secret must be REFUSED: ${res.stdout}${res.stderr}`);
  const [after] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(after[0].released, 0, 'the victim must NOT be evicted');
  assert.equal(after[0].session_id, before[0].session_id, 'the victim\'s secret must be untouched');
  assert.equal(after[0].window_id, victimSid, 'the victim\'s window claim must be intact');
});

test('BUG-354 r8 P8: a fresh-window process with NO secret cannot force-override a RESERVED row', async () => {
  const victimSid = 'bug354-r8-p8-victim';
  const freshSid = 'bug354-r8-p8-fresh';
  assert.equal(checkin('Bev', victimSid).status, 0);
  // Victim goes idle -> Bev becomes RESERVED (its own window's stale shape).
  await db.query('UPDATE sync_permits SET expires_ms=? WHERE name=?', [Date.now() - 60000, 'Bev']);
  const [before] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  const res = run(['checkin', '--name', 'Bev', '--force', '--human-ok'], freshSid);
  assert.notEqual(res.status, 0, `force-override of a RESERVED row without the target's secret must be REFUSED: ${res.stdout}${res.stderr}`);
  const [after] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(after[0].released, 0, 'the reserved row must NOT be evicted');
  assert.equal(after[0].session_id, before[0].session_id, 'the reserved row\'s secret must be untouched');
  assert.equal(after[0].window_id, victimSid, 'the reserved row must stay in its own window');
});

test('BUG-354 r8 P11: a process with NO window id and NO secret cannot force-evict the victim\'s row', async () => {
  const victimSid = 'bug354-r8-p11-victim';
  assert.equal(checkin('Bev', victimSid).status, 0);
  const [before] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  // run(..., '') sets CLAUDE_CODE_SESSION_ID to the empty string — NO window id
  // at all. liveWindowClaims is skipped entirely (`if (WINDOW_ID)`), F2 passes.
  const res = run(['checkin', '--name', 'Bev', '--force', '--human-ok'], '');
  assert.notEqual(res.status, 0, `force-evict with NO window id and NO secret must be REFUSED: ${res.stdout}${res.stderr}`);
  const [after] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(after[0].released, 0, 'the victim must NOT be evicted');
  assert.equal(after[0].session_id, before[0].session_id, 'the victim\'s secret must be untouched');
  assert.equal(after[0].window_id, victimSid, 'the victim\'s window claim must be intact');
});

test('BUG-354 r8 CONTROL: checkout --force WITH the target\'s server-issued secret still releases it (legit operator path preserved)', async () => {
  const victimSid = 'bug354-r8-ctrl-victim';
  assert.equal(checkin('Bev', victimSid).status, 0);
  const bevSecret = readSessionSecret(victimSid);
  assert.ok(bevSecret, 'precondition: the victim key file holds a real server-issued secret');
  const res = run(['checkout', '--force', 'Bev', '--session', bevSecret], 'bug354-r8-ctrl-op');
  assert.equal(res.status, 0, `checkout --force with the target's secret must succeed (operator recovery): ${res.stdout}${res.stderr}`);
  const [after] = await db.query('SELECT released FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(after[0].released, 1, 'the target permit must be released');
});

test('BUG-354 r5 F3: session-key files live in the per-user .claude/session-keys dir, NEVER the shared checkout', async () => {
  const sid = 'bug354-r5-f3-window';
  assert.equal(checkin('Bev', sid).status, 0);
  const checkoutPath = path.join(ROOT, '.claude', `.session-key-${sid}-${TEST_DB}`);
  assert.ok(!fs.existsSync(checkoutPath), 'no session-key file may be written to the shared checkout');
  const perUser = sessionKeyFile(sid);
  assert.ok(fs.existsSync(perUser), 'the per-user session-key file must exist');
  const secret = fs.readFileSync(perUser, 'utf8').trim();
  assert.ok(secret, 'the per-user key file must hold a server-issued secret');
  const [bev] = await db.query('SELECT session_id FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(secret, bev[0].session_id, 'the key file must hold exactly this permit\'s secret');
});

test('BUG-354 r5 F4: renew wake-recovery requires the session secret — ambient WINDOW_ID alone cannot re-acquire a released slot', async () => {
  const sid = 'bug354-r5-f4-window';
  assert.equal(checkin('Bev', sid).status, 0);
  const [before] = await db.query('SELECT session_id FROM sync_permits WHERE name=?', ['Bev']);
  // Victim goes fully idle: row released wholesale (beyond reserve), so neither
  // `mine` nor the stale-RESERVED path can fire — only the ambient hadName path
  // could re-acquire, and it must not without the secret (r4 REJECT F4).
  await db.query('UPDATE sync_permits SET released=1, expires_ms=? WHERE name=?', [Date.now() - 60000, 'Bev']);
  const res = run(['renew', '--auto'], sid);
  const [after] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(after[0].session_id, before[0].session_id,
    'wake recovery must NOT mint a fresh secret under the victim\'s name for an env-spoofer');
  assert.equal(after[0].released, 1, 'the released row must stay released (no silent re-acquire)');
});

test('BUG-354 r6 R1a (r5 P1): the swap acquire is F2-guarded — an attacker holding a secret-proven row in a DIFFERENT window cannot mint a 2nd live row under the victim\'s window', async () => {
  const victimSid = 'bug354-r6-swap-victim';
  const attackerSid = 'bug354-r6-swap-attacker';
  assert.equal(checkin('Bev', victimSid).status, 0); // victim live under victimSid
  assert.equal(checkin('Bill', attackerSid).status, 0); // attacker holds Bill under ITS OWN window
  const attSecret = readSessionSecret(attackerSid);
  assert.ok(attSecret, 'precondition: the attacker\'s Bill key file holds a real server-issued secret');
  const [victimBefore] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  // Attacker spoofs the VICTIM's window id and swaps its own Bill -> Bro. The
  // swap acquire stamps Bro live under victimSid WITHOUT consulting the
  // live-claim guard in r5 (fix bar: apply liveWindowClaimRefusal at the swap
  // acquire site). With the guard, the window already carries Bev (a live row
  // this caller cannot secret-prove) -> the swap must be REFUSED.
  const res = run(['checkin', '--name', 'Bro', '--session', attSecret], victimSid);
  assert.notEqual(res.status, 0, `the swap must be REFUSED under F2 — the attacker cannot mint Bro beside the victim's live Bev: ${res.stdout}${res.stderr}`);
  const [bro] = await db.query('SELECT released, session_id FROM sync_permits WHERE name=?', ['Bro']);
  assert.equal(bro[0].released, 1, 'Bro must NOT be acquired');
  assert.equal(bro[0].session_id, null, 'Bro must carry no attacker secret');
  const [bevAfter] = await db.query('SELECT session_id, released, window_id FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(bevAfter[0].released, 0, 'the victim must remain live');
  assert.equal(bevAfter[0].session_id, victimBefore[0].session_id, 'the victim\'s secret must be untouched');
  assert.equal(bevAfter[0].window_id, victimSid, 'the victim\'s window claim must be intact');
  const [bill] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bill']);
  assert.equal(bill[0].released, 0, 'the attacker\'s own Bill row must be untouched');
  assert.equal(bill[0].session_id, attSecret, 'the attacker\'s own secret must be intact');
});

test('BUG-354 r6 R1b (r5 P1): renew wake-recovery hadName acquire is F2-guarded — a released row under the victim\'s window cannot re-acquire a name while the victim holds the window live', async () => {
  const sid = 'bug354-r6-hadname-window';
  assert.equal(checkin('Bev', sid).status, 0); // victim live under sid
  const [victimBefore] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bev']);
  // Attacker constructs a RELEASED Bill row under the victim's window with a
  // secret it controls (the shape a pre-r5 ghost-row leak would leave). The
  // renew hadName path would re-acquire Bill (state FREE) WITHOUT the F2 guard
  // (fix bar: apply liveWindowClaimRefusal at the renew hadName acquire site).
  const attSecret = 'r6-hadname-attacker-secret';
  const bootId = String(Math.round((Date.now() - os.uptime() * 1000) / 10000));
  await db.query(
    `UPDATE sync_permits SET session_id=?, window_id=?, acquired_ms=?, expires_ms=?, heartbeat_ms=?, boot_id=?, released=1 WHERE name=?`,
    [attSecret, sid, Date.now() - 120000, Date.now() - 60000, Date.now() - 120000, bootId, 'Bill']);
  const res = run(['renew', '--auto', '--session', attSecret], sid);
  assert.notEqual(res.status, 0, `the hadName wake-recovery must be REFUSED under F2 — the window already holds the victim's live Bev: ${res.stdout}${res.stderr}`);
  const [bill] = await db.query('SELECT released, session_id FROM sync_permits WHERE name=?', ['Bill']);
  assert.equal(bill[0].released, 1, 'Bill must stay released — no wake-recovery mint beside the victim\'s live row');
  assert.equal(bill[0].session_id, attSecret, 'Bill must keep the attacker\'s secret, not a fresh server mint');
  const [bev] = await db.query('SELECT session_id, released FROM sync_permits WHERE name=?', ['Bev']);
  assert.equal(bev[0].released, 0, 'the victim\'s permit must remain live');
  assert.equal(bev[0].session_id, victimBefore[0].session_id, 'the victim\'s secret must be untouched');
});

test('BUG-354 r6 R2 (r5 P1): fresh machine with NO session-keys dir does not self-lockout — acquire and ensureSessionKey mkdir the per-user dir', async () => {
  const freshHome = fs.mkdtempSync(path.join(os.tmpdir(), 'sync-fresh-home-'));
  const sid = 'bug354-r6-fresh-home';
  try {
    // Point os.homedir() at a brand-new home: USERPROFILE is Node's win32
    // homedir source, and a fresh home has NO .claude/session-keys. r5 found
    // acquire/ensureSessionKey writeFileSync without mkdirSync -> ENOENT was
    // swallowed by the convenience catch -> first checkin on a fresh machine
    // silently produced NO key file -> the window could never renew (GR#17
    // silent-failure family). After the fix, the dir is created.
    const env = {
      ...process.env,
      USERPROFILE: freshHome,
      METRO_DB_HOST: DB_HOST,
      METRO_DB_PORT: String(DB_PORT),
      METRO_DB_USER: DB_USER,
      METRO_DB_PASSWORD: DB_PASSWORD,
      METRO_DB_NAME: TEST_DB,
      CLAUDE_CODE_SESSION_ID: sid,
      CLAUDE_SESSION_ID: '',
      CLAUDE_IDENTITY: '',
    };
    const res = spawnSync(process.execPath, ['claude-sync.js', 'checkin', '--name', 'Bev'], { cwd: ROOT, encoding: 'utf8', env });
    assert.equal(res.status, 0, `fresh-machine checkin must succeed: ${res.stderr}`);
    const keyFile = path.join(freshHome, '.claude', 'session-keys', `.session-key-${sid}-${TEST_DB}`);
    assert.ok(fs.existsSync(keyFile),
      'acquire must mkdir the per-user session-keys dir and write the key file on a fresh machine (r5 R2: silent ENOENT -> no key -> self-lockout)');
    const secret = fs.readFileSync(keyFile, 'utf8').trim();
    assert.ok(secret, 'the fresh-machine key file must hold a server-issued secret');
    // ensureSessionKey path: wipe the key dir and force a REAL renewal (expire
    // the permit near-now — renew --auto with plenty of TTL takes the
    // heartbeat-only fast path, which never touches the key file). The renewal
    // path calls ensureSessionKey, which must mkdir the dir and rewrite the key.
    fs.rmSync(path.join(freshHome, '.claude'), { recursive: true, force: true });
    await db.query('UPDATE sync_permits SET expires_ms=? WHERE name=?', [Date.now() + 1000, 'Bev']);
    const res2 = spawnSync(process.execPath, ['claude-sync.js', 'renew', '--auto', '--session', secret], { cwd: ROOT, encoding: 'utf8', env });
    assert.equal(res2.status, 0, `renew --auto after a wiped key dir must succeed: ${res2.stderr}`);
    assert.ok(fs.existsSync(keyFile), 'ensureSessionKey must mkdir the dir and rewrite the key on a real renewal (r5 R2)');
  } finally {
    try { fs.rmSync(freshHome, { recursive: true, force: true }); } catch { /* best-effort cleanup */ }
  }
});

test('BUG-354 r6 R3 (r5 P2 = BUG-360): findMine window-id fallback requires key-file possession — a key-less env-spoofer cannot read the victim\'s messages or advance their cursor', async () => {
  const sid = 'bug354-r6-bug360-window';
  assert.equal(checkin('Bev', sid).status, 0); // victim live under sid; key file written
  const victimSecret = readSessionSecret(sid);
  assert.ok(victimSecret, 'precondition: the victim\'s key file holds the server secret');
  // A message TO the victim from a permitted sender, so there is something to steal.
  const senderSid = 'bug354-r6-bug360-sender';
  assert.equal(checkin('Bill', senderSid).status, 0);
  const send = run(['message', 'BUG-360-TOP-SECRET', '--to', 'Bev'], senderSid);
  assert.equal(send.status, 0, `message send must succeed: ${send.stderr}`);
  const [cursorBefore] = await db.query('SELECT last_read_id FROM sync_read_cursor WHERE name=?', ['Bev']);
  // Attacker: knows the victim's window id but possesses NO key file (it is
  // deleted — possession of the per-user key file is the only thing r6 trusts).
  fs.unlinkSync(sessionKeyFile(sid));
  const res = run(['read'], sid);
  // Pre-fix (BUG-360): findMine matched Bev by bare ambient WINDOW_ID and the
  // spoofer's read delivered the victim's messages and advanced their cursor.
  // Post-fix: no key-file possession -> no match -> no delivery, cursor untouched.
  assert.ok(!res.stdout.includes('BUG-360-TOP-SECRET'),
    `the key-less spoofer must NOT receive the victim's message body: ${res.stdout}`);
  const [cursorAfter] = await db.query('SELECT last_read_id FROM sync_read_cursor WHERE name=?', ['Bev']);
  assert.equal(Number(cursorAfter[0].last_read_id), Number(cursorBefore[0].last_read_id),
    'the key-less spoofer must NOT advance the victim\'s read cursor');
  // False-pass guard: the victim, WITH the key file restored, still receives it.
  await fs.promises.writeFile(sessionKeyFile(sid), victimSecret, 'utf8');
  const readBack = run(['read'], sid);
  assert.ok(readBack.stdout.includes('BUG-360-TOP-SECRET'),
    `the victim with key-file possession must still receive the message: ${readBack.stdout}`);
});
