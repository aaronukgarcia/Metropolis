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
 * @FIX (SEC-007, SEC-008 — Destructive-agent findings, 2026-08-09):
 *   - SEC-007: the old version only scanned the raw Bash `command` STRING for
 *     the trailer regex. `-m "..."` embeds the message in that string, so it
 *     was caught — but `git commit -F <file>` sources the message from a file
 *     the hook never read, so a trailer written that way sailed through with
 *     zero denial. Fixed by inspecting the ACTUAL MESSAGE CONTENT being
 *     committed rather than the invocation text: `-m`/`--message` values are
 *     extracted from the command (as before), `-F`/`--file <path>` now has
 *     its target file read from disk and scanned too, and (see the
 *     2026-08-09 follow-up below) heredoc bodies anywhere in the command are
 *     also extracted and scanned.
 *   - SEC-008: the intercept condition used to be a bare
 *     `command.includes('git commit')` substring test, which fires on the
 *     phrase appearing anywhere in the command text — including inside an
 *     unrelated quoted string that merely mentions "git commit" in prose.
 *     Replaced with the same shell-command-boundary-anchored regex already
 *     proven in claude-version-guard.js / claude-bow-ref-check.js: the
 *     phrase must appear at the start of the command or immediately after a
 *     shell separator (`;`, `&`, `|`, `(`, newline), so quoted mentions never
 *     match but every real invocation still does.
 *
 * @FIX (follow-up same day, Bill-directed — ASM-034 investigation): the
 *   first pass of the SEC-007 fix left `-F -` (stdin) and bare `git commit`
 *   (editor) as WARN-then-ALLOW — "we couldn't inspect it, so we allowed
 *   it," the exact hole SEC-007 existed to close, wearing a warning label.
 *   Investigated with evidence (scratch repo, non-interactive stdin, exactly
 *   as the Bash tool actually invokes commands) before deciding:
 *     1. Is it genuinely uninspectable? For this project's actual commit
 *        convention (commit.md: `-m "$(cat <<'EOF' ... EOF)"` heredoc-in-
 *        substitution) NO — the heredoc body is physically present in the
 *        raw `command` string the Bash tool captures, inside the already-
 *        scanned `-m "..."` span, and was ALREADY being caught (verified
 *        live). The one remaining real gap was `-F -` fed by a shell
 *        heredoc (`git commit -F - <<'EOF' ... EOF`) — also verified live to
 *        succeed non-interactively — whose body sits in the command string
 *        too but wasn't being extracted. Fixed by adding HEREDOC_RE
 *        extraction: every `<<'DELIM' ... DELIM` / `<<DELIM ... DELIM` body
 *        anywhere in the command is now pulled out and scanned, regardless
 *        of which flag (if any) consumes it.
 *     2. Post-commit fallback: not needed once (1) covers the realistic
 *        input space — see (3).
 *     3. Traffic through the literal bare-commit gap — `git commit` with NO
 *        -m and NO -F/--file flag AT ALL (not even `-F -`) — is verified
 *        ZERO by construction: (`git commit < /dev/null`) that git itself
 *        aborts such a commit with "Aborting commit due to empty commit
 *        message" the instant stdin isn't a TTY, which is exactly the Bash
 *        tool's invocation environment (no TTY, ever). There is no
 *        legitimate commit this case is protecting — denying it costs
 *        nothing. So this residual case, and an unreadable `-F <path>`
 *        target file (we truly cannot know what it contains), DENY rather
 *        than warn-and-allow: an inspection failure no longer lets the
 *        check pass.
 *
 * @FIX (Tester-2 finding, same day — narrows the claim above): the first
 *   version of this deny ALSO fired on `-F -` fed by a plain pipe with no
 *   heredoc (e.g. `echo "msg" | git commit -F -`), which Tester-2 proved
 *   live actually succeeds (exit 0, real commit, message read straight off
 *   the pipe) — the "verified ZERO" claim in (3) above was true only for
 *   the literal bare-commit case, not for every shape the deny condition
 *   actually matched. That was an over-block, not a bypass, but a deny
 *   reason that states something untrue ("cannot succeed non-interactively
 *   anyway") is worse than an honest warning. Narrowed per Bill's steer:
 *   the literal bare case (no -m, no -F AT ALL) still denies — nothing can
 *   legitimately be there. `-F -` fed by a plain pipe with no heredoc is
 *   content this hook genuinely cannot see either way (git consumes the
 *   pipe directly), so it now WARNS (stderr) and ALLOWS rather than
 *   blocking real, succeeding work to guard against something invisible to
 *   both sides. `-F -` fed BY a heredoc is unaffected — its body is still
 *   extracted and scanned via HEREDOC_RE, same as before.
 *
 * Fail-graceful in the sense that stayed: an unparseable/unexpected hook
 * INPUT (not a message-inspection failure — a genuine plumbing error, e.g.
 * malformed JSON on stdin) still exits 0 silently, because that failure mode
 * says nothing about the commit's content and denying every Bash command
 * over a stdin hiccup would brick the session. But once we know we're
 * looking at a real `git commit` and cannot verify its message, we now deny.
 *
 * To disable: set env var `CLAUDE_DISABLE_COMMIT_CHECK=1` before commit.
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 * Returns JSON to block: { hookSpecificOutput: { permissionDecision: "deny", permissionDecisionReason: "..." } }
 */

'use strict';

const fs = require('fs');
const path = require('path');

// Same shell-boundary-anchored `git commit` matcher used by
// claude-version-guard.js / claude-bow-ref-check.js (SEC-008 fix): matches a
// real invocation at the start of the command or after a shell separator,
// never a bare mention inside a quoted string.
const GIT_COMMIT_RE = /(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/;

// Matches -m "..." / -m '...' / --message="..." / --message '...', allowing
// escaped quotes inside the body. Multiple matches = multiple -m flags
// (git's own behaviour: each becomes a paragraph).
//
// @FIX (self-caught during re-verification, 2026-08-09): the base pattern
// (originally shared with claude-bow-ref-check.js's MSG_FLAG_RE) only
// matched QUOTED values. `git commit -m init` — a single unquoted shell
// word, which git accepts perfectly fine and which this hook's own live
// PreToolUse invocation actually hit while I was re-testing in this
// session — has no quotes for the regex to match, so messages.length stayed
// 0, filePaths.length was 0, and the new literal-bare-commit deny branch
// (added for ASM-034) fired on a real, ordinary, succeeding commit — false
// positive with a false claim ("cannot succeed non-interactively"). Added a
// third alternative for a bare non-whitespace token so `-m word` is
// extracted the same as `-m "word"`.
const MSG_FLAG_RE = /(?:-m|--message)(?:=|\s+)(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|(\S+))/g;

// Matches -F <path> / --file <path> / --file=<path>, with or without quotes.
const FILE_FLAG_RE = /(?:-F|--file)(?:=|\s+)(?:"([^"]+)"|'([^']+)'|(\S+))/g;

// Matches a POSIX heredoc body anywhere in the command: `<<'DELIM'` /
// `<<DELIM` / `<<-'DELIM'` (the `-` form allows leading tabs before the
// closing delimiter), through the first line that is exactly DELIM. This is
// what actually carries the message for both this project's documented
// `-m "$(cat <<'EOF' ... EOF)"` convention (redundant with MSG_FLAG_RE
// there, since the whole heredoc sits inside the `-m` quotes and is already
// captured by it, but harmless to also scan) and for `-F - <<'EOF' ... EOF`
// (stdin-fed), where it is the ONLY source of the message. Scans regardless
// of which flag consumes it — a false positive here (an unrelated heredoc
// in the same compound command) only means extra scanning, not a missed
// trailer, which is the correct direction to err in a fail-closed gate.
const HEREDOC_RE = /<<-?\s*(['"]?)([A-Za-z_][\w]*)\1[ \t]*\r?\n([\s\S]*?)\r?\n[ \t]*\2\b/g;

const TRAILER_RE = /Co[- ]Authored[- ]By\s*:/i;

/** Every -m/--message body found in the command, joined as git would. */
function extractMFlagMessages(command) {
  const parts = [];
  let m;
  MSG_FLAG_RE.lastIndex = 0;
  while ((m = MSG_FLAG_RE.exec(command))) {
    const raw = m[1] !== undefined ? m[1] : (m[2] !== undefined ? m[2] : m[3]);
    parts.push(raw.replace(/\\(["'\\])/g, '$1'));
  }
  return parts;
}

/** Every -F/--file target path found in the command ('-' = stdin). */
function extractFileFlagPaths(command) {
  const paths = [];
  let m;
  FILE_FLAG_RE.lastIndex = 0;
  while ((m = FILE_FLAG_RE.exec(command))) {
    const raw = m[1] !== undefined ? m[1] : (m[2] !== undefined ? m[2] : m[3]);
    if (raw) paths.push(raw);
  }
  return paths;
}

/** Every heredoc body found anywhere in the command. */
function extractHeredocBodies(command) {
  const bodies = [];
  let m;
  HEREDOC_RE.lastIndex = 0;
  while ((m = HEREDOC_RE.exec(command))) {
    bodies.push(m[3]);
  }
  return bodies;
}

let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => { input += chunk; });
process.stdin.on('end', () => {
  try {
    if (process.env.CLAUDE_DISABLE_COMMIT_CHECK === '1') {
      process.exit(0);
    }

    const data = JSON.parse(input.replace(/^\uFEFF/, ''));
    const command = data?.tool_input?.command ?? '';

    // Only intercept real `git commit` invocations (SEC-008 fix).
    if (!GIT_COMMIT_RE.test(command)) {
      process.exit(0);
    }

    // Skip --amend (use existing message) and merge commits.
    if (command.includes('--amend')) {
      process.exit(0);
    }

    // SEC-007 fix: gather the message from every source that can carry it —
    // -m/--message bodies, -F/--file targets (read from disk), and any
    // heredoc bodies present in the command text — not just a regex over
    // the raw command string.
    const messages = extractMFlagMessages(command);

    // @FIX (Tester-2 finding, 2026-08-09 — narrows the previous version):
    //   the deny condition originally fired on "no -m and no non-stdin -F
    //   target", which ALSO caught `echo "msg" | git commit -F -` — a real
    //   pipe with no heredoc for HEREDOC_RE to find. Tester-2 proved live
    //   that shape succeeds (exit 0, real commit, message read off the
    //   pipe), so denying it blocks legitimate work over content this hook
    //   genuinely cannot see either way — not the same as the literal bare
    //   `git commit` case, where NOTHING can be there because git itself
    //   aborts non-interactively on an empty message. Narrowed accordingly:
    //   only the literal bare case (no -m, no -F/--file AT ALL) denies;
    //   `-F -` fed by a plain pipe (no heredoc) now warns-and-allows, same
    //   as the original SEC-007 fix's posture for genuinely-unreadable
    //   content — see file header and ASM-034 for the full chain.
    const heredocBodies = extractHeredocBodies(command);
    messages.push(...heredocBodies);

    const filePaths = extractFileFlagPaths(command);
    const unreadableNotes = [];
    let pipedStdinUnverified = false;
    for (const rawPath of filePaths) {
      if (rawPath === '-') {
        // Message piped via stdin. If a heredoc supplied it (this project's
        // documented convention for doing that non-interactively), its body
        // was already captured by extractHeredocBodies above. If there is
        // no heredoc in the command at all, the message is coming from a
        // plain pipe (e.g. `echo "..." | git commit -F -`) — git consumes
        // that pipe directly; this hook has no way to read it before the
        // commit runs. That is genuinely unreadable, not "nothing there".
        if (heredocBodies.length === 0) pipedStdinUnverified = true;
        continue;
      }
      const resolved = path.isAbsolute(rawPath) ? rawPath : path.join(process.cwd(), rawPath);
      try {
        messages.push(fs.readFileSync(resolved, 'utf8'));
      } catch (err) {
        unreadableNotes.push(`the -F/--file target "${rawPath}" could not be read (${err.message})`);
      }
    }

    // Deny (fail-closed) if any -F target genuinely could not be read: an
    // inspection failure must not let the check pass (this is the fix for
    // the ASM-034 concern — see file header investigation notes).
    if (unreadableNotes.length > 0) {
      process.stdout.write(JSON.stringify({
        hookSpecificOutput: {
          hookEventName: 'PreToolUse',
          permissionDecision: 'deny',
          permissionDecisionReason:
            '🛑 PRIX SIX COMMIT POLICY: could not verify this commit message is free of a ' +
            'Co-Authored-By trailer.\n\n' +
            `${unreadableNotes.join('\n')}\n\n` +
            'An unreadable message source is treated as a failed check, not a passed one. ' +
            'Fix the -F/--file path, or if you are certain the content is clean, bypass with ' +
            'CLAUDE_DISABLE_COMMIT_CHECK=1.',
        },
      }));
      process.exit(0);
    }

    // No -m/--message, no -F/--file flag at ALL (not even `-F -`), and no
    // heredoc anywhere in the command: there is no message source this hook
    // (or git itself, non-interactively) can find. Verified live: `git
    // commit` with no message source and stdin not a TTY (exactly this
    // hook's invocation environment) aborts immediately with "Aborting
    // commit due to empty commit message" — no legitimate commit reaches
    // this state, so denying it costs nothing while closing the "couldn't
    // see, so we allowed it" hole. This is narrower than filePaths.length
    // === 0 && messages.length === 0 would suggest at a glance — a `-F -`
    // flag DOES count as "found a source" here even when its content is
    // unreadable, because that shape can genuinely succeed (see
    // pipedStdinUnverified below, handled separately as a warn).
    if (messages.length === 0 && filePaths.length === 0) {
      process.stdout.write(JSON.stringify({
        hookSpecificOutput: {
          hookEventName: 'PreToolUse',
          permissionDecision: 'deny',
          permissionDecisionReason:
            '🛑 PRIX SIX COMMIT POLICY: could not find a message source (-m/--message, ' +
            '-F/--file, or a heredoc) for this `git commit`, so the Co-Authored-By check ' +
            'could not be performed.\n\n' +
            'A commit with no discoverable message source cannot succeed non-interactively ' +
            'anyway (git aborts on an empty message when stdin is not a TTY) — use ' +
            '`-m "..."` or `-F <file>` instead. If you believe this is a false positive, ' +
            'bypass with CLAUDE_DISABLE_COMMIT_CHECK=1.',
        },
      }));
      process.exit(0);
    }

    if (pipedStdinUnverified && messages.length === 0) {
      // `-F -` fed by a plain pipe, no heredoc anywhere to recover the
      // content from: genuinely unreadable pre-commit. Warn loudly instead
      // of silently passing, but allow — denying would block a real,
      // succeeding commit over content we have no way to see either way.
      process.stderr.write(
        '⚠️  PRIX SIX COMMIT POLICY: this commit reads its message from `-F -` fed by a plain ' +
        'pipe (no heredoc found in the command) — the Co-Authored-By check could NOT be ' +
        'performed pre-commit, because git consumes that pipe directly and this hook cannot ' +
        'read it first. Please confirm the message yourself before it lands.\n'
      );
      process.exit(0);
    }

    const trailerFound = messages.some(msg => TRAILER_RE.test(msg));
    if (!trailerFound) {
      process.exit(0);
    }

    // Trailer found — block with an instructional message.
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
    // Parse error or unexpected hook-input shape — don't block. This is a
    // plumbing failure (can't even read what command ran), not a message-
    // inspection failure, so it stays fail-graceful.
    process.exit(0);
  }
});
