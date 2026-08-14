package finance

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// gbp converts a whole-pounds value to micro-pounds (Money).
func gbp(p int64) Money { return Money(p) * MicropoundsPerPound }

// seedTreasury posts an external inflow into the treasury (e.g. a grant),
// giving the city spendable funds without a loan.
func seedTreasury(t *testing.T, f *FinanceAPI, amt Money) {
	t.Helper()
	if _, err := f.Post(Transaction{
		Description: "test seed grant",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideCredit, Amount: amt, Category: Category("seed")},
			{Account: AcctExternal, Side: SideDebit, Amount: amt, Category: Category("seed")},
		},
	}); err != nil {
		t.Fatalf("seedTreasury: %v", err)
	}
}

// allowAllGate reports every tier reached.
type allowAllGate struct{}

func (allowAllGate) MilestoneReached(tier int) bool { return true }

// denyGate reports no tier reached.
type denyGate struct{}

func (denyGate) MilestoneReached(tier int) bool { return false }

// hasCode reports whether err is a registry error carrying the given code
// (via errs.E.Is's code match).
func hasCode(err error, code string) bool {
	var e *errs.E
	return errors.As(err, &e) && e.Code == code
}
