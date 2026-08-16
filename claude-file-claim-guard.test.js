/**
 * claude-file-claim-guard.test.js — unit + subprocess tests for FEAT-136
 * (tool.fileclaimguard) Guard 2: the edit-time file-claim PreToolUse guard.
 *
 * The guard blocks an Edit/Write whose target path is claimed in
 * sync_file_claims by a DIFFERENT live session (session_id mismatch) and
 * allows silently when the path is unowned or owned by the current session.
 * It must FAIL OPEN on any DB error — a warning on stderr, exit 0 — so a dead
 * database can never crash an Edit.
 *
 * Runs against a scratch database (METRO_DB_NAME=metro_test_fileclaim_<pid>)
 * created and dropped by this suite; the real `metro` DB is never written to.
 * The pure decision logic (toRepoRelative / foreignOwner) is unit-tested with
 * no DB at all; the deny/allow/fail-open behaviour is proven end-to-end by
 * spawning the real script against the scratch DB.
 *
 * Run: node --test claude-file-claim-guard.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('path');
const { spawnSync } = require('child_process');
const mysql = require('mysql2/promise');

const ROOT = __dirname;
const TEST_DB = process.env.METRO_DB_TEST_NAME || `metro_test_fileclaim_${process.pid}`;

const DB_HOST = process.env.METRO_DB_HOST || '127.0.0.1';
const DB_PORT = Number(process.env.METRO_DB_PORT || 3306);
const DB_USER = process.env.METRO_DB_USER || 'root';
const DB_PASSWORD = process.env.METRO_DB_PASSWORD || '';

let db;

// Loaded before the guard exists is a red-phase "Cannot find module" — this
// file is the red-then-green proof for Guard 2.
const { toRepoRelative, extractEditPaths, foreignOwner } = require('./claude-file-claim-guard.js');

// ── pure unit tests (no DB) ────────────────────────────────────────────────

test('toRepoRelative maps an absolute path under ROOT to a repo-relative forward-slash path', () => {
  const abs = path.join(ROOT, 'internal', 'ui', 'dash', 'dashboard.md');
  assert.equal(toRepoRelative(abs), 'internal/ui/dash/dashboard.md');
});

test('toRepoRelative leaves an already-relative path unchanged', () => {
  assert.equal(toRepoRelative('internal/ui/dash/dashboard.md'), 'internal/ui/dash/dashboard.md');
});

test('toRepoRelative maps the project root itself to the empty string', () => {
  assert.equal(toRepoRelative(ROOT), '');
});

// ── FEAT-136 reject-fix regressions: path-canonicalisation / MultiEdit / glob ──

test('toRepoRelative collapses a ./-spelled path to the canonical repo-relative key', () => {
  // Raw strings on purpose: path.join() would collapse `./`/`..` before the
  // guard ever saw it, defeating the regression.
  assert.equal(toRepoRelative(`${ROOT}/./docs/x.md`), 'docs/x.md');
  assert.equal(toRepoRelative(`${ROOT}/docs/../docs/x.md`), 'docs/x.md');
  assert.equal(toRepoRelative(`${ROOT}//docs//x.md`), 'docs/x.md');
  assert.equal(toRepoRelative('internal/./ui/dash/x.md'), 'internal/ui/dash/x.md');
});

test('toRepoRelative anchors a relative ..-escape to the repo root (r2)', () => {
  // The round-1 code only resolved ABSOLUTE paths; a RELATIVE `..` was never
  // anchored to ROOT, so `../Metropolis/docs/x.md` stayed the distinct key
  // `../Metropolis/docs/x.md` and overlaps() missed the claim on `docs/x.md`.
  // Raw strings on purpose: path.join() would collapse the `..` first.
  const escape = `../${path.basename(ROOT)}/docs/x.md`;
  assert.equal(toRepoRelative(escape), 'docs/x.md');
  assert.equal(toRepoRelative('./docs/x.md'), 'docs/x.md');
  assert.equal(toRepoRelative('docs//x.md'), 'docs/x.md');
});

test('extractEditPaths pulls file_path from Edit/Write/MultiEdit and notebook_path from NotebookEdit', () => {
  assert.deepEqual(extractEditPaths({ file_path: 'docs/a.md' }), ['docs/a.md']);
  assert.deepEqual(
    extractEditPaths({ file_path: 'docs/a.md', edits: [{ old_string: 'x', new_string: 'y' }] }),
    ['docs/a.md']
  );
  assert.deepEqual(extractEditPaths({ notebook_path: 'nb.ipynb', new_source: 'z' }), ['nb.ipynb']);
  // A per-edit path inside edits[] is extracted too (defensive against a
  // MultiEdit variant that carries the target on each edit).
  assert.deepEqual(
    extractEditPaths({ edits: [{ old_string: 'x', new_string: 'y', file_path: 'docs/b.md' }] }),
    ['docs/b.md']
  );
});

test('foreignOwner returns null when there are no claims (unowned target)', () => {
  assert.equal(foreignOwner('internal/ui/dash/dashboard.md', [], 'me'), null);
});

test('foreignOwner returns null when the only overlapping claim is the current session\'s own', () => {
  const claims = [{ path: 'internal/ui/dash', name: 'bob', session_id: 'me' }];
  assert.equal(foreignOwner('internal/ui/dash/dashboard.md', claims, 'me'), null);
});

test('foreignOwner returns the owner when a different session claims an overlapping directory', () => {
  const claims = [{ path: 'internal/ui/dash', name: 'bob', session_id: 'someone-else' }];
  const hit = foreignOwner('internal/ui/dash/dashboard.md', claims, 'me');
  assert.ok(hit, 'a foreign overlapping claim must produce a deny decision');
  assert.equal(hit.owner, 'bob');
  assert.equal(hit.heldPath, 'internal/ui/dash');
});

test('foreignOwner returns null for a disjoint path', () => {
  const claims = [{ path: 'internal/ui/dash', name: 'bob', session_id: 'someone-else' }];
  assert.equal(foreignOwner('internal/ui/mining/scratch.md', claims, 'me'), null);
});

test('foreignOwner returns the owner on an exact same-path foreign claim', () => {
  const claims = [{ path: 'internal/ui/dash/dashboard.md', name: 'ben', session_id: 'other' }];
  const hit = foreignOwner('internal/ui/dash/dashboard.md', claims, 'me');
  assert.ok(hit);
  assert.equal(hit.owner, 'ben');
});

// ── integration (subprocess against scratch DB) ────────────────────────────

function runGuard(payload, envOverrides = {}) {
  return spawnSync(process.execPath, [path.join(ROOT, 'claude-file-claim-guard.js')], {
    cwd: ROOT,
    input: JSON.stringify(payload),
    encoding: 'utf8',
    env: {
      ...process.env,
      METRO_DB_HOST: DB_HOST,
      METRO_DB_PORT: String(DB_PORT),
      METRO_DB_USER: DB_USER,
      METRO_DB_PASSWORD: DB_PASSWORD,
      METRO_DB_NAME: TEST_DB,
      CLAUDE_DISABLE_FILE_CLAIM_GUARD: '',
      ...envOverrides,
    },
  });
}

function editPayload(filePath, sessionId) {
  return { tool_name: 'Edit', session_id: sessionId, tool_input: { file_path: filePath } };
}

function decision(result) {
  const out = (result.stdout || '').trim();
  if (!out) return null;
  try {
    return JSON.parse(out).hookSpecificOutput || null;
  } catch {
    return null;
  }
}

test.before(async () => {
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
});

test.beforeEach(async () => {
  await db.query('DELETE FROM sync_file_claims');
});

test('a foreign-owned edit is DENIED, naming the owner and the release command', async () => {
  await db.query(
    'INSERT INTO sync_file_claims (path, name, session_id, claimed_ms) VALUES (?, ?, ?, ?)',
    ['internal/ui/dash', 'bob', 'foreign-session', Date.now()]
  );
  const abs = path.join(ROOT, 'internal', 'ui', 'dash', 'dashboard.md');
  const r = runGuard(editPayload(abs, 'current-session'));
  assert.equal(r.status, 0, 'deny is signalled on stdout JSON, exit stays 0');
  const d = decision(r);
  assert.ok(d, `expected a deny JSON on stdout, got: ${r.stdout}`);
  assert.equal(d.permissionDecision, 'deny');
  assert.match(d.permissionDecisionReason, /claimed by bob/i);
  assert.match(d.permissionDecisionReason, /claude-sync\.js release/);
});

test('a ./-spelled foreign edit is DENIED (path-canonicalisation)', async () => {
  await db.query(
    'INSERT INTO sync_file_claims (path, name, session_id, claimed_ms) VALUES (?, ?, ?, ?)',
    ['internal/ui/dash', 'bob', 'foreign-session', Date.now()]
  );
  // Raw strings on purpose: path.join() collapses `./`/`..`/`//` before the
  // guard sees it, defeating the regression. Each spelling must resolve to
  // the SAME claimed file, not slip past the comparison as a distinct string.
  for (const spelling of [
    `${ROOT}/./internal/ui/dash/dashboard.md`,
    `${ROOT}/internal/nope/../ui/dash/dashboard.md`,
    `${ROOT}//internal//ui//dash//dashboard.md`,
  ]) {
    const r = runGuard(editPayload(spelling, 'current-session'));
    const d = decision(r);
    assert.ok(d, `expected a deny for "${spelling}", got: ${r.stdout}`);
    assert.equal(d.permissionDecision, 'deny');
    assert.match(d.permissionDecisionReason, /claimed by bob/i);
  }
});

test('a relative ..-escape foreign edit is DENIED (root-anchored canonicalisation, r2)', async () => {
  await db.query(
    'INSERT INTO sync_file_claims (path, name, session_id, claimed_ms) VALUES (?, ?, ?, ?)',
    ['docs/x.md', 'bob', 'foreign-session', Date.now()]
  );
  // The reachable bypass from the r2 reject note: a RELATIVE `..` spelling
  // (`../Metropolis/docs/x.md`) resolves to the SAME claimed bytes as
  // `docs/x.md` but round-1 produced a distinct unanchored key and ALLOWED the
  // edit. Each spelling must now collapse to `docs/x.md` and be denied.
  for (const spelling of [
    `../${path.basename(ROOT)}/docs/x.md`,
    './docs/x.md',
    'docs//x.md',
  ]) {
    const r = runGuard(editPayload(spelling, 'current-session'));
    const d = decision(r);
    assert.ok(d, `expected a deny for "${spelling}", got: ${r.stdout}`);
    assert.equal(d.permissionDecision, 'deny');
    assert.match(d.permissionDecisionReason, /claimed by bob/i);
  }
});

test('a foreign MultiEdit is DENIED (edits[] mutator is no longer silently skipped)', async () => {
  await db.query(
    'INSERT INTO sync_file_claims (path, name, session_id, claimed_ms) VALUES (?, ?, ?, ?)',
    ['internal/ui/dash', 'bob', 'foreign-session', Date.now()]
  );
  const abs = path.join(ROOT, 'internal', 'ui', 'dash', 'dashboard.md');
  const r = runGuard({
    tool_name: 'MultiEdit',
    session_id: 'current-session',
    tool_input: { file_path: abs, edits: [{ old_string: 'a', new_string: 'b' }] },
  });
  const d = decision(r);
  assert.ok(d, `expected a deny for MultiEdit, got: ${r.stdout}`);
  assert.equal(d.permissionDecision, 'deny');
  assert.match(d.permissionDecisionReason, /claimed by bob/i);
});

test('a foreign NotebookEdit is DENIED (notebook_path mutator is no longer silently skipped)', async () => {
  await db.query(
    'INSERT INTO sync_file_claims (path, name, session_id, claimed_ms) VALUES (?, ?, ?, ?)',
    ['internal/ui/dash', 'bob', 'foreign-session', Date.now()]
  );
  const nb = path.join(ROOT, 'internal', 'ui', 'dash', 'analysis.ipynb');
  const r = runGuard({
    tool_name: 'NotebookEdit',
    session_id: 'current-session',
    tool_input: { notebook_path: nb, new_source: 'x' },
  });
  const d = decision(r);
  assert.ok(d, `expected a deny for NotebookEdit, got: ${r.stdout}`);
  assert.equal(d.permissionDecision, 'deny');
  assert.match(d.permissionDecisionReason, /claimed by bob/i);
});

test('a concrete edit under a *.test.go glob claim is DENIED (glob-aware overlaps)', async () => {
  await db.query(
    'INSERT INTO sync_file_claims (path, name, session_id, claimed_ms) VALUES (?, ?, ?, ?)',
    ['internal/engine/consumption/*_test.go', 'bob', 'foreign-session', Date.now()]
  );
  const abs = path.join(ROOT, 'internal', 'engine', 'consumption', 's6_endtoend_test.go');
  const r = runGuard(editPayload(abs, 'current-session'));
  const d = decision(r);
  assert.ok(d, `expected a deny for a glob-claimed file, got: ${r.stdout}`);
  assert.equal(d.permissionDecision, 'deny');
  assert.match(d.permissionDecisionReason, /claimed by bob/i);
});

test('an own-session edit is ALLOWED (no deny JSON, empty stdout)', async () => {
  await db.query(
    'INSERT INTO sync_file_claims (path, name, session_id, claimed_ms) VALUES (?, ?, ?, ?)',
    ['internal/ui/dash', 'bob', 'own-session', Date.now()]
  );
  const abs = path.join(ROOT, 'internal', 'ui', 'dash', 'dashboard.md');
  const r = runGuard(editPayload(abs, 'own-session'));
  assert.equal(r.status, 0);
  assert.equal((r.stdout || '').trim(), '');
});

test('an unowned edit is ALLOWED (no deny JSON, empty stdout)', async () => {
  const abs = path.join(ROOT, 'internal', 'ui', 'mining', 'scratch.md');
  const r = runGuard(editPayload(abs, 'current-session'));
  assert.equal(r.status, 0);
  assert.equal((r.stdout || '').trim(), '');
});

test('a DB failure fails OPEN — exit 0 with a stderr warning, never a crash', () => {
  const abs = path.join(ROOT, 'internal', 'ui', 'dash', 'dashboard.md');
  const r = runGuard(editPayload(abs, 'current-session'), { METRO_DB_PORT: '1' });
  assert.equal(r.status, 0, 'a dead DB must never fail the Edit');
  assert.match(r.stderr, /fail-open|allowing edit/i);
  assert.equal((r.stdout || '').trim(), '', 'fail-open must not emit a deny');
});

test('a non-Edit/Write tool is ignored (no DB attempt, no output)', () => {
  const r = runGuard({ tool_name: 'Bash', session_id: 's', tool_input: { command: 'x' } }, { METRO_DB_PORT: '1' });
  assert.equal(r.status, 0);
  assert.equal((r.stdout || '').trim(), '');
  assert.equal(r.stderr, '', 'a non-Edit/Write payload must exit at the gate without touching the DB');
});

test('the deliberate-disable kill switch allows the edit', () => {
  const abs = path.join(ROOT, 'internal', 'ui', 'dash', 'dashboard.md');
  const r = runGuard(editPayload(abs, 'current-session'), { METRO_DB_PORT: '1', CLAUDE_DISABLE_FILE_CLAIM_GUARD: '1' });
  assert.equal(r.status, 0);
  assert.equal((r.stdout || '').trim(), '');
  assert.equal(r.stderr, '');
});
