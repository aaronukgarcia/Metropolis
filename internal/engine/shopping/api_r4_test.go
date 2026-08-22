package shopping

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- MOD-050 r4 verdict fix regression tests -----------------------------
//
// The r4 REJECT items were: (1) the four format weights lived only as an
// in-code fallback, never in the real data/shopping.json, so production
// never actually read them from data; (2) LoadConfig performed no
// validation on the weight fields at all -- negative/NaN values loaded
// silently, and an explicit zero was indistinguishable from "unloaded"
// (both silently substituted the in-code default). These tests prove the
// fix by mutation: each one mutates the data actually consumed and shows
// the observable output moves accordingly, per this project's
// verification standard (a check that can't fail proves nothing).

// dataDir is the real data/ directory as seen from this package during
// `go test` (internal/engine/shopping -> ../../../data).
const dataDir = "../../../data"

// TestShopping_R4_RealDataFileWeightsAreNonZeroAndLoaded proves the real
// data/shopping.json (not a test fixture) supplies non-zero weight
// values and that LoadConfig actually reads them into s.cfg -- i.e. the
// production Config load path is no longer dead code relying on the
// in-code fallback. Proof-of-failure: if data/shopping.json omitted the
// weight fields (the pre-fix state), cfg.CornerShopWeight etc. would be
// nil pointers here instead of pointers to the disclosed placeholder
// values -- this assertion would fail exactly the way it did before the
// fix.
func TestShopping_R4_RealDataFileWeightsAreNonZeroAndLoaded(t *testing.T) {
	api := New()
	if err := api.LoadConfig(dataDir); err != nil {
		t.Fatalf("failed to load real data/shopping.json: %v", err)
	}

	api.mu.RLock()
	defer api.mu.RUnlock()

	for _, w := range []struct {
		name string
		val  *float64
	}{
		{"cornerShopWeight", api.cfg.CornerShopWeight},
		{"marketHallWeight", api.cfg.MarketHallWeight},
		{"supermarketWeight", api.cfg.SupermarketWeight},
		{"retailParkWeight", api.cfg.RetailParkWeight},
	} {
		if w.val == nil {
			t.Fatalf("expected data/shopping.json to set %s explicitly (r4 fix); got nil (field absent -- the pre-fix state)", w.name)
		}
		if *w.val <= 0 {
			t.Errorf("expected data/shopping.json's %s to be a positive sanctioned placeholder, got %v", w.name, *w.val)
		}
	}
}

// TestShopping_R4_MutatedDataFileWeightsMoveTheSplits proves the real
// data-file weights are load-bearing on production output: it copies the
// real data/shopping.json to a temp dir, mutates ONLY supermarketWeight
// to be dominant, reloads, and shows the split responds -- exactly the
// data-driven behaviour the r4 fix requires (as opposed to the in-code
// fallback, which would ignore this mutation).
func TestShopping_R4_MutatedDataFileWeightsMoveTheSplits(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(dataDir, "shopping.json"))
	if err != nil {
		t.Fatalf("failed to read real data/shopping.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("failed to parse real data/shopping.json: %v", err)
	}

	// Baseline: real weights as shipped.
	baseline := New()
	if err := baseline.LoadConfig(dataDir); err != nil {
		t.Fatalf("failed to load baseline config: %v", err)
	}
	_ = baseline.RegisterCellAccess(101, 5.0, 5.0, 5.0, 5.0, 0.9, 0.9, 0.9, 0.9)
	baselineSplits, _ := baseline.TripsByFormat(101, false)

	// Mutation: crank supermarketWeight to dwarf the others, restore
	// everything else untouched.
	cfg["supermarketWeight"] = 500.0
	mutated, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to remarshal mutated config: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "shopping.json"), mutated, 0644); err != nil {
		t.Fatalf("failed to write mutated config: %v", err)
	}

	mutatedAPI := New()
	if err := mutatedAPI.LoadConfig(tempDir); err != nil {
		t.Fatalf("failed to load mutated config: %v", err)
	}
	_ = mutatedAPI.RegisterCellAccess(101, 5.0, 5.0, 5.0, 5.0, 0.9, 0.9, 0.9, 0.9)
	mutatedSplits, _ := mutatedAPI.TripsByFormat(101, false)

	if mutatedSplits["supermarket"] <= baselineSplits["supermarket"] {
		t.Errorf("expected mutated supermarketWeight (500.0) to raise the supermarket split above baseline; baseline=%d mutated=%d", baselineSplits["supermarket"], mutatedSplits["supermarket"])
	}
}

