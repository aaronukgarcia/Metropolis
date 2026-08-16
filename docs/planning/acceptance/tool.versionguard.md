BOW code: FEAT-106

# Acceptance criteria — tool.versionguard (FEAT-106, archaeology)

**Module key:** tool.versionguard
**BOW code:** FEAT-106 (GUID `a48cf07f-4071-44a9-b038-c8dbeb778b62`, created
2026-08-15 via code.json regeneration). Note the guard's own source header
still cites `legacy.versionguard / FEAT-002` as its history — the earlier
retarget that retired the Prix-Six two-file check; FEAT-106 is the current
owning item and carries no git refs yet. See Escalation B.
**Spec refs:** GR#2 (Version Discipline — Metropolis profile); M0-ENG §3
(app version = `git describe --tags --dirty`, injected via `-ldflags`; hand-
maintained version files banned) and §5 (hooks); `docs/golden-rules-detail.md`
"Rule #2 — Version Discipline (Metropolis profile)".
**Date:** 2026-08-16 (written after the fact — this is archaeology, not a
brief BAs wrote before dispatch).
**Status:** active
**Package under test:** `claude-version-guard.js` (repo root, Node.js) and its
extracted payload module `claude-version-checker.js` (BUG-088's GR#3 split —
the single source of truth for the GR#2 check). The trigger is built by
`claude-git-commit-trigger.js` (shared, BUG-123).
**Standard gates:** Node, not Go — SG-1/SG-2/SG-4/SG-7 do not apply. This
item's own gates: `node --check claude-version-guard.js claude-version-checker.js`;
`node --test claude-version-guard.test.js claude-version-checker.test.js`;
SG-6 (no Co-Authored-By).

## Why this file exists, and why it is written last-to-first

This guard has existed since the project's first days, went through a retarget
(FEAT-002/`legacy.versionguard`: drop the retired `app/package.json` +
`app/src/lib/version.ts` two-file check once `cmd/`/`internal/` exist), a
P0 refactor (BUG-088: extract the GR#2 payload into `claude-version-checker.js`
without changing PreToolUse behaviour), and a bypass-family fix (BUG-123:
`git -c key=value commit` never triggered). At no point did a criteria file
exist for the *current* FEAT-106 contract; `legacy.versionguard.md` documents
only the retarget. This file states the contract the code actually implements
today, so the next Destructive round has something to be judged against
instead of starting from the code again.

## What this guard actually guarantees (read from the code, not assumed)

### A. Engagement — when the guard evaluates a command at all

- **AC-1.** The trigger is `GIT_COMMIT_RE = buildAnchoredGitVerbTriggerRegex('commit')`
  (`claude-version-guard.js:140`), built by the shared tokenizer in
  `claude-git-commit-trigger.js`. It matches a shell-command-boundary-anchored
  `git` token (start of string, or after `; & | ( \n`) followed by a run of
  recognised global options consumed one at a time, then a verb WORD compared
  by exact `Set` membership against `commit`. Only a real `git commit` engages
  the GR#2 check; every other command exits 0 with empty stdout. Check: a
  passing test asserts `GIT_COMMIT_RE.test('git commit -m x') === true` and
  `GIT_COMMIT_RE.test('git status') === false`; a spawn test asserts a
  `npm install` payload produces exit 0 and empty stdout. **What a false pass
  looks like:** a test that only checks the direct-command case would also pass
  a build that dropped the shell-boundary anchoring — the boundary cases (`;`,
  `&&`, `|`, newline, `(`) must be asserted separately (they are: see AC-19).

- **AC-2.** `git commit --amend` is skipped (allow) via a literal
  `command.includes('--amend')` (`claude-version-guard.js:170`). Check: source
  reading confirms the only skip test is `--amend`. **Divergence note:** the
  code comment above that line claims it also skips "merge commits, or if this
  IS the version bump commit" — the code implements neither of those two extra
  skips. This is a doc/code drift, flagged in Escalation A, not an AC the code
  actually satisfies.

- **AC-3.** Escape hatch `CLAUDE_DISABLE_VERSION_GUARD=1` is read from
  `process.env` only, never from text inside the proposed command; when set it
  exits 0 before parsing stdin (`claude-version-guard.js:148`). Check: source
  reading shows the check is `process.env.CLAUDE_DISABLE_VERSION_GUARD === '1'`
  at the top of the stdin `end` handler.

- **AC-4.** Stdin JSON is BOM-stripped before parse
  (`input.replace(/^﻿/, '')`, `claude-version-guard.js:156`) so a
  BOM-prefixed payload (e.g. piped from PowerShell) cannot fall into the
  fail-open catch and silently allow. Check: source reading confirms the
  `.replace` precedes `JSON.parse`; this is the `@FIX (v3.1.11)` documented in
  the source.

### B. Deny vs allow — the GR#2 payload (delegated to claude-version-checker.js)

- **AC-5 (deny — hand-maintained version file).** A commit that stages a
  hand-maintained version file is DENIED. "Hand-maintained" is, exactly: the
  two retired exact paths `app/package.json` and `app/src/lib/version.ts`
  (`HAND_MAINTAINED_EXACT_PATHS`), OR any file whose basename is exactly
  `VERSION`, OR a `version.go` file (matching `VERSION_GO_RE`, i.e. any path
  ending in `version.go`) other than the exempt
  `internal/foundation/buildinfo/buildinfo.go`, whose staged content adds a
  line containing a semver-shaped literal (`SEMVER_LITERAL_RE`). The deny
  output is `permissionDecision: "deny"` with a `GOLDEN RULE #2 VIOLATION`
  reason naming the offending file(s) and pointing at the milestone-tag +
  `-ldflags` mechanism. Check: `isHandMaintainedVersionFile('app/package.json')`,
  `isHandMaintainedVersionFile('VERSION')`, and `isHandMaintainedVersionFile('some/dir/VERSION')`
  are all `true`; the BUG-123 end-to-end test stages a real `VERSION` file and
  asserts `permissionDecision === 'deny'` with `/GOLDEN RULE #2/` in the reason.

- **AC-6 (allow — docs/tooling exemption).** A commit whose staged files are
  all matched by `EXEMPT_PATTERNS` (`docs/`, `*.md`, `.claude/`, root
  `claude-*.js`, `.gitignore`, root `package.json`/`package-lock.json`) is
  exempt — `status: 'clean'`, silent allow. Check: the parity test stages a
  docs-only file and asserts `checkVersion().status === 'clean'`.

- **AC-7 (allow — no Go skeleton).** If neither `cmd/` nor `internal/` exists
  on disk (`fs.existsSync` at check time, not cached), the check returns
  `clean` without inspecting staged files. Check: source reading shows the
  `hasGoSkeleton` guard gates the whole offending-file scan.

- **AC-8 (warn-allow — engine-touching commit).** A commit touching `cmd/` or
  `internal/` that stages no offending version file returns `clean` WITH a
  `note`; the guard surfaces that note as `permissionDecision: "allow"` with a
  non-blocking GR#2 reminder (milestone tags + `-ldflags` are the mechanism;
  BOW `[mkey]` enforcement is `tool.bow`'s job, not this hook's). Check:
  source reading shows the `note` branch maps to `warnAllow(...)`; the note
  text disclaims `[mkey]` enforcement to MOD-007.

