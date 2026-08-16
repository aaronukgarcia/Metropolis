package worklife

import (
	"fmt"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// fileWorklife is data/worklife.json's filename, relative to the resolved
// data directory (see data.ResolveDataDir). This package owns its own
// balance surface the same way engine.services owns data/services.json and
// engine.market owns data/market.json; the loader routes through
// foundation/data's generic Load[T] (the shared duplicate-key /
// malformed-JSON / version-field machinery) rather than a hand-rolled
// decoder.
const fileWorklife = "worklife.json"

// WorklifeFile is data/worklife.json's top-level schema: the three time
// patterns and the placeholder working-week policy values. The top-level
// $comment / meta blocks are documentation for human readers and are
// deliberately not part of this struct (GR#15: the runtime reads the
// schedule figures, not the prose).
type WorklifeFile struct {
	Version             int                    `json:"version"`
	Patterns            []PatternDef           `json:"patterns"`
	WorkingWeekPolicies []WorkingWeekPolicyDef `json:"workingWeekPolicies"`
}

// PatternDef is one data/worklife.json pattern entry (AC-2: every hour and
// on-duty-profile figure is read from here, never a Go literal).
type PatternDef struct {
	ID                string        `json:"id"`
	Label             string        `json:"label"`
	HoursPerDay       int64         `json:"hoursPerDay"`
	DaysPerWeek       int64         `json:"daysPerWeek"`
	StartHour         int64         `json:"startHour,omitempty"`
	EndHour           int64         `json:"endHour,omitempty"`
	CoverageSpanHours int64         `json:"coverageSpanHours"`
	Rotations         []RotationDef `json:"rotations,omitempty"`
	Placeholder       bool          `json:"placeholder"`
	Disclosure        string        `json:"disclosure"`
	SpecRef           string        `json:"specRef"`
}

// RotationDef is one shift rotation window within a pattern.
type RotationDef struct {
	StartHour int64 `json:"startHour"`
	EndHour   int64 `json:"endHour"`
}

// WorkingWeekPolicyDef is one data/worklife.json workingWeekPolicies entry
// (the placeholder values MOD-064 will author in policies.json; worklife's
// tests consume these until then).
type WorkingWeekPolicyDef struct {
	ID               string  `json:"id"`
	Label            string  `json:"label"`
	HoursPerWeek     int64   `json:"hoursPerWeek"`
	WageCoefficient  float64 `json:"wageCoefficient"`
	WellbeingWeight  float64 `json:"wellbeingWeight"`
	HoursPerWeekUnit string  `json:"hoursPerWeekUnit"`
	WageUnit         string  `json:"wageCoefficientUnit"`
	WellbeingUnit    string  `json:"wellbeingWeightUnit"`
	Placeholder      bool    `json:"placeholder"`
	Disclosure       string  `json:"disclosure"`
	SpecRef          string  `json:"specRef"`
}

// LoadWorklife reads and validates data/worklife.json from dir via
// foundation/data's generic Load[T] and returns the decoded,
// schema-validated file. Every failure is a registry-sourced *errs.E
// wrapped under this package's own ErrDataInvalid (AC-15), never a silent
// default substitution and never a panic.
func LoadWorklife(dir, correlationID string) (WorklifeFile, error) {
	f, err := data.Load[WorklifeFile, *WorklifeFile](filepath.Join(dir, fileWorklife), correlationID)
	if err != nil {
		return WorklifeFile{}, errs.Wrap(ErrDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return f, nil
}

// Validate implements data.Validator. It enforces the module-specific
// schema rules foundation/data's generic loader has no notion of (the
// shared loader only checks version presence and JSON structure):
//
//   - exactly the three documented pattern kinds, each once, none unknown;
//   - every pattern's hoursPerDay in (0, 24] and daysPerWeek in (0, 7];
//   - a positive coverage span and a non-empty disclosure comment;
//   - core-hours carries a 0 <= startHour < endHour <= 24 window;
//   - shift carries rotations that contiguously tile [0, coverageSpanHours]
//     with no gaps, overlaps, or negative lengths;
//   - every working-week policy carries positive hours, a finite wage
//     coefficient >= 1, a finite non-negative wellbeing weight, and a
//     non-empty disclosure comment.
//
// Field-level failures are returned as *data.FieldError so the generic
// Load reports the offending field name in its registry-sourced message.
func (f *WorklifeFile) Validate() error {
	if f.Version <= 0 {
		return fieldErr("version", "required, must be a positive integer")
	}
	if len(f.Patterns) != 3 {
		return fieldErr("patterns", "must contain exactly the three documented kinds (core-hours, shift, any-time)")
	}

	seen := make(map[string]bool, len(f.Patterns))
	for i, p := range f.Patterns {
		prefix := fmt.Sprintf("patterns[%d]", i)
		switch PatternKind(p.ID) {
		case PatternCoreHours, PatternShift, PatternAnyTime:
		default:
			return fieldErr(prefix+".id", fmt.Sprintf("unrecognised pattern kind %q (want core-hours/shift/any-time)", p.ID))
		}
		if seen[p.ID] {
			return fieldErr(prefix+".id", fmt.Sprintf("duplicate pattern kind %q", p.ID))
		}
		seen[p.ID] = true

		if p.HoursPerDay <= 0 || p.HoursPerDay > int64(hoursPerDay) {
			return fieldErr(prefix+".hoursPerDay", fmt.Sprintf("must be in (0, %d], got %d", hoursPerDay, p.HoursPerDay))
		}
		if p.DaysPerWeek <= 0 || p.DaysPerWeek > int64(daysPerWeek) {
			return fieldErr(prefix+".daysPerWeek", fmt.Sprintf("must be in (0, %d], got %d", daysPerWeek, p.DaysPerWeek))
		}
		if p.CoverageSpanHours <= 0 || p.CoverageSpanHours > int64(hoursPerDay) {
			return fieldErr(prefix+".coverageSpanHours", fmt.Sprintf("must be in (0, %d], got %d", hoursPerDay, p.CoverageSpanHours))
		}
		if p.Disclosure == "" {
			return fieldErr(prefix+".disclosure", "required, must be a non-empty disclosure comment")
		}

		switch PatternKind(p.ID) {
		case PatternCoreHours:
			if p.StartHour < 0 || p.EndHour > int64(hoursPerDay) || p.StartHour >= p.EndHour {
				return fieldErr(prefix, "core-hours requires 0 <= startHour < endHour <= 24")
			}
		case PatternShift:
			if len(p.Rotations) == 0 {
				return fieldErr(prefix+".rotations", "shift requires at least one rotation")
			}
			if err := validateRotations(prefix, p.Rotations, p.CoverageSpanHours); err != nil {
				return err
			}
		}
	}

	for i, wp := range f.WorkingWeekPolicies {
		prefix := fmt.Sprintf("workingWeekPolicies[%d]", i)
		if wp.ID == "" {
			return fieldErr(prefix+".id", "required, must be non-empty")
		}
		if wp.HoursPerWeek <= 0 || wp.HoursPerWeek > int64(hoursPerWeek) {
			return fieldErr(prefix+".hoursPerWeek", fmt.Sprintf("must be in (0, %d], got %d", hoursPerWeek, wp.HoursPerWeek))
		}
		if !num.IsFinite(wp.WageCoefficient) || wp.WageCoefficient < 1 {
			return fieldErr(prefix+".wageCoefficient", "must be finite and >= 1")
		}
		if !num.IsFinite(wp.WellbeingWeight) || wp.WellbeingWeight < 0 {
			return fieldErr(prefix+".wellbeingWeight", "must be finite and >= 0")
		}
		if wp.Disclosure == "" {
			return fieldErr(prefix+".disclosure", "required, must be a non-empty disclosure comment")
		}
	}
	return nil
}

// validateRotations enforces that a shift pattern's rotations are
// non-overlapping, contiguous, and tile [0, coverageSpanHours] exactly. This
// is what makes "a full roster leaves no hour unstaffed" hold by
// construction (AC-4), and it means a negative or gapped rotation window is
// a load-time error, never a silently-masked staffing hole (AC-15).
func validateRotations(prefix string, rots []RotationDef, span int64) error {
	var prev int64
	for i, r := range rots {
		rf := fmt.Sprintf("%s.rotations[%d]", prefix, i)
		if r.StartHour < 0 || r.EndHour > int64(hoursPerDay) || r.StartHour >= r.EndHour {
			return fieldErr(rf, fmt.Sprintf("requires 0 <= startHour < endHour <= %d", hoursPerDay))
		}
		if r.StartHour != prev {
			return fieldErr(rf, fmt.Sprintf("rotations must be contiguous (start at %d), got startHour %d", prev, r.StartHour))
		}
		prev = r.EndHour
	}
	if prev != span {
		return fieldErr(prefix+".rotations", fmt.Sprintf("rotations must tile [0, %d), but end at %d", span, prev))
	}
	return nil
}

// fieldErr builds a *data.FieldError naming the offending field and rule,
// so foundation/data's generic Load can render a precise registry-sourced
// message (GR#7/GR#15).
func fieldErr(field, rule string) error {
	return &data.FieldError{Field: field, Rule: rule}
}
