package menu

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
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

// drillViewSaveSlot is the drill-through destination view this screen names
// for every save slot (SF-5/MEN): the F1 map viewport ("f1.viewport"), the
// registered F-screen-key view a loaded save lands on. "Enter on a save
// slot" loads it and returns the player to the live game, which is F1's
// map viewport — a real view ui.screen.map actually subscribes to
// (mapscreen.ViewSubscriptionName), not a fabricated scope. The previous
// "serializer.bundle" name was a dead end (ASM-651): int.serializer
// (INT-002) is a disk-format foundation module, not a view publisher, and
// "serializer" is neither an F-screen key nor an engine-domain noun per
// int.protocol's view-naming scheme.
//
// EntityID is deliberately empty (whole view): f1.viewport has no
// bundle-named sub-entity, and the specific bundle being loaded is carried
// by the Load action's path (Screen.Load), not by a DrillTarget sub-entity.
// drill_test.go asserts this constant equals mapscreen.ViewSubscriptionName
// (drift test — if F1's view is ever renamed, this fails and forces
// reconciliation), so the destination provably exists rather than merely
// being grammar-valid.
const drillViewSaveSlot = "f1.viewport"

// DrillTargets returns the drill-through source identities this screen
// supplies for registration into ui.dash's (MOD-038) drill-through graph,
// per SF-5: one for the current-session figures and one per save slot.
// Each is the canonical dash.DrillTarget (ViewName, EntityID) shape —
// GR#3 forbids a parallel bespoke copy — with the session figure's
// ViewName ViewSession ("f10.session", whole view) and each save slot's
// ViewName drillViewSaveSlot ("f1.viewport", whole view). Registration,
// navigation and dead-end detection remain MOD-038's job; this screen only
// produces the source list.
func DrillTargets(entries []SaveEntry, session Session) []dash.DrillTarget {
	out := []dash.DrillTarget{
		{ViewName: ViewSession},
	}
	for range entries {
		out = append(out, dash.DrillTarget{ViewName: drillViewSaveSlot})
	}
	return out
}
