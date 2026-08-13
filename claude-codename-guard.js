/**
 * PreToolUse hook — codename guard (BOW mkey: tool.codenameguard).
 *
 * Enforces GOLDEN RULE #22 (Codename Discipline): the reference title this
 * project's design docs compare against is 'Blue', and only 'Blue'. Its real
 * name, its abbreviations and its numbered sequel form must never be written
 * into git — not in code, data, docs, plans, comments, commit messages, or
 * branch names.
 *
 * WHY MECHANICAL. The repo is intended to go public. A name written into git
 * is a disclosure that cannot be withdrawn afterwards: clones, caches and
 * indexers outlive any later edit, which is exactly why the existing
 * occurrences were removed by rewriting history rather than by editing the
 * working tree. A rule that depends on everyone remembering it, across a
 * dozen concurrent agents, will be broken — so it is checked instead.
 *
 * WHY THE PATTERNS ARE ASSEMBLED FROM FRAGMENTS BELOW, which looks like
 * obfuscation and is not: this file lives IN git. If it contained the
 * forbidden strings as plain literals in order to search for them, the guard
 * would be the single largest violation of the rule it enforces — and it
 * would flag itself on every commit. The same trap catches a well-meaning
 * comment explaining a rename ("renamed <real name> to 'Blue'"), which is why
 * the rule covers comments and commit messages and not just code. Fragments
 * are joined at runtime so no forbidden literal ever appears on disk.
 *
 * WHAT IT CHECKS, on `git commit` and `git push`:
 *   1. Staged content (git diff --cached), added lines only — an existing
 *      violation elsewhere in a file must not block an unrelated fix.
 *   2. The commit message, including -m arguments and heredoc bodies. This is
 *      the likeliest place to slip: the message describing the removal is the
 *      easiest thing to write the name into.
 *   3. The current branch name.
 *
 * Ambiguity is handled deliberately. The bare two-letter abbreviation is NOT
 * matched: it appears innocently in ordinary technical prose, and a guard that
 * fires on false positives gets disabled within a day — a failure mode this
 * project has now catalogued three times (SEC-026, and twice since). The
 * numbered forms ARE matched, since those are unambiguous.
 *
 * Fail-CLOSED, like claude-plan-guard.js and unlike claude-dispatch-guard.js.
 * The cost asymmetry decides it: a false block is a minor annoyance that a
 * human resolves in seconds, while a miss is permanent and public. If this
 * guard cannot do its job it must not pretend the commit is clean.
 *
 * Deliberate disable: CLAUDE_DISABLE_CODENAME_GUARD=1. Use it to commit a
 * genuine false positive, never to push a real one.
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 *
 * FEAT-046 (GR#3): this guard's PATTERNS/isLowerLetter/scan() mechanism now
 * lives in claude-codename-patterns.js, required below UNCHANGED — the same
 * shared module also backs the new enforcing `commit-msg` content scan
 * (claude-codename-content-scan.js). This file remains the PreToolUse
 * ADVISORY layer (fires earlier, on command text/branch name/staged diff,
 * before a commit is even attempted) — retained exactly as before, not
 * replaced or demoted by the new git-level enforcement layer.
 */

'use strict';

const { execSync } = require('child_process');
const fs = require('fs');
const { buildBareGitVerbTriggerRegex } = require('./claude-git-commit-trigger.js');
const { PATTERNS, isLowerLetter, lineMatches, lineMatchesWithBoundary, scan } = require('./claude-codename-patterns.js');
const { splitDiffSections } = require('./claude-codename-diff.js');

// BUG-123 (2026-08-12): this guard's trigger used to be the bare
// `/\bgit\s+(commit|push)\b/`, which does not tolerate ANY global option
// between `git` and the verb — so `git -c user.email=... commit` (or
// `-c commit.gpgsign=false`, or `--git-dir=...`) slipped past this
// FAIL-CLOSED GR#22 guard entirely, unscanned. Built from the same shared
// option-run grammar the sibling commit-only guards now use (GR#3 — see
// claude-git-commit-trigger.js's header); this guard keeps its original bare
// word-boundary shape (no shell-boundary anchoring), unchanged in every other
// respect.
const GIT_COMMIT_OR_PUSH_RE = buildBareGitVerbTriggerRegex('commit|push');

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

// lineMatches/lineMatchesWithBoundary/scan now live in
// claude-codename-patterns.js (FEAT-046, GR#3) — required at the top of this
// file, unchanged. See that module for the BUG-140/BUG-144 boundary-logic
// rationale previously documented inline here.

