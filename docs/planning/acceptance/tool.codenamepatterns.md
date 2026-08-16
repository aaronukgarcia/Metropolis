BOW code: FEAT-121

# Acceptance criteria — tool.codenamepatterns (FEAT-121)

**BOW code:** FEAT-121
**code.json:** `tool.codenamepatterns` (GUID `31c3389f-87b1-43b1-9cd4-fd711016ef44`)
**File under test:** `claude-codename-patterns.js` (root tooling, layer tooling)
**Spec refs:** GR#22 (Codename Discipline — this module is the single source of "forbidden");
GR#3 (Single Source of Truth — the entire point of this file's existence); GR#6 (GUID
documentation — the logic was moved, not rewritten, out of `claude-codename-guard.js`);
BUG-123 / BUG-137 / BUG-140 / BUG-144 (the bypass classes the boundary logic closes);
ASM-150 (the fingerprint ruling that makes the former expansion-content pack names forbidden);
ASM-739 / ASM-740 (assumptions logged against this item).
**Date:** 2026-08-16
**Status:** RETROSPECTIVE — code is already committed; these criteria document the contract
of the committed module. Each AC cites the test that already proves it.

> **GR#22 discipline in this file itself.** The forbidden reference title, its real name, its
> abbreviations, its numbered sequel form, and its former expansion-content pack names are
> never written literally here — they are referred to by category only. This acceptance file
> applies to itself the exact discipline it documents.

## What this module is

`claude-codename-patterns.js` is the **shared GR#22 forbidden-pattern source**. It was
extracted out of `claude-codename-guard.js` (FEAT-046 / `tool.codenameguard`) so it can be
`require()`'d, UNCHANGED, by both that PreToolUse guard and the enforcing commit-msg content
scan (FEAT-119). It owns the two things that make GR#22 mechanical rather than a memory:

1. **The pattern DATA** — what "forbidden" means, assembled from string fragments at runtime
   so no forbidden literal ever sits whole on disk (this file lives in git; if it held the
   literals as plain text to search for them, it would be the single largest violation of the
   rule it enforces, and it would flag itself on every commit).
2. **The scan MECHANISM** — the one `scan()` entry point both consumers call, plus the
   boundary-classification rule that decides whether a candidate match is a real hit or
   ordinary embedded prose.

The logic is UNCHANGED from its pre-extraction form — this file is a MOVE, not a REWRITE
(GR#6). Its header forbids "improving" the pattern set or boundary logic without a fresh BOW
item (ASM-740).

## User stories

- As **every other GR#22 layer**, I need one place that defines "forbidden" so a pattern added
  to catch a new bypass cannot silently apply to only one layer, and a future edit to one copy
  and not the other cannot happen — there is only one copy.
- As **a committer writing ordinary technical prose**, I need the bare two-letter abbreviation
  (which appears innocently in prose) *not* to fire, so the guard does not get disabled for
  false positives — while the numbered form of the same abbreviation *does* fire, because that
  is unambiguous.
- As **an operator whose commit is denied**, I need the denial to name *what category* matched
  without printing the forbidden literal itself.

## Acceptance criteria

### A. What it must block — the reference title, its abbreviations, and its numbered form

- **AC-1. The `PATTERNS` set covers, at minimum: the reference title's real name (the two-word
  title, with any separator between the words and either of the two number-forms of the first
  word); the distinctive single word from the title (on its own, with a boundary rule); and the
  numbered abbreviation — the two-letter abbreviation immediately followed by the digit 1 or 2
  (the "numbered sequel form").** These three categories are the core of GR#22's "real name,
  abbreviations, and numbered sequel form." **Proven by:** the end-to-end denials in
  `claude-codenamehook.test.js` and `claude-codename-guard.test.js`, whose positive fixtures are
  all assembled from the `ABBR` fragment (`['C','S','1'].join('')` — the numbered form) and the
  other runtime-synthesized fragment fixtures.

- **AC-2. The former expansion-content pack names are forbidden by fingerprint, per the
  ASM-150 ruling.** This combination of pack names, taken together, is as much a fingerprint of
  the reference title as the title itself. Each is matched with a flexible separator regex that
  absorbs "&" vs "and" and hyphenation variants. The committed set is treated as COMPLETE and is
  not to be extended without a fresh BOW item (ASM-740; the module header states this).
  **Proven by:** source review of the `PATTERNS` entries labelled "a former expansion-content
  pack name"; the set is eight entries as committed.

- **AC-3. The bare two-letter abbreviation, on its own (no trailing digit), is deliberately NOT
  matched.** It appears innocently in ordinary technical prose, and a guard that fires on false
  positives gets disabled within a day — a failure mode this project has catalogued repeatedly.
  Only the abbreviation immediately followed by a 1 or 2 digit matches. **Proven by:**
  `claude-codenamehook.test.js` AC-9(b) negative control — ordinary prose containing the bare
  two-letter abbreviation with no digit ("the CS department reviewed this change") is accepted
  with zero exit and one new commit.

- **AC-4. Fragments are never joined in source — no forbidden literal appears on disk.** The
  fragments (`SKY`, `LINES`, `CITY`, `IES`, the pack-name fragments) are separate `const`s, and
  the one place two fragments would otherwise sit adjacent (`SAN_FRAN()`) is assembled inside a
  *function* purely to keep the two place-name fragments from ever appearing as a single joined
  literal in the file. `PATTERNS` is built at runtime. **Proven by:** source review — the file's
  own header explains this is not obfuscation but the rule applied to itself; the same
  fragment-assembly discipline is mirrored in both test files' `ABBR` constants.

