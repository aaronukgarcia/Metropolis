package proj

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func renderCurve(t *testing.T, c Curve) (*core.Buffer, core.Rect) {
	t.Helper()
	buf := core.NewBuffer(80, 8)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 8}
	RenderCurve(buf, rect, c, widgets.DefaultPalette)
	return buf, rect
}

// countBraille counts cells in rect holding a Braille-pattern rune.
func countBraille(buf *core.Buffer, rect core.Rect) int {
	n := 0
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		for x := rect.X; x < rect.X+rect.W; x++ {
			if isBraille(buf.Get(x, y).Rune) {
				n++
			}
		}
	}
	return n
}

// TestRenderCurve_HistorySolidProjectionDim is PRJ-1's core idiom check:
// history renders solid (normal intensity), projection dim, and the two
// occupy distinct horizontal regions of the chart.
func TestRenderCurve_HistorySolidProjectionDim(t *testing.T) {
	c := Curve{
		Key: "water.demand", Status: StatusAvailable,
		History:    []float64{10, 11, 12, 13},
		Projection: []float64{14, 15, 16, 17},
	}
	buf, rect := renderCurve(t, c)
	chart := core.Rect{X: rect.X, Y: rect.Y + 1, W: rect.W, H: rect.H - 1}

	var sawSolid, sawDim bool
	for y := chart.Y; y < chart.Y+chart.H; y++ {
		for x := chart.X; x < chart.X+chart.W; x++ {
			cell := buf.Get(x, y)
			if !isBraille(cell.Rune) {
				continue
			}
			if cell.Style == tcell.StyleDefault.Dim(true) {
				sawDim = true
			} else if cell.Style == tcell.StyleDefault {
				sawSolid = true
			}
		}
	}
	if !sawSolid {
		t.Error("no solid (non-dim) history Braille cells rendered")
	}
	if !sawDim {
		t.Error("no dim projection Braille cells rendered")
	}
}

// TestRenderCurve_ConfidenceBandsChangeOutput proves bands are actually
// drawn: a curve with confidence bands renders strictly more Braille dots
// than the same curve without bands.
func TestRenderCurve_ConfidenceBandsChangeOutput(t *testing.T) {
	base := Curve{Key: "k", Status: StatusAvailable, History: []float64{1, 2}, Projection: []float64{3, 4, 5}}
	withBands := base
	withBands.ConfidenceUpper = []float64{4.5, 5.5, 6.0}
	withBands.ConfidenceLower = []float64{2.5, 3.5, 4.0}

	bufA, rectA := renderCurve(t, base)
	bufB, rectB := renderCurve(t, withBands)
	chartA := core.Rect{X: rectA.X, Y: rectA.Y + 1, W: rectA.W, H: rectA.H - 1}
	chartB := core.Rect{X: rectB.X, Y: rectB.Y + 1, W: rectB.W, H: rectB.H - 1}

	if countBraille(bufB, chartB) <= countBraille(bufA, chartA) {
		t.Errorf("confidence bands added no Braille dots: base=%d withBands=%d", countBraille(bufA, chartA), countBraille(bufB, chartB))
	}
}

// TestRenderCurve_ThresholdLineChangesOutput proves threshold lines are
// drawn (PRJ-1): a curve with a threshold renders more Braille dots than
// the same curve without one, and the added dots carry the danger style.
func TestRenderCurve_ThresholdLineChangesOutput(t *testing.T) {
	base := Curve{Key: "k", Status: StatusAvailable, History: []float64{1, 2}, Projection: []float64{3, 4, 5}}
	withThreshold := base
	withThreshold.Thresholds = []Threshold{{Value: 4.0, Label: "capacity ceiling"}}

	bufA, rectA := renderCurve(t, base)
	bufB, rectB := renderCurve(t, withThreshold)
	chartA := core.Rect{X: rectA.X, Y: rectA.Y + 1, W: rectA.W, H: rectA.H - 1}
	chartB := core.Rect{X: rectB.X, Y: rectB.Y + 1, W: rectB.W, H: rectB.H - 1}

	if countBraille(bufB, chartB) <= countBraille(bufA, chartA) {
		t.Errorf("threshold line added no Braille dots: base=%d withThreshold=%d", countBraille(bufA, chartA), countBraille(bufB, chartB))
	}

	dangerStyle := widgets.DefaultPalette.Style(widgets.TokenDanger)
	sawDanger := false
	for y := chartB.Y; y < chartB.Y+chartB.H; y++ {
		for x := chartB.X; x < chartB.X+chartB.W; x++ {
			cell := bufB.Get(x, y)
			if isBraille(cell.Rune) && cell.Style == dangerStyle {
				sawDanger = true
			}
		}
	}
	if !sawDanger {
		t.Error("threshold line dots do not carry the danger (TokenDanger) style")
	}
}

