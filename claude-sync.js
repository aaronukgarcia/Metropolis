// Module key: tool.sync (see code.json; GUID eae1b5fc-9fc9-46fa-af15-5333c5db21f8)
// Spec ref: M0-ENG §5 (hooks); session coordination

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
 *   checkin [--name N] [--any]  - Acquire a permit slot (5-min TTL). On success also
 *                                 prints the METROPOLIS STARTUP SUMMARY (BOW state from
 *                                 the metro DB, Vestige check, git sync check) via
 *                                 claude-bow.js printStartupSummary. A successful checkin
 *                                 also delivers any unread directed/broadcast messages
 *                                 (see `message`, below) for the resolved identity and
 *                                 advances that identity's read cursor (FEAT-069) — this
 *                                 does NOT happen on `renew --auto`'s heartbeat, only on
 *                                 a genuine checkin.
 *           [--force --human-ok]  Force-evict a live holder (HUMAN AUTHORISATION ONLY)
 *   renew [--auto] [--session ID] - Extend permit; --auto only renews when < 3.5 min left
 *   ping [--session ID]         - Renew + heartbeat + status line
 *   checkout [--session ID]     - Release this window's permit
 *   checkout --force <Name>     - Admin: evict a specific permit holder
 *   status [--session ID]       - Show all slots, marking this window's
 *   read                        - Full coordination state: slots, activity log, NO-TOUCH zones
 *   write "message"             - Log a milestone to the activity log
 *   message "<text>" [--to <Name>] [--body-file <path>]
 *                                - Send a directed (--to) or broadcast (no --to) message,
 *                                  delivered to the resolved-name recipient's next checkin
 *                                  (FEAT-069). Requires an active permit.
 *   claim <path> [--session ID] - Claim a NO-TOUCH zone before modifying files
 *   release <path>              - Release a claimed path
 *   gc                          - Clean up permits expired beyond the reserve window
 *   loop-set --session <id> "<spec>"  - FEAT-070: configure the caller's standing /loop spec
 *                                 (e.g. "15m /oversight-sweep"), re-armed at every
 *                                 checkin (see claude-startup.js) so it survives reboot
 *                                 without persisting the running loop process itself.
 *                                 `--session <id>` is MANDATORY (the id YOUR OWN checkin
 *                                 printed as "Session: <uuid>") — round 2 Destructive REJECT
 *                                 finding A: WINDOW_ID (the env var) is not proof of identity,
 *                                 only the DB-issued session secret is; see findMineBySessionSecret.
 *   loop-clear --session <id>   - FEAT-070: clear the caller's own standing loop (self-only)
 *   loop-show --session <id>    - FEAT-070: show the caller's own standing loop state
 *                                 spec text is printable-ASCII-only (allowlist, not blocklist —
 *                                 round 2 finding B).
 *   util [--hours N] [--target N] [--set-target N] [--now] [--json]
 *                                - FEAT-076 (tool.agentlog): hourly agent dispatch/stop
 *                                 utilisation report, built from sync_dispatch_events
 *                                 (logged by claude-dispatch-guard.js on dispatch and
 *                                 claude-agent-stop.js on stop). --hours sets the report
 *                                 window (default 12). --target overrides the target lane
 *                                 count for this one run only. --set-target PERSISTS a new
 *                                 target to project_meta.dispatch_target_lanes (default 12
 *                                 when never set -- GR#15, no hardcoded target elsewhere).
 *                                 --now prints a compact RUNNING NOW block (measured lanes,
 *                                 per-identity counts, oldest-open age) instead of the full
 *                                 table. --json emits the bucketed rows as machine-readable
 *                                 JSON instead of the padded table.
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
const { connectCLI } = require('./claude-db.js');

const NAMES = ['Bill', 'Bob', 'Ben'];
const TTL_MS = 5 * 60 * 1000;             // permit lifetime
const RENEW_THRESHOLD_MS = 3.5 * 60 * 1000; // --auto renews only below this remaining
const RESERVE_MS = 30 * 60 * 1000;        // expired slot stays reserved for its window

// FEAT-070 (tool.looparm): a standing /loop spec is "stale" — and withheld from
// auto-arm — when neither it nor its last successful arm has happened within
// this window. Measured from MAX(set_ms, last_armed_ms), per AC-8, NOT from
// set_ms alone (see docs/planning/acceptance/tool.looparm.md Design section).
// Env-overridable, mirroring TTL_MS/RESERVE_MS's existing precedent.
const LOOP_STALE_MS = Number(process.env.METRO_LOOP_STALE_MS) || 72 * 60 * 60 * 1000;

// FEAT-070: marker that brackets the standing-loop status block printed at the
// end of a successful checkin's stdout, mirroring claude-bow.js's SUMMARY_MARKER
// contract — claude-startup.js's printSessionSummary looks for this exact string
// to lift the block out of raw checkin output and place it inside the mandatory
// startup sequence. Keep this literal string identical in both files.
const LOOP_MARKER = '── STANDING LOOP ──';

// Boot id: boot time rounded to 10 s. Survives across processes in one boot,
// changes on reboot — that mismatch is how dead holders are proven dead.
const BOOT_ID = String(Math.round((Date.now() - os.uptime() * 1000) / 10000));

// ── CLI parsing ───────────────────────────────────────────────────────────────

const argv = process.argv.slice(2);
const command = argv[0] || 'read';
const positional = [];
const flags = {};
// Value-consuming flags — every other `--flag` is treated as a bare boolean.
// AC-5 (FEAT-069): `--to`/`--body-file` MUST be listed here, or `--to Bob`
// silently corrupts the message (`--to` becomes `true`, "Bob" lands in
// `positional` and is mistaken for message text).
// FEAT-076 AC-17/AC-18: 'hours'/'target'/'set-target' are the util command's
// own value flags — added here (not a bespoke parser) so they inherit this
// loop's existing dangling-value-flag rejection (BUG-168/BUG-196 precedent
// in claude-bow.js: a value flag with nothing after it, or immediately
// followed by another recognized flag, is a hard parse error, never a
// silent mis-parse or silent clear). '--now'/'--json' are deliberately NOT
// listed — they're plain booleans, same treatment as the other bare `--flag`
// tokens already falling through to the `else if` branch below.
const VALUE_FLAGS = new Set(['name', 'session', 'to', 'body-file', 'hours', 'target', 'set-target']);
for (let i = 1; i < argv.length; i++) {
  const a = argv[i];
  if (a.startsWith('--') && VALUE_FLAGS.has(a.slice(2))) {
    const flagName = a.slice(2);
    const next = argv[i + 1];
    // BUG (Culvert, FEAT-069 Destructive REJECT): a value-flag with no following
    // token used to fall through to `argv[++i]` running off the array end,
    // silently storing JS `undefined` as the flag's value. Downstream guards
    // like `if (flags.to !== undefined)` are then FALSE for this exact case
    // (the value literally IS undefined), so an explicitly-supplied `--to`
    // with a missing value was silently discarded and the message fell
    // through to a broadcast instead of erroring. A flag immediately followed
    // by another flag (`--to --body-file x`) is equally ambiguous — the next
    // token is clearly not this flag's value. Both are now a hard parse error.
    if (next === undefined || next.startsWith('--')) {
      console.error(
        `claude-sync: --${flagName} requires a value` +
        (next === undefined ? ', but none was given (end of arguments).'
          : `, but the next token "${next}" looks like another flag.`)
      );
      process.exit(1);
    }
    flags[flagName] = next;
    i++;
  }
  else if (a.startsWith('--')) { flags[a.slice(2)] = true; }
  else { positional.push(a); }
}

const WINDOW_ID = process.env.CLAUDE_CODE_SESSION_ID || process.env.CLAUDE_SESSION_ID || '';

// ── DB helpers ────────────────────────────────────────────────────────────────

async function connect() {
  return connectCLI('claude-sync');
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
  // FEAT-069 (tool.syncmsg): directed/broadcast messages + per-identity read
  // cursor. Name-keyed (matching sync_permits' own keying), NOT window-keyed
  // (matching sync_window_map would deliver to the wrong entity — see
  // docs/planning/acceptance/tool.syncmsg.md "What 'directed message' means").
  await db.query(`CREATE TABLE IF NOT EXISTS sync_messages (
    id        INT AUTO_INCREMENT PRIMARY KEY,
    ts        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    from_name VARCHAR(16) NULL,
    to_name   VARCHAR(16) NULL,
    body      TEXT NOT NULL
  ) ENGINE=InnoDB`);
  await db.query(`CREATE TABLE IF NOT EXISTS sync_read_cursor (
    name         VARCHAR(16) PRIMARY KEY,
    last_read_id INT NOT NULL DEFAULT 0
  ) ENGINE=InnoDB`);
  // FEAT-076 (tool.agentlog, AC-1): agent dispatch/stop event log. Created
  // ONLY here — hooks (claude-dispatch-guard.js, claude-agent-stop.js) never
  // run DDL themselves; an ER_NO_SUCH_TABLE there is a stderr note + exit 0
  // fail-open, never a schema-creation attempt from inside a hook. Append-
  // only by usage (AC-2): no code path anywhere issues UPDATE/DELETE against
  // this table.
  await db.query(`CREATE TABLE IF NOT EXISTS sync_dispatch_events (
    id             INT AUTO_INCREMENT PRIMARY KEY,
    ts             TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    event          ENUM('dispatch','stop') NOT NULL,
    session_id     CHAR(36) NULL,
    name           VARCHAR(16) NULL,
    subagent_type  VARCHAR(64) NULL,
    description    VARCHAR(255) NULL,
    bow_codes      VARCHAR(255) NULL,
    prompt_chars   INT NULL,
    INDEX idx_dispatch_ts (ts),
    INDEX idx_dispatch_session (session_id)
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`);
  // FEAT-070 (tool.looparm): standing /loop configuration, one row per identity.
  // Deliberately NO boot_id column — unlike sync_permits, this table's entire
  // purpose is to survive a reboot (see tool.looparm.md Design section); gating
  // it on BOOT_ID would defeat the item's own title.
  await db.query(`CREATE TABLE IF NOT EXISTS sync_loop_config (
    name           VARCHAR(16) PRIMARY KEY,
    spec           TEXT NOT NULL,
    set_ms         BIGINT NOT NULL,
    set_by_session CHAR(36) NULL,
    last_armed_ms  BIGINT NULL,
    armed_count    INT NOT NULL DEFAULT 0
  ) ENGINE=InnoDB`);
  // Pre-existing on the real machine DB (CLAUDE.md: "Created 2026-08-08",
  // project facts key/value store) but NOT previously created by this file —
  // FEAT-076's resolveTargetLanes()/`util --set-target` need it to exist on
  // any fresh/test DB too, so it's declared here, idempotently, same as
  // every other table in this function. Schema matches the live table
  // exactly (meta_key PK, meta_value text, updated_at auto-touched).
  await db.query(`CREATE TABLE IF NOT EXISTS project_meta (
    meta_key   VARCHAR(64) PRIMARY KEY,
    meta_value TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
  ) ENGINE=InnoDB`);
  for (const n of NAMES) {
    await db.query('INSERT IGNORE INTO sync_permits (name) VALUES (?)', [n]);
    await db.query('INSERT IGNORE INTO sync_read_cursor (name, last_read_id) VALUES (?, 0)', [n]);
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

async function printSuccess(name, sessionId, db) {
  console.log(`YOU ARE: ${name}`);
  console.log(`Session: ${sessionId}`);
  console.log(`Permit TTL: 5 minutes — auto-renewed by the PostToolUse hook while you work.`);
  console.log(`Prefix every response with "${name.toLowerCase()}>".`);
  // Startup summary: BOW state (proves the metro DB answers), Vestige, git sync.
  // A summary failure must never cost the checkin — but say so, don't go silent.
  try {
    await require('./claude-bow.js').printStartupSummary(db);
  } catch (err) {
    console.log(`(startup summary unavailable: ${err.message})`);
  }
  // FEAT-076 AC-19: one-line utilisation summary, between the BOW summary
  // above and the standing-loop check below — failure-tolerant like both of
  // its neighbours, must never abort checkin over a DB blip or a table that
  // hasn't been created yet on a fresh install.
  try {
    await printUtilSummary(db);
  } catch (err) {
    console.log(`(utilisation unavailable: ${err.message})`);
  }
  // FEAT-070 (AC-6/AC-7/AC-8/AC-9): standing-loop auto-arm. Only prints
  // anything at all when `name` has a sync_loop_config row (AC-7: silent,
  // byte-identical no-op otherwise). Runs once per printSuccess call, which
  // is itself only reached once per successful checkin resolution — so this
  // is naturally the "exactly once, only for the identity that actually
  // resolved" behaviour AC-9 requires, with no extra bookkeeping needed.
  try {
    await printLoopArmStatus(db, name);
  } catch (err) {
    console.log(`(standing loop check unavailable: ${err.message})`);
  }
}

/**
 * FEAT-070 (AC-6/AC-8/AC-9): look up `name`'s standing /loop config (if any)
 * and either arm it (updating last_armed_ms/armed_count) or, if stale, report
 * it without arming. Prints nothing when no row exists (AC-7). The printed
 * block is bracketed by LOOP_MARKER so claude-startup.js can lift it out of
 * raw checkin stdout and place it inside the mandatory startup sequence.
 */
async function printLoopArmStatus(db, name) {
  // FEAT-070 Destructive REJECT, finding #3 (AC-9 "exactly once" race): the
  // original code ran an unlocked SELECT-then-UPDATE outside cmdCheckin's own
  // transaction, so N concurrent checkins for the same identity could all
  // read the same pre-update row and each print the MANDATORY instruction,
  // with armed_count incrementing racily instead of exactly once. Mirror the
  // file's existing `lockedPermits`/`FOR UPDATE` pattern: open a dedicated
  // transaction here, lock the identity's row with SELECT ... FOR UPDATE, and
  // do the read + conditional update inside it, so concurrent callers
  // serialize on this row and each sees a consistent, up-to-date state.
  await db.beginTransaction();
  let rows;
  try {
    [rows] = await db.query('SELECT * FROM sync_loop_config WHERE name=? FOR UPDATE', [name]);
    if (!rows.length) {
      await db.commit();
      return; // AC-7: nothing configured -> print nothing at all
    }
    const row = rows[0];
    const now = Date.now();
    const lastActivityMs = Math.max(Number(row.set_ms), row.last_armed_ms != null ? Number(row.last_armed_ms) : 0);
    const ageMs = now - lastActivityMs;

    if (ageMs > LOOP_STALE_MS) {
      // AC-8: stale — withhold the arm instruction, report instead. NOT armed:
      // last_armed_ms/armed_count are untouched.
      await db.commit();
      console.log('');
      console.log(LOOP_MARKER);
      console.log(`STALE STANDING LOOP — NOT auto-armed`);
      console.log(`Identity: ${name}`);
      console.log(`Stored spec: ${row.spec}`);
      console.log(`Age: ${fmtMs(ageMs)} since last set/arm (stale threshold: ${fmtMs(LOOP_STALE_MS)}).`);
      console.log(`Resolve: node claude-sync.js loop-clear   (cancel it)`);
      console.log(`      or: node claude-sync.js loop-set "${row.spec}"   (explicitly refresh it)`);
      return;
    }
    await db.query('UPDATE sync_loop_config SET last_armed_ms=?, armed_count=armed_count+1 WHERE name=?', [now, name]);
    await db.commit();
    console.log('');
    console.log(LOOP_MARKER);
    console.log(`MANDATORY: invoke \`/loop ${row.spec}\` as one of your first actions this session (standing loop, FEAT-070).`);
  } catch (err) {
    try { await db.rollback(); } catch { /* connection may already be dead */ }
    throw err;
  }
}

/**
 * FEAT-069 (AC-7/AC-8/AC-9/AC-10/AC-11): deliver unread messages for `name`
 * (directed to them, or broadcast) and advance their read cursor to the
 * highest delivered id — all inside the caller's already-open transaction,
 * so a message is never lost silently (commit-then-cursor-advances-together,
 * at-least-once never at-most-once). Cursor rows are always pre-seeded by
 * ensureSchema, so a plain UPDATE (never an upsert) is correct here.
 */
async function deliverUnread(db, name) {
  const [cursorRows] = await db.query('SELECT last_read_id FROM sync_read_cursor WHERE name=?', [name]);
  const lastReadId = cursorRows.length ? Number(cursorRows[0].last_read_id) : 0;
  const [msgs] = await db.query(
    'SELECT id, ts, from_name, body FROM sync_messages WHERE (to_name = ? OR to_name IS NULL) AND id > ? ORDER BY id ASC',
    [name, lastReadId]
  );
  if (msgs.length) {
    const maxId = msgs[msgs.length - 1].id;
    await db.query('UPDATE sync_read_cursor SET last_read_id=? WHERE name=?', [maxId, name]);
  }
  return msgs;
}

/** Plain-text rendering of delivered messages — terminal tool output, no UI richness. */
function printUnread(msgs) {
  if (!msgs.length) return;
  console.log('');
  console.log('UNREAD MESSAGES:');
  for (const m of msgs) {
    const ts = m.ts instanceof Date ? m.ts.toISOString().replace('T', ' ').slice(0, 19) : String(m.ts);
    console.log(`  [${ts}] ${m.from_name || 'unknown'}: ${m.body}`);
  }
}

/**
 * Resolve the permit row held by this window (active only unless allowStale).
 *
 * `--session <uuid>` fallback below exists for legitimate plain-terminal /
 * wake-recovery usage where CLAUDE_CODE_SESSION_ID isn't set, matching by
 * the permit's own DB-issued session id (plain-terminal usage). Used by
 * read/renew/status paths. NOT used by loop-set/loop-clear/loop-show — see
 * `findMineBySessionSecret` below, which those three commands use instead.
 */
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

/**
 * Resolve a permit strictly by its own DB-issued session SECRET — WINDOW_ID
 * is not consulted at all. FEAT-070 Destructive REJECT round 2, finding A
 * (Marrow): round 1's fix (`noSessionFallback`) only closed the `--session`
 * FLAG path while leaving the exact same trust placed in the ENV VAR form of
 * the identical idea — `WINDOW_ID` (`CLAUDE_CODE_SESSION_ID`/
 * `CLAUDE_SESSION_ID`) is populated straight from an environment variable
 * that ANY process fully controls, including a hostile one holding no
 * permit at all. Per this file's own header comment, that value "arrives via
 * env var (hooks set it from the hook stdin payload)" — i.e. it is ambient
 * to every hook invocation and visible output within a session, not a
 * secret an attacker needs privileged access to learn. Matching `row.
 * window_id === WINDOW_ID` therefore proves nothing: an attacker who merely
 * learns another identity's session UUID (from logs, transcripts, error
 * text) can set that exact value in their OWN process's env and pass every
 * WINDOW_ID-based check with zero flags, zero permit of their own.
 *
 * The one value in this system that genuinely is unpredictable and is
 * disclosed to exactly one process — the one that actually performed the
 * checkin — is the permit's own `session_id`: a `crypto.randomUUID()` minted
 * SERVER-SIDE inside `acquire()` (line ~244) and printed exactly once, in
 * that checkin call's own stdout ("Session: <uuid>", see `printSuccess`).
 * It is never copied into the shared hook env, never derivable from
 * WINDOW_ID, and never disclosed to any process that didn't either perform
 * the checkin itself or have that output deliberately relayed to it.
 *
 * `loop-set`/`loop-clear`/`loop-show` — the three commands that let the
 * resolved identity WRITE or read another identity's standing-loop row —
 * now authenticate ONLY against this secret, via a MANDATORY `--session
 * <id>` flag (the id printed at your own checkin). There is no WINDOW_ID
 * branch for these three commands at all — matching on WINDOW_ID string
 * equality, flag or env, was exactly the hole this closes. This does change
 * the zero-flag ergonomics for these three commands specifically (a
 * deliberate, security-motivated contract change, not an oversight): a
 * caller must capture and pass forward the session id its own checkin
 * printed, the same way it would handle any other credential a command
 * prints once and expects reused.
 */
function findMineBySessionSecret(byName, now) {
  if (!flags.session) return null;
  for (const n of NAMES) {
    const row = byName[n];
    if (row.session_id && row.session_id === flags.session && slotState(row, now) === 'ACTIVE') {
      return { row, state: 'ACTIVE' };
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
    const unread = await deliverUnread(db, mine.row.name);
    await db.commit();
    await printSuccess(mine.row.name, mine.row.session_id, db);
    printUnread(unread);
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
        const unread = await deliverUnread(db, name);
        await db.commit();
        console.log(`Evicted previous ${name} holder (human-authorised).`);
        await printSuccess(name, sessionId, db);
        printUnread(unread);
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
        const unread = await deliverUnread(db, name);
        await db.commit();
        await printSuccess(name, sessionId, db);
        printUnread(unread);
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
    const unread = await deliverUnread(db, name);
    await db.commit();
    await printSuccess(name, sessionId, db);
    printUnread(unread);
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
  const unread = await deliverUnread(db, free);
  await db.commit();
  await printSuccess(free, sessionId, db);
  printUnread(unread);
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

    // FEAT-070 (AC-11): oversight visibility — every identity's standing loop,
    // configured or not, so the lead's sweep sees all three without holding them.
    const [loopRows] = await db.query('SELECT * FROM sync_loop_config');
    const loopByName = {}; for (const r of loopRows) loopByName[r.name] = r;
    console.log('\nSTANDING LOOPS:');
    for (const n of NAMES) {
      const row = loopByName[n];
      if (!row) { console.log(`  ${n.padEnd(4)} none`); continue; }
      const setAge = fmtMs(now - Number(row.set_ms));
      const armedAge = row.last_armed_ms != null ? fmtMs(now - Number(row.last_armed_ms)) : 'never armed';
      console.log(`  ${n.padEnd(4)} "${row.spec}"  (set ${setAge} ago, last armed: ${armedAge}, armed_count=${row.armed_count})`);
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

/**
 * FEAT-069 (tool.syncmsg) — send a directed (--to <Name>) or broadcast
 * (no --to) message. Mirrors claude-bow.js's `--desc-file`/`--note-file`
 * pattern (BUG-090) for getting free text off the command line safely via
 * --body-file, for the identical shell-injection reason (AC-6).
 */
async function cmdMessage(db) {
  const inlineText = positional.join(' ');
  const bodyFile = flags['body-file'];

  // AC-6: inline text and --body-file are mutually exclusive — no partial write.
  if (inlineText && bodyFile !== undefined) {
    console.error('claude-sync: message text and --body-file may not both be supplied — pick one (mirrors BUG-090\'s --desc/--desc-file rule).');
    process.exit(1);
  }

  let body;
  if (bodyFile !== undefined) {
    try {
      body = fs.readFileSync(bodyFile, 'utf8');
    } catch (err) {
      console.error(`claude-sync: cannot read --body-file: ${err.message}`);
      process.exit(1);
    }
  } else {
    body = inlineText;
  }
  if (!body) {
    console.error('Usage: node claude-sync.js message "<text>" [--to <Name>] [--body-file <path>]');
    process.exit(1);
  }

  // AC-4: unknown --to target rejected before any write, exact reused string
  // from checkin's own validation (claude-sync.js's Unknown slot name error).
  let toName = null;
  if (flags.to !== undefined) {
    const name = NAMES.find(n => n.toLowerCase() === String(flags.to).toLowerCase());
    if (!name) {
      console.error(`Unknown slot name "${flags.to}". Valid: ${NAMES.join(', ')}`);
      process.exit(1);
    }
    toName = name;
  }

  // AC-2: requires an active permit; sender identity is mandatory, never
  // trusted from an argv flag — resolved the same way cmdClaim resolves it.
  const now = Date.now();
  const [rows] = await db.query('SELECT * FROM sync_permits');
  const byName = {}; for (const r of rows) byName[r.name] = r;
  const mine = findMine(byName, now);
  if (!mine) { console.error('No active permit — checkin before sending messages.'); process.exit(1); }

  await db.beginTransaction();
  try {
    const [result] = await db.query(
      'INSERT INTO sync_messages (from_name, to_name, body) VALUES (?, ?, ?)',
      [mine.row.name, toName, body]
    );
    // AC-9: sender never sees their own just-sent message flagged unread —
    // advance the sender's own cursor past it, within this same transaction.
    await db.query(
      'UPDATE sync_read_cursor SET last_read_id = GREATEST(last_read_id, ?) WHERE name=?',
      [result.insertId, mine.row.name]
    );
    await db.commit();
  } catch (err) {
    await db.rollback();
    throw err;
  }
  console.log(`Message sent${toName ? ` to ${toName}` : ' (broadcast)'}.`);
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

// FEAT-070 (AC-2/AC-3): configure the caller's own standing /loop spec.
// Positional text (like `write`'s message), not a --spec value-flag.
async function cmdLoopSet(db) {
  const spec = positional.join(' ').trim();
  if (!spec) {
    console.error('Usage: node claude-sync.js loop-set "<interval> <command>"');
    process.exit(1);
  }
  // FEAT-070 Destructive REJECT, finding #2 (round 1, extended round 2 —
  // Marrow): `spec` is displayed verbatim inside the "MANDATORY" startup
  // block (printLoopArmStatus / printSessionSummary). A multi-line or
  // control-character spec can visually merge with that numbered-list
  // startup formatting and masquerade as a genuine instruction (prompt
  // injection against whoever reads the startup output next). Round 1's
  // blocklist (`/[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]|[\r\n]/`) was pure ASCII
  // C0/DEL and missed TAB, the Unicode line/paragraph separators U+2028/
  // U+2029 (real JS line terminators, NOT matched by \r\n), the bidi/RTL-
  // override characters U+202A-U+202E and U+2066-U+2069, and zero-width
  // characters U+200B-U+200D/U+FEFF — all of which reach the same "visually
  // merge with startup text" vector via Unicode instead of ASCII. Blocklists
  // of "dangerous" characters have now proven incomplete TWICE in this exact
  // file (round 1 -> round 2), so this switches to an ALLOWLIST: a standing
  // loop spec is always a single short command line, so only printable
  // ASCII (U+0020 space through U+007E tilde) is accepted at all. Anything
  // outside that range — control chars, line/paragraph separators, bidi
  // overrides, zero-width characters, or any other Unicode codepoint,
  // known or not-yet-discovered — is rejected by construction, not by name.
  if (!/^[\x20-\x7E]*$/.test(spec)) {
    console.error('loop-set: spec must be printable ASCII only (space through ~) — no control characters, Unicode line/paragraph separators, bidi overrides, zero-width characters, or other non-ASCII codepoints. A standing loop spec is always a single short plain command line.');
    process.exit(1);
  }
  const now = Date.now();
  const [rows] = await db.query('SELECT * FROM sync_permits');
  const byName = {}; for (const r of rows) byName[r.name] = r;
  // FEAT-070 Destructive REJECT round 2, finding A: authenticate via the
  // server-issued session secret only — see findMineBySessionSecret's doc
  // comment for why WINDOW_ID (env-var-sourced) is not proof of identity.
  const mine = findMineBySessionSecret(byName, now);
  if (!mine) {
    console.error('No active permit — checkin, then pass --session <id printed by YOUR OWN checkin> before setting a standing loop.');
    process.exit(1);
  }
  // AC-2: a re-set is a fresh commitment — armed_count/last_armed_ms reset.
  await db.query(
    `REPLACE INTO sync_loop_config (name, spec, set_ms, set_by_session, last_armed_ms, armed_count)
     VALUES (?, ?, ?, ?, NULL, 0)`,
    [mine.row.name, spec, now, mine.row.session_id]
  );
  await log(db, mine.row.name, `standing loop set: ${spec}`);
  console.log(`${mine.row.name} standing loop set: ${spec}`);
}

// FEAT-070 (AC-4): clear the caller's OWN row only — never any other identity's.
async function cmdLoopClear(db) {
  const now = Date.now();
  const [rows] = await db.query('SELECT * FROM sync_permits');
  const byName = {}; for (const r of rows) byName[r.name] = r;
  // FEAT-070 Destructive REJECT round 2, finding A: see findMineBySessionSecret's doc comment.
  const mine = findMineBySessionSecret(byName, now);
  if (!mine) {
    console.error('No active permit — checkin, then pass --session <id printed by YOUR OWN checkin> before clearing a standing loop.');
    process.exit(1);
  }
  const [res] = await db.query('DELETE FROM sync_loop_config WHERE name=?', [mine.row.name]);
  if (!res.affectedRows) {
    console.log(`${mine.row.name}: nothing to clear — no standing loop configured.`);
    return;
  }
  await log(db, mine.row.name, 'standing loop cleared');
  console.log(`${mine.row.name} standing loop cleared.`);
}

// FEAT-070 (AC-5): show the caller's own standing loop state, never crashing
// on absence.
async function cmdLoopShow(db) {
  const now = Date.now();
  const [rows] = await db.query('SELECT * FROM sync_permits');
  const byName = {}; for (const r of rows) byName[r.name] = r;
  // FEAT-070 Destructive REJECT round 2, finding A: see findMineBySessionSecret's doc comment.
  const mine = findMineBySessionSecret(byName, now);
  if (!mine) {
    console.error('No active permit — checkin, then pass --session <id printed by YOUR OWN checkin> before checking your standing loop.');
    process.exit(1);
  }
  const [loopRows] = await db.query('SELECT * FROM sync_loop_config WHERE name=?', [mine.row.name]);
  if (!loopRows.length) {
    console.log('no standing loop configured');
    return;
  }
  const row = loopRows[0];
  const setAge = fmtMs(now - Number(row.set_ms));
  const armedAge = row.last_armed_ms != null ? fmtMs(now - Number(row.last_armed_ms)) : 'never armed';
  console.log(`${mine.row.name} standing loop: "${row.spec}"`);
  console.log(`  set ${setAge} ago, last armed: ${armedAge}, armed_count=${row.armed_count}`);
}

/**
 * FEAT-076 AC-19: the one-line utilisation summary shown at checkin.
 * Explicit "no dispatch events" message on an empty table (never a silent
 * 0/0), and its own errors propagate to printSuccess()'s try/catch above —
 * this function has no fallback of its own beyond fetching + formatting.
 */
async function printUtilSummary(db) {
  const { currentRunning, resolveTargetLanes } = require('./claude-dispatch-log.js');
  const since = new Date(Date.now() - 12 * 60 * 60 * 1000);
  const [rows] = await db.query(
    `SELECT event, ts, session_id, name FROM sync_dispatch_events WHERE ts >= ? ORDER BY ts`,
    [since]
  );
  if (!rows.length) {
    console.log('Utilisation (12h): no dispatch events recorded yet.');
    return;
  }
  const { running } = currentRunning(rows, { now: Date.now() });
  const { target, source } = await resolveTargetLanes(db);
  console.log(`Utilisation (12h): ${running}/${target} lanes running now (target: ${source}). Full report: node claude-sync.js util`);
}

/**
 * FEAT-076 (tool.agentlog) AC-17: `util` — hourly utilisation report built
 * entirely from claude-dispatch-log.js's pure functions; this command's own
 * job is just DB I/O (fetch events, optionally persist a new target) and CLI
 * flag handling. `--hours N` (default 12), `--target N` (one-run override,
 * never persisted), `--set-target N` (persists to project_meta and prints a
 * confirmation), `--now` (compact RUNNING NOW block), `--json` (machine-
 * readable bucket dump). AC-18's dangling-value-flag rejection is inherited
 * for free from the shared argv loop above (hours/target/set-target are in
 * VALUE_FLAGS) — no separate parsing here.
 */
async function cmdUtil(db) {
  const {
    bucketHours, formatUtilTable, resolveTargetLanes, currentRunning, sweepConcurrency, DEFAULT_CAP_MS,
  } = require('./claude-dispatch-log.js');

  const hours = flags.hours !== undefined ? Number(flags.hours) : 12;
  if (!Number.isFinite(hours) || hours <= 0) {
    console.error('claude-sync: --hours must be a positive number.');
    process.exit(1);
  }

  if (flags['set-target'] !== undefined) {
    const v = Number(flags['set-target']);
    if (!Number.isFinite(v) || v <= 0) {
      console.error('claude-sync: --set-target must be a positive number.');
      process.exit(1);
    }
    await db.query(
      `INSERT INTO project_meta (meta_key, meta_value) VALUES ('dispatch_target_lanes', ?)
       ON DUPLICATE KEY UPDATE meta_value = VALUES(meta_value)`,
      [String(v)]
    );
    console.log(`dispatch_target_lanes set to ${v} in project_meta.`);
  }

  let target, targetSource;
  if (flags.target !== undefined) {
    const v = Number(flags.target);
    if (!Number.isFinite(v) || v <= 0) {
      console.error('claude-sync: --target must be a positive number.');
      process.exit(1);
    }
    target = v;
    targetSource = 'one-run --target override (not persisted)';
  } else {
    const resolved = await resolveTargetLanes(db);
    target = resolved.target;
    targetSource = resolved.source;
  }

  const now = Date.now();
  // Fetch window generous enough that a dispatch just inside `hours` ago,
  // still open, has its capMs-synthetic-close correctly computed even if
  // that close time falls after the window start.
  const since = new Date(now - hours * 60 * 60 * 1000 - DEFAULT_CAP_MS);
  const [rows] = await db.query(
    `SELECT event, ts, session_id, name FROM sync_dispatch_events WHERE ts >= ? ORDER BY ts`,
    [since]
  );

  if (flags.now) {
    const { running, byName } = currentRunning(rows, { now });
    const { intervals } = sweepConcurrency(rows, {});
    const openNow = intervals.filter((iv) => iv.start <= now && iv.end > now);
    console.log(`RUNNING NOW: ${running}/${target} lanes (target: ${targetSource})`);
    if (openNow.length) {
      const oldestStart = Math.min(...openNow.map((iv) => iv.start));
      console.log(`  oldest open dispatch: ${Math.round((now - oldestStart) / 60000)}m ago`);
    }
    const names = Object.keys(byName).filter((n) => byName[n] > 0).sort();
    if (!names.length) {
      console.log('  (no lanes currently running)');
    } else {
      for (const n of names) console.log(`  ${n.padEnd(16)} ${byName[n]}`);
    }
    return;
  }

  const bucketed = bucketHours(rows, { hours, now, targetLanes: target });

  if (flags.json) {
    console.log(JSON.stringify({ hours, target, targetSource, rows: bucketed }, null, 2));
    return;
  }

  if (!rows.length) {
    console.log(`No dispatch events in the last ${hours}h.`);
  }
  console.log(`Utilisation — last ${hours}h, target ${target} lanes (${targetSource}):`);
  console.log(formatUtilTable(bucketed));
}

async function runCli() {
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
      case 'message': await cmdMessage(db); break;
      case 'claim': await cmdClaim(db); break;
      case 'release': await cmdRelease(db); break;
      case 'gc': await cmdGc(db); break;
      case 'loop-set': await cmdLoopSet(db); break;
      case 'loop-clear': await cmdLoopClear(db); break;
      case 'loop-show': await cmdLoopShow(db); break;
      case 'util': await cmdUtil(db); break;
      default:
        console.error(`Unknown command: ${command}`);
        console.error('Commands: init, checkin, renew, ping, checkout, status, read, write, message, claim, release, gc, loop-set, loop-clear, loop-show, util');
        process.exit(1);
    }
  } catch (err) {
    try { await db.rollback(); } catch { /* no open tx */ }
    console.error(`claude-sync error: ${err.message}`);
    process.exit(1);
  } finally {
    await db.end().catch(() => {});
  }
}

module.exports = {
  NAMES, connect, ensureSchema, findMine, findMineBySessionSecret, slotState, deliverUnread, printUnread,
  cmdCheckin, cmdRenew, cmdMessage, cmdCheckout, cmdStatus, cmdWrite, cmdClaim,
  cmdRelease, cmdGc,
  cmdLoopSet, cmdLoopClear, cmdLoopShow, printLoopArmStatus,
  LOOP_MARKER, LOOP_STALE_MS,
  cmdUtil, printUtilSummary,
};

// ── Entry ─────────────────────────────────────────────────────────────────────
if (require.main === module) {
  runCli();
}
