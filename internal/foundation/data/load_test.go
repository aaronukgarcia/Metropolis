package data

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

func testCorrelationID() string {
	return errs.NewCorrelationID()
}

// validNamingCorpusFixture is a minimal schema-valid naming_corpus.json
// for the shared happy-path / aggregate tests (all nine non-numbered
// road classes present, since NamingCorpus.Validate rejects a class
// with no suffix — data.modes-naming.md AC-10).
func validNamingCorpusFixture() string {
	return `{"version":1,"categories":` + validCategoriesFixture + `}`
}

// validExternalWorldFixture is a minimal schema-valid external_world.json
// for the shared happy-path / aggregate tests (a single pool whose
// capacity curve is non-decreasing, wage positive, and transport gated
// to a valid tier — the invariants ExternalWorld.Validate enforces).
func validExternalWorldFixture() string {
	return `{"version":1,"profiles":` + validProfilesFixture + `}`
}

// validRoadSuffixesFixture is the full valid "roadSuffixes" object body
// (all nine non-numbered road classes — NamingCorpus.Validate rejects a
// class with no suffix). Shared by validNamingCorpusFixture and the
// SEC-056 case-variant-duplicate tests.
const validRoadSuffixesFixture = `{"alley":["Close"],"gravel":["Lane"],"residential_street":["Road"],"two_lane":["Street"],"one_way_pairs":["Street"],"avenue_2_plus_2":["Avenue"],"bus_lane_variant":["Way"],"tram_track_variant":["Drive"],"dual_carriageway":["Road"]}`

// validCategoriesFixture is the full valid "categories" object body,
// shared by validNamingCorpusFixture and the SEC-056 tests.
const validCategoriesFixture = `{"roadPlaceNames":["Cheriton","Seabrook"],"roadSuffixes":` + validRoadSuffixesFixture + `}`

// validProfilesFixture is the minimal schema-valid "profiles" array body,
// shared by validExternalWorldFixture and the SEC-056 tests.
const validProfilesFixture = `[{"id":"london","name":"London","capacityByEra":[{"era":1,"capacity":500},{"era":2,"capacity":550}],"wageMicropounds":2900000000,"transportRequirement":[{"channel":"motorway","availableFromTier":1}]}]`

// validUnlockTreesFixture is a minimal schema-valid unlock_trees.json
// for the shared happy-path / aggregate tests. UnlockTrees.Validate
// derives its expected category count from meta.categories and requires
// every tree to cover all thirteen tiers, so the fixture is built
// programmatically from the helpers in unlock_trees_test.go rather than
// inlined (a 12-category × 13-tier literal would be hundreds of lines).
func validUnlockTreesFixture() string {
	return singleTreeFixture(validRoadNodes()...)
}

// assertPlaceholderCode checks that err is a registry-sourced *errs.E
// constructed against wantCode, and resolved as a real registry entry
// (data/errors.json — BUG-008 closed the gap where this package's
// codes were raised in source but not yet registered; see errors.go's
// doc comment). wantSubstr, if non-empty, must appear in err.Error()
// (which includes the wrapped cause via Wrap), proving the specific
// field/rule detail survived into the rendered message/context.
func assertPlaceholderCode(t *testing.T, err error, wantCode, wantSubstr string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != wantCode {
		t.Errorf("e.Code = %s, want %s", e.Code, wantCode)
	}
	if wantSubstr != "" && !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("err.Error() = %q, want it to contain %q", err.Error(), wantSubstr)
	}
}

// --- happy-path loads, one per file -------------------------------------

func TestLoadConsumption_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileConsumption, `{
		"version": 1,
		"residential": {"waterLitresPerPersonPerDay": 145, "electricityKWhPerPersonPerDay": 3.5,
			"gasKWhPerPersonPerDay": 13, "foodStaplesKgPerPersonPerDay": 1.4,
			"foodFreshKgPerPersonPerDay": 0.7, "householdWasteKgPerPersonPerDay": 1.1,
			"wastewaterFractionOfWater": 0.95},
		"classes": {"school": {"unit": "pupil", "waterL": 18, "elecKWh": 1.5, "gasKWh": 3.0, "wasteKg": 0.2}}
	}`)

	c, err := LoadConsumption(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadConsumption: %v", err)
	}
	if c.Residential.WaterLitresPerPersonPerDay != 145 {
		t.Errorf("water = %v, want 145", c.Residential.WaterLitresPerPersonPerDay)
	}
	if c.Classes["school"].Unit != "pupil" {
		t.Errorf("school unit = %q", c.Classes["school"].Unit)
	}
}

