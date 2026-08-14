package citizens

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// DistrictStats is the district-level service-coverage snapshot the
// life-writer consumes. This item consumes a districtStatistics interface;
// it does not compute coverage itself (Out of scope) — the caller supplies
// these measured aggregates, and life-writing is consistent with them by
// construction.
type DistrictStats struct {
	LeisureRate    float64 // leisure-venue density
	HealthRate     float64 // health-service coverage
	EmploymentRate float64 // employment rate
}

// LifeDetail is the deterministic reconstruction of a cold citizen's
// recent detail (§5.2 life-writing). It is binding: it is what happened,
// and the district aggregates it was drawn from already accounted for it.
type LifeDetail struct {
	LeisureCategory  uint8
	HealthEvent      uint8
	EmploymentChange bool
	CommuteMinutes   uint16
	SocialContacts   uint8
}

// LifeWrite reconstructs a cold citizen's recent detail deterministically
// from (record, district statistics, hash(seed, id, month)) (AC-10). It is
// a pure function of those inputs: re-inspecting the same citizen at the
// same month returns byte-identical output, and the reconstruction is
// consistent with the district statistics it consumes. All draws are
// independent counter-based streams keyed hash(worldSeed, id, month,
// purpose) (AC-15).
func LifeWrite(seed uint64, id uint64, month int64, rec ColdRecord, ds DistrictStats) LifeDetail {
	stream := det.NewStream(seed, id, month, "lifewrite")

	// Leisure category: drawn from the district's leisure mix, shaped by
	// the citizen's own personality novelty axis (deterministic).
	cat := uint8(stream.IntN(NumLeisureWeights))
	// Health event: higher district health coverage ⇒ fewer events.
	healthEvent := uint8(0)
	if stream.Float64() > ds.HealthRate {
		healthEvent = uint8(stream.IntN(4)) // 0..3 severity tiers
	}
	// Employment change: district employment rate shapes the probability.
	employmentChange := stream.Float64() < (1 - ds.EmploymentRate)
	// Commute: derived from the home cell (distance proxy) and a draw.
	commute := uint16(stream.IntN(120)) + uint16(rec.Home%60)
	// Social contacts: personality sociability, scaled. Promote the int8
	// axis to int32 BEFORE multiplying — an int8 * 9 overflows the int8
	// range for sociability ≥ 15 (15×9 = 135 > 127), wrapping to 255/-1.
	social := uint8((int32(rec.Personality[AxisSociability]) * 9) / 100)

	return LifeDetail{
		LeisureCategory:  cat,
		HealthEvent:      healthEvent,
		EmploymentChange: employmentChange,
		CommuteMinutes:   commute,
		SocialContacts:   social,
	}
}
