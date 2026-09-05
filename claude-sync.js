// Module key: tool.sync (see code.json; GUID eae1b5fc-9fc9-46fa-af15-5333c5db21f8)
// Spec ref: M0-ENG §5 (hooks); session coordination

/**
 * claude-sync.js — Metropolis session coordination (MariaDB backend)
 *
 * Port of the Prix Six claude-sync v2.2 DHCP-style permit system onto the
 * project's own `metro` MariaDB database (localhost:3306). Same protocol:
 * four named slots (Bill, Ben, Bev, Bro — Bro added 2026-08-20, Ben PARKED
 * 2026-08-19), 5-minute TTL permits, auto-renewal by the PostToolUse hook,
 * wake recovery, reserved slots, human-only force-evict.
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
 *                                 prints an UNREAD COUNT line ("UNREAD: N message(s) -
 *                                 run read to receive them") inside that same summary
 *                                 region when the resolved identity has unread directed/
 *                                 broadcast messages (see `message`, below) — but, as of
 *                                 FEAT-107, checkin no longer PRINTS THE MESSAGE BODIES
 *                                 and does NOT advance the read cursor. `read` is now the
 *                                 sole delivering + cursor-advancing command (see below).
 *                                 This split exists because checkin's own stdout is
 *                                 routinely piped/redirected by callers who only want the
 *                                 identity line (`checkin --any | Select-String YOU`, or
 *                                 the PowerShell/CI habit of `> $null`/`Out-Null`) — when
 *                                 checkin ALSO consumed the cursor, that redirection
 *                                 silently destroyed the unread messages (2026-08-20: nine
 *                                 messages lost this way). Wake recovery (`renew`'s stale-
 *                                 permit and previous-slot-reclaim paths) gets the same
 *                                 count-only treatment, for the same reason.
 *           [--force --human-ok]  Force-evict a live holder (BUG-354 r8: the
 *                                 TARGET's server-issued session secret must be
 *                                 presented via --session — a bare flag proves
 *                                 nothing, and the eviction otherwise refuses)
 *   renew [--auto] [--session ID] - Extend permit; --auto only renews when < 3.5 min left.
 *                                 --auto stays fully silent, including about messages
 *                                 (matches today's heartbeat-only behaviour). A genuine
 *                                 (non-auto) wake-recovery reclaim prints the same UNREAD
 *                                 COUNT line as checkin — never bodies, never advances
 *                                 the cursor.
 *   ping [--session ID]         - Renew + heartbeat + status line
 *   checkout [--session ID]     - Release this window's permit
 *   checkout --force <Name>     - Admin: evict a specific permit holder (BUG-354
 *                                 r8: requires --session <the target's secret>;
 *                                 no-secret releases are refused)
 *   status [--session ID]       - Show all slots, marking this window's
 *   read                        - Full coordination state: slots, activity log, NO-TOUCH
 *                                 zones, standing loops — PLUS (FEAT-107) delivers any
 *                                 unread directed/broadcast messages for the resolved
 *                                 identity (oldest-first) and advances its read cursor.
 *                                 `read` is now the ONLY command that delivers message
 *                                 bodies or moves the cursor; checkin/renew only ever
 *                                 report a count (see above).
 *   write "message"             - Log a milestone to the activity log
 *   message "<text>" [--to <Name>] [--body-file <path>]
 *                                - Send a directed (--to) or broadcast (no --to) message,
 *                                  delivered to the resolved-name recipient's next `read`
 *                                  (FEAT-069/FEAT-107). Requires an active permit.
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

const NAMES = ['Bill', 'Ben', 'Bev', 'Bro'];
// Bro added 2026-08-20 (Aaron-directed): fourth worker slot. Seeded into
// sync_permits on the next checkin like every NAMES entry (seedSlots).
// Bob was retired permanently (Aaron, 2026-08-18) but the slot row/history
// stays in the DB (operator handles data cleanup, never this file — GR#24).
// Kept as its own list, not just "absent from NAMES", so every caller-
// supplied-name path (checkin --name / CLAUDE_IDENTITY, message --to,
// checkout --force) can give a clear RETIRED rejection instead of the
// generic "Unknown slot name" a plain NAMES.find() miss would produce —
// that generic message reads like a typo, not a deliberate retirement, and
// is exactly how a stale CLAUDE_IDENTITY=Bob env var or muscle-memory
// `--name Bob` produced the 2026-08-18 overnight incident (see isRetired /
// retiredMessage below).
const RETIRED = ['Bob'];
// Ben is PARKED (Aaron, 2026-08-19: weeks). Unlike RETIRED, the slot STAYS
// SEEDED and LISTED — Ben is expected to return — but it must never be
// occupied or addressed while parked (BUG-354 D2). Kept as its own list (not
// merged into RETIRED) so error text can distinguish "parked" from "retired"
// and a future unpark is a one-line removal, exactly like the retirement flip.
const PARKED = ['Ben'];
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

/** Path to this window's per-window session-key file (BUG-354 r4, moved r5):
 *  it holds the server-issued session secret so the ping/startup hooks and the
 *  operator can present it as an explicit `--session` without trusting the
 *  ambient env. Suffixed with the DB name when METRO_DB_NAME is set, so a test
 *  run's key files can never collide with a live window's.
 *
 * BUG-354 r5 (Warden round 4, F3): the file lives in the PER-USER
 *  `os.homedir()/.claude/session-keys/` directory, never the shared checkout —
 *  the checkout is a git repo every lane shares, so a file written there is (a)
 *  visible to every concurrent session on the machine and (b) writable/deletable
 *  by any process that can touch the tree, which is exactly the surface an
 *  env-spoofing attacker used in round 4 (delete the victim's key file, then a
 *  fresh acquire wrote the ATTACKER's secret into the victim's path). A
 *  per-operator home directory is outside the repo and readable only by the
 *  operator account. */
function sessionKeyPath(windowId) {
  if (!windowId) return null;
  const dbTag = process.env.METRO_DB_NAME ? `-${process.env.METRO_DB_NAME}` : '';
  return path.join(os.homedir(), '.claude', 'session-keys', `.session-key-${windowId}${dbTag}`);
}

/** BUG-354 r4/r5: the checkout-path location r4 wrote key files to before F3
 *  moved them per-user. Used only for one-time migration in readSessionKey. */
const LEGACY_SESSION_KEY_DIR = path.join(__dirname, '.claude');

/** Read this window's server-issued session secret — the exact read the
 *  ping/startup hooks and the test harness use. Returns '' (never throws) when
 *  no key file exists. Migrates a legacy r4 checkout-path key file to the
 *  per-user location on first read, best-effort: the migration is a
 *  convenience, never a reason for a hook to fail. */
