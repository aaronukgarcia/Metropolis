package mining

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// This test file is the feat.minetypes (FEAT-103) regression suite. Test
// names are chosen to match the acceptance doc's own grep patterns (AC-1
// through AC-9). It reuses the package-level cid() and assertErrCode
// helpers from shuffle_test.go.

// realMineTypePath walks upward from the test cwd to the repo root's
// data/minetypes.json (the same resolution idea as realDepositDataPath).
func realMineTypePath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, "data", "minetypes.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("data/minetypes.json not found walking upward from %s", dir)
		}
		dir = parent
	}
}

// realCatalogue loads the committed data/minetypes.json, proving the
// shipped file is well-formed and giving every test the real, data-sourced
// catalogue (GR#15 — tests never hardcode balance numbers).
func realCatalogue(t *testing.T) Catalogue {
	t.Helper()
	c, err := LoadMineTypes(realMineTypePath(t), cid())
	if err != nil {
		t.Fatalf("load real data/minetypes.json: %v", err)
	}
	return c
}

// writeMutatedMineTypes loads the real data file, lets mutate edit its
// decoded JSON shape, and writes the result to a temp file whose path it
// returns. Used to prove a specific parameter is actually read (AC-2/AC-4).
func writeMutatedMineTypes(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	b, err := os.ReadFile(realMineTypePath(t))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	mutate(m)
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "minetypes.json")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// setTypeField edits one field of the named type entry in a decoded
// data/minetypes.json shape.
func setTypeField(m map[string]any, key, field string, value any) {
	for _, ty := range m["types"].([]any) {
		entry := ty.(map[string]any)
		if entry["key"] == key {
			entry[field] = value
		}
	}
}

// anyKeyContains reports whether some catalogue key contains sub. It is the
// data-derived family-coverage check for AC-3: the six §32 family names are
// taxonomy, not balance values, and are matched against the loaded keys
// rather than asserted as a hardcoded six-element literal of exact keys.
func anyKeyContains(keys []string, sub string) bool {
	for _, k := range keys {
		if strings.Contains(k, sub) {
			return true
		}
	}
	return false
}

// --- AC-3: §32 taxonomy completeness, data-derived count --------------------

func TestMineTypeTaxonomy(t *testing.T) {
	c := realCatalogue(t)
	keys := c.Keys()

	// The six §32 extraction-type families must each be represented by some
	// key in the loaded data. The expected set is derived from the data
	// file's own keys and checked by family membership — never a hardcoded
	// six-element literal of exact keys (GR#15).
	families := []string{"chalk", "sand", "brick", "ragstone", "coal", "offshore"}
	for _, fam := range families {
		if !anyKeyContains(keys, fam) {
			t.Errorf("no mine-type key represents the %q family — all six §32 extraction types must be present", fam)
		}
	}
	if len(keys) != len(families) {
		t.Errorf("catalogue has %d types, want %d (one per §32 family)", len(keys), len(families))
	}

	// Every entry resolves to a non-zero output rate (AC-3).
	for _, key := range keys {
		p, err := c.Resolve(key)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", key, err)
		}
		if p.OutputRate <= 0 {
			t.Errorf("mine type %q resolves to a non-positive output rate %v — every type must have a non-zero output", key, p.OutputRate)
		}
	}
}

// --- AC-2: two types resolve to two different parameter sets from data ----

func TestMineTypeTwoTypesDiffer(t *testing.T) {
	c := realCatalogue(t)
	chalk, err := c.Resolve("chalk")
	if err != nil {
		t.Fatal(err)
	}
	coal, err := c.Resolve("deep_coal")
	if err != nil {
		t.Fatal(err)
	}

	// The load-bearing rule: chalk quarry and deep coal mine resolve to two
	// DIFFERENT parameter sets, and every distinguishing field is actually
	// populated AND pairwise non-equal.
	if chalk.Footprint == coal.Footprint {
		t.Errorf("footprint not distinct: chalk=%d coal=%d", chalk.Footprint, coal.Footprint)
	}
	if chalk.OutputRate == coal.OutputRate {
		t.Errorf("output rate not distinct: chalk=%v coal=%v", chalk.OutputRate, coal.OutputRate)
	}
	if chalk.BlightClass == coal.BlightClass {
		t.Errorf("blight class not distinct: chalk=%s coal=%s", chalk.BlightClass, coal.BlightClass)
	}
	if chalk.Jobs == coal.Jobs {
		t.Errorf("jobs not distinct: chalk=%d coal=%d", chalk.Jobs, coal.Jobs)
	}
	chMin, chMax := chalk.DepthBand()
	coMin, coMax := coal.DepthBand()
	if chMin == coMin && chMax == coMax {
		t.Errorf("depth band not distinct: chalk=[%v,%v) coal=[%v,%v)", chMin, chMax, coMin, coMax)
	}
}

// --- AC-2 companion: single-type perturbation is data-sourced, isolated ----

