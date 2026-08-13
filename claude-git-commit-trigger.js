/**
 * Shared git-commit/push trigger builder (BOW: BUG-123, tool.secretguard).
 *
 * claude-secret-guard.js, claude-version-guard.js, claude-plan-guard.js and
 * claude-codename-guard.js each intercept `git commit` (codename-guard also
 * `git push`) at the PreToolUse layer with a boundary-anchored check, so a
 * quoted MENTION of "git commit" in prose never triggers the scan but a real
 * invocation always does (see each guard's own header for that history —
 * SEC-008, BUG-088). All four guards shared the SAME trigger, and all four
 * shared the SAME bug (BUG-123 round 1): the trigger only tolerated a single
 * `-C <dir>` global option between `git` and `commit`, so the extremely
 * common `git -c user.email=... commit` idiom (or `-c commit.gpgsign=false`,
 * or `--git-dir=...`, or several `-c`s stacked) didn't match at all — the
 * three fail-closed guards (secret/version/plan... version-guard is actually
 * fail-open by design, see its own header) exited before ever calling their
 * real scan, and the fail-closed codename guard's regex-only check missed it
 * outright.
 *
 * ROUND 1 shipped a fix built from a real (if narrow) grammar for git's
 * global options — `-c <key>=<value>`, `-C <dir>`, `--git-dir=`/
 * `--work-tree=`/`--namespace=`, plus a catch-all for other options —
 * repeated zero-or-more times, expressed as ONE regex: `git`, then
 * `(?:OPTION\s+)*`, then the verb, all inside a single alternation-heavy
 * pattern.
 *
 * ROUND 2 FIX (BUG-123, Tester B finding 1 — backtracking false positive).
 * Tester B proved that single-mega-regex shape is itself the bug class, not
 * just under-specified: JS regex alternation tries left-to-right and, on
 * overall match failure further down the pattern, BACKTRACKS into whichever
 * earlier alternative can still produce a DIFFERENT match — it does not
 * commit to "the specific `-c`/`-C` rule already matched its value, so never
 * reconsider". Concretely, for `git -c commit.gpgsign=false status`: the
 * specific `-c\s+\S+` rule consumes `-c commit.gpgsign=false` in one shot (no
 * internal whitespace to re-split on), the verb check then fails against
 * `status`, and the engine backtracks — not into a shorter `\S+` (nowhere to
 * split without whitespace) but into the CATCH-ALL alternative for the same
 * `-c` token, which happily matches bare `-c` alone (no value requirement).
 * That leaves `commit.gpgsign=false status` sitting unconsumed exactly where
 * the verb check runs next, and a `\bcommit\b`-style boundary treats the `.`
 * in `commit.gpgsign` as a non-word character — so the verb alternation
 * matches `commit` as a *prefix of the option's own value*, not a real verb.
 * Same root cause produced `-C commit-repo status` (a directory literally
 * named `commit-repo`) firing true, and `-c push.default=commit-should-not-
 * match log` firing the codename guard's bare `commit|push` alternation. A
 * narrower patch (excluding the specific rules' letters/names from the
 * catch-alls via negative lookahead) was drafted and would have closed these
 * four repros, but Bill's ruling on the feed is the standing decision for
 * this bug FAMILY: a single alternation regex, however carefully excluded,
 * keeps producing either this false positive or the next bypass shape,
 * because "backtrack into a different alternative when the tail fails" is a
 * property of the regex engine, not something a more careful alternative
 * list can fully close off for every future git option grammar quirk. The
 * durable fix is to stop asking one regex to both consume a variable-length,
 * option-grammar-shaped prefix AND assert a fixed verb at a computed offset
 * in a single backtracking search.
 *
 * FIX: TOKENIZE instead of pattern-match, reusing the established in-repo
 * shape (claude-author-guard.js's `parseGitInvocation()` — the same
 * `-C <dir>` / `-c <key>=<value>` option-loop grammar, hardened over several
 * BUG-044/047 rounds — and claude-bow.js's `tokenizeCommandSafely()` for the
 * "walk the string procedurally, no big alternation with a forced downstream
 * constraint" discipline). `findGitVerbToken()` below advances a cursor past
 * the `git` token ONE OPTION AT A TIME: each iteration runs a small,
 * self-contained regex anchored with `^` against `text.slice(i)`, and on a
 * match unconditionally advances `i` and loops — there is no outer construct
 * requiring the whole option run PLUS a trailing verb to match in one
 * attempt, so there is nothing overall for the engine to backtrack into.
 * Once no further option matches, whatever immediately follows (after
 * required whitespace) is extracted as a single verb WORD via its own
 * regex, and is then compared to the caller's verb set by EXACT SET
 * MEMBERSHIP (`Set.has`), never by substring/alternation match against the
 * remaining text. That equality check is what makes `commit.gpgsign=false`,
 * `commit-repo`, and `commit-should-not-match` all correctly non-matches:
 * none of them, taken as a whole token, equals the string `"commit"`.
 *
 * This module still does NOT track quote state across the whole command
 * string (the thing that caused BUG-088's false-negative, where an unrelated
 * unbalanced quote earlier in the command flipped quote-mask parity and hid
 * a real, later `git commit`) — it only recognises a bounded run of option
 * tokens immediately after an already-boundary-anchored `git` token, so it
 * cannot reproduce that failure mode. `-c`/`-C` values may themselves be
 * quoted (`-c "user.email=x"`) without needing any outer quote-state
 * tracking, because the value's own quotes are consumed locally within one
 * option's own regex.
 *
 * Kept as ONE shared module (GR#3) specifically because BUG-123 is what
 * happens when the same trigger logic is hand-copied into four files and
 * only one copy ever gets fixed in a given security pass. Each guard still
 * owns its own boundary style — the three fail-open/fail-closed commit
 * guards anchor to a shell-command boundary (start of string or after
 * `; & | ( \n`) as before; the codename guard, whose original trigger was a
 * bare `\b` word boundary with no shell-boundary anchoring at all, keeps
 * that shape too.
 *
 * PUBLIC SHAPE UNCHANGED ON PURPOSE: `buildAnchoredGitVerbTriggerRegex()` /
 * `buildBareGitVerbTriggerRegex()` still take a `'commit'` / `'commit|push'`
 * style verb-alternation STRING and still return an object usable exactly
 * like a RegExp via `.test(command)` — that is the entire interface the four
 * call sites (and their existing test suites, which call `GIT_COMMIT_RE.test
 * (...)` directly) already depend on. Internally the returned object is no
 * longer a real RegExp; it is a small tokenizer wrapped behind the same
 * `.test()` method, so this fix required zero changes to any call site.
 */

