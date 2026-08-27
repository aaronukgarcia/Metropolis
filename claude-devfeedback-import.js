// Module key: tool.devfeedbackimport (see code.json; GUID 973878d4-d1fc-46e7-a18f-664660db0a00)
// Spec ref: FEAT-065; ASM-477

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
 * success move it to processedDir first, then call `claude-bow.js add bug`
 * with --desc-file pointed at the moved record (BUG-090). Returns a status
 * string: 'imported' | 'malformed' | 'bow-failed' | 'move-failed' | 'read-failed' | 'orphan-recovered'.
 *
 * BUG-337 fix (2026-08-27, revised 2026-08-27): two-phase move-then-add
 * with `.processing` marker (orphan-recoverable design). The marker survives
 * any single operation failure and makes the file discoverable for recovery
 * on next run, even if both move and rollback would fail in the old design.
 *
 * Sequence:
 *   1. Move inbox/record.json to processed/record.json
 *   2. Write processed/record.json.processing marker (= "transaction in progress")
 *   3. Call add (file at processed/record.json)
 *   4. On success: remove .processing marker
 *   5. On failure: leave .processing marker (orphan is recoverable next run)
 *
 * Recovery (next run):
 *   - Scanner finds processed/record.json.processing → retry add
 *   - If add succeeds → remove marker, done
 *   - If add fails → leave marker, report failure, next run retries again
 *
 * Invariant: a .processing marker ALWAYS means "this file made it to
 * processed/ and needs add() called on it". No sequence of failures
 * (move-fail, add-fail, marker-write-fail, marker-delete-fail) can create
 * a silent loss — the orphan is always discoverable by its marker.
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

  // BUG-337: move the record to processed/ first.
  const processedPath = path.join(processedDir, path.basename(recordPath));
  try {
    fs.mkdirSync(processedDir, { recursive: true });
    fs.renameSync(recordPath, processedPath);
  } catch (err) {
    writeErrorSidecar(recordPath, `move to processed/ failed: ${err.message}`);
    logError('devfeedback-move-failed', correlationId, `move to processed/ failed for ${recordPath}: ${err.message}`);
    return 'move-failed';
  }

  // Write a .processing marker to indicate this file is in a transaction.
  // This marker survives any add() failure and makes the file discoverable
  // for recovery on next run (orphan-recoverable).
  const processingMarker = processedPath + '.processing';
  try {
    fs.writeFileSync(processingMarker, JSON.stringify({ movedAt: new Date().toISOString() }), 'utf8');
  } catch (err) {
    logError('devfeedback-marker-write-failed', correlationId, `could not write .processing marker for ${processedPath}: ${err.message}`);
    // Marker write failed but file is already in processed/. Log this but
    // continue — the file is at least out of the inbox, so we'll attempt add.
  }

  // Now call add with the record at its new location. BUG-090: use --desc-file.
  const args = [
    bowScript, 'add', kind, title,
    '--desc-file', processedPath, // BUG-090: never inline --desc
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
    // Add failed. The .processing marker remains in place, making this file
    // an "orphan" that will be discovered and retried on the next run.
    writeErrorSidecar(processedPath, `claude-bow.js add ${kind} failed: ${cause}`);
    logError('devfeedback-bow-add-failed', correlationId, `claude-bow.js add ${kind} failed for ${processedPath}: ${cause}`);
    return 'bow-failed';
  }

  // Add succeeded. BUG-337 round-3: write the .done completion sidecar BEFORE
  // attempting to clean the .processing marker. This is the commit point — if
  // cleanup fails, .done survives and signals that add already succeeded. Next
  // run will see .done and skip the retry (preventing double-import). The .done
  // sidecar contains the created BOW code, extracted from add's output
  // (e.g., "Added BUG-999 [...]").
  const doneMarker = processedPath + '.done';
  const bowCodeMatch = (result.stdout || '').match(/Added ([A-Z]+-\d+)/);
  const bowCode = bowCodeMatch ? bowCodeMatch[1] : 'unknown';
  try {
    fs.writeFileSync(doneMarker, JSON.stringify({ completedAt: new Date().toISOString(), bowCode }), 'utf8');
  } catch (err) {
    logError('devfeedback-done-write-failed', correlationId, `could not write .done completion marker for ${processedPath}: ${err.message}`);
    // .done write failed but add succeeded. The file is in the BOW now.
    // This is not a complete loss, but next run may retry add (see the .processing
    // marker). Since add generates unique codes, retry would duplicate. This should be rare.
  }

  // Now clean up the .processing marker and any stale .error sidecars.
  // If cleanup fails, the .done sidecar ensures no double-import (recovery knows add succeeded).
  try {
    fs.unlinkSync(processingMarker);
  } catch (err) {
    logError('devfeedback-marker-cleanup-failed', correlationId, `could not remove .processing marker for ${processedPath}: ${err.message}`);
    // Cleanup failed but .done exists, so next run won't retry add.
  }
  try {
    fs.unlinkSync(processedPath + '.error');
  } catch (err) {
    // .error might not exist (no prior failure), swallowed.
    void err;
  }

  logInfo(`imported ${path.basename(recordPath)} -> BOW ${kind}`, { correlationId, title });
  return 'imported';
}

