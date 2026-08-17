package social

import (
	"fmt"
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// slowFuseThresholdYears is A5's own spec-stated Slow-Fuse rule ("any
// decision whose principal effects land more than 5 game-years out") — the
// same structural rule engine.projections quotes, duplicated here as this
// module's local pre-submission threshold (GR#3's single-source caveat: the
// value is the design rule itself, not tunable balance data).
const slowFuseThresholdYears = 5.0

// caseloadCurveKey is the projections curve key this module registers its
// open-caseload provider under (AC-10).
const caseloadCurveKey = "social.caseload"

// ProjectedConsequence is the social-local projected-consequence payload a
// funding-cut command carries (AC-10/A5): the human-readable consequence
// prose plus a rendered series of projected values. A Slow-Fuse funding cut
// (FuseYears above the A5 threshold) must carry a non-empty rendered Series.
type ProjectedConsequence struct {
	Description string
	Series      []float64
}

// FundingCommand is the funding-level mutation command (AC-1): it sets a
// category's funding level through engine.services and — when the command is
// a CUT — submits the decision through engine.projections' Slow-Fuse gate
// (AC-10). FuseYears is A5's tag; a funding cut whose effect lands more than
// five game-years out must carry a non-empty Projection.
type FundingCommand struct {
	Category   Category
	Level      float64
	Month      int64   // the sim month the decision is made at
	FuseYears  float64 // A5's Slow-Fuse tag (AC-10)
	Projection ProjectedConsequence
}

// SetFunding applies a funding decision (AC-1/AC-4/AC-10):
//  1. rejects an unregistered category (AC-13) and an out-of-domain level;
//  2. when the command is a CUT (level below current), runs this module's own
//     Slow-Fuse pre-submission check — a cut whose FuseYears exceeds the A5
//     threshold without a local projected-consequence series is rejected here,
//     before it reaches engine.projections (AC-10);
//  3. applies the funding through engine.services.SetFunding;
//  4. for a cut, submits the decision through engine.projections' generic
//     Slow-Fuse gate (EnqueueDecision) carrying a real projected-consequence
//     payload.
//
// No social lock is held across the services/projections seams (recurring
// checklist #1): the dependencies are snapshotted under RLock and called
// outside it. The only conserved mutation is the single atomic
// services.SetFunding write, applied only after every validation passes, so
// no failure order leaves partial state — the projection is advisory (a
// decision marker), and its submission error is surfaced rather than swallowed.
func (a *SocialAPI) SetFunding(cmd FundingCommand) error {
	if err := a.checkNotCopied("SetFunding"); err != nil {
		return err
	}
	if !cmd.Category.Valid() {
		return errs.New(ErrUnknownCategory, a.correlationID, map[string]any{"category": cmd.Category.String()})
	}
	if !numFinite(cmd.Level) || cmd.Level < 0 || cmd.Level > 1 {
		return errs.New(ErrInvalidFunding, a.correlationID, map[string]any{
			"category": cmd.Category.String(), "level": cmd.Level,
		})
	}

	a.mu.RLock()
	servicesAPI := a.services
	projectionsAPI := a.projections
	a.mu.RUnlock()
	if servicesAPI == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "services", "operation": "SetFunding",
		})
	}
	id := services.ServiceID(categoryServiceID(cmd.Category))
	current, err := servicesAPI.FundingLevel(id)
	if err != nil {
		return err
	}
	isCut := cmd.Level < current

	var completionMonth int64
	var projectedStep float64
	if isCut {
		// Validate the FuseYears tag at the write boundary BEFORE the threshold
		// comparison: a degenerate tag (NaN/±Inf/<=0) must never reach `>`,
		// where NaN > threshold is false and would read as "under threshold".
		if !numFinite(cmd.FuseYears) || cmd.FuseYears <= 0 {
			return errs.New(ErrInvalidFuseYears, a.correlationID, map[string]any{
				"category": cmd.Category.String(), "fuseYears": cmd.FuseYears,
			})
		}
		// Upper-bound FuseYears so the completion month is representable
		// (SEC-179): the months conversion must not saturate int64 — a huge
		// finite FuseYears would clamp to MaxInt64 and the month sum would wrap
		// negative, landing the step at the wrong (near-epoch) month.
		fuseMonths := cmd.FuseYears * 12
		if !numFinite(fuseMonths) || fuseMonths >= float64(math.MaxInt64) {
			return errs.New(ErrInvalidFuseYears, a.correlationID, map[string]any{
				"category": cmd.Category.String(), "fuseYears": cmd.FuseYears,
			})
		}
		// SatAdd, never a bare `+`: even with the bound above, a near-MaxInt64
		// Month plus a large fuse saturates at the far-future extreme instead
		// of wrapping (GR#16).
		completionMonth = num.SatAdd(cmd.Month, num.ClampInt64FromFloat(fuseMonths))
		// Bound the series LENGTH before any O(n) pass over it (SEC-202): a
		// series over maxProjectionSeriesPoints would otherwise drive three
		// linear scans plus a len(Series)-sized allocation, so the length is
		// rejected at the write boundary — before seriesFinite, projectedDelta,
		// toProjectedConsequence, or projections' own empty() ever run.
		if err := checkSeriesLength(cmd.Projection.Series, a.correlationID); err != nil {
			return err
		}
		if !seriesFinite(cmd.Projection.Series) {
			return errs.New(ErrInvalidSeries, a.correlationID, map[string]any{
				"category": cmd.Category.String(), "series": cmd.Projection.Series,
			})
		}
		// The derived delta must be finite-guarded too (SEC-200): seriesFinite
		// only proves each series value is finite, but finite-minus-finite can
		// overflow — [-MaxFloat64, +MaxFloat64] yields +Inf, which would poison
		// the projection curve. Guard the derived value at the same write
		// boundary (weakness pattern #3: the guard must close the derived-value
		// overflow, not just the NaN/Inf series-value instance).
		var ok bool
		projectedStep, ok = num.GuardFinite(projectedDelta(cmd))
		if !ok {
			return errs.New(ErrInvalidSeries, a.correlationID, map[string]any{
				"category": cmd.Category.String(), "series": cmd.Projection.Series,
			})
		}
		if cmd.FuseYears > slowFuseThresholdYears && len(cmd.Projection.Series) == 0 {
			return errs.New(ErrSlowFusePayloadMissing, a.correlationID, map[string]any{
				"category": cmd.Category.String(), "fuseYears": cmd.FuseYears,
			})
		}
	}

	// Submit the projection decision BEFORE mutating funding (SEC-183): the
	// projection Slow-Fuse gate is the backstop that can reject a cut social's
	// local check passed, so it must run first — if it rejects, funding is left
	// untouched and the caller sees the error with no partial state. (The old
	// order mutated funding first, so a gate rejection left the cut funded with
	// no recorded projection.) The reverse residual — a projection recorded but
	// the subsequent funding write failing — is surfaced to the caller as the
	// SetFunding error and is acceptable: the projection is an advisory marker,
	// and the funding write is the authoritative conserved mutation.
	if isCut && projectionsAPI != nil {
		if err := projectionsAPI.EnqueueDecision(projections.Decision{
			ID:              fmt.Sprintf("social.funding.%s.%d", cmd.Category.String(), cmd.Month),
			Type:            "social.funding",
			CurveKey:        caseloadCurveKey,
			CompletionMonth: completionMonth,
			Delta:           projectedStep,
			FuseYears:       cmd.FuseYears,
			Consequence:     toProjectedConsequence(cmd),
		}); err != nil {
			return err
		}
	}

	if err := servicesAPI.SetFunding(id, cmd.Level); err != nil {
		return err
	}
	return nil
}

