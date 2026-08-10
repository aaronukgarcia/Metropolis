package keys

import "testing"

type stubIndex struct{ results []string }

func (s stubIndex) Search(query string) []string { return s.results }

func TestSearchStepsForwardAndBackwardThroughMatches(t *testing.T) {
	g := newTestGrammar()
	idx := stubIndex{results: []string{"Elm St", "Elm Ave", "Elm Court"}}

	got := g.Search("elm", idx)
	if len(got) != 3 {
		t.Fatalf("Search returned %d matches, want 3", len(got))
	}

	first, ok := g.NextMatch()
	if !ok || first != "Elm St" {
		t.Fatalf("NextMatch = %q,%v want Elm St,true", first, ok)
	}
	second, ok := g.NextMatch()
	if !ok || second != "Elm Ave" {
		t.Fatalf("NextMatch = %q,%v want Elm Ave,true", second, ok)
	}
	back, ok := g.PrevMatch()
	if !ok || back != "Elm St" {
		t.Fatalf("PrevMatch = %q,%v want Elm St,true", back, ok)
	}
}

func TestSearchWithNoMatchesStepsReturnFalse(t *testing.T) {
	g := newTestGrammar()
	g.Search("nothing", stubIndex{})
	if _, ok := g.NextMatch(); ok {
		t.Fatalf("NextMatch on empty result set reported ok=true")
	}
	if _, ok := g.PrevMatch(); ok {
		t.Fatalf("PrevMatch on empty result set reported ok=true")
	}
}

func TestSearchWrapsAround(t *testing.T) {
	g := newTestGrammar()
	g.Search("x", stubIndex{results: []string{"A", "B"}})
	g.NextMatch() // A
	g.NextMatch() // B
	wrapped, ok := g.NextMatch()
	if !ok || wrapped != "A" {
		t.Fatalf("NextMatch did not wrap: got %q,%v want A,true", wrapped, ok)
	}
}
