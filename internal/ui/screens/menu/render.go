package menu

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// drawText writes s left-to-right starting at (x, y), clipped to rect's
// right edge — the small shared primitive every text-row render function
// here uses, mirroring ui.screen.demo's drawText (render.go).
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

// RenderSaves draws the save/load browser list (MEN-1): one row per save
// slot — name, timestamp (CreatedAtTick), sim-date (GameMonth), and the
// header-derived summary. It is a pure function of its inputs (SF-8) and
// renders the entries in the order given (the caller supplies the already
// name-sorted list from SaveEntries).
func RenderSaves(buf *core.Buffer, rect core.Rect, entries []SaveEntry, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	y := rect.Y
	limit := rect.Y + rect.H
	for _, e := range entries {
		if y >= limit {
			break
		}
		line := fmt.Sprintf("%-16s tick %-8d month %-4d %s", e.Name, e.CreatedAtTick, e.GameMonth, e.Summary)
		drawText(buf, rect, rect.X, y, line, style)
		y++
	}
}

// RenderSession draws the current-session summary (SF-2's live f10.session
// figures) as one line: seed, tick, month, and paused/speed state.
func RenderSession(buf *core.Buffer, rect core.Rect, session Session, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	paused := "running"
	if session.Paused {
		paused = "paused"
	}
	line := fmt.Sprintf("current session: seed %d · month %d · tick %d · %s (speed %d)",
		session.WorldSeed, session.GameMonth, session.Tick, paused, session.Speed)
	drawText(buf, rect, rect.X, rect.Y, line, style)
}

// RenderNewGameForm draws the new-game setup form (MEN-5) as one line: the
// seed and the debug flag, exactly the two fields the form owns (ASM-255).
func RenderNewGameForm(buf *core.Buffer, rect core.Rect, req NewGameRequest, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	debug := "off"
	if req.Debug {
		debug = "on"
	}
	line := fmt.Sprintf("new game — seed: %s  debug: %s", req.Seed, debug)
	drawText(buf, rect, rect.X, rect.Y, line, style)
}

// DrillTargets returns the (widget, source) registration pairs this screen
// supplies to ui.dash's (MOD-038) drill-through graph, per SF-5: one entry
// per save slot's summary and one for the current-session figures.
// Registration itself (Enter opening the target, dead-end detection) is
// MOD-038's job — this screen only produces the pair list.
func DrillTargets(entries []SaveEntry, session Session) []DrillTarget {
	out := []DrillTarget{
		{WidgetID: "menu.session", Target: "engine.core.session"},
	}
	for _, e := range entries {
		out = append(out, DrillTarget{WidgetID: "menu.save." + e.Name, Target: "serializer.bundle." + e.Name})
	}
	return out
}
