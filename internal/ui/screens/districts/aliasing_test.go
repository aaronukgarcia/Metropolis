package districts

// Independent-destructive addition (GR#23, checklist item 7): the built
// copy-guard sweep (copyguard_test.go) proves accessors reject a struct-
// copied *Screen but never proves the *values* they hand back are
// defensive copies. Screen.Districts() and Screen.TaxSettings() both build
// a fresh slice and copy() into it (screen.go), which is the right shape --
// this test proves that shape actually holds by aliasing-attacking it: take
// a returned slice, mutate an element in place, re-read via the accessor,
// and assert the screen's internal state is unaffected. A regression to
// "return s.taxSettings directly" (dropping the copy() call) would let a
// caller silently corrupt the screen's authoritative, engine-confirmed
// state -- exactly the class of bug GR#20's copy-guard convention exists to
// prevent.

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func TestTaxSettings_AccessorReturnsDefensiveCopy(t *testing.T) {
	s := New("corr-alias")
	s.BindSubscription("sub-1")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: mustJSON(t, fullPatch())})

	got, ok := s.TaxSettings()
	if !ok || len(got) == 0 {
		t.Fatal("setup: TaxSettings have=false or empty after a valid delta")
	}

	// Mutate the caller's copy.
	original := got[0]
	got[0].Multiplier = 999999
	got[0].InstrumentLabel = "TAMPERED"

	// Re-read: the screen's internal state must be untouched by the caller's
	// mutation of the slice it was handed.
	again, ok := s.TaxSettings()
	if !ok {
		t.Fatal("TaxSettings have=false on re-read")
	}
	if again[0].Multiplier == 999999 || again[0].InstrumentLabel == "TAMPERED" {
		t.Fatalf("TaxSettings() leaked a mutable alias to internal state: re-read = %+v, want the untampered original %+v",
			again[0], original)
	}
}

func TestDistricts_AccessorReturnsDefensiveCopy(t *testing.T) {
	s := New("corr-alias-districts")
	s.BindSubscription("sub-1")
	districts := []wireDistrict{{DistrictID: "harbour", Name: "Harbour"}}
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: mustJSON(t, wirePatch{SchemaVersion: 1, Districts: &districts})})

	got, ok := s.Districts()
	if !ok || len(got) == 0 {
		t.Fatal("setup: Districts have=false or empty after a valid delta")
	}

	got[0].Name = "TAMPERED"
	got[0].DistrictID = "TAMPERED"

	again, ok := s.Districts()
	if !ok {
		t.Fatal("Districts have=false on re-read")
	}
	if again[0].Name == "TAMPERED" || again[0].DistrictID == "TAMPERED" {
		t.Fatalf("Districts() leaked a mutable alias to internal state: re-read = %+v", again[0])
	}
}
