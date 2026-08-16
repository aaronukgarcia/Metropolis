package firms

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// mustBuild loads the real data/buildings.json catalogue.
func mustBuild(t *testing.T) *build.BuildAPI {
	t.Helper()
	b, err := build.LoadDefault("firms-test-build")
	if err != nil {
		t.Fatalf("build.LoadDefault: %v", err)
	}
	return b
}

// seedCitizens seeds n citizens with ids 1..n (all tertiary, employed).
func seedCitizens(t *testing.T, n int) *citizens.CitizensAPI {
	t.Helper()
	recs := make([]citizens.ColdRecord, 0, n)
	for i := 1; i <= n; i++ {
		recs = append(recs, citizenRecord(uint64(i), 0, citizens.SectorTertiary, 0))
	}
	return mustCitizens(t, recs)
}

// TestFirmStaffRosterIsRealCitizens (AC-4): a firm's Staff is a slice of
// real CitizenIDs, not a bare integer headcount.
func TestFirmStaffRosterIsRealCitizens(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	if err := api.SetCitizens(seedCitizens(t, 30)); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}

	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	firm, err := api.Firm(id)
	if err != nil {
		t.Fatalf("Firm: %v", err)
	}
	if len(firm.Staff) != 1 || firm.Staff[0] != 1 {
		t.Fatalf("founder staff = %v, want [1]", firm.Staff)
	}
	if err := api.HireStaff(id, []uint64{2, 3}); err != nil {
		t.Fatalf("HireStaff: %v", err)
	}
	firm, _ = api.Firm(id)
	if len(firm.Staff) != 3 || firm.Staff[0] != 1 || firm.Staff[1] != 2 || firm.Staff[2] != 3 {
		t.Fatalf("staff roster after hires = %v, want [1 2 3]", firm.Staff)
	}
}

// TestGrowthBlockedNoHire (AC-4): a firm cannot advance stage without
// enough real hires to reach the target stage's staff floor.
func TestGrowthBlockedNoHire(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	_ = api.SetCitizens(seedCitizens(t, 30))

	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	// Empty hire list is an invalid staff-count (AC-16).
	if err := api.Grow(id, nil); !hasCode(err, ErrInvalidStaffCount) {
		t.Fatalf("Grow(empty hires) = %v, want ErrInvalidStaffCount", err)
	}
	// Two hires → 3 staff, below the Small floor of 6 → blocked.
	if err := api.Grow(id, []uint64{2, 3}); !hasCode(err, ErrGrowthBlocked) {
		t.Fatalf("Grow(insufficient hires) = %v, want ErrGrowthBlocked", err)
	}
	if st, _ := api.Stage(id); st != StageStartup {
		t.Fatalf("stage advanced without enough hires: got %v", st)
	}
}

// TestFailureUnemploysStaffEmployment (AC-5): failing a firm unemploys every
// staff CitizenID through CitizensAPI, and a non-roster citizen is unaffected.
func TestFailureUnemploysStaffEmployment(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	c := seedCitizens(t, 30)
	_ = api.SetCitizens(c)

	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	if err := api.HireStaff(id, []uint64{2, 3}); err != nil {
		t.Fatalf("HireStaff: %v", err)
	}

	ins, err := api.Fail(id)
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if len(ins.Unemployed) != 3 {
		t.Fatalf("Unemployed = %v, want 3 staff", ins.Unemployed)
	}

	for _, cid := range []uint64{1, 2, 3} {
		cit, ok := c.CitizenAt(cid, "firms-test")
		if !ok {
			t.Fatalf("citizen %d missing", cid)
		}
		if cit.Employment.State != citizens.EmploymentUnemployed {
			t.Fatalf("citizen %d employmentState = %v, want unemployed", cid, cit.Employment.State)
		}
	}
	// Control citizen (not on the roster) is unaffected.
	control, _ := c.CitizenAt(4, "firms-test")
	if control.Employment.State != citizens.EmploymentEmployed {
		t.Fatalf("control citizen employmentState changed to %v", control.Employment.State)
	}
}

// TestStageProgressionAndNoSkip (AC-6): stages advance one at a time in the
// documented order, never skipping.
func TestStageProgressionAndNoSkip(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	_ = api.SetCitizens(seedCitizens(t, 30))
	_ = api.SetBuild(mustBuild(t))

	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	if err := api.GrantPremises(id, "shop"); err != nil {
		t.Fatalf("GrantPremises: %v", err)
	}
	// 5 hires → 6 staff → Small (floor 6).
	if err := api.Grow(id, []uint64{2, 3, 4, 5, 6}); err != nil {
		t.Fatalf("Grow to Small: %v", err)
	}
	if st, _ := api.Stage(id); st != StageSmall {
		t.Fatalf("stage = %v, want Small (never skipped to a later stage)", st)
	}
}

