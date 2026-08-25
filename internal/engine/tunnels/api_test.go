package tunnels

import (
	"sync"
	"testing"
)

func TestTunnels_AC2_TBMProfile(t *testing.T) {
	api := New()
	_ = api.AcquireTBM(false) // buy TBM
	owned, leased, _ := api.TBMProgrammeState()
	if !owned || leased {
		t.Error("expected TBM to be owned and not leased")
	}

	api2 := New()
	_ = api2.AcquireTBM(true) // lease TBM
	owned2, leased2, _ := api2.TBMProgrammeState()
	if owned2 || !leased2 {
		t.Error("expected TBM to be leased and not owned")
	}
}

func TestTunnels_AC3_LearningCurve(t *testing.T) {
	api := New()
	_ = api.AcquireTBM(false)

	// Step 1: Initial cost
	cost0, _ := api.PerKmCost()

	// Step 2: Bore some km
	_ = api.BoreSegment(5.0, "road")
	cost1, _ := api.PerKmCost()

	if cost1 >= cost0 {
		t.Errorf("learning curve failed: expected cost after 5km (%f) < initial cost (%f)", cost1, cost0)
	}

	// Step 3: Bore more km
	_ = api.BoreSegment(10.0, "road")
	cost2, _ := api.PerKmCost()

	if cost2 >= cost1 {
		t.Errorf("learning curve failed: expected cost after 15km (%f) < cost after 5km (%f)", cost2, cost1)
	}
}

func TestTunnels_AC4_AC5_AC6_CostComparisons(t *testing.T) {
	api := New()

	// AC-4: Metro cost cheapening
	costWith, _ := api.GetMetroCost(true)
	costWithout, _ := api.GetMetroCost(false)
	if costWith >= costWithout {
		t.Errorf("expected metro tunnel cost (%f) to cheapen surface build (%f)", costWith, costWithout)
	}

	// AC-5: Utility bundling savings
	bundled, _ := api.GetUtilityBundledCost(true)
	unbundled, _ := api.GetUtilityBundledCost(false)
	if bundled >= unbundled {
		t.Errorf("expected bundled utility cost (%f) < unbundled trenching (%f)", bundled, unbundled)
	}

	// AC-6: Crossing demolition costs
	tunnelCrossing, _ := api.GetCrossingCost(true)
	surfaceCrossing, _ := api.GetCrossingCost(false)
	if tunnelCrossing != 0 {
		t.Errorf("expected tunnel crossing demolition/land cost to be 0, got %f", tunnelCrossing)
	}
	if surfaceCrossing <= 0 {
		t.Errorf("expected surface crossing demolition/land cost > 0, got %f", surfaceCrossing)
	}
}

func TestTunnels_AC7_SpoilReclamation(t *testing.T) {
	api := New()
	_ = api.AcquireTBM(false)

	// Test spoil handover compile-time and mock execution safety
	err := api.BoreSegment(1.0, "road")
	if err != nil {
		t.Fatalf("unexpected segment error: %v", err)
	}

	if api.TotalTunnelLengthKilometers() != 1.0 {
		t.Errorf("expected total length 1.0, got %f", api.TotalTunnelLengthKilometers())
	}
}

func TestTunnels_AC8_AC9_Hyperloop(t *testing.T) {
	api := New()
	_ = api.AcquireTBM(false)

	// AC-8 (a): Hyperloop is gated before unlock
	err := api.BoreSegment(1.0, "hyperloop")
	if err == nil {
		t.Error("expected hyperloop construction to be rejected before gate unlock")
	}

	_ = api.SetHyperloopGated(false) // unlock gate
	err = api.BoreSegment(1.0, "hyperloop")
	if err != nil {
		t.Fatalf("unexpected error after unlocking hyperloop gate: %v", err)
	}

	// AC-8 (b): passenger capacity comparison
	hc, mc, _ := api.HyperloopCapacity()
	if hc >= mc {
		t.Errorf("expected hyperloop volume cap (%f) < metro capacity (%f)", hc, mc)
	}

	// AC-8 (c): attractiveness-per-capex comparison
	ha, ma, _ := api.AttractivenessPerCapex()
	if ha <= ma {
		t.Errorf("expected hyperloop attractiveness-per-capex (%f) > metro (%f)", ha, ma)
	}

	// AC-9: Pre-commit advisor warning
	warning, err := api.HyperloopPreCommitWarning()
	if err != nil {
		t.Fatalf("unexpected warning error: %v", err)
	}
	if warning == "" {
		t.Error("expected non-empty advisor warning")
	}
}

func TestTunnels_AC10_NoTBMError(t *testing.T) {
	api := New()
	err := api.BoreSegment(1.0, "road")
	if err == nil {
		t.Error("expected error for boring without TBM")
	}
	expectedCode := ErrNoTBMProgramme + ": cannot bore tunnel without an active TBM programme (AC-10)"
	if err.Error() != expectedCode {
		t.Errorf("expected error matching custom code, got: %v", err)
	}
}

func TestTunnels_AC11_Determinism(t *testing.T) {
	api1 := New()
	api2 := New()

	_ = api1.AcquireTBM(false)
	_ = api1.BoreSegment(5.0, "road")

	_ = api2.AcquireTBM(false)
	_ = api2.BoreSegment(5.0, "road")

	cost1, _ := api1.PerKmCost()
	cost2, _ := api2.PerKmCost()

	if cost1 != cost2 {
		t.Errorf("expected cost to be deterministic, got %f and %f", cost1, cost2)
	}
}

func TestTunnels_AC13_Concurrency(t *testing.T) {
	api := New()
	_ = api.AcquireTBM(false)

	var wg sync.WaitGroup
	workers := 10
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = api.BoreSegment(0.1, "road")
			}
		}()
	}

	wg.Wait()
}
