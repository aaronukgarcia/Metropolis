// Module key: tool.quotemask (see code.json; GUID 45165319-674a-4415-adc8-f77bab928245)
// Spec ref: GR#22 (guards); shell-quoting

/**
 * claude-quote-mask.js — the single, canonical, escape-aware shell
 * quote/heredoc scanner (BUG-123 round 6 extraction).
 *
 * BACKGROUND. `buildQuoteMask()` started life in claude-author-guard.js
 * (BUG-043's fix, hardened by BUG-077 [backslash-escape-outside-quotes must
 * not open a phantom region] and BUG-078 [heredoc bodies are opaque to a
 * real shell]) and was then hand-copied — deliberately, at the time — into
 * claude-pre-commit-check.js. BUG-076/ASM-356 stood up quote-mask-drift.test.js
 * to watch those two copies for behavioural divergence, on the standing
 * argument that a PreToolUse guard must still emit a decision even if a
 * shared dependency is broken, and a `require()` of a sibling guard's own
 * module was seen to fail OPEN (not closed) when that module was
 * missing/broken.
 *
 * BUG-123 rounds 3, 4 and 5 then proved the OPPOSITE failure mode of that
 * same "keep copies" argument: `claude-git-commit-trigger.js`'s own
 * `consumeShellToken()` was a THIRD, hand-rolled, positionally-paired quote
 * scanner (`text.indexOf(ch, i + 1)`, no escape awareness at all) that
 * MODELED the hardened buildQuoteMask() behaviour instead of reusing it —
 * and, exactly as Bill's round-6 ruling predicted, it shipped the gap its
 * parent had already closed: an ODD count of backslash-escaped quotes inside
 * a `-c key="a\"b"` style value mispaired the scan, produced an unterminated
 * run, returned -1, and fell through to a silent unscanned commit (Marrow's
 * round-5 Destructive finding). "Modeled on the hardened scanner" is how
 * this project ended up with three divergent implementations of the same
 * escape rules; GR#3 (Single Source of Truth) says there must be exactly
 * ONE, and this file is it.
 *
 * THIS MODULE IS NOW THE ONLY PLACE `function buildQuoteMask(` MAY APPEAR IN
 * THE REPO (grep-verifiable — quote-mask-drift.test.js's discoverCopies()
 * enforces this as a live, informational check). Every consumer —
 * claude-author-guard.js, claude-pre-commit-check.js,
 * claude-git-commit-trigger.js, and (transitively, via claude-author-guard.js)
 * claude-destructive-guard.js — requires THIS file rather than keeping or
 * hand-rolling its own copy.
 *
 * API. `buildQuoteMask(text)` returns a same-length boolean array: mask[i]
 * is true when character i sits inside an open single- or double-quoted
 * region (including the quote characters themselves) or inside an inert
 * heredoc body; false otherwise. Backslash escapes the next character only
 * inside double quotes (real shell semantics — single-quoted text has no
 * escape character at all); outside any quoted region a backslash still
 * consumes the next character literally without opening a quoted region,
 * even if that character is itself a quote (BUG-077).
 *
 * A caller wanting "where does THIS token's value end" (as
 * claude-git-commit-trigger.js's option-value scanner does) walks the mask
 * from the token's start looking for the first position where mask[i] is
 * false AND text[i] is whitespace — by construction, whitespace INSIDE an
 * open quote is masked true and does not stop the walk, so the walk only
 * ever stops at a genuinely unquoted boundary. Reaching the end of `text`
 * while the last character's mask entry is still true means an unterminated
 * quote was swallowed to EOF (this module's existing, established fail-safe
 * for that case — see findHeredocBodyEnd's own ASM-351 note) and the caller
 * should treat that as "could not parse this token", not guess.
 *
 * KNOWN LIMITATION (ASM-344, carried forward honestly, not narrowed): this is
 * a toggle-based state machine, not a real shell lexer. A deliberately
 * unbalanced quote earlier in the command string can still flip quote-state
 * parity and mask a real invocation as if it were quoted prose (or the
 * reverse) for everything after it.
 *
 * Regression coverage for this exact file lives in claude-quote-mask.test.js
 * (the escape-aware corpus, including Marrow's round-5 odd-embedded-quote
 * repro and its 1/3/5-embedded-quote generalisations) — every consumer of
 * this module inherits that coverage automatically because there is nothing
 * left for a consumer to reimplement. The cross-file drift control,
 * quote-mask-drift.test.js, still runs (now over a single discovered copy)
 * as the live tripwire against a future accidental re-fork.
 */

'use strict';

