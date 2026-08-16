/**
 * claude-dispatch-guard.fileclaims.test.js — FEAT-136 Guard 1 integration:
 * the dispatch-time file-ownership-overlap check is a HARD BLOCK.
 *
 * spawns claude-dispatch-guard.js against a scratch database and proves the
 * criterion end-to-end:
 *   - a brief declaring a path another live session has claimed is DENIED
 *   - a brief declaring a disjoint path is ALLOWED
 *
 * (The block itself — pushing the overlap into `problems`, which deny() turns
 * into a permissionDecision:"deny" — has existed since the guard's first
 * commit 6f37bd8; this file locks that behaviour with a DB-backed test, which
 * the unit-only claude-dispatch-guard.test.js deliberately does not cover.)
 *
 * Runs against a scratch DB (METRO_DB_NAME=metro_test_dispatchclaims_<pid>)
 * created and dropped by this suite; the real `metro` DB is never written to.
 *
 * Run: node --test claude-dispatch-guard.fileclaims.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('path');
const { spawnSync } = require('child_process');
const mysql = require('mysql2/promise');

const ROOT = __dirname;
const TEST_DB = process.env.METRO_DB_TEST_NAME || `metro_test_dispatchclaims_${process.pid}`;

const DB_HOST = process.env.METRO_DB_HOST || '127.0.0.1';
const DB_PORT = Number(process.env.METRO_DB_PORT || 3306);
const DB_USER = process.env.METRO_DB_USER || 'root';
const DB_PASSWORD = process.env.METRO_DB_PASSWORD || '';

let db;

function runGuard(payload, envOverrides = {}) {
  return spawnSync(process.execPath, [path.join(ROOT, 'claude-dispatch-guard.js')], {
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
      CLAUDE_DISABLE_DISPATCH_GUARD: '',
      ...envOverrides,
    },
  });
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

function briefOwned(paths) {
  return ['FILES YOU OWN:', ...paths].join('\n');
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

test('an overlapping-ownership brief is DENIED (permissionDecision deny naming the owner)', async () => {
  await db.query(
    'INSERT INTO sync_file_claims (path, name, session_id, claimed_ms) VALUES (?, ?, ?, ?)',
    ['internal/ui/dash', 'bob', 'foreign-session', Date.now()]
  );
  const prompt = briefOwned(['internal/ui/dash/dashboard.md']);
  const r = runGuard({
    tool_name: 'Agent',
    session_id: 'current-session',
    tool_input: { prompt, subagent_type: 'general-purpose', description: 'x' },
  });
  assert.equal(r.status, 0, 'deny is signalled on stdout JSON, exit stays 0 (fail-open on exit code)');
  const d = decision(r);
  assert.ok(d, `expected a deny JSON on stdout, got: ${r.stdout}`);
  assert.equal(d.permissionDecision, 'deny');
  assert.match(d.permissionDecisionReason, /overlaps "internal\/ui\/dash", claimed by bob/);
});

test('a disjoint brief is ALLOWED (no deny JSON, empty stdout)', async () => {
  await db.query(
    'INSERT INTO sync_file_claims (path, name, session_id, claimed_ms) VALUES (?, ?, ?, ?)',
    ['internal/ui/dash', 'bob', 'foreign-session', Date.now()]
  );
  const prompt = briefOwned(['internal/ui/mining/scratch.md']);
  const r = runGuard({
    tool_name: 'Agent',
    session_id: 'current-session',
    tool_input: { prompt, subagent_type: 'general-purpose', description: 'x' },
  });
  assert.equal(r.status, 0);
  assert.equal((r.stdout || '').trim(), '', 'a disjoint brief must not produce a deny');
});

test('a brief owning a path claimed by its OWN session is ALLOWED', async () => {
  await db.query(
    'INSERT INTO sync_file_claims (path, name, session_id, claimed_ms) VALUES (?, ?, ?, ?)',
    ['internal/ui/dash', 'bob', 'same-session', Date.now()]
  );
  const prompt = briefOwned(['internal/ui/dash/dashboard.md']);
  const r = runGuard({
    tool_name: 'Agent',
    session_id: 'same-session',
    tool_input: { prompt, subagent_type: 'general-purpose', description: 'x' },
  });
  assert.equal(r.status, 0);
  assert.equal((r.stdout || '').trim(), '', 'an own-session claim must not deny the dispatch');
});
