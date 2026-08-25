package proj

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// brailleBase is U+2800 BRAILLE PATTERN BLANK — the Unicode codepoint all
// Braille patterns are an offset from. Mirrors widgets' own brailleBase
// (a Unicode constant, not a project-invented value). Used here to OR
// overlay dots into cells widgets.BrailleChart already drew.
const brailleBase = rune(0x2800)

// curveStyles returns the projection-pane styles (PRJ-1 / UI-SPEC §4):
// history solid (normal intensity), projection and confidence bands dim,
// threshold lines danger-red, decision markers warning-amber. Solid vs
// dim is the tcell Dim attribute, so the two are visibly distinct on any
// backend without depending on colour.
func curveStyles(palette widgets.Palette) (history, projection, band, threshold, marker tcell.Style) {
	history = tcell.StyleDefault
	projection = tcell.StyleDefault.Dim(true)
	band = tcell.StyleDefault.Dim(true)
	threshold = palette.Style(widgets.TokenDanger)
	marker = palette.Style(widgets.TokenWarning)
	return
}

// drawText writes s left-to-right starting at (x, y), clipped to rect's
// right edge — the small shared primitive every label row uses, mirroring
// widgets.DrawTable's and ui.screen.demo's drawRow/drawText clipping
// discipline.
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

// RenderHeader draws the F7 title line, including the forecast horizon N
// (PRJ-2) read from the view — never a hardcoded literal (GR#15). When
// !haveData it draws the title with "no data" rather than a fabricated
// horizon.
func RenderHeader(buf *core.Buffer, rect core.Rect, horizonMonths int, haveData bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	if !haveData {
		drawText(buf, rect, rect.X, rect.Y, "F7 Projections — no data", style)
		return
	}
	drawText(buf, rect, rect.X, rect.Y, fmt.Sprintf("F7 Projections — horizon: %d months", horizonMonths), style)
}

// RenderCurve draws one demand/supply curve (PRJ-1): a label line, then
// history as solid Braille and projection as dim Braille (widgets.
// BrailleChart, reused not reimplemented), confidence bands as dim dots,
// threshold lines, and queued-decision step markers. A curve whose status
// is not StatusAvailable renders "unavailable: <reason>" or "not yet
// unlocked" instead of a chart (PRJ-6 — never a blank or fabricated flat
// line). A pure function of its arguments (SF-8).
func RenderCurve(buf *core.Buffer, rect core.Rect, c Curve, palette widgets.Palette) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	history, projection, band, threshold, marker := curveStyles(palette)

	drawText(buf, rect, rect.X, rect.Y, curveLabelLine(c, rect.W), tcell.StyleDefault)
	if c.Status != StatusAvailable {
		statusLine := "unavailable: " + c.UnavailableReason
		if c.Status == StatusNotUnlocked {
			statusLine = "not yet unlocked: " + c.UnavailableReason
		}
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, statusLine, palette.Style(widgets.TokenWarning))
		}
		return
	}

	chartRect := core.Rect{X: rect.X, Y: rect.Y + 1, W: rect.W, H: rect.H - 1}
	if chartRect.H <= 0 {
		return
	}

	// The line chart itself: reused widgets.BrailleChart, not a screen-
	// local reimplementation (PRJ-1's explicit reuse requirement).
	widgets.BrailleChart(buf, chartRect, c.History, c.Projection, history, projection)

	renderCurveOverlays(buf, chartRect, c, band, threshold, marker)
}

// curveLabelLine builds the label row: key, then the human label, then a
// compact [m+<offset> <label>] note per queued decision marker (so the
// marker is readable even though its on-chart tick is only a few dots).
//
// maxWidth is the drawn width in columns (RenderCurve passes rect.W). The
// row is drawn clipped to that width anyway, so building more than maxWidth
// columns of label is wasted work: the builder stops before a marker note
// that would push the row past maxWidth. This bounds the work to the drawn
// width instead of the marker count — a hostile "f7.projections" patch can
// carry ~58k markers and still fit the 1 MiB wire cap, and the pre-fix
// `s += fmt.Sprintf(...)` per marker was O(n²) string copies that froze the
// render goroutine for seconds per frame (SEC-061). strings.Builder makes
// the build linear in what is actually written, and the width stop makes
// it O(width) regardless of input.
func curveLabelLine(c Curve, maxWidth int) string {
	var b strings.Builder
	b.WriteString(c.Key)
	if c.Label != "" {
		b.WriteString("  ")
		b.WriteString(c.Label)
	}
	for _, m := range c.Markers {
		note := fmt.Sprintf("  [m+%d %s]", m.MonthOffset, m.Label)
		if maxWidth > 0 && b.Len()+len(note) > maxWidth {
			break
		}
		b.WriteString(note)
	}
	return b.String()
}

