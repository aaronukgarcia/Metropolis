package projections

import (
	"math"
	"testing"
)

// This file is the Destructive round's regression suite (v1.8
// fix-the-CLASS rule): one test per BREAK, each written to fail if its
// corresponding fix is reverted. Kept in its own file (rather than
// folded into decisions_test.go/deathwarnings_test.go/
// projections_test.go) so a reviewer diffing "what did the Destructive
// round actually add" sees exactly this file, not a scatter of edits
// across the existing suites.

// --- BREAK 1: NaN FuseYears defeats the Slow-Fuse gate (decisions.go) ----

// TestBreak1_NaNFuseYearsRejectedNotSilentlyAccepted is BREAK-1's
// regression: `math.NaN() > slowFuseThresholdYears` is false in Go
// (every IEEE-754 ordering comparison against NaN is false), so before
// the fix, EnqueueDecision({FuseYears: NaN, Consequence: nil}) reached
// `if fuseYears > slowFuseThresholdYears && consequence.empty()`,
// found the first operand false, and returned nil — a corrupted fuse
// on a decision with NO projected-consequence payload sailed straight
// through A5's gate. Reverting the validateFuseYears guard in
// decisions.go's slowFuseGate makes this test fail (EnqueueDecision
// would return nil instead of ErrInvalidFuseYears).
func TestBreak1_NaNFuseYearsRejectedNotSilentlyAccepted(t *testing.T) {
	api := NewProjectionsAPI()
	err := api.EnqueueDecision(Decision{
		ID:        "break1-nan",
		FuseYears: math.NaN(),
		// Consequence deliberately nil — this is exactly the payload-
		// free submission the pre-fix NaN comparison let through.
	})
	assertCode(t, err, ErrInvalidFuseYears)

	// It must not have been silently queued either.
	if cancelErr := api.CancelDecision("break1-nan"); cancelErr == nil {
		t.Error("a NaN-FuseYears decision was queued despite EnqueueDecision returning an error")
	}
}

// TestBreak1_InfFuseYearsRejected covers the sibling degenerate values
// the break report named alongside NaN (+Inf/-Inf compare just as
// unreliably for this gate's purposes, even though +Inf > 5 happens to
// evaluate true today — -Inf > 5 is false, the same silent-bypass
// shape as NaN).
func TestBreak1_InfFuseYearsRejected(t *testing.T) {
	for _, fuse := range []float64{math.Inf(1), math.Inf(-1)} {
		err := api2().EnqueueDecision(Decision{ID: "break1-inf", FuseYears: fuse})
		assertCode(t, err, ErrInvalidFuseYears)
	}
}

// TestBreak1_NegativeFuseYearsRejected is this build's explicit
// decision (documented on ErrInvalidFuseYears): a negative FuseYears
// is invalid input, not a legitimately-short fuse, and is now
// rejected rather than silently accepted as "under threshold".
func TestBreak1_NegativeFuseYearsRejected(t *testing.T) {
	err := api2().EnqueueDecision(Decision{ID: "break1-negative", FuseYears: -1})
	assertCode(t, err, ErrInvalidFuseYears)
}

// TestBreak1_ValidFuseYearsStillWork proves the fix did not
// over-tighten: a normal short-fuse and a normal long-fuse-with-payload
// decision must still succeed exactly as before.
func TestBreak1_ValidFuseYearsStillWork(t *testing.T) {
	api := NewProjectionsAPI()
	if err := api.EnqueueDecision(Decision{ID: "short", FuseYears: 1}); err != nil {
		t.Errorf("short-fuse decision rejected: %v", err)
	}
	if err := api.EnqueueDecision(Decision{
		ID:          "long",
		FuseYears:   10,
		Consequence: &ProjectedConsequence{Description: "x"},
	}); err != nil {
		t.Errorf("long-fuse decision with a payload rejected: %v", err)
	}
}

// api2 is a tiny helper for the table-style Inf test above, avoiding
// repeated NewProjectionsAPI() boilerplate per iteration.
func api2() *ProjectionsAPI { return NewProjectionsAPI() }

// --- BREAK 2: NaN provider blinds the WarningLedger (deathwarnings.go) ---