- **AC-9 (allow — the sanctioned ldflags target).** `internal/foundation/buildinfo/buildinfo.go`
  is exempt by exact path (`BUILDINFO_EXEMPT_PATH`) — it is the sanctioned
  ldflags-injection target whose `"dev"` defaults are correct, never a
  violation. Check: `checker.BUILDINFO_EXEMPT_PATH === 'internal/foundation/buildinfo/buildinfo.go'`;
  the parity test asserts `VERSION_GO_RE` does NOT match that path, and that a
  hypothetical `internal/foundation/buildinfo/version.go` WOULD match (so the
  exemption is path-specific, not a blanket "any buildinfo file").

### C. Posture and error handling — fail-open, three-state checker

- **AC-10 (fail-open posture, stated by name).** The guard is fail-OPEN on
  internal error, deliberately, and its header says so by contrast with
  `claude-secret-guard.js`/`claude-plan-guard.js` (fail-closed): GR#2 is a
  hygiene check, not a security gate, so a hook bug must never brick unrelated
  commits. On a checker `internal-error` the guard writes a
  `⚠️  GR#2 GUARD: internal error ...` line to stderr and exits 0 (surfaced,
  not silent); on a JSON parse error or unexpected throw the top-level catch
  exits 0. Check: source reading shows the `internal-error` branch writes to
  `process.stderr` before `process.exit(0)`; the `catch (err)` at the end of
  the stdin handler exits 0.

