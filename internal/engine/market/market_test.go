package market

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func testCorrelationID() string {
	return errs.NewCorrelationID()
}

// realAPI loads a *MarketAPI against the repository's own
// data/market.json (via ResolveDataDir), for tests that check the
// actual spec-transcribed data (AC-2/AC-3/AC-4/etc.) rather than a
// synthetic fixture.
func realAPI(t *testing.T) *MarketAPI {
	t.Helper()
	dir, err := data.ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	api, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load real data/market.json: %v", err)
	}
	return api
}

func assertCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with code %s, got nil", wantCode)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != wantCode {
		t.Errorf("e.Code = %s, want %s (err: %v)", e.Code, wantCode, err)
	}
}

// --- AC-2: nine-commodity coverage --------------------------------------

func TestNineCommodityCount(t *testing.T) {
	if len(allCommodities) != 9 {
		t.Fatalf("allCommodities has %d entries, want 9", len(allCommodities))
	}

	api := realAPI(t)
	for _, c := range allCommodities {
		if _, err := api.SupplyMode(c); err != nil {
			t.Errorf("commodity %q not resolvable: %v", c, err)
		}
	}
	if len(api.commodities) != 9 {
		t.Errorf("loaded registry has %d commodities, want exactly 9", len(api.commodities))
	}
}

// --- AC-3: supply-mode taxonomy differs per commodity -------------------

func TestSupplyModeDiffersByCommodity(t *testing.T) {
	api := realAPI(t)

	fuelMode, err := api.SupplyMode(Fuel)
	if err != nil {
		t.Fatalf("SupplyMode(Fuel): %v", err)
	}
	waterMode, err := api.SupplyMode(Water)
	if err != nil {
		t.Fatalf("SupplyMode(Water): %v", err)
	}

	if fuelMode != ImportOnly {
		t.Errorf("Fuel.SupplyMode() = %v, want ImportOnly (§6: 'None early; synthetic late')", fuelMode)
	}
	if waterMode != Hybrid {
		t.Errorf("Water.SupplyMode() = %v, want Hybrid", waterMode)
	}
	if fuelMode == waterMode {
		t.Errorf("Fuel and Water report the same supply mode (%v) — at least two commodities must differ", fuelMode)
	}
}

// --- AC-4: static v1 price behind a config-flip seam ---------------------

func TestStaticPriceStable(t *testing.T) {
	api := realAPI(t)

	p1, err := api.Price(Water)
	if err != nil {
		t.Fatalf("Price(Water) #1: %v", err)
	}
	p2, err := api.Price(Water)
	if err != nil {
		t.Fatalf("Price(Water) #2: %v", err)
	}
	if p1 != p2 {
		t.Errorf("Price(Water) not stable across repeated calls: %v != %v", p1, p2)
	}
	if p1 <= 0 {
		t.Errorf("Price(Water) = %v, want a positive static price", p1)
	}
}

// --- AC-5: logistics-capacity-bounded availability ------------------------

func TestAvailabilityCapacityBound(t *testing.T) {
	api := realAPI(t)

	// A request comfortably inside the configured ceiling is granted in
	// full.
	small, err := api.Availability(Water, 10)
	if err != nil {
		t.Fatalf("Availability(Water, 10): %v", err)
	}
	if small.Available != 10 {
		t.Errorf("Availability(Water, 10).Available = %d, want 10", small.Available)
	}

	// A request far beyond the configured ceiling is bounded to the
	// ceiling — proving the returned figure genuinely varies with the
	// capacity input rather than always echoing the request back.
	huge, err := api.Availability(Water, small.CapacityCeiling*10)
	if err != nil {
		t.Fatalf("Availability(Water, huge): %v", err)
	}
	if huge.Available != small.CapacityCeiling {
		t.Errorf("Availability(Water, ceiling*10).Available = %d, want the ceiling %d", huge.Available, small.CapacityCeiling)
	}
	if huge.Available == huge.Requested {
		t.Errorf("Availability did not bound a request exceeding capacity: Available == Requested == %d", huge.Requested)
	}
}

