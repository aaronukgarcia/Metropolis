package parking

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

func TestParking_AC2_InstrumentFootprints(t *testing.T) {
	fpSurface := FootprintPerSpace(Surface)
	fpMulti := FootprintPerSpace(MultiStorey)
	fpOnStreet := FootprintPerSpace(OnStreet)

	if fpMulti >= fpSurface {
		t.Errorf("expected multi-storey footprint per space (%f) < surface (%f)", fpMulti, fpSurface)
	}

	if fpOnStreet != 6.0 {
		t.Errorf("expected on-street frontage to be 6.0, got %f", fpOnStreet)
	}
}

func TestParking_AC3_LandAccounting(t *testing.T) {
	api := New()
	tc := world.TileCoord{X: 5, Y: 5}
	cl := world.CellLocal{Col: 2, Row: 2}

	_ = api.RegisterFacility(1, tc, cl, 100, Surface, 1)

	// Total land footprint should match SpaceCount * FootprintPerSpace
	fp, err := api.TotalLandFootprint(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedFp := 100.0 * 15.0
	if fp != expectedFp {
		t.Errorf("expected land footprint %f, got %f", expectedFp, fp)
	}

	// Reconciled zoned area
	rfp, err := api.ReconcileZonedArea(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rfp != expectedFp {
		t.Errorf("expected reconciled area %f, got %f", expectedFp, rfp)
	}
}

func TestParking_AC4_WorkplaceAllocation(t *testing.T) {
	api := New()
	tc := world.TileCoord{X: 5, Y: 5}
	cl := world.CellLocal{Col: 2, Row: 2}
	parentTc := world.TileCoord{X: 5, Y: 5}
	parentCl := world.CellLocal{Col: 1, Row: 1}

	err := api.AddWorkplaceAllocation(10, tc, cl, parentTc, parentCl, 0.5, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	alloc, ok := api.allocations[10]
	if !ok {
		t.Fatal("failed to find registered workplace allocation")
	}
	if alloc.AllocationFraction != 0.5 || alloc.Spaces != 50 {
		t.Errorf("allocation state mismatch: %+v", alloc)
	}
}

func TestParking_AC5_AC6_ChargesAndElasticity(t *testing.T) {
	api := New()
	tc := world.TileCoord{X: 5, Y: 5}
	cl := world.CellLocal{Col: 2, Row: 2}

	_ = api.RegisterFacility(1, tc, cl, 50, Surface, 1)
	_ = api.ConfigureCharges(1, 4.0, 150.0) // district 1

	// Daytime peak hour charge (12:00)
	chargeDay, err := api.EffectiveCharge(1, 12, false)
	if err != nil {
		t.Fatalf("unexpected day error: %v", err)
	}
	if chargeDay != 6.0 { // 4.0 * 1.5 multiplier
		t.Errorf("expected day charge 6.0, got %f", chargeDay)
	}

	// Nighttime hour discount (02:00)
	chargeNight, err := api.EffectiveCharge(1, 2, false)
	if err != nil {
		t.Fatalf("unexpected night error: %v", err)
	}
	if chargeNight != 2.0 { // 4.0 * 0.5 multiplier
		t.Errorf("expected night charge 2.0, got %f", chargeNight)
	}

	// ModeChoiceImpact price elasticity
	impact, err := api.ModeChoiceImpact(1, 0.8) // transit quality 0.8
	if err != nil {
		t.Fatalf("unexpected impact error: %v", err)
	}
	expectedImpact := 0.8 - (6.0 * 0.05) - (0.8 * 0.1) // 0.8 - 0.3 - 0.08 = 0.42
	if expectedImpact != impact {
		t.Errorf("expected elasticity impact %f, got %f", expectedImpact, impact)
	}
}

func TestParking_AC7_AC8_RecordArrivals_Insufficiency(t *testing.T) {
	api := New()
	tc := world.TileCoord{X: 5, Y: 5}
	cl := world.CellLocal{Col: 2, Row: 2}

	_ = api.RegisterFacility(1, tc, cl, 100, Surface, 1)

	// Arriving trips within capacity
	cload, over, err := api.RecordArrivals(1, 80)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cload != 0 || over != 0 {
		t.Errorf("expected zero load/overspill, got cload=%d, over=%d", cload, over)
	}

	// Arriving trips exceed capacity (insufficiency)
	cload2, over2, err := api.RecordArrivals(1, 150)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedCload := int(50.0 * 0.4) // 40% of excess 50 = 20
	expectedOver := int(50.0 * 0.6)  // 60% of excess 50 = 30
	if cload2 != expectedCload || over2 != expectedOver {
		t.Errorf("expected cload=%d, over=%d; got cload=%d, over=%d", expectedCload, expectedOver, cload2, over2)
	}
}

func TestParking_AC9_AutonomyAndRedevelopment(t *testing.T) {
	api := New()
	tc := world.TileCoord{X: 5, Y: 5}
	cl := world.CellLocal{Col: 2, Row: 2}

	_ = api.RegisterFacility(1, tc, cl, 100, Surface, 1)

	// Enable late-era autonomy
	_ = api.SetAutonomyEra(true)
	capVal, _ := api.Capacity(1)
	if capVal != 50 { // capacity shrunk by 50%
		t.Errorf("expected shrunk capacity 50, got %d", capVal)
	}

	// Trigger busy arrivals to reset low period
	_, _, _ = api.RecordArrivals(1, 200)

	// Attempt conversion before sustained period should fail
	err := api.ConvertToRedevelopment(1)
	if err == nil {
		t.Error("expected error converting busy facility")
	}

	// Drive sustained low occupancy (< 10% capacity, sustained for 5 periods)
	for i := 0; i < 5; i++ {
		_, _, _ = api.RecordArrivals(1, 5) // 5 is less than 10% of 100
	}

	// Convert successfully
	err = api.ConvertToRedevelopment(1)
	if err != nil {
		t.Fatalf("unexpected conversion error: %v", err)
	}

	api.mu.RLock()
	isRedev := api.facilities[1].IsRedeveloped
	api.mu.RUnlock()
	if !isRedev {
		t.Error("facility should be marked as redeveloped")
	}
}

func TestParking_AC10_NoFacilityError(t *testing.T) {
	api := New()
	_, err := api.TotalLandFootprint(999)
	if err == nil {
		t.Error("expected error for unknown facility ID")
	}
	expectedCode := ErrUnknownFacility + ": unknown destination facility ID: 999 (AC-10)"
	if err.Error() != expectedCode {
		t.Errorf("expected error matching custom code, got: %v", err)
	}
}

func TestParking_AC11_Determinism(t *testing.T) {
	api1 := New()
	api2 := New()

	tc := world.TileCoord{X: 5, Y: 5}
	cl := world.CellLocal{Col: 2, Row: 2}

	_ = api1.RegisterFacility(1, tc, cl, 100, Surface, 1)
	_ = api1.ConfigureCharges(1, 4.0, 150.0)

	_ = api2.RegisterFacility(1, tc, cl, 100, Surface, 1)
	_ = api2.ConfigureCharges(1, 4.0, 150.0)

	c1, _ := api1.EffectiveCharge(1, 12, false)
	c2, _ := api2.EffectiveCharge(1, 12, false)

	if c1 != c2 {
		t.Errorf("expected charges to be deterministic, got %f and %f", c1, c2)
	}
}

func TestParking_AC13_Concurrency(t *testing.T) {
	api := New()
	tc := world.TileCoord{X: 5, Y: 5}
	cl := world.CellLocal{Col: 2, Row: 2}
	_ = api.RegisterFacility(1, tc, cl, 100, Surface, 1)

	var wg sync.WaitGroup
	workers := 10
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _, _ = api.RecordArrivals(1, 5)
			}
		}()
	}

	wg.Wait()
}
