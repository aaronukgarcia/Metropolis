package census

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
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

// ageBandLabels are §13-F6's five age-band row labels, matching
// engine.census.ageBandIndex's bucket boundaries
// (internal/engine/census/stats.go: <18, 18-34, 35-54, 55-74, 75+).
var ageBandLabels = [NumAgeBands]string{"0-17", "18-34", "35-54", "55-74", "75+"}

// educationTierLabels are §27's eight education-stage row labels,
// matching engine.census.StageKind's ordering
// (internal/engine/census/sources.go: none..adult-ed).
var educationTierLabels = [NumEducationTiers]string{
	"None", "Nursery", "Primary", "Secondary", "Sixth Form", "Technical", "University", "Adult Ed",
}

// RenderAgeBandPyramid draws AC-3's age-band spline as AC-10's ASCII
// population pyramid: one row per age band, a mirrored heat-intensity bar
// on both sides of the row label, reusing widgets.Heatmap (an existing
// ui.widgets primitive, not a bespoke cell-buffer routine).
//
// Honesty note (GR#3): engine.census exposes AgeBandSeries and SexSeries
// as two independent 5- and 2-element arrays (internal/engine/census/
// stats.go) — there is no joint age×sex table anywhere in engine.census,
// so this pyramid's two sides both mirror the SAME per-band population
// count. It is not a per-band male/female split (that data does not
// exist today; a per-band sex split would be fabricated, which GR#3
// forbids). The sex totals themselves render separately via
// RenderSexSeries, sourced from SexSeries independently (AC-3's
// independence guarantee).
func RenderAgeBandPyramid(buf *core.Buffer, rect core.Rect, bands [NumAgeBands]int64, have bool, palette widgets.Palette, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "POPULATION PYRAMID (age bands)", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}

	var maxVal float64
	for _, v := range bands {
		if fv := float64(v); fv > maxVal {
			maxVal = fv
		}
	}
	ramp := widgets.DefaultHeatRamp(palette)

	y := rect.Y + 2
	for i, count := range bands {
		if y >= rect.Y+rect.H {
			break
		}
		label := fmt.Sprintf("%-6s", ageBandLabels[i])
		drawText(buf, rect, rect.X, y, label, style)

		val := []float64{float64(count)}
		leftRect := core.Rect{X: rect.X + 7, Y: y, W: 10, H: 1}
		widgets.Heatmap(buf, leftRect, val, 1, 0, maxVal, ramp)
		rightRect := core.Rect{X: rect.X + 18, Y: y, W: 10, H: 1}
		widgets.Heatmap(buf, rightRect, val, 1, 0, maxVal, ramp)

		countStr := fmt.Sprintf(" %d", count)
		drawText(buf, rect, rect.X+29, y, countStr, style)
		y++
	}
}

// RenderSexSeries draws AC-3's sex spline (female/male totals), reusing
// widgets.Gauge for each bar (the same "reuse an existing widget, no
// bespoke bar" convention AC-10 states for the pyramid).
func RenderSexSeries(buf *core.Buffer, rect core.Rect, sex [NumSexBuckets]int64, have bool, palette widgets.Palette, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "SEX", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}
	total := sex[SexIndexFemale] + sex[SexIndexMale]
	var femaleFrac, maleFrac float64
	if total > 0 {
		femaleFrac = float64(sex[SexIndexFemale]) / float64(total)
		maleFrac = float64(sex[SexIndexMale]) / float64(total)
	}

	y := rect.Y + 1
	drawText(buf, rect, rect.X, y, fmt.Sprintf("%-8s", "Female"), style)
	widgets.Gauge(buf, core.Rect{X: rect.X + 9, Y: y, W: 12, H: 1}, femaleFrac, widgets.Thresholds{}, palette, style)
	drawText(buf, rect, rect.X+22, y, fmt.Sprintf(" %d", sex[SexIndexFemale]), style)
	y++
	if y < rect.Y+rect.H {
		drawText(buf, rect, rect.X, y, fmt.Sprintf("%-8s", "Male"), style)
		widgets.Gauge(buf, core.Rect{X: rect.X + 9, Y: y, W: 12, H: 1}, maleFrac, widgets.Thresholds{}, palette, style)
		drawText(buf, rect, rect.X+22, y, fmt.Sprintf(" %d", sex[SexIndexMale]), style)
	}
}