// renderCurveOverlays draws the confidence bands (dim dots), threshold
// lines (danger), and decision markers (warning) on top of the chart
// BrailleChart just drew. It must reproduce widgets.BrailleChart's value
// scale and horizontal span exactly so the overlays land on the same
// dots the chart used — combinedRange/dotRow/projectionCol below are
// deliberate mirrors of widgets' unexported combinedRange/plotSeries
// normalisation, kept in lockstep by the alignment test in render_test.go.
func renderCurveOverlays(buf *core.Buffer, rect core.Rect, c Curve, band, threshold, marker tcell.Style) {
	if len(c.History) == 0 && len(c.Projection) == 0 {
		return
	}
	dotsW := rect.W * 2
	dotsH := thresholdLineCap(rect.H)
	min, max := combinedRange(c.History, c.Projection)

	total := len(c.History) + len(c.Projection)
	histSpan := dotsW
	if total > 0 {
		histSpan = len(c.History) * dotsW / total
	}
	if len(c.History) > 0 && histSpan < 1 {
		histSpan = 1
	}
	projStart := histSpan
	projSpan := dotsW - histSpan

	// Confidence bands: one dim dot per projection point, at the upper and
	// lower bound values.
	if (len(c.ConfidenceUpper) > 0 || len(c.ConfidenceLower) > 0) && len(c.Projection) > 0 {
		bands := widgets.NewBrailleCanvas(rect.W, rect.H)
		n := len(c.Projection)
		for i := 0; i < n; i++ {
			col := projectionCol(i, n, projStart, projSpan)
			if i < len(c.ConfidenceUpper) {
				bands.SetDot(col, dotRow(c.ConfidenceUpper[i], min, max, dotsH))
			}
			if i < len(c.ConfidenceLower) {
				bands.SetDot(col, dotRow(c.ConfidenceLower[i], min, max, dotsH))
			}
		}
		overlayCanvas(buf, rect, bands, band)
	}

	// Threshold lines: a horizontal line across the chart at each
	// threshold's value. The chart has at most dotsH distinct dot-rows, so
	// at most dotsH threshold lines are visually distinct — draw each dot-row
	// at most once and stop as soon as every row is covered (SEC-091). This
	// bounds the line-rasterization work by the drawn viewport (dotsH is
	// thresholdLineCap, a viewport-derived bound — GR#15) rather than by the
	// wire threshold count: a hostile "f7.projections" patch can carry ~80k
	// thresholds and still fit the 1 MiB wire cap, and the pre-fix loop drew
	// a full dotsW-wide line per threshold every render tick. Re-drawing an
	// already-lit row is a no-op (SetDot ORs the dot mask), so skipping it
	// changes nothing visual — the output is byte-identical to drawing every
	// threshold, only the redundant passes are gone.
	if len(c.Thresholds) > 0 && dotsH > 0 {
		thr := widgets.NewBrailleCanvas(rect.W, rect.H)
		seen := make([]bool, dotsH)
		drawn := 0
		for _, t := range c.Thresholds {
			if drawn >= dotsH {
				break
			}
			row := dotRow(t.Value, min, max, dotsH)
			if seen[row] {
				continue
			}
			seen[row] = true
			drawn++
			for col := 0; col < dotsW; col++ {
				thr.SetDot(col, row)
			}
		}
		overlayCanvas(buf, rect, thr, threshold)
	}

	// Decision markers: a short vertical tick at each marker's month.
	if len(c.Markers) > 0 && len(c.Projection) > 0 {
		marks := widgets.NewBrailleCanvas(rect.W, rect.H)
		n := len(c.Projection)
		for _, m := range c.Markers {
			idx := m.MonthOffset
			if idx < 0 {
				idx = 0
			}
			if idx >= n {
				idx = n - 1
			}
			col := projectionCol(idx, n, projStart, projSpan)
			row := dotRow(c.Projection[idx], min, max, dotsH)
			// A 3-dot vertical tick centred on the value, so a marker reads
			// as a deliberate mark rather than one stray dot.
			for dy := -1; dy <= 1; dy++ {
				r := row + dy
				if r < 0 || r >= dotsH {
					continue
				}
				marks.SetDot(col, r)
			}
		}
		overlayCanvas(buf, rect, marks, marker)
	}
}

