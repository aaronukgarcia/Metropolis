/**
 * claude-devfeedback-import.js — FEAT-065 (feat.devmode) DoD #3 companion
 * script: file-drop feedback -> real BOW item.
 *
 * Spec: docs/planning/acceptance/feat.devmode.md AC-DM10/AC-DM11 (the
 * mechanism this file implements) and AC-DM17 (GR#1 error trapping on every
 * new boundary this file introduces).
 *
 * Architecture (feat.devmode.md's own "Architecture decision" section,
 * summarized here so a future edit doesn't drift from why this shape was
 * chosen): the running game (internal/engine/debug's gated
 * State.SubmitFeedback, feedback.go) never talks to the BOW/MariaDB stack
 * directly — no exec.Command, no HTTP call, no DB driver compiled into the
 * game binary. It only writes one JSON record per submission to a
 * well-known inbox directory (data/devfeedback/inbox/, gitignored). THIS
 * script is the separate, already-Node-side process that turns that data
 * into an instruction: it polls the inbox, and for each well-formed record
 * calls `claude-bow.js add bug` via spawnSync with an argv ARRAY
 * (shell:false, the project-wide convention — see claude-pre-push-check.js/
 * claude-version-checker.js for the same pattern) so a feedback body
 * containing a backtick, `$(...)`, an embedded quote, or a newline can
 * never reach shell-interpreted argument parsing.
 *
 * BUG-090 discipline (load-bearing, do not "simplify" this away): every
 * `claude-bow.js add` invocation below passes the record's own file path as
 * `--desc-file`, NEVER an inline `--desc` built from the record's free-text
 * body. feat.devmode.md's own DoD #3 text explicitly sanctions passing
 * "the record itself" as the desc-file — the record is already well-formed
 * JSON on disk, so no separate desc file needs to be fabricated. A future
 * edit that inlines the body into a `--desc` string reopens exactly the
 * class of defect BUG-090 was filed for.
 *
 * On success: the source record is moved to data/devfeedback/processed/
 * (never deleted — auditability, GR#1) and any stale `.error` sidecar from
 * a prior failed attempt on the same file is removed.
 *
 * On failure (malformed record, or the `claude-bow.js add` invocation
 * itself fails/exits non-zero): the record is left in inbox/ untouched and
 * a `.error` sidecar is written next to it naming the failure — a
 * submission is never silently lost. Re-running the script re-attempts
 * anything still sitting in inbox/ (a stale `.error` sidecar does not
 * suppress a retry): a malformed record fails validation identically every
 * time (so it never reaches the BOW call, and AC-DM11's "no duplicate BOW
 * items" holds trivially for that case); a transient `claude-bow.js`
 * failure (e.g. the metro DB briefly unreachable) gets a real chance to
 * self-heal on the next run instead of requiring a human to notice and
 * manually re-trigger it.
 *
 * KNOWN LIMITATION (documented rather than solved under this dispatch's
 * time budget — flagged in the FEAT-065 report): if `claude-bow.js add`
 * succeeds (a real BOW item now exists) but the subsequent
 * rename-into-processed/ fails (e.g. a permissions change mid-run), the
 * source record stays in inbox/ and a NEXT run would call `claude-bow.js
 * add` again for the same content, producing a duplicate BOW item. This
 * script marks that specific failure mode distinctly in its `.error`
 * sidecar ("MANUAL CLEANUP REQUIRED, do not resubmit without checking the
 * BOW for a duplicate first") so a human notices before it can repeat
 * silently, but it does not (yet) prevent the duplicate mechanically. A
 * true fix needs either a two-phase commit (mark-then-move) or querying
 * the BOW for an existing item with this correlationId before calling add
 * — out of scope for this dispatch, escalate if this edge case matters in
 * practice (a rename failing immediately after a successful adjacent write
 * is rare).
 *
 * Usage: node claude-devfeedback-import.js
 * Exits 0 even if individual records were malformed/failed (those are
 * reported, not fatal to the run) — exits 1 only on an unexpected crash
 * in the script itself (e.g. the inbox directory exists but is
 * unreadable for a reason other than "doesn't exist yet").
 */

