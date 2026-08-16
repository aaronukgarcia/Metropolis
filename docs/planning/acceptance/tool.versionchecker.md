BOW code: FEAT-132

# Acceptance criteria — tool.versionchecker (FEAT-132)

**BOW code:** FEAT-132 (P2) — "Version-file checker backing tool.versionguard (GR#2)."
**Module key / GUID:** `tool.versionchecker` / `6a16b608-613f-41b3-a6da-6ca1ad1ea000`
**Spec refs:** GR#2 (Version Discipline — Metropolis profile, M0-ENG §3: app version comes solely from `git describe`, injected via `-ldflags`); GR#3 (Single Source of Truth); BUG-088 (the extraction this module came from — see the BUG-088 section of `tool.secretguard.md`, AC-D3); SEC-002 (path passed as a single argv element to `spawnSync`, `shell:false`); ASM-386 (commit-msg verb-coverage gap for cherry-pick/revert/am).
**Date:** 2026-08-16
**Status:** **retrospective** — `claude-version-checker.js` is already committed; this file documents its contract, not a build gate. A Tester/Destructive verifies fidelity rather than constructing new code. Framing logged as ASM-778.
**Package under test:** `claude-version-checker.js` (repo root) — the Single Source of Truth for deciding whether a commit stages a hand-maintained version file or a hardcoded semver literal in a `version.go`. Tests: `claude-version-checker.test.js`.
**Standard gates:** Node.js — `node --check claude-version-checker.js`; `node --test claude-version-checker.test.js`; stdlib-only; SG-6 (no Co-Authored-By).

## What this module is (read before the ACs)

BUG-088's finding for `claude-version-guard.js` was entirely a *trigger* defect — its payload (`git diff --cached --name-only` plus per-file `git diff --cached`) was always sound. This module is that payload, relocated not reimplemented (AC-D3): same hand-maintained-file detection, same hardcoded-semver detection, same exemption pattern list. It carries none of the sibling guards' boundary-regex/quote-mask/engage-decision machinery (AC-B4). Its one deliberate, header-documented deviation from the other checkers is the fail-open posture on internal error — this is a hygiene check, not a security gate.

## Acceptance criteria

### Behaviour (the version-check contract)

- **AC-1. `checkVersion()` takes no arguments and returns a three-state result:** `{ status: 'clean', note?: <string> }`, `{ status: 'found-problems', findings: [<string>] }`, or `{ status: 'internal-error', error: <Error> }` — the shared discriminant AC-E1 of `tool.secretguard.md`'s BUG-088 section requires across all four checker modules. The `note` (only on `clean`) carries the non-blocking GR#2 reminder, never a detection. Check: the docs-only-clean test asserts `clean`; the internal-error test asserts `internal-error`; a Tester confirms the three literal `status` values are the only ones produced.

- **AC-2. Hand-maintained version files are detected by exact path or basename.** `isHandMaintainedVersionFile(relPath)` returns true for the two retired Prix-Six paths (`app/package.json`, `app/src/lib/version.ts` — `HAND_MAINTAINED_EXACT_PATHS`) and for any file whose basename is exactly `VERSION`; false for everything else, including the sanctioned `BUILDINFO_EXEMPT_PATH`. Check: the `isHandMaintainedVersionFile` unit test passes all six asserted cases (retired paths, bare `VERSION`, nested `VERSION`, the buildinfo exemption, and an ordinary README).

