package maintenance

import "testing"

// TestMultiFixResolvesTenJobsInOneDay proves AC-5: a crew's day is a job
// list, not one-trip-one-fix. A budget worth ten pothole-class jobs against a
// ten-job backlog resolves all ten in a single maintenance tick.
func TestMultiFixResolvesTenJobsInOneDay(t *testing.T) {
	a := newTestAPI(t)
	rate := a.cfg.Classes["dwelling"].EngineerDaysPerYear // from data, not a literal (GR#15)

	const jobs = 10
	for i := 0; i < jobs; i++ {
		if _, err := a.EnqueueJob("dwelling", rate, "test"); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := a.SetDailyBudget(rate*int64(jobs), "test"); err != nil {
		t.Fatalf("set budget: %v", err)
	}

	day, err := a.RunCrewDay("test")
	if err != nil {
		t.Fatalf("run crew day: %v", err)
	}
	if day.JobsResolved != jobs {
		t.Fatalf("resolved %d jobs in one day, want %d (one-trip-one-fix would resolve 1)", day.JobsResolved, jobs)
	}
	if day.Applied != rate*int64(jobs) {
		t.Fatalf("applied %d engineer-days, want %d", day.Applied, rate*int64(jobs))
	}
	if got := mustTotalBacklog(t, a); got != 0 {
		t.Fatalf("backlog after clearing 10 jobs = %d, want 0", got)
	}
}

// TestConservationBudget proves AC-6: a crew's daily budget is bounded — the
// total applied never exceeds the budget, the budget never goes negative, and
// the unresolved remainder is exactly the difference, still present as
// backlog. All expected figures are derived from runtime queries, never
// hardcoded (GR#15).
func TestConservationBudget(t *testing.T) {
	a := newTestAPI(t)
	rate := a.cfg.Classes["dwelling"].EngineerDaysPerYear

	// A three-job backlog; a budget worth two jobs.
	for i := 0; i < 3; i++ {
		if _, err := a.EnqueueJob("dwelling", rate, "test"); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	budget := 2 * rate
	if err := a.SetDailyBudget(budget, "test"); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	before := mustTotalBacklog(t, a)

	day, err := a.RunCrewDay("test")
	if err != nil {
		t.Fatalf("run crew day: %v", err)
	}

	// (a) applied <= budget.
	if day.Applied > budget {
		t.Fatalf("applied %d exceeds budget %d", day.Applied, budget)
	}
	// (b) the budget is never driven negative.
	if day.BudgetRemaining < 0 {
		t.Fatalf("budget remaining = %d, must never be negative", day.BudgetRemaining)
	}
	// (c) the unresolved remainder is exactly the difference, still in backlog.
	wantRemainder := before - day.Applied
	if day.BacklogRemaining != wantRemainder {
		t.Fatalf("backlog remaining = %d, want exactly before-applied = %d", day.BacklogRemaining, wantRemainder)
	}
	if got := mustTotalBacklog(t, a); got != wantRemainder {
		t.Fatalf("total backlog = %d, want %d", got, wantRemainder)
	}
	// The ledger balances exactly, in integer engineer-day units.
	if day.Applied != 2*rate {
		t.Fatalf("applied = %d, want exactly 2*rate = %d", day.Applied, 2*rate)
	}
}

// TestConservationBudgetLargerThanBacklog proves the complementary bound:
// when the budget exceeds the backlog, only the backlog is applied (no
// invented work) and the budget is not driven negative.
func TestConservationBudgetLargerThanBacklog(t *testing.T) {
	a := newTestAPI(t)
	rate := a.cfg.Classes["dwelling"].EngineerDaysPerYear
	for i := 0; i < 2; i++ {
		if _, err := a.EnqueueJob("dwelling", rate, "test"); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	before := mustTotalBacklog(t, a)
	if err := a.SetDailyBudget(10*rate, "test"); err != nil {
		t.Fatalf("set budget: %v", err)
	}

	day, err := a.RunCrewDay("test")
	if err != nil {
		t.Fatalf("run crew day: %v", err)
	}
	if day.Applied != before {
		t.Fatalf("applied %d, want exactly the whole backlog %d (no invented work)", day.Applied, before)
	}
	if day.BudgetRemaining != 10*rate-before {
		t.Fatalf("budget remaining = %d, want %d", day.BudgetRemaining, 10*rate-before)
	}
	if mustTotalBacklog(t, a) != 0 {
		t.Fatalf("backlog = %d, want 0", mustTotalBacklog(t, a))
	}
}

// TestBacklogAccumulatesAndDecreasesByApplied proves AC-7: un-fixed demand
// accumulates as a visible, stateful backlog that grows under sustained
// under-funding, and decreases only by exactly the engineer-days a crew
// actually applies.
func TestBacklogAccumulatesAndDecreasesByApplied(t *testing.T) {
	a := newTestAPI(t)
	if err := a.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Under-fund (no budget) for three simulated years. Each year accrues one
	// year's demand as backlog; it must strictly grow day-over-day (here,
	// year-over-year), proving accumulation is stateful — a recomputed
	// "demand - applied" would read zero.
	prev := mustTotalBacklog(t, a)
	for i := 0; i < 3; i++ {
		if err := a.AdvanceMonth(12, "test"); err != nil {
			t.Fatalf("advance year %d: %v", i, err)
		}
		now := mustTotalBacklog(t, a)
		if now <= prev {
			t.Fatalf("backlog must strictly grow under under-funding: %d -> %d", prev, now)
		}
		prev = now
	}

	// Apply a known budget; the backlog decreases by exactly the applied
	// engineer-days, never more, never negative.
	rate := a.cfg.Classes["dwelling"].EngineerDaysPerYear
	before := mustTotalBacklog(t, a)
	if err := a.SetDailyBudget(2*rate, "test"); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	day, err := a.RunCrewDay("test")
	if err != nil {
		t.Fatalf("run crew day: %v", err)
	}
	if got := mustTotalBacklog(t, a); got != before-day.Applied {
		t.Fatalf("backlog after apply = %d, want exactly before-applied = %d", got, before-day.Applied)
	}
	if mustTotalBacklog(t, a) < 0 {
		t.Fatalf("backlog = %d, must never go negative", mustTotalBacklog(t, a))
	}
}

// TestBacklogIsQueryablePerClass proves AC-7's per-class query surface and
// that per-class figures stay consistent with the city-wide total.
func TestBacklogIsQueryablePerClass(t *testing.T) {
	a := newTestAPI(t)
	dwellingRate := a.cfg.Classes["dwelling"].EngineerDaysPerYear
	heavyRate := a.cfg.Classes["heavy_industry"].EngineerDaysPerYear

	if _, err := a.EnqueueJob("dwelling", dwellingRate, "test"); err != nil {
		t.Fatalf("enqueue dwelling: %v", err)
	}
	if _, err := a.EnqueueJob("heavy_industry", heavyRate, "test"); err != nil {
		t.Fatalf("enqueue heavy: %v", err)
	}

	dw, err := a.Backlog("dwelling", "test")
	if err != nil {
		t.Fatalf("backlog dwelling: %v", err)
	}
	hv, err := a.Backlog("heavy_industry", "test")
	if err != nil {
		t.Fatalf("backlog heavy: %v", err)
	}
	if dw != dwellingRate || hv != heavyRate {
		t.Fatalf("per-class backlog = (%d, %d), want (%d, %d)", dw, hv, dwellingRate, heavyRate)
	}
	if total := mustTotalBacklog(t, a); total != dw+hv {
		t.Fatalf("total backlog %d != sum of per-class %d", total, dw+hv)
	}
}