'use strict';

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { spawnSync } = require('child_process');

const ROOT = __dirname;
const DEFAULT_INBOX_DIR = path.join(ROOT, 'data', 'devfeedback', 'inbox');
const DEFAULT_PROCESSED_DIR = path.join(ROOT, 'data', 'devfeedback', 'processed');
const DEFAULT_BOW_SCRIPT = path.join(ROOT, 'claude-bow.js');

// Must match internal/engine/debug/feedback.go's FeedbackSchemaVersion
// constant (GR#3: one source of truth for the schema's shape — this
// script does not own the schema, it only validates against it).
const SCHEMA_VERSION = 1;

// The devmode module key every imported item is tagged with
// (docs/planning/acceptance/feat.devmode.md AC-DM10). No per-screen
// "active screen" field exists on FeedbackRecord today (feedback.go's
// schema does not carry one — out of DoD #3's own minimum-fields list),
// so --code-path names this script's fixed origin rather than inventing
// an untracked field; see the FEAT-065 dispatch report for this call.
const DEFAULT_CODE_PATH = 'internal/ui/screens/devmode/ (feat.devmode dev console feedback submission)';

// ASM-477 (Bill's ruling): FeedbackRecord (internal/engine/debug/feedback.go)
// now carries an optional `sourceMkey` field naming the code.json key of
// the tool that actually produced a record — e.g. "feat.metricsdash" for
// internal/harness/metricsdash's LogNote. This script is the SAME
// importer for every writer sharing the inbox (GR#3: parametrize, do not
// fork it into a per-writer copy) — it derives --codejson/--code-path
// PER RECORD from this field instead of hardcoding feat.devmode for
// everything. A record with no sourceMkey (or an empty one) is either an
// older record written before this field existed, or a genuine
// feat.devmode submission (this package's own writer never sets the
// field to anything else) — both fall back to DEFAULT_SOURCE_MKEY /
// DEFAULT_CODE_PATH, preserving FEAT-065's original behavior exactly.
const DEFAULT_SOURCE_MKEY = 'feat.devmode';

// Known source mkey -> --code-path override. A source mkey NOT listed
// here (a future writer this file has never heard of) still attributes
// correctly via a generic derived code-path rather than silently
// collapsing back to feat.devmode/DEFAULT_CODE_PATH — see
// deriveAttribution below. This map only exists to give the two writers
// this project has today a nicer, file-path-specific --code-path than
// the generic fallback would produce.
const SOURCE_CODE_PATHS = {
  'feat.metricsdash': 'internal/harness/metricsdash/ (feat.metricsdash LogNote feedback submission)',
};

// BUG-126: FeedbackRecord also now carries an optional `kind` field
// (mirroring metricsdash.NoteKind's values) naming which BOW item type
// an imported record should become. Every value this script will ever
// accept must be a `claude-bow.js add <type>` verb this script knows how
// to satisfy the required flags for (see FINDING_DEFAULT_CLASS below for
// why 'finding' needs one more flag than 'bug'/'assumption' do).
const VALID_KINDS = ['bug', 'finding', 'assumption'];
const DEFAULT_KIND = 'bug'; // FEAT-065's original, unconditional behavior — every record predating this field, or explicitly kind-less, still becomes a bug exactly as before.

// `claude-bow.js add finding` requires --class from a closed
// (FINDING_CLASSES) list; FeedbackRecord carries no weakness-class field
// (a feedback note is not itself a security-finding writeup), so a
// finding-kind record is filed under the list's own generic catch-all
// bucket rather than this script inventing a classification it has no
// basis for.
const FINDING_DEFAULT_CLASS = 'other';

// ── Typed, correlation-ID-bearing logging (AC-DM17 / GR#1) ─────────────────
// Every failure this script can hit gets a stable code, a correlation ID,
// and a human-readable message, written as one JSON line to stderr — never
// a bare console.error(err) and never a swallowed catch{}.

