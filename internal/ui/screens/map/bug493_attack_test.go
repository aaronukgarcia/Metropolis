package mapscreen

import (
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// BUG-493: rough edges found in BUG-490's rejected round (worktree
// agent-a284d68d71c24f795, never landed) — the build-notice code itself
// (MapScreen.ApplyResult/BuildNotice, render.go's drawBuildNotice) is a
// real, kept improvement, but five gaps in it needed closing before this
// item's own commit:
//
//  1. the notice is unreadable at real terminal widths (measured live
//     strings 121/179 chars on one unwrapped row);
//  2. a BUG-267-class double-wrap duplicates error codes and the
//     correlation ID on the KindBuy path;
//  3. the notice never expires;
//  4. a rejection with Error==nil silently clears the notice with no log;
//  5. sec020_test.go's method-count comment (fixed directly in that file,
//     see its own updated enumeration + the exercise of ApplyDelta/
//     BindSubscription/UnbindSubscription in TestSEC020_*).
//
// This file proves 1-4.

func rowText(buf *core.Buffer, y, w int) string {
	runes := make([]rune, 0, w)
	for x := 0; x < w; x++ {
		runes = append(runes, buf.Get(x, y).Rune)
	}
	return string(runes)
}

func containsBuildRejected(s string) bool {
	return strings.Contains(s, "Build rejected:")
}

func newBug493NoticeScreen(t *testing.T, name string, w, h int) *MapScreen {
	t.Helper()
	m := NewMapScreen(name, widgets.DefaultPalette)
	m.ApplyPatch(fullPatchRaw(t))
	m.SetViewportSize(w, h)
	return m
}

// --- item 1: readable at real terminal widths --------------------------

// TestBug493_WideNotice_ReasonSurvivesTruncation_At80Cols is the RED->GREEN
// proof for item 1. The notice is built to mirror the real engine shape
// this bug report measured (leading "[CODE] [CODE]" chrome plus a
// duplicated trailing correlation suffix) but with a short, distinctive
// REASON token padded out with chrome to exceed 150 chars — chosen so the
// reason itself easily fits even a narrow terminal, isolating "does
// truncation eat the chrome or the reason" from "is the reason itself too
// long to ever fit" (a separate, unavoidable failure mode this test does
// not claim to solve).
func TestBug493_WideNotice_ReasonSurvivesTruncation_At80Cols(t *testing.T) {
	const reason = "REASON-TOKEN-XYZ"
	notice := "[MET-G804] [MET-E404] [MET-X001] [MET-X002] [MET-X003] [MET-X004] [MET-X005] " +
		reason +
		" (correlation: 11111111-1111-1111-1111-111111111111) (correlation: 11111111-1111-1111-1111-111111111111)"
	if len(notice) < 150 {
		t.Fatalf("test fixture too short: %d chars, want >= 150", len(notice))
	}

	const width = 80
	m := newBug493NoticeScreen(t, "bug493-wide-80", width, 10)
	m.ApplyResult(protocol.CommandResult{
		Accepted: false,
		Error:    &protocol.ErrorRef{Code: "MET-G804", Display: notice},
	})

	buf := core.NewBuffer(width, 10)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: width, H: 10})
	visible := rowText(buf, 0, width)
	t.Logf("80-col visible row: %q", visible)

	if !strings.Contains(visible, reason) {
		t.Fatalf("RED: reason token %q did not survive truncation at %d columns: %q", reason, width, visible)
	}
	if got := rowText(buf, 1, width); strings.Contains(got, "Build rejected") {
		t.Fatalf("the notice bled onto row 1: %q — drawBuildNotice must stay on one row", got)
	}
}