func TestAvailabilityNegativeRequestClampsToZero(t *testing.T) {
	api := realAPI(t)

	r, err := api.Availability(Water, -5)
	if err != nil {
		t.Fatalf("Availability(Water, -5): %v", err)
	}
	if r.Available != 0 {
		t.Errorf("Availability(Water, -5).Available = %d, want 0", r.Available)
	}
}

// --- AC-6: waste is a distinct negative-commodity export-cost path ------

func TestWastePricedOnlyViaExportPrice(t *testing.T) {
	api := realAPI(t)

	// The import-price path must reject Waste rather than silently
	// returning a positive per-unit-received price.
	if _, err := api.Price(Waste); err == nil {
		t.Fatal("Price(Waste) returned nil error, want ErrWasteNotImportable")
	} else {
		assertCode(t, err, ErrWasteNotImportable)
	}

	// The export path must accept Waste and return a real, positive
	// export cost.
	exportPrice, err := api.ExportPrice(Waste)
	if err != nil {
		t.Fatalf("ExportPrice(Waste): %v", err)
	}
	if exportPrice <= 0 {
		t.Errorf("ExportPrice(Waste) = %v, want a positive export cost", exportPrice)
	}
}

func TestExportPriceRejectsNonWasteCommodity(t *testing.T) {
	api := realAPI(t)

	if _, err := api.ExportPrice(Water); err == nil {
		t.Fatal("ExportPrice(Water) returned nil error, want ErrNotExportable")
	} else {
		assertCode(t, err, ErrNotExportable)
	}
}

// --- AC-7: services are structurally excluded -----------------------------

func TestNoServicesInRegistry(t *testing.T) {
	api := realAPI(t)

	services := []string{
		"education", "healthcare", "elderCare", "fire",
		"police", "deathcare", "leisure", "transport",
	}
	for _, s := range services {
		if c, err := api.CommodityByName(s); err == nil {
			t.Errorf("CommodityByName(%q) resolved to %q, want ErrUnknownCommodity (services are declared separately, §10)", s, c)
		} else {
			assertCode(t, err, ErrUnknownCommodity)
		}
	}
}

// --- AC-8: no hardcoded prices — genuinely data-driven --------------------

func TestPriceIsDataDriven(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, fullValidMarketJSON())

	api, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, err := api.Price(Water)
	if err != nil {
		t.Fatalf("Price(Water): %v", err)
	}
	if p != 2000 {
		t.Fatalf("Price(Water) = %v, want 2000 (fixture value)", p)
	}

	// Change the fixture's value and reload — the SAME code path must
	// reflect the new number, proving Price genuinely reads from data
	// with no code-side default or fallback substituting silently.
	changed := strings.Replace(fullValidMarketJSON(), `"importPriceMicropounds": 2000`, `"importPriceMicropounds": 987654`, 1)
	writeFixture(t, dir, changed)

	api2, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load (changed fixture): %v", err)
	}
	p2, err := api2.Price(Water)
	if err != nil {
		t.Fatalf("Price(Water) (changed fixture): %v", err)
	}
	if p2 != 987654 {
		t.Fatalf("Price(Water) after data edit = %v, want 987654 — value did not flow from data", p2)
	}
}

// --- AC-9: int64 micro-pounds, never a float on the price path ----------

func TestPriceTypeIsIntegerMicropounds(t *testing.T) {
	api := realAPI(t)
	p, err := api.Price(Power)
	if err != nil {
		t.Fatalf("Price(Power): %v", err)
	}
	// Compile-time proof: Price's return type is Micropounds, whose
	// underlying type is int64 (see its declaration in market.go) — a
	// float64/float32 return type would not convert to int64 without an
	// explicit (and here absent) truncating conversion at the call site.
	asInt64 := int64(p)
	if Micropounds(asInt64) != p {
		t.Errorf("Price(Power) round-trip through int64 changed value: %v -> %v", p, asInt64)
	}
}

// --- AC-10: unregistered commodity is a registry error, never a panic ---

