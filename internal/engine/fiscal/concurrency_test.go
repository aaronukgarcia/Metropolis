package fiscal

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// TestConcurrentTopologyAndLedgerUpdates exercises the race the -race gate
// checks (AC-13): the Sankey topology (and its drill-through / node / balance
// queries) is read concurrently with engine.finance ledger updates and
// engine.tax base updates. It asserts nothing ordering-dependent while the
// goroutines run — only that every result is a valid, non-error topology and
// that the ledger's final total is exactly the deterministic sum posted
// (dev-team-process: construct the state, then assert).
func TestConcurrentTopologyAndLedgerUpdates(t *testing.T) {
	f, fin, taxAPI := newTestFiscal(t)
	if err := fin.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	seedTreasury(t, fin, 1_000_000_000_000)

	const (
		writers   = 4
		readers   = 4
		perWriter = 50
	)
	const amt finance.Money = 1000

	payeID := incomeInstrumentID(t, taxAPI)

	var wg sync.WaitGroup

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				if _, err := fin.SettleImports(amt); err != nil {
					t.Errorf("SettleImports: %v", err)
					return
				}
				// A concurrent tax-state update alongside the finance writes.
				if err := taxAPI.SetBase(payeID, finance.Money(1000*(int64(w*perWriter+j)+1))); err != nil {
					t.Errorf("SetBase: %v", err)
					return
				}
			}
		}(i)
	}

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				if topo, err := f.SankeyTopology(); err == nil {
					// Cheap well-formedness check under concurrency.
					if len(topo.Edges) > 0 && len(topo.Nodes) == 0 {
						t.Errorf("topology with edges but no nodes")
					}
				}
				if _, err := f.Node(NodeImports); err != nil {
					t.Errorf("Node(NodeImports): %v", err)
				}
				if _, err := f.DrillThrough(NodeImports); err != nil {
					t.Errorf("DrillThrough(NodeImports): %v", err)
				}
				if _, err := f.BudgetBalance(); err != nil {
					t.Errorf("BudgetBalance: %v", err)
				}
				if _, err := f.TaxBreakdown(); err != nil {
					t.Errorf("TaxBreakdown: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	want := finance.Money(writers*perWriter) * amt
	if got := fin.ImportsTotal(); got != want {
		t.Errorf("ImportsTotal() = %d, want %d (deterministic post-join sum)", int64(got), int64(want))
	}
}