'use strict';

// ROUND 6 FIX (BUG-123, attacker "Marrow" finding — odd escaped-quote-count
// mispairing false negative, plus Bill's standing ruling on this whole bug
// family). Rounds 3, 4 and 5 each replaced the previous round's quote-value
// grammar with a HAND-ROLLED scanner that MODELED the project's already-
// hardened, escape-aware scanner (claude-author-guard.js's buildQuoteMask(),
// BUG-077/BUG-078-hardened) instead of reusing it — and each hand-rolled
// copy shipped exactly the gap its model had already closed. Round 4's own
// `consumeShellToken()` paired quote characters POSITIONALLY
// (`text.indexOf(ch, i + 1)`), with no concept of a preceding backslash
// escaping a quote character. That happens to parse correctly for a value
// containing zero or an EVEN count of embedded escaped quotes (the naive
// pairing and the escape-aware pairing agree by coincidence — round 4's own
// tests only ever exercised 0 or 2), but mispairs on an ODD count: for
// `git -c key="a\"b" commit -m x`, the naive scan sees the escaped `\"` as
// an ordinary close-quote, ends the "quoted region" one character early,
// then hits the value's REAL closing `"` as if it were a fresh unmatched
// open-quote, runs off the end of the string looking for its partner, finds
// none, and returns -1 — the option "fails to parse", the whole `git`
// occurrence is abandoned, and a real `git commit` lands completely
// unscanned (the guard's original fail-closed-bypass shape, BUG-123's own
// motivating impact, reproduced by a THIRD independent route).
//
// FIX (Bill's round-6 ruling, superseding rounds 2-5's "model it, don't
// share it" posture): this file no longer contains ANY quote-scanning logic
// of its own. `consumeShellToken()` below delegates entirely to the shared,
// canonical `buildQuoteMask()` (now extracted to claude-quote-mask.js and
// required by every consumer in the repo — see that module's header) to
// decide which positions are "inside a quote" (escape-aware: a backslash
// escapes the next character only inside double quotes, exactly like a real
// shell, and never opens a phantom quoted region when it appears outside
// one). Given the mask, finding a token's end is a walk forward from `start`
// to the first position that is BOTH unquoted (mask false) AND whitespace —
// whitespace INSIDE an open quote is masked true and never stops the walk,
// so the walk can only stop at a genuinely unquoted boundary, regardless of
// how many escaped quotes (odd or even) appear inside the value. Reaching
// the end of `text` while still inside an open quote (the mask's own
// established swallow-to-EOF fail-safe for an unterminated quote) is
// detected the same way and still returns -1, preserving this module's
// existing fail-closed posture: an option value that cannot be parsed
// cleanly stops the option loop rather than guessing, so whatever text is
// actually there gets read as the verb word next (or fails the verb-word
// regex and the whole `git` occurrence is correctly treated as a
// non-match) — same caller contract as every prior round, zero call-site
// changes required.
// BUG-044 round 2: consumeShellToken() itself moved to claude-quote-mask.js
// so claude-author-guard.js's `-c`/`-C` option-value scanning (which needed
// the exact same "walk to first unquoted whitespace" logic) could share it
// instead of hand-rolling a FOURTH copy — see that module's own doc comment
// for the full rationale. Re-exported here under the same local name so
// every call site below is unchanged.
const { buildQuoteMask, consumeShellToken } = require('./claude-quote-mask.js');

