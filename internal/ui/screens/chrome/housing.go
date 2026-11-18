package chrome

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// HousingFigures is BUG-1220's housing-and-migration block, published on
// chrome.topbar by the engine (compose/housing_pub.go) so the player can see
// WHY population moves: Places is the home stock (seed homes plus every
// completed dwelling's capacity), Free what is left after the people living
// there, MovedIn/Left the last monthly migration step, and Full the engine's
// own "no vacancy, nobody is admitted" verdict. Known is false until the
// first monthly migration step of the current timeline has run.
type HousingFigures struct {
	Known   bool  `json:"known"`
	Places  int64 `json:"places"`
	Lived   int64 `json:"lived"`
	Free    int64 `json:"free"`
	MovedIn int64 `json:"movedIn"`
	Left    int64 `json:"left"`
	Full    bool  `json:"full"`
}

// housingCue is the explicit instruction shown when homes are full.
const housingCue = "HOMES FULL - build dwellings (F3) to grow"

// HousingLine renders the second top-bar row. Pure function of the figures.
func (f Figures) HousingLine() string {
	h := f.Housing
	if !h.Known {
		return "homes: no month closed yet"
	}
	line := fmt.Sprintf("homes %d places, %d lived in, %d free | moved in %d, left %d this month",
		h.Places, h.Lived, h.Free, h.MovedIn, h.Left)
	if h.Full {
		// The cue leads: the full line is over 100 columns and an 80-column
		// terminal would cut the instruction off the end (BUG-1220).
		line = housingCue + " | " + line
	}
	return line
}

// RenderHousing draws the housing row at the top-left of rect (one row). On a
// struct-copied receiver it draws nothing, like Render.
func (c *Chrome) RenderHousing(buf *core.Buffer, rect core.Rect) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	if err := c.checkNotCopied(map[string]any{"method": "RenderHousing"}); err != nil {
		return
	}
	c.mu.Lock()
	if err := c.checkNotCopied(map[string]any{"method": "RenderHousing"}); err != nil {
		c.mu.Unlock()
		return
	}
	fig := c.figures
	c.mu.Unlock()
	drawText(buf, rect.X, rect.Y, fig.HousingLine(), tcell.StyleDefault)
}