// maxProjectionSeriesPoints bounds the number of points a funding cut's
// projected-consequence Series may carry (SEC-202). It is a resource ceiling
// — the same shape as maxCaseloadProposalsPerMonth (SEC-195) and
// engine.projections' maxCurveQueryMonths — NOT a balance number: the
// consequence magnitude is whatever the caller renders, and this ceiling
// exists only so a hostile or malformed caller (or a tick bug producing a
// runaway series) cannot turn one SetFunding call into a large transient
// allocation plus multiple O(n) scans. A slow-fuse consequence is a human-
// renderable chart snippet for a decision whose effects land a few game-years
// out (a 5-year fuse is 60 months; a century is 1,200), so any legitimate
// consequence sits several orders of magnitude below this ceiling and a
// balance pass never reaches it.
const maxProjectionSeriesPoints = 100_000

// checkSeriesLength rejects a projected-consequence Series longer than the
// resource ceiling (SEC-202). It is the single choke point every Series-
// consuming path funnels through, so the bound is on the LENGTH (a resource
// concern) rather than on any series value magnitude (a balance concern —
// the values themselves are only required finite, per seriesFinite/GR#16).
func checkSeriesLength(series []float64, correlationID string) error {
	if len(series) > maxProjectionSeriesPoints {
		return errs.New(ErrProjectionSeriesTooLong, correlationID, map[string]any{
			"points": len(series),
			"max":    maxProjectionSeriesPoints,
		})
	}
	return nil
}

