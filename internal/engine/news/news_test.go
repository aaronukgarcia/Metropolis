package news

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestTickerAttribution_SourceReferencesResolveToEvents is AC-1: two
// injected sim events must produce two ticker items whose source
// references (event ID, entity ID, tick, month) differ and each resolve
// back to the event it came from — not a shared or cosmetic value.
func TestTickerAttribution_SourceReferencesResolveToEvents(t *testing.T) {
	api, err := New("ticker-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := api.SetRoadNamer(fakeNamer{names: map[string]string{
		"road-1": "Pent Lane",
		"road-2": "Cheriton Avenue",
	}}); err != nil {
		t.Fatalf("SetRoadNamer: %v", err)
	}

	ev1 := Event{ID: "ev-1", Tick: 5, Category: CategoryRecord, Magnitude: 3, EntityID: "road-1", Text: "queue on the road"}
	ev2 := Event{ID: "ev-2", Tick: 65, Category: CategoryRecord, Magnitude: 4, EntityID: "road-2", Text: "queue on the road"}

	s1, err := api.Ingest(ev1)
	if err != nil {
		t.Fatalf("Ingest ev1: %v", err)
	}
	s2, err := api.Ingest(ev2)
	if err != nil {
		t.Fatalf("Ingest ev2: %v", err)
	}

	// Source references must differ, and each must resolve to its own event.
	if s1.EventID == s2.EventID {
		t.Errorf("source references must differ, both are %q", s1.EventID)
	}
	if s1.EventID != "ev-1" || s2.EventID != "ev-2" {
		t.Errorf("EventID did not resolve to the injected event: got %q and %q", s1.EventID, s2.EventID)
	}
	if s1.EntityID != "road-1" || s2.EntityID != "road-2" {
		t.Errorf("EntityID did not resolve to the injected entity: got %q and %q", s1.EntityID, s2.EntityID)
	}
	if s1.Tick != ev1.Tick || s1.Month != monthOf(ev1.Tick) {
		t.Errorf("story 1 tick/month = %d/%d, want %d/%d", s1.Tick, s1.Month, ev1.Tick, monthOf(ev1.Tick))
	}
	if s2.Tick != ev2.Tick || s2.Month != monthOf(ev2.Tick) {
		t.Errorf("story 2 tick/month = %d/%d, want %d/%d", s2.Tick, s2.Month, ev2.Tick, monthOf(ev2.Tick))
	}
	if s1.Name != "Pent Lane" || s2.Name != "Cheriton Avenue" {
		t.Errorf("Name did not resolve to the injected entity's name: got %q and %q", s1.Name, s2.Name)
	}
	if s1.Text != ev1.Text || s2.Text != ev2.Text {
		t.Errorf("Text did not resolve back to the injected event")
	}
}

// TestUnresolvedReference_ReturnsRegistryError is AC-8 (with the BUG-100
// assertion made explicit): a generation-time reference to an entity that
// no longer resolves must produce a registry-sourced error whose code
// matches ErrUnresolvedEntity, and must neither fabricate a placeholder
// name nor silently drop the story (the event is not recorded).
func TestUnresolvedReference_ReturnsRegistryError(t *testing.T) {
	api, err := New("unresolved-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := api.SetRoadNamer(fakeNamer{names: map[string]string{}}); err != nil {
		t.Fatalf("SetRoadNamer: %v", err)
	}

	_, err = api.Ingest(Event{
		ID: "ev-ghost", Tick: 0, Category: CategoryRecord, Magnitude: 1,
		EntityID: "ghost-road", Text: "story about a road that no longer exists",
	})
	if err == nil {
		t.Fatal("expected an error for an unresolvable entity reference")
	}

	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("error is not a registry-sourced *errs.E: %v", err)
	}
	if e.Code != ErrUnresolvedEntity {
		t.Errorf("registry code = %q, want %q", e.Code, ErrUnresolvedEntity)
	}

	// No silently-dropped story: the event must not be in the log.
	if got := api.History().Len(); got != 0 {
		t.Errorf("unresolvable event was recorded (history len %d), it should have been rejected, not dropped", got)
	}
}

