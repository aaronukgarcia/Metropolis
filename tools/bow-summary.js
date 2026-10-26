#!/usr/bin/env node
// forge:script.safe.bow-summary.js@2
// bow-summary.js - short read-only view of a light-schema BOW table (schema/admin.sql `bow`), for /bye, /update and /health-check.
// Aaron's own items first (his queue leads every session), then the open P0/P1 queue, then counts.
// Usage: node tools/bow-summary.js [--db safe] [--limit 8]
// Database: METRO_DB_NAME or --db (default safe). Read-only: it never writes.
// SAFE-215 (2026-10-20): shipped by forge to every project (profile base) because /bye and /health-check call it, so it must
// run where there is no node_modules (mysql2 comes from SAFE's checkout, as vestige-pointer.js does) and it must not crash a
// project whose BOW is not the light table (todo: Metropolis-style bow_items): then it prints one line and exits 0.
'use strict';
const path = require('path');
const SAFE_HOME = (process.env.SAFE_HOME || 'E:/projects/63-SAFE').replace(/\\/g, '/');
const mysql = (() => {
  try { return require(require.resolve('mysql2/promise', { paths: [process.cwd(), path.join(__dirname, '..')] })); } catch (e) {
    if (e.code !== 'MODULE_NOT_FOUND') throw e;
    try { return require(path.join(SAFE_HOME, 'node_modules', 'mysql2', 'promise')); } catch {
      console.error(`bow-summary: mysql2 not found here or under ${SAFE_HOME}/node_modules (set SAFE_HOME to the SAFE checkout, or npm install mysql2).`);
      process.exit(1);
    }
  }
})();

const arg = (k, d) => { const i = process.argv.indexOf(k); return i > -1 ? process.argv[i + 1] : d; };
const db = arg('--db', process.env.METRO_DB_NAME || 'safe');
const limit = Number(arg('--limit', 8));

(async () => {
  const c = await mysql.createConnection({ host: 'localhost', user: 'root', database: db });
  try {
    const [[t]] = await c.query("SELECT COUNT(*) n FROM information_schema.tables WHERE table_schema=? AND table_name='bow'", [db]);
    if (!t.n) { console.log(`BOW (${db}): no light bow table here; this project keeps its BOW elsewhere (claude-bow.js, or none yet)`); return; }
    const open = "status NOT IN ('done','dropped')";
    const [mine] = await c.query(`SELECT code, priority, status, title FROM bow WHERE owner='Aaron' AND ${open} ORDER BY priority, created_at`);
    const [top] = await c.query(`SELECT code, priority, owner, status, title, depends_on FROM bow WHERE owner<>'Aaron' AND ${open} AND priority IN ('P0','P1') ORDER BY priority, status='in_progress' DESC, code LIMIT ?`, [limit]);
    const [cnt] = await c.query(`SELECT priority, COUNT(*) n FROM bow WHERE ${open} GROUP BY priority ORDER BY priority`);
    const [wip] = await c.query("SELECT code, title FROM bow WHERE status='in_progress' ORDER BY code");
    const line = r => `  ${r.code} ${r.priority}${r.owner ? ' ' + r.owner : ''} [${r.status}] ${r.title.slice(0, 90)}${r.depends_on ? ' (after ' + r.depends_on + ')' : ''}`;
    console.log(`BOW (${db}): open ${cnt.map(r => r.priority + '=' + r.n).join(' ') || 'none'}`);
    console.log(`Aaron's queue (${mine.length}):`); mine.forEach(r => console.log(line(r)));
    console.log(`In progress (${wip.length}):`); wip.forEach(r => console.log(`  ${r.code} ${r.title.slice(0, 90)}`));
    console.log(`Top P0/P1 (first ${limit}):`); top.forEach(r => console.log(line(r)));
  } finally { await c.end(); }
})().catch(e => { console.error('bow-summary: ' + e.message); process.exit(1); });
