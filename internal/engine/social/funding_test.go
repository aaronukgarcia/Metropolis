package social

import (
	"errors"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestSlowFuseCutRejectsMissingProjection (AC-10): a funding CUT whose
// principal effect lands more than five game-years out is rejected by this
// module's own pre-submission check when it carries no local projected-
// consequence series — before it ever reaches engine.projections.
func TestSlowFuseCutRejectsMissingProjection(t *testing.T) {
	a, svc := wiredWithServices(t)

	// Baseline: fully fund family support (a non-cut increase, no projection
	// required).
	if err := a.SetFunding(FundingCommand{Category: CategoryFamilySupport, Level: 1.0, Month: 0}); err != nil {
		t.Fatalf("baseline funding: %v", err)
	}

	err := a.SetFunding(FundingCommand{Category: CategoryFamilySupport, Level: 0.5, Month: 1, FuseYears: 10})
	if err == nil {
		t.Fatal("expected a slow-fuse cut without a projection to be rejected")
	}
	if !errors.Is(err, &errs.E{Code: ErrSlowFusePayloadMissing}) {
		t.Fatalf("error code = %v, want %s", err, ErrSlowFusePayloadMissing)
	}
	_ = svc
}

// TestFuseYearsDegenerateRejected (AC-10): a funding cut with a NaN FuseYears
// tag must be rejected before the threshold comparison (NaN > threshold is
// false and would read as "under threshold").
func TestFuseYearsDegenerateRejected(t *testing.T) {
	a, _ := wiredWithServices(t)
	if err := a.SetFunding(FundingCommand{Category: CategoryFamilySupport, Level: 1.0, Month: 0}); err != nil {
		t.Fatalf("baseline funding: %v", err)
	}
	err := a.SetFunding(FundingCommand{Category: CategoryFamilySupport, Level: 0.5, Month: 1, FuseYears: math.NaN()})
	if err == nil {
		t.Fatal("expected a NaN FuseYears cut to be rejected")
	}
	if !errors.Is(err, &errs.E{Code: ErrInvalidFuseYears}) {
		t.Fatalf("error code = %v, want %s", err, ErrInvalidFuseYears)
	}
}

// TestFundingCutSubmitsSlowFuse (AC-10): a valid funding cut carries the
// FuseYears tag, registers the curve provider, and makes the REAL cross-module
// submission to engine.projections (RegisterCurveProvider + EnqueueDecision),
// so the projected consequence is observable through projections' curve.
func TestFundingCutSubmitsSlowFuse(t *testing.T) {
	a, svc := wiredWithServices(t)
	proj := projections.NewProjectionsAPI()
	if err := a.SetProjections(proj); err != nil {
		t.Fatalf("SetProjections: %v", err)
	}
	if err := a.RegisterProjectionProvider(); err != nil {
		t.Fatalf("RegisterProjectionProvider: %v", err)
	}
	if err := a.SetFunding(FundingCommand{Category: CategoryFamilySupport, Level: 1.0, Month: 0}); err != nil {
		t.Fatalf("baseline funding: %v", err)
	}

	cut := FundingCommand{
		Category:  CategoryFamilySupport,
		Level:     0.5,
		Month:     1,
		FuseYears: 10,
		Projection: ProjectedConsequence{
			Description: "family-support caseload projected to rise",
			Series:      []float64{10, 20},
		},
	}
	if err := a.SetFunding(cut); err != nil {
		t.Fatalf("SetFunding cut: %v", err)
	}

	// The enqueued decision's step (delta = 20-10 = 10) lands at
	// CompletionMonth = 1 + 10*12 = 121.
	pts, err := proj.Curve(caseloadCurveKey, 121, 121)
	if err != nil {
		t.Fatalf("Curve: %v", err)
	}
	if len(pts) != 1 || pts[0].Value != 10 {
		t.Fatalf("expected the enqueued delta (10) at the completion month, got %+v", pts)
	}
	_ = svc
}

// TestUnregisteredCategory (AC-13): a funding command targeting an
// unregistered category returns a registry-sourced error and creates no
// zero-value case record.
func TestUnregisteredCategory(t *testing.T) {
	a, _ := wiredWithServices(t)
	err := a.SetFunding(FundingCommand{Category: Category(99), Level: 0.5, Month: 0})
	if err == nil {
		t.Fatal("expected an unregistered category to be rejected")
	}
	if !errors.Is(err, &errs.E{Code: ErrUnknownCategory}) {
		t.Fatalf("error code = %v, want %s", err, ErrUnknownCategory)
	}
	if n := a.totalOpenCases(); n != 0 {
		t.Fatalf("no zero-value case record may be created, got %d open cases", n)
	}
}

// wiredWithServices builds a SocialAPI over a fresh services API with all five
// categories registered (the AC-4/AC-10/AC-13 funding setup).
func wiredWithServices(t *testing.T) (*SocialAPI, *services.ServicesAPI) {
	t.Helper()
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := services.New("test")
	if err := a.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if err := a.RegisterServices(); err != nil {
		t.Fatalf("RegisterServices: %v", err)
	}
	return a, svc
}