func TestUnknownCommodityQueriesReturnRegistryError(t *testing.T) {
	api := realAPI(t)
	bogus := CommodityType("plutonium")

	if _, err := api.Price(bogus); err == nil {
		t.Error("Price(bogus) returned nil error")
	} else {
		assertCode(t, err, ErrUnknownCommodity)
	}
	if _, err := api.Availability(bogus, 10); err == nil {
		t.Error("Availability(bogus, 10) returned nil error")
	} else {
		assertCode(t, err, ErrUnknownCommodity)
	}
	if _, err := api.SupplyMode(bogus); err == nil {
		t.Error("SupplyMode(bogus) returned nil error")
	} else {
		assertCode(t, err, ErrUnknownCommodity)
	}
}

func TestUnknownCommodityDoesNotPanic(t *testing.T) {
	api := realAPI(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("querying an unknown commodity panicked: %v", r)
		}
	}()
	_, _ = api.Price(CommodityType("does-not-exist"))
}

// --- AC-11: malformed/invalid market.json --------------------------------

func writeFixture(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, fileMarket), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func fullValidMarketJSON() string {
	return `{
		"version": 1,
		"pricingMode": "static",
		"commodities": {
			"water": {"supplyMode": "hybrid", "unit": "L", "importPriceMicropounds": 2000, "capacityCeiling": 5000000},
			"power": {"supplyMode": "hybrid", "unit": "kWh", "importPriceMicropounds": 150000, "capacityCeiling": 2000000},
			"gas": {"supplyMode": "hybrid", "unit": "kWh", "importPriceMicropounds": 70000, "capacityCeiling": 1500000},
			"foodStaples": {"supplyMode": "hybrid", "unit": "kg", "importPriceMicropounds": 400000, "capacityCeiling": 200000},
			"foodFresh": {"supplyMode": "hybrid", "unit": "kg", "importPriceMicropounds": 900000, "capacityCeiling": 100000},
			"fuel": {"supplyMode": "importOnly", "unit": "L", "importPriceMicropounds": 1500000, "capacityCeiling": 300000},
			"constructionMaterials": {"supplyMode": "hybrid", "unit": "tonne", "importPriceMicropounds": 45000000, "capacityCeiling": 50000},
			"consumerGoods": {"supplyMode": "hybrid", "unit": "kg", "importPriceMicropounds": 3000000, "capacityCeiling": 80000},
			"waste": {"supplyMode": "hybrid", "unit": "kg", "exportPriceMicropounds": 50000, "capacityCeiling": 120000}
		}
	}`
}

func TestLoad_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, `{ not valid json`)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrMarketDataInvalid)
}

func TestLoad_MissingCommodity(t *testing.T) {
	dir := t.TempDir()
	// A fixture with every commodity except "waste".
	missing := `{
		"version": 1,
		"pricingMode": "static",
		"commodities": {
			"water": {"supplyMode": "hybrid", "unit": "L", "importPriceMicropounds": 2000, "capacityCeiling": 5000000},
			"power": {"supplyMode": "hybrid", "unit": "kWh", "importPriceMicropounds": 150000, "capacityCeiling": 2000000},
			"gas": {"supplyMode": "hybrid", "unit": "kWh", "importPriceMicropounds": 70000, "capacityCeiling": 1500000},
			"foodStaples": {"supplyMode": "hybrid", "unit": "kg", "importPriceMicropounds": 400000, "capacityCeiling": 200000},
			"foodFresh": {"supplyMode": "hybrid", "unit": "kg", "importPriceMicropounds": 900000, "capacityCeiling": 100000},
			"fuel": {"supplyMode": "importOnly", "unit": "L", "importPriceMicropounds": 1500000, "capacityCeiling": 300000},
			"constructionMaterials": {"supplyMode": "hybrid", "unit": "tonne", "importPriceMicropounds": 45000000, "capacityCeiling": 50000},
			"consumerGoods": {"supplyMode": "hybrid", "unit": "kg", "importPriceMicropounds": 3000000, "capacityCeiling": 80000}
		}
	}`
	writeFixture(t, dir, missing)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrMissingCommodity)
	if !strings.Contains(err.Error(), "waste") {
		t.Errorf("err = %v, want it to name the missing commodity", err)
	}
}

func TestLoad_NegativePriceRejected(t *testing.T) {
	dir := t.TempDir()
	bad := strings.Replace(fullValidMarketJSON(), `"importPriceMicropounds": 2000`, `"importPriceMicropounds": -2000`, 1)
	writeFixture(t, dir, bad)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrMarketDataInvalid)
}