function logError(code, correlationId, message, extra) {
  const entry = Object.assign(
    { level: 'error', code, correlationId, message, at: new Date().toISOString() },
    extra || {}
  );
  process.stderr.write(JSON.stringify(entry) + '\n');
}

function logInfo(message, extra) {
  const entry = Object.assign({ level: 'info', message, at: new Date().toISOString() }, extra || {});
  process.stdout.write(JSON.stringify(entry) + '\n');
}

// ── Record validation ───────────────────────────────────────────────────
// Mirrors internal/engine/debug/feedback.go's FeedbackRecord field-for-
// field: schemaVersion (number, must equal SCHEMA_VERSION), timestamp
// (non-empty string), tick (number), correlationId (non-empty string),
// body (non-empty string), debugTouched (boolean). Any deviation is
// "malformed" — this is deliberately strict (AC-DM10's "well-formed
// record" language): a record that is valid JSON but the wrong shape is
// exactly as untrustworthy as one that isn't JSON at all.

const REQUIRED_STRING_FIELDS = ['timestamp', 'correlationId', 'body'];

function validateRecord(raw) {
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    return { ok: false, reason: `not valid JSON: ${err.message}` };
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return { ok: false, reason: 'record is not a JSON object' };
  }
  if (parsed.schemaVersion !== SCHEMA_VERSION) {
    return {
      ok: false,
      reason: `unsupported schemaVersion ${JSON.stringify(parsed.schemaVersion)}, expected ${SCHEMA_VERSION}`,
    };
  }
  for (const field of REQUIRED_STRING_FIELDS) {
    if (typeof parsed[field] !== 'string' || parsed[field].length === 0) {
      return { ok: false, reason: `field "${field}" missing or not a non-empty string` };
    }
  }
  if (typeof parsed.tick !== 'number' || !Number.isFinite(parsed.tick)) {
    return { ok: false, reason: 'field "tick" missing or not a finite number' };
  }
  if (typeof parsed.debugTouched !== 'boolean') {
    return { ok: false, reason: 'field "debugTouched" missing or not a boolean' };
  }
  // sourceMkey and kind (ASM-477/BUG-126) are OPTIONAL — absent entirely
  // is the expected shape for every record written before these fields
  // existed (backward compatibility is load-bearing here, not merely
  // convenient: FEAT-065's own already-Destructive-ACCEPTed behavior for
  // its own historical records must not change). Present-but-wrong-type
  // is still rejected as malformed, same strictness as every other field.
  if (parsed.sourceMkey !== undefined && typeof parsed.sourceMkey !== 'string') {
    return { ok: false, reason: 'field "sourceMkey" present but not a string' };
  }
  if (parsed.kind !== undefined && typeof parsed.kind !== 'string') {
    return { ok: false, reason: 'field "kind" present but not a string' };
  }
  return { ok: true, record: parsed };
}

// deriveAttribution derives the --codejson/--code-path pair a record's
// `claude-bow.js add` invocation should use (ASM-477), from the record's
// own sourceMkey field. A missing/empty sourceMkey falls back to
// DEFAULT_SOURCE_MKEY/defaultCodePath (opts.codePath, itself defaulting
// to DEFAULT_CODE_PATH) exactly as this script behaved before ASM-477's
// fix — no behavior change for FEAT-065's own historical records. A
// present sourceMkey outside SOURCE_CODE_PATHS still gets a sensible,
// generically-derived --code-path rather than being dropped back to
// feat.devmode, so a future writer this file has never heard of
// attributes correctly with zero changes needed here (GR#3).
function deriveAttribution(record, defaultCodePath) {
  const sourceMkey = typeof record.sourceMkey === 'string' && record.sourceMkey.length > 0
    ? record.sourceMkey
    : DEFAULT_SOURCE_MKEY;
  const codePath = sourceMkey === DEFAULT_SOURCE_MKEY
    ? defaultCodePath
    : (SOURCE_CODE_PATHS[sourceMkey] || `${sourceMkey} (feedback submission)`);
  return { codejson: sourceMkey, codePath };
}

