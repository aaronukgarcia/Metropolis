package data

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// realDataDir resolves the repo's actual data/ directory (not a
// t.TempDir fixture) so these tests can exercise the real
// data/buildings.json catalogue FEAT-010 delivers, per
// data.catalogue.md's AC-1..AC-16 (most of which are checks against
// the real file, not a synthetic fixture).
func realDataDir(t *testing.T) string {
	t.Helper()
	dir, err := ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	return dir
}

// --- AC-1: real file loads and validates ----------------------------------

func TestBuildings_RealCatalogue_LoadsAndValidates(t *testing.T) {
	dir := realDataDir(t)
	b, err := LoadBuildingsCatalogue(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildingsCatalogue(real data/buildings.json): %v", err)
	}
	if len(b.Entries) == 0 {
		t.Fatal("expected a non-empty catalogue")
	}
}

// --- AC-2/AC-3: every Part IV section and every supplement is present ----
// (GR#15: the "expected" section list is the documented Part IV/
// Supplement section-code taxonomy from the spec's own section headers
// — not a hardcoded entry COUNT. The check below asserts coverage
// [>=1 entry per known section/supplement], derived by counting the
// loaded file's own entries, never a fixed magic total.)

var partIVSections = []string{
	"R", "E", "W", "H", "ED", "F-P", "G", "PK", "L", "T", "PT", "HS", "C-I", "LM", "CM-WF",
}

func TestBuildings_RealCatalogue_AllPartIVSectionsPresent(t *testing.T) {
	dir := realDataDir(t)
	b, err := LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}
	counts := map[string]int{}
	for _, e := range b.Entries {
		if e.Supplement == "" {
			counts[e.CatalogueSection]++
		}
	}
	for _, sec := range partIVSections {
		if counts[sec] < 1 {
			t.Errorf("Part IV section %q has %d base entries, want >= 1", sec, counts[sec])
		}
	}
}

func TestBuildings_RealCatalogue_AllSupplementsPresent(t *testing.T) {
	dir := realDataDir(t)
	b, err := LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}
	counts := map[string]int{}
	for _, e := range b.Entries {
		if e.Supplement != "" {
			counts[e.Supplement]++
		}
	}
	for _, s := range []string{"S1", "S2", "S3"} {
		if counts[s] < 1 {
			t.Errorf("supplement %q has %d entries, want >= 1", s, counts[s])
		}
	}
}

// --- AC-5: spot-check representative entries against the spec's literal
// numbers (transcribed from docs/METROPOLIS-MASTER-v2.1.md, never
// invented — GR#15).

func findEntry(t *testing.T, entries []BuildingEntry, id string) BuildingEntry {
	t.Helper()
	for _, e := range entries {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("entry %q not found in catalogue", id)
	return BuildingEntry{}
}

func TestBuildings_RealCatalogue_SpotCheckSpecLiterals(t *testing.T) {
	dir := realDataDir(t)
	b, err := LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}

	cases := []struct {
		id, section, costRaw, capacityRaw string
	}{
		{"residential_street", "R", "250k", ""},
		{"nuclear_station_dungeness_ii_and_a_half", "E", "900M", "1,200 MW"},
		{"well", "W", "30k", "20 m3/d"},
		{"small_hospital", "H", "12M", "120 beds"},
		{"primary_school", "ED", "1.2M", "240"},
		{"toxic_waste_processing_plant", "SUP2", "", ""},
	}
	for _, c := range cases {
		e := findEntry(t, b.Entries, c.id)
		if e.CatalogueSection != c.section {
			t.Errorf("%s: catalogueSection = %q, want %q", c.id, e.CatalogueSection, c.section)
		}
		if c.costRaw != "" && e.CostRaw != c.costRaw {
			t.Errorf("%s: costRaw = %q, want %q", c.id, e.CostRaw, c.costRaw)
		}
		if c.capacityRaw != "" && e.CapacityRaw != c.capacityRaw {
			t.Errorf("%s: capacityRaw = %q, want %q", c.id, e.CapacityRaw, c.capacityRaw)
		}
	}

	// Toxic waste processing plant: spec states "max blight class" explicitly (§36 cross-ref).
	toxic := findEntry(t, b.Entries, "toxic_waste_processing_plant")
	if toxic.BlightClass != "max" {
		t.Errorf("toxic_waste_processing_plant.blightClass = %q, want %q", toxic.BlightClass, "max")
	}
}

// --- AC-6: no raw utility number ever appears inline -----------------------