// RenderCurves draws the demand/supply curve list (PRJ-1) stacked top-to-
// bottom in deterministic producer order — the order the engine sent them
// (GR#21), never re-sorted — each curve in a curveBandRows-row band. It is
// the per-tick entry point for this screen's curves, and it owns the
// total-curve-count bound (SEC-091, second half): a hostile
// "f7.projections" patch can carry ~10k curves and still fit the 1 MiB
// wire cap, and the pre-fix per-curve loop called RenderCurve once per
// curve, allocating 2+ Braille canvases each (measured at 12.2 ms /
// 11.6 MB per tick at 10k curves) with no bound tying that work to the
// drawn viewport. It returns the number of curves it actually drew, so a
// caller can render an honest "… and N more" indicator rather than
// silently padding the list.
//
// Dropping semantics: WHICH curves to draw is a UX/layout call, so this
// picks the deterministic default — the first curveRenderCap(rect.H)
// curves in producer order; every later curve is dropped and draws nothing
// (no label, no chart, no canvas allocation). A caller that wants a
// highest-salience selection can reorder the slice before calling (the
// bound and the "first-N" rule live here, not in a magic number). The cap
// itself is curveRenderCap(rect.H), derived from the render viewport the
// same way thresholdLineCap derives the threshold-line cap (GR#15): one
// curve occupies curveBandRows rows, so a rect.H-tall viewport holds at
// most rect.H/curveBandRows curves.
func RenderCurves(buf *core.Buffer, rect core.Rect, curves []Curve, palette widgets.Palette) int {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return 0
	}
	cap := curveRenderCap(rect.H)
	if cap <= 0 {
		return 0
	}
	if len(curves) > cap {
		curves = curves[:cap]
	}
	y := rect.Y
	for _, c := range curves {
		band := core.Rect{X: rect.X, Y: y, W: rect.W, H: curveBandRows}
		RenderCurve(buf, band, c, palette)
		y += curveBandRows
	}
	return len(curves)
}