// TestShopping_R4_NegativeWeightRejected proves LoadConfig rejects a
// negative format weight fail-closed with the registered MET-G4704 code,
// instead of silently loading it (the pre-fix behaviour: a negative
// weight would have flowed straight into the split arithmetic).
func TestShopping_R4_NegativeWeightRejected(t *testing.T) {
	api := New()
	tempDir := t.TempDir()

	configData := `{
		"foodDesertThreshold": 20,
		"onlineDeliveryShare": 0.15,
		"cornerShopPriceMult": 1.5,
		"marketHallPriceMult": 1.1,
		"supermarketPriceMult": 0.9,
		"retailParkPriceMult": 0.85,
		"cornerShopWeight": 1.5,
		"marketHallWeight": 2.0,
		"supermarketWeight": -4.0,
		"retailParkWeight": 3.0
	}`
	if err := os.WriteFile(filepath.Join(tempDir, "shopping.json"), []byte(configData), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	err := api.LoadConfig(tempDir)
	if err == nil {
		t.Fatal("expected error for negative supermarketWeight, got nil")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrInvalidWeightInput {
		t.Errorf("expected MET-G4704 (ErrInvalidWeightInput), got: %v", err)
	}

	// Proof the rejection is fail-CLOSED: the bad config must not have
	// been applied. cfg should still hold New()'s untouched defaults
	// (nil weight pointers -> effectiveWeight's documented default path).
	api.mu.RLock()
	stillNil := api.cfg.SupermarketWeight == nil
	api.mu.RUnlock()
	if !stillNil {
		t.Error("expected s.cfg to remain unmodified after a rejected LoadConfig (fail-closed)")
	}
}

// TestShopping_R4_NaNWeightRejected proves the fail-closed NaN guard
// itself (validateWeights -- the exact function LoadConfig calls) rejects
// a NaN format weight with MET-G4704. Exercised directly against
// validateWeights rather than through a disk-borne JSON fixture because
// Go's encoding/json enforces strict JSON syntax and rejects the bare
// `NaN` token as a parse error before ever reaching this module's own
// validation -- that parse-time rejection is itself fail-closed (proven
// by the second half of this test) but does not exercise MET-G4704, so
// both paths are checked.
func TestShopping_R4_NaNWeightRejected(t *testing.T) {
	nan := math.NaN()
	if !math.IsNaN(nan) {
		t.Fatal("test setup invariant broken: nan must be NaN")
	}

	cfg := Config{
		FoodDesertThreshold:  20,
		OnlineDeliveryShare:  0.15,
		CornerShopPriceMult:  1.5,
		MarketHallPriceMult:  1.1,
		SupermarketPriceMult: 0.9,
		RetailParkPriceMult:  0.85,
		CornerShopWeight:     &nan,
	}
	cfg.MarketHallWeight = floatPtr(2.0)
	cfg.SupermarketWeight = floatPtr(4.0)
	cfg.RetailParkWeight = floatPtr(3.0)

	err := validateWeights(cfg, "test-nan-weight")
	if err == nil {
		t.Fatal("expected validateWeights to reject a NaN cornerShopWeight, got nil")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrInvalidWeightInput {
		t.Errorf("expected MET-G4704 (ErrInvalidWeightInput), got: %v", err)
	}

	// Sanity control: the same cfg with a finite weight passes.
	finite := 4.0
	cfg.CornerShopWeight = &finite
	if err := validateWeights(cfg, "test-nan-weight-control"); err != nil {
		t.Errorf("expected an all-finite cfg to pass validateWeights, got: %v", err)
	}

	// Disk-borne case: encoding/json's own strict JSON-syntax rejection
	// of the bare `NaN` token is ALSO fail-closed -- LoadConfig must
	// reject it and leave cfg untouched, even though the failure surfaces
	// as a JSON syntax error rather than MET-G4704.
	api := New()
	tempDir := t.TempDir()
	base := `{
		"foodDesertThreshold": 20,
		"onlineDeliveryShare": 0.15,
		"cornerShopPriceMult": 1.5,
		"marketHallPriceMult": 1.1,
		"supermarketPriceMult": 0.9,
		"retailParkPriceMult": 0.85,
		"cornerShopWeight": NaN,
		"marketHallWeight": 2.0,
		"supermarketWeight": 4.0,
		"retailParkWeight": 3.0
	}`
	if err := os.WriteFile(filepath.Join(tempDir, "shopping.json"), []byte(base), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	if err := api.LoadConfig(tempDir); err == nil {
		t.Fatal("expected LoadConfig to reject a disk-borne NaN cornerShopWeight, got nil")
	}
	api.mu.RLock()
	stillNil := api.cfg.CornerShopWeight == nil
	api.mu.RUnlock()
	if !stillNil {
		t.Error("expected s.cfg to remain unmodified after a rejected LoadConfig (fail-closed)")
	}
}

// floatPtr is a small test helper: Go has no address-of-literal syntax.
func floatPtr(v float64) *float64 { return &v }

// TestShopping_R4_ZeroWeightDisablesFormatExplicitly proves the r4 fix's
// second requirement: an explicit zero weight disables that format's
// trips (they redistribute), and it is NOT silently replaced by the
// in-code default the way the pre-fix code replaced any zero weight.
// The other three formats remain unaffected in relative proportion to
// each other.
func TestShopping_R4_ZeroWeightDisablesFormatExplicitly(t *testing.T) {
	api := New()
	_ = api.RegisterCellAccess(101, 5.0, 5.0, 5.0, 5.0, 0.9, 0.9, 0.9, 0.9)

	tempDir := t.TempDir()
	configData := `{
		"foodDesertThreshold": 20,
		"onlineDeliveryShare": 0.15,
		"cornerShopPriceMult": 1.5,
		"marketHallPriceMult": 1.1,
		"supermarketPriceMult": 0.9,
		"retailParkPriceMult": 0.85,
		"cornerShopWeight": 1.5,
		"marketHallWeight": 2.0,
		"supermarketWeight": 0,
		"retailParkWeight": 3.0
	}`
	if err := os.WriteFile(filepath.Join(tempDir, "shopping.json"), []byte(configData), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	if err := api.LoadConfig(tempDir); err != nil {
		t.Fatalf("expected explicit zero weight to load successfully (disable, not reject): %v", err)
	}

	splits, err := api.TripsByFormat(101, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if splits["supermarket"] != 0 {
		t.Errorf("expected supermarket trips to be exactly 0 when supermarketWeight=0 explicitly (disabled, not defaulted), got %d", splits["supermarket"])
	}
	if splits["corner_shop"] == 0 || splits["market_hall"] == 0 || splits["retail_park"] == 0 {
		t.Errorf("expected the other three formats to remain active when only supermarketWeight is disabled, got splits: %+v", splits)
	}

	// Proof-of-failure control: the SAME cell with the DEFAULT (non-zero)
	// supermarketWeight must produce a non-zero supermarket split, so we
	// know the zero above genuinely came from the explicit disable and
	// not from some unrelated zeroing of the whole split map.
	control := New()
	_ = control.RegisterCellAccess(101, 5.0, 5.0, 5.0, 5.0, 0.9, 0.9, 0.9, 0.9)
	controlSplits, _ := control.TripsByFormat(101, false)
	if controlSplits["supermarket"] == 0 {
		t.Fatal("control (default weights) unexpectedly produced a zero supermarket split -- test fixture broken")
	}
}
