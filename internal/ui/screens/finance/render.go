package finance

import (
	"fmt"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/diagrams"
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

// micropoundsPerPound mirrors ui.screen.build/ui.screen.trade's own local
// copy (GR#20 bars this UI package from importing internal/engine/finance
// directly) — rebased 1,000,000 -> 1,000 alongside its engine-side
// siblings (BUG-452, 2026-09-01; see
// internal/foundation/det/money.go's MicropoundsPerPound doc comment).
const micropoundsPerPound = 1000.0

func formatPounds(micropounds int64) string {
	pounds := float64(micropounds) / micropoundsPerPound
	return fmt.Sprintf("£%.2f", pounds)
}

func RenderPL(buf *core.Buffer, rect core.Rect, pl PLView, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "PROFIT & LOSS ("+pl.Period+")", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	y := rect.Y + 2
	drawText(buf, rect, rect.X, y, "Revenues:", style.Underline(true))
	y++
	for _, item := range pl.Revenues {
		if y >= rect.Y+rect.H {
			break
		}
		rowStr := fmt.Sprintf("  %-20s %15s", item.Label, formatPounds(item.ValueMicropounds))
		drawText(buf, rect, rect.X, y, rowStr, style)
		y++
	}

	y++
	if y < rect.Y+rect.H {
		drawText(buf, rect, rect.X, y, "Expenses:", style.Underline(true))
		y++
	}
	for _, item := range pl.Expenses {
		if y >= rect.Y+rect.H {
			break
		}
		rowStr := fmt.Sprintf("  %-20s %15s", item.Label, formatPounds(item.ValueMicropounds))
		drawText(buf, rect, rect.X, y, rowStr, style)
		y++
	}
}

func RenderBalanceSheet(buf *core.Buffer, rect core.Rect, bs BalanceSheetView, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "BALANCE SHEET", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	y := rect.Y + 2
	drawText(buf, rect, rect.X, y, "Assets:", style.Underline(true))
	y++
	for _, item := range bs.Assets {
		if y >= rect.Y+rect.H {
			break
		}
		rowStr := fmt.Sprintf("  %-20s %15s", item.Label, formatPounds(item.ValueMicropounds))
		drawText(buf, rect, rect.X, y, rowStr, style)
		y++
	}

	y++
	if y < rect.Y+rect.H {
		drawText(buf, rect, rect.X, y, "Liabilities:", style.Underline(true))
		y++
	}
	for _, item := range bs.Liabilities {
		if y >= rect.Y+rect.H {
			break
		}
		rowStr := fmt.Sprintf("  %-20s %15s", item.Label, formatPounds(item.ValueMicropounds))
		drawText(buf, rect, rect.X, y, rowStr, style)
		y++
	}

	y++
	if y < rect.Y+rect.H {
		rowStr := fmt.Sprintf("Net Worth: %s", formatPounds(bs.NetWorth))
		drawText(buf, rect, rect.X, y, rowStr, style.Bold(true))
	}
}

// RenderMoneyMode is FEAT-143's AC-7 finance-UI honesty check: while
// unlimited is true (the session is running in Unlimited Money mode) it
// renders an explicit infinite/un-depletable-reserve indicator and
// nothing else -- money is not a constraint, so no budget figure is drawn
// alongside it. While unlimited is false (Real mode) it renders nothing,
// leaving the normal P&L/balance/budget surfaces (RenderPL/
// RenderBalanceSheet/RenderLoans) as the screen's only money display,
// exactly as before FEAT-143. have mirrors every other section's
// have-flag: false means no mode signal has been published yet, and this
// draws nothing (never assumes Real mode from an absent signal).
func RenderMoneyMode(buf *core.Buffer, rect core.Rect, unlimited bool, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 || !have || !unlimited {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "UNLIMITED MONEY -- SANDBOX MODE", style.Bold(true))
	drawText(buf, rect, rect.X, rect.Y+1, "money is not a constraint (financial checks bypassed)", style.Italic(true))
}

func RenderLoans(buf *core.Buffer, rect core.Rect, loans []LoanState, rating int, history []float64, rejected string, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "LOANS & CREDIT", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	ratingStr := fmt.Sprintf("Credit Rating: %d/12", rating)
	drawText(buf, rect, rect.X, rect.Y+1, ratingStr, style)

	// Render the Sparkline for the credit rating history trend (FIN-3)
	if len(history) > 0 {
		sparkRect := core.Rect{X: rect.X + 25, Y: rect.Y + 1, W: 12, H: 1}
		widgets.Sparkline(buf, sparkRect, history, style)
	}

	if rejected != "" {
		drawText(buf, rect, rect.X, rect.Y+2, "Loan Rejected: "+rejected, style.Foreground(tcell.ColorRed).Bold(true))
	}

	y := rect.Y + 3
	for _, l := range loans {
		if y >= rect.Y+rect.H {
			break
		}
		rowStr := fmt.Sprintf("Loan %s: %s @ %.1f%% (%d mo remaining) - Next: %s",
			l.ID, formatPounds(l.PrincipalMicropounds), l.RatePercent, l.TermMonths, formatPounds(l.NextPaymentMicropounds))
		drawText(buf, rect, rect.X, y, rowStr, style)
		y++
	}
}

func RenderSliders(buf *core.Buffer, rect core.Rect, sliders []TaxSliderState, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "TAX INSTRUMENTS & ELASTICITY", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	y := rect.Y + 2
	for _, s := range sliders {
		if y >= rect.Y+rect.H {
			break
		}
		valStr := strconv.FormatFloat(s.Value, 'f', 1, 64)
		rowStr := fmt.Sprintf("%-15s [%s] (%s)", s.Label, valStr, s.IncidenceDescription)
		drawText(buf, rect, rect.X, y, rowStr, style)
		y++
	}
}

func RenderPublicPayroll(buf *core.Buffer, rect core.Rect, p PublicPayrollView, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "PUBLIC PAYROLL (CIVIL SERVICE TRUTH)", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	drawText(buf, rect, rect.X, rect.Y+2, fmt.Sprintf("Gross Wage Cost: %s", formatPounds(p.WageCostMicropounds)), style)
	drawText(buf, rect, rect.X, rect.Y+3, fmt.Sprintf("Income Tax Clawback: %s", formatPounds(p.TaxClawbackMicropounds)), style)
}

// RenderSankey is a method on *Screen (not a free function like the other
// Render* helpers) precisely because it must use the Screen's HOISTED layout
// engine (BUG-316): s.engine lives for the Screen's whole lifetime, so an
// unchanged fiscal-circuit topology re-rendered on successive frames is served
// from the persistent, bounded cache instead of rebuilding a throwaway engine
// (and a fresh empty cache) every frame.
func (s *Screen) RenderSankey(buf *core.Buffer, rect core.Rect, sankey FiscalCircuitView, have bool, style tcell.Style) {
	// Copy guard (SEC-020): reject a struct-copied Screen before touching
	// its (aliased) engine pointer, mirroring every other *Screen method.
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "RenderSankey"}); err != nil {
		return
	}
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "FISCAL CIRCUIT SANKEY FLOW", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	// Build diagrams.SankeyTopology (FIN-6)
	var topo diagrams.SankeyTopology
	for _, b := range sankey.Bands {
		flow := diagrams.SankeyFlow{
			ID:     diagrams.SourceID(b.Source + "->" + b.Target),
			Amount: float64(b.Amount),
		}
		if b.Target == "Treasury" || b.Target == "Budget" {
			flow.Name = b.Source
			topo.Sources = append(topo.Sources, flow)
		} else {
			flow.Name = b.Target
			topo.Sinks = append(topo.Sinks, flow)
		}
	}

	// Render through the Screen's hoisted, bounded Sankey engine (BUG-316):
	// the cache persists across frames, so an unchanged topology at an
	// unchanged geometry is served from cache rather than re-laid-out.
	diagramRect := core.Rect{X: rect.X, Y: rect.Y + 2, W: rect.W, H: rect.H - 2}
	subBuf := core.NewBuffer(diagramRect.W, diagramRect.H)
	_, _ = s.engine.Sankey(subBuf, topo, diagrams.Options{Palette: widgets.DefaultPalette})

	// Copy from sub-buffer to main buffer
	for dy := 0; dy < diagramRect.H; dy++ {
		for dx := 0; dx < diagramRect.W; dx++ {
			cell := subBuf.Get(dx, dy)
			if cell.Rune != 0 {
				buf.Set(diagramRect.X+dx, diagramRect.Y+dy, cell.Rune, cell.Style)
			}
		}
	}
}