func TestLoadSeasonal_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileSeasonal, `{
		"version": 1,
		"curves": {"gasSeasonal": {"multipliers": [2.2,1,1,1,1,1,0.2,1,1,1,1,1]}}
	}`)

	s, err := LoadSeasonal(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadSeasonal: %v", err)
	}
	if len(s.Curves["gasSeasonal"].Multipliers) != 12 {
		t.Fatalf("expected 12 multipliers, got %d", len(s.Curves["gasSeasonal"].Multipliers))
	}
	if s.Curves["gasSeasonal"].Multipliers[0] != 2.2 {
		t.Errorf("January multiplier = %v, want 2.2", s.Curves["gasSeasonal"].Multipliers[0])
	}
}

func TestLoadModes_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileModes, `{"version": 1, "entries": [{"key": "car"}]}`)
	m, err := LoadModes(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadModes: %v", err)
	}
	if len(m.Entries) != 1 || m.Entries[0].Key != "car" {
		t.Errorf("entries = %+v", m.Entries)
	}
}

func TestLoadBuildings_HappyPath(t *testing.T) {
	// Fixture updated for FEAT-010/data.catalogue's full BuildingEntry
	// schema (buildings.go replaced the {"key": ...} skeleton this test
	// used to exercise, per types.go's own former TODO pointing at this
	// item). Logged as ASM (see FEAT-010's report): touching this
	// shared-file test is in scope because the skeleton it tested no
	// longer exists.
	dir := t.TempDir()
	writeFixture(t, dir, FileBuildings, `{"version": 1, "entries": [{
		"id": "primary_school", "name": "Primary school", "catalogueSection": "ED",
		"unlock": {"raw": "M3+DP", "milestone": "M3", "developmentPoint": true},
		"costRaw": "1.2M", "capacityRaw": "240", "consumptionRef": "school", "blightClass": "none"
	}]}`)
	b, err := LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}
	if len(b.Entries) != 1 || b.Entries[0].ID != "primary_school" {
		t.Errorf("entries = %+v", b.Entries)
	}
}

func TestLoadUnlockTrees_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileUnlockTrees, validUnlockTreesFixture())
	u, err := LoadUnlockTrees(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadUnlockTrees: %v", err)
	}
	if len(u.Trees) != 1 || u.Trees[0].ID != "roads" {
		t.Errorf("trees = %+v", u.Trees)
	}
	if len(u.Trees[0].Nodes) != 13 {
		t.Errorf("nodes = %d, want 13", len(u.Trees[0].Nodes))
	}
}

func TestLoadNamingCorpus_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileNamingCorpus, validNamingCorpusFixture())
	n, err := LoadNamingCorpus(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadNamingCorpus: %v", err)
	}
	if len(n.Categories.RoadPlaceNames) != 2 {
		t.Errorf("roadPlaceNames = %+v", n.Categories.RoadPlaceNames)
	}
	if len(n.Categories.RoadSuffixes.Alley) != 1 {
		t.Errorf("roadSuffixes.alley = %+v", n.Categories.RoadSuffixes.Alley)
	}
}

func TestLoadExternalWorld_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileExternalWorld, validExternalWorldFixture())
	e, err := LoadExternalWorld(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadExternalWorld: %v", err)
	}
	if len(e.Profiles) != 1 || e.Profiles[0].ID != "london" {
		t.Errorf("profiles = %+v", e.Profiles)
	}
	if e.Profiles[0].WageMicropounds != 2900000000 {
		t.Errorf("wageMicropounds = %d, want 2900000000", e.Profiles[0].WageMicropounds)
	}
}

