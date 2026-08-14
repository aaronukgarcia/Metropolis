// Module key: tool.prepushcheck (see code.json; GUID 53c61c25-b2ac-4ac5-ad15-31f41d9ca9b7)
// Spec ref: GR#19; M0-ENG §5 (hooks)

/**
 * PreToolUse hook — Prix Six pre-push function-deploy bundling check (GR#19).
 *
 * Catches `git push` commands and inspects the commits about to be pushed
 * (resolved against the ACTUAL destination remote/branch, not text-sniffed
 * from the command). If any such commit touches `functions/` files, the
 * message body must contain a `firebase deploy --only functions:` line. If
 * missing, the hook blocks the push and instructs the user to either:
 *   (a) amend the commit message with the bundled deploy command, or
 *   (b) acknowledge by setting CLAUDE_DISABLE_PUSH_CHECK=1.
 *
 * @FIX (SEC-012 — Destructive-agent finding, 2026-08-09): the old version
 *   only ran this check when the raw command TEXT contained the literal
 *   substrings "main" or "origin" — so `git push upstream release` or
 *   `git push backup master` ("master" doesn't contain "main") skipped the
 *   GR#19 check entirely, even if the pushed commits touched functions/ with
 *   no bundled deploy line. GR#19 governs the destination repo/branch, not
 *   the name it happens to be spelled with, so this hook must apply to
 *   pushes GENERALLY. Fixed by:
 *     - Detecting `git push` via a shell-command-boundary-anchored regex
 *       (same pattern as claude-version-guard.js / claude-bow-ref-check.js /
 *       claude-plan-guard.js) instead of a bare substring test.
 *     - Resolving the actual destination remote/branch by parsing the
 *       command's own positional `git push [<remote>] [<refspec>]` arguments
 *       (properly tokenised, respecting quotes) and, when either is omitted,
 *       falling back to the current branch's tracked upstream via
 *       `git rev-parse --abbrev-ref --symbolic-full-name @{u}` — never by
 *       sniffing "main"/"origin" out of the command text.
 *     - Every git invocation now uses spawnSync with an argv ARRAY
 *       (shell:false, the default) instead of execSync template-literal
 *       strings, so no git-derived value (branch name, commit hash) is ever
 *       re-parsed by a shell — same defensive pattern applied repo-wide for
 *       SEC-002/SEC-004.
 *   Also fixed in passing (over-strict, not a bypass, but same string-
 *   matching fragility the finding called out): the force-push exemption
 *   required a trailing space after `-f`, so a bare trailing `-f` was missed
 *   as an exemption. Now checked as a real token.
 *
 * Fail-graceful: ANY parse error or git failure → exit 0 silently (unchanged
 * posture — this is a hygiene reminder, not a security gate; see ASM-* for
 * why full fail-closed was not adopted here).
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 */

'use strict';
const { spawnSync } = require('child_process');

const ROOT = __dirname;

// Same shell-boundary-anchored matcher family used elsewhere in this repo's
// hooks (claude-version-guard.js / claude-bow-ref-check.js /
// claude-plan-guard.js) — a real invocation at the start of the command or
// after a shell separator, never a bare mention inside a quoted string.
const GIT_PUSH_RE = /(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?push\b/;

/** Minimal shell-token splitter: whitespace-separated, respects single and
 *  double quotes. Good enough for parsing `git push [opts] [remote] [refspec]`
 *  out of a command string we already know starts with a real `git push`. */
function tokenize(str) {
  const tokens = [];
  let cur = '';
  let quote = null;
  for (let i = 0; i < str.length; i++) {
    const ch = str[i];
    if (quote) {
      if (ch === quote) { quote = null; }
      else { cur += ch; }
      continue;
    }
    if (ch === '"' || ch === "'") { quote = ch; continue; }
    if (/\s/.test(ch)) {
      if (cur) { tokens.push(cur); cur = ''; }
      continue;
    }
    cur += ch;
  }
  if (cur) tokens.push(cur);
  return tokens;
}

/** Run `git <args>` via spawnSync (no shell). Returns trimmed stdout, or
 *  null if the command failed for any reason. */
function git(args, timeout = 5000) {
  const result = spawnSync('git', args, { cwd: ROOT, encoding: 'utf8', timeout });
  if (result.error || result.status !== 0) return null;
  return (result.stdout || '').trim();
}

/** Parse the positional remote/refspec out of a `git push ...` command
 *  string. Returns { remote, branch } — either field may be null if the
 *  command didn't specify it (caller falls back to tracked upstream). */
function parsePushTarget(command) {
  const pushIdx = command.search(GIT_PUSH_RE);
  const afterPush = command.slice(command.indexOf('push', pushIdx) + 'push'.length);
  const tokens = tokenize(afterPush);
  const positional = [];
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (t.startsWith('-')) {
      // Skip flags. A few take a following value argument; skip that too.
      if (['-o', '--push-option', '--repo', '--receive-pack'].includes(t)) i += 1;
      continue;
    }
    positional.push(t);
  }
  const [remoteTok, refspecTok] = positional;
  let branch = null;
  if (refspecTok) {
    // refspec may be "local:remote" — the destination branch is the part
    // after the colon, or the whole thing if there's no colon.
    const colonIdx = refspecTok.indexOf(':');
    branch = colonIdx >= 0 ? refspecTok.slice(colonIdx + 1) : refspecTok;
  }
  return { remote: remoteTok || null, branch: branch || null };
}

