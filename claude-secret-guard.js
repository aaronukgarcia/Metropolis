/**
 * PreToolUse hook — secret & hardcoding pre-commit guard (BOW mkey: tool.secretguard).
 *
 * Spec: GR#11 (Pre-Commit Security Review); GR#15 (Validators Derive From
 * Data); M0-ENG §5 (hooks); BUG-088 (this file's trigger-only remediation)
 *
 * GR#11 requires a mandatory security threat model before every commit; this
 * hook mechanises the secrets half of that review so it no longer depends on
 * lead vigilance. It intercepts `git commit` and scans STAGED content only
 * for private-key blocks, certificate/keystore files, api-key/token
 * patterns, connection-string passwords, high-entropy literals, and GR#15
 * hardcoding smells — see claude-secret-checker.js for the full detector
 * list and the allowlist matching rules.
 *
 * BUG-088 (2026-08-11): this hook's *trigger* — the bare `GIT_COMMIT_RE.test
 * (command)` check below, deciding whether to engage at all — was defeated
 * by any leading word (`env git commit`), any shell wrapper (`bash -c
 * "git commit ..."`), or any non-bareword git invocation (`git.exe commit`,
 * a quoted full path). Its *payload* (the actual scan) was always sound —
 * real git/filesystem state, read via `spawnSync`, never re-parsed from the
 * command string. Per docs/planning/acceptance/tool.secretguard.md's
 * BUG-088 section, this file KEEPS its blocking PreToolUse posture unchanged
 * (a secret false positive has a cheap, documented remedy —
 * claude-secret-guard.allow.json — unlike identity's false positives, and a
 * missed secret on a public repo is this item's own "worst outcome in the
 * class"). The payload logic itself has been extracted, UNCHANGED, into
 * claude-secret-checker.js — a standalone, requireable module that is now
 * the single source of truth for the scan (GR#3), reachable by this guard
 * AND by a future `commit-msg` hook dispatcher (out of scope for this file
 * — see the acceptance file's Section B). The real, durable fix for
 * BUG-088's trigger defect is that future `commit-msg` hook, which has no
 * text-parsing engage decision to make at all; this file's own trigger check
 * is UNCHANGED by this item (still the original, best-effort boundary-regex
 * check it has always been — see the trigger-comment correction further
 * below for a P0 finding on a prior pass of this refactor that briefly
 * ported quote-masking into this check, which has now been reverted)
 * because it still needs SOME engage decision at the PreToolUse layer until
 * that hook exists.
 *
 * KNOWN LIMITATION INHERITED FROM ASM-386 (AC-B2, stated here too since this
 * guard is the PreToolUse half of the same finding): even once a commit-msg
 * hook exists, it will not fire for `git cherry-pick` / `git revert` /
 * `git am` on this project's git version — verified independently for
 * claude-author-guard.js's identical situation, not re-verified here.
 *
 * A finding is suppressed only when it matches claude-secret-guard.allow.json
 * exactly (an allowlisted path skips the whole file; an allowlisted pattern
 * must match the extracted candidate string exactly / by anchored regex —
 * never a loose substring match, see AC-12).
 *
 * Fail-CLOSED, scoped to commits only (same posture as claude-plan-guard.js,
 * and the same lead-review lesson it already learned): this guard's entire
 * job is to stop a secret-bearing commit from landing, so an internal error
 * while scanning a `git commit` (git itself failing, a malformed allowlist
 * file, an unexpected exception) results in a DENY rather than an allow. But
 * that fail-closed posture must NOT bleed into non-commit commands — if
 * stdin is unparseable, we fall back to a raw substring sniff: deny only if
 * the raw text looks like it might be a `git commit`, otherwise allow
 * immediately. A hook-input hiccup must never brick `git status`, `npm
 * install`, or any other unrelated Bash command.
 *
 * To disable deliberately: set env var CLAUDE_DISABLE_SECRET_GUARD=1 in the
 * environment of the harness process that runs this hook (e.g. the shell
 * that launches Claude Code, or a persistent env entry in its settings) —
 * BEFORE the session starts, not inside a command an agent submits for
 * approval. This is a human/operator-only escape hatch, not a per-command
 * agent bypass: PreToolUse hooks run in the harness process to decide
 * whether to allow a proposed command, so they never inherit env vars set
 * inline within that same proposed command string (`CLAUDE_DISABLE_SECRET_GUARD=1
 * git commit ...` in a Bash tool_input does not reach this process — it
 * would only apply, if anything, to the child shell that runs the command
 * AFTER this hook has already allowed or denied it). That is intentional,
 * not a bug: an agent must never be able to self-authorize bypassing a
 * fail-closed security guard from within the very command being gated (see
 * ASM-045, ASM-048 follow-up — SEC-015).
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 * Denies via: { hookSpecificOutput: { hookEventName: "PreToolUse",
 *               permissionDecision: "deny", permissionDecisionReason: "..." } }
 * (same convention as claude-plan-guard.js / claude-pre-commit-check.js)
 */

