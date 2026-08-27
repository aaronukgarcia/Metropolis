package mapscreen

import (
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// BUG-330: an empty map used to render as an all-blank rectangle,
// indistinguishable from a broken screen (the project's "blank view ==
// looks broken" defect class). It must now draw an explicit, muted
// "nothing to show yet" placeholder that names the view and why it is
// empty. (The map's OTHER not-drawing path — a struct-copied receiver —
// is SEC-020's security fail-closed, which deliberately freezes the last
// real frame and is asserted untouched by sec020_test.go; it is not
// BUG-330's error-view state and is intentionally not a marker here.)
//
// RED proof (BUG-230 — no vacuous guard tests): scratch-revert render.go's
// `if snap.isEmpty() { widgets.PlaceholderEmpty(...) }` block and both
// sub-tests go RED (the buffer is blank again, no marker present).
// Confirmed before landing.

// bufferText flattens buf to newline-joined rows, blank/zero runes as
// spaces — enough to substring-search for a placeholder marker.
func bufferText(buf *core.Buffer) string {
	w, h := buf.Size()
	var sb strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := buf.Get(x, y)
			if c.Rune == 0 {
				sb.WriteByte(' ')
			} else {
				sb.WriteRune(c.Rune)
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestBUG330_EmptyMap_RendersExplicitPlaceholder_NotBlank(t *testing.T) {
	m := NewMapScreen("corr-bug330-empty", widgets.DefaultPalette)

	buf := core.NewBuffer(48, 14)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 48, H: 14})

	text := bufferText(buf)
	if !strings.Contains(text, widgets.PlaceholderEmptyMark) {
		t.Fatalf("empty map render did not draw the EMPTY placeholder %q (BUG-330: blank == looks broken)\n%s", widgets.PlaceholderEmptyMark, text)
	}
	// EMPTY must not masquerade as ERROR.
	if strings.Contains(text, widgets.PlaceholderErrorMark) {
		t.Fatalf("empty map render drew the ERROR marker %q — the two states must be distinct", widgets.PlaceholderErrorMark)
	}
}

func TestBUG330_SnapshotWithNoKnownCells_StillEmptyPlaceholder(t *testing.T) {
	m := NewMapScreen("corr-bug330-emptysnap", widgets.DefaultPalette)
	// A full patch with a real extent but no cells listed: haveSnapshot
	// becomes true, yet every cell is Known==false, so the viewport is all
	// blankGlyph — exactly the "served but empty" blank BUG-330 covers.
	m.applyFullLocked(wirePatch{Full: true, Extent: wireExtent{Width: 8, Height: 8}})

	buf := core.NewBuffer(48, 14)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 48, H: 14})

	if text := bufferText(buf); !strings.Contains(text, widgets.PlaceholderEmptyMark) {
		t.Fatalf("all-unknown snapshot did not draw the EMPTY placeholder %q\n%s", widgets.PlaceholderEmptyMark, text)
	}
}
