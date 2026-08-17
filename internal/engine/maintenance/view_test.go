package maintenance

import "testing"

// TestPerInstanceViewsAreDistinct proves AC-1: every placed structure carries
// its OWN MaintenanceView, keyed to its structure identity — not one shared
// per class. Two same-class structures placed at different months hold
// distinct ages, and reading one never moves the other's record.
func TestPerInstanceViewsAreDistinct(t *testing.T) {
	a := newTestAPI(t)

	if err := a.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register 1: %v", err)
	}
	if err := a.AdvanceMonth(12, "test"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := a.Register(2, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register 2: %v", err)
	}
	if err := a.AdvanceMonth(12, "test"); err != nil {
		t.Fatalf("advance 2: %v", err)
	}

	v1, err := a.View(1, "test")
	if err != nil {
		t.Fatalf("view 1: %v", err)
	}
	v2, err := a.View(2, "test")
	if err != nil {
		t.Fatalf("view 2: %v", err)
	}

	// Structure 1 was placed at month 0 (age 24); structure 2 at month 12
	// (age 12). A map[ZoneType]MaintenanceView keyed by class would give both
	// the same age — this is the exact false pass AC-1 warns about.
	if v1.AgeMonths == v2.AgeMonths {
		t.Fatalf("same-class structures placed at different months must have distinct ages, both = %d", v1.AgeMonths)
	}
	if v1.AgeMonths != 24 || v2.AgeMonths != 12 {
		t.Fatalf("ages = (%d, %d), want (24, 12)", v1.AgeMonths, v2.AgeMonths)
	}

	// Reading/mutating one record must leave the other byte-identical: the
	// two views are independent records, not one shared per class.
	before := v2
	if _, err := a.View(1, "test"); err != nil {
		t.Fatalf("re-read view 1: %v", err)
	}
	after, err := a.View(2, "test")
	if err != nil {
		t.Fatalf("re-read view 2: %v", err)
	}
	if after != before {
		t.Fatalf("viewing structure 1 moved structure 2's record:\nbefore=%+v\nafter =%+v", before, after)
	}
}

// TestSameClassInstancesHoldDistinctAges proves the same AC-1 invariant from
// the other direction: two otherwise-identical objects of the same class
// placed at different times are two different maintenance realities, not one
// shared row.
func TestSameClassInstancesHoldDistinctAges(t *testing.T) {
	a := newTestAPI(t)
	if err := a.Register(10, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register 10: %v", err)
	}
	if err := a.AdvanceMonth(120, "test"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := a.Register(11, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register 11: %v", err)
	}

	old, _ := a.View(10, "test")
	young, _ := a.View(11, "test")
	if old.AgeMonths != 120 || young.AgeMonths != 0 {
		t.Fatalf("ages = (%d, %d), want (120, 0)", old.AgeMonths, young.AgeMonths)
	}
	if old.AgeMonths == young.AgeMonths {
		t.Fatal("two same-class structures placed at different times must hold distinct ages")
	}
}

// TestMonotonicEfficiencyDecline proves AC-3: efficiency is a non-increasing
// function of age and per-instance repair demand is non-decreasing — never a
// reversal — as an instance ages from new to past its lifetime.
func TestMonotonicEfficiencyDecline(t *testing.T) {
	a := newTestAPI(t)
	if err := a.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register: %v", err)
	}

	prevEff := 2.0 // above the 1.0 maximum, so the first comparison always passes
	var prevDemand int64 = -1
	// Age from 0 to 800 months (past the 600-month lifetime) in 50-month steps.
	for step := 0; step <= 16; step++ {
		if err := a.AdvanceMonth(50, "test"); err != nil {
			t.Fatalf("advance %d: %v", step, err)
		}
		v, err := a.View(1, "test")
		if err != nil {
			t.Fatalf("view: %v", err)
		}
		if v.Efficiency > prevEff {
			t.Fatalf("efficiency increased at age %d months: %v -> %v (must be non-increasing)", v.AgeMonths, prevEff, v.Efficiency)
		}
		if v.RepairDemandPerYear < prevDemand {
			t.Fatalf("repair demand decreased at age %d months: %d -> %d (must be non-decreasing)", v.AgeMonths, prevDemand, v.RepairDemandPerYear)
		}
		prevEff, prevDemand = v.Efficiency, v.RepairDemandPerYear
	}
}

// TestNewNeedsLessRepairThanOld proves the load-bearing direction of AC-3:
// a newer instance has lower repair demand (and higher efficiency) than an
// otherwise-identical older instance of the same class.
func TestNewNeedsLessRepairThanOld(t *testing.T) {
	a := newTestAPI(t)
	if err := a.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register old: %v", err)
	}
	if err := a.AdvanceMonth(600, "test"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := a.Register(2, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register new: %v", err)
	}

	old, _ := a.View(1, "test")
	young, _ := a.View(2, "test")
	if young.RepairDemandPerYear > old.RepairDemandPerYear {
		t.Fatalf("newer instance demands more repair than older: new=%d old=%d", young.RepairDemandPerYear, old.RepairDemandPerYear)
	}
	if young.Efficiency < old.Efficiency {
		t.Fatalf("newer instance has lower efficiency than older: new=%v old=%v", young.Efficiency, old.Efficiency)
	}
}

// TestLifetimeRefitFlag proves AC-4: an instance whose age has reached its
// lifetime transitions to a distinct, queryable NeedsRefit state and stays
// there, while an otherwise-identical instance just under the lifetime stays
// unflagged. The flag — not merely worsening numbers — is the decision point.
func TestLifetimeRefitFlag(t *testing.T) {
	a := newTestAPI(t)
	// dwelling lifetime is 50 years = 600 months (testConfig).
	if err := a.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register 1: %v", err)
	}

	// Just under lifetime: 599 months — must NOT be flagged.
	if err := a.AdvanceMonth(599, "test"); err != nil {
		t.Fatalf("advance to 599: %v", err)
	}
	v, err := a.View(1, "test")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if v.NeedsRefit {
		t.Fatalf("instance at age %d months (just under 600) must not be flagged for refit", v.AgeMonths)
	}

	// Exactly lifetime: 600 months — the flag flips.
	if err := a.AdvanceMonth(1, "test"); err != nil {
		t.Fatalf("advance to 600: %v", err)
	}
	v, err = a.View(1, "test")
	if err != nil {
		t.Fatalf("view at lifetime: %v", err)
	}
	if !v.NeedsRefit {
		t.Fatalf("instance at exactly its lifetime (age %d) must be flagged for refit", v.AgeMonths)
	}

	// Place a second, otherwise-identical dwelling now, then age the world
	// until the second is just under its own lifetime.
	if err := a.Register(2, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register 2: %v", err)
	}
	if err := a.AdvanceMonth(599, "test"); err != nil {
		t.Fatalf("advance 2: %v", err)
	}
	old, _ := a.View(1, "test")   // age 1199 — beyond lifetime
	young, _ := a.View(2, "test") // age 599 — just under lifetime
	if !old.NeedsRefit {
		t.Fatalf("instance beyond its lifetime must remain flagged (age %d)", old.AgeMonths)
	}
	if young.NeedsRefit {
		t.Fatalf("otherwise-identical instance just under lifetime (age %d) must stay unflagged", young.AgeMonths)
	}
}