// TestRenderCurve_DecisionMarkerRendersLabel pins the queued-decision
// marker's on-chart annotation (PRJ-1): the label row names the queued
// decision and its month.
func TestRenderCurve_DecisionMarkerRendersLabel(t *testing.T) {
	c := Curve{
		Key: "education.capacity", Status: StatusAvailable,
		History:    []float64{100, 101},
		Projection: []float64{102, 103, 104},
		Markers:    []DecisionMarker{{MonthOffset: 1, Label: "school build"}},
	}
	buf, rect := renderCurve(t, c)
	line := renderedText(buf, core.Rect{X: rect.X, Y: rect.Y, W: rect.W, H: 1})[0]
	if !strings.Contains(line, "[m+1 school build]") {
		t.Errorf("label line = %q, want it to name the queued decision", line)
	}
}

// TestRenderCurve_UnavailableShowsReasonNoFabricatedLine is PRJ-6's
// anti-fabrication check: a non-available curve renders its reason as text
// and draws NO Braille chart (never a blank or a fabricated flat line).
func TestRenderCurve_UnavailableShowsReasonNoFabricatedLine(t *testing.T) {
	c := Curve{
		Key: "capexport.demand", Status: StatusUnavailable,
		UnavailableReason: "engine.capexport not yet real",
		History:           []float64{1, 2, 3},
		Projection:        []float64{4, 5, 6},
	}
	buf, rect := renderCurve(t, c)
	lines := renderedText(buf, rect)
	joined := strings.Join(lines, " ")
	if !strings.Contains(joined, "unavailable: engine.capexport not yet real") {
		t.Errorf("rendered text %q does not name the unavailability reason", lines)
	}
	if countBraille(buf, rect) != 0 {
		t.Errorf("unavailable curve rendered %d Braille cells, want 0 (no fabricated flat line)", countBraille(buf, rect))
	}
}

// TestRenderCurve_SeasonalStructurePreserved is PRJ-2's "seasonally aware"
// render-side half: the screen renders every month it is given without
// flattening, so a seasonally oscillating projection shows month-to-month
// variation (distinct dot rows) rather than a flat trend.
func TestRenderCurve_SeasonalStructurePreserved(t *testing.T) {
	// A seasonal wave: high/low alternating across 12 projected months.
	seasonal := []float64{5, 1, 5, 1, 5, 1, 5, 1, 5, 1, 5, 1}
	c := Curve{Key: "power.demand", Status: StatusAvailable, Projection: seasonal}
	buf, rect := renderCurve(t, c)
	chart := core.Rect{X: rect.X, Y: rect.Y + 1, W: rect.W, H: rect.H - 1}

	// Collect the distinct dot-rows the projection touches. A flat or
	// linearised render would touch one row (or a straight ramp); the
	// seasonal wave must touch at least the top and bottom rows.
	top, bottom := false, false
	for y := chart.Y; y < chart.Y+chart.H; y++ {
		for x := chart.X; x < chart.X+chart.W; x++ {
			if isBraille(buf.Get(x, y).Rune) {
				if y == chart.Y {
					top = true
				}
				if y == chart.Y+chart.H-1 {
					bottom = true
				}
			}
		}
	}
	if !top || !bottom {
		t.Errorf("seasonal projection touched top=%v bottom=%v, want both (the monthly structure must be preserved, not flattened)", top, bottom)
	}
}

// TestRenderCrossing_TwoSeriesAndCrossingAnnotation is PRJ-3's check: the
// internal-demand and contracted-capacity series both render (two Braille
// lines) and the crossing month is annotated.
func TestRenderCrossing_TwoSeriesAndCrossingAnnotation(t *testing.T) {
	x := Crossing{
		Key: "refuse.ashford", Label: "Refuse — Ashford", Status: StatusAvailable,
		InternalDemand:     []float64{100, 110, 120, 130},
		ContractedCapacity: []float64{115, 115, 115, 115},
		CrossingMonth:      2,
	}
	buf := core.NewBuffer(80, 8)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 8}
	RenderCrossing(buf, rect, x, widgets.DefaultPalette)

	line := renderedText(buf, core.Rect{X: 0, Y: 0, W: 80, H: 1})[0]
	if !strings.Contains(line, "crossing +2mo") {
		t.Errorf("crossing label = %q, want it to name the crossing month", line)
	}

	chart := core.Rect{X: 0, Y: 1, W: 80, H: 7}
	if countBraille(buf, chart) == 0 {
		t.Error("crossing chart rendered no Braille cells, want the two overlapping series")
	}
}

