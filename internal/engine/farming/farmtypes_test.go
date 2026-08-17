package farming

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

// This test file is the full FEAT-104 regression suite. Test names are
// chosen to match the acceptance doc's own grep patterns (AC-2..AC-10).

func cid() string { return errs.NewCorrelationID() }

// realFarmTypesPath walks upward from the test cwd to the repo root's
// data/farmtypes.json (the same resolution idea foundation/data uses, but
// self-contained so this package imports no unregistered edge).
func realFarmTypesPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, "data", "farmtypes.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("data/farmtypes.json not found walking upward from %s", dir)
		}
		dir = parent
	}
}

// realCatalogue loads the committed data/farmtypes.json, proving the shipped
// file is well-formed and giving every test the real, data-sourced
// parameters (GR#15 — tests never hardcode balance numbers).
func realCatalogue(t *testing.T) FarmTypeCatalogue {
	t.Helper()
	c, err := LoadFarmTypes(realFarmTypesPath(t), cid())
	if err != nil {
		t.Fatalf("load real data/farmtypes.json: %v", err)
	}
	return c
}

// writeMutatedFarmTypes loads the real data file, lets mutate edit its
// decoded JSON shape, and writes the result to a temp file whose path it
// returns. Used to prove a specific parameter is actually read (AC-2/AC-4)
// and that malformed shapes are rejected (AC-8).
func writeMutatedFarmTypes(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	b, err := os.ReadFile(realFarmTypesPath(t))
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
	p := filepath.Join(t.TempDir(), "farmtypes.json")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// assertErrCode is the GR#7 assertion: the returned error must be a
// registry-sourced *errs.E whose Code matches the claimed MET- code — not
// merely a non-nil error.
func assertErrCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", want)
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected registry-sourced *errs.E, got %T", err)
	}
	if e.Code != want {
		t.Fatalf("expected error code %s, got %s", want, e.Code)
	}
}

// variantNames returns the named variants of a type, in data order.
func variantNames(p FarmTypeParams) []string {
	out := make([]string, 0, len(p.Variants))
	for _, v := range p.Variants {
		out = append(out, v.Name)
	}
	return out
}

// --- AC-3: five-type taxonomy completeness, variants within categories ------