- **AC-3. A `version.go` is only a violation when its staged content hardcodes a semver literal.** `VERSION_GO_RE` (`/(^|\/)version\.go$/`) flags such files, **except** `BUILDINFO_EXEMPT_PATH` (`internal/foundation/buildinfo/buildinfo.go`, the one sanctioned ldflags-injection target), and only when `stagedDiffHasHardcodedSemver(relPath)` finds an added (`+`, not `+++`) line matching `SEMVER_LITERAL_RE` (`/["'`]v?\d+\.\d+\.\d+/`). Check: the `VERSION_GO_RE` unit test asserts the buildinfo exemption is not matched; `grep -n` confirms the `VERSION_GO_RE`/`SEMVER_LITERAL_RE`/`BUILDINFO_EXEMPT_PATH` constants.

- **AC-4. `stagedDiffHasHardcodedSemver` shells to git safely (SEC-002).** The rel-path is passed as a single argv element to `spawnSync` with no shell, and the call carries a 5s timeout. Check: `grep -n "spawnSync" claude-version-checker.js` shows the `['diff', '--cached', '--', relPath]` argv and `timeout: 5000`; no `shell: true`.

- **AC-5. Docs/tooling-only commits are exempt.** When every staged path matches an `EXEMPT_PATTERNS` entry (`docs/`, `*.md`, `.claude/`, `claude-*.js`, `.gitignore`, `package.json`, `package-lock.json`), `checkVersion()` returns `clean` without further scanning. Check: the docs-only-staged test passes (`clean`).

- **AC-6. A missing Go skeleton short-circuits to `clean`.** If neither `cmd/` nor `internal/` exists under `ROOT`, there is no engine code to enforce GR#2 against. Check: `grep -n "hasGoSkeleton" claude-version-checker.js` shows the short-circuit.

- **AC-7. A positive detection returns `found-problems` with the single deny-shaped finding string**, naming the offending paths and the M0-ENG §3 `git describe`-only rule. Check: `grep -n "hand-maintained version file" claude-version-checker.js` shows the finding text.

- **AC-8. The module header documents the fail-open deviation and the ASM-386 gap.** It states, by name: the one deliberate fail-open-on-internal-error deviation (and that fail-open describes only the internal-error path, never a detection result), and ASM-386 (cherry-pick/revert/am bypass the `commit-msg` caller). Check: the header-posture test asserts `/fail-OPEN|fail-open/i` and `/CALLER/` are present; the AC-B2 test asserts `ASM-386`/`cherry-pick`/`revert`/`am` are present.

### Fail-open / fail-closed

- **AC-9. Internal error is its own state, never silently "clean" — but the caller treats it as fail-open.** `checkVersion()` returns `{ status: 'internal-error', error }` when `git diff --cached --name-only` fails (never coerced to `clean`), while its header documents that the **caller** is expected to treat this particular checker's `internal-error` as fail-open (allow, surfaced not silently swallowed) — the opposite of the secret/plan checkers. This is a caller-side decision, not a fourth state; the three-state discriminant stays uniform. Check: the internal-error test asserts `status === 'internal-error'` and `error instanceof Error`; the header-posture test asserts the fail-open wording and the caller-side framing.

- **AC-10. `stagedDiffHasHardcodedSemver` fails open per-file.** When it cannot diff one staged `version.go` (git failure/timeout), it writes a warning to stderr naming the file and the fact that the check was **not** performed, then returns `false` (skip this file) rather than failing the whole check — the module's own documented fail-open posture at the per-file granularity. Check: `grep -n "could not diff staged file" claude-version-checker.js` shows the warning text and the `return false`.

- **AC-11. A positive detection is never treated as fail-open.** The fail-open posture describes only the internal-error path; a real hand-maintained version file (or hardcoded semver in a `version.go`) still returns `found-problems`. Check: the header prose states this distinction; the detection paths (`isHandMaintainedVersionFile`, `stagedDiffHasHardcodedSemver`) feed the `offending` array, never a `clean` result.

### Tests

- **AC-12. `claude-version-checker.test.js` exists and passes**, and contains no trigger-machinery copy (AC-B4) and a header naming the ASM-386 gap (AC-B2). Check: `node --test claude-version-checker.test.js` exits 0; the AC-B4/AC-B2 grep-style cases assert `buildQuoteMask|GIT_COMMIT_RE|isRealGitCommit` are absent and `ASM-386`/`cherry-pick`/`revert`/`am` are present.

- **AC-13. The suite stages fixtures into a throwaway git index, never the real repo's index.** The BUG-088 P1 correction requires `git --git-dir=<throwaway> --work-tree=<ROOT> add` (with `GIT_DIR`/`GIT_WORK_TREE` set only for the test's duration) so the real `.git/index` is never opened, locked, or mutated — the `checkVersion()` payload hardcodes `cwd: ROOT` by design. Check: the `withThrowawayIndex` helper and its comment are present; the docs-only and internal-error tests use it (or a subprocess) rather than `git add`/`git reset` against the real index.

- **AC-14. The internal-error test is subprocess-scoped.** It runs the checker in a child process with `GIT_DIR` pointed at a nonexistent path, so the env override never touches this test process's environment or any concurrent agent's. Check: the test uses `spawnSync(process.execPath, ['-e', script], { env: { ...process.env, GIT_DIR: ... } })`.

- **AC-15. The module is syntactically valid and stdlib-only.** Check: `node --check claude-version-checker.js` exits 0; `grep -n "require(" claude-version-checker.js` lists only `fs`, `path`, `child_process`.

## Out of scope

- **Closing ASM-386's cherry-pick/revert/am gap** — inherited and stated plainly, not re-solved.
- **Wiring this checker into a `commit-msg` dispatcher** — the follow-on integration `tool.secretguard.md`'s BUG-088 section (AC-B5) reserves; this module only defines the call contract.
- **The `--amend`/env-var-disable handling of `claude-version-guard.js`** — that is hook-plumbing that belongs to the caller (the guard file), not this payload module, per the module header.

## Assumptions logged

- **ASM-778** — retrospective framing: these ACs document the committed contract, and the tests ACs assert the existing suite already covers the named behaviours.
- **ASM-779** — the fail-open-on-internal-error deviation is caller-side (the module reports `internal-error`, the caller decides to allow), the module never fails open on a positive detection, and `stagedDiffHasHardcodedSemver`'s per-file warn-and-return-false is the module's own fail-open posture.

## Escalations

- None. Documentation-only pass over committed, tested code. One judgment call flagged for Bill's awareness rather than as a conflict: AC-9 documents the fail-open disposition as a **caller-side** decision encoded nowhere in the module (the module only returns `internal-error`), matching the module header's own framing — a reader expecting the module to emit a distinct fourth "fail-open" state would be misreading the three-state contract AC-E1 requires (ASM-779).
