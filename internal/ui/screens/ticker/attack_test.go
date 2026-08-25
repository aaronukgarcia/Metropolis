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

// TestAttack_BindBogusViewSilentlyDrops verifies the SEC-073 fix. Pre-fix,
// BindSubscription accepted any view string, so a delta bound to a non-owned
// view was dropped by ApplyDelta's routing switch with NO log entry — an
// unbound subscription logs MET-U701, but a bound-to-bogus-view subscription
// was silent. Post-fix, BindSubscription rejects the bogus view (MET-U702)
// and never records the binding, so a later delta for that id is dropped as
// an UNBOUND subscription and logs MET-U701 — logged, never silent.
func TestAttack_BindBogusViewSilentlyDrops(t *testing.T) {
	beforeN, beforeR := countCode(ErrUnknownSubscription)

	s := New("corr-bogus-view")
	err := s.BindSubscription("f9.bogus", "sub-bogus")
	assertErrCode(t, err, ErrUnrecognisedView)

	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-bogus", Patch: mustWire(t, wireTickerPatch{
		SchemaVersion: 1,
		Events:        []wireStory{{EventID: "evt-1", Text: "legit"}},
	})})

	if _, have := s.Ticker(); have {
		t.Fatalf("Ticker() have=true after a delta for a rejected bogus view — expected drop")
	}
	// The drop is NO LONGER silent: the now-unbound SubscriptionID logs
	// MET-U701 exactly once more (total = slot count + coalesced repeat).
	afterN, afterR := countCode(ErrUnknownSubscription)
	if afterN+afterR != beforeN+beforeR+1 {
		t.Errorf("MET-U701 total went %d -> %d after a delta for a rejected bogus view, want +1 (SEC-073: the drop must be logged, never silent)", beforeN+beforeR, afterN+afterR)
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
