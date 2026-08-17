package social

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing"
)

// testConfig returns a Config whose magnitudes are small round numbers chosen
// so the mechanisms are fast and unambiguous to exercise. The real magnitudes
// live in data/social.json; these fixture values only make the decomposed
// caseload arithmetic crisp (GR#15's test-fixture latitude — a validator
// derives from data, a unit test may inject a fixture). Hostel and foster
// capacity are tiny (2) so capacity-exhaustion paths are one step away.
func testConfig() Config {
	return Config{
		RoughSleepingLocation: "town-centre",
		Caseload: CaseloadConfig{
			FamilyPerDeprivation:             4,
			FamilyPerCrowdingStress:          3,
			FamilyPerFinancialStress:         5,
			CrisisFamilyCases:                2,
			HomelessnessPerDeprivation:       3,
			HomelessnessPerUnemploymentMonth: 0.2,
			HomelessnessPerFinancialStress:   4,
			DisabilityPerDeprivation:         2,
			FosteringPerCrowdingStress:       1,
			FosteringPerFinancialStress:      1,
			AddictionPerPressure:             6,
			UnemploymentCapMonths:            60,
		},
		HostelCapacity:               2,
		FosterCapacity:               2,
		CarersReleasedPerFundingUnit: 60,
		InterventionHarmThreshold:    0.5,
	}
}

// count returns the number of NewCase proposals in the given category.
func count(cases []NewCase, c Category) int64 {
	var n int64
	for _, x := range cases {
		if x.Category == c {
			n++
		}
	}
	return n
}

// mustGenerateCaseload calls GenerateCaseload and fails the test on error.
// The steady-state generator now validates its DriverInputs at the boundary
// (SEC-181), so every existing test that feeds a fixture driver set goes
// through this single error-checking shim.
func mustGenerateCaseload(t *testing.T, a *SocialAPI, month int64, in DriverInputs) []NewCase {
	t.Helper()
	out, err := a.GenerateCaseload(month, in)
	if err != nil {
		t.Fatalf("GenerateCaseload: %v", err)
	}
	return out
}

// testWellbeingFile returns a minimal, schema-valid wellbeing file with the
// given rent-burden threshold and family-stress weights — enough for the
// real engine.wellbeing to run the AC-3 35%-threshold path without loading a
// data file.
func testWellbeingFile(threshold, financialWeight, crowdingWeight float64) wellbeing.WellbeingFile {
	return wellbeing.WellbeingFile{
		Version:  1,
		Baseline: wellbeing.BaselineFile{Physical: 50, Mental: 50},
		Headline: wellbeing.HeadlineFile{PhysicalWeight: 1, MentalWeight: 1},
		Physical: wellbeing.PhysicalFile{
			AgeCurve: []wellbeing.AgeCurvePoint{{AgeYears: 0, Delta: 0}, {AgeYears: 100, Delta: 0}},
		},
		Mental: wellbeing.MentalFile{
			CommuteThresholdMinutes:   30,
			CommuteStressAtThreshold:  1,
			CommuteStressAt100Minutes: 2,
			CrowdingWeight:            crowdingWeight,
			FinancialStressWeight:     financialWeight,
			RentBurdenThreshold:       threshold,
			UnemploymentCapMonths:     60,
		},
	}
}

// seedCitizen appends one valid cold citizen record (mirroring
// engine.leisure's helper) so the intervention marker can be written to a
// real citizen record and read back through CitizensAPI.
func seedCitizen(t *testing.T, c *citizens.CitizensAPI, id uint64, birthMonth int64) {
	t.Helper()
	var p [citizens.NumPersonalityAxes]int8
	for i := range p {
		p[i] = 50
	}
	r := citizens.ColdRecord{
		ID:              id,
		BirthMonth:      birthMonth,
		Sex:             citizens.SexFemale,
		Personality:     p,
		Attainment:      0,
		Stage:           citizens.StageNone,
		HealthBand:      citizens.HealthExcellent,
		SatHousing:      50,
		SatServices:     50,
		SatEnvironment:  50,
		SatLeisureFit:   50,
		SatCommute:      50,
		EmploymentState: citizens.EmploymentNone,
	}
	if err := c.SeedColdRecords([]citizens.ColdRecord{r}, "test"); err != nil {
		t.Fatalf("seed citizen: %v", err)
	}
}
