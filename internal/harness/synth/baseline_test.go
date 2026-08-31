package synth

import (
	"math"
	"strings"
	"testing"
	"time"
)

// safeAllocCount/safeAllocBytes are fixture helpers: a value comfortably
// above MinMeasurableAllocs (BUG-272's primary noise floor), so tests
// that want the PRIMARY allocation-based check to actually run (rather
// than being skipped as BelowNoiseFloor) can build realistic fixtures
// without each test re-deriving its own "big enough" number.
const (
	safeAllocCount uint64 = 100_000
	safeAllocBytes uint64 = 10_000_000
)

// TestCompareToBaseline_NoBaseline is AC-8: a missing baseline does not
// fail the build — it reports "no prior baseline to compare" rather
// than treating "no baseline" as a 10% regression.
func TestCompareToBaseline_NoBaseline(t *testing.T) {
	current := PerfResult{PerMonthTick: 50 * time.Millisecond, AllocCount: safeAllocCount, AllocBytes: safeAllocBytes}
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
// TestCompareToBaseline_RegressionOverThreshold together prove
// RegressionThreshold's boundary is exercised on both sides of the
// PRIMARY, allocation-based signal (BUG-272) — not wall-clock, which is
// now advisory-only (see TestCompareToBaseline_WallClockNoiseAloneDoesNotRegress
// below for the fix this item actually exists to prove).
func TestCompareToBaseline_NoRegressionWithinThreshold(t *testing.T) {
	baseline := PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 100 * time.Millisecond, AllocBytes: 1_000_000, AllocCount: 100_000}
	// +9% growth on both alloc metrics: under the 10% threshold.
	current := PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 100 * time.Millisecond, AllocBytes: 1_090_000, AllocCount: 109_000}

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.HasBaseline {
		t.Fatal("HasBaseline should be true")
	}
	if cmp.BelowNoiseFloor {
		t.Fatalf("both sides clear MinMeasurableAllocs, the primary check should have run: %s", cmp.Message)
	}
	if cmp.Regressed {
		t.Fatalf("a %.1f%% alloc-bytes growth / %.1f%% alloc-count growth should not regress at a %.0f%% threshold: %s", cmp.AllocBytesDeltaFraction*100, cmp.AllocCountDeltaFraction*100, RegressionThreshold*100, cmp.Message)
	}
}

func TestCompareToBaseline_RegressionOverThreshold(t *testing.T) {
	baseline := PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 100 * time.Millisecond, AllocBytes: 1_000_000, AllocCount: 100_000}
	// +25% growth on both alloc metrics: comfortably over the 10% threshold.
	current := PerfResult{CitizenCount: 1000, Months: 12, PerMonthTick: 100 * time.Millisecond, AllocBytes: 1_250_000, AllocCount: 125_000}

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.StepRegressed {
		t.Fatalf("a %.1f%% alloc-bytes growth / %.1f%% alloc-count growth should step-regress at a %.0f%% threshold: %s", cmp.AllocBytesDeltaFraction*100, cmp.AllocCountDeltaFraction*100, RegressionThreshold*100, cmp.Message)
	}
	if !cmp.Regressed {
		t.Fatal("StepRegressed must imply the overall Regressed verdict")
	}
}

// TestCompareToBaseline_RegressionThresholdUnchanged pins RegressionThreshold
// at its spec-mandated 10% (M0-ENG §6 point 5) — BUG-272 moved WHAT the
// threshold is applied to (allocations instead of wall-clock), never the
// figure itself. A future edit that quietly raises this constant to make
// noisy signals pass more easily is the exact "gate-weakening" move this
// project bans; this test fails loudly if that ever happens.
func TestCompareToBaseline_RegressionThresholdUnchanged(t *testing.T) {
	if RegressionThreshold != 0.10 {
		t.Fatalf("RegressionThreshold must stay at the spec-mandated 10%% (M0-ENG §6 point 5) — got %.4f", RegressionThreshold)
	}
}

