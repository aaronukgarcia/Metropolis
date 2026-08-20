package policies

import (
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
)

// ---------------------------------------------------------------------------
// Regression tests for real defects in the tax-move and month-atomicity paths
// (engine.policies). engine.tax exposes GetDistrictMultiplier, so policies
// composes tax moves getter-first against the real applied multiplier and
// never maintains a private mirror of it.
// ---------------------------------------------------------------------------

// taxPolicy builds a district-scoped policy whose single coefficient carries a
// data-declared district-multiplier tax move.
func taxPolicy(id PolicyID, key string, delta float64, instrument string) *policyDef {
	return &policyDef{
		ID:        id,
		Name:      string(id),
		Category:  "economy",
		Scope:     ScopeDistrict,
		Mechanism: []CoefficientDelta{{Key: key, Delta: delta, Tax: &TaxMove{Instrument: instrument, Mode: taxMoveDistrictMultiplier}}},
	}
}

// lastTaxMultiplier returns the multiplier of the most recent recorded tax
// call, failing the test if none was recorded.
func lastTaxMultiplier(t *testing.T, rec *recordingTax) float64 {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.calls) == 0 {
		t.Fatal("no tax call recorded")
	}
	return rec.calls[len(rec.calls)-1].multiplier
}

// failAllFinance is a finance seam whose Post always fails, used to force an
// enactment-cost posting to fail and exercise the commit rollback.
type failAllFinance struct{}

func (failAllFinance) Post(finance.Transaction) (finance.TxID, error) {
	return 0, errors.New("injected finance failure")
}

// balanceCappedFinance is a finance seam with a finite treasury: Post rejects
// a transaction whose total debit exceeds the remaining balance (the same
// overdraft shape the real ledger enforces), and otherwise records it.
type balanceCappedFinance struct {
	mu      sync.Mutex
	txns    []finance.Transaction
	balance finance.Money
}

func (f *balanceCappedFinance) Post(tx finance.Transaction) (finance.TxID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var debit finance.Money
	for _, e := range tx.Entries {
		if e.Side == finance.SideDebit {
			debit += e.Amount
		}
	}
	if debit > f.balance {
		return 0, errors.New("insufficient funds")
	}
	f.balance -= debit
	f.txns = append(f.txns, tx)
	return finance.TxID(len(f.txns)), nil
}

func (f *balanceCappedFinance) debitTotal() finance.Money {
	f.mu.Lock()
	defer f.mu.Unlock()
	var total finance.Money
	for _, tx := range f.txns {
		for _, e := range tx.Entries {
			if e.Side == finance.SideDebit {
				total += e.Amount
			}
		}
	}
	return total
}

func (f *balanceCappedFinance) setBalance(b finance.Money) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.balance = b
}

// TestRepealRestoresTaxMultiplier (defect 1): Repeal must restore the district
// multiplier Enact applied — a repealed freeport (delta -1.0) must not leave
// the district's multiplier at 0.0.
func TestRepealRestoresTaxMultiplier(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	rec := &recordingTax{}
	a.tax = rec

	districtID, err := a.CreateDistrict("Harbour", cells(1))
	if err != nil {
		t.Fatalf("CreateDistrict: %v", err)
	}

	addPolicy(t, a, taxPolicy("freeport", "tax.businessRates.districtMultiplier", -1.0, "business-rates"))
	eid := mustEnact(t, a, "freeport", Scope{Kind: ScopeDistrict, District: districtID})
	if got := lastTaxMultiplier(t, rec); !floatApprox(got, 0.0) {
		t.Fatalf("freeport enact must set the district multiplier to 0.0, got %v", got)
	}

	if err := a.Repeal(eid); err != nil {
		t.Fatalf("Repeal: %v", err)
	}
	if got := lastTaxMultiplier(t, rec); !floatApprox(got, 1.0) {
		t.Fatalf("repeal must restore the pre-enactment multiplier 1.0, got %v (left at 0.0)", got)
	}
	if got := len(a.CoefficientState()); got != 0 {
		t.Fatalf("repeal must leave no active coefficients, got %d", got)
	}
}