function buildQuoteMask(text) {
  const mask = new Array(text.length).fill(false);
  // `quote` is one of: null (unquoted), '"' (double-quoted), "'" (POSIX
  // single-quoted — no escape character at all), or 'ansic' (BUG-080:
  // ANSI-C-quoted, `$'...'` — escape-aware like double quotes, but closed by
  // a bare `'`, not `$'`).
  let quote = null;
  let i = 0;
  while (i < text.length) {
    const c = text[i];
    if (quote) {
      mask[i] = true;
      if ((quote === '"' || quote === 'ansic') && c === '\\' && i + 1 < text.length) {
        mask[i + 1] = true;
        i += 2;
        continue;
      }
      // BUG-332 r14 (r13 attacker F2): inside a double-quoted region, a
      // `$(…)` command substitution (and a backtick substitution) is its OWN
      // quote context — a real shell parses the quotes INSIDE it separately,
      // so an inner `"` (or `'`) can never close the outer double-quote.
      // Without this, `bash -c "cat <<< '$(printf "%s\n" "…")' | bash"`
      // stopped the outer region at the first inner `"`, hiding the payload.
      // The whole balanced `$()` span is masked opaque (depth-tracked, so
      // nested `$(…)` and `$((…))` arithmetic work); an unterminated span
      // swallows to EOF (the established fail-safe).
      if (quote === '"' && c === '$' && text[i + 1] === '(') {
        let depth = 1;
        let p = i + 2;
        while (p < text.length && depth > 0) {
          const d = text[p];
          if (d === '(') depth++;
          else if (d === ')') depth--;
          mask[p] = true;
          p++;
        }
        i = p;
        continue;
      }
      if (quote === '"' && c === '`') {
        let p = i + 1;
        while (p < text.length && text[p] !== '`') { mask[p] = true; p++; }
        if (p < text.length) { mask[p] = true; p++; } // the closing backtick
        i = p;
        continue;
      }
      const closeChar = quote === 'ansic' ? "'" : quote;
      if (c === closeChar) quote = null;
      i++;
      continue;
    }
    // BUG-077: outside any quoted region, a real shell still treats `\X` as
    // "the literal character X, escape consumed" — it does NOT open a
    // quoted region even when X is a quote character. Without this, a stray
    // `\"`/`\'` in plain unquoted text would be read as a bare quote,
    // opening a phantom quoted region that could swallow a real
    // `git commit ...` after it as prose.
    if (c === '\\' && i + 1 < text.length) {
      i += 2;
      continue;
    }
    // BUG-080: `$'...'` is bash's ANSI-C quoting, a DISTINCT form from a bare
    // `'...'` POSIX single-quote — inside it, unlike a real single-quote,
    // backslash DOES escape the following character (same rule as double
    // quotes), so `\'` inside `$'...'` is a literal escaped quote, not the
    // terminator. Treating `$'` as opening a plain `'` region (the prior
    // behaviour) closed the mask's "quote" one character early at that
    // escaped `\'`, then the real closing `'` of the `$'...'` literal opened
    // a NEW, now-unbalanced region that swallowed everything after it —
    // including a real `git commit ...` — as inert "inside quotes" prose.
    // Recognising `$'` as its own trigger, distinct from a bare `'`, is what
    // lets the escape-aware branch above apply to it correctly.
    if (c === '$' && text[i + 1] === "'") {
      quote = 'ansic';
      mask[i] = true;
      mask[i + 1] = true;
      i += 2;
      continue;
    }
    if (c === '"' || c === "'") {
      quote = c;
      mask[i] = true;
      i++;
      continue;
    }
    // BUG-078: a heredoc body is opaque to a real shell — no quote parsing
    // happens inside it, however many stray/unbalanced quote characters it
    // contains. Mask the whole body (header through terminator line) as
    // inert without touching `quote`, then resume with quote state exactly
    // as it was before the heredoc started.
    if (c === '<' && text[i + 1] === '<') {
      const header = matchHeredocHeader(text, i);
      if (header) {
        const end = findHeredocBodyEnd(text, header.afterHeader, header.word, header.stripLeadingTabs);
        for (let j = i; j < end; j++) mask[j] = true;
        i = end;
        continue;
      }
    }
    i++;
  }
  return mask;
}

/** Recognises a heredoc header starting at `text[i]` (`<<`, optional `-` for
 * the tab-stripping form, optional whitespace, then the delimiter word —
 * bare, single-quoted, or double-quoted; the quoting only affects expansion
 * inside the body in a real shell, not the terminator match, so it is parsed
 * but does not change how the terminator is found). Returns
 * { afterHeader, word, stripLeadingTabs } or null if `text[i]` is not a
 * heredoc header. */
