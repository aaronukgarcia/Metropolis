package data

import (
	"reflect"
	"testing"
)

// minRoadPlaceNames is data.modes-naming.md AC-9's coverage floor (5x the
// 8 spec-cited §20 examples), logged as ASM-392 at BA time. It is a named
// constant traced to that assumption rather than a bare magic number, and
// it is asserted against the loaded real file — never against a hardcoded
// 44-name list (GR#15: the loader derives from the data file, and the test
// derives its expectation from the same named floor, not an invented count).
const minRoadPlaceNames = 40

// specCitedPlaceNames are the 8 place names §20 cites explicitly by
// example (data.modes-naming.md AC-8), transcribed verbatim from the
// spec — a checked-in reference list, not an invented total.
var specCitedPlaceNames = []string{
	"Cheriton", "Seabrook", "Pent", "Risborough", "Sandgate", "Downs", "Alkham", "Saltwood",
}

// TestNamingCorpus_RealFile_LoadsAndPopulates proves the committed
// data/naming_corpus.json (not a synthetic fixture) round-trips through
// the rich NamingCorpus type: the place-name list, the per-class suffix
// table, and the file's notes are all captured, and every road class
// carries at least one suffix.
func TestNamingCorpus_RealFile_LoadsAndPopulates(t *testing.T) {
	dir := realDataDir(t)
	n, err := LoadNamingCorpus(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadNamingCorpus(real data/naming_corpus.json): %v", err)
	}

	if n.Version != 1 {
		t.Errorf("Version = %d, want 1", n.Version)
	}
	if n.Comment == "" {
		t.Error("top-level $comment not captured")
	}
	if n.NumberedSchemeNote == "" {
		t.Error("numberedSchemeNote not captured")
	}
	if n.Categories.RoadSuffixesNote == "" {
		t.Error("roadSuffixesNote not captured")
	}

	names := n.Categories.RoadPlaceNames
	if len(names) < minRoadPlaceNames {
		t.Errorf("len(roadPlaceNames) = %d, want >= %d (AC-9/ASM-392 floor)", len(names), minRoadPlaceNames)
	}
	have := make(map[string]bool, len(names))
	for _, name := range names {
		have[name] = true
	}
	for _, want := range specCitedPlaceNames {
		if !have[want] {
			t.Errorf("spec-cited place name %q not present in roadPlaceNames (AC-8)", want)
		}
	}

	for _, c := range roadClasses(n.Categories.RoadSuffixes) {
		if len(c.suffixes) == 0 {
			t.Errorf("roadSuffixes.%s is empty, want >= 1 suffix (AC-10)", c.name)
		}
	}
}

// TestNamingCorpus_RoadSuffixesArrayRejected proves a malformed corpus
// where roadSuffixes is a JSON array (instead of a per-class object) is
// rejected with a registry-sourced schema error. Against the old flat-map
// skeleton this shape was silently accepted (an array decodes into
// []string), so this test fails against the skeleton.
func TestNamingCorpus_RoadSuffixesArrayRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileNamingCorpus, `{"version":1,"categories":{"roadPlaceNames":["Cheriton","Seabrook"],"roadSuffixes":["Close","Lane"]}}`)

	_, err := LoadNamingCorpus(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "roadSuffixes")
}

// TestNamingCorpus_MissingRoadClassRejected proves a corpus whose
// roadSuffixes object omits one of the nine non-numbered road classes
// (here dual_carriageway) is rejected with a registry-sourced schema
// error naming the missing class — not merely a generic type mismatch.
func TestNamingCorpus_MissingRoadClassRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileNamingCorpus, `{"version":1,"categories":{"roadPlaceNames":["Cheriton"],"roadSuffixes":{"alley":["Close"],"gravel":["Lane"],"residential_street":["Road"],"two_lane":["Street"],"one_way_pairs":["Street"],"avenue_2_plus_2":["Avenue"],"bus_lane_variant":["Way"],"tram_track_variant":["Drive"]}}}`)

	_, err := LoadNamingCorpus(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "dual_carriageway")
}

// TestNamingCorpus_EmptyPlaceNameRejected proves a blank entry in the
// place-name list is rejected (AC-11: distinct, non-empty strings).
func TestNamingCorpus_EmptyPlaceNameRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileNamingCorpus, `{"version":1,"categories":{"roadPlaceNames":["Cheriton",""],"roadSuffixes":{"alley":["Close"],"gravel":["Lane"],"residential_street":["Road"],"two_lane":["Street"],"one_way_pairs":["Street"],"avenue_2_plus_2":["Avenue"],"bus_lane_variant":["Way"],"tram_track_variant":["Drive"],"dual_carriageway":["Road"]}}}`)

	_, err := LoadNamingCorpus(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "roadPlaceNames[1]")
}

// TestNamingCorpus_DuplicatePlaceNameRejected proves a duplicated place
// name is rejected (AC-11).
func TestNamingCorpus_DuplicatePlaceNameRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileNamingCorpus, `{"version":1,"categories":{"roadPlaceNames":["Cheriton","Cheriton"],"roadSuffixes":{"alley":["Close"],"gravel":["Lane"],"residential_street":["Road"],"two_lane":["Street"],"one_way_pairs":["Street"],"avenue_2_plus_2":["Avenue"],"bus_lane_variant":["Way"],"tram_track_variant":["Drive"],"dual_carriageway":["Road"]}}}`)

	_, err := LoadNamingCorpus(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "duplicate place name")
}

// TestNamingCorpus_RepeatedLoadDeepEqual is the GR#21 determinism check
// (data.modes-naming.md AC-20): loading the real file twice yields
// structurally identical in-memory values.
func TestNamingCorpus_RepeatedLoadDeepEqual(t *testing.T) {
	dir := realDataDir(t)
	n1, err := LoadNamingCorpus(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	n2, err := LoadNamingCorpus(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !reflect.DeepEqual(n1, n2) {
		t.Error("repeated LoadNamingCorpus of the same file produced non-equal structs")
	}
}
