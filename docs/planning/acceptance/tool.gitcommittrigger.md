BOW code: FEAT-126

# Acceptance criteria — tool.gitcommittrigger (FEAT-126, retrospective)

**Module key:** tool.gitcommittrigger (code.json GUID bbca9146-1a3d-448f-bbc9-f556e70a8bb9)
**BOW code:** FEAT-126
**Spec refs:** M0-ENG §5 (hooks); BUG-123 (the bug family this module exists to
close — rounds 1 through 6 are narrated in the module's own header); BUG-088
(the trigger-bypass class the four guards shared); SEC-008; GR#3 (Single Source
of Truth — this is the ONE shared trigger, extracted specifically because BUG-123
is what happens when the same trigger logic is hand-copied into four files);
`docs/planning/acceptance/tool.secretguard.md` (the BUG-088 remediation spec that
frames the guard/checker split this module sits on the guard side of).
**Date:** 2026-08-16
**Status:** retrospective — written after the code shipped (the module landed as
part of the BUG-123/BUG-088 remediation wave; this file states its contract so
the next round is judged against a contract, not against the code from scratch).
**Package under test:** `claude-git-commit-trigger.js` (repo root, Node.js) and,
as its shared dependency, `claude-quote-mask.js`.
**Standard gates:** Node, not Go — SG-1/2/4/7 do not apply. `node --check
claude-git-commit-trigger.js`; the consumer suites enumerated in AC-13 passing;
SG-6 (no Co-Authored-By).

## Why this file exists

