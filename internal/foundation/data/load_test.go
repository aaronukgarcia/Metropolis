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
	writeFixture(t, dir, FileUnlockTrees, `{"version": 1, "trees": [{"category": "roads"}]}`)
	u, err := LoadUnlockTrees(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadUnlockTrees: %v", err)
	}
	if len(u.Trees) != 1 {
		t.Errorf("trees = %+v", u.Trees)
	}
}

func TestLoadNamingCorpus_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileNamingCorpus, `{"version": 1, "categories": {"roadNamesKentish": ["Cheriton", "Seabrook"]}}`)
	n, err := LoadNamingCorpus(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadNamingCorpus: %v", err)
	}
	if len(n.Categories["roadNamesKentish"]) != 2 {
		t.Errorf("categories = %+v", n.Categories)
	}
}

func TestLoadExternalWorld_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileExternalWorld, `{"version": 1, "profiles": [{"key": "london"}]}`)
	e, err := LoadExternalWorld(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadExternalWorld: %v", err)
	}
	if len(e.Profiles) != 1 {
		t.Errorf("profiles = %+v", e.Profiles)
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

// --- LoadAll (AC-3) --------------------------------------------------------

func seedAllFixtures(t *testing.T, dir string) {
	t.Helper()
	writeFixture(t, dir, FileConsumption, `{"version":1,"residential":{"waterLitresPerPersonPerDay":145,"electricityKWhPerPersonPerDay":3.5,"gasKWhPerPersonPerDay":13,"foodStaplesKgPerPersonPerDay":1.4,"foodFreshKgPerPersonPerDay":0.7,"householdWasteKgPerPersonPerDay":1.1,"wastewaterFractionOfWater":0.95},"classes":{}}`)
	writeFixture(t, dir, FileModes, `{"version":1,"entries":[]}`)
	writeFixture(t, dir, FileBuildings, `{"version":1,"entries":[]}`)
	writeFixture(t, dir, FileUnlockTrees, `{"version":1,"trees":[]}`)
	writeFixture(t, dir, FileNamingCorpus, `{"version":1,"categories":{}}`)
	writeFixture(t, dir, FileSeasonal, `{"version":1,"curves":{}}`)
	writeFixture(t, dir, FileExternalWorld, `{"version":1,"profiles":[]}`)
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