'use strict';

const path = require('path');

const checker = require('./claude-secret-checker.js');

// LAZY / DEFERRED require (BUG-123 round 9, attacker "Cinder" REJECT).
// claude-git-commit-trigger.js — and, transitively as of round 6,
// claude-quote-mask.js — used to be require()'d right here, at TRUE module
// top level, unconditionally, before main() (or its try/catch) existed at
// all. This file's own header says "Fail-CLOSED... an internal error while
// scanning a `git commit`... results in a DENY rather than an allow" — but a
// synchronous throw during that top-level require() (a syntax error in
// claude-quote-mask.js while it's under live concurrent edit in this same
// tree, a missing file, anything) happens during MODULE EVALUATION, before
// main()'s try/catch block is ever reached. Node's default handling of an
// uncaught exception at module-load time is to print a stack trace to
// stderr and exit 1 — NO stdout, NO hookSpecificOutput JSON at all. Under
// the PreToolUse contract (documented at claude-destructive-guard.js:137-158)
// that is a NON-BLOCKING failure: the proposed `git commit` PROCEEDS
// completely unscanned, reproducing BUG-123's own original impact (a
// fail-closed guard silently skipping its scan) via a brand new mechanism —
// a dependency crash instead of a regex-trigger miss. Live-reproduced in an
// isolated sandbox (this repo's files untouched): a broken copy of
// claude-quote-mask.js crashed this guard at require() time for EVERY
// command, not just commit-shaped ones, exit 1, zero stdout.
//
// claude-destructive-guard.js was already hardened against this identical
// shape after its own round-2/3 (see its DEPENDENCY LOADING comment) by
// moving its `require()`s of claude-author-guard.js / claude-bow.js INSIDE
// main(), where a throw is an ordinary JS exception the existing
// main().catch() net (here: the existing top-level try/catch inside
// process.stdin's 'end' handler) already covers. That pattern is now
// carried into this file: the trigger module is loaded lazily via
// loadGitCommitTrigger() below, called from INSIDE main()'s try block, so a
// load-time throw is caught by the SAME catch() that already denies on any
// other internal error — no new mechanism invented. A load failure does not
// unconditionally deny every Bash/PowerShell command in the session (that
// would brick `git status` / `npm install` over a bug in a file this guard
// doesn't even need for those) — it denies only when the raw command text
// PLAUSIBLY looks like a git commit (looksLikeCommitFallback(), dependency-
// free by construction, mirroring claude-destructive-guard.js's own
// FALLBACK_LOOKS_LIKE_COMMIT_RE), the same commit-shaped narrowing this
// file's unparseable-stdin branch already applies.
//
// Path resolution supports an optional env var override
// (CLAUDE_SECRET_GUARD_TRIGGER_PATH), defaulting to the real sibling file —
// production behaviour is byte-for-byte unchanged; the override exists only
// so the regression test suite can point at disposable fixture files
// (missing / syntax-error / wrong-exports) under the OS temp dir without
// ever touching the real, live-edited claude-git-commit-trigger.js /
// claude-quote-mask.js.
let _gitCommitTriggerMod = null;
function loadGitCommitTrigger() {
  if (_gitCommitTriggerMod) return _gitCommitTriggerMod;
  const triggerPath =
    process.env.CLAUDE_SECRET_GUARD_TRIGGER_PATH || path.join(__dirname, 'claude-git-commit-trigger.js');
  // eslint-disable-next-line global-require, import/no-dynamic-require
  const mod = require(triggerPath);
  if (typeof mod.buildAnchoredGitVerbTriggerRegex !== 'function') {
    throw new Error(
      'claude-git-commit-trigger.js loaded but did not export buildAnchoredGitVerbTriggerRegex ' +
        '(wrong module shape — cannot build the commit trigger without it)'
    );
  }
  _gitCommitTriggerMod = mod;
  return mod;
}

