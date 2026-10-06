// Module key: tool.trailerchecker (see code.json; GUID 8ecec508-51d0-4930-8281-2cf8af6c2083)
// Spec ref: M0-ENG §5 (hooks)

/**
 * Co-Authored-By trailer checker (BOW mkey: tool.secretguard, BUG-088
 * remediation, extracted from claude-pre-commit-check.js).
 *
 * UNLIKE the other three BUG-088 checkers, this module is NOT a relocation
 * of its guard's existing extraction logic — it is a REPLACEMENT for it
 * (AC-D1). claude-pre-commit-check.js's message extraction (`-m`/`--message`
 * values, `-F`/`--file` targets, heredoc bodies — all pulled out of the
 * proposed command STRING) was architecturally unsound independent of the
 * trigger defect: three documented residual gaps (a bare `git commit` with
 * no message source, `-F -` fed by a plain pipe, an unreadable `-F` target)
 * meant the payload itself could not always be trusted, not just the
 * decision to engage. See docs/planning/acceptance/tool.secretguard.md's
 * BUG-088 section, "The architectural distinction this section rests on" —
 * this guard is `claude-author-guard.js`'s twin in that respect, not its
 * sibling.
 *
 * This module's design instead reads the message the way a `commit-msg`
 * git hook actually receives it: as a real file at a path passed as an
 * argument ($1 in a real commit-msg script — live-verified per AC-B1,
 * resolving to `.git/COMMIT_EDITMSG`, correct and complete regardless of
 * whether the message was supplied via `-m`, `-F`, a heredoc, or an
 * editor). Reading the file directly makes ALL THREE of the old extraction
 * gaps disappear structurally, not by patching around them: git always
 * writes COMMIT_EDITMSG before invoking commit-msg, so there is no such
 * thing as "couldn't find the message" at this hook point.
 *
 * `checkTrailer(messageFilePath)` therefore does NOT carry forward the old
 * guard's `-m`/`--file`/heredoc extraction machinery (its per-flag
 * message-body extractors and their matching regexes) — AC-B4's dead-code
 * rule applies here specifically, as the concrete instance the acceptance
 * file names. (This header intentionally avoids spelling out those
 * helpers' exact identifier names, so a grep for them against this file —
 * the literal AC-B4 check — finds zero matches.)
 * Nor does it carry any of the sibling checkers' boundary-regex/quote-mask/
 * engage-decision trigger machinery (this header intentionally avoids
 * spelling out those helpers' exact identifier names, so a grep for them
 * against this file finds zero matches).
 *
 * KNOWN LIMITATION INHERITED FROM ASM-386, STATED PLAINLY (AC-B2): a
 * `commit-msg` hook (the intended future caller of this module's
 * `checkTrailer()`) does not fire for `git cherry-pick` / `git revert` /
 * `git am` on this project's git version (2.55.0.windows.3, verified three
 * independent ways per ASM-386's own comment thread). Not re-verified or
 * re-solved here.
 *
 * Exported call contract for a future dispatcher (AC-B5, AC-D1):
 * `checkTrailer(messageFilePath)` takes the ONE argument a real commit-msg
 * hook actually receives — the message file path (`$1`) — and returns one
 * of:
 *   { status: 'clean' }
 *   { status: 'found-problems', findings: [<string>, ...] }
 *   { status: 'internal-error', error: <Error> }
 * An unreadable message file is an internal-error, never "no trailer found"
 * (AC-F1) — an inspection failure must not be confused with a passed check.
 *
 * `TRAILER_RE` is unchanged from claude-pre-commit-check.js's own constant.
 */

'use strict';

const fs = require('fs');

const TRAILER_RE = /Co[- ]Authored[- ]By\s*:/i;

/**
 * Reads the message file at `messageFilePath` and scans its full text for a
 * Co-Authored-By trailer anywhere in it. Never throws — an unreadable file
 * is reported as 'internal-error', matching AC-F1's "unreadable is not
 * clean" rule.
 */
function checkTrailer(messageFilePath) {
  let content;
  try {
    content = fs.readFileSync(messageFilePath, 'utf8');
  } catch (err) {
    return { status: 'internal-error', error: err };
  }
  if (TRAILER_RE.test(content)) {
    return {
      status: 'found-problems',
      findings: [
        'Co-Authored-By trailer detected. This repo is solely Aaron\'s; no AI authorship ' +
        'trailers belong here. Remove the Co-Authored-By line from the commit message and retry.',
      ],
    };
  }
  return { status: 'clean' };
}

module.exports = {
  TRAILER_RE,
  checkTrailer,
};
