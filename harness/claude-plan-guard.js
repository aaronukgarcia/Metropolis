// Module key: tool.planguard (see code.json; GUID 5f71974a-a612-4113-ba75-8e91546b8977)
// Spec ref: GR#3; GR#6; M0-ENG §5 (hooks)

/**
 * PreToolUse hook — plan-drift guard (BOW mkey: tool.planguard).
 *
 * Spec: GR#3 (Single Source of Truth); GR#6 (GUID Documentation); M0-ENG §5 (hooks)
 *
 * docs/planning/master-plan-v2.1.json is the hand-authored single source of
 * truth for the module registry. tools/plan/generate.js derives code.json
 * (repo root) and tools/plan/bow-import.json from it, deterministically and
 * idempotently (GUIDs are carried over by key, so a clean regeneration never
 * changes anything already committed). This hook makes that contract
 * mechanical: it intercepts `git commit` and refuses to let a commit land
 * when code.json / bow-import.json are stale relative to the master plan,
 * or were hand-edited (both cases show up as the same symptom — regenerating
 * changes the files on disk).
 *
 * Fail-CLOSED, unlike claude-pre-commit-check.js's fail-open: this guard's
 * entire job is to stop a plan/registry-divergent commit from landing (GR#3,
 * GR#6). A crash or unexpected internal error in the guard itself must not
 * silently let such a commit through, so any error caught by the top-level
 * try/catch below results in a DENY (with the error message attached) rather
 * than an allow. This is a deliberate departure from the "never block
 * legitimate work due to a hook bug" posture used elsewhere in this repo.
 *
 * BUG-088 (2026-08-11): this hook's *trigger* — the bare `GIT_COMMIT_RE.test
 * (command)` check below — was defeated by any leading word, shell wrapper,
 * or non-bareword git invocation, same class as claude-secret-guard.js/
 * claude-version-guard.js/claude-pre-commit-check.js. Its *payload*
 * (regenerate + hash-compare) was always sound — real filesystem state. Per
 * docs/planning/acceptance/tool.secretguard.md's BUG-088 section, this file
 * KEEPS its blocking PreToolUse posture unchanged (a "false positive" here
 * is not a heuristic misfire, it is real drift — close to no false-positive
 * cost to weigh, and registry integrity is the same severity class as
 * identity). The payload logic has been extracted, UNCHANGED, into
 * claude-plan-checker.js — a standalone, requireable module now the single
 * source of truth for the drift check (GR#3), reachable by this guard AND
 * a future `commit-msg` hook dispatcher (out of scope here — see the
 * acceptance file's Section B). This file's own trigger check is UNCHANGED
 * by this item — still the original, best-effort boundary-regex check it has
 * always been (a prior pass of this refactor briefly ported quote-masking
 * into this check, a P0 undisclosed-behaviour-change finding; reverted — see
 * the trigger comment further below), still needed at this PreToolUse layer
 * until a commit-msg hook exists.
 *
 * To disable deliberately: set env var CLAUDE_DISABLE_PLAN_GUARD=1.
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 * Denies via: { hookSpecificOutput: { hookEventName: "PreToolUse",
 *               permissionDecision: "deny", permissionDecisionReason: "..." } }
 * (same convention as claude-pre-commit-check.js / claude-version-guard.js)
 */

'use strict';

const fs = require('fs');
const path = require('path');

const checker = require('./claude-plan-checker.js');