func TestFarmTypeTaxonomyFiveTypes(t *testing.T) {
	cat := realCatalogue(t)

	// The expected type set is derived from the FarmType enum (the manifest),
	// never a hardcoded five-element literal (GR#15).
	got := make(map[string]FarmTypeParams, len(cat.Types()))
	for _, p := range cat.Types() {
		if p.Type.String() == "unknown" {
			t.Fatalf("resolved type has no canonical name")
		}
		got[p.Type.String()] = p
	}
	for ft := FarmTypeArable; ft <= FarmTypeVineyard; ft++ {
		p, ok := got[ft.String()]
		if !ok {
			t.Fatalf("taxonomy missing %s", ft.String())
		}
		if len(p.Variants) == 0 {
			t.Fatalf("type %s has no variants — every category must name at least one §31 crop/livestock", ft.String())
		}
	}
	if len(got) != 5 {
		t.Fatalf("taxonomy has %d entries, want %d", len(got), 5)
	}

	// The named §31 crops/livestock resolve as variants WITHIN their
	// category, not as their own top-level types.
	wantVariants := map[FarmType][]string{
		FarmTypeArable:       {"wheat", "barley", "rapeseed", "potatoes"},
		FarmTypeLivestock:    {"dairy", "beef", "sheep", "pigs", "poultry"},
		FarmTypeOrchard:      {"apples", "cherries", "softFruit"},
		FarmTypeMarketGarden: {"fieldVeg", "polyTunnelSalad", "hops"},
		FarmTypeVineyard:     {"vines"},
	}
	for ft, wants := range wantVariants {
		have := variantNames(got[ft.String()])
		if len(have) != len(wants) {
			t.Fatalf("type %s has %d variants, want %d (%v)", ft.String(), len(have), len(wants), wants)
		}
		for _, w := range wants {
			found := false
			for _, h := range have {
				if h == w {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("type %s is missing its §31 variant %q", ft.String(), w)
			}
		}
	}
}

// --- AC-2: two same-category types resolve to two different parameter sets --

func TestDistinctParamsArableVsOrchard(t *testing.T) {
	cat := realCatalogue(t)
	arable, err := cat.Resolve("arable")
	if err != nil {
		t.Fatal(err)
	}
	orchard, err := cat.Resolve("orchard")
	if err != nil {
		t.Fatal(err)
	}

	// Footprint, soil band, BDI term and chain output are pairwise non-equal
	// AND each actually populated.
	if arable.Footprint <= 0 || orchard.Footprint <= 0 {
		t.Fatalf("footprint not populated: arable=%d orchard=%d", arable.Footprint, orchard.Footprint)
	}
	if arable.Footprint == orchard.Footprint {
		t.Fatalf("arable and orchard share footprint %d — distinct facilities must differ", arable.Footprint)
	}
	if arable.SoilBand == orchard.SoilBand {
		t.Fatalf("arable and orchard share soil band %s — distinct facilities must differ", arable.SoilBand)
	}
	if arable.BDITerm == orchard.BDITerm {
		t.Fatalf("arable and orchard share BDI term %v — distinct facilities must differ", arable.BDITerm)
	}
	if arable.Chain.Commodity == orchard.Chain.Commodity {
		t.Fatalf("arable and orchard share chain commodity %q — distinct facilities must differ", arable.Chain.Commodity)
	}
	if arable.Chain.Destination == orchard.Chain.Destination {
		t.Fatalf("arable and orchard share chain destination %s — distinct facilities must differ", arable.Chain.Destination)
	}
}

func TestIsolatedFarmtypeSingleTypePerturb(t *testing.T) {
	before := realCatalogue(t)
	beforeArable, _ := before.Resolve("arable")
	beforeOrchard, _ := before.Resolve("orchard")

	// Mutate ONLY arable's soil band in the loaded data fixture.
	path := writeMutatedFarmTypes(t, func(m map[string]any) {
		m["types"].(map[string]any)["arable"].(map[string]any)["soilBand"] = "fertileLoam"
	})
	after, err := LoadFarmTypes(path, cid())
	if err != nil {
		t.Fatal(err)
	}
	afterArable, _ := after.Resolve("arable")
	afterOrchard, _ := after.Resolve("orchard")

	// Arable's band changed, and the change is data-sourced (not a switch/case
	// in code): the resolved value now matches the mutated fixture.
	if beforeArable.SoilBand == afterArable.SoilBand {
		t.Fatalf("mutating arable's soil band in the data file did not change its resolved band (%s)", beforeArable.SoilBand)
	}
	if afterArable.SoilBand != SoilBandFertileLoam {
		t.Fatalf("arable resolved soil band %s, want %s (the mutated data value)", afterArable.SoilBand, SoilBandFertileLoam)
	}

	// The OTHER type is byte-identical before and after the single-type
	// perturbation — the change is isolated, proving the difference is
	// data-sourced rather than a shared default.
	if !reflect.DeepEqual(beforeOrchard, afterOrchard) {
		t.Fatalf("mutating arable's soil band changed orchard's parameter set: %+v -> %+v", beforeOrchard, afterOrchard)
	}
}

// --- AC-4: every figure is data-driven --------------------------------------

func TestDataDrivenFarmtypeReload(t *testing.T) {
	before := realCatalogue(t)
	beforeArable, _ := before.Resolve("arable")

	// Mutate footprint AND BDI term in the data file; both must flow through
	// to the resolved parameter set (the data is actually read, not merely
	// present in a file nobody loads).
	path := writeMutatedFarmTypes(t, func(m map[string]any) {
		m["types"].(map[string]any)["arable"].(map[string]any)["footprintCells"] = float64(9)
		m["types"].(map[string]any)["arable"].(map[string]any)["bdiTerm"] = -0.5
	})
	after, err := LoadFarmTypes(path, cid())
	if err != nil {
		t.Fatal(err)
	}
	afterArable, _ := after.Resolve("arable")

	if afterArable.Footprint == beforeArable.Footprint {
		t.Fatalf("mutating footprintCells did not change the resolved footprint (%d)", afterArable.Footprint)
	}
	if afterArable.Footprint != 9 {
		t.Fatalf("resolved footprint %d, want 9 (the mutated data value)", afterArable.Footprint)
	}
	if afterArable.BDITerm == beforeArable.BDITerm {
		t.Fatalf("mutating bdiTerm did not change the resolved BDI term (%v)", afterArable.BDITerm)
	}
	if afterArable.BDITerm != -0.5 {
		t.Fatalf("resolved BDI term %v, want -0.5 (the mutated data value)", afterArable.BDITerm)
	}
}

// --- AC-5: per-type soil band + BDI term as distinct ecological fields ------

func TestSoilBandAndBDITermChalkSlope(t *testing.T) {
	cat := realCatalogue(t)
	arable, err := cat.Resolve("arable")
	if err != nil {
		t.Fatal(err)
	}
	vineyard, err := cat.Resolve("vineyard")
	if err != nil {
		t.Fatal(err)
	}

	// Soil band and BDI term are DISTINCT fields and resolve to different
	// values for arable vs vineyard.
	if arable.SoilBand == vineyard.SoilBand {
		t.Fatalf("arable and vineyard share soil band %s — chalk-slope vines must differ from downland arable", arable.SoilBand)
	}
	if arable.BDITerm == vineyard.BDITerm {
		t.Fatalf("arable and vineyard share BDI term %v — they must differ", arable.BDITerm)
	}
	// Direction: arable is chalk-compatible with a low/negative BDI term;
	// vineyard sits on chalk slopes with a directionally higher BDI term.
	if arable.SoilBand != SoilBandChalkDownland {
		t.Fatalf("arable soil band %s, want chalk-compatible %s", arable.SoilBand, SoilBandChalkDownland)
	}
	if vineyard.SoilBand != SoilBandChalkSlope {
		t.Fatalf("vineyard soil band %s, want chalk-slope %s", vineyard.SoilBand, SoilBandChalkSlope)
	}
	if !(vineyard.BDITerm > arable.BDITerm) {
		t.Fatalf("vineyard BDI term %v is not directionally higher than arable's %v", vineyard.BDITerm, arable.BDITerm)
	}
}

// --- AC-6: stocking density is livestock's own per-variant parameter --------

func TestStockingDensityLivestockVariants(t *testing.T) {
	cat := realCatalogue(t)
	livestock, err := cat.Resolve("livestock")
	if err != nil {
		t.Fatal(err)
	}
	arable, _ := cat.Resolve("arable")
	orchard, _ := cat.Resolve("orchard")

	if len(livestock.Stocking) == 0 {
		t.Fatal("livestock resolves with no stocking table — §31 'with stocking density' is unmet")
	}
	dairy, ok := livestock.StockingFor("dairy")
	if !ok {
		t.Fatal("livestock has no dairy stocking density")
	}
	pigs, ok := livestock.StockingFor("pigs")
	if !ok {
		t.Fatal("livestock has no pigs stocking density")
	}
	if dairy.HeadPerCell == pigs.HeadPerCell {
		t.Fatalf("dairy and pigs share stocking density %v — per-variant densities must differ", dairy.HeadPerCell)
	}

	// The field is livestock-typed: arable and orchard do NOT carry a
	// stocking-density field at all (nil, not a shared zero).
	if arable.Stocking != nil {
		t.Fatalf("arable carries a stocking table (%v) — stocking density is livestock-only", arable.Stocking)
	}
	if orchard.Stocking != nil {
		t.Fatalf("orchard carries a stocking table (%v) — stocking density is livestock-only", orchard.Stocking)
	}
	if _, ok := arable.StockingFor("dairy"); ok {
		t.Fatal("arable.StockingFor returned ok for a livestock variant — non-livestock types must not carry stocking density")
	}
}

// --- AC-7: typed chain output per type, five destinations reachable ---------

func TestChainOutputFiveChains(t *testing.T) {
	cat := realCatalogue(t)
	arable, _ := cat.Resolve("arable")
	livestock, _ := cat.Resolve("livestock")

	// Arable and livestock resolve to different chain-output commodities and
	// destinations.
	if arable.Chain.Commodity == livestock.Chain.Commodity {
		t.Fatalf("arable and livestock share chain commodity %q", arable.Chain.Commodity)
	}
	if arable.Chain.Destination == livestock.Chain.Destination {
		t.Fatalf("arable and livestock share chain destination %s", arable.Chain.Destination)
	}

	// At least five distinct chain destinations (mill, dairy, abattoir,
	// packhouse, winery) are reachable across the five types, counting both
	// type-level chains and variant chain overrides (dairy → dairy plant).
	reached := make(map[ChainDestination]bool)
	for _, p := range cat.Types() {
		reached[p.Chain.Destination] = true
		for _, v := range p.Variants {
			if v.Chain != nil {
				reached[v.Chain.Destination] = true
			}
		}
	}
	for _, want := range []ChainDestination{ChainMill, ChainDairy, ChainAbattoir, ChainPackhouse, ChainWinery} {
		if !reached[want] {
			t.Fatalf("chain destination %s is not reachable across the five types", want)
		}
	}
}

// --- AC-8: unknown type / malformed data — no silent default -----------------

func TestUnknownFarmType(t *testing.T) {
	cat := realCatalogue(t)
	p, err := cat.Resolve("biodynamic")
	assertErrCode(t, err, ErrUnknownFarmType)
	if !reflect.DeepEqual(p, FarmTypeParams{}) {
		t.Fatalf("unknown-type resolve returned a non-zero parameter set %+v — no default substitution allowed", p)
	}
}

func TestMalformedFarmtypes(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing-type", func(m map[string]any) {
			delete(m["types"].(map[string]any), "orchard")
		}},
		{"missing-footprint", func(m map[string]any) {
			m["types"].(map[string]any)["arable"].(map[string]any)["footprintCells"] = float64(0)
		}},
		{"missing-chain-commodity", func(m map[string]any) {
			m["types"].(map[string]any)["arable"].(map[string]any)["chain"].(map[string]any)["commodity"] = ""
		}},
		{"unknown-soil-band", func(m map[string]any) {
			m["types"].(map[string]any)["arable"].(map[string]any)["soilBand"] = "marsh"
		}},
		{"unknown-chain-destination", func(m map[string]any) {
			m["types"].(map[string]any)["arable"].(map[string]any)["chain"].(map[string]any)["destination"] = "brewery"
		}},
		{"negative-stocking-density", func(m map[string]any) {
			m["types"].(map[string]any)["livestock"].(map[string]any)["stocking"].([]any)[0].(map[string]any)["headPerCell"] = -1.0
		}},
		{"stocking-on-non-livestock", func(m map[string]any) {
			m["types"].(map[string]any)["arable"].(map[string]any)["stocking"] = []any{
				map[string]any{"variant": "dairy", "headPerCell": 1.0},
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeMutatedFarmTypes(t, tc.mutate)
			// The failed load must return the registry code AND yield no
			// usable (partial) catalogue — all-or-nothing, never a partial map
			// silently continuing.
			cat, err := LoadFarmTypes(path, cid())
			assertErrCode(t, err, ErrFarmTypeDataInvalid)
			if got := cat.Types(); len(got) != 0 {
				t.Fatalf("failed load returned %d types — expected an all-or-nothing (zero) result", len(got))
			}
		})
	}
}