// deriveKind derives the `claude-bow.js add <kind>` verb a record should
// import as (BUG-126), from the record's own kind field. A missing/empty/
// unrecognized kind falls back to DEFAULT_KIND ('bug') exactly as this
// script's hardcoded behavior was before BUG-126's fix.
function deriveKind(record) {
  return typeof record.kind === 'string' && VALID_KINDS.includes(record.kind)
    ? record.kind
    : DEFAULT_KIND;
}

// deriveTitle builds a short BOW title from the record's free-text body —
// never the raw, unbounded body itself (BOW titles are meant to be
// skimmable in `claude-bow.js list`).
function deriveTitle(record) {
  const oneLine = String(record.body).replace(/\s+/g, ' ').trim();
  const truncated = oneLine.length > 80 ? oneLine.slice(0, 77) + '...' : oneLine;
  return `Dev feedback: ${truncated || '(empty body)'}`;
}

function writeErrorSidecar(recordPath, reason) {
  const sidecar = recordPath + '.error';
  const body = JSON.stringify({ failedAt: new Date().toISOString(), reason }, null, 2) + '\n';
  fs.writeFileSync(sidecar, body, 'utf8');
}

function clearStaleErrorSidecar(recordPath) {
  try {
    fs.unlinkSync(recordPath + '.error');
  } catch (err) {
    // ENOENT (no sidecar existed) is the expected common case — not an
    // error. Anything else is swallowed deliberately here too: a stale
    // sidecar failing to delete is cosmetic (the record itself already
    // moved to processed/ successfully), never worth failing the run over.
    void err;
  }
}

/**
 * importOne processes a single inbox record file: validate, and on
 * success call `claude-bow.js add bug` with --desc-file pointed at the
 * record itself (BUG-090), then move it to processedDir. Returns a status
 * string: 'imported' | 'malformed' | 'bow-failed' | 'move-failed' | 'read-failed'.
 */
function importOne(recordPath, opts) {
  const processedDir = opts.processedDir;
  const bowScript = opts.bowScript;
  const spawnSyncFn = opts.spawnSyncFn;
  const codePath = opts.codePath;
  const correlationId = crypto.randomUUID();

  let raw;
  try {
    raw = fs.readFileSync(recordPath, 'utf8');
  } catch (err) {
    logError('devfeedback-read-failed', correlationId, `could not read ${recordPath}: ${err.message}`);
    try {
      writeErrorSidecar(recordPath, `read failed: ${err.message}`);
    } catch (sidecarErr) {
      logError('devfeedback-sidecar-write-failed', correlationId, `also could not write .error sidecar for ${recordPath}: ${sidecarErr.message}`);
    }
    return 'read-failed';
  }

  const validation = validateRecord(raw);
  if (!validation.ok) {
    writeErrorSidecar(recordPath, validation.reason);
    logError('devfeedback-malformed', correlationId, `malformed record ${recordPath}: ${validation.reason}`);
    return 'malformed';
  }

  const record = validation.record;
  const title = deriveTitle(record);

  // ASM-477/BUG-126: attribution (--codejson/--code-path) and the BOW
  // item type (add <kind>) are now derived PER RECORD from the record's
  // own sourceMkey/kind fields, instead of being hardcoded for every
  // record regardless of which tool actually submitted it.
  const attribution = deriveAttribution(record, codePath);
  const kind = deriveKind(record);

  const args = [
    bowScript, 'add', kind, title,
    '--desc-file', recordPath, // BUG-090: never inline --desc
    '--code-path', attribution.codePath,
    '--codejson', attribution.codejson,
  ];
  if (kind === 'finding') {
    // `claude-bow.js add finding` requires --class from a closed list
    // this record has no basis to classify itself against — see
    // FINDING_DEFAULT_CLASS's own comment above.
    args.push('--class', FINDING_DEFAULT_CLASS);
  }
  const result = spawnSyncFn(process.execPath, args, { cwd: ROOT, encoding: 'utf8', timeout: 30000 });

  if (result.error || result.status !== 0) {
    const cause = result.error
      ? result.error.message
      : `exit ${result.status}: ${(result.stderr || result.stdout || '').trim()}`;
    writeErrorSidecar(recordPath, `claude-bow.js add bug failed: ${cause}`);
    logError('devfeedback-bow-add-failed', correlationId, `claude-bow.js add bug failed for ${recordPath}: ${cause}`);
    return 'bow-failed';
  }

  try {
    fs.mkdirSync(processedDir, { recursive: true });
    fs.renameSync(recordPath, path.join(processedDir, path.basename(recordPath)));
  } catch (err) {
    // See this file's header "KNOWN LIMITATION" note: a BOW item now
    // exists for this submission but the move failed. Marked distinctly
    // so a human notices before a re-run could double-add it.
    writeErrorSidecar(
      recordPath,
      `BOW item "${title}" was created successfully but moving the record to processed/ failed: ${err.message} -- MANUAL CLEANUP REQUIRED, do not resubmit without checking the BOW for a duplicate first`
    );
    logError('devfeedback-move-failed', correlationId, `post-success move failed for ${recordPath}: ${err.message}`);
    return 'move-failed';
  }

  clearStaleErrorSidecar(recordPath); // a prior failed attempt's sidecar is now stale
  logInfo(`imported ${path.basename(recordPath)} -> BOW bug`, { correlationId, title });
  return 'imported';
}

