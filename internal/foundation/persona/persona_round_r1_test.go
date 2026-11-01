package persona

// FEAT-2326609842 round r1 (opus-round-r1-passport) attacker probes: the
// honesty of the stated collision odds and the visual distinctness claim.

import (
	"testing"
)

// The package doc states expected name-colliding pairs = n(n-1)/(2*NameSpace()).
// Measure the real count over the contiguous id range a city mints and report
// the ratio; the doc is only honest if observed <= ~1.5x stated.
func TestRoundR1_NameCollisionOddsHonest(t *testing.T) {
	const n = 200000
	seen := make(map[string]int, n)
	for id := uint64(1); id <= n; id++ {
		seen[Name(id)]++
	}
	var pairs float64
	for _, c := range seen {
		pairs += float64(c) * float64(c-1) / 2
	}
	stated := float64(n) * float64(n-1) / (2 * float64(NameSpace()))
	t.Logf("NameSpace=%d distinctObserved=%d observedPairs=%.0f statedPairs=%.0f ratio=%.2f", NameSpace(), len(seen), pairs, stated, pairs/stated)
	if pairs > 1.5*stated {
		t.Errorf("collision odds understated: observed %.0f pairs vs stated %.0f (x%.2f)", pairs, stated, pairs/stated)
	}
}

// Visual avatar distinctness: the rendered small attributes.
func TestRoundR1_AvatarVisualSpace(t *testing.T) {
	const n = 200000
	seen := map[[6]uint8]int{}
	for id := uint64(1); id <= n; id++ {
		a := AvatarOf(id)
		seen[[6]uint8{a.Skin, a.HairStyle, a.HairColor, a.Eyes, a.Outfit, a.Accessory}]++
	}
	var pairs float64
	for _, c := range seen {
		pairs += float64(c) * float64(c-1) / 2
	}
	t.Logf("avatar visual combos used=%d of %d; visually-identical pairs among %d = %.0f", len(seen), AvatarSkinTones*AvatarHairStyles*AvatarHairColours*AvatarEyeColours*AvatarOutfits*AvatarAccessories, n, pairs)
}

func TestRoundR1_AddressLabelInjectiveOnWorldGrid(t *testing.T) {
	// 6000-row world grid (30 tiles x 200 cells); count rows whose label band saturates.
	if AddressLabel(5, 250) == AddressLabel(5, 5999) {
		t.Logf("AddressLabel saturates: rows 250..5999 all render %q-band -> %d distinct homes per column share one label", AddressLabel(5, 250), 5999-250+1)
	}
}
