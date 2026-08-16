// Module key: tool.fileclaimguard (see code.json; GUID bbc20510-f7f5-483d-89ed-8ec84aa33738)
// Spec ref: GR#3; GR#20; M0-ENG §5 (hooks); FEAT-136

/**
 * PreToolUse hook — edit-time file-claim guard (BOW mkey: tool.fileclaimguard,
 * FEAT-136 Guard 2).
 *
 * WHY THIS EXISTS
 *
 * claude-dispatch-guard.js's dispatch-time ownership check only runs when a
 * brief DECLARES ownership ("FILES YOU OWN: ..."), and it only fires on the
 * Agent tool — it cannot see a subagent that reaches for a file it was never
 * told about, or two agents (from different sessions) that both drift onto the
 * same file mid-task. The 2026-08-16 ui.dash.md collision happened because
 * nothing stood between an Edit/Write tool call and the on-disk bytes. This
 * guard is that last line: before every Edit/Write it consults the metro
 * DB's existing sync_file_claims table and refuses the edit when the target
 * path is claimed by a DIFFERENT live session.
 *
 * WHAT IT DOES
 *
 *   - Reads the mutator tool's target path(s) — Edit/Write's `file_path`, and
 *     (FEAT-136 reject-fix) MultiEdit/NotebookEdit's `file_path`/`notebook_path`
 *     and any `edits[].file_path` — canonicalises each to the repo-relative
 *     form sync_file_claims stores, and asks for live claims (claimed_ms within
 *     CLAIM_TTL_MS — the SAME "live claim" notion as claude-dispatch-guard.js,
 *     imported from there, GR#3).
 *   - If a live claim on an overlapping path (itself, an ancestor, a descendant,
 *     or a glob pattern that expands to it) is owned by a session OTHER than the
 *     current one, DENIES the edit, naming the owner and the release command.
 *   - If the path is unowned or owned by the current session, allows silently.
 *
 * FAIL-OPEN, deliberately. This hook runs on EVERY edit in every session; a
 * bug or a DB outage here must never be able to freeze the whole team's
 * editing. Every internal error (unparsable stdin, DB unreachable, query
 * failure) writes a one-line stderr warning and exits 0 (allow). Only a
 * positively-identified foreign claim produces a deny.
 *
 * Deliberate disable: CLAUDE_DISABLE_FILE_CLAIM_GUARD=1.
 *
 * Reuses claude-dispatch-guard.js's exported connect() (which itself is the
 * shared claude-db.js throw-on-failure helper — no new connection scheme) and
 * its overlaps()/normalise() path semantics, so the two guards can never
 * disagree on what counts as a collision.
 *
 * Receives JSON on stdin: { tool_name: "Edit"|"Write"|"MultiEdit"|"NotebookEdit",
 *   session_id, tool_input: { file_path | notebook_path, ... } }. Denies via the
 *   standard PreToolUse hookSpecificOutput shape (same as claude-dispatch-guard.js).
 */

'use strict';

const fs = require('fs');
const path = require('path');

const {
  connect,
  overlaps,
  normalise,
  CLAIM_TTL_MS,
} = require('./claude-dispatch-guard.js');

const ROOT = __dirname;

function allow() {
  process.exit(0);
}

function deny(reason) {
  process.stdout.write(
    JSON.stringify({
      hookSpecificOutput: {
        hookEventName: 'PreToolUse',
        permissionDecision: 'deny',
        permissionDecisionReason: reason,
      },
    })
  );
  process.exit(0);
}

function readStdin() {
  try {
    return fs.readFileSync(0, 'utf8');
  } catch {
    return '';
  }
}

/**
 * Convert a mutator tool's target path (absolute Windows path under the repo
 * root, or an already-relative path) to the repo-relative forward-slash form
 * sync_file_claims stores. Pure and side-effect-free — unit-tested directly.
 *
 * FEAT-136 r2 reject-fix (relative-path `..` escape): EVERY incoming path is
 * anchored to ROOT the same way — `path.resolve(ROOT, filePath)` — so a
 * relative spelling (`../Metropolis/docs/x.md`, `./docs/x.md`, `docs//x.md`)
 * resolves against the repo root and an absolute spelling passes through
 * unchanged, then the ROOT prefix is stripped to the repo-relative key. The
 * round-1 code only resolved ABSOLUTE paths; a relative path went through
 * path.posix.normalize, which collapses internal `.`/`..` but never anchors a
 * leading `..` to ROOT — so `../Metropolis/docs/x.md` stayed the distinct key
 * `../Metropolis/docs/x.md`, `overlaps(..., 'docs/x.md')` missed it, and a
 * foreign edit of the claimed file was allowed. `.`/`..`/duplicate slashes
 * therefore collapse to one key before the ownership comparison.
 */
