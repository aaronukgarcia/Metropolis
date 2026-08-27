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
 *   2. AND the checkin's OWN output must be piped/redirected to a sink. This
 *      is PIPELINE-SCOPED, not command-global (BUG-346): the old check tested
 *      the WHOLE command string for a fixed allow-list of sinks, so it (a)
 *      missed every sink outside that list — `| findstr`, `| grep`, `> file`,
 *      `>> file` — and (b) could false-fire on an unrelated pipe elsewhere in
 *      a compound command (`checkin && ls | grep x`). We now isolate the text
 *      that belongs to the checkin's own pipeline — everything AFTER the
 *      checkin invocation, up to the next statement separator (`&&`, `||`,
 *      `;`, newline); a single `|` stays inside the pipeline — and flag it if
 *      that trailing text contains:
 *        - ANY pipe (`| grep`, `| findstr`, `| Select-String`, `| Out-Null`,
 *          `| head`, `| tail`, `| $null`, `| Tee-Object`, ...): the count
 *          line is handed to another command instead of being displayed.
 *        - ANY stdout / all-stream redirect to a file or null target
 *          (`> file`, `>> file`, `> $null`, `> NUL`, `> /dev/null`, `*> x`,
 *          `1> x`, `&> x`). A stderr-only redirect (`2> err`, the stream-merge
 *          `2>&1`) is NOT a sink — stdout (the UNREAD line) is still shown.
 *   Both conditions must hold — a bare `checkin` (no redirection at all), an
 *   unrelated command piped/redirected away, or a pipe on a DIFFERENT stage of
 *   a compound command never triggers this hook. `read` piping is out of
 *   scope on purpose: only checkin ever carried the message-loss risk.
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

/** Statement separators that END the checkin's pipeline. A single `|` is a
 *  pipe (stays inside the pipeline); `||`/`&&`/`;`/newline start a new
 *  statement, so anything after them is NOT the checkin's output. Matched
 *  earliest-wins by exec() so `| grep x || ls` truncates at the `||`, keeping
 *  the `| grep x` pipe in scope. `&&` and `||` are listed before bareword so
 *  the two-char operators win over an incidental single `&`/`|`. */
const STATEMENT_SEPARATOR_RE = /(\|\||&&|;|\r|\n)/;

/** Any pipe within the (already pipeline-scoped) trailing text. Because the
 *  text is truncated at `||`, every remaining `|` is a real pipe to another
 *  command — the sink swallows the UNREAD line regardless of WHICH command it
 *  is (`grep`, `findstr`, `Select-String`, `Out-Null`, `head`, `tail`, ...). */
const PIPE_SINK_RE = /\|/;

/** A stdout / all-stream redirect to a file or null target within the trailing
 *  text: `>`, `>>`, `1>`, `*>`, `*>>`, `&>`, `&>>` followed by a target.
 *  Deliberately EXCLUDES a stderr-only `2>` and the stream-merge `2>&1`
 *  (leading `2`, or a `>&` order) — those leave stdout (the UNREAD line)
 *  visible, so they are not message-hiding sinks. */
const REDIRECT_SINK_RE = /(?:(?:^|[^0-9>&])(?:\*|1)?|&)>{1,2}\s*[^\s&]/;

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

/** Given the raw command, return the slice of text that belongs to the
 *  checkin invocation's OWN pipeline: everything after the (first) checkin
 *  invocation, truncated at the next statement separator so a pipe/redirect
 *  on an UNRELATED stage of a compound command is not attributed to checkin.
 *  Returns null when the command does not invoke checkin at all. */
function checkinPipelineTail(command) {
  const cmd = String(command || '');
  const m = CHECKIN_INVOCATION_RE.exec(cmd);
  if (!m) return null;
  let tail = cmd.slice(m.index + m[0].length);
  const sep = STATEMENT_SEPARATOR_RE.exec(tail);
  if (sep) tail = tail.slice(0, sep.index);
  return tail;
}

/** True when the pipeline-scoped trailing text hands checkin's stdout to a
 *  sink — any pipe, or any stdout/all-stream redirect to a file/null. */
function hasPipelineSink(tail) {
  if (tail == null) return false;
  return PIPE_SINK_RE.test(tail) || REDIRECT_SINK_RE.test(tail);
}

/** Pure predicate, exported for the test file: does `command` invoke
 *  checkin AND pipe/redirect the checkin's OWN output through a sink? Both
 *  conditions required, and the sink must live in the checkin's own pipeline
 *  (see header — BUG-346 pipeline scoping). */
function isSuppressedCheckin(command) {
  return hasPipelineSink(checkinPipelineTail(command));
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
        'checkin through ANY sink (| grep / | findstr / | Select-String / ' +
        '| Out-Null / | head / | tail) or redirecting it to a file or null ' +
        '(> file / >> file / > $null / > NUL / > /dev/null) can hide that ' +
        'you have mail waiting on `read`. Run checkin bare, or capture its ' +
        'full stdout, and follow up with `node claude-sync.js read` if it ' +
        'reports any UNREAD count.'
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
    checkinPipelineTail,
    hasPipelineSink,
    CHECKIN_INVOCATION_RE,
    STATEMENT_SEPARATOR_RE,
    PIPE_SINK_RE,
    REDIRECT_SINK_RE,
  };
}