function main() {
  if (process.env.CLAUDE_DISABLE_CODENAME_GUARD === '1') allow();

  let payload;
  try {
    payload = JSON.parse(fs.readFileSync(0, 'utf8') || '{}');
  } catch {
    // Unparsable hook input is not evidence of a clean commit, but it is also
    // not this guard's call to make — the shell will fail on its own.
    allow();
  }

  const cmd = String((payload.tool_input || {}).command || '');
  if (!GIT_COMMIT_OR_PUSH_RE.test(cmd)) allow();

  const hits = [];

  // 2 & 3 first: they need no subprocess and cover the likeliest slip.
  scan(cmd, 'the git command (message text or arguments)', hits);

  try {
    const branch = execSync('git rev-parse --abbrev-ref HEAD', {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
    scan(branch, `the branch name "${branch}"`, hits);
  } catch {
    /* detached HEAD or no commits yet — nothing to check */
  }

  // 1: staged ADDED lines only. Scanning whole files would block an unrelated
  // fix in a file that still carries an occurrence somewhere else.
  try {
    // BUG-185: `--no-color` makes this invocation immune to color.ui /
    // color.diff forced to 'always' in any applicable git config — without
    // it, ANSI escape sequences prepended to the 'diff --git '/'@@ ' marker
    // lines defeat splitDiffSections()'s raw-text-at-start-of-line match,
    // silently blinding both this guard and the commit-msg hook at once.
    const diff = execSync('git diff --cached --unified=0 --no-color', {
      encoding: 'utf8',
      maxBuffer: 64 * 1024 * 1024,
      stdio: ['ignore', 'pipe', 'ignore'],
    });
    // BUG-182: added-line content is classified by POSITION (inside a '@@'
    // hunk body or not — claude-codename-diff.js's splitDiffSections), never
    // by re-testing a line's own text against the '+++ b/<path>' header
    // shape. The old `l.startsWith('+') && !l.startsWith('+++')` filter here
    // silently dropped a genuine added line whose own content began with two
    // literal '+' characters — textually identical to the header line — and
    // this exact same trap independently existed in
    // claude-codename-content-scan.js, so BUG-182 fixes both call sites by
    // routing them through this one shared function (GR#3).
    const { addedLines, pathHeaderLines } = splitDiffSections(diff);
    scan(addedLines, 'staged content (added lines)', hits);

    // BUG-137: a forbidden word appearing ONLY in a new/renamed/copied file's
    // PATH — never in file content or the commit message — bypassed the
    // content-only scan above, since a unified diff's path-header lines
    // ('+++ b/<path>', '--- a/<path>', 'rename to/from <path>', 'copy
    // to/from <path>') all start with something other than a plain '+' and
    // were excluded outright rather than stripped and scanned.
    scan(pathHeaderLines, 'staged file path (new, renamed, or copied file)', hits);
  } catch (err) {
    deny(
      `🛑 CODENAME GUARD (GR#22): could not read the staged diff to check it ` +
        `— ${err.message}\n\nFailing closed: an unchecked commit is not a clean ` +
        `one, and a name written into git cannot be withdrawn once the repo is ` +
        `public. Resolve the git error and retry.`
    );
  }

  if (hits.length) {
    deny(
      `🛑 CODENAME GUARD — GOLDEN RULE #22 violation (${hits.length}):\n\n` +
        hits.map((h) => `  - ${h}`).join('\n') +
        `\n\nThe reference title is 'Blue', and only 'Blue'. Its real name, its ` +
        `abbreviations and its numbered form must never enter git — code, data, ` +
        `docs, comments, commit messages or branch names.\n\n` +
        `Rewrite to say 'Blue' or "the reference title". Where a sentence only ` +
        `reads sensibly with the real name, rewrite the sentence: the reference ` +
        `is being renamed, not deleted, so keep the technical point.\n\n` +
        `Note the trap this guard exists to catch — do NOT write a commit ` +
        `message or comment EXPLAINING the rename that quotes the old name. ` +
        `The explanation would itself be the exposure.\n\n` +
        `Deliberate bypass (genuine false positive only): ` +
        `CLAUDE_DISABLE_CODENAME_GUARD=1`
    );
  }

  allow();
}

// require.main === module guard (BUG-123, same testability pattern already
// used by claude-secret-guard.js / claude-version-guard.js /
// claude-plan-guard.js): when run directly as the hook, behaviour is
// unchanged — main() still runs unconditionally below. When require()'d by a
// test harness, main() is never called (so stdin is never touched) and the
// trigger regex is exported for direct, unit-level testing.
if (require.main === module) {
try {
  main();
} catch (err) {
  // Fail closed — see the header on cost asymmetry.
  deny(
    `🛑 CODENAME GUARD (GR#22) internal error: ${err.message}\n\n` +
      `Failing closed deliberately. Bypass only if you have checked by hand: ` +
      `CLAUDE_DISABLE_CODENAME_GUARD=1`
  );
}
} else {
  module.exports = {
    GIT_COMMIT_OR_PUSH_RE,
    // BUG-061 (tool.bow `redact` subcommand): exported so claude-bow.js can
    // reuse this guard's own fragment-assembled pattern set and boundary
    // logic verbatim (GR#3 single source of truth) instead of re-deriving a
    // second copy of the forbidden-pattern list — a drifted second copy is
    // exactly the kind of gap that would let a real name back into the BOW
    // even after this guard blocks it from reaching git. Nothing here adds a
    // new literal to this file: PATTERNS is still fragment-assembled above,
    // isLowerLetter is the same boundary-classification helper the guard's
    // own scan() uses.
    PATTERNS,
    isLowerLetter,
  };
}
