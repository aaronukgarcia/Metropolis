#!/usr/bin/env node
// forge:script.safe.vestige-pointer.js@3
/**
 * vestige-pointer.js — the recall bridge (SAFE-064, Aaron 2026-10-06).
 *
 * Vestige 4.x (Strata) removed free-text recall: you can only pull a memory by
 * exact id or exact tag. This keeps an external FULLTEXT index over a Vestige
 * export so a session can FIND a half-remembered memory by free text, get its
 * {mem_id, tags, gist}, then PULL the authoritative memory from Strata by that
 * id/tag (find-then-pull). The index is never a source of truth.
 *
 * Commands
 *   init                                 apply schema/pointer.sql
 *   index <dump.jsonl> [--truncate] [--scope s]
 *                                        (re)build the index from a Vestige export; --scope stamps rows the
 *                                        export left scope-less (pair with a per-scope export)
 *   find "<query>" [--scope s] [--all] [--limit n] [--json]
 *                                        free-text search -> pointer hits to pull; rows in --scope rank first
 *   stats                                row counts and last build
 *
 * DB: MariaDB `safe` (localhost, root, no password). Override with SAFE_DB_* env.
 * The dump is produced by mcp__vestige__maintain { action: "export", format: "jsonl" [, scope ] } on 4.x
 * (it lands under %APPDATA%\vestige\core\data-v4\exports\); the 4.1.1 exe has no CLI, so the refresh is a
 * session step (/update), not a nightly job. The indexer also accepts the sqlite-shaped export
 * (id/content/tags/scope/...) used pre-4.x. Runs from any project: mysql2 is resolved from SAFE_HOME.
 */
'use strict';
const fs = require('fs');
const path = require('path');
// SAFE-081: this file is shipped alone as a forge asset (script.safe.vestige-pointer.js), so a project
// that installs it has no package.json or node_modules. Resolve mysql2 from the installing project first,
// then from SAFE's own checkout (SAFE_HOME or the default location), so `node vestige-pointer.js` works
// anywhere without a shim. The shared index lives in SAFE's MariaDB whichever copy of this script runs.
const SAFE_HOME = (process.env.SAFE_HOME || 'E:/projects/63-SAFE').replace(/\\/g, '/');
const mysql = (() => {
  try { return require('mysql2/promise'); } catch (e) {
    if (e.code !== 'MODULE_NOT_FOUND') throw e;
    const alt = path.join(SAFE_HOME, 'node_modules', 'mysql2', 'promise');
    try { return require(alt); } catch {
      console.error(`vestige-pointer: mysql2 not found here or under ${SAFE_HOME}/node_modules (set SAFE_HOME to the SAFE checkout, or npm install mysql2).`);
      process.exit(1);
    }
  }
})();

const DB = {
  host: process.env.SAFE_DB_HOST || 'localhost',
  port: Number(process.env.SAFE_DB_PORT || 3306),
  user: process.env.SAFE_DB_USER || 'root',
  password: process.env.SAFE_DB_PASSWORD || '',
  database: process.env.SAFE_DB_NAME || 'safe',
};

function args() {
  const a = process.argv.slice(2); const pos = []; const opt = {};
  for (let i = 0; i < a.length; i++) {
    if (a[i].startsWith('--')) { const k = a[i].slice(2); const nx = a[i + 1]; if (nx !== undefined && !nx.startsWith('--')) { opt[k] = nx; i++; } else opt[k] = true; }
    else pos.push(a[i]);
  }
  return { pos, opt };
}
const conn = () => mysql.createConnection({ ...DB, multipleStatements: true });

