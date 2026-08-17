package capexport

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is the Destructive-bounce regression suite for
// SEC-184/185/186/187/192/193/194 (engine.capexport, MOD-049). Each test
// reproduces the reported attack shape and asserts the post-fix invariant;
// each FAILS against the pre-fix code and PASSES against the fix.

// TestConcurrentCancelsPostExactlyOnePenalty (SEC-184): 64 concurrent cancels
// of one contract, released together, must leave exactly one treasury
// penalty-debit — one debit per successful cancel. The pre-fix code posted the
// penalty BEFORE the write-lock re-check, so a losing cancel had already
// debited the treasury by the time it returned ErrInvalidContract. Driven to
// completion (no sleeps/timing races) and asserted on the money-conservation
// invariant, so it is deterministic post-fix: the debit count must equal the
// success count, and the success count must be exactly one.
func TestConcurrentCancelsPostExactlyOnePenalty(t *testing.T) {
	a, svc, fin, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 10000)
	bindLine(t, a, ExportHospitalBeds, id)
	c, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 10, TermMonths: 12, RateMicropounds: 1_000_000})
	if err != nil {
		t.Fatalf("IssueContract: %v", err)
	}

	const n = 64
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	var successes, unexpected atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait()
			if _, err := a.PayCancellationPenalty(c.ID); err != nil {
				if !errors.Is(err, &errs.E{Code: ErrInvalidContract}) {
					unexpected.Add(1)
				}
			} else {
				successes.Add(1)
			}
		}()
	}
	start.Done()
	wg.Wait()

	if got := unexpected.Load(); got != 0 {
		t.Fatalf("%d cancels returned an unexpected error, want only success or ErrInvalidContract", got)
	}
	if got := successes.Load(); got != 1 {
		t.Fatalf("%d successful cancels, want exactly 1", got)
	}

	debits := 0
	for _, e := range fin.LinesByCategory(CatTradeExport) {
		if e.Account == finance.AcctTreasury && e.Side == finance.SideDebit {
			debits++
		}
	}
	if debits != int(successes.Load()) {
		t.Fatalf("%d treasury penalty-debit entries for %d successful cancels, want exactly one debit per cancel (SEC-184: the penalty must be posted atomically with the cancellation)", debits, successes.Load())
	}
}

// TestAccrueRevenueBoundedByRemainingTerm (SEC-185): accruing a contract for
// more months than its remaining term must be capped to that term, never
// fabricate revenue for months beyond the contract's life.
func TestAccrueRevenueBoundedByRemainingTerm(t *testing.T) {
	a, svc, fin, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 1000)
	bindLine(t, a, ExportHospitalBeds, id)
	c, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 1, TermMonths: 1, RateMicropounds: 1_000_000})
	if err != nil {
		t.Fatalf("IssueContract: %v", err)
	}

	amount, err := a.AccrueRevenue(c.ID, 1_000_000)
	if err != nil {
		t.Fatalf("AccrueRevenue: %v", err)
	}
	// Capped to the 1 remaining month, not 1,000,000 months: 1 × 1,000,000 × 1.
	if amount != 1_000_000 {
		t.Fatalf("AccrueRevenue(1M months on a 1-month contract) = %d, want 1,000,000 (capped to remaining term)", amount)
	}
	if !hasTreasurySide(fin, amount, finance.SideCredit) {
		t.Fatalf("capped revenue credit %d not posted as a treasury credit", amount)
	}
}

// TestAccrueRevenueRejectsSaturatedAmount (SEC-185): a computed amount whose
// intermediate saturates int64 must be rejected, not posted as a fabricated
// MaxInt64 value.
func TestAccrueRevenueRejectsSaturatedAmount(t *testing.T) {
	a, svc, _, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 1000)
	bindLine(t, a, ExportHospitalBeds, id)
	c, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 2, TermMonths: 12, RateMicropounds: math.MaxInt64})
	if err != nil {
		t.Fatalf("IssueContract: %v", err)
	}
	if _, err := a.AccrueRevenue(c.ID, 1); err == nil || !errors.Is(err, &errs.E{Code: ErrInvalidContractInput}) {
		t.Fatalf("AccrueRevenue with saturating amount err = %v, want ErrInvalidContractInput (never a fabricated MaxInt64 posting)", err)
	}
}

