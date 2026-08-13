/**
 * PreToolUse hook — Prix Six commit-style enforcement, DEMOTED TO ADVISORY
 * (BUG-088, 2026-08-11; BOW mkey: tool.secretguard).
 *
 * ============================================================================
 * THE DEMOTION (BUG-088) — READ THIS FIRST.
 * ============================================================================
 *
 * This file used to catch `git commit` commands whose message body contains
 * a `Co-Authored-By:` trailer and DENY them. BUG-088 found this guard is
 * architecturally `claude-author-guard.js`'s twin, not its sibling: not only
 * was its *trigger* (`isRealGitCommit()` below, parsed from the proposed
 * command STRING) defeated by any leading word, shell wrapper, or
 * non-bareword git invocation — its *payload* (message extraction:
 * `-m`/`--file`/heredoc, ALSO parsed from that same command string) was
 * equally unsound, with three documented residual gaps of its own (a bare
 * `git commit` with no message source, `-F -` fed by a plain pipe, an
 * unreadable `-F` target — see the @FIX history below, retained unchanged).
 * Per `claude-author-guard.js`'s own precedent (FEAT-045, Aaron's ruling):
 * fixing the trigger alone does not fix a check whose payload is equally
 * unsound, so this file gets the SAME demotion FEAT-045 already gave
 * claude-author-guard.js, for the same reason.
 *
 * THIS FILE NO LONGER EMITS A BLOCKING DECISION OF ANY KIND (AC-C3). No code
 * path here ever produces the harness-blocking permissionDecision value
 * (spelled without the adjacent literal here on purpose, matching
 * claude-author-guard.js's own convention, so a grep for it against this
 * file finds zero matches) — every path exits 0. The detection machinery
 * below (message extraction + TRAILER_RE matching, unchanged internals —
 * the same command-text-parsing machinery that BUG-088 found equally
 * unsound as the trigger stays exactly as unsound as it is; demotion
 * changes what happens with a positive detection, not the detection quality
 * itself) still runs, and when it believes it has found a trailer, this
 * file now says so as INFORMATION ONLY (a non-blocking advisory reason) —
 * never as a decision the harness could act on as a pause or refusal.
 *
 * THE REAL CONTROL, GOING FORWARD: a future `commit-msg` git hook (out of
 * scope for this dispatch — see docs/planning/acceptance/tool.secretguard.md's
 * BUG-088 section) that reads the composed message directly from the file
 * git passes it ($1, resolving to COMMIT_EDITMSG) via the newly-extracted
 * claude-trailer-checker.js module (AC-D1) — no command-text parsing, no
 * engage decision, no residual extraction gaps, because git always writes
 * that file before invoking commit-msg regardless of which flag supplied
 * the message. Until that hook is wired in (a follow-on integration
 * dispatch, per the acceptance file's Escalations), THIS file's advisory
 * warning is the only signal at all for a Co-Authored-By trailer — same
 * "before vs at" tradeoff already documented for claude-author-guard.js:
 * nothing is lost from the index/working tree by a later commit-msg catch,
 * only the convenience of being warned off before the commit is attempted.
 *
 * FAIL-OPEN, matching claude-author-guard.js's inversion (AC-C3 table):
 * every internal error in this file (fs read failure, unparseable stdin,
 * any uncaught exception) now results in a silent, non-blocking exit 0.
 * This is the opposite of the future commit-msg hook's expected fail-closed
 * posture on the identical condition — the two layers deliberately
 * disagree because a false pause at this advisory layer used to cost a
 * human/agent seconds for nothing, and this file is no longer a hard stop.
 *
 * `git commit --no-verify` and the ASM-386 cherry-pick/revert/am gap are
 * unaffected by this file either way — see claude-trailer-checker.js's
 * header for the details; not re-stated here.
 *
 * Everything below this point (message extraction, TRAILER_RE, the
 * detection functions) is HISTORICAL — retained unchanged as the record of
 * why this guard's payload was never sound independent of its trigger, and
 * because the same regex (TRAILER_RE) is what claude-trailer-checker.js
 * also uses (unchanged) for the real, future enforcement point.
 *
 * ---------------------------------------------------------------------
 * ORIGINAL HEADER (pre-BUG-088, retained for history) — READ AS PAST TENSE.
 * ---------------------------------------------------------------------
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
 * @FIX (BUG-043, class fix ported from claude-author-guard.js): GIT_COMMIT_RE's
 *   boundary class (`[;&|(\n]`) exists to catch a real invocation hidden after
 *   a shell separator, but a regex has no notion of "inside a string
 *   literal" — a BOW comment quoting the phrase "...(git commit ... is the
 *   bypass...)" matched the `(` boundary exactly as if it were real shell
 *   syntax, and this guard then went looking for a message source that did
 *   not exist because there was no real commit at all (see BUG-043 — the
 *   guard denied its own bug report). Fixed by porting buildQuoteMask() from
 *   claude-author-guard.js (that guard's own BUG-043 fix, Tester-verified)
 *   unchanged: a same-length boolean mask over the command text marking
 *   which positions sit inside an open quoted region or heredoc body. Any
 *   GIT_COMMIT_RE match whose "git" token falls inside that mask is now
 *   skipped. Detection of a REAL invocation immediately after a genuine,
 *   UNQUOTED `(`/`;`/`&`/`|`/newline is unchanged — the fix is knowing which
 *   side of a quote the boundary sits on, not removing the boundary class.
 *   See buildQuoteMask()'s own comment below for the KNOWN LIMITATION
 *   (ASM-344) carried forward from the author guard: this is a toggle, not a
 *   real shell lexer, and a deliberately unbalanced quote earlier in the
 *   string can still flip parity for everything after it.
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
 * (HISTORICAL — pre-demotion; see the DEMOTION block at the top of this
 * file for what this hook actually emits now: never a blocking decision.)
 */

