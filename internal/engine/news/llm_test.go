package news

import (
	"fmt"
	"testing"
	"unicode"
)

// The tests in this file pin the AC-6/AC-7 fact-lock contract: a rewrite
// that alters a name or a number is rejected and the engine prose retained,
// while a rewrite that preserves every fact is adopted. They are the
// SEC-108/SEC-109 coverage — token-equality for numbers, whole-word matching
// for names, never substring containment.

// TestFactLock_RejectsAlteredNumber is AC-6's number-fact rejection: a
// rewrite that changes a numeric fact ("2 deaths" -> "20 deaths") must fail
// the fact lock, because 2 is not the same token as 20.
func TestFactLock_RejectsAlteredNumber(t *testing.T) {
	st := Story{Text: "2 deaths"}
	if FactLock(st, "20 deaths") {
		t.Error("FactLock accepted a rewrite that changed 2 to 20 (substring containment, SEC-108)")
	}
}

// TestFactLock_RejectsInjectedNumber: "42 deaths" -> "42 deaths, 500 injured"
// invents a new numeric fact and must fail.
func TestFactLock_RejectsInjectedNumber(t *testing.T) {
	st := Story{Text: "42 deaths"}
	if FactLock(st, "42 deaths, 500 injured") {
		t.Error("FactLock accepted a rewrite that invented a new number 500 (SEC-108)")
	}
}

// TestFactLock_RejectsDroppedNumber: a number paraphrased into words must
// fail, because the numeric fact no longer survives verbatim.
func TestFactLock_RejectsDroppedNumber(t *testing.T) {
	st := Story{Text: "42 deaths"}
	if FactLock(st, "forty-two deaths") {
		t.Error("FactLock accepted a rewrite that dropped the numeric fact 42 (SEC-108)")
	}
}

// TestFactLock_RejectsNameExtension: a name extended on the right must fail.
func TestFactLock_RejectsNameExtension(t *testing.T) {
	st := Story{Name: "Pent Lane", Text: "2 deaths"}
	if FactLock(st, "2 deaths on Pent Lane East") {
		t.Error("FactLock accepted a rewrite that extended Pent Lane to Pent Lane East (SEC-108)")
	}
}

// TestFactLock_RejectsNameSubstring: a name must not match inside a longer
// word — "Pent" is not a fact that survives inside "Penton".
func TestFactLock_RejectsNameSubstring(t *testing.T) {
	st := Story{Name: "Pent", Text: "2 deaths"}
	if FactLock(st, "2 deaths on Pentonville Road") {
		t.Error("FactLock accepted a rewrite where Pent matched inside Pentonville (SEC-108)")
	}
}

// TestFactLock_AcceptsUnchangedText: a verbatim rewrite of a nameless story
// passes.
func TestFactLock_AcceptsUnchangedText(t *testing.T) {
	st := Story{Text: "2 deaths"}
	if !FactLock(st, "2 deaths") {
		t.Error("FactLock rejected a verbatim rewrite")
	}
}

// TestFactLock_RejectsNumberReorder is SEC-144: numbers are facts in ORDER.
// "2 deaths on 3 roads" and "3 deaths on 2 roads" hold the same number
// multiset but different facts, so the order-swapped rewrite must fail.
func TestFactLock_RejectsNumberReorder(t *testing.T) {
	st := Story{Text: "2 deaths on 3 roads"}
	if FactLock(st, "3 deaths on 2 roads") {
		t.Error("FactLock accepted a rewrite that swapped the numbers 2 and 3 (SEC-144)")
	}
}

// TestFactLock_RejectsNonASCIIDigit is SEC-145: an injected non-ASCII
// decimal digit run is a numeric fact and must be seen. "2 deaths" rewritten
// to "2 deaths, ٥٠٠ injured" (Arabic-Indic 500) must fail.
func TestFactLock_RejectsNonASCIIDigit(t *testing.T) {
	st := Story{Text: "2 deaths"}
	if FactLock(st, "2 deaths, ٥٠٠ injured") {
		t.Error("FactLock accepted a rewrite that injected a non-ASCII number ٥٠٠ (SEC-145)")
	}
}

