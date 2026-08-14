package consumption

import (
	"reflect"
	"testing"
)

// TestDeterministicSolve is AC-15: the daily network solve is
// deterministic — the same demand/supply inputs produce identical solved
// allocations across repeated runs, regardless of the caller's input
// order, with allocation served in a documented sorted-EntityRef priority
// order (never map-iteration-dependent).
func TestDeterministicSolve(t *testing.T) {
	n := NewNetwork(UtilityPower, testCorrelationID())
	n.AddSource(Source{ID: "grid", Type: SourceSellindgeGrid, Capacity: 60})

	// Deliberately unordered inputs.
	consumers := []Consumer{
		{EntityRef: "zulu", Demand: 50},
		{EntityRef: "alpha", Demand: 30},
		{EntityRef: "mike", Demand: 20},
	}

	first, err := n.Solve(consumers)
	if err != nil {
		t.Fatalf("Solve (run 1): %v", err)
	}
	second, err := n.Solve(consumers)
	if err != nil {
		t.Fatalf("Solve (run 2): %v", err)
	}

	if first.Delivered != second.Delivered || first.ShortfallTotal != second.ShortfallTotal {
		t.Errorf("repeated solve diverged: run1 {delivered %v, shortfall %v} vs run2 {delivered %v, shortfall %v}",
			first.Delivered, first.ShortfallTotal, second.Delivered, second.ShortfallTotal)
	}
	if !reflect.DeepEqual(first.PerConsumer, second.PerConsumer) {
		t.Errorf("per-consumer allocations diverged across identical solves:\nrun1 %+v\nrun2 %+v",
			first.PerConsumer, second.PerConsumer)
	}

	// The allocation order is sorted by EntityRef, not input order.
	wantOrder := []string{"alpha", "mike", "zulu"}
	for i, want := range wantOrder {
		if first.PerConsumer[i].EntityRef != want {
			t.Fatalf("allocation[%d] = %q, want %q (sorted priority order, AC-15)",
				i, first.PerConsumer[i].EntityRef, want)
		}
	}

	// The same solve over a REVERSED input slice must produce the identical
	// allocation — proving input order does not leak into the result.
	reversed := []Consumer{consumers[2], consumers[1], consumers[0]}
	third, err := n.Solve(reversed)
	if err != nil {
		t.Fatalf("Solve (reversed): %v", err)
	}
	if !reflect.DeepEqual(first.PerConsumer, third.PerConsumer) {
		t.Errorf("reversed-input solve diverged from sorted-input solve:\nsorted   %+v\nreversed %+v",
			first.PerConsumer, third.PerConsumer)
	}
}