// seriesFinite reports whether every value in a projected Series is a finite
// IEEE-754 float. A NaN/±Inf value would flow into projectedDelta and then
// into engine.projections' queued step, poisoning the curve — so the whole
// series is validated at the write boundary (GR#16).
func seriesFinite(series []float64) bool {
	for _, v := range series {
		if !numFinite(v) {
			return false
		}
	}
	return true
}

// projectedDelta is the curve-visible step magnitude a funding cut applies
// from its CompletionMonth onward (the last-minus-first projected trend).
// The difference of two finite series values can overflow to ±Inf (SEC-200),
// so SetFunding finite-guards the result via num.GuardFinite before it reaches
// engine.projections — this function returns the raw value, never a guarded one.
func projectedDelta(cmd FundingCommand) float64 {
	if len(cmd.Projection.Series) == 0 {
		return 0
	}
	return cmd.Projection.Series[len(cmd.Projection.Series)-1] - cmd.Projection.Series[0]
}

// toProjectedConsequence converts this module's local projected-consequence
// payload into engine.projections' payload shape.
func toProjectedConsequence(cmd FundingCommand) *projections.ProjectedConsequence {
	series := make([]projections.Point, 0, len(cmd.Projection.Series))
	for i, v := range cmd.Projection.Series {
		series = append(series, projections.Point{
			Month:      cmd.Month + int64(i),
			Value:      v,
			Historical: i == 0,
			Confidence: projections.ConfidenceComputed,
		})
	}
	desc := cmd.Projection.Description
	if desc == "" {
		desc = fmt.Sprintf("social funding cut for %s to %.2f", cmd.Category.String(), cmd.Level)
	}
	return &projections.ProjectedConsequence{Description: desc, Series: series}
}

// RegisterProjectionProvider registers this module's open-caseload curve
// provider with engine.projections (AC-10) — the source of the projected-
// consequence payload a funding cut carries. The provider projects the
// current total open caseload flat (a placeholder trend; the real decade-long
// decay is a later balance concern). It must run at boot before any funding
// cut is submitted.
func (a *SocialAPI) RegisterProjectionProvider() error {
	if err := a.checkNotCopied("RegisterProjectionProvider"); err != nil {
		return err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.projections == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "projections", "operation": "RegisterProjectionProvider",
		})
	}
	return a.projections.RegisterCurveProvider(caseloadCurveKey, projections.CurveProviderFunc(func(monthIndex int64) (float64, error) {
		return float64(a.totalOpenCases()), nil
	}))
}

// totalOpenCases returns the current open-case count across all categories
// (the headline figure the projection provider projects forward).
func (a *SocialAPI) totalOpenCases() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var n int64
	for _, c := range a.cases {
		if c.Status == StatusOpen {
			n++
		}
	}
	return n
}
