package ticker

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// Regression tests for the three SEC- findings fixed in this dispatch:
// SEC-072 (archive wire ceiling silently freezes history), SEC-074
// (search selection stale after archive patch), SEC-076 (whitespace-only
// eventId accepted). Each test is written so it FAILS against the
// unfixed code and PASSES after the fix.

// metCodeTotal sums 1+Repeat across every errs ring slot carrying code —
// the total number of times code has been raised into the in-memory ring.
// SEC-033 coalesces consecutive same-code raises into one slot with
// Repeat=N-1, so the slot count alone under-counts. Named distinctly from
// attack_test.go's countCode to avoid a duplicate symbol.
func metCodeTotal(code string) int {
	total := 0
	for _, e := range errs.Recent() {
		if e.Code == code {
			total += 1 + e.Repeat
		}
	}
	return total
}

// oversizedArchivePatch returns a raw f9.archive patch whose byte size
// exceeds maxPatchWireBytes, so decodeWirePatch rejects it BEFORE
// json.Unmarshal runs — the tests exercise the size gate, never an
// expensive decode.
func oversizedArchivePatch() []byte {
	over := make([]byte, 0, maxPatchWireBytes+128)
	over = append(over, []byte(`{"schemaVersion":1,"stories":[{"eventId":"evt-x","tick":1,"name":"Pent Lane","text":"`)...)
	over = append(over, []byte(strings.Repeat("X", maxPatchWireBytes))...)
	over = append(over, []byte(`"}]}`)...)
	return over
}

// TestSEC076_WhitespaceOnlyEventIDRejected (SEC-076): a whitespace-only
// eventId (three spaces) must be rejected at the patch boundary — loudly
// via MET-U703 — exactly like an empty eventId. Unfixed code accepted it
// and carried it into dash.DrillTarget.EntityID, breaking TIK-5's
// traceable-source rule.
func TestSEC076_WhitespaceOnlyEventIDRejected(t *testing.T) {
	before := metCodeTotal(ErrMissingEventID)

	s := New("corr-sec076")
	s.BindSubscription(ViewTicker, "sub-tick")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-tick", Patch: mustWire(t, wireTickerPatch{
		SchemaVersion: 1,
		Events:        []wireStory{{EventID: "   ", Text: "plausible but untraceable"}},
	})})

	events, _ := s.Ticker()
	if len(events) != 0 {
		t.Errorf("Ticker() accepted %d story/stories with a whitespace-only eventId, want 0 (SEC-076: reject eventIds empty after trimming)", len(events))
	}
	if after := metCodeTotal(ErrMissingEventID); after <= before {
		t.Errorf("MET-U703 was not raised for the whitespace-only eventId (total before=%d after=%d)", before, after)
	}
}

// TestSEC074_SearchInvalidatedAfterArchivePatch (SEC-074): replacing the
// archive must invalidate the search selection, so SearchMatchedCount /
// NextMatch / CurrentMatch never serve a stale snapshot while Archive()
// is fresh.
func TestSEC074_SearchInvalidatedAfterArchivePatch(t *testing.T) {
	s := New("corr-sec074")
	s.BindSubscription(ViewArchive, "sub-arch")

	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: mustWire(t, wireArchivePatch{
		SchemaVersion: 1,
		Stories:       []wireStory{{EventID: "evt-1", Name: "Pent Lane", Text: "a"}, {EventID: "evt-2", Name: "Pent Way", Text: "b"}},
	})})
	if got := len(s.SearchStories("pent")); got != 2 {
		t.Fatalf("setup: SearchStories = %d matches, want 2", got)
	}

	// The archive grows to three Pent* stories. The search selection was
	// computed against the two-story archive and must now be discarded.
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: mustWire(t, wireArchivePatch{
		SchemaVersion: 1,
		Stories:       []wireStory{{EventID: "evt-1", Name: "Pent Lane", Text: "a"}, {EventID: "evt-2", Name: "Pent Way", Text: "b"}, {EventID: "evt-3", Name: "Pent Stream", Text: "c"}},
	})})

	if s.SearchActive() {
		t.Errorf("SearchActive() still true after the archive was replaced — the search selection is stale")
	}
	if got := s.SearchMatchedCount(); got != 0 {
		t.Errorf("SearchMatchedCount() = %d after the archive patch, want 0 (selection invalidated)", got)
	}
	if st, ok := s.NextMatch(); ok {
		t.Errorf("NextMatch() returned a stale match %+v after the archive patch", st)
	}
	if st, ok := s.CurrentMatch(); ok {
		t.Errorf("CurrentMatch() returned a stale match %+v after the archive patch", st)
	}
}