// LAZY / DEFERRED require (BUG-123 round 9, attacker "Cinder" REJECT) — see
// claude-secret-guard.js's identical comment block (same finding hit both
// files, same fix applied to both, kept word-for-word so the two guards
// don't independently drift on this — GR#3). claude-git-commit-trigger.js
// (and transitively claude-quote-mask.js) used to be require()'d right here,
// at TRUE module top level, before main()'s try/catch existed. A synchronous
// throw during that require (a broken claude-quote-mask.js under live
// concurrent edit, a missing file) crashed the whole process at MODULE LOAD
// TIME — uncaught exception, exit 1, zero stdout, no hookSpecificOutput
// JSON — which under the PreToolUse contract is a non-blocking failure: the
// proposed `git commit` PROCEEDS unscanned, directly contradicting this
// file's own header ("Fail-CLOSED... any error caught by the top-level
// try/catch below results in a DENY"). Live-reproduced in an isolated
// sandbox (this repo's files untouched): a broken copy of
// claude-quote-mask.js crashed this guard at require() time for EVERY
// command. Fixed the same way claude-destructive-guard.js was hardened
// after its own round-2/3: the require moves inside main()'s try block
// (loadGitCommitTrigger() below), so a load-time throw is caught by the
// SAME catch() that already denies on any other internal error.
let _gitCommitTriggerMod = null;
function loadGitCommitTrigger() {
  if (_gitCommitTriggerMod) return _gitCommitTriggerMod;
  const triggerPath =
    process.env.CLAUDE_PLAN_GUARD_TRIGGER_PATH || path.join(__dirname, 'claude-git-commit-trigger.js');
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

// Self-contained (NO further `require()`s) best-effort check: does `command`
// plausibly contain a git commit invocation? Deliberately COARSER than the
// real trigger — exists only to decide "is a dependency-load failure worth
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

// @FIX (SEC-008): the intercept test used to be a bare
//   `command.includes('git commit')` — a substring match with no shell-
//   boundary anchoring, so it fired on the phrase appearing anywhere in the
//   command text, including inside an unrelated quoted string (reproduced
//   live: a `node claude-bow.js add finding` call whose --desc merely
//   mentioned "git commit" in prose caused this fail-closed guard to
//   regenerate code.json and DENY an unrelated Bash call). Replaced with the
//   same shell-command-boundary-anchored regex already proven in
//   claude-version-guard.js / claude-bow-ref-check.js: the phrase must sit at
//   the start of the command or immediately after a shell separator (`;`,
//   `&`, `|`, `(`, newline), so quoted mentions never match but every real
//   invocation still does.
//
// BUG-088 CORRECTION (2026-08-11): a prior pass of this refactor silently
// ported claude-author-guard.js's buildQuoteMask()/isRealGitCommit()
// quote-tracking machinery into this trigger check. That never shipped here
// — `git show HEAD:claude-plan-guard.js` confirms this file's trigger has
// only ever been a bare `GIT_COMMIT_RE.test(command)`, matching AC-C1's
// explicit claim that this guard's PreToolUse trigger is unchanged by
// BUG-088. Porting the quote mask in introduced a NEW, undisclosed
// false-negative: an unbalanced/odd-count quote character earlier in the
// command string (e.g. inside a shell comment, `"# don't forget to review;
// git commit -m x"`) flips the mask's quote-state parity and makes a real,
// immediately-following `git commit` invisible to the trigger, silently
// skipping the whole plan-drift check. Reverted to the original bare-regex
// shape. The quote-masking fix (BUG-043) is real and correct, but it lives
// ONLY in claude-author-guard.js (and, deliberately, claude-destructive-
// guard.js) — see GR#3: duplicating it into this guard is exactly the kind
// of accidental, unreviewed drift GR#3 exists to prevent.
//
// BUG-088: this trigger machinery stays HERE, not in claude-plan-checker.js
// (AC-B4/AC-B3) — see this file's header for why.
//
// BUG-123 (2026-08-12): the single `(?:-C\s+\S+\s+)?` slot only tolerated one
// bare `-C <dir>` between `git` and `commit`, so `git -c user.email=... commit`
// (and other `-c`-bearing invocations) never matched — this fail-closed
// guard exited BEFORE calling checker.checkPlan(), silently skipping the
// plan-drift check. Fixed via claude-git-commit-trigger.js's shared
// option-run grammar (GR#3 — see that module's header). Still a bare
// RegExp-like object, no quote-masking added.
//
// ROUND 9: no longer built here at module top level — see the LAZY /
// DEFERRED require comment above. Built on demand by buildGitCommitRe().
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

function main() {
let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => { input += chunk; });
process.stdin.on('end', () => {
  try {
    if (process.env.CLAUDE_DISABLE_PLAN_GUARD === '1') {
      process.exit(0);
    }

    // Strip a UTF-8 BOM (PowerShell pipes prepend one) before parsing.
    let command;
    try {
      const data = JSON.parse(input.replace(/^\uFEFF/, ''));
      command = data?.tool_input?.command ?? '';
    } catch {
      // Unparseable input: we cannot tell what the command is. Fail-closed
      // must apply to commits ONLY — denying every Bash command because stdin
      // hiccuped would brick the whole session. Fall back to a raw substring
      // sniff: deny if it might be a commit, allow anything else.
      if (input.includes('git commit')) {
        deny('🛑 PLAN GUARD: hook input was unparseable but appears to contain a git commit — denying (fail-closed). Raw input parse error; retry the commit.');
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
          'PLAN GUARD: a required dependency (claude-git-commit-trigger.js) failed to load — ' +
          'cannot determine whether this is a git commit. Denying (fail-closed; this command looks ' +
          'like it could be a git commit) — see claude-plan-guard.js header.\n\n' +
          `${err && err.stack ? err.stack : err}\n\n` +
          'Emergency bypass (use deliberately, not routinely): CLAUDE_DISABLE_PLAN_GUARD=1'
        );
        return;
      }
      process.exit(0);
    }

    // Only intercept real git commit invocations (SEC-008 fix). No
    // quote-masking here — see the GIT_COMMIT_RE comment above (BUG-088
    // correction): that machinery belongs to claude-author-guard.js only.
    if (!GIT_COMMIT_RE.test(command)) {
      process.exit(0);
    }

    // BUG-291: test-only escape hatch, same pattern as BUG-165's
    // CLAUDE_AUTHOR_GUARD_FORCE_TOKENIZE_ERROR in claude-author-guard.js —
    // lets a test PROVE this guard's trigger actually fired (i.e. execution
    // reached the checkPlan() call below) without resorting to a wall-clock
    // elapsed-time assertion (BUG-031: count work, not time — a fast CI
    // runner previously made an elapsed>100ms assertion false-fail an
    // innocent PR). When CLAUDE_PLAN_GUARD_TEST_MARKER names a non-empty
    // path, touch that file right before invoking the real check. Wrapped in
    // its own try/catch, fail-open: a marker-write failure (bad path,
    // read-only disk, permissions) must NEVER affect production behaviour or
    // abort the guard — it is purely an out-of-band signal for tests.
    // SECURITY (BUG-291 round finding): never point the marker at a file the
    // guard itself trusts (code.json, the master plan, generate.js) — the
    // fixed-payload write lands BEFORE checkPlan() reads them. Doing so
    // corrupts the target and fail-closed DENIES every commit until repaired
    // (a self-DoS, never a bypass, because the payload is a fixed literal).
    if (process.env.CLAUDE_PLAN_GUARD_TEST_MARKER) {
      try {
        fs.writeFileSync(process.env.CLAUDE_PLAN_GUARD_TEST_MARKER, 'plan-guard-trigger-fired\n');
      } catch {
        // Deliberately swallowed — test-only escape hatch, must fail open.
      }
    }

    // Delegate the actual drift check to the extracted checker module
    // (BUG-088, AC-C1: observable PreToolUse behaviour is unchanged).
    const result = checker.checkPlan();

    if (result.status === 'internal-error') {
      deny(
        '🛑 PLAN GUARD: internal error while checking plan drift — denying commit ' +
        '(fail-closed by design; see claude-plan-guard.js header).\n\n' +
        `${result.error && result.error.stack ? result.error.stack : result.error}`
      );
      return;
    }

    if (result.status === 'found-problems') {
      deny(
        `🛑 PLAN GUARD:\n\n${result.findings.join('\n\n')}`
      );
      return;
    }

    // Clean: outputs already matched the master plan. Allow.
    process.exit(0);

  } catch (err) {
    // Fail-CLOSED by design (see header comment): an internal guard error
    // must never silently let a plan-divergent commit through.
    deny(
      '🛑 PLAN GUARD: internal error while checking plan drift — denying commit ' +
      '(fail-closed by design; see claude-plan-guard.js header).\n\n' +
      `${err && err.stack ? err.stack : err}`
    );
  }
});
}

// require.main === module guard (added for BUG-043 testability, same pattern
// as claude-secret-guard.js): when run directly as the hook, behaviour is
// unchanged. When require()'d by a test harness, main() is never called (so
// stdin is never touched and generate.js is never invoked) and the pure
// helper functions below are exported.
if (require.main === module) {
  main();
} else {
  // ROUND 9: eager build here is deliberate and SAFE — this branch runs only
  // when a test harness require()'s this file directly (never the
  // production PreToolUse hook path, which always takes the
  // `require.main === module` branch above), and the existing test suite
  // calls `GIT_COMMIT_RE.test(...)` synchronously with no setup step. See
  // claude-secret-guard.js's identical comment for the full rationale.
  const GIT_COMMIT_RE = buildGitCommitRe();
  module.exports = {
    GIT_COMMIT_RE,
    looksLikeCommitFallback,
  };
}
