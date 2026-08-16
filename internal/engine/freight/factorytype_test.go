package freight

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- fixtures -----------------------------------------------------------

// copyDataFile copies a single data file verbatim into a temp data dir.
func copyDataFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// writeMutatedJSON reads src, applies mutate (if non-nil) to the decoded
// object, and writes the result to dst.
func writeMutatedJSON(t *testing.T, src, dst string, mutate func(map[string]any)) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", src, err)
	}
	if mutate != nil {
		mutate(m)
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", src, err)
	}
	if err := os.WriteFile(dst, out, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// loadFactoryTypeDir builds a temp data dir (real market/logistics plus a
// mutated freight.json and factorytypes.json) and Loads it, returning the
// Load error so malformed-data tests can assert on it.
func loadFactoryTypeDir(t *testing.T, freightMutate, factoryTypeMutate func(map[string]any)) (*FreightAPI, error) {
	t.Helper()
	repo := repoDataDir(t)
	dir := t.TempDir()
	for _, f := range []string{"market.json", "logistics.json"} {
		copyDataFile(t, filepath.Join(repo, f), filepath.Join(dir, f))
	}
	writeMutatedJSON(t, filepath.Join(repo, "freight.json"), filepath.Join(dir, "freight.json"), freightMutate)
	writeMutatedJSON(t, filepath.Join(repo, "factorytypes.json"), filepath.Join(dir, "factorytypes.json"), factoryTypeMutate)

	f, err := Load(dir, "factorytype-test-correlation")
	if err != nil {
		return nil, err
	}
	if err := f.LoadFactoryTypeCatalogue(filepath.Join(dir, fileFactoryTypes)); err != nil {
		return nil, err
	}
	return f, nil
}

// factoryTypeFixture is loadFactoryTypeDir with a fatal on Load error — the
// happy-path convenience for tests that expect valid data.
func factoryTypeFixture(t *testing.T, freightMutate, factoryTypeMutate func(map[string]any)) *FreightAPI {
	t.Helper()
	f, err := loadFactoryTypeDir(t, freightMutate, factoryTypeMutate)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return f
}

// setFreightStageJobs mutates a chain stage's jobs figure in a decoded
// freight.json object (found by family + stage id, not index).
func setFreightStageJobs(m map[string]any, family, stageID string, jobs float64) {
	chains := m["chains"].(map[string]any)
	fam := chains[family].(map[string]any)
	stages := fam["stages"].([]any)
	for _, s := range stages {
		st := s.(map[string]any)
		if st["id"] == stageID {
			st["jobs"] = jobs
			return
		}
	}
}

// factoryTypeField returns the nested per-type object for key, so a test can
// mutate one field without repeating the type assertion.
func factoryTypeField(m map[string]any, key string) map[string]any {
	return m["factoryTypes"].(map[string]any)[key].(map[string]any)
}

// --- AC-1: typed resolve surface, no discriminator field ------------------

func TestFactoryTypeParamsHasNamedFields(t *testing.T) {
	typ := reflect.TypeOf(FactoryTypeParams{})
	named := map[string]bool{}
	hasTypeString := false
	for i := 0; i < typ.NumField(); i++ {
		fld := typ.Field(i)
		if fld.Name == "Type" && fld.Type.Kind() == reflect.String {
			hasTypeString = true
		}
		named[fld.Name] = true
	}
	if hasTypeString {
		t.Fatal("FactoryTypeParams carries a discriminator string field — the generic-factory anti-pattern AC-1 forbids")
	}
	for _, want := range []string{"Key", "FootprintCells", "Inputs", "Outputs", "Jobs", "PowerKWhPerDay", "WaterLitresPerDay", "BlightClass"} {
		if !named[want] {
			t.Errorf("FactoryTypeParams missing named, typed field %s", want)
		}
	}
}

// --- AC-2: two types resolve to two different parameter sets -------------

func TestSteelVsElectronics(t *testing.T) {
	f := factoryTypeFixture(t, nil, nil)
	steel, err := f.FactoryType(FactorySteelMill)
	if err != nil {
		t.Fatalf("steel: %v", err)
	}
	elec, err := f.FactoryType(FactoryElectronics)
	if err != nil {
		t.Fatalf("electronics: %v", err)
	}

	// Every distinguishing field is populated AND pairwise non-equal.
	if steel.FootprintCells == elec.FootprintCells {
		t.Errorf("footprint not distinct: both %d", steel.FootprintCells)
	}
	if reflect.DeepEqual(steel.Inputs, elec.Inputs) {
		t.Errorf("inputs not distinct: both %+v", steel.Inputs)
	}
	if reflect.DeepEqual(steel.Outputs, elec.Outputs) {
		t.Errorf("outputs not distinct: both %+v", steel.Outputs)
	}
	if steel.Jobs == elec.Jobs {
		t.Errorf("jobs not distinct: both %d", steel.Jobs)
	}
	if steel.PowerKWhPerDay == elec.PowerKWhPerDay {
		t.Errorf("power draw not distinct: both %d", steel.PowerKWhPerDay)
	}
	if steel.WaterLitresPerDay == elec.WaterLitresPerDay {
		t.Errorf("water draw not distinct: both %d", steel.WaterLitresPerDay)
	}
	if steel.BlightClass == elec.BlightClass {
		t.Errorf("blight class not distinct: both %d", steel.BlightClass)
	}

	// And each is actually populated (not a defaulted zero that happens to
	// differ only by key).
	if len(steel.Inputs) == 0 || len(steel.Outputs) == 0 {
		t.Errorf("steel input-output pair not populated")
	}
	if len(elec.Inputs) == 0 || len(elec.Outputs) == 0 {
		t.Errorf("electronics input-output pair not populated")
	}
}

func TestSingleTypePerturb(t *testing.T) {
	before := factoryTypeFixture(t, nil, nil)
	elecBefore, err := before.FactoryType(FactoryElectronics)
	if err != nil {
		t.Fatalf("electronics before: %v", err)
	}
	textilesBefore, err := before.FactoryType(FactoryTextiles)
	if err != nil {
		t.Fatalf("textiles before: %v", err)
	}

	// Mutate ONLY electronics' utility draw in the data file; textiles must
	// be byte-identical before and after (the difference is data-sourced and
	// isolated, not a shared default row).
	after := factoryTypeFixture(t, nil, func(m map[string]any) {
		factoryTypeField(m, "electronics")["powerKWhPerDay"] = float64(elecBefore.PowerKWhPerDay * 2)
	})
	elecAfter, err := after.FactoryType(FactoryElectronics)
	if err != nil {
		t.Fatalf("electronics after: %v", err)
	}
	textilesAfter, err := after.FactoryType(FactoryTextiles)
	if err != nil {
		t.Fatalf("textiles after: %v", err)
	}

	if elecAfter.PowerKWhPerDay != elecBefore.PowerKWhPerDay*2 {
		t.Errorf("electronics power after = %d, want %d", elecAfter.PowerKWhPerDay, elecBefore.PowerKWhPerDay*2)
	}
	if !reflect.DeepEqual(textilesAfter, textilesBefore) {
		t.Errorf("textiles changed when only electronics was perturbed: %+v -> %+v", textilesBefore, textilesAfter)
	}
}

// --- AC-3: taxonomy completeness (data/manifest-derived, not a literal) ---

func TestFactoryTypeTaxonomy(t *testing.T) {
	f := factoryTypeFixture(t, nil, nil)
	got := f.FactoryTypes()

	// The expected count comes from the manifest, not a hardcoded literal.
	if len(got) != len(allFactoryTypes) {
		t.Fatalf("resolved %d factory types, want %d (the manifest)", len(got), len(allFactoryTypes))
	}
	seen := map[FactoryType]bool{}
	for _, ft := range got {
		if ft.Jobs <= 0 {
			t.Errorf("type %s has non-positive jobs %d", ft.Key.String(), ft.Jobs)
		}
		if len(ft.Inputs) == 0 || len(ft.Outputs) == 0 {
			t.Errorf("type %s has a zero input-output pair", ft.Key.String())
		}
		seen[ft.Key] = true
	}
	for _, key := range allFactoryTypes {
		if !seen[key] {
			t.Errorf("manifest type %s missing from the resolved catalogue", key.String())
		}
	}
}

// --- AC-4: every figure data-driven, actually read -----------------------

func TestFactoryTypeDataDrivenReload(t *testing.T) {
	before := factoryTypeFixture(t, nil, nil)
	glassBefore, err := before.FactoryType(FactoryGlass)
	if err != nil {
		t.Fatalf("glass before: %v", err)
	}

	// Mutate a value in the data file and reload: the resolved parameter
	// must reflect the change (the data is actually read, not hardcoded).
	after := factoryTypeFixture(t, nil, func(m map[string]any) {
		factoryTypeField(m, "glass")["footprintCells"] = float64(glassBefore.FootprintCells + 7)
	})
	glassAfter, err := after.FactoryType(FactoryGlass)
	if err != nil {
		t.Fatalf("glass after: %v", err)
	}

	if glassAfter.FootprintCells != glassBefore.FootprintCells+7 {
		t.Errorf("data-driven footprint: got %d, want %d", glassAfter.FootprintCells, glassBefore.FootprintCells+7)
	}
}

// --- AC-5: single source of truth against data/freight.json --------------

func TestFactoryTypeSingleSource(t *testing.T) {
	f := factoryTypeFixture(t, nil, nil)

	// The overlapping facilities (cement, steel, food processing) resolve
	// byte-equal to their chain stage through ONE code path.
	overlaps := []struct {
		typeKey FactoryType
		stageID StageID
	}{
		{FactorySteelMill, "steelMill"},
		{FactoryCement, "cementPlant"},
		{FactoryFoodProcessing, "flourMill"},
	}
	for _, o := range overlaps {
		ft, err := f.FactoryType(o.typeKey)
		if err != nil {
			t.Fatalf("%s: %v", o.typeKey.String(), err)
		}
		st, err := f.Stage(o.stageID)
		if err != nil {
			t.Fatalf("stage %s: %v", o.stageID, err)
		}
		if !reflect.DeepEqual(ft.Inputs, st.Inputs) {
			t.Errorf("%s inputs %+v != stage %s inputs %+v", o.typeKey.String(), ft.Inputs, o.stageID, st.Inputs)
		}
		if !reflect.DeepEqual(ft.Outputs, st.Outputs) {
			t.Errorf("%s outputs %+v != stage %s outputs %+v", o.typeKey.String(), ft.Outputs, o.stageID, st.Outputs)
		}
		if ft.Jobs != st.Jobs {
			t.Errorf("%s jobs %d != stage %s jobs %d", o.typeKey.String(), ft.Jobs, o.stageID, st.Jobs)
		}
		if ft.PowerKWhPerDay != st.PowerKWhPerDay {
			t.Errorf("%s power %d != stage %s power %d", o.typeKey.String(), ft.PowerKWhPerDay, o.stageID, st.PowerKWhPerDay)
		}
		if ft.WaterLitresPerDay != st.WaterLitresPerDay {
			t.Errorf("%s water %d != stage %s water %d", o.typeKey.String(), ft.WaterLitresPerDay, o.stageID, st.WaterLitresPerDay)
		}
		if int(ft.BlightClass) != st.BlightClass {
			t.Errorf("%s blight %d != stage %s blight %d", o.typeKey.String(), ft.BlightClass, o.stageID, st.BlightClass)
		}
	}

	// Mutate ONE source (freight.json's cementPlant jobs) and assert BOTH
	// surfaces change together — proving a reference, not a copy.
	g := factoryTypeFixture(t, func(m map[string]any) {
		setFreightStageJobs(m, "construction", "cementPlant", 999)
	}, nil)
	cement, err := g.FactoryType(FactoryCement)
	if err != nil {
		t.Fatalf("cement after mutation: %v", err)
	}
	stage, err := g.Stage("cementPlant")
	if err != nil {
		t.Fatalf("cementPlant after mutation: %v", err)
	}
	if stage.Jobs != 999 {
		t.Fatalf("stage cementPlant jobs = %d, want 999 (mutation did not land)", stage.Jobs)
	}
	if cement.Jobs != stage.Jobs {
		t.Errorf("factory cement jobs %d != stage cementPlant jobs %d — the reference is broken, the two drifted", cement.Jobs, stage.Jobs)
	}
}

// --- AC-6: footprint + blight as facility-level params, heavy vs light ----

func TestFactoryTypeHeavyVsLight(t *testing.T) {
	f := factoryTypeFixture(t, nil, nil)
	steel, _ := f.FactoryType(FactorySteelMill)
	chem, _ := f.FactoryType(FactoryChemicalsConverter)
	cement, _ := f.FactoryType(FactoryCement)
	elec, _ := f.FactoryType(FactoryElectronics)
	textiles, _ := f.FactoryType(FactoryTextiles)
	assembler, _ := f.FactoryType(FactoryAssembler)

	heavy := []FactoryTypeParams{steel, chem, cement}
	light := []FactoryTypeParams{elec, textiles, assembler}
	for _, h := range heavy {
		for _, l := range light {
			if h.FootprintCells <= l.FootprintCells {
				t.Errorf("heavy %s footprint %d must exceed light %s footprint %d", h.Key.String(), h.FootprintCells, l.Key.String(), l.FootprintCells)
			}
			if h.BlightClass <= l.BlightClass {
				t.Errorf("heavy %s blight %d must exceed light %s blight %d", h.Key.String(), h.BlightClass, l.Key.String(), l.BlightClass)
			}
		}
	}

	// Every blight class maps to the documented enum, not a free-form value.
	for _, ft := range f.FactoryTypes() {
		if !validBlightClass(ft.BlightClass) {
			t.Errorf("type %s blight %d is not a documented enum value", ft.Key.String(), ft.BlightClass)
		}
	}
}

// --- AC-7: FDI archetype mapping + per-type utility draw -----------------

func TestFactoryTypeUtilityDraw(t *testing.T) {
	f := factoryTypeFixture(t, nil, nil)
	elec, _ := f.FactoryType(FactoryElectronics)
	textiles, _ := f.FactoryType(FactoryTextiles)
	assembler, _ := f.FactoryType(FactoryAssembler)
	chem, _ := f.FactoryType(FactoryChemicalsConverter)

	if elec.PowerKWhPerDay <= textiles.PowerKWhPerDay || elec.PowerKWhPerDay <= assembler.PowerKWhPerDay {
		t.Errorf("electronics utility draw (%d) must materially exceed textiles (%d) and assembler (%d)",
			elec.PowerKWhPerDay, textiles.PowerKWhPerDay, assembler.PowerKWhPerDay)
	}
	if chem.BlightClass <= assembler.BlightClass {
		t.Errorf("chemicals converter blight (%d) must exceed assembler blight (%d)", chem.BlightClass, assembler.BlightClass)
	}
}

func TestFactoryTypeFDIArchetype(t *testing.T) {
	f := factoryTypeFixture(t, nil, nil)

	// §46 archetypes map onto factory types with the archetype's character
	// carried as a per-type parameter, not as prose.
	elec, err := f.FactoryType(FactoryElectronics) // semiconductor fab
	if err != nil {
		t.Fatal(err)
	}
	chem, err := f.FactoryType(FactoryChemicalsConverter) // chemicals complex
	if err != nil {
		t.Fatal(err)
	}
	steel, err := f.FactoryType(FactorySteelMill) // steel process plant
	if err != nil {
		t.Fatal(err)
	}

	if elec.BlightClass != BlightLow {
		t.Errorf("semiconductor fab maps to electronics, which must be low-blight (a clean fab), got %d", elec.BlightClass)
	}
	if chem.BlightClass != BlightHeavy {
		t.Errorf("chemicals complex maps to chemicals converter, which must carry §46's blight-class-high, got %d", chem.BlightClass)
	}
	if steel.BlightClass != BlightHeavy {
		t.Errorf("steel process plant maps to steel mill, the §33 chain anchor (heavy blight), got %d", steel.BlightClass)
	}
}

// --- AC-8: unknown type / malformed data → registry-sourced error ---------

func TestUnknownFactoryType(t *testing.T) {
	f := factoryTypeFixture(t, nil, nil)
	_, err := f.FactoryType(FactoryType(99)) // out-of-range, not a real type
	if err == nil {
		t.Fatal("expected an error for an unknown factory-type key")
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected a registry-sourced *errs.E, got %T", err)
	}
	if e.Code != ErrUnknownFactoryType {
		t.Errorf("error code = %s, want %s", e.Code, ErrUnknownFactoryType)
	}
	// No partially-loaded or default-substituted catalogue: the known types
	// still resolve exactly the manifest count.
	if got := len(f.FactoryTypes()); got != len(allFactoryTypes) {
		t.Errorf("catalogue changed after an unknown-key resolve: %d types, want %d", got, len(allFactoryTypes))
	}
}

func TestMalformedFactorytypes(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missingFootprint", func(m map[string]any) { factoryTypeField(m, "assembler")["footprintCells"] = float64(0) }},
		{"negativeJobs", func(m map[string]any) { factoryTypeField(m, "textiles")["jobs"] = float64(-5) }},
		{"unknownBlightEnum", func(m map[string]any) { factoryTypeField(m, "glass")["blightClass"] = float64(9) }},
		{"danglingStageRef", func(m map[string]any) { factoryTypeField(m, "steelMill")["stageRef"] = "nonexistentStage" }},
		{"stageRefWithDuplicateInline", func(m map[string]any) { factoryTypeField(m, "cement")["jobs"] = float64(30) }},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			f, err := loadFactoryTypeDir(t, nil, c.mutate)
			if err == nil {
				t.Fatalf("expected Load to fail for %s, got a non-nil *FreightAPI", c.name)
			}
			if f != nil {
				t.Fatalf("expected a nil *FreightAPI on failure, got a partially-loaded catalogue (%s)", c.name)
			}
			var e *errs.E
			if !errors.As(err, &e) {
				t.Fatalf("expected a registry-sourced *errs.E, got %T", err)
			}
			if e.Code != ErrFactoryTypeDataInvalid {
				t.Errorf("error code = %s, want %s", e.Code, ErrFactoryTypeDataInvalid)
			}
		})
	}
}

