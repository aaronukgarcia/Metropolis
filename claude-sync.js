/**
 * claude-sync.js — Metropolis session coordination (MariaDB backend)
 *
 * Port of the Prix Six claude-sync v2.2 DHCP-style permit system onto the
 * project's own `metro` MariaDB database (localhost:3306). Same protocol:
 * three named slots (Bill, Bob, Ben), 5-minute TTL permits, auto-renewal by
 * the PostToolUse hook, wake recovery, reserved slots, human-only force-evict.
 *
 * The CLI surface and output strings are contract-compatible with the hook
 * scripts (claude-startup.js parses "YOU ARE: <Name>", "SLOT IS OCCUPIED",
 * "SLOT IS RESERVED", "ALL SLOTS FULL", "expires in Xm Ys",
 * "FORCE-EVICT BLOCKED"; claude-ping-check.js runs `renew --auto`).
 *
 * Commands:
 *   init                        - Create coordination tables (auto-runs on every command)
 *   checkin [--name N] [--any]  - Acquire a permit slot (5-min TTL)
 *           [--force --human-ok]  Force-evict a live holder (HUMAN AUTHORISATION ONLY)
 *   renew [--auto] [--session ID] - Extend permit; --auto only renews when < 3.5 min left
 *   ping [--session ID]         - Renew + heartbeat + status line
 *   checkout [--session ID]     - Release this window's permit
 *   checkout --force <Name>     - Admin: evict a specific permit holder
 *   status [--session ID]       - Show all slots, marking this window's
 *   read                        - Full coordination state: slots, activity log, NO-TOUCH zones
 *   write "message"             - Log a milestone to the activity log
 *   claim <path> [--session ID] - Claim a NO-TOUCH zone before modifying files
 *   release <path>              - Release a claimed path
 *   gc                          - Clean up permits expired beyond the reserve window
 *
 * Identity resolution (same as prix6 v2.2): the Claude window's session UUID
 * arrives via the CLAUDE_CODE_SESSION_ID env var (hooks set it from the hook
 * stdin payload). Every permit is keyed to that window id, so multiple windows
 * never cross-renew. CLAUDE_IDENTITY (set by metro.bat) is the preferred slot
 * for a plain `checkin`; `--any` ignores it.
 *
 * States per slot:
 *   ACTIVE    expires_ms in the future, same OS boot        -> renewable by holder only
 *   RESERVED  expired < 30 min ago, same OS boot            -> only its old window may reclaim
 *   FREE      never held / released / reboot / reserve over -> anyone may acquire
 *
 * DB config via env (defaults match the machine's MariaDB setup):
 *   METRO_DB_HOST (127.0.0.1)  METRO_DB_PORT (3306)
 *   METRO_DB_USER (root)       METRO_DB_PASSWORD ('')      METRO_DB_NAME (metro)
 */

'use strict';

const os = require('os');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const mysql = require('mysql2/promise');

const NAMES = ['Bill', 'Bob', 'Ben'];
const TTL_MS = 5 * 60 * 1000;             // permit lifetime
const RENEW_THRESHOLD_MS = 3.5 * 60 * 1000; // --auto renews only below this remaining
const RESERVE_MS = 30 * 60 * 1000;        // expired slot stays reserved for its window

// Boot id: boot time rounded to 10 s. Survives across processes in one boot,
// changes on reboot — that mismatch is how dead holders are proven dead.
const BOOT_ID = String(Math.round((Date.now() - os.uptime() * 1000) / 10000));

// ── CLI parsing ───────────────────────────────────────────────────────────────

const argv = process.argv.slice(2);
const command = argv[0] || 'read';
const positional = [];
const flags = {};
for (let i = 1; i < argv.length; i++) {
  const a = argv[i];
  if (a === '--name' || a === '--session') { flags[a.slice(2)] = argv[++i]; }
  else if (a.startsWith('--')) { flags[a.slice(2)] = true; }
  else { positional.push(a); }
}

const WINDOW_ID = process.env.CLAUDE_CODE_SESSION_ID || process.env.CLAUDE_SESSION_ID || '';

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
    console.error(`claude-sync: cannot connect to metro MariaDB: ${err.message}`);
    console.error('Ensure the MariaDB service is running (Get-Service MariaDB) and the metro database exists.');
    process.exit(1);
  }
}

