/**
 * claude-bow.js — Metropolis Book of Work (metro MariaDB backend)
 *
 * The BOW is the single source of truth for planned/active work on Metropolis:
 * modules, features, bugs and interfaces. Every item has a GUID (primary key),
 * a short human code (MOD-001 / FEAT-001 / BUG-001 / INT-001), a priority
 * (P0..P3), status, dependency links, comments (optionally carrying example
 * code), and git commit references.
 *
 * Tables (database `metro`, created on first run):
 *   bow_items        guid PK, code UNIQUE, item_type, title, description,
 *                    priority, status, created/updated/closed timestamps
 *   bow_dependencies (item_guid, depends_on_guid) — "item depends on X"
 *   bow_comments     comment body + optional example_code/code_language
 *   bow_git_refs     commit hash + branch + note per item
 *
 * Commands:
 *   init                                  - Create BOW tables (auto-runs on every command)
 *   add <type> "title" [--priority P2] [--desc "..."]
 *                                         - type: module|feature|bug|interface
 *   list [--type T] [--status S] [--all]  - Open items grouped by priority (--all incl. closed)
 *   show <code|guid>                      - Full detail: deps, comments, code, git refs
 *   comment <code> "text" [--example-file F | --example "code"] [--lang js]
 *   depend <code> --on <code> [--note "..."]   (cycle-checked)
 *   undepend <code> --on <code>
 *   ref <code> <commit-hash> [--note "..."]    - Link a git commit to an item
 *   set <code> [--priority P1] [--status in_progress|blocked|open]
 *   done <code> [--note "resolution"] [--force]
 *                                         - Blocked while open dependencies remain (GR#12)
 *                                           unless --force
 *   summary                               - Compact BOW summary (used at checkin)
 *   startup-summary                       - BOW summary + Vestige check + git sync check
 *
 * DB config via the same env vars as claude-sync.js:
 *   METRO_DB_HOST (127.0.0.1)  METRO_DB_PORT (3306)
 *   METRO_DB_USER (root)       METRO_DB_PASSWORD ('')      METRO_DB_NAME (metro)
 */

'use strict';

const os = require('os');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { execSync } = require('child_process');
const mysql = require('mysql2/promise');

const TYPES = ['module', 'feature', 'bug', 'interface'];
const TYPE_PREFIX = { module: 'MOD', feature: 'FEAT', bug: 'BUG', interface: 'INT' };
const PRIORITIES = ['P0', 'P1', 'P2', 'P3'];
const STATUSES = ['open', 'in_progress', 'blocked', 'done', 'cancelled'];
const OPEN_STATUSES = ['open', 'in_progress', 'blocked'];
const SUMMARY_MARKER = '── METROPOLIS STARTUP SUMMARY ──';

// ── CLI parsing ───────────────────────────────────────────────────────────────

const argv = process.argv.slice(2);
const command = argv[0] || 'summary';
const positional = [];
const flags = {};
const VALUE_FLAGS = ['priority', 'desc', 'status', 'type', 'on', 'note', 'example', 'example-file', 'lang', 'author'];
for (let i = 1; i < argv.length; i++) {
  const a = argv[i];
  if (a.startsWith('--') && VALUE_FLAGS.includes(a.slice(2))) { flags[a.slice(2)] = argv[++i]; }
  else if (a.startsWith('--')) { flags[a.slice(2)] = true; }
  else { positional.push(a); }
}

// ── DB helpers ────────────────────────────────────────────────────────────────

async function connect() {
  try {
    return await mysql.createConnection({
      host: process.env.METRO_DB_HOST || '127.0.0.1',
      port: Number(process.env.METRO_DB_PORT || 3306),
      user: process.env.METRO_DB_USER || 'root',
      password: process.env.METRO_DB_PASSWORD || '',
      database: process.env.METRO_DB_NAME || 'metro',
    });
  } catch (err) {
    console.error(`claude-bow: cannot connect to metro MariaDB: ${err.message}`);
    console.error('Ensure the MariaDB service is running (Get-Service MariaDB) and the metro database exists.');
    process.exit(1);
  }
}