// SAFE-081: the 4.1.1 `maintain export` row carries NO scope field (Strata tracks scope server-side, the
// export drops it), so every post-cutover memory was landing in the index as 'user' and `find --scope X`
// could not prefer X. Two repairs: (a) `index --scope X` stamps every scope-less row (pair it with a
// per-scope export: maintain export { scope: X }); (b) a tag -> scope map infers the scope from the
// project tags Aaron's sessions already use. A row that has its own scope is never overridden.
const TAG_SCOPE = {
  'book:gf': 'ghostflux', 'ghost-flux': 'ghostflux', 'ghostflux': 'ghostflux', 'ghost_flux': 'ghostflux',
  'book:rf': 'residualflux', 'residual-flux': 'residualflux', 'residualflux': 'residualflux',
  'book:fc': 'finalcollapse', 'final-collapse': 'finalcollapse', 'finalcollapse': 'finalcollapse',
  'safe': 'safe', 'metropolis': 'metropolis', 'metro': 'metropolis', 'minvoice': 'minvoice', 'forge': 'forge',
  'sitrep': 'sitrep', 'tax': 'tax', 'cleanemail': 'cleanemail', 'securenotes': 'securenotes', 'sunset': 'sunset',
  'photo': 'photo', 'photocurator': 'photocurator', 'prix6': 'prix6', 'audit-project': 'audit', 'audit-session': 'audit',
  'marula-factory': 'marula-factory', 'papers': 'papers', 'clifton': '61-JohnnyClifton',
};
function inferScope(tags) {
  for (const t of tags) { const s = TAG_SCOPE[String(t).toLowerCase()]; if (s) return s; }
  for (const t of tags) { const m = /^(?:scope|project):(.+)$/i.exec(String(t)); if (m) return m[1].toLowerCase(); }
  return null;
}

// Normalise one export row (sqlite-shaped or 4.x maintain-export-shaped) to the index row.
function norm(o, forceScope) {
  const id = o.id || o.mem_id || o.memory_id || o.nodeId;
  if (!id) return null;
  const content = o.content ?? o.text ?? '';
  let tags = o.tags;
  if (typeof tags === 'string') { try { tags = JSON.parse(tags); } catch { tags = tags.split(/[\s,]+/); } }
  if (!Array.isArray(tags)) tags = tags ? [String(tags)] : [];
  const tagStr = tags.filter(Boolean).join(' ');
  const gist = String(content).replace(/\s+/g, ' ').trim().slice(0, 300);
  const d = (x) => { if (!x) return null; const s = String(x).replace('T', ' ').replace(/\.\d+.*$/, '').replace('Z', ''); return s.slice(0, 19) || null; };
  return {
    mem_id: String(id), scope: o.scope || forceScope || inferScope(tags) || 'user', node_type: o.node_type || o.nodeType || null,
    tags: tagStr, gist, content: String(content),
    created_at: d(o.created_at || o.createdAt), updated_at: d(o.updated_at || o.updatedAt),
    valid_from: d(o.valid_from || o.validFrom), valid_until: d(o.valid_until || o.validUntil),
    superseded: (o.superseded_by || o.supersededBy || o.superseded) ? 1 : 0,
  };
}

async function cmdInit() {
  const sql = fs.readFileSync(path.join(__dirname, 'schema', 'pointer.sql'), 'utf8');
  const c = await conn(); await c.query(sql); await c.end();
  console.log('vestige-pointer: schema applied (vestige_pointer, vestige_pointer_build).');
}

async function cmdIndex(file, opt) {
  if (!file || !fs.existsSync(file)) { console.error('index: dump file not found: ' + file); process.exit(1); }
  const lines = fs.readFileSync(file, 'utf8').split('\n').filter(l => l.trim());
  const rows = []; let bad = 0;
  const forceScope = typeof opt.scope === 'string' ? opt.scope : null;
  for (const l of lines) { try { const n = norm(JSON.parse(l), forceScope); if (n) rows.push(n); else bad++; } catch { bad++; } }
  const c = await conn();
  if (opt.truncate) await c.query('TRUNCATE TABLE vestige_pointer');
  const cols = ['mem_id', 'scope', 'node_type', 'tags', 'gist', 'content', 'created_at', 'updated_at', 'valid_from', 'valid_until', 'superseded'];
  const upd = cols.filter(k => k !== 'mem_id').map(k => `${k}=VALUES(${k})`).join(', ');
  let n = 0;
  for (let i = 0; i < rows.length; i += 500) {
    const batch = rows.slice(i, i + 500);
    const ph = batch.map(() => '(' + cols.map(() => '?').join(',') + ')').join(',');
    const vals = batch.flatMap(r => cols.map(k => r[k]));
    await c.execute(`INSERT INTO vestige_pointer (${cols.join(',')}) VALUES ${ph} ON DUPLICATE KEY UPDATE ${upd}`, vals);
    n += batch.length;
  }
  const [[{ c: total }]] = await c.query('SELECT COUNT(*) c FROM vestige_pointer');
  await c.execute('INSERT INTO vestige_pointer_build (source, rows_in, rows_index, note) VALUES (?,?,?,?)',
    [file, lines.length, n, bad ? `${bad} unparseable/skipped` : null]);
  await c.end();
  console.log(`vestige-pointer: indexed ${n} rows from ${lines.length} lines (${bad} skipped); table now ${total}.`);
}

