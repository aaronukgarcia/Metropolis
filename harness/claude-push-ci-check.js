#!/usr/bin/env node
// claude-push-ci-check.js — GR#28 enforcement hook (Aaron, 2026-08-31).
// PostToolUse(Bash) fail-OPEN reminder: after a `git push`, emit additionalContext
// telling the session to VERIFY CI (a push to main bypasses the 4 required checks;
// the bypass SKIPS them, it does not pass them — checking CI is the only backstop).
// Also fires BEFORE-style advice when a git push runs without a recent /ci-green.
// This is a REMINDER, not a blocker (fail-open) — it never denies the push; it makes
// the "did you check CI?" step impossible to silently skip.
//
// Reads the PreToolUse/PostToolUse JSON on stdin. Emits hookSpecificOutput with
// additionalContext when the command is a git push. Exits 0 always (fail-open).

'use strict';

function readStdin() {
  return new Promise((resolve) => {
    let data = '';
    process.stdin.on('data', (c) => (data += c));
    process.stdin.on('end', () => resolve(data));
    // Guard: if no stdin arrives quickly, don't hang the hook.
    setTimeout(() => resolve(data), 2000).unref?.();
  });
}

(async () => {
  try {
    const raw = await readStdin();
    if (!raw) { process.exit(0); }
    let payload;
    try { payload = JSON.parse(raw); } catch { process.exit(0); }

    const cmd =
      payload?.tool_input?.command ??
      payload?.toolInput?.command ??
      '';
    if (typeof cmd !== 'string' || !cmd) { process.exit(0); }

    // Only care about git push invocations.
    if (!/\bgit\s+push\b/.test(cmd)) { process.exit(0); }

    const toMain = /HEAD:main|origin\s+main|:\s*main\b/.test(cmd);
    const lines = [
      'GR#28 — VERIFY AGAINST CI. A `git push`' + (toMain ? ' to main' : '') + ' just ran.',
      'A push to main BYPASSES the 4 required checks ("Bypassed rule violations") — the bypass',
      'SKIPS the checks, it does not pass them. Local gates are NOT CI.',
      'NOW: `gh run list --branch main --limit 1` then `gh run view <id>`. The push is not done until CI is seen.',
      'If a job/test is red that is NOT in the known-red baseline (memory: metropolis-ci-known-red-baseline),',
      'it is a NEW regression you introduced this push — fix it same-session as a P1 (BUG-456 tracks greening CI).',
      'Before the NEXT push, run /ci-green (golangci-lint at the pinned CI version — go vet is NOT enough).',
    ];

    const out = {
      hookSpecificOutput: {
        hookEventName: payload?.hook_event_name ?? 'PostToolUse',
        additionalContext: lines.join('\n'),
      },
    };
    process.stdout.write(JSON.stringify(out));
    process.exit(0);
  } catch {
    // Fail open — a reminder hook must never block or crash a push flow.
    process.exit(0);
  }
})();