/**
 * Matches `-c` or `-C` (given as `flag`, a single letter) followed by
 * required whitespace and a quote-aware value token, ANCHORED at `i` in
 * `text`. Returns the absolute index one past the whole option (flag +
 * whitespace + value), or -1 if `-<flag>` isn't present at `i`, isn't
 * followed by whitespace, or its value token has an unbalanced quote / is
 * empty. Kept separate from the pure-regex options below because `-c`/`-C`
 * are the only two options whose value can legitimately contain an embedded
 * quote that starts anywhere within the token — see the ROUND 4 header note.
 */
function matchQuoteAwareOption(text, i, flag, quoteMask) {
  const head = new RegExp('^\\s+-' + flag + '(\\s+)').exec(text.slice(i));
  if (!head) return -1;
  const valueStart = i + head[0].length;
  const valueEnd = consumeShellToken(text, valueStart, quoteMask);
  return valueEnd === -1 ? -1 : valueEnd;
}

// The remaining git global options, matched ANCHORED to the current cursor
// position (`^\s+...`) and consumed unconditionally on match — see header
// for why this per-option, no-forced-backtrack shape is what closes the bug
// class. `--git-dir=`/`--work-tree=`/`--namespace=` (either `=value` or
// ` value` form), then a conservative catch-all for any other long
// (`--word` / `--word=value`) or short (`-x`) global option git accepts
// (e.g. `--no-pager`, `--bare`, `-p`). `-c`/`-C` are handled separately by
// `matchQuoteAwareOption()` above, NOT here — see ROUND 4 header note.
const GIT_GLOBAL_OPTION_RE = new RegExp(
  '^\\s+(?:' +
    '--git-dir(?:=\\S+|\\s+\\S+)' +
    '|--work-tree(?:=\\S+|\\s+\\S+)' +
    '|--namespace(?:=\\S+|\\s+\\S+)' +
    '|--[A-Za-z][A-Za-z-]*(?:=\\S+)?' +
    '|-[A-Za-z]' +
  ')'
);

// The verb word itself, once no more options match: required whitespace
// then a bare identifier (letters/hyphens — e.g. `commit`, `push`,
// `status`, `commit-repo`... note this deliberately captures the WHOLE
// token, hyphens included, so `commit-repo` is extracted as the single word
// `"commit-repo"`, never truncated to a `commit` prefix — that whole-token
// capture is what makes the subsequent equality check exact.)
const GIT_VERB_WORD_RE = /^\s+([A-Za-z][A-Za-z-]*)/;

/**
 * Starting right after a matched `git` token (`pos` = index of the first
 * character after `git`), walks forward consuming global options one at a
 * time and returns the verb word that follows them, or `null` if the
 * invocation has no options-run-then-word shape at all (e.g. `git` at the
 * very end of the string). Each option match is independent — there is no
 * combined pattern requiring the whole run plus the verb to match together,
 * so nothing here can backtrack INTO a different option interpretation
 * because a later check failed; the verb, once extracted, is a plain string
 * for the caller to compare by equality. `quoteMask` (optional) is a
 * precomputed `buildQuoteMask(text)` result — callers scanning the same
 * `text` for multiple `git` occurrences (see `makeGitVerbTrigger` below)
 * pass it in once rather than paying to rebuild it per occurrence; it is
 * built lazily by `consumeShellToken()` itself if omitted.
 */
