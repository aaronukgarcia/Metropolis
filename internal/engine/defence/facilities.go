package defence

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// personnelPurpose is the det.Stream purpose tag for deriving a deterministic
// citizen ID for a facility's personnel/children (AC-8/AC-13): the ID is a
// pure function of (worldSeed, facilityID, index), never a shared counter
// that could collide with another settlement path.
const personnelPurpose = "defence-personnel"

// Facility returns the read-only snapshot of one built facility (AC-9's
// queryable surface). A FacilityID that was never built returns ok=false —
// never a fabricated zero-value facility.
func (d *DefenceAPI) Facility(id FacilityID) (FacilityInfo, bool) {
	if err := d.checkNotCopied("Facility"); err != nil {
		return FacilityInfo{}, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	f, ok := d.facilities[id]
	if !ok {
		return FacilityInfo{}, false
	}
	return FacilityInfo{
		ID:              f.id,
		Type:            f.typ,
		Site:            f.site,
		MandateID:       f.mandateID,
		ChoiceID:        f.choiceID,
		Personnel:       f.personnel,
		MarriedQuarters: f.marriedQuarters,
		SchoolPlaces:    f.schoolPlaces,
		Payroll:         d.payrollOfLocked(f),
		Procurement:     f.procurement,
		Closed:          f.closed,
	}, true
}

// FacilityPayroll returns a facility's floor-protected payroll (AC-7): the
// nominal wage bill scaled by the recorded recession wage-bill factor, floored
// at the data-sourced payroll floor. With the shipped data (floor == nominal)
// a recession leaves the payroll at its pre-recession baseline — the §55
// anti-cyclical anchor.
func (d *DefenceAPI) FacilityPayroll(id FacilityID) (finance.Money, error) {
	if err := d.checkNotCopied("FacilityPayroll"); err != nil {
		return 0, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	f, ok := d.facilities[id]
	if !ok {
		return 0, errs.New(ErrNoFacility, d.correlationID, map[string]any{"facility": uint64(id)})
	}
	return d.payrollOfLocked(f), nil
}

// payrollOfLocked computes the floor-protected payroll (caller holds d.mu).
func (d *DefenceAPI) payrollOfLocked(f *facility) finance.Money {
	raw := f.nominalPayroll
	if d.wageBillFactor < 1 {
		raw = moneyTimesFactor(f.nominalPayroll, d.wageBillFactor)
	}
	if raw < f.payrollFloor {
		raw = f.payrollFloor
	}
	return raw
}

// moneyTimesFactor scales a money amount by a factor in [0,1] in exact int64
// fixed point (amount × round(factor×10000) / 10000), so a recession factor
// never bleeds float rounding into a money figure and never overflows
// (GR#16).
func moneyTimesFactor(amount finance.Money, factor float64) finance.Money {
	bp := int64(math.Round(factor * 10000)) // round to basis points (deterministic)
	if bp <= 0 {
		return 0
	}
	if bp >= 10000 {
		return amount
	}
	p, overflow := num.SafeMul(int64(amount), bp)
	if overflow {
		return finance.Money(math.MaxInt64)
	}
	return finance.Money(p / 10000)
}

// RecordRecession records a recession wage-bill factor (AC-7): the fraction
// an ordinary employer's wage bill retains under the recession. Defence
// payroll applies the same factor but floors at the data-sourced floor. The
// factor must be finite and in [0,1]; out-of-domain values are rejected,
// never clamped (a factor > 1 would be a boom, not a recession, and a NaN
// would silently disable the floor).
func (d *DefenceAPI) RecordRecession(factor float64) error {
	if err := d.checkNotCopied("RecordRecession"); err != nil {
		return err
	}
	if !num.IsFinite(factor) || factor < 0 || factor > 1 {
		return errs.New(ErrInvalidInput, d.correlationID, map[string]any{"field": "wageBillFactor", "value": factor})
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.wageBillFactor = factor
	return nil
}

// WageBillFactor returns the recorded recession wage-bill factor (default 1.0
// = no recession). Queryable so a test can assert the recession is actually
// in effect rather than the payroll merely being hardwired unchanged.
func (d *DefenceAPI) WageBillFactor() float64 {
	if err := d.checkNotCopied("WageBillFactor"); err != nil {
		return 1
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.wageBillFactor
}

// MarriedQuarters returns a facility's married-quarters count (AC-8): the
// number of forces-families households the facility settled as real
// engine.citizens household records.
func (d *DefenceAPI) MarriedQuarters(id FacilityID) (int64, error) {
	if err := d.checkNotCopied("MarriedQuarters"); err != nil {
		return 0, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	f, ok := d.facilities[id]
	if !ok {
		return 0, errs.New(ErrNoFacility, d.correlationID, map[string]any{"facility": uint64(id)})
	}
	return f.marriedQuarters, nil
}

// SchoolPlaceDemand returns a facility's forces-families school-place demand
// (AC-8): the data-sourced children-per-quarter count times the
// married-quarters count — a figure a downstream education consumer reads,
// backed by real child citizen records with a school stage.
func (d *DefenceAPI) SchoolPlaceDemand(id FacilityID) (int64, error) {
	if err := d.checkNotCopied("SchoolPlaceDemand"); err != nil {
		return 0, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	f, ok := d.facilities[id]
	if !ok {
		return 0, errs.New(ErrNoFacility, d.correlationID, map[string]any{"facility": uint64(id)})
	}
	return f.schoolPlaces, nil
}

// ProcurementContractValue returns a facility's defence-procurement contract
// value (AC-9): the queryable output a future engine.fdi consumer (the
// shipyard/rail-works/aerospace anchors) subscribes to. The cross-module
// award is blocked on BUG-058 (engine.fdi has zero registered consumers);
// this is the documented interim value.
func (d *DefenceAPI) ProcurementContractValue(id FacilityID) (finance.Money, error) {
	if err := d.checkNotCopied("ProcurementContractValue"); err != nil {
		return 0, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	f, ok := d.facilities[id]
	if !ok {
		return 0, errs.New(ErrNoFacility, d.correlationID, map[string]any{"facility": uint64(id)})
	}
	return f.procurement, nil
}

// CloseFacility closes a built facility (AC-10): a national-policy closure
// that records the closure facts — which facility, where, when, how many jobs
// lost — as a queryable [ClosureEvent]. The §32-scale shock itself is
// engine.spiral's derivation; this package produces the event and routes
// nothing until the engine.defence → engine.spiral edge lands (BUG-058).
// A second close of an already-closed facility returns ErrNoFacility — never
// a silently-double-counted closure.
func (d *DefenceAPI) CloseFacility(id FacilityID, month int64) (ClosureEvent, error) {
	if err := d.checkNotCopied("CloseFacility"); err != nil {
		return ClosureEvent{}, err
	}
	if month < 0 {
		return ClosureEvent{}, errs.New(ErrInvalidInput, d.correlationID, map[string]any{"field": "month", "value": month})
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	f, ok := d.facilities[id]
	if !ok || f.closed {
		return ClosureEvent{}, errs.New(ErrNoFacility, d.correlationID, map[string]any{"facility": uint64(id)})
	}
	f.closed = true
	return ClosureEvent{
		FacilityID:   f.id,
		FacilityType: f.typ,
		Site:         f.site,
		Month:        month,
		JobsLost:     f.personnel,
	}, nil
}

// settlePersonnelLocked creates the facility's personnel and forces-families
// children as REAL engine.citizens records, and forms the married-quarters
// households (AC-8). It is called from RespondToMandate while d.mu is held;
// citizens acquires its own lock and never calls back, so the lock order is
// defence → citizens only. The citizen IDs are deterministic pure functions
// of (worldSeed, facilityID, index), so settlement is reproducible across
// runs and worker counts (AC-13).
func (d *DefenceAPI) settlePersonnelLocked(c *citizens.CitizensAPI, id FacilityID, f *facility) error {
	if err := d.checkNotCopied("settlePersonnelLocked"); err != nil {
		return err
	}
	if f.personnel == 0 {
		return nil
	}

	// 1. Seed the adult personnel as employed public-sector citizens.
	adults := make([]citizens.ColdRecord, 0, f.personnel)
	for i := int64(0); i < f.personnel; i++ {
		adults = append(adults, d.personnelRecord(id, i, false))
	}
	if err := c.SeedColdRecords(adults, d.correlationID); err != nil {
		return err
	}

	// 2. Form the married-quarters households by pairing personnel in a
	// fixed order (i, i+1) — the same partner-in-pairs shape engine.attract's
	// own test helper uses, so the household records are real, not a bare
	// "jobs: N" aggregate.
	pairs := f.marriedQuarters
	if pairs*2 > f.personnel {
		pairs = f.personnel / 2 // guarded by Validate, but never pair past the roster
	}
	for i := int64(0); i < pairs; i++ {
		aID := d.personnelID(id, 2*i)
		bID := d.personnelID(id, 2*i+1)
		if err := c.ApplyLifeEventCommand(citizens.LifeEventCommand{
			CorrelationID: d.correlationID,
			Kind:          citizens.LifeEventPartner,
			CitizenID:     aID,
			PartnerID:     bID,
		}); err != nil {
			return err
		}
	}

	// 3. Seed the forces-families children as school-stage citizens.
	if f.schoolPlaces > 0 {
		children := make([]citizens.ColdRecord, 0, f.schoolPlaces)
		for i := int64(0); i < f.schoolPlaces; i++ {
			children = append(children, d.personnelRecord(id, f.personnel+i, true))
		}
		if err := c.SeedColdRecords(children, d.correlationID); err != nil {
			return err
		}
	}
	return nil
}

// personnelRecord builds a valid cold citizen record for a facility's
// personnel (adult, employed, public sector) or forces-family child
// (school-stage, unemployed). Values are deterministic placeholders (GR#15's
// balance regime) — the point is a REAL record, not a tuned biography.
func (d *DefenceAPI) personnelRecord(facilityID FacilityID, index int64, child bool) citizens.ColdRecord {
	var personality [citizens.NumPersonalityAxes]int8
	stage := citizens.StageAdultEd
	employment := citizens.EmploymentEmployed
	sector := citizens.SectorPublic
	childCount := uint8(0)
	if child {
		stage = citizens.StagePrimary
		employment = citizens.EmploymentStudent
		sector = citizens.SectorNone
	}
	return citizens.ColdRecord{
		ID:              d.personnelID(facilityID, index),
		BirthMonth:      0,
		Sex:             citizens.SexFemale,
		Personality:     personality,
		Stage:           stage,
		HealthBand:      citizens.HealthGood,
		Wealth:          100_000_000, // £100 — a placeholder, not a balance claim
		EmploymentState: employment,
		Sector:          sector,
		ChildCount:      childCount,
		SatHousing:      50,
		SatServices:     50,
		SatEnvironment:  50,
		SatLeisureFit:   50,
		SatCommute:      50,
	}
}

// personnelID derives a deterministic citizen ID for (facilityID, index) from
// the world seed — a pure function, so two settlements of the same facility
// produce the same IDs and no shared counter can collide (AC-8/AC-13).
func (d *DefenceAPI) personnelID(facilityID FacilityID, index int64) uint64 {
	stream := det.NewStream(d.worldSeed, uint64(facilityID), index, personnelPurpose)
	return stream.At(0)
}
