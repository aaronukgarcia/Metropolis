package unlocks

import "testing"

// --- AC-6: exactly the twelve §22 categories ----------------------------

// TestTwelveCategoriesExactly asserts the loaded category set is EXACTLY
// §22's twelve named categories — same length AND same names. A build
// that loaded a 13th accidental category (e.g. a copy-paste duplicate
// under a different key) would fail the length check; a build that missed
// one would fail the membership check. The "at least 12" false-pass the
// AC warns about cannot pass here.
func TestTwelveCategoriesExactly(t *testing.T) {
	api := realAPI(t)

	want := []string{
		"Roads", "Electricity", "Water & Gas", "Health & Deathcare",
		"Education", "Fire", "Police", "Garbage", "Parks & Rec",
		"Transport", "Communications", "Welfare",
	}

	got := api.Categories()
	if len(got) != len(want) {
		t.Fatalf("loaded %d categories, want exactly %d (§22's twelve)", len(got), len(want))
	}
	gotSet := make(map[string]bool, len(got))
	for _, name := range got {
		gotSet[name] = true
	}
	for _, name := range want {
		if !gotSet[name] {
			t.Errorf("category %q is missing from the loaded set (§22's twelve)", name)
		}
	}
}

// TestCategoriesReturnsDefensiveCopy proves the exported slice is a copy,
// not the internal index (weakness pattern #1): mutating the returned
// slice must not change the API's own category set.
func TestCategoriesReturnsDefensiveCopy(t *testing.T) {
	api := realAPI(t)
	before := api.Categories()

	got := api.Categories()
	got[0] = "CORRUPTED"
	after := api.Categories()
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("mutating the returned Categories slice changed the internal index at %d", i)
		}
	}
}

// --- AC-7: spend-to-unlock ----------------------------------------------

// TestSpendDevelopmentPointsSuccess spends DP on a real tier-2 node and
// asserts the balance debits and the node unlocks.
func TestSpendDevelopmentPointsSuccess(t *testing.T) {
	api, _ := realAPIWithFinance(t)
	if _, err := api.AdvancePopulation(100, testCorrelationID()); err != nil {
		t.Fatalf("AdvancePopulation(100): %v", err)
	}

	node := api.SignatureUnlocks(2)[0]
	beforeDP := api.DevelopmentPoints()
	cost := int64(api.nodes[node].DPCost)

	if err := api.SpendDevelopmentPoints(node, testCorrelationID()); err != nil {
		t.Fatalf("SpendDevelopmentPoints(%s): %v", node, err)
	}
	if api.DevelopmentPoints() != beforeDP-cost {
		t.Errorf("DevelopmentPoints = %d, want %d (debited by the node's dpCost)", api.DevelopmentPoints(), beforeDP-cost)
	}
	if !api.IsNodeUnlocked(node) {
		t.Errorf("node %q not unlocked after SpendDevelopmentPoints", node)
	}
}

// TestSpendInsufficientDPRejected rejects a spend that exceeds the
// balance. It force-reaches tier 12 (which grants no DP) and then tries
// to spend on a tier-12 node whose cost (12) exceeds the zero balance.
func TestSpendInsufficientDPRejected(t *testing.T) {
	api := realAPI(t)
	if err := api.SetDebugGate(func(string) error { return nil }); err != nil {
		t.Fatalf("SetDebugGate: %v", err)
	}
	if err := api.ForceUnlock(ForceTarget{Tier: 12}, testCorrelationID()); err != nil {
		t.Fatalf("ForceUnlock(tier 12): %v", err)
	}

	node := api.SignatureUnlocks(12)[0]
	err := api.SpendDevelopmentPoints(node, testCorrelationID())
	assertCode(t, err, ErrInsufficientDP)
	if api.IsNodeUnlocked(node) {
		t.Errorf("node %q unlocked despite insufficient DP", node)
	}
}