async function ensureSchema(db) {
  await db.query(`CREATE TABLE IF NOT EXISTS bow_items (
    guid        CHAR(36) PRIMARY KEY,
    code        VARCHAR(16) NOT NULL UNIQUE,
    item_type   ENUM('module','feature','bug','interface') NOT NULL,
    title       VARCHAR(255) NOT NULL,
    description TEXT NULL,
    priority    ENUM('P0','P1','P2','P3') NOT NULL DEFAULT 'P2',
    status      ENUM('open','in_progress','blocked','done','cancelled') NOT NULL DEFAULT 'open',
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    closed_at   TIMESTAMP NULL,
    closed_note VARCHAR(512) NULL,
    INDEX idx_bow_status (status),
    INDEX idx_bow_type (item_type)
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`);
  await db.query(`CREATE TABLE IF NOT EXISTS bow_dependencies (
    item_guid       CHAR(36) NOT NULL,
    depends_on_guid CHAR(36) NOT NULL,
    note            VARCHAR(255) NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (item_guid, depends_on_guid),
    CONSTRAINT fk_bow_dep_item FOREIGN KEY (item_guid) REFERENCES bow_items(guid) ON DELETE CASCADE,
    CONSTRAINT fk_bow_dep_on   FOREIGN KEY (depends_on_guid) REFERENCES bow_items(guid) ON DELETE CASCADE
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`);
  await db.query(`CREATE TABLE IF NOT EXISTS bow_comments (
    id            INT AUTO_INCREMENT PRIMARY KEY,
    item_guid     CHAR(36) NOT NULL,
    author        VARCHAR(32) NULL,
    body          TEXT NOT NULL,
    example_code  MEDIUMTEXT NULL,
    code_language VARCHAR(32) NULL,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_bow_comment_item FOREIGN KEY (item_guid) REFERENCES bow_items(guid) ON DELETE CASCADE,
    INDEX idx_bow_comment_item (item_guid)
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`);
  await db.query(`CREATE TABLE IF NOT EXISTS bow_git_refs (
    id          INT AUTO_INCREMENT PRIMARY KEY,
    item_guid   CHAR(36) NOT NULL,
    commit_hash VARCHAR(40) NOT NULL,
    branch      VARCHAR(128) NULL,
    note        VARCHAR(255) NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_bow_ref_item FOREIGN KEY (item_guid) REFERENCES bow_items(guid) ON DELETE CASCADE,
    INDEX idx_bow_ref_item (item_guid)
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`);
}

/** Resolve an item by short code (FEAT-001, case-insensitive) or GUID. */
async function findItem(db, ref) {
  if (!ref) return null;
  const [rows] = await db.query(
    'SELECT * FROM bow_items WHERE guid = ? OR UPPER(code) = UPPER(?) LIMIT 1', [ref, ref]);
  return rows.length ? rows[0] : null;
}

async function requireItem(db, ref) {
  const item = await findItem(db, ref);
  if (!item) {
    console.error(`claude-bow: no BOW item matches "${ref}" (use a code like FEAT-001 or a GUID; see: node claude-bow.js list --all)`);
    process.exit(1);
  }
  return item;
}

/** Next short code for a type: FEAT-001, FEAT-002, ... */
async function nextCode(db, type) {
  const prefix = TYPE_PREFIX[type];
  const [rows] = await db.query(
    "SELECT code FROM bow_items WHERE code LIKE CONCAT(?, '-%')", [prefix]);
  let max = 0;
  for (const r of rows) {
    const n = parseInt(r.code.split('-')[1], 10);
    if (Number.isFinite(n) && n > max) max = n;
  }
  return `${prefix}-${String(max + 1).padStart(3, '0')}`;
}

/** Current session identity for comment attribution (statusline identity file). */
function currentAuthor() {
  if (flags.author) return String(flags.author);
  try {
    return fs.readFileSync(path.join(__dirname, '.claude', '.identity'), 'utf8').trim() || null;
  } catch { return null; }
}

function ts(d) {
  return d instanceof Date ? d.toISOString().replace('T', ' ').slice(0, 19) : String(d);
}

// ── Commands ──────────────────────────────────────────────────────────────────

