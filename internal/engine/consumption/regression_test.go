package consumption

import (
	"math"
	"reflect"
	"testing"
)

// This file is the regression suite for the Destructive round's safe-coercion
// findings (GR#1/GR#16) plus the AC-8 and AC-15 semantic fixes. Every test
// here failed against the pre-fix implementation and passes now.

// Defect 1: negative source capacity must be rejected, never solved into a
// negative delivery (units destroyed).
func TestNegativeSourceCapacityRejected(t *testing.T) {
	n := NewNetwork(UtilityWater, testCorrelationID())
	err := n.AddSource(Source{ID: "s", Capacity: -100})
	assertCode(t, err, ErrInvalidSource)
	if len(n.Sources()) != 0 {
		t.Errorf("negative-capacity source was appended despite rejection: %+v", n.Sources())
	}

	// Defence-in-depth: a directly-injected bad source is still rejected at
	// Solve time, not silently solved into negative delivery.
	n2 := NewNetwork(UtilityWater, testCorrelationID())
	n2.sources = []Source{{ID: "bad", Capacity: -100}}
	_, err = n2.Solve([]Consumer{{EntityRef: "a", Demand: 10}})
	assertCode(t, err, ErrInvalidSource)
}

// Defect 2: negative edge length must be rejected, never solved into a
// negative loss fraction.
func TestNegativeEdgeLengthRejected(t *testing.T) {
	n := NewNetwork(UtilityWater, testCorrelationID())
	err := n.AddEdge(Edge{From: "a", To: "b", LengthKm: -10})
	assertCode(t, err, ErrInvalidEdge)
	if len(n.Edges()) != 0 {
		t.Errorf("negative-length edge was appended despite rejection: %+v", n.Edges())
	}

	// Defence-in-depth at Solve time.
	n2 := NewNetwork(UtilityPower, testCorrelationID())
	n2.AddSource(Source{ID: "g", Capacity: 100})
	n2.edges = []Edge{{From: "a", To: "b", LengthKm: -10}}
	_, err = n2.Solve([]Consumer{{EntityRef: "a", Demand: 10}})
	assertCode(t, err, ErrInvalidEdge)
}

// Defect 3: a negative aquifer sustainable yield must be rejected, never
// yielding a negative draw.
func TestNegativeAquiferYieldRejected(t *testing.T) {
	_, err := NewAquiferYield(-100, testCorrelationID())
	assertCode(t, err, ErrInvalidAquiferYield)

	// NaN yield rejected too.
	_, err = NewAquiferYield(math.NaN(), testCorrelationID())
	assertCode(t, err, ErrInvalidAquiferYield)
}

// Defect 4: a non-finite (NaN/Inf) occupancy must be rejected by the public
// demand APIs, never propagated as a NaN demand with a nil error.
func TestNonFiniteOccupancyRejected(t *testing.T) {
	api := realAPI(t)
	opts := DemandOptions{MonthIndex: 0, GasNetworkPresent: true}

	_, err := api.ClassDemand("hospital", math.NaN(), opts)
	assertCode(t, err, ErrInvalidOccupancy)

	_, err = api.ClassDemand("hospital", math.Inf(1), opts)
	assertCode(t, err, ErrInvalidOccupancy)

	_, err = api.ResidentialDemand(math.NaN(), opts)
	assertCode(t, err, ErrInvalidOccupancy)
}

// Defect 5 (AC-8 semantic fix): the aquifer must NOT degrade when the
// network has zero demand — degradation keys off the water actually
// abstracted, not the borehole's nominal capacity.
func TestAquiferDoesNotDegradeWithoutDemand(t *testing.T) {
	aq := mustAquifer(t, 1000)
	n := NewNetwork(UtilityWater, testCorrelationID())
	if err := n.AddSource(Source{ID: "borehole", Type: SourceBorehole, Capacity: 5000, Aquifer: aq}); err != nil {
		t.Fatalf("AddSource: %v", err)
	}

	if _, err := n.Solve([]Consumer{{EntityRef: "a", Demand: 0}}); err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if aq.Current() != 1000 {
		t.Errorf("aquifer degraded with zero demand: yield %v, want 1000 (AC-8)", aq.Current())
	}
}

// Defect 5 (positive control): the aquifer still degrades when the network's
// actual abstraction exceeds the sustainable ceiling.
func TestAquiferDegradesWhenOverAbstractedViaSolve(t *testing.T) {
	aq := mustAquifer(t, 1000)
	n := NewNetwork(UtilityWater, testCorrelationID())
	n.AddSource(Source{ID: "borehole", Type: SourceBorehole, Capacity: 5000, Aquifer: aq})

	// Demand 2000 > sustainable 1000, within the borehole's 5000 capacity:
	// this is genuine over-abstraction and must degrade the yield.
	if _, err := n.Solve([]Consumer{{EntityRef: "a", Demand: 2000}}); err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if aq.Current() >= 1000 {
		t.Errorf("aquifer did not degrade under real over-abstraction: yield %v (AC-8)", aq.Current())
	}
}

