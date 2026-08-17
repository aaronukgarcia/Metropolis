package census

import (
	"reflect"
	"testing"
)

// TestAggregateRespondsToInputChange proves the stats generator recomputes
// from live state: mutating one citizen's employment state via the owning
// module changes exactly the aggregates derived from it (AC-3).
func TestAggregateRespondsToInputChange(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	for i := uint64(1); i <= 10; i++ {
		cv := mkCitizen(i)
		cv.Employment = EmploymentEmployed
		cv.Sector = SectorSecondary
		w.citizens.set(cv)
	}

	before, err := c.Snapshot(100, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	a0 := c.Stats(before)
	if a0.Employed != 10 || a0.Unemployed != 0 {
		t.Fatalf("seed employment wrong: %+v", a0)
	}

	// Mutate one citizen's employment state (the owning module's command).
	changed := mkCitizen(5)
	changed.Employment = EmploymentUnemployed
	w.citizens.set(changed)

	after, err := c.Snapshot(100, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	a1 := c.Stats(after)
	if a1.Employed != 9 || a1.Unemployed != 1 {
		t.Fatalf("aggregate did not move by exactly the one change: employed=%d unemployed=%d",
			a1.Employed, a1.Unemployed)
	}
}

// TestSplineSeriesIndependent proves the three spline series are computed
// per-citizen and independently perturbable: changing only education inputs
// leaves the age-band series byte-identical (AC-18).
func TestSplineSeriesIndependent(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	seed := func() {
		// 4 citizens: two 20-year-olds (band 1), one 40-year-old (band 2),
		// one 80-year-old (band 4); 2 female + 2 male.
		ages := []int64{20, 20, 40, 80}
		sexes := []Sex{SexFemale, SexMale, SexFemale, SexMale}
		for i, y := range ages {
			cv := mkCitizen(uint64(i + 1))
			cv.BirthMonth = 1000 - y*12
			cv.Sex = sexes[i]
			w.citizens.set(cv)
		}
	}
	seed()
	for i := uint64(1); i <= 4; i++ {
		w.education.set(i, EducationView{Attainment: 50, Stages: []StageView{{Stage: StageSecondary}}})
	}

	snap, err := c.Snapshot(1000, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	ages := c.AgeBandSeries(snap)
	if !reflect.DeepEqual(ages, [numAgeBands]int64{0, 2, 1, 0, 1}) {
		t.Fatalf("age-band series wrong: %v", ages)
	}
	sexes := c.SexSeries(snap)
	if !reflect.DeepEqual(sexes, [2]int64{2, 2}) {
		t.Fatalf("sex series wrong: %v", sexes)
	}
	tiers := c.EducationTierSeries(snap)
	if tiers[StageSecondary] != 4 {
		t.Fatalf("education-tier series wrong: %v", tiers)
	}

	// Change only education inputs; the age-band series must be unchanged.
	for i := uint64(1); i <= 4; i++ {
		w.education.set(i, EducationView{Attainment: 90, Stages: []StageView{{Stage: StageUniversity}}})
	}
	snap2, err := c.Snapshot(1000, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !reflect.DeepEqual(c.AgeBandSeries(snap2), ages) {
		t.Fatalf("age-band series changed when only education changed")
	}
	tiers2 := c.EducationTierSeries(snap2)
	if tiers2[StageUniversity] != 4 {
		t.Fatalf("education-tier series did not move: %v", tiers2)
	}
}

// TestAgeBandSeriesReproducesDistribution checks the age-band bucketing
// boundary behaviour (AC-18).
func TestAgeBandSeriesReproducesDistribution(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	years := []int64{10, 17, 18, 34, 35, 54, 55, 74, 75, 90}
	for i, y := range years {
		cv := mkCitizen(uint64(i + 1))
		cv.BirthMonth = 2000 - y*12
		w.citizens.set(cv)
	}
	snap, err := c.Snapshot(2000, "test")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// band0: 10,17 (2); band1: 18,34 (2); band2: 35,54 (2); band3: 55,74 (2); band4: 75,90 (2)
	want := [numAgeBands]int64{2, 2, 2, 2, 2}
	if got := c.AgeBandSeries(snap); !reflect.DeepEqual(got, want) {
		t.Fatalf("age-band distribution wrong: got %v want %v", got, want)
	}
}