func TestMineTypeIsolationSingleTypePerturb(t *testing.T) {
	before := realCatalogue(t)
	chalkBefore, err := before.Resolve("chalk")
	if err != nil {
		t.Fatal(err)
	}
	coalBefore, err := before.Resolve("deep_coal")
	if err != nil {
		t.Fatal(err)
	}

	// Mutate ONLY chalk's footprint in a copy of the data file.
	path := writeMutatedMineTypes(t, func(m map[string]any) {
		setTypeField(m, "chalk", "footprint", chalkBefore.Footprint+7)
	})

	after, err := LoadMineTypes(path, cid())
	if err != nil {
		t.Fatal(err)
	}
	chalkAfter, err := after.Resolve("chalk")
	if err != nil {
		t.Fatal(err)
	}
	coalAfter, err := after.Resolve("deep_coal")
	if err != nil {
		t.Fatal(err)
	}

	if chalkAfter.Footprint == chalkBefore.Footprint {
		t.Fatalf("mutated chalk footprint did not change (data not read): %d -> %d", chalkBefore.Footprint, chalkAfter.Footprint)
	}
	if coalAfter.Footprint != coalBefore.Footprint {
		t.Fatalf("mutating chalk's footprint changed deep coal's footprint (%d -> %d) — the change did not flow per-type from data", coalBefore.Footprint, coalAfter.Footprint)
	}
}

// --- AC-4: every figure data-driven, no hardcoded literals ------------------

func TestDataDrivenMinetypeReload(t *testing.T) {
	// Two data files differing ONLY in chalk's output rate — the resolved
	// parameter must reflect the change, proving the value is actually read
	// from data/minetypes.json, never a Go literal.
	lowPath := writeMutatedMineTypes(t, func(m map[string]any) {
		setTypeField(m, "chalk", "outputRate", 10.0)
	})
	highPath := writeMutatedMineTypes(t, func(m map[string]any) {
		setTypeField(m, "chalk", "outputRate", 500.0)
	})

	low, err := LoadMineTypes(lowPath, cid())
	if err != nil {
		t.Fatal(err)
	}
	high, err := LoadMineTypes(highPath, cid())
	if err != nil {
		t.Fatal(err)
	}

	lowChalk, err := low.Resolve("chalk")
	if err != nil {
		t.Fatal(err)
	}
	highChalk, err := high.Resolve("chalk")
	if err != nil {
		t.Fatal(err)
	}
	if highChalk.OutputRate <= lowChalk.OutputRate {
		t.Fatalf("raising chalk outputRate did not increase the resolved value (low=%v high=%v) — the parameter is not being read", lowChalk.OutputRate, highChalk.OutputRate)
	}
}

// --- AC-5: depth band + compatibility gates (geology vs deposit distinct) --

func TestMineTypeDepthBandCompatibilityGate(t *testing.T) {
	c := realCatalogue(t)
	coal, err := c.Resolve("deep_coal")
	if err != nil {
		t.Fatal(err)
	}
	chalk, err := c.Resolve("chalk")
	if err != nil {
		t.Fatal(err)
	}

	// Deep coal declares the coal deposit class and a deep band; chalk
	// declares the chalk geology class and a shallow band. The depth check
	// is directional (coal entirely deeper than chalk), never a hardcoded
	// magnitude.
	if coal.DepositClass != "coal" {
		t.Errorf("deep coal depositClass = %q, want coal", coal.DepositClass)
	}
	if chalk.GeologyClass != "chalk" {
		t.Errorf("chalk geologyClass = %q, want chalk", chalk.GeologyClass)
	}
	coMin, coMax := coal.DepthBand()
	chMin, chMax := chalk.DepthBand()
	if coMin <= chMax {
		t.Errorf("deep coal depth band [%v,%v) is not deeper than chalk's [%v,%v)", coMin, coMax, chMin, chMax)
	}

	// The two compatibility gate KINDS are distinct fields: a bulk type is
	// geology-gated only, a deposit-backed type carries both a geology gate
	// and a deposit gate, and those two gates hold different values.
	if chalk.DepositClass != "" {
		t.Errorf("chalk is a bulk type but declares depositClass %q — bulk types are geology-gated only", chalk.DepositClass)
	}
	if coal.GeologyClass != "deep_coal" {
		t.Errorf("deep coal geologyClass = %q, want deep_coal", coal.GeologyClass)
	}
	if coal.GeologyClass == coal.DepositClass {
		t.Errorf("deep coal geology gate %q equals deposit gate %q — geology and deposit are distinct mechanisms and must not collapse to one field", coal.GeologyClass, coal.DepositClass)
	}
}

// --- AC-6: deep coal's spoil tip + subsidence radius + jobs culture ---------

