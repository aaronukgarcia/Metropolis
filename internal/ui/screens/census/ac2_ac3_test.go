package census

// AC-2 (the "stub cannot fake" differential check) / AC-3 (three
// independently-perturbable demographic splines).

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func renderPyramidInto(bands [NumAgeBands]int64, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(80, 10)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderAgeBandPyramid(buf, rect, bands, have, widgets.DefaultPalette, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

func renderSexInto(sex [NumSexBuckets]int64, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(80, 10)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderSexSeries(buf, rect, sex, have, widgets.DefaultPalette, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

func renderEduTierInto(tiers [NumEducationTiers]int64, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(80, 10)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderEducationTierSeries(buf, rect, tiers, have, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

// TestAgeBandSpline_DifferentialStubCannotFake is AC-2/AC-3's differential
// single-field mutation check for the age-band spline: two wire patches
// differ in exactly one field (ageBands), and (a) the pyramid pane must
// render differently while (b) the untouched sex/education-tier panes
// must render byte-identically -- proving this screen reads the real
// subscribed field rather than hardcoding canned splines or wiring the
// wrong one. False-pass risk this rejects: a screen wired to a stub that
// always emits the same canned splines would pass a superficial "renders
// three splines" smoke test while never actually being wired to
// anything.
func TestAgeBandSpline_DifferentialStubCannotFake(t *testing.T) {
	base := fullPatch()
	mutated := fullPatch()

	mutatedBands := [NumAgeBands]int64{1000, 2500, 2200, 1400, 9999}
	mutated.AgeBands = &mutatedBands

	sA := New("corr-a")
	sA.BindSubscription("sub-a")
	sA.ApplyDelta(protocolDelta(t, "sub-a", base))

	sB := New("corr-b")
	sB.BindSubscription("sub-b")
	sB.ApplyDelta(protocolDelta(t, "sub-b", mutated))

	bandsA, _ := sA.AgeBandSeries()
	bandsB, _ := sB.AgeBandSeries()
	sexA, _ := sA.SexSeries()
	sexB, _ := sB.SexSeries()
	eduA, _ := sA.EducationTierSeries()
	eduB, _ := sB.EducationTierSeries()

	pa, paRect := renderPyramidInto(bandsA, true)
	pb, _ := renderPyramidInto(bandsB, true)
	if bufsEqual(pa, pb, paRect) {
		t.Error("age-band pyramid unchanged after mutating ageBands (a) -- screen may be wired to a stub, not the subscribed field")
	}

	sa, saRect := renderSexInto(sexA, true)
	sb, _ := renderSexInto(sexB, true)
	if !bufsEqual(sa, sb, saRect) {
		t.Error("sex series pane changed even though ageBands is its only mutated input -- AC-3 independence violated")
	}

	ea, eaRect := renderEduTierInto(eduA, true)
	eb, _ := renderEduTierInto(eduB, true)
	if !bufsEqual(ea, eb, eaRect) {
		t.Error("education-tier pane changed even though ageBands is its only mutated input -- AC-3 independence violated")
	}
	if bandsA == bandsB {
		t.Error("fixture ageBands did not actually differ between runs -- test setup bug")
	}
}

// TestSexSpline_Independent is AC-3's second independence pair: mutating
// only sexSeries must leave the age-band and education-tier panes
// byte-identical.
func TestSexSpline_Independent(t *testing.T) {
	base := fullPatch()
	mutated := fullPatch()
	mutatedSex := [NumSexBuckets]int64{100, 9000}
	mutated.SexSeries = &mutatedSex

	sA := New("corr-a2")
	sA.BindSubscription("sub-a2")
	sA.ApplyDelta(protocolDelta(t, "sub-a2", base))
	sB := New("corr-b2")
	sB.BindSubscription("sub-b2")
	sB.ApplyDelta(protocolDelta(t, "sub-b2", mutated))

	sexA, _ := sA.SexSeries()
	sexB, _ := sB.SexSeries()
	bandsA, _ := sA.AgeBandSeries()
	bandsB, _ := sB.AgeBandSeries()
	eduA, _ := sA.EducationTierSeries()
	eduB, _ := sB.EducationTierSeries()

	sa, saRect := renderSexInto(sexA, true)
	sb, _ := renderSexInto(sexB, true)
	if bufsEqual(sa, sb, saRect) {
		t.Error("sex series pane unchanged after mutating sexSeries")
	}

	pa, paRect := renderPyramidInto(bandsA, true)
	pb, _ := renderPyramidInto(bandsB, true)
	if !bufsEqual(pa, pb, paRect) {
		t.Error("age-band pyramid changed even though sexSeries is its only mutated input -- AC-3 independence violated")
	}

	ea, eaRect := renderEduTierInto(eduA, true)
	eb, _ := renderEduTierInto(eduB, true)
	if !bufsEqual(ea, eb, eaRect) {
		t.Error("education-tier pane changed even though sexSeries is its only mutated input -- AC-3 independence violated")
	}
}

// TestEducationTierSpline_Independent is AC-3's third independence pair:
// mutating only educationTiers must leave the age-band and sex panes
// byte-identical.
func TestEducationTierSpline_Independent(t *testing.T) {
	base := fullPatch()
	mutated := fullPatch()
	mutatedEdu := [NumEducationTiers]int64{200, 300, 1500, 1800, 900, 700, 1800, 9999}
	mutated.EducationTiers = &mutatedEdu

	sA := New("corr-a3")
	sA.BindSubscription("sub-a3")
	sA.ApplyDelta(protocolDelta(t, "sub-a3", base))
	sB := New("corr-b3")
	sB.BindSubscription("sub-b3")
	sB.ApplyDelta(protocolDelta(t, "sub-b3", mutated))

	eduA, _ := sA.EducationTierSeries()
	eduB, _ := sB.EducationTierSeries()
	bandsA, _ := sA.AgeBandSeries()
	bandsB, _ := sB.AgeBandSeries()
	sexA, _ := sA.SexSeries()
	sexB, _ := sB.SexSeries()

	ea, eaRect := renderEduTierInto(eduA, true)
	eb, _ := renderEduTierInto(eduB, true)
	if bufsEqual(ea, eb, eaRect) {
		t.Error("education-tier pane unchanged after mutating educationTiers")
	}

	pa, paRect := renderPyramidInto(bandsA, true)
	pb, _ := renderPyramidInto(bandsB, true)
	if !bufsEqual(pa, pb, paRect) {
		t.Error("age-band pyramid changed even though educationTiers is its only mutated input -- AC-3 independence violated")
	}

	sa, saRect := renderSexInto(sexA, true)
	sb, _ := renderSexInto(sexB, true)
	if !bufsEqual(sa, sb, saRect) {
		t.Error("sex series pane changed even though educationTiers is its only mutated input -- AC-3 independence violated")
	}
}
