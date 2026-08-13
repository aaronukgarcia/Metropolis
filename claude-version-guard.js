/**
 * PreToolUse hook — Golden Rule #2 enforcement (Metropolis Go-monorepo
 * profile; BOW mkey: legacy.versionguard / FEAT-002).
 *
 * GR#2 ("Version Discipline") is satisfied differently in this repo's Go
 * layout than in the retired Prix-Six-style `app/package.json` +
 * `app/src/lib/version.ts` two-file pattern: M0-ENG §3 mandates that the
 * app version come SOLELY from `git describe --tags --dirty`, injected at
 * build time via `-ldflags -X ...buildinfo.Version=...` (see
 * `internal/foundation/buildinfo/buildinfo.go`), with milestone cuts marked
 * by annotated tags `v0.<milestone>.<n>` (M0-ENG §5). Hand-maintained
 * version files are explicitly banned for this stack — so this hook no
 * longer requires one to be staged. `MOD-001` (the app/ skeleton the old
 * two-file check targeted) was cancelled; `MOD-003` (Go monorepo skeleton)
 * is what actually landed.
 *
 * Retargeted behaviour once `cmd/` or `internal/` exist in the repo (they
 * do, as of MOD-003):
 *   1. DENY a commit that stages a hand-maintained version file: the
 *      retired `app/package.json` / `app/src/lib/version.ts` paths, any
 *      file named exactly `VERSION`, or a `version.go` file whose staged
 *      content hardcodes a semver literal instead of relying on ldflags.
 *      `internal/foundation/buildinfo/buildinfo.go` is exempt by path — its
 *      `"dev"` defaults are the sanctioned ldflags injection target.
 *   2. WARN (allow, with a stdout note) when a commit touches `cmd/` or
 *      `internal/` — a reminder that milestone tags are the version
 *      mechanism here, and that BOW `[mkey]` commit-message enforcement is
 *      `tool.bow`'s (MOD-007) job, not this hook's (see Escalation #1 in
 *      docs/planning/acceptance/legacy.versionguard.md — this item is
 *      scoped to retiring the two-file check ONLY).
 *   3. The docs/tooling exemption (docs/, *.md, .claude/, root
 *      claude-*.js, .gitignore, root package.json/package-lock.json) is
 *      unchanged.
 *
 * Rationale / history: Aaron's 2026-08-08 16:15 DECIDED note (BOW
 * legacy.versionguard comment) approved this retarget. Left unretargeted,
 * this hook demanded two files (`app/package.json`, `app/src/lib/version.ts`)
 * that no longer exist in the plan, blocking every non-exempt commit to
 * `cmd/`, `internal/`, or `data/` — see the acceptance file's status note
 * (2026-08-09) for the live-blocking symptom this fix addresses.
 *
 * Fails OPEN on any unexpected error (parse failure, git failure, fs
 * error) — unchanged from every prior version of this hook. This is a
 * deliberate departure from claude-plan-guard.js / claude-secret-guard.js's
 * fail-closed posture: GR#2 here is a hygiene check, not a security gate,
 * and a hook bug must never brick unrelated commits (AC-8).
 *
 * BUG-088 (2026-08-11): this hook's *trigger* — the bare `GIT_COMMIT_RE.test
 * (command)` check below — was defeated by any leading word, shell wrapper,
 * or non-bareword git invocation, same class as the three sibling guards.
 * Its *payload* (staged hand-maintained-file / hardcoded-semver detection)
 * was always sound. Per docs/planning/acceptance/tool.secretguard.md's
 * BUG-088 section, this file STAYS EXACTLY AS-IS in PreToolUse behaviour and
 * posture (unchanged by this item — it is explicitly documented, above, as
 * "a hygiene check, not a security gate", and BUG-088's remediation does not
 * raise its blast radius). The payload logic has been extracted, UNCHANGED,
 * into claude-version-checker.js — a standalone, requireable module now the
 * single source of truth for the check (GR#3), reachable by this guard AND
 * a future `commit-msg` hook dispatcher (out of scope here — see the
 * acceptance file's Section B). Per that same table: the checker module's
 * OWN internal-error state stays fail-OPEN when a caller uses it (this is
 * the one BUG-088 checker that deliberately does NOT get identity's
 * fail-closed answer — see claude-version-checker.js's header for the full
 * reasoning). This file's own trigger check is UNCHANGED by this item —
 * still the original, best-effort boundary-regex check it has always been
 * (a prior pass of this refactor briefly ported quote-masking into this
 * check, a P0 undisclosed-behaviour-change finding; reverted — see the
 * trigger comment further below).
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 * Returns JSON to block: { hookSpecificOutput: { permissionDecision: "deny" } }
 * Returns JSON with permissionDecision "allow" + a reason to warn-but-allow.
 * Returns nothing to allow silently.
 *
 * Escape hatch (unchanged name): CLAUDE_DISABLE_VERSION_GUARD=1
 */

'use strict';

const checker = require('./claude-version-checker.js');
const { buildAnchoredGitVerbTriggerRegex } = require('./claude-git-commit-trigger.js');

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

function warnAllow(reason) {
  const output = JSON.stringify({
    hookSpecificOutput: {
      hookEventName: 'PreToolUse',
      permissionDecision: 'allow',
      permissionDecisionReason: reason,
    },
  });
  process.stdout.write(output);
  process.exit(0);
}

