BOW code: FEAT-119

# Acceptance criteria — tool.codenamecontentscan (FEAT-119)

**BOW code:** FEAT-119
**code.json:** `tool.codenamecontentscan` (GUID `8cb8f51a-eaa8-41b2-bc2e-58b2f658bd3b`)
**File under test:** `claude-codename-content-scan.js` (root tooling, layer tooling)
**Spec refs:** GR#22 (Codename Discipline — the rule this module enforces at the git level);
GR#3 (Single Source of Truth); FEAT-046 / `tool.codenameguard` (the parent item this module was
split out of — its `claude-codenamehook.md` acceptance file is the house-style and rigor
precedent); BUG-182 / BUG-183 / BUG-185 (the three bypass classes closed inside this module);
ASM-386 (the `cherry-pick`/`revert`/`am` hook-dispatch finding this module inherits as a
disclosed limitation); ASM-736 / ASM-737 / ASM-742 (assumptions logged against this item).
**Date:** 2026-08-16
**Status:** RETROSPECTIVE — code is already committed; these criteria document the contract
of the committed module. Each AC cites the test that already proves it.

> **GR#22 discipline in this file itself.** The forbidden reference title, its real name, its
> abbreviations, its numbered sequel form, and its former expansion-content pack names are
> never written literally anywhere in this acceptance file — they are referred to by category
> only ("the reference title", "the distinctive single word", "the numbered abbreviation",
> "the former expansion-content pack names"), for the same reason the module under test
> assembles its patterns from fragments at runtime: a document living in git that types the
> forbidden literal to describe the rule would itself be a violation of the rule (GR#22).

## What this module is

`claude-codename-content-scan.js` is the **enforcing, git-level second layer** of GR#22. It
runs inside the installed `commit-msg` hook (`.git/hooks/commit-msg`, wired by the
FEAT-046/`tool.codenameguard` build) and inspects what is *actually being committed*, unlike
the PreToolUse `claude-codename-guard.js` which inspects what is *typed*. It owns exactly two
responsibilities and nothing else:

1. **The scan surfaces** — which text gets fed to the shared pattern scanner.
2. **Fail-closed propagation** — surfacing any internal failure upward so the hook denies.

It deliberately owns **neither** the forbidden-pattern definitions (those live in
`claude-codename-patterns.js`, FEAT-121) **nor** the header-vs-hunk line classification (that
lives in `claude-codename-diff.js`, FEAT-120). This three-way split is the retrospective read
of the committed code, documented here because no single BOW item description states it
(ASM-736).

## User stories

- As **Bill (or any committer on a repo that is going public)**, I need content that arrives
  by pasting into a file — not by typing into a command — to be caught when it is committed,
  so the PreToolUse guard's blind spot (content pasted from the BOW into a doc, then
  `git add` + `git commit -m "..."` with nothing incriminating in the command) is closed by
  inspecting the staged diff, not the command text.
- As **an operator whose commit is denied**, I need the denial to tell me *what category* of
  forbidden content matched and *which surface* (added content vs. file path) without the
  denial message itself re-printing the forbidden literal — otherwise the explanation would
  leak the name it exists to protect (see AC-8).
- As **a committer making an unrelated fix**, I need a pre-existing violation elsewhere in the
  same file — or in a line I am removing — not to block my commit, so the scan must be
  diff-scoped and added-line-only.

## Acceptance criteria

### A. What it must block (the reference title, its abbreviations, and its numbered form)

- **AC-1. The module blocks every hit from the shared GR#22 pattern set, on two surfaces:
  staged added-line content and new/renamed/copied file paths.** The pattern set it feeds is
  FEAT-121's `claude-codename-patterns.js` `PATTERNS`, which covers the reference title's real
  name (the two-word title with any separator), the distinctive single word from the title, the
  numbered abbreviation (the two-letter abbreviation immediately followed by the digit 1 or 2 —
  the "numbered sequel form"), and the former expansion-content pack names. This module defines
  **no pattern of its own** — it calls the shared `scan()` twice: once on `stagedAddedLines()`
  and once on `stagedPathHeaderLines()`. **Proven by:** `claude-codenamehook.test.js` AC-3 (a
  fragment-assembled positive staged content is rejected with non-zero exit and no new commit),
  BUG-183 (a forbidden pattern living only in a new file's path is rejected), and the AC-4
  shared-module tests. **What a lazy implementation looks like:** a copy of the pattern list
  pasted into this file "for defense in depth" — rejected by AC-5's `new RegExp` grep and the
  AC-4 monkeypatch test (emptying the shared `PATTERNS` array empties what this module scans).

- **AC-2. A forbidden pattern in a file *path* alone — never in file content and never in the
  commit message — must deny the commit exactly like one in content (BUG-183).** A new, renamed,
  or copied file whose *name* carries a forbidden pattern, with a clean body, reaches neither
  the added-line scan nor the commit message; `scanStagedDiff()` therefore scans
  `splitDiffSections()`'s `pathHeaderLines` half too, via the same shared pattern set (GR#3 —
  no second detection path invented). **Proven by:** `claude-codenamehook.test.js` BUG-183
  (installed hook rejects `${ABBR}-notes.txt` with a clean body; `stagedPathHeaderLines()`
  surfaces the filename and `scanStagedDiff()` reports a hit) and the clean-rename negative
  control (a rename with clean old path, new path, and body is still accepted — no false
  positive from path scanning).

