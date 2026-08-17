package accelerator

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/consumption"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func testCorrelationID() string { return "test-accelerator" }

// loadTestAPI loads the real data/accelerator.json (via Load) and the real
// data/consumption.json UtilityAPI, so tests exercise the actual data files
// (GR#15 — expected figures come from data/runtime queries, never hardcoded).
func loadTestAPI(t *testing.T) (*AcceleratorAPI, *consumption.UtilityAPI) {
	t.Helper()
	dir, err := data.ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	a, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load accelerator: %v", err)
	}
	u, err := consumption.Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load consumption: %v", err)
	}
	return a, u
}

// testDeps is the bundle of fake dependencies a test wires into the API,
// exposing the observable side effects each seam records.
type testDeps struct {
	research  *fakeResearchSource
	permits   *fakePermits
	decomm    *fakeDecommission
	fdi       *fakeFdi
	wellbeing *fakeWellbeing
}

// wireAll wires every dependency with a default-permissive configuration:
// the gate is the real ThresholdGate over the loaded threshold, the permit
// is granted, and all side-effect sinks record their calls. The education
// research output is set to the given value.
func wireAll(t *testing.T, a *AcceleratorAPI, u *consumption.UtilityAPI, researchOutput int64) *testDeps {
	t.Helper()
	d := &testDeps{
		research:  &fakeResearchSource{output: researchOutput},
		permits:   &fakePermits{permitted: true},
		decomm:    &fakeDecommission{},
		fdi:       &fakeFdi{},
		wellbeing: &fakeWellbeing{},
	}
	if err := a.SetEducation(d.research); err != nil {
		t.Fatalf("SetEducation: %v", err)
	}
	if err := a.SetGate(ThresholdGate{Threshold: a.ExpertGateThreshold()}); err != nil {
		t.Fatalf("SetGate: %v", err)
	}
	if err := a.SetUtility(u); err != nil {
		t.Fatalf("SetUtility: %v", err)
	}
	if err := a.SetWellbeing(d.wellbeing); err != nil {
		t.Fatalf("SetWellbeing: %v", err)
	}
	if err := a.SetFdi(d.fdi); err != nil {
		t.Fatalf("SetFdi: %v", err)
	}
	if err := a.SetPermits(d.permits); err != nil {
		t.Fatalf("SetPermits: %v", err)
	}
	if err := a.SetDecommission(d.decomm); err != nil {
		t.Fatalf("SetDecommission: %v", err)
	}
	return d
}

// assertCode asserts err is a registry-sourced *errs.E carrying exactly the
// wanted code (AC-13 — the rejection is the claimed registry code, not a
// generic failure).
func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not a registry-sourced *errs.E", err)
	}
	if e.Code != want {
		t.Fatalf("error code = %q, want %q", e.Code, want)
	}
}

// --- fakes ---------------------------------------------------------------

type fakeResearchSource struct{ output int64 }

func (f *fakeResearchSource) ResearchPoints() int64 { return f.output }

type fakePermits struct{ permitted bool }

func (f *fakePermits) HasPermit(facilityKey string) (bool, error) { return f.permitted, nil }

// fakeDecommission records the last facility key accrued. err, when non-nil,
// makes AccrueLiability fail cleanly (without recording) — for the AC-13
// no-partial-state rejection tests.
type fakeDecommission struct {
	accrued string
	err     error
}

func (f *fakeDecommission) AccrueLiability(facilityKey string) error {
	if f.err != nil {
		return f.err
	}
	f.accrued = facilityKey
	return nil
}

// fakeFdi models engine.fdi's queryable prospect figure as a running total,
// so a test can observe the accelerator's anchor draw raising it (AC-9).
// err, when non-nil, makes both AddAnchorProspect and its compensating
// RemoveAnchorProspect fail — for the AC-13 no-partial-state rejection tests.
type fakeFdi struct {
	prospects int64
	err       error
}

func (f *fakeFdi) AddAnchorProspect(magnitude int64) error {
	if f.err != nil {
		return f.err
	}
	f.prospects += magnitude
	return nil
}

func (f *fakeFdi) RemoveAnchorProspect(magnitude int64) error {
	if f.err != nil {
		return f.err
	}
	f.prospects -= magnitude
	return nil
}

// fakeWellbeing models the wellbeing outcome as a running score the health
// spillover raises, so a test can observe it improving when the accelerator
// is online (AC-8).
type fakeWellbeing struct {
	outcome float64
	posts   int
}

func (f *fakeWellbeing) PostHealthSpillover(magnitude float64) error {
	f.outcome += magnitude
	f.posts++
	return nil
}