func TestLoadPolicies_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FilePolicies, `{"version": 1, "entries": [{"key": "cyclePriorityNetwork"}]}`)
	p, err := LoadPolicies(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadPolicies: %v", err)
	}
	if len(p.Entries) != 1 {
		t.Errorf("entries = %+v", p.Entries)
	}
}

// --- malformed JSON (AC-9) ------------------------------------------------

func TestLoad_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileConsumption, `{not valid json`)

	_, err := LoadConsumption(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeMalformedJSON, "")
}

// TestLoad_MET_F602_CauseSubstituted is BUG-099's regression test for
// the shared foundation/data.Load path: MET-F602's registered template
// ("...is not well-formed JSON: {cause}") must have its {cause}
// placeholder substituted with the real json.Unmarshal error text, not
// left as the literal unsubstituted string "{cause}" in the
// GR#1-visible message (the same class of defect BUG-099 fixed for
// engine.market's MET-E600).
func TestLoad_MET_F602_CauseSubstituted(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileConsumption, `{not valid json`)

	_, err := LoadConsumption(dir, testCorrelationID())
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != CodeMalformedJSON {
		t.Fatalf("e.Code = %s, want %s", e.Code, CodeMalformedJSON)
	}
	if strings.Contains(e.Msg, "{cause}") {
		t.Errorf("e.Msg = %q contains the literal unsubstituted placeholder %q", e.Msg, "{cause}")
	}
	if !strings.Contains(e.Msg, "invalid character") {
		t.Errorf("e.Msg = %q, want it to contain the real json decode cause text", e.Msg)
	}
}

// --- missing file (AC-8) --------------------------------------------------

func TestLoad_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadConsumption(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeFileNotFound, "")
}

// --- missing version field (AC-2 / AC-10) ---------------------------------

func TestLoad_MissingVersion(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileModes, `{"entries": []}`)

	_, err := LoadModes(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeMissingVersion, "field version")
}

func TestLoad_ZeroVersionIsMissing(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FilePolicies, `{"version": 0, "entries": []}`)

	_, err := LoadPolicies(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeMissingVersion, "field version")
}

// --- schema violations naming the offending field (AC-2 / AC-10) ---------

func TestLoadConsumption_NegativeCoefficientRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileConsumption, `{
		"version": 1,
		"residential": {"waterLitresPerPersonPerDay": -5, "electricityKWhPerPersonPerDay": 3.5,
			"gasKWhPerPersonPerDay": 13, "foodStaplesKgPerPersonPerDay": 1.4,
			"foodFreshKgPerPersonPerDay": 0.7, "householdWasteKgPerPersonPerDay": 1.1,
			"wastewaterFractionOfWater": 0.95},
		"classes": {}
	}`)

	_, err := LoadConsumption(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "field residential.waterLitresPerPersonPerDay")
}

func TestLoadConsumption_MissingClassUnitRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileConsumption, `{
		"version": 1,
		"residential": {"waterLitresPerPersonPerDay": 145, "electricityKWhPerPersonPerDay": 3.5,
			"gasKWhPerPersonPerDay": 13, "foodStaplesKgPerPersonPerDay": 1.4,
			"foodFreshKgPerPersonPerDay": 0.7, "householdWasteKgPerPersonPerDay": 1.1,
			"wastewaterFractionOfWater": 0.95},
		"classes": {"school": {"unit": "", "waterL": 18, "elecKWh": 1.5, "gasKWh": 3.0, "wasteKg": 0.2}}
	}`)

	_, err := LoadConsumption(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "classes[school].unit")
}

func TestLoadConsumption_WrongTypeRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileConsumption, `{
		"version": 1,
		"residential": {"waterLitresPerPersonPerDay": "a lot", "electricityKWhPerPersonPerDay": 3.5,
			"gasKWhPerPersonPerDay": 13, "foodStaplesKgPerPersonPerDay": 1.4,
			"foodFreshKgPerPersonPerDay": 0.7, "householdWasteKgPerPersonPerDay": 1.1,
			"wastewaterFractionOfWater": 0.95},
		"classes": {}
	}`)

	_, err := LoadConsumption(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "waterLitresPerPersonPerDay")
}