async function cmdFind(query, opt) {
  if (!query) { console.error('find: give a query string'); process.exit(1); }
  const limit = Math.min(Number(opt.limit || 10), 50);
  const c = await conn();
  // SAFE-081: the asked-for scope outranks the shared 'user' scope. The old ORDER BY score alone let the
  // 889 user-scope rows bury a project's own memories (ghostflux find returned immich hits first).
  const scope = typeof opt.scope === 'string' ? opt.scope : 'safe';
  const where = ['MATCH(content,tags,gist) AGAINST (? IN NATURAL LANGUAGE MODE)']; const params = [query];
  if (!opt.all) { where.push('(scope = ? OR scope = ?)'); params.push(scope, 'user'); }
  let [rows] = await c.query(
    `SELECT mem_id, scope, node_type, tags, gist, superseded,
            MATCH(content,tags,gist) AGAINST (? IN NATURAL LANGUAGE MODE) AS score
     FROM vestige_pointer WHERE ${where.join(' AND ')}
     ORDER BY (scope = ?) DESC, score DESC LIMIT ${limit}`, [query, ...params, scope]);
  // Fallback: LIKE on tags/gist when FULLTEXT finds nothing (short/rare terms).
  if (!rows.length) {
    const like = '%' + query.replace(/[%_]/g, '') + '%';
    const lw = ['(tags LIKE ? OR gist LIKE ? OR content LIKE ?)']; const lp = [like, like, like];
    if (!opt.all) { lw.push('(scope = ? OR scope = ?)'); lp.push(scope, 'user'); }
    [rows] = await c.query(`SELECT mem_id, scope, node_type, tags, gist, superseded, NULL AS score FROM vestige_pointer WHERE ${lw.join(' AND ')} ORDER BY (scope = ?) DESC LIMIT ${limit}`, [...lp, scope]);
  }
  await c.end();
  if (opt.json) { console.log(JSON.stringify(rows, null, 2)); return; }
  if (!rows.length) { console.log('No pointer hits. Re-dump if the memory is recent, or widen with --all.'); return; }
  console.log(`${rows.length} pointer hit(s) — pull the one you want from Strata by mem_id or a tag:\n`);
  for (const r of rows) {
    console.log(`  ${r.mem_id}  [${r.scope}${r.node_type ? '/' + r.node_type : ''}]${r.superseded ? ' (superseded)' : ''}  score=${r.score == null ? 'like' : Number(r.score).toFixed(3)}`);
    console.log(`    tags: ${r.tags || '-'}`);
    console.log(`    gist: ${r.gist || '-'}`);
    console.log(`    pull: mcp__vestige__recall { handle: "${r.mem_id}" }  (or recall by an exact tag above)\n`);
  }
}

async function cmdStats() {
  const c = await conn();
  const [[{ total }]] = await c.query('SELECT COUNT(*) total FROM vestige_pointer');
  const [byScope] = await c.query('SELECT scope, COUNT(*) n FROM vestige_pointer GROUP BY scope ORDER BY n DESC');
  const [builds] = await c.query('SELECT built_at, source, rows_in, rows_index, note FROM vestige_pointer_build ORDER BY id DESC LIMIT 3');
  await c.end();
  console.log(`vestige_pointer: ${total} rows`);
  console.log('by scope: ' + byScope.map(r => `${r.scope}=${r.n}`).join(', '));
  console.log('recent builds:'); for (const b of builds) console.log(`  ${b.built_at} ${b.source} in=${b.rows_in} idx=${b.rows_index} ${b.note || ''}`);
}

(async () => {
  const { pos, opt } = args();
  const cmd = pos[0];
  try {
    if (cmd === 'init') await cmdInit();
    else if (cmd === 'index') await cmdIndex(pos[1], opt);
    else if (cmd === 'find') await cmdFind(pos.slice(1).filter(x => !x.startsWith('--')).join(' '), opt);
    else if (cmd === 'stats') await cmdStats();
    else { console.log('usage: vestige-pointer.js init | index <dump.jsonl> [--truncate] | find "<query>" [--scope s] [--all] [--limit n] [--json] | stats'); process.exit(1); }
  } catch (e) { console.error('vestige-pointer error:', e.message); process.exit(1); }
})();
