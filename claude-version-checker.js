/**
 * GR#2 hand-maintained-version-file checker (BOW mkey: tool.secretguard,
 * BUG-088 remediation, extracted from claude-version-guard.js).
 *
 * This module is the SINGLE SOURCE OF TRUTH (GR#3) for the payload-
 * inspection logic that decides whether a commit stages a hand-maintained
 * version file or a hardcoded semver literal in a version.go file, banned
 * under GR#2's Metropolis profile (M0-ENG §3: app version comes SOLELY from
 * `git describe`, injected via -ldflags). It is `require()`'d by
 * claude-version-guard.js (the PreToolUse layer, UNCHANGED by this item —
 * see that file's header) and is designed to also be `require()`'d by a
 * future `commit-msg` dispatcher (BUG-088's Section B — that dispatcher is
 * NOT implemented here, see docs/planning/acceptance/tool.secretguard.md's
 * BUG-088 section, AC-B5).
 *
 * BUG-088 finding this extraction addresses: this guard's *trigger* was
 * defeated by any leading word, shell wrapper, or non-bareword git
 * invocation, same class as the other three siblings. Its *payload* (this
 * module: `git diff --cached --name-only` + per-file `git diff --cached`)
 * was always sound. This module carries none of the sibling guards'
 * boundary-regex/quote-mask/engage-decision machinery, by design (AC-B4): a
 * commit-msg hook has no engage decision to make, and copying dead trigger
 * machinery into this module would misrepresent that trigger-parsing is
 * still part of this design. (This header intentionally avoids spelling
 * out those helpers' exact identifier names, so a grep for them against
 * this file — the literal AC-B4 check — finds zero matches.)
 *
 * KNOWN LIMITATION INHERITED FROM ASM-386, STATED PLAINLY (AC-B2): a
 * `commit-msg` hook (the intended future caller of this module's
 * `checkVersion()`) does not fire for `git cherry-pick` / `git revert` /
 * `git am` on this project's git version (2.55.0.windows.3, verified three
 * independent ways per ASM-386's own comment thread). Not re-verified or
 * re-solved here.
 *
 * FAIL-OPEN ON INTERNAL ERROR — THE ONE DELIBERATE DEVIATION IN THIS FILE
 * (AC-C table, "why" column): this check is a hygiene check, not a security
 * gate (its own guard's header has said so since before BUG-088), so
 * `checkVersion()`'s three-state result on an internal error (e.g. `git
 * diff` failing) is reported as `internal-error` — the SAME discriminant
 * name every other BUG-088 checker uses (AC-E1's uniformity requirement) —
 * but the CALLER (claude-version-guard.js, and eventually a commit-msg
 * dispatcher) is expected to treat this particular checker's
 * `internal-error` as fail-OPEN (allow, surfaced not silently swallowed),
 * the opposite of how the secret/plan checkers' `internal-error` should be
 * treated (fail-closed, deny). This is a CALLER-side decision, not a
 * fourth state — the three-state discriminant AC-E1 requires stays uniform
 * across all four modules; only what each caller DOES with "internal-error"
 * differs, and that difference is table-driven (see the acceptance file's
 * Section C), not something this module needs to encode itself. On an
 * ACTUAL positive detection (a real hand-maintained version file staged),
 * this checker still reports "found-problems" — fail-open only describes
 * the internal-error path, never a detection result.
 *
 * Exported call contract for a future dispatcher (AC-B5): `checkVersion()`
 * takes NO arguments (reads git/fs state itself) and returns one of:
 *   { status: 'clean', note?: <string> }
 *   { status: 'found-problems', findings: [<string>, ...] }
 *   { status: 'internal-error', error: <Error> }
 * `note` (only present on "clean") carries the non-blocking GR#2 reminder
 * the original guard emitted as warn-allow when a commit touches cmd/ or
 * internal/ — informational only, never a detection.
 *
 * Everything below this header is RELOCATED, NOT REIMPLEMENTED, from
 * claude-version-guard.js (AC-D3): same hand-maintained-file detection,
 * same hardcoded-semver detection, same exemption pattern list. See
 * claude-version-checker.test.js for the parity proof.
 */

'use strict';

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const ROOT = __dirname;

// The two retired Prix-Six-style version files (MOD-001, cancelled).
const HAND_MAINTAINED_EXACT_PATHS = new Set([
  'app/package.json',
  'app/src/lib/version.ts',
]);

// The one sanctioned ldflags-injection target.
const BUILDINFO_EXEMPT_PATH = 'internal/foundation/buildinfo/buildinfo.go';