// Self-contained (NO further `require()`s — must work even when
// loadGitCommitTrigger() itself is what just failed) best-effort check: does
// `command` plausibly contain a git commit invocation? Deliberately COARSER
// than the real trigger (which needs claude-git-commit-trigger.js's real
// tokenizer) — it exists only to decide "is a dependency-load failure worth
// denying over", mirroring claude-destructive-guard.js's
// looksLikeCommitFallback() exactly.
//
// ROUND 10 (attacker "Thresher" REJECT): this used to be a single regex,
// FALLBACK_LOOKS_LIKE_COMMIT_RE = /\bgit(?:\.(?:exe|cmd))?\b[\s\S]{0,200}?\bcommit\b/i,
// which capped the gap between "git" and "commit" at ~200 chars. That cap was
// a mistaken performance guard: a single legitimately long `-c` option value
// (e.g. `git -c user.name="<250 chars>" commit -m x`, Thresher's exact repro)
// pushes the real invocation's git-to-commit distance past the window, so
// with the primary dependency broken NOTHING catches it — reproducing
// BUG-123's own original impact (a real commit landing completely unscanned)
// via a fourth mechanism. This fallback runs only on the rare
// dependency-broken path, so it never needed to be bounded for performance;
// it needs to find "commit" anywhere after "git" in the WHOLE string. Rebuilt
// below as plain indexOf/startsWith scanning rather than a regex at all —
// each scan is strictly O(n) with zero backtracking, so it is immune to
// catastrophic-backtracking-style ReDoS regardless of adversarial input
// length or shape (measured: a 1MB non-matching string completes in low
// single-digit milliseconds, see the round-10 regression test).
function isWordChar(ch) {
  return !!ch && /[a-z0-9_]/.test(ch);
}
function looksLikeCommitFallback(command) {
  const lower = (command || '').toLowerCase();
  let idx = 0;
  while (idx < lower.length) {
    const gitIdx = lower.indexOf('git', idx);
    if (gitIdx === -1) return false;
    const beforeCh = gitIdx > 0 ? lower[gitIdx - 1] : '';
    const afterStart = gitIdx + 3;
    const hasSuffix = lower.startsWith('.exe', afterStart) || lower.startsWith('.cmd', afterStart);
    const afterEnd = hasSuffix ? afterStart + 4 : afterStart;
    const afterCh = afterEnd < lower.length ? lower[afterEnd] : '';
    if (!isWordChar(beforeCh) && !isWordChar(afterCh)) {
      // Found a bare "git" (or git.exe/git.cmd) token — now look for "commit"
      // anywhere after it, no distance bound at all.
      return lower.indexOf('commit', afterEnd) !== -1;
    }
    idx = gitIdx + 3;
  }
  return false;
}

