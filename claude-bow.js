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
 *   add <type> "title" [--priority P2] [--desc "..." | --desc-file <path>]
 *                                         - type: module|feature|bug|interface
 *   list [--type T] [--status S] [--all]  - Open items grouped by priority (--all incl. closed)
 *   show <code|guid>                      - Full detail: deps, comments, code, git refs
 *   comment <code> "text" [--example-file F | --example "code"] [--lang js]
 *   depend <code> --on <code> [--note "..." | --note-file <path>]   (cycle-checked)
 *   undepend <code> --on <code>
 *   ref <code> <commit-hash> [--note "..." | --note-file <path>]    - Link a git commit to an item
 *   set <code> [--priority P1] [--status in_progress|blocked|open]
 *   redact <code> | redact --comment <id>
 *                                         - BUG-061 (GR#22): scan an item's title/
 *                                           description, or a comment's body, for the
 *                                           reference-title patterns (reusing
 *                                           claude-codename-guard.js's own fragment-
 *                                           assembled pattern set — GR#3) and replace
 *                                           every hit with [REDACTED-GR22]. Auto-posts
 *                                           an audit comment recording ONLY the pattern
 *                                           class(es), count and field(s) affected —
 *                                           never the matched text, never the pre-image.
 *                                           No --match/--text flag exists: the forbidden
 *                                           text never transits the command line.
 *   done <code> [--note "resolution" | --note-file <path>] [--force]
 *                                         - Blocked while open dependencies remain (GR#12)
 *                                           unless --force
 *
 *   BUG-090 (docs/planning/acceptance/tool.bowcli.md): every free-text flag
 *   above (--desc/--note/--detail, plus the pre-existing --example) accepts a
 *   `-file <path>` alternative that reads the value from a file instead of
 *   the command line — prefer it whenever the content has a backtick,
 *   `$(...)`, an embedded quote, or spans multiple lines, since the OUTER
 *   shell submitting the command reinterprets those characters before this
 *   tool ever sees the string.
 *   import <plan-file.json> [--dry-run]   - Bulk upsert items+deps from a generated plan
 *                                           (tools/plan/bow-import.json; idempotent by mkey)
 *   ready                                 - Open items with no open deps, by sprint+seq
 *                                           (the v_ready_to_build equivalent — work from here)
 *   weakness                              - Security-finding recurrence report by class
 *   lint                                  - FEAT-060: report-only prose-vs-graph drift scan
 *                                           (description/comment text vs bow_dependencies and
 *                                           bow_items.status — always exits 0, never wired
 *                                           into any hook/CI path; see tool.bowlint.md)
 *   summary                               - Compact BOW summary (used at checkin)
 *   startup-summary                       - BOW summary + Vestige check + git sync check
 *   destructive <code> --verdict accept|reject --attacker "<name>"
 *                       [--class c1[,c2,...]] [--findings SEC-001[,...]] [--note "..." | --note-file <path>]
 *                                         - Record a Destructive-agent verdict (FEAT-040,
 *                                           GR#23). APPEND-ONLY: never overwrites a prior
 *                                           verdict row for the same item.
 *   verdict <code> [--json]               - Report the LATEST recorded Destructive verdict
 *                                           for an item (or "no verdict recorded").
 *   exists CODE1 CODE2 ... | --codes CODE1,CODE2,...
 *                                         - BUG-075: cheap batch existence check, one DB
 *                                           round-trip for any number of codes. For each
 *                                           code prints EXISTS (with its one-line title, so
 *                                           the caller can also eyeball "does this look like
 *                                           what I think it is") or NOT FOUND. Matches by
 *                                           short code (case-insensitive) or mkey — same
 *                                           lookup rule as findItem()/requireItem(), just
 *                                           batched. Replaces N `show` calls when verifying a
 *                                           report's cited codes actually resolve before
 *                                           relaying or accepting them as fact (dev-team-
 *                                           process.md's Tester/lead citation-verification
 *                                           duty).
 *   gate <sprint#> --check <1-5> --name <data-files|call-edges|tripwires|
 *        boundary-rulings|ready-queue> --verdict pass|fail|partial|skipped
 *        --runner "<name>" [--detail "..." | --detail-file <path>] [--run <guid>]
 *                                         - Record one sprint-gate check verdict
 *                                           (FEAT-061, GR#12/GR#23). APPEND-ONLY.
 *   gate-run <sprint#> [--runner "<name>"] - Run all 5 sprint-gate checks and
 *                                           record their verdicts (one gate_run_guid).
 *   gate-status <sprint#>                 - Report the LATEST gate run's 5 rows plus
 *                                           the DERIVED overall verdict (never a 6th
 *                                           hand-set field, GR#15).
 *
 * v2 planning fields (2026-08-08): every item may carry mkey (machine key,
 * matches code.json module key), seq (global build-order sequence number),
 * milestone (M0..M4/future), layer, spec_ref (master doc §), guid_in/guid_out
 * (interface GUIDs mirrored from code.json), estimate_days.
 * List supports --by-seq to show build order.
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
const { execSync, spawnSync } = require('child_process');
const mysql = require('mysql2/promise');

const TYPES = ['module', 'feature', 'bug', 'interface', 'assumption', 'finding'];
const TYPE_PREFIX = { module: 'MOD', feature: 'FEAT', bug: 'BUG', interface: 'INT', assumption: 'ASM', finding: 'SEC' };
// Weakness classes for security findings. The point of a closed list is
// COUNTING: if one class keeps recurring, that is a training signal about how
// the team writes code, not just N separate bugs to fix. Add a class only when
// something genuinely does not fit — a long tail of one-offs defeats the count.
const FINDING_CLASSES = [
  'input-validation', 'bounds-overflow', 'integer-conversion', 'type-confusion',
  'encapsulation-leak', 'insecure-call-surface', 'concurrency-safety',
  'resource-exhaustion', 'error-disclosure', 'injection', 'auth-trust-boundary',
  'crypto-randomness', 'other',
];
const PRIORITIES = ['P0', 'P1', 'P2', 'P3'];
const STATUSES = ['open', 'in_progress', 'blocked', 'done', 'cancelled'];
const OPEN_STATUSES = ['open', 'in_progress', 'blocked'];
const SUMMARY_MARKER = '── METROPOLIS STARTUP SUMMARY ──';

// BUG-115: ensureSchema() issues ALTER TABLE / CREATE TABLE / MODIFY COLUMN
// statements that take MariaDB metadata locks (MDL) — running it on every
// single invocation, including pure reads, exposes every concurrent agent's
// `list`/`show`/etc. to MDL-queue-starvation behind any long-running
// transaction elsewhere against these tables. Every command below is a pure
// read: verified by grepping each cmd* function's body for INSERT/UPDATE/
// DELETE/REPLACE/ALTER/CREATE — none of them appear under any of these
// commands.
//
// Several of these commands (list/show/ready/verdict/weakness) DO reference
// columns that were added by ensureSchema's ALTER/MODIFY statements rather
// than the original CREATE TABLE (seq, sprint, mkey, finding_class, the
// widened item_type enum) — either directly in their own SQL, or indirectly
// via the shared findItem() helper's `OR mkey = ?` clause. That is safe to
// build on here because, per ensureSchema's own comments, those columns are
// NOT a future/pending migration — they are already permanently present on
// the real `metro` database (added out-of-band; ensureSchema's ALTER
// statements have been confirmed no-ops there for some time) and the ADD
// COLUMN IF NOT EXISTS / MODIFY COLUMN forms mean nothing here ever
// regresses if ensureSchema is skipped for one invocation. A genuinely
// UNMIGRATED database (a scratch DB nobody has run ensureSchema against
// yet) is never handed to the read-only path in practice: the existing test
// suite's own precedent (test.before() below) stands up a scratch DB by
// calling connect()/ensureSchema() explicitly BEFORE issuing any CLI
// command against it — exactly the mechanism GR#3's "single source of
// truth" comment on ensureSchema recommends reusing, not by hoping a read
// command bootstraps it. `gate-status`/`lint`/`summary` don't touch any
// ALTER-added column or the mkey lookup at all.
//
// `init` and `startup-summary` are deliberately EXCLUDED — `startup-summary`
// calls ensureSchema itself inside printStartupSummary() for its other
// caller (claude-sync, which passes a raw connection and needs the tables
// guaranteed), and `init`'s entire purpose is running ensureSchema. Every
// write command (add/set/comment/depend/undepend/ref/redact/done/import/
// destructive/gate/gate-run) is likewise excluded. When in doubt about a
// command, it is NOT on this list — under-optimizing (still paying the
// ensureSchema cost) is safe; skipping it for a command that turns out to
// write, or to need a column no real database will ever actually be
// missing, is not.
const READ_ONLY_COMMANDS = new Set(['list', 'show', 'ready', 'summary', 'weakness', 'lint', 'verdict', 'gate-status', 'exists']);

// ── CLI parsing ───────────────────────────────────────────────────────────────

const argv = process.argv.slice(2);
const command = argv[0] || 'summary';
const positional = [];
const flags = {};
const VALUE_FLAGS = ['priority', 'desc', 'desc-file', 'status', 'type', 'on', 'note', 'note-file',
  'example', 'example-file', 'lang', 'author',
  'mkey', 'seq', 'sprint', 'spec', 'milestone', 'layer', 'guid-in', 'guid-out', 'estimate',
  'code-path', 'codejson', 'class', 'verdict', 'attacker', 'findings',
  'check', 'name', 'runner', 'run', 'detail', 'detail-file',
  // BUG-075: `exists CODE1 CODE2 ... | --codes CODE1,CODE2,...` batch check.
  'codes',
  // BUG-061 (tool.bow `redact`): --comment <id> selects a bow_comments row
  // instead of an item code. Deliberately no --match/--text value flag exists
  // ANYWHERE in this list — Aaron's binding constraint (BUG-061) is that the
  // forbidden text must never transit the command line, so the redact
  // command detects occurrences itself rather than being told what to redact.
  'comment'];
// BUG-168: a VALUE_FLAGS flag as the LAST token on the command line (e.g. a
// truncated/typo'd `set CODE --mkey` with nothing after it) makes
// `argv[++i]` read past the end of the array, so `flags[name]` ends up
// `undefined` rather than a real string. That is a genuinely distinct case
// from an explicit `--mkey ''` (which is the string ''), but a plain
// presence check (`'mkey' in flags`, BUG-132) can't tell them apart — the
// key exists in both. `danglingFlags` records exactly which flag names hit
// this out-of-bounds read, so any command with a presence-vs-value
// distinction (currently cmdSet's mkey/seq/sprint) can treat "flag present
// but consumed no value" as a usage error instead of a silent clear-to-NULL.
//
// BUG-171 (regression in BUG-168's fix): BUG-168 only caught the "off the
// end of argv" case. It did NOT catch a dangling flag immediately followed
// by ANOTHER recognized `--flag` token (e.g. `--mkey --seq 5`) — that next
// token is not `undefined`, so the old check let it fall through and get
// blindly consumed as `--mkey`'s string value, silently corrupting the
// column and dropping `--seq` (and its own value `5`) on the floor with no
// error at all. Fix: a candidate value that itself starts with `--` is
// ALSO dangling — don't consume it, mark the ORIGINAL flag dangling, and
// leave `i` where it is so the next loop iteration re-processes that token
// as its own flag. No VALUE_FLAGS value is ever intentionally allowed to
// start with `--` on this command line (the one case that needs `--`-shaped
// literal content, --desc/--note/--detail, has a dedicated *-file sibling
// added by BUG-090 specifically to keep risky text off argv), so this
// widened check has no legitimate value it breaks.
const danglingFlags = new Set();
for (let i = 1; i < argv.length; i++) {
  const a = argv[i];
  if (a.startsWith('--') && VALUE_FLAGS.includes(a.slice(2))) {
    const name = a.slice(2);
    const next = argv[i + 1];
    if (next === undefined || next.startsWith('--')) {
      danglingFlags.add(name);
      flags[name] = undefined;
      // Deliberately do NOT advance i past `next` — if it's a real flag
      // token, the next loop iteration must see it fresh and process it as
      // its own flag rather than have it silently swallowed here.
    } else {
      i++;
      flags[name] = next;
    }
  }
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
  // v2 planning columns (2026-08-08, master-plan load): global build sequence,
  // machine key for idempotent import, milestone/layer, spec cross-reference and
  // the code.json inbound/outbound interface GUID mirrors (traceability).
  await db.query(`ALTER TABLE bow_items
    ADD COLUMN IF NOT EXISTS mkey VARCHAR(64) NULL AFTER code,
    ADD COLUMN IF NOT EXISTS seq INT NULL AFTER mkey,
    ADD COLUMN IF NOT EXISTS sprint INT NULL AFTER seq,
    ADD COLUMN IF NOT EXISTS milestone VARCHAR(16) NULL AFTER priority,
    ADD COLUMN IF NOT EXISTS layer VARCHAR(32) NULL AFTER milestone,
    ADD COLUMN IF NOT EXISTS spec_ref VARCHAR(200) NULL AFTER layer,
    ADD COLUMN IF NOT EXISTS guid_in CHAR(36) NULL,
    ADD COLUMN IF NOT EXISTS guid_out CHAR(36) NULL,
    ADD COLUMN IF NOT EXISTS estimate_days DECIMAL(5,1) NULL,
    ADD COLUMN IF NOT EXISTS code_path VARCHAR(255) NULL,
    ADD COLUMN IF NOT EXISTS codejson_ref VARCHAR(128) NULL,
    ADD COLUMN IF NOT EXISTS finding_class VARCHAR(64) NULL`);
  // Schema-drift fix discovered while building BUG-090's regression tests
  // against a scratch database: code_path/codejson_ref/finding_class are
  // read/written by cmdAdd/cmdSet/cmdShow/cmdWeakness but were missing from
  // this ALTER — the real `metro` database already carries them (added
  // out-of-band at some point), so `add` only ever worked there; a fresh
  // scratch DB (exactly what any future test suite stands up via
  // ensureSchema) had no working `add` at all. Logged as a BOW finding, see
  // `node claude-bow.js show` for the code — this file is the single source
  // of truth for the schema (GR#3), so it must match what production
  // actually has.
  await db.query('ALTER TABLE bow_items ADD UNIQUE INDEX IF NOT EXISTS idx_bow_mkey (mkey)');
  await db.query('ALTER TABLE bow_items ADD INDEX IF NOT EXISTS idx_bow_seq (seq)');
  // BUG-114: item_type's ENUM was never widened to include 'assumption'/
  // 'finding' even though TYPE_PREFIX/TYPES (above) have listed both as
  // valid item types since cmdAdd started requiring --code-path/--codejson
  // for assumptions and --class for findings. Same schema-drift class as the
  // code_path/codejson_ref/finding_class fix above: the real `metro` database
  // already carries the wider enum (added out-of-band), so a fresh scratch DB
  // stood up via ensureSchema could create bug/feature/interface/module items
  // fine but `add assumption`/`add finding` failed outright with "Data
  // truncated for column 'item_type'". MODIFY COLUMN has no IF NOT EXISTS
  // form, but re-running an identical MODIFY COLUMN is a no-op in
  // MySQL/MariaDB (verified directly, not assumed) so this is safe to run on
  // every ensureSchema call including against an already-patched database.
  await db.query(`ALTER TABLE bow_items
    MODIFY COLUMN item_type ENUM('module','feature','bug','interface','assumption','finding') NOT NULL`);
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
  // FEAT-040 (tool.destructiveguard, GR#23): append-only Destructive-agent
  // verdict history. Deliberately APPEND-only (no UPDATE/DELETE path
  // anywhere in this file for this table) — a mutable "latest verdict"
  // column would erase the reject -> fix -> accept history, and the record
  // of what an attacker tried and FAILED to break is evidence, not noise
  // (lead ruling on ASM-194, node claude-bow.js show FEAT-040). `id` is a
  // monotonic tiebreaker so "latest" is deterministic even when two verdicts
  // land inside the same TIMESTAMP second (see AC-27 determinism).
  await db.query(`CREATE TABLE IF NOT EXISTS bow_destructive_verdicts (
    id               INT AUTO_INCREMENT PRIMARY KEY,
    guid             CHAR(36) NOT NULL UNIQUE,
    item_guid        CHAR(36) NOT NULL,
    verdict          ENUM('accept','reject') NOT NULL,
    attacker         VARCHAR(128) NOT NULL,
    weakness_classes VARCHAR(512) NULL,
    findings         VARCHAR(512) NULL,
    note             TEXT NULL,
    created_at       TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_bow_destructive_item FOREIGN KEY (item_guid) REFERENCES bow_items(guid) ON DELETE CASCADE,
    INDEX idx_bow_destructive_item (item_guid)
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`);
  // FEAT-061 (tool.sprintgate, GR#12/GR#15/GR#23): append-only per-check gate
  // verdicts, one row per check (1..5) per gate run, sharing a gate_run_guid
  // (AC-23/AC-25). Keyed on the plain `sprint` INT column bow_items already
  // carries (AC-1) rather than a fictitious "sprint anchor" item — see
  // docs/planning/acceptance/tool.sprintgate.md Escalation A. Mirrors
  // bow_destructive_verdicts' append-only shape (never UPDATE/DELETE here) so
  // the same "prose verdict is not a verdict" failure FEAT-040 was built to
  // fix cannot recur for gate verdicts either.
  await db.query(`CREATE TABLE IF NOT EXISTS bow_gate_verdicts (
    id            INT AUTO_INCREMENT PRIMARY KEY,
    guid          CHAR(36) NOT NULL UNIQUE,
    gate_run_guid CHAR(36) NOT NULL,
    sprint        INT NOT NULL,
    check_number  TINYINT NOT NULL,
    check_name    VARCHAR(64) NOT NULL,
    verdict       ENUM('pass','fail','partial','skipped') NOT NULL,
    runner        VARCHAR(128) NOT NULL,
    detail        TEXT NULL,
    created_at    TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    INDEX idx_gate_sprint (sprint),
    INDEX idx_gate_run (gate_run_guid)
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`);
}

/** Resolve an item by short code (FEAT-001, case-insensitive) or GUID. */
async function findItem(db, ref) {
  if (!ref) return null;
  const [rows] = await db.query(
    'SELECT * FROM bow_items WHERE guid = ? OR UPPER(code) = UPPER(?) OR mkey = ? LIMIT 1', [ref, ref, ref]);
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

// ── Safer free-text input mode (BUG-090, docs/planning/acceptance/tool.bowcli.md) ──
//
// `claude-bow.js`'s own argv parsing (VALUE_FLAGS above) is plain
// process.argv consumption — Node never re-interprets the string it
// receives. The real vulnerability sits one layer up: an agent using the
// Bash tool constructs a shell command line such as
// `node claude-bow.js add bug "title" --desc "...contains `some command`..."`,
// and the OUTER shell performs command substitution on backtick-wrapped (or
// $(...)-wrapped) content INSIDE a double-quoted argument BEFORE node ever
// sees the string (BUG-090's confirmed mechanism). No change inside this
// file can stop bash interpreting its own quoting grammar — what this file
// CAN do is stop REQUIRING an agent to put risky content on the command
// line in the first place. `cmdComment`'s `--example-file` (see below,
// unchanged, the existing precedent this ports) already does exactly that
// for example code; `resolveTextFlag` extends the identical shape
// (fs.readFileSync(path, 'utf8'), no trimming, no re-escaping) to
// --desc/--desc-file (`add`), --note/--note-file (`depend`, `ref`, `done`,
// `destructive`) and --detail/--detail-file (`gate`) — the other free-text
// fields most likely to carry copy-pasted attack strings, git commands, or
// code snippets. If the risky text never appears on the command line, the
// outer shell never gets a chance to interpret it, regardless of content.
//
// Prefer `--<field>-file <path>` over `--<field> "<text>"` whenever the
// content contains a backtick, a `$(...)` sequence, an embedded double
// quote, or spans multiple lines — those are exactly the shapes BUG-090 (and
// the related BUG-043) turned into real trouble.
const RISKY_CONTENT_RE = /`|\$\(|"/;

/**
 * Resolve a free-text CLI value that may be supplied either directly
 * (`--<field> "<text>"`) or via a file (`--<field>-file <path>`) — the
 * BUG-090 safer-input-mode pattern, ported verbatim from `cmdComment`'s
 * pre-existing `--example-file` handling (`fs.readFileSync(path, 'utf8')`,
 * no transformation of the content either way). Supplying both flags for
 * the same field is rejected (non-zero exit, clear message, no silent
 * precedence — AC-3) BEFORE any caller can reach a DB write. Omitting both
 * returns null, matching the pre-existing `flags.desc || null` etc. shape
 * exactly (AC-4 — no behavioural drift for the direct form).
 *
 * AC-6 (advisory, non-fatal): a DIRECTLY-supplied value containing a
 * backtick, `$(`, or an embedded double quote gets a stderr warning
 * suggesting the `-file` alternative. This is a heuristic nudge, not a
 * detector of successful shell injection — by the time this process sees
 * the string, any command substitution the outer shell was going to do has
 * already happened. Never blocks the write.
 */
// SEC-050 (Destructive finding on BUG-090): --desc-file/--note-file/
// --detail-file (resolveTextFlag below) had NO path-scope restriction --
// confirmed live by reading C:\Windows\win.ini through it. resolveTextFlag
// doesn't grant a NEW capability (an agent invoking this already has
// unrestricted filesystem read via its own tools), but it creates a new
// SINK: file content lands silently in a BOW field that other
// sessions/exports/doc pipelines may surface more broadly than intended.
// Same shared boundary-check shape as FIX-2's `isPathUnderAllowedGrepRoot`
// further down this file (GR#3 -- one "is this path in bounds"
// implementation, reused here via `isPathUnderAnyRoot` rather than
// re-derived). The allowed roots for these CLI text flags are deliberately
// wider than the tripwire grep roots (this is an operator/agent-supplied
// flag, not untrusted acceptance-doc text): the project repo root
// (legitimate -- reading a doc/note out of the repo itself) and the OS temp
// directory (legitimate -- the project's own scratchpad convention writes
// working files there, and BUG-090's own test suite already exercises
// --desc-file/--note-file against os.tmpdir()-based fixtures). Anything
// else -- /etc/passwd, C:\Windows\win.ini, an arbitrary absolute path, a
// ../ escape from either root -- is refused before the read is attempted.
const TEXT_FLAG_ALLOWED_ROOTS = [__dirname, os.tmpdir()];

function isTextFlagPathAllowed(filePath) {
  return isPathUnderAnyRoot(process.cwd(), filePath, TEXT_FLAG_ALLOWED_ROOTS);
}

/**
 * Shared overflow-check helper (BUG-151/BUG-173/BUG-027 -- GR#3: one
 * mechanism, not three near-duplicate length checks). Checks `text`'s
 * length against `maxLen` and, if it would exceed the column's real limit,
 * prints a clear, typed error naming the column, the limit, and the actual
 * length, then exits non-zero BEFORE any DB write is attempted -- never
 * lets a raw driver error ("Data too long for column '...'") leak through.
 * `context` is a short human string identifying what's being checked (e.g.
 * "closing note for BUG-145", "title for new bug item") so the message is
 * actionable, not generic. No-op for null/undefined/non-string text.
 */
function rejectIfOverColumnLimit(text, maxLen, columnLabel, context) {
  if (typeof text === 'string' && text.length > maxLen) {
    console.error(
      `claude-bow error: ${context} is too long for the "${columnLabel}" column: ` +
      `${text.length} chars, maximum is ${maxLen}. Shorten it and try again -- nothing was written.`);
    process.exit(1);
  }
}

function resolveTextFlag(fieldName) {
  const fileFlag = `${fieldName}-file`;
  const direct = flags[fieldName];
  const filePath = flags[fileFlag];
  if (direct !== undefined && filePath !== undefined) {
    console.error(`claude-bow: --${fieldName} and --${fileFlag} may not both be supplied — pick one (BUG-090: no silent precedence between a shell-quoted value and a file's content).`);
    process.exit(1);
  }
  if (filePath !== undefined) {
    if (!isTextFlagPathAllowed(filePath)) {
      console.error(`claude-bow: --${fileFlag} "${filePath}" resolves outside the allowed roots (the project repo root or the OS temp directory) -- refusing to read (SEC-050 path-scope guard). This flag is for repo docs or scratch files, not arbitrary filesystem reads.`);
      process.exit(1);
    }
    try {
      return fs.readFileSync(filePath, 'utf8');
    } catch (err) {
      console.error(`Cannot read --${fileFlag}: ${err.message}`);
      process.exit(1);
    }
  }
  if (direct && RISKY_CONTENT_RE.test(String(direct))) {
    console.error(`claude-bow: WARNING — --${fieldName} contains a backtick, "$(", or an embedded double quote. If this value came from a shell command line, the OUTER shell may already have reinterpreted it before this process ever saw it (BUG-090). Consider --${fileFlag} <path> next time. (advisory only — not blocking)`);
  }
  return direct || null;
}

// ── Commands ──────────────────────────────────────────────────────────────────

async function cmdAdd(db) {
  const type = String(positional[0] || '').toLowerCase();
  const title = positional[1];
  if (!TYPES.includes(type) || !title) {
    console.error('Usage: node claude-bow.js add <module|feature|bug|interface|assumption> "title" [--priority P0..P3] [--desc "..." | --desc-file <path>]');
    console.error('  assumption additionally REQUIRES: --code-path "<file or dir>" --codejson "<module key or GUID>"');
    console.error('  BUG-090: if --desc content contains a backtick, "$(...)", an embedded quote, or spans multiple lines, use --desc-file <path> instead — the outer shell reinterprets those characters in an inline --desc value before this tool ever sees them.');
    process.exit(1);
  }
  // Assumption logging rule (Aaron, 2026-08-09): an assumption that is not
  // traceable to the code it concerns is not logged, it is just a note. Any
  // agent may raise one, but never without both references — the Tester and
  // the developer are both required to reject work carrying unlogged
  // assumptions, and they can only check that if the link exists.
  if (type === 'assumption') {
    const missing = [];
    if (!flags['code-path']) missing.push('--code-path "<file or dir the assumption is about>"');
    if (!flags.codejson) missing.push('--codejson "<code.json module key or GUID>"');
    if (missing.length) {
      console.error('claude-bow: an assumption MUST be traceable to code. Missing:');
      for (const m of missing) console.error(`  ${m}`);
      console.error('If the assumption is not about any code yet, it belongs in the item description of the work that prompted it, not here.');
      process.exit(1);
    }
  }
  // Security findings (Destructive agent). Same traceability rule as
  // assumptions, plus a closed weakness class — an uncounted finding cannot
  // reveal a pattern, and the pattern is the point.
  if (type === 'finding') {
    const missing = [];
    if (!flags['code-path']) missing.push('--code-path "<file:line or dir>"');
    if (!flags.codejson) missing.push('--codejson "<code.json module key or GUID>"');
    if (!flags.class) missing.push(`--class <${FINDING_CLASSES.join('|')}>`);
    if (missing.length) {
      console.error('claude-bow: a security finding MUST be traceable and classified. Missing:');
      for (const m of missing) console.error(`  ${m}`);
      process.exit(1);
    }
    if (!FINDING_CLASSES.includes(String(flags.class))) {
      console.error(`claude-bow: unknown finding class "${flags.class}". Valid: ${FINDING_CLASSES.join(', ')}`);
      console.error('Use "other" only if it genuinely fits nothing — a long tail of one-offs defeats the pattern count.');
      process.exit(1);
    }
  }
  const priority = String(flags.priority || 'P2').toUpperCase();
  if (!PRIORITIES.includes(priority)) {
    console.error(`Invalid priority "${flags.priority}". Valid: ${PRIORITIES.join(', ')}`);
    process.exit(1);
  }
  // BUG-027: validate title length BEFORE the INSERT (applies to every `add`
  // subcommand -- module/feature/bug/interface/assumption/finding -- since
  // they all share the one `title` column) so an over-length title fails
  // with a clear, typed message naming the limit and given length instead
  // of a raw "Data too long for column 'title'" driver error.
  rejectIfOverColumnLimit(title, BOW_COLUMN_MAX_LEN.title, 'title', `title for new ${type} item`);
  // BUG-090 (AC-1/AC-3): resolved BEFORE the code/guid are minted and BEFORE
  // the INSERT — a mutual-exclusion or unreadable-file rejection here exits
  // non-zero with no row ever written, matching --example-file's precedent.
  const desc = resolveTextFlag('desc');
  const guid = crypto.randomUUID();
  const code = await nextCode(db, type);
  await db.query(
    `INSERT INTO bow_items (guid, code, mkey, seq, item_type, title, description, priority,
       milestone, layer, spec_ref, guid_in, guid_out, estimate_days, code_path, codejson_ref,
       finding_class)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    [guid, code, flags.mkey || null, flags.seq != null ? Number(flags.seq) : null, type, title,
     desc, priority, flags.milestone || null, flags.layer || null, flags.spec || null,
     flags['guid-in'] || null, flags['guid-out'] || null, flags.estimate != null ? Number(flags.estimate) : null,
     flags['code-path'] || null, flags.codejson || null, flags.class || null]);
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
     ORDER BY ${flags['by-seq'] ? 'ISNULL(seq), seq, priority' : 'priority, ISNULL(seq), seq, item_type, code'}`, params);

  if (!items.length) { console.log('BOW is clean — no matching items.'); return; }
  let currentP = null;
  for (const it of items) {
    if (!flags['by-seq'] && it.priority !== currentP) { currentP = it.priority; console.log(`\n${currentP}:`); }
    const dep = it.open_deps > 0 ? `  ⛓ ${it.open_deps} dep(s)` : '';
    const seq = it.seq != null ? String(it.seq).padStart(4) : '   -';
    const ms = it.milestone ? ` ${it.milestone.padEnd(6)}` : '       ';
    console.log(`  ${seq} ${it.code.padEnd(9)}${flags['by-seq'] ? ' ' + it.priority : ''}${ms}[${it.status.toUpperCase().padEnd(11)}] ${it.title}${dep}`);
  }
  console.log(`\nTotal: ${items.length}`);
}

async function cmdShow(db) {
  const item = await requireItem(db, positional[0]);
  console.log(`${item.code} — ${item.title}`);
  console.log(`  GUID:     ${item.guid}`);
  console.log(`  Type:     ${item.item_type}   Priority: ${item.priority}   Status: ${item.status}`);
  if (item.mkey || item.seq != null) console.log(`  Key:      ${item.mkey || '-'}   Seq: ${item.seq != null ? item.seq : '-'}   Sprint: ${item.sprint != null ? item.sprint : '-'}`);
  if (item.milestone || item.layer) console.log(`  Phase:    ${item.milestone || '-'}   Layer: ${item.layer || '-'}`);
  if (item.spec_ref) console.log(`  Spec:     ${item.spec_ref}`);
  if (item.code_path || item.codejson_ref) {
    console.log(`  Code:     ${item.code_path || '-'}`);
    console.log(`  code.json:${item.codejson_ref || '-'}`);
  }
  if (item.guid_in || item.guid_out) {
    console.log(`  IF in:    ${item.guid_in || '-'}`);
    console.log(`  IF out:   ${item.guid_out || '-'}`);
  }
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
    console.error('  BUG-090: prefer --example-file over --example when the code contains a backtick, "$(...)", or an embedded quote — the outer shell reinterprets those inside an inline argument before this tool ever sees them.');
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
  if (!flags.on) { console.error('Usage: node claude-bow.js depend <code> --on <code> [--note "..." | --note-file <path>]'); process.exit(1); }
  const target = await requireItem(db, flags.on);
  if (target.guid === item.guid) { console.error('An item cannot depend on itself.'); process.exit(1); }
  const note = resolveTextFlag('note'); // BUG-090 (AC-2/AC-3)

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
    [item.guid, target.guid, note]);
  console.log(`${item.code} now depends on ${target.code}${note ? ' — ' + note : ''}.`);
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
    console.error('Usage: node claude-bow.js ref <code> <commit-hash 7-40 hex> [--note "..." | --note-file <path>]');
    process.exit(1);
  }
  const note = resolveTextFlag('note'); // BUG-090 (AC-2/AC-3)
  let branch = null;
  try { branch = execSync('git rev-parse --abbrev-ref HEAD', { cwd: __dirname, encoding: 'utf8', timeout: 5000 }).trim(); }
  catch { /* not fatal — branch is decoration */ }
  await db.query(
    'INSERT INTO bow_git_refs (item_guid, commit_hash, branch, note) VALUES (?, ?, ?, ?)',
    [item.guid, hash.toLowerCase(), branch, note]);
  console.log(`Linked commit ${hash.slice(0, 10)} to ${item.code}${note ? ' — ' + note : ''}.`);
}

/**
 * FEAT-040 (tool.destructiveguard, GR#23) — record a Destructive-agent
 * verdict against a BOW item. Exported (alongside findItemByRef) so
 * claude-destructive-guard.js can `require('./claude-bow.js')` this directly
 * rather than shelling out or re-implementing the write (a subprocess-per-
 * check design would itself be a finding under weakness pattern #6 — see
 * tool.destructiveguard.md AC-8). All validation happens here, BEFORE the
 * INSERT, so a bad --class/--findings/--attacker value can never produce a
 * partial row (AC-2/AC-3/AC-4 all-or-nothing).
 *
 * `ref` resolves via findItem (code/mkey/guid) — an unknown ref throws
 * without writing anything (AC-5). `opts.classes`/`opts.findings` accept
 * either a comma-separated string (the CLI shape) or an array.
 */
async function recordDestructiveVerdict(db, ref, opts = {}) {
  const item = await findItem(db, ref);
  if (!item) {
    throw new Error(`no BOW item matches "${ref}" (use a code like FEAT-040 or a GUID)`);
  }

  const verdict = String(opts.verdict || '').trim().toLowerCase();
  if (verdict !== 'accept' && verdict !== 'reject') {
    throw new Error(`--verdict must be exactly "accept" or "reject" (got "${opts.verdict}")`);
  }

  const attacker = String(opts.attacker == null ? '' : opts.attacker).trim();
  if (!attacker) {
    throw new Error('--attacker is required and must be non-empty — an unnamed attacker is not an audit trail');
  }

  const toList = (v) => {
    if (v == null) return [];
    if (Array.isArray(v)) return v.map(String).map(s => s.trim()).filter(Boolean);
    return String(v).split(',').map(s => s.trim()).filter(Boolean);
  };

  // AC-3: validated against the ONE existing FINDING_CLASSES constant
  // (already used by `add finding`) — never a second, independently-typed list.
  const classes = toList(opts.classes);
  for (const c of classes) {
    if (!FINDING_CLASSES.includes(c)) {
      throw new Error(`unknown weakness class "${c}". Valid: ${FINDING_CLASSES.join(', ')}`);
    }
  }

  // AC-4: every --findings code must resolve via findItemByRef BEFORE any
  // write happens — an unresolvable code aborts the whole write, no partial row.
  const findingsList = toList(opts.findings);
  for (const f of findingsList) {
    const finding = await findItem(db, f);
    if (!finding) {
      throw new Error(`--findings references unknown BOW code "${f}" — resolve or drop it before recording`);
    }
  }

  const guid = crypto.randomUUID();
  await db.query(
    `INSERT INTO bow_destructive_verdicts (guid, item_guid, verdict, attacker, weakness_classes, findings, note)
     VALUES (?, ?, ?, ?, ?, ?, ?)`,
    [guid, item.guid, verdict, attacker, classes.length ? classes.join(',') : null,
     findingsList.length ? findingsList.join(',') : null, opts.note || null]);

  return { guid, item, verdict, attacker, classes, findings: findingsList };
}

/**
 * FEAT-040 — the LATEST (greatest created_at, id as a deterministic
 * tiebreaker — see AC-27) Destructive verdict row for an item, or null if
 * either the ref does not resolve or no verdict has ever been recorded.
 * Callers that need to distinguish "unknown ref" from "no verdict yet" must
 * resolve the ref themselves first (see cmdVerdict / the guard's own usage).
 */
async function latestDestructiveVerdict(db, ref) {
  const item = await findItem(db, ref);
  if (!item) return null;
  const [rows] = await db.query(
    `SELECT * FROM bow_destructive_verdicts WHERE item_guid = ?
     ORDER BY created_at DESC, id DESC LIMIT 1`,
    [item.guid]);
  return rows.length ? rows[0] : null;
}

async function cmdDestructive(db) {
  const ref = positional[0];
  if (!ref) {
    console.error('Usage: node claude-bow.js destructive <code|mkey> --verdict accept|reject --attacker "<name>" [--class c1[,c2,...]] [--findings SEC-001[,...]] [--note "..." | --note-file <path>]');
    process.exit(1);
  }
  try {
    const result = await recordDestructiveVerdict(db, ref, {
      verdict: flags.verdict,
      attacker: flags.attacker,
      classes: flags.class,
      findings: flags.findings,
      note: resolveTextFlag('note'), // BUG-090 (AC-2/AC-3)
    });
    console.log(`Recorded ${result.verdict.toUpperCase()} verdict on ${result.item.code} by "${result.attacker}".`);
    if (result.classes.length) console.log(`  Classes:  ${result.classes.join(', ')}`);
    if (result.findings.length) console.log(`  Findings: ${result.findings.join(', ')}`);
  } catch (err) {
    console.error(`claude-bow destructive: ${err.message}`);
    process.exit(1);
  }
}

async function cmdVerdict(db) {
  const ref = positional[0];
  if (!ref) {
    console.error('Usage: node claude-bow.js verdict <code|mkey> [--json]');
    process.exit(1);
  }
  const item = await requireItem(db, ref);
  const v = await latestDestructiveVerdict(db, ref);

  if (flags.json) {
    console.log(JSON.stringify({
      code: item.code,
      mkey: item.mkey || null,
      verdict: v ? v.verdict : null, // AC-7: explicit null, never an absent field
      attacker: v ? v.attacker : null,
      classes: v && v.weakness_classes ? v.weakness_classes.split(',') : [],
      findings: v && v.findings ? v.findings.split(',') : [],
      note: v ? v.note : null,
      created_at: v ? ts(v.created_at) : null,
    }));
    return;
  }

  if (!v) {
    console.log(`${item.code}: no verdict recorded.`);
    return;
  }
  console.log(`${item.code}: ${v.verdict.toUpperCase()} by "${v.attacker}" at ${ts(v.created_at)}`);
  if (v.weakness_classes) console.log(`  Classes:  ${v.weakness_classes}`);
  if (v.findings) console.log(`  Findings: ${v.findings}`);
  if (v.note) console.log(`  Note:     ${v.note}`);
}

/**
 * BUG-075: a cheap batch existence check so verifying a report's cited BOW
 * codes is one command rather than N separate `show` calls. BUG-075's two
 * incidents were both a citation problem — a code that was never filed at
 * all, and a code that IS real but does not say what the citing report
 * claimed. This command answers the FIRST half mechanically (does the code
 * resolve?) and hands back enough (the one-line title) for a human/agent to
 * eyeball the SECOND half without a second round-trip. It does not and
 * cannot fully automate "matches what the report claims" — that still needs
 * a `show` and a read, per dev-team-process.md's citation-verification duty.
 */
async function cmdExists(db) {
  const raw = [...positional];
  if (flags.codes) raw.push(...String(flags.codes).split(','));
  const codes = raw.flatMap((c) => String(c).split(',')).map((c) => c.trim()).filter(Boolean);

  if (!codes.length) {
    console.error('Usage: node claude-bow.js exists CODE1 CODE2 ... | --codes CODE1,CODE2,...');
    console.error('  BUG-075: verify claimed ASM/BUG/FEAT/etc. codes actually resolve before citing or relaying them as fact.');
    process.exit(1);
  }

  // Dedupe case-insensitively, preserving first-seen order for output —
  // one caller-visible line per distinct code asked about, however many
  // times it was repeated on the command line.
  const seen = new Set();
  const uniqueCodes = [];
  for (const c of codes) {
    const key = c.toUpperCase();
    if (!seen.has(key)) { seen.add(key); uniqueCodes.push(c); }
  }

  // Single round-trip: match by short code (case-insensitive) or mkey, the
  // same two-way lookup findItem()/requireItem() use for one code, batched
  // via IN(...) for all of them at once.
  const upperCodes = uniqueCodes.map((c) => c.toUpperCase());
  const placeholders = uniqueCodes.map(() => '?').join(',');
  const [rows] = await db.query(
    `SELECT code, title, mkey FROM bow_items WHERE UPPER(code) IN (${placeholders}) OR mkey IN (${placeholders})`,
    [...upperCodes, ...uniqueCodes]);

  const byUpperCode = new Map();
  const byMkey = new Map();
  for (const r of rows) {
    byUpperCode.set(r.code.toUpperCase(), r);
    if (r.mkey) byMkey.set(r.mkey, r);
  }

  let foundCount = 0;
  for (const c of uniqueCodes) {
    const row = byUpperCode.get(c.toUpperCase()) || byMkey.get(c);
    if (row) { foundCount++; console.log(`${c}: EXISTS — ${row.code} — ${row.title}`); }
    else console.log(`${c}: NOT FOUND`);
  }
  console.log(`\n${foundCount}/${uniqueCodes.length} exist.`);
}

/**
 * Weakness-pattern report over security findings.
 *
 * The reason findings carry a closed `finding_class` is so this report can
 * exist. A list of N findings is a to-do list; N findings where 6 share one
 * class is a statement about how the team writes code, and the fix for that is
 * teaching, not tickets. Recurrence is the signal — so recurring classes are
 * called out explicitly rather than left for a reader to notice.
 */
async function cmdWeakness(db) {
  const [rows] = await db.query(
    `SELECT finding_class AS cls, status, COUNT(*) AS n
       FROM bow_items WHERE item_type = 'finding' GROUP BY finding_class, status`);
  if (!rows.length) { console.log('No security findings recorded yet.'); return; }

  const byClass = new Map();
  let total = 0, open = 0;
  for (const r of rows) {
    const cls = r.cls || '(unclassified)';
    const e = byClass.get(cls) || { total: 0, open: 0 };
    e.total += Number(r.n);
    if (OPEN_STATUSES.includes(r.status)) e.open += Number(r.n);
    byClass.set(cls, e);
    total += Number(r.n);
    if (OPEN_STATUSES.includes(r.status)) open += Number(r.n);
  }

  console.log(`Security weakness patterns — ${total} finding(s), ${open} still open\n`);
  const sorted = [...byClass.entries()].sort((a, b) => b[1].total - a[1].total);
  const width = Math.max(...sorted.map(([c]) => c.length));
  for (const [cls, e] of sorted) {
    const bar = '#'.repeat(Math.min(e.total, 40));
    console.log(`  ${cls.padEnd(width)}  ${String(e.total).padStart(3)} total  ${String(e.open).padStart(3)} open  ${bar}`);
  }

  const recurring = sorted.filter(([, e]) => e.total >= 3);
  if (recurring.length) {
    console.log('\nRECURRING (>=3) — these are training signals, not just defects:');
    for (const [cls, e] of recurring) {
      console.log(`  ${cls} x${e.total} — the devs keep writing this. Fixing each instance treats the symptom.`);
    }
  } else {
    console.log('\nNo class has recurred 3+ times yet — too early to call a pattern.');
  }
}

// ── Lint (FEAT-060): prose-vs-graph drift ───────────────────────────────────
//
// Scans bow_items.description and bow_comments.body (never example_code,
// AC-2 — that column exists precisely so a code named inside a worked
// example is not conflated with a code named in prose) for BOW-code-shaped
// tokens, and reports three drift classes against the real bow_dependencies
// graph and bow_items.status:
//   Class 1 — prose asserts a gating relationship via one of a closed set of
//             phrase templates, but no bow_dependencies row backs it up
//             (the BUG-012 shape).
//   Class 2 — a cited code does not resolve to any real bow_items row
//             (the BUG-075 shape).
//   Class 3 — a `done` item's own text still gating-cites a code that is
//             not actually closed (the prose-level mirror of GR#12, which
//             only ever sees bow_dependencies rows, not description text).
// Report-only (AC-10/AC-11): this command never exits nonzero for a finding
// and is not wired into any hook/CI path in this version — see
// docs/planning/acceptance/tool.bowlint.md.

// Class-1/Class-3 shared gating-phrase templates (GR#15: this is the single
// place the list lives). Deliberately short and precision-favoring — a wider
// "code near the word gate" heuristic was rejected because it would fire on
// every incidental SEC-030/SEC-031-style citation in a file like
// tool.astgate.md's own acceptance criteria. Growing this list is expected
// future work (Bill's call, per the escalation logged in tool.bowlint.md),
// not a defect in this version.
const GATING_PHRASES = [
  'gate (it )?against',
  'gated against',
  'gating against',
  'gate (it )?on',
  'gated on',
  'blocked by',
  'blocks on',
  'must land before',
  'whichever lands first',
];
const GATING_PHRASE_RE = new RegExp(`(${GATING_PHRASES.join('|')})`, 'i');

// ── Destructive-driven tightening (2026-08-11, see ASM-406/ASM-40x below) ──
//
// Fix 2 (P1): same-sentence proximity alone let an unrelated code cited
// elsewhere in a long sentence be treated as the TARGET of a gating phrase
// (real 5/8 false-positive rate the Destructive found against the live BOW —
// see the brief's four worked examples). Named per GR#15, not inline magic
// numbers/lists.
//
// GATING_MAX_WORD_DISTANCE: how many whole words may separate a matched
// gating phrase from a code token for that token to still count as the
// phrase's target. 6 is a judgment call, not derived from data — it is
// "still plausibly the same clause" without a real dependency parse. Chosen
// over a stricter value (e.g. 3) because AC-5's own BUG-012 fixture is
// "gated against MOD-068/MOD-069 or INT-004" where the third code sits ~4
// words from the phrase; a threshold below that would break a fixture this
// file's own acceptance criteria requires to pass.
const GATING_MAX_WORD_DISTANCE = 6;

// NEGATION_WINDOW / NEGATION_WORDS: a negation particle found within this
// many words immediately BEFORE a matched gating phrase inverts its meaning
// ("is NOT blocked by") and suppresses the match entirely for that
// occurrence. Simple lexical check, not a parser, per the brief's explicit
// "not a full NLP solution" instruction.
const NEGATION_WINDOW = 3;
const NEGATION_WORDS = ['not', "n't", 'never', 'no longer', 'without'];

// GATING_JOIN_RE (Fix 1, P0, round-2 Destructive): the characters/words
// allowed to sit BETWEEN two code tokens for the second to still count as
// part of the same gating claim as the first — a '/' character, a ',', the
// literal word "or", or plain whitespace, in any combination. Anything else
// between two tokens (a real word, "see also", "for background context", …)
// means the second token is an incidental citation, not a joined target.
// Named per GR#15 rather than an inline literal, matching AC-5's own wording
// ("multiple codes joined by `/`/`or`").
const GATING_JOIN_RE = /^[\s,/]*$/;

/**
 * True if `between` (the raw sentence substring separating two already-
 * adjacent-in-order code tokens) contains nothing but joining punctuation/
 * words ('/', ',', "or", whitespace) — Fix 1. The literal word "or" is
 * stripped first (word-bounded, so it never eats the "or" inside "before")
 * before checking the remainder is pure whitespace/slash/comma.
 */
function isPureJoin(between) {
  return GATING_JOIN_RE.test(between.replace(/\bor\b/gi, ''));
}

// SENTENCE_ABBREVIATIONS (Fix 3): common abbreviation/decimal shapes whose
// internal period must never be treated as a sentence boundary by
// splitSentences. A period between two digits (decimal versions, "v1.2.3")
// is handled separately below by pattern, not by this list.
const SENTENCE_ABBREVIATIONS = ['e.g', 'i.e', 'etc', 'vs', 'approx', 'fig', 'cf', 'no'];

// KNOWN LIMITATION (round-2 Destructive, P2, disclosed not fixed — see
// tool.bowlint.md FIX 3(b)/brief): this is a fixed, finite list, not
// abbreviation detection. Any abbreviation NOT on it (e.g. "ref.", "approx."
// variants not spelled exactly as listed, a future writer's own shorthand)
// still fragments the sentence at that period, which can separate a gating
// phrase from its target and lose a genuine finding — e.g. "blocked by ref.
// MOD-073" splits into "blocked by ref" / "MOD-073" and the two halves never
// share a sentence, so the match is missed entirely. Growing this list for a
// real missed instance is expected future work (same posture as
// GATING_PHRASES above), not a defect in this version; a full fix would need
// real abbreviation/NLP detection, out of scope for a "not a full NLP
// solution" tool.

/**
 * BOW-code-shaped token regex, built at call time from TYPE_PREFIX's own
 * values (GR#15 — never a second, independently-typed prefix list; a 7th
 * type added to TYPE_PREFIX is picked up automatically, unlike a literal
 * /\b(MOD|FEAT|BUG|INT|ASM|SEC)-\d+\b/ typed into this function).
 */
function codeTokenRegex() {
  const prefixes = Object.values(TYPE_PREFIX).join('|');
  return new RegExp(`\\b(?:${prefixes})-\\d+\\b`, 'g');
}

/**
 * Find every ```-fenced code block's [start, end) character range in text.
 * Fix 3: a token on its own line inside a triple-backtick fence is preceded
 * by a newline, not a backtick, so the inline single-backtick "quoted" check
 * in extractCodeTokens misses it entirely unless we also check fence ranges.
 */
function fencedBlockRanges(text) {
  const ranges = [];
  const re = /```[\s\S]*?```/g;
  let m;
  while ((m = re.exec(text))) {
    ranges.push([m.index, m.index + m[0].length]);
  }
  return ranges;
}

/**
 * Extract every code-shaped token from one text field (description or
 * comment body — never example_code, AC-2). Each token records its
 * character span (start/end, used by Fix 2's word-distance check below) and
 * whether it sits inside a single backtick span OR a triple-backtick fenced
 * block (Fix 3): AC-3 treats a backtick-quoted code as "cited as an
 * illustrative example" (this project's own established convention), not
 * "asserting a live gating relationship." Per AC-3's literal text and
 * ASM-406's correction (Fix 1), this `quoted` flag is consumed ONLY by the
 * Class-1 branch of runLint — Class-2 always uses the full token list
 * regardless, and Class-3 also uses the full list (not filtered here).
 */
function extractCodeTokens(text) {
  if (!text) return [];
  const re = codeTokenRegex();
  const fenced = fencedBlockRanges(text);
  const out = [];
  let m;
  while ((m = re.exec(text))) {
    const start = m.index;
    const end = start + m[0].length;
    const backtickQuoted = text[start - 1] === '`' && text[end] === '`';
    const fenceQuoted = fenced.some(([fs, fe]) => start >= fs && end <= fe);
    out.push({ code: m[0].toUpperCase(), quoted: backtickQuoted || fenceQuoted, start, end });
  }
  return out;
}

/**
 * Guard a text's sentence-ending periods that must NOT be treated as
 * sentence boundaries by splitSentences (Fix 3): decimal versions
 * ("v1.2.3") and common abbreviations (SENTENCE_ABBREVIATIONS). Protected
 * periods are swapped for a sentinel control character (never legitimately
 * present in BOW prose) and restored after splitting.
 */
const SENTENCE_BOUNDARY_SENTINEL = '';
function protectSentenceBoundaries(text) {
  let out = text.replace(/(\d)\.(?=\d)/g, `$1${SENTENCE_BOUNDARY_SENTINEL}`);
  for (const abbr of SENTENCE_ABBREVIATIONS) {
    const escaped = abbr.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    const re = new RegExp(`\\b${escaped}\\.`, 'gi');
    out = out.replace(re, (whole) => whole.slice(0, -1) + SENTENCE_BOUNDARY_SENTINEL);
  }
  return out;
}

/**
 * Split free text into sentences bounded by '.', ';' or a newline — the unit
 * the Class-1/Class-3 gating-phrase match is scoped to (AC-4: "the same
 * sentence that contains the code token", not the whole field — a wide
 * whole-field window is exactly the noisier alternative AC-6 rejects).
 * Fix 3: periods inside decimals/abbreviations are protected first so they
 * do not fragment a sentence away from the gating phrase it belongs with.
 */
function splitSentences(text) {
  if (!text) return [];
  return protectSentenceBoundaries(text)
    .split(/[.;\n]+/)
    .map((s) => s.replace(new RegExp(SENTENCE_BOUNDARY_SENTINEL, 'g'), '.').trim())
    .filter(Boolean);
}

/** \S+-token positions within a sentence, used by the word-distance check. */
function wordsWithIndex(sentence) {
  const out = [];
  const re = /\S+/g;
  let m;
  while ((m = re.exec(sentence))) {
    out.push({ start: m.index, end: m.index + m[0].length, word: m[0] });
  }
  return out;
}

/** The [firstWordIdx, lastWordIdx] range of `words` overlapping [start, end). */
function wordIndexRange(words, start, end) {
  let first = -1;
  let last = -1;
  for (let i = 0; i < words.length; i++) {
    if (words[i].end > start && words[i].start < end) {
      if (first === -1) first = i;
      last = i;
    }
  }
  return [first, last];
}

/** Whole-word distance between two character spans in the same sentence. */
function wordDistance(words, aStart, aEnd, bStart, bEnd) {
  const [aFirst, aLast] = wordIndexRange(words, aStart, aEnd);
  const [bFirst, bLast] = wordIndexRange(words, bStart, bEnd);
  if (aFirst === -1 || bFirst === -1) return Infinity;
  if (aLast < bFirst) return bFirst - aLast - 1;
  if (bLast < aFirst) return aFirst - bLast - 1;
  return 0;
}

/**
 * Fix 2 negation check: does a NEGATION_WORDS particle appear within
 * NEGATION_WINDOW words immediately before the matched phrase's first word?
 * ("engine.roads (MOD-024) is NOT blocked by this" — real Destructive find.)
 *
 * Round-2 Destructive P1 fix: NEGATION_WORDS contains a multi-word entry
 * ("no longer"), but the single-word per-token check below can never match a
 * multi-word string against one \S+ token — that made "no longer" dead code,
 * unreachable under any input. Multi-word entries are therefore checked
 * separately against the raw sentence substring covering the same lookback
 * window, not the tokenized words.
 */
function isNegatedPhrase(sentence, words, phraseFirstWordIdx) {
  if (phraseFirstWordIdx === -1) return false;
  const from = Math.max(0, phraseFirstWordIdx - NEGATION_WINDOW);

  // Single-word/contraction particles: per-token check (unchanged from Fix 2).
  for (let i = from; i < phraseFirstWordIdx; i++) {
    const w = words[i].word.toLowerCase().replace(/[^a-z']/g, '');
    if (NEGATION_WORDS.some((n) => !n.includes(' ') && (w === n || w.includes(n)))) return true;
  }

  // Multi-word particles ("no longer"): substring check against the raw text
  // spanning the same NEGATION_WINDOW-word lookback, since they cannot match
  // any single token above.
  const windowStartWord = words[from];
  const phraseWord = words[phraseFirstWordIdx];
  if (windowStartWord && phraseWord) {
    const snippet = sentence.slice(windowStartWord.start, phraseWord.start).toLowerCase();
    if (NEGATION_WORDS.some((n) => n.includes(' ') && snippet.includes(n))) return true;
  }

  return false;
}

/**
 * Fix 2 self-referential/meta-description check: a matched phrase wrapped in
 * quote marks (e.g. `...text says 'gate against X' with X still open...` —
 * the tool's own spec describing its detection rule, a real Destructive
 * find) is a citation of the phrase, not a live claim using it. General
 * heuristic chosen over a hardcoded FEAT-060-only exclusion (see report):
 * quoting is how this project already marks "an example of the pattern"
 * (AC-3's own backtick convention makes the same move for codes), and it
 * generalises to any future item that quotes the lint's own phrase list
 * back at readers, not just FEAT-060.
 */
function isQuotedPhraseCitation(sentence, phraseMatch) {
  const before = sentence.slice(0, phraseMatch.index);
  const after = sentence.slice(phraseMatch.index + phraseMatch[0].length);
  return /['"‘“]\s*$/.test(before) && /['"’”]/.test(after);
}

// KNOWN LIMITATION (round-2 Destructive, P2, disclosed not fixed — see
// tool.bowlint.md FIX 3(a)/brief): this is a simple "is it inside quote
// marks" heuristic, not attribution parsing, so it cannot distinguish the
// intended case (a sentence quoting the lint's OWN phrase list back at the
// reader, e.g. this file's own doc comments) from a genuine, attributed,
// sourced claim that happens to be quoted for citation, e.g. `Reported by
// Aaron: "blocked by MOD-500, needs fixing"` — that IS a live gating claim,
// but this heuristic wrongly suppresses it to zero findings because the
// phrase sits inside quote marks either way. A real fix needs attribution/
// speech-act parsing, which is out of scope for a "not a full NLP solution"
// tool (see the six-phrase gating-vocabulary tradeoff above for the same
// kind of accepted-precision-loss reasoning). Left as a known gap.

/**
 * Sentence-level gating-phrase scan, shared verbatim by Class-1 and Class-3
 * (AC-9 — one phrase list, one sentence-bounding rule; only the downstream
 * check differs between the two classes). Returns, per matching sentence,
 * the matched phrase text and every code token within GATING_MAX_WORD_
 * DISTANCE words of it (Fix 2), including quoted ones — Fix 1/ASM-406:
 * backtick suppression is no longer applied here at the shared level; the
 * `quoted` flag rides through on each token and is filtered ONLY by the
 * Class-1 branch of runLint, per AC-3's literal "Class-1's phrase-trigger
 * check" scoping. A phrase match that is negated (Fix 2) or is itself a
 * quoted citation of the phrase (Fix 2, meta-description) never produces a
 * reference at all, for either class.
 *
 * Round-2 Destructive P0 fix: proximity to the phrase (GATING_MAX_WORD_
 * DISTANCE) is no longer sufficient on its own to call a token the phrase's
 * target. Only the FIRST code token following the phrase within that
 * distance is taken as the anchor target; any further token is accepted ONLY
 * if the raw text between it and the previous confirmed target is nothing
 * but a joining construct (GATING_JOIN_RE / isPureJoin — '/', ',', "or",
 * whitespace). The moment real prose sits between two tokens ("see also",
 * "for background context", …), extension stops — that token, and anything
 * after it, is not part of this reference. This is what lets "gate it
 * against MOD-068/MOD-069 or INT-004" (AC-5's own shape) still yield all
 * three codes while "gated against MOD-503, see also BUG-599 for background
 * context" yields only MOD-503.
 */
function findGatingReferences(text) {
  const results = [];
  for (const sentence of splitSentences(text)) {
    const phraseMatch = sentence.match(GATING_PHRASE_RE);
    if (!phraseMatch) continue;
    if (isQuotedPhraseCitation(sentence, phraseMatch)) continue;

    const words = wordsWithIndex(sentence);
    const phraseStart = phraseMatch.index;
    const phraseEnd = phraseStart + phraseMatch[0].length;
    const [phraseFirstWordIdx] = wordIndexRange(words, phraseStart, phraseEnd);
    if (isNegatedPhrase(sentence, words, phraseFirstWordIdx)) continue;

    // Candidates in sentence order, restricted to tokens that follow the
    // phrase — the real gating shapes this tool targets ("gate against X",
    // "blocked by X/Y") always name the target(s) after the phrase.
    const candidates = extractCodeTokens(sentence)
      .filter((t) => t.start >= phraseEnd)
      .sort((a, b) => a.start - b.start);

    let anchorIdx = -1;
    for (let i = 0; i < candidates.length; i++) {
      const dist = wordDistance(words, phraseStart, phraseEnd, candidates[i].start, candidates[i].end);
      if (dist <= GATING_MAX_WORD_DISTANCE) { anchorIdx = i; break; }
    }
    if (anchorIdx === -1) continue;

    const tokens = [candidates[anchorIdx]];
    let prevEnd = candidates[anchorIdx].end;
    for (let i = anchorIdx + 1; i < candidates.length; i++) {
      const between = sentence.slice(prevEnd, candidates[i].start);
      if (!isPureJoin(between)) break;
      tokens.push(candidates[i]);
      prevEnd = candidates[i].end;
    }

    results.push({ phrase: phraseMatch[0], tokens });
  }
  return results;
}

/**
 * Pure three-class lint over already-fetched rows — no DB access inside this
 * function, so the test suite can exercise real detection logic directly
 * against fixture rows (AC-13..16) without a round trip per token.
 *
 *   items:    [{ guid, code, description, status }, ...] — every bow_items
 *             row, open and closed both (AC-2 — Class 3 needs closed items).
 *   comments: [{ item_guid, body }, ...] — every bow_comments row.
 *   deps:     Set of "item_guid|depends_on_guid" strings from
 *             bow_dependencies.
 *
 * Returns { class1, class2, class3 }, each an array of finding strings in
 * the exact wording AC-4/AC-7/AC-8 specify.
 */
function runLint(items, comments, deps) {
  const byCode = new Map();
  for (const it of items) byCode.set(String(it.code).toUpperCase(), it);
  const commentsByItem = new Map();
  for (const c of comments) {
    const list = commentsByItem.get(c.item_guid) || [];
    list.push(c.body);
    commentsByItem.set(c.item_guid, list);
  }

  const class1 = [];
  const class2 = [];
  const class3 = [];
  // One finding per unique (owner, cited-code) pair, not per raw occurrence —
  // an item that names the same missing/fabricated/stale code twice (once in
  // its description, once in a follow-up comment) is one drift instance, not
  // two lines in the report.
  const seenClass1 = new Set();
  const seenClass2 = new Set();
  const seenClass3 = new Set();

  for (const item of items) {
    const texts = [item.description, ...(commentsByItem.get(item.guid) || [])];
    for (const text of texts) {
      if (!text) continue;

      // Class 2 (AC-7): every extracted token, quoted or not, regardless of
      // phrase match — checked for existence against bow_items, case
      // insensitively, the same UPPER(code)=UPPER(?) semantics findItem uses.
      for (const tok of extractCodeTokens(text)) {
        if (byCode.has(tok.code)) continue;
        const key = `${item.code}|${tok.code}`;
        if (seenClass2.has(key)) continue;
        seenClass2.add(key);
        class2.push(`${item.code} — cites "${tok.code}" which does not exist in bow_items`);
      }

      // Class 1 / Class 3 — sentence-bounded gating-phrase matches (AC-4/AC-5
      // /AC-6, AC-8/AC-9). A token that doesn't resolve is Class-2's concern
      // only — Class 1/3 both need a real target item to check a real
      // dependency row or a real status against.
      for (const ref of findGatingReferences(text)) {
        for (const tok of ref.tokens) {
          const target = byCode.get(tok.code);
          if (!target) continue;

          // Fix 1 (ASM-406 correction): AC-3 scopes backtick/fence
          // suppression to "Class-1's phrase-trigger check" ONLY — Class-3
          // below deliberately does NOT check tok.quoted, so a done item
          // citing a backtick-quoted still-open gate is still reported.
          const hasDep = deps.has(`${item.guid}|${target.guid}`);
          if (!tok.quoted && !hasDep) {
            const key1 = `${item.code}|${target.code}`;
            if (!seenClass1.has(key1)) {
              seenClass1.add(key1);
              class1.push(`${item.code} — prose says "${ref.phrase}" about ${target.code}, but no bow_dependencies row links them`);
            }
          }

          if (item.status === 'done' && OPEN_STATUSES.includes(target.status)) {
            const key3 = `${item.code}|${target.code}`;
            if (!seenClass3.has(key3)) {
              seenClass3.add(key3);
              class3.push(`${item.code} — done, but its own text says "${ref.phrase}" against ${target.code}, which is still ${target.status}`);
            }
          }
        }
      }
    }
  }

  return { class1, class2, class3 };
}

/**
 * `node claude-bow.js lint` — report-only BOW hygiene check (FEAT-060).
 * Always exits 0 for findings (AC-10) — genuine usage/connection errors
 * still bubble to the outer try/catch's process.exit(1), same as every
 * other command.
 */
async function cmdLint(db) {
  const [items] = await db.query('SELECT guid, code, description, status FROM bow_items');
  const [comments] = await db.query('SELECT item_guid, body FROM bow_comments');
  const [depRows] = await db.query('SELECT item_guid, depends_on_guid FROM bow_dependencies');
  const deps = new Set(depRows.map((d) => `${d.item_guid}|${d.depends_on_guid}`));

  const { class1, class2, class3 } = runLint(items, comments, deps);
  const total = class1.length + class2.length + class3.length;

  console.log('BOW lint — prose-vs-graph drift (report-only, always exits 0)\n');
  if (!total) {
    console.log('No drift found: every prose-cited gating relationship is wired, every cited code resolves, no done item cites a still-open gate.');
    return;
  }
  if (class1.length) {
    console.log(`Class 1 — prose names a gate, no bow_dependencies row (${class1.length}):`);
    for (const f of class1) console.log(`  ${f}`);
    console.log('');
  }
  if (class2.length) {
    console.log(`Class 2 — cited code does not resolve (${class2.length}):`);
    for (const f of class2) console.log(`  ${f}`);
    console.log('');
  }
  if (class3.length) {
    console.log(`Class 3 — done item cites a gate that is still open (${class3.length}):`);
    for (const f of class3) console.log(`  ${f}`);
    console.log('');
  }
  console.log(`${total} finding(s) total. Report-only — this command never exits nonzero for findings.`);
}

// ── Sprint gate (FEAT-061, tool.sprintgate) ─────────────────────────────────
//
// A mechanical, per-sprint gate checklist run before the first dispatch of
// sprint N (docs/planning/acceptance/tool.sprintgate.md). Five checks:
//   1. data-files       — every data/*.json an in-scope AC's Check: clause
//                          names exists, is non-trivially-empty, and (where a
//                          loader test is named) reports schema-valid;
//                          numeric leaves without a provenance/placeholder
//                          marker are flagged (AC-4..AC-9).
//   2. call-edges       — every module-to-module call an in-scope AC asserts
//                          has a real registered code.json outbound edge,
//                          checked against a freshly-read code.json every run
//                          (AC-10..AC-12). This is REGISTRATION-only: it
//                          confirms the edge is PLANNED in code.json, not that
//                          real Go code already imports across it — a
//                          materially weaker claim than FEAT-062's AST-backed
//                          check. FEAT-062 (tools/plan/codejson-audit.js,
//                          done 2026-08-13) now exists and exports a reusable
//                          runAudit(), but check 2 does NOT call it — this is
//                          a deliberate, logged AC-12 deferral (ASM-483, BOW
//                          comment on FEAT-061), not an oversight or a stale
//                          "FEAT-062 doesn't exist yet" claim. Reason: check 2
//                          runs BEFORE dispatch, when the sprint's modules are
//                          NOT YET 'done' and no Go code exists to AST-parse —
//                          FEAT-062's edge check is scoped to BOTH endpoints
//                          being 'done', so for check 2's actual use case it
//                          would just report 'skip', adding a live DB
//                          connection + `go run` subprocess sweep of the
//                          whole repo for no extra signal. runAudit() also
//                          hardcodes ROOT/CODE_JSON_PATH with no fixture-path
//                          override, so reuse would break this check's
//                          isolated unit tests. See ASM-483 for the full
//                          reasoning and the deferred-consolidation trigger.
//   3. tripwires        — every "Check (once unblocked)" AC in scope has an
//                          adjacent Tripwire (mechanical...) block, and that
//                          block's LIVE exit code matches its own documented
//                          expectation (AC-13..AC-15, the BUG-100 standard).
//   4. boundary-rulings — cross-module boundary rulings (tagged per AC-16, or
//                          caught by AC-17's keyword heuristic) are cited in
//                          BOTH affected modules' acceptance files
//                          (AC-16..AC-19). Report-only — never gates overall.
//   5. ready-queue      — FEAT-060's lint is green, or a graceful `skipped`
//                          verdict while FEAT-060 doesn't exist yet
//                          (AC-20..AC-22).
//
// Every run writes exactly 5 rows to bow_gate_verdicts sharing one
// gate_run_guid (AC-25); the overall verdict is always DERIVED from those 5
// rows (AC-26), never a 6th hand-set field.

const ACCEPTANCE_DIR = path.join(__dirname, 'docs', 'planning', 'acceptance');
const CODE_JSON_PATH = path.join(__dirname, 'code.json');
const SPRINT_PLAN_PATH = path.join(__dirname, 'docs', 'planning', 'sprint-plan-v1.md');
const GATE_CHECK_NAMES = ['data-files', 'call-edges', 'tripwires', 'boundary-rulings', 'ready-queue'];
const GATE_VERDICTS = ['pass', 'fail', 'partial', 'skipped'];
// Checks that gate the overall verdict — check 4 (boundary-rulings) is
// advisory-only per AC-19 and is deliberately excluded here.
const GATING_CHECK_NUMBERS = [1, 2, 3, 5];
// FIX-2 (P0 verdict-gaming remediation, Destructive finding on FEAT-061).
// AC-24 explicitly requires a standalone `gate` CLI command that records one
// check's row directly (mirroring destructive/verdict's own record/query
// pairing) — so the freeform recording path cannot simply be removed
// (option (a) in the Destructive's brief is unavailable here). Instead every
// row written OUTSIDE of a real `runGate` run is structurally tagged with
// this reserved runner prefix, so it can never be mistaken for a
// mechanically-verified result the way the Destructive proved it could
// before this fix (five hand-called `recordGateVerdict` rows read back as a
// clean overall PASS, indistinguishable from a real run).
const MANUAL_OVERRIDE_TAG = 'MANUAL-OVERRIDE';

// ── A. Scope determination (AC-1..AC-3) ─────────────────────────────────────

/** AC-1: sprint N's member items, derived from bow_items.sprint, never from
 * a hand-read sprint-plan-v1.md table row. */
async function resolveSprintItems(db, sprintN) {
  const [rows] = await db.query('SELECT * FROM bow_items WHERE sprint = ?', [Number(sprintN)]);
  return rows;
}

/** AC-2: for every OPEN/IN_PROGRESS item in scope, resolve its acceptance
 * file at docs/planning/acceptance/<mkey>.md. A missing file is recorded as
 * an explicit gap, never silently skipped from the report. */
function resolveAcceptanceFiles(items, acceptanceDir = ACCEPTANCE_DIR) {
  const relevant = items.filter((i) => i.status === 'open' || i.status === 'in_progress');
  const resolved = [];
  const missing = [];
  for (const item of relevant) {
    if (!item.mkey) { missing.push({ item, mkey: null, filePath: null, reason: 'item has no mkey set' }); continue; }
    const filePath = path.join(acceptanceDir, `${item.mkey}.md`);
    if (fs.existsSync(filePath)) {
      resolved.push({ item, mkey: item.mkey, filePath, text: fs.readFileSync(filePath, 'utf8') });
    } else {
      missing.push({ item, mkey: item.mkey, filePath });
    }
  }
  return { resolved, missing };
}

/** Extract sprint N's item mkey list straight out of sprint-plan-v1.md's own
 * markdown table row (`| **S<N>** | Name | Items | Exit gate |`), used ONLY
 * for AC-3's drift comparison — never as the scope source itself (AC-1). */
function parseSprintPlanMkeys(sprintPlanText, sprintN) {
  const marker = `**S${sprintN}**`;
  const line = (sprintPlanText || '').split('\n').find((l) => l.trim().startsWith('|') && l.includes(marker));
  if (!line) return [];
  const cells = line.split('|').map((c) => c.trim());
  const itemsCell = cells[3] || '';
  return itemsCell.split(',').map((s) => s.replace(/[✅*]/g, '').trim()).filter(Boolean);
}

/** AC-3: items whose mkey appears in sprint N's sprint-plan-v1.md row but
 * whose bow_items.sprint disagrees (or is NULL) — a drift finding, never
 * silently resolved either way. */
async function findScopeDrift(db, sprintN, sprintPlanMkeys) {
  const findings = [];
  for (const mkey of sprintPlanMkeys) {
    const item = await findItem(db, mkey);
    if (!item) continue; // an unresolvable mkey is FEAT-060 lint's concern, not this AC's
    if (item.sprint === null || Number(item.sprint) !== Number(sprintN)) {
      findings.push(
        `mkey "${mkey}" (${item.code}) appears in sprint-plan-v1.md's S${sprintN} row but bow_items.sprint is ` +
        `${item.sprint === null ? 'NULL' : item.sprint}`
      );
    }
  }
  return findings;
}

// ── B. Check 1 — data files (AC-4..AC-9) ────────────────────────────────────

/** Every "Check:"/"Check (once unblocked):" clause's own text span, up to
 * the next top-level bullet — extraction (AC-4/AC-10) is scoped to these
 * spans only, never the whole file, so an unrelated data-file mention
 * elsewhere in the AC's prose is never mistaken for its own stated check. */
function extractCheckClauseSpans(acText) {
  const spans = [];
  const re = /Check(?: \(once unblocked\))?:/g;
  let m;
  while ((m = re.exec(acText))) {
    const start = m.index;
    const rest = acText.slice(start + 1);
    const nextBullet = rest.search(/\n- \*\*/);
    const end = nextBullet === -1 ? acText.length : start + 1 + nextBullet;
    spans.push(acText.slice(start, end));
  }
  return spans;
}

/** AC-4: every `data/*.json`-shaped path literal inside a Check: clause. */
function extractDataFilePaths(acText) {
  const found = new Set();
  for (const span of extractCheckClauseSpans(acText)) {
    const re = /`?(data\/[\w.\-/]+\.json)`?/g;
    let m;
    while ((m = re.exec(span))) found.add(m[1]);
  }
  return [...found];
}

/** AC-8: recursively flag numeric leaf values that have neither a sibling
 * `provenance.source`+`provenance.sourceType` object nor a sibling
 * `comment`/`$comment` disclosing "placeholder" (data.modes-naming.md's
 * established convention). Detects ABSENCE of a marker only — never quality
 * (AC-8's own false-pass warning; a Destructive agent's concern, not this
 * heuristic's). */
function flagUnmarkedPlaceholders(node, pathPrefix = '') {
  const flags = [];
  if (Array.isArray(node)) {
    node.forEach((v, i) => flags.push(...flagUnmarkedPlaceholders(v, `${pathPrefix}[${i}]`)));
    return flags;
  }
  if (node && typeof node === 'object') {
    const prov = node.provenance;
    const hasProvenance = !!(prov && typeof prov === 'object' &&
      typeof prov.source === 'string' && prov.source.trim() &&
      typeof prov.sourceType === 'string' && prov.sourceType.trim());
    const commentText = (typeof node.comment === 'string' && node.comment) ||
      (typeof node['$comment'] === 'string' && node['$comment']) || '';
    const hasDisclosure = /placeholder/i.test(commentText);
    for (const [k, v] of Object.entries(node)) {
      if (k === 'provenance' || k === 'comment' || k === '$comment') continue;
      const childPath = pathPrefix ? `${pathPrefix}.${k}` : k;
      if (typeof v === 'number') {
        if (!hasProvenance && !hasDisclosure) flags.push(childPath);
      } else {
        flags.push(...flagUnmarkedPlaceholders(v, childPath));
      }
    }
    return flags;
  }
  return flags;
}

/**
 * AC-5/AC-6/AC-7/AC-8: check one referenced data file against one acceptance
 * file's own text. Returns an array of finding objects `{path, status,
 * detail}` where status is 'fail' (missing/empty — hard check-1 FAIL),
 * 'partial' (exists, but schema-check-availability or placeholder-marker
 * absence means "not fully verified" — never silently promoted to pass), or
 * 'pass'.
 *
 * AC-6 note: where the AC names a Go loader test file, this gate SHELLS OUT
 * to `go test` on that file's package (never re-implementing field checks
 * inline, per AC-6's own "what a lazy implementation looks like" warning) via
 * an injectable `runGoTest` (defaults to a real `go test` invocation) so the
 * unit tests below can prove the invocation shape without needing a live Go
 * toolchain in every environment this suite runs in.
 */
function checkDataFileForAcFile(acFile, relPath, opts = {}) {
  const rootDir = opts.rootDir || __dirname;
  const runGoTest = opts.runGoTest || defaultRunGoTest;
  const abs = path.join(rootDir, relPath);

  if (!fs.existsSync(abs)) {
    return { path: relPath, status: 'fail', detail: `${acFile.item.code}: ${relPath} does not exist (FEAT-047/empty-stub precedent — a spec-required data file is missing)` };
  }
  let raw;
  try { raw = fs.readFileSync(abs, 'utf8'); }
  catch (e) { return { path: relPath, status: 'fail', detail: `${acFile.item.code}: ${relPath} unreadable: ${e.message}` }; }

  let json;
  try { json = JSON.parse(raw); }
  catch (e) { return { path: relPath, status: 'fail', detail: `${acFile.item.code}: ${relPath} is not valid JSON: ${e.message}` }; }

  const isTrivialEmpty = (typeof json === 'object' && json !== null && !Array.isArray(json) && Object.keys(json).length === 0) ||
    (Array.isArray(json) && json.length === 0);
  if (isTrivialEmpty) {
    return { path: relPath, status: 'fail', detail: `${acFile.item.code}: ${relPath} is an empty stub ({}/[]) (FEAT-047/empty-stub precedent)` };
  }

  const findings = [];
  const testMatch = acFile.text.match(/[\w./\-]+_test\.go/);
  if (!testMatch) {
    findings.push({ path: relPath, status: 'partial', detail: `${acFile.item.code}: ${relPath} exists and is non-empty — existence-only verified, no schema check named in ${acFile.mkey}.md (AC-7)` });
  } else {
    const testFile = testMatch[0];
    const result = runGoTest(testFile, { rootDir });
    if (!result.ok) {
      findings.push({ path: relPath, status: 'fail', detail: `${acFile.item.code}: ${relPath}'s named loader test (${testFile}) did not pass: ${result.output || ''}`.trim() });
    } else {
      findings.push({ path: relPath, status: 'pass', detail: `${acFile.item.code}: ${relPath} verified via loader test ${testFile}` });
    }
  }

  const flagged = flagUnmarkedPlaceholders(json);
  for (const fp of flagged) {
    findings.push({ path: relPath, status: 'partial', detail: `${acFile.item.code}: ${relPath} field "${fp}" is a bare numeric value with no provenance/placeholder marker (AC-8; approval-vs-disclosure distinction not yet determinable, AC-9)` });
  }
  return findings;
}

/** Real (non-test-injected) AC-6 runner: `go test` on the named test file's
 * package directory. Never invoked by the unit tests below (which inject a
 * fake), only by the live `gate-run` command. */
function defaultRunGoTest(testFile, { rootDir }) {
  const pkgDir = path.dirname(path.join(rootDir, testFile));
  const result = spawnSync('go', ['test', pkgDir], { cwd: rootDir, encoding: 'utf8', timeout: 120000 });
  return { ok: result.status === 0, output: (result.stdout || '') + (result.stderr || '') };
}

/** Runs check 1 across every resolved acceptance file in scope. */
function runCheck1DataFiles(resolvedAcFiles, opts = {}) {
  const findings = [];
  for (const acFile of resolvedAcFiles) {
    for (const relPath of extractDataFilePaths(acFile.text)) {
      const result = checkDataFileForAcFile(acFile, relPath, opts);
      if (Array.isArray(result)) findings.push(...result); else findings.push(result);
    }
  }
  const fails = findings.filter((f) => f.status === 'fail');
  const partials = findings.filter((f) => f.status === 'partial');
  let verdict = 'pass';
  if (fails.length) verdict = 'fail';
  else if (partials.length) verdict = 'partial';
  const detail = findings.length
    ? [...fails, ...partials].map((f) => f.detail).join(' | ') || `${findings.length} data-file check(s) passed clean`
    : 'no in-scope data-file references found';
  return { checkNumber: 1, checkName: 'data-files', verdict, detail, findings };
}

// ── C. Check 2 — call edges (AC-10..AC-12) ──────────────────────────────────

/** AC-10: extract every module-to-module call assertion an in-scope AC's
 * Check: clause names — both the prose form ("a registered `A`->`B` edge")
 * and the `node -e` tripwire one-liner form (BUG-100's own shape). */
function extractCallEdgeAssertions(acText) {
  const edges = [];
  const seen = new Set();
  const add = (source, target) => {
    const key = `${source}|${target}`;
    if (seen.has(key)) return;
    seen.add(key);
    edges.push({ source, target });
  };
  for (const span of extractCheckClauseSpans(acText)) {
    const proseRe = /registered\s+`?([a-z][\w]*\.[\w]+)`?\s*(?:→|->)\s*`?([a-z][\w]*\.[\w]+)`?\s*edge/ig;
    let pm;
    while ((pm = proseRe.exec(span))) add(pm[1], pm[2]);

    const keyRe = /key===['"]([a-z][\w.]+)['"]/g;
    const keys = [];
    let km;
    while ((km = keyRe.exec(span))) keys.push(km[1]);
    if (keys.length >= 2) {
      if (/\.outbound\.calls\.some/.test(span)) add(keys[0], keys[1]);
      else if (/\.inbound\.consumers\.some/.test(span)) add(keys[1], keys[0]);
    }
  }
  return edges;
}

/** AC-11: does codeJson register a real outbound source->target edge? Loaded
 * fresh by the caller every run (never cached) per AC-11's own requirement. */
function edgeExistsInCodeJson(codeJson, source, target) {
  const mod = (codeJson.modules || []).find((m) => m.key === source);
  if (!mod) return false;
  return ((mod.outbound && mod.outbound.calls) || []).some((c) => c.key === target);
}

/** Check 2 — registration-only call-edge verification (AC-10..AC-12): confirms
 * an in-scope AC's asserted (source,target) edge is REGISTERED in a
 * freshly-read code.json, not that real Go code already imports across it.
 * AC-12 escalation: does NOT call FEAT-062's tools/plan/codejson-audit.js
 * runAudit() even though FEAT-062 has shipped and exports it — a logged
 * deferral (ASM-483, cross-referenced on FEAT-061), not a stale assumption
 * that FEAT-062 is unavailable. See the checklist comment above this
 * function and ASM-483 for the full reasoning. */
function runCheck2CallEdges(resolvedAcFiles, opts = {}) {
  const codeJsonPath = opts.codeJsonPath || CODE_JSON_PATH;
  let codeJson;
  try { codeJson = JSON.parse(fs.readFileSync(codeJsonPath, 'utf8')); }
  catch (e) { return { checkNumber: 2, checkName: 'call-edges', verdict: 'fail', detail: `cannot read/parse code.json at ${codeJsonPath}: ${e.message}`, findings: [] }; }

  const results = [];
  for (const acFile of resolvedAcFiles) {
    for (const { source, target } of extractCallEdgeAssertions(acFile.text)) {
      const exists = edgeExistsInCodeJson(codeJson, source, target);
      results.push({ item: acFile.item.code, source, target, exists });
    }
  }
  const missing = results.filter((r) => !r.exists);
  let verdict = 'pass';
  if (missing.length) verdict = 'fail';
  const detail = results.length
    ? missing.length
      ? missing.map((r) => `${r.item}: ${r.source}->${r.target} not registered in code.json (BUG-058 precedent)`).join(' | ')
      : `${results.length} call-edge assertion(s) verified registered`
    : 'no in-scope call-edge assertions found';
  return { checkNumber: 2, checkName: 'call-edges', verdict, detail, findings: results };
}

// ── D. Check 3 — tripwires (AC-13..AC-15) ───────────────────────────────────

/** Split acceptance-file text into top-level `- **AC-N ...` bullet blocks —
 * the "same AC block" unit AC-14 requires the Tripwire label to sit within.
 *
 * BUG-162: the original split point was ONLY a following `- **` bullet, so
 * a later section heading (e.g. `## D. Check 3 — "Check (once unblocked)"
 * tripwires are armed`) with no AC bullet of its own got absorbed whole
 * into the PRECEDING AC's block — right up to the next `- **` line. If that
 * absorbed heading happened to contain the literal phrase "Check (once
 * unblocked)" (as tool.sprintgate.md's own section D heading does, quoting
 * the phrase it's about), `findTripwireChecks` wrongly treated the prior AC
 * (AC-12 there) as a deferred-check AC needing a Tripwire block, found
 * none, and misreported it "unarmed" — a false positive on an AC that was
 * never a "Check (once unblocked)" AC at all. Fixed by also splitting
 * ahead of any markdown heading line (`#` through `######`), so a heading
 * always terminates the previous AC's block; headings are then dropped by
 * the same `startsWith('- **')` filter that already excludes anything
 * that isn't a real AC bullet.
 *
 * BUG-193: that heading regex (`#{1,6}\s`) has no concept of markdown code
 * fences. An AC block containing an unindented fenced code sample
 * (``` or ~~~ starting at column 0) can itself contain a flush-left
 * hash-comment line, e.g. a shell/python comment `# some comment` at
 * column 0. That line also matches `#{1,6}\s` and was being misread as a
 * section-heading boundary, truncating the AC block mid-fence and
 * discarding everything after it (including a Tripwire line further down
 * the same block) — a false "unarmed" report on an AC that really is
 * armed. Fixed by walking the text line-by-line and tracking fence-open
 * state (toggled by a line that, once trimmed, starts with ``` or ~~~):
 * heading/bullet boundaries are only honoured while NOT inside an open
 * fence, so a flush-left `#` line inside a fence can never split the
 * block. This project's own acceptance docs never trigger the bug (BUG-162
 * verified every real fence in the corpus is indented under its bullet),
 * so this is a latent-gap fix, not a live-corpus regression fix.
 *
 * BUG-195: the BUG-193 fix tracked fence state as a bare boolean toggled by
 * ANY line starting with ``` or ~~~, with no notion of WHICH marker opened
 * the fence or how long it was. Two live repros followed: (1) a fence
 * opened with ``` whose BODY contains a literal flush-left `~~~` line
 * (valid content in that context, not a delimiter) flipped the toggle
 * closed early, and the real closing ``` then flipped it back open,
 * leaving it stuck true for the rest of the document; (2) an AC block with
 * a genuinely unclosed fence (opened, never closed) left the toggle stuck
 * true permanently. Both cases silently absorbed every following line —
 * including a whole subsequent real `- **AC-N` block and its Tripwire —
 * into the current block, so the later AC vanished from
 * findTripwireChecks() results entirely (not even a false "unarmed" FAIL).
 *
 * Fixed by tracking real fence state — {char, length} of the SPECIFIC
 * marker that opened the fence — and only closing on a line whose marker
 * is the SAME character and AT LEAST as long, per CommonMark's actual
 * fence-closing rule (a fence closes on a line of the same character with
 * a run at least as long as the opener; a different character, or a
 * shorter run of the same character, does not close it). This fixes mixed
 * markers outright (a `~~~` line inside a ``` fence never matches the
 * open fence's marker, so it can't close it).
 *
 * For the unclosed-fence case: CommonMark itself treats an unclosed fence
 * as implicitly closed at end-of-document, with everything after the
 * opener as fence content — so a scanner absorbing the rest of the
 * document is spec-correct, not a bug in isolation. But this scanner isn't
 * a renderer; it drives findTripwireChecks(), where that absorption
 * silently deletes a later AC/Tripwire from the results with no signal at
 * all. So the fix keeps the spec-correct absorption (an unclosed fence in
 * a malformed AC doc legitimately makes everything after it "inside" that
 * block) but now logs a console.warn when EOF is reached with a fence
 * still open, naming the block, so the condition is at least visible
 * instead of purely silent. */
function splitAcBlocks(acText) {
  const text = acText || '';
  const lines = text.split('\n');
  const blocks = [];
  let current = [];
  let fence = null; // { char: '`'|'~', length: N } while inside an open fence, else null
  const parseFenceMarker = (line) => {
    const m = line.trim().match(/^(`{3,}|~{3,})/);
    return m ? { char: m[1][0], length: m[1].length } : null;
  };
  const isBoundary = (line) => /^(- \*\*|#{1,6}\s)/.test(line);

  for (const line of lines) {
    const startsNewBlock = !fence && isBoundary(line) && current.length > 0;
    if (startsNewBlock) {
      blocks.push(current.join('\n'));
      current = [];
    }
    current.push(line);

    const marker = parseFenceMarker(line);
    if (fence) {
      // Only the SAME character, with a run at least as long as the opener,
      // closes the fence (CommonMark fence-closing rule). A different
      // character (mixed markers) or a shorter run never closes it.
      if (marker && marker.char === fence.char && marker.length >= fence.length) {
        fence = null;
      }
    } else if (marker) {
      fence = marker;
    }
  }
  if (current.length > 0) blocks.push(current.join('\n'));

  if (fence) {
    console.warn(
      `splitAcBlocks: reached end of document with an unclosed ${fence.char.repeat(fence.length)} fence still open — ` +
      'the remainder of the document was treated as fence content (CommonMark-correct), which may hide a later AC block. ' +
      'Check the source acceptance doc for a missing closing fence.'
    );
  }

  return blocks.map((s) => s.trim()).filter((s) => s.startsWith('- **'));
}

/** AC-13/AC-14: for every "Check (once unblocked)" AC block, find its
 * adjacent `Tripwire (mechanical...): \`cmd\` must exit N` block, or record
 * an unarmed FAIL when none exists. */
function findTripwireChecks(acText) {
  const results = [];
  for (const block of splitAcBlocks(acText)) {
    if (!block.includes('Check (once unblocked)')) continue;
    const acNumMatch = block.match(/AC-(\d+)/);
    const acNum = acNumMatch ? acNumMatch[1] : '?';
    const twMatch = block.match(/\*{0,2}Tripwire \(mechanical[^)]*\):\*{0,2}\s*`([^`]+)`\s*must exit\s*\*{0,2}(\d+)\*{0,2}/i);
    if (!twMatch) {
      results.push({ acNum, armed: false });
    } else {
      results.push({ acNum, armed: true, command: twMatch[1], expectedExit: Number(twMatch[2]) });
    }
  }
  return results;
}

/**
 * FIX-1 (P0 RCE remediation, Destructive finding on FEAT-061). The tripwire
 * text extracted from an acceptance file is authored by junior agents — it is
 * NOT trusted input. The pre-fix implementation ran it via
 * `spawnSync(command, {shell:true})`, which hands the raw string to a real
 * shell: a malicious or corrupted tripwire block like
 *   `node -e "process.exit(0)" & node -e "require('fs').writeFileSync(...)"`
 * gets its injected second command executed in full, with the check still
 * reporting PASS. This tokenizer performs NO shell interpretation at all: it
 * walks the string by hand, only ever recognising (a) single/double-quoted
 * spans, extracted verbatim, and (b) unquoted tokens restricted to a
 * conservative safe charset. Any unquoted shell metacharacter (`&`, `;`,
 * `|`, backtick, `$`, `(`, `)`, `<`, `>`, `*`, newline, ...), or an unbalanced
 * quote, or trailing garbage immediately after a closing quote, makes the
 * WHOLE command unparseable and this returns `null` — the caller must then
 * refuse to run anything, not attempt a partial/best-effort execution.
 */
function tokenizeCommandSafely(cmd) {
  const s = String(cmd || '');
  const tokens = [];
  let i = 0;
  while (i < s.length) {
    while (i < s.length && /\s/.test(s[i])) i++;
    if (i >= s.length) break;
    const ch = s[i];
    if (ch === '"' || ch === "'") {
      const quote = ch;
      let j = i + 1;
      let buf = '';
      let closed = false;
      while (j < s.length) {
        if (quote === '"' && s[j] === '\\' && j + 1 < s.length) { buf += s[j + 1]; j += 2; continue; }
        if (s[j] === quote) { closed = true; j++; break; }
        buf += s[j]; j++;
      }
      if (!closed) return null; // unbalanced quote — refuse
      if (j < s.length && !/\s/.test(s[j])) return null; // e.g. "..."foo — ambiguous concatenation, refuse
      tokens.push(buf);
      i = j;
    } else {
      let j = i;
      while (j < s.length && !/\s/.test(s[j])) j++;
      const tok = s.slice(i, j);
      // Conservative unquoted charset: letters, digits, and the handful of
      // characters real flags/paths/module keys need. Nothing shell-special.
      if (!/^[A-Za-z0-9_./=-]+$/.test(tok)) return null;
      tokens.push(tok);
      i = j;
    }
  }
  return tokens;
}

/**
 * FIX-2 (round-2 Destructive remediation, Destructive finding on FEAT-061).
 * Round 1 closed the shell-injection RCE by removing `shell:true` and
 * tokenizing by hand (FIX-1 above). But the `node -e "<code>"` allowlist
 * entry it introduced validated only the OUTER shape — 3 tokens, `node`,
 * `-e`, a quoted string — with ZERO validation of what `<code>` actually
 * does. `spawnSync('node', ['-e', code])` runs a real Node process with full
 * privileges, so the round-2 Destructive proved `code` can be
 * `require('child_process').execSync('<any shell command>', {shell:true})`
 * or `require('fs').writeFileSync(...)` — reintroducing round 1's exact RCE,
 * one layer down, fully proven by actual execution.
 *
 * The only fix that actually closes this is to never hand `<code>` to a JS
 * engine at all. Auditing every real tripwire in this project's acceptance
 * files (`docs/planning/acceptance/*.md`, grep for "Tripwire (mechanical")
 * shows every single one follows one narrow structural template:
 *
 *   const m=require('./code.json').modules.find(x=>x.key==='<MODULE_KEY>');
 *   process.exit(<BOOLEXPR>?<0|1>:<0|1>)
 *
 * where <BOOLEXPR> is exactly one of:
 *   m.outbound.calls.some(c=>c.key==='<T1>'[||c.key==='<T2>'...])
 *   (m.inbound.consumers||[]).some(c=>c.key==='<T1>'[||c.key==='<T2>'...])
 *   (m.inbound.consumers||[]).length===0
 *
 * `parseCodeJsonEdgeTripwire` recognises ONLY this exact shape via anchored
 * regexes — never a generic JS grammar, never `eval`/`new Function`/`vm`,
 * never any form of dynamic code execution — and extracts the module key,
 * direction, target key(s), and exit-code polarity as plain string literals.
 * `evaluateCodeJsonEdgeTripwire` then answers the check directly in THIS
 * process using a real `require('./code.json')` (safe: reading the
 * project's own data file, never executing attacker-authored code) and
 * plain array `.some()`/`.length` comparisons against the extracted
 * literals. If the text does not match the template exactly, this returns
 * `null` and the caller refuses to run anything — no partial/best-effort
 * execution of any kind, and (critically) NO CHILD PROCESS IS EVER SPAWNED
 * for this shape, closing the hole for good rather than sandboxing it.
 *
 * (ASM logged: two real acceptance-file constructs exist today that do NOT
 * fit this template — `feat.disasters.md`'s AC-7 two-module cross-check
 * `require('./code.json'); ...find(...)+find(...); (m...||svc...)` and
 * `engine.wellbeing.md`'s `.every(k=>m.outbound.calls.some(c=>c.key===k))`
 * array-driven form. Neither currently reaches this code path — neither
 * sits inside a "Tripwire (mechanical...): `cmd` must exit N" block that
 * `findTripwireChecks` actually extracts as armed (feat.disasters.md's AC-7
 * block has no adjacent "Check (once unblocked)" text; engine.wellbeing's
 * two are narrative "exits 0" prose, not "Tripwire (mechanical)"-labelled
 * blocks) — but flagged for Bill in case that changes, rather than widening
 * this parser unilaterally to cover shapes not yet proven safe to admit.)
 */
const NODE_E_OUTER_RE = /^const m=require\('\.\/code\.json'\)\.modules\.find\(x=>x\.key===('[a-z][\w.]*')\);\s*process\.exit\((.+)\)$/;
const TERNARY_RE = /^(.+)\?([01]):([01])$/;
const OUTBOUND_SOME_RE = /^m\.outbound\.calls\.some\(c=>(.+)\)$/;
const INBOUND_SOME_RE = /^\(m\.inbound\.consumers\|\|\[\]\)\.some\(c=>(.+)\)$/;
const INBOUND_LEN0_RE = /^\(m\.inbound\.consumers\|\|\[\]\)\.length===0$/;

/** Verifies `str` is EXACTLY one-or-more `c.key==='X'` chunks joined by
 * `||`, with nothing else present anywhere — extra/foreign characters (a
 * stray function call, a template literal, anything) make this return null,
 * which the caller treats as "does not match the template." Rebuilding the
 * expected string from the extracted literals and comparing it byte-for-byte
 * against the original is what makes this a structural match rather than a
 * permissive "contains the substring" check. */
function parseKeyCondLiteral(str) {
  const keys = [];
  const re = /c\.key===('[a-z][\w.]*')/g;
  let m;
  while ((m = re.exec(str))) keys.push(m[1].slice(1, -1));
  if (keys.length === 0) return null;
  const rebuilt = keys.map((k) => `c.key==='${k}'`).join('||');
  if (rebuilt !== str) return null;
  return keys;
}

/** Parses a `node -e "<code>"` tripwire's `<code>` into its semantic
 * parameters, or returns `null` if it does not match the narrow code.json
 * edge-check template EXACTLY. Pure string/regex work — `<code>` is never
 * executed, interpreted, or passed to `eval`/`new Function`/`vm` at any
 * point. */
function parseCodeJsonEdgeTripwire(code) {
  const outer = NODE_E_OUTER_RE.exec(String(code || ''));
  if (!outer) return null;
  const moduleKey = outer[1].slice(1, -1);
  const tern = TERNARY_RE.exec(outer[2]);
  if (!tern) return null;
  const boolExpr = tern[1];
  const trueExit = Number(tern[2]);
  const falseExit = Number(tern[3]);

  let direction;
  let targetKeys;
  let m;
  if ((m = OUTBOUND_SOME_RE.exec(boolExpr))) {
    targetKeys = parseKeyCondLiteral(m[1]);
    if (!targetKeys) return null;
    direction = 'outbound';
  } else if ((m = INBOUND_SOME_RE.exec(boolExpr))) {
    targetKeys = parseKeyCondLiteral(m[1]);
    if (!targetKeys) return null;
    direction = 'inbound';
  } else if (INBOUND_LEN0_RE.test(boolExpr)) {
    direction = 'inbound-length-zero';
    targetKeys = [];
  } else {
    return null;
  }
  return { moduleKey, direction, targetKeys, trueExit, falseExit };
}

/** Evaluates a `parseCodeJsonEdgeTripwire` result directly against a real
 * code.json — no child process, ever, for this shape. Reuses Check 2's
 * `edgeExistsInCodeJson` lookup for the outbound-single/OR case per GR#3
 * (single source of truth for "does this module call that key"), rather
 * than re-implementing the module lookup a second time. */
function evaluateCodeJsonEdgeTripwire(parsed, codeJsonPath) {
  let codeJson;
  try { codeJson = JSON.parse(fs.readFileSync(codeJsonPath, 'utf8')); }
  catch (e) { return { ok: false, status: null, reason: `cannot read/parse code.json at ${codeJsonPath}: ${e.message}` }; }

  let matched;
  if (parsed.direction === 'outbound') {
    matched = parsed.targetKeys.some((t) => edgeExistsInCodeJson(codeJson, parsed.moduleKey, t));
  } else {
    const mod = (codeJson.modules || []).find((mm) => mm.key === parsed.moduleKey);
    const consumers = (mod && mod.inbound && mod.inbound.consumers) || [];
    matched = parsed.direction === 'inbound-length-zero'
      ? consumers.length === 0
      : consumers.some((c) => parsed.targetKeys.includes(c.key));
  }
  return { ok: true, status: matched ? parsed.trueExit : parsed.falseExit };
}

/** FIX-2 P1 (round-2 Destructive finding on the `grep` shape): the outer
 * argv-array/no-shell execution was already safe from injection, but the
 * path arguments were unconstrained — any charset-legal path, including a
 * `../../..` traversal, was accepted and handed straight to `grep`, letting
 * a malicious/corrupted tripwire read arbitrary files outside the project's
 * own acceptance-file / source trees. Every path argument is now resolved
 * to an absolute path (relative to `cwd`) and required to stay under one of
 * the allowed roots — `docs/planning/acceptance/`, `internal/`, or `data/`
 * — rejecting any `../` traversal that escapes them. */
const ALLOWED_GREP_ROOT_NAMES = ['docs/planning/acceptance', 'internal', 'data'];

/** Shared path-scope primitive (GR#3 -- reused by both this tripwire grep
 * guard, FIX-2, and SEC-050's --desc-file/--note-file/--detail-file guard
 * near resolveTextFlag above, rather than each maintaining its own copy).
 * Resolves `pathArg` to an absolute path against `cwd` and returns true iff
 * it falls under (or equals) ANY of the given absolute `roots`. */
function isPathUnderAnyRoot(cwd, pathArg, roots) {
  const resolved = path.resolve(cwd, pathArg);
  return roots.some((rootDir) => {
    const root = path.resolve(rootDir);
    return resolved === root || resolved.startsWith(root + path.sep);
  });
}

function isPathUnderAllowedGrepRoot(cwd, pathArg) {
  return isPathUnderAnyRoot(cwd, pathArg, ALLOWED_GREP_ROOT_NAMES.map((rootName) => path.resolve(cwd, rootName)));
}

/**
 * FIX-1 (round-1 RCE remediation, retained) + FIX-2 (round-2 remediation,
 * this fix): given the (never-shell-interpreted) argv tokens, match against
 * the small allowlist of known-safe tripwire shapes this project actually
 * uses. Returns `{ ok:true, kind:'node-eval', parsed }` for a recognised
 * code.json edge-check (to be evaluated directly, no spawn), `{ ok:true,
 * kind:'grep-exec', file, args }` for a path-validated grep (executed via a
 * no-shell argv spawn), or `{ ok:false, reason }` naming exactly why the
 * shape was rejected — never a silent fallback to "try running it anyway."
 */
function matchTripwireShape(tokens, rawCommand, cwd) {
  if (!tokens || tokens.length === 0) {
    return { ok: false, reason: `command could not be safely parsed (unquoted shell metacharacters, unbalanced quotes, or empty) in "${rawCommand}" — refusing to execute` };
  }
  if (tokens.length === 3 && tokens[0] === 'node' && tokens[1] === '-e') {
    const parsed = parseCodeJsonEdgeTripwire(tokens[2]);
    if (!parsed) {
      return {
        ok: false,
        reason: `unrecognized tripwire shape — only the code.json edge-check template is supported (a "node -e" body must be exactly ` +
          `"const m=require('./code.json').modules.find(x=>x.key==='<key>'); process.exit(<outbound/inbound edge check>?<0|1>:<0|1>)") — refusing to execute arbitrary code, got: "${tokens[2]}"`,
      };
    }
    return { ok: true, kind: 'node-eval', parsed };
  }
  if (tokens[0] === 'grep' && tokens.length >= 3 && /^-[A-Za-z]+$/.test(tokens[1])) {
    const args = tokens.slice(1);
    const pathArgs = args.slice(2); // args[0]=flags, args[1]=pattern, args[2..]=paths
    for (const p of pathArgs) {
      if (!isPathUnderAllowedGrepRoot(cwd || __dirname, p)) {
        return {
          ok: false,
          reason: `grep path argument "${p}" resolves outside the allowed roots (${ALLOWED_GREP_ROOT_NAMES.join(', ')}) — refusing to execute (path-traversal guard, FIX-2)`,
        };
      }
    }
    return { ok: true, kind: 'grep-exec', file: 'grep', args };
  }
  return {
    ok: false,
    reason: `unrecognized/unsafe tripwire command shape "${rawCommand}" — only "node -e \\"<code>\\"" (the code.json edge-check template) and "grep -<flags> \\"<pattern>\\" <path...>" (paths constrained to docs/planning/acceptance/, internal/, data/) are allowlisted, cannot execute`,
  };
}

/** AC-15: actually run a tripwire command — real spawn (grep) or direct
 * in-process evaluation (the code.json edge-check template) by default,
 * injectable for tests. Never uses a shell and never interpolates the raw
 * string: the command is tokenized (never-shell-interpreted) and matched
 * against a strict allowlist (FIX-1/FIX-2); the recognised code.json
 * edge-check template is answered directly in this process with NO CHILD
 * PROCESS spawned at all (FIX-2); only the grep shape ever spawns, via
 * `spawnSync(file, argsArray)` with `shell` left at its default `false` and
 * its path arguments root-constrained, so there is no shell metacharacter
 * interpretation and no arbitrary-code-execution path at any point. */
function defaultRunTripwire(command, cwd) {
  const effectiveCwd = cwd || __dirname;
  const tokens = tokenizeCommandSafely(command);
  const match = matchTripwireShape(tokens, command, effectiveCwd);
  if (!match.ok) return { ok: false, status: null, reason: match.reason };
  if (match.kind === 'node-eval') {
    return evaluateCodeJsonEdgeTripwire(match.parsed, path.join(effectiveCwd, 'code.json'));
  }
  let result;
  try {
    result = spawnSync(match.file, match.args, { cwd: effectiveCwd, encoding: 'utf8', timeout: 20000 });
  } catch (err) {
    return { ok: false, status: null, reason: `spawnSync threw for "${match.file}": ${err.message}` };
  }
  if (result.error) return { ok: false, status: null, reason: `failed to spawn "${match.file}": ${result.error.message}` };
  return { ok: true, status: result.status };
}

function runCheck3Tripwires(resolvedAcFiles, opts = {}) {
  const cwd = opts.cwd || __dirname;
  const runTripwire = opts.runTripwire || defaultRunTripwire;
  const perAc = [];
  for (const acFile of resolvedAcFiles) {
    for (const tw of findTripwireChecks(acFile.text)) {
      if (!tw.armed) {
        perAc.push({ item: acFile.item.code, acNum: tw.acNum, ok: false, reason: `"Check (once unblocked)" found with no adjacent Tripwire block — unarmed` });
        continue;
      }
      const live = runTripwire(tw.command, cwd);
      if (!live.ok) {
        perAc.push({ item: acFile.item.code, acNum: tw.acNum, ok: false, reason: `tripwire not executed — ${live.reason}` });
        continue;
      }
      if (live.status === tw.expectedExit) {
        perAc.push({ item: acFile.item.code, acNum: tw.acNum, ok: true });
      } else {
        perAc.push({ item: acFile.item.code, acNum: tw.acNum, ok: false, reason: `live exit ${live.status} != documented expected ${tw.expectedExit} — blocker cleared, re-arm before dispatch` });
      }
    }
  }
  const failing = perAc.filter((r) => !r.ok);
  const verdict = perAc.length === 0 ? 'pass' : failing.length ? 'fail' : 'pass';
  const detail = perAc.length
    ? failing.length
      ? failing.map((r) => `${r.item} AC-${r.acNum}: ${r.reason}`).join(' | ')
      : `${perAc.length} tripwire(s) armed and confirmed still blocked`
    : 'no "Check (once unblocked)" ACs in scope';
  return { checkNumber: 3, checkName: 'tripwires', verdict, detail, findings: perAc };
}

// ── E. Check 4 — boundary rulings (AC-16..AC-19, report-only) ──────────────

const BOUNDARY_TAG_RE = /\[boundary ruling:\s*([a-z][\w.]*)\s*<->\s*([a-z][\w.]*)\]/i;
const MKEY_RE = /\b[a-z]+\.[a-z][a-z0-9]*\b/g;

/** AC-16: comments opening with the `[boundary ruling: A <-> B]` marker. */
function findConfirmedBoundaryRulings(comments) {
  const out = [];
  for (const c of comments) {
    const m = (c.body || '').match(BOUNDARY_TAG_RE);
    if (m) out.push({ moduleA: m[1], moduleB: m[2], code: c.item_code || null, text: c.body, confirmed: true });
  }
  return out;
}

/** AC-17: retroactive heuristic fallback — "boundary"/"owns"/"ownership"
 * near two distinct mkeys, flagged as a CANDIDATE, never merged silently
 * into AC-16's confirmed list. */
function findCandidateBoundaryRulings(comments) {
  const out = [];
  for (const c of comments) {
    const body = c.body || '';
    if (!/\bboundary\b/i.test(body) && !/\bowns\b|\bownership\b/i.test(body)) continue;
    const mkeys = [...new Set(body.match(MKEY_RE) || [])];
    if (mkeys.length >= 2) {
      out.push({ moduleA: mkeys[0], moduleB: mkeys[1], code: c.item_code || null, text: body, confirmed: false });
    }
  }
  return out;
}

/** AC-18: for a ruling naming modules A/B, do BOTH acceptance files cite it
 * (by exact text or by the ruling's own BOW code)? Returns null if cited on
 * both sides, else a finding string naming the missing side(s). */
function crossCiteFinding(ruling, acceptanceDir = ACCEPTANCE_DIR) {
  const read = (mkey) => {
    try { return fs.readFileSync(path.join(acceptanceDir, `${mkey}.md`), 'utf8'); }
    catch { return null; }
  };
  const cites = (text) => text != null && (text.includes(ruling.text) || (ruling.code && text.includes(ruling.code)));
  const aOk = cites(read(ruling.moduleA));
  const bOk = cites(read(ruling.moduleB));
  if (aOk && bOk) return null;
  const missing = [];
  if (!aOk) missing.push(ruling.moduleA);
  if (!bOk) missing.push(ruling.moduleB);
  return `ruling ${ruling.code || '(untagged)'} (${ruling.moduleA} <-> ${ruling.moduleB}) missing citation in: ${missing.join(', ')} (ASM-247 precedent)`;
}

/** AC-19: report-only — the returned verdict is 'pass' (clean) or 'partial'
 * (findings present) but this check is EXCLUDED from overall derivation
 * (GATING_CHECK_NUMBERS), and the detail text says so explicitly. */
function runCheck4BoundaryRulings(comments, opts = {}) {
  const acceptanceDir = opts.acceptanceDir || ACCEPTANCE_DIR;
  const rulings = [...findConfirmedBoundaryRulings(comments), ...findCandidateBoundaryRulings(comments)];
  const findings = [];
  for (const ruling of rulings) {
    const f = crossCiteFinding(ruling, acceptanceDir);
    if (f) findings.push(`${ruling.confirmed ? 'confirmed' : 'CANDIDATE'}: ${f}`);
  }
  const verdict = findings.length ? 'partial' : 'pass';
  const detail = (findings.length ? findings.join(' | ') : `${rulings.length} boundary ruling(s) found, all cross-cited`) +
    ' — report-only per AC-19, does NOT gate the overall verdict';
  return { checkNumber: 4, checkName: 'boundary-rulings', verdict, detail, findings };
}

// ── F. Check 5 — ready-queue truthfulness / FEAT-060 (AC-20..AC-22) ────────

/** Re-resolved live, every call — never cached (AC-22). Skips gracefully
 * (never a hard FAIL) while FEAT-060 does not yet exist/ship done (AC-20);
 * once it is done, runs the lint directly (function reuse, not a shell-out —
 * GR#3) and requires zero findings (AC-21). */
async function runCheck5ReadyQueue(db) {
  const feat060 = await findItem(db, 'FEAT-060');
  if (!feat060 || feat060.status !== 'done') {
    return { checkNumber: 5, checkName: 'ready-queue', verdict: 'skipped', detail: 'FEAT-060 not yet available, skipped', findings: [] };
  }
  const [items] = await db.query('SELECT guid, code, description, status FROM bow_items');
  const [comments] = await db.query('SELECT item_guid, body FROM bow_comments');
  const [depRows] = await db.query('SELECT item_guid, depends_on_guid FROM bow_dependencies');
  const deps = new Set(depRows.map((d) => `${d.item_guid}|${d.depends_on_guid}`));
  const { class1, class2, class3 } = runLint(items, comments, deps);
  const total = class1.length + class2.length + class3.length;
  if (total === 0) {
    return { checkNumber: 5, checkName: 'ready-queue', verdict: 'pass', detail: 'FEAT-060 lint clean: 0 findings', findings: [] };
  }
  return {
    checkNumber: 5, checkName: 'ready-queue', verdict: 'fail',
    detail: `FEAT-060 lint not clean: ${total} finding(s) (class1=${class1.length}, class2=${class2.length}, class3=${class3.length}) — a lying ready-queue is the BUG-012 class`,
    findings: [...class1, ...class2, ...class3],
  };
}

// ── G. Verdict recording (AC-23..AC-28) ─────────────────────────────────────

/** AC-24: record ONE check's row. Validates fully before the INSERT so a bad
 * value can never produce a partial write (mirrors recordDestructiveVerdict's
 * own all-or-nothing discipline). `opts.gateRunGuid` groups multiple calls
 * into one run (used by runGate below); omitted, a fresh single-row run is
 * started — a deliberate extension beyond the literal CLI usage string,
 * needed so `gate-run`'s five internal writes share one gate_run_guid. */
async function recordGateVerdict(db, opts = {}) {
  const sprint = Number(opts.sprint);
  if (!Number.isInteger(sprint)) throw new Error('--sprint (a sprint number) is required and must be an integer');

  const checkNumber = Number(opts.checkNumber);
  if (!Number.isInteger(checkNumber) || checkNumber < 1 || checkNumber > 5) {
    throw new Error('--check must be an integer 1-5');
  }

  const checkName = String(opts.checkName || '').trim();
  if (!GATE_CHECK_NAMES.includes(checkName)) {
    throw new Error(`--name must be one of ${GATE_CHECK_NAMES.join(', ')} (got "${opts.checkName}")`);
  }

  const verdict = String(opts.verdict || '').trim().toLowerCase();
  if (!GATE_VERDICTS.includes(verdict)) {
    throw new Error(`--verdict must be one of ${GATE_VERDICTS.join(', ')} (got "${opts.verdict}")`);
  }

  let runner = String(opts.runner == null ? '' : opts.runner).trim();
  if (!runner) throw new Error('--runner is required and must be non-empty — an unnamed runner is not an audit trail');

  // FIX-2: `opts.mechanical` is set ONLY by runGate's own internal five
  // writes (never by the CLI, never by a caller reaching this function any
  // other way) — everything else, including the standalone `gate` CLI
  // command AC-24 requires, is a manual/freeform recording and gets tagged
  // as such, unconditionally, before the INSERT. This cannot be spoofed via
  // --runner because the tag is applied here, not trusted from the caller.
  if (!opts.mechanical && !runner.startsWith(`${MANUAL_OVERRIDE_TAG}:`)) {
    runner = `${MANUAL_OVERRIDE_TAG}:${runner}`;
  }

  const gateRunGuid = opts.gateRunGuid || crypto.randomUUID();
  const guid = crypto.randomUUID();
  await db.query(
    `INSERT INTO bow_gate_verdicts (guid, gate_run_guid, sprint, check_number, check_name, verdict, runner, detail)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
    [guid, gateRunGuid, sprint, checkNumber, checkName, verdict, runner, opts.detail || null]
  );
  return { guid, gateRunGuid, sprint, checkNumber, checkName, verdict, runner };
}

/** FIX-2: does this gate run's row set contain any manually-tagged rows? Used
 * by gate-status to surface "NOT a mechanically-verified result" loudly
 * rather than folding a manual row silently into a clean-looking PASS. */
function hasManualOverrideRows(rows) {
  return (rows || []).some((r) => String(r.runner || '').startsWith(`${MANUAL_OVERRIDE_TAG}:`));
}

/** AC-27: the latest gate run's 5 rows for a sprint (greatest created_at,
 * `id` as a deterministic tiebreaker — matches latestDestructiveVerdict's own
 * tiebreak). Returns null if no run has ever been recorded for this sprint. */
async function latestGateRun(db, sprintN) {
  const [head] = await db.query(
    `SELECT gate_run_guid FROM bow_gate_verdicts WHERE sprint = ? ORDER BY created_at DESC, id DESC LIMIT 1`,
    [Number(sprintN)]
  );
  if (!head.length) return null;
  const gateRunGuid = head[0].gate_run_guid;
  const [rows] = await db.query(
    `SELECT * FROM bow_gate_verdicts WHERE sprint = ? AND gate_run_guid = ? ORDER BY check_number ASC`,
    [Number(sprintN), gateRunGuid]
  );
  return { gateRunGuid, rows };
}

/** AC-26: overall verdict, derived fresh every call from the 5 rows — never
 * stored. FAIL if any GATING_CHECK_NUMBERS row is 'fail'; PARTIAL if none
 * fail but at least one is 'partial'/'skipped' (or a gating row is simply
 * missing — an incomplete run is never silently PASS); PASS only if every
 * gating row is 'pass'. */
function deriveOverallVerdict(rows) {
  const byCheck = new Map(rows.map((r) => [Number(r.check_number), r.verdict]));
  const gating = GATING_CHECK_NUMBERS.map((n) => byCheck.get(n));
  if (gating.some((v) => v === 'fail')) return 'FAIL';
  if (gating.some((v) => v == null || v === 'partial' || v === 'skipped')) return 'PARTIAL';
  return 'PASS';
}

/**
 * The automated 5-check gate run (AC-25's atomicity requirement): resolves
 * scope, runs all 5 checks, and writes exactly 5 rows sharing one
 * gate_run_guid — or, if the runner crashes mid-check, still writes 5 rows
 * with the unreached checks marked failed/crashed (never a silent partial
 * set of rows presented as if it were a complete run).
 */
async function runGate(db, sprintN, opts = {}) {
  const runner = opts.runner || currentAuthor() || 'unknown';
  const acceptanceDir = opts.acceptanceDir || ACCEPTANCE_DIR;
  const sprintPlanPath = opts.sprintPlanPath || SPRINT_PLAN_PATH;
  const codeJsonPath = opts.codeJsonPath || CODE_JSON_PATH;
  const rootDir = opts.rootDir || __dirname;
  const gateRunGuid = crypto.randomUUID();

  let results = [];
  let scopeNote = '';
  try {
    const items = await resolveSprintItems(db, sprintN);
    const { resolved, missing } = resolveAcceptanceFiles(items, acceptanceDir);
    let sprintPlanText = '';
    try { sprintPlanText = fs.readFileSync(sprintPlanPath, 'utf8'); } catch { /* optional — AC-3 drift just can't run without it */ }
    const planMkeys = parseSprintPlanMkeys(sprintPlanText, sprintN);
    const drift = await findScopeDrift(db, sprintN, planMkeys);
    if (missing.length || drift.length) {
      scopeNote = [
        missing.length ? `${missing.length} sprint-${sprintN} item(s) with no acceptance file: ${missing.map((m) => `${m.item.code}${m.mkey ? ` (${m.mkey})` : ''}`).join(', ')}` : '',
        drift.length ? `scope drift: ${drift.join('; ')}` : '',
      ].filter(Boolean).join(' | ') + ' | ';
    }

    const [allComments] = await db.query(
      `SELECT c.item_guid, c.body, i.code AS item_code FROM bow_comments c JOIN bow_items i ON i.guid = c.item_guid`
    );

    results = [
      runCheck1DataFiles(resolved, { rootDir }),
      runCheck2CallEdges(resolved, { codeJsonPath }),
      runCheck3Tripwires(resolved, { cwd: rootDir }),
      runCheck4BoundaryRulings(allComments, { acceptanceDir }),
      await runCheck5ReadyQueue(db),
    ];
  } catch (err) {
    // AC-25: a crash mid-run must not leave a silent partial row set. Fill in
    // every check not yet computed as an explicit crashed FAIL.
    const have = new Set(results.map((r) => r.checkNumber));
    for (let n = 1; n <= 5; n++) {
      if (!have.has(n)) {
        results.push({ checkNumber: n, checkName: GATE_CHECK_NAMES[n - 1], verdict: 'fail', detail: `gate runner crashed before this check completed: ${err.message}`, findings: [] });
      }
    }
    results.sort((a, b) => a.checkNumber - b.checkNumber);
  }

  // FIX-3 (P0 crash-mid-write remediation, Destructive finding on FEAT-061).
  // The pre-fix insertion loop sat OUTSIDE the try/catch above, so a failure
  // DURING the DB write (3rd INSERT throwing — a real deadlock/connection
  // drop) left a silent partial row set (e.g. 2 of 5 rows) with no signal
  // that the run never completed. This loop is now wrapped the same way: if
  // an INSERT throws partway through, every check_number not yet
  // successfully written gets a crashed-marker row backfilled (mirroring the
  // computation-crash backfill above) so the "exactly 5 rows per
  // gate_run_guid, always" invariant holds whether the failure happens
  // during check logic OR during the write itself.
  const rows = [];
  try {
    for (const r of results) {
      const detail = r.checkNumber === 1 && scopeNote ? scopeNote + r.detail : r.detail;
      rows.push(await recordGateVerdict(db, {
        sprint: sprintN, checkNumber: r.checkNumber, checkName: r.checkName,
        verdict: r.verdict, runner, detail, gateRunGuid, mechanical: true,
      }));
    }
  } catch (err) {
    const written = new Set(rows.map((r) => r.checkNumber));
    for (let i = 0; i < results.length; i++) {
      const r = results[i];
      if (written.has(r.checkNumber)) continue;
      const crashed = { ...r, verdict: 'fail', detail: `gate runner crashed while writing this check's verdict: ${err.message}` };
      results[i] = crashed;
      try {
        rows.push(await recordGateVerdict(db, {
          sprint: sprintN, checkNumber: crashed.checkNumber, checkName: crashed.checkName,
          verdict: crashed.verdict, runner, detail: crashed.detail, gateRunGuid, mechanical: true,
        }));
      } catch (err2) {
        // Cannot force a row into existence if the DB is still down — surface
        // this loudly (never pretend the backfill succeeded) rather than
        // leaving a silent gap the caller has no way to detect.
        console.error(`gate-run: FAILED to backfill crashed-marker row for check ${crashed.checkNumber} (sprint ${sprintN}, run ${gateRunGuid}): ${err2.message}`);
      }
    }
  }
  return { gateRunGuid, results, overall: deriveOverallVerdict(results.map((r) => ({ check_number: r.checkNumber, verdict: r.verdict }))) };
}

async function cmdGate(db) {
  const sprint = positional[0];
  if (sprint == null || !flags.check || !flags.name || !flags.verdict || !flags.runner) {
    console.error('Usage: node claude-bow.js gate <sprint#> --check <1-5> --name <data-files|call-edges|tripwires|boundary-rulings|ready-queue> --verdict pass|fail|partial|skipped --runner "<name>" [--detail "..." | --detail-file <path>] [--run <gate-run-guid>]');
    process.exit(1);
  }
  try {
    const result = await recordGateVerdict(db, {
      sprint, checkNumber: flags.check, checkName: flags.name, verdict: flags.verdict,
      runner: flags.runner, detail: resolveTextFlag('detail'), gateRunGuid: flags.run, // BUG-090 (AC-2/AC-3)
    });
    console.log(`Recorded gate check ${result.checkNumber} (${result.checkName}) = ${result.verdict.toUpperCase()} for sprint ${result.sprint} [run ${result.gateRunGuid}].`);
  } catch (err) {
    console.error(`claude-bow gate: ${err.message}`);
    process.exit(1);
  }
}

async function cmdGateStatus(db) {
  const sprint = positional[0];
  if (sprint == null) {
    console.error('Usage: node claude-bow.js gate-status <sprint#>');
    process.exit(1);
  }
  const run = await latestGateRun(db, sprint);
  if (!run) {
    console.log(`Sprint ${sprint}: NO GATE VERDICTS RECORDED. Per AC-28, treat this identically to overall FAIL — dispatch must not proceed.`);
    return;
  }
  console.log(`Sprint ${sprint} — latest gate run ${run.gateRunGuid}:\n`);
  for (const row of run.rows) {
    const manualTag = String(row.runner || '').startsWith(`${MANUAL_OVERRIDE_TAG}:`) ? '  [MANUAL-OVERRIDE — NOT mechanically verified]' : '';
    console.log(`  check ${row.check_number} (${row.check_name}): ${row.verdict.toUpperCase()}${manualTag}  [${row.runner}, ${ts(row.created_at)}]`);
    if (row.detail) console.log(`    ${row.detail}`);
  }
  console.log(`\nOverall (derived, checks 1/2/3/5; check 4 is advisory): ${deriveOverallVerdict(run.rows)}`);
  // FIX-2: a manually-recorded row set must never read the same as a real
  // mechanical run — surface this loudly and separately from the derived
  // overall line above, which per AC-26 stays a pure function of the verdict
  // values alone.
  if (hasManualOverrideRows(run.rows)) {
    console.log(`\n*** WARNING: this run contains MANUAL-OVERRIDE row(s) — recorded via the standalone \`gate\` command, NOT by a real \`gate-run\`. This is NOT a mechanically-verified result; treat the overall verdict above as unverified until a real \`gate-run\` is executed. ***`);
  }
}

async function cmdGateRun(db) {
  const sprint = positional[0];
  if (sprint == null) {
    console.error('Usage: node claude-bow.js gate-run <sprint#> [--runner "<name>"]');
    process.exit(1);
  }
  const result = await runGate(db, sprint, { runner: flags.runner });
  console.log(`Gate run ${result.gateRunGuid} for sprint ${sprint} complete. Overall: ${result.overall}\n`);
  for (const r of result.results) {
    console.log(`  check ${r.checkNumber} (${r.checkName}): ${r.verdict.toUpperCase()}`);
    if (r.detail) console.log(`    ${r.detail}`);
  }
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
  // BUG-132: `flags.mkey` truthiness collapses "--mkey '' " (explicit clear
  // request) into the same case as "--mkey never passed" (leave untouched),
  // since '' is falsy in JS. The CLI parser (VALUE_FLAGS loop above) DOES
  // still record the key on `flags` when an empty string is supplied — only
  // the value is empty, not the presence — so `'mkey' in flags` is the
  // correct presence test, distinct from `flags.mkey` truthiness. An explicit
  // empty value clears the column to NULL; a non-empty value sets it; the key
  // being absent entirely (never passed on the command line) leaves the
  // column untouched, same as before. Same pattern applied to seq/sprint,
  // the other nullable columns `set` exposes an explicit-clear path for.
  // BUG-168: a dangling flag (last token on the command line, no value
  // following) is a malformed invocation, NOT a request to clear the
  // column — that would silently alias "forgot to type the value" onto
  // "clear this field", which is exactly the data-loss regression BUG-132's
  // presence check introduced. Reject before the '' vs value branch below.
  if (danglingFlags.has('mkey')) { console.error('claude-bow: --mkey requires a value (use --mkey \'\' to clear, or omit --mkey entirely to leave it unchanged).'); process.exit(1); }
  if (danglingFlags.has('seq')) { console.error('claude-bow: --seq requires a value (use --seq \'\' to clear, or omit --seq entirely to leave it unchanged).'); process.exit(1); }
  if (danglingFlags.has('sprint')) { console.error('claude-bow: --sprint requires a value (use --sprint \'\' to clear, or omit --sprint entirely to leave it unchanged).'); process.exit(1); }
  // BUG-196: `desc`/`desc-file` got NO dangling-flag guard when BUG-017
  // wired --desc into `set`, unlike mkey/seq/sprint above (BUG-168/171).
  // A dangling `--desc` (nothing after it, or the next token is itself a
  // recognized `--flag`) fell straight through resolveTextFlag's `direct ||
  // null` fallback and silently wiped the description to NULL with exit 0.
  // Same pattern, same fix: reject before ever reaching resolveTextFlag.
  if (danglingFlags.has('desc')) { console.error('claude-bow: --desc requires a value (a dangling --desc was not applied). Description is free-form prose, not a short categorical field like --mkey, so an explicit empty clear is NOT supported here -- see the --desc \'\' rejection below if you meant that.'); process.exit(1); }
  if (danglingFlags.has('desc-file')) { console.error('claude-bow: --desc-file requires a path (a dangling --desc-file was not applied).'); process.exit(1); }
  // BUG-196 (empty-string decision): BUG-132 made `--mkey ''` a legitimate,
  // documented "explicit clear" for short categorical columns. Description
  // is different -- it IS the record (the substantive prose content), not a
  // lookup key, so a genuinely-intentional "clear the description to
  // nothing" is not a normal editing action, and indistinguishable in
  // behaviour from the BUG-171 dangling-flag shape (`--desc --priority P1`)
  // where '' was never typed at all. Unlike mkey/seq/sprint, `--desc ''` is
  // therefore REJECTED outright rather than treated as a supported clear —
  // safer default for a free-text field where accidental data loss is far
  // more costly than for a short key. (If a deliberate clear is ever truly
  // needed, that should be a separate explicit affordance, not silent `''`.)
  if (flags.desc === '') { console.error('claude-bow: --desc \'\' (empty) is rejected -- description is free-form prose, not a short key, so clearing it to nothing is not supported as an implicit side effect of an empty value (this is almost always a mistake, e.g. a dangling flag or an accidentally-empty quoted string). Nothing was written.'); process.exit(1); }
  if ('mkey' in flags) {
    if (flags.mkey === '') { updates.push('mkey = NULL'); }
    else { updates.push('mkey = ?'); params.push(flags.mkey); }
  }
  if ('seq' in flags) {
    if (flags.seq === '') { updates.push('seq = NULL'); }
    else { updates.push('seq = ?'); params.push(Number(flags.seq)); }
  }
  if ('sprint' in flags) {
    if (flags.sprint === '') { updates.push('sprint = NULL'); }
    else { updates.push('sprint = ?'); params.push(Number(flags.sprint)); }
  }
  if (flags.milestone) { updates.push('milestone = ?'); params.push(flags.milestone); }
  if (flags.layer) { updates.push('layer = ?'); params.push(flags.layer); }
  if (flags.spec) { updates.push('spec_ref = ?'); params.push(flags.spec); }
  if (flags['guid-in']) { updates.push('guid_in = ?'); params.push(flags['guid-in']); }
  if (flags['guid-out']) { updates.push('guid_out = ?'); params.push(flags['guid-out']); }
  if (flags.estimate != null) { updates.push('estimate_days = ?'); params.push(Number(flags.estimate)); }
  if (flags['code-path']) { updates.push('code_path = ?'); params.push(String(flags['code-path'])); }
  if (flags.codejson) { updates.push('codejson_ref = ?'); params.push(String(flags.codejson)); }
  // BUG-017: `set` previously had NO repair path for a corrupted
  // description (BUG-090's own class -- an unescaped `$(...)`/backtick in
  // an inline --desc value gets expanded by the OUTER shell before this
  // process ever sees the argument, splicing unrelated shell output into
  // the stored description). Ports the exact `--desc "..." | --desc-file
  // <path>` safe-input pattern `add` already uses (resolveTextFlag,
  // BUG-090/SEC-050): mutual-exclusion check, path-scope guard on
  // --desc-file, and the same advisory (non-blocking) shell-injection
  // warning on a risky direct --desc value. `description` is TEXT (no
  // VARCHAR limit -- see BOW_COLUMN_MAX_LEN's comment above), so no
  // rejectIfOverColumnLimit call is needed here, matching `add`'s existing
  // behaviour for the same column.
  if ('desc' in flags || 'desc-file' in flags) {
    const desc = resolveTextFlag('desc');
    updates.push('description = ?'); params.push(desc);
  }
  if (!updates.length) { console.error('Usage: node claude-bow.js set <code> [--priority P0..P3] [--status ...] [--mkey K] [--seq N] [--milestone M1] [--layer L] [--spec "§n"] [--guid-in G] [--guid-out G] [--estimate D] [--code-path P] [--codejson K] [--desc "..." | --desc-file <path>]'); process.exit(1); }
  params.push(item.guid);
  await db.query(`UPDATE bow_items SET ${updates.join(', ')} WHERE guid = ?`, params);
  console.log(`${item.code} updated${flags.priority ? ` priority=${String(flags.priority).toUpperCase()}` : ''}${flags.status ? ` status=${String(flags.status).toLowerCase()}` : ''}.`);
}

// ── redact (BUG-061, GR#22) ──────────────────────────────────────────────────
//
// Aaron's binding design constraints (BOW BUG-061, 2026-08-11 ruling): (1) the
// forbidden text never transits the command line or any audit trail — no
// --match/--text flag anywhere, detection is self-contained; (2) auditable,
// never silent — an auto-comment records pattern-class + count + field only;
// (3) the original (pre-redaction) text is stored nowhere — no backup column,
// no log line, no stdout echo; (4) --comment <id> is also accepted, since
// bow_comments is prose-bearing too.
//
// GR#3 (single source of truth): the pattern set itself is NOT re-derived
// here. It is required straight from claude-codename-guard.js, which builds
// it from string fragments at runtime specifically so no forbidden literal
// ever sits whole in source (see that file's header). This module only adds
// the REPLACE half (the guard only detects/denies); the matching semantics
// (global regex per pattern, boundary-aware zero-width-safe scan for
// `boundary: true` patterns) are ported verbatim from the guard's own
// lineMatchesWithBoundary so a hit here is the same hit the guard would have
// blocked at commit time.
// BUG-151/BUG-173/BUG-027 (GR#3 -- one source of truth for the real column
// sizes, not three re-hardcoded copies of "255" and "512"): mirrors the
// VARCHAR sizes declared in ensureSchema() above (bow_items.title
// VARCHAR(255), bow_items.closed_note VARCHAR(512)). `description` and
// `bow_comments.body` are TEXT/effectively unbounded, so they are
// intentionally absent -- no check needed there.
const BOW_COLUMN_MAX_LEN = { title: 255, closed_note: 512 };
// Back-compat alias for redact's field-keyed lookup below.
const REDACT_FIELD_MAX_LEN = { title: BOW_COLUMN_MAX_LEN.title };

function loadCodenameGuardPatterns() {
  const guard = require('./claude-codename-guard.js');
  if (!guard || !Array.isArray(guard.PATTERNS) || typeof guard.isLowerLetter !== 'function') {
    console.error('claude-bow: claude-codename-guard.js did not export PATTERNS/isLowerLetter — refusing to redact without the guard\'s canonical pattern set (GR#3: no second, hand-derived copy of the forbidden-pattern list).');
    process.exit(1);
  }
  return guard;
}

/**
 * Replace every occurrence of every codename-guard pattern in `text` with the
 * literal marker [REDACTED-GR22]. Returns { text, hits } where hits is
 * [{ what, count }] — `what` is the guard's own safe, non-identifying
 * pattern-class description (e.g. "the distinctive single word from the
 * reference title"), never the matched substring itself. Nothing about the
 * original matched text is retained anywhere in the return value.
 */
function redactText(text, guardExports) {
  const { PATTERNS, isLowerLetter } = guardExports;
  let working = text;
  const hits = [];
  for (const p of PATTERNS) {
    p.re.lastIndex = 0;
    let count = 0;
    let result = '';
    let lastIndex = 0;
    let m;
    while ((m = p.re.exec(working))) {
      let real = true;
      if (p.boundary) {
        const before = m.index > 0 ? working[m.index - 1] : undefined;
        const after = m.index + m[0].length < working.length ? working[m.index + m[0].length] : undefined;
        real = !isLowerLetter(before) || !isLowerLetter(after);
      }
      if (real) {
        result += working.slice(lastIndex, m.index) + '[REDACTED-GR22]';
        lastIndex = m.index + m[0].length;
        count++;
      }
      if (p.re.lastIndex === m.index) p.re.lastIndex += 1; // zero-length-match guard
    }
    result += working.slice(lastIndex);
    if (count > 0) {
      working = result;
      hits.push({ what: p.what, count });
    }
  }
  return { text: working, hits };
}

async function cmdRedact(db) {
  const guardExports = loadCodenameGuardPatterns();
  const isComment = flags.comment !== undefined;

  let subject;       // human-readable label for output/audit comment, safe to print
  let auditItemGuid; // which item's comment thread carries the audit trail
  let fieldSpecs;     // [{ name, value }]
  let applyUpdate;    // async (newValuesByFieldName) => void

  if (isComment) {
    const commentId = Number(flags.comment);
    if (!Number.isFinite(commentId)) {
      console.error('Usage: node claude-bow.js redact --comment <id>');
      process.exit(1);
    }
    const [rows] = await db.query('SELECT * FROM bow_comments WHERE id = ?', [commentId]);
    if (!rows.length) {
      console.error(`claude-bow: no comment with id ${commentId}`);
      process.exit(1);
    }
    const comment = rows[0];
    subject = `comment #${commentId}`;
    auditItemGuid = comment.item_guid;
    fieldSpecs = [{ name: 'body', value: comment.body }];
    applyUpdate = async (newValues) => {
      await db.query('UPDATE bow_comments SET body = ? WHERE id = ?', [newValues.body, commentId]);
    };
  } else {
    const item = await requireItem(db, positional[0]);
    subject = item.code;
    auditItemGuid = item.guid;
    fieldSpecs = [
      { name: 'title', value: item.title },
      { name: 'description', value: item.description },
    ];
    applyUpdate = async (newValues) => {
      const sets = [];
      const params = [];
      if (newValues.title !== undefined) { sets.push('title = ?'); params.push(newValues.title); }
      if (newValues.description !== undefined) { sets.push('description = ?'); params.push(newValues.description); }
      params.push(item.guid);
      await db.query(`UPDATE bow_items SET ${sets.join(', ')} WHERE guid = ?`, params);
    };
  }

  const newValues = {};
  // Aggregated by field+pattern-class so multiple distinct PATTERNS entries
  // sharing the same safe `what` description (e.g. the several
  // former-expansion-pack-name patterns) report as one summed count rather
  // than one line per pattern — "the count" per Aaron's ruling means the
  // occurrence count per class, not the internal pattern-table row count.
  const reportMap = new Map(); // key `${field} ${what}` -> { field, what, count }
  let totalCount = 0;
  for (const f of fieldSpecs) {
    if (!f.value) continue;
    const { text, hits } = redactText(f.value, guardExports);
    if (!hits.length) continue;
    newValues[f.name] = text;
    for (const h of hits) {
      const key = `${f.name} ${h.what}`;
      const existing = reportMap.get(key);
      if (existing) existing.count += h.count;
      else reportMap.set(key, { field: f.name, what: h.what, count: h.count });
      totalCount += h.count;
    }
  }
  const report = [...reportMap.values()];

  if (!report.length) {
    console.log(`redact: no forbidden-pattern occurrences found in ${subject} — nothing changed.`);
    return;
  }

  // BUG-151: check the redacted result against each field's actual column
  // limit BEFORE writing. The [REDACTED-GR22] marker (16 chars) can be
  // longer than the pattern it replaces, so a title already near the 255
  // cap can overflow on redact. If it would, refuse the write entirely —
  // no partial write, no silent truncation — and say explicitly that the
  // GR#22 violation is STILL PRESENT (the field is unmodified, since we
  // never attempted the write), so an operator can't mistake this failure
  // for a successful redaction. Never names the matched/redacted text
  // itself (same discipline as the audit comment below).
  for (const [field, newText] of Object.entries(newValues)) {
    const maxLen = REDACT_FIELD_MAX_LEN[field];
    if (maxLen !== undefined && newText.length > maxLen) {
      console.error(
        `claude-bow error: GR#22 redact BLOCKED for ${subject}, field "${field}": ` +
        `redacting would grow this field to ${newText.length} chars, exceeding the ` +
        `${maxLen}-char column limit. The write was NOT attempted — the GR#22 ` +
        `violation in this field is STILL PRESENT and was NOT removed. Nothing was ` +
        `changed in the database. Manually shorten the field (e.g. trim unrelated ` +
        `text) and re-run redact, or edit out the violation by hand.`);
      process.exit(1);
    }
  }

  // Write the redacted text back via the same DB write paths the rest of
  // this file already uses (no raw ad-hoc SQL bypassing the query patterns
  // above) — the pre-redaction text is discarded here, not stored anywhere.
  await applyUpdate(newValues);

  // Auto-comment: pattern-class + count + field ONLY (Aaron's constraint 2/3
  // — never the redacted text, never the pre-image). `r.what` is always one
  // of the guard's own fixed, non-identifying descriptions.
  const summary = report.map((r) => `${r.field}: ${r.count} occurrence(s) of "${r.what}"`).join('; ');
  await db.query(
    'INSERT INTO bow_comments (item_guid, author, body) VALUES (?, ?, ?)',
    [auditItemGuid, currentAuthor(),
     `GR#22 redaction (BUG-061) applied to ${subject}${isComment ? ' (comment body)' : ''}: ${summary}. ` +
     'Original text stored nowhere; occurrences replaced with [REDACTED-GR22].']);

  console.log(`redact: ${subject} — redacted ${totalCount} occurrence(s):`);
  for (const r of report) console.log(`  ${r.field}: ${r.count} x "${r.what}"`);
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
  const note = resolveTextFlag('note'); // BUG-090 (AC-2/AC-3)
  // BUG-173: validate closed_note length BEFORE the UPDATE, same mechanism
  // as BUG-027's title check and BUG-151's redact check (GR#3) -- a clear,
  // typed message naming the limit instead of a raw "Data too long for
  // column 'closed_note'" driver error.
  rejectIfOverColumnLimit(note, BOW_COLUMN_MAX_LEN.closed_note, 'closed_note', `closing note for ${item.code}`);
  await db.query(
    'UPDATE bow_items SET status = ?, closed_at = CURRENT_TIMESTAMP, closed_note = ? WHERE guid = ?',
    ['done', note, item.guid]);
  console.log(`${item.code} marked DONE${note ? ' — ' + note : ''}${openDeps.length ? ' (--force: open deps overridden)' : ''}.`);
}

/**
 * Ready-to-build: open items with no open dependencies, in sprint+seq order.
 * The metro equivalent of the spec's v_ready_to_build view (M0-ENG §4) —
 * "order of work: always from ready, priority then milestone order" (§6.2).
 */
async function cmdReady(db) {
  const [items] = await db.query(
    `SELECT i.* FROM bow_items i
     WHERE i.status IN ('open','in_progress') AND NOT EXISTS (
       SELECT 1 FROM bow_dependencies d
       JOIN bow_items di ON di.guid = d.depends_on_guid
       WHERE d.item_guid = i.guid AND di.status IN ('open','in_progress','blocked'))
     ORDER BY ISNULL(sprint), sprint, ISNULL(seq), seq, priority, code`);
  if (!items.length) { console.log('Nothing is ready to build — check `list --status blocked` for what is stuck.'); return; }
  let currentSprint;
  for (const it of items) {
    const s = it.sprint != null ? `Sprint ${it.sprint}` : 'Unscheduled';
    if (s !== currentSprint) { currentSprint = s; console.log(`\n${s}:`); }
    console.log(`  ${String(it.seq ?? '-').padStart(4)} ${it.code.padEnd(9)} ${it.priority} [${it.status.toUpperCase().padEnd(11)}] ${it.title}`);
  }
  console.log(`\n${items.length} item(s) ready — no open dependencies.`);
}

/**
 * Bulk import from a generated plan file (tools/plan/bow-import.json).
 * Idempotent: items are upserted by mkey — existing items keep their code, guid
 * and status; planning fields (title/desc/seq/priority/milestone/layer/spec/
 * guid_in/guid_out) are refreshed. Dependencies are re-asserted (REPLACE).
 * File shape: { items: [{ mkey, type, title, desc, seq, priority, milestone,
 *   layer, specRef, guid, guidIn, guidOut, deps: [mkey...] }] }
 */
async function cmdImport(db) {
  const file = positional[0];
  if (!file) { console.error('Usage: node claude-bow.js import <plan-file.json> [--dry-run]'); process.exit(1); }
  let plan;
  try { plan = JSON.parse(fs.readFileSync(file, 'utf8')); }
  catch (err) { console.error(`claude-bow: cannot read plan file: ${err.message}`); process.exit(1); }
  const items = Array.isArray(plan.items) ? plan.items : null;
  if (!items || !items.length) { console.error('claude-bow: plan file has no items[].'); process.exit(1); }

  // ── Validate before touching the database (all-or-nothing) ──
  const errors = [];
  const byKey = new Map();
  const seqSeen = new Map();
  for (const it of items) {
    const where = `item "${it.mkey || it.title || '?'}"`;
    if (!it.mkey || !/^[a-z0-9][a-z0-9._-]*$/i.test(it.mkey)) errors.push(`${where}: missing/invalid mkey`);
    else if (byKey.has(it.mkey)) errors.push(`${where}: duplicate mkey`);
    else byKey.set(it.mkey, it);
    if (!TYPES.includes(it.type)) errors.push(`${where}: invalid type "${it.type}"`);
    if (!it.title) errors.push(`${where}: missing title`);
    if (it.priority && !PRIORITIES.includes(it.priority)) errors.push(`${where}: invalid priority "${it.priority}"`);
    if (it.seq != null) {
      if (!Number.isInteger(it.seq)) errors.push(`${where}: seq must be an integer`);
      else if (seqSeen.has(it.seq)) errors.push(`${where}: duplicate seq ${it.seq} (also ${seqSeen.get(it.seq)})`);
      else seqSeen.set(it.seq, it.mkey);
    }
  }
  // Dependency targets must exist in the file or already in the DB.
  const [dbKeyRows] = await db.query('SELECT mkey FROM bow_items WHERE mkey IS NOT NULL');
  const dbKeys = new Set(dbKeyRows.map(r => r.mkey));
  for (const it of items) {
    for (const dep of it.deps || []) {
      if (dep === it.mkey) errors.push(`item "${it.mkey}": depends on itself`);
      if (!byKey.has(dep) && !dbKeys.has(dep)) errors.push(`item "${it.mkey}": unknown dependency "${dep}"`);
    }
  }
  // Cycle check over in-file edges (Kahn).
  const indeg = new Map([...byKey.keys()].map(k => [k, 0]));
  for (const it of items) for (const dep of it.deps || []) if (byKey.has(dep)) indeg.set(it.mkey, indeg.get(it.mkey) + 1);
  const queue = [...indeg.entries()].filter(([, d]) => d === 0).map(([k]) => k);
  let visited = 0;
  while (queue.length) {
    const k = queue.pop(); visited++;
    for (const it of items) {
      if ((it.deps || []).includes(k) && byKey.has(it.mkey)) {
        indeg.set(it.mkey, indeg.get(it.mkey) - 1);
        if (indeg.get(it.mkey) === 0) queue.push(it.mkey);
      }
    }
  }
  if (visited < byKey.size) errors.push(`dependency cycle detected among: ${[...indeg.entries()].filter(([, d]) => d > 0).map(([k]) => k).join(', ')}`);

  if (errors.length) {
    console.error(`claude-bow import: ${errors.length} validation error(s) — nothing imported:`);
    for (const e of errors) console.error(`  - ${e}`);
    process.exit(1);
  }
  if (flags['dry-run']) { console.log(`Dry run OK: ${items.length} items validate clean.`); return; }

  // ── Pass 1: upsert items (in seq order so codes roughly follow the sequence) ──
  let added = 0, updated = 0;
  const sorted = [...items].sort((a, b) => (a.seq ?? 1e9) - (b.seq ?? 1e9));
  for (const it of sorted) {
    const [rows] = await db.query('SELECT guid FROM bow_items WHERE mkey = ?', [it.mkey]);
    if (rows.length) {
      await db.query(
        `UPDATE bow_items SET title = ?, description = ?, seq = ?, sprint = ?, priority = ?, milestone = ?,
           layer = ?, spec_ref = ?, guid_in = ?, guid_out = ? WHERE mkey = ?`,
        [it.title, it.desc || null, it.seq ?? null, it.sprint ?? null, it.priority || 'P2', it.milestone || null,
         it.layer || null, it.specRef || null, it.guidIn || null, it.guidOut || null, it.mkey]);
      updated++;
    } else {
      const guid = it.guid || crypto.randomUUID();
      const code = await nextCode(db, it.type);
      await db.query(
        `INSERT INTO bow_items (guid, code, mkey, seq, sprint, item_type, title, description, priority,
           milestone, layer, spec_ref, guid_in, guid_out)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
        [guid, code, it.mkey, it.seq ?? null, it.sprint ?? null, it.type, it.title, it.desc || null, it.priority || 'P2',
         it.milestone || null, it.layer || null, it.specRef || null, it.guidIn || null, it.guidOut || null]);
      added++;
    }
  }

  // ── Pass 2: dependencies (validated acyclic above) ──
  let depCount = 0;
  for (const it of items) {
    if (!(it.deps || []).length) continue;
    const [me] = await db.query('SELECT guid FROM bow_items WHERE mkey = ?', [it.mkey]);
    for (const dep of it.deps) {
      const [target] = await db.query('SELECT guid FROM bow_items WHERE mkey = ?', [dep]);
      if (!me.length || !target.length) continue; // validated above; belt-and-braces
      await db.query(
        'REPLACE INTO bow_dependencies (item_guid, depends_on_guid, note) VALUES (?, ?, ?)',
        [me[0].guid, target[0].guid, 'requires (master plan)']);
      depCount++;
    }
  }
  console.log(`Import complete: ${added} added, ${updated} updated, ${depCount} dependency link(s) asserted.`);
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

/** Git sync state: branch, dirty files, ahead/behind origin (with a quick fetch).
 *
 * @FIX (SEC-004): git-derived values (notably the current branch name) MUST
 *   NEVER be re-interpolated into a second shell command string — a branch
 *   name is attacker-influenceable (fork/remote checkout) the moment we
 *   aren't the only ones choosing it, and git-check-ref-format permits many
 *   shell-metacharacter-adjacent characters. This helper now always invokes
 *   `git` via spawnSync with an argv ARRAY (shell:false, the default), so
 *   every argument — including `origin/${branch}...HEAD` — is passed as one
 *   literal argv element and never re-parsed by a shell, on Windows or POSIX.
 *   Same pattern already used by claude-secret-guard.js / claude-plan-guard.js.
 */
function printGitCheck() {
  const git = (args, timeout = 5000) => {
    const result = spawnSync('git', args, {
      cwd: __dirname,
      encoding: 'utf8',
      timeout,
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    if (result.error) throw result.error;
    if (result.status !== 0) {
      throw new Error((result.stderr || result.stdout || `git ${args.join(' ')} failed`).trim());
    }
    return (result.stdout || '').trim();
  };
  try {
    const branch = git(['rev-parse', '--abbrev-ref', 'HEAD']);
    let fetched = true;
    try { git(['fetch', '--quiet', 'origin'], 15000); } catch { fetched = false; }
    const dirty = git(['status', '--porcelain']).split('\n').filter(Boolean).length;
    let ahead = null, behind = null;
    try {
      const lr = git(['rev-list', '--left-right', '--count', `origin/${branch}...HEAD`]).split(/\s+/);
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

// Canonical item lookup, exported for reuse by the tool.bow (MOD-007) hooks
// (claude-bow-ref-check.js, claude-bow-autoref.js) so they never re-derive
// this WHERE clause themselves (BUG-003: a bespoke reimplementation drifted
// from this exact matching behaviour — case-sensitive mkey, no guid branch).
// Pure extraction: findItem's body is unchanged, just given a stable exported
// name. Callers own their own `db` connection/handle.
const findItemByRef = findItem;

module.exports = {
  printStartupSummary, printBowSummary, SUMMARY_MARKER, findItemByRef,
  recordDestructiveVerdict, latestDestructiveVerdict,
  // BUG-075: batch existence check exported for direct unit testing in
  // addition to the required real-subprocess CLI tests.
  cmdExists,
  // FEAT-060 (tool.bowlint): exported for direct unit testing against
  // fixture rows, per the acceptance file's AC-12..16 scratch-DB standard.
  // connect/ensureSchema/TYPE_PREFIX are exported so the test suite can
  // stand up a scratch database via the exact same mechanism (METRO_DB_*
  // env vars) and schema this file itself uses — no second, reimplemented
  // connection/schema path for tests to drift from (GR#3).
  runLint, extractCodeTokens, splitSentences, findGatingReferences,
  codeTokenRegex, GATING_PHRASES, cmdLint, connect, ensureSchema, TYPE_PREFIX,
  // FEAT-061 (tool.sprintgate): exported for direct unit testing against
  // fixture rows/files, per the acceptance file's own scratch-DB + fixture-
  // file standard (same reasoning as the FEAT-060 exports above, GR#3).
  resolveSprintItems, resolveAcceptanceFiles, parseSprintPlanMkeys, findScopeDrift,
  extractCheckClauseSpans, extractDataFilePaths, flagUnmarkedPlaceholders,
  checkDataFileForAcFile, runCheck1DataFiles,
  extractCallEdgeAssertions, edgeExistsInCodeJson, runCheck2CallEdges,
  splitAcBlocks, findTripwireChecks, runCheck3Tripwires,
  // FIX-1 (RCE remediation): exported for direct unit testing of the
  // tokenizer/allowlist against malicious and legitimate fixtures.
  tokenizeCommandSafely, matchTripwireShape, defaultRunTripwire,
  // FIX-2 (round-2 RCE remediation): exported for direct unit testing of the
  // narrow code.json edge-check parser/evaluator and the grep path guard.
  parseCodeJsonEdgeTripwire, evaluateCodeJsonEdgeTripwire, isPathUnderAllowedGrepRoot,
  findConfirmedBoundaryRulings, findCandidateBoundaryRulings, crossCiteFinding, runCheck4BoundaryRulings,
  runCheck5ReadyQueue,
  recordGateVerdict, latestGateRun, deriveOverallVerdict, runGate,
  // FIX-2 (verdict-gaming remediation): exported so tests can assert the
  // manual-override tag/detection directly, not just via string-matching CLI
  // stdout.
  MANUAL_OVERRIDE_TAG, hasManualOverrideRows,
  GATE_CHECK_NAMES, GATE_VERDICTS, GATING_CHECK_NUMBERS,
  // BUG-090 (tool.bowcli): exported so the regression suite can unit-test
  // the mutual-exclusion/advisory-warning behaviour directly, in addition to
  // the required real-subprocess byte-identity checks (AC-1).
  resolveTextFlag, RISKY_CONTENT_RE,
  // SEC-050 + BUG-151/BUG-173/BUG-027: exported for direct unit testing of
  // the shared path-scope and overflow-check helpers, in addition to the
  // required real-subprocess CLI tests.
  isPathUnderAnyRoot, isTextFlagPathAllowed, TEXT_FLAG_ALLOWED_ROOTS,
  rejectIfOverColumnLimit, BOW_COLUMN_MAX_LEN,
  // BUG-061 (tool.bow `redact`): exported for direct unit testing of the
  // replace/count logic against fixture text, in addition to the required
  // real-subprocess contract tests (Aaron's ruling: verify the way Tester-2
  // verified the guard — real stdin/argv, not just internal functions).
  redactText, loadCodenameGuardPatterns, cmdRedact,
  // BUG-115 (tool.planning): exported so the regression suite can assert the
  // exact read-only/write classification directly, in addition to the
  // required real-subprocess query-count proof.
  READ_ONLY_COMMANDS,
};

// ── Entry ─────────────────────────────────────────────────────────────────────

if (require.main === module) {
  (async () => {
    const db = await connect();
    try {
      // BUG-115: only pay ensureSchema()'s metadata-lock cost for commands
      // that actually write. See READ_ONLY_COMMANDS above for the exact
      // enumerated set and the reasoning for excluding init/startup-summary.
      if (!READ_ONLY_COMMANDS.has(command)) await ensureSchema(db);
      const runDispatch = async () => {
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
          case 'redact': await cmdRedact(db); break;
          case 'done': await cmdDone(db); break;
          case 'import': await cmdImport(db); break;
          case 'ready': await cmdReady(db); break;
          case 'summary': await printBowSummary(db); break;
          case 'weakness': await cmdWeakness(db); break;
          case 'lint': await cmdLint(db); break;
          case 'startup-summary': await printStartupSummary(db); break;
          case 'destructive': await cmdDestructive(db); break;
          case 'verdict': await cmdVerdict(db); break;
          case 'exists': await cmdExists(db); break;
          case 'gate': await cmdGate(db); break;
          case 'gate-status': await cmdGateStatus(db); break;
          case 'gate-run': await cmdGateRun(db); break;
          default:
            console.error(`Unknown command: ${command}`);
            console.error('Commands: init, add, list, show, comment, depend, undepend, ref, set, redact, done, import, summary, startup-summary, weakness, lint, destructive, verdict, exists, gate, gate-status, gate-run');
            process.exit(1);
        }
      };
      try {
        await runDispatch();
      } catch (err) {
        // BUG-170: BUG-115's fix skips ensureSchema() for READ_ONLY_COMMANDS,
        // which is correct for the steady-state (already-migrated) case this
        // file's `metro` database is in practice always in — but leaves a
        // genuinely fresh/never-migrated scratch DB (no tables at all) with
        // no bootstrap path when a READ command happens to be the first one
        // run against it, which is a very natural first action (a fresh
        // clone, a new test env). Before this fix that silently worked
        // because ensureSchema ran unconditionally on every command; restore
        // that behaviour for read commands specifically, but ONLY as a
        // one-shot fallback triggered by the exact missing-table error
        // (errno 1146 / ER_NO_SUCH_TABLE) rather than unconditionally, so the
        // steady-state case (BUG-115's own DDL-counter tests) still executes
        // zero DDL statements per read command. Re-throw anything else
        // (including a second ER_NO_SUCH_TABLE after the retry, which would
        // mean ensureSchema itself failed to create the table it needs) so
        // it surfaces as a real error rather than looping.
        if (READ_ONLY_COMMANDS.has(command) && err && err.code === 'ER_NO_SUCH_TABLE') {
          await ensureSchema(db);
          await runDispatch();
        } else {
          throw err;
        }
      }
    } catch (err) {
      console.error(`claude-bow error: ${err.message}`);
      process.exit(1);
    } finally {
      await db.end().catch(() => {});
    }
  })();
}
