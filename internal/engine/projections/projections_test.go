package projections

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// assertCode is this package's shared error-code assertion helper,
// matching engine.season/engine.market/engine.invariant's own
// test-suite convention.
func assertCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with code %s, got nil", wantCode)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != wantCode {
		t.Errorf("e.Code = %s, want %s (err: %v)", e.Code, wantCode, err)
	}
}

// --- AC-2: horizon is data-sourced, not a bare Go literal ----------------

func TestHorizonMonthsReadFromConfig(t *testing.T) {
	api := NewProjectionsAPI()
	horizon, err := api.HorizonMonths()
	if err != nil {
		t.Fatalf("HorizonMonths: %v", err)
	}
	if horizon <= 0 {
		t.Fatalf("HorizonMonths = %d, want a positive value from horizon.json", horizon)
	}
}

func TestHorizonProviderOverride(t *testing.T) {
	api := NewProjectionsAPI(WithHorizonProvider(func() (int64, error) { return 240, nil }))
	horizon, err := api.HorizonMonths()
	if err != nil {
		t.Fatalf("HorizonMonths: %v", err)
	}
	if horizon != 240 {
		t.Errorf("HorizonMonths = %d, want the overridden 240 (US-6's engine.unlocks seam)", horizon)
	}
}

// --- AC-3: seasonally-driven curves vary month-to-month, not a flat trend --

