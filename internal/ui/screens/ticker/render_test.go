package ticker

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// rowText reconstructs row y of buf as a string (runes concatenated,
// trailing blanks trimmed) so a test can assert on a rendered line
// without duplicating the exact rune sequence.
func rowText(buf *core.Buffer, y int) string {
	w, _ := buf.Size()
	var b strings.Builder
	for x := 0; x < w; x++ {
		b.WriteRune(buf.Get(x, y).Rune)
	}
	return strings.TrimRight(b.String(), " ")
}

// bufferEqual reports whether two buffers are cell-for-cell identical.
func bufferEqual(a, b *core.Buffer) bool {
	aw, ah := a.Size()
	bw, bh := b.Size()
	if aw != bw || ah != bh {
		return false
	}
	for y := 0; y < ah; y++ {
		for x := 0; x < aw; x++ {
			if a.Get(x, y) != b.Get(x, y) {
				return false
			}
		}
	}
	return true
}

func TestRenderTicker_NoNewsFeed(t *testing.T) {
	buf := core.NewBuffer(40, 2)
	RenderTicker(buf, core.Rect{X: 0, Y: 0, W: 40, H: 2}, nil, 0, false, tcell.StyleDefault)
	if got := rowText(buf, 0); got != noNewsFeedText {
		t.Errorf("row 0 = %q, want %q (TIK-7)", got, noNewsFeedText)
	}
}

func TestRenderTicker_ScrollsThroughEvents(t *testing.T) {
	events := []Story{
		{EventID: "e1", Text: "one"},
		{EventID: "e2", Text: "two"},
		{EventID: "e3", Text: "three"},
	}
	buf := core.NewBuffer(20, 1)
	// Step 0 -> top is index 0 ("one"). Step 1 -> top is index 1 ("two").
	RenderTicker(buf, core.Rect{X: 0, Y: 0, W: 20, H: 1}, events, 0, true, tcell.StyleDefault)
	if got := rowText(buf, 0); got != "one" {
		t.Errorf("step 0 row = %q, want %q", got, "one")
	}
	RenderTicker(buf, core.Rect{X: 0, Y: 0, W: 20, H: 1}, events, 1, true, tcell.StyleDefault)
	if got := rowText(buf, 0); got != "two" {
		t.Errorf("step 1 row = %q, want %q", got, "two")
	}
}

func TestRenderArchive_States(t *testing.T) {
	rect := core.Rect{X: 0, Y: 0, W: 40, H: 1}

	// Each state renders into a FRESH buffer: RenderArchive (like every
	// render function here) is a pure draw-into-the-buffer, not a
	// clear-and-redraw, so reusing one buffer across states would leave
	// stale cells from the previous state and make the assertion read
	// concatenated text.
	loading := core.NewBuffer(40, 1)
	RenderArchive(loading, rect, nil, false, false, false, 0, tcell.StyleDefault)
	if got := rowText(loading, 0); got != loadingArchiveText {
		t.Errorf("loading state = %q, want %q", got, loadingArchiveText)
	}

	noMatches := core.NewBuffer(40, 1)
	RenderArchive(noMatches, rect, nil, true, true, false, 0, tcell.StyleDefault)
	if got := rowText(noMatches, 0); got != noMatchesText {
		t.Errorf("no-matches state = %q, want %q", got, noMatchesText)
	}

	matches := core.NewBuffer(40, 1)
	RenderArchive(matches, rect, []Story{{EventID: "e", Text: "hit"}}, true, true, false, 1, tcell.StyleDefault)
	if got := rowText(matches, 0); got != "hit" {
		t.Errorf("matches state = %q, want %q", got, "hit")
	}

	stalled := core.NewBuffer(80, 1)
	RenderArchive(stalled, core.Rect{X: 0, Y: 0, W: 80, H: 1}, []Story{{EventID: "e", Text: "hit"}}, true, false, true, 0, tcell.StyleDefault)
	if got := rowText(stalled, 0); got != archiveStoppedText {
		t.Errorf("stalled state = %q, want %q (SEC-072)", got, archiveStoppedText)
	}
}

func TestRenderAnnual_NoBiggestStory(t *testing.T) {
	buf := core.NewBuffer(40, 4)
	review := AnnualReview{Year: 5, HasBiggest: false, Numbers: []AnnualNumber{{Label: "Deaths", Value: 3}}}
	RenderAnnual(buf, core.Rect{X: 0, Y: 0, W: 40, H: 4}, review, true, tcell.StyleDefault)
	if got := rowText(buf, 2); got != "no story this year" {
		t.Errorf("biggest-story row = %q, want %q", got, "no story this year")
	}
}

