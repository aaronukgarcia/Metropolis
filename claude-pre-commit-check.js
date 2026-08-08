/**
 * PreToolUse hook — Prix Six commit-style enforcement.
 *
 * Catches `git commit` commands whose message body contains a
 * `Co-Authored-By:` trailer. The project policy (commit.md GATE 0) is that
 * Aaron is the sole author and no Claude/AI authorship trailers belong on
 * commits in this repo. This hook makes the rule mechanical, so even when
 * Claude composes a commit message that slips a trailer in, the hook
 * blocks the commit before it lands.
 *
 * Fail-graceful: ANY parse error, ANY unexpected input shape → exit 0
 * silently. Never block legitimate work due to a hook bug.
 *
 * To disable: set env var `CLAUDE_DISABLE_COMMIT_CHECK=1` before commit.
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 * Returns JSON to block: { hookSpecificOutput: { permissionDecision: "deny", permissionDecisionReason: "..." } }
 */

'use strict';

let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => { input += chunk; });
process.stdin.on('end', () => {
  try {
    if (process.env.CLAUDE_DISABLE_COMMIT_CHECK === '1') {
      process.exit(0);
    }

    const data = JSON.parse(input);
    const command = data?.tool_input?.command ?? '';

    // Only intercept git commit commands
    if (!command.includes('git commit')) {
      process.exit(0);
    }

    // Skip --amend (use existing message) and merge commits
    if (command.includes('--amend')) {
      process.exit(0);
    }

    // Look for Co-Authored-By: trailer (case-insensitive variants)
    const trailer = /Co[- ]Authored[- ]By\s*:/i;
    if (!trailer.test(command)) {
      process.exit(0);
    }

    // Trailer found — block with an instructional message
    const reason = '🛑 PRIX SIX COMMIT POLICY: Co-Authored-By trailer detected.\n' +
      '\n' +
      'This repo is solely Aaron\'s; no AI authorship trailers belong here. The rule\n' +
      'is in commit.md and in Vestige memory (feedback_no_co_authored_by_lines).\n' +
      '\n' +
      'Remove the Co-Authored-By line from the commit message and try again.\n' +
      'If you intentionally need to bypass, set CLAUDE_DISABLE_COMMIT_CHECK=1.';

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