function findGitVerbToken(text, pos, quoteMask) {
  const mask = quoteMask || buildQuoteMask(text);
  let i = pos;
  for (;;) {
    // -c/-C are tried FIRST, ahead of the generic catch-all below, because
    // their value grammar needs quote-aware scanning (ROUND 4/6) that a plain
    // regex can't express — if the generic `-[A-Za-z]` catch-all ran first
    // it would happily match a bare `-c`/`-C` with no value at all and leave
    // the real value sitting unconsumed right where the verb check runs
    // next, reintroducing exactly the ROUND 2 backtracking failure mode via
    // ordering instead of regex backtracking.
    const cEnd = matchQuoteAwareOption(text, i, 'c', mask);
    if (cEnd !== -1) { i = cEnd; continue; }
    const capCEnd = matchQuoteAwareOption(text, i, 'C', mask);
    if (capCEnd !== -1) { i = capCEnd; continue; }
    const m = GIT_GLOBAL_OPTION_RE.exec(text.slice(i));
    if (!m) break;
    i += m[0].length;
  }
  const verbMatch = GIT_VERB_WORD_RE.exec(text.slice(i));
  return verbMatch ? verbMatch[1] : null;
}

/** Splits a `'commit'` / `'commit|push'` style alternation string into a
 * Set of exact verb strings for `Set.has()` membership checks — the
 * equality check that replaces the old substring/alternation regex match. */
function buildVerbSet(verbAlternation) {
  return new Set(String(verbAlternation).split('|').filter(Boolean));
}

/**
 * Builds a git-token-boundary regex (global, so every occurrence in the
 * command string is visited) and wraps it with `findGitVerbToken()` +
 * exact-Set-membership verb comparison into a `.test(command)`-shaped
 * object, matching the previous RegExp-based public shape without being a
 * real RegExp.
 */
function makeGitVerbTrigger(boundarySource, verbAlternation) {
  const verbSet = buildVerbSet(verbAlternation);
  return {
    test(command) {
      const text = String(command == null ? '' : command);
      // Built ONCE per command and threaded through every occurrence visited
      // below, rather than rebuilt per `git` token — buildQuoteMask() is a
      // pure function of `text` alone, so its result never changes across
      // occurrences in the same command string.
      const quoteMask = buildQuoteMask(text);
      const boundaryRe = new RegExp(boundarySource, 'g');
      let m;
      while ((m = boundaryRe.exec(text)) !== null) {
        const gitEnd = m.index + m[0].length;
        const verb = findGitVerbToken(text, gitEnd, quoteMask);
        if (verb !== null && verbSet.has(verb)) return true;
        // `git` token matched but not followed by a recognised commit/push
        // verb (e.g. `git status`, or options that don't resolve to one of
        // verbAlternation's words) — keep scanning for a LATER `git ...`
        // invocation in the same command string (e.g. `git status; git
        // commit -m x`), same as the previous single-regex version did via
        // its own global-equivalent boundary scanning.
        if (boundaryRe.lastIndex === m.index) boundaryRe.lastIndex++; // zero-length guard
      }
      return false;
    },
  };
}

/**
 * Builds the shell-boundary-anchored trigger used by
 * claude-secret-guard.js / claude-version-guard.js / claude-plan-guard.js:
 * `git`, then any run of recognised global options consumed one at a time,
 * then a verb word compared by exact equality against `verbAlternation`
 * (e.g. `'commit'`). Matches at the start of the command or immediately
 * after a shell separator (`; & | ( \n`) — a quoted mention inside prose
 * does not match. Returned object exposes `.test(command)` like a RegExp.
 */
function buildAnchoredGitVerbTriggerRegex(verbAlternation) {
  return makeGitVerbTrigger('(?:^|[;&|(\\n])\\s*git(?=\\s)', verbAlternation);
}

/**
 * Builds the bare word-boundary trigger used by claude-codename-guard.js:
 * `git`, then any run of global options consumed one at a time, then a verb
 * word compared by exact equality against `verbAlternation` (e.g.
 * `'commit|push'`). No shell-boundary anchoring — matches this guard's
 * original, intentionally looser shape (see its own header: it accepts the
 * small false-positive cost this implies in exchange for never missing a
 * real invocation). Returned object exposes `.test(command)` like a RegExp.
 */
function buildBareGitVerbTriggerRegex(verbAlternation) {
  return makeGitVerbTrigger('\\bgit(?=\\s)', verbAlternation);
}

module.exports = {
  findGitVerbToken,
  buildAnchoredGitVerbTriggerRegex,
  buildBareGitVerbTriggerRegex,
};
