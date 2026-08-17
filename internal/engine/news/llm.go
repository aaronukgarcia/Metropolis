package news

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file implements the optional LLM soft-layer seam (§29, "post-v1" per
// V.1's scope table): prose rewriting that is fact-locked, so "no
// hallucinated news" is an enforced property of the pipeline rather than a
// hope about model behaviour (AC-6/AC-7).
//
// The rewrite touches only a story's prose (Text). The story's Name, Tick,
// Month, EventID and EntityID are structural facts carried through
// unchanged — the soft-layer is never given a channel to alter them. The
// only facts the soft-layer COULD alter are therefore the prose-visible
// ones. FactLock parses three fact classes from the original and requires
// each to survive verbatim in the rewrite (see doc.go "What a fact is"):
//
//  1. the ordered sequence of signed numeric facts across every Unicode
//     numeric character (decimal digits and homoglyphs);
//  2. the named entity as a whole-token phrase bounded on both sides, with
//     any embedded number surviving too;
//  3. the data-driven fact-word set (event-type words, month names, and the
//     like) loaded from news-facts.json (SEC-148).
//
// FactLock is conservative: a rewrite it cannot prove safe is rejected and
// the engine prose retained (a false rejection is harmless — AC-7).

// numberPattern is the shared numeric-fact pattern used by numberRe (the
// prose number sequence) and nameTokenRe (a name's embedded numbers): an
// optional run of adjacent non-alphanumeric prefix symbols followed by a
// maximal run of Unicode numeric characters. The number part is the WHOLE
// Unicode numeric class \p{N} — decimal digits \p{Nd} (any script, SEC-145),
// letter numbers \p{Nl} (e.g. Roman numerals Ⅻ), and "other number"
// homoglyphs \p{No} (superscripts ² ⁵⁰⁰, subscripts ₅₀₀, circled ①, vulgar
// fractions ½) — so an injected numeric homoglyph cannot hide from the lock
// by being a non-decimal digit (SEC-205).
//
// The sign part is FAIL-CLOSED, not an enumeration (SEC-214): it is the whole
// non-(real-letter/number/whitespace) class — any character that is not a
// REAL letter (\p{Lu}/\p{Ll}/\p{Lt}/\p{Lo}), not a number (\p{N}), and not
// whitespace (\s and the Unicode separators \p{Z}) — so EVERY sign, math,
// modifier, currency and punctuation symbol that can prefix a number is
// captured as part of the numeric fact. The earlier enumerations (SEC-205's
// \p{Pd} plus the SEC-214 homoglyph list) leaked a new sibling sign homoglyph
// every round — modifier plus ˖ U+02D6 (the plus-mirror of the listed ˗
// U+02D7), commercial minus ⁒ U+2052, heavy plus ➕ U+2795 and heavy minus ➖
// U+2796 all collapsed to their bare digit. Deriving the class instead of
// listing it leaves no whitelist to keep extending: ANY sign decoration on a
// number that is absent (or different) in the original is a different token,
// so the rewrite is rejected. An unsigned number in both original and rewrite
// still tokenizes to its bare digits and matches, and a sign identical in
// both still matches (SEC-205). Excluding whitespace is what makes the prefix
// "adjacent": a sign separated from its number ("- 2") is not a signed
// number, and a whitespace gap must not merge two neighbouring numbers into
// one token.
//
// The class deliberately excludes ONLY real letters — \p{Lu} (uppercase),
// \p{Ll} (lowercase), \p{Lt} (titlecase) and \p{Lo} (other) — not the whole
// letter category. That distinction is SEC-220: the modifier-letter category
// \p{Lm} contains sign-like horizontal bars that, under the old \pL exclusion,
// were treated as letters and collapsed to the bare digit. U+02C9 ˉ (MODIFIER
// LETTER MACRON) and U+02CD ˍ (MODIFIER LETTER LOW MACRON) look like a minus
// and are letters only by Unicode category; excluding \p{Lm} would let a sign
// flip wear one of those bars and pass the no-hallucinated-news gate, so Lm
// is left IN the sign class and captured as sign decoration.
//
// Over-rejection (AC-7 harmless, not a security hole): because the class is
// derived rather than enumerated, it also folds currency symbols and
// punctuation that prefix a number into the numeric fact, so a legitimate
// rewrite that re-expresses formatting false-rejects — "£100"→"100 pounds",
// "(5 injured)"→"5 injured", "2-for-1"→"2 for 1", "-5C"→"minus 5C". This is
// left as-is: tightening the class to drop currency/punctuation would either
// revert to an enumeration (reopening the SEC-205/SEC-214 homoglyph leak) or
// let a currency/punctuation change pass unverified (a currency flip £→$ is
// itself a changed fact). The false rejections fall back to engine prose, so
// they are accepted by design.
const numberPattern = `[^\p{Lu}\p{Ll}\p{Lt}\p{Lo}\p{N}\s\p{Z}]*\p{N}+`

// numberRe matches a maximal (optionally sign-prefixed) run of Unicode
// numeric characters (numberPattern) — the prose-visible numeric facts ("42"
// in "42 deaths", "٥٠٠" in an Arabic-Indic 500, "²" in "² casualties") that a
// rewrite must not alter, add, or drop.
var numberRe = regexp.MustCompile(numberPattern)

// wordRe matches a maximal run of Unicode letters — one "word" for the
// fact-word check, so a fact-word must match a whole word in the rewritten
// prose, never a substring of a longer word ("record" is not "recording").
var wordRe = regexp.MustCompile(`\pL+`)

// nameTokenRe matches one atomic name fact-token: a maximal run of Unicode
// letters OR a signed numeric run (the same numberPattern numberRe uses).
// Tokenizing the name on BOTH makes a name's embedded number a fact
// (SEC-207): "M20" is the two facts "M" and "20", and a purely-numeric name
// "42" is the single fact "42" — so neither can be silently dropped by a
// rewrite. The pre-SEC-207 code tokenized with \pL+ only, which made the
// "20" of "M20" invisible and skipped a purely-numeric name entirely.
var nameTokenRe = regexp.MustCompile(`\pL+|` + numberPattern)

// FactLock reports whether every engine fact in original survives verbatim in
// rewritten: the ordered number sequence of its Text, the set of fact-bearing
// words of its Text (news-facts.json, SEC-148), and its Name (when
// non-empty) as a whole-word phrase bounded on both sides. It is the
// exact-match mechanism of AC-6 — a rewrite that changes a death count, a
// record margin, a named entity, or the prose's meaning fails it.
func FactLock(original Story, rewritten string) bool {
	// Load (and, once, validate) the fact-word list. New normally loads it
	// first; this is defense in depth for standalone callers. A lock that
	// cannot load its vocabulary fails closed — reject every rewrite rather
	// than verify against an empty set and report an unsafe rewrite safe.
	loadFactWordsOnce("")
	if factWordsErr != nil {
		return false
	}
	if !numbersSurvive(original.Text, rewritten) {
		return false
	}
	if !factWordsSurvive(original.Text, rewritten, factWordsSet) {
		return false
	}
	return nameSurvives(original.Name, original.Text, rewritten)
}

// numbersSurvive reports whether the ordered sequence of numeric tokens in
// original equals the sequence in rewritten — a rewrite that changes a
// number ("2"→"20"), paraphrases one away ("42"→"forty-two"), invents one
// ("42"→"42 deaths, 500 injured"), or reorders two numbers ("2 deaths on 3
// roads"→"3 deaths on 2 roads") fails. The comparison is strict ordered
// equality, not a multiset (SEC-144): the position of a number among the
// story's facts is itself a fact. Token equality, never substring
// containment (SEC-108).
func numbersSurvive(original, rewritten string) bool {
	return slices.Equal(numberRe.FindAllString(original, -1), numberRe.FindAllString(rewritten, -1))
}

// nameSurvives reports whether name appears in prose as a whole-token phrase
// bounded on BOTH sides: its tokens must appear contiguously as whole
// tokens, and the phrase must not be immediately preceded or followed by
// another token. A name token is a run of letters or a signed numeric run
// (nameTokenRe), so a name's embedded number is itself a fact (SEC-207):
// "M20" rewritten to "M 2" drops the "20" and fails, and a purely-numeric
// name "42" is checked rather than skipped. "Pent Lane" survives verbatim,
// but "Pent Lane East" (right extension, SEC-108), "East Pent Lane" (left
// mirror, SEC-146), and "on Pent Lane" (a preceding word — a conservative
// false rejection) do not: without a part-of-speech model the lock cannot
// distinguish a preceding preposition from a name-extending qualifier, so it
// rejects both rather than adopt a misreported road name. A name token must
// not be a substring of a longer token ("Pent" is not "Penton").
//
// When name is empty the story has no named entity, and "empty stays empty"
// holds (SEC-216) like the number and fact-word classes: the rewrite must
// not introduce a named entity the original prose did not carry. A named
// entity has two lexical signatures the lock can see without a
// part-of-speech model: a proper noun (a letter run whose first rune is a
// Titlecase or Uppercase letter — §20 names are proper nouns) and a location
// phrase (two or more consecutive words absent from the original). A rewrite
// of "42 deaths" that invents "42 deaths on Pent Lane" adds the proper nouns
// "Pent"/"Lane" and fails, and one that invents "42 deaths near the old
// mill" adds a location phrase and fails; one that only adds a single
// lowercase verb ("42 deaths occurred") adds no name and is adopted.
func nameSurvives(name, original, prose string) bool {
	if name != "" {
		nameWords := nameTokenRe.FindAllString(name, -1)
		if len(nameWords) == 0 {
			return true // a name with no tokenizable component carries no phrase to check
		}
		proseWords := nameTokenRe.FindAllString(prose, -1)
		for i := 0; i+len(nameWords) <= len(proseWords); i++ {
			if !slices.Equal(proseWords[i:i+len(nameWords)], nameWords) {
				continue
			}
			// Matched. The name is intact only when no token extends it on
			// either side: i == 0 (nothing precedes) and the final phrase
			// (nothing follows). A preceding or following token means the name
			// was extended into a different name (a changed fact).
			return i == 0 && i+len(nameWords) == len(proseWords)
		}
		return false
	}
	// Empty name: "empty stays empty" (SEC-216). The rewrite must not invent
	// a named entity the original prose did not carry. A named entity has two
	// lexical signatures the lock can see without a part-of-speech model: a
	// proper noun (a title-case word — §20 names are proper nouns, so
	// "Pent"/"Lane" count) and a location phrase (two or more consecutive new
	// words, so "the old mill" counts). A single new lowercase word is the
	// soft-layer's flavour allowance ("occurred") and is not a name.
	return noNameInvented(original, prose)
}

// noNameInvented reports whether prose introduces no named entity relative to
// original, for a nameless story (SEC-216). It is the name class's "empty
// stays empty" invariant, symmetric with the number and fact-word classes: a
// rewrite may add at most one new word, and never a proper noun or a
// multi-word phrase (an invented location the engine never asserted).
func noNameInvented(original, prose string) bool {
	if !sameFactWordSet(properNouns(original), properNouns(prose)) {
		return false
	}
	originalWords := make(map[string]struct{})
	for _, w := range wordRe.FindAllString(original, -1) {
		originalWords[strings.ToLower(w)] = struct{}{}
	}
	run := 0
	for _, w := range wordRe.FindAllString(prose, -1) {
		if _, ok := originalWords[strings.ToLower(w)]; ok {
			run = 0
			continue
		}
		run++
		if run >= 2 {
			return false
		}
	}
	return true
}

// properNouns extracts the set of proper-noun tokens in s: maximal letter
// runs (wordRe) whose first rune is a Titlecase or Uppercase letter — the
// lexical signature of a named entity (§20 names are proper nouns). A
// lowercase run is a common word, not a name, so "occurred" is not a name
// while "Pent"/"Lane" are (SEC-216).
func properNouns(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, w := range wordRe.FindAllString(s, -1) {
		r, _ := utf8.DecodeRuneInString(w)
		if unicode.IsTitle(r) || unicode.IsUpper(r) {
			out[w] = struct{}{}
		}
	}
	return out
}

// rewriteStory applies the fact-locked rewrite to one story. A nil
// rewriter returns the story unchanged. A rewriter error/timeout returns
// the engine story unchanged (AC-7). A rewrite that passes FactLock is
// adopted; one that fails it is rejected (logged via ErrFactLockRejected)
// and the engine story retained (AC-6) — never silently published.
func rewriteStory(st Story, rw ProseRewriter, correlationID string) Story {
	if rw == nil {
		return st
	}
	rewritten, err := rw.Rewrite(st)
	if err != nil {
		// AC-7: a failure/timeout never blocks or drops the story — the
		// engine prose is still produced. (No error is logged: a failed
		// optional enhancement is not a defect, and the story is complete
		// either way.)
		return st
	}
	if FactLock(st, rewritten) {
		st.Text = rewritten
		return st
	}
	// AC-6: the rewrite altered a fact. Log the rejection (GR#17 — a
	// rejected rewrite must be surfaced, never silently absorbed) and keep
	// the engine prose.
	_ = errs.New(ErrFactLockRejected, correlationID, map[string]any{
		"eventId": st.EventID,
	})
	return st
}

// RewriteBulletin applies the fact-locked soft-layer to already-generated
// bulletin stories, returning the (possibly rewritten) stories in the same
// order. It is the LLM stage of the news pipeline: generation stays
// deterministic (no rewriter), and this explicit step is the only place the
// optional soft-layer runs. A nil rewriter is a no-op.
func RewriteBulletin(stories []BulletinStory, rw ProseRewriter, correlationID string) []BulletinStory {
	if rw == nil {
		return stories
	}
	out := make([]BulletinStory, len(stories))
	for i, bs := range stories {
		bs.Story = rewriteStory(bs.Story, rw, correlationID)
		out[i] = bs
	}
	return out
}

// RewriteEpilogue applies the fact-locked soft-layer to an epilogue's
// milestone claims and biggest story (AC-7's "bulletin/epilogue item").
// A nil rewriter is a no-op.
func RewriteEpilogue(ep EpilogueReport, rw ProseRewriter, correlationID string) EpilogueReport {
	if rw == nil {
		return ep
	}
	for i := range ep.Milestones {
		ep.Milestones[i] = rewriteStory(ep.Milestones[i], rw, correlationID)
	}
	if ep.HasBiggest {
		ep.BiggestStory = rewriteStory(ep.BiggestStory, rw, correlationID)
	}
	return ep
}
