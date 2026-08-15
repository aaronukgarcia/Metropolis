package ticker

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// TestTIK6_ArchiveIsTheSingleEpilogueSource asserts TIK-6's "a single
// store, not a duplicated one" (GR#3) within this package's scope: the
// searchable history archive and the epilogue's data source are the SAME
// store, exposed through the same Archive() query path — not a second
// copy. The epilogue's own end-game presentation is MOD-043's concern
// (out of scope per ui.screen.ticker.md); what this screen guarantees,
// and what this test proves, is that whatever reads the archive for the
// epilogue reads exactly the data this screen's search indexes.
func TestTIK6_ArchiveIsTheSingleEpilogueSource(t *testing.T) {
	s := New("corr-tik6")
	s.BindSubscription(ViewArchive, "sub-arch")

	full := []wireStory{
		{EventID: "evt-1", Name: "Pent Lane", Text: "queue clears"},
		{EventID: "evt-2", Name: "Pent Way", Text: "roadworks"},
		{EventID: "evt-3", Name: "Seabrook", Text: "first graduate"},
	}
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: mustWire(t, wireArchivePatch{
		SchemaVersion: 1,
		Stories:       full,
	})})

	// The epilogue-source path (Archive()) and the search path
	// (SearchStories) must read the SAME store: every search match must be
	// an equal member of the Archive() result, never a divergent second
	// copy. If search indexed a second store, a match could differ from —
	// or be absent from — Archive(), and the epilogue (reading Archive)
	// would not reconcile with what the player searched.
	archive, have := s.Archive()
	if !have || len(archive) != len(full) {
		t.Fatalf("Archive() = %+v (have=%v), want the %d-story store", archive, have, len(full))
	}
	byEventID := make(map[string]Story, len(archive))
	for _, st := range archive {
		byEventID[st.EventID] = st
	}

	matches := s.SearchStories("pent")
	if len(matches) != 2 {
		t.Fatalf("SearchStories(\"pent\") = %d matches, want 2", len(matches))
	}
	for _, m := range matches {
		fromArchive, ok := byEventID[m.EventID]
		if !ok {
			t.Errorf("search match %+v is not present in Archive() — search indexed a second store (TIK-6 violated)", m)
			continue
		}
		if fromArchive != m {
			t.Errorf("search match %+v differs from Archive()'s %+v for the same event ID — divergent copies (TIK-6 violated)", m, fromArchive)
		}
	}
}
