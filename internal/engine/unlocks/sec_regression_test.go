package unlocks

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// --- SEC-080: XP accrual must saturate, never wrap ----------------------

// TestXPAwardSaturatesNotWraps proves a MaxInt64 award followed by a
// positive award saturates at MaxInt64 rather than wrapping to a negative
// total. Against the pre-fix `u.xp += amount`, MaxInt64+1 wraps to
// MinInt64 and this test fails.
func TestXPAwardSaturatesNotWraps(t *testing.T) {
	api := realAPI(t)

	if err := api.AwardPopulationXP(math.MaxInt64, testCorrelationID()); err != nil {
		t.Fatalf("AwardPopulationXP(MaxInt64): %v", err)
	}
	if err := api.AwardPopulationXP(1, testCorrelationID()); err != nil {
		t.Fatalf("AwardPopulationXP(1): %v", err)
	}

	if got := api.XP(); got != math.MaxInt64 {
		t.Errorf("XP = %d, want %d (saturating addition must not wrap, SEC-080)", got, int64(math.MaxInt64))
	}
	if api.XP() < 0 {
		t.Errorf("XP = %d, wrapped negative; saturating arithmetic required", api.XP())
	}
}

// --- SEC-081: no population mutation before finance is validated --------

// TestAdvancePopulationRequiresFinanceBeforeMutating proves a crossing
// with finance unwired fails WITHOUT mutating population or tier. Against
// the pre-fix order (population written before the finance check),
// CurrentPopulation would already be 100 and this test fails.
func TestAdvancePopulationRequiresFinanceBeforeMutating(t *testing.T) {
	api := realAPI(t) // finance not wired

	_, err := api.AdvancePopulation(100, testCorrelationID())
	assertCode(t, err, ErrFinanceNotWired)

	if got := api.CurrentPopulation(); got != 0 {
		t.Errorf("CurrentPopulation = %d after rejected crossing, want 0 (no partial mutation, SEC-081)", got)
	}
	if got := api.CurrentTier(); got != 0 {
		t.Errorf("CurrentTier = %d after rejected crossing, want 0 (no partial mutation, SEC-081)", got)
	}
	if api.DevelopmentPoints() != 0 || api.ExpansionPermits() != 0 {
		t.Error("DP/permits mutated after a rejected crossing with unwired finance")
	}
}

// --- SEC-082: sticky-flag write precedes the unlock ---------------------

// TestForceUnlockDebugTouchBeforeApply proves a failing debugTouch leaves
// the milestone/node state untouched. Against the pre-fix order (unlock
// applied, then touch invoked), MilestoneReached(7) would already be true
// and this test fails.
func TestForceUnlockDebugTouchBeforeApply(t *testing.T) {
	api := realAPI(t)
	if err := api.SetDebugGate(func(string) error { return nil }); err != nil {
		t.Fatalf("SetDebugGate: %v", err)
	}
	if err := api.SetDebugTouch(func() error { return errors.New("sticky flag write failed") }); err != nil {
		t.Fatalf("SetDebugTouch: %v", err)
	}

	err := api.ForceUnlock(ForceTarget{Tier: 7}, testCorrelationID())
	if err == nil {
		t.Fatal("ForceUnlock with a failing debugTouch returned nil error")
	}
	if api.MilestoneReached(7) {
		t.Error("MilestoneReached(7) = true after a failed debugTouch; the unlock must not apply (SEC-082)")
	}
	if api.CurrentTier() != 0 {
		t.Errorf("CurrentTier = %d after a failed debugTouch, want 0", api.CurrentTier())
	}
	if api.DebugTouched() {
		t.Error("DebugTouched = true after a failed debugTouch")
	}
}

// --- SEC-082 (companion): ForceUnlock rejects no-op "none" nodes --------

// TestForceUnlockRejectsNoOpNode proves ForceUnlock rejects a kind:"none"
// no-op placeholder node as a target, matching SpendDevelopmentPoints'
// rule that only real "unlock" nodes are spendable/unlockable (a fifth
// gap surfaced by the Destructive probe file, fixed for consistency).
func TestForceUnlockRejectsNoOpNode(t *testing.T) {
	api := realAPI(t)
	if err := api.SetDebugGate(func(string) error { return nil }); err != nil {
		t.Fatalf("SetDebugGate: %v", err)
	}
	var noneID string
	for id, n := range api.nodes {
		if n.Kind == "none" {
			noneID = id
			break
		}
	}
	if noneID == "" {
		t.Fatal("no kind:none node in the fixture")
	}

	err := api.ForceUnlock(ForceTarget{NodeID: noneID}, testCorrelationID())
	assertCode(t, err, ErrInvalidUnlockTarget)
	if api.IsNodeUnlocked(noneID) {
		t.Errorf("ForceUnlock unlocked the no-op node %q", noneID)
	}
}

// --- SEC-083: MilestoneReached is lock-free (no finance.mu -> unlocks.mu) -

// TestMilestoneReachedLockFreeWithFinanceBorrow deterministically proves
// the ABBA lock-cycle fix: it holds unlocks.mu (write) while
// finance.Borrow runs. finance.Borrow holds finance.mu and then calls the
// injected MilestoneReached. Against the pre-fix code (MilestoneReached
// taking unlocks.mu's RLock), that call blocks forever behind this
// goroutine's write lock and the select below times out, failing the test.
// With the lock-free fix it completes immediately.
func TestMilestoneReachedLockFreeWithFinanceBorrow(t *testing.T) {
	api, f := realAPIWithFinance(t)
	if err := f.SetMilestoneGate(api); err != nil {
		t.Fatalf("SetMilestoneGate: %v", err)
	}
	if _, err := api.AdvancePopulation(0, testCorrelationID()); err != nil { // cross tier 1
		t.Fatalf("AdvancePopulation(0): %v", err)
	}
	if !api.MilestoneReached(1) {
		t.Fatal("MilestoneReached(1) = false after crossing tier 1")
	}

	api.mu.Lock()
	defer api.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := f.Borrow(finance.LoanRequest{Tier: 1, Principal: 1_000_000, TermMonths: 12})
		done <- err
	}()

	select {
	case borrowErr := <-done:
		if borrowErr != nil {
			t.Errorf("Borrow: %v", borrowErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ABBA deadlock (SEC-083): finance.Borrow blocked on unlocks.mu via MilestoneReached while unlocks.mu was held")
	}
}
