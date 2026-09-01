package trade

import (
	"fmt"
	"math"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// micropoundsPerPound is the money scale (1 GBP = 1,000 units since the
// BUG-452 rebase, 2026-09-01 — was 1,000,000 pre-rebase; M0-ENG §1.2 /
// engine.finance.money.go). It is a unit conversion constant, not a
// balance value — used only to DISPLAY int64 money-unit figures as pounds
// (the stored value stays int64; GR#16). GR#20 bars this UI package from
// importing internal/engine/finance directly, so this stays a
// hand-duplicated literal — rebased alongside its engine-side siblings
// (see internal/foundation/det/money.go's MicropoundsPerPound doc comment)
// rather than left silently stale.
const micropoundsPerPound int64 = 1_000

// drawText writes s left-to-right starting at (x, y), clipped to rect's
// right edge — the small shared primitive every label row uses, mirroring
// ui.screen.proj's drawText clipping discipline.
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

// formatTonnes renders a t/day figure as "<n>t/day".
func formatTonnes(tonnesPerDay int64) string {
	return fmt.Sprintf("%dt/day", tonnesPerDay)
}

// formatPounds renders an int64 micro-pound figure as pounds to two decimal
// places (integer arithmetic only — no float in the display path, so the
// formatting is deterministic across runs). Negative values get a leading
// minus. The magnitude is computed in uint64 so that negating
// math.MinInt64 never wraps to a negative value (GR#16): |MinInt64| = 2^63
// has no int64 representation, and the old in-place `-micropounds` left
// both sign and magnitude negative, rendering a double-minus string (SEC-142).
func formatPounds(micropounds int64) string {
	sign := ""
	var mag uint64
	if micropounds < 0 {
		sign = "-"
		// -(v+1)+1 yields |v| without ever negating MinInt64 itself — the
		// same two's-complement magnitude trick as foundation/num.absU64.
		mag = uint64(-(micropounds + 1)) + 1
	} else {
		mag = uint64(micropounds)
	}
	// unitsPerPenny/half-unit rounding are DERIVED from micropoundsPerPound
	// (100 pence per pound), not hand-duplicated literals — BUG-452
	// (2026-09-01) found the pre-existing hardcoded "10000"/"5000" pair
	// silently wrong after the base-unit rebase (they assumed the
	// pre-rebase 1,000,000 scale) and this file's own
	// TestFormatPounds_Deterministic caught it, so the formula is
	// generalized here to survive any future rescale.
	unitsPerPenny := uint64(micropoundsPerPound) / 100
	pounds := mag / uint64(micropoundsPerPound)
	frac := mag % uint64(micropoundsPerPound)
	hundredths := (frac + unitsPerPenny/2) / unitsPerPenny
	if hundredths >= 100 {
		pounds++
		hundredths -= 100
	}
	return fmt.Sprintf("%s£%d.%02d", sign, pounds, hundredths)
}

// formatRisk renders a [0,1] risk indicator as a whole-number percentage.
// The [0,1] domain is clamped BEFORE the float→int conversion (GR#16): a
// finite risk above ~9.2e16 makes risk*100 overflow the int conversion and
// wrap negative, so the old post-conversion clamp fired on the wrong sign
// and rendered 0% for a risk that should read 100% (SEC-143). NaN renders
// 0% — both ordered comparisons are false for NaN, so it is clamped
// explicitly rather than relying on the platform's float→int NaN result.
func formatRisk(risk float64) string {
	if risk < 0 || math.IsNaN(risk) {
		risk = 0
	} else if risk > 1 {
		risk = 1
	}
	// risk ∈ [0,1] here, so risk*100 ∈ [0,100] and the conversion cannot
	// overflow; whole-number truncation is the intended display.
	return fmt.Sprintf("%d%%", int(risk*100))
}

// RenderHeader draws the F5 title line plus the staleness marker. When
// !haveData it draws "no data" rather than a fabricated figure.
func RenderHeader(buf *core.Buffer, rect core.Rect, haveData, stale bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	title := "F5 Trade & Logistics"
	if !haveData {
		drawText(buf, rect, rect.X, rect.Y, title+" — no data", style)
		return
	}
	if stale {
		drawText(buf, rect, rect.X, rect.Y, title+"  (stale)", style)
		return
	}
	drawText(buf, rect, rect.X, rect.Y, title, style)
}

// RenderContracts draws the import-contract list (TRD-1): one row per
// contract showing the commodity, term, £/unit price, and cancellation
// penalty. A cancelled contract is marked. When !have it renders
// "unavailable", never blank (TRD-8).
func RenderContracts(buf *core.Buffer, rect core.Rect, contracts []ImportContract, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "Import contracts", style)
	if !have {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style)
		}
		return
	}
	if len(contracts) == 0 {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "none", style)
		}
		return
	}
	y := rect.Y + 1
	for _, c := range contracts {
		if y >= rect.Y+rect.H {
			return
		}
		status := ""
		if c.Status == StatusCancelled {
			status = " [cancelled]"
		}
		row := fmt.Sprintf("%s  %s  %dmo (%d left)  %s/unit  penalty %s%s",
			c.ID, c.Commodity, c.TermMonths, c.MonthsRemaining,
			formatPounds(c.PricePerUnitMicropounds),
			formatPounds(c.CancellationPenaltyMicropounds), status)
		drawText(buf, rect, rect.X, y, row, style)
		y++
	}
}