// RenderEducationTierSeries draws AC-3's education-tier spline, reusing
// widgets.Sparkline across the eight stage buckets (per-tier population
// counts, downsampled/plotted exactly as any other trending series
// UI-SPEC §2 documents).
func RenderEducationTierSeries(buf *core.Buffer, rect core.Rect, tiers [NumEducationTiers]int64, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "EDUCATION TIER", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}
	series := make([]float64, NumEducationTiers)
	for i, v := range tiers {
		series[i] = float64(v)
	}
	sparkRect := core.Rect{X: rect.X, Y: rect.Y + 1, W: widgets.SparklineWidth, H: 1}
	widgets.Sparkline(buf, sparkRect, series, style)

	y := rect.Y + 2
	for i, v := range tiers {
		if y >= rect.Y+rect.H {
			break
		}
		row := fmt.Sprintf("%-12s %d", educationTierLabels[i], v)
		drawText(buf, rect, rect.X, y, row, style)
		y++
	}
}

// RenderBlueWhiteCollar draws AC-4's blue/white-collar workforce split,
// reusing widgets.Gauge for each bar (never a screen-local constant
// ratio).
func RenderBlueWhiteCollar(buf *core.Buffer, rect core.Rect, bwc BlueWhiteCollar, have bool, palette widgets.Palette, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "WORKFORCE (blue/white collar)", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}
	total := bwc.Blue + bwc.White
	var blueFrac, whiteFrac float64
	if total > 0 {
		blueFrac = float64(bwc.Blue) / float64(total)
		whiteFrac = float64(bwc.White) / float64(total)
	}
	y := rect.Y + 1
	drawText(buf, rect, rect.X, y, fmt.Sprintf("%-8s", "Blue"), style)
	widgets.Gauge(buf, core.Rect{X: rect.X + 9, Y: y, W: 12, H: 1}, blueFrac, widgets.Thresholds{}, palette, style)
	drawText(buf, rect, rect.X+22, y, fmt.Sprintf(" %d", bwc.Blue), style)
	y++
	if y < rect.Y+rect.H {
		drawText(buf, rect, rect.X, y, fmt.Sprintf("%-8s", "White"), style)
		widgets.Gauge(buf, core.Rect{X: rect.X + 9, Y: y, W: 12, H: 1}, whiteFrac, widgets.Thresholds{}, palette, style)
		drawText(buf, rect, rect.X+22, y, fmt.Sprintf(" %d", bwc.White), style)
	}
}

// kpiLabels maps a KPI key to its display label (AC-5).
var kpiLabels = map[string]string{
	KPIKeyGDP:            "GDP",
	KPIKeyHappiness:      "Happiness",
	KPIKeyLandValue:      "Land Value",
	KPIKeyHomeless:       "Homeless",
	KPIKeyInHospital:     "In Hospital",
	KPIKeyOutOfWork:      "Out of Work",
	KPIKeyUnfilledJobs:   "Unfilled Jobs",
	KPIKeyJobSkillDemand: "Job-Skill Demand",
}

// RenderKPITiles draws AC-5's eight city-KPI tiles, one row per tile.
func RenderKPITiles(buf *core.Buffer, rect core.Rect, kpis []KPITile, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "CITY KPIs", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}
	y := rect.Y + 1
	for _, k := range kpis {
		if y >= rect.Y+rect.H {
			break
		}
		label := kpiLabels[k.Key]
		if label == "" {
			label = k.Key
		}
		row := fmt.Sprintf("%-18s %.2f", label, k.Value)
		drawText(buf, rect, rect.X, y, row, style)
		y++
	}
}