// TestDemandCurveNeverReturnsNonFinite (SEC-186): a growth rate large enough
// to overflow math.Pow(1+g, N) within the query window — including the shipped
// default g=0.02, which overflows near month 35727 — must never leak +Inf (or
// NaN, from 0 × Inf when demand is 0) onto the projection surface. The
// provider rejects the non-finite point (Curve returns an error); if Curve
// succeeds, every point must be finite.
func TestDemandCurveNeverReturnsNonFinite(t *testing.T) {
	cases := []struct {
		name string
		g    float64
		base float64
		to   int64
	}{
		{"huge growth overflows", 1e18, 10, 40},
		{"shipped default growth overflows", 0.02, 10, 40000},
		{"zero demand produces NaN via 0×Inf", 1e18, 0, 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, svc, _, proj := newTestAPI(t)
			id := registerService(t, svc, "hospital", 1000)
			setDemand(t, svc, id, tc.base)
			bindLine(t, a, ExportHospitalBeds, id)
			if err := a.SetDemandGrowth(tc.g); err != nil {
				t.Fatalf("SetDemandGrowth: %v", err)
			}
			if err := a.RegisterContractCurves(); err != nil {
				t.Fatalf("RegisterContractCurves: %v", err)
			}

			points, err := proj.Curve(DemandCurveKey(ExportHospitalBeds), 0, tc.to)
			if err != nil {
				// Rejection is the accepted outcome — the non-finite point was
				// refused rather than propagated to F7.
				return
			}
			for _, p := range points {
				if !num.IsFinite(p.Value) {
					t.Fatalf("month %d demand = %v (non-finite) leaked onto the curve", p.Month, p.Value)
				}
			}
		})
	}
}

// TestReBindRejectsOverCapacity (SEC-187): re-binding a line whose already-sold
// (committed) quantity exceeds the new service's capacity must be rejected,
// mirroring IssueContract's oversell check — otherwise the line becomes a
// permanently-crossing line with no demand change.
func TestReBindRejectsOverCapacity(t *testing.T) {
	a, svc, _, _ := newTestAPI(t)
	big := registerService(t, svc, "hospital", 100)
	bindLine(t, a, ExportHospitalBeds, big)
	if _, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 90, TermMonths: 12, RateMicropounds: 1_000_000}); err != nil {
		t.Fatalf("IssueContract: %v", err)
	}

	small := registerService(t, svc, "clinic", 10)
	if err := a.BindServiceLine(ExportHospitalBeds, small); err == nil || !errors.Is(err, &errs.E{Code: ErrInsufficientSurplus}) {
		t.Fatalf("re-bind over capacity err = %v, want ErrInsufficientSurplus", err)
	}

	// The rejected re-bind left the binding (and the committed figure) intact.
	book, err := a.SurplusBook(ExportHospitalBeds)
	if err != nil {
		t.Fatalf("SurplusBook: %v", err)
	}
	if book.Capacity != 100 {
		t.Fatalf("capacity after rejected re-bind = %v, want 100 (binding unchanged)", book.Capacity)
	}
	if c, err := a.Committed(ExportHospitalBeds); err != nil || c != 90 {
		t.Fatalf("committed after rejected re-bind = %v, %v; want 90, nil", c, err)
	}

	// A re-bind to a service with adequate capacity still succeeds.
	bigger := registerService(t, svc, "regional", 200)
	if err := a.BindServiceLine(ExportHospitalBeds, bigger); err != nil {
		t.Fatalf("re-bind to adequate capacity: %v", err)
	}
}

// TestCancellationPenaltyRejectsOverflow (SEC-192): a contract whose
// cancellation penalty (remaining × rate × quantity) overflows int64 must be
// REJECTED with ErrInvalidContractInput, never silently saturated to MaxInt64
// and posted. Reproduces the reported shape TermMonths=MaxInt64,
// RateMicropounds=MaxInt64, Quantity=1 — the true penalty ≈ 8.5e37 overflows
// int64, and the pre-fix code saturated to MaxInt64 (which then failed the
// finance overdraft check for the wrong reason). Exercised through
// PayCancellationPenalty so the money-conservation outcome (no fabricated
// MaxInt64 debit) is what is asserted, not just the arithmetic.
func TestCancellationPenaltyRejectsOverflow(t *testing.T) {
	a, svc, fin, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 1000)
	bindLine(t, a, ExportHospitalBeds, id)
	c, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 1, TermMonths: math.MaxInt64, RateMicropounds: math.MaxInt64})
	if err != nil {
		t.Fatalf("IssueContract: %v", err)
	}

	if _, err := a.PayCancellationPenalty(c.ID); err == nil || !errors.Is(err, &errs.E{Code: ErrInvalidContractInput}) {
		t.Fatalf("PayCancellationPenalty on an overflowing penalty err = %v, want ErrInvalidContractInput (never a saturated MaxInt64 posting)", err)
	}

	for _, e := range fin.LinesByCategory(CatTradeExport) {
		if e.Account == finance.AcctTreasury && e.Side == finance.SideDebit && e.Amount == finance.Money(math.MaxInt64) {
			t.Fatalf("a MaxInt64 treasury debit was posted — the penalty was saturated, not rejected (SEC-192)")
		}
	}
}

