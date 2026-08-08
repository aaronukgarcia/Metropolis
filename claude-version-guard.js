/**
 * PreToolUse hook — Golden Rule #2 enforcement.
 * Blocks any `git commit` command unless BOTH package.json and version.ts
 * are staged (git added) in the current commit — EXCEPT docs/tooling-only
 * commits (docs/, *.md, .claude/, root claude-*.js), which are GR#2-exempt
 * per Aaron 2026-07-29 and pass without a bump (see @FIX v3.1.11 below).
 *
 * Why: On 2026-03-24, 10 consecutive commits were pushed to main under the
 * same version number (2.5.6) because raw git commands bypassed the /commit
 * skill. This hook makes it impossible to commit without a version bump,
 * regardless of whether /commit is used or git is called directly.
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 * Returns JSON to block: { hookSpecificOutput: { permissionDecision: "deny" } }
 * Returns nothing to allow.
 */

const { execSync } = require('child_process');

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
      // claude-sync) — the versioned app manifest will live at app/package.json.
      /^package\.json$/,
      /^package-lock\.json$/,
    ];
    const relPaths = stagedFiles.map(f => f.replace(/^03\.Current\//, ''));
    if (relPaths.length > 0 && relPaths.every(f => EXEMPT_PATTERNS.some(rx => rx.test(f)))) {
      process.exit(0); // docs/tooling-only commit — GR#2 exempt, no bump required
    }

    const hasPackageJson = staged.includes('package.json');
    const hasVersionTs = staged.includes('version.ts');

    if (hasPackageJson && hasVersionTs) {
      // Both files staged — now verify the version ACTUALLY changed (not just re-staged)
      let versionDiff = '';
      try {
        versionDiff = execSync('git diff --cached -- app/package.json app/src/lib/version.ts', {
          encoding: 'utf8',
          timeout: 5000,
        });
      } catch {
        // If diff fails, allow — don't block on tooling errors
        process.exit(0);
      }

      if (versionDiff.includes('+') && (versionDiff.includes('"version"') || versionDiff.includes('APP_VERSION'))) {
        // Version string actually changed — allow
        process.exit(0);
      }

      // Files staged but version didn't change — block
      const reason = `🛑 GOLDEN RULE #2: Version files are staged but the version number hasn't changed.\n` +
        `Run /bump to increment the version before committing.`;
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

    // Build a clear message about what's missing
    const missing = [];
    if (!hasPackageJson) missing.push('package.json');
    if (!hasVersionTs) missing.push('src/lib/version.ts');

    const reason = `🛑 GOLDEN RULE #2 VIOLATION: Version bump required.\n` +
      `Missing from staged files: ${missing.join(' + ')}\n` +
      `Run /bump first, then commit. Every commit to main must bump the version.`;

    const output = JSON.stringify({
      hookSpecificOutput: {
        hookEventName: 'PreToolUse',
        permissionDecision: 'deny',
        permissionDecisionReason: reason,
      },
    });

    process.stdout.write(output);
    process.exit(0);

  } catch (err) {
    // Parse error or unexpected input — don't block
    process.exit(0);
  }
});