// RenderKPISource draws AC-6's drill-in resolution for one KPI: either
// the entity IDs (population-derived KPI) or the ledger LineValue
// (aggregate KPI). An Unavailable source (AC-12) renders an explicit
// "unavailable" line, never a silently-rendered zero.
func RenderKPISource(buf *core.Buffer, rect core.Rect, src KPISource, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "SOURCE: "+src.Key, style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}
	if src.Unavailable {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable: "+src.Reason, style.Foreground(tcell.ColorRed).Italic(true))
		return
	}
	y := rect.Y + 1
	if len(src.EntityIDs) > 0 {
		row := fmt.Sprintf("entities: %d (line %d)", len(src.EntityIDs), src.LineValue)
		drawText(buf, rect, rect.X, y, row, style)
	} else {
		row := fmt.Sprintf("line value: %d", src.LineValue)
		drawText(buf, rect, rect.X, y, row, style)
	}
}

// RenderCitizenBio draws AC-7's cradle-to-grave citizen bio: education,
// employment, family, retirement, income — all five facets verbatim from
// the subscribed CitizenBio view. An Unavailable bio (AC-12) renders an
// explicit "unavailable" pane, never a silently-rendered zero-value bio.
func RenderCitizenBio(buf *core.Buffer, rect core.Rect, bio CitizenBio, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "CITIZEN BIO: "+bio.GUID, style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}
	if bio.Unavailable {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable: "+bio.Reason, style.Foreground(tcell.ColorRed).Italic(true))
		return
	}
	y := rect.Y + 1
	lines := []string{
		fmt.Sprintf("Education: attainment %d, schooling %d, tie %q, stages %d", bio.Education.Attainment, bio.Education.Schooling, bio.Education.IndustryTie, len(bio.Education.Stages)),
		fmt.Sprintf("Employment: %s, %s, workplace %d", bio.Employment.State, bio.Employment.Sector, bio.Employment.Workplace),
		fmt.Sprintf("Family: household %d, partner %d, home %d", bio.Family.Household, bio.Family.Partner, bio.Family.Home),
		fmt.Sprintf("Retirement: month %d", bio.Retirement),
		fmt.Sprintf("Income: %d", bio.Income),
	}
	for _, line := range lines {
		if y >= rect.Y+rect.H {
			break
		}
		drawText(buf, rect, rect.X, y, line, style)
		y++
	}
}

// RenderEducationCrimeLinkage draws AC-8's education→crime linkage report
// as a direction-and-structure display: the figures print verbatim from
// the subscribed view, never a screen-computed slope/elasticity.
func RenderEducationCrimeLinkage(buf *core.Buffer, rect core.Rect, link EducationCrimeLinkage, have bool, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	drawText(buf, rect, rect.X, rect.Y, "EDUCATION -> CRIME LINKAGE", style.Bold(true))
	if !have {
		drawText(buf, rect, rect.X, rect.Y+1, "unavailable", style.Italic(true))
		return
	}
	lines := []string{
		fmt.Sprintf("Population: %d", link.Population),
		fmt.Sprintf("Mean attainment: %.2f", link.MeanAttainment),
		fmt.Sprintf("Crime rate: %.4f", link.CrimeRate),
		fmt.Sprintf("Uneducated fraction: %.4f", link.UneducatedFraction),
		fmt.Sprintf("Policy coefficient: %.4f", link.PolicyCoefficient),
	}
	y := rect.Y + 1
	for _, line := range lines {
		if y >= rect.Y+rect.H {
			break
		}
		drawText(buf, rect, rect.X, y, line, style)
		y++
	}
}

// DrillTargets returns AC-9's drill-through source identities for
// registration into ui.dash's (MOD-038) drill-through graph: one target
// per KPI tile (AC-5) and one per citizen-bio facet (AC-7), per the same
// (ViewName, EntityID) shape ui.screen.services'/ui.screen.build's
// DrillTargets already establish. This package implements no
// navigation/dead-end-detection/graph storage itself (MOD-038's job).
func DrillTargets(kpis []KPITile, bio CitizenBio, haveBio bool) []dash.DrillTarget {
	var out []dash.DrillTarget
	for _, k := range kpis {
		out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: protocol.EntityID("kpi." + k.Key)})
	}
	if haveBio && bio.GUID != "" {
		facets := []string{"education", "employment", "family", "retirement", "income"}
		for _, f := range facets {
			out = append(out, dash.DrillTarget{ViewName: ViewSubscriptionName, EntityID: protocol.EntityID("bio." + bio.GUID + "." + f)})
		}
	}
	return out
}