function matchHeredocHeader(text, i) {
  const re = /^<<(-)?[ \t]*(?:"([^"\n]*)"|'([^'\n]*)'|([A-Za-z_][A-Za-z0-9_]*))/;
  const m = re.exec(text.slice(i));
  if (!m) return null;
  const word = m[2] !== undefined ? m[2] : m[3] !== undefined ? m[3] : m[4];
  if (!word) return null;
  return { afterHeader: i + m[0].length, word, stripLeadingTabs: !!m[1] };
}

/** Finds the index just past the heredoc terminator line: scans line by line
 * from `pos` (which is still on the header line) for a line whose content
 * (with leading tabs stripped first, if `stripLeadingTabs` — the `<<-` form)
 * exactly equals `word`. An UNTERMINATED heredoc (no such line before the end
 * of `text`) swallows to the end of the string — a deliberate fail-safe
 * (ASM-351): nothing past it gets a false ALLOW because nothing past it gets
 * scanned as a candidate invocation at all; whatever follows falls through
 * with the same `quote` state it had before the heredoc, so the OUTER
 * command's own parsing is unaffected.
 *
 * NOTE (BUG-081, open): this does not yet normalise \r before the
 * terminator-line equality check, so a CRLF-terminated heredoc's terminator
 * line (`EOF\r`) does not exactly equal `word` (`EOF`) and the heredoc is
 * treated as unterminated (swallowed to EOF). Tracked separately; not this
 * extraction's job to fix — see quote-mask-drift.test.js's CRLF case, which
 * deliberately asserts only cross-copy agreement, not a golden "correct"
 * answer, for exactly this reason. */
function findHeredocBodyEnd(text, pos, word, stripLeadingTabs) {
  const firstNewline = text.indexOf('\n', pos);
  if (firstNewline === -1) return text.length; // header with no body at all
  let idx = firstNewline + 1;
  while (idx <= text.length) {
    const nextNewline = text.indexOf('\n', idx);
    const line = nextNewline === -1 ? text.slice(idx) : text.slice(idx, nextNewline);
    const compare = stripLeadingTabs ? line.replace(/^\t+/, '') : line;
    if (compare === word) {
      return nextNewline === -1 ? text.length : nextNewline + 1;
    }
    if (nextNewline === -1) return text.length; // unterminated — swallow to EOF
    idx = nextNewline + 1;
  }
  return text.length;
}

/**
 * heredocBodyRange(text, header) — the span of a heredoc's BODY ONLY (the
 * first character after the header line's newline through the start of the
 * terminator line), or null when there is no body (a header line with no
 * following content, or a body that cannot be located). BUG-332 r9 needs the
 * body as standalone command text: `sudo bash <<'EOF'` makes the body COMMANDS
 * the shell executes, and extracting it as its own scan text is what lets the
 * git-word detectors see the `git add`/`git commit` inside a body that
 * buildQuoteMask masks opaque (BUG-078). `header` is the object returned by
 * matchHeredocHeader(); the terminator is located with findHeredocBodyEnd(), so
 * an unterminated heredoc's "body" extends to the end of `text` — the same
 * fail-safe, and harmless: the guard never treats trailing outer text as a
 * body, it only adds a scan text that over-reaches into text already scanned
 * by the caller.
 */
function heredocBodyRange(text, header) {
  const firstNewline = text.indexOf('\n', header.afterHeader);
  if (firstNewline === -1) return null; // header line with no body
  const start = firstNewline + 1;
  const bodyEnd = findHeredocBodyEnd(text, header.afterHeader, header.word, header.stripLeadingTabs);
  // bodyEnd is just past the terminator LINE; the body ends where that line
  // starts (the newline before it, plus one). An unterminated heredoc has no
  // terminator line, so the body runs to the end of text.
  const terminatorStart = text.lastIndexOf('\n', bodyEnd - 2) + 1;
  if (terminatorStart < start) return null;
  return { start, end: terminatorStart };
}

/**
 * consumeShellToken(text, start, quoteMask) — walks forward from `start`
 * looking for the first position that is BOTH outside any quoted/heredoc
 * region (per `quoteMask`, built lazily from `text` if omitted) AND
 * whitespace. Because whitespace inside an open quote is masked `true`, the
 * walk can only stop at a genuinely unquoted boundary, regardless of how
 * many (escaped or not) quote characters the token contains — this is what
 * lets a caller extract a single shell "word" like `user.email="fake
 * attacker <fake@evil.com>"` as one token instead of truncating at the
 * first embedded space (BUG-044 round 2's exact repro).
 *
 * Extracted here (BUG-044 round 2) alongside `dequoteShellToken()` below so
 * that `claude-git-commit-trigger.js` (BUG-123 round 6, the original home of
 * this exact walk) and `claude-author-guard.js` (BUG-044's `-c`/`-C`
 * option-value scanning) share ONE implementation rather than each keeping
 * its own hand-rolled copy — precisely the copy-drift failure mode this
 * module's header describes BUG-123 rounds 3-5 falling into.
 *
 * Returns the index one past the token, or -1 if the token is empty (start
 * position is itself unquoted whitespace / end of text) or unterminated
 * (reaches end of `text` while still inside an open quote — buildQuoteMask's
 * own established swallow-to-EOF fail-safe). A caller receiving -1 should
 * treat the option/token as unparseable and stop, not guess.
 */
