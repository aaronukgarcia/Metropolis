package census

import (
	"testing"
)

// TestLessEducationMoreCrime proves the education→crime linkage is computed
// from the live modules' data: a lower-education population reports the
// higher crime figure, and mutating either source moves only its own side
// of the report — never a hardcoded "crime = -education × k" line (AC-14).
func TestLessEducationMoreCrime(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))

	w.education.set(1, EducationView{Attainment: 10})
	w.crime.setRate(0.20)
	low, err := c.Snapshot(1, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	lowLink := c.EducationCrimeLinkage(low)

	w.education.set(1, EducationView{Attainment: 90})
	w.crime.setRate(0.01)
	high, err := c.Snapshot(1, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	highLink := c.EducationCrimeLinkage(high)

	if !(lowLink.MeanAttainment < highLink.MeanAttainment) {
		t.Fatalf("attainment direction wrong: low=%v high=%v", lowLink.MeanAttainment, highLink.MeanAttainment)
	}
	if !(lowLink.CrimeRate > highLink.CrimeRate) {
		t.Fatalf("crime direction wrong: low=%v high=%v", lowLink.CrimeRate, highLink.CrimeRate)
	}

	// Mutating only crime moves CrimeRate, not MeanAttainment (data-derived,
	// not hardcoded).
	w.crime.setRate(0.30)
	moved, _ := c.Snapshot(1, "test")
	movedLink := c.EducationCrimeLinkage(moved)
	if movedLink.CrimeRate != 0.30 || movedLink.MeanAttainment != highLink.MeanAttainment {
		t.Fatalf("linkage not data-derived: %+v", movedLink)
	}
}

// TestBlueWhiteCollarMovesWithSector proves the blue/white split is emergent
// from per-citizen sector data: moving a cohort between sectors moves the
// split by exactly that cohort (AC-17).
func TestBlueWhiteCollarMovesWithSector(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	for i := uint64(1); i <= 10; i++ {
		cv := mkCitizen(i)
		cv.Sector = SectorSecondary // blue
		w.citizens.set(cv)
	}
	snap, _ := c.Snapshot(1, "test")
	bw := c.BlueWhiteCollar(snap)
	if bw.Blue != 10 || bw.White != 0 {
		t.Fatalf("seed split wrong: %+v", bw)
	}

	// Move a cohort of 3 citizens to the public (white) sector.
	for i := uint64(1); i <= 3; i++ {
		cv := mkCitizen(i)
		cv.Sector = SectorPublic // white
		w.citizens.set(cv)
	}
	snap2, _ := c.Snapshot(1, "test")
	bw2 := c.BlueWhiteCollar(snap2)
	if bw2.Blue != 7 || bw2.White != 3 {
		t.Fatalf("split did not move by exactly the cohort: %+v", bw2)
	}
}

// TestCityKPIConservation proves each of the eight named KPIs equals the
// aggregate of the owning modules' figures the same query surfaces report,
// and that changing one input changes only the KPIs derived from it (AC-19).
func TestCityKPIConservation(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)

	e1 := mkCitizen(1)
	e1.Employment = EmploymentEmployed
	e1.Home = 1001
	e2 := mkCitizen(2)
	e2.Employment = EmploymentEmployed
	e2.Home = 1002
	u3 := mkCitizen(3)
	u3.Employment = EmploymentUnemployed
	u3.Home = 0 // homeless
	w.citizens.set(e1)
	w.citizens.set(e2)
	w.citizens.set(u3)

	w.services.hospital = 45
	w.services.unfilled = 12
	w.services.skillDemand = 60
	w.finance.gdp = 1_000_000
	w.finance.land = 2_000_000
	w.wellbeing.happiness = 88

	snap, err := c.Snapshot(1, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if got := c.Homeless(snap); got != 1 {
		t.Fatalf("Homeless = %d want 1", got)
	}
	if got := c.OutOfWork(snap); got != 1 {
		t.Fatalf("OutOfWork = %d want 1", got)
	}
	if got := c.InHospital(snap); got != 45 {
		t.Fatalf("InHospital = %d want 45", got)
	}
	if got := c.UnfilledJobs(snap); got != 12 {
		t.Fatalf("UnfilledJobs = %d want 12", got)
	}
	if got := c.JobSkillDemand(snap); got != 60 {
		t.Fatalf("JobSkillDemand = %d want 60", got)
	}
	if got := c.GDP(snap); got != 1_000_000 {
		t.Fatalf("GDP = %d want 1000000", got)
	}
	if got := c.LandValue(snap); got != 2_000_000 {
		t.Fatalf("LandValue = %d want 2000000", got)
	}
	if got := c.Happiness(snap); got != 88 {
		t.Fatalf("Happiness = %v want 88", got)
	}

	// Change only the hospital input; only InHospital should move.
	w.services.hospital = 99
	snap2, _ := c.Snapshot(1, "test")
	if got := c.InHospital(snap2); got != 99 {
		t.Fatalf("InHospital did not move: %d", got)
	}
	if got := c.Homeless(snap2); got != 1 {
		t.Fatalf("Homeless moved when only hospital changed: %d", got)
	}
}

// TestPolicyObservedRewardEducation proves the census observes — never
// enacts — the reward/penalise-education policy: enacting a reward-education
// policy via the policies source is reflected in the linkage report's
// coefficient and the resulting attainment change, without the census
// enacting anything itself (AC-15).
func TestPolicyObservedRewardEducation(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))

	w.policies.coef = 0.0 // neutral
	w.education.set(1, EducationView{Attainment: 20})
	w.crime.setRate(0.15)
	before, _ := c.Snapshot(1, "test")
	bLink := c.EducationCrimeLinkage(before)
	if bLink.PolicyCoefficient != 0.0 {
		t.Fatalf("neutral policy coefficient wrong: %+v", bLink)
	}

	// Enact a reward-education policy (the policies module's own instrument);
	// its enacted effect raises attainment and lowers crime. The census only
	// reads the sources — it enacts nothing itself.
	w.policies.coef = 1.0
	w.education.set(1, EducationView{Attainment: 80})
	w.crime.setRate(0.02)
	after, _ := c.Snapshot(1, "test")
	aLink := c.EducationCrimeLinkage(after)

	if aLink.PolicyCoefficient != 1.0 {
		t.Fatalf("enacted policy not observed: %+v", aLink)
	}
	if !(aLink.MeanAttainment > bLink.MeanAttainment) {
		t.Fatalf("attainment did not rise with the reward policy: %v -> %v", bLink.MeanAttainment, aLink.MeanAttainment)
	}
}

// TestDrillSourceResolutionSumsToAggregate proves Source() resolves a KPI's
// drill-target to the entities that compose it, whose count sums to the
// reported aggregate (AC-20).
func TestDrillSourceResolutionSumsToAggregate(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)

	h1 := mkCitizen(1)
	h1.Home = 0
	h2 := mkCitizen(2)
	h2.Home = 0
	ok := mkCitizen(3)
	ok.Home = 5000
	w.citizens.set(h1)
	w.citizens.set(h2)
	w.citizens.set(ok)

	snap, _ := c.Snapshot(1, "test")
	res, err := c.Source(snap, KPIKeyHomeless)
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if res.LineValue != c.Homeless(snap) {
		t.Fatalf("source resolution does not sum to aggregate: %d vs %d", res.LineValue, c.Homeless(snap))
	}
	if len(res.EntityIDs) != 2 || res.EntityIDs[0] != 1 || res.EntityIDs[1] != 2 {
		t.Fatalf("source entity set wrong: %v", res.EntityIDs)
	}
}
