package citizens

// Household is a real household entity (§5.4, AC-12): a membership set
// plus a dwelling size, from which overcrowding and rent burden are
// derivable. Households are formed by partnering events among hot/sampled
// citizens (statistically among cold, calibrated to the sample).
type Household struct {
	ID            uint64
	Members       []uint64 // member citizen ids
	DwellingRooms uint8    // dwelling size in rooms
}

// Overcrowded reports whether the household's membership exceeds its
// dwelling capacity (one room per member is the not-overcrowded line;
// more members than rooms is overcrowded). §5.4: overcrowding feeds
// satisfaction and the migration balance.
func (h Household) Overcrowded() bool {
	if h.DwellingRooms == 0 {
		return true // no dwelling ⇒ any membership is overcrowded
	}
	return len(h.Members) > int(h.DwellingRooms)
}

// RentBurdenRatio returns the household's rent burden: monthly rent as a
// fraction of monthly income. A ratio above 1.0 means rent exceeds income;
// §5.4's "rent burden feeds satisfaction". Pure and deterministic.
func (h Household) RentBurdenRatio(monthlyRentMicroPounds, monthlyIncomeMicroPounds int64) float64 {
	if monthlyIncomeMicroPounds <= 0 {
		// No income ⇒ unbounded burden; report a sentinel above 1.0 rather
		// than dividing by zero (GR#1: never a silent zero or NaN).
		return 2.0
	}
	ratio := float64(monthlyRentMicroPounds) / float64(monthlyIncomeMicroPounds)
	if ratio < 0 {
		return 0
	}
	return ratio
}

// pairingThreshold is the minimum membership a household retains before it
// is dissolved back into unpaired citizens (the "no household" sentinel). A
// household is formed as a pair (2 members — see FormHousehold); when a
// departure drops it below 2 the surviving member is alone and it is no
// longer a pair, so it is dissolved. LifeEventPartner forms at this size and
// LifeEventDeath unwires at this size: one shared pairing rule (GR#3).
const pairingThreshold = 2

// FormHousehold creates a new household containing both partners and
// returns it with a shared membership (AC-12: a partnering event creates
// a shared householdId for both partners — the caller writes that id onto
// both citizens' Household fields via the command surface).
func FormHousehold(id uint64, partnerA, partnerB uint64, dwellingRooms uint8) Household {
	return Household{
		ID:            id,
		Members:       []uint64{partnerA, partnerB},
		DwellingRooms: dwellingRooms,
	}
}