// TestBug493_WideNotice_ReasonSurvivesTruncation_At40Cols is the same
// proof at a much narrower width — the classic "even worse" case BUG-493
// names explicitly.
func TestBug493_WideNotice_ReasonSurvivesTruncation_At40Cols(t *testing.T) {
	const reason = "REASON-TOKEN-XYZ"
	notice := "[MET-G804] [MET-E404] [MET-X001] [MET-X002] [MET-X003] [MET-X004] [MET-X005] " +
		reason +
		" (correlation: 11111111-1111-1111-1111-111111111111) (correlation: 11111111-1111-1111-1111-111111111111)"
	if len(notice) < 150 {
		t.Fatalf("test fixture too short: %d chars, want >= 150", len(notice))
	}

	const width = 40
	m := newBug493NoticeScreen(t, "bug493-wide-40", width, 10)
	m.ApplyResult(protocol.CommandResult{
		Accepted: false,
		Error:    &protocol.ErrorRef{Code: "MET-G804", Display: notice},
	})

	buf := core.NewBuffer(width, 10)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: width, H: 10})
	visible := rowText(buf, 0, width)
	t.Logf("40-col visible row: %q", visible)

	if !strings.Contains(visible, reason) {
		t.Fatalf("RED: reason token %q did not survive truncation at %d columns: %q", reason, width, visible)
	}
	if got := rowText(buf, 1, width); strings.Contains(got, "Build rejected") {
		t.Fatalf("the notice bled onto row 1: %q", got)
	}
}

// TestBug493_RealMeasuredStrings_ReasonWordSurvivesAt80Cols replays the
// TWO exact strings BUG-493's own report measured against a live engine
// (121 and 179 chars) and proves the reported cut-mid-word failure
// ("...PurchaseTile rejected for tile {15 15}: a") no longer happens at
// 80 columns: stripping the leading bracket-code chrome and the
// deduplicated correlation suffix frees enough room for the WHOLE human
// reason to fit.
func TestBug493_RealMeasuredStrings_ReasonWordSurvivesAt80Cols(t *testing.T) {
	const width = 80
	cases := []struct {
		name   string
		notice string
		reason string
	}{
		{
			name:   "not-owned",
			notice: "[MET-G503] cell (tile {15 15}, local {11 11}) is not owned by owner 1 (correlation: 8a0d39ed-1054-49e6-bc19-97b153de5c66)",
			reason: "is not owned by owner 1",
		},
		{
			name:   "already-owned-double-wrap",
			notice: "[MET-G804] [MET-E404] PurchaseTile rejected for tile {15 15}: already owned (correlation: 9dfedd1a-7112-4780-a382-06636d56a8fc) (correlation: 9dfedd1a-7112-4780-a382-06636d56a8fc)",
			reason: "already owned",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newBug493NoticeScreen(t, "bug493-real-"+c.name, width, 10)
			m.ApplyResult(protocol.CommandResult{Accepted: false, Error: &protocol.ErrorRef{Code: "MET-G503", Display: c.notice}})
			buf := core.NewBuffer(width, 10)
			m.Render(buf, core.Rect{X: 0, Y: 0, W: width, H: 10})
			visible := rowText(buf, 0, width)
			t.Logf("%d-col visible row: %q", width, visible)
			if !strings.Contains(visible, c.reason) {
				t.Fatalf("RED: reason %q did not survive at %d columns: %q", c.reason, width, visible)
			}
		})
	}
}

// TestBug493_ZeroWidthAndNegativeRect_NoPanic is the boundary attack on
// drawBuildNotice's own clamping, carried over from the rejected round.
func TestBug493_ZeroWidthAndNegativeRect_NoPanic(t *testing.T) {
	m := newBug493NoticeScreen(t, "bug493-bounds", 10, 4)
	m.ApplyResult(protocol.CommandResult{
		Accepted: false,
		Error:    &protocol.ErrorRef{Code: "MET-G503", Display: "[MET-G503] cell is not owned (correlation: x)"},
	})
	for _, r := range []core.Rect{
		{X: 0, Y: 0, W: 0, H: 0},
		{X: 0, Y: 0, W: 1, H: 1},
		{X: 0, Y: 0, W: -5, H: -5},
		{X: 5, Y: 5, W: 3, H: 3},
	} {
		buf := core.NewBuffer(10, 10)
		m.Render(buf, r) // must not panic or write out of bounds
	}
}

// --- item 2: BUG-267-class double-wrap deduplication --------------------