function readSessionKey(windowId) {
  if (!windowId) return '';
  const p = sessionKeyPath(windowId);
  try {
    const s = fs.readFileSync(p, 'utf8').trim();
    if (s) return s;
  } catch { /* not yet at the per-user path */ }
  try {
    const dbTag = process.env.METRO_DB_NAME ? `-${process.env.METRO_DB_NAME}` : '';
    const legacy = path.join(LEGACY_SESSION_KEY_DIR, `.session-key-${windowId}${dbTag}`);
    const s = fs.readFileSync(legacy, 'utf8').trim();
    if (s) {
      try {
        fs.mkdirSync(path.dirname(p), { recursive: true });
        fs.writeFileSync(p, s, 'utf8');
        fs.unlinkSync(legacy); // best-effort — never fail a hook over cleanup
      } catch { /* migration convenience only */ }
      return s;
    }
  } catch { /* no legacy file either */ }
  return '';
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
  // BUG-354 r4 (Warden round 3): the per-window session-key file is written
  // ONLY when the caller can prove it IS the window that file belongs to —
  // either no key file exists yet (genuinely fresh window) or the caller
  // presents the existing secret. A caller whose env merely CLAIMS this window
  // id (spoofing) must never be able to overwrite the real owner's key file:
  // that would redirect the owner's next hook renew onto the attacker's row.
  try {
    const key = sessionKeyPath(WINDOW_ID);
    if (key) {
      let existing = '';
      try { existing = fs.readFileSync(key, 'utf8').trim(); } catch { /* none yet */ }
      if (!existing || flags.session === existing) {
        // BUG-354 r6 (r5 REJECT finding 2): the per-user session-keys
        // directory is NOT guaranteed to exist (fresh machine/profile). Without
        // this mkdir the first checkin's writeFileSync throws ENOENT, the
        // catch swallows it, and the window ends up with no key file — so its
        // hooks read an empty secret and the next checkin self-locks (GR#17).
        fs.mkdirSync(path.dirname(key), { recursive: true });
        fs.writeFileSync(key, sessionId, 'utf8');
      }
      // else: key file exists and this caller cannot present it — leave the
      // owner's secret intact. The DB row still claims the slot, but under a
      // secret only this caller knows; it expires and is GC'd harmlessly.
    }
  } catch { /* key file is an automation convenience — never fail a checkin */ }
  return sessionId;
}

/** BUG-354 r4: ensure this window's per-window session-key file holds `secret`.
 *  The renew-of-self paths keep the row's existing server-issued secret (they
 *  do not re-mint), so they must also ensure the key file reflects it — a
 *  window whose key file is missing (e.g. a row acquired under pre-r4 code)
 *  can bootstrap itself on its own next renew. Only writes when the file is
 *  absent or already matches; never overwrites a different secret. */
function ensureSessionKey(secret) {
  try {
    const key = sessionKeyPath(WINDOW_ID);
    if (!key || !secret) return;
    let existing = '';
    try { existing = fs.readFileSync(key, 'utf8').trim(); } catch { /* none */ }
    if (!existing || existing === secret) {
      // BUG-354 r6 (r5 REJECT finding 2): same ENOENT as acquire — ensure the
      // per-user session-keys directory exists before the first key write.
      fs.mkdirSync(path.dirname(key), { recursive: true });
      fs.writeFileSync(key, secret, 'utf8');
    }
  } catch { /* convenience — never fail a command over it */ }
}

/** BUG-354 F2: release any OTHER non-free permit row still belonging to THIS
 *  window before acquiring a new slot. Root cause: after an idle expiry a
 *  window's old slot lingers as RESERVED (released=0) for up to RESERVE_MS,
 *  and the fresh-acquire path (`checkin --name <free>` / `--any`) took the new
 *  slot WITHOUT releasing it — leaving one window holding two live rows and
 *  blocking that slot for everyone else. Plain-terminal acquires (no WINDOW_ID)
 *  have no window key of their own and never leave such rows, so this is
 *  window-keyed only.
 *
 * BUG-354 r4 (Warden round 3): the rows released are keyed by the window id of
 *  a SECRET-PROVEN row (`provenRow`) — never by the ambient WINDOW_ID env, which
 *  any process can set to another window's value. A caller that cannot present a
 *  row's server-issued session secret must not release any row, including the
 *  "other" rows of the window id it merely claims.
 *
 * BUG-354 r5 (Warden round 4, F1): the sweep is additionally SESSION-scoped by
 *  state — it may release ONLY stale RESERVED rows (expired but within reserve,
 *  the exact legacy-leak leftovers it exists to clean), NEVER an ACTIVE row.
 *  Round 4's repro: an env-spoofer who minted a ghost row under the victim's
 *  window id (the F2 hole) then swapped identities, and the r4 window_id sweep
 *  released the VICTIM's LIVE row because it happened to share the window id.
 *  Under r5 the F2 refusal blocks the ghost mint outright, and this sweep is
 *  belt-and-braces: a live permit of ANY name is untouchable by it. A legitimate
 *  window's own stale leftovers are RESERVED (expired) rows, so nothing
 *  legitimate is lost by refusing to touch ACTIVE rows. */
async function releaseOtherRowsForWindow(db, exceptName, provenRow) {
  if (!provenRow || !provenRow.window_id) return;
  const now = Date.now();
  const [stale] = await db.query(
    `SELECT name FROM sync_permits
      WHERE window_id=? AND released=0 AND name<>? AND expires_ms < ?
        AND expires_ms + ? > ?
      FOR UPDATE`,
    [provenRow.window_id, exceptName, now, RESERVE_MS, now]);
  if (stale.length) {
    await db.query(
      'UPDATE sync_permits SET released=1 WHERE window_id=? AND released=0 AND name<>? AND expires_ms < ?',
      [provenRow.window_id, exceptName, now]);
  }
}

/** Keep the statusline and prefix hook truthful: they read
 *  .claude/.identity-<window> first, falling back to the shared .identity.
 *  BUG-354 D3: identity is keyed PER-WINDOW, not per-checkout. The shared
 *  .identity is cross-window, last-checkin-wins state — writing it from every
 *  acquire let one window's standing-loop checkin rewrite another window's
 *  prefix hook / statusline identity (the live incident: a Bev-holding window
 *  was told to answer as "bill>" then "ben>" because Bill's loop clobbered the
 *  shared file). Only the per-window marker is written when this window has an
 *  id; a plain-terminal acquire (no id) falls back to the shared file because
 *  it has no per-window key of its own. */