// RenderJunctions draws the junction queue live view (TRD-2): per junction,
// one widgets.QueueLane row per approach — the signature truck-glyph image,
// reused verbatim (not reimplemented), cargo-coded glyphs growing leftward
// with the wait-time figure. When !have it renders "unavailable" (TRD-8).
func RenderJunctions(buf *core.Buffer, rect core.Rect, junctions []JunctionQueue, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "Junction queue", style)
	if !have {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style)
		}
		return
	}
	if len(junctions) == 0 {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "no junctions", style)
		}
		return
	}
	y := rect.Y + 1
	for _, j := range junctions {
		if y >= rect.Y+rect.H {
			return
		}
		label := j.Label
		if label == "" {
			label = j.JunctionID
		}
		drawText(buf, rect, rect.X, y, fmt.Sprintf("%s  %s", j.JunctionID, label), style)
		y++
		for _, a := range j.Approaches {
			if y >= rect.Y+rect.H {
				return
			}
			drawText(buf, rect, rect.X, y, "  "+a.ApproachID, style)
			// Reserve 2 + len(approachID) columns for the label already
			// drawn, then hand the rest of the row to QueueLane (which
			// draws its own wait-time label + glyph run).
			labelCols := 2 + len(a.ApproachID) + 1
			laneRect := core.Rect{X: rect.X + labelCols, Y: y, W: rect.W - labelCols, H: 1}
			widgets.QueueLane(buf, laneRect, a.TruckCount, a.Cargo, a.WaitSeconds, style)
			y++
		}
	}
}

// RenderWarehouse draws the per-commodity warehouse stock/buffer table
// (TRD-3): held stock, capacity, the player's safety-buffer target, and
// the current flow, all in t/day (the only spec-fixed flow unit — ASM-251).
// When !have it renders "unavailable" (TRD-8).
func RenderWarehouse(buf *core.Buffer, rect core.Rect, rows []WarehouseCommodity, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "Warehouse stock / buffer policy", style)
	if !have {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style)
		}
		return
	}
	if len(rows) == 0 {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "no commodities", style)
		}
		return
	}
	y := rect.Y + 1
	for _, w := range rows {
		if y >= rect.Y+rect.H {
			return
		}
		row := fmt.Sprintf("%s  stock %dt / %dt  buffer %dt/day  flow %dt/day",
			w.Commodity, w.StockTonnes, w.CapacityTonnes, w.BufferTonnesPerDay, w.FlowTonnesPerDay)
		drawText(buf, rect, rect.X, y, row, style)
		y++
	}
}

// RenderPort draws the port panel (TRD-4): berths, crane rate, customs
// throughput, and the smuggling-risk indicator. It reflects the unlock
// state read from the view — !port.Unlocked renders "not yet unlocked",
// and it never implements its own tier-gating logic. When !have it renders
// "unavailable" (TRD-8) — distinct from "not yet unlocked".
func RenderPort(buf *core.Buffer, rect core.Rect, port PortState, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "Port", style)
	if !have {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style)
		}
		return
	}
	if !port.Unlocked {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "not yet unlocked", style)
		}
		return
	}
	y := rect.Y + 1
	lines := []string{
		fmt.Sprintf("berths: %d", port.Berths),
		fmt.Sprintf("crane rate: %dt/hr", port.CraneRateTonnesPerHour),
		fmt.Sprintf("operating hours: %d/day", port.OperatingHoursPerDay),
		fmt.Sprintf("customs throughput: %dt/day", port.CustomsThroughputTonnesPerDay),
		fmt.Sprintf("smuggling risk: %s", formatRisk(port.SmugglingRisk)),
	}
	for _, ln := range lines {
		if y >= rect.Y+rect.H {
			return
		}
		drawText(buf, rect, rect.X, y, ln, style)
		y++
	}
}

