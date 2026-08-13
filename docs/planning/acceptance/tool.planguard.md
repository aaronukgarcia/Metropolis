BOW code: FEAT-024

# Acceptance criteria — tool.planguard (archaeology)

**BOW code:** FEAT-024 — status `done`, closed 2026-08-08 16:41:01.
**Module key:** tool.planguard
**Spec refs:** GR#3 (Single Source of Truth); GR#6 (GUID Documentation);
M0-ENG §5 (hooks); SEC-008 (boundary-anchoring fix); BUG-043 (quote-mask
fix, ported from `claude-author-guard.js`); BUG-088 (open — names this guard
as one of the ported guards sharing the class that beat the author guard
15 times).
**Date:** 2026-08-11 (written after the fact)
**Status:** active
**Package under test:** `claude-plan-guard.js`.
**Standard gates:** Node, not Go — SG-1/2/4/7 don't apply. This item's own
gates: `node --check claude-plan-guard.js`; `node claude-plan-guard.test.js`
passing; SG-6.

## Why this ranks high

Every module in this repo — including this file's own siblings — depends,
transitively, on `code.json` and the BOW being in sync with the master plan
(GR#3). This guard is the only thing standing between a hand-edited or stale
`code.json`/`bow-import.json` and `main`. It has already been through two
documented rounds of fixes (the original build's own SEC-008 finding, then
BUG-043) and is currently flagged — by BUG-088, filed against the guard
family, not this guard individually — as sharing the SAME fragile-parsing
class that produced 15 live bypasses against `claude-author-guard.js` before
that guard was rewritten with a real tokenizer. That is the finding this file
exists to state plainly (see Divergence below), not to re-discover.

## What this guard actually guarantees

### A. Engagement

- **AC-1.** Non-`git commit` Bash/PowerShell commands exit 0 immediately, no
  stdout. Check: a passing test feeds `{"tool_input":{"command":"git
  status"}}` and asserts exit 0, empty stdout.
- **AC-2.** Engagement is boundary-anchored (`GIT_COMMIT_RE`: start-of-string
  or immediately after a shell separator `;&|(` or newline) and quote-mask
  aware (`buildQuoteMask`, ported from `claude-author-guard.js`) — a `git
  commit` phrase inside a quoted string (e.g. a `--desc` argument describing
  this very guard) does not engage it. Check: `grep -n "git\\s+.*commit"
  claude-plan-guard.js` shows the anchored pattern; a passing test feeds a
  command whose ONLY appearance of the phrase is inside a quoted `--desc`
  value and asserts allow (exit 0, no regeneration side effect) — this is
  SEC-008's original regression target, restated as a check.
- **AC-3.** A `-C <dir>` global flag between `git` and `commit` is tolerated
  (`git -C somedir commit ...` still engages). Check: a passing test asserts
  this form is recognised.

### B. Drift detection

- **AC-4.** On engagement, `tools/plan/generate.js --check` runs first
  (validates only, writes nothing); non-zero exit denies, surfacing the
  validator's stdout/stderr verbatim. Check: a passing test with a
  deliberately invalid fixture master plan (e.g. a duplicate `seq`) asserts
  DENY with the validator's own error text present in the deny reason, not a
  generic message — the check must fail an implementation that denies
  correctly but discards the validator's diagnostic (a developer fixing the
  drift needs the actual error, not just "denied").
- **AC-5.** Drift/hand-edit detection is by CONTENT HASH, not by a
  `--check`-only pass: `code.json` and `tools/plan/bow-import.json` are
  hashed as they sit in the working tree, `generate.js` is run for real
  (regenerating both in place), then hashed again — a mismatch denies. Check:
  a passing test hand-edits `code.json` with a value regeneration would
  overwrite (e.g. a stale `moduleCount`) and asserts DENY, with the working
  copy left REGENERATED (not reverted) — the check must assert the
  post-invocation file content differs from the pre-invocation hand-edit,
  not merely that the process exit code was nonzero, since a build that
  denied without actually regenerating would still "look" correct from exit
  code alone.
- **AC-6.** A clean working tree (outputs already match a regeneration)
  allows silently. Check: a passing test running a full generate → guard
  cycle against a consistent fixture asserts allow with no stdout.

### C. Missing dependencies and fail-closed posture