function toRepoRelative(filePath) {
  const p = path.resolve(ROOT, String(filePath)).replace(/\\/g, '/');
  const root = ROOT.replace(/\\/g, '/').replace(/\/+$/, '');
  const lower = p.toLowerCase();
  const rootLower = root.toLowerCase();
  if (lower === rootLower) return '';
  if (lower.startsWith(rootLower + '/')) return p.slice(root.length + 1);
  // Not under the repo root (a POSIX-absolute path resolving to the drive
  // root, or a path escaping via enough `..`): keep the resolved absolute
  // form — it names a DIFFERENT file, so it must never match a claim held on
  // the repo-relative spelling.
  return p.replace(/^\/+/, '');
}

/**
 * FEAT-136 reject-fix (MultiEdit/NotebookEdit): the settings.json matcher is
 * the regex `Edit|Write`, which partial-matches `MultiEdit`/`NotebookEdit`, so
 * those tool calls REACH this guard — but used to early-allow() on the exact
 * name gate. Every path a mutator can touch is extracted here (the top-level
 * file_path, notebook_path/notebookPath, and any edits[].file_path carried by a
 * per-edit variant), so a MultiEdit/NotebookEdit targeting a foreign-owned path
 * is checked and denied, not silently skipped.
 */
function extractEditPaths(input) {
  const found = [];
  const push = (v) => {
    if (typeof v === 'string' && v.trim()) found.push(v);
  };
  push(input.file_path);
  push(input.notebook_path);
  push(input.notebookPath);
  if (Array.isArray(input.edits)) {
    for (const e of input.edits) {
      if (e && typeof e === 'object') {
        push(e.file_path);
        push(e.notebook_path);
        push(e.notebookPath);
        push(e.path);
      }
    }
  }
  return found;
}

// The mutator tools the settings.json `Edit|Write` matcher routes here. Kept
// explicit (not a suffix match) so a future unrelated "FooEdit" tool is still
// fail-open rather than half-understood.
const MUTATOR_TOOLS = new Set(['Edit', 'Write', 'MultiEdit', 'NotebookEdit']);

/**
 * The pure decision: given live claims (rows with at least { path, name,
 * session_id }), the current session id, and the repo-relative edit target,
 * return { owner, heldPath } for the first FOREIGN overlapping claim, or null
 * when the edit is unowned or owned by the current session. Exported for unit
 * tests — main() is the only thing that turns a hit into a deny.
 */
function foreignOwner(targetRelPath, claims, sessionId) {
  for (const held of claims || []) {
    const heldSession = held.session_id == null ? null : String(held.session_id);
    if (heldSession === sessionId) continue; // own claim -> allow
    const heldPath = normalise(String(held.path));
    if (overlaps(targetRelPath, heldPath)) {
      return { owner: held.name, heldPath };
    }
  }
  return null;
}

async function main() {
  if (process.env.CLAUDE_DISABLE_FILE_CLAIM_GUARD === '1') allow();

  let payload;
  try {
    payload = JSON.parse(readStdin() || '{}');
  } catch {
    allow(); // unparsable input is not this guard's problem to adjudicate
  }

  const toolName = payload.tool_name || payload.tool || '';
  if (!MUTATOR_TOOLS.has(toolName)) allow();

  const input = payload.tool_input || {};
  const editPaths = extractEditPaths(input);
  if (!editPaths.length) allow();

  const sessionId = payload.session_id || process.env.CLAUDE_SESSION_ID || null;

  let conn;
  try {
    conn = await connect();
  } catch (err) {
    process.stderr.write(
      `file-claim-guard: cannot reach metro MariaDB, allowing edit (fail-open) — ${err.message}\n`
    );
    allow();
    return;
  }

  try {
    const cutoff = Date.now() - CLAIM_TTL_MS;
    const [claims] = await conn.query(
      `SELECT path, name, session_id FROM sync_file_claims WHERE claimed_ms > ?`,
      [cutoff]
    );
    for (const filePath of editPaths) {
      const targetRel = normalise(toRepoRelative(filePath));
      const hit = foreignOwner(targetRel, claims, sessionId);
      if (hit) {
        deny(
          `🛑 FILE CLAIM — "${filePath}" is claimed by ${hit.owner} (a different live session). ` +
            `Editing it now would race that agent's in-flight work — the exact ` +
            `collision class FEAT-136 exists to stop. Wait for them to finish, or ` +
            `release the claim first with: node claude-sync.js release "${hit.heldPath}" ` +
            `(with the owner's awareness), then re-send the edit. ` +
            `Deliberate bypass: CLAUDE_DISABLE_FILE_CLAIM_GUARD=1`
        );
        return;
      }
    }
  } catch (err) {
    // Fail-open: a DB blip mid-query must never crash an Edit.
    process.stderr.write(
      `file-claim-guard: claim check failed, allowing edit (fail-open) — ${err.message}\n`
    );
  } finally {
    try {
      await conn.end();
    } catch {
      /* closing an already-dead connection must not fail the edit */
    }
  }

  allow();
}

if (require.main === module) {
  main().catch((err) => {
    process.stderr.write(`file-claim-guard: internal error, allowing edit — ${err.message}\n`);
    process.exit(0);
  });
}

module.exports = { toRepoRelative, extractEditPaths, foreignOwner };
