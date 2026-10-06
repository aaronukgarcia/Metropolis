// Module key: tool.codenamecontentscan (see code.json; GUID 8cb8f51a-eaa8-41b2-bc2e-58b2f658bd3b)
// Spec ref: GR#22

/**
 * claude-codename-content-scan.js — commit-msg content scan core (FEAT-046,
 * BOW mkey: tool.codenamehook / code.json tool.codenameguard).
 *
 * The enforcing, git-level second layer of GR#22 (Codename Discipline): this
 * module inspects `git diff --cached`, added lines only, using the SAME
 * shared fragment-assembled pattern set the PreToolUse `claude-codename-
 * guard.js` already uses (claude-codename-patterns.js — GR#3, single source
 * of truth; this module defines NO pattern of its own). It is required from
 * githooks/commit-msg (installed at `.git/hooks/commit-msg` alongside
 * FEAT-045's identity check — see that file's header for the hook-point
 * evidence this item's own AC-1 independently confirmed for content-scan
 * visibility specifically).
 *
 * ============================================================================
 * WHY commit-msg, NOT pre-commit (AC-1).
 * ============================================================================
 * Live evidence gathered for this item (throwaway repo, real git, out-of-
 * repo scratch directory, never this repo — see claude-codenamehook.test.js's
 * "AC-1 evidence" tests): a `pre-commit` and a `commit-msg` hook, each
 * running `git diff --cached --unified=0` and reporting the diff's length,
 * were installed together in the same throwaway repo. For a plain `git
 * commit`, both fired and observed byte-identical staged-diff content. For a
 * real `git merge --no-ff`, ONLY `commit-msg` fired — `pre-commit` produced
 * no output at all. Since a merge can introduce forbidden content exactly as
 * easily as a plain commit, `pre-commit` is structurally insufficient here,
 * for the same reason FEAT-045's AC-1 rejected it for identity checking.
 *
 * ============================================================================
 * SCOPE (AC-2): staged diff, added lines only. Never the working tree, never
 * unstaged changes. Scanning whole files would block an unrelated fix in a
 * file that still carries a pre-existing violation elsewhere; scanning
 * removed lines would block the very commit that deletes a violation.
 * ============================================================================
 *
 * ============================================================================
 * FAIL-CLOSED (AC-7) — same posture as githooks/commit-msg's identity check,
 * the OPPOSITE of the demoted claude-author-guard.js.
 * ============================================================================
 * A `git diff --cached` invocation failure, or the shared pattern module
 * throwing/failing to load, is NOT caught here — it propagates to the
 * caller (githooks/commit-msg's main()), whose job is to turn any thrown
 * error from this module into a denied commit. This is a CONTENT-SECURITY
 * check, not an identity check: a codename leak into a public repo cannot be
 * withdrawn once pushed, unlike an identity mismatch which costs a human
 * seconds to correct — so an unchecked commit must never be treated as a
 * clean one.
 *
 * ============================================================================
 * DISCLOSED LIMITATIONS — stated, not glossed over.
 * ============================================================================
 * (1) AC-10: `cherry-pick`, `revert`, and `am` do not invoke `commit-msg` at
 *     all on this machine's git (ASM-386, established for identity checking
 *     in githooks/commit-msg, confirmed to be a property of git's own hook
 *     dispatch — not of what a given commit-msg script does — so it applies
 *     identically here). This module's real guarantee is "content committed
 *     via `git commit` or `git merge` cannot introduce a GR#22 violation,"
 *     not "...via any commit-creating verb."
 * (2) AC-11: an interactively-composed commit MESSAGE BODY (not passed via
 *     `-m`) is out of scope for this module — it inspects `git diff
 *     --cached`, never the commit-msg hook's own `$1` argument. The
 *     PreToolUse guard's command-text scan covers `-m`/heredoc arguments
 *     that appear in a Bash/PowerShell command string but cannot see an
 *     editor-composed message either — this is a real, disclosed gap
 *     covered by NEITHER layer today, escalated to Bill/Aaron per the
 *     acceptance file (docs/planning/acceptance/tool.codenamehook.md),
 *     not resolved unilaterally here.
 *
 * BUG-183 (fixed here): a forbidden pattern landing ONLY in a new, renamed,
 * or copied file's PATH — never in file content or the commit message —
 * used to bypass this enforcing layer, since it scanned added hunk-body
 * content exclusively. The sibling PreToolUse `claude-codename-guard.js`
 * already scanned `splitDiffSections()`'s `pathHeaderLines` output for the
 * same class of gap (BUG-137); this module now does the same, via the same
 * shared `scan()` — no second detection path invented (GR#3).
 */

