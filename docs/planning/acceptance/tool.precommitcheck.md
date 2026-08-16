BOW code: FEAT-109

# Acceptance criteria — tool.precommitcheck (FEAT-109)

**BOW code:** FEAT-109 (`tool.precommitcheck`)
**Module key:** tool.precommitcheck (GUID 99e74615-a772-41ae-9c92-10ba2092b75f)
**Spec refs:** M0-ENG §5 (hooks); GR#2 (Version/Identity Discipline — this
guard enforces the complementary "sole author" half of commit policy: commit.md
GATE 0, no AI/Co-Authored-By trailers in a repo that is solely Aaron's, the same
BUG-042-adjacent identity hygiene as the merge-identity rule); GR#18 (Migration
Dead-Code Audit — the retained extraction machinery below is the live instance
of why this matters);
BUG-088 (the demotion — this guard is `claude-author-guard.js`'s *twin*, not
its sibling); `docs/planning/acceptance/tool.secretguard.md`'s BUG-088 section
(the architectural distinction table that classified this guard's payload as
unsound); `docs/planning/acceptance/tool.committhook.md` (FEAT-045, the
"demote to advisory / invert-not-delete" precedent); `claude-trailer-checker.js`
(the real, future enforcement point this guard now only warns toward).
**Date:** 2026-08-16 (written after the fact — this is archaeology, not a
pre-dispatch brief; the code shipped already and the criteria document the
contract the committed code actually holds)
**Status:** retrospective
**Package under test:** `claude-pre-commit-check.js` (repo root, Node.js) —
a `PreToolUse` hook wired on both the `Bash` and `PowerShell` matchers in
`.claude/settings.json`.
**Standard gates:** Node, not Go — SG-1/2/4/7 do not apply. This item's own
gates: `node --check claude-pre-commit-check.js`; `node --test
claude-pre-commit-check.test.js`; SG-6 (no Co-Authored-By).

## Why this file exists, and what it is now

This hook is the authorship-trailer half of the commit-policy pair. It is
**demoted to advisory-only** (BUG-088), and the demotion is the whole contract:
the file no longer emits a blocking decision of any kind. Every code path exits
0; a positive detection (a `Co-Authored-By:` trailer) surfaces as a non-blocking
warning, never a `deny`/`ask`. The real enforcement is `claude-trailer-checker.js`
(fail-closed, at commit time) once a follow-on integration wires it into a
`commit-msg` git hook. Until then, this file's warning is the *only* pre-commit
signal for a trailer, and it is deliberately fail-open.

The message-extraction machinery below (`-m`/`--message`, `-F`/`--file`, heredoc
bodies) is **historical, retained unchanged as the record of why this guard's
payload was never sound** — not because it still governs a decision. It runs,
but its only consequence is choosing which advisory warning (if any) to print.
This file documents the demoted contract; it does not re-litigate BUG-088.

## What this guard actually guarantees (read from the code, not assumed)

### A. Engagement — when it even looks at a command

- **AC-1.** Only a real `git commit` invocation engages the check. Engagement
  is boundary-anchored (`GIT_COMMIT_RE` — start-of-string or immediately after a
  shell separator `;`/`&`/`|`/`(`/newline) and quote-mask aware
  (`buildQuoteMask`, imported from `claude-quote-mask.js`): a `git commit`
  phrase sitting inside a quoted string or heredoc body does not engage. Check:
  `claude-pre-commit-check.test.js` already asserts `isRealGitCommit` returns
  `false` for a quoted `"... (git commit --author=... ...)"` prose mention and
  `true` for `(git commit --author="Fake <fake@evil.com>" -m x)` — the BUG-043
  regression pair. A passing test additionally asserts `git -C somedir commit`
  (the `-C` global-flag form) is recognised.
- **AC-2.** A non-`git commit` command exits 0 immediately with no stdout.
  Check: a test feeds `{"tool":"Bash","tool_input":{"command":"git status"}}`
  and asserts exit 0, empty stdout.
- **AC-3.** `git commit --amend` is skipped after engagement (it reuses the
  existing message, so there is no newly-composed trailer to warn about).
  Check: `grep -n -- "--amend" claude-pre-commit-check.js` shows the skip; a
  passing test feeds a `git commit --amend -m x` command and asserts exit 0 with
  no advisory output.

### B. Message-source extraction (historical, retained-inert)

- **AC-4.** `TRAILER_RE` is the detection predicate, and it is the one piece of
  this file that is *also* carried forward unchanged into
  `claude-trailer-checker.js`: `/Co[- ]Authored[- ]By\s*:/i` — matching
  `Co-Authored-By`, `Co Authored By`, `Co-Authored By`, etc., case-insensitive.
  Check: `grep -n "TRAILER_RE" claude-pre-commit-check.js
  claude-trailer-checker.js` shows the same literal regex in both (GR#3 — one
  detection predicate, not two that can drift).
