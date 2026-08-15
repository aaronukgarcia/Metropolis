package proj

import (
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestRenderSlowFuse_RendersProjectionCurveNotBareNumber is PRJ-5's own
// check: a >60-month (Slow-Fuse) decision's consequence, handed to this
// screen's exported rendering call, renders as a projection curve (Braille
// dots in a chart), not a bare number. countBraille==0 would be the
// signature of a text/number-only rendering; a real curve always lights
// Braille cells.
func TestRenderSlowFuse_RendersProjectionCurveNotBareNumber(t *testing.T) {
	consequence := Consequence{
		Label:      "Cut school funding",
		FuseMonths: 72, // >60 months => >5 game-years (ASM-239)
		History:    []float64{80, 78, 76},
		Projection: []float64{74, 70, 64, 58, 50, 42},
	}

	buf := core.NewBuffer(40, 8)
	rect := core.Rect{X: 0, Y: 0, W: 40, H: 8}
	RenderSlowFuse(buf, rect, consequence, widgets.DefaultPalette)

	chart := core.Rect{X: rect.X, Y: rect.Y + 1, W: rect.W, H: rect.H - 1}
	if n := countBraille(buf, chart); n == 0 {
		t.Fatalf("RenderSlowFuse rendered no Braille curve (0 chart cells) — the consequence is a bare number, not a projection curve")
	}

	// The label names the decision and its fuse horizon.
	line := renderedText(buf, core.Rect{X: rect.X, Y: rect.Y, W: rect.W, H: 1})[0]
	if !strings.Contains(line, "Cut school funding") || !strings.Contains(line, "+72mo") {
		t.Errorf("RenderSlowFuse label = %q, want the decision name and its +72mo fuse", line)
	}
}

// TestRenderSlowFuse_EmptyConsequenceStillNotFabricated mirrors PRJ-6:
// RenderSlowFuse never fabricates a flat line — an empty consequence
// renders only its label (and no Braille), never a made-up curve.
func TestRenderSlowFuse_EmptyConsequenceStillNotFabricated(t *testing.T) {
	buf := core.NewBuffer(40, 6)
	rect := core.Rect{X: 0, Y: 0, W: 40, H: 6}
	RenderSlowFuse(buf, rect, Consequence{Label: "Empty", FuseMonths: 72}, widgets.DefaultPalette)

	if countBraille(buf, rect) != 0 {
		t.Errorf("empty consequence rendered %d Braille cells, want 0 (no fabricated line)", countBraille(buf, rect))
	}
}
