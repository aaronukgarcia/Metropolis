BOW code: FEAT-131

# Acceptance criteria — tool.trailerchecker (FEAT-131)

**BOW code:** FEAT-131 (P2) — "Commit-trailer checker backing tool.precommitcheck."
**Module key / GUID:** `tool.trailerchecker` / `8ecec508-51d0-4930-8281-2cf8af6c2083`
**Spec refs:** M0-ENG §5 (hooks); BUG-088 (the extraction this module came from — see the BUG-088 section of `tool.secretguard.md`, AC-D1, where this module is the one **replacement** rather than a relocation); ASM-386 (commit-msg verb-coverage gap for cherry-pick/revert/am); SG-6 (no Co-Authored-By — the trailer this check exists to reject).
**Date:** 2026-08-16
**Status:** **retrospective** — `claude-trailer-checker.js` is already committed; this file documents its contract, not a build gate. A Tester/Destructive verifies fidelity rather than constructing new code. Framing logged as ASM-776.
**Package under test:** `claude-trailer-checker.js` (repo root). Tests: `claude-trailer-checker.test.js`.
**Standard gates:** Node.js — `node --check claude-trailer-checker.js`; `node --test claude-trailer-checker.test.js`; stdlib-only; SG-6 (no Co-Authored-By).

## What this module is (read before the ACs)

Unlike the other three BUG-088 checkers, this module is **not** a relocation of its guard's existing extraction logic — it is a **replacement** for it (AC-D1 of `tool.secretguard.md`'s BUG-088 section). `claude-pre-commit-check.js`'s old message extraction (pulling `-m`/`--message` values, `-F`/`--file` targets, and heredoc bodies out of the proposed command **string**) was architecturally unsound independent of the trigger defect: three residual gaps (a bare `git commit` with no message source, `-F -` fed by a plain pipe, an unreadable `-F` target) meant the payload itself could not always be trusted. This module instead reads the message the way a `commit-msg` hook actually receives it: as a real file at the path passed as the argument (`$1`, resolving to `.git/COMMIT_EDITMSG`), correct and complete regardless of how the message was supplied. Reading the file directly makes all three gaps disappear structurally.

## Acceptance criteria

### Behaviour (the trailer-check contract)

- **AC-1. `checkTrailer(messageFilePath)` takes exactly one argument — the message file path** (the `$1` a real `commit-msg` hook receives) — and never reads a command string, a pipe, or stdin. Check: the test asserts `checker.checkTrailer.length === 1`; the module source contains no `process.stdin` and no `spawnSync`.

- **AC-2. `checkTrailer` returns a three-state result:** `{ status: 'clean' }`, `{ status: 'found-problems', findings: [<string>] }`, or `{ status: 'internal-error', error: <Error> }` — the shared discriminant AC-E1 of `tool.secretguard.md`'s BUG-088 section requires across all four checker modules. Check: the clean / found-problems / internal-error tests each assert one of the three `status` values.

- **AC-3. Detection uses the unchanged trailer regex.** `TRAILER_RE = /Co[- ]Authored[- ]By\s*:/i` is carried verbatim from `claude-pre-commit-check.js` and matched against the file's full text, so a trailer anywhere in the message (header, body, mid-line, any casing/spacing variant `Co-authored-by:`/`Co authored by:`) is found. Check: the mid-body lower-case `co-authored-by:` test passes; `grep -n "TRAILER_RE" claude-trailer-checker.js` shows the single definition and its use.

- **AC-4. A found trailer produces `found-problems` with the single deny-shaped finding string** explaining that this repo is solely Aaron's and no AI-authorship trailer belongs in it. Check: the `-m`-shaped detection test asserts `status === 'found-problems'` and `findings.length > 0`.

- **AC-5. The module carries no trigger machinery and no extraction machinery (AC-B4).** None of `buildQuoteMask`/`GIT_COMMIT_RE`/`isRealGitCommit`, and none of `extractMFlagMessages`/`extractFileFlagPaths`/`extractHeredocBodies`/`MSG_FLAG_RE`/`FILE_FLAG_RE`/`HEREDOC_RE`, is present in the source. Check: the AC-B4 test asserts both regex families are absent; a Tester re-runs `grep -rn "buildQuoteMask\|GIT_COMMIT_RE\|isRealGitCommit\|extractMFlagMessages\|extractFileFlagPaths\|extractHeredocBodies\|MSG_FLAG_RE\|FILE_FLAG_RE\|HEREDOC_RE" claude-trailer-checker.js` and finds zero matches.