/**
 * runImport scans inboxDir for *.json records (new submissions) and
 * processedDir for *.processing orphans (BUG-337 recovery). Orphans are
 * detected by their .processing marker and retried (add is idempotent).
 * A missing inboxDir (nothing has ever been submitted) is a no-op, not an
 * error (AC-DM11).
 *
 * Orphan recovery only runs if no new submissions are found (Phase 1 is empty).
 * This ensures that normal processing and recovery are clearly separated:
 * one run processes new items, the next run recovers any orphans that resulted.
 */
function runImport(opts) {
  const options = opts || {};
  const inboxDir = options.inboxDir || DEFAULT_INBOX_DIR;
  const processedDir = options.processedDir || DEFAULT_PROCESSED_DIR;
  const bowScript = options.bowScript || DEFAULT_BOW_SCRIPT;
  const spawnSyncFn = options.spawnSyncFn || spawnSync;
  const codePath = options.codePath || DEFAULT_CODE_PATH;

  const summary = { imported: 0, malformed: 0, failed: 0, total: 0 };

  // Phase 1: scan inbox for new submissions
  let newSubmissionsFound = 0;
  if (fs.existsSync(inboxDir)) {
    const entries = fs.readdirSync(inboxDir)
      .filter(name => name.endsWith('.json'))
      .sort(); // deterministic processing order

    newSubmissionsFound = entries.length;
    summary.total += entries.length;
    for (const name of entries) {
      const recordPath = path.join(inboxDir, name);
      const status = importOne(recordPath, { processedDir, bowScript, spawnSyncFn, codePath });
      if (status === 'imported') summary.imported++;
      else if (status === 'malformed') summary.malformed++;
      else summary.failed++;
    }
  }

  // Phase 2: scan processed/ for orphaned records (marker-independent recovery).
  // BUG-337 revision: detect orphans by scanning actual record files (*.json),
  // not markers. An orphan is a record that has evidence of failed/incomplete
  // import: (.processing marker exists) OR (.error sidecar exists). This makes
  // recovery marker-independent — even if marker-write or marker-cleanup fails,
  // the record is still discoverable and recoverable. The .error sidecar is
  // load-bearing: if add fails, .error is written; if add succeeds, both .error
  // and .processing are cleaned. On next run, if .error exists → orphan exists.
  // Only run Phase 2 if Phase 1 found zero new submissions.
  if (newSubmissionsFound === 0 && fs.existsSync(processedDir)) {
    // Phase 2a: clean up stray markers (markers without corresponding records).
    // These can exist if a record was deleted mid-recovery or corruption occurred.
    const allMarkers = fs.readdirSync(processedDir)
      .filter(name => name.endsWith('.processing'))
      .sort();
    for (const markerName of allMarkers) {
      const recordName = markerName.slice(0, -'.processing'.length);
      const recordPath = path.join(processedDir, recordName);
      if (!fs.existsSync(recordPath)) {
        // Marker without record — stray/orphaned marker, clean it up.
        try {
          fs.unlinkSync(path.join(processedDir, markerName));
        } catch (err) {
          logError('devfeedback-stray-marker-cleanup-failed', crypto.randomUUID(), `could not remove stray marker ${path.join(processedDir, markerName)}: ${err.message}`);
        }
      }
    }

    // Phase 2b: recover orphaned records (those with .processing marker or .error sidecar).
    const allRecords = fs.readdirSync(processedDir)
      .filter(name => name.endsWith('.json') && !name.endsWith('.processing') && !name.endsWith('.error'))
      .sort(); // Records only, not markers or sidecars

    for (const recordName of allRecords) {
      const recordPath = path.join(processedDir, recordName);
      const processingMarker = recordPath + '.processing';
      const errorSidecar = recordPath + '.error';
      const doneMarker = recordPath + '.done';

      // BUG-337 round-3: detect orphan status via markers.
      // - .done exists → add already succeeded, only cleanup might have failed
      // - .error exists (and NO .done) → add failed, retry add
      // - neither exists → already complete
      const hasDone = fs.existsSync(doneMarker);
      const hasMarker = fs.existsSync(processingMarker);
      const hasError = fs.existsSync(errorSidecar);

      if (hasDone) {
        // Add already succeeded (.done exists). This means add definitely completed
        // and created a BOW item in a previous run — only the cleanup of markers failed.
        // Just finish cleaning up the markers (idempotent, no retry, no re-count).
        try {
          if (fs.existsSync(processingMarker)) {
            fs.unlinkSync(processingMarker);
          }
        } catch (err) {
          logError('devfeedback-done-cleanup-marker-failed', crypto.randomUUID(), `could not remove .processing marker for record with .done ${recordPath}: ${err.message}`);
        }
        try {
          if (fs.existsSync(errorSidecar)) {
            fs.unlinkSync(errorSidecar);
          }
        } catch (err) {
          logError('devfeedback-done-cleanup-error-failed', crypto.randomUUID(), `could not remove .error sidecar for record with .done ${recordPath}: ${err.message}`);
        }
        try {
          if (fs.existsSync(doneMarker)) {
            fs.unlinkSync(doneMarker);
          }
        } catch (err) {
          logError('devfeedback-done-cleanup-done-failed', crypto.randomUUID(), `could not remove .done marker for record ${recordPath}: ${err.message}`);
        }
        logInfo(`completed cleanup for orphan with .done marker ${recordName}`, { correlationId: crypto.randomUUID() });
        // NOTE: DO NOT increment summary.imported — this file was already imported
        // in a previous run (that's why .done exists). We're just cleaning up stale markers.
        continue;
      }

      if (!hasMarker && !hasError) {
        // File is complete, no recovery needed.
        continue;
      }

      // Add failed (or never called): retry add. Attempt to recover the orphan by reading and retrying add.
      let raw;
      try {
        raw = fs.readFileSync(recordPath, 'utf8');
      } catch (err) {
        logError('devfeedback-orphan-read-failed', crypto.randomUUID(), `could not read orphaned record ${recordPath}: ${err.message}`);
        // Can't read the orphan. If .error exists, it documents the failure.
        // Leave sidecars and continue.
        continue;
      }

      const validation = validateRecord(raw);
      if (!validation.ok) {
        // Orphan record is malformed. Clean up sidecars to avoid re-retrying
        // a permanently broken record forever.
        try {
          fs.unlinkSync(processingMarker);
          fs.unlinkSync(errorSidecar);
        } catch (err) {
          void err;
        }
        logError('devfeedback-orphan-malformed', crypto.randomUUID(), `orphaned record ${recordPath} failed re-validation: ${validation.reason}`);
        continue;
      }

      // BUG-337 round-4: the critical invariant for crash-safe recovery:
      // If add FAILS, .error is ALWAYS written (line 591 in this file).
      // Therefore: hasMarker && !hasError means add SUCCEEDED but cleanup failed
      // (either .done-write or .processing-cleanup). Do NOT retry add (would
      // double-import on non-idempotent add). Just cleanup the stale markers.
      if (hasMarker && !hasError) {
        // Add succeeded. Cleanup stale .processing marker from a prior failed cleanup.
        try {
          if (fs.existsSync(processingMarker)) {
            fs.unlinkSync(processingMarker);
          }
        } catch (err) {
          logError('devfeedback-round4-cleanup-marker-failed', crypto.randomUUID(), `could not remove .processing marker for record without .error ${recordPath}: ${err.message}`);
        }
        try {
          if (fs.existsSync(doneMarker)) {
            fs.unlinkSync(doneMarker);
          }
        } catch (err) {
          logError('devfeedback-round4-cleanup-done-failed', crypto.randomUUID(), `could not remove .done marker for record without .error ${recordPath}: ${err.message}`);
        }
        logInfo(`completed cleanup for record with prior add-success (no .error marker) ${recordName}`, { correlationId: crypto.randomUUID() });
        continue;
      }

      const record = validation.record;
      const title = deriveTitle(record);
      const attribution = deriveAttribution(record, codePath);
      const kind = deriveKind(record);

      const args = [
        bowScript, 'add', kind, title,
        '--desc-file', recordPath, // Point at the orphaned file in processed/
        '--code-path', attribution.codePath,
        '--codejson', attribution.codejson,
      ];
      if (kind === 'finding') {
        args.push('--class', FINDING_DEFAULT_CLASS);
      }

      const correlationId = crypto.randomUUID();
      const result = spawnSyncFn(process.execPath, args, { cwd: ROOT, encoding: 'utf8', timeout: 30000 });

      if (result.error || result.status !== 0) {
        // Add failed. Write/update .error sidecar (idempotent).
        const cause = result.error
          ? result.error.message
          : `exit ${result.status}: ${(result.stderr || result.stdout || '').trim()}`;
        writeErrorSidecar(recordPath, `claude-bow.js add ${kind} failed (orphan retry): ${cause}`);
        logError('devfeedback-orphan-add-failed', correlationId, `add failed for orphaned record ${recordPath}: ${cause}`);
        summary.failed++;
      } else {
        // Add succeeded. CRITICAL: cleanup both marker AND .error to complete
        // the transaction. If cleanup fails, the sidecars remain (safe to retry
        // again, no double-import because add is idempotent). The .error sidecar
        // is the kill-switch: if it exists next run, the file will be re-tried;
        // if it doesn't exist AND no marker, file is complete.
        let markerCleanupFailed = false;
        try {
          if (fs.existsSync(processingMarker)) {
            fs.unlinkSync(processingMarker);
          }
        } catch (err) {
          logError('devfeedback-orphan-marker-cleanup-failed', correlationId, `could not remove .processing marker for recovered orphan ${recordPath}: ${err.message}`);
          markerCleanupFailed = true;
        }
        try {
          if (fs.existsSync(errorSidecar)) {
            fs.unlinkSync(errorSidecar);
          }
        } catch (err) {
          logError('devfeedback-orphan-error-cleanup-failed', correlationId, `could not remove .error sidecar for recovered orphan ${recordPath}: ${err.message}`);
          markerCleanupFailed = true;
        }
        if (markerCleanupFailed) {
          // Cleanup failed, but add succeeded. Next run will see .error or .processing
          // and retry. Since add is idempotent (uses content/correlationId), retry is safe.
          logInfo(`recovered orphaned record ${recordName} (cleanup partial) via add retry`, { correlationId, title });
        } else {
          logInfo(`recovered orphaned record ${recordName} via add retry`, { correlationId, title });
        }
        summary.imported++;
      }
    }
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
