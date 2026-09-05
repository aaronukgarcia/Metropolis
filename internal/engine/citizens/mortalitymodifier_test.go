package citizens

import (
	"math"
	"testing"
)

// TestSetMortalityModifier_DefaultNeutral proves the documented no-op:
// a CitizensAPI that never calls SetMortalityModifier (every existing
// NewCitizensAPI caller) produces the exact same ColdPassParams as one that
// explicitly wires a neutral 1.0 getter (MOD-034's downstream-effect
// application seam, registry.go's SetMortalityModifier/coldParamsLocked).
func TestSetMortalityModifier_DefaultNeutral(t *testing.T) {
	const seed = uint64(777)
	recs := []ColdRecord{mkRecord(1, 0), mkRecord(2, 0), mkRecord(3, 0)}

	unwired, err := NewCitizensAPI(seed, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := unwired.SeedColdRecords(recs, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	unwiredParams := unwired.coldParamsLocked("corr")

	wiredNeutral, err := NewCitizensAPI(seed, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := wiredNeutral.SeedColdRecords(recs, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := wiredNeutral.SetMortalityModifier(func() float64 { return 1.0 }, "corr"); err != nil {
		t.Fatalf("SetMortalityModifier: %v", err)
	}
	wiredParams := wiredNeutral.coldParamsLocked("corr")

	if unwiredParams.MortalityMultiplier != wiredParams.MortalityMultiplier {
		t.Fatalf("neutral (1.0) modifier changed MortalityMultiplier: unwired=%v wired=%v",
			unwiredParams.MortalityMultiplier, wiredParams.MortalityMultiplier)
	}
}

// TestSetMortalityModifier_FoldsMultiplicatively proves MOD-034's seam
// contract: coldParamsLocked folds the injected getter's return value into
// the sample-derived MortalityMultiplier by straight multiplication, never
// a replacement or an additive term — two otherwise-identical CitizensAPI
// instances (same seed, same seeded cold records, so the sample-derived
// base multiplier is identical) differing ONLY in whether a 2.5x getter is
// wired must differ by exactly that factor.
func TestSetMortalityModifier_FoldsMultiplicatively(t *testing.T) {
	const seed = uint64(778)
	const factor = 2.5
	recs := []ColdRecord{mkRecord(1, 0), mkRecord(2, 0), mkRecord(3, 0), mkRecord(4, 0)}

	base, err := NewCitizensAPI(seed, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := base.SeedColdRecords(recs, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	baseParams := base.coldParamsLocked("corr")

	scaled, err := NewCitizensAPI(seed, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := scaled.SeedColdRecords(recs, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := scaled.SetMortalityModifier(func() float64 { return factor }, "corr"); err != nil {
		t.Fatalf("SetMortalityModifier: %v", err)
	}
	scaledParams := scaled.coldParamsLocked("corr")

	want := baseParams.MortalityMultiplier * factor
	if math.Abs(scaledParams.MortalityMultiplier-want) > 1e-9 {
		t.Fatalf("MortalityMultiplier with %vx modifier = %v, want %v (base %v x %v)",
			factor, scaledParams.MortalityMultiplier, want, baseParams.MortalityMultiplier, factor)
	}

	// Passing nil restores the documented default (no-op): re-wiring the
	// same *CitizensAPI with a nil getter must reproduce the UNSCALED base
	// value exactly, proving nil is a real reset and not merely "leave
	// whatever was last set".
	if err := scaled.SetMortalityModifier(nil, "corr"); err != nil {
		t.Fatalf("SetMortalityModifier(nil): %v", err)
	}
	resetParams := scaled.coldParamsLocked("corr")
	if resetParams.MortalityMultiplier != baseParams.MortalityMultiplier {
		t.Fatalf("MortalityMultiplier after SetMortalityModifier(nil) = %v, want base %v",
			resetParams.MortalityMultiplier, baseParams.MortalityMultiplier)
	}
}

// TestSetMortalityModifier_CopiedValueRejected proves the SEC-020 copy
// guard: SetMortalityModifier on a struct-copied *CitizensAPI must fail
// closed with a registry-sourced error and must NOT mutate the copy's
// mortalityModifier field (mirrors attack_feat087_inc3_handoff_test.go's
// identical SetDeathDrainCapacity copy-guard proof).
func TestSetMortalityModifier_CopiedValueRejected(t *testing.T) {
	api, err := NewCitizensAPI(999, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	apiCopy := citizensByteCopy(api) // shared helper from copyguard_test.go; see its doc comment
	if err := apiCopy.SetMortalityModifier(func() float64 { return 2.0 }, "corr"); err == nil {
		t.Fatalf("CitizensAPI.SetMortalityModifier on a struct copy returned nil error")
	}
	if apiCopy.mortalityModifier != nil {
		t.Fatalf("CitizensAPI.SetMortalityModifier mutated a struct copy's mortalityModifier field despite returning an error")
	}
}