// TestFactLock_RejectsNameLeftExtension is SEC-146: the name check must
// guard the LEFT boundary too. "Pent Lane" renamed to "East Pent Lane" in
// the prose must fail.
func TestFactLock_RejectsNameLeftExtension(t *testing.T) {
	st := Story{Name: "Pent Lane", Text: "2 deaths"}
	if FactLock(st, "2 deaths on East Pent Lane") {
		t.Error("FactLock accepted a rewrite that extended Pent Lane to East Pent Lane (SEC-146)")
	}
}

// TestFactLock_RejectsNamePrecededByWord pins the conservative half of the
// SEC-146 fix: with no part-of-speech model the lock cannot distinguish a
// preceding preposition ("on Pent Lane") from a name-extending qualifier
// ("East Pent Lane"), so it rejects both rather than adopt a misreported
// name. This is a deliberate false rejection (harmless — AC-7 falls back to
// the engine prose).
func TestFactLock_RejectsNamePrecededByWord(t *testing.T) {
	st := Story{Name: "Pent Lane", Text: "2 deaths"}
	if FactLock(st, "2 deaths on Pent Lane") {
		t.Error("FactLock accepted a rewrite where the name is preceded by a word (conservative SEC-146 rejection)")
	}
}

// TestFactLock_AcceptsStandaloneName: a name bounded on both sides survives.
func TestFactLock_AcceptsStandaloneName(t *testing.T) {
	st := Story{Name: "Pent Lane", Text: ""}
	if !FactLock(st, "Pent Lane") {
		t.Error("FactLock rejected a rewrite that is exactly the bounded name")
	}
}

// TestFactLock_RejectsFactWordSwap is SEC-148: event-type words are facts.
// "2 deaths" rewritten to "2 births" (same numbers, no name) must fail.
func TestFactLock_RejectsFactWordSwap(t *testing.T) {
	st := Story{Text: "2 deaths"}
	if FactLock(st, "2 births") {
		t.Error("FactLock accepted a rewrite that changed deaths to births (SEC-148)")
	}
	if FactLock(st, "2 recoveries") {
		t.Error("FactLock accepted a rewrite that changed deaths to recoveries (SEC-148)")
	}
}

// TestFactLock_RejectsMonthWordSwap is SEC-148's date half: a non-numeric
// month word is a fact. "record set in March" rewritten to "record set in
// April" must fail.
func TestFactLock_RejectsMonthWordSwap(t *testing.T) {
	st := Story{Text: "record set in March"}
	if FactLock(st, "record set in April") {
		t.Error("FactLock accepted a rewrite that changed March to April (SEC-148)")
	}
}

// TestFactLock_RejectsNumericHomoglyphAndSign is SEC-205: the number lock's
// tokenizer must see every Unicode numeric character a count can wear — the
// whole \p{N} class (decimal digits \p{Nd}, letter numbers \p{Nl}, other
// numbers \p{No}) — and a sign-prefixed number, whatever sign homoglyph it
// wears (\p{Pd} dash family plus '+' and '−'), must not collapse to its bare
// digits. Each rewrite below injects a numeric fact past the narrower
// \p{Nd}+ / [\p{Nd}\p{No}]+ tokenizers and must be rejected now.
func TestFactLock_RejectsNumericHomoglyphAndSign(t *testing.T) {
	st := Story{Text: "2 deaths"}
	cases := []struct {
		name    string
		rewrite string
	}{
		{"superscript two", "2 deaths, ² casualties"},
		{"superscript 500", "2 deaths, ⁵⁰⁰ casualties"},
		{"subscript 500", "2 deaths, ₅₀₀ casualties"},
		{"circled one", "2 deaths, ① casualties"},
		{"vulgar fraction half", "2 deaths, ½ casualties"},
		{"roman numeral twelve", "2 deaths, Ⅻ casualties"},
		{"leading ascii minus", "-2 deaths"},
		{"leading en dash minus", "–2 deaths"},
		{"leading em dash minus", "—2 deaths"},
		{"leading fullwidth hyphen-minus", "－2 deaths"},
		{"leading ascii plus", "+2 deaths"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if FactLock(st, tc.rewrite) {
				t.Errorf("FactLock accepted %q (SEC-205)", tc.rewrite)
			}
		})
	}
}

