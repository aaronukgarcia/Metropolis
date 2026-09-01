/**
 * githooks/verdict-guard.test.js — tests for githooks/verdict-guard.js, the
 * git-side half of GR#23's Destructive-verdict gate (BUG-340/BUG-336
 * deliverable 1).
 *
 * Pure-logic tests use throwaway git repos (never this repo's own working
 * tree) and, where a real BOW row is needed, the real local metro MariaDB
 * with throwaway bow_items rows deleted in `finally` — same conventions as
 * claude-destructive-guard.test.js.
 *
 * Run: node --test githooks/verdict-guard.test.js
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

const vg = require('./verdict-guard.js');
const bow = require('../claude-bow.js');
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
  const mkey = `test.verdictguard.${label}.${suffix}`;
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
// — a plain `return fn(dir)` only returns fn's PENDING promise synchronously
// (an async function runs to its first `await` and then hands back control),
// so `finally`'s rmSync would fire immediately, deleting `dir` while an
// async callback's still-pending work (e.g. `await recordDestructiveVerdict`
// before `stageFile`/`runCheck` even run) is in flight. Needed now that a
// self-verdict fixture records recorderCwd: dir INSIDE the callback, before
// using `dir` again for staging/checking.
async function withTempRepo(fn) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'verdict-guard-fixture-'));
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

// ---------------------------------------------------------------------------
// getStagedFiles / isCodeBearing — pure classification
// ---------------------------------------------------------------------------

test('getStagedFiles(): reads the real staged set from a throwaway repo', () => {
  withTempRepo((dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    stageFile(dir, 'docs/notes.md', '# notes\n');
    const { dg } = vg.loadDependencies();
    const files = vg.getStagedFiles(dg, dir);
    assert.ok(files.includes('internal/foo.go'));
    assert.ok(files.includes('docs/notes.md'));
  });
});

test('isCodeBearing(): true for a file under an enforced dir (internal/)', () => {
  const { dg } = vg.loadDependencies();
  assert.equal(vg.isCodeBearing(dg, ['internal/foo.go']), true);
});

test('isCodeBearing(): false for a docs-only path (PROVE-CAN-FAIL companion to the enforced-dir case above)', () => {
  const { dg } = vg.loadDependencies();
  assert.equal(vg.isCodeBearing(dg, ['docs/notes.md']), false);
});

// ---------------------------------------------------------------------------
// checkStagedCommit() end-to-end
// ---------------------------------------------------------------------------

function writeMsgFile(dir, text) {
  const p = path.join(dir, '.msg-under-test');
  fs.writeFileSync(p, text, 'utf8');
  return p;
}

async function runCheck(dir, message, envOverrides = {}) {
  const msgPath = writeMsgFile(dir, message);
  const savedArgv2 = process.argv[2];
  const savedEnv = {};
  for (const k of Object.keys(envOverrides)) savedEnv[k] = process.env[k];
  try {
    process.argv[2] = msgPath;
    Object.assign(process.env, envOverrides);
    return await vg.checkStagedCommit(dir);
  } finally {
    process.argv[2] = savedArgv2;
    for (const k of Object.keys(envOverrides)) {
      if (savedEnv[k] === undefined) delete process.env[k]; else process.env[k] = savedEnv[k];
    }
  }
}

test('checkStagedCommit(): a docs-only staged set INSIDE an enforced dir is allowed with NO tag at all (FEAT-077 exemption, derived from claude-destructive-guard.js)', async () => {
  // BUG-340 r1 A3/M2 (independent round REJECT, coverage-gap finding): the
  // ORIGINAL version of this test staged `docs/readme-notes.md`, which is
  // NOT code-bearing at all (docs/ is not an enforced dir) — isCodeBearing()
  // returns false and checkStagedCommit() returns { ok:true } at its EARLY
  // exit, never even calling isExemptFileSet(). A mutant that disabled the
  // FEAT-077 exemption branch entirely (`if (dg.isExemptFileSet(...))` ->
  // `if (false)`) left this suite fully GREEN in round r1 — the exemption
  // branch was never actually exercised. Staging a lone `.md` file INSIDE an
  // enforced directory (internal/) forces isCodeBearing() to return true
  // FIRST, so the exemption branch is the ONLY thing that can produce
  // ok:true here.
  await withTempRepo(async (dir) => {
    stageFile(dir, 'internal/notes.md', '# notes\n');
    const result = await runCheck(dir, 'docs: tidy notes');
    assert.equal(result.ok, true, result.reason);
  });
});

test('checkStagedCommit(): a code-bearing staged set with NO tag is denied (PROVE-CAN-FAIL: this is the bypass trap AC-16 names)', async () => {
  await withTempRepo(async (dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const result = await runCheck(dir, 'fix: something with no tag');
    assert.equal(result.ok, false);
    assert.match(result.reason, /BOW tag/i);
    // BUG-340 r1 F5 (independent round REJECT, finding): this deny message
    // is built by claude-destructive-guard.js's noTagDenyMessage(), which
    // reused (before F5) a HARDCODED CLAUDE_DISABLE_DESTRUCTIVE_GUARD — the
    // PreToolUse hook's OWN bypass env, which does NOT bypass this git-side
    // hook at all (only CLAUDE_DISABLE_GIT_VERDICT_GUARD does). An operator
    // reading this exact message and setting the named env would find
    // nothing changed. The message must name THIS file's own BYPASS_ENV.
    assert.match(result.reason, new RegExp(vg.BYPASS_ENV), 'the deny message must name the GIT-SIDE bypass env, not the PreToolUse guard\'s');
    assert.doesNotMatch(result.reason, /CLAUDE_DISABLE_DESTRUCTIVE_GUARD/, 'the WRONG (PreToolUse-only) bypass env must not appear in a git-hook deny message');
  });
});

test('checkStagedCommit(): an unresolvable tag on a code-bearing commit is denied and named', async () => {
  await withTempRepo(async (dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const result = await runCheck(dir, '[FEAT-999999999] change');
    assert.equal(result.ok, false);
    assert.match(result.reason, /FEAT-999999999/);
  });
});

test('checkStagedCommit(): a real tag with an ACCEPTED verdict, from a DIFFERENT session, is allowed', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'accept-path');
  try {
    await recordDestructiveVerdict(db, item.code, {
      verdict: 'accept', attacker: 'Independent-Attacker', recorderSession: 'some-other-session',
    });
    await withTempRepo(async (dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const result = await runCheck(dir, `[${item.code}] change`, {
        CLAUDE_CODE_SESSION_ID: 'this-session-committing-now',
      });
      assert.equal(result.ok, true, result.reason);
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('checkStagedCommit(): the SAME tag/verdict, recorded from THIS SAME session AND cwd, is denied as a self-verdict (BUG-340 r1 F1)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'self-verdict-path');
  try {
    await withTempRepo(async (dir) => {
      // BUG-340 r1 F1: recorder_session alone is no longer sufficient — the
      // recorded verdict must ALSO carry the SAME cwd as the committer's
      // repo (`dir`) to be a genuine self-verdict (see
      // claude-bow-recordersession.test.js / claude-destructive-guard-
      // selfverdict.test.js for the full session-vs-cwd matrix).
      await recordDestructiveVerdict(db, item.code, {
        verdict: 'accept', attacker: 'Solo-Attacker', recorderSession: 'session-git-hook-self-test', recorderCwd: dir,
      });
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const result = await runCheck(dir, `[${item.code}] change`, {
        CLAUDE_CODE_SESSION_ID: 'session-git-hook-self-test',
      });
      assert.equal(result.ok, false);
      assert.match(result.reason, /self-verdict|BUG-340/i);
      // BUG-340 r1 F5: same bypass-env correctness requirement as the
      // no-tag deny above — every deny path through this git-side hook must
      // name ITS OWN bypass env, never the PreToolUse guard's.
      assert.match(result.reason, new RegExp(vg.BYPASS_ENV));
      assert.doesNotMatch(result.reason, /CLAUDE_DISABLE_DESTRUCTIVE_GUARD/);
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('checkStagedCommit(): a REJECT-only verdict on a code-bearing commit is denied', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'reject-path');
  try {
    await recordDestructiveVerdict(db, item.code, { verdict: 'reject', attacker: 'Fixture' });
    await withTempRepo(async (dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const result = await runCheck(dir, `[${item.code}] change`);
      assert.equal(result.ok, false);
      assert.match(result.reason, new RegExp(item.code));
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('checkStagedCommit(): a git ref recorded AT/AFTER the accept verdict voids it (verdict-tie rule, shared with claude-destructive-guard.js)', async () => {
  const db = await connectDb();
  const item = await createFixtureItem(db, 'tie-path');
  try {
    await recordDestructiveVerdict(db, item.code, {
      verdict: 'accept', attacker: 'Independent-Attacker', recorderSession: 'other-session',
    });
    // Insert a git ref timestamped AFTER the verdict.
    await db.query(
      'INSERT INTO bow_git_refs (item_guid, commit_hash, branch, note, created_at) VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP(6) + INTERVAL 1 SECOND)',
      [item.guid, 'a'.repeat(40), 'main', 'post-attack ref']
    );
    await withTempRepo(async (dir) => {
      stageFile(dir, 'internal/foo.go', 'package foo\n');
      const result = await runCheck(dir, `[${item.code}] change`, { CLAUDE_CODE_SESSION_ID: 'yet-another-session' });
      assert.equal(result.ok, false);
      assert.match(result.reason, /post-attack|un-attacked|newer/i);
    });
  } finally {
    await deleteFixtureItem(db, item.guid);
    await db.end();
  }
});

test('checkStagedCommit(): the bypass env var allows even a code-bearing, zero-tag commit (documented operator-only escape hatch)', async () => {
  await withTempRepo(async (dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const result = await runCheck(dir, 'fix: no tag at all', { [vg.BYPASS_ENV]: '1' });
    assert.equal(result.ok, true);
    assert.equal(result.bypassed, true);
  });
});

test('CONTROL (PROVE-CAN-FAIL): with the bypass env var UNSET, the identical zero-tag code-bearing commit is denied', async () => {
  await withTempRepo(async (dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    const result = await runCheck(dir, 'fix: no tag at all');
    assert.equal(result.ok, false);
  });
});

// ---------------------------------------------------------------------------
// DB-unreachable posture: fail-closed with a loud, documented bypass message
// ---------------------------------------------------------------------------

test('checkStagedCommit(): DB-unreachable denies (fail-closed) and the deny message NAMES the bypass env var (loud remediation, not a silent brick)', async () => {
  await withTempRepo(async (dir) => {
    stageFile(dir, 'internal/foo.go', 'package foo\n');
    // Point the BOW dependency at an unreachable host via env override —
    // claude-bow.js's own connect() reads METRO_DB_HOST.
    const result = await runCheck(dir, '[FEAT-123456] change', {
      METRO_DB_HOST: '127.0.0.1',
      METRO_DB_PORT: '1', // nothing listens here
    });
    assert.equal(result.ok, false);
    assert.match(result.reason, new RegExp(vg.BYPASS_ENV));
  });
});
