package education

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// AC-9: a stage-funding command whose FuseYears exceeds the A5 threshold but
// carries no local projected-consequence value is rejected by this module's
// own pre-submission check — before it ever reaches engine.projections.
func TestSlowFuseLocalRejectsEmptyProjection(t *testing.T) {
	a, _, svc, _ := newWiredAPI(t, 20)

	err := a.SetStageFunding(FundingCommand{
		Stage:      StagePrimary,
		Level:      0.1, // a funding CUT
		Month:      8,
		FuseYears:  20,                     // > slowFuseThresholdYears
		Projection: ProjectedConsequence{}, // empty: no local projection value
	})
	if err == nil {
		t.Fatalf("expected ErrSlowFusePayloadMissing")
	}
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrSlowFusePayloadMissing {
		t.Fatalf("error code = %v, want %s", err, ErrSlowFusePayloadMissing)
	}
	// The funding must NOT have been applied (the pre-check ran first).
	level, _ := svc.FundingLevel(services.ServiceID(stageServiceID(StagePrimary)))
	if level != 0 {
		t.Fatalf("funding changed despite rejected pre-submission: %v", level)
	}
}

// AC-9: a stage-funding command WITH a FuseYears tag and a non-empty
// projection is applied and submitted through engine.projections' Slow-Fuse
// gate (the real submission call — RegisterCurveProvider + EnqueueDecision).
func TestFuseYearsSubmissionAppliesFunding(t *testing.T) {
	a, _, svc, proj := newWiredAPI(t, 21)

	err := a.SetStageFunding(FundingCommand{
		Stage:     StagePrimary,
		Level:     0.8,
		Month:     8,
		FuseYears: 20,
		Projection: ProjectedConsequence{
			Description: "primary funded to 0.8: projected attainment +30 over 20 years",
			Series:      []float64{0, 10, 20, 30},
		},
	})
	if err != nil {
		t.Fatalf("SetStageFunding with projection: %v", err)
	}

	// The funding side effect landed through engine.services.
	level, err := svc.FundingLevel(services.ServiceID(stageServiceID(StagePrimary)))
	if err != nil || level != 0.8 {
		t.Fatalf("funding = %v (err %v), want 0.8", level, err)
	}

	// The registered curve provider is queryable through engine.projections
	// (the submission's source), and the decision's future months are not
	// errored (the gate accepted the payload).
	if _, err := proj.Curve(attainmentCurveKey, 8, 16); err != nil {
		t.Fatalf("projected curve not registered/queryable: %v", err)
	}
}

// AC-9: a non-finite FuseYears tag (NaN/±Inf) is rejected before funding is
// applied — NaN > threshold is false, so it used to sail past the Slow-Fuse
// gate entirely, mirroring engine.projections' own BREAK-1 finite-tag guard.
func TestFundingRejectsNonFiniteFuseYears(t *testing.T) {
	a, _, svc, _ := newWiredAPI(t, 22)

	err := a.SetStageFunding(FundingCommand{
		Stage:     StagePrimary,
		Level:     0.8,
		Month:     8,
		FuseYears: math.NaN(),
		Projection: ProjectedConsequence{
			Description: "primary funded",
			Series:      []float64{0, 10},
		},
	})
	if err == nil {
		t.Fatalf("expected NaN FuseYears to be rejected")
	}
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrInvalidFuseYears {
		t.Fatalf("error code = %v, want %s", err, ErrInvalidFuseYears)
	}
	level, _ := svc.FundingLevel(services.ServiceID(stageServiceID(StagePrimary)))
	if level != 0 {
		t.Fatalf("funding applied despite rejected FuseYears: %v", level)
	}
}

// AC-9: a Description-only projection (no rendered Series) no longer
// satisfies the Slow-Fuse gate — it used to apply funding with no rendered
// curve, so a real projected-consequence payload must carry a non-empty
// Series, not just prose.
func TestFundingRejectsDescriptionOnlyProjection(t *testing.T) {
	a, _, svc, _ := newWiredAPI(t, 23)

	err := a.SetStageFunding(FundingCommand{
		Stage:     StagePrimary,
		Level:     0.8,
		Month:     8,
		FuseYears: 20,
		Projection: ProjectedConsequence{
			Description: "primary funded to 0.8, but no rendered series",
		},
	})
	if err == nil {
		t.Fatalf("expected Description-only projection to be rejected")
	}
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrSlowFusePayloadMissing {
		t.Fatalf("error code = %v, want %s", err, ErrSlowFusePayloadMissing)
	}
	level, _ := svc.FundingLevel(services.ServiceID(stageServiceID(StagePrimary)))
	if level != 0 {
		t.Fatalf("funding applied despite no rendered series: %v", level)
	}
}
