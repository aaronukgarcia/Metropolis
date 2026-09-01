/**
 * claude-destructive-guard-selfverdict.test.js — BUG-340 (mechanical verdict
 * independence, GR#23 independence amendment) end-to-end tests for the
 * self-verdict refusal added to claude-destructive-guard.js.
 *
 * Same throwaway-repo / real-local-metro-DB conventions as
 * claude-destructive-guard.test.js (see that file's header) — a dedicated
 * file rather than editing the large existing suite, to keep this focused
 * addition reviewable on its own.
 *
 * Run: node --test claude-destructive-guard-selfverdict.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const crypto = require('crypto');
const { spawnSync } = require('child_process');
const mysql = require('mysql2/promise');

const ROOT = __dirname;
const GUARD_PATH = path.join(ROOT, 'claude-destructive-guard.js');

const bow = require('./claude-bow.js');
const { recordDestructiveVerdict } = bow;

function connectDb() {
  return mysql.createConnection({
    host: process.env.METRO_DB_HOST || '127.0.0.1',
    port: Number(process.env.METRO_DB_PORT || 3306),
    user: process.env.METRO_DB_USER || 'root',
    password: process.env.METRO_DB_PASSWORD || '',
    database: process.env.METRO_DB_NAME || 'metro',
  });
}

// BUG-340 r2 N1 (independent round REJECT): see claude-bow-recordersession
// .test.js's identical comment for the full rationale — the old uint32
// random code collided with real large-random FEAT keys (two stale
// 2026-08-22 fixtures found live). Reserved range strictly above the uint32
// max + an unmistakable title prefix.
const FIXTURE_CODE_RESERVED_BASE = 9990000000; // > 4294967295 (uint32 max)
function fixtureCode() {
  const tail = crypto.randomBytes(4).readUInt32BE(0) % 9000000; // 0..8999999
  return `FEAT-${FIXTURE_CODE_RESERVED_BASE + tail}`;
}

const _fixtureGuids = new Set();

async function createFixtureItem(db, label) {
  const suffix = crypto.randomBytes(4).toString('hex');
  const guid = crypto.randomUUID();
  const code = fixtureCode();
  const mkey = `test.destructiveguard.selfverdict.${label}.${suffix}`;
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

// BUG-340 r2 N1: guaranteed cleanup backstop, same convention as
// claude-bow-recordersession.test.js — a per-test `finally` is the fast
// path, this sweep is the "even on failure" guarantee.
test.after(async () => {
  if (!_fixtureGuids.size) return;
  let db;
  try {
    db = await connectDb();
  } catch {
    return;
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

function git(cwd, args) {
  const r = spawnSync('git', args, { cwd, encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`git ${args.join(' ')} failed: ${r.stderr}`);
  return r.stdout.trim();
}

// BUG-340 r1 F1: made ASYNC-SAFE (awaits `fn`'s return value BEFORE cleanup)
// so a caller can `await recordDestructiveVerdict(...)` INSIDE the callback
// (needed to record a verdict with recorderCwd: dir — the dir only exists
// once withTempRepo has created it). The original sync `return fn(dir)`
// would have let `finally`'s rmSync race an async callback's still-pending
// DB write, deleting the temp repo out from under it.
async function withTempRepo(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dg-sv-fix-'));
  try {
    git(dir, ['init', '-b', 'main']);
    git(dir, ['config', 'user.name', 'Fixture Contributor']);
    git(dir, ['config', 'user.email', 'fixture@example.invalid']);
    return await fn(dir);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

function stageFile(dir, relPath, content = 'x\n') {
  const full = path.join(dir, relPath);
  fs.mkdirSync(path.dirname(full), { recursive: true });
  fs.writeFileSync(full, content, 'utf8');
  git(dir, ['add', relPath]);
}

/** Same shape as claude-destructive-guard.test.js's own runGuard(), but with
 * an explicit `env` REPLACING process.env entirely (not merged on top of
 * it) — needed so a test can prove the "no resolvable session identity"
 * case by genuinely UNSETTING both CLAUDE_CODE_SESSION_ID and
 * CLAUDE_SESSION_ID, which a plain override-merge could not do if the test
 * runner's own environment happens to carry one. */
function runGuardWithEnv(cwd, command, env) {
  const payload = JSON.stringify({ tool: 'Bash', tool_input: { command } });
  const r = spawnSync(process.execPath, [GUARD_PATH], {
    cwd, input: payload, encoding: 'utf8', env,
  });
  let denied = false;
  let reason = null;
  const stdout = (r.stdout || '').trim();
  if (stdout) {
    const parsed = JSON.parse(stdout);
    denied = parsed?.hookSpecificOutput?.permissionDecision === 'deny';
    reason = parsed?.hookSpecificOutput?.permissionDecisionReason || null;
  }
  return { denied, reason, stdout, stderr: r.stderr, status: r.status };
}

function envWithSession(sessionId) {
  const env = { ...process.env };
  delete env.CLAUDE_CODE_SESSION_ID;
  delete env.CLAUDE_SESSION_ID;
  if (sessionId !== undefined) env.CLAUDE_CODE_SESSION_ID = sessionId;
  return env;
}

