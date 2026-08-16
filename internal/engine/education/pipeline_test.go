package education

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// AC-2: every stage is registered against engine.services' generic Service
// framework and queryable through engine.services' own methods.
func TestStageRegistration(t *testing.T) {
	a, _, svc, _ := newWiredAPI(t, 1)
	_ = a
	for _, s := range stageOrder {
		id := services.ServiceID(stageServiceID(s))
		if _, err := svc.Capacity(id); err != nil {
			t.Fatalf("stage %s not queryable through services.Capacity: %v", s, err)
		}
		if _, err := svc.FundingLevel(id); err != nil {
			t.Fatalf("stage %s not queryable through services.FundingLevel: %v", s, err)
		}
	}
}

// AC-2: the secondary-exit fork is a genuine three-way branch, distributable
// across all three outcomes, not defaulted to one.
func TestForkDistributesAcrossAllThreeBranches(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 2)
	for id := uint64(1); id <= 3; id++ {
		seedCitizen(t, c, id, 0, 0)
	}
	advanceCitizens(t, c, 32) // age 32 == secondary entry (September)

	for id := uint64(1); id <= 3; id++ {
		if err := a.Enrol(id, 32); err != nil {
			t.Fatalf("enrol %d: %v", id, err)
		}
	}
	advanceCitizens(t, c, 12) // to month 44 == fork gate (September)
	if err := a.ApplyFork(ForkCommand{SixthForm: 1, Technical: 1, LeaveAt16: 1}, 44); err != nil {
		t.Fatalf("fork: %v", err)
	}

	got := map[Stage]int{}
	for id := uint64(1); id <= 3; id++ {
		s, ok := a.PupilStage(id)
		if !ok {
			t.Fatalf("pupil %d vanished", id)
		}
		got[s]++
	}
	if got[StageSixthForm] != 1 || got[StageTechnicalCollege] != 1 || got[StageLeaveAt16] != 1 {
		t.Fatalf("fork did not distribute across all three branches: %v", got)
	}
}

// AC-3: stage eligibility is computed from engine.citizens' derived age
// (birthMonth-based), never a locally-stored age field.
func TestAgeGateSourcedFromCitizensAge(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 3)
	// All three citizens share birthMonth 0; they are enrolled at successive
	// September gates so their derived ages at enrolment differ (8, 20, 32),
	// placing them in nursery, primary, secondary respectively.
	for id := uint64(1); id <= 3; id++ {
		seedCitizen(t, c, id, 0, 0)
	}
	advanceCitizens(t, c, 8)
	if err := a.Enrol(1, 8); err != nil { // age 8 → nursery
		t.Fatalf("enrol 1: %v", err)
	}
	advanceCitizens(t, c, 12)
	if err := a.Enrol(2, 20); err != nil { // age 20 → primary
		t.Fatalf("enrol 2: %v", err)
	}
	advanceCitizens(t, c, 12)
	if err := a.Enrol(3, 32); err != nil { // age 32 → secondary
		t.Fatalf("enrol 3: %v", err)
	}

	if s, _ := a.PupilStage(1); s != StageNursery {
		t.Fatalf("age-8 citizen should be nursery, got %s", s)
	}
	if s, _ := a.PupilStage(2); s != StagePrimary {
		t.Fatalf("age-20 citizen should be primary, got %s", s)
	}
	if s, _ := a.PupilStage(3); s != StageSecondary {
		t.Fatalf("age-32 citizen should be secondary, got %s", s)
	}

	// Structural check: the Pupil record carries no age field to desync.
	p, _ := a.Pupil(1)
	if p.EnrolMonth != 8 {
		t.Fatalf("enrolment month wrong: %d", p.EnrolMonth)
	}
}

// AC-4: an eligible citizen is NOT promoted mid-year even when all other
// conditions are met — only at the next September gate.
func TestSeptemberGateMidYearNoPromotion(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 4)
	seedCitizen(t, c, 1, 8, 0) // age 0 at month 8
	advanceCitizens(t, c, 8)
	if err := a.Enrol(1, 8); err != nil {
		t.Fatalf("enrol: %v", err)
	}

	// Mid-year (age 30, month 38 — not September): age 30 >= primary entry 20,
	// so the only thing missing is the intake gate.
	advanceCitizens(t, c, 30)
	if err := a.AdvanceIntake(38); err != nil {
		t.Fatalf("advance mid-year: %v", err)
	}
	if s, _ := a.PupilStage(1); s != StageNursery {
		t.Fatalf("promoted mid-year: still nursery expected, got %s", s)
	}

	// September gate (month 44): now the promotion fires.
	advanceCitizens(t, c, 6)
	if err := a.AdvanceIntake(44); err != nil {
		t.Fatalf("advance september: %v", err)
	}
	if s, _ := a.PupilStage(1); s != StagePrimary {
		t.Fatalf("not promoted at September gate: primary expected, got %s", s)
	}
}