// TestSEC072_OversizedArchivePatchSurfacesStalledState (SEC-072): an
// f9.archive patch whose full-snapshot payload exceeds the wire ceiling
// must surface a distinct "archive stopped updating" state (ArchiveStalled
// + a render banner) instead of silently freezing the last-known-good
// archive. Unfixed code dropped the oversized patch with haveArchive=true
// and no way to tell the archive had stopped.
func TestSEC072_OversizedArchivePatchSurfacesStalledState(t *testing.T) {
	s := New("corr-sec072")
	s.BindSubscription(ViewArchive, "sub-arch")

	// Seed a known-good archive.
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: mustWire(t, wireArchivePatch{
		SchemaVersion: 1,
		Stories:       []wireStory{{EventID: "evt-0", Text: "seed"}},
	})})
	if s.ArchiveStalled() {
		t.Fatal("ArchiveStalled() = true before any oversized patch")
	}

	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: oversizedArchivePatch()})

	if !s.ArchiveStalled() {
		t.Fatalf("ArchiveStalled() = false after an oversized archive patch — the archive froze silently (SEC-072)")
	}

	// The last-known-good archive is intact (frozen, not corrupted).
	archive, have := s.Archive()
	if !have || len(archive) != 1 || archive[0].EventID != "evt-0" {
		t.Errorf("Archive() after oversized patch = %+v (have=%v), want unchanged seed [evt-0]", archive, have)
	}

	// The render path surfaces the distinct stopped banner.
	buf := core.NewBuffer(80, 2)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 2}
	RenderArchive(buf, rect, archive, have, s.SearchActive(), s.ArchiveStalled(), s.SearchMatchedCount(), tcell.StyleDefault)
	if got := rowText(buf, 0); got != archiveStoppedText {
		t.Errorf("stalled render row 0 = %q, want %q (SEC-072 banner)", got, archiveStoppedText)
	}

	// A later patch that fits under the ceiling applies and clears the
	// stalled flag (the archive resumed updating).
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: mustWire(t, wireArchivePatch{
		SchemaVersion: 1,
		Stories:       []wireStory{{EventID: "evt-1", Text: "resumed"}},
	})})
	if s.ArchiveStalled() {
		t.Error("ArchiveStalled() = true after a fitting patch applied — the archive resumed, the flag must clear")
	}
}

// TestSEC085_ColdStartOversizedPatchSurfacesStalledNotLoading (SEC-085): on
// a cold start — no archive applied yet — an oversized FIRST patch must
// surface the stopped banner, not the misleading "loading history" state.
// The earlier fix only surfaced the stall on the previously-loaded path,
// because RenderArchive checked !haveArchive before archiveStalled.
func TestSEC085_ColdStartOversizedPatchSurfacesStalledNotLoading(t *testing.T) {
	s := New("corr-sec085")
	s.BindSubscription(ViewArchive, "sub-arch")

	if s.ArchiveStalled() {
		t.Fatal("ArchiveStalled() = true before any archive patch")
	}

	// The very first archive patch is already over the wire ceiling.
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: oversizedArchivePatch()})

	if !s.ArchiveStalled() {
		t.Fatalf("ArchiveStalled() = false after a cold-start oversized archive patch — the freeze was not surfaced (SEC-085)")
	}
	_, have := s.Archive()
	if have {
		t.Errorf("Archive() have=true after a cold-start oversized patch, want no applied archive")
	}

	buf := core.NewBuffer(80, 1)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 1}
	RenderArchive(buf, rect, nil, have, s.SearchActive(), s.ArchiveStalled(), s.SearchMatchedCount(), tcell.StyleDefault)
	if got := rowText(buf, 0); got != archiveStoppedText {
		t.Errorf("cold-start stalled render row 0 = %q, want %q (SEC-085: stalled banner, not %q)", got, archiveStoppedText, loadingArchiveText)
	}
}