func TestLoadSeasonal_WrongCurveLengthRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileSeasonal, `{"version": 1, "curves": {"bad": {"multipliers": [1,1,1]}}}`)

	_, err := LoadSeasonal(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "curves[bad].multipliers")
}

// --- duplicate key detection (BUG-060) -------------------------------------

// TestLoadSeasonal_DuplicateCurveKeyRejected is BUG-060's regression test:
// a hand-edited seasonal.json with the same curve key ("gasSeasonal")
// appearing twice inside "curves" must now fail Load with a clear,
// registry-sourced error naming the duplicate, instead of silently
// keeping the second body (standard encoding/json map-decode behaviour)
// with no signal that the first was ever discarded.
func TestLoadSeasonal_DuplicateCurveKeyRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileSeasonal, `{
		"version": 1,
		"curves": {
			"gasSeasonal": {"multipliers": [2.2,1,1,1,1,1,0.2,1,1,1,1,1]},
			"gasSeasonal": {"multipliers": [9,9,9,9,9,9,9,9,9,9,9,9]}
		}
	}`)

	_, err := LoadSeasonal(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeDuplicateKey, "curves.gasSeasonal")
}

// TestLoadSeasonal_NoFalsePositiveOnDistinctKeys confirms the duplicate
// check does not misfire on a normal, duplicate-free seasonal.json with
// multiple distinct curves -- the same shape a real §17.1 file uses
// (gas/water/electricity seasonal curves side by side).
func TestLoadSeasonal_NoFalsePositiveOnDistinctKeys(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileSeasonal, `{
		"version": 1,
		"curves": {
			"gasSeasonal": {"multipliers": [2.2,1,1,1,1,1,0.2,1,1,1,1,1]},
			"waterSummerPeak": {"multipliers": [1,1,1,1,1,1.25,1.25,1.25,1,1,1,1]},
			"electricityWinter": {"multipliers": [1.15,1.15,1,1,1,1,1,1,1,1,1,1.15]}
		}
	}`)

	s, err := LoadSeasonal(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadSeasonal: %v", err)
	}
	if len(s.Curves) != 3 {
		t.Errorf("len(Curves) = %d, want 3", len(s.Curves))
	}
	if s.Curves["gasSeasonal"].Multipliers[0] != 2.2 {
		t.Errorf("gasSeasonal[0] = %v, want 2.2", s.Curves["gasSeasonal"].Multipliers[0])
	}
}

// TestLoadSeasonal_DuplicateKeyThenLaterSyntaxErrorReportsMalformed is
// BUG-060 round 2's regression test: a file with BOTH a genuine
// duplicate key (earlier in the byte stream) AND a later syntax error
// (a trailing comma, here) must report CodeMalformedJSON, not
// CodeDuplicateKey. Round 1's walkForDuplicateKey returned as soon as it
// found the duplicate, without continuing to scan the rest of the
// document, so the later syntax error was never discovered and the
// file's real breakage was masked until a second run (after the
// duplicate was fixed) surfaced it. Load now runs json.Unmarshal before
// the duplicate-key walk, so any genuine syntax error anywhere in the
// document -- including after an earlier duplicate key -- is reported
// first.
func TestLoadSeasonal_DuplicateKeyThenLaterSyntaxErrorReportsMalformed(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileSeasonal, `{
		"version": 1,
		"curves": {
			"gasSeasonal": {"multipliers": [2.2,1,1,1,1,1,0.2,1,1,1,1,1]},
			"gasSeasonal": {"multipliers": [9,9,9,9,9,9,9,9,9,9,9,9]},
			"waterSummerPeak": {"multipliers": [1,1,1,1,1,1.25,1.25,1.25,1,1,1,],}
		}
	}`)

	_, err := LoadSeasonal(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeMalformedJSON, "")
}