// TestAdvanceMonthOpexAtomicOnMidMonthFailure (defect 2): a mid-loop opex
// failure must leave NO opex posted and the clock unmoved, so a retry posts the
// full month exactly once (no double-debit).
func TestAdvanceMonthOpexAtomicOnMidMonthFailure(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	fin := &balanceCappedFinance{balance: 75_000}
	a.finance = fin

	defA := simplePolicy("policyA", ScopeCitywide, "economy.wage.level", 0.10)
	defA.Cost = CostDef{OpexMonthlyMicroPounds: 50_000}
	defB := simplePolicy("policyB", ScopeCitywide, "economy.wage.level", 0.20)
	defB.Cost = CostDef{OpexMonthlyMicroPounds: 50_000}
	addPolicy(t, a, defA)
	addPolicy(t, a, defB)
	mustEnact(t, a, "policyA", Scope{Kind: ScopeCitywide})
	mustEnact(t, a, "policyB", Scope{Kind: ScopeCitywide})

	// The treasury can cover policyA's line but not both: the month's combined
	// opex (100k) must be rejected atomically — policyA's 50k is NOT left
	// posted with the clock unmoved.
	if _, err := a.AdvanceMonth(1); err == nil {
		t.Fatal("AdvanceMonth overdrawing the treasury must fail")
	}
	if got := fin.debitTotal(); got != 0 {
		t.Fatalf("failed month must not leave policyA's opex posted, got %d", got)
	}

	// Replenish and retry the same month: the full month posts exactly once
	// (100k), never policyA twice.
	fin.setBalance(200_000)
	if _, err := a.AdvanceMonth(1); err != nil {
		t.Fatalf("retry AdvanceMonth: %v", err)
	}
	if got := fin.debitTotal(); got != finance.Money(100_000) {
		t.Fatalf("retry must post both opex lines exactly once (no double-debit), got %d", got)
	}
}

// TestFailedEnactRollbackPreservesPriorMultiplier (defect 3): a failed Enact's
// rollback must restore the prior multiplier, not reset it to neutral 1.0 and
// clobber a prior still-active policy on the same district+instrument.
func TestFailedEnactRollbackPreservesPriorMultiplier(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	rec := &recordingTax{}
	a.tax = rec
	a.finance = failAllFinance{}

	districtID, err := a.CreateDistrict("Harbour", cells(1))
	if err != nil {
		t.Fatalf("CreateDistrict: %v", err)
	}

	// Policy A: active, halves the multiplier (0.5), no enactment cost.
	addPolicy(t, a, taxPolicy("freeportA", "tax.businessRates.districtMultiplier", -0.5, "business-rates"))
	mustEnact(t, a, "freeportA", Scope{Kind: ScopeDistrict, District: districtID})
	if got := lastTaxMultiplier(t, rec); !floatApprox(got, 0.5) {
		t.Fatalf("policy A must apply multiplier 0.5, got %v", got)
	}

	// Policy B: same district+instrument, but its enactment cost posting fails
	// — the rollback must preserve A's 0.5, not reset to neutral 1.0.
	defB := taxPolicy("freeportB", "tax.businessRates.districtMultiplier", -0.5, "business-rates")
	defB.Cost = CostDef{EnactmentMicroPounds: 5_000_000}
	addPolicy(t, a, defB)
	if _, err := a.Enact("freeportB", Scope{Kind: ScopeDistrict, District: districtID}); err == nil {
		t.Fatal("enactment with failing finance must error")
	}
	if got := lastTaxMultiplier(t, rec); !floatApprox(got, 0.5) {
		t.Fatalf("failed enact rollback must preserve policy A's 0.5 multiplier, got %v", got)
	}
}

