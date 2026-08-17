package roads

import "testing"

// TestClassSlugsMatchNamingCorpus holds the GR#3 duplication across the
// data/naming_corpus.json boundary: this package's nine non-numbered class
// slugs must agree, one-for-one, with the "roadSuffixes" field names in
// data/naming_corpus.json (which foundation.data owns). If either side is
// renamed, this test fails — the drift is told to a developer, not to a
// user at runtime. The failure message explains WHY the duplication exists
// (see dev-team-process weakness #2).
func TestClassSlugsMatchNamingCorpus(t *testing.T) {
	expected := []string{
		"alley", "gravel", "residential_street", "two_lane", "one_way_pairs",
		"avenue_2_plus_2", "bus_lane_variant", "tram_track_variant", "dual_carriageway",
	}
	if len(expected) != int(ClassUrbanExpressway) {
		t.Fatalf("expected list length %d != number of non-numbered classes %d", len(expected), int(ClassUrbanExpressway))
	}
	for i, want := range expected {
		if classSlugs[i] != want {
			t.Errorf("classSlugs[%d] = %q, want %q — this slug is also the data/naming_corpus.json roadSuffixes key; changing one requires changing the other (GR#3 drift)", i, classSlugs[i], want)
		}
	}

	// Every non-numbered class resolves to a non-empty suffix list in the
	// loaded corpus; the two numbered classes resolve to nil (M-/A- scheme).
	a := newTestAPI(t)
	for c := RoadClass(0); c < ClassUrbanExpressway; c++ {
		if s := suffixesForClass(c, a.corpus.Categories.RoadSuffixes); len(s) == 0 {
			t.Errorf("class %s has no suffix list in data/naming_corpus.json", c.String())
		}
	}
	if suffixesForClass(ClassUrbanExpressway, a.corpus.Categories.RoadSuffixes) != nil {
		t.Errorf("urban_expressway must use the M-/A- numbering scheme, not suffixes")
	}
	if suffixesForClass(ClassMotorway, a.corpus.Categories.RoadSuffixes) != nil {
		t.Errorf("motorway must use the M-/A- numbering scheme, not suffixes")
	}
}