// TestCompareToBaseline_AllocBytesAloneCanRegress and
// TestCompareToBaseline_AllocCountAloneCanRegress prove EITHER alloc
// metric crossing the threshold is sufficient — a regression that grows
// one without moving the other must not slip through by only checking
// the other (BaselineComparison.StepRegressed's doc comment).
func TestCompareToBaseline_AllocBytesAloneCanRegress(t *testing.T) {
	baseline := PerfResult{CitizenCount: 1000, Months: 12, AllocBytes: 1_000_000, AllocCount: 100_000}
	current := PerfResult{CitizenCount: 1000, Months: 12, AllocBytes: 1_500_000, AllocCount: 100_500} // bytes +50%, count +0.5%

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.StepRegressed {
		t.Fatalf("an AllocBytes-only regression must still trip StepRegressed: %s", cmp.Message)
	}
}

func TestCompareToBaseline_AllocCountAloneCanRegress(t *testing.T) {
	baseline := PerfResult{CitizenCount: 1000, Months: 12, AllocBytes: 1_000_000, AllocCount: 100_000}
	current := PerfResult{CitizenCount: 1000, Months: 12, AllocBytes: 1_005_000, AllocCount: 150_000} // bytes +0.5%, count +50%

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.StepRegressed {
		t.Fatalf("an AllocCount-only regression must still trip StepRegressed: %s", cmp.Message)
	}
}

// TestCompareToBaseline_WallClockNoiseAloneDoesNotRegress is BUG-272's
// headline fix, reproduced directly: the live-verified CI failure was a
// docs-only PR, byte-identical engine code, reporting a 45% wall-clock
// "regression" purely from shared-runner jitter. With allocation counts
// UNCHANGED (as a genuinely identical build would measure) and only
// PerMonthTick moved by 45%, the gate must NOT regress — this is the
// exact scenario that used to fail every PR before this fix.
func TestCompareToBaseline_WallClockNoiseAloneDoesNotRegress(t *testing.T) {
	baseline := PerfResult{
		CitizenCount: OneMillionCitizens, Months: 500,
		PerMonthTick: 172825 * time.Nanosecond, // BUG-272's own cited baseline figure
		AllocBytes:   safeAllocBytes, AllocCount: safeAllocCount,
	}
	current := PerfResult{
		CitizenCount: OneMillionCitizens, Months: 500,
		PerMonthTick: 250552 * time.Nanosecond,                   // BUG-272's own cited "regressed" figure: +45%
		AllocBytes:   safeAllocBytes, AllocCount: safeAllocCount, // IDENTICAL allocations — same code
	}

	cmp := CompareToBaseline(&baseline, nil, current)
	if cmp.Regressed {
		t.Fatalf("BUG-272: a 45%% wall-clock delta with IDENTICAL allocation counts must not regress the gate (this is the exact false-positive that blocked every PR): %s", cmp.Message)
	}
	if cmp.StepRegressed {
		t.Fatal("the primary allocation-based step check must not fire when allocations did not change")
	}
	if cmp.BelowNoiseFloor {
		t.Fatalf("both sides clear MinMeasurableAllocs, the primary check should have run and passed: %s", cmp.Message)
	}
	if cmp.WallClockGrossRegressed {
		t.Fatalf("a 45%% wall-clock delta must not trip the advisory GROSS check (threshold is %.0f%%, i.e. >2x): %s", WallClockGrossRegressionThreshold*100, cmp.Message)
	}
	// The advisory delta is still recorded (informational), just not gating.
	if cmp.DeltaFraction <= 0 {
		t.Fatalf("DeltaFraction should still report the observed advisory wall-clock delta, got %.4f", cmp.DeltaFraction)
	}
}