func TestRender_IdenticalInputsRenderIdentically(t *testing.T) {
	events := []Story{{EventID: "e1", Name: "Pent Lane", Text: "queue clears"}, {EventID: "e2", Text: "first graduate"}}
	bulletin := []BulletinStory{{Story: Story{EventID: "b1", Name: "Seabrook", Text: "gang taxes traders"}, Rank: 1}}
	annual := AnnualReview{Year: 1, Numbers: []AnnualNumber{{Label: "Deaths", Value: 9}}, BiggestStory: Story{EventID: "a1", Text: "big"}, HasBiggest: true}
	archive := []Story{{EventID: "e1", Text: "a"}, {EventID: "e2", Text: "b"}}
	rect := core.Rect{X: 0, Y: 0, W: 50, H: 6}

	render := func() *core.Buffer {
		buf := core.NewBuffer(50, 6)
		RenderTicker(buf, rect, events, 0, true, tcell.StyleDefault)
		RenderBulletin(buf, rect, 3, bulletin, true, tcell.StyleDefault)
		RenderAnnual(buf, rect, annual, true, tcell.StyleDefault)
		RenderArchive(buf, rect, archive, true, false, false, 0, tcell.StyleDefault)
		return buf
	}

	if !bufferEqual(render(), render()) {
		t.Fatal("render not deterministic: identical inputs produced different buffers (SF-8)")
	}
}

// sf3Fixture is the four-view wire state SF-3 drives through ApplyDelta.
// Each differential clones it, changes exactly one field of exactly one
// figure, and applies both runs through the real wire-decode path.
type sf3Fixture struct {
	ticker   wireTickerPatch
	bulletin wireBulletinPatch
	annual   wireAnnualPatch
	archive  wireArchivePatch
}

// sf3Base returns the fixed four-view fixture every differential starts
// from. Each figure carries its OWN field value, so mutating one figure's
// field never leaks into a sibling's (SF-3's isolation requirement).
func sf3Base() sf3Fixture {
	return sf3Fixture{
		ticker: wireTickerPatch{
			SchemaVersion: 1,
			Events:        []wireStory{{EventID: "e1", Name: "Pent Lane", Text: "queue clears"}},
		},
		bulletin: wireBulletinPatch{
			SchemaVersion: 1,
			Month:         3,
			Stories:       []wireBulletinStory{{wireStory: wireStory{EventID: "b1", Name: "Seabrook", Text: "gang taxes traders"}, Rank: 1}},
		},
		annual: wireAnnualPatch{
			SchemaVersion: 1,
			Year:          1,
			Numbers:       []wireAnnualNumber{{Label: "Deaths", Value: 9}},
		},
		archive: wireArchivePatch{
			SchemaVersion: 1,
			Stories:       []wireStory{{EventID: "a1", Name: "Pent Lane", Text: "archive record"}},
		},
	}
}

// sf3Clone returns a deep copy of f (slices re-allocated) so a mutation to
// one differential run never aliases the base fixture or the other run —
// sf3Fixture holds slices, and a shallow copy would share backing arrays,
// so the two "distinct" runs would collapse to whichever mutation ran last.
func sf3Clone(f sf3Fixture) sf3Fixture {
	f.ticker.Events = append([]wireStory(nil), f.ticker.Events...)
	f.bulletin.Stories = append([]wireBulletinStory(nil), f.bulletin.Stories...)
	f.annual.Numbers = append([]wireAnnualNumber(nil), f.annual.Numbers...)
	f.archive.Stories = append([]wireStory(nil), f.archive.Stories...)
	return f
}

// sf3Apply constructs a Screen, binds the four subscriptions, and applies
// f's four patches through ApplyDelta — the same route production Deltas
// take — so the differential exercises wire decode, not a hand-built
// Story slice handed straight to a render function.
func sf3Apply(t *testing.T, f sf3Fixture) *Screen {
	t.Helper()
	s := New("corr-sf3")
	s.BindSubscription(ViewTicker, "sub-tick")
	s.BindSubscription(ViewBulletin, "sub-bull")
	s.BindSubscription(ViewAnnual, "sub-ann")
	s.BindSubscription(ViewArchive, "sub-arch")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-tick", Patch: mustWire(t, f.ticker)})
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-bull", Patch: mustWire(t, f.bulletin)})
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-ann", Patch: mustWire(t, f.annual)})
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-arch", Patch: mustWire(t, f.archive)})
	return s
}

// sf3RenderOthers renders every figure EXCEPT skip into one buffer. It is
// the SF-3 (b) half: after mutating skip's field, the buffer holding only
// the unmutated siblings must be byte-identical between the two runs.
func sf3RenderOthers(s *Screen, rect core.Rect, skip string) *core.Buffer {
	buf := core.NewBuffer(60, 6)
	if skip != "ticker" {
		events, have := s.Ticker()
		RenderTicker(buf, rect, events, 0, have, tcell.StyleDefault)
	}
	if skip != "bulletin" {
		stories, month, have := s.Bulletin()
		RenderBulletin(buf, rect, month, stories, have, tcell.StyleDefault)
	}
	if skip != "annual" {
		annual, have := s.Annual()
		RenderAnnual(buf, rect, annual, have, tcell.StyleDefault)
	}
	if skip != "archive" {
		archive, have := s.Archive()
		RenderArchive(buf, rect, archive, have, false, false, 0, tcell.StyleDefault)
	}
	return buf
}