test('BUG-340 r1 F1: a covering ACCEPT verdict recorded by THIS SAME session AND cwd is refused as a self-verdict', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'self-refused');
  try {
    await withTempRepo(async (dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      // BUG-340 r1 F1: recorder_session ALONE is no longer sufficient to
      // prove a self-verdict (a dispatched round shares the lead's session
      // env by inheritance) -- the recorded verdict must ALSO carry the
      // SAME cwd as the committer's repo (`dir`) for this to be a genuine
      // self-verdict, i.e. the same agent recording AND committing from the
      // SAME worktree.
      await recordDestructiveVerdict(db, item.code, {
        verdict: 'accept', attacker: 'Solo-Attacker', recorderSession: 'session-self-test-A', recorderCwd: dir,
      });
      const r = runGuardWithEnv(dir, `git commit -m "[${item.code}] change"`, envWithSession('session-self-test-A'));
      assert.equal(r.denied, true, 'the same session AND cwd that recorded the verdict must not have it count');
      assert.match(r.reason, /self-verdict|BUG-340/i);
      assert.match(r.reason, new RegExp(item.code));
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('CONTROL (PROVE-CAN-FAIL companion): the IDENTICAL verdict, checked from a DIFFERENT session, is accepted — proves the refusal is about identity match, not the verdict itself', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'self-control-diff');
  try {
    await withTempRepo(async (dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      await recordDestructiveVerdict(db, item.code, {
        verdict: 'accept', attacker: 'Solo-Attacker', recorderSession: 'session-self-test-B', recorderCwd: dir,
      });
      const r = runGuardWithEnv(dir, `git commit -m "[${item.code}] change"`, envWithSession('a-totally-different-session'));
      assert.equal(r.denied, false, 'a DIFFERENT session must not be refused — an independent attacker recorded this verdict');
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-340 r1 F1 (PROVE-CAN-FAIL: the design-call itself): the SAME session but a DIFFERENT cwd (a dispatched round from another worktree) is ALLOWED, not refused', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'self-diff-cwd-round');
  try {
    await withTempRepo(async (attackerDir) => {
      // The "attacker" records the verdict from ITS OWN worktree (a
      // different temp repo from the one the "lead" will commit from
      // below) — the realistic shape of a dispatched /round: a subagent
      // INHERITS the lead's CLAUDE_CODE_SESSION_ID (same session env) but
      // runs from its own worktree (different cwd).
      await recordDestructiveVerdict(db, item.code, {
        verdict: 'accept', attacker: 'Dispatched-Attacker', recorderSession: 'lead-session-shared', recorderCwd: attackerDir,
      });
    });
    await withTempRepo(async (leadDir) => {
      stageFile(leadDir, 'internal/foo.go', 'package foo\n');
      const r = runGuardWithEnv(leadDir, `git commit -m "[${item.code}] change"`, envWithSession('lead-session-shared'));
      assert.equal(
        r.denied, false,
        'a dispatched round (same inherited session, DIFFERENT worktree) must be ALLOWED — this is the exact ' +
          'mechanical-independence-blocks-every-legitimate-round bug BUG-340 r1 F1 fixes'
      );
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-340 r1 F1: a verdict with recorder_session matching but recorder_cwd = NULL (a pre-F1 row, post recorder_session/pre recorder_cwd migration) is allow-with-warn, never refused — cwd cannot be proven', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'self-legacy-cwd-null');
  try {
    // Insert directly (bypassing recordDestructiveVerdict) to simulate a row
    // written AFTER recorder_session existed but BEFORE this F1 fix's
    // recorder_cwd column was populated — the exact migration-boundary shape.
    await db.query(
      `INSERT INTO bow_destructive_verdicts (guid, item_guid, verdict, attacker, recorder_session, recorder_cwd)
       VALUES (?, ?, 'accept', 'Legacy-Attacker', 'session-cwd-null-test', NULL)`,
      [require('crypto').randomUUID(), item.guid]
    );

    await withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const r = runGuardWithEnv(dir, `git commit -m "[${item.code}] change"`, envWithSession('session-cwd-null-test'));
      assert.equal(
        r.denied, false,
        'a matching session with an UNPROVABLE (NULL) recorder_cwd must never be treated as a self-verdict match'
      );
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-340: a verdict with recorder_session = NULL (legacy, pre-BUG-340 row) is allow-with-warn, never refused, even when the current session happens to share a name', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'self-legacy-null');
  try {
    // Insert directly (bypassing recordDestructiveVerdict's own stamping) to
    // simulate a genuinely OLD row that predates this column's existence.
    await db.query(
      `INSERT INTO bow_destructive_verdicts (guid, item_guid, verdict, attacker, recorder_session)
       VALUES (?, ?, 'accept', 'Legacy-Attacker', NULL)`,
      [require('crypto').randomUUID(), item.guid]
    );

    await withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const r = runGuardWithEnv(dir, `git commit -m "[${item.code}] change"`, envWithSession('session-self-test-C'));
      assert.equal(r.denied, false, 'a NULL recorder_session must never be treated as a self-verdict match');
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('BUG-340: when the CURRENT session has no resolvable identity at all (no env vars set), a same-named recorder_session is still allow-with-warn, not refused', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'self-unknown-current');
  try {
    // The recorder itself also resolved to 'unknown' at record time (no env
    // set when it was recorded) — the realistic shape of this case.
    await recordDestructiveVerdict(db, item.code, {
      verdict: 'accept', attacker: 'Fixture', recorderSession: 'unknown',
    });

    await withTempRepo((dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const r = runGuardWithEnv(dir, `git commit -m "[${item.code}] change"`, envWithSession(undefined));
      assert.equal(r.denied, false, '"unknown" recorder + "unknown" current session must never match as a self-verdict');
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});