// TestSpendMissingTierPrerequisiteRejected rejects a spend on a node whose
// milestone prerequisite has not been reached.
func TestSpendMissingTierPrerequisiteRejected(t *testing.T) {
	api, _ := realAPIWithFinance(t)
	if _, err := api.AdvancePopulation(0, testCorrelationID()); err != nil { // tier 1
		t.Fatalf("AdvancePopulation(0): %v", err)
	}

	// A tier-5 node has prereqTier 5 > current tier 1.
	node := api.SignatureUnlocks(5)[0]
	err := api.SpendDevelopmentPoints(node, testCorrelationID())
	assertCode(t, err, ErrTierPrerequisite)
	if api.IsNodeUnlocked(node) {
		t.Errorf("node %q unlocked despite missing tier prerequisite", node)
	}
}

// TestSpendUnknownNodeRejected rejects a typo'd node id with a
// registry-sourced error, never a silent false negative (AC-12).
func TestSpendUnknownNodeRejected(t *testing.T) {
	api, _ := realAPIWithFinance(t)
	if _, err := api.AdvancePopulation(0, testCorrelationID()); err != nil {
		t.Fatalf("AdvancePopulation(0): %v", err)
	}
	err := api.SpendDevelopmentPoints("roads_typo_that_does_not_exist", testCorrelationID())
	assertCode(t, err, ErrUnregisteredGate)
}

// TestSpendTwiceRejected proves double-spend is a loud error, not a silent
// DP drain (GR#1).
func TestSpendTwiceRejected(t *testing.T) {
	api, _ := realAPIWithFinance(t)
	if _, err := api.AdvancePopulation(100, testCorrelationID()); err != nil {
		t.Fatalf("AdvancePopulation(100): %v", err)
	}
	node := api.SignatureUnlocks(2)[0]
	if err := api.SpendDevelopmentPoints(node, testCorrelationID()); err != nil {
		t.Fatalf("first SpendDevelopmentPoints: %v", err)
	}
	dpAfter := api.DevelopmentPoints()
	err := api.SpendDevelopmentPoints(node, testCorrelationID())
	assertCode(t, err, ErrNodeAlreadyUnlocked)
	if api.DevelopmentPoints() != dpAfter {
		t.Errorf("DevelopmentPoints changed on a rejected double-spend: %d -> %d", dpAfter, api.DevelopmentPoints())
	}
}

// --- AC-8: divergent toolkits at the same tier --------------------------

// TestDivergentToolkits builds two independently-simulated worlds at the
// same milestone tier with different DP spend histories and asserts their
// IsUnlocked answers differ for at least one node — proving "two players
// at the same tier own different toolkits" is a real, queryable property.
func TestDivergentToolkits(t *testing.T) {
	build := func(spendNode string) *UnlocksAPI {
		api, _ := realAPIWithFinance(t)
		if _, err := api.AdvancePopulation(100, testCorrelationID()); err != nil { // tier 2
			t.Fatalf("AdvancePopulation(100): %v", err)
		}
		if err := api.SpendDevelopmentPoints(spendNode, testCorrelationID()); err != nil {
			t.Fatalf("SpendDevelopmentPoints(%s): %v", spendNode, err)
		}
		return api
	}

	// Two distinct tier-2 nodes, derived from data (GR#15).
	sigs := realAPI(t).SignatureUnlocks(2)
	if len(sigs) < 2 {
		t.Fatalf("SignatureUnlocks(2) has %d nodes, need at least 2 for a divergent-toolkit test", len(sigs))
	}
	nodeA, nodeB := sigs[0], sigs[1]

	apiA := build(nodeA)
	apiB := build(nodeB)

	if apiA.CurrentTier() != apiB.CurrentTier() {
		t.Fatalf("the two worlds are not at the same tier: %d vs %d", apiA.CurrentTier(), apiB.CurrentTier())
	}
	if apiA.IsUnlocked(Gate{NodeID: nodeA}) != true {
		t.Errorf("world A did not unlock its chosen node %q", nodeA)
	}
	if apiB.IsUnlocked(Gate{NodeID: nodeB}) != true {
		t.Errorf("world B did not unlock its chosen node %q", nodeB)
	}
	if apiA.IsUnlocked(Gate{NodeID: nodeB}) != false {
		t.Errorf("world A reports node %q unlocked but it spent on %q — toolkits not divergent", nodeB, nodeA)
	}
	if apiB.IsUnlocked(Gate{NodeID: nodeA}) != false {
		t.Errorf("world B reports node %q unlocked but it spent on %q — toolkits not divergent", nodeA, nodeB)
	}
}
