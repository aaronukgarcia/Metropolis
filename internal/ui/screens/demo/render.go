package demo

import (
	"fmt"
	"math"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// drawText writes s left-to-right starting at (x, y), clipped to
// rect's right edge — the small shared primitive every text-row render
// function below uses, mirroring widgets.DrawTable's own drawRow
// clipping discipline (internal/ui/widgets/table.go).
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

func bar(value, max float64, width int) string {
	if max <= 0 || width <= 0 {
		return ""
	}
	n := int(math.Round(value / max * float64(width)))
	if n < 0 {
		n = 0
	}
	if n > width {
		n = width
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = '#'
	}
	return string(b)
}

// HoursTotal sums hours's Hours field — DEMO-4's "rendered totals sum to
// a value no test hardcodes" figure, drawn from the fixture rather than
// computed by the test itself.
func HoursTotal(hours []ActivityHours) float64 {
	var total float64
	for _, h := range hours {
		total += h.Hours
	}
	return total
}

// RenderHoursByActivity draws the §42 "how your city spends Saturday"
// hours-by-activity view (DEMO-4): one row per activity (a proportional
// '#' bar plus its exact hours figure), followed by a total-hours
// footer row computed via HoursTotal — never a hardcoded figure.
func RenderHoursByActivity(buf *core.Buffer, rect core.Rect, hours []ActivityHours, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	maxHours := 0.0
	for _, h := range hours {
		if h.Hours > maxHours {
			maxHours = h.Hours
		}
	}
	barWidth := rect.W / 2
	if barWidth < 1 {
		barWidth = 1
	}

	y := rect.Y
	limit := rect.Y + rect.H
	for _, h := range hours {
		if y >= limit {
			break
		}
		line := fmt.Sprintf("%-16s %-*s %.1fh", h.Activity, barWidth, bar(h.Hours, maxHours, barWidth), h.Hours)
		drawText(buf, rect, rect.X, y, line, style)
		y++
	}
	if y < limit {
		drawText(buf, rect, rect.X, y, fmt.Sprintf("Total: %.1fh", HoursTotal(hours)), style)
	}
}

// RenderTypologies draws the §HS housing demand-vs-stock view (DEMO-5):
// one row per typology, "no longer available" for a retired one
// (SF-7/DEMO-9) instead of its last stale Demand/Stock numbers.
func RenderTypologies(buf *core.Buffer, rect core.Rect, rows []TypologyRow, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	y := rect.Y
	limit := rect.Y + rect.H
	for _, row := range rows {
		if y >= limit {
			break
		}
		var line string
		if row.Retired {
			line = fmt.Sprintf("%-16s no longer available", row.Typology)
		} else {
			line = fmt.Sprintf("%-16s demand %-6d stock %-6d", row.Typology, row.Demand, row.Stock)
		}
		drawText(buf, rect, rect.X, y, line, style)
		y++
	}
}

// RenderCommuteLeak draws the §21 in/out-commuting leak view (DEMO-6) as
// two explicitly distinct lines — residents working off-map (out) and
// off-map workers filling local vacancies (in) — never merged into one
// undifferentiated "commuting" number.
func RenderCommuteLeak(buf *core.Buffer, rect core.Rect, figures CommuteFigures, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, fmt.Sprintf("Out-commuting (residents working off-map): %d", figures.OutCommuters), style)
	if rect.H > 1 {
		drawText(buf, rect, rect.X, rect.Y+1, fmt.Sprintf("In-commuting (off-map workers, local jobs): %d", figures.InCommuters), style)
	}
}

// RenderPersonality draws the personality-trait distribution (DEMO-7).
func RenderPersonality(buf *core.Buffer, rect core.Rect, traits []TraitBucket, style tcell.Style) {
	renderHistogram(buf, rect, style, len(traits), func(i int) (string, float64) {
		return traits[i].Trait, float64(traits[i].Count)
	})
}

// RenderLeisureTaste draws the leisure-taste weighting distribution
// (DEMO-7).
func RenderLeisureTaste(buf *core.Buffer, rect core.Rect, taste []TasteBucket, style tcell.Style) {
	renderHistogram(buf, rect, style, len(taste), func(i int) (string, float64) {
		return taste[i].Taste, taste[i].Weight
	})
}

// renderHistogram is the shared row-per-bucket '#'-bar renderer behind
// RenderPersonality and RenderLeisureTaste — same shape as
// RenderHoursByActivity's per-row bar, generalised over a (label,value)
// accessor so the two distribution views (DEMO-7) don't duplicate the
// scaling/clip logic.
func renderHistogram(buf *core.Buffer, rect core.Rect, style tcell.Style, n int, at func(i int) (label string, value float64)) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 || n == 0 {
		return
	}
	maxV := 0.0
	for i := 0; i < n; i++ {
		_, v := at(i)
		if v > maxV {
			maxV = v
		}
	}
	barWidth := rect.W / 2
	if barWidth < 1 {
		barWidth = 1
	}
	y := rect.Y
	limit := rect.Y + rect.H
	for i := 0; i < n; i++ {
		if y >= limit {
			break
		}
		label, v := at(i)
		line := fmt.Sprintf("%-16s %-*s %.2f", label, barWidth, bar(v, maxV, barWidth), v)
		drawText(buf, rect, rect.X, y, line, style)
		y++
	}
}

// DrillTargets returns the (widget, source) registration pairs this
// screen supplies to ui.dash's (MOD-038) drill-through graph, per
// SF-5/DEMO-8: the pyramid total, one entry per non-retired typology,
// and the two distinct commuting-leak figures. Registration itself
// (Enter opening the target, dead-end detection) is MOD-038's job — see
// doc.go's SF-5 note; this screen only produces the pair list.
func DrillTargets(typologies []TypologyRow, commute CommuteFigures) []DrillTarget {
	out := []DrillTarget{
		{WidgetID: "demo.pyramid.total", Target: "citizen.population"},
	}
	for _, t := range typologies {
		if t.Retired {
			continue
		}
		out = append(out, DrillTarget{WidgetID: "demo.typology." + t.Typology, Target: "household.typology." + t.Typology})
	}
	out = append(out,
		DrillTarget{WidgetID: "demo.commute.out", Target: "extcommute.out"},
		DrillTarget{WidgetID: "demo.commute.in", Target: "extcommute.in"},
	)
	return out
}
