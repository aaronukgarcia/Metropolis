package social

import "github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing"

// This file defines social's local view of code.json's registered
// engine.social → engine.wellbeing outbound edge (the GR#20 "consume via
// registered interfaces" shape). Social needs exactly TWO values from
// wellbeing — the Crowding and FinancialStress driver magnitudes that feed
// the family-stress caseload input (AC-3) — so it consumes those through a
// narrow seam rather than reaching for wellbeing's full attribution surface.
// The real *wellbeing.WellbeingAPI is adapted by [WellbeingFamilyStress];
// tests inject a fake.

// FamilyStressQuery is the input social hands the family-stress source: the
// raw crowding and rent-burden figures, passed THROUGH to engine.wellbeing
// so wellbeing's own driver model (including its 35% rent-burden threshold)
// is the single computation. Social never re-derives crowding or the
// threshold itself (AC-3).
type FamilyStressQuery struct {
	CitizenID      uint64
	Month          int64
	PersonsPerRoom float64 // crowding input, passed through
	RentBurden     float64 // rent/income ratio, passed through (already computed by engine.citizens)
}

// FamilyStressResult is the two §18 family-stress driver magnitudes (≥ 0),
// as computed by engine.wellbeing — the input to this module's family-stress
// caseload term.
type FamilyStressResult struct {
	Crowding        float64
	FinancialStress float64
}

// FamilyStressSource is the seam over engine.wellbeing's two family-stress
// drivers — wellbeing.Crowding and wellbeing.FinancialStress (the §18
// MentalAttribution DriverDelta fields social consumes, AC-3). The real
// *wellbeing.WellbeingAPI satisfies it via [WellbeingFamilyStress].
type FamilyStressSource interface {
	FamilyStress(q FamilyStressQuery) (FamilyStressResult, error)
}

// WellbeingFamilyStress adapts *wellbeing.WellbeingAPI to social's
// FamilyStressSource seam. It calls wellbeing's pure Attribute engine with
// the passed-through PersonsPerRoom/RentBurden and reads the resulting
// Mental.Crowding and Mental.FinancialStress driver deltas — engine.
// wellbeing's registered Crowding and FinancialStress drivers — negating
// each (the deltas are ≤ 0) so the returned magnitudes are ≥ 0. This is the
// one place in this package that reaches wellbeing, so the family-stress
// input is never a locally-duplicated crowding/rent-burden model.
type WellbeingFamilyStress struct {
	Wellbeing *wellbeing.WellbeingAPI
}

// FamilyStress implements FamilyStressSource.
func (w WellbeingFamilyStress) FamilyStress(q FamilyStressQuery) (FamilyStressResult, error) {
	if w.Wellbeing == nil {
		return FamilyStressResult{}, nil // no source wired: degrade to zero stress (AC-14-style)
	}
	attr, err := w.Wellbeing.Attribute(q.CitizenID, q.Month, wellbeing.DriverInputs{
		PersonsPerRoom: q.PersonsPerRoom,
		RentBurden:     q.RentBurden,
	})
	if err != nil {
		return FamilyStressResult{}, err
	}
	return FamilyStressResult{
		Crowding:        stressMagnitude(attr.Mental.Crowding.Delta),
		FinancialStress: stressMagnitude(attr.Mental.FinancialStress.Delta),
	}, nil
}

// stressMagnitude turns a ≤ 0 driver delta into a ≥ 0 stress magnitude.
func stressMagnitude(delta float64) float64 {
	if delta >= 0 {
		return 0
	}
	return -delta
}