function consumeShellToken(text, start, quoteMask) {
  const mask = quoteMask || buildQuoteMask(text);
  let i = start;
  while (i < text.length) {
    if (!mask[i] && /\s/.test(text[i])) break;
    i++;
  }
  if (i === start) return -1; // empty value — refuse
  if (i === text.length && mask[text.length - 1]) return -1; // unterminated quote
  return i;
}

/**
 * dequoteShellToken(token) — strips a shell token's own quote characters and
 * resolves its escapes, following the SAME toggle/escape rules as
 * `buildQuoteMask()` (single/double quote toggling; backslash escapes the
 * next character only inside double quotes; outside any quote a backslash
 * still consumes the next character literally without opening a quoted
 * region). Given a token like `user.email="fake attacker <fake@evil.com>"`
 * (as returned by slicing `text` between a `consumeShellToken()` start/end
 * pair), returns the shell's own view of that token's value:
 * `user.email=fake attacker <fake@evil.com>`. Operates on an already-sliced
 * token, so it does not need (and does not do) heredoc recognition — a
 * heredoc header can never appear mid-token, only at a bare word boundary,
 * which `buildQuoteMask()` over the FULL text already accounts for when
 * `consumeShellToken()` finds the token's boundaries in the first place.
 */
function dequoteShellToken(token) {
  let out = '';
  let quote = null; // null | '"' | "'" | 'ansic' (BUG-080, see buildQuoteMask)
  let i = 0;
  while (i < token.length) {
    const c = token[i];
    if (quote) {
      if (quote === '"') {
        // BUG-332 r14 (r13 attacker F2): bash-accurate double-quote escapes —
        // ONLY `\"` `\\` `\$` `` \` `` collapse; `\n`, `\t`, `\x41`, `\.` etc.
        // stay as the literal two characters. The prior `\X`→`X` collapse
        // mangled a `%s\n` printf format into `%sn`, which the constant-printf
        // emitter (evalConstantPrintf) rejects — hiding the payload. And a
        // `$(…)` / backtick substitution inside double quotes is its OWN quote
        // context: its inner `"`/`'` never toggle the outer quote, and the
        // span's content is appended RAW (a nested shell parses it), exactly
        // as buildQuoteMask now masks it.
        if (c === '\\' && i + 1 < token.length &&
            (token[i + 1] === '"' || token[i + 1] === '\\' ||
             token[i + 1] === '$' || token[i + 1] === '`')) {
          out += token[i + 1];
          i += 2;
          continue;
        }
        if (c === '$' && token[i + 1] === '(') {
          let depth = 1;
          let p = i + 2;
          while (p < token.length && depth > 0) {
            const d = token[p];
            if (d === '(') depth++;
            else if (d === ')') depth--;
            out += d;
            p++;
          }
          i = p;
          continue;
        }
        if (c === '`') {
          let p = i + 1;
          while (p < token.length && token[p] !== '`') { out += token[p]; p++; }
          if (p < token.length) p++; // closing backtick consumed, not output
          i = p;
          continue;
        }
      } else if (quote === 'ansic') {
        if (c === '\\' && i + 1 < token.length) {
          out += token[i + 1];
          i += 2;
          continue;
        }
      }
      const closeChar = quote === 'ansic' ? "'" : quote;
      if (c === closeChar) {
        quote = null;
        i++;
        continue;
      }
      out += c;
      i++;
      continue;
    }
    if (c === '\\' && i + 1 < token.length) {
      out += token[i + 1];
      i += 2;
      continue;
    }
    if (c === '$' && token[i + 1] === "'") {
      quote = 'ansic';
      i += 2;
      continue;
    }
    if (c === '"' || c === "'") {
      quote = c;
      i++;
      continue;
    }
    out += c;
    i++;
  }
  return out;
}

module.exports = {
  buildQuoteMask,
  matchHeredocHeader,
  findHeredocBodyEnd,
  heredocBodyRange,
  consumeShellToken,
  dequoteShellToken,
};