async function ensureSchema(db) {
  await db.query(`CREATE TABLE IF NOT EXISTS sync_permits (
    name         VARCHAR(16) PRIMARY KEY,
    session_id   CHAR(36) NULL,
    window_id    VARCHAR(64) NULL,
    acquired_ms  BIGINT NULL,
    expires_ms   BIGINT NULL,
    heartbeat_ms BIGINT NULL,
    boot_id      VARCHAR(32) NULL,
    released     TINYINT(1) NOT NULL DEFAULT 1
  ) ENGINE=InnoDB`);
  await db.query(`CREATE TABLE IF NOT EXISTS sync_activity (
    id      INT AUTO_INCREMENT PRIMARY KEY,
    ts      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    name    VARCHAR(16) NULL,
    message TEXT NOT NULL
  ) ENGINE=InnoDB`);
  await db.query(`CREATE TABLE IF NOT EXISTS sync_file_claims (
    path       VARCHAR(512) PRIMARY KEY,
    name       VARCHAR(16) NOT NULL,
    session_id CHAR(36) NULL,
    claimed_ms BIGINT NOT NULL
  ) ENGINE=InnoDB`);
  // Window -> last-held name. Survives the slot being reassigned to another
  // window, so wake recovery can still say "your old name is gone" (the slot
  // row itself only remembers its CURRENT holder).
  await db.query(`CREATE TABLE IF NOT EXISTS sync_window_map (
    window_id  VARCHAR(64) PRIMARY KEY,
    name       VARCHAR(16) NOT NULL,
    updated_ms BIGINT NOT NULL
  ) ENGINE=InnoDB`);
  for (const n of NAMES) {
    await db.query('INSERT IGNORE INTO sync_permits (name) VALUES (?)', [n]);
  }
}

function slotState(row, now) {
  if (!row.session_id || row.released) return 'FREE';
  if (row.boot_id !== BOOT_ID) return 'FREE';           // reboot voids permits and reservations
  if (Number(row.expires_ms) > now) return 'ACTIVE';
  if (now - Number(row.expires_ms) < RESERVE_MS) return 'RESERVED';
  return 'FREE';
}

function fmtMs(ms) {
  const s = Math.max(0, Math.ceil(ms / 1000));
  return `${Math.floor(s / 60)}m ${s % 60}s`;
}

async function log(db, name, message) {
  await db.query('INSERT INTO sync_activity (name, message) VALUES (?, ?)', [name, message]);
}

async function lockedPermits(db) {
  const [rows] = await db.query('SELECT * FROM sync_permits FOR UPDATE');
  const byName = {};
  for (const r of rows) byName[r.name] = r;
  return byName;
}

async function acquire(db, name, prevRow) {
  const now = Date.now();
  const sessionId = crypto.randomUUID();
  await db.query(
    `UPDATE sync_permits SET session_id=?, window_id=?, acquired_ms=?, expires_ms=?, heartbeat_ms=?, boot_id=?, released=0 WHERE name=?`,
    [sessionId, WINDOW_ID || null, now, now + TTL_MS, now, BOOT_ID, name]
  );
  if (WINDOW_ID) {
    await db.query('REPLACE INTO sync_window_map (window_id, name, updated_ms) VALUES (?, ?, ?)',
      [WINDOW_ID, name, now]);
  }
  writeIdentityFiles(name);
  return sessionId;
}

/** Keep the statusline truthful: it reads .claude/.identity-<window> first,
 *  falling back to the shared .identity. Writing both here (on every acquire,
 *  including wake recovery and IDENTITY CHANGED) means the statusline follows
 *  identity changes without depending on the startup hook re-running. */
function writeIdentityFiles(name) {
  try {
    const dotClaude = path.join(__dirname, '.claude');
    fs.mkdirSync(dotClaude, { recursive: true });
    if (WINDOW_ID) fs.writeFileSync(path.join(dotClaude, `.identity-${WINDOW_ID}`), name.toLowerCase(), 'utf8');
    fs.writeFileSync(path.join(dotClaude, '.identity'), name.toLowerCase(), 'utf8');
  } catch { /* statusline nicety — never fail a checkin over it */ }
}

function printSuccess(name, sessionId) {
  console.log(`YOU ARE: ${name}`);
  console.log(`Session: ${sessionId}`);
  console.log(`Permit TTL: 5 minutes — auto-renewed by the PostToolUse hook while you work.`);
  console.log(`Prefix every response with "${name.toLowerCase()}>".`);
}

