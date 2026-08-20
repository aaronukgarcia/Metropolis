/**
 * claude-bow-columns.test.js — column-length QoL fix (2026-08-20).
 *
 * Motivating incident: "Data too long for column X" surfaced as a raw MySQL
 * driver error mid-operation — it broke a BOW import AFTER its registry PR
 * had already merged (spec_ref), and killed `destructive`/`ref` writes three
 * times the same day (attacker, note). validateLen() is now the one
 * mechanism every write path in claude-bow.js uses to check a value against
 * its column's real VARCHAR limit BEFORE writing: single-row commands
 * REJECT up front (exit 1, nothing written — the caller can shorten and
 * retry losslessly); bulk `import` TRUNCATES-with-ellipsis and prints a
 * warning per truncated field instead, so a long plan always completes
 * rather than leaving the DB half-updated.
 *
 * Runs against its own scratch database (METRO_DB_NAME=metro_test_bowcolumns
 * by default, overridable via METRO_DB_TEST_NAME) — the real `metro`
 * database is never written to, following the exact isolation pattern
 * claude-bow.test.js already established (own DB, dropped in test.after,
 * FK-safe DELETE order in test.beforeEach).
 *
 * Covers:
 *   - RED/GREEN pairs for validateLen()'s three modes (exit/throw/truncate)
 *     as pure-function unit tests.
 *   - An over-limit `destructive --attacker` is rejected cleanly with
 *     nothing written (real subprocess — the exact 2026-08-20 `destructive`
 *     incident field).
 *   - An over-limit `ref --note` is rejected cleanly with nothing written
 *     (real subprocess — the exact 2026-08-20 `ref` incident field).
 *   - A bulk `import` with an over-limit spec_ref TRUNCATES and prints a
 *     warning, and the import COMPLETES (the exact 2026-08-20 import
 *     incident field) — in-process cmdImport is not exported, so this drives
 *     the real subprocess CLI and asserts against the DB row afterwards.
 *   - At-limit values (exactly maxLen chars) pass through untouched for
 *     both reject-mode and truncate-mode call sites.
 *
 * Run: node --test claude-bow-columns.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const crypto = require('crypto');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');
const mysql = require('mysql2/promise');

const ROOT = __dirname;
const TEST_DB = process.env.METRO_DB_TEST_NAME || `metro_test_bowcolumns_${process.pid}`;

// Set BEFORE requiring claude-bow.js's connect() — same discipline
// claude-bow.test.js uses (connect() reads METRO_DB_NAME from process.env
// at call time, not at module load).
process.env.METRO_DB_NAME = TEST_DB;

const bow = require('./claude-bow.js');
const { connect, ensureSchema, validateLen, BOW_COLUMN_MAX_LEN } = bow;

const DB_HOST = process.env.METRO_DB_HOST || '127.0.0.1';
const DB_PORT = Number(process.env.METRO_DB_PORT || 3306);
const DB_USER = process.env.METRO_DB_USER || 'root';
const DB_PASSWORD = process.env.METRO_DB_PASSWORD || '';

let db;

test.before(async () => {
  const boot = await mysql.createConnection({ host: DB_HOST, port: DB_PORT, user: DB_USER, password: DB_PASSWORD });
  await boot.query(`CREATE DATABASE IF NOT EXISTS \`${TEST_DB}\``);
  await boot.end();

  db = await connect();
  await ensureSchema(db);
});

test.after(async () => {
  if (db) {
    await db.query(`DROP DATABASE IF EXISTS \`${TEST_DB}\``);
    await db.end();
  }
});

test.beforeEach(async () => {
  // FK-safe delete order — clean slate per test.
  await db.query('DELETE FROM bow_gate_verdicts');
  await db.query('DELETE FROM bow_destructive_verdicts');
  await db.query('DELETE FROM bow_git_refs');
  await db.query('DELETE FROM bow_comments');
  await db.query('DELETE FROM bow_dependencies');
  await db.query('DELETE FROM bow_items');
});

async function insertItem({ code, mkey = null } = {}) {
  const guid = crypto.randomUUID();
  await db.query(
    `INSERT INTO bow_items (guid, code, item_type, title, priority, status, mkey) VALUES (?, ?, 'feature', ?, 'P2', 'open', ?)`,
    [guid, code, code, mkey]
  );
  return guid;
}

function bowCli(args) {
  return spawnSync(process.execPath, ['claude-bow.js', ...args], {
    cwd: ROOT, env: { ...process.env, METRO_DB_NAME: TEST_DB }, encoding: 'utf8',
  });
}

function mkTempDir(prefix) {
  return fs.mkdtempSync(path.join(os.tmpdir(), prefix));
}

// =============================================================================
// validateLen() unit tests — pure function, all three modes
// =============================================================================

test('validateLen: at-limit value (exactly maxLen chars) passes through untouched in exit mode', () => {
  const exact = 'x'.repeat(10);
  assert.equal(validateLen('field', exact, 10, { mode: 'exit' }), exact);
});

test('validateLen: at-limit value (exactly maxLen chars) passes through untouched in truncate mode', () => {
  const exact = 'y'.repeat(10);
  assert.equal(validateLen('field', exact, 10, { mode: 'truncate' }), exact);
});

test('validateLen: null/undefined/non-string values are a no-op passthrough regardless of mode', () => {
  assert.equal(validateLen('field', null, 5, { mode: 'exit' }), null);
  assert.equal(validateLen('field', undefined, 5, { mode: 'truncate' }), undefined);
  assert.equal(validateLen('field', 42, 1, { mode: 'exit' }), 42);
});

test('validateLen: throw mode raises an Error naming the field/limit/length for an over-limit value, and never exits the process', () => {
  const over = 'z'.repeat(11);
  assert.throws(
    () => validateLen('attacker', over, 10, { mode: 'throw', context: 'test context' }),
    (err) => err instanceof Error
      && err.message.includes('attacker')
      && err.message.includes('10')
      && err.message.includes('11')
  );
});

test('validateLen: truncate mode returns a value exactly maxLen chars long, ending in an ellipsis, and prints a warning', () => {
  const over = 'a'.repeat(20);
  const originalWarn = console.warn;
  let warned = '';
  console.warn = (msg) => { warned += msg; };
  try {
    const result = validateLen('spec_ref', over, 10, { mode: 'truncate', context: 'import item "x"' });
    assert.equal(result.length, 10, 'truncated value must be exactly maxLen chars');
    assert.equal(result[result.length - 1], '…', 'truncated value must end in an ellipsis');
    assert.equal(result.slice(0, 9), over.slice(0, 9), 'kept prefix must be the original content, byte-identical');
    assert.match(warned, /spec_ref/);
    assert.match(warned, /truncated/);
  } finally {
    console.warn = originalWarn;
  }
});

// =============================================================================
// RED/GREEN: single-row commands REJECT cleanly, nothing written
// =============================================================================

test('RED->GREEN: `destructive --attacker` over the 128-char limit is rejected cleanly, no row written (the exact 2026-08-20 incident field)', async () => {
  await insertItem({ code: 'FEAT-8801' });

  const overLimitAttacker = 'A'.repeat(BOW_COLUMN_MAX_LEN.verdict_attacker + 1);
  // RED: prove the check can fail — an over-limit attacker name.
  const red = bowCli(['destructive', 'FEAT-8801', '--verdict', 'accept', '--attacker', overLimitAttacker]);
  assert.notEqual(red.status, 0, 'over-limit --attacker must exit non-zero');
  assert.match(red.stderr, /attacker/i);
  assert.match(red.stderr, new RegExp(String(BOW_COLUMN_MAX_LEN.verdict_attacker)), 'error must name the actual limit');

  const [rowsAfterRed] = await db.query('SELECT * FROM bow_destructive_verdicts');
  assert.equal(rowsAfterRed.length, 0, 'nothing must be written when the attacker name is over the limit');

  // GREEN: an at-limit attacker name (exactly the limit) succeeds and writes.
  const atLimitAttacker = 'B'.repeat(BOW_COLUMN_MAX_LEN.verdict_attacker);
  const green = bowCli(['destructive', 'FEAT-8801', '--verdict', 'accept', '--attacker', atLimitAttacker]);
  assert.equal(green.status, 0, `at-limit --attacker must succeed: ${green.stderr}`);

  const [rowsAfterGreen] = await db.query('SELECT attacker FROM bow_destructive_verdicts');
  assert.equal(rowsAfterGreen.length, 1);
  assert.equal(rowsAfterGreen[0].attacker, atLimitAttacker, 'the at-limit value must be stored byte-identical, not truncated');
});

test('RED->GREEN: `ref --note` over the 255-char limit is rejected cleanly, no row written (the exact 2026-08-20 incident field)', async () => {
  await insertItem({ code: 'FEAT-8802' });
  const hash = '1234567890abcdef1234567890abcdef12345678';

  const overLimitNote = 'N'.repeat(BOW_COLUMN_MAX_LEN.ref_note + 1);
  // RED: prove the check can fail.
  const red = bowCli(['ref', 'FEAT-8802', hash, '--note', overLimitNote]);
  assert.notEqual(red.status, 0, 'over-limit --note must exit non-zero');
  assert.match(red.stderr, /note/i);

  const [rowsAfterRed] = await db.query('SELECT * FROM bow_git_refs');
  assert.equal(rowsAfterRed.length, 0, 'nothing must be written when the note is over the limit');

  // GREEN: an at-limit note succeeds and writes byte-identical.
  const atLimitNote = 'M'.repeat(BOW_COLUMN_MAX_LEN.ref_note);
  const green = bowCli(['ref', 'FEAT-8802', hash, '--note', atLimitNote]);
  assert.equal(green.status, 0, `at-limit --note must succeed: ${green.stderr}`);

  const [rowsAfterGreen] = await db.query('SELECT note FROM bow_git_refs');
  assert.equal(rowsAfterGreen.length, 1);
  assert.equal(rowsAfterGreen[0].note, atLimitNote);
});

test('RED->GREEN: `set --spec` over the 200-char limit is rejected cleanly, item unchanged', async () => {
  await insertItem({ code: 'FEAT-8803' });
  const overLimitSpec = 'S'.repeat(BOW_COLUMN_MAX_LEN.spec_ref + 1);

  const red = bowCli(['set', 'FEAT-8803', '--spec', overLimitSpec]);
  assert.notEqual(red.status, 0, 'over-limit --spec must exit non-zero');
  assert.match(red.stderr, /spec_ref/);

  const [rows] = await db.query('SELECT spec_ref FROM bow_items WHERE code = ?', ['FEAT-8803']);
  assert.equal(rows[0].spec_ref, null, 'spec_ref must remain untouched when the new value is over the limit');
});

// =============================================================================
// RED/GREEN: bulk import TRUNCATES with a warning and COMPLETES
// =============================================================================

test('RED->GREEN: bulk `import` with an over-limit spec_ref TRUNCATES-with-warning and the import COMPLETES (the exact 2026-08-20 import incident)', async () => {
  const overLimitSpec = 'M0-ENG §' + '9'.repeat(BOW_COLUMN_MAX_LEN.spec_ref); // well over 200 chars
  const plan = {
    items: [
      { mkey: 'test.import.col.overflow', type: 'feature', title: 'Import overflow test item', specRef: overLimitSpec },
    ],
  };
  const tmp = mkTempDir('bow-columns-import-');
  const planFile = path.join(tmp, 'plan.json');
  fs.writeFileSync(planFile, JSON.stringify(plan), 'utf8');

  // RED (documents the failure this fix replaces): confirm the plan's
  // spec_ref really is over the limit — if this assertion ever fails the
  // rest of the test is meaningless.
  assert.ok(overLimitSpec.length > BOW_COLUMN_MAX_LEN.spec_ref);

  const result = bowCli(['import', planFile]);
  // GREEN: the import completes (exit 0), never dies mid-run.
  assert.equal(result.status, 0, `import with an over-limit spec_ref must still complete: ${result.stderr}`);
  assert.match(result.stdout, /Import complete/);
  // A warning naming the truncated field must be printed so the source plan can be fixed.
  assert.match(result.stderr, /spec_ref/);
  assert.match(result.stderr, /truncated/i);

  const [rows] = await db.query('SELECT spec_ref FROM bow_items WHERE mkey = ?', ['test.import.col.overflow']);
  assert.equal(rows.length, 1, 'the item must have been written despite the over-limit field');
  assert.equal(rows[0].spec_ref.length, BOW_COLUMN_MAX_LEN.spec_ref, 'stored spec_ref must be truncated to exactly the column limit');
  assert.equal(rows[0].spec_ref.endsWith('…'), true, 'truncated spec_ref must end in an ellipsis');
});

test('GREEN: bulk `import` with an at-limit title (exactly maxLen chars) is stored byte-identical, not truncated', async () => {
  const atLimitTitle = 'T'.repeat(BOW_COLUMN_MAX_LEN.title);
  const plan = { items: [{ mkey: 'test.import.col.atlimit', type: 'feature', title: atLimitTitle }] };
  const tmp = mkTempDir('bow-columns-import-atlimit-');
  const planFile = path.join(tmp, 'plan.json');
  fs.writeFileSync(planFile, JSON.stringify(plan), 'utf8');

  const result = bowCli(['import', planFile]);
  assert.equal(result.status, 0, `import must succeed: ${result.stderr}`);
  assert.doesNotMatch(result.stderr, /truncated/i, 'an at-limit value must never trigger a truncation warning');

  const [rows] = await db.query('SELECT title FROM bow_items WHERE mkey = ?', ['test.import.col.atlimit']);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].title, atLimitTitle, 'at-limit title must be stored byte-identical, not truncated');
});