'use strict';

const fs = require('fs');
const path = require('path');
// BUG-123 round 6: buildQuoteMask() (and its heredoc helpers) moved to a
// single shared module, claude-quote-mask.js — see that file's header for
// why this file no longer carries its own copy.
const { buildQuoteMask } = require('./claude-quote-mask.js');

// Same shell-boundary-anchored `git commit` matcher used by
// claude-version-guard.js / claude-bow-ref-check.js (SEC-008 fix): matches a
// real invocation at the start of the command or after a shell separator,
// never a bare mention inside a quoted string. 'g' flag added for BUG-043 —
// isRealGitCommit() below needs to walk every match, not just the first.
const GIT_COMMIT_RE = /(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/g;

// ---------------------------------------------------------------------------
// Quote-state tracking for GIT_COMMIT_RE (BUG-043) — buildQuoteMask() (and
// its heredoc helpers) now lives in claude-quote-mask.js and is imported at
// the top of this file (BUG-123 round 6). It was previously a standalone
// hand-copy of claude-author-guard.js's implementation, kept deliberately
// separate on the argument that a shared dependency could break all guards
// at once if broken; the round-6 ruling supersedes that with GR#3 (a single
// source of truth for this exact scanner, given three independent copies had
// each proven capable of drifting) — see claude-quote-mask.js's own header.
// ---------------------------------------------------------------------------

/** True if `command` contains a REAL `git commit` invocation — a
 * GIT_COMMIT_RE match whose "git" token is NOT inside a quoted argument or
 * heredoc body (BUG-043). Walks every match rather than stopping at the
 * first, so a quoted mention earlier in the string never masks a real
 * invocation later in the same command. */
function isRealGitCommit(command) {
  const mask = buildQuoteMask(command);
  GIT_COMMIT_RE.lastIndex = 0;
  let m;
  while ((m = GIT_COMMIT_RE.exec(command)) !== null) {
    const gitPos = m.index + m[0].toLowerCase().lastIndexOf('git');
    if (!mask[gitPos]) return true;
  }
  return false;
}

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

/** BUG-088 DEMOTION (matching claude-author-guard.js's advise() exactly):
 * emits a non-blocking, harness-visible reason via the SAME field the old
 * blocking decision used to travel next to (hookSpecificOutput /
 * permissionDecisionReason) but with the decision itself set to the
 * harness's non-pausing value — proceeds without pausing, exactly like a
 * silent exit(0), just with a message attached. Always exits 0. This is the
 * ONLY place in this file that writes hookSpecificOutput any more. */
function advise(reason) {
  process.stdout.write(
    JSON.stringify({
      hookSpecificOutput: {
        hookEventName: 'PreToolUse',
        permissionDecision: 'allow',
        permissionDecisionReason: reason,
      },
    })
  );
  process.exit(0);
}

function main() {
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

    // Only intercept real `git commit` invocations (SEC-008 fix, quote-aware
    // since BUG-043).
    if (!isRealGitCommit(command)) {
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

    // BUG-088 DEMOTION: previously denied (fail-closed) if any -F target
    // genuinely could not be read. Now advisory-only — see the file header.
    if (unreadableNotes.length > 0) {
      advise(
        '⚠️ PRIX SIX COMMIT POLICY (advisory only, BUG-088): could not verify this commit ' +
        'message is free of a Co-Authored-By trailer.\n\n' +
        `${unreadableNotes.join('\n')}\n\n` +
        'This is a WARNING, not a block — the real control is the future commit-msg hook ' +
        '(fail-closed, at commit time; see claude-trailer-checker.js).'
      );
      return;
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
    // BUG-088 DEMOTION: previously denied here too — same rationale, now
    // advisory-only.
    if (messages.length === 0 && filePaths.length === 0) {
      advise(
        '⚠️ PRIX SIX COMMIT POLICY (advisory only, BUG-088): could not find a message source ' +
        '(-m/--message, -F/--file, or a heredoc) for this `git commit`, so the Co-Authored-By ' +
        'check could not be performed. This is a WARNING, not a block — the real control is ' +
        'the future commit-msg hook (fail-closed, at commit time; see claude-trailer-checker.js).'
      );
      return;
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

    // BUG-088 DEMOTION: trailer found — previously blocked; now an advisory
    // only (see the file header). The real control is the future commit-msg
    // hook via claude-trailer-checker.js.
    advise(
      '⚠️ PRIX SIX COMMIT POLICY (advisory only, BUG-088): Co-Authored-By trailer detected.\n' +
      '\n' +
      'This repo is solely Aaron\'s; no AI authorship trailers belong here. This is a WARNING, ' +
      'not a block — the real control is now the future commit-msg hook (fail-closed, at ' +
      'commit time; see claude-trailer-checker.js).'
    );

  } catch (err) {
    // Fail-OPEN (BUG-088 demotion, inverted from the pre-demotion posture):
    // any internal error here — parse failure, fs error, or unexpected
    // hook-input shape — never blocks. This layer is no longer a hard stop.
    process.exit(0);
  }
});
}

// require.main === module guard (same pattern as claude-secret-guard.js,
// added here for BUG-043 testability): when run directly as the hook (the
// only way PreToolUse ever invokes it), behaviour is unchanged. When
// require()'d by a test harness, main() is never called (so stdin is never
// touched) and the pure helper functions below are exported instead.
if (require.main === module) {
  main();
} else {
  module.exports = {
    GIT_COMMIT_RE,
    buildQuoteMask,
    isRealGitCommit,
    extractMFlagMessages,
    extractFileFlagPaths,
    extractHeredocBodies,
  };
}
