package synth

import (
	"testing"
	"time"
)

// TestCompareToBaseline_NoBaseline is AC-8: a missing baseline does not
// fail the build — it reports "no prior baseline to compare" rather
// than treating "no baseline" as a 10% regression.
func TestCompareToBaseline_NoBaseline(t *testing.T) {
	current := PerfResult{PerMonthTick: 50 * time.Millisecond}
	cmp := CompareToBaseline(nil, nil, current)

	if cmp.HasBaseline {
		t.Fatal("HasBaseline should be false when baseline is nil")
	}
	if cmp.Regressed {
		t.Fatal("a missing baseline must never be treated as a regression (AC-8)")
	}
	if cmp.Message == "" {
		t.Fatal("Message should explain that no baseline exists")
	}
}

// TestCompareToBaseline_NoRegressionWithinThreshold and
// TestCompareToBaseline_RegressionOverThreshold together prove the
// RegressionThreshold boundary is exercised on both sides.
func TestCompareToBaseline_NoRegressionWithinThreshold(t *testing.T) {
	// Months is set (BUG-254) so the measured tick window (PerMonthTick x
	// Months) clears MinMeasurableDuration and the percentage check runs.
	baseline := PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 100 * time.Millisecond}
	// +9% growth: under the 10% threshold.
	current := PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 109 * time.Millisecond}

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.HasBaseline {
		t.Fatal("HasBaseline should be true")
	}
	if cmp.Regressed {
		t.Fatalf("a %.1f%% growth should not regress at a %.0f%% threshold", cmp.DeltaFraction*100, RegressionThreshold*100)
	}
}

func TestCompareToBaseline_RegressionOverThreshold(t *testing.T) {
	baseline := PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 100 * time.Millisecond}
	// +25% growth: comfortably over the 10% threshold.
	current := PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 125 * time.Millisecond}

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.Regressed {
		t.Fatalf("a %.1f%% growth should regress at a %.0f%% threshold", cmp.DeltaFraction*100, RegressionThreshold*100)
	}
}

// TestCompareToBaseline_BelowNoiseFloorSkipsRegressionCheck is the
// BUG-031-avoidance property this item's dispatch brief specifically
// asked for: a huge PERCENTAGE regression against a near-zero absolute
// baseline (the walking-skeleton common case, see doc.go) must not fail
// the gate — it must be reported as "below the noise floor", never as a
// regression.
func TestCompareToBaseline_BelowNoiseFloorSkipsRegressionCheck(t *testing.T) {
	// Both windows (PerMonthTick x Months) sit far under
	// MinMeasurableDuration — the walking-skeleton shape.
	baseline := PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 1 * time.Microsecond}
	current := PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 3 * time.Microsecond} // +200%, but both are noise

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.BelowNoiseFloor {
		t.Fatal("BelowNoiseFloor should be true when both measurements are under MinMeasurableDuration")
	}
	if cmp.Regressed {
		t.Fatal("a below-noise-floor comparison must never be reported as Regressed — this is exactly the BUG-031 trap this gate is designed to avoid")
	}
}

// TestCompareToBaseline_OneSideBelowNoiseFloorAlsoSkips proves the floor
// applies if EITHER side is below it, not only when both are.
func TestCompareToBaseline_OneSideBelowNoiseFloorAlsoSkips(t *testing.T) {
	baseline := PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 1 * time.Microsecond} // window 12us: below floor
	current := PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 50 * time.Millisecond} // window 600ms: above floor, huge absolute jump

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.BelowNoiseFloor {
		t.Fatal("BelowNoiseFloor should be true when the BASELINE alone is under MinMeasurableDuration, even if current is not")
	}
	if cmp.Regressed {
		t.Fatal("must not report Regressed while BelowNoiseFloor is true")
	}
}

