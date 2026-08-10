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
	cmp := CompareToBaseline(nil, current)

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
	baseline := PerfResult{PerMonthTick: 100 * time.Millisecond}
	// +9% growth: under the 10% threshold.
	current := PerfResult{PerMonthTick: 109 * time.Millisecond}

	cmp := CompareToBaseline(&baseline, current)
	if !cmp.HasBaseline {
		t.Fatal("HasBaseline should be true")
	}
	if cmp.Regressed {
		t.Fatalf("a %.1f%% growth should not regress at a %.0f%% threshold", cmp.DeltaFraction*100, RegressionThreshold*100)
	}
}

func TestCompareToBaseline_RegressionOverThreshold(t *testing.T) {
	baseline := PerfResult{PerMonthTick: 100 * time.Millisecond}
	// +25% growth: comfortably over the 10% threshold.
	current := PerfResult{PerMonthTick: 125 * time.Millisecond}

	cmp := CompareToBaseline(&baseline, current)
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
	baseline := PerfResult{PerMonthTick: 1 * time.Microsecond}
	current := PerfResult{PerMonthTick: 3 * time.Microsecond} // +200%, but both are noise

	cmp := CompareToBaseline(&baseline, current)
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
	baseline := PerfResult{PerMonthTick: 1 * time.Microsecond} // below floor
	current := PerfResult{PerMonthTick: 50 * time.Millisecond} // above floor, huge absolute jump

	cmp := CompareToBaseline(&baseline, current)
	if !cmp.BelowNoiseFloor {
		t.Fatal("BelowNoiseFloor should be true when the BASELINE alone is under MinMeasurableDuration, even if current is not")
	}
	if cmp.Regressed {
		t.Fatal("must not report Regressed while BelowNoiseFloor is true")
	}
}