func TestBuildings_RealCatalogue_NoRawUtilityFields(t *testing.T) {
	dir := realDataDir(t)
	raw, err := os.ReadFile(filepath.Join(dir, FileBuildings))
	if err != nil {
		t.Fatalf("read buildings.json: %v", err)
	}
	forbidden := []string{`"waterL"`, `"elecKwh"`, `"elecKWh"`, `"gasKwh"`, `"gasKWh"`, `"wasteKg"`}
	for _, f := range forbidden {
		if strings.Contains(string(raw), f) {
			t.Errorf("buildings.json contains forbidden raw-utility field %s (§17: utility numbers live only in consumption.json)", f)
		}
	}
	if !strings.Contains(string(raw), `"consumptionRef"`) {
		t.Error("buildings.json has no consumptionRef usage at all — expected at least one utility-relevant entry")
	}
}

// --- AC-7: blight class enum, spot-checked against spec's qualitative
// language for the entries it explicitly names.

func TestBuildings_RealCatalogue_BlightClassAssignments(t *testing.T) {
	dir := realDataDir(t)
	b, err := LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}
	refinery := findEntry(t, b.Entries, "refinery")
	if refinery.BlightClass == "none" {
		t.Error("refinery.blightClass should not be \"none\" (§50: \"top blight class\")")
	}
	heavyIndustry := findEntry(t, b.Entries, "heavy_industry_estate")
	if heavyIndustry.BlightClass == "none" {
		t.Error("heavy_industry_estate.blightClass should not be \"none\" (spec: \"pollution radius\")")
	}
}

// --- AC-8: every HS entry has a non-empty appealProfile --------------------

func TestBuildings_RealCatalogue_EveryHSEntryHasAppealProfile(t *testing.T) {
	dir := realDataDir(t)
	b, err := LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}
	found := 0
	for _, e := range b.Entries {
		if e.CatalogueSection != "HS" {
			continue
		}
		found++
		if len(e.AppealProfile) == 0 {
			t.Errorf("HS entry %q has empty appealProfile", e.ID)
		}
	}
	if found == 0 {
		t.Fatal("no HS entries found in catalogue")
	}
}

// --- AC-9: every §23 sourcePack group has at least one tagged entry -------

func TestBuildings_RealCatalogue_SourcePackCoverage(t *testing.T) {
	dir := realDataDir(t)
	b, err := LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}
	counts := map[string]int{}
	for _, e := range b.Entries {
		if e.SourcePack != "" {
			counts[e.SourcePack]++
		}
	}
	for pack := range knownSourcePacks {
		if counts[pack] < 1 {
			t.Errorf("§23 sourcePack %q has %d tagged entries, want >= 1", pack, counts[pack])
		}
	}
}

// --- AC-10/AC-10b: uniqueness enforced by the loader's public path, not
// merely by a test assertion after the fact.

func TestBuildings_RealCatalogue_NoDuplicateIDs(t *testing.T) {
	dir := realDataDir(t)
	b, err := LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}
	seen := make(map[string]bool, len(b.Entries))
	for _, e := range b.Entries {
		if seen[e.ID] {
			t.Fatalf("duplicate id %q in real catalogue", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestLoadBuildings_DuplicateIDRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileBuildings, `{"version": 1, "entries": [
		{"id": "pub", "name": "Pub", "catalogueSection": "L", "unlock": {"raw": "M2", "milestone": "M2"}, "blightClass": "none"},
		{"id": "pub", "name": "Pub (duplicate)", "catalogueSection": "L", "unlock": {"raw": "M2", "milestone": "M2"}, "blightClass": "none"}
	]}`)
	_, err := LoadBuildings(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "duplicate id")
}

// --- AC-11: registry-sourced errors per failure class ----------------------

func TestLoadBuildings_MissingRequiredFieldRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileBuildings, `{"version": 1, "entries": [
		{"id": "pub", "catalogueSection": "L", "unlock": {"raw": "M2"}, "blightClass": "none"}
	]}`)
	_, err := LoadBuildings(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, ".name")
}

func TestLoadBuildings_UnknownBlightClassRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileBuildings, `{"version": 1, "entries": [
		{"id": "pub", "name": "Pub", "catalogueSection": "L", "unlock": {"raw": "M2"}, "blightClass": "extreme"}
	]}`)
	_, err := LoadBuildings(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "blightClass")
}

func TestLoadBuildings_InvalidIDFormatRejected(t *testing.T) {
	dir := t.TempDir()
	// Leading digit and an embedded path separator — both outside
	// buildingIDPattern's domain (AC-12b): rejected, never normalised.
	writeFixture(t, dir, FileBuildings, `{"version": 1, "entries": [
		{"id": "../etc/passwd", "name": "Hostile", "catalogueSection": "L", "unlock": {"raw": "M2"}, "blightClass": "none"}
	]}`)
	_, err := LoadBuildings(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, ".id")
}