// TestBug493_DoubleWrapCorrelation_DedupedToOne is the RED->GREEN proof
// for item 2: three structurally different rejection shapes (single code,
// double code, and a synthetic triple-nested wrap) each end up with
// EXACTLY ONE "(correlation:" occurrence in BuildNotice() after
// ApplyResult, never zero (that would be losing real debug information)
// and never more than one (BUG-267's own defect).
func TestBug493_DoubleWrapCorrelation_DedupedToOne(t *testing.T) {
	const marker = "(correlation:"
	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "single-code-single-correlation",
			input: "[MET-G503] cell is not owned (correlation: 8a0d39ed-1054-49e6-bc19-97b153de5c66)",
		},
		{
			name:  "double-code-doubled-correlation",
			input: "[MET-G804] [MET-E404] PurchaseTile rejected for tile {15 15}: already owned (correlation: 9dfedd1a-7112-4780-a382-06636d56a8fc) (correlation: 9dfedd1a-7112-4780-a382-06636d56a8fc)",
		},
		{
			name:  "triple-nested-wrap",
			input: "[MET-H505] [MET-G804] [MET-E404] op Zone failed (correlation: aaaaaaaa-1111-1111-1111-111111111111) (correlation: bbbbbbbb-2222-2222-2222-222222222222) (correlation: cccccccc-3333-3333-3333-333333333333)",
		},
		{
			name:  "no-correlation-at-all",
			input: "[MET-B040] tile is not owned",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := NewMapScreen("bug493-dedupe-"+c.name, widgets.DefaultPalette)
			m.ApplyResult(protocol.CommandResult{Accepted: false, Error: &protocol.ErrorRef{Code: "MET-XXXX", Display: c.input}})
			got := m.BuildNotice()
			count := strings.Count(got, marker)
			wantHasCorrelation := strings.Contains(c.input, marker)
			wantCount := 0
			if wantHasCorrelation {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatalf("RED: BuildNotice() = %q has %d %q occurrences, want exactly %d", got, count, marker, wantCount)
			}
		})
	}
}

// TestBug493_DedupeCorrelationSuffix_UnitLevel exercises the helper
// directly, proving the FIRST correlation ID (not an arbitrary one) is
// the one kept — every real double-wrap shape carries the same ID
// repeated, so this is mostly a determinism/behaviour-pinning check.
func TestBug493_DedupeCorrelationSuffix_UnitLevel(t *testing.T) {
	in := "msg (correlation: FIRST) (correlation: SECOND)"
	want := "msg (correlation: FIRST)"
	if got := dedupeCorrelationSuffix(in); got != want {
		t.Fatalf("dedupeCorrelationSuffix(%q) = %q, want %q", in, got, want)
	}
	// Unchanged when there is nothing to dedupe.
	single := "msg (correlation: ONLY)"
	if got := dedupeCorrelationSuffix(single); got != single {
		t.Fatalf("dedupeCorrelationSuffix(%q) = %q, want unchanged", single, got)
	}
	plain := "msg with no correlation at all"
	if got := dedupeCorrelationSuffix(plain); got != plain {
		t.Fatalf("dedupeCorrelationSuffix(%q) = %q, want unchanged", plain, got)
	}
}

// --- item 3: explicit dismiss, never a spontaneous expiry ----------------

// TestBug493_Notice_PersistsAcrossUnrelatedActivity_UntilDismissed proves
// two things at once: the notice is NOT cleared by any activity other
// than a subsequent command result or an explicit dismiss (still true,
// and still correct per ApplyResult's contract — a spontaneous clear
// would be its own bug), and DismissBuildNotice DOES clear it on request
// (BUG-493 item 3's fix).
func TestBug493_Notice_PersistsAcrossUnrelatedActivity_UntilDismissed(t *testing.T) {
	const width = 60
	m := newBug493NoticeScreen(t, "bug493-dismiss", width, 8)
	m.ApplyResult(protocol.CommandResult{
		Accepted: false,
		Error:    &protocol.ErrorRef{Code: "MET-G503", Display: "[MET-G503] cell is not owned (correlation: x)"},
	})
	if m.BuildNotice() == "" {
		t.Fatal("precondition: no notice set")
	}

	// Everything a player might plausibly do that is NOT another build
	// command and NOT the dismiss key.
	for i := 0; i < 100; i++ {
		m.Pan(1, 0)
		m.MoveCursor(1, 1)
		m.CycleOverlay(true)
		m.SetStale(i%2 == 0)
		m.ApplyPatch(fullPatchRaw(t))
		buf := core.NewBuffer(width, 8)
		m.Render(buf, core.Rect{X: 0, Y: 0, W: width, H: 8})
	}
	if got := m.BuildNotice(); got == "" {
		t.Fatal("notice cleared by unrelated activity — that would contradict ApplyResult's documented contract")
	}

	// The dismiss key (DismissBuildNotice) DOES clear it.
	m.DismissBuildNotice()
	if got := m.BuildNotice(); got != "" {
		t.Fatalf("RED: BuildNotice() after DismissBuildNotice = %q, want \"\" (item 3 fix)", got)
	}
	buf := core.NewBuffer(width, 8)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: width, H: 8})
	if containsBuildRejected(rowText(buf, 0, width)) {
		t.Fatal("Render() after DismissBuildNotice still shows a build notice")
	}

	// Dismissing an already-empty notice is a no-op, not a panic.
	m.DismissBuildNotice()
	if got := m.BuildNotice(); got != "" {
		t.Fatalf("DismissBuildNotice on an already-empty notice = %q, want \"\"", got)
	}
}

