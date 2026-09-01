package build

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// micropoundsPerPound is the money scale (1 GBP = 1,000 units since the
// BUG-452 rebase, 2026-09-01 — was 1,000,000 pre-rebase; M0-ENG §1.2 /
// engine.finance.money.go). It is a unit conversion constant, not a
// balance value — used only to DISPLAY int64 money-unit figures as pounds
// (the stored value stays int64; GR#16). This mirrors ui.screen.trade's
// local formatPounds (there is no shared money formatter in
// ui.widgets/ui.core yet — flagged in the delivery report as a GR#3
// extraction candidate, out of this package's file ownership). GR#20 bars
// this UI package from importing internal/engine/finance directly, so
// this stays a hand-duplicated literal — rebased alongside its three
// engine-side siblings (see internal/foundation/det/money.go's
// MicropoundsPerPound doc comment) rather than left silently stale.
const micropoundsPerPound int64 = 1_000

// laneLengthCap bounds the queue-lane glyph run so the lane never exceeds a
// reasonable visual width before widgets.QueueLane's own rect clamp. It is
// a display choice, not a lead-time value (the lead-time figure renders
// separately as text).
const laneLengthCap = 30

// drawText writes s left-to-right starting at (x, y), clipped to rect's
// right edge — the small shared primitive every label row uses, mirroring
// ui.screen.trade's drawText clipping discipline.
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

// formatPounds renders an int64 micro-pound figure as pounds to two decimal
// places (integer arithmetic only — no float in the display path, so the
// formatting is deterministic across runs). Negative values get a leading
// minus. The magnitude is computed in uint64 so that negating
// math.MinInt64 never wraps to a negative value (GR#16): |MinInt64| = 2^63
// has no int64 representation, and the in-place `-micropounds` would leave
// both sign and magnitude negative, rendering a double-minus string
// (SEC-142, mirrored from ui.screen.trade).
func formatPounds(micropounds int64) string {
	sign := ""
	var mag uint64
	if micropounds < 0 {
		sign = "-"
		mag = uint64(-(micropounds + 1)) + 1
	} else {
		mag = uint64(micropounds)
	}
	// unitsPerPenny/half-unit rounding are DERIVED from micropoundsPerPound
	// (100 pence per pound), not hand-duplicated literals — BUG-452
	// (2026-09-01) found the pre-existing hardcoded "10000"/"5000" pair
	// silently wrong after the base-unit rebase (they assumed the
	// pre-rebase 1,000,000 scale) and this file's own
	// TestFormatPounds_Deterministic caught it (£1.25 rendered as £1.00),
	// so the formula is generalized here to survive any future rescale.
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

// laneLength clamps an int64 lead-time-remaining figure into the
// non-negative int glyph-run length widgets.QueueLane draws (GR#16: an
// int64 -> int conversion is clamped, never wrapped). The lane is a
// visual "how much work is still queued" indicator, not the lead-time
// figure itself (that renders as text).
func laneLength(remaining int64) int {
	if remaining <= 0 {
		return 0
	}
	if remaining > laneLengthCap {
		return laneLengthCap
	}
	return int(remaining)
}

// RenderHeader draws the F3 title line plus the staleness marker. When
// !haveData it draws "no data" rather than a fabricated figure.
func RenderHeader(buf *core.Buffer, rect core.Rect, haveData, stale bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	title := "F3 Land & Construction"
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

// RenderLandPrice draws the land-purchase pane (BLD-1): the price for the
// cell the player is considering buying. When !have it renders
// "unavailable", never blank (BLD-8).
func RenderLandPrice(buf *core.Buffer, rect core.Rect, price LandPriceView, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "Land purchase", style)
	if !have {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style)
		}
		return
	}
	if rect.H > 1 {
		drawText(buf, rect, rect.X, rect.Y+1,
			fmt.Sprintf("cell (%d,%d)  %s", price.Cell.X, price.Cell.Y, formatPounds(price.PriceMicropounds)), style)
	}
}

// RenderZones draws the §34 zone-catalogue pane (BLD-2): one row per zone
// type showing its construction economics (materials, labour, base lead
// time). When !have it renders "unavailable" (BLD-8).
func RenderZones(buf *core.Buffer, rect core.Rect, zones []ZoneInfo, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "Zones (§34)", style)
	if !have {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style)
		}
		return
	}
	if len(zones) == 0 {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "no zones", style)
		}
		return
	}
	y := rect.Y + 1
	for _, z := range zones {
		if y >= rect.Y+rect.H {
			return
		}
		name := z.Name
		if name == "" {
			name = z.Zone
		}
		row := fmt.Sprintf("  %s  materials %dt  labour %dd  lead %dd",
			name, z.Materials, z.Labour, z.BaseLeadTimeDays)
		drawText(buf, rect, rect.X, y, row, style)
		y++
	}
}

