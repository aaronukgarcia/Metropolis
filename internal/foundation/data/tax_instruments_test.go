package data

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

// newTestTaxInstruments builds a minimal-but-valid TaxInstruments value with
// all six instruments present, each satisfying Validate. Negative tests
// mutate one field of this baseline and assert the loader rejects it with a
// registry-sourced CodeSchemaInvalid naming the offending field. Building
// the fixture as typed structs (rather than hand-written JSON) keeps the
// six-instrument shape single-sourced and lets each test break exactly one
// rule; the real-file happy-path test below guards against JSON-tag drift
// against data/tax_instruments.json's actual keys.
func newTestTaxInstruments() TaxInstruments {
	ins := func(name, category string) TaxInstrument {
		return TaxInstrument{
			Name:       name,
			Category:   category,
			RateRange:  &RateRange{MinPercent: 0, MaxPercent: 30},
			Elasticity: &Elasticity{Coefficient: 0.4},
			BearerWeights: &BearerWeights{RatePoints: []RatePoint{{
				RatePercent: 20,
				Bearers: []Bearer{
					{Category: "consumer", Share: 0.7},
					{Category: "firm", Share: 0.3},
				},
			}}},
			ZoneOverrides: map[string]ZoneOverride{},
		}
	}
	paye := ins("PAYE (income tax + National Insurance)", "income")
	paye.IncomeTaxBands = []IncomeTaxBand{{
		LowerBoundMicroPounds: 0,
		UpperBoundMicroPounds: nil,
		RatePercent:           20,
	}}
	paye.NIRates = &NIRates{EmployeePercent: 8, EmployerPercent: 13.8}

	return TaxInstruments{
		Version: 1,
		Instruments: map[string]TaxInstrument{
			"vat":             ins("VAT (sales tax share)", "consumption"),
			"import-duties":   ins("Import duties", "import"),
			"corporation-tax": ins("Corporation tax", "corporateProfit"),
			"paye":            paye,
			"council-tax":     ins("Council tax", "property"),
			"business-rates":  ins("Business rates", "property"),
		},
	}
}

// writeTaxInstruments marshals ti to JSON and writes it as
// tax_instruments.json into a fresh temp dir, returning the dir.
func writeTaxInstruments(t *testing.T, ti TaxInstruments) string {
	t.Helper()
	b, err := json.Marshal(ti)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	dir := t.TempDir()
	writeFixture(t, dir, FileTaxInstruments, string(b))
	return dir
}

// --- happy path against the REAL data file (SEC-089: nothing read it) -----

// TestLoadTaxInstruments_RealFile_LoadsAndValidates proves the orphaned
// data/tax_instruments.json now loads and validates cleanly end-to-end, and
// that the loaded ID set is exactly the six FEAT-056 instruments (not a
// count-only pass) with at least one populated zoneOverrides (AC-4's
// worked example on business-rates).
func TestLoadTaxInstruments_RealFile_LoadsAndValidates(t *testing.T) {
	dir := realDataDir(t)
	ti, err := LoadTaxInstruments(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadTaxInstruments(real data/tax_instruments.json): %v", err)
	}

	if len(ti.Instruments) != 6 {
		t.Errorf("len(Instruments) = %d, want exactly 6", len(ti.Instruments))
	}
	for _, id := range taxInstrumentIDs {
		if _, ok := ti.Instruments[id]; !ok {
			t.Errorf("missing required instrument %q", id)
		}
	}

	br, ok := ti.Instruments["business-rates"]
	if !ok {
		t.Fatal("business-rates not present")
	}
	if len(br.ZoneOverrides) == 0 {
		t.Error("business-rates.zoneOverrides is empty — FEAT-056's zone-scoped worked example (AC-4) is not queryable")
	}
	if _, ok := br.ZoneOverrides["heavyIndustry"]; !ok {
		t.Error("business-rates.zoneOverrides lacks the heavyIndustry discount entry")
	}
}

// --- instrument-ID set (AC-1 / SEC-090) ------------------------------------

