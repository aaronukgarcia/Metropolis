package census

// AC-7 (citizen bio drill-in -- cradle-to-grave facets rendered from
// CensusAPI.CitizenBio, mirrors engine.census.md AC-9/AC-10).

import (
	"testing"
)

// TestCitizenBioRender_AllFacetsVerbatim feeds a fixture bio (including a
// specialist-university industry tie) and asserts all five facets render
// the fixture's values verbatim.
func TestCitizenBioRender_AllFacetsVerbatim(t *testing.T) {
	s := newScreenWithData(t, "sub-bio")
	bio, have := s.SelectedBio()
	if !have {
		t.Fatal("SelectedBio() have=false, want true")
	}

	if bio.GUID != "citizen:42" || bio.ID != 42 {
		t.Errorf("bio identity = %+v, want GUID citizen:42 ID 42", bio)
	}
	// Education facet.
	if bio.Education.Attainment != 750 || bio.Education.Schooling != 144 {
		t.Errorf("bio.Education = %+v, want attainment 750 schooling 144", bio.Education)
	}
	if bio.Education.IndustryTie != "specialist-university:fintech" {
		t.Errorf("bio.Education.IndustryTie = %q, want the fixture's specialist-university tie", bio.Education.IndustryTie)
	}
	if len(bio.Education.Stages) != 2 || bio.Education.Stages[1].Stage != "university" {
		t.Errorf("bio.Education.Stages = %+v, want the fixture's 2-stage trajectory ending in university", bio.Education.Stages)
	}
	// Employment facet.
	if bio.Employment.State != "employed" || bio.Employment.Sector != "tertiary" || bio.Employment.Workplace != 901 {
		t.Errorf("bio.Employment = %+v, want fixture employment facet", bio.Employment)
	}
	// Family facet.
	if bio.Family.Household != 7 || bio.Family.Partner != 43 || bio.Family.Home != 501 {
		t.Errorf("bio.Family = %+v, want fixture family facet", bio.Family)
	}
	// Retirement facet.
	if bio.Retirement != 900 {
		t.Errorf("bio.Retirement = %d, want 900", bio.Retirement)
	}
	// Income facet.
	if bio.Income != 45000000 {
		t.Errorf("bio.Income = %d, want 45000000", bio.Income)
	}

	rows := renderCitizenBioRows(t, bio, true)
	mustContainAll(t, rows, []string{
		"specialist-university:fintech",
		"employed",
		"tertiary",
		"901",
		"900", // retirement month appears
		"45000000",
	})
}

// TestCitizenBioRender_StagesDeepCopied proves SelectedBio's Stages slice
// does not alias the screen's internal state (SEC-063/AC-13): mutating a
// second call's returned slice must not corrupt a first call's already-
// returned value.
func TestCitizenBioRender_StagesDeepCopied(t *testing.T) {
	s := newScreenWithData(t, "sub-bio-alias")
	first, _ := s.SelectedBio()
	firstStagesLen := len(first.Education.Stages)

	second, _ := s.SelectedBio()
	second.Education.Stages[0].Stage = "corrupted"

	third, _ := s.SelectedBio()
	if third.Education.Stages[0].Stage == "corrupted" {
		t.Error("mutating one SelectedBio() call's Stages slice corrupted a later call -- accessor is aliasing internal state (SEC-063)")
	}
	if len(first.Education.Stages) != firstStagesLen {
		t.Error("first.Education.Stages length changed after a later call -- aliasing")
	}
}
