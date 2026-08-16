package education

// Stage is engine.education's own stage enum (§27). It deliberately
// carries MORE values than engine.citizens' compressed citizens.Stage,
// because the §27 pipeline's secondary-exit fork is a genuine three-way
// branch — sixth form (academic), technical college (trades), and
// leave-at-16 (unskilled pool) — each a distinct trajectory, not a
// boolean "stayed on or left" flag. Each value is a small bucketed code
// (an integer, never a string) so it can field-compress the same way
// engine.citizens' enums do.
type Stage uint8

const (
	StageNone Stage = iota
	StageNursery
	StagePrimary
	StageSecondary
	StageSixthForm
	StageTechnicalCollege
	StageLeaveAt16
	StageUniversity
	StageAdultEducation
	StageU3A

	// numStages is the count of Stage values (including StageNone).
	// A schema constant, not a balance number.
	numStages
)

// validStage reports whether s is a real Stage enum value. Stage is a
// uint8, so a caller-supplied value in [numStages, 255] is a malformed
// command — indexing enrolled[s]/registered[s] with it would panic
// (index out of range). Every command/query boundary bounds-checks against
// this before touching those arrays, so a malformed stage is rejected with
// ErrStageNotRegistered, never a panic (AC-12).
func validStage(s Stage) bool { return s < numStages }

// String renders the canonical stage name for logs/inspection.
func (s Stage) String() string {
	switch s {
	case StageNone:
		return "none"
	case StageNursery:
		return "nursery"
	case StagePrimary:
		return "primary"
	case StageSecondary:
		return "secondary"
	case StageSixthForm:
		return "sixth-form"
	case StageTechnicalCollege:
		return "technical-college"
	case StageLeaveAt16:
		return "leave-at-16"
	case StageUniversity:
		return "university"
	case StageAdultEducation:
		return "adult-education"
	case StageU3A:
		return "u3a"
	}
	return "unknown"
}

// stageOrder is the canonical §27 pipeline order, used anywhere the module
// must iterate stages deterministically (GR#21: a slice, never a map range).
// The three fork outcomes sit side by side immediately after secondary,
// exactly as §27 lays them out.
var stageOrder = []Stage{
	StageNursery,
	StagePrimary,
	StageSecondary,
	StageSixthForm,
	StageTechnicalCollege,
	StageLeaveAt16,
	StageUniversity,
	StageAdultEducation,
	StageU3A,
}

// nextStage returns the single automatic next stage for a pupil leaving s,
// or StageNone when the exit is not automatic:
//   - StageSecondary's exit is the three-way fork, which is NOT automatic —
//     it is distributed by the ApplyFork command (AC-13), so nextStage
//     returns StageNone and the pupil waits for that command;
//   - StageU3A is terminal.
//
// Every other stage has exactly one documented successor (§27).
func nextStage(s Stage) Stage {
	switch s {
	case StageNursery:
		return StagePrimary
	case StagePrimary:
		return StageSecondary
	case StageSixthForm:
		return StageUniversity
	case StageTechnicalCollege:
		return StageUniversity
	case StageLeaveAt16:
		return StageAdultEducation
	case StageUniversity:
		return StageAdultEducation
	case StageAdultEducation:
		return StageU3A
	default:
		return StageNone
	}
}

// compulsoryStageForAge maps an age in months to the compulsory stage a
// citizen of that age belongs in (nursery < primary entry age < secondary
// entry age; at or past the secondary entry age the citizen is in
// secondary, awaiting the fork). The exact month thresholds come from
// Config.EntryAgeMonths (data-sourced, GR#15) — this function only encodes
// the ordering, never a literal age.
func compulsoryStageForAge(ageMonths int64, cfg Config) Stage {
	switch {
	case ageMonths < cfg.EntryAgeMonths[StagePrimary]:
		return StageNursery
	case ageMonths < cfg.EntryAgeMonths[StageSecondary]:
		return StagePrimary
	default:
		return StageSecondary
	}
}

// stageServiceID returns the engine.services ServiceID this module
// registers a stage's capacity/funding/quality against (AC-2). The id is a
// stable, deterministic string derived from the stage, so a consumer can
// always address a stage's service instance through engine.services' own
// query methods.
func stageServiceID(s Stage) string {
	return "education." + s.String()
}
