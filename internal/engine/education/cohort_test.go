package education

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// AC-10: the pupil cohort-accounting identity holds at every stage, every
// month —
//
//	EnrolledThisMonth == EnrolledLastMonth
//	    + Intake - Promoted - ForkedOut - DroppedOut - Deceased - Emigrated
//
// Each right-hand term is summed independently from its own event kind
// (StageLedger), and reconciled against an independently-bookkept enrolment
// figure at every September gate.
func TestPupilConservation(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 30)
	for id := uint64(1); id <= 3; id++ {
		seedCitizen(t, c, id, 0, 0)
	}
	advanceCitizens(t, c, 8)

	// Independent bookkeeping, updated by the test's own arithmetic as it
	// drives operations — never read back from the module under test.
	expected := map[Stage]int64{}
	snap := map[int64]map[Stage]int64{}

	snapshot := func(month int64) {
		m := map[Stage]int64{}
		for s, v := range expected {
			m[s] = v
		}
		snap[month] = m
	}
	assertEnrolled := func(month int64) {
		for s := range allStages() {
			got, err := a.Enrolment(s)
			if err != nil {
				t.Fatalf("month %d stage %s: Enrolment: %v", month, s, err)
			}
			if got != expected[s] {
				t.Fatalf("month %d stage %s: enrolled=%d, independent bookkeeping=%d", month, s, got, expected[s])
			}
		}
	}

	// Month 8: enrol three citizens (all nursery, age 8 < primary entry 20).
	for id := uint64(1); id <= 3; id++ {
		if err := a.Enrol(id, 8); err != nil {
			t.Fatalf("enrol %d: %v", id, err)
		}
	}
	expected[StageNursery] = 3
	snapshot(8)
	assertEnrolled(8)

	// Month 20: nursery → primary.
	advanceCitizens(t, c, 12)
	if err := a.AdvanceIntake(20); err != nil {
		t.Fatalf("advance 20: %v", err)
	}
	expected[StageNursery] = 0
	expected[StagePrimary] = 3
	snapshot(20)
	assertEnrolled(20)

	// Month 32: primary → secondary.
	advanceCitizens(t, c, 12)
	if err := a.AdvanceIntake(32); err != nil {
		t.Fatalf("advance 32: %v", err)
	}
	expected[StagePrimary] = 0
	expected[StageSecondary] = 3
	snapshot(32)
	assertEnrolled(32)

	// Month 44: three-way fork (one each), then a death and an emigration.
	advanceCitizens(t, c, 12)
	if err := a.ApplyFork(ForkCommand{SixthForm: 1, Technical: 1, LeaveAt16: 1}, 44); err != nil {
		t.Fatalf("fork: %v", err)
	}
	expected[StageSecondary] = 0
	expected[StageSixthForm] = 1
	expected[StageTechnicalCollege] = 1
	expected[StageLeaveAt16] = 1
	if err := a.RemovePupil(1, DepartureDeceased, 44); err != nil {
		t.Fatalf("remove deceased: %v", err)
	}
	expected[StageSixthForm] = 0
	if err := a.RemovePupil(2, DepartureEmigrated, 44); err != nil {
		t.Fatalf("remove emigrated: %v", err)
	}
	expected[StageTechnicalCollege] = 0
	snapshot(44)
	assertEnrolled(44)

	// Month 56: the leave-at-16 pupil is not yet old enough for adult ed.
	advanceCitizens(t, c, 12)
	if err := a.AdvanceIntake(56); err != nil {
		t.Fatalf("advance 56: %v", err)
	}
	snapshot(56)
	assertEnrolled(56)

	// Month 68: leave-at-16 → adult education.
	advanceCitizens(t, c, 12)
	if err := a.AdvanceIntake(68); err != nil {
		t.Fatalf("advance 68: %v", err)
	}
	expected[StageLeaveAt16] = 0
	expected[StageAdultEducation] = 1
	snapshot(68)
	assertEnrolled(68)

	// Reconciliation: replay each stage's independently-summed ledger terms
	// and assert the running total matches the bookkept enrolment at every
	// month, for every stage.
	for _, s := range stageOrder {
		var running int64
		for _, term := range a.StageLedger(s) {
			running = running + term.Intake - term.Promoted - term.ForkedOut -
				term.DroppedOut - term.Deceased - term.Emigrated
			want, ok := snap[term.Month][s]
			if !ok {
				t.Fatalf("stage %s has ledger month %d with no snapshot", s, term.Month)
			}
			if running != want {
				t.Fatalf("stage %s month %d: identity reconciliation = %d, bookkept = %d (terms %+v)",
					s, term.Month, running, want, term)
			}
		}
	}
}

// AC-10: an out-of-range DepartureReason is rejected BEFORE the pupil is
// removed, so the live enrolled count and the independent ledger replay
// cannot diverge (the Destructive repro DepartureReason(99) used to remove
// the pupil while the ledger switch silently recorded no term).
func TestRemovePupilUnknownReasonRejected(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 31)
	seedCitizen(t, c, 1, 0, 0)
	advanceCitizens(t, c, 8)
	if err := a.Enrol(1, 8); err != nil {
		t.Fatalf("enrol: %v", err)
	}

	err := a.RemovePupil(1, DepartureReason(99), 8)
	if err == nil {
		t.Fatalf("expected unknown DepartureReason to be rejected")
	}
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrInvalidDepartureReason {
		t.Fatalf("error code = %v, want %s", err, ErrInvalidDepartureReason)
	}
	// No mutation: the pupil is still enrolled and the ledger still agrees.
	if _, ok := a.PupilStage(1); !ok {
		t.Fatalf("pupil removed despite rejected departure reason")
	}
	if got, err := a.Enrolment(StageNursery); err != nil || got != 1 {
		t.Fatalf("nursery enrolment = %d (err %v), want 1 (no mutation)", got, err)
	}
}

// allStages returns the stages to assert enrolment against (the pipeline
// order plus StageNone is irrelevant — the module never enrols into None).
func allStages() map[Stage]bool {
	m := map[Stage]bool{}
	for _, s := range stageOrder {
		m[s] = true
	}
	return m
}