/** Was this push invocation a force-push? Checked as real tokens, not a
 *  substring-with-trailing-space hack. */
function isForcePush(command) {
  const tokens = tokenize(command);
  return tokens.some(t => t === '-f' || t === '--force' || t === '--force-with-lease' || /^--force-with-lease=/.test(t));
}

let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => { input += chunk; });
process.stdin.on('end', () => {
  try {
    if (process.env.CLAUDE_DISABLE_PUSH_CHECK === '1') {
      process.exit(0);
    }

    const BOM = String.fromCharCode(0xFEFF);
    const data = JSON.parse(input.startsWith(BOM) ? input.slice(BOM.length) : input);
    const command = data?.tool_input?.command ?? '';

    // Only intercept real `git push` invocations, for ANY remote/branch
    // (SEC-012 fix — no longer gated on "main"/"origin" appearing in text).
    if (!GIT_PUSH_RE.test(command)) {
      process.exit(0);
    }

    // Skip force-push (assume the user knows what they're doing) — same
    // policy as before, now token-matched rather than substring-matched.
    if (isForcePush(command)) {
      process.exit(0);
    }

    // Resolve the actual destination remote/branch: from the command's own
    // positional args first, falling back to the current branch's tracked
    // upstream. Never inferred from substrings in the command text.
    const { remote: cmdRemote, branch: cmdBranch } = parsePushTarget(command);
    let remote = cmdRemote;
    let branch = cmdBranch;

    if (!remote || !branch) {
      const upstream = git(['rev-parse', '--abbrev-ref', '--symbolic-full-name', '@{u}']);
      if (upstream && upstream.includes('/')) {
        const slash = upstream.indexOf('/');
        remote = remote || upstream.slice(0, slash);
        branch = branch || upstream.slice(slash + 1);
      }
    }

    if (!remote || !branch) {
      // Can't determine destination (no upstream, ambiguous refspec) —
      // repo state unclear, don't block.
      process.exit(0);
    }

    // Find what's about to be pushed: commits on HEAD that aren't on the
    // resolved destination ref.
    const pendingCommits = git(['log', `${remote}/${branch}..HEAD`, '--pretty=format:%H']);
    if (pendingCommits === null || !pendingCommits) {
      process.exit(0); // nothing to push, or destination ref doesn't exist locally yet
    }

    const commitHashes = pendingCommits.split('\n').filter(Boolean);
    const offendingCommits = [];

    for (const hash of commitHashes) {
      // Did this commit touch functions/?
      const filesChanged = git(['show', hash, '--name-only', '--pretty=format:']);
      if (filesChanged === null) continue; // skip on error
      const touchesFunctions = filesChanged.split('\n').some(f => f.startsWith('functions/'));
      if (!touchesFunctions) continue;

      // Get full message body.
      const messageBody = git(['show', hash, '--pretty=format:%B', '--no-patch']);
      if (messageBody === null) continue;

      // Does the body contain a firebase deploy --only functions line?
      const deployRegex = /firebase\s+deploy\s+--only\s+functions/i;
      if (!deployRegex.test(messageBody)) {
        offendingCommits.push({ hash: hash.slice(0, 8), msg: messageBody.split('\n')[0] });
      }
    }

    if (offendingCommits.length === 0) {
      process.exit(0);
    }

    // Block with an instructional message
    const list = offendingCommits.map(c => `   ${c.hash} — ${c.msg}`).join('\n');
    const reason = '🛑 GOLDEN RULE #19: Cloud Functions deploy command missing.\n' +
      '\n' +
      `The following commits touch functions/ but their messages don't bundle a\n` +
      `firebase deploy --only functions:... command (target: ${remote}/${branch}):\n` +
      list + '\n' +
      '\n' +
      'Cloud Functions are NOT auto-deployed by App Hosting. Each commit changing\n' +
      'functions/ MUST end with the bundled deploy command including ALL pending\n' +
      'function changes from prior commits.\n' +
      '\n' +
      'Fix: amend the offending commit messages with the deploy command, OR\n' +
      'acknowledge by setting CLAUDE_DISABLE_PUSH_CHECK=1 (e.g. for pure docs\n' +
      'commits where no actual function code changed).';

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
    // Any unexpected error — don't block
    process.exit(0);
  }
});