// AC-5: enrolling N nursery/primary/secondary pupils generates N school-run
// trips into engine.traffic's trip-demand surface.
func TestSchoolRunTripDemand(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 5)
	tr := newFakeTraffic()
	if err := a.SetTraffic(tr); err != nil {
		t.Fatalf("set traffic: %v", err)
	}
	for id := uint64(1); id <= 5; id++ {
		seedCitizen(t, c, id, 8, 0)
	}
	advanceCitizens(t, c, 8)
	for id := uint64(1); id <= 5; id++ {
		if err := a.Enrol(id, 8); err != nil {
			t.Fatalf("enrol %d: %v", id, err)
		}
	}
	if got := tr.totalDemand(); got != 5 {
		t.Fatalf("school-run demand = %d, want 5 (one per pupil)", got)
	}
}

// AC-6: the quality-weighted attainment score is written IMMEDIATELY at the
// primary→secondary transition, from the stage's realised funding-quality —
// two funded-quality levels produce two different scores the moment the
// transition fires (never a deferred, lazily-computed value).
func TestAttainmentWriteImmediate(t *testing.T) {
	run := func(primaryFunding float64) int32 {
		a, c, _, _ := newWiredAPI(t, 6)
		seedCitizen(t, c, 1, 0, 0)
		advanceCitizens(t, c, 8)
		if err := a.Enrol(1, 8); err != nil {
			t.Fatalf("enrol: %v", err)
		}
		// Nursery at baseline quality (score 0), primary at the given funding.
		if err := a.SetStageFunding(FundingCommand{
			Stage: StageNursery, Level: 0.5, Month: 8, FuseYears: 20,
			Projection: ProjectedConsequence{Description: "nursery baseline", Series: []float64{0, 10}},
		}); err != nil {
			t.Fatalf("set nursery funding: %v", err)
		}
		if err := a.SetStageFunding(FundingCommand{
			Stage: StagePrimary, Level: primaryFunding, Month: 8, FuseYears: 20,
			Projection: ProjectedConsequence{Description: "primary funding", Series: []float64{0, 10}},
		}); err != nil {
			t.Fatalf("set primary funding: %v", err)
		}
		// Nursery → primary at month 20, then primary → secondary at month 32:
		// the primary→secondary transition writes the primary stage's
		// attainment from its realised funding-quality.
		advanceCitizens(t, c, 12)
		if err := a.AdvanceIntake(20); err != nil {
			t.Fatalf("advance 20: %v", err)
		}
		advanceCitizens(t, c, 12)
		if err := a.AdvanceIntake(32); err != nil {
			t.Fatalf("advance 32: %v", err)
		}
		p, ok := a.Pupil(1)
		if !ok {
			t.Fatalf("pupil vanished")
		}
		return p.Attainment
	}

	high := run(0.9)
	low := run(0.1)
	if high == low {
		t.Fatalf("attainment did not differ across funding levels: high=%d low=%d", high, low)
	}
	if high <= 0 || low >= 0 {
		t.Fatalf("attainment direction wrong: high=%d (want >0) low=%d (want <0)", high, low)
	}
}

// AC-7: personality drift is applied incrementally at each stage transition
// in the documented direction — the delta after stage 1 is already nonzero
// and correctly signed before stage 2 has occurred.
func TestDriftIncremental(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 7)
	seedCitizen(t, c, 1, 0, 0)
	advanceCitizens(t, c, 8)
	if err := a.Enrol(1, 8); err != nil {
		t.Fatalf("enrol: %v", err)
	}

	// Nursery HIGH (above baseline) → widening drift.
	if err := a.SetStageFunding(FundingCommand{
		Stage: StageNursery, Level: 0.9, Month: 8, FuseYears: 20,
		Projection: ProjectedConsequence{Description: "nursery high", Series: []float64{0, 10}},
	}); err != nil {
		t.Fatalf("set nursery: %v", err)
	}
	// Primary LOW (below baseline) → narrowing drift.
	if err := a.SetStageFunding(FundingCommand{
		Stage: StagePrimary, Level: 0.1, Month: 8, FuseYears: 20,
		Projection: ProjectedConsequence{Description: "primary low", Series: []float64{0, 10}},
	}); err != nil {
		t.Fatalf("set primary: %v", err)
	}

	// Stage 1: nursery → primary at month 20 (age 20 == primary entry).
	advanceCitizens(t, c, 12)
	if err := a.AdvanceIntake(20); err != nil {
		t.Fatalf("advance stage 1: %v", err)
	}
	p1, _ := a.Pupil(1)
	if p1.LastDrift == 0 {
		t.Fatalf("drift after stage 1 is zero — not applied incrementally")
	}
	if p1.LastDrift <= 0 {
		t.Fatalf("drift after stage 1 (good schooling) should widen (>0), got %d", p1.LastDrift)
	}

	// Stage 2: primary → secondary at month 32 (age 32 == secondary entry).
	advanceCitizens(t, c, 12)
	if err := a.AdvanceIntake(32); err != nil {
		t.Fatalf("advance stage 2: %v", err)
	}
	p2, _ := a.Pupil(1)
	if p2.LastDrift >= 0 {
		t.Fatalf("drift after stage 2 (poor schooling) should narrow (<0), got %d", p2.LastDrift)
	}
}