// --- item 4: nil-Error rejection is logged, not silent ------------------

// TestBug493_RejectedWithNilError_ClearsAndLogs is the RED->GREEN proof
// for item 4: a REJECTED CommandResult with Error == nil still clears the
// notice (a malformed result showing a possibly-stale rejection forever
// would be its own bug), but — unlike before this fix — leaves a
// registry-sourced MET-U102 trail rather than looking identical to an
// ordinary Accept.
func TestBug493_RejectedWithNilError_ClearsAndLogs(t *testing.T) {
	m := newBug493NoticeScreen(t, "bug493-nilerr", 40, 6)
	m.ApplyResult(protocol.CommandResult{
		Accepted: false,
		Error:    &protocol.ErrorRef{Code: "MET-G503", Display: "[MET-G503] cell is not owned (correlation: x)"},
	})
	if m.BuildNotice() == "" {
		t.Fatal("precondition: no notice set")
	}

	before := len(errs.Recent())

	// A REJECTION carrying no ErrorRef — protocol-malformed
	// (ErrRejectedResultMissingError's own contract), but nothing on this
	// delivery path enforces that.
	m.ApplyResult(protocol.CommandResult{Accepted: false, Error: nil})

	if got := m.BuildNotice(); got != "" {
		t.Fatalf("BuildNotice() after a nil-Error rejection = %q, want \"\" (cleared, not left stale)", got)
	}

	found := false
	for _, e := range errs.Recent() {
		if e.Code == "MET-U102" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("RED: no MET-U102 entry found in errs.Recent() (had %d entries before, %d after) after a rejected result with Error==nil — this is exactly the silent-clear gap BUG-493 item 4 closes", before, len(errs.Recent()))
	}
}

// TestBug493_EmptyDisplay_RendersNothing: a rejection whose registry
// Display resolved to "" produces no notice at all — carried over from
// the rejected round's characterisation, still true after this item's
// fixes (an empty Display has nothing for dedupeCorrelationSuffix/
// drawBuildNotice to do anything useful with).
func TestBug493_EmptyDisplay_RendersNothing(t *testing.T) {
	const width = 40
	m := newBug493NoticeScreen(t, "bug493-empty", width, 6)
	m.ApplyResult(protocol.CommandResult{
		Accepted: false,
		Error:    &protocol.ErrorRef{Code: "MET-XXXX", Display: ""},
	})
	buf := core.NewBuffer(width, 6)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: width, H: 6})
	if containsBuildRejected(rowText(buf, 0, width)) {
		t.Fatal("an empty Display somehow drew a notice")
	}
}

// --- baseline regression (BUG-490's own contract, still true) -----------

