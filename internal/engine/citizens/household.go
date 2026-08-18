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

// AddMember appends a new member (a couple's newborn child, FEAT-160) to a
// household's membership in place. It is the growth-side counterpart to
// removeHouseholdMemberLocked's pruning: FormHousehold fixes a household at
// exactly 2 members (the pairing size), but a household is not capped at 2
// for the lifetime of the game — children born to the couple join the same
// household without forming a new one.
func (h *Household) AddMember(id uint64) {
	h.Members = append(h.Members, id)
}

// Dissolution invariant (F1 fix, destructive-review REJECT on FEAT-160): a
// household is no longer dissolved by inferring "still paired" from raw
// membership count (len(Members) >= 2), because that stops being true once
// a couple has children — a widowed parent's Members slice still holds
// parent + children (>= 2 entries) even though the ADULT PAIRING is gone.
// The pairing is instead tracked explicitly via each citizen's own Partner
// field (Citizen.Partner / ColdRecord.Partner), never inferred from
// membership size. registry.go's removeHouseholdMemberLocked applies this
// invariant on departure (death/emigration):
//   - the departed member is always pruned from h.Members;
//   - if the departed member WAS one half of a pairing (their own Partner
//     field, resolved before their record is removed, names the survivor),
//     the survivor's Partner is cleared to 0 — the pairing dissolves, so the
//     survivor may legitimately re-partner later — but the survivor's
//     Household reference is left intact;
//   - the household itself persists for as long as ANY member remains
//     (surviving parent + children, or even a lone childless survivor who
//     keeps living in the same dwelling); it is deleted only once
//     len(Members) == 0.