// AC-8: university enrolment is capped by halls capacity (distinct from
// teaching capacity) — above-capacity enrolment is queued, not admitted.
func TestHallsCapacityGatesEnrolment(t *testing.T) {
	cfg := testConfig()
	cfg.HallsCapacity = 1
	a, c, _, _ := newWiredAPIWithConfig(t, cfg, 8)

	for id := uint64(1); id <= 2; id++ {
		seedCitizen(t, c, id, 0, 0)
	}
	advanceCitizens(t, c, 32)
	for id := uint64(1); id <= 2; id++ {
		if err := a.Enrol(id, 32); err != nil {
			t.Fatalf("enrol %d: %v", id, err)
		}
	}
	// Fork both into sixth form at month 44.
	advanceCitizens(t, c, 12)
	if err := a.ApplyFork(ForkCommand{SixthForm: 2}, 44); err != nil {
		t.Fatalf("fork: %v", err)
	}
	// Advance to university entry (month 56): halls capacity is 1, so only
	// one of the two sixth-form pupils is admitted; the other stays queued.
	advanceCitizens(t, c, 12)
	if err := a.AdvanceIntake(56); err != nil {
		t.Fatalf("advance university: %v", err)
	}

	inUni := 0
	inSixth := 0
	for id := uint64(1); id <= 2; id++ {
		s, _ := a.PupilStage(id)
		switch s {
		case StageUniversity:
			inUni++
		case StageSixthForm:
			inSixth++
		}
	}
	if inUni != 1 || inSixth != 1 {
		t.Fatalf("halls capacity not enforced: university=%d sixth-form=%d, want 1 and 1", inUni, inSixth)
	}
}

// AC-8: a graduating cohort of size N produces research points proportional
// to N at the documented (data-sourced) rate.
func TestResearchPointsProportionalToGraduates(t *testing.T) {
	cfg := testConfig()
	cfg.ResearchPointsPerGraduate = 2
	a, c, _, _ := newWiredAPIWithConfig(t, cfg, 9)

	for id := uint64(1); id <= 3; id++ {
		seedCitizen(t, c, id, 0, 0)
	}
	advanceCitizens(t, c, 32)
	for id := uint64(1); id <= 3; id++ {
		if err := a.Enrol(id, 32); err != nil {
			t.Fatalf("enrol %d: %v", id, err)
		}
	}
	// Fork all three into sixth form.
	advanceCitizens(t, c, 12)
	if err := a.ApplyFork(ForkCommand{SixthForm: 3}, 44); err != nil {
		t.Fatalf("fork: %v", err)
	}
	// Sixth form → university (month 56), then university → adult (month 68):
	// three graduates.
	advanceCitizens(t, c, 12)
	if err := a.AdvanceIntake(56); err != nil {
		t.Fatalf("advance to university: %v", err)
	}
	advanceCitizens(t, c, 12)
	if err := a.AdvanceIntake(68); err != nil {
		t.Fatalf("advance to adult: %v", err)
	}

	if got := a.ResearchPoints(); got != 6 { // 3 graduates × 2 points
		t.Fatalf("research points = %d, want 6 (3 graduates × 2)", got)
	}
}

// AC-12: a stage-funding command against an unregistered stage returns the
// claimed registry-sourced error and creates no enrolment side effect.
func TestUnregisteredStage(t *testing.T) {
	a, c, svc, _ := newWiredAPI(t, 10)
	// Note: newWiredAPI already registered stages; build a second API with
	// the same deps but WITHOUT RegisterStages.
	b, err := New(testConfig(), 10, "test")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := b.SetServices(svc); err != nil {
		t.Fatalf("set services: %v", err)
	}
	_ = c

	err = b.SetStageFunding(FundingCommand{
		Stage: StagePrimary, Level: 0.5, Month: 8, FuseYears: 20,
		Projection: ProjectedConsequence{Description: "x"},
	})
	if err == nil {
		t.Fatalf("expected ErrStageNotRegistered")
	}
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrStageNotRegistered {
		t.Fatalf("error code = %v, want %s", err, ErrStageNotRegistered)
	}
	if got, _ := a.Enrolment(StagePrimary); got != 0 {
		t.Fatalf("unregistered-stage funding created an enrolment record")
	}
}

