package finance

import (
	"fmt"
	"sort"
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
	// One cell per rune (BUG-1222): ranging over the string indexes by BYTE, so the
	// two-byte pound sign used to take two cells and push every amount a column
	// right (and clip the last digit of a long one).
	i := -1
	for _, r := range text {
		i++
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

// plLabelWidthWide is the label column of a P&L row in a pane wide enough for
// it; a narrower pane (80x24 gives 40 columns) shrinks the label so the amount
// is never clipped off the right edge (BUG-1222).
const plLabelWidthWide = 41

// plRowLayout is the column plan for one P&L pane width.
type plRowLayout struct {
	labelW   int
	showPrev bool
}

// plLayoutFor picks the label width and whether the previous-month column
// fits; the previous-month column goes first when the pane is narrow (BUG-1222).
func plLayoutFor(width int, hasPrev bool) plRowLayout {
	// "  " + label + " " + 11-wide amount [+ " " + 11-wide previous amount]
	l := plRowLayout{labelW: width - 3 - 11}
	if hasPrev && l.labelW-12 >= 20 {
		l.showPrev = true
		l.labelW -= 12
	}
	if l.labelW > plLabelWidthWide {
		l.labelW = plLabelWidthWide
	}
	if l.labelW < 8 {
		l.labelW = 8
	}
	return l
}

func plClip(label string, w int) string {
	r := []rune(label)
	if len(r) > w {
		return string(r[:w])
	}
	return label
}

// plRowText formats one P&L row: label, this month and, when the pane has room
// and a month before exists, that month's figure (BUG-1213).
func plRowText(item PLItem, lay plRowLayout) string {
	row := fmt.Sprintf("  %-*s %11s", lay.labelW, plClip(item.Label, lay.labelW), formatPounds(item.ValueMicropounds))
	if lay.showPrev && item.PrevValueMicropounds != nil {
		row += fmt.Sprintf(" %11s", formatPounds(*item.PrevValueMicropounds))
	}
	return row
}

// plBodyRow is one itemised income or outflow row.
type plBodyRow struct {
	item   PLItem
	income bool // true: revenue row; false: outflow
	order  int
}

type plLine struct {
	text      string
	underline bool
}

func plAbs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// plFoldedLine builds the summary line for the n >= 2 income or outflow rows
// that do not fit. The amount is a POSITIVE total, like the rows beside it, and
// the wording names the direction of the money (BUG-1222). When the label would
// be clipped it is shortened, never the amount.
func plFoldedLine(n int, income bool, total int64, lay plRowLayout) string {
	kind := "outflow"
	if income {
		kind = "income"
	}
	label := fmt.Sprintf("... %d more %s rows", n, kind)
	if len([]rune(label)) > lay.labelW {
		label = fmt.Sprintf("... %d more", n)
	}
	if len([]rune(label)) > lay.labelW {
		label = fmt.Sprintf("... %d", n)
	}
	return fmt.Sprintf("  %-*s %11s", lay.labelW, plClip(label, lay.labelW), formatPounds(total))
}

// plSignedPounds formats an amount with an explicit + or - so the direction of
// the residue is never a guess.
func plSignedPounds(v int64) string {
	if v < 0 {
		return "-" + formatPounds(-v)
	}
	return "+" + formatPounds(v)
}

// plResidueText formats the signed "Other treasury movements" row that sits
// directly above Net.
func plResidueText(item PLItem, lay plRowLayout) string {
	row := fmt.Sprintf("  %-*s %11s", lay.labelW, plClip(item.Label, lay.labelW), plSignedPounds(item.ValueMicropounds))
	if lay.showPrev && item.PrevValueMicropounds != nil {
		row += fmt.Sprintf(" %11s", plSignedPounds(*item.PrevValueMicropounds))
	}
	return row
}

// plTitle returns the heading that shows the period label whatever the width.
func plTitle(period string, width int) string {
	for _, t := range []string{"PROFIT & LOSS - TREASURY (" + period + ")", "PROFIT & LOSS (" + period + ")", "P&L (" + period + ")", period} {
		if len([]rune(t)) <= width-1 {
			return t
		}
	}
	return period
}

// RenderPL draws the treasury Profit and Loss in rect. Layout is by PRIORITY
// (BUG-1222): when the pane is shorter than the full statement the period
// label, the Net treasury change line and the City economy Household
// consumption line always survive. Next comes the smallest set that still adds
// up to Net: a positive "... N more income rows" fold under Treasury income, a
// positive "... N more outflow rows" fold under Treasury outflows and the
// signed residue row directly above Net, so income minus outflows plus the
// residue is the Net on screen. Then the City economy heading and wages line,
// then the largest rows by absolute amount. Below the height where the
// reconciling set fits, only Net and consumption show. The previous-month
// column is dropped when the pane is too narrow for it. Stateless: nothing
// scrolls.
func RenderPL(buf *core.Buffer, rect core.Rect, pl PLView, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, plTitle(pl.Period, rect.W), style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	lay := plLayoutFor(rect.W-1, pl.Net != nil && pl.Net.PrevValueMicropounds != nil)

	var body []plBodyRow
	nIncome, nOut := 0, 0
	for _, it := range pl.Revenues {
		body = append(body, plBodyRow{item: it, income: true})
		nIncome++
	}
	for _, it := range pl.Expenses {
		body = append(body, plBodyRow{item: it})
		nOut++
	}
	for i := range body {
		body[i].order = i
	}

	// Full statement: title, blank, income heading + rows, blank, outflow
	// heading + rows, residue, Net, City economy heading + rows.
	fullLines := 1 + 1 + 1 + nIncome + 1 + 1 + nOut + btoi(pl.Other != nil) + btoi(pl.Net != nil)
	if len(pl.CityEconomy) > 0 {
		fullLines += 1 + len(pl.CityEconomy)
	}

	var lines []plLine
	if fullLines <= rect.H {
		lines = plFullLines(pl, body, lay)
	} else {
		lines = plCompactLines(pl, body, lay, rect.H)
	}
	for i, ln := range lines {
		if i+1 >= rect.H {
			break
		}
		st := style
		if ln.underline {
			st = st.Underline(true)
		}
		drawText(buf, rect, rect.X, rect.Y+1+i, ln.text, st)
	}
}

// plFullLines is the roomy layout: blank separators and both headings, in the
// original order, with the signed residue directly above Net.
func plFullLines(pl PLView, body []plBodyRow, lay plRowLayout) []plLine {
	lines := []plLine{{}, {text: "Treasury income:", underline: true}}
	for _, r := range body {
		if r.income {
			lines = append(lines, plLine{text: plRowText(r.item, lay)})
		}
	}
	lines = append(lines, plLine{}, plLine{text: "Treasury outflows:", underline: true})
	for _, r := range body {
		if !r.income {
			lines = append(lines, plLine{text: plRowText(r.item, lay)})
		}
	}
	if pl.Other != nil {
		lines = append(lines, plLine{text: plResidueText(*pl.Other, lay)})
	}
	if pl.Net != nil {
		lines = append(lines, plLine{text: plRowText(*pl.Net, lay)})
	}
	if len(pl.CityEconomy) > 0 {
		lines = append(lines, plLine{text: "City economy (not treasury money):", underline: true})
		for _, c := range pl.CityEconomy {
			lines = append(lines, plLine{text: plRowText(c, lay)})
		}
	}
	return lines
}

// plSideCost is the number of lines one side (income or outflows) takes when
// shown of its ranked rows are shown: the rows, a heading, and a fold line when
// two or more rows are hidden (a single hidden row is simply shown in its
// place).
func plSideCost(total, shown int) int {
	hidden := total - shown
	if hidden == 1 {
		shown++
		hidden = 0
	}
	lines := shown + btoi(hidden >= 2)
	if lines > 0 {
		lines++ // heading
	}
	return lines
}

// plCompactLines is the short-pane layout. h is the pane height including the
// title line, so h-1 lines are available here.
func plCompactLines(pl PLView, body []plBodyRow, lay plRowLayout, h int) []plLine {
	var consumption, wages *PLItem
	if len(pl.CityEconomy) > 0 {
		consumption = &pl.CityEconomy[0]
	}
	if len(pl.CityEconomy) > 1 {
		wages = &pl.CityEconomy[1]
	}
	haveNet := pl.Net != nil
	haveCons := consumption != nil

	// Survival order: Net, Household consumption, the reconciling set (both
	// folds, their headings and the residue), City economy heading, wages line,
	// then more rows. Below the reconciling set only Net and consumption (and
	// the City economy heading) show.
	spare := h - 1 - btoi(haveNet) - btoi(haveCons)
	if spare < 0 {
		spare = 0
	}

	// Rank rows by absolute amount, largest first (ties keep table order).
	ranked := append([]plBodyRow(nil), body...)
	sort.SliceStable(ranked, func(i, j int) bool {
		return plAbs(ranked[i].item.ValueMicropounds) > plAbs(ranked[j].item.ValueMicropounds)
	})
	totInc, totOut := 0, 0
	for _, r := range body {
		if r.income {
			totInc++
		} else {
			totOut++
		}
	}
	hasBody := len(body) > 0 || pl.Other != nil
	residue := btoi(pl.Other != nil)
	costOf := func(k int) int {
		sInc, sOut := 0, 0
		for _, r := range ranked[:k] {
			if r.income {
				sInc++
			} else {
				sOut++
			}
		}
		return plSideCost(totInc, sInc) + plSideCost(totOut, sOut) + residue
	}

	showBody := hasBody && costOf(0) <= spare
	showCityHdr, showWages := false, false
	pick := 0
	if showBody {
		left := spare - costOf(0)
		showCityHdr = haveCons && left >= 1
		left -= btoi(showCityHdr)
		showWages = wages != nil && showCityHdr && left >= 1
		for k := len(ranked); k >= 0; k-- {
			if costOf(k) <= spare-btoi(showCityHdr)-btoi(showWages) {
				pick = k
				break
			}
		}
	} else {
		showCityHdr = haveCons && spare >= 1
		if !hasBody {
			showWages = wages != nil && showCityHdr && spare >= 2
		}
	}

	shown := map[int]bool{}
	incHidden, outHidden := 0, 0
	incTotal, outTotal := int64(0), int64(0)
	if showBody {
		for i, r := range ranked {
			if i < pick {
				shown[r.order] = true
			}
		}
		// A side with exactly one hidden row shows that row in the fold's place.
		for _, inc := range []bool{true, false} {
			cnt, idx := 0, -1
			for _, r := range ranked {
				if r.income == inc && !shown[r.order] {
					cnt++
					idx = r.order
				}
			}
			if cnt == 1 {
				shown[idx] = true
			}
		}
		for _, r := range ranked {
			if shown[r.order] {
				continue
			}
			if r.income {
				incHidden++
				incTotal += r.item.ValueMicropounds
			} else {
				outHidden++
				outTotal += r.item.ValueMicropounds
			}
		}
	}

	var lines []plLine
	if showBody {
		emit := func(inc bool, heading string, hidden int, total int64) {
			first := true
			head := func() {
				if first {
					lines = append(lines, plLine{text: heading, underline: true})
					first = false
				}
			}
			for _, r := range body {
				if r.income != inc || !shown[r.order] {
					continue
				}
				head()
				lines = append(lines, plLine{text: plRowText(r.item, lay)})
			}
			if hidden > 0 {
				head()
				lines = append(lines, plLine{text: plFoldedLine(hidden, inc, total, lay)})
			}
		}
		emit(true, "Treasury income:", incHidden, incTotal)
		emit(false, "Treasury outflows:", outHidden, outTotal)
		if pl.Other != nil {
			lines = append(lines, plLine{text: plResidueText(*pl.Other, lay)})
		}
	}
	if haveNet {
		lines = append(lines, plLine{text: plRowText(*pl.Net, lay)})
	}
	if showCityHdr {
		lines = append(lines, plLine{text: "City economy (not treasury money):", underline: true})
	}
	if haveCons {
		lines = append(lines, plLine{text: plRowText(*consumption, lay)})
	}
	if showWages {
		lines = append(lines, plLine{text: plRowText(*wages, lay)})
	}
	return lines
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

// RenderPayrollShortfall (BUG-723, GR#17) draws the payroll-shortfall
// status line: nothing while have is false (no signal published yet) or
// while the amount is zero (the most recent month posted its full private
// wage bill — the healthy, silent default), a single warning line
// otherwise. Mirrors RenderMoneyMode's exact "guard on have, draw nothing
// when there is nothing to say" shape — like every Render* function in
// this package, it is NOT wired into an assembled TUI frame by any caller
// today (none of RenderPL/RenderBalanceSheet/RenderMoneyMode/RenderLoans/
// RenderSliders/RenderPublicPayroll/RenderSankey have a production caller
// either — this package's Screen is fully wired to the live protocol
// delta stream via ApplyDelta/PayrollShortfall(), but assembling an actual
// TUI frame out of these Render* primitives is a separate, pre-existing,
// package-wide gap this fix does not attempt to close).
func RenderPayrollShortfall(buf *core.Buffer, rect core.Rect, p PayrollShortfallView, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 || !have || p.AmountMicropounds <= 0 {
		return
	}
	line := fmt.Sprintf("PAYROLL SHORTFALL: %s (month %d, %d consecutive month(s))", formatPounds(p.AmountMicropounds), p.Month, p.Months)
	drawText(buf, rect, rect.X, rect.Y, line, style.Bold(true))
}

// RenderInsolvency (BUG-769, AC-7) draws the insolvency status line:
// nothing while have is false (no signal published yet) or while Months is
// 0 (no consecutive failed-obligations streak — the healthy, silent
// default), a single warning line otherwise, escalated to an insolvent/
// game-over line once Insolvent is true. Mirrors RenderPayrollShortfall's
// exact "guard on have, draw nothing when there is nothing to say" shape —
// like every Render* function in this package, it is NOT wired into an
// assembled TUI frame by any caller today (this fix does not attempt to
// close that pre-existing, package-wide gap — see RenderPayrollShortfall's
// own doc comment).
func RenderInsolvency(buf *core.Buffer, rect core.Rect, v InsolvencyView, have bool, style tcell.Style) {
	// BUG-769 round REJECT (opus-round-bug769, F2): `v.Months <= 0` alone
	// used to short-circuit BEFORE looking at v.Insolvent, drawing
	// NOTHING — but FinanceAPI.gameOver LATCHES (RecordMonthResult never
	// clears it once set) while InsolvencyMonths resets to 0 on the very
	// next met month, so a real, reachable production sequence (3 starved
	// months then 3 funded months, proven live by
	// attack_bug769_round_test.go's TestAttackBUG769_RecoveryAfterGameOver)
	// reaches exactly Months=0 + Insolvent=true — the GAME OVER line would
	// silently vanish the month after it first appeared. The guard now
	// only skips rendering when there is genuinely nothing to say: no
	// signal published (have=false) or a clean, never-latched city
	// (Months<=0 AND not Insolvent).
	if buf == nil || rect.W <= 0 || rect.H <= 0 || !have || (v.Months <= 0 && !v.Insolvent) {
		return
	}
	if v.Insolvent {
		line := fmt.Sprintf("INSOLVENT: %d consecutive month(s) of unmet obligations — GAME OVER", v.Months)
		drawText(buf, rect, rect.X, rect.Y, line, style.Bold(true).Foreground(tcell.ColorRed))
		return
	}
	line := fmt.Sprintf("INSOLVENCY WARNING: %d consecutive month(s) of unmet obligations", v.Months)
	drawText(buf, rect, rect.X, rect.Y, line, style.Bold(true))
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