// Defect 6: two individually-finite demands that sum to +Inf must be
// rejected after aggregation, never poison the conserved accounting.
func TestDemandSumOverflowRejected(t *testing.T) {
	n := NewNetwork(UtilityPower, testCorrelationID())
	n.AddSource(Source{ID: "g", Capacity: 1e308})

	const big = 1.7e308 // finite, but two of them overflow to +Inf
	_, err := n.Solve([]Consumer{
		{EntityRef: "a", Demand: big},
		{EntityRef: "b", Demand: big},
	})
	assertCode(t, err, ErrInvalidDemand)
}

// Round-2 defect 1 (supply-side overflow mirror): a 100%-loss pipe must
// deliver 0, never mask an Inf-Inf NaN as full delivery. Two huge sources +
// one huge edge drive lossFraction to exactly 1.0.
func TestHundredPercentLossDeliversZero(t *testing.T) {
	n := NewNetwork(UtilityWater, testCorrelationID())
	n.AddSource(Source{ID: "s1", Capacity: 1.7e308})
	n.AddSource(Source{ID: "s2", Capacity: 1.7e308})
	n.AddEdge(Edge{From: "s1", To: "city", LengthKm: 1.7e308})

	res, err := n.Solve([]Consumer{{EntityRef: "city", Demand: 100}})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.Delivered != 0 {
		t.Errorf("100%%-loss pipe delivered %v, want 0", res.Delivered)
	}
	if res.ShortfallTotal != 100 {
		t.Errorf("shortfall = %v, want 100", res.ShortfallTotal)
	}
	if res.Delivered+res.ShortfallTotal != res.Demand {
		t.Errorf("conservation violated: %v + %v != %v", res.Delivered, res.ShortfallTotal, res.Demand)
	}
	// No non-finite figure may leak out of the solve.
	if !isFinite(res.Delivered) || !isFinite(res.ShortfallTotal) || !isFinite(res.Produced) || !isFinite(res.Loss) {
		t.Errorf("solve leaked non-finite results: %+v", res)
	}
}

// Round-2 defect 2: AquiferYield.Abstract is a public mutator and must
// reject negative/non-finite requests, mirroring NewAquiferYield.
func TestAbstractRejectsNegativeAndNaN(t *testing.T) {
	a := mustAquifer(t, 1000)

	if _, err := a.Abstract(-100); err == nil {
		t.Fatal("Abstract(-100) returned nil error, want ErrInvalidAbstraction")
	} else {
		assertCode(t, err, ErrInvalidAbstraction)
	}
	if _, err := a.Abstract(math.NaN()); err == nil {
		t.Fatal("Abstract(NaN) returned nil error, want ErrInvalidAbstraction")
	} else {
		assertCode(t, err, ErrInvalidAbstraction)
	}
	// Yield is untouched by a rejected request.
	if a.Current() != 1000 {
		t.Errorf("yield changed after rejected abstraction: %v", a.Current())
	}
}

// Class sweep: a finite-but-huge occupancy that overflows the
// coefficient × occupancy product must be rejected, not returned as +Inf.
func TestDemandOverflowRejected(t *testing.T) {
	api := realAPI(t)
	opts := DemandOptions{MonthIndex: 0, GasNetworkPresent: true}

	_, err := api.ClassDemand("hospital", 1e308, opts)
	assertCode(t, err, ErrDemandOverflow)

	_, err = api.ResidentialDemand(1e308, opts)
	assertCode(t, err, ErrDemandOverflow)
}

// Defect 7 (AC-15): duplicate EntityRefs must not make the allocation depend
// on input order — the tie-break (descending demand) yields identical
// allocation for either input ordering.
func TestDuplicateEntityRefDeterministic(t *testing.T) {
	build := func() []Consumer {
		return []Consumer{{EntityRef: "dup", Demand: 100}, {EntityRef: "dup", Demand: 50}}
	}
	buildReversed := func() []Consumer {
		return []Consumer{{EntityRef: "dup", Demand: 50}, {EntityRef: "dup", Demand: 100}}
	}

	n1 := NewNetwork(UtilityWater, testCorrelationID())
	n1.AddSource(Source{ID: "s", Capacity: 120})
	r1, err := n1.Solve(build())
	if err != nil {
		t.Fatalf("Solve (forward): %v", err)
	}

	n2 := NewNetwork(UtilityWater, testCorrelationID())
	n2.AddSource(Source{ID: "s", Capacity: 120})
	r2, err := n2.Solve(buildReversed())
	if err != nil {
		t.Fatalf("Solve (reversed): %v", err)
	}

	if !reflect.DeepEqual(r1.PerConsumer, r2.PerConsumer) {
		t.Errorf("duplicate-EntityRef allocation depended on input order:\nforward  %+v\nreversed %+v",
			r1.PerConsumer, r2.PerConsumer)
	}
	// Tie-break: the larger demand is served first.
	if r1.PerConsumer[0].Delivered != 100 {
		t.Errorf("expected the 100-demand consumer served first, got delivered %v", r1.PerConsumer[0].Delivered)
	}
}
