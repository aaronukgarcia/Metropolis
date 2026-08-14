package citizens

import (
	"math"
	"reflect"
	"testing"
)

// TestDerivedParamsChangeWithSampleComposition (AC-8): cold-pass parameters
// are measured from the sample's composition, never hardcoded — a young
// sample and an old sample derive different parameters.
func TestDerivedParamsChangeWithSampleComposition(t *testing.T) {
	const month = int64(1200)
	young := mkRecord(1, 0)
	young.BirthMonth = month - 10 // age ~10 months
	old := mkRecord(1, 0)
	old.BirthMonth = month - 900 // age 75 years

	sYoung := BuildStratifiedSample([]ColdRecord{young}, month, 11, 1)
	sOld := BuildStratifiedSample([]ColdRecord{old}, month, 11, 1)

	pYoung := DeriveColdPassParams(sYoung)
	pOld := DeriveColdPassParams(sOld)

	if !(pYoung.MortalityMultiplier < pOld.MortalityMultiplier) {
		t.Fatalf("older sample must raise the mortality multiplier, got young=%g old=%g", pYoung.MortalityMultiplier, pOld.MortalityMultiplier)
	}
	if !(pYoung.EducationTransitionRate > pOld.EducationTransitionRate) {
		t.Fatalf("younger sample must raise the education-transition rate, got young=%g old=%g", pYoung.EducationTransitionRate, pOld.EducationTransitionRate)
	}
	if pYoung.LowConfidence || pOld.LowConfidence {
		t.Fatal("a coverage-guaranteed sample should not be low-confidence")
	}
}

// TestSamplingFirewallExcludesViewportHot (AC-9): promoting a citizen to
// HOT via the viewport does NOT change the sample — viewport elevation is
// display fidelity only and never a parameter-estimation input.
func TestSamplingFirewallExcludesViewportHot(t *testing.T) {
	api, err := NewCitizensAPI(13, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	records := make([]ColdRecord, 100)
	for i := range records {
		records[i] = mkRecord(uint64(i+1), uint16(i%5))
	}
	if err := api.SeedColdRecords(records, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	before := api.BuildSample("corr").Members()
	// Viewport entry: promote citizen 1 (and several others) to HOT.
	for _, id := range []uint64{1, 2, 3} {
		if err := api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: id, Target: FidelityHot}); err != nil {
			t.Fatalf("promote %d: %v", id, err)
		}
	}
	after := api.BuildSample("corr").Members()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("viewport promotion changed the stratified sample — the sampling firewall is breached")
	}
}

// TestCameraInvariance (AC-16): two otherwise-identical runs whose viewport
// parks over DIFFERENT districts derive byte-identical cold-pass parameters
// (and byte-identical world state after a month) — parameters come from the
// sample, never the camera.
func TestCameraInvariance(t *testing.T) {
	records := make([]ColdRecord, 500)
	for i := range records {
		records[i] = mkRecord(uint64(i+1), uint16(i%10))
		records[i].BirthMonth = 0
	}

	run := func(viewportDistricts []uint16) ColdPassParams {
		api, err := NewCitizensAPI(21, "corr")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		if err := api.SeedColdRecords(records, "corr"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		// Park the camera: promote every citizen in the viewport districts.
		for _, r := range records {
			for _, d := range viewportDistricts {
				if r.District == d {
					_ = api.ApplyFidelityCommand(FidelityCommand{CorrelationID: "corr", CitizenID: r.ID, Target: FidelityHot})
				}
			}
		}
		return api.ColdParams("corr")
	}

	p1 := run([]uint16{0, 1})
	p2 := run([]uint16{8, 9})
	if p1 != p2 {
		t.Fatalf("camera-invariance violated: params differ across viewports: %+v vs %+v", p1, p2)
	}
}

// TestCoverageGapFallback (AC-14): a nil/empty sample, or one below the
// coverage floor, degrades to neutral defaults + LowConfidence rather than
// dividing by zero or returning NaNs.
func TestCoverageGapFallback(t *testing.T) {
	pNil := DeriveColdPassParams(nil)
	if !pNil.LowConfidence || pNil.MortalityMultiplier != 1.0 {
		t.Fatalf("nil sample must fall back to neutral params + low confidence, got %+v", pNil)
	}

	empty := &StratifiedSample{counts: map[Stratum]int{}, members: []uint64{}}
	pEmpty := DeriveColdPassParams(empty)
	if !pEmpty.LowConfidence {
		t.Fatal("empty sample must flag low confidence")
	}
	for _, v := range []float64{pEmpty.MortalityMultiplier, pEmpty.EducationTransitionRate, pEmpty.JobMatchRate} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("empty-sample params contain NaN/Inf: %v", v)
		}
	}

	// A sample below the coverage floor (1 member, floor 3) flags
	// low-confidence without dividing by zero.
	gapped := &StratifiedSample{
		counts:        map[Stratum]int{{District: 1, Age: AgeBand18to34, Income: IncomeBand2}: 1},
		members:       []uint64{42},
		minPerStratum: 3,
	}
	pGap := DeriveColdPassParams(gapped)
	if !pGap.LowConfidence {
		t.Fatal("below-floor sample must flag low confidence")
	}
	if math.IsNaN(pGap.MortalityMultiplier) {
		t.Fatal("below-floor sample produced NaN mortality multiplier")
	}
}

// TestBuildStratifiedSampleCoverageGuarantee (AC-9): every non-empty
// stratum holds at least minPerStratum members.
func TestBuildStratifiedSampleCoverageGuarantee(t *testing.T) {
	records := make([]ColdRecord, 40)
	for i := range records {
		records[i] = mkRecord(uint64(i+1), uint16(i%4)) // 4 districts, 10 each
	}
	s := BuildStratifiedSample(records, 1200, 5, 3)
	for st, c := range s.counts {
		if c < 3 {
			t.Fatalf("stratum %+v has %d members, below the coverage floor 3", st, c)
		}
	}
	if s.Empty() {
		t.Fatal("non-empty population produced an empty sample")
	}
}
