package compose

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-479 independent destructive round (Opus r1), attack: the option
// PRECEDENCE seam.
//
// save_wire.go's Composition.Load builds its option list as
//
//	append([]save.LoadOption{save.WithExpectedWorldSeed(c.state.seed)}, opts...)
//
// i.e. the composition's own expected seed is PREPENDED and the caller's
// opts come AFTER. save.resolveLoadOptions is last-write-wins, so a
// caller-supplied WithExpectedWorldSeed OVERRIDES the composition's own
// seed — a second route past the refusal that does NOT go through
// AllowSeedMismatch: a caller CAN bypass the composition's expectation
// by re-declaring the expected seed as the bundle's. save_wire.go's doc
// comment claimed the opposite ("there is no way to silently skip it")
// until the r2 round corrected it; the doc and this test now agree that
// the check cannot be skipped by OMISSION but CAN be overridden by a
// caller that explicitly names an option.
//
// This is NOT reachable from any wire/client input — Composition.Load's
// opts are a Go-only parameter and every in-tree caller (snapshot.go's
// LoadAt, cmd/metroserve via RestoreLatestSnapshotOrGenesis) passes
// none — so it is a documentation-accuracy finding, not a live bypass.
// This test PINS the actual behaviour so that if anyone later tightens
// precedence (e.g. by appending the composition's seed LAST, which would
// make the doc claim true), the change is a deliberate, visible one and
// not a silent semantic drift.
func TestAttackBUG479_CallerSuppliedExpectedSeedOverridesComposition(t *testing.T) {
	eA, compA := buildCompositionWithSeed(t, bug479SeedA)
	driveMultiDomain(t, eA, compA)

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Control: with no opts the seed-B composition refuses the seed-A
	// bundle (the fix working as designed).
	_, control := buildCompositionWithSeed(t, bug479SeedB)
	if err := control.Load(dir); !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("control Load error = %v, want %s -- the base fix must refuse before this attack means anything", err, save.ErrSaveSeedMismatch)
	}

	// Attack: the same seed-B composition, but the caller re-declares the
	// expected seed as the BUNDLE's seed. Last-write-wins means the
	// composition's own prepended expectation is overridden and the load
	// is accepted with no AllowSeedMismatch anywhere.
	_, victim := buildCompositionWithSeed(t, bug479SeedB)
	pristineDigest := victim.StateDigest()
	err := victim.Load(dir, save.WithExpectedWorldSeed(int64(bug479SeedA)))
	if err != nil {
		t.Fatalf("caller-overridden expected seed: Load returned %v; this test pins CURRENT behaviour (override accepted). If precedence was deliberately tightened so the composition's seed always wins, update this test and save_wire.go's doc comment together", err)
	}
	if got := victim.StateDigest(); got == pristineDigest {
		t.Fatal("caller-overridden Load reported success but the composition's state did not change -- the override did not actually apply the bundle, so this test is not measuring what it claims")
	}
}
