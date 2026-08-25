package ticker

// SEC-075: the archive screen's NextMatch/PrevMatch (search.go) mirror
// keys.KeyGrammar's n/N stepping arithmetic — circular wrap, searchPos
// starting at -1 — but reimplement it over []Story instead of consuming
// keys' stepping, so a future change to ui.keys' wrap or start index would
// silently diverge the ticker's n/N behaviour from every other screen.
//
// This file locks the two implementations together. It drives a
// keys.KeyGrammar and the ticker Screen over the SAME match set (the screen
// is itself the grammar's keys.NameIndex, so the grammar's []string matches
// are the screen's []Story matches projected to names, in the same order)
// and asserts the returned names agree at every step, across a full wrap
// cycle plus one, in both directions. If either side's wrap or start index
// changes, the sequences disagree and this test fails — the two can no
// longer drift silently apart.

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// TestSearchSteppingAgreesWithKeysGrammar steps the ticker screen and a
// keys.KeyGrammar forward and backward in lockstep over the same match set,
// asserting identical step sequences.
func TestSearchSteppingAgreesWithKeysGrammar(t *testing.T) {
	s := setupArchive(t, []wireStory{
		{EventID: "e1", Name: "Pent Lane", Text: "queue clears"},
		{EventID: "e2", Name: "Pent Way", Text: "roadworks"},
		{EventID: "e3", Name: "Pent Stream", Text: "resurfacing"},
		{EventID: "e4", Name: "Seabrook", Text: "unrelated"},
	})

	// The grammar searches the same index, so its match set is the screen's
	// matches projected to names, in the same archive order.
	g := keys.NewKeyGrammar(nil, 0, 0, "corr-drift")
	if got := g.Search("pent", s); len(got) != 3 {
		t.Fatalf("setup: grammar.Search(%q) = %d matches, want 3", "pent", len(got))
	}

	// Reset the screen's selection so both cursors start at -1 before the
	// first step.
	if n := len(s.SearchStories("pent")); n != 3 {
		t.Fatalf("setup: screen.SearchStories(%q) = %d matches, want 3", "pent", n)
	}

	// 2*N+1 steps covers two full wraps plus one, catching any divergence in
	// the wrap or the -1 start index.
	const n = 3
	for i := 0; i < 2*n+1; i++ {
		st, sok := s.NextMatch()
		name, gok := g.NextMatch()
		if sok != gok {
			t.Fatalf("forward step %d: ok mismatch (screen %v, keys %v)", i, sok, gok)
		}
		if !sok {
			t.Fatalf("forward step %d: both reported no match unexpectedly", i)
		}
		if st.Name != name {
			t.Fatalf("forward step %d: screen stepped to %q but keys stepped to %q — n stepping diverged (SEC-075)", i, st.Name, name)
		}
	}

	// Same full-wrap coverage backward.
	for i := 0; i < 2*n+1; i++ {
		st, sok := s.PrevMatch()
		name, gok := g.PrevMatch()
		if sok != gok {
			t.Fatalf("backward step %d: ok mismatch (screen %v, keys %v)", i, sok, gok)
		}
		if !sok {
			t.Fatalf("backward step %d: both reported no match unexpectedly", i)
		}
		if st.Name != name {
			t.Fatalf("backward step %d: screen stepped to %q but keys stepped to %q — N stepping diverged (SEC-075)", i, st.Name, name)
		}
	}
}

// TestSearchSteppingNoMatchesAgreesWithKeysGrammar locks the zero-match
// contract too: both must report ok=false (never a zero-value match) for an
// empty result set, in both directions.
func TestSearchSteppingNoMatchesAgreesWithKeysGrammar(t *testing.T) {
	s := setupArchive(t, []wireStory{
		{EventID: "e1", Name: "Pent Lane", Text: "queue clears"},
	})

	g := keys.NewKeyGrammar(nil, 0, 0, "corr-drift")
	g.Search("zzz", s)
	s.SearchStories("zzz")

	_, sok := s.NextMatch()
	_, gok := g.NextMatch()
	if sok != gok {
		t.Errorf("empty result set: NextMatch ok mismatch (screen %v, keys %v)", sok, gok)
	}
	if sok {
		t.Error("screen.NextMatch() reported a match for an empty result set")
	}

	_, sok = s.PrevMatch()
	_, gok = g.PrevMatch()
	if sok != gok {
		t.Errorf("empty result set: PrevMatch ok mismatch (screen %v, keys %v)", sok, gok)
	}
	if sok {
		t.Error("screen.PrevMatch() reported a match for an empty result set")
	}
}
