package census

import (
	"reflect"
	"testing"
)

// TestCitizenBioAssemblesFacets proves a citizen bio assembles every facet
// from the owning modules' query surfaces — education, employment, family,
// home, workplace, retirement, and income (AC-9).
func TestCitizenBioAssemblesFacets(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)

	cv := mkCitizen(42)
	cv.BirthMonth = 12
	cv.Sex = SexMale
	cv.Employment = EmploymentEmployed
	cv.Sector = SectorTertiary
	cv.Workplace = 900
	w.citizens.set(cv)
	w.education.set(42, EducationView{
		Attainment: 80,
		Stages:     []StageView{{Stage: StagePrimary, StartMonth: 0, EndMonth: 60}},
	})
	w.finance.setIncome(42, 55_000_000)

	bio, err := c.CitizenBio(citizenGUID(42), 100, "test")
	if err != nil {
		t.Fatalf("CitizenBio: %v", err)
	}
	if bio.ID != 42 || bio.Sex != SexMale {
		t.Fatalf("bio identity wrong: %+v", bio)
	}
	if bio.Education.Attainment != 80 {
		t.Fatalf("bio education not assembled: %+v", bio.Education)
	}
	if bio.Employment.Sector != SectorTertiary || bio.Employment.Workplace != 900 {
		t.Fatalf("bio employment not assembled: %+v", bio.Employment)
	}
	if bio.Family.Home != cv.Home {
		t.Fatalf("bio family/home not assembled: %+v", bio.Family)
	}
	if bio.Income != 55_000_000 {
		t.Fatalf("bio income not sourced from finance: %d", bio.Income)
	}
	// Retirement = birth month + 68 years (the data-file placeholder).
	if bio.Retirement.RetirementMonth != 12+68*12 {
		t.Fatalf("bio retirement month wrong: %d", bio.Retirement.RetirementMonth)
	}
}

// TestEducationBioIndustryTie proves the education bio carries the
// specialist-university-to-industry tie sourced from EducationSource (AC-10).
func TestEducationBioIndustryTie(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(7))
	w.education.set(7, EducationView{
		Attainment: 95,
		Stages: []StageView{
			{Stage: StagePrimary, StartMonth: 0, EndMonth: 60},
			{Stage: StageUniversity, StartMonth: 60, EndMonth: -1},
		},
		IndustryTie: "pharma", // the Pfizer-Sandwich-class tie, sourced not authored
	})

	bio, err := c.CitizenBio(citizenGUID(7), 100, "test")
	if err != nil {
		t.Fatalf("CitizenBio: %v", err)
	}
	if bio.Education.IndustryTie != "pharma" {
		t.Fatalf("industry tie not carried: %+v", bio.Education)
	}
	if len(bio.Education.Stages) != 2 || bio.Education.Stages[1].Stage != StageUniversity {
		t.Fatalf("stage trajectory not carried: %+v", bio.Education.Stages)
	}
}

// TestNonCitizenCarHouseChopperTracked proves a car and a citizen both
// satisfy the generic tracked-object contract, with distinct GUIDs and
// distinct life-history shapes (AC-11).
func TestNonCitizenCarHouseChopperTracked(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))

	must(t, c.TrackObject(carGUID(100), ObjectCar, LifeSpanShortLived))
	must(t, c.TrackObject(houseGUID(200), ObjectHouse, LifeSpanWholeGame))
	must(t, c.TrackObject(chopperGUID(300), ObjectChopper, LifeSpanShortLived))
	must(t, c.RecordLifeEvent(carGUID(100), 5, "mileage: 12000"))

	if err := c.RunObservers(5, "test"); err != nil {
		t.Fatalf("RunObservers: %v", err)
	}

	objs := c.TrackedObjects()
	if len(objs) != 4 { // 1 citizen + car + house + chopper
		t.Fatalf("want 4 tracked objects, got %d: %v", len(objs), objs)
	}

	car, err := c.ObjectBio(carGUID(100), "test")
	if err != nil {
		t.Fatalf("ObjectBio(car): %v", err)
	}
	if car.Kind != ObjectCar || car.LifeSpan != LifeSpanShortLived {
		t.Fatalf("car bio shape wrong: %+v", car)
	}
	if len(car.LifeHistory) != 1 || car.LifeHistory[0].Description != "mileage: 12000" {
		t.Fatalf("car life-history wrong: %+v", car.LifeHistory)
	}

	// The citizen's bio is a different (richer) shape.
	_, err = c.CitizenBio(citizenGUID(1), 5, "test")
	if err != nil {
		t.Fatalf("CitizenBio: %v", err)
	}
}

// TestBioDeterministic proves assembling the same bio twice at the same tick
// returns byte-identical output (AC-13).
func TestBioDeterministic(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(9))
	w.education.set(9, EducationView{Attainment: 60, Stages: []StageView{{Stage: StageSecondary}}})
	w.finance.setIncome(9, 10_000_000)

	a, err := c.CitizenBio(citizenGUID(9), 33, "test")
	if err != nil {
		t.Fatalf("CitizenBio: %v", err)
	}
	b, err := c.CitizenBio(citizenGUID(9), 33, "test")
	if err != nil {
		t.Fatalf("CitizenBio: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("bio not deterministic:\na=%+v\nb=%+v", a, b)
	}
}