async function cmdAdd(db) {
  const type = String(positional[0] || '').toLowerCase();
  const title = positional[1];
  if (!TYPES.includes(type) || !title) {
    console.error('Usage: node claude-bow.js add <module|feature|bug|interface> "title" [--priority P0..P3] [--desc "..."]');
    process.exit(1);
  }
  const priority = String(flags.priority || 'P2').toUpperCase();
  if (!PRIORITIES.includes(priority)) {
    console.error(`Invalid priority "${flags.priority}". Valid: ${PRIORITIES.join(', ')}`);
    process.exit(1);
  }
  const guid = crypto.randomUUID();
  const code = await nextCode(db, type);
  await db.query(
    'INSERT INTO bow_items (guid, code, item_type, title, description, priority) VALUES (?, ?, ?, ?, ?, ?)',
    [guid, code, type, title, flags.desc || null, priority]);
  console.log(`Added ${code} [${type}/${priority}] "${title}"`);
  console.log(`GUID: ${guid}`);
}

async function cmdList(db) {
  const where = [];
  const params = [];
  if (flags.all) { /* everything */ }
  else if (flags.status) { where.push('status = ?'); params.push(flags.status); }
  else { where.push(`status IN (${OPEN_STATUSES.map(() => '?').join(',')})`); params.push(...OPEN_STATUSES); }
  if (flags.type) { where.push('item_type = ?'); params.push(String(flags.type).toLowerCase()); }

  const [items] = await db.query(
    `SELECT i.*,
       (SELECT COUNT(*) FROM bow_dependencies d
          JOIN bow_items di ON di.guid = d.depends_on_guid
        WHERE d.item_guid = i.guid AND di.status IN ('open','in_progress','blocked')) AS open_deps
     FROM bow_items i ${where.length ? 'WHERE ' + where.join(' AND ') : ''}
     ORDER BY priority, item_type, code`, params);

  if (!items.length) { console.log('BOW is clean — no matching items.'); return; }
  let currentP = null;
  for (const it of items) {
    if (it.priority !== currentP) { currentP = it.priority; console.log(`\n${currentP}:`); }
    const dep = it.open_deps > 0 ? `  ⛓ waiting on ${it.open_deps} dep(s)` : '';
    console.log(`  ${it.code.padEnd(9)} [${it.status.toUpperCase().padEnd(11)}] ${it.title}${dep}`);
  }
  console.log(`\nTotal: ${items.length}`);
}

async function cmdShow(db) {
  const item = await requireItem(db, positional[0]);
  console.log(`${item.code} — ${item.title}`);
  console.log(`  GUID:     ${item.guid}`);
  console.log(`  Type:     ${item.item_type}   Priority: ${item.priority}   Status: ${item.status}`);
  console.log(`  Created:  ${ts(item.created_at)}   Updated: ${ts(item.updated_at)}`);
  if (item.closed_at) console.log(`  Closed:   ${ts(item.closed_at)}${item.closed_note ? ' — ' + item.closed_note : ''}`);
  if (item.description) console.log(`  Desc:     ${item.description}`);

  const [deps] = await db.query(
    `SELECT d.note, i.code, i.title, i.status FROM bow_dependencies d
     JOIN bow_items i ON i.guid = d.depends_on_guid WHERE d.item_guid = ? ORDER BY i.code`, [item.guid]);
  if (deps.length) {
    console.log('  Depends on:');
    for (const d of deps) console.log(`    ${d.code} [${d.status}] ${d.title}${d.note ? ' — ' + d.note : ''}`);
  }
  const [rdeps] = await db.query(
    `SELECT i.code, i.title, i.status FROM bow_dependencies d
     JOIN bow_items i ON i.guid = d.item_guid WHERE d.depends_on_guid = ? ORDER BY i.code`, [item.guid]);
  if (rdeps.length) {
    console.log('  Blocks (depended on by):');
    for (const d of rdeps) console.log(`    ${d.code} [${d.status}] ${d.title}`);
  }

  const [refs] = await db.query(
    'SELECT * FROM bow_git_refs WHERE item_guid = ? ORDER BY created_at', [item.guid]);
  if (refs.length) {
    console.log('  Git refs:');
    for (const r of refs) console.log(`    ${r.commit_hash.slice(0, 10)}${r.branch ? ' (' + r.branch + ')' : ''}${r.note ? ' — ' + r.note : ''}  [${ts(r.created_at)}]`);
  }

  const [comments] = await db.query(
    'SELECT * FROM bow_comments WHERE item_guid = ? ORDER BY created_at', [item.guid]);
  if (comments.length) {
    console.log('  Comments:');
    for (const c of comments) {
      console.log(`    [${ts(c.created_at)}${c.author ? ' ' + c.author : ''}] ${c.body}`);
      if (c.example_code) {
        console.log(`    \`\`\`${c.code_language || ''}`);
        for (const line of c.example_code.split('\n')) console.log(`    ${line}`);
        console.log('    ```');
      }
    }
  }
}

