package roads

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// TestPotholesRaiseCostLowerSpeed (US-6) asserts that an under-maintained
// (aged) road has a lower effective speed and a higher cost multiplier than
// a freshly-maintained one — maintenance is a real, felt trade-off.
func TestPotholesRaiseCostLowerSpeed(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)

	fresh, err := a.MaintenanceState(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Condition != 1.0 {
		t.Fatalf("fresh condition = %v, want 1.0", fresh.Condition)
	}
	if fresh.CostMultiplier != 1.0 {
		t.Fatalf("fresh cost multiplier = %v, want 1.0", fresh.CostMultiplier)
	}

	if err := a.Advance(100); err != nil {
		t.Fatal(err)
	}
	aged, err := a.MaintenanceState(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if aged.Condition >= fresh.Condition {
		t.Fatalf("aged condition %v not below fresh %v", aged.Condition, fresh.Condition)
	}
	if aged.EffectiveSpeedKPH >= fresh.EffectiveSpeedKPH {
		t.Fatalf("aged effective speed %v not below fresh %v", aged.EffectiveSpeedKPH, fresh.EffectiveSpeedKPH)
	}
	if aged.CostMultiplier <= fresh.CostMultiplier {
		t.Fatalf("aged cost multiplier %v not above fresh %v", aged.CostMultiplier, fresh.CostMultiplier)
	}
}

// TestRepairRestoresCondition (US-6) asserts spending money on repair
// improves the condition and never overshoots perfect (1.0).
func TestRepairRestoresCondition(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)
	if err := a.Advance(100); err != nil {
		t.Fatal(err)
	}
	before, _ := a.MaintenanceState(r.ID)

	if err := a.RepairRoad(RepairRoadCommand{CorrelationID: "test", RoadID: r.ID, AmountMicropounds: int64(det.FromPounds(2000))}); err != nil {
		t.Fatal(err)
	}
	after, _ := a.MaintenanceState(r.ID)
	if after.Condition <= before.Condition {
		t.Fatalf("condition after repair %v not above before %v", after.Condition, before.Condition)
	}
	if after.Condition > 1.0 {
		t.Fatalf("condition after repair %v overshoots 1.0", after.Condition)
	}
}

// TestNegativeRepairRejected asserts a negative repair amount is rejected.
func TestNegativeRepairRejected(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)
	if err := a.RepairRoad(RepairRoadCommand{CorrelationID: "test", RoadID: r.ID, AmountMicropounds: -1}); err == nil {
		t.Fatal("negative repair amount accepted, want error")
	}
}
