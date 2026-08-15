package proj

// CurveStatus classifies a curve's availability for rendering (PRJ-6 /
// SF-7). It is the screen-side decoding of the "f7.projections" wire
// status string; any wire string this package does not recognise decodes
// to StatusUnavailable with a reason naming the raw value, so an
// unexpected producer status is shown rather than silently rendered as
// data.
type CurveStatus int

const (
	// StatusAvailable: the source view has delivered data for this curve;
	// it renders normally (PRJ-1).
	StatusAvailable CurveStatus = iota
	// StatusUnavailable: the source view has not yet delivered data (e.g.
	// engine.capexport not real) — render "unavailable: <reason>", never a
	// blank or fabricated flat line (PRJ-6).
	StatusUnavailable
	// StatusNotUnlocked: the forecasting tier that would supply this curve
	// is not yet unlocked — render "not yet unlocked" (PRJ-6).
	StatusNotUnlocked
)

// String renders CurveStatus for logs/tests.
func (s CurveStatus) String() string {
	switch s {
	case StatusAvailable:
		return "available"
	case StatusUnavailable:
		return "unavailable"
	case StatusNotUnlocked:
		return "not-unlocked"
	default:
		return "unknown"
	}
}

// Threshold is one threshold line on a curve chart (PRJ-1 / UI-SPEC §4):
// a capacity ceiling or floor value drawn as a horizontal line across the
// chart, independent of the value series itself. Sourced from
// "f7.projections" curves[].thresholds[].
type Threshold struct {
	// Value is the threshold value, in the same units as the curve's
	// history/projection series.
	Value float64
	// Label names the threshold (e.g. "capacity ceiling"); empty when the
	// producer supplied none.
	Label string
}

// DecisionMarker is one queued-decision step marker on a curve chart
// (PRJ-1 / UI-SPEC §4): a decision the player has queued whose effect
// lands at a future month — drawn as a step marker on the projected curve
// before it is built. Sourced from "f7.projections" curves[].markers[].
type DecisionMarker struct {
	// MonthOffset is the projection index (0 = first projected month) at
	// which the queued decision lands. Clamped to the projection's span at
	// render time rather than trusted as a raw index (GR#1).
	MonthOffset int
	// Label names the queued decision (e.g. "school build").
	Label string
}

// Curve is one demand/supply curve this screen renders (PRJ-1): history
// and projection as two distinct series (so solid-vs-dim is a render-time
// fact, never a guess), optional confidence bands, thresholds, and queued
// decision markers. Sourced from "f7.projections" curves[].
type Curve struct {
	// Key is the stable curve identifier (e.g. "water.demand"); used for
	// drill-through widget IDs and the SF-2 traceability table.
	Key string
	// Label is the human-readable display name; Key is shown when empty.
	Label string

	// Status is this curve's availability. When not StatusAvailable,
	// UnavailableReason names why and no chart is drawn (PRJ-6).
	Status            CurveStatus
	UnavailableReason string

	// History is the observed series (solid Braille); Projection is the
	// forecast series (dim Braille). Either may be empty; a curve with
	// both empty but StatusAvailable renders its label and nothing else
	// (there is no fabricated flat line to draw).
	History    []float64
	Projection []float64

	// ConfidenceUpper/ConfidenceLower are the confidence band around the
	// projection, rendered as dim dots (PRJ-1). Either may be nil/empty;
	// a missing band simply draws no band. Each entry pairs with the
	// Projection entry at the same index when present.
	ConfidenceUpper []float64
	ConfidenceLower []float64

	Thresholds []Threshold
	Markers    []DecisionMarker
}

// Crossing is one contracted-vs-internal demand crossing chart (PRJ-3 /
// §36): the internal-demand growth curve and the contracted-away-capacity
// curve on one chart, so the year the export contract stops paying and
// starts costing is visible. Sourced from "f7.projections" crossings[].
type Crossing struct {
	Key   string
	Label string

	Status            CurveStatus
	UnavailableReason string

	// InternalDemand is the internal demand growth series.
	InternalDemand []float64
	// ContractedCapacity is the capacity contracted away to off-map
	// neighbours (sold capacity), drawn on the same value scale.
	ContractedCapacity []float64
	// CrossingMonth is the month offset (0 = first projected month) where
	// InternalDemand first exceeds ContractedCapacity; -1 means no
	// crossing within the horizon.
	CrossingMonth int
}

// RateOutlook is the §45 national base-rate cycle curve (PRJ-4): read-
// only — the player positions for it, never controls it. Sourced from
// "f7.projections" rateOutlook.
type RateOutlook struct {
	Status            CurveStatus
	UnavailableReason string

	History    []float64
	Projection []float64
}

// Consequence is the projected-consequence payload a >60-month (A5
// Slow-Fuse) decision confirmation passes to RenderSlowFuse (PRJ-5). It
// is deliberately the minimum shape a confirmation flow needs to hand
// over: a label, the consequence's fuse horizon, and the history/
// projection series to render as a curve.
type Consequence struct {
	// Label names the decision (e.g. "Cut school funding").
	Label string
	// FuseMonths is the decision's principal-effect horizon in months. The
	// Slow-Fuse gate (engine.projections AC-5) guarantees this is >60 for
	// any consequence that reaches this screen; RenderSlowFuse does not
	// re-enforce the rule (the gate owns it) — it renders whatever curve
	// it is given.
	FuseMonths int
	History    []float64
	Projection []float64
}