// TestBug493_RejectedBuildResult_RendersVisibleNotice_AcceptClears mirrors
// the original BUG-490 shipped test: a rejection renders, and a later
// accept clears it. Kept here (rather than assuming the original file)
// since BUG-493 is this fix's own commit.
func TestBug493_RejectedBuildResult_RendersVisibleNotice_AcceptClears(t *testing.T) {
	m := NewMapScreen("bug493-baseline", widgets.DefaultPalette)
	m.ApplyPatch(fullPatchRaw(t))
	m.SetViewportSize(40, 5)

	buf := core.NewBuffer(40, 5)
	rect := core.Rect{X: 0, Y: 0, W: 40, H: 5}

	m.Render(buf, rect)
	if got := rowText(buf, 0, 40); containsBuildRejected(got) {
		t.Fatalf("Render() before any ApplyResult already shows a build notice: %q", got)
	}

	m.ApplyResult(protocol.CommandResult{
		CorrelationID: "corr-1",
		Accepted:      false,
		Error:         &protocol.ErrorRef{Code: "MET-B040", Display: "tile is not owned"},
	})
	if got := m.BuildNotice(); got != "tile is not owned" {
		t.Fatalf("BuildNotice() after a rejection = %q, want %q", got, "tile is not owned")
	}

	buf2 := core.NewBuffer(40, 5)
	m.Render(buf2, rect)
	got := rowText(buf2, 0, 40)
	want := "Build rejected: tile is not owned"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("Render() row0 after a rejected build = %q, want it to start with %q", got, want)
	}

	m.ApplyResult(protocol.CommandResult{CorrelationID: "corr-2", Accepted: true})
	if got := m.BuildNotice(); got != "" {
		t.Fatalf("BuildNotice() after a later Accept = %q, want \"\" (a stale rejection must not linger)", got)
	}
	buf3 := core.NewBuffer(40, 5)
	m.Render(buf3, rect)
	if got := rowText(buf3, 0, 40); containsBuildRejected(got) {
		t.Fatalf("Render() after Accept still shows a build notice: %q", got)
	}
}

// =======================================================================
// Independent destructive round r1 (Opus, GR#23 — NOT the author).
// Everything below this banner was written by the attacker, not the
// builder, and is kept as a permanent regression surface.
// =======================================================================

// TestBug493R1_NoticePersistsAcrossEveryOtherExportedMutator is the
// hardened form of item 3's persistence claim: the builder's own test
// pans and issues one unrelated command; this one drives EVERY other
// exported *MapScreen mutator (and 50 repeat Renders) and proves not one
// of them clears the notice. The only things that may clear it are the
// two documented ones — an Accept and DismissBuildNotice — plus the
// deliberately-malformed nil-Error rejection (item 4), each asserted
// separately elsewhere in this file.
func TestBug493R1_NoticePersistsAcrossEveryOtherExportedMutator(t *testing.T) {
	const disp = "[MET-G503] STICKY-REASON (correlation: sticky-id)"
	steps := []struct {
		name string
		fn   func(t *testing.T, m *MapScreen)
	}{
		{"Pan", func(_ *testing.T, m *MapScreen) { m.Pan(3, 3) }},
		{"MoveCursor", func(_ *testing.T, m *MapScreen) { m.MoveCursor(2, 2) }},
		{"CycleOverlay", func(_ *testing.T, m *MapScreen) { m.CycleOverlay(true) }},
		{"SetStale", func(_ *testing.T, m *MapScreen) { m.SetStale(true) }},
		{"SetViewportSize", func(_ *testing.T, m *MapScreen) { m.SetViewportSize(60, 12) }},
		{"ApplyPatch", func(t *testing.T, m *MapScreen) { m.ApplyPatch(fullPatchRaw(t)) }},
		{"BindUnbindSubscription", func(_ *testing.T, m *MapScreen) {
			m.BindSubscription(protocol.SubscriptionID("r1-sub"))
			m.UnbindSubscription(protocol.SubscriptionID("r1-sub"))
		}},
		{"ApplyDelta", func(t *testing.T, m *MapScreen) {
			m.BindSubscription(protocol.SubscriptionID("r1-sub2"))
			m.ApplyDelta(protocol.Delta{SubscriptionID: protocol.SubscriptionID("r1-sub2"), Patch: fullPatchRaw(t)})
		}},
		{"Render50x", func(_ *testing.T, m *MapScreen) {
			buf := core.NewBuffer(80, 10)
			for i := 0; i < 50; i++ {
				m.Render(buf, core.Rect{X: 0, Y: 0, W: 80, H: 10})
			}
		}},
		{"ReadOnlyAccessors", func(_ *testing.T, m *MapScreen) {
			_ = m.Inspect(1, 1)
			_ = m.InspectCursor()
			_, _ = m.Offset()
			_, _ = m.CursorPos()
			_ = m.ActiveOverlay()
		}},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			m := newBug493NoticeScreen(t, "bug493r1-"+s.name, 80, 10)
			m.ApplyResult(protocol.CommandResult{
				Accepted: false,
				Error:    &protocol.ErrorRef{Code: "MET-G503", Display: disp},
			})
			s.fn(t, m)
			if got := m.BuildNotice(); got != disp {
				t.Fatalf("%s cleared the build notice: BuildNotice() = %q, want it still %q — only an Accept, DismissBuildNotice, or a malformed nil-Error rejection may clear it", s.name, got, disp)
			}
		})
	}
}

