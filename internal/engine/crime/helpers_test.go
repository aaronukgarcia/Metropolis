package crime

import (
	"math"
	"testing"
)

// Shared test scaffolding for engine.crime. Every test constructs its own
// CrimeAPI with a fixed world seed so the counter-based streams are
// deterministic across runs (GR#21).

func testAPI(t *testing.T) *CrimeAPI {
	t.Helper()
	a, err := New(42, "crime-test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// defaultDistrict returns a benign baseline district: no gang-formation
// conditions, non-zero drivers so every type has a measurable figure.
func defaultDistrict(id DistrictID) DistrictInput {
	return DistrictInput{
		District:                 id,
		OwnDeprivation:           0.5,
		NeighbourWealth:          0.5,
		YouthUnemployment:        0.1,
		Blight:                   0.1,
		YouthLeisureDesert:       0.2,
		PolicePresence:           0.5,
		EraWealth:                0.3,
		PortThroughput:           0.2,
		CustomsFunding:           0.2,
		PatrolCoverage:           5,
		DetectiveCapacity:        5,
		PreventionInfrastructure: 0.3,
		EligiblePool:             100000,
		RegenerationInvestment:   0,
		PrisonAbsorption:         0,
		CourthouseThroughput:     10000,
	}
}

// formationDistrict returns a district whose conditions hold for gang
// formation: high youth unemployment, high blight, low clearance (zero
// detectives), and no regeneration investment.
func formationDistrict(id DistrictID) DistrictInput {
	d := defaultDistrict(id)
	d.YouthUnemployment = 0.3
	d.Blight = 0.3
	d.DetectiveCapacity = 0
	d.PatrolCoverage = 0
	d.RegenerationInvestment = 0
	return d
}

func advance(t *testing.T, a *CrimeAPI, month int64, districts ...DistrictInput) {
	t.Helper()
	if err := a.AdvanceMonth(month, districts, SecurityInput{}); err != nil {
		t.Fatalf("AdvanceMonth(%d): %v", month, err)
	}
}

func advanceSec(t *testing.T, a *CrimeAPI, month int64, sec SecurityInput, districts ...DistrictInput) {
	t.Helper()
	if err := a.AdvanceMonth(month, districts, sec); err != nil {
		t.Fatalf("AdvanceMonth(%d): %v", month, err)
	}
}

// almostEqual is the float comparison tolerance used throughout (generation
// figures are per-100k rates, so 1e-6 is well below any meaningful delta).
func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

// formGang runs formationDistrict for the given district for formationMonths
// months, then returns the API and the month at which the gang formed. The
// caller may continue advancing.
func formGang(t *testing.T, id DistrictID) (*CrimeAPI, GangID) {
	t.Helper()
	a := testAPI(t)
	for m := int64(0); m < 24; m++ {
		advance(t, a, m, formationDistrict(id))
	}
	ids := a.GangIDs()
	if len(ids) != 1 {
		t.Fatalf("expected exactly 1 gang to form after 24 sustained months, got %d", len(ids))
	}
	return a, ids[0]
}
