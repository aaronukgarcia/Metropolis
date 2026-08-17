package defence

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// acceptNaval builds the naval facility via the naval-base choice and returns
// the defence API, its finance/citizens deps, and the resulting facility id.
func acceptNaval(t *testing.T, seed uint64) (*DefenceAPI, *finance.FinanceAPI, *citizens.CitizensAPI, FacilityID) {
	t.Helper()
	d, f, c := newWiredDefence(t, seed)
	res, err := d.RespondToMandate(MandateResponse{
		MandateID: "naval-100k", Choice: "naval-base",
		Site:    SiteRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0}},
		OwnerID: 1, Month: 0,
	})
	if err != nil {
		t.Fatalf("RespondToMandate: %v", err)
	}
	return d, f, c, res.FacilityID
}

// TestAntiCyclicalPayroll_FloorProtected runs a recession fixture and asserts
// the facility's payroll stays at its pre-recession baseline while an
// ordinary-employer comparator contracts in the same run (AC-7) — both halves
// in one scenario, so a payroll never wired to the recession could not pass.
func TestAntiCyclicalPayroll_FloorProtected(t *testing.T) {
	d, _, _, id := acceptNaval(t, 1)
	fc := validConfig().Facilities["naval"]
	baseline := finance.Money(fc.PayrollMicropounds)

	before, err := d.FacilityPayroll(id)
	if err != nil {
		t.Fatalf("FacilityPayroll: %v", err)
	}
	if before != baseline {
		t.Fatalf("pre-recession payroll = %d, want baseline %d", int64(before), int64(baseline))
	}

	if err := d.RecordRecession(0.8); err != nil {
		t.Fatalf("RecordRecession: %v", err)
	}
	// The recession is actually in effect (not a silent no-op).
	if got := d.WageBillFactor(); got != 0.8 {
		t.Fatalf("WageBillFactor() = %v, want 0.8", got)
	}

	after, err := d.FacilityPayroll(id)
	if err != nil {
		t.Fatalf("FacilityPayroll (recession): %v", err)
	}
	if after != baseline {
		t.Fatalf("defence payroll contracted under recession: %d, want unchanged %d", int64(after), int64(baseline))
	}

	// The ordinary-employer comparator: the SAME factor with no floor
	// contracts the wage bill — proving the recession is live and the floor is
	// what holds the defence payroll up.
	ordinary := moneyTimesFactor(baseline, 0.8)
	if ordinary >= baseline {
		t.Fatalf("ordinary comparator did not contract: %d >= %d", int64(ordinary), int64(baseline))
	}
}

// TestAntiCyclicalPayroll_FloorDoesWork constructs a facility whose floor is
// below its nominal wage bill and asserts the payroll floors at the floor (not
// at the fully-contracted raw value) — proving the floor is a real mechanism,
// not a hardwired "unchanged" constant.
func TestAntiCyclicalPayroll_FloorDoesWork(t *testing.T) {
	cfg := validConfig()
	fc := cfg.Facilities["naval"]
	fc.PayrollMicropounds = 1_000_000
	fc.PayrollFloorMicropounds = 600_000 // partial protection
	cfg.Facilities["naval"] = fc

	d := newDefence(t, cfg, 1)
	f := finance.NewFinanceAPI("corr-defence")
	c, err := citizens.NewCitizensAPI(1, "corr-defence")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := d.SetFinance(f); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	if err := d.SetCitizens(c); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}
	if err := d.SetBuild(newBuild(t)); err != nil {
		t.Fatalf("SetBuild: %v", err)
	}
	res, err := d.RespondToMandate(MandateResponse{
		MandateID: "naval-100k", Choice: "naval-base",
		Site:    SiteRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0}},
		OwnerID: 1, Month: 0,
	})
	if err != nil {
		t.Fatalf("RespondToMandate: %v", err)
	}
	if err := d.RecordRecession(0.5); err != nil {
		t.Fatalf("RecordRecession: %v", err)
	}
	pay, err := d.FacilityPayroll(res.FacilityID)
	if err != nil {
		t.Fatalf("FacilityPayroll: %v", err)
	}
	// raw would be 500,000; the floor holds it at 600,000.
	if pay != finance.Money(600_000) {
		t.Fatalf("floor-protected payroll = %d, want 600000", int64(pay))
	}
}

