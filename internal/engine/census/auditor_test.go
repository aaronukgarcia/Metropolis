package census

import (
	"testing"
)

// TestAuditorHistoryOrderAndNoDuplicate proves the auditor's series is
// monotonic, tick-keyed, and idempotent: auditing ticks 1,2,3 yields an
// ordered series, and re-auditing tick 2 creates no duplicate (AC-4).
func TestAuditorHistoryOrderAndNoDuplicate(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))

	for _, tick := range []int64{1, 2, 3} {
		if err := c.RunObservers(tick, "test"); err != nil {
			t.Fatalf("RunObservers(%d): %v", tick, err)
		}
	}
	// Re-audit tick 2 — must be a no-op.
	if err := c.RunObservers(2, "test"); err != nil {
		t.Fatalf("re-audit: %v", err)
	}

	series := c.HistorySeries()
	if len(series) != 3 {
		t.Fatalf("want 3 history entries, got %d", len(series))
	}
	for i, want := range []int64{1, 2, 3} {
		if series[i].Tick != want {
			t.Fatalf("series out of order at %d: got tick %d want %d", i, series[i].Tick, want)
		}
	}
}

// TestAuditorHistoryNotFound proves a query for a never-audited tick returns
// a documented not-found result, never a silent zero (AC-4).
func TestAuditorHistoryNotFound(t *testing.T) {
	c := newTestCensus(t)
	w := wire(t, c)
	w.citizens.set(mkCitizen(1))
	if err := c.RunObservers(1, "test"); err != nil {
		t.Fatalf("RunObservers: %v", err)
	}
	if _, ok := c.HistoryAt(999); ok {
		t.Fatalf("HistoryAt(999) should be not-found")
	}
	if _, ok := c.HistoryAt(1); !ok {
		t.Fatalf("HistoryAt(1) should be found")
	}
}
