package widgets

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// BUG-330: the project's "blank view = looks broken" defect class. Two
// different not-drawing-anything conditions used to reach the terminal as
// the SAME ambiguous blank rectangle, indistinguishable from a genuinely
// broken screen:
//
//   - EMPTY: the view is wired and working but has no data to show YET —
//     a pre-wiring boot, a zero-row payload, or (the map) a snapshot with
//     no known cells. The correct player-facing signal is "nothing here
//     yet", not a dead blank the player reads as a crash.
//   - REJECTED/ERROR: the render was refused or the screen is in an
//     invalid state (e.g. a struct-copied screen failing its SEC-020
//     copy-guard). The correct signal is a loud, distinct error marker —
//     never the same nothing an EMPTY view draws.
//
// These two helpers give every screen ONE shared, consistent way to draw
// each state so the two can never again collapse into the same blank.
// Both are deterministic (GR#21): pure functions of their arguments, no
// clock and no rand — the same inputs draw the same cells every call.
const (
	// PlaceholderEmptyMark is the fixed substring every EMPTY placeholder
	// draws. Screens and tests key off it to tell an EMPTY view apart from
	// both a blank and an errored one (BUG-330).
	PlaceholderEmptyMark = "nothing to show yet"

	// PlaceholderErrorMark is the fixed substring (a leading "[!] " glyph
	// group plus this word) every REJECTED/ERROR placeholder draws — a
	// distinct marker from PlaceholderEmptyMark so the two states are never
	// ambiguous (BUG-330).
	PlaceholderErrorMark = "render error"

	// placeholderErrorGlyph prefixes the error line so the ERROR state is
	// visually distinct at a glance, not just on close reading.
	placeholderErrorGlyph = "[!] "
)

// PlaceholderEmpty draws BUG-330's EMPTY view-state into rect: a centered,
// muted (TokenDecay, dim) message that NAMES the view and, optionally,
// says why it is empty — deliberately, visibly distinct from a raw blank
// (the "looks broken" ambiguity this closes) and from PlaceholderError.
// viewName is the human label of the pane ("MAP", "SERVICE FUNDING", …);
// reason is an optional short explanation ("waiting for first snapshot")
// drawn on the line below, omitted when empty.
func PlaceholderEmpty(buf *core.Buffer, rect core.Rect, viewName, reason string, palette Palette, base tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	style := base.Foreground(palette.Color(TokenDecay)).Dim(true)
	midY := rect.Y + rect.H/2
	drawCenteredLine(buf, rect, midY, placeholderEmptyLabel(viewName), style)
	if reason != "" {
		drawCenteredLine(buf, rect, midY+1, reason, style)
	}
}

// PlaceholderError draws BUG-330's REJECTED/ERROR view-state into rect: a
// centered error indicator in TokenDanger, naming the view and carrying a
// short detail plus (if supplied) the correlation ID so an operator can
// tie the on-screen error to the registry-logged one (GR#1's selectable
// display). Distinct from PlaceholderEmpty in colour, glyph, and marker
// word so the two states can never be confused.
func PlaceholderError(buf *core.Buffer, rect core.Rect, viewName, detail, correlationID string, palette Palette, base tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	style := base.Foreground(palette.Color(TokenDanger)).Bold(true)
	midY := rect.Y + rect.H/2
	drawCenteredLine(buf, rect, midY, placeholderErrorLabel(viewName), style)
	if detail != "" {
		drawCenteredLine(buf, rect, midY+1, detail, style)
	}
	if correlationID != "" {
		drawCenteredLine(buf, rect, midY+2, "ref "+correlationID, base.Foreground(palette.Color(TokenDanger)))
	}
}

// placeholderEmptyLabel is the EMPTY headline: "<view> — nothing to show
// yet". A blank viewName degrades to just the marker rather than a dangling
// dash.
func placeholderEmptyLabel(viewName string) string {
	if viewName == "" {
		return PlaceholderEmptyMark
	}
	return viewName + " — " + PlaceholderEmptyMark
}

// placeholderErrorLabel is the ERROR headline: "[!] <view> render error".
func placeholderErrorLabel(viewName string) string {
	if viewName == "" {
		return placeholderErrorGlyph + PlaceholderErrorMark
	}
	return placeholderErrorGlyph + viewName + " " + PlaceholderErrorMark
}

// drawCenteredLine writes text horizontally centered on row y within rect,
// clipped to rect (core.Buffer.Set drops any out-of-range cell itself, so
// this only has to compute a start column and stop past the right edge).
// A line wider than rect is left-clamped so its start is at least rect.X.
func drawCenteredLine(buf *core.Buffer, rect core.Rect, y int, text string, style tcell.Style) {
	if y < rect.Y || y >= rect.Y+rect.H {
		return
	}
	runes := []rune(text)
	start := rect.X + (rect.W-len(runes))/2
	if start < rect.X {
		start = rect.X
	}
	for i, r := range runes {
		x := start + i
		if x >= rect.X+rect.W {
			break
		}
		buf.Set(x, y, r, style)
	}
}
