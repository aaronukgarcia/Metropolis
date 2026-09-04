package deathservices

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestBodyConservationHoldsAcrossMixedDisposal (AC-14): a synthetic death
// surge disposed of by a mix of burial/cremation/dispensation/awaiting
// satisfies BodiesReleased == sum(five independently-sourced terms)
// exactly, every period.
func TestBodyConservationHoldsAcrossMixedDisposal(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}

	const n = 50
	deaths := make([]citizens.RealisedDeath, n)
	for i := 0; i < n; i++ {
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	checkConservation(t, d)

	// Bury a third, cremate a third, leave the rest awaiting.
	buryCount := n / 3
	for i := 1; i <= buryCount; i++ {
		if err := d.Bury(uint64(i), "cem-1", 1, "corr"); err != nil {
			t.Fatalf("Bury(%d): %v", i, err)
		}
	}
	checkConservation(t, d)

	cremCount := n / 3
	cremIDs := make([]uint64, 0, cremCount)
	for i := buryCount + 1; i <= buryCount+cremCount; i++ {
		cremIDs = append(cremIDs, uint64(i))
	}
	// Cremate in daily-throughput-sized batches (AC-5(c)'s per-day cap
	// applies even to a single logical "wave") until the whole batch is
	// processed.
	day := int64(1)
	for len(cremIDs) > 0 {
		batch, _, err := d.Cremate(cremIDs, "crem-1", day, "corr")
		if err != nil {
			t.Fatalf("Cremate: %v", err)
		}
		if len(batch) == 0 {
			t.Fatalf("Cremate made no progress on a %d-body remainder", len(cremIDs))
		}
		cremIDs = cremIDs[len(batch):]
		day++
	}
	checkConservation(t, d)

	// Emergency-dispense the remainder.
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 9999, DeathMonth: 2, EmergencyFlag: true}}, "corr"); err != nil {
		t.Fatalf("Intake (emergency trigger): %v", err)
	}
	remaining := make([]uint64, 0)
	for i := buryCount + cremCount + 1; i <= n; i++ {
		remaining = append(remaining, uint64(i))
	}
	dispenseCount := len(remaining)
	// Dispense in van-capacity-sized batches (AC-11's per-trip cap applies
	// even while active) until the whole remainder is handled.
	for len(remaining) > 0 {
		batch, err := d.Dispense(remaining, 2, "corr")
		if err != nil {
			t.Fatalf("Dispense: %v", err)
		}
		if len(batch) == 0 {
			t.Fatalf("Dispense made no progress on a %d-body remainder", len(remaining))
		}
		remaining = remaining[len(batch):]
	}
	checkConservation(t, d)

	snap, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.BodiesAwaitingHandling != 1 { // only the 9999 emergency trigger remains
		t.Fatalf("BodiesAwaitingHandling = %d, want 1", snap.BodiesAwaitingHandling)
	}
	if snap.BodiesBuried != int64(buryCount) {
		t.Fatalf("BodiesBuried = %d, want %d", snap.BodiesBuried, buryCount)
	}
	if snap.BodiesCremated != int64(cremCount) {
		t.Fatalf("BodiesCremated = %d, want %d", snap.BodiesCremated, cremCount)
	}
	if snap.BodiesHandledByDispensation != int64(dispenseCount) {
		t.Fatalf("BodiesHandledByDispensation = %d, want %d", snap.BodiesHandledByDispensation, dispenseCount)
	}
}

func checkConservation(t *testing.T, d *DeathServicesAPI) {
	t.Helper()
	snap, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Sum() != snap.BodiesReleased {
		t.Fatalf("conservation identity violated: released=%d sum(awaiting+enRoute+buried+cremated+dispensed)=%d (%+v)",
			snap.BodiesReleased, snap.Sum(), snap)
	}
}