// TestBug493R1_DismissDoesNotWedgeTheNotice covers the failure mode a
// dismiss mechanism can introduce that a "does it clear?" test cannot
// see — a screen that, once dismissed, never shows a notice AGAIN.
// Repeated, idempotent dismisses must not latch anything off.
func TestBug493R1_DismissDoesNotWedgeTheNotice(t *testing.T) {
	m := newBug493NoticeScreen(t, "bug493r1-wedge", 80, 10)
	for round, want := range []string{"first", "second", "third"} {
		m.ApplyResult(protocol.CommandResult{
			Accepted: false,
			Error:    &protocol.ErrorRef{Code: "MET-G503", Display: want},
		})
		if got := m.BuildNotice(); got != want {
			t.Fatalf("round %d: BuildNotice() = %q, want %q — a previous dismiss wedged the notice off", round, got, want)
		}
		m.DismissBuildNotice()
		m.DismissBuildNotice()
		m.DismissBuildNotice()
		if got := m.BuildNotice(); got != "" {
			t.Fatalf("round %d: BuildNotice() after dismiss = %q, want empty", round, got)
		}
	}
}

// TestBug493R1_ConcurrentResultsDismissAndRender_NoRaceNoWedge storms the
// screen from 8 goroutines with rejections, accepts, malformed nil-Error
// rejections, dismisses, reads and renders, then proves the screen is
// still USABLE afterwards — a race-detector run alone would not catch a
// logical wedge. Run under -race in CI.
func TestBug493R1_ConcurrentResultsDismissAndRender_NoRaceNoWedge(t *testing.T) {
	m := newBug493NoticeScreen(t, "bug493r1-race", 80, 10)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			buf := core.NewBuffer(80, 10)
			for i := 0; i < 400; i++ {
				switch (g + i) % 6 {
				case 0:
					m.ApplyResult(protocol.CommandResult{
						Accepted: false,
						Error:    &protocol.ErrorRef{Code: "MET-G804", Display: "[A] [B] reason (correlation: q) (correlation: q)"},
					})
				case 1:
					m.DismissBuildNotice()
				case 2:
					m.Render(buf, core.Rect{X: 0, Y: 0, W: 80, H: 10})
				case 3:
					m.ApplyResult(protocol.CommandResult{Accepted: true})
				case 4:
					m.ApplyResult(protocol.CommandResult{Accepted: false, Error: nil})
				case 5:
					_ = m.BuildNotice()
					m.Pan(1, 0)
				}
			}
		}(g)
	}
	wg.Wait()

	m.DismissBuildNotice()
	m.ApplyResult(protocol.CommandResult{
		Accepted: false,
		Error:    &protocol.ErrorRef{Code: "MET-G503", Display: "post-storm"},
	})
	if got := m.BuildNotice(); got != "post-storm" {
		t.Fatalf("WEDGE after the concurrency storm: BuildNotice() = %q, want %q", got, "post-storm")
	}
}

