package services

import (
	"fmt"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
	"github.com/gdamore/tcell/v2"
)

func drawText(buf *core.Buffer, rect core.Rect, x, y int, text string, style tcell.Style) {
	if buf == nil {
		return
	}
	for i, r := range text {
		cx := x + i
		if cx >= rect.X+rect.W {
			break
		}
		if cy := y; cy < rect.Y+rect.H {
			buf.Set(cx, cy, r, style)
		}
	}
}

// RenderSliders draws SVC-1's per-service funding sliders. rejected (SVC-8,
// empty when there is nothing to report) surfaces the engine's rejection
// reason rather than a silent revert.
func RenderSliders(buf *core.Buffer, rect core.Rect, sliders []ServiceSlider, rejected string, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "SERVICE FUNDING", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	y := rect.Y + 1
	if rejected != "" {
		drawText(buf, rect, rect.X, y, "Funding Rejected: "+rejected, style.Foreground(tcell.ColorRed).Bold(true))
		y++
	}
	y++
	for _, sl := range sliders {
		if y >= rect.Y+rect.H {
			break
		}
		valStr := strconv.FormatFloat(sl.Value, 'f', 1, 64)
		minStr := strconv.FormatFloat(sl.Min, 'f', 1, 64)
		maxStr := strconv.FormatFloat(sl.Max, 'f', 1, 64)
		rowStr := fmt.Sprintf("%-15s [%s] (range %s-%s)", sl.Label, valStr, minStr, maxStr)
		drawText(buf, rect, rect.X, y, rowStr, style)
		y++
	}
}

// RenderCapacityDemand draws SVC-2's per-service capacity-vs-demand
// gauge, reusing widgets.Gauge (UI-SPEC §2's block-fill gauge idiom)
// rather than hand-rolling a bar.
func RenderCapacityDemand(buf *core.Buffer, rect core.Rect, cd []CapacityDemand, have bool, palette widgets.Palette, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "CAPACITY VS DEMAND", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	y := rect.Y + 2
	for _, c := range cd {
		if y >= rect.Y+rect.H {
			break
		}
		label := fmt.Sprintf("%-15s", c.Label)
		drawText(buf, rect, rect.X, y, label, style)
		gaugeRect := core.Rect{X: rect.X + 16, Y: y, W: 12, H: 1}
		widgets.Gauge(buf, gaugeRect, c.Ratio(), widgets.Thresholds{}, palette, style)
		figures := fmt.Sprintf(" %.0f/%.0f", c.DemandUnits, c.CapacityUnits)
		drawText(buf, rect, rect.X+29, y, figures, style)
		y++
	}
}

// RenderResponseTimes draws SVC-4's response-time distribution figures
// (§26's unified dispatch model: fire, ambulance, air ambulance, police).
func RenderResponseTimes(buf *core.Buffer, rect core.Rect, rt []ResponseTimeStat, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "RESPONSE TIME DISTRIBUTION", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	y := rect.Y + 2
	for _, r := range rt {
		if y >= rect.Y+rect.H {
			break
		}
		rowStr := fmt.Sprintf("%-15s median %.0fs  p90 %.0fs  (n=%d)", r.Label, r.MedianSeconds, r.P90Seconds, r.SampleCount)
		drawText(buf, rect, rect.X, y, rowStr, style)
		y++
	}
}

// RenderWaitingLists draws SVC-5's waiting-list figures with a 12-cell
// sparkline trend, reusing widgets.Sparkline rather than hand-rolling one.
func RenderWaitingLists(buf *core.Buffer, rect core.Rect, wl []WaitingList, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "WAITING LISTS", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	y := rect.Y + 2
	for _, w := range wl {
		if y >= rect.Y+rect.H {
			break
		}
		label := fmt.Sprintf("%-20s %6d", w.Label, w.CurrentCount)
		drawText(buf, rect, rect.X, y, label, style)
		if len(w.TrendHistory) > 0 {
			sparkRect := core.Rect{X: rect.X + 28, Y: y, W: 12, H: 1}
			widgets.Sparkline(buf, sparkRect, w.TrendHistory, style)
		}
		y++
	}
}

// RenderPublicServicePie draws SVC-6's Public Service Pie allocation.
// BLOCKED (see doc.go): PublicServicePie() always returns have=false
// today (no engine.fiscal outbound edge is registered for
// ui.screen.services, BUG-058 candidate), so this always renders the
// honest "unavailable" state (SF-7) rather than a faked benchmark —
// mirrors ui.screen.trade's RenderSafety/TRD-6 pattern exactly.
func RenderPublicServicePie(buf *core.Buffer, rect core.Rect, pie PublicServicePieView, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "PUBLIC SERVICE PIE (per 1,000 population)", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	y := rect.Y + 2
	for _, sl := range pie.Slices {
		if y >= rect.Y+rect.H {
			break
		}
		rowStr := fmt.Sprintf("%-15s benchmark %.2f  actual %.2f", sl.Label, sl.BenchmarkPer1k, sl.ActualFunding)
		drawText(buf, rect, rect.X, y, rowStr, style)
		y++
	}
}

// DrillTargets returns the drill-through source identities this screen
// supplies for registration into ui.dash's (MOD-038) drill-through graph,
// per SF-5, for every figure documented in doc.go's SF-2 table except
// SVC-3's coverage jump (see CoverageJumpTarget, declared BLOCKED
// separately) and SVC-6's Pie slices (BLOCKED, see doc.go — a slice this
// screen never actually has data for is not registered as a drill source).
func DrillTargets(sliders []ServiceSlider, cd []CapacityDemand, rt []ResponseTimeStat, wl []WaitingList) []dash.DrillTarget {
	var out []dash.DrillTarget
	for _, sl := range sliders {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "slider." + sl.ID})
	}
	for _, c := range cd {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "capacity." + c.ServiceID})
	}
	for _, r := range rt {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "response." + r.ServiceID})
	}
	for _, w := range wl {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "waiting." + w.ID})
	}
	return out
}
