package ticker

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
)

// State strings for the TIK-7/TIK-8 unavailable-data states. Unexported
// constants (not inline literals) so a test can assert on them without
// duplicating the string, and so the render path and the test can never
// drift apart silently.
const (
	// noNewsFeedText is TIK-7's clear "stream unavailable" state: shown
	// by the ticker/bulletin/annual panes before their first patch
	// arrives, rather than an empty scroll that looks broken.
	noNewsFeedText = "no news feed"

	// loadingArchiveText is TIK-8's "still loading" state: the archive
	// pane before the first f9.archive patch arrives.
	loadingArchiveText = "loading history"

	// noMatchesText is TIK-8's "no matches" state: a search ran and
	// matched zero archive entries — explicitly distinct from
	// loadingArchiveText.
	noMatchesText = "no matches"

	// archiveStoppedText is SEC-072's distinct state, surfaced when an
	// f9.archive patch outgrew this screen's wire ceiling and the archive
	// froze at last-known-good: the frozen history is still shown below the
	// banner, but the freeze is visible instead of silent (GR#17).
	archiveStoppedText = "history archive stopped updating (wire limit reached)"
)

// drawText writes s left-to-right starting at (x, y), clipped to rect's
// right edge — the same shared text-draw primitive every other screen
// uses (mirrors ui.screen.demo's drawText / ui.widgets' drawRow clipping
// discipline). Rune sanitisation is NOT this function's job: core.Buffer.
// Set already funnels every rune through its SEC-011 trust boundary, so
// engine-supplied prose reaching the terminal is escaped there centrally
// (the display boundary treats text as text — weakness pattern #4's
// display-text exception, ASM-077).
func drawText(buf *core.Buffer, rect core.Rect, x, y int, s string, style tcell.Style) {
	limit := rect.X + rect.W
	for _, r := range s {
		if x >= limit {
			return
		}
		buf.Set(x, y, r, style)
		x++
	}
}

// storyHeadline renders one story as a single display row: "<name>: <text>"
// when the story has a §20 name, "<text>" otherwise. The event ID is
// deliberately NOT printed — it is a drill-through reference (TIK-5),
// carried in the Story data model and surfaced through DrillTargets, not
// a display field for the reader.
func storyHeadline(st Story) string {
	if st.Name != "" {
		return fmt.Sprintf("%s: %s", st.Name, st.Text)
	}
	return st.Text
}

// RenderTicker draws the rolling ticker (TIK-1): a scrolling window of
// atomic events driven by the deterministic scroll step (scroll.go),
// never the wall clock (SF-8). Before the first f9.ticker patch, it
// draws TIK-7's "no news feed" state. Rendering is a pure function of its
// arguments — the same inputs draw identically across repeated calls.
func RenderTicker(buf *core.Buffer, rect core.Rect, events []Story, scrollStep int, haveTicker bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	if !haveTicker {
		drawText(buf, rect, rect.X, rect.Y, noNewsFeedText, style)
		return
	}
	rows := window(events, scrollPosition(scrollStep, len(events)), rect.H)
	y := rect.Y
	for _, st := range rows {
		drawText(buf, rect, rect.X, y, storyHeadline(st), style)
		y++
	}
}

// RenderBulletin draws the monthly bulletin front page (TIK-2): a header
// line naming the month, then the engine-selected salience-ranked stories
// in rank order (lead first). The 3–5 selection and ranking are the
// engine editor's job (out of scope); this renderer draws exactly the
// stories it is handed. Read-on-pause (TIK-2) is inherent: the bulletin
// is a pure function of the last-applied patch state, so it stays visible
// and readable while the sim is paused and no new deltas arrive — it
// never blanks or requires the clock to keep advancing.
func RenderBulletin(buf *core.Buffer, rect core.Rect, month int64, stories []BulletinStory, haveBulletin bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	if !haveBulletin {
		drawText(buf, rect, rect.X, rect.Y, noNewsFeedText, style)
		return
	}
	drawText(buf, rect, rect.X, rect.Y, fmt.Sprintf("Monthly Bulletin — month %d", month), style)
	y := rect.Y + 1
	limit := rect.Y + rect.H
	for _, st := range stories {
		if y >= limit {
			break
		}
		drawText(buf, rect, rect.X, y, fmt.Sprintf("%d. %s", st.Rank, storyHeadline(st.Story)), style)
		y++
	}
}