func TestWrongTypeFactorytypes(t *testing.T) {
	f, err := loadFactoryTypeDir(t, nil, func(m map[string]any) {
		// jobs as a string: the wrong JSON type for an *int64 field (GR#16).
		factoryTypeField(m, "electronics")["jobs"] = "not-a-number"
	})
	if err == nil {
		t.Fatal("expected Load to fail for a wrong-typed field")
	}
	if f != nil {
		t.Fatalf("expected nil *FreightAPI, got a partial catalogue")
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.E, got %T", err)
	}
	if e.Code != ErrFactoryTypeDataInvalid {
		t.Errorf("error code = %s, want %s", e.Code, ErrFactoryTypeDataInvalid)
	}
}

// --- AC-9: deterministic function of (data file, type key) ---------------

func TestFactoryTypeDeterminism(t *testing.T) {
	a := factoryTypeFixture(t, nil, nil).FactoryTypes()
	b := factoryTypeFixture(t, nil, nil).FactoryTypes()

	if !reflect.DeepEqual(a, b) {
		t.Errorf("two loads of the same data produced different resolved parameter sets")
	}
	for i, ft := range a {
		if ft.Key != allFactoryTypes[i] {
			t.Errorf("resolve order not manifest order at index %d: got %s, want %s", i, ft.Key.String(), allFactoryTypes[i].String())
		}
	}
}

// --- AC-10: concurrent resolve is race-free (SG-7 scoped) ----------------

func TestFactoryTypeConcurrent(t *testing.T) {
	f := factoryTypeFixture(t, nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, key := range allFactoryTypes {
				if _, err := f.FactoryType(key); err != nil {
					t.Errorf("FactoryType(%s): %v", key.String(), err)
				}
			}
			if got := len(f.FactoryTypes()); got != len(allFactoryTypes) {
				t.Errorf("FactoryTypes() returned %d, want %d", got, len(allFactoryTypes))
			}
		}()
	}
	wg.Wait()
}