func TestWrongTypeFarmtypes(t *testing.T) {
	// A field of the wrong JSON type (GR#16) must be rejected by the loader,
	// never silently coerced.
	path := writeMutatedFarmTypes(t, func(m map[string]any) {
		m["types"].(map[string]any)["arable"].(map[string]any)["footprintCells"] = "four"
	})
	cat, err := LoadFarmTypes(path, cid())
	assertErrCode(t, err, ErrFarmTypeDataInvalid)
	if got := cat.Types(); len(got) != 0 {
		t.Fatalf("wrong-type load returned %d types — expected an all-or-nothing (zero) result", len(got))
	}
}

// --- AC-9: deterministic load and resolve -----------------------------------

func TestDeterministicFarmtypeCatalogue(t *testing.T) {
	path := realFarmTypesPath(t)
	first, err := LoadFarmTypes(path, cid())
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadFarmTypes(path, cid())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Types(), second.Types()) {
		t.Fatal("loading the same data/farmtypes.json twice produced different parameter sets — determinism violated")
	}

	// Resolution is a pure function of (data file, type key): same key, same
	// resolved set, independent of any map-iteration order.
	a1, _ := first.Resolve("arable")
	a2, _ := second.Resolve("arable")
	if !reflect.DeepEqual(a1, a2) {
		t.Fatal("resolving 'arable' on two loads produced different results — determinism violated")
	}
}

