package synth

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// BaselineComparison is CompareToBaseline's verdict (AC-6, AC-8, AC-10).
//
// # BUG-272/BUG-473: allocation metrics are the ONLY gate, wall-clock is ADVISORY
//
// Every field below whose name mentions "Alloc" is part of the gate's
// PRIMARY, threshold-enforcing check (RegressionThreshold/
// CumulativeRegressionThreshold, limits.go) — see MinMeasurableAllocs'
// doc comment (limits.go) for why allocation counters replaced wall-
// clock as the quantity those thresholds are judged against. The
// PerMonth/wall-clock fields remain, but are now purely ADVISORY: they
// feed only WallClockGrossRegressed, a much wider, catastrophic-only
// check (WallClockGrossRegressionThreshold).
//
// # BUG-473: wall-clock is advisory-ONLY and NEVER gates CI
//
// BUG-272 demoted wall-clock for the PRIMARY threshold but LEFT
// WallClockGrossRegressed contributing to the merge-blocking Regressed
// verdict as a "safety net". BUG-473 found that even the gross (>2x)
// check flaps at perf-SMOKE scale: the per-month wall-clock there is
// sub-millisecond (e.g. baseline 491µs), so ordinary ~2x shared-runner
// jitter (→1.156ms) trips the 100% gross threshold and reddens CI on a
// comment-only doc commit — the exact "never assert wall-clock upper
// bounds in CI" (BUG-031) trap this file's own CompareToBaseline doc
// comment warns against. So WallClockGrossRegressed is now advisory
// ONLY: it is still computed, still reported in Message, and surfaced by
// cmd/perfci as a non-blocking GitHub ::warning::, but it is NOT part of
// the Regressed verdict and can NEVER fail CI. The alloc-based signal
// (runner-independent, BUG-272/BUG-034) is the sole merge gate.
type BaselineComparison struct {
	HasBaseline bool

	// --- PRIMARY signal (BUG-272): deterministic allocation metrics ---

	BaselineAllocBytes uint64
	CurrentAllocBytes  uint64
	BaselineAllocCount uint64
	CurrentAllocCount  uint64

	// AllocBytesDeltaFraction/AllocCountDeltaFraction are (current-
	// baseline)/baseline for the tick-window allocation counters; 0 when
	// !HasBaseline, BelowNoiseFloor, or ScaleMismatch.
	AllocBytesDeltaFraction float64
	AllocCountDeltaFraction float64

	// StepRegressed is the ORIGINAL check, re-based onto allocations by
	// BUG-272: did current regress > RegressionThreshold over baseline
	// (the immediately-prior stored measurement) on EITHER AllocBytes OR
	// AllocCount? Either metric alone crossing the threshold is enough —
	// a regression that grows one without the other (e.g. many small new
	// allocations that barely move total bytes, or fewer-but-huge new
	// allocations that barely move the count) must not slip through by
	// only checking the other. Kept as its own field, separate from the
	// combined Regressed verdict, so a caller (or a test) can tell WHICH
	// check fired — see CumulativeRegressed's doc comment for why a
	// single combined bool is not enough to understand a BUG-083 finding.
	StepRegressed bool

	// AnchorAllocBytes/AnchorAllocCount, CumulativeAllocBytesDeltaFraction/
	// CumulativeAllocCountDeltaFraction, CumulativeChecked, and
	// CumulativeRegressed are BUG-083's second, independent check
	// (CumulativeRegressionThreshold, limits.go), re-based onto
	// allocations by BUG-272: current compared against anchor — a FIXED
	// reference point (the earliest-recorded, or most recently explicitly
	// human-accepted, measurement for this preset; see results.go's
	// LoadLatestBaseline and PerfRecord.AcceptedRegression) — rather than
	// the moving step-to-step baseline. A relative gate anchored to a
	// moving reference point cannot see cumulative drift, by
	// construction: 30 consecutive 9%-over-immediate-prior commits each
	// individually pass the step check while the stored figure compounds
	// 13.27x with zero signal (live-verified, BUG-083). CumulativeChecked
	// is false (and CumulativeRegressed always false) when anchor is nil,
	// scale-mismatched against current, or anchor.AllocCount is below
	// MinMeasurableAllocs — the same defensive "skip rather than mislead"
	// shape as ScaleMismatch/BelowNoiseFloor below, not folded into those
	// two fields because a skipped CUMULATIVE check must never suppress
	// an otherwise-valid STEP verdict (CouldNotEvaluate() intentionally
	// does not consider this field at all — see that method's doc
	// comment).
	AnchorAllocBytes                  uint64
	AnchorAllocCount                  uint64
	CumulativeAllocBytesDeltaFraction float64
	CumulativeAllocCountDeltaFraction float64
	CumulativeChecked                 bool
	CumulativeRegressed               bool

	// BelowNoiseFloor is true when either side's AllocCount is below
	// MinMeasurableAllocs — the PRIMARY signal's noise floor (BUG-272,
	// renamed in role from its pre-BUG-272 wall-clock meaning but kept
	// as the same field name/CouldNotEvaluate() semantics for minimal
	// churn to that method and to cmd/perfci's exit-code plumbing).
	BelowNoiseFloor bool

	// ScaleMismatch (BUG-056) is true when baseline and current do not
	// share the same CitizenCount/Months — see CompareToBaseline's doc
	// comment, point 4, for why this backstop exists even though the
	// checked-in CI config already keeps smoke-scale and real-scale
	// histories in separate files/cache keys operationally.
	ScaleMismatch bool

	// --- ADVISORY signal (BUG-272): wall-clock, demoted ---

	BaselinePerMonth time.Duration
	CurrentPerMonth  time.Duration
	AnchorPerMonth   time.Duration

	// DeltaFraction is the ADVISORY wall-clock (current-baseline)/
	// baseline delta; 0 when !HasBaseline, ScaleMismatch, or
	// WallClockBelowNoiseFloor. Purely informational except insofar as it
	// feeds WallClockGrossRegressed below — it is NOT, on its own, part
	// of the primary pass/fail decision at RegressionThreshold any more
	// (BUG-272).
	DeltaFraction float64

	// WallClockBelowNoiseFloor is true when either side's measured tick
	// window (PerMonthTick x Months, BUG-254's measuredTickWindow) is
	// below MinMeasurableDuration — the wall-clock check is then skipped
	// entirely (WallClockGrossRegressed stays false), which has no
	// bearing on CouldNotEvaluate()/the primary verdict since wall-clock
	// is advisory only.
	WallClockBelowNoiseFloor bool

	// WallClockGrossRegressed is BUG-272's demoted wall-clock check: true
	// only when DeltaFraction exceeds WallClockGrossRegressionThreshold
	// (a catastrophic, e.g. >2x, slowdown) — see that constant's doc
	// comment (limits.go). ADVISORY ONLY (BUG-473): it is computed and
	// carried in Message (and surfaced by cmd/perfci as a non-blocking
	// GitHub ::warning::) so a genuinely catastrophic slowdown allocation
	// counts alone would not catch (a busy-wait, lock contention, …) is
	// still visible, but it is DELIBERATELY NOT part of the Regressed
	// verdict and can NEVER fail CI — at perf-smoke's sub-millisecond
	// per-month scale even this gross threshold flaps on shared-runner
	// jitter (BUG-473), which is exactly the wall-clock-upper-bound-in-CI
	// trap (BUG-031) this gate exists to avoid.
	WallClockGrossRegressed bool

	// Regressed is the overall merge-blocking verdict cmd/perfci gates on:
	// StepRegressed || CumulativeRegressed. Both are the runner-independent
	// allocation-based signal (BUG-272). WallClockGrossRegressed is
	// DELIBERATELY excluded (BUG-473): wall-clock is advisory only and
	// never gates CI.
	Regressed bool

	Message string // human-readable summary, printed verbatim by cmd/perfci
}

