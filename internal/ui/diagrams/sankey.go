package diagrams

import (
	"math"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// budgetLabel is the fixed label of the aggregate budget node (the §54
// Fiscal Circuit's single sink of sources and source of sinks). Its width
// is also the minimum label-column width, so the label column is at least
// this wide regardless of the caller's source/sink names.
const budgetLabel = "budget"

// RenderSankey renders the §54 Fiscal Circuit as proportional block-width
// money bands (AC-4, US-2): a top block of source bands, a full-width
// budget band, then a bottom block of sink bands. Each band's width is
//
//	round(amount / stageTotal * bandMaxWidth)
//
// where bandMaxWidth is the buffer width minus the label column (at least
// the width of the budget label) minus one separator cell. This is the
// documented rounding tolerance (AC-4): character-cell widths are
// discrete, so a band width is the per-cell integer rounding of a
// continuous ratio — never a fuzzy or wall-clock match.
//
// Degenerate inputs (AC-7): zero sources AND zero sinks → zero Result, nil
// error. An unbalanced source-total vs sink-total is not an error: each
// stage is normalised by its own total (the documented partial state). A
// nil buf → zero Result.
func RenderSankey(buf *core.Buffer, topo SankeyTopology, opts Options) (Result, error) {
	if buf == nil {
		return Result{}, nil
	}
	if len(topo.Sources) == 0 && len(topo.Sinks) == 0 {
		return Result{}, nil
	}
	bufW, _ := buf.Size()
	labelW := maxSankeyLabel(topo)
	bandMax := bufW - labelW - 1
	if bandMax < 1 {
		bandMax = 1
	}

	srcTotal := flowTotal(topo.Sources)
	sinkTotal := flowTotal(topo.Sinks)
	bandStyle := opts.Palette.Style(widgets.TokenMoney)

	var hits []Hit
	y := 0
	for _, f := range topo.Sources {
		w := bandWidth(f.Amount, srcTotal, bandMax)
		drawSankeyRow(buf, y, labelW, f.Name, w, bandStyle)
		hits = append(hits, Hit{Rect: core.Rect{X: labelW + 1, Y: y, W: w, H: 1}, ID: f.ID})
		y++
	}
	drawBudgetRow(buf, y, labelW, bandMax, bandStyle)
	y++
	for _, f := range topo.Sinks {
		w := bandWidth(f.Amount, sinkTotal, bandMax)
		drawSankeyRow(buf, y, labelW, f.Name, w, bandStyle)
		hits = append(hits, Hit{Rect: core.Rect{X: labelW + 1, Y: y, W: w, H: 1}, ID: f.ID})
		y++
	}

	return Result{Region: core.Rect{X: 0, Y: 0, W: labelW + 1 + bandMax, H: y}, Hits: hits}, nil
}

// bandWidth returns the discrete cell width of a band for amount within a
// stage of total, against a maximum of maxWidth cells. A non-positive
// amount, total, or maxWidth yields zero (defensive: a negative money flow
// is malformed and renders as no band rather than a negative-width band).
func bandWidth(amount, total float64, maxWidth int) int {
	if total <= 0 || amount <= 0 || maxWidth <= 0 {
		return 0
	}
	return int(math.Round(amount / total * float64(maxWidth)))
}

// flowTotal sums the (non-negative) amounts of a set of flows. Negative
// amounts contribute zero, so a stage with only negative amounts totals
// zero and every band is zero-width (a documented degenerate state).
func flowTotal(flows []SankeyFlow) float64 {
	var t float64
	for _, f := range flows {
		if f.Amount > 0 {
			t += f.Amount
		}
	}
	return t
}

// maxSankeyLabel returns the label-column width: at least the width of the
// budget label, and wide enough for the longest source or sink name.
func maxSankeyLabel(topo SankeyTopology) int {
	w := runeWidth(budgetLabel)
	for _, f := range topo.Sources {
		if lw := runeWidth(f.Name); lw > w {
			w = lw
		}
	}
	for _, f := range topo.Sinks {
		if lw := runeWidth(f.Name); lw > w {
			w = lw
		}
	}
	return w
}

// drawSankeyRow draws a source/sink row: the name in the label column, then
// a run of width solid blocks in the band colour.
func drawSankeyRow(buf *core.Buffer, y, labelW int, name string, width int, bandStyle tcell.Style) {
	drawText(buf, 0, y, name, tcell.StyleDefault)
	for x := 0; x < width; x++ {
		buf.Set(labelW+1+x, y, '█', bandStyle)
	}
}

// drawBudgetRow draws the aggregate budget node: the budget label and a
// full-width run of partial blocks (visually distinct from the money
// bands' solid blocks). It carries no caller SourceID, so it contributes no
// hit.
func drawBudgetRow(buf *core.Buffer, y, labelW, bandMax int, bandStyle tcell.Style) {
	drawText(buf, 0, y, budgetLabel, tcell.StyleDefault)
	for x := 0; x < bandMax; x++ {
		buf.Set(labelW+1+x, y, '░', bandStyle)
	}
}