// @FIX (v3.1.10): The v3.1.9 word-boundary regex still fired on "git commit"
//   inside string literals. Tightened to require a SHELL COMMAND BOUNDARY
//   before "git commit" — start of string, or after a shell separator
//   (; & | ( newline). Inside a quoted string the preceding char is
//   typically space/letters/quote, none of which match this class. So real
//   `git commit` invocations match; mentions of "git commit" in string
//   content do not.
//
// BUG-088 CORRECTION (2026-08-11): a prior pass of this refactor silently
// ported claude-author-guard.js's buildQuoteMask()/isRealGitCommit()
// quote-tracking machinery into this trigger check. That never shipped here
// — `git show HEAD:claude-version-guard.js` confirms this file's trigger has
// only ever been this bare boundary-anchored regex test, matching AC-C2's
// explicit claim that this guard's PreToolUse trigger is unchanged by
// BUG-088. Porting the quote mask in introduced a NEW, undisclosed
// false-negative: an unbalanced/odd-count quote character earlier in the
// command string (e.g. inside a shell comment, `"# don't forget to review;
// git commit -m x"`) flips the mask's quote-state parity and makes a real,
// immediately-following `git commit` invisible to the trigger. Reverted to
// the original bare-regex shape. The quote-masking fix (BUG-043) is real and
// correct, but it lives ONLY in claude-author-guard.js (and, deliberately,
// claude-destructive-guard.js) — see GR#3: duplicating it into this guard is
// exactly the kind of accidental, unreviewed drift GR#3 exists to prevent.
//
// BUG-123 (2026-08-12): the single `(?:-C\s+\S+\s+)?` slot only tolerated one
// bare `-C <dir>` between `git` and `commit`, so `git -c user.email=... commit`
// (and other `-c`-bearing invocations) never matched — this guard's version
// check was silently skipped for any commit prefixed with `-c`. Fixed via
// claude-git-commit-trigger.js's shared option-run grammar (GR#3 — see that
// module's header). Still a bare RegExp, no quote-masking added.
const GIT_COMMIT_RE = buildAnchoredGitVerbTriggerRegex('commit');

function main() {
let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => { input += chunk; });
process.stdin.on('end', () => {
  try {
    if (process.env.CLAUDE_DISABLE_VERSION_GUARD === '1') {
      process.exit(0);
    }

    // @FIX (v3.1.11): strip a leading BOM before parsing — a BOM-prefixed
    //   stdin (e.g. piped from PowerShell) made JSON.parse throw, and the
    //   fail-graceful catch turned that into a silent ALLOW. Harness stdin
    //   is BOM-free, but a gate should not fail open over one invisible byte.
    const data = JSON.parse(input.replace(/^\uFEFF/, ''));
    const command = data?.tool_input?.command ?? '';

    // @ESCAPE_HATCH (v3.1.10): set CLAUDE_DISABLE_VERSION_GUARD=1 to bypass entirely
    //   when working on chained `git add && git commit` sequences where the hook
    //   sees the future-staged state, not the current.
    // Bare boundary-anchored regex test — see the GIT_COMMIT_RE comment above
    // (BUG-088 correction): no quote-masking here, that machinery belongs to
    // claude-author-guard.js only.
    if (!GIT_COMMIT_RE.test(command)) {
      process.exit(0);
    }

    // Skip: amend, merge commits, or if this IS the version bump commit
    if (command.includes('--amend')) {
      process.exit(0);
    }

    // Delegate the actual GR#2 check to the extracted checker module
    // (BUG-088, AC-C2: observable PreToolUse behaviour is unchanged). Its
    // own git-invocation failures land in result.status === 'internal-error'
    // — this hook's fail-OPEN posture (see header) is applied HERE, at the
    // caller, exactly as the checker module's own header says a caller
    // must: don't block on it, just don't pretend nothing happened.
    const result = checker.checkVersion();

    if (result.status === 'internal-error') {
      // Fail-OPEN, unchanged posture: surface it (not silently), don't block.
      process.stderr.write(
        `⚠️  GR#2 GUARD: internal error while checking staged content (${result.error && result.error.message}). ` +
        'Skipping the check rather than blocking the commit (fail-open, by this hook\'s documented posture).\n'
      );
      process.exit(0);
    }

    if (result.status === 'found-problems') {
      deny(
        `🛑 GOLDEN RULE #2 VIOLATION (Metropolis profile): ${result.findings.join(' ')}`
      );
      return;
    }

    // Clean. If the checker attached a non-blocking note (cmd/ or internal/
    // touched), surface it as a warn-allow exactly as before.
    if (result.note) {
      warnAllow(`ℹ️  GR#2 note: ${result.note}`);
      return;
    }

    process.exit(0);

  } catch (err) {
    // Parse error or unexpected input — don't block
    process.exit(0);
  }
});
}

// require.main === module guard (added for BUG-043 testability, same pattern
// as claude-secret-guard.js): when run directly as the hook, behaviour is
// unchanged. When require()'d by a test harness, main() is never called (so
// stdin is never touched) and the pure helper functions below are exported.
if (require.main === module) {
  main();
} else {
  module.exports = {
    GIT_COMMIT_RE,
  };
}
