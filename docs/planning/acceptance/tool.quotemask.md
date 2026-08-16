BOW code: FEAT-129

# Acceptance criteria — tool.quotemask (FEAT-129)

**BOW code:** FEAT-129 (P2) — "Portable shell quote-mask primitive shared by the guards."
**Module key / GUID:** `tool.quotemask` / `45165319-674a-4415-adc8-f77bab928245`
**Spec refs:** GR#22 (guards); GR#3 (Single Source of Truth); BUG-123 round 6 (Marrow's odd-embedded-quote finding and the consolidation ruling); BUG-077 (backslash-escape-outside-quotes); BUG-078 (heredoc bodies are opaque); BUG-080 (ANSI-C quoting `$'...'`); BUG-044 round 2 (shared `consumeShellToken`/`dequoteShellToken`); BUG-081 (open — CRLF heredoc terminator, explicitly out of scope here); ASM-344 (toggle state machine, not a real shell lexer); ASM-351 (unterminated-heredoc swallow-to-EOF).
**Date:** 2026-08-16
**Status:** **retrospective** — `claude-quote-mask.js` is already committed; this file documents its contract, not a build gate. A Tester/Destructive verifies fidelity (the committed suite passes and the code matches the described contract) rather than constructing new code. Framing logged as ASM-771.
**Package under test:** `claude-quote-mask.js` (repo root), a pure, stdlib-free Node module (no `require()` at all). Its tests: `claude-quote-mask.test.js` (direct coverage) and `quote-mask-drift.test.js` (cross-file drift tripwire).
**Standard gates:** Node.js — `node --check claude-quote-mask.js`; `node --test claude-quote-mask.test.js`; `node --test quote-mask-drift.test.js`; no npm dependency (it has none to introduce); SG-6 (no Co-Authored-By).

## What this module is (read before the ACs)

`buildQuoteMask()` started life inside `claude-author-guard.js` (BUG-043/BUG-077/BUG-078), was hand-copied into `claude-pre-commit-check.js`, and then a third positional scanner appeared in `claude-git-commit-trigger.js` — three divergent implementations of the same escape rules, the exact GR#3 violation BUG-123 round 6 was filed to end. This file is the consolidation: the only place `function buildQuoteMask(` may appear in the repo, required by every consumer rather than re-forked. The ACs below document that consolidation as the committed contract.

## Acceptance criteria

### Behaviour (the mask and token-walk contract)

- **AC-1. `buildQuoteMask(text)` returns a same-length boolean array.** `mask[i] === true` iff character `i` sits inside an open single- or double-quoted region (including the quote characters themselves) or inside an inert heredoc body; `false` otherwise. Check: `claude-quote-mask.test.js` passes the plain-unquoted, simple-double-quoted, and simple-single-quoted cases, each asserting `mask.length === text.length` and the expected 0/1 pattern.

- **AC-2. Backslash escaping follows real shell semantics, three ways.** (a) Inside double quotes, a backslash escapes the next character, so `\"` does not close the region. (b) Inside single quotes there is **no** escape character — a backslash is a literal character and the very next `'` closes the region. (c) Outside any quoted region, a backslash still consumes the next character literally **without** opening a quoted region, even when that character is itself a quote (BUG-077). Check: the "escaped quote inside double quotes" case, the "single-quoted regions take NO backslash escapes" case, and the BUG-077 "escaped quote outside any region does not open a phantom region" case all pass.

- **AC-3. ANSI-C quoting `$'...'` is a distinct quote form (BUG-080).** Inside it, backslash **does** escape the following character (same rule as double quotes), so `\'` is a literal escaped quote, not the terminator; the region is closed by a bare `'`, not `$'`. Check: the BUG-080 cases pass — the internal-escaped-quote case, the "no leading `$`" POSIX regression guard, and the benign-ANSI-C no-over-trigger case.

- **AC-4. Heredoc bodies are masked inert (BUG-078).** A `<<` header — optional `-` (tab-stripping form), optional whitespace, then a bare/single-quoted/double-quoted delimiter word — is recognised by `matchHeredocHeader(text, i)` returning `{ afterHeader, word, stripLeadingTabs }` (or `null`). The body through the terminator line is masked `true` **without** touching quote state, so stray/unbalanced quote characters inside the body cannot leak past the terminator. Check: the BUG-078 heredoc case passes.

