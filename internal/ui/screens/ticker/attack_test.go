package ticker

// Destructive-agent attack reproductions for FEAT-020 (ui.screen.ticker).
// These are NOT regression tests against a fix — they document the exact
// behaviour observed while attacking the built-but-uncommitted code, so
// the finding can be re-verified later. Each test PASSES on the current
// tree and pins the observed behaviour; a future fix is expected to
// change (and thereby break) the relevant assertion.

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// countCode reports how many Recent() entries carry code, plus the largest
// Repeat seen for that code (0 if absent).
func countCode(code string) (int, int) {
	entries := errs.Recent()
	n, maxRepeat := 0, 0
	for _, e := range entries {
		if e.Code == code {
			n++
			if e.Repeat > maxRepeat {
				maxRepeat = e.Repeat
			}
		}
	}
	return n, maxRepeat
}

// TestAttack_BindBogusViewSilentlyDrops reproduces the asymmetry: Subscribe
// rejects a non-owned view (MET-U702), but BindSubscription accepts any
// view string. A delta bound to a non-owned view is then dropped by
// ApplyDelta's switch (which has no default case) with NO log entry — an
// unbound subscription logs MET-U701, but a bound-to-bogus-view
// subscription is silent. Assertions pin the current silent behaviour.
func TestAttack_BindBogusViewSilentlyDrops(t *testing.T) {
	before701, _ := countCode(ErrUnknownSubscription)

	s := New("corr-bogus-view")
	s.BindSubscription("f9.bogus", "sub-bogus")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-bogus", Patch: mustWire(t, wireTickerPatch{
		SchemaVersion: 1,
		Events:        []wireStory{{EventID: "evt-1", Text: "legit"}},
	})})

	if _, have := s.Ticker(); have {
		t.Fatalf("Ticker() have=true after a delta bound to a bogus view — expected silent drop")
	}
	// The drop is SILENT: no MET-U701 (unknown subscription) was logged.
	after701, _ := countCode(ErrUnknownSubscription)
	if after701 != before701 {
		t.Errorf("MET-U701 count went %d -> %d after a bound-to-bogus-view delta: the drop was NOT silent, it logged", before701, after701)
	}
}

// TestAttack_EmptyEventIDFloodCoalescesInRing quantifies the TIK-5
// rejection path's resource behaviour: a patch carrying N stories, each
// with an empty eventId, produces N errs.New(MET-U703) constructions (each
// a full registry lookup, template render and ring/NDJSON write) — but the
// in-memory ring coalesces them by code (SEC-033), so the F12 tail holds
// ONE MET-U703 slot with Repeat >= N-1 rather than N distinct entries. The
// CPU/alloc work is linear in the patch size and bounded above by the 4 MiB
// wire ceiling, so this is bounded amplification, not an unbounded DoS.
func TestAttack_EmptyEventIDFloodCoalescesInRing(t *testing.T) {
	_, beforeRepeat := countCode(ErrMissingEventID)

	const n = 500
	stories := make([]wireStory, n)
	for i := range stories {
		stories[i] = wireStory{EventID: "", Text: "plausible but untraceable"}
	}
	s := New("corr-flood")
	s.BindSubscription(ViewTicker, "sub-tick")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-tick", Patch: mustWire(t, wireTickerPatch{
		SchemaVersion: 1,
		Events:        stories,
	})})

	events, _ := s.Ticker()
	if len(events) != 0 {
		t.Fatalf("Ticker() = %d events after an all-empty-eventId patch, want 0 (every story rejected)", len(events))
	}
	slots, repeat := countCode(ErrMissingEventID)
	if slots != 1 {
		t.Errorf("MET-U703 occupies %d ring slots after a %d-story flood, want exactly 1 (SEC-033 coalescing)", slots, n)
	}
	if repeat < beforeRepeat+n-1 {
		t.Errorf("MET-U703 Repeat = %d (before=%d) after %d rejected stories, want >= %d", repeat, beforeRepeat, n, beforeRepeat+n-1)
	}
}
