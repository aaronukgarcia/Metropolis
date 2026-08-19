package census

// AC-8 (education->crime linkage report, mirrors engine.census.md AC-14).

import (
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func renderLinkageInto(link EducationCrimeLinkage, have bool) []string {
	buf := core.NewBuffer(100, 8)
	rect := core.Rect{X: 0, Y: 0, W: 100, H: 8}
	RenderEducationCrimeLinkage(buf, rect, link, have, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return renderedText(buf, rect)
}

// TestEducationCrimeLinkage_DirectionOnly feeds two fixture linkage
// snapshots differing only in mean attainment (one lower) and asserts the
// render distinguishes them (the lower-attainment fixture's crime figure
// renders as the higher one), without the screen computing its own
// elasticity -- the render prints exactly the subscribed CrimeRate field,
// never a screen-derived slope.
func TestEducationCrimeLinkage_DirectionOnly(t *testing.T) {
	lowAttainmentHighCrime := EducationCrimeLinkage{
		Population:         8000,
		MeanAttainment:     400.0, // lower attainment
		CrimeRate:          0.045, // higher crime rate (data-derived, not computed here)
		UneducatedFraction: 0.30,
		PolicyCoefficient:  0.10,
	}
	highAttainmentLowCrime := EducationCrimeLinkage{
		Population:         8000,
		MeanAttainment:     900.0, // higher attainment
		CrimeRate:          0.008, // lower crime rate
		UneducatedFraction: 0.02,
		PolicyCoefficient:  0.10,
	}

	rowsLow := renderLinkageInto(lowAttainmentHighCrime, true)
	rowsHigh := renderLinkageInto(highAttainmentLowCrime, true)

	if !rowContains(rowsLow, "0.0450") {
		t.Errorf("low-attainment fixture's rendered crime rate missing 0.0450: %v", rowsLow)
	}
	if !rowContains(rowsHigh, "0.0080") {
		t.Errorf("high-attainment fixture's rendered crime rate missing 0.0080: %v", rowsHigh)
	}
	if strings.Join(rowsLow, "\n") == strings.Join(rowsHigh, "\n") {
		t.Error("linkage render did not distinguish the two fixtures -- direction/structure not surfaced")
	}

	// The render must never print a computed slope/elasticity figure
	// distinct from the subscribed fields -- it has exactly the five
	// documented figures.
	if rowContains(rowsLow, "elasticity") || rowContains(rowsLow, "slope") {
		t.Error("render printed a screen-computed elasticity/slope term -- AC-8 forbids the screen inventing its own coefficient")
	}
}

// TestEducationCrimeLinkage_UnavailableRendersExplicitly proves a
// not-yet-sent linkage view renders "unavailable", never a zero report.
func TestEducationCrimeLinkage_UnavailableRendersExplicitly(t *testing.T) {
	rows := renderLinkageInto(EducationCrimeLinkage{}, false)
	if !rowContains(rows, "unavailable") {
		t.Error("RenderEducationCrimeLinkage with have=false did not render \"unavailable\"")
	}
}
