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
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 * Returns JSON to block: { hookSpecificOutput: { permissionDecision: "deny" } }
 * Returns JSON with permissionDecision "allow" + a reason to warn-but-allow.
 * Returns nothing to allow silently.
 *
 * Escape hatch (unchanged name): CLAUDE_DISABLE_VERSION_GUARD=1
 */

'use strict';

const fs = require('fs');
const path = require('path');
const { execSync, spawnSync } = require('child_process');

const ROOT = __dirname;

// The two retired Prix-Six-style version files (MOD-001, cancelled).
const HAND_MAINTAINED_EXACT_PATHS = new Set([
  'app/package.json',
  'app/src/lib/version.ts',
]);

// The one sanctioned ldflags-injection target — its "dev" defaults are not
// a hardcoded version, they're the pre-build placeholder (see file header).
const BUILDINFO_EXEMPT_PATH = 'internal/foundation/buildinfo/buildinfo.go';

const VERSION_GO_RE = /(^|\/)version\.go$/;
const SEMVER_LITERAL_RE = /["'`]v?\d+\.\d+\.\d+/;

function isHandMaintainedVersionFile(relPath) {
  if (HAND_MAINTAINED_EXACT_PATHS.has(relPath)) return true;
  if (path.basename(relPath) === 'VERSION') return true;
  return false;
}

// A version.go file (other than the exempt buildinfo.go) is only a
// violation if its STAGED content actually hardcodes a semver-looking
// literal — merely existing under that filename isn't itself banned.
// @FIX (SEC-002): relPath used to be interpolated into an execSync shell
//   string (`git diff --cached -- "${relPath}"`). On Windows, execSync runs
//   via cmd.exe, which percent-expands `%VAR%` tokens EVEN INSIDE double
//   quotes — a staged path containing a literal '%' (legal on NTFS) could
//   silently retarget the diff at an unrelated/nonexistent path, `git`
//   would then fail, and the old catch block swallowed that and returned
//   `false` ("no hardcoded semver found") — silently defeating GR#2 for
//   that commit. Fixed by passing relPath as a single argv element to
//   spawnSync (shell:false, the default) — no shell ever re-parses it, so
//   there is nothing left to expand, exactly like claude-secret-guard.js /
//   claude-plan-guard.js already do for git-derived values.
//
// A genuine (non-injection) git failure here is still possible (e.g. this
// hook running outside a git repo). This hook's documented posture (see
// file header) is fail-OPEN for GR#2 as a whole — a hygiene check, not a
// security gate — so we still return `false` (no violation found) rather
// than blocking the commit. But per the "never let an error silently pass"
// lesson from this finding, that fallback is no longer SILENT: it is
// surfaced on stderr so a human reviewing hook output can see a check was
// skipped, rather than the commit passing with false confidence and no
// trace. See ASM-* logged for this file.
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
      `⚠️  GR#2 GUARD: could not diff staged file "${relPath}" to check for a hardcoded semver ` +
      `(${details}). Skipping this file's check rather than blocking the commit (fail-open, ` +
      'by this hook\'s documented posture) — but this means the hardcoded-semver check for this ' +
      'file was NOT performed. Please verify manually.\n'
    );
    return false;
  }
  const diff = result.stdout || '';
  return diff.split('\n').some(
    line => line.startsWith('+') && !line.startsWith('+++') && SEMVER_LITERAL_RE.test(line)
  );
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

    // @FIX (v3.1.10): The v3.1.9 word-boundary regex still fired on "git commit"
    //   inside string literals (e.g. BOW summaries describing the regex itself).
    //   Tightened to require a SHELL COMMAND BOUNDARY before "git commit" — start
    //   of string, or after a shell separator (; & | ( newline). Inside a quoted
    //   string the preceding char is typically space/letters/quote, none of which
    //   match this class. So real `git commit` invocations match; mentions of
    //   "git commit" in string content do not.
    //
    // @ESCAPE_HATCH (v3.1.10): set CLAUDE_DISABLE_VERSION_GUARD=1 to bypass entirely
    //   when working on chained `git add && git commit` sequences where the hook
    //   sees the future-staged state, not the current.
    if (!/(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/.test(command)) {
      process.exit(0);
    }

    // Skip: amend, merge commits, or if this IS the version bump commit
    if (command.includes('--amend')) {
      process.exit(0);
    }

    // @FIX (v3.1.8): Removed hardcoded path `E:\\GoogleDrive\\Papers\\03-PrixSix` —
    //   project moved to `E:\\git\\prix6\\03.Current` ~2026-04 and the old path no
    //   longer exists. The hardcoded `git -C` was failing silently (caught by the
    //   try/except → exit 0), meaning GR#2 enforcement was completely disabled
    //   from the project move until 2026-05-06. Now uses git from cwd, which is
    //   path-agnostic and works wherever Claude Code launches from inside the repo.
    let staged = '';
    try {
      staged = execSync('git diff --cached --name-only', {
        encoding: 'utf8',
        timeout: 5000,
      });
    } catch {
      // If git fails, don't block — might be outside repo
      process.exit(0);
    }

    // @FIX (v3.1.11): GR#2 exemption (Aaron, 2026-07-29) is now encoded in the
    //   gate instead of living only in /commit GATE 1. Commits whose staged
    //   files are ALL non-deployed paths (docs, markdown, .claude skills,
    //   root claude-*.js tooling) require NO version bump — bumping for them
    //   would advertise a phantom release on /about and burn a build. Before
    //   this fix, such commits could only pass via
    //   CLAUDE_DISABLE_VERSION_GUARD=1, which trained routine bypassing of
    //   the gate — the exact decay mode that kills gates. Deployed code
    //   (app/, functions/, firestore.rules, apphosting.yaml) still blocks.
    //   Note: `git diff --cached --name-only` paths are repo-root-relative,
    //   so files under the project dir arrive prefixed with `03.Current/`.
    const stagedFiles = staged.split('\n').map(f => f.trim()).filter(Boolean);
    const EXEMPT_PATTERNS = [
      /^docs\//,               // documentation tree
      /\.md$/i,                // markdown anywhere (CLAUDE.md, README, skills)
      /^\.claude\//,           // skills / settings, if ever tracked
      /^claude-[\w.-]+\.js$/,  // root coordination + hook tooling scripts
      /^\.gitignore$/,
      // Metropolis: ROOT package.json holds only hook-tooling deps (mysql2 for
      // claude-sync) — never the versioned app manifest (that pattern is retired,
      // see @RETARGET below).
      /^package\.json$/,
      /^package-lock\.json$/,
    ];
    const relPaths = stagedFiles.map(f => f.replace(/^03\.Current\//, ''));
    if (relPaths.length > 0 && relPaths.every(f => EXEMPT_PATTERNS.some(rx => rx.test(f)))) {
      process.exit(0); // docs/tooling-only commit — GR#2 exempt, no bump required
    }

    // @RETARGET (v4.0.0, 2026-08-09, FEAT-002/legacy.versionguard): the old
    //   two-file (app/package.json + app/src/lib/version.ts) check is retired.
    //   GR#2 is now satisfied via git-describe + ldflags (see file header) for
    //   any repo where the Go skeleton exists. Evaluated fresh every
    //   invocation — this is a fresh process per hook call, so there is no
    //   stale-cache risk.
    const hasGoSkeleton =
      fs.existsSync(path.join(ROOT, 'cmd')) || fs.existsSync(path.join(ROOT, 'internal'));

    if (!hasGoSkeleton) {
      // No Go skeleton yet, and the app/ layout it would have replaced
      // (MOD-001) is cancelled — there is nothing left for this hook to
      // check. Allow.
      process.exit(0);
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
      const reason =
        `🛑 GOLDEN RULE #2 VIOLATION (Metropolis profile): hand-maintained version file(s) staged.\n` +
        `Offending: ${offending.join(', ')}\n` +
        `M0-ENG §3 bans hand-maintained version files for this stack — the app version comes ` +
        `SOLELY from \`git describe --tags --dirty\`, injected via -ldflags at build ` +
        `(see internal/foundation/buildinfo/buildinfo.go). Cut a milestone tag ` +
        `(v0.<milestone>.<n>) instead of hand-editing a version string.`;
      deny(reason);
      return;
    }

    // WARN-but-allow: cmd/ or internal/ commits are the ones GR#2's
    // milestone-tag discipline actually governs, and (separately) the ones
    // tool.bow (MOD-007) will require a BOW [mkey] ref on — not this hook's
    // job (AC-7). Just a reminder, never a block.
    const touchesEngineCode = relPaths.some(f => /^cmd\//.test(f) || /^internal\//.test(f));
    if (touchesEngineCode) {
      warnAllow(
        'ℹ️  GR#2 note: this commit touches cmd/ or internal/. Version discipline for this repo ' +
        'is milestone tags (v0.<milestone>.<n>) + -ldflags injection, not a hand-edited file — ' +
        'no action needed here. BOW [mkey] commit-message enforcement is tool.bow\'s (MOD-007) job, ' +
        'not this hook\'s.'
      );
      return;
    }

    process.exit(0);

  } catch (err) {
    // Parse error or unexpected input — don't block
    process.exit(0);
  }
});