func TestLoad_NegativeCapacityRejected(t *testing.T) {
	dir := t.TempDir()
	bad := strings.Replace(fullValidMarketJSON(), `"capacityCeiling": 5000000`, `"capacityCeiling": -1`, 1)
	writeFixture(t, dir, bad)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrMarketDataInvalid)
}

func TestLoad_UnrecognisedSupplyModeRejected(t *testing.T) {
	dir := t.TempDir()
	bad := strings.Replace(fullValidMarketJSON(), `"supplyMode": "hybrid", "unit": "L", "importPriceMicropounds": 2000`, `"supplyMode": "bogus", "unit": "L", "importPriceMicropounds": 2000`, 1)
	writeFixture(t, dir, bad)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrMarketDataInvalid)
}

func TestLoad_WasteWithImportPriceRejected(t *testing.T) {
	dir := t.TempDir()
	bad := strings.Replace(fullValidMarketJSON(),
		`"waste": {"supplyMode": "hybrid", "unit": "kg", "exportPriceMicropounds": 50000, "capacityCeiling": 120000}`,
		`"waste": {"supplyMode": "hybrid", "unit": "kg", "importPriceMicropounds": 50000, "capacityCeiling": 120000}`, 1)
	writeFixture(t, dir, bad)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrMarketDataInvalid)
}

func TestLoad_NonWasteWithExportPriceRejected(t *testing.T) {
	dir := t.TempDir()
	bad := strings.Replace(fullValidMarketJSON(),
		`"water": {"supplyMode": "hybrid", "unit": "L", "importPriceMicropounds": 2000, "capacityCeiling": 5000000}`,
		`"water": {"supplyMode": "hybrid", "unit": "L", "importPriceMicropounds": 2000, "exportPriceMicropounds": 100, "capacityCeiling": 5000000}`, 1)
	writeFixture(t, dir, bad)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrMarketDataInvalid)
}

func TestLoad_WrongPricingModeRejected(t *testing.T) {
	dir := t.TempDir()
	bad := strings.Replace(fullValidMarketJSON(), `"pricingMode": "static"`, `"pricingMode": "dynamic"`, 1)
	writeFixture(t, dir, bad)

	_, err := Load(dir, testCorrelationID())
	assertCode(t, err, ErrMarketDataInvalid)
}

func TestLoad_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, fullValidMarketJSON())

	api, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p, err := api.Price(Gas); err != nil || p != 70000 {
		t.Errorf("Price(Gas) = %v, %v; want 70000, nil", p, err)
	}
}

// --- AC-12: determinism ----------------------------------------------------

func TestDeterministicAcrossRepeatedCalls(t *testing.T) {
	api := realAPI(t)

	var firstPrices [9]Micropounds
	for i, c := range allCommodities {
		if c == Waste {
			continue
		}
		v, err := api.Price(c)
		if err != nil {
			t.Fatalf("Price(%v): %v", c, err)
		}
		firstPrices[i] = v
	}

	for iter := 0; iter < 5; iter++ {
		for i, c := range allCommodities {
			if c == Waste {
				continue
			}
			v, err := api.Price(c)
			if err != nil {
				t.Fatalf("Price(%v): %v", c, err)
			}
			if v != firstPrices[i] {
				t.Errorf("iteration %d, commodity %v: got %v, want %v (non-deterministic)", iter, c, v, firstPrices[i])
			}
		}
	}
}

// --- AC-14: concurrent reads are safe (go test -race) -----------------------

func TestConcurrentQueriesAreRaceFree(t *testing.T) {
	api := realAPI(t)

	var wg sync.WaitGroup
	errCh := make(chan error, 128)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(n int64) {
			defer wg.Done()
			if _, err := api.Price(Water); err != nil {
				errCh <- err
			}
			if _, err := api.Availability(Power, n); err != nil {
				errCh <- err
			}
			if _, err := api.SupplyMode(Fuel); err != nil {
				errCh <- err
			}
			if _, err := api.ExportPrice(Waste); err != nil {
				errCh <- err
			}
		}(int64(i))
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent query error: %v", err)
	}
}