func TestLoadBuildings_DanglingConsumptionRefRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileBuildings, `{"version": 1, "entries": [
		{"id": "mystery_building", "name": "Mystery building", "catalogueSection": "L",
		 "unlock": {"raw": "M2"}, "blightClass": "none", "consumptionRef": "doesNotExist"}
	]}`)
	writeFixture(t, dir, FileConsumption, `{
		"version": 1,
		"residential": {"waterLitresPerPersonPerDay": 145, "electricityKWhPerPersonPerDay": 3.5,
			"gasKWhPerPersonPerDay": 13, "foodStaplesKgPerPersonPerDay": 1.4,
			"foodFreshKgPerPersonPerDay": 0.7, "householdWasteKgPerPersonPerDay": 1.1,
			"wastewaterFractionOfWater": 0.95},
		"classes": {"school": {"unit": "pupil", "waterL": 18, "elecKWh": 1.5, "gasKWh": 3.0, "wasteKg": 0.2}}
	}`)
	_, err := LoadBuildingsCatalogue(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeBuildingDanglingConsumptionRef, "doesNotExist")
}

func TestLoadBuildingsCatalogue_ConsumptionRefResolves(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileBuildings, `{"version": 1, "entries": [
		{"id": "primary_school", "name": "Primary school", "catalogueSection": "ED",
		 "unlock": {"raw": "M3+DP", "milestone": "M3", "developmentPoint": true},
		 "consumptionRef": "school", "blightClass": "none"}
	]}`)
	writeFixture(t, dir, FileConsumption, `{
		"version": 1,
		"residential": {"waterLitresPerPersonPerDay": 145, "electricityKWhPerPersonPerDay": 3.5,
			"gasKWhPerPersonPerDay": 13, "foodStaplesKgPerPersonPerDay": 1.4,
			"foodFreshKgPerPersonPerDay": 0.7, "householdWasteKgPerPersonPerDay": 1.1,
			"wastewaterFractionOfWater": 0.95},
		"classes": {"school": {"unit": "pupil", "waterL": 18, "elecKWh": 1.5, "gasKWh": 3.0, "wasteKg": 0.2}}
	}`)
	b, err := LoadBuildingsCatalogue(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildingsCatalogue: %v", err)
	}
	if len(b.Entries) != 1 {
		t.Fatalf("entries = %+v", b.Entries)
	}
}

// --- AC-4: unlock.milestone always parses to a known M1-M13 tier when set

func TestBuildings_RealCatalogue_UnlockMilestonesAreValid(t *testing.T) {
	dir := realDataDir(t)
	b, err := LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}
	re := regexp.MustCompile(`^M(1[0-3]|[1-9])$`)
	for _, e := range b.Entries {
		if e.Unlock.Milestone != "" && !re.MatchString(e.Unlock.Milestone) {
			t.Errorf("%s: unlock.milestone = %q is not a valid M1-M13 tier", e.ID, e.Unlock.Milestone)
		}
	}
	// A concrete +DP flag example, transcribed directly from the spec
	// (Primary school: "M3+DP").
	school := findEntry(t, b.Entries, "primary_school")
	if school.Unlock.Milestone != "M3" || !school.Unlock.DevelopmentPoint {
		t.Errorf("primary_school.unlock = %+v, want milestone M3 with developmentPoint=true", school.Unlock)
	}
}

// --- AC-13/GR#21: repeated load is deep-equal (determinism) ---------------

func TestLoadBuildings_RepeatedLoadDeepEqual(t *testing.T) {
	dir := realDataDir(t)
	b1, err := LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	b2, err := LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !reflect.DeepEqual(b1, b2) {
		t.Error("repeated LoadBuildings of the same file produced non-equal structs")
	}
}

// --- AC-14: no timestamp / non-reproducible value inside the catalogue ----

func TestBuildings_RealCatalogue_NoTimestamps(t *testing.T) {
	dir := realDataDir(t)
	raw, err := os.ReadFile(filepath.Join(dir, FileBuildings))
	if err != nil {
		t.Fatalf("read buildings.json: %v", err)
	}
	iso := regexp.MustCompile(`"\d{4}-\d{2}-\d{2}T`)
	if iso.Match(raw) {
		t.Error("buildings.json contains an ISO-timestamp-shaped value inside an entry body")
	}
}

// --- AC-15: top-level $comment/meta citing Part IV -------------------------

func TestBuildings_RealCatalogue_HasTopLevelComment(t *testing.T) {
	dir := realDataDir(t)
	raw, err := os.ReadFile(filepath.Join(dir, FileBuildings))
	if err != nil {
		t.Fatalf("read buildings.json: %v", err)
	}
	if !strings.Contains(string(raw), `"$comment"`) {
		t.Fatal(`buildings.json has no top-level "$comment" field`)
	}
	if !strings.Contains(string(raw), "Part IV") && !strings.Contains(string(raw), "catalogue") {
		t.Error(`buildings.json's "$comment" does not mention "Part IV" or "catalogue"`)
	}
}

// sanity: keep errs import used even if future edits trim other usages.
var _ = errs.NewCorrelationID