// @FIX (SEC-008 follow-up, Bill-directed scope expansion 2026-08-09): this
// hook's commit-intercept used to be a bare `command.includes('git commit')`
// substring test. Replaced with the same shell-command-boundary-anchored
// regex already proven in claude-version-guard.js / claude-bow-ref-check.js /
// claude-plan-guard.js / claude-pre-commit-check.js: the phrase must sit at
// the start of the command or immediately after a shell separator (`;`,
// `&`, `|`, `(`, newline), so a quoted mention never matches but every real
// invocation still does.
//
// BUG-088 CORRECTION (2026-08-11): a prior pass of this refactor silently
// ported claude-author-guard.js's buildQuoteMask()/isRealGitCommit()
// quote-tracking machinery into this trigger check. That machinery does NOT
// belong here and never shipped here — `git show HEAD:claude-secret-guard.js`
// confirms this file's trigger has only ever been a bare `GIT_COMMIT_RE.test
// (command)`, matching AC-C1/AC-C2's explicit claim that this guard's
// PreToolUse trigger is unchanged by BUG-088. Porting the quote mask in
// introduced a NEW, undisclosed false-negative: an unbalanced/odd-count
// quote character earlier in the command string (e.g. inside a shell
// comment, `"# don't forget to review; git commit -m x"`) flips the mask's
// quote-state parity and makes a real, immediately-following `git commit`
// invisible to the trigger, silently skipping the whole scan. Reverted to
// the original bare-regex shape. The quote-masking fix (BUG-043) is real and
// correct, but it lives ONLY in claude-author-guard.js (and, deliberately,
// claude-destructive-guard.js) — see GR#3: duplicating it into this guard is
// exactly the kind of accidental, unreviewed drift GR#3 exists to prevent.
//
// This trigger machinery stays HERE, not in claude-secret-checker.js
// (AC-B4/AC-B3): it is what decides whether to engage at THIS PreToolUse
// layer specifically, which is unchanged in posture by BUG-088 (see header).
// A future commit-msg dispatcher requiring claude-secret-checker.js has no
// engage decision to make at all, so it needs none of this.
//
// BUG-123 (2026-08-12): the single `(?:-C\s+\S+\s+)?` global-option slot only
// tolerated one bare `-C <dir>` between `git` and `commit`, so the extremely
// common `git -c user.email=... commit` idiom (or `-c commit.gpgsign=false`,
// or several `-c`s, or `-c` combined with `-C`) did not match at all — this
// fail-closed guard exited BEFORE calling checker.runScan(), silently
// skipping the secret scan. Fixed by building the trigger from
// claude-git-commit-trigger.js's shared option-run grammar (GR#3 — see that
// module's header for why this is a shared regex builder and not four
// independently-drifting copies, and for why it is NOT the same thing as the
// whole-command quote-mask machinery BUG-088 deliberately kept out of this
// file). Still a bare, single RegExp-like object — no quote-masking of the
// surrounding command text, unchanged posture.
//
// ROUND 9: no longer built here at module top level — see the LAZY /
// DEFERRED require comment above. Built on demand by buildGitCommitRe()
// (production, inside main()'s try block) and once, eagerly, in the
// module.exports branch below (test-import path only, never the production
// hook path — see that branch's own comment).
function buildGitCommitRe() {
  return loadGitCommitTrigger().buildAnchoredGitVerbTriggerRegex('commit');
}

