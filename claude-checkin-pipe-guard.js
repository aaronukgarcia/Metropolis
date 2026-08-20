// Spec ref: FEAT-107 (tool.sync delivery split); M0-ENG §5 (hooks)
// NOTE: no "Module key" line yet — this is a new hook file with no code.json
// GUID registration of its own. Registration (GR#20/#6) is the Architect's
// call at PR-audit time (/register-guid), not this builder's; the existing
// tool.sync GUID (eae1b5fc-9fc9-46fa-af15-5333c5db21f8, see claude-sync.js's
// own header) covers the delivery-split behaviour this hook exists to guard.

/**
 * PreToolUse hygiene hook — FEAT-107 "checkin pipe" guard.
 *
 * WHY THIS EXISTS.
 *
 * On 2026-08-20, `node claude-sync.js checkin` was piped to `$null` (a
 * habit picked up from callers who only wanted the "YOU ARE: <Name>" line
 * and treated the rest of stdout as noise). Under the PRE-FEAT-107 contract,
 * checkin both DELIVERED unread message bodies AND ADVANCED the read
 * cursor in the same call — so piping its stdout away didn't just hide the
 * messages, it destroyed them: the cursor moved past them with nobody ever
 * having read the text. Nine messages were lost this way.
 *
 * FEAT-107 (see claude-sync.js's own header + claude-sync.test.js's AC-7..
 * AC-12) fixes the root cause structurally: checkin now only ever prints an
 * UNREAD COUNT ("UNREAD: N message(s) - run read to receive them") and never
 * advances the cursor; `read` is the sole delivering + cursor-advancing
 * command. That means a piped/redirected checkin can no longer destroy a
 * message — at worst it hides the COUNT line, and the count is still sitting
 * there, undelivered, waiting for a `read`.
 *
 * So the STAKES of piping checkin's output dropped from "data loss" to
 * "you might miss finding out you have mail" — a hygiene concern, not a
 * safety one. This hook reflects that: it WARNS (fail-open, non-blocking),
 * exactly like claude-version-guard.js's warn-mode notes, never DENY. Its
 * only job is to catch the habit before it costs someone a missed message,
 * the same way version-guard nudges without gatekeeping.
 *
 * Detection (deliberately a plain regex over the raw command string, not a
 * real shell parse — see docs/planning/... "a regex is not a shell parser"
 * lesson from the fail-CLOSED guard family; that lesson is about guards
 * whose false negatives are a security hole. This hook fails open and only
 * ever WARNS, so a missed detection here costs nothing worse than the
 * pre-FEAT-107 status quo, and a false positive costs nothing worse than an
 * unnecessary reminder — the asymmetry that justifies the simpler check):
 *   1. The command must actually invoke `claude-sync.js checkin` (bareword
 *      match, tolerant of `node `, quoting, and trailing flags like --any /
 *      --name X / --force --human-ok).
 *   2. AND the command's output must be piped/redirected to one of:
 *        - $null / Out-Null (PowerShell)
 *        - > NUL / >nul (cmd.exe-style, case-insensitive)
 *        - > /dev/null (POSIX)
 *        - | Select-Object / | Select-String (PowerShell filters that can
 *          silently drop the UNREAD line if it doesn't match the filter)
 *        - | head / | tail (POSIX filters, same risk)
 *   Both conditions must hold — a bare `checkin` (no redirection at all) or
 *   an unrelated command piped to $null never triggers this hook.
 *
 * Receives JSON on stdin: { tool: "Bash"|"PowerShell", tool_input: { command: "..." } }
 * (same envelope shape as every other PreToolUse guard in this repo).
 * Returns JSON with permissionDecision "allow" + a reason to warn-but-allow.
 * Returns nothing (exit 0, no stdout) to allow silently.
 *
 * Escape hatch (same naming convention as the other guards):
 *   CLAUDE_DISABLE_CHECKIN_PIPE_GUARD=1
 */

'use strict';

/** Matches a `claude-sync.js checkin` invocation: tolerant of a leading
 *  `node `/path prefix, quoting around the script path, and any flags
 *  after `checkin` (--any, --name X, --force --human-ok, etc). Deliberately
 *  loose — see the header's asymmetry note: a false positive here just
 *  prints an unnecessary reminder, never blocks anything. */
const CHECKIN_INVOCATION_RE = /claude-sync\.js["'`]?\s+checkin\b/i;

/** Matches the specific redirect/filter shapes named in the FEAT-107 brief:
 *  PowerShell $null / Out-Null, cmd.exe/POSIX NUL / /dev/null redirects, and
 *  the Select-Object / Select-String / head / tail filter family that can
 *  silently swallow the UNREAD count line if it doesn't match the filter. */
const SUPPRESSING_SINK_RE = /(\|\s*(?:Out-Null|Select-Object|Select-String|head|tail)\b)|(>{1,2}\s*(?:\$null|\/dev\/null|nul\b))/i;

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

/** Pure predicate, exported for the test file: does `command` invoke
 *  checkin AND pipe/redirect its output through one of the suppressing
 *  sinks? Both conditions required (see header). */
function isSuppressedCheckin(command) {
  const cmd = String(command || '');
  return CHECKIN_INVOCATION_RE.test(cmd) && SUPPRESSING_SINK_RE.test(cmd);
}

function main() {
  let input = '';
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', chunk => { input += chunk; });
  process.stdin.on('end', () => {
    try {
      if (process.env.CLAUDE_DISABLE_CHECKIN_PIPE_GUARD === '1') {
        process.exit(0);
      }

      // Strip a leading BOM before parsing — same defensive fix as
      // claude-version-guard.js v3.1.11 (a BOM-prefixed stdin, e.g. piped
      // from PowerShell, would otherwise throw and fail open silently
      // without ever reaching the warn path).
      const data = JSON.parse(input.replace(/^﻿/, ''));
      const command = data?.tool_input?.command ?? '';

      if (!isSuppressedCheckin(command)) {
        process.exit(0);
      }

      warnAllow(
        'checkin output must be read in full, not piped/redirected away — ' +
        'as of FEAT-107, checkin only prints an UNREAD COUNT line (never ' +
        'message bodies, never advances the read cursor), but that count ' +
        'line still lives in the STARTUP SUMMARY region of stdout. Piping ' +
        'checkin to $null/Out-Null/NUL//dev/null or through a Select-' +
        'Object/Select-String/head/tail filter can hide that you have mail ' +
        'waiting on `read`. Run checkin bare, or capture its full stdout, ' +
        'and follow up with `node claude-sync.js read` if it reports any ' +
        'UNREAD count.'
      );
    } catch (err) {
      // Parse error or unexpected input — never block, this hook only warns.
      process.exit(0);
    }
  });
}

if (require.main === module) {
  main();
} else {
  module.exports = {
    isSuppressedCheckin,
    CHECKIN_INVOCATION_RE,
    SUPPRESSING_SINK_RE,
  };
}
