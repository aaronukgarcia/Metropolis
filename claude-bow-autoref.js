/**
 * PostToolUse hook — BOW commit-hash auto-ref (BOW mkey: tool.bow / MOD-007).
 *
 * Spec: M0-ENG §4/§5 ("...auto-comments the commit hash onto the entity via
 * `bow`"); docs/planning/acceptance/tool.bow.md AC-8/AC-9/AC-11/AC-15.
 *
 * Fires after every Bash/PowerShell tool call. When the just-run command was
 * a `git commit` that actually landed, reads the real, just-made commit via
 * `git log -1 --format=%H%x1f%s%x1f%B` (never trusts the tool's captured
 * stdout for the hash — the working tree is the source of truth), extracts
 * `[mkey]`/`[CODE]` tags from the full commit message, resolves each one via
 * claude-bow.js's own canonical `findItemByRef(db, ref)` (never a bespoke
 * query — BUG-003 was exactly this hook reimplementing that lookup with
 * drift), and for each tag that resolves, INSERTs a `bow_git_refs` row — the
 * exact same statement claude-bow.js's own `ref` command runs (item_guid,
 * hash, current branch, note "auto-ref (hook)"). This is the ONE sanctioned
 * direct DB *write* among this project's hooks; everything else (including
 * claude-bow-ref-check.js) is read-only.
 *
 * Idempotent (AC-9): before inserting, checks for an existing
 * (item_guid, commit_hash) row and skips if already present — safe to run
 * twice for the same commit (hook re-invocation, retried tool call).
 *
 * Never mutates anything but bow_git_refs (AC-15) — in particular it never
 * touches an item's `status`; marking something `done` remains a deliberate
 * `node claude-bow.js done` action.
 *
 * Failure posture: this runs AFTER the commit has already landed, so nothing
 * here can be "fail-open" or "fail-closed" in the blocking sense — it simply
 * can never block or undo anything further (AC-11). Every failure (DB down,
 * query error, git error) is caught, appended to claude-bow-autoref.log
 * (repo root, gitignored via the existing `*.log` rule), and swallowed —
 * silent to the user/session, never surfaced as a denial or thrown error.
 *
 * Receives JSON on stdin: { tool: "Bash"|"PowerShell", tool_input: { command },
 *   tool_response / tool_output: { stdout, ... } }
 * Always exits 0. Never writes a permissionDecision — PostToolUse hooks
 * don't block.
 *
 * Sits in .claude/settings.json's PostToolUse Bash matcher (after
 * claude-reflection.js) and a new PostToolUse PowerShell matcher (mirroring
 * how claude-bow-ref-check.js was added to both PreToolUse matchers).
 */

'use strict';

const fs = require('fs');
const path = require('path');
const { execSync } = require('child_process');
const mysql = require('mysql2/promise');
const { findItemByRef } = require('./claude-bow.js');

const ROOT = __dirname;
const LOG_PATH = path.join(ROOT, 'claude-bow-autoref.log');
const TAG_RE = /\[([^\[\]\n]+)\]/g;

function log(line) {
  try {
    const stamp = new Date().toISOString();
    fs.appendFileSync(LOG_PATH, `[${stamp}] ${line}\n`, 'utf8');
  } catch { /* logging must never throw */ }
}

function extractTags(message) {
  const tags = [];
  TAG_RE.lastIndex = 0;
  let m;
  while ((m = TAG_RE.exec(message))) {
    const tag = m[1].trim();
    if (tag) tags.push(tag);
  }
  return tags;
}

async function connectReadWrite() {
  return mysql.createConnection({
    host: process.env.METRO_DB_HOST || '127.0.0.1',
    port: Number(process.env.METRO_DB_PORT || 3306),
    user: process.env.METRO_DB_USER || 'root',
    password: process.env.METRO_DB_PASSWORD || '',
    database: process.env.METRO_DB_NAME || 'metro',
    connectTimeout: 4000,
  });
}

/** Has this (item_guid, commit_hash) pair already been ref'd? */
async function refExists(db, itemGuid, hash) {
  const [rows] = await db.query(
    'SELECT id FROM bow_git_refs WHERE item_guid = ? AND commit_hash = ? LIMIT 1',
    [itemGuid, hash.toLowerCase()]
  );
  return rows.length > 0;
}

