package widgets

import (
	"strconv"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// CargoKind selects a queue lane's glyph — UI-SPEC §2's "cargo-coded
// truck glyphs."
type CargoKind int

const (
	CargoGeneral CargoKind = iota
	CargoFuel
	CargoPassenger
	CargoWaste
	CargoFreight
)

// cargoGlyph maps CargoKind to its glyph, precomputed. Glyphs are
// chosen from Unicode blocks with narrow (single-cell) East Asian
// Width, deliberately avoiding emoji/wide symbols (⛽, ☺, …) that render
// double-width in most terminals and would break the "N glyphs == N
// queue length" cell-count contract AC-9 tests against.
var cargoGlyph = map[CargoKind]rune{
	CargoGeneral:   '▮', // generic truck
	CargoFuel:      '◆', // tanker
	CargoPassenger: '●', // bus/coach
	CargoWaste:     '▲', // refuse
	CargoFreight:   '▩', // container freight
}

// Glyph returns k's rendered glyph, defaulting to CargoGeneral's glyph
// for any unrecognised CargoKind value (a future kind added at one call
// site but not this table degrades to the generic glyph, never a
// panic/zero-rune).
func (k CargoKind) Glyph() rune {
	if g, ok := cargoGlyph[k]; ok {
		return g
	}
	return cargoGlyph[CargoGeneral]
}

// QueueLane draws the junction-approach idiom (UI-SPEC §2: "the
// junction pane draws each approach as a lane of cargo-coded truck
// glyphs growing leftward with a wait-time figure — the signature image
// of the game") into rect's first row: a wait-time label at the left
// end, then a run of length glyphs of kind growing from the right edge
// leftward (the queue "backs up" towards the junction stop line at the
// lane's right end, matching how a real approach queue grows away from
// the signal).
//
// length is clamped to non-negative and to whatever glyph cells fit
// after the wait-time label (AC-11: a negative or absurdly large length
// renders the clamped, fitting count rather than panicking or
// overflowing the rect). waitSeconds renders as "Ns" left-aligned;
// negative waitSeconds renders as "0s" (a negative wait is not a
// meaningful state to display as-is).
func QueueLane(buf *core.Buffer, rect core.Rect, length int, kind CargoKind, waitSeconds int, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	if waitSeconds < 0 {
		waitSeconds = 0
	}
	label := strconv.Itoa(waitSeconds) + "s"

	y := rect.Y
	x := rect.X
	limit := rect.X + rect.W
	for _, r := range label {
		if x >= limit {
			break
		}
		buf.Set(x, y, r, style)
		x++
	}
	if x < limit {
		buf.Set(x, y, ' ', style)
		x++
	}

	glyphAreaStart := x
	available := limit - glyphAreaStart
	if length < 0 {
		length = 0
	}
	if length > available {
		length = available
	}
	g := kind.Glyph()
	// Growing leftward from the right edge: the rightmost `length`
	// cells of the glyph area are filled, the cells between the label
	// and the queue's tail stay blank (an empty gap, i.e. "no queue
	// yet" reads as a short queue hugging the stop line, a long queue
	// reads as reaching back towards the label).
	blankUntil := limit - length
	for cx := glyphAreaStart; cx < limit; cx++ {
		if cx < blankUntil {
			buf.Set(cx, y, ' ', style)
		} else {
			buf.Set(cx, y, g, style)
		}
	}
}
