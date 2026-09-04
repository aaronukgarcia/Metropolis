package deathservices

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

func newIntakenAPI(t *testing.T, citizenIDs ...uint64) *DeathServicesAPI {
	t.Helper()
	d := NewDeathServicesAPI(testConfig(t), "corr")
	deaths := make([]citizens.RealisedDeath, len(citizenIDs))
	for i, id := range citizenIDs {
		deaths[i] = citizens.RealisedDeath{CitizenID: id, DeathMonth: 1}
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	return d
}

// TestBurialPlotConsumption (AC-2): burying one body increments occupied
// plots by 1 and decrements available plots by 1.
func TestBurialPlotConsumption(t *testing.T) {
	d := newIntakenAPI(t, 1)
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	occBefore, capVal, err := d.CemeteryOccupancy("cem-1", "corr")
	if err != nil {
		t.Fatalf("CemeteryOccupancy: %v", err)
	}
	if occBefore != 0 {
		t.Fatalf("initial occupancy = %d, want 0", occBefore)
	}
	if capVal != d.cfg.GraveyardPlotCapacity() {
		t.Fatalf("capacity = %d, want data-sourced %d (GR#15)", capVal, d.cfg.GraveyardPlotCapacity())
	}

	if err := d.Bury(1, "cem-1", 5, "corr"); err != nil {
		t.Fatalf("Bury: %v", err)
	}
	occAfter, capAfter, err := d.CemeteryOccupancy("cem-1", "corr")
	if err != nil {
		t.Fatalf("CemeteryOccupancy: %v", err)
	}
	if occAfter != occBefore+1 {
		t.Fatalf("occupancy after burial = %d, want %d", occAfter, occBefore+1)
	}
	available := capAfter - occAfter
	availableBefore := capVal - occBefore
	if available != availableBefore-1 {
		t.Fatalf("available plots = %d, want %d (decremented by 1)", available, availableBefore-1)
	}

	b, err := d.Body(1, "corr")
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if b.State != BodyBuried || b.CemeteryID != "cem-1" {
		t.Fatalf("body after burial = %+v, want Buried at cem-1", b)
	}
}

// TestPlotReuseHorizonBoundary (AC-3): a buried plot rejects reuse for
// every tick before buriedMonth+horizon and becomes allocatable at/after
// that boundary.
func TestPlotReuseHorizonBoundary(t *testing.T) {
	cfg := writeConfigFixture(t, func(c *Config) {
		c.Params.PlotReuseHorizonMonths.Value = 6
	})
	horizon := cfg.PlotReuseHorizonMonths()
	if horizon != 6 {
		t.Fatalf("fixture horizon = %d, want 6", horizon)
	}

	d := NewDeathServicesAPI(cfg, "corr")
	if err := d.RegisterCemeteryWithCapacity("cem-1", 1, "corr"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 10}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	buriedMonth := int64(10)
	if err := d.Bury(1, "cem-1", buriedMonth, "corr"); err != nil {
		t.Fatalf("Bury: %v", err)
	}

	// Before the horizon: the single plot is full and NOT reuse-eligible,
	// so a second burial must fail with ErrNoPlotAvailable. Each probe body
	// is then cremated (not left Awaiting) -- H6's round-2 admission fix
	// (cemetery.go's awaitingAheadCountLocked) makes plot priority a
	// GLOBAL, strict age-order rule (deterministic under concurrency,
	// AC-18): a still-Awaiting older probe body would legitimately
	// outrank citizen 2 for the plot this test frees up below, which
	// would defeat the point of this specific boundary check (it wants to
	// isolate "does citizen 2 alone get the freed plot", not "does the
	// oldest still-waiting body win it", a DIFFERENT and equally-valid
	// property already covered by TestAttackDeterminismUnderPlotContention).
	for m := buriedMonth; m < buriedMonth+horizon; m++ {
		if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: uint64(1000 + m), DeathMonth: m}}, "corr"); err != nil {
			t.Fatalf("Intake at month %d: %v", m, err)
		}
		err := d.Bury(uint64(1000+m), "cem-1", m, "corr")
		if err == nil {
			t.Fatalf("Bury at month %d (< horizon boundary %d) succeeded, want ErrNoPlotAvailable", m, buriedMonth+horizon)
		}
		assertRegistryCode(t, err, ErrNoPlotAvailable)
		if _, _, err := d.Cremate([]uint64{uint64(1000 + m)}, "crem-1", m, "corr"); err != nil {
			t.Fatalf("Cremate probe body at month %d: %v", m, err)
		}
	}

	// At/after the horizon boundary: the plot becomes allocatable.
	eligible, err := d.PlotEligibleForReuse("cem-1", 1, buriedMonth+horizon, "corr")
	if err != nil {
		t.Fatalf("PlotEligibleForReuse: %v", err)
	}
	if !eligible {
		t.Fatalf("plot not eligible for reuse at month %d (buried %d, horizon %d)", buriedMonth+horizon, buriedMonth, horizon)
	}
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 2, DeathMonth: buriedMonth + horizon}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if err := d.Bury(2, "cem-1", buriedMonth+horizon, "corr"); err != nil {
		t.Fatalf("Bury at reuse boundary: %v", err)
	}
}