func TestLoadTaxInstruments_MissingInstrumentRejected(t *testing.T) {
	ti := newTestTaxInstruments()
	delete(ti.Instruments, "paye")

	_, err := LoadTaxInstruments(writeTaxInstruments(t, ti), testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "missing required instrument")
}

func TestLoadTaxInstruments_UnknownInstrumentIDRejected(t *testing.T) {
	ti := newTestTaxInstruments()
	// A seventh, unknown ID must be rejected, not silently kept (AC-1's
	// false-pass warning: a count-only check would pass six-plus-a-stray).
	ti.Instruments["landValueTax"] = ti.Instruments["vat"]

	_, err := LoadTaxInstruments(writeTaxInstruments(t, ti), testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "unknown instrument ID")
}

// TestTaxInstrumentIDsMatchBuildingIDPattern (SEC-090): every accepted
// instrument ID is a lowercase slug in buildingIDPattern's domain, so the
// IDs that become engine.tax lookup keys can never carry a casing variant
// that a future enforcing loader would reject (weakness pattern #4).
func TestTaxInstrumentIDsMatchBuildingIDPattern(t *testing.T) {
	for _, id := range taxInstrumentIDs {
		if !buildingIDPattern.MatchString(id) {
			t.Errorf("tax instrument ID %q does not match buildingIDPattern %s", id, buildingIDPattern.String())
		}
	}
}

// --- missing-required / out-of-range / unknown-enum fields (AC-13, GR#17) --

func TestLoadTaxInstruments_MissingRateRangeRejected(t *testing.T) {
	ti := newTestTaxInstruments()
	vat := ti.Instruments["vat"]
	vat.RateRange = nil
	ti.Instruments["vat"] = vat

	_, err := LoadTaxInstruments(writeTaxInstruments(t, ti), testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "instruments[vat].rateRange")
}

func TestLoadTaxInstruments_RateRangeOutOfOrderRejected(t *testing.T) {
	ti := newTestTaxInstruments()
	vat := ti.Instruments["vat"]
	vat.RateRange.MinPercent = 40 // > MaxPercent (30): min/max inverted
	ti.Instruments["vat"] = vat

	_, err := LoadTaxInstruments(writeTaxInstruments(t, ti), testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "maxPercent")
}

func TestLoadTaxInstruments_UnknownCategoryRejected(t *testing.T) {
	ti := newTestTaxInstruments()
	vat := ti.Instruments["vat"]
	vat.Category = "incomeTax"
	ti.Instruments["vat"] = vat

	_, err := LoadTaxInstruments(writeTaxInstruments(t, ti), testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "instruments[vat].category")
}

func TestLoadTaxInstruments_BearerSharesNotSummingToOneRejected(t *testing.T) {
	ti := newTestTaxInstruments()
	vat := ti.Instruments["vat"]
	// 0.7 + 0.4 = 1.1, outside the 1e-9 tolerance of 1.0.
	vat.BearerWeights.RatePoints[0].Bearers[1].Share = 0.4
	ti.Instruments["vat"] = vat

	_, err := LoadTaxInstruments(writeTaxInstruments(t, ti), testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "sum to 1.0")
}

func TestLoadTaxInstruments_BearerShareOutOfRangeRejected(t *testing.T) {
	ti := newTestTaxInstruments()
	vat := ti.Instruments["vat"]
	vat.BearerWeights.RatePoints[0].Bearers[0].Share = 1.5 // > 1
	ti.Instruments["vat"] = vat

	_, err := LoadTaxInstruments(writeTaxInstruments(t, ti), testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "must be in [0, 1]")
}