- **AC-5.** The three historical message sources are extracted from the command
  text: `-m`/`--message` bodies (`MSG_FLAG_RE`, including the bare unquoted
  `-m init` form), `-F`/`--file` targets read from disk (`FILE_FLAG_RE`), and
  heredoc bodies anywhere in the command (`HEREDOC_RE`). These are retained
  unchanged — this AC documents *presence*, not *soundness* (BUG-088 already
  ruled the payload unsound; the demotion in Section C is the response).
  Check: `grep -n "MSG_FLAG_RE\|FILE_FLAG_RE\|HEREDOC_RE" claude-pre-commit-check.js`
  finds all three; the AC explicitly does **not** require them to be trust-worthy,
  because no decision rests on them any more.

### C. The demotion — advisory-only, fail-open

- **AC-6.** No code path emits a blocking decision. Zero `permissionDecision`
  values of `deny` or `ask` exist anywhere in the file — not "zero reachable
  from the entry point," zero full stop, so a later edit cannot accidentally
  reconnect a dead blocking branch. Check:
  `grep -n "permissionDecision.*['\"]deny['\"]" claude-pre-commit-check.js` and
  the `"ask"` equivalent both find zero matches. This grep is the same floor
  `tool.committhook.md` AC-6 mandates for `claude-author-guard.js`; a reviewer
  confirms by eye that no renamed equivalent field exists (a grep cannot prove a
  negative against an unknown future field name).
- **AC-7.** Every code path exits 0, including the positive-detection paths.
  Check: `claude-pre-commit-check.test.js` AC-C3 already asserts a `git commit
  -m "...\n\nCo-Authored-By: Claude <noreply@anthropic.com>"` fixture exits 0
  and, if it emits stdout, that the parsed `permissionDecision` is `allow`.
  **False-pass warning:** deleting the old deny-shaped assertions instead of
  inverting them would pass a shallow "grep finds no deny" check while leaving
  no regression test — the existing test file's load-bearing proof (its
  `findLastPreDemotionRevision()` walk that dynamically discovers the last
  pre-demotion revision and re-verifies it *did* deny the exact same fixture)
  is what proves the demotion is a real behavioural change, not a pre-existing
  no-op; that proof must keep passing.
- **AC-8.** A positive detection surfaces as one of exactly two advisory shapes,
  never a block: (a) `hookSpecificOutput` with `permissionDecision: "allow"` and
  a `permissionDecisionReason` carrying the warning text, or (b) stderr-only
  warning with exit 0 and no `hookSpecificOutput` at all. Both are non-blocking
  by the harness's contract. Check: a passing test feeds the trailer fixture and
  asserts the observed output is shape (a) or (b) and, either way, the
  harness-facing result is allow; a second passing test asserts the warning text
  is non-empty for that fixture (an advisory that says nothing is not advisory,
  it is absent).
- **AC-9.** Fail-open on internal error — the inverse of the pre-demotion
  posture. Any parse failure, fs read failure, or uncaught exception results in
  a silent exit 0 (the outer `try`/`catch`). Check: a passing test feeds
  malformed stdin (non-JSON) and asserts exit 0, no output.
- **AC-10.** The three residual "could not inspect" paths are *warn*, not block:
  an unreadable `-F`/`--file` target, a literal bare `git commit` with no
  message source at all, and `-F -` fed by a plain pipe (no heredoc). Each
  produces an advisory warning (stderr or the `advise()` allow-shape) and exit 0.
  Check: `grep -n "advisory only" claude-pre-commit-check.js` shows these paths
  are labelled advisory, and a passing test feeds a `git commit -F nonexistent`
  command and asserts exit 0 with a warning (not a deny).
- **AC-11.** The header states the demotion and the fail-open contrast plainly,
  including the "before vs at" tradeoff and the pointer to
  `claude-trailer-checker.js` as the real control. Check: reviewed by eye
  against a named list (demotion reason, fail-open posture, the future commit-msg
  enforcement point, the `--no-verify`/cherry-pick-revert-am caveat) — prose
  completeness, not grep-checkable.

### D. Escape hatch

- **AC-12.** `CLAUDE_DISABLE_COMMIT_CHECK=1` read from `process.env` bypasses
  the hook entirely (exit 0 before any processing). Check: `grep -n
  "CLAUDE_DISABLE_COMMIT_CHECK" claude-pre-commit-check.js` matches; a passing
  test sets the var in the test process's env and asserts exit 0 even for a
  trailer-bearing fixture.

