package leisure

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestDiscretionaryHoursCommuteCoupling proves AC-2: the weekly budget is
// computed per citizen as 168 − work − sleep − chores − commute, with the
// commute term read from engine.traffic's per-citizen door-to-door figure —
// so two citizens identical except for commute differ in Discretionary by
// exactly the commute delta (never a flat citywide constant).
func TestDiscretionaryHoursCommuteCoupling(t *testing.T) {
	a, c, tr, _ := newWiredAPI(t, 1)

	var p [citizens.NumPersonalityAxes]int32
	for i := range p {
		p[i] = 50
	}
	seedCitizen(t, c, 1, 0, p, citizens.EmploymentEmployed)
	seedCitizen(t, c, 2, 0, p, citizens.EmploymentEmployed)

	tr.commute[1] = 5
	tr.commute[2] = 9

	b1, err := a.DiscretionaryHours(1, "test")
	if err != nil {
		t.Fatalf("citizen 1: %v", err)
	}
	b2, err := a.DiscretionaryHours(2, "test")
	if err != nil {
		t.Fatalf("citizen 2: %v", err)
	}

	// Work/sleep/chores/education/overtime held fixed — only commute differs.
	if b1.WorkHours != b2.WorkHours || b1.SleepHours != b2.SleepHours ||
		b1.ChoreHours != b2.ChoreHours || b1.EducationHours != b2.EducationHours {
		t.Fatalf("non-commute terms drifted: %+v vs %+v", b1, b2)
	}
	if b1.CommuteHours != 5 || b2.CommuteHours != 9 {
		t.Fatalf("commute not sourced from traffic: %v / %v", b1.CommuteHours, b2.CommuteHours)
	}

	const wantDelta = 4.0 // 9h − 5h
	if got := b1.Discretionary - b2.Discretionary; got != wantDelta {
		t.Fatalf("discretionary delta = %v, want exactly %v (the commute delta)", got, wantDelta)
	}
}

// TestOvertimeTradeOff captures the overtime trade-off directionally: adding
// overtime hours reduces a citizen's discretionary (and therefore leisure)
// time while generating a positive wage figure.
func TestOvertimeTradeOff(t *testing.T) {
	a, c, tr, _ := newWiredAPI(t, 1)

	var p [citizens.NumPersonalityAxes]int32
	p[citizens.AxisSociability] = 100 // going-out share > 0 so leisure hours are visible
	seedCitizen(t, c, 1, 0, p, citizens.EmploymentEmployed)
	tr.commute[1] = 5

	before, err := a.DiscretionaryHours(1, "test")
	if err != nil {
		t.Fatalf("before: %v", err)
	}

	if err := a.SetOvertimeHours(1, 10, "test"); err != nil {
		t.Fatalf("set overtime: %v", err)
	}
	after, err := a.DiscretionaryHours(1, "test")
	if err != nil {
		t.Fatalf("after: %v", err)
	}

	if after.Discretionary >= before.Discretionary {
		t.Fatalf("overtime must reduce discretionary hours: %v → %v", before.Discretionary, after.Discretionary)
	}
	if after.LeisureHours >= before.LeisureHours {
		t.Fatalf("overtime must reduce leisure hours: %v → %v", before.LeisureHours, after.LeisureHours)
	}
	if after.OvertimeHours != 10 {
		t.Fatalf("overtime hours not recorded: %v", after.OvertimeHours)
	}
	if after.OvertimeWage <= 0 {
		t.Fatalf("overtime must generate wages: %v", after.OvertimeWage)
	}
}