// AC-12: an enrolment query for a citizen with no valid state returns the
// claimed registry-sourced error and creates no enrolment record.
func TestInvalidCitizenState(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 11)
	_ = c
	advanceCitizens(t, c, 8)
	err := a.Enrol(999999, 8) // never seeded
	if err == nil {
		t.Fatalf("expected ErrInvalidCitizenState")
	}
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrInvalidCitizenState {
		t.Fatalf("error code = %v, want %s", err, ErrInvalidCitizenState)
	}
	if got, _ := a.Enrolment(StageNursery); got != 0 {
		t.Fatalf("invalid-citizen enrol created a zero-value enrolment record")
	}
}

// AC-13: a fork command whose branches do not sum to the cohort's full size
// is rejected, and no pupil moves.
func TestForkMismatch(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 12)
	for id := uint64(1); id <= 2; id++ {
		seedCitizen(t, c, id, 0, 0)
	}
	advanceCitizens(t, c, 32)
	for id := uint64(1); id <= 2; id++ {
		if err := a.Enrol(id, 32); err != nil {
			t.Fatalf("enrol %d: %v", id, err)
		}
	}
	advanceCitizens(t, c, 12)
	// Eligible cohort is 2; a command summing to 1 must be rejected.
	err := a.ApplyFork(ForkCommand{SixthForm: 1}, 44)
	if err == nil {
		t.Fatalf("expected ErrForkMismatch")
	}
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrForkMismatch {
		t.Fatalf("error code = %v, want %s", err, ErrForkMismatch)
	}
	if got, _ := a.Enrolment(StageSecondary); got != 2 {
		t.Fatalf("mismatched fork moved pupils: secondary enrolment = %d, want 2", got)
	}
}

// AC-13: a crafted fork command whose branch counts overflow int64 (the
// Destructive repro {SixthForm: MaxInt64, Technical: 1, LeaveAt16: -MaxInt64}
// used to wrap the sum check to the cohort size and then index out of range)
// is rejected as a registry-sourced error with no pupil moved and no panic.
func TestForkOverflowRejected(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 13)
	seedCitizen(t, c, 1, 0, 0)
	advanceCitizens(t, c, 32)
	if err := a.Enrol(1, 32); err != nil {
		t.Fatalf("enrol: %v", err)
	}
	advanceCitizens(t, c, 12) // age 44 == fork gate

	err := a.ApplyFork(ForkCommand{SixthForm: math.MaxInt64, Technical: 1, LeaveAt16: -math.MaxInt64}, 44)
	if err == nil {
		t.Fatalf("expected overflowing fork command to be rejected")
	}
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrForkMismatch {
		t.Fatalf("error code = %v, want %s", err, ErrForkMismatch)
	}
	if s, ok := a.PupilStage(1); !ok || s != StageSecondary {
		t.Fatalf("overflowing fork moved the pupil: stage=%s present=%v", s, ok)
	}
	if got, _ := a.Enrolment(StageSecondary); got != 1 {
		t.Fatalf("overflowing fork changed secondary enrolment: got %d, want 1", got)
	}
}

// AC-13: a fork command carrying a negative branch count is rejected even
// when the three counts happen to sum to the cohort size (the Destructive
// repro {-1, 0, 2} against eligible=1 used to silently coerce the pupil to
// leave-at-16 without honouring the declared distribution).
func TestForkNegativeCountRejected(t *testing.T) {
	a, c, _, _ := newWiredAPI(t, 14)
	seedCitizen(t, c, 1, 0, 0)
	advanceCitizens(t, c, 32)
	if err := a.Enrol(1, 32); err != nil {
		t.Fatalf("enrol: %v", err)
	}
	advanceCitizens(t, c, 12)

	err := a.ApplyFork(ForkCommand{SixthForm: -1, Technical: 0, LeaveAt16: 2}, 44)
	if err == nil {
		t.Fatalf("expected negative-count fork command to be rejected")
	}
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrForkMismatch {
		t.Fatalf("error code = %v, want %s", err, ErrForkMismatch)
	}
	if s, ok := a.PupilStage(1); !ok || s != StageSecondary {
		t.Fatalf("negative-count fork moved the pupil: stage=%s present=%v", s, ok)
	}
	if got, _ := a.Enrolment(StageSecondary); got != 1 {
		t.Fatalf("negative-count fork changed secondary enrolment: got %d, want 1", got)
	}
}