func TestLoadTaxInstruments_NonFiniteRejected(t *testing.T) {
	// The per-field IsFinite guards are exercised through Validate() directly
	// because encoding/json's Marshal rejects NaN/±Inf before a fixture could
	// ever reach the loader — so these cannot route through writeTaxInstruments
	// (mirrors internal/engine/fuel/data.go's own BUG-297 regression test).
	cases := []struct {
		name   string
		mutate func(*TaxInstruments)
	}{
		{"NaN rateRange min", func(ti *TaxInstruments) {
			vat := ti.Instruments["vat"]
			vat.RateRange.MinPercent = math.NaN()
			ti.Instruments["vat"] = vat
		}},
		{"NaN ratePoint rate", func(ti *TaxInstruments) {
			vat := ti.Instruments["vat"]
			vat.BearerWeights.RatePoints[0].RatePercent = math.NaN()
			ti.Instruments["vat"] = vat
		}},
		{"NaN bearer share", func(ti *TaxInstruments) {
			vat := ti.Instruments["vat"]
			vat.BearerWeights.RatePoints[0].Bearers[0].Share = math.NaN()
			ti.Instruments["vat"] = vat
		}},
		{"NaN incomeTaxBand rate", func(ti *TaxInstruments) {
			paye := ti.Instruments["paye"]
			paye.IncomeTaxBands[0].RatePercent = math.NaN()
			ti.Instruments["paye"] = paye
		}},
		{"NaN NI employee percent", func(ti *TaxInstruments) {
			paye := ti.Instruments["paye"]
			paye.NIRates.EmployeePercent = math.NaN()
			ti.Instruments["paye"] = paye
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ti := newTestTaxInstruments()
			tc.mutate(&ti)
			if err := ti.Validate(); err == nil {
				t.Fatalf("non-finite value accepted by Validate")
			}
		})
	}
}

func TestLoadTaxInstruments_UnknownZoneClassRejected(t *testing.T) {
	ti := newTestTaxInstruments()
	br := ti.Instruments["business-rates"]
	br.ZoneOverrides = map[string]ZoneOverride{
		"retail": {RateMultiplier: f64ptr(0.7)},
	}
	ti.Instruments["business-rates"] = br

	_, err := LoadTaxInstruments(writeTaxInstruments(t, ti), testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "zone class")
}

// --- PAYE's two sub-components (AC-2 / AC-13) ------------------------------

func TestLoadTaxInstruments_PayeMissingIncomeTaxBandsRejected(t *testing.T) {
	ti := newTestTaxInstruments()
	paye := ti.Instruments["paye"]
	paye.IncomeTaxBands = nil
	ti.Instruments["paye"] = paye

	_, err := LoadTaxInstruments(writeTaxInstruments(t, ti), testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "incomeTaxBands")
}

func TestLoadTaxInstruments_PayeMissingNIRatesRejected(t *testing.T) {
	ti := newTestTaxInstruments()
	paye := ti.Instruments["paye"]
	paye.NIRates = nil
	ti.Instruments["paye"] = paye

	_, err := LoadTaxInstruments(writeTaxInstruments(t, ti), testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "niRates")
}

// --- determinism (AC-14 / GR#21) -------------------------------------------

func TestLoadTaxInstruments_RepeatedLoadDeepEqual(t *testing.T) {
	dir := realDataDir(t)
	ti1, err := LoadTaxInstruments(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	ti2, err := LoadTaxInstruments(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !reflect.DeepEqual(ti1, ti2) {
		t.Error("repeated LoadTaxInstruments of the same file produced non-equal structs")
	}
}

// TestLoadTaxInstruments_MultipleViolationsDeterministicOrder proves that a
// file with several simultaneously-violating instruments blames the SAME
// (sorted-first) instrument on every load, never depending on Go map
// iteration order (AC-14). council-tax sorts before vat, so council-tax's
// unknown category is the stable first error.
func TestLoadTaxInstruments_MultipleViolationsDeterministicOrder(t *testing.T) {
	ti := newTestTaxInstruments()
	council := ti.Instruments["council-tax"]
	council.Category = "bogus"
	ti.Instruments["council-tax"] = council
	vat := ti.Instruments["vat"]
	vat.RateRange = nil
	ti.Instruments["vat"] = vat

	dir := writeTaxInstruments(t, ti)
	for i := 0; i < 3; i++ {
		_, err := LoadTaxInstruments(dir, testCorrelationID())
		assertPlaceholderCode(t, err, CodeSchemaInvalid, "instruments[council-tax].category")
	}
}

// f64ptr returns a pointer to v, for building pointer-valued struct fields
// in fixtures without a package-level helper.
func f64ptr(v float64) *float64 {
	return &v
}
