package services

import (
	"testing"
)

// --- AC-5: Public Service Pie benchmark ratios, data-loaded --------------

// TestPublicServicePiePoliceRatio (AC-5, GR#15): the loaded police
// benchmark is exactly 2.4 per 1,000 — transcribed from §54, not invented.
func TestPublicServicePiePoliceRatio(t *testing.T) {
	a := testLoadedAPI(t)
	p, err := a.BenchmarkRatio("police")
	if err != nil {
		t.Fatalf("BenchmarkRatio(police): %v", err)
	}
	if p.PerThousand != 2.4 {
		t.Fatalf("police.PerThousand = %v, want 2.4 (§54)", p.PerThousand)
	}
	if p.Placeholder {
		t.Errorf("police benchmark flagged placeholder, want the §54-transcribed figure")
	}
}

// TestPublicServicePieAllEightCategoriesPresent (AC-5): all eight §54-named
// categories are present and data-driven — police, teachers, nurses & GPs,
// dentists & opticians, firefighters, social workers, refuse crews, council
// officers.
func TestPublicServicePieAllEightCategoriesPresent(t *testing.T) {
	a := testLoadedAPI(t)
	want := []string{
		"police", "teachers", "nursesGps", "dentistsOpticians",
		"firefighters", "socialWorkers", "refuseCrews", "councilOfficers",
	}
	for _, id := range want {
		b, err := a.BenchmarkRatio(id)
		if err != nil {
			t.Fatalf("BenchmarkRatio(%s): %v — category missing from data", id, err)
		}
		if b.PerThousand == 0 && b.PerPupil == 0 {
			t.Errorf("benchmark %s has no ratio (perThousand=%v perPupil=%v)", id, b.PerThousand, b.PerPupil)
		}
	}
}

// TestStaffingNeedDerivesFromBenchmark (AC-5 structural half): a per-1k
// benchmark staffing need is ratio × population/1000 — police at 2000
// population needs 4.8 staff (2.4 × 2).
func TestStaffingNeedDerivesFromBenchmark(t *testing.T) {
	a := testLoadedAPI(t)
	need, err := a.StaffingNeed(ServicePoliceJail, 2000)
	if err != nil {
		t.Fatalf("StaffingNeed: %v", err)
	}
	if need != 4.8 {
		t.Fatalf("StaffingNeed(police, 2000) = %v, want 4.8 (2.4 × 2000/1000)", need)
	}
}

// TestStaffingNeedUnknownKindRejected: an unregistered kind is a registry
// error (AC-11), distinct from a legitimate zero-need result.
func TestStaffingNeedUnknownKindRejected(t *testing.T) {
	a := testLoadedAPI(t)
	if _, err := a.StaffingNeed("not-a-kind", 1000); err == nil {
		t.Fatal("StaffingNeed(unknown kind) returned nil, want ErrUnknownServiceKind")
	} else {
		assertCode(t, err, ErrUnknownServiceKind)
	}
}

// --- AC-6: scale-dependent consequences ----------------------------------

// TestScaleConsequenceVillageVsCity (AC-6): an IDENTICAL relative
// shortfall (10% below benchmark) produces a materially smaller impact at
// a village population (2,000) than at a city population (2,000,000) —
// §54's "mild at village scale, systemic at city scale".
func TestScaleConsequenceVillageVsCity(t *testing.T) {
	a := testLoadedAPI(t)
	halfPoint := a.SeverityHalfPoint()
	if halfPoint <= 0 {
		t.Fatalf("SeverityHalfPoint = %v, want positive (from data/services.json)", halfPoint)
	}

	village := ShortfallImpact(0.10, 2000, halfPoint)
	city := ShortfallImpact(0.10, 2_000_000, halfPoint)

	if village >= city {
		t.Fatalf("ShortfallImpact(10%%, 2k) = %v, want < ShortfallImpact(10%%, 2M) = %v", village, city)
	}
	// "Materially smaller" — require at least an order of magnitude of
	// separation, not merely a token difference.
	if city < village*10 {
		t.Errorf("scale separation too small: village %v vs city %v (want city >= 10× village)", village, city)
	}
}

// TestShortfallImpactIsPureAndBounded: the consequence curve is a pure,
// bounded function of its inputs.
func TestShortfallImpactIsPureAndBounded(t *testing.T) {
	for i := 0; i < 10; i++ {
		got := ShortfallImpact(0.25, 500_000, 100_000)
		if got != ShortfallImpact(0.25, 500_000, 100_000) {
			t.Fatalf("ShortfallImpact not pure on iteration %d", i)
		}
		if got < 0 || got > 1 {
			t.Fatalf("ShortfallImpact = %v, want in [0,1]", got)
		}
	}
}