/** Resolve the permit row held by this window (active only unless allowStale). */
function findMine(byName, now, { allowStale = false } = {}) {
  for (const n of NAMES) {
    const row = byName[n];
    if (!row.window_id || row.window_id !== WINDOW_ID || !WINDOW_ID) continue;
    const state = slotState(row, now);
    if (state === 'ACTIVE') return { row, state };
    if (allowStale && state === 'RESERVED') return { row, state };
  }
  // --session fallback: match by permit session id (plain-terminal usage)
  if (flags.session) {
    for (const n of NAMES) {
      const row = byName[n];
      if (row.session_id === flags.session && slotState(row, now) !== 'FREE') {
        return { row, state: slotState(row, now) };
      }
    }
  }
  return null;
}

// ── Commands ──────────────────────────────────────────────────────────────────

async function cmdCheckin(db) {
  const now = Date.now();
  await db.beginTransaction();
  const byName = await lockedPermits(db);

  // Already holding an active permit in this window? Renew, done.
  const mine = findMine(byName, now);
  if (mine) {
    await db.query('UPDATE sync_permits SET expires_ms=?, heartbeat_ms=? WHERE name=?',
      [now + TTL_MS, now, mine.row.name]);
    await db.commit();
    printSuccess(mine.row.name, mine.row.session_id);
    return;
  }

  const requested = flags.name || (!flags.any && process.env.CLAUDE_IDENTITY) || null;

  if (requested) {
    const name = NAMES.find(n => n.toLowerCase() === String(requested).toLowerCase());
    if (!name) {
      await db.rollback();
      console.error(`Unknown slot name "${requested}". Valid: ${NAMES.join(', ')}`);
      process.exit(1);
    }
    const row = byName[name];
    const state = slotState(row, now);

    if (state === 'ACTIVE') {
      // Auto-reclaim from a provably dead holder is handled by slotState (boot
      // mismatch -> FREE). A live holder requires the human-only force path.
      if (flags.force && flags['human-ok']) {
        const sessionId = await acquire(db, name);
        await log(db, name, `${name} FORCE-EVICTED previous holder (human-authorised) and checked in`);
        await db.commit();
        console.log(`Evicted previous ${name} holder (human-authorised).`);
        printSuccess(name, sessionId);
        return;
      }
      if (flags.force) {
        await db.rollback();
        console.error(`FORCE-EVICT BLOCKED: evicting a live holder requires the human-only --human-ok flag.`);
        console.error(`A human must authorise: node claude-sync.js checkin --name ${name} --force --human-ok`);
        process.exit(1);
      }
      await db.rollback();
      console.error(`SLOT IS OCCUPIED: ${name} is held by a live session (name-occupied), expires in ${fmtMs(Number(row.expires_ms) - now)}.`);
      console.error(`Take the next free slot instead: node claude-sync.js checkin --any`);
      process.exit(1);
    }

    if (state === 'RESERVED' && row.window_id !== WINDOW_ID) {
      if (flags.force && flags['human-ok']) {
        const sessionId = await acquire(db, name);
        await log(db, name, `${name} reservation overridden (human-authorised)`);
        await db.commit();
        printSuccess(name, sessionId);
        return;
      }
      await db.rollback();
      console.error(`SLOT IS RESERVED: ${name} expired recently and is held for its idle window (name-reserved).`);
      console.error(`Take the next free slot instead: node claude-sync.js checkin --any`);
      process.exit(1);
    }

    // FREE, or RESERVED for this very window — take it.
    const sessionId = await acquire(db, name);
    await log(db, name, `${name} checked in`);
    await db.commit();
    printSuccess(name, sessionId);
    return;
  }

  // No specific name — first free slot (Bill -> Bob -> Ben).
  const free = NAMES.find(n => slotState(byName[n], now) === 'FREE'
    || (slotState(byName[n], now) === 'RESERVED' && byName[n].window_id === WINDOW_ID));
  if (!free) {
    await db.rollback();
    console.error('ALL SLOTS FULL (all-full): Bill, Bob and Ben are all occupied or reserved.');
    for (const n of NAMES) {
      const row = byName[n];
      const state = slotState(row, now);
      if (state === 'ACTIVE') console.error(`  ${n} expires in ${fmtMs(Number(row.expires_ms) - now)}`);
      else console.error(`  ${n} reserved for an idle window (reservation lapses in ${fmtMs(RESERVE_MS - (now - Number(row.expires_ms)))})`);
    }
    process.exit(1);
  }
  const sessionId = await acquire(db, free);
  await log(db, free, `${free} checked in`);
  await db.commit();
  printSuccess(free, sessionId);
}