- **AC-5. `consumeShellToken(text, start, quoteMask?)` walks to the first genuinely-unquoted whitespace boundary.** It returns the index one past the token, or `-1` when the token is empty (`start` is itself unquoted whitespace/EOF) or unterminated (reaches end of `text` while still inside an open quote — `buildQuoteMask`'s swallow-to-EOF fail-safe). It lazily builds the mask from `text` when one is not passed. Check: the `consumeShellToken` cases pass — stop-at-first-unquoted-whitespace, `-1` for empty, `-1` for unterminated, and reuse-of-a-precomputed-mask.

- **AC-6. `dequoteShellToken(token)` strips a token's own quote characters and resolves its escapes** using the same toggle/escape rules, returning the shell's view of the value (e.g. `user.email="fake attacker <fake@evil.com>"` → `user.email=fake attacker <fake@evil.com>`). It operates on an already-sliced token, so it does no heredoc recognition. Check: the `dequoteShellToken` cases pass (double-quoted, single-quoted, escaped-embedded-double-quote, bare-unquoted-unchanged, and the BUG-044 round 2 end-to-end pair).

- **AC-7. Single Source of Truth (GR#3): one production declaration, consumed by reference.** `claude-quote-mask.js` is the only **non-test** file declaring `buildQuoteMask` (as a declaration, function expression, arrow function, or method shorthand); the former guard copies now `require()` the shared module and re-export the function by reference rather than reimplementing it. Check: the "re-export the SAME shared function object" test passes (`assert.equal(authorGuard.buildQuoteMask, buildQuoteMask)` and the `claude-pre-commit-check.js` equivalent); and `quote-mask-drift.test.js`'s `discoverCopies()` asserts the discovered set equals `KNOWN_COPIES_AS_OF_2026_08_12 = ['claude-quote-mask.js']`. (A test file may still carry a local `buildQuoteMask` *reconstruction* as a regression-proof fixture — `collectJsFiles` excludes `*.test.js` by design, so those are not "copies" in the drift sense.) Written against the live discovered set, not a hardcoded consumer count — ASM-772.

- **AC-8. The module header documents its known limitations honestly.** It states, by name, ASM-344 (a toggle-based state machine, not a real shell lexer — a deliberately unbalanced quote earlier in the string can still flip quote-state parity) and the BUG-081 CRLF-heredoc-terminator caveat (the terminator-equality check does not yet normalise `\r`). Check: reviewed by eye against the module header.

### Fail-open / fail-closed

- **AC-9. `buildQuoteMask` is pure and total — there is no "internal error" state to fail open or closed on.** It performs no I/O (no `fs`, `child_process`, `process.stdin`, or `process.exit`) and never throws; every input yields a same-length mask. Check: `grep -n "require(\|spawnSync\|process\." claude-quote-mask.js` finds no I/O or process usage, and the full test corpus completes with no unhandled rejection.

- **AC-10. The fail-safe for unterminated constructs is swallow-to-EOF, which is fail-closed for the caller's detection purpose.** An unterminated quote or heredoc masks everything to end-of-string as `true`, and `consumeShellToken` returns `-1` (unparseable) rather than a guessed boundary — so nothing past the unterminated construct is ever scanned as a real invocation. Check: the unterminated-quote case and the `consumeShellToken`-unterminated case both pass; the module header states the same contract in prose.

- **AC-11. A caller receiving `-1` from `consumeShellToken` must treat the token as unparseable and stop, not guess.** This is the documented degradation path for ASM-344's parity-flip residual: when the toggle is beaten, the result is "not scanned", never a manufactured ALLOW. Check: the module header's `consumeShellToken` contract prose states this; reviewed by eye.

### Tests

- **AC-12. `claude-quote-mask.test.js` exists and passes**, covering the escape-aware corpus including Marrow's round-5 odd-embedded-quote repro and its 1/3/5 generalisation (plus the 0/2/4 even baselines). Check: `node --test claude-quote-mask.test.js` exits 0; a spot-check confirms the BUG-077/BUG-078/BUG-080/BUG-123/BUG-044 cases are all present by name.

- **AC-13. The drift tripwire still runs over the single discovered copy.** `quote-mask-drift.test.js` remains live as the informational alarm against a future accidental re-fork. Check: `node --test quote-mask-drift.test.js` exits 0 (its inventory test runs against whatever `discoverCopies()` finds at the time).

- **AC-14. The module is syntactically valid and introduces no dependency.** Check: `node --check claude-quote-mask.js` exits 0; `git diff --cached -- package.json` (at the original extraction commit) introduced no new dependency, and the file has no `require()` at all.

## Out of scope

- **Fixing BUG-081** (CRLF heredoc terminator normalisation) — tracked separately; the drift test deliberately asserts only cross-copy agreement for the CRLF case, not a golden "correct" answer.
- **Fixing ASM-344** (building a real shell lexer rather than a toggle) — the module's header already disclaims being a full shell parser.
- **Consolidating the two consumers further** (merging the guard files) — out of scope for the same reason ASM-356 gives: a PreToolUse guard must still emit a decision when a shared dependency is broken.

## Assumptions logged

- **ASM-771** — the retrospective framing: these ACs document the committed contract, and the tests ACs assert the existing suite already covers the named behaviours (fidelity check, not new construction).
- **ASM-772** — the single-source-of-truth AC (AC-7) is written against the live `discoverCopies()` result rather than a hardcoded consumer count, so it does not go stale when a consumer is added or removed.

## Escalations

- None. This is a documentation-only pass over already-committed, already-tested code; no spec/brief conflict surfaced. One judgment call flagged for Bill's awareness rather than as a conflict: AC-7 phrases the single-declaration guarantee as "exactly one `function buildQuoteMask(` declaration, consumed by reference" rather than naming a fixed set of consumer files, specifically so the AC survives future consumer churn without a re-edit (ASM-772).
