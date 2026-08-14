package finance

import "testing"

// TestLandPriceComponentsMonotonic (AC-4) varies each of the four
// component inputs independently and asserts LandPrice responds in the
// documented direction.
func TestLandPriceComponentsMonotonic(t *testing.T) {
	base := LandCell{
		Terrain:   TerrainGrass,
		Junction:  false,
		Roads:     0,
		Services:  0,
		CoastView: false,
		Pollution: 0,
		CitySize:  0,
	}

	// base(terrain): urban base exceeds grass base.
	urban := base
	urban.Terrain = TerrainUrban
	if got, want := LandPrice(urban), LandPrice(base); got <= want {
		t.Errorf("urban base price %d should exceed grass %d", got, want)
	}

	// access(junction, roads): a junction and more roads raise price.
	connected := base
	connected.Junction = true
	connected.Roads = 2
	if got, want := LandPrice(connected), LandPrice(base); got <= want {
		t.Errorf("better access %d should exceed baseline %d", got, want)
	}

	// amenity(services, coast, pollution): services/coast raise, pollution lowers.
	amenable := base
	amenable.Services = 3
	amenable.CoastView = true
	if got, want := LandPrice(amenable), LandPrice(base); got <= want {
		t.Errorf("better amenity %d should exceed baseline %d", got, want)
	}
	polluted := base
	polluted.Pollution = 50
	if got, want := LandPrice(polluted), LandPrice(base); got >= want {
		t.Errorf("higher pollution %d should be below baseline %d", got, want)
	}

	// scarcity(city size): a larger city raises price.
	big := base
	big.CitySize = 100_000
	if got, want := LandPrice(big), LandPrice(base); got <= want {
		t.Errorf("higher scarcity %d should exceed baseline %d", got, want)
	}
}

// TestLandPriceComponentFunctions (AC-4) asserts the four component
// functions are independently monotonic.
func TestLandPriceComponentFunctions(t *testing.T) {
	if AccessFactor(true, 2) <= AccessFactor(false, 0) {
		t.Error("AccessFactor should rise with a junction and roads")
	}
	if AmenityFactor(3, true, 0) <= AmenityFactor(0, false, 0) {
		t.Error("AmenityFactor should rise with services and coast view")
	}
	if AmenityFactor(0, false, 80) >= AmenityFactor(0, false, 0) {
		t.Error("AmenityFactor should fall as pollution rises")
	}
	if ScarcityFactor(10_000) <= ScarcityFactor(0) {
		t.Error("ScarcityFactor should rise with city size")
	}
}
