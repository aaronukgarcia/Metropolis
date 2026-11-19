package build

import (
	"fmt"
	"regexp"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

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

// rowWidth is the number of terminal columns a row occupies (one per rune;
// the glyphs used here are all single-width).
func rowWidth(s string) int { return len([]rune(s)) }

// rowFmt renders one row from a (possibly shortened) name.
type rowFmt func(name string) string

// pickTier returns the lowest format tier at which EVERY row fits in w
// columns (BUG-1234 part 3), so a column never mixes verbose and compact
// rows. mk(i) lists row i's formats from fullest wording to most compact. If
// no tier fits every row, the most compact tier is returned.
func pickTier(w int, names []string, mk func(i int) []rowFmt) int {
	if len(names) == 0 {
		return 0
	}
	tiers := len(mk(0))
	for t := 0; t < tiers; t++ {
		ok := true
		for i, n := range names {
			if rowWidth(mk(i)[t](n)) > w {
				ok = false
				break
			}
		}
		if ok {
			return t
		}
	}
	return tiers - 1
}

// fitRow renders a row at the chosen tier. When even that is too wide (only
// possible at the most compact tier), the NAME alone is shortened with an
// ellipsis (down to one rune), so the figures and the status badge at the end
// of the row are never the part that gets cut. Only if that still does not
// fit is the row clipped by drawText.
func fitRow(w int, name string, tier int, cands []rowFmt) string {
	f := cands[tier]
	if row := f(name); rowWidth(row) <= w {
		return row
	}
	rs := []rune(name)
	for n := len(rs) - 1; n >= 2; n-- {
		if row := f(string(rs[:n-1]) + "…"); rowWidth(row) <= w {
			return row
		}
	}
	if len(rs) > 2 {
		return f(string(rs[:1]) + "…")
	}
	return f(name)
}

var capacityRe = regexp.MustCompile(`^(\d+) homes, (\d+) jobs$`)

// compactCapacity shortens "8 homes, 0 jobs" to "8 homes" and "0 homes, 15
// jobs" to "15 jobs" (a zero side adds nothing); any other text is returned
// verbatim.
func compactCapacity(raw string) string {
	m := capacityRe.FindStringSubmatch(raw)
	if m == nil {
		return raw
	}
	switch {
	case m[2] == "0":
		return m[1] + " homes"
	case m[1] == "0":
		return m[2] + " jobs"
	}
	return raw
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
			fmt.Sprintf("cell (%d,%d)  %s", price.Cell.X, price.Cell.Y, widgets.FormatPounds(price.PriceMicropounds)), style)
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
	names := make([]string, len(zones))
	for i, z := range zones {
		names[i] = z.Name
		if names[i] == "" {
			names[i] = z.Zone
		}
	}
	tier := pickTier(rect.W, names, func(i int) []rowFmt { return zoneFmts(zones[i]) })
	for i, z := range zones {
		if y >= rect.Y+rect.H {
			return
		}
		drawText(buf, rect, rect.X, y, fitRow(rect.W, names[i], tier, zoneFmts(z)), style)
		y++
	}
}

// zoneFmts lists a zone row's formats, fullest first. The compact tiers keep
// all three figures and only abbreviate the words (BUG-1234 part 3).
func zoneFmts(z ZoneInfo) []rowFmt {
	return []rowFmt{
		func(n string) string {
			return fmt.Sprintf("  %s  materials %dt  labour %dd  lead %dd", n, z.Materials, z.Labour, z.BaseLeadTimeDays)
		},
		func(n string) string {
			return fmt.Sprintf("%s mat %dt lab %dd lead %dd", n, z.Materials, z.Labour, z.BaseLeadTimeDays)
		},
		func(n string) string {
			return fmt.Sprintf("%s mat%dt lab%dd lead%dd", n, z.Materials, z.Labour, z.BaseLeadTimeDays)
		},
	}
}

// orderFmts lists a queue row's formats, fullest first. Only the last two
// tiers drop a figure (labour remaining), and only at narrow widths.
func orderFmts(o BuildOrder) []rowFmt {
	return []rowFmt{
		func(z string) string {
			return fmt.Sprintf("  #%d %s  materials %d/%dt  labour %dd  lead %dd  [%s]",
				o.ID, z, o.MaterialsDrawn, o.MaterialsBillTotal,
				o.LabourRemaining, o.LeadTimeRemaining, o.Status)
		},
		func(z string) string {
			return fmt.Sprintf("#%d %s %d/%dt lab %dd %dd left [%s]",
				o.ID, z, o.MaterialsDrawn, o.MaterialsBillTotal,
				o.LabourRemaining, o.LeadTimeRemaining, o.Status)
		},
		func(z string) string {
			return fmt.Sprintf("#%d %s %d/%dt %dd left [%s]",
				o.ID, z, o.MaterialsDrawn, o.MaterialsBillTotal,
				o.LeadTimeRemaining, o.Status)
		},
		func(z string) string {
			return fmt.Sprintf("#%d %s %d/%dt %dd [%s]",
				o.ID, z, o.MaterialsDrawn, o.MaterialsBillTotal,
				o.LeadTimeRemaining, o.Status)
		},
	}
}

