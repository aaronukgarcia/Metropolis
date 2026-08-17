package wellbeing

import (
	"fmt"
	"math"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fileWellbeing is data/wellbeing.json's filename, relative to the resolved
// data directory (see data.ResolveDataDir). This package owns its own
// balance surface the same way engine.market owns data/market.json and
// engine.services owns data/services.json; the loader routes through
// foundation/data's generic Load[T] (the shared duplicate-key /
// malformed-JSON / version-field machinery) rather than a hand-rolled
// decoder, matching the MOD-020 ruling-1 precedent.
const fileWellbeing = "wellbeing.json"

// maxCoefficient is the largest "sane" balance coefficient Validate accepts
// for a driver weight, headline weight, age-curve delta, commute stress
// anchor, or downstream-modifier slope. It is orders of magnitude above any
// legitimate 0-100-track value (the shipped weights are 8..15 and the slopes
// 0.01) yet small enough that an accepted coefficient can never overflow
// float64 in any driver/modifier/headline product whose input is in-domain.
// The one runtime input deliberately left unbounded — crowding's
// persons-per-room — is saturated finite by satFinite instead (SEC-093), so
// AC-2's additive identity Total == Baseline + Σ(driver.Delta) stays exact
// for every accepted config: satFinite then only ever fires on an absurd
// persons-per-room, never on the data file.
const maxCoefficient = 1e6

// WellbeingFile is data/wellbeing.json's top-level schema (§18/§42 balance
// surface): the two track baselines, the headline-composite weights, the
// per-driver physical/mental weights, and the four downstream-modifier
// coefficients. Every number here is a balance placeholder (GR#15 / the
// balance-number regime) sourced at Load time — none of the driver
// arithmetic below reads a balance literal from Go source.
type WellbeingFile struct {
	Version   int          `json:"version"`
	Baseline  BaselineFile `json:"baseline"`
	Headline  HeadlineFile `json:"headline"`
	Physical  PhysicalFile `json:"physical"`
	Mental    MentalFile   `json:"mental"`
	Modifiers ModifierFile `json:"modifiers"`
}

// BaselineFile is the neutral starting point of each 0-100 track. A
// data-sourced offset, never a hardcoded baseline in Go (GR#15 / Aaron's
// "the health value is the conserved sum, never a hardcoded baseline").
type BaselineFile struct {
	Physical float64 `json:"physical"`
	Mental   float64 `json:"mental"`
}

// HeadlineFile is the §18 headline-composite weighting: Wellbeing =
// wPhys·physical + wMent·mental + wSat·satisfaction.
type HeadlineFile struct {
	PhysicalWeight     float64 `json:"physicalWeight"`
	MentalWeight       float64 `json:"mentalWeight"`
	SatisfactionWeight float64 `json:"satisfactionWeight"`
}

// AgeCurvePoint is one anchor of the §18 physical age curve: the signed
// physical-track delta at a given age in years, linearly interpolated
// between anchors.
type AgeCurvePoint struct {
	AgeYears float64 `json:"ageYears"`
	Delta    float64 `json:"delta"`
}

// PhysicalFile is the six physical-driver weight surface (§18).
type PhysicalFile struct {
	AgeCurve                 []AgeCurvePoint `json:"ageCurve"`
	HealthcareAccessWeight   float64         `json:"healthcareAccessWeight"`
	DietWeight               float64         `json:"dietWeight"`
	ActiveTravelWeight       float64         `json:"activeTravelWeight"`
	PollutionWeight          float64         `json:"pollutionWeight"`
	SportParticipationWeight float64         `json:"sportParticipationWeight"`
}

// MentalFile is the nine mental-driver weight surface (§18). The commute
// penalty is data-encoded as a two-slope shape: linear from (0,0) to
// (threshold, commuteStressAtThreshold), then linear again from there to
// (100 minutes, commuteStressAt100Minutes), with the second slope steeper
// (AC-4's nonlinear past-45-minutes penalty).
type MentalFile struct {
	CommuteWeight             float64 `json:"commuteWeight"`
	CommuteThresholdMinutes   float64 `json:"commuteThresholdMinutes"`
	CommuteStressAtThreshold  float64 `json:"commuteStressAtThreshold"`
	CommuteStressAt100Minutes float64 `json:"commuteStressAt100Minutes"`
	JobAmbitionMismatchWeight float64 `json:"jobAmbitionMismatchWeight"`
	GreenSpaceWeight          float64 `json:"greenSpaceWeight"`
	LeisureFitWeight          float64 `json:"leisureFitWeight"`
	CrowdingWeight            float64 `json:"crowdingWeight"`
	IsolationWeight           float64 `json:"isolationWeight"`
	NoiseWeight               float64 `json:"noiseWeight"`
	FinancialStressWeight     float64 `json:"financialStressWeight"`
	RentBurdenThreshold       float64 `json:"rentBurdenThreshold"`
	UnemploymentWeight        float64 `json:"unemploymentWeight"`
	UnemploymentCapMonths     float64 `json:"unemploymentCapMonths"`
}

// ModifierFile is the four §18 downstream-effect coefficient surface
// (mortality, productivity, satisfaction, emigration), each a slope on a
// multiplier centred at 1.0.
type ModifierFile struct {
	MortalitySlope    float64 `json:"mortalitySlope"`
	ProductivitySlope float64 `json:"productivitySlope"`
	SatisfactionSlope float64 `json:"satisfactionSlope"`
	EmigrationSlope   float64 `json:"emigrationSlope"`
}

// LoadWellbeing reads and validates data/wellbeing.json from dir via
// foundation/data's generic Load[T] (duplicate-key / malformed-JSON /
// missing-version handling for free) and returns the decoded,
// schema-validated file. Every failure is a registry-sourced *errs.E
// re-wrapped under this package's own ErrDataInvalid — never a silent
// default substitution, never a panic.
func LoadWellbeing(dir, correlationID string) (WellbeingFile, error) {
	f, err := data.Load[WellbeingFile, *WellbeingFile](filepath.Join(dir, fileWellbeing), correlationID)
	if err != nil {
		return WellbeingFile{}, errs.Wrap(ErrDataInvalid, correlationID, err, map[string]any{
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
//   - both baselines within the nominal [0,100] track domain;
//   - the three headline weights non-negative and not all zero;
//   - the age curve carrying at least two strictly-increasing anchors with
//     finite deltas of magnitude ≤ maxCoefficient;
//   - every balance coefficient (headline weights, per-driver weights, the
//     commute stress anchors, and the four modifier slopes) non-negative and
//     no larger than maxCoefficient — the sane-coefficient bound that keeps
//     AC-2's additive identity exact;
//   - the commute threshold strictly positive and the two commute stress
//     anchors encoding the steeper-above shape (AC-4);
//   - the rent-burden threshold in [0,1) (a 35% threshold is §18, but the
//     value itself is a balance number — see the escalation note);
//   - the unemployment cap strictly positive.
//
// Field-level failures are returned as *data.FieldError so the generic
// Load reports the offending field name in its registry-sourced message.
func (w *WellbeingFile) Validate() error {
	if w.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	if err := requireRange("baseline.physical", w.Baseline.Physical, 0, 100); err != nil {
		return err
	}
	if err := requireRange("baseline.mental", w.Baseline.Mental, 0, 100); err != nil {
		return err
	}
	if err := requireCoefficient("headline.physicalWeight", w.Headline.PhysicalWeight); err != nil {
		return err
	}
	if err := requireCoefficient("headline.mentalWeight", w.Headline.MentalWeight); err != nil {
		return err
	}
	if err := requireCoefficient("headline.satisfactionWeight", w.Headline.SatisfactionWeight); err != nil {
		return err
	}
	if w.Headline.PhysicalWeight == 0 && w.Headline.MentalWeight == 0 && w.Headline.SatisfactionWeight == 0 {
		return &data.FieldError{Field: "headline", Rule: "at least one headline weight must be non-zero"}
	}

	if len(w.Physical.AgeCurve) < 2 {
		return &data.FieldError{Field: "physical.ageCurve", Rule: "must have at least two anchor points"}
	}
	prev := -1.0
	for i, p := range w.Physical.AgeCurve {
		prefix := fmt.Sprintf("physical.ageCurve[%d]", i)
		if !isFinite(p.AgeYears) || p.AgeYears < 0 {
			return &data.FieldError{Field: prefix + ".ageYears", Rule: "must be finite and >= 0"}
		}
		if !isFinite(p.Delta) {
			return &data.FieldError{Field: prefix + ".delta", Rule: "must be finite"}
		}
		if math.Abs(p.Delta) > maxCoefficient {
			return &data.FieldError{Field: prefix + ".delta", Rule: fmt.Sprintf("magnitude must be <= %v (sane coefficient bound), got %v", maxCoefficient, p.Delta)}
		}
		if i > 0 && p.AgeYears <= prev {
			return &data.FieldError{Field: prefix + ".ageYears", Rule: "anchors must be strictly increasing"}
		}
		prev = p.AgeYears
	}

	for _, f := range []struct {
		name string
		v    float64
	}{
		{"physical.healthcareAccessWeight", w.Physical.HealthcareAccessWeight},
		{"physical.dietWeight", w.Physical.DietWeight},
		{"physical.activeTravelWeight", w.Physical.ActiveTravelWeight},
		{"physical.pollutionWeight", w.Physical.PollutionWeight},
		{"physical.sportParticipationWeight", w.Physical.SportParticipationWeight},
		{"mental.commuteWeight", w.Mental.CommuteWeight},
		{"mental.jobAmbitionMismatchWeight", w.Mental.JobAmbitionMismatchWeight},
		{"mental.greenSpaceWeight", w.Mental.GreenSpaceWeight},
		{"mental.leisureFitWeight", w.Mental.LeisureFitWeight},
		{"mental.crowdingWeight", w.Mental.CrowdingWeight},
		{"mental.isolationWeight", w.Mental.IsolationWeight},
		{"mental.noiseWeight", w.Mental.NoiseWeight},
		{"mental.financialStressWeight", w.Mental.FinancialStressWeight},
		{"mental.unemploymentWeight", w.Mental.UnemploymentWeight},
		{"modifiers.mortalitySlope", w.Modifiers.MortalitySlope},
		{"modifiers.productivitySlope", w.Modifiers.ProductivitySlope},
		{"modifiers.satisfactionSlope", w.Modifiers.SatisfactionSlope},
		{"modifiers.emigrationSlope", w.Modifiers.EmigrationSlope},
	} {
		if err := requireCoefficient(f.name, f.v); err != nil {
			return err
		}
	}

	if !isFinite(w.Mental.CommuteThresholdMinutes) || w.Mental.CommuteThresholdMinutes <= 0 {
		return &data.FieldError{Field: "mental.commuteThresholdMinutes", Rule: "must be finite and strictly positive"}
	}
	if err := requireCoefficient("mental.commuteStressAtThreshold", w.Mental.CommuteStressAtThreshold); err != nil {
		return err
	}
	if err := requireCoefficient("mental.commuteStressAt100Minutes", w.Mental.CommuteStressAt100Minutes); err != nil {
		return err
	}
	// AC-4's nonlinearity is structural: the marginal stress per minute above
	// the threshold must exceed the marginal below it. Enforce the two
	// anchors encode that, so a hand-edited data file cannot silently flatten
	// the penalty into a single linear slope.
	if !(w.Mental.CommuteStressAt100Minutes > w.Mental.CommuteStressAtThreshold) {
		return &data.FieldError{Field: "mental.commuteStressAt100Minutes", Rule: "must exceed commuteStressAtThreshold (steeper above the threshold)"}
	}

	if !isFinite(w.Mental.RentBurdenThreshold) || w.Mental.RentBurdenThreshold < 0 || w.Mental.RentBurdenThreshold >= 1 {
		return &data.FieldError{Field: "mental.rentBurdenThreshold", Rule: "must be finite and in [0,1)"}
	}
	if !isFinite(w.Mental.UnemploymentCapMonths) || w.Mental.UnemploymentCapMonths <= 0 {
		return &data.FieldError{Field: "mental.unemploymentCapMonths", Rule: "must be finite and strictly positive"}
	}
	return nil
}

// requireNonNegative / requireRange / isFinite are tiny local validation
// helpers so Validate stays readable without reaching into foundation/data's
// unexported helpers (the exported surface here is just Validator/FieldError).
func requireNonNegative(field string, v float64) error {
	if !isFinite(v) {
		return &data.FieldError{Field: field, Rule: fmt.Sprintf("must be finite, got %v", v)}
	}
	if v < 0 {
		return &data.FieldError{Field: field, Rule: fmt.Sprintf("must be >= 0, got %v", v)}
	}
	return nil
}

// requireCoefficient is requireNonNegative plus the sane-upper-bound half of
// the SEC-093 contract: a weight or slope above maxCoefficient is a data
// error rejected at Load/New, never a value that overflows a product
// downstream. Bounding here (rather than only saturating at the arithmetic)
// is what keeps the AC-2 additive identity exact — a saturated driver delta
// would otherwise break Baseline + Σ(delta) == Total.
func requireCoefficient(field string, v float64) error {
	if err := requireNonNegative(field, v); err != nil {
		return err
	}
	if v > maxCoefficient {
		return &data.FieldError{Field: field, Rule: fmt.Sprintf("must be <= %v (sane coefficient bound), got %v", maxCoefficient, v)}
	}
	return nil
}

func requireRange(field string, v, lo, hi float64) error {
	if !isFinite(v) {
		return &data.FieldError{Field: field, Rule: fmt.Sprintf("must be finite, got %v", v)}
	}
	if v < lo || v > hi {
		return &data.FieldError{Field: field, Rule: fmt.Sprintf("must be in [%v, %v], got %v", lo, hi, v)}
	}
	return nil
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