// nanProvider is a CurveProvider (and GhostCityPeakProvider) that
// always returns NaN, reproducing BREAK-2's exact repro shape: "a
// provider returning NaN for now/prev".
type nanProvider struct{}

func (nanProvider) Value(int64) (float64, error) { return math.NaN(), nil }
func (nanProvider) HistoricPeak() float64        { return 100000 }

// TestBreak2_NaNProviderNeverTaggedComputed is BREAK-2's core
// regression: before the fix, extrapolateToThreshold let a NaN
// now/prev fall through its "flat or improving" escape hatch (every
// comparison against NaN is false, including the escape hatch's own
// checks) all the way to `months := remainingToThreshold / slope`
// (NaN/NaN = NaN), returning that NaN tagged ConfidenceComputed — a
// fabricated-looking real reading. Reverting the isNonFinite guards in
// extrapolateToThreshold makes this test fail (Confidence would read
// Computed with a NaN MonthsRemaining instead of Unavailable).
func TestBreak2_NaNProviderNeverTaggedComputed(t *testing.T) {
	api := NewProjectionsAPI()
	if err := api.RegisterCurveProvider(CurveKeyFinanceInsolvencyRisk, nanProvider{}); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	result, err := api.MarginToInsolvency(5)
	if err != nil {
		t.Fatalf("MarginToInsolvency: %v", err)
	}
	if result.Confidence == ConfidenceComputed {
		t.Fatalf("Confidence = Computed for a NaN-returning provider, want Unavailable (MonthsRemaining=%v)", result.MonthsRemaining)
	}
	if !math.IsNaN(result.MonthsRemaining) && result.Confidence != ConfidenceUnavailable {
		t.Errorf("unexpected result for a NaN provider: %+v", result)
	}
}

// TestBreak2_NaNMarginNeverSilentlyUncrossedInLedger is BREAK-2's
// ledger-blinding regression, stated directly against
// WarningLedger.observe: a NaN margin must be treated as a crossing
// (surfaced), never as "not crossed" (margin<=threshold is false for
// NaN, which is exactly how the ledger used to go silently blind).
// Reverting observe's isNonFinite(margin) guard makes this test fail
// (the ledger would stay empty after a NaN observation).
func TestBreak2_NaNMarginNeverSilentlyUncrossedInLedger(t *testing.T) {
	ledger := newWarningLedger()
	ledger.observe(MetricMarginToInsolvency, 7, math.NaN(), 6 /* threshold */)

	entries := ledger.Query(MetricMarginToInsolvency, 7, 7)
	if len(entries) != 1 {
		t.Fatalf("ledger has %d entries after a NaN observation at month 7, want exactly 1 (the degenerate signal must be surfaced, not swallowed): %+v", len(entries), entries)
	}
	if !math.IsNaN(entries[0].Margin) {
		t.Errorf("recorded entry's Margin = %v, want NaN preserved for diagnosis", entries[0].Margin)
	}
}

// TestBreak2_NaNProviderStillRecordsInLedgerEndToEnd is the full
// integration path: MarginToInsolvency against a NaN-returning
// provider must leave a trace in the WarningLedger, not a provably
// empty one — the exact false-assurance scenario the Destructive
// round's break report describes ("a finance/spiral death could fire
// with a provably-empty ledger while the ledger falsely reads 'no
// warning needed'").
func TestBreak2_NaNProviderStillRecordsInLedgerEndToEnd(t *testing.T) {
	api := NewProjectionsAPI()
	if err := api.RegisterCurveProvider(CurveKeyFinanceInsolvencyRisk, nanProvider{}); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	if _, err := api.MarginToInsolvency(3); err != nil {
		t.Fatalf("MarginToInsolvency: %v", err)
	}

	ledger, err := api.WarningLedger()
	if err != nil {
		t.Fatalf("WarningLedger: %v", err)
	}
	if entries := ledger.Query(MetricMarginToInsolvency, 3, 3); len(entries) == 0 {
		t.Error("ledger is empty after a NaN-provider MarginToInsolvency call — the degenerate signal was silently swallowed end-to-end")
	}
}