// CompareToBaseline implements AC-6's regression check and AC-8's
// missing-baseline handling, hardened against the exact trap this item's
// dispatch brief named explicitly: BUG-031, and re-anchored onto
// deterministic allocation metrics by BUG-272 (see MinMeasurableAllocs'
// doc comment, limits.go, for the live-verified wall-clock CI-noise
// failure that fix closes).
//
// # Why this does not repeat BUG-031
//
// BUG-031 turned main red because a test asserted an ABSOLUTE wall-clock
// ceiling (100ms) that a correct, unregressed build blew past (707ms) on
// a shared runner under -race — the ceiling was never wrong about what
// it wanted to catch, it was measuring the wrong thing: a fixed number
// picked once has no way to know it is running on a slower box today
// than the number was chosen against.
//
// This gate is built differently, in five ways:
//
//  1. RELATIVE, not absolute. It compares this run's allocation counters
//     against a STORED baseline from this package's own results history
//     (results.go's LoadLatestBaseline — the parent commit's own
//     measurement), never a number picked once and frozen in source. A
//     slower/busier runner does not move an allocation count at all, so
//     unlike the old wall-clock comparison this property is now even
//     stronger than "the percentage stays meaningful" — the absolute
//     figures themselves are runner-independent.
//  2. A NOISE FLOOR (MinMeasurableAllocs, limits.go) on the PRIMARY
//     signal. If EITHER side's AllocCount is below this floor, the
//     allocation percentage comparison is skipped entirely
//     (BelowNoiseFloor is set instead of Regressed) — a percentage
//     computed against a near-zero allocation count is dominated by a
//     handful of incidental allocations, not real simulated work. See
//     MinMeasurableAllocs' doc comment for why this essentially never
//     fires against a genuine RunPerf measurement.
//  3. A CITIZENCOUNT/MONTHS CROSS-CHECK (BUG-056), so the smoke-scale-
//     vs-real-scale separation this gate depends on is not ONLY an
//     operational convention (perf-smoke and perf-1m-probe writing to
//     two different files under two different actions/cache key
//     prefixes, .github/workflows/ci.yml). That separation holds in the
//     checked-in CI config as tested, but CompareToBaseline itself used
//     to have zero defence if the paths were ever pointed at the same
//     file by a future config typo, a copy-pasted job, or a manually
//     re-run perfci invocation with the wrong -results flag — a
//     2,000-citizen smoke run and a 1,000,000-citizen real run both
//     carry Preset=="1M" and would silently compare as if they were the
//     same scale. If baseline and current do not share the same
//     CitizenCount and Months, the percentage comparison is skipped
//     (ScaleMismatch is set instead of Regressed), the same defensive
//     shape as the noise-floor check above.
//  4. WORK COUNTERS are now the PRIMARY signal, not a mere cross-check
//     (BUG-272). Every persisted PerfRecord (results.go) has always
//     carried AllocBytes/AllocCount/TotalTicks; this function now gates
//     on the first two directly, at RegressionThreshold, instead of only
//     letting a human cross-check them by hand after a wall-clock flag —
//     "measure work, not just time, wherever possible" from this item's
//     original brief, taken to its conclusion once wall-clock proved too
//     noisy to gate on at this engine speed.
//  5. WALL-CLOCK survives as a DEMOTED, ADVISORY, gross-only check
//     (WallClockGrossRegressionThreshold, limits.go) that NEVER gates CI
//     (BUG-473) — see BaselineComparison's doc comment for what it still
//     surfaces as a non-blocking warning and why leaving it in the
//     merge-blocking verdict (as BUG-272 originally did) still flapped at
//     perf-smoke's sub-millisecond scale.
//
// # BUG-083: a second, independent check against a fixed anchor
//
// anchor is a SEPARATE reference point from baseline: baseline is the
// step-to-step "last known good" (results.go's LoadLatestBaseline
// replays history forward, freezing it at the last non-regressed
// record rather than the last-appended one); anchor is a FIXED point
// (the earliest-recorded, or most recently explicitly human-accepted,
// measurement for this preset) that never moves on an ordinary passing
// commit. Both checks run independently; Regressed is true if either
// allocation-based check fires (StepRegressed || CumulativeRegressed —
// wall-clock is advisory only and never gates, BUG-473) — see
// CumulativeRegressionThreshold's doc
// comment (limits.go) for the full live-verified rationale: a relative
// gate compared only against a moving reference point cannot see
// sustained drift, by construction, no matter how conservatively that
// moving point is advanced. anchor may be nil (no anchor recorded yet,
// e.g. the very first comparison) — the cumulative check is then simply
// skipped (CumulativeChecked stays false), never treated as a failure of
// its own.
//
// measuredTickWindow is the total measured tick span r's PerMonthTick
// was derived from — PerMonthTick x Months — the quantity the ADVISORY
// wall-clock check's own noise floor (MinMeasurableDuration, limits.go)
// is compared against since BUG-254 (see that constant's doc comment for
// why the floor attaches to the window, not the per-month figure).
//
// Deliberately reconstructed from PerMonthTick and Months rather than
// read off r.TickTime, even though a genuine RunPerf record carries all
// three consistently: PerMonthTick and Months are the two fields the
// gate already validates (ScaleMismatch equality, ImplausibleReason's
// Months/PerMonthTick bounds), while TickTime is otherwise-unvalidated —
// a hand-injected record (the BUG-073/085/095 second-writer family)
// could carry a huge TickTime alongside a tiny PerMonthTick to unlock a
// comparison the floor should refuse. A floor derived only from the
// validated fields leaves that route closed without adding yet another
// cross-field consistency check.
func measuredTickWindow(r PerfResult) time.Duration {
	return r.PerMonthTick * time.Duration(r.Months)
}