### B. Scope — diff-scoped, added lines only

- **AC-3. The scan reads `git diff --cached`, added lines only — never the working tree, never
  unstaged changes.** Scanning whole files would block an unrelated fix in a file that still
  carries a pre-existing violation elsewhere; scanning removed lines would block the very commit
  that deletes a violation. `stagedAddedLines()`/`stagedPathHeaderLines()` both invoke
  `git diff --cached --unified=0 --no-color`. **Proven by:** `claude-codenamehook.test.js`
  AC-2 — a pre-existing violation seeded into history (via a fixture-only `--no-verify`) and
  left unstaged by a clean edit to a different line does **not** block the commit; the
  source-level grep asserts `diff --cached`.

- **AC-4. Added lines are derived by POSITION, never by re-testing a line's own text against
  the `+++ b/<path>` header shape (BUG-182).** The old `startsWith('+') && !startsWith('+++')`
  filter silently dropped any genuine added line whose own content began with two literal `+`
  characters. This module now derives added lines from `claude-codename-diff.js`'s
  `splitDiffSections()`, which classifies header-vs-hunk-body by position (inside a `@@` hunk
  body or not). **Proven by:** `claude-codenamehook.test.js` BUG-182 — the installed hook
  rejects a staged line whose content begins `++<abbr>`; the source-level test asserts the
  `++`-prefixed line survives into `stagedAddedLines()` rather than being dropped as a header.

- **AC-5. The scan is immune to a forced-color git config (BUG-185).** `--no-color` is passed
  explicitly so `color.ui`/`color.diff` being forced to `always` in any applicable git config
  cannot prepend ANSI escapes to the `diff --git `/`@@ ` marker lines that
  `claude-codename-diff.js` matches by raw text at the true start of the line. **Proven by:**
  the BUG-185 guard-side tests in `claude-codename-guard.test.js` (a genuine violation staged
  under `color.ui=always` is still denied; a clean commit under the same config is still
  allowed) — the call site `--no-color` flag is the same one this module passes.

### C. Fail-closed posture — propagate, never catch

