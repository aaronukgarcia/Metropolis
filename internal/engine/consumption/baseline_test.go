package consumption

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// TestResidentialBaselineMatchesSpec is AC-2: the per-person residential
// daily baseline is loaded from data/consumption.json and matches §17.1's
// transcribed figures (GR#15: expected values are transcribed from the
// spec into the test, not invented here).
func TestResidentialBaselineMatchesSpec(t *testing.T) {
	api := realAPI(t)
	b := api.ResidentialBaseline()

	cases := []struct {
		name string
		got  float64
		want float64
	}{
		{"water L/person/day", b.WaterLitresPerPersonPerDay, 145},
		{"electricity kWh/person/day", b.ElectricityKWhPerPersonPerDay, 3.5},
		{"gas kWh/person/day", b.GasKWhPerPersonPerDay, 13},
		{"food staples kg/person/day", b.FoodStaplesKgPerPersonPerDay, 1.4},
		{"food fresh kg/person/day", b.FoodFreshKgPerPersonPerDay, 0.7},
		{"household waste kg/person/day", b.HouseholdWasteKgPerPersonPerDay, 1.1},
		{"wastewater fraction of water", b.WastewaterFractionOfWater, 0.95},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v (§17.1)", c.name, c.got, c.want)
		}
	}
}

// TestClassCoefficientsMatchSpec is AC-3: every §17.2 class resolves, and
// five representative classes match §17.2's literal figures.
func TestClassCoefficientsMatchSpec(t *testing.T) {
	api := realAPI(t)

	// Every class §17.2 names must resolve (AC-3's "resolvable for every
	// class §17.2 names" clause).
	allClasses := []string{
		"school", "university", "clinic", "hospital", "elderCareHome",
		"office", "shop", "restaurantCafe", "hotel", "lightIndustry",
		"heavyIndustry", "leisureVenue", "stadium", "swimmingPoolLeisureCentre",
		"park", "stationRailMetro", "airport", "waterTreatmentWorks",
		"sewageWorks", "desalination",
	}
	for _, ref := range allClasses {
		if _, err := api.ClassCoefficients(ref); err != nil {
			t.Errorf("class %q should resolve against data/consumption.json, got: %v", ref, err)
		}
	}

	// Five representative classes match §17.2's literal figures.
	representatives := []struct {
		ref              string
		water, elec, gas float64
		waste            float64
	}{
		{"hospital", 400, 28, 30, 3.2},
		{"office", 25, 5, 2, 0.35},
		{"heavyIndustry", 400, 90, 60, 15},
		{"stationRailMetro", 2, 0.4, 0, 0.05},
		{"desalination", 0, 3.8, 0, 0},
	}
	for _, c := range representatives {
		coef, err := api.ClassCoefficients(c.ref)
		if err != nil {
			t.Fatalf("ClassCoefficients(%q): %v", c.ref, err)
		}
		if coef.WaterL != c.water || coef.ElecKWh != c.elec || coef.GasKWh != c.gas || coef.WasteKg != c.waste {
			t.Errorf("%s coefficients = {water %v, elec %v, gas %v, waste %v}, want {water %v, elec %v, gas %v, waste %v} (§17.2)",
				c.ref, coef.WaterL, coef.ElecKWh, coef.GasKWh, coef.WasteKg, c.water, c.elec, c.gas, c.waste)
		}
	}
}

// TestResolveConsumptionRef is AC-4: a data/buildings.json entry's
// consumptionRef resolves through UtilityAPI to a §17.2 class, and the
// resulting demand equals coefficient × occupancy. monthIndex 2 (March)
// has all three seasonal multipliers at 1.0 (see data/seasonal.json), so
// the base coefficient-driven demand is observed unmodified.
func TestResolveConsumptionRef(t *testing.T) {
	api := realAPI(t)

	dir, err := data.ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	buildings, err := data.LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}

	var entry data.BuildingEntry
	found := false
	for _, e := range buildings.Entries {
		if e.ConsumptionRef == "hospital" {
			entry = e
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no entry with consumptionRef \"hospital\" in data/buildings.json")
	}

	coef, err := api.ClassCoefficients(entry.ConsumptionRef)
	if err != nil {
		t.Fatalf("ClassCoefficients(%q): %v", entry.ConsumptionRef, err)
	}

	occupancy := 100.0
	got, err := api.ClassDemand(entry.ConsumptionRef, occupancy, DemandOptions{
		MonthIndex:        2, // March: power/water/gas seasonal multipliers all 1.0
		GasNetworkPresent: true,
	})
	if err != nil {
		t.Fatalf("ClassDemand(%q, %v): %v", entry.ConsumptionRef, occupancy, err)
	}

	if got.Water != coef.WaterL*occupancy {
		t.Errorf("water demand = %v, want coefficient %v × occupancy %v = %v", got.Water, coef.WaterL, occupancy, coef.WaterL*occupancy)
	}
	if got.Power != coef.ElecKWh*occupancy {
		t.Errorf("power demand = %v, want %v", got.Power, coef.ElecKWh*occupancy)
	}
	if got.Gas != coef.GasKWh*occupancy {
		t.Errorf("gas demand = %v, want %v", got.Gas, coef.GasKWh*occupancy)
	}
	if got.Waste != coef.WasteKg*occupancy {
		t.Errorf("waste demand = %v, want %v", got.Waste, coef.WasteKg*occupancy)
	}
}
