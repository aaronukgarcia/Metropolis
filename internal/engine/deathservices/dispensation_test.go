package deathservices

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestDispensationGateActivatesOnEventFlag (AC-10): dispensation activates
// in the same tick that an Intake batch carries FEAT-087's EmergencyFlag --
// reading the SAME signal FEAT-087 stamps, never a local weather
// calculation. H5 fix (round-2): an ordinary (EmergencyFlag=false) batch
// arriving afterwards must NOT clear it -- only the external event-end
// signal (SetDispensationActive(false, ...), see
// TestAttackOrdinaryIntakeClearsActiveDispensation) may lower active.
func TestDispensationGateActivatesOnEventFlag(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")

	active, err := d.DispensationActive("corr")
	if err != nil || active {
		t.Fatalf("initial DispensationActive = (%v, %v), want (false, nil)", active, err)
	}

	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1, EmergencyFlag: true}}, "corr"); err != nil {
		t.Fatalf("Intake (emergency): %v", err)
	}
	active, err = d.DispensationActive("corr")
	if err != nil || !active {
		t.Fatalf("DispensationActive after an emergency-flagged death = (%v, %v), want (true, nil)", active, err)
	}

	// An ordinary batch must NOT clear an active event (H5) -- most deaths,
	// even DURING a declared emergency, are ordinary deaths.
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 2, DeathMonth: 2, EmergencyFlag: false}}, "corr"); err != nil {
		t.Fatalf("Intake (normal): %v", err)
	}
	active, err = d.DispensationActive("corr")
	if err != nil || !active {
		t.Fatalf("DispensationActive after an ORDINARY batch = (%v, %v), want (true, nil) -- an ordinary batch must never lower active (H5)", active, err)
	}

	// Only the external event-end signal lowers active.
	if err := d.SetDispensationActive(false, "corr"); err != nil {
		t.Fatalf("SetDispensationActive(false): %v", err)
	}
	active, err = d.DispensationActive("corr")
	if err != nil || active {
		t.Fatalf("DispensationActive after the event ended = (%v, %v), want (false, nil)", active, err)
	}
}

// TestDispensationThroughputLiftIsBoundedByData (AC-11): while
// dispensation is active, a trip may carry more than one body (contra
// AC-7's normal cap), and total monthly dispensation throughput exceeds
// the normal hearse-only budget, bounded by the data-sourced multiplier --
// directional only, never a pinned magnitude.
func TestDispensationThroughputLiftIsBoundedByData(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	vanCap := d.cfg.DispensationVanBodyCapacity()
	if vanCap <= 1 {
		t.Fatalf("data-sourced van capacity = %d, want > 1 for this test to mean anything", vanCap)
	}
	ids := make([]uint64, vanCap)
	deaths := make([]citizens.RealisedDeath, vanCap)
	for i := int64(0); i < vanCap; i++ {
		ids[i] = uint64(i + 1)
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1, EmergencyFlag: true}
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	active, _ := d.DispensationActive("corr")
	if !active {
		t.Fatalf("dispensation not active after an emergency-flagged intake batch")
	}

	dispensed, err := d.Dispense(ids, 1, "corr")
	if err != nil {
		t.Fatalf("Dispense (van-capacity trip): %v", err)
	}
	if int64(len(dispensed)) <= 1 {
		t.Fatalf("dispensed %d bodies in one trip while active, want > 1 (AC-11's lift)", len(dispensed))
	}

	monthlyBudget := d.cfg.DispensationMonthlyBudget()
	normalBudget, err := d.HearseMonthlyBudget("corr")
	if err != nil {
		t.Fatalf("HearseMonthlyBudget: %v", err)
	}
	if monthlyBudget <= normalBudget {
		t.Fatalf("dispensation monthly budget %d does not exceed the normal hearse budget %d", monthlyBudget, normalBudget)
	}
}