function deny(reason) {
  const output = JSON.stringify({
    hookSpecificOutput: {
      hookEventName: 'PreToolUse',
      permissionDecision: 'deny',
      permissionDecisionReason: reason,
    },
  });
  process.stdout.write(output);
  process.exit(0);
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

function main() {
  let input = '';
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', chunk => { input += chunk; });
  process.stdin.on('end', () => {
  try {
    if (process.env.CLAUDE_DISABLE_SECRET_GUARD === '1') {
      process.exit(0);
    }

    let command;
    try {
      const data = JSON.parse(input.replace(/^\uFEFF/, ''));
      command = data?.tool_input?.command ?? '';
    } catch {
      if (input.includes('git commit')) {
        deny('🛑 SECRET GUARD: hook input was unparseable but appears to contain a git commit — denying (fail-closed). Raw input parse error; retry the commit.');
      }
      process.exit(0);
    }

    // ROUND 9 (BUG-123, Cinder REJECT): the trigger module is loaded HERE,
    // lazily, inside this try block — not at module top level — so a
    // load-time throw (broken claude-quote-mask.js, missing
    // claude-git-commit-trigger.js, wrong exports) is caught by the SAME
    // catch() below that already denies on any other internal error, rather
    // than crashing the whole process before this try/catch ever existed.
    // See the LAZY / DEFERRED require comment near the top of this file.
    let GIT_COMMIT_RE;
    try {
      GIT_COMMIT_RE = buildGitCommitRe();
    } catch (err) {
      if (looksLikeCommitFallback(command)) {
        deny(
          'SECRET GUARD: a required dependency (claude-git-commit-trigger.js) failed to load — ' +
          'cannot determine whether this is a git commit. Denying (fail-closed; this command looks ' +
          'like it could be a git commit) — see claude-secret-guard.js header.\n\n' +
          `${err && err.stack ? err.stack : err}\n\n` +
          'Emergency bypass (use deliberately, not routinely): CLAUDE_DISABLE_SECRET_GUARD=1'
        );
      }
      process.exit(0);
    }

    if (!GIT_COMMIT_RE.test(command)) {
      process.exit(0);
    }

    // Delegate the actual scan to the extracted checker module (BUG-088,
    // AC-C1: observable PreToolUse behaviour is unchanged).
    const findings = checker.runScan();

    if (findings.length > 0) {
      deny(
        '🛑 SECRET GUARD: staged content contains possible secrets or GR#15 hardcoding smells (GR#11).\n\n' +
        `${checker.formatFindings(findings)}\n\n` +
        'Remove or rotate the secret(s), or read the value from config/data instead of hardcoding it, then re-stage and retry.\n' +
        'If this is a false positive, add a precise entry to claude-secret-guard.allow.json (see its inline docs) — never widen a pattern to fix a single case.\n' +
        'Emergency bypass (use deliberately, not routinely): CLAUDE_DISABLE_SECRET_GUARD=1'
      );
      return;
    }

    process.exit(0);

  } catch (err) {
    deny(
      '🛑 SECRET GUARD: internal error while scanning staged content — denying commit ' +
      '(fail-closed by design; see claude-secret-guard.js header).\n\n' +
      `${err && err.stack ? err.stack : err}`
    );
  }
  });
}

if (require.main === module) {
  main();
} else {
  // Exported for tests only. Re-exports the checker's payload-inspection
  // helpers verbatim (AC-D2 — this file no longer defines them itself, but
  // the existing test suite's require()'d names must keep working, per
  // AC-C1's "only require() target may change" allowance) plus this file's
  // own trigger constant (GIT_COMMIT_RE, which stays local to the PreToolUse
  // layer — see AC-B4). No isRealGitCommit()/buildQuoteMask() any more
  // (BUG-088 correction, see the trigger comment above) — the trigger is a
  // bare regex test, matching HEAD's original, un-refactored shape.
  //
  // ROUND 9: this eager build (rather than lazy, as main() now uses) is
  // deliberate and SAFE here — this branch runs only when a test harness
  // require()'s this file directly (never the production PreToolUse hook
  // path, which always takes the `require.main === module` branch above),
  // and the existing test suite's ~180 assertions call `GIT_COMMIT_RE.test
  // (...)` synchronously with no setup step, so the trigger must already be
  // a built object by the time this module.exports runs.
  const GIT_COMMIT_RE = buildGitCommitRe();
  module.exports = {
    GIT_COMMIT_RE,
    looksLikeCommitFallback,
    scanLine: checker.scanLine,
    identifierWords: checker.identifierWords,
    isHardcodeKeywordIdentifier: checker.isHardcodeKeywordIdentifier,
    HARDCODE_KEYWORDS: checker.HARDCODE_KEYWORDS,
    HARDCODE_EXEMPT_LITERALS: checker.HARDCODE_EXEMPT_LITERALS,
    looksHighEntropy: checker.looksHighEntropy,
    shannonEntropy: checker.shannonEntropy,
    redact: checker.redact,
    stripGoCommentsAndStrings: checker.stripGoCommentsAndStrings,
    collectGoPackageIdentifiers: checker.collectGoPackageIdentifiers,
    isGoPackageIdentifier: checker.isGoPackageIdentifier,
    isAllowlistedValue: checker.isAllowlistedValue,
    isAllowlistedPath: checker.isAllowlistedPath,
    globMatch: checker.globMatch,
    parseAddedLines: checker.parseAddedLines,
    loadAllowlist: checker.loadAllowlist,
  };
}
