package education

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// StageQuality returns a stage's realised funding-quality, read through
// engine.services' generic Quality surface (AC-2/AC-6) — never a local
// duplicate of the funding→quality model. An unregistered stage is rejected
// (AC-12).
func (a *EducationAPI) StageQuality(s Stage) (float64, error) {
	if err := a.checkNotCopied("StageQuality"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.stageQualityLocked(s)
}

func (a *EducationAPI) stageQualityLocked(s Stage) (float64, error) {
	if a.services == nil {
		return 0, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{"dependency": "services", "operation": "StageQuality"})
	}
	id, ok := a.stageServiceIDLocked(s)
	if !ok {
		return 0, errs.New(ErrStageNotRegistered, a.correlationID, map[string]any{"stage": s.String()})
	}
	return a.services.Quality(id)
}

// StageCapacity returns a stage's numeric capacity ceiling, read through
// engine.services (AC-1's capacity query). An unregistered stage is
// rejected (AC-12).
func (a *EducationAPI) StageCapacity(s Stage) (float64, error) {
	if err := a.checkNotCopied("StageCapacity"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.services == nil {
		return 0, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{"dependency": "services", "operation": "StageCapacity"})
	}
	id, ok := a.stageServiceIDLocked(s)
	if !ok {
		return 0, errs.New(ErrStageNotRegistered, a.correlationID, map[string]any{"stage": s.String()})
	}
	return a.services.Capacity(id)
}