// TestLoadSeasonal_SyntaxErrorBeforeDuplicateStillReportsMalformed
// confirms the reverse ordering (syntax error earlier in the byte
// stream than the duplicate key) keeps reporting CodeMalformedJSON, as
// it already did before round 2 -- this scenario was never broken, only
// the opposite ordering was.
func TestLoadSeasonal_SyntaxErrorBeforeDuplicateStillReportsMalformed(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileSeasonal, `{
		"version": 1,
		"curves": {
			"waterSummerPeak": {"multipliers": [1,1,1,1,1,1.25,1.25,1.25,1,1,1,],}
			"gasSeasonal": {"multipliers": [2.2,1,1,1,1,1,0.2,1,1,1,1,1]},
			"gasSeasonal": {"multipliers": [9,9,9,9,9,9,9,9,9,9,9,9]}
		}
	}`)

	_, err := LoadSeasonal(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeMalformedJSON, "")
}

// TestFindDuplicateKey_NestedPath confirms the underlying walker reports
// a dotted path for a duplicate nested inside an object (not just a
// top-level duplicate), matching the "curves.<name>" shape the seasonal
// tests above rely on.
func TestFindDuplicateKey_NestedPath(t *testing.T) {
	path, found, err := findDuplicateKey([]byte(`{"a":{"b":1,"b":2}}`))
	if err != nil {
		t.Fatalf("findDuplicateKey: %v", err)
	}
	if !found {
		t.Fatal("expected a duplicate to be found")
	}
	if path != "a.b" {
		t.Errorf("path = %q, want %q", path, "a.b")
	}
}

// TestFindDuplicateKey_NoDuplicate confirms a duplicate-free document
// reports found=false.
func TestFindDuplicateKey_NoDuplicate(t *testing.T) {
	_, found, err := findDuplicateKey([]byte(`{"a":{"b":1,"c":2},"d":[1,2,3]}`))
	if err != nil {
		t.Fatalf("findDuplicateKey: %v", err)
	}
	if found {
		t.Error("expected no duplicate to be found")
	}
}

// --- SEC-056: case-variant duplicate keys ----------------------------------

// TestFindDuplicateKey_CaseVariant is SEC-056's walker-level regression
// test: two keys inside the same object that differ only by case ("b" and
// "B") must be reported as duplicates, because encoding/json matches
// struct field names case-insensitively and silently last-write-wins
// across them.
func TestFindDuplicateKey_CaseVariant(t *testing.T) {
	path, found, err := findDuplicateKey([]byte(`{"a":{"b":1,"B":2}}`))
	if err != nil {
		t.Fatalf("findDuplicateKey: %v", err)
	}
	if !found {
		t.Fatal("expected a case-variant duplicate to be found")
	}
	if path != "a.B" {
		t.Errorf("path = %q, want %q", path, "a.B")
	}
}

// TestFindDuplicateKey_UnicodeFold is SEC-056's class-fix proof at the
// walker level: encoding/json's foldName/equalFoldRight fold ſ (U+017F
// long s) ↔ s/S, so "entries"/"entrieſ" (and "Entries"/"entrieſ") are the
// SAME field and must be reported as a duplicate. strings.EqualFold
// implements exactly this Unicode simple fold; strings.ToLower does not
// (it leaves the already-lowercase long-s untouched, so a ToLower-keyed
// map would see these as distinct and the duplicate would slip through).
func TestFindDuplicateKey_UnicodeFold(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"longS_vs_s", `{"entries":1,"entrieſ":2}`, "entrieſ"},
		{"longS_vs_S", `{"Entries":1,"entrieſ":2}`, "entrieſ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, found, err := findDuplicateKey([]byte(tc.json))
			if err != nil {
				t.Fatalf("findDuplicateKey: %v", err)
			}
			if !found {
				t.Fatal("expected a Unicode-fold duplicate to be found")
			}
			if path != tc.want {
				t.Errorf("path = %q, want %q", path, tc.want)
			}
		})
	}
}

