package chrome

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// topBarString renders Figures as the top bar's single line (AC-1). It is a
// pure function of the Figures value so the render test can assert the
// known injected values appear without going through the buffer, and so the
// format is one place rather than repeated per field.
func (f Figures) topBarString() string {
	return fmt.Sprintf("%s | cycle %d/30 | speed %d | money %d | pop %d | rating %s",
		f.Date, f.ClockCycle, f.Speed, f.Money, f.Population, f.Rating)
}

// alertLine renders one alert's displayed text. The tier colour is applied
// by drawAlertStack via Tier.Token(); the line itself is just the alert's
// Text (a crisis alert is distinguished by its colour + the auto-pause
// behaviour, not by a bespoke glyph — §13 gives the alert stack's content
// as the alert's own message).
func (a Alert) alertLine() string { return a.Text }

// Render draws the top bar and the bottom alert stack into buf within rect
// (AC-1/AC-2). It is a pure function of Chrome's state (AC-14): the same
// state renders identically across repeated calls, and nothing here samples
// the wall clock (AC-15). Row 0 of rect is the top bar; subsequent rows are
// the alert stack, one alert per row, colour-coded by tier, most-important
// first.
//
// On a struct-copied receiver (SEC-020) it draws NOTHING and returns — buf
// is left exactly as the caller passed it in. A render loop must not panic
// or corrupt the buffer over a misuse that the copyguard already logs
// (same posture as ui.screen.map's Render).
func (c *Chrome) Render(buf *core.Buffer, rect core.Rect) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	if err := c.checkNotCopied(map[string]any{"method": "Render"}); err != nil {
		return
	}

	c.mu.Lock()
	if err := c.checkNotCopied(map[string]any{"method": "Render"}); err != nil {
		c.mu.Unlock()
		return
	}
	fig := c.figures
	alerts := snapshotAlerts(c.alerts) // defensive copy: draw runs lock-free
	c.mu.Unlock()

	drawTopBar(buf, rect, fig)
	drawAlertStack(buf, rect, alerts, c.palette)
}

// drawTopBar writes the figures line at rect's top-left corner. Anything
// beyond rect.W columns is simply not drawn (Buffer.Set ignores
// out-of-range coordinates).
func drawTopBar(buf *core.Buffer, rect core.Rect, f Figures) {
	drawText(buf, rect.X, rect.Y, f.topBarString(), tcell.StyleDefault)
}

// drawAlertStack writes each alert below the top bar, one per row, in
// stack order (most-important first), colour-coded by tier (AC-2). Alerts
// that would fall outside rect's height are simply not drawn.
func drawAlertStack(buf *core.Buffer, rect core.Rect, alerts []Alert, palette widgets.Palette) {
	row := rect.Y + 1
	for _, a := range alerts {
		if row >= rect.Y+rect.H {
			return
		}
		drawText(buf, rect.X, row, a.alertLine(), palette.Style(a.Tier.Token()))
		row++
	}
}

// drawText writes s starting at (x, y), advancing one column per rune.
// Every rune passes through core.Buffer.Set, so untrusted alert text is
// escaped/filtered at the single trust boundary (core's sanitizeRune,
// SEC-011) rather than re-implemented here — display text is escaped, not
// rejected (weakness pattern #4's display-boundary exception).
func drawText(buf *core.Buffer, x, y int, s string, style tcell.Style) {
	for i, r := range s {
		buf.Set(x+i, y, r, style)
	}
}
