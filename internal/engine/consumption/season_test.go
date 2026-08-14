package consumption

import "testing"

// TestSeasonalMultiplierLayer is AC-11: seasonal multipliers are applied as
// a layer on top of coefficient-driven base demand, calling engine.season's
// SeasonAPI curve functions (never a locally re-implemented curve). A
// January gas-demand query returns base-demand × GasDemandMultiplier(0)
// exactly.
func TestSeasonalMultiplierLayer(t *testing.T) {
	api := realAPI(t)

	coef, err := api.ClassCoefficients("hotel") // gasKWh 18 per room-night
	if err != nil {
		t.Fatalf("ClassCoefficients: %v", err)
	}

	occupancy := 10.0
	// January = monthIndex 0. The multiplier is read from the SAME
	// engine.season SeasonAPI the module uses, so the expected value is
	// base × multiplier exactly, matching the module's own arithmetic order.
	mult, err := api.season.GasDemandMultiplier(0)
	if err != nil {
		t.Fatalf("GasDemandMultiplier(0): %v", err)
	}
	if mult != 2.2 {
		t.Fatalf("January gas multiplier = %v, want 2.2 (§17.1)", mult)
	}

	got, err := api.ClassDemand("hotel", occupancy, DemandOptions{MonthIndex: 0, GasNetworkPresent: true})
	if err != nil {
		t.Fatalf("ClassDemand: %v", err)
	}

	base := coef.GasKWh * occupancy
	want := base * mult
	if got.Gas != want {
		t.Errorf("January gas demand = %v, want base %v × multiplier %v = %v (§17.1 ×2.2 Jan)",
			got.Gas, base, mult, want)
	}
}