'use strict';

const { execFileSync } = require('child_process');
const patterns = require('./claude-codename-patterns.js');
const { splitDiffSections } = require('./claude-codename-diff.js');
const { LOCKFILE_BASENAMES, isKnownLockfileBasename } = require('./claude-codename-guard.js');
const { NPM_INTEGRITY_HASH_RE } = require('./claude-codename-patterns.js');

/** Runs `git diff --cached --unified=0` and returns the added-line content
 * (never the working tree, never unstaged changes — AC-2). Throws on any
 * git invocation failure; the caller is responsible for the fail-closed
 * decision (AC-7) — this function does not swallow the error itself.
 *
 * BUG-182: the added-line content is derived from the diff by POSITION
 * (claude-codename-diff.js's splitDiffSections — inside a '@@' hunk body or
 * not), never by re-testing a line's own text against the '+++ b/<path>'
 * header shape. The previous `l.startsWith('+') && !l.startsWith('+++')`
 * filter here silently dropped any genuine added line whose own content
 * began with two literal '+' characters, since git emits that as a '+'
 * marker followed by '++...' — textually identical to the header line. See
 * claude-codename-diff.js's header for the full mechanism.
 *
 * TEST-ONLY ESCAPE HATCH: CLAUDE_CODENAME_SCAN_FORCE_ERROR=1 makes this
 * throw deliberately instead of invoking git at all — same shape as
 * claude-author-identity.js's CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR, existing
 * SOLELY so AC-7's fail-closed test can prove githooks/commit-msg's
 * behaviour without needing to actually break git on the test machine. Never
 * read anywhere except here; no effect on real-world behaviour when unset. */
function stagedAddedLines() {
  if (process.env.CLAUDE_CODENAME_SCAN_FORCE_ERROR === '1') {
    throw new Error('CLAUDE_CODENAME_SCAN_FORCE_ERROR forced failure (test-only escape hatch)');
  }
  // BUG-185: `--no-color` is passed explicitly so this invocation is immune
  // to color.ui / color.diff being forced to 'always' in ANY applicable git
  // config (local repo, global, or system) — without it, git prepends ANSI
  // escape sequences to every diff line including the 'diff --git ' and
  // '@@ ' marker lines that splitDiffSections() matches by raw text at the
  // true start of the line, which silently blinds the classifier (inHunk
  // never flips true) and drops genuinely forbidden added content entirely.
  // See claude-codename-diff.js for the defense-in-depth stripping added as
  // a second layer.
  const diff = execFileSync('git', ['diff', '--cached', '--unified=0', '--no-color'], {
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    stdio: ['ignore', 'pipe', 'ignore'],
  });
  return splitDiffSections(diff).addedLines;
}

/** BUG-183: same `git diff --cached --unified=0 --no-color` invocation as
 * stagedAddedLines(), but returns the diff's `pathHeaderLines` half of
 * splitDiffSections()'s output — the per-file header lines identifying a
 * new, renamed, or copied file's PATH (BUG-137's shape, extracted by the
 * same shared claude-codename-diff.js module claude-codename-guard.js
 * already consumes for this exact purpose). A path-only violation (a
 * forbidden pattern in a filename, never in file content or the commit
 * message) reaches neither stagedAddedLines() above nor the commit message,
 * so without this the enforcing layer could not see it at all — the gap
 * this item exists to close. Same fail-closed contract as
 * stagedAddedLines(): git invocation failure propagates to the caller. */