// TestCompareToBaseline_WallClockGrossRegressionIsAdvisoryOnly is
// BUG-473's headline regression test. A gross (>2x) wall-clock slowdown
// with UNCHANGED allocation counts must STILL be detected
// (WallClockGrossRegressed true) and reported in the Message, but it must
// NOT set Regressed — wall-clock is advisory only and never gates CI.
//
// This test is RED against the pre-fix code: the old line was
// `Regressed = StepRegressed || CumulativeRegressed ||
// WallClockGrossRegressed`, which set Regressed true here. Re-adding
// `|| cmp.WallClockGrossRegressed` to CompareToBaseline reddens the
// `if cmp.Regressed` assertion below (prove-can-fail).
//
// The failure mode BUG-473 closes: at perf-SMOKE scale the per-month
// figure is sub-millisecond, so a routine ~2x jitter spike trips this
// 100% gross threshold and used to FAIL the job on a comment-only doc
// commit. Now it only warns.
func TestCompareToBaseline_WallClockGrossRegressionIsAdvisoryOnly(t *testing.T) {
	baseline := PerfResult{
		CitizenCount: OneMillionCitizens, Months: 500,
		PerMonthTick: 172825 * time.Nanosecond,
		AllocBytes:   safeAllocBytes, AllocCount: safeAllocCount,
	}
	current := PerfResult{
		CitizenCount: OneMillionCitizens, Months: 500,
		PerMonthTick: 3 * 172825 * time.Nanosecond,               // 3x — well over the gross threshold
		AllocBytes:   safeAllocBytes, AllocCount: safeAllocCount, // allocations UNCHANGED
	}

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.WallClockGrossRegressed {
		t.Fatalf("a 3x wall-clock slowdown must still be DETECTED as a gross regression (the signal is preserved, just non-blocking): %s", cmp.Message)
	}
	if cmp.Regressed {
		t.Fatalf("BUG-473: a wall-clock-only gross regression must NOT set the merge-blocking Regressed verdict — wall-clock is advisory only and can never fail CI: %s", cmp.Message)
	}
	if cmp.StepRegressed {
		t.Fatal("the primary allocation-based check must not have fired (allocations were unchanged)")
	}
	if cmp.CumulativeRegressed {
		t.Fatal("the cumulative allocation check must not have fired (allocations were unchanged)")
	}
	// The advisory signal must remain visible in the Message so cmd/perfci
	// can surface it as a ::warning::.
	if !strings.Contains(cmp.Message, "ADVISORY WARNING") || !strings.Contains(cmp.Message, "wall-clock") {
		t.Fatalf("Message must still carry the advisory wall-clock GROSS warning so it stays visible, got: %s", cmp.Message)
	}
}

// TestCompareToBaseline_AllocRegressionStillGatesRegardlessOfWallClock is
// BUG-473's companion "the real gate still fires" check: an allocation
// regression past the threshold must set Regressed whether or not the
// advisory wall-clock check also fires. Demoting wall-clock must not
// weaken the allocation gate.
func TestCompareToBaseline_AllocRegressionStillGatesRegardlessOfWallClock(t *testing.T) {
	baseline := PerfResult{
		CitizenCount: OneMillionCitizens, Months: 500,
		PerMonthTick: 172825 * time.Nanosecond,
		AllocBytes:   1_000_000, AllocCount: 100_000,
	}
	current := PerfResult{
		CitizenCount: OneMillionCitizens, Months: 500,
		PerMonthTick: 172825 * time.Nanosecond,       // wall-clock flat
		AllocBytes:   1_250_000, AllocCount: 125_000, // +25% allocations — a real regression
	}

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.StepRegressed {
		t.Fatalf("a +25%% allocation regression must trip the primary step check: %s", cmp.Message)
	}
	if !cmp.Regressed {
		t.Fatalf("BUG-473 must NOT weaken the allocation gate — an alloc regression must still set Regressed: %s", cmp.Message)
	}
}

// TestCompareToBaseline_BelowAllocNoiseFloorSkipsRegressionCheck is
// BUG-272's re-scoped analogue of the original BUG-031-avoidance
// property: a huge PERCENTAGE regression against a near-zero absolute
// allocation count must not fail the gate — it must be reported as
// "below the (allocation) noise floor", never as a regression.
func TestCompareToBaseline_BelowAllocNoiseFloorSkipsRegressionCheck(t *testing.T) {
	baseline := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: 5, AllocBytes: 400}
	current := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: 15, AllocBytes: 1200} // +200%, but both are noise-scale

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.BelowNoiseFloor {
		t.Fatal("BelowNoiseFloor should be true when both AllocCounts are under MinMeasurableAllocs")
	}
	if cmp.Regressed {
		t.Fatal("a below-noise-floor comparison must never be reported as Regressed — this is exactly the BUG-031-class trap this gate is designed to avoid, now re-scoped to allocations (BUG-272)")
	}
}

