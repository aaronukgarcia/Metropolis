// Module key: tool.bowrefcheck (see code.json; GUID 3ef12808-4e54-439e-b5a3-a9158bb26aa7)
// Spec ref: M0-ENG §4

/**
 * PreToolUse hook — BOW commit-message ref validation (BOW mkey: tool.bow /
 * MOD-007).
 *
 * Spec: M0-ENG §4 (The Book of Work) / §5 ("A commit-msg hook validates the
 * ref exists in BoW and auto-comments the commit hash onto the entity");
 * docs/planning/acceptance/tool.bow.md (17 ACs).
 *
 * Intercepts `git commit` commands. If the staged change touches `cmd/`,
 * `internal/`, or `data/` (checked via `git diff --cached --name-only`,
 * never via the command string), the commit message must carry at least one
 * `[mkey]` or `[CODE-NNN]` tag (e.g. `[tool.bow]` or `[MOD-007]`) that
 * resolves to a live row in the metro BOW's `bow_items` table — resolution
 * uses claude-bow.js's own canonical `findItemByRef(db, ref)` (guid exact /
 * code case-insensitive / mkey case-sensitive), never a bespoke query, so
 * this hook can never drift from the CLI's own lookup behaviour (BUG-003).
 * Commits that don't touch those trees are exempt and pass through silently
 * (AC-2/AC-3).
 *
 * FAIL-OPEN POSTURE (deliberate, LEAD-CONFIRMED — see tool.bow.md Escalation
 * #2): unlike claude-plan-guard.js / claude-secret-guard.js (which are
 * fail-CLOSED because they gate *content* problems), this hook is a hygiene
 * gate over BOW traceability, mirroring claude-version-guard.js's posture.
 * A transient infrastructure failure (DB unreachable, unparseable stdin, an
 * unextractable commit message) must never brick unrelated commits repo-
 * wide — every such case WARNS (or is silently exempt) and ALLOWS, it never
 * denies. Only two things are genuinely denied: an enforced-path commit with
 * zero `[mkey]` tags (AC-5), or a tag that the (successfully-reached) BOW
 * says does not exist (AC-6/AC-12).
 *
 * Message extraction: parses `-m "..."` / -m '...' / --message=... from the
 * command string (handles escaped quotes within). Multiple `-m` flags are
 * joined (git's own behaviour: each becomes a paragraph). If no `-m`/
 * `--message` flag can be extracted at all (commit-from-file `-F`, heredoc
 * body, interactive editor flow, or a quoting shape this regex can't parse),
 * we cannot know the message — WARN and ALLOW rather than guess.
 *
 * Escape hatch: CLAUDE_DISABLE_BOW_REF=1 bypasses this hook entirely.
 *
 * Receives JSON on stdin: { tool: "Bash"|"PowerShell", tool_input: { command: "..." } }
 * Denies via: { hookSpecificOutput: { hookEventName: "PreToolUse",
 *               permissionDecision: "deny", permissionDecisionReason: "..." } }
 * Warns via the same shape with permissionDecision: "allow" + a reason.
 * (Same convention as claude-plan-guard.js / claude-version-guard.js.)
 *
 * Sits in .claude/settings.json's PreToolUse Bash and PowerShell matcher
 * arrays, appended AFTER claude-secret-guard.js (and before
 * claude-pre-push-check.js).
 */

'use strict';

const { execSync } = require('child_process');
const { connect } = require('./claude-db.js');
const { findItemByRef } = require('./claude-bow.js');

const ROOT = __dirname;
const ENFORCED_PATH_RE = /^(cmd|internal|data)\//;

// Bracketed tags: [tool.bow], [MOD-007], [engine.traffic]. Extraction is
// deliberately unopinionated about shape (AC-12: a malformed tag is still
// extracted, then denied at the BOW-lookup step as "unknown", not silently
// skipped as "not a tag").
const TAG_RE = /\[([^\[\]\n]+)\]/g;

