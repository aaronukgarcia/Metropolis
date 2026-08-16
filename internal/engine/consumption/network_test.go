package consumption

import "testing"

// TestNetworkLossByDistance is AC-6's first half: a longer pipe/wire run
// loses more than a shorter one for the same network (loss is monotonically
// increasing in total edge length).
func TestNetworkLossByDistance(t *testing.T) {
	short := NewNetwork(UtilityWater, testCorrelationID())
	if err := short.AddEdge(Edge{From: "a", To: "b", LengthKm: 1, Corridor: true}); err != nil {
		t.Fatalf("AddEdge (short): %v", err)
	}

	long := NewNetwork(UtilityWater, testCorrelationID())
	if err := long.AddEdge(Edge{From: "a", To: "b", LengthKm: 50, Corridor: false}); err != nil {
		t.Fatalf("AddEdge (long): %v", err)
	}

	if long.LossFraction() <= short.LossFraction() {
		t.Errorf("longer run loss %v should exceed shorter run loss %v (AC-6)",
			long.LossFraction(), short.LossFraction())
	}
}

// TestSolveConserved is AC-6's binding clause: the solve is a conserved
// allocation — delivered + shortfall == demand exactly (not approximately)
// — for an under-supplied network.
func TestSolveConserved(t *testing.T) {
	n := NewNetwork(UtilityWater, testCorrelationID())
	if err := n.AddSource(Source{ID: "borehole", Type: SourceBorehole, Capacity: 60}); err != nil {
		t.Fatalf("AddSource: %v", err)
	}

	// Total demand 150, supply 60, no edges => zero loss.
	consumers := []Consumer{
		{EntityRef: "household-2", Demand: 50},
		{EntityRef: "household-1", Demand: 100},
	}
	res, err := n.Solve(consumers)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	if res.Delivered+res.ShortfallTotal != res.Demand {
		t.Errorf("conservation violated: delivered %v + shortfall %v != demand %v (AC-6)",
			res.Delivered, res.ShortfallTotal, res.Demand)
	}
	if res.Delivered != 60 {
		t.Errorf("delivered = %v, want 60 (the post-loss supply)", res.Delivered)
	}
	if res.ShortfallTotal != 90 {
		t.Errorf("shortfall = %v, want 90", res.ShortfallTotal)
	}
	if res.Loss != 0 {
		t.Errorf("loss = %v, want 0 (no edges)", res.Loss)
	}
}

// TestDeliveredPlusShortfallConserved re-asserts the invariant on a
// lossy, over-supplied network: even with a longer edge run, delivered +
// shortfall == demand holds exactly.
func TestDeliveredPlusShortfallConserved(t *testing.T) {
	n := NewNetwork(UtilityPower, testCorrelationID())
	if err := n.AddSource(Source{ID: "grid", Type: SourceSellindgeGrid, Capacity: 1000}); err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	if err := n.AddEdge(Edge{From: "grid", To: "city", LengthKm: 10, Corridor: false}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	res, err := n.Solve([]Consumer{{EntityRef: "district", Demand: 500}})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.Delivered+res.ShortfallTotal != res.Demand {
		t.Errorf("conservation violated with loss: delivered %v + shortfall %v != demand %v",
			res.Delivered, res.ShortfallTotal, res.Demand)
	}
	if res.Loss <= 0 {
		t.Errorf("loss = %v, want > 0 over a 10 km edge", res.Loss)
	}
}

// TestTotalDrawEqualsSumOfConsumerDraws proves the allocation's
// conservation/coefficient invariant: the aggregate figures are exactly the
// sum of the per-consumer figures — no units created or destroyed between
// the total draw and the per-consumer allocation. It uses a partial-serve
// scenario (supply 120 vs demand 150) so one consumer is genuinely
// under-served, and every equality is asserted exactly (the inputs are
// integer-exact floats, so the invariant holds to the last bit).
func TestTotalDrawEqualsSumOfConsumerDraws(t *testing.T) {
	n := NewNetwork(UtilityWater, testCorrelationID())
	if err := n.AddSource(Source{ID: "source", Capacity: 120}); err != nil {
		t.Fatalf("AddSource: %v", err)
	}

	res, err := n.Solve([]Consumer{
		{EntityRef: "household-1", Demand: 100},
		{EntityRef: "household-2", Demand: 50},
	})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	sumDemand := 0.0
	sumDelivered := 0.0
	sumShortfall := 0.0
	for _, a := range res.PerConsumer {
		sumDemand += a.Demand
		sumDelivered += a.Delivered
		sumShortfall += a.Shortfall
		// Per-consumer conservation holds too.
		if a.Delivered+a.Shortfall != a.Demand {
			t.Errorf("consumer %s: delivered %v + shortfall %v != demand %v",
				a.EntityRef, a.Delivered, a.Shortfall, a.Demand)
		}
	}

	if sumDemand != res.Demand {
		t.Errorf("sum of per-consumer demand %v != total demand %v (units created/destroyed)", sumDemand, res.Demand)
	}
	if sumDelivered != res.Delivered {
		t.Errorf("sum of per-consumer delivered %v != total delivered %v (units created/destroyed)", sumDelivered, res.Delivered)
	}
	if sumShortfall != res.ShortfallTotal {
		t.Errorf("sum of per-consumer shortfall %v != total shortfall %v", sumShortfall, res.ShortfallTotal)
	}
}

// TestShortfallQuery is AC-12: a per-entity shortfall query returns the
// gap between demand and delivered supply for the current (last) tick.
func TestShortfallQuery(t *testing.T) {
	n := NewNetwork(UtilityWater, testCorrelationID())
	if err := n.AddSource(Source{ID: "source", Capacity: 50}); err != nil {
		t.Fatalf("AddSource: %v", err)
	}

	if _, err := n.Solve([]Consumer{{EntityRef: "entity-a", Demand: 100}}); err != nil {
		t.Fatalf("Solve: %v", err)
	}

	s, err := n.Shortfall("entity-a")
	if err != nil {
		t.Fatalf("Shortfall: %v", err)
	}
	if s == 0 {
		t.Errorf("shortfall = 0, want non-zero for an under-supplied entity")
	}
	if s != 50 {
		t.Errorf("shortfall = %v, want 50 (100 demand - 50 delivered)", s)
	}
}

// TestNoSourceDiagnostic is AC-14: a network with zero sources returns a
// loud registry-sourced error each tick it cannot be solved, never a
// silent 100%-shortfall answer.
func TestNoSourceDiagnostic(t *testing.T) {
	n := NewNetwork(UtilityPower, testCorrelationID())
	_, err := n.Solve([]Consumer{{EntityRef: "a", Demand: 10}})
	assertCode(t, err, ErrNoSource)
}
