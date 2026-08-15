package unlocks

import "testing"

// --- AC-4: crossing a threshold grants all four (five) effects ----------

// TestCrossMilestoneGrantsAllEffects crosses tier 1 (Wilderness, pop 0)
// and asserts every §4 grant: the tier becomes gate-passable (signature
// unlocks + loan uplift via MilestoneReached), the expansion-permit
// allowance increases, a cash award posts through engine.finance (US-7),
// and a Development-Point grant lands (§22). If any one effect were
// dropped, this test fails.
func TestCrossMilestoneGrantsAllEffects(t *testing.T) {
	api, f := realAPIWithFinance(t)

	beforeMoney := f.TotalMoneyInCirculation()

	crossed, err := api.AdvancePopulation(0, testCorrelationID())
	if err != nil {
		t.Fatalf("AdvancePopulation(0): %v", err)
	}
	if len(crossed) != 1 || crossed[0].Tier != 1 || crossed[0].Name != "Wilderness" {
		t.Fatalf("crossed = %+v, want exactly [Wilderness]", crossed)
	}

	// Effect 1 + 4: signature unlocks gate-passable and loan uplift —
	// both are MilestoneReached(1) turning true.
	if !api.MilestoneReached(1) {
		t.Error("MilestoneReached(1) = false after crossing tier 1; signature unlocks / loan facility not gated open")
	}
	if api.MilestoneReached(2) {
		t.Error("MilestoneReached(2) = true before crossing tier 2; the ladder advanced too far")
	}

	// Effect 2: expansion-permit allowance.
	if got := api.ExpansionPermits(); got != permitGrantPerMilestone {
		t.Errorf("ExpansionPermits = %d, want %d (one permit grant)", got, permitGrantPerMilestone)
	}

	// Effect 3: cash award posted through FinanceAPI.
	afterMoney := f.TotalMoneyInCirculation()
	if afterMoney-beforeMoney != cashAwardPerMilestone {
		t.Errorf("TotalMoneyInCirculation delta = %d, want %d (the cash award, US-7)", afterMoney-beforeMoney, int64(cashAwardPerMilestone))
	}
	// The award is drill-through-able as a milestone.award entry.
	if lines := f.LinesByCategory(categoryMilestoneAward); len(lines) == 0 {
		t.Error("no milestone.award ledger lines posted; the cash award is not drill-through-able (AC-11)")
	}

	// Effect 5 (§22): Development-Point grant.
	if got := api.DevelopmentPoints(); got != dpGrantPerMilestone {
		t.Errorf("DevelopmentPoints = %d, want %d (§22's DP grant)", got, dpGrantPerMilestone)
	}
}

// --- AC-5: monotonic higher-water mark, no regression -------------------

// TestNoRegressionHigherWaterMark crosses tier 2, then reduces population
// below the threshold (a Detroit-spiral emigration dip, §12), and asserts
// the tier and its unlocks remain at the higher-water-mark level. If a
// population dip revoked unlocks, this test fails.
func TestNoRegressionHigherWaterMark(t *testing.T) {
	api, _ := realAPIWithFinance(t)

	if _, err := api.AdvancePopulation(100, testCorrelationID()); err != nil {
		t.Fatalf("AdvancePopulation(100): %v", err)
	}
	if api.CurrentTier() != 2 {
		t.Fatalf("CurrentTier = %d, want 2 (Hamlet)", api.CurrentTier())
	}
	if !api.MilestoneReached(2) {
		t.Fatal("MilestoneReached(2) = false after crossing tier 2")
	}

	// Emigration dip below the 100-citizen threshold.
	dipped, err := api.AdvancePopulation(50, testCorrelationID())
	if err != nil {
		t.Fatalf("AdvancePopulation(50): %v", err)
	}
	if len(dipped) != 0 {
		t.Errorf("population dip crossed %d milestone(s), want 0", len(dipped))
	}
	if api.CurrentPopulation() != 50 {
		t.Errorf("CurrentPopulation = %d, want 50 (the dip is recorded)", api.CurrentPopulation())
	}
	if api.CurrentTier() != 2 {
		t.Errorf("CurrentTier = %d after dip, want 2 (higher-water mark, never downgraded)", api.CurrentTier())
	}
	if !api.MilestoneReached(2) {
		t.Error("MilestoneReached(2) = false after dip; previously-granted unlocks were revoked (AC-5)")
	}
}

