BOW code: FEAT-120

# Acceptance criteria — tool.codenamediff (FEAT-120)

**BOW code:** FEAT-120
**code.json:** `tool.codenamediff` (GUID `147cceb9-c50e-4719-9f2e-df80e4b8c806`)
**File under test:** `claude-codename-diff.js` (root tooling, layer tooling)
**Spec refs:** GR#22 (Codename Discipline — this module is the classifier that makes the
enforcing layer unable to *drop* a violation); GR#3 (Single Source of Truth — the reason this
module exists at all); BUG-182 (the added-line text-prefix-filter bypass this module fixes);
BUG-137 (the path-header surface this module extracts); BUG-185 (the forced-color blind spot,
defended against here); ASM-738 (assumption logged against this item).
**Date:** 2026-08-16
**Status:** RETROSPECTIVE — code is already committed; these criteria document the contract
of the committed module. Each AC cites the test that already proves it.

> **GR#22 discipline in this file itself.** The forbidden literals are never written here; this
> module contains none of them and this acceptance file refers to them by category only.

## What this module is

`claude-codename-diff.js` is the **shared unified-diff line classifier** extracted to fix
BUG-182. It turns raw `git diff --cached --unified=0` output into two strings — `addedLines`
and `pathHeaderLines` — by classifying each line as **header-region** or **hunk-body** by
POSITION, never by matching the header lines' own text. Both GR#22 consumers
(`claude-codename-content-scan.js`, FEAT-119, and `claude-codename-guard.js`) `require()` this
module and call `splitDiffSections()` rather than each re-deriving the split — the exact
duplication that let BUG-182 exist as one bug with two independent occurrences.

Its "block" responsibility is indirect but load-bearing: it must never *drop* a line that
carries forbidden content, and it must never *misroute* a line into the wrong bucket, because
a dropped or misrouted line is a violation the pattern scanner never sees. The reference
title, its abbreviations, and its numbered form are "blocked" only if the classifier first
delivers the bytes that contain them to the scanner (ASM-738).

## User stories

- As **the enforcing content scan (FEAT-119)**, I need added-line content derived by position
  so that a genuine added line whose own text begins with two literal `+` characters is treated
  as content, not silently dropped as a `+++ b/<path>` header lookalike.