// costText shows a catalogue cost in the shared money style ("£15,000": whole
// pounds, thousands commas). The catalogue's CostRaw is TEXT from the engine
// ("£15000.00"); widgets.FormatMoneyText is the one strict parser, and text it
// does not understand (for example "20k") is shown unchanged.
func costText(raw string) string {
	out, _ := widgets.FormatMoneyText(raw)
	return out
}

// catalogueFmts lists a catalogue row's formats, fullest first. The compact
// tier drops the section word and a zero homes/jobs half; the cost keeps its
// commas in every tier (Aaron, FEAT-9995385625).
func catalogueFmts(e CatalogueEntry) []rowFmt {
	badge := e.Unlock.String()
	return []rowFmt{
		func(n string) string {
			return fmt.Sprintf("  %s (%s) cost %s cap %s [%s]", n, e.Section, costText(e.CostRaw), e.CapacityRaw, badge)
		},
		func(n string) string {
			return fmt.Sprintf("%s %s %s [%s]", n, costText(e.CostRaw), compactCapacity(e.CapacityRaw), badge)
		},
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
	// BUG-1234 part 2: orders still in progress come first, in the order the
	// engine published them; finished orders collapse into one "N completed"
	// line instead of a row each. Rows are budgeted so that the completed
	// line and, when active orders overflow, the "+N more" line always fit.
	var active []BuildOrder
	completed := 0
	for _, o := range orders {
		if o.Status == StatusComplete {
			completed++
			continue
		}
		active = append(active, o)
	}
	rows := rect.H - 1
	reserved := 0
	if completed > 0 {
		reserved = 1
	}
	shown := len(active)
	more := 0
	if shown > rows-reserved {
		// One row for "+N more" as well, unless the pane is so short that
		// only the more line fits.
		shown = rows - reserved - 1
		if shown < 0 {
			shown = 0
		}
		more = len(active) - shown
	}
	y := rect.Y + 1
	zoneNames := make([]string, shown)
	for i := range zoneNames {
		zoneNames[i] = active[i].Zone
	}
	tier := pickTier(rect.W, zoneNames, func(i int) []rowFmt { return orderFmts(active[i]) })
	for _, o := range active[:shown] {
		label := fitRow(rect.W, o.Zone, tier, orderFmts(o))
		drawText(buf, rect, rect.X, y, label, style)
		// Hand the rest of the row to QueueLane (which draws its own
		// seconds label + glyph run) — the construction queue backing up
		// toward completion, cargo-coded as construction freight.
		labelCols := rowWidth(label) + 1
		if labelCols < rect.W {
			laneRect := core.Rect{X: rect.X + labelCols, Y: y, W: rect.W - labelCols, H: 1}
			widgets.QueueLane(buf, laneRect, laneLength(o.LeadTimeRemaining), widgets.CargoFreight, 0, style)
		}
		y++
	}
	if more > 0 && y < rect.Y+rect.H {
		drawText(buf, rect, rect.X, y, fmt.Sprintf("  +%d more", more), style)
		y++
	}
	if completed > 0 && y < rect.Y+rect.H {
		drawText(buf, rect, rect.X, y, fmt.Sprintf("  %d completed", completed), style)
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
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	tier := pickTier(rect.W, names, func(i int) []rowFmt { return catalogueFmts(entries[i]) })
	for i, e := range entries {
		if y >= rect.Y+rect.H {
			return
		}
		drawText(buf, rect, rect.X, y, fitRow(rect.W, names[i], tier, catalogueFmts(e)), style)
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
			fmt.Sprintf("cell (%d,%d)  compensation %s", dem.Cell.X, dem.Cell.Y, widgets.FormatPounds(dem.CompensationMicropounds)), style)
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
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: protocol.EntityID(fmt.Sprintf("queue.%d", o.ID))})
	}
	for _, e := range entries {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: protocol.EntityID("catalogue." + e.ID)})
	}
	out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "landPrice"})
	return out
}