### E. Bypass coverage — stated, not glossed

- **AC-13 (residual gap, inherited).** The trigger (`isRealGitCommit`) is still
  command-text parsing, so the BUG-088 bypass family (a bare-word prefix like
  `env`/`sudo`, a `git.exe`/wrapper invocation, `bash -c "git commit ..."`) can
  still defeat *engagement*. Because the file is now advisory-only, a defeated
  trigger means "no warning," not "a trailer lands undetected" — the trailer is
  still caught by the future commit-msg hook. The criteria document this as the
  current contract, not as a gap to close here. Check: reviewed by eye (the
  header states the demotion changes what happens with a positive detection,
  not the detection quality itself).
- **AC-14 (residual gap, inherited).** `git commit --no-verify` and the
  cherry-pick/revert/am verbs are outside this hook's reach by construction,
  per the source header's citation of ASM-386 — not re-verified live this pass.
  See ASM-731. Check: header comment present (reviewed by eye).

### F. Tests

- **AC-15.** `claude-pre-commit-check.test.js` exists and passes, covering: the
  BUG-043 quote-mask false-positive/false-negative pair, the shell-separator
  engagement forms, the "no phrase at all" negative, the "quoted mention does
  not mask a real invocation later" case, and the AC-C3 demotion proof (current
  guard never denies + the dynamically-discovered last pre-demotion revision
  *does* deny + the grep-for-zero-deny literal check). Check:
  `node --test claude-pre-commit-check.test.js` is green; the demotion proof's
  `findLastPreDemotionRevision()` throws loudly if history contains no denying
  revision rather than skipping silently (a gate that cannot evaluate must not
  report success).
- **AC-16.** The unit-level tests exercise the pure helpers by
  `require()`ing the file, relying on the `require.main === module` guard so
  stdin is never touched — no spawned-process test may depend on the file
  running `main()` when required. Check: the test file's header documents this;
  `node --test claude-pre-commit-check.test.js` passes with no stdin hang.

## Out of scope (stated, not silently absent)

- Wiring `claude-trailer-checker.js` into `.git/hooks/commit-msg` — that is the
  follow-on integration dispatch, not this file's contract (the header names it
  as the real control and stops there).
- Closing `--no-verify`, or the cherry-pick/revert/am verb-coverage gap
  (ASM-386) — inherited, not re-solved (AC-14).
- Re-hardening the extraction machinery (`MSG_FLAG_RE`/`FILE_FLAG_RE`/
  `HEREDOC_RE`). BUG-088 ruled it unsound; the demotion is the fix, and
  hardening a now-inert advisory layer would be wasted build.
- Re-blocking. The demotion is deliberate and irreversible-by-this-item; the
  AC-C3 load-bearing proof exists precisely so a future revert to blocking is
  caught by name.

## Assumptions

Logged via `node claude-bow.js add assumption`:

- **ASM-730.** These retrospective criteria capture the post-BUG-088 *demoted*
  contract, not the pre-demotion blocking behaviour: the retained `-m`/`-F`/
  heredoc extraction machinery is inert, and the real Co-Authored-By enforcement
  is `claude-trailer-checker.js` via a future commit-msg hook. What breaks if
  this is wrong: a developer or Destructive agent reading this file as a
  still-blocking guard would build/attack the wrong contract.
- **ASM-731.** The cherry-pick/revert/am and `git commit --no-verify` bypass
  gaps are inherited as out of scope per the source header's citation of
  ASM-386; this pass did not re-verify them live (ASM-386's own comment thread
  already carries the live confirmation, and re-litigating a settled finding
  would waste a cycle).

## Escalations

- **No BOW item owns the demotion itself.** The demotion shipped under BUG-088's
  remediation wave (see `tool.secretguard.md`'s BUG-088 section), and
  `git log` for this file shows no standalone `[FEAT-xxx]` commit for the
  demotion as its own change. FEAT-109 is the registry entry for the *guard*,
  but the demotion is a BUG-088-adjacent behaviour change not separately
  tracked. Flagging for Bill: either fold the demotion into BUG-088's already-
  open record or open a distinct item, so the "advisory-only" state has a
  closed review trail the way FEAT-045's demotion of `claude-author-guard.js`
  does.
- **The future commit-msg wiring is the only real control and is currently
  unbuilt.** Until `claude-trailer-checker.js` is wired into a `commit-msg`
  hook, the sole pre-commit signal for a trailer is this file's advisory
  warning, and that warning is defeatable by the same bypass family that
  defeated the blocking guard. This is a known, documented gap (AC-13/AC-14),
  not a defect in these criteria — but the gap stays open until the integration
  dispatch lands. Flagging for Bill/Aaron to prioritise against the engine
  wave.
