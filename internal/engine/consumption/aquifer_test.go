package consumption

import "testing"

// mustAquifer constructs a valid aquifer for a test, failing the test on a
// construction error (which should never happen for a non-negative yield).
func mustAquifer(t *testing.T, sustainable float64) *AquiferYield {
	t.Helper()
	a, err := NewAquiferYield(sustainable, testCorrelationID())
	if err != nil {
		t.Fatalf("NewAquiferYield(%v): %v", sustainable, err)
	}
	return a
}

// TestAquiferYieldDegradation is AC-8: sustained over-abstraction above the
// sustainable-yield ceiling degrades future yield, and the degradation is
// CONTINUED (month over month), not one-shot — the false-pass framing
// (abstract once, degrade once, then flatline) is what this multi-month
// assertion rules out.
func TestAquiferYieldDegradation(t *testing.T) {
	a := mustAquifer(t, 1000)

	// Yield starts at the sustainable ceiling and must never exceed it.
	if a.Current() != 1000 {
		t.Fatalf("initial yield = %v, want 1000", a.Current())
	}

	prev := a.Current()
	for month := 0; month < 6; month++ {
		if _, err := a.Abstract(2000); err != nil { // over-abstraction: request above the 1000 ceiling
			t.Fatalf("month %d: Abstract: %v", month, err)
		}
		cur := a.Current()
		if cur >= prev {
			t.Fatalf("month %d: yield %v did not degrade below prior %v (continued-degradation failure, AC-8)",
				month, cur, prev)
		}
		prev = cur
	}
}

// TestAquiferYieldCapsDraw proves the ceiling is real: the aquifer never
// supplies more than its current yield even when asked to.
func TestAquiferYieldCapsDraw(t *testing.T) {
	a := mustAquifer(t, 1000)
	got, err := a.Abstract(5000)
	if err != nil {
		t.Fatalf("Abstract(5000): %v", err)
	}
	if got != 1000 {
		t.Errorf("abstracted %v, want 1000 (bounded by current yield)", got)
	}
}
