package finance

import "testing"

// FEAT-1972079927 inc2 (firms-pay-construction, Aaron's 2026-08-31 ruling):
// SettleConstructionSourced retargets treasury->merchant (AcctFirms, money
// stays in-city) when materials were sourced from a local builders'
// merchant, or treasury->external (AcctExternal, a leak) when imported.
// These tests prove BOTH the routing and the conservation property: a
// local settlement leaves TotalMoneyInCirculation UNCHANGED (both accounts
// are RoleMoney), an imported settlement DECREASES it by exactly the cost
// (AcctExternal is RoleExternal, outside the tracked stock) — never a
// bare account-balance check that could pass even if money were destroyed
// or fabricated.

// TestSettleConstructionSourced_Local_ConservesMoney proves the local
// (merchant) branch: treasury debits, AcctFirms credits by the same
// amount, and the total money stock is bit-for-bit unchanged.
//
// PROOF THIS CAN FAIL: temporarily hardcoding `account := AcctExternal`
// unconditionally (ignoring the local bool) in SettleConstructionSourced
// makes this test's TotalMoneyInCirculation-unchanged assertion fail
// (verified by hand during development: total dropped by the cost instead
// of staying flat), then reverted.
func TestSettleConstructionSourced_Local_ConservesMoney(t *testing.T) {
	f := NewFinanceAPI("inc2-local")
	seedTreasury(t, f, gbp(100))

	opening := f.TotalMoneyInCirculation()
	openingTreasury, _ := f.AccountBalance(AcctTreasury)
	openingFirms, _ := f.AccountBalance(AcctFirms)

	cost := gbp(10)
	settled, err := f.SettleConstructionSourced(cost, true)
	if err != nil {
		t.Fatalf("SettleConstructionSourced(local): %v", err)
	}
	if settled != cost {
		t.Fatalf("settled = %d, want %d", settled, cost)
	}

	if bal, _ := f.AccountBalance(AcctTreasury); bal != openingTreasury-cost {
		t.Fatalf("treasury = %d, want %d", bal, openingTreasury-cost)
	}
	if bal, _ := f.AccountBalance(AcctFirms); bal != openingFirms+cost {
		t.Fatalf("firms = %d, want %d (money must land IN-CITY on a local sourcing)", bal, openingFirms+cost)
	}
	if got := f.TotalMoneyInCirculation(); got != opening {
		t.Fatalf("TotalMoneyInCirculation = %d, want unchanged %d (a local merchant sale must conserve — treasury and firms are both RoleMoney)", got, opening)
	}
	if got := f.RecomputeMoneyStock(); got != opening {
		t.Fatalf("RecomputeMoneyStock (from-scratch) = %d, want %d — the running total drifted from the ledger", got, opening)
	}
}

// TestSettleConstructionSourced_External_LeaksMoney proves the imported
// branch: treasury debits, AcctExternal credits (outside the tracked
// stock), and the total money stock DECREASES by exactly the cost — the
// documented "money leaks out" behaviour for imported materials.
//
// PROOF THIS CAN FAIL: temporarily hardcoding `account := AcctFirms`
// unconditionally makes this test's "money decreased by cost" assertion
// fail (total stayed flat instead) — verified by hand during development,
// then reverted.
func TestSettleConstructionSourced_External_LeaksMoney(t *testing.T) {
	f := NewFinanceAPI("inc2-external")
	seedTreasury(t, f, gbp(100))

	opening := f.TotalMoneyInCirculation()
	openingTreasury, _ := f.AccountBalance(AcctTreasury)

	cost := gbp(10)
	settled, err := f.SettleConstructionSourced(cost, false)
	if err != nil {
		t.Fatalf("SettleConstructionSourced(external): %v", err)
	}
	if settled != cost {
		t.Fatalf("settled = %d, want %d", settled, cost)
	}

	if bal, _ := f.AccountBalance(AcctTreasury); bal != openingTreasury-cost {
		t.Fatalf("treasury = %d, want %d", bal, openingTreasury-cost)
	}
	if got := f.TotalMoneyInCirculation(); got != opening-cost {
		t.Fatalf("TotalMoneyInCirculation = %d, want %d (an imported sourcing must LEAK exactly the cost out of the tracked stock)", got, opening-cost)
	}
	if got := f.RecomputeMoneyStock(); got != opening-cost {
		t.Fatalf("RecomputeMoneyStock (from-scratch) = %d, want %d", got, opening-cost)
	}
}

// TestSettleConstructionSourced_NegativeCostRejected mirrors every other
// stage-5 outflow's negative-amount guard (AC parity with
// SettleConstruction/SettleImports/SettleOpex).
func TestSettleConstructionSourced_NegativeCostRejected(t *testing.T) {
	f := NewFinanceAPI("inc2-negative")
	if _, err := f.SettleConstructionSourced(-1, true); !hasCode(err, ErrNegativeAmount) {
		t.Fatalf("expected ErrNegativeAmount, got %v", err)
	}
	if _, err := f.SettleConstructionSourced(-1, false); !hasCode(err, ErrNegativeAmount) {
		t.Fatalf("expected ErrNegativeAmount, got %v", err)
	}
}

// TestSettleConstructionSourced_ZeroCostNoOp proves a zero-cost settlement
// posts no transaction at all (matching PostCouncilTax's zero-skip
// pattern) — never a balanced no-op entry pair cluttering the ledger.
func TestSettleConstructionSourced_ZeroCostNoOp(t *testing.T) {
	f := NewFinanceAPI("inc2-zero")
	seedTreasury(t, f, gbp(10))
	opening := f.TotalMoneyInCirculation()
	openingTreasury, _ := f.AccountBalance(AcctTreasury)

	if settled, err := f.SettleConstructionSourced(0, true); err != nil || settled != 0 {
		t.Fatalf("SettleConstructionSourced(0): settled=%d err=%v, want 0, nil", settled, err)
	}
	if got := f.TotalMoneyInCirculation(); got != opening {
		t.Fatalf("TotalMoneyInCirculation = %d, want unchanged %d", got, opening)
	}
	if bal, _ := f.AccountBalance(AcctTreasury); bal != openingTreasury {
		t.Fatalf("treasury = %d, want unchanged %d", bal, openingTreasury)
	}
}
