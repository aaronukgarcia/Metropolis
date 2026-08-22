package districts

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
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

// RenderTaxSettings draws AC-6's per-district tax-settings panel, scoped to
// selectedDistrict (rows for other districts are not shown -- US-5's "same
// screen a district's identity lives on" scoping). rejected (AC-9, empty
// when there is nothing to report) surfaces the engine's rejection reason
// rather than a silent revert.
func RenderTaxSettings(buf *core.Buffer, rect core.Rect, settings []DistrictTaxSetting, selectedDistrict, rejected string, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "PER-DISTRICT TAX SETTINGS", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	y := rect.Y + 1
	if rejected != "" {
		drawText(buf, rect, rect.X, y, "Rejected: "+rejected, style.Foreground(tcell.ColorRed).Bold(true))
		y++
	}
	if selectedDistrict == "" {
		drawText(buf, rect, rect.X, y, "select a district to edit its tax settings", style.Italic(true))
		return
	}
	y++
	shown := 0
	for _, s := range settings {
		if s.DistrictID != selectedDistrict {
			continue
		}
		if y >= rect.Y+rect.H {
			break
		}
		row := fmt.Sprintf("%-20s x%.2f -> %.2f%% (cap %.2f%%)", s.InstrumentLabel, s.Multiplier, s.EffectiveRate, s.RateMax)
		drawText(buf, rect, rect.X, y, row, style)
		y++
		shown++
	}
	if shown == 0 {
		drawText(buf, rect, rect.X, y, "no tax settings for district "+selectedDistrict, style.Italic(true))
	}
}

// RenderBlockedFeature draws a US-2/US-3/US-4/US-1-shaped feature (policy
// library, impact preview, conflict warning, district drawing/naming) in
// its honest, permanently-BLOCKED state -- see doc.go: engine.policies is
// not merged to main (it is REJECT-state in a lane worktree), so this
// screen has no live PoliciesAPI subscription to source these panes from
// at all. Rather than fabricate a wire schema against unreviewed/rejected
// code (GR#25 "never build against an unregistered/unreviewed
// dependency"), every one of these panes renders the same honest
// "unavailable" state SF-7 already mandates for a pane with no data --
// mirrors ui.screen.services' RenderPublicServicePie/SVC-6 pattern, except
// here the entire feature (not one field) is BLOCKED.
func RenderBlockedFeature(buf *core.Buffer, rect core.Rect, title string, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, title, style.Bold(true))
	if rect.H > 1 {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable -- engine.policies not yet merged to main", style.Italic(true))
	}
}

// financeJumpView is AC-7's whole-view drill-through destination for a
// district's total tax revenue: the F2 Finance view name
// (financescreen.ViewSubscriptionName), held here as a literal so this
// package need not import ui.screen.finance from production code (SF-1
// stays scoped to protocol-only consumption of the ENGINE; a cross-screen
// literal is still checked for drift -- see drill_finance_test.go, which
// imports ui.screen.finance in a _test.go file only, the sanctioned
// exception ui.screen.services' drill_map_test.go already establishes).
const financeJumpView = "f2.finance"

// FinanceJumpTarget returns AC-7's whole-view drill-through target for a
// district's aggregate tax-revenue figure: Enter opens F2 Finance (the
// whole, already-registered source view), per AC-7's explicitly buildable
// half. It does NOT filter F2 to the district -- F2 has no per-district
// scoping dimension of its own (engine.tax computes RevenueInDistrict, but
// nothing on ui.screen.finance's registered outbound edges or wire schema
// exposes a district filter), so this jump names the real whole view
// rather than fabricating a filtered one that does not exist. Row-level
// drill-through (the specific incidence entry) is separately blocked by
// ASM-275 -- see AC-7/doc.go.
func FinanceJumpTarget(districtID string) dash.DrillTarget {
	return dash.DrillTarget{ViewName: financeJumpView, EntityID: "district." + districtID + ".tax-revenue"}
}

// DrillTargets returns AC-7's whole-view drill-through source identities
// this screen supplies for registration into ui.dash's (MOD-038)
// drill-through graph, per SF-5 -- one per rendered tax-setting row, plus
// FinanceJumpTarget per district represented. AC-2/3/4/5/8's panes are
// BLOCKED (see doc.go) and have no data of their own, so nothing from
// those panes is registered here (mirrors ui.screen.services' SVC-6 "a
// slice this screen never has data for is not registered as a drill
// source" convention).
func DrillTargets(settings []DistrictTaxSetting) []dash.DrillTarget {
	var out []dash.DrillTarget
	seenDistrict := make(map[string]bool)
	for _, s := range settings {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: "tax." + s.DistrictID + "." + s.InstrumentID})
		if !seenDistrict[s.DistrictID] {
			seenDistrict[s.DistrictID] = true
			out = append(out, FinanceJumpTarget(s.DistrictID))
		}
	}
	return out
}