// RenderCrossing draws one contracted-vs-internal demand crossing chart
// (PRJ-3 / §36): the internal-demand growth curve and the contracted-away
// capacity curve on one chart (two overlapping lines over one shared
// value scale), annotated with the crossing month (or "no crossing within
// horizon"). A crossing whose status is not StatusAvailable renders
// "unavailable: <reason>" instead (PRJ-6).
func RenderCrossing(buf *core.Buffer, rect core.Rect, x Crossing, palette widgets.Palette) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	history, projection, _, threshold, _ := curveStyles(palette)

	crossingText := "no crossing within horizon"
	if x.CrossingMonth >= 0 {
		crossingText = fmt.Sprintf("crossing +%dmo", x.CrossingMonth)
	}
	label := x.Key
	if x.Label != "" {
		label += "  " + x.Label
	}
	drawText(buf, rect, rect.X, rect.Y, label+"  "+crossingText, tcell.StyleDefault)

	if x.Status != StatusAvailable {
		statusLine := "unavailable: " + x.UnavailableReason
		if x.Status == StatusNotUnlocked {
			statusLine = "not yet unlocked: " + x.UnavailableReason
		}
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, statusLine, palette.Style(widgets.TokenWarning))
		}
		return
	}

	chartRect := core.Rect{X: rect.X, Y: rect.Y + 1, W: rect.W, H: rect.H - 1}
	if chartRect.H <= 0 {
		return
	}
	if len(x.InternalDemand) == 0 && len(x.ContractedCapacity) == 0 {
		return
	}

	dotsW := chartRect.W * 2
	dotsH := chartRect.H * 4
	min, max := combinedRange(x.InternalDemand, x.ContractedCapacity)

	// Two overlapping lines over one value scale. widgets.BrailleChart is
	// a history-then-projection (sequential) two-series chart, so it cannot
	// express two same-timeframe series; the crossing chart composes the
	// same BrailleCanvas dot-plane instead (the same composition
	// ui.screen.demo's pyramid.go uses for a chart shape widgets has no
	// primitive for — see doc.go). The contracted-capacity (dim) line is
	// drawn first, internal-demand (solid) second, so solid wins any cell
	// the two share — the same history-over-projection priority
	// BrailleChart documents for its own boundary.
	capacity := widgets.NewBrailleCanvas(chartRect.W, chartRect.H)
	plotSeriesLine(capacity, x.ContractedCapacity, dotsW, dotsH, min, max)
	overlayCanvas(buf, chartRect, capacity, projection)

	demand := widgets.NewBrailleCanvas(chartRect.W, chartRect.H)
	plotSeriesLine(demand, x.InternalDemand, dotsW, dotsH, min, max)
	overlayCanvas(buf, chartRect, demand, history)

	// A tick at the crossing month itself (when one exists within the
	// horizon), so the crossing year is visible on the chart as well as in
	// the label.
	if x.CrossingMonth >= 0 && len(x.InternalDemand) > 0 {
		idx := x.CrossingMonth
		if idx >= len(x.InternalDemand) {
			idx = len(x.InternalDemand) - 1
		}
		col := seriesCol(idx, len(x.InternalDemand), dotsW)
		row := dotRow(x.InternalDemand[idx], min, max, dotsH)
		ticks := widgets.NewBrailleCanvas(chartRect.W, chartRect.H)
		for dy := -1; dy <= 1; dy++ {
			r := row + dy
			if r < 0 || r >= dotsH {
				continue
			}
			ticks.SetDot(col, r)
		}
		overlayCanvas(buf, chartRect, ticks, threshold)
	}
}

// RenderRateOutlook draws the §45 national base-rate cycle curve (PRJ-4):
// read-only — the player positions for it, never controls it. Rendered as
// the same history-solid/projection-dim line chart as every other curve,
// with no confidence bands, thresholds, or markers (a rate outlook has
// none — the rate is a given, not a decision). A non-available outlook
// renders "unavailable: <reason>" (PRJ-6).
func RenderRateOutlook(buf *core.Buffer, rect core.Rect, r RateOutlook, palette widgets.Palette) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	history, projection, _, _, _ := curveStyles(palette)

	drawText(buf, rect, rect.X, rect.Y, "Base rate outlook (§45)", tcell.StyleDefault)
	if r.Status != StatusAvailable {
		statusLine := "unavailable: " + r.UnavailableReason
		if r.Status == StatusNotUnlocked {
			statusLine = "not yet unlocked: " + r.UnavailableReason
		}
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, statusLine, palette.Style(widgets.TokenWarning))
		}
		return
	}

	chartRect := core.Rect{X: rect.X, Y: rect.Y + 1, W: rect.W, H: rect.H - 1}
	if chartRect.H <= 0 {
		return
	}
	widgets.BrailleChart(buf, chartRect, r.History, r.Projection, history, projection)
}

// RenderSlowFuse is PRJ-5 / A5's cross-screen reuse point: the exported
// projection-rendering primitive a >60-month (Slow-Fuse) decision's
// confirmation UI calls to render the decision's consequence curve inline,
// instead of that screen reimplementing a projection chart. It renders a
// Consequence exactly as RenderCurve renders a curve's history+projection
// (same BrailleChart, same solid/dim idiom), so a consequence reads as a
// projection curve, never a bare number. It does not re-enforce the
// >60-month rule — the Slow-Fuse gate (engine.projections AC-5) owns that;
// it renders whatever consequence it is given.
func RenderSlowFuse(buf *core.Buffer, rect core.Rect, consequence Consequence, palette widgets.Palette) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	history, projection, _, _, _ := curveStyles(palette)

	label := consequence.Label
	if label == "" {
		label = "consequence"
	}
	drawText(buf, rect, rect.X, rect.Y, fmt.Sprintf("%s  (+%dmo)", label, consequence.FuseMonths), tcell.StyleDefault)

	chartRect := core.Rect{X: rect.X, Y: rect.Y + 1, W: rect.W, H: rect.H - 1}
	if chartRect.H <= 0 {
		return
	}
	widgets.BrailleChart(buf, chartRect, consequence.History, consequence.Projection, history, projection)
}