// allocDeltaFraction returns (current-baseline)/baseline as a signed
// fraction, guarding two hazards a bare `float64(int64(current) -
// int64(baseline)) / float64(baseline)` has (BUG-305):
//
//  1. current/baseline are uint64 (PerfResult.AllocBytes/AllocCount);
//     int64(x) for x > math.MaxInt64 is well-defined Go truncation but
//     numerically wrong -- it turns into a large NEGATIVE int64, poisoning
//     the delta. num.ClampInt64FromUint64 saturates at MaxInt64 instead,
//     which for any REAL allocation count (nowhere near 2^63) is a no-op;
//     it only changes behaviour for an already-implausible, corrupted, or
//     hostile record.
//  2. baseline == 0 makes the division 0/0 == NaN whenever current is also
//     0 (both sides measured zero of this metric), which would otherwise
//     propagate a NaN into DeltaFraction/StepRegressed/CumulativeRegressed
//     and every %.1f%% message built from it. baseline == 0 with current >
//     0 is a real, unbounded proportional increase (growth from nothing),
//     reported as +Inf rather than NaN so it still compares > any finite
//     RegressionThreshold and reads sensibly in a formatted message,
//     instead of NaN's silently-false comparisons letting a growth-from-
//     zero regression slip past every threshold check undetected.
func allocDeltaFraction(current, baseline uint64) float64 {
	if baseline == 0 {
		if current == 0 {
			return 0
		}
		return math.Inf(1)
	}
	c := num.ClampInt64FromUint64(current)
	b := num.ClampInt64FromUint64(baseline)
	return float64(c-b) / float64(b)
}