// --- AC-10: concurrent resolve is race-free ---------------------------------

func TestConcurrentFarmTypeResolve(t *testing.T) {
	cat := realCatalogue(t)
	baseline := cat.Types()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, key := range []string{"arable", "livestock", "orchard", "marketGarden", "vineyard"} {
				p, err := cat.Resolve(key)
				if err != nil {
					t.Errorf("concurrent resolve(%s): %v", key, err)
					return
				}
				if len(p.Variants) == 0 {
					t.Errorf("concurrent resolve(%s) returned no variants", key)
					return
				}
				if p.Type == FarmTypeLivestock {
					if _, ok := p.StockingFor("dairy"); !ok {
						t.Errorf("concurrent resolve(livestock) lost the dairy stocking density")
						return
					}
				}
			}
			if got := cat.Types(); !reflect.DeepEqual(got, baseline) {
				t.Errorf("concurrent Types() returned a different order/set")
			}
		}()
	}
	wg.Wait()
}

// --- SEC-120: catalogue accessors return deep copies -------------------------
//
// Regression for the SEC-120 class ("a catalogue accessor returns a shallow
// copy whose slices/pointers alias the internal SSOT state"): Resolve and
// Types must hand back FarmTypeParams whose Variants/Stocking slices and
// Variant.Chain pointers are deep copies, so a caller editing a "local" field
// cannot corrupt the catalogue for every later call (GR#3) and two goroutines
// editing their own results cannot race on the shared backing arrays (GR#21).
// These tests FAIL against the shallow-copy implementation (the SEC-120 repro:
// wheat -> HACKED-WHEAT on resolve-again) and PASS once the accessors deep-copy.