async function cmdRenew(db) {
  const now = Date.now();
  await db.beginTransaction();
  const byName = await lockedPermits(db);

  const mine = findMine(byName, now);
  if (mine) {
    const remaining = Number(mine.row.expires_ms) - now;
    if (flags.auto && remaining > RENEW_THRESHOLD_MS) {
      await db.query('UPDATE sync_permits SET heartbeat_ms=? WHERE name=?', [now, mine.row.name]);
      await db.commit();
      return; // plenty of time left — heartbeat only, stay silent
    }
    await db.query('UPDATE sync_permits SET expires_ms=?, heartbeat_ms=? WHERE name=?',
      [now + TTL_MS, now, mine.row.name]);
    await db.commit();
    if (!flags.auto) console.log(`${mine.row.name} renewed — expires in ${fmtMs(TTL_MS)}.`);
    return;
  }

  // Wake recovery: permit expired while idle.
  const stale = findMine(byName, now, { allowStale: true });
  if (stale) {
    const sessionId = await acquire(db, stale.row.name);
    await log(db, stale.row.name, `${stale.row.name} wake recovery — re-acquired after idle expiry`);
    await db.commit();
    console.log(`[claude-sync] Wake recovery: re-acquired ${stale.row.name} (permit expired while idle). Session: ${sessionId}`);
    return;
  }

  // This window previously held a name that is now gone or taken — next free slot.
  let hadName = WINDOW_ID && NAMES.find(n => byName[n].window_id === WINDOW_ID);
  if (!hadName && WINDOW_ID) {
    const [map] = await db.query('SELECT name FROM sync_window_map WHERE window_id=?', [WINDOW_ID]);
    if (map.length) hadName = map[0].name;
  }
  if (hadName) {
    const free = NAMES.find(n => slotState(byName[n], now) === 'FREE');
    if (free) {
      const sessionId = await acquire(db, free);
      await log(db, free, `${free} assigned via wake recovery (previous name ${hadName} unavailable)`);
      await db.commit();
      console.log(`[claude-sync] ⚠ IDENTITY CHANGED: your previous name ${hadName} is no longer yours.`);
      console.log(`[claude-sync] YOU ARE: ${free} — prefix every response with "${free.toLowerCase()}>" from now on. Session: ${sessionId}`);
      return;
    }
    await db.commit();
    console.log('[claude-sync] WARNING: permit expired and all slots are occupied. You hold NO identity — do not prefix responses until a checkin succeeds.');
    return;
  }

  await db.commit();
  if (!flags.auto) console.log('No permit for this window. Run: node claude-sync.js checkin');
}

async function cmdCheckout(db) {
  const now = Date.now();
  await db.beginTransaction();
  const byName = await lockedPermits(db);

  let target = null;
  if (flags.force) {
    const forcedName = positional[0] || (typeof flags.force === 'string' ? flags.force : null);
    const name = NAMES.find(n => forcedName && n.toLowerCase() === String(forcedName).toLowerCase());
    if (!name) {
      await db.rollback();
      console.error('checkout --force requires a slot name: node claude-sync.js checkout --force Bill');
      process.exit(1);
    }
    target = byName[name];
  } else {
    const mine = findMine(byName, now, { allowStale: true });
    target = mine ? mine.row : null;
  }

  if (!target || !target.session_id || target.released) {
    await db.commit();
    console.log('No active permit to check out for this window.');
    return;
  }
  await db.query('UPDATE sync_permits SET released=1 WHERE name=?', [target.name]);
  await db.query('DELETE FROM sync_file_claims WHERE name=?', [target.name]);
  await log(db, target.name, `${target.name} checked out${flags.force ? ' (admin force)' : ''}`);
  await db.commit();
  console.log(`${target.name} checked out. Slot released.`);
}

async function cmdStatus(db, { full = false } = {}) {
  const now = Date.now();
  const byName = await (async () => {
    const [rows] = await db.query('SELECT * FROM sync_permits');
    const m = {}; for (const r of rows) m[r.name] = r; return m;
  })();

  console.log('claude-sync (metro MariaDB) — slot status:');
  for (const n of NAMES) {
    const row = byName[n];
    const state = slotState(row, now);
    const you = WINDOW_ID && row.window_id === WINDOW_ID && state !== 'FREE' ? '  ← YOU' : '';
    if (state === 'ACTIVE') {
      console.log(`  ${n.padEnd(4)} ACTIVE   expires in ${fmtMs(Number(row.expires_ms) - now)}${you}`);
    } else if (state === 'RESERVED') {
      console.log(`  ${n.padEnd(4)} RESERVED reservation lapses in ${fmtMs(RESERVE_MS - (now - Number(row.expires_ms)))}${you}`);
    } else {
      console.log(`  ${n.padEnd(4)} FREE`);
    }
  }

  if (full) {
    const [claims] = await db.query('SELECT * FROM sync_file_claims ORDER BY claimed_ms');
    if (claims.length) {
      console.log('\nNO-TOUCH ZONES (claimed paths):');
      for (const c of claims) console.log(`  ${c.path}  — ${c.name}`);
    }
    const [acts] = await db.query('SELECT ts, name, message FROM sync_activity ORDER BY id DESC LIMIT 10');
    if (acts.length) {
      console.log('\nRecent activity:');
      for (const a of acts.reverse()) {
        console.log(`  [${a.ts.toISOString().replace('T', ' ').slice(0, 19)}] ${a.name ? a.name + ': ' : ''}${a.message}`);
      }
    }
  }
}