- **AC-11 (three-state contract — internal error is its own state).**
  `checkVersion()` takes no arguments, never throws, and returns exactly one
  of `{status:'clean' [, note]}`, `{status:'found-problems', findings:[...]}`,
  or `{status:'internal-error', error}`. `internal-error` is never silently
  downgraded to `clean` (AC-F1 of the BUG-088 extraction). The guard maps:
  `internal-error` → allow-with-stderr, `found-problems` → deny, `clean`+note →
  warn-allow, `clean` → silent allow. Check: the checker test forces
  `git diff --cached --name-only` to fail and asserts the result is
  `{status:'internal-error'}` with an `Error` instance, not `clean`.

### D. Bypass-family coverage (documented honestly — this guard is fail-open, so a missed trigger is a skipped hygiene check, never a block)

- **AC-12 (`-c` / `-C` global-option run — COVERED, BUG-123).** `git -c key=value commit`,
  `git -C dir commit`, and stacked/combined forms all fire, because the trigger
  delegates option-run parsing to `claude-git-commit-trigger.js`'s tokenizer
  (per-option walk, no forced backtracking, exact verb Set equality). Check:
  tests assert `git -c foo=bar commit`, `git -c a=b -C /dir commit`, and
  `git -C /dir -c a=b commit` all `=== true`, with a sanity test proving the
  pre-fix single-`-C`-slot regex missed `git -c foo=bar commit`.

- **AC-13 (`.exe` / `.cmd` suffix — NOT covered, known gap).** The trigger's
  boundary source is `(?:^|[;&|(\n])\s*git(?=\s)` — a literal `git` followed
  by whitespace — so `git.exe commit` / `git.cmd commit` do NOT fire (the
  `(?=\s)` fails before the `.`). Written as a known open gap, not a satisfied
  contract; fail-open means the cost is a skipped check, not a false block.
  ASM-748.

- **AC-14 (shell wrappers / leading words — NOT covered, known gap, BUG-088
  class).** The left boundary class is `[;&|(\n]` with no `\s`, so a leading
  wrapper word (`sudo git commit`, `env git commit`, `xargs git commit`) or a
  shell-wrapper body (`bash -c "git commit ..."`) does not fire — total
  non-detection of the check (not a bypass of a block, since this guard only
  ever denies on a positive detection). ASM-748.

- **AC-15 (git aliases — NOT covered, known gap).** The trigger only matches
  the literal verb `commit` by exact Set equality; a git alias (`git ci` with
  `alias.ci = commit`) does not fire. Unlike `tool.worktreeguard` (which
  reuses `resolveAlias`), this guard's trigger never resolves aliases. ASM-748.

- **AC-16 (accepted over-trigger — quoted prose mention).** Because the trigger
  is a bare boundary regex with no quote-state tracking, a quoted MENTION of
  `(git commit ...)` as prose (e.g. inside a BOW comment) DOES over-trigger the
  check — an accepted false positive that costs an extra staged-diff
  inspection, never a bypass. The test file asserts this over-trigger as a
  load-bearing regression guard: reintroducing quote-masking to "fix" it would
  reintroduce BUG-088's false-negative (see AC-17).

- **AC-17 (quote-masking must NOT be reintroduced — BUG-088 P0).** The BUG-088
  correction removed author-guard's `buildQuoteMask`/`isRealGitCommit`
  machinery from this guard's trigger. The test reconstructs the buggy
  quote-masked trigger and proves it false-negatives on the Destructive's exact
  fixture (`"# don't forget to review; git commit -m x"` — an unbalanced quote
  earlier in the command hides a real, later `git commit`), while the bare
  regex correctly fires. This is the "a regex is not a shell parser" lesson,
  kept in this guard's own test suite so a future "fix" is caught by a failing
  test.

### E. Registry errors