async function cmdComment(db) {
  const item = await requireItem(db, positional[0]);
  const body = positional[1];
  if (!body) {
    console.error('Usage: node claude-bow.js comment <code> "text" [--example-file F | --example "code"] [--lang js]');
    process.exit(1);
  }
  let example = flags.example || null;
  if (flags['example-file']) {
    try { example = fs.readFileSync(flags['example-file'], 'utf8'); }
    catch (err) { console.error(`Cannot read --example-file: ${err.message}`); process.exit(1); }
  }
  await db.query(
    'INSERT INTO bow_comments (item_guid, author, body, example_code, code_language) VALUES (?, ?, ?, ?, ?)',
    [item.guid, currentAuthor(), body, example, example ? (flags.lang || null) : null]);
  console.log(`Comment added to ${item.code}${example ? ' (with example code)' : ''}.`);
}

async function cmdDepend(db) {
  const item = await requireItem(db, positional[0]);
  if (!flags.on) { console.error('Usage: node claude-bow.js depend <code> --on <code> [--note "..."]'); process.exit(1); }
  const target = await requireItem(db, flags.on);
  if (target.guid === item.guid) { console.error('An item cannot depend on itself.'); process.exit(1); }

  // Cycle check: walk target's dependency closure; if item appears, reject.
  const [allDeps] = await db.query('SELECT item_guid, depends_on_guid FROM bow_dependencies');
  const graph = {};
  for (const d of allDeps) (graph[d.item_guid] = graph[d.item_guid] || []).push(d.depends_on_guid);
  const seen = new Set();
  const stack = [target.guid];
  while (stack.length) {
    const g = stack.pop();
    if (g === item.guid) {
      console.error(`Dependency cycle: ${target.code} already depends (transitively) on ${item.code}.`);
      process.exit(1);
    }
    if (seen.has(g)) continue;
    seen.add(g);
    for (const next of graph[g] || []) stack.push(next);
  }

  await db.query(
    'REPLACE INTO bow_dependencies (item_guid, depends_on_guid, note) VALUES (?, ?, ?)',
    [item.guid, target.guid, flags.note || null]);
  console.log(`${item.code} now depends on ${target.code}${flags.note ? ' — ' + flags.note : ''}.`);
}

async function cmdUndepend(db) {
  const item = await requireItem(db, positional[0]);
  if (!flags.on) { console.error('Usage: node claude-bow.js undepend <code> --on <code>'); process.exit(1); }
  const target = await requireItem(db, flags.on);
  const [res] = await db.query(
    'DELETE FROM bow_dependencies WHERE item_guid = ? AND depends_on_guid = ?', [item.guid, target.guid]);
  console.log(res.affectedRows ? `${item.code} no longer depends on ${target.code}.` : `No such dependency.`);
}

async function cmdRef(db) {
  const item = await requireItem(db, positional[0]);
  const hash = positional[1];
  if (!hash || !/^[0-9a-f]{7,40}$/i.test(hash)) {
    console.error('Usage: node claude-bow.js ref <code> <commit-hash 7-40 hex> [--note "..."]');
    process.exit(1);
  }
  let branch = null;
  try { branch = execSync('git rev-parse --abbrev-ref HEAD', { cwd: __dirname, encoding: 'utf8', timeout: 5000 }).trim(); }
  catch { /* not fatal — branch is decoration */ }
  await db.query(
    'INSERT INTO bow_git_refs (item_guid, commit_hash, branch, note) VALUES (?, ?, ?, ?)',
    [item.guid, hash.toLowerCase(), branch, flags.note || null]);
  console.log(`Linked commit ${hash.slice(0, 10)} to ${item.code}${flags.note ? ' — ' + flags.note : ''}.`);
}

