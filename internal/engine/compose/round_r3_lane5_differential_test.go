package compose

// FEAT-9995385620 round r3 (attacker opus-round-r3-rules-lane5): the
// declared remaining difference between DecideRuleVersions (the read-only
// pre-check) and restoreLatest (the real restore). The builder's claim is
// that the restore can only refuse for a rule version where the pre-check
// passed when snapshot versions go backwards, "which only tampering
// produces". This test builds a history WITHOUT tampering:
//
//   - snapshot versions go forwards (S1 at version 0, S2 at version 1);
//   - one AdvanceTicks append is lost (the documented cause of a
//     tail-inconsistency, snapshot.go RestoreLatestSnapshotOrGenesis doc:
//     "a lost journal frame (a failed durable append ...)"), simulated with
//     the same append-suppressing guard store the helpers already use;
//   - a later build retires version 0 (rules.OldestSupported = 1, the
//     designed retirement mechanism).
//
// The lost advance shifts S2's journal split, so S2's tail starts with a
// Build whose Zone is not in S2's state: replay rejects it (MET-G811) and
// the restore walks back to S1, whose version 0 is retired: MET-G5611.
// DecideRuleVersions never sees that: it judges S2's version and S2's tail
// (both version 1, both fine) and stops. So the pre-check passes, the host
// Wires (paging files) and repairs the journal, and only then is the city
// refused for its rule version.
//
// Without the retirement the same city wakes from S1 (asserted below), so
// this is not a city that was doomed anyway.

import (
	"context"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// buildLostAdvanceHistory returns a store holding S1 (version 0, tick 65)
// and S2 (version 1, tick 105) and a journal that lost one AdvanceTicks
// frame between them.
func buildLostAdvanceHistory(t *testing.T) persist.Store {
	t.Helper()
	store := persist.NewMemStore()
	rc := newRuleCity(t, store, 0)
	rc.sessionA(t)           // version 0, tick 65
	rc.snapshot(t, store, 5) // S1: version 0, tick 65
	if err := rc.c.PinRuleVersion(rules.RefuseOccupiedCell); err != nil {
		t.Fatal(err)
	}
	rc.guard.replaying.Store(true) // this append fails to land
	rc.advance(t, 10)              // tick 75, frame lost
	rc.guard.replaying.Store(false)
	c4 := protocol.CellRef{X: 4, Y: 3}
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: c4, ZoneType: "dwelling"})
	rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: c4, BuildingType: "dwelling"})
	rc.advance(t, 30)        // tick 105
	rc.snapshot(t, store, 5) // S2: version 1, tick 105
	c5 := protocol.CellRef{X: 5, Y: 3}
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: c5, ZoneType: "dwelling"})
	rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: c5, BuildingType: "dwelling"})
	rc.advance(t, 60) // construction completes; the shifted split falls inside this advance
	// First tail command after S2's (shifted) split: its building was built
	// in the frames the shifted split hands to S2, but S2's state was taken
	// before that build, so replay from S2 rejects it.
	rc.do(t, protocol.KindDemolish, protocol.DemolishPayload{Cell: c5})
	rc.advance(t, 10)
	return store
}

func restoreErr(t *testing.T, store persist.Store) error {
	t.Helper()
	rc := newRuleCity(t, store, -1)
	rc.guard.replaying.Store(true)
	_, err := RestoreLatestSnapshotOrGenesisReport(context.Background(), rc.e, rc.c, store, ruleTestCity)
	return err
}

func TestRoundR3Lane5_ControlWithoutRetirementWakes(t *testing.T) {
	rules.SetLiveVersionForTest(t, rules.RefuseOccupiedCell)
	store := buildLostAdvanceHistory(t)
	if err := restoreErr(t, store); err != nil {
		t.Fatalf("control: without retirement the city must wake from S1, got %v", err)
	}
}

func TestRoundR3Lane5_LostAdvanceThenRetirement_PrecheckMustAgreeWithRestore(t *testing.T) {
	rules.SetLiveVersionForTest(t, rules.RefuseOccupiedCell)
	store := buildLostAdvanceHistory(t)
	rules.SetOldestSupportedForTest(t, rules.RefuseOccupiedCell)

	pre := precheckT(t, store, ruleTestCity, ruleTestSeed)
	real := restoreErr(t, store)
	t.Logf("pre-check: %v", pre)
	t.Logf("restore:   %v", real)
	if IsRuleVersionRefusal(real) && !IsRuleVersionRefusal(pre) {
		t.Fatalf("BLOCKER: the restore refuses for its rule version (%v) but the pre-check passed, so the host Wires and repairs before the refusal", real)
	}
	if IsRuleVersionRefusal(pre) && real == nil {
		t.Fatalf("BLOCKER: the pre-check refuses (%v) a city the restore wakes", pre)
	}
}