func CompareToBaseline(baseline, anchor *PerfResult, current PerfResult) BaselineComparison {
	if baseline == nil {
		return BaselineComparison{
			HasBaseline:       false,
			CurrentPerMonth:   current.PerMonthTick,
			CurrentAllocBytes: current.AllocBytes,
			CurrentAllocCount: current.AllocCount,
			Message:           "no prior baseline to compare — recording this run as the new baseline",
		}
	}

	if baseline.CitizenCount != current.CitizenCount || baseline.Months != current.Months {
		return BaselineComparison{
			HasBaseline:        true,
			BaselinePerMonth:   baseline.PerMonthTick,
			CurrentPerMonth:    current.PerMonthTick,
			BaselineAllocBytes: baseline.AllocBytes,
			CurrentAllocBytes:  current.AllocBytes,
			BaselineAllocCount: baseline.AllocCount,
			CurrentAllocCount:  current.AllocCount,
			ScaleMismatch:      true,
			Message: fmt.Sprintf(
				"baseline and current do not share the same scale (baseline citizens=%d months=%d, current citizens=%d months=%d) — skipping the percentage regression check rather than comparing two different scales as if they were the same run",
				baseline.CitizenCount, baseline.Months, current.CitizenCount, current.Months,
			),
		}
	}

	cmp := BaselineComparison{
		HasBaseline:        true,
		BaselinePerMonth:   baseline.PerMonthTick,
		CurrentPerMonth:    current.PerMonthTick,
		BaselineAllocBytes: baseline.AllocBytes,
		CurrentAllocBytes:  current.AllocBytes,
		BaselineAllocCount: baseline.AllocCount,
		CurrentAllocCount:  current.AllocCount,
	}

	// --- ADVISORY wall-clock check (BUG-272: demoted from primary) ---
	// Computed regardless of whether the primary allocation check below
	// can itself be evaluated — it is informational except for the
	// catastrophic-only WallClockGrossRegressed safety net, which stays
	// meaningful even when allocation counts happen to be too small to
	// judge on their own.
	if measuredTickWindow(*baseline) >= MinMeasurableDuration && measuredTickWindow(current) >= MinMeasurableDuration {
		wallDelta := float64(current.PerMonthTick-baseline.PerMonthTick) / float64(baseline.PerMonthTick)
		cmp.DeltaFraction = wallDelta
		cmp.WallClockGrossRegressed = wallDelta > WallClockGrossRegressionThreshold
	} else {
		cmp.WallClockBelowNoiseFloor = true
	}

	// --- PRIMARY allocation-based check (BUG-272) ---
	if baseline.AllocCount < MinMeasurableAllocs || current.AllocCount < MinMeasurableAllocs {
		cmp.BelowNoiseFloor = true
		// BUG-473: wall-clock is advisory only and never gates CI, so it
		// cannot turn a could-not-evaluate (below-noise-floor) primary
		// signal into a Regressed verdict either — Regressed stays false.
		cmp.Regressed = false
		var wallNote string
		if cmp.WallClockBelowNoiseFloor {
			wallNote = "; advisory wall-clock check also skipped (below its own noise floor)"
		} else if cmp.WallClockGrossRegressed {
			wallNote = fmt.Sprintf("; ADVISORY WARNING: wall-clock fired a GROSS regression (baseline=%s current=%s delta=%.1f%%, gross threshold %.0f%%) — advisory ONLY, does NOT fail the gate (BUG-473); the primary allocation signal could not be judged either",
				baseline.PerMonthTick, current.PerMonthTick, cmp.DeltaFraction*100, WallClockGrossRegressionThreshold*100)
		} else {
			wallNote = fmt.Sprintf("; advisory wall-clock delta=%.1f%% (informational, gross threshold %.0f%%)", cmp.DeltaFraction*100, WallClockGrossRegressionThreshold*100)
		}
		cmp.Message = fmt.Sprintf(
			"both baseline and current AllocCount must be >= %d to gate on the primary allocation-based regression signal (BUG-272); baseline AllocCount=%d current AllocCount=%d is below the noise floor, skipping the allocation regression check%s",
			MinMeasurableAllocs, baseline.AllocCount, current.AllocCount, wallNote,
		)
		return cmp
	}

	allocBytesDelta := allocDeltaFraction(current.AllocBytes, baseline.AllocBytes)
	allocCountDelta := allocDeltaFraction(current.AllocCount, baseline.AllocCount)
	cmp.AllocBytesDeltaFraction = allocBytesDelta
	cmp.AllocCountDeltaFraction = allocCountDelta
	cmp.StepRegressed = allocBytesDelta > RegressionThreshold || allocCountDelta > RegressionThreshold

	// BUG-083's cumulative check only applies when anchor is a usable,
	// same-scale, above-noise-floor reference — the identical defensive
	// posture as the ScaleMismatch/BelowNoiseFloor checks above, applied
	// to the second reference point rather than skipping the whole
	// function. An anchor that fails this is simply not consulted; it
	// never turns into a false ScaleMismatch/BelowNoiseFloor verdict for
	// the (already-validated) step check above.
	if anchor != nil && anchor.CitizenCount == current.CitizenCount && anchor.Months == current.Months && anchor.AllocCount >= MinMeasurableAllocs {
		cumAllocBytesDelta := allocDeltaFraction(current.AllocBytes, anchor.AllocBytes)
		cumAllocCountDelta := allocDeltaFraction(current.AllocCount, anchor.AllocCount)
		cmp.AnchorPerMonth = anchor.PerMonthTick
		cmp.AnchorAllocBytes = anchor.AllocBytes
		cmp.AnchorAllocCount = anchor.AllocCount
		cmp.CumulativeAllocBytesDeltaFraction = cumAllocBytesDelta
		cmp.CumulativeAllocCountDeltaFraction = cumAllocCountDelta
		cmp.CumulativeChecked = true
		cmp.CumulativeRegressed = cumAllocBytesDelta > CumulativeRegressionThreshold || cumAllocCountDelta > CumulativeRegressionThreshold
	}

	// BUG-473: only the runner-independent allocation-based checks gate the
	// merge verdict. WallClockGrossRegressed is advisory only (carried in
	// Message below, surfaced by cmd/perfci as a non-blocking warning) and
	// is DELIBERATELY excluded here so wall-clock noise can never fail CI.
	cmp.Regressed = cmp.StepRegressed || cmp.CumulativeRegressed

	var parts []string
	if cmp.StepRegressed {
		parts = append(parts, fmt.Sprintf(
			"ALLOC REGRESSED (step): baseline bytes=%d count=%d current bytes=%d count=%d bytes delta=%.1f%% count delta=%.1f%% (threshold %.0f%%)",
			baseline.AllocBytes, baseline.AllocCount, current.AllocBytes, current.AllocCount,
			allocBytesDelta*100, allocCountDelta*100, RegressionThreshold*100,
		))
	}
	if cmp.CumulativeRegressed {
		parts = append(parts, fmt.Sprintf(
			"ALLOC REGRESSED (cumulative drift, BUG-083/BUG-272): anchor bytes=%d count=%d current bytes=%d count=%d cumulative bytes delta=%.1f%% count delta=%.1f%% (threshold %.0f%%)",
			anchor.AllocBytes, anchor.AllocCount, current.AllocBytes, current.AllocCount,
			cmp.CumulativeAllocBytesDeltaFraction*100, cmp.CumulativeAllocCountDeltaFraction*100, CumulativeRegressionThreshold*100,
		))
	}

	// The advisory wall-clock note is appended to whichever message shape
	// is built below, never folded into the "REGRESSED:" parts — a
	// wall-clock gross regression is a WARNING, not a failure (BUG-473).
	var wallNote string
	if cmp.WallClockBelowNoiseFloor {
		wallNote = " (advisory wall-clock check skipped: below noise floor)"
	} else if cmp.WallClockGrossRegressed {
		wallNote = fmt.Sprintf("; ADVISORY WARNING: wall-clock GROSS regression baseline=%s current=%s delta=%.1f%% (gross threshold %.0f%%) — advisory ONLY, does NOT fail the gate (BUG-473)",
			baseline.PerMonthTick, current.PerMonthTick, cmp.DeltaFraction*100, WallClockGrossRegressionThreshold*100)
	} else {
		wallNote = fmt.Sprintf("; advisory wall-clock delta=%.1f%% (informational, gross threshold %.0f%%)", cmp.DeltaFraction*100, WallClockGrossRegressionThreshold*100)
	}

	if len(parts) > 0 {
		cmp.Message = "REGRESSED: " + strings.Join(parts, "; ") + wallNote
	} else {
		msg := fmt.Sprintf("alloc bytes delta=%.1f%% count delta=%.1f%% (threshold %.0f%%)", allocBytesDelta*100, allocCountDelta*100, RegressionThreshold*100)
		if cmp.CumulativeChecked {
			msg += fmt.Sprintf("; cumulative alloc bytes delta=%.1f%% count delta=%.1f%% (threshold %.0f%%)", cmp.CumulativeAllocBytesDeltaFraction*100, cmp.CumulativeAllocCountDeltaFraction*100, CumulativeRegressionThreshold*100)
		}
		msg += wallNote
		cmp.Message = msg
	}

	return cmp
}

