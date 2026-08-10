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
	DeltaFraction    float64 // (current-baseline)/baseline; 0 when !HasBaseline or BelowNoiseFloor
	Regressed        bool
	BelowNoiseFloor  bool
	Message          string // human-readable summary, printed verbatim by cmd/perfci
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
// This gate is built differently, in three ways:
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
//  3. WORK COUNTERS travel alongside the wall-clock number. This
//     function does not gate on PerfResult's AllocBytes/AllocCount/
//     TotalTicks, but every persisted PerfRecord (results.go) carries
//     them, so a human investigating a flagged regression has an
//     allocation/operation count to cross-check the wall-clock claim
//     against before trusting it — "measure work, not just time,
//     wherever possible" from this item's brief.
func CompareToBaseline(baseline *PerfResult, current PerfResult) BaselineComparison {
	if baseline == nil {
		return BaselineComparison{
			HasBaseline:     false,
			CurrentPerMonth: current.PerMonthTick,
			Message:         "no prior baseline to compare — recording this run as the new baseline",
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
	regressed := delta > RegressionThreshold
	return BaselineComparison{
		HasBaseline:      true,
		BaselinePerMonth: baseline.PerMonthTick,
		CurrentPerMonth:  current.PerMonthTick,
		DeltaFraction:    delta,
		Regressed:        regressed,
		Message: fmt.Sprintf(
			"baseline=%s current=%s delta=%.1f%% (threshold %.0f%%)",
			baseline.PerMonthTick, current.PerMonthTick, delta*100, RegressionThreshold*100,
		),
	}
}

// RegressionError builds the registry-sourced error cmd/perfci reports
// and exits non-zero on (AC-10) when cmp.Regressed is true. Exported so
// cmd/perfci (a separate `main` package) never has to duplicate this
// package's unexported error-code vocabulary.
func RegressionError(correlationID string, cmp BaselineComparison) error {
	return errs.New(codeRegressionDetected, correlationID, map[string]any{
		"deltaFraction": cmp.DeltaFraction,
		"threshold":     RegressionThreshold,
		"message":       cmp.Message,
	})
}