// TestFactLock_RejectsSignHomoglyph is SEC-214: the sign class must cover the
// whole Unicode sign-homoglyph space — the targeted sign homoglyphs
// (superscript/subscript plus and minus, plus-minus, minus-or-plus, modifier
// minus, fullwidth plus) in addition to \p{Pd}, '+' and '−' — so a sign flip
// is rejected no matter which sign homoglyph it wears. Each rewrite below
// flips a bare "2" into a signed fact and must be rejected.
func TestFactLock_RejectsSignHomoglyph(t *testing.T) {
	st := Story{Text: "2 deaths"}
	cases := []struct {
		name    string
		rewrite string
	}{
		{"superscript minus", "⁻2 deaths"},
		{"subscript minus", "₋2 deaths"},
		{"minus-or-plus", "∓2 deaths"},
		{"modifier minus", "˗2 deaths"},
		{"superscript plus", "⁺2 deaths"},
		{"subscript plus", "₊2 deaths"},
		{"fullwidth plus", "＋2 deaths"},
		{"plus-minus", "±2 deaths"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if FactLock(st, tc.rewrite) {
				t.Errorf("FactLock accepted %q (SEC-214)", tc.rewrite)
			}
		})
	}
}

// TestFactLock_RejectsAnySignHomoglyph is the SEC-214 class closure: the
// number sign class is now derived from the whole non-alphanumeric class,
// not enumerated, so a NEW sign homoglyph — one present in no earlier
// whitelist — cannot slip through by collapsing to the bare digit. The r6
// repros are modifier plus ˖ U+02D6, commercial minus ⁒ U+2052, heavy plus
// ➕ U+2795 and heavy minus ➖ U+2796; × U+00D7 and ÷ U+00F7 are further
// off-whitelist math signs the previous enumerations also missed. Each
// rewrite below decorates the bare "2" with a sign the original does not
// carry and must be rejected.
func TestFactLock_RejectsAnySignHomoglyph(t *testing.T) {
	st := Story{Text: "2 deaths"}
	cases := []struct {
		name    string
		rewrite string
	}{
		{"modifier plus", "˖2 deaths"},
		{"commercial minus", "⁒2 deaths"},
		{"heavy plus", "➕2 deaths"},
		{"heavy minus", "➖2 deaths"},
		{"multiplication sign", "×2 deaths"},
		{"division sign", "÷2 deaths"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if FactLock(st, tc.rewrite) {
				t.Errorf("FactLock accepted %q (SEC-214 sign class is not fail-closed)", tc.rewrite)
			}
		})
	}
}

