package finance

import (
	"sync"
	"testing"
)

// TestDeterministicTotalsAcrossWorkers (AC-14) proves the same set of
// transactions posted concurrently across different worker counts yields
// identical monetary totals — money summation is independent of goroutine
// scheduling order.
func TestDeterministicTotalsAcrossWorkers(t *testing.T) {
	const n = 200
	ops := make([]Transaction, n)
	for i := range ops {
		if i%2 == 0 {
			ops[i] = Transaction{Description: "wages", Entries: []Entry{
				{Account: AcctTreasury, Side: SideDebit, Amount: gbp(1), Category: CatWages},
				{Account: AcctHouseholds, Side: SideCredit, Amount: gbp(1), Category: CatWages},
			}}
		} else {
			ops[i] = Transaction{Description: "opex", Entries: []Entry{
				{Account: AcctTreasury, Side: SideDebit, Amount: gbp(1), Category: CatOpex},
				{Account: AcctExternal, Side: SideCredit, Amount: gbp(1), Category: CatOpex},
			}}
		}
	}

	run := func(workers int) (Money, Money) {
		f := NewFinanceAPI("det")
		seedTreasury(t, f, gbp(10000))

		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := w; i < len(ops); i += workers {
					if _, err := f.Post(ops[i]); err != nil {
						t.Errorf("Post: %v", err)
					}
				}
			}(w)
		}
		wg.Wait()

		return f.TotalMoneyInCirculation(), f.RecomputeMoneyStock()
	}

	stock1, recompute1 := run(1)
	stock7, recompute7 := run(7)

	if stock1 != stock7 {
		t.Fatalf("money stock differs across worker counts: 1 worker=%d, 7 workers=%d", stock1, stock7)
	}
	if stock1 != recompute1 || stock7 != recompute7 {
		t.Fatalf("running total drifted from from-scratch recompute: %d vs %d / %d vs %d",
			stock1, recompute1, stock7, recompute7)
	}
	// 100 wages (internal, neutral) + 100 opex (external, -£1 each): seed £10000 - £100.
	if want := gbp(9900); stock1 != want {
		t.Fatalf("total money = %d, want %d", stock1, want)
	}
}
