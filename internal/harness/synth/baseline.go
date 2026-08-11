package synth

import (
	"fmt"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BaselineComparison is CompareToBaseline's verdict (AC-6, AC-8, AC-10).
type BaselineComparison struct {
	HasBaseline      bool
	BaselinePerMonth time.Duration
	CurrentPerMonth  time.Duration
	DeltaFraction    float64 // (current-baseline)/baseline; 0 when !HasBaseline, BelowNoiseFloor, or ScaleMismatch
	Regressed        bool    // StepRegressed || CumulativeRegressed — the overall verdict cmd/perfci gates on
	BelowNoiseFloor  bool
	// ScaleMismatch (BUG-056) is true when baseline and current do not
	// share the same CitizenCount/Months — see CompareToBaseline's doc
	// comment, point 4, for why this backstop exists even though the
	// checked-in CI config already keeps smoke-scale and real-scale
	// histories in separate files/cache keys operationally.
	ScaleMismatch bool
	Message       string // human-readable summary, printed verbatim by cmd/perfci

	// StepRegressed is the ORIGINAL check: did current regress >
	// RegressionThreshold over baseline (the immediately-prior stored
	// measurement)? Kept as its own field, separate from the combined
	// Regressed verdict, so a caller (or a test) can tell WHICH check
	// fired — see CumulativeRegressed's doc comment for why a single
	// combined bool is not enough to understand a BUG-083 finding.
	StepRegressed bool

	// AnchorPerMonth, CumulativeDeltaFraction, CumulativeChecked, and
	// CumulativeRegressed are BUG-083's second, independent check
	// (CumulativeRegressionThreshold, limits.go): current compared
	// against anchor — a FIXED reference point (the earliest-recorded,
	// or most recently explicitly human-accepted, measurement for this
	// preset; see results.go's LoadLatestBaseline and
	// PerfRecord.AcceptedRegression) — rather than the moving
	// step-to-step baseline. A relative gate anchored to a moving
	// reference point cannot see cumulative drift, by construction: 30
	// consecutive 9%-over-immediate-prior commits each individually
	// pass the step check while the stored figure compounds 13.27x with
	// zero signal (live-verified, BUG-083). CumulativeChecked is false
	// (and CumulativeRegressed always false) when anchor is nil, scale-
	// mismatched against current, or itself below MinMeasurableDuration
	// — the same defensive "skip rather than mislead" shape as
	// ScaleMismatch/BelowNoiseFloor above, not folded into those two
	// fields because a skipped CUMULATIVE check must never suppress a
	// otherwise-valid STEP verdict (CouldNotEvaluate() intentionally
	// does not consider this field at all — see that method's doc
	// comment).
	AnchorPerMonth          time.Duration
	CumulativeDeltaFraction float64
	CumulativeChecked       bool
	CumulativeRegressed     bool
}

// CompareToBaseline implements AC-6's regression check and AC-8's
// missing-baseline handling, hardened against the exact trap this item's
// dispatch brief named explicitly: BUG-031.
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
// This gate is built differently, in four ways:
//
//  1. RELATIVE, not absolute. It compares this run's PerMonthTick
//     against a STORED baseline from this package's own results history
//     (results.go's LoadLatestBaseline — the parent commit's own
//     measurement), never a number picked once and frozen in source. A
//     slower runner shifts both sides of the ratio together, so the
//     percentage stays meaningful even as absolute times drift.
//  2. A NOISE FLOOR (MinMeasurableDuration, limits.go). If EITHER the
//     baseline or the current measurement is below this floor, the
//     percentage comparison is skipped entirely (BelowNoiseFloor is set
//     instead of Regressed) — a percentage computed against a near-zero
//     absolute duration is dominated by GC/scheduler jitter, not real
//     work. This is not a hypothetical: engine.core is a walking
//     skeleton with ZERO registered phase hooks as of this sprint (see
//     doc.go's "Status"), so a near-zero monthly-tick time is the COMMON
//     case today, not a rare edge — without this floor, this gate would
//     be exactly one BUG-031 waiting to happen the first time it ran on
//     a busier-than-usual CI box.
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
//  4. WORK COUNTERS travel alongside the wall-clock number. This
//     function does not gate on PerfResult's AllocBytes/AllocCount/
//     TotalTicks, but every persisted PerfRecord (results.go) carries
//     them, so a human investigating a flagged regression has an
//     allocation/operation count to cross-check the wall-clock claim
//     against before trusting it — "measure work, not just time,
//     wherever possible" from this item's brief.
//
// # BUG-083: a second, independent check against a fixed anchor
//
// anchor is a SEPARATE reference point from baseline: baseline is the
// step-to-step "last known good" (results.go's LoadLatestBaseline
// replays history forward, freezing it at the last non-regressed
// record rather than the last-appended one); anchor is a FIXED point
// (the earliest-recorded, or most recently explicitly human-accepted,
// measurement for this preset) that never moves on an ordinary passing
// commit. Both checks run independently; Regressed is true if EITHER
// fires (StepRegressed || CumulativeRegressed) — see
// CumulativeRegressionThreshold's doc comment (limits.go) for the full
// live-verified rationale: a relative gate compared only against a
// moving reference point cannot see sustained drift, by construction,
// no matter how conservatively that moving point is advanced. anchor
// may be nil (no anchor recorded yet, e.g. the very first comparison)
// — the cumulative check is then simply skipped (CumulativeChecked
// stays false), never treated as a failure of its own.
func CompareToBaseline(baseline, anchor *PerfResult, current PerfResult) BaselineComparison {
	if baseline == nil {
		return BaselineComparison{
			HasBaseline:     false,
			CurrentPerMonth: current.PerMonthTick,
			Message:         "no prior baseline to compare — recording this run as the new baseline",
		}
	}

	if baseline.CitizenCount != current.CitizenCount || baseline.Months != current.Months {
		return BaselineComparison{
			HasBaseline:      true,
			BaselinePerMonth: baseline.PerMonthTick,
			CurrentPerMonth:  current.PerMonthTick,
			ScaleMismatch:    true,
			Message: fmt.Sprintf(
				"baseline and current do not share the same scale (baseline citizens=%d months=%d, current citizens=%d months=%d) — skipping the percentage regression check rather than comparing two different scales as if they were the same run",
				baseline.CitizenCount, baseline.Months, current.CitizenCount, current.Months,
			),
		}
	}

	if baseline.PerMonthTick < MinMeasurableDuration || current.PerMonthTick < MinMeasurableDuration {
		return BaselineComparison{
			HasBaseline:      true,
			BaselinePerMonth: baseline.PerMonthTick,
			CurrentPerMonth:  current.PerMonthTick,
			BelowNoiseFloor:  true,
			Message: fmt.Sprintf(
				"both measurements must be >= %s to gate on a percentage regression; baseline=%s current=%s is below the noise floor, skipping the check",
				MinMeasurableDuration, baseline.PerMonthTick, current.PerMonthTick,
			),
		}
	}

	delta := float64(current.PerMonthTick-baseline.PerMonthTick) / float64(baseline.PerMonthTick)
	stepRegressed := delta > RegressionThreshold

	cmp := BaselineComparison{
		HasBaseline:      true,
		BaselinePerMonth: baseline.PerMonthTick,
		CurrentPerMonth:  current.PerMonthTick,
		DeltaFraction:    delta,
		StepRegressed:    stepRegressed,
	}

	// BUG-083's cumulative check only applies when anchor is a usable,
	// same-scale, above-noise-floor reference — the identical defensive
	// posture as the ScaleMismatch/BelowNoiseFloor checks above, applied
	// to the second reference point rather than skipping the whole
	// function. An anchor that fails this is simply not consulted; it
	// never turns into a false ScaleMismatch/BelowNoiseFloor verdict for
	// the (already-validated) step check above.
	if anchor != nil && anchor.CitizenCount == current.CitizenCount && anchor.Months == current.Months && anchor.PerMonthTick >= MinMeasurableDuration {
		cumulativeDelta := float64(current.PerMonthTick-anchor.PerMonthTick) / float64(anchor.PerMonthTick)
		cmp.AnchorPerMonth = anchor.PerMonthTick
		cmp.CumulativeDeltaFraction = cumulativeDelta
		cmp.CumulativeChecked = true
		cmp.CumulativeRegressed = cumulativeDelta > CumulativeRegressionThreshold
	}

	cmp.Regressed = cmp.StepRegressed || cmp.CumulativeRegressed

	switch {
	case cmp.StepRegressed && cmp.CumulativeRegressed:
		cmp.Message = fmt.Sprintf(
			"REGRESSED (step AND cumulative): baseline=%s current=%s step delta=%.1f%% (threshold %.0f%%); anchor=%s cumulative delta=%.1f%% (threshold %.0f%%)",
			baseline.PerMonthTick, current.PerMonthTick, delta*100, RegressionThreshold*100,
			anchor.PerMonthTick, cmp.CumulativeDeltaFraction*100, CumulativeRegressionThreshold*100,
		)
	case cmp.StepRegressed:
		cmp.Message = fmt.Sprintf(
			"REGRESSED (step): baseline=%s current=%s delta=%.1f%% (threshold %.0f%%)",
			baseline.PerMonthTick, current.PerMonthTick, delta*100, RegressionThreshold*100,
		)
	case cmp.CumulativeRegressed:
		cmp.Message = fmt.Sprintf(
			"REGRESSED (cumulative drift, BUG-083): anchor=%s current=%s cumulative delta=%.1f%% (threshold %.0f%%) — each individual step stayed under the %.0f%% step threshold (baseline=%s, step delta=%.1f%%), but sustained drift from the original/accepted baseline exceeded the cumulative threshold",
			anchor.PerMonthTick, current.PerMonthTick, cmp.CumulativeDeltaFraction*100, CumulativeRegressionThreshold*100,
			RegressionThreshold*100, baseline.PerMonthTick, delta*100,
		)
	case cmp.CumulativeChecked:
		cmp.Message = fmt.Sprintf(
			"baseline=%s current=%s delta=%.1f%% (threshold %.0f%%); anchor=%s cumulative delta=%.1f%% (threshold %.0f%%)",
			baseline.PerMonthTick, current.PerMonthTick, delta*100, RegressionThreshold*100,
			anchor.PerMonthTick, cmp.CumulativeDeltaFraction*100, CumulativeRegressionThreshold*100,
		)
	default:
		cmp.Message = fmt.Sprintf(
			"baseline=%s current=%s delta=%.1f%% (threshold %.0f%%)",
			baseline.PerMonthTick, current.PerMonthTick, delta*100, RegressionThreshold*100,
		)
	}

	return cmp
}

// RegressionError builds the registry-sourced error cmd/perfci reports
// and exits non-zero on (AC-10) when cmp.Regressed is true. Exported so
// cmd/perfci (a separate `main` package) never has to duplicate this
// package's unexported error-code vocabulary.
func RegressionError(correlationID string, cmp BaselineComparison) error {
	return errs.New(codeRegressionDetected, correlationID, map[string]any{
		"deltaFraction":           cmp.DeltaFraction,
		"threshold":               RegressionThreshold,
		"stepRegressed":           cmp.StepRegressed,
		"cumulativeChecked":       cmp.CumulativeChecked,
		"cumulativeDeltaFraction": cmp.CumulativeDeltaFraction,
		"cumulativeThreshold":     CumulativeRegressionThreshold,
		"cumulativeRegressed":     cmp.CumulativeRegressed,
		"message":                 cmp.Message,
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
