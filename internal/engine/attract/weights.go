package attract

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// Weights holds the seven §11 attractiveness weights (w₁…w₇) — the
// coefficients of the master-dial formula
//
//	A = w₁·jobAvailability + w₂·housingAffordability + w₃·serviceCoverage
//	    + w₄·environment + w₅·leisureFit + w₆·safety + w₇·reputation
//
// The weights are loaded from config data (Config, ParseConfig) — never
// literal constants in this package's source (GR#15, AC-2): rebalancing is
// a data edit, not a code change. Each weight is a finite fraction in
// [0,1] and the seven sum to 1, so with the five pushed terms and the
// computed housing term on a [0,100] scale, the fundamentals portion of A
// lands in [0,100] and reputation adds a signed "beyond fundamentals" kick.
type Weights struct {
	JobAvailability      float64
	HousingAffordability float64
	ServiceCoverage      float64
	Environment          float64
	LeisureFit           float64
	Safety               float64
	Reputation           float64
}

// weightSumEpsilon is the tolerance for the "weights sum to 1" check —
// a data authoring near-miss (0.9999999) is accepted, a real imbalance is
// not.
const weightSumEpsilon = 1e-6

// sum returns the seven weights' total.
func (w Weights) sum() float64 {
	return w.JobAvailability + w.HousingAffordability + w.ServiceCoverage +
		w.Environment + w.LeisureFit + w.Safety + w.Reputation
}

// validate rejects any weight that is non-finite or outside [0,1], and any
// set whose total departs from 1 by more than weightSumEpsilon — with a
// registry-sourced error and no partial application (AC-10: an invalid
// weight config never silently substitutes an unweighted or zero-weighted
// term). It returns nil for a valid set.
func (w Weights) validate(correlationID string) error {
	fields := []struct {
		name  string
		value float64
	}{
		{"jobAvailability", w.JobAvailability},
		{"housingAffordability", w.HousingAffordability},
		{"serviceCoverage", w.ServiceCoverage},
		{"environment", w.Environment},
		{"leisureFit", w.LeisureFit},
		{"safety", w.Safety},
		{"reputation", w.Reputation},
	}
	for _, f := range fields {
		if !isFinite(f.value) || f.value < 0 || f.value > 1 {
			return errs.New(ErrInvalidWeights, correlationID, map[string]any{
				"field": f.name,
				"value": f.value,
			})
		}
	}
	if s := w.sum(); !isFinite(s) || s < 1-weightSumEpsilon || s > 1+weightSumEpsilon {
		return errs.New(ErrInvalidWeights, correlationID, map[string]any{
			"field": "sum",
			"value": s,
		})
	}
	return nil
}