func TestSeasonalProjectionVariesAcrossHorizon(t *testing.T) {
	seasonAPI, err := season.LoadDefault(errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("season.LoadDefault: %v", err)
	}

	api := NewProjectionsAPI()
	provider := SeasonalCurveProvider{
		Base: func(monthIndex int64) (float64, error) { return 100, nil }, // flat base trend
		Multiplier: func(s *season.SeasonAPI, monthIndex int64) (float64, error) {
			return s.PowerDemandMultiplier(monthIndex)
		},
		Season: seasonAPI,
	}
	if err := api.RegisterCurveProvider("engine.power.demand", provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	points, err := api.Curve("engine.power.demand", 0, 23) // 24 months, 2 full years
	if err != nil {
		t.Fatalf("Curve: %v", err)
	}

	first := points[0].Value
	allEqual := true
	for _, p := range points[1:] {
		if p.Value != first {
			allEqual = false
			break
		}
	}
	if allEqual {
		t.Errorf("seasonal curve is flat across the horizon (all values = %v) — the seasonal multiplier was not composed into the projected series", first)
	}
}

// --- AC-6: Computed vs Extrapolated confidence ---------------------------

func TestConfidenceBeyondHorizonNotComputed(t *testing.T) {
	api := NewProjectionsAPI(WithHorizonProvider(func() (int64, error) { return 12, nil }))
	provider := fakeProvider{def: 5}
	if err := api.RegisterCurveProvider("test.curve", provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	if err := api.SetCurrentMonth(0); err != nil {
		t.Fatalf("SetCurrentMonth: %v", err)
	}

	points, err := api.Curve("test.curve", 0, 24)
	if err != nil {
		t.Fatalf("Curve: %v", err)
	}

	for _, p := range points {
		wantComputed := p.Month <= 12
		gotComputed := p.Confidence == ConfidenceComputed
		if gotComputed != wantComputed {
			t.Errorf("month %d: Confidence = %v (Computed=%v), want Computed=%v", p.Month, p.Confidence, gotComputed, wantComputed)
		}
	}

	// The specific beyond-horizon assertion the AC calls out by name.
	beyond := points[len(points)-1]
	if beyond.Confidence == ConfidenceComputed {
		t.Errorf("month %d (beyond horizon 12) is tagged Computed, want Extrapolated/Unavailable", beyond.Month)
	}
}

// --- AC-9: unregistered key / negative month, registry-sourced errors ----

func TestUnregisteredCurveKeyRejected(t *testing.T) {
	api := NewProjectionsAPI()
	points, err := api.Curve("nonexistent.curve", 0, 5)
	assertCode(t, err, ErrUnknownCurveKey)
	if points != nil {
		t.Errorf("Curve returned %+v for an unregistered key, want nil — no zero-value curve silently returned as if valid", points)
	}
}

func TestNegativeMonthQueryRejected(t *testing.T) {
	api := NewProjectionsAPI()
	provider := fakeProvider{def: 1}
	if err := api.RegisterCurveProvider("test.curve", provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	points, err := api.Curve("test.curve", -1, 5)
	assertCode(t, err, ErrNegativeMonthQuery)
	if points != nil {
		t.Errorf("Curve returned %+v for a negative fromMonth, want nil", points)
	}

	if err := api.SetCurrentMonth(-1); err == nil {
		t.Error("SetCurrentMonth(-1) unexpectedly succeeded")
	} else {
		assertCode(t, err, ErrNegativeMonthQuery)
	}
}

// --- AC-11: determinism across repeated calls and worker counts ----------

func TestDeterministicAcrossRepeatedCalls(t *testing.T) {
	api := NewProjectionsAPI()
	provider := fakeProvider{values: map[int64]float64{0: 1, 1: 2, 2: 3, 3: 4}}
	if err := api.RegisterCurveProvider("test.curve", provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	// A decision-marker step is included so the map-iteration-order
	// note in decisions.go's decisionStepsForKey is genuinely exercised,
	// not just the plain provider path.
	if err := api.EnqueueDecision(Decision{ID: "d1", CurveKey: "test.curve", CompletionMonth: 2, Delta: 100}); err != nil {
		t.Fatalf("EnqueueDecision: %v", err)
	}

	first, err := api.Curve("test.curve", 0, 3)
	if err != nil {
		t.Fatalf("Curve: %v", err)
	}

	for iter := 0; iter < 5; iter++ {
		got, err := api.Curve("test.curve", 0, 3)
		if err != nil {
			t.Fatalf("Curve (iter %d): %v", iter, err)
		}
		for i := range got {
			if got[i] != first[i] {
				t.Errorf("iter %d, index %d: got %+v, want %+v (non-deterministic)", iter, i, got[i], first[i])
			}
		}
	}
}

// TestDeterministicAcrossWorkerCounts is AC-11's "and across worker/
// shard counts" half: the same ProjectionsAPI queried concurrently
// from varying goroutine counts must return the identical series each
// time, proving the result depends only on registered state, never on
// how many workers happened to be querying it.
func TestDeterministicAcrossWorkerCounts(t *testing.T) {
	api := NewProjectionsAPI()
	provider := fakeProvider{values: map[int64]float64{0: 7, 1: 8, 2: 9}}
	if err := api.RegisterCurveProvider("test.curve", provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	want, err := api.Curve("test.curve", 0, 2)
	if err != nil {
		t.Fatalf("Curve: %v", err)
	}

	for _, workers := range []int{1, 4, 16} {
		var wg sync.WaitGroup
		results := make([][]Point, workers)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				got, err := api.Curve("test.curve", 0, 2)
				if err != nil {
					t.Errorf("worker %d Curve: %v", idx, err)
					return
				}
				results[idx] = got
			}(w)
		}
		wg.Wait()
		for idx, got := range results {
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("workers=%d worker=%d index=%d: got %+v, want %+v", workers, idx, i, got[i], want[i])
				}
			}
		}
	}
}

// --- AC-14: concurrent registration/query is race-free -------------------

func TestConcurrentRegisterAndQueryIsRaceFree(t *testing.T) {
	api := NewProjectionsAPI()

	var wg sync.WaitGroup
	errCh := make(chan error, 128)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := "test.curve." + itoa64(int64(idx))
			if err := api.RegisterCurveProvider(key, fakeProvider{def: float64(idx)}); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := "test.curve." + itoa64(int64(idx))
			if _, err := api.Curve(key, 0, 5); err != nil {
				errCh <- err
			}
			if _, err := api.MarginToInsolvency(0); err != nil {
				// Unknown-key error is expected here (no
				// CurveKeyFinanceInsolvencyRisk registered in this
				// test) — only a non-ErrUnknownCurveKey failure is a
				// real problem.
				if e, ok := err.(*errs.E); !ok || e.Code != ErrUnknownCurveKey {
					errCh <- err
				}
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent access error: %v", err)
	}
}

// TestConcurrentCurveQueriesNoArgClosure is AC-14's plainest shape: a
// fixed number of no-argument goroutines (Go 1.22+ per-iteration loop
// variable semantics make capturing the loop index directly safe)
// hammering the same registered curve concurrently, proving Curve
// itself is race-free under -race independent of any decision/
// margin machinery.
func TestConcurrentCurveQueriesNoArgClosure(t *testing.T) {
	api := NewProjectionsAPI()
	provider := fakeProvider{values: map[int64]float64{0: 1, 1: 2, 2: 3}}
	if err := api.RegisterCurveProvider("test.curve", provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := api.Curve("test.curve", 0, 2); err != nil {
				t.Errorf("Curve: %v", err)
			}
		}()
	}
	wg.Wait()
}