func TestMineTypeDeepCoalSpoilTipSubsidenceRadius(t *testing.T) {
	c := realCatalogue(t)
	coal, err := c.Resolve("deep_coal")
	if err != nil {
		t.Fatal(err)
	}
	chalk, err := c.Resolve("chalk")
	if err != nil {
		t.Fatal(err)
	}

	spoilTip, radius := coal.Subsidence()
	if spoilTip <= 0 {
		t.Errorf("deep coal spoil-tip footprint = %d, want > 0", spoilTip)
	}
	if radius <= 0 {
		t.Errorf("deep coal subsidence radius = %v, want > 0", radius)
	}
	if coal.Jobs <= chalk.Jobs {
		t.Errorf("deep coal jobs %d not greater than chalk jobs %d — the §32 mining-jobs culture requires a materially larger headcount", coal.Jobs, chalk.Jobs)
	}

	// Subsidence/spoil-tip are pinned to the deep-coal type entry
	// specifically: a bulk quarry carries neither (AC-6).
	if chalkSubs, chalkRadius := chalk.Subsidence(); chalkSubs != 0 || chalkRadius != 0 {
		t.Errorf("chalk carries spoil-tip/subsidence (%d, %v) — subsidence is deep-coal-specific, not a shared field", chalkSubs, chalkRadius)
	}

	// The blight-class field is a SEPARATE field from subsidence: a deep
	// coal mine is not merely "high blight" — subsidence is its own risk
	// flag, carried independently.
	if coal.BlightClass == BlightLow && radius > 0 {
		t.Errorf("deep coal has a subsidence radius %v but blight class low — subsidence and blight are independent fields and must be valued separately", radius)
	}
}

// --- AC-7: unknown type / malformed data — no silent default ---------------

func TestUnknownMineType(t *testing.T) {
	c := realCatalogue(t)
	p, err := c.Resolve("gold_mine")
	assertErrCode(t, err, ErrUnknownMineType)
	// No default-substituted parameter set is produced as a side effect.
	if p != (MineTypeParams{}) {
		t.Fatalf("unknown key returned a non-zero parameter set %+v — must not default-substitute", p)
	}
}

func TestMalformedMinetypes(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing-footprint", func(m map[string]any) {
			setTypeField(m, "chalk", "footprint", 0)
		}},
		{"missing-output-rate", func(m map[string]any) {
			setTypeField(m, "chalk", "outputRate", 0.0)
		}},
		{"negative-jobs", func(m map[string]any) {
			setTypeField(m, "chalk", "jobs", -1)
		}},
		{"unknown-blight-class", func(m map[string]any) {
			setTypeField(m, "chalk", "blightClass", "apocalyptic")
		}},
		{"dangling-geology-class", func(m map[string]any) {
			setTypeField(m, "chalk", "geologyClass", "kimberlite")
		}},
		{"dangling-deposit-class", func(m map[string]any) {
			setTypeField(m, "deep_coal", "depositClass", "vibranium")
		}},
		{"inverted-depth-band", func(m map[string]any) {
			setTypeField(m, "chalk", "depthMax", 0.0)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeMutatedMineTypes(t, tc.mutate)
			c, err := LoadMineTypes(path, cid())
			assertErrCode(t, err, ErrMineTypeDataInvalid)
			// All-or-nothing: a failed load yields no partially-populated
			// catalogue (AC-7).
			if c.Len() != 0 {
				t.Fatalf("failed load returned %d types — expected all-or-nothing (zero)", c.Len())
			}
		})
	}
}

func TestWrongTypeMinetypes(t *testing.T) {
	// A field of the wrong JSON type (footprint as a string) must be
	// rejected by typed decoding (GR#16), never coerced or silently
	// defaulted.
	path := writeMutatedMineTypes(t, func(m map[string]any) {
		setTypeField(m, "chalk", "footprint", "six")
	})
	c, err := LoadMineTypes(path, cid())
	assertErrCode(t, err, ErrMineTypeDataInvalid)
	if c.Len() != 0 {
		t.Fatalf("wrong-type load returned %d types — expected all-or-nothing (zero)", c.Len())
	}
}

// --- AC-8: determinism ------------------------------------------------------

func TestDeterministicMinetype(t *testing.T) {
	a := realCatalogue(t)
	b := realCatalogue(t)

	// Loading the same data file twice produces byte-identical resolved
	// parameter sets in the same canonical order.
	if !reflect.DeepEqual(a.All(), b.All()) {
		t.Fatal("loading the same data/minetypes.json twice produced different catalogues (determinism violated)")
	}
	if !reflect.DeepEqual(a.Keys(), b.Keys()) {
		t.Fatal("catalogue key order differs across loads — resolution must not depend on map-iteration order")
	}
}

// --- AC-9: concurrent resolve race-free ------------------------------------

func TestMineTypeConcurrentResolveNoRace(t *testing.T) {
	c := realCatalogue(t)
	keys := c.Keys()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				for _, key := range keys {
					if _, err := c.Resolve(key); err != nil {
						t.Errorf("Resolve(%q): %v", key, err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
