package firms

import (
	"testing"
)

// TestDepositBoundCapsCredit (AC-13): a credit request cannot be approved
// for more than the deposit-backed lending capacity allows, and lowering
// the deposit pool denies a previously-approved request.
func TestDepositBoundCapsCredit(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	fin := mustFinance(t)
	_ = api.SetFinance(fin)
	_ = api.SetCitizens(seedCitizens(t, 5))

	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}

	// Seed £1,000 of household deposits (1e9 micro-pounds).
	seedDeposits(t, fin, 1_000_000_000)
	if got := api.DepositPool(); got != 1_000_000_000 {
		t.Fatalf("DepositPool = %d, want 1e9", got)
	}
	// Capacity = pool × 900/1000 = £900 (9e8 micro-pounds).
	cap := api.LendingCapacity()
	if cap != 900_000_000 {
		t.Fatalf("LendingCapacity = %d, want 9e8", cap)
	}

	// £800 ≤ £900 → approved.
	d, err := api.ApproveCredit(CreditRequest{FirmID: id, Principal: 800_000_000, Month: 0})
	if err != nil {
		t.Fatalf("ApproveCredit(£800) = %v", err)
	}
	if !d.Approved || d.Amount != 800_000_000 {
		t.Fatalf("decision = %+v, want approved £800", d)
	}

	// Lower the deposit pool to £500 → capacity £450 < £800 → denied.
	drainDeposits(t, fin, 500_000_000)
	if got := api.DepositPool(); got != 500_000_000 {
		t.Fatalf("DepositPool after drain = %d, want 5e8", got)
	}
	_, err = api.ApproveCredit(CreditRequest{FirmID: id, Principal: 800_000_000, Month: 0})
	if !hasCode(err, ErrCreditDenied) {
		t.Fatalf("ApproveCredit(£800) after drain = %v, want ErrCreditDenied", err)
	}
}

// TestRateCycleSpikeRaisesInsolvency (AC-14): a rate-cycle spike raises the
// insolvency rate among credit-dependent Startup/Small firms, with no player
// command altering the rate.
func TestRateCycleSpikeRaisesInsolvency(t *testing.T) {
	const outstanding = 1_200_000_000 // £1,200
	const cashflow = 10_000_000       // £10/month

	// Baseline: month 0 → base 500bp + Startup spread 300bp = 800bp.
	// monthly interest = 1.2e9 × 800 / 10000 / 12 = £8 < £10 → survives.
	baseline := newAPIWithConfig(t, controlledConfig(), 1)
	_ = baseline.SetCitizens(seedCitizens(t, 5))
	id, err := baseline.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	baseline.firms[id].firm.Financial.CreditOutstanding = outstanding
	baseline.firms[id].firm.Financial.MonthlyCashFlow = cashflow
	if err := baseline.ResolveMonth(0); err != nil {
		t.Fatalf("ResolveMonth(baseline): %v", err)
	}
	if baseline.FailedCount() != 0 {
		t.Fatalf("baseline Startup failed at the low rate (failed=%d)", baseline.FailedCount())
	}

	// Spike: month 96 → base 900bp + 300bp = 1200bp.
	// monthly interest = 1.2e9 × 1200 / 10000 / 12 = £12 > £10 → insolvent.
	spiked := newAPIWithConfig(t, controlledConfig(), 1)
	_ = spiked.SetCitizens(seedCitizens(t, 5))
	id2, err := spiked.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	spiked.firms[id2].firm.Financial.CreditOutstanding = outstanding
	spiked.firms[id2].firm.Financial.MonthlyCashFlow = cashflow
	if err := spiked.ResolveMonth(96); err != nil {
		t.Fatalf("ResolveMonth(spike): %v", err)
	}
	if spiked.FailedCount() != 1 {
		t.Fatalf("spiked Startup did not fail at the high rate (failed=%d)", spiked.FailedCount())
	}
}

// TestChurnBothFoundedAndFailed (AC-9): over a synthetic multi-month run a
// healthy economy churns — both the founded-firm and failed-firm counts are
// nonzero (deterministic, not probable).
func TestChurnBothFoundedAndFailed(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	_ = api.SetCitizens(seedCitizens(t, 30))

	// Month 1: a firm is founded.
	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	if api.FoundedCount() == 0 {
		t.Fatal("expected at least one founded firm")
	}

	// A later month: the same firm is credit-dependent with cash flow below
	// its spiked borrowing cost, so it fails (insolvency).
	api.firms[id].firm.Financial.CreditOutstanding = 1_200_000_000
	api.firms[id].firm.Financial.MonthlyCashFlow = 10_000_000
	if err := api.ResolveMonth(96); err != nil {
		t.Fatalf("ResolveMonth: %v", err)
	}
	if api.FailedCount() == 0 {
		t.Fatal("expected at least one failed firm")
	}
}