### B. The boundary rule — catches snake_case and camelCase, rejects embedded prose

- **AC-5. A forbidden pattern adjacent to an underscore (snake_case) or directly to a capital
  letter with no separator (camelCase) is a hit (BUG-140).** Ordinary regex `\b` is a
  `\w`/non-`\w` transition and underscore is a `\w` character, so `\bword\b` silently missed
  `word_export`; and a lookaround `[a-zA-Z]` class cannot tell case apart once the `i` flag
  folds it. The fix is a plain-JS boundary check in `lineMatchesWithBoundary()`: a `boundary:
  true` pattern is matched without anchors via a global exec loop, and a match counts only if
  the character immediately before/after it is NOT a lowercase letter (an adjacent digit,
  underscore, uppercase letter, punctuation, whitespace, or string edge all count as a
  boundary). **Proven by:** `claude-codename-guard.test.js` BUG-140 — snake_case
  (`<abbr>_module_enabled`) and camelCase (`flag_<abbr>Zone`) fixtures are both denied.

- **AC-6. A forbidden pattern as a camelCase MIDDLE segment — lowercase before, uppercase after
  — is a hit (BUG-144).** BUG-140's original AND semantics (require BOTH sides boundary) missed
  the `prefixWordEngine`-shaped case: a match with a lowercase-adjacent left side but an
  unambiguous uppercase transition on the right was rejected even though the right side alone
  proves it is not ordinary lowercase prose. The fix is OR semantics — a match counts if EITHER
  neighbour is a real boundary. **Proven by:** `claude-codename-guard.test.js` BUG-144
  (`mega<abbr>Zone` is denied).

- **AC-7. A match embedded on BOTH sides in a continuing all-lowercase run is still NOT a hit —
  no over-correction.** Only this one case is rejected under OR semantics, because it is
  genuinely indistinguishable from ordinary prose. **Proven by:** the BUG-140 and BUG-144
  false-positive controls (`a<abbr>zebra`, `x<abbr>y` and similar fully-embedded forms) are
  asserted to pass in `claude-codename-guard.test.js`.

### C. The explanation must not leak the name

- **AC-8. The human-readable hit descriptions produced by `scan()` name the match CATEGORY
  generically — never the forbidden literal itself.** Every `PATTERNS` entry carries a generic
  `what` string ("the full reference title", "the distinctive single word from the reference
  title", "a numbered abbreviation of the reference title", "a former expansion-content pack
  name"), and `scan()` builds `<surface>: contains <what>.` from it. Because the denial reason
  is composed from these generic strings and never from the matched text, the message that
  reaches the operator cannot re-introduce the leak the rule exists to prevent. This is a
  load-bearing invariant that currently rests on the `what` strings being reviewed, not on a
  test that greps them (ASM-739). **Proven by:** source review of the `what` strings and the
  `scan()` template; documented as a reviewable invariant per ASM-739.

### D. Single source of truth — one scan entry point

- **AC-9. `scan(text, where, hits)` is the ONE scan entry point both consumers call — neither
  reimplements matching.** It splits `text` into lines, runs every pattern, and pushes one hit
  per (pattern, first-matching-line) with the line number when the text spans multiple lines.
  `boundary: true` patterns use `lineMatchesWithBoundary()`; `boundary: false` patterns use
  `lineMatches()`. **Proven by:** `claude-codenamehook.test.js` AC-4 — emptying the shared
  `PATTERNS` array empties what both the guard and the content scan observe through `scan()`;
  and the guard-side boundary tests in `claude-codename-guard.test.js`.

- **AC-10. The logic is a MOVE, not a REWRITE (GR#6).** The fragment values and the boundary
  mechanism are unchanged from their pre-extraction form in `claude-codename-guard.js`'s git
  history (BUG-123/137/140/144). **Proven by:** the module header's explicit statement and the
  guard-side regression suite (21 tests) passing post-extraction against the identical fixtures.

## Out of scope

- The git invocation, scan surfaces, and fail-closed propagation — owned by FEAT-119
  (`claude-codename-content-scan.js`).
- Header-vs-hunk line classification — owned by FEAT-120 (`claude-codename-diff.js`).
- The PreToolUse guard's command-text / branch-name / commit-message trigger logic — owned by
  `claude-codename-guard.js` and `claude-git-commit-trigger.js`; this module only supplies the
  pattern data and the scan mechanism those layers call.
- Extending or "improving" the pattern set or boundary logic — the module header requires a
  fresh BOW item for that (ASM-740).

## Assumptions

Logged via `node claude-bow.js add assumption`, per the brief:

- **ASM-739** — the explanation-must-not-leak guarantee rests on the generic `what` strings in
  `PATTERNS`; no test currently greps those strings for a forbidden literal, so the ACs document
  it as a reviewable invariant rather than a mechanical check.
- **ASM-740** — the forbidden set includes the former expansion-content pack names per the
  ASM-150 fingerprint ruling; the committed `PATTERNS` list is treated as complete and is not to
  be extended without a fresh BOW item, matching the module header.
