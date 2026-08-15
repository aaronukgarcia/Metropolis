package projections

// CurveProvider is the registration contract code.json's engine.
// projections inbound edge documents as "systems register curve
// providers" (US-1, AC-1). Any system with a demand/supply curve
// implements Value and registers it under a stable key via
// RegisterCurveProvider — this package never knows or cares what the
// curve actually measures (§13/Out of scope: "any individual system's
// actual curve math ... is out of scope").
//
// Value must be a pure function of monthIndex for a given provider's
// own internal state at the time it is called (GR#21, mirroring
// engine.season's SeasonAPI purity contract) — the same monthIndex
// passed twice must return the same value until the provider's own
// registrant mutates its state between calls (e.g. a fake test
// provider moving to a new trend state, or a real system's month-end
// update). ProjectionsAPI itself never mutates a registered provider.
type CurveProvider interface {
	Value(monthIndex int64) (float64, error)
}

// CurveProviderFunc adapts a plain func to CurveProvider, the same
// http.HandlerFunc-style convenience every registration-surface
// package in this codebase's UI layer uses for its own Handler/
// Precondition-shaped interfaces.
type CurveProviderFunc func(monthIndex int64) (float64, error)

// Value implements CurveProvider.
func (f CurveProviderFunc) Value(monthIndex int64) (float64, error) {
	return f(monthIndex)
}

// Confidence is the AC-6 confidence/provenance marker every projected
// Point carries, distinguishing a genuinely modelled value from a
// guess — the exact anti-ambush distinction US-5 asks for ("never
// mistake confidence for certainty").
type Confidence int

const (
	// ConfidenceUnavailable: no value could be produced at all (e.g. a
	// MarginToGhostCity query against a city whose historic peak has
	// never exceeded the AC-18 threshold — the margin is genuinely
	// undefined, not a false "0 months" alarm).
	ConfidenceUnavailable Confidence = iota

	// ConfidenceComputed: derived from a registered curve provider
	// within the current horizon N (AC-6) — the only confidence value
	// that promises a real, modelled consequence rather than a guess.
	ConfidenceComputed

	// ConfidenceExtrapolated: derived by continuing a trend beyond what
	// a registered provider actually modelled — either because the
	// query month is beyond the current horizon N, or because the
	// value itself (e.g. MarginToInsolvency/MarginToGhostCity) is a
	// trend-continuation estimate rather than a directly-registered
	// curve value. AC-6: a month beyond N never returns ConfidenceComputed.
	ConfidenceExtrapolated
)

// String renders a human-readable confidence name (log/debug use —
// never itself the GR#1 user-visible error text, which always goes
// through errs.E.Display).
func (c Confidence) String() string {
	switch c {
	case ConfidenceComputed:
		return "Computed"
	case ConfidenceExtrapolated:
		return "Extrapolated"
	default:
		return "Unavailable"
	}
}

// Point is one month's value in a queried curve series — code.json's
// "systems register curve providers; UI subscribes to named
// projections" query-side payload. Historical distinguishes an actual
// (already-happened, per ProjectionsAPI's CurrentMonth) point from a
// projected one (AC-7), so a UI consumer can render solid-vs-dim per
// UI-SPEC §4's documented idiom without guessing.
type Point struct {
	Month      int64
	Value      float64
	Historical bool
	Confidence Confidence
}