// assertDuplicateKeyNaming checks err is a MET-F609 (CodeDuplicateKey)
// error and that its rendered message names the offending field, matched
// case-insensitively (the walker reports the second occurrence's exact
// spelling, which is the case-variant one).
func assertDuplicateKeyNaming(t *testing.T, err error, field string) {
	t.Helper()
	assertPlaceholderCode(t, err, CodeDuplicateKey, "")
	if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(field)) {
		t.Errorf("err.Error() = %q, want it to name the %q field", err.Error(), field)
	}
}

// TestLoadNamingCorpus_CaseVariantDuplicateCategoriesRejected proves a
// naming_corpus.json carrying both "categories" and "Categories" (the
// same struct field to encoding/json) is rejected with MET-F609, not
// silently last-write-wins'd.
func TestLoadNamingCorpus_CaseVariantDuplicateCategoriesRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileNamingCorpus,
		`{"version":1,"categories":`+validCategoriesFixture+`,"Categories":`+validCategoriesFixture+`}`)

	_, err := LoadNamingCorpus(dir, testCorrelationID())
	assertDuplicateKeyNaming(t, err, "categories")
}

// TestLoadNamingCorpus_CaseVariantDuplicateRoadSuffixesRejected proves the
// same for a nested required field: "roadSuffixes" vs "RoadSuffixes".
func TestLoadNamingCorpus_CaseVariantDuplicateRoadSuffixesRejected(t *testing.T) {
	dir := t.TempDir()
	categoriesWithDup := `{"roadPlaceNames":["Cheriton","Seabrook"],"roadSuffixes":` + validRoadSuffixesFixture + `,"RoadSuffixes":` + validRoadSuffixesFixture + `}`
	writeFixture(t, dir, FileNamingCorpus, `{"version":1,"categories":`+categoriesWithDup+`}`)

	_, err := LoadNamingCorpus(dir, testCorrelationID())
	assertDuplicateKeyNaming(t, err, "roadSuffixes")
}

// TestLoadExternalWorld_CaseVariantDuplicateProfilesRejected proves the
// same for the external_world.json required field "profiles" vs
// "Profiles".
func TestLoadExternalWorld_CaseVariantDuplicateProfilesRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileExternalWorld,
		`{"version":1,"profiles":`+validProfilesFixture+`,"Profiles":`+validProfilesFixture+`}`)

	_, err := LoadExternalWorld(dir, testCorrelationID())
	assertDuplicateKeyNaming(t, err, "profiles")
}

// TestLoadModes_LongSDuplicateEntriesRejected is SEC-056's end-to-end
// proof: a modes.json carrying both "entries" and "entrieſ" (long-s
// U+017F) -- which encoding/json folds to the SAME struct field but a
// strings.ToLower map does NOT -- must be rejected with MET-F609, not
// silently last-write-wins'd. Both halves decode fine on their own (so a
// duplicate-free reading would reach Validate and pass), which is what
// makes the case-fold gap a silent last-write-wins rather than a loud
// type error.
func TestLoadModes_LongSDuplicateEntriesRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileModes,
		`{"version":1,"entries":[{"key":"car"}],"entrieſ":[{"key":"bus"}]}`)

	_, err := LoadModes(dir, testCorrelationID())
	assertDuplicateKeyNaming(t, err, "entrieſ")
}

// --- LoadAll (AC-3) --------------------------------------------------------

func seedAllFixtures(t *testing.T, dir string) {
	t.Helper()
	writeFixture(t, dir, FileConsumption, `{"version":1,"residential":{"waterLitresPerPersonPerDay":145,"electricityKWhPerPersonPerDay":3.5,"gasKWhPerPersonPerDay":13,"foodStaplesKgPerPersonPerDay":1.4,"foodFreshKgPerPersonPerDay":0.7,"householdWasteKgPerPersonPerDay":1.1,"wastewaterFractionOfWater":0.95},"classes":{}}`)
	writeFixture(t, dir, FileModes, `{"version":1,"entries":[]}`)
	writeFixture(t, dir, FileBuildings, `{"version":1,"entries":[]}`)
	writeFixture(t, dir, FileUnlockTrees, validUnlockTreesFixture())
	writeFixture(t, dir, FileNamingCorpus, validNamingCorpusFixture())
	writeFixture(t, dir, FileSeasonal, `{"version":1,"curves":{}}`)
	writeFixture(t, dir, FileExternalWorld, validExternalWorldFixture())
	writeFixture(t, dir, FilePolicies, `{"version":1,"entries":[]}`)
}

