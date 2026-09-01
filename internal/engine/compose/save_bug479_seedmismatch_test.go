package compose

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-479: Composition.Load/LoadAt never validated the bundle's
// WorldSeed against the loading composition's own seed, so a
// differently-seeded load was silently accepted and every seed-derived
// stateless draw (attract hash draws, det.Stream per-draw RNG) diverged
// from the saved trajectory with no error. This file proves the fix
// end-to-end through Composition.Load/LoadAt exactly as a real caller
// (cmd/metroserve, the snapshot-restore path) would exercise it;
// internal/engine/save/load_seedcheck_test.go proves the same mechanism
// one layer down, against Manager.Load directly.

// bug479SeedA/B are two distinct, arbitrary, fixed seeds — distinct from
// roundTripSeed (save_roundtrip_test.go) so this file's fixtures never
// collide with that suite's.
const (
	bug479SeedA = uint64(479001)
	bug479SeedB = uint64(479002)
)

func buildCompositionWithSeed(t *testing.T, seed uint64) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp
}

// TestBUG479_Load_SeedMismatch_RefusedAndCompositionUntouched is the
// headline fix: Save under seed A, Load into a fresh composition built
// with seed B, must be refused with save.ErrSaveSeedMismatch — and the
// seed-B composition's StateDigest after the refused Load must be
// IDENTICAL to a pristine, never-loaded seed-B composition's digest,
// proving the refusal happened before any participant's state was
// touched (not just that SOME state matches by coincidence).
func TestBUG479_Load_SeedMismatch_RefusedAndCompositionUntouched(t *testing.T) {
	eA, compA := buildCompositionWithSeed(t, bug479SeedA)
	driveMultiDomain(t, eA, compA)

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The pristine reference: a fresh seed-B composition that never
	// attempts any load at all.
	_, pristine := buildCompositionWithSeed(t, bug479SeedB)
	pristineDigest := pristine.StateDigest()

	// The composition under test: a SEPARATE fresh seed-B composition
	// that DOES attempt (and must be refused) the seed-A load.
	_, compB := buildCompositionWithSeed(t, bug479SeedB)
	preAttemptDigest := compB.StateDigest()
	if preAttemptDigest != pristineDigest {
		t.Fatalf("two independently-built fresh seed-%d compositions already disagree before any load was attempted (digest A=%x B=%x) -- fixture is not deterministic, fix the fixture before trusting this test", bug479SeedB, pristineDigest, preAttemptDigest)
	}

	err := compB.Load(dir)
	if err == nil {
		t.Fatal("Load of a seed-479001 bundle into a seed-479002 composition succeeded, want save.ErrSaveSeedMismatch")
	}
	if !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("Load error = %v, want code %s", err, save.ErrSaveSeedMismatch)
	}

	if got := compB.StateDigest(); got != pristineDigest {
		t.Fatalf("composition state CHANGED on a refused seed-mismatched load: pristine digest=%x, post-refusal digest=%x -- the refusal must leave every participant untouched", pristineDigest, got)
	}
}

// TestBUG479_Load_SeedMismatch_ProveCanFail proves the untouched-state
// assertion above has teeth: loading the SAME mismatched bundle with
// save.AllowSeedMismatch() (the deliberate opt-in) DOES change compB's
// digest away from pristine, so "digest unchanged" is a real signal of
// "load was refused", not a vacuous truth about StateDigest never moving.
func TestBUG479_Load_SeedMismatch_ProveCanFail(t *testing.T) {
	eA, compA := buildCompositionWithSeed(t, bug479SeedA)
	driveMultiDomain(t, eA, compA)

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, pristine := buildCompositionWithSeed(t, bug479SeedB)
	pristineDigest := pristine.StateDigest()

	_, compB := buildCompositionWithSeed(t, bug479SeedB)
	if err := compB.Load(dir, save.AllowSeedMismatch()); err != nil {
		t.Fatalf("Load with AllowSeedMismatch() failed: %v, want success", err)
	}
	if got := compB.StateDigest(); got == pristineDigest {
		t.Fatal("prove-can-fail: an AllowSeedMismatch() load that actually applied compA's driven state produced the SAME digest as a pristine untouched composition -- the untouched-state assertion in the mismatch test cannot detect a real difference")
	}
}

// TestBUG479_Load_SeedMatch_StillRoundTrips is a direct BUG-479
// regression companion to the existing save_roundtrip_test.go suite
// (which already exercises the same-seed path via buildComposition and
// therefore already proves this): a same-seed Load succeeds and the
// StateDigest round-trips exactly, i.e. the new seed check does not
// introduce any false-positive refusal on the ordinary path.
func TestBUG479_Load_SeedMatch_StillRoundTrips(t *testing.T) {
	eA, compA := buildCompositionWithSeed(t, bug479SeedA)
	driveMultiDomain(t, eA, compA)
	digestA := compA.StateDigest()

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, compB := buildCompositionWithSeed(t, bug479SeedA)
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := compB.StateDigest(); got != digestA {
		t.Fatalf("same-seed Load did NOT round-trip StateDigest: A=%x B=%x", digestA, got)
	}
}

// TestBUG479_LoadAt_SeedMismatch_Refused proves LoadAt inherits the same
// refusal (it delegates its state restore to Load — save_wire.go), and
// that the refusal happens before SeedClockForRestore ever runs: the
// engine clock on compB must still read its fresh, never-seeded state
// (LoadAt normally seeds it to `tick`).
func TestBUG479_LoadAt_SeedMismatch_Refused(t *testing.T) {
	eA, compA := buildCompositionWithSeed(t, bug479SeedA)
	driveMultiDomain(t, eA, compA)

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("eA.Clock: %v", err)
	}

	eB, compB := buildCompositionWithSeed(t, bug479SeedB)
	clockBBefore, err := eB.Clock()
	if err != nil {
		t.Fatalf("eB.Clock (pre-LoadAt): %v", err)
	}

	err = compB.LoadAt(dir, clockA.Tick())
	if err == nil {
		t.Fatal("LoadAt of a seed-mismatched bundle succeeded, want save.ErrSaveSeedMismatch")
	}
	if !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("LoadAt error = %v, want code %s", err, save.ErrSaveSeedMismatch)
	}

	clockBAfter, err := eB.Clock()
	if err != nil {
		t.Fatalf("eB.Clock (post-refused-LoadAt): %v", err)
	}
	if clockBAfter.Tick() != clockBBefore.Tick() {
		t.Fatalf("refused LoadAt still advanced/seeded the clock: before tick=%d, after tick=%d", clockBBefore.Tick(), clockBAfter.Tick())
	}
}

// TestBUG479_LoadAt_AllowSeedMismatch_Succeeds proves LoadAt forwards
// opts through to the underlying Load call, so the deliberate-reseed
// escape hatch (FEAT-1972079897's rules-change replay case) is available
// at the LoadAt entry point too, not just Load.
func TestBUG479_LoadAt_AllowSeedMismatch_Succeeds(t *testing.T) {
	eA, compA := buildCompositionWithSeed(t, bug479SeedA)
	driveMultiDomain(t, eA, compA)

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("eA.Clock: %v", err)
	}

	_, compB := buildCompositionWithSeed(t, bug479SeedB)
	if err := compB.LoadAt(dir, clockA.Tick(), save.AllowSeedMismatch()); err != nil {
		t.Fatalf("LoadAt with AllowSeedMismatch() failed: %v, want success", err)
	}
}