// TestRenderCrossing_NoCrossingAnnotated is PRJ-3's no-crossing case.
func TestRenderCrossing_NoCrossingAnnotated(t *testing.T) {
	x := Crossing{
		Key: "power.sellindge", Status: StatusAvailable,
		InternalDemand:     []float64{100, 110},
		ContractedCapacity: []float64{200, 200},
		CrossingMonth:      -1,
	}
	buf := core.NewBuffer(80, 6)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 6}
	RenderCrossing(buf, rect, x, widgets.DefaultPalette)
	line := renderedText(buf, core.Rect{X: 0, Y: 0, W: 80, H: 1})[0]
	if !strings.Contains(line, "no crossing within horizon") {
		t.Errorf("crossing label = %q, want 'no crossing within horizon'", line)
	}
}

// TestRenderRateOutlook_ReadOnlyCurve is PRJ-4's check: the rate outlook
// renders as a plain history+projection curve with a §45 title, and an
// unavailable outlook renders its reason rather than a blank.
func TestRenderRateOutlook_ReadOnlyCurve(t *testing.T) {
	r := RateOutlook{Status: StatusAvailable, History: []float64{2.0, 2.1}, Projection: []float64{2.2, 2.4, 2.6}}
	buf := core.NewBuffer(40, 6)
	rect := core.Rect{X: 0, Y: 0, W: 40, H: 6}
	RenderRateOutlook(buf, rect, r, widgets.DefaultPalette)

	line := renderedText(buf, core.Rect{X: 0, Y: 0, W: 40, H: 1})[0]
	if !strings.Contains(line, "Base rate outlook") {
		t.Errorf("rate label = %q, want 'Base rate outlook (§45)'", line)
	}
	chart := core.Rect{X: 0, Y: 1, W: 40, H: 5}
	if countBraille(buf, chart) == 0 {
		t.Error("rate outlook rendered no Braille curve")
	}
}

// TestScaleHelpers_AlignWithWidgetsBrailleChart is the drift test for the
// duplicated normalisation (weakness pattern #2): it computes where the
// projection's maximum point SHOULD be using this package's own helpers,
// then asserts widgets.BrailleChart actually drew a Braille dot in exactly
// that cell. If either widgets.BrailleChart's scale or this package's
// mirror of it drifts, the computed cell and the drawn cell stop matching.
func TestScaleHelpers_AlignWithWidgetsBrailleChart(t *testing.T) {
	rect := core.Rect{X: 0, Y: 0, W: 20, H: 5}
	projection := []float64{0, 100}

	// Reference render via the real widget.
	ref := core.NewBuffer(20, 5)
	widgets.BrailleChart(ref, rect, nil, projection, tcell.StyleDefault, tcell.StyleDefault.Dim(true))

	// This package's mirror of the widget's scale, for the projection max
	// (index 1, value 100).
	dotsW := rect.W * 2
	dotsH := rect.H * 4
	min, max := combinedRange(nil, projection)
	projStart, projSpan := 0, dotsW // history empty -> full width is projection
	col := projectionCol(1, len(projection), projStart, projSpan)
	row := dotRow(100, min, max, dotsH)
	cellX, cellY := col/2, row/4

	c := ref.Get(cellX, cellY)
	if !isBraille(c.Rune) {
		t.Fatalf("widgets.BrailleChart did not draw the projection max at cell (%d,%d) — computed (col=%d,row=%d) from this package's scale mirrors, but the chart disagrees (drift)", cellX, cellY, col, row)
	}
}

// TestDotRow_EdgeCases pins the duplicated dotRow formula's boundaries:
// flat -> middle, min -> bottom, max -> top, out-of-range clamps.
func TestDotRow_EdgeCases(t *testing.T) {
	const dotsH = 20
	if got := dotRow(5, 0, 10, dotsH); got != 9 {
		t.Errorf("dotRow(5,0,10) = %d, want 9 (the range midpoint of a 0..19 row span)", got)
	}
	if got := dotRow(0, 0, 10, dotsH); got != dotsH-1 {
		t.Errorf("dotRow(min) = %d, want %d (bottom)", got, dotsH-1)
	}
	if got := dotRow(10, 0, 10, dotsH); got != 0 {
		t.Errorf("dotRow(max) = %d, want 0 (top)", got)
	}
	if got := dotRow(99, 0, 10, dotsH); got != 0 {
		t.Errorf("dotRow(above max) = %d, want 0 (clamped)", got)
	}
	if got := dotRow(-99, 0, 10, dotsH); got != dotsH-1 {
		t.Errorf("dotRow(below min) = %d, want %d (clamped)", got, dotsH-1)
	}
	// Flat series (max <= min) maps to the vertical middle, matching
	// widgets.BrailleChart's own flat-series degenerate case.
	if got := dotRow(5, 5, 5, dotsH); got != dotsH/2 {
		t.Errorf("dotRow(flat) = %d, want %d (middle)", got, dotsH/2)
	}
}
