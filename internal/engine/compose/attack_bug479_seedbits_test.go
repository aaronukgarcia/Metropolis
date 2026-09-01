package compose

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-479 independent destructive round (Opus r1, 2026-09-01) — coverage
// gap found by mutation.
//
// Composition seeds are uint64 (compositionState.seed, compose.go) and the
// save layer's Header.WorldSeed is int64, so save_wire.go bridges them with
// a plain int64(...) conversion on BOTH the write side (Save's
// save.Context.WorldSeed) and the read side (Load's
// save.WithExpectedWorldSeed). That conversion is bijective and therefore
// correct — but nothing in the BUG-479 suite as written could tell: every
// fixture seed in it (bug479SeedA/B = 479001/479002, roundTripSeed) fits in
// 20 bits, so the mutation
//
//	save.WithExpectedWorldSeed(int64(int32(c.state.seed)))
//
// left the ENTIRE internal/engine/compose suite green (measured, this
// round). A truncating or sign-losing bridge would silently re-open exactly
// the defect BUG-479 closed — two cities whose seeds differ only in their
// high bits would load each other's bundles with no error at all — so these
// two cases pin the full 64-bit width of the comparison.

// Seeds sharing identical low 32 bits, differing only above bit 31: kills
// any int32/uint32 truncation of the seed on either side of the bridge.
const (
	bug479SeedHighA = uint64(0x0000_0001_0007_4F19)
	bug479SeedHighB = uint64(0x0000_0002_0007_4F19)
)

// Seeds differing ONLY in bit 63 (int64's sign bit): kills any comparison
// that drops or normalises the sign when crossing uint64 <-> int64.
const (
	bug479SeedSignA = uint64(0x8000_0000_0007_4F19)
	bug479SeedSignB = uint64(0x0000_0000_0007_4F19)
)

// assertCrossSeedLoadRefused saves a bundle from a composition seeded with
// saveSeed and proves a composition seeded with loadSeed refuses it with
// save.ErrSaveSeedMismatch, leaving its state digest identical to a
// pristine never-loaded composition on the same seed.
func assertCrossSeedLoadRefused(t *testing.T, saveSeed, loadSeed uint64) {
	t.Helper()
	if saveSeed == loadSeed {
		t.Fatalf("fixture bug: saveSeed and loadSeed are both %#x", saveSeed)
	}

	_, compSave := buildCompositionWithSeed(t, saveSeed)
	dir := t.TempDir()
	if err := compSave.Save(dir); err != nil {
		t.Fatalf("Save under seed %#x: %v", saveSeed, err)
	}

	_, pristine := buildCompositionWithSeed(t, loadSeed)
	pristineDigest := pristine.StateDigest()

	_, compLoad := buildCompositionWithSeed(t, loadSeed)
	err := compLoad.Load(dir)
	if err == nil {
		t.Fatalf("a bundle saved under seed %#x loaded into a seed-%#x composition with NO error — the seed comparison is losing the bits these two seeds differ in", saveSeed, loadSeed)
	}
	if !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("Load error = %v, want %s", err, save.ErrSaveSeedMismatch)
	}
	if got := compLoad.StateDigest(); got != pristineDigest {
		t.Fatalf("composition state changed on a refused load: pristine=%x post-refusal=%x", pristineDigest, got)
	}
}

// TestAttackBUG479_SeedsDifferingOnlyAboveBit31_Refused: the int32
// truncation mutation survives the whole existing suite; it does not
// survive this.
func TestAttackBUG479_SeedsDifferingOnlyAboveBit31_Refused(t *testing.T) {
	if bug479SeedHighA&0xFFFFFFFF != bug479SeedHighB&0xFFFFFFFF {
		t.Fatalf("fixture bug: the two high-bit seeds must share their low 32 bits (%#x vs %#x)", bug479SeedHighA&0xFFFFFFFF, bug479SeedHighB&0xFFFFFFFF)
	}
	assertCrossSeedLoadRefused(t, bug479SeedHighA, bug479SeedHighB)
}

// TestAttackBUG479_SeedsDifferingOnlyInSignBit_Refused: the two seeds are
// numerically identical in every bit except int64's sign bit, so any
// unsigned/absolute normalisation of the comparison accepts the foreign
// bundle.
func TestAttackBUG479_SeedsDifferingOnlyInSignBit_Refused(t *testing.T) {
	if bug479SeedHighA^bug479SeedHighB == 0 {
		t.Fatal("fixture bug: sign-bit seeds are equal")
	}
	signA := bug479SeedSignA
	if int64(signA) >= 0 {
		t.Fatalf("fixture bug: seed %#x must be NEGATIVE once cast to int64 (it is the top-bit-set case)", bug479SeedSignA)
	}
	assertCrossSeedLoadRefused(t, bug479SeedSignA, bug479SeedSignB)
}

// TestAttackBUG479_HighBitSeed_SameSeedStillRoundTrips is the
// prove-can-fail companion to the two refusals above: the SAME high-bit
// seed must still load its own bundle successfully. Without this, a
// comparison that simply refused every large seed would pass both
// refusal tests while breaking every real save on a 64-bit seed.
func TestAttackBUG479_HighBitSeed_SameSeedStillRoundTrips(t *testing.T) {
	eA, compA := buildCompositionWithSeed(t, bug479SeedHighA)
	driveMultiDomain(t, eA, compA)
	digestA := compA.StateDigest()

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, compB := buildCompositionWithSeed(t, bug479SeedHighA)
	if err := compB.Load(dir); err != nil {
		t.Fatalf("same (high-bit) seed Load refused: %v", err)
	}
	if got := compB.StateDigest(); got != digestA {
		t.Fatalf("same-seed Load did not round-trip StateDigest: A=%x B=%x", digestA, got)
	}
}
