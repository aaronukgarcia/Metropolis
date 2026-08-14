package consumption

import "testing"

// TestCorridorCheaperThanCrossCountry is AC-7: an edge routed within a road
// corridor costs less than an equivalent-length cross-country edge (§2.2
// "built in road corridors for free, cross-country at cost").
func TestCorridorCheaperThanCrossCountry(t *testing.T) {
	corridor := Edge{From: "a", To: "b", LengthKm: 10, Corridor: true}
	crossCountry := Edge{From: "a", To: "b", LengthKm: 10, Corridor: false}

	if corridor.Cost() >= crossCountry.Cost() {
		t.Errorf("corridor cost %v should be strictly less than cross-country cost %v (AC-7)",
			corridor.Cost(), crossCountry.Cost())
	}
	if corridor.Cost() != 0 {
		t.Errorf("corridor-routed edge cost = %v, want 0 (free in road corridors, §2.2)", corridor.Cost())
	}
	if crossCountry.Cost() != 10*crossCountryCostPerKm {
		t.Errorf("cross-country cost = %v, want length × crossCountryCostPerKm = %v",
			crossCountry.Cost(), 10*crossCountryCostPerKm)
	}
}
