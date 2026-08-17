package news

import "testing"

// TestFactWordListLoadsAndCoversSecurityTokens pins two things about the
// embedded news-facts.json: (1) it loads and validates (a malformed edit
// fails construction and this test, GR#15/GR#7), and (2) it still carries
// the tokens the SEC-148 regression tests rely on — if a data edit dropped
// one of these, the FactLock tests in llm_test.go would silently stop
// testing what they think they test.
func TestFactWordListLoadsAndCoversSecurityTokens(t *testing.T) {
	set, err := loadFactWords("facts-correlation")
	if err != nil {
		t.Fatalf("loadFactWords: %v", err)
	}
	if len(set) == 0 {
		t.Fatal("fact-word list loaded empty")
	}
	for _, tok := range []string{"deaths", "births", "recoveries", "record", "march", "april"} {
		if _, ok := set[tok]; !ok {
			t.Errorf("news-facts.json no longer carries the SEC-148 token %q — the FactLock regression tests would silently stop covering it", tok)
		}
	}
}

// TestFactWordsSurvive is the fact-word-set mechanism at unit level: a swap
// of an event-type word changes the set and must fail, while an unchanged
// set (including a verbatim rewrite) passes.
func TestFactWordsSurvive(t *testing.T) {
	set, err := loadFactWords("facts-unit-correlation")
	if err != nil {
		t.Fatalf("loadFactWords: %v", err)
	}
	if factWordsSurvive("2 deaths", "2 births", set) {
		t.Error("factWordsSurvive accepted deaths -> births")
	}
	if !factWordsSurvive("2 deaths", "2 deaths occurred", set) {
		t.Error("factWordsSurvive rejected a rewrite that preserved every fact-word")
	}
}

// TestIsLetterWord pins the token schema: a fact-word token must be exactly
// one whole word of letters, so whole-word matching is unambiguous.
func TestIsLetterWord(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"deaths", true},
		{"march", true},
		{"", false},
		{"deaths2", false},
		{"death toll", false},
		{"before-after", false},
	}
	for _, tc := range cases {
		if got := isLetterWord(tc.in); got != tc.want {
			t.Errorf("isLetterWord(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