// RenderQueue draws the build queue (BLD-3): one row per order showing its
// zone and the three figures — materials drawn/total, labour remaining,
// and lead-time remaining — plus a widgets.QueueLane lane (reused verbatim,
// not reimplemented) whose glyph run length is the order's remaining lead
// time, so "how much work is still queued" is visible at a glance. The
// lane's seconds label is unused (build orders track lead time in days, not
// wait seconds; the lead-time figure renders as text). When !have it
// renders "unavailable" (BLD-8).
func RenderQueue(buf *core.Buffer, rect core.Rect, orders []BuildOrder, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "Build queue", style)
	if !have {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style)
		}
		return
	}
	if len(orders) == 0 {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "no build orders", style)
		}
		return
	}
	y := rect.Y + 1
	for _, o := range orders {
		if y >= rect.Y+rect.H {
			return
		}
		label := fmt.Sprintf("  #%d %s  materials %d/%dt  labour %dd  lead %dd  [%s]",
			o.ID, o.Zone, o.MaterialsDrawn, o.MaterialsBillTotal,
			o.LabourRemaining, o.LeadTimeRemaining, o.Status)
		drawText(buf, rect, rect.X, y, label, style)
		// Hand the rest of the row to QueueLane (which draws its own
		// seconds label + glyph run) — the construction queue backing up
		// toward completion, cargo-coded as construction freight.
		labelCols := len(label) + 1
		if labelCols < rect.W {
			laneRect := core.Rect{X: rect.X + labelCols, Y: y, W: rect.W - labelCols, H: 1}
			widgets.QueueLane(buf, laneRect, laneLength(o.LeadTimeRemaining), widgets.CargoFreight, 0, style)
		}
		y++
	}
}

// RenderCatalogue draws the building catalogue (BLD-5): one row per entry
// showing its name, section, verbatim cost/capacity text, and its
// unlock-state badge — read straight off the view's unlockState, never
// recomputed. When !have it renders "unavailable"; a single entry whose
// unlock state is unavailable renders "unavailable" as its badge (BLD-8).
func RenderCatalogue(buf *core.Buffer, rect core.Rect, entries []CatalogueEntry, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "Building catalogue", style)
	if !have {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style)
		}
		return
	}
	if len(entries) == 0 {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "no entries", style)
		}
		return
	}
	y := rect.Y + 1
	for _, e := range entries {
		if y >= rect.Y+rect.H {
			return
		}
		row := fmt.Sprintf("  %s (%s)  cost %s  cap %s  [%s]",
			e.Name, e.Section, e.CostRaw, e.CapacityRaw, e.Unlock.String())
		drawText(buf, rect, rect.X, y, row, style)
		y++
	}
}

// RenderDemolition draws the demolition pane (BLD-4): the compensation for
// demolishing the cell's structure, the figure a confirmation step shows
// before Demolish is issued. When !have it renders "unavailable" (BLD-8).
func RenderDemolition(buf *core.Buffer, rect core.Rect, dem DemolitionView, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "Demolition", style)
	if !have {
		if rect.H > 1 {
			drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style)
		}
		return
	}
	if rect.H > 1 {
		drawText(buf, rect, rect.X, rect.Y+1,
			fmt.Sprintf("cell (%d,%d)  compensation %s", dem.Cell.X, dem.Cell.Y, formatPounds(dem.CompensationMicropounds)), style)
	}
}

// DrillTargets returns the drill-through source identities this screen
// supplies for registration into ui.dash's (MOD-038) drill-through graph,
// per SF-5/BLD-6: one per build-queue order (its materials/labour/lead-time
// figures) and one per catalogue entry (its cost/lead-time/unlock figures),
// plus the land-purchase price. Every target uses this screen's single
// subscribed view (ViewSubscriptionName, "f3.build") as its ViewName and a
// sub-entity path as its EntityID, mirroring ui.screen.trade's canonical
// dash.DrillTarget shape — GR#3 forbids a parallel bespoke copy.
// Registration, navigation and dead-end detection remain MOD-038's job;
// this screen only produces the source list.
func DrillTargets(orders []BuildOrder, entries []CatalogueEntry) []dash.DrillTarget {
	var out []dash.DrillTarget
	for _, o := range orders {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: fmt.Sprintf("queue.%d", o.ID)})
	}
	for _, e := range entries {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "catalogue." + e.ID})
	}
	out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "landPrice"})
	return out
}