- **AC-6. The three old residual gaps no longer exist as gaps.** Because the message is always a real file at this hook point, there is no "bare `git commit` with no message source", no "`-F -` via a plain pipe", and no "unreadable `-F` target" — each collapses into "read the one file". Check: the empty-message-file test asserts `clean` (an empty file plainly has no trailer), and the `-F -`-shaped test asserts the signature takes only a file path (the pipe scenario cannot be constructed by design).

- **AC-7. The module header states the ASM-386 gap plainly.** It names cherry-pick/revert/am and ASM-386 as the inherited verb-coverage limitation a `commit-msg` caller inherits, and states the "replacement, not relocation" rationale. Check: the AC-B2 test asserts `ASM-386`/`cherry-pick`/`revert`/`am` are present in the header; the "replacement" framing is reviewed by eye.

### Fail-open / fail-closed

- **AC-8. An unreadable message file is `internal-error`, never `clean` (fail-closed on inspection failure).** An inspection failure must not be confused with a passed check. Check: the missing-file test asserts `status === 'internal-error'` and `error instanceof Error`.

- **AC-9. An empty-but-readable message file is `clean`, not `internal-error`.** An empty file is inspectable and plainly contains no trailer — a clean read, not an inspection failure. This distinction (empty = clean, unreadable = internal-error) is the whole of the module's fail-open/fail-closed boundary. Check: the empty-file test asserts `clean`; the missing-file test asserts `internal-error`; both are present side by side.

- **AC-10. The advisory demotion is caller-side, not this module's contract.** `claude-pre-commit-check.js`'s PreToolUse layer is demoted to advisory-only (per `tool.secretguard.md`'s BUG-088 Section C), but this module itself only returns the three-state result — it encodes no `permissionDecision`, no exit code, no stdout emission. A future `commit-msg` dispatcher decides what to do with each state. Check: `grep -n "permissionDecision\|process.exit\|console\." claude-trailer-checker.js` finds zero matches; the module header documents the caller-side posture. Logged as ASM-777.

### Tests

- **AC-11. `claude-trailer-checker.test.js` exists and passes.** Check: `node --test claude-trailer-checker.test.js` exits 0.

- **AC-12. The suite proves the collapse of the three extraction paths into one.** A single file-based test covers what used to require three separate suites (`-m`, `-F`, heredoc): the `-m`-shaped and `-F`/heredoc-shaped (mid-body) cases both assert detection over a real message file. Check: both cases are present and pass.

- **AC-13. The suite proves the residual gaps are gone as gaps, not patched.** The empty-file case (bare-`git commit` shape) and the signature-length case (`-F -` shape) assert the *structural* disappearance of the old gaps rather than a new workaround. Check: both cases are present and pass.

- **AC-14. The module is syntactically valid and stdlib-only.** Check: `node --check claude-trailer-checker.js` exits 0; `grep -n "require(" claude-trailer-checker.js` lists only `fs`.

## Out of scope

- **Closing ASM-386's cherry-pick/revert/am gap** — inherited and stated plainly, not re-solved.
- **Wiring this checker into a `commit-msg` dispatcher** — the follow-on integration `tool.secretguard.md`'s BUG-088 section (AC-B5) reserves; this module only defines the call contract.
- **The PreToolUse demotion of `claude-pre-commit-check.js` itself** — that is the BUG-088 Section C / AC-C3 work on the guard file, not this module's contract.

## Assumptions logged

- **ASM-776** — retrospective framing: these ACs document the committed contract, and the tests ACs assert the existing suite already covers the named behaviours.
- **ASM-777** — the empty-file-reads-clean reading and the caller-side location of the advisory demotion are documented as the committed contract; the module itself encodes only the three-state return.

## Escalations

- None. Documentation-only pass over committed, tested code. One judgment call flagged for Bill's awareness rather than as a conflict: AC-8/AC-9 pin the entire fail-open/fail-closed boundary to the empty-vs-unreadable distinction (empty = clean, unreadable = internal-error), because that is the only two-way inspection outcome this file-based module actually has — a reader expecting a richer posture would be reading the caller's demotion decision back into the module where it does not live (ASM-777).