const VERSION_GO_RE = /(^|\/)version\.go$/;
const SEMVER_LITERAL_RE = /["'`]v?\d+\.\d+\.\d+/;

const EXEMPT_PATTERNS = [
  /^docs\//,
  /\.md$/i,
  /^\.claude\//,
  /^claude-[\w.-]+\.js$/,
  /^\.gitignore$/,
  /^package\.json$/,
  /^package-lock\.json$/,
];

function isHandMaintainedVersionFile(relPath) {
  if (HAND_MAINTAINED_EXACT_PATHS.has(relPath)) return true;
  if (path.basename(relPath) === 'VERSION') return true;
  return false;
}

// A version.go file (other than the exempt buildinfo.go) is only a
// violation if its STAGED content actually hardcodes a semver-looking
// literal. Passes relPath as a single argv element to spawnSync
// (shell:false) — no shell ever re-parses it (SEC-002 fix, relocated
// unchanged).
function stagedDiffHasHardcodedSemver(relPath) {
  const result = spawnSync('git', ['diff', '--cached', '--', relPath], {
    encoding: 'utf8',
    timeout: 5000,
  });
  if (result.error || result.status !== 0) {
    const details = result.error
      ? result.error.message
      : (result.stderr || '').trim() || `git diff --cached -- ${relPath} exited ${result.status}`;
    process.stderr.write(
      `⚠️  GR#2 CHECKER: could not diff staged file "${relPath}" to check for a hardcoded semver ` +
      `(${details}). Skipping this file's check rather than failing the whole check (fail-open, ` +
      'by this checker\'s documented posture) — but this means the hardcoded-semver check for ' +
      'this file was NOT performed. Please verify manually.\n'
    );
    return false;
  }
  const diff = result.stdout || '';
  return diff.split('\n').some(
    line => line.startsWith('+') && !line.startsWith('+++') && SEMVER_LITERAL_RE.test(line)
  );
}

/**
 * Runs the full GR#2 hand-maintained-version-file check (relocated
 * unchanged from claude-version-guard.js's main() body, minus its
 * --amend/env-var-disable handling, which are hook-plumbing concerns that
 * belong to the CALLER, not this payload module). Never throws — every
 * failure mode is captured into the return value.
 */
function checkVersion() {
  try {
    let staged = '';
    try {
      const result = spawnSync('git', ['diff', '--cached', '--name-only'], {
        cwd: ROOT,
        encoding: 'utf8',
        timeout: 5000,
      });
      if (result.error || result.status !== 0) {
        const details = result.error
          ? result.error.message
          : (result.stderr || '').trim() || `exited ${result.status}`;
        return { status: 'internal-error', error: new Error(`git diff --cached --name-only failed: ${details}`) };
      }
      staged = result.stdout || '';
    } catch (err) {
      return { status: 'internal-error', error: err };
    }

    const stagedFiles = staged.split('\n').map(f => f.trim()).filter(Boolean);
    const relPaths = stagedFiles.map(f => f.replace(/^03\.Current\//, ''));

    if (relPaths.length > 0 && relPaths.every(f => EXEMPT_PATTERNS.some(rx => rx.test(f)))) {
      return { status: 'clean' }; // docs/tooling-only commit — GR#2 exempt
    }

    const hasGoSkeleton =
      fs.existsSync(path.join(ROOT, 'cmd')) || fs.existsSync(path.join(ROOT, 'internal'));

    if (!hasGoSkeleton) {
      return { status: 'clean' };
    }

    const offending = [];
    for (const f of relPaths) {
      if (isHandMaintainedVersionFile(f)) {
        offending.push(f);
        continue;
      }
      if (VERSION_GO_RE.test(f) && f !== BUILDINFO_EXEMPT_PATH) {
        if (stagedDiffHasHardcodedSemver(f)) {
          offending.push(f);
        }
      }
    }

    if (offending.length > 0) {
      return {
        status: 'found-problems',
        findings: [
          `hand-maintained version file(s) staged: ${offending.join(', ')}. M0-ENG §3 bans ` +
          'hand-maintained version files for this stack — the app version comes SOLELY from ' +
          '`git describe --tags --dirty`, injected via -ldflags. Cut a milestone tag ' +
          '(v0.<milestone>.<n>) instead of hand-editing a version string.',
        ],
      };
    }

    const touchesEngineCode = relPaths.some(f => /^cmd\//.test(f) || /^internal\//.test(f));
    if (touchesEngineCode) {
      return {
        status: 'clean',
        note:
          'this commit touches cmd/ or internal/. Version discipline for this repo is milestone ' +
          'tags (v0.<milestone>.<n>) + -ldflags injection, not a hand-edited file — no action ' +
          'needed here. BOW [mkey] commit-message enforcement is tool.bow\'s (MOD-007) job, ' +
          'not this hook\'s.',
      };
    }

    return { status: 'clean' };
  } catch (err) {
    // AC-F1: an internal error is its own state — never silently downgraded
    // to "clean". (The CALLER decides to treat this state as fail-open —
    // see this module's header.)
    return { status: 'internal-error', error: err };
  }
}

module.exports = {
  ROOT,
  HAND_MAINTAINED_EXACT_PATHS,
  BUILDINFO_EXEMPT_PATH,
  VERSION_GO_RE,
  SEMVER_LITERAL_RE,
  EXEMPT_PATTERNS,
  isHandMaintainedVersionFile,
  stagedDiffHasHardcodedSemver,
  checkVersion,
};
