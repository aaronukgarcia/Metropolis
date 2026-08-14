package consumption

import "testing"

// TestAllElectricStrategy is AC-10: with no gas network (GasNetworkPresent
// false), a building's gas demand (heating/cooking) reroutes to electricity
// demand — §17.1's "electric-heated homes shift this to E" — so the
// all-electric electricity demand exceeds the gas-present electricity
// demand, with no forced gas dependency. monthIndex 2 (March) keeps all
// seasonal multipliers at 1.0 so the shift is observed unmodified.
func TestAllElectricStrategy(t *testing.T) {
	api := realAPI(t)

	// hotel: 20 kWh elec + 18 kWh gas per room-night (§17.2) — a class with
	// real gas demand whose shift is observable.
	optsGas := DemandOptions{MonthIndex: 2, GasNetworkPresent: true}
	withGas, err := api.ClassDemand("hotel", 10, optsGas)
	if err != nil {
		t.Fatalf("ClassDemand(gas present): %v", err)
	}

	allElectric, err := api.ClassDemand("hotel", 10, DemandOptions{MonthIndex: 2, GasNetworkPresent: false})
	if err != nil {
		t.Fatalf("ClassDemand(all-electric): %v", err)
	}

	if allElectric.Power <= withGas.Power {
		t.Errorf("all-electric power %v should exceed gas-present power %v (heating load shifted, AC-10)",
			allElectric.Power, withGas.Power)
	}
	if allElectric.Gas != 0 {
		t.Errorf("all-electric gas demand = %v, want 0 (no gas network => no gas draw)", allElectric.Gas)
	}
	// The shift is 1:1 on energy (documented v1 assumption): power gains
	// exactly the gas demand that vanished.
	if allElectric.Power != withGas.Power+withGas.Gas {
		t.Errorf("all-electric power %v != gas-present power %v + gas %v (expected a 1:1 shift)",
			allElectric.Power, withGas.Power, withGas.Gas)
	}
}