// DrillTargets returns the drill-through source identities this screen
// supplies for registration into ui.dash's (MOD-038) drill-through graph,
// per SF-5.
func DrillTargets(pl PLView, bs BalanceSheetView, loans []LoanState, sliders []TaxSliderState, payroll PublicPayrollView, sankey FiscalCircuitView, rating int) []dash.DrillTarget {
	var out []dash.DrillTarget
	for _, r := range pl.Revenues {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: protocol.EntityID("pl.revenue." + r.Label)})
	}
	for _, e := range pl.Expenses {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: protocol.EntityID("pl.expense." + e.Label)})
	}
	for _, a := range bs.Assets {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: protocol.EntityID("balance.asset." + a.Label)})
	}
	for _, l := range bs.Liabilities {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: protocol.EntityID("balance.liability." + l.Label)})
	}
	out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "balance.net_worth"})
	for _, l := range loans {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: protocol.EntityID("loan." + l.ID)})
	}
	out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "credit.rating"})
	for _, s := range sliders {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: protocol.EntityID("tax.slider." + s.ID)})
	}
	out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "payroll.gross"})
	out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "payroll.clawback"})
	for _, b := range sankey.Bands {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: protocol.EntityID("sankey." + b.Source + "." + b.Target)})
	}
	return out
}