// TestDispensationRevertsOnEventEnd (AC-12): ending the event rejects a
// subsequent multi-body trip with a typed error and re-enforces the
// one-body cap.
func TestDispensationRevertsOnEventEnd(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	deaths := []citizens.RealisedDeath{
		{CitizenID: 1, DeathMonth: 1, EmergencyFlag: true},
		{CitizenID: 2, DeathMonth: 1, EmergencyFlag: true},
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	// While active: a multi-body trip succeeds.
	if _, err := d.Dispense([]uint64{1, 2}, 1, "corr"); err != nil {
		t.Fatalf("Dispense while active: %v", err)
	}

	// End the event via the external signal (H5: an ordinary Intake batch
	// alone no longer ends it).
	if err := d.SetDispensationActive(false, "corr"); err != nil {
		t.Fatalf("SetDispensationActive(false): %v", err)
	}
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 3, DeathMonth: 2, EmergencyFlag: false}}, "corr"); err != nil {
		t.Fatalf("Intake (event end): %v", err)
	}
	active, _ := d.DispensationActive("corr")
	if active {
		t.Fatalf("dispensation still active after the event ended")
	}

	// A subsequent multi-body attempt is rejected outright.
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 4, DeathMonth: 2}, {CitizenID: 5, DeathMonth: 2}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	_, err := d.Dispense([]uint64{4, 5}, 2, "corr")
	if err == nil {
		t.Fatalf("multi-body Dispense after event end succeeded, want ErrMultiBodyOutsideDispensation")
	}
	assertRegistryCode(t, err, ErrMultiBodyOutsideDispensation)

	// The bodies were NOT dispensed by the rejected call.
	for _, id := range []uint64{4, 5} {
		b, err := d.Body(id, "corr")
		if err != nil {
			t.Fatalf("Body(%d): %v", id, err)
		}
		if b.State != BodyAwaiting {
			t.Fatalf("body %d state = %s after a rejected multi-body dispense, want awaiting (no side effect)", id, b.State)
		}
	}

	// The one-body cap works normally post-reversion.
	dispensed, err := d.Dispense([]uint64{4}, 2, "corr")
	if err != nil {
		t.Fatalf("single-body Dispense post-reversion: %v", err)
	}
	if len(dispensed) != 1 {
		t.Fatalf("dispensed = %v, want [4]", dispensed)
	}
}

// TestDispensationWellbeingApprovalPenaltyApplied (AC-13): while
// dispensation is active, a negative wellbeing/approval penalty applies;
// it is exactly zero once inactive.
func TestDispensationWellbeingApprovalPenaltyApplied(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")

	wb, err := d.DispensationWellbeingPenalty("corr")
	if err != nil {
		t.Fatalf("DispensationWellbeingPenalty (inactive): %v", err)
	}
	if wb != 0 {
		t.Fatalf("wellbeing penalty while inactive = %g, want 0", wb)
	}
	ap, err := d.DispensationApprovalPenalty("corr")
	if err != nil {
		t.Fatalf("DispensationApprovalPenalty (inactive): %v", err)
	}
	if ap != 0 {
		t.Fatalf("approval penalty while inactive = %g, want 0", ap)
	}

	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1, EmergencyFlag: true}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}

	wb, err = d.DispensationWellbeingPenalty("corr")
	if err != nil {
		t.Fatalf("DispensationWellbeingPenalty (active): %v", err)
	}
	if !(wb < 0) {
		t.Fatalf("wellbeing penalty while active = %g, want negative", wb)
	}
	placeholderMin := d.cfg.Params.DispensationWellbeingPenalty.Value
	if wb != placeholderMin {
		t.Fatalf("wellbeing penalty = %g, want the data-sourced placeholder %g", wb, placeholderMin)
	}

	ap, err = d.DispensationApprovalPenalty("corr")
	if err != nil {
		t.Fatalf("DispensationApprovalPenalty (active): %v", err)
	}
	if !(ap < 0) {
		t.Fatalf("approval penalty while active = %g, want negative", ap)
	}
}