function writeIdentityFiles(name) {
  try {
    const dotClaude = path.join(__dirname, '.claude');
    fs.mkdirSync(dotClaude, { recursive: true });
    if (WINDOW_ID) fs.writeFileSync(path.join(dotClaude, `.identity-${WINDOW_ID}`), name.toLowerCase(), 'utf8');
    else fs.writeFileSync(path.join(dotClaude, '.identity'), name.toLowerCase(), 'utf8');
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
  // FEAT-107: unread message COUNT only — checkin no longer delivers bodies
  // or advances the read cursor (see the header comment and countUnread's
  // doc comment for why). Printed here, still inside the SUMMARY_MARKER..
  // LOOP_MARKER region of stdout, so claude-startup.js's existing slice
  // relays it into the session's mandatory startup block the same way it
  // already relays the BOW/Vestige/git lines above.
  try {
    printUnreadCount(await countUnread(db, name));
  } catch (err) {
    console.log(`(unread check unavailable: ${err.message})`);
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
 * FEAT-069/FEAT-107 (AC-7/AC-8/AC-9/AC-10/AC-11): deliver unread messages for
 * `name` (directed to them, or broadcast) and advance their read cursor to
 * the highest delivered id — all inside the caller's already-open
 * transaction, so a message is never lost silently (commit-then-cursor-
 * advances-together, at-least-once never at-most-once). Cursor rows are
 * always pre-seeded by ensureSchema, so a plain UPDATE (never an upsert) is
 * correct here.
 *
 * FEAT-107: as of the delivery-split, `read` is the ONLY caller of this
 * function — checkin and renew's wake-recovery paths use `countUnread`
 * (below) instead, which never touches the cursor. Keeping the delivering
 * and the counting paths as two separate functions (rather than one function
 * with a "deliver: boolean" flag) makes it structurally impossible for a
 * future checkin-adjacent call site to accidentally advance the cursor by
 * passing the wrong flag.
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

/**
 * FEAT-107: count-only companion to deliverUnread — same eligibility
 * (directed to `name`, or broadcast, past their current read cursor) but
 * NEVER touches sync_read_cursor and NEVER returns message bodies. This is
 * what checkin and renew's wake-recovery paths use so that a caller who
 * pipes/redirects their stdout cannot silently destroy unread messages
 * (2026-08-20 incident: `checkin > $null` consumed nine messages because the
 * old single deliverUnread() path both delivered AND advanced the cursor
 * inside checkin itself).
 */
async function countUnread(db, name) {
  const [cursorRows] = await db.query('SELECT last_read_id FROM sync_read_cursor WHERE name=?', [name]);
  const lastReadId = cursorRows.length ? Number(cursorRows[0].last_read_id) : 0;
  const [[{ cnt }]] = await db.query(
    'SELECT COUNT(*) AS cnt FROM sync_messages WHERE (to_name = ? OR to_name IS NULL) AND id > ?',
    [name, lastReadId]
  );
  return Number(cnt);
}

/** Plain-text rendering of the FEAT-107 unread COUNT line — used by checkin
 *  and renew's wake-recovery paths. Prints nothing when the count is zero,
 *  matching printUnread's existing "nothing to say, say nothing" contract
 *  for the zero-messages case. Explicitly states the delivery contract: read
 *  is required (GR#17 silent-failure detection, BUG-356). */
function printUnreadCount(n) {
  if (n > 0) console.log(`UNREAD: ${n} message(s) - checkin shows count only; run 'read' to receive them (FEAT-107 delivery split).`);
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
  // BUG-354 r5 (F5): --session secret FIRST — the server-issued secret is the
  // identity authority, so a presented secret must never be preempted by an
  // ambient WINDOW_ID match (BUG-360 tracks the residual env-based identity on
  // read/message/write/claim; this ordering is the first cut).
  if (flags.session) {
    for (const n of NAMES) {
      const row = byName[n];
      if (row.session_id === flags.session && slotState(row, now) !== 'FREE') {
        return { row, state: slotState(row, now) };
      }
    }
  }
  for (const n of NAMES) {
    const row = byName[n];
    if (!row.window_id || row.window_id !== WINDOW_ID || !WINDOW_ID) continue;
    // BUG-354 r6 (BUG-360): a bare ambient WINDOW_ID match no longer resolves
    // a permit. WINDOW_ID is env-sourced (hooks set it from stdin payload) and
    // fully controllable by a hostile process; matching it proves nothing. The
    // caller must additionally possess this window's per-user session-key file
    // AND have it match the row's server-issued secret — the same possession
    // proof readSessionKey uses. A spoofer fabricating the victim's id with no
    // key file gets no match.
    const keySecret = readSessionKey(WINDOW_ID);
    if (!keySecret || keySecret !== row.session_id) continue;
    const state = slotState(row, now);
    if (state === 'ACTIVE') return { row, state };
    if (allowStale && state === 'RESERVED') return { row, state };
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
 * The one value in this system that genuinely is unpredictable is the
 * permit's own `session_id`: a `crypto.randomUUID()` minted SERVER-SIDE inside
 * `acquire()` (line ~244) and printed exactly once, in that checkin call's own
 * stdout ("Session: <uuid>", see `printSuccess`). It is never copied into the
 * shared hook env and never derivable from WINDOW_ID. It IS also persisted to
 * this window's per-user session-key file (BUG-354 r4/r5 F3) so the hooks can
 * present it on renew/checkout — the file lives outside the shared checkout,
 * in `os.homedir()/.claude/session-keys/`, readable only by the operator
 * account, and the key is disclosed to any process that can prove possession
 * of that file (which is exactly the identity it is supposed to prove).
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
function findMineBySessionSecret(byName, now, { allowStale = false } = {}) {
  if (!flags.session) return null;
  for (const n of NAMES) {
    const row = byName[n];
    if (!row.session_id || row.session_id !== flags.session) continue;
    const state = slotState(row, now);
    if (state === 'ACTIVE') return { row, state };
    if (allowStale && state === 'RESERVED') return { row, state };
  }
  return null;
}

/** True if `candidate` (case-insensitive) names a retired slot (Bob). */
function isRetired(candidate) {
  return RETIRED.some(n => n.toLowerCase() === String(candidate).toLowerCase());
}

/** Clear rejection text for a retired-slot name, reused at every
 *  caller-supplied-name site (checkin --name / CLAUDE_IDENTITY, message
 *  --to, checkout --force) so the message is identical wherever it fires. */
function retiredMessage(candidate) {
  return `${candidate} is retired (Aaron, 2026-08-18) - live slots: ${liveNames().join(', ')}.`;
}

/** True if `candidate` (case-insensitive) names a PARKED slot (Ben). */
function isParked(candidate) {
  return PARKED.some(n => n.toLowerCase() === String(candidate).toLowerCase());
}

/** True if the slot must never be occupied or addressed — retired OR parked
 *  (BUG-354 D2 / BUG-344: parked is a third slot state needing the same
 *  isRetired-like treatment on every assignment path). */
function isUnusable(candidate) {
  return isRetired(candidate) || isParked(candidate);
}

/** Clear rejection text for a parked-slot name, mirroring retiredMessage. */
function parkedMessage(candidate) {
  return `${candidate} is parked (Aaron, 2026-08-19: weeks) - slot stays seeded but must not be occupied or addressed. Use a live slot: ${liveNames().join(', ')}.`;
}

/** Rejection text for any unusable slot name, naming the actual state. */
function unusableMessage(candidate) {
  return isParked(candidate) ? parkedMessage(candidate) : retiredMessage(candidate);
}

/** Live slot names in NAMES order — retired and parked slots excluded. */
function liveNames() {
  return NAMES.filter(n => !isUnusable(n));
}

/**
 * GR#17 silent-failure detection (BUG-356): determine if a standing loop spec
 * is "checkin-only" — i.e. it invokes the checkin command but NOT the read
 * command. After FEAT-107, checkin only prints an unread COUNT and does NOT
 * deliver message bodies or advance the read cursor — only read does. A loop
 * that calls checkin without read will check in every iteration, report a
 * rising armed_count, look healthy in every status view, and NEVER receive any
 * directed messages.
 *
 * Detection: match the actual claude-sync commands, not bare substrings. The
 * spec invokes checkin if it contains "claude-sync.js checkin" or "claude-sync
 * checkin" (the actual command line). Avoids false positives like "checkin-
 * dashboard" (a hypothetical skill) or false negatives like "readme" (a docs
 * reference). If the spec invokes a skill like "15m /oversight-sweep", we
 * can't see what the skill does internally, so we don't flag it — that's a
 * known limitation. Only specs that explicitly invoke claude-sync's checkin
 * command without also invoking read are flagged.
 */
function isCheckinOnly(loopSpec) {
  if (!loopSpec || typeof loopSpec !== 'string') return false;
  // Match "claude-sync.js checkin" or "claude-sync checkin" (the actual command)
  const hasCheckin = /claude-sync(\.js)?\s+checkin\b/.test(loopSpec);
  // Match "claude-sync.js read" or "claude-sync read" (the read command)
  const hasRead = /claude-sync(\.js)?\s+read\b/.test(loopSpec);
  return hasCheckin && !hasRead;
}

// ── Commands ──────────────────────────────────────────────────────────────────

/** BUG-354 r5 F2 / r7: refuse an acquire when this ambient window id already
 *  holds a live (ACTIVE/RESERVED) claim the caller cannot secret-prove.
 *  `claims` is the FULL list of live rows under WINDOW_ID (r7: all of them,
 *  never the NAMES-first — H2 ghost shadowing). `provenRow` is the caller's
 *  secret-resolved row, if any. A claim is provable only when its server-issued
 *  session_id matches the presented --session or provenRow's secret (the
 *  secret is the identity authority, never a name/window-id string match).
 *  Returns an error message, or null when the acquire may proceed. */
function liveWindowClaimRefusal(claims, provenRow) {
  if (!claims || !claims.length) return null;
  const provable = new Set();
  if (provenRow && provenRow.session_id) provable.add(String(provenRow.session_id));
  if (flags.session) provable.add(String(flags.session));
  const unproven = claims.filter(c => !provable.has(String(c.session_id)));
  if (!unproven.length) return null;
  return `WINDOW ID CLAIMED: this window (${WINDOW_ID}) already holds a live session (${unproven.map(c => c.name).join(', ')}) whose server-issued secret this invocation cannot present — refusing to mint a second live row. Present --session <secret> to prove this is your window, or wait for the claim to lapse.`;
}

/** BUG-354 r8 (r7 REJECT, attacker ac9ff2cc): a force-evict / checkout --force
 *  destroys a LIVE target row selected BY NAME and must therefore authenticate
 *  the TARGET row's server-issued session secret — the only operator credential
 *  this system has. The r7 F2 gate keys to the AMBIENT window id, and a fresh
 *  (or absent) window id has zero live claims under it, so the gate passes
 *  vacuously and a no-secret rogue process evicts any identity it can name
 *  (P1/P2/P3/P8/P11); an attacker presenting its OWN live secret from a fresh
 *  window is likewise not blocked (P4). Requiring flags.session === the target's
 *  session_id closes all six: only the target's own key file authorises its
 *  eviction. The legitimate operator recovery is unchanged in capability — read
 *  the hung session's secret from its per-window session-key file
 *  (os.homedir()/.claude/session-keys/.session-key-<window-id>) and present it
 *  explicitly. Returns an error message when the caller may NOT evict targetRow,
 *  else null. */
function forceEvictRefusal(targetRow) {
  if (!targetRow || !targetRow.session_id) return null;
  if (flags.session && String(flags.session) === String(targetRow.session_id)) return null;
  return `FORCE-EVICT REFUSED: evicting ${targetRow.name} (held by another live session) requires possession of THAT session's server-issued secret — a bare --force --human-ok proves nothing and this window (${WINDOW_ID || '<none>'}) cannot name a victim by authority. A human operator must read the target's secret from its per-window session-key file (os.homedir()/.claude/session-keys/.session-key-<window-id>) and present it explicitly: node claude-sync.js checkout --force ${targetRow.name} --session <target-secret>  (or checkin --name ${targetRow.name} --session <target-secret> to renew it).`;
}

async function cmdCheckin(db) {
  // BUG-354 r4 (Warden round 3): checkin ACCEPTS --session — the server-issued
  // session secret IS the identity authority for every destructive operation.
  // r2's F1 rejection was built around findMine's WINDOW_ID match; with that
  // removed, presenting the secret is the only way to prove a held row is
  // "mine" (the W-1 swap and W-4 renew attacks were exactly the WINDOW_ID
  // comparison being vacuous under env spoofing). A caller with NO --session is
  // a fresh acquire: it may claim a FREE slot, but can never release, swap,
  // or evict any held/reserved row.
  const now = Date.now();
  await db.beginTransaction();
  const byName = await lockedPermits(db);

  // BUG-354 D1: resolve the operator's instruction BEFORE the held-permit fast
  // path. `--name` is AUTHORITATIVE — it must win or fail loudly, never be
  // silently ignored because the window already holds a permit (the old shape:
  // "YOU ARE: <old>" + exit 0, observed live while holding Ben). The ambient
  // CLAUDE_IDENTITY env (metro.bat's preference) is NOT authoritative — it
  // only guides a FRESH acquisition, never forces an identity swap.
  const requested = flags.name || null;
  const envPref = (!flags.any && process.env.CLAUDE_IDENTITY) || null;
  const preferred = requested || envPref || null;

  // Validate an explicit --name up front so a bad instruction fails loudly
  // (non-zero, clear message) even when the window already holds a permit.
  let requestedName = null;
  if (requested) {
    if (isUnusable(requested)) {
      await db.rollback();
      console.error(unusableMessage(requested));
      process.exit(1);
    }
    requestedName = NAMES.find(n => n.toLowerCase() === String(requested).toLowerCase());
    if (!requestedName) {
      await db.rollback();
      console.error(`Unknown slot name "${requested}". Valid: ${NAMES.join(', ')}`);
      process.exit(1);
    }
  }

  // Already holding an active permit? Resolved ONLY by the server-issued
  // session secret this invocation presents — never by ambient WINDOW_ID
  // (BUG-354 r4 W-1/W-4: an env-spoofed attacker must not resolve the victim's
  // row as "mine"). `proven` additionally matches a RESERVED stale own-row so a
  // window whose permit expired while idle can still prove its identity for
  // releasing that stale row before taking a fresh slot.
  const mine = findMineBySessionSecret(byName, now);
  const proven = findMineBySessionSecret(byName, now, { allowStale: true });

  // BUG-354 r5 (Warden round 4, F2): ONE live row per window id. Before any
  // FRESH acquire, refuse if this ambient window id is already claimed by a live
  // row (ACTIVE or RESERVED per slotState — a dead-by-boot-mismatch row is FREE
  // and no claim) whose server-issued secret this invocation cannot present.
  // Round 4's repro: an env-spoofer set CLAUDE_CODE_SESSION_ID to the victim's
  // value, deleted the victim's key file (it then lived in the shared checkout),
  // and a secret-less fresh acquire minted a ghost row beside the victim's live
  // one AND wrote the attacker's secret into the victim's key-file path — the
  // victim's next hook renew resolved the attacker's row as its own. The refusal
  // below closes the mint at the door: a legitimate caller either presents its
  // secret (the hooks always do — `proven`/`mine` already admit it) or has a
  // genuinely fresh window id with no live claim. Only the secret-less attacker
  // hits the wall.
  // BUG-354 r7 (r6 REJECT H2): collect EVERY live row under this window id,
  // not just the first in NAMES order. Round 6's ghost-shadowing attack
  // created a ghost ACTIVE row earlier in NAMES order (Bill) under the
  // victim's window; a first-only scan saw the ghost (proven by the attacker's
  // own secret) and missed the victim's real Bev — the refusal passed and a
  // second live row was minted. The refusal must weigh ALL live rows.
  let liveWindowClaims = [];
  if (WINDOW_ID) {
    for (const n of NAMES) {
      const st = slotState(byName[n], now);
      if ((st === 'ACTIVE' || st === 'RESERVED') && byName[n].window_id === WINDOW_ID) {
        liveWindowClaims.push({ name: n, state: st, session_id: byName[n].session_id });
      }
    }
  }

  if (mine) {
    if (requestedName) {
      if (requestedName === mine.row.name) {
        // Same slot — plain renew (re-asserts the instruction harmlessly).
        await db.query('UPDATE sync_permits SET expires_ms=?, heartbeat_ms=? WHERE name=?',
          [now + TTL_MS, now, mine.row.name]);
        ensureSessionKey(mine.row.session_id);
        await db.commit();
        await printSuccess(mine.row.name, mine.row.session_id, db);
        return;
      }
      // Different slot: --name is authoritative. Swap when the target is
      // available to this window; FAIL LOUDLY otherwise — never silently keep
      // the old identity and exit 0.
      const targetRow = byName[requestedName];
      const targetState = slotState(targetRow, now);

      if (targetState === 'ACTIVE') {
        // Auto-reclaim from a provably dead holder is handled by slotState
        // (boot mismatch -> FREE). A live holder requires the human-only path.
        if (flags.force && flags['human-ok']) {
          // BUG-354 r7 (r6 REJECT H1): the F2 one-live-row refusal must gate the
          // force-evict acquire too — r6 left all four force/override branches
          // unguarded, so --force --human-ok (a plain flag, no human proof) could
          // mint a ghost row beside a live claim it could not secret-prove.
          const f2Refusal = liveWindowClaimRefusal(liveWindowClaims, mine.row);
          if (f2Refusal) {
            await db.rollback();
            console.error(f2Refusal);
            process.exit(1);
          }
          // BUG-354 r8 (r7 REJECT, ac9ff2cc): this branch is reached only when
          // flags.session is NOT the target's secret (presenting the target's own
          // secret resolves it as `mine` and routes to the plain renew above), so
          // the eviction always refuses here — a live holder can only be taken
          // over by proving the target's secret, never by naming it. F2 above is
          // retained as a second line in case findMine semantics ever change.
          const r8Refusal = forceEvictRefusal(targetRow);
          if (r8Refusal) {
            await db.rollback();
            console.error(r8Refusal);
            process.exit(1);
          }
          // An identity change must never leave the OLD row live beside the new
          // one — release it the way the non-force swap below does (r6 H1: the
          // force swap left the proven row live and ended with two live rows).
          await db.query('UPDATE sync_permits SET released=1 WHERE name=?', [mine.row.name]);
          await releaseOtherRowsForWindow(db, requestedName, mine.row);
          const sessionId = await acquire(db, requestedName);
          await log(db, requestedName, `${requestedName} FORCE-EVICTED previous holder (human-authorised) and checked in`);
          await db.commit();
          console.log(`Evicted previous ${requestedName} holder (human-authorised).`);
          await printSuccess(requestedName, sessionId, db);
          return;
        }
        if (flags.force) {
          await db.rollback();
          console.error(`FORCE-EVICT BLOCKED: evicting a live holder requires the human-only --human-ok flag.`);
          console.error(`A human must authorise: node claude-sync.js checkin --name ${requestedName} --force --human-ok`);
          process.exit(1);
        }
        await db.rollback();
        console.error(`SLOT IS OCCUPIED: ${requestedName} is held by a live session (name-occupied), expires in ${fmtMs(Number(targetRow.expires_ms) - now)}.`);
        console.error(`Identity change to ${requestedName} refused (held elsewhere). Human-only eviction: node claude-sync.js checkin --name ${requestedName} --force --human-ok`);
        process.exit(1);
      }

      if (targetState === 'RESERVED' && targetRow.session_id !== flags.session) {
        if (flags.force && flags['human-ok']) {
          // BUG-354 r7 (r6 REJECT H1): same F2 gate + old-row release as the
          // ACTIVE force-evict above.
          const f2Refusal = liveWindowClaimRefusal(liveWindowClaims, mine.row);
          if (f2Refusal) {
            await db.rollback();
            console.error(f2Refusal);
            process.exit(1);
          }
          // BUG-354 r8 (r7 REJECT, ac9ff2cc): same target-secret authentication
          // as the ACTIVE force-evict — a reserved holder is a live session too,
          // and can only be overridden by possession of its secret.
          const r8Refusal = forceEvictRefusal(targetRow);
          if (r8Refusal) {
            await db.rollback();
            console.error(r8Refusal);
            process.exit(1);
          }
          await db.query('UPDATE sync_permits SET released=1 WHERE name=?', [mine.row.name]);
          await releaseOtherRowsForWindow(db, requestedName, mine.row);
          const sessionId = await acquire(db, requestedName);
          await log(db, requestedName, `${requestedName} reservation overridden (human-authorised)`);
          await db.commit();
          await printSuccess(requestedName, sessionId, db);
          return;
        }
        await db.rollback();
        console.error(`SLOT IS RESERVED: ${requestedName} expired recently and is held for its idle window (name-reserved).`);
        console.error(`Identity change to ${requestedName} refused. Take a different slot: node claude-sync.js checkin --any`);
        process.exit(1);
      }

      // Target FREE, or RESERVED for this very window (its secret is ours) —
      // swap in one transaction: release the old slot, acquire the requested
      // one. `mine` is by construction the row whose server-issued secret this
      // invocation presented, so releasing it releases OUR OWN permit — no
      // further window-id guard is needed (BUG-354 r4).
      //
      // BUG-354 r6 (r5 REJECT finding 1): the F2 refusal must ALSO gate this
      // swap acquire. Round 4's repro ended in the swap path: an env-spoofer
      // who had minted a ghost row beside the victim's (the pre-r5 F2 hole)
      // then swapped identities, and the swap's acquire stamped the attacker's
      // secret into the victim's window WITHOUT consulting the live-claim
      // guard. `proven` is the secret-proven row; if this window id already
      // holds a live row this caller cannot secret-prove, refuse the swap the
      // same way a fresh acquire refuses.
      const f2Refusal = liveWindowClaimRefusal(liveWindowClaims, proven && proven.row);
      if (f2Refusal) {
        await db.rollback();
        console.error(f2Refusal);
        process.exit(1);
      }
      await db.query('UPDATE sync_permits SET released=1 WHERE name=?', [mine.row.name]);
      await releaseOtherRowsForWindow(db, requestedName, mine.row);
      const sessionId = await acquire(db, requestedName);
      await log(db, requestedName, `${requestedName} checked in (identity changed from ${mine.row.name} by operator --name)`);
      await db.commit();
      await printSuccess(requestedName, sessionId, db);
      return;
    }
    // No explicit --name — renew, done. Ambient CLAUDE_IDENTITY never forces a
    // swap; the window already holds a slot.
    await db.query('UPDATE sync_permits SET expires_ms=?, heartbeat_ms=? WHERE name=?',
      [now + TTL_MS, now, mine.row.name]);
    ensureSessionKey(mine.row.session_id);
    await db.commit();
    await printSuccess(mine.row.name, mine.row.session_id, db);
    return;
  }

  if (preferred) {
    if (isUnusable(preferred)) {
      await db.rollback();
      console.error(unusableMessage(preferred));
      process.exit(1);
    }
    const name = NAMES.find(n => n.toLowerCase() === String(preferred).toLowerCase());
    if (!name) {
      await db.rollback();
      console.error(`Unknown slot name "${preferred}". Valid: ${NAMES.join(', ')}`);
      process.exit(1);
    }
    const row = byName[name];
    const state = slotState(row, now);

    if (state === 'ACTIVE') {
      // Auto-reclaim from a provably dead holder is handled by slotState (boot
      // mismatch -> FREE). A live holder requires the human-only force path.
      if (flags.force && flags['human-ok']) {
        // BUG-354 r7 (r6 REJECT H1): gate the preferred-path force-evict too.
        // Here `mine` is null (a fresh caller) and `proven` is null unless a
        // secret is presented, so the gate refuses whenever the window already
        // carries a live row the caller cannot secret-prove — exactly round 6's
        // secret-less --force --human-ok mint.
        const f2Refusal = liveWindowClaimRefusal(liveWindowClaims, proven && proven.row);
        if (f2Refusal) {
          await db.rollback();
          console.error(f2Refusal);
          process.exit(1);
        }
        // BUG-354 r8 (r7 REJECT, ac9ff2cc): a fresh caller (mine null) with a
        // foreign/absent secret must never force-evict a live row by name — the
        // F2 gate above sees no claims under a fresh window and passes
        // vacuously. The target's own server-issued secret is the only
        // authorisation (and presenting it routes this to the plain renew /
        // fresh take path, never an eviction).
        const r8Refusal = forceEvictRefusal(row);
        if (r8Refusal) {
          await db.rollback();
          console.error(r8Refusal);
          process.exit(1);
        }
        await releaseOtherRowsForWindow(db, name, proven && proven.row);
        const sessionId = await acquire(db, name);
        await log(db, name, `${name} FORCE-EVICTED previous holder (human-authorised) and checked in`);
        await db.commit();
        console.log(`Evicted previous ${name} holder (human-authorised).`);
        await printSuccess(name, sessionId, db);
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

    if (state === 'RESERVED' && row.session_id !== flags.session) {
      if (flags.force && flags['human-ok']) {
        // BUG-354 r7 (r6 REJECT H1): same F2 gate as the preferred ACTIVE
        // force-evict above.
        const f2Refusal = liveWindowClaimRefusal(liveWindowClaims, proven && proven.row);
        if (f2Refusal) {
          await db.rollback();
          console.error(f2Refusal);
          process.exit(1);
        }
        // BUG-354 r8 (r7 REJECT, ac9ff2cc): target-secret authentication for
        // overriding a reservation — identical rule to the ACTIVE override.
        const r8Refusal = forceEvictRefusal(row);
        if (r8Refusal) {
          await db.rollback();
          console.error(r8Refusal);
          process.exit(1);
        }
        await releaseOtherRowsForWindow(db, name, proven && proven.row);
        const sessionId = await acquire(db, name);
        await log(db, name, `${name} reservation overridden (human-authorised)`);
        await db.commit();
        await printSuccess(name, sessionId, db);
        return;
      }
      await db.rollback();
      console.error(`SLOT IS RESERVED: ${name} expired recently and is held for its idle window (name-reserved).`);
      console.error(`Take the next free slot instead: node claude-sync.js checkin --any`);
      process.exit(1);
    }

    // FREE, or RESERVED for this very window — take it.
    // BUG-354 r5 F2: never mint a second live row beside a live claim this
    // caller cannot secret-prove (an env-spoofer fabricating the victim's id).
    const f2Refusal = liveWindowClaimRefusal(liveWindowClaims, proven && proven.row);
    if (f2Refusal) {
      await db.rollback();
      console.error(f2Refusal);
      process.exit(1);
    }
    await releaseOtherRowsForWindow(db, name, proven && proven.row);
    const sessionId = await acquire(db, name);
    await log(db, name, `${name} checked in`);
    await db.commit();
    await printSuccess(name, sessionId, db);
    return;
  }

  // BUG-344: `--any` ignores CLAUDE_IDENTITY as a slot preference, but a
  // retired name in that env must still fail closed with the RETIRED
  // rejection — not silently land on the next live slot. isRetired is
  // otherwise only consulted when !flags.any (envPref / --name).
  if (!preferred && process.env.CLAUDE_IDENTITY && isRetired(process.env.CLAUDE_IDENTITY)) {
    await db.rollback();
    console.error(retiredMessage(process.env.CLAUDE_IDENTITY));
    process.exit(1);
  }

  // No specific name — prefer THIS window's own last-held identity (per
  // sync_window_map) when that slot is actually available to it; otherwise
  // first free slot in NAMES order. FIX (Aaron, 2026-08-18 incident): falling
  // straight to first-free-in-NAMES-order let a woken window with a
  // lapsed/mismatched reservation land on a DIFFERENT identity than the one
  // its previous session held — that's how a lead session's window kept
  // resurrecting the Bill slot via a stale map row. Checking the map first
  // keeps a window in its own identity whenever that slot is FREE or still
  // RESERVED for this same window; it never grants a slot held live by someone
  // else, and never resurrects a retired or PARKED name (BUG-354 D2).
  let free = null;
  // The persistent window-map preference (which FREE slot this window prefers
  // as its own identity) is keyed by a PROVEN window id, never by the ambient
  // env alone — any process can set that to another window's value. Proof is
  // either a live secret-matched row, or key-file possession: the caller
  // presents a --session matching this window's OWN per-window key file
  // (written at its last acquire), which an env-spoofing attacker does not
  // hold. A caller with neither proof has no last-held identity and goes
  // straight to first-free.
  let mapWindowId = (proven && proven.row && proven.row.window_id) || null;
  if (!mapWindowId && WINDOW_ID && flags.session) {
    try {
      const k = sessionKeyPath(WINDOW_ID);
      if (k && fs.readFileSync(k, 'utf8').trim() === flags.session) mapWindowId = WINDOW_ID;
    } catch { /* no key file — no proof */ }
  }
  if (mapWindowId) {
    const [mapRows] = await db.query('SELECT name FROM sync_window_map WHERE window_id=?', [mapWindowId]);
    if (mapRows.length) {
      const mapped = mapRows[0].name;
      if (NAMES.includes(mapped) && !isUnusable(mapped)) {
        const mappedState = slotState(byName[mapped], now);
        if (mappedState === 'FREE' || (mappedState === 'RESERVED' && byName[mapped].session_id === flags.session)) {
          free = mapped;
        }
      }
    }
  }
  if (!free) {
    free = NAMES.find(n => !isUnusable(n)
      && (slotState(byName[n], now) === 'FREE'
        || (slotState(byName[n], now) === 'RESERVED' && byName[n].session_id === flags.session)));
  }
  if (!free) {
    await db.rollback();
    console.error(`ALL SLOTS FULL (all-full): ${liveNames().join(', ')} are all occupied or reserved.`);
    for (const n of NAMES) {
      const row = byName[n];
      const state = slotState(row, now);
      if (state === 'ACTIVE') console.error(`  ${n} expires in ${fmtMs(Number(row.expires_ms) - now)}`);
      else console.error(`  ${n} reserved for an idle window (reservation lapses in ${fmtMs(RESERVE_MS - (now - Number(row.expires_ms)))})`);
    }
    process.exit(1);
  }
  // BUG-354 r5 F2: never mint a second live row beside a live claim this
  // caller cannot secret-prove (an env-spoofer fabricating the victim's id).
  const f2Refusal = liveWindowClaimRefusal(liveWindowClaims, proven && proven.row);
  if (f2Refusal) {
    await db.rollback();
    console.error(f2Refusal);
    process.exit(1);
  }
  await releaseOtherRowsForWindow(db, free, proven && proven.row);
  const sessionId = await acquire(db, free);
  await log(db, free, `${free} checked in`);
  await db.commit();
  await printSuccess(free, sessionId, db);
}

async function cmdRenew(db) {
  const now = Date.now();
  // BUG-486: the PostToolUse ping hook called `renew --auto` with ONLY
  // CLAUDE_CODE_SESSION_ID set in env (see claude-ping-check.js) — it never
  // passed --session, because that secret is printed exactly once, at
  // checkin, and the hook is a different process invocation entirely. Every
  // resolution path below (`mine`/`stale`/`hadName`) is gated on flags.session
  // via findMineBySessionSecret, so a bare `renew --auto` silently no-opped
  // on EVERY hook call — the live symptom (an active session's permit decays
  // to RESERVED despite constant tool use, because the hook's "renewal"
  // never actually renewed).
  //
  // FIX LOCATION: claude-ping-check.js, NOT here. The obvious-looking fix —
  // have cmdRenew itself default flags.session from
  // readSessionKey(WINDOW_ID) when the caller passed none — was tried and
  // is a SECURITY REGRESSION: it reintroduces exactly the WINDOW_ID+key-file
  // trust that BUG-354 r4/r6 deliberately excluded from renew (unlike
  // findMine(), which read/status/message paths use and BUG-360 tracks as an
  // accepted residual risk). Proven by the existing suite: 'BUG-354 r4 W-3'
  // and 'BUG-354 r5 F4' (claude-sync.test.js) both model an attacker who
  // knows/guesses a victim's WINDOW_ID value and sets it in env with no
  // secret of their own — on the SAME machine, sessionKeyPath(WINDOW_ID) is
  // keyed ONLY by that guessable id string, so "possession of the key file"
  // proves nothing once the id is known; a default-from-key-file here would
  // let that same attacker wake-recover / re-mint the victim's permit via
  // bare `renew --auto`. Both tests went RED under that approach and are the
  // reason it was reverted. The real fix is at the hook: claude-ping-check.js
  // legitimately IS the window it renews for, so it already has filesystem
  // access to ITS OWN session-key file — it now reads that file itself and
  // passes the secret as an explicit --session flag (the same thing an
  // operator/test would do manually), so cmdRenew's trust boundary here is
  // completely unchanged: still secret-only, still flags.session-gated.
  await db.beginTransaction();
  const byName = await lockedPermits(db);

  // BUG-354 r6 (r5 REJECT finding 1): same one-live-row-per-window claim as
  // cmdCheckin — needed to gate the hadName wake-recovery acquire below, which
  // r5 left unguarded.
  // BUG-354 r7 (r6 REJECT H2): collect EVERY live row under this window id,
  // not just the first in NAMES order. Round 6's ghost-shadowing attack
  // created a ghost ACTIVE row earlier in NAMES order (Bill) under the
  // victim's window; a first-only scan saw the ghost (proven by the attacker's
  // own secret) and missed the victim's real Bev — the refusal passed and a
  // second live row was minted. The refusal must weigh ALL live rows.
  let liveWindowClaims = [];
  if (WINDOW_ID) {
    for (const n of NAMES) {
      const st = slotState(byName[n], now);
      if ((st === 'ACTIVE' || st === 'RESERVED') && byName[n].window_id === WINDOW_ID) {
        liveWindowClaims.push({ name: n, state: st, session_id: byName[n].session_id });
      }
    }
  }

  const mine = findMineBySessionSecret(byName, now);
  if (mine) {
    const remaining = Number(mine.row.expires_ms) - now;
    if (flags.auto && remaining > RENEW_THRESHOLD_MS) {
      await db.query('UPDATE sync_permits SET heartbeat_ms=? WHERE name=?', [now, mine.row.name]);
      await db.commit();
      return; // plenty of time left — heartbeat only, stay silent
    }
    await db.query('UPDATE sync_permits SET expires_ms=?, heartbeat_ms=? WHERE name=?',
      [now + TTL_MS, now, mine.row.name]);
    ensureSessionKey(mine.row.session_id);
    await db.commit();
    if (!flags.auto) console.log(`${mine.row.name} renewed — expires in ${fmtMs(TTL_MS)}.`);
    return;
  }

  // Wake recovery: permit expired while idle. Secret-resolved only — an
  // env-spoofing attacker must not be able to re-acquire the victim's RESERVED
  // row and mint a fresh secret under its own control (BUG-354 r4 W-3).
  const stale = findMineBySessionSecret(byName, now, { allowStale: true });
  if (stale) {
    // BUG-354 D2: a PARKED slot must never be (re)assigned — even if this
    // window's own previous permit was Ben (a pre-park assignment), recovery
    // must not land the window back in it.
    if (isUnusable(stale.row.name)) {
      await db.commit();
      console.error(`[claude-sync] Your previous slot "${stale.row.name}" is ${isParked(stale.row.name) ? 'PARKED' : 'retired'} and cannot be re-acquired.`);
      console.error(`[claude-sync] Check in explicitly with a live name: node claude-sync.js checkin --name <${liveNames().join('|')}>`);
      process.exit(1);
    }
    // BUG-354 r7 (r6 REJECT H6): the F2 refusal must gate wake recovery too —
    // r6 left the stale-permit re-acquire unguarded, so `renew --session <own
    // secret>` from a spoofed window id re-acquired the caller's own lapsed row
    // live under the VICTIM's window id, beside the victim's live row.
    const f2Refusal = liveWindowClaimRefusal(liveWindowClaims, stale.row);
    if (f2Refusal) {
      await db.rollback();
      console.error(f2Refusal);
      process.exit(1);
    }
    const sessionId = await acquire(db, stale.row.name);
    await log(db, stale.row.name, `${stale.row.name} wake recovery — re-acquired after idle expiry`);
    await db.commit();
    console.log(`[claude-sync] Wake recovery: re-acquired ${stale.row.name} (permit expired while idle). Session: ${sessionId}`);
    // FEAT-107: checkin-adjacent path — same count-only, never-deliver,
    // never-advance-cursor treatment as a genuine checkin (see header comment).
    try {
      printUnreadCount(await countUnread(db, stale.row.name));
    } catch (err) {
      console.log(`(unread check unavailable: ${err.message})`);
    }
    return;
  }

  // This window previously held `hadName` (per its own released permit row, or
  // the persistent sync_window_map) but neither `mine` nor `stale` matched
  // above — typically its reservation lapsed while idle.
  // FIX (Aaron, 2026-08-19, cross-assign incident): the DB activity log
  // showed wake recovery silently handing a window a DIFFERENT identity
  // than the one it held ("Bill assigned via wake recovery (previous name
  // Ben unavailable)", "Bob assigned via wake recovery (previous name Bill
  // unavailable)") — a session telling a human it is someone else with no
  // human in the loop. Wake recovery must NEVER adopt a different name:
  // if the previous slot is itself FREE, reclaim that SAME name; if it is
  // genuinely unavailable (held live by another window, or reserved for
  // another window), fail loudly and exit nonzero instead of cross-assigning.
  // BUG-354 F3: a released row must never count as "last held" UNLESS this
  // invocation proves it is its own released row by presenting that row's
  // server-issued secret — after a D1 swap the old slot is released but
  // retains this window's window_id, and an unreleased-inclusive, secret-blind
  // scan resurrects the PRE-swap identity (map says Bev, but the released Bill
  // row shadows it) and wake recovery reclaims the wrong slot.
  //
  // BUG-354 r5 (Warden round 4, F4): the ENTIRE hadName path is gated on
  // `flags.session` — a secret-less caller (an env-spoofer fabricating the
  // victim's window id) must never be able to re-acquire the victim's name via
  // ambient WINDOW_ID + sync_window_map alone. With a secret presented, the
  // previous name is resolved as either (a) a RELEASED row whose secret this
  // invocation presents (possession of the secret IS the identity), or (b) the
  // persistent sync_window_map, but ONLY under key-file proof: the presented
  // secret matches this window's own per-user key file. A caller with neither
  // has no proof of a previous name and gets a clean no-op, never a re-acquire.
  let hadName = null;
  if (flags.session) {
    hadName = NAMES.find(n => {
      const r = byName[n];
      return r && r.released && r.session_id === flags.session;
    });
    if (!hadName && WINDOW_ID) {
      try {
        const k = sessionKeyPath(WINDOW_ID);
        if (k && fs.readFileSync(k, 'utf8').trim() === flags.session) {
          const [map] = await db.query('SELECT name FROM sync_window_map WHERE window_id=?', [WINDOW_ID]);
          if (map.length) hadName = map[0].name;
        }
      } catch { /* no key file — no proof */ }
    }
  }
  if (hadName) {
    if (!NAMES.includes(hadName) || isUnusable(hadName)) {
      // The previous name is retired, PARKED, or no longer a valid slot at all
      // (e.g. a stale window_map row pointing at retired Bob, or parked Ben) —
      // there is no "same name" left to reclaim. Loud + explicit, never a
      // silent swap to some other name (BUG-354 D2).
      await db.commit();
      console.error(`[claude-sync] Your previous slot "${hadName}" no longer exists or is unusable (retired or parked).`);
      console.error(`[claude-sync] Check in explicitly with a current name: node claude-sync.js checkin --name <${liveNames().join('|')}>`);
      process.exit(1);
    }
    const state = slotState(byName[hadName], now);
    if (state === 'FREE') {
      // BUG-354 r6 (r5 REJECT finding 1): same one-live-row-per-window guard as
      // the checkin acquires. A caller who cannot secret-prove the live claim
      // this window_id already carries must never mint a second live row
      // beside it via wake recovery.
      const f2Refusal = liveWindowClaimRefusal(liveWindowClaims, byName[hadName]);
      if (f2Refusal) {
        await db.rollback();
        console.error(f2Refusal);
        process.exit(1);
      }
      const sessionId = await acquire(db, hadName);
      await log(db, hadName, `${hadName} wake recovery — re-acquired after idle expiry (via window map)`);
      await db.commit();
      console.log(`[claude-sync] Wake recovery: re-acquired ${hadName} (permit expired while idle). Session: ${sessionId}`);
      // FEAT-107: same count-only treatment as the stale-permit reclaim above.
      try {
        printUnreadCount(await countUnread(db, hadName));
      } catch (err) {
        console.log(`(unread check unavailable: ${err.message})`);
      }
      return;
    }
    // ACTIVE (held live by another window) or RESERVED (for another
    // window) — the previous slot is genuinely unavailable right now. Fail
    // loudly; the human/operator decides the next step, this code never does.
    await db.rollback();
    console.error(`[claude-sync] Your previous slot "${hadName}" is held; re-checkin explicitly:`);
    console.error(`[claude-sync]   node claude-sync.js checkin --name ${hadName} (--force --human-ok if a human authorises eviction)`);
    process.exit(1);
  }

  await db.commit();
  if (!flags.auto) {
    console.log('No permit for this window. Run: node claude-sync.js checkin');
  } else {
    // GR#17 (silent failure detection): a --auto call that resolves nothing
    // must never no-op in total silence — the ping hook relays this
    // process's stdout straight into the session (see claude-ping-check.js),
    // so this is the ONLY channel a stuck window's user ever gets. This does
    // NOT change the trust model above: it fires only for whatever secret (or
    // absence of one) this invocation actually presented via --session.
    console.log(flags.session
      ? '[claude-sync] renew --auto: the presented --session secret matched no live/stale permit — it has expired past RESERVE_MS or was reassigned. Checkin explicitly: node claude-sync.js checkin'
      : '[claude-sync] renew --auto: no --session secret presented — nothing to renew. If this is the ping hook, see claude-ping-check.js (BUG-486: it must read and pass its own session-key file).');
  }
}

async function cmdCheckout(db) {
  const now = Date.now();
  await db.beginTransaction();
  const byName = await lockedPermits(db);

  let target = null;
  if (flags.force) {
    const forcedName = positional[0] || (typeof flags.force === 'string' ? flags.force : null);
    if (forcedName && isUnusable(forcedName)) {
      await db.rollback();
      console.error(unusableMessage(forcedName));
      process.exit(1);
    }
    const name = NAMES.find(n => forcedName && n.toLowerCase() === String(forcedName).toLowerCase());
    if (!name) {
      await db.rollback();
      console.error('checkout --force requires a slot name: node claude-sync.js checkout --force Bill');
      process.exit(1);
    }
    target = byName[name];
    // BUG-354 r8 (r7 REJECT, ac9ff2cc): checkout --force released a LIVE row
    // selected BY NAME with no proof at all (P3: `checkout --force Bev` released
    // the victim). Releasing a live session is a destructive act — it must
    // authenticate the TARGET row's server-issued secret, the only operator
    // credential this system has (the r8 control keeps the legit recovery:
    // checkout --force Bev --session <Bev-secret> still releases).
    const r8Refusal = forceEvictRefusal(target);
    if (r8Refusal) {
      await db.rollback();
      console.error(r8Refusal);
      process.exit(1);
    }
  } else {
    // BUG-354 r4: checkout resolves the permit to release ONLY by the
    // server-issued session secret this invocation presents (allowStale so a
    // window can check out its own RESERVED row too). Ambient WINDOW_ID is
    // never consulted — an env-spoofing attacker with no secret must not be
    // able to release the victim's permit (Warden repro #2). The secret comes
    // from the operator: `node claude-sync.js checkout --session <secret>`.
    const mine = findMineBySessionSecret(byName, now, { allowStale: true });
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
        // BUG-264 fix: with dateStrings:true, a.ts is a string like "2026-08-18 23:01:53"
        // (local time, no timezone info). Use it directly without toISOString() tz conversion.
        const tsStr = typeof a.ts === 'string' ? a.ts : a.ts.toISOString().replace('T', ' ');
        console.log(`  [${tsStr.slice(0, 19)}] ${a.name ? a.name + ': ' : ''}${a.message}`);
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

    // FEAT-107: `read` is the sole delivering + cursor-advancing path for
    // unread messages. Resolve the identity THIS window currently holds
    // (allowStale so a woken window whose reservation lapsed can still read
    // its own backlog) and deliver+advance for it only — never for any other
    // identity, and never as a side effect of a bare `status`. Failure here
    // must not crash `read`'s otherwise-successful slot/activity/loop output
    // — same fail-tolerant shape as printSuccess's own try/catch blocks.
    try {
      const mine = findMine(byName, now, { allowStale: true });
      if (mine) {
        await db.beginTransaction();
        let unread;
        try {
          unread = await deliverUnread(db, mine.row.name);
          await db.commit();
        } catch (err) {
          await db.rollback();
          throw err;
        }
        printUnread(unread);
      }
    } catch (err) {
      console.log(`(unread message delivery unavailable: ${err.message})`);
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
    if (isUnusable(flags.to)) {
      console.error(unusableMessage(flags.to));
      process.exit(1);
    }
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

  // GR#17 silent-failure detection (BUG-356): if sending to a lane with a
  // standing loop, check if it is checkin-only (deaf to messages since
  // FEAT-107). The sender is blind to the failure — they see "Message sent"
  // with no error — so we warn them explicitly here, at send time. The message
  // MUST ALWAYS SEND (done above) regardless of this warning check — if the
  // loop-config query throws (transient DB error), we skip the warning rather
  // than crashing the entire message send. The warning is best-effort, never
  // a blocker. Mirror the pattern at printSuccess (lines 539-542).
  try {
    if (toName) {
      const [[loopRow]] = await db.query('SELECT spec FROM sync_loop_config WHERE name=?', [toName]);
      if (loopRow && isCheckinOnly(loopRow.spec)) {
        console.log(`⚠ WARNING: ${toName}'s standing loop is checkin-only and CANNOT deliver messages (FEAT-107 split) — it will not see this message. Fix ${toName}'s loop to include 'read', or use the file channel (metropolis-status/bev-to-${toName.toLowerCase()}.md).`);
      }
    }
  } catch (err) {
    console.log(`(loop-config check unavailable: ${err.message})`);
  }
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
  NAMES, RETIRED, PARKED, isRetired, isParked, isUnusable, retiredMessage, parkedMessage,
  unusableMessage, liveNames,
  connect, ensureSchema, findMine, findMineBySessionSecret, slotState, deliverUnread, printUnread,
  sessionKeyPath, readSessionKey,
  countUnread, printUnreadCount, isCheckinOnly,
  cmdCheckin, cmdRenew, cmdMessage, cmdCheckout, cmdStatus, cmdWrite, cmdClaim,
  cmdRelease, cmdGc,
  cmdLoopSet, cmdLoopClear, cmdLoopShow, printLoopArmStatus,
  LOOP_MARKER, LOOP_STALE_MS,
  cmdUtil, printUtilSummary,
  // ASM-734 test-only export: BOOT_ID was previously internal-only, so a pure
  // fixture test of slotState's reboot-mismatch behaviour (AC-6) had no way to
  // construct a row that matches THIS process's real boot id without
  // reimplementing the (os.uptime()-based, inherently timing-sensitive)
  // derivation formula itself. Exporting the already-computed value adds no
  // new behaviour and changes nothing at runtime.
  BOOT_ID,
};

// ── Entry ─────────────────────────────────────────────────────────────────────
if (require.main === module) {
  runCli();
}