// TestFactLock_RejectsModifierLetterSign is SEC-220: the fail-closed sign
// class [^\pL\pN\s\p{Z}] excluded the WHOLE letter category, so Unicode
// modifier letters (Lm) that look like horizontal bars — U+02C9 ˉ MACRON and
// U+02CD ˍ LOW MACRON — were treated as letters and collapsed to the bare
// digit, letting a sign flip wear a minus-shaped letter bar and pass the
// no-hallucinated-news gate. The sign class now excludes only real letters
// (\p{Lu}\p{Ll}\p{Lt}\p{Lo}), numbers and whitespace, so Lm is captured as
// sign decoration: a bare "2" rewritten to "ˉ2" or "ˍ2" is a signed fact and
// must be rejected.
//
// The repro characters are asserted against the Go unicode package as the
// property the regex relies on: each is genuinely Lm, and in none of the
// excluded categories (real letters Lu/Ll/Lt/Lo, numbers N, separators Z).
// An ordinary word letter, by contrast, IS an excluded real letter and NOT Lm
// — so prose letters never get folded into the sign.
func TestFactLock_RejectsModifierLetterSign(t *testing.T) {
	for _, r := range []rune{'ˉ', 'ˍ'} {
		if !unicode.Is(unicode.Lm, r) {
			t.Errorf("%U (%c) is not Lm; the SEC-220 fix relies on it being a modifier letter", r, r)
		}
		for _, cat := range []*unicode.RangeTable{unicode.Lu, unicode.Ll, unicode.Lt, unicode.Lo, unicode.N, unicode.Z} {
			if unicode.Is(cat, r) {
				t.Errorf("%U (%c) is in an excluded category; it must be captured as sign, not excluded", r, r)
			}
		}
	}
	if !unicode.Is(unicode.Ll, 'a') || unicode.Is(unicode.Lm, 'a') {
		t.Error("ordinary word letter 'a' must be an excluded real letter (Ll), not Lm")
	}

	st := Story{Text: "2 deaths"}
	cases := []struct {
		name    string
		rewrite string
	}{
		{"modifier letter macron", "ˉ2 deaths"},
		{"modifier letter low macron", "ˍ2 deaths"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if FactLock(st, tc.rewrite) {
				t.Errorf("FactLock accepted %q (SEC-220 sign flip via Lm homoglyph)", tc.rewrite)
			}
		})
	}
}

// TestFactLock_AcceptsModifierLetterSignIdentical is the SEC-220 preserve side:
// a rewrite carrying the SAME Lm sign decoration as the original still
// matches (the sign is a fact, and an identical sign is preserved), and an
// unsigned-both rewrite still passes — the tightened class must not
// over-reject verbatim prose.
func TestFactLock_AcceptsModifierLetterSignIdentical(t *testing.T) {
	cases := []struct {
		name     string
		original string
		rewrite  string
	}{
		{"unsigned both", "2 deaths", "2 deaths"},
		{"macron both", "ˉ2 deaths", "ˉ2 deaths"},
		{"low macron both", "ˍ2 deaths", "ˍ2 deaths"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !FactLock(Story{Text: tc.original}, tc.rewrite) {
				t.Errorf("FactLock rejected an identical-sign rewrite %q -> %q (SEC-220)", tc.original, tc.rewrite)
			}
		})
	}
}

// TestFactLock_AcceptsIdenticalSign is the SEC-214 preserve side: the sign
// class generalization must NOT reject a rewrite whose number carries the
// SAME sign decoration as the original. An unsigned number in both still
// matches, and a sign identical in both still matches — including a sign
// that was never in any earlier whitelist (˖, ➖).
func TestFactLock_AcceptsIdenticalSign(t *testing.T) {
	cases := []struct {
		name     string
		original string
		rewrite  string
	}{
		{"unsigned both", "2 deaths", "2 deaths"},
		{"ascii minus both", "-2 deaths", "-2 deaths"},
		{"u2212 minus both", "−2 deaths", "−2 deaths"},
		{"modifier plus both", "˖2 deaths", "˖2 deaths"},
		{"heavy minus both", "➖2 deaths", "➖2 deaths"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !FactLock(Story{Text: tc.original}, tc.rewrite) {
				t.Errorf("FactLock rejected an identical-sign rewrite %q -> %q (SEC-214)", tc.original, tc.rewrite)
			}
		})
	}
}

// TestFactLock_RejectsInventedName is SEC-216: an empty source name must not
// admit a named rewrite. "42 deaths" rewritten to "42 deaths on Pent Lane"
// invents the named entity "Pent Lane" (proper nouns "Pent"/"Lane") and must
// fail, and "42 deaths near the old mill" invents a location phrase — the
// name class enforces "empty stays empty" like the number and fact-word
// classes.
func TestFactLock_RejectsInventedName(t *testing.T) {
	st := Story{Text: "42 deaths"}
	cases := []struct {
		name    string
		rewrite string
	}{
		{"proper-noun entity", "42 deaths on Pent Lane"},
		{"location phrase", "42 deaths near the old mill"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if FactLock(st, tc.rewrite) {
				t.Errorf("FactLock accepted a rewrite that invented a named entity %q (SEC-216)", tc.rewrite)
			}
		})
	}
}