// RegressionError builds the registry-sourced error cmd/perfci reports
// and exits non-zero on (AC-10) when cmp.Regressed is true. Exported so
// cmd/perfci (a separate `main` package) never has to duplicate this
// package's unexported error-code vocabulary.
func RegressionError(correlationID string, cmp BaselineComparison) error {
	return errs.New(codeRegressionDetected, correlationID, map[string]any{
		"allocBytesDeltaFraction":           cmp.AllocBytesDeltaFraction,
		"allocCountDeltaFraction":           cmp.AllocCountDeltaFraction,
		"threshold":                         RegressionThreshold,
		"stepRegressed":                     cmp.StepRegressed,
		"cumulativeChecked":                 cmp.CumulativeChecked,
		"cumulativeAllocBytesDeltaFraction": cmp.CumulativeAllocBytesDeltaFraction,
		"cumulativeAllocCountDeltaFraction": cmp.CumulativeAllocCountDeltaFraction,
		"cumulativeThreshold":               CumulativeRegressionThreshold,
		"cumulativeRegressed":               cmp.CumulativeRegressed,
		"wallClockDeltaFraction":            cmp.DeltaFraction,
		"wallClockGrossThreshold":           WallClockGrossRegressionThreshold,
		"wallClockGrossRegressed":           cmp.WallClockGrossRegressed,
		"message":                           cmp.Message,
	})
}

