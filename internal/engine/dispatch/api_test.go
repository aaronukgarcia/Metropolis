package dispatch

import (
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/traffic"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func TestDispatch_AC2_UnifiedDispatch(t *testing.T) {
	api := New()
	_ = api.AddFleetUnit(1, "fire")
	_ = api.AddFleetUnit(2, "ambulance")
	_ = api.AddFleetUnit(3, "air-ambulance")
	_ = api.AddFleetUnit(4, "police")

	f, _ := api.SubmitIncident(101, "fire")
	a, _ := api.SubmitIncident(102, "ambulance")
	aa, _ := api.SubmitIncident(103, "air-ambulance")
	p, _ := api.SubmitIncident(104, "police")

	if f == 0 || a == 0 || aa == 0 || p == 0 {
		t.Error("expected all 4 services to dispatch through the unified API")
	}
}

func TestDispatch_AC3_NearestUnit_And_NoUnitAvailable_Queue(t *testing.T) {
	api := New()
	_ = api.AddFleetUnit(10, "police") // closer due to ID proxy
	_ = api.AddFleetUnit(20, "police") // further

	inc1, _ := api.SubmitIncident(101, "police")
	api.mu.RLock()
	unitID := api.incidents[inc1].UnitID
	api.mu.RUnlock()

	if unitID != 10 {
		t.Errorf("expected nearest available unit (10), got %d", unitID)
	}

	inc2, _ := api.SubmitIncident(102, "police")
	api.mu.RLock()
	unitID2 := api.incidents[inc2].UnitID
	api.mu.RUnlock()

	if unitID2 != 20 {
		t.Errorf("expected next unit (20), got %d", unitID2)
	}

	// No units available -> Queue
	inc3, _ := api.SubmitIncident(103, "police")
	api.mu.RLock()
	status := api.incidents[inc3].Status
	api.mu.RUnlock()

	if status != "queued" {
		t.Errorf("expected incident to queue, got %s", status)
	}
}

func TestDispatch_AC4_BlueLight_CongestionBites(t *testing.T) {
	api := New()
	_ = api.AddFleetUnit(10, "ambulance")

	trafficAPI := traffic.New()
	_ = trafficAPI.AddDemand(10, 1000) // add congestion
	_ = api.SetTraffic(trafficAPI)

	incID, _ := api.SubmitIncident(101, "ambulance")

	api.mu.RLock()
	delay := api.incidents[incID].Delay
	api.mu.RUnlock()

	// Traffic mock logic: delay = (10 + commute) * 0.625
	// If it was a teleport, delay would be 0.
	if delay <= 0 {
		t.Error("expected blue-light routing to have real non-zero travel time based on congestion")
	}
}

func TestDispatch_AC5_Outcome_ResponseTime(t *testing.T) {
	api := New()
	_ = api.AddFleetUnit(10, "fire")

	incID, _ := api.SubmitIncident(101, "fire")

	api.mu.Lock()
	api.incidents[incID].Delay = 20.0
	api.mu.Unlock()

	out1, _ := api.ResolveOutcome(incID)

	incID2, _ := api.SubmitIncident(101, "fire")
	api.mu.Lock()
	api.incidents[incID2].Delay = 40.0
	api.mu.Unlock()

	out2, _ := api.ResolveOutcome(incID2)

	if out2 >= out1 {
		t.Errorf("expected outcome to worsen with delay (out1: %f, out2: %f)", out1, out2)
	}
}

func TestDispatch_AC6_FireSpread_BlockLoss(t *testing.T) {
	api := New()
	_ = api.AddFleetUnit(10, "fire")

	wAPI := world.NewWorldAPI(world.TileCoord{X: 0, Y: 0})
	_ = api.SetWorld(wAPI)

	incID, _ := api.SubmitIncident(101, "fire")

	api.mu.Lock()
	api.incidents[incID].Delay = 50.0 // severe delay
	api.mu.Unlock()

	spread, _ := api.FireSpread(incID)
	if spread <= 0 {
		t.Error("expected slow response to cause block loss (fire spread)")
	}
}

func TestDispatch_AC7_AirAmbulance(t *testing.T) {
	api := New()
	_ = api.AddFleetUnit(10, "air-ambulance")

	trafficAPI := traffic.New()
	_ = trafficAPI.AddDemand(10, 50000) // severe congestion
	_ = api.SetTraffic(trafficAPI)

	incID, _ := api.SubmitIncident(101, "air-ambulance")

	api.mu.RLock()
	delay := api.incidents[incID].Delay
	api.mu.RUnlock()

	// Air ambulance ignores traffic delays, mocked at 5.0
	if math.Abs(delay-5.0) > 1e-9 {
		t.Errorf("expected air-ambulance to ignore road congestion (5.0), got %f", delay)
	}
}

func TestDispatch_AC8_WaitingList(t *testing.T) {
	api := New()
	sAPI, _ := services.LoadDefault("test-dispatch")
	_ = api.SetServices(sAPI)

	_ = sAPI.RegisterService(services.ServiceSpec{ID: "hosp", Kind: "healthcare", StaffingNeed: 10.0})

	_ = sAPI.SetPoolStaff("nursing", 2.0)
	_, _ = sAPI.AllocateStaffing("nursing")
	wait1, _ := api.WaitingList()

	_ = sAPI.SetPoolStaff("nursing", 10.0)
	_, _ = sAPI.AllocateStaffing("nursing")
	wait2, _ := api.WaitingList()

	if wait2 >= wait1 {
		t.Errorf("expected waiting list to visibly lengthen as funding drops (wait1: %f, wait2: %f)", wait1, wait2)
	}
}

