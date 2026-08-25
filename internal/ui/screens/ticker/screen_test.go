package ticker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// mustWire marshals v to JSON for use as a Delta.Patch in tests. These
// are fixed, known-marshalable wire structs, so a failure here indicates
// a real bug in the test itself, not an expected condition.
func mustWire(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

// assertErrCode fails t unless err is a registry error carrying wantCode.
func assertErrCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error carrying %s, got nil", wantCode)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != wantCode {
		t.Errorf("e.Code = %s, want %s", e.Code, wantCode)
	}
}

func TestSubscribe_RejectsUnrecognisedView(t *testing.T) {
	s := New("corr-sub")
	sent := 0
	send := func(protocol.Command) error { sent++; return nil }

	if err := s.Subscribe(ViewTicker, send); err != nil {
		t.Fatalf("Subscribe(ViewTicker): %v", err)
	}
	if sent != 1 {
		t.Errorf("sent = %d, want 1", sent)
	}

	err := s.Subscribe("f9.bogus", send)
	assertErrCode(t, err, ErrUnrecognisedView)
	if sent != 1 {
		t.Errorf("sent = %d after rejected Subscribe, want still 1", sent)
	}
}

func TestBindSubscription_RejectsUnrecognisedView(t *testing.T) {
	s := New("corr-bind")

	// A known view binds successfully.
	if err := s.BindSubscription(ViewTicker, "sub-tick"); err != nil {
		t.Fatalf("BindSubscription(ViewTicker): %v", err)
	}

	// An unknown view is rejected with MET-U702 (ErrUnrecognisedView) and
	// records no binding (SEC-073) — mirroring Subscribe's validation.
	err := s.BindSubscription("f9.bogus", "sub-bogus")
	assertErrCode(t, err, ErrUnrecognisedView)

	// The rejected id was never bound: a delta for it is dropped as an
	// unknown subscription (MET-U701), never routed silently (SEC-073).
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-bogus", Patch: mustWire(t, wireTickerPatch{
		SchemaVersion: 1,
		Events:        []wireStory{{EventID: "evt-1", Text: "legit"}},
	})})
	if _, have := s.Ticker(); have {
		t.Fatalf("Ticker() have=true after a delta for a rejected bogus view — want dropped as unknown")
	}
}

func TestSubscribeAll_SendsFourViewsInOrder(t *testing.T) {
	s := New("corr-suball")
	var order []string
	send := func(cmd protocol.Command) error {
		if p, ok := cmd.Payload.(protocol.SubscribePayload); ok {
			order = append(order, p.ViewName)
		}
		return nil
	}
	if err := s.SubscribeAll(send); err != nil {
		t.Fatalf("SubscribeAll: %v", err)
	}
	want := []string{ViewTicker, ViewBulletin, ViewAnnual, ViewArchive}
	if len(order) != len(want) {
		t.Fatalf("subscribed %d views, want %d (%v)", len(order), len(want), order)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("view[%d] = %q, want %q", i, order[i], w)
		}
	}
}

func TestApplyDelta_RoutesToCorrectView(t *testing.T) {
	s := New("corr-route")
	s.BindSubscription(ViewTicker, "sub-tick")
	s.BindSubscription(ViewArchive, "sub-arch")

	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-tick", Patch: mustWire(t, wireTickerPatch{
		SchemaVersion: 1,
		Events:        []wireStory{{EventID: "evt-1", Tick: 10, Name: "Pent Lane", Text: "queue clears"}},
	})})
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: mustWire(t, wireArchivePatch{
		SchemaVersion: 1,
		Stories:       []wireStory{{EventID: "evt-2", Tick: 9, Name: "Seabrook", Text: "first graduate"}},
	})})

	events, haveTicker := s.Ticker()
	if !haveTicker || len(events) != 1 || events[0].EventID != "evt-1" {
		t.Errorf("Ticker() = %+v, %v, want one event evt-1", events, haveTicker)
	}
	archive, haveArchive := s.Archive()
	if !haveArchive || len(archive) != 1 || archive[0].EventID != "evt-2" {
		t.Errorf("Archive() = %+v, %v, want one story evt-2", archive, haveArchive)
	}
}

func TestApplyDelta_UnknownSubscriptionDropped(t *testing.T) {
	s := New("corr-unknown")
	// No BindSubscription for "sub-ghost": SF-7 requires the delta be
	// dropped and logged, never applied, never a panic.
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-ghost", Patch: mustWire(t, wireTickerPatch{
		SchemaVersion: 1,
		Events:        []wireStory{{EventID: "evt-1"}},
	})})
	if _, haveTicker := s.Ticker(); haveTicker {
		t.Error("Ticker() reports data after an unbound-SubscriptionID delta was applied")
	}
}

func TestApplyDelta_MalformedPatchDropped(t *testing.T) {
	s := New("corr-malformed")
	s.BindSubscription(ViewTicker, "sub-tick")

	// Establish a known-good state.
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-tick", Patch: mustWire(t, wireTickerPatch{
		SchemaVersion: 1,
		Events:        []wireStory{{EventID: "evt-1", Text: "good"}},
	})})

	// A malformed patch (wrong schemaVersion) must be dropped, leaving the
	// last-known-good state intact — never partially applied, never a panic.
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-tick", Patch: mustWire(t, wireTickerPatch{
		SchemaVersion: 99,
		Events:        []wireStory{{EventID: "evt-2", Text: "bad"}},
	})})

	events, _ := s.Ticker()
	if len(events) != 1 || events[0].EventID != "evt-1" {
		t.Errorf("Ticker() after malformed patch = %+v, want unchanged [evt-1]", events)
	}
}

