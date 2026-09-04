package deathservices

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestWireDrainCapacityCallsThroughToCitizens (M3): WireDrainCapacity
// reaches the registered engine.citizens outbound edge's real
// SetDeathDrainCapacity surface without error, on a genuine
// *citizens.CitizensAPI (not a mock) -- the actual wiring point a future
// composition root would call.
func TestWireDrainCapacityCallsThroughToCitizens(t *testing.T) {
	capi, err := citizens.NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("citizens.NewCitizensAPI: %v", err)
	}
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.WireDrainCapacity(capi, "corr"); err != nil {
		t.Fatalf("WireDrainCapacity: %v", err)
	}
}

// TestWireDrainCapacityNilIsNoOp (M3 boundary): a nil citizensAPI is a
// documented no-op, never a panic.
func TestWireDrainCapacityNilIsNoOp(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.WireDrainCapacity(nil, "corr"); err != nil {
		t.Fatalf("WireDrainCapacity(nil) = %v, want nil (no-op)", err)
	}
}

// TestMonthlyDrainCapacityIsLiveNotStatic (M3, the central design-intent
// check): DeathServicesAPI.MonthlyDrainCapacity shrinks as this module's
// OWN plot/cremation/hearse capacity is consumed -- never a fixed number
// -- and grows again once a fresh month's counters are due to reset.
func TestMonthlyDrainCapacityIsLiveNotStatic(t *testing.T) {
	cfg := writeConfigFixture(t, func(c *Config) {
		c.Params.HearseMonthlyTransportBudget.Value = 20
		c.Params.CremationDailyThroughputPerBody.Value = 5
	})
	d := NewDeathServicesAPI(cfg, "corr")
	if err := d.RegisterCemeteryWithCapacity("cem-1", 10, "corr"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}

	before := d.MonthlyDrainCapacity(1)
	// plots(10) + cremation(5*30=150) + hearse(20) = 180.
	wantBefore := int64(10 + 5*daysPerMonthApprox + 20)
	if int64(before) != wantBefore {
		t.Fatalf("MonthlyDrainCapacity(1) = %d, want %d (10 plots + %d cremation + 20 hearse)", before, wantBefore, 5*daysPerMonthApprox)
	}

	// Consume 4 plots via burial -- capacity must shrink by exactly 4.
	deaths := make([]citizens.RealisedDeath, 4)
	for i := 0; i < 4; i++ {
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := d.Bury(uint64(i+1), "cem-1", 1, "corr"); err != nil {
			t.Fatalf("Bury(%d): %v", i+1, err)
		}
	}
	afterBurials := d.MonthlyDrainCapacity(1)
	if int64(before-afterBurials) != 4 {
		t.Fatalf("MonthlyDrainCapacity did not shrink by exactly 4 after 4 burials: before=%d after=%d", before, afterBurials)
	}

	// Consume the hearse budget via RunHearseTransport -- capacity must
	// shrink further by the amount transported.
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 100, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	transported, _, err := d.RunHearseTransport([]uint64{100}, "cem-1", 1, "corr")
	if err != nil {
		t.Fatalf("RunHearseTransport: %v", err)
	}
	if len(transported) != 1 {
		t.Fatalf("transported = %v, want [100]", transported)
	}
	afterHearse := d.MonthlyDrainCapacity(1)
	// One more plot consumed (100 was buried) AND one hearse-budget unit
	// consumed -- capacity drops by 2 total from afterBurials.
	if int64(afterBurials-afterHearse) != 2 {
		t.Fatalf("MonthlyDrainCapacity did not shrink by 2 (1 plot + 1 hearse unit) after RunHearseTransport: afterBurials=%d afterHearse=%d", afterBurials, afterHearse)
	}

	// A NEW month resets the hearse budget component (plots stay consumed
	// -- burial is permanent) -- capacity rises again, never sticking at a
	// static floor.
	afterNewMonth := d.MonthlyDrainCapacity(2)
	if afterNewMonth <= afterHearse {
		t.Fatalf("MonthlyDrainCapacity(2) = %d, want > MonthlyDrainCapacity(1)=%d (hearse budget resets month-to-month, proving this is a LIVE read, not a static cached number)", afterNewMonth, afterHearse)
	}
}

// TestQueueReleaseTracksLiveDrainCapacity (M3, end-to-end): wiring this
// module's live capacity into a real citizens.DeathQueue via
// SetDrainCapacity/RealiseDrained (the exact mechanism WireDrainCapacity
// drives through CitizensAPI) makes the queue's realised-per-month count
// track OUR capacity, not release the whole backlog unboundedly.
func TestQueueReleaseTracksLiveDrainCapacity(t *testing.T) {
	cfg := writeConfigFixture(t, func(c *Config) {
		c.Params.HearseMonthlyTransportBudget.Value = 3
		c.Params.GraveyardPlotCapacity.Value = 3
	})
	d := NewDeathServicesAPI(cfg, "corr")
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	// Deliberately NO crematorium registered: MonthlyDrainCapacity's
	// cremation component is per-registered-crematorium
	// (dailyThroughput*30 each), so zero registered crematoria keeps this
	// test's total capacity small and easy to assert against the
	// mortality config's own (much larger) ordinary budget -- proving
	// DRAIN, not the ordinary budget, is the binding constraint below.
	deaths := make([]citizens.RealisedDeath, 3)
	for i := range deaths {
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	for i := range deaths {
		if err := d.Bury(uint64(i+1), "cem-1", 1, "corr"); err != nil {
			t.Fatalf("Bury: %v", err)
		}
	}
	// Plots now fully consumed (0 remain); only the hearse component (3)
	// is left.
	remaining := d.MonthlyDrainCapacity(1)
	wantRemaining := int64(3) // plots gone, no crematoria, hearse budget intact
	if int64(remaining) != wantRemaining {
		t.Fatalf("MonthlyDrainCapacity(1) after exhausting plots = %d, want %d", remaining, wantRemaining)
	}

	// Now prove a real citizens.DeathQueue's release is bounded by this
	// live figure: set the queue's drain to exactly `remaining`'s value
	// via THIS module (SetDrainCapacity takes our DeathServicesAPI
	// directly, since it implements citizens.DrainCapacity), enqueue far
	// more deaths than that, and confirm RealiseDrained releases no more
	// than our live capacity for month 1.
	q := citizens.NewDeathQueue()
	if err := q.SetDrainCapacity(d, "corr"); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}
	const n = 500
	for i := uint64(0); i < n; i++ {
		if err := q.Enqueue(i+1000, 1, "corr"); err != nil {
			t.Fatalf("Enqueue(%d): %v", i, err)
		}
	}
	cfgMortality, err := citizens.LoadDefaultMortalityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}
	released := q.RealiseDrained(cfgMortality, false, 1, "corr")
	if int64(len(released)) > int64(remaining) {
		t.Fatalf("queue released %d bodies in month 1, want <= our live drain capacity %d -- FEAT-087's queue is not respecting the injected live capacity", len(released), remaining)
	}
	if len(released) == 0 {
		t.Fatalf("queue released 0 bodies despite a positive live drain capacity of %d -- capacity is not actually being consulted", remaining)
	}
}
