#!/usr/bin/env node
// forge:script.safe.lib-catalog.js@3
// Per-project file library catalog (SAFE-432). Maintains `<slug>-lib.json` at the project root: one entry per
// tracked file naming what it is for. `build` creates/refreshes the entry list (adds new files, drops removed
// ones, keeps existing descriptions, seeds a first guess from the file's own header/frontmatter where there is
// one); the real author (the /update or /bye session, i.e. Claude) fills every entry left `description: null`
// with a genuine sentence, then `log` appends a timestamped, honest record of the run — "ok" only when nothing
// is left null, "pending" otherwise. Shared asset (forge `script.safe.lib-catalog.js`): fix it here, not in a
// project copy.
'use strict';
const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');

function arg(name, def) {
  const i = process.argv.indexOf(name);
  return i !== -1 && process.argv[i + 1] ? process.argv[i + 1] : def;
}

const ROOT = process.cwd();

function defaultSlug() {
  const base = path.basename(ROOT);
  const stripped = base.replace(/^\d+[-_]/, '').toLowerCase().replace(/[^a-z0-9]+/g, '');
  return stripped || 'project';
}

const SLUG = arg('--slug', defaultSlug());
const CATALOG_PATH = path.join(ROOT, `${SLUG}-lib.json`);

const EXCLUDE_DIR_RE = /(^|[\\/])(\.git|node_modules|temp|_sessions|\.claude[\\/]worktrees|quarantine)([\\/]|$)/i;

function isGitRepo() {
  try { execFileSync('git', ['rev-parse', '--is-inside-work-tree'], { cwd: ROOT, stdio: 'ignore' }); return true; }
  catch { return false; }
}

function trackedFiles() {
  if (isGitRepo()) {
    // -z: NUL-separated, unquoted UTF-8 paths (plain ls-files quotes and octal-escapes non-ASCII names).
    const out = execFileSync('git', ['ls-files', '-z'], { cwd: ROOT, encoding: 'utf8', maxBuffer: 256 * 1024 * 1024 });
    return out.split('\0').filter(Boolean).filter((f) => !EXCLUDE_DIR_RE.test(f));
  }
  // Non-git project (e.g. a Photos-style folder): walk the tree ourselves.
  const out = [];
  (function walk(dir) {
    for (const name of fs.readdirSync(dir)) {
      const abs = path.join(dir, name);
      const rel = path.relative(ROOT, abs).split(path.sep).join('/');
      if (EXCLUDE_DIR_RE.test(rel)) continue;
      const st = fs.statSync(abs);
      if (st.isDirectory()) walk(abs);
      else out.push(rel);
    }
  })(ROOT);
  return out;
}

function readSafe(file) {
  try { return fs.readFileSync(path.join(ROOT, file), 'utf8'); } catch { return null; }
}