// TestCompareToBaseline_OneSideBelowAllocNoiseFloorAlsoSkips proves the
// allocation floor applies if EITHER side is below it, not only when
// both are.
func TestCompareToBaseline_OneSideBelowAllocNoiseFloorAlsoSkips(t *testing.T) {
	baseline := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: 5, AllocBytes: 400}                        // below floor
	current := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: safeAllocCount, AllocBytes: safeAllocBytes} // above floor, huge absolute jump

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.BelowNoiseFloor {
		t.Fatal("BelowNoiseFloor should be true when the BASELINE alone is under MinMeasurableAllocs, even if current is not")
	}
	if cmp.Regressed {
		t.Fatal("must not report Regressed (from the primary signal) while BelowNoiseFloor is true")
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
	baseline := PerfResult{CitizenCount: 2000, Months: 3, PerMonthTick: 5 * time.Millisecond, AllocCount: safeAllocCount, AllocBytes: safeAllocBytes}
	current := PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 500 * time.Millisecond, AllocCount: safeAllocCount * 100, AllocBytes: safeAllocBytes * 100}

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
	baseline := PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 5 * time.Millisecond, AllocCount: safeAllocCount, AllocBytes: safeAllocBytes}
	current := PerfResult{CitizenCount: OneMillionCitizens, Months: 12, PerMonthTick: 5 * time.Millisecond, AllocCount: safeAllocCount, AllocBytes: safeAllocBytes}

	cmp := CompareToBaseline(&baseline, nil, current)
	if !cmp.ScaleMismatch {
		t.Fatal("ScaleMismatch should be true when baseline and current Months differ")
	}
	if cmp.Regressed {
		t.Fatal("a scale mismatch must never be reported as Regressed")
	}
}

// TestCompareToBaseline_CumulativeAllocDriftCaught is BUG-083's
// anchor-based sustained-drift check, re-based onto allocations
// (BUG-272): a current run that is under RegressionThreshold against the
// immediately-prior baseline (so StepRegressed is false) but far over
// CumulativeRegressionThreshold against the FIXED anchor must still be
// caught.
func TestCompareToBaseline_CumulativeAllocDriftCaught(t *testing.T) {
	anchor := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: 100_000, AllocBytes: 1_000_000}
	baseline := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: 200_000, AllocBytes: 2_000_000} // already 100% over anchor (pre-existing, e.g. accepted)
	current := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: 205_000, AllocBytes: 2_050_000}  // +2.5% over baseline (step OK) but +105% over anchor (cumulative threshold is 20%)

	cmp := CompareToBaseline(&baseline, &anchor, current)
	if cmp.StepRegressed {
		t.Fatalf("the step check (vs baseline) should NOT fire: %s", cmp.Message)
	}
	if !cmp.CumulativeChecked {
		t.Fatal("CumulativeChecked should be true — anchor is same-scale and above MinMeasurableAllocs")
	}
	if !cmp.CumulativeRegressed {
		t.Fatalf("a %.1f%%/%.1f%% cumulative drift over anchor should trip CumulativeRegressionThreshold (%.0f%%): %s", cmp.CumulativeAllocBytesDeltaFraction*100, cmp.CumulativeAllocCountDeltaFraction*100, CumulativeRegressionThreshold*100, cmp.Message)
	}
	if !cmp.Regressed {
		t.Fatal("CumulativeRegressed must still drive the overall Regressed verdict")
	}
}