// TestCompareToBaseline_CitizenCountMismatchSkipsRegressionCheck is
// BUG-056's regression test: a smoke-scale run (few citizens) and a
// real-scale run (1,000,000 citizens) both carrying the same Preset
// label must never be compared as if they were the same scale, even
// though today's checked-in CI config already keeps them in separate
// files operationally. A huge, entirely scale-driven jump must be
// reported as ScaleMismatch, never as Regressed.
func TestCompareToBaseline_CitizenCountMismatchSkipsRegressionCheck(t *testing.T) {
	baseline := PerfResult{CitizenCount: 2000, Months: 3, PerMonthTick: 5 * time.Millisecond}
	current := PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 500 * time.Millisecond}

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.ScaleMismatch {
		t.Fatal("ScaleMismatch should be true when baseline and current CitizenCount differ")
	}
	if cmp.Regressed {
		t.Fatal("a scale mismatch must never be reported as Regressed — the comparison is not meaningful across scales")
	}
}

// TestCompareToBaseline_MonthsMismatchSkipsRegressionCheck is the
// Months half of BUG-056's cross-check — a different run length also
// makes a percentage comparison meaningless even at the same
// CitizenCount.
func TestCompareToBaseline_MonthsMismatchSkipsRegressionCheck(t *testing.T) {
	baseline := PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 5 * time.Millisecond}
	current := PerfResult{CitizenCount: OneMillionCitizens, Months: 12, PerMonthTick: 5 * time.Millisecond}

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.ScaleMismatch {
		t.Fatal("ScaleMismatch should be true when baseline and current Months differ")
	}
	if cmp.Regressed {
		t.Fatal("a scale mismatch must never be reported as Regressed")
	}
}

// TestCouldNotEvaluate_TrueForScaleMismatchAndBelowNoiseFloor is BUG-071's
// unit-level check on the exact predicate cmd/perfci gates its exit code
// and baseline-write decision on: both skip reasons must report
// CouldNotEvaluate() true.
func TestCouldNotEvaluate_TrueForScaleMismatchAndBelowNoiseFloor(t *testing.T) {
	scaleMismatch := BaselineComparison{HasBaseline: true, ScaleMismatch: true}
	if !scaleMismatch.CouldNotEvaluate() {
		t.Fatal("CouldNotEvaluate() should be true when ScaleMismatch is set")
	}

	belowFloor := BaselineComparison{HasBaseline: true, BelowNoiseFloor: true}
	if !belowFloor.CouldNotEvaluate() {
		t.Fatal("CouldNotEvaluate() should be true when BelowNoiseFloor is set")
	}
}

// TestCouldNotEvaluate_FalseWhenNoBaseline is the AC-8 boundary BUG-071's
// fix must not disturb: a missing baseline is a genuine, expected,
// DIFFERENT outcome (record this run as the new baseline, exit 0) — not
// a failed attempt at a comparison. Conflating the two would misfile
// the common "first run on a fresh preset" case as an alarming
// could-not-evaluate.
func TestCouldNotEvaluate_FalseWhenNoBaseline(t *testing.T) {
	noBaseline := CompareToBaseline(nil, nil, PerfResult{PerMonthTick: 3 * time.Microsecond})
	if noBaseline.CouldNotEvaluate() {
		t.Fatal("CouldNotEvaluate() should be false when HasBaseline is false — AC-8's missing-baseline case records a new baseline, it is not a skipped comparison")
	}
}

// TestCouldNotEvaluate_FalseForGenuinePassAndRegression proves the
// predicate does not over-fire on the two outcomes it must leave alone.
func TestCouldNotEvaluate_FalseForGenuinePassAndRegression(t *testing.T) {
	pass := CompareToBaseline(&PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 100 * time.Millisecond}, nil, PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 105 * time.Millisecond})
	if pass.CouldNotEvaluate() {
		t.Fatal("CouldNotEvaluate() should be false for a genuine within-threshold pass")
	}

	regressed := CompareToBaseline(&PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 100 * time.Millisecond}, nil, PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 200 * time.Millisecond})
	if !regressed.Regressed {
		t.Fatal("test setup: expected this pair to regress")
	}
	if regressed.CouldNotEvaluate() {
		t.Fatal("CouldNotEvaluate() should be false for a genuine regression — it has a real verdict, it was not skipped")
	}
}

