package services

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
	"github.com/gdamore/tcell/v2"
)

// BUG-330: a services section with data present but ZERO rows used to draw
// only its header — an ambiguous near-blank the player reads as broken. It
// must now show an explicit EMPTY placeholder, distinct from the REJECTED
// state (sliders' "Funding Rejected: ...").
//
// RED proof (BUG-230 — no vacuous guard tests): scratch-revert render.go's
// `if len(...) == 0 { widgets.PlaceholderEmpty(...) }` blocks and the empty
// sub-tests go RED (only the header draws, no marker). Confirmed before
// landing.

func renderToNewBuffer(t *testing.T, draw func(buf *core.Buffer, rect core.Rect)) []string {
	t.Helper()
	rect := core.Rect{X: 0, Y: 0, W: 60, H: 10}
	buf := core.NewBuffer(rect.W, rect.H)
	draw(buf, rect)
	return renderedText(buf, rect)
}

func TestBUG330_Services_EmptyRows_ShowExplicitPlaceholder(t *testing.T) {
	style := tcell.StyleDefault

	cases := []struct {
		name string
		draw func(buf *core.Buffer, rect core.Rect)
	}{
		{"sliders", func(buf *core.Buffer, rect core.Rect) {
			RenderSliders(buf, rect, nil, "", true, style)
		}},
		{"capacity", func(buf *core.Buffer, rect core.Rect) {
			RenderCapacityDemand(buf, rect, nil, true, widgets.DefaultPalette, style)
		}},
		{"response", func(buf *core.Buffer, rect core.Rect) {
			RenderResponseTimes(buf, rect, nil, true, style)
		}},
		{"waiting", func(buf *core.Buffer, rect core.Rect) {
			RenderWaitingLists(buf, rect, nil, true, style)
		}},
		{"pie", func(buf *core.Buffer, rect core.Rect) {
			RenderPublicServicePie(buf, rect, PublicServicePieView{}, true, style)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := renderToNewBuffer(t, tc.draw)
			if !rowContains(rows, widgets.PlaceholderEmptyMark) {
				t.Fatalf("%s with have=true but zero rows did not draw the EMPTY placeholder %q (BUG-330: near-blank == looks broken)\n%v", tc.name, widgets.PlaceholderEmptyMark, rows)
			}
			if rowContains(rows, widgets.PlaceholderErrorMark) {
				t.Fatalf("%s EMPTY render drew the ERROR marker %q — the two states must be distinct", tc.name, widgets.PlaceholderErrorMark)
			}
		})
	}
}

// The REJECTED state (a funding rejection) must remain visibly distinct
// from the EMPTY state — same screen, different signal.
func TestBUG330_Services_Rejected_IsDistinctFromEmpty(t *testing.T) {
	rows := renderToNewBuffer(t, func(buf *core.Buffer, rect core.Rect) {
		RenderSliders(buf, rect, nil, "funding cannot go below the statutory floor", true, tcell.StyleDefault)
	})
	if !rowContains(rows, "Funding Rejected:") {
		t.Fatalf("rejected sliders render did not draw the rejection reason\n%v", rows)
	}
	if rowContains(rows, widgets.PlaceholderEmptyMark) {
		t.Fatalf("rejected sliders render also drew the EMPTY marker %q — REJECTED and EMPTY must be distinct", widgets.PlaceholderEmptyMark)
	}
}