// Attainment returns a pupil's cumulative quality-weighted attainment score
// (AC-6) and whether the citizen is an enrolled pupil.
func (a *EducationAPI) Attainment(citizenID uint64) (int32, bool) {
	if err := a.checkNotCopied("Attainment"); err != nil {
		return 0, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	p, ok := a.pupils[citizenID]
	if !ok {
		return 0, false
	}
	return p.Attainment, true
}

// Pupil returns a pupil's full pipeline record (stage, cumulative
// attainment, last-transition drift delta, enrolment month, school) and
// whether the citizen is an enrolled pupil.
func (a *EducationAPI) Pupil(citizenID uint64) (Pupil, bool) {
	if err := a.checkNotCopied("Pupil"); err != nil {
		return Pupil{}, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	p, ok := a.pupils[citizenID]
	if !ok {
		return Pupil{}, false
	}
	return *p, true
}

// PupilStage returns a pupil's current stage and whether the citizen is an
// enrolled pupil.
func (a *EducationAPI) PupilStage(citizenID uint64) (Stage, bool) {
	if err := a.checkNotCopied("PupilStage"); err != nil {
		return StageNone, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	p, ok := a.pupils[citizenID]
	if !ok {
		return StageNone, false
	}
	return p.Stage, true
}

// HallsCapacity returns the university halls-of-residence capacity (AC-8) —
// a DISTINCT capacity input from teaching capacity. Enrolment above it is
// rejected/queued even when teaching capacity has headroom.
func (a *EducationAPI) HallsCapacity() float64 {
	if err := a.checkNotCopied("HallsCapacity"); err != nil {
		return 0
	}
	return a.cfg.HallsCapacity
}

// ResearchPoints returns the accumulated university research-points output
// (AC-8): a graduating cohort of size N produces research points
// proportional to N at the data-sourced ResearchPointsPerGraduate rate.
func (a *EducationAPI) ResearchPoints() int64 {
	if err := a.checkNotCopied("ResearchPoints"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.researchPoints
}

// Enrol enters a citizen into the age-appropriate compulsory stage at a
// September intake month (AC-3/AC-4). The age is derived from
// engine.citizens' own derived age (birthMonth-based, never a locally-stored
// age field). A citizen with no valid record/age is rejected (AC-12); a
// non-intake month is a no-op (entry into the pipeline is September-gated,
// AC-4).
func (a *EducationAPI) Enrol(citizenID uint64, month int64) error {
	if err := a.checkNotCopied("Enrol"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.citizens == nil || a.season == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{"dependency": "citizens/season", "operation": "Enrol"})
	}
	intake, err := a.season.IsSchoolIntakeMonth(month)
	if err != nil {
		return err
	}
	if !intake {
		return nil // entry is September-gated (AC-4)
	}
	age, err := a.citizenAgeLocked(citizenID)
	if err != nil {
		return err
	}
	if _, exists := a.pupils[citizenID]; exists {
		return nil // already enrolled: no-op
	}
	stage := compulsoryStageForAge(age, a.cfg)
	p := &Pupil{CitizenID: citizenID, Stage: stage, EnrolMonth: month, SchoolID: citizenID}
	a.pupils[citizenID] = p
	a.recordEvent(cohortEvent{Month: month, Stage: stage, Kind: eventIntake, CitizenID: citizenID})
	a.schoolRunLocked(stage, p)
	return nil
}

// citizenAgeLocked returns a citizen's derived age (months) via
// engine.citizens' own derived age — citizens.Citizen.Age(), computed from
// birthMonth + the sim month, never a locally-stored age field this package
// would have to keep in sync (AC-3). A citizen with no valid record or a
// negative age is rejected (AC-12). The caller holds a.mu.
func (a *EducationAPI) citizenAgeLocked(citizenID uint64) (int64, error) {
	cit, ok := a.citizens.CitizenAt(citizenID, a.correlationID)
	if !ok {
		return 0, errs.New(ErrInvalidCitizenState, a.correlationID, map[string]any{"citizen": citizenID})
	}
	age := cit.Age()
	if age < 0 {
		return 0, errs.New(ErrInvalidCitizenState, a.correlationID, map[string]any{"citizen": citizenID, "age": age})
	}
	return age, nil
}

// AdvanceIntake processes one September intake gate (AC-4): every pupil who
// has aged into their stage's single next stage is promoted. Secondary's
// exit is the three-way fork, which is NOT automatic — it is distributed by
// ApplyFork (AC-13) — and U3A is terminal. A non-intake month is a no-op,
// and a re-run of an already-processed intake month is a no-op (AC-10's
// conservation at the write boundary).
func (a *EducationAPI) AdvanceIntake(month int64) error {
	if err := a.checkNotCopied("AdvanceIntake"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.citizens == nil || a.season == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{"dependency": "citizens/season", "operation": "AdvanceIntake"})
	}
	intake, err := a.season.IsSchoolIntakeMonth(month)
	if err != nil {
		return err
	}
	if !intake {
		return nil // mid-year: no promotion (AC-4)
	}
	if a.hasIntakeRun && month <= a.lastIntakeMonth {
		return nil // idempotent (AC-10)
	}
	a.hasIntakeRun = true
	a.lastIntakeMonth = month

	for _, id := range a.sortedPupilIDs() {
		p := a.pupils[id]
		next := nextStage(p.Stage)
		if next == StageNone {
			continue // secondary awaits the fork, U3A is terminal
		}
		// University enrolment is capped by halls-of-residence capacity (AC-8),
		// a DISTINCT capacity input from teaching capacity: a pupil who has aged
		// into university but finds the halls full is queued, not admitted.
		if next == StageUniversity && float64(a.enrolled[StageUniversity]) >= a.cfg.HallsCapacity {
			continue
		}
		age, err := a.citizenAgeLocked(id)
		if err != nil {
			continue // a vanished citizen is handled by RemovePupil
		}
		if age >= a.cfg.EntryAgeMonths[next] {
			// Documented attrition path (AC-10's DroppedOut term): a
			// deterministic draw from hash(worldSeed, id, month, "education")
			// (AC-14) — no shared RNG, no wall clock.
			if a.cfg.DropoutRate > 0 && a.dropoutDecides(id, month) {
				a.removePupilLocked(id, DepartureDroppedOut, month)
				continue
			}
			a.transitionPupilLocked(p, p.Stage, next, month)
		}
	}
	return nil
}

// dropoutDecides draws the deterministic per-transition attrition decision
// from the counter-based hash stream keyed (worldSeed, id, month,
// "education") (AC-14). The same inputs always produce the same decision.
func (a *EducationAPI) dropoutDecides(id uint64, month int64) bool {
	stream := det.NewStream(a.seed, id, month, "education")
	return stream.Float64() < a.cfg.DropoutRate
}

// transitionPupilLocked moves a pupil from one stage to the next: it writes
// the exiting stage's quality-weighted attainment score IMMEDIATELY at the
// transition (AC-6 — never deferred), applies the per-stage personality
// drift (AC-7 — incremental, never accumulated to a terminal event), records
// the two accounting terms (promoted-out + intake-in), and regenerates the
// school-run trip for the new stage (AC-5). The caller holds a.mu.
func (a *EducationAPI) transitionPupilLocked(p *Pupil, from, to Stage, month int64) {
	score := a.stageScoreLocked(from)
	p.Attainment = satAddInt32(p.Attainment, score)
	p.LastDrift = a.applyDriftLocked(p.CitizenID, score, month)
	p.Stage = to
	p.EnrolMonth = month

	a.recordEvent(cohortEvent{Month: month, Stage: from, Kind: eventPromoted, CitizenID: p.CitizenID})
	a.recordEvent(cohortEvent{Month: month, Stage: to, Kind: eventIntake, CitizenID: p.CitizenID})

	// University graduation produces research points (AC-8).
	if from == StageUniversity {
		a.researchPoints += int64(math.Round(a.cfg.ResearchPointsPerGraduate))
	}

	a.schoolRunLocked(to, p)
}

// stageScoreLocked maps a stage's realised funding-quality onto a signed
// quality-weighted attainment score (AC-6): positive above baseline, negative
// below. The caller holds a.mu.
func (a *EducationAPI) stageScoreLocked(s Stage) int32 {
	if a.services == nil {
		return 0
	}
	q, err := a.stageQualityLocked(s)
	if err != nil {
		return 0
	}
	return attainmentScore(a.cfg, q)
}

// attainmentScore converts a realised funding-quality (in [0,1]) into a
// signed attainment score: round((quality - BaselineQuality) *
// AttainmentScale), clamped to the int16 range (GR#16 — never a wrapping
// bare conversion). Positive = above-baseline (good) schooling, negative =
// below-baseline (poor).
func attainmentScore(cfg Config, quality float64) int32 {
	dev := quality - cfg.BaselineQuality
	s := math.Round(dev * cfg.AttainmentScale)
	if s > math.MaxInt16 {
		return math.MaxInt16
	}
	if s < math.MinInt16 {
		return math.MinInt16
	}
	return int32(s)
}

// applyDriftLocked applies the per-stage personality drift to a citizen's
// 8-axis P vector (AC-7): it computes the drift via engine.citizens'
// documented ApplyEducationEffect (the §5.1 mechanism — good schooling
// widens ambition/novelty-seeking, poor schooling narrows them) and writes
// the citizen record through CitizensAPI's command-based mutation path
// (LifeEventEducation, per engine.citizens.md AC-1b — never a direct field
// write). It returns the ambition-axis drift delta so the direction is
// inspectable at the transition boundary. The caller holds a.mu.
func (a *EducationAPI) applyDriftLocked(citizenID uint64, score int32, month int64) int32 {
	if a.citizens == nil {
		return 0
	}
	cit, ok := a.citizens.CitizenAt(citizenID, a.correlationID)
	if !ok {
		return 0
	}
	drifted := citizens.ApplyEducationEffect(cit.Personality, score)
	delta := drifted[citizens.AxisAmbition] - cit.Personality[citizens.AxisAmbition]

	// The citizen-record write: engine.citizens' documented education-drift
	// command path. (The command derives its magnitude from the citizen's
	// stored attainment snapshot; this module's own score — the delta above —
	// is the authoritative per-stage drift, see doc.go's AC-6 note.)
	_ = a.citizens.ApplyLifeEventCommand(citizens.LifeEventCommand{
		CorrelationID: a.correlationID,
		Kind:          citizens.LifeEventEducation,
		CitizenID:     citizenID,
	})
	return delta
}

// schoolRunLocked feeds a nursery/primary/secondary pupil's school-run trip
// into engine.traffic's registered trip-generation surface (AC-5) — a
// genuine addition to traffic demand, not an attendance flag traffic never
// reads. The caller holds a.mu.
func (a *EducationAPI) schoolRunLocked(s Stage, p *Pupil) {
	if a.traffic == nil || p.SchoolID == 0 {
		return
	}
	switch s {
	case StageNursery, StagePrimary, StageSecondary:
	default:
		return
	}
	_ = a.traffic.AddDemand(p.SchoolID, 1)
	_ = a.traffic.RegisterTrip(TripDemand{SchoolID: p.SchoolID, Mode: "walk", Count: 1})
}

// ForkCommand is the secondary-exit three-way fork distribution (AC-2/AC-13):
// the number of the leaving cohort to send to sixth form, technical college,
// and leave-at-16. The three counts must sum exactly to the eligible
// secondary cohort's full size.
type ForkCommand struct {
	SixthForm int64
	Technical int64
	LeaveAt16 int64
}

// ApplyFork distributes the secondary cohort that has aged past the fork
// gate across the three documented branches (AC-2's genuine three-way fork).
// A command whose three branch counts do not sum to the cohort's full size
// is rejected (AC-13) — the conservation identity enforced at the write
// boundary, not only checked after the fact.
func (a *EducationAPI) ApplyFork(cmd ForkCommand, month int64) error {
	if err := a.checkNotCopied("ApplyFork"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.citizens == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{"dependency": "citizens", "operation": "ApplyFork"})
	}

	// Collect the eligible secondary cohort in deterministic ascending-id
	// order (GR#21).
	var eligible []*Pupil
	for _, id := range a.sortedPupilIDs() {
		p := a.pupils[id]
		if p.Stage != StageSecondary {
			continue
		}
		age, err := a.citizenAgeLocked(id)
		if err != nil {
			continue
		}
		if age >= a.cfg.EntryAgeMonths[StageSixthForm] {
			eligible = append(eligible, p)
		}
	}

	// Validate the command at the write boundary (AC-13): every branch count
	// must be non-negative, and the three-count total must be computed with
	// overflow-safe arithmetic before it is compared against the cohort. A
	// negative or overflowing count would otherwise coerce a branch into the
	// leave-at-16 remainder or index eligible[] out of range.
	consistent := cmd.SixthForm >= 0 && cmd.Technical >= 0 && cmd.LeaveAt16 >= 0
	total := int64(0)
	if consistent {
		for _, c := range [...]int64{cmd.SixthForm, cmd.Technical, cmd.LeaveAt16} {
			var ok bool
			total, ok = num.SatAddChecked(total, c)
			if ok {
				consistent = false
				break
			}
		}
	}
	if !consistent || total != int64(len(eligible)) {
		return errs.New(ErrForkMismatch, a.correlationID, map[string]any{
			"sixthForm": cmd.SixthForm,
			"technical": cmd.Technical,
			"leaveAt16": cmd.LeaveAt16,
			"eligible":  int64(len(eligible)),
		})
	}

	i := 0
	for end := i + int(cmd.SixthForm); i < end; i++ {
		a.forkPupilLocked(eligible[i], StageSixthForm, month)
	}
	for end := i + int(cmd.Technical); i < end; i++ {
		a.forkPupilLocked(eligible[i], StageTechnicalCollege, month)
	}
	for ; i < len(eligible); i++ {
		a.forkPupilLocked(eligible[i], StageLeaveAt16, month)
	}
	return nil
}

// forkPupilLocked moves one secondary pupil into a fork branch: writes the
// secondary-stage attainment + drift (AC-6/AC-7), records the forked-out +
// intake terms (AC-10), and regenerates the school-run trip (AC-5). The
// caller holds a.mu.
func (a *EducationAPI) forkPupilLocked(p *Pupil, branch Stage, month int64) {
	score := a.stageScoreLocked(StageSecondary)
	p.Attainment = satAddInt32(p.Attainment, score)
	p.LastDrift = a.applyDriftLocked(p.CitizenID, score, month)
	p.Stage = branch
	p.EnrolMonth = month

	a.recordEvent(cohortEvent{Month: month, Stage: StageSecondary, Kind: eventForkedOut, CitizenID: p.CitizenID})
	a.recordEvent(cohortEvent{Month: month, Stage: branch, Kind: eventIntake, CitizenID: p.CitizenID})

	a.schoolRunLocked(branch, p)
}

// satAddInt32 adds two int32 values with saturation (GR#16: never a wrapping
// bare addition on a stored score).
func satAddInt32(a, b int32) int32 {
	if b > 0 && a > math.MaxInt32-b {
		return math.MaxInt32
	}
	if b < 0 && a < math.MinInt32-b {
		return math.MinInt32
	}
	return a + b
}