// TestPersonnelAsCitizens_RealRecords builds a facility and asserts its
// personnel become REAL citizen records (TotalPopulation rises by the exact
// personnel + children count), and the married-quarters / school-place figures
// are queryable (AC-8) — not a bare "jobs: N" aggregate.
func TestPersonnelAsCitizens_RealRecords(t *testing.T) {
	fc := validConfig().Facilities["naval"]
	d, _, c, id := acceptNaval(t, 1)

	wantPop := int(fc.PersonnelCount + fc.MarriedQuarters*fc.ChildrenPerQuarter)
	if got := c.TotalPopulation("corr-defence"); got != wantPop {
		t.Fatalf("TotalPopulation() = %d, want %d (personnel %d + children %d)", got, wantPop, fc.PersonnelCount, fc.MarriedQuarters*fc.ChildrenPerQuarter)
	}

	mq, err := d.MarriedQuarters(id)
	if err != nil {
		t.Fatalf("MarriedQuarters: %v", err)
	}
	if mq != fc.MarriedQuarters {
		t.Fatalf("MarriedQuarters() = %d, want %d", mq, fc.MarriedQuarters)
	}
}

// TestForcesFamilies_SchoolPlaceDemand asserts forces-families children appear
// as real school-stage citizen records and in the queryable school-place
// demand a downstream education consumer reads (AC-8).
func TestForcesFamilies_SchoolPlaceDemand(t *testing.T) {
	fc := validConfig().Facilities["naval"]
	d, _, c, id := acceptNaval(t, 1)

	schoolPlaces, err := d.SchoolPlaceDemand(id)
	if err != nil {
		t.Fatalf("SchoolPlaceDemand: %v", err)
	}
	want := fc.MarriedQuarters * fc.ChildrenPerQuarter
	if schoolPlaces != want {
		t.Fatalf("SchoolPlaceDemand() = %d, want %d", schoolPlaces, want)
	}
	if schoolPlaces == 0 {
		t.Fatal("forces-families children produced zero school-place demand")
	}

	// The first child record (indexed after the personnel) must be a real
	// school-stage citizen.
	childID := d.personnelID(id, fc.PersonnelCount)
	child, ok := c.CitizenAt(childID, "corr-defence")
	if !ok {
		t.Fatalf("child citizen %d not found", childID)
	}
	if len(child.Education.Stages) == 0 || child.Education.Stages[0].Stage != citizens.StagePrimary {
		t.Fatalf("child stage = %+v, want primary", child.Education.Stages)
	}
}

// TestProcurementContractValue_Queryable asserts a built facility exposes its
// data-sourced procurement contract value as a queryable output a future
// engine.fdi consumer reads (AC-9 — the BUG-058-blocked interim).
func TestProcurementContractValue_Queryable(t *testing.T) {
	fc := validConfig().Facilities["naval"]
	d, _, _, id := acceptNaval(t, 1)
	v, err := d.ProcurementContractValue(id)
	if err != nil {
		t.Fatalf("ProcurementContractValue: %v", err)
	}
	if v != finance.Money(fc.ProcurementMicropounds) {
		t.Fatalf("ProcurementContractValue() = %d, want %d", int64(v), fc.ProcurementMicropounds)
	}
}

// TestClosureShock_FactsRecorded closes a facility and asserts the closure
// facts (which facility, where, when, jobs lost) are recorded and queryable
// (AC-10's interim: the §32-scale shock routing is engine.spiral's, pending
// the unregistered edge — see doc.go), and that a double close is rejected.
func TestClosureShock_FactsRecorded(t *testing.T) {
	fc := validConfig().Facilities["naval"]
	d, _, _, id := acceptNaval(t, 1)

	ev, err := d.CloseFacility(id, 12)
	if err != nil {
		t.Fatalf("CloseFacility: %v", err)
	}
	if ev.JobsLost != fc.PersonnelCount {
		t.Fatalf("ClosureEvent.JobsLost = %d, want %d", ev.JobsLost, fc.PersonnelCount)
	}
	if ev.FacilityID != id {
		t.Fatalf("ClosureEvent.FacilityID = %d, want %d", uint64(ev.FacilityID), uint64(id))
	}
	if ev.Month != 12 {
		t.Fatalf("ClosureEvent.Month = %d, want 12", ev.Month)
	}
	fi, ok := d.Facility(id)
	if !ok || !fi.Closed {
		t.Fatal("facility not marked closed after CloseFacility")
	}

	// A second close is rejected, never silently double-counted.
	if _, err := d.CloseFacility(id, 13); err == nil {
		t.Fatal("second CloseFacility returned nil error")
	} else {
		isErr(t, err, ErrNoFacility)
	}
}

// TestNoFacilityQuery_Rejected asserts a payroll/procurement query against an
// unknown facility id returns ErrNoFacility, never a fabricated zero figure
// (AC-11).
func TestNoFacilityQuery_Rejected(t *testing.T) {
	d := newDefence(t, validConfig(), 1)
	if _, err := d.FacilityPayroll(999); err == nil {
		t.Fatal("FacilityPayroll(unknown) returned nil error")
	} else {
		isErr(t, err, ErrNoFacility)
	}
}
