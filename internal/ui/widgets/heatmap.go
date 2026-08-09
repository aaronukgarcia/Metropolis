package widgets

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// HeatRamp is an ordered sequence of colours a Heatmap interpolates a
// normalised [0,1] metric across. len(HeatRamp) must be >= 1; a
// single-colour ramp degenerates to "always that colour" (useful for
// tests, not for a real overlay).
type HeatRamp []tcell.Color

// DefaultHeatRamp returns a five-stop ramp from p's "calm" end (water,
// standing in for "low") through warning to danger ("high") — the
// generic shape UI-SPEC §2's map overlays (land value, traffic v/c,
// noise dBA, BDI, coverage) share; a specific overlay may supply its
// own HeatRamp instead (e.g. a coverage overlay might want a
// money-green ramp) — this is just the sane default.
func DefaultHeatRamp(p Palette) HeatRamp {
	return HeatRamp{
		p.Color(TokenWater),
		p.Color(TokenMoney),
		p.Color(TokenPower),
		p.Color(TokenWarning),
		p.Color(TokenDanger),
	}
}

// rampColor maps a normalised value in [0,1] to a colour in ramp by
// nearest-stop selection (no blending — terminal 16/24-bit colour
// blending is not worth the complexity here, and discrete bands read
// better for dashboard-style overlays anyway). Out-of-range value is
// clamped, not rejected — AC-11's degenerate-input contract.
func rampColor(ramp HeatRamp, value float64) tcell.Color {
	if len(ramp) == 0 {
		return tcell.ColorDefault
	}
	if value < 0 {
		value = 0
	}
	if value > 1 {
		value = 1
	}
	idx := int(value*float64(len(ramp)-1) + 0.5)
	if idx < 0 {
		idx = 0
	}
	if idx > len(ramp)-1 {
		idx = len(ramp) - 1
	}
	return ramp[idx]
}

// Heatmap paints a background-colour ramp over rect from values (a
// row-major slice of normalised-or-raw metric samples, width cells
// wide, len(values) == width*rect.H expected — a shorter slice simply
// leaves the remaining cells untouched rather than panicking, and a
// longer one ignores the excess). min/max normalise raw values to
// [0,1] before ramp lookup (pass 0,1 if values are already normalised).
//
// Critically — AC-5 — Heatmap NEVER touches a cell's Rune: it reads the
// existing Cell via buf.Get, keeps its Rune and foreground exactly as
// they were, and writes back only a new Background colour merged onto
// the existing Style. The structure/terrain glyph a prior draw call put
// there (or the blank default) survives untouched; only the background
// carries the metric. This is the two-data-layers-per-cell contract
// UI-SPEC §2 describes for map overlays.
func Heatmap(buf *core.Buffer, rect core.Rect, values []float64, width int, min, max float64, ramp HeatRamp) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 || width <= 0 {
		return
	}
	span := max - min

	for row := 0; row < rect.H; row++ {
		for col := 0; col < rect.W; col++ {
			i := row*width + col
			if i < 0 || i >= len(values) {
				continue
			}
			raw := values[i]
			var norm float64
			if span <= 0 {
				norm = 0
			} else {
				norm = (raw - min) / span
			}
			color := rampColor(ramp, norm)

			x, y := rect.X+col, rect.Y+row
			existing := buf.Get(x, y)
			newStyle := existing.Style.Background(color)
			buf.Set(x, y, existing.Rune, newStyle)
		}
	}
}
