package dash

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// tileHeight returns a fixed render height per tile kind. Fixed (not
// content-derived) so layout is deterministic and a resize never changes
// tile order or count (AC-12).
func tileHeight(kind TileKind) int {
	switch kind {
	case KindGauge:
		return 1
	case KindBigNum:
		return 3
	case KindSpark:
		return 3
	case KindTable:
		return 6
	case KindDiagram:
		return 6
	case KindAlerts:
		return 4
	case KindMiniMap:
		return 5
	default:
		return 3
	}
}

// Render draws every tile of l into buf, stacked vertically in tile
// order within rect (a slice, so order is deterministic — AC-12). Each
// tile delegates to its corresponding ui.widgets function via RenderTile
// (AC-1: shared widget set, not bespoke per-tile drawing).
func Render(buf *core.Buffer, rect core.Rect, l Layout, palette widgets.Palette, base tcell.Style) {
	y := rect.Y
	for _, t := range l.tiles {
		h := tileHeight(t.kind)
		if y >= rect.Y+rect.H {
			return
		}
		tileRect := core.Rect{X: rect.X, Y: y, W: rect.W, H: h}
		RenderTile(buf, tileRect, t, palette, base)
		y += h + 1
	}
}

// Render draws the dashboard's current layout under the read lock, so it
// is safe to run concurrently with the editor's save/mutation path
// (AC-13): the editor holds the write lock while mutating, so a render
// either sees the layout fully before or fully after a mutation, never a
// torn state.
func (d *Dashboard) Render(buf *core.Buffer, rect core.Rect, palette widgets.Palette, base tcell.Style) {
	if err := d.checkNotCopied(corr(), map[string]any{"method": "Render"}); err != nil {
		return
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if err := d.checkNotCopied(corr(), map[string]any{"method": "Render"}); err != nil {
		return
	}
	Render(buf, rect, d.layout, palette, base)
}

// RenderTile draws one tile of any kind into rect, dispatching to the
// tile type's ui.widgets function. A nil buffer or a degenerate rect is
// a no-op (mirrors the widgets' own degenerate-rect discipline).
func RenderTile(buf *core.Buffer, rect core.Rect, t Tile, palette widgets.Palette, base tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	switch t.kind {
	case KindBigNum:
		renderBignum(buf, rect, t.bignum, palette, base)
	case KindGauge:
		renderGauge(buf, rect, t.gauge, palette, base)
	case KindSpark:
		renderSpark(buf, rect, t.spark, base)
	case KindTable:
		renderTable(buf, rect, t.table, base)
	case KindMiniMap:
		renderMinimap(buf, rect, t.minimap, palette)
	case KindAlerts:
		renderAlerts(buf, rect, t.alerts, palette, base)
	case KindDiagram:
		renderDiagram(buf, rect, t.diagram, palette, base)
	}
}

func renderBignum(buf *core.Buffer, rect core.Rect, s BignumSpec, palette widgets.Palette, base tcell.Style) {
	state := widgets.BigNumState{
		Label:      s.Label,
		ValueText:  s.ValueText,
		Prev:       s.Prev,
		Curr:       s.Curr,
		Series:     s.Series,
		Thresholds: s.Thresholds,
	}
	widgets.BigNum(buf, rect, state, palette, base)
}

func renderGauge(buf *core.Buffer, rect core.Rect, s GaugeSpec, palette widgets.Palette, base tcell.Style) {
	// One row for the gauge fill, one for the label when there is room.
	widgets.Gauge(buf, rect, s.Value, s.Thresholds, palette, base)
	if s.Label != "" && rect.H >= 2 {
		writeText(buf, rect.X, rect.Y+1, rect.W, s.Label, base)
	}
}

func renderSpark(buf *core.Buffer, rect core.Rect, s SparkSpec, base tcell.Style) {
	if s.Label != "" {
		writeText(buf, rect.X, rect.Y, rect.W, s.Label, base)
	}
	if rect.H >= 2 {
		widgets.Sparkline(buf, core.Rect{X: rect.X, Y: rect.Y + 1, W: rect.W, H: 1}, s.Series, base)
	}
}

func renderTable(buf *core.Buffer, rect core.Rect, s *TableSpec, base tcell.Style) {
	if s == nil {
		return
	}
	// Visible rows are a simple top-down window over the (unsorted) rows;
	// sort/filter is driven by the caller via TableSpec.SortedRows/Filter
	// and re-rendered with the resulting index set (see RenderTableRows).
	rows := make([]int, 0, s.NumRows())
	for i := range s.Rows {
		rows = append(rows, i)
	}
	widgets.DrawTable(buf, rect, s, s.Columns, widgets.VisibleRows(rows, widgets.Window{Height: rect.H - 1}), base, base)
}

// RenderTableRows draws a table tile with an explicit, caller-computed
// row index set (typically SortedRows' or Filter's output). This is the
// dashboard-level sort/filter render path — it wires the tile's data
// through ui.widgets' DrawTable exactly as the un-sorted path does.
func RenderTableRows(buf *core.Buffer, rect core.Rect, s *TableSpec, rows []int, base tcell.Style) {
	if s == nil || buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	widgets.DrawTable(buf, rect, s, s.Columns, widgets.VisibleRows(rows, widgets.Window{Height: rect.H - 1}), base, base)
}

func renderMinimap(buf *core.Buffer, rect core.Rect, s MinimapSpec, palette widgets.Palette) {
	width := s.Width
	if width <= 0 {
		width = len(s.Values)
	}
	if width <= 0 {
		return
	}
	ramp := widgets.DefaultHeatRamp(palette)
	// Draw the value grid as background-colour ramps over the tile rect;
	// min/max derive from the data (GR#15: expected range comes from the
	// values, not a hardcoded constant).
	min, max := dataRange(s.Values)
	widgets.Heatmap(buf, rect, s.Values, width, min, max, ramp)
}

func renderAlerts(buf *core.Buffer, rect core.Rect, s AlertsSpec, palette widgets.Palette, base tcell.Style) {
	widgets.Border(buf, rect, widgets.Unfocused, s.Label, base)
	for i, e := range s.Entries {
		y := rect.Y + 1 + i
		if y >= rect.Y+rect.H-1 {
			break
		}
		style := base
		switch e.Severity {
		case SeverityWarning:
			style = palette.Style(widgets.TokenWarning)
		case SeverityDanger:
			style = palette.Style(widgets.TokenDanger)
		}
		writeText(buf, rect.X+1, y, rect.W-2, e.Text, style)
	}
}

func renderDiagram(buf *core.Buffer, rect core.Rect, s *DiagramSpec, palette widgets.Palette, base tcell.Style) {
	// A diagram tile renders its embedded ui.diagrams output; ui.dash's
	// job is to carry the hit-test mapping, not to lay out the diagram
	// (that is ui.diagrams, MOD-037). Until that package lands, the tile
	// renders a bordered placeholder and — to keep the drill-through
	// contract visible — a marker per hit-test element at its region.
	_ = palette
	widgets.Border(buf, rect, widgets.Unfocused, "diagram", base)
	if s == nil {
		return
	}
	for _, h := range s.Hits {
		if h.Region.X >= rect.X && h.Region.X < rect.X+rect.W &&
			h.Region.Y >= rect.Y && h.Region.Y < rect.Y+rect.H {
			buf.Set(h.Region.X, h.Region.Y, '●', base)
		}
	}
}

// writeText writes s into buf at (x, y), clipped to maxW cells.
func writeText(buf *core.Buffer, x, y, maxW int, s string, style tcell.Style) {
	cx := x
	limit := x + maxW
	for _, r := range s {
		if cx >= limit {
			break
		}
		buf.Set(cx, y, r, style)
		cx++
	}
}

// dataRange returns the min/max of values. Empty (or NaN-only) input
// returns (0, 0) — the degenerate no-op range a heatmap renders as a
// flat ramp rather than panicking on division.
func dataRange(values []float64) (min, max float64) {
	first := true
	for _, v := range values {
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
	return min, max
}

// ProjectionThreshold is a horizontal threshold line on a projections
// pane (UI-SPEC §4's "threshold lines").
type ProjectionThreshold struct {
	Value float64
	Label string
}

// ProjectionDecision is a queued decision marker: a step change at a
// projected date (UI-SPEC §4's "decision markers the player has queued",
// e.g. "a planned school appears as a capacity step before it's built").
// At is an index into the combined history+projection timeline.
type ProjectionDecision struct {
	At    int
	Label string
}

// Projection is a projections-pane series: history (solid), projection
// (dim), threshold lines, and queued decision markers (AC-8).
type Projection struct {
	History    []float64
	Projection []float64
	Thresholds []ProjectionThreshold
	Decisions  []ProjectionDecision
}

// RenderProjection draws the four-element projections-pane idiom into
// rect: history as a solid Braille line, projection as a dim Braille
// line (widgets.BrailleChart's two-series mode), threshold lines as
// horizontal '─' rows, and decision markers as '●' cells at their
// projected column (AC-8). All four are visually distinct by
// construction: braille glyphs for the series, box-drawing for
// thresholds, a distinct marker glyph for decisions, and the tcell Dim
// attribute distinguishing projection from history.
func RenderProjection(buf *core.Buffer, rect core.Rect, p Projection, base tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	// Braille chart maps the combined series into a 2x4 dot grid per
	// cell; history solid, projection dim.
	widgets.BrailleChart(buf, rect, p.History, p.Projection, base, base.Dim(true))

	min, max := combinedDataRange(p.History, p.Projection)
	total := len(p.History) + len(p.Projection)

	for _, th := range p.Thresholds {
		y := scaleToRow(th.Value, min, max, rect)
		for x := rect.X; x < rect.X+rect.W; x++ {
			buf.Set(x, y, '─', base.Dim(true))
		}
	}
	for _, d := range p.Decisions {
		x := scaleToCol(d.At, total, rect)
		row := rect.Y + rect.H - 1
		buf.Set(x, row, '●', base)
	}
}

// combinedDataRange returns min/max over both series (or (0,0) if both
// empty), the same degenerate-safe shape as dataRange.
func combinedDataRange(a, b []float64) (float64, float64) {
	merged := make([]float64, 0, len(a)+len(b))
	merged = append(merged, a...)
	merged = append(merged, b...)
	return dataRange(merged)
}

// scaleToRow maps value in [min,max] to a row within rect (0 -> top,
// max -> bottom). A zero-span range (min==max) pins to the middle row so
// it never divides by zero.
func scaleToRow(value, min, max float64, rect core.Rect) int {
	if max == min {
		return rect.Y + rect.H/2
	}
	frac := (value - min) / (max - min)
	row := rect.Y + rect.H - 1 - int(frac*float64(rect.H-1))
	if row < rect.Y {
		row = rect.Y
	}
	if row >= rect.Y+rect.H {
		row = rect.Y + rect.H - 1
	}
	return row
}

// scaleToCol maps an index into a total-length timeline to a column
// within rect.
func scaleToCol(index, total int, rect core.Rect) int {
	if total <= 0 {
		return rect.X
	}
	col := rect.X + (index * rect.W / total)
	if col >= rect.X+rect.W {
		col = rect.X + rect.W - 1
	}
	if col < rect.X {
		col = rect.X
	}
	return col
}