// TestTaxMoveMatchesCombinedEffect (defect 4): tax moves must compose
// multiplicatively (two -0.5 moves -> 0.25) so the applied multiplier equals
// what CombinedEffect reports (1 + CombinedEffect).
func TestTaxMoveMatchesCombinedEffect(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	rec := &recordingTax{}
	a.tax = rec

	districtID, err := a.CreateDistrict("Harbour", cells(1))
	if err != nil {
		t.Fatalf("CreateDistrict: %v", err)
	}

	const key = "tax.businessRates.districtMultiplier"
	addPolicy(t, a, taxPolicy("freeportA", key, -0.5, "business-rates"))
	addPolicy(t, a, taxPolicy("freeportB", key, -0.5, "business-rates"))
	mustEnact(t, a, "freeportA", Scope{Kind: ScopeDistrict, District: districtID})
	mustEnact(t, a, "freeportB", Scope{Kind: ScopeDistrict, District: districtID})

	applied := lastTaxMultiplier(t, rec)
	combined, err := a.CombinedEffect(key, Scope{Kind: ScopeDistrict, District: districtID})
	if err != nil {
		t.Fatalf("CombinedEffect: %v", err)
	}
	if !floatApprox(applied, 1.0+combined) {
		t.Fatalf("applied multiplier %v must equal 1+CombinedEffect %v", applied, 1.0+combined)
	}
	if !floatApprox(applied, 0.25) {
		t.Fatalf("two -0.5 moves must compose multiplicatively to 0.25, got %v", applied)
	}
}

// TestOutOfBandTaxMultiplierReadBack (defect 5 — MOD-064 REJECT r2): an
// out-of-band mutation of the applied district multiplier must be read by the
// next Enact, never silently clobbered by a stale policies-side mirror.
//
// Exact repro: two -0.5 freeport moves compose the real multiplier to 0.25; an
// external actor then sets the real multiplier to 0.9. The old mirror still
// said 0.25, so the next -0.5 move wrote 0.25 × 0.5 = 0.125, silently
// destroying the real 0.9. Getter-first, the next move must write
// 0.9 × 0.5 = 0.45.
func TestOutOfBandTaxMultiplierReadBack(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	rec := &recordingTax{}
	a.tax = rec

	districtID, err := a.CreateDistrict("Harbour", cells(1))
	if err != nil {
		t.Fatalf("CreateDistrict: %v", err)
	}

	const key = "tax.businessRates.districtMultiplier"
	addPolicy(t, a, taxPolicy("freeportA", key, -0.5, "business-rates"))
	addPolicy(t, a, taxPolicy("freeportB", key, -0.5, "business-rates"))
	addPolicy(t, a, taxPolicy("freeportC", key, -0.5, "business-rates"))
	mustEnact(t, a, "freeportA", Scope{Kind: ScopeDistrict, District: districtID})
	mustEnact(t, a, "freeportB", Scope{Kind: ScopeDistrict, District: districtID})
	if got := lastTaxMultiplier(t, rec); !floatApprox(got, 0.25) {
		t.Fatalf("two -0.5 moves must compose to 0.25, got %v", got)
	}

	// Out-of-band mutation: the real applied multiplier is now 0.9, while the
	// (now-deleted) policies-side mirror would still have said 0.25.
	if err := rec.SetDistrictMultiplier(tax.DistrictID(districtID), "business-rates", 0.9); err != nil {
		t.Fatalf("out-of-band SetDistrictMultiplier: %v", err)
	}

	// The next Enact must read the real 0.9 and compose 0.9 × 0.5 = 0.45 —
	// never the stale mirror's 0.25 × 0.5 = 0.125.
	mustEnact(t, a, "freeportC", Scope{Kind: ScopeDistrict, District: districtID})
	if got := lastTaxMultiplier(t, rec); !floatApprox(got, 0.45) {
		t.Fatalf("next Enact must read the real 0.9 (got %v, want 0.45 — the stale mirror would write 0.125)", got)
	}
}
