/**
 * claude-bow-recordersession.test.js — BUG-340 (mechanical verdict
 * independence, deliverable 2a): tests for claude-bow.js's
 * currentSessionIdentity() and the recorder_session stamping in
 * recordDestructiveVerdict()/the bow_destructive_verdicts schema migration.
 *
 * Runs against the real local metro MariaDB (same convention as
 * claude-destructive-guard.test.js's own BOW-backed cases): creates
 * throwaway bow_items rows, always deletes them in a `finally` (cascades to
 * bow_destructive_verdicts via the FK).
 *
 * Run: node --test claude-bow-recordersession.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const crypto = require('crypto');
const mysql = require('mysql2/promise');

const path = require('node:path');
const bow = require('./claude-bow.js');
const { recordDestructiveVerdict, latestDestructiveVerdict, currentSessionIdentity, ensureSchema } = bow;

function connectDb() {
  return mysql.createConnection({
    host: process.env.METRO_DB_HOST || '127.0.0.1',
    port: Number(process.env.METRO_DB_PORT || 3306),
    user: process.env.METRO_DB_USER || 'root',
    password: process.env.METRO_DB_PASSWORD || '',
    database: process.env.METRO_DB_NAME || 'metro',
  });
}

// BUG-340 r2 N1 (independent round REJECT): fixture BOW codes MUST be
// unmistakable and self-cleaning. The old `FEAT-${crypto.randomBytes(4).
// readUInt32BE(0)}` scheme drew from the FULL uint32 range (0..4294967295),
// which overlaps the numeric range real large-random FEAT keys ALSO draw
// from (FEAT-1972079945, FEAT-2326609711, etc. all sit inside that same
// uint32 span) — two stale 2026-08-22 fixture rows survived in the LIVE
// metro BOW as a result, one adjacent to a real item. Fixtures now draw from
// a RESERVED sub-range strictly ABOVE the uint32 max (so no uint32-based
// real-code generator can ever produce a collision) and carry an
// unmistakable "DESTRUCTIVE-SCRATCH" title prefix so a human auditing
// `node claude-bow.js list` can immediately identify — and hand-remove — any
// row that somehow survives a failed cleanup.
const FIXTURE_CODE_RESERVED_BASE = 9990000000; // > 4294967295 (uint32 max)
function fixtureCode() {
  const tail = crypto.randomBytes(4).readUInt32BE(0) % 9000000; // 0..8999999
  return `FEAT-${FIXTURE_CODE_RESERVED_BASE + tail}`;
}

// BUG-340 r2 N1: every fixture guid created by this file is tracked here and
// swept by the test.after() backstop below — a belt-and-braces guarantee
// that survives a test whose assertion throws OUTSIDE its own try/finally
// (the existing per-test `finally { deleteFixtureItem(...) }` pattern is the
// fast path; this is the "even on failure" guarantee the r2 finding asked
// for).
const _fixtureGuids = new Set();

async function createFixtureItem(db, label) {
  const suffix = crypto.randomBytes(4).toString('hex');
  const guid = crypto.randomUUID();
  const code = fixtureCode();
  const mkey = `test.bugrecordersession.${label}.${suffix}`;
  await db.query(
    'INSERT INTO bow_items (guid, code, mkey, item_type, title, priority) VALUES (?, ?, ?, ?, ?, ?)',
    [guid, code, mkey, 'feature', `DESTRUCTIVE-SCRATCH fixture — ${label} (${suffix})`, 'P3']);
  _fixtureGuids.add(guid);
  return { guid, code, mkey };
}

async function deleteFixtureItem(db, guid) {
  await db.query('DELETE FROM bow_items WHERE guid = ?', [guid]);
  _fixtureGuids.delete(guid);
}

// BUG-340 r2 N1: guaranteed cleanup backstop — runs once after every test in
// this file, regardless of pass/fail, and deletes any fixture guid that a
// per-test `finally` did not already remove. A DELETE against an
// already-removed guid is a harmless no-op (0 rows affected), so double
// cleanup from both the per-test finally AND this sweep is safe.
test.after(async () => {
  if (!_fixtureGuids.size) return;
  let db;
  try {
    db = await connectDb();
  } catch {
    return; // DB unreachable at teardown — nothing more we can do here.
  }
  try {
    for (const guid of _fixtureGuids) {
      // eslint-disable-next-line no-await-in-loop
      await db.query('DELETE FROM bow_items WHERE guid = ?', [guid]).catch(() => {});
    }
  } finally {
    await db.end().catch(() => {});
  }
});

// ---------------------------------------------------------------------------
// currentSessionIdentity()
// ---------------------------------------------------------------------------

test('currentSessionIdentity(): prefers CLAUDE_CODE_SESSION_ID over CLAUDE_SESSION_ID (same precedence as claude-sync.js\'s WINDOW_ID)', () => {
  const savedA = process.env.CLAUDE_CODE_SESSION_ID;
  const savedB = process.env.CLAUDE_SESSION_ID;
  try {
    process.env.CLAUDE_CODE_SESSION_ID = 'window-A';
    process.env.CLAUDE_SESSION_ID = 'window-B';
    assert.equal(currentSessionIdentity(), 'window-A');
  } finally {
    if (savedA === undefined) delete process.env.CLAUDE_CODE_SESSION_ID; else process.env.CLAUDE_CODE_SESSION_ID = savedA;
    if (savedB === undefined) delete process.env.CLAUDE_SESSION_ID; else process.env.CLAUDE_SESSION_ID = savedB;
  }
});

test('currentSessionIdentity(): falls back to CLAUDE_SESSION_ID when CLAUDE_CODE_SESSION_ID is unset', () => {
  const savedA = process.env.CLAUDE_CODE_SESSION_ID;
  const savedB = process.env.CLAUDE_SESSION_ID;
  try {
    delete process.env.CLAUDE_CODE_SESSION_ID;
    process.env.CLAUDE_SESSION_ID = 'window-B';
    assert.equal(currentSessionIdentity(), 'window-B');
  } finally {
    if (savedA === undefined) delete process.env.CLAUDE_CODE_SESSION_ID; else process.env.CLAUDE_CODE_SESSION_ID = savedA;
    if (savedB === undefined) delete process.env.CLAUDE_SESSION_ID; else process.env.CLAUDE_SESSION_ID = savedB;
  }
});

test('currentSessionIdentity(): returns the literal string "unknown" — not empty, not null — when neither env var is set (PROVE-CAN-FAIL: asserting the WRONG literal must fail)', () => {
  const savedA = process.env.CLAUDE_CODE_SESSION_ID;
  const savedB = process.env.CLAUDE_SESSION_ID;
  try {
    delete process.env.CLAUDE_CODE_SESSION_ID;
    delete process.env.CLAUDE_SESSION_ID;
    assert.equal(currentSessionIdentity(), 'unknown');
    assert.notEqual(currentSessionIdentity(), ''); // would only pass if the function were broken
  } finally {
    if (savedA === undefined) delete process.env.CLAUDE_CODE_SESSION_ID; else process.env.CLAUDE_CODE_SESSION_ID = savedA;
    if (savedB === undefined) delete process.env.CLAUDE_SESSION_ID; else process.env.CLAUDE_SESSION_ID = savedB;
  }
});

// ---------------------------------------------------------------------------
// recordDestructiveVerdict() stamps recorder_session
// ---------------------------------------------------------------------------

test('recordDestructiveVerdict(): stamps recorder_session from currentSessionIdentity() by default', async () => {
  const db = await connectDb();
  await ensureSchema(db);
  const item = await createFixtureItem(db, 'stamp-default');
  const savedA = process.env.CLAUDE_CODE_SESSION_ID;
  try {
    process.env.CLAUDE_CODE_SESSION_ID = 'session-stamp-test-1';
    const result = await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Fixture' });
    assert.equal(result.recorderSession, 'session-stamp-test-1');
    const stored = await latestDestructiveVerdict(db, item.code);
    assert.equal(stored.recorder_session, 'session-stamp-test-1');
  } finally {
    if (savedA === undefined) delete process.env.CLAUDE_CODE_SESSION_ID; else process.env.CLAUDE_CODE_SESSION_ID = savedA;
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('recordDestructiveVerdict(): an explicit opts.recorderSession overrides the env-derived default', async () => {
  const db = await connectDb();
  await ensureSchema(db);
  const item = await createFixtureItem(db, 'stamp-override');
  try {
    const result = await recordDestructiveVerdict(db, item.code, {
      verdict: 'accept', attacker: 'Fixture', recorderSession: 'explicit-override-session',
    });
    assert.equal(result.recorderSession, 'explicit-override-session');
    const stored = await latestDestructiveVerdict(db, item.code);
    assert.equal(stored.recorder_session, 'explicit-override-session');
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('recordDestructiveVerdict(): with no session env at all, stamps "unknown" (PROVE-CAN-FAIL: asserting any other value must fail)', async () => {
  const db = await connectDb();
  await ensureSchema(db);
  const item = await createFixtureItem(db, 'stamp-unknown');
  const savedA = process.env.CLAUDE_CODE_SESSION_ID;
  const savedB = process.env.CLAUDE_SESSION_ID;
  try {
    delete process.env.CLAUDE_CODE_SESSION_ID;
    delete process.env.CLAUDE_SESSION_ID;
    const result = await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Fixture' });
    assert.equal(result.recorderSession, 'unknown');
  } finally {
    if (savedA === undefined) delete process.env.CLAUDE_CODE_SESSION_ID; else process.env.CLAUDE_CODE_SESSION_ID = savedA;
    if (savedB === undefined) delete process.env.CLAUDE_SESSION_ID; else process.env.CLAUDE_SESSION_ID = savedB;
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('bow_destructive_verdicts.recorder_session column exists after ensureSchema() (migration ran)', async () => {
  const db = await connectDb();
  await ensureSchema(db);
  const [rows] = await db.query(
    "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bow_destructive_verdicts' AND COLUMN_NAME = 'recorder_session'"
  );
  assert.equal(rows.length, 1, 'expected the recorder_session column to exist after ensureSchema()');
  await db.end();
});

// ---------------------------------------------------------------------------
// BUG-340 r1 F1: recorder_cwd — the second half of the session/cwd pair
// ---------------------------------------------------------------------------

test('bow_destructive_verdicts.recorder_cwd column exists after ensureSchema() (BUG-340 r1 F1 migration ran)', async () => {
  const db = await connectDb();
  await ensureSchema(db);
  const [rows] = await db.query(
    "SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'bow_destructive_verdicts' AND COLUMN_NAME = 'recorder_cwd'"
  );
  assert.equal(rows.length, 1, 'expected the recorder_cwd column to exist after ensureSchema() — F1 was NOT applied if this fails');
  await db.end();
});

test('recordDestructiveVerdict(): stamps recorder_cwd from currentRecorderCwd() (process.cwd()) by default', async () => {
  const db = await connectDb();
  await ensureSchema(db);
  const item = await createFixtureItem(db, 'stamp-cwd-default');
  try {
    const result = await recordDestructiveVerdict(db, item.code, { verdict: 'accept', attacker: 'Fixture' });
    assert.ok(result.recorderCwd && result.recorderCwd !== 'unknown', `expected a real cwd, got ${result.recorderCwd}`);
    const stored = await latestDestructiveVerdict(db, item.code);
    assert.equal(stored.recorder_cwd, result.recorderCwd);
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('recordDestructiveVerdict(): an explicit opts.recorderCwd overrides the process.cwd()-derived default (PROVE-CAN-FAIL: the two must differ)', async () => {
  const db = await connectDb();
  await ensureSchema(db);
  const item = await createFixtureItem(db, 'stamp-cwd-override');
  try {
    const result = await recordDestructiveVerdict(db, item.code, {
      verdict: 'accept', attacker: 'Fixture', recorderCwd: 'E:\\some\\other\\worktree',
    });
    assert.notEqual(result.recorderCwd, bow.currentRecorderCwd(), 'the override must NOT equal this process\'s own real cwd');
    assert.match(result.recorderCwd, /some\/other\/worktree$/i);
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

// ---------------------------------------------------------------------------
// isSelfVerdict() — the shared "session AND cwd both match" predicate
// ---------------------------------------------------------------------------

// Platform-neutral fixtures (the earlier 'E:\git...' literals only
// normalised on Windows and reddened on the Linux CI node-test runner).
const _svRoot = path.resolve('/tmp/selfverdict/repo');
const _svStored = bow.normalizeRecorderCwd(_svRoot);
const _svSub = bow.normalizeRecorderCwd(path.join(_svRoot, '.claude', 'worktrees', 'attacker'));

test('isSelfVerdict(): true only when BOTH recorder_session and recorder_cwd match', () => {
  const v = { recorder_session: 'sess-A', recorder_cwd: 'e:/git/metropolis' };
  assert.equal(bow.isSelfVerdict(v, 'sess-A', 'E:\\git\\Metropolis'), true);
});

test('isSelfVerdict(): false when session matches but cwd differs (PROVE-CAN-FAIL: this is the exact dispatched-round case F1 exists for)', () => {
  const v = { recorder_session: 'sess-A', recorder_cwd: 'e:/git/metropolis/.claude/worktrees/attacker' };
  assert.equal(bow.isSelfVerdict(v, 'sess-A', 'E:\\git\\Metropolis'), false);
});

test('isSelfVerdict(): false when cwd matches but session differs', () => {
  const v = { recorder_session: 'sess-B', recorder_cwd: 'e:/git/metropolis' };
  assert.equal(bow.isSelfVerdict(v, 'sess-A', 'E:\\git\\Metropolis'), false);
});

test('isSelfVerdict(): false when either identity is "unknown"/empty/null, never treated as a match', () => {
  assert.equal(bow.isSelfVerdict({ recorder_session: null, recorder_cwd: null }, 'sess-A', 'E:\\git\\Metropolis'), false);
  assert.equal(bow.isSelfVerdict({ recorder_session: 'unknown', recorder_cwd: 'unknown' }, 'unknown', 'E:\\git\\Metropolis'), false);
  assert.equal(bow.isSelfVerdict({ recorder_session: 'sess-A', recorder_cwd: null }, 'sess-A', 'E:\\git\\Metropolis'), false);
});

// ---------------------------------------------------------------------------
// BUG-340 r3 remediation — two surviving mutations pinned (r3 REJECT F1/F2)
// ---------------------------------------------------------------------------

test('BUG-340 r3 F1: an unresolved-cwd: marked row IS a self-verdict on session match (fail-closed, PROVE-CAN-FAIL: flipping the marker branch to return false must redden this)', () => {
  // A recorder whose environment had no git on PATH stamps
  // UNRESOLVED_CWD_MARKER + <normalised cwd>. The r2/r3 design treats that
  // row as SAME-TREE (fail-closed): the committer cannot prove independence,
  // so a session match must deny. The r3 round proved flipping this branch
  // to fail-open passed the entire estate — this test is that missing guard.
  const v = {
    recorder_session: 'sess-A',
    recorder_cwd: bow.UNRESOLVED_CWD_MARKER + 'e:/somewhere/entirely/else',
  };
  assert.equal(
    bow.isSelfVerdict(v, 'sess-A', _svRoot),
    true,
    'an unresolved-cwd row on a matching session must be treated as SELF (fail-closed), regardless of the path it carries'
  );
  // And with a DIFFERENT session it stays not-self (the marker never widens
  // the deny to other sessions).
  assert.equal(bow.isSelfVerdict(v, 'sess-B', _svRoot), false);
});

test('BUG-340 r3 F2: currentRecorderCwd() run from a SUBDIRECTORY stamps the repo toplevel, not the subdir (PROVE-CAN-FAIL: reverting to raw-cwd stamping must redden this)', () => {
  // The r2 headline fix stamps `git rev-parse --show-toplevel` so a verdict
  // recorded from any subdirectory of the repo still matches the committer's
  // toplevel comparison. The r3 round proved reverting that fix passed the
  // whole estate — this test guards it. Run from a real subdirectory of THIS
  // repo (the worktree the test itself lives in).
  const path = require('node:path');
  const prevCwd = process.cwd();
  const sub = path.join(__dirname, 'githooks');
  try {
    process.chdir(sub);
    const stamped = bow.currentRecorderCwd();
    const expectedToplevel = bow.normalizeRecorderCwd(__dirname);
    assert.equal(
      stamped,
      expectedToplevel,
      `currentRecorderCwd() from ${sub} must stamp the repo toplevel (${expectedToplevel}), got ${stamped}`
    );
    assert.ok(!stamped.startsWith(bow.UNRESOLVED_CWD_MARKER), 'git was available, so no unresolved marker');
    assert.ok(!stamped.endsWith('/githooks'), 'must not stamp the subdirectory itself');
  } finally {
    process.chdir(prevCwd);
  }
});