function stagedPathHeaderLines() {
  if (process.env.CLAUDE_CODENAME_SCAN_FORCE_ERROR === '1') {
    throw new Error('CLAUDE_CODENAME_SCAN_FORCE_ERROR forced failure (test-only escape hatch)');
  }
  const diff = execFileSync('git', ['diff', '--cached', '--unified=0', '--no-color'], {
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    stdio: ['ignore', 'pipe', 'ignore'],
  });
  return splitDiffSections(diff).pathHeaderLines;
}

/** BUG-416: returns the diff's `sections` array (per-file breakdown of added
 * lines) from splitDiffSections(). This allows scanStagedDiff() to apply
 * file-specific filtering: integrity-hash-shaped lines are scanned normally in
 * most files, but skipped (only for integrity-hash shape AND in a known
 * lockfile basename) to prevent false positives from machine-generated npm
 * lockfile hashes. The same fail-closed contract as stagedAddedLines(): git
 * invocation failure propagates to the caller. */
function stagedDiffSections() {
  if (process.env.CLAUDE_CODENAME_SCAN_FORCE_ERROR === '1') {
    throw new Error('CLAUDE_CODENAME_SCAN_FORCE_ERROR forced failure (test-only escape hatch)');
  }
  const diff = execFileSync('git', ['diff', '--cached', '--unified=0', '--no-color'], {
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
    stdio: ['ignore', 'pipe', 'ignore'],
  });
  return splitDiffSections(diff).sections;
}

/** Core check: scans the staged diff's added lines against the shared GR#22
 * pattern set (claude-codename-patterns.js — GR#3, no second copy). Returns
 * an array of human-readable hit descriptions (empty when clean). Throws if
 * `git diff` itself fails, or if the shared pattern module's scan() throws
 * (AC-4's "lazy implementation" trap: there is no fallback pattern list
 * here — a broken shared module means this scan cannot verify anything, so
 * it must not silently report "clean").
 *
 * BUG-416: applies file-specific filtering to the added lines before
 * scanning: integrity-hash-shaped lines are skipped ONLY in known lockfile
 * basenames (package-lock.json, npm-shrinkwrap.json, yarn.lock, pnpm-lock.yaml),
 * preventing false positives from machine-generated npm integrity hashes. In all
 * other files, integrity-hash-shaped lines are scanned normally (a crafted line
 * carrying the reference title's numbered form in source code is caught). */
function scanStagedDiff() {
  const hits = [];

  // BUG-416: scan added lines with file-specific filtering. Integrity-hash
  // lines are skipped ONLY for known lockfile basenames, not in arbitrary
  // source files where such a line would be a real smuggle attempt.
  const sections = stagedDiffSections();
  const filteredLines = [];
  for (const section of sections) {
    const isLockfile = isKnownLockfileBasename(section.filePath);
    for (const line of section.addedLines) {
      // Only skip integrity-hash lines if this is a known lockfile.
      if (isLockfile && NPM_INTEGRITY_HASH_RE.test(line)) continue;
      filteredLines.push(line);
    }
  }
  patterns.scan(filteredLines.join('\n'), 'staged content (added lines)', hits);

  // BUG-183: also scan the new/renamed/copied file paths themselves, same
  // shared pattern set, same fail-closed posture — a violation living only
  // in a filename must deny the commit exactly like one living in content.
  const pathHeaders = stagedPathHeaderLines();
  patterns.scan(pathHeaders, 'staged file path (new, renamed, or copied file)', hits);

  return hits;
}

module.exports = {
  stagedAddedLines,
  stagedPathHeaderLines,
  stagedDiffSections,
  scanStagedDiff,
};