// RenderBalance draws the balance-of-trade extension (TRD-5 / §33):
// imports and exports, each broken down by commodity AND by artery, with
// t/day and £/day per figure. When !have it renders "unavailable" (TRD-8).
func RenderBalance(buf *core.Buffer, rect core.Rect, balance BalanceOfTradeView, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "Balance of trade (§33)", style)
	if !have {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style)
		}
		return
	}
	y := rect.Y + 1
	renderLedger := func(title string, ledger TradeLedgerView) {
		if y >= rect.Y+rect.H {
			return
		}
		drawText(buf, rect, rect.X, y, "  "+title, style)
		y++
		for _, f := range ledger.ByCommodity {
			if y >= rect.Y+rect.H {
				return
			}
			drawText(buf, rect, rect.X, y, fmt.Sprintf("    %s  %s  %s/day", f.Key, formatTonnes(f.TonnesPerDay), formatPounds(f.ValuePerDayMicropounds)), style)
			y++
		}
		for _, f := range ledger.ByArtery {
			if y >= rect.Y+rect.H {
				return
			}
			drawText(buf, rect, rect.X, y, fmt.Sprintf("    via %s  %s  %s/day", f.Key, formatTonnes(f.TonnesPerDay), formatPounds(f.ValuePerDayMicropounds)), style)
			y++
		}
	}
	renderLedger("imports", balance.Imports)
	renderLedger("exports", balance.Exports)
}

// RenderSafety draws the pipeline-vs-truck safety trade view (TRD-6 / §50):
// per corridor, the chemical/fuel pipeline grid's capacity against the
// truck-movement count it would remove from the same corridor, plus the
// leak-event risk. When !have it renders "unavailable" (TRD-8) — the
// chemical/fuel network's data is not a registered code.json outbound
// edge for this screen (BUG-058 candidate), so no safety data is expected
// until that edge lands; the view exists and renders the honest
// "unavailable" state rather than fabricating a comparison.
func RenderSafety(buf *core.Buffer, rect core.Rect, corridors []SafetyCorridor, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "Pipeline vs truck (§50)", style)
	if !have {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style)
		}
		return
	}
	if len(corridors) == 0 {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "no corridors", style)
		}
		return
	}
	y := rect.Y + 1
	for _, c := range corridors {
		if y >= rect.Y+rect.H {
			return
		}
		row := fmt.Sprintf("%s  pipeline %dt/day  trucks %d/day  leak risk %s",
			c.Corridor, c.PipelineCapacityTonnesPerDay, c.TruckMovementsPerDay, formatRisk(c.LeakRisk))
		drawText(buf, rect, rect.X, y, row, style)
		y++
	}
}

// DrillTargets returns the drill-through source identities this screen
// supplies for registration into ui.dash's (MOD-038) drill-through graph,
// per SF-5: one per contract, junction, warehouse commodity, port figure,
// balance flow (commodity and artery), and safety corridor. Every target
// uses this screen's single subscribed view (ViewSubscriptionName,
// "f5.trade") as its ViewName and a sub-entity path as its EntityID,
// mirroring ui.screen.proj's canonical dash.DrillTarget shape — GR#3
// forbids a parallel bespoke copy. Registration, navigation and dead-end
// detection remain MOD-038's job; this screen only produces the source
// list.
func DrillTargets(contracts []ImportContract, junctions []JunctionQueue, warehouse []WarehouseCommodity, balance BalanceOfTradeView, safety []SafetyCorridor) []dash.DrillTarget {
	var out []dash.DrillTarget
	for _, c := range contracts {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "contract." + c.ID})
	}
	for _, j := range junctions {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "junction." + j.JunctionID})
	}
	for _, w := range warehouse {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "warehouse." + w.Commodity})
	}
	out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "port"})
	for _, f := range balance.Imports.ByCommodity {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "import.commodity." + f.Key})
	}
	for _, f := range balance.Imports.ByArtery {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "import.artery." + f.Key})
	}
	for _, f := range balance.Exports.ByCommodity {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "export.commodity." + f.Key})
	}
	for _, f := range balance.Exports.ByArtery {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "export.artery." + f.Key})
	}
	for _, c := range safety {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "safety." + c.Corridor})
	}
	return out
}
