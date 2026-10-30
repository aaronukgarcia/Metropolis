#!/usr/bin/env node
// forge:script.safe.library-find.js@1
// library-find.js - ask SAFE's central file library whether a tool already exists before you build one (SAFE-520).
// Searches safe.library_file (path and purpose line) and safe.library_text (whole-file full-text index of py js ts mjs cjs ps1
// bat sh sql files in every registered project), filled nightly by safe-library.js.
// Usage: node tools/library-find.js <words...> [--ext py,js] [--project slug] [--limit 20] [--json]
// Aaron's rule (2026-10-29): run this before building any new script or tool. A hit is reused, or its owner is asked (a
// question.md note) to promote it into forge so every project gets it. Read-only. Works from any cwd and any project.
// Shared asset: fix it here, not in a project copy. mysql2 resolves from SAFE_HOME (default E:/projects/63-SAFE) like
// bow-summary.js; when the database is down or the tables are missing it prints one line and exits 0 (never breaks a session).
'use strict';
const path = require('path');

// ---- pure helpers (tested with a fake query) ----
function parseArgs(argv) {
  const o = { words: [], ext: [], project: null, limit: 20, json: false };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--json') o.json = true;
    else if (a === '--ext') o.ext = String(argv[++i] || '').toLowerCase().split(',').map((s) => s.replace(/^\./, '').trim()).filter(Boolean);
    else if (a === '--project') o.project = argv[++i] || null;
    else if (a === '--limit') o.limit = Math.max(1, Number(argv[++i]) || 20);
    else if (!a.startsWith('--')) o.words.push(...a.split(/\s+/).filter(Boolean));
  }
  return o;
}
const clean = (w) => w.replace(/[+\-<>()~*"@]/g, ' ').trim().split(/\s+/).filter((x) => x.length > 2);   // InnoDB full-text drops tokens under 3 chars, so "to" would only add noise
const likeEsc = (w) => '%' + w.replace(/[\\%_]/g, (c) => '\\' + c) + '%';

function pathQuery(o, words) {
  const where = []; const p = [];
  for (const w of words) { where.push('(f.path LIKE ? OR f.description LIKE ?)'); p.push(likeEsc(w), likeEsc(w)); }
  const filt = filters(o, p);
  return { sql: `SELECT f.project, f.path, f.description, f.description_source, (f.path LIKE ?) AS inpath FROM library_file f WHERE ${where.join(' AND ')}${filt} LIMIT ?`,
    params: [likeEsc(words[0]), ...p, o.limit * 5] };
}
function filters(o, p) {
  let s = '';
  if (o.ext.length) { s += ` AND f.ext IN (${o.ext.map(() => '?').join(',')})`; p.push(...o.ext); }
  if (o.project) { s += ' AND f.project=?'; p.push(o.project); }
  return s;
}
function textQuery(o, words, mode) {
  const p = [];
  const against = mode === 'boolean' ? words.map((w) => '+' + w).join(' ') : words.join(' ');
  const filt = filters(o, p);
  return { sql: `SELECT f.project, f.path, f.description, f.description_source, MATCH(t.body) AGAINST (? ${mode === 'boolean' ? 'IN BOOLEAN MODE' : 'IN NATURAL LANGUAGE MODE'}) AS score FROM library_text t JOIN library_file f ON f.id=t.file_id WHERE MATCH(t.body) AGAINST (? ${mode === 'boolean' ? 'IN BOOLEAN MODE' : 'IN NATURAL LANGUAGE MODE'})${filt} ORDER BY score DESC LIMIT ?`,
    params: [against, against, ...p, o.limit * 2] };
}

// q(sql, params) -> rows. Returns ranked hits: path/purpose hits first, then full-text by relevance.
async function find(q, o) {
  const words = o.words.flatMap(clean);
  if (!words.length) return { hits: [], words };
  const seen = new Map();
  const add = (r, source, rank) => {
    const k = r.project + '|' + r.path;
    if (seen.has(k)) { const h = seen.get(k); if (!h.sources.includes(source)) h.sources.push(source); return; }
    seen.set(k, { project: r.project, path: r.path, purpose: r.description || null, sources: [source], rank });
  };
  const pr = await q(...Object.values(pathQuery(o, words)));
  for (const r of pr) add(r, r.inpath ? 'path' : 'purpose', 0);
  let tr = await q(...Object.values(textQuery(o, words, 'boolean')));
  if (!tr.length) tr = await q(...Object.values(textQuery(o, words, 'natural')));
  tr.forEach((r, i) => add(r, 'text', 1 + i / 1000));
  const hits = [...seen.values()].sort((a, b) => a.rank - b.rank || a.project.localeCompare(b.project) || a.path.localeCompare(b.path)).slice(0, o.limit);
  return { hits, words };
}

const line = (h) => `${h.project}  ${h.path}  | ${h.purpose || 'no purpose line'}  | source: ${h.sources.join('+')}`;
function render(hits, totals) {
  if (!hits.length) return [`no match in the library (${totals.files} files, ${totals.projects} projects); build it, describe it in the header`];
  return [...hits.map(line), `${hits.length} hits in ${hits.length} files; reuse it or ask the owner to promote it into forge (GGR practice, SAFE-520)`];
}

// ---- main ----
function loadMysql() {
  const home = (process.env.SAFE_HOME || 'E:/projects/63-SAFE').split(String.fromCharCode(92)).join('/');
  for (const f of [() => require(require.resolve('mysql2/promise', { paths: [process.cwd(), path.join(__dirname, '..')] })), () => require(path.join(home, 'node_modules', 'mysql2', 'promise'))]) {
    try { return f(); } catch (e) { if (e.code !== 'MODULE_NOT_FOUND') throw e; }
  }
  return null;
}

async function main() {
  const o = parseArgs(process.argv.slice(2));
  if (!o.words.length) { console.log('usage: node tools/library-find.js <words...> [--ext py,js] [--project slug] [--limit 20] [--json]'); return; }
  const mysql = loadMysql();
  if (!mysql) { console.log(`library-find: mysql2 not found (set SAFE_HOME to the SAFE checkout); skipping the library check`); return; }
  let c;
  try {
    c = await mysql.createConnection({ host: '127.0.0.1', user: 'root', database: 'safe', connectTimeout: 5000 });
    const q = async (sql, p) => (await c.query(sql, p))[0];
    const { hits } = await find(q, o);
    const [t] = await q('SELECT COUNT(*) files, COUNT(DISTINCT project) projects FROM library_file');
    if (o.json) console.log(JSON.stringify({ hits, files: t.files, projects: t.projects }, null, 1));
    else console.log(render(hits, { files: Math.round(t.files / 1000) + 'k', projects: t.projects }).join('\n'));
  } catch (e) {
    console.log(`library-find: library unavailable (${String(e.code || e.message).slice(0, 80)}); skipping the library check`);
  } finally { if (c) await c.end().catch(() => {}); }
}

if (require.main === module) main().catch((e) => { console.log('library-find: ' + e.message); });
module.exports = { parseArgs, find, pathQuery, textQuery, render, line };