// TestCompareToBaseline_ZeroBaselineAllocBytes_NoNaN is BUG-305's 0/0
// case: baseline.AllocBytes == 0 (a real, if unusual, measurement — the
// synth run allocated zero net bytes on this metric) with current also
// 0. A bare float64(0)/float64(0) division is NaN, which would then
// silently poison DeltaFraction/StepRegressed/every %.1f%% message built
// from it — and NaN's comparisons are always false, so `NaN >
// RegressionThreshold` never fires either, letting corruption slip past
// undetected. This proves the guarded path returns a finite 0, not NaN.
func TestCompareToBaseline_ZeroBaselineAllocBytes_NoNaN(t *testing.T) {
	baseline := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: safeAllocCount, AllocBytes: 0}
	current := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: safeAllocCount, AllocBytes: 0}

	cmp := CompareToBaseline(&baseline, nil, current)
	if math.IsNaN(cmp.AllocBytesDeltaFraction) {
		t.Fatalf("AllocBytesDeltaFraction is NaN (0/0) — must be a finite value, got %v", cmp.AllocBytesDeltaFraction)
	}
	if cmp.AllocBytesDeltaFraction != 0 {
		t.Errorf("AllocBytesDeltaFraction = %v, want 0 (baseline and current both measured zero)", cmp.AllocBytesDeltaFraction)
	}
	if cmp.StepRegressed {
		t.Errorf("StepRegressed should be false — no genuine bytes growth (0 -> 0) and count delta is also 0")
	}
}

// TestCompareToBaseline_ZeroBaselineAllocBytesGrowth_RegressedNotNaN
// covers the other 0-baseline branch: baseline measured zero bytes but
// current measured a real, positive amount — genuine unbounded
// proportional growth. This must be reported as a regression (+Inf, not
// NaN), never silently pass a NaN-poisoned comparison.
func TestCompareToBaseline_ZeroBaselineAllocBytesGrowth_RegressedNotNaN(t *testing.T) {
	baseline := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: safeAllocCount, AllocBytes: 0}
	current := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: safeAllocCount, AllocBytes: 500_000}

	cmp := CompareToBaseline(&baseline, nil, current)
	if math.IsNaN(cmp.AllocBytesDeltaFraction) {
		t.Fatalf("AllocBytesDeltaFraction is NaN — must be a finite/Inf value that still compares correctly, got %v", cmp.AllocBytesDeltaFraction)
	}
	if !cmp.StepRegressed {
		t.Fatalf("growth from a zero baseline to a real positive AllocBytes must be reported as regressed: %s", cmp.Message)
	}
	if !cmp.Regressed {
		t.Fatal("StepRegressed must drive the overall Regressed verdict")
	}
}

// TestCouldNotEvaluate_TrueForScaleMismatchAndBelowNoiseFloor is BUG-071's
// unit-level check on the exact predicate cmd/perfci gates its exit code
// and baseline-write decision on: both skip reasons must report
// CouldNotEvaluate() true. BelowNoiseFloor is now the PRIMARY
// (allocation-based) floor (BUG-272) — CouldNotEvaluate()'s own logic is
// unchanged.
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
	noBaseline := CompareToBaseline(nil, nil, PerfResult{PerMonthTick: 3 * time.Microsecond, AllocCount: safeAllocCount, AllocBytes: safeAllocBytes})
	if noBaseline.CouldNotEvaluate() {
		t.Fatal("CouldNotEvaluate() should be false when HasBaseline is false — AC-8's missing-baseline case records a new baseline, it is not a skipped comparison")
	}
}

// TestCouldNotEvaluate_FalseForGenuinePassAndRegression proves the
// predicate does not over-fire on the two outcomes it must leave alone.
func TestCouldNotEvaluate_FalseForGenuinePassAndRegression(t *testing.T) {
	pass := CompareToBaseline(
		&PerfResult{CitizenCount: 1000, Months: 12, AllocCount: 100_000, AllocBytes: 1_000_000},
		nil,
		PerfResult{CitizenCount: 1000, Months: 12, AllocCount: 105_000, AllocBytes: 1_050_000},
	)
	if pass.CouldNotEvaluate() {
		t.Fatal("CouldNotEvaluate() should be false for a genuine within-threshold pass")
	}

	regressed := CompareToBaseline(
		&PerfResult{CitizenCount: 1000, Months: 12, AllocCount: 100_000, AllocBytes: 1_000_000},
		nil,
		PerfResult{CitizenCount: 1000, Months: 12, AllocCount: 200_000, AllocBytes: 2_000_000},
	)
	if !regressed.Regressed {
		t.Fatal("test setup: expected this pair to regress")
	}
	if regressed.CouldNotEvaluate() {
		t.Fatal("CouldNotEvaluate() should be false for a genuine regression — it has a real verdict, it was not skipped")
	}
}