// DrillTargets returns the drill-through source identities this screen
// supplies for registration into ui.dash's (MOD-038) drill-through graph,
// per SF-5: one per curve, one per crossing, and the rate-outlook figure.
// Every target uses this screen's single subscribed view
// (ViewSubscriptionName, "f7.projections") as its ViewName and a
// sub-entity path as its EntityID, mirroring ui.screen.ticker's canonical
// dash.DrillTarget shape — GR#3 forbids a parallel bespoke copy, and the
// widget identity is the caller's tile ID in dash's model, not part of a
// DrillTarget. Registration, navigation and dead-end detection remain
// MOD-038's job; this screen only produces the source list.
func DrillTargets(curves []Curve, crossings []Crossing) []dash.DrillTarget {
	out := make([]dash.DrillTarget, 0, len(curves)+len(crossings)+1)
	for _, c := range curves {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "curve." + c.Key})
	}
	for _, x := range crossings {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "crossing." + x.Key})
	}
	out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "rate"})
	return out
}

// --- scale/normalisation helpers --------------------------------------
//
// These reproduce widgets.BrailleChart's value scale and horizontal span
// (its combinedRange/plotSeries normalisation are unexported) so this
// package's overlays land on the exact dots the chart drew. They are kept
// in lockstep with widgets.BrailleChart by render_test.go's alignment
// test, which asserts a threshold at the series maximum lands on the
// chart's own top edge.