// A best-effort first guess only: the real description is written by the session that runs build (SAFE-432).
// Never treated as "done" by itself — `log` still reports a file pending until a human/LLM pass confirms it.
function guessDescription(file, text) {
  if (text == null) return null;
  if (/\.md$/i.test(file)) {
    const fm = text.match(/^---\r?\n([\s\S]*?)\r?\n---/);
    if (fm) {
      const m = fm[1].match(/^description:\s*(.+)$/m);
      if (m) return m[1].trim().slice(0, 240);
    }
    const h = text.match(/^#\s+(.+)$/m);
    if (h) return h[1].trim().slice(0, 240);
    return null;
  }
  if (/\.json$/i.test(file)) {
    try {
      const j = JSON.parse(text);
      const d = j.description || j._comment || j._why || j._what || j._kit;
      if (d) return String(d).slice(0, 240);
    } catch { /* not JSON, or no such key */ }
    return null;
  }
  const body = text.replace(/^#!.*\r?\n/, ''); // drop a shebang before looking for the header comment
  if (/\.(py)$/i.test(file) || /^#!.*python/.test(text)) {
    const doc = body.match(/^\s*("""|''')([\s\S]*?)\1/);
    if (doc) { const joined = doc[2].split(/\r?\n/)[0].trim() || doc[2].replace(/\s+/g, ' ').trim(); if (joined) return joined.slice(0, 240); }
  }
  if (/\.(js|mjs|cjs|ts)$/i.test(file) || /^#!.*node/.test(text)) {
    const block = body.match(/^\s*\/\*\*?([\s\S]*?)\*\//);
    if (block) {
      const joined = block[1].split(/\r?\n/).map((l) => l.replace(/^\s*\*?\s?/, '')).filter((l) => l.trim()).join(' ').replace(/\s+/g, ' ').trim();
      if (joined) return joined.slice(0, 240);
    }
    const lines = body.split(/\r?\n/);
    const comment = [];
    for (const l of lines.slice(0, 20)) {
      const m = l.match(/^\s*\/\/\s?(.*)$/);
      if (m) comment.push(m[1]); else if (comment.length) break; else if (l.trim() === '') continue; else break;
    }
    const joined = comment.join(' ').replace(/\s+/g, ' ').trim();
    return joined ? joined.slice(0, 240) : null;
  }
  const hashCommented = /\.(sh|bat|ps1|sql)$/i.test(file) || /^#!.*sh/.test(text);
  if (hashCommented) {
    const marker = /\.(bat|ps1)$/i.test(file) ? /^\s*(?:::|REM)\s?(.*)$/i : /\.sql$/i.test(file) ? /^\s*--\s?(.*)$/ : /^\s*#\s?(.*)$/;
    const lines = body.split(/\r?\n/);
    const comment = [];
    for (const l of lines.slice(0, 20)) {
      const m = l.match(marker);
      if (m) comment.push(m[1]); else if (comment.length) break; else if (l.trim() === '') continue; else break;
    }
    const joined = comment.join(' ').replace(/\s+/g, ' ').trim();
    return joined ? joined.slice(0, 240) : null;
  }
  return null;
}

function loadCatalog() {
  if (!fs.existsSync(CATALOG_PATH)) return { project: SLUG, generated_by: 'tools/lib-catalog.js', updated: null, files: {}, log: [] };
  const j = JSON.parse(fs.readFileSync(CATALOG_PATH, 'utf8'));
  j.files = j.files || {}; j.log = j.log || [];
  return j;
}

function saveCatalog(cat) {
  cat.updated = new Date().toISOString();
  fs.writeFileSync(CATALOG_PATH, JSON.stringify(cat, null, 2) + '\n', 'utf8');
}

function pendingCount(cat) {
  return Object.values(cat.files).filter((e) => e.description === null || e.description === undefined).length;
}

function cmdBuild() {
  const cat = loadCatalog();
  const files = new Set(trackedFiles());
  const reseed = process.argv.includes('--reseed-pending');
  let added = 0, removed = 0, seeded = 0;
  for (const f of Object.keys(cat.files)) if (!files.has(f)) { delete cat.files[f]; removed++; }
  for (const f of files) {
    if (cat.files[f] && !(reseed && !cat.files[f].description)) continue;
    const guess = guessDescription(f, readSafe(f));
    const already = cat.files[f];
    cat.files[f] = { description: guess, updated: new Date().toISOString().slice(0, 10) };
    if (already) seeded += guess ? 1 : 0; else { added++; if (guess) seeded++; }
  }
  saveCatalog(cat);
  const pending = pendingCount(cat);
  console.log(`lib-catalog: ${CATALOG_PATH}`);
  console.log(`  files: ${Object.keys(cat.files).length} | added: ${added} (${seeded} seeded from header/frontmatter) | removed: ${removed} | pending description: ${pending}`);
  if (pending) console.log(`  next: fill the ${pending} null description(s) (node tools/lib-catalog.js status --limit 50 lists them), then run "log"`);
}

function cmdStatus() {
  const cat = loadCatalog();
  const limit = Number(arg('--limit', '20'));
  const pending = Object.entries(cat.files).filter(([, e]) => !e.description).map(([f]) => f);
  console.log(`lib-catalog: ${CATALOG_PATH}`);
  console.log(`  files: ${Object.keys(cat.files).length} | pending description: ${pending.length}`);
  for (const f of pending.slice(0, limit)) console.log(`  pending: ${f}`);
  if (pending.length > limit) console.log(`  ...and ${pending.length - limit} more`);
}

function cmdLog() {
  const by = arg('--by', null);
  if (!by) { console.error('lib-catalog log: --by <update|bye|...> is required'); process.exitCode = 1; return; }
  if (!fs.existsSync(CATALOG_PATH)) { console.error(`lib-catalog log: no ${CATALOG_PATH} yet; run "build" first`); process.exitCode = 1; return; }
  const cat = loadCatalog();
  const pending = pendingCount(cat);
  const total = Object.keys(cat.files).length;
  const status = pending === 0 ? 'ok' : 'pending';
  cat.log.push({ at: new Date().toISOString(), by, status, files: total, pending });
  saveCatalog(cat);
  console.log(`lib-catalog: logged ${by} run (${status}) — ${total} files, ${pending} pending`);
}

const cmd = process.argv[2];
if (cmd === 'build') cmdBuild();
else if (cmd === 'status') cmdStatus();
else if (cmd === 'log') cmdLog();
else { console.log('usage: node tools/lib-catalog.js build|status|log --by <update|bye> [--slug <slug>] [--limit <n>]'); process.exitCode = cmd ? 1 : 0; }