func variantIndex(p FarmTypeParams, name string) int {
	for i, v := range p.Variants {
		if v.Name == name {
			return i
		}
	}
	return -1
}

func stockingIndex(p FarmTypeParams, name string) int {
	for i, s := range p.Stocking {
		if s.Variant == name {
			return i
		}
	}
	return -1
}

func typeIndex(ps []FarmTypeParams, name string) int {
	for i, p := range ps {
		if p.Name == name {
			return i
		}
	}
	return -1
}

func TestResolveReturnsDeepCopy(t *testing.T) {
	cat := realCatalogue(t)

	t.Run("variants-slice", func(t *testing.T) {
		arable, err := cat.Resolve("arable")
		if err != nil {
			t.Fatal(err)
		}
		if len(arable.Variants) == 0 || arable.Variants[0].Name != "wheat" {
			t.Fatalf("precondition: arable.Variants[0].Name = %q, want wheat", arable.Variants[0].Name)
		}
		arable.Variants[0].Name = "HACKED-WHEAT"
		again, err := cat.Resolve("arable")
		if err != nil {
			t.Fatal(err)
		}
		if again.Variants[0].Name != "wheat" {
			t.Fatalf("mutating a Resolve result corrupted the catalogue: next Resolve gave %q, want wheat", again.Variants[0].Name)
		}
	})

	t.Run("stocking-slice", func(t *testing.T) {
		livestock, err := cat.Resolve("livestock")
		if err != nil {
			t.Fatal(err)
		}
		i := stockingIndex(livestock, "dairy")
		if i < 0 {
			t.Fatal("precondition: livestock has a dairy stocking entry")
		}
		wantHead := livestock.Stocking[i].HeadPerCell
		livestock.Stocking[i].Variant = "HACKED-DAIRY"
		livestock.Stocking[i].HeadPerCell = -999
		again, err := cat.Resolve("livestock")
		if err != nil {
			t.Fatal(err)
		}
		if got := again.Stocking[i]; got.Variant != "dairy" || got.HeadPerCell != wantHead {
			t.Fatalf("mutating a Resolve result's Stocking corrupted the catalogue: next Resolve gave %+v, want {dairy %v}", got, wantHead)
		}
	})

	t.Run("variant-chain-pointer", func(t *testing.T) {
		livestock, err := cat.Resolve("livestock")
		if err != nil {
			t.Fatal(err)
		}
		i := variantIndex(livestock, "dairy")
		if i < 0 || livestock.Variants[i].Chain == nil {
			t.Fatal("precondition: livestock's dairy variant carries a non-nil Chain override")
		}
		livestock.Variants[i].Chain.Commodity = "HACKED-MILK"
		again, err := cat.Resolve("livestock")
		if err != nil {
			t.Fatal(err)
		}
		if got := again.Variants[i].Chain.Commodity; got != "milk" {
			t.Fatalf("mutating a Resolve result's *Chain corrupted the catalogue: next Resolve gave %q, want milk", got)
		}
	})
}

