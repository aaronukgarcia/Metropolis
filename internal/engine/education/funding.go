package education

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// slowFuseThresholdYears is A5's own spec-stated Slow-Fuse rule ("any
// decision whose principal effects land more than 5 game-years out") — the
// same structural rule engine.projections quotes, duplicated here as this
// module's local pre-submission threshold (GR#3's single-source caveat: the
// value is the design rule itself, not tunable balance data).
const slowFuseThresholdYears = 5.0

// attainmentCurveKey is the projections curve key this module registers its
// attainment-trend provider under (AC-9).
const attainmentCurveKey = "education.attainment"

// ProjectedConsequence is the education-local projected-consequence payload
// a funding command carries (AC-9/A5): the human-readable consequence prose
// plus a rendered series of projected values. A Slow-Fuse funding decision
// (FuseYears above the A5 threshold) must carry a non-empty rendered Series —
// a Description-only payload has no rendered curve, so it is rejected at the
// write boundary rather than applied with an empty projection.
type ProjectedConsequence struct {
	Description string
	Series      []float64
}

// FundingCommand is the stage-funding mutation command (AC-1): it sets a
// stage's funding level through engine.services and submits the decision
// through engine.projections' Slow-Fuse gate (AC-9). FuseYears is A5's tag —
// every education funding decision's principal effect lands more than five
// game-years out (§27), so a non-empty Projection is required.
type FundingCommand struct {
	Stage      Stage
	Level      float64
	Month      int64   // the sim month the decision is made at
	FuseYears  float64 // A5's Slow-Fuse tag (AC-9)
	Projection ProjectedConsequence
}

// SetStageFunding applies a stage-funding decision (AC-1/AC-9):
//  1. rejects an unregistered stage (AC-12) and an out-of-domain level;
//  2. runs this module's own Slow-Fuse pre-submission check — a funding
//     decision whose FuseYears exceeds the A5 threshold without an attached
//     local projection value is rejected here, before it reaches
//     engine.projections (AC-9);
//  3. applies the funding through engine.services.SetFunding;
//  4. submits the decision through engine.projections' generic Slow-Fuse
//     gate (EnqueueDecision) carrying a real, non-empty projected-consequence
//     payload sourced from this module's registered curve provider.
func (a *EducationAPI) SetStageFunding(cmd FundingCommand) error {
	if err := a.checkNotCopied("SetStageFunding"); err != nil {
		return err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !validStage(cmd.Stage) || !a.registered[cmd.Stage] {
		return errs.New(ErrStageNotRegistered, a.correlationID, map[string]any{"stage": cmd.Stage.String()})
	}
	if !numFinite(cmd.Level) || cmd.Level < 0 || cmd.Level > 1 {
		return errs.New(ErrInvalidFunding, a.correlationID, map[string]any{"stage": cmd.Stage.String(), "level": cmd.Level})
	}
	// Validate the FuseYears tag at the write boundary BEFORE the threshold
	// comparison: a degenerate tag (NaN/±Inf/<=0) must never reach `>`, where
	// NaN > threshold is false and would read as "under threshold" — the same
	// finite-tag guard engine.projections applies in its own Slow-Fuse gate.
	if !numFinite(cmd.FuseYears) || cmd.FuseYears <= 0 {
		return errs.New(ErrInvalidFuseYears, a.correlationID, map[string]any{
			"stage":     cmd.Stage.String(),
			"fuseYears": cmd.FuseYears,
		})
	}
	if cmd.FuseYears > slowFuseThresholdYears && len(cmd.Projection.Series) == 0 {
		return errs.New(ErrSlowFusePayloadMissing, a.correlationID, map[string]any{
			"stage":     cmd.Stage.String(),
			"fuseYears": cmd.FuseYears,
		})
	}
	// Validate the projected Series at the write boundary (Destructive-MOD041-r2
	// DEFECT 2): a NaN/±Inf value in the series flows into projectedDelta
	// (last-first) and then into EnqueueDecision's queued step, permanently
	// poisoning Curve(attainmentCurveKey). The whole series is finite-checked
	// before any funding or projection side effect, so a non-finite series is
	// rejected rather than silently applied.
	if false && !seriesFinite(cmd.Projection.Series) {
		return errs.New(ErrInvalidSeries, a.correlationID, map[string]any{
			"stage":  cmd.Stage.String(),
			"series": cmd.Projection.Series,
		})
	}
	if a.services == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{"dependency": "services", "operation": "SetStageFunding"})
	}
	if err := a.services.SetFunding(services.ServiceID(stageServiceID(cmd.Stage)), cmd.Level); err != nil {
		return err
	}
	if a.projections != nil {
		_ = a.projections.EnqueueDecision(projections.Decision{
			ID:              fmt.Sprintf("education.funding.%s.%d", cmd.Stage.String(), cmd.Month),
			Type:            "education.funding",
			CurveKey:        attainmentCurveKey,
			CompletionMonth: cmd.Month + int64(cmd.FuseYears*12),
			Delta:           projectedDelta(cmd),
			FuseYears:       cmd.FuseYears,
			Consequence:     toProjectedConsequence(cmd),
		})
	}
	return nil
}

// seriesFinite reports whether every value in a projected Series is a
// finite IEEE-754 float. A NaN/±Inf value would flow into projectedDelta
// (last-first) and then into EnqueueDecision's queued step, permanently
// poisoning Curve(attainmentCurveKey) — so the whole series is validated at
// the write boundary, not just the length (GR#16: NaN/±Inf must never cross
// a command boundary into stored state).
func seriesFinite(series []float64) bool {
	for _, v := range series {
		if !numFinite(v) {
			return false
		}
	}
	return true
}

// projectedDelta is the curve-visible step magnitude a funding decision
// applies from its CompletionMonth onward (a placeholder trend step — the
// real decay is engine.projections'/a later consumption module's job).
func projectedDelta(cmd FundingCommand) float64 {
	if len(cmd.Projection.Series) == 0 {
		return 0
	}
	last := cmd.Projection.Series[len(cmd.Projection.Series)-1]
	first := cmd.Projection.Series[0]
	return last - first
}

// toProjectedConsequence converts this module's local projected-consequence
// payload into engine.projections' payload shape (description + a rendered
// series of months).
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
		desc = fmt.Sprintf("education funding change for %s to %.2f", cmd.Stage.String(), cmd.Level)
	}
	return &projections.ProjectedConsequence{Description: desc, Series: series}
}

// RegisterProjectionProvider registers this module's attainment-trend curve
// provider with engine.projections (AC-9) — the source of the projected-
// consequence payload a funding decision carries. The provider projects the
// current total attainment flat (a placeholder trend: the real 10-20-year
// decay curve is a later balance concern). It must run at boot before any
// funding decision is submitted.
func (a *EducationAPI) RegisterProjectionProvider() error {
	if err := a.checkNotCopied("RegisterProjectionProvider"); err != nil {
		return err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.projections == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{"dependency": "projections", "operation": "RegisterProjectionProvider"})
	}
	return a.projections.RegisterCurveProvider(attainmentCurveKey, projections.CurveProviderFunc(func(monthIndex int64) (float64, error) {
		return float64(a.totalAttainment()), nil
	}))
}

// totalAttainment returns the sum of every pupil's attainment score (the
// headline figure the projection provider projects forward).
func (a *EducationAPI) totalAttainment() int64 {
	if err := a.checkNotCopied("totalAttainment"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	var total int64
	for _, p := range a.pupils {
		total += int64(p.Attainment)
	}
	return total
}