// TestBreak2_InfPeakNeverComputesRealMargin covers MarginToGhostCity's
// own extra input (HistoricPeak) going non-finite — the peak<=floor
// comparison in the original code was also false for NaN, so a NaN
// peak would have fallen through to computing a NaN threshold instead
// of being caught as Unavailable.
func TestBreak2_InfPeakNeverComputesRealMargin(t *testing.T) {
	provider := newFakeTrendProvider()
	provider.setPeak(math.Inf(1))
	provider.setState(map[int64]float64{0: 1000, 1: 900})

	api := NewProjectionsAPI()
	if err := api.RegisterCurveProvider(CurveKeyGhostCityPopulation, provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	result, err := api.MarginToGhostCity(1)
	if err != nil {
		t.Fatalf("MarginToGhostCity: %v", err)
	}
	if result.Confidence == ConfidenceComputed {
		t.Errorf("Confidence = Computed for an infinite HistoricPeak, want Unavailable")
	}
}

// TestBreak2b_DivisionOverflowNeverComputed is Destructive round 2's
// residual finding: the isNonFinite guards on now/prev only check the
// INPUTS to extrapolateToThreshold's arithmetic, not the DERIVED
// `months := remainingToThreshold / slope` result. All-finite inputs
// can still overflow that division into +Inf — peak=1e308 gives an
// astronomically large threshold/remainingToThreshold, and a
// near-zero-but-nonzero slope (prev/now differing by 1e-15) divides it
// into +Inf, which used to fall straight past the `months < 0` clamp
// (false for +Inf) and get returned tagged ConfidenceComputed: a
// "confidently-computed infinite runway" handed to finance/spiral,
// the same AC-6 violation class as BREAK-2, one arithmetic step
// later. Reverting the isNonFinite(months) guard in
// extrapolateToThreshold makes this test fail (Confidence would read
// Computed with a +Inf MonthsRemaining instead of Unavailable).
func TestBreak2b_DivisionOverflowNeverComputed(t *testing.T) {
	provider := newFakeTrendProvider()
	provider.setPeak(1e308)
	provider.setState(map[int64]float64{0: 1 - 1e-15, 1: 1.0})

	api := NewProjectionsAPI()
	if err := api.RegisterCurveProvider(CurveKeyGhostCityPopulation, provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	result, err := api.MarginToGhostCity(1)
	if err != nil {
		t.Fatalf("MarginToGhostCity: %v", err)
	}
	if result.Confidence == ConfidenceComputed {
		t.Fatalf("Confidence = Computed for a finite-input division that overflows to +Inf (MonthsRemaining=%v), want Unavailable", result.MonthsRemaining)
	}
	if !math.IsInf(result.MonthsRemaining, 1) {
		t.Errorf("MonthsRemaining = %v, want +Inf preserved for diagnosis — the repro inputs no longer overflow the division, check they still reproduce the original finding", result.MonthsRemaining)
	}
}

// --- BREAK 3: overflowing make() panics instead of erroring (projections.go) --

// TestBreak3_HugeRangeReturnsErrorNotPanic is BREAK-3's exact repro:
// Curve("k", 0, math.MaxInt64) used to compute
// `make([]Point, 0, toMonth-fromMonth+1)`, where toMonth-fromMonth+1
// overflows int64 into a negative capacity, and make() panics
// ("makeslice: cap out of range") instead of this package returning
// its normal GR#7 registry-sourced error. If this test's surrounding
// process were to panic, `go test` would report it as a crash, not a
// clean test failure — this test's job is to prove Curve returns an
// ordinary error value instead, so a caller (or CI) never sees an
// unrecovered panic for caller-controlled input.
func TestBreak3_HugeRangeReturnsErrorNotPanic(t *testing.T) {
	api := NewProjectionsAPI()
	if err := api.RegisterCurveProvider("test.curve", fakeProvider{def: 1}); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	points, err := api.Curve("test.curve", 0, math.MaxInt64)
	if err == nil {
		t.Fatalf("Curve(0, math.MaxInt64) returned no error (points=%v) — want a registry-sourced bounds error, not a silent huge allocation attempt", points)
	}
	assertCode(t, err, ErrNegativeMonthQuery)
	if points != nil {
		t.Errorf("Curve returned %+v alongside an error, want nil", points)
	}
}

// TestBreak3_InvertedRangeRejected covers the same make()-cap hazard's
// simpler trigger: fromMonth > toMonth also drives
// toMonth-fromMonth+1 negative (e.g. fromMonth=5, toMonth=3 gives
// cap=-1), which panicked exactly as readily as the overflow case
// before this fix, via a much more mundane caller mistake (no
// overflow arithmetic needed at all).
func TestBreak3_InvertedRangeRejected(t *testing.T) {
	api := NewProjectionsAPI()
	if err := api.RegisterCurveProvider("test.curve", fakeProvider{def: 1}); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	points, err := api.Curve("test.curve", 5, 3)
	if err == nil {
		t.Fatalf("Curve(5, 3) (inverted range) returned no error (points=%v)", points)
	}
	assertCode(t, err, ErrNegativeMonthQuery)
}

// TestBreak3_LargeButNonOverflowingRangeStillRejected proves the fix's
// bound is a genuine safety ceiling (maxCurveQueryMonths), not merely
// a check for the exact overflow boundary — a caller-supplied range
// that would allocate an enormous (but not overflowing) slice is
// rejected too, rather than being allowed through to exhaust memory.
func TestBreak3_LargeButNonOverflowingRangeStillRejected(t *testing.T) {
	api := NewProjectionsAPI()
	if err := api.RegisterCurveProvider("test.curve", fakeProvider{def: 1}); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	_, err := api.Curve("test.curve", 0, maxCurveQueryMonths+1)
	assertCode(t, err, ErrNegativeMonthQuery)
}

// TestBreak3_OrdinaryRangeStillWorks proves the fix did not
// over-tighten: a normal, small, valid query range must still succeed.
func TestBreak3_OrdinaryRangeStillWorks(t *testing.T) {
	api := NewProjectionsAPI()
	if err := api.RegisterCurveProvider("test.curve", fakeProvider{def: 1}); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	points, err := api.Curve("test.curve", 0, 12)
	if err != nil {
		t.Fatalf("Curve(0, 12): %v", err)
	}
	if len(points) != 13 {
		t.Errorf("len(points) = %d, want 13", len(points))
	}
}

// --- LOWER-SEV: an all-zero Series must not satisfy the Slow-Fuse gate --

// TestLowerSev_AllZeroSeriesConsequenceStillCountsAsEmpty is the
// break report's lower-severity gap: a ProjectedConsequence carrying
// only zero-value Points (no Description, Series: []Point{{}}) used
// to satisfy ProjectedConsequence.empty()'s old check (`Description ==
// "" && len(Series) == 0`) as "non-empty" purely because len(Series)
// was 1, despite carrying no real information. Reverting empty()'s
// per-point zero-value scan makes this test fail (EnqueueDecision
// would succeed instead of returning ErrSlowFuseMissingPayload).
func TestLowerSev_AllZeroSeriesConsequenceStillCountsAsEmpty(t *testing.T) {
	api := NewProjectionsAPI()
	err := api.EnqueueDecision(Decision{
		ID:        "break-lowersev",
		FuseYears: 8,
		Consequence: &ProjectedConsequence{
			Series: []Point{{}}, // one all-zero-value Point, no Description
		},
	})
	assertCode(t, err, ErrSlowFuseMissingPayload)
}

// TestLowerSev_GenuineSeriesStillSatisfiesTheGate proves the fix did
// not over-tighten: a Series carrying at least one real (non-zero)
// Point still counts as a valid payload, with no Description needed.
func TestLowerSev_GenuineSeriesStillSatisfiesTheGate(t *testing.T) {
	api := NewProjectionsAPI()
	err := api.EnqueueDecision(Decision{
		ID:        "break-lowersev-valid",
		FuseYears: 8,
		Consequence: &ProjectedConsequence{
			Series: []Point{{Month: 72, Value: 42, Confidence: ConfidenceComputed}},
		},
	})
	if err != nil {
		t.Errorf("a Consequence with a genuine non-zero Series point was rejected: %v", err)
	}
}
