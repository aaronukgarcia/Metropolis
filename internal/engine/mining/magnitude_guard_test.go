package mining

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// This file is the SEC-219 regression suite: the two blight/minetype loaders
// validated FINITENESS ONLY on the two factors of the site-capacity product —
// Extraction.CapacityDays (data/mining.json) and OutputRate
// (data/minetypes.json). site.go's SiteExtraction computes
// capacity := params.OutputRate × b.cfg.Extraction.CapacityDays, so a single
// unbounded finite value (e.g. 1e308, a valid float64) overflowed the product
// to +Inf. That made the site INEXHAUSTIBLE — s.extracted >= s.capacity is
// never true, so two successive Extract calls both succeed — and leaked +Inf
// through the read-only ExtractionSite.Capacity snapshot. Both factors are now
// bounded to maxDataMagnitude (1e12), the same overflow guard the deposit
// loader already applies (SEC-208), so a hostile or corrupt data edit is
// rejected at load (all-or-nothing, GR#15) rather than silently degenerating
// the model (GR#1/GR#16).

// writeMutatedBlight loads the real data/mining.json, lets mutate edit its
// decoded JSON shape, and writes the result to a temp file whose path it
// returns (the blight-side mirror of minetype_test.go's writeMutatedMineTypes).
func writeMutatedBlight(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	b, err := os.ReadFile(realMiningPath(t))
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
	p := filepath.Join(t.TempDir(), "mining.json")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestExtractionCapacityOverflowRejectedAtLoad reproduces the end-to-end
// inexhaustible-site scenario from the SEC-219 verdict: capacityDays=1e308 and
// outputRate=1e308 each loaded clean pre-fix (finiteness-only validation), so
// SiteExtraction's capacity product overflowed to +Inf and the read-only
// SiteInfo().Capacity snapshot leaked +Inf. Post-fix both hostile magnitudes
// are rejected at load, all-or-nothing, so a +Inf capacity can never be
// produced. A value just above the bound (1e13) is rejected too, proving the
// guard is the exact maxDataMagnitude ceiling rather than a reject-+Inf-only
// special case.
func TestExtractionCapacityOverflowRejectedAtLoad(t *testing.T) {
	blightCases := []struct {
		name  string
		value float64
	}{
		{"capacity-days-1e13", 1e13},
		{"capacity-days-1e308", 1e308},
	}
	for _, tc := range blightCases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeMutatedBlight(t, func(m map[string]any) {
				m["extraction"].(map[string]any)["capacityDays"] = tc.value
			})
			cfg, err := LoadBlightConfig(path, cid())
			assertErrCode(t, err, ErrBlightDataInvalid)
			if len(cfg.ClassProfile) != 0 {
				t.Fatalf("overflowing capacityDays load returned a populated config (%d class profiles) — all-or-nothing", len(cfg.ClassProfile))
			}
		})
	}

	typeCases := []struct {
		name  string
		value float64
	}{
		{"output-rate-1e13", 1e13},
		{"output-rate-1e308", 1e308},
	}
	for _, tc := range typeCases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeMutatedMineTypes(t, func(m map[string]any) {
				setTypeField(m, "chalk", "outputRate", tc.value)
			})
			cat, err := LoadMineTypes(path, cid())
			assertErrCode(t, err, ErrMineTypeDataInvalid)
			if cat.Len() != 0 {
				t.Fatalf("overflowing outputRate load returned %d types — all-or-nothing", cat.Len())
			}
		})
	}
}

// TestNewBlightAPIRejectsOverflowCapacityDays holds a caller-constructed
// BlightConfig to the same bound the loader enforces (the SEC-208 lesson: the
// shared validateBlightConfig is the single source of truth, so the loader
// cannot be bypassed by building the config by hand). Pre-fix, NewBlightAPI
// accepted capacityDays=1e308 and returned a non-nil API whose first
// SiteExtraction would overflow capacity to +Inf.
func TestNewBlightAPIRejectsOverflowCapacityDays(t *testing.T) {
	base, err := LoadBlightConfig(realMiningPath(t), cid())
	if err != nil {
		t.Fatal(err)
	}
	base.Extraction.CapacityDays = 1e308

	b, err := NewBlightAPI(newWorld(t), base, cid())
	assertErrCode(t, err, ErrBlightDataInvalid)
	if b != nil {
		t.Fatalf("NewBlightAPI returned a non-nil API alongside a rejection — fail-closed means nil API")
	}
}