Four PreToolUse guards — `claude-secret-guard.js`, `claude-version-guard.js`,
`claude-plan-guard.js`, and `claude-codename-guard.js` — each intercept `git
commit` (codename-guard also `git push`) with a boundary-anchored trigger so a
quoted *mention* of "git commit" in prose never scans but a real invocation
always does. All four originally shared one hand-copied trigger regex, and all
four shared one bug (BUG-123): the trigger only tolerated a single `-C <dir>`
global option between `git` and the verb, so `git -c user.email=... commit`
(and `-c commit.gpgsign=false`, `--git-dir=...`, stacked `-c`s) never matched.
This module is the fix, extracted into **one** shared file (GR#3) so the same
logic is never hand-copied into four files and only one copy ever gets fixed in
a security pass again.

The module's two headline design decisions, both from its own header and both
carried into the ACs:

1. **Tokenize, do not pattern-match.** Rounds 2–5 proved a single alternation
   regex that both consumes a variable-length option-grammar prefix and asserts
   a fixed verb at a computed offset is the bug *class*, because JS regex
   alternation backtracks into a different alternative when the tail fails.
   `findGitVerbToken()` instead advances a cursor one option at a time — each
   iteration runs a small self-contained anchored regex and unconditionally
   advances — so there is no outer construct to backtrack into, and the verb,
   once extracted, is compared by exact `Set.has()` membership, never substring
   match.
2. **Delegate quote-scanning to the canonical scanner.** Round 6 removes all
   quote-scanning logic from this module: `consumeShellToken()` (and the mask it
   uses) live in `claude-quote-mask.js`, required here, so a `-c "user.email=x"`
   value with an odd count of embedded escaped quotes parses exactly once, the
   same way every other consumer parses it.

## Behaviour

### A. The public interface (unchanged on purpose)

- **AC-1. `buildAnchoredGitVerbTriggerRegex(verbAlternation)` and
  `buildBareGitVerbTriggerRegex(verbAlternation)` return an object with a
  `.test(command)` method**, taking a `'commit'` / `'commit|push'` style
  verb-alternation **string** and returning a truthy/falsy result — the entire
  interface the four call sites and their existing suites already depend on
  (`GIT_COMMIT_RE.test(...)`). Internally the returned object is **not** a real
  RegExp; it is a tokenizer wrapped behind the same `.test()` shape, so the fix
  required zero call-site changes. Check: a passing test asserts
  `buildAnchoredGitVerbTriggerRegex('commit').test('git commit -m x') === true`
  and `typeof trigger.test === 'function'`; a passing test asserts
  `buildBareGitVerbTriggerRegex('commit|push').test('git push') === true`.
- **AC-2. The verb comparison is exact set membership, not substring or
  alternation match.** This is what makes `commit.gpgsign=false`,
  `commit-repo`, and `commit-should-not-match` all non-matches: taken as a whole
  token, none of them equals the string `"commit"`. Check: the four canonical
  BUG-123-round-2 repros — `git -c commit.gpgsign=false status`,
  `git -c commit.template=/dev/null diff`, `git -C commit-repo status`, and (via
  the bare trigger) `git -c push.default=commit-should-not-match log` — all
  return `false` through a consumer suite (see AC-13); these are asserted as
  `false`, not merely "not asserted".
- **AC-3. `findGitVerbToken(text, pos, quoteMask)` returns the verb word that
  follows a matched `git` token (after consuming a run of global options), or
  `null`** if there is no options-run-then-word shape (e.g. `git` at end of
  string). It is exported for direct testing and reuse. Check: a passing test
  asserts `findGitVerbToken('git -c a=b commit -m x', 3)` returns `'commit'`
  and `findGitVerbToken('git', 3)` returns `null`.

### B. Option grammar and the tokenizing walk

- **AC-4. Global options are consumed one at a time, `-c`/`-C` first, then the
  rest.** The recognised grammar is: `-c <key>=<value>` and `-C <dir>`
  (quote-aware — their value may contain an embedded quote, so they are handled
  by `matchQuoteAwareOption` before the generic catch-all), then
  `--git-dir=`/`--work-tree=`/`--namespace=` (either `=value` or ` value` form),
  then a conservative catch-all for any other long (`--word` / `--word=value`) or
  short (`-x`) global option. The ordering matters and is the module's own
  stated rationale: if the generic `-[A-Za-z]` catch-all ran first it would
  match a bare `-c`/`-C` with no value and reintroduce round 2's failure mode
  via ordering. Check: a passing consumer test asserts each of the stacked
  shapes matches a real trailing verb — `git -c a=b -c c=d commit`,
  `git -c a=b -C /some/dir commit`, `git -C /some/dir -c a=b commit`,
  `git --git-dir=/x/.git --work-tree=/x commit`, `git -c user.name="John Q
  Commit" commit`.
- **AC-5. The verb word is a whole identifier token, hyphens included.** The
  verb regex captures `[A-Za-z][A-Za-z-]*`, so `commit-repo` is extracted as the
  single word `"commit-repo"`, never truncated to a `commit` prefix. Check: the
  `git -C commit-repo status` repro (AC-2) returns `false` precisely because the
  extracted word `commit-repo` fails `Set.has('commit')`; asserted through the
  consumer suite.
- **AC-6. Quote-aware option values delegate entirely to
  `claude-quote-mask.js`.** This module contains **no** quote-scanning logic of
  its own: `consumeShellToken` and `buildQuoteMask` are required from
  `claude-quote-mask.js` and re-exported under the same local name. Check:
  `grep -n "indexOf('\"')\|indexOf(.*,\s*i\s*+\s*1)" claude-git-commit-trigger.js`
  finds no hand-rolled positional quote pairing; `grep -n "require('./claude-quote-mask.js')"
  claude-git-commit-trigger.js` matches; and the Marrow odd-embedded-quote repro
  (`git -c key="a\"b" commit -m x`) returns `true` through a consumer suite —
  the round-4 positional scanner would have returned a silent `false` on it.
- **AC-7. An unparseable option value stops the loop rather than guessing.**
  When `consumeShellToken` returns -1 (empty or unterminated value), the module
  treats that `git` occurrence as a non-match — it does not throw and does not
  invent a token. Check: a passing test asserts
  `buildAnchoredGitVerbTriggerRegex('commit').test('git -c key="unterminated commit')`
  returns `false` without throwing; this is the module's own fail-closed-for-a-
  trigger posture (see AC-11).

### C. Two boundary styles, owned by the callers

- **AC-8. The anchored trigger matches only at the start of the command or
  immediately after a shell separator** (`; & | ( \n`) — a quoted mention of
  "git commit" inside prose does not match. This is the shape the three
  fail-open/fail-closed commit guards (secret/version/plan) use. Check: a
  passing consumer test asserts `true` for `git commit -m x`, `true; git commit
  -m x`, `true && git commit -m x`, `true | git commit -m x`, `(git commit -m
  x)`, and a newline-separated pair; and `false` for `npm install` and a prose
  string that merely contains the words "git commit".
- **AC-9. The bare trigger matches on a word boundary with no shell-boundary
  anchoring** — the codename guard's original, intentionally looser shape. It
  accepts the small false-positive cost this implies in exchange for never
  missing a real invocation. Check: a passing consumer test asserts the bare
  trigger fires for `git commit` and `git push` and does not fire for `git
  status`.
- **AC-10. Multiple `git` invocations in one command string are all visited.**
  The trigger's global boundary scan continues past a non-matching verb (e.g.
  `git status; git commit -m x`) rather than stopping at the first `git`. Check:
  a passing test asserts `buildAnchoredGitVerbTriggerRegex('commit').test('git
  status; git commit -m x') === true`.

## Fail-open / fail-closed posture (owned by the callers, not the module)