// RenderAnnual draws the annual review (TIK-3): a header naming the
// year, the year-in-numbers figures, and the year's biggest story. A
// review with no biggest story shows an explicit "no story this year"
// line rather than fabricating one (SF-7's unavailable-data posture).
func RenderAnnual(buf *core.Buffer, rect core.Rect, review AnnualReview, haveAnnual bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	if !haveAnnual {
		drawText(buf, rect, rect.X, rect.Y, noNewsFeedText, style)
		return
	}
	y := rect.Y
	limit := rect.Y + rect.H
	drawText(buf, rect, rect.X, y, fmt.Sprintf("Annual Review — year %d", review.Year), style)
	y++
	for _, n := range review.Numbers {
		if y >= limit {
			return
		}
		drawText(buf, rect, rect.X, y, fmt.Sprintf("%-20s %d", n.Label, n.Value), style)
		y++
	}
	if y >= limit {
		return
	}
	if review.HasBiggest {
		drawText(buf, rect, rect.X, y, "Biggest story: "+storyHeadline(review.BiggestStory), style)
	} else {
		drawText(buf, rect, rect.X, y, "no story this year", style)
	}
}

// RenderArchive draws the searchable history archive (TIK-4/TIK-8). The
// states are explicit and, where they can coincide, stacked rather than
// mutually exclusive:
//
//   - archiveStalled                     -> archiveStoppedText, drawn FIRST
//     as a header banner — SEC-072/SEC-085's surfaced freeze, never a
//     silent one (GR#17). It takes precedence over "still loading", so a
//     cold-start oversized first patch surfaces the stop rather than
//     misleadingly showing "loading history".
//   - !haveArchive && !archiveStalled    -> loadingArchiveText ("still
//     loading": the first f9.archive patch has not arrived).
//   - searchActive && matchedCount == 0  -> noMatchesText ("no matches":
//     a search ran and matched zero) — TIK-8's required distinction.
//   - otherwise                          -> the passed stories (the full
//     archive when no search is active, the matched set when one is).
func RenderArchive(buf *core.Buffer, rect core.Rect, stories []Story, haveArchive, searchActive, archiveStalled bool, matchedCount int, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	y := rect.Y
	limit := rect.Y + rect.H
	if archiveStalled {
		drawText(buf, rect, rect.X, y, archiveStoppedText, style)
		y++
	}
	if !haveArchive {
		if !archiveStalled && y < limit {
			drawText(buf, rect, rect.X, y, loadingArchiveText, style)
		}
		return
	}
	if searchActive && matchedCount == 0 {
		if y < limit {
			drawText(buf, rect, rect.X, y, noMatchesText, style)
		}
		return
	}
	for _, st := range stories {
		if y >= limit {
			break
		}
		drawText(buf, rect, rect.X, y, storyHeadline(st), style)
		y++
	}
}

// drillViewNewsEvent is the drill-through destination view this screen
// names for every rendered story (SF-5/TIK-5): the engine.news event
// view that "Enter on this story" opens, in int.protocol's entity-scoped
// form — scope "news", projection "event", with the specific event
// addressed by DrillTarget.EntityID (dash's opaque per-row identifier)
// rather than baked into the view name, because a wire event ID is an
// opaque engine-defined string not guaranteed to satisfy the
// lowercase.dot-segment grammar a ViewName must (tick5_test.go asserts
// this constant validates). MOD-043 (engine.news) is unbuilt at dispatch
// time, so this name is forward-looking and reconciled against MOD-043's
// landed view names by a drift test when that module ships.
const drillViewNewsEvent = "news.event"

// DrillTargets returns the drill-through source identities this screen
// supplies for registration into ui.dash's (MOD-038) drill-through
// graph, per SF-5/TIK-5: one canonical dash.DrillTarget per rendered
// story, whose ViewName is drillViewNewsEvent and whose EntityID is the
// story's originating engine.news event ID (the sim event Enter should
// open). This is MOD-038's landed (ViewName, EntityID) shape — GR#3
// forbids a parallel bespoke copy, and the widget identity is the
// caller's tile ID in dash's model, not part of a DrillTarget.
// Registration, navigation and dead-end detection remain MOD-038's job;
// this screen only produces the source list.
func DrillTargets(stories []Story) []dash.DrillTarget {
	out := make([]dash.DrillTarget, 0, len(stories))
	for _, st := range stories {
		out = append(out, dash.DrillTarget{
			ViewName: drillViewNewsEvent,
			EntityID: protocol.EntityID(st.EventID),
		})
	}
	return out
}
