BOW code: ASM-381

# Acceptance criteria — tool.planning / `.scratch/` gitignore (ASM-381 hole fill)

**BOW code:** ASM-381 (P2, open)
**code.json:** `tool.planning`
**Code:** `.gitignore`, `claude-scratch.js` (FEAT-058)
**Date:** 2026-08-27 (BA-5b — BA-5 died before this hole had an AC)
**Status:** hole-fill — documents the current contract; adding `.scratch/` to `.gitignore` is a follow-up chore, not this fold's claim that it already landed.

## Acceptance criteria

- **AC-381 (ASM-381 — `.scratch/` is in-tool excluded, not gitignored).**
  `claude-scratch.js` writes snapshots under `.scratch/` and `getChangedPaths()`
  hardcodes an exclusion for `relPath === '.scratch'` or any path starting
  with `.scratch/`, so snapshot correctness does not depend on git ignore
  rules. Project `.gitignore` does **not** list `.scratch/`, so `git status`
  and `git add -A` still see snapshot contents as untracked noise. Adding
  `.scratch/` to `.gitignore` is a follow-up touching that file (out of
  FEAT-058's original dispatch set). Check: `grep -n "^\.scratch" .gitignore`
  is empty today; `grep -n "relPath === '.scratch'" claude-scratch.js` matches
  the live skip. A passing test named
  `testScratchRootIsExcludedEvenIfGitReportsUntracked` asserts a fixture
  `.scratch/foo` path is omitted from `getChangedPaths()` even when git lists
  it as `??`. **False-pass:** grepping `.scratch` in `claude-scratch.js`
  comments would also pass a build that dropped the hardcoded filter — the
  binding checks are the empty `.gitignore` line plus the live `relPath` skip.