- **AC-11. The module is total over string inputs: `.test()` never throws, and
  an unparseable token resolves to a non-match, never a crash.** The module is a
  pure builder with no I/O and no error paths of its own beyond the quote-mask
  delegate; its one "can't parse" outcome (AC-7) is a `false`, not an
  exception. The fail-open vs fail-closed decision is therefore **not** this
  module's to make — each guard consumer owns its own posture, and the module's
  only contract obligation is to hand back a reliable boolean so the consumer's
  posture is actually honoured. Check: `node --check` plus a passing test that
  feeds a batch of adversarial strings (empty, unbalanced quotes, heredoc
  bodies, `git` at end of string) and asserts `.test()` returns a boolean and
  never throws.
- **AC-12. The consumer postures, documented so a future reader does not
  attribute them to this module:** `claude-secret-guard.js` and
  `claude-plan-guard.js` are fail-closed (and additionally load this module via
  a `loadGitCommitTrigger()` dynamic require that fails closed on a broken or
  missing trigger file — see BUG-123 round 9 in `claude-plan-guard.test.js`);
  `claude-version-guard.js` is fail-open by design; `claude-codename-guard.js`
  is fail-closed with a regex-only check. None of that is this module's doing.
  Check: reviewed by eye — the AC exists so the posture is stated once and the
  module is not blamed for a consumer's choice.

## Tests

- **AC-13. There is no dedicated `claude-git-commit-trigger.test.js`; the
  trigger's behaviour is covered through its consumers' suites plus the shared
  scanner suite** (ASM-760). The concrete coverage lives in:
  `claude-secret-guard.test.js` (anchored trigger, the `-c`/`-C` stacked forms,
  the round-2 backtracking false-positive repros, quote-aware values, and the
  round-6 Marrow odd-embedded-quote repro), `claude-plan-guard.test.js` (same
  trigger plus the round-9 broken/missing-dependency fail-closed paths),
  `claude-codename-guard.test.js` (the bare `commit|push` trigger),
  `claude-version-guard.test.js` (the anchored trigger's plain-commit path), and
  `claude-quote-mask.test.js` (the shared mask/`consumeShellToken` corpus every
  consumer inherits). Check: `node --test claude-secret-guard.test.js
  claude-plan-guard.test.js claude-codename-guard.test.js
  claude-version-guard.test.js claude-quote-mask.test.js` exits 0.
- **AC-14. `quote-mask-drift.test.js` still runs as the live tripwire against a
  future re-fork**, asserting there is exactly one `function buildQuoteMask(`
  declaration in the repo and that consumers re-export the shared function by
  reference, not a copy. Check: `node --test quote-mask-drift.test.js` exits 0;
  `grep -rn "function buildQuoteMask(" claude-git-commit-trigger.js` finds zero
  matches (the module requires it, never redefines it).
- **AC-15. The four round-2 backtracking repros are asserted as `false`, not
  merely absent.** A lazy fix that "mostly works" would leave these unasserted;
  the consumer suites assert each one explicitly (see AC-2). Check: the reviewer
  confirms the four `false` assertions exist in the consumer suites, not just
  the matching `true` cases.

## Out of scope (stated, not silently absent)

- Whole-command quote-state tracking. This module deliberately does **not** track
  quote state across the entire command string (the thing that caused BUG-088's
  false-negative, where an unrelated unbalanced quote earlier in the command
  flipped quote-mask parity and hid a real, later `git commit`). It only
  recognises a bounded run of option tokens after an already-boundary-anchored
  `git` token, so it cannot reproduce that failure mode — but neither does it
  claim to close every future trigger-bypass shape (ASM-761).
- The payload scans the guards run *after* the trigger fires. This module
  decides *whether to engage*; the secret/version/plan/codename checks
  themselves are the guards' (and, post-BUG-088, the four checker modules')
  scope, not this module's.
- Any change to the verb set, boundary style, or public `.test()` shape — those
  are caller-owned (AC-8/AC-9), and AC-1's "public shape unchanged" is the
  contract, not a suggestion.

## Assumptions

Logged via `node claude-bow.js add assumption`:

- **ASM-760** — no dedicated test file; the trigger is covered only through the
  consumer suites enumerated in AC-13 plus `claude-quote-mask.test.js`.
- **ASM-761** — the module does not track whole-command quote state; the bounded
  option-run recognition after an anchored `git` token is a stated scope
  boundary, not a gap to be closed here.

## Escalations

- **A dedicated `claude-git-commit-trigger.test.js` would be worth a follow-on**
  (the module's three exports are directly testable and the four repros live
  spread across four suites today), but it is not required for this contract to
  be judged — the indirect coverage in AC-13 is the live proof. Flagged as a
  hygiene improvement, not a correctness gap.

- **ASM-915 (FEAT-084 CC fold).** claude-git-commit-trigger.js header line 5 cites provenance mkey tool.secretguard while the module key is tool.gitcommittrigger per code.json (weakest form of the re-keying theme).