func TestTypesReturnsDeepCopy(t *testing.T) {
	cat := realCatalogue(t)

	t.Run("variants-slice", func(t *testing.T) {
		types := cat.Types()
		i := typeIndex(types, "arable")
		if i < 0 || len(types[i].Variants) == 0 || types[i].Variants[0].Name != "wheat" {
			t.Fatal("precondition: arable (first variant wheat) present in Types()")
		}
		types[i].Variants[0].Name = "HACKED-WHEAT"
		again, err := cat.Resolve("arable")
		if err != nil {
			t.Fatal(err)
		}
		if again.Variants[0].Name != "wheat" {
			t.Fatalf("mutating a Types() result corrupted the catalogue: Resolve gave %q, want wheat", again.Variants[0].Name)
		}
	})

	t.Run("stocking-slice", func(t *testing.T) {
		types := cat.Types()
		i := typeIndex(types, "livestock")
		if i < 0 {
			t.Fatal("precondition: livestock present in Types()")
		}
		si := stockingIndex(types[i], "poultry")
		if si < 0 {
			t.Fatal("precondition: livestock has a poultry stocking entry")
		}
		wantHead := types[i].Stocking[si].HeadPerCell
		types[i].Stocking[si].Variant = "HACKED-POULTRY"
		types[i].Stocking[si].HeadPerCell = -999
		again, err := cat.Resolve("livestock")
		if err != nil {
			t.Fatal(err)
		}
		if got := again.Stocking[si]; got.Variant != "poultry" || got.HeadPerCell != wantHead {
			t.Fatalf("mutating a Types() result's Stocking corrupted the catalogue: Resolve gave %+v, want {poultry %v}", got, wantHead)
		}
	})

	t.Run("variant-chain-pointer", func(t *testing.T) {
		types := cat.Types()
		i := typeIndex(types, "livestock")
		if i < 0 {
			t.Fatal("precondition: livestock present in Types()")
		}
		vi := variantIndex(types[i], "dairy")
		if vi < 0 || types[i].Variants[vi].Chain == nil {
			t.Fatal("precondition: livestock's dairy variant carries a non-nil Chain override")
		}
		types[i].Variants[vi].Chain.Destination = ChainMill
		again, err := cat.Resolve("livestock")
		if err != nil {
			t.Fatal(err)
		}
		if got := again.Variants[vi].Chain.Destination; got != ChainDairy {
			t.Fatalf("mutating a Types() result's *Chain corrupted the catalogue: Resolve gave destination %s, want dairy", got)
		}
	})
}

// TestConcurrentResolveResultsAreIsolated drives concurrent Resolve calls that
// each edit their own result. A shallow copy makes these writes race on the
// shared backing arrays — caught by -race (GR#21); a deep copy keeps every
// goroutine's result private.
func TestConcurrentResolveResultsAreIsolated(t *testing.T) {
	cat := realCatalogue(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, key := range []string{"arable", "livestock", "orchard", "marketGarden", "vineyard"} {
				p, err := cat.Resolve(key)
				if err != nil {
					t.Errorf("concurrent resolve(%s): %v", key, err)
					return
				}
				for j := range p.Variants {
					p.Variants[j].Name = p.Variants[j].Name + "-mine"
					if p.Variants[j].Chain != nil {
						p.Variants[j].Chain.Commodity = p.Variants[j].Chain.Commodity + "-mine"
					}
				}
				for j := range p.Stocking {
					p.Stocking[j].HeadPerCell += 1
				}
			}
		}()
	}
	wg.Wait()
}