// TestBug493R1_DedupeCorrelation_HostileAndNonTrailingShapes extends item
// 2 beyond the builder's three well-formed shapes. It asserts the one
// property that genuinely holds for every shape — at most one
// "(correlation:" survives, and zero survive only if there were none to
// begin with — and then CHARACTERISES, deliberately, the two limits this
// round found. Both are latent, not live: no data/errors.json message
// places any text after its {cause} token today, so every real
// correlation segment a Display can carry is trailing.
//
//	L1 — dedupeCorrelationSuffix's regexp is UNANCHORED while its own doc
//	     comment says "trailing", so a "(correlation: ...)" occurring
//	     mid-text is stripped too, and the id that survives is the FIRST
//	     match rather than the real (last) one.
//	L2 — a Display quoting a correlation id inside its human sentence
//	     loses that quotation.
//
// These arms assert CURRENT behaviour on purpose: if someone anchors the
// regexp (the one-line fix), this test fails loudly and points at the
// follow-up rather than letting the change land unseen.
func TestBug493R1_DedupeCorrelation_HostileAndNonTrailingShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "no-correlation-at-all",
			in:   "[MET-G503] plain reason with no correlation segment",
			want: "[MET-G503] plain reason with no correlation segment",
		},
		{
			name: "four-deep-nested-wrap",
			in:   "[MET-A] [MET-B] [MET-C] [MET-D] deeply wrapped reason (correlation: R) (correlation: R) (correlation: R) (correlation: R)",
			want: "[MET-A] [MET-B] [MET-C] [MET-D] deeply wrapped reason (correlation: R)",
		},
		{
			name: "differing-ids-keeps-first",
			in:   "[MET-A] [MET-B] reason (correlation: INNER) (correlation: OUTER)",
			want: "[MET-A] [MET-B] reason (correlation: INNER)",
		},
		{
			name: "hostile-fake-id-em",
			in:   "[MET-G503] the tile label (correlation: fake-id) was rejected (correlation: REAL-ID)",
			want: "[MET-G503] the tile label was rejected (correlation: fake-id)",
		},
		{
			name: "same-id-quoted-in",
			in:   "[MET-G503] see log entry (correlation: REAL-ID) for detail (correlation: REAL-ID)",
			want: "[MET-G503] see log entry for detail (correlation: REAL-ID)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := dedupeCorrelationSuffix(c.in)
			if got != c.want {
				t.Fatalf("dedupeCorrelationSuffix(%q)\n got  %q\n want %q", c.in, got, c.want)
			}
			if n := strings.Count(got, "(correlation:"); n > 1 {
				t.Fatalf("BUG-267 class survived: %d correlation segments in %q", n, got)
			}
			if strings.Contains(c.in, "(correlation:") && !strings.Contains(got, "(correlation:") {
				t.Fatalf("dedupe stripped the correlation id to ZERO — real debug information lost: %q", got)
			}
		})
	}
}

// TestBug493R1_ReadabilityFloor_RealMeasuredStrings pins the HONEST width
// floor of item 1's fix using the two strings BUG-493 actually measured
// against a live engine. drawBuildNotice does not wrap — it draws one row
// and ellipsis-truncates — so "the reason survives" holds only while
// prefix+reason fits rect.W. That is comfortably satisfied at the 80
// columns a real terminal has (the reported defect) and NOT satisfied at
// 40 for either real string: the builder's own At40Cols test passes only
// because its fixture's reason is a short synthetic token sitting behind
// 77 characters of pure bracket chrome. Filed as a follow-up (wrap onto
// the second row — rect.H is >= 2 in every real layout).
func TestBug493R1_ReadabilityFloor_RealMeasuredStrings(t *testing.T) {
	cases := []struct {
		name    string
		notice  string
		reason  string
		floorOK []int
		floorNo []int
	}{
		{
			name:    "not-owned-121ch",
			notice:  "[MET-G503] cell (tile {15 15}, local {11 11}) is not owned by owner 1 (correlation: 8a0d39ed-1054-49e6-bc19-97b153de5c66)",
			reason:  "is not owned by owner 1",
			floorOK: []int{120, 100, 81, 80, 79},
			floorNo: []int{41, 40, 39},
		},
		{
			name:    "already-owned-179ch-double-wrapped",
			notice:  "[MET-G804] [MET-E404] PurchaseTile rejected for tile {15 15}: already owned (correlation: 9dfedd1a-7112-4780-a382-06636d56a8fc) (correlation: 9dfedd1a-7112-4780-a382-06636d56a8fc)",
			reason:  "already owned",
			floorOK: []int{120, 100, 81, 80, 79},
			floorNo: []int{41, 40, 39},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, w := range c.floorOK {
				m := newBug493NoticeScreen(t, "bug493r1-ok", w, 10)
				m.ApplyResult(protocol.CommandResult{Accepted: false, Error: &protocol.ErrorRef{Code: "MET-G503", Display: c.notice}})
				buf := core.NewBuffer(w, 10)
				m.Render(buf, core.Rect{X: 0, Y: 0, W: w, H: 10})
				row := rowText(buf, 0, w)
				if !strings.Contains(row, c.reason) {
					t.Fatalf("REGRESSION: reason %q lost at %d cols: %q", c.reason, w, row)
				}
				if got := rowText(buf, 1, w); containsBuildRejected(got) {
					t.Fatalf("notice bled onto row 1 at %d cols: %q", w, got)
				}
			}
			for _, w := range c.floorNo {
				m := newBug493NoticeScreen(t, "bug493r1-no", w, 10)
				m.ApplyResult(protocol.CommandResult{Accepted: false, Error: &protocol.ErrorRef{Code: "MET-G503", Display: c.notice}})
				buf := core.NewBuffer(w, 10)
				m.Render(buf, core.Rect{X: 0, Y: 0, W: w, H: 10})
				row := rowText(buf, 0, w)
				if strings.Contains(row, c.reason) {
					t.Fatalf("the documented %d-col limit no longer holds (wrapping added?) — retire this arm and its follow-up: %q", w, row)
				}
				if len([]rune(strings.TrimRight(row, " "))) > w {
					t.Fatalf("notice overflowed rect.W=%d: %q", w, row)
				}
			}
		})
	}
}