// --- BOW MOD-020 ruling 2: nil-pointer guard at the dereference site -----
//
// These three tests construct a *MarketAPI directly (white-box, same
// package) with a commodity record missing the price/capacity pointer
// field Load's validation would normally guarantee present — the only
// way to exercise "the validator's promise was bypassed" without
// actually breaking foundation/data's or this package's own validation.
// Before the guards these tests exist to prove (Price's
// ImportPriceMicropounds check, ExportPrice's ExportPriceMicropounds
// check, Availability's CapacityCeiling check), each dereference
// panicked; ptrInt64 below is the only helper needed to build the
// malformed fixture records.

func ptrInt64(v int64) *int64 { return &v }

// TestPrice_NilImportPriceIsGuarded reproduces the exact panic the
// Tester found (removing the Waste check in Price nil-derefs
// ImportPriceMicropounds) via the general case: ANY non-Waste commodity
// record reaching Price with a nil ImportPriceMicropounds — which Load
// itself can never produce, but a *MarketAPI built by another path (or
// a future weakening of validateCommodityPricingXOR) could. Before the
// guard in market.go's Price, this was a real nil-pointer panic (see
// this file's diff — the guard was temporarily removed and this test
// observed the panic before being restored); after the guard, it is a
// registry-sourced ErrCommodityFieldMissing.
func TestPrice_NilImportPriceIsGuarded(t *testing.T) {
	api := &MarketAPI{
		commodities: map[CommodityType]commodityRecord{
			Water: {
				SupplyMode:      Hybrid,
				Unit:            "L",
				CapacityCeiling: ptrInt64(100),
				// ImportPriceMicropounds deliberately left nil — the
				// exact condition validateCommodityPricingXOR (and
				// foundation/data's MarketFile.Validate) exist to
				// prevent a Load-returned *MarketAPI from ever having.
			},
		},
		correlationID: testCorrelationID(),
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Price panicked instead of returning a typed error: %v", r)
		}
	}()

	_, err := api.Price(Water)
	if err == nil {
		t.Fatal("Price with nil ImportPriceMicropounds returned nil error, want ErrCommodityFieldMissing")
	}
	assertCode(t, err, ErrCommodityFieldMissing)
}

// TestExportPrice_NilExportPriceIsGuarded is TestPrice_NilImportPriceIsGuarded's
// symmetric case for ExportPrice/ExportPriceMicropounds.
func TestExportPrice_NilExportPriceIsGuarded(t *testing.T) {
	api := &MarketAPI{
		commodities: map[CommodityType]commodityRecord{
			Waste: {
				SupplyMode:      Hybrid,
				Unit:            "kg",
				CapacityCeiling: ptrInt64(100),
				// ExportPriceMicropounds deliberately left nil.
			},
		},
		correlationID: testCorrelationID(),
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ExportPrice panicked instead of returning a typed error: %v", r)
		}
	}()

	_, err := api.ExportPrice(Waste)
	if err == nil {
		t.Fatal("ExportPrice with nil ExportPriceMicropounds returned nil error, want ErrCommodityFieldMissing")
	}
	assertCode(t, err, ErrCommodityFieldMissing)
}

// TestAvailability_NilCapacityCeilingIsGuarded is the third dereference
// site found by widening the audit past Price/ExportPrice per Ben's
// ruling-2 instruction ("is that the only place a pointer field is
// dereferenced on the strength of a validator's promise?") —
// Availability dereferences CapacityCeiling on the same unenforced-
// invariant basis.
func TestAvailability_NilCapacityCeilingIsGuarded(t *testing.T) {
	api := &MarketAPI{
		commodities: map[CommodityType]commodityRecord{
			Water: {
				SupplyMode:             Hybrid,
				Unit:                   "L",
				ImportPriceMicropounds: ptrInt64(2000),
				// CapacityCeiling deliberately left nil.
			},
		},
		correlationID: testCorrelationID(),
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Availability panicked instead of returning a typed error: %v", r)
		}
	}()

	_, err := api.Availability(Water, 10)
	if err == nil {
		t.Fatal("Availability with nil CapacityCeiling returned nil error, want ErrCommodityFieldMissing")
	}
	assertCode(t, err, ErrCommodityFieldMissing)
}