// TestFactLock_RejectsNameNumericAlteration is SEC-207: a name's embedded
// number is itself a fact. The pre-fix nameSurvives tokenized with \pL+
// only, so "M20" -> "M 2" dropped the "20" and passed, and a purely-numeric
// name "42" was skipped entirely. Both must now be rejected.
func TestFactLock_RejectsNameNumericAlteration(t *testing.T) {
	cases := []struct {
		name     string // subtest name
		story    string // the story's Name field
		original string // the story's Text field
		rewrite  string
	}{
		{"embedded number dropped", "M20", "queue 2 hours", "M 2"},
		{"pure-numeric name dropped", "42", "queue", "queue"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if FactLock(Story{Name: tc.story, Text: tc.original}, tc.rewrite) {
				t.Errorf("FactLock accepted %q for name %q (SEC-207)", tc.rewrite, tc.story)
			}
		})
	}
}

// TestRewriteStory_RejectsFactAltering is AC-6 end-to-end: a rewriter that
// alters a fact is rejected and the engine prose retained.
func TestRewriteStory_RejectsFactAltering(t *testing.T) {
	rw := fakeRewriter{fn: func(Story) (string, error) { return "20 deaths", nil }}
	st := Story{EventID: "e1", Text: "2 deaths"}
	out := rewriteStory(st, rw, "rewrite-correlation")
	if out.Text != "2 deaths" {
		t.Errorf("fact-altering rewrite was published: got %q, want engine prose %q", out.Text, "2 deaths")
	}
}

// TestRewriteStory_AdoptsFactPreserving is AC-6/AC-7: a rewrite that preserves
// every fact is adopted.
func TestRewriteStory_AdoptsFactPreserving(t *testing.T) {
	rw := fakeRewriter{fn: func(Story) (string, error) { return "2 deaths occurred", nil }}
	st := Story{EventID: "e1", Text: "2 deaths"}
	out := rewriteStory(st, rw, "rewrite-correlation")
	if out.Text != "2 deaths occurred" {
		t.Errorf("fact-preserving rewrite not adopted: got %q", out.Text)
	}
}

// TestRewriteStory_RejectsNameEmbedding pins the conservative SEC-146
// consequence end-to-end: a rewrite that embeds the name alongside prose
// ("2 deaths on Pent Lane") is not provably name-preserving (the name is
// preceded by a word), so it is rejected and the engine prose retained —
// the soft-layer never gets to misreport the named entity.
func TestRewriteStory_RejectsNameEmbedding(t *testing.T) {
	rw := fakeRewriter{fn: func(Story) (string, error) { return "2 deaths on Pent Lane", nil }}
	st := Story{Name: "Pent Lane", EventID: "e1", Text: "2 deaths"}
	out := rewriteStory(st, rw, "rewrite-correlation")
	if out.Text != "2 deaths" {
		t.Errorf("name-embedding rewrite was adopted: got %q, want engine prose %q", out.Text, "2 deaths")
	}
}

// TestRewriteStory_ErrorFallsBack is AC-7: a rewriter error keeps the engine
// prose.
func TestRewriteStory_ErrorFallsBack(t *testing.T) {
	rw := fakeRewriter{fn: func(Story) (string, error) { return "", fmt.Errorf("boom") }}
	st := Story{EventID: "e1", Text: "2 deaths"}
	out := rewriteStory(st, rw, "rewrite-correlation")
	if out.Text != "2 deaths" {
		t.Errorf("rewrite error did not fall back to engine prose: got %q", out.Text)
	}
}

// TestRewriteStory_NilRewriterIsNoop: a disabled (nil) rewriter is a no-op.
func TestRewriteStory_NilRewriterIsNoop(t *testing.T) {
	st := Story{EventID: "e1", Text: "2 deaths"}
	out := rewriteStory(st, nil, "rewrite-correlation")
	if out.Text != "2 deaths" {
		t.Errorf("nil rewriter changed the story: got %q", out.Text)
	}
}