/** Insert a bow_git_refs row for (item_guid, hash) unless it already exists. Returns 'inserted' | 'skipped-duplicate'. */
async function insertRefIdempotent(db, itemGuid, hash, branch, note) {
  if (await refExists(db, itemGuid, hash)) {
    return 'skipped-duplicate';
  }
  try {
    await db.query(
      'INSERT INTO bow_git_refs (item_guid, commit_hash, branch, note) VALUES (?, ?, ?, ?)',
      [itemGuid, hash.toLowerCase(), branch || null, note || null]
    );
    return 'inserted';
  } catch (err) {
    // A duplicate-key race (two hook invocations for the same commit landing
    // concurrently) is the one error class we swallow as harmless — anything
    // else propagates to the caller's catch-and-log.
    if (err && (err.code === 'ER_DUP_ENTRY' || /duplicate/i.test(err.message || ''))) {
      return 'skipped-duplicate';
    }
    throw err;
  }
}

/** Core auto-ref logic for a single landed commit. Exported for direct
 *  testing with a fake hash/subject — no real git commit required. */
async function autoRefForCommit(db, { hash, message, branch }) {
  const tags = extractTags(message || '');
  const results = [];
  for (const tag of tags) {
    // eslint-disable-next-line no-await-in-loop
    const item = await findItemByRef(db, tag);
    if (!item) {
      results.push({ tag, status: 'unknown-tag' });
      continue;
    }
    // eslint-disable-next-line no-await-in-loop
    const outcome = await insertRefIdempotent(db, item.guid, hash, branch, 'auto-ref (hook)');
    results.push({ tag, code: item.code, status: outcome });
  }
  return results;
}

function currentBranch() {
  try {
    return execSync('git rev-parse --abbrev-ref HEAD', { cwd: ROOT, encoding: 'utf8', timeout: 5000 }).trim();
  } catch {
    return null;
  }
}

/** Read the real, just-landed commit from the working tree (never trusts
 *  captured tool stdout for the hash — see header). Returns null if HEAD
 *  isn't a commit we should trust (e.g. not in a repo). */
function readHeadCommit() {
  try {
    const raw = execSync('git log -1 --format=%H%x1f%B', { cwd: ROOT, encoding: 'utf8', timeout: 5000 });
    const sep = raw.indexOf('\x1f');
    if (sep === -1) return null;
    const hash = raw.slice(0, sep).trim();
    const message = raw.slice(sep + 1);
    if (!/^[0-9a-f]{7,40}$/i.test(hash)) return null;
    return { hash, message };
  } catch {
    return null;
  }
}

async function main() {
  let input = '';
  process.stdin.setEncoding('utf8');
  for await (const chunk of process.stdin) input += chunk;

  let command = '';
  try {
    const data = JSON.parse(input.replace(/^﻿/, ''));
    command = data?.tool_input?.command ?? '';
  } catch {
    // Unparseable input — nothing to do, never block anything (PostToolUse).
    process.exit(0);
    return;
  }

  if (!/(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/.test(command)) {
    process.exit(0);
    return;
  }
  if (command.includes('--amend')) {
    // Amending rewrites the hash out from under us in a way that's ambiguous
    // to auto-ref safely; leave it to a manual `node claude-bow.js ref`.
    process.exit(0);
    return;
  }

  const head = readHeadCommit();
  if (!head) {
    log('SKIP: could not read HEAD commit (not a repo, or git log failed) — commit may not have landed.');
    process.exit(0);
    return;
  }

  const tags = extractTags(head.message);
  if (tags.length === 0) {
    process.exit(0);
    return;
  }

  let db;
  try {
    db = await connectReadWrite();
  } catch (err) {
    // AC-11: DB unreachable post-commit — log only, never block/undo anything.
    log(`FAIL: DB unreachable for HEAD ${head.hash} — ${err.message}`);
    process.exit(0);
    return;
  }

  try {
    const branch = currentBranch();
    const results = await autoRefForCommit(db, { hash: head.hash, message: head.message, branch });
    for (const r of results) {
      if (r.status === 'unknown-tag') {
        log(`WARN: HEAD ${head.hash} tag [${r.tag}] does not resolve to a live BOW item — no ref written.`);
      } else {
        log(`OK: HEAD ${head.hash} -> ${r.code} [${r.tag}] (${r.status})`);
      }
    }
  } catch (err) {
    log(`FAIL: auto-ref for HEAD ${head.hash} threw — ${err && err.stack ? err.stack : err}`);
  } finally {
    try { await db.end(); } catch { /* ignore */ }
  }

  process.exit(0);
}

if (require.main === module) {
  main().catch((err) => {
    log(`FAIL: uncaught error in main() — ${err && err.stack ? err.stack : err}`);
    process.exit(0);
  });
}

module.exports = {
  extractTags,
  findItemByRef,
  refExists,
  insertRefIdempotent,
  autoRefForCommit,
  readHeadCommit,
  connectReadWrite,
};