async function cmdWrite(db) {
  const message = positional.join(' ');
  if (!message) { console.error('Usage: node claude-sync.js write "message"'); process.exit(1); }
  const now = Date.now();
  const [rows] = await db.query('SELECT * FROM sync_permits');
  const byName = {}; for (const r of rows) byName[r.name] = r;
  const mine = findMine(byName, now, { allowStale: true });
  await log(db, mine ? mine.row.name : null, message);
  console.log(`Logged${mine ? ` as ${mine.row.name}` : ''}: ${message}`);
}

async function cmdClaim(db) {
  const p = positional[0];
  if (!p) { console.error('Usage: node claude-sync.js claim <path>'); process.exit(1); }
  const now = Date.now();
  const [rows] = await db.query('SELECT * FROM sync_permits');
  const byName = {}; for (const r of rows) byName[r.name] = r;
  const mine = findMine(byName, now);
  if (!mine) { console.error('No active permit — checkin before claiming paths.'); process.exit(1); }
  const [existing] = await db.query('SELECT * FROM sync_file_claims WHERE path=?', [p]);
  if (existing.length && existing[0].name !== mine.row.name) {
    console.error(`NO-TOUCH ZONE: ${p} is already claimed by ${existing[0].name}. STOP and ask the human first.`);
    process.exit(1);
  }
  await db.query('REPLACE INTO sync_file_claims (path, name, session_id, claimed_ms) VALUES (?, ?, ?, ?)',
    [p, mine.row.name, mine.row.session_id, now]);
  await log(db, mine.row.name, `claimed ${p}`);
  console.log(`${mine.row.name} claimed: ${p}`);
}

async function cmdRelease(db) {
  const p = positional[0];
  if (!p) { console.error('Usage: node claude-sync.js release <path>'); process.exit(1); }
  const [res] = await db.query('DELETE FROM sync_file_claims WHERE path=?', [p]);
  console.log(res.affectedRows ? `Released: ${p}` : `No claim found for: ${p}`);
}

async function cmdGc(db) {
  const now = Date.now();
  const [res] = await db.query(
    'UPDATE sync_permits SET released=1 WHERE released=0 AND (expires_ms < ? OR boot_id <> ?)',
    [now - RESERVE_MS, BOOT_ID]
  );
  const [claims] = await db.query(
    'DELETE FROM sync_file_claims WHERE name NOT IN (SELECT name FROM sync_permits WHERE released=0)');
  console.log(`gc: released ${res.affectedRows} stale permit(s), removed ${claims.affectedRows} orphaned claim(s).`);
}

// ── Entry ─────────────────────────────────────────────────────────────────────

(async () => {
  const db = await connect();
  try {
    await ensureSchema(db);
    switch (command) {
      case 'init': console.log('metro coordination tables ready (sync_permits, sync_activity, sync_file_claims).'); break;
      case 'checkin': await cmdCheckin(db); break;
      case 'renew': await cmdRenew(db); break;
      case 'ping': flags.auto = false; await cmdRenew(db); await cmdStatus(db); break;
      case 'checkout': await cmdCheckout(db); break;
      case 'status': await cmdStatus(db); break;
      case 'read': await cmdStatus(db, { full: true }); break;
      case 'write': await cmdWrite(db); break;
      case 'claim': await cmdClaim(db); break;
      case 'release': await cmdRelease(db); break;
      case 'gc': await cmdGc(db); break;
      default:
        console.error(`Unknown command: ${command}`);
        console.error('Commands: init, checkin, renew, ping, checkout, status, read, write, claim, release, gc');
        process.exit(1);
    }
  } catch (err) {
    try { await db.rollback(); } catch { /* no open tx */ }
    console.error(`claude-sync error: ${err.message}`);
    process.exit(1);
  } finally {
    await db.end().catch(() => {});
  }
})();