// TestAccrueRevenueIsIdempotentAcrossCalls (SEC-193): two identical
// AccrueRevenue(id, N) calls must not double-post. The contract records its
// accrued-to-date months, so the second call finds the months already accrued
// and is rejected rather than posting a second 12,000,000 for a 12-month
// contract worth 12,000,000. The assertion is the money-conservation
// invariant: total revenue credits must equal the first accrual exactly.
func TestAccrueRevenueIsIdempotentAcrossCalls(t *testing.T) {
	a, svc, fin, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 1000)
	bindLine(t, a, ExportHospitalBeds, id)
	c, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 1, TermMonths: 12, RateMicropounds: 1_000_000})
	if err != nil {
		t.Fatalf("IssueContract: %v", err)
	}

	first, err := a.AccrueRevenue(c.ID, 12)
	if err != nil {
		t.Fatalf("first AccrueRevenue: %v", err)
	}
	if first != 12_000_000 {
		t.Fatalf("first accrual = %d, want 12,000,000", first)
	}

	if _, err := a.AccrueRevenue(c.ID, 12); err == nil {
		t.Fatalf("second identical AccrueRevenue returned nil, want rejection (the months are already accrued)")
	}

	var total finance.Money
	for _, e := range fin.LinesByCategory(CatTradeExport) {
		if e.Account == finance.AcctTreasury && e.Side == finance.SideCredit {
			total += e.Amount
		}
	}
	if total != first {
		t.Fatalf("total revenue credits = %d, want %d (SEC-193: two identical accruals must not double-post)", total, first)
	}
}

// TestReBindRejectsDemandSqueezedService (SEC-194): re-binding a line whose
// already-sold quantity fits the new service's raw capacity but not its
// exportable slack (capacity − internal demand) must be rejected. SEC-187 only
// checked committed > capacity, so a re-bind to capacity 100 with internal
// demand 95 still yielded a permanently-crossing line. The full oversell check
// (mirroring IssueContract's quantity > capacity − demand − committed) rejects
// committed > capacity − demand.
func TestReBindRejectsDemandSqueezedService(t *testing.T) {
	a, svc, _, _ := newTestAPI(t)
	big := registerService(t, svc, "hospital", 100)
	setDemand(t, svc, big, 0)
	bindLine(t, a, ExportHospitalBeds, big)
	if _, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 90, TermMonths: 12, RateMicropounds: 1_000_000}); err != nil {
		t.Fatalf("IssueContract: %v", err)
	}

	// New service: capacity 100 (committed 90 fits raw capacity) but internal
	// demand 95 leaves exportable slack 5 — committed 90 > 5, so it must reject.
	squeezed := registerService(t, svc, "clinic", 100)
	setDemand(t, svc, squeezed, 95)
	if err := a.BindServiceLine(ExportHospitalBeds, squeezed); err == nil || !errors.Is(err, &errs.E{Code: ErrInsufficientSurplus}) {
		t.Fatalf("re-bind onto a demand-squeezed service err = %v, want ErrInsufficientSurplus", err)
	}

	// The rejected re-bind left the binding intact.
	book, err := a.SurplusBook(ExportHospitalBeds)
	if err != nil {
		t.Fatalf("SurplusBook: %v", err)
	}
	if book.Capacity != 100 {
		t.Fatalf("capacity after rejected re-bind = %v, want 100 (binding unchanged)", book.Capacity)
	}
}

