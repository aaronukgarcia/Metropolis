package maintenance

import "testing"

// TestCityWideDemandIsAggregate proves AC-8: city-wide repair demand is
// computed by aggregation over the per-instance views, never a
// separately-maintained counter that could drift out of step. The reported
// figure equals the sum of the per-instance figures the same surface reports.
func TestCityWideDemandIsAggregate(t *testing.T) {
	a := newTestAPI(t)
	ids := []StructureID{1, 2, 3, 4}
	classes := []Class{"dwelling", "heavy_industry", "dwelling", "shop"}
	for i, id := range ids {
		if err := a.Register(id, classes[i], RegisterOptions{}, "test"); err != nil {
			t.Fatalf("register %d: %v", id, err)
		}
	}
	if err := a.AdvanceMonth(120, "test"); err != nil {
		t.Fatalf("advance: %v", err)
	}

	demand, err := a.CityDemand("test")
	if err != nil {
		t.Fatalf("city demand: %v", err)
	}

	var sum int64
	for _, id := range ids {
		v, err := a.View(id, "test")
		if err != nil {
			t.Fatalf("view %d: %v", id, err)
		}
		sum += v.RepairDemandPerYear
	}
	if demand.RepairDemandPerYear != sum {
		t.Fatalf("city-wide demand %d != sum of per-instance demands %d (conservation of aggregation)", demand.RepairDemandPerYear, sum)
	}
	if demand.Total != demand.RepairDemandPerYear+demand.Backlog {
		t.Fatalf("city total %d != demand %d + backlog %d", demand.Total, demand.RepairDemandPerYear, demand.Backlog)
	}
}

// TestAggregateDemandIncludesBacklog proves AC-8's second half: the surfaced
// demand includes the accumulated backlog, so engine.staffing sees the whole
// repair obligation, not just the annual flow.
func TestAggregateDemandIncludesBacklog(t *testing.T) {
	a := newTestAPI(t)
	if err := a.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Accrue one year of demand into the backlog (under-funded).
	if err := a.AdvanceMonth(12, "test"); err != nil {
		t.Fatalf("advance: %v", err)
	}

	demand, err := a.CityDemand("test")
	if err != nil {
		t.Fatalf("city demand: %v", err)
	}
	if demand.Backlog == 0 {
		t.Fatalf("expected a non-zero accumulated backlog, got 0")
	}
	if demand.Total != demand.RepairDemandPerYear+demand.Backlog {
		t.Fatalf("total %d != demand %d + backlog %d", demand.Total, demand.RepairDemandPerYear, demand.Backlog)
	}
}