// TestCompareToBaseline_AllocFloorBoundaryDerivesFromConstant pins
// BUG-272's re-scoped noise floor at its exact boundary, with every
// figure DERIVED from MinMeasurableAllocs rather than hardcoded
// (GR#15): an AllocCount exactly AT the floor is evaluated; one even one
// below it is BelowNoiseFloor. If the constant's value ever changes,
// this test moves with it or fails loudly — it cannot silently keep
// asserting a stale figure.
func TestCompareToBaseline_AllocFloorBoundaryDerivesFromConstant(t *testing.T) {
	atFloor := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: MinMeasurableAllocs, AllocBytes: safeAllocBytes}
	cmpAt := CompareToBaseline(&atFloor, nil, atFloor)
	if cmpAt.BelowNoiseFloor {
		t.Fatalf("an AllocCount exactly at MinMeasurableAllocs (%d) must be evaluated, got BelowNoiseFloor: %s", MinMeasurableAllocs, cmpAt.Message)
	}
	if cmpAt.Regressed {
		t.Fatalf("identical at-floor measurements must not regress: %s", cmpAt.Message)
	}

	below := PerfResult{CitizenCount: 1000, Months: 12, AllocCount: MinMeasurableAllocs - 1, AllocBytes: safeAllocBytes}
	cmpBelow := CompareToBaseline(&below, nil, below)
	if !cmpBelow.BelowNoiseFloor {
		t.Fatalf("an AllocCount below MinMeasurableAllocs (%d) must be BelowNoiseFloor, got: %s", MinMeasurableAllocs, cmpBelow.Message)
	}
	if cmpBelow.Regressed {
		t.Fatal("a below-floor comparison must never be Regressed")
	}
}

// TestCompareToBaseline_QuantumScaleWindowIsAdvisoryOnly is BUG-254's
// direct reproduction, re-verified under BUG-272's new regime: the exact
// pre-BUG-254 CI shape — a real ~3.3ms/month hook-work measurement over
// only 3 months (a ~10ms window, a single-digit multiple of the ~1ms
// Windows timer quantum) showing an 18.9% wall-clock delta that was pure
// runner noise — must never be treated as a REGRESSED verdict on its
// own. Since BUG-272, the wall-clock window floor (MinMeasurableDuration)
// only governs the now-ADVISORY check, so with a healthy, above-floor
// allocation count on both sides this comparison runs on the (unaffected)
// primary allocation signal and reports WallClockBelowNoiseFloor for the
// advisory side, not BelowNoiseFloor (which is alloc-only since BUG-272).
func TestCompareToBaseline_QuantumScaleWindowIsAdvisoryOnly(t *testing.T) {
	baseline := PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 3320 * time.Microsecond, AllocCount: safeAllocCount, AllocBytes: safeAllocBytes} // ~9.96ms window
	current := PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 3947 * time.Microsecond, AllocCount: safeAllocCount, AllocBytes: safeAllocBytes}  // +18.9% wall-clock — the live-verified CI noise figure; allocations unchanged

	cmp := CompareToBaseline(&baseline, nil, current)
	if cmp.Regressed {
		t.Fatalf("BUG-254/BUG-272: a quantum-scale (%s) wall-clock window's noise delta must not be judged as a regression when allocations are unchanged: %s", measuredTickWindow(baseline), cmp.Message)
	}
	if !cmp.WallClockBelowNoiseFloor {
		t.Fatalf("BUG-254: a quantum-scale wall-clock window must be refused by the advisory check's own floor, got: %s", cmp.Message)
	}
	if cmp.BelowNoiseFloor {
		t.Fatalf("BUG-272: the PRIMARY (allocation) floor is independent of the wall-clock window and both sides clear MinMeasurableAllocs here — BelowNoiseFloor should be false: %s", cmp.Message)
	}
}