async function cmdSet(db) {
  const item = await requireItem(db, positional[0]);
  const updates = [];
  const params = [];
  if (flags.priority) {
    const p = String(flags.priority).toUpperCase();
    if (!PRIORITIES.includes(p)) { console.error(`Invalid priority "${flags.priority}". Valid: ${PRIORITIES.join(', ')}`); process.exit(1); }
    updates.push('priority = ?'); params.push(p);
  }
  if (flags.status) {
    const s = String(flags.status).toLowerCase();
    if (!STATUSES.includes(s)) { console.error(`Invalid status "${flags.status}". Valid: ${STATUSES.join(', ')}`); process.exit(1); }
    updates.push('status = ?'); params.push(s);
    if (s === 'done' || s === 'cancelled') { updates.push('closed_at = CURRENT_TIMESTAMP'); }
    else { updates.push('closed_at = NULL'); updates.push('closed_note = NULL'); }
  }
  if (!updates.length) { console.error('Usage: node claude-bow.js set <code> [--priority P0..P3] [--status open|in_progress|blocked|done|cancelled]'); process.exit(1); }
  params.push(item.guid);
  await db.query(`UPDATE bow_items SET ${updates.join(', ')} WHERE guid = ?`, params);
  console.log(`${item.code} updated${flags.priority ? ` priority=${String(flags.priority).toUpperCase()}` : ''}${flags.status ? ` status=${String(flags.status).toLowerCase()}` : ''}.`);
}

async function cmdDone(db) {
  const item = await requireItem(db, positional[0]);
  // GR#12: an item is not complete while the things it depends on are open.
  const [openDeps] = await db.query(
    `SELECT i.code, i.status, i.title FROM bow_dependencies d
     JOIN bow_items i ON i.guid = d.depends_on_guid
     WHERE d.item_guid = ? AND i.status IN ('open','in_progress','blocked')`, [item.guid]);
  if (openDeps.length && !flags.force) {
    console.error(`GR#12 BLOCK: ${item.code} still has open dependencies:`);
    for (const d of openDeps) console.error(`  ${d.code} [${d.status}] ${d.title}`);
    console.error('Close those first, or override with --force if they are genuinely not blockers.');
    process.exit(1);
  }
  await db.query(
    'UPDATE bow_items SET status = ?, closed_at = CURRENT_TIMESTAMP, closed_note = ? WHERE guid = ?',
    ['done', flags.note || null, item.guid]);
  console.log(`${item.code} marked DONE${flags.note ? ' — ' + flags.note : ''}${openDeps.length ? ' (--force: open deps overridden)' : ''}.`);
}

// ── Startup summary (checkin integration) ─────────────────────────────────────

/**
 * Compact BOW summary. Printing this successfully IS the MariaDB health check:
 * it only appears when the metro database answered the queries.
 */
async function printBowSummary(db) {
  const [counts] = await db.query(
    `SELECT priority, status, COUNT(*) AS n FROM bow_items
     WHERE status IN ('open','in_progress','blocked') GROUP BY priority, status`);
  const [totals] = await db.query(
    `SELECT COUNT(*) AS total, SUM(status = 'done') AS done FROM bow_items`);

  const open = counts.reduce((s, r) => s + Number(r.n), 0);
  const byP = {};
  for (const r of counts) byP[r.priority] = (byP[r.priority] || 0) + Number(r.n);
  const pStr = PRIORITIES.filter(p => byP[p]).map(p => `${byP[p]} ${p}`).join(', ');
  const blocked = counts.filter(r => r.status === 'blocked').reduce((s, r) => s + Number(r.n), 0);

  console.log(`BOW (metro MariaDB): OK — ${open} open item(s)${pStr ? ` (${pStr})` : ''}` +
    `${blocked ? `, ${blocked} blocked` : ''}, ${Number(totals[0].done || 0)}/${totals[0].total} done`);

  if (open) {
    const [top] = await db.query(
      `SELECT i.code, i.priority, i.status, i.title,
         (SELECT COUNT(*) FROM bow_dependencies d
            JOIN bow_items di ON di.guid = d.depends_on_guid
          WHERE d.item_guid = i.guid AND di.status IN ('open','in_progress','blocked')) AS open_deps
       FROM bow_items i WHERE i.status IN ('open','in_progress','blocked')
       ORDER BY i.priority, i.code LIMIT 5`);
    for (const t of top) {
      console.log(`  ${t.priority} ${t.code.padEnd(9)} [${t.status}] ${t.title}${t.open_deps ? `  ⛓ ${t.open_deps} dep(s)` : ''}`);
    }
    if (open > top.length) console.log(`  ... +${open - top.length} more: node claude-bow.js list`);
  }
}