// combinedRange returns the min/max across both series (mirror of
// widgets.combinedRange). Both empty returns (0, 0).
func combinedRange(a, b []float64) (min, max float64) {
	first := true
	scan := func(s []float64) {
		for _, v := range s {
			if first {
				min, max = v, v
				first = false
				continue
			}
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
	}
	scan(a)
	scan(b)
	return min, max
}

// thresholdLineCap is the maximum number of threshold lines a chart of the
// given cell height can meaningfully draw — the data-driven bound (GR#15)
// that caps the threshold loop in renderCurveOverlays (SEC-091).
//
// Derivation: a threshold is a horizontal line drawn at exactly one Braille
// dot-row, and a BrailleCanvas packs four dot-rows per cell row, so a chart
// h cells tall has exactly h*4 distinct dot-rows (the same rect.H*4 geometry
// renderCurveOverlays uses for dotsH). dotRow maps every threshold value onto
// one of those rows, so any threshold beyond the first h*4 distinct rows is
// guaranteed to land on a row an earlier threshold already occupied — drawing
// it again is redundant work, not additional information. Bounding the loop
// to h*4 drawn lines therefore makes per-tick line-rasterization work
// O(dotsW * h*4), the chart's own drawn area, rather than proportional to the
// attacker-controlled wire threshold count (a hostile "f7.projections" patch
// can carry ~80k thresholds and still fit the 1 MiB wire cap; the pre-fix
// loop drew a full dotsW-wide line per threshold, measured at 23.7 ms per
// render tick). The bound is derived from the render viewport (a runtime
// value), never a hardcoded literal, matching the SEC-061 fix's "bound the
// work by the drawn width" idiom.
func thresholdLineCap(rectH int) int {
	if rectH <= 0 {
		return 0
	}
	return rectH * 4
}

// curveBandRows is the number of viewport rows one curve occupies in
// RenderCurves' stacked layout: one label row plus one chart row (a
// StatusAvailable curve's chart requires a second row; a non-available
// curve draws its status line there instead). It is layout geometry — a
// terminal cell row is one unit — not a data-derived count, exactly as
// thresholdLineCap's "4" is Braille dot-row geometry.
const curveBandRows = 2

// curveRenderCap is the total-curve-count bound (SEC-091, second half):
// the maximum number of curves RenderCurves will draw in one tick. Derived
// from the render viewport the same way thresholdLineCap derives the
// threshold-line cap (GR#15): each curve occupies curveBandRows rows, so a
// viewport rectH rows tall holds at most rectH/curveBandRows curves.
// Degenerate (non-positive) heights cap to zero.
func curveRenderCap(viewportH int) int {
	if viewportH <= 0 {
		return 0
	}
	return viewportH / curveBandRows
}

// dotRow maps value v to a dot row within dotsH (0 = top), mirroring
// widgets.BrailleChart's plotSeries dotRow: higher value -> lower row,
// flat series -> the middle row.
func dotRow(v, min, max float64, dotsH int) int {
	if dotsH <= 0 {
		return 0
	}
	if max <= min {
		return dotsH / 2
	}
	norm := (v - min) / (max - min)
	row := dotsH - 1 - int(norm*float64(dotsH-1)+0.5)
	if row < 0 {
		row = 0
	}
	if row > dotsH-1 {
		row = dotsH - 1
	}
	return row
}

// projectionCol maps projection index i (0..n-1) to a dot column within
// the projection span [projStart, projStart+projSpan), mirroring
// widgets.BrailleChart's plotSeries dotCol for the projection series.
func projectionCol(i, n, projStart, projSpan int) int {
	if n == 1 {
		return projStart
	}
	return projStart + i*(projSpan-1)/(n-1)
}

// seriesCol maps series index i (0..n-1) to a dot column across the full
// dot width dotsW, mirroring projectionCol with a zero start and full span.
func seriesCol(i, n, dotsW int) int {
	if n == 1 {
		return 0
	}
	return i * (dotsW - 1) / (n - 1)
}

// plotSeriesLine draws series as a connected line across the full dot
// width, mirroring widgets' unexported plotSeries/brailleLine for the one
// chart shape (two overlapping same-timeframe series, PRJ-3's crossing)
// that widgets.BrailleChart's sequential history/projection split cannot
// express. Composed over widgets.BrailleCanvas, so the dot-plane
// addressing is reused even though the line raster itself is local.
func plotSeriesLine(canvas *widgets.BrailleCanvas, series []float64, dotsW, dotsH int, min, max float64) {
	n := len(series)
	if n == 0 || dotsH <= 0 {
		return
	}
	prevX, prevY := seriesCol(0, n, dotsW), dotRow(series[0], min, max, dotsH)
	canvas.SetDot(prevX, prevY)
	for i := 1; i < n; i++ {
		x, y := seriesCol(i, n, dotsW), dotRow(series[i], min, max, dotsH)
		brailleLine(canvas, prevX, prevY, x, y)
		prevX, prevY = x, y
	}
}

// brailleLine sets every dot on the Bresenham line from (x0,y0) to (x1,y1)
// inclusive — the standard integer-only line algorithm (no floating point,
// no allocation), mirroring widgets' unexported brailleLine.
func brailleLine(c *widgets.BrailleCanvas, x0, y0, x1, y1 int) {
	dx := x1 - x0
	if dx < 0 {
		dx = -dx
	}
	sx := 1
	if x0 > x1 {
		sx = -1
	}
	dy := y1 - y0
	if dy > 0 {
		dy = -dy
	}
	sy := 1
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		c.SetDot(x0, y0)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

// overlayCanvas composites a BrailleCanvas's dots onto an already-drawn
// chart by OR-ing the overlay's dot masks into each cell's existing
// Braille pattern, applying overlayStyle to the whole cell (a terminal
// cell has one Style, not one per dot — UI-SPEC §2's documented
// limitation, the same one widgets.BrailleChart documents for its own
// history/projection boundary).
func overlayCanvas(buf *core.Buffer, rect core.Rect, c *widgets.BrailleCanvas, style tcell.Style) {
	if buf == nil || c == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	for cy := 0; cy < rect.H; cy++ {
		for cx := 0; cx < rect.W; cx++ {
			m := c.Mask(cx, cy)
			if m == 0 {
				continue
			}
			x, y := rect.X+cx, rect.Y+cy
			existing := buf.Get(x, y)
			combined := m
			if existing.Rune >= brailleBase && existing.Rune < brailleBase+0x100 {
				combined |= uint8(existing.Rune - brailleBase)
			}
			buf.Set(x, y, brailleBase+rune(combined), style)
		}
	}
}