// TestRender_SF3_SingleFieldMutation is SF-3's differential shape, driven
// through ApplyDelta (not direct render calls): for each figure, two full
// delta sequences differ in exactly that figure's one field, and the
// bound figure's render output must differ (a) while every OTHER figure's
// render output is byte-identical (b) — proving each figure is wired to
// its source field through the wire decode, not hardcoded or cross-wired
// to a sibling's JSON field.
func TestRender_SF3_SingleFieldMutation(t *testing.T) {
	rect := core.Rect{X: 0, Y: 0, W: 60, H: 6}
	base := sf3Base()

	cases := []struct {
		name string
		// mutate returns the A/B fixtures, differing in exactly one field
		// of exactly one figure.
		mutate func(f sf3Fixture) (a, b sf3Fixture)
		// renderFigure renders ONLY the mutated figure.
		renderFigure func(s *Screen) *core.Buffer
		// renderOthers renders every figure EXCEPT the mutated one.
		renderOthers func(s *Screen) *core.Buffer
	}{
		{
			name: "ticker.text",
			mutate: func(f sf3Fixture) (sf3Fixture, sf3Fixture) {
				a := sf3Clone(f)
				b := sf3Clone(f)
				a.ticker.Events[0].Text = "queue clears"
				b.ticker.Events[0].Text = "queue blocked"
				return a, b
			},
			renderFigure: func(s *Screen) *core.Buffer {
				events, have := s.Ticker()
				buf := core.NewBuffer(60, 6)
				RenderTicker(buf, rect, events, 0, have, tcell.StyleDefault)
				return buf
			},
			renderOthers: func(s *Screen) *core.Buffer { return sf3RenderOthers(s, rect, "ticker") },
		},
		{
			name: "bulletin.text",
			mutate: func(f sf3Fixture) (sf3Fixture, sf3Fixture) {
				a := sf3Clone(f)
				b := sf3Clone(f)
				a.bulletin.Stories[0].Text = "gang taxes traders"
				b.bulletin.Stories[0].Text = "gang dispersed"
				return a, b
			},
			renderFigure: func(s *Screen) *core.Buffer {
				stories, month, have := s.Bulletin()
				buf := core.NewBuffer(60, 6)
				RenderBulletin(buf, rect, month, stories, have, tcell.StyleDefault)
				return buf
			},
			renderOthers: func(s *Screen) *core.Buffer { return sf3RenderOthers(s, rect, "bulletin") },
		},
		{
			name: "annual.number.value",
			mutate: func(f sf3Fixture) (sf3Fixture, sf3Fixture) {
				a := sf3Clone(f)
				b := sf3Clone(f)
				a.annual.Numbers[0].Value = 9
				b.annual.Numbers[0].Value = 10
				return a, b
			},
			renderFigure: func(s *Screen) *core.Buffer {
				annual, have := s.Annual()
				buf := core.NewBuffer(60, 6)
				RenderAnnual(buf, rect, annual, have, tcell.StyleDefault)
				return buf
			},
			renderOthers: func(s *Screen) *core.Buffer { return sf3RenderOthers(s, rect, "annual") },
		},
		{
			name: "archive.text",
			mutate: func(f sf3Fixture) (sf3Fixture, sf3Fixture) {
				a := sf3Clone(f)
				b := sf3Clone(f)
				a.archive.Stories[0].Text = "archive record"
				b.archive.Stories[0].Text = "archive amended"
				return a, b
			},
			renderFigure: func(s *Screen) *core.Buffer {
				archive, have := s.Archive()
				buf := core.NewBuffer(60, 6)
				RenderArchive(buf, rect, archive, have, false, false, 0, tcell.StyleDefault)
				return buf
			},
			renderOthers: func(s *Screen) *core.Buffer { return sf3RenderOthers(s, rect, "archive") },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fa, fb := tc.mutate(base)
			sa := sf3Apply(t, fa)
			sb := sf3Apply(t, fb)

			// (a): the figure whose field we mutated must render differently.
			if bufferEqual(tc.renderFigure(sa), tc.renderFigure(sb)) {
				t.Fatal("(a) figure render identical after its source field changed — the figure is not wired to the field")
			}
			// (b): every OTHER figure must render byte-identically.
			if !bufferEqual(tc.renderOthers(sa), tc.renderOthers(sb)) {
				t.Fatal("(b) a non-target figure changed when only the target's field did — cross-wired field")
			}
		})
	}
}

func TestRenderBulletin_OrderPreserved(t *testing.T) {
	buf := core.NewBuffer(40, 3)
	stories := []BulletinStory{
		{Story: Story{EventID: "lead", Text: "lead story"}, Rank: 1},
		{Story: Story{EventID: "second", Text: "second story"}, Rank: 2},
	}
	RenderBulletin(buf, core.Rect{X: 0, Y: 0, W: 40, H: 3}, 12, stories, true, tcell.StyleDefault)
	if got := rowText(buf, 1); !strings.HasPrefix(got, "1. lead") {
		t.Errorf("bulletin row 1 = %q, want rank-1 lead first", got)
	}
	if got := rowText(buf, 2); !strings.HasPrefix(got, "2. second") {
		t.Errorf("bulletin row 2 = %q, want rank-2 second", got)
	}
}