/**
 * runImport scans inboxDir for *.json records (never .tmp partial writes,
 * never .error sidecars — both are excluded by the .json-only filter) and
 * processes each via importOne. Returns a summary object. A missing
 * inboxDir (nothing has ever been submitted) is a legitimate no-op, not an
 * error (AC-DM11).
 */
function runImport(opts) {
  const options = opts || {};
  const inboxDir = options.inboxDir || DEFAULT_INBOX_DIR;
  const processedDir = options.processedDir || DEFAULT_PROCESSED_DIR;
  const bowScript = options.bowScript || DEFAULT_BOW_SCRIPT;
  const spawnSyncFn = options.spawnSyncFn || spawnSync;
  const codePath = options.codePath || DEFAULT_CODE_PATH;

  const summary = { imported: 0, malformed: 0, failed: 0, total: 0 };

  if (!fs.existsSync(inboxDir)) {
    return summary;
  }

  const entries = fs.readdirSync(inboxDir)
    .filter(name => name.endsWith('.json'))
    .sort(); // deterministic processing order

  summary.total = entries.length;
  for (const name of entries) {
    const recordPath = path.join(inboxDir, name);
    const status = importOne(recordPath, { processedDir, bowScript, spawnSyncFn, codePath });
    if (status === 'imported') summary.imported++;
    else if (status === 'malformed') summary.malformed++;
    else summary.failed++;
  }
  return summary;
}

function main() {
  const summary = runImport();
  logInfo('devfeedback import run complete', summary);
}

// require.main === module guard (same testability pattern as
// claude-plan-guard.js/claude-secret-guard.js): running this file directly
// drives main(); required from a test harness exposes the pure functions
// instead, with no side effects at require time.
if (require.main === module) {
  try {
    main();
  } catch (err) {
    logError('devfeedback-fatal', crypto.randomUUID(), `unexpected fatal error: ${err && err.stack ? err.stack : err}`);
    process.exitCode = 1;
  }
} else {
  module.exports = {
    runImport,
    importOne,
    validateRecord,
    deriveTitle,
    deriveAttribution,
    deriveKind,
    DEFAULT_INBOX_DIR,
    DEFAULT_PROCESSED_DIR,
    DEFAULT_BOW_SCRIPT,
    DEFAULT_CODE_PATH,
    DEFAULT_SOURCE_MKEY,
    SOURCE_CODE_PATHS,
    VALID_KINDS,
    DEFAULT_KIND,
    FINDING_DEFAULT_CLASS,
    SCHEMA_VERSION,
  };
}