// TestAccrueRevenueCollectsFullTermMonthByMonth (SEC-197): the natural per-tick
// accrual — SetMonth(m) then AccrueRevenue(id, 1) — must collect the FULL
// term's revenue, not half of it. The pre-fix bound available = remaining −
// AccruedMonths merges two independent constraints (remaining shrinks with
// elapsed while AccruedMonths grows with each accrual), so the two advance in
// lockstep and the bound collapses to TermMonths − 2·elapsed, hitting zero at
// half the term: a 12-month contract accruing 1 month per sim month was
// rejected at month 6 having collected 6,000,000 of 12,000,000. The
// money-conservation invariant asserted: total revenue credits must equal the
// full term's value, month-by-month.
func TestAccrueRevenueCollectsFullTermMonthByMonth(t *testing.T) {
	a, svc, fin, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 1000)
	bindLine(t, a, ExportHospitalBeds, id)
	c, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 1, TermMonths: 12, RateMicropounds: 1_000_000})
	if err != nil {
		t.Fatalf("IssueContract: %v", err)
	}

	var total finance.Money
	for m := int64(0); m < 12; m++ {
		if err := a.SetMonth(m); err != nil {
			t.Fatalf("SetMonth(%d): %v", m, err)
		}
		amount, err := a.AccrueRevenue(c.ID, 1)
		if err != nil {
			t.Fatalf("AccrueRevenue at month %d: %v (SEC-197: a 12-month contract accruing 1 month per sim month must collect the full term, not be rejected halfway)", m, err)
		}
		total += amount
	}
	if total != 12_000_000 {
		t.Fatalf("total accrued = %d, want 12,000,000 (SEC-197: the full term's revenue must be collected month-by-month)", total)
	}

	// The ledger must reflect the same full amount (money conservation).
	var ledger finance.Money
	for _, e := range fin.LinesByCategory(CatTradeExport) {
		if e.Account == finance.AcctTreasury && e.Side == finance.SideCredit {
			ledger += e.Amount
		}
	}
	if ledger != total {
		t.Fatalf("ledger revenue credits = %d, want %d", ledger, total)
	}
}

// TestIssueContractReReadsBindingUnderWriteLock (SEC-198): the oversell check
// must validate against the service binding actually in force at commit time,
// not a binding snapshotted before the write lock. A concurrent BindServiceLine
// to a smaller service slipping between IssueContract's RUnlock and Lock must
// not leave a permanently-crossing oversold line. Driven deterministically via
// testHookBeforeIssueCommit (no sleeps/timing): IssueContract is paused in the
// race window, the re-bind commits, and only then is IssueContract released.
// Pre-fix, IssueContract validates against the stale big service and issues
// committed=100 against a binding of capacity 10; post-fix it re-reads the
// binding under the write lock, sees the small service, and rejects with
// ErrInsufficientSurplus.
func TestIssueContractReReadsBindingUnderWriteLock(t *testing.T) {
	a, svc, _, _ := newTestAPI(t)
	big := registerService(t, svc, "hospital", 100)
	small := registerService(t, svc, "clinic", 10)
	bindLine(t, a, ExportHospitalBeds, big)

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(release) }) }
	testHookBeforeIssueCommit = func() {
		close(entered)
		<-release
	}
	defer func() {
		doRelease() // never leave the paused goroutine blocked on a failure path
		testHookBeforeIssueCommit = nil
	}()

	type result struct {
		c   Contract
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		c, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 100, TermMonths: 12, RateMicropounds: 1_000_000})
		resCh <- result{c, err}
	}()

	<-entered // IssueContract has released the RLock and is paused before the write lock

	// Re-bind to the smaller service while IssueContract is paused. committed is
	// still 0, so BindServiceLine's own re-bind re-check (SEC-194) passes and the
	// binding flips to `small`.
	if err := a.BindServiceLine(ExportHospitalBeds, small); err != nil {
		t.Fatalf("BindServiceLine to small: %v", err)
	}

	doRelease() // let IssueContract proceed to the write lock
	res := <-resCh

	// Post-fix, the write-lock re-read sees `small` (capacity 10) and rejects the
	// 100-unit issue. The issue must NOT have committed against the stale binding.
	if res.err == nil {
		t.Fatalf("IssueContract issued %+v against the stale big service while the line is now bound to small (capacity 10) — a permanently-crossing oversold line (SEC-198)", res.c)
	}
	if !errors.Is(res.err, &errs.E{Code: ErrInsufficientSurplus}) {
		t.Fatalf("IssueContract err = %v, want ErrInsufficientSurplus (the write-lock re-read must reject against the new binding)", res.err)
	}

	// The invariant, asserted through the public query surface: committed must
	// fit within the current binding's exportable slack.
	book, err := a.SurplusBook(ExportHospitalBeds)
	if err != nil {
		t.Fatalf("SurplusBook: %v", err)
	}
	if book.Committed > book.Surplus {
		t.Fatalf("permanently-crossing oversold line: committed %v > exportable slack %v (capacity %v, demand %v) — the stale-binding read let IssueContract oversell the newly-bound service (SEC-198)", book.Committed, book.Surplus, book.Capacity, book.Demand)
	}
}