// CouldNotEvaluate is BUG-071's fix: the gate has THREE outcomes, not
// two — passed, failed, and could-not-evaluate — and this method is the
// single place that decides which comparisons fall in the third bucket,
// so cmd/perfci (and anything else that ever consumes a
// BaselineComparison) cannot reimplement this test slightly differently
// and drift.
//
// True exactly when a baseline EXISTED to compare against but the
// percentage check was skipped anyway (ScaleMismatch or
// BelowNoiseFloor) — i.e. CompareToBaseline tried to judge this run and
// could not. Deliberately false when !HasBaseline: a missing baseline is
// AC-8's ordinary "first run on a fresh preset/cache" case, a genuine,
// expected outcome with its own well-defined behaviour (record this run
// as the new baseline, exit 0) — not a failed attempt at a comparison.
// Conflating the two would misfile the common, harmless "no history yet"
// case as an alarming "could not evaluate", which is its own way of
// making a real signal impossible to trust.
func (cmp BaselineComparison) CouldNotEvaluate() bool {
	return cmp.HasBaseline && (cmp.ScaleMismatch || cmp.BelowNoiseFloor)
}

// CouldNotEvaluateWarning builds the registry-sourced, non-fatal signal
// cmd/perfci reports on stderr (BUG-071) when cmp.CouldNotEvaluate() is
// true — the loud, human-readable half of making "the gate could not do
// its job" impossible to mistake for "the gate is satisfied". Mirrors
// RegressionError's shape (registry-sourced, exported so cmd/perfci does
// not duplicate this package's error vocabulary) but is deliberately a
// distinct code/severity from codeRegressionDetected: this is not a
// build failure being reported, it is an honest "no verdict was
// reached" — printed, never silently folded into either a pass or a
// fail.
func CouldNotEvaluateWarning(correlationID string, cmp BaselineComparison) error {
	return errs.New(codeGateCouldNotEvaluate, correlationID, map[string]any{
		"scaleMismatch":   cmp.ScaleMismatch,
		"belowNoiseFloor": cmp.BelowNoiseFloor,
		"message":         cmp.Message,
	})
}
