package deathservices

import "testing"

// attack_bug720_reround_month_test.go — INDEPENDENT re-round pin for
// BUG-720 round F4's `>`-not-`!=` month comparison, asserted at THIS
// module's own API boundary rather than through compose. compose's F1
// truncation gate (RemainingHearseBudget <= 0 -> break) independently
// suppresses the hearse refund, so a compose-level test cannot distinguish
// `>` from `!=` at all; the dispensation channel has no such gate, so the
// `>` fix is genuinely load-bearing there. Both halves are pinned here.

// TestReroundBUG720_HearseMonthGoingBackwardsNeverRefunds proves the fix
// where it lives: with the month-scoped budget spent at month 5, a call at
// month 0 (the shape a plain Composition.Load produces) must transport
// NOTHING. With the pre-fix `!=` comparison this returns a whole free
// budget.
func TestReroundBUG720_HearseMonthGoingBackwardsNeverRefunds(t *testing.T) {
	d := mkAPI(t, nil)
	intakeN(t, d, 1, 5000, 0, false)
	budget, err := d.HearseMonthlyBudget("atk")
	if err != nil {
		t.Fatalf("HearseMonthlyBudget: %v", err)
	}
	awaiting, err := d.AwaitingSorted("atk")
	if err != nil {
		t.Fatalf("AwaitingSorted: %v", err)
	}
	moved, _, err := d.RunHearseTransport(awaiting, "cem-1", 5, "atk")
	if err != nil {
		t.Fatalf("RunHearseTransport(month 5): %v", err)
	}
	if int64(len(moved)) != budget {
		t.Fatalf("fixture: month 5 moved %d, want the whole budget %d", len(moved), budget)
	}
	// Same month again: exhausted.
	awaiting, _ = d.AwaitingSorted("atk")
	again, _, err := d.RunHearseTransport(awaiting, "cem-1", 5, "atk")
	if err != nil {
		t.Fatalf("RunHearseTransport(month 5 again): %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("fixture: budget not exhausted, moved %d more", len(again))
	}
	// Month goes BACKWARDS — the save-scum shape. Must move zero.
	awaiting, _ = d.AwaitingSorted("atk")
	back, _, err := d.RunHearseTransport(awaiting, "cem-1", 0, "atk")
	if err != nil {
		t.Fatalf("RunHearseTransport(month 0): %v", err)
	}
	if len(back) != 0 {
		t.Fatalf("BUG-720 F4 REGRESSED: a month going backwards (5 -> 0) refunded the hearse budget: %d bodies transported for free", len(back))
	}
}

// TestReroundBUG720_DispensationMonthGoingBackwardsNeverRefunds is the
// half compose's F1 gate does NOT mask: runDeathServices calls Dispense in
// a loop with no remaining-budget pre-read, so dispensationState's own
// resetMonthLocked is the only thing standing between a backwards month
// and a free emergency-throughput budget.
func TestReroundBUG720_DispensationMonthGoingBackwardsNeverRefunds(t *testing.T) {
	d := mkAPI(t, nil)
	intakeN(t, d, 1, 8000, 0, false)
	if err := d.SetDispensationActive(true, "atk"); err != nil {
		t.Fatalf("SetDispensationActive: %v", err)
	}
	// Drain month 5's whole dispensation budget.
	spent := 0
	for {
		awaiting, err := d.AwaitingSorted("atk")
		if err != nil {
			t.Fatalf("AwaitingSorted: %v", err)
		}
		got, err := d.Dispense(awaiting, 5, "atk")
		if err != nil {
			t.Fatalf("Dispense(month 5): %v", err)
		}
		if len(got) == 0 {
			break
		}
		spent += len(got)
	}
	if spent == 0 {
		t.Fatal("fixture: dispensation moved nothing at month 5")
	}
	// Month goes BACKWARDS. Must dispense nothing.
	awaiting, _ := d.AwaitingSorted("atk")
	back, err := d.Dispense(awaiting, 0, "atk")
	if err != nil {
		t.Fatalf("Dispense(month 0): %v", err)
	}
	if len(back) != 0 {
		t.Fatalf("BUG-720 F4 REGRESSED (dispensation): month 5 -> 0 refunded the dispensation budget: %d bodies dispensed for free (month-5 spend was %d)", len(back), spent)
	}
	// And a genuine advance past the watermark DOES resume.
	awaiting, _ = d.AwaitingSorted("atk")
	fresh, err := d.Dispense(awaiting, 6, "atk")
	if err != nil {
		t.Fatalf("Dispense(month 6): %v", err)
	}
	if len(fresh) == 0 {
		t.Fatal("DEAD BUDGET: month 6 (past the watermark) dispensed nothing")
	}
}