- **AC-18 (no registry-sourced errors).** Neither the guard nor the checker
  emits a registry-sourced `MET-xxx` error. Their entire error surface is
  free-text `permissionDecisionReason` strings (deny/warn) and stderr warnings,
  none of which carry a `data/errors.json` code. GR#7's error registry is
  Go-engine-scoped; these Node root-tooling hooks sit outside it. Check:
  `grep -n "MET-\|errors.json\|registry" claude-version-guard.js claude-version-checker.js`
  finds no match. ASM-749.

### F. Test coverage

- **AC-19.** `claude-version-guard.test.js` covers the trigger in isolation and
  end-to-end: the BUG-088 Destructive fixture (unbalanced quote then a real
  commit fires); real commits after `(`, `;`, `&&`, `|`, newline, and at start
  of string; no-fire on `npm install`/`git status`; the accepted prose
  over-trigger (asserted, so a silent quote-mask reintroduction fails the
  test); end-to-end silent allow on a non-commit command; BUG-123 round 1
  (`-c`/`-C`/stacked fire + pre-fix sanity miss); BUG-123 round 2 (`-c commit.gpgsign=false status`,
  `-C commit-repo status` do NOT fire — the backtracking false-positive fix);
  BUG-123 round 4 (quote opening mid-token after `=`, e.g. `user.name="John Q Commit"`);
  and a BUG-123 end-to-end deny of a staged `VERSION` file committed via
  `git -c ... commit` (proving the real `checkVersion()` payload runs, using a
  throwaway `GIT_DIR`/`GIT_WORK_TREE` index rather than the repo's own).

- **AC-20.** `claude-version-checker.test.js` covers the extraction contract:
  AC-B4 (no trigger machinery in the checker — grep for
  `buildQuoteMask|GIT_COMMIT_RE|isRealGitCommit` finds none); AC-B2 (header
  names `cherry-pick`/`revert`/`am` and cites ASM-386); the header's
  fail-open-on-error posture as a caller-side decision; `isHandMaintainedVersionFile`
  parity (retired paths + bare `VERSION`); `VERSION_GO_RE` + `BUILDINFO_EXEMPT_PATH`;
  AC-D3 (docs-only staged commit → `clean`, via a throwaway index); and AC-F1
  (git diff failure → `{status:'internal-error'}`, never `clean`).

### G. Determinism

- **AC-21.** The guard and checker are deterministic given the same repo state
  (staged files) and command — no randomness, no wall-clock-dependent decision
  input. The checker's 5000 ms `spawnSync` timeout is a resource bound, not a
  decision input. Check: `grep -n "Date.now\|Math.random\|time.Now"` across the
  guard, checker, and trigger finds no matches.

## Out of scope (stated, not silently absent)

- BOW `[mkey]` commit-message enforcement — `tool.bow` (MOD-007), never this
  hook (stated in both the header and AC-8's note text).
- The `commit-msg`-hook dispatcher that `claude-version-checker.js`'s header
  says a future caller would use — not implemented (BUG-088 Section B /
  AC-B5 of the extraction; the checker is `require()`-able but no dispatcher
  consumes it beyond this guard).
- ASM-386 (a `commit-msg` hook does not fire for `git cherry-pick`/`revert`/`am`
  on this project's git version) — inherited, cited in the checker header,
  not re-solved here.

## Escalations

- **A. Header claims three skip conditions, code implements one.** The comment
  at `claude-version-guard.js:169` says "Skip: amend, merge commits, or if this
  IS the version bump commit", but the code only checks
  `command.includes('--amend')`. A `git merge` or version-bump commit is NOT
  actually skipped. Recommend Bill either implement the two missing skips or
  correct the comment to match the code; until then the documented contract is
  AC-2 (only `--amend` is skipped).
- **B. FEAT-106 has no git refs; the code shipped under a different key.** The
  guard's own header cites `legacy.versionguard / FEAT-002`, and the committed
  code predates FEAT-106 (created 2026-08-15 via code.json regeneration, no
  `git_refs`). A Destructive round against this file will need its verdict
  recorded on FEAT-106, not on the legacy key. Recommend Bill back-link the
  historical commits (`git log -- claude-version-guard.js`) onto FEAT-106 so
  the owning item is complete.