- **AC-7.** `tools/plan/generate.js` missing, or the master plan
  missing/unparsable, denies with a clear message rather than crashing or
  silently allowing. Check: a passing test with `GENERATE_PATH` pointed at a
  nonexistent file asserts DENY naming the missing generator.
- **AC-8.** Any internal error (thrown exception, unexpected `spawnSync`
  failure) denies via the top-level `try/catch` — this guard is explicitly
  fail-closed, "a deliberate departure from the never-block-legitimate-work
  posture used elsewhere" per its own header. Check: a passing test forces
  `hashFiles` (or another helper) to throw and asserts DENY, not a silent
  allow.
- **AC-9.** Unparseable stdin JSON falls back to a raw-substring check
  (`input.includes('git commit')`): denies if the raw text looks commit-
  shaped, allows otherwise (BOM-stripped first) — scoped fail-closed, not
  "fail closed on everything," so a stdin hiccup never bricks non-commit
  commands. Check: a passing test feeds non-JSON stdin with no `git commit`
  substring and asserts allow; a passing test feeds non-JSON stdin containing
  that substring and asserts deny.
- **AC-10.** `CLAUDE_DISABLE_PLAN_GUARD=1` disables the guard, read from
  `process.env` only. Check: a passing test sets it in the TEST PROCESS env
  and asserts allow regardless of drift present.

## Divergence — what shipped vs. what this file would have required

**This is the actual finding, not the file.** `GIT_COMMIT_RE` here is:

```
/(?:^|[;&|(\n])\s*git\s+(?:-C\s+\S+\s+)?commit\b/g
```

Compared against `claude-author-guard.js`'s current (v4, post-15-bypasses)
token parser, this guard's engagement pattern:

- matches the literal word `git` only — no `git.exe`/`git.cmd` suffix
  tolerance, no quoted-path token, no Windows-unquoted-space-path
  resolution (author-guard's AC-11/ROUND4-3);
- does not recurse into shell wrappers (`bash -c`, `powershell -Command`,
  `cmd /c`, ...) at all — a commit issued through any of those wrappers is
  structurally invisible to this guard's `GIT_COMMIT_RE`, which never sees
  past the wrapper's own opening token;
- accepts only `-C <dir>` as a global flag before the verb, not `-c
  key=value` — so `git -c foo=bar commit ...` is very likely still
  recognised (the `-C` group is optional, and `\s+commit\b` will still find
  `commit` later in the string via the regex engine's own backtracking/
  retry, so this specific case is not obviously broken) but has not been
  positively verified against this guard the way it was against the author
  guard;
- has no per-verb enumeration — it only ever looks for the literal word
  `commit`, so `cherry-pick`/`revert`/`am`/`merge` invocations that could
  fabricate an author never engage this guard **at all**, for a different
  reason than "out of scope" — this guard's job is registry drift, not
  identity, so those verbs not engaging it is arguably correct scope, but
  worth stating rather than assuming.

**BUG-088 already names this class** ("GIT_COMMIT_RE's engage boundary is
defeated by any leading word or wrapper, same class that beat
claude-author-guard.js 15 times") against this guard and three siblings
(`claude-pre-commit-check.js`, `claude-secret-guard.js`,
`claude-version-guard.js`), and it is open. The engagement gap does not
threaten the SAME severity here as it did for the author guard — a missed
engagement on `tool.planguard` means a stale `code.json` might slip through
under an unusual wrapper, not a fabricated public-history identity — but it
is the same bug shape, unfixed, and this file is what makes that comparison
checkable rather than a hunch. No new BUG filed here (BUG-088 already covers
it); this file's AC-1/AC-2 above describe the CURRENT, narrower engagement
contract, deliberately not the wider one BUG-088 asks for, so a future fix
to BUG-088 is a criteria change here too, not just a code change.

## Out of scope

- Validating anything about the CONTENT of `code.json`/`bow-import.json`
  beyond "matches what `generate.js` would produce" — semantic correctness
  of the master plan itself is `tools/plan/generate.js`'s own validation
  (MET-T0xx codes), not this guard's job.
- `git merge`/`cherry-pick`/etc. reaching `code.json` via a non-`commit`
  porcelain verb — see Divergence above.

## Escalations

- BUG-088's fix, when it lands, should be applied to this guard identically
  to how it lands on the author guard (shared parsing primitives, per that
  guard's own header note about reuse) — flagging so the fix isn't done once
  and assumed to cover all four ported guards.
