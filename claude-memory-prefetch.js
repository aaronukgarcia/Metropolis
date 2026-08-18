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

  // GR#4 mechanical prefix enforcement (Aaron, 2026-08-18: a memory-based
  // prefix rule provably lapsed for hours in the 08-18 lead session — the
  // "who" check caught it. Fix = the harness reminds on EVERY prompt).
  // Reads the same .identity file claude-statusline.js uses; fails open.
  let prefixLine = '';
  try {
    const fs = require('fs');
    const path = require('path');
    // Per-session resolution (Bill's cross-window finding, 2026-08-18): the
    // shared .identity file is last-checkin-wins across windows, so prefer
    // the per-session marker claude-startup writes (.identity-<session_id>,
    // session_id from the hook's stdin JSON payload). Fall back to the
    // shared file only when no per-session marker exists.
    let idFile = path.join(__dirname, '.claude', '.identity');
    try {
      const stdin = fs.readFileSync(0, 'utf8');
      const sid = JSON.parse(stdin).session_id;
      if (sid && /^[A-Za-z0-9-]{8,64}$/.test(sid)) {
        const perSession = path.join(__dirname, '.claude', '.identity-' + sid);
        if (fs.existsSync(perSession)) idFile = perSession;
      }
    } catch (_) { /* no/unparseable stdin -> shared-file fallback */ }
    if (fs.existsSync(idFile)) {
      const name = String(fs.readFileSync(idFile, 'utf8')).trim().split(/\s/)[0];
      if (/^[A-Za-z]{2,16}$/.test(name)) {
        prefixLine =
          'GR#4: START your response with "' + name.toLowerCase() + '> " ' +
          '(every response, no exceptions).\n';
      }
    }
  } catch (_) { /* fail open — never block the prompt */ }

  // The reminder. Kept short to minimise noise on every prompt.
  const reminder = prefixLine + [
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