// TestDanglingEntity_NoNamerWired covers AC-8's other shape: a non-empty
// entity reference with no RoadNamer wired at all (the engine.roads seam
// unset) is the same registry error, never a fabricated name.
func TestDanglingEntity_NoNamerWired(t *testing.T) {
	api, err := New("dangling-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// No SetRoadNamer call — the seam is unset.
	_, err = api.Ingest(Event{
		ID: "ev-ghost", Tick: 0, Category: CategoryRecord, Magnitude: 1,
		EntityID: "any-road", Text: "story about a road",
	})
	if err == nil {
		t.Fatal("expected an error for an entity reference with no naming seam wired")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrUnresolvedEntity {
		t.Fatalf("want ErrUnresolvedEntity, got %v", err)
	}
}

// TestInvalidEvent_Rejected is GR#1/GR#16 at the Ingest boundary: a
// malformed event is rejected with ErrInvalidEvent and never recorded.
func TestInvalidEvent_Rejected(t *testing.T) {
	api, err := New("invalid-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct {
		name string
		ev   Event
	}{
		{"empty id", Event{ID: "   ", Tick: 0, Category: CategoryRecord, Magnitude: 1, Text: "x"}},
		{"negative tick", Event{ID: "e1", Tick: -1, Category: CategoryRecord, Magnitude: 1, Text: "x"}},
		{"unknown category", Event{ID: "e1", Tick: 0, Category: "bogus", Magnitude: 1, Text: "x"}},
		{"negative magnitude", Event{ID: "e1", Tick: 0, Category: CategoryRecord, Magnitude: -1, Text: "x"}},
		{"empty text", Event{ID: "e1", Tick: 0, Category: CategoryRecord, Magnitude: 1, Text: "  "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := api.Ingest(tc.ev)
			if err == nil {
				t.Fatalf("expected ErrInvalidEvent for %s, got nil", tc.name)
			}
			var e *errs.E
			if !errors.As(err, &e) || e.Code != ErrInvalidEvent {
				t.Fatalf("want ErrInvalidEvent, got %v", err)
			}
		})
	}
	if got := api.History().Len(); got != 0 {
		t.Errorf("invalid events must not be recorded, history len = %d", got)
	}
}

// TestConcurrentIngestNoDataRace is AC-12: the ticker-ingestion path fed
// concurrently from multiple shard workers must record every event exactly
// once with no data race. The only assertion is the guaranteed one — the
// final count — which holds under any schedule; it is not a timing race.
func TestConcurrentIngestNoDataRace(t *testing.T) {
	api, err := New("race-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const workers = 8
	const perWorker = 200

	var wg sync.WaitGroup
	errCh := make(chan error, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				ev := Event{
					ID:        fmt.Sprintf("w%d-e%d", w, i),
					Tick:      int64(i),
					Category:  CategoryRecord,
					Magnitude: 1,
					Text:      "story",
				}
				if _, err := api.Ingest(ev); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent Ingest failed: %v", err)
	}
	if got, want := api.History().Len(), workers*perWorker; got != want {
		t.Errorf("history len = %d, want %d (every event recorded exactly once)", got, want)
	}
}

// TestArchiveQuery_MatchesAndOrders is the archive's basic query surface:
// Query returns matching stories in ingest order, and Archive returns all
// of them.
func TestArchiveQuery_MatchesAndOrders(t *testing.T) {
	api, err := New("archive-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ids := []string{"a", "b", "c"}
	for i, id := range ids {
		if _, err := api.Ingest(Event{ID: id, Tick: int64(i), Category: CategoryRecord, Magnitude: 1, Text: "story"}); err != nil {
			t.Fatalf("Ingest %s: %v", id, err)
		}
	}
	all := api.Archive()
	if len(all) != 3 {
		t.Fatalf("Archive len = %d, want 3", len(all))
	}
	for i, st := range all {
		if st.EventID != ids[i] {
			t.Errorf("Archive[%d].EventID = %q, want %q (ingest order)", i, st.EventID, ids[i])
		}
	}
	matches := api.Query(func(st Story) bool { return strings.HasPrefix(st.EventID, "b") })
	if len(matches) != 1 || matches[0].EventID != "b" {
		t.Errorf("Query prefix 'b' returned %+v, want just event b", matches)
	}
}
