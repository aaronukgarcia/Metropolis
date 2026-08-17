package maintenance

import "testing"

// TestUnknownClassErrors proves AC-11: operating on an unknown class key
// returns a registry-sourced error and mutates no maintenance state — never a
// panic, never a silently-accepted no-op a caller could mistake for success.
func TestUnknownClassErrors(t *testing.T) {
	a := newTestAPI(t)

	if err := a.Register(1, "hospital", RegisterOptions{}, "test"); err != nil {
		wantCode(t, err, ErrUnknownClass)
	} else {
		t.Fatal("Register with an unknown class must return ErrUnknownClass")
	}
	if _, exists := a.instances[1]; exists {
		t.Fatal("a failed Register must not create an instance (no state mutation on failure)")
	}

	if _, err := a.EnqueueJob("hospital", 10, "test"); err != nil {
		wantCode(t, err, ErrUnknownClass)
	} else {
		t.Fatal("EnqueueJob with an unknown class must return ErrUnknownClass")
	}
	if mustTotalBacklog(t, a) != 0 {
		t.Fatal("a failed EnqueueJob must not mutate the backlog")
	}

	if _, err := a.Backlog("hospital", "test"); err != nil {
		wantCode(t, err, ErrUnknownClass)
	} else {
		t.Fatal("Backlog with an unknown class must return ErrUnknownClass")
	}
}

// TestUnknownStructureErrors proves AC-11's unknown-structure-reference case.
func TestUnknownStructureErrors(t *testing.T) {
	a := newTestAPI(t)
	if _, err := a.View(999, "test"); err != nil {
		wantCode(t, err, ErrUnknownStructure)
	} else {
		t.Fatal("View with an unknown structure reference must return ErrUnknownStructure")
	}
}

// TestNegativeBudgetErrors proves AC-11's negative-budget case: rejected with
// a registry-sourced error and the stored budget is left unchanged.
func TestNegativeBudgetErrors(t *testing.T) {
	a := newTestAPI(t)
	if err := a.SetDailyBudget(-5, "test"); err != nil {
		wantCode(t, err, ErrNegativeBudget)
	} else {
		t.Fatal("SetDailyBudget with a negative budget must return ErrNegativeBudget")
	}
	if a.dailyBudget != 0 {
		t.Fatalf("a failed SetDailyBudget must not mutate the stored budget (got %d)", a.dailyBudget)
	}
}

// TestNegativeAgeErrors proves AC-11's negative-age case: a negative month
// advance is rejected and the sim month is left unchanged.
func TestNegativeAgeErrors(t *testing.T) {
	a := newTestAPI(t)
	if err := a.AdvanceMonth(-1, "test"); err != nil {
		wantCode(t, err, ErrNegativeAge)
	} else {
		t.Fatal("AdvanceMonth with a negative advance must return ErrNegativeAge")
	}
	if a.month != 0 {
		t.Fatalf("a failed AdvanceMonth must not mutate the month (got %d)", a.month)
	}
}