func TestApplyDelta_MissingEventIDRejected(t *testing.T) {
	s := New("corr-tik5")
	s.BindSubscription(ViewTicker, "sub-tick")

	// TIK-5: a story with no backing event ID is rejected at the patch
	// boundary — even though its prose reads plausibly — while the
	// well-formed sibling story in the same patch still applies.
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-tick", Patch: mustWire(t, wireTickerPatch{
		SchemaVersion: 1,
		Events: []wireStory{
			{EventID: "evt-1", Text: "legitimate"},
			{EventID: "", Text: "plausible but untraceable"},
		},
	})})

	events, _ := s.Ticker()
	if len(events) != 1 || events[0].EventID != "evt-1" {
		t.Errorf("Ticker() = %+v, want exactly [evt-1] (the eventId-less story must be dropped)", events)
	}
}

func TestApplyDelta_ArchiveReplacesWholesale(t *testing.T) {
	s := New("corr-arch")
	s.BindSubscription(ViewArchive, "sub-arch")

	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: mustWire(t, wireArchivePatch{
		SchemaVersion: 1,
		Stories:       []wireStory{{EventID: "evt-1", Text: "a"}},
	})})
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: mustWire(t, wireArchivePatch{
		SchemaVersion: 1,
		Stories:       []wireStory{{EventID: "evt-1", Text: "a"}, {EventID: "evt-2", Text: "b"}},
	})})

	archive, have := s.Archive()
	if !have || len(archive) != 2 {
		t.Fatalf("Archive() = %+v (have=%v), want two stories", archive, have)
	}
}

func TestBulletin_SortedByRank(t *testing.T) {
	s := New("corr-bull")
	s.BindSubscription(ViewBulletin, "sub-bull")

	// Engine sent the stories out of rank order; Bulletin() must return
	// them sorted by Rank ascending (tie-broken by EventID) — GR#21's
	// deterministic order regardless of wire order.
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-bull", Patch: mustWire(t, wireBulletinPatch{
		SchemaVersion: 1,
		Month:         12,
		Stories: []wireBulletinStory{
			{wireStory: wireStory{EventID: "c", Text: "third"}, Salience: 0.3, Rank: 3},
			{wireStory: wireStory{EventID: "a", Text: "lead"}, Salience: 0.9, Rank: 1},
			{wireStory: wireStory{EventID: "b", Text: "second"}, Salience: 0.6, Rank: 2},
		},
	})})

	stories, month, have := s.Bulletin()
	if !have || month != 12 || len(stories) != 3 {
		t.Fatalf("Bulletin() = %+v, month=%d, have=%v, want 3 stories month 12", stories, month, have)
	}
	wantOrder := []string{"a", "b", "c"}
	for i, w := range wantOrder {
		if stories[i].EventID != w {
			t.Errorf("stories[%d].EventID = %q, want %q", i, stories[i].EventID, w)
		}
	}
}

func TestScreen_AccessorCopies(t *testing.T) {
	// An accessor must return a copy, not a slice alias into screen state
	// — mutating the returned slice must not corrupt the screen.
	s := New("corr-copy")
	s.BindSubscription(ViewArchive, "sub-arch")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: mustWire(t, wireArchivePatch{
		SchemaVersion: 1,
		Stories:       []wireStory{{EventID: "evt-1", Text: "a"}},
	})})

	got, _ := s.Archive()
	got[0].Text = "mutated"
	got[0].EventID = "tampered"

	again, _ := s.Archive()
	if again[0].EventID != "evt-1" || again[0].Text != "a" {
		t.Errorf("mutating Archive()'s return corrupted the screen: %+v", again)
	}
}

func TestAdvanceScroll_ClampsAndWraps(t *testing.T) {
	s := New("corr-scroll")
	s.AdvanceScroll(2)
	s.AdvanceScroll(-10) // clamps at 0, never negative
	if got := s.ScrollStep(); got != 0 {
		t.Errorf("ScrollStep() = %d, want 0 (negative clamp)", got)
	}
	s.AdvanceScroll(3)
	if got := s.ScrollStep(); got != 3 {
		t.Errorf("ScrollStep() = %d, want 3", got)
	}
}

// TestStoryHeadline_Formatting locks the display format: a named story
// renders "<name>: <text>", an unnamed story renders bare "<text>".
func TestStoryHeadline_Formatting(t *testing.T) {
	named := Story{EventID: "e", Name: "Pent Lane", Text: "queue clears"}
	if got := storyHeadline(named); !strings.HasPrefix(got, "Pent Lane: ") {
		t.Errorf("storyHeadline(named) = %q, want name-prefixed", got)
	}
	unnamed := Story{EventID: "e", Text: "queue clears"}
	if got := storyHeadline(unnamed); got != "queue clears" {
		t.Errorf("storyHeadline(unnamed) = %q, want bare text", got)
	}
}