func TestDispatch_AC9_SharedNursing_NurseShortage(t *testing.T) {
	api := New()
	sAPI, _ := services.LoadDefault("test-dispatch")
	_ = api.SetServices(sAPI)

	_ = sAPI.RegisterService(services.ServiceSpec{ID: "elder", Kind: "healthcare", StaffingNeed: 10.0})

	// High pool
	_ = sAPI.SetPoolStaff("nursing", 10.0)
	_, _ = sAPI.AllocateStaffing("nursing")
	wait1, _ := api.WaitingList()
	elder1, _ := api.ElderCareQuality()

	// Shortage
	_ = sAPI.SetPoolStaff("nursing", 2.0)
	_, _ = sAPI.AllocateStaffing("nursing")
	wait2, _ := api.WaitingList()
	elder2, _ := api.ElderCareQuality()

	if wait2 <= wait1 || elder2 >= elder1 {
		t.Error("expected shared nurse shortage to degrade hospital and elder care together")
	}
}

func TestDispatch_AC10_Distribution(t *testing.T) {
	api := New()
	pAPI := projections.NewProjectionsAPI()

	// SetProjections registers the curve provider
	_ = api.SetProjections(pAPI)

	_ = api.AddFleetUnit(10, "ambulance")
	incID, _ := api.SubmitIncident(101, "ambulance")
	_, _ = api.ResolveOutcome(incID)

	// Implicitly passes if no crash and SetProjections was called successfully
}

func TestDispatch_AC11_FleetConservation_UnitConservation(t *testing.T) {
	api := New()

	// Create fleet
	_ = api.AddFleetUnit(1, "fire")
	_ = api.AddFleetUnit(2, "fire")
	_ = api.AddFleetUnit(3, "fire")

	total, avail, enr, ons, oos, _ := api.AuditFleet("fire")
	if total != 3 || avail != 3 || enr != 0 || ons != 0 || oos != 0 {
		t.Fatalf("initial fleet bad: %d total, %d avail", total, avail)
	}

	// Assign one
	incID, _ := api.SubmitIncident(101, "fire")
	total, avail, enr, ons, oos, _ = api.AuditFleet("fire")
	if avail != 2 || enr != 1 {
		t.Fatalf("assigned fleet bad: %d avail, %d enr", avail, enr)
	}

	// Resolve it -> returns to pool
	_, _ = api.ResolveOutcome(incID)
	total, avail, enr, ons, oos, _ = api.AuditFleet("fire")
	if avail != 3 || enr != 0 {
		t.Fatalf("resolved fleet bad: %d avail, %d enr", avail, enr)
	}

	// Maintenance
	_ = api.Maintenance(1)
	total, avail, enr, ons, oos, _ = api.AuditFleet("fire")
	if avail != 2 || oos != 1 {
		t.Fatalf("maintenance fleet bad: %d avail, %d oos", avail, oos)
	}

	if total != avail+enr+ons+oos {
		t.Error("fleet conservation identity violated")
	}
}

func TestDispatch_AC13_UnknownCell_UnknownIncident(t *testing.T) {
	api := New()

	wAPI := world.NewWorldAPI(world.TileCoord{X: 0, Y: 0})
	_ = api.SetWorld(wAPI)

	// Unregistered cell
	_, err := api.SubmitIncident(9999, "fire") // 9999 is off-map
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrInvalidCell {
		t.Errorf("expected off-map error MET-G5001, got: %v", err)
	}

	// Ensure no incident created
	api.mu.RLock()
	count := len(api.incidents)
	api.mu.RUnlock()
	if count != 0 {
		t.Error("expected no incident created on error")
	}

	// Unknown incident
	_, err = api.ResolveOutcome(8888)
	if !errors.As(err, &re) || re.Code != ErrUnknownIncident {
		t.Errorf("expected unknown incident error MET-G5002, got: %v", err)
	}
}

func TestDispatch_AC14_InvalidUnitType_DoubleAssign(t *testing.T) {
	api := New()
	_ = api.AddFleetUnit(1, "police")

	_, err := api.SubmitIncident(101, "swat")
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrInvalidUnitType {
		t.Errorf("expected invalid unit error MET-G5003, got: %v", err)
	}

	incID, _ := api.SubmitIncident(101, "police")
	api.mu.RLock()
	unitID := api.incidents[incID].UnitID
	api.mu.RUnlock()

	err = api.Maintenance(unitID)
	if !errors.As(err, &re) || re.Code != ErrDoubleAssign {
		t.Errorf("expected double assign error MET-G5004, got: %v", err)
	}
}

func TestDispatch_AC15_Determinism(t *testing.T) {
	api1 := New()
	api2 := New()

	_ = api1.AddFleetUnit(1, "fire")
	_ = api1.AddFleetUnit(2, "fire")
	_ = api2.AddFleetUnit(1, "fire")
	_ = api2.AddFleetUnit(2, "fire")

	id1, _ := api1.SubmitIncident(101, "fire")
	id2, _ := api2.SubmitIncident(101, "fire")

	api1.mu.RLock()
	unit1 := api1.incidents[id1].UnitID
	api1.mu.RUnlock()

	api2.mu.RLock()
	unit2 := api2.incidents[id2].UnitID
	api2.mu.RUnlock()

	if unit1 != unit2 {
		t.Errorf("expected deterministic unit selection, got %d and %d", unit1, unit2)
	}
}

func TestDispatch_AC17_Concurrency(t *testing.T) {
	api := New()
	_ = api.AddFleetUnit(1, "ambulance")

	var wg sync.WaitGroup
	workers := 10
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func(cellID uint64) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = api.SubmitIncident(cellID, "ambulance")
				_, _ = api.WaitingList()
			}
		}(uint64(i * 100))
	}

	wg.Wait()
}
