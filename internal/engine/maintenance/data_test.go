package maintenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRateDataDriven proves AC-2: engineer-days/year is a function of class,
// loaded from data/maintenance.json — never a hardcoded per-class Go literal.
// Two classes resolve to two distinct rates from data (a real ordering, not a
// single constant wearing a class field).
func TestRateDataDriven(t *testing.T) {
	a, err := loadFixture(t, validFixture)
	if err != nil {
		t.Fatalf("load valid fixture: %v", err)
	}

	if err := a.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register dwelling: %v", err)
	}
	if err := a.Register(2, "shop", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register shop: %v", err)
	}

	dwelling, _ := a.View(1, "test")
	shop, _ := a.View(2, "test")
	if dwelling.BaseEngineerDaysPerYear == shop.BaseEngineerDaysPerYear {
		t.Fatalf("two classes resolved to the same rate %d — 'scaled by class' is not a real ordering", dwelling.BaseEngineerDaysPerYear)
	}
	// The computed figure reflects the data file's own values, not a Go literal.
	if dwelling.BaseEngineerDaysPerYear != 10 || shop.BaseEngineerDaysPerYear != 8 {
		t.Fatalf("base rates = (%d, %d), want the fixture's (10, 8)", dwelling.BaseEngineerDaysPerYear, shop.BaseEngineerDaysPerYear)
	}
}

// TestReloadMaintenanceDataReflectsRateChange proves AC-2's data-driven
// check: mutating one class's rate in the loaded fixture changes that class's
// computed engineer-days/year (and only that class's), proving the mapping is
// live data, not a compiled constant.
func TestReloadMaintenanceDataReflectsRateChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte(validFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	a, err := Load(dir, "test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := a.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register dwelling: %v", err)
	}
	if err := a.Register(2, "shop", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register shop: %v", err)
	}
	beforeDwelling, _ := a.View(1, "test")
	beforeShop, _ := a.View(2, "test")

	// Mutate only dwelling's rate (10 -> 20); shop stays 8.
	mutated := strings.Replace(validFixture, `"dwelling": {"engineerDaysPerYear": 10`, `"dwelling": {"engineerDaysPerYear": 20`, 1)
	if mutated == validFixture {
		t.Fatal("fixture mutation did not take effect (replacement string mismatch)")
	}
	if err := os.WriteFile(path, []byte(mutated), 0o644); err != nil {
		t.Fatalf("rewrite fixture: %v", err)
	}

	b, err := Load(dir, "test")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := b.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("re-register dwelling: %v", err)
	}
	if err := b.Register(2, "shop", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("re-register shop: %v", err)
	}
	afterDwelling, _ := b.View(1, "test")
	afterShop, _ := b.View(2, "test")

	if afterDwelling.BaseEngineerDaysPerYear != beforeDwelling.BaseEngineerDaysPerYear*2 {
		t.Fatalf("dwelling rate did not reflect the data mutation: before=%d after=%d", beforeDwelling.BaseEngineerDaysPerYear, afterDwelling.BaseEngineerDaysPerYear)
	}
	if afterShop.BaseEngineerDaysPerYear != beforeShop.BaseEngineerDaysPerYear {
		t.Fatalf("mutating dwelling's rate changed shop's rate: before=%d after=%d", beforeShop.BaseEngineerDaysPerYear, afterShop.BaseEngineerDaysPerYear)
	}
}

// TestLoadDefaultLoadsRealData proves the committed data/maintenance.json is
// itself schema-valid and loads with the full class vocabulary (AC-16's
// data-file shape, checked against the real file rather than a fixture).
func TestLoadDefaultLoadsRealData(t *testing.T) {
	a, err := LoadDefault("test")
	if err != nil {
		t.Fatalf("LoadDefault on the committed data file: %v", err)
	}
	if len(a.cfg.Classes) < 2 {
		t.Fatalf("committed data file defines %d classes, want at least 2", len(a.cfg.Classes))
	}
	// The eight zone types (the class stand-in vocabulary) must all resolve.
	for _, c := range []Class{"dwelling", "shop", "office", "entertainment", "farming", "manufacturing", "heavy_industry", "mining"} {
		if !a.cfg.classKnown(c) {
			t.Fatalf("committed data file is missing the zone-type class %q", c)
		}
	}
}

// TestMalformedDataRejected proves AC-12: a class missing its rate produces a
// registry-sourced load-time error, never a silent default substitution.
func TestMalformedDataRejected(t *testing.T) {
	body := `{
	  "version": 1,
	  "crewCostPerEngineerDay": 100,
	  "contractorCostPerEngineerDay": 300,
	  "classes": {
	    "dwelling": {"lifetimeYears": 50, "disclosure": "placeholder pending Aaron's balance pass"},
	    "shop": {"engineerDaysPerYear": 8, "lifetimeYears": 40, "disclosure": "placeholder pending Aaron's balance pass"}
	  }
	}`
	a, err := loadFixture(t, body)
	if a != nil {
		t.Fatal("a malformed data file must not produce an API (no default-substituted rate)")
	}
	wantCode(t, err, ErrMaintenanceDataInvalid)
}

// TestInvalidMaintenanceDataRejected proves AC-12's remaining enumerated
// failures: a negative lifetime, an unrecognised class string, and a
// rate-defined-but-not-resolvable vocabulary each fail at load time with a
// registry-sourced error and no default substitution.
func TestInvalidMaintenanceDataRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "negative lifetime",
			body: `{
			  "version": 1,
			  "crewCostPerEngineerDay": 100,
			  "contractorCostPerEngineerDay": 300,
			  "classes": {
			    "dwelling": {"engineerDaysPerYear": 10, "lifetimeYears": -5, "disclosure": "placeholder pending Aaron's balance pass"},
			    "shop": {"engineerDaysPerYear": 8, "lifetimeYears": 40, "disclosure": "placeholder pending Aaron's balance pass"}
			  }
			}`,
		},
		{
			name: "unrecognised class string",
			body: `{
			  "version": 1,
			  "crewCostPerEngineerDay": 100,
			  "contractorCostPerEngineerDay": 300,
			  "classes": {
			    "bad class!": {"engineerDaysPerYear": 10, "lifetimeYears": 50, "disclosure": "placeholder pending Aaron's balance pass"},
			    "shop": {"engineerDaysPerYear": 8, "lifetimeYears": 40, "disclosure": "placeholder pending Aaron's balance pass"}
			  }
			}`,
		},
		{
			name: "fewer than two classes",
			body: `{
			  "version": 1,
			  "crewCostPerEngineerDay": 100,
			  "contractorCostPerEngineerDay": 300,
			  "classes": {
			    "dwelling": {"engineerDaysPerYear": 10, "lifetimeYears": 50, "disclosure": "placeholder pending Aaron's balance pass"}
			  }
			}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := loadFixture(t, tc.body)
			if a != nil {
				t.Fatal("a malformed data file must not produce an API (no default substitution)")
			}
			wantCode(t, err, ErrMaintenanceDataInvalid)
		})
	}
}