- **AC-6. The module is fail-closed: a `git diff --cached` invocation failure, or the shared
  pattern module throwing / failing to load, is NOT caught here.** `stagedAddedLines()` /
  `stagedPathHeaderLines()` throw on git failure; `scanStagedDiff()` throws if `patterns.scan()`
  throws. There is no fallback pattern list and no `try/catch` that swallows the error and
  reports "clean" — a broken shared module means this scan cannot verify anything, so it must
  not silently report clean. The *caller* (`githooks/commit-msg`'s `main()`) is responsible for
  turning any thrown error into a denied commit. This is the opposite posture of the demoted
  `claude-author-guard.js` (fail-open), and the header states that contrast explicitly — a
  codename leak into a public repo cannot be withdrawn, unlike an identity mismatch which costs
  a human seconds. **Proven by:** `claude-codenamehook.test.js` AC-7 — with
  `CLAUDE_CODENAME_SCAN_FORCE_ERROR=1` forced, a commit with clean content and a sanctioned
  identity still exits non-zero and creates no commit object; the header-grep test asserts
  `FAIL-CLOSED` appears in both the hook source and this module.

- **AC-7. The test-only escape hatch is inert in production.** `CLAUDE_CODENAME_SCAN_FORCE_ERROR=1`
  makes `stagedAddedLines()`/`stagedPathHeaderLines()` throw *without invoking git at all* —
  it exists solely so AC-6's fail-closed test can prove the hook's behaviour without breaking
  git on the test machine. It is read nowhere else and has no effect when unset (ASM-737).
  **Proven by:** review of the source (`process.env.CLAUDE_CODENAME_SCAN_FORCE_ERROR === '1'`
  is the only read); AC-7's test exercises it and every other test leaves it unset.

### D. The explanation must not leak the name

- **AC-8. A hit description never prints the forbidden literal.** Every hit this module emits is
  a `patterns.scan(..., where, hits)` call whose description is built by the shared module as
  `<surface>: contains <category>.` — where `<category>` is a generic `what` string from
  FEAT-121 (e.g. "the full reference title", "the distinctive single word from the reference
  title", "a numbered abbreviation of the reference title", "a former expansion-content pack
  name"). The `where` this module supplies is likewise generic ("staged content (added lines)",
  "staged file path (new, renamed, or copied file)"). Because the denial reason is composed
  from these generic strings and never from the matched text itself, the message that reaches
  the operator cannot re-introduce the leak the scan exists to prevent. This invariant is
  load-bearing but rests on the `what` strings living in FEAT-121's file, not on this module —
  see ASM-739. **Proven by:** source review (this module contributes only `where` strings and
  forwards `hits`); the invariant is documented as reviewable rather than mechanically grepped
  (ASM-739).

### E. Single source of truth and tests

- **AC-9. This module defines no pattern of its own and no second detection path (GR#3).** Its
  only regex-free behaviour is `git diff` invocation plus two calls into the shared
  `scan()`. **Proven by:** `claude-codenamehook.test.js` AC-5 — a source grep asserts
  `claude-codename-content-scan.js` contains no `new RegExp(` outside the shared-module
  `require`; AC-4 — both this module and `claude-codename-guard.js` `require` the same
  `./claude-codename-patterns.js` path, and emptying the shared `PATTERNS` array empties what
  both observe.

- **AC-10. The module's contract is covered by a passing test suite, and every positive fixture
  is fragment-assembled at test-runtime, never a literal.** The tests live in
  `claude-codenamehook.test.js` (AC-1 through AC-11 plus BUG-182/BUG-183 regressions); every
  positive fixture assembles its forbidden content from fragments (the `ABBR` constant built
  from `['C','S','1'].join('')`), matching GR#22's own discipline so the test file cannot
  itself become a violation. **Proven by:** `node --test claude-codenamehook.test.js` green, and
  review of the fixture-building discipline in that file's header and `ABBR` definition.

### F. Disclosed limitations — stated, not glossed over

- **AC-11. The module's real guarantee is "content committed via `git commit` or `git merge`
  cannot introduce a GR#22 violation" — not "...via any commit-creating verb."**
  `cherry-pick`, `revert`, and `am` do not invoke `commit-msg` at all on this machine's git
  (ASM-386, a property of git's own hook dispatch), and an interactively-composed commit
  *message body* (not passed via `-m`) is out of scope because this module inspects the staged
  diff, never the `commit-msg` hook's own `$1` argument. Both gaps are named in the module
  header, and the message-body gap is escalated rather than resolved unilaterally here
  (ASM-742). **Proven by:** `claude-codenamehook.test.js` AC-10 (header names cherry-pick,
  revert, am, citing ASM-386) and AC-11 (header names the message-body gap).

## Out of scope

- Command-text and branch-name scanning — owned by the retained PreToolUse
  `claude-codename-guard.js`, not duplicated here.
- Commit-message body composed interactively — see AC-11, disclosed gap (ASM-742).
- `cherry-pick` / `revert` / `am` coverage — see AC-11 (ASM-386), structurally uncovered by any
  `commit-msg`/`pre-commit` hook.
- The GR#22 pattern *values* and fragment-assembly *technique* — owned by FEAT-121
  (`claude-codename-patterns.js`); this module consumes them, never defines them.
- Header-vs-hunk line classification — owned by FEAT-120 (`claude-codename-diff.js`).

## Assumptions

Logged via `node claude-bow.js add assumption`, per the brief:

- **ASM-736** — the FEAT-119/120/121 split of labour (content-scan owns surfaces + fail-closed
  propagation; diff owns line classification; patterns owns the match set and explanation
  wording) is the BA's read of the committed code, not stated in any one item description.
- **ASM-737** — fail-closed is caller-enforced: this module throws and never catches, and the
  `CLAUDE_CODENAME_SCAN_FORCE_ERROR` escape hatch is test-only and inert when unset.
- **ASM-742** — `cherry-pick`/`revert`/`am` and the editor-composed message body remain
  disclosed, out-of-scope gaps inherited from FEAT-046, not defects to close here.
