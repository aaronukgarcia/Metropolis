package ticker

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// TestTIK5_EveryRenderedStoryTracesToAnEventID is the structural
// "no hallucinated news" check (TIK-5): across all four surfaces, every
// story the screen surfaces through its accessors carries a non-empty
// originating event ID, and DrillTargets produces a drill-through
// reference for every one of them. The rejection half (a story arriving
// with an empty eventId is dropped, never rendered) is covered by
// TestApplyDelta_MissingEventIDRejected in screen_test.go; this test
// proves the positive half — nothing rendered lacks a traceable source.
func TestTIK5_EveryRenderedStoryTracesToAnEventID(t *testing.T) {
	s := New("corr-tik5")
	s.BindSubscription(ViewTicker, "sub-tick")
	s.BindSubscription(ViewBulletin, "sub-bull")
	s.BindSubscription(ViewAnnual, "sub-ann")
	s.BindSubscription(ViewArchive, "sub-arch")

	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-tick", Patch: mustWire(t, wireTickerPatch{
		SchemaVersion: 1,
		Events:        []wireStory{{EventID: "evt-1", Name: "Pent Lane", Text: "queue clears"}, {EventID: "evt-2", Text: "first graduate"}},
	})})
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-bull", Patch: mustWire(t, wireBulletinPatch{
		SchemaVersion: 1,
		Month:         3,
		Stories: []wireBulletinStory{
			{wireStory: wireStory{EventID: "evt-2", Text: "first graduate"}, Rank: 1},
			{wireStory: wireStory{EventID: "evt-1", Name: "Pent Lane", Text: "queue clears"}, Rank: 2},
		},
	})})
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-ann", Patch: mustWire(t, wireAnnualPatch{
		SchemaVersion: 1,
		Year:          1,
		Numbers:       []wireAnnualNumber{{Label: "Deaths", Value: 1}},
		BiggestStory:  &wireStory{EventID: "evt-2", Text: "first graduate"},
	})})
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: mustWire(t, wireArchivePatch{
		SchemaVersion: 1,
		Stories:       []wireStory{{EventID: "evt-1", Text: "queue clears"}, {EventID: "evt-2", Text: "first graduate"}},
	})})

	// Collect every story across every surface, then assert each carries a
	// non-empty event ID.
	var all []Story
	ticker, _ := s.Ticker()
	all = append(all, ticker...)
	bulletin, _, _ := s.Bulletin()
	for _, b := range bulletin {
		all = append(all, b.Story)
	}
	annual, _ := s.Annual()
	if annual.HasBiggest {
		all = append(all, annual.BiggestStory)
	}
	archive, _ := s.Archive()
	all = append(all, archive...)

	if len(all) == 0 {
		t.Fatal("no stories surfaced across any surface — the fixture is empty")
	}
	for _, st := range all {
		if st.EventID == "" {
			t.Errorf("surfaced story %+v has no event ID — TIK-5 requires every rendered story trace to a source event", st)
		}
	}

	// Drill-through (SF-5/TIK-5): every story must carry a canonical
	// dash.DrillTarget whose EntityID is the story's originating event ID
	// and whose ViewName is a grammar-valid protocol view name — so "Enter
	// on this story" always resolves to a real source, never a dead end.
	targets := DrillTargets(all)
	if len(targets) != len(all) {
		t.Fatalf("DrillTargets produced %d targets for %d stories, want one each", len(targets), len(all))
	}
	for i, dt := range targets {
		if dt.EntityID != all[i].EventID {
			t.Errorf("DrillTarget %d EntityID = %q, want the story's event ID %q", i, dt.EntityID, all[i].EventID)
		}
		if err := protocol.ValidateViewName(dt.ViewName); err != nil {
			t.Errorf("DrillTarget %d ViewName = %q is not a grammar-valid protocol view name: %v", i, dt.ViewName, err)
		}
	}
}

// TestTIK5_BiggestStoryWithoutEventIDDropped: an annual patch whose
// biggestStory carries no eventId must render as "no story this year"
// (HasBiggest false) rather than a fabricated biggest story — the same
// rejection posture as the other surfaces, applied to the single biggest
// story.
func TestTIK5_BiggestStoryWithoutEventIDDropped(t *testing.T) {
	s := New("corr-tik5b")
	s.BindSubscription(ViewAnnual, "sub-ann")

	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-ann", Patch: mustWire(t, wireAnnualPatch{
		SchemaVersion: 1,
		Year:          2,
		BiggestStory:  &wireStory{EventID: "", Text: "plausible but untraceable"},
	})})

	annual, have := s.Annual()
	if !have {
		t.Fatal("Annual() have=false after a valid patch")
	}
	if annual.HasBiggest {
		t.Errorf("Annual() HasBiggest=true for a biggestStory with no event ID — want dropped (TIK-5)")
	}
}
