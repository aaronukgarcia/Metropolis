package ticker

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func setupArchive(t *testing.T, stories []wireStory) *Screen {
	t.Helper()
	s := New("corr-search")
	s.BindSubscription(ViewArchive, "sub-arch")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: mustWire(t, wireArchivePatch{
		SchemaVersion: 1,
		Stories:       stories,
	})})
	return s
}

func TestSearch_NameSubstringCaseInsensitive(t *testing.T) {
	s := setupArchive(t, []wireStory{
		{EventID: "e1", Name: "Pent Lane", Text: "queue clears"},
		{EventID: "e2", Name: "Pent Way", Text: "roadworks"},
		{EventID: "e3", Name: "Seabrook", Text: "first graduate"},
		{EventID: "e4", Text: "no name here"},
	})

	// "pent" (lowercase) matches the two capitalised Pent names, in
	// archive order (UI-SPEC §3's "/pent cycles Pent Lane, Pent Way").
	names := s.Search("pent")
	if len(names) != 2 || names[0] != "Pent Lane" || names[1] != "Pent Way" {
		t.Errorf("Search(\"pent\") = %v, want [Pent Lane Pent Way]", names)
	}

	// A query matching nothing returns empty, and a story with no name
	// never matches a non-empty query.
	if got := s.Search("zzz"); len(got) != 0 {
		t.Errorf("Search(\"zzz\") = %v, want empty", got)
	}
}

func TestSearchStories_NStepStepping(t *testing.T) {
	s := setupArchive(t, []wireStory{
		{EventID: "e1", Name: "Pent Lane", Text: "queue clears"},
		{EventID: "e2", Name: "Pent Way", Text: "roadworks"},
	})

	matches := s.SearchStories("pent")
	if len(matches) != 2 {
		t.Fatalf("SearchStories(\"pent\") = %d matches, want 2", len(matches))
	}

	// First n steps to the first match (searchPos -1 -> 0), second n to
	// the second, third n wraps back to the first.
	first, ok := s.NextMatch()
	if !ok || first.EventID != "e1" {
		t.Fatalf("first NextMatch = %+v, %v, want e1", first, ok)
	}
	second, ok := s.NextMatch()
	if !ok || second.EventID != "e2" {
		t.Fatalf("second NextMatch = %+v, %v, want e2", second, ok)
	}
	wrapped, ok := s.NextMatch()
	if !ok || wrapped.EventID != "e1" {
		t.Fatalf("third NextMatch = %+v, %v, want wrap to e1", wrapped, ok)
	}

	// N steps backward, wrapping from the first back to the last.
	prev, ok := s.PrevMatch()
	if !ok || prev.EventID != "e2" {
		t.Fatalf("PrevMatch from first = %+v, %v, want wrap to e2", prev, ok)
	}
}

func TestSearch_NoMatchesDistinguishableFromStillLoading(t *testing.T) {
	// Still loading: archive never applied.
	empty := New("corr-loading")
	if empty.SearchActive() {
		t.Error("SearchActive() = true before any search ran")
	}
	if _, have := empty.Archive(); have {
		t.Error("Archive() reports have=true before any patch")
	}

	// Loaded, then a zero-match search: active, matched 0.
	s := setupArchive(t, []wireStory{{EventID: "e1", Name: "Pent Lane", Text: "queue clears"}})
	s.SearchStories("does-not-exist")
	if !s.SearchActive() {
		t.Error("SearchActive() = false after a zero-match search (TIK-8's 'no matches' must be active, not 'still loading')")
	}
	if got := s.SearchMatchedCount(); got != 0 {
		t.Errorf("SearchMatchedCount() = %d, want 0", got)
	}
	if _, ok := s.CurrentMatch(); ok {
		t.Error("CurrentMatch() reported a match for a zero-match search")
	}
}

func TestSearch_EmptyQueryMatchesNothing(t *testing.T) {
	s := setupArchive(t, []wireStory{{EventID: "e1", Name: "Pent Lane", Text: "queue clears"}})
	if got := s.SearchStories("   "); len(got) != 0 {
		t.Errorf("SearchStories(whitespace) = %v, want empty (never match-everything on empty query)", got)
	}
}
