package finance

import (
	"fmt"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
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

func formatPounds(micropounds int64) string {
	pounds := float64(micropounds) / 1000000.0
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

func RenderLoans(buf *core.Buffer, rect core.Rect, loans []LoanState, rating int, have bool, style tcell.Style) {
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

func RenderSankey(buf *core.Buffer, rect core.Rect, sankey FiscalCircuitView, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "FISCAL CIRCUIT SANKEY FLOW", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	y := rect.Y + 2
	for _, b := range sankey.Bands {
		if y >= rect.Y+rect.H {
			break
		}
		rowStr := fmt.Sprintf("%s -> %s: %s", b.Source, b.Target, formatPounds(b.Amount))
		drawText(buf, rect, rect.X, y, rowStr, style)
		y++
	}
}