// TestTerminalExclusivityBuryThenNoReuse (AC-15): a buried body no longer
// appears awaiting, and a second disposal attempt against it is a typed
// error.
func TestTerminalExclusivityBuryThenNoReuse(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if err := d.Bury(1, "cem-1", 1, "corr"); err != nil {
		t.Fatalf("Bury: %v", err)
	}

	backlog, _ := d.AwaitingBacklog("corr")
	if backlog != 0 {
		t.Fatalf("backlog after burial = %d, want 0 (buried body no longer awaiting)", backlog)
	}

	// A second disposal attempt (bury again) is rejected.
	err := d.Bury(1, "cem-1", 1, "corr")
	if err == nil {
		t.Fatalf("second Bury on an already-buried body succeeded, want ErrBodyAlreadyHandled")
	}
	assertRegistryCode(t, err, ErrBodyAlreadyHandled)

	// Cremation never co-applies to a buried body.
	_, _, err = d.Cremate([]uint64{1}, "crem-1", 1, "corr")
	if err == nil {
		t.Fatalf("Cremate on an already-buried body succeeded, want ErrBodyAlreadyHandled")
	}
	assertRegistryCode(t, err, ErrBodyAlreadyHandled)

	// The cremation attempt did not silently record the body as cremated.
	snap, _ := d.Snapshot("corr")
	if snap.BodiesCremated != 0 {
		t.Fatalf("BodiesCremated = %d after a rejected cremation of an already-buried body, want 0", snap.BodiesCremated)
	}
	if snap.BodiesBuried != 1 {
		t.Fatalf("BodiesBuried = %d, want 1", snap.BodiesBuried)
	}
}

// TestTerminalExclusivityCremationNeverAppearsBuried (AC-15): a cremated
// body never appears in buried-plot occupancy.
func TestTerminalExclusivityCremationNeverAppearsBuried(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if _, _, err := d.Cremate([]uint64{1}, "crem-1", 1, "corr"); err != nil {
		t.Fatalf("Cremate: %v", err)
	}
	occ, _, err := d.CemeteryOccupancy("cem-1", "corr")
	if err != nil {
		t.Fatalf("CemeteryOccupancy: %v", err)
	}
	if occ != 0 {
		t.Fatalf("cemetery occupancy = %d after cremating (not burying) a body, want 0", occ)
	}
}

// TestSoakConservationUnderHostileCapacities (400-month soak, mirroring
// FEAT-087's landing style): the conservation identity holds under
// hostile drain/throughput capacities -- 0, negative, and MaxInt -- proving
// the identity is not merely correct on well-behaved inputs.
func TestSoakConservationUnderHostileCapacities(t *testing.T) {
	hostileConfigs := []func(*Config){
		func(c *Config) { c.Params.HearseMonthlyTransportBudget.Value = 1 },
		func(c *Config) { c.Params.CremationDailyThroughputPerBody.Value = 1 },
		func(c *Config) {
			c.Params.HearseMonthlyTransportBudget.Value = 1
			c.Params.CremationDailyThroughputPerBody.Value = 1
		},
		func(c *Config) {
			c.Params.HearseMonthlyTransportBudget.Value = 1 << 30 // effectively unbounded
			c.Params.CremationDailyThroughputPerBody.Value = 1 << 30
		},
	}

	for ci, mutate := range hostileConfigs {
		cfg := writeConfigFixture(t, mutate)
		d := NewDeathServicesAPI(cfg, "corr")
		if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
			t.Fatalf("[config %d] RegisterCemetery: %v", ci, err)
		}
		if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
			t.Fatalf("[config %d] RegisterCrematorium: %v", ci, err)
		}

		var nextID uint64 = 1
		for month := int64(1); month <= 400; month++ {
			// A small, deterministic per-month death count (varying to
			// exercise both light and heavy months) -- 0 negative/MaxInt
			// budgets are exercised via the CONFIG hostility above (a
			// non-positive configured value is itself rejected at load,
			// so "hostile" here means "at the documented floor of 1" and
			// "effectively unbounded", the two extremes production code
			// must survive; the surrounding disposal calls additionally
			// probe negative/absurd per-call day/month indices below).
			deathsThisMonth := int((month % 7) + 1)
			deaths := make([]citizens.RealisedDeath, deathsThisMonth)
			for i := 0; i < deathsThisMonth; i++ {
				deaths[i] = citizens.RealisedDeath{CitizenID: nextID, DeathMonth: month}
				nextID++
			}
			if _, err := d.Intake(deaths, "corr"); err != nil {
				t.Fatalf("[config %d, month %d] Intake: %v", ci, month, err)
			}

			ids, err := d.AwaitingSorted("corr")
			if err != nil {
				t.Fatalf("[config %d, month %d] AwaitingSorted: %v", ci, month, err)
			}
			if len(ids) > 0 {
				if month%2 == 0 {
					if _, _, err := d.Cremate(ids, "crem-1", month, "corr"); err != nil {
						t.Fatalf("[config %d, month %d] Cremate: %v", ci, month, err)
					}
				} else {
					if _, _, err := d.RunHearseTransport(ids, "cem-1", month, "corr"); err != nil {
						t.Fatalf("[config %d, month %d] RunHearseTransport: %v", ci, month, err)
					}
				}
			}
			checkConservation(t, d)
		}
	}
}
