package maintenance

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// TestSEC162FailedSettlementLeavesBacklogUnchanged reproduces SEC-162 (P1,
// insecure-call-surface): RunCrewDay must not consume the backlog before the
// finance settlement succeeds. When SettleOpex fails — here, an insolvent
// treasury: a fresh FinanceAPI opens a zero-balance treasury with no credit
// line, so any positive opex debit is ErrInsufficientFunds — RunCrewDay must
// return the error AND leave the backlog, the job list, and the finance
// ledger exactly as they were. The day did not run, so a retry finds the jobs
// still there.
func TestSEC162FailedSettlementLeavesBacklogUnchanged(t *testing.T) {
	a := newTestAPI(t)
	f := finance.NewFinanceAPI("test")
	// Deliberately do NOT fund a credit line: the treasury is insolvent.
	if err := a.SetFinance(f); err != nil {
		t.Fatalf("set finance: %v", err)
	}

	rate := a.cfg.Classes["dwelling"].EngineerDaysPerYear
	for i := 0; i < 3; i++ {
		if _, err := a.EnqueueJob("dwelling", rate, "test"); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	if err := a.SetDailyBudget(2*rate, "test"); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	before := mustTotalBacklog(t, a)

	day, err := a.RunCrewDay("test")
	if err == nil {
		t.Fatal("RunCrewDay against an insolvent treasury must return the settlement error")
	}
	wantCode(t, err, finance.ErrInsufficientFunds)
	if day != (CrewDay{}) {
		t.Fatalf("a failed day must return a zero CrewDay, got %+v", day)
	}

	// The backlog must be UNCHANGED — not consumed ahead of the settlement.
	if got := mustTotalBacklog(t, a); got != before {
		t.Fatalf("total backlog changed by a failed settlement: %d -> %d (must be unchanged)", before, got)
	}
	if got, _ := a.Backlog("dwelling", "test"); got != before {
		t.Fatalf("per-class backlog changed by a failed settlement: %d, want %d", got, before)
	}
	if len(a.jobs) != 3 {
		t.Fatalf("job list consumed by a failed settlement: %d jobs remain, want 3", len(a.jobs))
	}
	// And finance posted nothing.
	if got := int64(f.OpexTotal()); got != 0 {
		t.Fatalf("finance posted %d opex on a failed settlement, want 0", got)
	}
}

// TestSEC163UnboundedPositiveSizeFactorRejected reproduces SEC-163 (P3,
// input-validation): a positive SizePerMille is unbounded, so
// Register(SizePerMille=MaxInt64) silently saturates BaseEngineerDaysPerYear
// in baseEngineerDaysPerYear's SafeMul — the SEC-117 silent-wrong-answer
// shape. Register must reject a factor whose rate×SizePerMille overflows
// int64, while still accepting the largest non-saturating factor (no
// over-rejection of the valid positive domain).
func TestSEC163UnboundedPositiveSizeFactorRejected(t *testing.T) {
	a := newTestAPI(t)
	rate := a.cfg.Classes["dwelling"].EngineerDaysPerYear

	if err := a.Register(1, "dwelling", RegisterOptions{SizePerMille: math.MaxInt64}, "test"); err == nil {
		t.Fatal("Register(SizePerMille=MaxInt64) must be rejected, not silently saturated")
	}
	wantCode(t, a.Register(1, "dwelling", RegisterOptions{SizePerMille: math.MaxInt64}, "test"), ErrInvalidInput)
	if _, exists := a.instances[1]; exists {
		t.Fatal("a rejected Register must not create an instance")
	}

	// The first saturating factor is MaxInt64/rate + 1; it must be rejected.
	boundary := math.MaxInt64 / rate // largest non-saturating factor for this rate
	if err := a.Register(2, "dwelling", RegisterOptions{SizePerMille: boundary + 1}, "test"); err == nil {
		t.Fatalf("Register(SizePerMille=%d) must be rejected (rate×factor overflows int64)", boundary+1)
	}

	// The largest non-saturating factor is still accepted and scales exactly.
	if err := a.Register(3, "dwelling", RegisterOptions{SizePerMille: boundary}, "test"); err != nil {
		t.Fatalf("Register(SizePerMille=%d) must be accepted (non-saturating): %v", boundary, err)
	}
	v, err := a.View(3, "test")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if want := rate * boundary / sizePerMilleUnit; v.BaseEngineerDaysPerYear != want {
		t.Fatalf("base = %d, want exact %d (no saturation)", v.BaseEngineerDaysPerYear, want)
	}
}