// TestBug493R1_EmptyDisplayRejection_SilentlyClearsWithNoLog is the
// sibling of item 4 this round found still open: a REJECTED result whose
// ErrorRef is present but whose Display is "" clears the notice with NO
// registry trace at all — from the player's seat identical to an Accept,
// which is exactly the BUG-490 defect (a rejected build with no visible
// feedback) reachable through a narrower hole than the nil-Error one item
// 4 closed. Asserted as CURRENT behaviour and filed as a follow-up: when
// the empty-Display arm gains its own registry warn, this test fails and
// points at it.
func TestBug493R1_EmptyDisplayRejection_SilentlyClearsWithNoLog(t *testing.T) {
	m := newBug493NoticeScreen(t, "bug493r1-emptydisp", 40, 6)
	m.ApplyResult(protocol.CommandResult{
		Accepted: false,
		Error:    &protocol.ErrorRef{Code: "MET-G503", Display: "[MET-G503] a real reason (correlation: x)"},
	})
	if m.BuildNotice() == "" {
		t.Fatal("precondition: no notice set")
	}

	before := len(errs.Recent())
	m.ApplyResult(protocol.CommandResult{Accepted: false, Error: &protocol.ErrorRef{Code: "MET-G503", Display: ""}})

	if got := m.BuildNotice(); got != "" {
		t.Fatalf("BuildNotice() after an empty-Display rejection = %q, want empty", got)
	}
	if len(errs.Recent()) != before {
		t.Fatalf("an empty-Display rejection now logs (%d -> %d entries) — the documented gap is closed, retire this characterisation test and its follow-up", before, len(errs.Recent()))
	}
}

// TestBug493R1_NilErrorRejections_LogWithRenderedMessage_NoFlood proves
// item 4's log is a REAL registry entry (message fully interpolated — no
// literal "{token}", the BUG-357 class) and that repeated malformed
// results coalesce in the ring rather than flooding it.
func TestBug493R1_NilErrorRejections_LogWithRenderedMessage_NoFlood(t *testing.T) {
	m := newBug493NoticeScreen(t, "bug493r1-u102", 40, 6)
	before := len(errs.Recent())
	for i := 0; i < 25; i++ {
		m.ApplyResult(protocol.CommandResult{Accepted: false, Error: nil})
	}
	var found errs.Entry
	slots := 0
	for _, e := range errs.Recent() {
		if e.Code == "MET-U102" {
			slots++
			found = e
		}
	}
	if slots == 0 {
		t.Fatalf("no MET-U102 entry after 25 nil-Error rejections (ring had %d entries before)", before)
	}
	if strings.ContainsAny(found.Msg, "{}") {
		t.Fatalf("BUG-357 class: MET-U102 message left an unrendered token: %q", found.Msg)
	}
	if found.Msg == "" {
		t.Fatal("MET-U102 logged an empty message")
	}
	if slots > 1 {
		t.Fatalf("25 identical malformed results occupied %d ring slots — flooding the error ring, want them coalesced into 1", slots)
	}
}