// TestCompareToBaseline_WindowFloorBoundaryDerivesFromConstant pins
// BUG-254's re-scoped noise floor at its exact boundary, with every
// figure DERIVED from MinMeasurableDuration rather than hardcoded
// (GR#15): a measured tick window (PerMonthTick x Months) exactly AT the
// floor is evaluated; one even a hair below it is BelowNoiseFloor. If
// the constant's value or scope (window vs per-month) ever changes, this
// test moves with the constant or fails loudly — it cannot silently keep
// asserting a stale figure.
func TestCompareToBaseline_WindowFloorBoundaryDerivesFromConstant(t *testing.T) {
	// months chosen so MinMeasurableDuration divides exactly and the
	// at-floor window reconstructs to precisely MinMeasurableDuration
	// with no integer-division loss.
	const months = 10
	if MinMeasurableDuration%months != 0 {
		t.Fatalf("test fixture assumption broken: MinMeasurableDuration (%s) is not divisible by %d months — pick a divisor so the boundary is exact", MinMeasurableDuration, months)
	}
	perMonthAtFloor := MinMeasurableDuration / months

	atFloor := PerfResult{CitizenCount: 1000, Months: months, PerMonthTick: perMonthAtFloor}
	cmpAt := CompareToBaseline(&atFloor, nil, atFloor)
	if cmpAt.BelowNoiseFloor {
		t.Fatalf("a window exactly at MinMeasurableDuration (%s) must be evaluated, got BelowNoiseFloor: %s", MinMeasurableDuration, cmpAt.Message)
	}
	if cmpAt.Regressed {
		t.Fatalf("identical at-floor measurements must not regress: %s", cmpAt.Message)
	}

	below := PerfResult{CitizenCount: 1000, Months: months, PerMonthTick: perMonthAtFloor - time.Nanosecond}
	cmpBelow := CompareToBaseline(&below, nil, below)
	if !cmpBelow.BelowNoiseFloor {
		t.Fatalf("a window below MinMeasurableDuration (%s) must be BelowNoiseFloor, got: %s", MinMeasurableDuration, cmpBelow.Message)
	}
	if cmpBelow.Regressed {
		t.Fatal("a below-floor comparison must never be Regressed")
	}
}

// TestCompareToBaseline_QuantumScaleWindowIsRefusedNotJudged is BUG-254's
// direct reproduction: the exact pre-fix CI shape — a real ~3.3ms/month
// hook-work measurement over only 3 months (a ~10ms window, a single-digit
// multiple of the ~1ms Windows timer quantum) showing an 18.9% delta that
// was pure runner noise — must be refused as BelowNoiseFloor, never
// reported as REGRESSED. RED against the pre-fix 2ms PER-MONTH floor
// (both sides cleared 2ms, the 18.9% delta compared as a genuine step
// regression over the 10% threshold, and the required perf gate went red
// on a zero-change commit); GREEN against the window-scoped floor.
func TestCompareToBaseline_QuantumScaleWindowIsRefusedNotJudged(t *testing.T) {
	baseline := PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 3320 * time.Microsecond} // ~9.96ms window
	current := PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 3947 * time.Microsecond}  // +18.9% — the live-verified CI noise figure

	cmp := CompareToBaseline(&baseline, nil, current)
	if cmp.Regressed {
		t.Fatalf("BUG-254: a quantum-scale (%s) window's noise delta was judged as a regression: %s", measuredTickWindow(baseline), cmp.Message)
	}
	if !cmp.BelowNoiseFloor {
		t.Fatalf("BUG-254: a quantum-scale window must be refused as BelowNoiseFloor (honest exit 3 in cmd/perfci), got: %s", cmp.Message)
	}
}
