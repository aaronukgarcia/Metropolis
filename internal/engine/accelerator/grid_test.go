package accelerator

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/consumption"
)

// TestGridCouplingDrawResolvesThroughConsumption is AC-4: the accelerator's
// electricity/water draw is resolved by posting into engine.consumption's
// UtilityAPI through the data-sourced consumptionRef class — never a
// hardcoded draw and never a call into engine.fuel. The resolved demand
// equals the consumptionRef coefficient × the data-sourced throughput.
func TestGridCouplingDrawResolvesThroughConsumption(t *testing.T) {
	a, u := loadTestAPI(t)
	wireAll(t, a, u, a.ExpertGateThreshold())
	if err := a.Build(BuildCommand{Key: CatalogueKey}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	base, err := a.ResolvedDemand(demandOptions())
	if err != nil {
		t.Fatalf("ResolvedDemand: %v", err)
	}

	// The draw is the consumptionRef class's coefficient × the throughput —
	// read back through the same UtilityAPI (GR#15: figures come from the
	// data files, never hardcoded in this test).
	coef, err := u.ClassCoefficients("accelerator")
	if err != nil {
		t.Fatalf("ClassCoefficients: %v", err)
	}
	if base.Power != coef.ElecKWh {
		t.Errorf("power draw = %v, want coefficient %v × throughput 1", base.Power, coef.ElecKWh)
	}
	if base.Water != coef.WaterL {
		t.Errorf("water draw = %v, want coefficient %v × throughput 1", base.Water, coef.WaterL)
	}
	if base.Gas != coef.GasKWh {
		t.Errorf("gas draw = %v, want coefficient %v", base.Gas, coef.GasKWh)
	}

	// The draw is positive — a "massive" load, not a cosmetic zero.
	if base.Power <= 0 || base.Water <= 0 {
		t.Errorf("draw must be positive (power %v, water %v)", base.Power, base.Water)
	}
}

// TestPeakStacksAboveBaseAndOtherLoads is AC-5: the electricity draw is
// peak-load-aware (peak above base) and stacking the accelerator's peak with
// a separately-modelled existing load (residential, via the same UtilityAPI)
// yields a combined peak strictly greater than either alone.
func TestPeakStacksAboveBaseAndOtherLoads(t *testing.T) {
	a, u := loadTestAPI(t)
	wireAll(t, a, u, a.ExpertGateThreshold())
	if err := a.Build(BuildCommand{Key: CatalogueKey}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	opts := demandOptions()
	base, err := a.ResolvedDemand(opts)
	if err != nil {
		t.Fatalf("ResolvedDemand: %v", err)
	}
	peak, err := a.PeakDemand(opts)
	if err != nil {
		t.Fatalf("PeakDemand: %v", err)
	}

	// Peak electricity sits above base electricity (the hour-of-day peak
	// figure — §17 peak-vs-base, AC-5's false-pass guard).
	if peak.Power <= base.Power {
		t.Errorf("peak power %v must be strictly above base power %v", peak.Power, base.Power)
	}

	// A separately-modelled existing load: residential demand via the same
	// UtilityAPI (the grid-coupling precedent's "stacks with every other
	// load" clause).
	other, err := u.ResidentialDemand(1000, opts)
	if err != nil {
		t.Fatalf("ResidentialDemand: %v", err)
	}

	combined := peak.Power + other.Power
	if combined <= peak.Power {
		t.Errorf("combined peak %v must exceed the accelerator peak alone %v", combined, peak.Power)
	}
	if combined <= other.Power {
		t.Errorf("combined peak %v must exceed the other load alone %v", combined, other.Power)
	}
}

// TestDeliveredPlusShortfallConservesAcceleratorDraw is AC-6: the
// accelerator's demand participates in engine.consumption's conserved solve.
// Against an under-supplied power network, delivered + shortfall == demand
// holds including the accelerator's load, and the under-supplied network
// reports a real shortfall that includes the facility's unmet load.
func TestDeliveredPlusShortfallConservesAcceleratorDraw(t *testing.T) {
	a, u := loadTestAPI(t)
	wireAll(t, a, u, a.ExpertGateThreshold())
	if err := a.Build(BuildCommand{Key: CatalogueKey}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	// An under-supplied power network: a single tiny source, far below the
	// accelerator's draw.
	net := consumption.NewNetwork(consumption.UtilityPower, testCorrelationID())
	if err := net.AddSource(consumption.Source{
		ID:       "tiny",
		Type:     consumption.SourceGenset,
		Capacity: 1,
	}); err != nil {
		t.Fatalf("AddSource: %v", err)
	}

	res, err := u.SolveDailyTick(net, []consumption.DemandEntity{a.DemandEntity()}, demandOptions())
	if err != nil {
		t.Fatalf("SolveDailyTick: %v", err)
	}

	// Conservation: delivered + shortfall == demand, exactly (mirroring
	// engine.consumption.md AC-6's equality — ShortfallTotal is a single
	// subtraction from Demand).
	if res.Delivered+res.ShortfallTotal != res.Demand {
		t.Errorf("conservation violated: delivered %v + shortfall %v != demand %v", res.Delivered, res.ShortfallTotal, res.Demand)
	}

	// The accelerator's load is in the demand, and the under-supplied network
	// reports a real shortfall that includes the facility's unmet load.
	if res.Demand <= 0 {
		t.Errorf("demand = %v, want the accelerator's positive load included", res.Demand)
	}
	if res.ShortfallTotal <= 0 {
		t.Errorf("shortfall = %v, want a real shortfall on an under-supplied network", res.ShortfallTotal)
	}
	for _, alloc := range res.PerConsumer {
		if alloc.EntityRef == CatalogueKey && alloc.Shortfall <= 0 {
			t.Errorf("accelerator's per-consumer shortfall = %v, want > 0 on an under-supplied network", alloc.Shortfall)
		}
	}
}
