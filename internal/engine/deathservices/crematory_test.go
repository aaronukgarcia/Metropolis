package deathservices

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
)

// TestCremationConsumesNoPlotAndCostsMoney (AC-5): cremating N bodies
// consumes zero plots and charges a cost > 0.
func TestCremationConsumesNoPlotAndCostsMoney(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	deaths := []citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}, {CitizenID: 2, DeathMonth: 1}}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}

	occBefore, _, err := d.CemeteryOccupancy("cem-1", "corr")
	if err != nil {
		t.Fatalf("CemeteryOccupancy: %v", err)
	}

	cremated, cost, err := d.Cremate([]uint64{1, 2}, "crem-1", 1, "corr")
	if err != nil {
		t.Fatalf("Cremate: %v", err)
	}
	if len(cremated) != 2 {
		t.Fatalf("cremated = %v, want 2 bodies", cremated)
	}
	if cost <= 0 {
		t.Fatalf("cremation cost = %d, want > 0", cost)
	}
	perBody, err := d.PerBodyCostMicropounds("corr")
	if err != nil {
		t.Fatalf("PerBodyCostMicropounds: %v", err)
	}
	if cost != perBody*2 {
		t.Fatalf("cost = %d, want %d (perBody x 2)", cost, perBody*2)
	}

	occAfter, _, err := d.CemeteryOccupancy("cem-1", "corr")
	if err != nil {
		t.Fatalf("CemeteryOccupancy: %v", err)
	}
	if occAfter != occBefore {
		t.Fatalf("cremation consumed a plot: occupancy %d -> %d", occBefore, occAfter)
	}

	for _, id := range []uint64{1, 2} {
		b, err := d.Body(id, "corr")
		if err != nil {
			t.Fatalf("Body(%d): %v", id, err)
		}
		if b.State != BodyCremated {
			t.Fatalf("body %d state = %s, want cremated", id, b.State)
		}
	}
}

// TestCremationDailyThroughputQueuesExcess (AC-5(c)): attempting to
// cremate more than the daily throughput seed in one day queues the
// excess rather than exceeding it.
func TestCremationDailyThroughputQueuesExcess(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	throughput, err := d.DailyThroughput("corr")
	if err != nil {
		t.Fatalf("DailyThroughput: %v", err)
	}
	n := throughput + 5 // deliberately over the daily cap
	deaths := make([]citizens.RealisedDeath, n)
	ids := make([]uint64, n)
	for i := int64(0); i < n; i++ {
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
		ids[i] = uint64(i + 1)
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}

	day1, _, err := d.Cremate(ids, "crem-1", 1, "corr")
	if err != nil {
		t.Fatalf("Cremate day 1: %v", err)
	}
	if int64(len(day1)) != throughput {
		t.Fatalf("day 1 cremated %d bodies, want exactly the daily cap %d (excess must queue, not exceed)", len(day1), throughput)
	}

	backlog, err := d.AwaitingBacklog("corr")
	if err != nil {
		t.Fatalf("AwaitingBacklog: %v", err)
	}
	if int64(backlog) != n-throughput {
		t.Fatalf("backlog after day 1 = %d, want %d", backlog, n-throughput)
	}

	// Day 2: the excess ids that are STILL awaiting (a real caller queries
	// its own backlog rather than blindly re-offering already-terminal ids,
	// since Cremate rejects a re-dispose attempt with ErrBodyAlreadyHandled,
	// AC-15) drain normally.
	stillAwaiting := ids[len(day1):]
	day2, _, err := d.Cremate(stillAwaiting, "crem-1", 2, "corr")
	if err != nil {
		t.Fatalf("Cremate day 2: %v", err)
	}
	if int64(len(day2)) != n-throughput {
		t.Fatalf("day 2 cremated %d, want the remaining %d", len(day2), n-throughput)
	}
	backlogAfter, _ := d.AwaitingBacklog("corr")
	if backlogAfter != 0 {
		t.Fatalf("backlog after day 2 = %d, want 0 (fully drained)", backlogAfter)
	}
}

// TestCremationCostRoutedThroughServices (AC-6): cremation cost integrates
// with engine.services' funding/quality path (via UpdateStaffing feeding
// GrossWageCost), not a locally-invented ledger.
func TestCremationCostRoutedThroughServices(t *testing.T) {
	sv, err := services.LoadDefault("corr")
	if err != nil {
		t.Fatalf("services.LoadDefault: %v", err)
	}
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.Wire(sv, nil, "corr"); err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}

	before, err := sv.GrossWageCost(CrematoriumServiceID)
	if err != nil {
		t.Fatalf("GrossWageCost (before): %v", err)
	}

	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if _, _, err := d.Cremate([]uint64{1}, "crem-1", 1, "corr"); err != nil {
		t.Fatalf("Cremate: %v", err)
	}

	after, err := sv.GrossWageCost(CrematoriumServiceID)
	if err != nil {
		t.Fatalf("GrossWageCost (after): %v", err)
	}
	if after <= before {
		t.Fatalf("engine.services' GrossWageCost for the crematorium service did not increase after cremation: before=%d after=%d", before, after)
	}
}

// TestCremationWorksUnwiredFromServices (AC-6 boundary): a DeathServicesAPI
// never Wired to engine.services still cremates normally (AC-5's own
// accessors are self-contained); only the cross-module posting is skipped.
func TestCremationWorksUnwiredFromServices(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	cremated, cost, err := d.Cremate([]uint64{1}, "crem-1", 1, "corr")
	if err != nil {
		t.Fatalf("Cremate unwired: %v", err)
	}
	if len(cremated) != 1 || cost <= 0 {
		t.Fatalf("Cremate unwired = (%v, %d), want 1 body cremated with cost > 0", cremated, cost)
	}
}
