package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-1972079947 — independent destructive round r1 (Opus).
//
// TestLoadAt_TickContinuity_AcrossMonthBoundary compares whole StateDigests.
// A mutation sweep of attract/participant.go showed that comparison is only
// actually SENSITIVE to nextMigrantID: zeroing the restored
// reputation.value, dropping reputation.hasBaseline, or dropping
// lastAdvancedMonth/hasAdvanced all leave the post-boundary digest
// unchanged, because ApplyMigration truncates net migration to a whole
// number of migrant PAIRS (migration.go: ClampInt64FromFloat then
// /migrantHouseholdSize), which absorbs the sub-person score delta a
// reputation-momentum difference produces at baseline-one's weights
// (reputation weight 0.15) — and because FallRate 0.8 re-converges the
// momentum within a month or two anyway. Those fields ARE covered by
// attract's own round-trip test and by
// TestSaveRoundTrip_PerModuleStateIsByteIdentical, but nothing asserted
// them across the LoadAt + continued-ticking path the item exists for.
//
// This test closes that: it compares engine.attract's OWN serialized state
// (all four persisted fields, byte-for-byte) between a LoadAt'd
// composition and a reference engine that never stopped, after both tick
// two further months. It fails on a reputation/momentum/counter divergence
// that the digest comparison would swallow.
func TestAttack_LoadAt_AttractOwnStateMatchesReferenceAcrossMonthBoundary(t *testing.T) {
	extraTicks := int64(2 * core.DailyTicksPerMonth)

	eRef, compRef := buildComposition(t)
	driveMultiDomain(t, eRef, compRef)
	if err := eRef.AdvanceTicks(errs.NewCorrelationID(), extraTicks); err != nil {
		t.Fatalf("AdvanceTicks (reference): %v", err)
	}

	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	// Precondition: the save point must carry NON-DEFAULT attract state, or
	// "restored correctly" would be indistinguishable from "never restored".
	atSave := attractStateJSON(t, compA)
	if atSave == freshAttractStateJSON(t) {
		t.Fatalf("test setup: attract state at the save point is still the fresh default (%s) -- this comparison would be vacuous", atSave)
	}
	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock (A): %v", err)
	}
	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	eB, compB := buildComposition(t)
	if err := compB.LoadAt(dir, clockA.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if got := attractStateJSON(t, compB); got != atSave {
		t.Fatalf("attract state did not round-trip through LoadAt:\n got=%s\nwant=%s", got, atSave)
	}
	if err := eB.AdvanceTicks(errs.NewCorrelationID(), extraTicks); err != nil {
		t.Fatalf("AdvanceTicks (loaded): %v", err)
	}

	got, want := attractStateJSON(t, compB), attractStateJSON(t, compRef)
	if got == atSave {
		t.Fatalf("attract state did not move after the resumed months (%s) -- the momentum/migrant-id path is not being exercised, so this comparison has no teeth", got)
	}
	if got != want {
		t.Fatalf("engine.attract's own state diverged from a never-stopped reference across a month boundary:\n loaded=%s\n    ref=%s", got, want)
	}
}

// attractStateJSON returns the exact bytes engine.attract's save
// participant would emit for a composition's live attract module — all four
// persisted fields, so a divergence in any one of them is visible.
func attractStateJSON(t *testing.T, comp *Composition) string {
	t.Helper()
	rec, ok, err := attract.NewSaveParticipant(comp.state.attract).Source()()
	if err != nil || !ok {
		t.Fatalf("attract Source: err=%v ok=%v", err, ok)
	}
	return string(rec.Data)
}

// freshAttractStateJSON is the emission of a never-driven attract module —
// the "not restored at all" baseline the precondition above rules out.
func freshAttractStateJSON(t *testing.T) string {
	t.Helper()
	_, comp := buildComposition(t)
	return attractStateJSON(t, comp)
}