/** Vestige MCP availability: exe on disk + registered in user-level .claude.json. */
function printVestigeCheck() {
  try {
    const cfgPath = path.join(os.homedir(), '.claude.json');
    const cfg = JSON.parse(fs.readFileSync(cfgPath, 'utf8'));
    const entry = (cfg.mcpServers || {}).vestige;
    if (!entry) { console.log('Vestige: NOT CONFIGURED in ~/.claude.json — memory recall unavailable!'); return; }
    const exeOk = entry.command ? fs.existsSync(entry.command) : false;
    console.log(exeOk
      ? `Vestige: configured, binary present (${path.basename(entry.command)}) — confirm live with mcp__vestige__search`
      : `Vestige: configured BUT binary missing at ${entry.command} — memory recall will fail!`);
  } catch (err) {
    console.log(`Vestige: check failed (${err.message}) — verify manually with mcp__vestige__search`);
  }
}

/** Git sync state: branch, dirty files, ahead/behind origin (with a quick fetch). */
function printGitCheck() {
  const git = (args, timeout = 5000) =>
    execSync(`git ${args}`, { cwd: __dirname, encoding: 'utf8', timeout, stdio: ['pipe', 'pipe', 'pipe'] }).trim();
  try {
    const branch = git('rev-parse --abbrev-ref HEAD');
    let fetched = true;
    try { git('fetch --quiet origin', 15000); } catch { fetched = false; }
    const dirty = git('status --porcelain').split('\n').filter(Boolean).length;
    let ahead = null, behind = null;
    try {
      const lr = git(`rev-list --left-right --count origin/${branch}...HEAD`).split(/\s+/);
      behind = Number(lr[0]); ahead = Number(lr[1]);
    } catch { /* no upstream for this branch */ }

    const synced = dirty === 0 && ahead === 0 && behind === 0;
    if (synced) {
      console.log(`Git: ${branch} — SYNCED (clean tree, level with origin/${branch}${fetched ? '' : ', fetch failed — offline?'})`);
    } else {
      const bits = [];
      if (dirty) bits.push(`${dirty} uncommitted change(s)`);
      if (ahead) bits.push(`${ahead} commit(s) ahead of origin`);
      if (behind) bits.push(`${behind} commit(s) BEHIND origin — pull needed`);
      if (ahead === null) bits.push('no upstream tracking branch');
      console.log(`Git: ${branch} — NOT SYNCED: ${bits.join('; ')}${fetched ? '' : ' (fetch failed — offline?)'}`);
    }
  } catch (err) {
    console.log(`Git: check failed (${err.message.split('\n')[0]})`);
  }
}

/** Full startup block, printed by claude-sync checkin and relayed by claude-startup. */
async function printStartupSummary(db) {
  await ensureSchema(db); // callers (claude-sync) pass a raw connection — make BOW tables certain
  console.log('');
  console.log(SUMMARY_MARKER);
  await printBowSummary(db);
  printVestigeCheck();
  printGitCheck();
}

module.exports = { printStartupSummary, printBowSummary, SUMMARY_MARKER };

// ── Entry ─────────────────────────────────────────────────────────────────────

if (require.main === module) {
  (async () => {
    const db = await connect();
    try {
      await ensureSchema(db);
      switch (command) {
        case 'init': console.log('metro BOW tables ready (bow_items, bow_dependencies, bow_comments, bow_git_refs).'); break;
        case 'add': await cmdAdd(db); break;
        case 'list': await cmdList(db); break;
        case 'show': await cmdShow(db); break;
        case 'comment': await cmdComment(db); break;
        case 'depend': await cmdDepend(db); break;
        case 'undepend': await cmdUndepend(db); break;
        case 'ref': await cmdRef(db); break;
        case 'set': await cmdSet(db); break;
        case 'done': await cmdDone(db); break;
        case 'summary': await printBowSummary(db); break;
        case 'startup-summary': await printStartupSummary(db); break;
        default:
          console.error(`Unknown command: ${command}`);
          console.error('Commands: init, add, list, show, comment, depend, undepend, ref, set, done, summary, startup-summary');
          process.exit(1);
      }
    } catch (err) {
      console.error(`claude-bow error: ${err.message}`);
      process.exit(1);
    } finally {
      await db.end().catch(() => {});
    }
  })();
}