func TestLoadAll_HappyPath(t *testing.T) {
	dir := t.TempDir()
	seedAllFixtures(t, dir)

	cfg, err := LoadAll(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if cfg.Consumption.Version != 1 {
		t.Error("Consumption not populated")
	}
	if cfg.Modes.Version != 1 {
		t.Error("Modes not populated")
	}
	if cfg.Buildings.Version != 1 {
		t.Error("Buildings not populated")
	}
	if cfg.UnlockTrees.Version != 1 {
		t.Error("UnlockTrees not populated")
	}
	if cfg.NamingCorpus.Version != 1 {
		t.Error("NamingCorpus not populated")
	}
	if cfg.Seasonal.Version != 1 {
		t.Error("Seasonal not populated")
	}
	if cfg.ExternalWorld.Version != 1 {
		t.Error("ExternalWorld not populated")
	}
	if cfg.Policies.Version != 1 {
		t.Error("Policies not populated")
	}
}

func TestLoadAll_PropagatesPerFileError(t *testing.T) {
	dir := t.TempDir()
	seedAllFixtures(t, dir)
	writeFixture(t, dir, FileBuildings, `{not valid json`)

	_, err := LoadAll(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeMalformedJSON, "")
}

// --- determinism: repeated load is deep-equal (AC-12/GR#21) --------------

func TestLoadConsumption_RepeatedLoadDeepEqual(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileConsumption, `{
		"version": 1,
		"residential": {"waterLitresPerPersonPerDay": 145, "electricityKWhPerPersonPerDay": 3.5,
			"gasKWhPerPersonPerDay": 13, "foodStaplesKgPerPersonPerDay": 1.4,
			"foodFreshKgPerPersonPerDay": 0.7, "householdWasteKgPerPersonPerDay": 1.1,
			"wastewaterFractionOfWater": 0.95},
		"classes": {"school": {"unit": "pupil", "waterL": 18, "elecKWh": 1.5, "gasKWh": 3.0, "wasteKg": 0.2},
			"hospital": {"unit": "bed", "waterL": 400, "elecKWh": 28, "gasKWh": 30, "wasteKg": 3.2}}
	}`)

	c1, err := LoadConsumption(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	c2, err := LoadConsumption(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !reflect.DeepEqual(c1, c2) {
		t.Errorf("loads not deep-equal:\n%+v\n%+v", c1, c2)
	}
}

// --- env-override path resolution (AC-7) ----------------------------------

func TestResolveDataDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(dataDirEnv, dir)

	got, err := ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	if got != dir {
		t.Errorf("got %q, want %q", got, dir)
	}
}

func TestResolveDataDir_UpwardSearchFromNonRepoCwd(t *testing.T) {
	// Simulates go test's per-package working directory: search must
	// still find the repo root's data/ directory by walking upward from
	// an arbitrary subdirectory, without any env override.
	t.Setenv(dataDirEnv, "")

	sub := t.TempDir()
	repoRoot := filepath.Join(sub, "repo")
	nested := filepath.Join(repoRoot, "a", "b", "c")
	if err := os.MkdirAll(filepath.Join(repoRoot, "data"), 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "data", FileConsumption), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write marker file: %v", err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	got, err := ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	wantSuffix := filepath.Join(repoRoot, "data")
	if got != wantSuffix {
		t.Errorf("got %q, want %q", got, wantSuffix)
	}
}

func TestResolveDataDir_RealRepoFromPackageDir(t *testing.T) {
	// No env override, no chdir: exercises the resolution path against
	// the real repo data/ directory from this package's own working
	// directory, the way `go test ./internal/foundation/data/...` runs.
	t.Setenv(dataDirEnv, "")

	got, err := ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir against real repo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(got, FileConsumption)); err != nil {
		t.Errorf("resolved dir %q does not contain %s: %v", got, FileConsumption, err)
	}
}
