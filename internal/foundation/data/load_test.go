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
