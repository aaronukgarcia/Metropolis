package unlocks

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// --- AC-1: IsUnlocked gate check + mutation commands --------------------

// TestGateMilestoneTier drives the milestone half of the gate check.
func TestGateMilestoneTier(t *testing.T) {
	api, _ := realAPIWithFinance(t)

	if api.IsUnlocked(Gate{MilestoneTier: 1}) {
		t.Error("IsUnlocked(MilestoneTier 1) = true before any milestone crossing")
	}
	if _, err := api.AdvancePopulation(0, testCorrelationID()); err != nil { // tier 1
		t.Fatalf("AdvancePopulation(0): %v", err)
	}
	if !api.IsUnlocked(Gate{MilestoneTier: 1}) {
		t.Error("IsUnlocked(MilestoneTier 1) = false after crossing tier 1")
	}
	if api.IsUnlocked(Gate{MilestoneTier: 2}) {
		t.Error("IsUnlocked(MilestoneTier 2) = true before crossing tier 2")
	}
}

// TestGateRequiresAnyDP checks the data.catalogue "developmentPoint"
// boolean flag maps to "at least one DP spent" (see the logged ASM).
func TestGateRequiresAnyDP(t *testing.T) {
	api, _ := realAPIWithFinance(t)

	if api.IsUnlocked(Gate{RequiresDP: true}) {
		t.Error("IsUnlocked(RequiresDP) = true with zero DP spent")
	}
	if _, err := api.AdvancePopulation(100, testCorrelationID()); err != nil { // tier 2
		t.Fatalf("AdvancePopulation(100): %v", err)
	}
	if api.IsUnlocked(Gate{RequiresDP: true}) {
		t.Error("IsUnlocked(RequiresDP) = true after crossing but before spending any DP")
	}
	node := api.SignatureUnlocks(2)[0]
	if err := api.SpendDevelopmentPoints(node, testCorrelationID()); err != nil {
		t.Fatalf("SpendDevelopmentPoints: %v", err)
	}
	if !api.IsUnlocked(Gate{RequiresDP: true}) {
		t.Error("IsUnlocked(RequiresDP) = false after spending DP")
	}
}

// TestGateAchievement checks the achievement boolean gate (consumed as an
// injected boolean — AC's Out of scope).
func TestGateAchievement(t *testing.T) {
	api := realAPI(t)
	if api.IsUnlocked(Gate{RequiresAchievement: true, AchievementMet: false}) {
		t.Error("IsUnlocked(achievement, unmet) = true; an unmet achievement gate must fail")
	}
	if !api.IsUnlocked(Gate{RequiresAchievement: true, AchievementMet: true}) {
		t.Error("IsUnlocked(achievement, met) = false; a met achievement gate must pass")
	}
}

// --- AC-10: data.catalogue's unlock field is checkable ------------------

// TestCatalogueGate loads a real buildings.json entry's unlock gate and
// asserts IsUnlocked resolves it against the current milestone state.
func TestCatalogueGate(t *testing.T) {
	dir, err := data.ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	buildings, err := data.LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}

	// Pick an entry with a clean M-milestone gate (no conditional text).
	var entry data.BuildingEntry
	found := false
	for _, e := range buildings.Entries {
		if e.Unlock.Milestone != "" && e.Unlock.Conditional == "" {
			entry = e
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no buildings.json entry with a clean M-milestone gate found")
	}
	tier := milestoneTierFromString(entry.Unlock.Milestone)

	api, _ := realAPIWithFinance(t)

	gate := GateForCatalogue(entry.Unlock, false)
	if api.IsUnlocked(gate) {
		t.Fatalf("IsUnlocked(%s's unlock M%d) = true before any crossing", entry.ID, tier)
	}

	// Cross the entry's milestone tier; the gate must now pass (assuming
	// the entry also has no DP flag; entries with a DP flag need a spend).
	if _, err := api.AdvancePopulation(milestoneLadder[tier-1].Population, testCorrelationID()); err != nil {
		t.Fatalf("AdvancePopulation to tier %d: %v", tier, err)
	}
	if !api.IsUnlocked(gate) {
		t.Errorf("IsUnlocked(%s's unlock M%d) = false after crossing its milestone", entry.ID, tier)
	}
}

// --- AC-12: unregistered gate references are typed errors ---------------

// TestUnregisteredGateReturnsTypedError asserts an unregistered node id
// and an out-of-range tier are returned as a registry-sourced
// ErrUnregisteredGate — and specifically that the returned error carries
// that code rather than a silent "not unlocked" false negative (BUG-100's
// explicit assertion).
func TestUnregisteredGateReturnsTypedError(t *testing.T) {
	api := realAPI(t)

	// Unregistered node id.
	if _, err := api.CheckGate(Gate{NodeID: "no_such_node"}); err == nil {
		t.Error("CheckGate(unregistered node) returned nil error; want ErrUnregisteredGate, not a silent false")
	} else {
		assertCode(t, err, ErrUnregisteredGate)
	}

	// Out-of-range milestone tier.
	if _, err := api.CheckGate(Gate{MilestoneTier: 99}); err == nil {
		t.Error("CheckGate(tier 99) returned nil error; want ErrUnregisteredGate, not a silent false")
	} else {
		assertCode(t, err, ErrUnregisteredGate)
	}

	// CheckNodeUnlocked on a typo'd id.
	if _, err := api.CheckNodeUnlocked("no_such_node"); err == nil {
		t.Error("CheckNodeUnlocked(typo) returned nil error; want ErrUnregisteredGate")
	} else {
		assertCode(t, err, ErrUnregisteredGate)
	}
}

// TestGenuineGateFailureIsNotAnError proves the distinction AC-12 draws:
// a REAL node that exists but is not yet unlocked is a legitimate false,
// not an error — so the typed error is reserved for unregistered
// references only.
func TestGenuineGateFailureIsNotAnError(t *testing.T) {
	api := realAPI(t)

	node := api.SignatureUnlocks(7)[0] // a real node, not yet unlocked
	ok, err := api.CheckGate(Gate{NodeID: node})
	if err != nil {
		t.Fatalf("CheckGate(real un-unlocked node) returned error %v; want (false, nil)", err)
	}
	if ok {
		t.Errorf("CheckGate(real un-unlocked node) = true; want false (genuine gate failure, no error)")
	}
}
