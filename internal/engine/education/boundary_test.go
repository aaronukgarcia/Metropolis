package education

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestMalformedStageRejectedNotPanic is the regression for
// Destructive-MOD041-r2 DEFECT 1 (crash): Stage is a uint8, so any value in
// [numStages, 255] used to index enrolled[s]/registered[s] out of range and
// panic. Each command/query boundary must instead return ErrStageNotRegistered
// — the error AC-12 already promised for a stage that does not exist — and
// never panic.
func TestMalformedStageRejectedNotPanic(t *testing.T) {
	a, _, _, _ := newWiredAPI(t, 40) // services wired + stages registered

	// Enrolment(255): the query boundary that indexed enrolled[s] directly.
	if _, err := a.Enrolment(Stage(255)); err == nil {
		t.Fatalf("Enrolment(255): expected ErrStageNotRegistered, got nil")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrStageNotRegistered {
		t.Fatalf("Enrolment(255): error code = %v, want %s", err, ErrStageNotRegistered)
	}

	// StageQuality(255) / StageCapacity(255): the query boundaries that
	// indexed registered[s] via stageServiceIDLocked.
	if _, err := a.StageQuality(Stage(255)); err == nil {
		t.Fatalf("StageQuality(255): expected ErrStageNotRegistered, got nil")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrStageNotRegistered {
		t.Fatalf("StageQuality(255): error code = %v, want %s", err, ErrStageNotRegistered)
	}
	if _, err := a.StageCapacity(Stage(255)); err == nil {
		t.Fatalf("StageCapacity(255): expected ErrStageNotRegistered, got nil")
	} else if e, ok := err.(*errs.E); !ok || e.Code != ErrStageNotRegistered {
		t.Fatalf("StageCapacity(255): error code = %v, want %s", err, ErrStageNotRegistered)
	}

	// SetStageFunding(Stage:255): the command boundary that indexed
	// registered[cmd.Stage] directly.
	err := a.SetStageFunding(FundingCommand{
		Stage: Stage(255), Level: 0.5, Month: 8, FuseYears: 20,
		Projection: ProjectedConsequence{Description: "x", Series: []float64{0, 10}},
	})
	if err == nil {
		t.Fatalf("SetStageFunding(Stage 255): expected ErrStageNotRegistered, got nil")
	}
	if e, ok := err.(*errs.E); !ok || e.Code != ErrStageNotRegistered {
		t.Fatalf("SetStageFunding(Stage 255): error code = %v, want %s", err, ErrStageNotRegistered)
	}
}

// TestFundingRejectsNonFiniteSeries is the regression for
// Destructive-MOD041-r2 DEFECT 2 (silent poison): a ProjectedConsequence.Series
// containing NaN/±Inf was accepted (only len(Series)==0 was checked), so
// projectedDelta (last-first) became non-finite and was swallowed into
// EnqueueDecision, making Curve(attainmentCurveKey) return NaN/±Inf
// permanently. The whole series must be finite-checked at the write boundary.
func TestFundingRejectsNonFiniteSeries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value float64
	}{
		{"NaN", math.NaN()},
		{"positive-infinity", math.Inf(1)},
		{"negative-infinity", math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, svc, proj := newWiredAPI(t, 41)
			// The non-finite value sits in the LAST position, so projectedDelta
			// (last-first) is non-finite — exactly the poison path the repro
			// identified.
			series := []float64{0, tc.value}
			err := a.SetStageFunding(FundingCommand{
				Stage: StagePrimary, Level: 0.8, Month: 8, FuseYears: 20,
				Projection: ProjectedConsequence{Description: "primary funded", Series: series},
			})
			if err == nil {
				t.Fatalf("expected non-finite Series to be rejected, got nil")
			}
			e, ok := err.(*errs.E)
			if !ok || e.Code != ErrInvalidSeries {
				t.Fatalf("error code = %v, want %s", err, ErrInvalidSeries)
			}

			// No funding side effect: the pre-check ran before SetFunding.
			level, _ := svc.FundingLevel(services.ServiceID(stageServiceID(StagePrimary)))
			if level != 0 {
				t.Fatalf("funding applied despite rejected non-finite Series: %v", level)
			}

			// The curve is NOT poisoned: every returned point stays finite.
			pts, err := proj.Curve(attainmentCurveKey, 8, 16)
			if err != nil {
				t.Fatalf("curve errored after rejected non-finite Series: %v", err)
			}
			for _, pt := range pts {
				if math.IsNaN(pt.Value) || math.IsInf(pt.Value, 0) {
					t.Fatalf("curve poisoned after rejected non-finite Series: point value %v", pt.Value)
				}
			}
		})
	}
}