// Matches -m "..." / -m '...' / --message="..." / --message '...', allowing
// escaped quotes inside the body. Multiple matches = multiple -m flags.
const MSG_FLAG_RE = /(?:-m|--message)(?:=|\s+)(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)')/g;

function deny(reason) {
  process.stdout.write(JSON.stringify({
    hookSpecificOutput: {
      hookEventName: 'PreToolUse',
      permissionDecision: 'deny',
      permissionDecisionReason: reason,
    },
  }));
  process.exit(0);
}

function warnAllow(reason) {
  process.stdout.write(JSON.stringify({
    hookSpecificOutput: {
      hookEventName: 'PreToolUse',
      permissionDecision: 'allow',
      permissionDecisionReason: reason,
    },
  }));
  process.exit(0);
}

function allowSilently() {
  process.exit(0);
}

/** Extract the commit message body from a `git commit ...` command string.
 *  Returns null if no -m/--message flag could be parsed at all. */
function extractMessage(command) {
  const parts = [];
  let m;
  MSG_FLAG_RE.lastIndex = 0;
  while ((m = MSG_FLAG_RE.exec(command))) {
    const raw = m[1] !== undefined ? m[1] : m[2];
    parts.push(raw.replace(/\\(["'\\])/g, '$1'));
  }
  if (parts.length === 0) return null;
  return parts.join('\n');
}

/** Extract [tag] contents from a commit message (may be empty array). */
function extractTags(message) {
  const tags = [];
  TAG_RE.lastIndex = 0;
  let m;
  while ((m = TAG_RE.exec(message))) {
    const tag = m[1].trim();
    if (tag) tags.push(tag);
  }
  return tags;
}

/** Read-only mysql2 connection (shared claude-db.js helper, GR#3/BUG-203). */
async function connectReadOnly() {
  return connect({ connectTimeout: 4000 });
}

/** Near-miss LIKE suggestions for an unknown tag (best-effort UX, not load-bearing;
 *  does not affect the pass/fail resolution, which is findItemByRef's alone). */
async function nearMisses(db, tag) {
  const [rows] = await db.query(
    'SELECT code, mkey FROM bow_items WHERE code LIKE ? OR mkey LIKE ? ORDER BY code LIMIT 5',
    [`%${tag}%`, `%${tag}%`]
  );
  return rows.map(r => r.mkey ? `${r.code} (${r.mkey})` : r.code);
}

async function main() {
  if (process.env.CLAUDE_DISABLE_BOW_REF === '1') {
    allowSilently();
    return;
  }

  let input = '';
  process.stdin.setEncoding('utf8');
  for await (const chunk of process.stdin) input += chunk;

  let command;
  try {
    // Strip a UTF-8 BOM (PowerShell pipes prepend one) before parsing.
    const data = JSON.parse(input.replace(/^\uFEFF/, ''));
    command = data?.tool_input?.command ?? '';
  } catch {
    // Unparseable stdin: fail-open hygiene posture (AC-10's spirit extends
    // here too — this hook never denies over a plumbing hiccup).
    allowSilently();
    return;
  }

  // Only intercept git commit commands (same shell-boundary regex as
  // claude-version-guard.js, avoiding false hits on "git commit" appearing
  // inside quoted string content).
  if (!/(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/.test(command)) {
    allowSilently();
    return;
  }

  if (command.includes('--amend')) {
    allowSilently();
    return;
  }

  // AC-3: staged-file inspection, never command-string path hints.
  let staged = '';
  try {
    staged = execSync('git diff --cached --name-only', {
      cwd: ROOT,
      encoding: 'utf8',
      timeout: 5000,
    });
  } catch {
    // Can't tell what's staged (e.g. outside a repo) — fail open.
    allowSilently();
    return;
  }

  const stagedFiles = staged.split('\n').map(f => f.trim()).filter(Boolean);
  const touchesEnforcedPath = stagedFiles.some(f => ENFORCED_PATH_RE.test(f));
  if (!touchesEnforcedPath) {
    // AC-2/AC-3: non-enforced-path commit — silent allow, no output at all.
    allowSilently();
    return;
  }

  const message = extractMessage(command);
  if (message === null) {
    warnAllow(
      'ℹ️  BOW REF CHECK: could not extract the commit message from this command ' +
      '(commit-from-file / -F / heredoc / editor flow?). This commit touches cmd/, ' +
      'internal/, or data/, which normally requires a [mkey] BOW reference (tool.bow, ' +
      'MOD-007) — but this hook only understands `-m "..."` / `--message=...` shapes, ' +
      'so it cannot verify. Allowing (fail-open hygiene posture). Please confirm the ' +
      'message carries a valid [mkey] tag yourself.'
    );
    return;
  }

  const tags = extractTags(message);
  if (tags.length === 0) {
    deny(
      '🛑 BOW REF CHECK: this commit touches cmd/, internal/, or data/ but the commit ' +
      'message carries no [mkey] BOW reference.\n\n' +
      'M0-ENG §5\'s commit convention requires every commit touching engine/UI/data code ' +
      'to carry a traceable BOW link. Add a tag naming the item you\'re working, e.g.:\n' +
      '  git commit -m "[tool.bow] add ref-check hook"\n' +
      '  git commit -m "[MOD-007] add ref-check hook"\n\n' +
      'Look up the right item: node claude-bow.js list  /  node claude-bow.js show <code>\n' +
      'Emergency bypass (use deliberately, not routinely): CLAUDE_DISABLE_BOW_REF=1'
    );
    return;
  }

  let db;
  try {
    db = await connectReadOnly();
  } catch (err) {
    // AC-10: DB unreachable at PreToolUse time — fail OPEN with a visible warning.
    warnAllow(
      `⚠️  BOW REF CHECK: metro MariaDB is unreachable (${err.message}) — cannot validate ` +
      `[${tags.join('], [')}] against the live BOW. Allowing (fail-open, per tool.bow AC-10). ` +
      'Please confirm these refs are valid once the DB is back, or run ' +
      '`node claude-bow.js ref <code> <hash>` manually.'
    );
    return;
  }

  try {
    const unknown = [];
    for (const tag of tags) {
      // eslint-disable-next-line no-await-in-loop
      const item = await findItemByRef(db, tag);
      if (!item) unknown.push(tag);
    }

    if (unknown.length > 0) {
      const suggestionLines = [];
      for (const tag of unknown) {
        // eslint-disable-next-line no-await-in-loop
        const suggestions = await nearMisses(db, tag);
        suggestionLines.push(
          `  [${tag}] — no match. ` +
          (suggestions.length ? `Did you mean: ${suggestions.join(', ')}?` : 'No close matches found.')
        );
      }
      deny(
        `🛑 BOW REF CHECK: unknown BOW reference(s) in commit message: [${unknown.join('], [')}]\n\n` +
        `${suggestionLines.join('\n')}\n\n` +
        'Every [mkey]/[CODE] tag must resolve to a live item (node claude-bow.js show <ref>). ' +
        'Fix the tag, or create the item first: node claude-bow.js add <type> "title".\n' +
        'Emergency bypass (use deliberately, not routinely): CLAUDE_DISABLE_BOW_REF=1'
      );
      return;
    }

    // All tags resolved — allow silently.
    allowSilently();
  } catch (err) {
    // A query blew up after we successfully connected — treat the same as
    // DB-unreachable (AC-10's fail-open posture covers any BOW-lookup
    // failure, not just the initial connect).
    warnAllow(
      `⚠️  BOW REF CHECK: BOW lookup failed unexpectedly (${err.message}) — allowing ` +
      '(fail-open, per tool.bow AC-10). Please confirm the ref(s) manually.'
    );
  } finally {
    try { await db.end(); } catch { /* ignore */ }
  }
}

main().catch(() => {
  // Absolute last resort: never let an unexpected throw brick a commit.
  // This hook's entire posture is fail-open (AC-10) — even an internal bug
  // here must not deny.
  process.exit(0);
});