// TestNoSkipStage (AC-6): a single Grow advances exactly one stage even
// when the roster would already qualify for a later stage.
func TestNoSkipStage(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	_ = api.SetCitizens(seedCitizens(t, 300))
	_ = api.SetBuild(mustBuild(t))

	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	if err := api.GrantPremises(id, "shop"); err != nil {
		t.Fatalf("GrantPremises: %v", err)
	}
	// 250 hires → 251 staff (would qualify for Enterprise), but one Grow
	// moves Startup → Small only.
	hires := make([]uint64, 0, 250)
	for i := 2; i <= 251; i++ {
		hires = append(hires, uint64(i))
	}
	if err := api.Grow(id, hires); err != nil {
		t.Fatalf("Grow: %v", err)
	}
	if st, _ := api.Stage(id); st != StageSmall {
		t.Fatalf("stage = %v, want Small (single-step advancement)", st)
	}
}

// TestNoPremisesBlocksGrowth (AC-7): without secured premises a firm is
// blocked from advancing and enters the stalled state, distinct from failure.
func TestNoPremisesBlocksGrowth(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	_ = api.SetCitizens(seedCitizens(t, 30))
	_ = api.SetBuild(mustBuild(t))

	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	// Enough hires, but no premises granted.
	if err := api.Grow(id, []uint64{2, 3, 4, 5, 6}); !hasCode(err, ErrNoPremises) {
		t.Fatalf("Grow(no premises) = %v, want ErrNoPremises", err)
	}
	firm, _ := api.Firm(id)
	if !firm.Stalled {
		t.Fatal("expected the firm to enter the stalled/exit state")
	}
	if firm.Stage != StageStartup {
		t.Fatalf("stage advanced without premises: %v", firm.Stage)
	}
	// Grant premises → growth now succeeds.
	if err := api.GrantPremises(id, "shop"); err != nil {
		t.Fatalf("GrantPremises: %v", err)
	}
	if err := api.Grow(id, []uint64{2, 3, 4, 5, 6}); err != nil {
		t.Fatalf("Grow(after premises) = %v", err)
	}
	if st, _ := api.Stage(id); st != StageSmall {
		t.Fatalf("stage = %v, want Small", st)
	}
}

// TestInsolvencyDistinctFromAcquisition (AC-9): insolvency unemploys staff;
// acquisition transfers staff without an employment-state change.
func TestInsolvencyDistinctFromAcquisition(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	c := seedCitizens(t, 30)
	_ = api.SetCitizens(c)

	// Insolvency unemploys (already covered in TestFailureUnemploysStaffEmployment);
	// here prove the OUTCOME TYPE is distinct and acquisition transfers.
	a, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found A: %v", err)
	}
	_ = api.HireStaff(a, []uint64{2})

	b, err := api.Found(3)
	if err != nil {
		t.Fatalf("Found B: %v", err)
	}
	_ = api.HireStaff(b, []uint64{4})

	acq, err := api.Acquire(a, b)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if acq.AcquirerID != a || acq.TargetID != b {
		t.Fatalf("Acquisition = %+v", acq)
	}
	if len(acq.Transferred) != 2 || acq.Transferred[0] != 3 || acq.Transferred[1] != 4 {
		t.Fatalf("Transferred = %v, want [3 4]", acq.Transferred)
	}
	// The acquirer now carries the target's staff (founder 1 + hire 2, then
	// the transferred 3 and 4).
	af, _ := api.Firm(a)
	if len(af.Staff) != 4 {
		t.Fatalf("acquirer staff = %v, want [1 2 3 4] (transferred)", af.Staff)
	}
	// Transferred staff keep their employment (not unemployed).
	for _, cid := range []uint64{3, 4} {
		cit, _ := c.CitizenAt(cid, "firms-test")
		if cit.Employment.State != citizens.EmploymentEmployed {
			t.Fatalf("acquired staff %d was unemployed (state %v) — acquisition must transfer, not unemploy", cid, cit.Employment.State)
		}
	}
	// The target is gone.
	if _, err := api.Firm(b); !hasCode(err, ErrUnknownFirm) {
		t.Fatalf("target firm still registered after acquisition: %v", err)
	}
}

// TestGrowAlreadyEnterpriseRejected (AC-16): growth against an Enterprise
// firm is rejected, never silently clamped.
func TestGrowAlreadyEnterpriseRejected(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	_ = api.SetCitizens(seedCitizens(t, 5))
	// Place a firm directly at Enterprise (same-package).
	api.firms[1] = &firmState{firm: Firm{ID: 1, Stage: StageEnterprise, Staff: []uint64{1}}}
	if err := api.Grow(1, []uint64{2}); !hasCode(err, ErrAlreadyEnterprise) {
		t.Fatalf("Grow(Enterprise) = %v, want ErrAlreadyEnterprise", err)
	}
}

// TestInvalidStaffCountRejected (AC-16): a zero staff-count query/command is
// rejected with a typed error.
func TestInvalidStaffCountRejected(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	_ = api.SetCitizens(seedCitizens(t, 5))
	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	if err := api.HireStaff(id, nil); !hasCode(err, ErrInvalidStaffCount) {
		t.Fatalf("HireStaff(empty) = %v, want ErrInvalidStaffCount", err)
	}
}
