// Module key: tool.memoryprefetch (see code.json; GUID 4f83f462-e5a9-48b7-9094-8565789fc1e7)
// Spec ref: GR#14; M0-ENG §5 (hooks)

/**
 * UserPromptSubmit hook — Metropolis memory pre-fetch reminder (GR#14).
 *
 * On every user prompt, output a short reminder for Claude to query Vestige
 * for project-specific rules before composing significant actions (commit
 * messages, deploy commands, security-sensitive code). The reminder is
 * appended to the conversation as user-prompt-submit-hook context.
 *
 * This is a STATIC reminder — it doesn't actually call Vestige (the script
 * has no MCP access). The reminder just nudges Claude to query memory at
 * the moment of action, not just at session start.
 *
 * Fail-graceful: any error → exit 0 silently. The reminder is nice-to-have,
 * not blocking.
 *
 * Disable with CLAUDE_DISABLE_MEMORY_REMINDER=1.
 */

'use strict';

try {
  if (process.env.CLAUDE_DISABLE_MEMORY_REMINDER === '1') {
    process.exit(0);
  }

  // The reminder. Kept short to minimise noise on every prompt.
  const reminder = [
    'GR#14 reminder — before composing commit messages, deploy commands, or',
    'security-sensitive code, query Vestige (mcp__vestige__search) for project-',
    'specific rules. The /commit and /deploy skills GATE 0 already handle this',
    'for those flows; for ad-hoc requests, do it manually. Common queries:',
    '  "metropolis commit style attribution"',
    '  "metropolis deploy verification"',
    '  "metropolis <feature-area> rule"',
  ].join('\n');

  process.stdout.write(reminder);
  process.exit(0);
} catch (err) {
  process.exit(0);
}