// TestCrossMultipleTiersInOneCall crosses tiers 1-4 in a single
// AdvancePopulation(5000) and asserts the crossed list is in ascending
// tier order (determinism — AC-14's sorted-grant-order requirement).
func TestCrossMultipleTiersInOneCall(t *testing.T) {
	api, _ := realAPIWithFinance(t)

	crossed, err := api.AdvancePopulation(5_000, testCorrelationID())
	if err != nil {
		t.Fatalf("AdvancePopulation(5000): %v", err)
	}
	wantTiers := []int{1, 2, 3, 4}
	if len(crossed) != len(wantTiers) {
		t.Fatalf("crossed %d milestones, want %d", len(crossed), len(wantTiers))
	}
	for i, want := range wantTiers {
		if crossed[i].Tier != want {
			t.Fatalf("crossed[%d].Tier = %d, want %d (ascending order — GR#21)", i, crossed[i].Tier, want)
		}
	}
	if api.CurrentTier() != 4 {
		t.Errorf("CurrentTier = %d, want 4", api.CurrentTier())
	}
}

// --- AC-19: sprint-plan S4 exit gate — milestone tiers 1-4 gate --------

// TestTier1to4ExitGate is the composite proof the S4 exit gate names: it
// crosses §4's tiers 1-4 (Wilderness→0, Hamlet→100, Village→500, Small
// Town→5,000) in sequence within one run and, at each crossing, asserts
// (a) the tier's named signature unlocks become gate-passable, (b) a cash
// award posts, and (c) content gated behind a not-yet-reached tier
// (tier 5's expansion tiles) remains locked. The signature unlocks are
// derived from data/unlock_trees.json (SignatureUnlocks), never hardcoded
// (GR#15).
func TestTier1to4ExitGate(t *testing.T) {
	api, f := realAPIWithFinance(t)

	steps := []struct {
		population int64
		tier       int
	}{
		{population: 0, tier: 1},
		{population: 100, tier: 2},
		{population: 500, tier: 3},
		{population: 5_000, tier: 4},
	}

	for _, step := range steps {
		moneyBefore := f.TotalMoneyInCirculation()

		crossed, err := api.AdvancePopulation(step.population, testCorrelationID())
		if err != nil {
			t.Fatalf("AdvancePopulation(%d): %v", step.population, err)
		}
		if len(crossed) != 1 || crossed[0].Tier != step.tier {
			t.Fatalf("AdvancePopulation(%d) crossed %+v, want exactly [tier %d]", step.population, crossed, step.tier)
		}

		// (a) this tier's signature unlocks are now gate-passable: the
		// milestone gate is open, and every data-derived signature unlock
		// node at this tier has its milestone prerequisite satisfied.
		if !api.MilestoneReached(step.tier) {
			t.Errorf("tier %d: MilestoneReached(%d) = false after crossing", step.tier, step.tier)
		}
		signatures := api.SignatureUnlocks(step.tier)
		for _, id := range signatures {
			n := api.nodes[id]
			if api.CurrentTier() < n.PrereqTier {
				t.Errorf("tier %d: signature node %q has prereqTier %d > current tier %d — not gate-passable",
					step.tier, id, n.PrereqTier, api.CurrentTier())
			}
		}

		// (b) a cash award posts through FinanceAPI (AC-4).
		if delta := f.TotalMoneyInCirculation() - moneyBefore; delta != cashAwardPerMilestone {
			t.Errorf("tier %d: money delta = %d, want %d (cash award)", step.tier, delta, int64(cashAwardPerMilestone))
		}

		// (c) tier 5's expansion tiles remain locked at every tier-1..4
		// population — the not-yet-reached tier's gate stays closed.
		if api.MilestoneReached(5) {
			t.Errorf("tier %d: MilestoneReached(5) = true, but tier 5 (expansion tiles) must stay locked", step.tier)
		}
		if api.IsUnlocked(Gate{MilestoneTier: 5}) {
			t.Errorf("tier %d: IsUnlocked(MilestoneTier 5) = true; expansion tiles are gated behind tier 5", step.tier)
		}
	}

	if api.CurrentTier() != 4 {
		t.Fatalf("CurrentTier = %d, want 4 after the tier 1-4 sequence", api.CurrentTier())
	}
}

// TestSignatureUnlocksNamedTier4Examples pins the §4-named tier-4
// signature unlocks (grid connection, secondary school, bus routes, fire
// & police posts) to the data-derived set, so AC-19's literal examples
// are actually represented in unlock_trees.json rather than silently
// absent.
func TestSignatureUnlocksNamedTier4Examples(t *testing.T) {
	api := realAPI(t)
	sigs := api.SignatureUnlocks(4)

	// §4 tier 4: "Grid connection (Sellindge), secondary school, bus
	// routes, fire & police posts" — each maps to a data node id.
	want := map[string]bool{
		"electricity_grid_connection": true, // grid connection (Sellindge)
		"education_secondary_school":  true, // secondary school
		"transport_bus_routes":        true, // bus routes
		"fire_station":                true, // fire post
		"police_station":              true, // police post
	}
	got := make(map[string]bool, len(sigs))
	for _, id := range sigs {
		got[id] = true
	}
	for id := range want {
		if !got[id] {
			t.Errorf("tier-4 signature unlock %q is absent from SignatureUnlocks(4); §4's named tier-4 unlocks are not all in data", id)
		}
	}
}