- As **the PreToolUse guard**, I need the same header-vs-hunk split I used to hand-roll, so a
  fix to the classifier reaches me without a second, divergent copy (GR#3).
- As **a committer whose new/renamed file carries a forbidden *name* with a clean body**, I
  need the per-file path headers surfaced so that a path-only violation is caught — but a
  clean rename must still pass, so path extraction must not over-fire.

## Acceptance criteria

### A. Classify by position, never by text (the BUG-182 contract)

- **AC-1. A line is classified as header-region or hunk-body strictly by position (`inHunk`),
  never by re-testing a line's own text against the header lines' shape.** A real unified diff
  has exactly one header region per file section — everything from its `diff --git` line up to
  (not including) the first `@@` hunk marker — and every line at or after that `@@` marker is
  unambiguously hunk-body content until the next `diff --git` line starts a new section. A
  boolean `inHunk` tracks that; `FILE_START_RE` (`/^diff --git /`) resets it to false, `HUNK_RE`
  (`/^@@ /`) sets it true. Because the classifier never looks at a line's content to decide its
  bucket, a hunk-body line's own first two characters can never be misclassified as a header,
  regardless of what the content says. **Proven by:** `claude-codename-guard.test.js` BUG-182
  sanity test — the pre-fix text-prefix filter drops a `++`-prefixed added line entirely, while
  `splitDiffSections()` recovers it into `addedLines`; and the hook-level BUG-182 tests in both
  test files.

- **AC-2. A genuine added line whose own content begins with two literal `+` characters is
  emitted as `addedLines` content, marker stripped, never dropped as a header (BUG-182).**
  The old filter `l.startsWith('+') && !l.startsWith('+++')` guessed the one-per-file
  `+++ b/<path>` header by text shape; git emits an added line whose payload begins `++` as its
  own single `+` marker followed by `++...` — textually identical to the header line. Under
  position-based classification that ambiguity cannot arise. **Proven by:**
  `claude-codename-guard.test.js` BUG-182 sanity (asserts `splitDiffSections(diff).addedLines`
  equals the `++`-prefixed payload) and `claude-codenamehook.test.js` BUG-182 source-level test.

- **AC-3. A new file section always restarts in its own header region, even when the previous
  file's last hunk left `inHunk === true`.** The `FILE_START_RE` branch sets `inHunk = false`
  and continues, so the next file's `diff --git`/`index`/`---`/`+++`/rename/copy lines are
  classified as headers, not as hunk-body content. **Proven by:** the multi-file shapes
  exercised by the BUG-183 and clean-rename tests (a rename produces two file sections in one
  diff and is classified correctly).

### B. Path-header extraction (the BUG-137 surface)

- **AC-4. The per-file header lines that identify a new, renamed, or copied file's PATH are
  extracted into `pathHeaderLines`, path text only, prefix stripped.** `PATH_HEADER_RE`
  (`/^(\+\+\+ |--- |rename to |rename from |copy to |copy from )/`) matches the header-region
  lines that carry a path, and the matched prefix is removed so the surviving text is the path
  itself. This is the BUG-137 shape: a forbidden pattern living only in a filename (never in
  content or the commit message) is visible to the pattern scanner via this bucket. **Proven
  by:** `claude-codenamehook.test.js` BUG-183 (`stagedPathHeaderLines()` surfaces a forbidden
  filename) and the clean-rename negative control (a clean rename produces no false hit).

- **AC-5. A clean rename or copy — clean old path, clean new path, clean body — yields no false
  hit from path scanning.** Path extraction must not over-fire: the classifier surfaces paths
  but the *matching* decision stays with the shared pattern set, so an ordinary rename of an
  ordinary file passes. **Proven by:** `claude-codenamehook.test.js` BUG-183 clean-rename test
  (rename `original.txt` → `renamed.txt` commits with zero exit and one new commit).

### C. Defense in depth against forced color (BUG-185)

- **AC-6. Classification is immune to a forced-color diff, even if a future call site omits
  `--no-color`.** Before each line is tested against the header/hunk regexes, one leading ANSI
  CSI escape sequence (`ESC [ params final-byte`) is stripped by `stripLeadingAnsi()`. The
  primary fix is `--no-color` at both call sites (FEAT-119 and the guard), so this strip is a
  no-op there; it exists as defense-in-depth in case a future call site invokes git without the
  flag. It is deliberately *not* a general ANSI parser — classification only ever depends on a
  line's true start. Content extraction still uses the ORIGINAL line, since stripping is for
  position classification only. **Proven by:** `claude-codename-guard.test.js` BUG-185 unit
  test (a colored diff with escapes on the `diff --git`/`@@`/`+` marker lines is still
  classified correctly, recovering both `addedLines` and `pathHeaderLines`) and its
  pre-fix sanity test (without the strip, the raw-text match drops the added line).

### D. Single source of truth

- **AC-7. Both consumers `require()` this module and call `splitDiffSections()` — neither
  re-derives the header-vs-content split.** The module exports only `splitDiffSections`; there
  is no second classifier path. **Proven by:** `claude-codenamehook.test.js` AC-4/AC-5 and the
  `require('./claude-codename-diff.js')` references in both `claude-codename-content-scan.js`
  and `claude-codename-guard.js` (see their headers).

### E. Tests

- **AC-8. The classifier's contract is proven by tests that mutate the diff shape, not by
  grepping for it.** The tests for this module live inside `claude-codename-guard.test.js`
  (BUG-182 sanity + pre-fix reproduction, BUG-185 unit + pre-fix reproduction) and
  `claude-codenamehook.test.js` (BUG-182 source-level, BUG-183 path-header). The BUG-182 and
  BUG-185 tests each include a *pre-fix* sanity assertion proving the old behaviour genuinely
  fails on the fixture — so a pass is a real fix, not a fixture that never exercised the attack.
  **Proven by:** `node --test claude-codename-guard.test.js` and
  `node --test claude-codenamehook.test.js` green.

## Out of scope

- The forbidden-pattern definitions themselves — owned by FEAT-121
  (`claude-codename-patterns.js`); this module carries no patterns and no pattern regexes.
- The git invocation and fail-closed propagation — owned by FEAT-119
  (`claude-codename-content-scan.js`); this module is a pure function over diff text.
- Command-text / branch-name / commit-message scanning — owned by the PreToolUse guard.
- A general ANSI/terminal parser — AC-6 is deliberately a single leading-CSI strip, not a full
  escape-sequence parser.

## Assumptions

Logged via `node claude-bow.js add assumption`, per the brief:

- **ASM-738** — the position-based `inHunk` classifier is assumed to fully supersede the
  BUG-182 text-prefix filter (`startsWith('+') && !startsWith('+++')`) with no residual
  text-prefix path; the pre-fix sanity tests are the evidence that the old path was the sole
  defect, not one of several.