// TestPlotReuseHorizonIsDataDriven (AC-3): mutating the fixture's horizon
// value shifts the reuse boundary, proving the mechanism is data-driven,
// not compiled.
func TestPlotReuseHorizonIsDataDriven(t *testing.T) {
	run := func(horizon int64) bool {
		cfg := writeConfigFixture(t, func(c *Config) {
			c.Params.PlotReuseHorizonMonths.Value = float64(horizon)
		})
		d := NewDeathServicesAPI(cfg, "corr")
		if err := d.RegisterCemeteryWithCapacity("cem-1", 1, "corr"); err != nil {
			t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
		}
		if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 0}}, "corr"); err != nil {
			t.Fatalf("Intake: %v", err)
		}
		if err := d.Bury(1, "cem-1", 0, "corr"); err != nil {
			t.Fatalf("Bury: %v", err)
		}
		eligible, err := d.PlotEligibleForReuse("cem-1", 1, 3, "corr")
		if err != nil {
			t.Fatalf("PlotEligibleForReuse: %v", err)
		}
		return eligible
	}

	if run(10) {
		t.Fatalf("horizon=10, month=3 reported eligible, want not yet (boundary should not have shifted early)")
	}
	if !run(3) {
		t.Fatalf("horizon=3, month=3 reported NOT eligible -- boundary did not move with the data value")
	}
}

// TestFullCemeteryLandPressureTriage (AC-4): filling a cemetery to
// capacity and attempting a further burial returns a typed
// no-plot-available error -- never a silent wraparound/overflow.
func TestFullCemeteryLandPressureTriage(t *testing.T) {
	const capacity = 3
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemeteryWithCapacity("cem-1", capacity, "corr"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	deaths := make([]citizens.RealisedDeath, capacity+1)
	for i := range deaths {
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	for i := 0; i < capacity; i++ {
		if err := d.Bury(uint64(i+1), "cem-1", 1, "corr"); err != nil {
			t.Fatalf("Bury(%d): %v", i+1, err)
		}
	}
	occ, capVal2, err := d.CemeteryOccupancy("cem-1", "corr")
	if err != nil || occ != capVal2 {
		t.Fatalf("cemetery not fully occupied before overflow attempt: occ=%d cap=%d err=%v", occ, capVal2, err)
	}

	// The overflow attempt: none of the existing plots are reuse-eligible
	// (horizon is hundreds of months by default), so this must be a
	// documented triage rejection, never a silent capacity extension.
	err = d.Bury(uint64(capacity+1), "cem-1", 1, "corr")
	if err == nil {
		t.Fatalf("Bury into a full cemetery succeeded -- silent overflow/wraparound, land pressure erased")
	}
	assertRegistryCode(t, err, ErrNoPlotAvailable)

	// The overflowing body stays Awaiting (fallback-routable), and the
	// cemetery's occupancy is UNCHANGED by the rejected attempt.
	b, err := d.Body(uint64(capacity+1), "corr")
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if b.State != BodyAwaiting {
		t.Fatalf("overflowing body state = %s, want awaiting (fallback-routable)", b.State)
	}
	occAfter, capAfter, _ := d.CemeteryOccupancy("cem-1", "corr")
	if occAfter != occ || capAfter != capVal2 {
		t.Fatalf("cemetery occupancy/capacity changed by a rejected burial: before=(%d,%d) after=(%d,%d)", occ, capVal2, occAfter, capAfter)
	}
}

// TestFullCemeteryFallsBackToCremation (AC-4): the documented fallback
// route -- a body rejected by a saturated cemetery can still be disposed
// of via cremation, proving the triage path is real, not a dead end.
func TestFullCemeteryFallsBackToCremation(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemeteryWithCapacity("cem-1", 1, "corr"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}, {CitizenID: 2, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if err := d.Bury(1, "cem-1", 1, "corr"); err != nil {
		t.Fatalf("Bury(1): %v", err)
	}
	if err := d.Bury(2, "cem-1", 1, "corr"); err == nil {
		t.Fatalf("Bury(2) into a full cemetery succeeded, want ErrNoPlotAvailable")
	}
	cremated, cost, err := d.Cremate([]uint64{2}, "crem-1", 1, "corr")
	if err != nil {
		t.Fatalf("Cremate fallback: %v", err)
	}
	if len(cremated) != 1 || cremated[0] != 2 {
		t.Fatalf("Cremate fallback cremated = %v, want [2]", cremated)
	}
	if cost <= 0 {
		t.Fatalf("cremation cost = %d, want > 0", cost)
	}
}
