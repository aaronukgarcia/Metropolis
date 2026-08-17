package maintenance

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// TestSpendSettlesToFinanceOpex proves AC-10's substance (not just the grep):
// maintenance spend — crew cost for the applied work plus contractor cost for
// the un-met remainder — settles through engine.finance's SettleOpex surface,
// never a maintenance-local ledger. The expected figure is derived from the
// config's cost placeholders and the crew-day result (GR#15), never a
// hardcoded literal.
func TestSpendSettlesToFinanceOpex(t *testing.T) {
	a := newTestAPI(t)
	f := finance.NewFinanceAPI("test")
	// Fund the treasury's credit line so the opex debit can post (a fresh
	// FinanceAPI opens a zero-balance treasury).
	if err := f.SetCreditLine(finance.AcctTreasury, finance.Money(1_000_000_000)); err != nil {
		t.Fatalf("set credit line: %v", err)
	}
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
	day, err := a.RunCrewDay("test")
	if err != nil {
		t.Fatalf("run crew day: %v", err)
	}

	// crew cost for the applied work + contractor cost for the un-met remainder.
	want := day.Applied*a.cfg.CrewCostPerEngineerDay + day.BacklogRemaining*a.cfg.ContractorCostPerEngineerDay
	if want == 0 {
		t.Fatal("expected a non-zero maintenance spend")
	}
	if got := int64(f.OpexTotal()); got != want {
		t.Fatalf("finance opex total = %d, want the settled maintenance spend %d", got, want)
	}
}
